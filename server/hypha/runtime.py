"""État partagé d'un processus API : configuration, base, caches mémoire.

Chaque processus (et chaque worker uvicorn) a ses propres caches ; ils ne portent que des
correspondances stables (clé → org, agent → hôte, série → id), si bien que deux nœuds qui ne
se voient pas restent justes.
"""
from dataclasses import dataclass, field

from .config import Settings
from .db import Database, PartitionMap
from .ingest.otlp import CumulativeState
from .util import RateLimiter, TTLCache


@dataclass
class Runtime:
    settings: Settings
    db: Database
    partitions: PartitionMap = field(default_factory=PartitionMap)
    ingest_keys: TTLCache = field(default_factory=lambda: TTLCache(60))
    app_keys: TTLCache = field(default_factory=lambda: TTLCache(60))
    sessions: TTLCache = field(default_factory=lambda: TTLCache(5))
    hosts: TTLCache = field(default_factory=lambda: TTLCache(3600))
    hosts_by_name: TTLCache = field(default_factory=lambda: TTLCache(60))
    rum_apps: TTLCache = field(default_factory=lambda: TTLCache(60))
    series: dict = field(default_factory=dict)
    key_touched: dict = field(default_factory=dict)
    otlp_state: CumulativeState = field(default_factory=CumulativeState)
    ingest_limiter: RateLimiter | None = None
    rum_limiter: RateLimiter | None = None
    login_limiter: RateLimiter = field(default_factory=lambda: RateLimiter(10))
    oidc: object | None = None
    log_filters: TTLCache = field(default_factory=lambda: TTLCache(60))   # org → filtres compilés
    filter_pending: dict = field(default_factory=dict)                     # (org, filtre) → lignes jetées
    filter_flushed_at: float = 0.0
    bad_filters: set = field(default_factory=set)

    def __post_init__(self):
        self.ingest_limiter = RateLimiter(self.settings.ingest_rate_per_min)
        self.rum_limiter = RateLimiter(self.settings.rum_rate_per_min)


_current: Runtime | None = None


def set_runtime(rt: Runtime | None) -> None:
    global _current
    _current = rt


def rt() -> Runtime:
    if _current is None:
        raise RuntimeError("runtime HYPHA non initialisé")
    return _current
