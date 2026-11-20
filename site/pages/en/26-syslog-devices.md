---
id: syslog-devices
path: /en/docs/syslog-devices/
title: Network and security devices (syslog)
nav: Devices (syslog)
section: Integrations
order: 26
updated: 2026-11-20
description: Send the syslog of a firewall or router (Fortinet, Stormshield, pfSense, OPNsense, Palo Alto, Cisco ASA, SonicWall, WatchGuard, Check Point, MikroTik, Ubiquiti) to a HYPHA agent, and read denials, threats, admin logins and VPN events.
lead: The device sends its logs to an agent on your network; the server splits them with the vendor's model and derives attributes shared by every device.
---
## How it works

1. The device sends its syslog (UDP or TCP) to a HYPHA agent on its network.
2. The agent receives it, decodes the syslog header and records, from its `parsers` rule, which parsing model applies (attribute `syslog.parser`).
3. The server splits the message with that model: firewall action, addresses and ports, protocol, rule, user, interface, the device's own severity, event type.

> **Tested on samples, not on the devices.** Each model was checked against lines taken from the vendor's public documentation (or, when the vendor publishes none, from a public integration guide or from the documented format), sent over UDP and TCP to a test agent. None of these devices was actually connected. A line the model does not recognise keeps its full text in the message, with no attribute added.

## Turn reception on in an agent

Syslog reception is off by default. Add a `conf.d/40-syslog.yaml` file to the agent that will receive the logs:

```yaml
syslog:
  enabled: true
  udp: "0.0.0.0:5514"          # address:port; empty = no UDP
  tcp: "0.0.0.0:5514"          # newline-terminated or length-prefixed frames
  allowed_sources: ["192.0.2.0/24"]   # only these senders (empty = all)
  parsers:
    - {match: 192.0.2.1, parser: fortinet}        # sender address
    - {match: 192.0.2.16/29, parser: cisco-asa}   # CIDR network
    - {match: "pfsense*", parser: pfsense}         # pattern on the header's host name
  tags: {site: hq}
```

- A port above 1024 (here 5514) needs no extra rights for the agent; port 80 is refused.
- The agent never touches a firewall: open the port on the host for the sending devices only.
- `match` is an address, a CIDR network, or a pattern (`*`, `?`) on the syslog header's host name (otherwise the address). The first matching rule wins.
- `rate_limit` (2,000 lines/s by default) and `max_connections` (64) bound reception; dropped lines are counted in `syslog.dropped`.
- Check with `sudo hypha-agent check-config`, then restart the agent.

## Models

| Device | `parser` | Format received | Samples used in tests |
|---|---|---|---|
| Fortinet FortiGate (FortiOS) | `fortinet` | key=value (`date=… logid=…`) | vendor: "Sample logs by log type" (FortiOS 7.0) and Fortinet Community |
| Stormshield SNS | `stormshield` | WELF key=value (`id=firewall …`) | vendor (alarm), FortiSIEM (filter) |
| pfSense | `pfsense` | `filterlog` CSV | vendor ("Viewing the Firewall Log"); Netgate grammar |
| OPNsense | `opnsense` | `filterlog` CSV (same format) | built on the Netgate grammar |
| Palo Alto Networks (PAN-OS) | `paloalto` | CSV TRAFFIC, THREAT, SYSTEM, CONFIG, GLOBALPROTECT | built on the PAN-OS 10.2 field order |
| Cisco ASA and FTD | `cisco-asa` | `%ASA-n-nnnnnn: …` | vendor (Cisco Security Center); message guide templates |
| SonicWall (SonicOS) | `sonicwall` | key=value (`id=firewall … m=… msg=…`) | FortiSIEM, IBM/Juniper JSA |
| WatchGuard Firebox | `watchguard` | `msg_id="…"` then `Allow` or `Deny …` | vendor (Fireware 12.12 log catalog); Graylog |
| Check Point | `checkpoint` | Log Exporter: syslog `[key:"value"; …]`, CEF, LEEF | built on the Log Exporter guide format |
| MikroTik RouterOS | `mikrotik` | `topics message` (`firewall,info input: …`) | vendor (RouterOS "Log" page) |
| Ubiquiti UniFi and EdgeOS | `ubiquiti` | kernel logs (`[RULE] IN=… SRC=…`), UniFi Network CEF | Graylog (the vendor's site refuses automated reads) |

The `kv` model (wave 1) remains available for any other device that writes `key=value` pairs: each key becomes an attribute, without normalisation.

## Attributes produced

The same for every model, when the device provides them:

| Attribute | Values |
|---|---|
| `evt.category` | `traffic`, `threat`, `vpn`, `admin`, `system` |
| `evt.name` | the vendor's event name (subtype, message number, `admin_login`…) |
| `evt.outcome` | `success`, `failure` (logins, VPN) |
| `fw.action` | `allow`, `deny` |
| `fw.rule` | name or number of the rule, policy or access list |
| `network.source.ip`, `network.source.port` | flow source |
| `network.destination.ip`, `network.destination.port` | flow destination |
| `network.protocol`, `network.interface.in`, `network.interface.out` | protocol and interfaces |
| `usr.name` | user or administrator |
| `threat.name`, `threat.type` | signature, virus, threat type |
| `vpn.tunnel`, `vpn.peer`, `vpn.status` | tunnel, remote peer, `up` or `down` |
| `device.vendor`, `device.product`, `device.name`, `device.severity` | device and the severity it reports |

The vendor's raw fields stay under its prefix (`fortinet.*`, `stormshield.*`, `filterlog.*`, `paloalto.*`, `cisco.asa.*`, `sonicwall.*`, `watchguard.*`, `checkpoint.*`, `mikrotik.*`, `ubiquiti.*`). `network.client.ip` remains the address of the sending device.

The line's status is raised, never lowered: threat → `warning` (`error` when the device rates it high or critical); failed admin login, VPN failure, tunnel down → `warning`.

Search examples: `source:syslog @fw.action:deny`, `@evt.category:threat`, `@evt.category:admin @evt.outcome:failure`, `@syslog.parser:cisco-asa @vpn.status:down`.

## Configure sending on the device

Replace `192.0.2.50` with the agent's address and `5514` with its port. Menus change between versions: check your version's documentation.

- **Fortinet**: `config log syslogd setting`, `set status enable`, `set server "192.0.2.50"`, `set port 5514`, `set mode udp` (or `reliable` for TCP), `set format default`; then enable logging on the policies you want.
- **Stormshield**: *Logs - Syslog - IPFIX* module, *Syslog* tab: a profile towards the agent (UDP or TCP, port, RFC 5424 format), log families ticked (filtering, alarms, VPN, administration).
- **pfSense**: *Status > System Logs > Settings*, *Enable Remote Logging*, server `192.0.2.50:5514`, contents *Firewall Events* and *System Events*. pfSense sends over UDP only.
- **OPNsense**: *System > Settings > Logging*, remote destinations tab: UDP or TCP transport, applications `filter` (filterlog) and `audit`, port.
- **Palo Alto**: *Device > Server Profiles > Syslog* (server, UDP or TCP, port, BSD or IETF format), then a *Log Forwarding* profile on the rules (traffic, threats) and *Device > Log Settings* (system, configuration, GlobalProtect). The default CSV format is what the model expects.
- **Cisco ASA**: `logging enable`, `logging timestamp`, `logging trap informational`, `logging host inside 192.0.2.50 udp/5514` (or `tcp/5514`; with TCP add `logging permit-hostdown`, otherwise the ASA blocks traffic when the syslog server stops answering).
- **SonicWall**: *Device > Log > Syslog*, add the server (address, port), *Default* format.
- **WatchGuard**: *System > Logging*, *Syslog Server* tab: address, port, *Syslog* format.
- **Check Point**: on the log server, `cp_log_export add name hypha target-server 192.0.2.50 target-port 5514 protocol udp format syslog`, then `cp_log_export restart name hypha`.
- **MikroTik**: `/system logging action add name=hypha target=remote remote=192.0.2.50 remote-port=5514`, then `/system logging add topics=firewall action=hypha`, `/system logging add topics=account action=hypha`, `/system logging add topics=critical action=hypha`. RouterOS does not say what a logging rule did: set `log=yes log-prefix="drop"` on drop rules so that `fw.action` is `deny`.
- **Ubiquiti**: UniFi Network 9 and later, *Settings > Control Plane > Integrations > Activity Logging*, SIEM server (CEF); logging enabled on the firewall rules you want. EdgeOS: `set system syslog host 192.0.2.50:5514 facility all level info` and `log enable` on the rules.

## Ready-made monitors and dashboards

*Network and security devices* family of the [monitor catalogue](/en/docs/monitors/), each grouped by sending device and optionally restricted to one model (`parser` variable):

- **Firewall denial spike** (`fw.action:deny`, 5 minutes);
- **Threats detected by a device** (`evt.category:threat`);
- **Failed admin logins on a device** (10 minutes);
- **VPN tunnel down** (`vpn.status:down`) and **Repeated VPN failures** (15 minutes).

Reception itself has its own: *Syslog reception failing*, *Silent syslog device*, *Dropped syslog lines*.

The dashboard catalogue offers one dashboard per model (*Fortinet FortiGate (syslog)*, *Cisco ASA et FTD (syslog)*…): lines received per device, dropped lines, then denied traffic, threats, administration, VPN and errors, filterable by agent and by device.

## What was tested

On a test machine, an agent received on an unprivileged port, over UDP then TCP, every sample line of the eleven models, each model sent from its own address; the parsed attributes were read back in HYPHA. The sample lines and their sources are in `server/tests/test_equipements.py`. Not tested: the behaviour of real devices (version-specific formats, missing fields, volumes), and the admin and VPN messages that vendors do not publish as samples (Stormshield, SonicWall, Check Point, UniFi). If a log from your device is not recognised, the line keeps all its text: send us an anonymised sample.
