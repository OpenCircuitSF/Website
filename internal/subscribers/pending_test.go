package subscribers

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestListPending_ExcludesSyntheticAndNonPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	pending, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	synthetic, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour, Synthetic: true}, now)
	if err != nil {
		t.Fatalf("Create synthetic: %v", err)
	}
	activeSub, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create active: %v", err)
	}
	if _, err := store.Confirm(context.Background(), *activeSub.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	rows, err := store.ListPending(context.Background(), true)
	if err != nil {
		t.Fatalf("ListPending: %v", err)
	}

	seen := map[int64]bool{}
	for _, r := range rows {
		seen[r.ID] = true
	}
	if !seen[pending.ID] {
		t.Errorf("ListPending missing genuinely pending subscriber %d", pending.ID)
	}
	if seen[synthetic.ID] {
		t.Errorf("ListPending unexpectedly includes synthetic subscriber %d", synthetic.ID)
	}
	if seen[activeSub.ID] {
		t.Errorf("ListPending unexpectedly includes confirmed (active) subscriber %d", activeSub.ID)
	}
}

// TestListPending_SortOrder proves oldestFirst actually drives the ORDER BY
// direction, not just that the query runs — a table-driven property, with
// two subscribers whose confirm_sent_at is forced apart so ascending and
// descending genuinely disagree on which comes first.
func TestListPending_SortOrder(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	older, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("Create older: %v", err)
	}
	newer, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create newer: %v", err)
	}

	indexOf := func(rows []Subscriber, id int64) int {
		for i, r := range rows {
			if r.ID == id {
				return i
			}
		}
		return -1
	}

	asc, err := store.ListPending(context.Background(), true)
	if err != nil {
		t.Fatalf("ListPending(oldestFirst=true): %v", err)
	}
	if indexOf(asc, older.ID) > indexOf(asc, newer.ID) {
		t.Errorf("oldestFirst=true: older subscriber %d did not sort before newer %d", older.ID, newer.ID)
	}

	desc, err := store.ListPending(context.Background(), false)
	if err != nil {
		t.Fatalf("ListPending(oldestFirst=false): %v", err)
	}
	if indexOf(desc, newer.ID) > indexOf(desc, older.ID) {
		t.Errorf("oldestFirst=false: newer subscriber %d did not sort before older %d", newer.ID, older.ID)
	}
}

func TestAdminResendConfirmation_Success(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Move confirm_sent_at into the past so a fresh cooldown check passes.
	past := now.Add(-2 * time.Hour)
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET confirm_sent_at = $2 WHERE id = $1`, created.ID, past); err != nil {
		t.Fatalf("backdating confirm_sent_at: %v", err)
	}

	result, err := store.AdminResendConfirmation(context.Background(), created.ID, now, time.Hour, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("AdminResendConfirmation: %v", err)
	}
	if result.Subscriber.ConfirmToken == nil || *result.Subscriber.ConfirmToken == *created.ConfirmToken {
		t.Errorf("resend did not mint a fresh, different confirm_token")
	}
	if result.Subscriber.ConfirmSentAt == nil || !result.Subscriber.ConfirmSentAt.Equal(now) {
		t.Errorf("ConfirmSentAt = %v, want stamped to %v", result.Subscriber.ConfirmSentAt, now)
	}
	if result.Subscriber.ConfirmExpiresAt == nil || !result.Subscriber.ConfirmExpiresAt.Equal(now.Add(7*24*time.Hour)) {
		t.Errorf("ConfirmExpiresAt = %v, want extended to %v", result.Subscriber.ConfirmExpiresAt, now.Add(7*24*time.Hour))
	}
	if result.PreviousConfirmSentAt == nil || !result.PreviousConfirmSentAt.Equal(past) {
		t.Errorf("PreviousConfirmSentAt = %v, want %v", result.PreviousConfirmSentAt, past)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation' AND status = 'queued'`,
		created.ID,
	).Scan(&queued); err != nil {
		t.Fatalf("counting queued confirmation rows: %v", err)
	}
	// Create's own signup already queued one; the resend queues a second.
	if queued != 2 {
		t.Errorf("queued confirmation rows after resend = %d, want 2 (one from signup, one from resend)", queued)
	}
}

func TestAdminResendConfirmation_CooldownActive(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Create just stamped confirm_sent_at = now, well inside a 1-hour cooldown.
	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now.Add(time.Minute), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendCooldownActive) {
		t.Fatalf("got err=%v, want ErrResendCooldownActive", err)
	}

	var queued int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'confirmation'`, created.ID,
	).Scan(&queued); err != nil {
		t.Fatalf("counting queued rows: %v", err)
	}
	if queued != 1 {
		t.Errorf("queued confirmation rows after a cooldown-refused resend = %d, want still 1 (mail-bomb guard held)", queued)
	}
}

func TestAdminResendConfirmation_SuppressedRefused(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := suppressions.Add(context.Background(), NewSuppression{Email: created.Email, Reason: SuppressionReasonManual, Note: "test"}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now, time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendSuppressed) {
		t.Fatalf("got err=%v, want ErrResendSuppressed", err)
	}
}

func TestAdminResendConfirmation_NotPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Confirm(context.Background(), *created.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := store.AdminResendConfirmation(context.Background(), created.ID, now.Add(2*time.Minute), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrNotPending) {
		t.Fatalf("got err=%v, want ErrNotPending", err)
	}
}

func TestAdminResendConfirmation_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	if _, err := store.AdminResendConfirmation(context.Background(), -1, time.Now(), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrPendingSubscriberNotFound) {
		t.Fatalf("got err=%v, want ErrPendingSubscriberNotFound", err)
	}
}

// TestAdminResendConfirmation_StillRefusesInvited is criterion 2's other
// half (#0312): the pre-existing ErrResendNotForInvited guard survives
// #0312's changes untouched, proven positively rather than merely by
// absence of a regression — no existing test in this package called
// AdminResendConfirmation against a genuinely invited row before this one.
//
// Mutation M5 (#0312's plan): delete the ErrResendNotForInvited guard in
// AdminResendConfirmation. Must fail — and must fail on the RENDERED
// outbound_queue row (kind/payload), not merely on the returned error
// value, so a "fixed" version that returns the right error but still
// enqueues the wrong template would still be caught.
func TestAdminResendConfirmation_StillRefusesInvited(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)

	if _, err := subStore.AdminResendConfirmation(context.Background(), invited.ID, now.Add(time.Minute), time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendNotForInvited) {
		t.Fatalf("got err=%v, want ErrResendNotForInvited", err)
	}

	// The only outbound_queue row for this address must still be the
	// ORIGINAL invitation ImportStore.Commit enqueued — a refused resend
	// must not have enqueued a second, generic-confirmation row alongside it.
	var kind string
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*), max(kind) FROM outbound_queue WHERE subscriber_id = $1`, invited.ID,
	).Scan(&count, &kind); err != nil {
		t.Fatalf("counting outbound_queue rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("outbound_queue rows for %d after a refused resend = %d, want 1 (the original invitation only)", invited.ID, count)
	}
	if kind != "import_invite" {
		t.Errorf("the one outbound_queue row's kind = %q, want %q — a refused resend must never leave a generic-confirmation row behind", kind, "import_invite")
	}
}

// TestAdminResendInvitation_SendsInvitationWithProvenance is #0312
// criterion 1's store-level half: the enqueued outbound_queue row is
// kind=import_invite (never confirmation), carrying the OWNING
// subscriber_imports row's own source/source_detail/collected_at — the
// SAME three fields ImportStore.Commit used for the first invitation.
// TestOutboxWorker_AdminResendInvitation_RendersSameProvenanceAsFirstInvitation
// (internal/mailing, #0312's plan lives there because rendering requires
// internal/mailing.OutboxWorker, which this leaf package cannot import —
// see store.go's package doc comment) is the stronger, rendered-body
// version of the same oracle.
//
// Mutation M1 (#0312's plan): enqueue outbox.KindConfirmation instead. Must
// fail — proven directly here, and independently in internal/mailing.
func TestAdminResendInvitation_SendsInvitationWithProvenance(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	email := uniqueImportEmail(t)
	in := validCommitInput(t, []ImportRow{{Email: email}})
	in.ConsentMode = ConsentModeInvite
	in.Source = ImportSourceLuma
	in.SourceDetail = "Intro to Soldering (provenance payload test)"
	in.CollectedAt = time.Date(2026, 5, 12, 0, 0, 0, 0, time.UTC)
	result, err := importStore.Commit(context.Background(), in, now)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	invited, err := subStore.FindByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Minute), time.Hour); err != nil {
		t.Fatalf("AdminResendInvitation: %v", err)
	}

	var kind, payloadText string
	if err := pool.QueryRow(context.Background(),
		`SELECT kind, payload::text FROM outbound_queue
		  WHERE subscriber_id = $1 ORDER BY id DESC LIMIT 1`,
		invited.ID,
	).Scan(&kind, &payloadText); err != nil {
		t.Fatalf("reading the re-sent row: %v", err)
	}
	if kind != "import_invite" {
		t.Fatalf("re-sent row kind = %q, want %q", kind, "import_invite")
	}
	for _, want := range []string{
		`"import_source": "` + ImportSourceLuma + `"`,
		`"source_detail": "Intro to Soldering (provenance payload test)"`,
		`"collected_at": "2026-05-12T00:00:00Z"`,
	} {
		if !strings.Contains(payloadText, want) {
			t.Errorf("re-sent payload %s does not contain %s", payloadText, want)
		}
	}
	_ = result
}

// TestAdminResendInvitation_RefusesSecondResend is #0312's core bound: the
// approved deviation from PRD §6.10.1 permits at MOST one admin re-send,
// ever, per address — never a second.
//
// Mutation M2 (#0312's plan): drop guard 5 (the invite_resent_at IS NULL
// check). Must fail.
func TestAdminResendInvitation_RefusesSecondResend(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Minute), time.Hour); err != nil {
		t.Fatalf("first AdminResendInvitation: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(2*time.Hour), time.Hour); !errors.Is(err, ErrInviteAlreadyResent) {
		t.Fatalf("second AdminResendInvitation err = %v, want ErrInviteAlreadyResent", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM outbound_queue WHERE subscriber_id = $1 AND kind = 'import_invite'`, invited.ID,
	).Scan(&count); err != nil {
		t.Fatalf("counting import_invite rows: %v", err)
	}
	if count != 2 {
		t.Errorf("import_invite rows after one accepted resend and one refused resend = %d, want 2 (the original plus the one accepted resend)", count)
	}
}

// TestAdminResendInvitation_InvitedAtStaysWriteOnce is #0312 criterion 5's
// direct oracle: invited_at — the column every subsequent import checks to
// decide "has this address ever been invited" (#0129's whole anti-abuse
// property) — must not be re-stamped by an admin re-send. Only
// invite_resent_at, a SEPARATE column, records that the re-send happened.
//
// Mutation M3 (#0312's plan): re-stamp invited_at in the re-send UPDATE.
// Must fail.
func TestAdminResendInvitation_InvitedAtStaysWriteOnce(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, firstImport := commitInvite(t, importStore, uniqueImportEmail(t), now)
	if invited.InvitedAt == nil {
		t.Fatal("invited.InvitedAt is nil right after Commit, want set")
	}
	originalInvitedAt := *invited.InvitedAt

	result, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("AdminResendInvitation: %v", err)
	}
	if result.Subscriber.InvitedAt == nil || !result.Subscriber.InvitedAt.Equal(originalInvitedAt) {
		t.Errorf("InvitedAt after resend = %v, want unchanged %v", result.Subscriber.InvitedAt, originalInvitedAt)
	}
	if result.Subscriber.InviteResentAt == nil {
		t.Error("InviteResentAt is nil after a successful resend, want set")
	}

	// Practical demonstration of what invited_at write-once actually buys:
	// a SECOND import of the same address is still skipped, and neither
	// import's invited_count changes as a result of the resend.
	secondEmail := invited.Email
	secondIn := validCommitInput(t, []ImportRow{{Email: secondEmail}})
	secondIn.ConsentMode = ConsentModeInvite
	secondResult, err := importStore.Commit(context.Background(), secondIn, now.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("second Commit: %v", err)
	}
	if secondResult.Import.InvitedCount != 0 {
		t.Errorf("second import's InvitedCount = %d, want 0 (already-invited address must be skipped)", secondResult.Import.InvitedCount)
	}
	refreshedFirst, err := importStore.GetImport(context.Background(), firstImport.ID)
	if err != nil {
		t.Fatalf("GetImport(first): %v", err)
	}
	if refreshedFirst.InvitedCount != firstImport.InvitedCount {
		t.Errorf("first import's InvitedCount changed from %d to %d after the resend and a later import — want unchanged", firstImport.InvitedCount, refreshedFirst.InvitedCount)
	}
}

// TestAdminResendInvitation_DoesNotExtendConsent is #0312 criterion 3: a
// re-send cannot make the address any more consenting than it was —
// status, consent_basis, and confirmed_at are all untouched, and the
// resend nets zero on Growth30Days (which reads exactly those columns).
func TestAdminResendInvitation_DoesNotExtendConsent(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)

	since := now.Add(-24 * time.Hour)
	confirmedBefore, importedBefore, unsubBefore, err := subStore.Growth30Days(context.Background(), since)
	if err != nil {
		t.Fatalf("Growth30Days before: %v", err)
	}

	result, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Hour), time.Hour)
	if err != nil {
		t.Fatalf("AdminResendInvitation: %v", err)
	}
	if result.Subscriber.Status != StatusPending {
		t.Errorf("Status after resend = %q, want unchanged %q", result.Subscriber.Status, StatusPending)
	}
	if result.Subscriber.ConsentBasis != nil {
		t.Errorf("ConsentBasis after resend = %v, want nil (still unaccepted)", result.Subscriber.ConsentBasis)
	}
	if result.Subscriber.ConfirmedAt != nil {
		t.Errorf("ConfirmedAt after resend = %v, want nil", result.Subscriber.ConfirmedAt)
	}

	confirmedAfter, importedAfter, unsubAfter, err := subStore.Growth30Days(context.Background(), since)
	if err != nil {
		t.Fatalf("Growth30Days after: %v", err)
	}
	if confirmedAfter != confirmedBefore || importedAfter != importedBefore || unsubAfter != unsubBefore {
		t.Errorf("Growth30Days delta across the resend = (confirmed %+d, imported %+d, unsubscribed %+d), want (0, 0, 0)",
			confirmedAfter-confirmedBefore, importedAfter-importedBefore, unsubAfter-unsubBefore)
	}
}

// TestAdminResendInvitation_RefusesNotAnInvitation covers guard 3: an
// ordinary pending website signup (never import-linked) is not an
// unaccepted invitation, and AdminResendInvitation must refuse it rather
// than silently building one.
func TestAdminResendInvitation_RefusesNotAnInvitation(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.AdminResendInvitation(context.Background(), created.ID, now.Add(time.Minute), time.Hour); !errors.Is(err, ErrResendNotAnInvitation) {
		t.Fatalf("got err=%v, want ErrResendNotAnInvitation", err)
	}
}

// TestAdminResendInvitation_RefusesRevokedImport covers guard 4: an invited
// row whose owning subscriber_imports batch has been revoked must not be
// re-invited on that batch's behalf, even in the (currently unreachable
// except by future code) case where the row itself is still 'pending'.
func TestAdminResendInvitation_RefusesRevokedImport(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, imp := commitInvite(t, importStore, uniqueImportEmail(t), now)

	// Revoke moves a still-pending invitee to 'unsubscribed' (see Revoke's
	// own doc comment), which guard 2 (status='pending') would already
	// catch on its own — force the row back to 'pending' afterward so this
	// test isolates guard 4 specifically, per that guard's own doc comment
	// ("not redundant with guard 2").
	if _, _, _, err := importStore.Revoke(context.Background(), imp.ID, "test revoke", now.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE subscribers SET status = $2, confirm_token = $3, confirm_expires_at = $4 WHERE id = $1`,
		invited.ID, StatusPending, *invited.ConfirmToken, invited.ConfirmExpiresAt,
	); err != nil {
		t.Fatalf("forcing row back to pending: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(2*time.Minute), time.Hour); !errors.Is(err, ErrInviteImportRevoked) {
		t.Fatalf("got err=%v, want ErrInviteImportRevoked", err)
	}
}

// TestAdminResendInvitation_RefusesSuppressed and
// TestAdminResendInvitation_RefusesCooldown cover guards 6 and 7 — the same
// two AdminResendConfirmation already enforces, applied unchanged to the
// invitation path (criterion 4: "the cooldown and abuse protections that
// govern the public resend path apply here too").
func TestAdminResendInvitation_RefusesSuppressed(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	suppressions := NewSuppressionStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)
	if _, err := suppressions.Add(context.Background(), NewSuppression{Email: invited.Email, Reason: SuppressionReasonManual, Note: "test"}, now); err != nil {
		t.Fatalf("Add suppression: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Minute), time.Hour); !errors.Is(err, ErrResendSuppressed) {
		t.Fatalf("got err=%v, want ErrResendSuppressed", err)
	}
}

func TestAdminResendInvitation_RefusesCooldown(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)
	// Stamp confirm_sent_at to "now" directly (Commit itself never stamps
	// it — see importInvitePayload's own doc comment) so the cooldown check
	// has something recent to refuse against.
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET confirm_sent_at = $2 WHERE id = $1`, invited.ID, now); err != nil {
		t.Fatalf("stamping confirm_sent_at: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(time.Minute), time.Hour); !errors.Is(err, ErrResendCooldownActive) {
		t.Fatalf("got err=%v, want ErrResendCooldownActive", err)
	}
}

func TestAdminResendInvitation_NotFound(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)

	if _, err := store.AdminResendInvitation(context.Background(), -1, time.Now(), time.Hour); !errors.Is(err, ErrPendingSubscriberNotFound) {
		t.Fatalf("got err=%v, want ErrPendingSubscriberNotFound", err)
	}
}

func TestAdminResendInvitation_NotPending(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)
	if _, err := subStore.Confirm(context.Background(), *invited.ConfirmToken, now.Add(time.Minute)); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := subStore.AdminResendInvitation(context.Background(), invited.ID, now.Add(2*time.Minute), time.Hour); !errors.Is(err, ErrNotPending) {
		t.Fatalf("got err=%v, want ErrNotPending", err)
	}
}

// TestAdminResendConfirmation_AfterPublicTakeover_NoLongerRefuses is #0313
// criterion 2's direct proof, at the store layer: once
// ClaimAndEnqueueConfirmation's converting branch has cleared import_id on
// a row (the public-takeover path #0313 adds), AdminResendConfirmation must
// stop refusing it — the two paths agree because there is no unaccepted
// invitation left for either to disagree about, not because one of them was
// special-cased around the other.
func TestAdminResendConfirmation_AfterPublicTakeover_NoLongerRefuses(t *testing.T) {
	pool := testPool(t)
	importStore := NewImportStore(pool)
	subStore := NewStore(pool)
	now := time.Now().UTC().Truncate(time.Second)

	invited, _ := commitInvite(t, importStore, uniqueImportEmail(t), now)

	// Before the takeover: refused, exactly as #0129 established.
	if _, err := subStore.AdminResendConfirmation(context.Background(), invited.ID, now, time.Hour, 7*24*time.Hour); !errors.Is(err, ErrResendNotForInvited) {
		t.Fatalf("before takeover: got err=%v, want ErrResendNotForInvited", err)
	}

	// Simulate the public takeover directly through the same store method
	// existingSignup's StatusPending branch calls
	// (internal/handlers/subscribe.go's sendConfirmation).
	if claimed, err := subStore.ClaimAndEnqueueConfirmation(context.Background(), invited, now.Add(time.Minute), time.Hour, time.Hour,
		RestartSignupInput{SignupIP: "203.0.113.5", SignupUserAgent: "test-agent"},
	); err != nil || !claimed {
		t.Fatalf("ClaimAndEnqueueConfirmation (public takeover): claimed=%v err=%v", claimed, err)
	}

	// After the takeover: no longer refused.
	if _, err := subStore.AdminResendConfirmation(context.Background(), invited.ID, now.Add(2*time.Hour), time.Hour, 7*24*time.Hour); errors.Is(err, ErrResendNotForInvited) {
		t.Fatalf("after takeover: still got ErrResendNotForInvited, want success")
	} else if err != nil {
		t.Fatalf("after takeover: AdminResendConfirmation: %v", err)
	}
}

// TestExpirePendingSweep_ExpiresPastTTL_LeavesRowPending is #0128's core
// property: a pending signup past confirm_expires_at gets its token cleared
// and a confirmation_expired event, but the row survives as 'pending', not
// deleted.
func TestExpirePendingSweep_ExpiresPastTTL_LeavesRowPending(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Minute}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	swept, err := store.ExpirePendingSweep(context.Background(), now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("ExpirePendingSweep: %v", err)
	}
	if swept < 1 {
		t.Fatalf("ExpirePendingSweep swept %d rows, want at least 1", swept)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Status != StatusPending {
		t.Errorf("Status = %q after sweep, want %q (left pending, not deleted)", got.Status, StatusPending)
	}
	if got.ConfirmToken != nil {
		t.Errorf("ConfirmToken = %v after sweep, want nil (cleared)", *got.ConfirmToken)
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_expired'`, created.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting confirmation_expired events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("confirmation_expired events = %d, want 1", eventCount)
	}
}

// TestExpirePendingSweep_NotYetExpired_LeavesRowUntouched proves the sweep
// does not touch a pending signup whose confirm_expires_at has not passed.
func TestExpirePendingSweep_NotYetExpired_LeavesRowUntouched(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Hour}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := store.ExpirePendingSweep(context.Background(), now.Add(time.Minute)); err != nil {
		t.Fatalf("ExpirePendingSweep: %v", err)
	}

	got, err := store.GetByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ConfirmToken == nil {
		t.Errorf("ConfirmToken cleared by sweep before expiry — sweep ran too eagerly")
	}
}

// TestExpirePendingSweep_IsIdempotent proves a second sweep pass over an
// already-expired-and-cleared row does not write a duplicate
// confirmation_expired event — the WHERE clause's confirm_token IS NOT NULL
// guard is what makes this true.
func TestExpirePendingSweep_IsIdempotent(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	now := time.Now()

	created, err := store.Create(context.Background(), NewSignup{Email: uniqueEmail(t), ConfirmTTL: time.Minute}, now)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	later := now.Add(2 * time.Minute)
	if _, err := store.ExpirePendingSweep(context.Background(), later); err != nil {
		t.Fatalf("first ExpirePendingSweep: %v", err)
	}
	if _, err := store.ExpirePendingSweep(context.Background(), later.Add(time.Minute)); err != nil {
		t.Fatalf("second ExpirePendingSweep: %v", err)
	}

	var eventCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE subscriber_id = $1 AND action = 'confirmation_expired'`, created.ID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("counting confirmation_expired events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("confirmation_expired events after two sweep passes = %d, want 1", eventCount)
	}
}
