---
id: accounts
path: /docs/comptes-et-roles/
title: Comptes, rôles, clés et journal d'audit
nav: Comptes et rôles
section: Administrer
order: 50
updated: 2026-11-19
description: Gérer les membres d'un espace HYPHA, leurs rôles, les clés d'ingestion et d'application, et le journal d'audit.
lead: Quatre rôles, deux sortes de clés, et une trace de chaque modification.
---
## Espaces et connexion

- Sur hypha.s33d.fr, on se connecte avec un compte S33D (connexion unique). Un même compte peut appartenir à plusieurs espaces ; le sélecteur en haut de l'écran passe de l'un à l'autre.
- En auto-hébergement, les comptes sont locaux (identifiant et mot de passe), et une connexion OpenID Connect peut s'ajouter.
- Une session dure 7 jours.

## Rôles

| Rôle | Peut |
|---|---|
| Lecture (`viewer`) | tout voir, demander une explication |
| Membre (`member`) | en plus : moniteurs, tableaux, maintenances, incidents, vues, tests synthétiques, SLO, rapports, proposer une action |
| Administrateur (`admin`) | en plus : canaux, clés, membres, masquage des journaux, flotte, approbation des actions, pages de statut, partage public des tableaux |
| Propriétaire (`owner`) | tout ; seul un propriétaire nomme un autre propriétaire |

## Inviter

**Réglages › Membres** : une adresse e-mail et un rôle. L'invitation devient active quand la personne se connecte avec cette adresse. Un administrateur change un rôle ou retire un membre à tout moment.

## Clés

**Réglages › Clés API** :

| Clé | Sert à | En-tête |
|---|---|---|
| Ingestion | envoyer des données : agents, OpenTelemetry | `X-Hypha-Api-Key` (ou `Authorization: Bearer` pour OTLP) |
| Application | appeler l'API de l'interface depuis un script | `X-Hypha-App-Key` |

- Une clé commence par `hph_`, suivi de 40 caractères. Elle ne s'affiche qu'une fois, à sa création : HYPHA n'en garde que l'empreinte.
- Une clé d'application agit avec le rôle de la personne qui l'a créée.
- Une clé d'ingestion n'ouvre pas l'API de l'interface.
- Une clé se révoque d'un clic ; les agents qui l'utilisent sont refusés aussitôt. Pour la remplacer, créez la nouvelle, posez-la (`HYPHA_FORCE_CONF=1` à la réinstallation), puis révoquez l'ancienne.

## Journal d'audit

**Réglages › Audit** (administrateur) : chaque écriture faite par l'interface ou l'API y entre : création, modification, suppression, acquittement, maintenance, approbation ou rejet d'une action, envoi d'essai, rotation de clé, connexion, déconnexion. Pour chacune : date, auteur, objet, résumé, état avant et après, adresse IP. Filtres par période, auteur, type d'objet, action et texte.

Les entrées sont conservées 13 mois.

## Consommation

**Réglages › Consommation** montre, jour par jour, les lignes de journal et leur volume, les points de métriques, les spans, les événements RUM et les contrôles reçus, ainsi que le nombre d'hôtes, d'équipements et de séries, et les services les plus bavards. Voir aussi [Formules et facturation](/docs/formules-et-facturation/).

## Profil

**Réglages › Profil** : votre compte, votre espace, son hébergement, et vos préférences de langue (français ou anglais) et de thème (sombre ou clair), mémorisées dans le navigateur.
