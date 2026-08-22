-- Campaign send-state columns and settings the send worker and its pre-send
-- gate need (#0045, PRD §6.6). migrations/000017 is already applied and must
-- not be edited (CLAUDE.md §1 — migrations are append-only).

-- materialized_at: the "materialize exactly once" marker. Without it, a
-- worker resuming a 'sending' campaign cannot distinguish "audience
-- complete" from "crashed halfway through materializing", and re-running
-- Materialize unconditionally would pick up subscribers who confirmed AFTER
-- the send started, which #0044's plan (§1) explicitly forbids.
ALTER TABLE email_campaigns ADD COLUMN materialized_at TIMESTAMPTZ;

-- test_sent_at: the no_test_send preflight gate's storage. This migration
-- adds and #0045's Preflight READS the column; #0046's test-send endpoint
-- WRITES it (now() on a successful test send). It lands here because this
-- issue carries the acceptance criterion that enforces it — see #0045's
-- plan §3.
ALTER TABLE email_campaigns ADD COLUMN test_sent_at TIMESTAMPTZ;

-- Widen email_sends' status CHECK to add the seventh value 'sending' — the
-- per-row claim state between 'queued' and 'sent'/'failed' the worker's
-- atomic per-row claim (#0045's plan §4, updated by #0122 to also stamp
-- claimed_at) requires:
--   UPDATE email_sends SET status='sending', attempts=attempts+1, claimed_at=now()
--    WHERE id=$1 AND status='queued'
-- 000017's CHECK predates this state and must be dropped and re-added
-- rather than edited in place (CHECK constraints have no ALTER-in-place
-- form in Postgres).
ALTER TABLE email_sends DROP CONSTRAINT email_sends_status_check;
ALTER TABLE email_sends
    ADD CONSTRAINT email_sends_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'bounced', 'complained', 'skipped'));

-- claimed_at: stamped by SendStore.ClaimRow in the same statement that
-- flips a row to 'sending' and increments attempts (#0122). Without it,
-- OrphanSweep cannot tell a crashed worker's abandoned 'sending' row from a
-- live worker's in-flight one, and resets both — the row worker.go's
-- ClaimRow just claimed a moment ago gets un-claimed by another worker's
-- OrphanSweep pass and mailed twice. OrphanSweep only resets a row whose
-- claimed_at is older than worker.go's orphanStaleAfter (derived from
-- sendMessageTimeout + writeStatusTimeout — the two hard bounds a live
-- claim can legitimately take, not a constant sized against measured
-- machine load, CLAUDE.md §5). NULL claimed_at (a 'sending' row that
-- reached that status by some path other than ClaimRow, e.g. a test fixture
-- or a pre-#0122 row) is always treated as orphaned.
ALTER TABLE email_sends ADD COLUMN claimed_at TIMESTAMPTZ;

-- Seed the two settings rows this issue's gate and rate limiter read,
-- matching 000008's ON CONFLICT DO NOTHING idiom so a re-run (or a deploy
-- that already has the key) is a no-op rather than an error.
--
-- max_send_rate: PRD §6.6's default (10/s); #0045's plan §7 — the
-- operator's dial, capped by MAX_SEND_RATE's env-level ceiling and never
-- allowed to fall back to unbounded.
INSERT INTO settings (key, value, updated_at)
VALUES ('max_send_rate', '10', now())
ON CONFLICT (key) DO NOTHING;

-- default_from_name: PRD §9's runtime-editable sender display name,
-- consumed by #0045's plan §5.2 (Message.From). Empty by default — the
-- worker falls back to EMAIL_FROM's bare address until an operator sets
-- this.
INSERT INTO settings (key, value, updated_at)
VALUES ('default_from_name', '', now())
ON CONFLICT (key) DO NOTHING;
