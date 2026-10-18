package outreachconn

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/bluesky"
)

// fakeStore is a map-backed store that counts writes.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[string]string
	applies int
	allErr  error
}

func newFakeStore(rows map[string]string) *fakeStore {
	if rows == nil {
		rows = map[string]string{}
	}
	return &fakeStore{rows: rows}
}

func (f *fakeStore) All(context.Context) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.allErr != nil {
		return nil, f.allErr
	}
	out := map[string]string{}
	for k, v := range f.rows {
		out[k] = v
	}
	return out, nil
}

func (f *fakeStore) Apply(_ context.Context, set map[string]string, clear []string, _ time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.applies++
	for k, v := range set {
		f.rows[k] = v
	}
	for _, k := range clear {
		delete(f.rows, k)
	}
	return nil
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func newMgr(t *testing.T, st *fakeStore, env map[string]string) *Manager {
	t.Helper()
	m := NewManager(st, env, quietLog())
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	return m
}

func fieldOf(m *Manager, key string) FieldState {
	for _, g := range m.Snapshot().Groups {
		for _, f := range g.Fields {
			if f.Key == key {
				return f
			}
		}
	}
	panic("no field " + key)
}

func TestPrecedence(t *testing.T) {
	env := map[string]string{
		KeyShortLinksBaseURL: "https://env.example",
		KeyMastodonBaseURL:   "https://masto.example",
		KeyBlueskyPDSURL:     bluesky.DefaultPDSURL,
	}
	st := newFakeStore(map[string]string{KeyShortLinksBaseURL: "https://db.example"})
	m := newMgr(t, st, env)
	if f := fieldOf(m, KeyShortLinksBaseURL); f.Source != SourceDatabase || f.Value != "https://db.example" {
		t.Errorf("db must beat env: %+v", f)
	}
	if f := fieldOf(m, KeyMastodonBaseURL); f.Source != SourceEnvironment || f.Value != "https://masto.example" {
		t.Errorf("env applies without a row: %+v", f)
	}
	if f := fieldOf(m, KeyBlueskyPDSURL); f.Source != SourceDefault {
		t.Errorf("default pds must report default: %+v", f)
	}
	if f := fieldOf(m, KeyBlueskyHandle); f.Source != SourceUnset || f.Value != "" {
		t.Errorf("neither must be unset: %+v", f)
	}
}

func TestClearRevertsToEnv(t *testing.T) {
	st := newFakeStore(nil)
	m := newMgr(t, st, map[string]string{KeyMastodonBaseURL: "https://env.example"})
	ctx := context.Background()
	if _, err := m.Apply(ctx, map[string]string{KeyMastodonBaseURL: "https://db.example"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if f := fieldOf(m, KeyMastodonBaseURL); f.Source != SourceDatabase {
		t.Fatalf("after set: %+v", f)
	}
	ap, err := m.Apply(ctx, map[string]string{KeyMastodonBaseURL: "  "}, time.Now())
	if err != nil || len(ap.Cleared) != 1 {
		t.Fatalf("clear: %+v %v", ap, err)
	}
	if f := fieldOf(m, KeyMastodonBaseURL); f.Source != SourceEnvironment || f.Value != "https://env.example" {
		t.Errorf("after clear: %+v", f)
	}
}

func TestClientReuseAndListeners(t *testing.T) {
	st := newFakeStore(nil)
	env := map[string]string{
		KeyBlueskyHandle: "alice.example.test", KeyBlueskyAppPassword: "pw-fake",
	}
	m := newMgr(t, st, env)
	var calls []Changed
	m.OnChange(func(c Changed) { calls = append(calls, c) })
	before := m.Bluesky()
	if before == nil {
		t.Fatal("bluesky must be configured from env")
	}
	ctx := context.Background()
	if _, err := m.Apply(ctx, map[string]string{KeyMastodonBaseURL: "https://m.example", KeyMastodonToken: "tok-fake"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if m.Bluesky() != before {
		t.Error("an unrelated Mastodon save must keep the same Bluesky client")
	}
	if len(calls) != 1 || calls[0] != (Changed{Mastodon: true}) {
		t.Errorf("listener calls = %+v", calls)
	}
	if m.Mastodon() == nil {
		t.Error("mastodon must now be configured")
	}
	if _, err := m.Apply(ctx, map[string]string{KeyBlueskyHandle: "bob.example.test"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if m.Bluesky() == before {
		t.Error("a Bluesky handle change must swap the client")
	}
	if len(calls) != 2 || calls[1] != (Changed{Bluesky: true}) {
		t.Errorf("listener calls = %+v", calls)
	}
}

func TestNoOpApply(t *testing.T) {
	st := newFakeStore(map[string]string{KeyMastodonBaseURL: "https://m.example"})
	m := newMgr(t, st, nil)
	n := 0
	m.OnChange(func(Changed) { n++ })
	ap, err := m.Apply(context.Background(), map[string]string{
		KeyMastodonBaseURL: "https://m.example/", // same after normalising
		KeyMastodonToken:   "",                   // clear of a field with no row
	}, time.Now())
	if err != nil || len(ap.Set)+len(ap.Cleared) != 0 {
		t.Fatalf("apply = %+v, %v", ap, err)
	}
	if st.applies != 0 || n != 0 {
		t.Errorf("store applies=%d listener calls=%d, want 0 and 0", st.applies, n)
	}
}

func TestApplyValidationIsAllOrNothing(t *testing.T) {
	st := newFakeStore(nil)
	m := newMgr(t, st, nil)
	_, err := m.Apply(context.Background(), map[string]string{
		KeyMastodonToken: "tok-fake", KeyMastodonBaseURL: "http://example.com/BAD-VALUE-CANARY",
	}, time.Now())
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Key != KeyMastodonBaseURL || strings.Contains(err.Error(), "CANARY") {
		t.Fatalf("err = %v", err)
	}
	if st.applies != 0 || len(st.rows) != 0 {
		t.Errorf("nothing may be written: applies=%d rows=%v", st.applies, st.rows)
	}
}

func TestBadEnvURLLeavesGroupUnconfigured(t *testing.T) {
	m := NewManager(newFakeStore(nil), map[string]string{
		KeyMastodonBaseURL: "http://example.com", KeyMastodonToken: "tok-fake",
	}, quietLog())
	if err := m.Reload(context.Background()); err != nil {
		t.Fatalf("a bad value must not fail Reload: %v", err)
	}
	for _, g := range m.Snapshot().Groups {
		if g.Name == GroupMastodon && (g.Configured || g.Error == "") {
			t.Errorf("mastodon group = %+v", g)
		}
	}
}

func TestSnapshotNeverHoldsSecrets(t *testing.T) {
	const envCanary, dbCanary = "env-secret-CANARY", "db-secret-CANARY"
	env := map[string]string{KeyShortLinksAPIToken: envCanary, KeyBlueskyAppPassword: envCanary, KeyMastodonToken: envCanary}
	st := newFakeStore(map[string]string{KeyMastodonToken: dbCanary})
	m := newMgr(t, st, env)
	b, err := json.Marshal(m.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "CANARY") {
		t.Errorf("snapshot leaked a secret: %s", b)
	}
}

func TestReloadReturnsReadError(t *testing.T) {
	st := newFakeStore(nil)
	st.allErr = errors.New("boom")
	m := NewManager(st, nil, quietLog())
	if err := m.Reload(context.Background()); err == nil {
		t.Error("Reload must return the store error")
	}
}

func TestConcurrentReadsDuringApply(t *testing.T) {
	st := newFakeStore(nil)
	m := newMgr(t, st, nil)
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = m.Bluesky()
					_ = m.ShortLinks()
					_ = m.Snapshot()
				}
			}
		}()
	}
	for i := 0; i < 50; i++ {
		v := "pw-fake-" + string(rune('a'+i%26))
		if _, err := m.Apply(context.Background(), map[string]string{KeyBlueskyHandle: "a.example.test", KeyBlueskyAppPassword: v}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
}

// Account names the public account without any network call (#0581).
func TestAccount(t *testing.T) {
	m := newMgr(t, newFakeStore(nil), map[string]string{
		KeyBlueskyHandle: "alice.example.test", KeyMastodonBaseURL: "https://social.example.test/",
	})
	if got := m.Account("bluesky"); got != "@alice.example.test" {
		t.Errorf("bluesky = %q", got)
	}
	if got := m.Account("mastodon"); got != "social.example.test" {
		t.Errorf("mastodon = %q", got)
	}
	if got := m.Account("instagram"); got != "" {
		t.Errorf("instagram = %q", got)
	}
	m2 := newMgr(t, newFakeStore(nil), map[string]string{KeyBlueskyHandle: "@bob.example.test"})
	if got := m2.Account("bluesky"); got != "@bob.example.test" {
		t.Errorf("handle with @ = %q", got)
	}
	if got := m2.Account("mastodon"); got != "" {
		t.Errorf("unset mastodon = %q", got)
	}
}
