---
id: getting-started
path: /docs/demarrage/
title: Démarrer avec HYPHA
nav: Démarrer
section: Démarrer
order: 2
updated: 2026-11-19
description: Créer un compte HYPHA, installer le premier agent et recevoir la première alerte, en dix minutes.
lead: Un compte, un agent, et vos premières données arrivent dans la minute. Comptez dix minutes.
---
## 1. Créer un compte

1. Ouvrez [hypha.s33d.fr](https://hypha.s33d.fr) et choisissez **Se connecter**.
2. Connectez-vous avec votre compte S33D, ou créez-en un : adresse e-mail, ou compte Google, Microsoft, Apple, Facebook ou GitHub.
3. À la première connexion, HYPHA crée votre **espace** en formule Découverte, gratuite et sans carte bancaire.

L'espace neuf reçoit d'office :

- un canal de notification par e-mail vers l'adresse de votre compte ;
- six moniteurs reliés à ce canal : hôte sans nouvelles, disque presque plein, mémoire saturée, unité systemd en échec, service Windows arrêté, certificat proche de la fin.

Vous êtes donc prévenu d'une panne courante sans rien régler.

> Vos données doivent rester chez vous ? Installez le même logiciel sur votre serveur : voir [Auto-hébergement](/docs/auto-hebergement/).

## 2. Installer l'agent

Ouvrez **Réglages › Installer l'agent**. La page donne la commande de votre système et peut créer la clé d'ingestion pour vous (bouton **Créer une clé et l'insérer**).

Sous Linux :

```bash
curl -fsSL https://hypha.s33d.fr/install/install.sh | sudo HYPHA_API_KEY=hph_votre_cle bash
```

Sous Windows, dans PowerShell lancé en administrateur :

```powershell
$env:HYPHA_API_KEY='hph_votre_cle'; iwr -useb https://hypha.s33d.fr/install/install.ps1 | iex
```

Le détail par système : [Linux](/docs/agent-linux/), [Windows](/docs/agent-windows/), [Docker](/docs/agent-docker/), [Kubernetes](/docs/kubernetes/).

## 3. Voir ses données

Dans la minute qui suit, l'hôte apparaît dans **Infrastructure**. La vue d'ensemble affiche ses mesures, la fiche de l'hôte ses processus, ses journaux, ses contrôles et son inventaire.

Tant qu'aucun agent n'est relié, la vue d'ensemble affiche trois étapes pour vous guider.

## 4. Recevoir une alerte

Les six moniteurs posés d'office surveillent déjà l'hôte. Pour aller plus loin :

- **Moniteurs › Nouveau moniteur › Depuis un modèle** propose plus de 90 modèles : processeur, mémoire, disque plein dans 48 h, certificats, bases de données, clouds, Kubernetes ;
- **Réglages › Canaux de notification** ajoute Slack, Teams, Telegram, Discord, PagerDuty, un webhook et d'autres canaux ;
- le bouton **Envoyer un essai** de chaque canal vérifie qu'il arrive à destination.

## 5. Inviter l'équipe

**Réglages › Membres** invite une personne par son adresse e-mail, avec un rôle : lecture, membre, administrateur ou propriétaire. L'invitation devient active à sa première connexion. Voir [Comptes et rôles](/docs/comptes-et-roles/).

## Et ensuite

- Brancher les logiciels de l'hôte : [Intégrations](/docs/integrations/) et [Bases de données](/docs/bases-de-donnees/).
- Suivre les équipements réseau : [SNMP](/docs/snmp/).
- Lire un compte AWS, Azure, Google Cloud, OVHcloud ou Scaleway : [Clouds](/docs/cloud/).
- Comprendre une panne et la réparer : [Explication et réparation](/docs/explication-et-reparation/).
