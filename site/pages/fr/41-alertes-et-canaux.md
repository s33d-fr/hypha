---
id: alerts
path: /docs/alertes-et-canaux/
title: Alertes, vue Problèmes et canaux de notification
nav: Alertes et canaux
section: Alerter et agir
order: 41
updated: 2026-11-21
description: La vue Problèmes de HYPHA, l'acquittement, les priorités, les escalades, et les treize canaux de notification : e-mail, notifications push, webhook, Slack, Teams, Telegram, Discord, Google Chat, Mattermost, ntfy, PagerDuty, Opsgenie, GLPI.
lead: Une seule liste de ce qui ne va pas, triée par priorité, et des notifications qui arrivent là où l'équipe travaille.
---
## La vue Problèmes

En tête de la barre latérale, **Problèmes** liste tous les groupes de moniteurs hors OK de l'espace, triés par priorité puis par ancienneté. Un compteur des problèmes non acquittés reste visible dans la barre latérale.

Pour chaque ligne : priorité, état, moniteur, groupe, hôte, valeur, durée qui court, prise en charge, dernière notification. Les actions :

- **Acquitter**, avec un commentaire : les relances et les escalades s'arrêtent ; un retour à OK efface l'acquittement. La sélection multiple acquitte en lot.
- **Expliquer** : l'IA de S33D décrit la cause probable (voir [Explication et réparation](/docs/explication-et-reparation/)).
- **Mettre en maintenance** (voir [Maintenances](/docs/maintenances/)).
- **Ouvrir un incident**, prérempli avec le moniteur, le groupe et leur historique (voir [Incidents](/docs/incidents/)).

Filtres : priorité, état, acquittés ou non, mis en attente par une maintenance ou une dépendance, texte libre.

## Priorités

| Priorité | Couleur | Usage conseillé |
|---|---|---|
| P1 | rouge | service arrêté pour les clients |
| P2 | orange | panne qui s'aggrave ou touche une fonction |
| P3 | jaune | dégradation à traiter dans la journée |
| P4 | bleu | à planifier |
| P5 | gris | information |

## Canaux de notification

**Réglages › Canaux de notification** (rôle administrateur). Chaque canal a un bouton **Envoyer un essai**.

| Canal | Réglage |
|---|---|
| E-mail | une ou plusieurs adresses ; message en HTML sobre avec l'état, la valeur, le seuil, le lien vers le moniteur et vers l'explication, et les dernières lignes de journal de l'hôte |
| Notifications push | tous les membres de l'espace ou des membres choisis ; notification sur les téléphones et ordinateurs qu'ils ont activés, avec le lien vers le problème et son explication, et un bouton **Acquitter** (voir [Application mobile](/docs/application-mobile/)) |
| Webhook | adresse et en-têtes ; corps JSON `{monitor, group, state, value, message, url}` |
| Slack, Microsoft Teams, Google Chat, Discord, Mattermost | adresse du webhook entrant |
| Telegram | jeton du robot et identifiant de la conversation |
| ntfy | serveur, sujet, jeton facultatif |
| PagerDuty | clé de routage (Events API v2) : déclenchement puis résolution |
| Opsgenie | clé d'API et région (UE ou US) : création puis fermeture |
| GLPI | adresse de l'API REST, jeton de l'application et jeton d'API d'un utilisateur : ticket ouvert à l'alerte, résolu au retour à la normale (voir ci-dessous) |

Chaque envoi a 10 secondes pour aboutir, et deux nouvelles tentatives. Les secrets d'un canal (jetons, clés, en-têtes) ne sont jamais réaffichés.

Les notifications par SMS, prévues dans les formules Pro et Entreprise (0,10 € HT l'unité), ne sont pas encore ouvertes.

### Canal GLPI

Une alerte ouvre un ticket dans [GLPI](https://glpi-project.org) par son API REST (`apirest.php`) ; le retour à la normale le résout.

- **Alerte** (ALERT, WARN, sans données) : ticket ouvert, au titre du moniteur et du groupe suivi d'une référence stable (`[HYPHA-1a2b3c4d5e]`) ; l'urgence suit la priorité du moniteur (P1 très haute à P5 très basse). Tant que ce ticket n'est pas résolu, les relances, escalades et aggravations y ajoutent un **suivi** au lieu d'ouvrir un autre ticket.
- **Retour à la normale** : un suivi « rétabli », puis une **solution** : le ticket passe « Résolu » (la clôture reste à GLPI et à sa règle d'approbation).
- **Envoyer un essai** : un ticket ouvert puis résolu aussitôt.

Réglages : l'adresse (`https://votre-glpi/apirest.php`, ou l'adresse du GLPI, complétée d'elle-même), le **jeton de l'application** (Configuration › Générale › API : activer l'API REST, puis un client d'API avec son jeton) et le **jeton d'API d'un utilisateur** (Préférences › Clés d'accès distant), de préférence un compte de service au profil Technicien (droit de créer des tickets, d'y ajouter suivis et solutions). Facultatifs : type (incident ou demande), entité, catégorie ITIL, source de la demande (identifiants numériques de GLPI). La connexion à l'API se fait par jeton seulement : ni identifiant ni mot de passe. Une session est ouverte puis fermée à chaque envoi. Un GLPI sur un réseau privé n'est joint que si l'espace autorise les cibles privées (auto-hébergement).

Éprouvé contre GLPI 12.0.2 (image officielle, MariaDB 11.4) : ticket ouvert à l'urgence « très haute » pour un moniteur P1, suivi ajouté par la relance, suivi et solution à la reprise (statut « Résolu »), essai de canal, jeton refusé (erreur `ERROR_GLPI_LOGIN_USER_TOKEN` rendue telle quelle).

## Escalades

Dans les options d'un moniteur : si un groupe reste hors OK et non acquitté après un délai (par exemple 15 minutes), HYPHA prévient d'autres canaux, avec leur propre message. Plusieurs marches se suivent.

## Rapports programmés

**Réglages › Rapports** envoie par e-mail, à l'échéance choisie :

- un **résumé** : disponibilité des hôtes, alertes par priorité, moniteurs les plus bruyants, durée des problèmes, SLO, incidents, hôtes apparus ou disparus, mises à jour de sécurité en attente, certificats à renouveler sous 30 jours ;
- l'**état des SLO** ;
- un **tableau de bord** rendu en valeurs et petites courbes.

Chaque rapport a un aperçu et un bouton d'envoi immédiat.
