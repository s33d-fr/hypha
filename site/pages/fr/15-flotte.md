---
id: fleet
path: /docs/flotte/
title: Flotte d'agents et mises à jour signées
nav: Flotte et mises à jour
section: Installer l'agent
order: 15
updated: 2026-11-19
description: Suivre la version et l'état de chaque agent, mettre toute une flotte à jour depuis l'interface, avec des binaires signés et vérifiés.
lead: Une version cible choisie dans l'interface, et chaque agent se met à jour seul, après avoir vérifié la signature du binaire.
---
## La page Agents

**Réglages › Agents** liste chaque agent : hôte, version (colorée si elle retarde), système, dernier contact, état de chaque collecteur et intégration, capacités (actions, tests synthétiques privés, mise à jour automatique, équipements SNMP) et dernière erreur.

## Le canal de contrôle

L'agent interroge le serveur toutes les 30 secondes (`control.interval`). Il y lit la version cible, les actions approuvées et les tests synthétiques qui lui reviennent. Le serveur ne se connecte jamais à l'agent : aucun port n'est ouvert sur vos machines.

## Mettre à jour une flotte

1. Posez `auto_update: true` dans la configuration des agents concernés.
2. Dans **Réglages › Agents**, sélectionnez un agent, plusieurs, ou toute la flotte d'un système, puis choisissez la version cible.
3. Suivez l'état en direct : téléchargement, vérification, installation, puis la nouvelle version au contact suivant.

À chaque mise à jour, l'agent :

1. télécharge `SHA256SUMS` et sa signature `SHA256SUMS.sig` depuis `<url>/install/` ;
2. vérifie la signature **ed25519** avec la clé publique intégrée à l'agent lors de sa construction ;
3. télécharge le binaire de sa plateforme et contrôle sa somme SHA-256 ;
4. lance le nouveau binaire pour vérifier sa version et la configuration en place ;
5. le pose, puis redémarre : sous Linux par systemd, sous Windows par un processus détaché.

Une signature absente ou fausse arrête tout : l'agent garde son binaire et signale l'échec. Un échec est retenté une heure plus tard, trois fois au plus. L'agent ne revient jamais à une version plus ancienne, sauf si vous la choisissez explicitement.

Sous Linux, `/usr` reste en lecture seule pour l'agent : il prépare le binaire vérifié sous `/var/lib/hypha`, et c'est le démarrage suivant de l'unité qui revérifie la signature et le pose.

En conteneur, l'agent ne se met jamais à jour lui-même : changez la version de l'image.

## Auto-hébergement

Le kit d'auto-hébergement sert les binaires signés et leur signature sous `/install/` de votre serveur : vos agents se mettent à jour depuis chez vous, sans joindre S33D.
