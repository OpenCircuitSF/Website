-- #0603: outreach short URLs were stored as https://host/KEY, but ShortLinks
-- serves redirects only at /u/KEY (anything else is its SPA, HTTP 200), so
-- every posted link was dead. This repairs the stored URLs to https://host/u/KEY.
--
-- Only rows whose path does not already start with '/u/' are touched, so
-- the migration is idempotent and leaves correct rows alone. The test is the
-- anchored path prefix, not a substring: a key that itself begins with 'u'
-- (https://host/uXLcM0) is still broken and becomes https://host/u/uXLcM0.
--
-- Post bodies hold either the {link} token (a draft not yet prepared; resolved
-- later from the now-corrected short_url, so nothing to rewrite) or the
-- expanded URL (written by SetPrepared). For the expanded form the row's own
-- old URL is replaced by its own new URL (regex-quoted, and only where no
-- further key character or '/' follows, so host/abc never rewrites inside
-- host/abcd, and host/u never inside an already-repaired host/u/u after a
-- down and up),
-- in the same statement that rewrites
-- short_url (every SET expression sees the pre-update row). Only drafts,
-- scheduled, failed and manual posts have their body rewritten: posted and
-- queued rows record what was (or is about to be) sent, so their body is left
-- alone, though their short_url column is still repaired.
UPDATE outreach_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url !~ '^https?://[^/]+/u/';

UPDATE outreach_flyer_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url !~ '^https?://[^/]+/u/';

UPDATE outreach_posts
   SET body = CASE WHEN status IN ('draft', 'scheduled', 'failed', 'manual')
                   THEN regexp_replace(body,
                                       regexp_replace(short_url, '([.^$*+?()\[\]{}|\\])', '\\\1', 'g') || '(?![A-Za-z0-9_/-])',
                                       regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/'),
                                       'g')
                   ELSE body END,
       short_url = regexp_replace(short_url, '^(https?://[^/]+)/', '\1/u/')
 WHERE short_url IS NOT NULL
   AND short_url !~ '^https?://[^/]+/u/';
