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
