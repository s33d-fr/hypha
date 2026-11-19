"""§ 23 : motifs, masquage des données sensibles, analyse sécurité, regroupement, métriques de journaux."""
import json
from datetime import datetime, timezone

import pytest

from hypha.errors import QueryError
from hypha.ingest import body as B
from hypha.ingest import log_metrics as LM
from hypha.ingest import pipelines as P
from hypha.ingest import sensitive as S
from hypha.query import logs_group as G
from hypha.query.patterns import group_patterns, pattern_query, template

from conftest import placeholders

TS = 1_800_000_000_000


def row(message, status="info", attributes=None, service="svc", ts=TS):
    return B.log_row(1, 10, "web-01", ts, message, status, service, "journald", attributes or {}, {}, None, None)


def msg(r):
    return r[B.LOG_COLUMNS.index("message")]


def attrs(r):
    return json.loads(r[B.LOG_COLUMNS.index("attributes")])


# ── motifs ─────────────────────────────────────────────────────────────────────

def test_template_placeholders():
    assert template("Failed password for root from 203.0.113.7 port 52214 ssh2") == \
        "Failed password for root from <ip> port <num> ssh2"
    assert template('GET /api/v1/hosts/12 took 34ms user="alice" id=6f1c2b8e-0000-4000-8000-000000000001') == \
        'GET <path> took <num>ms user=<str> id=<uuid>'
    assert template("2026-11-18T14:03:12.123Z worker-3 0xdeadbeef 1a2b3c4d5e") == "<ts> worker-<num> <hex> <hex>"
    assert template("[18/Nov/2026:14:03:12 +0100] sda1 v2 l'hôte n'a pas répondu") == \
        "[<ts>] sda1 v2 l'hôte n'a pas répondu"
    assert template("connect to [2001:db8::1]:443 and fe80::1 failed") == "connect to [<ip>]:<num> and <ip> failed"
    assert template("") == "" and template(None) == ""
    assert len(template("x " * 5000)) <= 500


def test_pattern_query_and_grouping():
    tpl = "Failed password for <str> from <ip> port <num> ssh2"
    assert pattern_query(tpl, "sshd") == 'service:sshd "Failed password for" "from" "port" "ssh2"'
    assert pattern_query('say "hello" <num>') == '"say \\"hello\\""'
    assert pattern_query("<num> <ip>") == ""
    now = datetime(2026, 11, 18, tzinfo=timezone.utc)
    rows = [{"message": f"upstream timed out pid {i}", "status": "error", "service": "nginx", "ts": now}
            for i in range(3)] + [{"message": "started", "status": "info", "service": "cron", "ts": now}]
    out = group_patterns(rows, 10)
    assert out[0]["pattern"] == "upstream timed out pid <num>" and out[0]["count"] == 3
    assert out[0]["share"] == 0.75 and out[0]["status"] == {"error": 3} and out[0]["services"] == ["nginx"]
    assert out[0]["query"] == 'service:nginx "upstream timed out pid"'
    assert len(group_patterns(rows, 1)) == 1


def test_pattern_query_round_trips_through_the_log_language():
    from hypha.query import logs_match
    tpl = template('Invalid user "admin" from 198.51.100.4')
    pred = logs_match.compile_predicate(pattern_query(tpl))
    assert pred({"message": 'Invalid user "admin" from 198.51.100.4'})
    assert not pred({"message": "Accepted publickey for alice"})


# ── masquage ───────────────────────────────────────────────────────────────────

def rules(*keys, **kw):
    return [S.compile_rule({"id": i + 1, "name": k, "builtin": k, **kw}) for i, k in enumerate(keys)]


def masked(text, *keys):
    return S.mask_text(text, rules(*keys))[0]


def nir(body: str) -> str:
    n = int(body[:5] + {"2A": "19", "2B": "18"}.get(body[5:7], body[5:7]) + body[7:])
    return body + f"{97 - n % 97:02d}"


def test_controls():
    assert S.luhn_ok("4111111111111111") and not S.luhn_ok("4111111111111112")
    assert S.iban_ok("FR7630006000011234567890189") and S.iban_ok("DE89370400440532013000")
    assert not S.iban_ok("FR7630006000011234567890188") and not S.iban_ok("FR763000600001123456789018")
    good = nir("1850578006084")
    assert S.nir_ok(good) and not S.nir_ok(good[:-2] + "00")
    corsica = nir("185052A006084")
    assert S.nir_ok(corsica)
    assert S.public_ipv4("8.8.8.8") and not S.public_ipv4("172.16.1.10") and not S.public_ipv4("203.0.113.5")


def test_builtin_rules():
    assert masked("carte 4111 1111 1111 1111 refusée", "credit_card") == "carte [masqué:credit_card] refusée"
    assert masked("carte 4111 1111 1111 1112", "credit_card") == "carte 4111 1111 1111 1112"
    assert masked("ref 0000000000000000", "credit_card") == "ref 0000000000000000"
    assert masked("virement FR76 3000 6000 0112 3456 7890 189 EUR", "iban") == "virement [masqué:iban] EUR"
    assert masked("IBAN DE89370400440532013000.", "iban") == "IBAN [masqué:iban]."
    assert masked("FR7630006000011234567890188", "iban") == "FR7630006000011234567890188"
    n = nir("1850578006084")
    spaced = f"{n[0]} {n[1:3]} {n[3:5]} {n[5:7]} {n[7:10]} {n[10:13]} {n[13:]}"
    assert masked(f"assuré {spaced} ok", "fr_nir") == "assuré [masqué:fr_nir] ok"
    assert masked(f"assuré {n}", "fr_nir") == "assuré [masqué:fr_nir]"
    assert masked(f"assuré {n[:-2]}00", "fr_nir") == f"assuré {n[:-2]}00"
    assert masked("appel du 06 12 34 56 78 et +33 6 12 34 56 78, 0612345678", "phone_fr") == \
        "appel du [masqué:phone_fr] et [masqué:phone_fr], [masqué:phone_fr]"
    assert masked("pid 0612345678901", "phone_fr") == "pid 0612345678901"
    assert masked("mail de alice@example.org à x.y+z@exemple.co.uk", "email") == \
        "mail de [masqué:email] à [masqué:email]"
    assert masked("de 8.8.8.8 vers 172.16.1.10 et 203.0.113.5", "ipv4_public") == \
        "de [masqué:ipv4_public] vers 172.16.1.10 et 203.0.113.5"


@pytest.mark.parametrize("text,expected", [
    ("Authorization: Bearer abc.def.ghi-123", "Authorization: Bearer [masqué:secrets]"),
    ("curl -H 'authorization: token ghp_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'",
     "curl -H 'authorization: token [masqué:secrets]'"),
    ("login password=hunter2&user=alice", "login password=[masqué:secrets]&user=alice"),
    ('{"db_password": "s3cr3t", "user": "x"}', '{"db_password": "[masqué:secrets]", "user": "x"}'),
    ("X-Api-Key: 0123456789abcdef", "X-Api-Key: [masqué:secrets]"),  # valeur fictive, gitleaks:allow
    ("csrf_token=deadbeef42", "csrf_token=[masqué:secrets]"),
    ("cle AKIAIOSFODNN7EXAMPLE fin", "cle [masqué:secrets] fin"),
    ("jeton xoxb-123456789012-abcdefghij", "jeton [masqué:secrets]"),
    ("clé hph_" + "a" * 40 + " posée", "clé [masqué:secrets] posée"),
    ("jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U",  # JWT d'exemple de jwt.io, gitleaks:allow
     "jwt [masqué:secrets]"),
    ("-----BEGIN RSA PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY----- fin", "[masqué:secrets] fin"),
    ("Failed password for root from 1.2.3.4", "Failed password for root from 1.2.3.4"),
    ("token expired for user alice", "token expired for user alice"),
    ("bypass=1 compass=north", "bypass=1 compass=north"),
])
def test_secrets_rule(text, expected):
    assert masked(text, "secrets") == expected


def test_mask_row_message_and_attributes():
    rs = rules("secrets", "email")
    counts: dict = {}
    r = row("connexion de alice@example.org", attributes={"http": {"headers": {"authorization": "Basic Zm9vOmJhcg=="}},
                                                     "password": "hunter2", "pin": 1234, "n": 5,
                                                     "list": ["a@b.fr", 3]})
    out = S.mask_row(r, rs, counts)
    assert msg(out) == "connexion de [masqué:email]"
    a = attrs(out)
    assert a["http"]["headers"]["authorization"] == "[masqué:secrets]"
    assert a["password"] == "[masqué:secrets]" and a["n"] == 5 and a["pin"] == 1234
    assert a["list"] == ["[masqué:email]", 3]
    assert counts == {1: 2, 2: 2}
    plain = row("rien à masquer ici", attributes={"http": {"status_code": 200}})
    assert S.mask_row(plain, rs, counts) is plain


def test_scope_and_targets():
    r1 = S.compile_rule({"id": 1, "name": "x", "builtin": "email", "scope_query": "service:billing",
                         "targets": ["message"]})
    counts: dict = {}
    a = row("a@b.fr", service="billing", attributes={"to": "c@d.fr"})
    b = row("a@b.fr", service="web")
    assert msg(S.mask_row(a, [r1], counts)) == "[masqué:email]"
    assert attrs(S.mask_row(a, [r1], counts))["to"] == "c@d.fr"
    assert msg(S.mask_row(b, [r1], counts)) == "a@b.fr"


def test_custom_pattern_and_safety():
    rule = S.compile_rule({"id": 9, "name": "Matricule", "pattern": r"MAT-\d{6}", "replacement": "[masqué:matricule]"})
    assert S.mask_text("agent MAT-123456 vu", [rule])[0] == "agent [masqué:matricule] vu"
    for bad in [r"(a+)+$", r"(\w*)*x", r"(.)\1", r"", r"a|", "(" * 3, "x" * 600]:
        with pytest.raises(QueryError):
            S.compile_pattern(bad)
    assert S.compile_pattern(r"(?:ab){2,5}c").search("ababc")
    with pytest.raises(QueryError):
        S.compile_rule({"id": 1, "name": "x", "builtin": "inconnu"})


def test_seed_rows_new_org_gets_secrets_active():
    new = S.builtin_rows(True)
    old = S.builtin_rows(False)
    assert [r[1] for r in new] == list(S.BUILTIN_ORDER)
    assert [r[1] for r in new if r[3]] == ["secrets"] and not any(r[3] for r in old)
    assert all(r[2] == f"[masqué:{r[1]}]" for r in new)


def test_rules_sql_placeholders():
    assert placeholders(S.RULES_SQL) == 1


# ── analyse sécurité ───────────────────────────────────────────────────────────

@pytest.mark.parametrize("line,expected", [
    ("Failed password for invalid user admin from 198.51.100.4 port 52214 ssh2",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "admin", "network.client.ip": "198.51.100.4"}),
    ("sshd[1234]: Failed password for root from 2001:db8::7 port 22 ssh2",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "root", "network.client.ip": "2001:db8::7"}),
    ("Accepted publickey for alice from 172.16.1.10 port 51234 ssh2: ED25519 SHA256:abc",
     {"evt.name": "authentication", "evt.outcome": "success", "usr.name": "alice", "network.client.ip": "172.16.1.10"}),
    ("Invalid user oracle from 203.0.113.9 port 4000",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "oracle", "network.client.ip": "203.0.113.9"}),
    ("Disconnected from authenticating user root 203.0.113.9 port 4022 [preauth]",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "root", "network.client.ip": "203.0.113.9"}),
    ("    alice : TTY=pts/0 ; PWD=/home/alice ; USER=root ; COMMAND=/usr/bin/apt update",
     {"evt.name": "sudo", "evt.outcome": "success", "usr.name": "alice", "sudo.command": "/usr/bin/apt update",
      "sudo.run_as": "root"}),
    ("alice : 3 incorrect password attempts ; TTY=pts/0 ; PWD=/home/alice ; USER=root ; COMMAND=/bin/ls",
     {"evt.name": "sudo", "evt.outcome": "failure", "usr.name": "alice", "sudo.command": "/bin/ls", "sudo.run_as": "root"}),
    ("pam_unix(sudo:auth): authentication failure; logname=alice uid=1000 euid=0 tty=/dev/pts/0 ruser=alice rhost=  user=alice",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "alice", "evt.category": "sudo"}),
    ("pam_unix(sshd:auth): authentication failure; logname= uid=0 euid=0 tty=ssh ruser= rhost=198.51.100.4  user=root",
     {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "root", "network.client.ip": "198.51.100.4"}),
    ("(to root) alice on pts/1", {"evt.name": "authentication", "evt.outcome": "success", "usr.name": "alice",
                                "evt.category": "su"}),
    ("FAILED SU (to root) alice on pts/1", {"evt.name": "authentication", "evt.outcome": "failure", "usr.name": "alice",
                                          "evt.category": "su"}),
    ("2026-11-18 10:00:00,123 fail2ban.actions [811]: NOTICE  [sshd] Ban 198.51.100.4",
     {"evt.name": "ban", "evt.outcome": "success", "fail2ban.jail": "sshd", "network.client.ip": "198.51.100.4"}),
    ("NOTICE [recidive] Unban 198.51.100.4", {"evt.name": "unban", "evt.outcome": "success",
                                              "fail2ban.jail": "recidive", "network.client.ip": "198.51.100.4"}),
])
def test_security_lines(line, expected):
    assert P.parse_security(line) == expected


def test_security_merged_into_attributes():
    out = P.process_row(row("Failed password for root from 198.51.100.4 port 22 ssh2",
                            attributes={"usr": {"name": "déjà"}}))
    a = attrs(out)
    assert a["evt"] == {"name": "authentication", "outcome": "failure"}
    assert a["usr"]["name"] == "déjà" and a["network"]["client"]["ip"] == "198.51.100.4"
    assert P.parse_security("Server listening on 0.0.0.0 port 22.") is None
    assert P.parse_security("token for nothing") is None


# ── regroupement ───────────────────────────────────────────────────────────────

def test_group_keys():
    keys = G.parse_keys(["service", "@http.status_code", "host", "service"])
    assert [k.name for k in keys] == ["service", "@http.status_code", "host"]
    assert [k.tag for k in keys] == ["service", "http.status_code", "host"]
    exprs, params = G.sql_select(keys)
    assert exprs[0] == "service AS g0" and exprs[2] == "hostname AS g2"
    assert placeholders(exprs[1]) == 2 and params == [["http", "status_code"], "http.status_code"]
    assert G.sql_group(keys) == "1, 2, 3"
    assert G.row_groups(keys, {"g0": "nginx", "g1": 502, "g2": None}) == \
        {"service": "nginx", "@http.status_code": "502", "host": "N/A"}
    with pytest.raises(QueryError):
        G.parse_keys(["@a", "@b", "@c", "@d", "@e", "@f"])
    with pytest.raises(QueryError):
        G.parse_keys(["pid"])
    with pytest.raises(QueryError):
        G.parse_keys(["@bad path"])


def test_group_line_values():
    from hypha.ingest.log_filters import LogView
    line = LogView(row("x", attributes={"http": {"status_code": 502}, "duration": "12.5", "a.b": True}))
    assert G.line_value(G.parse_key("@http.status_code"), line) == "502"
    assert G.line_value(G.parse_key("@a.b"), line) == "true"
    assert G.line_value(G.parse_key("host"), line) == "web-01"
    assert G.line_value(G.parse_key("@absent"), line) is None
    assert G.line_number(("duration",), "duration", line) == 12.5
    assert G.line_number(("http", "status_code"), "http.status_code", line) == 502.0
    assert G.line_number(("a", "b"), "a.b", line) is None


# ── métriques tirées des journaux ──────────────────────────────────────────────

def test_log_metrics_count_and_distribution():
    count = LM.compile_metric({"id": 1, "name": "logs.ssh.failures", "query": "@evt.outcome:failure",
                               "group_by": ["@network.client.ip"], "aggregation": "count"})
    dist = LM.compile_metric({"id": 2, "name": "logs.http.duration", "query": "service:nginx",
                              "group_by": ["service"], "aggregation": "distribution", "attribute": "@duration"})
    agg = LM.Aggregator()
    lines = [row("x", service="sshd", attributes={"evt": {"outcome": "failure"}, "network": {"client": {"ip": "1.2.3.4"}}},
                 ts=TS + i * 1000) for i in range(3)]
    lines += [row("y", service="nginx", attributes={"duration": d}, ts=TS + 2000) for d in (10, 20, 30, 40)]
    lines.append(row("z", service="nginx", ts=TS + 20_000))               # sans durée : ignorée
    agg.observe(1, [count, dist], lines)
    bucket = TS // 15000 * 15000
    assert agg.take_closed(bucket + 15000) == []                            # pas encore clos
    out = agg.take_closed(bucket + 15000 + LM.CLOSE_DELAY_MS)
    by_name = {(o[1], tuple(sorted(o[3].items()))): o for o in out}
    c = by_name[("logs.ssh.failures", (("network.client.ip", "1.2.3.4"),))]
    assert c[2] == "count" and c[4] == bucket and c[5] == 3.0
    assert by_name[("logs.http.duration.avg", (("service", "nginx"),))][5] == 25.0
    assert by_name[("logs.http.duration.p50", (("service", "nginx"),))][5] == 25.0
    assert by_name[("logs.http.duration.max", (("service", "nginx"),))][5] == 40.0
    assert by_name[("logs.http.duration.count", (("service", "nginx"),))][5] == 4.0
    assert by_name[("logs.http.duration.p95", (("service", "nginx"),))][2] == "distribution"
    assert not agg.buckets
    agg.restore([o for o in out if o[2] == "count"])
    assert sum(agg.buckets.values()) == 3


def test_log_metric_definitions():
    with pytest.raises(QueryError):
        LM.compile_metric({"id": 1, "name": "logs.x", "query": "", "aggregation": "distribution"})
    with pytest.raises(QueryError):
        LM.compile_metric({"id": 1, "name": "logs.x", "query": '"ouvert', "aggregation": "count"})
    m = LM.compile_metric({"id": 1, "name": "logs.all", "query": "", "group_by": None, "aggregation": "count"})
    assert m.predicate(LogViewDict()) and m.keys == ()
    assert LM.quantile([1.0, 2.0, 3.0, 4.0], 0.5) == 2.5
    d = LM.Dist()
    for v in range(10_000):
        d.add(float(v))
    assert d.count == 10_000 and len(d.sample) == LM.RESERVOIR and d.vmax == 9999.0


class LogViewDict(dict):
    def get(self, key, default=None):
        return super().get(key, default)
