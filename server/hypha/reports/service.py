"""Rapports programmés (contrat § 22) : rassemblement des chiffres, rendu, envoi par courriel, tâche du worker.

Un rapport échu est marqué envoyé AVANT l'envoi : un SMTP en panne fait perdre un rapport (journalisé)
plutôt que d'en envoyer dix à la reprise.
"""
import logging
from datetime import datetime, timedelta, timezone

from ..config import Settings
from ..db import fetch_all, fetch_one
from ..errors import QueryError
from ..monitors.notify import Notice, deliver_all
from ..query import metrics_exec, metrics_ql as mql
from ..slo import compute as SC
from ..slo.service import FIELDS as SLO_FIELDS, slo_status
from ..status.service import _changes, _monitor_history
from ..util import dt_to_ms, now_ms, parse_group_key, real_value
from . import render as R
from . import schedule as SCH

log = logging.getLogger("hypha.reports")

FIELDS = """id, name, kind, dashboard_id, channel_ids, schedule, period_s, enabled, last_sent_at, created_by,
            created_at"""
WIDGETS = ("query_value", "toplist", "timeseries")
PRIORITY_LABELS = {1: "P1", 2: "P2", 3: "P3", 4: "P4", 5: "P5", None: "non classée"}


def _fmt_dt(dt: datetime | None) -> str:
    return dt.astimezone(timezone.utc).strftime("%d/%m/%Y %H:%M UTC") if dt else "—"


async def _hosts_availability(conn, org_id: int, start: datetime, now: datetime) -> str:
    monitors = await fetch_all(conn, "SELECT id, no_data_s FROM monitors WHERE org_id = %s AND type = 'host' AND enabled",
                               (org_id,))
    if not monitors:
        return R.paragraph("Aucun moniteur « hôte sans nouvelles » : disponibilité des hôtes non suivie.")
    hist = await _monitor_history(conn, org_id, [m["id"] for m in monitors], start, now)
    per_host: dict[str, list] = {}
    a, b = start.timestamp(), now.timestamp()
    for m in monitors:
        h = hist[m["id"]]
        groups = set(h["current"]) | {r["group_key"] for r in h["before"] + h["inside"]}
        for g in groups:
            host = parse_group_key(g).get("host")
            if not host:
                continue
            before = _changes([r for r in h["before"] if r["group_key"] == g])
            inside = _changes([r for r in h["inside"] if r["group_key"] == g])
            current = {g: h["current"][g]} if g in h["current"] else {}
            per_host.setdefault(host, []).extend(SC.alert_intervals(before, inside, current, a, b, m["no_data_s"]))
    rows = []
    for host, intervals in per_host.items():
        down = SC.covered(SC.merge(intervals), a, b)
        rows.append((100.0 * (1 - down / (b - a)), host, down))
    rows.sort()
    perfect = sum(1 for up, _, _ in rows if up >= 100)
    shown = [[host, f"{up:.2f} %", R.duration(down)] for up, host, down in rows if up < 100][:15]
    text = R.paragraph(f"{len(rows)} hôtes suivis, {perfect} disponibles à 100 %.")
    return text + (R.table(["Hôte", "Disponibilité", "Sans nouvelles"], shown) if shown else "")


async def _alerts(conn, org_id: int, start: datetime, now: datetime) -> tuple[str, str]:
    by_prio = await fetch_all(conn, """
        SELECT m.priority, count(*) AS n FROM alert_events a JOIN monitors m ON m.id = a.monitor_id
         WHERE a.org_id = %s AND a.ts >= %s AND a.to_state = 'ALERT' GROUP BY 1 ORDER BY 1 NULLS LAST
    """, (org_id, start))
    noisy = await fetch_all(conn, """
        SELECT m.id, m.name, m.priority, count(*) AS n FROM alert_events a JOIN monitors m ON m.id = a.monitor_id
         WHERE a.org_id = %s AND a.ts >= %s GROUP BY 1, 2, 3 ORDER BY n DESC, m.id LIMIT 10
    """, (org_id, start))
    alerts = (R.table(["Priorité", "Alertes"], [[PRIORITY_LABELS.get(r["priority"], r["priority"]), r["n"]]
                                               for r in by_prio]) if by_prio else R.paragraph("Aucune alerte."))
    top = (R.table(["Moniteur", "Priorité", "Changements d'état"],
                   [[r["name"], PRIORITY_LABELS.get(r["priority"], r["priority"]), r["n"]] for r in noisy])
           if noisy else R.paragraph("Aucun changement d'état."))
    return alerts, top


async def _problem_time(conn, org_id: int, start: datetime, now: datetime) -> str:
    ids = [r["monitor_id"] for r in await fetch_all(conn, """
        SELECT DISTINCT a.monitor_id FROM alert_events a WHERE a.org_id = %s AND a.ts >= %s
        UNION
        SELECT ms.monitor_id FROM monitor_states ms JOIN monitors m ON m.id = ms.monitor_id
         WHERE m.org_id = %s AND ms.state IN ('WARN', 'ALERT')
    """, (org_id, start, org_id))]
    if not ids:
        return R.paragraph("Aucun problème sur la période.")
    no_data = {r["id"]: r["no_data_s"] for r in await fetch_all(
        conn, "SELECT id, no_data_s FROM monitors WHERE org_id = %s AND id = ANY(%s::bigint[])", (org_id, ids))}
    hist = await _monitor_history(conn, org_id, [i for i in ids if i in no_data], start, now)
    total = 0.0
    for mid, h in hist.items():
        for a, b in SC.alert_intervals(_changes(h["before"]), _changes(h["inside"]), h["current"], start.timestamp(),
                                       now.timestamp(), no_data[mid], frozenset({"WARN", "ALERT"})):
            total += b - a
    return R.paragraph(f"Durée cumulée des problèmes (avertissements et alertes, tous groupes) : {R.duration(total)}.")


async def _slos(conn, org_id: int) -> str:
    rows = await fetch_all(conn, f"SELECT org_id, {SLO_FIELDS} FROM slos WHERE org_id = %s ORDER BY name, id LIMIT 50",
                           (org_id,))
    if not rows:
        return R.paragraph("Aucun SLO.")
    out = []
    for s in rows:
        try:
            st = await slo_status(conn, s, with_history=False)
            out.append([s["name"], f"{real_value(s['target'])} %", f"{st['sli']:.3f} %",
                        f"{st['error_budget_remaining']:.1f} %", R.badge(st["state"])])
        except QueryError as e:
            out.append([s["name"], f"{real_value(s['target'])} %", "—", "—", R.badge(str(e)[:80])])
    return R.table(["SLO", "Cible", "SLI", "Budget restant", "État"], out, raw_cols=(4,))


async def _incidents(conn, org_id: int, start: datetime) -> str:
    rows = await fetch_all(conn, """
        SELECT number, title, severity, status, detected_at, resolved_at FROM incidents
         WHERE org_id = %s AND (detected_at >= %s OR resolved_at >= %s OR status <> 'resolved')
         ORDER BY detected_at DESC LIMIT 30
    """, (org_id, start, start))
    if not rows:
        return R.paragraph("Aucun incident.")
    return R.table(["Incident", "Titre", "Sévérité", "Statut", "Début", "Résolu"],
                   [[f"INC-{r['number']}", r["title"], f"SEV-{r['severity']}", r["status"], _fmt_dt(r["detected_at"]),
                     _fmt_dt(r["resolved_at"])] for r in rows])


async def _hosts_changes(conn, org_id: int, start: datetime) -> str:
    rows = await fetch_all(conn, """
        SELECT hostname, first_seen, last_seen FROM hosts
         WHERE org_id = %s AND (first_seen >= %s OR (last_seen >= %s AND last_seen < now() - interval '1 hour'))
         ORDER BY hostname LIMIT 100
    """, (org_id, start, start))
    if not rows:
        return R.paragraph("Aucun hôte nouveau ni disparu.")
    return R.table(["Hôte", "Changement", "Date"],
                   [[r["hostname"], "nouveau" if r["first_seen"] >= start else "disparu",
                     _fmt_dt(r["first_seen"] if r["first_seen"] >= start else r["last_seen"])] for r in rows])


async def _updates(conn, org_id: int) -> str:
    rows = await fetch_all(conn, """
        SELECT h.hostname, CASE WHEN (i.updates ->> 'security') ~ '^[0-9]+$' THEN (i.updates ->> 'security')::int END AS security,
               CASE WHEN (i.updates ->> 'pending') ~ '^[0-9]+$' THEN (i.updates ->> 'pending')::int END AS pending
          FROM host_inventory i JOIN hosts h ON h.id = i.host_id AND h.org_id = i.org_id
         WHERE i.org_id = %s
           AND CASE WHEN (i.updates ->> 'security') ~ '^[0-9]+$' THEN (i.updates ->> 'security')::int ELSE 0 END > 0
         ORDER BY 2 DESC, 1 LIMIT 30
    """, (org_id,))
    if not rows:
        return R.paragraph("Aucune mise à jour de sécurité en attente (selon l'inventaire).")
    return R.table(["Hôte", "Sécurité", "Total en attente"], [[r["hostname"], r["security"], r["pending"]] for r in rows])


async def _certificates(conn, org_id: int) -> str:
    items = []
    for r in await fetch_all(conn, """
        SELECT DISTINCT ON (r.test_id) t.name, r.details ->> 'ssl_days_left' AS days
          FROM synthetic_results r JOIN synthetic_tests t ON t.id = r.test_id AND t.org_id = r.org_id
         WHERE r.org_id = %s AND r.ts > now() - interval '1 day' AND r.details ? 'ssl_days_left'
         ORDER BY r.test_id, r.ts DESC
    """, (org_id,)):
        try:
            items.append((float(r["days"]), r["name"], "test synthétique"))
        except (TypeError, ValueError):
            continue
    for r in await fetch_all(conn, """
        SELECT DISTINCT ON (s.id) s.tags, p.value FROM metric_series s JOIN metric_points p ON p.series_id = s.id
         WHERE s.org_id = %s AND s.name = 'tls.cert.days_left' AND p.ts > now() - interval '1 day'
         ORDER BY s.id, p.ts DESC
    """, (org_id,)):
        tags = r["tags"] or {}
        items.append((r["value"], tags.get("subject") or tags.get("cert_path") or "?", f"agent {tags.get('host', '?')}"))
    soon = sorted(i for i in items if i[0] is not None and i[0] < 30)
    if not soon:
        return R.paragraph("Aucun certificat à renouveler sous 30 jours.")
    return R.table(["Certificat", "Jours restants", "Relevé par"], [[name, f"{days:.0f}", src] for days, name, src in soon[:30]])


async def summary_sections(conn, org_id: int, start: datetime, now: datetime) -> list[str]:
    alerts, top = await _alerts(conn, org_id, start, now)
    return [
        R.section("Disponibilité des hôtes", await _hosts_availability(conn, org_id, start, now)),
        R.section("Alertes par priorité", alerts),
        R.section("Les 10 moniteurs les plus bruyants", top),
        R.section("Problèmes", await _problem_time(conn, org_id, start, now)),
        R.section("SLO", await _slos(conn, org_id)),
        R.section("Incidents", await _incidents(conn, org_id, start)),
        R.section("Hôtes nouveaux ou disparus", await _hosts_changes(conn, org_id, start)),
        R.section("Mises à jour de sécurité en attente", await _updates(conn, org_id)),
        R.section("Certificats à renouveler sous 30 jours", await _certificates(conn, org_id)),
    ]


def _template_values(tvars: list) -> dict:
    """Valeur par défaut de chaque variable du tableau ; plusieurs valeurs (§ 30) : une seule retenue,
    sinon tout le parc (le rapport ne sait pas écrire un filtre à plusieurs valeurs)."""
    out = {}
    for v in tvars or []:
        default = v.get("default", "*")
        if isinstance(default, list):
            default = default[0] if len(default) == 1 else "*"
        out[v.get("name")] = "*" if default in ("*", "", None) else f"{v.get('tag')}:{default}"
    return out


async def dashboard_sections(conn, org_id: int, dashboard_id: int | None, start: datetime, now: datetime) -> list[str]:
    d = await fetch_one(conn, "SELECT title, widgets, template_vars FROM dashboards WHERE org_id = %s AND id = %s",
                        (org_id, dashboard_id)) if dashboard_id else None
    if d is None:
        return [R.paragraph("Tableau de bord introuvable.")]
    values = _template_values(d["template_vars"])
    a, b = dt_to_ms(start), dt_to_ms(now)
    source = mql.choose_source(a, b, now_ms())
    out = [R.paragraph(f"Tableau « {d['title']} »")]
    for w in d["widgets"] or []:
        if not isinstance(w, dict) or w.get("type") not in WIDGETS:
            continue
        rows = []
        for q in (w.get("queries") or [])[:5]:
            text = mql.apply_template_vars(str(q.get("q") or ""), values)
            try:
                mq = mql.parse(text)
                step = mql.choose_interval(a, b, 60, source, mq.rollup_s)
                series = await metrics_exec.execute(conn, org_id, mq, a, b, source, step)
            except QueryError as e:
                rows.append([text, "—", "—", "—", "—", R.esc(f"requête en erreur : {e}")])
                continue
            stats = []
            for s in series:
                vals = [v for _, v in s["points"] if v is not None]
                if not vals:
                    continue
                label = ", ".join(f"{k}:{v}" for k, v in s["group"].items()) or mq.metric
                stats.append((sum(vals) / len(vals), label, min(vals), max(vals), vals[-1], s["points"]))
            stats.sort(key=lambda x: -x[0])
            for avg, label, lo, hi, last, pts in stats[:10]:
                rows.append([label, R.fmt(lo), R.fmt(avg), R.fmt(hi), R.fmt(last), R.sparkline([v for _, v in pts])])
        body = (R.table(["Série", "Min", "Moy", "Max", "Dernière", "Courbe"], rows, raw_cols=(5,))
                if rows else R.paragraph("Aucune donnée sur la période."))
        out.append(R.section(str(w.get("title") or w.get("id") or "Widget"), body))
    return out


async def build(conn, settings: Settings, report: dict, org_id: int, now: datetime | None = None) -> tuple[str, str, str]:
    """(sujet, HTML, texte) d'un rapport sur sa période."""
    now = now or datetime.now(timezone.utc)
    start = now - timedelta(seconds=report["period_s"])
    org = await fetch_one(conn, "SELECT name FROM orgs WHERE id = %s", (org_id,))
    subtitle = f"{(org or {}).get('name', '')} · du {_fmt_dt(start)} au {_fmt_dt(now)}"
    if report["kind"] == "summary":
        sections = await summary_sections(conn, org_id, start, now)
    elif report["kind"] == "slo":
        sections = [R.section("SLO", await _slos(conn, org_id))]
    else:
        sections = await dashboard_sections(conn, org_id, report["dashboard_id"], start, now)
    subject = f"[HYPHA] {report['name']} — {start.strftime('%d/%m')} au {now.strftime('%d/%m/%Y')}"
    html = R.page(report["name"], subtitle, sections, settings.public_url)
    text = (f"{report['name']}\n{subtitle}\n\nLa version HTML de ce courriel porte le rapport complet.\n"
            f"{settings.public_url}\n")
    return subject, html, text


async def email_channels(conn, org_id: int, ids: list[int]) -> list[dict]:
    if not ids:
        return []
    return await fetch_all(conn, """
        SELECT id, kind, name, config FROM notification_channels
         WHERE org_id = %s AND id = ANY(%s::bigint[]) AND enabled AND kind = 'email'
    """, (org_id, ids))


def notices(settings: Settings, report: dict, channels: list[dict], subject: str, html: str, text: str) -> list[Notice]:
    mon = {"id": report["id"], "name": report["name"], "type": "report"}
    return [Notice(c, mon, "*", "OK", None, None, text, settings.public_url, kind="report", subject_override=subject,
                   html_body=html) for c in channels]


async def tick(db, settings: Settings) -> int:
    """Envoie les rapports échus ; rend leur nombre."""
    now = datetime.now(timezone.utc)
    async with db.writer() as conn:
        rows = await fetch_all(conn, f"SELECT org_id, {FIELDS} FROM report_schedules WHERE enabled ORDER BY id")
        due = []
        for r in rows:
            try:
                if SCH.is_due(r["schedule"] or {}, now, r["last_sent_at"], r["created_at"]):
                    due.append(r)
            except (ValueError, KeyError, TypeError) as e:
                log.warning("rapport %s : échéance illisible (%s)", r["id"], e)
        for r in due:
            await conn.execute("UPDATE report_schedules SET last_sent_at = %s WHERE id = %s", (now, r["id"]))
    for r in due:
        try:
            async with db.writer() as conn:
                subject, html, text = await build(conn, settings, r, r["org_id"], now)
                channels = await email_channels(conn, r["org_id"], r["channel_ids"] or [])
            results = await deliver_all(settings, notices(settings, r, channels, subject, html, text))
            if not channels:
                log.warning("rapport %s (%s) : aucun canal courriel actif", r["id"], r["name"])
            elif not all(ok for ok, _ in results):
                log.error("rapport %s (%s) : envoi incomplet %s", r["id"], r["name"], results)
        except Exception:
            log.exception("rapport %s (%s) : échec de la préparation", r["id"], r["name"])
    return len(due)
