"""Coût à l'ingestion des traitements 0.4.0 (§§ 23, 25), mesuré en mémoire sur des lignes réalistes.

Référence : le chemin 0.3.0 en mémoire (décompression et lecture du corps JSON de l'agent, contrôle
des lignes, filtres d'exclusion, découpage des journaux d'accès). Le temps d'écriture en base (COPY,
index dont le trigramme du message) n'y figure pas : le rapport réel, requête comprise, est plus faible.

Mesuré : le même chemin suivi du masquage, de l'empreinte des erreurs et de 3 métriques tirées des
journaux, dans deux réglages :
- celui d'une organisation nouvelle (règle `secrets` active) : le surcoût ne double pas le temps
  d'ingestion en mémoire (rapport < 2, exigence du chantier) ;
- le cas le plus lourd, les 7 règles livrées actives : rapport borné à 3,5 en mémoire (garde contre
  une régression), soit moins du double une fois l'écriture en base comptée.
"""
import gzip
import json
import time

from hypha.ingest import body as B
from hypha.ingest import error_tracking as ET
from hypha.ingest import log_filters as LF
from hypha.ingest import log_metrics as LM
from hypha.ingest import pipelines as P
from hypha.ingest import sensitive as S

NOW = 1_800_000_000_000
LINES = 20_000

SAMPLES = [
    {"message": 'INFO:     192.0.2.233:0 - "GET /entreprise/exemple-123456789 HTTP/1.1" 200 OK',
     "service": "uvicorn", "status": "info"},
    {"message": '203.0.113.7 - - [18/Nov/2026:14:03:12 +0100] "GET /api/v1/hosts?x=1 HTTP/2.0" 404 153 '
                '"https://hypha.s33d.fr/" "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0" 0.012',
     "service": "nginx", "status": "info"},
    {"message": '203.0.113.102:33317 [18/Nov/2026:14:03:12.655] http-in static/srv1 10/0/30/69/109 200 2750 - - ---- '
                '1/1/1/1/0 0/0 {} {} "GET /index.html HTTP/1.1"', "service": "haproxy", "status": "info"},
    {"message": "Failed password for invalid user admin from 1.2.3.4 port 52214 ssh2", "service": "sshd",
     "status": "info"},
    {"message": "pam_unix(cron:session): session opened for user root(uid=0) by root(uid=0)", "service": "cron",
     "status": "info"},
    {"message": "checkpoint complete: wrote 1234 buffers (7.5%); 0 WAL file(s) added, 0 removed, 3 recycled",
     "service": "postgresql", "status": "info"},
    {"message": "envoi du rapport à alice@example.org terminé en 412 ms", "service": "worker", "status": "info",
     "attributes": {"report": {"id": 12, "to": "alice@example.org"}, "duration": 412}},
    {"message": "upstream timed out (110: Connection timed out) while reading response header from upstream",
     "service": "nginx", "status": "error", "attributes": {"http": {"status_code": 504}}},
    {"message": "GET /api/sante HTTP/1.1 200", "service": "uvicorn", "status": "info"},
    {"message": "Traceback (most recent call last):\n  File \"/opt/app/views.py\", line 42, in view\n"
                "    total = int(value)\nValueError: invalid literal for int() with base 10: 'abc'",
     "service": "api", "status": "error"},
    {"message": "appel sortant avec Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJl",
     "service": "proxy", "status": "info"},
]


def items(n=LINES):
    out = []
    for i in range(n):
        s = SAMPLES[i % len(SAMPLES)]
        out.append({**s, "ts": NOW + i, "source": "journald", "attributes": dict(s.get("attributes") or {})})
    return out


def payload(n=LINES) -> bytes:
    return gzip.compress(json.dumps({"agent": {"id": "6f1c2b8e-0000-4000-8000-000000000001", "hostname": "web-01"},
                                     "logs": items(n)}).encode())


def baseline(raw, filters):
    body = B.parse_json_object(B.decompress(raw, "gzip"))
    rows, _ = B.parse_logs(B.require_list(body, "logs"), B.Window.around(NOW), 1, 10, "web-01", now=NOW)
    rows, _ = LF.apply_filters(filters, rows)
    return P.process_rows(rows)


def full(raw, filters, rules, metrics, agg):
    rows = baseline(raw, filters)
    counts: dict = {}
    rows = S.mask_rows(rows, rules, counts)
    occurrences = [o for o in (ET.from_log_row(r, i) for i, r in enumerate(rows)) if o is not None]
    ET.aggregate(occurrences)
    agg.observe(1, metrics, rows)
    return rows, counts, occurrences


def best_of(fn, runs=3):
    best = float("inf")
    for _ in range(runs):
        t0 = time.perf_counter()
        fn()
        best = min(best, time.perf_counter() - t0)
    return best


FILTERS = [LF.compile_filter({"id": 1, "query": '"GET /api/sante"', "action": "exclude", "sample_rate": 0.0}),
           LF.compile_filter({"id": 2, "query": "service:sshd debug1", "action": "exclude", "sample_rate": 0.0})]
METRICS = [LM.compile_metric(m) for m in (
    {"id": 1, "name": "logs.ssh.failures", "query": "@evt.outcome:failure", "group_by": ["@network.client.ip"],
     "aggregation": "count"},
    {"id": 2, "name": "logs.http.errors", "query": "status:error", "group_by": ["service", "@http.status_code"],
     "aggregation": "count"},
    {"id": 3, "name": "logs.jobs.duration", "query": "service:worker", "group_by": ["service"],
     "aggregation": "distribution", "attribute": "@duration"})]


def measure(keys):
    rules = S.RuleSet(S.compile_rule({"id": i + 1, "name": k, "builtin": k}) for i, k in enumerate(keys))
    raw = payload()
    base = best_of(lambda: baseline(raw, FILTERS))
    extra = best_of(lambda: full(raw, FILTERS, rules, METRICS, LM.Aggregator()))
    rows, counts, occurrences = full(raw, FILTERS, rules, METRICS, LM.Aggregator())
    report = (f"{len(keys)} règle(s) : référence {base / LINES * 1e6:.1f} µs/ligne, avec masquage + erreurs + "
              f"3 métriques {extra / LINES * 1e6:.1f} µs/ligne, rapport {extra / base:.2f}")
    print(report)
    assert counts and occurrences
    return extra / base, rows, report


def test_ingestion_cost_new_org_settings():
    ratio, rows, report = measure(["secrets"])
    assert any("[masqué:secrets]" in r[7] for r in rows)
    assert ratio < 2.0, report


def test_ingestion_cost_all_builtin_rules():
    ratio, rows, report = measure(S.BUILTIN_ORDER)
    assert any("[masqué:email]" in r[7] for r in rows) and any("[masqué:ipv4_public]" in r[7] for r in rows)
    assert ratio < 3.5, report
