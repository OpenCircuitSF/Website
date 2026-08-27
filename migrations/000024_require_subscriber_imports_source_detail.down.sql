ALTER TABLE subscriber_imports
    DROP CONSTRAINT subscriber_imports_source_detail_check;

ALTER TABLE subscriber_imports
    ALTER COLUMN source_detail DROP NOT NULL;
