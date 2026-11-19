"""Validation d'un test synthétique (contrat § 7, « Synthétiques » ; § 29 multi-étapes, assertions,
emplacements)."""
import ipaddress
import re
from urllib.parse import urlsplit

from . import assertions as A
from .probes import split_host_port

KINDS = ("http", "tcp", "ssl", "dns", "icmp", "multistep", "smtp", "imap", "mail_roundtrip", "mail_dns")
# Types que seul le serveur exécute (l'agent ne les connaît pas, et le mot de passe d'une boîte ne
# part pas vers les agents).
SERVER_ONLY = ("mail_roundtrip", "mail_dns")
TLS_MODES = ("auto", "starttls", "implicit", "off")
STARTTLS_PROTOS = ("smtp", "imap", "pop3")
EMAIL_RE = re.compile(r"^[^@\s<>]{1,64}@[A-Za-z0-9.-]{1,253}$")
SELECTOR_RE = re.compile(r"^[A-Za-z0-9._-]{1,63}$")
MASK = "••••••••"
LOCATION_RE = re.compile(r"^(?:server|agent:\d{1,18})$")
MAX_LOCATIONS = 20
METHODS = ("GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS")
DNS_TYPES = ("A", "AAAA", "CNAME", "MX", "TXT", "NS", "SOA", "CAA", "SRV", "PTR")


def _num(cfg: dict, key: str, lo: float, hi: float, default):
    v = cfg.get(key, default)
    if v is None:
        return None
    if isinstance(v, bool) or not isinstance(v, (int, float)) or not lo <= v <= hi:
        raise ValueError(f"config.{key} doit être un nombre entre {lo:g} et {hi:g}")
    return v


def _host(host: str) -> str:
    host = (host or "").strip()
    if not host or len(host) > 253 or any(c.isspace() for c in host):
        raise ValueError("nom d'hôte invalide")
    return host


def literal_ip(target_host: str) -> str | None:
    try:
        return str(ipaddress.ip_address(target_host.strip("[]")))
    except ValueError:
        return None


def normalize_config(kind: str, target: str, cfg) -> tuple[str, dict]:
    if cfg is None:
        cfg = {}
    if not isinstance(cfg, dict):
        raise ValueError("config doit être un objet")
    if not isinstance(target, str) or not target.strip() or len(target) > 2000:
        raise ValueError("target est obligatoire")
    target = target.strip()
    out: dict = {"timeout_s": _num(cfg, "timeout_s", 1, 60, 10)}
    if kind == "multistep":
        variables = cfg.get("variables") or {}
        if not isinstance(variables, dict) or len(variables) > 50 or not all(
                isinstance(k, str) and A._NAME_RE.match(k) and isinstance(v, (str, int, float))
                and not isinstance(v, bool) and len(str(v)) <= 2000 for k, v in variables.items()):
            raise ValueError("config.variables doit être un objet {nom: valeur texte} (50 au plus)")
        out["variables"] = {k: str(v) for k, v in variables.items()}
        return target, out
    if kind == "http":
        parts = urlsplit(target)
        if parts.scheme not in ("http", "https") or not parts.hostname:
            raise ValueError("target doit être une URL http:// ou https://")
        method = str(cfg.get("method", "GET")).upper()
        if method not in METHODS:
            raise ValueError(f"config.method attendue : {'|'.join(METHODS)}")
        headers = cfg.get("headers", {}) or {}
        if not isinstance(headers, dict) or len(headers) > 50 or not all(
                isinstance(k, str) and isinstance(v, str) for k, v in headers.items()):
            raise ValueError("config.headers doit être un objet {nom: valeur texte}")
        body = cfg.get("body", "") or ""
        if not isinstance(body, str) or len(body) > 100_000:
            raise ValueError("config.body doit être une chaîne (100 000 caractères au plus)")
        expect = cfg.get("expect_status", 200)
        if isinstance(expect, list):
            if not expect or not all(isinstance(c, int) and 100 <= c <= 599 for c in expect):
                raise ValueError("config.expect_status : liste de statuts HTTP")
        elif expect is not None and (not isinstance(expect, int) or isinstance(expect, bool) or not 100 <= expect <= 599):
            raise ValueError("config.expect_status doit être un statut HTTP")
        text = cfg.get("expect_text", "") or ""
        if not isinstance(text, str) or len(text) > 1000:
            raise ValueError("config.expect_text doit être une chaîne (1000 caractères au plus)")
        out.update({"method": method, "headers": headers, "body": body, "expect_status": expect,
                    "expect_text": text, "follow_redirects": bool(cfg.get("follow_redirects", True))})
        if cfg.get("ssl_days_warn") is not None:
            out["ssl_days_warn"] = _num(cfg, "ssl_days_warn", 0, 365, None)
        if cfg.get("assertions"):
            out["assertions"] = A.validate_assertions(cfg["assertions"], "config.assertions")
        return target, out
    if kind in ("tcp", "ssl"):
        try:
            host, port = split_host_port(target, 443 if kind == "ssl" else None)
        except ValueError as e:
            raise ValueError(f"target : {e}")
        if port is None:
            raise ValueError("target doit être hôte:port")
        _host(host)
        if kind == "ssl":
            out["ssl_days_warn"] = _num(cfg, "ssl_days_warn", 0, 365, 21)
            starttls = cfg.get("starttls") or None
            if starttls is not None and starttls not in STARTTLS_PROTOS:
                raise ValueError(f"config.starttls attendu : {'|'.join(STARTTLS_PROTOS)} ou vide")
            if starttls:
                out["starttls"] = starttls
            if cfg.get("assertions"):
                out["assertions"] = A.validate_assertions(cfg["assertions"], "config.assertions")
        return target, out
    if kind in ("smtp", "imap"):
        try:
            host, port = split_host_port(target, 25 if kind == "smtp" else 993)
        except ValueError as e:
            raise ValueError(f"target : {e}")
        _host(host)
        tls = cfg.get("tls") or "auto"
        if tls not in TLS_MODES:
            raise ValueError(f"config.tls attendu : {'|'.join(TLS_MODES)}")
        out.update({"tls": tls, "ssl_days_warn": _num(cfg, "ssl_days_warn", 0, 365, 21)})
        for key in ("tls_server_name", "ehlo_name"):
            if cfg.get(key):
                out[key] = _host(str(cfg[key]))
        banner = cfg.get("expect_banner") or ""
        if not isinstance(banner, str) or len(banner) > 200:
            raise ValueError("config.expect_banner doit être une chaîne (200 caractères au plus)")
        if banner:
            out["expect_banner"] = banner
        if cfg.get("assertions"):
            out["assertions"] = A.validate_assertions(cfg["assertions"], "config.assertions")
        return target, out
    if kind == "mail_roundtrip":
        if not EMAIL_RE.match(target):
            raise ValueError("target doit être l'adresse de la boîte relevée")
        out["timeout_s"] = _num(cfg, "timeout_s", 1, 60, 20)
        for key, port in (("smtp", 587), ("imap", 993)):
            value = cfg.get(key)
            if not isinstance(value, str) or not value.strip():
                raise ValueError(f"config.{key} est obligatoire (hôte:port)")
            try:
                host, _ = split_host_port(value, port)
            except ValueError as e:
                raise ValueError(f"config.{key} : {e}")
            _host(host)
            out[key] = value.strip()
            mode = cfg.get(f"{key}_tls") or "auto"
            if mode not in ("auto", "starttls", "implicit"):
                raise ValueError(f"config.{key}_tls attendu : auto|starttls|implicit (jamais en clair)")
            out[f"{key}_tls"] = mode
        password = cfg.get("password")
        if not isinstance(password, str) or not password or password == MASK or len(password) > 500:
            raise ValueError("config.password est obligatoire (mot de passe de la boîte)")
        out["password"] = password
        for key in ("username", "from", "to", "reply_from"):
            value = cfg.get(key) or ""
            if not isinstance(value, str) or len(value) > 320:
                raise ValueError(f"config.{key} doit être une chaîne")
            if value and key in ("from", "to") and not EMAIL_RE.match(value.strip()):
                raise ValueError(f"config.{key} doit être une adresse")
            if value:
                out[key] = value.strip()
        out["wait_s"] = _num(cfg, "wait_s", 10, 900, 180)
        out["expect_auth_pass"] = bool(cfg.get("expect_auth_pass", True))
        if cfg.get("ehlo_name"):
            out["ehlo_name"] = _host(str(cfg["ehlo_name"]))
        return target, out
    if kind == "mail_dns":
        _host(target)
        selectors = cfg.get("dkim_selectors") or []
        if not isinstance(selectors, list) or len(selectors) > 10 or not all(
                isinstance(s, str) and SELECTOR_RE.match(s) for s in selectors):
            raise ValueError("config.dkim_selectors : liste de 10 sélecteurs au plus")
        zones = cfg.get("dnsbl_zones") or []
        if not isinstance(zones, list) or len(zones) > 10 or not all(isinstance(z, str) for z in zones):
            raise ValueError("config.dnsbl_zones : liste de 10 zones au plus")
        ips = cfg.get("dnsbl_ips") or []
        if not isinstance(ips, list) or len(ips) > 50 or not all(
                isinstance(i, str) and literal_ip(i) and ":" not in i for i in ips):
            raise ValueError("config.dnsbl_ips : liste de 50 adresses IPv4 au plus")
        for key, values in (("dmarc_policy", ("none", "quarantine", "reject")), ("spf_all", ("-all", "~all"))):
            if cfg.get(key) not in (None, "") and cfg[key] not in values:
                raise ValueError(f"config.{key} attendu : {'|'.join(values)} ou vide")
            if cfg.get(key):
                out[key] = cfg[key]
        out.update({"dkim_selectors": selectors, "dnsbl": bool(cfg.get("dnsbl", False)),
                    "dnsbl_zones": [_host(z) for z in zones], "dnsbl_ips": ips})
        return target.strip().rstrip("."), out
    if kind == "dns":
        _host(target)
        rtype = str(cfg.get("record_type", "A")).upper()
        if rtype not in DNS_TYPES:
            raise ValueError(f"config.record_type attendu : {'|'.join(DNS_TYPES)}")
        ns = cfg.get("nameserver")
        if ns is not None and literal_ip(str(ns)) is None:
            raise ValueError("config.nameserver doit être une adresse IP")
        expect = cfg.get("expect", "") or ""
        if not isinstance(expect, str) or len(expect) > 500:
            raise ValueError("config.expect doit être une chaîne")
        out.update({"record_type": rtype, "nameserver": ns, "expect": expect})
        return target, out
    if kind == "icmp":
        host, _ = split_host_port(target)
        _host(host)
        out["timeout_s"] = _num(cfg, "timeout_s", 1, 30, 5)
        out["count"] = int(_num(cfg, "count", 1, 10, 3))
        return target, out
    raise ValueError(f"kind attendu : {'|'.join(KINDS)}")


def _headers(headers, where: str) -> dict:
    headers = headers or {}
    if not isinstance(headers, dict) or len(headers) > 50 or not all(
            isinstance(k, str) and isinstance(v, str) for k, v in headers.items()):
        raise ValueError(f"{where} doit être un objet {{nom: valeur texte}}")
    return headers


def normalize_steps(steps) -> list[dict]:
    """Étapes d'un test multistep : URL (gabarits {{variable}} permis), méthode, en-têtes, corps,
    délai, assertions et extractions."""
    from .multistep import MAX_STEPS
    if not isinstance(steps, list) or not steps or len(steps) > MAX_STEPS:
        raise ValueError(f"steps doit être une liste de 1 à {MAX_STEPS} étapes")
    out = []
    for i, s in enumerate(steps):
        label = f"steps[{i}]"
        if not isinstance(s, dict):
            raise ValueError(f"{label} doit être un objet")
        name = s.get("name") or f"étape {i + 1}"
        if not isinstance(name, str) or len(name) > 200:
            raise ValueError(f"{label}.name : texte de 200 caractères au plus")
        url = s.get("url")
        if not isinstance(url, str) or not url.strip() or len(url) > 2000:
            raise ValueError(f"{label}.url est obligatoire")
        url = url.strip()
        if not url.startswith("{{"):        # « {{base}}/connexion » : la variable porte schéma et hôte
            parts = urlsplit(url)
            if parts.scheme not in ("http", "https") or not parts.hostname:
                raise ValueError(f"{label}.url doit être une URL http:// ou https://")
        method = str(s.get("method", "GET")).upper()
        if method not in METHODS:
            raise ValueError(f"{label}.method attendue : {'|'.join(METHODS)}")
        body = s.get("body", "") or ""
        if not isinstance(body, str) or len(body) > 100_000:
            raise ValueError(f"{label}.body doit être une chaîne (100 000 caractères au plus)")
        timeout = s.get("timeout_s", 10)
        if isinstance(timeout, bool) or not isinstance(timeout, (int, float)) or not 1 <= timeout <= 60:
            raise ValueError(f"{label}.timeout_s doit être un nombre entre 1 et 60")
        out.append({"name": name, "method": method, "url": url, "headers": _headers(s.get("headers"), f"{label}.headers"),
                    "body": body, "timeout_s": timeout,
                    "assertions": A.validate_assertions(s.get("assertions"), f"{label}.assertions"),
                    "extract": A.validate_extracts(s.get("extract"), f"{label}.extract"),
                    "follow_redirects": bool(s.get("follow_redirects", True))})
    return out


def normalize_locations(locations) -> list[str]:
    if locations is None:
        return ["server"]
    if not isinstance(locations, list) or not locations or len(locations) > MAX_LOCATIONS:
        raise ValueError(f"locations : liste de 1 à {MAX_LOCATIONS} emplacements (server, agent:<host_id>)")
    out = []
    for loc in locations:
        if not isinstance(loc, str) or not LOCATION_RE.match(loc.strip()):
            raise ValueError(f"emplacement invalide : {loc!r} (server ou agent:<host_id>)")
        if loc.strip() not in out:
            out.append(loc.strip())
    return out


def location_host_ids(locations: list[str]) -> list[int]:
    return sorted({int(loc.split(":", 1)[1]) for loc in locations if loc.startswith("agent:")})


def step_hosts(steps: list[dict]) -> list[str]:
    """Hôtes des URL d'étapes connus à l'enregistrement (sans variable dans la partie hôte)."""
    out = []
    for s in steps:
        parts = urlsplit(s["url"])
        if parts.hostname and "{{" not in (parts.netloc or ""):
            out.append(parts.hostname)
    return out


def target_host(kind: str, target: str) -> str | None:
    if kind == "multistep":
        return None
    if kind == "http":
        return urlsplit(target).hostname
    if kind == "dns":
        return target
    try:
        return split_host_port(target)[0]
    except ValueError:
        return None


def normalize_test(data: dict, icmp_ok: bool) -> dict:
    name = data.get("name")
    if not isinstance(name, str) or not name.strip() or len(name) > 200:
        raise ValueError("name est obligatoire (200 caractères au plus)")
    kind = data.get("kind")
    if kind not in KINDS:
        raise ValueError(f"kind attendu : {'|'.join(KINDS)}")
    if kind == "icmp" and not icmp_ok:
        raise ValueError("icmp indisponible sur ce serveur : le noyau refuse l'écho ICMP sans privilège "
                         "(net.ipv4.ping_group_range) ; utilisez un test tcp")
    steps: list = []
    target = data.get("target")
    if kind == "multistep":
        steps = normalize_steps(data.get("steps"))
        # La cible affichée d'un multi-étapes est l'URL de sa première étape.
        if not isinstance(target, str) or not target.strip():
            target = steps[0]["url"]
    target, config = normalize_config(kind, target, data.get("config"))
    interval = data.get("interval_s", 60)
    if not isinstance(interval, int) or isinstance(interval, bool) or not 30 <= interval <= 86400:
        raise ValueError("interval_s doit être un entier entre 30 et 86400")
    if kind == "mail_roundtrip" and interval < 300:
        raise ValueError("interval_s : 300 s au moins pour un aller-retour de messagerie")
    if kind == "mail_dns" and interval < 300:
        raise ValueError("interval_s : 300 s au moins pour un test DNS de messagerie")
    locations = normalize_locations(data.get("locations"))
    if kind in SERVER_ONLY and locations != ["server"]:
        raise ValueError(f"un test {kind} ne tourne que sur le serveur (locations: [\"server\"])")
    return {"name": name.strip(), "kind": kind, "target": target, "config": config, "interval_s": interval,
            "enabled": bool(data.get("enabled", True)), "steps": steps, "locations": locations}


def public_config(kind: str, config: dict | None) -> dict | None:
    """Config rendue par l'API : le mot de passe d'une boîte n'en sort jamais."""
    if not isinstance(config, dict) or "password" not in config:
        return config
    return {**config, "password": MASK}


def keep_secret(new_config, current_config) -> dict | None:
    """À la modification, un mot de passe absent ou masqué garde la valeur enregistrée."""
    if not isinstance(new_config, dict):
        return new_config
    if new_config.get("password") in (None, "", MASK) and isinstance(current_config, dict) \
            and current_config.get("password"):
        return {**new_config, "password": current_config["password"]}
    return new_config
