"""Vague 6 des intégrations (Jenkins, GitHub, Zot, Odoo, WSUS, canal GLPI) : chaque modèle de moniteur se
construit, passe la validation d'un moniteur ordinaire et vise un contrôle ou une métrique que l'agent
publie vraiment ; chaque intégration a son tableau prêt ; le canal GLPI se valide, masque ses jetons, et
parle à GLPI comme le vrai GLPI 12.0.2 l'a accepté (session, ticket, suivi, solution)."""
import asyncio
import json
import re

import httpx
import pytest

from hypha.api import dashboard_catalogue as CAT
from hypha.api.settings_routes import SECRET_FIELDS, _redact, validate_channel
from hypha.auth.principal import Principal
from hypha.monitors import glpi as G
from hypha.monitors import notify as N
from hypha.monitors import templates
from hypha.monitors.templates_vague6 import vague6
from hypha.monitors.validate import normalize_monitor

from test_dashboard_catalogue import AGENT, _agent_names

V6 = [t["id"] for t in vague6(templates._v, templates._metric, templates._check, templates.two, templates.SCOPE)]


def test_templates_build_and_validate():
    assert len(V6) == len(set(V6)) >= 25
    ids = [t["id"] for t in templates.TEMPLATES]
    assert len(ids) == len(set(ids)), "identifiant de modèle en double"
    for tid in V6:
        m = normalize_monitor(templates.build(tid, {}))
        assert m["name"] and m["type"] in ("metric", "service_check"), tid
    assert templates.build("odoo_slow", {"scope": "env:prod"})["query"]["q"] == \
        "avg:odoo.response_time{env:prod,endpoint:health} by {host,instance}"
    assert templates.build("zot_down", {})["query"]["tags"] == {"instance_name": "zot"}
    assert templates.build("wsus_content_free", {"critical": 5, "warning": 8})["thresholds"] == \
        {"critical": 5 * 1024 ** 3, "warning": 8 * 1024 ** 3}


@pytest.mark.skipif(not AGENT.is_dir(), reason="arbre de l'agent absent")
def test_templates_target_real_names():
    known = _agent_names()
    for tid in V6:
        d = templates.build(tid, {})
        q = d["query"]
        name = q["check"] if d["type"] == "service_check" else re.match(r"^[a-z]+:([^{]+)\{", q["q"]).group(1)
        assert name in known, f"{tid} : {name} inconnu de l'agent"


def test_one_board_per_integration():
    covered = {i for c in CAT.CATALOGUE for i in c["integrations"]}
    assert {"jenkins", "github", "odoo", "wsus", "openmetrics:zot"} <= covered
    if AGENT.is_dir():
        reg = (AGENT / "integrations" / "vague6.go").read_text(encoding="utf-8")
        for p in ("jenkins", "github", "odoo"):
            assert f'named("{p}"' in reg, p
        assert 'Name: "zot"' in (AGENT / "omprofiles" / "vague6.go").read_text(encoding="utf-8")


# ── Canal GLPI ─────────────────────────────────────────────────────────────────

GOOD = {"url": "https://glpi.example.fr/apirest.php", "app_token": "A" * 40, "user_token": "u1" * 20,
        "entities_id": "3", "ticket_type": "2"}


def test_glpi_channel_validation_and_redaction():
    cfg = validate_channel("glpi", GOOD)
    assert cfg == {"url": GOOD["url"], "app_token": "A" * 40, "user_token": "u1" * 20, "entities_id": 3, "ticket_type": 2}
    assert validate_channel("glpi", {**GOOD, "entities_id": "", "ticket_type": 1})["ticket_type"] == 1
    for bad in ({**GOOD, "app_token": "court"}, {**GOOD, "user_token": "x y" * 10}, {**GOOD, "url": "ftp://glpi"},
                {**GOOD, "entities_id": "-1"}, {**GOOD, "ticket_type": 3}, {k: v for k, v in GOOD.items() if k != "user_token"}):
        with pytest.raises(ValueError):
            validate_channel("glpi", bad)
    assert {"app_token", "user_token"} <= set(SECRET_FIELDS)
    row = {"id": 1, "kind": "glpi", "config": cfg}
    red = _redact(row, Principal(1, None, None, 1, "member"))["config"]
    assert "app_token" not in red and "user_token" not in red and red["url"] == "https://glpi.example.fr/…"
    assert G.base_url({"url": "https://glpi.example.fr"}) == "https://glpi.example.fr/apirest.php"
    assert G.base_url({"url": "https://glpi.example.fr/apirest.php/"}) == "https://glpi.example.fr/apirest.php"


class FakeGLPI:
    """GLPI réduit, sur les réponses du vrai GLPI 12.0.2 (21/11/2026) : initSession, search/Ticket,
    Ticket, ITILFollowup, ITILSolution, killSession ; erreurs au format ["ERROR_…", "texte"]."""

    def __init__(self):
        self.tickets, self.followups, self.solutions, self.sessions = {}, [], [], 0

    def __call__(self, req: httpx.Request) -> httpx.Response:
        path = req.url.path.split("/apirest.php", 1)[1]
        if path != "/initSession" and req.headers.get("Session-Token") != "sess":
            return httpx.Response(401, json=["ERROR_SESSION_TOKEN_INVALID", "session_token semble incorrect"])
        if req.headers.get("App-Token") != "A" * 40:
            return httpx.Response(400, json=["ERROR_WRONG_APP_TOKEN_PARAMETER", "parameter app_token seems wrong"])
        if path == "/initSession":
            if req.headers.get("Authorization") != "user_token " + "u1" * 20:
                return httpx.Response(401, json=["ERROR_GLPI_LOGIN_USER_TOKEN", "parameter user_token seems invalid"])
            self.sessions += 1
            return httpx.Response(200, json={"session_token": "sess"})
        if path == "/killSession":
            self.sessions -= 1
            return httpx.Response(200, json=True)
        if path == "/search/Ticket":
            q = dict(req.url.params)
            ref = q["criteria[0][value]"]
            assert q["criteria[1][value]"] == "notold"
            rows = [{"2": tid} for tid, t in self.tickets.items() if ref in t["name"] and t["status"] < 5]
            return httpx.Response(200, json={"totalcount": len(rows), "data": rows})
        body = json.loads(req.content)["input"]
        if path == "/Ticket" and req.method == "POST":
            tid = len(self.tickets) + 1
            self.tickets[tid] = {**body, "status": 1}
            return httpx.Response(201, json={"id": tid, "message": ""})
        m = re.match(r"^/Ticket/(\d+)/(ITILFollowup|ITILSolution)$", path)
        if m and req.method == "POST":
            tid = int(m.group(1))
            assert body["items_id"] == tid and body["itemtype"] == "Ticket"
            if m.group(2) == "ITILFollowup":
                self.followups.append(tid)
            else:
                self.solutions.append(tid)
                self.tickets[tid]["status"] = 5
            return httpx.Response(201, json={"id": len(self.followups) + len(self.solutions), "message": ""})
        return httpx.Response(404, json=["ERROR_RESOURCE_NOT_FOUND_NOR_COMMONDBTM", "ressource introuvable"])


def _send(fake, notice):
    async def go():
        async with httpx.AsyncClient(transport=httpx.MockTransport(fake)) as client:
            return await N.send(None, client, notice)
    return asyncio.run(go())


def test_glpi_alert_followup_recovery_and_test(monkeypatch):
    async def no_guard(url, allow_private=False):
        return None
    monkeypatch.setattr(G, "check_url", no_guard)
    fake = FakeGLPI()
    chan = {"id": 9, "kind": "glpi", "name": "GLPI", "config": validate_channel("glpi", GOOD)}
    mon = {"id": 12, "name": "Jenkins injoignable", "type": "service_check", "priority": 1}
    alert = N.Notice(chan, mon, "host:ci", "ALERT", "OK", None, "Jenkins injoignable.", "https://h/#/monitors/12")
    assert _send(fake, alert) == "ticket GLPI n° 1 ouvert"
    t = fake.tickets[1]
    ref = G.ref_of("12:host:ci")
    assert t["name"].endswith(f"[{ref}]") and t["urgency"] == 5 and t["type"] == 2 and t["entities_id"] == 3
    assert "Jenkins injoignable." in t["content"] and t["content"].startswith("<p>")
    renotify = N.Notice(chan, mon, "host:ci", "ALERT", "ALERT", None, "toujours", "u", renotify=True)
    assert _send(fake, renotify) == "suivi ajouté au ticket GLPI n° 1" and fake.followups == [1]
    ok = N.Notice(chan, mon, "host:ci", "OK", "ALERT", None, "Jenkins de nouveau joignable.", "u")
    assert _send(fake, ok) == "ticket(s) GLPI résolu(s) : 1"
    assert fake.tickets[1]["status"] == 5 and fake.solutions == [1] and fake.followups == [1, 1]
    assert _send(fake, ok) == "aucun ticket GLPI ouvert pour ce moniteur"
    assert _send(fake, alert) == "ticket GLPI n° 2 ouvert"         # ticket résolu : nouvelle panne, nouveau ticket
    test = N.Notice(chan, {"id": 0, "name": "Essai de canal", "type": "test"}, "*", "OK", None, None, "essai", "u",
                    kind="test", dedup_key="test:9")
    assert _send(fake, test) == "ticket GLPI n° 3 ouvert puis résolu" and fake.tickets[3]["status"] == 5
    assert fake.sessions == 0                                          # chaque session refermée
    bad = {**chan, "config": {**chan["config"], "user_token": "X" * 40}}
    with pytest.raises(G.GLPIError, match="ERROR_GLPI_LOGIN_USER_TOKEN"):
        _send(fake, N.Notice(bad, mon, "*", "ALERT", None, None, "m", "u"))


def test_glpi_refuses_private_address():
    from hypha.synthetics.netguard import TargetRefused
    chan = {"id": 9, "kind": "glpi", "name": "GLPI",
            "config": validate_channel("glpi", {**GOOD, "url": "http://127.0.0.1:8090/apirest.php"})}
    with pytest.raises(TargetRefused):
        _send(FakeGLPI(), N.Notice(chan, {"id": 1, "name": "x"}, "*", "ALERT", None, None, "m", "u", allow_private=False))
