-- subscribers: one row per email address, independent of any user account.
-- Per PRD §6.2. Numbered after interests (000009) because
-- subscriber_interests below carries a foreign key to it.
CREATE TABLE subscribers (
    id                 BIGSERIAL PRIMARY KEY,
    email              TEXT UNIQUE NOT NULL,        -- stored lower(trim(...))
    status             TEXT NOT NULL DEFAULT 'pending',
                       -- pending | active | unsubscribed | bounced | complained
    confirm_token      TEXT UNIQUE,                 -- NULL once confirmed
    confirm_sent_at    TIMESTAMPTZ,
    confirm_expires_at TIMESTAMPTZ,
    confirmed_at       TIMESTAMPTZ,
    manage_token       TEXT UNIQUE NOT NULL,        -- long-lived preference/unsub token
    signup_ip          INET,                        -- consent evidence
    signup_user_agent  TEXT,
    utm_source         TEXT,
    utm_medium         TEXT,
    utm_campaign       TEXT,
    unsubscribed_at    TIMESTAMPTZ,
    unsubscribe_source TEXT,                        -- one_click | preferences | mailto | admin
    -- Provenance (#0125, PRD §6.10). Every address must be able to answer
    -- "where did this come from, and when?" without reading the event log.
    -- import_id has no inline REFERENCES here: subscriber_imports does not
    -- exist until migrations/000023 creates it (this table, 000010, is
    -- numbered first), so the FK is added there via ALTER TABLE once the
    -- target exists — see 000023's own comment. The column itself lands
    -- here, per the greenfield note (PRD §6.2): nothing is in production,
    -- so a Phase 8 column is added directly to the migration that owns
    -- subscribers rather than stacked as a later ALTER TABLE.
    source             TEXT NOT NULL DEFAULT 'signup_form',
                       -- signup_form | import | admin_manual | api
    source_detail      TEXT,                         -- e.g. 'luma:oc-soldering-2026-05'
    consent_basis      TEXT,                         -- double_opt_in | imported_prior_consent | admin_attested
    import_id          BIGINT,
    invited_at         TIMESTAMPTZ,                   -- #0129: set once, ever — the presence of
                                                       -- this column is what makes "one invitation
                                                       -- per address, ever" enforceable across
                                                       -- separate imports
    -- Delivery health (#0124, PRD §6.9). The streak is the live decision
    -- variable the circuit breaker and the repeated-soft-bounce rule read;
    -- email_events (000014) remains the immutable history behind it.
    -- soft_bounce_streak counts CONSECUTIVE Transient/Undetermined bounces
    -- since the last successful Delivery — zeroed by a Delivery event, and
    -- also zeroed by removing a suppression (a re-enabled address gets a
    -- fresh runway, not one bounce from re-suppression). This supersedes
    -- the rolling-30-day-window rule 000015/000016 originally shipped
    -- (#0039/#0109) — see issues/0124.md's Notes for why a window has no
    -- notion of "the address recovered" and a streak does.
    soft_bounce_streak INT NOT NULL DEFAULT 0,
    last_bounce_at     TIMESTAMPTZ,
    last_delivery_at   TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Guard the status vocabulary at the database, not just in Go, so a stray
-- UPDATE (a future migration, a one-off admin query) can't park a row in an
-- unrecognized state that every store method upstream assumes can't happen.
-- 'complained' has no path back to 'active' in the store layer (CLAUDE.md §9:
-- only an admin clears that state) — this CHECK does not encode that
-- transition rule itself (transitions need history, not just the current
-- value), only the closed set of legal values.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_status_check
    CHECK (status IN ('pending', 'active', 'unsubscribed', 'bounced', 'complained'));

ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_unsubscribe_source_check
    CHECK (unsubscribe_source IS NULL
           OR unsubscribe_source IN ('one_click', 'preferences', 'mailto', 'admin'));

-- #0125: guard the provenance vocabulary the same way status/
-- unsubscribe_source are guarded above.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_source_check
    CHECK (source IN ('signup_form', 'import', 'admin_manual', 'api'));

ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_consent_basis_check
    CHECK (consent_basis IS NULL
           OR consent_basis IN ('double_opt_in', 'imported_prior_consent', 'admin_attested'));

-- Belt-and-suspenders: the store layer normalizes with lower(trim(...))
-- before every write, but this CHECK means no code path — including a
-- future direct INSERT that forgets to normalize — can persist a
-- non-normalized address.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_email_normalized
    CHECK (email = lower(trim(email)));

CREATE INDEX idx_subscribers_status ON subscribers (status);
CREATE INDEX idx_subscribers_created_at ON subscribers (created_at DESC);
CREATE INDEX idx_subscribers_confirm_expires ON subscribers (confirm_expires_at)
    WHERE confirm_token IS NOT NULL;

-- subscriber_interests: join table. A subscriber with zero rows here is
-- valid and expected (PRD §6.1) — general-announcements-only — so nothing
-- here requires at least one row per subscriber.
CREATE TABLE subscriber_interests (
    subscriber_id BIGINT NOT NULL REFERENCES subscribers(id) ON DELETE CASCADE,
    interest_id   BIGINT NOT NULL REFERENCES interests(id) ON DELETE CASCADE,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (subscriber_id, interest_id)
);
CREATE INDEX idx_subscriber_interests_interest ON subscriber_interests (interest_id);
