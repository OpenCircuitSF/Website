ALTER TABLE subscriber_events DROP COLUMN import_id;
ALTER TABLE subscribers DROP CONSTRAINT subscribers_import_id_fkey;
DROP TABLE subscriber_imports;
