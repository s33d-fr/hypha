"""Tableaux prêts de la vague 8 des intégrations (réseau, stockage et sauvegardes auto-hébergés).

Ajoutés en fin de catalogue par `dashboard_catalogue.py` ; les fonctions de construction viennent de ce
module en paramètres (pas d'import circulaire). Chaque métrique et chaque contrôle visés sont vérifiés
contre le code de l'agent par `tests/test_dashboard_catalogue.py` et `tests/test_vague8.py`.
"""


def boards(board, ts, qv, top, checks, WARN80):
    H = "{$host}"
    DNS, WEB, STO, MQ, BAK = "DNS", "Web", "Stockage", "Files de messages", "Sauvegardes"
    return [
        board("pihole", DNS, "Pi-hole", "Requêtes et blocages sur 24 h, état du blocage, clients, listes de blocage (taille, âge), "
              "réponses par type, avertissements de FTL.", ["pihole"], [
                  checks("w1", "Pi-hole", 0, 0, "pihole.can_connect"),
                  checks("w2", "Blocage", 3, 0, "pihole.blocking"),
                  qv("w3", "Bloquées sur 24 h (%)", 6, 0, f"avg:pihole.queries.percent_blocked{H}", "percent", precision=1),
                  qv("w4", "Domaines en liste", 9, 0, f"max:pihole.gravity.domains_blocked{H}", "count"),
                  ts("w5", "Requêtes et blocages (24 h glissantes)", 0, 3, [f"sum:pihole.queries.total{H}", f"sum:pihole.queries.blocked{H}",
                                                                           f"sum:pihole.queries.cached{H}", f"sum:pihole.queries.forwarded{H}"],
                     "count", precision=0),
                  ts("w6", "Requêtes par état", 6, 3, [f"max:pihole.queries.by_status{H} by {{status}}"], "count", display="area", precision=0),
                  ts("w7", "Réponses par type", 0, 7, [f"max:pihole.queries.by_reply{H} by {{reply}}"], "count", display="area", precision=0),
                  ts("w8", "Clients actifs", 6, 7, [f"max:pihole.clients.active{H} by {{host}}"], "count", precision=0),
                  qv("w9", "Âge des listes", 0, 11, f"max:pihole.gravity.age{H}", "second"),
                  qv("w10", "Avertissements de FTL", 3, 11, f"max:pihole.messages{H}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w11", "Mises à jour disponibles", 6, 11, f"max:pihole.updates_available{H}", "count"),
                  qv("w12", "Requêtes par seconde", 9, 11, f"avg:pihole.queries.frequency{H}", "operation_per_second", precision=2),
              ]),
        board("adguard", DNS, "AdGuard Home", "Requêtes et blocages de la fenêtre de statistiques, protection, temps de traitement, "
              "serveurs amont, listes de filtrage (règles, âge).", ["adguard"], [
                  checks("w1", "AdGuard Home", 0, 0, "adguard.can_connect"),
                  checks("w2", "Protection", 3, 0, "adguard.protection"),
                  qv("w3", "Bloquées (%)", 6, 0, f"avg:adguard.queries.percent_blocked{H}", "percent", precision=1),
                  qv("w4", "Règles de filtrage", 9, 0, f"max:adguard.filters.rules{H}", "count"),
                  ts("w5", "Requêtes et blocages (fenêtre de statistiques)", 0, 3,
                     [f"sum:adguard.queries{H}", f"sum:adguard.queries.blocked{H}", f"sum:adguard.queries.safebrowsing{H}",
                      f"sum:adguard.queries.parental{H}"], "count", precision=0),
                  ts("w6", "Temps de traitement moyen (ms)", 6, 3, [f"avg:adguard.processing_time{H} by {{host}}"], "millisecond"),
                  ts("w7", "Temps moyen des serveurs amont (ms)", 0, 7, [f"avg:adguard.upstream.avg_time{H} by {{upstream}}"], "millisecond"),
                  top("w8", "Réponses par serveur amont", 6, 7, f"max:adguard.upstream.responses{H} by {{upstream}}", "count", precision=0),
                  top("w9", "Âge des listes de filtrage", 0, 11, f"max:adguard.filter.age{H} by {{filter}}", "second", precision=0),
                  qv("w10", "Listes vides ou jamais lues", 6, 11, f"max:adguard.filters.empty{H}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w11", "Listes actives", 9, 11, f"max:adguard.filters.enabled{H}", "count"),
              ]),
        board("unbound", DNS, "Unbound", "Requêtes, taux de succès du cache, préchargements, réponses par code et par type, "
              "file des requêtes, temps de récursion, DNSSEC, mémoire et caches.", ["unbound"], [
                  checks("w1", "Unbound", 0, 0, "unbound.can_connect"),
                  qv("w2", "Succès du cache (%)", 3, 0, f"avg:unbound.cache.hit_rate{H}", "percent", precision=1),
                  qv("w3", "Récursion moyenne (s)", 6, 0, f"avg:unbound.recursion.time.avg{H}", "second", precision=3),
                  qv("w4", "En service depuis", 9, 0, f"min:unbound.time.up{H}", "second"),
                  ts("w5", "Requêtes, succès et défauts du cache", 0, 3, [f"sum:unbound.num.queries{H}", f"sum:unbound.num.cachehits{H}",
                                                                         f"sum:unbound.num.cachemiss{H}", f"sum:unbound.num.prefetch{H}"],
                     "count", precision=0),
                  ts("w6", "Réponses par code", 6, 3, [f"sum:unbound.num.answer.rcode{H} by {{rcode}}"], "count", display="bars", precision=0),
                  ts("w7", "File des requêtes", 0, 7, [f"max:unbound.requestlist.current{H}", f"max:unbound.requestlist.max{H}",
                                                       f"sum:unbound.requestlist.exceeded{H}"], "count", precision=0),
                  ts("w8", "DNSSEC : sûres et invalides", 6, 7, [f"sum:unbound.num.answer.secure{H}", f"sum:unbound.num.answer.bogus{H}"],
                     "count", display="bars", precision=0),
                  ts("w9", "Mémoire des caches", 0, 11, [f"max:unbound.mem.cache.rrset{H}", f"max:unbound.mem.cache.message{H}"], "byte"),
                  ts("w10", "Requêtes par type", 6, 11, [f"sum:unbound.num.query.type{H} by {{query_type}}"], "count", display="area", precision=0),
              ]),
        board("caddy", WEB, "Caddy", "Requêtes, réponses par code, durées, erreurs, serveurs amont du mandataire (santé, requêtes "
              "en cours, échecs), rechargement de la configuration.", ["caddy", "openmetrics"], [
                  checks("w1", "API d'administration", 0, 0, "caddy.can_connect"),
                  qv("w2", "Amonts hors service", 3, 0, f"sum:caddy.upstreams.failing{H}", "count", thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w3", "Dernier rechargement réussi", 6, 0, f"min:caddy.config.last_reload_successful{H}", "count"),
                  qv("w4", "Requêtes en cours", 9, 0, f"sum:caddy.http.requests_in_flight{H}", "count"),
                  ts("w5", "Réponses par code", 0, 3, [f"sum:caddy.http.request.duration.count{H} by {{code}}"], "count", display="bars", precision=0),
                  ts("w6", "Durée des requêtes (p95, s)", 6, 3, [f"max:caddy.http.request.duration.p95{H} by {{server}}"], "second", precision=3),
                  ts("w7", "Santé des serveurs amont", 0, 7, [f"min:caddy.reverse_proxy.upstream.healthy{H} by {{upstream}}"], "count", precision=0),
                  ts("w8", "Erreurs des gestionnaires", 6, 7, [f"sum:caddy.http.request_errors{H} by {{server,handler}}"], "count", display="bars", precision=0),
                  top("w9", "Requêtes en cours par amont", 0, 11, f"max:caddy.upstream.requests{H} by {{upstream}}", "count", precision=0),
                  ts("w10", "Mémoire résidente", 6, 11, [f"max:caddy.process.resident_memory{H} by {{host}}"], "byte"),
              ]),
        board("minio", STO, "MinIO", "Santé de la grappe, nœuds et disques, capacité, usage, requêtes S3 et erreurs, trafic, "
              "objets par seau.", ["openmetrics"], [
                  qv("w1", "Santé de la grappe", 0, 0, f"min:minio.cluster.health.status{H}", "count", thresholds=[{"value": 0, "color": "error"}]),
                  qv("w2", "Disques hors ligne", 3, 0, f"max:minio.cluster.drives.offline{H}", "count", thresholds=[{"value": 1, "color": "error"}]),
                  qv("w3", "Nœuds hors ligne", 6, 0, f"max:minio.cluster.nodes.offline{H}", "count", thresholds=[{"value": 1, "color": "error"}]),
                  qv("w4", "Objets", 9, 0, f"max:minio.cluster.usage.objects{H}", "count"),
                  ts("w5", "Capacité utile : libre et totale", 0, 3, [f"min:minio.cluster.capacity.usable.free{H}",
                                                                     f"max:minio.cluster.capacity.usable.total{H}"], "byte"),
                  ts("w6", "Requêtes S3 par API", 6, 3, [f"sum:minio.s3.requests{H} by {{api}}"], "count", display="area", precision=0),
                  ts("w7", "Erreurs S3 (4xx, 5xx)", 0, 7, [f"sum:minio.s3.requests.4xx_errors{H}", f"sum:minio.s3.requests.5xx_errors{H}",
                                                          f"sum:minio.s3.requests.rejected.auth{H}"], "count", display="bars", precision=0),
                  ts("w8", "Trafic S3 (octets)", 6, 7, [f"sum:minio.s3.traffic.received{H}", f"sum:minio.s3.traffic.sent{H}"], "byte"),
                  top("w9", "Octets par seau", 0, 11, f"max:minio.bucket.usage.bytes{H} by {{bucket}}", "byte", precision=0),
                  ts("w10", "Disques en guérison", 6, 11, [f"max:minio.cluster.erasure_set.healing_drives{H} by {{pool,set}}"], "count", precision=0),
              ]),
        board("mosquitto", MQ, "Mosquitto", "Clients connectés, messages et octets reçus et envoyés, messages abandonnés, "
              "abonnements, messages retenus, mémoire du courtier.", ["mosquitto"], [
                  checks("w1", "Mosquitto", 0, 0, "mosquitto.can_connect"),
                  qv("w2", "Clients connectés", 3, 0, f"sum:mosquitto.clients.connected{H}", "count"),
                  qv("w3", "Abonnements", 6, 0, f"sum:mosquitto.subscriptions{H}", "count"),
                  qv("w4", "Messages retenus", 9, 0, f"sum:mosquitto.retained_messages{H}", "count"),
                  ts("w5", "Messages reçus et envoyés", 0, 3, [f"sum:mosquitto.messages.received{H}", f"sum:mosquitto.messages.sent{H}"],
                     "count", precision=0),
                  ts("w6", "Octets reçus et envoyés", 6, 3, [f"sum:mosquitto.bytes.received{H}", f"sum:mosquitto.bytes.sent{H}"], "byte"),
                  ts("w7", "Clients connectés et déconnectés", 0, 7, [f"max:mosquitto.clients.connected{H}", f"max:mosquitto.clients.disconnected{H}"],
                     "count", precision=0),
                  ts("w8", "Messages abandonnés", 6, 7, [f"sum:mosquitto.publish.messages.dropped{H}"], "count", display="bars", precision=0),
                  ts("w9", "Mémoire du courtier", 0, 11, [f"max:mosquitto.heap.current{H} by {{host}}"], "byte"),
                  ts("w10", "Messages stockés", 6, 11, [f"max:mosquitto.store.messages.count{H} by {{host}}"], "count", precision=0),
              ]),
        board("proxmox_backup", BAK, "Proxmox Backup Server", "Magasins (occupation, prévision de remplissage, blocs, "
              "déduplication), dernière sauvegarde de chaque groupe, travaux planifiés, vérifications, tâches en échec, nœud.",
              ["proxmox_backup"], [
                  checks("w1", "Serveur", 0, 0, "proxmox_backup.can_connect"),
                  checks("w2", "Dernière sauvegarde par groupe", 3, 0, "proxmox_backup.backup", w=5),
                  checks("w3", "Travaux planifiés", 8, 0, "proxmox_backup.job", w=4),
                  ts("w4", "Occupation des magasins (%)", 0, 3, [f"max:proxmox_backup.datastore.usage{H} by {{datastore}}"], "percent",
                     thresholds=WARN80),
                  top("w5", "Âge de la dernière sauvegarde", 6, 3, f"max:proxmox_backup.group.last_backup.age{H} by {{backup_type,backup_id}}",
                      "second", precision=0),
                  qv("w6", "Jours avant remplissage", 0, 7, f"min:proxmox_backup.datastore.days_until_full{H}", "count", precision=0),
                  qv("w7", "Instantanés en échec de vérification", 3, 7, f"max:proxmox_backup.datastore.snapshots.verify_failed{H}", "count",
                     thresholds=[{"value": 1, "color": "error"}]),
                  qv("w8", "Blocs défectueux", 6, 7, f"max:proxmox_backup.datastore.bad_chunks{H}", "count",
                     thresholds=[{"value": 1, "color": "error"}]),
                  qv("w9", "Déduplication", 9, 7, f"avg:proxmox_backup.datastore.dedup_factor{H}", None, precision=1),
                  ts("w10", "Tâches en échec par type", 0, 9, [f"sum:proxmox_backup.tasks.failed{H} by {{pbs_task}}"], "count", display="bars", precision=0),
                  ts("w11", "Instantanés par magasin", 6, 9, [f"max:proxmox_backup.datastore.snapshots{H} by {{datastore}}"], "count", precision=0),
                  ts("w12", "Processeur et mémoire du serveur", 0, 13, [f"avg:proxmox_backup.node.cpu{H}"], "percent"),
                  ts("w13", "Espace occupé (octets)", 6, 13, [f"max:proxmox_backup.datastore.used{H} by {{datastore}}",
                                                             f"max:proxmox_backup.datastore.total{H} by {{datastore}}"], "byte"),
              ]),
        board("borg_restic", BAK, "Borg et restic", "Dépôts Borg et restic : âge de la dernière sauvegarde, sauvegardes "
              "interrompues, nombre d'archives et d'instantanés, taille des dépôts.", ["borg", "restic"], [
                  checks("w1", "Dépôts Borg", 0, 0, "borg.can_connect"),
                  checks("w2", "Dernière sauvegarde Borg", 3, 0, "borg.backup"),
                  checks("w3", "Dépôts restic", 6, 0, "restic.can_connect"),
                  checks("w4", "Dernière sauvegarde restic", 9, 0, "restic.backup"),
                  top("w5", "Âge de la dernière archive Borg", 0, 3, f"max:borg.last_archive.age{H} by {{host,repository}}", "second", precision=0),
                  top("w6", "Âge du dernier instantané restic", 6, 3, f"max:restic.last_snapshot.age{H} by {{host,repository}}", "second", precision=0),
                  ts("w7", "Archives Borg et points de reprise", 0, 7, [f"max:borg.archives{H} by {{repository}}", f"max:borg.checkpoints{H} by {{repository}}"],
                     "count", precision=0),
                  ts("w8", "Instantanés restic", 6, 7, [f"max:restic.snapshots{H} by {{repository}}"], "count", precision=0),
                  ts("w9", "Taille des dépôts Borg (stats)", 0, 11, [f"max:borg.repository.unique_csize{H} by {{repository}}"], "byte"),
                  ts("w10", "Taille des dépôts restic (stats)", 6, 11, [f"max:restic.repository.size{H} by {{repository}}"], "byte"),
                  ts("w11", "restic : octets ajoutés par la dernière sauvegarde", 0, 15, [f"max:restic.last_snapshot.added{H} by {{repository}}"], "byte"),
                  ts("w12", "restic : durée de la dernière sauvegarde (s)", 6, 15, [f"max:restic.last_snapshot.duration{H} by {{repository}}"], "second"),
              ]),
    ]
