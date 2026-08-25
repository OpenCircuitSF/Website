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
