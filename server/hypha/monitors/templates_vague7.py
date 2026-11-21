"""Modèles de moniteurs des applications auto-hébergées (vague 7 des intégrations).

Ajoutés en fin de catalogue par `templates.py` ; chaque contrôle ou métrique visé est publié par
l'agent (sondes de `integrations/vague7_*.go`, profils de `omprofiles/vague7.go`), vérifié par
`tests/test_vague7.py`. Les fonctions de construction viennent de `templates.py` en paramètres.
"""
import re

_INSTANCE_RE = re.compile(r"^[a-z0-9][a-z0-9_\-]{0,31}$")
GIB = 1024 ** 3


def vague7(_v, _metric, _check, two, SCOPE):
    FAM = "Applications auto-hébergées"

    def tagged(scope, tag):
        return tag if scope == "*" else f"{scope},{tag}"

    def down(tid, name, product, check, by=("host", "instance_name"), desc=None, priority=1):
        return {"id": tid, "family": FAM, "name": name,
                "description": desc or f"L'agent n'arrive plus à lire {product}.",
                "vars": [], "build": lambda v: _check(name, check, list(by), priority=priority,
                                                       message=two("{{host}} : " + product + " injoignable.",
                                                                   "{{host}} : " + product + " de nouveau joignable."))}

    def prom_down(tid, name, product, default, hint):
        """Cible OpenMetrics d'une application injoignable, filtrée sur son instance_name."""
        def build(v):
            inst = v["profile"]
            if not isinstance(inst, str) or not _INSTANCE_RE.match(inst):
                raise ValueError("la variable profile vaut un nom d'instance (minuscules, chiffres, - et _)")
            d = _check(f"{name} : {inst}", "openmetrics.can_connect", ["host", "instance_name"], priority=1,
                       message=two("{{host}} : " + product + " ({{tags.endpoint}}) ne répond plus.",
                                   "{{host}} : " + product + " répond de nouveau."))
            d["query"]["tags"] = {"instance_name": inst}
            return d
        return {"id": tid, "family": FAM, "name": name,
                "description": f"La cible OpenMetrics de {product} ne répond plus (service arrêté, jeton refusé, mesures coupées).",
                "vars": [_v("profile", f"Instance ({hint})", "text", default)], "build": build}

    def msg(alert, ok):
        return two(alert, ok)

    return [
        # ── Nextcloud ────────────────────────────────────────────────────────────────────
        down("nextcloud_down", "Nextcloud injoignable", "Nextcloud", "nextcloud.can_connect",
             desc="status.php ne répond plus (serveur web arrêté, PHP en panne, certificat refusé)."),
        {"id": "nextcloud_status", "family": FAM, "name": "Nextcloud hors service",
         "description": "Nextcloud en mode maintenance, en attente de la mise à niveau de sa base (occ upgrade) ou non installé.",
         "vars": [], "build": lambda v: _check("Nextcloud hors service", "nextcloud.status", ["host", "instance_name"], priority=1,
                                               message=two("{{host}} : Nextcloud hors service (mode maintenance, mise à niveau de la base en attente ou installation inachevée).",
                                                           "{{host}} : Nextcloud de nouveau en service."))},
        {"id": "nextcloud_storage_free", "family": FAM, "name": "Place libre de Nextcloud",
         "description": "Place libre du dossier de données de Nextcloud (serverinfo) sous le seuil pendant 15 minutes.",
         "vars": [SCOPE, _v("critical", "Place libre (Gio, critique)", default=5), _v("warning", "Place libre (Gio, avertissement)", default=20)],
         "build": lambda v: _metric("Place libre de Nextcloud", f"min:nextcloud.storage.free{{{v['scope']}}} by {{host,instance_name}}", "<",
                                    v["critical"] * GIB, v["warning"] * GIB, window_s=900, aggregation="max", priority=2,
                                    message=msg("{{host}} : il ne reste que {{value}} octets libres pour Nextcloud.",
                                                "{{host}} : place libre de Nextcloud revenue à {{value}} octets."))},
        {"id": "nextcloud_opcache_full", "family": FAM, "name": "OPcache de Nextcloud plein",
         "description": "La mémoire OPcache de PHP est pleine : les scripts sont recompilés à chaque requête (opcache.memory_consumption à relever).",
         "vars": [SCOPE],
         "build": lambda v: _metric("OPcache de Nextcloud plein", f"max:nextcloud.php.opcache.full{{{v['scope']}}} by {{host}}", ">=", 1,
                                    window_s=900, aggregation="min", priority=4,
                                    message=msg("{{host}} : OPcache plein, PHP recompile les scripts de Nextcloud.",
                                                "{{host}} : OPcache de nouveau en dessous de sa limite."))},
        # ── Jellyfin ─────────────────────────────────────────────────────────────────────
        down("jellyfin_down", "Jellyfin injoignable", "Jellyfin", "jellyfin.can_connect",
             desc="/health de Jellyfin ne répond plus (serveur arrêté)."),
        {"id": "jellyfin_health", "family": FAM, "name": "Jellyfin en mauvaise santé",
         "description": "/health rend Degraded (avertissement) ou Unhealthy (critique).",
         "vars": [], "build": lambda v: _check("Jellyfin en mauvaise santé", "jellyfin.health", ["host", "instance_name"], priority=2,
                                               message=two("{{host}} : Jellyfin dégradé ou en mauvaise santé (/health).", "{{host}} : Jellyfin de nouveau en bonne santé."))},
        {"id": "jellyfin_tasks_failed", "family": FAM, "name": "Tâches planifiées de Jellyfin en échec",
         "description": "La dernière exécution d'une tâche planifiée (analyse des bibliothèques, nettoyage…) a échoué.",
         "vars": [], "build": lambda v: _check("Tâches planifiées de Jellyfin en échec", "jellyfin.scheduled_tasks", ["host", "instance_name"],
                                               priority=4, message=two("{{host}} : une tâche planifiée de Jellyfin a échoué à sa dernière exécution.",
                                                                       "{{host}} : tâches planifiées de Jellyfin de nouveau réussies."))},
        {"id": "jellyfin_transcoding", "family": FAM, "name": "Transcodages Jellyfin simultanés",
         "description": "Nombre de lectures transcodées en même temps au-dessus du seuil pendant 10 minutes (processeur ou GPU saturé).",
         "vars": [SCOPE, _v("critical", "Transcodages (critique)", default=4), _v("warning", "Transcodages (avertissement)", default=2)],
         "build": lambda v: _metric("Transcodages Jellyfin simultanés", f"max:jellyfin.sessions.transcoding{{{v['scope']}}} by {{host}}", ">=",
                                    v["critical"], v["warning"], window_s=600, aggregation="min", priority=4,
                                    message=msg("{{host}} : {{value}} lectures transcodées en même temps.",
                                                "{{host}} : transcodages revenus à {{value}}."))},
        # ── Syncthing ────────────────────────────────────────────────────────────────────
        down("syncthing_down", "Syncthing injoignable", "Syncthing", "syncthing.can_connect",
             desc="/rest/noauth/health de Syncthing ne répond plus (Syncthing arrêté)."),
        {"id": "syncthing_folder", "family": FAM, "name": "Dossier Syncthing en défaut",
         "description": "Dossier en erreur (marqueur .stfolder absent, disque, droits : critique) ou fichiers impossibles à recevoir (avertissement).",
         "vars": [], "build": lambda v: _check("Dossier Syncthing en défaut", "syncthing.folder", ["host", "instance_name", "folder"], priority=2,
                                               message=two("{{host}} : dossier Syncthing {{tags.folder_label}} en défaut.",
                                                           "{{host}} : dossier {{tags.folder_label}} de nouveau synchronisé."))},
        {"id": "syncthing_device_disconnected", "family": FAM, "name": "Appareil Syncthing déconnecté",
         "description": "Un appareil distant (hors pause) n'est plus connecté.",
         "vars": [], "build": lambda v: _check("Appareil Syncthing déconnecté", "syncthing.device", ["host", "instance_name", "device"],
                                               priority=4, message=two("{{host}} : appareil Syncthing {{tags.device}} déconnecté.",
                                                                       "{{host}} : appareil {{tags.device}} de nouveau connecté."))},
        {"id": "syncthing_device_absent", "family": FAM, "name": "Appareil Syncthing absent depuis longtemps",
         "description": "Un appareil distant n'a plus été vu depuis plus de N heures (sauvegarde ou poste qui ne synchronise plus).",
         "vars": [SCOPE, _v("critical", "Heures sans nouvelles (critique)", default=72), _v("warning", "Heures sans nouvelles (avertissement)", default=24)],
         "build": lambda v: _metric("Appareil Syncthing absent depuis longtemps",
                                    f"max:syncthing.device.last_seen_age{{{v['scope']}}} by {{host,device}}", ">",
                                    v["critical"] * 3600, v["warning"] * 3600, window_s=900, aggregation="min", priority=3,
                                    message=msg("{{host}} : l'appareil {{tags.device}} n'a plus été vu depuis {{value}} s.",
                                                "{{host}} : l'appareil {{tags.device}} est revenu."))},
        {"id": "syncthing_out_of_sync", "family": FAM, "name": "Dossier Syncthing en retard",
         "description": "Des fichiers restent à recevoir dans un dossier depuis plus d'une heure.",
         "vars": [SCOPE],
         "build": lambda v: _metric("Dossier Syncthing en retard", f"max:syncthing.folder.need_files{{{v['scope']}}} by {{host,folder}}", ">", 0,
                                    window_s=3600, aggregation="min", priority=4,
                                    message=msg("{{host}} : {{value}} fichiers attendent dans le dossier {{tags.folder}} depuis une heure.",
                                                "{{host}} : dossier {{tags.folder}} de nouveau à jour."))},
        # ── Immich ───────────────────────────────────────────────────────────────────────
        down("immich_down", "Immich injoignable", "Immich", "immich.can_connect",
             desc="/api/server/ping d'Immich ne répond plus (serveur arrêté, base ou Redis injoignables au démarrage)."),
        {"id": "immich_queues", "family": FAM, "name": "Files de tâches d'Immich bloquées",
         "description": "Une file de tâches d'Immich (miniatures, métadonnées, vidéos…) est en pause ou porte des tâches en échec.",
         "vars": [], "build": lambda v: _check("Files de tâches d'Immich bloquées", "immich.queues", ["host", "instance_name"], priority=3,
                                               message=two("{{host}} : une file de tâches d'Immich est en pause ou porte des tâches en échec.",
                                                           "{{host}} : files de tâches d'Immich de nouveau normales."))},
        {"id": "immich_storage", "family": FAM, "name": "Disque de la photothèque Immich",
         "description": "Occupation du disque de la photothèque (/api/server/storage) au-dessus du seuil pendant 15 minutes.",
         "vars": [SCOPE, _v("critical", "Seuil critique (%)", default=90), _v("warning", "Seuil d'avertissement (%)", default=80)],
         "build": lambda v: _metric("Disque de la photothèque Immich", f"max:immich.storage.usage{{{v['scope']}}} by {{host,instance_name}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=2,
                                    message=msg("{{host}} : disque de la photothèque Immich occupé à {{value}} %.",
                                                "{{host}} : disque de la photothèque Immich revenu à {{value}} %."))},
        {"id": "immich_backlog", "family": FAM, "name": "Tâches d'Immich en attente",
         "description": "Tâches en attente dans une file d'Immich au-dessus du seuil pendant une heure (import massif ou traitement arrêté).",
         "vars": [SCOPE, _v("critical", "Tâches en attente (critique)", default=5000), _v("warning", "Tâches en attente (avertissement)", default=500)],
         "build": lambda v: _metric("Tâches d'Immich en attente", f"max:immich.jobs{{{tagged(v['scope'], 'state:waiting')}}} by {{host,queue}}", ">",
                                    v["critical"], v["warning"], window_s=3600, aggregation="min", priority=4,
                                    message=msg("{{host}} : {{value}} tâches attendent dans la file {{tags.queue}} d'Immich.",
                                                "{{host}} : file {{tags.queue}} d'Immich revenue à {{value}} tâches."))},
        # ── Gitea et Forgejo ─────────────────────────────────────────────────────────────
        prom_down("gitea_down", "Gitea ou Forgejo injoignable", "la forge", "gitea", "gitea, forgejo"),
        # ── Matrix Synapse ───────────────────────────────────────────────────────────────
        prom_down("synapse_down", "Synapse injoignable", "Synapse", "synapse", "synapse"),
        {"id": "synapse_event_lag", "family": FAM, "name": "Retard du traitement des événements Synapse",
         "description": "Retard d'un traitement d'événements de Synapse (envoi fédéré, appservices, poussées) au-dessus du seuil pendant 10 minutes.",
         "vars": [SCOPE, _v("critical", "Retard (s, critique)", default=60), _v("warning", "Retard (s, avertissement)", default=10)],
         "build": lambda v: _metric("Retard du traitement des événements Synapse", f"max:synapse.event_processing.lag{{{v['scope']}}} by {{host,name}}",
                                    ">", v["critical"] * 1000, v["warning"] * 1000, window_s=600, aggregation="min", priority=3,
                                    message=msg("{{host}} : le traitement {{tags.name}} de Synapse a {{value}} ms de retard.",
                                                "{{host}} : traitement {{tags.name}} de Synapse revenu à {{value}} ms."))},
        {"id": "synapse_federation_backlog", "family": FAM, "name": "File de fédération Synapse",
         "description": "Événements (PDU) en attente d'envoi vers d'autres serveurs Matrix au-dessus du seuil pendant 15 minutes.",
         "vars": [SCOPE, _v("critical", "PDU en attente (critique)", default=1000), _v("warning", "PDU en attente (avertissement)", default=100)],
         "build": lambda v: _metric("File de fédération Synapse", f"max:synapse.federation.pending_pdus{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=3,
                                    message=msg("{{host}} : {{value}} événements attendent d'être fédérés.",
                                                "{{host}} : file de fédération revenue à {{value}}."))},
        {"id": "synapse_slow_requests", "family": FAM, "name": "Requêtes Synapse lentes",
         "description": "95e centile du temps de réponse de Synapse au-dessus du seuil pendant 10 minutes.",
         "vars": [SCOPE, _v("critical", "Temps (s, critique)", default=5), _v("warning", "Temps (s, avertissement)", default=2)],
         "build": lambda v: _metric("Requêtes Synapse lentes", f"max:synapse.http.response_time.p95{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=600, aggregation="min", priority=3,
                                    message=msg("{{host}} : 95 % des requêtes Synapse répondent en moins de {{value}} s.",
                                                "{{host}} : temps de réponse de Synapse revenu à {{value}} s."))},
        # ── Home Assistant ───────────────────────────────────────────────────────────────
        prom_down("homeassistant_down", "Home Assistant injoignable", "Home Assistant", "homeassistant", "homeassistant"),
        {"id": "homeassistant_entity_unavailable", "family": FAM, "name": "Entité Home Assistant indisponible",
         "description": "Une entité (capteur, interrupteur…) est indisponible depuis 30 minutes (pile vide, appareil hors de portée).",
         "vars": [SCOPE],
         "build": lambda v: _metric("Entité Home Assistant indisponible", f"max:homeassistant.entity.available{{{v['scope']}}} by {{host,entity}}",
                                    "<", 1, window_s=1800, aggregation="max", priority=4,
                                    message=msg("{{host}} : entité {{tags.entity}} indisponible depuis 30 minutes.",
                                                "{{host}} : entité {{tags.entity}} de nouveau disponible."))},
        {"id": "homeassistant_battery_low", "family": FAM, "name": "Pile faible (Home Assistant)",
         "description": "Niveau de pile d'un capteur sous le seuil pendant une heure.",
         "vars": [SCOPE, _v("critical", "Niveau (%, critique)", default=10), _v("warning", "Niveau (%, avertissement)", default=25)],
         "build": lambda v: _metric("Pile faible (Home Assistant)", f"min:homeassistant.sensor.battery{{{v['scope']}}} by {{host,entity}}", "<",
                                    v["critical"], v["warning"], window_s=3600, aggregation="max", priority=4,
                                    message=msg("{{host}} : pile de {{tags.entity}} à {{value}} %.",
                                                "{{host}} : pile de {{tags.entity}} revenue à {{value}} %."))},
        {"id": "homeassistant_update", "family": FAM, "name": "Mise à jour disponible (Home Assistant)",
         "description": "Une entité de mise à jour (Home Assistant, modules complémentaires, micrologiciels) annonce une nouvelle version.",
         "vars": [SCOPE],
         "build": lambda v: _metric("Mise à jour disponible (Home Assistant)", f"max:homeassistant.update.available{{{v['scope']}}} by {{host,entity}}",
                                    ">=", 1, window_s=3600, aggregation="min", priority=5,
                                    message=msg("{{host}} : mise à jour disponible ({{tags.entity}}).",
                                                "{{host}} : {{tags.entity}} à jour."))},
        # ── Mastodon ─────────────────────────────────────────────────────────────────────
        prom_down("mastodon_down", "Mastodon injoignable", "Mastodon", "mastodon", "mastodon, mastodon-web, mastodon-sidekiq, mastodon_streaming"),
        {"id": "mastodon_sidekiq_latency", "family": FAM, "name": "Files Sidekiq de Mastodon en retard",
         "description": "Âge de la plus vieille tâche d'une file Sidekiq au-dessus du seuil pendant 10 minutes (fédération, notifications en retard ; "
                        "MASTODON_PROMETHEUS_EXPORTER_SIDEKIQ_DETAILED_METRICS=true).",
         "vars": [SCOPE, _v("critical", "Retard (s, critique)", default=300), _v("warning", "Retard (s, avertissement)", default=60)],
         "build": lambda v: _metric("Files Sidekiq de Mastodon en retard", f"max:mastodon.sidekiq.queue.latency{{{v['scope']}}} by {{host,queue}}", ">",
                                    v["critical"], v["warning"], window_s=600, aggregation="min", priority=2,
                                    message=msg("{{host}} : la file {{tags.queue}} de Mastodon a {{value}} s de retard.",
                                                "{{host}} : file {{tags.queue}} de Mastodon revenue à {{value}} s."))},
        {"id": "mastodon_sidekiq_retry", "family": FAM, "name": "Tâches Sidekiq de Mastodon à relancer",
         "description": "Tâches en attente de nouvel essai (échecs répétés : serveur distant injoignable, base saturée) au-dessus du seuil.",
         "vars": [SCOPE, _v("critical", "Tâches (critique)", default=1000), _v("warning", "Tâches (avertissement)", default=200)],
         "build": lambda v: _metric("Tâches Sidekiq de Mastodon à relancer", f"max:mastodon.sidekiq.stats.retry{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=900, aggregation="min", priority=3,
                                    message=msg("{{host}} : {{value}} tâches Sidekiq attendent un nouvel essai.",
                                                "{{host}} : tâches à relancer revenues à {{value}}."))},
        {"id": "mastodon_puma_backlog", "family": FAM, "name": "Requêtes Mastodon en attente dans Puma",
         "description": "Requêtes en attente d'un fil de Puma pendant 10 minutes : le web de Mastodon n'absorbe plus la charge.",
         "vars": [SCOPE, _v("critical", "Requêtes en attente (critique)", default=20), _v("warning", "Requêtes en attente (avertissement)", default=5)],
         "build": lambda v: _metric("Requêtes Mastodon en attente dans Puma", f"max:mastodon.puma.request_backlog{{{v['scope']}}} by {{host}}", ">",
                                    v["critical"], v["warning"], window_s=600, aggregation="min", priority=3,
                                    message=msg("{{host}} : {{value}} requêtes attendent un fil de Puma.",
                                                "{{host}} : file de Puma revenue à {{value}}."))},
    ]
