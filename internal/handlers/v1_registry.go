package handlers

import (
	"github.com/brennanMKE/OpenCircuitSF/internal/api"
)

// V1Deps are the stores the /api/v1 operations read and write. #0676,
// #0677 and #0678 each add theirs here.
type V1Deps struct {
	Engagement engagementWindowStore
}

// BuildV1Registry is the one place the v1 operations are listed (#0675).
// The REST routes (api.NewHTTPRouter), the OpenAPI document and #0679's MCP
// tools all come from the registry it returns. Building an operation never
// touches its store, so a parity test may pass nil stores.
func BuildV1Registry(cfg api.Config, d V1Deps) *api.Registry {
	reg := api.NewRegistry(cfg)
	reg.Add(api.CoreOperations(cfg.Version)...)
	reg.Add(EngagementOperation(d.Engagement))
	return reg
}
