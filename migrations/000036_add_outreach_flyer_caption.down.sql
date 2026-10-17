ALTER TABLE outreach_flyers
    DROP CONSTRAINT IF EXISTS outreach_flyers_title_position_check,
    DROP CONSTRAINT IF EXISTS outreach_flyers_description_short_check,
    DROP CONSTRAINT IF EXISTS outreach_flyers_description_check,
    DROP COLUMN IF EXISTS title_position,
    DROP COLUMN IF EXISTS description_short,
    DROP COLUMN IF EXISTS description;
