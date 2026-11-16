package api

import (
	"context"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// WhoAmI is whoami's output. It never carries the secret.
type WhoAmI struct {
	Name          string     `json:"name" jsonschema:"the API key's name"`
	Prefix        string     `json:"prefix" jsonschema:"the key's display prefix, safe to show"`
	Scopes        []string   `json:"scopes" jsonschema:"the scopes this key holds; a :write scope implies the matching :read"`
	ExpiresAt     *time.Time `json:"expires_at" jsonschema:"when the key stops working; null if it never expires"`
	ServerVersion string     `json:"server_version"`
	APIVersion    string     `json:"api_version" jsonschema:"always v1"`
}

type whoAmIInput struct{}

// CoreOperations are the operations every registry has: whoami at
// /api/v1/me, and token_probe at /api/v1/token, #0652's probe URL kept as a
// deprecated alias for Home Assistant. Both take any valid key.
func CoreOperations(version string) []Operation {
	fn := func(ctx context.Context, _ whoAmIInput) (WhoAmI, error) {
		t, ok := middleware.APITokenFromContext(ctx)
		if !ok {
			return WhoAmI{}, &Error{Status: http.StatusUnauthorized, Code: CodeUnauthorized, Message: "a valid API key is required"}
		}
		scopes := t.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		return WhoAmI{Name: t.Name, Prefix: t.Prefix, Scopes: scopes, ExpiresAt: t.ExpiresAt, ServerVersion: version, APIVersion: "v1"}, nil
	}
	return []Operation{
		Define(Operation{
			Name: "whoami", Method: http.MethodGet, Path: "/api/v1/me", AnyToken: true,
			Summary: "Show this API key's name, scopes and expiry, and the server version. Use it to check a connection.",
		}, fn),
		Define(Operation{
			Name: "token_probe", Method: http.MethodGet, Path: "/api/v1/token", AnyToken: true,
			Summary:     "Deprecated alias of /api/v1/me, kept for Home Assistant's connection check.",
			MCPExcluded: "deprecated alias of whoami", Deprecated: true,
		}, fn),
	}
}
