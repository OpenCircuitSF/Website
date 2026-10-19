-- #0603: outreach short URLs were stored as https://host/KEY, but ShortLinks
-- serves redirects only at /u/KEY (anything else is its SPA, HTTP 200), so
-- every posted link was dead. This repairs the stored URLs to https://host/u/KEY.
--
-- Only rows whose short_url does not already contain '/u/' are touched, so
-- the migration is idempotent and leaves correct rows alone.
--
-- Post bodies hold either the {link} token (a draft not yet prepared; resolved
-- later from the now-corrected short_url, so nothing to rewrite) or the
-- expanded URL (written by SetPrepared). For the expanded form the row's own
-- old URL is replaced by its own new URL, in the same statement that rewrites
-- short_url (every SET expression sees the pre-update row). Only drafts,
-- scheduled, failed and manual posts have their body rewritten: posted and
-- queued rows record what was (or is about to be) sent, so their body is left
-- alone, though their short_url column is still repaired.
UPDATE outreach_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url NOT LIKE '%/u/%';

UPDATE outreach_flyer_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url NOT LIKE '%/u/%';

UPDATE outreach_posts
   SET body = CASE WHEN status IN ('draft', 'scheduled', 'failed', 'manual')
                   THEN replace(body, short_url, regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/'))
                   ELSE body END,
       short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url IS NOT NULL
   AND short_url NOT LIKE '%/u/%';
