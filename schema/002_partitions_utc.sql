-- 002 : calcul des partitions en UTC, et réparation des partitions créées hors UTC.
-- Une session en Europe/Paris décale d'une heure les bornes qui traversent un changement d'heure
-- (« + 1 mois » suit le fuseau de la session) ; la partition obtenue porte alors le nom du mois
-- d'avant et chevauche celle que calcule une session UTC. Idempotent.
SET search_path TO hypha, public;

ALTER FUNCTION hypha.maintain_partitions() SET timezone = 'UTC';

-- Supprime les partitions VIDES dont la borne basse n'est pas un début de jour (ou de mois) UTC.
-- Une partition non vide est laissée et signalée : jamais de donnée effacée par une réparation.
DO $$
DECLARE
    r       record;
    debut   timestamptz;
    attendu timestamptz;
    n       bigint;
BEGIN
    SET LOCAL timezone = 'UTC';
    FOR r IN
        SELECT c.relname, pc.relname AS parent, p.step, pg_get_expr(c.relpartbound, c.oid) AS bound
          FROM pg_inherits inh
          JOIN pg_class c      ON c.oid = inh.inhrelid
          JOIN pg_class pc     ON pc.oid = inh.inhparent
          JOIN pg_namespace ns ON ns.oid = pc.relnamespace
          JOIN hypha.partition_policy p ON p.parent = pc.relname
         WHERE ns.nspname = 'hypha' AND c.relkind = 'r'
    LOOP
        debut := substring(r.bound from 'FROM \(''([^'']+)''\)')::timestamptz;
        attendu := date_trunc(r.step, debut);
        IF debut <> attendu THEN
            EXECUTE format('SELECT count(*) FROM hypha.%I', r.relname) INTO n;
            IF n = 0 THEN
                EXECUTE format('DROP TABLE hypha.%I', r.relname);
                RAISE NOTICE 'partition % supprimee (borne % hors UTC, vide)', r.relname, debut;
            ELSE
                RAISE WARNING 'partition % : borne % hors UTC mais % lignes, laissee en place', r.relname, debut, n;
            END IF;
        END IF;
    END LOOP;
END $$;

SELECT hypha.maintain_partitions();
INSERT INTO schema_version (version) VALUES (2) ON CONFLICT (version) DO NOTHING;
