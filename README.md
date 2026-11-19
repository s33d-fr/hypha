# HYPHA

*[Version française plus bas](#hypha-en-français).*

HYPHA is an open-source observability platform built by [S33D](https://www.s33d.fr): metrics, logs,
traces, inventory, synthetic tests, SNMP devices, databases, Kubernetes, hypervisors and cloud
connectors, with monitors, alerts, incidents, SLOs, dashboards and public status pages. When
something breaks, HYPHA explains the probable cause in plain language and can propose a repair
(restarting a named service, for example) that a human approves in one click.

The name comes from the hypha, the filament of the mycelium: each installed agent is a filament,
together they form the network.

- Documentation: <https://hypha.s33d.fr/en/docs/> (French: <https://hypha.s33d.fr/docs/>)
- Hosted service: <https://hypha.s33d.fr>

## Repository layout

| Path | Content | License |
|---|---|---|
| `agent/` | the `hypha-agent` program (Go), Linux and Windows | Apache 2.0 |
| `server/` | API and worker (Python, FastAPI) | AGPL v3 |
| `ui/` | web interface and documentation pages (static) | AGPL v3 |
| `schema/` | PostgreSQL schema and migrations | AGPL v3 |
| `packaging/` | Docker images and the Docker Compose self-hosting kit | AGPL v3 |
| `site/` | generator and sources of the documentation pages | AGPL v3 |

## Install the agent

Create an ingestion key in **Settings › Install the agent** of your HYPHA server, then:

Linux (systemd, amd64 or arm64):

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_your_key bash
```

Windows (PowerShell run as administrator):

```powershell
$env:HYPHA_API_KEY='hph_your_key'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

With a self-hosted server, download the script from your own server and add
`HYPHA_URL=https://hypha.your-domain.example`. Docker, Kubernetes, `.deb` and `.rpm` packages and
uninstallation are described in the documentation:
[Linux](https://hypha.s33d.fr/en/docs/agent-linux/),
[Windows](https://hypha.s33d.fr/en/docs/agent-windows/),
[Docker](https://hypha.s33d.fr/en/docs/agent-docker/),
[Kubernetes](https://hypha.s33d.fr/en/docs/kubernetes/).

The agent opens no port on the host: it sends its data over HTTPS to the server set in its
configuration and fetches its instructions from it.

## Self-host with Docker Compose

The same software as the hosted service, on your own Linux server (amd64 or arm64), free and
without limits. Your data stays in your PostgreSQL database.

```bash
mkdir hypha && cd hypha
curl -fsSLO https://hypha.s33d.fr/auto-hebergement/docker-compose.yml
curl -fsSL https://hypha.s33d.fr/auto-hebergement/env.example -o .env
# fill in .env: HYPHA_DB_PASSWORD, HYPHA_PUBLIC_URL, HYPHA_ADMIN_PASSWORD, HYPHA_SECRET
mkdir certs && cp /path/fullchain.pem /path/privkey.pem certs/
docker compose up -d
```

The same files are in [`packaging/docker/`](packaging/docker/). Full guide (accounts, local AI
model, updates, backups): <https://hypha.s33d.fr/en/docs/self-hosting/>.

## Hosted service

S33D runs HYPHA for you at <https://hypha.s33d.fr>, hosted in France: free **Découverte** plan up to
5 hosts, then paid **Pro** and **Entreprise** plans. Only the hosted service is paid; the software
in this repository is free. Plans and prices: <https://hypha.s33d.fr/en/pricing/>.

## Build from source

Agent (Go 1.26):

```bash
cd agent
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=0.0.0-dev" -o hypha-agent ./cmd/hypha-agent
```

`agent/packaging/build.sh` builds every target with `.deb` and `.rpm` packages; release builds of
this repository run in GitHub Actions (`.github/workflows/agent.yml`).

Server (Python 3.14):

```bash
cd server
python -m pip install -r requirements.txt pytest
python -m pytest -q
```

## Code signing policy

Windows builds of the agent are to be signed through SignPath Foundation. See
[CODE_SIGNING_POLICY.md](CODE_SIGNING_POLICY.md) for the signing scope, team roles and privacy
policy.

## Security

Please report vulnerabilities privately: see [SECURITY.md](SECURITY.md).

## Contributing

Issues and pull requests are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md).

## License

Copyright © 2026 S33D.

- `agent/`: [Apache License 2.0](agent/LICENSE) (third-party notices in
  [agent/THIRD_PARTY_LICENSES.txt](agent/THIRD_PARTY_LICENSES.txt)).
- Everything else (server, interface, schema, self-hosting kit, documentation sources):
  [GNU Affero General Public License v3.0](LICENSE).

---

# HYPHA en français

HYPHA est une plateforme d'observabilité libre conçue par [S33D](https://www.s33d.fr) : mesures,
journaux, traces, inventaire, tests synthétiques, équipements SNMP, bases de données, Kubernetes,
hyperviseurs et connecteurs cloud, avec moniteurs, alertes, incidents, SLO, tableaux et pages de
statut publiques. Quand une panne survient, HYPHA en explique la cause probable en français et peut
proposer une réparation (redémarrer un service nommé, par exemple) qu'un humain valide d'un clic.

Le nom vient de l'hyphe, le filament du mycélium : chaque agent installé est un filament, ensemble
ils forment le réseau.

- Documentation : <https://hypha.s33d.fr/docs/> (anglais : <https://hypha.s33d.fr/en/docs/>)
- Service hébergé : <https://hypha.s33d.fr>

## Contenu du dépôt

| Chemin | Contenu | Licence |
|---|---|---|
| `agent/` | le programme `hypha-agent` (Go), Linux et Windows | Apache 2.0 |
| `server/` | API et worker (Python, FastAPI) | AGPL v3 |
| `ui/` | interface web et pages de documentation (statiques) | AGPL v3 |
| `schema/` | schéma PostgreSQL et migrations | AGPL v3 |
| `packaging/` | images Docker et kit d'auto-hébergement Docker Compose | AGPL v3 |
| `site/` | générateur et sources des pages de documentation | AGPL v3 |

## Installer l'agent

Créez une clé d'ingestion dans **Réglages › Installer l'agent** de votre serveur HYPHA, puis :

Linux (systemd, amd64 ou arm64) :

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_votre_cle bash
```

Windows (PowerShell lancé en administrateur) :

```powershell
$env:HYPHA_API_KEY='hph_votre_cle'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

Avec un serveur auto-hébergé, téléchargez le script depuis votre serveur et ajoutez
`HYPHA_URL=https://hypha.votre-domaine.example`. Docker, Kubernetes, paquets `.deb` et `.rpm` et
désinstallation : voir la documentation
([Linux](https://hypha.s33d.fr/docs/agent-linux/),
[Windows](https://hypha.s33d.fr/docs/agent-windows/),
[Docker](https://hypha.s33d.fr/docs/agent-docker/),
[Kubernetes](https://hypha.s33d.fr/docs/kubernetes/)).

L'agent n'ouvre aucun port sur l'hôte : il envoie ses données en HTTPS au serveur indiqué dans sa
configuration et y relève ses consignes.

## Auto-héberger avec Docker Compose

Le même logiciel que le service hébergé, sur votre serveur Linux (amd64 ou arm64), gratuit et sans
limite. Vos données restent dans votre base PostgreSQL.

```bash
mkdir hypha && cd hypha
curl -fsSLO https://hypha.s33d.fr/auto-hebergement/docker-compose.yml
curl -fsSL https://hypha.s33d.fr/auto-hebergement/env.example -o .env
# remplir .env : HYPHA_DB_PASSWORD, HYPHA_PUBLIC_URL, HYPHA_ADMIN_PASSWORD, HYPHA_SECRET
mkdir certs && cp /chemin/fullchain.pem /chemin/privkey.pem certs/
docker compose up -d
```

Les mêmes fichiers sont dans [`packaging/docker/`](packaging/docker/). Notice complète (comptes,
modèle d'IA local, mises à jour, sauvegardes) : <https://hypha.s33d.fr/docs/auto-hebergement/>.

## Service hébergé

S33D fait tourner HYPHA pour vous sur <https://hypha.s33d.fr>, hébergé en France : formule
**Découverte** gratuite jusqu'à 5 hôtes, puis formules payantes **Pro** et **Entreprise**. Seul le
service hébergé est payant ; le logiciel de ce dépôt est libre et gratuit. Formules et prix :
<https://hypha.s33d.fr/tarifs/>.

## Construire depuis les sources

Agent (Go 1.26) :

```bash
cd agent
go test ./...
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=0.0.0-dev" -o hypha-agent ./cmd/hypha-agent
```

`agent/packaging/build.sh` construit toutes les cibles avec les paquets `.deb` et `.rpm` ; les
versions publiées depuis ce dépôt sont construites par GitHub Actions
(`.github/workflows/agent.yml`).

Serveur (Python 3.14) :

```bash
cd server
python -m pip install -r requirements.txt pytest
python -m pytest -q
```

## Politique de signature du code

Les exécutables Windows de l'agent ont vocation à être signés par SignPath Foundation. Périmètre,
rôles et politique de confidentialité : [CODE_SIGNING_POLICY.md](CODE_SIGNING_POLICY.md) (en
anglais).

## Sécurité

Signalez une vulnérabilité en privé : voir [SECURITY.md](SECURITY.md).

## Contribuer

Tickets et demandes de fusion bienvenus : voir [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

Copyright © 2026 S33D.

- `agent/` : [licence Apache 2.0](agent/LICENSE) (avis des composants tiers dans
  [agent/THIRD_PARTY_LICENSES.txt](agent/THIRD_PARTY_LICENSES.txt)).
- Tout le reste (serveur, interface, schéma, kit d'auto-hébergement, sources de la documentation) :
  [GNU Affero General Public License v3.0](LICENSE).
