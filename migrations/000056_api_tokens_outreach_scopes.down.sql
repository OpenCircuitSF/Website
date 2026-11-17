-- #0696: reverses 000056_api_tokens_outreach_scopes.up.sql. Restoring
-- 000053's scope CHECK fails while any token carries a scope added by 000056;
-- the file runs as one implicit transaction, so that failure changes no
-- schema or row. golang-migrate still records version 55 dirty, so recover
-- with `migrate force 56`, delete those tokens by hand, and run the down
-- again. The refusal is deliberate: a down migration should not destroy
-- tokens an admin created.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;

ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check
    CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY[
        'engagement:read',
        'guides:read',
        'guide_posts:read',
        'guide_posts:write',
        'events:read',
        'events:write',
        'venues:read'
    ]::text[]);
