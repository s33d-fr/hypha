"""Espace de démonstration des vidéos de la documentation (jamais l'espace S33D).

Lancé sur un nœud web, dans l'environnement du serveur :
    cd /opt/hypha/server && set -a && . /opt/hypha/.env && set +a
    /opt/hypha/venv/bin/python demo-espace.py creer      > secrets (sortie : variables DEMO_*)
    /opt/hypha/venv/bin/python demo-espace.py nommer en  (ou fr) : noms affichés dans la langue du tournage
    /opt/hypha/venv/bin/python demo-espace.py supprimer  : espace, comptes, clés et données

Comptes : « demo-video » (lecture seule, viewer) sert au navigateur sans tête ; « demo-admin »
(admin, sans session) ne sert qu'à porter la clé `app` qui pose moniteurs, tableaux et page de statut.
Les secrets sortent sur la sortie standard seulement : la rediriger vers un fichier 600.
"""
import asyncio
import sys

from hypha.auth import accounts
from hypha.cli import _with_conn
from hypha.db import fetch_one

SLUG = "demo-video"
NOMS = {"fr": ("Démo HYPHA", "Visiteur"), "en": ("HYPHA demo", "Visitor")}


async def _org(conn):
    return await fetch_one(conn, "SELECT id FROM orgs WHERE slug = %s", (SLUG,))


async def creer(conn):
    if await _org(conn):
        raise SystemExit("espace de démonstration déjà présent : « supprimer » d'abord")
    org = await accounts.create_org(conn, NOMS["fr"][0], SLUG)
    if org["slug"] != SLUG:
        raise SystemExit("slug pris par un autre espace")
    viewer = await accounts.upsert_local_user(conn, "demo-video", "!", None, NOMS["fr"][1])
    admin = await accounts.upsert_local_user(conn, "demo-admin", "!", None, "Demo admin")
    await accounts.add_membership(conn, org["id"], viewer, "viewer")
    await accounts.add_membership(conn, org["id"], admin, "admin")
    _, ingest = await accounts.create_api_key(conn, org["id"], "demo-donnees", "ingest")
    _, app = await accounts.create_api_key(conn, org["id"], "demo-objets", "app", created_by=admin)
    sid = await accounts.create_session(conn, viewer, org["id"], None, "banc headless")
    print(f"DEMO_ORG_ID={org['id']}")
    print(f"DEMO_INGEST={ingest}")
    print(f"DEMO_APP={app}")
    print(f"DEMO_SESSION={sid}")


async def nommer(conn, lang):
    org = await _org(conn)
    if not org:
        raise SystemExit("aucun espace de démonstration")
    nom_org, nom_user = NOMS[lang]
    await conn.execute("UPDATE orgs SET name = %s WHERE id = %s", (nom_org, org["id"]))
    await conn.execute("UPDATE users SET name = %s WHERE sub = 'local:demo-video'", (nom_user,))
    print(f"espace {org['id']} : {nom_org} / {nom_user}")


async def supprimer(conn):
    org = await _org(conn)
    if org:
        oid = org["id"]
        # Tables partitionnées sans clé étrangère vers orgs : vidées à la main avant l'espace.
        tables = await conn.execute("""
            SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
              JOIN pg_attribute a ON a.attrelid = c.oid AND a.attname = 'org_id' AND NOT a.attisdropped
             WHERE n.nspname = 'hypha' AND c.relkind = 'p'
        """)
        for (rel,) in [tuple(r.values()) for r in await tables.fetchall()]:
            await conn.execute(f'DELETE FROM hypha."{rel}" WHERE org_id = %s', (oid,))
        for rel in ("metric_points", "metric_rollup_5m", "metric_rollup_1h"):
            await conn.execute(f"""
                DELETE FROM hypha.{rel} WHERE series_id IN (SELECT id FROM hypha.metric_series WHERE org_id = %s)
            """, (oid,))
        await conn.execute("DELETE FROM orgs WHERE id = %s", (oid,))
        print(f"espace {oid} supprimé")
    for login in ("demo-video", "demo-admin"):
        await conn.execute("DELETE FROM sessions WHERE user_id IN (SELECT id FROM users WHERE sub = %s)", ("local:" + login,))
        await conn.execute("DELETE FROM users WHERE sub = %s", ("local:" + login,))
    print("comptes demo-video et demo-admin supprimés")


def main():
    cmd = sys.argv[1] if len(sys.argv) > 1 else ""
    if cmd == "creer":
        asyncio.run(_with_conn(creer))
    elif cmd == "nommer" and len(sys.argv) > 2 and sys.argv[2] in NOMS:
        asyncio.run(_with_conn(lambda c: nommer(c, sys.argv[2])))
    elif cmd == "supprimer":
        asyncio.run(_with_conn(supprimer))
    else:
        raise SystemExit(__doc__)


if __name__ == "__main__":
    main()
