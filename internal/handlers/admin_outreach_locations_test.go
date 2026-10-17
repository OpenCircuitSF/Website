package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/outreach"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

type testLocation struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Code       string  `json:"code"`
	Address    string  `json:"address"`
	Notes      string  `json:"notes"`
	Active     bool    `json:"active"`
	CodeLocked bool    `json:"code_locked"`
	UpdatedAt  *string `json:"updated_at"`
}

func locTestCode() string { return "z" + strconv.FormatInt(testdb.Unique(), 36) }

func (e *outreachEnv) createLocation(t *testing.T, body string) testLocation {
	t.Helper()
	code, b := e.do(t, http.MethodPost, "/admin/outreach-locations", body)
	if code != http.StatusCreated {
		t.Fatalf("create location status = %d (%s)", code, b)
	}
	l := decodeOutreach[testLocation](t, b)
	t.Cleanup(func() {
		_, _ = e.pool.Exec(context.Background(), `DELETE FROM outreach_locations WHERE id = $1`, l.ID)
	})
	return l
}

func locAuditCount(t *testing.T, pool *pgxpool.Pool, action string, id int64) int {
	t.Helper()
	return outreachAuditCount(t, pool, action, id)
}

func TestAdminOutreachLocations_AuthGuards(t *testing.T) {
	e := newOutreachEnv(t, true)
	paths := []struct{ method, path string }{
		{http.MethodGet, "/admin/outreach-locations"},
		{http.MethodPost, "/admin/outreach-locations"},
		{http.MethodPatch, "/admin/outreach-locations/1"},
		{http.MethodDelete, "/admin/outreach-locations/1"},
	}
	for _, p := range paths {
		for token, want := range map[string]int{"": http.StatusUnauthorized, "outreach-user-token": http.StatusForbidden} {
			resp := doJSON(t, e.srv.Client(), p.method, e.srv.URL+p.path, token, "")
			resp.Body.Close()
			if resp.StatusCode != want {
				t.Errorf("%s %s token=%q status = %d, want %d", p.method, p.path, token, resp.StatusCode, want)
			}
		}
	}
}

func TestAdminOutreachLocations_CreateListAudit(t *testing.T) {
	e := newOutreachEnv(t, true)
	code := locTestCode()
	l := e.createLocation(t, fmt.Sprintf(`{"name":"  Mission Library ","code":%q,"address":"300 Main","notes":"north board"}`, code))
	if l.Name != "Mission Library" || !l.Active || l.CodeLocked || l.Code != code {
		t.Errorf("view = %+v", l)
	}
	if n := locAuditCount(t, e.pool, "outreach_location.created", l.ID); n != 1 {
		t.Errorf("created audit rows = %d, want 1", n)
	}
	var meta string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT metadata::text FROM audit_log WHERE action = 'outreach_location.created' AND target_id = $1`, l.ID).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"code"`, `"name"`, `"active"`} {
		if !strings.Contains(meta, want) {
			t.Errorf("metadata %s missing %s", meta, want)
		}
	}
	if strings.Contains(meta, "300 Main") || strings.Contains(meta, "north board") {
		t.Errorf("metadata leaks address or notes: %s", meta)
	}

	status, b := e.do(t, http.MethodGet, "/admin/outreach-locations", "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	list := decodeOutreach[struct {
		Locations []testLocation `json:"locations"`
	}](t, b)
	found := false
	for _, x := range list.Locations {
		found = found || x.ID == l.ID
	}
	if !found {
		t.Error("created location not in list")
	}

	// Inactive on create is honoured.
	off := e.createLocation(t, fmt.Sprintf(`{"name":"Off","code":%q,"active":false}`, locTestCode()))
	if off.Active {
		t.Error("active:false ignored on create")
	}
}

func TestAdminOutreachLocations_CreateValidation(t *testing.T) {
	e := newOutreachEnv(t, true)
	good := locTestCode()
	cases := map[string]string{
		"uppercase code (server does not normalise)": `{"name":"X","code":"Ab"}`,
		"short code":    `{"name":"X","code":"a"}`,
		"long code":     `{"name":"X","code":"abcdefghijklmnopq"}`,
		"trailing dash": `{"name":"X","code":"ab-"}`,
		"missing code":  `{"name":"X"}`,
		"missing name":  fmt.Sprintf(`{"code":%q}`, good),
		"blank name":    fmt.Sprintf(`{"name":"   ","code":%q}`, good),
		"long name":     fmt.Sprintf(`{"name":%q,"code":%q}`, strings.Repeat("n", 101), good),
		"long address":  fmt.Sprintf(`{"name":"X","code":%q,"address":%q}`, good, strings.Repeat("a", 301)),
		"long notes":    fmt.Sprintf(`{"name":"X","code":%q,"notes":%q}`, good, strings.Repeat("a", 2001)),
		"unknown field": fmt.Sprintf(`{"name":"X","code":%q,"bogus":1}`, good),
		"not json":      `nope`,
	}
	for name, body := range cases {
		if code, b := e.do(t, http.MethodPost, "/admin/outreach-locations", body); code != http.StatusBadRequest {
			t.Errorf("%s: status = %d (%s), want 400", name, code, b)
		}
	}
}

func TestAdminOutreachLocations_DuplicateCode(t *testing.T) {
	e := newOutreachEnv(t, true)
	code := locTestCode()
	e.createLocation(t, fmt.Sprintf(`{"name":"A","code":%q}`, code))
	if status, b := e.do(t, http.MethodPost, "/admin/outreach-locations", fmt.Sprintf(`{"name":"B","code":%q}`, code)); status != http.StatusConflict || !strings.Contains(string(b), "code already in use") {
		t.Errorf("duplicate: %d %s", status, b)
	}
}

func TestAdminOutreachLocations_Patch(t *testing.T) {
	e := newOutreachEnv(t, true)
	l := e.createLocation(t, fmt.Sprintf(`{"name":"A","code":%q}`, locTestCode()))
	path := fmt.Sprintf("/admin/outreach-locations/%d", l.ID)

	// A no-op patch is 200 with no audit row.
	if status, b := e.do(t, http.MethodPatch, path, `{"name":"A"}`); status != http.StatusOK {
		t.Fatalf("no-op: %d %s", status, b)
	}
	if n := locAuditCount(t, e.pool, "outreach_location.updated", l.ID); n != 0 {
		t.Errorf("no-op wrote %d audit rows", n)
	}

	newCode := locTestCode()
	body := fmt.Sprintf(`{"name":"B","code":%q,"notes":"n","active":false}`, newCode)
	if status, b := e.do(t, http.MethodPatch, path, body); status != http.StatusOK {
		t.Fatalf("patch: %d %s", status, b)
	}
	var changed, prev, code string
	if err := e.pool.QueryRow(context.Background(),
		`SELECT metadata->'changed'::text, metadata->>'previous_code', metadata->>'code'
		   FROM audit_log WHERE action = 'outreach_location.updated' AND target_id = $1`, l.ID).Scan(&changed, &prev, &code); err != nil {
		t.Fatal(err)
	}
	if changed != `["active", "code", "name", "notes"]` || prev != l.Code || code != newCode {
		t.Errorf("audit changed=%s prev=%s code=%s", changed, prev, code)
	}

	for name, b := range map[string]string{"empty body": `{}`, "bad code": `{"code":"BAD"}`, "blank name": `{"name":" "}`} {
		if status, _ := e.do(t, http.MethodPatch, path, b); status != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, status)
		}
	}
	if status, _ := e.do(t, http.MethodPatch, "/admin/outreach-locations/999999999", `{"name":"x"}`); status != http.StatusNotFound {
		t.Errorf("unknown id: %d", status)
	}
	if status, _ := e.do(t, http.MethodPatch, "/admin/outreach-locations/abc", `{"name":"x"}`); status != http.StatusNotFound {
		t.Errorf("bad id: %d", status)
	}

	other := e.createLocation(t, fmt.Sprintf(`{"name":"O","code":%q}`, locTestCode()))
	if status, _ := e.do(t, http.MethodPatch, path, fmt.Sprintf(`{"code":%q}`, other.Code)); status != http.StatusConflict {
		t.Errorf("code collision: %d", status)
	}
}

func TestAdminOutreachLocations_LockedCodeAndDelete(t *testing.T) {
	e := newOutreachEnv(t, true)
	l := e.createLocation(t, fmt.Sprintf(`{"name":"A","code":%q}`, locTestCode()))
	if err := outreach.NewStore(e.pool).MarkLocationUsed(context.Background(), e.pool, l.ID); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/admin/outreach-locations/%d", l.ID)

	if status, b := e.do(t, http.MethodPatch, path, fmt.Sprintf(`{"code":%q}`, locTestCode())); status != http.StatusConflict || !strings.Contains(string(b), "code is locked") {
		t.Errorf("locked code change: %d %s", status, b)
	}
	var stored string
	_ = e.pool.QueryRow(context.Background(), `SELECT code FROM outreach_locations WHERE id = $1`, l.ID).Scan(&stored)
	if stored != l.Code {
		t.Errorf("code changed despite 409: %s", stored)
	}
	// Resubmitting the same code with other changes succeeds.
	if status, b := e.do(t, http.MethodPatch, path, fmt.Sprintf(`{"code":%q,"active":false}`, l.Code)); status != http.StatusOK {
		t.Errorf("same-code patch: %d %s", status, b)
	}

	if status, b := e.do(t, http.MethodDelete, path, ""); status != http.StatusConflict || !strings.Contains(string(b), "deactivate it instead") {
		t.Errorf("delete locked: %d %s", status, b)
	}
	if n := locAuditCount(t, e.pool, "outreach_location.deleted", l.ID); n != 0 {
		t.Errorf("refused delete audited %d rows", n)
	}

	free := e.createLocation(t, fmt.Sprintf(`{"name":"F","code":%q}`, locTestCode()))
	fpath := fmt.Sprintf("/admin/outreach-locations/%d", free.ID)
	if status, b := e.do(t, http.MethodDelete, fpath, ""); status != http.StatusNoContent {
		t.Fatalf("delete unlocked: %d %s", status, b)
	}
	if n := locAuditCount(t, e.pool, "outreach_location.deleted", free.ID); n != 1 {
		t.Errorf("delete audit rows = %d", n)
	}
	if status, _ := e.do(t, http.MethodDelete, fpath, ""); status != http.StatusNotFound {
		t.Errorf("delete again: %d", status)
	}
}
