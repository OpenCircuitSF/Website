-- #0648: the Luma calendar Outreach compares events with, saved from admin
-- Settings through internal/serversettings ('cal-…', or 'off'). Widens the key
-- CHECK on server_setting_values (000042, already applied to production), so
-- it is a new migration rather than an edit.
ALTER TABLE server_setting_values DROP CONSTRAINT server_setting_values_key_check;
ALTER TABLE server_setting_values ADD CONSTRAINT server_setting_values_key_check CHECK (key IN (
    'media_dir', 'email_from', 'email_reply_to', 'email_list_domain',
    'ses_sandbox', 'ses_inbound_bucket', 'send_batch_size',
    'bluesky_card_url', 'outreach_hashtags', 'outreach_post_hour',
    'outreach_channels', 'luma_calendar_id'));
