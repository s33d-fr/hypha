"""Tableaux livrés d'office à une organisation (contrat § 7) : « Vue système » et « Vue réseau ».

Les requêtes reprennent les métriques de l'agent (contrat § 4) ; `$host` vient de la variable de
modèle et disparaît quand elle vaut `*`.
"""
HOST_VAR = [{"name": "host", "tag": "host", "default": "*"}]


def _ts(wid, title, x, y, queries, unit=None, w=6, h=4, display="line", **options):
    opts = {"unit": unit, "precision": 1, **options} if unit else {"precision": 1, **options}
    return {"id": wid, "type": "timeseries", "title": title, "x": x, "y": y, "w": w, "h": h,
            "queries": [{"q": q, "display": display} for q in queries], "options": opts}


SYSTEM = {
    "title": "Vue système",
    "description": "Processeur, mémoire, charge, disques et contrôles de chaque hôte.",
    "template_vars": HOST_VAR,
    "widgets": [
        {"id": "w1", "type": "host_map", "title": "Carte des hôtes (CPU)", "x": 0, "y": 0, "w": 6, "h": 4,
         "queries": [{"q": "avg:system.cpu.usage{$host} by {host}"}],
         "options": {"unit": "percent", "precision": 0,
                     "thresholds": [{"value": 80, "color": "warn"}, {"value": 95, "color": "error"}]}},
        {"id": "w2", "type": "check_status", "title": "Contrôles", "x": 6, "y": 0, "w": 3, "h": 4,
         "queries": [], "options": {}},
        {"id": "w3", "type": "monitor_summary", "title": "Moniteurs", "x": 9, "y": 0, "w": 3, "h": 4,
         "queries": [], "options": {}},
        _ts("w4", "CPU utilisé (%)", 0, 4, ["avg:system.cpu.usage{$host} by {host}"], "percent",
            thresholds=[{"value": 80, "color": "warn"}]),
        _ts("w5", "Mémoire utilisée (%)", 6, 4, ["avg:system.mem.pct_used{$host} by {host}"], "percent",
            thresholds=[{"value": 90, "color": "warn"}]),
        _ts("w6", "Charge normalisée (÷ cœurs)", 0, 8, ["avg:system.load.norm.1{$host} by {host}"],
            thresholds=[{"value": 1, "color": "warn"}]),
        {"id": "w7", "type": "toplist", "title": "Disques les plus pleins (%)", "x": 6, "y": 8, "w": 6, "h": 4,
         "queries": [{"q": "max:system.disk.in_use{$host} by {host,mount}"}],
         "options": {"unit": "percent", "precision": 0,
                     "thresholds": [{"value": 80, "color": "warn"}, {"value": 90, "color": "error"}]}},
        _ts("w8", "Lectures disque (octets/s)", 0, 12, ["sum:system.io.read_bytes{$host} by {host}"],
            "byte_per_second", display="area"),
        _ts("w9", "Écritures disque (octets/s)", 6, 12, ["sum:system.io.write_bytes{$host} by {host}"],
            "byte_per_second", display="area"),
        _ts("w10", "Swap utilisé (%)", 0, 16, ["avg:system.swap.pct_used{$host} by {host}"], "percent"),
        _ts("w11", "Processus", 6, 16, ["sum:system.processes.count{$host} by {host}"], "count", precision=0),
        {"id": "w12", "type": "log_stream", "title": "Erreurs récentes", "x": 0, "y": 20, "w": 12, "h": 5,
         "queries": [], "options": {"log_query": "status:error OR status:critical OR status:alert OR status:emergency"}},
    ],
}

NETWORK = {
    "title": "Vue réseau",
    "description": "Débits, paquets, erreurs et connexions TCP des interfaces de chaque hôte.",
    "template_vars": HOST_VAR,
    "widgets": [
        _ts("w1", "Reçu (octets/s)", 0, 0, ["sum:system.net.bytes_rcvd{$host} by {host}"], "byte_per_second",
            display="area"),
        _ts("w2", "Émis (octets/s)", 6, 0, ["sum:system.net.bytes_sent{$host} by {host}"], "byte_per_second",
            display="area"),
        _ts("w3", "Paquets reçus et émis (paquets/s)", 0, 4,
            ["sum:system.net.packets_in{$host} by {host}", "sum:system.net.packets_out{$host} by {host}"],
            "packet_per_second"),
        _ts("w4", "Erreurs et pertes (paquets/s)", 6, 4,
            ["sum:system.net.errors_in{$host} by {host}", "sum:system.net.errors_out{$host} by {host}",
             "sum:system.net.drops_in{$host} by {host}"], "packet_per_second", display="bars"),
        _ts("w5", "Connexions TCP par état", 0, 8, ["sum:system.net.tcp.connections{$host} by {state}"], "count",
            display="area", precision=0),
        {"id": "w6", "type": "toplist", "title": "Interfaces les plus chargées (reçu, octets/s)", "x": 6, "y": 8,
         "w": 6, "h": 4, "queries": [{"q": "max:system.net.bytes_rcvd{$host} by {host,interface}"}],
         "options": {"unit": "byte_per_second", "precision": 0}},
        {"id": "w7", "type": "toplist", "title": "Interfaces les plus chargées (émis, octets/s)", "x": 0, "y": 12,
         "w": 6, "h": 4, "queries": [{"q": "max:system.net.bytes_sent{$host} by {host,interface}"}],
         "options": {"unit": "byte_per_second", "precision": 0}},
        {"id": "w8", "type": "query_value", "title": "Connexions TCP (total)", "x": 6, "y": 12, "w": 6, "h": 4,
         "queries": [{"q": "sum:system.net.tcp.connections{$host}"}], "options": {"unit": "count", "precision": 0}},
    ],
}

DEFAULTS = (SYSTEM, NETWORK)
