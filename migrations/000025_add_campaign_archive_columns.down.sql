ALTER TABLE email_campaigns DROP CONSTRAINT email_campaigns_archive_status_check;
ALTER TABLE email_campaigns DROP COLUMN archived_at;
ALTER TABLE email_campaigns DROP COLUMN archive_status;
ALTER TABLE email_campaigns DROP COLUMN slug;
