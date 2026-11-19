#!/usr/bin/env python3
"""Moniteurs, test synthétique, tableau, SLO, page de statut et incident de l'espace de démonstration.

    DEMO_APP=hph_… python3 demo-objets.py creer        # une fois, après demo-donnees.py remplir
    DEMO_APP=hph_… python3 demo-objets.py nommer en    # noms en anglais avant le tournage anglais (ou fr)

Clé `app` de l'espace de démonstration (rôle admin). Identifiants gardés dans demo-objets.json.
"""
import json
import os
import sys
import urllib.error
import urllib.request

URL = os.environ.get("DEMO_URL", "https://hypha.s33d.fr").rstrip("/") + "/api/v1"
KEY = os.environ.get("DEMO_APP", "")
FICHIER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "demo-objets.json")

MONITEURS = [
    ("host_down", {"tag": "", "no_data_s": 300}, "Hôte sans nouvelles", "Host not reporting"),
    ("cpu_high", {}, "Processeur saturé", "CPU saturated"),
    ("memory_high", {}, "Mémoire saturée", "Memory saturated"),
    ("disk_full", {}, "Disque presque plein", "Disk almost full"),
    ("systemd_unit", {}, "Unité systemd en échec", "systemd unit failed"),
    ("windows_service", {}, "Service Windows arrêté", "Windows service stopped"),
    ("cert_expiry", {}, "Certificat proche de la fin", "Certificate close to expiry"),
    ("ssh_bruteforce", {}, "Attaque SSH par force brute", "SSH brute-force attack"),
    ("http_5xx", {"service": "shop-api", "critical": 2}, "Erreurs HTTP 5xx : shop-api", "HTTP 5xx errors: shop-api"),
    ("latency_p95", {"service": "shop-api", "critical": 1000, "warning": 500}, "Latence p95 : shop-api", "p95 latency: shop-api"),
    ("new_error", {"service": "shop-api"}, "Nouvelle erreur applicative : shop-api", "New application error: shop-api"),
]
NOMS = {
    "synth": ("Page d'accueil", "Home page"),
    "dash": ("Boutique en ligne", "Online shop"),
    "slo": ("Disponibilité de la commande", "Checkout availability"),
    "page": ("Boutique (démo)", "Shop (demo)"),
    "page_desc": ("État de la boutique en ligne de démonstration.", "Status of the demo online shop."),
    "comp": {"front": ("Boutique en ligne", "Online shop"), "pay": ("Commande et paiement", "Checkout and payment"),
             "home": ("Page d'accueil", "Home page"), "srv": ("Serveurs d'application", "Application servers")},
    "group": ("Boutique", "Shop"),
    "inc": ("Lenteurs sur la boutique", "Slow responses on the shop"),
    "inc_msg": ("Certaines pages de la boutique répondent lentement. L'équipe est sur le sujet.",
                "Some shop pages respond slowly. The team is on it."),
    "dash_sys": ("Vue système", "System overview"),
    "dash_net": ("Vue réseau", "Network overview"),
}


def api(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(URL + path, data=data, method=method, headers={
        "Content-Type": "application/json", "X-Hypha-App-Key": KEY, "User-Agent": "hypha-demo-objets"})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            raw = r.read()
            return json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        raise SystemExit(f"{method} {path} : {e.code} {e.read()[:400]!r}")


def lang_idx(lang):
    return 0 if lang == "fr" else 1


def dashboard(title):
    q = lambda s, d="line": {"q": s, "display": d}
    return {"title": title, "description": "", "template_vars": [{"name": "host", "tag": "host", "default": "*"}],
            "widgets": [
                {"id": "w1", "type": "query_value", "title": "Requêtes / s", "x": 0, "y": 0, "w": 3, "h": 2,
                 "queries": [q("sum:nginx.requests{$host}")], "options": {"precision": 0}},
                {"id": "w2", "type": "query_value", "title": "CPU max (%)", "x": 3, "y": 0, "w": 3, "h": 2,
                 "queries": [q("max:system.cpu.usage{$host}")],
                 "options": {"unit": "percent", "precision": 0, "thresholds": [{"value": 80, "color": "warn"}, {"value": 90, "color": "crit"}]}},
                {"id": "w3", "type": "monitor_summary", "title": "Moniteurs", "x": 6, "y": 0, "w": 6, "h": 2, "queries": [], "options": {}},
                {"id": "w4", "type": "timeseries", "title": "CPU par hôte", "x": 0, "y": 2, "w": 6, "h": 4,
                 "queries": [q("avg:system.cpu.usage{$host} by {host}")], "options": {"unit": "percent"}},
                {"id": "w5", "type": "timeseries", "title": "Trafic nginx", "x": 6, "y": 2, "w": 6, "h": 4,
                 "queries": [q("sum:nginx.requests{$host} by {host}", "bars")], "options": {}},
                {"id": "w6", "type": "toplist", "title": "Disques les plus pleins", "x": 0, "y": 6, "w": 4, "h": 4,
                 "queries": [q("max:system.disk.in_use{*} by {host,mount}")], "options": {"unit": "percent"}},
                {"id": "w7", "type": "log_stream", "title": "Erreurs", "x": 4, "y": 6, "w": 8, "h": 4, "queries": [],
                 "options": {"log_query": "status:error OR status:critical"}},
            ]}


def creer():
    ids = {"monitors": {}}
    synth = api("POST", "/synthetics", {"name": NOMS["synth"][0], "kind": "http", "target": "https://example.com/",
                                        "interval_s": 300, "locations": ["server"], "config": {"expect_status": 200}})
    ids["synth"] = synth["id"]
    for tid, vars_, _, _ in MONITEURS:
        m = api("POST", "/monitors/from-template", {"template": tid, "vars": vars_})
        ids["monitors"][tid] = m["id"]
    m = api("POST", "/monitors/from-template", {"template": "synthetic_failed", "vars": {"test_id": synth["id"], "failures": 2}})
    ids["monitors"]["synthetic_failed"] = m["id"]
    ids["dash"] = api("POST", "/dashboards", dashboard(NOMS["dash"][0]))["id"]
    ids["slo"] = api("POST", "/slos", {"name": NOMS["slo"][0], "type": "monitor", "monitor_id": ids["monitors"]["http_5xx"],
                                       "target": 99.5, "timeframe": "30d"})["id"]
    page = api("POST", "/status-pages", {"slug": "demo-boutique", "title": NOMS["page"][0], "description": NOMS["page_desc"][0],
                                          "public": True, "components": components(0, ids)})
    ids["page"] = page["id"]
    inc = api("POST", "/incidents", {"title": NOMS["inc"][0], "severity": 3, "status_page_ids": [page["id"]],
                                     "public_message": NOMS["inc_msg"][0]})
    ids["inc"] = inc["id"]
    api("POST", f"/incidents/{inc['id']}/timeline", {"kind": "public_update", "body": NOMS["inc_msg"][0]})
    with open(FICHIER, "w") as f:
        json.dump(ids, f)
    print(json.dumps(ids))


def components(i, ids):
    c, g, m = NOMS["comp"], NOMS["group"][i], ids["monitors"]
    return [{"name": c["front"][i], "group": g, "kind": "monitor", "ref_id": m["latency_p95"]},
            {"name": c["pay"][i], "group": g, "kind": "monitor", "ref_id": m["http_5xx"]},
            {"name": c["home"][i], "group": g, "kind": "synthetic", "ref_id": ids["synth"]},
            {"name": c["srv"][i], "group": g, "kind": "monitor", "ref_id": m["cpu_high"]}]


def nommer(lang):
    i = lang_idx(lang)
    with open(FICHIER) as f:
        ids = json.load(f)
    for tid, _, fr, en in MONITEURS:
        api("PUT", f"/monitors/{ids['monitors'][tid]}", {"name": (fr, en)[i]})
    api("PUT", f"/monitors/{ids['monitors']['synthetic_failed']}", {"name": ("Synthétique en échec", "Synthetic test failing")[i]})
    api("PUT", f"/synthetics/{ids['synth']}", {"name": NOMS["synth"][i]})
    api("PUT", f"/slos/{ids['slo']}", {"name": NOMS["slo"][i]})
    api("PUT", f"/status-pages/{ids['page']}", {"title": NOMS["page"][i], "description": NOMS["page_desc"][i],
                                               "components": components(i, ids)})
    api("PUT", f"/incidents/{ids['inc']}", {"title": NOMS["inc"][i], "public_message": NOMS["inc_msg"][i]})
    for d in api("GET", "/dashboards") or []:
        full = api("GET", f"/dashboards/{d['id']}")
        for key in ("dash", "dash_sys", "dash_net"):
            if full.get("title") in NOMS[key]:
                full["title"] = NOMS[key][i]
                api("PUT", f"/dashboards/{d['id']}", {k: full[k] for k in ("title", "description", "widgets", "template_vars") if k in full})
    print(f"noms : {lang}")


if __name__ == "__main__":
    if not KEY:
        raise SystemExit("DEMO_APP absente")
    if len(sys.argv) > 1 and sys.argv[1] == "creer":
        creer()
    elif len(sys.argv) > 2 and sys.argv[1] == "nommer" and sys.argv[2] in ("fr", "en"):
        nommer(sys.argv[2])
    else:
        raise SystemExit(__doc__)
