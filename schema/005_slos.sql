-- 005 : SLO (contrat § 13). Idempotent. Exécuté par hypha_owner.
SET search_path TO hypha, public;

CREATE TABLE IF NOT EXISTS hypha.slos (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name text NOT NULL,
  description text NOT NULL DEFAULT '',
  type text NOT NULL CHECK (type IN ('monitor','metric')),
  monitor_id bigint REFERENCES hypha.monitors(id) ON DELETE CASCADE,   -- type monitor
  good_query text,      -- type metric : ex. sum:trace.hits{service:api,!status:error}
  total_query text,     -- type metric : ex. sum:trace.hits{service:api}
  target real NOT NULL CHECK (target > 0 AND target < 100),          -- ex. 99.9
  warning real CHECK (warning IS NULL OR (warning > 0 AND warning < 100)),
  timeframe text NOT NULL DEFAULT '30d' CHECK (timeframe IN ('7d','30d','90d')),
  tags jsonb NOT NULL DEFAULT '{}',
  created_by bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS slos_org ON hypha.slos (org_id);

-- Moniteur de type `slo` (§ 13) : la contrainte de 001 sur monitors.type ne le connaît pas.
ALTER TABLE hypha.monitors DROP CONSTRAINT IF EXISTS monitors_type_check;
ALTER TABLE hypha.monitors ADD CONSTRAINT monitors_type_check
  CHECK (type IN ('metric','service_check','log','host','synthetic','apm','slo'));

INSERT INTO schema_version (version) VALUES (5) ON CONFLICT (version) DO NOTHING;
