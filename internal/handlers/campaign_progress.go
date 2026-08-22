// campaign_progress.go (#0048, PRD §8) adapts internal/mailing's
// ProgressPublisher seam to the SAME SSE broker GET /api/events (events.go,
// this package) already streams from — the wiring point
// cmd/opencircuit/main.go's newSendWorkerIfEnabled names as the literal
// `Progress: nil` #0048 replaces.
//
// Campaign send progress is broadcast, not per-user: a campaign is not
// "owned" by whichever admin session happens to have it open, so this uses
// Broker.PublishAll (every connected admin sees it) rather than Publish
// (one userID's channel) — the same distinction events.go's own EventsHandler
// documents for the subscribing side. GET /api/events itself is
// session+admin gated (main.go's mountAndServe), so only admin sessions are
// ever subscribed in the first place; PublishAll broadcasting to "every
// subscriber" is therefore still scoped to admins in practice, not a broader
// exposure.
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/brennanMKE/OpenCircuitSF/internal/events"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

// campaignProgressEventName is the SSE `event:` name
// web/src/lib/campaignProgress.ts's PROGRESS_EVENT constant and
// CampaignEditor.svelte's subscribeEvent call both key off of. Keep the two
// in sync by hand — there is no shared source of truth across the Go/TS
// boundary.
const campaignProgressEventName = "campaign.progress"

// campaignProgressBroker is the narrow seam campaignProgressPublisher needs
// from *events.Broker: broadcasting one event to every connected admin
// session. Depending on this interface, rather than the concrete broker,
// keeps the publisher unit-testable with a fake — mirroring
// eventSubscriber's own convention in events.go for the subscribing side.
type campaignProgressBroker interface {
	PublishAll(event events.Event)
}

// campaignProgressPublisher implements mailing.ProgressPublisher over a
// campaignProgressBroker.
type campaignProgressPublisher struct {
	broker campaignProgressBroker
	log    *slog.Logger
}

// NewCampaignProgressPublisher adapts *events.Broker (the concrete type
// cmd/opencircuit/main.go constructs) to mailing.ProgressPublisher for that
// call site. Returns a genuinely nil mailing.ProgressPublisher when broker is
// nil, so mailing.Worker's own `if w.progress == nil` guard (publishProgress,
// worker.go) still works — the same typed-nil trap
// NewCampaignPreflightChecker's doc comment (admin_campaigns.go) warns
// about: a non-nil interface wrapping a nil *campaignProgressPublisher would
// defeat that guard and panic on the first publish.
func NewCampaignProgressPublisher(broker *events.Broker, log *slog.Logger) mailing.ProgressPublisher {
	if broker == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &campaignProgressPublisher{broker: broker, log: log}
}

// PublishCampaignProgress marshals p and broadcasts it as a "campaign.progress"
// SSE frame to every connected admin session. ctx is accepted to satisfy
// mailing.ProgressPublisher but unused: Broker.PublishAll is a synchronous,
// in-memory, non-blocking fan-out (each subscriber send is a select with a
// default branch — see broker.go), so there is nothing here that could
// block long enough for a context deadline to matter, and the send worker's
// own correctness must not depend on this call succeeding or even being
// watched by anyone (CLAUDE.md: "the worker's correctness path must not
// depend on anyone watching").
func (p *campaignProgressPublisher) PublishCampaignProgress(_ context.Context, cp mailing.CampaignProgress) {
	payload, err := json.Marshal(cp)
	if err != nil {
		// Defensive: CampaignProgress is a flat struct of int64s and one
		// string (see its own doc comment in worker.go), so encoding/json
		// cannot actually fail here — logged rather than silently dropped in
		// case that struct ever grows a field that can.
		p.log.Error("campaign progress publisher: marshal", "campaign_id", cp.CampaignID, "err", err)
		return
	}
	p.broker.PublishAll(events.Event{Name: campaignProgressEventName, Payload: payload})
}
