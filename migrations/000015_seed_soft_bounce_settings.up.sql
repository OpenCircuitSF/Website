-- Seed #0039's repeated-soft-bounce threshold setting and #0124's
-- per-campaign circuit-breaker settings (PRD §6.9). Both numbers must be
-- admin-configurable at runtime (the issues' acceptance criteria) — so they
-- live here as settings rows, not as Go constants. UpdateSetting only
-- mutates an EXISTING key (internal/auth/store.go), so every row must exist
-- before PATCH /admin/settings can ever change it, exactly like 000008's
-- physical_address seed.
--
-- soft_bounce_threshold_window_days (originally seeded here, "5 soft
-- bounces in 30 days") is RETIRED by #0124: the repeated-soft-bounce rule
-- is now a consecutive streak on subscribers.soft_bounce_streak (migration
-- 000010), reset by a Delivery event, not a rolling window re-queried from
-- email_events. A window has no notion of "the address recovered"; a
-- streak does. See issues/0124.md's Notes and PRD §6.9's superseding note.
-- internal/handlers/soft_bounce.go falls back to these same defaults in
-- code if a row is ever missing or holds a value that fails to parse, so a
-- missing/corrupt row degrades gracefully rather than breaking SES event
-- ingestion or the send worker — but the seed below is what a normal
-- deployment actually reads.
INSERT INTO settings (key, value, updated_at)
VALUES ('soft_bounce_threshold_count', '5', now())
ON CONFLICT DO NOTHING;

-- #0124's circuit breaker (PRD §6.9): the send worker tracks the running
-- bounce/complaint rate of the campaign in flight and pauses the send when
-- either crosses its threshold AND at least send_health_min_sample messages
-- have been sent (below that, rates are too noisy to act on — one bounce
-- out of three sends is 33%, not a trend). Defaults match PRD §6.9's table
-- exactly: AWS enforces a bounce rate under 5% and a complaint rate under
-- 0.1% ACCOUNT-WIDE, and crossing either risks the whole account being
-- re-sandboxed, not just this campaign failing.
INSERT INTO settings (key, value, updated_at)
VALUES ('send_health_min_sample', '50', now())
ON CONFLICT DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('send_health_bounce_pct', '5.0', now())
ON CONFLICT DO NOTHING;

INSERT INTO settings (key, value, updated_at)
VALUES ('send_health_complaint_pct', '0.1', now())
ON CONFLICT DO NOTHING;
