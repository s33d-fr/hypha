"""`python -m hypha migrate` : applique schema/NNN_*.sql dans l'ordre, selon hypha.schema_version.

Se connecte avec HYPHA_DB_MIGRATE (propriétaire du schéma), à défaut HYPHA_DB_WRITE (auto-hébergé,
un seul compte). Chaque fichier passe dans sa propre transaction ; un fichier déjà inscrit dans
schema_version est sauté. Si HYPHA_GRANT_TO est posé, le compte applicatif reçoit ensuite ses
droits (données seulement, aucun DDL), y compris sur les objets créés plus tard.
"""
import logging
import re
from pathlib import Path

import psycopg
from psycopg import sql

from .config import Settings

log = logging.getLogger("hypha.migrate")

FILE_RE = re.compile(r"^(\d+)_.+\.sql$")
ROLE_RE = re.compile(r"^[a-z_][a-z0-9_]{0,62}$")

GRANTS = (
    "GRANT USAGE ON SCHEMA hypha TO {r}",
    "GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA hypha TO {r}",
    "GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA hypha TO {r}",
    "GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA hypha TO {r}",
    "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA hypha GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO {r}",
    "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA hypha GRANT USAGE, SELECT, UPDATE ON SEQUENCES TO {r}",
    "ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA hypha GRANT EXECUTE ON FUNCTIONS TO {r}",
)


def schema_files(directory: Path) -> list[tuple[int, Path]]:
    if not directory.is_dir():
        raise SystemExit(f"dossier du schéma introuvable : {directory} (HYPHA_SCHEMA_DIR)")
    files = []
    for path in directory.iterdir():
        m = FILE_RE.match(path.name)
        if m and path.is_file():
            files.append((int(m.group(1)), path))
    files.sort()
    versions = [v for v, _ in files]
    if len(versions) != len(set(versions)):
        raise SystemExit("deux fichiers de schéma portent le même numéro de version")
    return files


def applied_versions(conn: psycopg.Connection) -> set[int]:
    row = conn.execute("SELECT to_regclass('hypha.schema_version') IS NOT NULL").fetchone()
    if not row[0]:
        return set()
    return {r[0] for r in conn.execute("SELECT version FROM hypha.schema_version").fetchall()}


def run(settings: Settings) -> None:
    dsn = settings.db_migrate
    if not dsn:
        raise SystemExit("HYPHA_DB_MIGRATE ou HYPHA_DB_WRITE est obligatoire")
    grant_to = settings.grant_to
    if grant_to and not ROLE_RE.match(grant_to):
        raise SystemExit(f"HYPHA_GRANT_TO invalide : {grant_to!r}")
    files = schema_files(settings.schema_dir)
    with psycopg.connect(dsn, autocommit=False, application_name="hypha-migrate") as conn:
        done = applied_versions(conn)
        conn.commit()
        for version, path in files:
            if version in done:
                log.info("schéma %03d déjà appliqué : %s", version, path.name)
                continue
            log.info("application du schéma %03d : %s", version, path.name)
            # Sans paramètre, psycopg envoie le texte tel quel (protocole simple) : plusieurs
            # instructions et des corps $$…$$ passent sans traitement des %.
            conn.execute(path.read_text(encoding="utf-8"))
            conn.execute("INSERT INTO hypha.schema_version (version) VALUES (%s) ON CONFLICT (version) DO NOTHING",
                         (version,))
            conn.commit()
        if grant_to:
            for stmt in GRANTS:
                conn.execute(sql.SQL(stmt).format(r=sql.Identifier(grant_to)))
            conn.commit()
            log.info("droits accordés au rôle %s", grant_to)
        final = sorted(applied_versions(conn))
    log.info("schéma à jour, versions appliquées : %s", ", ".join(map(str, final)) or "aucune")
