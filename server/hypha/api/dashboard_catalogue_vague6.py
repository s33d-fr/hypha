"""Tableaux prêts de la vague 6 des intégrations : Jenkins, GitHub, Zot, Odoo, WSUS.

Ajoutés en fin de catalogue par `dashboard_catalogue.py` ; les fonctions de construction viennent
de ce module en paramètres (pas d'import circulaire). Chaque métrique et chaque contrôle visés sont
vérifiés contre le code de l'agent par `tests/test_dashboard_catalogue.py`.
"""


def boards(board, ts, qv, top, checks, WARN80):
    H = "{$host}"
    CI, REG, APP, WIN = "CI et forges", "Dépôts et registres", "Applications métier", "Windows"
    return [
        board("jenkins", CI, "Jenkins", "Tâches en échec ou instables, file d'attente et builds bloqués, exécuteurs occupés, "
              "nœuds hors ligne et leur place libre, santé du plugin Metrics.", ["jenkins"], [
                  checks("w1", "Jenkins", 0, 0, "jenkins.can_connect"),
                  checks("w2", "Tâches", 3, 0, "jenkins.job.status"),
                  checks("w3", "Nœuds", 6, 0, "jenkins.node.status"),
                  checks("w4", "Santé", 9, 0, "jenkins.health"),
                  qv("w5", "Tâches en échec", 0, 3, f"max:jenkins.jobs.failing{H}", "count", thresholds=[{"value": 1, "color": "error"}]),
                  qv("w6", "Builds en file", 3, 3, f"max:jenkins.queue.size{H}", "count"),
                  qv("w7", "Exécuteurs occupés (%)", 6, 3, f"max:jenkins.executors.usage{H}", "percent", thresholds=WARN80),
                  qv("w8", "Nœuds hors ligne", 9, 3, f"max:jenkins.nodes.offline{H}", "count", thresholds=[{"value": 1, "color": "warn"}]),
                  ts("w9", "File d'attente", 0, 5, [f"max:jenkins.queue.size{H}", f"max:jenkins.queue.stuck{H}",
                                                     f"max:jenkins.queue.blocked{H}"], "count", precision=0),
                  ts("w10", "Exécuteurs", 6, 5, [f"max:jenkins.executors.busy{H}", f"max:jenkins.executors.total{H}"], "count", precision=0),
                  ts("w11", "Tâches en échec et instables", 0, 9, [f"max:jenkins.jobs.failing{H}", f"max:jenkins.jobs.unstable{H}"],
                     "count", display="bars", precision=0),
                  ts("w12", "Attente du plus ancien build (s)", 6, 9, [f"max:jenkins.queue.oldest_age{H} by {{host}}"], "second"),
                  top("w13", "Durée du dernier build", 0, 13, f"max:jenkins.job.last_build.duration{H} by {{job}}", "second"),
                  top("w14", "Santé des tâches (%)", 6, 13, f"min:jenkins.job.health{H} by {{job}}", "percent"),
                  top("w15", "Place libre des nœuds (%)", 0, 17, f"min:jenkins.node.disk_free_percent{H} by {{node}}", "percent"),
                  top("w16", "Temps de réponse des nœuds (ms)", 6, 17, f"max:jenkins.node.response_time{H} by {{node}}", "millisecond"),
              ]),
        board("github", CI, "GitHub", "Workflows GitHub Actions en échec sur la branche par défaut, exécutions en file et en cours, "
              "durées, alertes de Dependabot, du code scanning et du secret scanning, quota de l'API.", ["github"], [
                  checks("w1", "Connecteur", 0, 0, "github.can_connect"),
                  checks("w2", "Workflows", 3, 0, "github.workflow.status"),
                  checks("w3", "Sécurité", 6, 0, "github.security"),
                  checks("w4", "Quota de l'API", 9, 0, "github.rate_limit"),
                  qv("w5", "Workflows en échec", 0, 3, "sum:github.workflows.failing{*}", "count", thresholds=[{"value": 1, "color": "error"}]),
                  qv("w6", "Exécutions en file", 3, 3, "sum:github.runs.queued{*}", "count"),
                  qv("w7", "Exécutions en cours", 6, 3, "sum:github.runs.in_progress{*}", "count"),
                  qv("w8", "Quota restant", 9, 3, "min:github.rate_limit.remaining{*}", "count"),
                  ts("w9", "Exécutions en file et en cours", 0, 5, ["sum:github.runs.queued{*} by {repo}", "sum:github.runs.in_progress{*} by {repo}"],
                     "count", display="bars", precision=0),
                  ts("w10", "Attente de la plus ancienne exécution (s)", 6, 5, ["max:github.runs.queued.oldest_age{*} by {repo}"], "second"),
                  top("w11", "Durée de la dernière exécution", 0, 9, "max:github.workflow.run.duration{*} by {repo,workflow}", "second"),
                  top("w12", "Alertes Dependabot ouvertes", 6, 9, "sum:github.dependabot.alerts{*} by {repo,severity}", "count", precision=0),
                  top("w13", "Alertes du code scanning", 0, 13, "sum:github.code_scanning.alerts{*} by {repo,severity}", "count", precision=0),
                  top("w14", "Secrets exposés", 6, 13, "sum:github.secret_scanning.alerts{*} by {repo}", "count", precision=0),
                  ts("w15", "Quota de l'API utilisé (%)", 0, 17, ["max:github.rate_limit.used_percent{*} by {host}"], "percent", w=12),
              ]),
        board("zot", REG, "Zot", "Registre OCI Zot : requêtes par code et par méthode, latence, téléchargements et envois par dépôt, "
              "stockage par dépôt, verrous du stockage, tâches de fond.", ["openmetrics:zot"], [
                  checks("w1", "Zot", 0, 0, "openmetrics.can_connect"),
                  qv("w2", "Stockage total", 3, 0, f"sum:zot.repo.storage{H}", "byte"),
                  qv("w3", "Tâches en file", 6, 0, f"sum:zot.scheduler.queue{H}", "count"),
                  qv("w4", "Verrous (p95, s)", 9, 0, f"max:zot.storage.lock_latency.p95{H}", "second", precision=3),
                  ts("w5", "Requêtes par code", 0, 3, [f"sum:zot.http.requests{H} by {{code}}"], "operation_per_second"),
                  ts("w6", "Latence par méthode (p95, s)", 6, 3, [f"max:zot.http.latency.p95{H} by {{method}}"], "second"),
                  top("w7", "Stockage par dépôt", 0, 7, f"max:zot.repo.storage{H} by {{repo}}", "byte"),
                  ts("w8", "Téléchargements et envois", 6, 7, [f"sum:zot.repo.downloads{H} by {{repo}}", f"sum:zot.repo.uploads{H} by {{repo}}"],
                     "operation_per_second"),
                  ts("w9", "Travailleurs des tâches de fond", 0, 11, [f"max:zot.scheduler.workers{H} by {{state}}"], "count", precision=0),
                  ts("w10", "Attente des verrous (p95, s)", 6, 11, [f"max:zot.storage.lock_latency.p95{H} by {{locktype}}"], "second"),
              ]),
        board("odoo", APP, "Odoo", "Santé (/web/health, base PostgreSQL jointe), temps de réponse, processus et workers, "
              "mémoire résidente par rôle.", ["odoo"], [
                  checks("w1", "Odoo", 0, 0, "odoo.can_connect"),
                  checks("w2", "Santé", 3, 0, "odoo.health"),
                  qv("w3", "Temps de réponse (ms)", 6, 0, f"max:odoo.response_time{H}", "millisecond", precision=1),
                  qv("w4", "Workers", 9, 0, f"max:odoo.workers{H}", "count"),
                  ts("w5", "Temps de réponse (ms)", 0, 3, [f"avg:odoo.response_time{H} by {{host,endpoint}}"], "millisecond"),
                  ts("w6", "Processus par rôle", 6, 3, [f"max:odoo.processes{H} by {{role}}"], "count", display="area", precision=0),
                  ts("w7", "Mémoire résidente par rôle", 0, 7, [f"max:odoo.processes.memory{H} by {{role}}"], "byte", display="area"),
                  ts("w8", "Base PostgreSQL jointe", 6, 7, [f"min:odoo.database.reachable{H} by {{host}}"], None, precision=0),
              ]),
        board("wsus", WIN, "WSUS", "Synchronisation avec le serveur amont, ordinateurs à mettre à jour, en défaut ou sans rapport, "
              "mises à jour en attente d'approbation, fichiers à télécharger, base et place libre du contenu.", ["wsus"], [
                  checks("w1", "WSUS", 0, 0, "wsus.can_connect"),
                  checks("w2", "Synchronisation", 3, 0, "wsus.sync"),
                  checks("w3", "Services", 6, 0, "wsus.service"),
                  checks("w4", "Pool WsusPool", 9, 0, "wsus.app_pool"),
                  qv("w5", "Ordinateurs", 0, 3, f"max:wsus.computers{H}", "count"),
                  qv("w6", "Ordinateurs en défaut", 3, 3, f"max:wsus.computers.with_errors{H}", "count", thresholds=[{"value": 1, "color": "warn"}]),
                  qv("w7", "En attente d'approbation", 6, 3, f"max:wsus.updates.not_approved{H}", "count"),
                  qv("w8", "Critiques non approuvées", 9, 3, f"max:wsus.updates.critical_not_approved{H}", "count"),
                  ts("w9", "Ordinateurs", 0, 5, [f"max:wsus.computers.needing_updates{H}", f"max:wsus.computers.with_errors{H}",
                                                  f"max:wsus.computers.stale{H}"], "count", precision=0),
                  ts("w10", "Mises à jour", 6, 5, [f"max:wsus.updates.not_approved{H}", f"max:wsus.updates.needing_files{H}",
                                                   f"max:wsus.updates.with_client_errors{H}"], "count", precision=0),
                  ts("w11", "Âge de la dernière synchronisation réussie (s)", 0, 9, [f"max:wsus.sync.age{H} by {{host}}"], "second"),
                  ts("w12", "Base SUSDB et place libre du contenu", 6, 9, [f"max:wsus.database.size{H} by {{host}}",
                                                                           f"min:wsus.content.free{H} by {{host}}"], "byte"),
              ]),
    ]
