-- #0582: an automatic post whose text the admin hand-edited ("customized") is
-- left alone when the shared announcement message is saved. It cannot be
-- derived: Bluesky and Mastodon leads differ by design, so Mastodon would read
-- as customized on first load, and comparing against a re-rendered template
-- breaks when the workshop summary changes. A new migration, not an edit:
-- outreach_posts (000032) is already applied to production.
ALTER TABLE outreach_posts ADD COLUMN customized BOOLEAN NOT NULL DEFAULT false;
