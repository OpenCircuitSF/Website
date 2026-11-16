package apitokens

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

func TestStore_CreateStoresOnlyHash(t *testing.T) {
	pool := testPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	tok, secret, err := s.Create(ctx, "Home Assistant", []string{ScopeEngagementRead}, nil, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	var row string
	if err := pool.QueryRow(ctx, `SELECT row_to_json(t)::text FROM api_tokens t WHERE id=$1`, tok.ID).Scan(&row); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row, secret) || strings.Contains(row, secret[len(TokenPrefix):]) {
		t.Fatal("row contains the secret")
	}
	var hash []byte
	var prefix string
	if err := pool.QueryRow(ctx, `SELECT token_hash, display_prefix FROM api_tokens WHERE id=$1`, tok.ID).Scan(&hash, &prefix); err != nil {
		t.Fatal(err)
	}
	if string(hash) != string(Hash(secret)) {
		t.Fatal("token_hash != Hash(secret)")
	}
	if prefix != secret[:DisplayPrefixLength] || tok.Prefix != prefix {
		t.Fatalf("prefix %q, token prefix %q", prefix, tok.Prefix)
	}
}

func TestStore_ResolveValid(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tok, secret, err := s.Create(ctx, "HA", []string{ScopeEngagementRead}, nil, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Resolve(ctx, secret, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != tok.ID || got.Name != "HA" || len(got.Scopes) != 1 || got.Scopes[0] != ScopeEngagementRead {
		t.Fatalf("resolved %+v", got)
	}
}

func TestStore_ResolveUnknownAndMalformed(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	unknown, _ := Generate()
	for _, in := range []string{unknown, "not-a-token", ""} {
		if _, err := s.Resolve(ctx, in, t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q: got %v want ErrNotFound", in, err)
		}
	}
}

func TestStore_RevokeStopsResolve(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tok, secret, _ := s.Create(ctx, "HA", []string{ScopeEngagementRead}, nil, nil, t0)
	r, err := s.Revoke(ctx, tok.ID, t0)
	if err != nil || r.RevokedAt == nil {
		t.Fatalf("revoke: %v %+v", err, r)
	}
	if _, err := s.Resolve(ctx, secret, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resolve after revoke: %v", err)
	}
}

func TestStore_RevokeTwiceAndUnknown(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tok, _, _ := s.Create(ctx, "HA", []string{ScopeEngagementRead}, nil, nil, t0)
	if _, err := s.Revoke(ctx, tok.ID, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, tok.ID, t0); !errors.Is(err, ErrAlreadyRevoked) {
		t.Fatalf("second revoke: %v", err)
	}
	if _, err := s.Revoke(ctx, tok.ID+1000000, t0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown revoke: %v", err)
	}
}

func TestStore_LastUsedThrottle(t *testing.T) {
	pool := testPool(t)
	s := NewStore(pool)
	ctx := context.Background()
	tok, secret, _ := s.Create(ctx, "HA", []string{ScopeEngagementRead}, nil, nil, t0)
	read := func() time.Time {
		var v time.Time
		if err := pool.QueryRow(ctx, `SELECT last_used_at FROM api_tokens WHERE id=$1`, tok.ID).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, step := range []struct {
		at, want time.Time
	}{
		{t0, t0},
		{t0.Add(30 * time.Second), t0},
		{t0.Add(61 * time.Second), t0.Add(61 * time.Second)},
	} {
		if _, err := s.Resolve(ctx, secret, step.at); err != nil {
			t.Fatal(err)
		}
		if got := read(); !got.Equal(step.want) {
			t.Fatalf("resolve at %v: last_used_at %v want %v", step.at, got, step.want)
		}
	}
}

func TestStore_ListOrder(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	a, _, _ := s.Create(ctx, "old", []string{ScopeEngagementRead}, nil, nil, t0)
	b, _, _ := s.Create(ctx, "new", []string{ScopeEngagementRead}, nil, nil, t0.Add(time.Hour))
	c, _, _ := s.Create(ctx, "revoked-newest", []string{ScopeEngagementRead}, nil, nil, t0.Add(2*time.Hour))
	if _, err := s.Revoke(ctx, c.ID, t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].ID != b.ID || list[1].ID != a.ID || list[2].ID != c.ID {
		t.Fatalf("order %+v", list)
	}
}

func TestStore_CreateRejectsUnknownScope(t *testing.T) {
	s := NewStore(testPool(t))
	if _, _, err := s.Create(context.Background(), "HA", []string{"admin:write"}, nil, nil, t0); err == nil {
		t.Fatal("insert with admin:write succeeded")
	}
}

// TestStore_ResolveExpiry (#0674): a token resolves before expires_at and is
// ErrNotFound — the same error as a revoked token — from expires_at on.
func TestStore_ResolveExpiry(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	exp := t0.Add(24 * time.Hour)
	tok, secret, err := s.Create(ctx, "agent", []string{ScopeEventsWrite}, nil, &exp, t0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresAt == nil || !tok.ExpiresAt.Equal(exp) {
		t.Fatalf("created ExpiresAt %v", tok.ExpiresAt)
	}
	got, err := s.Resolve(ctx, secret, exp.Add(-time.Second))
	if err != nil {
		t.Fatalf("before expiry: %v", err)
	}
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("resolved ExpiresAt %v", got.ExpiresAt)
	}
	for _, at := range []time.Time{exp, exp.Add(time.Hour)} {
		if _, err := s.Resolve(ctx, secret, at); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Resolve at %v: err %v, want ErrNotFound", at, err)
		}
	}
	list, err := s.List(ctx)
	if err != nil || len(list) != 1 || list[0].ExpiresAt == nil || !list[0].ExpiresAt.Equal(exp) {
		t.Fatalf("List %+v err %v", list, err)
	}
}

// TestStore_NoExpiryNeverExpires: a nil expiry resolves far in the future.
func TestStore_NoExpiryNeverExpires(t *testing.T) {
	s := NewStore(testPool(t))
	ctx := context.Background()
	tok, secret, err := s.Create(ctx, "HA", []string{ScopeEngagementRead}, nil, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if tok.ExpiresAt != nil {
		t.Fatalf("ExpiresAt %v", tok.ExpiresAt)
	}
	if _, err := s.Resolve(ctx, secret, t0.AddDate(50, 0, 0)); err != nil {
		t.Fatal(err)
	}
}

// TestStore_CreateRejectsPastExpiry: api_tokens_expires_after_created.
func TestStore_CreateRejectsPastExpiry(t *testing.T) {
	s := NewStore(testPool(t))
	past := t0.Add(-time.Minute)
	if _, _, err := s.Create(context.Background(), "HA", []string{ScopeEngagementRead}, nil, &past, t0); err == nil {
		t.Fatal("insert with an expiry before created_at succeeded")
	}
}

// TestStore_CreateAcceptsEveryKnownScope proves the live CHECK admits the
// whole Go set (internal/db's guard compares the migration text).
func TestStore_CreateAcceptsEveryKnownScope(t *testing.T) {
	s := NewStore(testPool(t))
	tok, _, err := s.Create(context.Background(), "agent", KnownScopes, nil, nil, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok.Scopes) != len(KnownScopes) {
		t.Fatalf("scopes %v", tok.Scopes)
	}
}
