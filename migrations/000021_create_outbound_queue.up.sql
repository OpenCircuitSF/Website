-- outbound_queue: the durable transport for transactional mail (§6.11,
-- #0126). Campaign mail already has email_sends (migrations/000017) — a row
-- per message, claimed by a worker, an orphan sweep for a crashed claim.
-- This is that same shape applied to confirmation/welcome/notification mail,
-- which was previously dispatched from an in-process goroutine with a few
-- retries and lost entirely on an SES outage or a process restart.
--
-- kind is a free-text column with no CHECK constraint (PRD §6.2 gives it as
-- a SQL comment, not an enum) so a new kind can be added by a later issue
-- without a migration. See internal/outbox.Kind for the Go-side closed set.
--
-- payload stores TEMPLATE INPUTS, not rendered MIME, so a template fix
-- applies to mail already queued (#0126's plan §2). internal/outbox.Store's
-- MarkSent blanks payload to '{}'::jsonb once a message is sent, since a
-- 'sent' row otherwise archives a live token (confirm_token/manage_token,
-- registration/recovery tokens) forever after the row's purpose has ended;
-- an 'abandoned' row keeps its payload, because that row IS the diagnostic
-- and the token is already dead.
--
-- 'failed' is listed in the status comment (PRD §6.2) but not used by this
-- issue: the state machine here is queued -> sending -> sent | abandoned.
-- Reserved for a future use rather than invented one here.
CREATE TABLE outbound_queue (
    id              BIGSERIAL PRIMARY KEY,
    kind            TEXT NOT NULL,          -- confirmation | already_subscribed | welcome |
                                            -- goodbye | admin_alert | registration | recovery
    recipient       TEXT NOT NULL,
    subscriber_id   BIGINT REFERENCES subscribers(id) ON DELETE CASCADE,
    payload         JSONB NOT NULL,         -- template inputs, not rendered MIME
    status          TEXT NOT NULL DEFAULT 'queued',  -- queued | sent | failed | abandoned
    attempts        INT NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ses_message_id  TEXT,
    error           TEXT,
    claimed_at      TIMESTAMPTZ,            -- orphan sweep, same shape as email_sends
    sent_at         TIMESTAMPTZ,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE outbound_queue
    ADD CONSTRAINT outbound_queue_status_check
    CHECK (status IN ('queued', 'sending', 'sent', 'failed', 'abandoned'));

-- 'sending' is not in PRD §6.2's prose comment (queued | sent | failed |
-- abandoned) but is required by the claim/release/orphan-sweep state
-- machine #0126's plan §2 specifies (modelled on email_sends'/email_sends-
-- adjacent worker shapes elsewhere in this schema, none of which needs an
-- in-between "claimed" state visible to the CHECK because none of them run
-- a background claim-then-release cycle the way this queue does). Included
-- in the CHECK so a stray UPDATE can't park a row outside the five values
-- any store method expects.

-- The claim query (#0126's plan §2): `... WHERE status='queued' AND
-- next_attempt_at <= now() ORDER BY next_attempt_at, id ... FOR UPDATE SKIP
-- LOCKED`. Partial on status='queued' so the index stays small as sent/
-- abandoned rows accumulate.
CREATE INDEX idx_outbound_queue_due ON outbound_queue (next_attempt_at, id)
    WHERE status = 'queued';

-- queue_max_retries: the Go-side default (8) this key seeds. Snake_case, no
-- dots, matching every other settings key (max_send_rate,
-- soft_bounce_threshold_count) — see #0126's plan §9 item 3 for why the
-- acceptance criterion's literal "queue.max_retries" is not a valid key in
-- this project. UpdateSetting only mutates an EXISTING key, so this row
-- must exist before PATCH /admin/settings can ever change it (000008's
-- physical_address seed, 000015's soft-bounce seed).
INSERT INTO settings (key, value, updated_at)
VALUES ('queue_max_retries', '8', now())
ON CONFLICT DO NOTHING;
