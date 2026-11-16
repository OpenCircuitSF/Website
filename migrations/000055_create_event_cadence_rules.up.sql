-- #0678: what a normal month of events looks like, so the coverage report
-- (GET /api/v1/events/coverage) can say what is missing. Owned by
-- internal/cadence. Rules are stated in America/Los_Angeles. A rule matches
-- by event type, by a case-insensitive title prefix, or by both, and
-- optionally by venue. weekday uses Go's time.Weekday numbering (0 is
-- Sunday). Deleting an event type deletes its rules; deleting a venue only
-- drops a rule's venue filter.
CREATE TABLE event_cadence_rules (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT NOT NULL,
    event_type_id BIGINT REFERENCES event_types(id) ON DELETE CASCADE,
    title_prefix  TEXT,
    frequency     TEXT NOT NULL,
    weekday       SMALLINT,
    per_month     SMALLINT,
    venue_id      BIGINT REFERENCES venues(id) ON DELETE SET NULL,
    active        BOOLEAN NOT NULL DEFAULT true,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT event_cadence_rules_name_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 120),
    CONSTRAINT event_cadence_rules_prefix_check CHECK (title_prefix IS NULL OR char_length(btrim(title_prefix)) BETWEEN 1 AND 120),
    CONSTRAINT event_cadence_rules_match_check CHECK (event_type_id IS NOT NULL OR title_prefix IS NOT NULL),
    CONSTRAINT event_cadence_rules_frequency_check CHECK (
        (frequency = 'weekly'  AND weekday IS NOT NULL AND weekday BETWEEN 0 AND 6 AND per_month IS NULL) OR
        (frequency = 'monthly' AND per_month IS NOT NULL AND per_month BETWEEN 1 AND 31 AND weekday IS NULL))
);
