-- #0100: an address can have more than one independent reason to be blocked.
-- Keying on email alone made the first reason recorded the only one kept, so a
-- later admin "clear complaint" could delete a hard-bounce suppression and let
-- mail resume to a permanently-failing address. Key on (email, reason) so each
-- reason is its own removable fact. IsSuppressed stays reason-blind.
ALTER TABLE suppressions DROP CONSTRAINT suppressions_pkey;
ALTER TABLE suppressions ADD CONSTRAINT suppressions_pkey PRIMARY KEY (email, reason);
