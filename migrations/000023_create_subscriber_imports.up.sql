-- subscriber_imports: one row per CSV import run (PRD §6.10, #0125). The
-- batch is the unit of revocation: an import that turns out to lack consent
-- is undone wholesale, not row by row.
--
-- Numbered after subscriber_events (000022) rather than alongside
-- subscribers (000010): subscribers.import_id and subscriber_events.import_id
-- both reference this table, and both of those migrations shipped before
-- this issue existed — 000022's own comment names this exact migration as
-- the one that would add the column and its FK once the target exists (see
-- that file's "import_id is DELIBERATELY OMITTED" note). This migration
-- does three things: creates subscriber_imports itself, then backfills the
-- two forward-referencing FKs those earlier tables were left without.
--
-- consent_mode selects between #0125's prior_consent (inserts active, sends
-- nothing) and #0129's invite (inserts pending, sends one invitation) —
-- only prior_consent has a producer today; invite is validated and stored
-- but #0125's commit path refuses it (ErrConsentModeNotSupported) until
-- #0129 lands. source_detail/collected_at/consent_note are all required at
-- the Go layer (not enforced by a NOT NULL here beyond consent_note/
-- collected_at, which PRD §6.2 itself marks NOT NULL) — see
-- internal/subscribers/imports.go's Commit for the validation.
CREATE TABLE subscriber_imports (
    id              BIGSERIAL PRIMARY KEY,
    source          TEXT NOT NULL,          -- luma | eventbrite | meetup | manual_csv | other
    source_detail   TEXT,                   -- event name, export filename, URL
    consent_mode    TEXT NOT NULL DEFAULT 'invite',  -- prior_consent | invite
    consent_note    TEXT NOT NULL,          -- how consent was obtained, in the admin's words
    collected_at    DATE NOT NULL,          -- when the SOURCE collected the addresses;
                                            -- also quoted in the #0129 invitation copy
    filename        TEXT,
    row_count       INT NOT NULL DEFAULT 0,
    inserted_count  INT NOT NULL DEFAULT 0,
    skipped_count   INT NOT NULL DEFAULT 0,
    invited_count   INT NOT NULL DEFAULT 0,   -- invite mode only (#0129)
    confirmed_count INT NOT NULL DEFAULT 0,   -- invitations accepted (#0129), updated as they land
    status          TEXT NOT NULL DEFAULT 'committed',  -- committed | revoked
    revoked_at      TIMESTAMPTZ,
    revoked_reason  TEXT,
    imported_by     BIGINT REFERENCES users(id),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE subscriber_imports
    ADD CONSTRAINT subscriber_imports_source_check
    CHECK (source IN ('luma', 'eventbrite', 'meetup', 'manual_csv', 'other'));

ALTER TABLE subscriber_imports
    ADD CONSTRAINT subscriber_imports_consent_mode_check
    CHECK (consent_mode IN ('prior_consent', 'invite'));

ALTER TABLE subscriber_imports
    ADD CONSTRAINT subscriber_imports_status_check
    CHECK (status IN ('committed', 'revoked'));

ALTER TABLE subscriber_imports
    ADD CONSTRAINT subscriber_imports_consent_note_check
    CHECK (btrim(consent_note) <> '');

-- subscribers.import_id (migrations/000010) was created as a plain BIGINT
-- with no REFERENCES — subscriber_imports did not exist yet. The FK arrives
-- now that it does.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_import_id_fkey
    FOREIGN KEY (import_id) REFERENCES subscriber_imports(id);

-- subscriber_events.import_id (PRD §6.2) was deliberately omitted by
-- migrations/000022 for the identical reason — see that file's comment.
ALTER TABLE subscriber_events
    ADD COLUMN import_id BIGINT REFERENCES subscriber_imports(id) ON DELETE SET NULL;
