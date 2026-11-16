-- #0678: reverses 000055_create_event_cadence_rules.up.sql. The rules are
-- admin-entered settings with no history elsewhere, so dropping them loses them.
DROP TABLE event_cadence_rules;
