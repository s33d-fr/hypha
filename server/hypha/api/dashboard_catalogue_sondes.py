"""Tableaux prêts des sondes dédiées (vague 3 des intégrations, agent 0.6.0).

Ajoutés en fin de catalogue par `dashboard_catalogue.py` ; les fonctions de construction viennent
de ce module en paramètres (pas d'import circulaire). Chaque métrique et chaque contrôle visés sont
vérifiés contre le code de l'agent par `tests/test_dashboard_catalogue.py`.
"""


def boards(board, ts, qv, top, checks, WARN80):
    H = "{$host}"
    WEB, BDD, STO, ID, DNS, REG, LOG, SEC, SVC = ("Web", "Bases de données", "Stockage", "Annuaire et identité", "DNS",
                                                   "Dépôts et registres", "Journaux", "Sécurité", "Services")
    return [
        # ── Web et applications ──────────────────────────────────────────────────────────
        board("phpfpm", WEB, "PHP-FPM", "Processus actifs et inoccupés par pool, requêtes acceptées et lentes, file d'attente, "
              "plafond pm.max_children atteint.", ["phpfpm"], [
                  checks("w1", "Pools joignables", 0, 0, "php_fpm.can_connect"),
                  qv("w2", "Processus occupés (%)", 3, 0, f"max:php_fpm.processes.usage{H}", "percent", thresholds=WARN80),
                  qv("w3", "Requêtes en attente", 6, 0, f"max:php_fpm.listen_queue.size{H}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w4", "Plafond atteint (15 min)", 9, 0, f"sum:php_fpm.processes.max_reached{H}", "count"),
                  ts("w5", "Processus actifs et inoccupés", 0, 3, [f"sum:php_fpm.processes.active{H} by {{pool}}",
                                                                   f"sum:php_fpm.processes.idle{H} by {{pool}}"], "count", display="area", precision=0),
                  ts("w6", "Requêtes acceptées", 6, 3, [f"sum:php_fpm.requests.accepted{H} by {{pool}}"], "count", display="bars", precision=0),
                  ts("w7", "File d'attente", 0, 7, [f"max:php_fpm.listen_queue.size{H} by {{host,pool}}"], "count", precision=0),
                  ts("w8", "Requêtes lentes", 6, 7, [f"sum:php_fpm.requests.slow{H} by {{pool}}"], "count", display="bars", precision=0),
              ]),
        board("varnish", WEB, "Varnish", "Requêtes clientes, taux de succès du cache, objets, évictions, connexions aux serveurs "
              "d'origine et leur santé, stockage.", ["varnish"], [
                  checks("w1", "Varnish", 0, 0, "varnish.can_connect"),
                  checks("w2", "Serveurs d'origine", 3, 0, "varnish.backend_healthy"),
                  qv("w3", "Succès du cache (%)", 6, 0, f"avg:varnish.cache_hit_rate{H}", "percent", precision=1),
                  qv("w4", "Objets en cache", 9, 0, f"sum:varnish.n_object{H}", "count"),
                  ts("w5", "Requêtes clientes, succès et défauts", 0, 3, [f"sum:varnish.client_req{H}", f"sum:varnish.cache_hit{H}",
                                                                         f"sum:varnish.cache_miss{H}"], "count", precision=0),
                  ts("w6", "Taux de succès du cache (%)", 6, 3, [f"avg:varnish.cache_hit_rate{H} by {{host}}"], "percent"),
                  ts("w7", "Requêtes par serveur d'origine", 0, 7, [f"sum:varnish.backend.requests{H} by {{backend}}"], "count", precision=0),
                  ts("w8", "Évictions et connexions abandonnées", 6, 7, [f"sum:varnish.n_lru_nuked{H}", f"sum:varnish.sess_dropped{H}",
                                                                         f"sum:varnish.backend_fail{H}"], "count", display="bars", precision=0),
                  ts("w9", "Stockage occupé", 0, 11, [f"max:varnish.storage.used{H} by {{storage}}"], "byte", w=12),
              ]),
        board("squid", WEB, "Squid", "Requêtes clientes, taux de succès, erreurs, volumes échangés, descripteurs et clients.", ["squid"], [
                  checks("w1", "Squid", 0, 0, "squid.can_connect"),
                  qv("w2", "Succès du cache (%)", 3, 0, f"avg:squid.cachemgr.client_http.hit_rate{H}", "percent", precision=1),
                  qv("w3", "Clients", 6, 0, f"max:squid.clients{H}", "count"),
                  qv("w4", "Descripteurs utilisés", 9, 0, f"max:squid.fd.used{H}", "count"),
                  ts("w5", "Requêtes clientes et succès", 0, 3, [f"sum:squid.cachemgr.client_http.requests{H}",
                                                                  f"sum:squid.cachemgr.client_http.hits{H}"], "count", precision=0),
                  ts("w6", "Erreurs (clients, serveurs)", 6, 3, [f"sum:squid.cachemgr.client_http.errors{H}",
                                                                 f"sum:squid.cachemgr.server.all.errors{H}"], "count", display="bars", precision=0),
                  ts("w7", "Kio servis aux clients", 0, 7, [f"sum:squid.cachemgr.client_http.kbytes_out{H} by {{host}}"], "count"),
                  ts("w8", "Kio reçus des serveurs", 6, 7, [f"sum:squid.cachemgr.server.all.kbytes_in{H} by {{host}}"], "count"),
              ]),
        board("lighttpd", WEB, "Lighttpd", "Requêtes et octets par seconde, connexions actives et inoccupées.", ["lighttpd"], [
                  checks("w1", "Lighttpd", 0, 0, "lighttpd.can_connect"),
                  qv("w2", "Requêtes par seconde", 3, 0, f"sum:lighttpd.net.request_per_s{H}", "operation_per_second", precision=1),
                  qv("w3", "Connexions actives", 6, 0, f"sum:lighttpd.performance.busy_servers{H}", "count"),
                  qv("w4", "En service depuis", 9, 0, f"min:lighttpd.performance.uptime{H}", "second"),
                  ts("w5", "Requêtes par seconde", 0, 3, [f"sum:lighttpd.net.request_per_s{H} by {{host}}"], "operation_per_second"),
                  ts("w6", "Débit (octets/s)", 6, 3, [f"sum:lighttpd.net.bytes_per_s{H} by {{host}}"], "byte_per_second"),
              ]),
        board("gunicorn", WEB, "Gunicorn", "Workers occupés et inoccupés par application ; requêtes, durées et codes de réponse "
              "par le StatsD natif de Gunicorn.", ["gunicorn"], [
                  checks("w1", "Applications", 0, 0, "gunicorn.is_running", w=4),
                  qv("w2", "Workers occupés", 4, 0, f"sum:gunicorn.worker_processes{{$host,state:working}}", "count"),
                  qv("w3", "Workers inoccupés", 8, 0, f"sum:gunicorn.worker_processes{{$host,state:idle}}", "count", w=4),
                  ts("w4", "Workers par état", 0, 3, [f"sum:gunicorn.worker_processes{H} by {{app,state}}"], "count", display="area", precision=0),
                  ts("w5", "Requêtes (StatsD)", 6, 3, [f"sum:gunicorn.requests{H} by {{host}}"], "count", display="bars", precision=0),
              ]),
        # ── Services ─────────────────────────────────────────────────────────────────────
        board("supervisord", SVC, "Supervisor", "Programmes gérés par Supervisor : état, nombre par état, durée de fonctionnement.",
              ["supervisord"], [
                  checks("w1", "Supervisor", 0, 0, "supervisord.can_connect"),
                  checks("w2", "Programmes", 3, 0, "supervisord.process.status", w=9),
                  ts("w3", "Programmes par état", 0, 3, [f"max:supervisord.process.count{H} by {{status}}"], "count", display="area", precision=0),
                  top("w4", "Durée de fonctionnement", 6, 3, f"min:supervisord.process.uptime{H} by {{host,supervisord_process}}", "second"),
              ]),
        # ── Bases de données ─────────────────────────────────────────────────────────────
        board("pgbouncer", BDD, "PgBouncer", "Clients actifs et en attente, connexions serveur par état, attente maximale, "
              "requêtes, transactions et temps moyens par base.", ["pgbouncer"], [
                  checks("w1", "PgBouncer", 0, 0, "pgbouncer.can_connect"),
                  qv("w2", "Clients en attente", 3, 0, f"sum:pgbouncer.pools.cl_waiting{H}", "count", thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w3", "Attente maximale (s)", 6, 0, f"max:pgbouncer.pools.maxwait{H}", "second", precision=2),
                  checks("w4", "Bases", 9, 0, "pgbouncer.database"),
                  ts("w5", "Clients actifs et en attente", 0, 3, [f"sum:pgbouncer.pools.cl_active{H} by {{db}}",
                                                                  f"sum:pgbouncer.pools.cl_waiting{H} by {{db}}"], "count", precision=0),
                  ts("w6", "Connexions serveur", 6, 3, [f"sum:pgbouncer.pools.sv_active{H}", f"sum:pgbouncer.pools.sv_idle{H}",
                                                        f"sum:pgbouncer.pools.sv_used{H}"], "count", display="area", precision=0),
                  ts("w7", "Requêtes et transactions", 0, 7, [f"sum:pgbouncer.stats.queries{H} by {{db}}",
                                                              f"sum:pgbouncer.stats.transactions{H} by {{db}}"], "count", display="bars", precision=0),
                  ts("w8", "Temps moyens (ms)", 6, 7, [f"avg:pgbouncer.stats.avg_query_time{H} by {{db}}",
                                                       f"avg:pgbouncer.stats.avg_transaction_time{H} by {{db}}"], "millisecond"),
              ]),
        board("proxysql", BDD, "ProxySQL", "Connexions clientes et serveur, requêtes et requêtes lentes, état, latence et "
              "connexions de chaque serveur MySQL derrière ProxySQL.", ["proxysql"], [
                  checks("w1", "ProxySQL", 0, 0, "proxysql.can_connect"),
                  checks("w2", "Serveurs MySQL", 3, 0, "proxysql.backend.status", w=6),
                  qv("w3", "Clients connectés", 9, 0, f"sum:proxysql.client.connections_connected{H}", "count"),
                  ts("w4", "Requêtes et requêtes lentes", 0, 3, [f"sum:proxysql.questions{H}", f"sum:proxysql.slow_queries{H}"], "count",
                     display="bars", precision=0),
                  ts("w5", "Latence des serveurs (ms)", 6, 3, [f"max:proxysql.pool.latency_ms{H} by {{backend}}"], "millisecond"),
                  ts("w6", "Connexions utilisées par serveur", 0, 7, [f"sum:proxysql.pool.connections_used{H} by {{backend}}"], "count", precision=0),
                  ts("w7", "Erreurs de connexion par serveur", 6, 7, [f"sum:proxysql.pool.connections_error{H} by {{backend}}"], "count",
                     display="bars", precision=0),
              ]),
        board("clickhouse", BDD, "ClickHouse", "Requêtes en cours et échouées, insertions, parts par partition, répliques en lecture "
              "seule, mémoire suivie, connexions.", ["clickhouse"], [
                  checks("w1", "ClickHouse", 0, 0, "clickhouse.can_connect"),
                  qv("w2", "Parts max. par partition", 3, 0, f"max:clickhouse.part.max_per_partition{H}", "count",
                     thresholds=[{"value": 300, "color": "warn"}, {"value": 1000, "color": "error"}]),
                  qv("w3", "Répliques en lecture seule", 6, 0, f"max:clickhouse.table.replicated.readonly{H}", "count",
                     thresholds=[{"value": 1, "color": "error"}]),
                  qv("w4", "Mémoire suivie", 9, 0, f"max:clickhouse.memory.tracked{H}", "byte", precision=1),
                  ts("w5", "Requêtes et échecs", 0, 3, [f"sum:clickhouse.query.count{H}", f"sum:clickhouse.query.failed.count{H}"], "count",
                     display="bars", precision=0),
                  ts("w6", "Requêtes en cours", 6, 3, [f"max:clickhouse.query.active{H} by {{host}}"], "count", precision=0),
                  ts("w7", "Lignes insérées", 0, 7, [f"sum:clickhouse.insert.rows{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w8", "Durée moyenne des requêtes (ms)", 6, 7, [f"avg:clickhouse.query.time.avg{H} by {{host}}"], "millisecond"),
              ]),
        board("couchdb", BDD, "CouchDB", "Requêtes HTTP et codes de réponse, temps de réponse, lectures et écritures, bases "
              "ouvertes, réplications, tâches actives.", ["couchdb"], [
                  checks("w1", "CouchDB", 0, 0, "couchdb.can_connect"),
                  qv("w2", "Temps de réponse p95 (ms)", 3, 0, f"max:couchdb.couchdb.request_time.p95{H}", "millisecond"),
                  qv("w3", "Bases ouvertes", 6, 0, f"max:couchdb.couchdb.open_databases{H}", "count"),
                  qv("w4", "Réplications en échec", 9, 0, f"max:couchdb.couch_replicator.jobs.crashed{H}", "count",
                     thresholds=[{"value": 1, "color": "error"}]),
                  ts("w5", "Requêtes HTTP", 0, 3, [f"sum:couchdb.couchdb.httpd.requests{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w6", "Réponses par famille", 6, 3, [f"sum:couchdb.couchdb.httpd_status_codes{H} by {{status_class}}"], "count",
                     display="bars", precision=0),
                  ts("w7", "Lectures et écritures", 0, 7, [f"sum:couchdb.couchdb.database_reads{H}", f"sum:couchdb.couchdb.database_writes{H}"],
                     "count", precision=0),
                  ts("w8", "Tâches actives", 6, 7, [f"max:couchdb.active_tasks{H} by {{task_type}}"], "count", display="area", precision=0),
              ]),
        board("couchbase", BDD, "Couchbase", "Santé des nœuds, RAM et disque du cluster, opérations, éléments et quota par bucket.",
              ["couchbase"], [
                  checks("w1", "Couchbase", 0, 0, "couchbase.can_connect"),
                  checks("w2", "Nœuds", 3, 0, "couchbase.node.health"),
                  qv("w3", "Quota RAM utilisé (%)", 6, 0, f"max:couchbase.ram.quota_usage{H}", "percent", thresholds=WARN80),
                  qv("w4", "Disque utilisé (%)", 9, 0, f"max:couchbase.hdd.usage{H}", "percent", thresholds=WARN80),
                  ts("w5", "Opérations par bucket (par s)", 0, 3, [f"sum:couchbase.by_bucket.ops{H} by {{bucket}}"], "operation_per_second"),
                  ts("w6", "Quota RAM par bucket (%)", 6, 3, [f"max:couchbase.by_bucket.quota_percent_used{H} by {{bucket}}"], "percent",
                     thresholds=WARN80),
                  ts("w7", "Éléments par bucket", 0, 7, [f"max:couchbase.by_bucket.item_count{H} by {{bucket}}"], "count", precision=0),
                  ts("w8", "Disque par bucket", 6, 7, [f"max:couchbase.by_bucket.disk_used{H} by {{bucket}}"], "byte"),
              ]),
        # ── Coordination et stockage ─────────────────────────────────────────────────────
        board("zookeeper", STO, "ZooKeeper", "Rôle du serveur, latences, requêtes en attente, connexions, nœuds et observateurs, "
              "suiveurs synchronisés.", ["zookeeper"], [
                  checks("w1", "ZooKeeper", 0, 0, "zookeeper.can_connect"),
                  checks("w2", "ruok", 3, 0, "zookeeper.ruok"),
                  qv("w3", "Latence moyenne (ms)", 6, 0, f"avg:zookeeper.latency.avg{H}", "millisecond"),
                  qv("w4", "Requêtes en attente", 9, 0, f"max:zookeeper.outstanding_requests{H}", "count"),
                  ts("w5", "Latences (ms)", 0, 3, [f"avg:zookeeper.latency.avg{H} by {{host}}", f"max:zookeeper.latency.max{H} by {{host}}"],
                     "millisecond"),
                  ts("w6", "Connexions", 6, 3, [f"max:zookeeper.connections{H} by {{host}}"], "count", precision=0),
                  ts("w7", "Nœuds et observateurs", 0, 7, [f"max:zookeeper.znode_count{H}", f"max:zookeeper.watch_count{H}"], "count", precision=0),
                  ts("w8", "Paquets reçus et envoyés", 6, 7, [f"sum:zookeeper.packets.received{H}", f"sum:zookeeper.packets.sent{H}"], "count",
                     precision=0),
              ]),
        board("ceph", STO, "Ceph", "Santé de la grappe, OSD en service, groupes de placement, capacité, débits et occupation "
              "des pools.", ["ceph"], [
                  checks("w1", "Santé", 0, 0, "ceph.overall_status"),
                  qv("w2", "OSD en service", 3, 0, f"min:ceph.num_up_osds{H}", "count"),
                  qv("w3", "PG active+clean (%)", 6, 0, f"min:ceph.pgs.active_clean_pct{H}", "percent", precision=1),
                  qv("w4", "Capacité utilisée (%)", 9, 0, f"max:ceph.aggregate_pct_used{H}", "percent", thresholds=WARN80),
                  ts("w5", "Débits (octets/s)", 0, 3, [f"avg:ceph.read_bytes_sec{H}", f"avg:ceph.write_bytes_sec{H}"], "byte_per_second"),
                  ts("w6", "Opérations par seconde", 6, 3, [f"avg:ceph.read_op_per_sec{H}", f"avg:ceph.write_op_per_sec{H}"],
                     "operation_per_second"),
                  ts("w7", "Groupes de placement par état", 0, 7, [f"max:ceph.pgs{H} by {{pg_state}}"], "count", display="area", precision=0),
                  top("w8", "Occupation des pools (%)", 6, 7, f"max:ceph.pool.pct_used{H} by {{ceph_pool}}", "percent", thresholds=WARN80),
              ]),
        board("btrfs", STO, "Btrfs", "Allocation par type et profil, espace non alloué, disques manquants et compteurs d'erreurs.",
              ["btrfs"], [
                  checks("w1", "Santé", 0, 0, "btrfs.health", w=4),
                  top("w2", "Non alloué (%)", 4, 0, f"min:btrfs.unallocated.pct{H} by {{host,mount}}", "percent", w=8, h=3),
                  ts("w3", "Occupation par type (%)", 0, 3, [f"max:btrfs.usage{H} by {{mount,usage_type}}"], "percent", thresholds=WARN80),
                  ts("w4", "Espace non alloué", 6, 3, [f"min:btrfs.unallocated{H} by {{host,mount}}"], "byte"),
                  top("w5", "Erreurs par disque", 0, 7, f"max:btrfs.device.errors{H} by {{host,mount,device,error_type}}", "count", w=12,
                      precision=0),
              ]),
        board("nfs", STO, "NFS", "Client : montages, opérations, débits, temps aller-retour, retransmissions. Serveur : fils nfsd, "
              "appels RPC, octets lus et écrits.", ["nfs"], [
                  checks("w1", "Montages", 0, 0, "nfs.mount", w=4),
                  checks("w2", "Serveur", 4, 0, "nfs.server"),
                  qv("w3", "Fils nfsd", 7, 0, f"max:nfs.server.threads{H}", "count"),
                  qv("w4", "Retransmissions", 10, 0, f"sum:nfs.client.retrans{H}", "count", w=2),
                  ts("w5", "Octets lus et écrits (client)", 0, 3, [f"sum:nfs.client.read.bytes{H} by {{mount}}",
                                                                   f"sum:nfs.client.write.bytes{H} by {{mount}}"], "byte"),
                  ts("w6", "Aller-retour moyen (ms)", 6, 3, [f"avg:nfs.client.read.rtt{H} by {{mount}}",
                                                             f"avg:nfs.client.write.rtt{H} by {{mount}}"], "millisecond"),
                  ts("w7", "Appels RPC (serveur)", 0, 7, [f"sum:nfs.server.rpc.calls{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w8", "Octets lus et écrits (serveur)", 6, 7, [f"sum:nfs.server.read.bytes{H}", f"sum:nfs.server.write.bytes{H}"], "byte"),
              ]),
        # ── Annuaire et identité ─────────────────────────────────────────────────────────
        board("openldap", ID, "OpenLDAP", "Connexions, opérations par type, octets et entrées envoyés, fils et attentes de slapd.",
              ["openldap"], [
                  checks("w1", "slapd", 0, 0, "openldap.can_connect"),
                  qv("w2", "Connexions ouvertes", 3, 0, f"max:openldap.connections.current{H}", "count"),
                  qv("w3", "Fils actifs", 6, 0, f"max:openldap.threads{{$host,status:active}}", "count"),
                  qv("w4", "En service depuis", 9, 0, f"min:openldap.uptime{H}", "second"),
                  ts("w5", "Opérations terminées par type", 0, 3, [f"sum:openldap.operations.completed{H} by {{operation}}"], "count",
                     display="bars", precision=0),
                  ts("w6", "Nouvelles connexions", 6, 3, [f"sum:openldap.connections.total{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w7", "Entrées envoyées", 0, 7, [f"sum:openldap.statistics.entries{H} by {{host}}"], "count", precision=0),
                  ts("w8", "Attentes en lecture et écriture", 6, 7, [f"max:openldap.waiter.read{H}", f"max:openldap.waiter.write{H}"], "count",
                     precision=0),
              ]),
        board("keycloak", ID, "Keycloak", "Disponibilité, sessions actives et hors ligne par royaume et par client, utilisateurs.",
              ["keycloak"], [
                  checks("w1", "Keycloak", 0, 0, "keycloak.can_connect"),
                  checks("w2", "API d'administration", 3, 0, "keycloak.admin_api"),
                  qv("w3", "Sessions actives", 6, 0, f"sum:keycloak.sessions.active{H}", "count"),
                  qv("w4", "Utilisateurs", 9, 0, f"sum:keycloak.users{H}", "count"),
                  ts("w5", "Sessions actives par royaume", 0, 3, [f"sum:keycloak.sessions.active{H} by {{realm}}"], "count", precision=0),
                  ts("w6", "Sessions hors ligne par royaume", 6, 3, [f"sum:keycloak.sessions.offline{H} by {{realm}}"], "count", precision=0),
                  top("w7", "Sessions par client", 0, 7, f"max:keycloak.client.sessions.active{H} by {{realm,client}}", "count", w=12, precision=0),
              ]),
        # ── DNS ──────────────────────────────────────────────────────────────────────────
        board("powerdns", DNS, "PowerDNS (serveur faisant autorité)", "Requêtes UDP et TCP, latence, cache de paquets et de "
              "requêtes, SERVFAIL, file d'attente, état de sécurité.", ["powerdns"], [
                  checks("w1", "PowerDNS", 0, 0, "powerdns.authoritative.can_connect"),
                  checks("w2", "Sécurité", 3, 0, "powerdns.authoritative.security_status"),
                  qv("w3", "Latence (µs)", 6, 0, f"avg:powerdns.authoritative.latency{H}", precision=0),
                  qv("w4", "Succès du cache de paquets (%)", 9, 0, f"avg:powerdns.authoritative.packetcache_hit_rate{H}", "percent", precision=1),
                  ts("w5", "Requêtes UDP et TCP", 0, 3, [f"sum:powerdns.authoritative.udp_queries{H}", f"sum:powerdns.authoritative.tcp_queries{H}"],
                     "count", display="bars", precision=0),
                  ts("w6", "SERVFAIL et délais dépassés", 6, 3, [f"sum:powerdns.authoritative.servfail_packets{H}",
                                                                 f"sum:powerdns.authoritative.timedout_packets{H}"], "count", display="bars", precision=0),
                  ts("w7", "Latence (µs)", 0, 7, [f"avg:powerdns.authoritative.latency{H} by {{host}}"]),
                  ts("w8", "File d'attente", 6, 7, [f"max:powerdns.authoritative.qsize_q{H} by {{host}}"], "count", precision=0),
              ]),
        board("powerdns_recursor", DNS, "PowerDNS Recursor", "Questions, cache, temps de réponse par tranche, délais dépassés, "
              "SERVFAIL, requêtes en cours, état de sécurité.", ["powerdns_recursor"], [
                  checks("w1", "Recursor", 0, 0, "powerdns.recursor.can_connect"),
                  checks("w2", "Sécurité", 3, 0, "powerdns.recursor.security_status"),
                  qv("w3", "Succès du cache (%)", 6, 0, f"avg:powerdns.recursor.cache_hit_rate{H}", "percent", precision=1),
                  qv("w4", "Requêtes en cours", 9, 0, f"max:powerdns.recursor.concurrent_queries{H}", "count"),
                  ts("w5", "Questions", 0, 3, [f"sum:powerdns.recursor.questions{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w6", "Réponses par délai", 6, 3, [f"sum:powerdns.recursor.answers0_1{H}", f"sum:powerdns.recursor.answers1_10{H}",
                                                        f"sum:powerdns.recursor.answers10_100{H}", f"sum:powerdns.recursor.answers100_1000{H}",
                                                        f"sum:powerdns.recursor.answers_slow{H}"], "count", display="area", precision=0),
                  ts("w7", "SERVFAIL et délais dépassés", 0, 7, [f"sum:powerdns.recursor.servfail_answers{H}",
                                                                 f"sum:powerdns.recursor.outgoing_timeouts{H}"], "count", display="bars", precision=0),
                  ts("w8", "Entrées du cache", 6, 7, [f"max:powerdns.recursor.cache_entries{H} by {{host}}"], "count", precision=0),
              ]),
        # ── Dépôts et registres ──────────────────────────────────────────────────────────
        board("nexus", REG, "Sonatype Nexus", "Disponibilité, écriture, contrôles de santé, tas JVM, requêtes et réponses, magasins "
              "de blobs.", ["nexus"], [
                  checks("w1", "Nexus", 0, 0, "nexus.can_connect"),
                  checks("w2", "Écriture", 3, 0, "nexus.writable"),
                  checks("w3", "Santé", 6, 0, "nexus.health"),
                  qv("w4", "Tas JVM (%)", 9, 0, f"max:nexus.jvm.heap.usage{H}", "percent", thresholds=WARN80),
                  ts("w5", "Requêtes HTTP", 0, 3, [f"sum:nexus.http.requests{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w6", "Réponses par famille", 6, 3, [f"sum:nexus.http.responses{H} by {{status_class}}"], "count", display="bars", precision=0),
                  ts("w7", "Latence des requêtes (ms)", 0, 7, [f"avg:nexus.http.request.latency.avg{H}", f"max:nexus.http.request.latency.p95{H}"],
                     "millisecond"),
                  top("w8", "Place libre des magasins de blobs", 6, 7, f"min:nexus.blobstore.available{H} by {{blobstore}}", "byte"),
              ]),
        board("harbor", REG, "Harbor", "Santé de chaque composant, projets, dépôts, stockage consommé et disque du registre.",
              ["harbor"], [
                  checks("w1", "Harbor", 0, 0, "harbor.status"),
                  checks("w2", "Composants", 3, 0, "harbor.component", w=6),
                  qv("w3", "Disque (%)", 9, 0, f"max:harbor.disk.usage{H}", "percent", thresholds=WARN80),
                  ts("w4", "Projets et dépôts", 0, 3, [f"max:harbor.projects.count{H}", f"max:harbor.repos.count{H}"], "count", precision=0),
                  ts("w5", "Stockage consommé", 6, 3, [f"max:harbor.storage.consumption{H} by {{host}}"], "byte"),
              ]),
        # ── Journaux ─────────────────────────────────────────────────────────────────────
        board("fluentd", LOG, "Fluentd", "Sorties à tampon : file d'attente, octets en attente, place libre, nouveaux essais, "
              "enregistrements émis et écrits.", ["fluentd"], [
                  checks("w1", "Fluentd", 0, 0, "fluentd.can_connect"),
                  top("w2", "Place libre des tampons (%)", 3, 0, f"min:fluentd.buffer_available_buffer_space_ratios{H} by {{plugin_id}}", "percent",
                      w=9, h=3),
                  ts("w3", "Morceaux en attente", 0, 3, [f"max:fluentd.buffer_queue_length{H} by {{plugin_id}}"], "count", precision=0),
                  ts("w4", "Octets en attente", 6, 3, [f"max:fluentd.buffer_total_queued_size{H} by {{plugin_id}}"], "byte"),
                  ts("w5", "Enregistrements émis", 0, 7, [f"sum:fluentd.emit_records{H} by {{plugin_id}}"], "count", display="bars", precision=0),
                  ts("w6", "Nouveaux essais", 6, 7, [f"max:fluentd.retry_count{H} by {{plugin_id}}"], "count", precision=0),
              ]),
        # ── Sécurité ─────────────────────────────────────────────────────────────────────
        board("ssh", SEC, "SSH", "Disponibilité, bannière et temps de réponse des serveurs SSH suivis.", ["ssh"], [
                  checks("w1", "Serveurs SSH", 0, 0, "ssh.can_connect", w=6),
                  top("w2", "Temps de réponse (s)", 6, 0, f"max:ssh.response_time{H} by {{target}}", "second", h=3),
                  ts("w3", "Temps de réponse (s)", 0, 3, [f"max:ssh.response_time{H} by {{target}}"], "second", w=12),
              ]),
        board("wazuh", SEC, "Wazuh", "Démons du manager, agents par état, événements reçus et perdus, alertes écrites, files "
              "d'analysisd et de remoted.", ["wazuh"], [
                  checks("w1", "API", 0, 0, "wazuh.can_connect"),
                  checks("w2", "Démons", 3, 0, "wazuh.manager.daemons"),
                  qv("w3", "Agents déconnectés", 6, 0, f"max:wazuh.agents{{$host,status:disconnected}}", "count",
                     thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w4", "File d'analysisd (%)", 9, 0, f"max:wazuh.analysisd.event_queue.usage{H}", "percent", thresholds=WARN80),
                  ts("w5", "Agents par état", 0, 3, [f"max:wazuh.agents{H} by {{status}}"], "count", display="area", precision=0),
                  ts("w6", "Événements reçus et perdus", 6, 3, [f"sum:wazuh.analysisd.events.received{H}", f"sum:wazuh.analysisd.events.dropped{H}"],
                     "count", display="bars", precision=0),
                  ts("w7", "Alertes écrites", 0, 7, [f"sum:wazuh.analysisd.alerts_written{H} by {{host}}"], "count", display="bars", precision=0),
                  ts("w8", "Sessions des agents", 6, 7, [f"max:wazuh.remoted.tcp_sessions{H} by {{host}}"], "count", precision=0),
              ]),
        board("suricata", SEC, "Suricata", "Fraîcheur des statistiques, paquets capturés et perdus, alertes par gravité, flux par "
              "protocole applicatif, mémoire.", ["suricata"], [
                  checks("w1", "Statistiques", 0, 0, "suricata.can_read"),
                  qv("w2", "Paquets perdus (%)", 3, 0, f"max:suricata.capture.drop_rate{H}", "percent", precision=1,
                     thresholds=[{"value": 1, "color": "warn"}, {"value": 5, "color": "error"}]),
                  qv("w3", "Alertes de gravité 1", 6, 0, f"sum:suricata.alerts{{$host,severity:1}}", "count",
                     thresholds=[{"value": 1, "color": "error"}]),
                  qv("w4", "En service depuis", 9, 0, f"min:suricata.uptime{H}", "second"),
                  ts("w5", "Paquets capturés et perdus", 0, 3, [f"sum:suricata.capture.kernel_packets{H}", f"sum:suricata.capture.kernel_drops{H}"],
                     "count", precision=0),
                  ts("w6", "Alertes par gravité", 6, 3, [f"sum:suricata.alerts{H} by {{severity}}"], "count", display="bars", precision=0),
                  ts("w7", "Flux par protocole", 0, 7, [f"sum:suricata.app_layer.flow{H} by {{app_proto}}"], "count", display="area", precision=0),
                  ts("w8", "Mémoire des flux et de TCP", 6, 7, [f"max:suricata.flow.memuse{H}", f"max:suricata.tcp.memuse{H}",
                                                                f"max:suricata.tcp.reassembly_memuse{H}"], "byte"),
              ]),
    ]
