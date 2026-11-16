-- #0674: reverses 000053_api_tokens_v2.up.sql. Restoring 000051's
-- engagement:read-only scope CHECK fails while any token carries a scope
-- added by 000053; the file runs as one implicit transaction, so that
-- failure changes no schema or row. golang-migrate still records version 52
-- dirty, so recover with `migrate force 53`, delete those tokens by hand,
-- and run the down again. The refusal is deliberate: a down migration should
-- not destroy tokens an admin created.
ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_scopes_check;

ALTER TABLE api_tokens ADD CONSTRAINT api_tokens_scopes_check
    CHECK (cardinality(scopes) > 0 AND scopes <@ ARRAY['engagement:read']::text[]);

ALTER TABLE api_tokens DROP CONSTRAINT api_tokens_expires_after_created;

ALTER TABLE api_tokens DROP COLUMN expires_at;
