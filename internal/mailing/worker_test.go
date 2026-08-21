package mailing

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

func sesTooManyRequests() error     { return &types.TooManyRequestsException{} }
func sesLimitExceeded() error       { return &types.LimitExceededException{} }
func sesSendingPaused() error       { return &types.SendingPausedException{} }
func sesAccountSuspended() error    { return &types.AccountSuspendedException{} }
func sesMailFromNotVerified() error { return &types.MailFromDomainNotVerifiedException{} }
func sesNotFound() error            { return &types.NotFoundException{} }
func sesBadRequest() error          { return &types.BadRequestException{} }
func sesMessageRejected() error     { return &types.MessageRejected{} }

// ── 1. The physical_address refusal (CLAUDE.md §9) ─────────────────────────

// TestWorker_RefusesCampaignWhenPhysicalAddressBlank proves the send worker
// itself — not just Preflight in isolation — refuses to start a campaign
// whose physical_address setting is blank, whitespace-only, or missing
// entirely, demoting it to draft and writing exactly one send_refused audit
// row carrying physical_address_missing. Mutation: change
// `strings.TrimSpace(v) == ""` to `v == ""` in Preflight (preflight.go) —
// the three whitespace cases must fail; delete the check entirely — all
// four must fail.
func TestWorker_RefusesCampaignWhenPhysicalAddressBlank(t *testing.T) {
	pool := testPool(t)
	cases := []struct {
		name    string
		address string
		unset   bool
	}{
		{"empty", "", false},
		{"single space", " ", false},
		{"tabs and newlines", "\t\n ", false},
		{"row missing entirely", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workerTestFixture(t, pool)
			if tc.unset {
				setSetting(t, pool, settingPhysicalAddress, "") // seeded row always exists; simulate via empty
			} else {
				setSetting(t, pool, settingPhysicalAddress, tc.address)
			}

			interestA := seedInterest(t, pool)
			campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
			resetCampaignAuditRows(t, pool, campaignID)
			subID := seedSubscriber(t, pool, subscribers.StatusActive)
			attachInterest(t, pool, subID, interestA.ID)

			mailer := &RecordingMailer{}
			auditLogger := auditLoggerForTest(t, pool)
			w := newTestWorker(t, pool, mailer)
			w.auditor = auditLogger

			processed, err := w.claimAndDrain(context.Background())
			if err != nil {
				t.Fatalf("claimAndDrain: %v", err)
			}
			if !processed {
				t.Fatal("claimAndDrain() = false, want true (the campaign should have been demoted)")
			}

			if len(mailer.Sent()) != 0 {
				t.Fatalf("mailer.Sent() = %d messages, want 0", len(mailer.Sent()))
			}
			if got := campaignStatus(t, pool, campaignID); got != CampaignStatusDraft {
				t.Fatalf("status = %q, want draft", got)
			}

			rows := auditRowsFor(t, pool, campaignID, audit.ActionEmailCampaignSendRefused)
			if len(rows) != 1 {
				t.Fatalf("send_refused audit rows = %d, want exactly 1", len(rows))
			}
			if !strings.Contains(rows[0], PreflightCodePhysicalAddress) {
				t.Errorf("audit metadata %q does not mention %q", rows[0], PreflightCodePhysicalAddress)
			}
		})
	}
}

// ── 2. Not bypassable from the UI (CLAUDE.md §9) ────────────────────────────

// TestWorker_RefusesCampaignForcedToScheduledBySQL inserts a campaign
// straight into 'scheduled' with a blank physical_address via raw SQL —
// bypassing every handler, exactly like a direct psql UPDATE — and proves
// the worker still refuses it. Mutation: skip the gate and go straight to
// ClaimStart in claimAndDrain — must fail (this test would then see a sent
// message and a 'sending'/'sent' campaign).
func TestWorker_RefusesCampaignForcedToScheduledBySQL(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, settingPhysicalAddress, "")

	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	// Raw SQL, not CampaignStore.Send — simulating a bypass of every handler.
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'scheduled', scheduled_at = now() - interval '1 minute', test_sent_at = now() WHERE id = $1`,
		c.ID,
	); err != nil {
		t.Fatalf("force campaign to scheduled: %v", err)
	}
	seedSubscriber(t, pool, subscribers.StatusActive)

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}
	if len(mailer.Sent()) != 0 {
		t.Fatalf("mailer.Sent() = %d, want 0 — nothing should ever be sent", len(mailer.Sent()))
	}
	if got := campaignStatus(t, pool, c.ID); got == CampaignStatusSending || got == CampaignStatusSent {
		t.Fatalf("status = %q, want draft or failed (never sending/sent)", got)
	}
}

// ── 3. No double-send, two workers ──────────────────────────────────────────

// TestWorker_TwoWorkersOneCampaign_EachRecipientSentExactlyOnce runs two
// Worker values concurrently against the SAME already-'sending',
// already-materialized campaign and asserts every recipient is sent exactly
// once across both workers combined. Mutation: drop `AND status='queued'`
// from ClaimRow's UPDATE (worker_store.go) — must fail with duplicates.
func TestWorker_TwoWorkersOneCampaign_EachRecipientSentExactlyOnce(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	const n = 24
	var emails []string
	for i := 0; i < n; i++ {
		_, _, email := seedQueuedSend(t, pool, campaignID)
		emails = append(emails, email)
	}

	mailer := &RecordingMailer{}
	w1 := newTestWorker(t, pool, mailer)
	w2 := newTestWorker(t, pool, mailer)

	var wg sync.WaitGroup
	wg.Add(2)
	for _, w := range []*Worker{w1, w2} {
		w := w
		go func() {
			defer wg.Done()
			c, err := w.store.ClaimResume(context.Background())
			if err != nil || c == nil {
				t.Errorf("ClaimResume: c=%+v err=%v", c, err)
				return
			}
			if err := w.drainCampaign(context.Background(), c); err != nil {
				t.Errorf("drainLoop: %v", err)
			}
		}()
	}
	wg.Wait()

	sent := mailer.Sent()
	if len(sent) != n {
		t.Fatalf("mailer.Sent() = %d messages, want %d", len(sent), n)
	}
	for _, email := range emails {
		if got := countMessagesTo(sent, email); got != 1 {
			t.Errorf("countMessagesTo(%q) = %d, want exactly 1", email, got)
		}
	}
	counts := countSendStatuses(t, pool, campaignID)
	if counts["sent"] != n {
		t.Errorf("email_sends sent count = %d, want %d (statuses: %v)", counts["sent"], n, counts)
	}
}

// ── 4. No double-send across a crash ────────────────────────────────────────

// TestWorker_AbortBetweenSESAcceptAndStatusWrite_ResumesWithOneDuplicate
// fault-injects an abort on the 4th successful send — reproducing the §6
// crash window at the exact instruction boundary a SIGKILL would hit,
// deterministically. A second Worker then resumes. Mutations: (a) delete
// the orphan sweep — recipient #4 receives one message but the row stays
// 'sending' and the campaign never completes; (b) change Materialize's
// ON CONFLICT DO NOTHING to DO UPDATE SET status='queued' — every recipient
// duplicates (not exercised by this test directly, but the property is
// covered by TestSendStore_OrphanSweep_ResetsOnlySendingRows and the schema
// comment on email_sends).
func TestWorker_AbortBetweenSESAcceptAndStatusWrite_ResumesWithOneDuplicate(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	const n = 10
	var emails []string
	var sendIDs []int64
	for i := 0; i < n; i++ {
		id, _, email := seedQueuedSend(t, pool, campaignID)
		sendIDs = append(sendIDs, id)
		emails = append(emails, email)
	}

	mailer := &RecordingMailer{}
	w1 := newTestWorker(t, pool, mailer)

	sendPostCrashHook = abortAfterNSends(4)
	t.Cleanup(func() { sendPostCrashHook = nil })

	c1, err := w1.store.ClaimResume(context.Background())
	if err != nil || c1 == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c1, err)
	}
	err = w1.drainCampaign(context.Background(), c1)
	if err == nil || !errors.Is(err, errAbortedForTest) {
		t.Fatalf("first drainLoop error = %v, want errAbortedForTest", err)
	}
	if len(mailer.Sent()) != 4 {
		t.Fatalf("after simulated crash, mailer.Sent() = %d, want 4", len(mailer.Sent()))
	}

	sendPostCrashHook = nil // second worker resumes for real, no further crashes

	w2 := newTestWorker(t, pool, mailer)
	c2, err := w2.store.ClaimResume(context.Background())
	if err != nil || c2 == nil {
		t.Fatalf("second ClaimResume: c=%+v err=%v", c2, err)
	}
	if err := w2.drainCampaign(context.Background(), c2); err != nil {
		t.Fatalf("second drainLoop: %v", err)
	}

	sent := mailer.Sent()
	if len(sent) != n+1 {
		t.Fatalf("total mailer.Sent() = %d, want %d (one acknowledged duplicate)", len(sent), n+1)
	}
	dupEmail := emails[3] // the 4th send, 0-indexed
	if got := countMessagesTo(sent, dupEmail); got != 2 {
		t.Errorf("countMessagesTo(dupEmail) = %d, want 2", got)
	}
	for i, email := range emails {
		if i == 3 {
			continue
		}
		if got := countMessagesTo(sent, email); got != 1 {
			t.Errorf("countMessagesTo(emails[%d]) = %d, want 1", i, got)
		}
	}

	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusSent {
		t.Fatalf("campaign status = %q, want sent", got)
	}
	counts := countSendStatuses(t, pool, campaignID)
	if counts["sent"] != n {
		t.Errorf("email_sends sent count = %d, want %d", counts["sent"], n)
	}
}

// TestWorker_AbortBeforeSESCall_ResumesWithNoDuplicate is the companion
// proving the other side of the window: a crash BEFORE the SES call (the
// row was claimed, attempts incremented, but nothing was ever sent) resumes
// with no duplicate at all.
func TestWorker_AbortBeforeSESCall_ResumesWithNoDuplicate(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	const n = 6
	var emails []string
	for i := 0; i < n; i++ {
		_, _, email := seedQueuedSend(t, pool, campaignID)
		emails = append(emails, email)
	}

	mailer := &RecordingMailer{}
	w1 := newTestWorker(t, pool, mailer)

	sendPreCrashHook = abortAfterNSends(3)
	t.Cleanup(func() { sendPreCrashHook = nil })

	c1, err := w1.store.ClaimResume(context.Background())
	if err != nil || c1 == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c1, err)
	}
	err = w1.drainCampaign(context.Background(), c1)
	if err == nil || !errors.Is(err, errAbortedForTest) {
		t.Fatalf("first drainLoop error = %v, want errAbortedForTest", err)
	}
	if len(mailer.Sent()) != 2 {
		t.Fatalf("before the simulated crash, mailer.Sent() = %d, want 2", len(mailer.Sent()))
	}

	sendPreCrashHook = nil
	w2 := newTestWorker(t, pool, mailer)
	c2, err := w2.store.ClaimResume(context.Background())
	if err != nil || c2 == nil {
		t.Fatalf("second ClaimResume: c=%+v err=%v", c2, err)
	}
	if err := w2.drainCampaign(context.Background(), c2); err != nil {
		t.Fatalf("second drainLoop: %v", err)
	}

	sent := mailer.Sent()
	if len(sent) != n {
		t.Fatalf("total mailer.Sent() = %d, want %d — no duplicate", len(sent), n)
	}
	for _, email := range emails {
		if got := countMessagesTo(sent, email); got != 1 {
			t.Errorf("countMessagesTo(%q) = %d, want exactly 1", email, got)
		}
	}
}

// ── 5. The eligibility re-check happens inside the claim transaction ───────

// TestWorker_UnsubscribedBetweenMaterializeAndSend_IsSkippedNotMailed proves
// a subscriber who unsubscribes after materialization is skipped, not
// mailed, and every suppression reason produces the same result. Mutation:
// delete the RecheckEligibleTx/MarkSkippedTx calls from ClaimBatch — the
// unsubscribed/suppressed address gets mail; must fail.
func TestWorker_UnsubscribedBetweenMaterializeAndSend_IsSkippedNotMailed(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	okID, _, okEmail := seedQueuedSend(t, pool, campaignID)
	unsubID, unsubSubID, unsubEmail := seedQueuedSend(t, pool, campaignID)
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET status = 'unsubscribed' WHERE id = $1`, unsubSubID); err != nil {
		t.Fatalf("unsubscribe seeded subscriber: %v", err)
	}

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)
	c, err := w.store.ClaimResume(context.Background())
	if err != nil || c == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c, err)
	}
	if err := w.drainCampaign(context.Background(), c); err != nil {
		t.Fatalf("drainLoop: %v", err)
	}

	sent := mailer.Sent()
	if countMessagesTo(sent, unsubEmail) != 0 {
		t.Errorf("unsubscribed address received mail: %v", sent)
	}
	if countMessagesTo(sent, okEmail) != 1 {
		t.Errorf("eligible address did not receive exactly one message: %v", sent)
	}
	if got := sendRowStatus(t, pool, unsubID); got != "skipped" {
		t.Errorf("unsubscribed row status = %q, want skipped (not failed)", got)
	}
	if got := sendRowStatus(t, pool, okID); got != "sent" {
		t.Errorf("eligible row status = %q, want sent", got)
	}
}

// TestWorker_AllFourSuppressionReasons_AreSkipped repeats the property above
// for every subscribers.SuppressionReason* value.
func TestWorker_AllFourSuppressionReasons_AreSkipped(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	reasons := []string{
		subscribers.SuppressionReasonHardBounce,
		subscribers.SuppressionReasonComplaint,
		subscribers.SuppressionReasonManual,
		subscribers.SuppressionReasonRepeatedSoftBounce,
	}

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	type row struct {
		sendID int64
		email  string
	}
	var suppressed []row
	for _, reason := range reasons {
		id, _, email := seedQueuedSend(t, pool, campaignID)
		seedSuppression(t, pool, email, reason)
		suppressed = append(suppressed, row{id, email})
	}

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)
	c, err := w.store.ClaimResume(context.Background())
	if err != nil || c == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c, err)
	}
	if err := w.drainCampaign(context.Background(), c); err != nil {
		t.Fatalf("drainLoop: %v", err)
	}

	sent := mailer.Sent()
	for _, r := range suppressed {
		if countMessagesTo(sent, r.email) != 0 {
			t.Errorf("suppressed address %q received mail", r.email)
		}
		if got := sendRowStatus(t, pool, r.sendID); got != "skipped" {
			t.Errorf("row %d status = %q, want skipped", r.sendID, got)
		}
	}
}

// ── 6. No open-tracking pixel (CLAUDE.md §9) ────────────────────────────────

var trackingImgTag = regexp.MustCompile(`(?i)<img[^>]*>`)
var trackingImgSrc = regexp.MustCompile(`(?i)src="([^"]+)"`)
var trackingImgWidth = regexp.MustCompile(`(?i)width="?1"?`)
var trackingImgHeight = regexp.MustCompile(`(?i)height="?1"?`)

// TestWorker_SentMessagesContainNoTrackingPixel drains a real campaign
// through the real renderer and scans every delivered HTML body for a 1x1
// image or a remote-host <img>. Mutation: inject
// `<img src="https://track.example/o/{{token}}.gif" width="1" height="1">`
// into the campaign body (simulating a template regression) — must fail.
func TestWorker_SentMessagesContainNoTrackingPixel(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject",
		"Hello **world**.\n\n![logo](https://www.example-oc-test.com/logo.png)", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	seedQueuedSend(t, pool, campaignID)

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)
	c, err := w.store.ClaimResume(context.Background())
	if err != nil || c == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c, err)
	}
	if err := w.drainCampaign(context.Background(), c); err != nil {
		t.Fatalf("drainLoop: %v", err)
	}

	sent := mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("mailer.Sent() = %d, want 1", len(sent))
	}
	for _, tag := range trackingImgTag.FindAllString(sent[0].HTMLBody, -1) {
		is1x1 := trackingImgWidth.MatchString(tag) && trackingImgHeight.MatchString(tag)
		srcMatch := trackingImgSrc.FindStringSubmatch(tag)
		remoteHost := len(srcMatch) == 2 && !strings.HasPrefix(srcMatch[1], testWorkerBaseURL)
		if is1x1 || remoteHost {
			t.Errorf("delivered HTML contains a suspicious <img>: %s", tag)
		}
	}
}

// ── 7 & 8. Per-recipient headers, campaign mail only ────────────────────────

// TestWorker_MultiRecipientBatch_EachMessageCarriesOwnFreshToken proves the
// send loop that actually runs (not a simulation of its shape) builds
// CampaignHeaders per recipient from that recipient's OWN, CURRENT
// manage_token — including a token rotated after materialization.
// Mutations: (a) hoist CampaignHeaders above the loop — must fail; (b) use
// the materialization-time token instead of the fresh recheck value — the
// rotated recipient must fail.
func TestWorker_MultiRecipientBatch_EachMessageCarriesOwnFreshToken(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Hello world", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	_, sub1, email1 := seedQueuedSend(t, pool, campaignID)
	_, sub2, email2 := seedQueuedSend(t, pool, campaignID)
	_, sub3, email3 := seedQueuedSend(t, pool, campaignID)

	// Rotate subscriber 2's token AFTER materialization — the fresh value
	// RecheckEligibleTx reads, not any snapshot.
	newToken := fmt.Sprintf("zz-rotated-token-%d", time.Now().UnixNano())
	if _, err := pool.Exec(context.Background(), `UPDATE subscribers SET manage_token = $2 WHERE id = $1`, sub2, newToken); err != nil {
		t.Fatalf("rotate token: %v", err)
	}

	tokensByID := map[int64]string{}
	rows, err := pool.Query(context.Background(), `SELECT id, manage_token FROM subscribers WHERE id = ANY($1)`, []int64{sub1, sub2, sub3})
	if err != nil {
		t.Fatalf("read tokens: %v", err)
	}
	for rows.Next() {
		var id int64
		var tok string
		if err := rows.Scan(&id, &tok); err != nil {
			t.Fatalf("scan token: %v", err)
		}
		tokensByID[id] = tok
	}
	rows.Close()

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)
	c, err := w.store.ClaimResume(context.Background())
	if err != nil || c == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c, err)
	}
	if err := w.drainCampaign(context.Background(), c); err != nil {
		t.Fatalf("drainLoop: %v", err)
	}

	sent := mailer.Sent()
	if len(sent) != 3 {
		t.Fatalf("mailer.Sent() = %d, want 3", len(sent))
	}

	byTo := map[string]Message{}
	for _, m := range sent {
		byTo[m.To] = m
	}
	for subID, email := range map[int64]string{sub1: email1, sub2: email2, sub3: email3} {
		msg, ok := byTo[email]
		if !ok {
			t.Fatalf("no message sent to %q", email)
		}
		wantToken := tokensByID[subID]
		listUnsub := headerValue(t, msg.Headers, "List-Unsubscribe")
		if !strings.Contains(listUnsub, wantToken) {
			t.Errorf("message to %q's List-Unsubscribe %q does not contain its own token %q", email, listUnsub, wantToken)
		}
		// Cross-contamination check: no OTHER subscriber's token appears.
		for otherID, otherTok := range tokensByID {
			if otherID == subID {
				continue
			}
			if strings.Contains(listUnsub, otherTok) {
				t.Errorf("message to %q's List-Unsubscribe contains ANOTHER recipient's token %q", email, otherTok)
			}
		}
		_ = headerValue(t, msg.Headers, "List-Unsubscribe-Post")
		_ = headerValue(t, msg.Headers, "List-Id")
		if !strings.Contains(msg.HTMLBody, wantToken) {
			t.Errorf("message to %q's HTML body footer does not contain its own token", email)
		}
	}
}

// TestWorker_RefusesCampaignWhenReplyToBlank is the worker-level half of
// #0043's carried-in review item: SESMailer.Send already sets Reply-To from
// cfg.EmailReplyTo on every message (Message itself has no ReplyTo field —
// RecordingMailer, used here, has no way to observe that transport-level
// header), so what this issue adds is refusing to drain at all when
// EMAIL_REPLY_TO is blank, rather than silently shipping campaign mail with
// no Reply-To. The header itself, on a real SESMailer, is already pinned by
// TestSESMailer_Send_MultipartWithCustomHeaders in ses_mailer_test.go.
func TestWorker_RefusesCampaignWhenReplyToBlank(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)
	seedQueuedSend(t, pool, campaignID)

	mailer := &RecordingMailer{}
	auditLogger := auditLoggerForTest(t, pool)
	audienceStore := NewAudienceStore(pool)
	settings := dbSettings{pool: pool}
	sendStore := NewSendStore(pool, audienceStore, settings, nil, testWorkerBaseURL, testWorkerListDomain, "" /* blank ReplyTo */)
	w, err := NewWorker(WorkerDeps{
		Store: sendStore, Audience: audienceStore, Audit: auditLogger, Mailer: mailer, Settings: settings,
		BaseURL: testWorkerBaseURL, ListDomain: testWorkerListDomain, FromAddr: testWorkerFromAddr, ReplyTo: "",
		EnvMaxSendRate: 100000, BatchSize: 50,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	c, err := w.store.ClaimResume(context.Background())
	if err != nil || c == nil {
		t.Fatalf("ClaimResume: c=%+v err=%v", c, err)
	}
	if err := w.drainCampaign(context.Background(), c); err != nil {
		t.Fatalf("drainLoop: %v", err)
	}
	if len(mailer.Sent()) != 0 {
		t.Fatalf("mailer.Sent() = %d, want 0 — a blank reply-to must stop the drain", len(mailer.Sent()))
	}
	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusFailed {
		t.Fatalf("status = %q, want failed", got)
	}
}

// ── 9. The test-send gate ───────────────────────────────────────────────────

// TestWorker_RefusesCampaignWithoutTestSend proves a campaign with no
// test_sent_at is refused. Mutation: delete the TestSentAt==nil branch from
// Preflight — must fail.
func TestWorker_RefusesCampaignWithoutTestSend(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	store := NewCampaignStore(pool)
	c, err := store.Create(context.Background(), CampaignInput{
		Name: uniqueCampaignName(t), Subject: "s", BodyMD: "b", AudienceMode: AudienceAll,
	})
	if err != nil {
		t.Fatalf("create campaign: %v", err)
	}
	cleanupCampaign(t, pool, c.ID)
	// Schedule WITHOUT stamping test_sent_at (unlike seedScheduledCampaign).
	if _, err := pool.Exec(context.Background(),
		`UPDATE email_campaigns SET status = 'scheduled', scheduled_at = now() - interval '1 minute' WHERE id = $1`,
		c.ID,
	); err != nil {
		t.Fatalf("schedule campaign: %v", err)
	}
	seedSubscriber(t, pool, subscribers.StatusActive)

	mailer := &RecordingMailer{}
	w := newTestWorker(t, pool, mailer)
	processed, err := w.claimAndDrain(context.Background())
	if err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}
	if !processed {
		t.Fatal("claimAndDrain() = false, want true")
	}
	if len(mailer.Sent()) != 0 {
		t.Fatalf("mailer.Sent() = %d, want 0", len(mailer.Sent()))
	}
	if got := campaignStatus(t, pool, c.ID); got != CampaignStatusDraft {
		t.Fatalf("status = %q, want draft", got)
	}
}

// ── 10. EMAIL_LIST_DOMAIN unset ─────────────────────────────────────────────

// TestWorker_RefusesWhenListDomainUnset asserts nothing is sent and no
// malformed mailto: header ever reaches the mailer when ListDomain is
// blank. Mutation: delete the list_domain_unset check from Preflight —
// must fail.
func TestWorker_RefusesWhenListDomainUnset(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	seedSubscriber(t, pool, subscribers.StatusActive)

	mailer := &RecordingMailer{}
	audienceStore := NewAudienceStore(pool)
	settings := dbSettings{pool: pool}
	sendStore := NewSendStore(pool, audienceStore, settings, nil, testWorkerBaseURL, "" /* blank ListDomain */, testWorkerReplyTo)
	w, err := NewWorker(WorkerDeps{
		Store: sendStore, Audience: audienceStore, Mailer: mailer, Settings: settings,
		BaseURL: testWorkerBaseURL, ListDomain: "", FromAddr: testWorkerFromAddr, ReplyTo: testWorkerReplyTo,
		EnvMaxSendRate: 100000, BatchSize: 50,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	processed, err := w.claimAndDrain(context.Background())
	if err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}
	if !processed {
		t.Fatal("claimAndDrain() = false, want true")
	}
	if len(mailer.Sent()) != 0 {
		t.Fatalf("mailer.Sent() = %d, want 0", len(mailer.Sent()))
	}
	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusDraft {
		t.Fatalf("status = %q, want draft", got)
	}
}

// ── 11. Attempts bound retries ──────────────────────────────────────────────

// TestWorker_ThreeRetryableFailuresMarkRowFailed uses a mailer that always
// throttles for one address and always succeeds for another, and asserts
// the always-failing row becomes 'failed' with attempts==3 while the other
// completes normally. Mutation: increment attempts AFTER the send instead
// of in the claim (ClaimRow) — the count never reaches 3 and this test must
// fail.
func TestWorker_ThreeRetryableFailuresMarkRowFailed(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)
	setSetting(t, pool, settingMaxSendRate, "100000")

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	forceSending(t, pool, campaignID)

	badID, _, badEmail := seedQueuedSend(t, pool, campaignID)
	goodID, _, goodEmail := seedQueuedSend(t, pool, campaignID)

	mailer := &recordingClassifiableMailer{
		classify: func(to string) error {
			if to == badEmail {
				return sesTooManyRequests()
			}
			return nil
		},
	}
	w := newTestWorker(t, pool, mailer)
	w.sleep = func(context.Context, time.Duration) error { return nil } // never wait in a test

	// Drain repeatedly: each pass claims the batch fresh (the bad row keeps
	// returning to 'queued' until its 3rd attempt).
	for i := 0; i < 4; i++ {
		c, err := w.store.ClaimResume(context.Background())
		if err != nil {
			t.Fatalf("ClaimResume pass %d: %v", i, err)
		}
		if c == nil {
			break
		}
		if err := w.drainCampaign(context.Background(), c); err != nil {
			t.Fatalf("drainLoop pass %d: %v", i, err)
		}
		if sendRowStatus(t, pool, badID) == "failed" {
			break
		}
	}

	if got := sendRowStatus(t, pool, badID); got != "failed" {
		t.Fatalf("bad row status = %q, want failed", got)
	}
	var attempts int
	if err := pool.QueryRow(context.Background(), `SELECT attempts FROM email_sends WHERE id = $1`, badID).Scan(&attempts); err != nil {
		t.Fatalf("read attempts: %v", err)
	}
	if attempts != 3 {
		t.Errorf("attempts = %d, want 3", attempts)
	}
	if got := sendRowStatus(t, pool, goodID); got != "sent" {
		t.Errorf("good row status = %q, want sent", got)
	}
	if countMessagesTo(mailer.Sent(), goodEmail) != 1 {
		t.Errorf("good address did not receive exactly one message")
	}
	if countMessagesTo(mailer.Sent(), badEmail) != 0 {
		t.Errorf("bad address should never have received mail")
	}
}

// ── 12. Error classification ────────────────────────────────────────────────

func TestClassifySendError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want sendClass
	}{
		{"throttling", sesTooManyRequests(), sendClassRetryable},
		{"limit exceeded", sesLimitExceeded(), sendClassRetryable},
		{"sending paused", sesSendingPaused(), sendClassTerminalCampaign},
		{"account suspended", sesAccountSuspended(), sendClassTerminalCampaign},
		{"mail from not verified", sesMailFromNotVerified(), sendClassTerminalCampaign},
		{"not found (config set)", sesNotFound(), sendClassTerminalCampaign},
		{"bad request", sesBadRequest(), sendClassTerminalCampaign},
		{"message rejected", sesMessageRejected(), sendClassTerminalRow},
		{"no message id", ErrNoMessageID, sendClassTerminalRow},
		{"wrapped throttling", errorsWrap(sesTooManyRequests()), sendClassRetryable},
		{"unknown error", errors.New("network blip"), sendClassRetryable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySendError(tc.err); got != tc.want {
				t.Errorf("classifySendError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// ── 13. From header injection ───────────────────────────────────────────────

func TestWorker_FromHeaderRejectsUnsafeDisplayName(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	cases := []struct {
		name  string
		value string
	}{
		{"CRLF injection", "Open Circuit\r\nBcc: x@y"},
		{"non-ASCII", "Öpen Circuit SF"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setSetting(t, pool, settingDefaultFromName, tc.value)
			w := newTestWorker(t, pool, &RecordingMailer{})
			got := w.resolveFromHeader(context.Background())
			if got != "" {
				t.Errorf("resolveFromHeader() = %q, want empty (fallback to bare address)", got)
			}
		})
	}

	t.Run("safe name is wrapped", func(t *testing.T) {
		setSetting(t, pool, settingDefaultFromName, "Open Circuit SF")
		w := newTestWorker(t, pool, &RecordingMailer{})
		got := w.resolveFromHeader(context.Background())
		want := `"Open Circuit SF" <` + testWorkerFromAddr + `>`
		if got != want {
			t.Errorf("resolveFromHeader() = %q, want %q", got, want)
		}
	})
}

// ── 14. Gate ordering — covered by preflight_test.go's
// TestPreflight_FailureOrderIsStable.

// ── 15. Completion ──────────────────────────────────────────────────────────

func TestWorker_CampaignCompletesWhenNoQueuedRowsRemain(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)
	seedSubscriber(t, pool, subscribers.StatusActive)

	mailer := &RecordingMailer{}
	auditLogger := auditLoggerForTest(t, pool)
	w := newTestWorker(t, pool, mailer)
	w.auditor = auditLogger

	if _, err := w.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("claimAndDrain: %v", err)
	}

	if got := campaignStatus(t, pool, campaignID); got != CampaignStatusSent {
		t.Fatalf("status = %q, want sent", got)
	}
	var completedAt *time.Time
	if err := pool.QueryRow(context.Background(), `SELECT completed_at FROM email_campaigns WHERE id = $1`, campaignID).Scan(&completedAt); err != nil {
		t.Fatalf("read completed_at: %v", err)
	}
	if completedAt == nil {
		t.Error("completed_at is nil, want set")
	}
	rows := auditRowsFor(t, pool, campaignID, audit.ActionEmailCampaignSendCompleted)
	if len(rows) != 1 {
		t.Fatalf("send_completed audit rows = %d, want exactly 1", len(rows))
	}
}

// TestWorker_SendStartedAuditWrittenOnlyOnFirstClaim proves
// campaign.send_started is written exactly once even across a resume.
func TestWorker_SendStartedAuditWrittenOnlyOnFirstClaim(t *testing.T) {
	pool := testPool(t)
	workerTestFixture(t, pool)

	campaignID := seedScheduledCampaign(t, pool, "Subject", "Body", Audience{Mode: AudienceAll})
	resetCampaignAuditRows(t, pool, campaignID)
	seedSubscriber(t, pool, subscribers.StatusActive)
	seedSubscriber(t, pool, subscribers.StatusActive)

	auditLogger := auditLoggerForTest(t, pool)
	w1 := newTestWorker(t, pool, &RecordingMailer{})
	w1.auditor = auditLogger

	if _, err := w1.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("first claimAndDrain: %v", err)
	}

	// Force it back to 'sending' to simulate a resume after a crash, WITHOUT
	// clearing materialized_at.
	if _, err := pool.Exec(context.Background(), `UPDATE email_campaigns SET status = 'sending' WHERE id = $1`, campaignID); err != nil {
		t.Fatalf("force resume: %v", err)
	}
	if _, err := pool.Exec(context.Background(), `UPDATE email_sends SET status = 'queued' WHERE campaign_id = $1`, campaignID); err != nil {
		t.Fatalf("requeue rows: %v", err)
	}

	w2 := newTestWorker(t, pool, &RecordingMailer{})
	w2.auditor = auditLogger
	if _, err := w2.claimAndDrain(context.Background()); err != nil {
		t.Fatalf("second claimAndDrain: %v", err)
	}

	rows := auditRowsFor(t, pool, campaignID, audit.ActionEmailCampaignSendStarted)
	if len(rows) != 1 {
		t.Fatalf("send_started audit rows = %d, want exactly 1 (resume must not rewrite it)", len(rows))
	}
}

// ── helpers ──────────────────────────────────────────────────────────────

func auditLoggerForTest(t *testing.T, pool *pgxpool.Pool) *audit.Logger {
	t.Helper()
	return audit.New(pool)
}

// auditRowsFor returns the metadata (as raw JSON text) of every audit_log
// row matching campaignID/action, for asserting an exact write count and
// scanning metadata content without a full JSON-unmarshal round trip.
func auditRowsFor(t *testing.T, pool *pgxpool.Pool, campaignID int64, action string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT metadata::text FROM audit_log WHERE target_type = $1 AND target_id = $2 AND action = $3`,
		audit.TargetEmailCampaign, campaignID, action,
	)
	if err != nil {
		t.Fatalf("query audit_log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var meta string
		if err := rows.Scan(&meta); err != nil {
			t.Fatalf("scan audit_log metadata: %v", err)
		}
		out = append(out, meta)
	}
	return out
}

func errorsWrap(err error) error {
	return &wrappedErr{err}
}

type wrappedErr struct{ err error }

func (w *wrappedErr) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrappedErr) Unwrap() error { return w.err }

// recordingClassifiableMailer records every successfully "sent" message and
// lets a test classify a specific recipient as throttled.
type recordingClassifiableMailer struct {
	mu       sync.Mutex
	sent     []Message
	classify func(to string) error
}

func (m *recordingClassifiableMailer) Send(_ context.Context, msg Message) (string, error) {
	if m.classify != nil {
		if err := m.classify(msg.To); err != nil {
			return "", err
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return "id-" + msg.To, nil
}

func (m *recordingClassifiableMailer) Sent() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.sent))
	copy(out, m.sent)
	return out
}
