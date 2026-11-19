---
id: configuration
path: /docs/configuration/
title: Référence de configuration de l'agent
nav: Configuration de l'agent
section: Installer l'agent
order: 14
updated: 2026-11-19
description: Toutes les clés de hypha.yaml et de conf.d : clé, serveur, étiquettes, journaux, processus, réseau, contrôles, intégrations, SNMP, actions, mises à jour.
lead: Un fichier principal, des compléments dans conf.d, et une commande qui vérifie le tout avant de redémarrer.
---
## Où se trouve la configuration

| Système | Fichier principal | Compléments |
|---|---|---|
| Linux | `/etc/hypha/hypha.yaml` (600) | `/etc/hypha/conf.d/*.yaml` |
| Windows | `C:\ProgramData\Hypha\hypha.yaml` | `C:\ProgramData\Hypha\conf.d\*.yaml` |
| Docker | `/etc/hypha/hypha.yaml` dans le conteneur | `/etc/hypha/conf.d/` monté |

Règles de lecture :

- Les fichiers `conf.d/*.yaml` sont lus après le fichier principal, dans l'ordre alphabétique, et fusionnés clé par clé : un objet se fusionne, une liste remplace la liste, une clé absente garde sa valeur.
- Les variables `HYPHA_API_KEY` et `HYPHA_URL` l'emportent sur les fichiers.
- Un fichier illisible ou invalide arrête l'agent : jamais de configuration à moitié lue. Une clé inconnue n'est qu'un avertissement.

Avant chaque redémarrage :

```bash
sudo hypha-agent check-config
```

La commande affiche la configuration fusionnée, secrets masqués, et nomme le fichier d'origine de chaque erreur.

## Clés générales

| Clé | Défaut | Rôle |
|---|---|---|
| `api_key` | (obligatoire) | clé d'ingestion, `hph_` suivi de 40 caractères |
| `url` | `https://hypha.s33d.fr` | serveur de collecte ; votre adresse en auto-hébergement |
| `hostname` | nom système | nom de l'hôte dans HYPHA |
| `tags` | `{}` | étiquettes ajoutées à tout ce que l'agent envoie ; clés `a-z 0-9 _ . - /` |
| `interval` | `15s` | cadence des mesures et des contrôles (1 s au moins) |
| `tls_skip_verify` | `false` | ne coupez la vérification du certificat que pour un serveur auto-hébergé à certificat interne |
| `proxy` | (vide) | mandataire HTTP ; vide = variables `HTTPS_PROXY` et `NO_PROXY` |
| `buffer.dir`, `buffer.max_mb` | `/var/lib/hypha/buffer`, `256` | tampon disque des envois en échec ; les plus anciens partent au-delà |

## Journaux

```yaml
logs:
  enabled: true
  journald: true                       # Linux : journal systemd
  windows_eventlog: [System, Application]
  files:
    - {path: /var/log/nginx/*.log, service: nginx, source: nginx}
    - {path: /srv/app/logs/*.json, service: boutique}
  exclude_patterns: ["GET /health"]    # expressions régulières, lignes jamais envoyées
```

Une ligne JSON est décodée : `message` ou `msg` devient le message, `level` ou `severity` le statut, le reste part en attributs. Un message est tronqué à 32 Kio. Pour réduire le volume côté serveur, voir aussi les [filtres d'exclusion](/docs/journaux/#filtres-d-exclusion-et-echantillonnage).

## Processus, réseau, conteneurs

```yaml
processes:
  enabled: true
  include: ["^java", "gunicorn"]   # toujours relevés, en plus des 30 premiers par CPU et par mémoire
network:
  enabled: true
  # exclude_interfaces: [lo, "docker*", "veth*", "br-*", "virbr*", "cali*", "flannel*", "cni*"]
containers:
  enabled: auto                     # auto = si /var/run/docker.sock existe
```

## Contrôles

```yaml
checks:
  systemd_units: [nginx.service]    # suivies ; une unité en échec est TOUJOURS signalée
  windows_services: [W3SVC]         # suivis ; un service automatique arrêté est signalé
  windows_services_exclude: ["^Veeam"]
  windows_auto_services: auto       # auto, always, never
  http:
    - {name: site, url: "https://exemple.fr/", expect_status: 200, timeout: 5s}
  tcp:
    - {name: base, address: "192.0.2.5:5432", timeout: 3s}
  processes:
    - {name: nginx}
    - {name: gunicorn, match: contains, min: 2}
    - {name: "^php-fpm[0-9.]*$", match: regex, min: 1, max: 1}
```

Les contrôles HTTP et TCP partent de l'hôte : ils voient les adresses internes. Pour des tests planifiés avec historique et assertions, voir les [tests synthétiques](/docs/synthetiques/).

## Intégrations

Chaque intégration s'active seule quand son logiciel est détecté (`enabled: auto`), ou se force (`true`) et se coupe (`false`). Son état apparaît dans **Réglages › Agents** : ok, absent, coupée, ou l'erreur rencontrée. Le détail de chacune est sur les pages [Intégrations](/docs/integrations/), [Bases de données](/docs/bases-de-donnees/), [SNMP](/docs/snmp/), [Clouds](/docs/cloud/), [Virtualisation](/docs/virtualisation/) et [Kubernetes](/docs/kubernetes/).

### Plusieurs instances

Pour lire plusieurs cibles d'une même intégration (ovhcloud, mysql, redis, memcached, rabbitmq, mongodb, opensearch, kubernetes, libvirt, proxmox, aws, scaleway, azure, gcp), donnez une liste `instances`. Chaque entrée porte un `name` (minuscules, chiffres, `-` et `_`), reprend les valeurs par défaut de l'intégration et s'additionne d'un fichier `conf.d` à l'autre. Ses données portent l'étiquette `instance_name`.

```yaml
integrations:
  redis:
    instances:
      - {name: sessions, address: "127.0.0.1:6379"}
      - {name: file, address: "127.0.0.1:6380", password: "…"}
```

## Métriques applicatives et traces

```yaml
statsd:
  enabled: true
  listen: 127.0.0.1:8125     # format DogStatsD : nom:valeur|g|c|ms|h|s|#etiquette:valeur
otlp:
  enabled: false
  listen: 127.0.0.1:4318     # OTLP/HTTP : l'agent relaie vers HYPHA en ajoutant la clé
```

Une écoute hors de la boucle locale déclenche un avertissement : elle ouvre le port au réseau.

## Canal de contrôle, mises à jour, actions

```yaml
control:
  enabled: true
  interval: 30s            # l'agent interroge le serveur ; aucun port ouvert sur l'hôte
auto_update: false         # suivre la version cible fixée dans Réglages › Agents
actions:
  enabled: false
  allow: [restart_service, start_service, stop_service]   # aussi : run_script, update_agent
  services: ["*"]
  deny_services: ["sshd*", "ssh*", "hypha-agent*", "systemd-*"]
  scripts:
    vider-cache: /etc/hypha/scripts/vider-cache.sh
  timeout: 120s
synthetics:
  enabled: false           # emplacement privé de tests synthétiques
  max_concurrent: 4
inventory:
  enabled: true
  interval: 6h
  packages: true
```

Les actions sont coupées par défaut. Le service de l'agent n'est jamais touché, quelle que soit la liste. Un script nommé doit avoir un chemin absolu, appartenir à root et n'être modifiable ni par le groupe ni par les autres ; il est lancé sans argument et sa sortie est bornée à 64 Kio. Voir [Explication et réparation](/docs/explication-et-reparation/) et [Flotte et mises à jour](/docs/flotte/).

## SNMP

```yaml
snmp:
  enabled: true
  interval: 60s
  devices:
    - {name: switch-baie-1, address: 192.0.2.10, version: 2c, community: lecture, tags: {role: switch}}
  traps: {enabled: false, listen: "0.0.0.0:162", community: public}
```

Le détail des versions, des profils et des réglages SNMPv3 est sur la page [SNMP](/docs/snmp/). Les secrets SNMP restent dans la configuration de l'agent : ils ne partent jamais vers le serveur.
