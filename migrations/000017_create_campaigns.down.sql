-- Reverse dependency order: email_sends and campaign_interests both carry
-- foreign keys into email_campaigns.
DROP TABLE email_sends;
DROP TABLE campaign_interests;
DROP TABLE email_campaigns;
