import json

from hypha.ingest import body as B
from hypha.ingest import pipelines as P

TS = 1_800_000_000_000


def row(message, status="info", attributes=None):
    return B.log_row(1, 10, "web-01", TS, message, status, "svc", "journald", attributes or {}, {}, None, None)


def attrs_of(r):
    return json.loads(r[B.LOG_COLUMNS.index("attributes")])


def status_of(r):
    return r[B.LOG_COLUMNS.index("status")]


# ── uvicorn (lignes relevées en production) ────────────────────────────────────

def test_uvicorn_production_lines():
    a = P.parse_access('INFO:     192.0.2.233:0 - "GET /entreprise/exemple-123456789 HTTP/1.1" 200 OK')
    assert a == {"http.method": "GET", "http.url_path": "/entreprise/exemple-123456789",
                 "http.status_code": 200, "network.client.ip": "192.0.2.233"}
    a = P.parse_access('INFO:     127.0.0.1:55224 - "POST /v1/crawl HTTP/1.1" 200 OK')
    assert a == {"http.method": "POST", "http.url_path": "/v1/crawl", "http.status_code": 200,
                 "network.client.ip": "127.0.0.1"}


def test_uvicorn_error_and_query_string():
    r = P.process_row(row('INFO:     203.0.113.5:51234 - "GET /api/v1/hosts?q=web&tag=env:prod HTTP/1.1" 500 '
                          'Internal Server Error'))
    assert status_of(r) == "error"
    assert attrs_of(r) == {"http": {"method": "GET", "url_path": "/api/v1/hosts", "status_code": 500},
                           "network": {"client": {"ip": "203.0.113.5"}}}
    assert P.parse_access("INFO:     Started server process [1234]") is None
    assert P.parse_access("INFO:     Application startup complete.") is None


# ── nginx / Apache « combined » ────────────────────────────────────────────────

def test_nginx_combined():
    line = ('203.0.113.7 - - [18/Nov/2026:14:03:12 +0100] "GET /api/v1/hosts?x=1 HTTP/2.0" 404 153 '
            '"https://hypha.s33d.fr/" "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"')
    assert P.parse_access(line) == {
        "http.method": "GET", "http.url_path": "/api/v1/hosts", "http.status_code": 404,
        "network.client.ip": "203.0.113.7", "network.bytes_written": 153,
        "http.useragent": "Mozilla/5.0 (X11; Linux x86_64; rv:140.0) Gecko/20100101 Firefox/140.0"}
    assert status_of(P.process_row(row(line))) == "warning"


def test_combined_with_request_time_user_and_escapes():
    a = P.parse_access('192.0.2.10 - alice [18/Nov/2026:14:03:12 +0100] "POST /login HTTP/1.1" 302 0 "-" '
                       '"curl/8.5.0" 0.004')
    assert a["duration"] == 4.0 and a["network.bytes_written"] == 0 and a["http.useragent"] == "curl/8.5.0"
    a = P.parse_access('198.51.100.2 - - [18/Nov/2026:14:03:12 +0000] "HEAD / HTTP/1.1" 200 - "-" "-"')
    assert "network.bytes_written" not in a and "http.useragent" not in a
    a = P.parse_access('198.51.100.2 - - [18/Nov/2026:14:03:12 +0000] "GET /a HTTP/1.1" 200 5 "-" '
                       '"Agent \\"spécial\\" 1.0"')
    assert a["http.useragent"] == 'Agent "spécial" 1.0'
    assert P.parse_access('198.51.100.2 - - [18/Nov/2026:14:03:12 +0000] "-" 400 0 "-" "-"') is None


# ── HAProxy httplog (format par défaut d'HAProxy 3.x) ──────────────────────────

def test_haproxy_httplog_reference_line():
    line = ('203.0.113.102:33317 [06/Feb/2009:12:14:14.655] http-in static/srv1 10/0/30/69/109 200 2750 - - ---- '
            '1/1/1/1/0 0/0 {1wt.eu} {} "GET /index.html HTTP/1.1"')
    assert P.parse_access(line) == {
        "http.method": "GET", "http.url_path": "/index.html", "http.status_code": 200,
        "network.client.ip": "203.0.113.102", "network.bytes_written": 2750, "haproxy.frontend": "http-in",
        "haproxy.backend": "static", "haproxy.server": "srv1", "duration": 109}


def test_haproxy_syslog_prefix_nosrv_and_h2_absolute_uri():
    line = ('haproxy[2201]: 198.51.100.12:40122 [18/Nov/2026:14:03:12.345] fe_https~ be_hypha/<NOSRV> '
            '0/-1/-1/-1/0 503 217 - - SC-- 12/12/0/0/0 0/0 "GET https://hypha.s33d.fr/api/sante HTTP/2.0"')
    a = P.parse_access(line)
    assert a["haproxy.frontend"] == "fe_https~" and a["haproxy.server"] == "<NOSRV>"
    assert a["http.url_path"] == "/api/sante" and a["http.status_code"] == 503 and a["duration"] == 0
    assert status_of(P.process_row(row(line))) == "error"
    a = P.parse_access('2001:db8::1:51234 [18/Nov/2026:14:03:12.345] fe be/s1 1/0/1/2/3 200 10 - - ---- '
                       '1/1/0/0/0 0/0 "GET / HTTP/1.1"')
    assert a["network.client.ip"] == "2001:db8::1" and a["duration"] == 3
    a = P.parse_access('203.0.113.9:4000 [18/Nov/2026:14:03:12.345] fe be/s1 0/0/0/-1/+5 -1 0 - - CD-- '
                       '1/1/0/0/0 0/0 "GET /lent HTTP/1.1"')
    assert "http.status_code" not in a and a["duration"] == 5


# ── fusion et statut ───────────────────────────────────────────────────────────

def test_existing_attributes_never_overwritten():
    line = 'INFO:     203.0.113.5:51234 - "GET /x HTTP/1.1" 500 Internal Server Error'
    r = P.process_row(row(line, attributes={"http": {"status_code": 418}, "network.client.ip": "1.2.3.4"}))
    a = attrs_of(r)
    assert a["http"] == {"status_code": 418, "method": "GET", "url_path": "/x"}
    assert a["network.client.ip"] == "1.2.3.4" and "network" not in a
    assert status_of(r) == "warning"                  # 418 conservé : ≥ 400, pas ≥ 500
    r = P.process_row(row(line, attributes={"http": "texte libre"}))
    assert attrs_of(r)["http"] == "texte libre" and attrs_of(r)["network"] == {"client": {"ip": "203.0.113.5"}}


def test_status_never_lowered_and_only_info_changes():
    line = 'INFO:     203.0.113.5:51234 - "GET /x HTTP/1.1" 404 Not Found'
    assert status_of(P.process_row(row(line, "critical"))) == "critical"
    assert status_of(P.process_row(row(line, "debug"))) == "debug"
    assert status_of(P.process_row(row(line, "info"))) == "warning"
    ok = 'INFO:     203.0.113.5:51234 - "GET /x HTTP/1.1" 204 No Content'
    assert status_of(P.process_row(row(ok))) == "info"


def test_status_from_existing_code_without_access_line():
    # OTLP : attribut à plat, message libre ; la règle de statut vaut aussi.
    original = row("échec de l'appel amont", attributes={"http.status_code": 503})
    r = P.process_row(original)
    assert status_of(r) == "error" and attrs_of(r) == {"http.status_code": 503}
    r = P.process_row(row("appel amont", attributes={"http": {"status_code": "502"}}))
    assert status_of(r) == "error"


def test_unmatched_lines_pass_unchanged():
    for msg in ("kernel: eth0: link up", "sshd[901]: Accepted publickey for alice", "",
                "Nov 18 14:03:12 web-01 CRON[1234]: (root) CMD (run-parts /etc/cron.hourly)"):
        r = row(msg)
        assert P.process_row(r) is r
    rows = [row("rien"), row('INFO:     1.2.3.4:0 - "GET / HTTP/1.1" 200 OK')]
    out = P.process_rows(rows)
    assert out[0] is rows[0] and attrs_of(out[1])["http"]["status_code"] == 200


def test_url_path():
    assert P.url_path("/a/b?c=1#x") == "/a/b"
    assert P.url_path("https://hypha.s33d.fr/api?x=1") == "/api"
    assert P.url_path("http://h") == "/"
