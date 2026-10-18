package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/serversettings"
)

// serverSettings is the slice of *serversettings.Manager this handler uses.
type serverSettings interface {
	Snapshot() serversettings.Snapshot
	Apply(ctx context.Context, values map[string]string, now time.Time) (serversettings.Applied, error)
}

// AdminServerSettingsHandler serves the admin-only routes behind the "Server
// settings" area of Settings (#0566): read the state, and save or clear
// values. All routes MUST be mounted behind RequireSession then RequireAdmin.
// No request body is ever logged, and the audit metadata names field keys
// only, never values.
type AdminServerSettingsHandler struct {
	settings serverSettings
	auditor  *audit.Logger
	now      func() time.Time
}

// NewAdminServerSettingsHandler constructs the handler. A nil auditor
// disables audit writes.
func NewAdminServerSettingsHandler(s serverSettings, auditor *audit.Logger) *AdminServerSettingsHandler {
	return &AdminServerSettingsHandler{settings: s, auditor: auditor, now: time.Now}
}

type serverSettingsView struct {
	Groups []serverSettingGroupView `json:"groups"`
}

type serverSettingGroupView struct {
	Name   string                   `json:"name"`
	Error  string                   `json:"error"`
	Fields []serverSettingFieldView `json:"fields"`
}

type serverSettingFieldView struct {
	Key             string `json:"key"`
	EnvVar          string `json:"env_var"`
	Source          string `json:"source"`
	Value           string `json:"value"`
	Default         string `json:"default"`
	RestartRequired bool   `json:"restart_required"`
	PendingRestart  bool   `json:"pending_restart"`
}

func toServerSettingsView(s serversettings.Snapshot) serverSettingsView {
	v := serverSettingsView{Groups: make([]serverSettingGroupView, 0, len(s.Groups))}
	for _, g := range s.Groups {
		gv := serverSettingGroupView{Name: g.Name, Error: g.Error, Fields: make([]serverSettingFieldView, 0, len(g.Fields))}
		for _, f := range g.Fields {
			gv.Fields = append(gv.Fields, serverSettingFieldView{
				Key: f.Key, EnvVar: f.EnvVar, Source: string(f.Source), Value: f.Value,
				Default: f.EffectiveDefault, RestartRequired: f.RestartRequired, PendingRestart: f.PendingRestart,
			})
		}
		v.Groups = append(v.Groups, gv)
	}
	return v
}

// Get handles GET /admin/server-settings.
func (h *AdminServerSettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toServerSettingsView(h.settings.Snapshot()))
}

type patchServerSettingsRequest struct {
	Values map[string]*string `json:"values"`
}

// Patch handles PATCH /admin/server-settings. A key that is absent is
// unchanged, a non-blank string sets the field, and a blank string clears the
// saved value so the field falls back to its environment variable or default.
// Every entry is validated before anything is written.
func (h *AdminServerSettingsHandler) Patch(w http.ResponseWriter, r *http.Request) {
	u, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}
	var req patchServerSettingsRequest
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
		if _, known := serversettings.FieldByKey(k); !known {
			writeError(w, http.StatusBadRequest, "unknown key: "+k)
			return
		}
		if v == nil {
			writeError(w, http.StatusBadRequest, "values."+k+" must be a string")
			return
		}
		values[k] = *v
	}
	applied, err := h.settings.Apply(r.Context(), values, h.now())
	if err != nil {
		var ve *serversettings.ValidationError
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
			Action:     audit.ActionServerSettingsUpdated,
			TargetType: audit.TargetServerSettings,
			Metadata:   map[string]any{"set": setKeys, "cleared": clearedKeys},
			IP:         clientIP(r),
		})
	}
	writeJSON(w, http.StatusOK, toServerSettingsView(h.settings.Snapshot()))
}
