"""Sonde de santé de HAProxy : `GET /api/sante`, sans authentification ni requête en base.

Répond 200 tant que le processus tourne, même base perdue : sinon HAProxy retirerait les trois
nœuds d'un coup et l'interface ne pourrait plus afficher l'erreur. L'état de la base est le
dernier connu de la sonde de fond (Database.health_loop).
"""
from fastapi import APIRouter

from .. import __version__
from ..runtime import rt
from .common import ok

router = APIRouter()


@router.get("/api/sante")
async def sante():
    return ok({"ok": True, "version": __version__, "db": rt().db.state})
