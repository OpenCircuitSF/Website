package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// Shared /api/v1 test helpers (#0676; #0677 and #0678 reuse them). For a
// store-free op test, internal/api/apitest (an in-memory resolver and
// idempotency store) is enough. These are for the Postgres-backed tests:
// real api_tokens rows, so audit rows and api_idempotency_keys' foreign key
// see a real token, and never a seeded id (AGENTS.md §8b).

// v1TestKey creates a real api_tokens row holding scopes and returns its
// secret and token. The row is deleted on cleanup.
func v1TestKey(t *testing.T, pool *pgxpool.Pool, scopes ...string) (string, apitokens.Token) {
	t.Helper()
	tok, secret, err := apitokens.NewStore(pool).Create(context.Background(), "v1test", scopes, nil, nil, time.Now())
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM api_tokens WHERE id = $1`, tok.ID)
	})
	return secret, tok
}

// v1TestRegistry is a registry with the core ops plus ops, using the
// Postgres idempotency store.
func v1TestRegistry(pool *pgxpool.Pool, ops ...api.Operation) *api.Registry {
	reg := api.NewRegistry(api.Config{Version: "test", Idempotency: api.NewIdempotencyStore(pool)})
	reg.Add(api.CoreOperations("test")...)
	reg.Add(ops...)
	return reg
}

// v1TestRouter serves ops as main.go does: the real token store resolves
// keys, a fresh per-token limiter, Postgres idempotency.
func v1TestRouter(t *testing.T, pool *pgxpool.Pool, ops ...api.Operation) http.Handler {
	t.Helper()
	return api.NewHTTPRouter(v1TestRegistry(pool, ops...), api.RouterDeps{
		Resolver: apitokens.NewStore(pool), Limiter: middleware.NewAPITokenRateLimiter(), BaseURL: "http://example.test",
	})
}

// v1Decode decodes a success body into T, failing the test on bad JSON.
func v1Decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %T: %v (%s)", v, err, rec.Body.Bytes())
	}
	return v
}

// v1AuditRow is one audit_log row written by an API token.
type v1AuditRow struct {
	ActorID  *int64
	TargetID *int64
	Metadata map[string]any
}

// v1AuditRows returns the audit rows for action attributed to token id
// (metadata actor "token:<id>"), oldest first.
func v1AuditRows(t *testing.T, pool *pgxpool.Pool, action string, tokenID int64) []v1AuditRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT actor_id, target_id, metadata FROM audit_log
		  WHERE action = $1 AND metadata->>'actor' = $2 ORDER BY id`, action, "token:"+strconv.FormatInt(tokenID, 10))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []v1AuditRow
	for rows.Next() {
		var r v1AuditRow
		var raw []byte
		if err := rows.Scan(&r.ActorID, &r.TargetID, &raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &r.Metadata); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
