---
id: network-storage-backups
path: /docs/reseau-stockage-sauvegardes/
title: Réseau, stockage et sauvegardes auto-hébergés : Pi-hole, AdGuard Home, Unbound, Caddy, MinIO, Mosquitto, Proxmox Backup Server, Borg, restic
nav: Réseau, stockage et sauvegardes
section: Intégrations
order: 29
updated: 2026-11-21
description: Superviser avec HYPHA les services auto-hébergés du réseau et des données : Pi-hole et AdGuard Home (filtrage DNS), Unbound, Caddy, MinIO, le courtier MQTT Mosquitto, Proxmox Backup Server, Borg et restic (âge de la dernière sauvegarde).
lead: Requêtes DNS et blocages, serveurs amont, capacité du stockage objet, clients MQTT, et surtout l'âge et l'état de la dernière sauvegarde de chaque groupe ou dépôt, lus en lecture seule.
---
## En un coup d'œil

| Logiciel | Collecte | Activation | Espace de noms | Éprouvé sur |
|---|---|---|---|---|
| Pi-hole | API REST de Pi-hole 6 (`/api`) | auto si `/etc/pihole` existe, ou `password` posé | `pihole.*` | Pi-hole 6.6 (FTL 6.9) |
| AdGuard Home | API `/control` (compte de l'interface) | dès que `username` et `password` sont posés | `adguard.*` | AdGuard Home 1.0.0 |
| Unbound | `unbound-control stats_noreset` | auto si `/etc/unbound/unbound.conf` existe | `unbound.*` | Unbound 1.26.2 (Debian 13) |
| Caddy | API d'administration (`:2019`) + profil OpenMetrics `caddy` | sonde auto sur `127.0.0.1:2019` ; profil à déclarer | `caddy.*` | Caddy 2.11.9 |
| MinIO | profils OpenMetrics `minio` et `minio_bucket` | à déclarer | `minio.*` | MinIO RELEASE.2025-10-15T17-29-55Z |
| Mosquitto | sujets `$SYS` en MQTT 3.1.1 | auto si `/etc/mosquitto` existe, ou `username` posé | `mosquitto.*` | Mosquitto 2.1.4 |
| Proxmox Backup Server | API (`:8007`), jeton au rôle Audit | dès que le jeton est posé | `proxmox_backup.*` | PBS 4.2.14 |
| Borg | `borg list --json` (et `borg info`) | dès qu'un dépôt est déclaré | `borg.*` | Borg 1.4.4 (serveur 1.4.0) |
| restic | `restic snapshots --json` (et `restic stats`) | dès qu'un dépôt est déclaré | `restic.*` | restic 0.18.1, rest-server |

Chaque logiciel a été lancé dans un environnement d'essai de S33D, en conteneur, avec ses vraies données : requêtes DNS réelles, sauvegardes réelles, puis pannes provoquées (sauvegarde interrompue, bloc corrompu, serveur amont arrêté). Les sondes acceptent des **instances** (`instances:`, une entrée nommée par serveur supplémentaire) et des **étiquettes fixes** (`tags:`) posées sur toutes leurs mesures et tous leurs contrôles. Rien n'est jamais écrit dans ces logiciels.

## Pi-hole

Pi-hole 6 sert une API REST sur son serveur web (port 80 et 443 par défaut). Créez un **mot de passe d'application** (Réglages › Interface web / API › Configurer le mot de passe d'application) : il ne demande pas la double authentification et peut être retiré sans toucher au mot de passe de l'interface.

```yaml
integrations:
  pihole:
    url: http://127.0.0.1           # ou https://127.0.0.1 avec insecure: true (certificat autosigné)
    password: "mot de passe d'application"
```

L'agent ouvre une session au premier relevé, la garde tant que Pi-hole l'accepte et la ferme à son arrêt : Pi-hole limite le nombre de sessions ouvertes. Un Pi-hole sans mot de passe se lit sans `password`.

| Mesure | Sens |
|---|---|
| `pihole.queries.total`, `.blocked`, `.cached`, `.forwarded`, `.unique_domains` | sur les 24 dernières heures (fenêtre glissante de FTL) |
| `pihole.queries.percent_blocked` | part des requêtes bloquées (%) |
| `pihole.queries.frequency` | requêtes par seconde (moyenne récente de FTL) |
| `pihole.queries.by_status` (`status`), `pihole.queries.by_reply` (`reply`) | requêtes par traitement (gravity, cache, forwarded…) et par réponse |
| `pihole.clients.active`, `pihole.clients.total` | clients |
| `pihole.gravity.domains_blocked`, `pihole.gravity.age` | domaines en liste, secondes depuis la dernière mise à jour des listes |
| `pihole.blocking.enabled` | 1 si le blocage est actif |
| `pihole.messages`, `pihole.messages.by_type` | avertissements de FTL (Outils › Diagnostic) |
| `pihole.updates_available` | composants (core, web, FTL) dont Pi-hole annonce une version plus récente |

Contrôles : `pihole.can_connect` et `pihole.blocking` (avertissement : blocage coupé, avec le temps restant ; critique : blocage en échec).

## AdGuard Home

L'agent lit `/control/status`, `/control/stats` et `/control/filtering/status` avec le compte de l'interface, en authentification de base. Il n'appelle jamais `/control/version.json` (qui interroge les serveurs d'AdGuard) ni aucune route d'écriture.

```yaml
integrations:
  adguard:
    url: http://127.0.0.1:3000
    username: admin
    password: "…"
```

| Mesure | Sens |
|---|---|
| `adguard.queries`, `.queries.blocked`, `.queries.percent_blocked` | sur la fenêtre de statistiques d'AdGuard Home (24 h par défaut) |
| `adguard.queries.safebrowsing`, `.safesearch`, `.parental` | réponses remplacées par la navigation sécurisée, la recherche sécurisée, le contrôle parental |
| `adguard.processing_time` | temps de traitement moyen (ms) |
| `adguard.upstream.responses`, `adguard.upstream.avg_time` (`upstream`) | réponses et temps moyen (ms) de chaque serveur amont |
| `adguard.filter.rules`, `adguard.filter.age` (`filter`) | règles et âge (s) de chaque liste active |
| `adguard.filters.enabled`, `.rules`, `.empty`, `.oldest_update_age` | listes actives, règles au total, listes vides ou jamais téléchargées, plus vieille mise à jour |
| `adguard.running`, `adguard.protection_enabled`, `adguard.filtering.enabled` | 1/0 |

Contrôles : `adguard.can_connect` et `adguard.protection` (avertissement : protection coupée ; critique : serveur DNS arrêté).

## Unbound

Activez le canal de contrôle (le paquet Debian le fait déjà, sur une socket Unix), et de préférence les statistiques détaillées :

```text
server:
    extended-statistics: yes
remote-control:
    control-enable: yes
    control-interface: /run/unbound.ctl
```

```yaml
integrations:
  unbound:
    command: unbound-control                   # « docker exec unbound unbound-control » en conteneur
    # config_file: /etc/unbound/unbound.conf   # passé par -c
```

L'agent lance `unbound-control stats_noreset` toutes les 30 secondes, sans shell : les compteurs ne sont pas remis à zéro. Les compteurs donnent un écart par intervalle ; si un autre outil lit `stats` (qui remet à zéro), un seul intervalle est perdu.

- Compteurs : `unbound.num.queries`, `.cachehits`, `.cachemiss`, `.prefetch`, `.expired`, `.recursivereplies`, `unbound.num.answer.rcode` (`rcode`), `unbound.num.query.type` (`query_type`), `unbound.num.answer.secure`, `.bogus`, `unbound.num.rrset.bogus`, `unbound.requestlist.exceeded`, `.overwritten`, `unbound.unwanted.queries`, `.replies`, `unbound.num.query.tcp`, `.tls`, `.https`, `.ipv6`.
- Jauges : `unbound.cache.hit_rate` (%), `unbound.requestlist.avg`, `.max`, `.current`, `unbound.recursion.time.avg`, `.median` (s), `unbound.time.up`, `unbound.mem.cache.rrset`, `.message`, `unbound.msg.cache.count`, `unbound.rrset.cache.count`, `unbound.infra.cache.count`, `unbound.key.cache.count`.

Contrôle `unbound.can_connect` : critique si `unbound-control` échoue (Unbound arrêté, canal de contrôle coupé, clés illisibles).

## Caddy

Caddy publie ses mesures Prometheus sur son API d'administration (`/metrics`, port 2019). Celles des serveurs HTTP demandent l'option globale `metrics` :

```text
{
	metrics
}
```

```yaml
integrations:
  openmetrics:
    - profile: caddy                 # http://127.0.0.1:2019/metrics
  caddy:
    url: http://127.0.0.1:2019       # sonde de l'API d'administration (auto)
```

Profil `caddy` : `caddy.http.requests`, `caddy.http.requests_in_flight`, `caddy.http.request_errors` (par `server` et `handler`), `caddy.http.request.duration` (histogramme : `.count` par `code` = réponses par code, `.p95`…), `caddy.http.response.duration`, `caddy.http.request.size`, `caddy.http.response.size`, `caddy.reverse_proxy.upstream.healthy` (1/0 par `upstream`), `caddy.config.last_reload_successful`, `caddy.config.last_reload_success_timestamp`, `caddy.process.resident_memory`, `caddy.go.goroutines`.

La sonde `caddy` lit en GET seulement `/config/apps/http/servers` (`caddy.servers`, `caddy.routes`) et `/reverse_proxy/upstreams` (`caddy.upstream.requests` : requêtes en cours, `caddy.upstream.fails` : échecs comptés par la santé passive, `caddy.upstreams`, `caddy.upstreams.failing`). Contrôle `caddy.can_connect`. L'API d'administration ne doit jamais être ouverte au réseau.

## MinIO

MinIO publie ses mesures sur `/minio/v2/metrics/cluster` (grappe) et `/minio/v2/metrics/bucket` (par seau). Créez le jeton Prometheus de MinIO, puis rangez la valeur de `bearer_token` dans un fichier lisible par l'agent seul :

```text
mc admin prometheus generate monalias cluster
```

```yaml
integrations:
  openmetrics:
    - profile: minio                 # http://127.0.0.1:9000/minio/v2/metrics/cluster
      bearer_token_file: /etc/hypha/minio.token
    - profile: minio_bucket          # /minio/v2/metrics/bucket, même jeton
      bearer_token_file: /etc/hypha/minio.token
```

Sans jeton, MinIO répond 403, sauf `MINIO_PROMETHEUS_AUTH_TYPE=public`. Dans une grappe, un seul agent suffit (le point de grappe agrège tous les nœuds ; étiquette `server`).

- Santé : `minio.cluster.health.status` (1 = saine), `minio.cluster.nodes.online`, `.offline`, `minio.cluster.drives.online`, `.offline`, `.total`, `minio.cluster.erasure_set.healing_drives`, `.online_drives`, `.status`, `.write_quorum` (`pool`, `set`).
- Capacité : `minio.cluster.capacity.usable.free`, `.total`, `minio.cluster.capacity.raw.free`, `.total` (octets).
- Usage (après le premier passage du scanner de MinIO) : `minio.cluster.usage.objects`, `.bytes`, `.versions`, `minio.cluster.buckets` ; par seau : `minio.bucket.usage.bytes`, `.objects`, `minio.bucket.requests`, `minio.bucket.traffic.received`, `.sent` (`bucket`).
- API S3 : `minio.s3.requests` (`api`), `minio.s3.requests.errors`, `.4xx_errors`, `.5xx_errors`, `.inflight`, `.waiting`, `minio.s3.requests.rejected.auth`, `.invalid`, `.timestamp`, `.header`, `minio.s3.traffic.received`, `.sent`.
- Nœuds : `minio.node.uptime`, `minio.node.process.resident_memory`, `minio.node.fd.open`, `.limit`.

MinIO ne publie plus d'image ni de binaire de son édition libre : la version éprouvée a été construite depuis ses sources officielles.

## Mosquitto

L'agent se connecte au courtier en MQTT 3.1.1, s'abonne à `$SYS/broker/#` en QoS 0, lit les valeurs retenues, puis se déconnecte : il ne publie jamais. Donnez-lui un compte qui ne lit que `$SYS` :

```text
# mosquitto_passwd -b /etc/mosquitto/passwd hypha "…"
# ACL (acl_file) :
user hypha
topic read $SYS/#
```

```yaml
integrations:
  mosquitto:
    url: mqtt://127.0.0.1:1883       # mqtts://hôte:8883 en TLS (ca_file, insecure)
    username: hypha
    password: "…"
```

- Jauges : `mosquitto.clients.connected`, `.disconnected`, `.total`, `.expired`, `mosquitto.subscriptions`, `mosquitto.retained_messages`, `mosquitto.messages.stored`, `mosquitto.store.messages.count`, `.bytes`, `mosquitto.heap.current`, `.maximum`, `mosquitto.connections.sockets`, `mosquitto.uptime`, `mosquitto.load.connections.1min`, `mosquitto.load.messages.received.1min`, `.sent.1min`.
- Compteurs (écart par intervalle) : `mosquitto.messages.received`, `.sent`, `mosquitto.publish.messages.received`, `.sent`, `.dropped`, `mosquitto.publish.bytes.received`, `.sent`, `mosquitto.bytes.received`, `.sent`.

Contrôle `mosquitto.can_connect` : compte refusé (CONNACK 4 ou 5), abonnement refusé par l'ACL, ou aucun sujet `$SYS` reçu (`sys_interval 0`).

## Proxmox Backup Server

Créez un utilisateur et un jeton d'API au rôle **Audit**, sur le chemin `/`, pour l'utilisateur ET pour le jeton (les droits d'un jeton sont l'intersection des deux) :

```text
proxmox-backup-manager user create hypha@pbs
proxmox-backup-manager acl update / Audit --auth-id hypha@pbs
proxmox-backup-manager user generate-token hypha@pbs lecture
proxmox-backup-manager acl update / Audit --auth-id 'hypha@pbs!lecture'
proxmox-backup-manager cert info | grep Fingerprint
```

```yaml
integrations:
  proxmox_backup:
    url: https://127.0.0.1:8007
    token_id: "hypha@pbs!lecture"
    token_secret: "…"                # valeur rendue par generate-token
    fingerprint: "af:c8:fd:…"        # empreinte SHA-256 du certificat (ou ca_file, ou insecure: true)
    # interval: 1m
    # max_groups: 500                # groupes de sauvegarde suivis par magasin
```

L'abonnement n'est jamais lu. Par relevé :

- Magasins : `proxmox_backup.datastore.total`, `.used`, `.avail`, `.usage` (%), `.days_until_full` (prévision de PBS), `.chunks`, `.bad_chunks`, `.gc_pending`, `.dedup_factor`, `.groups`, `.last_backup.age` (`datastore`).
- Groupes de sauvegarde (VM, conteneur, hôte ; tous les espaces de noms) : `proxmox_backup.group.last_backup.age` (secondes depuis la dernière sauvegarde) et `proxmox_backup.group.snapshots`, étiquettes `datastore`, `backup_type`, `backup_id`, `pbs_namespace`.
- Vérifications (toutes les 15 min) : `proxmox_backup.datastore.snapshots`, `.snapshots.verify_failed`, `.snapshots.unverified`.
- Tâches en échec depuis le relevé précédent : `proxmox_backup.tasks.failed` (`pbs_task`) et un événement par tâche (« tâche backup essai:host/… : connection error »).
- Serveur : `proxmox_backup.node.cpu`, `.memory.used`, `.memory.total`, `.root.used`, `.root.total`, `.uptime`, `.load.1`.

Contrôles : `proxmox_backup.can_connect`, `proxmox_backup.datastore` (critique : magasin en erreur), `proxmox_backup.job` (dernier passage de chaque travail de vérification, d'élagage, de synchronisation et du ramasse-miettes ; critique en échec), `proxmox_backup.backup` (dernière tâche de sauvegarde de chaque groupe sur 7 jours, relue toutes les 5 min : critique en échec, levé dès que la suivante réussit).

## Borg et restic

L'agent lance l'outil installé sur la machine, sans shell et **sans verrou** : `borg --bypass-lock`, `restic --no-lock`. Une sauvegarde, un élagage ou une vérification en cours ne sont jamais gênés ni retardés par la supervision. Chaque dépôt est relu toutes les 15 minutes.

```yaml
integrations:
  borg:
    repositories:
      - name: serveur
        repository: ssh://borg@sauvegarde.exemple/./depot
        passphrase_file: /etc/hypha/borg-serveur.pass     # root, droits 600 ou 400
        rsh: "ssh -i /root/.ssh/borg -o BatchMode=yes"
  restic:
    repositories:
      - name: nas
        repository_file: /etc/hypha/restic-nas.repo        # adresse avec identifiants (rest:, s3:…)
        password_file: /etc/hypha/restic-nas.pass
      - name: local
        repository: /srv/restic
        password_file: /etc/hypha/restic-local.pass
    # stats: true      # taille des dépôts (borg info, restic stats), relue toutes les 6 h
    # interval: 15m
    # timeout: 2m
```

- La phrase de passe ne passe jamais en argument ni en variable d'environnement : Borg la reçoit par son entrée standard (`BORG_PASSPHRASE_FD=0`), restic lit lui-même son `--password-file`. Un fichier de phrase de passe, de mot de passe ou d'adresse ouvert au groupe ou à tous, ou qui n'appartient pas à root, est refusé.
- `env:` passe des variables sans secret (par exemple `AWS_SHARED_CREDENTIALS_FILE` vers un fichier réservé à root) ; un mot de passe dans l'adresse d'un dépôt est refusé (utilisez `repository_file`).
- Sous systemd, l'outil tourne dans sa propre unité bornée (512 Mio, la moitié d'un cœur, priorité d'entrée-sortie basse, durée limitée à `timeout`) : un gros dépôt ne pèse pas sur l'agent. Borg garde son dossier de travail dans `/var/lib/hypha/borg`.
- restic réessaie plusieurs minutes contre un serveur injoignable : le relevé est arrêté à `timeout` et le contrôle passe en critique.

| Mesure | Sens |
|---|---|
| `borg.last_archive.age` | secondes depuis le début de la dernière archive complète |
| `borg.archives`, `borg.checkpoints` | archives complètes, points de reprise (`.checkpoint`) |
| `borg.repository.last_modified.age` | secondes depuis la dernière écriture dans le dépôt |
| `borg.repository.unique_csize`, `.unique_size`, `.total_size` | avec `stats: true` : place occupée (dédupliquée et compressée), dédupliquée, totale |
| `restic.last_snapshot.age` | secondes depuis le dernier instantané |
| `restic.snapshots`, `restic.host.last_snapshot.age` (`restic_host`) | instantanés, âge du dernier par machine |
| `restic.last_snapshot.files`, `.bytes`, `.added`, `.duration` | dernier instantané (restic 0.17 ou plus) |
| `restic.repository.size`, `.uncompressed_size`, `.compression_ratio` | avec `stats: true` |

Étiquette `repository` (le `name` du dépôt). Contrôles : `borg.can_connect` et `restic.can_connect` (dépôt lisible, critique sinon : serveur injoignable, phrase de passe refusée, fichier mal protégé), `borg.backup` (avertissement : la dernière sauvegarde s'est arrêtée en route, point de reprise plus récent que la dernière archive complète ; critique : aucune archive) et `restic.backup` (critique : aucun instantané).

## Modèles de moniteurs

- **DNS** : Pi-hole injoignable, blocage de Pi-hole coupé, listes de blocage de Pi-hole anciennes, avertissements de Pi-hole, AdGuard Home injoignable, protection d'AdGuard Home coupée, listes de filtrage anciennes ou vides, Unbound injoignable, réponses SERVFAIL d'Unbound, requêtes abandonnées par Unbound, réponses DNSSEC invalides.
- **Web** : API d'administration de Caddy injoignable, serveur amont de Caddy hors service, rechargement de la configuration de Caddy en échec, réponses 5xx de Caddy.
- **Stockage** : grappe MinIO en mauvaise santé, disques ou nœuds MinIO hors ligne, disques en guérison, espace libre MinIO bas, erreurs 5xx de l'API S3.
- **Files de messages** : Mosquitto injoignable, messages MQTT abandonnés, clients MQTT absents.
- **Sauvegardes** : Proxmox Backup Server injoignable, magasin indisponible ou presque plein, sauvegarde PBS en échec, travail PBS en échec, instantanés en échec de vérification, dépôt Borg ou restic illisible, dernière sauvegarde Borg interrompue, dépôt restic sans instantané, et **dernière sauvegarde trop ancienne** pour PBS (par groupe), Borg et restic (par dépôt), seuils en heures (48 h critique, 26 h avertissement par défaut).

## Tableaux prêts

**Tableaux de bord › Catalogue** : Pi-hole, AdGuard Home et Unbound (famille **DNS**), Caddy (**Web**), MinIO (**Stockage**), Mosquitto (**Files de messages**), Proxmox Backup Server, Borg et restic (**Sauvegardes**).

## Ce qui n'a pas été éprouvé

- Grappe MinIO de plusieurs nœuds (essai sur un nœud et un disque) et guérison de disques.
- Proxmox Backup Server : synchronisation entre deux serveurs, bandes, magasins amovibles et espaces de noms (lus par l'API, sans essai réel).
- Pi-hole 6 en HTTPS et double authentification ; AdGuard Home derrière un mandataire.
- Mosquitto en TLS (`mqtts://`) ; autres courtiers MQTT (EMQX, HiveMQ : leurs sujets `$SYS` diffèrent).
- Borg 2 (format et commandes nouvelles) ; dépôts restic sur S3 ou SFTP (seuls un dépôt REST et un chemin local ont été lus).
