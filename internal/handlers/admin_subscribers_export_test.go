package handlers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// exportSeedOpts configures seedExportSubscriber. A nil UTM pointer maps to
// SQL NULL (proving #0059's "empty and NULL columns must not become the
// literal <nil> or NULL" criterion); a non-nil pointer is stored verbatim,
// however exotic, so these tests can plant CSV-hostile content (commas,
// quotes, newlines, a leading =/+/-/@, UTF-8, a very long value) exactly
// where a real attacker would: the UTM triple, which is populated straight
// from the public, unauthenticated /api/subscribe query string.
type exportSeedOpts struct {
	status        string
	email         string // "" uses testSubscriberEmail(t)
	utmSource     *string
	utmMedium     *string
	utmCampaign   *string
	interestSlugs []string
	synthetic     bool
}

// strPtr is defined in admin_workshop_preview_test.go (same package, same
// signature) — reused here rather than redeclared.

// seedExportSubscriber inserts a subscribers row with caller-controlled
// UTM/interest/synthetic fields the shared seedTestSubscriber helper
// (admin_subscribers_test.go) doesn't expose, and registers per-row cleanup
// keyed by id — never a hardcoded or seeded id (CLAUDE.md §8b).
func seedExportSubscriber(t *testing.T, pool *pgxpool.Pool, opts exportSeedOpts) (id int64, email string) {
	t.Helper()
	ctx := context.Background()
	email = opts.email
	if email == "" {
		email = testSubscriberEmail(t)
	}
	token := fmt.Sprintf("zz-subtest-token-%d", testdb.Unique())
	if err := pool.QueryRow(ctx,
		`INSERT INTO subscribers (email, status, manage_token, utm_source, utm_medium, utm_campaign, synthetic, confirmed_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7,
		         CASE WHEN $2 IN ('active','unsubscribed','bounced','complained') THEN now() ELSE NULL END)
		 RETURNING id`,
		email, opts.status, token, opts.utmSource, opts.utmMedium, opts.utmCampaign, opts.synthetic,
	).Scan(&id); err != nil {
		t.Fatalf("seed export subscriber: %v", err)
	}
	t.Cleanup(func() {
		cctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(cctx, `DELETE FROM subscribers WHERE id = $1`, id)
	})
	for _, slug := range opts.interestSlugs {
		interestID := mustSeededInterestID(t, pool, slug)
		if _, err := pool.Exec(ctx,
			`INSERT INTO subscriber_interests (subscriber_id, interest_id) VALUES ($1, $2)`,
			id, interestID,
		); err != nil {
			t.Fatalf("link interest %q: %v", slug, err)
		}
	}
	return id, email
}

// exportRecordsByEmail runs GET /admin/subscribers/export?query, parses the
// CSV response with encoding/csv (the same library the handler writes with,
// so this proves round-trip correctness rather than just "some bytes came
// back"), asserts the header row, and indexes every data row by its email
// column for the caller to assert against.
func exportRecordsByEmail(t *testing.T, client *http.Client, url, token string) (map[string][]string, *http.Response) {
	t.Helper()
	resp := doJSON(t, client, "GET", url, token, "")
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body: %s", url, resp.StatusCode, body)
	}
	r := csv.NewReader(strings.NewReader(string(body)))
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parsing CSV response: %v\nbody:\n%s", err, body)
	}
	if len(records) == 0 {
		t.Fatalf("expected at least a header row, got none")
	}
	wantHeader := []string{"email", "status", "interests", "confirmed_at", "created_at", "utm_source", "utm_medium", "utm_campaign"}
	if strings.Join(records[0], ",") != strings.Join(wantHeader, ",") {
		t.Fatalf("header = %v, want %v", records[0], wantHeader)
	}
	out := make(map[string][]string, len(records)-1)
	for _, rec := range records[1:] {
		if len(rec) != len(wantHeader) {
			t.Fatalf("row %v has %d fields, want %d", rec, len(rec), len(wantHeader))
		}
		out[rec[0]] = rec
	}
	return out, resp
}

// ── Headers ───────────────────────────────────────────────────────────────────

func TestAdminSubscribers_Export_Headers(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-headers@example.com")
	seedSession(t, pool, admin, "admin-token-export-headers")

	client := srv.Client()
	resp := doJSON(t, client, "GET", srv.URL+"/admin/subscribers/export", "admin-token-export-headers", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/csv; charset=utf-8")
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, `filename="subscribers-export.csv"`) {
		t.Errorf("Content-Disposition = %q, want attachment with a fixed filename", cd)
	}
	if xcto := resp.Header.Get("X-Content-Type-Options"); xcto != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", xcto)
	}
}

// ── CSV correctness and column set ──────────────────────────────────────────

// TestAdminSubscribers_Export_CSVEdgeCases plants exactly the hostile content
// #0059 calls out — a comma, a double quote, an embedded newline, a leading
// =/+/-/@ (CSV/formula injection), a UTF-8 value, a very long value, and
// NULL UTM columns — each in its own row, and asserts encoding/csv round-
// trips every one of them correctly (or, for the injection case, that the
// guarded value round-trips to the ORIGINAL text prefixed with a single
// quote, per #0059's Notes: "Prefix such fields with a single quote").
func TestAdminSubscribers_Export_CSVEdgeCases(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-edge@example.com")
	seedSession(t, pool, admin, "admin-token-export-edge")

	longValue := strings.Repeat("x", 6000)

	_, commaEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusActive, utmSource: strPtr("spring, sale"),
	})
	_, quoteEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusActive, utmMedium: strPtr(`she said "hello"`),
	})
	_, newlineEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusActive, utmCampaign: strPtr("line one\nline two"),
	})
	_, utf8LongEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusActive, utmSource: strPtr("café ☕ 発売"), utmCampaign: strPtr(longValue),
	})
	_, nullEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusPending, // utmSource/Medium/Campaign left nil -> SQL NULL; confirmed_at NULL too
	})
	_, injectionEmail := seedExportSubscriber(t, pool, exportSeedOpts{
		status:    subscribers.StatusActive,
		utmSource: strPtr("=SUM(A1:A9)"), utmMedium: strPtr("+shady"), utmCampaign: strPtr("-1+cmd|calc"),
	})
	atInjectionEmail := "=" + testSubscriberEmail(t) // leading '=' is the injection char, not a realistic address, but the sanitizer must guard it uniformly regardless of column
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO subscribers (email, status, manage_token, confirmed_at) VALUES ($1, $2, $3, now())`,
		atInjectionEmail, subscribers.StatusActive, fmt.Sprintf("zz-subtest-token-%d", testdb.Unique()),
	); err != nil {
		t.Fatalf("seed injection-email subscriber: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = pool.Exec(ctx, `DELETE FROM subscribers WHERE email = $1`, atInjectionEmail)
	})

	client := srv.Client()
	records, _ := exportRecordsByEmail(t, client, srv.URL+"/admin/subscribers/export", "admin-token-export-edge")

	// column order: email(0), status(1), interests(2), confirmed_at(3), created_at(4), utm_source(5), utm_medium(6), utm_campaign(7)
	if rec, ok := records[commaEmail]; !ok || rec[5] != "spring, sale" {
		t.Errorf("comma field round-trip: got %v, ok=%v", rec, ok)
	}
	if rec, ok := records[quoteEmail]; !ok || rec[6] != `she said "hello"` {
		t.Errorf("quote field round-trip: got %v, ok=%v", rec, ok)
	}
	if rec, ok := records[newlineEmail]; !ok || rec[7] != "line one\nline two" {
		t.Errorf("newline field round-trip: got %v, ok=%v", rec, ok)
	}
	if rec, ok := records[utf8LongEmail]; !ok {
		t.Errorf("utf8/long row missing")
	} else {
		if rec[5] != "café ☕ 発売" {
			t.Errorf("utf8 field round-trip: got %q", rec[5])
		}
		if rec[7] != longValue {
			t.Errorf("long field round-trip: got length %d, want %d", len(rec[7]), len(longValue))
		}
	}
	if rec, ok := records[nullEmail]; !ok {
		t.Errorf("null-columns row missing")
	} else {
		for i, col := range []string{"interests", "confirmed_at", "utm_source", "utm_medium", "utm_campaign"} {
			idx := map[string]int{"interests": 2, "confirmed_at": 3, "utm_source": 5, "utm_medium": 6, "utm_campaign": 7}[col]
			if rec[idx] != "" {
				t.Errorf("NULL/empty column %s(%d) = %q, want empty string (not <nil>/NULL)", col, i, rec[idx])
			}
		}
	}
	if rec, ok := records[injectionEmail]; !ok {
		t.Errorf("injection row missing")
	} else {
		if rec[5] != "'=SUM(A1:A9)" {
			t.Errorf("utm_source injection guard: got %q, want leading-quote-prefixed original", rec[5])
		}
		if rec[6] != "'+shady" {
			t.Errorf("utm_medium injection guard: got %q", rec[6])
		}
		if rec[7] != "'-1+cmd|calc" {
			t.Errorf("utm_campaign injection guard: got %q", rec[7])
		}
	}
	if rec, ok := records["'"+atInjectionEmail]; !ok {
		t.Errorf("email injection guard: want a row keyed %q (quote-prefixed), got records: %v", "'"+atInjectionEmail, keysOf(records))
	} else if rec[0] != "'"+atInjectionEmail {
		t.Errorf("email injection guard: got %q", rec[0])
	}
}

func keysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestAdminSubscribers_Export_InterestsSemicolonJoined verifies the
// multi-value interests column is semicolon-joined, alphabetical by slug —
// exercising a subscriber with more than one selected interest.
func TestAdminSubscribers_Export_InterestsSemicolonJoined(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-interests@example.com")
	seedSession(t, pool, admin, "admin-token-export-interests")

	_, email := seedExportSubscriber(t, pool, exportSeedOpts{
		status:        subscribers.StatusActive,
		interestSlugs: []string{"soldering", "microcontrollers"},
	})

	client := srv.Client()
	records, _ := exportRecordsByEmail(t, client, srv.URL+"/admin/subscribers/export", "admin-token-export-interests")
	rec, ok := records[email]
	if !ok {
		t.Fatalf("row for %s missing", email)
	}
	if rec[2] != "microcontrollers;soldering" {
		t.Errorf("interests = %q, want %q", rec[2], "microcontrollers;soldering")
	}
}

// TestAdminSubscribers_Export_ExcludesSynthetic proves the #0046-review
// amendment to #0059: a synthetic=true test-send fixture row must never
// appear in an operator-facing export, exactly like it's already excluded
// from List/StatusCounts.
func TestAdminSubscribers_Export_ExcludesSynthetic(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-synth@example.com")
	seedSession(t, pool, admin, "admin-token-export-synth")

	_, synthEmail := seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusActive, synthetic: true})
	_, realEmail := seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusActive})

	client := srv.Client()
	records, _ := exportRecordsByEmail(t, client, srv.URL+"/admin/subscribers/export", "admin-token-export-synth")
	if _, ok := records[synthEmail]; ok {
		t.Errorf("synthetic row %s must not appear in export", synthEmail)
	}
	if _, ok := records[realEmail]; !ok {
		t.Errorf("real row %s missing from export", realEmail)
	}
}

// ── Filters ───────────────────────────────────────────────────────────────────

func TestAdminSubscribers_Export_RespectsStatusAndInterestFilters(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-filter@example.com")
	seedSession(t, pool, admin, "admin-token-export-filter")

	_, activeIoT := seedExportSubscriber(t, pool, exportSeedOpts{
		status: subscribers.StatusActive, interestSlugs: []string{"microcontrollers"},
	})
	_, pendingNoInterest := seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusPending})

	client := srv.Client()

	// status filter
	records, _ := exportRecordsByEmail(t, client, srv.URL+"/admin/subscribers/export?status=active", "admin-token-export-filter")
	if _, ok := records[activeIoT]; !ok {
		t.Errorf("status=active filter: expected %s present", activeIoT)
	}
	if _, ok := records[pendingNoInterest]; ok {
		t.Errorf("status=active filter: expected %s absent", pendingNoInterest)
	}

	// interest filter
	interestID := mustSeededInterestID(t, pool, "microcontrollers")
	records, _ = exportRecordsByEmail(t, client, fmt.Sprintf("%s/admin/subscribers/export?interest_id=%d", srv.URL, interestID), "admin-token-export-filter")
	if _, ok := records[activeIoT]; !ok {
		t.Errorf("interest_id filter: expected %s present", activeIoT)
	}
	if _, ok := records[pendingNoInterest]; ok {
		t.Errorf("interest_id filter: expected %s absent", pendingNoInterest)
	}

	// invalid status still 400s, same as List
	resp := doJSON(t, client, "GET", srv.URL+"/admin/subscribers/export?status=bogus", "admin-token-export-filter", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status=bogus: status = %d, want 400", resp.StatusCode)
	}
}

// ── Audit ─────────────────────────────────────────────────────────────────────

// TestAdminSubscribers_Export_Audited proves the export writes exactly one
// subscriber.exported audit row (#0059's Notes: "auditing the export
// matters"), attributed to the exporting admin, with a row_count matching
// what was actually streamed and the filter that was applied.
func TestAdminSubscribers_Export_Audited(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminSubscribersMux(pool, newTestSubscribeHandler(pool)))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-subs-export-audit@example.com")
	seedSession(t, pool, admin, "admin-token-export-audit")

	seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusActive})
	seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusActive})
	seedExportSubscriber(t, pool, exportSeedOpts{status: subscribers.StatusPending})

	client := srv.Client()
	records, _ := exportRecordsByEmail(t, client, srv.URL+"/admin/subscribers/export?status=active", "admin-token-export-audit")
	wantRows := len(records)
	if wantRows == 0 {
		t.Fatal("expected at least one exported row")
	}

	ctx := context.Background()
	var actorID *int64
	var action string
	var metaRaw []byte
	if err := pool.QueryRow(ctx,
		`SELECT actor_id, action, metadata FROM audit_log
		  WHERE action = 'subscriber.exported' AND actor_id = $1
		  ORDER BY id DESC LIMIT 1`,
		admin,
	).Scan(&actorID, &action, &metaRaw); err != nil {
		t.Fatalf("query audit row: %v", err)
	}
	if actorID == nil || *actorID != admin {
		t.Errorf("actor_id = %v, want %d", actorID, admin)
	}
	var meta struct {
		RowCount     int    `json:"row_count"`
		FilterStatus string `json:"filter_status"`
		FilterQuery  string `json:"filter_query"`
	}
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if meta.RowCount != wantRows {
		t.Errorf("metadata row_count = %d, want %d", meta.RowCount, wantRows)
	}
	if meta.FilterStatus != subscribers.StatusActive {
		t.Errorf("metadata filter_status = %q, want %q", meta.FilterStatus, subscribers.StatusActive)
	}
}
