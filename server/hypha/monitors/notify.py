"""Envoi des notifications sans bloquer la boucle du worker (contrat §§ 7, 18).

Canaux : courriel (SMTP, HTML + texte), webhook, Slack, Teams, Telegram, Discord, Google Chat,
Mattermost (format Slack), ntfy, PagerDuty (Events API v2 : trigger/resolve) et Opsgenie
(create/close par alias). Délai de 10 s par appel, deux reprises espacées.

Les envois du worker passent par une file traitée en tâche de fond. Une notification perdue (file
pleine, worker arrêté) est journalisée ; l'état du moniteur, lui, est déjà en base avec son
événement. L'API (essai d'un canal, incidents, rapports) envoie directement par `deliver_all`.
"""
import asyncio
import html
import logging
import smtplib
import ssl
from dataclasses import dataclass, field
from email.message import EmailMessage
from urllib.parse import quote

import httpx

from ..config import Settings
from ..synthetics.netguard import TargetRefused, check_url

log = logging.getLogger("hypha.notify")

STATE_COLORS = {"OK": "2EB67D", "WARN": "ECB22E", "ALERT": "E01E5A", "NODATA": "888888"}
STATE_LABELS = {"OK": "rétabli", "WARN": "avertissement", "ALERT": "alerte", "NODATA": "sans données"}
PRIORITY_LABELS = {1: "P1 critique", 2: "P2 haute", 3: "P3 moyenne", 4: "P4 basse", 5: "P5 info"}
TIMEOUT_S = 10
ATTEMPTS = ((1, 2), (2, 10), (3, 0))          # essai, pause avant le suivant
PAGERDUTY_URL = "https://events.pagerduty.com/v2/enqueue"
OPSGENIE_URLS = {"eu": "https://api.eu.opsgenie.com", "us": "https://api.opsgenie.com"}
PREFIX = {"renotify": "[relance] ", "escalation": "[escalade] ", "flapping": "[instable] ",
          "reminder": "[toujours] ", "test": "[essai] "}


@dataclass
class Notice:
    channel: dict             # ligne notification_channels
    monitor: dict             # {id, name, type, priority?}
    group: str
    state: str
    previous: str | None
    value: float | None
    message: str
    url: str
    renotify: bool = False
    allow_private: bool = False
    kind: str = "alert"       # alert | renotify | escalation | flapping | reminder | test | incident | report
    subject_override: str | None = None
    threshold: float | None = None
    explain_url: str | None = None
    logs: list = field(default_factory=list)      # 5 dernières lignes d'erreur de l'hôte (courriel)
    html_body: str | None = None                   # rapport : HTML déjà rendu
    dedup_key: str | None = None

    @property
    def subject(self) -> str:
        if self.subject_override:
            return self.subject_override
        prefix = "[relance] " if self.renotify and self.kind == "alert" else PREFIX.get(self.kind, "")
        group = "" if self.group == "*" else f" ({self.group})"
        prio = self.monitor.get("priority")
        p = f"[P{prio}] " if prio else ""
        return f"{prefix}[HYPHA] [{self.state}] {p}{self.monitor['name']}{group}"

    @property
    def key(self) -> str:
        return self.dedup_key or f"{self.monitor.get('id')}:{self.group}"

    def webhook_payload(self) -> dict:
        return {"monitor": self.monitor, "group": self.group, "state": self.state, "value": self.value,
                "message": self.message, "url": self.url, "kind": self.kind,
                "priority": self.monitor.get("priority"), "previous": self.previous}


class Notifier:
    def __init__(self, settings: Settings, max_queue: int = 2000, concurrency: int = 5):
        self.settings = settings
        self.queue: asyncio.Queue[Notice] = asyncio.Queue(maxsize=max_queue)
        self.sem = asyncio.Semaphore(concurrency)
        self.client: httpx.AsyncClient | None = None
        self._tasks: set[asyncio.Task] = set()

    def submit(self, notice: Notice) -> None:
        try:
            self.queue.put_nowait(notice)
        except asyncio.QueueFull:
            log.error("file de notifications pleine : %s perdue", notice.subject)

    async def run(self) -> None:
        self.client = make_client()
        try:
            while True:
                notice = await self.queue.get()
                task = asyncio.create_task(self._deliver(notice))
                self._tasks.add(task)
                task.add_done_callback(self._tasks.discard)
        finally:
            for t in list(self._tasks):
                t.cancel()
            await self.client.aclose()

    async def _deliver(self, n: Notice) -> None:
        async with self.sem:
            await deliver(self.settings, self.client, n)


def make_client() -> httpx.AsyncClient:
    return httpx.AsyncClient(timeout=TIMEOUT_S, follow_redirects=False, headers={"User-Agent": "HYPHA-Notifier/0.4"})


async def deliver(settings: Settings, client: httpx.AsyncClient, n: Notice) -> tuple[bool, str]:
    """Trois essais au plus ; rend (réussi, détail)."""
    last = ""
    for attempt, pause in ATTEMPTS:
        try:
            await send(settings, client, n)
            return True, "envoyé"
        except TargetRefused as e:
            log.error("canal %s refusé : %s", n.channel["id"], e)
            return False, f"adresse refusée : {e}"
        except Exception as e:
            last = str(e) or type(e).__name__
            log.warning("envoi %s vers le canal %s (essai %s) en échec : %s",
                        n.channel["kind"], n.channel["id"], attempt, last)
            if pause:
                await asyncio.sleep(pause)
    log.error("notification abandonnée après 3 essais : %s", n.subject)
    return False, last


async def deliver_all(settings: Settings, notices: list[Notice]) -> list[tuple[bool, str]]:
    """Envoi direct (hors worker), canal par canal ; une erreur n'empêche pas les suivants."""
    if not notices:
        return []
    async with make_client() as client:
        return list(await asyncio.gather(*(deliver(settings, client, n) for n in notices)))


MONITOR_KINDS = ("alert", "renotify", "escalation", "flapping", "reminder")


def text_body(n: Notice) -> str:
    lines = [n.message, ""]
    if n.kind in MONITOR_KINDS:
        lines += [f"État : {n.state} ({STATE_LABELS.get(n.state, n.state)})"]
        if n.group != "*":
            lines.append(f"Groupe : {n.group}")
        if n.value is not None:
            lines.append(f"Valeur : {n.value}")
        if n.threshold is not None:
            lines.append(f"Seuil : {n.threshold}")
    lines += ["", n.url]
    if n.explain_url:
        lines.append(f"Explication : {n.explain_url}")
    if n.logs:
        lines += ["", "Dernières erreurs de l'hôte :"] + [f"  {line}" for line in n.logs]
    return "\n".join(lines) + "\n"


def alert_html(n: Notice) -> str:
    """Courriel HTML sobre : bandeau coloré par état, valeur, seuil, liens, dernières erreurs."""
    e = html.escape
    color = STATE_COLORS.get(n.state, "888888")
    rows = []
    if n.kind in MONITOR_KINDS:
        rows.append(("État", f"{n.state} ({STATE_LABELS.get(n.state, n.state)})"))
        if n.monitor.get("priority"):
            rows.append(("Priorité", PRIORITY_LABELS.get(n.monitor["priority"], str(n.monitor["priority"]))))
    if n.group != "*":
        rows.append(("Groupe", n.group))
    if n.value is not None:
        rows.append(("Valeur", f"{n.value:.4g}" if isinstance(n.value, float) else str(n.value)))
    if n.threshold is not None:
        rows.append(("Seuil", f"{n.threshold:g}"))
    table = "".join(f'<tr><td style="padding:4px 12px 4px 0;color:#666">{e(k)}</td>'
                    f'<td style="padding:4px 0"><strong>{e(str(v))}</strong></td></tr>' for k, v in rows)
    links = f'<a href="{e(n.url)}" style="color:#1d4ed8">Ouvrir dans HYPHA</a>'
    if n.explain_url:
        links += f' · <a href="{e(n.explain_url)}" style="color:#1d4ed8">Expliquer avec l\'IA</a>'
    logs = ""
    if n.logs:
        logs = ('<p style="margin:16px 0 4px;color:#666">Dernières erreurs de l\'hôte</p>'
                '<pre style="background:#f4f4f5;padding:8px;font-size:12px;white-space:pre-wrap;margin:0">'
                + e("\n".join(n.logs)) + "</pre>")
    message = e(n.message).replace("\n", "<br>")
    return (f'<!doctype html><html><body style="margin:0;padding:16px;background:#ffffff">'
            f'<div style="font-family:system-ui,-apple-system,Segoe UI,Arial,sans-serif;max-width:640px;color:#18181b">'
            f'<div style="border-left:6px solid #{color};background:#fafafa;padding:10px 14px;font-size:16px">'
            f'<strong>{e(n.subject)}</strong></div>'
            f'<p style="margin:16px 0">{message}</p>'
            f'<table style="border-collapse:collapse;font-size:14px">{table}</table>'
            f'<p style="margin:16px 0">{links}</p>{logs}'
            f'<p style="margin:24px 0 0;color:#999;font-size:12px">HYPHA, supervision S33D</p>'
            f'</div></body></html>')


def _pd_severity(state: str) -> str:
    return {"ALERT": "critical", "WARN": "warning", "NODATA": "warning"}.get(state, "info")


def pagerduty_event(n: Notice, routing_key: str, action: str | None = None) -> dict:
    action = action or ("resolve" if n.state == "OK" else "trigger")
    body = {"routing_key": routing_key, "event_action": action, "dedup_key": n.key[:255]}
    if action == "trigger":
        host = next((p.partition(":")[2] for p in n.group.split(",") if p.startswith("host:")), None)
        body["payload"] = {"summary": f"{n.subject} — {n.message}"[:1024], "source": host or "hypha",
                           "severity": "info" if n.kind == "test" else _pd_severity(n.state),
                           "component": n.monitor.get("name"), "group": n.group, "class": n.monitor.get("type"),
                           "custom_details": {"value": n.value, "state": n.state, "message": n.message}}
        body["links"] = [{"href": n.url, "text": "HYPHA"}]
        body["client"], body["client_url"] = "HYPHA", n.url
    return body


def opsgenie_create(n: Notice) -> dict:
    prio = n.monitor.get("priority")
    return {"message": n.subject[:130], "alias": n.key[:512], "description": text_body(n)[:15000],
            "priority": f"P{prio}" if prio in (1, 2, 3, 4, 5) else ("P5" if n.kind == "test" else "P3"),
            "source": "HYPHA", "tags": ["hypha", str(n.monitor.get("type") or "monitor")],
            "details": {"group": n.group, "state": n.state, "url": n.url}}


def telegram_text(n: Notice) -> str:
    return f"{n.subject}\n{n.message}\n{n.url}"[:4000]


def discord_payload(n: Notice) -> dict:
    return {"embeds": [{"title": n.subject[:256], "description": n.message[:4000], "url": n.url,
                        "color": int(STATE_COLORS.get(n.state, "888888"), 16)}]}


def ntfy_payload(n: Notice, topic: str) -> dict:
    return {"topic": topic, "title": n.subject[:250], "message": n.message[:4000] or n.subject, "click": n.url,
            "priority": {"ALERT": 5, "WARN": 4, "NODATA": 3}.get(n.state, 3),
            "tags": [{"ALERT": "rotating_light", "WARN": "warning", "OK": "white_check_mark"}.get(n.state, "bell")]}


async def _post(client, url, payload=None, headers=None, allow_private=False):
    await check_url(url, allow_private)
    r = await client.post(url, json=payload, headers=headers or {})
    if not 200 <= r.status_code < 300:
        raise RuntimeError(f"réponse {r.status_code}")
    return r


async def send(settings: Settings, client: httpx.AsyncClient, n: Notice) -> None:
    kind, cfg = n.channel["kind"], n.channel.get("config") or {}
    if kind == "email":
        await asyncio.to_thread(send_email, settings, cfg.get("to") or [], n)
        return
    if kind == "telegram":
        url = f"https://api.telegram.org/bot{cfg.get('bot_token', '')}/sendMessage"
        r = await client.post(url, json={"chat_id": cfg.get("chat_id"), "text": telegram_text(n),
                                          "disable_web_page_preview": True})
        if r.status_code != 200:
            raise RuntimeError(f"réponse {r.status_code} de Telegram")
        return
    if kind == "pagerduty":
        if n.kind == "test":
            await _post(client, PAGERDUTY_URL, pagerduty_event(n, cfg.get("routing_key", ""), "trigger"))
            await _post(client, PAGERDUTY_URL, pagerduty_event(n, cfg.get("routing_key", ""), "resolve"))
            return
        await _post(client, PAGERDUTY_URL, pagerduty_event(n, cfg.get("routing_key", "")))
        return
    if kind == "opsgenie":
        base = OPSGENIE_URLS.get(cfg.get("region", "eu"), OPSGENIE_URLS["eu"])
        headers = {"Authorization": f"GenieKey {cfg.get('api_key', '')}"}
        close = f"{base}/v2/alerts/{quote(n.key[:512], safe='')}/close?identifierType=alias"
        if n.state != "OK" or n.kind == "test":
            await _post(client, f"{base}/v2/alerts", opsgenie_create(n), headers)
        if n.state == "OK" or n.kind == "test":
            await _post(client, close, {"source": "HYPHA", "note": n.message[:1000]}, headers)
        return
    url = cfg.get("url", "")
    if kind == "ntfy":
        headers = {"Authorization": f"Bearer {cfg['token']}"} if cfg.get("token") else {}
        await _post(client, url.rstrip("/"), ntfy_payload(n, cfg.get("topic", "")), headers, n.allow_private)
        return
    if kind == "webhook":
        payload = n.webhook_payload()
        headers = {k: v for k, v in (cfg.get("headers") or {}).items() if isinstance(v, str)}
    elif kind in ("slack", "mattermost"):
        payload = {"text": f"*{n.subject}*\n{n.message}\n<{n.url}|Ouvrir dans HYPHA>"}
        headers = {}
    elif kind == "googlechat":
        payload = {"text": f"*{n.subject}*\n{n.message}\n{n.url}"}
        headers = {}
    elif kind == "discord":
        payload = discord_payload(n)
        headers = {}
    else:
        payload = teams_card(n)
        headers = {}
    await _post(client, url, payload, headers, n.allow_private)


def send_email(settings: Settings, to: list[str], n: Notice) -> None:
    s = settings
    if not s.smtp_host or not to:
        raise RuntimeError("SMTP non configuré (HYPHA_SMTP_HOST) ou destinataire absent")
    msg = EmailMessage()
    msg["Subject"] = n.subject
    msg["From"] = s.smtp_from or s.smtp_user
    msg["To"] = ", ".join(to)
    msg.set_content(text_body(n))
    msg.add_alternative(n.html_body or alert_html(n), subtype="html")
    ctx = ssl.create_default_context()
    if s.smtp_security == "ssl":
        server = smtplib.SMTP_SSL(s.smtp_host, s.smtp_port, timeout=20, context=ctx)
    else:
        server = smtplib.SMTP(s.smtp_host, s.smtp_port, timeout=20)
    with server:
        if s.smtp_security == "starttls":
            server.starttls(context=ctx)
        if s.smtp_user:
            server.login(s.smtp_user, s.smtp_password)
        server.send_message(msg)


def teams_card(n: Notice) -> dict:
    """Carte adaptative : format accepté par les webhooks entrants Teams (flux Workflows)."""
    return {
        "type": "message",
        "attachments": [{
            "contentType": "application/vnd.microsoft.card.adaptive",
            "content": {
                "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
                "type": "AdaptiveCard", "version": "1.4",
                "body": [
                    {"type": "TextBlock", "size": "Medium", "weight": "Bolder", "text": n.subject, "wrap": True,
                     "color": {"ALERT": "Attention", "WARN": "Warning", "OK": "Good"}.get(n.state, "Default")},
                    {"type": "TextBlock", "text": n.message, "wrap": True},
                ],
                "actions": [{"type": "Action.OpenUrl", "title": "Ouvrir dans HYPHA", "url": n.url}],
            },
        }],
    }
