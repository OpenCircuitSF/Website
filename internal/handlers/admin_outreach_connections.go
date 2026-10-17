package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/outreachconn"
)

// outreachConnections is the slice of *outreachconn.Manager this handler
// uses.
type outreachConnections interface {
	Snapshot() outreachconn.Snapshot
	Apply(ctx context.Context, values map[string]string, now time.Time) (outreachconn.Applied, error)
	Test(ctx context.Context, group string) outreachconn.TestResult
}

// AdminOutreachConnectionsHandler serves the admin-only routes behind the
// "Outreach connections" area of Settings (#0564): read the state, save or
// clear credentials, and test a group against its service. All routes MUST be
// mounted behind RequireSession then RequireAdmin.
//
// Secrets are write-only. A secret field is rendered from a struct with no
// value field at all, so no code path can put one in a response, and no
// request body is ever logged or audited.
type AdminOutreachConnectionsHandler struct {
	conns   outreachConnections
	auditor *audit.Logger
	now     func() time.Time
}

// NewAdminOutreachConnectionsHandler constructs the handler. A nil auditor
// disables audit writes.
func NewAdminOutreachConnectionsHandler(conns outreachConnections, auditor *audit.Logger) *AdminOutreachConnectionsHandler {
	return &AdminOutreachConnectionsHandler{conns: conns, auditor: auditor, now: time.Now}
}

type connectionsView struct {
	Groups []connectionGroupView `json:"groups"`
}

type connectionGroupView struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Error      string `json:"error"`
	Fields     []any  `json:"fields"`
}

// plainFieldView is a non-secret field; it carries its effective value.
type plainFieldView struct {
	Key    string `json:"key"`
	EnvVar string `json:"env_var"`
	Secret bool   `json:"secret"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// secretFieldView is a secret field. It deliberately has no value field.
type secretFieldView struct {
	Key    string `json:"key"`
	EnvVar string `json:"env_var"`
	Secret bool   `json:"secret"`
	Source string `json:"source"`
	Set    bool   `json:"set"`
}

type connectionTestView struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
	Error  string `json:"error,omitempty"`
}

func toConnectionsView(s outreachconn.Snapshot) connectionsView {
	v := connectionsView{Groups: make([]connectionGroupView, 0, len(s.Groups))}
	for _, g := range s.Groups {
		gv := connectionGroupView{Name: g.Name, Configured: g.Configured, Error: g.Error, Fields: make([]any, 0, len(g.Fields))}
		for _, f := range g.Fields {
			if f.Secret {
				gv.Fields = append(gv.Fields, secretFieldView{
					Key: f.Key, EnvVar: f.EnvVar, Secret: true, Source: string(f.Source), Set: f.Source != outreachconn.SourceUnset,
				})
				continue
			}
			gv.Fields = append(gv.Fields, plainFieldView{
				Key: f.Key, EnvVar: f.EnvVar, Secret: false, Source: string(f.Source), Value: f.Value,
			})
		}
		v.Groups = append(v.Groups, gv)
	}
	return v
}

// Get handles GET /admin/outreach-connections.
func (h *AdminOutreachConnectionsHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toConnectionsView(h.conns.Snapshot()))
}

type patchConnectionsRequest struct {
	Values map[string]*string `json:"values"`
}

// Patch handles PATCH /admin/outreach-connections. A key that is absent is
// unchanged, a non-blank string sets the field, and a blank string clears the
// saved value so the field falls back to its environment variable. Every
// entry is validated before anything is written.
func (h *AdminOutreachConnectionsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	u, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req patchConnectionsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Values) == 0 {
		writeError(w, http.StatusBadRequest, "values is required")
		return
	}
	values := make(map[string]string, len(req.Values))
	for k, v := range req.Values {
		if _, known := outreachconn.FieldByKey(k); !known {
			writeError(w, http.StatusBadRequest, "unknown key: "+k)
			return
		}
		if v == nil {
			writeError(w, http.StatusBadRequest, "values."+k+" must be a string")
			return
		}
		values[k] = *v
	}
	applied, err := h.conns.Apply(r.Context(), values, h.now())
	if err != nil {
		var ve *outreachconn.ValidationError
		if errors.As(err, &ve) {
			writeError(w, http.StatusBadRequest, ve.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if len(applied.Set)+len(applied.Cleared) > 0 && h.auditor != nil {
		actor := u.ID
		setKeys, clearedKeys := applied.Set, applied.Cleared
		if setKeys == nil {
			setKeys = []string{}
		}
		if clearedKeys == nil {
			clearedKeys = []string{}
		}
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actor,
			Action:     audit.ActionOutreachConnectionsUpdated,
			TargetType: audit.TargetOutreachConnections,
			Metadata:   map[string]any{"set": setKeys, "cleared": clearedKeys},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, toConnectionsView(h.conns.Snapshot()))
}

func (h *AdminOutreachConnectionsHandler) test(w http.ResponseWriter, r *http.Request, group string) {
	res := h.conns.Test(r.Context(), group)
	writeJSON(w, http.StatusOK, connectionTestView{OK: res.OK, Detail: res.Detail, Error: res.Error})
}

// Handles POST /admin/outreach-connections/shortlinks/test.
func (h *AdminOutreachConnectionsHandler) TestShortLinks(w http.ResponseWriter, r *http.Request) {
	h.test(w, r, outreachconn.GroupShortLinks)
}

// Handles POST /admin/outreach-connections/bluesky/test.
func (h *AdminOutreachConnectionsHandler) TestBluesky(w http.ResponseWriter, r *http.Request) {
	h.test(w, r, outreachconn.GroupBluesky)
}

// Handles POST /admin/outreach-connections/mastodon/test.
func (h *AdminOutreachConnectionsHandler) TestMastodon(w http.ResponseWriter, r *http.Request) {
	h.test(w, r, outreachconn.GroupMastodon)
}
