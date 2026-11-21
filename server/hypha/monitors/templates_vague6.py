"""Modèles de moniteurs de la vague 6 des intégrations : Jenkins, GitHub, Zot, Odoo, WSUS.

Ajoutés en fin de catalogue par `templates.py` ; chaque contrôle ou métrique visé est publié par
l'agent (vérifié par `tests/test_vague6.py` contre le code de l'agent). Les fonctions de
construction viennent de `templates.py` en paramètres, pour éviter un import circulaire.
"""

GIB = 1024 ** 3


def vague6(_v, _metric, _check, two, SCOPE):
    CI, REG, APP, WIN = "CI et forges", "Dépôts et registres", "Applications métier", "Windows"

    def check(tid, family, name, desc, chk, by, alert, ok, priority=2):
        return {"id": tid, "family": family, "name": name, "description": desc, "vars": [],
                "build": lambda v: _check(name, chk, list(by), priority=priority, message=two(alert, ok))}

    def zot_down(v):
        d = _check("Zot injoignable", "openmetrics.can_connect", ["host", "instance_name"], priority=1,
                   message=two("{{host}} : le registre Zot ({{tags.endpoint}}) ne répond plus.",
                               "{{host}} : le registre Zot répond de nouveau."))
        d["query"]["tags"] = {"instance_name": "zot"}
        return d

    def scoped(v, extra):
        return extra if v["scope"] == "*" else v["scope"] + "," + extra

    return [
        # ── Jenkins ──────────────────────────────────────────────────────────────────────
        check("jenkins_down", CI, "Jenkins injoignable", "L'API de Jenkins ne répond plus (service arrêté, compte refusé).",
              "jenkins.can_connect", ("host", "instance"), "{{host}} : Jenkins injoignable.", "{{host}} : Jenkins de nouveau joignable.",
              priority=1),
        check("jenkins_job_failed", CI, "Tâche Jenkins en échec",
              "Le dernier build terminé d'une tâche a échoué (CRIT) ou est instable, interrompu (WARN).",
              "jenkins.job.status", ("host", "job"), "Jenkins : la tâche {{tags.job}} est en échec.",
              "Jenkins : la tâche {{tags.job}} a de nouveau réussi.", priority=3),
        check("jenkins_node_offline", CI, "Nœud Jenkins hors ligne",
              "Un nœud (agent de build) est hors ligne (CRIT), ou mis hors ligne par un administrateur (WARN).",
              "jenkins.node.status", ("host", "node"), "Jenkins : le nœud {{tags.node}} est hors ligne.",
              "Jenkins : le nœud {{tags.node}} est de nouveau en ligne."),
        check("jenkins_queue_stuck", CI, "Builds bloqués dans la file Jenkins",
              "Jenkins marque des builds « stuck » : en attente bien au-delà de leur durée habituelle (aucun nœud ne porte "
              "l'étiquette demandée, exécuteurs tous pris).", "jenkins.queue", ("host", "instance"),
              "{{host}} : des builds sont bloqués dans la file de Jenkins.", "{{host}} : plus aucun build bloqué dans la file.", priority=3),
        check("jenkins_health", CI, "Santé de Jenkins",
              "Contrôles de santé du plugin Metrics en défaut (place disque, plugins, fils bloqués), ou arrêt préparé.",
              "jenkins.health", ("host", "instance"), "{{host}} : Jenkins signale un défaut de santé.",
              "{{host}} : santé de Jenkins revenue au vert."),
        {"id": "jenkins_queue_wait", "family": CI, "name": "Attente dans la file Jenkins",
         "description": "Le plus ancien build en attente l'est depuis trop longtemps (en secondes, sur 15 minutes).",
         "vars": [SCOPE, _v("critical", "Attente critique (s)", default=1800), _v("warning", "Attente d'avertissement (s)", default=600)],
         "build": lambda v: _metric("Attente dans la file Jenkins", f"max:jenkins.queue.oldest_age{{{v['scope']}}} by {{host,instance}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=3,
                                    message=two("{{host}} : un build attend depuis {{value}} s dans la file de Jenkins.",
                                                "{{host}} : attente dans la file de Jenkins revenue à {{value}} s."))},
        {"id": "jenkins_executors_saturated", "family": CI, "name": "Exécuteurs Jenkins saturés",
         "description": "Part des exécuteurs occupés pendant 30 minutes : les builds s'accumulent.",
         "vars": [SCOPE, _v("critical", "Seuil critique (%)", default=100), _v("warning", "Seuil d'avertissement (%)", default=90)],
         "build": lambda v: _metric("Exécuteurs Jenkins saturés", f"min:jenkins.executors.usage{{{v['scope']}}} by {{host,instance}}", ">=",
                                    v["critical"], v["warning"], window_s=1800, aggregation="min", priority=3,
                                    message=two("{{host}} : {{value}} % des exécuteurs Jenkins occupés depuis 30 min.",
                                                "{{host}} : exécuteurs Jenkins de nouveau disponibles ({{value}} %)."))},
        {"id": "jenkins_node_disk", "family": CI, "name": "Place libre d'un nœud Jenkins",
         "description": "Part libre du disque de travail d'un nœud (moniteur DiskSpaceMonitor ; demande le droit Agent/Connect).",
         "vars": [SCOPE, _v("critical", "Seuil critique (% libre)", default=10), _v("warning", "Seuil d'avertissement (% libre)", default=20)],
         "build": lambda v: _metric("Place libre d'un nœud Jenkins", f"min:jenkins.node.disk_free_percent{{{v['scope']}}} by {{host,node}}", "<",
                                    v["critical"], v["warning"], window_s=900, aggregation="max", priority=2,
                                    message=two("Jenkins : le nœud {{tags.node}} n'a plus que {{value}} % de place libre.",
                                                "Jenkins : le nœud {{tags.node}} a de nouveau {{value}} % de place libre."))},
        # ── GitHub ───────────────────────────────────────────────────────────────────────
        check("github_down", CI, "Connecteur GitHub en échec",
              "L'agent n'arrive plus à lire l'API de GitHub (jeton refusé ou expiré, dépôt introuvable, réseau).",
              "github.can_connect", ("host",), "{{host}} : l'API de GitHub n'est plus lue.", "{{host}} : l'API de GitHub est de nouveau lue."),
        check("github_workflow_failed", CI, "Workflow GitHub Actions en échec",
              "La dernière exécution d'un workflow sur la branche par défaut a échoué ou dépassé son délai (CRIT), "
              "ou a été annulée (WARN).", "github.workflow.status", ("repo", "workflow"),
              "{{tags.repo}} : le workflow {{tags.workflow}} est en échec.", "{{tags.repo}} : le workflow {{tags.workflow}} a de nouveau réussi.",
              priority=3),
        check("github_security", CI, "Alertes de sécurité GitHub",
              "Alertes ouvertes de Dependabot ou du code scanning critiques, ou secret exposé (CRIT) ; de gravité haute (WARN). "
              "Demande un jeton aux droits de lecture des alertes.", "github.security", ("repo",),
              "{{tags.repo}} : alertes de sécurité ouvertes.", "{{tags.repo}} : plus d'alerte de sécurité critique ni haute."),
        check("github_rate_limit", CI, "Quota de l'API GitHub", "Moins de 10 % du quota horaire de l'API (WARN), quota épuisé (CRIT).",
              "github.rate_limit", ("host",), "{{host}} : quota de l'API GitHub presque épuisé.", "{{host}} : quota de l'API GitHub rétabli.",
              priority=4),
        {"id": "github_runs_queued", "family": CI, "name": "Exécutions GitHub Actions en file",
         "description": "La plus ancienne exécution en file attend un exécuteur depuis trop longtemps (exécuteurs auto-hébergés "
                        "arrêtés, minutes épuisées), en secondes, sur 15 minutes.",
         "vars": [SCOPE, _v("critical", "Attente critique (s)", default=1800), _v("warning", "Attente d'avertissement (s)", default=600)],
         "build": lambda v: _metric("Exécutions GitHub Actions en file", f"max:github.runs.queued.oldest_age{{{v['scope']}}} by {{repo}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=3,
                                    message=two("{{tags.repo}} : une exécution attend un exécuteur depuis {{value}} s.",
                                                "{{tags.repo}} : plus d'exécution en attente prolongée."))},
        # ── Zot ──────────────────────────────────────────────────────────────────────────
        {"id": "zot_down", "family": REG, "name": "Zot injoignable",
         "description": "Le point /metrics du registre Zot ne répond plus (registre arrêté, extension metrics coupée).",
         "vars": [], "build": zot_down},
        {"id": "zot_scheduler_backlog", "family": REG, "name": "Tâches en retard dans Zot",
         "description": "Tâches de fond de Zot (nettoyage, vérification, synchronisation) en file pendant 15 minutes.",
         "vars": [SCOPE, _v("critical", "Tâches en file (critique)", default=100), _v("warning", "Tâches en file (avertissement)", default=20)],
         "build": lambda v: _metric("Tâches en retard dans Zot", f"sum:zot.scheduler.queue{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=4,
                                    message=two("{{host}} : {{value}} tâches de fond en file dans Zot.",
                                                "{{host}} : file des tâches de Zot revenue à {{value}}."))},
        {"id": "zot_lock_latency", "family": REG, "name": "Verrous du stockage Zot lents",
         "description": "Attente des verrous du stockage de Zot (95e centile, secondes) : disque lent ou nettoyage concurrent.",
         "vars": [SCOPE, _v("critical", "Seuil critique (s)", default=5), _v("warning", "Seuil d'avertissement (s)", default=1)],
         "build": lambda v: _metric("Verrous du stockage Zot lents", f"max:zot.storage.lock_latency.p95{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=600, aggregation="avg", priority=3,
                                    message=two("{{host}} : verrous du stockage Zot à {{value}} s (95e centile).",
                                                "{{host}} : verrous du stockage Zot revenus à {{value}} s."))},
        # ── Odoo ─────────────────────────────────────────────────────────────────────────
        check("odoo_down", APP, "Odoo injoignable", "Odoo ne répond plus sur /web/health (service arrêté, mandataire coupé).",
              "odoo.can_connect", ("host", "instance"), "{{host}} : Odoo injoignable.", "{{host}} : Odoo de nouveau joignable.", priority=1),
        check("odoo_health", APP, "Odoo en défaut", "/web/health rend « fail » : le serveur PostgreSQL d'Odoo ne répond plus (Odoo 18 et suivants).",
              "odoo.health", ("host", "instance"), "{{host}} : Odoo en défaut, base injoignable.", "{{host}} : Odoo de nouveau sain.", priority=1),
        {"id": "odoo_slow", "family": APP, "name": "Odoo lent",
         "description": "Temps de réponse de /web/health (millisecondes, moyenne sur 10 minutes) : workers tous occupés, machine saturée.",
         "vars": [SCOPE, _v("critical", "Seuil critique (ms)", default=2000), _v("warning", "Seuil d'avertissement (ms)", default=500)],
         "build": lambda v: _metric("Odoo lent", f"avg:odoo.response_time{{{scoped(v, 'endpoint:health')}}} by {{host,instance}}", ">",
                                    v["critical"], v["warning"], window_s=600, aggregation="avg", priority=3,
                                    message=two("{{host}} : Odoo répond en {{value}} ms.", "{{host}} : Odoo répond de nouveau en {{value}} ms."))},
        # ── WSUS (coupé par défaut dans l'agent) ─────────────────────────────────────────
        check("wsus_down", WIN, "WSUS injoignable", "Le service WsusService est arrêté ou l'API d'administration de WSUS ne répond plus.",
              "wsus.can_connect", ("host",), "{{host}} : WSUS injoignable.", "{{host}} : WSUS de nouveau joignable.", priority=2),
        check("wsus_sync", WIN, "Synchronisation WSUS en échec",
              "Dernière synchronisation avec le serveur amont en échec (CRIT), ou aucune réussie depuis sync_max_age (WARN).",
              "wsus.sync", ("host",), "{{host}} : la synchronisation de WSUS échoue.", "{{host}} : synchronisation de WSUS réussie.", priority=3),
        check("wsus_service", WIN, "Service du rôle WSUS arrêté", "WsusService, IIS (W3SVC) ou la base interne (WID) arrêté.",
              "wsus.service", ("host", "windows_service"), "{{host}} : service {{tags.windows_service}} arrêté.",
              "{{host}} : service {{tags.windows_service}} de nouveau en marche.", priority=2),
        check("wsus_app_pool", WIN, "Pool d'applications WsusPool arrêté",
              "Le pool IIS de WSUS s'est arrêté (souvent après un dépassement de sa limite de mémoire) : clients et console coupés.",
              "wsus.app_pool", ("host",), "{{host}} : pool WsusPool arrêté.", "{{host}} : pool WsusPool redémarré.", priority=2),
        {"id": "wsus_computers_errors", "family": WIN, "name": "Ordinateurs en défaut dans WSUS",
         "description": "Ordinateurs qui signalent des mises à jour en erreur, pendant une heure.",
         "vars": [SCOPE, _v("critical", "Ordinateurs en défaut (critique)", default=10), _v("warning", "Ordinateurs en défaut (avertissement)", default=1)],
         "build": lambda v: _metric("Ordinateurs en défaut dans WSUS", f"max:wsus.computers.with_errors{{{v['scope']}}} by {{host}}", ">=",
                                    v["critical"], v["warning"], window_s=3600, aggregation="min", priority=4,
                                    message=two("{{host}} : {{value}} ordinateur(s) en défaut de mise à jour dans WSUS.",
                                                "{{host}} : ordinateurs en défaut revenus à {{value}}."))},
        {"id": "wsus_content_free", "family": WIN, "name": "Place libre du contenu WSUS",
         "description": "Place libre du volume du dossier de contenu de WSUS (Gio).",
         "vars": [SCOPE, _v("critical", "Seuil critique (Gio)", default=10), _v("warning", "Seuil d'avertissement (Gio)", default=25)],
         "build": lambda v: _metric("Place libre du contenu WSUS", f"min:wsus.content.free{{{v['scope']}}} by {{host}}", "<",
                                    v["critical"] * GIB, v["warning"] * GIB, window_s=900, aggregation="max", priority=3,
                                    message=two("{{host}} : le volume du contenu WSUS manque de place.",
                                                "{{host}} : place libre du contenu WSUS rétablie."))},
    ]
