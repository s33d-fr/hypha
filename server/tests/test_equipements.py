"""Modèles d'analyse des équipements réseau et de sécurité (vague 4).

Lignes d'exemple tirées de documentations publiques, citées à chaque bloc. Trois natures, dites à chaque fois :
- « éditeur » : copiée telle quelle de la documentation (ou du site d'assistance) de l'éditeur ;
- « tiers » : copiée d'un guide d'intégration public d'un autre éditeur (SIEM), faute d'exemple chez l'éditeur ;
- « construite » : écrite sur le format ou l'ordre des champs documenté par l'éditeur, valeurs d'exemple
  (adresses RFC 5737), faute de ligne complète publiée.
Les modèles sont éprouvés sur ces exemples, pas sur les équipements réels.
"""
import json

from hypha.ingest import body as B
from hypha.ingest import equipements as E
from hypha.ingest import pipelines as P

TS = 1_800_000_000_000


def syslog_row(message, parser, status="info"):
    attrs = {"syslog": {"facility": "local7", "severity": 6, "parser": parser},
             "network": {"client": {"ip": "192.0.2.254"}}}
    return B.log_row(1, 10, "agent-01", TS, message, status, "syslog", "syslog", attrs, {"syslog_host": "fw01"}, None, None)


def attrs_of(r):
    return json.loads(r[B.LOG_COLUMNS.index("attributes")])


def status_of(r):
    return r[B.LOG_COLUMNS.index("status")]


def test_registry_lists_every_model():
    for name in ("kv", "fortinet", "stormshield", "pfsense", "opnsense", "paloalto", "cisco-asa", "sonicwall",
                 "watchguard", "checkpoint", "mikrotik", "ubiquiti"):
        assert name in P.DEVICE_PARSERS, name
    for name, fn in P.DEVICE_PARSERS.items():
        assert fn("") is None, name
        assert fn("ligne sans rapport avec un équipement") is None, name


# ── Fortinet FortiGate ─────────────────────────────────────────────────────────
# Éditeur : FortiOS 7.0.4 Administration Guide, « Sample logs by log type »
# https://docs.fortinet.com/document/fortigate/7.0.4/administration-guide/986892/sample-logs-by-log-type

FGT_FORWARD = (
    'date=2019-05-10 time=11:37:47 logid="0000000013" type="traffic" subtype="forward" level="notice" vd="vdom1" '
    'eventtime=1557513467369913239 srcip=10.1.100.11 srcport=58012 srcintf="port12" srcintfrole="undefined" '
    'dstip=23.59.154.35 dstport=80 dstintf="port11" dstintfrole="undefined" srcuuid="ae28f494-5735-51e9-f247-d1d2ce663f4b" '
    'dstuuid="ae28f494-5735-51e9-f247-d1d2ce663f4b" poluuid="ccb269e0-5735-51e9-a218-a397dd08b7eb" sessionid=105048 '
    'proto=6 action="close" policyid=1 policytype="policy" service="HTTP" dstcountry="Canada" srccountry="Reserved" '
    'trandisp="snat" transip=172.16.200.2 transport=58012 appid=34050 app="HTTP.BROWSER_Firefox" appcat="Web.Client" '
    'apprisk="elevated" applist="g-default" duration=116 sentbyte=1188 rcvdbyte=1224 sentpkt=17 rcvdpkt=16 '
    'utmaction="allow" countapp=1 osname="Ubuntu" mastersrcmac="a2:e9:00:ec:40:01" srcmac="a2:e9:00:ec:40:01" '
    'srcserver=0 utmref=65500-742')
FGT_LOCAL = (
    'date=2019-05-10 time=11:50:48 logid="0001000014" type="traffic" subtype="local" level="notice" vd="vdom1" '
    'eventtime=1557514248379911176 srcip=172.16.200.254 srcport=62024 srcintf="port11" srcintfrole="undefined" '
    'dstip=172.16.200.2 dstport=443 dstintf="vdom1" dstintfrole="undefined" sessionid=107478 proto=6 '
    'action="server-rst" policyid=0 policytype="local-in-policy" service="HTTPS" dstcountry="Reserved" '
    'srccountry="Reserved" trandisp="noop" app="Web Management(HTTPS)" duration=5 sentbyte=1247 rcvdbyte=1719 '
    'sentpkt=5 rcvdpkt=6 appcat="unscanned"')
FGT_ADMIN_OK = (
    'date=2019-05-13 time=11:20:54 logid="0100032001" type="event" subtype="system" level="information" vd="vdom1" '
    'eventtime=1557771654587081441 logdesc="Admin login successful" sn="1557771654" user="admin" '
    'ui="ssh(172.16.200.254)" method="ssh" srcip=172.16.200.254 dstip=172.16.200.2 action="login" status="success" '
    'reason="none" profile="super_admin" msg="Administrator admin logged in successfully from ssh(172.16.200.254)"')
FGT_VPN = (
    'date=2019-05-13 time=14:21:42 logid="0101037127" type="event" subtype="vpn" level="notice" vd="root" '
    'eventtime=1557782502722231889 logdesc="Progress IPsec phase 1" msg="progress IPsec phase 1" action="negotiate" '
    'remip=50.1.1.101 locip=50.1.1.100 remport=500 locport=500 outintf="port14" '
    'cookies="9091f4d4837ea71c/0000000000000000" user="N/A" group="N/A" xauthuser="N/A" xauthgroup="N/A" '
    'assignip=N/A vpntunnel="test" status="success" init="local" mode="main" dir="outbound" stage=1 '
    'role="initiator" result="OK"')
FGT_HA = (
    'date=2019-05-10 time=09:53:18 logid="0108037894" type="event" subtype="ha" level="critical" vd="root" '
    'eventtime=1557507199208575235 logdesc="Virtual cluster member joined" msg="Virtual cluster detected member join" '
    'vcluster=1 ha_group=0 sn="FG2K5E3916900286"')
FGT_VIRUS = (
    'date=2019-05-13 time=11:45:03 logid="0211008192" type="utm" subtype="virus" eventtype="infected" level="warning" '
    'vd="vdom1" eventtime=1557773103767393505 msg="File is infected." action="blocked" service="HTTP" sessionid=359260 '
    'srcip=10.1.100.11 dstip=172.16.200.55 srcport=60446 dstport=80 srcintf="port12" srcintfrole="undefined" '
    'dstintf="port11" dstintfrole="undefined" policyid=4 proto=6 direction="incoming" filename="eicar.com" '
    'quarskip="File-was-not-quarantined." virus="EICAR_TEST_FILE" dtype="Virus" '
    'ref="http://www.fortinet.com/ve?vn=EICAR_TEST_FILE" virusid=2172 url="http://172.16.200.55/virus/eicar.com" '
    'profile="g-default" agent="curl/7.47.0" '
    'analyticscksum="275a021bbfb6489e54d471899f7db9d1663fc695ec2fe2a2c4538aabf651fd0f" analyticssubmit="false" '
    'crscore=50 craction=2 crlevel="critical"')
FGT_IPS = (
    'date=2019-05-15 time=17:56:41 logid="0419016384" type="utm" subtype="ips" eventtype="signature" level="alert" '
    'vd="root" eventtime=1557968201 severity="critical" srcip=10.1.100.22 srccountry="Reserved" dstip=172.16.200.55 '
    'srcintf="port10" srcintfrole="lan" dstintf="port9" dstintfrole="wan" sessionid=4017 action="dropped" proto=6 '
    'service="HTTP" policyid=1 attack="Adobe.Flash.newfunction.Handling.Code.Execution" srcport=46810 dstport=80 '
    'hostname="172.16.200.55" url="/ips/sig1.pdf" direction="incoming" attackid=23305 profile="block-critical-ips" '
    'ref="http://www.fortinet.com/ids/VID23305" incidentserialno=582633933 '
    'msg="applications3: Adobe.Flash.newfunction.Handling.Code.Execution," crscore=50 craction=4096 '
    'crlevel="critical"')
# Éditeur (site d'assistance) : Fortinet Community, « Technical Tip: How to check failed admin logins »
# https://community.fortinet.com/t5/FortiGate/Technical-Tip-How-to-check-failed-admin-logins-from-the-GUI-and/ta-p/329141
FGT_ADMIN_FAIL = (
    'date=2024-07-30 time=09:12:22 eventtime=1722345142416647315 tz="-0400" logid="0100032002" type="event" '
    'subtype="system" level="alert" vd="root" logdesc="Admin login failed" sn="0" user="testlogin" '
    'ui="https(172.25.181.118)" method="https" srcip=172.25.181.118 dstip=10.9.11.34 action="login" status="failed" '
    'reason="name_invalid" msg="Administrator testlogin login failed from https(172.25.181.118) because of invalid '
    'user name"')
# Construite : fiche 23102 LOG_ID_IPSEC_TUNNEL_DOWN (« IPsec VPN tunnel down », type event, catégorie vpn)
# https://docs.fortinet.com/document/fortigate/7.6.6/fortios-log-message-reference/23102
FGT_TUNNEL_DOWN = ('date=2026-11-20 time=03:00:00 logid="0101023102" type="event" subtype="vpn" level="information" '
                   'vd="root" logdesc="IPsec VPN tunnel down" remip=198.51.100.7 vpntunnel="site-b"')


def test_fortinet_traffic():
    a = E.parse_fortinet(FGT_FORWARD)
    assert a["evt.category"] == "traffic" and a["evt.name"] == "forward" and a["fw.action"] == "allow"
    assert (a["network.source.ip"], a["network.source.port"]) == ("10.1.100.11", 58012)
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("23.59.154.35", 80)
    assert a["network.protocol"] == "tcp" and a["network.interface.in"] == "port12" and a["network.interface.out"] == "port11"
    assert a["fw.rule"] == "1" and a["device.severity"] == "notice" and a["device.vendor"] == "Fortinet"
    assert a["fortinet.logid"] == "0000000013" and a["fortinet.sessionid"] == 105048 and "status" not in a
    a = E.parse_fortinet(FGT_LOCAL)
    assert a["fw.action"] == "allow" and a["fw.rule"] == "0" and a["network.destination.port"] == 443


def test_fortinet_admin_vpn_system():
    a = E.parse_fortinet(FGT_ADMIN_OK)
    assert a["evt.category"] == "admin" and a["evt.name"] == "admin_login" and a["evt.outcome"] == "success"
    assert a["usr.name"] == "admin" and a["network.source.ip"] == "172.16.200.254" and "status" not in a
    a = E.parse_fortinet(FGT_ADMIN_FAIL)
    assert a["evt.outcome"] == "failure" and a["status"] == "warning" and a["usr.name"] == "testlogin"
    a = E.parse_fortinet(FGT_VPN)
    assert a["evt.category"] == "vpn" and a["vpn.tunnel"] == "test" and a["vpn.peer"] == "50.1.1.101"
    assert a["evt.outcome"] == "success" and "vpn.status" not in a and "usr.name" not in a
    a = E.parse_fortinet(FGT_TUNNEL_DOWN)
    assert a["vpn.status"] == "down" and a["status"] == "warning" and a["vpn.tunnel"] == "site-b"
    a = E.parse_fortinet(FGT_HA)
    assert a["evt.category"] == "system" and a["evt.name"] == "ha" and a["device.severity"] == "critical"


def test_fortinet_threats():
    a = E.parse_fortinet(FGT_VIRUS)
    assert a["evt.category"] == "threat" and a["threat.name"] == "EICAR_TEST_FILE" and a["threat.type"] == "virus"
    assert a["fw.action"] == "deny" and a["status"] == "warning"
    a = E.parse_fortinet(FGT_IPS)
    assert a["threat.name"] == "Adobe.Flash.newfunction.Handling.Code.Execution" and a["status"] == "error"
    assert a["fw.action"] == "deny" and a["network.interface.in"] == "port10"


def test_fortinet_through_ingestion():
    r = P.process_row(syslog_row(FGT_IPS, "fortinet"))
    a = attrs_of(r)
    assert status_of(r) == "error" and a["evt"]["category"] == "threat"
    assert a["network"]["source"]["ip"] == "10.1.100.22" and a["network"]["client"]["ip"] == "192.0.2.254"
    assert a["syslog"]["parser"] == "fortinet" and a["fortinet"]["attackid"] == 23305
    # Le statut posé par l'équipement n'est jamais abaissé.
    assert status_of(P.process_row(syslog_row(FGT_VIRUS, "fortinet", status="critical"))) == "critical"


# ── Stormshield SNS ────────────────────────────────────────────────────────────
# Éditeur : SNS v4, « Understanding audit logs »
# https://documentation.stormshield.eu/SNS/v4/en/Content/Description_of_Audit_logs/Understand_log_files.htm
SNS_ALARM = ('id=firewall time="2019-01-27 13:24:28" fw="V50XXA0G0000002" tz=+0000 startime="2011-01-27 13:24:28" '
             'pri=4 srcif="Ethernet0" srcifname="out" ipproto=tcp proto=ssh src=192.168.0.1 srcport=54937 '
             'srcportname=ephemeral_fw dst=192.168.1.1 dstport=22 dstportname=ssh dstname=Firewall_out action=pass '
             'msg="Interactive connection detected" class=protocol classification=0 alarmid=85')
SNS_DATE = ('id=firewall time="2019-01-01 01:00:00" fw="U800SXXXXXXXXXX" tz=+0100 startime="2019-01-01 01:00:17" '
            'datechange=1 duration=-18')
# Tiers : FortiSIEM External Systems Configuration Guide, « Stormshield Network Security »
# https://docs.fortinet.com/document/fortisiem/6.2.1/external-systems-configuration-guide/990763
SNS_FILTER = ('id=firewall time="2019-02-24 16:38:01" fw="SN310A17B0323A7" tz=+0100 startime="2019-02-24 16:38:00" '
              'pri=5 confid=00 slotlevel=2 ruleid=4 rulename="1690fb96019_7" srcif="Ethernet0" srcifname="out" '
              'ipproto=udp proto=ssdp src=10.11.11.11 srcport=49907 srcportname=ephemeral_fw_udp srcname=skywalker '
              'srcmac=11:11:11:11:11:11 dst=10.10.10.10 dstport=1900 dstportname=sdp ipv=4 sent=0 rcvd=0 '
              'duration=0.00 action=pass logtype="filter"')


def test_stormshield():
    a = E.parse_stormshield(SNS_ALARM)
    assert a["evt.category"] == "threat" and a["threat.name"] == "Interactive connection detected"
    assert a["threat.type"] == "protocol" and a["status"] == "warning" and a["fw.action"] == "allow"
    assert a["network.protocol"] == "tcp" and a["network.destination.port"] == 22 and a["network.interface.in"] == "out"
    assert a["device.name"] == "V50XXA0G0000002" and a["stormshield.alarmid"] == 85
    a = E.parse_stormshield(SNS_FILTER)
    assert a["evt.category"] == "traffic" and a["evt.name"] == "filter" and a["fw.action"] == "allow"
    assert a["fw.rule"] == "1690fb96019_7" and a["network.protocol"] == "udp"
    assert (a["network.source.ip"], a["network.destination.ip"], a["network.destination.port"]) == (
        "10.11.11.11", "10.10.10.10", 1900)
    a = E.parse_stormshield(SNS_DATE)
    assert a["evt.category"] == "system" and "status" not in a


# ── pfSense et OPNsense (filterlog) ────────────────────────────────────────────
# Éditeur : Netgate, « Viewing the Firewall Log » (ligne coupée en deux dans la page, recollée)
# https://docs.netgate.com/pfsense/en/latest/monitoring/logs/firewall.html
PFS_BLOCK = ("Aug  3 08:59:02 master filterlog: 5,16777216,,1000000103,igb1,match,block,in,4,0x10,,128,0,0,"
             "none,17,udp,328,198.51.100.1,198.51.100.2,67,68,308")
# Construites : grammaire « Raw Filter Log Format » de Netgate (champs communs, IPv4/IPv6, TCP)
# https://docs.netgate.com/pfsense/en/latest/monitoring/logs/raw-filter-format.html
PFS_TCP = "4,,,1000000003,em0,match,pass,out,4,0x0,,64,12345,0,DF,6,tcp,60,192.0.2.10,198.51.100.20,51515,443,0,S,1234567890,,64240,,mss"
PFS_V6 = "1,,,1000000001,em1,match,block,in,6,0x00,0x00000,64,udp,17,72,2001:db8::1,2001:db8::2,5353,5353,32"
# Éditeur (forum public de Netgate, pas la documentation) : connexion refusée à l'interface d'administration
# https://forum.netgate.com/post/1120764
PFS_AUTH = ("Sep 20 16:02:08 webproxy php-fpm[736]: /index.php: webConfigurator authentication error for user "
            "'admin' from: 10.254.201.100")


def test_pfsense_filterlog():
    a = E.parse_pfsense(PFS_BLOCK)
    assert a["device.product"] == "pfSense" and a["evt.category"] == "traffic" and a["fw.action"] == "deny"
    assert a["fw.rule"] == "1000000103" and a["network.interface.in"] == "igb1" and a["network.protocol"] == "udp"
    assert (a["network.source.ip"], a["network.source.port"]) == ("198.51.100.1", 67)
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("198.51.100.2", 68)
    assert a["filterlog.reason"] == "match" and a["filterlog.ttl"] == 128
    # Le message tel que l'agent le rend (étiquette filterlog retirée) donne la même chose.
    assert E.parse_pfsense(PFS_BLOCK.split("filterlog: ")[1])["network.source.ip"] == "198.51.100.1"
    a = E.parse_opnsense(PFS_TCP)
    assert a["device.product"] == "OPNsense" and a["fw.action"] == "allow" and a["network.interface.out"] == "em0"
    assert a["network.protocol"] == "tcp" and a["network.destination.port"] == 443 and a["filterlog.tcp_flags"] == "S"
    a = E.parse_pfsense(PFS_V6)
    assert a["network.source.ip"] == "2001:db8::1" and a["network.destination.port"] == 5353 and a["fw.action"] == "deny"


def test_pfsense_admin_login():
    a = E.parse_pfsense(PFS_AUTH)
    assert a["evt.category"] == "admin" and a["evt.outcome"] == "failure" and a["status"] == "warning"
    assert a["usr.name"] == "admin" and a["network.source.ip"] == "10.254.201.100"


# ── Palo Alto Networks PAN-OS ──────────────────────────────────────────────────
# Construites : ordre des champs de PAN-OS 10.2 (Traffic, Threat, System, GlobalProtect Log Fields)
# https://docs.paloaltonetworks.com/pan-os/10-2/pan-os-admin/monitoring/use-syslog-for-monitoring/syslog-field-descriptions/traffic-log-fields
# (et …/threat-log-fields, …/system-log-fields, …/globalprotect-log-fields). Aucune ligne complète n'y est publiée.

def pan(fields: dict, n: int) -> str:
    row = [""] * n
    for i, v in fields.items():
        row[i] = v
    return ",".join(f'"{v}"' if "," in v else v for v in row)


COMMON = {0: "1", 1: "2026/11/20 03:00:00", 2: "012801000001", 5: "2049", 6: "2026/11/20 03:00:00"}
PAN_TRAFFIC = pan(COMMON | {3: "TRAFFIC", 4: "end", 7: "192.0.2.10", 8: "198.51.100.20", 11: "web-sortant",
                            12: "acme\\alice", 14: "ssl", 15: "vsys1", 16: "trust", 17: "untrust", 18: "ethernet1/2",
                            19: "ethernet1/1", 22: "34512", 24: "51514", 25: "443", 29: "tcp", 30: "allow",
                            31: "5823", 46: "tcp-fin", 52: "PA-440"}, 60)
PAN_DENY = pan(COMMON | {3: "TRAFFIC", 4: "deny", 7: "203.0.113.9", 8: "192.0.2.10", 11: "interdit", 24: "40000",
                         25: "22", 29: "tcp", 30: "deny"}, 60)
PAN_THREAT = pan(COMMON | {3: "THREAT", 4: "vulnerability", 7: "203.0.113.9", 8: "192.0.2.10", 11: "entrant",
                           24: "40001", 25: "445", 29: "tcp", 30: "reset-both", 31: "",
                           32: "Essai HYPHA(99999)", 33: "any", 34: "critical", 35: "client-to-server", 59: "PA-440"}, 70)
PAN_SYS_AUTH = pan(COMMON | {3: "SYSTEM", 4: "auth", 8: "auth-fail", 12: "general", 13: "medium",
                             14: "failed authentication for user 'admin'. Reason: Invalid username/password. "
                                 "From: 192.0.2.50.", 22: "PA-440"}, 26)
PAN_GP = pan(COMMON | {3: "GLOBALPROTECT", 4: "", 8: "gateway-auth", 9: "login", 10: "LDAP", 11: "IPSec",
                       12: "alice", 15: "198.51.100.77", 26: "Authentication failed, invalid password",
                       28: "failure"}, 50)


def test_paloalto_traffic_and_threat():
    a = E.parse_paloalto(PAN_TRAFFIC)
    assert a["evt.category"] == "traffic" and a["evt.name"] == "end" and a["fw.action"] == "allow"
    assert (a["network.source.ip"], a["network.source.port"], a["network.destination.port"]) == ("192.0.2.10", 51514, 443)
    assert a["fw.rule"] == "web-sortant" and a["usr.name"] == "acme\\alice" and a["network.interface.in"] == "ethernet1/2"
    assert a["device.name"] == "PA-440" and a["paloalto.application"] == "ssl" and a["paloalto.zone_from"] == "trust"
    assert E.parse_paloalto(PAN_DENY)["fw.action"] == "deny"
    a = E.parse_paloalto(PAN_THREAT)
    assert a["evt.category"] == "threat" and a["threat.name"] == "Essai HYPHA(99999)" and a["status"] == "error"
    assert a["fw.action"] == "deny" and a["threat.type"] == "vulnerability" and a["device.name"] == "PA-440"
    # Derrière un en-tête BSD que l'agent n'aurait pas retiré.
    assert E.parse_paloalto("<14>Nov 20 03:00:00 PA-440 " + PAN_DENY)["network.source.ip"] == "203.0.113.9"


def test_paloalto_system_and_globalprotect():
    a = E.parse_paloalto(PAN_SYS_AUTH)
    assert a["evt.category"] == "admin" and a["evt.outcome"] == "failure" and a["status"] == "warning"
    assert a["usr.name"] == "admin" and a["network.source.ip"] == "192.0.2.50"
    a = E.parse_paloalto(PAN_GP)
    assert a["evt.category"] == "vpn" and a["evt.outcome"] == "failure" and a["usr.name"] == "alice"
    assert a["network.source.ip"] == "198.51.100.77" and a["status"] == "warning"


# ── Cisco ASA ──────────────────────────────────────────────────────────────────
# Éditeur : Cisco Security Center, « Identify Incidents Using Firewall and IOS Router Syslog Events »
# (lignes coupées dans la page, recollées) https://sec.cloudapps.cisco.com/security/center/resources/identify_incidents_via_syslog
ASA_DENY = ('Aug 24 2007 08:54:48: %ASA-4-106023: Deny tcp src outside:192.168.208.63/46857 dst '
            'inside:192.168.150.77/443 by access-group "OUTSIDE" [0x5063b82f, 0x0]')
ASA_DENY_ICMP = ('Aug 24 2007 09:16:41: %ASA-4-106023: Deny icmp src outside:192.168.208.63 dst inside:192.168.150.77 '
                 '(type 8, code 0) by access-group "OUTSIDE" [0xd3f63b90, 0x0]')
ASA_ACL = ('Aug 24 2007 09:02:37: %ASA-6-106100: access-list OUTSIDE denied tcp outside/192.168.208.63(51587) -> '
           'inside/192.168.150.77(104) hit-cnt 1 first hit [0x22e8ac21, 0x0]')
ASA_NOCONN = ('Aug 24 2007 09:16:14: %ASA-6-106015: Deny TCP (no connection) from 192.168.150.65/2278 to '
              '64.101.128.83/80 flags RST on interface inside')
ASA_RPF = ('Aug 24 2007 09:17:32: %ASA-1-106021: Deny ICMP reverse path check from 192.168.150.60 to 192.168.2.1 on '
           'interface outside')
ASA_BUILT = ('Aug 24 2007 11:15:16: %ASA-6-302013: Built inbound TCP connection 733280 for '
             'outside:192.168.208.63/51606 (192.168.208.63/51606) to inside:192.168.150.70/80 (192.168.150.70/80)')
ASA_TEARDOWN = ('Aug 24 2007 11:15:16: %ASA-6-302014: Teardown TCP connection 733280 for outside:192.168.208.63/51606 '
                'to inside:192.168.150.70/80 duration 0:00:00 bytes 0 TCP Reset-O')
ASA_ICMP = ('Aug 24 2007 10:27:22: %ASA-6-302020: Built ICMP connection for faddr 192.168.208.63/15343 gaddr '
            '192.168.150.70/0 laddr 192.168.150.70/0')
ASA_IDS = ('Aug 24 2007 10:07:47: %ASA-4-400014: IDS:2004 ICMP echo request from 192.168.208.63 to 192.168.150.77 on '
           'interface outside')
ASA_OTHER = ('Aug 24 2007 08:54:31: %ASA-4-500004: Invalid transport field for protocol=TCP, from '
             '192.168.208.63/46855 to 192.168.150.77/0')
# Construites : gabarits du « Cisco Secure Firewall ASA Series Syslog Messages » (113015, 113005, 113019)
# https://www.cisco.com/c/en/us/td/docs/security/asa/syslog/asa-syslog/syslog-messages-101001-to-199021.html
ASA_AAA_LOCAL = ("%ASA-6-113015: AAA user authentication Rejected : reason = Invalid password : local database : "
                 "user = admin : user IP = 192.0.2.50")
ASA_AAA_SERVER = ("%ASA-6-113005: AAA user authentication Rejected : reason = AAA failure : server = 192.0.2.5 : "
                  "user =admin : user IP = 192.0.2.51")
ASA_L2L_DOWN = ("%ASA-4-113019: Group = 198.51.100.7, Username = 198.51.100.7, IP = 198.51.100.7, Session "
                "disconnected. Session Type: LAN-to-LAN, Duration: 0h:42m:10s, Bytes xmt: 1200, Bytes rcv: 3400, "
                "Reason: Lost Service")
ASA_RA_END = ("%ASA-4-113019: Group = nomades, Username = alice, IP = 203.0.113.40, Session disconnected. Session "
              "Type: AnyConnect-Parent, Duration: 1h:02m:03s, Bytes xmt: 1, Bytes rcv: 2, Reason: User Requested")


def test_cisco_asa_traffic():
    a = E.parse_cisco_asa(ASA_DENY)
    assert a["evt.category"] == "traffic" and a["fw.action"] == "deny" and a["fw.rule"] == "OUTSIDE"
    assert (a["network.source.ip"], a["network.source.port"]) == ("192.168.208.63", 46857)
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("192.168.150.77", 443)
    assert a["network.interface.in"] == "outside" and a["network.interface.out"] == "inside"
    assert a["device.severity"] == 4 and a["cisco.asa.message_id"] == "106023" and a["evt.name"] == "106023"
    a = E.parse_cisco_asa(ASA_DENY_ICMP)
    assert a["network.protocol"] == "icmp" and "network.source.port" not in a and a["fw.rule"] == "OUTSIDE"
    a = E.parse_cisco_asa(ASA_ACL)
    assert a["fw.action"] == "deny" and a["network.source.port"] == 51587 and a["network.destination.port"] == 104
    a = E.parse_cisco_asa(ASA_NOCONN)
    assert a["fw.action"] == "deny" and a["network.destination.ip"] == "64.101.128.83" and a["network.interface.in"] == "inside"
    a = E.parse_cisco_asa(ASA_RPF)
    assert a["fw.action"] == "deny" and a["network.protocol"] == "icmp" and a["device.severity"] == 1
    a = E.parse_cisco_asa(ASA_BUILT)
    assert a["fw.action"] == "allow" and a["network.source.port"] == 51606 and a["network.destination.port"] == 80
    assert a["cisco.asa.connection_id"] == 733280
    a = E.parse_cisco_asa(ASA_TEARDOWN)
    assert a["evt.name"] == "302014" and a["network.destination.ip"] == "192.168.150.70"
    a = E.parse_cisco_asa(ASA_ICMP)
    assert a["network.protocol"] == "icmp" and a["network.source.ip"] == "192.168.208.63"
    a = E.parse_cisco_asa(ASA_OTHER)
    assert a["evt.category"] == "system" and a["evt.name"] == "500004" and "status" not in a


def test_cisco_asa_threat_admin_vpn():
    a = E.parse_cisco_asa(ASA_IDS)
    assert a["evt.category"] == "threat" and a["threat.name"] == "IDS:2004 ICMP echo request" and a["status"] == "warning"
    a = E.parse_cisco_asa(ASA_AAA_LOCAL)
    assert a["evt.category"] == "admin" and a["evt.outcome"] == "failure" and a["usr.name"] == "admin"
    assert a["network.source.ip"] == "192.0.2.50" and a["status"] == "warning" and a["cisco.asa.reason"] == "Invalid password"
    a = E.parse_cisco_asa(ASA_AAA_SERVER)
    assert a["usr.name"] == "admin" and a["network.source.ip"] == "192.0.2.51"
    a = E.parse_cisco_asa(ASA_L2L_DOWN)
    assert a["evt.category"] == "vpn" and a["vpn.status"] == "down" and a["status"] == "warning"
    assert a["vpn.peer"] == "198.51.100.7" and a["cisco.asa.reason"] == "Lost Service"
    a = E.parse_cisco_asa(ASA_RA_END)
    assert a["evt.category"] == "vpn" and "vpn.status" not in a and "status" not in a and a["usr.name"] == "alice"


# ── SonicWall ──────────────────────────────────────────────────────────────────
# Tiers : FortiSIEM External Systems Configuration Guide, « Dell SonicWALL Firewall » (ligne recollée)
# https://docs.fortinet.com/document/fortisiem/6.3.2/external-systems-configuration-guide/887765
SW_OPEN = ('Jan  3 13:45:36 192.168.5.1 id=firewall sn=000SERIAL time="2007-01-03 14:48:06" fw=1.1.1.1 pri=6 '
           'c=262144 m=98 msg="Connection Opened" n=23419 src=2.2.2.2:36701:WAN dst=1.1.1.1:50000:WAN '
           'proto=tcp/50000')
# Tiers : Juniper JSA DSM Configuration Guide, « SonicWALL Sample Event Messages » (tel que publié, espaces de mise
# en page compris) https://www.juniper.net/documentation/us/en/software/jsa7.5.0/jsa-dsm/topics/concept/jsa-configuring-dsm-sample-event-message-sonicwall.html
SW_SCAN = ('id=firewall sn=01234567ABCD time=" 2018-11-07 11:16:02 " fw=10.0.0.2 pri=1 c=32 m= 83 '
           'msg="Probable port scan detected" n=2 src= 10.0.0.3 : 443 :X1 dst= 172.16.194.2 : 47379 :X1 '
           'srcMac= 00:00:5E:00:53:ff dstMac= 00:00:5E:00:53:00 proto=tcp/1 note="TCP scanned port list, 14551, '
           '61968, 53577, 27976, 29050, 25330, 21761, 23903, 7412, 47379" fw_action="NA"')


def test_sonicwall():
    a = E.parse_sonicwall(SW_OPEN)
    assert a["evt.category"] == "traffic" and a["evt.name"] == "98" and a["fw.action"] == "allow"
    assert (a["network.source.ip"], a["network.source.port"], a["network.interface.in"]) == ("2.2.2.2", 36701, "WAN")
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("1.1.1.1", 50000)
    assert a["network.protocol"] == "tcp" and a["device.severity"] == 6 and a["sonicwall.sn"] == "000SERIAL"
    a = E.parse_sonicwall(SW_SCAN)
    assert a["evt.category"] == "threat" and a["threat.name"] == "Probable port scan detected"
    assert a["threat.type"] == "scan" and a["status"] == "error"


# ── WatchGuard Firebox ─────────────────────────────────────────────────────────
# Éditeur : Fireware v12.12 Log Catalog (colonne « Log Message Example », recollée)
# https://www.watchguard.com/help/docs/fireware/12/en-US/log_catalog/12_12_Log-Catalog.pdf
WG_ALLOW = "Allow Firebox 0-External 52 tcp 20 127 10.0.1.2 206.190.60.138 62443 80 offset 8 S 832026162 win 8192 (HTTP-00)"
WG_IPS = ('Deny 1-Trusted 0-External 1440 tcp 20 61 10.0.1.2 192.168.130.126 55810 80 offset 5 A 447868619 win 54 '
          'signature_name="EXPLOIT Apple QuickTime FLIC Animation file buffer overflow -1-2" signature_cat="Misc" '
          'signature_id="1112464" severity="4" sig_vers="18.124" msg="IPS detected" (HTTP-00)')
WG_AUTH_FAIL = ("Authentication of Firewall user [test@RADIUS] from 10.0.1.2 was rejected, received an Access-Reject "
                "response from the RADIUS server")
WG_AUTH_OK = "Authentication of firewall user [user1@Firebox-DB] from 198.51.100.2 was accepted"
WG_LOCK = "User test is locked out briefly after 3 login failures"
# Éditeur : Fireware Help, « Read a Log Message » (vue Traffic Monitor)
# https://www.watchguard.com/help/docs/help-center/en-US/Content/en-US/Fireware/logging/read_log-msg.html
WG_MON = ('2024-04-01 23:39:46 Deny 10.0.1.131 10.0.1.1 echo-request/icmp Trusted Firebox Denied 84 64 (Ping-00) '
          'proc_id="firewall" rc="101" msg_id="3000-0148" type="8" duration="0" sent_pkts="1" rcvd_pkts="0" '
          'sent_bytes="84" rcvd_bytes="0"')
# Tiers : Graylog Illuminate, « WatchGuard Firebox Content Pack » (forme syslog)
# https://go2docs.graylog.org/illuminate-current/content_packs/watchguard_firebox_content_pack.htm
WG_SYSLOG = ('DEV01 0011223344556 (2022-09-01T13:58:33) firewall: msg_id="3000-0148" Allow External Inside 44 tcp 20 '
             '238 10.11.12.13 192.168.1.10 58325 60951 offset 6 S 3172487743 win 4  geo_src="USA"  geo_dst="USA"  '
             '(Inbound Policy-00)')


def test_watchguard_traffic_and_threat():
    a = E.parse_watchguard(WG_ALLOW)
    assert a["evt.category"] == "traffic" and a["fw.action"] == "allow" and a["fw.rule"] == "HTTP-00"
    assert a["network.interface.in"] == "Firebox" and a["network.interface.out"] == "0-External"
    assert (a["network.source.ip"], a["network.destination.ip"], a["network.destination.port"]) == (
        "10.0.1.2", "206.190.60.138", 80)
    a = E.parse_watchguard(WG_IPS)
    assert a["evt.category"] == "threat" and a["fw.action"] == "deny" and a["status"] == "error"
    assert a["threat.name"] == "EXPLOIT Apple QuickTime FLIC Animation file buffer overflow -1-2"
    a = E.parse_watchguard(WG_MON)
    assert a["fw.action"] == "deny" and a["network.protocol"] == "icmp" and a["fw.rule"] == "Ping-00"
    assert a["evt.name"] == "3000-0148" and a["network.interface.in"] == "Trusted"
    a = E.parse_watchguard(WG_SYSLOG)
    assert a["fw.action"] == "allow" and a["fw.rule"] == "Inbound Policy-00" and a["network.source.port"] == 58325
    assert a["watchguard.geo_src"] == "USA" and a["network.interface.in"] == "External"


def test_watchguard_authentication():
    a = E.parse_watchguard(WG_AUTH_FAIL)
    assert a["evt.category"] == "system" and a["evt.name"] == "user_auth" and a["evt.outcome"] == "failure"
    assert a["status"] == "warning" and a["usr.name"] == "test" and a["network.source.ip"] == "10.0.1.2"
    a = E.parse_watchguard(WG_AUTH_OK)
    assert a["evt.outcome"] == "success" and "status" not in a
    a = E.parse_watchguard(WG_AUTH_OK.replace("firewall user", "Management user"))
    assert a["evt.category"] == "admin" and a["evt.name"] == "admin_login"
    a = E.parse_watchguard(WG_LOCK)
    assert a["evt.name"] == "account_locked" and a["usr.name"] == "test" and a["status"] == "warning"


# ── Check Point (Log Exporter) ─────────────────────────────────────────────────
# Construites : format « syslog » du Log Exporter (corps entre crochets, champs séparés par « ; », clé:"valeur",
# échappements \" \\ \]) et en-tête CEF, d'après le Log Exporter Administration Guide
# https://sc1.checkpoint.com/documents/Log_Exporter/EN/CP_Log_Exporter_AdminGuide.pdf
# Noms de champs (src, dst, s_port, service, proto, ifdir, ifname, origin, product, action) : même guide et
# « LEA Fields » de Check Point. Aucune ligne complète publiée accessible.
CP_DROP = ('<134>1 2026-11-20T03:00:00Z gw-paris CheckPoint 25031 - [action:"Drop"; flags:"411908"; '
           'ifdir:"inbound"; ifname:"eth1"; origin:"192.0.2.1"; dst:"198.51.100.20"; proto:"6"; '
           'rule_name:"Bloquer \\"SSH\\""; s_port:"51514"; service:"22"; src:"203.0.113.9"; '
           'product:"VPN-1 & FireWall-1"]')
CP_IPS = ('[action:"Prevent"; origin:"192.0.2.1"; product:"IPS"; protection_name:"Essai HYPHA"; severity:"Critical"; '
          'src:"203.0.113.9"; dst:"192.0.2.10"; service:"445"; proto:"6"]')
CP_LOGIN = '[action:"Failed Log In"; administrator:"admin"; product:"SmartConsole"; src:"192.0.2.50"; status:"Failure"]'
CP_CEF = ('CEF:0|Check Point|VPN-1 & FireWall-1|Check Point|Log|https|Unknown|act=Accept deviceDirection=1 '
          'src=192.0.2.10 spt=51515 dst=198.51.100.20 dpt=443 proto=6')


def test_checkpoint():
    a = E.parse_checkpoint(CP_DROP)
    assert a["evt.category"] == "traffic" and a["fw.action"] == "deny" and a["fw.rule"] == 'Bloquer "SSH"'
    assert (a["network.source.ip"], a["network.source.port"], a["network.destination.port"]) == ("203.0.113.9", 51514, 22)
    assert a["network.interface.in"] == "eth1" and a["network.protocol"] == "tcp" and a["device.name"] == "192.0.2.1"
    a = E.parse_checkpoint(CP_IPS)
    assert a["evt.category"] == "threat" and a["threat.name"] == "Essai HYPHA" and a["status"] == "error"
    assert a["fw.action"] == "deny"
    a = E.parse_checkpoint(CP_LOGIN)
    assert a["evt.category"] == "admin" and a["evt.outcome"] == "failure" and a["usr.name"] == "admin"
    assert a["status"] == "warning" and a["network.source.ip"] == "192.0.2.50"
    a = E.parse_checkpoint(CP_CEF)
    assert a["fw.action"] == "allow" and a["network.destination.port"] == 443 and a["device.product"] == "VPN-1 & FireWall-1"
    # « CEF: » pris pour l'étiquette syslog par l'agent : le reste suffit.
    assert E.parse_checkpoint(CP_CEF[4:])["network.source.ip"] == "192.0.2.10"


# ── MikroTik RouterOS ──────────────────────────────────────────────────────────
# Éditeur : RouterOS, page « Log » (affichage de /log print, heure comprise)
# https://help.mikrotik.com/docs/spaces/ROS/pages/328094/Log
MT_LOGIN = "sep/15 09:54:33 system,info,account user admin logged in from 10.1.101.212 via winbox"
MT_LOGOUT = "sep/15 12:42:55 system,info,account user admin logged out from 10.1.101.212 via telnet"
MT_CHANGE = "sep/15 12:34:26 system,info mangle rule added by admin"
MT_FW = ("01:01:58 firewall,info input: in:ether1 out:(none), src-mac 00:21:29:6d:82:07, proto UDP, "
         "10.1.101.1:520->10.1.101.255:520, len 452")
MT_REBOOT = "jan/02/1970 02:00:09 system,info router rebooted"
MT_OSPF = "11:11:43 route,ospf,debug SEND: Hello Packet 10.255.255.1 -> 224.0.0.5 on lo0"


def test_mikrotik():
    a = E.parse_mikrotik(MT_LOGIN)
    assert a["evt.category"] == "admin" and a["evt.outcome"] == "success" and a["usr.name"] == "admin"
    assert a["network.source.ip"] == "10.1.101.212" and a["mikrotik.via"] == "winbox" and a["device.severity"] == "info"
    assert E.parse_mikrotik(MT_LOGIN.split(" ", 2)[2])["usr.name"] == "admin"        # tel qu'envoyé par syslog
    a = E.parse_mikrotik(MT_LOGOUT)
    assert a["evt.name"] == "admin_logout" and "evt.outcome" not in a
    a = E.parse_mikrotik(MT_CHANGE)
    assert a["evt.category"] == "admin" and a["evt.name"] == "config" and a["usr.name"] == "admin"
    a = E.parse_mikrotik(MT_FW)
    assert a["evt.category"] == "traffic" and a["fw.rule"] == "input" and "fw.action" not in a
    assert (a["network.source.ip"], a["network.source.port"]) == ("10.1.101.1", 520)
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("10.1.101.255", 520)
    assert a["network.protocol"] == "udp" and a["network.interface.in"] == "ether1" and "network.interface.out" not in a
    assert a["mikrotik.src_mac"] == "00:21:29:6d:82:07" and a["mikrotik.length"] == 452
    # Construite : la même ligne avec un préfixe de journalisation (log-prefix) sur une règle de rejet.
    a = E.parse_mikrotik(MT_FW.replace("firewall,info input:", "firewall,info DROP-WAN input:"))
    assert a["fw.action"] == "deny" and a["fw.rule"] == "DROP-WAN" and a["mikrotik.chain"] == "input"
    assert E.parse_mikrotik(MT_REBOOT)["evt.category"] == "system"
    a = E.parse_mikrotik(MT_OSPF)
    assert a["evt.name"] == "route" and a["device.severity"] == "debug"


# ── Ubiquiti (UniFi, EdgeOS) ───────────────────────────────────────────────────
# Tiers : Graylog Illuminate, « Ubiquiti UniFi Content Pack » (help.ui.com refuse les lectures automatiques)
# https://go2docs.graylog.org/illuminate-current/content_packs/ubiquiti_unifi_content_pack.htm
UBNT_FW = ('Houston-Office-DM-Pro [LAN_LOCAL-RET-2147483647] DESCR="no rule description" IN=br148 OUT= '
           'MAC=60:22:32:28:41:e5:d0:21:f9:96:fa:d8:08:00 SRC=10.148.153.76 DST=10.148.0.1 LEN=1500 TOS=00 PREC=0x00 '
           'TTL=64 ID=10151 DF PROTO=TCP SPT=33725 DPT=7550 SEQ=270903990 ACK=2412021963 WINDOW=913 ACK URGP=0')
UBNT_IPS = ('pandora CEF:0|Ubiquiti|UniFi Network|9.4.19|201|Threat Detected and Blocked|7|proto=TCP '
            'src=81.181.129.172 spt=54321 dst=192.168.0.233 dpt=443 UNIFIcategory=Security '
            'UNIFIsubCategory=Intrusion Prevention UNIFIhost=Office-UDM-Pro UNIFIdeviceMac=84:78:48:80:0d:86 '
            'UNIFIdeviceName=Office-UDM-Pro UNIFIdeviceModel=UDM-Pro UNIFIdeviceIp=192.168.0.1 UNIFIrisk=medium '
            'UNIFIipsSignature=ET DROP Dshield Block Listed Source group 1 UNIFIipsSignatureId=2402000 '
            'msg=A network intrusion attempt has been detected and blocked.')


def test_ubiquiti():
    a = E.parse_ubiquiti(UBNT_FW)
    assert a["evt.category"] == "traffic" and a["fw.rule"] == "LAN_LOCAL-RET-2147483647" and "fw.action" not in a
    assert (a["network.source.ip"], a["network.source.port"]) == ("10.148.153.76", 33725)
    assert (a["network.destination.ip"], a["network.destination.port"]) == ("10.148.0.1", 7550)
    assert a["network.protocol"] == "tcp" and a["network.interface.in"] == "br148" and "network.interface.out" not in a
    assert a["ubiquiti.description"] == "no rule description" and a["ubiquiti.length"] == 1500
    # Construite : même ligne avec l'étiquette d'une règle de rejet (« -D- ») d'EdgeOS ou d'UniFi.
    assert E.parse_ubiquiti(UBNT_FW.replace("LAN_LOCAL-RET-", "WAN_LOCAL-D-"))["fw.action"] == "deny"
    a = E.parse_ubiquiti(UBNT_IPS)
    assert a["evt.category"] == "threat" and a["fw.action"] == "deny" and a["status"] == "warning"
    assert a["threat.name"] == "ET DROP Dshield Block Listed Source group 1" and a["device.name"] == "Office-UDM-Pro"
    assert (a["network.source.ip"], a["network.destination.port"]) == ("81.181.129.172", 443)
    assert a["ubiquiti.UNIFIipsSignatureId"] == "2402000"
    assert E.parse_ubiquiti(UBNT_IPS.split("CEF:", 1)[1])["threat.type"] == "Intrusion Prevention"


def test_every_model_through_ingestion_keeps_agent_attributes():
    for parser, line in (("stormshield", SNS_ALARM), ("pfsense", PFS_BLOCK), ("paloalto", PAN_THREAT),
                         ("cisco-asa", ASA_DENY), ("sonicwall", SW_SCAN), ("watchguard", WG_IPS),
                         ("checkpoint", CP_IPS), ("mikrotik", MT_LOGIN), ("ubiquiti", UBNT_IPS)):
        r = P.process_row(syslog_row(line, parser))
        a = attrs_of(r)
        assert a["syslog"]["parser"] == parser and a["network"]["client"]["ip"] == "192.0.2.254", parser
        assert a["device"]["vendor"] and a["evt"]["category"], parser
        if a["evt"]["category"] == "threat":
            assert status_of(r) in ("warning", "error"), parser


# ── moniteurs et tableaux au catalogue ─────────────────────────────────────────

def test_monitor_templates_and_dashboards():
    from hypha.api import dashboard_catalogue as CAT
    from hypha.monitors import templates
    from hypha.monitors.validate import normalize_monitor

    for tid in ("fw_deny_spike", "fw_threats", "fw_admin_login_failures", "fw_vpn_down", "fw_vpn_failures"):
        m = normalize_monitor(templates.build(tid, {}))
        assert m["type"] == "log" and m["query"]["q"].startswith("source:syslog @") and m["query"]["by"] == ["@network.client.ip"]
    m = templates.build("fw_deny_spike", {"parser": "Cisco-ASA; DROP", "critical": 50, "warning": 20})
    assert m["query"]["q"] == "source:syslog @syslog.parser:cisco-asadrop @fw.action:deny"
    assert m["name"] == "Pic de refus du pare-feu (cisco-asadrop)" and m["thresholds"] == {"critical": 50, "warning": 20}
    normalize_monitor(templates.build("fw_vpn_down", {"parser": "fortinet"}))
    for parser in P.DEVICE_PARSERS:
        if parser == "kv":
            continue
        d = CAT.get("syslog_" + parser.replace("-", "_"))
        assert d is not None, parser
        streams = [w["options"]["log_query"] for w in d["widgets"] if w["type"] == "log_stream"]
        assert all(f"@syslog.parser:{parser} " in q for q in streams) and len(streams) == 5
        assert {v["name"] for v in d["template_vars"]} == {"host", "device"}
