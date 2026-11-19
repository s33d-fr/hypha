---
id: cloud
path: /en/docs/cloud/
title: AWS, Azure, Google Cloud, OVHcloud and Scaleway
nav: Clouds
section: Integrations
order: 23
updated: 2026-11-19
description: HYPHA read-only cloud connectors: AWS EC2 instances, EBS volumes and CloudWatch alarms, Azure VMs and Resource Health, Compute Engine, OVHcloud servers, Scaleway Instances and Elastic Metal.
lead: An agent reads the provider's API with a read-only account. Instances become resources in HYPHA, next to your hosts.
---
## How it works

- The connector runs in an agent of your choice: it only needs outbound access to the provider's API.
- It turns on as soon as its credentials are set. Keep them in a `conf.d` file reserved to root (600).
- Each instance or server becomes a **cloud resource**: a host without an agent, attached to the agent that queries it, with its state at the provider and its checks.
- If a HYPHA agent also runs inside the instance, HYPHA matches the two (same serial number, or same public address) and shows a single line. The host page then shows "Seen by connectors".
- Several accounts or projects: an `instances` list (see [Configuration](/en/docs/configuration/#several-instances)).
- Each connector publishes `<name>.can_connect`. The monitor catalogue has a "connector failing" template per provider.

## AWS

Reads EC2, EBS, CloudWatch alarms and the account identity, through a read-only IAM policy:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["ec2:DescribeInstances", "ec2:DescribeInstanceStatus", "ec2:DescribeVolumes",
               "cloudwatch:DescribeAlarms", "sts:GetCallerIdentity"],
    "Resource": "*"
  }]
}
```

```yaml
integrations:
  aws:
    access_key_id: AKIA…
    secret_access_key: "…"
    regions: [eu-west-3, eu-central-1]
    interval: 5m
    metrics: false
```

- Instances: state, type, addresses, EC2 status checks and scheduled events (`aws.ec2.status`).
- EBS volumes in error (`aws.ebs.volume`), CloudWatch alarms in alarm (`aws.cloudwatch.alarm`).
- `metrics: true` adds instance CPU through `cloudwatch:GetMetricData` (add it to the policy). AWS bills this call to your account: it stays off by default.

## Azure

A service principal with the **Reader** role on each monitored subscription:

```bash
az ad sp create-for-rbac --name hypha-lecture --role Reader --scopes /subscriptions/<subscription-id>
```

```yaml
integrations:
  azure:
    tenant_id: "…"
    client_id: "…"
    client_secret: "…"
    subscriptions: ["<subscription-id>"]
```

- VMs: size, system, power state, addresses (interfaces and public IPs).
- Health as seen by Azure (Resource Health): `azure.vm.health` check. A VM deallocated by its owner is not an outage.
- Fired Azure Monitor alerts: one check per subscription, back to green as soon as none is active.

## Google Cloud

A service account with the **Compute Viewer** role (`roles/compute.viewer`) and its JSON key, in a separate file:

```yaml
integrations:
  gcp:
    credentials_file: /etc/hypha/gcp-readonly.json   # 600, root
    projects: [my-project]
```

Instances in every zone of the project: state, type, addresses (`gcp.instance.status`). An instance being repaired by Google (`REPAIRING`) turns critical.

## OVHcloud

An OVHcloud API application and a "consumer" key limited to reads: `GET` rights on `/dedicated/server*`, `/ip*` and `/domain*`.

```yaml
integrations:
  ovhcloud:
    endpoint: https://eu.api.ovh.com/1.0
    application_key: "…"
    application_secret: "…"
    consumer_key: "…"
    interval: 15m
```

What a machine's agent cannot see:

- the state of each dedicated server as seen by OVHcloud (`ovh.dedicated_server.state`);
- IP addresses blocked for spam (`ovh.ip.spam`) or hacking (`ovh.ip.antihack`);
- ongoing anti-DDoS mitigation (`ovh.ip.mitigation`);
- the expiry of services and domains (`ovh.service.renewal`, `ovh.service.days_left`), read every 6 hours;
- OVHcloud interventions and tasks on your servers, as events.

Only `GET` requests go out, 200 ms apart; 5 minutes at least between two passes.

## Scaleway

The API key of an IAM member with `InstancesReadOnly` and `ElasticMetalReadOnly`:

```yaml
integrations:
  scaleway:
    secret_key: "…"
    zones: [fr-par-1, fr-par-2, nl-ams-1]
```

Per zone: instances (state, lock, scheduled maintenance) and Elastic Metal servers (state, answer to Scaleway's ping).

## What it costs

- At the provider: EC2, STS, Azure Resource Manager and Compute Engine reads are not billed; at AWS, `DescribeAlarms` falls within the free monthly CloudWatch request volume, and `metrics: true` is billed. Check your provider's terms.
- At HYPHA: each resource read by a connector counts as a device. See [pricing](/en/pricing/).
