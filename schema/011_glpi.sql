-- 011 : canal de notification GLPI (vague 6 des intégrations). Idempotent. Exécuté par hypha_owner.
-- Contrainte de type de canal élargie seulement : aucune table touchée.
SET search_path TO hypha, public;
SET LOCAL lock_timeout = '10s';

-- ── Canal « glpi » : ticket ouvert à l'alerte, suivi et solution à la reprise (API REST de GLPI) ─────
ALTER TABLE hypha.notification_channels DROP CONSTRAINT IF EXISTS notification_channels_kind_check;
ALTER TABLE hypha.notification_channels ADD CONSTRAINT notification_channels_kind_check CHECK (kind IN
  ('email','webhook','slack','teams','telegram','discord','googlechat','mattermost','ntfy','pagerduty','opsgenie',
   'push','glpi'));

INSERT INTO hypha.schema_version (version) VALUES (11) ON CONFLICT (version) DO NOTHING;
