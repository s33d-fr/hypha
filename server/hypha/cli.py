"""Ligne de commande : `python -m hypha <commande>` (contrat § 6)."""
import argparse
import asyncio
import getpass
import logging
import os
import sys

from .config import get_settings
from .logsetup import setup_logging

log = logging.getLogger("hypha.cli")


def _api(args) -> None:
    import uvicorn
    s = get_settings()
    uvicorn.run(
        "hypha.app:create_app", factory=True,
        host=s.listen_host, port=s.listen_port, workers=s.workers,
        # L'IP du client ne vient de X-Forwarded-For que si la connexion arrive d'un proxy de confiance.
        proxy_headers=True, forwarded_allow_ips=",".join(ip.strip() for ip in s.trusted_proxies.split(",") if ip.strip()),
        log_config=None, access_log=s.access_log, server_header=False,
        timeout_graceful_shutdown=20,
    )


async def _with_conn(fn):
    import psycopg
    from psycopg.rows import dict_row
    s = get_settings()
    if not s.db_write:
        raise SystemExit("HYPHA_DB_WRITE absent")
    async with await psycopg.AsyncConnection.connect(s.db_write, row_factory=dict_row,
                                                     application_name="hypha-cli") as conn:
        await conn.execute("SET search_path TO hypha, public")
        result = await fn(conn)
        await conn.commit()
        return result


async def _org_id(conn, slug: str) -> int:
    from .db import fetch_one
    row = await fetch_one(conn, "SELECT id FROM orgs WHERE slug = %s", (slug,))
    if row is None:
        raise SystemExit(f"organisation inconnue : {slug}")
    return row["id"]


def _create_org(args) -> None:
    from .auth import accounts

    async def go(conn):
        return await accounts.create_org(conn, args.name, args.slug)
    org = asyncio.run(_with_conn(go))
    if org["slug"] != args.slug:
        print(f"slug « {args.slug} » déjà pris : organisation créée sous « {org['slug']} »", file=sys.stderr)
    print(f"organisation {org['id']} : {org['slug']} ({org['name']})")


def _create_key(args) -> None:
    from .auth import accounts

    async def go(conn):
        return await accounts.create_api_key(conn, await _org_id(conn, args.org), args.name, args.kind)
    row, key = asyncio.run(_with_conn(go))
    print(f"clé {row['kind']} n° {row['id']} « {row['name']} » (affichée une seule fois) :", file=sys.stderr)
    print(key)


def _create_user(args) -> None:
    from .auth import accounts
    from .auth.passwords import hash_password
    password = args.password
    if password in (None, "-"):
        password = os.environ.get("HYPHA_NEW_PASSWORD") or getpass.getpass("mot de passe : ")
    if len(password) < 8:
        raise SystemExit("mot de passe de 8 caractères au moins")
    login = args.login.strip().lower()
    pw_hash = hash_password(password)

    async def go(conn):
        org_id = await _org_id(conn, args.org)
        user_id = await accounts.upsert_local_user(conn, login, pw_hash, args.email, args.name)
        await accounts.add_membership(conn, org_id, user_id, args.role)
        return user_id
    if get_settings().saas:
        print("attention : HYPHA_MODE=saas, les comptes locaux ne servent qu'en auto-hébergé", file=sys.stderr)
    user_id = asyncio.run(_with_conn(go))
    print(f"utilisateur {user_id} : {login} ({args.role}) dans {args.org}")


def _migrate(args) -> None:
    from . import migrate
    migrate.run(get_settings())


def _worker(args) -> None:
    from . import worker
    worker.run()


def _facturation(args) -> None:
    import asyncio
    import json

    from .config import get_settings
    from .db import Database, fetch_one
    from .runtime import Runtime, set_runtime

    async def run():
        settings = get_settings()
        if not settings.saas:
            raise SystemExit("formules et facturation : service hébergé seulement (HYPHA_MODE=saas)")
        db = Database(settings, "hypha-cli")
        set_runtime(Runtime(settings=settings, db=db))
        await db.open()
        try:
            async with db.writer() as conn:
                org = await fetch_one(conn, "SELECT id, slug, plan FROM orgs WHERE slug = %s", (args.org,))
                if org is None:
                    raise SystemExit(f"espace inconnu : {args.org}")
                if org["plan"] == "interne":
                    raise SystemExit("l'espace interne de S33D n'a ni formule ni facturation")
                from .billing import service as B
                if args.action == "remise-association":
                    print(json.dumps(await B.set_discount(conn, org["id"], "association"), ensure_ascii=False))
                elif args.action == "sans-remise":
                    print(json.dumps(await B.set_discount(conn, org["id"], None), ensure_ascii=False))
                elif args.action == "formule":
                    if not args.formule:
                        raise SystemExit("--formule requis")
                    await B.set_plan(conn, org["id"], args.formule)
                    print(f"espace {org['slug']} : formule {args.formule}")
                row = await B.billing_row(conn, org["id"]) or {}
                plan = (await fetch_one(conn, "SELECT plan FROM orgs WHERE id = %s", (org["id"],)))["plan"]
                shown = {k: (str(v) if v is not None else None) for k, v in row.items()
                         if k not in ("last_report",)}
                print(json.dumps({"espace": org["slug"], "formule": plan, "facturation": shown},
                                 ensure_ascii=False, indent=1))
        finally:
            await db.close()

    asyncio.run(run())


def main(argv=None) -> None:
    parser = argparse.ArgumentParser(prog="python -m hypha", description="Serveur HYPHA")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("api", help="API HTTP (uvicorn, HYPHA_LISTEN)").set_defaults(fn=_api)
    sub.add_parser("worker", help="moniteurs, synthétiques, agrégats, partitions").set_defaults(fn=_worker)
    sub.add_parser("migrate", help="applique schema/*.sql").set_defaults(fn=_migrate)
    p = sub.add_parser("create-org", help="crée une organisation")
    p.add_argument("--slug", required=True)
    p.add_argument("--name", required=True)
    p.set_defaults(fn=_create_org)
    p = sub.add_parser("create-key", help="crée une clé d'API (affichée une seule fois)")
    p.add_argument("--org", required=True, help="slug de l'organisation")
    p.add_argument("--name", required=True)
    p.add_argument("--kind", choices=("ingest", "app"), default="ingest")
    p.set_defaults(fn=_create_key)
    p = sub.add_parser("facturation", help="formule et facturation d'un espace (§ 52), réservé à S33D")
    p.add_argument("action", choices=["etat", "remise-association", "sans-remise", "formule"])
    p.add_argument("--org", required=True, help="slug de l'espace")
    p.add_argument("--formule", choices=["decouverte", "pro", "entreprise"],
                   help="action formule : pose la formule sans Stripe (contrat sur bon de commande)")
    p.set_defaults(fn=_facturation)
    p = sub.add_parser("create-user", help="crée un compte local (auto-hébergé)")
    p.add_argument("--org", required=True, help="slug de l'organisation")
    p.add_argument("--login", required=True)
    p.add_argument("--password", help="omis ou « - » : lu dans HYPHA_NEW_PASSWORD ou demandé")
    p.add_argument("--role", choices=("owner", "admin", "member", "viewer"), default="admin")
    p.add_argument("--email")
    p.add_argument("--name")
    p.set_defaults(fn=_create_user)
    args = parser.parse_args(argv)
    setup_logging(get_settings().log_level)
    args.fn(args)
