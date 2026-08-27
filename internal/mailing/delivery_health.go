// delivery_health.go is #0124's circuit breaker (PRD §6.9): the send
// worker's own mid-send safety mechanism, distinct from the account-wide
// complaint bands #0227 added to the admin dashboard (admin_dashboard.go,
// internal/handlers) — that pair reads the account's history across every
// campaign ever sent and only warns; this one reads ONE campaign's own
// running rate, while it is in flight, and stops the send.
//
// # Why a breaker at all
//
// AWS enforces a bounce rate under 5% and a complaint rate under 0.1%
// ACROSS THE WHOLE SES ACCOUNT. Crossing either puts the account under
// review and then back into sandbox — which takes down confirmation email
// too, not just campaigns. A bad import or a stale list can cross 5% in the
// first few hundred sends of a single campaign, and nothing before this
// issue watched for that while a send was actually running.
//
// # The decision, stated plainly (issue's "decide and argue")
//
//   - What stops: THIS campaign's drain, on the worker that is running it.
//     Remaining email_sends rows stay 'queued' — nothing is lost, nothing is
//     marked failed. Other campaigns and transactional mail (a separate
//     queue, internal/outbox) are entirely unaffected.
//   - At what threshold: send_health_bounce_pct (default 5.0%) OR
//     send_health_complaint_pct (default 0.1%), each independently
//     sufficient to trip — PRD §6.9's table, verbatim. Below
//     send_health_min_sample (default 50) messages sent, the breaker never
//     evaluates the rate at all: "5% of 3" is not a rate, it's a coin flip.
//   - Automatic or advisory: AUTOMATIC. The worker pauses the campaign
//     itself, with no human in the loop, the moment it observes the
//     crossing — see checkDeliveryHealth/pauseCampaignDeliveryHealth below.
//     An advisory-only breaker (surface a warning, let the send continue)
//     was rejected: the whole point is that nobody may be watching the
//     dashboard while a bad list burns through the account's reputation in
//     the first sixty seconds of a send.
//   - What un-trips it: a DELIBERATE ADMIN ACTION, POST
//     /admin/campaigns/{id}/resume (internal/handlers/admin_campaigns.go),
//     gated by a typed confirmation exactly like #0047's send confirmation
//     — never automatic, and never merely "wait a while and it clears
//     itself". A breaker that resets on its own defeats the purpose (the
//     next batch would trip it again immediately, or — worse — the rate
//     could transiently dip below threshold while the underlying list
//     problem is still there) and a breaker nothing can reset is worse than
//     none (a bad-but-recoverable send would be stuck forever). Resuming
//     does not bypass the breaker: drainLoop calls checkDeliveryHealth at
//     the TOP of its loop as well as at the end of every batch (#0269), so
//     a resume that lands on a still-tripped rate re-trips before claiming
//     a single row — not after sending one more full batch, which was the
//     end-of-batch-only shape's gap. A campaign that resumes into a
//     genuinely-recovered rate keeps draining; one that resumes into a
//     still-bad rate, with real work still queued, sends zero further
//     messages. The top-of-loop check is gated on Queued > 0, though
//     (checkAndMaybePauseDeliveryHealth): a trip observed with nothing left
//     queued has nothing further to stop, and re-pausing there would strand
//     the campaign forever — Resume would just re-observe the same
//     already-fixed rate and re-pause, since no further send ever occurs to
//     move it. In that case the top-of-loop check stands aside and lets the
//     loop reach ClaimBatch, which finds nothing and completes the campaign
//     via CompleteIfDone instead (#0269's review). The end-of-batch check
//     is NOT gated this way — a trip discovered exactly as the last batch
//     finishes still pauses and alerts, so the operator learns the list was
//     bad even though nothing more will be sent; only the top-of-loop
//     re-check on a later Resume must stand down once nothing remains.
//   - Who is told: CampaignStore.Resume's own audit row aside, the trip
//     itself writes ActionEmailCampaignPausedDeliveryHealth to audit_log
//     (an operator reviewing /admin/audit sees it) AND enqueues an
//     outbox.KindAdminAlert to ADMIN_EMAIL (#0126's queue — durable, not a
//     synchronous SES call that could itself fail silently) naming the
//     campaign, the observed rate(s), and the threshold(s). CLAUDE.md §10
//     item 4 notes ADMIN_EMAIL/who-reads-it is still undecided; this issue
//     does not block on that being resolved — an empty AdminEmail simply
//     skips the enqueue (enqueueDeliveryHealthAlert's own doc comment), and
//     the audit row and the paused status are not conditioned on it.
package mailing

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// deliveryHealthResult is checkDeliveryHealth's report for one evaluation.
// Zero value (Tripped=false, Sent=0) is the correct "nothing to see" result
// for a campaign below the minimum sample or with a nil Stats dependency.
type deliveryHealthResult struct {
	Tripped bool
	// Reasons is "bounce_rate", "complaint_rate", or both comma-joined —
	// either threshold independently trips the breaker (PRD §6.9), and a
	// campaign that crosses both at once should say so rather than picking
	// one arbitrarily.
	Reasons                          string
	Sent, Bounced, Complained        int64
	BounceRate, ComplaintRate        float64 // percent, e.g. 5.0 means 5%
	BounceThreshold, ComplaintThresh float64 // percent, the configured settings values used for this check
	MinSample                        int
	// Queued is email_sends.status='queued' for this campaign, read off the
	// SAME StatusCounts row as Sent — no extra query. #0269's top-of-loop
	// caller uses this to decide whether a trip is actionable: with nothing
	// left queued, there is nothing further to stop, and pausing would only
	// strand the campaign (see checkAndMaybePauseDeliveryHealth).
	Queued int64
}

// checkDeliveryHealth evaluates campaignID's running bounce/complaint rate
// against the configured thresholds. A nil w.stats (a test, or a future
// deployment mode with no CampaignStatsStore wired) disables the breaker
// entirely — checkDeliveryHealth returns a zero result and nil error, never
// panics — matching this file's package doc comment on Worker.stats'
// nil-tolerance. A query error is returned, not swallowed: the caller
// (drainLoop) logs it and keeps draining rather than treating "the health
// check itself failed" as "the campaign is healthy".
func (w *Worker) checkDeliveryHealth(ctx context.Context, campaignID int64) (deliveryHealthResult, error) {
	if w.stats == nil {
		return deliveryHealthResult{}, nil
	}

	minSample, bouncePct, complaintPct := resolveSendHealthSettings(ctx, w.settings, w.log)

	counts, err := w.stats.StatusCounts(ctx, campaignID)
	if err != nil {
		return deliveryHealthResult{}, fmt.Errorf("mailing: reading send counts for campaign %d: %w", campaignID, err)
	}
	result := deliveryHealthResult{
		Sent: counts.Sent, Queued: counts.Queued, MinSample: minSample,
		BounceThreshold: bouncePct, ComplaintThresh: complaintPct,
	}
	if counts.Sent < int64(minSample) {
		// "Below this many sends, rates are too noisy to act on" (PRD
		// §6.9) — deliberately a strict less-than: a campaign that has
		// sent EXACTLY minSample messages already has a real sample and
		// may trip on this very check.
		return result, nil
	}

	events, err := w.stats.EventCounts(ctx, campaignID)
	if err != nil {
		return deliveryHealthResult{}, fmt.Errorf("mailing: reading event counts for campaign %d: %w", campaignID, err)
	}
	result.Bounced = events.Bounced
	result.Complained = events.Complained
	// #0269: the denominator (counts.Sent) grows SYNCHRONOUSLY with sends —
	// it is exactly how many rows this worker has stamped 'sent' so far —
	// while the numerator (events.Bounced/Complained) grows ASYNCHRONOUSLY,
	// via SES's webhook (internal/handlers/ses_notifications.go) landing
	// sometime after the send that triggered it, often well after. So a
	// resumed campaign's measured rate is diluted while it sends: real
	// bounce/complaint events for the batch just sent may not have arrived
	// yet, understating the true rate for exactly as long as that lag
	// lasts. This is inherent to measuring a rate against in-flight mail,
	// not a defect — but it means the breaker is systematically late in
	// proportion to batch size, which is the other argument (besides the
	// resume gap, see this file's package doc comment) for evaluating at
	// the top of drainLoop's loop as well as at the end of a batch: a
	// smaller window between evaluations is the only lever available here.
	result.BounceRate = float64(events.Bounced) / float64(counts.Sent) * 100
	result.ComplaintRate = float64(events.Complained) / float64(counts.Sent) * 100

	var reasons []string
	// >=, not >: "crossing" the threshold (PRD §6.9) includes landing
	// exactly on it — the same "reaching the threshold suppresses" reading
	// this issue's streak rule uses (ses_notifications.go's applyBounce).
	if result.BounceRate >= bouncePct {
		reasons = append(reasons, "bounce_rate")
	}
	if result.ComplaintRate >= complaintPct {
		reasons = append(reasons, "complaint_rate")
	}
	if len(reasons) > 0 {
		result.Tripped = true
		result.Reasons = strings.Join(reasons, ",")
	}
	return result, nil
}

// resolveSendHealthSettings reads the three send_health_* settings
// (migrations/000015), falling back to PRD §6.9's defaults on a
// missing/unparseable row — matching internal/handlers/soft_bounce.go's
// parsePositiveIntSetting convention for the identical reason (a config
// hiccup must never silently disable a safety check OR crash the worker).
// A package-local twin, not a shared helper, because internal/mailing
// cannot import internal/handlers (the reverse already holds — see
// internal/handlers/settings.go's own validSettingValue, which imports
// internal/mailing for these same three key constants).
func resolveSendHealthSettings(ctx context.Context, settings SettingsReader, log *slog.Logger) (minSample int, bouncePct, complaintPct float64) {
	minSample = resolveSendHealthIntSetting(ctx, settings, SettingSendHealthMinSample, DefaultSendHealthMinSample, log)
	bouncePct = resolveSendHealthPctSetting(ctx, settings, SettingSendHealthBouncePct, DefaultSendHealthBouncePct, log)
	complaintPct = resolveSendHealthPctSetting(ctx, settings, SettingSendHealthComplaintPct, DefaultSendHealthComplaintPct, log)
	return minSample, bouncePct, complaintPct
}

func resolveSendHealthIntSetting(ctx context.Context, settings SettingsReader, key string, fallback int, log *slog.Logger) int {
	value, err := settings.GetSetting(ctx, key)
	if err != nil {
		if log != nil {
			log.Warn("mailing: send-health setting missing, using default", "key", key, "default", fallback, "err", err)
		}
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil || n <= 0 {
		if log != nil {
			log.Warn("mailing: send-health setting has an invalid value, using default", "key", key, "value", value, "default", fallback)
		}
		return fallback
	}
	return n
}

func resolveSendHealthPctSetting(ctx context.Context, settings SettingsReader, key string, fallback float64, log *slog.Logger) float64 {
	value, err := settings.GetSetting(ctx, key)
	if err != nil {
		if log != nil {
			log.Warn("mailing: send-health setting missing, using default", "key", key, "default", fallback, "err", err)
		}
		return fallback
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || f <= 0 || f > 100 {
		if log != nil {
			log.Warn("mailing: send-health setting has an invalid value, using default", "key", key, "value", value, "default", fallback)
		}
		return fallback
	}
	return f
}

// pauseCampaignDeliveryHealth performs the trip: moves campaignID to
// 'paused_delivery_health' (guarded to the same `AND status='sending'`
// shape as MarkFailedCampaign, so a concurrent completion/failure racing
// this call is a harmless no-op rather than a double transition), then —
// only if this call actually performed the transition — publishes a
// closing progress snapshot, writes the audit row, and enqueues the admin
// alert. subject is the campaign's own Message.Subject (claimedCampaign has
// no separate "name" field available to the worker), used only for the
// alert's human-readable text.
func (w *Worker) pauseCampaignDeliveryHealth(ctx context.Context, campaignID int64, subject string, health deliveryHealthResult) error {
	did, err := w.store.MarkPausedDeliveryHealth(ctx, campaignID)
	if err != nil {
		return err
	}
	if !did {
		return nil
	}
	w.publishProgress(ctx, campaignID)
	w.auditPausedDeliveryHealth(ctx, campaignID, health)
	w.enqueueDeliveryHealthAlert(ctx, campaignID, subject, health)
	return nil
}

// auditPausedDeliveryHealth writes ActionEmailCampaignPausedDeliveryHealth.
// Metadata is a plain inline map literal (internal/handlers'
// audit_email_metadata_guard_test.go's AST guard — #0237/#0252 — requires
// exactly this shape: an inline map or a local map built from string-
// literal keys, never an expression it cannot resolve).
func (w *Worker) auditPausedDeliveryHealth(ctx context.Context, campaignID int64, health deliveryHealthResult) {
	if w.auditor == nil {
		return
	}
	targetID := campaignID
	w.auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionEmailCampaignPausedDeliveryHealth,
		TargetType: audit.TargetEmailCampaign,
		TargetID:   &targetID,
		Metadata: map[string]any{
			"reasons":             health.Reasons,
			"sent":                health.Sent,
			"bounced":             health.Bounced,
			"complained":          health.Complained,
			"bounce_rate_pct":     health.BounceRate,
			"complaint_rate_pct":  health.ComplaintRate,
			"bounce_threshold":    health.BounceThreshold,
			"complaint_threshold": health.ComplaintThresh,
			"min_sample":          health.MinSample,
		},
	})
}

// enqueueDeliveryHealthAlert enqueues one outbox.KindAdminAlert (#0126's
// queue, built for exactly this caller — see that issue's plan §7) naming
// the campaign, the observed rate(s), and the threshold(s). A nil
// w.outbox or an empty w.adminEmail (ADMIN_EMAIL unset — CLAUDE.md §10 item
// 4 notes this is still undecided) skips the enqueue entirely: the trip
// itself (the paused status and the audit row) is NOT conditioned on
// either, since those are the load-bearing safety action — the alert is a
// notification of a fact that has already taken effect, not a precondition
// for it.
func (w *Worker) enqueueDeliveryHealthAlert(ctx context.Context, campaignID int64, subject string, health deliveryHealthResult) {
	if w.outbox == nil || w.adminEmail == "" {
		return
	}
	lines := []string{
		fmt.Sprintf("Campaign #%d (%q) was paused mid-send by the delivery-health circuit breaker.", campaignID, subject),
		fmt.Sprintf("Sent so far: %d", health.Sent),
	}
	if health.BounceRate >= health.BounceThreshold {
		lines = append(lines, fmt.Sprintf("Bounce rate: %.2f%% of %d sent (threshold %.2f%%)", health.BounceRate, health.Sent, health.BounceThreshold))
	}
	if health.ComplaintRate >= health.ComplaintThresh {
		lines = append(lines, fmt.Sprintf("Complaint rate: %.2f%% of %d sent (threshold %.2f%%)", health.ComplaintRate, health.Sent, health.ComplaintThresh))
	}
	lines = append(lines, "Resume the campaign from its admin page once the underlying issue is understood, or cancel it.")

	if _, err := w.outbox.Enqueue(ctx, outbox.Item{
		Kind:      outbox.KindAdminAlert,
		Recipient: w.adminEmail,
		Payload: map[string]any{
			"subject": fmt.Sprintf("Campaign #%d paused — delivery health", campaignID),
			"lines":   lines,
		},
	}); err != nil {
		w.log.Error("mailing: enqueueing delivery-health admin alert failed", "campaign_id", campaignID, "err", err)
	}
}
