-- #0675: Idempotency-Key replay for /api/v1 create operations, owned by
-- internal/api (idempotency.go). A key is scoped per API token and kept 24
-- hours; Begin sweeps expired rows lazily, so no worker is needed. Only a
-- success is stored: status_code is NULL while the first request is still
-- running, and a failed request deletes its row so a retry runs again.
-- response_body is BYTEA, not JSONB, so a replay returns the original bytes
-- exactly (JSONB would reorder keys and rewrite whitespace).
CREATE TABLE api_idempotency_keys (
    token_id      BIGINT      NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    idem_key      TEXT        NOT NULL,
    operation     TEXT        NOT NULL,
    request_hash  BYTEA       NOT NULL,
    status_code   INTEGER,
    response_body BYTEA,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (token_id, idem_key),
    CONSTRAINT api_idempotency_keys_key_check CHECK (char_length(idem_key) BETWEEN 1 AND 255),
    CONSTRAINT api_idempotency_keys_hash_check CHECK (octet_length(request_hash) = 32)
);

CREATE INDEX api_idempotency_keys_created_at_idx ON api_idempotency_keys (created_at);
