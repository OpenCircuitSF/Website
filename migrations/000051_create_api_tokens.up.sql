-- #0652: bearer tokens an admin creates so a machine (Home Assistant) can
-- read /api/v1/ routes. Only the SHA-256 of the token is stored;
-- display_prefix is its first 12 characters, shown in the admin list.
CREATE TABLE api_tokens (
    id             BIGSERIAL PRIMARY KEY,
    name           TEXT NOT NULL,
    token_hash     BYTEA NOT NULL,
    display_prefix TEXT NOT NULL,
    scopes         TEXT[] NOT NULL,
    created_by     BIGINT REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_used_at   TIMESTAMPTZ,
    revoked_at     TIMESTAMPTZ,
    CONSTRAINT api_tokens_token_hash_key UNIQUE (token_hash),
    CONSTRAINT api_tokens_token_hash_length CHECK (octet_length(token_hash) = 32),
    CONSTRAINT api_tokens_name_check CHECK (char_length(btrim(name)) BETWEEN 1 AND 100),
    CONSTRAINT api_tokens_display_prefix_check CHECK (left(display_prefix, 5) = 'ocsf_' AND char_length(display_prefix) = 12),
    CONSTRAINT api_tokens_scopes_check CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['engagement:read']::text[])
);
