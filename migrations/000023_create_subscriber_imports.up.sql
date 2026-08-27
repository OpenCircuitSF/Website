-- subscriber_imports: one row per CSV import run (PRD §6.10, #0125). The
-- batch is the unit of revocation: an import that turns out to lack consent
-- is undone wholesale, not row by row.
--
-- Numbered after subscriber_events (000022) rather than alongside
-- subscribers (000010): subscribers.import_id and subscriber_events.import_id
-- both reference this table, and both of those migrations shipped before
-- this issue existed — 000022's own comment names this exact migration as
-- the one that would add the column and its FK once the target exists (see
-- that file's "import_id is DELIBERATELY OMITTED" note). 000010 also
-- shipped to production on 2026-08-25 and is append-only per CLAUDE.md §1,
-- so none of #0125's provenance columns can land there either, regardless
-- of the forward-reference issue. This migration does four things: creates
-- subscriber_imports itself, adds all five provenance columns to
-- subscribers (source/source_detail/consent_basis/import_id/invited_at)
-- plus their two CHECK constraints, then backfills the two
-- forward-referencing FKs those earlier tables were left without.
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

-- Provenance columns on subscribers (#0125, PRD §6.10). These land here,
-- not in migrations/000010, because 000010 shipped to production on
-- 2026-08-25 (CLAUDE.md §1) and is now append-only — the greenfield
-- exception this issue originally relied on has expired. import_id in
-- particular cannot be added any earlier than this migration regardless:
-- it is a forward reference to subscriber_imports, which does not exist
-- until the CREATE TABLE above runs. This ALTER TABLE must precede the FK
-- constraint immediately below, which requires the column to exist first.
ALTER TABLE subscribers
    ADD COLUMN source        TEXT NOT NULL DEFAULT 'signup_form',
                             -- signup_form | import | admin_manual | api
    ADD COLUMN source_detail TEXT,                 -- e.g. 'luma:oc-soldering-2026-05'
    ADD COLUMN consent_basis TEXT,                 -- double_opt_in | imported_prior_consent | admin_attested
    ADD COLUMN import_id     BIGINT,
    ADD COLUMN invited_at    TIMESTAMPTZ;           -- #0129: set once, ever — the presence of
                                                    -- this column is what makes "one invitation
                                                    -- per address, ever" enforceable across
                                                    -- separate imports

-- #0125: guard the provenance vocabulary the same way status/
-- unsubscribe_source are guarded in migrations/000010.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_source_check
    CHECK (source IN ('signup_form', 'import', 'admin_manual', 'api'));

ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_consent_basis_check
    CHECK (consent_basis IS NULL
           OR consent_basis IN ('double_opt_in', 'imported_prior_consent', 'admin_attested'));

-- subscribers.import_id (added above, this migration) was created as a
-- plain BIGINT with no REFERENCES — subscriber_imports did not exist until
-- the CREATE TABLE earlier in this same file. The FK arrives now that it
-- does.
ALTER TABLE subscribers
    ADD CONSTRAINT subscribers_import_id_fkey
    FOREIGN KEY (import_id) REFERENCES subscriber_imports(id);

-- subscriber_events.import_id (PRD §6.2) was deliberately omitted by
-- migrations/000022 for the identical reason — see that file's comment.
ALTER TABLE subscriber_events
    ADD COLUMN import_id BIGINT REFERENCES subscriber_imports(id) ON DELETE SET NULL;
