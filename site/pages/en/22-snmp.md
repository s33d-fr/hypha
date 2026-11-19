---
id: snmp
path: /en/docs/snmp/
title: Network devices over SNMP
nav: SNMP
section: Integrations
order: 22
updated: 2026-11-19
description: Monitor switches, routers, NAS, printers and UPS over SNMP v1, v2c and v3 from a HYPHA agent, with nothing installed on the device.
lead: An agent queries the devices of its network and publishes them as full hosts. SNMP secrets never leave the agent.
---
## Declare a device

In the configuration of an agent that reaches the device, preferably a `conf.d/20-snmp.yaml` file (mode 600):

```yaml
snmp:
  enabled: true
  interval: 60s
  devices:
    # SNMP v2c
    - {name: rack-1-switch, address: 192.0.2.10, version: 2c, community: readonly, tags: {role: switch}}
    # SNMP v3, authentication and encryption
    - name: backup-nas
      address: 192.0.2.20
      version: 3
      user: hypha
      level: authPriv
      auth_protocol: sha        # md5, sha, sha224, sha256, sha384, sha512
      auth_key: "…"             # 8 characters at least
      priv_protocol: aes        # des, aes, aes192, aes256, aes192c, aes256c
      priv_key: "…"
      profile: auto
```

Other per-device settings: `port` (161), `timeout` (5 s), `retries`, `context`, `interfaces_exclude` (patterns), `interface_down`.

Advice:

- prefer SNMPv3 with `authPriv`, with a read-only account limited to the agent's address;
- disable the device's `public` community before turning SNMP on;
- check with `sudo hypha-agent check-config`, then restart the agent.

## Profiles

`profile: auto` reads `sysObjectID` and picks the profile:

| Profile | What it adds |
|---|---|
| `generic` | MIB-II, IF-MIB, HOST-RESOURCES-MIB: interfaces, CPU, memory, storage |
| `mikrotik` | RouterOS CPU, temperatures, fans, power supplies |
| `synology` | disk, volume and RAID state |
| ASUSTOR (detected) | temperatures, fans, disk state and temperature, volume state and usage; `snmp.disk.health` and `snmp.volume.health` checks |
| `printer` | printer state and supply levels |
| `ups` | UPS-MIB: battery charge, runtime, output load |

## Metrics and checks

| Metric | Unit |
|---|---|
| `snmp.ifBandwidthIn`, `.ifBandwidthOut` | bytes per second |
| `snmp.ifBandwidthInUsage`, `.OutUsage` | percent of the interface speed |
| `snmp.ifInErrors`, `.ifOutErrors`, `.ifInDiscards`, `.ifOutDiscards` | packets per second |
| `snmp.ifOperStatus`, `snmp.ifAdminStatus` | state |
| `snmp.cpu.usage`, `snmp.memory.usage`, `snmp.storage.usage` | percent |
| `snmp.temperature`, `snmp.fan.speed`, `snmp.psu.status` | degrees, RPM, state |
| `snmp.ups.battery.charge`, `.runtime`, `.load` | percent, seconds |
| `snmp.sysUpTimeInstance`, `snmp.poll.duration` | seconds, milliseconds |

Checks:

- `snmp.can_poll`: critical when the device stops answering;
- `snmp.interface.status`: critical for an interface that is administratively up but down. With `interface_down: seen` (default), only an interface already seen up counts: a free switch port stays OK. `always` applies the rule to every up interface.

## Traps

```yaml
snmp:
  traps: {enabled: true, listen: "0.0.0.0:162", community: readonly}
```

Each v1 or v2c trap received becomes an event (`source:snmp type:snmp_trap`), 60 per minute at most. An "event" monitor can alert on them.

## In the interface

- **Infrastructure** filters by type (servers or network devices). A device page shows its identity and its interfaces as a table: name, alias, state, speed, throughput, errors, with small charts.
- **Settings › Devices** lists the devices seen, the agent that queries them and their last contact.
- The network map shows devices as nodes.

Catalogue templates (**Network** family): network interface down, device unreachable.
