-- #0561: the full-photo "caption" layout. description and
-- description_short override the workshop summary on the flyer ('' means
-- use the default); title_position overrides the focal-point rule.
ALTER TABLE outreach_flyers
    ADD COLUMN description       TEXT NOT NULL DEFAULT '',
    ADD COLUMN description_short TEXT NOT NULL DEFAULT '',
    ADD COLUMN title_position    TEXT NOT NULL DEFAULT 'auto',
    ADD CONSTRAINT outreach_flyers_description_check CHECK (char_length(description) <= 360),
    ADD CONSTRAINT outreach_flyers_description_short_check CHECK (char_length(description_short) <= 140),
    ADD CONSTRAINT outreach_flyers_title_position_check CHECK (title_position IN ('auto', 'below', 'above'));
