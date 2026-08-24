-- email_campaigns, campaign_interests, email_sends: the campaign-sending
-- schema for Phase 5 (#0040, PRD §6.2). Numbered after subscribers/interests
-- (000009/000010) because campaign_interests and email_sends both carry
-- foreign keys into those tables.
--
-- Migration ordering (PRD §6.2's "Migration ordering" note):
-- email_campaigns.workshop_id conceptually references workshops(id), but
-- workshops does not exist until Phase 6 (#0050) and Phase 5 must stay
-- independently deployable. The column is added here, nullable, with no
-- REFERENCES clause; #0050 attaches the FK with a later ALTER TABLE.
--
-- "campaigns" here is unrelated to ShortLinks' campaigns table (grouping
-- short links); the word is coincidental and that table was never ported.
CREATE TABLE email_campaigns (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,                    -- internal label, not shown to recipients
    subject       TEXT NOT NULL,
    preheader     TEXT,
    body_md       TEXT NOT NULL,                     -- authored Markdown source
    status        TEXT NOT NULL DEFAULT 'draft',
                  -- draft | scheduled | sending | sent | canceled | failed
    audience_mode TEXT NOT NULL DEFAULT 'any_of',
                  -- all | any_of | all_of | none_selected
    workshop_id   BIGINT,                            -- FK to workshops(id) added by #0050 (Phase 6)
    scheduled_at  TIMESTAMPTZ,
    started_at    TIMESTAMPTZ,
    completed_at  TIMESTAMPTZ,
    created_by    BIGINT REFERENCES users(id),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Guard the enum-like columns at the database, matching the
-- subscribers_status_check / suppressions_reason_check precedent: a stray
-- UPDATE (a future migration, a one-off admin query) can't park a row in a
-- value no store method upstream expects.
ALTER TABLE email_campaigns
    ADD CONSTRAINT email_campaigns_status_check
    CHECK (status IN ('draft', 'scheduled', 'sending', 'sent', 'canceled', 'failed'));

ALTER TABLE email_campaigns
    ADD CONSTRAINT email_campaigns_audience_mode_check
    CHECK (audience_mode IN ('all', 'any_of', 'all_of', 'none_selected'));

-- campaign_interests: which interests target an any_of/all_of campaign.
-- Ignored (not rejected) by #0044's audience predicate when the campaign's
-- mode is 'all' or 'none_selected' — PRD §6.1 treats zero-interest
-- subscribers as a real, expected segment, and rows here for an 'all'
-- campaign are a UI leftover, not an error.
--
-- ON DELETE CASCADE on both columns per PRD §6.2 and #0040's carried-in
-- requirement from #0044's plan: deleting a campaign or an interest must
-- not leave an orphaned targeting row behind.
CREATE TABLE campaign_interests (
    campaign_id BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    interest_id BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    PRIMARY KEY (campaign_id, interest_id)
);

-- email_sends: one row per (campaign, subscriber), materialized when a send
-- starts (#0044). The UNIQUE key below is the idempotency guarantee: a
-- crashed and restarted send can never double-deliver, because
-- materialization re-runs as `ON CONFLICT (campaign_id, subscriber_id) DO
-- NOTHING` — never DO UPDATE, which would be a documented path from 'sent'
-- back to 'queued', i.e. a double-send written into the schema itself.
-- Carve-out: Postgres treats NULLs as distinct for UNIQUE purposes, so once
-- #0060's erasure sets a row's subscriber_id to NULL, that row is no longer
-- constrained by this key against any other NULL row. Harmless today — an
-- erased subscriber can never be re-materialized into a new send, so there
-- is nothing left to double-deliver to — but the key is not a guarantee for
-- anonymized rows the way it is for live ones.
--
-- 'skipped' is carried in from #0044's plan (2026-08-21), not present in
-- PRD §6.2's prose table but already listed in its email_sends comment: the
-- send worker re-checks eligibility inside the transaction that claims each
-- batch, and a recipient who was eligible at materialization but has since
-- unsubscribed or been suppressed is marked 'skipped' rather than mailed.
-- Landing it here avoids #0044 shipping its own ALTER TABLE just to widen
-- this CHECK.
--
-- subscriber_id is nullable with ON DELETE SET NULL, not the NOT NULL/
-- CASCADE pair an earlier version of this migration had (#0060, edited into
-- this migration in place — greenfield, CLAUDE.md §1). #0060's acceptance
-- criteria require a GDPR erasure to ANONYMIZE this table's rows for the
-- erased subscriber rather than delete them, so historical campaign counts
-- never silently change. A CASCADE delete would destroy exactly the rows
-- erasure must preserve, so subscribers.Store.Erase (internal/subscribers/
-- erase.go) anonymizes email_sends.email for the subscriber's rows and lets
-- the subsequent DELETE FROM subscribers set subscriber_id to NULL via this
-- FK — severing the identifying link without losing the row or its status.
CREATE TABLE email_sends (
    id             BIGSERIAL PRIMARY KEY,
    campaign_id    BIGINT NOT NULL REFERENCES email_campaigns(id) ON DELETE CASCADE,
    subscriber_id  BIGINT REFERENCES subscribers(id) ON DELETE SET NULL,
    email          TEXT NOT NULL,                    -- snapshot of subscribers.email at materialization (anonymized in place by #0060's erasure)
    status         TEXT NOT NULL DEFAULT 'queued',
                   -- queued | sent | failed | skipped
                   -- 'bounced'/'complained' were removed by #0131: SES bounce
                   -- and complaint events are recorded exclusively in
                   -- email_events (#0038), never stamped onto this column, so
                   -- admitting the two values here described states no code
                   -- path could ever produce. #0049's per-campaign stats
                   -- reconcile bounce/complaint counts via a JOIN against
                   -- email_events instead.
    ses_message_id TEXT,
    attempts       INT NOT NULL DEFAULT 0,
    error          TEXT,
    sent_at        TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (campaign_id, subscriber_id)
);

ALTER TABLE email_sends
    ADD CONSTRAINT email_sends_status_check
    CHECK (status IN ('queued', 'sent', 'failed', 'skipped'));

-- Same normalization guard subscribers.email and email_events.recipient
-- already carry: the snapshot is taken from an already-normalized
-- subscribers.email, so this can never fail in practice, but it closes off
-- a future direct INSERT that forgets to normalize.
ALTER TABLE email_sends
    ADD CONSTRAINT email_sends_email_normalized
    CHECK (email = lower(trim(email)));

-- The send worker's claim query (PRD §6.6): `SELECT ... FROM email_sends
-- WHERE campaign_id=$1 AND status='queued' ORDER BY id ... FOR UPDATE SKIP
-- LOCKED`. Carried in from #0044's plan as idx_email_sends_queued.
CREATE INDEX idx_email_sends_queued ON email_sends (campaign_id, id)
    WHERE status = 'queued';

-- #0049's SES-event reconciliation joins email_sends to email_events on
-- ses_message_id.
CREATE INDEX idx_email_sends_message_id ON email_sends (ses_message_id);
