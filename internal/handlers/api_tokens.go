package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// apiTokenStore is the narrow store APITokensHandler needs (#0652);
// *apitokens.Store satisfies it.
type apiTokenStore interface {
	Create(ctx context.Context, name string, scopes []string, createdBy *int64, now time.Time) (apitokens.Token, string, error)
	List(ctx context.Context) ([]apitokens.Token, error)
	Revoke(ctx context.Context, id int64, now time.Time) (apitokens.Token, error)
	Resolve(ctx context.Context, secret string, now time.Time) (apitokens.Token, error)
}

// APIv1Route is one token-authenticated route. mountAndServe registers each as GET only.
type APIv1Route struct {
	Path    string // must start with "/api/v1/"
	Scope   string // "" = any valid token
	Handler http.Handler
}

// APITokensHandler serves the admin API token routes and owns the list of
// token-authenticated /api/v1/ routes (#0652). Nothing here logs a request
// body or a token secret.
type APITokensHandler struct {
	store   apiTokenStore
	auditor *audit.Logger
	now     func() time.Time
	v1      []APIv1Route
}

// NewAPITokensHandler returns a handler over store. A nil auditor disables
// audit writes. It registers the built-in GET /api/v1/token probe.
func NewAPITokensHandler(store apiTokenStore, auditor *audit.Logger) *APITokensHandler {
	h := &APITokensHandler{store: store, auditor: auditor, now: time.Now}
	h.v1 = append(h.v1, APIv1Route{Path: "/api/v1/token", Scope: "", Handler: http.HandlerFunc(h.Describe)})
	return h
}

// AddV1Route adds a token-authenticated route. It panics on a path outside
// /api/v1/ or an unknown scope, since either is a wiring bug.
func (h *APITokensHandler) AddV1Route(path, scope string, handler http.Handler) {
	if !strings.HasPrefix(path, "/api/v1/") {
		panic("handlers: API v1 route outside /api/v1/: " + path)
	}
	if scope != "" && !apitokens.IsKnownScope(scope) {
		panic("handlers: API v1 route with unknown scope: " + scope)
	}
	h.v1 = append(h.v1, APIv1Route{Path: path, Scope: scope, Handler: handler})
}

// V1Routes returns the token-authenticated routes.
func (h *APITokensHandler) V1Routes() []APIv1Route { return h.v1 }

// Resolver returns the token resolver RequireAPIToken uses.
func (h *APITokensHandler) Resolver() middleware.APITokenResolver { return h.store }

// apiTokenView is the JSON shape of a token. It has no secret field.
type apiTokenView struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

func toAPITokenView(t apitokens.Token) apiTokenView {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return apiTokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: scopes,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt}
}

// List handles GET /admin/api-tokens.
func (h *APITokensHandler) List(w http.ResponseWriter, r *http.Request) {
	toks, err := h.store.List(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	views := make([]apiTokenView, 0, len(toks))
	for _, t := range toks {
		views = append(views, toAPITokenView(t))
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": views})
}

type createAPITokenRequest struct {
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

// Create handles POST /admin/api-tokens. The secret is in this response only.
func (h *APITokensHandler) Create(w http.ResponseWriter, r *http.Request) {
	u, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req createAPITokenRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if utf8.RuneCountInString(name) > 100 {
		writeError(w, http.StatusBadRequest, "name must be 100 characters or fewer")
		return
	}
	if len(req.Scopes) == 0 {
		writeError(w, http.StatusBadRequest, "at least one scope is required")
		return
	}
	scopes := make([]string, 0, len(req.Scopes))
	for _, s := range req.Scopes {
		if !apitokens.IsKnownScope(s) {
			writeError(w, http.StatusBadRequest, "unknown scope: "+s)
			return
		}
		dup := false
		for _, have := range scopes {
			if have == s {
				dup = true
				break
			}
		}
		if !dup {
			scopes = append(scopes, s)
		}
	}
	tok, secret, err := h.store.Create(r.Context(), name, scopes, &u.ID, h.now())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if h.auditor != nil {
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &u.ID,
			Action:     audit.ActionAPITokenCreated,
			TargetType: audit.TargetAPIToken,
			TargetID:   &tok.ID,
			Metadata:   map[string]any{"name": tok.Name, "prefix": tok.Prefix, "scopes": tok.Scopes},
			IP:         clientIP(r),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": toAPITokenView(tok), "secret": secret})
}

// Revoke handles POST /admin/api-tokens/{id}/revoke.
func (h *APITokensHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	u, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	tok, err := h.store.Revoke(r.Context(), id, h.now())
	switch {
	case errors.Is(err, apitokens.ErrNotFound):
		writeError(w, http.StatusNotFound, "token not found")
		return
	case errors.Is(err, apitokens.ErrAlreadyRevoked):
		writeError(w, http.StatusConflict, "token already revoked")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if h.auditor != nil {
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &u.ID,
			Action:     audit.ActionAPITokenRevoked,
			TargetType: audit.TargetAPIToken,
			TargetID:   &tok.ID,
			Metadata:   map[string]any{"name": tok.Name, "prefix": tok.Prefix},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"token": toAPITokenView(tok)})
}

// Describe handles GET /api/v1/token: it names the presenting token, so a
// client (Home Assistant, #0654) can check that its token works.
func (h *APITokensHandler) Describe(w http.ResponseWriter, r *http.Request) {
	t, ok := middleware.APITokenFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": t.Name, "prefix": t.Prefix, "scopes": t.Scopes})
}
