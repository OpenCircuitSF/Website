package handlers

import (
	"encoding/json"
	"net/http"
)

// VersionHandler serves GET /api/version: the running build's identity, which
// open SPA tabs compare against the oc-build meta stamped into their page to
// notice a newer deploy (#0715). It is separate from /admin/version because
// that route is admin-gated and reads the database; this one is public,
// touches nothing, and carries only what `opencircuit --version` prints.
type VersionHandler struct{ version, commit string }

// NewVersionHandler returns a VersionHandler for the given version and commit.
func NewVersionHandler(version, commit string) *VersionHandler {
	return &VersionHandler{version: version, commit: commit}
}

func (h *VersionHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Build   string `json:"build"`
	}{h.version, h.commit, h.version + "+" + h.commit})
}
