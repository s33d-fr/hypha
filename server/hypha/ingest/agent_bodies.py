"""Corps de l'agent ajoutés en 0.4.0, validés sans accès à la base : inventaire (§ 15), requêtes des
bases (§ 30), résultats des synthétiques privés (§ 29), résultat d'une action (§ 28)."""
import re

from ..util import clean_json, jdump, ms_to_dt, now_ms
from .body import BodyError, Window, as_float, as_int, as_ts, opt_text

MAX_PACKAGES = 50_000
MAX_UPDATES = 2_000
MAX_DBM_QUERIES = 500
MAX_QUERY_TEXT = 4096
MAX_SYNTHETIC_RESULTS = 1000
MAX_OUTPUT = 64 * 1024
ACTION_RESULT_STATUSES = ("running", "succeeded", "failed", "refused_by_agent")
INT64 = 2 ** 63 - 1


def body_ts(body: dict, window: Window) -> int:
    ts = as_ts(body.get("ts"))
    if ts is None or not window.ok(ts):
        return now_ms()
    return ts


def _bool(v) -> bool | None:
    if isinstance(v, bool):
        return v
    if isinstance(v, (int, float)) and v in (0, 1):
        return bool(v)
    if isinstance(v, str) and v.strip().lower() in ("true", "false", "yes", "no", "1", "0"):
        return v.strip().lower() in ("true", "yes", "1")
    return None


# ── § 15 inventaire ────────────────────────────────────────────────────────────

def parse_inventory(body: dict) -> dict:
    """Rend {ts, hardware, os, updates (JSON), packages:[(nom, version, arch, source)], dropped}."""
    hardware = body.get("hardware") if isinstance(body.get("hardware"), dict) else {}
    os_info = dict(body.get("os")) if isinstance(body.get("os"), dict) else {}
    if "reboot_required" in os_info:
        os_info["reboot_required"] = _bool(os_info["reboot_required"])
    raw_updates = body.get("updates") if isinstance(body.get("updates"), dict) else {}
    updates: dict = {}
    for k in ("pending", "security"):
        if k in raw_updates:
            updates[k] = as_int(raw_updates.get(k), 0, 10_000_000)
    if isinstance(raw_updates.get("list"), list):
        items = []
        for u in raw_updates["list"][:MAX_UPDATES]:
            if not isinstance(u, dict) or not opt_text(u.get("name"), 300):
                continue
            items.append({"name": opt_text(u.get("name"), 300), "current": opt_text(u.get("current"), 200),
                          "candidate": opt_text(u.get("candidate"), 200), "security": bool(_bool(u.get("security")))})
        updates["list"] = items
    packages: dict[tuple, tuple] = {}
    dropped = 0
    raw_packages = body.get("packages")
    if raw_packages is not None and not isinstance(raw_packages, list):
        raise BodyError(400, "invalid_body", "`packages` doit être une liste")
    for p in (raw_packages or [])[:MAX_PACKAGES]:
        name = opt_text(p.get("name"), 300) if isinstance(p, dict) else None
        if not name:
            dropped += 1
            continue
        arch = opt_text(p.get("arch"), 50) or ""
        packages[(name, arch)] = (name, opt_text(p.get("version"), 200) or "", arch,
                                  opt_text(p.get("source"), 50) or "")
    dropped += max(0, len(raw_packages or []) - MAX_PACKAGES)
    return {"ts": ms_to_dt(body_ts(body, Window.around())), "hardware": jdump(clean_json(hardware)),
            "os": jdump(os_info), "updates": jdump(updates), "packages": sorted(packages.values()),
            "dropped": dropped}


# ── § 30 requêtes des bases (DBM) ──────────────────────────────────────────────

DBM_COLUMNS = ("org_id", "host_id", "ts", "engine", "instance", "db", "query_id", "query", "calls", "total_ms",
               "mean_ms", "rows", "shared_blks_hit", "shared_blks_read")


def parse_dbm(body: dict, org_id: int, host_id: int, window: Window) -> tuple[list[tuple], int]:
    items = body.get("queries")
    if not isinstance(items, list):
        raise BodyError(400, "invalid_body", "liste `queries` absente")
    ts = ms_to_dt(body_ts(body, window))
    rows, dropped = [], 0
    for q in items[:MAX_DBM_QUERIES]:
        if not isinstance(q, dict):
            dropped += 1
            continue
        query_id = opt_text(q.get("query_id"), 100)
        text = opt_text(q.get("query"), MAX_QUERY_TEXT)
        calls = as_int(q.get("calls"), 0, INT64)
        total = as_float(q.get("total_ms"))
        if not query_id or not text or calls is None or total is None or total < 0:
            dropped += 1
            continue
        rows.append((org_id, host_id, ts, opt_text(q.get("engine"), 30) or "postgres",
                     opt_text(q.get("instance"), 200) or "", opt_text(q.get("db"), 200) or "", query_id, text,
                     calls, total, total / calls if calls else None, as_int(q.get("rows"), 0, INT64) or 0,
                     as_int(q.get("shared_blks_hit"), 0, INT64) or 0, as_int(q.get("shared_blks_read"), 0, INT64) or 0))
    dropped += max(0, len(items) - MAX_DBM_QUERIES)
    return rows, dropped


# ── § 29 résultats des synthétiques exécutés par un agent ──────────────────────

SYNTHETIC_COLUMNS = ("org_id", "test_id", "ts", "location", "ok", "status_code", "latency_ms", "error", "details")


def parse_synthetic_results(body: dict, window: Window) -> tuple[list[dict], int]:
    items = body.get("results")
    if not isinstance(items, list):
        raise BodyError(400, "invalid_body", "liste `results` absente")
    out, dropped = [], 0
    for r in items[:MAX_SYNTHETIC_RESULTS]:
        test_id = as_int(r.get("test_id"), 1) if isinstance(r, dict) else None
        ok = r.get("ok") if isinstance(r, dict) else None
        ts = as_ts(r.get("ts")) if isinstance(r, dict) else None
        ts = now_ms() if ts is None and isinstance(r, dict) else ts
        if test_id is None or not isinstance(ok, bool) or ts is None or not window.ok(ts):
            dropped += 1
            continue
        latency = as_float(r.get("latency_ms"))
        details = r.get("details") if isinstance(r.get("details"), dict) else {}
        out.append({"test_id": test_id, "ts": ms_to_dt(ts), "ok": ok,
                    "status_code": as_int(r.get("status_code"), 0, 999),
                    "latency_ms": latency if latency is None or latency >= 0 else None,
                    "error": opt_text(r.get("error"), 2000), "details": jdump(details)})
    dropped += max(0, len(items) - MAX_SYNTHETIC_RESULTS)
    return out, dropped


# ── § 28 résultat d'une action ─────────────────────────────────────────────────

def parse_action_result(body: dict) -> dict:
    status = body.get("status")
    if status not in ACTION_RESULT_STATUSES:
        raise BodyError(400, "invalid_body", f"status attendu : {'|'.join(ACTION_RESULT_STATUSES)}")
    output = body.get("output")
    output = output if isinstance(output, str) else ("" if output is None else str(output))
    if len(output.encode("utf-8", "replace")) > MAX_OUTPUT:
        output = output.encode("utf-8", "replace")[:MAX_OUTPUT].decode("utf-8", "ignore")
    return {"status": status, "exit_code": as_int(body.get("exit_code"), -2 ** 31, 2 ** 31 - 1),
            "output": opt_text(output, MAX_OUTPUT) or ""}


_UUID_RE = re.compile(r"^[0-9a-fA-F]{8}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{4}-?[0-9a-fA-F]{12}$")


def header_agent_id(value: str | None) -> str:
    """`X-Hypha-Agent-Id` normalisé ; 400 s'il manque ou n'est pas un UUID."""
    import uuid
    if not value or not _UUID_RE.match(value.strip()):
        raise BodyError(400, "invalid_agent", "en-tête X-Hypha-Agent-Id absent ou invalide (UUID attendu)")
    return str(uuid.UUID(value.strip()))
