-- #0312: an admin needs to be able to re-send a failed/abandoned import
-- invitation exactly once more, ever -- the bounded, user-approved
-- deviation from PRD §6.10.1's "one invitation per address, ever" recorded
-- in issues/0312.md's "Decision" section (approved 2026-08-31). invited_at
-- (migrations/000023) stays write-once and keeps governing every AUTOMATED
-- import path unconditionally; this column records the single admin-
-- triggered exception, separately, so the two can never be confused.
--
-- Write-once by CONSTRAINT, not merely by convention: AdminResendInvitation
-- (internal/subscribers/pending.go) refuses a second call once this is
-- non-NULL (ErrInviteAlreadyResent), but the CHECK below additionally
-- guarantees a re-send can never be stamped on a row that was never an
-- invitation in the first place -- invite_resent_at implies invited_at.
--
-- 000010/000023 (subscribers' original columns and its invite-mode
-- provenance columns) are both frozen (CLAUDE.md §1 -- applied to
-- production since 2026-08-25), so this lands as a new ALTER TABLE rather
-- than an edit to either. No backfill is owed: every existing row gets NULL,
-- which is the correct starting value -- no address has ever been
-- re-invited before this column existed.
ALTER TABLE subscribers ADD COLUMN invite_resent_at TIMESTAMPTZ;

ALTER TABLE subscribers ADD CONSTRAINT subscribers_invite_resent_requires_invite
    CHECK (invite_resent_at IS NULL OR invited_at IS NOT NULL);
