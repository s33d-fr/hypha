---
id: self-hosting
path: /en/docs/self-hosting/
title: Self-hosting
nav: Self-hosting
section: Administer
order: 53
updated: 2026-11-19
description: Install HYPHA on your own server with Docker Compose (Libre plan): requirements, installation, accounts, local AI, update, backup. Your data stays with you.
lead: The same software as hypha.s33d.fr, on your machine, free and without limits. Your metrics, logs and traces stay in your PostgreSQL database.
---
## Requirements

- A 64-bit Linux server (amd64 or arm64) with Docker and Docker Compose.
- Memory and disk according to the number of monitored hosts and the log retention.
- A DNS name and its TLS certificate (`fullchain.pem`, `privkey.pem`). Only the HTTPS port is published.

## Install

```bash
mkdir hypha && cd hypha
curl -fsSLO https://hypha.s33d.fr/auto-hebergement/docker-compose.yml
curl -fsSL https://hypha.s33d.fr/auto-hebergement/env.example -o .env
```

Fill in `.env`. The four required values:

```bash
HYPHA_DB_PASSWORD=     # openssl rand -hex 24
HYPHA_PUBLIC_URL=      # https://hypha.your-domain.com
HYPHA_ADMIN_PASSWORD=  # password of the "admin" account
HYPHA_SECRET=          # openssl rand -hex 32
```

Install the certificate, then start:

```bash
mkdir certs
cp /path/fullchain.pem /path/privkey.pem certs/
docker compose up -d
```

On first start, HYPHA creates the database, the organisation (`HYPHA_ORG_NAME`), the administration account (`HYPHA_ADMIN_LOGIN`, `admin` by default) and six base monitors. Open `HYPHA_PUBLIC_URL` and sign in.

Then install the agents from **Settings › Install the agent**: the command downloads the agent from your server and links it to it (`HYPHA_URL`).

## What the kit contains

| Service | Role |
|---|---|
| `db` | PostgreSQL 18; data in the `hypha-db` volume |
| `migrate` | updates the schema, then stops |
| `api` | HYPHA API and agent intake |
| `worker` | monitors, synthetic tests, notifications, database partitions |
| `nginx` | interface, signed agent binaries, the only published port (HTTPS) |

Public images: `registre-vm.s33d.fr/hypha/server` and `registre-vm.s33d.fr/hypha/web`, for amd64 and arm64.

## Useful settings

| Variable | Purpose |
|---|---|
| `HYPHA_SMTP_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_FROM`, `_SECURITY` | sending alerts by email |
| `HYPHA_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` | OpenID Connect sign-in, in addition to local accounts; only invited accounts get in |
| `HYPHA_LLM_URL`, `HYPHA_LLM_MODEL` | your Ollama-style model server, for outage explanations and postmortems; empty, the feature is off |
| `HYPHA_ALLOW_PRIVATE_TARGETS` | allow synthetic tests and webhooks towards internal addresses |
| `HYPHA_LOG_LEVEL` | server log level |
| `HYPHA_OTEL_ENDPOINT`, `HYPHA_OTEL_SAMPLE` | traces of the server itself, through an agent's OTLP relay |

## Accounts

When self-hosting, accounts are local. Add them from **Settings › Members**, or from the command line:

```bash
docker compose exec api python -m hypha create-user --org <slug> --login maria --role member
```

## Update

```bash
# in .env: HYPHA_VERSION=<new version>
docker compose pull
docker compose up -d
```

The `migrate` service runs migrations before the API restarts. The kit serves the signed agent binaries and their signature: your agents update from your server (see [Fleet and updates](/en/docs/fleet/)).

## Back up

```bash
docker compose exec -T db pg_dump -U hypha -Fc hypha > hypha-$(date +%F).dump
```

## Licences

The server and interface are distributed under the **AGPL v3** licence, the agent under the **Apache 2.0** licence. The Libre plan has no limit and no billing. Support with guaranteed response times is part of the Entreprise plan of the hosted service.
