// worker.go is the send worker itself (#0045, PRD §6.6): the loop that
// turns queued email_sends rows into delivered mail, and the only place a
// campaign moves scheduled -> sending -> sent/failed. One in-process
// goroutine, started by `serve` behind SEND_WORKER_ENABLED — see NewWorker's
// doc comment for why this is not a separate binary or a systemd timer.
//
// # Shutdown is a signal, not a context cancellation
//
// Cancelling a context that a 'sent'-status UPDATE is riding on turns "SES
// accepted, we recorded it" into "SES accepted, we recorded nothing" — a
// guaranteed duplicate on every deploy. So Stop closes an internal channel
// and Run checks it between messages and between batches, never during one;
// the SES call and the status write that immediately follows it each run on
// their own detached context (context.WithTimeout(context.Background(),
// …)), the same precedent internal/handlers/subscribe.go's
// releaseSendClaim already set, so a SIGTERM arriving mid-message cannot
// leave a message accepted by SES and unrecorded in Postgres.
//
// # At-least-once, honestly (§6 of this issue's plan)
//
// attempts is incremented and committed in the SAME statement that claims a
// row to 'sending' (SendStore.ClaimRow), before the SES call; the 'sent'
// update commits after. The window between SES accepting a message and that
// update committing is real and cannot be closed — SES's SendEmail is not
// enlisted in our Postgres transaction and offers no client-supplied
// idempotency key. If the process dies in that window, the orphan sweep
// returns the row to 'queued' once claimed_at is older than orphanStaleAfter
// (below) and the message sends a second time. Because sends are sequential
// (one message in flight per worker, §7), at most one message can be in
// that window at any instant per worker — the duplicate is one message, not
// a batch. Marking 'sent' BEFORE the SES call would instead risk losing a
// message entirely on the same crash, which is strictly worse for a mailing
// list: a duplicate is a harmless annoyance; a silent drop is a broken
// promise nothing surfaces. This system deliberately does not achieve
// exactly-once.
//
// # The orphan sweep must not un-claim a live worker's row (#0122)
//
// The paragraph above describes ONE worker recovering from its OWN crash.
// Two Worker values can legitimately drain the same 'sending' campaign at
// once (ClaimResume takes no lock — see its own doc comment), and each
// independently runs the orphan sweep before draining. An unconditional
// sweep cannot tell "this row was abandoned by a process that already died"
// from "this row was claimed by the other worker a moment ago and is still
// in flight" — resetting the latter to 'queued' lets it be claimed and
// mailed a second time by the worker that swept it, while the original
// claimant, which never re-checks a row's status after its own claim
// succeeds, sends its own copy too. SendStore.OrphanSweep now only resets a
// row whose claimed_at predates orphanStaleAfter's window, which is sized
// to exceed how long a LIVE claim can possibly take (sendMessageTimeout +
// writeStatusTimeout), so a row claimed moments ago is never mistaken for
// one abandoned by a crash. The cutoff is computed entirely by Postgres
// (`claimed_at < now() - $2::interval`, worker_store.go's OrphanSweep) —
// Run hands over the orphanStaleAfter duration, not a Go-clock timestamp, so
// there is no dependency on the app host's and the database's clocks
// agreeing.
//
// # A resume pass that did nothing must not busy-loop (#0122, review pass 2)
//
// ClaimResume takes priority every poll (see Run's and claimAndDrain's own
// doc comments): while a campaign is 'sending' with a not-yet-stale orphan
// and nothing else queued, a resume pass claims nothing, sends nothing, and
// CompleteIfDone correctly refuses (the orphan still counts as 'sending').
// Before this section existed, claimAndDrain reported that pass as
// "processed" unconditionally, so Run's `if processed { continue }` re-ran
// it immediately with no ticker wait — a busy loop against Postgres for the
// entire orphanStaleAfter window (measured at ~28,000 transactions/sec).
// claimAndDrain, drainCampaign, and drainLoop now each report whether the
// pass did anything (materialized an audience, failed/completed the
// campaign, or claimed at least one row) rather than merely "ran without
// error"; Run only skips the poll wait when that is true.
package mailing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const (
	defaultPollInterval = 2 * time.Second
	defaultBatchSize    = 50
	defaultMaxSendRate  = 10

	// sendMessageTimeout and writeStatusTimeout bound the two detached-context
	// operations that must survive a SIGTERM arriving mid-message (see this
	// file's package doc comment). Neither is sized against measured machine
	// load (CLAUDE.md §5) — sendMessageTimeout covers one SES round trip
	// (including SESMailer's own internal throttling retries), and
	// writeStatusTimeout covers one single-row UPDATE.
	sendMessageTimeout = 30 * time.Second
	writeStatusTimeout = 5 * time.Second

	// backoffCap bounds the campaign-level exponential backoff (§7) SES
	// throttling triggers: 1s, 2s, 4s, 8s, 16s, capped at 30s, reset to 1s on
	// the next successful send.
	backoffCap = 30 * time.Second
)

// orphanStaleAfter bounds how long a row may legitimately sit in 'sending'
// before OrphanSweep treats it as abandoned by a crashed worker rather than
// held by a live one (#0122).
//
// # No batch flaw here — claimed_at is stamped per row, not per batch (#0295)
//
// #0284 found that outbox_worker.go's outboxOrphanStaleAfter was sized
// against ONE message's worst case while outbox.Store.ClaimDue stamps
// claimed_at ONCE for a WHOLE batch that then sends serially — so the last
// row in a batch had really been held far longer than the single-message
// derivation accounted for. #0295 was filed to check whether this worker's
// own orphanStaleAfter has the identical defect.
//
// It does not, checked directly rather than assumed: SendStore.ClaimBatch
// (worker_store.go) never changes a row's status or touches claimed_at at
// all — its SELECT ... FOR UPDATE SKIP LOCKED + RecheckEligibleTx commits
// with every still-eligible row still 'queued'. The actual atomic claim —
// status='sending', claimed_at=now() — is SendStore.ClaimRow, and it is the
// FIRST statement inside sendOne (below), called once per recipient,
// individually, at the exact moment that recipient's own processing
// begins. So batch size never enters this bound at all: each row's
// claimed_at reflects when ITS OWN send started, never an earlier
// batch-wide stamp, regardless of how many predecessors already ran ahead
// of it in drainLoop's serial loop. This is, architecturally, the per-row
// claimed_at re-stamp #0284's own doc comment names as the alternative to
// a large batch-derived window — worker.go has simply always worked this
// way, for the orthogonal reason ClaimBatch's own doc comment gives:
// holding one transaction open across a batch's SES calls would roll back
// every status update on a mid-batch crash, turning a one-message
// duplicate window into a batch-sized one.
// TestSendStore_ClaimBatch_LeavesRowsQueuedUntilClaimRow
// (worker_store_test.go) is the regression guard: if ClaimBatch is ever
// changed to claim atomically the way outbox's ClaimDue does, that test
// fails immediately — the signal this var would then need #0284/#0294's
// batch-aware treatment instead of the single-row one below.
//
// # The single-row bound
//
// A live worker can hold ONE row in 'sending' for at most
// sendMessageTimeout (the SES call's own detached, bounded sendCtx) plus
// writeStatusTimeout (MarkSent's own detached, bounded writeCtx
// immediately after) = 35s today, before MarkSent's UPDATE moves the row
// OUT of 'sending' — unlike outbox_worker.go, RecordEvent here runs AFTER
// that transition has already committed (sendOne, below: MarkSent then
// RecordEvent), so RecordEvent's own writeStatusTimeout cannot make this
// row look more stale than it is, and — because there is no batch-wide
// claim — it cannot delay a "next row" either, the way it does in
// outbox_worker.go. orphanStaleAfter doubles that legitimate 35s window
// rather than adding a flat margin, so ordinary scheduling jitter right at
// the boundary can never be mistaken for a crash: a full extra 35s of
// margin, not the ~1s (and, briefly, negative) margin
// outboxOrphanStaleAfter's now-superseded early attempts left themselves.
//
// # Still not a STRICT bound
//
// Worker.Run is started as `go sendWorker.Run(context.Background())`
// (cmd/opencircuit/main.go), and drainLoop's calls to ClaimRow itself, and
// — on the retryable/terminal-row failure paths — MarkFailedRow and
// MarkRetryOrFailed, all run on that plain ctx rather than a bounded
// detached context the way the success path's sendCtx/writeCtx are. A slow
// or hanging round trip on any of THOSE calls is therefore not
// deadline-bound at all — the same undeadlined-context residual #0284
// named for outboxOrphanStaleAfter. No expression over sendMessageTimeout
// and writeStatusTimeout is ever a strict bound for that reason;
// tightening it would mean giving those calls their own detached,
// timeout-bounded contexts, which is a real, separate change outside this
// doc comment's scope — worth its own issue if it is ever wanted.
//
// A var, not a const, so a test can shrink it; NOT sized against measured
// machine load (CLAUDE.md §5) — it is sized against the two hard timeouts
// that already bound one send attempt, the same way workerCloseTimeout is
// sized against one in-flight message rather than a benchmark.
var orphanStaleAfter = 2 * (sendMessageTimeout + writeStatusTimeout)

// CampaignProgress is one snapshot of a campaign's send progress — the seam
// #0048 publishes over SSE. Total is fixed at materialization (#0044's plan
// requires a fixed denominator); Remaining is queued+sending; Skipped is its
// own bucket, never folded into Failed. JSON tags are load-bearing: this
// struct is marshaled verbatim (internal/handlers/campaign_progress.go, via
// encoding/json) into the SSE frame's data field, and web/src/lib/
// campaignProgress.ts's CampaignProgress type is keyed off these exact
// snake_case names. When a field is added here, add it there in the same
// commit —
// internal/handlers/campaign_progress_parity_test.go's
// TestCampaignProgressParity_KeySet (#0241) fails a test if you don't,
// in either direction: it reads this struct's json tags via reflection and
// campaignProgress.ts's interface from source, so a field added to only one
// side is caught rather than merely documented.
//
// # Why Status is carried in the snapshot
//
// Status is email_campaigns.status as of this publish ('sending', 'sent',
// 'failed', 'canceled'), read fresh by publishProgress. It is here because
// TERMINALITY IS NOT DERIVABLE FROM THE COUNTS. #0048's first attempt had the
// client infer "the send is over" from Remaining == 0, which is a true
// statement about rows in flight but is not implied by — and on the failure
// paths is actively contradicted by — the campaign reaching a terminal
// status: MarkFailedCampaign (worker_store.go) updates email_campaigns only
// and never touches email_sends, so physical_address_missing,
// reply_to_missing, and every terminal-class SES error leave the unsent rows
// 'queued' and publish Remaining > 0 on a campaign that has definitively
// stopped. A failed campaign legitimately has recipients who will never be
// mailed. Rather than force the counts to lie about that (resolving live
// rows to a resting state would be a #0045-owned semantic change, and would
// destroy the operator-facing fact of how many people were spared), the
// snapshot states the campaign's status outright and the client stops
// guessing.
type CampaignProgress struct {
	CampaignID int64  `json:"campaign_id"`
	Status     string `json:"status"`
	Total      int64  `json:"total"`
	Sent       int64  `json:"sent"`
	Failed     int64  `json:"failed"`
	Skipped    int64  `json:"skipped"`
	Remaining  int64  `json:"remaining"`
}

// ProgressPublisher is the seam #0048 supplies a broker-backed
// implementation of. nil until then; every call site in this file is
// nil-guarded.
type ProgressPublisher interface {
	PublishCampaignProgress(ctx context.Context, p CampaignProgress)
}

// Worker drains queued campaign sends at a throttled rate, refusing to run
// (or continue running) a campaign whose pre-send requirements are unmet.
// See NewWorker for construction and this file's package doc comment for
// its shutdown and idempotency guarantees.
type Worker struct {
	store    *SendStore
	audience *AudienceStore
	auditor  *audit.Logger
	mailer   Mailer
	render   CampaignRenderer
	settings SettingsReader
	progress ProgressPublisher
	// events records campaign_sent (#0126, PRD §6.11) after a successful
	// SES send. nil-tolerant, matching every other optional dependency in
	// this file — a nil events simply skips the write.
	events *subscribers.Store
	// stats and outbox back #0124's circuit breaker: stats reads the
	// campaign's running bounce/complaint rate (internal/mailing's own
	// CampaignStatsStore, campaign_stats.go); outbox enqueues the
	// admin_alert on trip (#0126's plan §7, built for exactly this
	// caller). Both nil-tolerant — see checkDeliveryHealth's and
	// enqueueDeliveryHealthAlert's doc comments for what a nil dependency
	// degrades to. Deliberately NOT optional in production wiring
	// (cmd/opencircuit/main.go always supplies both when the worker itself
	// is constructed) — nil-tolerance here exists for tests that don't
	// need the breaker, not as a sanctioned way to ship without it.
	stats      *CampaignStatsStore
	outbox     *outbox.Store
	adminEmail string

	baseURL, listDomain, fromAddr, replyTo string
	envMaxSendRate, batchSize              int

	pollInterval time.Duration
	sleep        func(context.Context, time.Duration) error
	log          *slog.Logger

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// WorkerDeps is NewWorker's construction argument.
type WorkerDeps struct {
	Store    *SendStore
	Audience *AudienceStore
	Audit    *audit.Logger // nil disables audit writes, matching every other handler's nil-tolerance
	Mailer   Mailer
	Render   CampaignRenderer // nil defaults to MarkdownCampaignRenderer{}
	Settings SettingsReader
	Progress ProgressPublisher // nil until #0048; every call site nil-guarded
	// Events records campaign_sent (#0126). nil disables the write,
	// matching Audit's own nil-tolerance above.
	Events *subscribers.Store
	// Stats and Outbox back #0124's circuit breaker — see Worker's own
	// field doc comments. AdminEmail is cfg.AdminEmail (ADMIN_EMAIL,
	// optional per CLAUDE.md §10 item 4); empty disables the alert enqueue
	// specifically (the breaker still trips and pauses the campaign either
	// way — the alert is a notification of that fact, not a precondition
	// for it).
	Stats      *CampaignStatsStore
	Outbox     *outbox.Store
	AdminEmail string

	BaseURL, ListDomain, FromAddr, ReplyTo string
	// EnvMaxSendRate is MAX_SEND_RATE — the deploy-level ceiling. BatchSize
	// is SEND_BATCH_SIZE.
	EnvMaxSendRate, BatchSize int

	// PollInterval defaults to 2s (PRD §6.6). Overridable so tests never
	// wait out a real poll tick.
	PollInterval time.Duration
	Log          *slog.Logger
}

// NewWorker constructs a Worker. It does not start it — call Run.
//
// # Why an in-process goroutine, not a separate binary
//
// #0048 publishes send progress over the SAME process's SSE broker
// (internal/events.Broker, an in-memory fan-out) — a separate worker process
// would have to reach that broker over the network, inventing an internal
// transport for a single-box deployment. The deploy target is one EC2
// instance with one systemd unit (CLAUDE.md §7); a second unit is a second
// thing to install, monitor, and forget to restart, and SEND_WORKER_ENABLED
// already exists precisely so that if the site is ever scaled to two
// instances, exactly one of them runs the worker — that is the scaling
// story, a separate binary is not needed to tell it. A systemd-timer/cron
// drain was also rejected: poll-per-minute latency on an operator-initiated
// action, and no place to hold the rate limiter's state.
//
// # STORAGE=json and MAILER_NOOP=true never reach this constructor
//
// internal/devstore has no subscribers/suppressions/campaigns backing at
// all, so cmd/opencircuit never calls NewWorker in dev mode — nil-guarded at
// the call site exactly like sesNotifyH. MAILER_NOOP=true similarly refuses
// to construct the worker at the cmd/opencircuit call site (logging one
// slog.Warn) rather than here: noOpMailingMailer.Send returns the literal
// message id "noop", and writing that into email_sends.ses_message_id would
// poison #0038's bounce/complaint join key and #0049's stats with rows that
// claim to have been delivered.
func NewWorker(deps WorkerDeps) (*Worker, error) {
	if deps.Store == nil {
		return nil, errors.New("mailing: worker requires a SendStore")
	}
	if deps.Audience == nil {
		return nil, errors.New("mailing: worker requires an AudienceStore")
	}
	if deps.Mailer == nil {
		return nil, errors.New("mailing: worker requires a Mailer")
	}
	if deps.Settings == nil {
		return nil, errors.New("mailing: worker requires a SettingsReader")
	}

	render := deps.Render
	if render == nil {
		render = MarkdownCampaignRenderer{}
	}
	pollInterval := deps.PollInterval
	if pollInterval <= 0 {
		pollInterval = defaultPollInterval
	}
	batchSize := deps.BatchSize
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	envMaxSendRate := deps.EnvMaxSendRate
	if envMaxSendRate <= 0 {
		envMaxSendRate = defaultMaxSendRate
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	return &Worker{
		store:          deps.Store,
		audience:       deps.Audience,
		auditor:        deps.Audit,
		mailer:         deps.Mailer,
		render:         render,
		settings:       deps.Settings,
		progress:       deps.Progress,
		events:         deps.Events,
		stats:          deps.Stats,
		outbox:         deps.Outbox,
		adminEmail:     deps.AdminEmail,
		baseURL:        deps.BaseURL,
		listDomain:     deps.ListDomain,
		fromAddr:       deps.FromAddr,
		replyTo:        deps.ReplyTo,
		envMaxSendRate: envMaxSendRate,
		batchSize:      batchSize,
		pollInterval:   pollInterval,
		sleep:          sleepWithContext,
		log:            log,
		stopCh:         make(chan struct{}),
		doneCh:         make(chan struct{}),
	}, nil
}

// Run blocks, polling every pollInterval, until Stop is called or ctx is
// done. Each pass claims at most one campaign (a resume takes priority over
// a fresh start) and drains it to completion before returning to the
// ticker — there is no concurrency across campaigns within one Worker.
//
// processed (claimAndDrain's return) means the pass did something —
// claimed, materialized, sent, completed, failed, or demoted a campaign —
// not merely "ran without error". A pass that resumed a campaign but found
// nothing yet claimable (a live orphan not yet stale, nothing queued) is
// NOT processed, and falls through to the ordinary pollInterval wait below
// instead of looping immediately — see this file's package doc comment,
// "A resume pass that did nothing must not busy-loop" (#0122).
func (w *Worker) Run(ctx context.Context) {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		processed, err := w.claimAndDrain(ctx)
		if err != nil {
			w.log.Error("mailing: send worker pass failed", "err", err)
		}
		if processed {
			continue
		}

		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-time.After(w.pollInterval):
		}
	}
}

// Stop signals Run to stop claiming new work and blocks until Run has
// returned or ctx's deadline elapses, whichever comes first — see this
// file's package doc comment for why this is a signal, not a context
// cancellation. Safe to call more than once.
func (w *Worker) Stop(ctx context.Context) error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	select {
	case <-w.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// claimAndDrainHook, when non-nil, is called at the start of every
// claimAndDrain pass — a test-only counting seam (the same nil-in-production
// pattern as sendPreCrashHook/sendPostCrashHook above) for
// TestWorker_Run_ResumeWithLiveOrphan_DoesNotBusyLoop (#0122, review pass 2),
// which asserts a BOUNDED PASS COUNT over a fixed window to prove Run no
// longer busy-loops on a resume that did nothing. Always nil in production.
var claimAndDrainHook func()

// claimAndDrain performs one poll pass: resume a campaign already 'sending'
// if one exists, else evaluate the next due 'scheduled' campaign against the
// gate and either demote it or claim and drain it. Returns processed=true
// when this pass did something real (claimed, materialized, sent, completed,
// failed, or demoted a campaign) so Run can poll again immediately instead
// of waiting a full tick. A resume that found nothing yet claimable — a live
// orphan not yet stale, nothing queued — is processed=false: see this file's
// package doc comment, "A resume pass that did nothing must not busy-loop"
// (#0122).
func (w *Worker) claimAndDrain(ctx context.Context) (bool, error) {
	if claimAndDrainHook != nil {
		claimAndDrainHook()
	}
	resumed, err := w.store.ClaimResume(ctx)
	if err != nil {
		return false, err
	}
	if resumed != nil {
		return w.drainCampaign(ctx, resumed)
	}

	candidateID, ok, err := w.store.PeekScheduledCandidate(ctx)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}

	pre, err := w.store.GatherPreflight(ctx, candidateID)
	if err != nil {
		if errors.Is(err, ErrCampaignNotFound) {
			// Deleted or otherwise vanished between the peek and this read
			// (not possible via any route this codebase exposes today, but
			// not worth crashing the worker over).
			return false, nil
		}
		return false, err
	}

	result := Preflight(pre)
	if !result.OK() {
		demoted, derr := w.store.DemoteToDraft(ctx, candidateID)
		if derr != nil {
			return false, derr
		}
		if demoted {
			// The authoritative gate refusing the campaign: CLAUDE.md §9's
			// "not bypassable from the UI" property. Written only by
			// whichever worker actually performed the demotion — a
			// concurrent second worker that lost the DemoteToDraft race
			// (demoted=false) must not write a duplicate audit row.
			w.auditSendRefused(ctx, candidateID, result)
		}
		return demoted, nil
	}

	started, err := w.store.ClaimStart(ctx, candidateID)
	if err != nil {
		return false, err
	}
	if started == nil {
		// A concurrent worker already claimed or demoted this campaign
		// between our gate evaluation and this UPDATE.
		return false, nil
	}
	return w.drainCampaign(ctx, started)
}

// drainCampaign runs the orphan sweep, materializes the audience exactly
// once, and drains every queued row to completion. c.MaterializedAt nil
// means this is the first claim (fresh start or a resume of a crash that
// happened before materialization finished); non-nil means either a normal
// resume or a fresh start whose materialization already completed in an
// earlier attempt. The bool return reports whether this call did anything
// real — see claimAndDrain's doc comment (#0122).
func (w *Worker) drainCampaign(ctx context.Context, c *claimedCampaign) (bool, error) {
	// The cutoff is computed by Postgres itself (worker_store.go's
	// OrphanSweep: `claimed_at < now() - $2::interval`), not by comparing
	// this process's clock against claimed_at — see orphanStaleAfter's doc
	// comment and this file's package doc comment (#0122).
	if _, err := w.store.OrphanSweep(ctx, c.ID, orphanStaleAfter); err != nil {
		return false, err
	}

	didWork := false
	if c.MaterializedAt == nil {
		aud := Audience{Mode: c.AudienceMode, InterestIDs: c.InterestIDs}
		res, err := w.audience.Materialize(ctx, c.ID, aud)
		if err != nil {
			return false, err
		}
		didStamp, err := w.store.SetMaterializedAt(ctx, c.ID)
		if err != nil {
			return false, err
		}

		total, _, _, _, _, _, err := w.store.CountEmailSends(ctx, c.ID)
		if err != nil {
			return false, err
		}
		didWork = true

		if didStamp {
			// campaign.send_started is written ONLY on the start claim,
			// never on a resume — the worker that wins SetMaterializedAt's
			// race is the one that gets to write it, so a campaign
			// restarted five times still has exactly one row.
			w.auditSendStarted(ctx, c, res, total)
		}

		if total == 0 {
			// Preview said non-zero seconds earlier; zero rows here is
			// genuinely anomalous (§4 of this issue's plan).
			return true, w.failCampaign(ctx, c.ID, "empty_audience")
		}
	}

	loopWork, err := w.drainLoop(ctx, c)
	return didWork || loopWork, err
}

// drainLoop re-reads physical_address and reply-to fresh for this campaign
// (never cached at boot — CLAUDE.md §9) and processes batches until nothing
// remains queued, the drain is stopped by Stop, or a terminalCampaign-class
// error halts it. The bool return reports whether this call did anything
// real: failed or completed the campaign, or claimed at least one batch row
// — never true for the "nothing queued, orphan not yet stale, campaign not
// done" pass that motivated this return value (#0122; see this file's
// package doc comment).
func (w *Worker) drainLoop(ctx context.Context, c *claimedCampaign) (bool, error) {
	physicalAddress, settingsErr := w.settings.GetSetting(ctx, settingPhysicalAddress)
	if settingsErr != nil || strings.TrimSpace(physicalAddress) == "" {
		// Refusing is always correct (CLAUDE.md §9) — this branch is what
		// makes a resume whose address went blank after the original start
		// claim stop rather than send non-compliant mail.
		return true, w.failCampaign(ctx, c.ID, "physical_address_missing")
	}
	if strings.TrimSpace(w.replyTo) == "" {
		return true, w.failCampaign(ctx, c.ID, "reply_to_missing")
	}

	fromHeader := w.resolveFromHeader(ctx)
	backoff := time.Second
	didWork := false

	for {
		select {
		case <-w.stopCh:
			return didWork, nil
		default:
		}

		// #0269: evaluate the breaker at the TOP of the loop too, not only
		// at the end of a batch (below). Without this, a Resume that lands
		// on an already-tripped rate put a full w.batchSize on the wire
		// before the end-of-batch check ever re-ran — measured: 300
		// recipients at a 100% bounce rate with batchSize=100 tripped at
		// 100, then one Resume sent exactly +100 more before re-pausing.
		// Checking here first means a resume into a still-bad rate sends
		// zero further messages instead of one more batch.
		//
		// gateOnQueuedWork=true: this call runs BEFORE ClaimBatch, so it is
		// the one whose only remaining job on a fully-drained campaign is to
		// observe an empty batch and call CompleteIfDone below. Without the
		// gate, a trip that lands exactly on the final batch would re-trip
		// on every future Resume forever — CompleteIfDone is never reached
		// because this check always fires first, and nothing further is
		// ever sent to move the (already-final) rate. Gating on "is
		// anything still queued" lets that terminal case fall through to
		// ClaimBatch → empty → CompleteIfDone instead, while a genuine
		// still-bad rate WITH real work queued still stops here, before
		// claiming a single row (see delivery_health.go's package doc).
		if stopped, err := w.checkAndMaybePauseDeliveryHealth(ctx, c, true); stopped {
			return true, err
		}

		sendRate := w.effectiveSendRate(ctx)
		limiter := rate.NewLimiter(rate.Limit(sendRate), 1)

		recipients, err := w.store.ClaimBatch(ctx, c.ID, w.batchSize)
		if err != nil {
			return didWork, err
		}

		if len(recipients) == 0 {
			done, err := w.store.CompleteIfDone(ctx, c.ID)
			if err != nil {
				return didWork, err
			}
			if done {
				w.auditSendCompleted(ctx, c.ID)
				w.publishProgress(ctx, c.ID)
				didWork = true
			}
			// done=false here is exactly "resumed and did nothing": nothing
			// queued, and the campaign isn't actually finished (a live
			// orphan not yet stale is still counted 'sending'). didWork
			// stays false unless an earlier batch in this same call already
			// set it — Run must fall back to pollInterval, not spin.
			return didWork, nil
		}

		didWork = true
		for _, r := range recipients {
			select {
			case <-w.stopCh:
				return didWork, nil
			default:
			}
			if err := limiter.Wait(ctx); err != nil {
				return didWork, err
			}

			outcome, sendErr := w.sendOne(ctx, c, r, physicalAddress, fromHeader)
			switch outcome {
			case sendOutcomeSent:
				backoff = time.Second
			case sendOutcomeThrottled:
				if serr := w.sleep(ctx, backoff); serr != nil {
					return didWork, serr
				}
				backoff *= 2
				if backoff > backoffCap {
					backoff = backoffCap
				}
			case sendOutcomeTerminalCampaign:
				if _, rerr := w.store.ReleaseRow(ctx, r.SendID); rerr != nil {
					w.log.Error("mailing: releasing row after terminal campaign error", "send_id", r.SendID, "err", rerr)
				}
				return didWork, w.failCampaignSES(ctx, c.ID, sendErr)
			case sendOutcomeAborted:
				// A test fault-injection hook simulated the process dying at
				// a specific instruction boundary (see sendPreCrashHook /
				// sendPostCrashHook's doc comments) — return immediately, as
				// a real crash would, leaving whatever state the row is
				// already in. Never set outside a test.
				return didWork, sendErr
			}
		}

		w.publishProgress(ctx, c.ID)

		// #0124's circuit breaker (PRD §6.9), also checked once per drained
		// batch — the same cadence publishProgress already uses, not once
		// per message: bounce/complaint events arrive asynchronously via
		// SES's webhook (internal/handlers/ses_notifications.go), often
		// well after the send that triggered them, so checking more often
		// than once per batch buys no earlier detection, only more queries
		// against a rate that hasn't moved. This end-of-batch check catches
		// a rate that trips DURING the batch just sent; the top-of-loop
		// check above catches a rate that was already tripped BEFORE this
		// batch started (in particular, right after a Resume) — #0269.
		//
		// gateOnQueuedWork=false, deliberately unlike the top-of-loop call
		// above: a trip discovered here, right as the batch just sent
		// happens to be the last one, still pauses and alerts even though
		// nothing remains queued — the operator needs to see that the send
		// finished with a bad rate. It is the RESUME afterward that must not
		// get stuck re-tripping (that is the top-of-loop call's job, and why
		// it alone is gated) — #0269's review.
		if stopped, err := w.checkAndMaybePauseDeliveryHealth(ctx, c, false); stopped {
			return true, err
		}
	}
}

// checkAndMaybePauseDeliveryHealth runs checkDeliveryHealth once for c and,
// if it tripped, pauses the campaign — the single implementation drainLoop's
// two call sites (top of the loop, before ClaimBatch; end of every batch)
// both funnel through, so they can never drift in what "tripped" means or
// how a trip is handled. stopped=true means drainLoop must return
// immediately; err then carries pauseCampaignDeliveryHealth's own result
// (nil on the ordinary path). A health-check query error is deliberately NOT
// reported as stopped=true: that would turn "the database had a hiccup"
// into "safety checking quietly stopped" — it is logged and treated as a
// pass instead, exactly as drainLoop did inline before #0269 split this out.
//
// gateOnQueuedWork distinguishes the two call sites (#0269's review, fixing
// a defect the first version of this split introduced): when true (the
// top-of-loop call), a trip is only actionable if something is still
// queued — with Queued==0 there is nothing further this trip could stop,
// and pausing anyway would strand the campaign forever, because a later
// Resume would re-observe the very same already-final rate and re-pause
// without ever sending anything to move it. Standing down here lets
// drainLoop fall through to ClaimBatch, find nothing, and route to
// CompleteIfDone instead. When false (the end-of-batch call), a trip pauses
// unconditionally, even if the batch just sent was the last one queued —
// the operator still needs to see that the send finished over threshold.
func (w *Worker) checkAndMaybePauseDeliveryHealth(ctx context.Context, c *claimedCampaign, gateOnQueuedWork bool) (stopped bool, err error) {
	tripped, herr := w.checkDeliveryHealth(ctx, c.ID)
	if herr != nil {
		w.log.Error("mailing: delivery-health check failed", "campaign_id", c.ID, "err", herr)
		return false, nil
	}
	if !tripped.Tripped {
		return false, nil
	}
	if gateOnQueuedWork && tripped.Queued == 0 {
		return false, nil
	}
	return true, w.pauseCampaignDeliveryHealth(ctx, c.ID, c.Subject, tripped)
}

// sendOutcome classifies what happened to one recipient's send attempt, for
// drainLoop's dispatch.
type sendOutcome int

const (
	// sendOutcomeSkipped covers every case where nothing was sent and the
	// row is already in its correct resting state: the per-row claim lost
	// a race, or a retryable failure updated the row itself
	// (queued-for-retry or failed-at-3-attempts).
	sendOutcomeSkipped sendOutcome = iota
	sendOutcomeSent
	sendOutcomeTerminalRow
	sendOutcomeThrottled
	sendOutcomeTerminalCampaign
	// sendOutcomeAborted is produced only by the test fault-injection hooks
	// below; production code never returns it.
	sendOutcomeAborted
)

// errAbortedForTest is the sentinel drainLoop returns when a test hook
// simulates the process dying mid-send. Package-private and referenced only
// by the two hooks and their tests.
var errAbortedForTest = errors.New("mailing: simulated crash for test")

// sendPreCrashHook and sendPostCrashHook are deliberate fault-injection
// seams for TestWorker_AbortBeforeSESCall_ResumesWithNoDuplicate and
// TestWorker_AbortBetweenSESAcceptAndStatusWrite_ResumesWithOneDuplicate
// (this issue's plan §11, criterion 4). Both are nil in production.
//
// sendPreCrashHook, when non-nil, is called immediately after a row is
// claimed (attempts already incremented and committed) but BEFORE any SES
// call — simulating a crash that never reaches the network. The row stays
// 'sending'; a resume's orphan sweep returns it to 'queued' and it is sent
// exactly once, overall.
//
// sendPostCrashHook, when non-nil, is called immediately AFTER a successful
// SES call but BEFORE the 'sent' status write — the exact instruction
// boundary a SIGKILL would hit that this file's package doc comment
// describes as the one unavoidable duplicate-delivery window. The message
// is already recorded as delivered (the test's Mailer has it in hand); the
// row still shows 'sending' until a resume's orphan sweep and a second send
// attempt duplicate it.
//
// A deliberate deviation from the acceptance criterion's literal "verified
// by killing the process mid-send in a test" — an in-process abort at the
// exact commit boundary tests the same invariant deterministically, where a
// real SIGKILL only adds timing nondeterminism about WHERE the process
// died (CLAUDE.md §5 warns against exactly that class of flaky test). See
// this issue's plan §11, criterion 4, for the two-worker concurrent test
// that additionally exercises a genuinely concurrent second claimant.
var (
	sendPreCrashHook  func(sendID int64) bool
	sendPostCrashHook func(sendID int64) bool
)

// sendOne claims one row and, if the claim succeeds, sends it. It always
// leaves the row in a terminal-or-requeued state (or, for
// sendOutcomeTerminalCampaign, leaves it 'sending' for the caller to
// release) — never returns with the row silently stuck.
func (w *Worker) sendOne(ctx context.Context, c *claimedCampaign, r Recipient, physicalAddress, fromHeader string) (sendOutcome, error) {
	_, claimed, err := w.store.ClaimRow(ctx, r.SendID)
	if err != nil {
		w.log.Error("mailing: claiming send row", "send_id", r.SendID, "err", err)
		return sendOutcomeSkipped, nil
	}
	if !claimed {
		// Another worker claimed this row first — see #0044's
		// "increment attempts before the SES call" requirement and this
		// issue's plan §4 for why this statement IS the mutual exclusion
		// among ClaimRow calls. That claim held only so long as nothing
		// else could move a row back to 'queued' out from under a live
		// claimant — #0122 found that OrphanSweep did exactly that,
		// unconditionally, defeating this statement's exclusion the moment
		// a second worker's resume raced a first worker's in-flight send.
		// OrphanSweep now only resets rows stale enough that no live
		// worker could still hold them (worker_store.go's OrphanSweep,
		// orphanStaleAfter above), which is what makes this statement true
		// again rather than merely locally true.
		return sendOutcomeSkipped, nil
	}

	if sendPreCrashHook != nil && sendPreCrashHook(r.SendID) {
		return sendOutcomeAborted, errAbortedForTest
	}

	if strings.TrimSpace(r.ManageToken) == "" {
		// A message with a dead unsubscribe link is a compliance failure
		// (§8 of this issue's plan) — fail loudly, no SES call. The row's
		// stored reason is written for the admin who reads it on
		// CampaignStats.svelte's failed-sends list, not for a developer —
		// see adminEligibilityFailureMessage's doc comment. Log the
		// invariant violation itself too — RecheckEligibleTx's query
		// should never hand back a row with a blank token, so this line is
		// what lets an engineer notice that happened at all, since the
		// admin-facing string deliberately carries no diagnostic detail
		// (#0182).
		w.log.Error("mailing: recipient row has empty manage token from eligibility recheck", "send_id", r.SendID)
		if _, err := w.store.MarkFailedRow(ctx, r.SendID, adminEligibilityFailureMessage); err != nil {
			w.log.Error("mailing: marking empty-token row failed", "send_id", r.SendID, "err", err)
		}
		return sendOutcomeTerminalRow, nil
	}

	html, text, rerr := w.render.Campaign(CampaignRenderInput{
		Subject:         c.Subject,
		Preheader:       derefOrEmpty(c.Preheader),
		BodyMarkdown:    c.BodyMD,
		BaseURL:         w.baseURL,
		ManageToken:     r.ManageToken,
		PhysicalAddress: physicalAddress,
	})
	if rerr != nil {
		// The admin-facing string is deliberately fixed and generic (see
		// adminRenderFailureMessage's doc comment) — it does not carry
		// rerr, so without this line a render panic, ErrMissingBaseURL,
		// and a Markdown-conversion failure would be indistinguishable to
		// an engineer too, not just to the admin reading CampaignStats.svelte
		// (#0182).
		w.log.Error("mailing: rendering campaign for recipient", "send_id", r.SendID, "err", rerr)
		if _, err := w.store.MarkFailedRow(ctx, r.SendID, adminRenderFailureMessage(rerr)); err != nil {
			w.log.Error("mailing: marking render-failed row failed", "send_id", r.SendID, "err", err)
		}
		return sendOutcomeTerminalRow, nil
	}

	msg := Message{
		To:       r.Email,
		From:     fromHeader,
		Subject:  c.Subject,
		HTMLBody: html,
		TextBody: text,
		// Built fresh, per recipient, from THIS recipient's own token — see
		// CampaignHeaders' own doc comment for why hoisting this above the
		// loop (or reusing one recipient's token) is a compliance bug, not
		// a style choice.
		Headers: CampaignHeaders(w.baseURL, w.listDomain, r.ManageToken),
	}

	// The SES call itself runs on a detached context — see this file's
	// package doc comment.
	sendCtx, cancel := context.WithTimeout(context.Background(), sendMessageTimeout)
	messageID, sendErr := w.mailer.Send(sendCtx, msg)
	cancel()

	if sendErr == nil {
		if sendPostCrashHook != nil && sendPostCrashHook(r.SendID) {
			// The message is already recorded as delivered (the mailer has
			// it), but the 'sent' write below never runs — the exact crash
			// window this file's package doc comment describes.
			return sendOutcomeAborted, errAbortedForTest
		}
		writeCtx, wcancel := context.WithTimeout(context.Background(), writeStatusTimeout)
		_, werr := w.store.MarkSent(writeCtx, r.SendID, messageID)
		wcancel()
		if werr != nil {
			w.log.Error("mailing: marking sent row", "send_id", r.SendID, "err", werr)
		} else if w.events != nil {
			// #0126: campaign_sent — "a campaign message was accepted by
			// SES for this address" (PRD §6.11). Own detached, short
			// context, matching writeStatusTimeout's own bound; a failure
			// here is logged, not retried or surfaced to the caller — the
			// send itself already succeeded and committed.
			eventCtx, ecancel := context.WithTimeout(context.Background(), writeStatusTimeout)
			subID := r.SubscriberID
			if err := w.events.RecordEvent(eventCtx, subscribers.Event{
				SubscriberID: &subID,
				Email:        r.Email,
				Action:       subscribers.ActionCampaignSent,
				CampaignID:   &c.ID,
			}); err != nil {
				w.log.Error("mailing: recording campaign_sent event failed", "send_id", r.SendID, "err", err)
			}
			ecancel()
		}
		return sendOutcomeSent, nil
	}

	switch classifySendError(sendErr) {
	case sendClassTerminalCampaign:
		// Row release is the caller's job — it happens AFTER
		// failCampaignSES's own read of the current counts, so "remaining"
		// in that audit row still counts this recipient as not-yet-sent
		// however the caller chooses to release it.
		return sendOutcomeTerminalCampaign, sendErr
	case sendClassTerminalRow:
		if _, err := w.store.MarkFailedRow(ctx, r.SendID, adminSendErrorMessage(w.log, r.SendID, sendErr)); err != nil {
			w.log.Error("mailing: marking rejected row failed", "send_id", r.SendID, "err", err)
		}
		return sendOutcomeTerminalRow, nil
	default: // retryable
		if _, err := w.store.MarkRetryOrFailed(ctx, r.SendID, adminSendErrorMessage(w.log, r.SendID, sendErr)); err != nil {
			w.log.Error("mailing: retry-or-fail on send row", "send_id", r.SendID, "err", err)
		}
		if isThrottlingError(sendErr) {
			return sendOutcomeThrottled, nil
		}
		return sendOutcomeSkipped, nil
	}
}

// sendClass is classifySendError's result vocabulary (§8 of this issue's
// plan).
type sendClass int

const (
	sendClassRetryable sendClass = iota
	sendClassTerminalRow
	sendClassTerminalCampaign
)

// classifySendError maps an error from Mailer.Send to §8's table, using
// errors.As against sesv2/types exclusively — never string matching, so a
// wrapped error (fmt.Errorf("...: %w", sesErr)) still classifies correctly.
// Anything this function does not recognize (context deadline, a network
// error, a future SES exception type) is treated as retryable: unknown
// means assume transient, per §8.
func classifySendError(err error) sendClass {
	var tooMany *types.TooManyRequestsException
	if errors.As(err, &tooMany) {
		return sendClassRetryable
	}
	var limitExceeded *types.LimitExceededException
	if errors.As(err, &limitExceeded) {
		return sendClassRetryable
	}
	var sendingPaused *types.SendingPausedException
	if errors.As(err, &sendingPaused) {
		return sendClassTerminalCampaign
	}
	var acctSuspended *types.AccountSuspendedException
	if errors.As(err, &acctSuspended) {
		return sendClassTerminalCampaign
	}
	var mailFromNotVerified *types.MailFromDomainNotVerifiedException
	if errors.As(err, &mailFromNotVerified) {
		return sendClassTerminalCampaign
	}
	var notFound *types.NotFoundException
	if errors.As(err, &notFound) {
		return sendClassTerminalCampaign
	}
	var badRequest *types.BadRequestException
	if errors.As(err, &badRequest) {
		return sendClassTerminalCampaign
	}
	var rejected *types.MessageRejected
	if errors.As(err, &rejected) {
		return sendClassTerminalRow
	}
	if errors.Is(err, ErrNoMessageID) {
		return sendClassTerminalRow
	}
	return sendClassRetryable
}

// isThrottlingError reports whether err is specifically SES's throttling
// exception — the one class that triggers §7's campaign-level backoff, as
// opposed to LimitExceededException (a daily quota that "may pass
// tomorrow", not something an in-process backoff fixes).
func isThrottlingError(err error) bool {
	var tooMany *types.TooManyRequestsException
	return errors.As(err, &tooMany)
}

// adminEligibilityFailureMessage is written to email_sends.error (rendered
// verbatim in CampaignStats.svelte's failed-sends list) for a row whose
// recheck-eligible ManageToken came back blank — an invariant that
// RecheckEligibleTx's own query should never let through, so seeing this
// means our own bookkeeping produced a row it should not have. Not the
// recipient's mail system's doing, so unlike adminSendErrorMessage below it
// carries no raw diagnostic text — an admin cannot act on "eligibility
// recheck" or "manage token" (#0182).
const adminEligibilityFailureMessage = "Not sent — a required unsubscribe link could not be generated for this recipient, so the message was withheld rather than sent without one."

// adminRenderFailureMessage maps a CampaignRenderer.Campaign error to text
// for the same admin-facing surface as adminEligibilityFailureMessage.
// Every error RenderCampaign can return — ErrMissingManageToken,
// ErrMissingBaseURL, a wrapped Markdown-conversion failure, or a recovered
// panic — originates in our own rendering code, not in anything the
// recipient's mail system said, so like the eligibility case above it is
// replaced rather than passed through raw (#0182).
func adminRenderFailureMessage(err error) string {
	return "Not sent — this message's content could not be rendered for this recipient. The campaign draft or server configuration needs attention before this row is retried."
}

// sesWrapPrefix is the exact text ses_mailer.go's Send wraps around whatever
// the AWS SDK (or, for the retry-backoff wait, the local context) produced —
// emitted at exactly three places (SendEmail-error, sleep-error, and
// final-attempt returns), all through this constant so the literal cannot
// drift from what adminSendErrorMessage's doc comment and worker_test.go's
// sesResponseError describe (#0190 — ses_mailer.go used to repeat the
// literal at each of the three call sites instead of referencing this
// constant, so nothing tied them together). adminSendErrorMessage itself no
// longer inspects this prefix to classify an error (#0188 — see that
// function's doc comment for why matching on err.Error() text was itself
// the defect: SDK errors nest, so stripping a literal prefix off the
// formatted string left the nested "operation error …, https response
// error …" scaffolding behind it) — it is purely a wrapping detail now, not
// a discriminator.
const sesWrapPrefix = "mailing: sending via SES: "

// adminSendBuildFailureMessage is written to email_sends.error for
// SESMailer.Send's two argument-guard errors (msg.To == "", an empty
// HTML/text body) — a should-never-happen invariant in our own call
// construction (worker.go builds msg.To from r.Email, already checked
// non-blank by ClaimRow's query, and the body from RenderCampaign's already
// successful output), not anything SES said. Effectively unreachable from
// sendOne today, kept for the same reason adminEligibilityFailureMessage is:
// if a should-never-happen guard ever fires, it should not describe itself
// in this package's own vocabulary (#0182).
const adminSendBuildFailureMessage = "Not sent — this message could not be built for this recipient. The campaign draft or server configuration needs attention before this row is retried."

// adminNoMessageIDMessage is written to email_sends.error when SES reports
// success but the response carries no MessageId (ErrNoMessageID) — SES's
// behavior, but not SES's wording: ErrNoMessageID is an errors.New in this
// package, not text the AWS SDK produced, so it gets its own admin sentence
// rather than being passed through adminSendErrorMessage's raw-text
// passthrough (#0182).
const adminNoMessageIDMessage = "Not sent — SES accepted this message but did not report a delivery id, so it cannot be confirmed or tracked."

// adminSendTimedOutMessage is written to email_sends.error when the context
// deadline set around a SES call expires during the throttling-retry
// backoff wait (ses_mailer.go's sleep between attempts). That local timeout
// is wrapped in the same sesWrapPrefix as SES's own errors, since it
// happens inside SESMailer.Send's retry loop — but the "context deadline
// exceeded" / "context canceled" text underneath it is Go runtime
// vocabulary, not anything SES said, so it is replaced rather than passed
// through raw (#0182).
const adminSendTimedOutMessage = "Not sent — the send attempt did not complete before its time limit."

// adminSendUnreachableMessage is written to email_sends.error when a
// send error wraps *smithy.OperationError but never received (or never got
// far enough to receive) a service response to deserialize — a
// credential-chain refresh failure, a DNS/dial failure, or any other
// transport-level fault the AWS SDK reports through *smithy.OperationError
// without a smithy.APIError underneath it (smithy-go's Client.Invoke wraps
// every operation error in OperationError; a genuine SES response —
// modeled or not — deserializes into something that also implements
// APIError, everything else does not). Deliberately distinct from the raw
// SES-rejection text kept below: a rejection reason is about THIS
// recipient or campaign and an admin debugging a bad campaign wants it
// verbatim, while a credential/transport failure is about the deployment
// and repeating AWS/Go internals ("failed to refresh cached credentials",
// a bare dial error) would point an admin at the wrong thing entirely —
// the distinction #0185 asked this function to draw (#0182's own Notes
// warned against flattening every failure to one shape, and this is the
// case that warning was about).
const adminSendUnreachableMessage = "Not sent — this attempt could not reach SES at all (a network or credentials problem on our end, not anything about this recipient or campaign). If this keeps happening, an engineer needs to check the server's AWS configuration."

// adminSendErrorMessage maps a Mailer.Send error to text for
// email_sends.error. The discriminator is deliberately NOT
// SESMailer.Send's sesWrapPrefix — a Mailer other than SESMailer (every
// test double in this package included) can legitimately return a bare
// AWS SDK error with no such wrapping, and gating on the prefix's presence
// first made every one of those look "unrecognized" (#0185 caught this in
// its own first draft: TestWorker_ThreeRetryableFailuresMarkRowFailed's
// bare *types.TooManyRequestsException logged as unenumerated on every
// attempt). Instead:
//
//  1. This package's own sentinels (ErrNoMessageID, ErrEmptyRecipient,
//     ErrEmptyBody) and a context timeout during SESMailer.Send's retry
//     backoff are matched explicitly by errors.Is — never inferred from
//     "no prefix" — the same way #0182 bounced once for assuming a class
//     instead of enumerating it.
//  2. Anything else that is (or wraps) a smithy.APIError — every modeled
//     sesv2/types exception, and smithy.GenericAPIError for one that
//     isn't modeled yet — is a genuine response from SES about this
//     specific send: a rejection reason, a quota, a throttling notice.
//     Genuinely useful to an admin, so it is built from apiErr.ErrorCode()
//     and apiErr.ErrorMessage() directly — NOT err.Error() with
//     sesWrapPrefix stripped off the front (#0188): production nests a
//     modeled exception inside *smithy.OperationError and, over HTTP,
//     inside *smithy.(transport/http).ResponseError too, each of which
//     prepends its own "operation error …, " / "https response error
//     StatusCode: …, RequestID: …, " text to Error()'s formatted string.
//     Stripping only our own sesWrapPrefix left every layer between it and
//     the modeled exception intact — exactly the "implementation detail an
//     admin cannot act on" this function exists to keep out, and it only
//     ever showed up in production, because every test before #0188
//     exercised a *bare* modeled exception (Error() == "Code: Message",
//     nothing to strip). apiErr.ErrorCode()+": "+apiErr.ErrorMessage()
//     reaches the same text regardless of how deep the modeled exception is
//     nested, because it reads the exception's own fields rather than
//     re-parsing anything's Error() string.
//  3. Anything else that is (or wraps) *smithy.OperationError without a
//     smithy.APIError inside it is a transport-level failure from inside
//     the AWS SDK call that never resolved to an API response at all — a
//     credential-chain refresh failure, a DNS/dial failure — mapped to
//     adminSendUnreachableMessage, since that describes a server/deployment
//     problem, not anything about this recipient (see its own doc comment
//     for why that distinction matters). Type-based via errors.As, like
//     every branch above it (#0188 — a prior draft discriminated this one
//     case on sesWrapPrefix's string presence instead, the one branch in
//     this function not keyed off the error's shape).
//  4. Anything left is an error shape this function was not written
//     against — logged loudly, with send_id for correlation against the
//     rest of this worker's logging, rather than silently mislabeled.
func adminSendErrorMessage(log *slog.Logger, sendID int64, err error) string {
	if errors.Is(err, ErrNoMessageID) {
		return adminNoMessageIDMessage
	}
	if errors.Is(err, ErrEmptyRecipient) || errors.Is(err, ErrEmptyBody) {
		return adminSendBuildFailureMessage
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return adminSendTimedOutMessage
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		// A modeled exception's Message is only set when the response body
		// carried one (awsRestjson1_deserializeDocumentMessageRejected sets
		// it only when the JSON body has "message"/"Message", with no
		// default) — reachable in production, not just in a test double
		// (#0190). Fall back to the bare code rather than leave a dangling
		// "Code: " with nothing after the colon.
		if apiErr.ErrorMessage() == "" {
			return apiErr.ErrorCode()
		}
		return apiErr.ErrorCode() + ": " + apiErr.ErrorMessage()
	}

	var opErr *smithy.OperationError
	if errors.As(err, &opErr) {
		return adminSendUnreachableMessage
	}

	// Should not happen: every case this function was written against is
	// handled above. An engineer needs to know this function was handed a
	// return it was never enumerated for, rather than have it silently
	// mislabeled "could not be built" (#0185).
	log.Error("mailing: adminSendErrorMessage saw an unrecognized error", "send_id", sendID, "err", err)
	return adminSendBuildFailureMessage
}

// sesFailureReason names a terminalCampaign-class error for
// campaign.send_failed's metadata — a short, stable string, not the raw AWS
// error text.
func sesFailureReason(err error) string {
	var sendingPaused *types.SendingPausedException
	if errors.As(err, &sendingPaused) {
		return "sending_paused"
	}
	var acctSuspended *types.AccountSuspendedException
	if errors.As(err, &acctSuspended) {
		return "account_suspended"
	}
	var mailFromNotVerified *types.MailFromDomainNotVerifiedException
	if errors.As(err, &mailFromNotVerified) {
		return "mail_from_domain_not_verified"
	}
	var notFound *types.NotFoundException
	if errors.As(err, &notFound) {
		return "configuration_set_not_found"
	}
	var badRequest *types.BadRequestException
	if errors.As(err, &badRequest) {
		return "bad_request"
	}
	return "ses_error"
}

// failCampaign stops the drain and moves the campaign to 'failed' for a
// worker-detected reason (not an SES error) — an anomalous empty audience
// post-materialization, or physical_address/reply-to going blank. Guarded so
// only the worker that actually performs the transition writes the audit
// row. Also publishes a closing #0048 progress snapshot when it performs the
// transition: #0045 shipped this path without a final publish (its own
// review flagged it), which left a `failed` campaign's last SSE frame stuck
// on whatever the previous live batch reported.
//
// The publish is placed AFTER MarkFailedCampaign, inside `if did {}`, and
// recomputes everything from the database — so the snapshot carries
// Status: "failed" (CampaignProgress.Status, read by publishProgress), which
// is what makes it terminal for the client. Note what it does NOT carry:
// Remaining stays > 0 here, because MarkFailedCampaign updates
// email_campaigns only and the unsent rows are still 'queued'. That is
// correct and deliberate — those recipients really will never be mailed, and
// the operator is entitled to see how many. A campaign is terminal because
// of its status, not because its queue drained; see CampaignProgress's own
// doc comment.
func (w *Worker) failCampaign(ctx context.Context, campaignID int64, reason string) error {
	_, sent, failed, _, queued, sending, cerr := w.store.CountEmailSends(ctx, campaignID)
	if cerr != nil {
		w.log.Error("mailing: counting sends before failing campaign", "campaign_id", campaignID, "err", cerr)
	}
	did, err := w.store.MarkFailedCampaign(ctx, campaignID)
	if err != nil {
		return err
	}
	if did {
		w.publishProgress(ctx, campaignID)
		w.auditSendFailed(ctx, campaignID, reason, sent, failed, queued+sending)
	}
	return nil
}

// failCampaignSES is failCampaign's twin for a terminalCampaign-class SES
// error (§8): stop the drain immediately, leave the remaining rows queued
// (the per-row release already happened in the caller), and record the
// terminal SES error class rather than the raw AWS error text.
func (w *Worker) failCampaignSES(ctx context.Context, campaignID int64, sesErr error) error {
	return w.failCampaign(ctx, campaignID, sesFailureReason(sesErr))
}

// resolveFromHeader implements §5.2's Message.From composition from the
// default_from_name setting: blank stays blank (the mailer's own EMAIL_FROM
// default applies); a name containing anything other than printable ASCII,
// or a `"`, `\`, CR, or LF, falls back to blank rather than risk header
// injection through an operator-editable setting — refusing is cheaper than
// RFC 2047-encoding a value nobody has asked to internationalise.
func (w *Worker) resolveFromHeader(ctx context.Context) string {
	return ResolveFromHeader(ctx, w.settings, w.fromAddr)
}

// ResolveFromHeader implements the default_from_name -> Message.From
// composition (see resolveFromHeader's own doc comment above for the exact
// rules) as a standalone, exported function rather than only a Worker
// method, so #0046's test-send handler
// (internal/handlers/admin_campaign_preview.go) can build the identical
// From header a real send would produce — without either duplicating the
// safety checks (the printable-ASCII/quote/backslash refusal) or
// constructing a full Worker just to reach them. resolveFromHeader itself
// is kept as a thin wrapper so every existing worker_test.go call site
// keeps compiling unchanged.
func ResolveFromHeader(ctx context.Context, settings SettingsReader, fromAddr string) string {
	name, err := settings.GetSetting(ctx, settingDefaultFromName)
	if err != nil {
		return ""
	}
	name = strings.TrimSpace(name)
	if name == "" || !isSafeFromDisplayName(name) {
		return ""
	}
	return fmt.Sprintf("%q <%s>", name, fromAddr)
}

// isSafeFromDisplayName reports whether name is safe to interpolate into a
// From header display name: printable ASCII only (0x20-0x7e), which also
// excludes CR/LF (both below 0x20), plus an explicit refusal of `"` and `\`
// so the quoted form %q produces cannot be broken out of.
func isSafeFromDisplayName(name string) bool {
	for _, r := range name {
		if r < 0x20 || r > 0x7e {
			return false
		}
		if r == '"' || r == '\\' {
			return false
		}
	}
	return true
}

// effectiveSendRate resolves min(cfg.MaxSendRate, settings.max_send_rate)
// (§7 of this issue's plan): the env var is the deploy-level ceiling, the
// settings row is the operator's dial. A missing, blank, non-numeric, or
// non-positive settings value falls back to the env ceiling — never to
// unbounded. Read once per batch so an operator can throttle a running send
// without a restart.
func (w *Worker) effectiveSendRate(ctx context.Context) int {
	raw, err := w.settings.GetSetting(ctx, settingMaxSendRate)
	if err != nil {
		return w.envMaxSendRate
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return w.envMaxSendRate
	}
	if n > w.envMaxSendRate {
		return w.envMaxSendRate
	}
	return n
}

// publishProgress reports the current batch/completion snapshot to #0048's
// seam, nil-guarded. Every field is recomputed from the database rather than
// tracked incrementally, so a publish after a resume is correct even though
// this Worker instance never saw the earlier batches.
//
// Status is read fresh alongside the counts (never passed in by the caller)
// for the same reason failCampaign re-reads its counts after the transition
// rather than reusing the pre-transition ones: the database is the authority
// on what the campaign is, and a caller's idea of the status it "just wrote"
// can be stale the instant a concurrent cancel or a second worker's
// transition lands. Because every call site publishes AFTER whatever
// transition it performs, the status read here can only be at-or-after the
// one just written — a snapshot can never claim a status the campaign has
// not reached. A failed status read is logged and published as "", never
// treated as fatal: the counts are still true and useful, and the client's
// terminality predicate keeps a count-based fallback for exactly this case
// (web/src/lib/campaignProgress.ts's isTerminalSnapshot).
func (w *Worker) publishProgress(ctx context.Context, campaignID int64) {
	if w.progress == nil {
		return
	}
	total, sent, failed, skipped, queued, sending, err := w.store.CountEmailSends(ctx, campaignID)
	if err != nil {
		w.log.Error("mailing: counting sends for progress publish", "campaign_id", campaignID, "err", err)
		return
	}
	status, serr := w.store.CampaignStatus(ctx, campaignID)
	if serr != nil {
		w.log.Error("mailing: reading campaign status for progress publish", "campaign_id", campaignID, "err", serr)
		status = ""
	}
	w.progress.PublishCampaignProgress(ctx, CampaignProgress{
		CampaignID: campaignID,
		Status:     status,
		Total:      total,
		Sent:       sent,
		Failed:     failed,
		Skipped:    skipped,
		Remaining:  queued + sending,
	})
}

// ── audit ─────────────────────────────────────────────────────────────────

func (w *Worker) auditSendRefused(ctx context.Context, campaignID int64, result PreflightResult) {
	if w.auditor == nil {
		return
	}
	messages := make([]string, len(result.Failures))
	for i, f := range result.Failures {
		messages[i] = f.Message
	}
	id := campaignID
	w.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionEmailCampaignSendRefused,
		TargetType: audit.TargetEmailCampaign,
		TargetID:   &id,
		Metadata: map[string]any{
			"codes":    result.Codes(),
			"messages": messages,
			"source":   "send_worker",
		},
	})
}

func (w *Worker) auditSendStarted(ctx context.Context, c *claimedCampaign, res MaterializeResult, recipients int64) {
	if w.auditor == nil {
		return
	}
	id := c.ID
	w.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionEmailCampaignSendStarted,
		TargetType: audit.TargetEmailCampaign,
		TargetID:   &id,
		Metadata: map[string]any{
			"recipients":    recipients,
			"audience_mode": c.AudienceMode,
			"interest_ids":  c.InterestIDs,
			"inserted":      res.Inserted,
			"chunks":        res.Chunks,
			"created_by":    c.CreatedBy,
			"source":        "send_worker",
		},
	})
}

func (w *Worker) auditSendCompleted(ctx context.Context, campaignID int64) {
	if w.auditor == nil {
		return
	}
	_, sent, failed, skipped, _, _, err := w.store.CountEmailSends(ctx, campaignID)
	if err != nil {
		w.log.Error("mailing: counting sends for completion audit", "campaign_id", campaignID, "err", err)
	}
	id := campaignID
	w.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionEmailCampaignSendCompleted,
		TargetType: audit.TargetEmailCampaign,
		TargetID:   &id,
		Metadata: map[string]any{
			"sent":    sent,
			"failed":  failed,
			"skipped": skipped,
			"source":  "send_worker",
		},
	})
}

func (w *Worker) auditSendFailed(ctx context.Context, campaignID int64, reason string, sent, failed, remaining int64) {
	if w.auditor == nil {
		return
	}
	id := campaignID
	w.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionEmailCampaignSendFailed,
		TargetType: audit.TargetEmailCampaign,
		TargetID:   &id,
		Metadata: map[string]any{
			"reason":    reason,
			"sent":      sent,
			"failed":    failed,
			"remaining": remaining,
			"source":    "send_worker",
		},
	})
}

func derefOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
