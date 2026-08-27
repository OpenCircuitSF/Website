ALTER TABLE subscriber_events DROP COLUMN import_id;
ALTER TABLE subscribers DROP CONSTRAINT subscribers_import_id_fkey;
ALTER TABLE subscribers DROP CONSTRAINT subscribers_consent_basis_check;
ALTER TABLE subscribers DROP CONSTRAINT subscribers_source_check;
ALTER TABLE subscribers
    DROP COLUMN invited_at,
    DROP COLUMN import_id,
    DROP COLUMN consent_basis,
    DROP COLUMN source_detail,
    DROP COLUMN source;
DROP TABLE subscriber_imports;
