-- #0571, #0573, #0574: outreach content settings saved from admin Settings
-- through internal/serversettings: which URL the Bluesky card points at
-- ('short' or 'canonical'), the site-wide hashtag line appended to drafts,
-- and the Pacific posting hour suggested times use. Widens the key CHECK on
-- server_setting_values (000038, already applied to production), so it is a
-- new migration rather than an edit.
ALTER TABLE server_setting_values DROP CONSTRAINT server_setting_values_key_check;
ALTER TABLE server_setting_values ADD CONSTRAINT server_setting_values_key_check CHECK (key IN (
    'media_dir', 'email_from', 'email_reply_to', 'email_list_domain',
    'ses_sandbox', 'ses_inbound_bucket', 'send_batch_size',
    'bluesky_card_url', 'outreach_hashtags', 'outreach_post_hour'));
