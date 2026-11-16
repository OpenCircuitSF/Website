-- #0675: reverses 000054_create_api_idempotency_keys.up.sql. The rows are
-- 24-hour replay records, so dropping them loses nothing durable.
DROP TABLE api_idempotency_keys;
