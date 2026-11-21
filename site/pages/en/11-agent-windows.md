---
id: agent-windows
path: /en/docs/agent-windows/
title: Install the agent on Windows
nav: Windows
section: Install the agent
order: 11
updated: 2026-11-19
description: Install hypha-agent on Windows and Windows Server in one PowerShell command; service, event logs, update and uninstall.
lead: A Windows service, "Hypha Agent", that reads performance counters, event logs and services.
---
## Requirements

- Windows Server 2016 or later, or Windows 10 and 11, 64-bit (amd64).
- PowerShell run **as administrator**.
- Outbound HTTPS access to `hypha.s33d.fr`. The agent opens no port.
- An ingestion key (**Settings › API keys**).

## Install in one command

```powershell
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor 3072; $env:HYPHA_API_KEY='hph_your_key'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

The start of the line enables TLS 1.2, which PowerShell 5.1 lacks by default on Windows Server 2016 and 2019. The **Settings › Install the agent** page gives this command ready to paste.

The script downloads `hypha-agent.exe` to `C:\Program Files\Hypha`, checks its SHA-256 sum, writes `C:\ProgramData\Hypha\hypha.yaml`, then installs and starts the **Hypha Agent** service (short name `HyphaAgent`), automatic start, LocalSystem account, restarted on failure.

The variables `HYPHA_URL`, `HYPHA_TAGS`, `HYPHA_HOSTNAME`, `HYPHA_FORCE_CONF` and `HYPHA_TLS_PIN` (through `curl.exe`, Windows Server 2019 and later) work as on [Linux](/en/docs/agent-linux/#install-in-one-command).

## Check

```powershell
& "C:\Program Files\Hypha\hypha-agent.exe" status
& "C:\Program Files\Hypha\hypha-agent.exe" check-config
Get-Service HyphaAgent
```

## What the agent collects by default

- CPU, memory (including committed memory), page file, disks, I/O, network, processes.
- The **System** and **Application** event logs (list set and filtered by `logs.windows_eventlog`, see [Windows](/en/docs/windows/)).
- Performance counters and WMI queries of your choice, Active Directory domain controllers and DNS servers: see [Windows](/en/docs/windows/).
- Services: a stopped automatic service is reported on Windows Server. Update services known to stop on their own are excluded; add yours in `checks.windows_services_exclude`.
- Pending Windows updates, clock (`w32tm`), certificates of the computer store.
- IIS, SQL Server, Exchange and Hyper-V when present. See [Integrations](/en/docs/integrations/) and [Virtualization](/en/docs/virtualization/).

On a desktop edition, stopped services are not reported by default: set `checks.windows_auto_services: always` to follow them.

## Antivirus software

Some antivirus products hold or refuse the first launch of an executable they do not know. If an installation or an update fails with "Access denied", allow `C:\Program Files\Hypha\hypha-agent.exe`. During an automatic update, the agent waits up to 3 minutes for the new binary to answer; otherwise it keeps the old one and tries again an hour later, three times at most.

## Update

Run the install command again, or let the fleet update itself with `auto_update: true`: see [Fleet and updates](/en/docs/fleet/).

## Uninstall

```powershell
$env:HYPHA_UNINSTALL='1'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

The service and the binary are removed. `C:\ProgramData\Hypha` (configuration and state) stays in place.
