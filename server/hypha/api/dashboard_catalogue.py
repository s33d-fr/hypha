"""Catalogue des tableaux de bord prêts (vague 1 des intégrations, 0.5.0).

Sur le modèle du catalogue des moniteurs : des modèles en code, installés d'un clic dans un espace
(`POST /dashboards/catalogue/{id}`), qui devient alors un tableau ordinaire (modifiable, clonable,
exportable). Chaque requête vise une métrique que l'agent publie vraiment : la liste est vérifiée par
`tests/test_dashboard_catalogue.py` contre le code de l'agent. `$host` vient de la variable de modèle et
disparaît quand elle vaut `*`.
"""
from .default_dashboards import HOST_VAR, NETWORK, SYSTEM

LOG_ERRORS = "status:error OR status:critical OR status:alert OR status:emergency"


def ts(wid, title, x, y, queries, unit=None, w=6, h=4, display="line", **options):
    opts = {"precision": 1, **options}
    if unit:
        opts["unit"] = unit
    return {"id": wid, "type": "timeseries", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [{"q": q, "display": display} for q in queries], "options": opts}


def qv(wid, title, x, y, q, unit=None, w=3, h=2, precision=0, **options):
    opts = {"precision": precision, **options}
    if unit:
        opts["unit"] = unit
    return {"id": wid, "type": "query_value", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [{"q": q}], "options": opts}


def top(wid, title, x, y, q, unit=None, w=6, h=4, precision=1, **options):
    opts = {"precision": precision, **options}
    if unit:
        opts["unit"] = unit
    return {"id": wid, "type": "toplist", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [{"q": q, "display": "bars"}], "options": opts}


def checks(wid, title, x, y, check, w=3, h=3):
    return {"id": wid, "type": "check_status", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [], "options": {"check": check}}


def logs(wid, title, x, y, query, w=12, h=5):
    return {"id": wid, "type": "log_stream", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [], "options": {"log_query": query}}


def note(wid, x, y, text, w=12, h=2):
    return {"id": wid, "type": "note", "title": "", "x": x, "y": y, "w": w, "h": h, "queries": [],
            "options": {"text": text}}


WARN80 = [{"value": 80, "color": "warn"}, {"value": 90, "color": "error"}]


def board(cid, family, title, description, integrations, widgets, tvars=None):
    return {"id": cid, "family": family, "integrations": integrations,
            "dashboard": {"title": title, "description": description, "template_vars": tvars or HOST_VAR,
                          "widgets": widgets, "folder": family}}


CATALOGUE = [
    # ── Système ──────────────────────────────────────────────────────────────
    board("system", "Système", SYSTEM["title"], SYSTEM["description"], ["system"], SYSTEM["widgets"]),
    board("network", "Système", NETWORK["title"], NETWORK["description"], ["network"], NETWORK["widgets"]),
    board("system_detail", "Système", "Système en détail",
          "Processeur par nature (utilisateur, système, attente disque, vol), disques (occupation, attente, "
          "saturation), descripteurs, horloge, mises à jour et redémarrages en attente.", ["system", "ntp", "updates"], [
              ts("w1", "Processeur réellement occupé (%)", 0, 0, ["avg:system.cpu.busy{$host} by {host}"], "percent", thresholds=WARN80),
              ts("w2", "Attente disque (%)", 6, 0, ["avg:system.cpu.iowait{$host} by {host}"], "percent"),
              ts("w3", "Utilisateur, système, vol (%)", 0, 4, ["avg:system.cpu.user{$host}", "avg:system.cpu.system{$host}",
                                                                "avg:system.cpu.steal{$host}"], "percent", display="area"),
              ts("w4", "Saturation des disques (%)", 6, 4, ["max:system.io.util{$host} by {host,device}"], "percent"),
              ts("w5", "Attente moyenne des E/S (ms)", 0, 8, ["avg:system.io.await{$host} by {host,device}"], "millisecond"),
              top("w6", "Inodes les plus utilisés (%)", 6, 8, "max:system.fs.inodes.in_use{$host} by {host,mount}", "percent",
                  thresholds=WARN80),
              ts("w7", "Descripteurs ouverts", 0, 12, ["max:system.file_handles.in_use{$host} by {host}"], "count", precision=0),
              ts("w8", "Écart d'horloge (s)", 6, 12, ["max:ntp.offset{$host} by {host}"], "second", precision=3),
              top("w9", "Mises à jour en attente", 0, 16, "max:system.updates.pending{$host} by {host}", "count", w=4, precision=0),
              top("w10", "Mises à jour de sécurité", 4, 16, "max:system.updates.security{$host} by {host}", "count", w=4, precision=0),
              top("w11", "Redémarrage requis", 8, 16, "max:system.reboot_required{$host} by {host}", w=4, precision=0),
          ]),
    board("hardware", "Système", "Matériel : disques et capteurs",
          "Santé SMART des disques (température, secteurs réalloués, usure), températures et ventilateurs des capteurs.",
          ["smart", "hwmon"], [
              checks("w1", "Santé SMART", 0, 0, "smart.health", w=4),
              top("w2", "Température des disques (°C)", 4, 0, "max:smart.temperature{$host} by {host,device}", w=8, precision=0),
              top("w3", "Secteurs réalloués", 0, 4, "max:smart.reallocated_sectors{$host} by {host,device}", "count", precision=0),
              top("w4", "Usure des SSD (%)", 6, 4, "max:smart.wear_level{$host} by {host,device}", "percent", precision=0),
              ts("w5", "Températures des capteurs (°C)", 0, 8, ["max:system.temperature{$host} by {host,sensor}"]),
              ts("w6", "Ventilateurs (tr/min)", 6, 8, ["avg:system.fan.rpm{$host} by {host,sensor}"], precision=0),
          ]),
    board("windows", "Système", "Windows : IIS et SQL Server",
          "Requêtes et connexions IIS, lots et connexions SQL Server, cache, services et mises à jour Windows.",
          ["iis", "mssql"], [
              ts("w1", "Requêtes IIS (par s)", 0, 0, ["sum:iis.requests_per_s{$host} by {host}"], "operation_per_second"),
              ts("w2", "Connexions IIS", 6, 0, ["sum:iis.current_connections{$host} by {host}"], "count", precision=0),
              ts("w3", "Lots SQL Server (par s)", 0, 4, ["sum:mssql.batch_requests_per_s{$host} by {host}"], "operation_per_second"),
              ts("w4", "Connexions SQL Server", 6, 4, ["sum:mssql.user_connections{$host} by {host}"], "count", precision=0),
              ts("w5", "Succès du cache SQL Server (%)", 0, 8, ["avg:mssql.buffer_cache_hit_ratio{$host} by {host}"], "percent"),
              top("w6", "Mises à jour Windows en attente", 6, 8, "max:windows.updates.pending{$host} by {host}", "count", precision=0),
              checks("w7", "Services Windows", 0, 12, "windows.service", w=6),
          ]),
    # ── Bases de données ─────────────────────────────────────────────────────
    board("postgresql", "Bases de données", "PostgreSQL",
          "Connexions par état, transactions, taille des bases, cache, verrous, interblocages et retard de réplication.",
          ["postgres"], [
              checks("w1", "Joignable", 0, 0, "postgres.can_connect"),
              qv("w2", "Connexions utilisées (%)", 3, 0, "max:postgresql.percent_usage_connections{$host}", "percent",
                 thresholds=WARN80),
              qv("w3", "Retard de réplication (s)", 6, 0, "max:postgresql.replication_delay{$host}", "second", precision=1),
              qv("w4", "Requête la plus longue (s)", 9, 0, "max:postgresql.longest_query_s{$host}", "second", precision=1),
              ts("w5", "Connexions par état", 0, 3, ["sum:postgresql.connections{$host} by {state}"], "count", display="area",
                 precision=0),
              ts("w6", "Validations et annulations (par s)", 6, 3, ["sum:postgresql.commits{$host} by {host}",
                                                                      "sum:postgresql.rollbacks{$host} by {host}"],
                 "operation_per_second"),
              ts("w7", "Lignes lues et écrites (par s)", 0, 7, ["sum:postgresql.rows_fetched{$host}", "sum:postgresql.rows_inserted{$host}",
                                                                 "sum:postgresql.rows_updated{$host}", "sum:postgresql.rows_deleted{$host}"],
                 "operation_per_second"),
              ts("w8", "Succès du cache (%)", 6, 7, ["min:postgresql.buffer_hit{$host} by {db}"], "percent"),
              top("w9", "Taille des bases", 0, 11, "max:postgresql.database_size{$host} by {db}", "byte"),
              ts("w10", "Verrous par mode", 6, 11, ["sum:postgresql.locks{$host} by {lock_mode}"], "count", precision=0),
              ts("w11", "Interblocages et fichiers temporaires", 0, 15, ["sum:postgresql.deadlocks{$host}",
                                                                          "sum:postgresql.temp_bytes{$host}"], w=12),
          ]),
    board("mysql", "Bases de données", "MySQL et MariaDB",
          "Requêtes, connexions, fils actifs, requêtes lentes, InnoDB, tables temporaires sur disque et réplication.",
          ["mysql"], [
              checks("w1", "Joignable", 0, 0, "mysql.can_connect"),
              qv("w2", "Connexions utilisées (%)", 3, 0, "max:mysql.connections.usage{$host}", "percent", thresholds=WARN80),
              qv("w3", "Retard de réplication (s)", 6, 0, "max:mysql.replication.lag{$host}", "second"),
              checks("w4", "Réplication", 9, 0, "mysql.replication"),
              ts("w5", "Requêtes (par s)", 0, 3, ["sum:mysql.queries{$host} by {host}"], "operation_per_second"),
              ts("w6", "Ordres par type (par s)", 6, 3, ["sum:mysql.com.select{$host}", "sum:mysql.com.insert{$host}",
                                                         "sum:mysql.com.update{$host}", "sum:mysql.com.delete{$host}"],
                 "operation_per_second", display="area"),
              ts("w7", "Connexions et fils actifs", 0, 7, ["sum:mysql.connections.current{$host} by {host}",
                                                           "sum:mysql.threads.running{$host} by {host}"], "count", precision=0),
              ts("w8", "Requêtes lentes et tables temporaires sur disque", 6, 7, ["sum:mysql.slow_queries{$host}",
                                                                                  "sum:mysql.tmp_disk_tables{$host}"]),
              ts("w9", "Cache InnoDB (succès et occupation, %)", 0, 11, ["avg:mysql.innodb.buffer_pool.hit{$host}",
                                                                          "avg:mysql.innodb.buffer_pool.usage{$host}"], "percent"),
              ts("w10", "Trafic (octets/s)", 6, 11, ["sum:mysql.bytes.received{$host}", "sum:mysql.bytes.sent{$host}"],
                 "byte_per_second", display="area"),
          ]),
    board("redis", "Bases de données", "Redis et Valkey",
          "Mémoire, clients, commandes, taux de succès du cache, clés par base, évictions, réplication et persistance.",
          ["redis"], [
              checks("w1", "Joignable", 0, 0, "redis.can_connect"),
              checks("w2", "Persistance", 3, 0, "redis.persistence"),
              qv("w3", "Mémoire utilisée (% du plafond)", 6, 0, "max:redis.mem.usage{$host}", "percent", thresholds=WARN80),
              qv("w4", "Succès du cache (%)", 9, 0, "avg:redis.keyspace.hit_rate{$host}", "percent", precision=1),
              ts("w5", "Mémoire utilisée et RSS", 0, 3, ["max:redis.mem.used{$host} by {instance}", "max:redis.mem.rss{$host} by {instance}"],
                 "byte"),
              ts("w6", "Commandes", 6, 3, ["sum:redis.commands{$host} by {instance}"], "count", display="bars"),
              ts("w7", "Clients connectés et bloqués", 0, 7, ["sum:redis.clients.connected{$host} by {instance}",
                                                               "sum:redis.clients.blocked{$host} by {instance}"], "count", precision=0),
              ts("w8", "Clés par base", 6, 7, ["sum:redis.keys{$host} by {instance,db}"], "count", precision=0),
              ts("w9", "Clés évincées et expirées", 0, 11, ["sum:redis.keys.evicted{$host}", "sum:redis.keys.expired{$host}"], "count",
                 display="bars"),
              ts("w10", "Réseau (octets)", 6, 11, ["sum:redis.net.input{$host}", "sum:redis.net.output{$host}"], "byte", display="area"),
          ]),
    board("memcached", "Bases de données", "Memcached",
          "Taux de succès des lectures, occupation de la mémoire, connexions, commandes, objets et évictions.",
          ["memcached"], [
              checks("w1", "Joignable", 0, 0, "memcached.can_connect"),
              qv("w2", "Succès des lectures (%)", 3, 0, "avg:memcached.get.hit_rate{$host}", "percent", precision=1),
              qv("w3", "Mémoire occupée (%)", 6, 0, "max:memcached.bytes.usage{$host}", "percent", thresholds=WARN80),
              qv("w4", "Objets", 9, 0, "sum:memcached.items{$host}", "count"),
              ts("w5", "Lectures et écritures", 0, 3, ["sum:memcached.cmd.get{$host}", "sum:memcached.cmd.set{$host}"], "count",
                 display="bars"),
              ts("w6", "Succès et échecs des lectures", 6, 3, ["sum:memcached.get.hits{$host}", "sum:memcached.get.misses{$host}"],
                 "count", display="bars"),
              ts("w7", "Connexions", 0, 7, ["sum:memcached.connections.current{$host} by {instance}"], "count", precision=0),
              ts("w8", "Évictions", 6, 7, ["sum:memcached.evictions{$host} by {instance}"], "count", display="bars"),
          ]),
    board("mongodb", "Bases de données", "MongoDB",
          "Connexions, opérations par type, mémoire et cache WiredTiger, files d'attente, taille des bases et réplication.",
          ["mongodb"], [
              checks("w1", "Joignable", 0, 0, "mongodb.can_connect"),
              checks("w2", "Jeu de réplicas", 3, 0, "mongodb.replication"),
              qv("w3", "Retard de réplication (s)", 6, 0, "max:mongodb.replication.lag{$host}", "second"),
              qv("w4", "Cache WiredTiger (%)", 9, 0, "max:mongodb.cache.usage{$host}", "percent", thresholds=WARN80),
              ts("w5", "Opérations par type", 0, 3, ["sum:mongodb.operations{$host} by {op}"], "count", display="area"),
              ts("w6", "Connexions", 6, 3, ["sum:mongodb.connections.current{$host} by {instance}"], "count", precision=0),
              ts("w7", "Mémoire résidente", 0, 7, ["max:mongodb.mem.resident{$host} by {instance}"]),
              ts("w8", "Files d'attente (lectures, écritures)", 6, 7, ["sum:mongodb.queue.readers{$host}", "sum:mongodb.queue.writers{$host}"],
                 "count", precision=0),
              top("w9", "Taille des bases", 0, 11, "max:mongodb.database.size{$host} by {database}", "byte", w=12),
          ]),
    board("opensearch", "Recherche", "OpenSearch et Elasticsearch",
          "Santé de la grappe, fragments, tas JVM, disque, latences de recherche et d'indexation, rejets.",
          ["opensearch"], [
              checks("w1", "Santé de la grappe", 0, 0, "opensearch.cluster_health"),
              qv("w2", "Fragments non assignés", 3, 0, "max:opensearch.cluster.shards.unassigned{$host}", "count",
                 thresholds=[{"value": 1, "color": "warn"}]),
              qv("w3", "Nœuds", 6, 0, "max:opensearch.cluster.nodes{$host}", "count"),
              qv("w4", "Tâches en attente", 9, 0, "max:opensearch.cluster.pending_tasks{$host}", "count"),
              ts("w5", "Tas JVM (%)", 0, 3, ["max:opensearch.jvm.heap.usage{$host} by {host}"], "percent", thresholds=WARN80),
              ts("w6", "Disque des données (%)", 6, 3, ["max:opensearch.fs.usage{$host} by {host}"], "percent", thresholds=WARN80),
              ts("w7", "Latences (ms)", 0, 7, ["avg:opensearch.search.latency{$host} by {host}",
                                               "avg:opensearch.indexing.latency{$host} by {host}"], "millisecond"),
              ts("w8", "Recherches et indexations", 6, 7, ["sum:opensearch.search.queries{$host}", "sum:opensearch.indexing{$host}"]),
              ts("w9", "Écritures et recherches rejetées", 0, 11, ["sum:opensearch.thread_pool.rejected.write{$host}",
                                                                    "sum:opensearch.thread_pool.rejected.search{$host}"],
                 display="bars"),
              ts("w10", "Documents et taille des index", 6, 11, ["max:opensearch.docs{$host}", "max:opensearch.store.size{$host}"]),
          ]),
    board("rabbitmq", "Files de messages", "RabbitMQ",
          "Messages prêts et non acquittés, débits de publication et de livraison, files, connexions, état des nœuds.",
          ["rabbitmq"], [
              checks("w1", "Joignable", 0, 0, "rabbitmq.can_connect"),
              checks("w2", "Nœuds", 3, 0, "rabbitmq.node"),
              qv("w3", "Messages prêts", 6, 0, "sum:rabbitmq.messages.ready{$host}", "count"),
              qv("w4", "Non acquittés", 9, 0, "sum:rabbitmq.messages.unacked{$host}", "count"),
              ts("w5", "Débits (par s)", 0, 3, ["sum:rabbitmq.messages.publish_rate{$host}", "sum:rabbitmq.messages.deliver_rate{$host}",
                                                 "sum:rabbitmq.messages.ack_rate{$host}"], "operation_per_second"),
              top("w6", "Files les plus chargées", 6, 3, "max:rabbitmq.queue.messages{$host} by {vhost,queue}", "count", precision=0),
              ts("w7", "Connexions, canaux, consommateurs", 0, 7, ["sum:rabbitmq.connections{$host}", "sum:rabbitmq.channels{$host}",
                                                                    "sum:rabbitmq.consumers{$host}"], "count", precision=0),
              ts("w8", "Mémoire et descripteurs des nœuds (%)", 6, 7, ["max:rabbitmq.node.mem.usage{$host} by {node}",
                                                                       "max:rabbitmq.node.fd.usage{$host} by {node}"], "percent"),
          ]),
    # ── Web ──────────────────────────────────────────────────────────────────
    board("web", "Web", "nginx et Apache",
          "Requêtes et connexions de nginx (stub_status), requêtes, débit et processus d'Apache (mod_status), erreurs récentes.",
          ["nginx", "apache"], [
              ts("w1", "nginx : requêtes (par s)", 0, 0, ["sum:nginx.requests{$host} by {host}"], "operation_per_second"),
              ts("w2", "nginx : connexions", 6, 0, ["sum:nginx.connections.reading{$host}", "sum:nginx.connections.writing{$host}",
                                                    "sum:nginx.connections.waiting{$host}"], "count", display="area", precision=0),
              ts("w3", "Apache : requêtes (par s)", 0, 4, ["sum:apache.net.request_per_s{$host} by {host}"], "operation_per_second"),
              ts("w4", "Apache : processus occupés et libres", 6, 4, ["sum:apache.performance.busy_workers{$host}",
                                                                      "sum:apache.performance.idle_workers{$host}"], "count",
                 precision=0),
              ts("w5", "Apache : débit (octets/s)", 0, 8, ["sum:apache.net.bytes_per_s{$host} by {host}"], "byte_per_second"),
              logs("w6", "Erreurs des serveurs web", 6, 8,
                   "(service:nginx OR service:apache2 OR service:httpd OR service:haproxy) " + "(" + LOG_ERRORS + ")", w=6, h=4),
          ]),
    board("haproxy", "Web", "HAProxy",
          "Sessions, débit, réponses par classe, erreurs et file d'attente des frontaux et des backends, serveurs hors service.",
          ["haproxy"], [
              checks("w1", "Backends en service", 0, 0, "haproxy.backend_up", w=4),
              top("w2", "Serveurs (1 = en service)", 4, 0, "min:haproxy.server.up{$host} by {backend,server}", w=8, precision=0),
              ts("w3", "Sessions des frontaux", 0, 3, ["sum:haproxy.frontend.sessions.current{$host} by {frontend}"], "count",
                 precision=0),
              ts("w4", "Réponses 5xx des backends (par s)", 6, 3, ["sum:haproxy.backend.response.5xx{$host} by {backend}"],
                 "operation_per_second"),
              ts("w5", "Débit des frontaux (octets/s)", 0, 7, ["sum:haproxy.frontend.bytes.in{$host}", "sum:haproxy.frontend.bytes.out{$host}"],
                 "byte_per_second", display="area"),
              ts("w6", "File d'attente des backends", 6, 7, ["max:haproxy.backend.queue.current{$host} by {backend}"], "count",
                 precision=0),
              ts("w7", "Erreurs (par s)", 0, 11, ["sum:haproxy.frontend.errors{$host} by {frontend}",
                                                  "sum:haproxy.backend.errors{$host} by {backend}"], "operation_per_second", w=12),
          ]),
    # ── Messagerie, DNS, sécurité ────────────────────────────────────────────
    board("mail", "Messagerie", "Messagerie",
          "Files et remises de postfix, connexions et authentifications de Dovecot, analyse de rspamd, files d'Exchange.",
          ["postfix", "dovecot", "rspamd", "exchange"], [
              ts("w1", "postfix : files", 0, 0, ["max:postfix.queue.size{$host} by {queue}"], "count", precision=0),
              ts("w2", "postfix : remises", 6, 0, ["sum:postfix.delivery.sent{$host}", "sum:postfix.delivery.deferred{$host}",
                                                   "sum:postfix.delivery.bounced{$host}"], display="bars"),
              ts("w3", "Dovecot : connexions", 0, 4, ["sum:dovecot.connections{$host} by {host}"], "count", precision=0),
              ts("w4", "Dovecot : authentifications", 6, 4, ["sum:dovecot.auth.success{$host}", "sum:dovecot.auth.failures{$host}"],
                 display="bars"),
              ts("w5", "rspamd : messages analysés, spam, ham", 0, 8, ["sum:rspamd.messages.scanned{$host}", "sum:rspamd.messages.spam{$host}",
                                                                       "sum:rspamd.messages.ham{$host}"]),
              ts("w6", "rspamd : actions", 6, 8, ["sum:rspamd.actions{$host} by {action}"], display="area"),
              ts("w7", "Exchange : messages prêts et différés", 0, 12, ["sum:exchange.queue.ready{$host}", "sum:exchange.queue.deferred{$host}",
                                                                        "sum:exchange.queue.retry{$host}"], "count", precision=0),
              checks("w8", "Exchange : bases montées", 6, 12, "exchange.database.mounted", w=6),
          ]),
    board("dns", "Réseau", "DNS BIND",
          "Requêtes par type, réponses NXDOMAIN et SERVFAIL, clients récursifs et numéros de série des zones.",
          ["bind"], [
              ts("w1", "Requêtes par type (par s)", 0, 0, ["sum:bind.queries{$host} by {type}"], "operation_per_second", display="area"),
              ts("w2", "NXDOMAIN et SERVFAIL", 6, 0, ["sum:bind.responses.nxdomain{$host}", "sum:bind.responses.servfail{$host}"]),
              ts("w3", "Clients récursifs", 0, 4, ["max:bind.recursive_clients{$host} by {host}"], "count", precision=0),
              top("w4", "Numéro de série des zones", 6, 4, "max:bind.zone.serial{$host} by {host,zone}", precision=0),
          ]),
    board("security", "Sécurité", "Certificats et accès",
          "Échéance des certificats, adresses bannies par fail2ban, clients OpenVPN et échecs d'authentification.",
          ["x509", "fail2ban", "openvpn"], [
              checks("w1", "Certificats", 0, 0, "tls.cert.expiry"),
              top("w2", "Jours avant l'échéance", 3, 0, "min:tls.cert.days_left{$host} by {host,subject}", w=9, precision=0,
                  thresholds=[{"value": 21, "color": "warn"}, {"value": 7, "color": "error"}]),
              ts("w3", "fail2ban : adresses bannies", 0, 4, ["sum:fail2ban.banned{$host} by {jail}"], "count", precision=0),
              ts("w4", "OpenVPN : clients connectés", 6, 4, ["sum:openvpn.clients.connected{$host} by {host}"], "count", precision=0),
              logs("w5", "Échecs d'authentification", 0, 8, "@evt.name:authentication @evt.outcome:failure"),
          ]),
    board("snmp", "Réseau", "Équipements réseau (SNMP)",
          "Débit et charge des interfaces, état des ports, processeur, mémoire, températures, stockage et onduleurs.",
          ["snmp"], [
              checks("w1", "Équipements joignables", 0, 0, "snmp.can_poll"),
              checks("w2", "Interfaces", 3, 0, "snmp.interface.status"),
              qv("w3", "Équipements injoignables", 6, 0, "max:snmp.devices.unreachable{*}", "count",
                 thresholds=[{"value": 1, "color": "error"}]),
              qv("w4", "Charge de batterie (%)", 9, 0, "min:snmp.ups.battery.charge{$host}", "percent"),
              top("w5", "Interfaces les plus chargées (entrée, %)", 0, 3, "max:snmp.ifBandwidthInUsage{$host} by {host,interface}",
                  "percent", thresholds=WARN80),
              top("w6", "Interfaces les plus chargées (sortie, %)", 6, 3, "max:snmp.ifBandwidthOutUsage{$host} by {host,interface}",
                  "percent", thresholds=WARN80),
              ts("w7", "Débit entrant (octets/s)", 0, 7, ["sum:snmp.ifBandwidthIn{$host} by {host,interface}"], "byte_per_second"),
              ts("w8", "Débit sortant (octets/s)", 6, 7, ["sum:snmp.ifBandwidthOut{$host} by {host,interface}"], "byte_per_second"),
              ts("w9", "Processeur et mémoire (%)", 0, 11, ["avg:snmp.cpu.usage{$host} by {host}", "avg:snmp.memory.usage{$host} by {host}"],
                 "percent"),
              ts("w10", "Températures (°C)", 6, 11, ["max:snmp.temperature{$host} by {host}"]),
              top("w11", "Volumes et stockages (%)", 0, 15, "max:snmp.volume.usage{$host} by {host,volume}", "percent", w=12,
                  thresholds=WARN80),
          ]),
    # ── Conteneurs et orchestration ──────────────────────────────────────────
    board("docker", "Conteneurs", "Docker",
          "Processeur, mémoire et réseau de chaque conteneur.", ["docker"], [
              top("w1", "Conteneurs les plus gourmands (CPU, %)", 0, 0, "max:container.cpu.usage{$host} by {host,container_name}",
                  "percent"),
              top("w2", "Conteneurs les plus gourmands (mémoire)", 6, 0, "max:container.mem.usage{$host} by {host,container_name}",
                  "byte"),
              ts("w3", "CPU par conteneur (%)", 0, 4, ["avg:container.cpu.usage{$host} by {container_name}"], "percent"),
              ts("w4", "Mémoire par conteneur", 6, 4, ["max:container.mem.usage{$host} by {container_name}"], "byte"),
              ts("w5", "Reçu (octets/s)", 0, 8, ["sum:container.net.rcvd{$host} by {container_name}"], "byte_per_second"),
              ts("w6", "Émis (octets/s)", 6, 8, ["sum:container.net.sent{$host} by {container_name}"], "byte_per_second"),
          ]),
    board("kubernetes", "Conteneurs", "Kubernetes",
          "Nœuds prêts, pods par état, réservations de processeur et de mémoire, charges dégradées et redémarrages.",
          ["kubernetes"], [
              checks("w1", "API de la grappe", 0, 0, "kubernetes.can_connect"),
              checks("w2", "Nœuds", 3, 0, "kubernetes.node"),
              checks("w3", "Charges de travail", 6, 0, "kubernetes.workload"),
              qv("w4", "Pods impossibles à placer", 9, 0, "max:kubernetes.pods.unschedulable{*}", "count",
                 thresholds=[{"value": 1, "color": "warn"}]),
              ts("w5", "Pods par état", 0, 3, ["sum:kubernetes.pods.running{*} by {kube_cluster}", "sum:kubernetes.pods.pending{*} by {kube_cluster}",
                                               "sum:kubernetes.pods.failed{*} by {kube_cluster}"], "count", precision=0),
              ts("w6", "Nœuds prêts / total", 6, 3, ["sum:kubernetes.nodes.ready{*} by {kube_cluster}",
                                                     "sum:kubernetes.nodes{*} by {kube_cluster}"], "count", precision=0),
              top("w7", "Processeur réservé par nœud (%)", 0, 7, "max:kubernetes.node.cpu.requests_pct{*} by {kube_node}", "percent",
                  thresholds=WARN80),
              top("w8", "Mémoire réservée par nœud (%)", 6, 7, "max:kubernetes.node.mem.requests_pct{*} by {kube_node}", "percent",
                  thresholds=WARN80),
              ts("w9", "Redémarrages de conteneurs", 0, 11, ["sum:kubernetes.containers.restarts{*} by {kube_namespace}"], "count",
                 display="bars", precision=0),
              top("w10", "Répliques disponibles", 6, 11, "min:kubernetes.workload.available{*} by {kube_namespace,kube_workload}",
                  "count", precision=0),
          ], tvars=[]),
    # ── Virtualisation ───────────────────────────────────────────────────────
    board("libvirt", "Virtualisation", "KVM, libvirt et VirtualBox",
          "Domaines KVM (processeur, mémoire, disques, réseau), pools de stockage et VM VirtualBox.",
          ["libvirt", "virtualbox"], [
              checks("w1", "Domaines", 0, 0, "libvirt.domain"),
              checks("w2", "Pools", 3, 0, "libvirt.pool"),
              qv("w3", "Domaines en marche", 6, 0, "sum:libvirt.domains.running{$host}", "count"),
              qv("w4", "VM VirtualBox en marche", 9, 0, "sum:virtualbox.vms.running{$host}", "count"),
              top("w5", "Processeur des domaines (%)", 0, 3, "max:libvirt.domain.cpu.usage{$host} by {libvirt_domain}", "percent"),
              top("w6", "Mémoire des domaines", 6, 3, "max:libvirt.domain.mem.rss{$host} by {libvirt_domain}", "byte"),
              ts("w7", "Disques (octets/s)", 0, 7, ["sum:libvirt.domain.disk.read{$host} by {libvirt_domain}",
                                                    "sum:libvirt.domain.disk.write{$host} by {libvirt_domain}"], "byte_per_second"),
              ts("w8", "Réseau (octets/s)", 6, 7, ["sum:libvirt.domain.net.rx{$host} by {libvirt_domain}",
                                                   "sum:libvirt.domain.net.tx{$host} by {libvirt_domain}"], "byte_per_second"),
              top("w9", "Pools de stockage (%)", 0, 11, "max:libvirt.pool.usage{$host} by {host,libvirt_pool}", "percent", w=12,
                  thresholds=WARN80),
          ]),
    board("proxmox", "Virtualisation", "Proxmox VE",
          "Quorum, nœuds, invités, processeur et mémoire des nœuds, stockages, haute disponibilité et sauvegardes.",
          ["proxmox"], [
              checks("w1", "Quorum", 0, 0, "proxmox.cluster.quorum"),
              checks("w2", "Nœuds", 3, 0, "proxmox.node"),
              checks("w3", "Sauvegardes", 6, 0, "proxmox.backup"),
              qv("w4", "Invités en marche", 9, 0, "sum:proxmox.guests.running{*}", "count"),
              ts("w5", "Processeur des nœuds (%)", 0, 3, ["avg:proxmox.node.cpu.usage{*} by {proxmox_node}"], "percent",
                 thresholds=WARN80),
              ts("w6", "Mémoire des nœuds (%)", 6, 3, ["avg:proxmox.node.mem.usage{*} by {proxmox_node}"], "percent", thresholds=WARN80),
              top("w7", "Stockages (%)", 0, 7, "max:proxmox.storage.usage{*} by {proxmox_node,proxmox_storage}", "percent",
                  thresholds=WARN80),
              top("w8", "Invités les plus gourmands (CPU, %)", 6, 7, "max:proxmox.guest.cpu.usage{*} by {proxmox_guest}", "percent"),
              checks("w9", "Invités en haute disponibilité", 0, 11, "proxmox.guest.ha", w=6),
              checks("w10", "Stockages disponibles", 6, 11, "proxmox.storage", w=6),
          ], tvars=[]),
    board("hyperv", "Virtualisation", "Hyper-V",
          "VM en marche, processeur et mémoire de chaque VM, points de contrôle anciens, réplication.",
          ["hyperv"], [
              checks("w1", "VM", 0, 0, "hyperv.vm"),
              checks("w2", "Réplication", 3, 0, "hyperv.vm.replication"),
              qv("w3", "VM en marche", 6, 0, "sum:hyperv.vms.running{$host}", "count"),
              qv("w4", "VM au total", 9, 0, "sum:hyperv.vms.total{$host}", "count"),
              top("w5", "Processeur des VM (%)", 0, 3, "max:hyperv.vm.cpu.usage{$host} by {host,hyperv_vm}", "percent"),
              ts("w6", "Mémoire attribuée et demandée", 6, 3, ["sum:hyperv.vm.mem.assigned{$host} by {hyperv_vm}",
                                                               "sum:hyperv.vm.mem.demand{$host} by {hyperv_vm}"], "byte"),
              top("w7", "Âge des points de contrôle (jours)", 0, 7, "max:hyperv.vm.checkpoint.age{$host} by {host,hyperv_vm}", w=12),
          ]),
    board("vsphere", "Virtualisation", "VMware vSphere",
          "Hôtes ESXi, datastores, VM (processeur, mémoire), instantanés anciens.", ["vsphere"], [
              checks("w1", "Hôtes", 0, 0, "vsphere.host.status"),
              checks("w2", "Datastores", 3, 0, "vsphere.datastore.status"),
              checks("w3", "VM", 6, 0, "vsphere.vm.status"),
              qv("w4", "VM", 9, 0, "sum:vsphere.vm.count{*}", "count"),
              ts("w5", "Processeur des hôtes (%)", 0, 3, ["avg:vsphere.host.cpu.usage{*} by {vsphere_host}"], "percent", thresholds=WARN80),
              ts("w6", "Mémoire des hôtes (%)", 6, 3, ["avg:vsphere.host.mem.usage{*} by {vsphere_host}"], "percent", thresholds=WARN80),
              top("w7", "Datastores (%)", 0, 7, "max:vsphere.datastore.in_use{*} by {vsphere_datastore}", "percent", thresholds=WARN80),
              top("w8", "VM les plus gourmandes (CPU, %)", 6, 7, "max:vsphere.vm.cpu.usage{*} by {vsphere_vm}", "percent"),
              top("w9", "Instantanés anciens (jours)", 0, 11, "max:vsphere.vm.snapshot.age{*} by {vsphere_vm}", w=12),
          ], tvars=[]),
    # ── Cloud ────────────────────────────────────────────────────────────────
    board("clouds", "Cloud", "Clouds : AWS, Azure, Google Cloud, Scaleway, OVHcloud",
          "Ressources par fournisseur, contrôles d'état, alarmes et alertes relayées, échéances des services OVHcloud.",
          ["aws", "azure", "gcp", "scaleway", "ovhcloud"], [
              qv("w1", "Instances EC2", 0, 0, "sum:aws.ec2.instances{*}", "count"),
              qv("w2", "VM Azure", 3, 0, "sum:azure.vms{*}", "count"),
              qv("w3", "Instances Google", 6, 0, "sum:gcp.instances{*}", "count"),
              qv("w4", "Instances Scaleway", 9, 0, "sum:scaleway.instances{*}", "count"),
              checks("w5", "EC2", 0, 2, "aws.ec2.status"),
              checks("w6", "Alarmes CloudWatch", 3, 2, "aws.cloudwatch.alarm"),
              checks("w7", "Santé des VM Azure", 6, 2, "azure.vm.health"),
              checks("w8", "Alertes Azure Monitor", 9, 2, "azure.alerts"),
              checks("w9", "Google Compute Engine", 0, 5, "gcp.instance.status"),
              checks("w10", "Scaleway", 3, 5, "scaleway.instance.state"),
              checks("w11", "Serveurs OVH", 6, 5, "ovh.dedicated_server.state"),
              checks("w12", "Volumes EBS", 9, 5, "aws.ebs.volume"),
              top("w13", "Services OVHcloud : jours avant l'échéance", 0, 8, "min:ovh.service.days_left{*} by {ovh_name}", w=6,
                  precision=0, thresholds=[{"value": 30, "color": "warn"}, {"value": 7, "color": "error"}]),
              ts("w14", "CPU des instances EC2 (%)", 6, 8, ["avg:aws.ec2.cpu.usage{*} by {aws_instance}"], "percent"),
          ], tvars=[]),
    # ── Moteurs génériques (0.5.0) ───────────────────────────────────────────
    board("openmetrics", "Collecte générique", "Cibles OpenMetrics et Prometheus",
          "Toutes les cibles lues par le lecteur OpenMetrics : joignables, séries envoyées, séries écartées par le "
          "plafond de cardinalité, durée des relevés.", ["openmetrics"], [
              checks("w1", "Cibles joignables", 0, 0, "openmetrics.can_connect", w=4),
              qv("w2", "Séries envoyées", 4, 0, "sum:openmetrics.series{$host}", "count", w=4),
              qv("w3", "Séries écartées (plafond)", 8, 0, "sum:openmetrics.series.dropped{$host}", "count", w=4,
                 thresholds=[{"value": 1, "color": "warn"}]),
              ts("w4", "Séries par cible", 0, 3, ["sum:openmetrics.series{$host} by {host,instance_name}"], "count", precision=0),
              ts("w5", "Durée des relevés (s)", 6, 3, ["max:openmetrics.scrape.duration{$host} by {host,instance_name}"], "second",
                 precision=3),
              top("w6", "Séries écartées par cible", 0, 7, "max:openmetrics.series.dropped{$host} by {host,instance_name}", "count",
                  w=12, precision=0),
          ]),
    board("node_exporter", "Collecte générique", "node_exporter (Prometheus)",
          "Machines lues par leur node_exporter (profil node_exporter) : charge, processeur par mode, mémoire, "
          "systèmes de fichiers, réseau et disques.", ["openmetrics:node_exporter"], [
              ts("w1", "Charge (1 min)", 0, 0, ["avg:node.load.1{$host} by {host,instance_name}"]),
              ts("w2", "Processeur par mode (cœurs)", 6, 0, ["sum:node.cpu.seconds{$host,-mode:idle} by {mode}"], display="area"),
              ts("w3", "Mémoire disponible", 0, 4, ["min:node.memory.available{$host} by {host,instance_name}"], "byte"),
              top("w4", "Espace libre des systèmes de fichiers", 6, 4, "min:node.filesystem.avail{$host} by {instance_name,mountpoint}",
                  "byte"),
              ts("w5", "Réseau reçu et émis (octets/s)", 0, 8, ["sum:node.network.receive.bytes{$host} by {device}",
                                                                 "sum:node.network.transmit.bytes{$host} by {device}"], "byte_per_second"),
              ts("w6", "Disques : lus et écrits (octets/s)", 6, 8, ["sum:node.disk.read.bytes{$host} by {device}",
                                                                     "sum:node.disk.written.bytes{$host} by {device}"], "byte_per_second"),
              ts("w7", "Disques occupés (part du temps)", 0, 12, ["max:node.disk.io_time{$host} by {device}"], precision=2),
              ts("w8", "Processus en marche et bloqués", 6, 12, ["sum:node.procs.running{$host}", "sum:node.procs.blocked{$host}"],
                 "count", precision=0),
          ]),
    board("prometheus", "Collecte générique", "Serveur Prometheus",
          "Le serveur Prometheus lu par le profil prometheus : séries en tête, échantillons ajoutés, durée des requêtes, "
          "règles en échec, notifications perdues, rechargement de la conf.", ["openmetrics:prometheus"], [
              qv("w1", "Séries en tête", 0, 0, "max:prometheus.tsdb.head_series{$host}", "count"),
              qv("w2", "Dernier rechargement réussi (1 = oui)", 3, 0, "min:prometheus.config.last_reload_successful{$host}"),
              qv("w3", "Règles en échec (par s)", 6, 0, "sum:prometheus.rule.evaluation_failures{$host}", precision=2),
              qv("w4", "Notifications perdues (par s)", 9, 0, "sum:prometheus.notifications.dropped{$host}", precision=2),
              ts("w5", "Échantillons ajoutés (par s)", 0, 2, ["sum:prometheus.tsdb.samples_appended{$host} by {host}"],
                 "operation_per_second"),
              ts("w6", "Requêtes HTTP par code (par s)", 6, 2, ["sum:prometheus.http.requests{$host} by {code}"], "operation_per_second"),
              ts("w7", "Durée des requêtes HTTP, p95 (s)", 0, 6, ["max:prometheus.http.request_duration.p95{$host} by {handler}"],
                 "second", precision=3),
              ts("w8", "Régularité des relevés, p99 (s)", 6, 6, ["max:prometheus.target.interval_length.p99{$host} by {interval}"],
                 "second", precision=3),
              ts("w9", "Mémoire résidente", 0, 10, ["max:prometheus.process.resident_memory{$host} by {host}"], "byte", w=12),
          ]),
    board("nagios", "Collecte générique", "Plugins Nagios et Centreon",
          "État des plugins lancés par les agents (nagios.check), leur durée, et leurs données de performance.",
          ["nagios"], [
              checks("w1", "Plugins", 0, 0, "nagios.check", w=4, h=4),
              {"id": "w2", "type": "table", "title": "Données de performance (dernière valeur)", "x": 4, "y": 0, "w": 8, "h": 4,
               "queries": [{"q": "avg:nagios.perfdata{$host} by {host,nagios_check,perf_label}"}],
               "options": {"precision": 2, "aggs": ["last"], "aliases": ["Valeur"]}},
              ts("w3", "Durée des plugins (s)", 0, 4, ["max:nagios.duration{$host} by {host,nagios_check}"], "second", precision=2),
              ts("w4", "Données de performance", 6, 4, ["avg:nagios.perfdata{$host} by {nagios_check,perf_label}"]),
          ]),
    board("syslog", "Collecte générique", "Journaux reçus par syslog",
          "Lignes reçues par émetteur, émetteurs muets, lignes jetées (débit, file pleine, source refusée), connexions "
          "TCP et dernières erreurs des équipements.", ["syslog"], [
              checks("w1", "Réception", 0, 0, "syslog.can_receive"),
              qv("w2", "Lignes reçues", 3, 0, "sum:syslog.messages{$host}", "count"),
              qv("w3", "Lignes jetées", 6, 0, "sum:syslog.dropped{$host}", "count", thresholds=[{"value": 1, "color": "warn"}]),
              qv("w4", "Connexions TCP", 9, 0, "sum:syslog.connections{$host}", "count"),
              ts("w5", "Lignes reçues par émetteur", 0, 2, ["sum:syslog.messages{$host} by {syslog_host}"], "count", display="bars",
                 precision=0),
              top("w6", "Émetteurs les plus bavards", 6, 2, "sum:syslog.messages{$host} by {syslog_host}", "count", precision=0),
              ts("w7", "Lignes jetées par motif", 0, 6, ["sum:syslog.dropped{$host} by {reason}"], "count", display="bars", w=12,
                 precision=0),
              logs("w8", "Erreurs reçues des équipements", 0, 10, "source:syslog (" + LOG_ERRORS + ")"),
          ]),
]

BY_ID = {c["id"]: c for c in CATALOGUE}


def catalogue() -> list[dict]:
    """Tableaux livrés, sans leurs widgets (taille), avec leur nombre de widgets."""
    out = []
    for c in CATALOGUE:
        d = c["dashboard"]
        out.append({"id": c["id"], "family": c["family"], "title": d["title"], "description": d["description"],
                    "integrations": c["integrations"], "widget_count": len(d["widgets"])})
    return out


def get(cid: str) -> dict | None:
    """Définition complète (titre, description, widgets, variables, dossier) d'un tableau du catalogue."""
    c = BY_ID.get(cid)
    return None if c is None else c["dashboard"]


def metric_names() -> set[str]:
    """Métriques visées par les requêtes du catalogue (vérifiées contre le code de l'agent)."""
    import re
    out = set()
    rx = re.compile(r"^[a-z]+:([A-Za-z0-9_.\-/]+)\{")
    for c in CATALOGUE:
        for w in c["dashboard"]["widgets"]:
            for q in w.get("queries", []):
                m = rx.match(q["q"])
                if m:
                    out.add(m.group(1))
    return out


def check_names() -> set[str]:
    return {w["options"]["check"] for c in CATALOGUE for w in c["dashboard"]["widgets"]
            if w["type"] == "check_status" and w["options"].get("check")}
