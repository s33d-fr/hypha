---
id: windows
path: /en/docs/windows/
title: "Windows: event logs, counters, WMI, Active Directory and DNS"
nav: Windows
section: Integrations
order: 28
updated: 2026-11-21
description: Monitor Windows and Windows Server with HYPHA - event logs filtered by channel, level, source and event ID, performance counters and WMI queries of your choice, Active Directory domain controllers and Microsoft DNS servers.
lead: The event logs you choose, the counters and WMI classes of your software, and two Windows Server roles watched out of the box - Active Directory and DNS.
---
The Windows agent ([installation](/en/docs/agent-windows/)) runs as a service under the system account: it reads every log, Security included, and the host's performance counters. Everything below is read-only.

## Event logs

By default the agent ships the **System** and **Application** logs. The `logs.windows_eventlog` list takes channel names, or objects with filters:

```yaml
logs:
  windows_eventlog:
    - System
    - channel: Application
      exclude_providers: ["Microsoft-Windows-Security-SPP"]   # sources left out (glob patterns)
    - channel: Security
      event_ids: [4625, 4740, "4720-4726", 4728, 4732, 4756]  # IDs or ranges
      service: windows-security                                # instead of the source name
      tags: {role: dc}
    - channel: Microsoft-Windows-TaskScheduler/Operational
      levels: [critical, error, warning]
```

| Setting | Effect |
|---|---|
| `levels` | `critical`, `error`, `warning`, `information`, `verbose`, `audit_success`, `audit_failure`; empty = all |
| `event_ids`, `exclude_event_ids` | IDs (`4625`) or ranges (`4624-4634`) kept or left out |
| `providers`, `exclude_providers` | sources (providers) kept or left out, case-insensitive, `*` and `?` patterns allowed |
| `service` | HYPHA service of the channel's lines (default: the source name) |
| `tags` | tags added to the channel's lines |

Kept levels and IDs go into the XPath query sent to Windows: a domain controller's Security log is not read in full. A filtered-out event is not shipped, but the read position moves on: nothing is read again after a restart. Security events sit at level 0; filter them with `audit_success` and `audit_failure`, not `information`. A named channel (`…/Operational`, `…/Admin`) is read like any other; `wevtutil el` lists them.

Each line has the `eventlog` source and the attributes `@eventlog.channel`, `.event_id`, `.provider`, `.record_id`, `.level` (0 to 5), `.level_name` (the `levels` names above), `.audit` (`success` or `failure`), `.task`, `.opcode`, `.keywords`, the labels rendered by Windows in its own language (`.level_text`, `.task_text`, `.opcode_text`, `.keywords_text`), `.user_sid`, `.pid`, `.computer` and the event data (`@eventlog.data.TargetUserName`…). Search example: `source:eventlog @eventlog.event_id:4625`.

The `windows.eventlog.events` metric counts collected events per channel (`channel`) and status (`status`). Monitor templates (**Windows** family of the catalogue): critical Windows event, unexpected shutdown (6008), logon failures (4625, add the Security channel), locked-out account (4740), errors per log. Ready dashboard: "Journaux d'événements Windows".

## Performance counters

The counters of your software, as you choose, in `integrations.perf_counters`: one set per piece of software, read together.

```yaml
integrations:
  perf_counters:
    - name: sql-locks                    # instance_name tag
      namespace: sqlserver               # metric prefix (default: the name)
      interval: 30s                      # 10 s to 1 h
      timeout: 10s
      max_series: 500
      counters:
        - path: '\SQLServer:Locks(_Total)\Lock Waits/sec'
          metric: locks.waits            # → sqlserver.locks.waits
          unit: operation_per_second
        - path: '\PhysicalDisk(*)\Avg. Disk sec/Read'
          instance_tag: disk             # one series per instance, tagged disk
          exclude_instances: [_Total]
```

Paths use the **English names** of objects and counters, whatever the language of Windows (PowerShell shows them translated). The English list: `(Get-ItemProperty 'HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Perflib\009').Counter`. `(*)` reads every instance (one series each, filtered by `instances` and `exclude_instances`, glob patterns); a named instance (`(_Total)`) gives a single series. Without `metric`, the name comes from the counter: `% Processor Time` → `processor_time_pct`, `Lock Waits/sec` → `lock_waits_per_s`. "/sec" counters are already rates.

Safeguards: beyond `max_series`, further series are dropped; a reading that exceeds `timeout` is abandoned, and no other one starts until it returns. The `perf_counters.can_collect` check turns to warning when a counter does not exist on the host or series are dropped, to critical when nothing can be read. Reading metrics: `perf_counters.series`, `.series.dropped`, `.duration`.

## WMI queries

For what counters do not tell (states, sizes, software-specific classes), `integrations.wmi` runs read-only WQL queries:

```yaml
integrations:
  wmi:
    - name: os
      class: Win32_OperatingSystem
      properties: [FreePhysicalMemory, NumberOfProcesses]
    - name: physical-disks
      wmi_namespace: root\Microsoft\Windows\Storage   # root\cimv2 by default
      class: MSFT_PhysicalDisk
      properties: [{property: HealthStatus, metric: health}]
      tag_by: [FriendlyName]             # text properties turned into tags
    - name: stopped-services
      class: Win32_Service
      properties: [ExitCode]
      filter: "StartMode = 'Auto' AND State = 'Stopped'"
      tag_by: [Name]
      interval: 5m
```

Each numeric property of each row becomes a `<namespace>.<metric>` series (default: the property name, `FreePhysicalMemory` → `free_physical_memory`), tagged by `tag_by` (`FriendlyName` → `friendly_name`). 64-bit integers, returned as text by WMI, are converted; a non-numeric property is reported (put it in `tag_by`). Interval from 15 s to 1 h (1 min by default), same `timeout` and `max_series` safeguards as counters. The `wmi.can_query` check turns to warning when the query returns no row, to critical when it fails. Metrics: `wmi.rows`, `wmi.series`, `wmi.series.dropped`, `wmi.duration`. For a rate, prefer the performance counter: a `Win32_PerfFormattedData_*` class read now and then gives unreliable averages.

`perf_counters` and `wmi` lists from several `conf.d` files add up. Monitor templates (**Collecte générique** family): unreadable performance counters, failing WMI query. Ready dashboard: "Compteurs de performance et WMI".

## Active Directory

On a domain controller (NTDS service present) the agent turns itself on: `integrations.active_directory: {enabled: auto}` (`false` to turn it off).

- **Every 30 s**, counters: LDAP sessions, searches, writes and binds and their duration (`active_directory.ldap.*`), directory reads, writes and searches (`.ds.*`), inbound and outbound replication and pending operations (`.dra.*`), ATQ queue, Kerberos and NTLM authentications (`.kerberos.*`, `.ntlm.authentications`), Netlogon semaphores, the NTLM authentication bottleneck (`.netlogon.*`).
- **Every 30 s**, `active_directory.service` check of the role's services present: NTDS, Netlogon, Kdc, ADWS, DFSR, IsmServ, W32Time, LanmanServer, DNS (a disabled service is ignored); `active_directory.can_connect` critical when NTDS is stopped or its counters unreadable.
- **Every 5 min**, through PowerShell (ActiveDirectory module, served by ADWS): inbound replication partners, `active_directory.replication` check per partner and partition (warning on failure, critical with no success for 24 h or ever), age of the last success (`.replication.age`), FSMO roles held (`.fsmo_roles`, and in the `can_connect` message), `active_directory.sysvol` check (SYSVOL and NETLOGON shares).
- **Directory Service** and **DFS Replication** logs added to collection (`logs: false` to leave them out; an entry for the same channel in `logs.windows_eventlog` wins, with its filters).

Monitor templates (**Active Directory** family): domain controller down, role service stopped, replication failing, SYSVOL or NETLOGON not shared, slow LDAP binds, Netlogon bottleneck (NTLM). Ready dashboard: "Active Directory". For logon failures and locked-out accounts, add the Security channel (see above).

## Microsoft DNS

On a Windows DNS server (DNS service present), same principle: `integrations.windows_dns`.

```yaml
integrations:
  windows_dns:
    exclude_zones: ["*.in-addr.arpa"]   # zones neither checked nor measured (glob patterns)
    test_zone: example.com              # zone of the SOA question (default: the first primary zone)
    address: 127.0.0.1:53               # the server's address if it does not listen on loopback
```

- **Every 30 s**: queries received (UDP, TCP, dropped), responses, recursive queries and their failures and timeouts, dynamic updates (`windows_dns.*`); rejected updates, failed secure updates, successful and failed zone transfers, counted between two readings; cache and database memory.
- **Every 30 s**, the agent asks the server a real SOA question: `windows_dns.can_connect` critical when the service is stopped, does not answer, answers with an error (REFUSED, SERVFAIL…) or without authority; response time `windows_dns.response_time`.
- **Every 5 min**, through PowerShell (DnsServer module): `windows_dns.zone` check per zone (warning when paused or when a secondary zone's last transfer failed, critical when shut down or expired), serial number of primary zones (`windows_dns.zone.serial`), age of the last successful transfer of secondary zones.
- **DNS Server** log added to collection. Record changes are in the `Microsoft-Windows-DNSServer/Audit` channel; add it to `logs.windows_eventlog` if you want to keep them (large with many dynamic updates).

Monitor templates (**DNS Microsoft** family): silent DNS server, failing zone, recursion failures. Ready dashboard: "DNS Microsoft".

## WSUS

The probe is **off by default**: it only starts on an explicit decision, on the WSUS server itself (`integrations.wsus.enabled: true`). It reads the WSUS administration API (PowerShell, `Microsoft.UpdateServices.Administration` assembly, installed with the role console), read-only: nothing is approved, declined, synchronised or cleaned up.

```yaml
integrations:
  wsus:
    enabled: true
    stale_days: 30          # computers with no report for this many days
    sync_max_age: 48h       # last successful synchronisation older than this: warning
```

- **Every 60 s**: role services (`WsusService`, IIS `W3SVC`, internal database `MSSQL$MICROSOFT##WID`), `wsus.service` check per service.
- **Every 5 min**, every minute while a fault is seen: computers (`wsus.computers`, `.needing_updates`, `.with_errors`, `.stale`), updates (`wsus.updates.total`, `.approved`, `.declined`, `.expired`, `.not_approved`, `.critical_not_approved`, `.needing_files`, `.with_client_errors`, `.with_server_errors`), synchronisation (`wsus.sync.age` since the last successful one, `wsus.sync.duration`, `wsus.sync.failed`), SUSDB size (internal database) and free space of the content folder (`wsus.database.size`, `wsus.content.free`).
- Checks: `wsus.can_connect` (service stopped or API failing: CRIT), `wsus.sync` (last synchronisation failed: CRIT, with the WSUS message; none successful, older than `sync_max_age`, or cancelled: WARN; running: OK), `wsus.app_pool` (IIS `WsusPool` pool stopped: CRIT).

Monitor templates: **WSUS injoignable**, **Synchronisation WSUS en échec**, **Service du rôle WSUS arrêté**, **Pool d'applications WsusPool arrêté**, **Ordinateurs en défaut dans WSUS**, **Place libre du contenu WSUS**. Ready-made dashboard: **WSUS**.

Tested on a test WSUS server (Windows Server 2025 in French, WID internal database), seen in HYPHA: never synchronised (WARN), first synchronisation with Microsoft Update running (OK), WsusService and the WsusPool pool stopped (CRIT, then OK on return), synchronisation failure caused by a non-existent proxy (CRIT, with the WSUS message). Not yet seen on a real server: a successful synchronisation (the first category synchronisation took more than an hour on the test machine) and client computers reporting.

## Compatibility

Windows Server 2016 and later, plus Windows 10 and 11 for logs, counters and WMI. Proven on Windows Server 2025 in French, sole domain controller of a domain with directory-integrated DNS: replication between several controllers and secondary zones are read but have not been proven on a real server yet.
