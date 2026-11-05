-- #0615: the CRT screen's seeded copy says "events" now that a listing can be
-- any type. Each statement matches only the seeded value, so a row an admin
-- has edited stays as it is. The slug and source are identifiers and stay.
UPDATE crt_commands SET command = 'events --next', updated_at = now()
 WHERE slug = 'workshops-next' AND command = 'workshops --next';
UPDATE crt_commands SET output = replace(output, '[x] workshops  [ ] digests', '[x] events  [ ] digests'), updated_at = now()
 WHERE slug = 'subscribe-interests' AND output LIKE '%[x] workshops  [ ] digests%';
