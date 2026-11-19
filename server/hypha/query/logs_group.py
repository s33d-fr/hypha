"""Regroupement des journaux par clé (contrat § 23) : `service host status source` et `@chemin.attribut`.

Sert aux métriques tirées des journaux (côté Python, à l'ingestion) et au moniteur de journaux
groupé (côté SQL). Cinq clés au plus. Usage côté SQL :

    keys = logs_group.parse_keys(q.get("by", []))
    exprs, params = logs_group.sql_select(keys)            # « … AS g0 », « … AS g1 »
    sql = f"SELECT {', '.join(exprs)}, count(*) AS n FROM logs WHERE … GROUP BY {logs_group.sql_group(keys)}"
    rows = await fetch_all(conn, sql, params + where_params)
    values = logs_group.row_groups(keys, row)               # {"service": "nginx", "@http.status_code": "502"}

La valeur d'un attribut se lit imbriquée ({"http":{"status_code":502}}) ou à plat
({"http.status_code":502}), comme la recherche ; elle est rendue en texte (jsonb ->>).
"""
from dataclasses import dataclass

from ..errors import QueryError
from . import logs_match
from . import logs_ql as L

COLUMNS = {"service": "service", "host": "hostname", "status": "status", "source": "source"}
MAX_KEYS = 5
MISSING = "N/A"


@dataclass(frozen=True)
class GroupKey:
    name: str                       # tel que saisi : « service » ou « @http.status_code »
    column: str | None = None       # colonne de `logs` pour les clés simples
    path: tuple[str, ...] = ()      # chemin d'attribut
    flat: str = ""                  # forme à plat du chemin

    @property
    def tag(self) -> str:
        """Nom d'étiquette d'une série tirée des journaux (« @http.status_code » → « http.status_code »)."""
        return self.name[1:].lower() if self.name.startswith("@") else self.name


def parse_key(name) -> GroupKey:
    if not isinstance(name, str) or not name.strip():
        raise QueryError("clé de regroupement vide")
    name = name.strip()
    if name in COLUMNS:
        return GroupKey(name, COLUMNS[name])
    if name.startswith("@"):
        path = L.attribute_path(name[1:])
        return GroupKey(name, None, tuple(path), name[1:])
    raise QueryError(f"clé de regroupement inconnue : « {name} » (service host status source @attribut)")


def parse_keys(by) -> list[GroupKey]:
    if by is None:
        return []
    if not isinstance(by, list):
        raise QueryError("by doit être une liste")
    keys: list[GroupKey] = []
    for name in by:
        key = parse_key(name)
        if any(k.name == key.name for k in keys):
            continue
        keys.append(key)
    if len(keys) > MAX_KEYS:
        raise QueryError(f"{MAX_KEYS} clés de regroupement au plus")
    return keys


def sql_select(keys: list[GroupKey], prefix: str = "") -> tuple[list[str], list]:
    """Expressions `… AS g<i>` et leurs paramètres (à placer AVANT ceux du WHERE)."""
    exprs, params = [], []
    for i, k in enumerate(keys):
        if k.column:
            exprs.append(f"{prefix}{k.column} AS g{i}")
        else:
            exprs.append(f"coalesce({prefix}attributes #>> %s::text[], {prefix}attributes ->> %s) AS g{i}")
            params += [list(k.path), k.flat]
    return exprs, params


def sql_group(keys: list[GroupKey]) -> str:
    """Clause GROUP BY positionnelle (les expressions à paramètres ne se répètent pas)."""
    return ", ".join(str(i + 1) for i in range(len(keys)))


def row_groups(keys: list[GroupKey], row: dict) -> dict[str, str]:
    """Valeurs de groupe d'une ligne rendue par `sql_select` (g0, g1…), clé → texte ou « N/A »."""
    out = {}
    for i, k in enumerate(keys):
        v = row.get(f"g{i}")
        out[k.name] = MISSING if v is None else str(v)
    return out


def line_value(key: GroupKey, line) -> str | None:
    """Valeur de la clé pour une ligne vue par `get()` (LogView de l'ingestion ou dictionnaire)."""
    if key.column:
        v = line.get(key.column)
        return None if v is None else str(v)
    attrs = line.get("attributes")
    if not isinstance(attrs, dict):
        return None
    v = logs_match.jsonb_path(attrs, list(key.path))
    if v is logs_match._MISSING:
        v = attrs.get(key.flat)
    return logs_match.jsonb_text(v)


def line_number(path: tuple[str, ...], flat: str, line) -> float | None:
    """Valeur numérique d'un attribut (nombre JSON, ou texte numérique), sinon None."""
    attrs = line.get("attributes")
    if not isinstance(attrs, dict):
        return None
    v = logs_match.jsonb_path(attrs, list(path))
    if v is logs_match._MISSING:
        v = attrs.get(flat)
    if isinstance(v, bool) or v is None:
        return None
    if isinstance(v, (int, float)):
        f = float(v)
    elif isinstance(v, str):
        try:
            f = float(v.strip())
        except ValueError:
            return None
    else:
        return None
    return f if f == f and f not in (float("inf"), float("-inf")) else None
