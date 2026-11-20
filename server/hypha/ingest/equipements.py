"""Modèles d'analyse des journaux d'équipements réseau et de sécurité reçus par syslog (vague 4).

Chaque modèle est déclaré par `@device_parser("nom")` (registre `DEVICE_PARSERS` de pipelines.py) et
s'applique aux lignes dont l'agent a posé `syslog.parser` (règle `syslog.parsers` de sa conf). Il rend
des attributs à points, jamais écrasants, en deux familles :

- normalisés, les mêmes pour tous les équipements :
  evt.category (traffic | threat | vpn | admin | system), evt.name, evt.outcome (success | failure),
  fw.action (allow | deny), fw.rule, network.source.ip / .port, network.destination.ip / .port,
  network.protocol, network.interface.in / .out, usr.name, threat.name, threat.type, vpn.tunnel,
  vpn.peer, vpn.status (up | down), device.vendor, device.product, device.name, device.severity ;
- propres à l'éditeur, sous son préfixe (fortinet.*, stormshield.*, filterlog.*, paloalto.*, cisco.asa.*,
  sonicwall.*, watchguard.*, checkpoint.*, mikrotik.*, ubiquiti.*), bornés à MAX_RAW champs.

`network.client.ip` reste l'adresse de l'émetteur (posée par l'agent) : la source d'un flux va dans
`network.source.ip`. Statut aggravé (jamais abaissé) : menace → warning (error si l'équipement la dit
haute ou critique), échec de connexion d'administration ou de VPN, tunnel tombé → warning.
Une connexion d'administration porte evt.name « admin_login », jamais « authentication » (réservé aux
sessions SSH du système, que compte le moniteur d'attaque SSH par force brute).

Formats tirés de la documentation publique des éditeurs (sources citées dans tests/test_equipements.py) ;
éprouvés sur ces exemples, pas sur les équipements réels.
"""
import csv
import re

from .pipelines import device_parser

MAX_RAW = 60
MAX_VALUE = 1000
_INT = re.compile(r"^-?\d{1,15}$")
_IPV4 = re.compile(r"^\d{1,3}(?:\.\d{1,3}){3}$")
_IPV6 = re.compile(r"^[0-9A-Fa-f:]*:[0-9A-Fa-f:.]*$")

# Paires clé=valeur séparées par des blancs ; valeur entre guillemets (échappements \" \\) ou mot.
_KV = re.compile(r'(?:^|(?<=\s))([A-Za-z_][A-Za-z0-9_.\-]{0,63})=("(?:[^"\\]|\\.)*"|[^\s"]*)')

PROTOCOLS = {"1": "icmp", "2": "igmp", "6": "tcp", "17": "udp", "41": "ipv6", "47": "gre", "50": "esp",
             "51": "ah", "58": "ipv6-icmp", "89": "ospf", "112": "vrrp", "132": "sctp"}


# ── outils communs ─────────────────────────────────────────────────────────────

def _value(raw: str):
    if raw.startswith('"') and raw.endswith('"') and len(raw) >= 2:
        return raw[1:-1].replace('\\"', '"').replace("\\\\", "\\")[:MAX_VALUE]
    return int(raw) if _INT.match(raw) else raw[:MAX_VALUE]


def kv_pairs(message: str) -> dict:
    """Paires clé=valeur d'un message (la première occurrence d'une clé l'emporte)."""
    out = {}
    for m in _KV.finditer(message):
        out.setdefault(m.group(1), _value(m.group(2)))
        if len(out) >= 200:
            break
    return out


def _raw(prefix: str, pairs: dict) -> dict:
    """Champs de l'éditeur sous son préfixe, MAX_RAW au plus, clés sans point."""
    out = {}
    for k, v in pairs.items():
        if v in ("", None) or "." in k:
            continue
        out[f"{prefix}.{k}"] = v if isinstance(v, int) else str(v)[:MAX_VALUE]
        if len(out) >= MAX_RAW:
            break
    return out


def _ip(v) -> str | None:
    v = str(v or "").strip().strip("[]")
    if _IPV4.match(v) or (_IPV6.match(v) and v.count(":") >= 2):
        return v
    return None


def _port(v) -> int | None:
    try:
        p = int(str(v).strip())
    except (TypeError, ValueError):
        return None
    return p if 0 <= p <= 65535 else None


def _proto(v) -> str | None:
    if v in (None, ""):
        return None
    s = str(v).strip().lower()
    return PROTOCOLS.get(s, s) if s else None


def _put(out: dict, key: str, value) -> None:
    """Pose une valeur utile (ni vide, ni « N/A »)."""
    if value is None:
        return
    if isinstance(value, str):
        value = value.strip()
        if value in ("", "N/A", "n/a", "-", "none", "None", "(none)"):
            return
    out[key] = value


def _endpoints(out: dict, src=None, sport=None, dst=None, dport=None) -> None:
    _put(out, "network.source.ip", _ip(src))
    _put(out, "network.source.port", _port(sport) if sport not in (None, "") else None)
    _put(out, "network.destination.ip", _ip(dst))
    _put(out, "network.destination.port", _port(dport) if dport not in (None, "") else None)


def _base(vendor: str, product: str, category: str, name: str | None = None) -> dict:
    out = {"device.vendor": vendor, "device.product": product, "evt.category": category}
    _put(out, "evt.name", name)
    return out


def _threat_status(level: str | None) -> str:
    """Statut d'une menace selon la gravité qu'en donne l'équipement."""
    s = str(level or "").strip().lower()
    return "error" if s in ("critical", "high", "crit", "alert", "emergency", "emerg", "error", "5") else "warning"


def _admin_login(out: dict, success: bool | None, user=None) -> dict:
    out["evt.category"] = "admin"
    out["evt.name"] = "admin_login"
    _put(out, "usr.name", user)
    if success is not None:
        out["evt.outcome"] = "success" if success else "failure"
        if not success:
            out["status"] = "warning"
    return out


DENY_WORDS = ("deny", "denied", "drop", "dropped", "block", "blocked", "reject", "rejected", "reset", "prevent",
              "discard", "discarded", "refuse", "refused")


def _action(v, allow=(), deny=()) -> str | None:
    """allow | deny selon les mots de l'équipement, None si inconnu."""
    s = str(v or "").strip().lower()
    if not s:
        return None
    if s in deny or any(s.startswith(w) for w in DENY_WORDS):
        return "deny"
    if s in allow or s in ("allow", "allowed", "accept", "accepted", "pass", "permit", "permitted", "passthrough",
                           "forward", "start", "close", "end", "timeout"):
        return "allow"
    return None


def _cef(message: str) -> tuple[list[str], dict] | None:
    """En-tête (version, éditeur, produit, version, id, nom, gravité) et extension d'une ligne CEF ;
    tolère l'absence de « CEF: » (pris pour l'étiquette syslog par l'agent)."""
    i = message.find("CEF:")
    body = message[i + 4:] if i >= 0 else message
    parts, cur, k = [], [], 0
    while k < len(body) and len(parts) < 7:
        c = body[k]
        if c == "\\" and k + 1 < len(body):
            cur.append(body[k + 1])
            k += 2
            continue
        if c == "|":
            parts.append("".join(cur))
            cur = []
        else:
            cur.append(c)
        k += 1
    if len(parts) < 7 or not parts[0].strip().split()[-1:] or not parts[0].strip().split()[-1].isdigit():
        return None
    parts[0] = parts[0].strip().split()[-1]
    return parts, _cef_ext(body[k:])


_CEF_KEY = re.compile(r"(?:^|(?<=\s))([A-Za-z0-9_.\-]{1,63})=")


def _cef_ext(ext: str) -> dict:
    """Extension CEF : la valeur court jusqu'à la clé suivante (blancs permis, « \\= » échappé)."""
    keys = [m for m in _CEF_KEY.finditer(ext) if not (m.start() > 0 and ext[m.start() - 1] == "\\")]
    out = {}
    for n, m in enumerate(keys):
        end = keys[n + 1].start() if n + 1 < len(keys) else len(ext)
        val = ext[m.end():end].strip().replace("\\=", "=").replace("\\\\", "\\")
        out.setdefault(m.group(1), val[:MAX_VALUE])
        if len(out) >= 200:
            break
    return out


# ── Fortinet FortiGate (FortiOS, clé=valeur) ───────────────────────────────────

_FORTI_THREAT_SUBTYPES = {"virus", "ips", "anomaly", "waf", "virtual-patch", "dlp", "botnet", "emailfilter"}
_FORTI_THREAT_TYPES = {"virus", "ips", "anomaly", "attack"}


@device_parser("fortinet")
def parse_fortinet(message: str) -> dict | None:
    if "=" not in message or ("logid=" not in message and "type=" not in message):
        return None
    kv = kv_pairs(message)
    ftype, sub = str(kv.get("type", "")).lower(), str(kv.get("subtype", "")).lower()
    if not ftype and "logid" not in kv:
        return None
    logid = str(kv.get("logid", ""))
    desc = str(kv.get("logdesc", "")).lower()
    if ftype == "traffic":
        cat = "traffic"
    elif ftype in _FORTI_THREAT_TYPES or (ftype == "utm" and sub in _FORTI_THREAT_SUBTYPES):
        cat = "threat"
    elif ftype == "utm":
        cat = "traffic"
    elif sub == "vpn":
        cat = "vpn"
    else:
        cat = "system"
    out = _base("Fortinet", "FortiGate", cat, sub or ftype or None)
    _put(out, "device.name", kv.get("devname"))
    _put(out, "device.severity", kv.get("level"))
    _endpoints(out, kv.get("srcip"), kv.get("srcport"), kv.get("dstip"), kv.get("dstport"))
    _put(out, "network.protocol", _proto(kv.get("proto")))
    _put(out, "network.interface.in", kv.get("srcintf"))
    _put(out, "network.interface.out", kv.get("dstintf"))
    _put(out, "fw.rule", kv.get("policyname") or (str(kv["policyid"]) if "policyid" in kv else None))
    _put(out, "usr.name", kv.get("user") or kv.get("unauthuser"))
    action = kv.get("action")
    if cat == "traffic":
        _put(out, "fw.action", _action(action, allow=("server-rst", "client-rst", "ip-conn", "dns", "monitored")))
    elif cat == "threat":
        _put(out, "fw.action", _action(action, allow=("detected", "monitored", "pass", "analytics")))
        _put(out, "threat.name", kv.get("attack") or kv.get("virus") or kv.get("msg"))
        _put(out, "threat.type", sub or ftype)
        out["status"] = _threat_status(kv.get("severity") or kv.get("level"))
    elif cat == "vpn":
        _put(out, "vpn.tunnel", kv.get("vpntunnel") or kv.get("tunnelid"))
        _put(out, "vpn.peer", _ip(kv.get("remip")))
        st = str(kv.get("status", "")).lower()
        if st in ("success", "failure", "failed", "negotiate_error", "error"):
            out["evt.outcome"] = "success" if st == "success" else "failure"
        if "tunnel down" in desc or str(action).lower() == "tunnel-down" or logid.endswith(("23102", "39425", "39948")):
            out["vpn.status"], out["status"] = "down", "warning"
        elif "tunnel up" in desc or str(action).lower() == "tunnel-up" or logid.endswith(("23101", "39424", "39947")):
            out["vpn.status"] = "up"
        if out.get("evt.outcome") == "failure":
            out["status"] = "warning"
    if logid.endswith(("32001", "32002")) or "admin login" in desc:
        st = str(kv.get("status", "")).lower()
        _admin_login(out, None if not st else st == "success", kv.get("user"))
        _put(out, "network.source.ip", _ip(kv.get("srcip")))
    out.update(_raw("fortinet", kv))
    return out


# ── Stormshield SNS (WELF, clé=valeur) ─────────────────────────────────────────

@device_parser("stormshield")
def parse_stormshield(message: str) -> dict | None:
    if "id=firewall" not in message and "fw=" not in message:
        return None
    kv = kv_pairs(message)
    if kv.get("id") != "firewall" and "fw" not in kv:
        return None
    logtype = str(kv.get("logtype", "")).lower()
    if logtype == "alarm" or "alarmid" in kv:
        cat = "threat"
    elif logtype == "vpn":
        cat = "vpn"
    elif logtype == "server":
        cat = "admin"
    elif logtype in ("system", "auth", "date", "monitor", "count", "filterstat") or (not logtype and "src" not in kv):
        cat = "system"
    else:
        cat = "traffic"
    out = _base("Stormshield", "SNS", cat, logtype or ("alarm" if cat == "threat" else None))
    _put(out, "device.name", kv.get("fw"))
    _put(out, "device.severity", kv.get("pri"))
    _endpoints(out, kv.get("src"), kv.get("srcport"), kv.get("dst"), kv.get("dstport"))
    _put(out, "network.protocol", _proto(kv.get("ipproto")))
    _put(out, "network.interface.in", kv.get("srcifname") or kv.get("srcif"))
    _put(out, "network.interface.out", kv.get("dstifname") or kv.get("dstif"))
    _put(out, "fw.rule", kv.get("rulename") or (str(kv["ruleid"]) if "ruleid" in kv else None))
    _put(out, "usr.name", kv.get("user"))
    _put(out, "fw.action", _action(kv.get("action"), allow=("log", "bypass", "nat", "decrypt")))
    error = kv.get("error")
    failed = error not in (None, "", 0, "0")
    if cat == "threat":
        _put(out, "threat.name", kv.get("msg"))
        _put(out, "threat.type", kv.get("classification") if kv.get("classification") not in (None, "", 0) else kv.get("class"))
        # pri : 1 = alarme majeure, 4 = alarme mineure (niveaux syslog).
        out["status"] = "error" if kv.get("pri") in (0, 1, 2, 3) else "warning"
    elif cat == "vpn":
        _put(out, "vpn.peer", _ip(kv.get("dst")))
        if error is not None:
            out["evt.outcome"] = "failure" if failed else "success"
            if failed:
                out["status"] = "warning"
    elif cat == "admin":
        msg = str(kv.get("msg", "")).lower()
        bad = failed or any(w in msg for w in ("fail", "denied", "invalid", "bad password", "refus", "échec"))
        _admin_login(out, not bad, kv.get("user"))
    out.update(_raw("stormshield", kv))
    return out


# ── pfSense et OPNsense (filterlog, CSV) ───────────────────────────────────────

_FILTERLOG_TAG = re.compile(r"filterlog(?:\[\d+\])?:\s*")
_PFS_AUTH_FAIL = re.compile(r"authentication error for user '(?P<user>[^']*)' from:?\s*(?P<ip>[0-9A-Fa-f.:]+)")
_PFS_AUTH_OK = re.compile(r"Successful login for user '(?P<user>[^']*)' from:?\s*(?P<ip>[0-9A-Fa-f.:]+)")


def parse_filterlog(message: str) -> dict | None:
    """Ligne filterlog (grammaire « Raw Filter Log Format » de Netgate), sans ou avec l'étiquette."""
    m = _FILTERLOG_TAG.search(message)
    text = message[m.end():] if m else message
    f = text.strip().split(",")
    if len(f) < 9 or f[8] not in ("4", "6") or f[7] not in ("in", "out"):
        return None
    out = {"evt.category": "traffic", "evt.name": "filterlog"}
    _put(out, "fw.action", _action(f[6]))
    _put(out, "fw.rule", f[3] or f[0])
    _put(out, "network.interface.in" if f[7] == "in" else "network.interface.out", f[4])
    raw = {"rule_number": f[0], "sub_rule": f[1], "anchor": f[2], "tracker": f[3], "interface": f[4],
           "reason": f[5], "action": f[6], "direction": f[7], "ip_version": f[8]}
    if f[8] == "4" and len(f) >= 20:
        proto_id, proto, src, dst, rest = f[15], f[16], f[18], f[19], f[20:]
        raw.update({"ttl": f[11], "length": f[17]})
    elif f[8] == "6" and len(f) >= 17:
        proto, proto_id, src, dst, rest = f[12], f[13], f[15], f[16], f[17:]
        raw.update({"hop_limit": f[11], "length": f[14]})
    else:
        return out | _raw("filterlog", raw)
    _put(out, "network.protocol", (proto or "").lower() or _proto(proto_id))
    sport = dport = None
    if proto_id in ("6", "17") and len(rest) >= 2:
        sport, dport = rest[0], rest[1]
        if proto_id == "6" and len(rest) >= 4:
            raw["tcp_flags"] = rest[3]
    _endpoints(out, src, sport, dst, dport)
    return out | _raw("filterlog", {k: (int(v) if _INT.match(v or "") else v) for k, v in raw.items()})


def _netgate(product: str):
    def parse(message: str) -> dict | None:
        out = parse_filterlog(message)
        if out is not None:
            return {"device.vendor": "Netgate" if product == "pfSense" else "Deciso", "device.product": product} | out
        for rx, ok in ((_PFS_AUTH_FAIL, False), (_PFS_AUTH_OK, True)):
            m = rx.search(message)
            if m:
                res = _base("Netgate" if product == "pfSense" else "Deciso", product, "admin")
                _admin_login(res, ok, m["user"])
                _put(res, "network.source.ip", _ip(m["ip"]))
                return res
        return None
    return parse


parse_pfsense = device_parser("pfsense")(_netgate("pfSense"))
parse_opnsense = device_parser("opnsense")(_netgate("OPNsense"))


# ── Palo Alto Networks PAN-OS (CSV TRAFFIC, THREAT, SYSTEM, CONFIG, GLOBALPROTECT) ─

_PAN_HEAD = re.compile(r"(?:^|[\s>])(?P<csv>[^,\s]*,\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2},[^,]*,"
                       r"(?P<type>TRAFFIC|THREAT|SYSTEM|CONFIG|GLOBALPROTECT|USERID|AUTH|HIPMATCH|DECRYPTION),.*)$")


def _pan_get(f: list, i: int) -> str | None:
    return f[i] if i < len(f) and f[i] != "" else None


@device_parser("paloalto")
def parse_paloalto(message: str) -> dict | None:
    m = _PAN_HEAD.search(message)
    if not m:
        return None
    try:
        f = next(csv.reader([m["csv"]]))
    except (csv.Error, StopIteration):
        return None
    t, sub = f[3], (f[4] if len(f) > 4 else "")
    g = lambda i: _pan_get(f, i)  # noqa: E731
    raw = {"type": t, "subtype": sub, "serial": g(2), "receive_time": g(1), "generated_time": g(6)}
    if t in ("TRAFFIC", "THREAT"):
        cat = "traffic"
        if t == "THREAT":
            cat = "traffic" if sub in ("url", "file", "data") and (g(34) or "").lower() == "informational" else "threat"
        out = _base("Palo Alto Networks", "PAN-OS", cat, sub.lower() or t.lower())
        _endpoints(out, g(7), g(24), g(8), g(25))
        _put(out, "network.protocol", _proto(g(29)))
        _put(out, "fw.rule", g(11))
        _put(out, "usr.name", g(12))
        _put(out, "network.interface.in", g(18))
        _put(out, "network.interface.out", g(19))
        raw.update({"application": g(14), "vsys": g(15), "zone_from": g(16), "zone_to": g(17), "session_id": g(22)})
        if t == "TRAFFIC":
            _put(out, "fw.action", _action(g(30)))
            raw.update({"bytes": g(31), "session_end_reason": g(46)})
            _put(out, "device.name", g(52))
        else:
            _put(out, "fw.action", _action(g(30), allow=("alert",)))
            _put(out, "device.severity", g(34))
            raw.update({"url_or_file": g(31), "threat_category": g(33), "direction": g(35)})
            _put(out, "device.name", g(59))
            if cat == "threat":
                _put(out, "threat.name", g(32))
                _put(out, "threat.type", sub.lower())
                sev = (g(34) or "").lower()
                if sev != "informational":
                    out["status"] = _threat_status(sev)
    elif t == "SYSTEM":
        event, desc, sev = g(8) or "", g(14) or "", (g(13) or "").lower()
        low = (event + " " + desc).lower()
        if sub in ("vpn", "globalprotect", "global-protect", "sslvpn", "ras"):
            cat = "vpn"
        elif event.startswith("auth-") or "authentication" in low and "admin" in low or sub == "auth":
            cat = "admin"
        else:
            cat = "system"
        out = _base("Palo Alto Networks", "PAN-OS", cat, event or sub)
        _put(out, "device.severity", sev)
        _put(out, "device.name", g(22))
        raw.update({"event_id": event, "object": g(9), "module": g(12), "description": desc})
        if cat == "admin":
            fail = "fail" in event or "failed" in desc.lower()
            mu = re.search(r"for user '([^']+)'", desc)
            _admin_login(out, not fail, mu.group(1) if mu else None)
            mi = re.search(r"From: ([0-9A-Fa-f.:]+?)\.?$", desc.strip())
            _put(out, "network.source.ip", _ip(mi.group(1)) if mi else None)
        elif cat == "vpn":
            if "fail" in low:
                out["evt.outcome"], out["status"] = "failure", "warning"
            if re.search(r"\bdown\b|tunnel-down|deleted|delete", low):
                out["vpn.status"], out["status"] = "down", "warning"
            elif re.search(r"\bup\b|tunnel-up|succ", low):
                out["vpn.status"] = "up"
            _put(out, "vpn.tunnel", g(9))
        if sev in ("high", "critical"):
            out["status"] = "error"
    elif t == "CONFIG":
        out = _base("Palo Alto Networks", "PAN-OS", "admin", "config")
        _put(out, "usr.name", g(10))
        _put(out, "network.source.ip", _ip(g(11)))
        res = (g(12) or "").lower()
        if res:
            out["evt.outcome"] = "success" if res.startswith("succ") else "failure"
        raw.update({"host": g(7), "command": g(9), "result": g(12), "path": g(13)})
    elif t == "GLOBALPROTECT":
        out = _base("Palo Alto Networks", "PAN-OS", "vpn", g(8))
        _put(out, "usr.name", g(12))
        _put(out, "network.source.ip", _ip(g(15)))
        _put(out, "vpn.tunnel", g(11))
        _put(out, "vpn.peer", _ip(g(15)))
        st = (g(28) or "").lower()
        if st:
            out["evt.outcome"] = "success" if st == "success" else "failure"
            if st != "success":
                out["status"] = "warning"
        raw.update({"stage": g(9), "auth_method": g(10), "private_ip": g(17), "error": g(26), "description": g(27),
                    "portal": g(33)})
    else:
        out = _base("Palo Alto Networks", "PAN-OS", "system", t.lower())
    return out | _raw("paloalto", {k: v for k, v in raw.items() if v is not None})


# ── Cisco ASA (et FTD) : %ASA-n-nnnnnn ─────────────────────────────────────────

_ASA = re.compile(r"%(?P<fac>ASA|FTD|PIX|FWSM)-(?:session-)?(?P<lvl>[0-7])-(?P<id>\d{6}):?\s*(?P<body>.*)$")
_A = r"(?P<{0}if>[^\s:/]+)[:/](?P<{0}>[0-9A-Fa-f.:]+)(?:/(?P<{0}p>\d+)|\((?P<{0}p2>\d+)\))?"
_ASA_106023 = re.compile(r"^Deny (?P<proto>\S+) src " + _A.format("s") + r" dst " + _A.format("d")
                         + r"(?: \(type \d+, code \d+\))? by access-group \"(?P<acl>[^\"]+)\"")
_ASA_106100 = re.compile(r"^access-list (?P<acl>\S+) (?P<act>permitted|denied|est-allowed) (?P<proto>\S+) "
                         + _A.format("s") + r" ?-> ?" + _A.format("d"))
_ASA_FROM_TO = re.compile(r"^(?:Deny|Inbound|Outbound)? ?(?P<proto>TCP|UDP|ICMP|tcp|udp|icmp|\S+)?.*?from "
                          r"(?P<s>[0-9A-Fa-f.:]+?)(?:/(?P<sp>\d+))? to (?P<d>[0-9A-Fa-f.:]+?)(?:/(?P<dp>\d+))?"
                          r"(?: flags \S+)?(?: on interface (?P<ifc>\S+))?$")
_ASA_BUILT = re.compile(r"^(?P<what>Built|Teardown) (?:(?P<dir>inbound|outbound) )?(?P<proto>TCP|UDP|SCTP|GRE)"
                        r" connection (?P<cid>\d+) for " + _A.format("a") + r"(?: \([^)]*\))? to "
                        + _A.format("b"))
_ASA_ICMP = re.compile(r"^(?P<what>Built|Teardown) (?:(?P<dir>inbound|outbound) )?ICMP connection.*?faddr "
                       r"(?P<f>[0-9A-Fa-f.:]+)/\d+ gaddr (?P<g>[0-9A-Fa-f.:]+)/\d+ laddr (?P<l>[0-9A-Fa-f.:]+)")
_ASA_IDS = re.compile(r"^(?P<sig>IDS:\d+ [^\n]*?) from (?P<s>[0-9A-Fa-f.:]+) to (?P<d>[0-9A-Fa-f.:]+) on interface "
                      r"(?P<ifc>\S+)")
_ASA_AAA = re.compile(r"AAA user (?P<kind>authentication|authorization) (?P<res>Rejected|Successful)"
                      r".*?user = ?(?P<user>[^:\s]+)?.*?(?:user IP = (?P<ip>[0-9A-Fa-f.:]+))?\s*$")
_ASA_LOGIN = re.compile(r"Login (?P<res>permitted|denied) from (?P<s>[0-9A-Fa-f.:]+)/(?P<sp>\d+) to "
                        r"(?P<ifc>[^:\s]+):(?P<d>[0-9A-Fa-f.:]+)/(?P<svc>\S+) for user \"?(?P<user>[^\"]*)\"?")
_ASA_VPN_SESSION = re.compile(r"Group = (?P<grp>[^,]*), Username = (?P<user>[^,]*), IP = (?P<ip>[0-9A-Fa-f.:]+), "
                              r"Session disconnected\. Session Type: (?P<type>[^,]*),.*?Reason: (?P<reason>.*)$")
_ASA_CMD = re.compile(r"User '(?P<user>[^']+)' executed (?:the )?'(?P<cmd>[^']*)' command")
_ASA_VPN_IDS = ("713", "722", "725", "750", "751", "752", "602")


def _asa_ends(out: dict, m, a: str, b: str) -> None:
    _endpoints(out, m[a], m[a + "p"] or m[a + "p2"], m[b], m[b + "p"] or m[b + "p2"])
    _put(out, "network.interface.in", m[a + "if"])
    _put(out, "network.interface.out", m[b + "if"])


@device_parser("cisco-asa")
def parse_cisco_asa(message: str) -> dict | None:
    m = _ASA.search(message)
    if not m:
        return None
    mid, body, lvl = m["id"], m["body"].strip(), int(m["lvl"])
    product = {"ASA": "ASA", "FTD": "Firepower Threat Defense", "PIX": "PIX", "FWSM": "FWSM"}[m["fac"]]
    out = _base("Cisco", product, "system", mid)
    out["device.severity"] = lvl
    raw = {"message_id": mid, "level": lvl}
    if mid == "106023" and (x := _ASA_106023.match(body)):
        out["evt.category"], out["fw.action"], out["fw.rule"] = "traffic", "deny", x["acl"]
        _put(out, "network.protocol", _proto(x["proto"]))
        _asa_ends(out, x, "s", "d")
    elif mid == "106100" and (x := _ASA_106100.match(body)):
        out["evt.category"], out["fw.rule"] = "traffic", x["acl"]
        out["fw.action"] = "deny" if x["act"] == "denied" else "allow"
        _put(out, "network.protocol", _proto(x["proto"]))
        _asa_ends(out, x, "s", "d")
    elif mid.startswith("106") and (x := _ASA_FROM_TO.match(body)):
        out["evt.category"], out["fw.action"] = "traffic", "deny"
        mp = re.search(r"\b(TCP|UDP|ICMP|tcp|udp|icmp)\b", body)
        _put(out, "network.protocol", mp.group(1).lower() if mp else None)
        _endpoints(out, x["s"], x["sp"], x["d"], x["dp"])
        _put(out, "network.interface.in", x["ifc"])
    elif mid in ("302013", "302014", "302015", "302016") and (x := _ASA_BUILT.match(body)):
        out["evt.category"], out["fw.action"] = "traffic", "allow"
        out["network.protocol"] = x["proto"].lower()
        if x["dir"] == "outbound":            # « outbound » : le côté « for » est la destination
            _asa_ends(out, x, "b", "a")
        else:
            _asa_ends(out, x, "a", "b")
        raw["connection_id"] = int(x["cid"])
    elif mid in ("302020", "302021") and (x := _ASA_ICMP.match(body)):
        out["evt.category"], out["fw.action"], out["network.protocol"] = "traffic", "allow", "icmp"
        if x["dir"] == "outbound":
            _endpoints(out, x["l"], None, x["f"], None)
        else:
            _endpoints(out, x["f"], None, x["l"], None)
    elif mid.startswith("4000") and (x := _ASA_IDS.match(body)):
        out["evt.category"], out["threat.name"], out["threat.type"] = "threat", x["sig"], "ids"
        _endpoints(out, x["s"], None, x["d"], None)
        _put(out, "network.interface.in", x["ifc"])
        out["status"] = "error" if lvl <= 2 else "warning"
    elif mid in ("733100", "733101", "733102", "733104", "733105"):
        out["evt.category"], out["threat.type"], out["threat.name"] = "threat", "threat-detection", body[:200]
        out["status"] = "warning"
    elif mid in ("113004", "113005", "113015") and (x := _ASA_AAA.search(body)):
        _admin_login(out, x["res"] == "Successful", x["user"])
        raw["aaa"] = x["kind"]
        _put(out, "network.source.ip", _ip(x["ip"]))
        mr = re.search(r"reason = ([^:]+?) :", body)
        if mr:
            raw["reason"] = mr.group(1).strip()
    elif mid in ("605004", "605005") and (x := _ASA_LOGIN.search(body)):
        _admin_login(out, x["res"] == "permitted", x["user"])
        _endpoints(out, x["s"], x["sp"], x["d"], None)
        raw["service"] = x["svc"]
    elif mid in ("611101", "611102"):
        mu = re.search(r"Uname: (\S+)", body)
        _admin_login(out, mid == "611101", mu.group(1) if mu else None)
        mi = re.search(r"IP address: ([0-9A-Fa-f.:]+)", body)
        _put(out, "network.source.ip", _ip(mi.group(1)) if mi else None)
    elif mid in ("111008", "111009", "111010") and (x := _ASA_CMD.search(body)):
        out["evt.category"], out["evt.name"], out["usr.name"] = "admin", "command", x["user"]
        raw["command"] = x["cmd"]
    elif mid == "113019" and (x := _ASA_VPN_SESSION.search(body)):
        out["evt.category"], out["evt.name"] = "vpn", "session_disconnected"
        _put(out, "usr.name", x["user"])
        _put(out, "vpn.peer", _ip(x["ip"]))
        _put(out, "vpn.tunnel", x["grp"])
        raw.update({"session_type": x["type"].strip(), "reason": x["reason"].strip()})
        if "lan-to-lan" in x["type"].lower():    # tunnel de site à site, pas une fin de session d'un nomade
            out["vpn.status"], out["status"] = "down", "warning"
    elif mid.startswith(_ASA_VPN_IDS):
        out["evt.category"] = "vpn"
        low = body.lower()
        mp = re.search(r"(?:IP|peer) = ?([0-9A-Fa-f.:]+)", body)
        _put(out, "vpn.peer", _ip(mp.group(1)) if mp else None)
        if re.search(r"terminated|torn down|tunnel.*down|lost", low):   # SA effacée au renouvellement : pas une panne
            out["vpn.status"], out["status"] = "down", "warning"
        elif re.search(r"phase 2 completed|tunnel.*up|established", low):
            out["vpn.status"] = "up"
        if re.search(r"fail|error|reject|mismatch", low):
            out["evt.outcome"], out["status"] = "failure", "warning"
    elif mid.startswith(("106", "302", "303", "304", "305", "710", "209")):
        out["evt.category"] = "traffic"
    out["evt.name"] = out.get("evt.name") or mid
    return out | _raw("cisco.asa", raw)


# ── SonicWall SonicOS (clé=valeur) ─────────────────────────────────────────────

_SW_THREAT = re.compile(r"port scan|attack|intrusion|\bips\b|virus|spyware|botnet|malware|flood|exploit|"
                        r"\bidp\b|trojan|worm|smurf|land attack|ping of death|syn flood", re.I)
_SW_VPN = re.compile(r"\bipsec\b|\bike\b|\bvpn\b|\bsa\b|\btunnel\b|ssl ?vpn|l2tp|netextender", re.I)
_SW_LOGIN = re.compile(r"login|log in|logged in|logout|authentication|credentials", re.I)


def _sw_endpoint(v) -> tuple:
    """« ip:port:interface[:nom] » de SonicOS (IPv4)."""
    if not isinstance(v, str) or not v:
        return None, None, None
    parts = v.split(":")
    return parts[0], (parts[1] if len(parts) > 1 else None), (parts[2] if len(parts) > 2 else None)


@device_parser("sonicwall")
def parse_sonicwall(message: str) -> dict | None:
    if "id=" not in message or " m=" not in message and "msg=" not in message:
        return None
    kv = kv_pairs(message)
    if "m" not in kv and "msg" not in kv:
        return None
    msg = str(kv.get("msg", ""))
    mid = kv.get("m")
    sip, sport, sif = _sw_endpoint(kv.get("src"))
    dip, dport, dif = _sw_endpoint(kv.get("dst"))
    if _SW_THREAT.search(msg) or mid in (82, 83) or kv.get("ipscat") or kv.get("ipspri"):
        cat = "threat"
    elif _SW_LOGIN.search(msg):
        cat = "admin" if re.search(r"admin", msg, re.I) else "system"
    elif _SW_VPN.search(msg):
        cat = "vpn"
    elif sip or dip:
        cat = "traffic"
    else:
        cat = "system"
    out = _base("SonicWall", "SonicOS", cat, str(mid) if mid not in (None, "") else None)
    _put(out, "device.severity", kv.get("pri"))
    _endpoints(out, sip, sport, dip, dport)
    _put(out, "network.interface.in", sif)
    _put(out, "network.interface.out", dif)
    proto = kv.get("proto")
    _put(out, "network.protocol", str(proto).split("/")[0].lower() if proto else None)
    _put(out, "fw.rule", kv.get("rule"))
    _put(out, "usr.name", kv.get("usr") or kv.get("user"))
    act = _action(kv.get("fw_action"), allow=("forward", "na"))
    if act is None or str(kv.get("fw_action", "")).upper() == "NA":
        act = "deny" if re.search(r"drop|denied|block|reject|discard", msg, re.I) else (
            "allow" if re.search(r"connection (opened|closed)|allowed|forward", msg, re.I) else None)
    _put(out, "fw.action", act)
    if cat == "threat":
        _put(out, "threat.name", msg or kv.get("ipscat"))
        _put(out, "threat.type", kv.get("ipscat") or ("scan" if "scan" in msg.lower() else None))
        out["status"] = "error" if kv.get("pri") in (0, 1, 2) else "warning"
    elif cat in ("admin", "system") and _SW_LOGIN.search(msg):
        bad = re.search(r"denied|fail|bad credentials|invalid|locked", msg, re.I) is not None
        if cat == "admin":
            _admin_login(out, not bad, kv.get("usr") or kv.get("user"))
        else:
            out["evt.name"], out["evt.outcome"] = "user_login", "failure" if bad else "success"
        _put(out, "network.source.ip", _ip(sip))
    elif cat == "vpn":
        if re.search(r"fail|error|mismatch|reject|denied", msg, re.I):
            out["evt.outcome"], out["status"] = "failure", "warning"
        if re.search(r"tunnel (is )?down|disconnected|deleted|removed", msg, re.I):
            out["vpn.status"], out["status"] = "down", "warning"
        elif re.search(r"tunnel (is )?up|established|negotiation complete", msg, re.I):
            out["vpn.status"] = "up"
    out.update(_raw("sonicwall", kv))
    return out


# ── WatchGuard Firebox (Fireware) ──────────────────────────────────────────────

_WG_PKT = re.compile(r"(?:^|\s)(?P<disp>Allow|Deny) (?P<inif>\S+) (?P<outif>\S+) (?P<len>\d+) (?P<proto>[A-Za-z0-9\-]+) "
                     r"(?P<iphl>\d+) (?P<ttl>\d+) (?P<src>\S+) (?P<dst>\S+)(?: (?P<sp>\d+) (?P<dp>\d+))?")
_WG_MON = re.compile(r"(?:^|\s)(?P<disp>Allow|Deny) (?P<src>[0-9A-Fa-f.:]+) (?P<dst>[0-9A-Fa-f.:]+) "
                     r"(?P<svc>[\w.\-]+)/(?P<proto>[a-z0-9\-]+)(?: (?P<sp>\d+) (?P<dp>\d+))? (?P<inif>\S+) (?P<outif>\S+)")
_WG_POLICY = re.compile(r"\((?P<policy>[^()]+-\d+)\)")
_WG_AUTH = re.compile(r"Authentication of (?P<kind>\w+) user \[(?P<user>[^@\]]*)(?:@(?P<srv>[^\]]*))?\] from "
                      r"(?P<ip>[0-9A-Fa-f.:]+) was (?P<res>accepted|rejected)")
_WG_LOCK = re.compile(r"User (?P<user>\S+) is locked out .*?after (?P<n>\d+) login failures")
_WG_MGMT = re.compile(r"Management user (?P<user>[^@\s]+)@\S+ from (?P<ip>[0-9A-Fa-f.:]+)")


@device_parser("watchguard")
def parse_watchguard(message: str) -> dict | None:
    kv = kv_pairs(message)
    msg_id = str(kv.get("msg_id") or kv.get("id") or "")
    m = _WG_PKT.search(message) or _WG_MON.search(message)
    if m and m.re is _WG_PKT and not (_ip(m["src"]) or _ip(m["dst"])):
        m = _WG_MON.search(message)
    out = None
    if m:
        threat = "signature_name" in kv or re.search(r"IPS detected|virus|botnet|malware", str(kv.get("msg", "")), re.I)
        out = _base("WatchGuard", "Firebox", "threat" if threat else "traffic", msg_id or None)
        out["fw.action"] = "allow" if m["disp"] == "Allow" else "deny"
        _endpoints(out, m["src"], m["sp"], m["dst"], m["dp"])
        _put(out, "network.protocol", m["proto"].lower())
        _put(out, "network.interface.in", m["inif"])
        _put(out, "network.interface.out", m["outif"])
        p = _WG_POLICY.findall(message)
        _put(out, "fw.rule", p[-1] if p else None)
        if threat:
            _put(out, "threat.name", kv.get("signature_name") or kv.get("msg"))
            _put(out, "threat.type", kv.get("signature_cat") or "ips")
            _put(out, "device.severity", kv.get("severity"))
            out["status"] = "error" if kv.get("severity") in (4, 5, "4", "5") else "warning"
    elif (a := _WG_AUTH.search(message)):
        out = _base("WatchGuard", "Firebox", "admin", msg_id or None)
        ok = a["res"] == "accepted"
        if a["kind"].lower() == "management":
            _admin_login(out, ok, a["user"])
        else:
            out["evt.category"], out["evt.name"] = "system", "user_auth"
            out["evt.outcome"] = "success" if ok else "failure"
            _put(out, "usr.name", a["user"])
            if not ok:
                out["status"] = "warning"
        _put(out, "network.source.ip", _ip(a["ip"]))
    elif (a := _WG_LOCK.search(message)):
        out = _base("WatchGuard", "Firebox", "system", "account_locked")
        out["usr.name"], out["evt.outcome"], out["status"] = a["user"], "failure", "warning"
    elif (a := _WG_MGMT.search(message)):
        out = _base("WatchGuard", "Firebox", "admin", "config")
        _put(out, "usr.name", a["user"])
        _put(out, "network.source.ip", _ip(a["ip"]))
    elif msg_id or "firewall:" in message or "msg_id" in message:
        low = message.lower()
        vpn = re.search(r"bovpn|ikev?[12]?\b|ipsec|mobile vpn|tunnel", low)
        out = _base("WatchGuard", "Firebox", "vpn" if vpn else "system", msg_id or None)
        if vpn:
            if re.search(r"fail|error|mismatch|timed? ?out", low):
                out["evt.outcome"], out["status"] = "failure", "warning"
            if re.search(r"\bdown\b|deleted|expired|removed", low):
                out["vpn.status"], out["status"] = "down", "warning"
            elif re.search(r"\bup\b|established", low):
                out["vpn.status"] = "up"
    if out is None:
        return None
    out.update(_raw("watchguard", kv))
    return out


# ── Check Point (Log Exporter : syslog, CEF, LEEF) ─────────────────────────────

_CP_PAIR = re.compile(r'(?:^|[\[;\s])([A-Za-z_][A-Za-z0-9_\-]{0,63}):"((?:[^"\\]|\\.)*)"')
_CP_THREAT_PRODUCTS = ("ips", "smartdefense", "anti-virus", "anti virus", "anti-bot", "anti-malware", "threat emulation",
                       "threat extraction", "threat prevention", "zero phishing", "anti-spam")
_CP_LEEF = re.compile(r"(?:LEEF:)?(?P<ver>[12]\.0)\|Check Point\|(?P<product>[^|]*)\|(?P<pver>[^|]*)\|(?P<event>[^|]*)\|"
                      r"(?P<rest>.*)$", re.S)


def _cp_unescape(v: str) -> str:
    return re.sub(r"\\(.)", r"\1", v)[:MAX_VALUE]


def _cp_fields(message: str) -> dict | None:
    if "Check Point" in message:
        c = _cef(message)
        if c and c[0][1] == "Check Point":
            head, ext = c
            out = dict(ext)
            out.setdefault("product", head[2])
            out.setdefault("act", head[5])
            out.setdefault("action", ext.get("act") or head[5])
            out.setdefault("severity", head[6])
            return out
        m = _CP_LEEF.search(message)
        if m:
            rest = m["rest"]
            if rest[:1] and not re.match(r"[A-Za-z_]", rest[:1]) and rest[1:2] == "|":
                delim, rest = rest[0], rest[2:]    # LEEF 2.0 : délimiteur déclaré
            else:
                delim = "\t"
            out = {}
            for part in rest.split(delim) if delim in rest else re.split(r"\s+(?=[A-Za-z_][\w\-]*=)", rest):
                k, sep, v = part.partition("=")
                if sep:
                    out.setdefault(k.strip(), v.strip()[:MAX_VALUE])
            out.setdefault("product", m["product"])
            out.setdefault("action", out.get("action") or m["event"])
            return out
    pairs = {k: _cp_unescape(v) for k, v in _CP_PAIR.findall(message)}
    return pairs if pairs and ("product" in pairs or "action" in pairs or "origin" in pairs) else None


@device_parser("checkpoint")
def parse_checkpoint(message: str) -> dict | None:
    kv = _cp_fields(message)
    if not kv:
        return None
    g = lambda *keys: next((kv[k] for k in keys if kv.get(k) not in (None, "")), None)  # noqa: E731
    product = str(g("product") or "")
    action = str(g("action", "act") or "")
    low_p, low_a = product.lower(), action.lower()
    if any(p in low_p for p in _CP_THREAT_PRODUCTS) or g("protection_name", "attack", "malware_action", "malware_family"):
        cat = "threat"
    elif g("operation", "administrator") or low_a in ("log in", "log out", "failed log in") or "smartconsole" in low_p:
        cat = "admin"
    elif g("vpn_feature_name", "community", "scheme") or low_a in ("encrypt", "decrypt", "key install") \
            or "mobile access" in low_p or "remote access" in low_p:
        cat = "vpn"
    elif g("src", "dst") or "firewall" in low_p:
        cat = "traffic"
    else:
        cat = "system"
    out = _base("Check Point", product or "Quantum", cat, action or None)
    _put(out, "device.name", g("origin_sic_name", "originsicname", "origin"))
    _put(out, "device.severity", g("severity"))
    _endpoints(out, g("src"), g("s_port", "spt"), g("dst"), g("service", "dpt"))
    _put(out, "network.protocol", _proto(g("proto")))
    direction = str(g("ifdir") or "").lower()
    _put(out, "network.interface.out" if direction == "outbound" else "network.interface.in", g("ifname"))
    _put(out, "fw.rule", g("rule_name", "rule", "rule_uid"))
    _put(out, "usr.name", g("administrator", "src_user_name", "user", "usrName", "suser"))
    _put(out, "fw.action", _action(action, allow=("inform", "detect", "bypass", "ask", "encrypt", "decrypt",
                                                  "key install", "redirect")))
    status = str(g("status", "auth_status", "outcome") or "").lower()
    if cat == "threat":
        _put(out, "threat.name", g("protection_name", "attack", "malware_family", "malware_action"))
        _put(out, "threat.type", g("protection_type", "attack_info") or product)
        out["status"] = _threat_status(g("severity"))
    elif cat == "admin":
        failed = "fail" in low_a or "fail" in status
        if low_a in ("log in", "failed log in") or "log in" in str(g("operation") or "").lower():
            _admin_login(out, not failed, out.get("usr.name"))
        elif failed:
            out["evt.outcome"] = "failure"
        _put(out, "network.source.ip", _ip(g("src", "client_ip")))
    elif cat == "vpn":
        _put(out, "vpn.tunnel", g("community"))
        _put(out, "vpn.peer", _ip(g("peer_gateway", "dst")))
        if "fail" in status or "fail" in low_a or g("encryption_failure"):
            out["evt.outcome"], out["status"] = "failure", "warning"
        tunnel = str(g("tunnel_state", "vpn_tunnel_state") or "").lower()
        if "down" in tunnel:
            out["vpn.status"], out["status"] = "down", "warning"
        elif "up" in tunnel:
            out["vpn.status"] = "up"
    out.update(_raw("checkpoint", {k: v for k, v in kv.items() if k not in ("default_device_message",)}))
    return out


# ── MikroTik RouterOS (sujets puis message) ────────────────────────────────────

# Message distant de RouterOS : « sujets message » (format « default ») ; l'affichage de /log print met
# l'heure devant (« sep/15 09:54:33 »), tolérée.
_MT_TOPICS = re.compile(r"^(?:[A-Za-z]{3}/\d{1,2}(?:/\d{4})? )?(?:\d{2}:\d{2}:\d{2} )?"
                        r"(?P<topics>[a-z0-9\-]+(?:,[a-z0-9\-]+)+)\s+(?P<rest>.*)$")
_MT_FW = re.compile(r"^(?:(?P<prefix>.*?)\s+)?(?P<chain>[\w\-]+): in:(?P<in>\S+?)(?: out:(?P<out>.*?))?, "
                    r"(?:connection-state:\S+ )?(?:src-mac (?P<mac>[0-9A-Fa-f:]+), )?proto (?P<proto>[A-Za-z0-9\-]+)"
                    r"(?: \([^)]*\))?, (?P<src>[0-9A-Fa-f.:\[\]]+?)(?::(?P<sp>\d+))?->(?P<dst>[0-9A-Fa-f.:\[\]]+?)"
                    r"(?::(?P<dp>\d+))?, .*?len (?P<len>\d+)")
_MT_LOGIN = re.compile(r"user (?P<user>\S+) logged (?P<inout>in|out) from (?P<ip>[0-9A-Fa-f.:]+) via (?P<via>\S+)")
_MT_FAIL = re.compile(r"login failure for user (?P<user>\S+) from (?P<ip>[0-9A-Fa-f.:]+) via (?P<via>\S+)")
_MT_CHANGE = re.compile(r"(?:added|changed|removed|moved|deleted) by (?P<user>\S+)")
_MT_VPN_TOPICS = {"ipsec", "l2tp", "pptp", "ovpn", "sstp", "wireguard"}


@device_parser("mikrotik")
def parse_mikrotik(message: str) -> dict | None:
    m = _MT_TOPICS.match(message.strip())
    if not m:
        return None
    topics = m["topics"].split(",")
    rest = m["rest"]
    raw = {"topics": m["topics"]}
    if "firewall" in topics and (x := _MT_FW.match(rest)):
        out = _base("MikroTik", "RouterOS", "traffic", "firewall")
        prefix = (x["prefix"] or "").strip()
        _put(out, "fw.rule", prefix or x["chain"])
        word = prefix.lower()
        # La ligne de RouterOS ne dit pas ce que la règle a fait : on le lit dans le préfixe (log-prefix).
        if re.search(r"drop|deny|block|reject|refus", word):
            out["fw.action"] = "deny"
        elif re.search(r"accept|allow|pass|autoris", word):
            out["fw.action"] = "allow"
        _endpoints(out, x["src"], x["sp"], x["dst"], x["dp"])
        _put(out, "network.protocol", x["proto"].lower())
        _put(out, "network.interface.in", x["in"])
        o = (x["out"] or "").strip()
        if o and not o.startswith("("):
            out["network.interface.out"] = o
        raw.update({"chain": x["chain"], "src_mac": x["mac"], "length": int(x["len"])})
    elif (x := _MT_FAIL.search(rest)):
        out = _admin_login(_base("MikroTik", "RouterOS", "admin"), False, x["user"])
        _put(out, "network.source.ip", _ip(x["ip"]))
        raw["via"] = x["via"]
    elif (x := _MT_LOGIN.search(rest)):
        out = _base("MikroTik", "RouterOS", "admin")
        if x["inout"] == "in":
            _admin_login(out, True, x["user"])
        else:
            out["evt.name"], out["usr.name"] = "admin_logout", x["user"]
        _put(out, "network.source.ip", _ip(x["ip"]))
        raw["via"] = x["via"]
    elif "system" in topics and (x := _MT_CHANGE.search(rest)):
        out = _base("MikroTik", "RouterOS", "admin", "config")
        out["usr.name"] = x["user"]
    elif _MT_VPN_TOPICS & set(topics):
        out = _base("MikroTik", "RouterOS", "vpn", next(t for t in topics if t in _MT_VPN_TOPICS))
        low = rest.lower()
        if re.search(r"fail|error|mismatch|no suitable proposal|timed? ?out", low):
            out["evt.outcome"], out["status"] = "failure", "warning"
        if re.search(r"disconnected|terminated|\bdown\b|deleted|purging", low):
            out["vpn.status"], out["status"] = "down", "warning"
        elif re.search(r"\bconnected\b|established|\bup\b", low):
            out["vpn.status"] = "up"
        mp = re.search(r"(?:peer|from|remote)[: ]+([0-9A-Fa-f.:]+[0-9A-Fa-f])", rest)
        _put(out, "vpn.peer", _ip(mp.group(1)) if mp else None)
    else:
        out = _base("MikroTik", "RouterOS", "system", topics[0])
    sev = next((t for t in topics if t in ("debug", "info", "warning", "error", "critical")), None)
    _put(out, "device.severity", sev)
    return out | _raw("mikrotik", {k: v for k, v in raw.items() if v is not None})


# ── Ubiquiti (UniFi, EdgeOS : journaux du noyau, CEF de UniFi Network) ─────────

_UBNT_RULE = re.compile(r"\[(?P<rule>[A-Za-z0-9_]+(?:-[A-Za-z0-9_]+)+)\]")
_UBNT_KV = re.compile(r"(?:^|(?<=\s)|(?<=\]))([A-Z]{2,8})=(\S*)")
_UBNT_DESCR = re.compile(r'DESCR="((?:[^"\\]|\\.)*)"')


@device_parser("ubiquiti")
def parse_ubiquiti(message: str) -> dict | None:
    if "Ubiquiti|" in message:
        c = _cef(message)
        if c is None or c[0][1] != "Ubiquiti":
            return None
        head, ext = c
        ucat, usub = ext.get("UNIFIcategory", ""), ext.get("UNIFIsubCategory", "")
        name = head[5]
        low = (ucat + " " + usub + " " + name).lower()
        if ucat.lower() == "security" or "threat" in low or "intrusion" in low:
            cat = "threat"
        elif "admin" in low:
            cat = "admin"
        elif "vpn" in low or "teleport" in low or "wireguard" in low:
            cat = "vpn"
        elif "firewall" in low:
            cat = "traffic"
        else:
            cat = "system"
        out = _base("Ubiquiti", head[2] or "UniFi", cat, name or head[4])
        _put(out, "device.name", ext.get("UNIFIdeviceName") or ext.get("UNIFIhost"))
        _put(out, "device.severity", head[6])
        _endpoints(out, ext.get("src"), ext.get("spt"), ext.get("dst"), ext.get("dpt"))
        _put(out, "network.protocol", _proto(ext.get("proto")))
        if re.search(r"block|drop|den(y|ied)|reject", name, re.I):
            out["fw.action"] = "deny"
        if cat == "threat":
            _put(out, "threat.name", ext.get("UNIFIipsSignature") or name)
            _put(out, "threat.type", usub or ucat)
            risk = (ext.get("UNIFIrisk") or "").lower()
            out["status"] = "error" if risk in ("high", "critical") or (head[6].isdigit() and int(head[6]) >= 8) \
                else "warning"
        elif cat == "admin":
            failed = re.search(r"fail|denied|invalid", name, re.I) is not None
            if re.search(r"log(ged)? ?in|access|sign", name, re.I) or failed:
                _admin_login(out, not failed, ext.get("UNIFIadmin") or ext.get("suser") or ext.get("duser"))
            _put(out, "network.source.ip", _ip(ext.get("src") or ext.get("UNIFIadminIp")))
        elif cat == "vpn":
            if re.search(r"disconnect|down|fail", name, re.I):
                out["vpn.status"], out["status"] = "down", "warning"
            elif re.search(r"connect|up", name, re.I):
                out["vpn.status"] = "up"
        raw = {"signature_id": head[4]} | {k: v for k, v in ext.items() if k not in ("src", "dst", "spt", "dpt")}
        return out | _raw("ubiquiti", raw)
    if "SRC=" not in message or "DST=" not in message:
        return None
    kv = dict((k, v) for k, v in _UBNT_KV.findall(message))
    rule = _UBNT_RULE.search(message)
    out = _base("Ubiquiti", "UniFi/EdgeOS", "traffic", "firewall")
    if rule:
        name = rule["rule"]
        out["fw.rule"] = name
        parts = name.split("-")
        if any(p in ("D", "DROP", "R", "REJECT") for p in parts[1:]):
            out["fw.action"] = "deny"
        elif any(p in ("A", "ACCEPT") for p in parts[1:]):
            out["fw.action"] = "allow"
    _endpoints(out, kv.get("SRC"), kv.get("SPT"), kv.get("DST"), kv.get("DPT"))
    _put(out, "network.protocol", _proto(kv.get("PROTO")))
    _put(out, "network.interface.in", kv.get("IN"))
    _put(out, "network.interface.out", kv.get("OUT"))
    raw = {"length": kv.get("LEN"), "ttl": kv.get("TTL"), "mac": kv.get("MAC")}
    d = _UBNT_DESCR.search(message)
    if d:
        raw["description"] = d.group(1)
    return out | _raw("ubiquiti", {k: (int(v) if _INT.match(str(v)) else v) for k, v in raw.items() if v})
