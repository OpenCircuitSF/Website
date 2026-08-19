-- Seed the physical_address setting row. #0009's admin Settings tab edits
-- this as a runtime setting (not an env var, per #0007's Notes) so #0045's
-- send worker can read it fresh from the settings table at send time and
-- refuse to start a campaign while it is empty (CAN-SPAM 15 U.S.C. §7704
-- requires a physical postal address in every commercial message).
--
-- Seeded empty by default: the send worker's own emptiness check (#0045) is
-- the enforcement point, not this migration. UpdateSetting only mutates an
-- EXISTING key (see internal/auth/store.go), so the row must exist here
-- before PATCH /admin/settings can ever set it.
INSERT INTO settings (key, value, updated_at)
VALUES ('physical_address', '', now())
ON CONFLICT DO NOTHING;
