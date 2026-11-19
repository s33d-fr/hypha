-- 004 : recherche des journaux par sous-chaîne, servie par un index trigramme (contrat § 11).
-- Exécuté par hypha_owner (propriétaire de la base). Idempotent.
--
-- La recherche traduit un mot libre en `lower(message) LIKE '%v%' ESCAPE '\'` (query/logs_ql.py) :
-- le plein texte `fts` ne sert plus, sa colonne générée et son index GIN coûtaient à chaque écriture.
-- L'index trigramme est posé partition par partition, et SEULEMENT sur les partitions vides :
-- l'indexation d'une partition remplie bloquerait l'ingestion le temps de la construire. Les
-- partitions déjà remplies (aujourd'hui, hier…) restent sans index jusqu'à leur purge ; chaque
-- nouvelle partition le reçoit à sa création. Aucun index sur le parent : le planificateur utilise
-- l'index de chaque partition retenue après élagage.
SET search_path TO hypha, public;

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;

ALTER TABLE hypha.logs DROP COLUMN IF EXISTS fts;

DO $$
DECLARE
    r      record;
    vide   boolean;
BEGIN
    FOR r IN
        SELECT c.relname
          FROM pg_inherits inh
          JOIN pg_class c      ON c.oid = inh.inhrelid
          JOIN pg_class pc     ON pc.oid = inh.inhparent
          JOIN pg_namespace ns ON ns.oid = pc.relnamespace
         WHERE ns.nspname = 'hypha' AND pc.relname = 'logs' AND c.relkind = 'r'
    LOOP
        EXECUTE format('SELECT NOT EXISTS (SELECT 1 FROM hypha.%I LIMIT 1)', r.relname) INTO vide;
        IF vide THEN
            EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON hypha.%I USING gin (lower(message) public.gin_trgm_ops)',
                           r.relname || '_msg_trgm', r.relname);
        ELSE
            RAISE NOTICE 'partition % remplie : laissée sans index trigramme', r.relname;
        END IF;
    END LOOP;
END $$;

-- Même fonction que 001 (avec le fuseau UTC de 002), plus l'index trigramme posé sur chaque
-- nouvelle partition de `logs` juste après sa création, quand elle est encore vide.
-- `public` dans le search_path : l'opclasse gin_trgm_ops vit dans le schéma de l'extension.
CREATE OR REPLACE FUNCTION hypha.maintain_partitions() RETURNS text
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = hypha, public, pg_temp
SET timezone = 'UTC'
AS $$
DECLARE
    p         record;
    r         record;
    i         int;
    start_ts  timestamptz;
    end_ts    timestamptz;
    pname     text;
    created   int := 0;
    dropped   int := 0;
BEGIN
    FOR p IN SELECT * FROM hypha.partition_policy LOOP
        FOR i IN -1..p.premake LOOP
            IF p.step = 'day' THEN
                start_ts := (date_trunc('day', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') + make_interval(days => i);
                end_ts   := start_ts + interval '1 day';
                pname    := p.parent || '_p' || to_char(start_ts AT TIME ZONE 'UTC', 'YYYYMMDD');
            ELSE
                start_ts := (date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC') + make_interval(months => i);
                end_ts   := start_ts + interval '1 month';
                pname    := p.parent || '_p' || to_char(start_ts AT TIME ZONE 'UTC', 'YYYYMM');
            END IF;
            IF to_regclass('hypha.' || pname) IS NULL THEN
                EXECUTE format('CREATE TABLE hypha.%I PARTITION OF hypha.%I FOR VALUES FROM (%L) TO (%L)',
                               pname, p.parent, start_ts, end_ts);
                IF p.parent = 'logs' THEN
                    EXECUTE format('CREATE INDEX IF NOT EXISTS %I ON hypha.%I USING gin (lower(message) public.gin_trgm_ops)',
                                   pname || '_msg_trgm', pname);
                END IF;
                created := created + 1;
            END IF;
        END LOOP;

        FOR r IN
            SELECT c.relname, pg_get_expr(c.relpartbound, c.oid) AS bound
              FROM pg_inherits inh
              JOIN pg_class c      ON c.oid = inh.inhrelid
              JOIN pg_class pc     ON pc.oid = inh.inhparent
              JOIN pg_namespace n  ON n.oid = pc.relnamespace
             WHERE n.nspname = 'hypha' AND pc.relname = p.parent
        LOOP
            end_ts := substring(r.bound from 'TO \(''([^'']+)''\)')::timestamptz;
            IF end_ts IS NOT NULL AND end_ts < now() - p.retention THEN
                EXECUTE format('DROP TABLE hypha.%I', r.relname);
                dropped := dropped + 1;
            END IF;
        END LOOP;
    END LOOP;
    RETURN format('partitions créées %s, supprimées %s', created, dropped);
END $$;

SELECT hypha.maintain_partitions();
INSERT INTO schema_version (version) VALUES (4) ON CONFLICT (version) DO NOTHING;
