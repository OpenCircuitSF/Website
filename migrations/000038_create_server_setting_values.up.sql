-- #0566: operational server settings (media directory, email identity, SES
-- flags, send batch size) saved from admin Settings. One row per field that
-- has a saved value; no row means "fall back to the environment variable".
--
-- A dedicated table rather than the settings table, for the same three
-- reasons outreach_connection_values is: GET /admin/settings returns every
-- settings row and PATCH audits old/new values, and "no row" has to mean
-- "fall back", which the settings table's seeded rows cannot express.
--
-- Owned by internal/serversettings. None of these values is a secret, so
-- there is no at-rest question; the application still audits field names only.
CREATE TABLE server_setting_values (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT server_setting_values_key_check CHECK (key IN (
        'media_dir', 'email_from', 'email_reply_to', 'email_list_domain',
        'ses_sandbox', 'ses_inbound_bucket', 'send_batch_size')),
    CONSTRAINT server_setting_values_value_check CHECK (
        value <> '' AND char_length(value) <= 2048)
);
