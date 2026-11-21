---
id: self-hosted-apps
path: /docs/applications-auto-hebergees/
title: Applications auto-hébergées : Nextcloud, Jellyfin, Syncthing, Immich, Forgejo, Matrix, Home Assistant, Mastodon
nav: Applications auto-hébergées
section: Intégrations
order: 29
updated: 2026-11-21
description: Superviser avec HYPHA les applications auto-hébergées les plus répandues : Nextcloud, Jellyfin, Syncthing, Immich, Gitea et Forgejo, Matrix Synapse, Home Assistant et Mastodon, par leur API ou leurs mesures Prometheus.
lead: Huit applications que l'on héberge chez soi, lues par leur interface native : santé, utilisateurs, files de tâches, stockage, synchronisation, avec des modèles de moniteurs et un tableau prêt pour chacune.
---
## En un coup d'œil

Chaque application est lue par le moyen le plus direct qu'elle offre : son API quand ses mesures Prometheus ne disent pas l'essentiel (Nextcloud, Jellyfin, Syncthing, Immich), sinon un profil du [lecteur OpenMetrics](/docs/collecte-generique/). Tout a été éprouvé contre le vrai logiciel lancé en conteneur dans un environnement d'essai de S33D (dernière colonne).

| Application | Collecte | Activation | Espace de noms | Éprouvé sur |
|---|---|---|---|---|
| Nextcloud | sonde : `status.php` et API serverinfo | dès que `url` est posée | `nextcloud.*` | Nextcloud 35.0.3 (serverinfo 7.0.0) |
| Jellyfin | sonde : `/health` et API du serveur | auto sur `127.0.0.1:8096` | `jellyfin.*` | Jellyfin 12.4.0 |
| Syncthing | sonde : API REST | auto sur `127.0.0.1:8384` | `syncthing.*` | Syncthing 2.1.7 (deux appareils reliés) |
| Immich | sonde : API du serveur | dès que `url` est posée | `immich.*` | Immich 3.4.2 |
| Gitea et Forgejo | profil `gitea` ou `forgejo` (`/metrics`) | à déclarer | `gitea.*` | Gitea 28.4.0, Forgejo 13.0.5 |
| Matrix Synapse | profil `synapse` (`/_synapse/metrics`) | à déclarer | `synapse.*` | Synapse 1.165.0 |
| Home Assistant | profil `homeassistant` (`/api/prometheus`) | à déclarer | `homeassistant.*` | Home Assistant 2026.11.2 |
| Mastodon | profils `mastodon` et `mastodon_streaming` | à déclarer | `mastodon.*`, `mastodon_streaming.*` | Mastodon 4.7.6 (web, Sidekiq, streaming) |

Les sondes ne font que lire : aucune écriture, aucune tâche lancée, aucun appel qui ferait contacter un service extérieur par l'application. Les clés se posent de préférence dans un fichier lisible par l'agent seul (`api_key_file`, `token_file`), relu à chaque relevé : une clé changée est prise sans relancer l'agent. `hypha-agent check-config` les affiche masquées.

```yaml
integrations:
  nextcloud: {url: "https://cloud.exemple", token_file: /etc/hypha/nextcloud.token}
  jellyfin: {api_key_file: /etc/hypha/jellyfin.key}          # url par défaut : http://127.0.0.1:8096
  syncthing: {api_key_file: /etc/hypha/syncthing.key}        # url par défaut : http://127.0.0.1:8384
  immich: {url: "http://127.0.0.1:2283", api_key_file: /etc/hypha/immich.key}
  openmetrics:
    - profile: forgejo                                       # ou gitea
      bearer_token_file: /etc/hypha/forgejo-metrics.token
    - profile: synapse
    - profile: homeassistant
      bearer_token_file: /etc/hypha/homeassistant.token
    - {profile: mastodon, name: mastodon-web, url: "http://127.0.0.1:9394/metrics"}
    - {profile: mastodon, name: mastodon-sidekiq, url: "http://127.0.0.1:9395/metrics"}
    - profile: mastodon_streaming
```

Les quatre sondes acceptent des `instances` (plusieurs Nextcloud ou Syncthing sur une machine, chacun avec son `name`, étiquette `instance_name`).

## Nextcloud

La sonde lit `status.php` sans compte (installé, mode maintenance, mise à niveau de la base en attente, version), puis l'API de l'application **serverinfo** (livrée et active d'office). Le plus simple est un jeton propre à serverinfo, qui ne donne accès qu'à ces statistiques :

```sh
sudo -u www-data php occ config:app:set serverinfo token --value "$(openssl rand -hex 24)"
```

Le même jeton va dans `token_file` (en-tête `NC-Token`). À défaut, un compte administrateur et un mot de passe d'application (`username`, `password`).

- Contrôles : `nextcloud.can_connect`, `nextcloud.status` (critique en mode maintenance, quand la base attend `occ upgrade` ou si l'installation n'est pas finie ; serverinfo n'est alors pas interrogé).
- `nextcloud.maintenance`, `nextcloud.needs_db_upgrade`, `nextcloud.info` (étiquette `version`).
- `nextcloud.users`, `nextcloud.users.disabled`, `nextcloud.users.active` (étiquette `period` : `5m`, `1h`, `24h`, `7d`, `30d`), `nextcloud.files`, `nextcloud.storages`.
- `nextcloud.shares`, `nextcloud.shares.by_type` (`user`, `group`, `link`, `mail`, `room`, `federated_sent`, `federated_received`), `nextcloud.shares.link_no_password`.
- `nextcloud.storage.free` (place libre du dossier de données), `nextcloud.database.size` (étiquette `db_type`).
- PHP : `nextcloud.php.memory_limit`, `nextcloud.php.opcache.hit_rate`, `.opcache.memory.used`, `.memory.free`, `.memory.wasted`, `nextcloud.php.opcache.full`, `nextcloud.php.opcache.oom_restarts`, `nextcloud.php.apcu.hit_rate`, `nextcloud.php.apcu.memory.available`.

Les comptes de fichiers et de stockages sont ceux que serverinfo tient à jour par sa tâche de fond (`occ serverinfo:update-storage-statistics` les rafraîchit). Les mises à jour d'applications ne sont pas lues : la sonde garde les réglages par défaut de serverinfo (`skipApps`, `skipUpdate`), pour ne jamais faire interroger le magasin d'applications par Nextcloud.

Nextcloud 35 (vu dans l'essai) publie aussi `/metrics` au format OpenMetrics (comptes actifs, sessions, tâches de fond), réservé par défaut à la boucle locale (`openmetrics_allowed_clients` dans `config.php`) : le lecteur générique peut le lire, la sonde suffit pour l'essentiel.

## Jellyfin

Sans clé, la sonde lit `/health` (`Healthy`, `Degraded`, `Unhealthy`) et la version publique. Avec une clé d'API (**Tableau de bord › Clés d'API**), elle lit aussi les sessions, les bibliothèques et les tâches planifiées. Jellyfin 12.4 n'accepte la clé que dans l'en-tête `Authorization: MediaBrowser Token="…"` (`X-Emby-Token` et `?api_key=` sont refusés) : c'est ce que fait l'agent.

- Contrôles : `jellyfin.can_connect`, `jellyfin.health` (avertissement si `Degraded`, critique si `Unhealthy`), `jellyfin.scheduled_tasks` (avertissement quand la dernière exécution d'une tâche a échoué, avec son nom et son message).
- `jellyfin.healthy`, `jellyfin.info` (`version`), `jellyfin.pending_restart`, `jellyfin.update_available`.
- Sessions actives dans les 16 dernières minutes : `jellyfin.sessions.active`, `.sessions.playing`, `.sessions.paused`, `.sessions.transcoding` ; `jellyfin.streams` (étiquette `play_method` : `directplay`, `directstream`, `transcode`).
- `jellyfin.library.items` (étiquette `type` : `movie`, `series`, `episode`, `song`, `album`, `artist`, `book`…), `jellyfin.library.items_total`.
- `jellyfin.scheduled_tasks.failed`, `jellyfin.scheduled_tasks.running`.

Le `/metrics` de Jellyfin (`EnableMetrics` dans `system.xml`) ne publie que des mesures du moteur .NET et des requêtes HTTP : le lecteur générique peut le lire en plus.

## Syncthing

Sans clé, la sonde lit `/rest/noauth/health`. Avec la clé de l'interface (**Actions › Configuration › Interface graphique › Clé d'API**, en-tête `X-API-Key`), elle lit chaque minute les appareils, les connexions, les dossiers et les erreurs. `/rest/db/status` étant coûteux pour Syncthing, la sonde en fait une requête par dossier et par minute, et s'arrête à 100 dossiers.

- Contrôles : `syncthing.can_connect` ; `syncthing.folder` par dossier (étiquettes `folder`, `folder_label`, `folder_type`) : critique en erreur (marqueur `.stfolder` absent, droits, disque), avertissement quand des fichiers ne peuvent pas être reçus ou que la surveillance des changements échoue, OK en pause ; `syncthing.device` par appareil distant (étiquettes `device`, `device_id` sur 7 caractères) : OK connecté ou en pause, avertissement déconnecté, avec la date à laquelle il a été vu pour la dernière fois.
- Dossiers : `syncthing.folder.need_files`, `.need_bytes`, `.global_files`, `.global_bytes`, `.local_files`, `.local_bytes`, `.completion` (%), `.pull_errors`, `.error` ; `syncthing.folders` (étiquette `state` : `idle`, `scanning`, `syncing`, `error`, `paused`…).
- Appareils : `syncthing.device.connected`, `syncthing.device.last_seen_age` (s, 0 quand il est connecté), `syncthing.device.transfer.in` et `.transfer.out` (octets) ; `syncthing.devices` (étiquette `state` : `connected`, `disconnected`, `paused`).
- `syncthing.transfer.in`, `syncthing.transfer.out`, `syncthing.uptime`, `syncthing.goroutines`, `syncthing.memory.alloc`, `syncthing.memory.sys`, `syncthing.system.errors`, `syncthing.listeners.errors`, `syncthing.discovery.errors`, `syncthing.info` (`version`).

Syncthing 2.1 publie aussi `/metrics` (clé exigée) : états et volumes des dossiers sous forme de codes, sans les erreurs ni les appareils absents ; la sonde REST les donne en clair.

## Immich

Sans clé, la sonde lit `/api/server/ping` et la version. Avec une clé d'API (**Paramètres du compte › Clés d'API**) portant les permissions `server.storage`, `server.statistics` et `job.read` (et rien d'autre), elle lit le disque de la photothèque, le nombre de photos et de vidéos et les files de tâches.

- Contrôles : `immich.can_connect`, `immich.queues` (avertissement quand une file est en pause ou porte des tâches en échec, avec leur nom).
- `immich.storage.size`, `.storage.used`, `.storage.available` (octets), `immich.storage.usage` (%).
- `immich.assets` et `immich.usage.by_type` (étiquette `type` : `photo`, `video`), `immich.usage`, `immich.users` ; par compte (étiquette `user`) : `immich.user.usage`, `immich.user.assets`, et avec un quota `immich.user.quota`, `immich.user.quota_usage` (%).
- `immich.jobs` (étiquettes `queue` : `thumbnail_generation`, `metadata_extraction`, `video_conversion`… et `state` : `active`, `waiting`, `delayed`, `failed`, `paused`), `immich.queue.paused`, `immich.info` (`version`).

Les mesures Prometheus d'Immich (`IMMICH_TELEMETRY_INCLUDE=all`, ports 8081 et 8082) donnent surtout des durées internes et l'activité des files, sans le disque ni l'état des files : le lecteur générique peut les lire en plus. Piège vu sur Immich 3.4.2 : avec l'adresse d'écoute par défaut (`::`), ces deux ports répondent « 400 Bad Request » à toute requête ; poser `IMMICH_HOST=0.0.0.0` les rend lisibles.

## Gitea et Forgejo

Dans `app.ini` : `[metrics] ENABLED = true` (ou `GITEA__metrics__ENABLED=true`, `FORGEJO__metrics__ENABLED=true` en conteneur). Avec `[metrics] TOKEN = …`, le jeton va dans `bearer_token_file`. Forgejo publie les mêmes familles `gitea_*` que Gitea : les profils `gitea` et `forgejo` sont identiques et donnent les mêmes noms `gitea.*`, donc les mêmes tableaux et moniteurs (`instance_name` les distingue).

- `gitea.repositories`, `gitea.users` (étiquette `state` : `active`, `inactive` chez Gitea), `gitea.organizations`, `gitea.teams`, `gitea.issues`, `gitea.issues.open`, `gitea.issues.closed`, `gitea.milestones`, `gitea.labels`, `gitea.comments`, `gitea.releases`, `gitea.mirrors`, `gitea.stars`, `gitea.watches`, `gitea.follows`, `gitea.webhooks`, `gitea.hook_tasks`, `gitea.public_keys`, `gitea.oauth_apps`, `gitea.login_sources`, `gitea.projects`, `gitea.projects.boards`, `gitea.attachments`, `gitea.accesses`, `gitea.update_tasks`.
- `gitea.build_info` (étiquette `version`), `gitea.process.resident_memory`, `gitea.process.cpu_seconds`, `gitea.process.open_fds`, `gitea.go.goroutines`.

Ce sont des comptes de la base (jauges) : la santé de la forge se suit par `openmetrics.can_connect` et un test synthétique HTTP sur l'interface.

## Matrix Synapse

Dans `homeserver.yaml` : `enable_metrics: true` et un écouteur dédié, lié à la boucle locale.

```yaml
listeners:
  - port: 9000
    type: metrics
    bind_addresses: ['127.0.0.1']
```

- `synapse.http.requests` (étiquettes `method`, `servlet`), `synapse.http.responses` (`code`), `synapse.http.response_time` (s, `.p50`, `.p95`, `.p99`, `.count`), `synapse.http.in_flight`.
- `synapse.events.persisted`, `synapse.event_processing.lag` (ms, étiquette `name` : `federation_sender`, `appservice_sender`…).
- Fédération : `synapse.federation.pending_pdus`, `.pending_edus`, `.pending_destinations`, `.sent_transactions`, `.received_pdus`, `.staged_pdus`, `.oldest_staged_pdu`, `.known_servers`.
- Utilisateurs : `synapse.users.mau`, `.mau_max`, `.daily_active`, `.non_deactivated`, `.registrations`, `.logins`.
- `synapse.db.schedule_time` (attente d'une connexion à la base), `synapse.push.processed`, `synapse.push.failed`, `synapse.rate_limit.rejected`, `synapse.rate_limit.slept`, `synapse.pushers`, `synapse.notifier.rooms`, `.users`, `.listeners`, `synapse.threadpool.working`, `synapse.background_update.status`, `synapse.background_processes.in_flight`, `synapse.scheduler.running_tasks`, `synapse.appservice.failed_transactions`, `synapse.build_info` (`version`), `synapse.process.resident_memory`, `.cpu_seconds`, `.open_fds`.

La santé publique de Synapse se suit en plus par un test synthétique HTTP sur `/health` (réponse `OK`) de l'écouteur client.

## Home Assistant

Dans `configuration.yaml` : `prometheus:` (l'intégration Prometheus native). Sa route `/api/prometheus` demande un jeton d'accès longue durée (**Profil › Sécurité › Jetons d'accès longue durée**), posé dans `bearer_token_file`.

- Par entité (étiquettes `entity`, `domain`, `friendly_name`) : `homeassistant.entity.available` (0 = indisponible), `homeassistant.entity.state_changes`.
- Capteurs : `homeassistant.sensor.temperature` (°C), `.sensor.humidity` (%), `.sensor.battery` (%), `.sensor.power` (W), `.sensor.energy` (kWh), `.sensor.co2` (ppm) ; `homeassistant.binary_sensor.state`, `homeassistant.switch.state`, `homeassistant.light.brightness`, `homeassistant.cover.position`, `homeassistant.lock.state`, `homeassistant.input_boolean.state`, `homeassistant.device_tracker.state`, `homeassistant.alarm.state` (étiquette `state`).
- Thermostats : `homeassistant.climate.current_temperature`, `.climate.target_temperature`, `.climate.action` (étiquette `action`).
- `homeassistant.update.available` (1 = une mise à jour attend), `homeassistant.process.resident_memory`, `.cpu_seconds`, `.open_fds`.

Une maison de plusieurs centaines d'entités dépasse le plafond de 2 000 séries d'une cible (deux séries par entité au moins) : relever `max_series` dans l'entrée, ou écarter des domaines dans la conf de l'intégration Prometheus de Home Assistant (`filter`). Les autres familles (`homeassistant_sensor_unit_*`, dates de mise à jour) s'ajoutent par `metrics` et `rename`.

## Mastodon

Variables d'environnement de Mastodon (vues sur la 4.7.6) : `MASTODON_PROMETHEUS_EXPORTER_ENABLED=true`, et soit `MASTODON_PROMETHEUS_EXPORTER_LOCAL=true` (le web et Sidekiq servent chacun leur `/metrics` sur `MASTODON_PROMETHEUS_EXPORTER_PORT`, 9394 par défaut : un port par service quand ils tournent sur la même machine), soit le processus `bin/prometheus_exporter` qui les regroupe. `MASTODON_PROMETHEUS_EXPORTER_SIDEKIQ_DETAILED_METRICS=true` ajoute les files de Sidekiq (taille, retard), `MASTODON_PROMETHEUS_EXPORTER_WEB_DETAILED_METRICS=true` les requêtes HTTP. Le serveur de streaming publie `/metrics` sur son port (4000) sans réglage.

- Puma : `mastodon.puma.workers`, `.booted_workers`, `.running_threads`, `.busy_threads`, `.thread_pool_capacity`, `.max_threads`, `.request_backlog`.
- HTTP (détaillé) : `mastodon.http.requests` (étiquette `status`), `mastodon.http.request_duration`, `.sql_duration`, `.redis_duration` (résumés en s : `.avg`, `.p50`, `.p90`, `.p99`), `mastodon.http.queue_duration` (avec l'en-tête `X-Request-Start` du relais).
- Sidekiq : `mastodon.sidekiq.jobs`, `.failed_jobs`, `.dead_jobs`, `.restarted_jobs` (étiquette `queue`), `mastodon.sidekiq.job_duration`, `mastodon.sidekiq.queue.backlog` et `.queue.latency` (s, par `queue`), `mastodon.sidekiq.process.busy`, `.process.concurrency`, `mastodon.sidekiq.stats.enqueued`, `.retry`, `.dead`, `.scheduled`, `.failed`, `.processed`, `.processes`, `.workers`.
- `mastodon.db_pool.busy`, `.size`, `.waiting`, `.dead`, `mastodon.process.rss` (étiquette `type`), `mastodon.ruby.heap_live_slots`, `mastodon.ruby.major_gc`, `mastodon.collector.working`.
- Streaming : `mastodon_streaming.clients` et `.messages_sent` (étiquette `type` : `websocket`, `eventsource`), `.channels`, `.redis.subscriptions`, `.redis.messages_received`, `.pg_pool.total`, `.pg_pool.idle`, `.pg_pool.waiting`, `.eventloop.lag`, `.eventloop.lag_p99`, `.process.resident_memory`.

Les mesures de Puma et des files de Sidekiq sont relevées par Mastodon à intervalle : juste après un redémarrage, elles manquent quelques instants. La santé publique se suit en plus par un test synthétique HTTP sur `/health` (réponse `OK`) et `/api/v1/streaming/health`.

## Modèles de moniteurs

Dans **Moniteurs › Nouveau › Depuis un modèle**, famille **Applications auto-hébergées** :

- **Nextcloud** : injoignable, hors service (maintenance, mise à niveau en attente), place libre (Gio), OPcache plein.
- **Jellyfin** : injoignable, en mauvaise santé, tâches planifiées en échec, transcodages simultanés.
- **Syncthing** : injoignable, dossier en défaut, appareil déconnecté, appareil absent depuis N heures, dossier en retard depuis une heure.
- **Immich** : injoignable, files de tâches bloquées (pause, échecs), disque de la photothèque, tâches en attente.
- **Gitea et Forgejo**, **Synapse**, **Home Assistant**, **Mastodon** : cible injoignable (par `instance_name`) ; retard du traitement des événements, file de fédération et requêtes lentes de Synapse ; entité indisponible, pile faible et mise à jour disponible dans Home Assistant ; files Sidekiq en retard, tâches à relancer et requêtes en attente dans Puma pour Mastodon.

## Tableaux prêts

**Tableaux de bord › Catalogue**, famille **Applications auto-hébergées** : Nextcloud, Jellyfin, Syncthing, Immich, Gitea et Forgejo, Matrix Synapse, Home Assistant, Mastodon (avec le streaming), installés d'un clic puis modifiables comme des tableaux ordinaires.

## Ce qui a été éprouvé

Chaque application a tourné en conteneur dans l'environnement d'essai de S33D, avec des comptes et des données d'essai créés pour l'occasion. L'agent l'a d'abord lue à blanc, puis l'agent 0.11 de l'hôte d'essai a envoyé ses mesures à HYPHA : tous les contrôles et toutes les mesures des tableaux prêts y ont été vus, sauf `synapse.push.processed` et `synapse.push.failed` (aucune passerelle de notifications poussées dans l'essai).

Pannes provoquées, vues puis revenues à la normale : Nextcloud en mode maintenance (`nextcloud.status` critique) ; Jellyfin, Home Assistant et le Sidekiq de Mastodon arrêtés (critique) ; second Syncthing arrêté (`syncthing.can_connect` critique, et sur le premier, appareil déconnecté en avertissement avec sa dernière date de connexion) ; dossier Syncthing privé de son marqueur `.stfolder` (critique) ; file d'Immich mise en pause (avertissement).

Restent à voir sur une installation réelle : transcodage de Jellyfin, poussées de Synapse et fédération entre deux serveurs Matrix, maison Home Assistant de plusieurs centaines d'entités (l'essai en comptait 127), Mastodon fédéré sous charge, quotas d'Immich ; les tâches planifiées en échec de Jellyfin et les tâches en échec d'Immich ne sont éprouvées qu'en tests, sur des réponses réelles modifiées.
