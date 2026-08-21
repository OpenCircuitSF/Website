-- This DOWN INTENTIONALLY FAILS if any address carries more than one reason.
-- The only way to narrow the key back to (email) is to discard suppressions,
-- and silently discarding a suppression is the exact failure #0100 exists to
-- prevent. Resolve the duplicates deliberately before rolling back.
ALTER TABLE suppressions DROP CONSTRAINT suppressions_pkey;
ALTER TABLE suppressions ADD CONSTRAINT suppressions_pkey PRIMARY KEY (email);
