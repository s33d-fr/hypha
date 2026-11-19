-- 007 : HYPHA 0.4.3 (contrat § 32). Idempotent. Exécuté par hypha_owner.
-- Synthétiques de messagerie : smtp, imap, aller-retour d'un message, contrôle DNS d'un domaine.
SET search_path TO hypha, public;

ALTER TABLE hypha.synthetic_tests DROP CONSTRAINT IF EXISTS synthetic_tests_kind_check;
ALTER TABLE hypha.synthetic_tests ADD CONSTRAINT synthetic_tests_kind_check CHECK (kind IN
  ('http','tcp','ssl','dns','icmp','multistep','smtp','imap','mail_roundtrip','mail_dns'));

INSERT INTO hypha.schema_version (version) VALUES (7) ON CONFLICT (version) DO NOTHING;
