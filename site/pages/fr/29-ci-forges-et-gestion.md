---
id: ci-forges
path: /docs/ci-forges-et-gestion/
title: CI, forges, registres et gestion : Jenkins, GitHub, Zot, Odoo
nav: CI, forges et gestion
section: Intégrations
order: 29
updated: 2026-11-21
description: Intégrations de l'agent HYPHA pour Jenkins (API JSON), GitHub et GitHub Enterprise Cloud (connecteur en lecture seule), le registre OCI Zot (extension metrics) et Odoo (santé, version, workers) : réglages, droits, mesures, contrôles, modèles de moniteurs.
lead: Les builds en échec, les files qui s'allongent, les workflows rouges, le registre et l'ERP, lus en lecture seule par l'agent.
---
## Principe

Ces intégrations suivent les règles des autres ([Applications et services](/docs/applications-et-services/)) : en mode `auto`, une sonde dont le logiciel répond à son adresse habituelle s'active seule ; une adresse ou un compte posé dans la conf dit l'intention, et les échecs se signalent dès le premier passage. Une fois le logiciel vu, son contrôle `<nom>.can_connect` passe en CRIT s'il ne répond plus. Plusieurs cibles se déclarent par `instances` ([Configuration](/docs/configuration/)). Rien n'est jamais écrit : aucun build lancé, aucun workflow relancé, aucune image poussée, aucune base Odoo ouverte.

Chaque intégration a ses [modèles de moniteurs](/docs/moniteurs/) (familles « CI et forges », « Dépôts et registres », « Applications métier ») et son tableau prêt (**Tableaux › Catalogue**).

## Jenkins

L'agent lit l'API JSON de Jenkins toutes les 60 secondes : `/api/json` (tâches et leurs derniers builds, dossiers et projets multibranches sur trois niveaux), `/queue/api/json` (file d'attente), `/computer/api/json` (exécuteurs et nœuds), et, si le plugin Metrics est installé, ses contrôles de santé. En mode `auto`, la sonde s'active si un Jenkins répond sur `127.0.0.1:8080` (en-tête `X-Jenkins`).

```yaml
integrations:
  jenkins:
    url: https://ci.exemple.fr      # défaut http://127.0.0.1:8080
    username: hypha                 # compte de lecture
    password: 11aa22bb…             # son jeton d'API (Profil › Sécurité › Jeton d'API)
    jobs: ["prod/*", "deploiement-*"]   # facultatif : motifs des noms complets ; vide = toutes
    max_jobs: 500                   # tâches publiées une par une, au plus
```

Droits du compte (stratégie par matrice) : **Overall/Read**, **Job/Read**, **Agent/ExtendedRead**. Facultatifs : **Metrics/HealthCheck** (plugin Metrics) pour ses contrôles de santé ; **Agent/Connect** pour la place libre et le temps de réponse des nœuds, que Jenkins ne montre qu'à ce droit. Agent/Connect permet aussi de relier un agent de build : ne le donnez que si la place des nœuds vous importe plus que ce risque.

| Mesure | Sens |
|---|---|
| `jenkins.jobs.total`, `.failing`, `.unstable`, `.disabled`, `.building` | tâches suivies, dernier build en échec, instable, désactivées, en cours |
| `jenkins.job.health` (`job`) | santé de la tâche (0 à 100, météo de Jenkins) |
| `jenkins.job.last_build.duration` (`job`) | durée du dernier build terminé, en secondes |
| `jenkins.job.last_success.age` (`job`) | temps écoulé depuis le dernier build réussi |
| `jenkins.queue.size`, `.blocked`, `.buildable`, `.stuck`, `.oldest_age` | file d'attente : builds en attente, bloqués, prêts, marqués « stuck », attente du plus ancien |
| `jenkins.executors.total`, `.busy`, `.idle`, `.usage` | exécuteurs, occupés, libres, part occupée (%) |
| `jenkins.nodes.total`, `.offline` ; `jenkins.node.offline`, `.executors` (`node`) | nœuds et nœuds hors ligne |
| `jenkins.node.disk_free`, `.disk_free_percent`, `.temp_free`, `.response_time` (`node`) | place libre et temps de réponse d'un nœud (droit Agent/Connect) |
| `jenkins.health_check` (`check`) | contrôles du plugin Metrics (1 au vert, 0 en défaut) |
| `jenkins.quieting_down` | 1 quand un arrêt est préparé |

Contrôles : `jenkins.can_connect` ; `jenkins.job.status` par tâche (CRIT si le dernier build a échoué, WARN s'il est instable ou interrompu ; une tâche jamais construite ou désactivée n'est pas jugée) ; `jenkins.node.status` par nœud (CRIT hors ligne, WARN mis hors ligne par un administrateur) ; `jenkins.queue` (WARN quand Jenkins marque des builds bloqués, avec la raison : « aucun nœud ne porte l'étiquette… ») ; `jenkins.health` (CRIT si un contrôle du plugin Metrics est en défaut, WARN pendant un arrêt préparé).

Modèles : **Jenkins injoignable**, **Tâche Jenkins en échec**, **Nœud Jenkins hors ligne**, **Builds bloqués dans la file Jenkins**, **Attente dans la file Jenkins**, **Exécuteurs Jenkins saturés**, **Place libre d'un nœud Jenkins**, **Santé de Jenkins**. Tableau prêt : **Jenkins**.

Éprouvé sur Jenkins 2.580.2 LTS (image `jenkins/jenkins:lts-jdk21`, plugins Folders et Metrics), avec un compte aux droits ci-dessus : tâches réussie, en échec, instable et dans un dossier, build bloqué faute de nœud portant l'étiquette, nœud jamais relié (CRIT), arrêt de Jenkins (CRIT puis retour au vert).

## GitHub

Connecteur en lecture seule vers **github.com** ou **GitHub Enterprise Cloud** (`*.ghe.com`), sur le modèle des [connecteurs cloud](/docs/cloud/) : un seul agent l'interroge, ses mesures portent le nom de cet agent. Toutes les `interval` (5 minutes par défaut), pour chaque dépôt nommé :

- la branche par défaut (relue toutes les 6 heures) ;
- les 100 dernières exécutions de GitHub Actions : dernière exécution terminée de chaque workflow sur la branche par défaut (les demandes de fusion et les étiquettes sont écartées), exécutions en file et en cours ;
- avec un jeton qui le permet : alertes ouvertes de Dependabot, du code scanning et du secret scanning (100 au plus par type) ;
- le quota de l'API (`/rate_limit`, qui ne compte pas dans le quota).

```yaml
integrations:
  github:
    repositories: [mon-organisation/site, mon-organisation/api]
    token: github_pat_…           # facultatif pour un dépôt public
    # endpoint: https://api.mon-entreprise.ghe.com   # GitHub Enterprise Cloud (résidence des données)
    interval: 5m
    security: auto                # alertes lues si le jeton le permet ; false pour s'en passer
    tags: {forge: github}
```

Sans jeton, seuls les dépôts publics se lisent et GitHub accorde **60 requêtes par heure** à l'adresse publique de la machine (deux par dépôt et par relevé) : l'intervalle est alors de 5 minutes au moins, et l'agent prévient quand les dépôts nommés dépasseraient ce quota. Avec un jeton (5 000 requêtes par heure), une minute au moins. Jeton conseillé : **jeton à granularité fine** limité aux dépôts suivis, en lecture seule : **Actions**, **Metadata**, et pour les alertes **Dependabot alerts**, **Code scanning alerts**, **Secret scanning alerts**. Un type d'alerte que le jeton ne permet pas de lire est sauté et nommé dans le message de `github.can_connect`.

| Mesure | Sens |
|---|---|
| `github.workflows.failing` (`repo`) | workflows dont la dernière exécution sur la branche par défaut a échoué |
| `github.workflow.run.duration` (`repo`, `workflow`) | durée de la dernière exécution terminée |
| `github.runs.queued`, `.in_progress`, `.queued.oldest_age` (`repo`) | exécutions en file, en cours, attente de la plus ancienne en file (secondes) |
| `github.dependabot.alerts`, `github.code_scanning.alerts` (`repo`, `severity`) | alertes ouvertes par gravité (critical, high, medium, low) |
| `github.secret_scanning.alerts` (`repo`) | secrets exposés, alertes ouvertes |
| `github.rate_limit.limit`, `.remaining`, `.used_percent` | quota de l'API |

Contrôles : `github.can_connect` ; `github.workflow.status` par workflow (CRIT en échec ou arrêté par le délai, WARN annulé ou en attente d'une action) ; `github.security` par dépôt (CRIT si une alerte critique ou un secret exposé est ouvert, WARN pour une alerte de gravité haute) ; `github.rate_limit` (WARN sous 10 % du quota, CRIT épuisé). Les workflows propres à GitHub (Dependabot, graphe des dépendances) se nomment par leur chemin (`dependabot/update-graph`).

Modèles : **Connecteur GitHub en échec**, **Workflow GitHub Actions en échec**, **Exécutions GitHub Actions en file**, **Alertes de sécurité GitHub**, **Quota de l'API GitHub**. Tableau prêt : **GitHub**.

Éprouvé sur l'API publique de github.com, sans jeton, avec un dépôt public (workflows réussis, workflow dynamique de Dependabot). Le jeton, les alertes de sécurité et GitHub Enterprise Cloud suivent la documentation de l'API REST de GitHub (version 2022-11-28) et ses formats de réponse, repris dans les tests de l'agent ; ils n'ont pas encore été lus sur un vrai compte.

## Zot

Le registre OCI [Zot](https://zotregistry.dev) publie ses mesures au format Prometheus par son extension `metrics` (image complète ; l'image « minimal » ne l'a pas). Dans `config.json` de Zot :

```json
"extensions": { "metrics": { "enable": true, "prometheus": { "path": "/metrics" } } }
```

Puis le profil `zot` du [lecteur OpenMetrics](/docs/logiciels-prometheus/) :

```yaml
integrations:
  openmetrics:
    - profile: zot
      url: http://127.0.0.1:5000/metrics      # port de http.port
      # headers: {Authorization: "Basic …"}   # si Zot demande un compte
```

| Mesure | Sens |
|---|---|
| `zot.http.requests` (`code`, `method`) | requêtes par seconde |
| `zot.http.latency` (`method`) | latence (histogramme : `.p50`, `.p95`, `.p99`, `.avg`) |
| `zot.repo.downloads`, `zot.repo.uploads` (`repo`) | téléchargements et envois d'images par seconde |
| `zot.repo.storage` (`repo`) | place occupée par dépôt, en octets |
| `zot.storage.lock_latency` (`locktype`) | attente des verrous du stockage (histogramme) |
| `zot.gc.duration` | nettoyage du stockage (résumé) |
| `zot.scheduler.queue` (`priority`), `zot.scheduler.workers` (`state`), `.workers_total`, `zot.scheduler.generators` | tâches de fond : file, travailleurs occupés ou libres |

Modèles : **Zot injoignable**, **Tâches en retard dans Zot**, **Verrous du stockage Zot lents**. Tableau prêt : **Zot**.

Éprouvé sur Zot v2.1.23 (image `zot-linux-amd64`, en conteneur) : envoi puis téléchargement d'une image, arrêt du registre (`openmetrics.can_connect` en CRIT puis au vert).

## Odoo

L'agent lit Odoo (17 et suivants) toutes les 30 secondes, sans compte :

- `/web/health` ; depuis Odoo 18, `?db_server_status=1` vérifie aussi que le serveur PostgreSQL répond : sa panne rend « fail » ;
- la version par XML-RPC (`/xmlrpc/2/common`, méthode `version`), avec repli sur `/web/webclient/version_info` quand XML-RPC est coupé ou retiré ;
- le temps de réponse de chacun ;
- quand l'url vise la machine elle-même : les processus d'Odoo dans `/proc` (maître ou serveur à fils, workers, processus gevent des notifications en direct) et leur mémoire résidente. Avec `setproctitle` installé, les workers HTTP et cron se distinguent ; sans lui (image officielle), ce sont des « worker ».

```yaml
integrations:
  odoo:
    url: http://127.0.0.1:8069     # défaut ; ou l'adresse servie par le mandataire
    processes: true                # défaut : oui si l'url vise la machine
```

| Mesure | Sens |
|---|---|
| `odoo.response_time` (`endpoint` : `health`, `xmlrpc` ou `version_info`) | temps de réponse, en millisecondes |
| `odoo.database.reachable` | 1 si le serveur PostgreSQL d'Odoo répond (Odoo 18 et suivants) |
| `odoo.processes`, `odoo.processes.memory` (`role` : `main`, `worker`, `http`, `cron`, `gevent`) | processus et mémoire résidente par rôle |
| `odoo.workers` | workers en marche (mode multiprocessus, `workers` > 0) |

Contrôles : `odoo.can_connect` (avec la version) et `odoo.health` (CRIT quand `/web/health` rend « fail »). PostgreSQL lui-même se suit par l'[intégration PostgreSQL](/docs/bases-de-donnees/).

Modèles : **Odoo injoignable**, **Odoo en défaut**, **Odoo lent**. Tableau prêt : **Odoo**.

Éprouvé sur Odoo 19.0 (image officielle `odoo:19`, mode multiprocessus à deux workers HTTP et un cron, PostgreSQL 17) : version lue par XML-RPC, cinq processus vus (maître, trois workers, gevent), connexions à la base interdites (`odoo.health` en CRIT puis au vert), arrêt d'Odoo (CRIT).
