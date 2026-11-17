-- #0696: widens the api_tokens scope set with outreach and media, the same
-- way 000053 widened 000051's. The array below must match
-- apitokens.KnownScopes (guarded by internal/db's
-- TestAPITokenScopesMatchMigration).
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;

ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check
    CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY[
        'engagement:read',
        'guides:read',
        'guide_posts:read',
        'guide_posts:write',
        'events:read',
        'events:write',
        'venues:read',
        'outreach:read',
        'outreach:write',
        'media:read',
        'media:write'
    ]::text[]);
