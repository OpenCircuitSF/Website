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
-- Existing-row safety, corrected (#0404, 2026-09-03): the "no email_campaigns
-- row exists in production" premise this file originally argued from was
-- true when written (2026-08-27) and is false now. The user drafted a real
-- campaign through the admin console (id 1, "Open Circuits SF: September
-- 2026") and it sits in production's email_campaigns table today. The
-- original `ADD COLUMN slug TEXT UNIQUE NOT NULL` -- no DEFAULT -- fails
-- outright against that row with "column \"slug\" ... contains null values"
-- and leaves schema_migrations dirty at 25.
--
-- The column is therefore added nullable below, backfilled, and only then
-- constrained NOT NULL -- the same shape migrations/000024 already uses
-- against subscriber_imports.source_detail. The one production row (id 1)
-- gets slug '09-2026', decided by the user in #0405 (the MM-YYYY newsletter
-- template) -- deliberately NOT the value mailing.slugifyCampaign would mint
-- from its subject (`open-circuits-sf-september-2026`, measured by #0404's
-- planning pass). #0405 owns the minting rule for new campaigns and still
-- has open questions about it; this file only fixes the one existing row and
-- does not implement that rule.
--
-- This file was edited in place under CLAUDE.md §1's actual rule -- "never
-- edit a migration that has been applied to a database anyone cares about"
-- -- rather than repaired with a new migration, because production has
-- never applied 000025: measured 2026-09-03, production's schema_migrations
-- sat at version 22, not dirty. Every database that HAD already applied the
-- old form of this file (every local dev database, opencircuit_test, the
-- shared test template, and per-agent scratch databases) is rebuildable
-- from migrations/ in seconds and produces an identical resulting schema --
-- same email_campaigns_slug_key constraint name, same NOT NULL -- so this
-- edit is a no-op for all of them. See #0404's `## Plan` for the full
-- reasoning and the schema-diff proof.
ALTER TABLE email_campaigns ADD COLUMN slug TEXT UNIQUE;

-- The one row that exists anywhere this migration will ever meet a
-- non-empty email_campaigns: production's id 1, measured by #0404. Its slug
-- is '09-2026', decided by the user in #0405. Identity-checked on subject as
-- well as id so this can never stamp '09-2026' onto some other database's
-- unrelated row 1.
UPDATE email_campaigns
   SET slug = '09-2026'
 WHERE id = 1
   AND subject = 'Open Circuits SF: September 2026'
   AND slug IS NULL;

-- Safety net. No row anywhere is expected to reach this statement -- every
-- database that had already applied this migration had zero rows, and
-- production has exactly the one row the UPDATE above names. But SET NOT
-- NULL below fails hard on any straggler, so give it a value that is
-- non-blank and unique by construction (id is the primary key) and reads
-- unmistakably as a placeholder rather than impersonating a minted slug. An
-- UPDATE matching zero rows is a no-op -- the same belt-and-braces shape
-- migrations/000024 already uses.
UPDATE email_campaigns
   SET slug = 'campaign-' || id
 WHERE slug IS NULL;

ALTER TABLE email_campaigns ALTER COLUMN slug SET NOT NULL;

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
