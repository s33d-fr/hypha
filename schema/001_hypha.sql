-- HYPHA : schéma PostgreSQL (>= 15). Idempotent : rejouable sans effet sur une base à jour.
-- Appliqué par `hypha-server migrate`. Toute la télémétrie est partitionnée par temps (UTC) ;
-- la rétention se fait en supprimant des partitions (hypha.maintain_partitions()).
-- Horodatages : timestamptz, toujours en UTC côté application.

CREATE SCHEMA IF NOT EXISTS hypha;
SET search_path TO hypha, public;

CREATE TABLE IF NOT EXISTS schema_version (
    version     int PRIMARY KEY,
    applied_at  timestamptz NOT NULL DEFAULT now()
);

-- ════════════════════════════════════════════════════════════════════════════
-- Comptes, organisations, accès
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS orgs (
    id          bigserial PRIMARY KEY,
    slug        text NOT NULL UNIQUE,
    name        text NOT NULL,
    plan        text NOT NULL DEFAULT 'free',
    settings    jsonb NOT NULL DEFAULT '{}',      -- rétentions propres, fuseau, langue…
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS users (
    id             bigserial PRIMARY KEY,
    sub            text NOT NULL UNIQUE,           -- sujet OIDC, ou 'local:<login>' en auto-hébergé
    email          text,
    name           text,
    password_hash  text,                           -- comptes locaux seulement (auto-hébergé)
    created_at     timestamptz NOT NULL DEFAULT now(),
    last_login_at  timestamptz
);

CREATE TABLE IF NOT EXISTS memberships (
    org_id   bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    user_id  bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role     text NOT NULL DEFAULT 'member' CHECK (role IN ('owner','admin','member','viewer')),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (org_id, user_id)
);

CREATE TABLE IF NOT EXISTS sessions (
    id          text PRIMARY KEY,                  -- 43 caractères aléatoires (cookie hypha_session)
    user_id     bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    org_id      bigint REFERENCES orgs(id) ON DELETE SET NULL,  -- organisation courante
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL,
    ip          text,
    user_agent  text
);
CREATE INDEX IF NOT EXISTS sessions_expires ON sessions (expires_at);

CREATE TABLE IF NOT EXISTS api_keys (
    id            bigserial PRIMARY KEY,
    org_id        bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name          text NOT NULL,
    kind          text NOT NULL DEFAULT 'ingest' CHECK (kind IN ('ingest','app')),
    prefix        text NOT NULL,                   -- 12 premiers caractères, pour l'affichage
    key_hash      bytea NOT NULL UNIQUE,           -- sha256 de la clé entière ; la clé n'est jamais stockée
    created_by    bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_used_at  timestamptz,
    revoked_at    timestamptz
);
CREATE INDEX IF NOT EXISTS api_keys_org ON api_keys (org_id);

-- ════════════════════════════════════════════════════════════════════════════
-- Inventaire : hôtes et agents
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS hosts (
    id                bigserial PRIMARY KEY,
    org_id            bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    agent_id          uuid NOT NULL,               -- identifiant persistant posé à l'installation de l'agent
    hostname          text NOT NULL,
    os                text,                        -- linux | windows | darwin
    platform          text,                        -- ubuntu, debian, rhel, windows server…
    platform_version  text,
    kernel            text,
    arch              text,                        -- amd64 | arm64
    cpu_cores         int,
    cpu_model         text,
    mem_total         bigint,                      -- octets
    ip_addresses      jsonb NOT NULL DEFAULT '[]',
    tags              jsonb NOT NULL DEFAULT '{}', -- étiquettes déclarées dans la conf de l'agent
    agent_version     text,
    boot_time         timestamptz,
    meta              jsonb NOT NULL DEFAULT '{}', -- tout le reste (virtualisation, fabricant…)
    first_seen        timestamptz NOT NULL DEFAULT now(),
    last_seen         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (org_id, agent_id)
);
CREATE INDEX IF NOT EXISTS hosts_org_name ON hosts (org_id, hostname);

CREATE TABLE IF NOT EXISTS agents (
    id          uuid PRIMARY KEY,                  -- = hosts.agent_id
    org_id      bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    host_id     bigint REFERENCES hosts(id) ON DELETE CASCADE,
    version     text,
    os          text,
    arch        text,
    last_ip     text,
    status      jsonb NOT NULL DEFAULT '{}',       -- état des collecteurs, erreurs remontées par l'agent
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now()
);

-- ════════════════════════════════════════════════════════════════════════════
-- Métriques
-- ════════════════════════════════════════════════════════════════════════════

-- Une série = un nom + un jeu d'étiquettes (l'hôte est une étiquette : "host").
CREATE TABLE IF NOT EXISTS metric_series (
    id          bigserial PRIMARY KEY,
    org_id      bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        text NOT NULL,
    tags        jsonb NOT NULL DEFAULT '{}',      -- {"host":"web-01","device":"sda"} ; valeurs texte
    tags_hash   bytea NOT NULL,                   -- sha256 du JSON canonique (clés triées, sans espaces)
    host_id     bigint REFERENCES hosts(id) ON DELETE SET NULL,
    type        text NOT NULL DEFAULT 'gauge' CHECK (type IN ('gauge','count','rate','distribution')),
    unit        text,
    first_seen  timestamptz NOT NULL DEFAULT now(),
    last_seen   timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS metric_series_key ON metric_series (org_id, name, tags_hash);
CREATE INDEX IF NOT EXISTS metric_series_name ON metric_series (org_id, name);
CREATE INDEX IF NOT EXISTS metric_series_host ON metric_series (host_id);
CREATE INDEX IF NOT EXISTS metric_series_tags ON metric_series USING gin (tags jsonb_path_ops);

CREATE TABLE IF NOT EXISTS metric_points (
    series_id  bigint NOT NULL,
    ts         timestamptz NOT NULL,
    value      double precision NOT NULL
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS metric_points_series_ts ON metric_points (series_id, ts);

CREATE TABLE IF NOT EXISTS metric_rollup_5m (
    series_id  bigint NOT NULL,
    ts         timestamptz NOT NULL,               -- début du seau de 5 minutes
    cnt        int NOT NULL,
    sum        double precision NOT NULL,
    min        double precision NOT NULL,
    max        double precision NOT NULL,
    last       double precision NOT NULL
) PARTITION BY RANGE (ts);
CREATE UNIQUE INDEX IF NOT EXISTS metric_rollup_5m_key ON metric_rollup_5m (series_id, ts);

CREATE TABLE IF NOT EXISTS metric_rollup_1h (
    series_id  bigint NOT NULL,
    ts         timestamptz NOT NULL,               -- début de l'heure
    cnt        int NOT NULL,
    sum        double precision NOT NULL,
    min        double precision NOT NULL,
    max        double precision NOT NULL,
    last       double precision NOT NULL
) PARTITION BY RANGE (ts);
CREATE UNIQUE INDEX IF NOT EXISTS metric_rollup_1h_key ON metric_rollup_1h (series_id, ts);

-- ════════════════════════════════════════════════════════════════════════════
-- Journaux
-- ════════════════════════════════════════════════════════════════════════════

CREATE SEQUENCE IF NOT EXISTS logs_id_seq;
CREATE TABLE IF NOT EXISTS logs (
    org_id      bigint NOT NULL,
    ts          timestamptz NOT NULL,
    id          bigint NOT NULL DEFAULT nextval('hypha.logs_id_seq'),
    host_id     bigint,
    hostname    text,
    service     text,
    source      text,                              -- journald, file, eventlog, otlp, nginx…
    status      text NOT NULL DEFAULT 'info',      -- emergency alert critical error warning notice info debug
    message     text NOT NULL,
    attributes  jsonb NOT NULL DEFAULT '{}',       -- champs structurés (@http.status_code…)
    tags        jsonb NOT NULL DEFAULT '{}',
    trace_id    text,
    span_id     text,
    fts         tsvector GENERATED ALWAYS AS (to_tsvector('simple', left(message, 8192))) STORED
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS logs_org_ts ON logs (org_id, ts DESC, id DESC);
CREATE INDEX IF NOT EXISTS logs_org_service_ts ON logs (org_id, service, ts DESC);
CREATE INDEX IF NOT EXISTS logs_org_host_ts ON logs (org_id, host_id, ts DESC);
CREATE INDEX IF NOT EXISTS logs_org_status_ts ON logs (org_id, status, ts DESC);
CREATE INDEX IF NOT EXISTS logs_fts ON logs USING gin (fts);
CREATE INDEX IF NOT EXISTS logs_attrs ON logs USING gin (attributes jsonb_path_ops);
CREATE INDEX IF NOT EXISTS logs_trace ON logs (trace_id) WHERE trace_id IS NOT NULL;

-- ════════════════════════════════════════════════════════════════════════════
-- Traces (APM, OpenTelemetry)
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS spans (
    org_id               bigint NOT NULL,
    ts                   timestamptz NOT NULL,     -- début du span
    trace_id             text NOT NULL,            -- 32 hex
    span_id              text NOT NULL,            -- 16 hex
    parent_span_id       text,                     -- NULL = span racine
    service              text NOT NULL,
    name                 text NOT NULL,            -- opération
    resource             text,                     -- route, requête, file…
    kind                 text,                     -- server client internal producer consumer
    duration_ns          bigint NOT NULL,
    status               text NOT NULL DEFAULT 'unset' CHECK (status IN ('ok','error','unset')),
    status_message       text,
    host_id              bigint,
    hostname             text,
    attributes           jsonb NOT NULL DEFAULT '{}',
    resource_attributes  jsonb NOT NULL DEFAULT '{}',
    events               jsonb NOT NULL DEFAULT '[]'
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS spans_org_trace ON spans (org_id, trace_id);
CREATE INDEX IF NOT EXISTS spans_org_service_ts ON spans (org_id, service, ts DESC);
CREATE INDEX IF NOT EXISTS spans_org_roots ON spans (org_id, ts DESC) WHERE parent_span_id IS NULL;

-- ════════════════════════════════════════════════════════════════════════════
-- Processus, connexions réseau, conteneurs (instantanés de l'agent)
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS process_snapshots (
    org_id        bigint NOT NULL,
    host_id       bigint NOT NULL,
    ts            timestamptz NOT NULL,
    pid           int NOT NULL,
    ppid          int,
    name          text NOT NULL,
    username      text,
    cmdline       text,
    state         text,
    cpu_pct       real,                            -- % d'UN cœur (peut dépasser 100)
    mem_rss       bigint,                          -- octets
    mem_pct       real,
    threads       int,
    started_at    timestamptz,
    io_read_bps   bigint,
    io_write_bps  bigint
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS process_snapshots_host_ts ON process_snapshots (org_id, host_id, ts DESC);

CREATE TABLE IF NOT EXISTS net_connections (
    org_id     bigint NOT NULL,
    host_id    bigint NOT NULL,
    ts         timestamptz NOT NULL,
    proto      text NOT NULL,                      -- tcp | udp | tcp6 | udp6
    direction  text,                               -- incoming | outgoing | listening
    laddr      text,
    lport      int,
    raddr      text,
    rport      int,
    state      text,
    pid        int,
    process    text,
    count      int NOT NULL DEFAULT 1              -- connexions identiques regroupées
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS net_connections_host_ts ON net_connections (org_id, host_id, ts DESC);

CREATE TABLE IF NOT EXISTS container_snapshots (
    org_id        bigint NOT NULL,
    host_id       bigint NOT NULL,
    ts            timestamptz NOT NULL,
    container_id  text NOT NULL,
    name          text,
    image         text,
    state         text,
    cpu_pct       real,
    mem_usage     bigint,
    mem_limit     bigint,
    net_rx_bps    bigint,
    net_tx_bps    bigint,
    labels        jsonb NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS container_snapshots_host_ts ON container_snapshots (org_id, host_id, ts DESC);

-- ════════════════════════════════════════════════════════════════════════════
-- Contrôles de service et événements
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS check_runs (
    org_id      bigint NOT NULL,
    host_id     bigint,
    ts          timestamptz NOT NULL,
    check_name  text NOT NULL,                     -- systemd.unit, windows.service, http, tcp, hypha.agent.up…
    status      smallint NOT NULL CHECK (status BETWEEN 0 AND 3),  -- 0 OK 1 WARN 2 CRIT 3 UNKNOWN
    message     text,
    tags        jsonb NOT NULL DEFAULT '{}'
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS check_runs_org_ts ON check_runs (org_id, check_name, ts DESC);
CREATE INDEX IF NOT EXISTS check_runs_host_ts ON check_runs (org_id, host_id, ts DESC);

CREATE SEQUENCE IF NOT EXISTS events_id_seq;
CREATE TABLE IF NOT EXISTS events (
    org_id           bigint NOT NULL,
    ts               timestamptz NOT NULL,
    id               bigint NOT NULL DEFAULT nextval('hypha.events_id_seq'),
    host_id          bigint,
    source           text,                         -- agent, monitor, user, api, deploy…
    event_type       text,
    title            text NOT NULL,
    text             text,
    alert_type       text NOT NULL DEFAULT 'info' CHECK (alert_type IN ('info','success','warning','error')),
    priority         text NOT NULL DEFAULT 'normal' CHECK (priority IN ('normal','low')),
    tags             jsonb NOT NULL DEFAULT '{}',
    aggregation_key  text
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS events_org_ts ON events (org_id, ts DESC);

-- ════════════════════════════════════════════════════════════════════════════
-- Moniteurs, alertes, notifications
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS notification_channels (
    id          bigserial PRIMARY KEY,
    org_id      bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('email','webhook','slack','teams')),
    name        text NOT NULL,
    config      jsonb NOT NULL DEFAULT '{}',       -- {"to":[…]} | {"url":…,"headers":{…}}
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS monitors (
    id              bigserial PRIMARY KEY,
    org_id          bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name            text NOT NULL,
    type            text NOT NULL CHECK (type IN ('metric','service_check','log','host','synthetic','apm')),
    query           jsonb NOT NULL,                -- définition propre au type (cf. CONTRAT § Moniteurs)
    thresholds      jsonb NOT NULL DEFAULT '{}',   -- {"critical":90,"warning":80,"critical_recovery":85}
    message         text NOT NULL DEFAULT '',      -- gabarit : {{name}} {{group}} {{value}} {{state}}
    channel_ids     bigint[] NOT NULL DEFAULT '{}',
    notify_no_data  boolean NOT NULL DEFAULT false,
    no_data_s       int NOT NULL DEFAULT 600,
    renotify_s      int NOT NULL DEFAULT 0,        -- 0 = pas de relance
    tags            jsonb NOT NULL DEFAULT '{}',
    enabled         boolean NOT NULL DEFAULT true,
    muted_until     timestamptz,
    created_by      bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS monitors_org ON monitors (org_id);

CREATE TABLE IF NOT EXISTS monitor_states (
    monitor_id     bigint NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    group_key      text NOT NULL,                  -- '*' ou 'host:web-01,device:sda'
    state          text NOT NULL CHECK (state IN ('OK','WARN','ALERT','NODATA')),
    value          double precision,
    since          timestamptz NOT NULL DEFAULT now(),
    last_eval      timestamptz NOT NULL DEFAULT now(),
    last_notified  timestamptz,
    PRIMARY KEY (monitor_id, group_key)
);

CREATE TABLE IF NOT EXISTS alert_events (
    id          bigserial PRIMARY KEY,
    org_id      bigint NOT NULL,
    monitor_id  bigint NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    group_key   text NOT NULL,
    ts          timestamptz NOT NULL DEFAULT now(),
    from_state  text,
    to_state    text NOT NULL,
    value       double precision,
    message     text
);
CREATE INDEX IF NOT EXISTS alert_events_org_ts ON alert_events (org_id, ts DESC);
CREATE INDEX IF NOT EXISTS alert_events_monitor_ts ON alert_events (monitor_id, ts DESC);

-- ════════════════════════════════════════════════════════════════════════════
-- Tableaux de bord
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS dashboards (
    id             bigserial PRIMARY KEY,
    org_id         bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    title          text NOT NULL,
    description    text NOT NULL DEFAULT '',
    widgets        jsonb NOT NULL DEFAULT '[]',    -- cf. CONTRAT § Tableaux de bord
    template_vars  jsonb NOT NULL DEFAULT '[]',
    created_by     bigint REFERENCES users(id) ON DELETE SET NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS dashboards_org ON dashboards (org_id);

-- ════════════════════════════════════════════════════════════════════════════
-- Tests synthétiques
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS synthetic_tests (
    id          bigserial PRIMARY KEY,
    org_id      bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name        text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('http','tcp','ssl','dns','icmp')),
    target      text NOT NULL,                     -- URL, hôte:port, nom DNS
    config      jsonb NOT NULL DEFAULT '{}',       -- méthode, en-têtes, statut attendu, texte attendu, délai…
    interval_s  int NOT NULL DEFAULT 60 CHECK (interval_s >= 30),
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    last_run    timestamptz
);

CREATE TABLE IF NOT EXISTS synthetic_results (
    org_id       bigint NOT NULL,
    test_id      bigint NOT NULL,
    ts           timestamptz NOT NULL,
    location     text NOT NULL DEFAULT 'fr-s33d',
    ok           boolean NOT NULL,
    status_code  int,
    latency_ms   real,
    error        text,
    details      jsonb NOT NULL DEFAULT '{}'       -- dns_ms, connect_ms, tls_ms, ttfb_ms, jours avant fin du certificat…
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS synthetic_results_test_ts ON synthetic_results (test_id, ts DESC);

-- ════════════════════════════════════════════════════════════════════════════
-- RUM (supervision côté navigateur)
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS rum_apps (
    id            bigserial PRIMARY KEY,
    org_id        bigint NOT NULL REFERENCES orgs(id) ON DELETE CASCADE,
    name          text NOT NULL,
    client_token  text NOT NULL UNIQUE,            -- public, embarqué dans la page du client
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS rum_events (
    org_id       bigint NOT NULL,
    app_id       bigint NOT NULL,
    ts           timestamptz NOT NULL,
    session_id   text NOT NULL,
    view_url     text,
    event_type   text NOT NULL CHECK (event_type IN ('view','action','error','resource','long_task','vital')),
    name         text,
    duration_ms  real,
    attributes   jsonb NOT NULL DEFAULT '{}',
    user_agent   text,
    country      text
) PARTITION BY RANGE (ts);
CREATE INDEX IF NOT EXISTS rum_events_app_ts ON rum_events (org_id, app_id, ts DESC);

-- ════════════════════════════════════════════════════════════════════════════
-- Partitions : création d'avance et rétention
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS partition_policy (
    parent     text PRIMARY KEY,                   -- table partitionnée du schéma hypha
    step       text NOT NULL CHECK (step IN ('day','month')),
    retention  interval NOT NULL,
    premake    int NOT NULL DEFAULT 3              -- pas créés d'avance
);

INSERT INTO partition_policy (parent, step, retention, premake) VALUES
    ('metric_points',       'day',   '15 days',   3),
    ('metric_rollup_5m',    'day',   '92 days',   3),
    ('metric_rollup_1h',    'month', '15 months', 2),
    ('logs',                'day',   '15 days',   3),
    ('spans',               'day',   '15 days',   3),
    ('process_snapshots',   'day',   '3 days',    3),
    ('net_connections',     'day',   '3 days',    3),
    ('container_snapshots', 'day',   '3 days',    3),
    ('check_runs',          'day',   '30 days',   3),
    ('events',              'month', '13 months', 2),
    ('synthetic_results',   'day',   '92 days',   3),
    ('rum_events',          'day',   '30 days',   3)
ON CONFLICT (parent) DO NOTHING;

CREATE OR REPLACE FUNCTION hypha.maintain_partitions() RETURNS text
LANGUAGE plpgsql AS $$
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

-- ════════════════════════════════════════════════════════════════════════════
-- Agrégats de métriques : 5 minutes depuis le brut, 1 heure depuis les 5 minutes.
-- Chaque passage recalcule aussi les 15 dernières minutes (données en retard d'un agent
-- qui vidait son tampon) ; ON CONFLICT rend le recalcul idempotent.
-- ════════════════════════════════════════════════════════════════════════════

CREATE TABLE IF NOT EXISTS rollup_state (
    name        text PRIMARY KEY,
    done_until  timestamptz NOT NULL
);

CREATE OR REPLACE FUNCTION hypha.rollup_5m(p_until timestamptz DEFAULT now() - interval '1 minute') RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
    origin  constant timestamptz := timestamptz '2000-01-01 00:00:00+00';
    v_done  timestamptz;
    v_from  timestamptz;
    v_to    timestamptz;
    n       bigint;
BEGIN
    SELECT done_until INTO v_done FROM hypha.rollup_state WHERE name = '5m' FOR UPDATE;
    IF v_done IS NULL THEN
        v_done := date_bin('5 minutes', now() - interval '1 day', origin);
        INSERT INTO hypha.rollup_state VALUES ('5m', v_done) ON CONFLICT (name) DO NOTHING;
    END IF;
    v_to   := date_bin('5 minutes', p_until, origin);
    v_from := least(v_done, v_to - interval '15 minutes');
    IF v_to <= v_from THEN RETURN 0; END IF;

    INSERT INTO hypha.metric_rollup_5m (series_id, ts, cnt, sum, min, max, last)
    SELECT series_id, date_bin('5 minutes', ts, origin), count(*), sum(value), min(value), max(value),
           (array_agg(value ORDER BY ts DESC))[1]
      FROM hypha.metric_points
     WHERE ts >= v_from AND ts < v_to
     GROUP BY 1, 2
    ON CONFLICT (series_id, ts) DO UPDATE
       SET cnt = EXCLUDED.cnt, sum = EXCLUDED.sum, min = EXCLUDED.min, max = EXCLUDED.max, last = EXCLUDED.last;
    GET DIAGNOSTICS n = ROW_COUNT;

    UPDATE hypha.rollup_state SET done_until = greatest(done_until, v_to) WHERE name = '5m';
    RETURN n;
END $$;

CREATE OR REPLACE FUNCTION hypha.rollup_1h(p_until timestamptz DEFAULT now() - interval '6 minutes') RETURNS bigint
LANGUAGE plpgsql AS $$
DECLARE
    origin  constant timestamptz := timestamptz '2000-01-01 00:00:00+00';
    v_done  timestamptz;
    v_from  timestamptz;
    v_to    timestamptz;
    n       bigint;
BEGIN
    SELECT done_until INTO v_done FROM hypha.rollup_state WHERE name = '1h' FOR UPDATE;
    IF v_done IS NULL THEN
        v_done := date_bin('1 hour', now() - interval '2 days', origin);
        INSERT INTO hypha.rollup_state VALUES ('1h', v_done) ON CONFLICT (name) DO NOTHING;
    END IF;
    v_to   := date_bin('1 hour', p_until, origin);
    v_from := least(v_done, v_to - interval '1 hour');
    IF v_to <= v_from THEN RETURN 0; END IF;

    INSERT INTO hypha.metric_rollup_1h (series_id, ts, cnt, sum, min, max, last)
    SELECT series_id, date_bin('1 hour', ts, origin), sum(cnt), sum(sum), min(min), max(max),
           (array_agg(last ORDER BY ts DESC))[1]
      FROM hypha.metric_rollup_5m
     WHERE ts >= v_from AND ts < v_to
     GROUP BY 1, 2
    ON CONFLICT (series_id, ts) DO UPDATE
       SET cnt = EXCLUDED.cnt, sum = EXCLUDED.sum, min = EXCLUDED.min, max = EXCLUDED.max, last = EXCLUDED.last;
    GET DIAGNOSTICS n = ROW_COUNT;

    UPDATE hypha.rollup_state SET done_until = greatest(done_until, v_to) WHERE name = '1h';
    RETURN n;
END $$;

-- Le compte applicatif n'a aucun droit DDL : la maintenance (partitions, agrégats) s'exécute avec
-- les droits du propriétaire du schéma, et seulement par ces trois fonctions.
-- UTC imposé : l'arithmétique « + 1 mois » d'un timestamptz suit le fuseau de la session, et une
-- session en Europe/Paris décalerait d'une heure les bornes qui traversent un changement d'heure.
ALTER FUNCTION hypha.maintain_partitions() SECURITY DEFINER SET search_path = hypha, pg_temp SET timezone = 'UTC';
ALTER FUNCTION hypha.rollup_5m(timestamptz) SECURITY DEFINER SET search_path = hypha, pg_temp;
ALTER FUNCTION hypha.rollup_1h(timestamptz) SECURITY DEFINER SET search_path = hypha, pg_temp;

INSERT INTO schema_version (version) VALUES (1) ON CONFLICT (version) DO NOTHING;
SELECT hypha.maintain_partitions();
