"""Canal GLPI (vague 6) : une alerte ouvre un ticket par l'API REST de GLPI, la reprise le résout.

API REST historique de GLPI (`apirest.php`, éprouvée sur GLPI 12.0.2) : session ouverte par le jeton de
l'application (en-tête App-Token) et le jeton d'API d'un utilisateur (`Authorization: user_token`),
fermée après chaque envoi. Un ticket porte dans son titre une référence stable tirée du moniteur et
du groupe (`[HYPHA-xxxxxxxxxx]`) : c'est elle qui retrouve le ticket encore ouvert (recherche sur le
titre, statut « non résolu ») :
- alerte (ALERT, WARN, NODATA) : ticket ouvert s'il n'y en a pas, sinon suivi ajouté au ticket ouvert
  (relance, escalade, aggravation) ;
- reprise (OK) : suivi « rétabli » puis solution sur chaque ticket ouvert de la référence (le ticket
  passe « résolu ») ; aucun ticket ouvert : rien à faire ;
- essai : un ticket ouvert puis résolu aussitôt.
"""
import hashlib
import json
from urllib.parse import urlencode

from ..synthetics.netguard import check_url

SOLVED = 5                       # statut « résolu » d'un ticket GLPI
URGENCY = {1: 5, 2: 4, 3: 3, 4: 2, 5: 1}     # priorité HYPHA → urgence GLPI (5 = très haute)


class GLPIError(RuntimeError):
    pass


def ref_of(key: str) -> str:
    """Référence stable d'un couple moniteur et groupe, cherchée dans le titre des tickets."""
    return "HYPHA-" + hashlib.sha1(key.encode()).hexdigest()[:10]


def base_url(cfg: dict) -> str:
    """Adresse de l'API : l'url donnée, complétée de /apirest.php si elle désigne le GLPI lui-même."""
    url = (cfg.get("url") or "").rstrip("/")
    if not url.endswith("apirest.php") and "/apirest.php/" not in url + "/":
        url += "/apirest.php"
    return url


def urgency_of(n) -> int:
    prio = n.monitor.get("priority")
    if prio in URGENCY:
        return URGENCY[prio]
    return 4 if n.state == "ALERT" else 3


def _error(r) -> str:
    """Message d'erreur de GLPI : ["ERROR_…", "texte"] ou texte brut, sans jamais renvoyer un jeton."""
    try:
        body = r.json()
        if isinstance(body, list) and body:
            return " : ".join(str(x) for x in body[:2])[:300]
        if isinstance(body, dict):
            return json.dumps(body, ensure_ascii=False)[:300]
    except ValueError:
        pass
    return f"réponse {r.status_code}"


class Session:
    def __init__(self, client, cfg: dict, allow_private: bool):
        self.client, self.cfg, self.allow_private = client, cfg, allow_private
        self.base = base_url(cfg)
        self.token = None

    def _headers(self) -> dict:
        h = {"App-Token": self.cfg.get("app_token", ""), "Content-Type": "application/json"}
        if self.token:
            h["Session-Token"] = self.token
        return h

    async def open(self):
        await check_url(self.base, self.allow_private)
        r = await self.client.get(f"{self.base}/initSession",
                                  headers={**self._headers(), "Authorization": f"user_token {self.cfg.get('user_token', '')}"})
        if r.status_code != 200:
            raise GLPIError(f"ouverture de session GLPI refusée ({r.status_code}) : {_error(r)}")
        self.token = (r.json() or {}).get("session_token")
        if not self.token:
            raise GLPIError("ouverture de session GLPI : session_token absent")

    async def close(self):
        if self.token:
            try:
                await self.client.get(f"{self.base}/killSession", headers=self._headers())
            except Exception:  # noqa: BLE001 — la session expire d'elle-même
                pass
            self.token = None

    async def post(self, path: str, data: dict) -> dict:
        r = await self.client.post(f"{self.base}/{path}", headers=self._headers(), json={"input": data})
        if r.status_code not in (200, 201):
            raise GLPIError(f"GLPI {path} : {_error(r)}")
        out = r.json()
        return out if isinstance(out, dict) else (out[0] if isinstance(out, list) and out and isinstance(out[0], dict) else {})

    async def put(self, path: str, data: dict) -> None:
        r = await self.client.put(f"{self.base}/{path}", headers=self._headers(), json={"input": data})
        if r.status_code not in (200, 201, 207):
            raise GLPIError(f"GLPI {path} : {_error(r)}")

    async def open_tickets(self, ref: str) -> list[int]:
        """Tickets non résolus dont le titre porte la référence (moteur de recherche de GLPI : champ 1 = titre,
        12 = statut, valeur « notold » = ni résolu ni clos ; 2 = identifiant)."""
        q = urlencode([("criteria[0][field]", "1"), ("criteria[0][searchtype]", "contains"), ("criteria[0][value]", ref),
                       ("criteria[1][link]", "AND"), ("criteria[1][field]", "12"), ("criteria[1][searchtype]", "equals"),
                       ("criteria[1][value]", "notold"), ("forcedisplay[0]", "2"), ("range", "0-49")])
        r = await self.client.get(f"{self.base}/search/Ticket?{q}", headers=self._headers())
        if r.status_code not in (200, 206):
            raise GLPIError(f"recherche des tickets GLPI : {_error(r)}")
        rows = (r.json() or {}).get("data") or []
        ids = []
        for row in rows:
            v = row.get("2") if isinstance(row, dict) else None
            if v is not None and str(v).isdigit():
                ids.append(int(v))
        return sorted(set(ids))

    async def followup(self, ticket: int, content: str) -> None:
        await self.post(f"Ticket/{ticket}/ITILFollowup", {"itemtype": "Ticket", "items_id": ticket, "content": content})

    async def solve(self, ticket: int, content: str) -> None:
        """Solution (GLPI 9.3+) : le ticket passe « résolu » ; à défaut, statut posé directement."""
        try:
            await self.post(f"Ticket/{ticket}/ITILSolution", {"itemtype": "Ticket", "items_id": ticket, "content": content})
        except GLPIError:
            await self.put(f"Ticket/{ticket}", {"status": SOLVED})


def _html(text: str) -> str:
    import html
    return "<p>" + html.escape(text).replace("\n", "<br>") + "</p>"


def ticket_input(n, ref: str, body: str) -> dict:
    cfg = n.channel.get("config") or {}
    data = {"name": f"{n.subject} [{ref}]"[:250], "content": _html(body), "type": int(cfg.get("ticket_type") or 1),
            "urgency": urgency_of(n), "impact": 3}
    for key in ("entities_id", "itilcategories_id", "requesttypes_id"):
        if cfg.get(key) not in (None, ""):
            data[key] = int(cfg[key])
    return data


async def send(client, n, text_body) -> str:
    """Un envoi vers GLPI ; rend le détail (tickets ouverts, suivis, résolus)."""
    cfg = n.channel.get("config") or {}
    ref = ref_of(n.key)
    body = text_body(n)
    s = Session(client, cfg, n.allow_private)
    await s.open()
    try:
        if n.kind == "test":
            created = await s.post("Ticket", ticket_input(n, ref, body))
            tid = int(created.get("id") or 0)
            if not tid:
                raise GLPIError("création du ticket : identifiant absent de la réponse")
            await s.followup(tid, _html("Essai de canal : ce ticket est résolu aussitôt ouvert."))
            await s.solve(tid, _html("Essai de canal HYPHA terminé."))
            return f"ticket GLPI n° {tid} ouvert puis résolu"
        tickets = await s.open_tickets(ref)
        if n.state == "OK":
            for tid in tickets:
                await s.followup(tid, _html(body))
                await s.solve(tid, _html(f"Rétabli : {n.message}"[:2000]))
            return f"ticket(s) GLPI résolu(s) : {', '.join(map(str, tickets))}" if tickets else "aucun ticket GLPI ouvert pour ce moniteur"
        if tickets:
            for tid in tickets:
                await s.followup(tid, _html(f"[{n.state}] {body}"))
            return f"suivi ajouté au ticket GLPI n° {', '.join(map(str, tickets))}"
        created = await s.post("Ticket", ticket_input(n, ref, body))
        tid = created.get("id")
        return f"ticket GLPI n° {tid} ouvert"
    finally:
        await s.close()
