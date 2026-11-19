-- 003 : filtres d'exclusion des journaux (contrat § 11), appliqués à l'arrivée, avant l'écriture.
-- Idempotent. Les droits du compte applicatif viennent de `migrate` (HYPHA_GRANT_TO) et des droits
-- par défaut posés sur le schéma : aucun GRANT ici.
SET search_path TO hypha, public;

CREATE TABLE IF NOT EXISTS hypha.log_filters (
  id bigserial PRIMARY KEY,
  org_id bigint NOT NULL REFERENCES hypha.orgs(id) ON DELETE CASCADE,
  name text NOT NULL,
  query text NOT NULL,                 -- langage des journaux du § 7 (service:, status:, host:, source:, @attr:, mots, "phrase", -, OR)
  action text NOT NULL DEFAULT 'exclude' CHECK (action IN ('exclude','sample')),
  sample_rate real NOT NULL DEFAULT 0 CHECK (sample_rate >= 0 AND sample_rate <= 1),  -- part GARDÉE si action = sample
  enabled boolean NOT NULL DEFAULT true,
  position int NOT NULL DEFAULT 0,     -- ordre d'évaluation ; le premier filtre qui correspond décide
  excluded_total bigint NOT NULL DEFAULT 0,
  created_by bigint REFERENCES hypha.users(id) ON DELETE SET NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS log_filters_org ON hypha.log_filters (org_id, position);

INSERT INTO schema_version (version) VALUES (3) ON CONFLICT (version) DO NOTHING;
