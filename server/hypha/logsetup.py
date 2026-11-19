"""Journalisation sur stdout : journald (ou docker) horodate et conserve, on n'écrit aucun fichier."""
import logging
import sys


class _SkipHealthcheck(logging.Filter):
    # La sonde HAProxy passe toutes les 10 s sur chaque nœud : la tracer noierait le journal.
    def filter(self, record: logging.LogRecord) -> bool:
        args = record.args
        if isinstance(args, tuple) and len(args) >= 3 and isinstance(args[2], str):
            return not args[2].startswith("/api/sante")
        return "/api/sante" not in record.getMessage()


def setup_logging(level: str = "INFO") -> None:
    root = logging.getLogger()
    if getattr(root, "_hypha_configured", False):
        root.setLevel(level)
        return
    handler = logging.StreamHandler(sys.stdout)
    handler.setFormatter(logging.Formatter("%(levelname)s %(name)s: %(message)s"))
    root.handlers[:] = [handler]
    root.setLevel(level)
    root._hypha_configured = True  # type: ignore[attr-defined]
    logging.getLogger("uvicorn.access").addFilter(_SkipHealthcheck())
    # httpx trace chaque requête en INFO : trop bavard pour les synthétiques et les webhooks.
    logging.getLogger("httpx").setLevel(logging.WARNING)
    logging.getLogger("httpcore").setLevel(logging.WARNING)
    logging.getLogger("psycopg.pool").setLevel(logging.WARNING)
