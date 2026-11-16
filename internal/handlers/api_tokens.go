package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// apiTokenStore is the narrow store APITokensHandler needs (#0652);
// *apitokens.Store satisfies it.
type apiTokenStore interface {
	Create(ctx context.Context, name string, scopes []string, createdBy *int64, expiresAt *time.Time, now time.Time) (apitokens.Token, string, error)
	List(ctx context.Context) ([]apitokens.Token, error)
	Revoke(ctx context.Context, id int64, now time.Time) (apitokens.Token, error)
	Resolve(ctx context.Context, secret string, now time.Time) (apitokens.Token, error)
}

// APITokensHandler serves the admin API token routes and carries the
// /api/v1 operation registry (#0652, #0675) to mountAndServe. Nothing here
// logs a request body or a token secret.
type APITokensHandler struct {
	store   apiTokenStore
	auditor *audit.Logger
	now     func() time.Time
	v1      *api.Registry
}

// NewAPITokensHandler returns a handler over store. A nil auditor disables
// audit writes. Attach the v1 registry with SetV1.
func NewAPITokensHandler(store apiTokenStore, auditor *audit.Logger) *APITokensHandler {
	return &APITokensHandler{store: store, auditor: auditor, now: time.Now}
}

// SetV1 attaches the /api/v1 operation registry (BuildV1Registry).
func (h *APITokensHandler) SetV1(reg *api.Registry) { h.v1 = reg }

// V1 returns the registry SetV1 attached, or nil.
func (h *APITokensHandler) V1() *api.Registry { return h.v1 }

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
	ExpiresAt  *time.Time `json:"expires_at"`
}

func toAPITokenView(t apitokens.Token) apiTokenView {
	scopes := t.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return apiTokenView{ID: t.ID, Name: t.Name, Prefix: t.Prefix, Scopes: scopes,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, RevokedAt: t.RevokedAt, ExpiresAt: t.ExpiresAt}
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

// createAPITokenRequest is POST /admin/api-tokens's body. ExpiresAt is RFC
// 3339 and optional (#0674): null or absent makes a token that never expires.
type createAPITokenRequest struct {
	Name      string     `json:"name"`
	Scopes    []string   `json:"scopes"`
	ExpiresAt *time.Time `json:"expires_at"`
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
	now := h.now()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		writeError(w, http.StatusBadRequest, "expiry must be in the future")
		return
	}
	tok, secret, err := h.store.Create(r.Context(), name, scopes, &u.ID, req.ExpiresAt, now)
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
			Metadata:   map[string]any{"name": tok.Name, "prefix": tok.Prefix, "scopes": tok.Scopes, "expires_at": tok.ExpiresAt},
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
