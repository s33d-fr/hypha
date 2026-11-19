---
id: incidents
path: /docs/incidents/
title: Incidents et post-mortem
nav: Incidents
section: Alerter et agir
order: 43
updated: 2026-11-19
description: Déclarer et suivre un incident dans HYPHA, tenir sa chronologie, informer le public, puis faire rédiger le post-mortem par l'IA de S33D.
lead: Déclarer, suivre, résoudre, tirer la leçon. La chronologie se remplit seule et l'IA rédige le premier jet du post-mortem.
---
## Déclarer un incident

- Depuis la vue Problèmes : **Ouvrir un incident** sur une alerte. Le titre, la sévérité (tirée de la priorité) et le lien vers le moniteur sont remplis, la chronologie commence avec l'historique du groupe.
- Depuis la page **Incidents** : titre, sévérité de 1 à 5, commandant, impact pour les clients.

Chaque incident reçoit un numéro dans l'espace.

## Suivre

- **Statut** et **sévérité** modifiables ; chaque changement entre dans la chronologie.
- **Chronologie** : notes de l'équipe, mises à jour publiques, et, tant que l'incident est ouvert, chaque changement d'état des moniteurs liés.
- **Liens** vers des moniteurs, des hôtes, des services.
- **Graphiques** des moniteurs liés sur la durée de l'incident.
- **Notifications** : la création et les changements de statut partent vers les canaux d'incident de l'espace.

## Informer le public

Liez l'incident à une ou plusieurs [pages de statut](/docs/pages-de-statut/) : son titre, son statut et ses mises à jour publiques y apparaissent, sans aucune donnée interne.

## Le post-mortem

Onglet **Post-mortem**, bouton **Rédiger avec l'IA**. HYPHA rassemble la chronologie, les alertes liées et les journaux d'erreur des hôtes concernés pendant l'incident, puis le modèle de S33D rédige en Markdown :

- résumé, impact, chronologie ;
- cause racine ;
- ce qui a marché, ce qui n'a pas marché ;
- actions, en tableau : action, responsable, échéance.

Le texte reste modifiable dans l'éditeur. La rédaction est notée dans la chronologie. Le nombre de rédactions par l'IA dépend de la formule : voir les [tarifs](/tarifs/).
