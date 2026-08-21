-- Seed the #0039 repeated-soft-bounce threshold settings. PRD §6.5's state
-- machine fixes the default at "5 soft bounces in 30 days", but both numbers
-- must be admin-configurable at runtime (the issue's acceptance criteria) —
-- so they live here as settings rows, not as Go constants. UpdateSetting only
-- mutates an EXISTING key (internal/auth/store.go), so both rows must exist
-- before PATCH /admin/settings can ever change them, exactly like 000008's
-- physical_address seed.
--
-- internal/handlers/soft_bounce.go falls back to these same defaults in code
-- if a row is ever missing or holds a value that fails to parse as a
-- positive integer, so a missing/corrupt row degrades gracefully rather than
-- breaking SES event ingestion — but the seed below is what a normal
-- deployment actually reads.
INSERT INTO settings (key, value, updated_at)
VALUES ('soft_bounce_threshold_count', '5', now())
ON CONFLICT DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('soft_bounce_threshold_window_days', '30', now())
ON CONFLICT DO NOTHING;
