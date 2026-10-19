-- #0603 down: reverses ONLY the short_url columns (strips the '/u/' segment
-- back out). It does not restore post bodies: the up migration rewrote
-- the expanded URL inside drafts, scheduled, failed and manual bodies, and the
-- old text is not recoverable from the new. Restoring a dead-link form is not
-- something a body should be put back to anyway.
UPDATE outreach_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/u/', '\1/')
 WHERE short_url LIKE '%/u/%';

UPDATE outreach_flyer_links
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/u/', '\1/')
 WHERE short_url LIKE '%/u/%';

UPDATE outreach_posts
   SET short_url = regexp_replace(short_url, '^(https?://[^/]+)/u/', '\1/')
 WHERE short_url IS NOT NULL
   AND short_url LIKE '%/u/%';
