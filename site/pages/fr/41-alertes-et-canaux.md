---
id: alerts
path: /docs/alertes-et-canaux/
title: Alertes, vue Problèmes et canaux de notification
nav: Alertes et canaux
section: Alerter et agir
order: 41
updated: 2026-11-20
description: La vue Problèmes de HYPHA, l'acquittement, les priorités, les escalades, et les douze canaux de notification : e-mail, notifications push, webhook, Slack, Teams, Telegram, Discord, Google Chat, Mattermost, ntfy, PagerDuty, Opsgenie.
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

Chaque envoi a 10 secondes pour aboutir, et deux nouvelles tentatives. Les secrets d'un canal (jetons, clés, en-têtes) ne sont jamais réaffichés.

Les notifications par SMS, prévues dans les formules Pro et Entreprise (0,10 € HT l'unité), ne sont pas encore ouvertes.

## Escalades

Dans les options d'un moniteur : si un groupe reste hors OK et non acquitté après un délai (par exemple 15 minutes), HYPHA prévient d'autres canaux, avec leur propre message. Plusieurs marches se suivent.

## Rapports programmés

**Réglages › Rapports** envoie par e-mail, à l'échéance choisie :

- un **résumé** : disponibilité des hôtes, alertes par priorité, moniteurs les plus bruyants, durée des problèmes, SLO, incidents, hôtes apparus ou disparus, mises à jour de sécurité en attente, certificats à renouveler sous 30 jours ;
- l'**état des SLO** ;
- un **tableau de bord** rendu en valeurs et petites courbes.

Chaque rapport a un aperçu et un bouton d'envoi immédiat.
