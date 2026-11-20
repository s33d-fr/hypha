---
id: applications
path: /docs/applications-et-services/
title: Applications et services : PHP-FPM, Varnish, Squid, Gunicorn, PowerDNS, OpenLDAP, Keycloak, Nexus, Harbor, Ceph…
nav: Applications et services
section: Intégrations
order: 27
updated: 2026-11-20
description: Sondes dédiées de l'agent HYPHA pour PHP-FPM, Gunicorn, Varnish, Squid, Lighttpd, Supervisor, Fluentd, PowerDNS (serveur faisant autorité et Recursor), OpenLDAP, Keycloak, Sonatype Nexus, Harbor, ZooKeeper et Ceph : réglages, droits, mesures et contrôles.
lead: Chaque logiciel est lu par son interface native (page de statut, socket d'administration, commande de statistiques, API REST), en lecture seule.
---
## Principe

Ces sondes suivent les règles des autres intégrations ([Système, web et messagerie](/docs/integrations/)) : en mode `auto`, une sonde dont le logiciel répond à l'adresse habituelle s'active seule ; celles qui demandent un compte, une clé ou une adresse sans valeur par défaut ne s'activent que lorsqu'on les leur donne. Une fois le logiciel vu, son contrôle `<nom>.can_connect` passe en CRIT s'il ne répond plus. Les compteurs cumulés deviennent des écarts par intervalle. Plusieurs cibles d'une même sonde se déclarent par `instances` ([Configuration](/docs/configuration/)).

Chaque sonde a ses [modèles de moniteurs](/docs/moniteurs/) et son tableau prêt (**Tableaux › Catalogue**).

## PHP-FPM

L'agent parle FastCGI directement à la socket du pool et lit sa page de statut : aucune page à ouvrir dans le serveur web. Sans réglage, chaque socket `/run/php/php*-fpm.sock` ou `/run/php-fpm/*.sock` est lue, un pool par socket. Dans la conf de chaque pool :

```ini
pm.status_path = /status
ping.path = /ping
```

```yaml
integrations:
  phpfpm:
    address: 127.0.0.1:9000     # ou /run/php/php8.3-fpm.sock ; ou url: http://127.0.0.1/status
```

Mesures (étiquette `pool`) : processus actifs, inoccupés et totaux, part occupée (`php_fpm.processes.usage`), file d'attente (`php_fpm.listen_queue.size`), requêtes acceptées et lentes, plafond `pm.max_children` atteint (`php_fpm.processes.max_reached`). Contrôles `php_fpm.can_connect` et `php_fpm.can_ping`. Un pool trouvé sans page de statut reste « inactif » (`hypha-agent status` le dit), sans alerte.

## Gunicorn

L'agent reconnaît les maîtres Gunicorn dans `/proc` (titre `gunicorn: master [nom]` si setproctitle est installé, sinon `--name` ou le module WSGI de la ligne de commande) et compte leurs workers, occupés ou inoccupés (`gunicorn.worker_processes`, étiquettes `app` et `state`). Contrôle `gunicorn.is_running` : CRIT si une application déjà vue (ou listée dans `apps`) n'a plus de maître ou plus de worker.

Les requêtes viennent du StatsD natif de Gunicorn, reçu par l'agent :

```bash
gunicorn --statsd-host 127.0.0.1:8125 app:app
```

Métriques StatsD de Gunicorn : `gunicorn.requests`, `gunicorn.request.duration`, `gunicorn.request.status.<code>`, `gunicorn.workers`, `gunicorn.log.error`.

```yaml
integrations:
  gunicorn:
    apps: [boutique, "api.main:app"]   # facultatif : applications attendues
```

## Varnish

`varnishstat -j` (formes de Varnish 6.5 et plus, et d'avant), et la santé des serveurs d'origine par `varnishadm backend.list -j`. Pour un Varnish en conteneur ou une instance nommée :

```yaml
integrations:
  varnish:
    command: "docker exec varnish varnishstat"   # ou « varnishstat -n nom »
```

Mesures : requêtes clientes, succès, passes et défauts du cache, taux de succès (`varnish.cache_hit_rate`), objets, expirations et évictions (`varnish.n_lru_nuked`), connexions abandonnées, fils de travail, connexions, requêtes et échecs par serveur d'origine, stockage occupé et libre. Contrôle `varnish.backend_healthy` par serveur d'origine (CRIT s'il échoue à ses sondes). Sans accès au secret de `varnishadm`, la santé des serveurs d'origine n'est pas lue et le reste continue.

## Squid

Pages du gestionnaire de cache servies par Squid sur son port (`http://127.0.0.1:3128/squid-internal-mgr/counters` et `info`) ; la conf par défaut (`http_access allow localhost manager`) suffit depuis la machine. Un `cachemgr_passwd` se donne par `password`.

Mesures : requêtes clientes, succès, erreurs, Kio échangés avec les clients et les serveurs, taux de succès (`squid.cachemgr.client_http.hit_rate`), requêtes abandonnées, clients, descripteurs utilisés et maximum, stockage.

## Lighttpd

Page `mod_status` (`status.status-url = "/server-status"`), lue en `?auto`. Le port 80 de l'hôte étant d'abord celui d'Apache ou de nginx, l'adresse se donne :

```yaml
integrations:
  lighttpd:
    url: http://127.0.0.1:8080/server-status
```

Mesures : requêtes et octets (totaux, par seconde), connexions actives et inoccupées, durée de fonctionnement.

## Supervisor

API XML-RPC de Supervisor, par la socket Unix du `[unix_http_server]` (trouvée seule à `/run/supervisor.sock`, `/var/run/supervisor.sock`…) ou par le `[inet_http_server]` (`url: http://127.0.0.1:9001/RPC2`, `username`, `password`). Une socket sous `/tmp` est invisible pour l'agent (`PrivateTmp` de son unité systemd) : placez-la sous `/run`.

Un contrôle `supervisord.process.status` par programme : RUNNING en OK, STARTING en UNKNOWN, STOPPED en WARN, FATAL, EXITED et BACKOFF en CRIT, avec la raison donnée par Supervisor. Mesures : programmes par état, durée de fonctionnement de chacun.

## Fluentd

Greffon `monitor_agent` de Fluentd :

```
<source>
  @type monitor_agent
  bind 127.0.0.1
  port 24220
</source>
```

Par greffon (étiquettes `plugin_id`, `type`, `plugin_category`) : morceaux et octets en file d'attente, place libre du tampon (`fluentd.buffer_available_buffer_space_ratios`), nouveaux essais, enregistrements émis et écrits, annulations, vidages lents. Nommez chaque greffon (`@id`) : sans lui, son identifiant change à chaque redémarrage.

## PowerDNS

API du serveur web intégré, avec la clé de l'API (`webserver=yes`, `api=yes`, `api-key=…`). Deux sondes : `powerdns` (serveur faisant autorité, port 8081) et `powerdns_recursor` (port 8082).

```yaml
integrations:
  powerdns:
    url: http://127.0.0.1:8081
    api_key: "…"
  powerdns_recursor:
    url: http://127.0.0.1:8082
    api_key: "…"
```

Serveur faisant autorité : requêtes UDP et TCP, réponses, latence, caches de paquets et de requêtes (taux de succès), SERVFAIL, délais dépassés, paquets corrompus, file d'attente, signatures. Recursor : questions, requêtes sortantes, réponses par tranche de délai, caches et taux de succès, SERVFAIL, NXDOMAIN, délais dépassés, requêtes en cours, latence. Contrôles `powerdns.authoritative.security_status` et `powerdns.recursor.security_status` : WARN si PowerDNS conseille une mise à jour, CRIT si la version est vulnérable.

## OpenLDAP

Base `cn=Monitor` de slapd : connexions, opérations initiées et terminées par type, octets et entrées envoyés, fils (`openldap.threads`, étiquette `status`), attentes en lecture et écriture, durée de fonctionnement. La base `monitor` doit être déclarée dans slapd avec un droit de lecture pour le compte de l'agent :

```ldif
dn: cn=module{0},cn=config
changetype: modify
add: olcModuleLoad
olcModuleLoad: back_monitor

dn: olcDatabase={2}monitor,cn=config
changetype: add
objectClass: olcDatabaseConfig
olcDatabase: {2}monitor
olcAccess: {0}to * by dn.exact="gidNumber=0+uidNumber=0,cn=peercred,cn=external,cn=auth" read by * none
```

```yaml
integrations:
  openldap:
    url: "ldapi://%2Frun%2Fslapd%2Fldapi"   # SASL EXTERNAL : identité root de la socket
    # ou : url: ldaps://ldap.exemple:636, bind_dn: "cn=hypha,dc=exemple,dc=fr", password: "…"
```

## Keycloak

Santé sur le port de gestion (`KC_HEALTH_ENABLED=true`, `/health/ready`, base de données comprise) et sessions par l'API d'administration. Pour les sessions, un client confidentiel du royaume `master` avec « Service accounts roles », dont le compte de service porte `view-realm` et `view-users` du client `<royaume>-realm` de chaque royaume lu :

```yaml
integrations:
  keycloak:
    url: http://127.0.0.1:9000          # port de gestion
    admin_url: http://127.0.0.1:8080
    client_id: hypha
    client_secret: "…"
    # realms: [clients]                 # vide : tous ceux que le compte voit
```

Mesures : `keycloak.health.ready`, sessions actives et hors ligne par royaume et par client, utilisateurs par royaume. Contrôles `keycloak.can_connect` (DOWN : CRIT, avec les contrôles en défaut) et `keycloak.admin_api`. Les mesures JVM et HTTP de Keycloak ne sont publiées qu'au format Prometheus (`/metrics`) : elles se lisent par le [lecteur OpenMetrics](/docs/collecte-generique/).

## Sonatype Nexus

API REST : disponibilité (`/service/rest/v1/status`) et écriture possible (`nexus.writable`, CRIT en lecture seule) sans compte ; avec un compte dont le rôle porte `nx-metrics-all` et `nx-blobstores-read` : contrôles de santé du système (`nexus.health`, WARN avec le nom des contrôles en défaut), tas JVM, fils, requêtes et réponses HTTP par famille, latence, taille et place libre des magasins de blobs.

```yaml
integrations:
  nexus:
    url: http://127.0.0.1:8081
    username: hypha
    password: "…"
```

## Harbor

API v2.0 : santé de chaque composant (`harbor.component`, sans compte) et de l'ensemble (`harbor.status`), version et lecture seule ; avec un compte administrateur : projets, dépôts, stockage consommé, disque du registre (`harbor.disk.usage`).

```yaml
integrations:
  harbor:
    url: https://harbor.exemple
    username: admin
    password: "…"
```

## ZooKeeper

Commandes de quatre lettres `mntr` et `ruok` sur `127.0.0.1:2181`. Depuis ZooKeeper 3.5, elles doivent figurer dans `4lw.commands.whitelist=mntr,ruok,srvr` (`ZOO_4LW_COMMANDS_WHITELIST` dans l'image Docker officielle). Mesures : latences, requêtes en attente, connexions, nœuds, observateurs, éphémères, taille des données, descripteurs, suiveurs et suiveurs synchronisés (sur le meneur), paquets, rôle (`zookeeper.server_state`). Contrôles `zookeeper.can_connect` et `zookeeper.ruok`.

## Ceph

`ceph status` et `ceph df` en JSON, sur un nœud de la grappe disposant de l'outil `ceph` et d'un trousseau lisible ; en mode auto, seulement si `/etc/ceph/ceph.conf` existe. Un client en lecture seule suffit (`ceph auth get-or-create client.hypha mon 'allow r' mgr 'allow r'`) :

```yaml
integrations:
  ceph:
    command: "ceph --id hypha"          # ou « docker exec ceph ceph », « cephadm shell -- ceph »
```

Mesures (étiquette `ceph_fsid`) : moniteurs et quorum, OSD en service et dans la grappe, groupes de placement par état et part active+clean, capacité et occupation, débits et opérations, occupation et place utile de chaque pool. Contrôle `ceph.overall_status` : HEALTH_WARN en WARN, HEALTH_ERR en CRIT, avec les contrôles de santé non masqués. Un seul agent par grappe suffit.
