// Admin overview dashboard (#0061, PRD §5.2): GET /admin/overview, the
// landing screen at /admin — subscriber counts and 30-day growth, interest
// distribution, recent campaigns, any campaign currently sending, and a
// warnings row (complaint rate, missing physical address, SES sandbox,
// unprocessed inbound mail).
//
// # One query per figure, not N
//
// Every number below comes from exactly one aggregate query against its own
// store — StatusCounts, Growth30Days, interests.Store.SubscriberCounts,
// mailing.CampaignStore.List (itself two queries total regardless of
// campaign count, per that method's own "no pagination... count is small"
// doc comment), AccountComplaintRate, and ListSettings. Nothing here loops
// over a result set issuing a query per row — that is the N+1 shape this
// package's own review history has bounced elsewhere.
//
// # The synthetic exclusion (carried in from #0061's amendment)
//
// Growth30Days (subscribers/store.go) filters synthetic=true unconditionally,
// same as StatusCounts. Interest distribution reuses
// interests.Store.SubscriberCounts, which joins subscriber_interests rather
// than reading subscribers directly and is therefore outside the amendment's
// literal scope — but see this file's interestDistribution doc comment for
// why it is synthetic-safe anyway, and why a filter was not added there.
//
// # What "unprocessed inbound mail" cannot show yet
//
// PRD §6.5 path 3 (inbound mailto: unsubscribe processing, internal/inbound,
// #0058) is not built. There is no table this handler could aggregate to
// answer "how many inbound messages are waiting for manual review" — so the
// warnings response says so explicitly (inbound_mail_unavailable) rather than
// reporting a fabricated zero that would read as "nothing pending" when the
// true answer is "unknown."
package handlers

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/outbox"
)

// dashboardComplaintReviewThresholdPct is AWS's account-wide complaint-rate
// ceiling (PRD §6.9: "AWS enforces... a complaint rate under 0.1% across the
// whole SES account. Crossing... puts the account under review and then into
// sandbox"), computed account-wide across every campaign ever sent — the
// AMBER band (#0227). Crossing it does not degrade inbox placement the way
// the red band below does; it risks sending stopping altogether, since a
// re-sandboxed account can't send even confirmation emails. This is NOT
// send_health_complaint_pct (PRD §6.9's per-campaign circuit breaker, also
// defaulted to 0.1% but scoped to ONE campaign's own running rate at send
// time) — that setting doesn't exist as code today; the breaker itself is
// #0124, unimplemented. Keep the two separate once #0124 lands: conflating
// them would either weaken the breaker's bound or make this warning read
// from a per-campaign figure instead of the account-wide one it is defined
// against.
const dashboardComplaintReviewThresholdPct = 0.1

// dashboardComplaintRateThresholdPct is the Gmail/Yahoo bulk-sender
// complaint-rate ceiling (PRD §11: "complaint rate under 0.3%"), computed
// account-wide across every campaign ever sent — the RED band (#0227).
// Crossing it degrades inbox placement (throttling/filtering), a different
// and lesser consequence than the amber band above (AWS re-sandboxing,
// which stops sending entirely) — the two are reported as independent
// booleans, not one escalating color, precisely because the failure modes
// differ. Like the amber threshold, this is NOT
// send_health_complaint_pct — see that constant's doc comment; #0124 is the
// per-campaign circuit breaker this would-be settings-table value belongs
// to once it exists.
const dashboardComplaintRateThresholdPct = 0.3

// dashboardComplaintMinSample: below this many account-wide sends, a
// complaint rate is too noisy to act on (one complaint out of three sends is
// 33%, not a trend). Deliberately a local constant, not a read of the
// send_health_min_sample setting — that setting is scoped to one campaign's
// circuit breaker, not this account-wide figure, and reusing it would tie
// two independently-tunable thresholds together by accident.
const dashboardComplaintMinSample = 50

// dashboardRecentCampaignsLimit caps the "recent campaigns" list — the
// acceptance criteria ask for "recent," not "all," and mailing.CampaignStore
// itself is a small, unpaginated table (see its List doc comment), so this
// is applied in Go after the one List() call rather than in SQL.
const dashboardRecentCampaignsLimit = 5

// dashboardSubscriberStore is the narrow subscribers.Store seam this handler
// needs.
type dashboardSubscriberStore interface {
	StatusCounts(ctx context.Context) (map[string]int64, error)
	Growth30Days(ctx context.Context, since time.Time) (confirmed, unsubscribed int64, err error)
}

// dashboardInterestStore is the narrow interests.Store seam this handler
// needs.
type dashboardInterestStore interface {
	ListAll(ctx context.Context) ([]interests.Interest, error)
	SubscriberCounts(ctx context.Context) (map[int64]int64, error)
}

// dashboardCampaignStore is the narrow mailing.CampaignStore seam this
// handler needs.
type dashboardCampaignStore interface {
	List(ctx context.Context) ([]mailing.Campaign, error)
}

// dashboardStatsStore is the narrow mailing.CampaignStatsStore seam this
// handler needs.
type dashboardStatsStore interface {
	AccountComplaintRate(ctx context.Context) (complained, sent int64, err error)
}

// dashboardSettingsStore is the narrow settings seam this handler needs —
// the same ListSettings method settingStore (settings.go) depends on;
// *auth.Store satisfies both.
type dashboardSettingsStore interface {
	ListSettings(ctx context.Context) ([]auth.Setting, error)
}

// AdminDashboardHandler serves GET /admin/overview. Must be mounted behind
// middleware.RequireSession then middleware.RequireAdmin, exactly like every
// other admin handler — see cmd/opencircuit/main.go's adminRoutes.
type AdminDashboardHandler struct {
	subs      dashboardSubscriberStore
	interests dashboardInterestStore
	campaigns dashboardCampaignStore
	stats     dashboardStatsStore
	settings  dashboardSettingsStore
	// outbox reports queue depth/abandoned count (#0126). May be nil
	// (STORAGE=json has no outbound_queue backing); Overview omits the
	// figure and its warning rather than dereferencing it.
	outbox dashboardOutboxStore
	// sesSandbox mirrors config.Config.SESSandbox — see that field's doc
	// comment for why this is a manually-set flag, not a live query.
	sesSandbox bool
	// now is injectable so the 30-day growth window is deterministic in
	// tests, the same convention every other now-sensitive handler in this
	// package follows.
	now func() time.Time
}

// dashboardOutboxStore is the behavior AdminDashboardHandler needs from
// internal/outbox (#0126). *outbox.Store satisfies it via Counts.
type dashboardOutboxStore interface {
	Counts(ctx context.Context) (outbox.Counts, error)
}

// NewAdminDashboardHandler constructs an AdminDashboardHandler over the data
// layer. sesSandbox is config.Config.SESSandbox. outboxStore may be nil
// (STORAGE=json dev mode).
func NewAdminDashboardHandler(
	subs dashboardSubscriberStore,
	interestsStore dashboardInterestStore,
	campaigns dashboardCampaignStore,
	stats dashboardStatsStore,
	settings dashboardSettingsStore,
	outboxStore dashboardOutboxStore,
	sesSandbox bool,
) *AdminDashboardHandler {
	return &AdminDashboardHandler{
		subs: subs, interests: interestsStore, campaigns: campaigns,
		stats: stats, settings: settings, outbox: outboxStore, sesSandbox: sesSandbox,
		now: time.Now,
	}
}

// ── Response shapes ──────────────────────────────────────────────────────────

type dashboardSubscriberCounts struct {
	Pending      int64 `json:"pending"`
	Active       int64 `json:"active"`
	Unsubscribed int64 `json:"unsubscribed"`
	Bounced      int64 `json:"bounced"`
	Complained   int64 `json:"complained"`
}

// dashboardGrowth reports both directions of the 30-day window rather than a
// pre-subtracted net — "12 joined, 3 left" and "net +9" read differently to
// an operator, and the client can compute the net itself from these two.
type dashboardGrowth struct {
	Confirmed30d    int64 `json:"confirmed_30d"`
	Unsubscribed30d int64 `json:"unsubscribed_30d"`
	Net30d          int64 `json:"net_30d"`
}

type dashboardSubscribersView struct {
	Counts dashboardSubscriberCounts `json:"counts"`
	Growth dashboardGrowth           `json:"growth_30d"`
}

type dashboardInterestRow struct {
	ID              int64  `json:"id"`
	Slug            string `json:"slug"`
	Name            string `json:"name"`
	SubscriberCount int64  `json:"subscriber_count"`
}

type dashboardCampaignRow struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Status      string     `json:"status"`
	ScheduledAt *time.Time `json:"scheduled_at,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
}

func toDashboardCampaignRow(c mailing.Campaign) dashboardCampaignRow {
	return dashboardCampaignRow{
		ID: c.ID, Name: c.Name, Status: c.Status,
		ScheduledAt: c.ScheduledAt, StartedAt: c.StartedAt, CompletedAt: c.CompletedAt,
	}
}

// dashboardWarnings is the "what needs attention" row — the acceptance
// criteria's own framing for why this screen earns its place (issue notes:
// "an operator should learn that the physical address is missing here, not
// when a send is refused on announcement day"). Every boolean is a "should
// the operator see a warning banner" signal; the accompanying data fields
// give the banner something concrete to say rather than a bare flag.
type dashboardWarnings struct {
	// ComplaintRateReview (amber, #0227): the account-wide rate has crossed
	// AWS's 0.1% threshold — the account may be put under review and
	// returned to the sandbox, which would stop sending altogether.
	// ComplaintRateHigh (red): the account-wide rate has crossed Gmail/
	// Yahoo's 0.3% bulk-sender limit — mail will be filtered. The two are
	// independent booleans, not tiers of one scale: a rate above 0.3% sets
	// BOTH true, because both consequences apply simultaneously, and the
	// client renders each as its own distinct message (dashboard.ts's
	// buildWarnings) rather than collapsing them into one escalating
	// color.
	ComplaintRateReview bool `json:"complaint_rate_review"`
	ComplaintRateHigh   bool `json:"complaint_rate_high"`
	// ComplaintRatePct is omitted (nil) when the account-wide sample is
	// below dashboardComplaintMinSample — see this file's doc comment on
	// that constant. Never fabricated as 0 in that case, per CLAUDE.md §5's
	// "say so in the UI rather than presenting it as exact."
	ComplaintRatePct       *float64 `json:"complaint_rate_pct,omitempty"`
	ComplaintSampleSize    int64    `json:"complaint_sample_size"`
	PhysicalAddressUnset   bool     `json:"physical_address_unset"`
	SESSandboxActive       bool     `json:"ses_sandbox_active"`
	InboundMailUnavailable bool     `json:"inbound_mail_unavailable"`
	// OutboundQueueAbandoned (#0126): at least one outbound_queue row has
	// reached the terminal 'abandoned' state — a transactional message
	// (confirmation, registration, recovery, ...) that exhausted its
	// retries and was never delivered. False when outbox is nil
	// (STORAGE=json).
	OutboundQueueAbandoned bool `json:"outbound_queue_abandoned"`
}

// dashboardOutboundQueueView is #0126's queue-depth figure — the admin
// overview's answer to "is transactional mail actually flowing". Omitted
// (nil) from the response when outbox is nil (STORAGE=json), matching
// SendingCampaign's own omitempty convention above, rather than reporting
// fabricated zeros.
type dashboardOutboundQueueView struct {
	Queued              int64 `json:"queued"`
	Sending             int64 `json:"sending"`
	Sent                int64 `json:"sent"`
	Abandoned           int64 `json:"abandoned"`
	OldestQueuedAgeSecs int64 `json:"oldest_queued_age_seconds"`
}

type dashboardOverviewResponse struct {
	Subscribers     dashboardSubscribersView    `json:"subscribers"`
	Interests       []dashboardInterestRow      `json:"interests"`
	RecentCampaigns []dashboardCampaignRow      `json:"recent_campaigns"`
	OutboundQueue   *dashboardOutboundQueueView `json:"outbound_queue,omitempty"`
	// SendingCampaign is nil (omitted) when no campaign is currently
	// mid-send — the client's "any campaign currently sending, with live
	// progress" region shows nothing in that case rather than a
	// placeholder. When present, the client subscribes to the existing
	// campaign.progress SSE stream (GET /api/events) filtered to this id
	// for the live numbers; this response only says WHICH campaign, not
	// its live counts, since those move faster than a page load.
	SendingCampaign *dashboardCampaignRow `json:"sending_campaign,omitempty"`
	Warnings        dashboardWarnings     `json:"warnings"`
}

// Overview handles GET /admin/overview. Every read below is independent (no
// step depends on an earlier one's result), so a single failure is reported
// as one 500 rather than partially — matching this package's other
// multi-read handlers (e.g. AdminCampaignStatsHandler.Stats).
func (h *AdminDashboardHandler) Overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	counts, err := h.subs.StatusCounts(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	since := h.now().Add(-30 * 24 * time.Hour)
	confirmed30d, unsubscribed30d, err := h.subs.Growth30Days(ctx, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	interestRows, err := h.interestDistribution(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	campaigns, err := h.campaigns.List(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	recent, sending := recentAndSendingCampaigns(campaigns)

	complained, sent, err := h.stats.AccountComplaintRate(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	settings, err := h.settings.ListSettings(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// #0126: nil-guarded like every other STORAGE=json gap in this handler
	// — outbox is nil in dev mode, so the figure and its warning are
	// simply omitted rather than dereferencing it.
	var outboundQueue *dashboardOutboundQueueView
	var outboundQueueAbandoned bool
	if h.outbox != nil {
		counts, err := h.outbox.Counts(ctx)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		outboundQueue = &dashboardOutboundQueueView{
			Queued:              counts.Queued,
			Sending:             counts.Sending,
			Sent:                counts.Sent,
			Abandoned:           counts.Abandoned,
			OldestQueuedAgeSecs: counts.OldestQueuedAgeSecs,
		}
		outboundQueueAbandoned = counts.Abandoned > 0
	}

	warnings := h.buildWarnings(complained, sent, settings)
	warnings.OutboundQueueAbandoned = outboundQueueAbandoned

	writeJSON(w, http.StatusOK, dashboardOverviewResponse{
		Subscribers: dashboardSubscribersView{
			Counts: dashboardSubscriberCounts{
				Pending:      counts["pending"],
				Active:       counts["active"],
				Unsubscribed: counts["unsubscribed"],
				Bounced:      counts["bounced"],
				Complained:   counts["complained"],
			},
			Growth: dashboardGrowth{
				Confirmed30d:    confirmed30d,
				Unsubscribed30d: unsubscribed30d,
				Net30d:          confirmed30d - unsubscribed30d,
			},
		},
		Interests:       interestRows,
		RecentCampaigns: recent,
		OutboundQueue:   outboundQueue,
		SendingCampaign: sending,
		Warnings:        warnings,
	})
}

// interestDistribution joins interests.Store.ListAll (ordering and names)
// with SubscriberCounts (per-interest membership) — two queries total,
// regardless of interest count, mirroring the admin interests screen's own
// established shape (#0024).
//
// # Why no synthetic filter here, unlike Growth30Days
//
// SubscriberCounts counts subscriber_interests rows, not subscribers rows
// directly, so it falls outside the letter of #0061's amendment. In
// practice it is also outside the amendment's concern: the ONLY writer of
// synthetic=true rows is ensureTestRecipient
// (admin_campaign_preview.go), and it creates the row via
// subscribers.NewSignup{Email, ConfirmTTL, Synthetic: true} with no
// InterestIDs — nothing in this codebase ever calls SetInterests for a
// synthetic subscriber. A synthetic row therefore has zero
// subscriber_interests rows to inflate any bucket with. If a future change
// ever attaches interests to a synthetic row, this comment is the tripwire:
// SubscriberCounts (interests/store.go) would need the same synthetic
// exclusion Growth30Days carries.
func (h *AdminDashboardHandler) interestDistribution(ctx context.Context) ([]dashboardInterestRow, error) {
	all, err := h.interests.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	byInterest, err := h.interests.SubscriberCounts(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]dashboardInterestRow, 0, len(all))
	for _, it := range all {
		rows = append(rows, dashboardInterestRow{
			ID: it.ID, Slug: it.Slug, Name: it.Name,
			SubscriberCount: byInterest[it.ID],
		})
	}
	return rows, nil
}

// recentAndSendingCampaigns derives both the "recent campaigns" list and the
// "currently sending" campaign from ONE already-fetched slice — List() is
// called exactly once by Overview above; this is pure post-processing, not
// an additional query. campaigns is assumed sorted newest-first, which is
// mailing.CampaignStore.List's own documented order.
func recentAndSendingCampaigns(campaigns []mailing.Campaign) (recent []dashboardCampaignRow, sending *dashboardCampaignRow) {
	limit := dashboardRecentCampaignsLimit
	if limit > len(campaigns) {
		limit = len(campaigns)
	}
	recent = make([]dashboardCampaignRow, 0, limit)
	for _, c := range campaigns[:limit] {
		recent = append(recent, toDashboardCampaignRow(c))
	}
	for _, c := range campaigns {
		if c.Status == mailing.CampaignStatusSending {
			row := toDashboardCampaignRow(c)
			return recent, &row
		}
	}
	return recent, nil
}

// buildWarnings assembles the "what needs attention" row. settings is the
// full ListSettings result — physical_address is looked up from it rather
// than fetched by a dedicated query, since ListSettings already reads the
// whole (small) table.
func (h *AdminDashboardHandler) buildWarnings(complained, sent int64, settings []auth.Setting) dashboardWarnings {
	w := dashboardWarnings{
		SESSandboxActive: h.sesSandbox,
		// PRD §6.5 path 3 / #0058 is not built — see this file's package
		// doc comment. Always true until that ships.
		InboundMailUnavailable: true,
		ComplaintSampleSize:    sent,
	}

	if sent >= dashboardComplaintMinSample {
		pct := float64(complained) / float64(sent) * 100
		w.ComplaintRatePct = &pct
		// Both bands use >=, matching the ## Decision table in #0227
		// (both "≥"): a rate landing exactly ON either boundary is inside
		// the band, not below it — mirroring PRD §6.9's own "under 0.1%"/
		// "under 0.3%" framing, where the safe zone is strictly under and
		// the boundary value itself is already the violation.
		w.ComplaintRateReview = pct >= dashboardComplaintReviewThresholdPct
		w.ComplaintRateHigh = pct >= dashboardComplaintRateThresholdPct
	}

	addr := ""
	for _, s := range settings {
		if s.Key == "physical_address" {
			addr = s.Value
			break
		}
	}
	w.PhysicalAddressUnset = strings.TrimSpace(addr) == ""

	return w
}
