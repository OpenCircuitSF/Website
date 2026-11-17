package handlers

import (
	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// V1Deps are the stores the /api/v1 operations read and write. #0676,
// #0677 and #0678 each add theirs here.
type V1Deps struct {
	Engagement engagementWindowStore
	// BaseURL is the site's BASE_URL, for the public URLs ops return.
	BaseURL string
	// GuidePosts is the admin guide-posts handler; the #0676 operations run
	// its transport-free methods, so v1 and /admin/guide-posts share one
	// code path.
	GuidePosts *AdminGuidePostsHandler
	// Workshops is the admin workshops handler; the #0677 event operations
	// run its transport-free createWorkshop and patchWorkshop, so v1 and
	// /admin/workshops share one code path.
	Workshops *AdminWorkshopsHandler
	// Venues, EventTypes and Interests back the #0677 lookups and the
	// interest names on an event.
	Venues     venueLister
	EventTypes eventTypeLister
	Interests  interestLister
	// Cadence is the #0678 event coverage service (cadence rules and the
	// coverage report), shared with /admin/event-cadence.
	Cadence *EventCadenceHandler
	// Outreach is the admin outreach handler; the #0697 outreach operations
	// run its transport-free methods, shared with /admin/outreach.
	Outreach *AdminOutreachHandler
}

// BuildV1Registry is the one place the v1 operations are listed (#0675).
// The REST routes (api.NewHTTPRouter), the OpenAPI document and #0679's MCP
// tools all come from the registry it returns. Building an operation never
// touches its store, so a parity test may pass nil stores.
func BuildV1Registry(cfg api.Config, d V1Deps) *api.Registry {
	reg := api.NewRegistry(cfg)
	reg.Add(api.CoreOperations(cfg.Version)...)
	reg.Add(EngagementOperation(d.Engagement))
	reg.Add(guideOperations(d.BaseURL, d.GuidePosts)...)
	reg.Add(eventOperations(d)...)
	reg.Add(coverageOperations(d.Cadence)...)
	reg.Add(outreachOperations(d.Outreach)...)
	reg.Add(outreachWriteOperations(d.Outreach)...)
	reg.Add(outreachLinkOperations(d.Outreach)...)
	return reg
}
