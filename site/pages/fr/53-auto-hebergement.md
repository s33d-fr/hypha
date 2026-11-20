---
id: self-hosting
path: /docs/auto-hebergement/
title: Auto-hébergement
nav: Auto-hébergement
section: Administrer
order: 53
updated: 2026-11-20
description: Installer HYPHA sur votre serveur avec Docker Compose (formule Libre) : prérequis, installation, comptes, IA locale, mise à jour, sauvegarde. Vos données restent chez vous.
lead: Le même logiciel que hypha.s33d.fr, sur votre machine, gratuit et sans limite. Vos mesures, journaux et traces restent dans votre base PostgreSQL.
---
## Ce qu'il faut

- Un serveur Linux 64 bits (amd64 ou arm64) avec Docker et Docker Compose.
- De la mémoire et du disque selon le nombre d'hôtes suivis et la durée de conservation des journaux.
- Un nom DNS et son certificat TLS (`fullchain.pem`, `privkey.pem`). Seul le port HTTPS est publié.

## Installer

```bash
mkdir hypha && cd hypha
curl -fsSLO https://hypha.s33d.fr/auto-hebergement/docker-compose.yml
curl -fsSL https://hypha.s33d.fr/auto-hebergement/env.example -o .env
```

Remplissez `.env`. Les quatre valeurs obligatoires :

```bash
HYPHA_DB_PASSWORD=     # openssl rand -hex 24
HYPHA_PUBLIC_URL=      # https://hypha.votre-domaine.fr
HYPHA_ADMIN_PASSWORD=  # mot de passe du compte « admin »
HYPHA_SECRET=          # openssl rand -hex 32
```

Posez le certificat, puis démarrez :

```bash
mkdir certs
cp /chemin/fullchain.pem /chemin/privkey.pem certs/
docker compose up -d
```

Au premier démarrage, HYPHA crée la base, l'organisation (`HYPHA_ORG_NAME`), le compte d'administration (`HYPHA_ADMIN_LOGIN`, `admin` par défaut) et six moniteurs de base. Ouvrez `HYPHA_PUBLIC_URL` et connectez-vous.

Installez ensuite les agents depuis **Réglages › Installer l'agent** : la commande télécharge l'agent depuis votre serveur et le relie à lui (`HYPHA_URL`).

## Ce que contient le kit

| Service | Rôle |
|---|---|
| `db` | PostgreSQL 18 ; données dans le volume `hypha-db` |
| `migrate` | met le schéma à jour, puis s'arrête |
| `api` | API de HYPHA et réception des agents |
| `worker` | moniteurs, tests synthétiques, notifications, partitions de la base |
| `nginx` | interface, binaires signés de l'agent, seul port publié (HTTPS) |

Images publiques : `registre-vm.s33d.fr/hypha/server` et `registre-vm.s33d.fr/hypha/web`, en amd64 et arm64.

## Réglages utiles

| Variable | Rôle |
|---|---|
| `HYPHA_SMTP_HOST`, `_PORT`, `_USER`, `_PASSWORD`, `_FROM`, `_SECURITY` | envoi des alertes par e-mail |
| `HYPHA_OIDC_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` | connexion OpenID Connect, en plus des comptes locaux ; seuls les comptes invités entrent |
| `HYPHA_VAPID_PRIVATE_KEY`, `HYPHA_VAPID_SUBJECT` | notifications push vers les téléphones et ordinateurs : clé générée par `docker compose exec api python -m hypha vapid-key` (voir [Application mobile](/docs/application-mobile/)) ; vide, le canal est coupé |
| `HYPHA_LLM_URL`, `HYPHA_LLM_MODEL` | votre serveur de modèle de type Ollama, pour l'explication des pannes et le post-mortem ; vide, la fonction est coupée |
| `HYPHA_ALLOW_PRIVATE_TARGETS` | autoriser les tests synthétiques et les webhooks vers des adresses internes |
| `HYPHA_LOG_LEVEL` | niveau du journal du serveur |
| `HYPHA_OTEL_ENDPOINT`, `HYPHA_OTEL_SAMPLE` | traces du serveur lui-même, par le relais OTLP d'un agent |

## Comptes

En auto-hébergement, les comptes sont locaux. Ajoutez-en depuis **Réglages › Membres**, ou en ligne de commande :

```bash
docker compose exec api python -m hypha create-user --org <slug> --login marie --role member
```

## Mettre à jour

```bash
# dans .env : HYPHA_VERSION=<nouvelle version>
docker compose pull
docker compose up -d
```

Le service `migrate` passe les migrations avant le redémarrage de l'API. Le kit sert les binaires signés de l'agent et leur signature : vos agents se mettent à jour depuis votre serveur (voir [Flotte et mises à jour](/docs/flotte/)).

## Sauvegarder

```bash
docker compose exec -T db pg_dump -U hypha -Fc hypha > hypha-$(date +%F).dump
```

## Licences

Le serveur et l'interface sont distribués sous licence **AGPL v3**, l'agent sous licence **Apache 2.0**. La formule Libre n'a ni limite ni facturation. Un support avec délais garantis fait partie de la formule Entreprise du service hébergé.
