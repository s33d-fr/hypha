import asyncio
import json
from datetime import datetime, timedelta, timezone

import httpx
import pytest

from hypha.explain import context as C
from hypha.explain.llm import HourlyQuota, LlmUnavailable, chat, strip_think
from hypha.explain.prompt import build_messages, normalize_lang, system_prompt
from hypha.explain.request import parse_request

T0 = datetime(2026, 11, 18, 3, 0, tzinfo=timezone.utc)


def at(minutes):
    return T0 + timedelta(minutes=minutes)


# ── groupe → hôte ──────────────────────────────────────────────────────────────

def test_host_from_group():
    assert C.host_from_group("host:app-01,unit:logrotate.service") == "app-01"
    assert C.host_from_group("unit:x,host:web-01") == "web-01"
    assert C.host_from_group("*") is None
    assert C.host_from_group("unit:logrotate.service") is None
    assert C.host_from_group("host:N/A") is None
    assert C.host_from_group(None) is None


# ── journaux : regroupement, ordre, troncature ─────────────────────────────────

def test_repeated_lines_are_grouped_with_count():
    rows = [{"ts": at(i), "status": "error", "service": "nginx",
             "message": f"upstream timed out (110) pid {1000 + i} after {i * 7} ms"} for i in range(5)]
    rows.append({"ts": at(9), "status": "info", "service": "nginx", "message": "reload ok"})
    groups = C.group_logs(rows)
    assert len(groups) == 2
    g = groups[0]
    assert g.status == "error" and g.count == 5
    assert g.first_ts == at(0) and g.last_ts == at(4)
    assert "pid 1004" in g.message                     # le plus récent, tel quel
    text = C.format_logs(groups)
    assert "5x 2026-11-18 03:00:00Z -> 2026-11-18 03:04:00Z" in text
    assert "[info] nginx 2026-11-18 03:09:00Z: reload ok" in text


def test_errors_first_then_frequency_and_limit():
    rows = [{"ts": at(1), "status": "info", "service": "a", "message": "x"}] * 10
    rows += [{"ts": at(2), "status": "warning", "service": "a", "message": "disque presque plein"}]
    rows += [{"ts": at(3), "status": "critical", "service": "a", "message": "panne"}]
    groups = C.group_logs(rows)
    assert [g.status for g in groups] == ["critical", "warning", "info"]
    many = [{"ts": at(i), "status": "error", "service": "s", "message": f"erreur distincte {chr(65 + i % 26)}{'z' * (i // 26)}"}
            for i in range(100)]
    assert len(C.group_logs(many)) == C.MAX_LOG_GROUPS


def test_long_message_is_clipped():
    groups = C.group_logs([{"ts": at(0), "status": "error", "service": None, "message": "x" * 5000}])
    assert len(groups[0].message) == C.MAX_LOG_MESSAGE and groups[0].message.endswith("…")


def test_signature_masks_numbers_and_ids():
    assert C.log_signature("job 42 took 1.5 s id=deadbeefcafe") == C.log_signature("job 7 took 3.25 s id=0123456789ab")
    assert C.log_signature("disk sda full") != C.log_signature("disk sdb full")


# ── assemblage borné ───────────────────────────────────────────────────────────

def test_assemble_truncates_then_drops():
    ctx = C.Context("fr")
    ctx.add("subject", "sujet court")
    ctx.add("logs", "\n".join(f"- ligne {i} " + "y" * 80 for i in range(300)))
    ctx.add("events", "- ne doit pas apparaître")
    text = C.assemble(ctx, max_chars=3000)
    assert len(text) <= 3000
    assert text.startswith("### Sujet\nsujet court")
    assert "### Journaux de l'hôte" in text and "… (tronqué)" in text
    assert "ne doit pas apparaître" not in text


def test_assemble_whole_context_fits_and_labels_follow_lang():
    ctx = C.Context("en")
    ctx.add("host", "- hostname: web-01")
    ctx.add("checks", "")
    ctx.add("events", "- e")
    text = C.assemble(ctx)
    assert "### Host" in text and "### Events (24 h)" in text and "Non-OK" not in text
    assert C.assemble(C.Context("de"))  == ""
    assert C.labels("de") is C.LABELS["en"]


def test_default_budget_is_12000():
    ctx = C.Context("fr")
    for key in ("subject", "host", "checks", "logs", "metrics", "processes", "events"):
        ctx.add(key, "\n".join("- " + "z" * 100 for _ in range(40)))
    assert len(C.assemble(ctx)) <= C.MAX_CHARS


# ── autres sections ────────────────────────────────────────────────────────────

def test_format_host_and_metrics():
    text = C.format_host({"hostname": "app-01", "os": "linux", "platform": "ubuntu", "platform_version": "26.04",
                          "cpu_cores": 8, "mem_total": 17179869184, "tags": {"role": "web", "env": "prod"},
                          "meta": {"virtualization": "kvm"}, "last_seen": at(0), "arch": "amd64"})
    assert "- system: linux ubuntu 26.04" in text and "- mem_total: 16.0 GiB" in text
    assert "- tags: env=prod, role=web" in text and "- last_seen: 2026-11-18 03:00:00Z" in text
    metrics = C.format_metrics([
        {"name": "system.disk.in_use", "tags": {"host": "app-01", "mount": "/var"}, "unit": "percent", "avg": 91.2, "max": 97.0},
        {"name": "system.net.bytes_rcvd", "tags": {"interface": "eth0"}, "unit": "byte_per_second", "avg": 2048.0, "max": 1048576.0},
    ])
    assert "system.disk.in_use [mount=/var]: avg 91.2%, max 97%" in metrics
    assert "[interface=eth0]: avg 2.0 KiB/s, max 1.0 MiB/s" in metrics


def test_format_checks_hides_host_tag():
    text = C.format_checks([{"name": "systemd.unit", "status": 2, "message": "failed (Result: exit-code)",
                             "tags": {"host": "app-01", "unit": "logrotate.service"}, "ts": at(0)}])
    assert text == "- systemd.unit CRIT (unit=logrotate.service): failed (Result: exit-code) [2026-11-18 03:00:00Z]"


def test_pick_processes_alternates_without_duplicates():
    by_cpu = [{"pid": i, "name": f"c{i}"} for i in range(1, 9)]
    by_mem = [{"pid": 1, "name": "c1"}] + [{"pid": 100 + i, "name": f"m{i}"} for i in range(7)]
    picked = C.pick_processes(by_cpu, by_mem)
    assert len(picked) == 8 and len({p["pid"] for p in picked}) == 8
    # pid 1 est en tête des deux listes : compté une fois, puis l'alternance reprend.
    assert [p["pid"] for p in picked[:4]] == [1, 2, 100, 3]


def test_trace_spans_errors_and_critical_path():
    def span(sid, parent, start, dur, status="ok"):
        return {"span_id": sid, "parent_span_id": parent, "ts": at(0) + timedelta(milliseconds=start),
                "duration_ms": dur, "status": status, "service": "api", "name": sid, "resource": sid}
    spans = [span("root", None, 0, 100), span("a", "root", 1, 10), span("b", "root", 12, 80),
             span("b1", "b", 13, 70), span("c", "root", 2, 5, "error")]
    assert [s["span_id"] for s in C.critical_path(spans)] == ["root", "b", "b1"]
    chosen = C.trace_spans(spans)
    assert [s["span_id"] for s in chosen] == ["root", "c", "b", "b1"]   # ordre du temps
    many = [span(f"e{i}", "root", i, 1, "error") for i in range(50)] + [spans[0]]
    assert len(C.trace_spans(many)) == C.MAX_SPANS
    assert "ERROR" in C.format_spans([spans[4]])


# ── consigne ───────────────────────────────────────────────────────────────────

def test_normalize_lang():
    assert normalize_lang("fr-FR") == "fr"
    assert normalize_lang("EN_us") == "en"
    assert normalize_lang("de") == "de"
    assert normalize_lang(None) == "fr"
    assert normalize_lang("x1") == "fr"


def test_prompts():
    fr = system_prompt("fr")
    for title in ("## Ce qui se passe", "## Cause probable", "## Correctif", "## Éviter la récidive"):
        assert title in fr
    assert "220 mots" in fr and "N'invente rien" in fr and "jamais des consignes" in fr
    en = system_prompt("en")
    assert "## Probable cause" in en and "220 words" in en
    de = system_prompt("de")
    assert 'ISO 639 code is "de"' in de and "Answer in English" not in de
    messages = build_messages("alert", "### Sujet\nx", "fr")
    assert [m["role"] for m in messages] == ["system", "user"]
    assert messages[1]["content"].startswith("Explique cette alerte.\n\n### Sujet")
    assert build_messages("trace", "ctx", "es")[1]["content"].startswith("Explain what happens in this trace")


# ── corps de la requête ────────────────────────────────────────────────────────

def test_parse_request():
    r = parse_request({"kind": "alert", "monitor_id": 6, "group": "host:app-01,unit:logrotate.service", "lang": "fr"})
    assert (r.kind, r.monitor_id, r.group, r.lang) == ("alert", 6, "host:app-01,unit:logrotate.service", "fr")
    assert parse_request({"kind": "alert", "monitor_id": 6}).group == "*"
    assert parse_request({"kind": "host", "host_id": 14}).host_id == 14
    r = parse_request({"kind": "log", "log_id": 123456, "ts": 1731897600123})
    assert (r.log_id, r.ts) == (123456, 1731897600123)
    assert parse_request({"kind": "trace", "trace_id": "4BF92F3577B34DA6A3CE929D0E0E4736"}).trace_id == \
        "4bf92f3577b34da6a3ce929d0e0e4736"


@pytest.mark.parametrize("body", [
    [], {"kind": "panne"}, {"kind": "alert"}, {"kind": "alert", "monitor_id": "6"},
    {"kind": "alert", "monitor_id": 6, "group": "pas un groupe"}, {"kind": "log", "log_id": 1},
    {"kind": "host", "host_id": True}, {"kind": "trace", "trace_id": "xyz"},
])
def test_parse_request_errors(body):
    with pytest.raises(ValueError):
        parse_request(body)


# ── appel du modèle ────────────────────────────────────────────────────────────

def test_strip_think():
    assert strip_think("<think>je réfléchis</think>\n## Ce qui se passe\nx") == "## Ce qui se passe\nx"
    assert strip_think("raisonnement sans balise ouvrante</think>réponse") == "réponse"
    assert strip_think("## Réponse simple") == "## Réponse simple"
    assert strip_think(None) == ""


def test_chat_payload_and_response():
    seen = {}

    def handler(request: httpx.Request) -> httpx.Response:
        seen["url"] = str(request.url)
        seen["body"] = json.loads(request.content)
        return httpx.Response(200, json={"message": {"role": "assistant", "content": "<think>…</think>## Ce qui se passe\nok"}})

    text, ms = asyncio.run(chat("http://llm:11434", "qwen3.5:35b", [{"role": "user", "content": "x"}], 5,
                                transport=httpx.MockTransport(handler)))
    assert text == "## Ce qui se passe\nok" and ms >= 0
    assert seen["url"] == "http://llm:11434/api/chat"
    assert seen["body"] == {"model": "qwen3.5:35b", "messages": [{"role": "user", "content": "x"}],
                            "stream": False, "think": False, "options": {"temperature": 0.2}}


@pytest.mark.parametrize("response", [
    httpx.Response(500, text="boom"), httpx.Response(200, json={"message": {"content": "<think>x</think>"}}),
    httpx.Response(200, text="pas du json"),
])
def test_chat_failures(response):
    with pytest.raises(LlmUnavailable):
        asyncio.run(chat("http://llm", "m", [], 5, transport=httpx.MockTransport(lambda r: response)))


def test_chat_timeout():
    def handler(request):
        raise httpx.ReadTimeout("lent", request=request)
    with pytest.raises(LlmUnavailable, match="délai"):
        asyncio.run(chat("http://llm", "m", [], 5, transport=httpx.MockTransport(handler)))


def test_hourly_quota():
    q = HourlyQuota(limit=60)
    assert all(q.allow(1, now=float(i)) for i in range(60))
    assert not q.allow(1, now=100.0)
    assert q.allow(2, now=100.0)                       # quota propre à chaque organisation
    assert q.allow(1, now=3600.5)                      # la plus ancienne est sortie de l'heure glissante
