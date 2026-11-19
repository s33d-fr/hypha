"""§ 29 : synthétiques multi-étapes, assertions, extractions, emplacements, verdict par emplacement."""
import asyncio
import json

import httpx
import pytest

from hypha.synthetics import assertions as A
from hypha.synthetics import multistep as M
from hypha.synthetics.locations import consecutive_failures_by_location, synthetic_verdict
from hypha.synthetics.validate import location_host_ids, normalize_test, step_hosts


def resp(status=200, headers=None, body="", latency=12.0):
    return A.Response(status, {k.lower(): v for k, v in (headers or {}).items()}, body, latency)


def test_json_path():
    assert A.parse_path("$.a.b[0]['c d']") == ["a", "b", 0, "c d"]
    assert A.parse_path("data.items[2]") == ["data", "items", 2]
    assert A.json_get({"a": [{"b": 1}]}, A.parse_path("$.a[0].b")) == 1
    assert A.json_get({"a": [1]}, ["a", 5]) is A._MISSING
    for bad in ("", "$.a[", "$.a..b"):
        with pytest.raises(ValueError):
            A.parse_path(bad)


def test_assertions():
    checks = A.validate_assertions([
        {"type": "status", "op": "eq", "value": 200},
        {"type": "header", "path": "Content-Type", "op": "contains", "value": "json"},
        {"type": "body_contains", "value": "\"ok\""},
        {"type": "json_path", "path": "$.data.count", "op": "gt", "value": 2},
        {"type": "json_path", "path": "$.data.name", "op": "matches", "value": "^we[a-z]+"},
        {"type": "latency_ms", "op": "lt", "value": 500},
        {"type": "header", "path": "x-absent", "op": "ne", "value": "x"},
    ])
    r = resp(200, {"Content-Type": "application/json"}, json.dumps({"ok": True, "data": {"count": 3, "name": "web-01"}}))
    results = A.evaluate(checks, r)
    assert all(x["ok"] for x in results), results
    bad = A.evaluate(A.validate_assertions([{"type": "status", "op": "eq", "value": "201"},
                                            {"type": "json_path", "path": "$.nope", "op": "eq", "value": 1}]), r)
    assert [x["ok"] for x in bad] == [False, False] and bad[0]["actual"] == 200
    assert "en échec" in A.describe_failure(bad[0])
    for wrong in ([{"type": "x"}], [{"type": "status", "op": "lt", "value": "abc"}],
                  [{"type": "json_path", "op": "eq", "value": 1}], [{"type": "status", "op": "matches", "value": "(a+)+"}],
                  [{"type": "status"}], {"type": "status"}):
        with pytest.raises(ValueError):
            A.validate_assertions(wrong)


def test_extract_and_render():
    ex = A.validate_extracts([{"name": "jeton", "from": "json_path", "path": "$.token"},
                              {"name": "sid", "from": "header", "path": "X-Session"},
                              {"name": "csrf", "from": "regex", "path": r'name="csrf" value="([^"]+)"'},
                              {"name": "absent", "from": "header", "path": "X-Nope"}])
    r = resp(200, {"X-Session": "abc"}, json.dumps({"token": "t0k"}) + ' name="csrf" value="zz9"')
    r.doc = {"token": "t0k"}
    found, missing = A.extract(ex, r)
    assert found == {"jeton": "t0k", "sid": "abc", "csrf": "zz9"} and missing == ["absent"]
    miss: list = []
    assert A.render("https://x/{{ jeton }}/{{inconnu}}", {"jeton": "t0k"}, miss) == "https://x/t0k/{{inconnu}}"
    assert miss == ["inconnu"]
    with pytest.raises(ValueError):
        A.validate_extracts([{"name": "1x", "from": "header", "path": "a"}])


def test_normalize_multistep_and_locations():
    t = normalize_test({"name": "Connexion", "kind": "multistep", "locations": ["server", "agent:12", "agent:12"],
                        "config": {"variables": {"base": "https://hypha.s33d.fr", "n": 3}},
                        "steps": [{"name": "page", "url": "{{base}}/connexion", "assertions": [
                                      {"type": "status", "op": "eq", "value": 200}],
                                   "extract": [{"name": "csrf", "from": "regex", "path": "csrf=(\\w+)"}]},
                                  {"url": "https://hypha.s33d.fr/api/auth/local", "method": "post",
                                   "body": "{\"csrf\":\"{{csrf}}\"}", "headers": {"Content-Type": "application/json"}}]},
                       icmp_ok=True)
    assert t["kind"] == "multistep" and t["target"] == "{{base}}/connexion"
    assert t["locations"] == ["server", "agent:12"] and location_host_ids(t["locations"]) == [12]
    assert t["config"]["variables"] == {"base": "https://hypha.s33d.fr", "n": "3"}
    assert t["steps"][1]["method"] == "POST" and t["steps"][1]["name"] == "étape 2"
    assert step_hosts(t["steps"]) == ["hypha.s33d.fr"]
    h = normalize_test({"name": "Site", "kind": "http", "target": "https://hypha.s33d.fr/",
                        "config": {"assertions": [{"type": "status", "op": "eq", "value": 200}]}}, icmp_ok=True)
    assert h["config"]["assertions"][0]["type"] == "status" and h["locations"] == ["server"] and h["steps"] == []
    for bad in ({"name": "x", "kind": "multistep", "steps": []},
                {"name": "x", "kind": "multistep", "steps": [{"url": "ftp://x"}]},
                {"name": "x", "kind": "multistep", "steps": [{"url": "https://x", "timeout_s": 600}]},
                {"name": "x", "kind": "http", "target": "https://x", "locations": ["agent:abc"]},
                {"name": "x", "kind": "http", "target": "https://x", "locations": []},
                {"name": "x", "kind": "multistep", "steps": [{"url": "https://x"}], "config": {"variables": {"a b": 1}}}):
        with pytest.raises(ValueError):
            normalize_test(bad, icmp_ok=True)


def test_verdict_by_location():
    rows = [{"location": "fr-s33d", "ok": False}, {"location": "agent:web-01", "ok": False},
            {"location": "fr-s33d", "ok": False}, {"location": "agent:web-01", "ok": True},
            {"location": "fr-s33d", "ok": True}]
    assert consecutive_failures_by_location(rows) == {"fr-s33d": 2, "agent:web-01": 1}
    v = synthetic_verdict(rows, failures=2, min_failing=1)
    assert v.alert and v.failing == 1
    assert not synthetic_verdict(rows, failures=2, min_failing=2).alert
    assert synthetic_verdict(rows, failures=1, min_failing=2).alert
    assert synthetic_verdict([], 1, 1) is None


def test_multistep_runs_steps_with_variables(monkeypatch):
    async def fake_resolve(host, port, allow_private):
        return ["93.184.216.34"], 0.1
    monkeypatch.setattr(M, "resolve", fake_resolve)
    seen = []

    def handler(request: httpx.Request) -> httpx.Response:
        seen.append((request.method, str(request.url), request.content.decode()))
        if request.url.path == "/connexion":
            return httpx.Response(200, text='<input name="csrf" value="c5rf">', headers={"Set-Cookie": "sid=1"})
        if request.url.path == "/api/login":
            return httpx.Response(302, headers={"Location": "/tableau"})
        if request.url.path == "/tableau":
            return httpx.Response(200, json={"user": {"name": "alice"}})
        return httpx.Response(404)

    steps = [{"name": "page", "url": "{{base}}/connexion", "method": "GET",
              "assertions": [{"type": "status", "op": "eq", "value": 200}],
              "extract": [{"name": "csrf", "from": "regex", "path": 'value="([^"]+)"'}]},
             {"name": "connexion", "url": "{{base}}/api/login", "method": "POST", "body": "csrf={{csrf}}",
              "assertions": [{"type": "json_path", "path": "$.user.name", "op": "eq", "value": "alice"}],
              "follow_redirects": True}]
    r = asyncio.run(M.run_multistep(steps, {"variables": {"base": "https://exemple.test"}}, False,
                                    transport=httpx.MockTransport(handler)))
    assert r.ok, r.error
    assert seen[1] == ("POST", "https://exemple.test/api/login", "csrf=c5rf")
    assert seen[2][:2] == ("GET", "https://exemple.test/tableau")
    assert [s["ok"] for s in r.details["steps"]] == [True, True] and r.details["steps"][1]["redirects"] == 1
    # Une assertion en échec arrête le test à cette étape.
    steps[0]["assertions"] = [{"type": "status", "op": "eq", "value": 201}]
    r = asyncio.run(M.run_multistep(steps, {"variables": {"base": "https://exemple.test"}}, False,
                                    transport=httpx.MockTransport(handler)))
    assert not r.ok and len(r.details["steps"]) == 1 and "page" in r.error
    # Variable inconnue : échec sans requête.
    r = asyncio.run(M.run_multistep([{"name": "a", "url": "{{rien}}/x"}], {}, False,
                                    transport=httpx.MockTransport(handler)))
    assert not r.ok and "variable inconnue" in r.error


def test_multistep_network_guard(monkeypatch):
    from hypha.synthetics.netguard import TargetRefused

    async def refuse(host, port, allow_private):
        raise TargetRefused("adresse non publique refusée : 172.16.1.1")
    monkeypatch.setattr(M, "resolve", refuse)
    r = asyncio.run(M.run_multistep([{"name": "a", "url": "http://172.16.1.1/"}], {}, False,
                                    transport=httpx.MockTransport(lambda req: httpx.Response(200))))
    assert not r.ok and "non publique" in r.error
