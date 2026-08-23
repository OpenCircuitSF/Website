-- Reverse dependency order: drop the FK this migration attached to
-- email_campaigns before dropping the table it points at, then
-- workshop_interests (carries FKs into workshops), then workshops itself.
ALTER TABLE email_campaigns DROP CONSTRAINT email_campaigns_workshop_id_fkey;
DROP TABLE workshop_interests;
DROP TABLE workshops;
