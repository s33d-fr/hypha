---
id: traces
path: /docs/traces/
title: Traces, OpenTelemetry, erreurs et RUM
nav: Traces et OpenTelemetry
section: Observer
order: 32
updated: 2026-11-19
description: Envoyer traces, métriques et journaux OpenTelemetry à HYPHA ; APM, carte des services, suivi des erreurs et mesure de l'expérience réelle des visiteurs (RUM).
lead: HYPHA parle OpenTelemetry. Vos applications envoient leurs traces, directement ou par l'agent de l'hôte, et HYPHA en tire services, latences, erreurs et carte des dépendances.
---
## Envoyer des données OpenTelemetry

HYPHA reçoit OTLP sur HTTP, en protobuf ou en JSON :

| Signal | Adresse |
|---|---|
| Traces | `https://hypha.s33d.fr/v1/traces` |
| Métriques | `https://hypha.s33d.fr/v1/metrics` |
| Journaux | `https://hypha.s33d.fr/v1/logs` |

La clé d'ingestion passe dans l'en-tête `X-Hypha-Api-Key`, ou `Authorization: Bearer <clé>`.

### Par l'agent (conseillé)

Activez le relais de l'agent : les applications visent la boucle locale, l'agent ajoute la clé et réessaie si le serveur est injoignable.

```yaml
otlp:
  enabled: true
  listen: 127.0.0.1:4318
```

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
export OTEL_SERVICE_NAME=boutique-api
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment=prod,service.version=2.14.0
```

### En direct

```bash
export OTEL_EXPORTER_OTLP_ENDPOINT=https://hypha.s33d.fr
export OTEL_EXPORTER_OTLP_HEADERS="X-Hypha-Api-Key=hph_votre_cle"
```

### Ce que HYPHA retient

- `service.name` donne le service ; `host.name` rattache la trace à l'hôte du même nom.
- La ressource d'un span vient de `http.route`, sinon `url.path`, sinon `db.statement` (200 caractères).
- Un span au statut OTLP « erreur » est une erreur.
- Métriques OTLP : jauges et sommes deviennent des points (une somme cumulative devient un débit) ; un histogramme donne `.count`, `.sum`, `.avg` et des quantiles approchés `.p50`, `.p95`, `.p99`.

## APM

{{video:3-traces}}

- **Services** : débit, taux d'erreur, latences p50, p95 et p99 de chaque service.
- **Fiche d'un service** : opérations, ressources, courbes, déploiements superposés, journaux et moniteurs liés.
- **Traces** : filtre par service, opération, statut et durée minimale ; une trace s'affiche en cascade avec ses spans, ses attributs, ses événements et les journaux liés.
- **Carte des services** : les dépendances tirées des spans (appel d'un service à un autre, base de données, service externe), colorées par état.
- **Catalogue de services** : équipe, niveau, liens (procédure, dépôt, documentation, discussion), moniteurs, SLO et erreurs de chaque service. Le lien vers la procédure apparaît dans les notifications de ses moniteurs.

Modèles de moniteurs : erreurs HTTP 5xx d'un service, latence p95, nouvelle erreur applicative.

## Suivi des erreurs

La page **Erreurs** regroupe en problèmes les erreurs qui se ressemblent :

- spans en erreur portant un événement `exception` (`exception.type`, `exception.message`, `exception.stacktrace`) ;
- lignes de journal d'erreur portant une pile (Python, Java, Go, Node) ou `error.kind` et `error.stack` ;
- erreurs JavaScript remontées par le RUM.

L'empreinte d'un problème tient compte de l'origine, du service, du type et des cinq premières trames de la pile, sans numéros de ligne ni valeurs. Chaque problème montre sa pile, son histogramme, ses dernières occurrences avec la trace ou la ligne correspondante. Un problème résolu qui revient repasse ouvert et est marqué « régressé ». Boutons **Résoudre**, **Ignorer** et **Expliquer**.

## RUM : l'expérience réelle des visiteurs

Créez une application dans la page **RUM**, puis ajoutez son script à vos pages :

```html
<script src="https://hypha.s33d.fr/rum/hypha-rum.js" data-token="jeton_client" data-service="boutique-web" defer></script>
```

Attributs facultatifs : `data-env`, `data-version`, `data-sample` (part des sessions suivies, de 0 à 1), `data-full-url` (`true` pour garder l'adresse complète, paramètres compris).

Le script, sans dépendance, relève les vues et leur temps de chargement, les signaux web LCP, CLS et INP, les erreurs JavaScript et les ressources de plus d'une seconde. Il ne pose aucun cookie : la session tient dans l'onglet (`sessionStorage`) et expire après 30 minutes d'inactivité. La page RUM montre vues, sessions, erreurs, percentile 75 des signaux web et pages les plus lentes.

Le jeton client n'ouvre que l'envoi d'événements RUM : il peut figurer dans une page publique.
