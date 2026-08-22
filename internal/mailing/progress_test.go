// progress_test.go (#0048) proves publishProgress's two load-bearing rules,
// carried in from #0045's plan and restated in #0048's issue text:
//
//   - Remaining is queued+sending — email_sends gained a SEVENTH status
//     ('sending', migration 000018) set by the per-row atomic claim just
//     before each SES call; code that buckets only the original six
//     statuses silently drops every in-flight row from Remaining.
//   - Skipped is its OWN bucket, never folded into Failed — "the list
//     shrank under us" (an unsubscribe/suppression between materialization
//     and send) is correct behavior, not an error.
//
// It also proves the third rule #0048's second review added: the snapshot
// carries the campaign's own email_campaigns.status, because terminality is
// NOT derivable from the counts (MarkFailedCampaign never touches
// email_sends, so a 'failed' campaign publishes Remaining > 0). See
// CampaignProgress's doc comment in worker.go.
//
// Mutation proof for both (see #0048's ## Verification): edit
// publishProgress (worker.go) to drop `+ sending` from Remaining, or to
// fold Skipped into Failed — TestWorker_PublishProgress_SevenStatusArithmetic
// must fail either way.
package mailing

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// fakeProgressPublisher records every CampaignProgress it's handed, so a
// test can assert on the exact snapshot publishProgress computed without a
// real SSE broker.
type fakeProgressPublisher struct {
	calls []CampaignProgress
}

func (f *fakeProgressPublisher) PublishCampaignProgress(_ context.Context, p CampaignProgress) {
	f.calls = append(f.calls, p)
}

// seedSendWithStatus inserts one email_sends row for campaignID, directly at
// the given status — bypassing Materialize/ClaimRow so a test can assemble
// an arbitrary mix of the seven email_sends.status values without driving
// the worker's real claim/send pipeline for each one.
func seedSendWithStatus(t *testing.T, pool *pgxpool.Pool, campaignID int64, status string) {
	t.Helper()
	sendID, _, _ := seedQueuedSend(t, pool, campaignID)
	if status == "queued" {
		return
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_sends SET status = $2 WHERE id = $1`, sendID, status,
	); err != nil {
		t.Fatalf("seed send with status %q: %v", status, err)
	}
}

// TestWorker_PublishProgress_SevenStatusArithmetic is this issue's mutation-
// check target for both guards named in this file's package doc comment.
func TestWorker_PublishProgress_SevenStatusArithmetic(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})

	// One row of every status CountEmailSends buckets, plus a second queued
	// row and a second sending row so Remaining's sum (queued+sending) is
	// not trivially satisfiable by a mutant that reads just one of the two.
	seedSendWithStatus(t, pool, campaignID, "sent")
	seedSendWithStatus(t, pool, campaignID, "failed")
	seedSendWithStatus(t, pool, campaignID, "skipped")
	seedSendWithStatus(t, pool, campaignID, "queued")
	seedSendWithStatus(t, pool, campaignID, "queued")
	seedSendWithStatus(t, pool, campaignID, "sending")
	setCampaignStatus(t, pool, campaignID, "sending") // a mid-drain publish

	w := newTestWorker(t, pool, &RecordingMailer{})
	fake := &fakeProgressPublisher{}
	w.progress = fake

	w.publishProgress(context.Background(), campaignID)

	if len(fake.calls) != 1 {
		t.Fatalf("PublishCampaignProgress called %d times, want exactly 1", len(fake.calls))
	}
	got := fake.calls[0]
	want := CampaignProgress{
		CampaignID: campaignID,
		Status:     "sending",
		Total:      6,
		Sent:       1,
		Failed:     1,
		Skipped:    1,
		Remaining:  3, // 2 queued + 1 sending
	}
	if got != want {
		t.Fatalf("published progress = %+v, want %+v", got, want)
	}
}

// TestWorker_PublishProgress_NilPublisherIsNoop asserts the nil-guard every
// call site in worker.go relies on: a Worker with no progress publisher
// wired (Progress: nil, the state of every instance before #0048) must not
// panic when a batch/completion tries to publish.
func TestWorker_PublishProgress_NilPublisherIsNoop(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	seedSendWithStatus(t, pool, campaignID, "queued")

	w := newTestWorker(t, pool, &RecordingMailer{})
	// w.progress is nil by construction (newTestWorker never sets it).

	w.publishProgress(context.Background(), campaignID) // must not panic
}

// TestWorker_FailCampaign_PublishesTerminalSnapshot proves what makes
// failCampaign's closing frame TERMINAL, and — just as importantly — what
// does not.
//
// #0048's second review bounced the previous version of this test for
// asserting Remaining: 1 under a name promising a terminal snapshot, at a
// time when the client's only terminality signal was `remaining === 0`. The
// assertion was right about the worker and the name was right about the
// intent; what was missing was the field that reconciles them. Remaining
// stays 1 here BY DESIGN — MarkFailedCampaign (worker_store.go) updates
// email_campaigns only, so the unsent row is still 'queued' and that
// recipient genuinely will never be mailed. What makes the frame terminal is
// Status: "failed", read fresh by publishProgress after the transition.
//
// Two mutations this is the target for:
//   - delete `w.publishProgress(ctx, campaignID)` from failCampaign's
//     `if did {}` block → 0 calls, want 1;
//   - drop the CampaignStatus read from publishProgress (publish Status: "")
//     → Status mismatch, which is exactly the defect that left a failed
//     campaign rendering "Sending…" forever.
func TestWorker_FailCampaign_PublishesTerminalSnapshot(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	seedSendWithStatus(t, pool, campaignID, "sent")
	seedSendWithStatus(t, pool, campaignID, "queued")

	// failCampaign/MarkFailedCampaign only transitions a campaign currently
	// 'sending' (worker_store.go's `AND status='sending'` guard) —
	// seedScheduledCampaign leaves it 'scheduled', so move it to 'sending'
	// directly, the same state the real drain loop would have left it in.
	setCampaignStatus(t, pool, campaignID, "sending")

	w := newTestWorker(t, pool, &RecordingMailer{})
	fake := &fakeProgressPublisher{}
	w.progress = fake

	if err := w.failCampaign(context.Background(), campaignID, "empty_audience"); err != nil {
		t.Fatalf("failCampaign: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("PublishCampaignProgress called %d times, want exactly 1", len(fake.calls))
	}
	got := fake.calls[0]
	want := CampaignProgress{
		CampaignID: campaignID,
		Status:     "failed", // ← the terminal signal
		Total:      2,
		Sent:       1,
		Failed:     0,
		Skipped:    0,
		Remaining:  1, // ← deliberately NOT 0: one recipient will never be mailed
	}
	if got != want {
		t.Fatalf("published progress = %+v, want %+v", got, want)
	}

	// Stated separately from the struct compare so a future edit cannot
	// weaken the point by "fixing" Remaining to 0: the frame is terminal
	// while rows remain, and that combination is the whole reason Status
	// exists on CampaignProgress.
	if got.Remaining == 0 {
		t.Errorf("Remaining = 0; the failure path must NOT resolve unsent rows — see CampaignProgress's doc comment")
	}
	if got.Status != CampaignStatusFailed {
		t.Errorf("Status = %q, want %q — the client's only terminality signal on this path", got.Status, CampaignStatusFailed)
	}
}

// TestWorker_PublishProgress_CarriesLiveCampaignStatus pins the second half
// of the contract the previous test proves for 'failed': the published Status
// is read live from email_campaigns on every publish, so it tracks whatever
// the campaign currently is rather than whatever the worker last assumed.
// This is what lets the client treat 'sent'/'failed'/'canceled' as terminal
// and 'sending' as not, without ever inspecting the counts.
func TestWorker_PublishProgress_CarriesLiveCampaignStatus(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	// One queued row throughout, so Remaining is a CONSTANT 1 across every
	// case below — the published Status therefore cannot be an artifact of
	// the counts, which never change.
	seedSendWithStatus(t, pool, campaignID, "queued")

	w := newTestWorker(t, pool, &RecordingMailer{})
	fake := &fakeProgressPublisher{}
	w.progress = fake

	for _, status := range []string{
		CampaignStatusSending,
		CampaignStatusSent,
		CampaignStatusFailed,
		CampaignStatusCanceled,
	} {
		setCampaignStatus(t, pool, campaignID, status)
		fake.calls = nil

		w.publishProgress(context.Background(), campaignID)

		if len(fake.calls) != 1 {
			t.Fatalf("status %q: PublishCampaignProgress called %d times, want exactly 1", status, len(fake.calls))
		}
		got := fake.calls[0]
		if got.Status != status {
			t.Errorf("published Status = %q, want %q", got.Status, status)
		}
		if got.Remaining != 1 {
			t.Errorf("status %q: Remaining = %d, want a constant 1 across every case", status, got.Remaining)
		}
	}
}

// TestSendStore_CampaignStatus_UnknownCampaign asserts publishProgress's
// error branch has something real to handle: a status read for a campaign
// that does not exist errors rather than returning a plausible-looking empty
// string from a silent no-rows path.
func TestSendStore_CampaignStatus_UnknownCampaign(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	w := newTestWorker(t, pool, &RecordingMailer{})

	if _, err := w.store.CampaignStatus(context.Background(), -1); err == nil {
		t.Fatal("CampaignStatus(-1) = nil error, want an error for a nonexistent campaign")
	}
}

// TestWorker_FailCampaign_NoTransitionNoPublish asserts failCampaign's
// existing `if did {}` guard also covers the new publish: a second worker's
// concurrent failure attempt against a campaign that already left 'sending'
// (MarkFailedCampaign's own guard, worker_store.go) must not publish a
// spurious extra snapshot.
func TestWorker_FailCampaign_NoTransitionNoPublish(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	seedSendWithStatus(t, pool, campaignID, "sent")
	// Leave the campaign 'scheduled' (never 'sending'), so MarkFailedCampaign's
	// `AND status='sending'` guard makes `did` false.

	w := newTestWorker(t, pool, &RecordingMailer{})
	fake := &fakeProgressPublisher{}
	w.progress = fake

	if err := w.failCampaign(context.Background(), campaignID, "empty_audience"); err != nil {
		t.Fatalf("failCampaign: %v", err)
	}

	if len(fake.calls) != 0 {
		t.Fatalf("PublishCampaignProgress called %d times, want 0 (no transition performed)", len(fake.calls))
	}
}
