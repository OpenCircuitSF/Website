// outbox_worker.go is #0126's second send worker: it drains
// internal/outbox's outbound_queue (transactional mail — confirmation,
// already-subscribed, welcome, goodbye, admin alerts, registration,
// recovery, sessions-revoked, import invites) exactly as worker.go's
// *Worker drains email_sends (campaign mail), but as a SEPARATE type with
// its own claim/backoff/orphan-sweep, not an extension of *Worker.
//
// # A new worker, not an extension of *Worker — why
//
// *Worker's claim unit is a campaign, and it drains one campaign to
// completion before returning to its ticker (worker.go's Run doc comment:
// "there is no concurrency across campaigns within one Worker"). Folding
// transactional mail into it would put a confirmation email behind a
// five-thousand-recipient campaign drain — the exact failure #0126 exists
// to close, reintroduced with a different cause. OutboxWorker reuses the
// SHAPE — atomic claim, staleness-gated orphan sweep (#0122), detached
// contexts bounded by sendMessageTimeout/writeStatusTimeout, signal-based
// Stop — and none of the code path.
//
// # orphanStaleAfter is recomputed here, not shared with worker.go's
//
// Both use the identical derivation (2 * (sendMessageTimeout +
// writeStatusTimeout)) because both are bounded by the same two constants,
// but outboxOrphanStaleAfter is its own var — a future change to one
// worker's timeout budget must not silently retune the other's staleness
// window.
//
// # Rate limiting is this worker's own, not shared with *Worker's
//
// OutboxWorker carries its own rate.Limiter at the existing max_send_rate
// setting. Two workers can therefore momentarily exceed that combined rate
// while a campaign is draining at the same time transactional mail is
// flowing. Accepted deliberately: transactional volume here is a handful of
// messages a minute, SES's account-level rate is the real ceiling, and
// CLAUDE.md §5 is explicit that this project has no performance
// requirement to protect against that overlap.
package mailing

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

const (
	outboxDefaultPollInterval = 2 * time.Second
	outboxDefaultBatchSize    = 20

	// settingQueueMaxRetries is migrations/000021's seeded settings row.
	// #0126's plan §9 item 3: not "queue.max_retries" (dotted keys don't
	// exist in this project's settings table).
	settingQueueMaxRetries = "queue_max_retries"
)

// outboxOrphanStaleAfter mirrors worker.go's orphanStaleAfter derivation —
// see this file's package doc comment for why it is a separate var rather
// than a shared one. A var, not a const, so a test can shrink it; NOT sized
// against measured machine load (CLAUDE.md §5).
var outboxOrphanStaleAfter = 2 * (sendMessageTimeout + writeStatusTimeout)

// OutboxWorker drains internal/outbox's outbound_queue. Construct with
// NewOutboxWorker; call Run in its own goroutine, Stop to shut down.
type OutboxWorker struct {
	store    *outbox.Store
	events   *subscribers.Store // RecordEvent for confirmation_sent/welcome_sent — nil-tolerant, matching every other handler
	mailer   Mailer
	settings SettingsReader

	baseURL string
	// listDomain is config.Config.EmailListDomain, threaded through to
	// BuildWelcomeEmail (#0127) for the RFC 8058 List-Id/mailto: form —
	// see CampaignHeaders' own doc comment for why a blank value degrades
	// to the HTTPS-only header form rather than emitting an invalid
	// mailto: URI.
	listDomain     string
	envMaxSendRate int
	batchSize      int

	pollInterval time.Duration
	sleep        func(context.Context, time.Duration) error
	log          *slog.Logger

	stopCh   chan struct{}
	doneCh   chan struct{}
	stopOnce sync.Once
}

// OutboxWorkerDeps is NewOutboxWorker's construction argument.
type OutboxWorkerDeps struct {
	Store    *outbox.Store
	Events   *subscribers.Store // nil disables subscriber_events writes (confirmation_sent/welcome_sent)
	Mailer   Mailer
	Settings SettingsReader

	BaseURL string
	// ListDomain is config.Config.EmailListDomain (#0127) — see
	// OutboxWorker.listDomain's doc comment. Empty is tolerated the same
	// way CampaignHeaders tolerates it.
	ListDomain string
	// EnvMaxSendRate is the same MAX_SEND_RATE ceiling worker.go's Worker
	// reads — see effectiveSendRate/outboxEffectiveSendRate.
	EnvMaxSendRate int
	BatchSize      int
	PollInterval   time.Duration
	Log            *slog.Logger
}

// NewOutboxWorker constructs an OutboxWorker. It does not start it — call
// Run. See worker.go's NewWorker doc comment for why this runs as an
// in-process goroutine rather than a separate binary; the same reasoning
// applies unchanged.
func NewOutboxWorker(deps OutboxWorkerDeps) (*OutboxWorker, error) {
	if deps.Store == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires an outbox.Store")
	}
	if deps.Mailer == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires a Mailer")
	}
	if deps.Settings == nil {
		return nil, fmt.Errorf("mailing: outbox worker requires a SettingsReader")
	}

	pollInterval := deps.PollInterval
	if pollInterval <= 0 {
		pollInterval = outboxDefaultPollInterval
	}
	batchSize := deps.BatchSize
	if batchSize <= 0 {
		batchSize = outboxDefaultBatchSize
	}
	envMaxSendRate := deps.EnvMaxSendRate
	if envMaxSendRate <= 0 {
		envMaxSendRate = defaultMaxSendRate
	}
	log := deps.Log
	if log == nil {
		log = slog.Default()
	}

	return &OutboxWorker{
		store:          deps.Store,
		events:         deps.Events,
		mailer:         deps.Mailer,
		settings:       deps.Settings,
		baseURL:        deps.BaseURL,
		listDomain:     deps.ListDomain,
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
// done. Each pass sweeps orphans, claims a batch, sends it, then waits for
// the next tick.
func (w *OutboxWorker) Run(ctx context.Context) {
	defer close(w.doneCh)
	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		default:
		}

		processed, err := w.pass(ctx)
		if err != nil {
			w.log.Error("mailing: outbox worker pass failed", "err", err)
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

// Stop signals Run to stop claiming new work, releases any row this
// process currently holds claimed-but-unsent back to 'queued' (so a
// restart, or another process's worker, picks it up immediately instead of
// waiting out outboxOrphanStaleAfter), and blocks until Run has returned or
// ctx's deadline elapses. Safe to call more than once.
func (w *OutboxWorker) Stop(ctx context.Context) error {
	w.stopOnce.Do(func() { close(w.stopCh) })
	select {
	case <-w.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// pass sweeps orphans, expires overdue pending signups (#0128), claims one
// batch, and sends it. Returns processed=true if it did any real work
// (claimed at least one row), so Run can skip the poll wait, matching
// worker.go's "don't busy-loop on an empty pass" discipline (#0122).
func (w *OutboxWorker) pass(ctx context.Context) (bool, error) {
	sweepCtx, sweepCancel := context.WithTimeout(ctx, writeStatusTimeout)
	swept, err := w.store.OrphanSweep(sweepCtx, outboxOrphanStaleAfter)
	sweepCancel()
	if err != nil {
		return false, fmt.Errorf("mailing: outbox orphan sweep: %w", err)
	}
	if swept > 0 {
		w.log.Warn("mailing: outbox orphan sweep reclaimed rows", "count", swept)
	}

	// #0128: expiring pending signups past confirm_expires_at rides this
	// worker's existing poll loop rather than standing up a fourth
	// dedicated ticker in main.go — it is cheap (one indexed UPDATE ...
	// RETURNING per pass, idempotent, near-zero rows in the common case)
	// and this worker already holds the one *subscribers.Store this
	// package is given (w.events, also used for confirmation_sent/
	// welcome_sent). A failure here is logged and does not abort the
	// pass — mail still needs to go out even if the expiry sweep hiccups.
	// nil-tolerant: w.events may be nil (see OutboxWorkerDeps' doc
	// comment), matching every other w.events-gated branch in this file.
	if w.events != nil {
		expireCtx, expireCancel := context.WithTimeout(ctx, writeStatusTimeout)
		expired, expireErr := w.events.ExpirePendingSweep(expireCtx, time.Now())
		expireCancel()
		if expireErr != nil {
			w.log.Error("mailing: expire-pending-signups sweep failed", "err", expireErr)
		} else if expired > 0 {
			w.log.Info("mailing: expired pending signups past confirm_expires_at", "count", expired)
		}
	}

	rows, err := w.store.ClaimDue(ctx, w.batchSize)
	if err != nil {
		return false, fmt.Errorf("mailing: claiming outbound_queue batch: %w", err)
	}
	if len(rows) == 0 {
		return false, nil
	}

	sendRate := w.outboxEffectiveSendRate(ctx)
	limiter := rate.NewLimiter(rate.Limit(sendRate), 1)

	for _, row := range rows {
		select {
		case <-w.stopCh:
			// Leave this and every remaining row of the batch claimed;
			// Stop's own Release call (via OutboxWorker.releaseAll, called
			// from the goroutine that invoked Run — see Stop's doc
			// comment) or, failing that, the orphan sweep will reclaim
			// them.
			return true, nil
		default:
		}
		if err := limiter.Wait(ctx); err != nil {
			return true, nil // context cancelled/deadline — shutdown, not an error to log
		}
		w.sendOne(row)
	}
	return true, nil
}

// sendOne renders and sends a single claimed row, on a context detached
// from the caller's (so a SIGTERM's ctx cancellation doesn't abort a send
// already accepted by SES before the status write commits — the same
// precedent worker.go's package doc comment sets), and records the
// terminal state (sent, retried, or abandoned).
func (w *OutboxWorker) sendOne(row outbox.Row) {
	sendCtx, sendCancel := context.WithTimeout(context.Background(), sendMessageTimeout)
	msg, err := w.render(sendCtx, row)
	if err != nil {
		sendCancel()
		w.finishFailed(row, fmt.Errorf("rendering kind %q: %w", row.Kind, err))
		return
	}

	messageID, sendErr := w.mailer.Send(sendCtx, msg)
	sendCancel()
	if sendErr != nil {
		w.finishFailed(row, sendErr)
		return
	}

	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	if _, err := w.store.MarkSent(writeCtx, row.ID, messageID); err != nil {
		w.log.Error("mailing: marking outbound_queue row sent failed", "id", row.ID, "kind", row.Kind, "err", err)
		return
	}

	// #0126's plan §6: confirmation_sent is written here — "a confirmation
	// message LEFT the outbound queue" — not at enqueue time. #0127 adds
	// welcome_sent on the identical precedent ("a welcome message LEFT the
	// outbound queue"), not at Confirm's enqueue time — see
	// internal/subscribers.Store.Confirm's own comment on why it does NOT
	// write welcome_sent itself. Every other kind either has no
	// subscriber_events action yet (admin alerts, auth mail) or isn't in
	// the closed set at all (already_subscribed — see events.go's package
	// doc comment, #0126's plan §9 item 6).
	var sentAction subscribers.Action
	switch row.Kind {
	case outbox.KindConfirmation:
		sentAction = subscribers.ActionConfirmationSent
	case outbox.KindWelcome:
		sentAction = subscribers.ActionWelcomeSent
	}
	if w.events != nil && sentAction != "" && row.SubscriberID != nil {
		eventCtx, eventCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
		if err := w.events.RecordEvent(eventCtx, subscribers.Event{
			SubscriberID: row.SubscriberID,
			Email:        row.Recipient,
			Action:       sentAction,
		}); err != nil {
			w.log.Error("mailing: recording sent event failed", "id", row.ID, "action", sentAction, "subscriber_id", *row.SubscriberID, "err", err)
		}
		eventCancel()
	}
}

// finishFailed records a send/render failure via MarkRetryOrAbandon,
// logging the outcome either way.
func (w *OutboxWorker) finishFailed(row outbox.Row, sendErr error) {
	writeCtx, writeCancel := context.WithTimeout(context.Background(), writeStatusTimeout)
	defer writeCancel()
	maxRetries := w.effectiveMaxRetries(writeCtx)
	if _, err := w.store.MarkRetryOrAbandon(writeCtx, row.ID, row.Attempts, sendErr.Error(), maxRetries); err != nil {
		w.log.Error("mailing: recording outbound_queue failure failed", "id", row.ID, "kind", row.Kind, "send_err", sendErr, "err", err)
		return
	}
	if row.Attempts >= maxRetries {
		w.log.Error("mailing: outbound_queue row abandoned after max retries", "id", row.ID, "kind", row.Kind, "attempts", row.Attempts, "err", sendErr)
	} else {
		w.log.Warn("mailing: outbound_queue send failed, will retry", "id", row.ID, "kind", row.Kind, "attempts", row.Attempts, "err", sendErr)
	}
}

// effectiveMaxRetries reads settings.queue_max_retries, falling back to
// outbox.DefaultMaxRetries on a missing row or an unparseable/non-positive
// value — the same degrade-gracefully convention worker.go's
// effectiveSendRate and internal/handlers/soft_bounce.go both establish for
// their own settings-backed constants.
func (w *OutboxWorker) effectiveMaxRetries(ctx context.Context) int {
	raw, err := w.settings.GetSetting(ctx, settingQueueMaxRetries)
	if err != nil {
		return outbox.DefaultMaxRetries
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil || n <= 0 {
		return outbox.DefaultMaxRetries
	}
	return n
}

// outboxEffectiveSendRate mirrors worker.go's effectiveSendRate exactly
// (settings.max_send_rate, clamped to envMaxSendRate, falling back to
// envMaxSendRate on any read/parse failure) — kept as this worker's own
// method rather than shared, matching this file's "own rate limiter"
// decision in the package doc comment.
func (w *OutboxWorker) outboxEffectiveSendRate(ctx context.Context) int {
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

// --- payload shapes and rendering ---
//
// Every payload struct below is the JSONB contract between a producer
// (internal/subscribers.Store, internal/auth's services) and this
// renderer — matched by JSON field name, not by a shared Go type, since
// producer and consumer live in different packages and outbox.Item.Payload
// is deliberately `any`. See internal/outbox's package doc comment for why
// payload holds template inputs, not rendered MIME.

type confirmationPayload struct {
	ConfirmToken string `json:"confirm_token"`
	ManageToken  string `json:"manage_token"`
	TTLSeconds   int64  `json:"ttl_seconds"`
}

type alreadySubscribedPayload struct {
	ManageToken string `json:"manage_token"`
}

type registrationPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type recoveryPayload struct {
	Token      string `json:"token"`
	TTLSeconds int64  `json:"ttl_seconds"`
}

type sessionsRevokedPayload struct {
	At time.Time `json:"at"`
}

type adminAlertPayload struct {
	Subject string   `json:"subject"`
	Lines   []string `json:"lines"`
}

// welcomePayload is outbound_queue.payload's shape for outbox.KindWelcome
// (#0127) — mirrors internal/subscribers.welcomePayload field-for-field
// (matched by JSON field name, not a shared Go type, per this file's own
// "producer and consumer live in different packages" convention above).
// InterestNames is the subscriber's selection AT CONFIRM TIME, not re-read
// at send time — see BuildWelcomeEmail's doc comment.
type welcomePayload struct {
	ManageToken   string   `json:"manage_token"`
	InterestNames []string `json:"interest_names"`
}

// render resolves physical_address at SEND time (not enqueue time) — #0126's
// plan §3: "a small improvement: an address set between enqueue and send
// now produces a correct footer, where today it would not." This worker
// deliberately does NOT refuse to send a confirmation for a missing
// physical_address the way #0045's CAMPAIGN send worker refuses to start a
// campaign (CLAUDE.md §9's rule is scoped to that worker;
// BuildConfirmationEmail already documents "" as simply omitting the line)
// — refusing here would turn a cosmetic gap into a broken signup flow.
func (w *OutboxWorker) render(ctx context.Context, row outbox.Row) (Message, error) {
	switch row.Kind {
	case outbox.KindConfirmation:
		var p confirmationPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildConfirmationEmail(row.Recipient, w.baseURL, p.ConfirmToken, p.ManageToken, time.Duration(p.TTLSeconds)*time.Second, w.physicalAddress(ctx)), nil

	case outbox.KindAlreadySubscribed:
		var p alreadySubscribedPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildAlreadySubscribedEmail(row.Recipient, w.baseURL, p.ManageToken, w.physicalAddress(ctx)), nil

	case outbox.KindRegistration:
		var p registrationPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildRegistrationEmail(row.Recipient, w.baseURL, p.Token, time.Duration(p.TTLSeconds)*time.Second), nil

	case outbox.KindRecovery:
		var p recoveryPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildRecoveryEmail(row.Recipient, w.baseURL, p.Token, time.Duration(p.TTLSeconds)*time.Second), nil

	case outbox.KindSessionsRevoked:
		var p sessionsRevokedPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildSessionsRevokedEmail(row.Recipient, w.baseURL, p.At), nil

	case outbox.KindAdminAlert:
		var p adminAlertPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildAdminAlertEmail(row.Recipient, w.baseURL, p.Subject, p.Lines), nil

	case outbox.KindWelcome:
		var p welcomePayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			return Message{}, err
		}
		return BuildWelcomeEmail(row.Recipient, w.baseURL, w.listDomain, p.ManageToken, p.InterestNames, w.physicalAddress(ctx)), nil

	default:
		// goodbye (no producer), import_invite (#0129): no renderer yet.
		// Returning an error routes through the ordinary
		// MarkRetryOrAbandon path — it will retry a few times, then land
		// on 'abandoned' with this error retained, rather than crashing
		// the worker or silently dropping the row.
		return Message{}, fmt.Errorf("mailing: no renderer for outbound_queue kind %q", row.Kind)
	}
}

// physicalAddress reads settings.physical_address for the email footer,
// treating a nil dependency or any read error as an empty address — the
// same nil-tolerant convention SubscribeHandler used before #0126 moved
// this resolution here.
func (w *OutboxWorker) physicalAddress(ctx context.Context) string {
	if w.settings == nil {
		return ""
	}
	value, err := w.settings.GetSetting(ctx, settingPhysicalAddress)
	if err != nil {
		return ""
	}
	return value
}
