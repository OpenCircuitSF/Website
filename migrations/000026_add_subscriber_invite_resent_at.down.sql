ALTER TABLE subscribers DROP CONSTRAINT subscribers_invite_resent_requires_invite;
ALTER TABLE subscribers DROP COLUMN invite_resent_at;
