---
id: slos
path: /docs/slo/
title: Objectifs de niveau de service (SLO)
nav: SLO
section: Alerter et agir
order: 44
updated: 2026-11-19
description: Définir des SLO dans HYPHA, à partir d'un moniteur ou de deux requêtes de métriques, suivre le budget d'erreur et la vitesse de consommation.
lead: Un objectif chiffré, un budget d'erreur, et une alerte quand il fond trop vite.
---
## Deux façons de mesurer

- **À partir d'un moniteur** : la part du temps où il était OK. Un avertissement ou l'absence de données comptent comme OK, une alerte comme mauvais. Le temps avant la création du moniteur n'entre pas dans le calcul.
- **À partir de deux requêtes** : le rapport « bonnes » sur « toutes », par exemple les requêtes sans erreur sur toutes les requêtes. Sans aucune requête sur la fenêtre, le SLI vaut 100 %.

Réglages : cible (par exemple 99,9 %), seuil d'avertissement facultatif, fenêtre de 7, 30 ou 90 jours.

## Ce que HYPHA calcule

- **SLI** : la valeur mesurée sur la fenêtre.
- **Budget d'erreur restant** : (SLI − cible) ÷ (100 − cible), en pourcentage.
- **Vitesse de consommation** sur 1 heure et sur 6 heures : 1 signifie que le budget sera épuisé pile à la fin de la fenêtre.
- **État** : bon, avertissement, objectif manqué.

La page d'un SLO trace le SLI cumulé avec la ligne de la cible. Le widget de tableau de bord `slo` et le rapport « SLO » reprennent ces valeurs.

## Alerter

Un moniteur de type `slo` alerte quand le budget restant passe sous un seuil, ou quand la vitesse de consommation dépasse un seuil sur 1 h ou 6 h. Un SLO peut aussi figurer comme composant d'une [page de statut](/docs/pages-de-statut/) : objectif manqué y vaut « panne partielle ».
