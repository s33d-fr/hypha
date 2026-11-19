-- 009 : HYPHA 0.4.7, formules et facturation (docs/OFFRE.md, contrat § 52). Idempotent.
-- Exécuté par hypha_owner. Aucune réécriture de table : colonnes à défaut constant (instantané depuis
-- PostgreSQL 11), petites tables neuves, vues ; rien ne touche les partitions de télémétrie existantes.
SET search_path TO hypha, public;
-- Verrous brefs (orgs et usage_daily sont lus et écrits sans cesse) : échec net plutôt qu'une file d'attente.
SET LOCAL lock_timeout = '10s';

-- ── Formule de chaque espace ─────────────────────────────────────────────────────────────
-- 'free' (défaut d'origine, jamais lu) devient 'decouverte'. L'espace de S33D (n° 1, slug s33d) est
-- 'interne' : ni quota ni facturation. En auto-hébergé, la formule n'est jamais lue (HYPHA_MODE).
ALTER TABLE hypha.orgs ALTER COLUMN plan SET DEFAULT 'decouverte';
UPDATE hypha.orgs SET plan = 'decouverte' WHERE plan NOT IN ('interne', 'decouverte', 'pro', 'entreprise');
UPDATE hypha.orgs SET plan = 'interne' WHERE id = 1 AND slug = 's33d' AND plan <> 'interne';
ALTER TABLE hypha.orgs DROP CONSTRAINT IF EXISTS orgs_plan_check;
ALTER TABLE hypha.orgs ADD CONSTRAINT orgs_plan_check CHECK (plan IN ('interne', 'decouverte', 'pro', 'entreprise'));

-- ── Abonnement Stripe d'un espace ────────────────────────────────────────────────────────
-- Deux abonnements au plus : `usage_subscription_id` (mensuel, à l'usage, calé sur le 1er du mois
-- UTC, porte aussi le forfait Entreprise) et `commit_subscription_id` (annuel, hôtes engagés).
CREATE TABLE IF NOT EXISTS hypha.org_billing (
  org_id                  bigint PRIMARY KEY REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  stripe_customer_id      text UNIQUE,
  usage_subscription_id   text,
  commit_subscription_id  text,
  status                  text NOT NULL DEFAULT 'none',      -- statut Stripe de l'abonnement à l'usage
  wanted_plan             text CHECK (wanted_plan IS NULL OR wanted_plan IN ('pro', 'entreprise')),
  billing_interval        text NOT NULL DEFAULT 'month' CHECK (billing_interval IN ('month', 'year')),
  committed_hosts         int NOT NULL DEFAULT 0 CHECK (committed_hosts >= 0),
  period_start            timestamptz,
  period_end              timestamptz,
  commit_period_end       timestamptz,
  cancel_at               timestamptz,
  retention_days          smallint NOT NULL DEFAULT 15 CHECK (retention_days IN (15, 30, 90)),
  discount                text CHECK (discount IS NULL OR discount IN ('association')),
  company                 text,
  billing_email           text,
  country                 text,
  vat_id                  text,
  vat_status              text,                              -- verified | unverified | pending | null
  tax_mode                text NOT NULL DEFAULT 'fr20' CHECK (tax_mode IN ('fr20', 'reverse')),
  last_sync_at            timestamptz,
  last_report             jsonb NOT NULL DEFAULT '{}',
  last_error              text,
  updated_at              timestamptz NOT NULL DEFAULT now()
);

-- Événements Stripe reçus par le webhook HYPHA : rejeu sans double effet, et trace de ce qui a été fait.
CREATE TABLE IF NOT EXISTS hypha.stripe_events (
  id           text PRIMARY KEY,
  type         text NOT NULL,
  org_id       bigint,
  outcome      text NOT NULL DEFAULT '',
  received_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS stripe_events_received ON hypha.stripe_events (received_at);

-- ── Consommation (contrat § 30, complété) ───────────────────────────────────────────────
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS span_bytes bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS synthetic_runs bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS synthetic_billable_runs bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS explanations bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS sms bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS refused_log_lines bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS refused_spans bigint NOT NULL DEFAULT 0;
ALTER TABLE hypha.usage_daily ADD COLUMN IF NOT EXISTS retention_days smallint;

-- Hôtes et équipements vus dans chaque heure UTC (facturation proratisée à l'heure, pas au pic).
CREATE TABLE IF NOT EXISTS hypha.usage_hourly (
  org_id   bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  hour     timestamptz NOT NULL,
  hosts    int NOT NULL DEFAULT 0,
  devices  int NOT NULL DEFAULT 0,
  PRIMARY KEY (org_id, hour)
);

-- Hôtes refusés par une limite de formule : montrés dans l'interface, jamais en silence.
CREATE TABLE IF NOT EXISTS hypha.org_refused_hosts (
  org_id     bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  agent_id   uuid NOT NULL,
  hostname   text NOT NULL,
  kind       text NOT NULL,
  first_at   timestamptz NOT NULL DEFAULT now(),
  last_at    timestamptz NOT NULL DEFAULT now(),
  attempts   bigint NOT NULL DEFAULT 1,
  PRIMARY KEY (org_id, agent_id)
);

-- ── Conservation longue des journaux et des traces (option Pro : 30 ou 90 jours) ─────────
-- Les partitions journalières de `logs` et `spans` sont communes à tous les espaces et purgées à
-- 15 jours. Un jour clos (plus aucune ligne acceptable : fenêtre d'ingestion de 14 jours) est copié,
-- pour les seuls espaces qui ont l'option, dans `logs_long` / `spans_long` (partitions mensuelles,
-- purgées à 92 jours par la même maintenance), la nuit, par le worker. Les vues `*_etendus` lisent
-- `logs` après la borne de copie et `*_long` avant : aucun doublon, aucun trou.
CREATE TABLE IF NOT EXISTS hypha.logs_long (LIKE hypha.logs INCLUDING DEFAULTS) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS logs_long_org_ts ON hypha.logs_long (org_id, ts DESC, id DESC);
CREATE INDEX IF NOT EXISTS logs_long_org_service_ts ON hypha.logs_long (org_id, service, ts DESC);
CREATE INDEX IF NOT EXISTS logs_long_org_host_ts ON hypha.logs_long (org_id, host_id, ts DESC);
CREATE INDEX IF NOT EXISTS logs_long_org_status_ts ON hypha.logs_long (org_id, status, ts DESC);
CREATE INDEX IF NOT EXISTS logs_long_attrs ON hypha.logs_long USING gin (attributes jsonb_path_ops);
CREATE INDEX IF NOT EXISTS logs_long_trace ON hypha.logs_long (trace_id) WHERE trace_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS hypha.spans_long (LIKE hypha.spans INCLUDING DEFAULTS INCLUDING CONSTRAINTS)
  PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS spans_long_org_trace ON hypha.spans_long (org_id, trace_id);
CREATE INDEX IF NOT EXISTS spans_long_org_service_ts ON hypha.spans_long (org_id, service, ts DESC);
CREATE INDEX IF NOT EXISTS spans_long_org_roots ON hypha.spans_long (org_id, ts DESC) WHERE parent_span_id IS NULL;

INSERT INTO hypha.partition_policy (parent, step, retention, premake) VALUES
    ('logs_long',  'month', '92 days', 1),
    ('spans_long', 'month', '92 days', 1)
ON CONFLICT (parent) DO NOTHING;

CREATE TABLE IF NOT EXISTS hypha.long_retention_state (
  parent        text PRIMARY KEY CHECK (parent IN ('logs', 'spans')),
  copied_until  timestamptz NOT NULL           -- jours < copied_until : traités (copiés pour les espaces concernés)
);
INSERT INTO hypha.long_retention_state (parent, copied_until)
SELECT x, (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') - interval '15 days'
  FROM unnest(ARRAY['logs', 'spans']) AS x
ON CONFLICT (parent) DO NOTHING;

-- Espaces dont des lignes sont dans *_long : purge de nuit au-delà de leur option (30 jours), ou de
-- tout ce qu'ils y ont quand l'option s'arrête (retour à 15 jours, fin de l'abonnement).
CREATE TABLE IF NOT EXISTS hypha.long_retention_orgs (
  org_id          bigint PRIMARY KEY REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  retention_days  smallint NOT NULL,
  first_day       date NOT NULL,
  last_day        date NOT NULL
);

CREATE OR REPLACE FUNCTION hypha.long_boundary(p_parent text) RETURNS timestamptz
LANGUAGE sql STABLE AS $$ SELECT copied_until FROM hypha.long_retention_state WHERE parent = p_parent $$;

CREATE OR REPLACE VIEW hypha.logs_etendus AS
  SELECT * FROM hypha.logs WHERE ts >= hypha.long_boundary('logs')
  UNION ALL
  SELECT * FROM hypha.logs_long WHERE ts < hypha.long_boundary('logs');

CREATE OR REPLACE VIEW hypha.spans_etendus AS
  SELECT * FROM hypha.spans WHERE ts >= hypha.long_boundary('spans')
  UNION ALL
  SELECT * FROM hypha.spans_long WHERE ts < hypha.long_boundary('spans');

SELECT hypha.maintain_partitions();
INSERT INTO hypha.schema_version (version) VALUES (9) ON CONFLICT (version) DO NOTHING;
