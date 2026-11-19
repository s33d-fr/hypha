---
id: downtimes
path: /docs/maintenances/
title: Maintenances planifiées
nav: Maintenances
section: Alerter et agir
order: 42
updated: 2026-11-19
description: Mettre des moniteurs, des hôtes ou des étiquettes en maintenance dans HYPHA, une fois ou de façon récurrente, sans perdre l'historique.
lead: Pendant une maintenance, HYPHA continue d'évaluer et d'enregistrer, mais ne notifie personne.
---
## Ce que fait une maintenance

- Les moniteurs couverts sont évalués et leurs états enregistrés ; aucune notification ne part.
- Les groupes concernés portent la marque « maintenance » dans la vue Problèmes.
- À la fin, un groupe toujours hors OK notifie normalement, une fois.
- Un événement marque le début et la fin.
- Sur une page de statut, un composant en maintenance l'affiche.

## Portée

- **Moniteurs** : une liste, ou tous.
- **Étiquettes** : le groupe ou l'hôte doit porter toutes les étiquettes données (`env:preprod`, `role:db`).
- **Groupes** : des groupes précis (`host:web-02`).

## Durée et récurrence

- Une fois : maintenant pour une durée (1 h, 4 h, 1 jour, ou au choix), ou entre deux dates.
- Chaque jour, chaque semaine (jours au choix) ou chaque mois (jour du mois), à une heure donnée, pour une durée, dans le fuseau choisi, jusqu'à une date de fin facultative.

## Où la poser

- Page **Maintenances** : liste (actives en tête), création avec l'aperçu des moniteurs touchés.
- Fiche d'un hôte : **Mettre en maintenance** (1 h, 4 h, 1 jour, au choix).
- Page d'un moniteur, et ligne de la vue Problèmes.
- Par l'API : `POST /api/v1/downtimes` avec `{"duration_s": 3600, …}` commence tout de suite (voir [API](/docs/api/)).
