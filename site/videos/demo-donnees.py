#!/usr/bin/env python3
"""Données fictives de l'espace de démonstration (vidéos de la documentation).

Cinq hôtes inventés (adresses de documentation RFC 5737, noms génériques), une boutique en ligne
« shop-api » avec ses traces, ses journaux et une panne : le processeur de web-02 saturé par un
export lancé à la main. Rien ne vient du parc de S33D.

    DEMO_INGEST=hph_… python3 demo-donnees.py remplir 150   # 150 minutes d'historique
    DEMO_INGEST=hph_… python3 demo-donnees.py direct 90     # puis 90 minutes en direct

Bibliothèque standard seulement. État (début de la panne) dans demo-etat.json, à côté du script.
"""
import gzip
import json
import math
import os
import random
import sys
import time
import urllib.error
import urllib.request
import uuid

URL = os.environ.get("DEMO_URL", "https://hypha.s33d.fr").rstrip("/")
KEY = os.environ.get("DEMO_INGEST", "")
ETAT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "demo-etat.json")
STEP = 15
GIB = 1024 ** 3
NS = uuid.UUID("6f6d6f64-656d-6f2d-6879-706861000001")

HOSTS = [
    {"name": "web-01", "os": "linux", "platform": "ubuntu", "ver": "24.04", "kernel": "6.8.0-48-generic", "cores": 4,
     "mem": 8 * GIB, "disk": 80 * GIB, "ip": "198.51.100.11", "role": "web", "cpu": 24, "memp": 52, "diskp": 41},
    {"name": "web-02", "os": "linux", "platform": "ubuntu", "ver": "24.04", "kernel": "6.8.0-48-generic", "cores": 4,
     "mem": 8 * GIB, "disk": 80 * GIB, "ip": "198.51.100.12", "role": "web", "cpu": 27, "memp": 55, "diskp": 44},
    {"name": "db-01", "os": "linux", "platform": "debian", "ver": "12", "kernel": "6.1.0-27-amd64", "cores": 8,
     "mem": 32 * GIB, "disk": 500 * GIB, "ip": "198.51.100.21", "role": "db", "cpu": 31, "memp": 76, "diskp": 68},
    {"name": "cache-01", "os": "linux", "platform": "ubuntu", "ver": "24.04", "kernel": "6.8.0-48-generic", "cores": 2,
     "mem": 4 * GIB, "disk": 40 * GIB, "ip": "198.51.100.31", "role": "cache", "cpu": 9, "memp": 61, "diskp": 22},
    {"name": "win-app-01", "os": "windows", "platform": "Windows Server 2022 Datacenter", "ver": "10.0.20348",
     "kernel": "10.0.20348", "cores": 4, "mem": 16 * GIB, "disk": 127 * GIB, "ip": "198.51.100.41", "role": "app",
     "cpu": 17, "memp": 47, "diskp": 53},
]
for h in HOSTS:
    h["id"] = str(uuid.uuid5(NS, h["name"]))
BY = {h["name"]: h for h in HOSTS}
CLIENTS = [f"203.0.113.{i}" for i in (7, 18, 23, 34, 52, 61, 77, 89, 104, 131, 150, 177, 201, 230)]
PAGES = ["/", "/produits", "/produits/42", "/produits/317", "/panier", "/recherche?q=lampe", "/compte", "/static/app.js",
         "/static/style.css", "/api/produits/42", "/api/panier", "/api/commande"]
UA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0 Safari/537.36"


def etat():
    try:
        with open(ETAT) as f:
            return json.load(f)
    except (OSError, ValueError):
        return {}


def poser_etat(e):
    with open(ETAT, "w") as f:
        json.dump(e, f)


def post(path, body, otlp=False):
    data = gzip.compress(json.dumps(body, separators=(",", ":")).encode())
    req = urllib.request.Request(URL + path, data=data, method="POST", headers={
        "Content-Type": "application/json", "Content-Encoding": "gzip", "X-Hypha-Api-Key": KEY,
        "User-Agent": "hypha-demo-donnees"})
    for essai in range(4):
        try:
            with urllib.request.urlopen(req, timeout=30) as r:
                return r.status
        except urllib.error.HTTPError as e:
            if e.code < 500 and e.code != 429:
                print(f"{path} : {e.code} {e.read()[:300]!r}", file=sys.stderr)
                return e.code
        except (urllib.error.URLError, TimeoutError) as e:
            print(f"{path} : {e}", file=sys.stderr)
        time.sleep(2 * (essai + 1))
    return 0


def agent(h):
    return {"id": h["id"], "version": "0.4.21", "hostname": h["name"], "os": h["os"],
            "arch": "amd64"}


def wave(t, period, amp, phase=0.0):
    return amp * math.sin(2 * math.pi * t / period + phase)


def noise(t, key, amp):
    r = random.Random(f"{key}:{int(t // STEP)}")
    return (r.random() - 0.5) * 2 * amp


def incident(t, start):
    """0 avant la panne, monte en 3 minutes jusqu'à 1."""
    if start is None or t < start:
        return 0.0
    return min(1.0, (t - start) / 180.0)


def cpu_of(h, t, start):
    base = h["cpu"] + wave(t, 3600, 6, sum(map(ord, h["name"])) % 7) + noise(t, h["name"] + "c", 4)
    if h["name"] == "web-02":
        base += incident(t, start) * (96 - base)
    return max(1.0, min(99.5, base))


def series_at(h, t, start):
    """Toutes les séries d'un hôte à l'instant t : [(nom, unité, étiquettes, valeur)]."""
    cpu = cpu_of(h, t, start)
    sysp = cpu * 0.22
    iowait = 0.0 if h["os"] == "windows" else 0.4 + noise(t, h["name"] + "w", 0.3)
    out = [("system.cpu.usage", "percent", {}, cpu), ("system.cpu.busy", "percent", {}, cpu - iowait),
           ("system.cpu.user", "percent", {}, cpu - sysp),
           ("system.cpu.system", "percent", {}, sysp), ("system.cpu.idle", "percent", {}, 100 - cpu),
           ("system.cpu.num_cores", "count", {}, h["cores"])]
    memp = h["memp"] + wave(t, 5400, 3, 1) + noise(t, h["name"] + "m", 1)
    if h["name"] == "web-02":
        memp += incident(t, start) * 14
    used = h["mem"] * memp / 100
    out += [("system.mem.total", "byte", {}, h["mem"]), ("system.mem.used", "byte", {}, used),
            ("system.mem.free", "byte", {}, h["mem"] - used), ("system.mem.usable", "byte", {}, h["mem"] - used),
            ("system.mem.pct_used", "percent", {}, memp)]
    swap = 2 * GIB
    swapp = 3 + (incident(t, start) * 9 if h["name"] == "web-02" else 0)
    out += [("system.swap.total", "byte", {}, swap), ("system.swap.used", "byte", {}, swap * swapp / 100),
            ("system.swap.free", "byte", {}, swap * (1 - swapp / 100)), ("system.swap.pct_used", "percent", {}, swapp)]
    win = h["os"] == "windows"
    disks = [("C:" if win else "/", "C:" if win else "/dev/vda1", "NTFS" if win else "ext4", h["disk"], h["diskp"])]
    if h["name"] == "db-01":
        disks.append(("/var/lib/postgresql", "/dev/vdb1", "xfs", 400 * GIB, 71 + (t % 86400) / 86400 * 0.6))
    for mount, dev, fs, total, pct in disks:
        tags = {"device": dev, "mount": mount, "fstype": fs}
        out += [("system.disk.total", "byte", tags, total), ("system.disk.used", "byte", tags, total * pct / 100),
                ("system.disk.free", "byte", tags, total * (1 - pct / 100)), ("system.disk.in_use", "percent", tags, pct)]
        if not win:
            out.append(("system.fs.inodes.in_use", "percent", {"mount": mount}, pct * 0.4))
    dev = {"device": "C:" if win else "vda"}
    io = 1 + cpu / 10 + noise(t, h["name"] + "io", 1)
    out += [("system.io.read_bytes", "byte_per_second", dev, io * 180_000), ("system.io.write_bytes", "byte_per_second", dev, io * 420_000),
            ("system.io.util", "percent", dev, min(100, io * 3))]
    ifc = {"interface": "Ethernet" if win else "eth0"}
    traffic = {"web": 3_500_000, "db": 1_800_000, "cache": 900_000, "app": 600_000}[h["role"]]
    traffic *= 1 + wave(t, 3600, 0.25) + noise(t, h["name"] + "n", 0.1)
    out += [("system.net.bytes_rcvd", "byte_per_second", ifc, traffic * 0.35), ("system.net.bytes_sent", "byte_per_second", ifc, traffic),
            ("system.net.tcp.connections", "count", {"state": "established"}, 40 + traffic / 60_000),
            ("system.uptime", "second", {}, 1_640_000 + t % 10_000_000), ("system.processes.count", "count", {}, 180 if not win else 120),
            ("hypha.agent.payload_bytes", "byte", {}, 21_000 + noise(t, h["name"] + "p", 2000))]
    if not win:
        load = cpu / 100 * h["cores"] * (1.4 if h["name"] == "web-02" and incident(t, start) else 1.0)
        out += [("system.load.1", "count", {}, load), ("system.load.5", "count", {}, load * 0.95),
                ("system.load.15", "count", {}, load * 0.9), ("system.load.norm.1", "count", {}, load / h["cores"]),
                ("system.cpu.iowait", "percent", {}, iowait)]
    if h["role"] == "web":
        rps = 38 * (1 + wave(t, 3600, 0.3)) + noise(t, h["name"] + "r", 3)
        out += [("nginx.requests", "operation_per_second", {}, rps), ("nginx.connections.active", "count", {}, rps * 1.6),
                ("tls.cert.days_left", "day", {"cert_path": "/etc/letsencrypt/live/boutique.example/cert.pem",
                                              "subject": "boutique.example"}, 64 - (t % 86400) / 86400)]
    if h["role"] == "db":
        tags = {"db": "shop"}
        out += [("postgresql.connections", "count", {}, 46 + noise(t, "pgc", 6)), ("postgresql.max_connections", "count", {}, 200),
                ("postgresql.percent_usage_connections", "percent", {}, 23 + noise(t, "pgp", 3)),
                ("postgresql.buffer_hit", "percent", tags, 99.2 + noise(t, "pgb", 0.3)),
                ("postgresql.commits", "operation_per_second", tags, 140 + wave(t, 3600, 40)),
                ("postgresql.database_size", "byte", tags, 38 * GIB + t % 86400 * 1000)]
    if h["role"] == "cache":
        out += [("redis.clients.connected", "count", {}, 64 + noise(t, "rc", 5)), ("redis.mem.used", "byte", {}, 1.9 * GIB),
                ("redis.keyspace.hit_rate", "percent", {}, 93 + noise(t, "rh", 2)),
                ("redis.commands", "operation_per_second", {}, 2400 + wave(t, 3600, 600))]
    if win:
        out += [("iis.requests_per_s", "operation_per_second", {}, 22 + wave(t, 3600, 6)),
                ("iis.current_connections", "count", {}, 31 + noise(t, "iis", 4))]
    return out


def send_metrics(h, t0, t1, start, only=None):
    acc = {}
    t = t0 - t0 % STEP
    while t <= t1:
        for name, unit, tags, v in series_at(h, t, start):
            if only and name not in only:
                continue
            k = (name, json.dumps(tags, sort_keys=True))
            acc.setdefault(k, (name, unit, tags, []))[3].append([int(t * 1000), round(v, 3)])
        t += STEP
    series = [{"metric": n, "type": "gauge", "unit": u, "tags": tg, "points": p} for n, u, tg, p in acc.values()]
    return post("/api/v1/intake/metrics", {"agent": agent(h), "series": series})


def host_body(h):
    services = {"web": ["nginx", "x509"], "db": ["postgres"], "cache": ["redis"], "app": ["iis"]}[h["role"]]
    return {"agent": agent(h), "host": {
        "hostname": h["name"], "os": h["os"], "platform": h["platform"], "platform_version": h["ver"],
        "kernel": h["kernel"], "arch": "amd64", "cpu_cores": h["cores"], "cpu_model": "AMD EPYC 7543P 32-Core Processor",
        "mem_total": h["mem"], "ip_addresses": [h["ip"]], "boot_time": int((time.time() - 1_640_000) * 1000),
        "tags": {"env": "prod", "role": h["role"], "team": "boutique"}, "agent_version": "0.4.21",
        "meta": {"virtualization": "kvm", "vendor": "QEMU", "timezone": "Europe/Paris"},
        "status": {"collectors": {"logs": "ok", "processes": "ok", "network": "ok"}, "errors": []},
        "capabilities": {"actions": {"enabled": True, "kinds": ["restart_service", "start_service"], "scripts": []},
                         "synthetics": False, "auto_update": True, "snmp_devices": 0, "integrations": services}}}


def checks_at(h, t, start):
    ts = int(t * 1000)
    out = [{"ts": ts, "name": "hypha.agent.up", "status": 0, "message": "", "tags": {}}]
    units = {"web": ["nginx.service", "shop-api.service", "shop-worker.service"], "db": ["postgresql@16-main.service"],
             "cache": ["redis-server.service"]}.get(h["role"], [])
    for u in units:
        if h["name"] == "web-01" and u == "shop-worker.service" and etat().get("oom"):
            out.append({"ts": ts, "name": "systemd.unit", "status": 2, "message": "failed (Result: oom-kill)", "tags": {"unit": u}})
            continue
        out.append({"ts": ts, "name": "systemd.unit", "status": 0, "message": "active (running)", "tags": {"unit": u}})
    if h["os"] == "windows":
        out.append({"ts": ts, "name": "windows.service", "status": 0, "message": "Running", "tags": {"windows_service": "W3SVC"}})
    out.append({"ts": ts, "name": "ntp.in_sync", "status": 0, "message": "synchronisé", "tags": {}})
    if h["role"] == "web":
        out.append({"ts": ts, "name": "tls.cert.expiry", "status": 0, "message": "64 jours restants",
                    "tags": {"cert_path": "/etc/letsencrypt/live/boutique.example/cert.pem", "subject": "boutique.example"}})
    if h["role"] == "cache":
        out.append({"ts": ts, "name": "redis.can_connect", "status": 0, "message": "", "tags": {}})
    return out


def logs_between(h, t0, t1, start, rnd):
    out = []
    t = t0
    while t < t1:
        ts = int(t * 1000)
        inc = h["name"] == "web-02" and incident(t, start) > 0.5
        if h["role"] == "web":
            for _ in range(rnd.randint(3, 6)):
                page = rnd.choice(PAGES)
                status = 200 if rnd.random() > 0.04 else rnd.choice([301, 404])
                dur = rnd.uniform(0.004, 0.09)
                if inc and page.startswith("/api") and rnd.random() < 0.35:
                    status, dur = rnd.choice([502, 504]), rnd.uniform(29.5, 30.1)
                stamp = time.strftime("%d/%b/%Y:%H:%M:%S +0000", time.gmtime(t))
                method = "POST" if page in ("/api/panier", "/api/commande") else "GET"
                out.append({"ts": ts + rnd.randint(0, 14000), "service": "nginx", "source": "nginx", "status": "info",
                            "message": f'{rnd.choice(CLIENTS)} - - [{stamp}] "{method} {page} HTTP/1.1" {status} '
                                       f'{rnd.randint(300, 48000)} "-" "{UA}" {dur:.3f}',
                            "attributes": {"file": "/var/log/nginx/access.log"}, "tags": {}})
            if inc and rnd.random() < 0.5:
                out.append({"ts": ts + 3000, "service": "shop-api", "source": "gunicorn", "status": "critical",
                            "message": f"[CRITICAL] WORKER TIMEOUT (pid:{rnd.randint(2100, 2300)})", "attributes": {}, "tags": {}})
                out.append({"ts": ts + 3200, "service": "nginx", "source": "nginx", "status": "error",
                            "message": "upstream timed out (110: Connection timed out) while reading response header from "
                                       "upstream, client: 203.0.113.52, server: boutique.example, request: \"GET /api/produits/42 "
                                       "HTTP/1.1\", upstream: \"http://127.0.0.1:8000/api/produits/42\"",
                            "attributes": {"file": "/var/log/nginx/error.log"}, "tags": {}})
            if h["name"] == "web-02" and start and abs(t - start) < STEP:
                out.append({"ts": ts, "service": "sudo", "source": "journald", "status": "notice",
                            "message": "deploy : TTY=pts/0 ; PWD=/srv/shop ; USER=shop ; COMMAND=/usr/bin/python3 "
                                       "/srv/shop/bin/export_catalogue.py --full --format csv", "attributes": {}, "tags": {}})
            if rnd.random() < 0.06:
                out.append({"ts": ts + 5000, "service": "shop-api", "source": "python", "status": "error",
                            "message": "Traceback (most recent call last):\n  File \"/srv/shop/app/panier.py\", line 88, in ajouter\n"
                                       "    ligne = self.lignes[article['sku']]\nKeyError: 'sku'",
                            "attributes": {"logger": "shop.panier"}, "tags": {}})
            out.append({"ts": ts + 7000, "service": "shop-api", "source": "python", "status": "info",
                        "message": f"commande {rnd.randint(18000, 19999)} validée ({rnd.randint(1, 6)} articles)",
                        "attributes": {"logger": "shop.commande"}, "tags": {}})
            if h["name"] == "web-01" and rnd.random() < 0.15:
                ip = rnd.choice(["203.0.113.45", "203.0.113.45", "198.51.100.250"])
                user = rnd.choice(["admin", "oracle", "test", "ubuntu"])
                out.append({"ts": ts + 9000, "service": "sshd", "source": "journald", "status": "info",
                            "message": f"Failed password for invalid user {user} from {ip} port {rnd.randint(30000, 60000)} ssh2",
                            "attributes": {}, "tags": {}})
        elif h["role"] == "db":
            if rnd.random() < 0.2:
                out.append({"ts": ts, "service": "postgresql", "source": "postgresql", "status": "info",
                            "message": f"LOG:  checkpoint complete: wrote {rnd.randint(800, 4000)} buffers ({rnd.uniform(0.5, 3):.1f}%); "
                                       f"0 WAL file(s) added, 0 removed, 1 recycled", "attributes": {}, "tags": {}})
            if rnd.random() < 0.04:
                out.append({"ts": ts + 4000, "service": "postgresql", "source": "postgresql", "status": "error",
                            "message": "ERROR:  duplicate key value violates unique constraint \"commandes_pkey\"",
                            "attributes": {}, "tags": {}})
        elif h["role"] == "cache":
            if rnd.random() < 0.1:
                out.append({"ts": ts, "service": "redis", "source": "redis", "status": "info",
                            "message": "Background saving started by pid 1187", "attributes": {}, "tags": {}})
                out.append({"ts": ts + 900, "service": "redis", "source": "redis", "status": "info",
                            "message": "DB saved on disk", "attributes": {}, "tags": {}})
        else:
            if rnd.random() < 0.08:
                out.append({"ts": ts, "service": "stock-api", "source": "Application", "status": "info",
                            "message": "Synchronisation du stock terminée : 1 284 articles", "attributes": {}, "tags": {}})
            if rnd.random() < 0.02:
                out.append({"ts": ts, "service": "Service Control Manager", "source": "System", "status": "info",
                            "message": "The Windows Update service entered the running state.", "attributes": {}, "tags": {}})
        t += STEP
    return out


def hexid(rnd, n):
    return "".join(rnd.choice("0123456789abcdef") for _ in range(n))


def kv(d):
    out = []
    for k, v in d.items():
        val = {"intValue": str(v)} if isinstance(v, int) else {"stringValue": str(v)}
        out.append({"key": k, "value": val})
    return out


def traces_between(h, t0, t1, start, rnd):
    """Traces OTLP de shop-api (web-01, web-02) avec PostgreSQL, Redis et stock-api (win-app-01)."""
    spans_api, spans_stock = [], []
    routes = [("GET", "/api/produits/{id}", 0.55), ("POST", "/api/panier", 0.25), ("POST", "/api/commande", 0.12),
              ("GET", "/api/recherche", 0.08)]
    t = t0
    while t < t1:
        for _ in range(rnd.randint(2, 4)):
            r = rnd.random()
            acc = 0.0
            for method, route, p in routes:
                acc += p
                if r <= acc:
                    break
            begin = int((t + rnd.uniform(0, STEP)) * 1e9)
            slow = h["name"] == "web-02" and incident(t, start) > 0.5
            tid, root = hexid(rnd, 32), hexid(rnd, 16)
            cur = begin + int(rnd.uniform(0.3, 1.2) * 1e6)
            children = []
            db_ms = rnd.uniform(1.5, 9) * (6 if slow else 1)
            children.append(("SELECT shop.produits", "postgresql", "SELECT * FROM produits WHERE id = $1", db_ms))
            children.append(("GET panier:*", "redis", "GET", rnd.uniform(0.2, 0.9)))
            if route in ("/api/panier", "/api/commande"):
                children.append(("GET /stock/{sku}", "http", "GET /stock/{sku}", rnd.uniform(12, 40)))
            if route == "/api/commande":
                children.append(("INSERT shop.commandes", "postgresql", "INSERT INTO commandes (client_id, total) VALUES ($1, $2)",
                                 rnd.uniform(3, 12)))
            for name, system, stmt, ms in children:
                sid = hexid(rnd, 16)
                end = cur + int(ms * 1e6)
                attrs = {"db.system": system, "db.statement": stmt} if system != "http" else {
                    "http.request.method": "GET", "server.address": "stock-api", "peer.service": "stock-api"}
                spans_api.append({"traceId": tid, "spanId": sid, "parentSpanId": root, "name": name, "kind": 3,
                                  "startTimeUnixNano": str(cur), "endTimeUnixNano": str(end), "attributes": kv(attrs),
                                  "status": {"code": 1}})
                if system == "http":
                    spans_stock.append({"traceId": tid, "spanId": hexid(rnd, 16), "parentSpanId": sid,
                                        "name": "GET /stock/{sku}", "kind": 2, "startTimeUnixNano": str(cur + 600_000),
                                        "endTimeUnixNano": str(end - 400_000),
                                        "attributes": kv({"http.route": "/stock/{sku}", "http.request.method": "GET",
                                                          "http.response.status_code": 200}), "status": {"code": 1}})
                cur = end + int(rnd.uniform(0.1, 0.6) * 1e6)
            total_end = cur + int(rnd.uniform(0.5, 3) * 1e6) + (int(rnd.uniform(1.5, 4) * 1e9) if slow else 0)
            error = (route == "/api/commande" and rnd.random() < 0.05) or (slow and rnd.random() < 0.3)
            events = []
            code = 200
            if error:
                code = 500
                events = [{"timeUnixNano": str(total_end - 1000), "name": "exception", "attributes": kv({
                    "exception.type": "psycopg.errors.UniqueViolation",
                    "exception.message": "duplicate key value violates unique constraint \"commandes_pkey\"",
                    "exception.stacktrace": "Traceback (most recent call last):\n  File \"/srv/shop/app/commande.py\", line 141, "
                                            "in valider\n    cur.execute(INSERT_COMMANDE, valeurs)\n"
                                            "psycopg.errors.UniqueViolation: duplicate key value violates unique constraint "
                                            "\"commandes_pkey\""})}]
            spans_api.append({"traceId": tid, "spanId": root, "parentSpanId": "", "name": f"{method} {route}", "kind": 2,
                              "startTimeUnixNano": str(begin), "endTimeUnixNano": str(total_end),
                              "attributes": kv({"http.route": route, "http.request.method": method,
                                                "http.response.status_code": code}),
                              "status": {"code": 2 if error else 1}, "events": events})
        t += STEP

    def resource(service, host, spans):
        return {"resource": {"attributes": kv({"service.name": service, "host.name": host,
                                               "deployment.environment": "prod", "service.version": "2.14.0"})},
                "scopeSpans": [{"scope": {"name": "opentelemetry.instrumentation"}, "spans": spans}]}
    body = [resource("shop-api", h["name"], spans_api)]
    if spans_stock:
        body.append(resource("stock-api", "win-app-01", spans_stock))
    return {"resourceSpans": body}


def processes(h, t, start):
    base = {"web": [("nginx", "www-data", "nginx: worker process", 3.1, 52), ("gunicorn", "shop",
                    "gunicorn: worker [shop.wsgi:application]", 9.5, 310), ("node_exporter", "nobody", "node_exporter", 0.2, 18)],
            "db": [("postgres", "postgres", "postgres: 16/main: checkpointer", 0.8, 160),
                   ("postgres", "postgres", "postgres: shop shop 198.51.100.11(51234) idle", 4.2, 210)],
            "cache": [("redis-server", "redis", "/usr/bin/redis-server 127.0.0.1:6379", 6.5, 1990)],
            "app": [("w3wp", "IIS APPPOOL\\stock", "w3wp.exe -ap \"stock\"", 7.4, 420)]}[h["role"]]
    out = []
    for i, (name, user, cmd, cpu, rss) in enumerate(base):
        out.append({"pid": 1000 + i * 37, "ppid": 1, "name": name, "user": user, "cmdline": cmd, "state": "S",
                    "cpu_pct": cpu, "mem_rss": rss * 1024 * 1024, "mem_pct": rss * 1024 * 1024 / h["mem"] * 100,
                    "threads": 4, "started": int((t - 86400 * 3) * 1000), "io_read_bps": 0, "io_write_bps": 2048})
    if h["name"] == "web-02" and incident(t, start) > 0:
        out.append({"pid": 48211, "ppid": 47990, "name": "python3", "user": "shop",
                    "cmdline": "/usr/bin/python3 /srv/shop/bin/export_catalogue.py --full --format csv", "state": "R",
                    "cpu_pct": 372.0 * incident(t, start), "mem_rss": 1_300_000_000, "mem_pct": 15.1, "threads": 8,
                    "started": int(start * 1000), "io_read_bps": 9_400_000, "io_write_bps": 3_100_000})
    return out


def connections(h):
    if h["role"] == "web":
        return [{"proto": "tcp", "direction": "outgoing", "laddr": h["ip"], "lport": 0, "raddr": "198.51.100.21", "rport": 5432,
                 "state": "ESTABLISHED", "pid": 1037, "process": "gunicorn", "count": 12},
                {"proto": "tcp", "direction": "outgoing", "laddr": h["ip"], "lport": 0, "raddr": "198.51.100.31", "rport": 6379,
                 "state": "ESTABLISHED", "pid": 1037, "process": "gunicorn", "count": 6},
                {"proto": "tcp", "direction": "outgoing", "laddr": h["ip"], "lport": 0, "raddr": "198.51.100.41", "rport": 443,
                 "state": "ESTABLISHED", "pid": 1037, "process": "gunicorn", "count": 2},
                {"proto": "tcp", "direction": "listening", "laddr": "0.0.0.0", "lport": 443, "raddr": "", "rport": 0,
                 "state": "LISTEN", "pid": 1000, "process": "nginx", "count": 1}]
    port = {"db": (5432, "postgres"), "cache": (6379, "redis-server"), "app": (443, "System")}[h["role"]]
    return [{"proto": "tcp", "direction": "listening", "laddr": "0.0.0.0", "lport": port[0], "raddr": "", "rport": 0,
             "state": "LISTEN", "pid": 1000, "process": port[1], "count": 1}]


def cycle(t0, t1, start, rnd, live):
    for h in HOSTS:
        send_metrics(h, t0, t1, start)
        lg = logs_between(h, t0, t1, start, rnd)
        for i in range(0, len(lg), 2000):
            post("/api/v1/intake/logs", {"agent": agent(h), "logs": lg[i:i + 2000]})
        if h["role"] == "web":
            post("/v1/traces", traces_between(h, t0, t1, start, rnd))
        if live:
            post("/api/v1/intake/checks", {"agent": agent(h), "checks": checks_at(h, t1, start)})


def remplir(minutes):
    now = time.time()
    e = etat()
    start = e.get("panne") or now - 22 * 60
    poser_etat({"panne": start})
    rnd = random.Random(7)
    for h in HOSTS:
        post("/api/v1/intake/host", host_body(h))
    deb = now - minutes * 60
    post("/api/v1/intake/events", {"agent": agent(BY["web-01"]), "events": [
        {"ts": int((now - 95 * 60) * 1000), "source": "deploy", "type": "deploy", "title": "Déploiement shop-api 2.14.0",
         "text": "Livré par la CI sur web-01 et web-02", "alert_type": "success", "priority": "normal",
         "tags": {"service": "shop-api", "version": "2.14.0"}, "aggregation_key": "deploy:shop-api"}]})
    t = deb
    while t < now:
        cycle(t, min(now, t + 600), start, rnd, False)
        t += 600
        print(f"rempli jusqu'à {time.strftime('%H:%M', time.localtime(t))}", flush=True)


def panne_worker(now):
    """shop-worker tué par le manque de mémoire sur web-01 : l'unité reste en échec (réparation d'un clic)."""
    e = etat()
    if e.get("oom"):
        return
    ts = int((now - 240) * 1000)
    h = BY["web-01"]
    lines = [
        ("kernel", "kernel", "warning", "python3 invoked oom-killer: gfp_mask=0x140cca(GFP_HIGHUSER_MOVABLE|__GFP_COMP), order=0, oom_score_adj=0"),
        ("kernel", "kernel", "error", "Out of memory: Killed process 3121 (python3) total-vm:6814220kB, anon-rss:5912044kB, file-rss:0kB, "
                                      "shmem-rss:0kB, UID:998 pgtables:11788kB oom_score_adj:0"),
        ("systemd", "journald", "warning", "shop-worker.service: A process of this unit has been killed by the OOM killer."),
        ("systemd", "journald", "error", "shop-worker.service: Main process exited, code=killed, status=9/KILL"),
        ("systemd", "journald", "error", "shop-worker.service: Failed with result 'oom-kill'."),
        ("systemd", "journald", "info", "shop-worker.service: Consumed 3min 12.402s CPU time, 5.6G memory peak."),
        ("shop-worker", "python", "info", "file des commandes : 1 284 messages en attente"),
    ]
    logs = [{"ts": ts + i * 400, "service": s, "source": src, "status": st, "message": m, "attributes": {}, "tags": {}}
            for i, (s, src, st, m) in enumerate(lines)]
    post("/api/v1/intake/logs", {"agent": agent(h), "logs": logs})
    post("/api/v1/intake/events", {"agent": agent(h), "events": [
        {"ts": ts + 1600, "source": "agent", "type": "service_state", "title": "shop-worker.service : active → failed",
         "text": "Result: oom-kill", "alert_type": "error", "priority": "normal", "tags": {"unit": "shop-worker.service"},
         "aggregation_key": "systemd:shop-worker.service"}]})
    e["oom"] = int(now)
    poser_etat(e)


def direct(minutes):
    fin = time.time() + minutes * 60
    panne_worker(time.time())
    start = etat().get("panne")
    rnd = random.Random()
    n = 0
    last = time.time() - STEP
    while time.time() < fin:
        now = time.time()
        if n % 20 == 0:
            for h in HOSTS:
                post("/api/v1/intake/host", host_body(h))
        cycle(last, now, start, rnd, True)
        if n % 4 == 0:
            for h in HOSTS:
                post("/api/v1/intake/processes", {"agent": agent(h), "ts": int(now * 1000), "processes": processes(h, now, start)})
                post("/api/v1/intake/connections", {"agent": agent(h), "ts": int(now * 1000), "connections": connections(h)})
        last = now
        n += 1
        time.sleep(max(1.0, STEP - (time.time() - now)))


if __name__ == "__main__":
    if not KEY:
        raise SystemExit("DEMO_INGEST absente")
    cmd, minutes = (sys.argv[1] if len(sys.argv) > 1 else ""), float(sys.argv[2]) if len(sys.argv) > 2 else 60
    if cmd == "remplir":
        remplir(minutes)
    elif cmd == "direct":
        direct(minutes)
    elif cmd == "completer":
        # Une série ajoutée après coup (ici system.cpu.busy), remplie seule sur la durée donnée.
        now, start = time.time(), etat().get("panne")
        for h in HOSTS:
            send_metrics(h, now - minutes * 60, now, start, only=sys.argv[3].split(","))
        print("complété")
    else:
        raise SystemExit(__doc__)
