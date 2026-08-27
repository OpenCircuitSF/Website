package subscribers

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// uniqueImportEmail returns an address unique to this call and registers a
// t.Cleanup that deletes any subscribers row for it. Unlike most of this
// package's fixtures, an imported row lands `active` with `confirmed_at`
// stamped to the real wall clock — TestGrowth30Days_BoundaryAndSyntheticExclusion
// (store_test.go) measures a delta over a window anchored to `time.Now()`
// captured when IT runs, and this file's tests, running earlier
// (alphabetically, in the same TestMain), left enough freshly-confirmed
// rows in the shared table to fall inside that window and shift its count —
// caught by that test failing when this file's suite ran ahead of it, not
// by anything in this file's own assertions. Deleting each row at the end
// of its own test (not batched, not left for TestMain's one-time truncate)
// is what keeps later time-window-based tests honest.
func uniqueImportEmail(t *testing.T) string {
	t.Helper()
	email := fmt.Sprintf("zz-import-%d@example.com", testdb.Unique())
	t.Cleanup(func() {
		_, _ = testDBPool.Exec(context.Background(), `DELETE FROM subscribers WHERE email = $1`, email)
	})
	return email
}

func validCommitInput(t *testing.T, rows []ImportRow) CommitInput {
	t.Helper()
	return CommitInput{
		Source:       ImportSourceManualCSV,
		SourceDetail: "test batch",
		ConsentMode:  ConsentModePriorConsent,
		ConsentNote:  "collected via a paper sign-in sheet at an event, attested by the organizer",
		CollectedAt:  time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC),
		Filename:     "attendees.csv",
		Rows:         rows,
	}
}

// TestImportStore_Commit_InsertsActiveWithProvenance is the core
// prior_consent acceptance criterion: an address lands active, with
// source=import, consent_basis=imported_prior_consent, import_id set, and
// source_detail copied from the batch.
func TestImportStore_Commit_InsertsActiveWithProvenance(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	subStore := NewStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: email}})
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.ID == 0 {
		t.Fatal("Import.ID = 0, want non-zero")
	}
	if result.Import.InsertedCount != 1 {
		t.Errorf("InsertedCount = %d, want 1", result.Import.InsertedCount)
	}
	if result.Import.SkippedCount != 0 {
		t.Errorf("SkippedCount = %d, want 0", result.Import.SkippedCount)
	}

	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if sub.Status != StatusActive {
		t.Errorf("Status = %q, want %q", sub.Status, StatusActive)
	}
	if sub.Source != SubscriberSourceImport {
		t.Errorf("Source = %q, want %q", sub.Source, SubscriberSourceImport)
	}
	if sub.ConsentBasis == nil || *sub.ConsentBasis != ConsentBasisImportedPriorConsent {
		t.Errorf("ConsentBasis = %v, want %q", sub.ConsentBasis, ConsentBasisImportedPriorConsent)
	}
	if sub.ImportID == nil || *sub.ImportID != result.Import.ID {
		t.Errorf("ImportID = %v, want %d", sub.ImportID, result.Import.ID)
	}
	if sub.SourceDetail == nil || *sub.SourceDetail != "test batch" {
		t.Errorf("SourceDetail = %v, want %q", sub.SourceDetail, "test batch")
	}
	if sub.ConfirmedAt == nil {
		t.Error("ConfirmedAt is nil, want stamped for an imported active subscriber")
	}
}

// TestImportStore_Commit_SendsNothing is the load-bearing prior_consent
// property: the outbound queue must be empty after a committed import — no
// confirmation, no welcome, nothing.
func TestImportStore_Commit_SendsNothing(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: email}})
	if _, err := store.Commit(context.Background(), in, now); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1`, email,
	).Scan(&count); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if count != 0 {
		t.Errorf("outbound_queue rows for %q = %d, want 0 (prior_consent sends nothing)", email, count)
	}
}

// TestImportStore_Commit_WritesOneSubscriberEventPerInsertedAddress proves
// the subscriber_events side of "one audit_log entry and one
// subscriber_events row per inserted address."
func TestImportStore_Commit_WritesOneSubscriberEventPerInsertedAddress(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	e1, e2 := uniqueImportEmail(t), uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: e1}, {Email: e2}})
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	for _, email := range []string{e1, e2} {
		var action string
		var importID *int64
		err := pool.QueryRow(context.Background(),
			`SELECT action, import_id FROM subscriber_events WHERE email = $1 AND action = $2`,
			email, string(ActionImported),
		).Scan(&action, &importID)
		if err != nil {
			t.Fatalf("querying subscriber_events for %q: %v", email, err)
		}
		if importID == nil || *importID != result.Import.ID {
			t.Errorf("subscriber_events.import_id for %q = %v, want %d", email, importID, result.Import.ID)
		}
	}
}

// TestImportStore_Commit_SkipsSuppressedAddress is the absolute rule: a
// suppressed address is skipped and counted, never resurrected.
func TestImportStore_Commit_SkipsSuppressedAddress(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	suppressions := NewSuppressionStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := suppressions.Add(context.Background(), NewSuppression{
		Email:  email,
		Reason: SuppressionReasonHardBounce,
		Note:   "test fixture",
	}, now); err != nil {
		t.Fatalf("seeding suppression: %v", err)
	}

	in := validCommitInput(t, []ImportRow{{Email: email}})
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.InsertedCount != 0 {
		t.Errorf("InsertedCount = %d, want 0 (suppressed)", result.Import.InsertedCount)
	}
	if result.Import.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.Import.SkippedCount)
	}

	subStore := NewStore(pool)
	if _, err := subStore.FindByEmail(context.Background(), email); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByEmail after suppressed import err = %v, want ErrNotFound (no subscribers row created)", err)
	}
}

// TestImportStore_Commit_SkipsExistingSubscriberRegardlessOfStatus proves
// an import never overwrites an existing row, whatever status it's in —
// tested against a COMPLAINED row specifically, since that is the status
// CLAUDE.md §9 says must never be resurrected by any path.
func TestImportStore_Commit_SkipsExistingSubscriberRegardlessOfStatus(t *testing.T) {
	pool := testPool(t)
	subStore := NewStore(pool)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := subStore.Create(context.Background(), NewSignup{Email: uniqueImportEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("seeding subscriber: %v", err)
	}
	if _, err := subStore.MarkComplained(context.Background(), sub.ID, now); err != nil {
		t.Fatalf("marking complained: %v", err)
	}

	in := validCommitInput(t, []ImportRow{{Email: sub.Email}})
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.InsertedCount != 0 || result.Import.SkippedCount != 1 {
		t.Errorf("InsertedCount=%d SkippedCount=%d, want 0/1", result.Import.InsertedCount, result.Import.SkippedCount)
	}

	after, err := subStore.FindByEmail(context.Background(), sub.Email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if after.Status != StatusComplained {
		t.Errorf("Status after skipped import = %q, want still %q (never resurrected)", after.Status, StatusComplained)
	}
	if after.Source == SubscriberSourceImport {
		t.Error("Source was overwritten by a skipped import; an import must never change an existing row")
	}
}

// TestImportStore_Commit_LinksKnownInterestsAndIgnoresUnknown covers the
// interest-slug column: a known slug links subscriber_interests, an unknown
// one is silently not linked (Preview already reports it).
func TestImportStore_Commit_LinksKnownInterestsAndIgnoresUnknown(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)
	interestID := seededInterestID(t, pool, "soldering")

	in := validCommitInput(t, []ImportRow{{Email: email, InterestSlugs: []string{"soldering", "not-a-real-slug"}}})
	in.InterestSlugToID = map[string]int64{"soldering": interestID}
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.InsertedCount != 1 {
		t.Fatalf("InsertedCount = %d, want 1", result.Import.InsertedCount)
	}

	subStore := NewStore(pool)
	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	ids, err := subStore.InterestIDs(context.Background(), sub.ID)
	if err != nil {
		t.Fatalf("InterestIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != interestID {
		t.Errorf("InterestIDs = %v, want [%d]", ids, interestID)
	}
}

// TestImportStore_Commit_RefusesInviteMode proves #0125 does not silently
// downgrade or half-implement invite mode.
func TestImportStore_Commit_RefusesInviteMode(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	in.ConsentMode = ConsentModeInvite
	_, err := store.Commit(context.Background(), in, now)
	if !errors.Is(err, ErrConsentModeNotSupported) {
		t.Errorf("Commit with invite mode err = %v, want ErrConsentModeNotSupported", err)
	}
}

// TestImportStore_Commit_RequiresConsentNoteAndCollectedAt proves an import
// cannot be committed without source, collected_at, and a non-empty
// consent_note.
func TestImportStore_Commit_RequiresConsentNoteAndCollectedAt(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	noNote := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	noNote.ConsentNote = "   "
	if _, err := store.Commit(context.Background(), noNote, now); !errors.Is(err, ErrConsentNoteRequired) {
		t.Errorf("Commit with blank consent_note err = %v, want ErrConsentNoteRequired", err)
	}

	noDate := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	noDate.CollectedAt = time.Time{}
	if _, err := store.Commit(context.Background(), noDate, now); !errors.Is(err, ErrCollectedAtRequired) {
		t.Errorf("Commit with zero collected_at err = %v, want ErrCollectedAtRequired", err)
	}

	badSource := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	badSource.Source = "not-a-real-source"
	if _, err := store.Commit(context.Background(), badSource, now); !errors.Is(err, ErrInvalidImportSource) {
		t.Errorf("Commit with invalid source err = %v, want ErrInvalidImportSource", err)
	}
}

// TestImportStore_Commit_TransactionalOnFailure proves a failure mid-file
// commits nothing — including the subscriber_imports row itself. Forced by
// duplicating the same email twice with an interest slug id that does not
// exist as a real interests row, which fails the subscriber_interests FK
// mid-loop.
func TestImportStore_Commit_TransactionalOnFailure(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	const bogusInterestID = int64(999999999)
	in := validCommitInput(t, []ImportRow{{Email: email, InterestSlugs: []string{"ghost"}}})
	in.InterestSlugToID = map[string]int64{"ghost": bogusInterestID}

	// Before/after DELTA, not an absolute count: this package's test suite
	// shares one database (main_test.go truncates subscribers/suppressions/
	// subscriber_interests once, not subscriber_imports), and other tests in
	// this file reuse the identical validCommitInput consent_note text, so
	// an absolute "rows with this note" count would pick up their
	// successful commits too.
	var before int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM subscriber_imports`).Scan(&before); err != nil {
		t.Fatalf("counting subscriber_imports before: %v", err)
	}

	if _, err := store.Commit(context.Background(), in, now); err == nil {
		t.Fatal("Commit with a dangling interest FK succeeded, want an error")
	}

	subStore := NewStore(pool)
	if _, err := subStore.FindByEmail(context.Background(), email); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByEmail after failed commit err = %v, want ErrNotFound (nothing committed)", err)
	}
	var after int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM subscriber_imports`).Scan(&after); err != nil {
		t.Fatalf("counting subscriber_imports after: %v", err)
	}
	if after != before {
		t.Errorf("subscriber_imports row count changed from %d to %d after a failed commit, want unchanged", before, after)
	}
}

// TestImportStore_Preview_ClassifiesWithoutWriting proves Preview is
// read-only and classifies correctly, and that a within-file duplicate
// email is not double-counted.
func TestImportStore_Preview_ClassifiesWithoutWriting(t *testing.T) {
	pool := testPool(t)
	subStore := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	existing, err := subStore.Create(context.Background(), NewSignup{Email: uniqueImportEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("seeding existing subscriber: %v", err)
	}
	suppressed := uniqueImportEmail(t)
	if _, err := suppressions.Add(context.Background(), NewSuppression{Email: suppressed, Reason: SuppressionReasonManual, Note: "x"}, now); err != nil {
		t.Fatalf("seeding suppression: %v", err)
	}
	fresh := uniqueImportEmail(t)

	rows := []ImportRow{
		{Email: existing.Email},
		{Email: suppressed},
		{Email: fresh},
		{Email: fresh}, // within-file repeat — must fold into the same bucket, not double-count
		{Email: "not-an-email", InterestSlugs: []string{"bogus-slug"}},
	}
	// "not-an-email" is deliberately included to prove Preview does not
	// itself reject malformed rows — that is parseImportUpload's job in
	// the handler layer (this package imports nothing internal and cannot
	// call the handlers-package email validator). It is treated as just
	// another (odd-looking but syntactically a string) candidate here.

	result, err := store.Preview(context.Background(), rows, map[string]bool{"soldering": true})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if result.DuplicateCount != 1 {
		t.Errorf("DuplicateCount = %d, want 1", result.DuplicateCount)
	}
	if result.SuppressedCount != 1 {
		t.Errorf("SuppressedCount = %d, want 1", result.SuppressedCount)
	}
	// fresh (deduped) + "not-an-email" = 2 new candidates.
	if result.NewCount != 2 {
		t.Errorf("NewCount = %d, want 2", result.NewCount)
	}
	if len(result.UnknownInterestSlugs) != 1 || result.UnknownInterestSlugs[0] != "bogus-slug" {
		t.Errorf("UnknownInterestSlugs = %v, want [bogus-slug]", result.UnknownInterestSlugs)
	}

	// Nothing was written.
	if _, err := subStore.FindByEmail(context.Background(), fresh); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByEmail(%q) after Preview err = %v, want ErrNotFound (Preview writes nothing)", fresh, err)
	}
}

// TestImportStore_Revoke_UnsubscribesActiveOnly proves revocation moves
// every ACTIVE subscriber for the batch to unsubscribed with
// unsubscribe_source=admin, regardless of engagement, and marks the import
// row revoked with a reason.
func TestImportStore_Revoke_UnsubscribesActiveOnly(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	subStore := NewStore(pool)
	e1, e2 := uniqueImportEmail(t), uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: e1}, {Email: e2}})
	committed, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Move e2 out of active (unsubscribed already, e.g. via a later action)
	// before revoking, to prove Revoke only touches rows still active.
	sub2, err := subStore.FindByEmail(context.Background(), e2)
	if err != nil {
		t.Fatalf("FindByEmail(e2): %v", err)
	}
	if _, err := subStore.Unsubscribe(context.Background(), sub2.ID, SourcePreferences, now); err != nil {
		t.Fatalf("Unsubscribe(e2): %v", err)
	}

	revoked, revokedEmails, alreadyRevoked, err := store.Revoke(context.Background(), committed.Import.ID, "consent turned out to be improperly obtained", now)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if alreadyRevoked {
		t.Fatal("alreadyRevoked = true on first revoke, want false")
	}
	if revoked.Status != ImportStatusRevoked {
		t.Errorf("Import.Status = %q, want %q", revoked.Status, ImportStatusRevoked)
	}
	if revoked.RevokedReason == nil || *revoked.RevokedReason != "consent turned out to be improperly obtained" {
		t.Errorf("RevokedReason = %v, want the given reason", revoked.RevokedReason)
	}
	if len(revokedEmails) != 1 || revokedEmails[0] != e1 {
		t.Errorf("revokedEmails = %v, want [%s] (only the still-active one)", revokedEmails, e1)
	}

	sub1, err := subStore.FindByEmail(context.Background(), e1)
	if err != nil {
		t.Fatalf("FindByEmail(e1): %v", err)
	}
	if sub1.Status != StatusUnsubscribed {
		t.Errorf("e1 Status = %q, want %q", sub1.Status, StatusUnsubscribed)
	}
	if sub1.UnsubscribeSource == nil || *sub1.UnsubscribeSource != SourceAdmin {
		t.Errorf("e1 UnsubscribeSource = %v, want %q", sub1.UnsubscribeSource, SourceAdmin)
	}

	sub2After, err := subStore.FindByEmail(context.Background(), e2)
	if err != nil {
		t.Fatalf("FindByEmail(e2 after revoke): %v", err)
	}
	if sub2After.UnsubscribeSource == nil || *sub2After.UnsubscribeSource != SourcePreferences {
		t.Errorf("e2 UnsubscribeSource changed by revoke = %v, want unchanged %q", sub2After.UnsubscribeSource, SourcePreferences)
	}

	// One ActionImportRevoked event, for e1 only.
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1 AND action = $2`,
		e1, string(ActionImportRevoked),
	).Scan(&count); err != nil {
		t.Fatalf("counting revoke events: %v", err)
	}
	if count != 1 {
		t.Errorf("ActionImportRevoked events for e1 = %d, want 1", count)
	}
}

// TestImportStore_Revoke_AlreadyRevokedIsNoOp proves revoking an
// already-revoked import is a no-op, not an error.
func TestImportStore_Revoke_AlreadyRevokedIsNoOp(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	committed, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if _, _, _, err := store.Revoke(context.Background(), committed.Import.ID, "first reason", now); err != nil {
		t.Fatalf("first Revoke: %v", err)
	}

	imp, revokedEmails, alreadyRevoked, err := store.Revoke(context.Background(), committed.Import.ID, "second reason", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if !alreadyRevoked {
		t.Error("alreadyRevoked = false on second revoke, want true")
	}
	if len(revokedEmails) != 0 {
		t.Errorf("revokedEmails on no-op revoke = %v, want empty", revokedEmails)
	}
	if imp.RevokedReason == nil || *imp.RevokedReason != "first reason" {
		t.Errorf("RevokedReason after no-op second revoke = %v, want unchanged %q", imp.RevokedReason, "first reason")
	}
}

// TestImportStore_Revoke_UnknownID proves Revoke reports ErrImportNotFound
// for an id that does not exist.
func TestImportStore_Revoke_UnknownID(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	_, _, _, err := store.Revoke(context.Background(), 0, "reason", time.Now())
	if !errors.Is(err, ErrImportNotFound) {
		t.Errorf("Revoke(0, ...) err = %v, want ErrImportNotFound", err)
	}
}

// TestImportStore_Revoke_RequiresReason proves the reason field is
// mandatory, matching consent_note's own convention.
func TestImportStore_Revoke_RequiresReason(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	_, _, _, err := store.Revoke(context.Background(), 1, "   ", time.Now())
	if !errors.Is(err, ErrRevokeReasonRequired) {
		t.Errorf("Revoke with blank reason err = %v, want ErrRevokeReasonRequired", err)
	}
}

// TestImportStore_GetImport_RoundTrips is a basic sanity check on GetImport.
func TestImportStore_GetImport_RoundTrips(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	committed, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	got, err := store.GetImport(context.Background(), committed.Import.ID)
	if err != nil {
		t.Fatalf("GetImport: %v", err)
	}
	if got.ConsentNote != in.ConsentNote {
		t.Errorf("ConsentNote = %q, want %q", got.ConsentNote, in.ConsentNote)
	}

	if _, err := store.GetImport(context.Background(), 0); !errors.Is(err, ErrImportNotFound) {
		t.Errorf("GetImport(0) err = %v, want ErrImportNotFound", err)
	}
}

// TestImportStore_Commit_NeverProducesSyntheticRows guards against #0125's
// acceptance criterion that an import never produces migrations/000019
// synthetic test-send rows.
func TestImportStore_Commit_NeverProducesSyntheticRows(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	subStore := NewStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: email}})
	if _, err := store.Commit(context.Background(), in, now); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if sub.Synthetic {
		t.Error("Synthetic = true for an imported subscriber, want false")
	}
}
