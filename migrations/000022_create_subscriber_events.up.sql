-- subscriber_events: the append-only activity log (§6.11, #0126) — one row
-- per meaningful thing that happened to an address, in this project's own
-- vocabulary. Distinct from email_events (raw SES/SNS payloads, #0038) and
-- from audit_log (privileged staff actions against the console) — see
-- internal/subscribers/events.go's package doc comment for the full
-- three-logs argument.
--
-- email is snapshotted on every row (NOT NULL, no normalization CHECK: it
-- is written from an already-normalized subscribers.email at the point of
-- occurrence) so the log survives erasure of the subscribers row — #0060's
-- Erase redacts this column and lets subscriber_id go NULL via the FK
-- below, rather than deleting the row, so the erasure's own evidence
-- survives being performed. See internal/subscribers/events.go's redaction
-- for the exact statement.
--
-- action is validated in Go against a closed set (internal/subscribers.
-- Action) rather than a CHECK constraint, matching outbound_queue.kind's
-- precedent in this same migration pair: PRD §6.11's table is not phrased
-- as a CHECK, and a Go-side validator gives RecordEvent a typed error
-- instead of a raw constraint-violation to pattern-match.
--
-- import_id is DELIBERATELY OMITTED from this migration, though PRD §6.2
-- lists it (BIGINT REFERENCES subscriber_imports(id) ON DELETE SET NULL).
-- subscriber_imports does not exist yet — #0125 creates it, and #0126
-- (this migration) blocks #0125 — so there is no table for the column to
-- reference. #0125 adds the column (and its FK) when it creates
-- subscriber_imports; see #0126's plan §1 for why this is guard-safe:
-- internal/db/prd_index_parity_test.go only runs migrations -> PRD, never
-- PRD -> migrations, and its header names these three tables as the case
-- it was written to tolerate.
CREATE TABLE subscriber_events (
    id            BIGSERIAL PRIMARY KEY,
    subscriber_id BIGINT REFERENCES subscribers(id) ON DELETE SET NULL,
    email         TEXT NOT NULL,          -- snapshot; survives erasure of the row
    action        TEXT NOT NULL,          -- enum, see internal/subscribers.Action
    campaign_id   BIGINT REFERENCES email_campaigns(id) ON DELETE SET NULL,
    actor_user_id BIGINT REFERENCES users(id),   -- NULL when the subscriber or a webhook acted
    detail        JSONB,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_subscriber_events_subscriber ON subscriber_events (subscriber_id, created_at DESC);
CREATE INDEX idx_subscriber_events_email ON subscriber_events (email, created_at DESC);
CREATE INDEX idx_subscriber_events_action ON subscriber_events (action, created_at DESC);
