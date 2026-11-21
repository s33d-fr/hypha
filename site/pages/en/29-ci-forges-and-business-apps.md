---
id: ci-forges
path: /en/docs/ci-forges-and-business-apps/
title: "CI, forges, registries and business apps: Jenkins, GitHub, Zot, Odoo"
nav: CI, forges and business apps
section: Integrations
order: 29
updated: 2026-11-21
description: HYPHA agent integrations for Jenkins (JSON API), GitHub and GitHub Enterprise Cloud (read-only connector), the Zot OCI registry (metrics extension) and Odoo (health, version, workers): settings, permissions, metrics, checks, monitor templates.
lead: Failing builds, growing queues, red workflows, the registry and the ERP, read-only by the agent.
---
## Principle

These integrations follow the rules of the others ([Applications and services](/en/docs/applications-and-services/)): in `auto` mode, a probe whose software answers at its usual address turns itself on; an address or an account set in the configuration states the intent, and failures are reported from the first run. Once the software has been seen, its `<name>.can_connect` check turns critical if it stops answering. Several targets are declared with `instances` ([Configuration](/en/docs/configuration/)). Nothing is ever written: no build started, no workflow re-run, no image pushed, no Odoo database opened.

Each integration has its [monitor templates](/en/docs/monitors/) (families “CI et forges”, “Dépôts et registres”, “Applications métier”) and its ready-made dashboard (**Dashboards › Catalogue**).

## Jenkins

The agent reads the Jenkins JSON API every 60 seconds: `/api/json` (jobs and their latest builds, folders and multibranch projects down to three levels), `/queue/api/json` (build queue), `/computer/api/json` (executors and nodes), and, when the Metrics plugin is installed, its health checks. In `auto` mode, the probe turns on when a Jenkins answers on `127.0.0.1:8080` (`X-Jenkins` header).

```yaml
integrations:
  jenkins:
    url: https://ci.example.com     # default http://127.0.0.1:8080
    username: hypha                 # read-only account
    password: 11aa22bb…             # its API token (Profile › Security › API Token)
    jobs: ["prod/*", "deploy-*"]    # optional: full-name patterns; empty = all
    max_jobs: 500                   # jobs published one by one, at most
```

Account permissions (matrix strategy): **Overall/Read**, **Job/Read**, **Agent/ExtendedRead**. Optional: **Metrics/HealthCheck** (Metrics plugin) for its health checks; **Agent/Connect** for node free space and response time, which Jenkins only shows with that permission. Agent/Connect also allows connecting a build agent: only grant it if node disk space matters more to you than that risk.

| Metric | Meaning |
|---|---|
| `jenkins.jobs.total`, `.failing`, `.unstable`, `.disabled`, `.building` | tracked jobs, last build failed, unstable, disabled, running |
| `jenkins.job.health` (`job`) | job health (0 to 100, the Jenkins weather) |
| `jenkins.job.last_build.duration` (`job`) | duration of the last completed build, in seconds |
| `jenkins.job.last_success.age` (`job`) | time since the last successful build |
| `jenkins.queue.size`, `.blocked`, `.buildable`, `.stuck`, `.oldest_age` | build queue: waiting, blocked, ready, marked “stuck”, wait of the oldest |
| `jenkins.executors.total`, `.busy`, `.idle`, `.usage` | executors, busy, idle, busy share (%) |
| `jenkins.nodes.total`, `.offline`; `jenkins.node.offline`, `.executors` (`node`) | nodes and offline nodes |
| `jenkins.node.disk_free`, `.disk_free_percent`, `.temp_free`, `.response_time` (`node`) | node free space and response time (Agent/Connect permission) |
| `jenkins.health_check` (`check`) | Metrics plugin checks (1 healthy, 0 failing) |
| `jenkins.quieting_down` | 1 while a shutdown is prepared |

Checks: `jenkins.can_connect`; `jenkins.job.status` per job (CRIT when the last build failed, WARN when unstable or aborted; a job never built or disabled is not judged); `jenkins.node.status` per node (CRIT offline, WARN taken offline by an administrator); `jenkins.queue` (WARN when Jenkins marks builds as stuck, with the reason: “there are no nodes with the label…”); `jenkins.health` (CRIT when a Metrics plugin check fails, WARN during a prepared shutdown).

Templates: **Jenkins injoignable**, **Tâche Jenkins en échec**, **Nœud Jenkins hors ligne**, **Builds bloqués dans la file Jenkins**, **Attente dans la file Jenkins**, **Exécuteurs Jenkins saturés**, **Place libre d'un nœud Jenkins**, **Santé de Jenkins**. Ready-made dashboard: **Jenkins**.

Tested on Jenkins 2.580.2 LTS (`jenkins/jenkins:lts-jdk21` image, Folders and Metrics plugins), with an account holding the permissions above: successful, failed, unstable and nested jobs, a build stuck for lack of a node with its label, a never-connected node (CRIT), Jenkins stopped (CRIT, then back to green).

## GitHub

Read-only connector to **github.com** or **GitHub Enterprise Cloud** (`*.ghe.com`), built like the [cloud connectors](/en/docs/cloud/): a single agent polls it, and its metrics carry that agent's name. Every `interval` (5 minutes by default), for each named repository:

- the default branch (read again every 6 hours);
- the latest 100 GitHub Actions runs: the latest completed run of each workflow on the default branch (pull requests and tags are left out), queued and running runs;
- with a token that allows it: open Dependabot, code scanning and secret scanning alerts (100 at most per kind);
- the API quota (`/rate_limit`, which does not count against the quota).

```yaml
integrations:
  github:
    repositories: [my-org/site, my-org/api]
    token: github_pat_…           # optional for a public repository
    # endpoint: https://api.my-company.ghe.com   # GitHub Enterprise Cloud (data residency)
    interval: 5m
    security: auto                # alerts read when the token allows it; false to skip them
    tags: {forge: github}
```

Without a token, only public repositories can be read and GitHub grants **60 requests per hour** to the machine's public address (two per repository and per poll): the interval is then at least 5 minutes, and the agent warns when the named repositories would exceed that quota. With a token (5,000 requests per hour), one minute at least. Recommended token: a **fine-grained token** limited to the tracked repositories, read-only: **Actions**, **Metadata**, and for alerts **Dependabot alerts**, **Code scanning alerts**, **Secret scanning alerts**. An alert kind the token cannot read is skipped and named in the `github.can_connect` message.

| Metric | Meaning |
|---|---|
| `github.workflows.failing` (`repo`) | workflows whose latest run on the default branch failed |
| `github.workflow.run.duration` (`repo`, `workflow`) | duration of the latest completed run |
| `github.runs.queued`, `.in_progress`, `.queued.oldest_age` (`repo`) | queued runs, running runs, wait of the oldest queued run (seconds) |
| `github.dependabot.alerts`, `github.code_scanning.alerts` (`repo`, `severity`) | open alerts by severity (critical, high, medium, low) |
| `github.secret_scanning.alerts` (`repo`) | exposed secrets, open alerts |
| `github.rate_limit.limit`, `.remaining`, `.used_percent` | API quota |

Checks: `github.can_connect`; `github.workflow.status` per workflow (CRIT failed or timed out, WARN cancelled or waiting for an action); `github.security` per repository (CRIT when a critical alert or an exposed secret is open, WARN for a high-severity alert); `github.rate_limit` (WARN below 10 % of the quota, CRIT when exhausted). GitHub's own workflows (Dependabot, dependency graph) are named by their path (`dependabot/update-graph`).

Templates: **Connecteur GitHub en échec**, **Workflow GitHub Actions en échec**, **Exécutions GitHub Actions en file**, **Alertes de sécurité GitHub**, **Quota de l'API GitHub**. Ready-made dashboard: **GitHub**.

Tested on the public github.com API, without a token, with a public repository (successful workflows, Dependabot dynamic workflow). The token, security alerts and GitHub Enterprise Cloud follow the GitHub REST API documentation (version 2022-11-28) and its response formats, reproduced in the agent tests; they have not been read on a real account yet.

## Zot

The [Zot](https://zotregistry.dev) OCI registry publishes its metrics in Prometheus format through its `metrics` extension (full image; the “minimal” image does not have it). In Zot's `config.json`:

```json
"extensions": { "metrics": { "enable": true, "prometheus": { "path": "/metrics" } } }
```

Then the `zot` profile of the [OpenMetrics reader](/en/docs/prometheus-software/):

```yaml
integrations:
  openmetrics:
    - profile: zot
      url: http://127.0.0.1:5000/metrics      # the http.port
      # headers: {Authorization: "Basic …"}   # when Zot requires an account
```

| Metric | Meaning |
|---|---|
| `zot.http.requests` (`code`, `method`) | requests per second |
| `zot.http.latency` (`method`) | latency (histogram: `.p50`, `.p95`, `.p99`, `.avg`) |
| `zot.repo.downloads`, `zot.repo.uploads` (`repo`) | image downloads and uploads per second |
| `zot.repo.storage` (`repo`) | space used per repository, in bytes |
| `zot.storage.lock_latency` (`locktype`) | storage lock wait (histogram) |
| `zot.gc.duration` | storage garbage collection (summary) |
| `zot.scheduler.queue` (`priority`), `zot.scheduler.workers` (`state`), `.workers_total`, `zot.scheduler.generators` | background tasks: queue, busy or idle workers |

Templates: **Zot injoignable**, **Tâches en retard dans Zot**, **Verrous du stockage Zot lents**. Ready-made dashboard: **Zot**.

Tested on Zot v2.1.23 (`zot-linux-amd64` image, in a container): an image pushed then pulled, the registry stopped (`openmetrics.can_connect` CRIT, then back to green).

## Odoo

The agent reads Odoo (17 and later) every 30 seconds, without an account:

- `/web/health`; since Odoo 18, `?db_server_status=1` also checks that the PostgreSQL server answers: its failure returns “fail”;
- the version through XML-RPC (`/xmlrpc/2/common`, `version` method), falling back to `/web/webclient/version_info` when XML-RPC is disabled or removed;
- the response time of each;
- when the url points to the machine itself: Odoo processes in `/proc` (master or threaded server, workers, gevent process for live notifications) and their resident memory. With `setproctitle` installed, HTTP and cron workers are told apart; without it (official image), they are “worker”.

```yaml
integrations:
  odoo:
    url: http://127.0.0.1:8069     # default; or the address served by the proxy
    processes: true                # default: yes when the url points to the machine
```

| Metric | Meaning |
|---|---|
| `odoo.response_time` (`endpoint`: `health`, `xmlrpc` or `version_info`) | response time, in milliseconds |
| `odoo.database.reachable` | 1 when Odoo's PostgreSQL server answers (Odoo 18 and later) |
| `odoo.processes`, `odoo.processes.memory` (`role`: `main`, `worker`, `http`, `cron`, `gevent`) | processes and resident memory per role |
| `odoo.workers` | running workers (multi-process mode, `workers` > 0) |

Checks: `odoo.can_connect` (with the version) and `odoo.health` (CRIT when `/web/health` returns “fail”). PostgreSQL itself is followed by the [PostgreSQL integration](/en/docs/databases/).

Templates: **Odoo injoignable**, **Odoo en défaut**, **Odoo lent**. Ready-made dashboard: **Odoo**.

Tested on Odoo 19.0 (official `odoo:19` image, multi-process mode with two HTTP workers and one cron, PostgreSQL 17): version read through XML-RPC, five processes seen (master, three workers, gevent), connections to the database forbidden (`odoo.health` CRIT, then back to green), Odoo stopped (CRIT).
