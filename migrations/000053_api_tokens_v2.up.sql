-- #0674: API keys v2. Adds an optional expiry and widens the scope set so a
-- key can drive writes. 000051 is applied in production and stays untouched;
-- the scope CHECK is dropped and re-added here instead. The array below must
-- match apitokens.KnownScopes (guarded by internal/db's
-- TestAPITokenScopesMatchMigration).
ALTER TABLE api_tokens ADD COLUMN expires_at TIMESTAMPTZ;

ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_expires_after_created
    CHECK (expires_at IS NULL OR expires_at > created_at);

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
