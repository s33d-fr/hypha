"""Réglages posés à la création d'un espace : canal e-mail du compte et moniteurs de base."""
import asyncio

from hypha.monitors import defaults
from hypha.monitors import evaluate as ev
from hypha.monitors.template import flags_for, render


class Cur:
    def __init__(self, row):
        self.row = row

    async def fetchone(self):
        return self.row

    async def fetchall(self):
        return [self.row] if self.row else []


class Conn:
    """Faux accès base : rend un identifiant croissant à chaque INSERT … RETURNING id."""

    def __init__(self, done=False):
        self.sql, self.done, self.next_id = [], done, 100

    async def execute(self, sql, params=None):
        self.sql.append((" ".join(sql.split()), params))
        if "settings ? 'default_monitors'" in sql:
            return Cur({"done": self.done})
        if "RETURNING id" in sql:
            self.next_id += 1
            return Cur({"id": self.next_id})
        return Cur(None)


NAMES = ["Hôte sans nouvelles", "Disque presque plein", "Mémoire saturée", "Unité systemd en échec",
         "Service Windows arrêté", "Certificat proche de la fin"]


def test_default_monitors_valid_and_wired():
    mons = defaults.default_monitors([7])
    assert [m["name"] for m in mons] == NAMES
    assert all(m["channel_ids"] == [7] for m in mons)


def test_default_messages_say_recovery():
    ctx = {"host": "web-01", "value": "12", "group": "host:web-01",
           "tags": {"mount": "/", "unit": "nginx.service", "windows_service": "Spooler", "cert_path": "/c.pem"}}
    for m in defaults.default_monitors([]):
        if not m["message"]:
            continue
        alert = render(m["message"], {**ctx, "name": m["name"], **flags_for("ALERT", "OK")})
        back = render(m["message"], {**ctx, "name": m["name"], **flags_for("OK", "ALERT")})
        assert alert.strip() and back.strip() and alert != back, m["name"]


def test_provision_org_channel_monitors_once():
    conn = Conn()
    out = asyncio.run(defaults.provision_org(conn, 5, 9, "jeanne@example.org"))
    assert out == {"channel_id": 101, "monitor_ids": [102, 103, 104, 105, 106, 107]}
    channel = [p for s, p in conn.sql if s.startswith("INSERT INTO notification_channels")]
    assert channel == [(5, "E-mail (jeanne@example.org)", '{"to":["jeanne@example.org"]}')] or \
        channel[0][2].replace(" ", "") == '{"to":["jeanne@example.org"]}'
    monitors = [p for s, p in conn.sql if s.startswith("INSERT INTO monitors")]
    assert len(monitors) == 6 and all(p[6] == [101] for p in monitors)
    assert any("default_monitors" in s for s, _ in conn.sql if s.startswith("UPDATE orgs"))
    # Auto-hébergé sans e-mail : les moniteurs sans canal.
    conn = Conn()
    out = asyncio.run(defaults.provision_org(conn, 5, None, None))
    assert out["channel_id"] is None and len(out["monitor_ids"]) == 6
    # Déjà posés : rien.
    conn = Conn(done=True)
    assert asyncio.run(defaults.provision_org(conn, 5, 9, "jeanne@example.org")) == {"channel_id": None, "monitor_ids": []}
    assert not any(s.startswith("INSERT") for s, _ in conn.sql)


def test_problem_only_checks():
    assert ev.PROBLEM_ONLY_CHECKS == {"systemd.unit", "windows.service"}
