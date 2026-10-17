-- #0564: outreach connection credentials (ShortLinks, Bluesky, Mastodon)
-- saved from admin Settings. One row per field that has a saved value; no
-- row means "fall back to the environment variable".
--
-- A dedicated table rather than the settings table: GET /admin/settings
-- returns every settings row with its value and PATCH audits old/new values,
-- both wrong for a secret.
--
-- Secrets are stored in plaintext, protected like the rest of the database
-- and its backups. The only realistic encryption key is an environment
-- variable on the same box as the database, so encryption would protect only
-- a leaked backup, which already holds more valuable data. All three secrets
-- can be revoked at their service. The application never returns, logs, or
-- audits a secret value.
CREATE TABLE outreach_connection_values (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT outreach_connection_values_key_check CHECK (key IN (
        'shortlinks_base_url', 'shortlinks_api_token',
        'bluesky_handle', 'bluesky_app_password', 'bluesky_pds_url',
        'mastodon_base_url', 'mastodon_token')),
    CONSTRAINT outreach_connection_values_value_check CHECK (
        value <> '' AND char_length(value) <= 2048)
);
