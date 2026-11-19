"""Mise en forme du contexte envoyé au modèle, sans accès à la base.

Le contexte est borné (≈ 12 000 caractères) : les sections passent dans l'ordre d'importance
(sujet, hôte, contrôles, journaux, métriques, processus, événements) et la première qui déborde
est tronquée, les suivantes sont laissées de côté. Les journaux répétés sont regroupés avec leur
nombre, pour que 300 fois la même erreur ne prenne qu'une ligne.

Les lignes de données sont écrites en clé=valeur neutre ; seuls les titres de section suivent la
langue demandée, pour ne pas mêler deux langues dans ce que lit le modèle.
"""
import re
from dataclasses import dataclass, field
from datetime import datetime, timezone

from ..util import parse_group_key

MAX_CHARS = 12_000
MAX_LOG_GROUPS = 40
MAX_LOG_MESSAGE = 300
MAX_SPANS = 20
MAX_PROCESSES = 8

SEVERITY = {s: i for i, s in enumerate(
    ("emergency", "alert", "critical", "error", "warning", "notice", "info", "debug"))}

LABELS = {
    "fr": {"subject": "Sujet", "host": "Hôte", "monitor": "Moniteur", "checks": "Contrôles non OK",
           "logs": "Journaux de l'hôte", "metrics": "Métriques (30 min)", "processes": "Processus les plus gourmands",
           "events": "Événements (24 h)", "trace": "Spans de la trace (erreurs et chemin critique)",
           "trace_logs": "Journaux liés à la trace", "truncated": "… (tronqué)",
           "stack": "Pile d'appels", "occurrences": "Dernières occurrences"},
    "en": {"subject": "Subject", "host": "Host", "monitor": "Monitor", "checks": "Non-OK checks",
           "logs": "Host logs", "metrics": "Metrics (30 min)", "processes": "Top processes",
           "events": "Events (24 h)", "trace": "Trace spans (errors and critical path)",
           "trace_logs": "Logs linked to the trace", "truncated": "… (truncated)",
           "stack": "Stack trace", "occurrences": "Latest occurrences"},
}

_MASK_RE = re.compile(r"0x[0-9a-fA-F]+|\b[0-9a-fA-F]{8,}\b|\d+")
_SPACES_RE = re.compile(r"\s+")


def labels(lang: str) -> dict:
    return LABELS.get(lang, LABELS["en"])


def host_from_group(group: str | None) -> str | None:
    """Nom d'hôte porté par une clé de groupe de moniteur (`host:app-01,unit:x` → `app-01`)."""
    host = parse_group_key(group or "").get("host")
    return host if host and host != "N/A" else None


def fmt_ts(value) -> str:
    if value is None:
        return "?"
    if isinstance(value, (int, float)):
        value = datetime.fromtimestamp(value / 1000, tz=timezone.utc)
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).strftime("%Y-%m-%d %H:%M:%SZ")


def fmt_num(value, unit: str | None = None) -> str:
    if value is None:
        return "?"
    if unit in ("byte", "byte_per_second"):
        v, suffix = float(value), "B"
        for suffix in ("B", "KiB", "MiB", "GiB", "TiB"):
            if abs(v) < 1024 or suffix == "TiB":
                break
            v /= 1024
        return f"{v:.1f} {suffix}" + ("/s" if unit == "byte_per_second" else "")
    text = f"{float(value):.3g}" if isinstance(value, float) else str(value)
    return text + ("%" if unit == "percent" else "")


def clip(text, n: int) -> str:
    text = _SPACES_RE.sub(" ", str(text or "")).strip()
    return text if len(text) <= n else text[: n - 1] + "…"


def _kv(tags: dict | None, skip: tuple = ()) -> str:
    return ", ".join(f"{k}={v}" for k, v in sorted((tags or {}).items()) if k not in skip)


# ── journaux ───────────────────────────────────────────────────────────────────

def log_signature(message: str) -> str:
    """Forme du message sans ses nombres ni identifiants : deux lignes qui ne diffèrent que par un
    PID, une durée ou un horodatage se regroupent."""
    return _MASK_RE.sub("#", _SPACES_RE.sub(" ", message or "").strip())[:500]


@dataclass
class LogGroup:
    status: str
    service: str | None
    message: str               # message le plus récent du groupe, tronqué
    count: int = 1
    first_ts: object = None
    last_ts: object = None


def _stamp(ts) -> float:
    return ts.timestamp() if hasattr(ts, "timestamp") else float(ts or 0)


def group_logs(rows: list[dict], limit: int = MAX_LOG_GROUPS) -> list[LogGroup]:
    """Regroupe les lignes répétées ; erreurs et avertissements d'abord, puis les plus fréquentes."""
    groups: dict[tuple, LogGroup] = {}
    for r in rows:
        status = r.get("status") or "info"
        key = (status, r.get("service"), log_signature(r.get("message") or ""))
        ts = r.get("ts")
        g = groups.get(key)
        if g is None:
            groups[key] = LogGroup(status, r.get("service"), clip(r.get("message"), MAX_LOG_MESSAGE), 1, ts, ts)
            continue
        g.count += 1
        if ts is not None and (g.first_ts is None or _stamp(ts) < _stamp(g.first_ts)):
            g.first_ts = ts
        if ts is not None and (g.last_ts is None or _stamp(ts) > _stamp(g.last_ts)):
            g.last_ts = ts
            g.message = clip(r.get("message"), MAX_LOG_MESSAGE)
    ordered = sorted(groups.values(), key=lambda g: (SEVERITY.get(g.status, 6), -g.count, -_stamp(g.last_ts)))
    return ordered[:limit]


def format_logs(groups: list[LogGroup]) -> str:
    lines = []
    for g in groups:
        when = fmt_ts(g.last_ts) if g.count == 1 else f"{g.count}x {fmt_ts(g.first_ts)} -> {fmt_ts(g.last_ts)}"
        service = f" {g.service}" if g.service else ""
        lines.append(f"- [{g.status}]{service} {when}: {g.message}")
    return "\n".join(lines)


# ── autres sections ────────────────────────────────────────────────────────────

def format_host(h: dict) -> str:
    system = " ".join(str(x) for x in (h.get("os"), h.get("platform"), h.get("platform_version")) if x)
    fields = [
        ("hostname", h.get("hostname")),
        ("system", system or None),
        ("kernel", h.get("kernel")),
        ("arch", h.get("arch")),
        ("cpu_cores", h.get("cpu_cores")),
        ("mem_total", fmt_num(h["mem_total"], "byte") if h.get("mem_total") else None),
        ("virtualization", (h.get("meta") or {}).get("virtualization")),
        ("tags", _kv(h.get("tags")) or None),
        ("last_seen", fmt_ts(h.get("last_seen"))),
        ("boot_time", fmt_ts(h["boot_time"]) if h.get("boot_time") else None),
    ]
    return "\n".join(f"- {k}: {v}" for k, v in fields if v not in (None, ""))


def format_monitor(m: dict, group: str, state: dict | None, history: list[dict]) -> str:
    lines = [f"- name: {m['name']}", f"- type: {m['type']}", f"- group: {group}",
             f"- query: {clip(m.get('query'), 400)}", f"- thresholds: {m.get('thresholds') or {}}"]
    if state:
        lines.append(f"- state: {state['state']} since {fmt_ts(state.get('since'))}, "
                     f"value {fmt_num(state.get('value'))}")
    for h in history[:10]:
        lines.append(f"  - {fmt_ts(h['ts'])}: {h.get('from_state') or '?'} -> {h['to_state']} "
                     f"(value {fmt_num(h.get('value'))})")
    return "\n".join(lines)


def format_checks(checks: list[dict]) -> str:
    names = {1: "WARN", 2: "CRIT", 3: "UNKNOWN"}
    lines = []
    for c in checks:
        tags = _kv(c.get("tags"), skip=("host",))
        lines.append(f"- {c['name']} {names.get(c['status'], c['status'])}"
                     + (f" ({tags})" if tags else "")
                     + (f": {clip(c.get('message'), 200)}" if c.get("message") else "")
                     + f" [{fmt_ts(c.get('ts'))}]")
    return "\n".join(lines)


def format_events(events: list[dict]) -> str:
    return "\n".join(f"- {fmt_ts(e['ts'])} [{e.get('alert_type')}] {e.get('source') or '?'}/"
                     f"{e.get('event_type') or '?'}: {clip(e.get('title'), 200)}" for e in events)


METRIC_DETAIL = {"system.disk.in_use": "mount", "system.net.bytes_rcvd": "interface",
                 "system.net.bytes_sent": "interface", "system.io.read_bytes": "device",
                 "system.io.write_bytes": "device"}


def format_metrics(rows: list[dict], limit: int = 30) -> str:
    """Une ligne par série : nom, point de montage / interface / disque, moyenne et maximum."""
    rows = sorted(rows, key=lambda r: (r["name"], -(r.get("max") or 0)))
    lines = []
    for r in rows[:limit]:
        key = METRIC_DETAIL.get(r["name"])
        value = (r.get("tags") or {}).get(key) if key else None
        detail = f" [{key}={value}]" if value else ""
        lines.append(f"- {r['name']}{detail}: avg {fmt_num(r.get('avg'), r.get('unit'))}, "
                     f"max {fmt_num(r.get('max'), r.get('unit'))}")
    return "\n".join(lines)


def pick_processes(by_cpu: list[dict], by_mem: list[dict], n: int = MAX_PROCESSES) -> list[dict]:
    """Alterne les plus gros consommateurs de CPU et de mémoire, sans doublon."""
    out, seen = [], set()
    for i in range(max(len(by_cpu), len(by_mem))):
        for source in (by_cpu, by_mem):
            if i < len(source) and source[i]["pid"] not in seen and len(out) < n:
                seen.add(source[i]["pid"])
                out.append(source[i])
    return out


def format_processes(procs: list[dict]) -> str:
    return "\n".join(f"- {p['name']} pid={p['pid']} user={p.get('user') or '?'} cpu={fmt_num(p.get('cpu_pct'))}% "
                     f"rss={fmt_num(p.get('mem_rss'), 'byte')} cmd: {clip(p.get('cmdline'), 120)}" for p in procs)


# ── trace ──────────────────────────────────────────────────────────────────────

def _end_ms(s: dict) -> float:
    return _stamp(s["ts"]) * 1000 + (s.get("duration_ms") or 0)


def critical_path(spans: list[dict]) -> list[dict]:
    """Depuis la racine, descend à chaque niveau vers l'enfant qui finit le plus tard."""
    if not spans:
        return []
    ids = {s["span_id"] for s in spans}
    children: dict = {}
    for s in spans:
        children.setdefault(s.get("parent_span_id"), []).append(s)
    roots = [s for s in spans if not s.get("parent_span_id") or s["parent_span_id"] not in ids]
    node = max(roots or spans, key=lambda s: s.get("duration_ms") or 0)
    path, seen = [node], {node["span_id"]}
    while children.get(node["span_id"]):
        node = max(children[node["span_id"]], key=_end_ms)
        if node["span_id"] in seen:
            break
        seen.add(node["span_id"])
        path.append(node)
    return path


def trace_spans(spans: list[dict], limit: int = MAX_SPANS) -> list[dict]:
    """Spans en erreur d'abord, puis le chemin critique, 20 au plus, rendus dans l'ordre du temps."""
    chosen: dict = {}
    for s in [s for s in spans if s.get("status") == "error"] + critical_path(spans):
        if len(chosen) >= limit:
            break
        chosen.setdefault(s["span_id"], s)
    return sorted(chosen.values(), key=lambda s: _stamp(s["ts"]))


def format_spans(spans: list[dict]) -> str:
    lines = []
    for s in spans:
        status = " ERROR" if s.get("status") == "error" else ""
        msg = f": {clip(s.get('status_message'), 200)}" if s.get("status_message") else ""
        lines.append(f"- {fmt_ts(s['ts'])} service={s.get('service')} kind={s.get('kind') or '?'} "
                     f"name={clip(s.get('name'), 120)} resource={clip(s.get('resource'), 150)} "
                     f"duration={fmt_num(s.get('duration_ms'))}ms{status}{msg}")
    return "\n".join(lines)


# ── problème du suivi des erreurs (§ 25) ───────────────────────────────────────

def clip_lines(text, n: int) -> str:
    """Texte multi-lignes (pile) borné à `n` caractères, sauts de ligne gardés."""
    text = str(text or "").strip()
    return text if len(text) <= n else text[: n - 1] + "…"


def format_issue(issue: dict) -> str:
    fields = [("origin", issue.get("origin")), ("service", issue.get("service")),
              ("type", issue.get("error_type")), ("message", clip(issue.get("message"), 600)),
              ("status", issue.get("status")), ("count", issue.get("count")),
              ("first_seen", fmt_ts(issue.get("first_seen"))), ("last_seen", fmt_ts(issue.get("last_seen"))),
              ("regressed_at", fmt_ts(issue["regressed_at"]) if issue.get("regressed_at") else None),
              ("last_host", issue.get("last_host"))]
    return "\n".join(f"- {k}: {v}" for k, v in fields if v not in (None, ""))


def format_occurrences(rows: list[dict]) -> str:
    lines = []
    for o in rows:
        where = o.get("hostname") or o.get("view_url") or "?"
        detail = o.get("message") or o.get("name") or ""
        ref = f" trace={o['trace_id']}" if o.get("trace_id") else ""
        lines.append(f"- {fmt_ts(o.get('ts'))} {o.get('kind')} {where}{ref}: {clip(detail, 300)}")
    return "\n".join(lines)


# ── assemblage ─────────────────────────────────────────────────────────────────

@dataclass
class Context:
    lang: str
    sections: list[tuple[str, str]] = field(default_factory=list)   # (clé de LABELS, texte)
    summary: dict = field(default_factory=dict)

    def add(self, key: str, text: str | None) -> None:
        if text and text.strip():
            self.sections.append((key, text.strip()))


def assemble(ctx: Context, max_chars: int = MAX_CHARS) -> str:
    """Texte final : sections dans l'ordre, la première qui déborde est coupée, la suite écartée."""
    lab = labels(ctx.lang)
    out, used = [], 0
    for key, text in ctx.sections:
        block = f"### {lab.get(key, key)}\n{text}\n"
        if used + len(block) > max_chars:
            room = max_chars - used - len(lab["truncated"]) - 1
            if room > 200:
                out.append(block[:room].rsplit("\n", 1)[0] + "\n" + lab["truncated"] + "\n")
            break
        out.append(block)
        used += len(block) + 1
    return "\n".join(out)
