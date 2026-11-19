"""Service d'un moniteur et lien runbook du catalogue (contrat § 26).

Le service se lit, dans l'ordre : étiquette `service` du moniteur, clé `service:` du groupe,
champ `service` de la requête (APM, suivi des erreurs), terme `service:x` d'une requête de
métrique, de journaux ou d'événements. Le runbook vient de `services.runbook_url`.
"""
import re

from ..db import fetch_all
from ..util import parse_group_key

SERVICE_RE = re.compile(r"(?<![!\w\-])service:([A-Za-z0-9_.\-/]+)")


def monitor_service(m: dict, group: str | None = None) -> str | None:
    tags = m.get("tags") or {}
    if isinstance(tags, dict) and tags.get("service"):
        return str(tags["service"])
    if group:
        from_group = parse_group_key(group).get("service")
        if from_group and from_group != "N/A":
            return from_group
    q = m.get("query") or {}
    if isinstance(q, dict):
        if isinstance(q.get("service"), str) and q["service"]:
            return q["service"]
        found = SERVICE_RE.search(str(q.get("q") or ""))
        if found:
            return found.group(1)
    return None


async def runbooks(conn, org_id: int) -> dict[str, str]:
    rows = await fetch_all(conn, "SELECT name, runbook_url FROM hypha.services "
                                 "WHERE org_id = %s AND runbook_url <> ''", (org_id,))
    return {r["name"]: r["runbook_url"] for r in rows}


def with_runbook(text: str, runbook: str | None) -> str:
    """Ajoute le lien runbook au message s'il n'y figure pas déjà."""
    if not runbook or runbook in text:
        return text
    return f"{text}\n\nRunbook : {runbook}"
