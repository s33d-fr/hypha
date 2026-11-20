-- 010 : notifications push (application installable, Web Push). Idempotent. Exécuté par hypha_owner.
-- Petite table neuve et contrainte de canal élargie : rien ne touche la télémétrie ni les grosses tables.
SET search_path TO hypha, public;
SET LOCAL lock_timeout = '10s';

-- ── Canal « push » : vers les appareils des destinataires ({"users":[ids]} ou {"all":true}) ─────
ALTER TABLE hypha.notification_channels DROP CONSTRAINT IF EXISTS notification_channels_kind_check;
ALTER TABLE hypha.notification_channels ADD CONSTRAINT notification_channels_kind_check CHECK (kind IN
  ('email','webhook','slack','teams','telegram','discord','googlechat','mattermost','ntfy','pagerduty','opsgenie',
   'push'));

-- ── Appareils abonnés (un par navigateur ou application installée), propres à une personne ────────
-- endpoint : adresse du service de push du navigateur (vaut un jeton : jamais journalisée en entier) ;
-- p256dh et auth : clés de chiffrement du message (RFC 8291) ; lang : langue des textes envoyés.
CREATE TABLE IF NOT EXISTS hypha.push_subscriptions (
    id               bigserial PRIMARY KEY,
    user_id          bigint NOT NULL REFERENCES hypha.users(id) ON DELETE CASCADE,
    endpoint         text NOT NULL UNIQUE,
    p256dh           text NOT NULL,
    auth             text NOT NULL,
    label            text NOT NULL DEFAULT '',
    lang             text NOT NULL DEFAULT 'fr' CHECK (lang IN ('fr', 'en')),
    user_agent       text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_success_at  timestamptz,
    last_error_at    timestamptz,
    last_error       text,
    failures         int NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS push_subscriptions_user ON hypha.push_subscriptions (user_id);

INSERT INTO hypha.schema_version (version) VALUES (10) ON CONFLICT (version) DO NOTHING;
