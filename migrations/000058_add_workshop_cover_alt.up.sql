-- #0572: the cover's alt text. Mastodon posts attach the cover only when this
-- is set (1500 characters is Mastodon's media description limit). NULL means
-- no alt text. ALTER TABLE because 000020, which owns workshops, is frozen.
ALTER TABLE workshops ADD COLUMN cover_alt TEXT CHECK (cover_alt IS NULL OR char_length(cover_alt) <= 1500);
