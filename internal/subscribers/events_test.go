package subscribers

import (
	"context"
	"testing"
)

func TestSubscriberEvents_RejectsUnknownAction(t *testing.T) {
	pool := testPool(t)
	err := RecordEventTx(context.Background(), pool, Event{
		Email:  uniqueEmail(t),
		Action: Action("not_a_real_action"),
	})
	if err == nil {
		t.Fatalf("expected an error for an unknown action")
	}
}

func TestSubscriberEvents_RequiresEmail(t *testing.T) {
	pool := testPool(t)
	err := RecordEventTx(context.Background(), pool, Event{
		Action: ActionSignupRequested,
	})
	if err == nil {
		t.Fatalf("expected an error for an empty email")
	}
}

func TestSubscriberEvents_RecordsKnownAction(t *testing.T) {
	pool := testPool(t)
	store := NewStore(pool)
	email := uniqueEmail(t)

	if err := store.RecordEvent(context.Background(), Event{
		Email:  email,
		Action: ActionSignupRequested,
		Detail: map[string]any{"kind": "new"},
	}); err != nil {
		t.Fatalf("RecordEvent: %v", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM subscriber_events WHERE email = $1 AND action = $2`,
		email, string(ActionSignupRequested),
	).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1", count)
	}
}

// TestSubscriberEventActions_EveryConstantHasCallSiteOrOwner is the guard
// #0126's plan §6 requires: every Action constant is either written by a
// live call site in this tree, or has an explicit owner issue recorded
// here, so the closed set can't silently rot (a constant nobody writes and
// nobody has claimed) without a test failing to say so.
//
// This is deliberately a fixed table, not a source scan: the "call site"
// side of the guard is proven by the OTHER tests in this package and in
// internal/handlers/internal/mailing that exercise each write path (e.g.
// TestCreate_ClaimsAndEnqueuesConfirmationAtomically for signup_requested,
// and the outbox worker's own MarkSent tests, once #0126's worker lands,
// for confirmation_sent) — this test's job is only to make sure every
// constant in knownActions has been assigned to one bucket or the other,
// not to re-verify the write itself.
func TestSubscriberEventActions_EveryConstantHasCallSiteOrOwner(t *testing.T) {
	// hasCallSite: written by code in this repo today (#0126, #0127).
	hasCallSite := map[Action]bool{
		ActionSignupRequested:  true,
		ActionConfirmationSent: true,
		ActionConfirmed:        true,
		ActionInterestsChanged: true,
		ActionUnsubscribed:     true,
		ActionResubscribed:     true,
		ActionErased:           true,
		ActionCampaignSent:     true,
		ActionBouncedSoft:      true,
		ActionBouncedHard:      true,
		ActionComplained:       true,
		ActionSuppressed:       true,
		ActionUnsuppressed:     true,
		ActionAdminEdited:      true,
		// ActionDelivered (#0124): written by
		// internal/handlers.SESNotificationsHandler's Delivery-event branch
		// — see ses_notifications.go's applyRecipient, which now dispatches
		// EventTypeDelivery to applyDelivery.
		ActionDelivered: true,
		// ActionWelcomeSent (#0127): written by
		// internal/mailing.OutboxWorker.sendOne's MarkSent branch, the
		// same "written when the message actually LEAVES the queue"
		// precedent confirmation_sent already established — see
		// TestOutboxWorker_MarkSent_RecordsWelcomeSent
		// (internal/mailing/outbox_worker_test.go) for the call-site
		// proof this map only asserts the bookkeeping for.
		ActionWelcomeSent: true,
	}
	// ownedByLaterIssue: no call site yet in this tree; a specific later
	// issue is responsible for adding one. See events.go's package doc
	// comment and #0126's plan §6 table.
	ownedByLaterIssue := map[Action]string{
		ActionConfirmationExpired: "#0128",
		ActionImported:            "#0125",
		ActionImportRevoked:       "#0125",
		ActionInviteSent:          "#0129",
		ActionInviteAccepted:      "#0129",
		ActionInviteExpired:       "#0129",
	}

	for action := range knownActions {
		if hasCallSite[action] {
			continue
		}
		if owner, ok := ownedByLaterIssue[action]; ok && owner != "" {
			continue
		}
		t.Errorf("action %q has neither a call site nor a recorded owner issue — the closed set has rotted", action)
	}

	// The reverse: every entry in these two maps must actually be a known
	// action, so a typo here can't silently pass.
	for action := range hasCallSite {
		if !knownActions[action] {
			t.Errorf("hasCallSite lists %q, which is not in knownActions", action)
		}
	}
	for action := range ownedByLaterIssue {
		if !knownActions[action] {
			t.Errorf("ownedByLaterIssue lists %q, which is not in knownActions", action)
		}
	}
}
