---
id: generic-collection
path: /docs/collecte-generique/
title: Collecte générique : Prometheus, plugins Nagios et syslog
nav: Prometheus, Nagios, syslog
section: Intégrations
order: 25
updated: 2026-11-20
description: Le lecteur OpenMetrics et Prometheus de l'agent HYPHA et ses profils, l'exécution des plugins Nagios, Monitoring Plugins et Centreon, la réception syslog des équipements réseau.
lead: Trois moteurs pour superviser ce qui n'a pas d'intégration dédiée : tout logiciel qui publie ses mesures au format Prometheus, les milliers de plugins Nagios et Centreon existants, et les journaux envoyés par syslog.
---
## Lecteur OpenMetrics et Prometheus

L'agent lit les pages `/metrics` au format d'exposition Prometheus (texte 0.0.4) ou OpenMetrics 1.0 (texte). Une entrée par cible, dans `integrations.openmetrics` :

```yaml
integrations:
  openmetrics:
    - profile: node_exporter              # http://127.0.0.1:9100/metrics par défaut
    - name: monappli
      url: https://appli.exemple:8443/metrics
      interval: 30s                       # 30 s par défaut, 10 s au moins
      timeout: 10s
      namespace: monappli                 # préfixe des métriques HYPHA
      metrics: [http_requests_total, "job_*", "re:queue_(depth|age)_.*"]
      exclude_metrics: ["go_*"]
      rename: {http_requests_total: http.requests}
      labels: [code, method]              # étiquettes gardées comme tags ; "*" = toutes
      tags: {equipe: paiement}
      bearer_token_file: /etc/hypha/monappli.token   # relu à chaque relevé
      ca_file: /etc/hypha/ca-interne.pem  # aussi cert_file, key_file, insecure
      max_series: 2000
```

Les listes de plusieurs fichiers `conf.d` s'additionnent : un fichier par application. D'autres en-têtes passent par `headers` ; `username` et `password` donnent une authentification de base. Les valeurs d'en-têtes et les mots de passe ne s'affichent jamais dans `hypha-agent check-config`.

### Traduction des mesures

| Type Prometheus | Ce qu'HYPHA reçoit |
|---|---|
| jauge, inconnu | la valeur telle quelle |
| compteur | un débit par seconde (`byte_per_second` pour `_bytes`), rien à la première lecture ni après une remise à zéro |
| histogramme | `<nom>.count` et `<nom>.sum` (débits), `<nom>.avg`, centiles `<nom>.p50`, `.p95`, `.p99` estimés comme `histogram_quantile` ; les seaux avec `histogram_buckets: true` |
| résumé | `<nom>.count`, `<nom>.sum`, `<nom>.avg` et chaque quantile publié (`0.5` → `p50`, `0.999` → `p99_9`) |

Nom HYPHA : `<namespace>.<nom>` (le suffixe `_total` d'un compteur est retiré), ou `<namespace>.<rename>`. Les étiquettes Prometheus hors de `labels` sont écartées, et les séries qui ne diffèrent que par elles sont **additionnées** (par exemple `node_cpu_seconds_total` sans l'étiquette `cpu` donne le temps de tous les cœurs). Chaque série porte `instance_name` et `endpoint`.

### Garde-fou de cardinalité

Au-delà de `max_series` séries pour une cible (2 000 par défaut), l'agent garde les premières dans l'ordre alphabétique, écarte les autres, le dit dans l'état de l'agent et dans son journal, et publie `openmetrics.series.dropped`. Le modèle de moniteur « Plafond de séries OpenMetrics atteint » prévient quand cela dure.

### Profils intégrés

Un profil apporte le port, le chemin, les métriques retenues, leurs noms et les étiquettes utiles. Ce que l'entrée ajoute (`metrics`, `rename`, `labels`, `tags`) complète le profil.

| Profil | Port | Mesures (espace de noms) |
|---|---|---|
| `node_exporter` | 9100 | `node.load.1`, `node.cpu.seconds` par mode, `node.memory.*`, `node.filesystem.*` par point de montage, `node.network.*` et `node.disk.*` par périphérique, `node.procs.*`, `node.filefd.*`, `node.hwmon.temperature`, `node.time.offset` |
| `prometheus` | 9090 | `prometheus.tsdb.head_series`, `.tsdb.samples_appended`, `.http.requests` par code, `.http.request_duration.p95`, `.target.interval_length.p99`, `.rule.evaluation_failures`, `.notifications.dropped`, `.config.last_reload_successful`, `.process.resident_memory` |

### Contrôle et mesures de l'agent

- `openmetrics.can_connect` : critique quand la cible ne répond pas, répond en erreur ou rend une exposition illisible (le format protobuf n'est pas lu).
- `openmetrics.series`, `openmetrics.series.dropped`, `openmetrics.scrape.duration` par cible.

## Plugins Nagios, Monitoring Plugins et Centreon

L'agent lance des plugins au format Nagios et en tire un contrôle et des métriques :

```yaml
integrations:
  nagios:
    - name: disque-racine
      command: check_disk -w 20% -c 10% -p /
      interval: 60s                        # 10 s au moins
      timeout: 30s                         # au plus l'intervalle
    - name: http-local
      command: [check_http, -H, 127.0.0.1, -u, /sante]
      user: nagios                         # compte qui lance le plugin (Linux)
    - name: charge-centreon
      command: [/usr/bin/perl, /usr/lib/centreon/plugins/centreon_plugins.pl,
                --plugin=os::linux::local::plugin, --mode=load]
      tags: {role: web}
```

- Code de sortie 0, 1, 2, 3 : contrôle `nagios.check` OK, WARNING, CRITICAL, UNKNOWN, avec l'étiquette `nagios_check` (le nom de l'entrée) et le texte rendu par le plugin. Un autre code compte comme UNKNOWN, un délai dépassé comme CRITICAL.
- Données de performance (`'label'=valeur[unité];warn;crit;min;max`) : métrique `nagios.perfdata` avec l'étiquette `perf_label`, et `nagios.perfdata.warn`, `.crit`, `.min`, `.max` quand le plugin les donne. Unités converties : `s`, `ms`, `us`, `%`, `B`, `KB`, `MB`, `GB`, `TB`, `c`. `perfdata: false` les coupe.
- `nagios.duration` : durée de chaque lancement.

### Sécurité

- Les commandes viennent **seulement** des fichiers de configuration de l'hôte. Le serveur HYPHA ne peut ni en envoyer, ni en modifier.
- Sous Linux, l'agent refuse de lancer quoi que ce soit si un fichier qui déclare des plugins, ou l'exécutable lui-même, n'appartient pas à root ou reste modifiable par le groupe ou par tous (contrôle UNKNOWN qui le dit).
- Lancement direct, sans shell : `$`, `*`, `|` ou `;` restent des caractères ordinaires. Un simple nom de plugin est cherché dans `/usr/lib/nagios/plugins`, `/usr/lib64/nagios/plugins`, `/usr/lib/monitoring-plugins`, `/usr/local/nagios/libexec`, `/usr/lib/centreon/plugins`, jamais dans le `PATH`.
- Environnement réduit (`PATH` système, `LANG=C`), sans les variables de l'agent ; sortie bornée à 64 Kio ; au délai dépassé, le plugin et ses enfants sont arrêtés ; quatre plugins au plus en même temps.
- Sous Windows : chemin absolu obligatoire ; protégez le dossier de configuration de l'agent par ses droits (administrateurs seulement).

Paquets usuels : `monitoring-plugins` (Debian, Ubuntu), `nagios-plugins-all` (EPEL) ; plugins Centreon : dépôt libre `centreon-plugins` (Perl, avec `libjson-xs-perl`).

## Réception syslog

L'agent peut recevoir les journaux de vos équipements (pare-feux, switchs, routeurs, NAS, imprimantes) et de vos serveurs. **Coupée par défaut.**

```yaml
syslog:
  enabled: true
  udp: 0.0.0.0:514                 # vide = pas d'UDP
  tcp: 0.0.0.0:514                 # vide = pas de TCP
  allowed_sources: [192.0.2.0/24]  # vide = toutes les sources
  max_connections: 64
  rate_limit: 2000                 # lignes par seconde, au-delà jetées et comptées
  parsers:
    - {match: 192.0.2.10, parser: kv}
  tags: {site: siege}
```

- Formats : RFC 3164 (BSD) et RFC 5424, horodatage ISO 8601 de rsyslog ; en TCP, trames à longueur (octet-counting, RFC 6587) ou terminées par une fin de ligne. Une ligne qu'aucun format ne reconnaît garde tout son texte.
- L'agent n'ouvre aucun pare-feu : autorisez le port vous-même, et seulement depuis vos équipements. Le port 80 est refusé.
- Chaque ligne devient un journal HYPHA : source `syslog`, service = nom de l'application (sinon `syslog`), statut tiré de la sévérité (0 `emergency` … 7 `debug`), étiquette `syslog_host` = nom d'hôte de l'en-tête, sinon adresse de l'émetteur. Attributs : `syslog.facility`, `syslog.severity`, `syslog.hostname`, `syslog.appname`, `syslog.procid`, `syslog.msgid`, `syslog.timestamp`, `syslog.structured_data`, `network.client.ip`. L'heure retenue est celle de la réception (l'horloge d'un équipement peut être fausse).
- Modèles d'analyse : `parsers` désigne, par adresse, réseau ou nom d'hôte, le modèle que le serveur applique à l'arrivée (attribut `syslog.parser`). Livré : `kv`, qui découpe les paires `clé=valeur` (`action=deny srcip=203.0.113.9` devient `@action`, `@srcip`). Les découpages ordinaires (sshd, sudo, accès HTTP) s'appliquent aussi.
- Mesures : `syslog.messages` par `syslog_host` (0 pour un émetteur vu dans les 24 heures qui se tait), `syslog.dropped` par motif (`débit`, `file pleine`, `source refusée`, `connexions`, `trame trop longue`), `syslog.connections` ; contrôle `syslog.can_receive`.

Les journaux reçus se cherchent comme les autres : `source:syslog @syslog.hostname:fw01`, `source:syslog status:error`.

## Modèles de moniteurs et tableaux

Famille **Collecte générique** du catalogue de moniteurs : cible OpenMetrics injoignable, plafond de séries atteint, plugin Nagios en alerte, réception syslog en échec, équipement syslog muet, lignes syslog jetées, erreurs graves reçues par syslog.

Le [catalogue de tableaux](/docs/metriques/#catalogue-de-tableaux-prets) propose « Cibles OpenMetrics et Prometheus », « node_exporter (Prometheus) », « Serveur Prometheus », « Plugins Nagios et Centreon » et « Journaux reçus par syslog ».
