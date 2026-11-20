---
id: databases
path: /docs/bases-de-donnees/
title: Bases de données, caches, files et moteurs de recherche
nav: Bases de données
section: Intégrations
order: 21
updated: 2026-11-20
description: PostgreSQL et ses requêtes les plus coûteuses (DBM), MySQL et MariaDB, Redis et Valkey, Memcached, MongoDB, RabbitMQ, OpenSearch et Elasticsearch, PgBouncer, ProxySQL, ClickHouse, CouchDB, Couchbase, SQL Server : réglages, droits et mesures.
lead: Chaque intégration lit le logiciel avec un compte de supervision en lecture seule, ou par la socket locale quand c'est possible.
---
## PostgreSQL

Actif d'office si la socket `/var/run/postgresql/.s.PGSQL.5432` existe. L'agent se connecte par cette socket en tant qu'utilisateur système `postgres` (authentification `peer`, sans mot de passe), dans une session en lecture seule.

```yaml
integrations:
  postgres:
    enabled: auto
    # ou un compte de supervision qui porte le rôle pg_monitor :
    # dsn: postgres://hypha:motdepasse@127.0.0.1:5432/postgres?sslmode=disable
    dbm: auto
```

Mesures : connexions et leur part du maximum, transactions validées et annulées, lignes lues et écrites, taille des bases, taux de lecture en cache, verrous, requêtes en attente, plus longue requête, sessions inactives dans une transaction, écrivain d'arrière-plan, rôle primaire ou réplique, retard de réplication (`postgresql.replication_delay`, `postgresql.replication.wal_lag_bytes`).

### Requêtes les plus coûteuses (DBM)

Si l'extension `pg_stat_statements` existe dans la base `postgres`, l'agent envoie toutes les 5 minutes les 50 requêtes les plus coûteuses (durée totale), au texte normalisé tel que PostgreSQL le rend. La page **Bases de données** les classe par durée totale, appels ou durée moyenne, avec le taux de lecture en cache et l'évolution de chaque requête.

```sql
-- postgresql.conf : shared_preload_libraries = 'pg_stat_statements' (redémarrage requis s'il manque)
CREATE EXTENSION IF NOT EXISTS pg_stat_statements;
```

L'agent revérifie la présence de l'extension une fois par heure : après sa création, redémarrez `hypha-agent` pour la voir tout de suite.

## MySQL et MariaDB

Sans réglage, l'agent se connecte en `root` par la socket locale `/run/mysqld/mysqld.sock` (authentification par la socket des paquets Debian et Ubuntu). Sinon, un compte de supervision :

```sql
CREATE USER 'hypha'@'127.0.0.1' IDENTIFIED BY '…';
GRANT PROCESS, REPLICATION CLIENT ON *.* TO 'hypha'@'127.0.0.1';
```

```yaml
integrations:
  mysql:
    dsn: "hypha:motdepasse@tcp(127.0.0.1:3306)/"
```

Mesures : connexions (courantes, maximum, part utilisée, refusées, interrompues), requêtes et requêtes lentes, commandes par type, threads actifs, tables temporaires sur disque, cache InnoDB, attentes de verrous, taille des bases, retard et état de la réplication (`mysql.replication`).

## Redis et Valkey

Commande `INFO`, sur `127.0.0.1:6379` par défaut, ou une socket Unix.

```yaml
integrations:
  redis:
    address: 127.0.0.1:6379
    # username: hypha   # ACL de Redis 6 et plus
    # password: "…"
```

Mesures : clients connectés et bloqués, mémoire utilisée, plafond, fragmentation, taux de succès du cache, clés, clés expirées et évincées, commandes, débit réseau, réplication et répliques, sauvegarde sur disque (`redis.persistence`).

## Memcached

Commande `stats` sur `127.0.0.1:11211` : octets et plafond, taux de succès des lectures, évictions, connexions et connexions refusées, éléments, threads.

## MongoDB

URI de connexion, `mongodb://127.0.0.1:27017` par défaut. Avec l'authentification active, un compte au rôle `clusterMonitor` :

```yaml
integrations:
  mongodb:
    uri: "mongodb://hypha:motdepasse@127.0.0.1:27017/?authSource=admin"
```

Mesures : connexions courantes et disponibles, opérations, files de lecture et d'écriture, cache WiredTiger, mémoire résidente, taille des bases, état et retard du jeu de réplicas.

## RabbitMQ

API de gestion (`http://127.0.0.1:15672`), avec un compte au rôle `monitoring` (sans compte : `guest`, accepté par RabbitMQ depuis la machine elle-même seulement).

```yaml
integrations:
  rabbitmq:
    url: http://127.0.0.1:15672
    username: hypha
    password: "…"
```

Mesures : connexions, canaux, consommateurs, files, messages prêts et non acquittés, débits de publication, de remise et d'acquittement, alarmes du nœud (mémoire, disque, descripteurs) par le contrôle `rabbitmq.node`, messages par file.

## OpenSearch et Elasticsearch

API REST du nœud local : l'agent trouve l'adresse sur laquelle le port 9200 écoute, ou prend `url`.

```yaml
integrations:
  opensearch:
    url: https://127.0.0.1:9200
    username: hypha
    password: "…"
    insecure: false     # true seulement pour un certificat autosigné
```

Mesures : santé de la grappe (`opensearch.cluster_health`), nœuds, fragments actifs, en cours d'attribution, non attribués, tâches en attente, tas JVM et ramasse-miettes, occupation du disque, documents, indexation et recherche (débit, latence), écritures et recherches rejetées, disjoncteurs déclenchés.

## SQL Server

Sous Windows, par les compteurs de performance, si SQL Server est présent : `mssql.buffer_cache_hit_ratio`, `mssql.batch_requests_per_s`, `mssql.user_connections`.

## PgBouncer

Console d'administration (base `pgbouncer`), avec un compte listé dans `stats_users` de `pgbouncer.ini` :

```yaml
integrations:
  pgbouncer:
    dsn: postgres://hypha:motdepasse@127.0.0.1:6432/pgbouncer?sslmode=disable
```

Mesures : clients actifs et en attente, connexions serveur actives, inoccupées, utilisées, testées et en ouverture, attente maximale (`pgbouncer.pools.maxwait`) par base et par utilisateur ; requêtes, transactions, octets reçus et envoyés, temps moyens des requêtes et des transactions par base ; connexions et taille de pool par base. Contrôles `pgbouncer.can_connect` et `pgbouncer.database` (WARN pour une base en pause ou désactivée).

## ProxySQL

Interface d'administration (port 6032), avec le compte de `admin-stats_credentials` (lecture des tables `stats` seulement) :

```yaml
integrations:
  proxysql:
    dsn: "stats:motdepasse@tcp(127.0.0.1:6032)/"
```

Mesures : transactions actives, connexions clientes et serveur (ouvertes, créées, interrompues), requêtes et requêtes lentes, temps de traitement, mémoire des tampons, cache de requêtes (taux de succès), accès refusés ; par serveur MySQL derrière ProxySQL (étiquettes `hostgroup`, `backend`) : connexions utilisées et libres, réussies et en erreur, requêtes, octets, latence. Contrôle `proxysql.backend.status` par serveur : ONLINE en OK, OFFLINE_SOFT en WARN, SHUNNED et OFFLINE_HARD en CRIT.

## ClickHouse

Interface HTTP (`http://127.0.0.1:8123`), tables `system.metrics`, `system.events` et `system.asynchronous_metrics`. Un compte en lecture seule suffit (`readonly = 1`) ; sans compte, l'utilisateur `default`. L'identité passe par les en-têtes `X-ClickHouse-User` et `X-ClickHouse-Key`, jamais par l'URL.

```yaml
integrations:
  clickhouse:
    username: hypha
    password: "…"
```

Mesures : requêtes en cours, comptées, échouées (SELECT et INSERT à part), durée moyenne, lignes et octets insérés et lus, insertions retardées et refusées, fusions et mutations, connexions par protocole, mémoire suivie et résidente, parts par partition (`clickhouse.part.max_per_partition`), répliques en lecture seule, retard et file des répliques, nombre de bases, tables et parts, durée de fonctionnement.

## CouchDB

API REST du nœud (`/_node/_local/_stats`, `/_system`, `/_active_tasks`). CouchDB 3 réserve ces pages à un administrateur du serveur : la sonde attend son compte.

```yaml
integrations:
  couchdb:
    url: http://127.0.0.1:5984
    username: admin
    password: "…"
```

Mesures : requêtes HTTP (dont groupées et lectures de vues), réponses par famille de codes, temps de réponse (moyen, p95, p99, max), lectures et écritures de documents, bases et fichiers ouverts, réplications en cours, en attente et en échec (`couchdb.couch_replicator.jobs.crashed`), tâches actives par type, mémoire et file d'exécution d'Erlang.

## Couchbase

API REST du cluster (port 8091), avec un compte au rôle « Read-Only Admin » :

```yaml
integrations:
  couchbase:
    url: http://127.0.0.1:8091
    username: hypha
    password: "…"
```

Mesures : RAM et disque du cluster (totaux, utilisés, quotas), nœuds actifs, équilibre ; par nœud : processeur, mémoire, opérations, éléments ; par bucket : opérations par seconde, éléments, mémoire, disque, quota RAM utilisé (`couchbase.by_bucket.quota_percent_used`). Contrôle `couchbase.node.health` par nœud : CRIT hors du cluster ou en mauvaise santé, WARN pendant son réchauffement.

## Modèles de moniteurs

Familles **Bases de données**, **Files de messages** et **Recherche** du catalogue : injoignable, réplication arrêtée ou en retard, connexions presque saturées, mémoire Redis presque pleine, sauvegarde Redis en échec, file RabbitMQ qui s'accumule, nœud RabbitMQ en alarme, grappe OpenSearch dégradée, tas JVM saturé, disque au filigrane, écritures rejetées ; clients PgBouncer en attente et attente trop longue, serveur MySQL écarté par ProxySQL, trop de parts ClickHouse, réplique ClickHouse en lecture seule, réplication CouchDB en échec, nœud Couchbase en défaut, bucket Couchbase presque plein.
