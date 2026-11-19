"""Gestes nommés d'une action à distance et gestes reconnus dans la partie « Correctif » d'une
explication (contrat § 28). Sans accès à la base.

Aucune commande libre ne sort d'ici : seulement un geste (`restart_service`…) et ses paramètres,
validés par des expressions fermées. L'agent revérifie tout contre sa propre conf.
"""
import re
import unicodedata

KINDS = ("restart_service", "start_service", "stop_service", "run_script", "update_agent")
SERVICE_KINDS = ("restart_service", "start_service", "stop_service")
VERB_KIND = {"restart": "restart_service", "start": "start_service", "stop": "stop_service"}
UNIT_RE = re.compile(r"^[A-Za-z0-9@._:\\-]{1,200}$")
WIN_SERVICE_RE = re.compile(r"^[A-Za-z0-9_.\-$ ]{1,200}$")
SCRIPT_RE = re.compile(r"^[A-Za-z0-9_.\-]{1,100}$")
VERSION_RE = re.compile(r"^\d{1,4}\.\d{1,4}\.\d{1,6}(-[0-9A-Za-z.\-]{1,40})?$")

FIX_TITLES = {"correctif", "correctifs", "fix", "fixes", "correction", "corrections", "remediation",
              "solution", "solucion", "korrektur", "behebung", "correzione"}
_HEADING_RE = re.compile(r"^\s{0,3}#{1,6}\s+(.+?)\s*#*\s*$", re.M)
_SYSTEMCTL_RE = re.compile(r"(?:^|[\s`;|&(>$])(?:sudo\s+)?systemctl\s+(?:--?[\w-]+\s+)*(restart|start|stop)\s+([^\n;|&`)]+)")
_SERVICE_RE = re.compile(r"(?:^|[\s`;|&(>$])(?:sudo\s+)?service\s+([A-Za-z0-9@._\-]+)\s+(restart|start|stop)\b")
_PS_RE = re.compile(r"\b(Restart|Start|Stop)-Service\s+(?:-Name\s+)?(?:\"([^\"]+)\"|'([^']+)'|([A-Za-z0-9_.\-$]+))",
                    re.I)

LABELS = {
    "fr": {"restart_service": "Redémarrer {name} sur {host}", "start_service": "Démarrer {name} sur {host}",
           "stop_service": "Arrêter {name} sur {host}", "reason": "Correctif proposé par l'IA : {command}"},
    "en": {"restart_service": "Restart {name} on {host}", "start_service": "Start {name} on {host}",
           "stop_service": "Stop {name} on {host}", "reason": "Fix suggested by the AI: {command}"},
}


def _fold(text: str) -> str:
    text = unicodedata.normalize("NFKD", text.lower())
    return "".join(c for c in text if not unicodedata.combining(c)).strip(" :*_`")


def fix_section(markdown: str) -> str:
    """Texte de la partie « Correctif » (« Fix »…) ; à défaut, la 3e partie d'une réponse en quatre."""
    heads = list(_HEADING_RE.finditer(markdown or ""))
    parts = []
    for i, h in enumerate(heads):
        end = heads[i + 1].start() if i + 1 < len(heads) else len(markdown)
        parts.append((_fold(h.group(1)), markdown[h.end():end]))
    for title, body in parts:
        if title in FIX_TITLES or title.split(" ")[0] in FIX_TITLES:
            return body
    return parts[2][1] if len(parts) >= 4 else ""


UNIT_SUFFIXES = (".service", ".socket", ".timer", ".target", ".mount", ".automount", ".path", ".slice", ".scope",
                 ".swap", ".device")


def unit_name(name: str) -> str | None:
    """Nom d'unité systemd ; sans suffixe connu, `.service` (`php-fpm8.3` → `php-fpm8.3.service`)."""
    name = name.strip().strip("'\"")
    if not UNIT_RE.match(name) or name.startswith("-"):
        return None
    return name if name.endswith(UNIT_SUFFIXES) else name + ".service"


def find_gestures(text: str) -> list[dict]:
    """Gestes de service reconnus : [{kind, os, params, command}], dans l'ordre, sans doublon."""
    found: list[tuple[int, dict]] = []
    for m in _SYSTEMCTL_RE.finditer(text or ""):
        for token in m.group(2).split():
            if token.startswith("-"):
                continue
            unit = unit_name(token)
            if unit is None:
                break
            found.append((m.start(), {"kind": VERB_KIND[m.group(1)], "os": "linux", "params": {"unit": unit},
                                      "command": " ".join(m.group(0).split())}))
    for m in _SERVICE_RE.finditer(text or ""):
        unit = unit_name(m.group(1))
        if unit:
            found.append((m.start(), {"kind": VERB_KIND[m.group(2)], "os": "linux", "params": {"unit": unit},
                                      "command": " ".join(m.group(0).split())}))
    for m in _PS_RE.finditer(text or ""):
        name = (m.group(2) or m.group(3) or m.group(4) or "").strip()
        if WIN_SERVICE_RE.match(name):
            found.append((m.start(), {"kind": VERB_KIND[m.group(1).lower()], "os": "windows",
                                      "params": {"service": name}, "command": " ".join(m.group(0).split())}))
    out, seen = [], set()
    for _, g in sorted(found, key=lambda x: x[0]):
        key = (g["kind"], tuple(sorted(g["params"].items())))
        if key not in seen:
            seen.add(key)
            out.append(g)
    return out


def label(kind: str, params: dict, hostname: str, lang: str) -> str:
    lab = LABELS.get(lang, LABELS["en"])
    name = params.get("unit") or params.get("service") or params.get("script") or params.get("version") or ""
    return lab.get(kind, kind).format(name=name, host=hostname)


def reason(command: str, lang: str) -> str:
    return LABELS.get(lang, LABELS["en"])["reason"].format(command=command)[:500]


def validate_params(kind: str, params, os_name: str | None) -> dict:
    """Paramètres d'un geste, vérifiés ; ValueError sinon. Linux : `unit`, Windows : `service`."""
    if kind not in KINDS:
        raise ValueError(f"kind attendu : {'|'.join(KINDS)}")
    if not isinstance(params, dict):
        raise ValueError("params doit être un objet")
    windows = (os_name or "").lower() == "windows"
    if kind in SERVICE_KINDS:
        if set(params) - {"unit", "service"} or len(params) != 1:
            raise ValueError("params : {\"unit\": …} (Linux) ou {\"service\": …} (Windows)")
        value = params.get("unit") or params.get("service")
        if not isinstance(value, str):
            raise ValueError("nom de service attendu")
        if windows:
            if not WIN_SERVICE_RE.match(value.strip()):
                raise ValueError("nom de service Windows invalide")
            return {"service": value.strip()}
        unit = unit_name(value)
        if unit is None:
            raise ValueError("nom d'unité systemd invalide")
        return {"unit": unit}
    if kind == "run_script":
        if set(params) != {"script"} or not isinstance(params["script"], str) or not SCRIPT_RE.match(params["script"]):
            raise ValueError("params : {\"script\": nom déclaré dans la conf de l'agent}")
        return {"script": params["script"]}
    if set(params) != {"version"} or not isinstance(params["version"], str) or not VERSION_RE.match(params["version"]):
        raise ValueError("params : {\"version\": \"0.4.0\"}")
    return {"version": params["version"]}


def capable(capabilities: dict | None, kind: str, params: dict) -> tuple[bool, str]:
    """L'agent annonce-t-il ce geste (`agents.capabilities.actions`) ?"""
    act = (capabilities or {}).get("actions") or {}
    if not act.get("enabled"):
        return False, "les actions à distance sont coupées dans la conf de l'agent (actions.enabled)"
    if kind not in (act.get("kinds") or []):
        return False, f"l'agent n'autorise pas le geste {kind} (actions.allow)"
    if kind == "run_script" and params.get("script") not in (act.get("scripts") or []):
        return False, f"script inconnu de l'agent : {params.get('script')}"
    return True, ""
