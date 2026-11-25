-- 012 : greffons renommés (agent 0.14.0). Contrôle plugin.check, mesures plugin.duration et
-- plugin.perfdata(.warn|.crit|.min|.max), étiquette plugin, entrée « plugin:<nom> » dans l'état et
-- les capacités des agents. Les données déjà reçues, les moniteurs, leurs états, les alertes, les
-- événements et les tableaux passent sous ces noms ; le serveur range sous les mêmes noms ce que
-- publient encore les agents plus anciens (ingest/body.py).
-- Rejouable : chaque instruction ne touche que ce qui porte encore l'ancien nom.
SET search_path = hypha;

-- Forme canonique des étiquettes, identique à util.canonical_tags (json.dumps, clés triées,
-- séparateurs compacts, tout caractère hors de l'intervalle espace..~ échappé en \uXXXX).
CREATE OR REPLACE FUNCTION pg_temp.g012_ascii(s text) RETURNS text LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE
    out text := '';
    c   text;
    cp  int;
BEGIN
    IF s !~ '[^ -~]' THEN
        RETURN s;
    END IF;
    FOREACH c IN ARRAY regexp_split_to_array(s, '') LOOP
        cp := ascii(c);
        IF cp < 127 THEN
            out := out || c;
        ELSIF cp > 65535 THEN
            cp := cp - 65536;
            out := out || '\u' || lpad(to_hex(55296 + (cp >> 10)), 4, '0')
                       || '\u' || lpad(to_hex(56320 + (cp & 1023)), 4, '0');
        ELSE
            out := out || '\u' || lpad(to_hex(cp), 4, '0');
        END IF;
    END LOOP;
    RETURN out;
END $$;

CREATE OR REPLACE FUNCTION pg_temp.g012_hash(t jsonb) RETURNS bytea LANGUAGE sql IMMUTABLE AS $$
    SELECT sha256(convert_to(COALESCE(
        '{' || string_agg(pg_temp.g012_ascii(to_json(e.k)::text) || ':' || pg_temp.g012_ascii(e.v::text), ','
                          ORDER BY e.k COLLATE "C") || '}', '{}'), 'UTF8'))
      FROM jsonb_each(t) AS e(k, v)
$$;

CREATE OR REPLACE FUNCTION pg_temp.g012_tags(t jsonb) RETURNS jsonb LANGUAGE sql IMMUTABLE AS $$
    SELECT CASE WHEN t ? 'nagios_check'
                THEN (t - 'nagios_check') || jsonb_build_object('plugin', t -> 'nagios_check')
                ELSE t END
$$;

-- ── séries ─────────────────────────────────────────────────────────────────────
CREATE TEMP TABLE g012 ON COMMIT DROP AS
SELECT s.id, s.org_id, 'plugin.' || substr(s.name, 8) AS new_name, s.tags, pg_temp.g012_tags(s.tags) AS new_tags,
       s.tags_hash, NULL::bytea AS new_hash, NULL::bigint AS target
  FROM metric_series s
 WHERE s.org_id IN (SELECT id FROM orgs)
   AND s.name IN ('nagios.duration', 'nagios.perfdata', 'nagios.perfdata.warn', 'nagios.perfdata.crit',
                  'nagios.perfdata.min', 'nagios.perfdata.max');

DO $$
DECLARE
    n bigint;
BEGIN
    -- Garde-fou : l'empreinte recalculée ici doit rendre celle du serveur pour les séries actuelles.
    SELECT count(*) INTO n FROM g012 WHERE pg_temp.g012_hash(tags) <> tags_hash;
    IF n > 0 THEN
        RAISE EXCEPTION '012 : % série(s) dont l''empreinte ne se recalcule pas à l''identique, rien n''est changé', n;
    END IF;
END $$;

UPDATE g012 SET new_hash = pg_temp.g012_hash(new_tags);
UPDATE g012 g SET target = s.id
  FROM metric_series s
 WHERE s.org_id = g.org_id AND s.name = g.new_name AND s.tags_hash = g.new_hash;

-- Série sous le nouveau nom absente : renommage sur place (les points gardent leur série).
UPDATE metric_series s SET name = g.new_name, tags = g.new_tags, tags_hash = g.new_hash
  FROM g012 g
 WHERE s.id = g.id AND g.target IS NULL;

-- Série déjà créée sous le nouveau nom : ses points et agrégats reçoivent ceux de l'ancienne,
-- puis l'ancienne disparaît. Une série à la fois, par l'index (series_id, ts).
DO $$
DECLARE
    r record;
BEGIN
    FOR r IN SELECT id, target FROM g012 WHERE target IS NOT NULL LOOP
        UPDATE metric_points SET series_id = r.target WHERE series_id = r.id;
        UPDATE metric_rollup_5m o SET series_id = r.target
         WHERE o.series_id = r.id
           AND NOT EXISTS (SELECT 1 FROM metric_rollup_5m x WHERE x.series_id = r.target AND x.ts = o.ts);
        DELETE FROM metric_rollup_5m WHERE series_id = r.id;
        UPDATE metric_rollup_1h o SET series_id = r.target
         WHERE o.series_id = r.id
           AND NOT EXISTS (SELECT 1 FROM metric_rollup_1h x WHERE x.series_id = r.target AND x.ts = o.ts);
        DELETE FROM metric_rollup_1h WHERE series_id = r.id;
        DELETE FROM metric_series WHERE id = r.id;
    END LOOP;
END $$;

-- ── contrôles reçus ────────────────────────────────────────────────────────────
-- Un espace à la fois : l'index (org_id, check_name, ts) évite de parcourir toute la table.
DO $$
DECLARE
    o bigint;
BEGIN
    FOR o IN SELECT id FROM orgs LOOP
        UPDATE check_runs SET check_name = 'plugin.check', tags = pg_temp.g012_tags(tags)
         WHERE org_id = o AND check_name = 'nagios.check';
    END LOOP;
END $$;

-- ── moniteurs ──────────────────────────────────────────────────────────────────
UPDATE monitors
   SET query = regexp_replace(regexp_replace(query::text, '\mnagios_check\M', 'plugin', 'g'),
                              '\mnagios\.(check|duration|perfdata)', 'plugin.\1', 'g')::jsonb,
       message = replace(regexp_replace(message, '\mnagios_check\M', 'plugin', 'g'),
                         'le plugin {{tags.plugin}}', 'le greffon {{tags.plugin}}'),
       updated_at = now()
 WHERE query::text ~ '\mnagios(_check\M|\.(check|duration|perfdata))' OR message ~ '\mnagios_check\M';

UPDATE monitors SET name = 'Greffon en alerte', updated_at = now() WHERE name = 'Plugin Nagios en alerte';

-- États : la clé de groupe suit l'ordre du regroupement, le renommage la garde à sa place.
DELETE FROM monitor_states o
 WHERE o.group_key ~ '\mnagios_check:'
   AND EXISTS (SELECT 1 FROM monitor_states n
                WHERE n.monitor_id = o.monitor_id
                  AND n.group_key = regexp_replace(o.group_key, '\mnagios_check:', 'plugin:', 'g'));
UPDATE monitor_states SET group_key = regexp_replace(group_key, '\mnagios_check:', 'plugin:', 'g')
 WHERE group_key ~ '\mnagios_check:';

UPDATE alert_events
   SET group_key = regexp_replace(group_key, '\mnagios_check:', 'plugin:', 'g'),
       message = regexp_replace(message, '\mnagios_check\M', 'plugin', 'g')
 WHERE group_key ~ '\mnagios_check:' OR message ~ '\mnagios_check\M';

UPDATE events
   SET title = regexp_replace(title, '\mnagios_check\M', 'plugin', 'g'),
       text = regexp_replace(text, '\mnagios_check\M', 'plugin', 'g'),
       aggregation_key = regexp_replace(aggregation_key, '\mnagios_check\M', 'plugin', 'g'),
       tags = pg_temp.g012_tags(tags)
 WHERE title ~ '\mnagios_check\M' OR text ~ '\mnagios_check\M' OR aggregation_key ~ '\mnagios_check\M'
    OR tags ? 'nagios_check';

-- ── agents : état et capacités (les agents à jour les republient sous le nouveau nom) ──
UPDATE agents
   SET status = replace(status::text, '"nagios:', '"plugin:')::jsonb,
       capabilities = replace(capabilities::text, '"nagios:', '"plugin:')::jsonb
 WHERE status::text LIKE '%"nagios:%' OR capabilities::text LIKE '%"nagios:%';

-- ── tableaux installés depuis le catalogue ─────────────────────────────────────
UPDATE dashboards
   SET widgets = replace(replace(replace(
                   regexp_replace(regexp_replace(widgets::text, '\mnagios_check\M', 'plugin', 'g'),
                                  '\mnagios\.(check|duration|perfdata)', 'plugin.\1', 'g'),
                   '"title": "Plugins"', '"title": "Greffons"'),
                   '"title": "Durée des plugins (s)"', '"title": "Durée des greffons (s)"'),
                   '"title":"Plugins"', '"title":"Greffons"')::jsonb,
       updated_at = now()
 WHERE widgets::text ~ '\mnagios(_check\M|\.(check|duration|perfdata))';

UPDATE dashboards
   SET title = CASE title WHEN 'Plugins Nagios et Centreon' THEN 'Greffons'
                          WHEN 'Cibles OpenMetrics et Prometheus' THEN 'Cibles OpenMetrics'
                          WHEN 'node_exporter (Prometheus)' THEN 'node_exporter (système Linux)' END,
       description = replace(description,
                             'État des plugins lancés par les agents (nagios.check), leur durée, et leurs données de performance.',
                             'État des greffons lancés par les agents (plugin.check), leur durée, et leurs données de performance.'),
       updated_at = now()
 WHERE title IN ('Plugins Nagios et Centreon', 'Cibles OpenMetrics et Prometheus', 'node_exporter (Prometheus)');
