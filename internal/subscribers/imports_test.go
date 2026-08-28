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
	// #0292: confirmed_at is left NULL for a prior_consent import — PRD
	// §6.10 says outright that these subscribers "did not confirm here",
	// so stamping a local confirmation timestamp would assert something
	// that never happened. consent_basis=imported_prior_consent (asserted
	// above) is what records why the row is active without one.
	if sub.ConfirmedAt != nil {
		t.Errorf("ConfirmedAt = %v, want nil for an imported active subscriber (#0292)", sub.ConfirmedAt)
	}
}

// TestImportStore_Commit_DoesNotInflateGrowth30Days is #0292's direct proof
// that Growth30Days no longer counts an import as a confirmation — for the
// RIGHT reason (this is the #0266 class the issue names explicitly): the
// row this test commits is never deleted before Growth30Days is called
// (unlike uniqueImportEmail's t.Cleanup, which runs at the end of the
// test), so a passing assertion here cannot be explained by cleanup timing
// or fixture ordering the way TestGrowth30Days_BoundaryAndSyntheticExclusion's
// own doc comment describes historically happening. If Commit still
// stamped confirmed_at = now (the pre-#0292 behavior), this test would fail
// on the very same run that creates the row — there is no window in which
// it could pass by accident.
//
// #0305 extends the same proof one step further: the import must not
// merely fail to inflate "confirmed" — it must show up as "imported"
// instead, or #0292's fix would have made the dashboard understate growth
// rather than overstate it (the defect #0305 fixes). Both are asserted
// against the SAME before/after pair, from the SAME Commit call.
func TestImportStore_Commit_DoesNotInflateGrowth30Days(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	email := uniqueImportEmail(t)
	since := time.Now().UTC()

	confirmedBefore, importedBefore, _, err := subStore.Growth30Days(context.Background(), since)
	if err != nil {
		t.Fatalf("Growth30Days (before): %v", err)
	}

	in := validCommitInput(t, []ImportRow{{Email: email}})
	if _, err := importStore.Commit(context.Background(), in, since.Add(time.Second)); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	confirmedAfter, importedAfter, _, err := subStore.Growth30Days(context.Background(), since)
	if err != nil {
		t.Fatalf("Growth30Days (after): %v", err)
	}
	if confirmedAfter != confirmedBefore {
		t.Errorf("confirmed_30d = %d after a prior_consent import, want unchanged from %d — "+
			"an import must not read as a confirmation on the dashboard (#0292)", confirmedAfter, confirmedBefore)
	}
	if importedAfter != importedBefore+1 {
		t.Errorf("imported_30d = %d after a prior_consent import, want %d (before=%d + 1) — "+
			"an import must still read as growth on the dashboard, just not as a confirmation (#0305)",
			importedAfter, importedBefore+1, importedBefore)
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

// TestImportStore_Commit_InviteInsertsPendingAndEnqueuesInvitation is
// #0129's core acceptance criterion: an invite-mode commit lands the new
// address `pending` with a confirm token, consent_basis left NULL (asked,
// not yet consented), invited_at stamped, and exactly one
// outbox.KindImportInvite row enqueued in the SAME transaction.
func TestImportStore_Commit_InviteInsertsPendingAndEnqueuesInvitation(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	subStore := NewStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: email}})
	in.ConsentMode = ConsentModeInvite
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.InsertedCount != 1 {
		t.Errorf("InsertedCount = %d, want 1", result.Import.InsertedCount)
	}
	if result.Import.InvitedCount != 1 {
		t.Errorf("InvitedCount = %d, want 1", result.Import.InvitedCount)
	}

	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if sub.Status != StatusPending {
		t.Errorf("Status = %q, want %q", sub.Status, StatusPending)
	}
	if sub.ConfirmToken == nil || *sub.ConfirmToken == "" {
		t.Error("ConfirmToken is nil/empty, want a live confirm token")
	}
	if sub.ConfirmExpiresAt == nil || !sub.ConfirmExpiresAt.After(now) {
		t.Errorf("ConfirmExpiresAt = %v, want a time after %v", sub.ConfirmExpiresAt, now)
	}
	if sub.ConsentBasis != nil {
		t.Errorf("ConsentBasis = %v, want nil — an invited address has been asked, not yet consented", sub.ConsentBasis)
	}
	if sub.InvitedAt == nil {
		t.Error("InvitedAt is nil, want stamped")
	}
	if sub.ImportID == nil || *sub.ImportID != result.Import.ID {
		t.Errorf("ImportID = %v, want %d", sub.ImportID, result.Import.ID)
	}
	if sub.ConfirmedAt != nil {
		t.Errorf("ConfirmedAt = %v, want nil — nobody has confirmed yet", sub.ConfirmedAt)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1 AND kind = $2`,
		email, "import_invite",
	).Scan(&queued); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queued != 1 {
		t.Errorf("outbound_queue rows for %s = %d, want 1", email, queued)
	}
}

// TestImportStore_Commit_InviteSkipsAlreadyInvitedAddress proves the
// anti-abuse property PRD §6.10.1 requires: an address already present in
// subscribers (because it was invited by an EARLIER import) is skipped —
// never invited a second time, in any mode — since dedupe checks
// subscribers regardless of status, and invited_at is stamped exactly once
// at insert and never cleared.
func TestImportStore_Commit_InviteSkipsAlreadyInvitedAddress(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	first := validCommitInput(t, []ImportRow{{Email: email}})
	first.ConsentMode = ConsentModeInvite
	firstResult, err := store.Commit(context.Background(), first, now)
	if err != nil {
		t.Fatalf("first Commit: %v", err)
	}
	if firstResult.Import.InvitedCount != 1 {
		t.Fatalf("first InvitedCount = %d, want 1", firstResult.Import.InvitedCount)
	}

	second := validCommitInput(t, []ImportRow{{Email: email}})
	second.ConsentMode = ConsentModeInvite
	secondResult, err := store.Commit(context.Background(), second, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if secondResult.Import.InsertedCount != 0 || secondResult.Import.InvitedCount != 0 {
		t.Errorf("second import InsertedCount=%d InvitedCount=%d, want 0/0 (already invited)",
			secondResult.Import.InsertedCount, secondResult.Import.InvitedCount)
	}
	if secondResult.Import.SkippedCount != 1 {
		t.Errorf("second import SkippedCount = %d, want 1", secondResult.Import.SkippedCount)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1 AND kind = $2`,
		email, "import_invite",
	).Scan(&queued); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queued != 1 {
		t.Errorf("outbound_queue rows for %s after two imports = %d, want 1 (never re-invited)", email, queued)
	}
}

// TestImportStore_Commit_InviteSkipsSuppressedAddress mirrors
// TestImportStore_Commit_SkipsSuppressedAddress for invite mode — a
// suppressed address must never be invited, matching PRD §6.10.1's "skipped
// as always — they are never invited".
func TestImportStore_Commit_InviteSkipsSuppressedAddress(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	suppressions := NewSuppressionStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := suppressions.Add(context.Background(), NewSuppression{
		Email: email, Reason: SuppressionReasonHardBounce,
	}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	in := validCommitInput(t, []ImportRow{{Email: email}})
	in.ConsentMode = ConsentModeInvite
	result, err := store.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.Import.InvitedCount != 0 || result.Import.InsertedCount != 0 {
		t.Errorf("InsertedCount=%d InvitedCount=%d, want 0/0 (suppressed)", result.Import.InsertedCount, result.Import.InvitedCount)
	}
	if result.Import.SkippedCount != 1 {
		t.Errorf("SkippedCount = %d, want 1", result.Import.SkippedCount)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1 AND kind = $2`,
		email, "import_invite",
	).Scan(&queued); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queued != 0 {
		t.Errorf("outbound_queue rows for suppressed %s = %d, want 0", email, queued)
	}
}

// TestImportStore_Commit_RequiresConsentNoteAndCollectedAt proves an import
// cannot be committed without source, source_detail, collected_at, and a
// non-empty consent_note.
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

// TestImportStore_Commit_RequiresSourceDetail is #0291's acceptance
// criterion: PRD §6.10 names source_detail as one of the four fields a
// subscriber_imports row requires (the invitation copy #0129 will send is
// assembled from it), and blank or whitespace-only must both refuse the
// same as an empty consent_note does.
func TestImportStore_Commit_RequiresSourceDetail(t *testing.T) {
	pool := testPool(t)
	store := NewImportStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	empty := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	empty.SourceDetail = ""
	if _, err := store.Commit(context.Background(), empty, now); !errors.Is(err, ErrSourceDetailRequired) {
		t.Errorf("Commit with empty source_detail err = %v, want ErrSourceDetailRequired", err)
	}

	whitespace := validCommitInput(t, []ImportRow{{Email: uniqueImportEmail(t)}})
	whitespace.SourceDetail = "   "
	if _, err := store.Commit(context.Background(), whitespace, now); !errors.Is(err, ErrSourceDetailRequired) {
		t.Errorf("Commit with whitespace-only source_detail err = %v, want ErrSourceDetailRequired", err)
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

// commitInvite is a small helper for #0129's tests below: commits a single
// invite-mode row and returns the resulting subscriber and import.
func commitInvite(t *testing.T, importStore *ImportStore, email string, now time.Time) (Subscriber, Import) {
	t.Helper()
	in := validCommitInput(t, []ImportRow{{Email: email}})
	in.ConsentMode = ConsentModeInvite
	result, err := importStore.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("commitInvite: Commit: %v", err)
	}
	subStore := NewStore(importStore.pool)
	sub, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("commitInvite: FindByEmail: %v", err)
	}
	return sub, result.Import
}

// TestConfirm_InviteAcceptance_SetsConsentBasisSkipsWelcomeAndCounts is
// #0129's core confirmation-side acceptance criterion (PRD §6.10.1 step 4):
// following the invitation's confirm link — the SAME Confirm method and
// SAME token shape the public double opt-in flow uses — activates the
// subscriber, sets consent_basis=double_opt_in, records invite_accepted (in
// addition to confirmed), increments the owning import's confirmed_count,
// and sends NO welcome email ("the invitation was the introduction").
func TestConfirm_InviteAcceptance_SetsConsentBasisSkipsWelcomeAndCounts(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	invited, imp := commitInvite(t, importStore, email, now)
	if invited.ConfirmToken == nil {
		t.Fatal("invited subscriber has no confirm token")
	}

	confirmed, err := subStore.Confirm(context.Background(), *invited.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if confirmed.Status != StatusActive {
		t.Errorf("Status = %q, want %q", confirmed.Status, StatusActive)
	}
	if confirmed.ConsentBasis == nil || *confirmed.ConsentBasis != ConsentBasisDoubleOptIn {
		t.Errorf("ConsentBasis = %v, want %q", confirmed.ConsentBasis, ConsentBasisDoubleOptIn)
	}
	if confirmed.ConfirmedAt == nil {
		t.Error("ConfirmedAt is nil, want set")
	}
	// Indistinguishable from a website signup except by source/import_id
	// (#0129's acceptance criteria): source and import_id are still the
	// import's, but every OTHER field behaves exactly as a website
	// confirmation's would — asserted above (status, consent_basis,
	// confirmed_at) using the SAME assertions TestConfirm_Success uses for
	// a website signup.
	if confirmed.Source != SubscriberSourceImport {
		t.Errorf("Source = %q, want %q (unchanged by confirming)", confirmed.Source, SubscriberSourceImport)
	}

	// No welcome email — "the invitation was the introduction".
	var welcomeCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE recipient = $1 AND kind = 'welcome'`, email,
	).Scan(&welcomeCount); err != nil {
		t.Fatalf("counting welcome rows: %v", err)
	}
	if welcomeCount != 0 {
		t.Errorf("welcome rows for %s = %d, want 0 (no welcome follows an accepted invitation)", email, welcomeCount)
	}

	// invite_accepted event recorded, in addition to confirmed.
	var inviteAcceptedCount, confirmedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1 AND action = $2`,
		email, string(ActionInviteAccepted),
	).Scan(&inviteAcceptedCount); err != nil {
		t.Fatalf("counting invite_accepted events: %v", err)
	}
	if inviteAcceptedCount != 1 {
		t.Errorf("invite_accepted events = %d, want 1", inviteAcceptedCount)
	}
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1 AND action = $2`,
		email, string(ActionConfirmed),
	).Scan(&confirmedCount); err != nil {
		t.Fatalf("counting confirmed events: %v", err)
	}
	if confirmedCount != 1 {
		t.Errorf("confirmed events = %d, want 1", confirmedCount)
	}

	// subscriber_imports.confirmed_count incremented.
	after, err := importStore.GetImport(context.Background(), imp.ID)
	if err != nil {
		t.Fatalf("GetImport: %v", err)
	}
	if after.ConfirmedCount != 1 {
		t.Errorf("ConfirmedCount = %d, want 1", after.ConfirmedCount)
	}
}

// TestConfirm_InviteAcceptance_NeverCountsAsConsentingUntilConfirmed proves
// the load-bearing property this whole issue exists for: BEFORE the
// invitation is accepted, the address is pending with consent_basis still
// NULL — it does not count as consenting by any of the signals the rest of
// the system reads (status, consent_basis).
func TestConfirm_InviteAcceptance_NeverCountsAsConsentingUntilConfirmed(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, email, now)
	if invited.Status != StatusPending {
		t.Errorf("Status = %q, want %q before confirmation", invited.Status, StatusPending)
	}
	if invited.ConsentBasis != nil {
		t.Errorf("ConsentBasis = %v, want nil before confirmation — invited, not consented", invited.ConsentBasis)
	}
	if invited.ConfirmedAt != nil {
		t.Errorf("ConfirmedAt = %v, want nil before confirmation", invited.ConfirmedAt)
	}
}

// TestUnsubscribe_InviteDecline_SuppressesAddressAndClearsToken is #0129's
// core decline-side acceptance criterion (PRD §6.10.1: "carries... a
// one-click decline that suppresses the address outright"): unsubscribing a
// still-pending, unconfirmed import invitation — reached through the SAME
// Store.Unsubscribe every other unsubscribe path already uses — suppresses
// the address (so no future import can resurrect it) and clears the confirm
// token (so a still-live invitation link cannot later reactivate the row).
func TestUnsubscribe_InviteDecline_SuppressesAddressAndClearsToken(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	email := uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, email, now)

	declined, err := subStore.Unsubscribe(context.Background(), invited.ID, SourceOneClick, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if declined.Status != StatusUnsubscribed {
		t.Errorf("Status = %q, want %q", declined.Status, StatusUnsubscribed)
	}
	if declined.ConfirmToken != nil {
		t.Errorf("ConfirmToken = %v, want nil (cleared on decline)", *declined.ConfirmToken)
	}
	if declined.ConfirmExpiresAt != nil {
		t.Errorf("ConfirmExpiresAt = %v, want nil (cleared on decline)", declined.ConfirmExpiresAt)
	}

	sups, err := suppressions.ListByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(sups) != 1 || sups[0].Reason != SuppressionReasonManual {
		t.Errorf("suppressions for %s = %+v, want exactly one manual-reason row", email, sups)
	}

	// The stale confirm token — if a caller still held it — must no longer
	// resolve to anything, so a late click on the (declined) invitation
	// link can never reactivate this row.
	if _, err := subStore.FindByConfirmToken(context.Background(), *invited.ConfirmToken); !errors.Is(err, ErrNotFound) {
		t.Errorf("FindByConfirmToken after decline err = %v, want ErrNotFound", err)
	}
}

// TestUnsubscribe_OrdinaryUnsubscribeStillNeverSuppresses proves #0129's
// decline branch is scoped tightly: an ORDINARY active subscriber's
// unsubscribe (the vast majority of Unsubscribe calls) must still NOT add a
// suppression — internal/handlers/unsubscribe.go's own doc comment commits
// to this ("an unsubscribed... address must still be able to resubscribe
// through ordinary double opt-in"), and #0129 must not silently break it.
func TestUnsubscribe_OrdinaryUnsubscribeStillNeverSuppresses(t *testing.T) {
	pool := testPool(t)
	subStore := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	sub, err := subStore.Create(context.Background(), NewSignup{Email: uniqueImportEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	confirmed, err := subStore.Confirm(context.Background(), *sub.ConfirmToken, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := subStore.Unsubscribe(context.Background(), confirmed.ID, SourceOneClick, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}

	sups, err := suppressions.ListByEmail(context.Background(), sub.Email)
	if err != nil {
		t.Fatalf("ListByEmail: %v", err)
	}
	if len(sups) != 0 {
		t.Errorf("suppressions for an ordinary unsubscribe = %+v, want none", sups)
	}
}

// TestImportStore_Revoke_WidenedForInviteMode is #0129's Revoke acceptance
// criterion: revoking an invite-mode import moves still-pending
// (unaccepted) invitees to unsubscribed, but leaves an address that
// CONFIRMED alone — "its consent no longer derives from the import".
func TestImportStore_Revoke_WidenedForInviteMode(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	pendingEmail, confirmedEmail := uniqueImportEmail(t), uniqueImportEmail(t)
	now := time.Now().UTC().Truncate(time.Second)

	in := validCommitInput(t, []ImportRow{{Email: pendingEmail}, {Email: confirmedEmail}})
	in.ConsentMode = ConsentModeInvite
	committed, err := importStore.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}

	confirmedBefore, err := subStore.FindByEmail(context.Background(), confirmedEmail)
	if err != nil {
		t.Fatalf("FindByEmail(confirmedEmail): %v", err)
	}
	if _, err := subStore.Confirm(context.Background(), *confirmedBefore.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm(confirmedEmail): %v", err)
	}

	revoked, revokedEmails, alreadyRevoked, err := importStore.Revoke(context.Background(), committed.Import.ID, "consent was not properly obtained", now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if alreadyRevoked {
		t.Fatal("alreadyRevoked = true, want false")
	}
	if revoked.Status != ImportStatusRevoked {
		t.Errorf("Import.Status = %q, want %q", revoked.Status, ImportStatusRevoked)
	}
	if len(revokedEmails) != 1 || revokedEmails[0] != pendingEmail {
		t.Errorf("revokedEmails = %v, want [%s] (only the still-pending one)", revokedEmails, pendingEmail)
	}

	pendingAfter, err := subStore.FindByEmail(context.Background(), pendingEmail)
	if err != nil {
		t.Fatalf("FindByEmail(pendingEmail after revoke): %v", err)
	}
	if pendingAfter.Status != StatusUnsubscribed {
		t.Errorf("pendingEmail Status = %q, want %q", pendingAfter.Status, StatusUnsubscribed)
	}
	if pendingAfter.ConfirmToken != nil {
		t.Errorf("pendingEmail ConfirmToken = %v, want nil (cleared by revoke)", *pendingAfter.ConfirmToken)
	}

	confirmedAfter, err := subStore.FindByEmail(context.Background(), confirmedEmail)
	if err != nil {
		t.Fatalf("FindByEmail(confirmedEmail after revoke): %v", err)
	}
	if confirmedAfter.Status != StatusActive {
		t.Errorf("confirmedEmail Status = %q, want %q (left alone — consent no longer derives from the import)", confirmedAfter.Status, StatusActive)
	}
}
