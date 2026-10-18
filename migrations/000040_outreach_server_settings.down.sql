DELETE FROM server_setting_values WHERE key IN ('bluesky_card_url', 'outreach_hashtags', 'outreach_post_hour');
ALTER TABLE server_setting_values DROP CONSTRAINT server_setting_values_key_check;
ALTER TABLE server_setting_values ADD CONSTRAINT server_setting_values_key_check CHECK (key IN (
    'media_dir', 'email_from', 'email_reply_to', 'email_list_domain',
    'ses_sandbox', 'ses_inbound_bucket', 'send_batch_size'));
