UPDATE crt_commands SET command = 'workshops --next', updated_at = now()
 WHERE slug = 'workshops-next' AND command = 'events --next';
UPDATE crt_commands SET output = replace(output, '[x] events  [ ] digests', '[x] workshops  [ ] digests'), updated_at = now()
 WHERE slug = 'subscribe-interests' AND output LIKE '%[x] events  [ ] digests%';
