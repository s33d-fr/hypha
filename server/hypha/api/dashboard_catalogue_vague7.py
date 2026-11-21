"""Tableaux prêts des applications auto-hébergées (vague 7 des intégrations).

Ajoutés en fin de catalogue par `dashboard_catalogue.py` ; les fonctions de construction viennent de
ce module en paramètres (pas d'import circulaire). Chaque métrique et chaque contrôle visés sont
vérifiés contre le code de l'agent par `tests/test_dashboard_catalogue.py` et `tests/test_vague7.py`.
"""


def boards(board, ts, qv, top, checks, WARN80):
    H = "{$host}"
    FAM = "Applications auto-hébergées"
    return [
        board("nextcloud", FAM, "Nextcloud", "État du service (maintenance, mise à niveau en attente), comptes et comptes actifs, "
              "fichiers, partages par type, place libre, taille de la base, OPcache et APCu de PHP.", ["nextcloud"], [
                  checks("w1", "Joignable", 0, 0, "nextcloud.can_connect"),
                  checks("w2", "En service", 3, 0, "nextcloud.status"),
                  qv("w3", "Comptes", 6, 0, f"max:nextcloud.users{H}", "count"),
                  qv("w4", "Actifs (5 min)", 9, 0, f"max:nextcloud.users.active{{$host,period:5m}}", "count"),
                  qv("w5", "Fichiers", 0, 3, f"max:nextcloud.files{H}", "count"),
                  qv("w6", "Place libre", 3, 3, f"min:nextcloud.storage.free{H}", "byte"),
                  qv("w7", "Taille de la base", 6, 3, f"max:nextcloud.database.size{H}", "byte"),
                  qv("w8", "OPcache (succès %)", 9, 3, f"avg:nextcloud.php.opcache.hit_rate{H}", "percent", precision=1),
                  ts("w9", "Comptes actifs par période", 0, 5, [f"max:nextcloud.users.active{H} by {{period}}"], "count", precision=0),
                  ts("w10", "Partages par type", 6, 5, [f"max:nextcloud.shares.by_type{H} by {{type}}"], "count", display="area", precision=0),
                  ts("w11", "Place libre", 0, 9, [f"min:nextcloud.storage.free{H} by {{host,instance_name}}"], "byte"),
                  ts("w12", "Mémoire OPcache (utilisée, libre, perdue)", 6, 9, [f"max:nextcloud.php.opcache.memory.used{H}",
                                                                              f"max:nextcloud.php.opcache.memory.free{H}",
                                                                              f"max:nextcloud.php.opcache.memory.wasted{H}"], "byte"),
              ]),
        board("jellyfin", FAM, "Jellyfin", "Santé du serveur, lectures en cours, pauses et transcodages, mode de lecture, "
              "contenu des bibliothèques, tâches planifiées en échec, redémarrage ou mise à jour en attente.", ["jellyfin"], [
                  checks("w1", "Joignable", 0, 0, "jellyfin.can_connect"),
                  checks("w2", "Santé", 3, 0, "jellyfin.health"),
                  checks("w3", "Tâches planifiées", 6, 0, "jellyfin.scheduled_tasks"),
                  qv("w4", "Lectures en cours", 9, 0, f"max:jellyfin.sessions.playing{H}", "count"),
                  ts("w5", "Sessions actives, lectures, transcodages", 0, 3, [f"max:jellyfin.sessions.active{H}",
                                                                           f"max:jellyfin.sessions.playing{H}",
                                                                           f"max:jellyfin.sessions.transcoding{H}"], "count", precision=0),
                  ts("w6", "Lectures par mode", 6, 3, [f"max:jellyfin.streams{H} by {{play_method}}"], "count", display="area", precision=0),
                  top("w7", "Contenu des bibliothèques", 0, 7, f"max:jellyfin.library.items{H} by {{type}}", "count", precision=0),
                  qv("w8", "Tâches en échec", 6, 7, f"max:jellyfin.scheduled_tasks.failed{H}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w9", "Redémarrage en attente", 9, 7, f"max:jellyfin.pending_restart{H}", "count"),
              ]),
        board("syncthing", FAM, "Syncthing", "Appareils connectés ou absents, dossiers (état, erreurs, fichiers à recevoir, "
              "progression), octets échangés, erreurs du système et de la découverte.", ["syncthing"], [
                  checks("w1", "Joignable", 0, 0, "syncthing.can_connect"),
                  checks("w2", "Dossiers", 3, 0, "syncthing.folder"),
                  checks("w3", "Appareils", 6, 0, "syncthing.device"),
                  qv("w4", "Appareils connectés", 9, 0, f"max:syncthing.devices{{$host,state:connected}}", "count"),
                  top("w5", "Progression des dossiers (%)", 0, 3, f"min:syncthing.folder.completion{H} by {{folder_label}}", "percent"),
                  ts("w6", "Fichiers à recevoir", 6, 3, [f"max:syncthing.folder.need_files{H} by {{folder_label}}"], "count", precision=0),
                  ts("w7", "Octets reçus et envoyés", 0, 7, [f"sum:syncthing.transfer.in{H}", f"sum:syncthing.transfer.out{H}"], "byte",
                     display="bars"),
                  top("w8", "Dernière connexion (s)", 6, 7, f"max:syncthing.device.last_seen_age{H} by {{device}}", "second", precision=0),
                  ts("w9", "Échecs de réception et erreurs", 0, 11, [f"max:syncthing.folder.pull_errors{H} by {{folder_label}}",
                                                                     f"max:syncthing.system.errors{H}"], "count", precision=0),
                  ts("w10", "Taille des dossiers", 6, 11, [f"max:syncthing.folder.global_bytes{H} by {{folder_label}}"], "byte"),
              ]),
        board("immich", FAM, "Immich", "Disque de la photothèque, photos et vidéos, place prise par compte, files de tâches "
              "(actives, en attente, en échec, en pause).", ["immich"], [
                  checks("w1", "Joignable", 0, 0, "immich.can_connect"),
                  checks("w2", "Files de tâches", 3, 0, "immich.queues"),
                  qv("w3", "Disque occupé (%)", 6, 0, f"max:immich.storage.usage{H}", "percent", precision=1, thresholds=WARN80),
                  qv("w4", "Photos", 9, 0, f"max:immich.assets{{$host,type:photo}}", "count"),
                  ts("w5", "Disque de la photothèque", 0, 3, [f"max:immich.storage.used{H}", f"max:immich.storage.available{H}"], "byte",
                     display="area"),
                  top("w6", "Place prise par compte", 6, 3, f"max:immich.user.usage{H} by {{user}}", "byte"),
                  ts("w7", "Tâches en attente par file", 0, 7, [f"max:immich.jobs{{$host,state:waiting}} by {{queue}}"], "count", precision=0),
                  ts("w8", "Tâches actives et en échec", 6, 7, [f"sum:immich.jobs{{$host,state:active}}",
                                                               f"sum:immich.jobs{{$host,state:failed}}"], "count", precision=0),
              ]),
        board("gitea", FAM, "Gitea et Forgejo", "Dépôts, comptes par état, organisations, tickets ouverts et fermés, miroirs, "
              "versions publiées, mémoire et goroutines du serveur.", ["openmetrics:gitea", "openmetrics:forgejo"], [
                  checks("w1", "Cible joignable", 0, 0, "openmetrics.can_connect"),
                  qv("w2", "Dépôts", 3, 0, f"max:gitea.repositories{H}", "count"),
                  qv("w3", "Comptes", 6, 0, f"sum:gitea.users{H}", "count"),
                  qv("w4", "Tickets ouverts", 9, 0, f"max:gitea.issues.open{H}", "count"),
                  ts("w5", "Dépôts, organisations, miroirs", 0, 3, [f"max:gitea.repositories{H} by {{instance_name}}",
                                                                   f"max:gitea.organizations{H} by {{instance_name}}",
                                                                   f"max:gitea.mirrors{H} by {{instance_name}}"], "count", precision=0),
                  ts("w6", "Tickets ouverts et fermés", 6, 3, [f"max:gitea.issues.open{H}", f"max:gitea.issues.closed{H}"], "count",
                     precision=0),
                  ts("w7", "Mémoire résidente", 0, 7, [f"max:gitea.process.resident_memory{H} by {{host,instance_name}}"], "byte"),
                  ts("w8", "Goroutines", 6, 7, [f"max:gitea.go.goroutines{H} by {{host,instance_name}}"], "count", precision=0),
              ]),
        board("synapse", FAM, "Matrix Synapse", "Requêtes et réponses, temps de réponse (95e centile), événements écrits, retard du "
              "traitement des événements, file de fédération, utilisateurs actifs, poussées en échec, mémoire.", ["openmetrics:synapse"], [
                  checks("w1", "Cible joignable", 0, 0, "openmetrics.can_connect"),
                  qv("w2", "Actifs (jour)", 3, 0, f"max:synapse.users.daily_active{H}", "count"),
                  qv("w3", "Comptes", 6, 0, f"max:synapse.users.non_deactivated{H}", "count"),
                  qv("w4", "PDU à fédérer", 9, 0, f"max:synapse.federation.pending_pdus{H}", "count"),
                  ts("w5", "Requêtes par servlet", 0, 3, [f"sum:synapse.http.requests{H} by {{servlet}}"], "operation_per_second"),
                  ts("w6", "Temps de réponse (p95, s)", 6, 3, [f"max:synapse.http.response_time.p95{H} by {{host}}"], "second"),
                  ts("w7", "Événements écrits", 0, 7, [f"sum:synapse.events.persisted{H}"], "operation_per_second"),
                  ts("w8", "Retard du traitement des événements (ms)", 6, 7, [f"max:synapse.event_processing.lag{H} by {{name}}"],
                     "millisecond", precision=0),
                  ts("w9", "Poussées traitées et en échec", 0, 11, [f"sum:synapse.push.processed{H}", f"sum:synapse.push.failed{H}"],
                     "operation_per_second"),
                  ts("w10", "Mémoire résidente", 6, 11, [f"max:synapse.process.resident_memory{H} by {{host}}"], "byte"),
              ]),
        board("homeassistant", FAM, "Home Assistant", "Entités disponibles, changements d'état par domaine, températures, piles, "
              "puissance, mises à jour disponibles, mémoire.", ["openmetrics:homeassistant"], [
                  checks("w1", "Cible joignable", 0, 0, "openmetrics.can_connect"),
                  qv("w2", "Entités disponibles", 3, 0, f"sum:homeassistant.entity.available{H}", "count"),
                  qv("w3", "Mises à jour disponibles", 6, 0, f"sum:homeassistant.update.available{H}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w4", "Pile la plus basse (%)", 9, 0, f"min:homeassistant.sensor.battery{H}", "percent"),
                  ts("w5", "Changements d'état par domaine", 0, 3, [f"sum:homeassistant.entity.state_changes{H} by {{domain}}"],
                     "operation_per_second", display="area"),
                  ts("w6", "Températures (°C)", 6, 3, [f"avg:homeassistant.sensor.temperature{H} by {{friendly_name}}"]),
                  top("w7", "Piles", 0, 7, f"min:homeassistant.sensor.battery{H} by {{friendly_name}}", "percent", precision=0),
                  ts("w8", "Puissance (W)", 6, 7, [f"avg:homeassistant.sensor.power{H} by {{friendly_name}}"]),
                  ts("w9", "Mémoire résidente", 0, 11, [f"max:homeassistant.process.resident_memory{H} by {{host}}"], "byte", w=12),
              ]),
        board("mastodon", FAM, "Mastodon", "Puma (fils occupés, file d'attente), requêtes et leur durée, Sidekiq (tâches par file, "
              "retard des files, tâches à relancer et mortes), pool de la base, serveur de streaming (clients, messages).",
              ["openmetrics:mastodon", "openmetrics:mastodon_streaming"], [
                  checks("w1", "Cibles joignables", 0, 0, "openmetrics.can_connect"),
                  qv("w2", "Requêtes en attente (Puma)", 3, 0, f"max:mastodon.puma.request_backlog{H}", "count",
                     thresholds=[{"value": 5, "color": "warn"}]),
                  qv("w3", "Tâches à relancer", 6, 0, f"max:mastodon.sidekiq.stats.retry{H}", "count"),
                  qv("w4", "Clients du streaming", 9, 0, f"sum:mastodon_streaming.clients{H}", "count"),
                  ts("w5", "Requêtes par code", 0, 3, [f"sum:mastodon.http.requests{H} by {{status}}"], "operation_per_second"),
                  ts("w6", "Durée des requêtes (moyenne, s)", 6, 3, [f"avg:mastodon.http.request_duration.avg{H} by {{host}}"], "second"),
                  ts("w7", "Tâches Sidekiq par file", 0, 7, [f"sum:mastodon.sidekiq.jobs{H} by {{queue}}"], "operation_per_second",
                     display="area"),
                  ts("w8", "Retard des files Sidekiq (s)", 6, 7, [f"max:mastodon.sidekiq.queue.latency{H} by {{queue}}"], "second"),
                  ts("w9", "Fils de Puma (occupés, capacité)", 0, 11, [f"max:mastodon.puma.busy_threads{H}",
                                                                       f"max:mastodon.puma.thread_pool_capacity{H}"], "count", precision=0),
                  ts("w10", "Tâches à relancer, mortes, programmées", 6, 11, [f"max:mastodon.sidekiq.stats.retry{H}",
                                                                              f"max:mastodon.sidekiq.stats.dead{H}",
                                                                              f"max:mastodon.sidekiq.stats.scheduled{H}"], "count",
                     precision=0),
                  ts("w11", "Streaming : clients et messages", 0, 15, [f"sum:mastodon_streaming.clients{H} by {{type}}",
                                                                      f"sum:mastodon_streaming.messages_sent{H} by {{type}}"], "count"),
                  ts("w12", "Pool de la base (occupé, taille)", 6, 15, [f"max:mastodon.db_pool.busy{H}", f"max:mastodon.db_pool.size{H}"],
                     "count", precision=0),
              ]),
    ]
