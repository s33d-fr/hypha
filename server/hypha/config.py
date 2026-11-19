"""Configuration lue dans l'environnement, complétée par un fichier `.env` s'il existe.

Le `.env` ne remplace jamais une variable déjà posée (systemd `EnvironmentFile`, docker `env_file`) :
l'environnement du processus reste la référence.
"""
import os
from dataclasses import dataclass, field
from functools import lru_cache
from pathlib import Path

# server/hypha/config.py -> server/ -> racine du dépôt : le schéma vit à côté de server/.
SERVER_DIR = Path(__file__).resolve().parent.parent
DEFAULT_SCHEMA_DIR = SERVER_DIR.parent / "schema"


def _load_dotenv() -> None:
    candidates = []
    if os.environ.get("HYPHA_ENV_FILE"):
        candidates.append(Path(os.environ["HYPHA_ENV_FILE"]))
    candidates += [Path.cwd() / ".env", SERVER_DIR / ".env"]
    for path in candidates:
        if not path.is_file():
            continue
        for line in path.read_text(encoding="utf-8").splitlines():
            line = line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            if key.startswith("export "):
                key = key[7:].strip()
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in "\"'":
                value = value[1:-1]
            os.environ.setdefault(key, value)
        return


def _env(name: str, default: str = "") -> str:
    return os.environ.get(name, default).strip()


def _bool(name: str, default: bool) -> bool:
    raw = _env(name)
    if not raw:
        return default
    return raw.lower() in ("1", "true", "yes", "oui", "on")


def _int(name: str, default: int) -> int:
    raw = _env(name)
    return int(raw) if raw else default


@dataclass(frozen=True)
class Settings:
    mode: str
    db_write: str
    db_read: str
    db_migrate: str
    public_url: str
    listen_host: str
    listen_port: int
    secret: bytes
    oidc_issuer: str
    oidc_client_id: str
    oidc_client_secret: str
    smtp_host: str
    smtp_port: int
    smtp_user: str
    smtp_password: str
    smtp_from: str
    smtp_security: str          # starttls | ssl | none
    admin_login: str
    admin_password: str
    org_name: str
    schema_dir: Path
    grant_to: str
    log_level: str
    access_log: bool
    trusted_proxies: str
    workers: int
    pool_min: int
    pool_max: int
    agent_version: str
    agent_image: str
    synthetics_location: str
    allow_private_targets: bool
    ingest_rate_per_min: int
    rum_rate_per_min: int
    llm_url: str                # API de type Ollama ; vide = explication des pannes coupée
    llm_model: str
    llm_timeout_s: float
    otel_endpoint: str          # relais OTLP/HTTP (agent local) ; vide = aucune trace du serveur
    otel_sample: float          # part des traces racines gardées (0 à 1)
    install_dir: str = "/product/hypha/www/install"     # binaires publiés de l'agent (version.txt, § 27)
    stripe_secret_key: str = field(default="", repr=False)      # SaaS seulement ; jamais journalisée
    stripe_webhook_secret: str = field(default="", repr=False)  # signature du webhook HYPHA (whsec_…)
    billing_open: bool = False      # HYPHA_FACTURATION=ouverte : les abonnements payants s'ouvrent

    @property
    def billing_enabled(self) -> bool:
        """Facturation possible : SaaS avec une clé Stripe (l'auto-hébergé n'a ni formule ni Stripe)."""
        return self.saas and bool(self.stripe_secret_key)

    @property
    def explain_enabled(self) -> bool:
        return bool(self.llm_url and self.llm_model)

    @property
    def saas(self) -> bool:
        return self.mode == "saas"

    @property
    def oidc_enabled(self) -> bool:
        return bool(self.oidc_issuer and self.oidc_client_id)

    @property
    def cookie_secure(self) -> bool:
        # Un auto-hébergé servi en http simple (réseau local) ne recevrait jamais un cookie Secure.
        return not self.public_url.startswith("http://")


@lru_cache(maxsize=1)
def get_settings() -> Settings:
    _load_dotenv()
    mode = _env("HYPHA_MODE", "saas").lower()
    if mode not in ("saas", "selfhosted"):
        raise SystemExit(f"HYPHA_MODE invalide : {mode!r} (attendu saas ou selfhosted)")
    db_write = _env("HYPHA_DB_WRITE")
    listen = _env("HYPHA_LISTEN", "0.0.0.0:8231")
    host, _, port = listen.rpartition(":")
    secret_hex = _env("HYPHA_SECRET")
    try:
        secret = bytes.fromhex(secret_hex) if secret_hex else b""
    except ValueError:
        raise SystemExit("HYPHA_SECRET doit être en hexadécimal (64 caractères)")
    schema_dir = _env("HYPHA_SCHEMA_DIR")
    return Settings(
        mode=mode,
        db_write=db_write,
        db_read=_env("HYPHA_DB_READ") or db_write,
        db_migrate=_env("HYPHA_DB_MIGRATE") or db_write,
        public_url=_env("HYPHA_PUBLIC_URL", "https://hypha.s33d.fr").rstrip("/"),
        listen_host=host or "0.0.0.0",
        listen_port=int(port or 8231),
        secret=secret,
        oidc_issuer=_env("HYPHA_OIDC_ISSUER", "https://auth.s33d.fr" if mode == "saas" else "").rstrip("/"),
        oidc_client_id=_env("HYPHA_OIDC_CLIENT_ID", "hypha-app" if mode == "saas" else ""),
        oidc_client_secret=_env("HYPHA_OIDC_CLIENT_SECRET"),
        smtp_host=_env("HYPHA_SMTP_HOST"),
        smtp_port=_int("HYPHA_SMTP_PORT", 587),
        smtp_user=_env("HYPHA_SMTP_USER"),
        smtp_password=_env("HYPHA_SMTP_PASSWORD"),
        smtp_from=_env("HYPHA_SMTP_FROM"),
        smtp_security=_env("HYPHA_SMTP_SECURITY", "starttls").lower(),
        admin_login=_env("HYPHA_ADMIN_LOGIN").lower(),
        admin_password=os.environ.get("HYPHA_ADMIN_PASSWORD", ""),
        org_name=_env("HYPHA_ORG_NAME", "HYPHA"),
        schema_dir=Path(schema_dir) if schema_dir else DEFAULT_SCHEMA_DIR,
        grant_to=_env("HYPHA_GRANT_TO"),
        log_level=_env("HYPHA_LOG_LEVEL", "INFO").upper(),
        access_log=_bool("HYPHA_ACCESS_LOG", False),
        trusted_proxies=_env("HYPHA_TRUSTED_PROXIES", "127.0.0.1"),
        workers=max(1, _int("HYPHA_WORKERS", 1)),
        pool_min=_int("HYPHA_POOL_MIN", 2),
        pool_max=_int("HYPHA_POOL_MAX", 16),
        agent_version=_env("HYPHA_AGENT_VERSION", "0.4.3"),
        agent_image=_env("HYPHA_AGENT_IMAGE", "registre-vm.s33d.fr/hypha/agent-docker"),
        synthetics_location=_env("HYPHA_SYNTHETICS_LOCATION", "fr-s33d"),
        # En SaaS, un test ou un webhook vers une adresse privée sonderait le réseau d'S33D.
        allow_private_targets=_bool("HYPHA_ALLOW_PRIVATE_TARGETS", mode == "selfhosted"),
        ingest_rate_per_min=_int("HYPHA_INGEST_RATE_PER_MIN", 0),
        rum_rate_per_min=_int("HYPHA_RUM_RATE_PER_MIN", 120),
        llm_url=_env("HYPHA_LLM_URL").rstrip("/"),
        llm_model=_env("HYPHA_LLM_MODEL", "qwen3.5:35b"),
        llm_timeout_s=float(_env("HYPHA_LLM_TIMEOUT_S") or 90),
        otel_endpoint=_env("HYPHA_OTEL_ENDPOINT").rstrip("/"),
        otel_sample=_sample(_env("HYPHA_OTEL_SAMPLE")),
        install_dir=_env("HYPHA_INSTALL_DIR", "/product/hypha/www/install").rstrip("/"),
        stripe_secret_key=_env("HYPHA_STRIPE_SECRET_KEY") if mode == "saas" else "",
        stripe_webhook_secret=_env("HYPHA_STRIPE_WEBHOOK_SECRET") if mode == "saas" else "",
        billing_open=mode == "saas" and _env("HYPHA_FACTURATION", "fermee").lower() in ("ouverte", "open"),
    )


def _sample(raw: str) -> float:
    """Taux d'échantillonnage : 0,2 par défaut, borné à [0, 1] ; une valeur illisible garde le défaut."""
    try:
        value = float(raw) if raw else 0.2
    except ValueError:
        return 0.2
    return min(1.0, max(0.0, value))
