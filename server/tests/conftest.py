import re
import sys
from pathlib import Path

# Les tests importent le paquet depuis server/ sans installation.
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))


def placeholders(sql: str) -> int:
    """Nombre de paramètres %s attendus par une requête (les %% sont des % littéraux)."""
    return len(re.findall(r"(?<!%)%s", sql.replace("%%", "")))
