-- #0123, PRD §6.8: every SENT campaign is also a permanent public web page
-- at /archive/{slug}. This adds the three columns that back it to
-- email_campaigns.
--
-- migrations/000017 (which created email_campaigns) is frozen -- applied to
-- production since 2026-08-25, the day the greenfield exception (CLAUDE.md
-- §1) expired -- so this lands as a new ALTER TABLE rather than an edit to
-- the CREATE TABLE that owns the table. See issues/0123.md's "Correction
-- (#0293, 2026-08-27)" note: the issue originally called for editing
-- 000017 directly under the (then still open) greenfield exception; that
-- plan is struck through in the issue file, not deleted, as the record of
-- what was true before the expiry.
--
-- Existing-row safety: no email_campaigns row exists in production (#0272
-- names what production data does exist, and it is not campaign rows) and
-- this repository has no seed script or fixture that inserts one outside a
-- test's own isolated database (every INSERT INTO email_campaigns is a
-- test fixture, run against a per-agent scratch database that is dropped
-- afterward -- CLAUDE.md §5a). So `ADD COLUMN slug TEXT UNIQUE NOT NULL`
-- with no DEFAULT is safe exactly as PRD §6.2's greenfield note originally
-- argued -- the difference #0293 corrects is only WHICH migration file the
-- column lands in, not whether a backfill is needed.
ALTER TABLE email_campaigns ADD COLUMN slug TEXT UNIQUE NOT NULL;

-- NOT NULL alone does not stop an empty string, and "slug is never blank" is
-- an invariant the worker (CompleteIfDone), the SEO adapter, and the email
-- renderer's "View this email in your browser" link (which gates on
-- Slug != "") all rely on. Added in review of #0123 (2026-08-27) after
-- AdminCampaignsHandler.Patch was found to construct mailing.CampaignUpdate
-- without a Slug field, silently writing '' through Update on every PATCH.
-- The application-level fixes (the handler now passes the current slug
-- through; CampaignStore.Update treats an empty CampaignUpdate.Slug as
-- "keep current") make that specific bug impossible, but this constraint is
-- the backstop that makes ANY future caller's omission fail loudly at the
-- database instead of storing an empty slug.
ALTER TABLE email_campaigns
    ADD CONSTRAINT email_campaigns_slug_not_blank_check
    CHECK (btrim(slug) <> '');

-- archive_status: 'pending' until the campaign is actually sent (the
-- worker's CompleteIfDone stamps 'published' + archived_at in the same
-- UPDATE that flips status to 'sent' -- see internal/mailing/worker_store.go).
-- 'withheld' is the admin's own lever (PATCH /admin/campaigns/{id}/archive)
-- to pull an already-published page: 410 Gone, dropped from the index and
-- the sitemap, per PRD §6.8's table and this issue's "withheld must be 410,
-- not 404" note.
ALTER TABLE email_campaigns ADD COLUMN archive_status TEXT NOT NULL DEFAULT 'pending';

ALTER TABLE email_campaigns
    ADD CONSTRAINT email_campaigns_archive_status_check
    CHECK (archive_status IN ('pending', 'published', 'withheld'));

ALTER TABLE email_campaigns ADD COLUMN archived_at TIMESTAMPTZ;
