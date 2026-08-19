-- Enforce NOT NULL on a handful of session/passkey columns that 000002
-- (auth_credentials) and 000003 (sessions) leave nullable at the SQL level
-- even though every write path already supplies them:
--   passkey_credentials.user_id  -> NOT NULL
--   sessions.user_id             -> NOT NULL
--   sessions.created_at          -> NOT NULL DEFAULT now()
--   sessions.expires_at          -> NOT NULL
--   sessions.last_seen_at        -> NOT NULL
--
-- Ported from ShortLinks migration 000013, which existed there to correct a
-- production schema-drift incident (golang-migrate tracks version *numbers*,
-- not file *content*, so a database that had already run the 000004/000005
-- migrations before they were edited in place never re-applied the edit).
-- This project has no deployed database and no such history — squashing
-- these constraints directly into 000002/000003 was considered and rejected
-- only because #0004's acceptance criteria call for exactly seven
-- contiguous migrations (000001-000007), one per surviving ShortLinks
-- migration; kept as a separate step here for that reason, not because the
-- drift scenario applies to this repo.
--
-- On EVERY database this migration ever runs against here (there is no
-- pre-existing state), all five columns already satisfy these constraints
-- immediately after 000002/000003 apply, so this migration's DDL is a
-- deliberate no-op in practice: SET NOT NULL / SET DEFAULT on a column that
-- already has them succeeds and changes nothing. It still runs the same
-- defensive existence check the ShortLinks version did (see safety note
-- below) rather than assuming that's true, so a bug in a future edit to
-- 000002/000003 fails loudly here instead of silently.
--
-- Safety: there is no truthful value to backfill a NULL user_id with (it is
-- a FK to an unknown user), and these are the session/passkey tables, so
-- guessing or silently deleting rows is worse than stopping. This migration
-- FAILS LOUDLY, before making any change, if it finds a row that would
-- violate the new constraints.
--
-- This whole file runs in one transaction (golang-migrate's default for the
-- postgres driver), so an abort here leaves the schema completely
-- untouched — no partial application. golang-migrate does, however, mark
-- schema_migrations dirty at version 7 when a migration raises. Recovering
-- from an abort therefore takes three steps, not one:
--
--   1. Inspect and resolve (or deliberately delete) the offending rows, e.g.
--        SELECT * FROM passkey_credentials WHERE user_id IS NULL;
--        SELECT * FROM sessions
--          WHERE user_id IS NULL OR created_at IS NULL
--             OR expires_at IS NULL OR last_seen_at IS NULL;
--   2. migrate -path migrations -database "$DATABASE_URL" force 6
--      (safe: the transaction rolled back, so the schema really is still at
--       6. `force` rewrites schema_migrations — it sets the recorded
--       version back to 6 AND clears the dirty flag — but it alters no
--       tables, which is why it is the right tool here and not a way to
--       skip a migration that genuinely half-applied.)
--   3. migrate -path migrations -database "$DATABASE_URL" up
--
-- Both tables are checked before either is altered, and all violations are
-- reported in one message, so an operator fixing them by hand does not have
-- to discover the second table on a second failed attempt.
--
-- Locking: SET NOT NULL takes ACCESS EXCLUSIVE and scans the table to
-- verify. These are the session and passkey tables — small by nature at
-- this project's scale — so the pause is negligible.
--
-- Postgres compatibility: DO blocks, RAISE EXCEPTION, SET NOT NULL and SET
-- DEFAULT are all long-standing syntax; nothing here requires a specific
-- recent Postgres version.
DO $$
DECLARE
    bad_passkeys BIGINT;
    bad_sessions BIGINT;
BEGIN
    SELECT count(*) INTO bad_passkeys
        FROM passkey_credentials
        WHERE user_id IS NULL;

    SELECT count(*) INTO bad_sessions
        FROM sessions
        WHERE user_id IS NULL
           OR created_at IS NULL
           OR expires_at IS NULL
           OR last_seen_at IS NULL;

    IF bad_passkeys > 0 OR bad_sessions > 0 THEN
        RAISE EXCEPTION
            'migration 000007 aborted: % row(s) in passkey_credentials have a NULL user_id and % row(s) in sessions have a NULL user_id/created_at/expires_at/last_seen_at; no change was made. Resolve those rows, then: migrate force 6 && migrate up',
            bad_passkeys, bad_sessions;
    END IF;
END
$$;

ALTER TABLE passkey_credentials
    ALTER COLUMN user_id SET NOT NULL;

ALTER TABLE sessions
    ALTER COLUMN user_id SET NOT NULL,
    ALTER COLUMN created_at SET NOT NULL,
    ALTER COLUMN created_at SET DEFAULT now(),
    ALTER COLUMN expires_at SET NOT NULL,
    ALTER COLUMN last_seen_at SET NOT NULL;
