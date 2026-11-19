---
id: virtualization
path: /en/docs/virtualization/
title: Hypervisors: vSphere, Proxmox VE, Hyper-V, KVM and VirtualBox
nav: Virtualization
section: Integrations
order: 24
updated: 2026-11-19
description: Monitor ESXi and vCenter, Proxmox VE, Hyper-V, KVM with libvirt and VirtualBox with HYPHA, read-only: hosts, VMs, datastores, backups, snapshots.
lead: The state of hypervisors and their VMs, read-only, next to the agents installed inside the VMs.
---
## VMware vSphere (ESXi and vCenter)

No agent goes on an ESXi host: an agent of your fleet queries the vSphere API with an account holding the **ReadOnly** role.

```yaml
integrations:
  vsphere:
    interval: 60s
    targets:
      - name: vcenter
        url: https://vcenter.example/sdk
        username: hypha@vsphere.local
        password: "…"
        thumbprint: "AA:BB:…"     # SHA-1 fingerprint of a self-signed certificate
        # insecure: true          # avoid: accepts any certificate
        tags: {site: paris}
```

What HYPHA collects:

- ESXi hosts: state, alarms, maintenance mode, CPU, memory, running VMs, uptime (`vsphere.host.*`);
- datastores: accessibility and usage (`vsphere.datastore.*`);
- VMs: power, state, guest heartbeat, CPU, memory, age of the oldest snapshot (`vsphere.vm.*`);
- vSphere events.

ESXi hosts and VMs become resources in **Infrastructure**. A VM that also runs a HYPHA agent is matched to its host by serial number ("VMware-…"): one line, and its page shows what vSphere says about it.

## Proxmox VE

Two ways to read the cluster:

- **On a Proxmox node**: the agent uses `pvesh` as root, without an account, in a separate, bounded systemd unit. Only the first online node reads the cluster: the other agents stay quiet, without duplicates.
- **Remotely**: the API with a token holding the **PVEAuditor** role.

```yaml
integrations:
  proxmox:
    url: https://pve.example:8006
    token_id: "hypha@pve!readonly"
    token_secret: "…"
    ca_file: /etc/hypha/pve-ca.pem
```

Checks: cluster quorum, node offline, storage unavailable, HA guest out of its requested state, last backup (vzdump) of each node failing, cleared as soon as the next one succeeds. Metrics for nodes, storages and guests (VMs and containers). Failed tasks become events.

## Hyper-V

On Windows, the agent reads `Get-VM` when the Hyper-V service is present; no setting needed.

- `hyperv.vm` check per VM: critical state (storage unreachable), paused, stopped while its automatic start is "always", guest heartbeat lost.
- `hyperv.vm.replication` check: replication health.
- Metrics: CPU, assigned and demanded memory, vCPUs, uptime, number of checkpoints and age of the oldest (a forgotten AVHDX file grows endlessly).

## KVM and libvirt

On the host, `virsh` read-only (`qemu:///system` by default, set by `uri`).

- Domains: state and reason, CPU, vCPUs, memory, disks, network (`libvirt.domain.*`).
- Checks: crashed domain, paused on an I/O error (full pool, the classic case), stopped while autostart is on; storage pool inactive or inaccessible.
- Pools: usage and available space (`libvirt.pool.*`).

## VirtualBox

The host's agent collects the declared and running VMs, and reports an autostart VM that is stopped (`virtualbox.vm.running`). The `user` setting follows a single account.

## Monitor templates

The **Virtualization** family of the catalogue: vSphere connector failing, ESXi host faulty, datastore inaccessible or almost full, vSphere VM faulty, old snapshot; Proxmox unreachable, quorum lost, node offline, storage unavailable or almost full, HA guest out of its state, backup failing; Hyper-V VM faulty, old checkpoint, degraded replication; KVM VM faulty, pool inactive or almost full.
