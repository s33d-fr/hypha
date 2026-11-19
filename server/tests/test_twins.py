"""Jumeaux (§ 51) : ressource de connecteur et hôte à agent d'une même machine."""
from hypha.api.twins import annotate, match


def agent(i, name, ips, serial=None):
    return {"id": i, "hostname": name, "ip_addresses": ips, "serial": serial, "device": {}}


def res(i, name, ips, serial="", display=None):
    return {"id": i, "hostname": name, "ip_addresses": ips, "device": {"serial": serial, "display_name": display}}


AGENTS = [
    agent(1, "dns-01", ["1.2.3.4", "172.16.1.6"], "0123456789"),
    agent(2, "dns-02", ["5.6.7.8", "172.16.1.62"], "To Be Filled By O.E.M."),
    agent(3, "web-01", ["172.16.1.63", "172.17.0.1"], "VMware-42 1a 2b 3c 4d 5e 6f 70-81 92 a3 b4 c5 d6 e7 f8"),
    agent(4, "web-02", ["172.16.1.64", "172.17.0.1"], "0"),
    agent(5, "db-01", ["172.16.10.70"], None),
]


def test_ip_publique_suffit():
    # Serveur OVH : nom de service ≠ nom d'hôte, adresse publique propre à dns-01.
    links = match([res(10, "ns1234567.ip-1-2-3.eu", ["1.2.3.4"], display="mail-01.example.org")], AGENTS)
    assert links == {10: 1}


def test_serie_vmware():
    links = match([res(11, "vm-web", [], serial="VMware-42 1A 2B 3C 4D 5E 6F 70-81 92 A3 B4 C5 D6 E7 F8")], AGENTS)
    assert links == {11: 3}


def test_series_generiques_ecartees():
    # « 0123456789 » est la série de dns-01 : une ressource qui l'aurait ne doit pas s'y rattacher.
    assert match([res(12, "autre", [], serial="0123456789")], AGENTS) == {}
    assert match([res(13, "autre", [], serial="To Be Filled By O.E.M.")], AGENTS) == {}


def test_adresse_privee_demande_le_nom():
    assert match([res(14, "base", ["172.16.10.70"])], AGENTS) == {}
    assert match([res(15, "db-01.example.internal", ["172.16.10.70"])], AGENTS) == {15: 5}


def test_adresse_partagee_ignoree():
    # 172.17.0.1 (pont Docker) est sur deux hôtes : elle n'identifie personne.
    assert match([res(16, "web-01", ["172.17.0.1"])], AGENTS) == {}


def test_deux_hotes_candidats_aucun_lien():
    assert match([res(17, "x", ["1.2.3.4", "5.6.7.8"])], AGENTS) == {}


def test_adresses_vides_ou_invalides():
    assert match([res(18, "vm", ["", "pas-une-ip", "127.0.0.1", "fe80::1"])], AGENTS) == {}


def test_annotate():
    index = {a["id"]: a for a in AGENTS}
    r = res(10, "ns1234567.ip-1-2-3.eu", ["1.2.3.4"], display="mail-01.example.org")
    r["device"].update(provider="ovhcloud", type="dedicated_server")
    index[10] = r
    hosts = [dict(AGENTS[0]), dict(r)]
    annotate(hosts, {10: 1}, index)
    assert hosts[1]["same_as"] == {"id": 1, "hostname": "dns-01"}
    assert hosts[0]["seen_by"][0]["provider"] == "ovhcloud" and hosts[0]["seen_by"][0]["id"] == 10
    assert "seen_by" not in hosts[1] and "same_as" not in hosts[0]
