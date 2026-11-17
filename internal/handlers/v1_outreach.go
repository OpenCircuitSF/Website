package handlers

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
	"github.com/brennanMKE/OpenCircuitSF/internal/workshops"
)

// recordCtx writes e against targetType id, filling the target and IP, for
// the /api/v1 outreach operations (#0697; #0699-#0703 reuse it). ip ""
// takes the IP the v1 API put in ctx (api.ClientIPFromContext). An
// API-token request is attributed to its token; for a session request that
// is a no-op, so admin rows are unchanged.
func (h *AdminOutreachHandler) recordCtx(ctx context.Context, ip, targetType string, id int64, e audit.Entry) {
	if h.auditor == nil {
		return
	}
	e.TargetType = targetType
	e.TargetID = &id
	if ip == "" {
		ip = api.ClientIPFromContext(ctx)
	}
	e.IP = ip
	e = middleware.AttributeAPIToken(ctx, e)
	h.auditor.Record(ctx, e)
}

// outreachOperations are the /api/v1 event outreach reads (#0697). Building
// them never touches h: the closures read it only when called, so a parity
// test may pass nil. Each runs the same transport-free method as its
// /admin route.
func outreachOperations(h *AdminOutreachHandler) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "list_outreach", Method: http.MethodGet, Path: "/api/v1/outreach",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "List every event that has outreach, newest outreach first, with post counts by status.",
			Description: "Counts leave out drafts on a channel switched off in Settings. Times have a Pacific offset.",
		}, func(ctx context.Context, in listOutreachIn) (api.List[outreachSummaryV1], error) {
			return h.listOutreachV1(ctx, in)
		}),
		api.Define(api.Operation{
			Name: "get_event_outreach", Method: http.MethodGet, Path: "/api/v1/events/{id}/outreach",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "Get an event's outreach panel: availability, channels, waves, posts and hand-posted links.",
			Description: "The same panel the admin Outreach screen shows. started is false when outreach has not been started for the event.",
		}, func(ctx context.Context, in eventOutreachIn) (eventOutreachV1, error) {
			v, err := h.panelFor(ctx, in.ID)
			if errors.Is(err, workshops.ErrNotFound) {
				return eventOutreachV1{}, api.NotFound("event")
			}
			if err != nil {
				return eventOutreachV1{}, err
			}
			return eventOutreachV1{Started: v.Outreach != nil, outreachPanelView: v}, nil
		}),
		api.Define(api.Operation{
			Name: "outreach_connections_status", Method: http.MethodGet, Path: "/api/v1/outreach/connections",
			Scope:       apitokens.ScopeOutreachRead,
			Summary:     "Whether ShortLinks, Bluesky and Mastodon are connected, and which channels are switched on.",
			Description: "Status only: whether each setting is set and where it comes from (env or saved). No value is ever returned.",
		}, func(ctx context.Context, _ struct{}) (outreachConnectionsV1, error) {
			return h.connectionsStatusV1(), nil
		}),
	}
}

type listOutreachIn struct {
	api.Page
}

type eventOutreachIn struct {
	ID int64 `json:"id" path:"id"`
}

// outreachSummaryV1 is one list_outreach row: the admin summary with its
// times in Pacific.
type outreachSummaryV1 struct {
	OutreachID     int64          `json:"outreach_id"`
	WorkshopID     int64          `json:"workshop_id" jsonschema:"the event id"`
	WorkshopSlug   string         `json:"workshop_slug"`
	WorkshopTitle  string         `json:"workshop_title"`
	WorkshopStatus string         `json:"workshop_status"`
	CampaignSlug   string         `json:"campaign_slug" jsonschema:"the event's ShortLinks campaign"`
	StartsAt       *string        `json:"starts_at"`
	Counts         map[string]int `json:"counts" jsonschema:"posts by status"`
	CreatedAt      string         `json:"created_at"`
}

// outreachCursor is list_outreach's cursor: the last outreach id returned.
type outreachCursor struct {
	ID int64 `json:"id"`
}

// eventOutreachV1 is get_event_outreach's answer: the admin panel, field for
// field, plus started.
type eventOutreachV1 struct {
	Started bool `json:"started" jsonschema:"outreach has been started for this event"`
	outreachPanelView
}

type outreachConnectionsV1 struct {
	Channels []outreachChannelStatusV1 `json:"channels"`
	Groups   []outreachGroupStatusV1   `json:"groups"`
}

type outreachChannelStatusV1 struct {
	Name       string `json:"name"`
	Automatic  bool   `json:"automatic" jsonschema:"the site posts to this channel itself; otherwise it is posted by hand"`
	Enabled    bool   `json:"enabled" jsonschema:"switched on in Settings"`
	Configured bool   `json:"configured" jsonschema:"an automatic channel whose account is connected"`
}

type outreachGroupStatusV1 struct {
	Name       string                  `json:"name"`
	Configured bool                    `json:"configured"`
	Fields     []outreachFieldStatusV1 `json:"fields"`
}

// outreachFieldStatusV1 deliberately has no value field, secret or not.
type outreachFieldStatusV1 struct {
	Key    string `json:"key"`
	Secret bool   `json:"secret"`
	Set    bool   `json:"set"`
	Source string `json:"source" jsonschema:"environment, database, default or unset"`
}

func pacificRFC3339(s string) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.In(announceLocation).Format(time.RFC3339)
}

func (h *AdminOutreachHandler) listOutreachV1(ctx context.Context, in listOutreachIn) (api.List[outreachSummaryV1], error) {
	views, err := h.listSummaries(ctx)
	if err != nil {
		return api.List[outreachSummaryV1]{}, err
	}
	items := make([]outreachSummaryV1, 0, len(views))
	for _, v := range views {
		s := outreachSummaryV1(v)
		if s.StartsAt != nil {
			p := pacificRFC3339(*s.StartsAt)
			s.StartsAt = &p
		}
		s.CreatedAt = pacificRFC3339(s.CreatedAt)
		items = append(items, s)
	}
	slices.SortFunc(items, func(a, b outreachSummaryV1) int {
		switch {
		case a.OutreachID > b.OutreachID:
			return -1
		case a.OutreachID < b.OutreachID:
			return 1
		}
		return 0
	})
	return paginate(items, in.Page,
		func(s outreachSummaryV1) outreachCursor { return outreachCursor{ID: s.OutreachID} },
		func(s outreachSummaryV1, c outreachCursor) bool { return s.OutreachID < c.ID })
}

// connectionsStatusV1 is booleans and sources only: no field value of any
// kind is copied out of the snapshot.
func (h *AdminOutreachHandler) connectionsStatusV1() outreachConnectionsV1 {
	out := outreachConnectionsV1{Channels: []outreachChannelStatusV1{}, Groups: []outreachGroupStatusV1{}}
	for _, ch := range outreach.Channels() {
		out.Channels = append(out.Channels, outreachChannelStatusV1{
			Name: ch.Name, Automatic: ch.Automatic, Enabled: h.channelEnabled(ch.Name), Configured: h.channelConfigured(ch.Name),
		})
	}
	if h.conns == nil || h.conns.conns == nil {
		return out
	}
	for _, g := range h.conns.conns.Snapshot().Groups {
		gv := outreachGroupStatusV1{Name: g.Name, Configured: g.Configured, Fields: []outreachFieldStatusV1{}}
		for _, f := range g.Fields {
			gv.Fields = append(gv.Fields, outreachFieldStatusV1{
				Key: f.Key, Secret: f.Secret, Set: f.Source != outreachconn.SourceUnset, Source: string(f.Source),
			})
		}
		out.Groups = append(out.Groups, gv)
	}
	return out
}
