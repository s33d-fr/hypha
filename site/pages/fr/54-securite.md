---
id: security
path: /docs/securite/
title: Sécurité
nav: Sécurité
section: Administrer
order: 54
updated: 2026-11-19
description: Les mesures de sécurité de HYPHA : agent sans port ouvert, mises à jour signées, connecteurs en lecture seule, réparations en liste blanche, clés hachées, masquage, audit, hébergement en France.
lead: Ce que HYPHA fait pour limiter ce qu'un agent, une clé ou un compte peut faire, et où vont vos données.
---
## L'agent

- **Aucun port ouvert** sur l'hôte : l'agent envoie ses données en HTTPS et interroge lui-même le serveur pour ses consignes. StatsD et le relais OTLP n'écoutent que sur la boucle locale ; une autre adresse déclenche un avertissement.
- **Certificat du serveur vérifié** ; le couper (`tls_skip_verify`) déclenche un avertissement.
- **Droits** : sous Linux, l'agent tourne en root pour lire tous les journaux et `/proc`. L'unité systemd le durcit : `/usr`, `/boot` et `/etc` en lecture seule pour lui, `/home` en lecture seule, `/tmp` privé, aucune élévation de droits, écriture sous `/var/lib/hypha` seulement, plafonds de processeur et de mémoire. Sous Windows, il tourne en service LocalSystem.
- **Configuration** : `hypha.yaml` en 600. Les secrets (SNMP, bases, clouds) restent dans la configuration de l'agent et ne partent jamais vers le serveur ; `check-config` les affiche masqués.

## Mises à jour signées

Chaque binaire publié est listé dans `SHA256SUMS`, signé en **ed25519**. L'agent vérifie la signature avec la clé publique intégrée à sa construction, puis la somme du binaire, puis le lance pour contrôler sa version et la configuration, avant de le poser. Une signature absente ou fausse arrête la mise à jour. Voir [Flotte et mises à jour](/docs/flotte/).

L'installation par script vérifie la somme SHA-256 du binaire téléchargé.

## Connecteurs en lecture seule

Chaque connecteur demande le plus petit droit de lecture que propose le fournisseur :

| Connecteur | Droit demandé |
|---|---|
| AWS | `ec2:DescribeInstances`, `ec2:DescribeInstanceStatus`, `ec2:DescribeVolumes`, `cloudwatch:DescribeAlarms`, `sts:GetCallerIdentity` |
| Azure | rôle Lecteur sur l'abonnement |
| Google Cloud | `roles/compute.viewer` |
| OVHcloud | clé « consumer » limitée aux `GET` |
| Scaleway | `InstancesReadOnly`, `ElasticMetalReadOnly` |
| vSphere | rôle ReadOnly |
| Proxmox VE | jeton au rôle PVEAuditor |
| Kubernetes | `get`, `list`, `watch` sur nœuds, pods, événements, charges de travail ; ni secrets, ni journaux, ni `exec` |
| Bases de données | `pg_monitor`, `PROCESS` et `REPLICATION CLIENT`, `clusterMonitor`, rôle `monitoring` de RabbitMQ ; sessions en lecture seule |
| SNMP | compte v3 en lecture seule, `authPriv` conseillé |

## Réparations en liste blanche

Les actions à distance sont coupées par défaut. Activées, elles ne passent que des gestes nommés, sur les services permis par la configuration de l'hôte, avec une liste d'interdits (SSH, l'agent lui-même, systemd). Pas de commande libre ; un script se désigne par son nom, son chemin vient de l'hôte. Une action doit être approuvée par un administrateur et expire si personne ne la prend. Voir [Explication et réparation](/docs/explication-et-reparation/).

## Comptes, clés et traces

- Quatre rôles (lecture, membre, administrateur, propriétaire).
- Clés de 40 caractères aléatoires ; HYPHA n'en garde que l'empreinte SHA-256 et ne les montre qu'une fois. Une clé d'ingestion n'ouvre pas l'API de l'interface.
- Les secrets des canaux de notification ne sont jamais réaffichés.
- Le [journal d'audit](/docs/comptes-et-roles/#journal-d-audit) garde 13 mois chaque écriture, son auteur et son adresse IP.

## Données

- Service hébergé : sur l'infrastructure en propre de S33D, en France. Chiffrement en transit (HTTPS).
- Les explications et post-mortem sont rédigés par le modèle de S33D, sur ses propres serveurs : rien ne part chez un fournisseur d'IA.
- Le [masquage](/docs/journaux/#masquage-des-donnees-sensibles) retire les secrets des journaux avant leur écriture, d'office pour la règle `secrets`.
- Les tests synthétiques et les webhooks lancés depuis S33D ne visent que des adresses publiques : le réseau interne de S33D leur est fermé.
- Les pages de statut publiques et les tableaux partagés n'exposent que ce que vous avez choisi.
- Auto-hébergement : rien ne quitte votre serveur.

Le traitement des données personnelles est décrit dans la [politique de confidentialité](/confidentialite/).

## Signaler une faille

Écrivez à [contact@s33d.fr](mailto:contact@s33d.fr) avec les étapes pour la reproduire. Merci de nous laisser le temps de corriger avant toute publication.
