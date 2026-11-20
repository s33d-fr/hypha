---
id: monitors
path: /docs/moniteurs/
title: Moniteurs et modèles
nav: Moniteurs
section: Alerter et agir
order: 40
updated: 2026-11-20
description: Les types de moniteurs HYPHA (métrique, hôte, contrôle, journaux, APM, synthétique, anomalie, prévision, écart, composite, variation, événement, processus, erreurs, SLO), leurs seuils, leurs options et le catalogue de modèles.
lead: Un moniteur évalue des données chaque minute et change d'état. Partez d'un modèle du catalogue, il y en a plus de 90, ou écrivez le vôtre avec l'aperçu en direct.
---
{{video:5-moniteurs}}

## États

| État | Sens |
|---|---|
| OK | sous les seuils |
| Avertissement | seuil `warning` franchi |
| Alerte | seuil `critical` franchi |
| Sans données | plus de données sur la fenêtre (`no_data_s`) |

Un moniteur groupé (`by {host}`) a un état par groupe. Chaque changement d'état est noté, notifie les canaux du moniteur et crée un événement.

## Types

| Type | Ce qu'il évalue |
|---|---|
| `metric` | une requête de métrique sur une fenêtre (moyenne, max, min, somme, dernière valeur) |
| `host` | un hôte dont l'agent n'a rien envoyé depuis le délai fixé |
| `service_check` | le pire état récent d'un contrôle (`systemd.unit`, `windows.service`, `tls.cert.expiry`…) |
| `log` | le nombre de lignes d'une recherche sur une fenêtre, groupé par champ ou par attribut |
| `apm` | taux d'erreur, latence p95 ou débit d'un service |
| `synthetic` | N échecs consécutifs d'un test synthétique |
| `anomaly` | la part des points hors des bornes habituelles (moyenne glissante, mêmes créneaux des 7 derniers jours, ou décomposition saisonnière) |
| `forecast` | le temps avant qu'une courbe franchisse un seuil, par régression robuste (par exemple, disque plein dans 48 h) |
| `outlier` | le groupe qui s'écarte des autres (écart absolu médian) |
| `composite` | une expression logique sur d'autres moniteurs : `12 && (13 \|\| !14)` |
| `change` | la variation d'une requête par rapport à la même fenêtre plus tôt |
| `event` | le nombre d'événements d'une recherche (par exemple, des traps SNMP) |
| `process` | le nombre de processus d'un nom donné sur chaque hôte |
| `error_tracking` | les problèmes nouveaux ou régressés de la page Erreurs |
| `slo` | le budget d'erreur restant ou la vitesse de consommation d'un SLO |

Le formulaire montre l'aperçu en direct : les groupes, leur état, la courbe, et selon le type les bornes de l'anomalie ou la prévision.

## Seuils et options

- `critical` et `warning`, avec leur seuil de retour facultatif (`critical_recovery`, `warning_recovery`) pour éviter le va-et-vient autour d'une valeur.
- **Priorité** de P1 (critique) à P5 (information).
- **Évaluations consécutives** avant d'alerter, et avant de revenir à OK.
- **Délai d'évaluation** pour les données qui arrivent en retard.
- **Dépendance à l'hôte** (active par défaut) : quand un hôte ne répond plus, les autres alertes de cet hôte sont mises en attente et ne notifient pas. On peut aussi dépendre d'autres moniteurs.
- **Battement** : au-delà de 5 changements en 30 minutes, une seule notification « instable », puis silence jusqu'au retour au calme.
- **Relance** des notifications tant que l'alerte dure, et **escalades** vers d'autres canaux après un délai sans acquittement.
- **Sourdine** jusqu'à une date, ou sans fin.

## Le message

Le message de notification accepte des variables et des blocs conditionnels :

```text
{{#is_alert}}{{name}} sur {{host}} : {{value}} (seuil {{threshold}}).{{/is_alert}}
{{#is_warning}}{{host}} approche du seuil : {{value}}.{{/is_warning}}
{{#is_recovery}}{{host}} : retour à la normale après {{duration}}.{{/is_recovery}}
```

Variables : `{{name}}`, `{{group}}`, `{{host}}`, `{{value}}`, `{{last_value}}`, `{{state}}`, `{{priority}}`, `{{threshold}}`, `{{duration}}`, `{{url}}`, `{{tags.<clé>}}`, et `{{eta}}` pour une prévision. Blocs : `is_alert`, `is_warning`, `is_recovery`, `is_nodata`. La mention `@canal-<id>` ajoute un canal pour ce message.

## Le catalogue de modèles

**Moniteurs › Nouveau moniteur › Depuis un modèle** : plus de 180 modèles par famille, chacun avec ses variables (étiquettes visées, seuils, service).

| Famille | Exemples |
|---|---|
| Système | processeur ou mémoire saturés, attente disque, disque presque plein, disque plein dans 48 h, inodes, échange, charge anormale, hôte sans nouvelles, redémarrage requis |
| Services | unité systemd en échec, service Windows arrêté, programme Supervisor en échec |
| Sécurité | certificat proche de la fin, mises à jour de sécurité en attente, attaque SSH par force brute, SSH injoignable, démon Wazuh arrêté, agents Wazuh déconnectés, Suricata silencieux, paquets perdus par Suricata, alertes Suricata de gravité 1 |
| Applications | erreurs HTTP 5xx, latence p95, nouvelle erreur applicative |
| Bases de données, Files de messages, Recherche | PostgreSQL, MySQL, Redis, Memcached, MongoDB, RabbitMQ, OpenSearch, PgBouncer, ProxySQL, ClickHouse, CouchDB, Couchbase |
| Web | PHP-FPM (file d'attente, plafond de processus), Varnish (serveur d'origine malade, connexions abandonnées), Squid, Lighttpd, Gunicorn |
| Stockage | ZooKeeper, santé de la grappe Ceph, pool Ceph presque plein, Btrfs dégradé ou sans espace non alloué, montage NFS figé, serveur NFS arrêté, retransmissions NFS |
| Annuaire et identité, DNS | OpenLDAP, Keycloak, PowerDNS et son Recursor (version vulnérable, SERVFAIL) |
| Dépôts et registres, Journaux | Nexus (lecture seule, santé), Harbor (composant en défaut, disque), Fluentd (tampon plein, sortie bloquée) |
| Réseau | interface en panne, équipement injoignable |
| Messagerie | file d'attente Postfix |
| Synthétiques | synthétique en échec |
| Kubernetes, Cloud, Virtualisation | grappes, nœuds, AWS, Azure, Google Cloud, OVHcloud, Scaleway, vSphere, Proxmox, Hyper-V, KVM |
| Collecte générique | cible OpenMetrics injoignable, plafond de séries atteint, plugin Nagios en alerte, réception syslog en échec, équipement syslog muet, lignes syslog jetées, erreurs graves reçues par syslog |
| IA et GPU | Ollama, vLLM, LiteLLM, TGI, llama.cpp : injoignable, file d'attente, latence ; GPU NVIDIA trop chaud, saturé, mémoire pleine, en défaut |

Un espace neuf reçoit six moniteurs du catalogue, reliés à un canal e-mail vers l'adresse du compte : hôte sans nouvelles, disque presque plein, mémoire saturée, unité systemd en échec, service Windows arrêté, certificat proche de la fin.
