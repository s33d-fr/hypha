---
id: status-pages
path: /docs/pages-de-statut/
title: Pages de statut publiques
nav: Pages de statut
section: Alerter et agir
order: 45
updated: 2026-11-19
description: Publier une page de statut avec HYPHA : composants tirés des moniteurs, des tests synthétiques et des SLO, disponibilité sur 90 jours, incidents et maintenances.
lead: Une page publique qui se tient à jour toute seule, sans jamais montrer un nom de machine ni une valeur interne.
---
{{video:6-tableaux-et-statut}}

## Créer une page

**Réglages › Pages de statut** (rôle administrateur) :

1. un titre, une description et une adresse courte (`slug`) ;
2. des composants, choisis parmi les moniteurs, les tests synthétiques, les SLO et les hôtes, rangés en groupes ;
3. la publication.

La page vit à l'adresse `https://hypha.s33d.fr/status/?p=<slug>`. Elle suit le thème clair ou sombre du visiteur.

## Ce qu'elle montre

- Un bandeau global : opérationnel, dégradé, panne partielle, panne majeure, maintenance.
- Chaque composant et sa barre des 90 derniers jours ; le survol d'un jour donne sa disponibilité.
- Les incidents en cours, puis l'historique des 90 jours, avec leurs mises à jour publiques.
- Les maintenances actives et celles des 7 prochains jours.

## Comment l'état est calculé

| Source | Règle |
|---|---|
| Moniteur | OK : opérationnel · avertissement : dégradé · alerte : panne partielle, ou panne majeure en priorité P1 |
| Maintenance active | maintenance |
| Test synthétique | son dernier résultat |
| SLO | objectif manqué : panne partielle |

La disponibilité quotidienne se calcule comme un SLO de moniteur. La page est mise en cache 30 secondes. Elle n'expose ni nom d'hôte, ni valeur, ni requête : seulement les noms de composants que vous avez choisis.

Le nombre de pages dépend de la formule : voir les [tarifs](/tarifs/).
