// Admin build info (#0516): GET /admin/version — the running binary's
// semantic version and embedded commit hash, plus the database's
// schema_migrations version, so an admin can answer "what is production
// running?" from the console instead of an SSH session.
//
// # Where the values come from
//
// version and commitHash are the SAME package-level variables
// cmd/opencircuit's --version output reads (criterion 2) — main.go passes
// them in at construction, so there is no second, independently-maintained
// constant that can drift. A binary built without -ldflags carries
// commitHash "unknown" (#0509's deliberate loud placeholder), and this
// handler reports it as-is (criterion 4): the short form passes the value
// through unchanged rather than truncating or blanking it.
//
// # Why an admin-scoped route, not /health
//
// Criterion 5: PRD §8 fixes /health's response shape (liveness + DB
// connectivity) and it is unauthenticated — a different question from "which
// code and which schema is this process running". This route is registered
// through cmd/opencircuit's adminRoutes, so it carries RequireSession then
// RequireAdmin exactly like every other admin route.
//
// # The schema version (criterion 6)
//
// Included deliberately: "which code" and "which schema" are two halves of
// one question, and a mismatch between them is what a bad deploy looks like
// — the query is one SELECT against schema_migrations (internal/db's
// SchemaReader). Under STORAGE=json dev mode there is no schema_migrations,
// so the seam is nil there and both schema fields are omitted (the same
// nil-seam convention admin_dashboard.go uses for outbox) rather than
// reporting a fabricated value.
package handlers

import (
	"context"
	"net/http"
	"regexp"
)

// commitShortRe matches a value that looks like a full git sha — 7 or more
// lowercase hex characters (deploy.sh embeds the full 40-character
// `git rev-parse HEAD` output). Anything else ("unknown", a truncated
// value, garbage) is passed through untouched: the short form of a value we
// cannot recognize as a sha is the value itself, never a fabricated
// truncation of it (criterion 4).
var commitShortRe = regexp.MustCompile(`^[0-9a-f]{7,}$`)

// commitShortForm returns the conventional 7-character short sha for a
// value that looks like one, or the value unchanged otherwise.
func commitShortForm(hash string) string {
	if commitShortRe.MatchString(hash) {
		return hash[:7]
	}
	return hash
}

// versionSchemaSource is the narrow seam this handler needs for the
// schema_migrations stamp. *db.SchemaReader satisfies it. Nil (dev mode)
// omits the schema fields from the response — see this file's package doc
// comment.
type versionSchemaSource interface {
	SchemaVersion(ctx context.Context) (version int64, dirty bool, err error)
}

// AdminVersionHandler serves GET /admin/version. Must be mounted behind
// middleware.RequireSession then middleware.RequireAdmin, exactly like every
// other admin handler — see cmd/opencircuit/main.go's adminRoutes.
type AdminVersionHandler struct {
	version    string
	commitHash string
	schema     versionSchemaSource
}

// NewAdminVersionHandler constructs the handler from the main package's own
// version and commitHash variables (criterion 2: the same values --version
// prints — no second constant). schema may be nil (STORAGE=json dev mode).
func NewAdminVersionHandler(version, commitHash string, schema versionSchemaSource) *AdminVersionHandler {
	return &AdminVersionHandler{version: version, commitHash: commitHash, schema: schema}
}

// adminVersionResponse is the JSON body of GET /admin/version. The schema
// fields are pointers so a nil seam (dev mode) can omit them entirely rather
// than report a zero that would read as "schema version 0".
type adminVersionResponse struct {
	Version         string `json:"version"`
	CommitHash      string `json:"commit_hash"`
	CommitHashShort string `json:"commit_hash_short"`
	SchemaVersion   *int64 `json:"schema_version,omitempty"`
	SchemaDirty     bool   `json:"schema_dirty,omitempty"`
}

// Version handles GET /admin/version.
func (h *AdminVersionHandler) Version(w http.ResponseWriter, r *http.Request) {
	resp := adminVersionResponse{
		Version:         h.version,
		CommitHash:      h.commitHash,
		CommitHashShort: commitShortForm(h.commitHash),
	}

	// Nil seam (STORAGE=json has no schema_migrations): omit rather than
	// dereference or fabricate — the same convention admin_dashboard.go's
	// outbox field follows for dev mode.
	if h.schema != nil {
		version, dirty, err := h.schema.SchemaVersion(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		resp.SchemaVersion = &version
		resp.SchemaDirty = dirty
	}

	writeJSON(w, http.StatusOK, resp)
}
