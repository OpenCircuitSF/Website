package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// TestRedactionSQLTracksActionConstants pins redactionSQL's inlined literals
// to the Go constants they mirror. The UPDATE spells 'inbound_mail.parked'
// and 'inbound_mail' out as literals — deliberately, so a prepared
// statement's generic plan can always match migrations/000030's partial
// index predicate (see redactionSQL's doc comment) — which means renaming
// either constant or changing its value would otherwise silently orphan the
// sweep from the rows it exists to redact. Same package, so the comparison
// is direct; no database involved.
func TestRedactionSQLTracksActionConstants(t *testing.T) {
	if !strings.Contains(redactionSQL, "action = '"+ActionInboundMailParked+"'") {
		t.Errorf("redactionSQL no longer filters on ActionInboundMailParked's current value %q — the SQL literal and the constant have drifted", ActionInboundMailParked)
	}
	if !strings.Contains(redactionSQL, "target_type = '"+TargetInboundMail+"'") {
		t.Errorf("redactionSQL no longer filters on TargetInboundMail's current value %q — the SQL literal and the constant have drifted", TargetInboundMail)
	}
}

// parkedRowSpec describes one audit_log row seedParkedRow inserts, shaped the
// way recordParked (internal/handlers/ses_inbound.go) writes them. wantFrom
// is the "from" value the #0503 sweep must leave behind after one pass over
// this row: unchanged ("" keeps the original) or the redacted placeholder
// form. An addr of "" reproduces an unparseable-message row — recordParked
// omits the "from" key entirely, so the row carries no address at all.
type parkedRowSpec struct {
	name      string
	action    string
	target    string
	reason    string
	addr      string
	ageDays   int // seed created_at = now - ageDays*24h
	wantFrom  string
	wantSweep bool // whether the sweep's returned count must include this row
}

// parkedRowFixture is the #0503 policy matrix, one row per branch of
// redactionSQL's WHERE clause, plus the two boundary rows around the
// 90-day window. Rows are seeded per-test with unique marker keys (audit_log
// is truncated once per package run, not per test, and carries no unique
// constraint to collide with), so assertions read back by marker and neither
// see leftovers from an earlier run nor disturb any other test's rows.
var parkedRowFixture = []parkedRowSpec{
	{
		name:      "past window, from_not_found — the decision's non-subscriber case, redacted",
		reason:    "from_not_found",
		addr:      "zz-redact-secret-%d@example.org",
		ageDays:   91,
		wantFrom:  "redacted@example.org",
		wantSweep: true,
	},
	{
		name:      "past window, no_identifying_signal — the decision's other non-subscriber case, redacted",
		reason:    "no_identifying_signal",
		addr:      "zz-redact-stranger-%d@example.net",
		ageDays:   91,
		wantFrom:  "redacted@example.net",
		wantSweep: true,
	},
	{
		name:      "past window, token_not_found — same unverified-address shape (the token path never checks From:), redacted",
		reason:    "token_not_found",
		addr:      "zz-redact-stale-%d@example.com",
		ageDays:   91,
		wantFrom:  "redacted@example.com",
		wantSweep: true,
	},
	{
		name:     "within window, from_not_found — kept in full until 90 days pass",
		reason:   "from_not_found",
		addr:     "zz-redact-fresh-%d@example.org",
		ageDays:  89,
		wantFrom: "KEEP",
	},
	{
		name:     "exactly at the window boundary — created_at < cutoff is strict, so a row at exactly 90 days is not yet redacted",
		reason:   "from_not_found",
		addr:     "zz-redact-boundary-%d@example.org",
		ageDays:  -1, // special-cased below: seeded at now-retention exactly
		wantFrom: "KEEP",
	},
	{
		name:     "past window, auto_reply — the subscriber's own address, disclosed and retained like the other seven email-carrying sites",
		reason:   "auto_reply",
		addr:     "zz-redact-subscriber-%d@example.org",
		ageDays:  91,
		wantFrom: "KEEP",
	},
	{
		name:      "past window, already redacted — the idempotency guard, a no-op",
		reason:    "from_not_found",
		addr:      "redacted@example.org",
		ageDays:   91,
		wantFrom:  "redacted@example.org",
		wantSweep: false,
	},
	{
		name:     "past window, no local/domain split — nothing to preserve a domain from, left alone",
		reason:   "from_not_found",
		addr:     "zz-redact-noat-%d",
		ageDays:  91,
		wantFrom: "KEEP",
	},
	{
		name:     "past window, no from key at all — an unparseable message's shape, nothing to redact",
		reason:   "unparseable",
		addr:     "",
		ageDays:  91,
		wantFrom: "KEEP",
	},
	{
		name:     "past window, different target_type — only inbound_mail rows are the sweep's business",
		action:   ActionSubscriberUnsubscribed,
		target:   TargetSubscriber,
		reason:   "",
		addr:     "zz-redact-wrongtarget-%d@example.org",
		ageDays:  91,
		wantFrom: "KEEP",
	},
	{
		name:     "past window, different action — same target_type, still not a parked row",
		action:   ActionAccountLogin,
		target:   TargetInboundMail,
		addr:     "zz-redact-wrongaction-%d@example.org",
		ageDays:  91,
		wantFrom: "KEEP",
	},
}

// seedParkedRow inserts one spec's row with an explicit created_at (the one
// column Logger.Write cannot control — insert() leaves it to the database
// default) and returns the unique marker key its metadata carries.
func seedParkedRow(t *testing.T, pool *pgxpool.Pool, now time.Time, spec parkedRowSpec) string {
	t.Helper()
	marker := fmt.Sprintf("zz-redact-marker-%d", testdb.Unique())

	createdAt := now.Add(-time.Duration(spec.ageDays) * 24 * time.Hour)
	if spec.ageDays < 0 {
		createdAt = now.Add(-inboundSenderRetention)
	}

	meta := map[string]any{
		"key":        marker,
		"message_id": fmt.Sprintf("zz-redact-msgid-%d", testdb.Unique()),
		"subject":    "zz-redact-subject",
	}
	if spec.reason != "" {
		meta["reason"] = spec.reason
	}
	if spec.addr != "" {
		// Spec addresses carry a %d verb so every run seeds distinct
		// rows; the already-redacted fixture is the one verb-less shape,
		// seeded verbatim because its whole point is a local part that
		// already equals the placeholder.
		if strings.Contains(spec.addr, "%d") {
			meta["from"] = fmt.Sprintf(spec.addr, testdb.Unique())
		} else {
			meta["from"] = spec.addr
		}
	}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	action := spec.action
	if action == "" {
		action = ActionInboundMailParked
	}
	target := spec.target
	if target == "" {
		target = TargetInboundMail
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO audit_log (action, target_type, metadata, created_at)
		 VALUES ($1, $2, $3, $4)`,
		action, target, metaJSON, createdAt,
	); err != nil {
		t.Fatalf("seed parked row %q: %v", spec.name, err)
	}
	return marker
}

// readParkedFrom returns the row's metadata->>'from' ("" when the key is
// absent) and the metadata's other parked keys as a JSON string for the
// preserved-keys assertion.
func readParkedRow(t *testing.T, pool *pgxpool.Pool, marker string) (from, subject, messageID string) {
	t.Helper()
	var fromPtr, subjectPtr, messageIDPtr *string
	if err := pool.QueryRow(context.Background(),
		`SELECT metadata->>'from', metadata->>'subject', metadata->>'message_id'
		   FROM audit_log WHERE metadata->>'key' = $1`, marker,
	).Scan(&fromPtr, &subjectPtr, &messageIDPtr); err != nil {
		t.Fatalf("read back parked row %q: %v", marker, err)
	}
	if fromPtr != nil {
		from = *fromPtr
	}
	if subjectPtr != nil {
		subject = *subjectPtr
	}
	if messageIDPtr != nil {
		messageID = *messageIDPtr
	}
	return from, subject, messageID
}

// TestRedactExpiredInboundSenders_RetentionPolicy runs the sweep once over
// the full #0503 policy matrix and asserts, per row, what survived: only
// parked rows past the 90-day window that carry a real address are redacted,
// the subscriber's own auto_reply address is retained, and every other
// metadata key — the parts that make the row actionable as a count — is
// preserved verbatim.
func TestRedactExpiredInboundSenders_RetentionPolicy(t *testing.T) {
	pool := auditTestPool(t)
	logger := New(pool)
	ctx := context.Background()

	now := time.Now().UTC()

	type seeded struct {
		spec   parkedRowSpec
		marker string
	}
	seededRows := make([]seeded, 0, len(parkedRowFixture))
	for _, spec := range parkedRowFixture {
		seededRows = append(seededRows, seeded{spec: spec, marker: seedParkedRow(t, pool, now, spec)})
	}

	wantSwept := 0
	for _, s := range seededRows {
		if s.spec.wantSweep {
			wantSwept++
		}
	}

	got, err := logger.RedactExpiredInboundSenders(ctx, now)
	if err != nil {
		t.Fatalf("RedactExpiredInboundSenders: %v", err)
	}
	if got < int64(wantSwept) {
		t.Fatalf("sweep redacted %d rows, want at least %d (this test's own matrix; the database may hold older unredacted rows from an interrupted earlier run, which the sweep must also cover)", got, wantSwept)
	}

	for _, s := range seededRows {
		spec, marker := s.spec, s.marker
		t.Run(spec.name, func(t *testing.T) {
			from, subject, messageID := readParkedRow(t, pool, marker)
			// KEEP means "exactly the seeded address, untouched": still
			// carrying the seeded unique local-part prefix and never the
			// placeholder form (and, for a row that never carried an
			// address, still absent).
			if spec.wantFrom == "KEEP" {
				switch {
				case spec.addr == "":
					if from != "" {
						t.Errorf("from = %q, want the key absent (row never carried an address)", from)
					}
				case strings.HasPrefix(from, "redacted@"):
					t.Errorf("from = %q, want the seeded address retained in full (row must not be redacted)", from)
				default:
					if !strings.Contains(from, "zz-redact-") {
						t.Errorf("from = %q, want the seeded address's local part intact", from)
					}
				}
			} else {
				if from != spec.wantFrom {
					t.Errorf("from = %q, want %q", from, spec.wantFrom)
				}
				domain := spec.wantFrom[len("redacted@"):]
				if !strings.HasSuffix(from, domain) {
					t.Errorf("from = %q, want the domain %q preserved", from, domain)
				}
				if strings.Contains(from, "zz-redact-") {
					t.Errorf("from = %q, want the original local part gone (one-way redaction)", from)
				}
			}
			// The redaction must preserve the rest of the metadata — the
			// keys that keep the row actionable as a count — and the sweep
			// touches nothing outside metadata at all.
			if subject != "zz-redact-subject" {
				t.Errorf("metadata.subject = %q, want preserved verbatim", subject)
			}
			if messageID == "" {
				t.Errorf("metadata.message_id = %q, want preserved verbatim", messageID)
			}
			if spec.reason != "" {
				var metaRaw json.RawMessage
				if err := pool.QueryRow(ctx,
					`SELECT metadata FROM audit_log WHERE metadata->>'key' = $1`, marker,
				).Scan(&metaRaw); err != nil {
					t.Fatalf("read back metadata: %v", err)
				}
				var meta map[string]any
				if err := json.Unmarshal(metaRaw, &meta); err != nil {
					t.Fatalf("unmarshal metadata: %v", err)
				}
				if meta["reason"] != spec.reason {
					t.Errorf("metadata.reason = %v, want %q preserved", meta["reason"], spec.reason)
				}
			}
		})
	}
}

// TestRedactExpiredInboundSenders_IsIdempotent proves the sweep's second
// pass over the same database is a no-op: an already-redacted row never
// matches the WHERE clause again, so the count returns to zero and the
// redacted value is byte-identical after re-running. This is what makes the
// sweep safe to ride a poll loop that runs every couple of seconds — the
// outbox worker's own cadence.
func TestRedactExpiredInboundSenders_IsIdempotent(t *testing.T) {
	pool := auditTestPool(t)
	logger := New(pool)
	ctx := context.Background()

	now := time.Now().UTC()
	marker := seedParkedRow(t, pool, now, parkedRowSpec{
		name:      "idempotency — a from_not_found row past the window",
		reason:    "from_not_found",
		addr:      "zz-redact-idem-%d@example.org",
		ageDays:   91,
		wantFrom:  "redacted@example.org",
		wantSweep: true,
	})

	first, err := logger.RedactExpiredInboundSenders(ctx, now)
	if err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	if first < 1 {
		t.Fatalf("first sweep redacted %d rows, want at least 1", first)
	}
	afterFirst, _, _ := readParkedRow(t, pool, marker)

	second, err := logger.RedactExpiredInboundSenders(ctx, now)
	if err != nil {
		t.Fatalf("second sweep: %v", err)
	}
	afterSecond, _, _ := readParkedRow(t, pool, marker)

	if afterFirst != afterSecond {
		t.Errorf("redacted from changed between sweeps: %q then %q, want byte-identical (idempotent)", afterFirst, afterSecond)
	}
	if second != 0 {
		// Both sweeps use the same `now`, so the cutoff is identical and no
		// row can cross the window between the two calls — the first pass
		// already redacted everything past it, and rows from earlier runs
		// of this package are either already redacted (excluded by the
		// idempotency guard) or were inside the window then and still are.
		// A non-zero second count means the WHERE clause matched something
		// it must not; fail, don't shrug — a silent decline here would look
		// exactly like a healthy sweep.
		t.Fatalf("second sweep redacted %d rows, want 0 — the sweep is not idempotent", second)
	}
}
