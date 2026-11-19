"""Outils communs sans dépendance à la base : temps, étiquettes, clés, nettoyage des chaînes."""
import hashlib
import json
import math
import re
import secrets
import string
import time
from datetime import datetime, timedelta, timezone
from decimal import Decimal
from uuid import UUID

UTC = timezone.utc
EPOCH_2000 = datetime(2000, 1, 1, tzinfo=UTC)   # origine des seaux, la même que les agrégats SQL

TAG_KEY_RE = re.compile(r"^[a-z0-9_.\-/]{1,100}$")
METRIC_NAME_RE = re.compile(r"^[A-Za-z0-9_.\-/]{1,200}$")
KEY_RE = re.compile(r"^hph_[0-9A-Za-z]{40}$")
TAG_VALUE_MAX = 200
MAX_TAGS = 50

BASE62 = string.digits + string.ascii_letters

# Pas de regroupement « ronds » : un graphe lisible tombe toujours sur ces intervalles.
NICE_STEPS = [1, 5, 10, 15, 20, 30, 60, 120, 300, 600, 900, 1200, 1800, 3600, 7200, 10800,
              14400, 21600, 43200, 86400, 172800, 604800, 2592000]


# ── temps ──────────────────────────────────────────────────────────────────────

def now_ms() -> int:
    return int(time.time() * 1000)


def ms_to_dt(ms: int | float) -> datetime:
    return datetime.fromtimestamp(ms / 1000.0, tz=UTC)


def dt_to_ms(dt: datetime | None) -> int | None:
    if dt is None:
        return None
    if dt.tzinfo is None:
        dt = dt.replace(tzinfo=UTC)
    return int(dt.timestamp() * 1000)


def nice_step(min_seconds: float) -> int:
    for step in NICE_STEPS:
        if step >= min_seconds:
            return step
    return int(math.ceil(min_seconds))


def bucket_floor(ms: int, step_s: int) -> int:
    """Début du seau `date_bin(step, ts, 2000-01-01)` qui contient `ms`."""
    origin = dt_to_ms(EPOCH_2000)
    step_ms = step_s * 1000
    return origin + ((ms - origin) // step_ms) * step_ms


def seconds(td: timedelta) -> float:
    return td.total_seconds()


# ── chaînes et JSON ────────────────────────────────────────────────────────────

def clean_text(value, max_len: int | None = None) -> str | None:
    """Rend une chaîne acceptable par PostgreSQL : sans NUL, sans demi-substitut, bornée."""
    if value is None:
        return None
    if not isinstance(value, str):
        value = str(value)
    if "\x00" in value:
        value = value.replace("\x00", "")
    try:
        value.encode("utf-8")
    except UnicodeEncodeError:
        value = value.encode("utf-8", "replace").decode("utf-8")
    if max_len is not None and len(value) > max_len:
        value = value[:max_len]
    return value


def clean_json(obj, depth: int = 0):
    """Nettoie récursivement une valeur destinée à du jsonb (NaN, NUL, profondeur)."""
    if depth > 20:
        return None
    if obj is None or isinstance(obj, bool):
        return obj
    if isinstance(obj, int):
        # jsonb accepte les grands entiers, mais un entier Python sans borne peut être absurde.
        return obj if abs(obj) < 10 ** 30 else str(obj)
    if isinstance(obj, float):
        return obj if math.isfinite(obj) else None
    if isinstance(obj, str):
        return clean_text(obj, 65536)
    if isinstance(obj, dict):
        return {clean_text(str(k), 256): clean_json(v, depth + 1) for k, v in list(obj.items())[:500]}
    if isinstance(obj, (list, tuple)):
        return [clean_json(v, depth + 1) for v in list(obj)[:1000]]
    return clean_text(str(obj), 65536)


def jdump(obj) -> str:
    """JSON pour une colonne jsonb (COPY ou paramètre `%s::jsonb`)."""
    return json.dumps(clean_json(obj), ensure_ascii=False, separators=(",", ":"))


def json_default(obj):
    if isinstance(obj, datetime):
        return dt_to_ms(obj)
    if isinstance(obj, Decimal):
        return float(obj)
    if isinstance(obj, UUID):
        return str(obj)
    if isinstance(obj, (bytes, bytearray, memoryview)):
        return bytes(obj).hex()
    if isinstance(obj, (set, frozenset)):
        return list(obj)
    raise TypeError(f"type non sérialisable : {type(obj).__name__}")


def real_value(x) -> float | None:
    """Valeur d'une colonne `real` telle que saisie : float4 n'a que 7 chiffres significatifs, et
    99.9 relu vaut 99.90000152587890625."""
    return None if x is None else float(f"{float(x):.7g}")


def like_escape(value: str) -> str:
    """Échappe un motif LIKE (échappement par défaut : la barre oblique inverse)."""
    return value.replace("\\", "\\\\").replace("%", "\\%").replace("_", "\\_")


# ── étiquettes ─────────────────────────────────────────────────────────────────

def canonical_tags(tags: dict) -> str:
    # Forme fixée par le contrat : la même chaîne donne le même tags_hash sur tous les nœuds.
    return json.dumps(tags, sort_keys=True, separators=(",", ":"))


def tags_hash(tags: dict) -> bytes:
    return hashlib.sha256(canonical_tags(tags).encode("utf-8")).digest()


def clean_tags(raw, normalize_keys: bool = False) -> dict[str, str]:
    """Étiquettes valides seulement : clés `[a-z0-9_.\\-/]`, valeurs texte ≤ 200 caractères."""
    if not isinstance(raw, dict):
        return {}
    out: dict[str, str] = {}
    for key, value in raw.items():
        if len(out) >= MAX_TAGS:
            break
        if not isinstance(key, str):
            continue
        if normalize_keys:
            key = re.sub(r"[^a-z0-9_.\-/]", "_", key.strip().lower())[:100]
        if not TAG_KEY_RE.match(key):
            continue
        if value is None:
            continue
        if isinstance(value, bool):
            value = "true" if value else "false"
        elif isinstance(value, float) and value.is_integer():
            value = str(int(value))
        elif not isinstance(value, str):
            value = value if isinstance(value, (int, float)) else json.dumps(value, ensure_ascii=False)
            value = str(value)
        value = clean_text(value, TAG_VALUE_MAX)
        if value == "":
            continue
        out[key] = value
    return out


def group_key(by: list[str], values: dict) -> str:
    """Clé de groupe d'un moniteur : '*' sans regroupement, sinon 'host:web-01,device:sda'."""
    if not by:
        return "*"
    return ",".join(f"{k}:{values.get(k) if values.get(k) is not None else 'N/A'}" for k in by)


def parse_group_key(key: str) -> dict[str, str]:
    if not key or key == "*":
        return {}
    out = {}
    for part in key.split(","):
        k, _, v = part.partition(":")
        if k:
            out[k] = v
    return out


# ── clés d'accès ───────────────────────────────────────────────────────────────

def generate_api_key() -> str:
    return "hph_" + "".join(secrets.choice(BASE62) for _ in range(40))


def key_hash(key: str) -> bytes:
    return hashlib.sha256(key.encode("utf-8")).digest()


def key_prefix(key: str) -> str:
    return key[:12]


# ── petit cache à durée de vie ─────────────────────────────────────────────────

class TTLCache:
    """Cache mémoire borné ; une entrée expire après `ttl` secondes."""

    def __init__(self, ttl: float, max_items: int = 100_000):
        self.ttl = ttl
        self.max_items = max_items
        self._data: dict = {}

    def get(self, key, default=None):
        item = self._data.get(key)
        if item is None:
            return default
        value, expires = item
        if expires < time.monotonic():
            self._data.pop(key, None)
            return default
        return value

    def __contains__(self, key) -> bool:
        return self.get(key, _MISSING) is not _MISSING

    def set(self, key, value, ttl: float | None = None) -> None:
        if len(self._data) >= self.max_items:
            self._evict()
        self._data[key] = (value, time.monotonic() + (self.ttl if ttl is None else ttl))

    def pop(self, key) -> None:
        self._data.pop(key, None)

    def clear(self) -> None:
        self._data.clear()

    def _evict(self) -> None:
        now = time.monotonic()
        for key in [k for k, (_, exp) in self._data.items() if exp < now]:
            self._data.pop(key, None)
        if len(self._data) >= self.max_items:
            # Plus rien d'expiré : on repart de zéro plutôt que de grossir sans fin.
            self._data.clear()


_MISSING = object()


class RateLimiter:
    """Seau à jetons par clé : `per_min` requêtes par minute, rafale égale à la minute."""

    def __init__(self, per_min: int, max_keys: int = 50_000):
        self.per_min = per_min
        self.max_keys = max_keys
        self._buckets: dict = {}

    def allow(self, key) -> bool:
        if self.per_min <= 0:
            return True
        now = time.monotonic()
        tokens, last = self._buckets.get(key, (float(self.per_min), now))
        tokens = min(float(self.per_min), tokens + (now - last) * self.per_min / 60.0)
        if tokens < 1.0:
            self._buckets[key] = (tokens, now)
            return False
        if len(self._buckets) >= self.max_keys:
            self._buckets.clear()
        self._buckets[key] = (tokens - 1.0, now)
        return True
