"""Découpage des journaux d'accès à l'arrivée (contrat § 12), après les filtres du § 11.

Formats reconnus : uvicorn (accès), nginx / Apache « combined » (avec ou sans temps de requête
final), HAProxy « httplog » (format par défaut d'HAProxy 3.x). Chaque format a un test de
préfixe bon marché avant son expression précompilée ; une ligne qui ne correspond à rien passe
telle quelle. Les attributs produits portent les noms Datadog et ne remplacent JAMAIS un attribut
déjà présent (imbriqué ou à plat). Une ligne qui n'est pas un accès HTTP passe ensuite par l'analyse
sécurité du § 23 (sshd, sudo, su, fail2ban).

Statut déduit du code HTTP : une ligne `info` dont `http.status_code` vaut ≥ 500 devient `error`,
≥ 400 devient `warning` ; un statut plus grave déjà posé n'est jamais abaissé. La règle vaut aussi
pour un code HTTP déjà présent dans les attributs (journal JSON décodé par l'agent, OTLP).

Modèles d'analyse par équipement (0.5.0, point d'accroche de la vague 4) : une ligne reçue par la
réception syslog de l'agent (`source:syslog`) dont l'attribut `syslog.parser` nomme un modèle de
`DEVICE_PARSERS` passe par ce modèle avant le reste. Le modèle rend des attributs (même règle : rien
d'existant n'est écrasé) et, s'il le faut, un statut (`status`), qui ne peut que rendre la ligne plus
grave. Livré : `kv` (paires clé=valeur, valeurs entre guillemets admises).
"""
import json
import re
from collections.abc import Callable
from urllib.parse import urlsplit

from .body import LOG_COLUMNS, LOG_STATUSES

_I_STATUS = LOG_COLUMNS.index("status")
_I_MESSAGE = LOG_COLUMNS.index("message")
_I_ATTRS = LOG_COLUMNS.index("attributes")
_I_SOURCE = LOG_COLUMNS.index("source")

_UVICORN_PREFIXES = ("INFO:", "WARNING:", "ERROR:", "DEBUG:", "CRITICAL:", "TRACE:")

# INFO:     192.0.2.233:0 - "GET /entreprise/exemple-123456789 HTTP/1.1" 200 OK
_UVICORN = re.compile(
    r'^[A-Z]+:\s+(?P<client>\S+) - "(?P<method>[A-Z]+) (?P<path>[^ "]+) HTTP/[0-9.]+" (?P<status>\d{3})(?:\s|$)')

# 203.0.113.7 - - [18/Nov/2026:14:03:12 +0100] "GET /x?a=1 HTTP/1.1" 200 5123 "https://r/" "Mozilla/5.0" 0.012
_COMBINED = re.compile(
    r'^(?P<ip>\S+) \S+ \S+ \[[^\]]+\] "(?P<method>[A-Z]+) (?P<path>[^ "]+)(?: [^"]*)?" (?P<status>\d{3}) '
    r'(?P<bytes>\d+|-)(?: "(?:[^"\\]|\\.)*" "(?P<ua>(?:[^"\\]|\\.)*)")?(?: (?P<rt>\d+(?:\.\d+)?))?\s*$')

# %ci:%cp [%tr] %ft %b/%s %TR/%Tw/%Tc/%Tr/%Ta %ST %B %CC %CS %tsc %ac/%fc/%bc/%sc/%rc %sq/%bq %hr %hs %{+Q}r
# 203.0.113.102:33317 [06/Feb/2009:12:14:14.655] http-in static/srv1 10/0/30/69/109 200 2750 - - ---- 1/1/1/1/0 0/0 {} {} "GET /index.html HTTP/1.1"
# Recherche non ancrée au début : une ligne relayée par syslog porte « haproxy[1234]: » devant.
_HAPROXY = re.compile(
    r'(?P<ip>[0-9A-Fa-f.:]+):(?P<port>\d+) \[[^\]]+\] (?P<fe>\S+) (?P<be>[^/\s]+)/(?P<srv>\S+) '
    r'-?\d+/-?\d+/-?\d+/-?\d+/\+?(?P<ta>-?\d+) (?P<status>-?\d+) \+?(?P<bytes>\d+) '
    r'.*"(?P<method>[A-Z]+) (?P<path>[^ "]+)(?: [^"]*)?"\s*$')


def url_path(target: str) -> str:
    """Chemin sans paramètres ni fragment ; une cible absolue (proxy) garde seulement son chemin."""
    if "://" in target[:12]:
        return urlsplit(target).path or "/"
    return target.split("?", 1)[0].split("#", 1)[0]


def _status_code(text: str) -> int | None:
    code = int(text)
    return code if 100 <= code <= 599 else None


def parse_access(message: str) -> dict | None:
    """Attributs (noms à points) d'une ligne d'accès reconnue, ou None."""
    if not message or len(message) > 8192:
        return None
    if message.startswith(_UVICORN_PREFIXES):
        if ' - "' not in message:
            return None
        m = _UVICORN.match(message)
        if not m:
            return None
        client = m["client"]
        ip, sep, port = client.rpartition(":")
        out = {"http.method": m["method"], "http.url_path": url_path(m["path"]),
               "http.status_code": _status_code(m["status"]),
               "network.client.ip": ip if sep and port.isdigit() else client}
        return {k: v for k, v in out.items() if v is not None}
    if '] "' in message:
        m = _COMBINED.match(message)
        if not m:
            return None
        out = {"http.method": m["method"], "http.url_path": url_path(m["path"]),
               "http.status_code": _status_code(m["status"]), "network.client.ip": m["ip"]}
        if m["bytes"] != "-":
            out["network.bytes_written"] = int(m["bytes"])
        if m["ua"] is not None and m["ua"] not in ("", "-"):
            out["http.useragent"] = m["ua"].replace('\\"', '"')
        if m["rt"] is not None:
            out["duration"] = round(float(m["rt"]) * 1000, 3)      # $request_time est en secondes
        return {k: v for k, v in out.items() if v is not None}
    if "] " in message and message.rstrip().endswith('"'):
        m = _HAPROXY.search(message)
        if not m:
            return None
        out = {"http.method": m["method"], "http.url_path": url_path(m["path"]),
               "http.status_code": _status_code(m["status"]), "network.client.ip": m["ip"],
               "network.bytes_written": int(m["bytes"]), "haproxy.frontend": m["fe"],
               "haproxy.backend": m["be"], "haproxy.server": m["srv"]}
        ta = int(m["ta"])
        if ta >= 0:
            out["duration"] = ta                                    # %Ta : temps actif total, en ms
        return {k: v for k, v in out.items() if v is not None}
    return None


# ── analyse sécurité (contrat § 23) : sshd, sudo, su, fail2ban ─────────────────
# Lignes de journald (message nu) ou de fichier syslog (« sshd[123]: … » devant) : recherche non
# ancrée, précédée d'un test de sous-chaîne. Attributs : evt.name (authentication, sudo, ban,
# unban), evt.outcome (success, failure), usr.name, network.client.ip, sudo.command, fail2ban.jail.

_IP = r"[0-9A-Fa-f.:]+"
_SSH_FAILED = re.compile(rf"Failed (?:password|publickey|none|keyboard-interactive/pam|hostbased) for "
                         rf"(?:invalid user )?(?P<user>\S*) from (?P<ip>{_IP}) port \d+")
_SSH_ACCEPTED = re.compile(rf"Accepted (?:password|publickey|keyboard-interactive/pam|gssapi-with-mic|hostbased) "
                           rf"for (?P<user>\S+) from (?P<ip>{_IP}) port \d+")
_SSH_INVALID = re.compile(rf"Invalid user (?P<user>\S*) from (?P<ip>{_IP})")
_SSH_PREAUTH = re.compile(rf"(?:Disconnected from|Connection closed by|Received disconnect from) "
                          rf"(?:authenticating|invalid) user (?P<user>\S*) (?P<ip>{_IP}) port \d+")
_SUDO_CMD = re.compile(r"(?:^|\s)(?P<user>[\w.@$\-]+) : (?:(?P<fail>[^;=]+?) ; )?(?:TTY=\S+ ; )?PWD=[^;]* ; "
                       r"USER=(?P<runas>\S+) ;(?:\s*\w+=[^;]* ;)*\s*COMMAND=(?P<cmd>.+?)\s*$")
_PAM_FAIL = re.compile(r"pam_unix\((?P<svc>[\w\-]+?)(?::auth)?\): authentication failure;(?P<rest>.*)$")
_PAM_FIELD = re.compile(r"\b(logname|ruser|rhost|user)=(\S*)")
_SU = re.compile(r"(?P<fail>FAILED SU )?\(to (?P<target>[^)\s]+)\) (?P<user>\S+) on \S+")
_F2B = re.compile(rf"\[(?P<jail>[^\]\s]+)\]\s+(?P<action>Ban|Unban|Restore Ban)\s+(?P<ip>{_IP})\s*$")


def _auth(outcome: str, user: str | None, ip: str | None) -> dict:
    out = {"evt.name": "authentication", "evt.outcome": outcome}
    if user:
        out["usr.name"] = user
    if ip:
        out["network.client.ip"] = ip
    return out


def parse_security(message: str) -> dict | None:
    """Attributs de sécurité d'une ligne sshd, sudo, su ou fail2ban, ou None."""
    if not message or len(message) > 4096:
        return None
    if " for " in message or "user " in message:
        m = _SSH_FAILED.search(message)
        if m:
            return _auth("failure", m["user"], m["ip"])
        m = _SSH_ACCEPTED.search(message)
        if m:
            return _auth("success", m["user"], m["ip"])
        m = _SSH_INVALID.search(message)
        if m:
            return _auth("failure", m["user"], m["ip"])
        m = _SSH_PREAUTH.search(message)
        if m:
            return _auth("failure", m["user"], m["ip"])
    if "COMMAND=" in message:
        m = _SUDO_CMD.search(message)
        if m:
            out = {"evt.name": "sudo", "evt.outcome": "failure" if m["fail"] else "success",
                   "usr.name": m["user"], "sudo.command": m["cmd"][:2000], "sudo.run_as": m["runas"]}
            return out
    if "authentication failure" in message:
        m = _PAM_FAIL.search(message)
        if m:
            fields = dict(_PAM_FIELD.findall(m["rest"]))
            user = fields.get("logname") or fields.get("ruser") or fields.get("user")
            out = _auth("failure", user, fields.get("rhost") or None)
            if m["svc"].startswith("sudo"):
                out["evt.category"] = "sudo"
            return out
    if "(to " in message:
        m = _SU.search(message)
        if m:
            out = _auth("failure" if m["fail"] else "success", m["user"], None)
            out["evt.category"] = "su"
            return out
    if "Ban " in message or "Unban " in message:
        m = _F2B.search(message)
        if m:
            return {"evt.name": "unban" if m["action"] == "Unban" else "ban", "evt.outcome": "success",
                    "fail2ban.jail": m["jail"], "network.client.ip": m["ip"]}
    return None


def _present(attrs: dict, dotted: str) -> bool:
    if dotted in attrs:
        return True
    cur = attrs
    for seg in dotted.split("."):
        if not isinstance(cur, dict) or seg not in cur:
            return False
        cur = cur[seg]
    return True


def merge(attrs: dict, produced: dict) -> bool:
    """Pose les attributs produits (en objets imbriqués) sans toucher à l'existant ; vrai si changé."""
    changed = False
    for dotted, value in produced.items():
        if _present(attrs, dotted):
            continue
        *parents, leaf = dotted.split(".")
        cur = attrs
        for seg in parents:
            nxt = cur.setdefault(seg, {})
            if not isinstance(nxt, dict):
                cur = None
                break
            cur = nxt
        if cur is None or leaf in cur:
            continue
        cur[leaf] = value
        changed = True
    return changed


# ── modèles d'analyse par équipement (syslog) ──────────────────────────────────

DEVICE_PARSERS: dict[str, Callable[[str], dict | None]] = {}


def device_parser(name: str):
    """Déclare un modèle d'analyse : fonction message → attributs (noms à points), ou None."""
    def register(fn):
        DEVICE_PARSERS[name] = fn
        return fn
    return register


_KV = re.compile(r'(?:^|[\s,;])([A-Za-z_][A-Za-z0-9_\-]{0,63})=("(?:[^"\\]|\\.)*"|[^\s,;"]*)')
_INT = re.compile(r"^-?\d{1,15}$")
MAX_KV = 60


@device_parser("kv")
def parse_kv(message: str) -> dict | None:
    """Paires clé=valeur (pare-feux Fortinet, Stormshield, Sophos, Palo Alto en mode clé-valeur…) :
    chaque clé devient un attribut de premier niveau ; un entier reste un nombre."""
    if not message or "=" not in message or len(message) > 16384:
        return None
    out = {}
    for m in _KV.finditer(message):
        key, raw = m.group(1), m.group(2)
        if raw.startswith('"'):
            value = raw[1:-1].replace('\\"', '"').replace("\\\\", "\\")
        else:
            value = int(raw) if _INT.match(raw) else raw
        if isinstance(value, str):
            value = value[:1000]
        out.setdefault(key, value)
        if len(out) >= MAX_KV:
            break
    return out or None


def _graver(current: str, wanted) -> str:
    """Le plus grave des deux statuts (un modèle ne rend jamais une ligne moins grave)."""
    if wanted not in LOG_STATUSES or current not in LOG_STATUSES:
        return current
    return wanted if LOG_STATUSES.index(wanted) < LOG_STATUSES.index(current) else current


def device_attributes(attrs: dict, message: str) -> dict | None:
    sl = attrs.get("syslog")
    name = sl.get("parser") if isinstance(sl, dict) else None
    fn = DEVICE_PARSERS.get(name) if isinstance(name, str) else None
    if fn is None:
        return None
    try:
        return fn(message)
    except Exception:  # un modèle ne fait jamais tomber l'ingestion
        return None


def http_status_code(attrs: dict) -> int | None:
    for value in ((attrs.get("http") or {}).get("status_code") if isinstance(attrs.get("http"), dict) else None,
                  attrs.get("http.status_code")):
        if isinstance(value, bool) or value is None:
            continue
        try:
            return int(value)
        except (TypeError, ValueError):
            continue
    return None


def deduced_status(status: str, code: int | None) -> str:
    if status != "info" or code is None:
        return status
    if code >= 500:
        return "error"
    if code >= 400:
        return "warning"
    return status


def process_row(row: tuple) -> tuple:
    message, status, attrs_json = row[_I_MESSAGE], row[_I_STATUS], row[_I_ATTRS]
    if row[_I_SOURCE] == "syslog" and '"parser"' in (attrs_json or ""):
        return _process_device(row)
    produced = parse_access(message)
    if produced is None:
        produced = parse_security(message)
    if produced is None and "status_code" not in (attrs_json or ""):
        return row                                  # cas courant : rien à décoder
    attrs = json.loads(attrs_json or "{}")
    changed = merge(attrs, produced) if produced else False
    new_status = deduced_status(status, http_status_code(attrs))
    if not changed and new_status == status:
        return row
    out = list(row)
    out[_I_STATUS] = new_status
    if changed:
        out[_I_ATTRS] = json.dumps(attrs, ensure_ascii=False, separators=(",", ":"))
    return tuple(out)


def _process_device(row: tuple) -> tuple:
    message, status = row[_I_MESSAGE], row[_I_STATUS]
    attrs = json.loads(row[_I_ATTRS] or "{}")
    produced = dict(device_attributes(attrs, message) or {})
    new_status = _graver(status, produced.pop("status", None))
    changed = merge(attrs, produced) if produced else False
    for extra in (parse_access(message), parse_security(message)):
        if extra:
            changed = merge(attrs, extra) or changed
            break
    new_status = deduced_status(new_status, http_status_code(attrs))
    if not changed and new_status == status:
        return row
    out = list(row)
    out[_I_STATUS] = new_status
    if changed:
        out[_I_ATTRS] = json.dumps(attrs, ensure_ascii=False, separators=(",", ":"))
    return tuple(out)


def process_rows(rows: list[tuple]) -> list[tuple]:
    return [process_row(r) for r in rows]


# Modèles des équipements réseau et de sécurité (vague 4) : ils s'inscrivent dans DEVICE_PARSERS.
from . import equipements  # noqa: E402,F401
