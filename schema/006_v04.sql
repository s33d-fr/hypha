-- 006 : HYPHA 0.4.0 (contrat §§ 14 à 30). Idempotent. Exécuté par hypha_owner.
-- Équipements réseau (SNMP), inventaire, maintenances, acquittements, escalades, incidents,
-- pages de statut, rapports, métriques tirées des journaux, masquage des données sensibles,
-- vues enregistrées, journal d'audit, suivi des erreurs, catalogue de services, flotte d'agents,
-- actions à distance (réparation validée d'un clic), synthétiques multi-étapes et privés,
-- requêtes des bases (DBM), métadonnées des métriques, consommation par organisation.
SET search_path TO hypha, public;

-- ── Hôtes : équipements réseau collectés par un agent (§ 14) ─────────────────────────────
ALTER TABLE hypha.hosts ADD COLUMN IF NOT EXISTS kind text NOT NULL DEFAULT 'agent';
ALTER TABLE hypha.hosts ADD COLUMN IF NOT EXISTS via_host_id bigint REFERENCES hypha.hosts(id) ON DELETE SET NULL;
ALTER TABLE hypha.hosts ADD COLUMN IF NOT EXISTS device jsonb NOT NULL DEFAULT '{}';
DO $$ BEGIN
  ALTER TABLE hypha.hosts ADD CONSTRAINT hosts_kind_check CHECK (kind IN ('agent','network_device'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

-- ── Inventaire matériel et logiciel (§ 15) ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.host_inventory (
  host_id     bigint PRIMARY KEY REFERENCES hypha.hosts(id) ON DELETE CASCADE,
  org_id      bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  ts          timestamptz NOT NULL DEFAULT now(),
  hardware    jsonb NOT NULL DEFAULT '{}',   -- fabricant, modèle, n° de série, BIOS, CPU, barrettes, disques
  os          jsonb NOT NULL DEFAULT '{}',   -- nom, version, noyau, installation, redémarrage requis
  updates     jsonb NOT NULL DEFAULT '{}',   -- {"pending":12,"security":3,"list":[…]}
  packages_count int NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS host_inventory_org ON hypha.host_inventory (org_id);

CREATE TABLE IF NOT EXISTS hypha.host_packages (
  org_id   bigint NOT NULL,
  host_id  bigint NOT NULL REFERENCES hypha.hosts(id) ON DELETE CASCADE,
  name     text NOT NULL,
  version  text NOT NULL DEFAULT '',
  arch     text NOT NULL DEFAULT '',
  source   text NOT NULL DEFAULT '',          -- dpkg, rpm, windows, snap…
  PRIMARY KEY (host_id, name, arch)
);
CREATE INDEX IF NOT EXISTS host_packages_org_name ON hypha.host_packages (org_id, name);

-- ── Moniteurs : priorité, options, nouveaux types (§§ 16 à 19) ───────────────────────────
ALTER TABLE hypha.monitors ADD COLUMN IF NOT EXISTS priority smallint;
ALTER TABLE hypha.monitors ADD COLUMN IF NOT EXISTS options jsonb NOT NULL DEFAULT '{}';
DO $$ BEGIN
  ALTER TABLE hypha.monitors ADD CONSTRAINT monitors_priority_check CHECK (priority IS NULL OR priority BETWEEN 1 AND 5);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
ALTER TABLE hypha.monitors DROP CONSTRAINT IF EXISTS monitors_type_check;
ALTER TABLE hypha.monitors ADD CONSTRAINT monitors_type_check CHECK (type IN
  ('metric','service_check','log','host','synthetic','apm','slo',
   'anomaly','forecast','outlier','composite','change','event','process','error_tracking'));

ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS acked_by bigint REFERENCES hypha.users(id) ON DELETE SET NULL;
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS acked_at timestamptz;
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS ack_comment text;
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS flapping boolean NOT NULL DEFAULT false;
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS escalation_step int NOT NULL DEFAULT 0;
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS suppressed text;   -- downtime | dependency | flapping
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS pending_state text; -- tolérances : état en attente de confirmation
ALTER TABLE hypha.monitor_states ADD COLUMN IF NOT EXISTS pending_count int NOT NULL DEFAULT 0;
ALTER TABLE hypha.alert_events  ADD COLUMN IF NOT EXISTS suppressed text;

-- ── Maintenances planifiées (§ 16) ───────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.downtimes (
  id          bigserial PRIMARY KEY,
  org_id      bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name        text NOT NULL,
  message     text NOT NULL DEFAULT '',
  scope       jsonb NOT NULL DEFAULT '{}',    -- {"monitor_ids":[…],"tags":{"host":"web-01"},"groups":["host:web-01,…"]}
  start_ts    timestamptz NOT NULL,
  end_ts      timestamptz,                    -- NULL = sans fin
  recurrence  jsonb,                          -- {"type":"daily|weekly|monthly","days":[1..7],"day_of_month":1,"start":"02:00","duration_s":3600,"tz":"Europe/Paris","until":ts}
  enabled     boolean NOT NULL DEFAULT true,
  created_by  bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS downtimes_org ON hypha.downtimes (org_id);

-- ── Canaux de notification : nouveaux types (§ 18) ───────────────────────────────────────
ALTER TABLE hypha.notification_channels DROP CONSTRAINT IF EXISTS notification_channels_kind_check;
ALTER TABLE hypha.notification_channels ADD CONSTRAINT notification_channels_kind_check CHECK (kind IN
  ('email','webhook','slack','teams','telegram','discord','googlechat','mattermost','ntfy','pagerduty','opsgenie'));

-- ── Incidents (§ 20) ─────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.incidents (
  id               bigserial PRIMARY KEY,
  org_id           bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  number           int NOT NULL,                -- numéro dans l'organisation (INC-12)
  title            text NOT NULL,
  severity         smallint NOT NULL DEFAULT 3 CHECK (severity BETWEEN 1 AND 5),   -- SEV-1 … SEV-5
  status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active','stable','resolved')),
  commander_id     bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  customer_impact  text NOT NULL DEFAULT '',
  summary          text NOT NULL DEFAULT '',
  postmortem       text NOT NULL DEFAULT '',     -- Markdown
  fields           jsonb NOT NULL DEFAULT '{}',  -- {"services":[…],"hosts":[…],"teams":[…]}
  status_page_ids  bigint[] NOT NULL DEFAULT '{}',
  public_message   text NOT NULL DEFAULT '',
  detected_at      timestamptz NOT NULL DEFAULT now(),
  resolved_at      timestamptz,
  created_by       bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, number)
);
CREATE TABLE IF NOT EXISTS hypha.incident_timeline (
  id           bigserial PRIMARY KEY,
  incident_id  bigint NOT NULL REFERENCES hypha.incidents(id) ON DELETE CASCADE,
  ts           timestamptz NOT NULL DEFAULT now(),
  kind         text NOT NULL CHECK (kind IN ('note','status','severity','alert','link','public_update','system','ai')),
  author_id    bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  body         text NOT NULL DEFAULT '',
  data         jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX IF NOT EXISTS incident_timeline_inc ON hypha.incident_timeline (incident_id, ts);
CREATE TABLE IF NOT EXISTS hypha.incident_links (
  incident_id  bigint NOT NULL REFERENCES hypha.incidents(id) ON DELETE CASCADE,
  kind         text NOT NULL CHECK (kind IN ('monitor','host','slo','synthetic','service','issue')),
  ref          text NOT NULL,                  -- id, ou « monitor_id|group » pour un groupe d'alerte
  PRIMARY KEY (incident_id, kind, ref)
);

-- ── Pages de statut publiques (§ 21) ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.status_pages (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  slug         text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}$'),
  title        text NOT NULL,
  description  text NOT NULL DEFAULT '',
  components   jsonb NOT NULL DEFAULT '[]',    -- [{"name","group","kind":"monitor|synthetic|slo|host","ref_id"}]
  public       boolean NOT NULL DEFAULT false,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);

-- ── Rapports programmés (§ 22) ───────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.report_schedules (
  id            bigserial PRIMARY KEY,
  org_id        bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name          text NOT NULL,
  kind          text NOT NULL CHECK (kind IN ('summary','dashboard','slo')),
  dashboard_id  bigint REFERENCES hypha.dashboards(id) ON DELETE CASCADE,
  channel_ids   bigint[] NOT NULL DEFAULT '{}',
  schedule      jsonb NOT NULL DEFAULT '{}',   -- {"every":"day|week|month","weekday":1,"day":1,"hour":8,"tz":"Europe/Paris"}
  period_s      int NOT NULL DEFAULT 604800,
  enabled       boolean NOT NULL DEFAULT true,
  last_sent_at  timestamptz,
  created_by    bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at    timestamptz NOT NULL DEFAULT now()
);

-- ── Journaux : métriques tirées des journaux, masquage, vues (§ 23) ──────────────────────
CREATE TABLE IF NOT EXISTS hypha.log_metrics (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name         text NOT NULL CHECK (name ~ '^logs\.[a-z0-9_.]{1,120}$'),
  query        text NOT NULL DEFAULT '',
  group_by     text[] NOT NULL DEFAULT '{}',    -- service host status source @chemin.attribut (5 au plus)
  aggregation  text NOT NULL DEFAULT 'count' CHECK (aggregation IN ('count','distribution')),
  attribute    text,                            -- distribution : chemin numérique (@duration)
  enabled      boolean NOT NULL DEFAULT true,
  created_by   bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);
CREATE TABLE IF NOT EXISTS hypha.sensitive_rules (
  id             bigserial PRIMARY KEY,
  org_id         bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name           text NOT NULL,
  builtin        text,                          -- clé d'une règle livrée (secrets, carte, iban, email, phone_fr, ipv4…)
  pattern        text,                          -- expression régulière propre si builtin est NULL
  replacement    text NOT NULL DEFAULT '[masqué]',
  scope_query    text NOT NULL DEFAULT '',      -- journaux visés (langage du § 7), vide = tous
  targets        text[] NOT NULL DEFAULT '{message,attributes}',
  enabled        boolean NOT NULL DEFAULT true,
  position       int NOT NULL DEFAULT 0,
  matched_total  bigint NOT NULL DEFAULT 0,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  CHECK (builtin IS NOT NULL OR pattern IS NOT NULL)
);
CREATE INDEX IF NOT EXISTS sensitive_rules_org ON hypha.sensitive_rules (org_id, position);
CREATE TABLE IF NOT EXISTS hypha.saved_views (
  id          bigserial PRIMARY KEY,
  org_id      bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  page        text NOT NULL CHECK (page IN ('logs','traces','metrics','hosts','processes','errors')),
  name        text NOT NULL,
  state       jsonb NOT NULL DEFAULT '{}',     -- {"q":…,"columns":[…],"range":"1h",…}
  shared      boolean NOT NULL DEFAULT true,
  created_by  bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS saved_views_org ON hypha.saved_views (org_id, page);

-- ── Journal d'audit (§ 24) ───────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.audit_log (
  id           bigserial PRIMARY KEY,
  org_id       bigint,
  ts           timestamptz NOT NULL DEFAULT now(),
  user_id      bigint,
  actor        text NOT NULL,                  -- adresse de l'utilisateur, « clé:<nom> », « système »
  action       text NOT NULL,                  -- create update delete login logout approve reject ack mute run…
  object_type  text NOT NULL,                  -- monitor dashboard key channel member downtime incident action…
  object_id    text,
  summary      text NOT NULL DEFAULT '',
  diff         jsonb NOT NULL DEFAULT '{}',    -- {"avant":…,"apres":…} sans secret
  ip           text,
  user_agent   text
);
CREATE INDEX IF NOT EXISTS audit_log_org_ts ON hypha.audit_log (org_id, ts DESC);

-- ── Suivi des erreurs (§ 25) ─────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.error_issues (
  id             bigserial PRIMARY KEY,
  org_id         bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  fingerprint    text NOT NULL,
  origin         text NOT NULL CHECK (origin IN ('trace','log','rum')),
  service        text,
  error_type     text,
  message        text,
  stack          text,
  first_seen     timestamptz NOT NULL,
  last_seen      timestamptz NOT NULL,
  count          bigint NOT NULL DEFAULT 0,
  status         text NOT NULL DEFAULT 'open' CHECK (status IN ('open','ignored','resolved')),
  resolved_at    timestamptz,
  regressed_at   timestamptz,
  last_trace_id  text,
  last_log_id    bigint,
  last_host_id   bigint,
  UNIQUE (org_id, fingerprint)
);
CREATE INDEX IF NOT EXISTS error_issues_org_last ON hypha.error_issues (org_id, last_seen DESC);
CREATE TABLE IF NOT EXISTS hypha.error_issue_hits (
  issue_id  bigint NOT NULL REFERENCES hypha.error_issues(id) ON DELETE CASCADE,
  hour      timestamptz NOT NULL,
  count     bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (issue_id, hour)
);

-- ── Catalogue de services (§ 26) ─────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.services (
  id            bigserial PRIMARY KEY,
  org_id        bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name          text NOT NULL,
  description   text NOT NULL DEFAULT '',
  team          text NOT NULL DEFAULT '',
  owner         text NOT NULL DEFAULT '',
  tier          smallint CHECK (tier IS NULL OR tier BETWEEN 1 AND 4),
  runbook_url   text NOT NULL DEFAULT '',
  repo_url      text NOT NULL DEFAULT '',
  docs_url      text NOT NULL DEFAULT '',
  chat_url      text NOT NULL DEFAULT '',
  tags          jsonb NOT NULL DEFAULT '{}',
  depends_on    text[] NOT NULL DEFAULT '{}',   -- services dont il dépend (déclarés)
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (org_id, name)
);

-- ── Flotte d'agents et actions à distance (§§ 27, 28) ────────────────────────────────────
ALTER TABLE hypha.agents ADD COLUMN IF NOT EXISTS capabilities jsonb NOT NULL DEFAULT '{}';
ALTER TABLE hypha.agents ADD COLUMN IF NOT EXISTS config_hash text;
ALTER TABLE hypha.agents ADD COLUMN IF NOT EXISTS target_version text;
ALTER TABLE hypha.agents ADD COLUMN IF NOT EXISTS update_status jsonb NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS hypha.agent_actions (
  id           bigserial PRIMARY KEY,
  org_id       bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  host_id      bigint NOT NULL REFERENCES hypha.hosts(id) ON DELETE CASCADE,
  agent_id     uuid NOT NULL,
  kind         text NOT NULL CHECK (kind IN ('restart_service','start_service','stop_service','run_script','update_agent')),
  params       jsonb NOT NULL DEFAULT '{}',     -- {"unit":"nginx.service"} | {"service":"W3SVC"} | {"script":"vider-cache"} | {"version":"0.4.0"}
  reason       text NOT NULL DEFAULT '',
  origin       jsonb NOT NULL DEFAULT '{}',     -- {"monitor_id":6,"group":"…"} ou {"explain":true}
  status       text NOT NULL DEFAULT 'pending_approval' CHECK (status IN
                 ('pending_approval','approved','sent','running','succeeded','failed','rejected','expired','refused_by_agent')),
  proposed_by  bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  proposed_by_ai boolean NOT NULL DEFAULT false,
  approved_by  bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  approved_at  timestamptz,
  sent_at      timestamptz,
  finished_at  timestamptz,
  exit_code    int,
  output       text NOT NULL DEFAULT '',
  expires_at   timestamptz NOT NULL DEFAULT now() + interval '1 hour',
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS agent_actions_agent ON hypha.agent_actions (agent_id, status);
CREATE INDEX IF NOT EXISTS agent_actions_org ON hypha.agent_actions (org_id, created_at DESC);

-- ── Synthétiques : multi-étapes et emplacements privés (§ 29) ───────────────────────────
ALTER TABLE hypha.synthetic_tests DROP CONSTRAINT IF EXISTS synthetic_tests_kind_check;
ALTER TABLE hypha.synthetic_tests ADD CONSTRAINT synthetic_tests_kind_check CHECK (kind IN ('http','tcp','ssl','dns','icmp','multistep'));
ALTER TABLE hypha.synthetic_tests ADD COLUMN IF NOT EXISTS locations text[] NOT NULL DEFAULT '{server}';   -- server | agent:<host_id>
ALTER TABLE hypha.synthetic_tests ADD COLUMN IF NOT EXISTS steps jsonb NOT NULL DEFAULT '[]';

-- ── Bases de données : requêtes les plus coûteuses (§ 30) ────────────────────────────────
CREATE TABLE IF NOT EXISTS hypha.dbm_queries (
  org_id           bigint NOT NULL,
  host_id          bigint,
  ts               timestamptz NOT NULL,
  engine           text NOT NULL DEFAULT 'postgres',
  instance         text NOT NULL DEFAULT '',
  db               text NOT NULL DEFAULT '',
  query_id         text NOT NULL,
  query            text NOT NULL,               -- normalisée, 4 Kio au plus
  calls            bigint NOT NULL DEFAULT 0,   -- depuis l'échantillon précédent
  total_ms         double precision NOT NULL DEFAULT 0,
  mean_ms          double precision,
  rows             bigint NOT NULL DEFAULT 0,
  shared_blks_hit  bigint NOT NULL DEFAULT 0,
  shared_blks_read bigint NOT NULL DEFAULT 0
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS dbm_queries_org_ts ON hypha.dbm_queries (org_id, ts DESC);
INSERT INTO hypha.partition_policy (parent, step, retention, premake)
VALUES ('dbm_queries', 'day', '15 days', 3) ON CONFLICT (parent) DO NOTHING;

-- ── Métadonnées des métriques, tableaux partagés, consommation (§ 30) ───────────────────
CREATE TABLE IF NOT EXISTS hypha.metric_metadata (
  org_id       bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  metric       text NOT NULL,
  unit         text,
  per_unit     text,
  description  text NOT NULL DEFAULT '',
  integration  text NOT NULL DEFAULT '',
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (org_id, metric)
);
ALTER TABLE hypha.dashboards ADD COLUMN IF NOT EXISTS share_token text UNIQUE;
ALTER TABLE hypha.dashboards ADD COLUMN IF NOT EXISTS refresh_s int;
ALTER TABLE hypha.dashboards ADD COLUMN IF NOT EXISTS folder text NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS hypha.usage_daily (
  org_id        bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  day           date NOT NULL,
  log_lines     bigint NOT NULL DEFAULT 0,
  log_bytes     bigint NOT NULL DEFAULT 0,
  metric_points bigint NOT NULL DEFAULT 0,
  spans         bigint NOT NULL DEFAULT 0,
  rum_events    bigint NOT NULL DEFAULT 0,
  checks        bigint NOT NULL DEFAULT 0,
  PRIMARY KEY (org_id, day)
);

SELECT hypha.maintain_partitions();

INSERT INTO hypha.schema_version (version) VALUES (6) ON CONFLICT (version) DO NOTHING;
