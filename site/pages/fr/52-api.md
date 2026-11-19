---
id: api
path: /docs/api/
title: API de HYPHA
nav: API
section: Administrer
order: 52
updated: 2026-11-19
description: L'API REST de HYPHA : authentification par clé, envoi de données, OpenTelemetry, événements de déploiement, moniteurs, maintenances, métriques, journaux, actions.
lead: Tout ce que fait l'interface passe par une API JSON. Une clé d'application suffit pour l'automatiser.
---
## Authentification

| Usage | Clé | En-tête |
|---|---|---|
| Lire et écrire comme l'interface | application | `X-Hypha-App-Key: hph_…` |
| Envoyer des données | ingestion | `X-Hypha-Api-Key: hph_…` (OTLP : aussi `Authorization: Bearer hph_…`) |

Les clés se créent dans **Réglages › Clés API** (voir [Comptes et rôles](/docs/comptes-et-roles/#cles)). Une clé d'application agit avec le rôle de son auteur. Adresse de base : `https://hypha.s33d.fr/api/v1` (en auto-hébergement, votre adresse).

## Conventions

- Corps et réponses en JSON ; horodatages en millisecondes Unix (`ts`, `from`, `to`). Sans `from` ni `to`, la dernière heure.
- Erreur : un statut HTTP et `{"error": "code_court", "detail": "phrase lisible"}`.
- Une liste vide rend `[]` ; un objet absent de l'espace rend 404 `not_found`.
- Pagination : `limit` (100 par défaut, 1 000 au plus) et `cursor` quand le volume l'exige.
- Une limite de formule atteinte rend 402 `plan_limit` avec le détail.

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" https://hypha.s33d.fr/api/v1/hosts
```

## Lire

| Route | Rend |
|---|---|
| `GET /hosts` | les hôtes, leur état et leurs valeurs des 2 dernières minutes (`?q=`, `?tag=clé:valeur`) |
| `GET /hosts/{id}` | un hôte, son agent, ses derniers contrôles |
| `GET /problems` | les groupes hors OK, triés par priorité |
| `GET /monitors`, `GET /monitors/{id}/history` | les moniteurs et l'historique d'un moniteur |
| `POST /metrics/query` | séries d'une ou plusieurs requêtes, avec formules |
| `GET /logs/search`, `/logs/facets`, `/logs/histogram`, `/logs/patterns` | recherche dans les journaux |
| `GET /apm/services`, `/apm/traces`, `/apm/traces/{trace_id}` | services, traces, détail d'une trace |
| `GET /inventory`, `/inventory/packages?name=openssl&version_lt=3.5.2` | inventaire et recherche d'un paquet sur tout le parc |
| `GET /certificates` | certificats relevés, triés par échéance |
| `GET /usage` | consommation par jour |

Exemple de requête de métriques :

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" -H "Content-Type: application/json" \
  https://hypha.s33d.fr/api/v1/metrics/query \
  -d '{"queries":[{"id":"a","q":"avg:system.cpu.usage{env:prod} by {host}"}],"max_points":300}'
```

## Écrire

| Route | Effet |
|---|---|
| `POST /monitors`, `PUT /monitors/{id}`, `DELETE /monitors/{id}` | moniteurs |
| `POST /monitors/from-template` | moniteur depuis un modèle : `{"template": "disk_forecast", "vars": {…}}` |
| `POST /monitors/{id}/mute` | sourdine jusqu'à `until` (ms) ou sans fin (`null`) |
| `POST /monitors/{id}/groups/ack` | acquitter un groupe, avec un commentaire |
| `POST /downtimes` | maintenance ; `{"duration_s": 3600, "scope": {…}}` commence tout de suite |
| `POST /incidents`, `POST /incidents/from-alert` | incidents |
| `POST /synthetics`, `POST /synthetics/{id}/run` | tests synthétiques, exécution immédiate |
| `POST /dashboards`, `POST /dashboards/import` | tableaux de bord |
| `POST /actions`, `POST /actions/{id}/approve` | proposer et approuver une action à distance |
| `POST /explain` | explication par l'IA : `{"kind": "alert", "monitor_id": 6, "group": "host:web-02", "lang": "fr"}` ; `kind` vaut aussi `host`, `log`, `trace` ou `error` |

## Événements

Un événement apparaît sur la frise, se superpose aux graphiques et peut déclencher un moniteur. Exemple, à la fin d'un déploiement :

```bash
curl -s -H "X-Hypha-App-Key: $HYPHA_APP_KEY" -H "Content-Type: application/json" \
  https://hypha.s33d.fr/api/v1/events \
  -d '{"title":"Déploiement boutique-api 2.15.0","source":"deploy","type":"deploy","alert_type":"success",
       "tags":{"service":"boutique-api","version":"2.15.0"}}'
```

Les graphiques APM et la fiche du service superposent les événements `source:deploy`.

## Envoyer des données

| Route | Contenu |
|---|---|
| `POST /api/v1/intake/metrics`, `/logs`, `/checks`, `/events`, `/host`… | protocole de l'agent (corps JSON, gzip accepté, 10 Mo au plus) |
| `POST /v1/traces`, `/v1/metrics`, `/v1/logs` | OpenTelemetry (OTLP/HTTP, protobuf ou JSON) |
| `POST /api/v1/intake/rum?token=…` | navigateur (script RUM) |

Pour vos propres applications, préférez OpenTelemetry ou StatsD par l'agent : voir [Traces](/docs/traces/) et [Intégrations](/docs/integrations/#vos-propres-mesures). Le serveur accepte des horodatages compris entre 14 jours dans le passé et 10 minutes dans le futur.

Réponses de l'ingestion : `202 {"status":"ok","accepted":N,"dropped":M}` ; 401 clé inconnue ; 403 clé révoquée ou de type application ; 413 corps trop gros ; 429 débit ; 402 limite de formule.

## Pages publiques

- `GET /api/public/status/{slug}` : l'état d'une page de statut publiée, sans authentification.
- `GET /api/public/dashboards/{jeton}` : un tableau partagé en lecture.
