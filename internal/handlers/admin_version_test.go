package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brennanMKE/OpenCircuitSF/internal/db"
)

// fakeVersionSchema is a stub seam for the non-DB tests: it returns a fixed
// version/dirty pair (or a fixed error) so the handler's handling of the
// schema fields is exercised deterministically.
type fakeVersionSchema struct {
	version int64
	dirty   bool
	err     error
}

func (f *fakeVersionSchema) SchemaVersion(ctx context.Context) (int64, bool, error) {
	return f.version, f.dirty, f.err
}

func adminVersionBody(t *testing.T, h *AdminVersionHandler) adminVersionResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.Version(rec, httptest.NewRequest(http.MethodGet, "/admin/version", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("Version: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body adminVersionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return body
}

// TestAdminVersionReportsLdflagsValues pins criterion 2's "same variables"
// guarantee at the handler boundary: whatever the main package's
// version/commitHash variables hold is reported verbatim, with no second
// formatting constant in between. The full sha must also come back complete
// (criterion 3 — the console needs the copyable full value) with its short
// form alongside.
func TestAdminVersionReportsLdflagsValues(t *testing.T) {
	h := NewAdminVersionHandler("9.9.9-test", "0123456789abcdef0123456789abcdef01234567", nil)
	body := adminVersionBody(t, h)

	if body.Version != "9.9.9-test" {
		t.Errorf("version = %q, want the constructed value verbatim", body.Version)
	}
	if body.CommitHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("commit_hash = %q, want the constructed value verbatim", body.CommitHash)
	}
	if body.CommitHashShort != "0123456" {
		t.Errorf("commit_hash_short = %q, want the 7-character short form", body.CommitHashShort)
	}
	if body.SchemaVersion != nil {
		t.Errorf("schema_version = %v with a nil seam, want omitted", *body.SchemaVersion)
	}
}

// TestAdminVersionUnknownCommitDegradesHonestly pins criterion 4: a binary
// built without -ldflags carries commitHash "unknown" (#0509's deliberate
// loud placeholder), and both the full and short forms must show that value
// plainly — not truncated to seven characters, not blanked, not replaced
// with anything that would read as a real sha.
func TestAdminVersionUnknownCommitDegradesHonestly(t *testing.T) {
	h := NewAdminVersionHandler("0.1.2", "unknown", nil)
	body := adminVersionBody(t, h)

	if body.CommitHash != "unknown" || body.CommitHashShort != "unknown" {
		t.Errorf("commit_hash = %q / short = %q, want both \"unknown\" verbatim",
			body.CommitHash, body.CommitHashShort)
	}
}

// TestAdminVersionIncludesSchemaVersion pins criterion 6's decision to show
// the schema stamp: with a working seam the response carries the version and
// the dirty flag, so the console can show "which schema" next to "which
// code" and surface a mid-migration database rather than hiding it.
func TestAdminVersionIncludesSchemaVersion(t *testing.T) {
	h := NewAdminVersionHandler("0.1.2", "unknown", &fakeVersionSchema{version: 30, dirty: true})
	body := adminVersionBody(t, h)

	if body.SchemaVersion == nil || *body.SchemaVersion != 30 {
		t.Errorf("schema_version = %v, want 30", body.SchemaVersion)
	}
	if !body.SchemaDirty {
		t.Errorf("schema_dirty = false, want true")
	}
}

// TestAdminVersionSchemaErrorIs500: a failing schema read (e.g.
// schema_migrations missing — a database migrated by something else entirely)
// must not fabricate a zero version; it is a 500 like every other read
// failure in this package.
func TestAdminVersionSchemaErrorIs500(t *testing.T) {
	h := NewAdminVersionHandler("0.1.2", "unknown", &fakeVersionSchema{err: errors.New("no such table")})
	rec := httptest.NewRecorder()
	h.Version(rec, httptest.NewRequest(http.MethodGet, "/admin/version", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// TestSchemaReaderReadsSchemaMigrations proves the real seam against a real
// database: the test database is migrated by scripts/testdb.sh before this
// suite runs, so schema_migrations has a row, and SchemaVersion must read
// back exactly the version/dirty pair a direct SELECT sees. Gated on
// TEST_DATABASE_URL like every DB-backed test in this package (main_test.go).
func TestSchemaReaderReadsSchemaMigrations(t *testing.T) {
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}

	var wantVersion int64
	var wantDirty bool
	if err := testDBPool.QueryRow(context.Background(),
		"SELECT version, dirty FROM schema_migrations").Scan(&wantVersion, &wantDirty); err != nil {
		t.Fatalf("reading schema_migrations directly: %v", err)
	}

	gotVersion, gotDirty, err := (&db.SchemaReader{Pool: testDBPool}).SchemaVersion(context.Background())
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if gotVersion != wantVersion || gotDirty != wantDirty {
		t.Errorf("SchemaVersion = (%d, %v), want (%d, %v)",
			gotVersion, gotDirty, wantVersion, wantDirty)
	}
}
