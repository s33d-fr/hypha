---
id: metrics
path: /docs/metriques/
title: Métriques et tableaux de bord
nav: Métriques et tableaux
section: Observer
order: 30
updated: 2026-11-19
description: Le langage de requête des métriques HYPHA, ses formules et fonctions, l'explorateur, les tableaux de bord, leurs widgets et le partage public en lecture.
lead: Une requête courte, des formules, des fonctions, et une grille de widgets que toute l'équipe partage.
---
## Le langage de requête

```text
<agrégat>:<métrique>{<filtres>} by {<clés>}
```

- **Agrégats** entre séries : `avg`, `sum`, `min`, `max`, `count`.
- **Filtres** : `clé:valeur`, `!clé:valeur` pour exclure, `clé:préfixe*`, `*` pour tout.
- **Regroupement** : `by {host}`, `by {host,mount}`.
- **Agrégat dans le temps** (facultatif) : `.rollup(avg|sum|min|max|last, <secondes>)`.

Exemples :

```text
avg:system.cpu.usage{env:prod} by {host}
max:system.disk.in_use{role:db} by {host,mount}
sum:nginx.requests{*}.rollup(sum, 300)
```

HYPHA lit les points bruts sur deux jours au plus, les agrégats de 5 minutes jusqu'à 60 jours, puis les agrégats d'une heure.

### Formules et fonctions

Chaque requête porte un identifiant (`a`, `b`…) ; une formule les combine : `a / b * 100`. Fonctions disponibles, sur une requête ou une formule :

| Famille | Fonctions |
|---|---|
| Calcul | `abs`, `log2`, `log10`, `cumsum`, `per_second`, `rate`, `derivative` |
| Lissage | `moving_avg(x, n)`, `ewma(x, n)`, `median(x, n)` |
| Comparaison | `timeshift(x, -86400)` (même courbe un jour plus tôt) |
| Sélection | `top(x, 5, 'mean', 'desc')`, `clamp_min`, `clamp_max`, `fill(x, 'zero')` |
| Analyse | `forecast(x, 'linear', horizon)`, `anomalies(x, 'agile', 2)`, `outliers(x, 'mad', 3)` |

## L'explorateur

La page **Métriques** cherche une métrique, propose ses étiquettes, trace la requête et la formule, et enregistre la vue. Chaque métrique peut porter une unité et une description (catalogue fourni pour `system.*`, `snmp.*`, `postgresql.*`, `haproxy.*`, `nginx.*`).

## Tableaux de bord

Deux tableaux sont livrés d'office : **Vue système** et **Vue réseau**. Les vôtres se composent sur une grille de 12 colonnes.

Widgets : courbe, valeur, classement, flux de journaux, résumé des moniteurs, note, carte des hôtes, état des contrôles, carte de chaleur, distribution, tableau, variation, nuage de points, camembert, frise d'événements, courbe d'un moniteur avec ses seuils, valeur d'un moniteur, carte des services, carte du réseau, problèmes, liste de SLO, image, cadre intégré (même origine), groupe.

Fonctions du tableau :

- variables de modèle, à valeurs multiples (`$host`, `$env`) ;
- superposition des événements, par exemple les déploiements (`source:deploy`) ;
- comparaison avec la période précédente, axe logarithmique, seuils colorés ;
- clonage, dossiers, rafraîchissement automatique, mode TV en plein écran ;
- export et import en JSON.

### Partage public en lecture

Un administrateur peut publier un tableau par un lien secret (`/share/?t=…`). Le lien n'exécute que les requêtes des widgets du tableau, en lecture. Il se retire à tout moment.

## Envoyer vos mesures

- StatsD : voir [Intégrations](/docs/integrations/#vos-propres-mesures).
- OpenTelemetry : métriques OTLP sur `/v1/metrics`, voir [Traces](/docs/traces/).
- Événements de déploiement : voir [API](/docs/api/#evenements).
