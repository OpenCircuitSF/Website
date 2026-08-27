package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
	"github.com/brennanMKE/OpenCircuitSF/internal/testdb"
)

// adminImportsMux wires the real admin import routes guarded by
// RequireSession then RequireAdmin, backed by real stores — mirrors
// adminSubscribersMux (admin_subscribers_test.go).
func adminImportsMux(pool *pgxpool.Pool) http.Handler {
	authStore := auth.NewStore(pool)
	importsStore := subscribers.NewImportStore(pool)
	interestsStore := interests.NewStore(pool)
	h := NewAdminImportsHandler(importsStore, interestsStore, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("POST /admin/subscribers/import/preview", requireAdmin(http.HandlerFunc(h.Preview)))
	mux.Handle("POST /admin/subscribers/import", requireAdmin(http.HandlerFunc(h.Commit)))
	mux.Handle("POST /admin/imports/{id}/revoke", requireAdmin(http.HandlerFunc(h.Revoke)))
	return mux
}

// importFormFields is the set of multipart form values every preview/commit
// request carries alongside the file — collected in one place so a test
// changing one field doesn't have to repeat the rest.
type importFormFields struct {
	source         string
	sourceDetail   string
	consentMode    string
	consentNote    string
	collectedAt    string
	emailColumn    string
	interestColumn string
	checksum       string // commit only; empty for preview
}

func defaultImportFormFields() importFormFields {
	return importFormFields{
		source:       subscribers.ImportSourceManualCSV,
		sourceDetail: "Intro to Soldering sign-in sheet",
		consentMode:  subscribers.ConsentModePriorConsent,
		consentNote:  "collected on a paper sign-in sheet at the event, attested by the organizer",
		collectedAt:  "2026-05-12",
		emailColumn:  "0",
	}
}

// buildImportMultipart encodes csvBody as the "file" part alongside fields,
// returning the request body and the multipart Content-Type header value.
func buildImportMultipart(t *testing.T, csvBody string, fields importFormFields) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)

	part, err := w.CreateFormFile("file", "attendees.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(csvBody)); err != nil {
		t.Fatalf("write file part: %v", err)
	}

	set := func(name, value string) {
		if value == "" {
			return
		}
		if err := w.WriteField(name, value); err != nil {
			t.Fatalf("WriteField %q: %v", name, err)
		}
	}
	set("source", fields.source)
	set("source_detail", fields.sourceDetail)
	set("consent_mode", fields.consentMode)
	set("consent_note", fields.consentNote)
	set("collected_at", fields.collectedAt)
	set("email_column", fields.emailColumn)
	set("interest_column", fields.interestColumn)
	set("checksum", fields.checksum)

	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	return &buf, w.FormDataContentType()
}

// doImportUpload POSTs a multipart import request (preview or commit) with
// an admin session cookie.
func doImportUpload(t *testing.T, client *http.Client, url, token, csvBody string, fields importFormFields) *http.Response {
	t.Helper()
	body, contentType := buildImportMultipart(t, csvBody, fields)
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", contentType)
	req = withCookie(req, token)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

// adminImportsTestPool returns the package's shared pool, truncating the
// auth tables the same way adminSubscribersTestPool does, and cleaning up
// every row this file's scoped "zz-import-http-" prefix could have touched.
func adminImportsTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), handlersDBOpTimeout)
		defer cancel()
		_, _ = testDBPool.Exec(ctx, `DELETE FROM subscribers WHERE email LIKE 'zz-import-http-%'`)
		_, _ = testDBPool.Exec(ctx, `DELETE FROM subscriber_imports WHERE source_detail LIKE 'zz-import-http-%'`)
	})
	return testDBPool
}

func importHTTPEmail(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("zz-import-http-%d@example.com", testdb.Unique())
}

// TestAdminImports_NonAdminForbidden proves RequireAdmin guards all three
// routes at this handler-local mux — the authoritative real-mux proof lives
// in cmd/opencircuit/admin_wiring_test.go, matching every other admin
// handler suite's convention.
func TestAdminImports_NonAdminForbidden(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()

	user := seedUser(t, pool, "regular-imports@example.com")
	seedSession(t, pool, user, "user-token-imports")
	client := srv.Client()

	fields := defaultImportFormFields()
	resp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "user-token-imports", "email\n"+importHTTPEmail(t)+"\n", fields)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("preview with non-admin session: status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp2 := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "user-token-imports", "email\n"+importHTTPEmail(t)+"\n", fields)
	if resp2.StatusCode != http.StatusForbidden {
		t.Errorf("commit with non-admin session: status = %d, want 403", resp2.StatusCode)
	}
	resp2.Body.Close()

	resp3 := doJSON(t, client, "POST", srv.URL+"/admin/imports/1/revoke", "user-token-imports", `{"reason":"x"}`)
	if resp3.StatusCode != http.StatusForbidden {
		t.Errorf("revoke with non-admin session: status = %d, want 403", resp3.StatusCode)
	}
	resp3.Body.Close()
}

// TestAdminImports_Preview_ClassifiesAndReportsMalformed proves the
// handler-layer email-syntax classification (validEmailSyntax) and that
// preview writes nothing.
func TestAdminImports_Preview_ClassifiesAndReportsMalformed(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-preview@example.com")
	seedSession(t, pool, admin, "admin-token-preview")
	client := srv.Client()

	fresh := importHTTPEmail(t)
	csvBody := "email,notes\n" + fresh + ",hello\nnot-an-email,oops\n"
	resp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "admin-token-preview", csvBody, defaultImportFormFields())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body := readBody(t, resp)
		t.Fatalf("preview status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var got previewResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode preview response: %v", err)
	}
	if got.NewCount != 1 {
		t.Errorf("NewCount = %d, want 1", got.NewCount)
	}
	if got.MalformedCount != 1 {
		t.Errorf("MalformedCount = %d, want 1", got.MalformedCount)
	}
	if got.Checksum == "" {
		t.Error("Checksum is empty, want a non-empty hex digest")
	}

	// Preview must write nothing.
	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM subscribers WHERE email = $1`, fresh).Scan(&count); err != nil {
		t.Fatalf("counting subscribers: %v", err)
	}
	if count != 0 {
		t.Errorf("subscribers rows for %q after preview = %d, want 0", fresh, count)
	}
}

// TestAdminImports_Commit_ChecksumMismatchRejected proves commit refuses a
// checksum that does not match the resubmitted file.
func TestAdminImports_Commit_ChecksumMismatchRejected(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-checksum@example.com")
	seedSession(t, pool, admin, "admin-token-checksum")
	client := srv.Client()

	email := importHTTPEmail(t)
	fields := defaultImportFormFields()
	fields.checksum = "0000000000000000000000000000000000000000000000000000000000000000"
	resp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "admin-token-checksum", "email\n"+email+"\n", fields)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		body := readBody(t, resp)
		t.Fatalf("commit with wrong checksum status = %d, want 409; body: %s", resp.StatusCode, body)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM subscribers WHERE email = $1`, email).Scan(&count); err != nil {
		t.Fatalf("counting subscribers: %v", err)
	}
	if count != 0 {
		t.Errorf("subscribers rows for %q after rejected commit = %d, want 0", email, count)
	}
}

// TestAdminImports_Commit_SuccessInsertsAndAudits is the end-to-end
// happy path: preview to get the checksum, commit with it, and verify the
// subscriber landed active with provenance, the outbound queue is empty,
// and one audit_log row was written.
func TestAdminImports_Commit_SuccessInsertsAndAudits(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-commit@example.com")
	seedSession(t, pool, admin, "admin-token-commit")
	client := srv.Client()

	email := importHTTPEmail(t)
	csvBody := "email\n" + email + "\n"
	fields := defaultImportFormFields()

	previewResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "admin-token-commit", csvBody, fields)
	var preview previewResponse
	if err := json.NewDecoder(previewResp.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	previewResp.Body.Close()

	fields.checksum = preview.Checksum
	commitResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "admin-token-commit", csvBody, fields)
	defer commitResp.Body.Close()
	if commitResp.StatusCode != http.StatusOK {
		body := readBody(t, commitResp)
		t.Fatalf("commit status = %d, want 200; body: %s", commitResp.StatusCode, body)
	}
	var got commitResponse
	if err := json.NewDecoder(commitResp.Body).Decode(&got); err != nil {
		t.Fatalf("decode commit response: %v", err)
	}
	if got.Import.InsertedCount != 1 {
		t.Errorf("InsertedCount = %d, want 1", got.Import.InsertedCount)
	}
	if got.Import.Status != subscribers.ImportStatusCommitted {
		t.Errorf("Import.Status = %q, want %q", got.Import.Status, subscribers.ImportStatusCommitted)
	}

	var status, source string
	var consentBasis *string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, source, consent_basis FROM subscribers WHERE email = $1`, email,
	).Scan(&status, &source, &consentBasis); err != nil {
		t.Fatalf("reading committed subscriber: %v", err)
	}
	if status != subscribers.StatusActive {
		t.Errorf("status = %q, want %q", status, subscribers.StatusActive)
	}
	if source != subscribers.SubscriberSourceImport {
		t.Errorf("source = %q, want %q", source, subscribers.SubscriberSourceImport)
	}
	if consentBasis == nil || *consentBasis != subscribers.ConsentBasisImportedPriorConsent {
		t.Errorf("consent_basis = %v, want %q", consentBasis, subscribers.ConsentBasisImportedPriorConsent)
	}

	var queued int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM outbound_queue WHERE recipient = $1`, email).Scan(&queued); err != nil {
		t.Fatalf("counting outbound_queue: %v", err)
	}
	if queued != 0 {
		t.Errorf("outbound_queue rows for %q = %d, want 0 (prior_consent sends nothing)", email, queued)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND target_type = $3`,
		audit.ActionSubscriberImportCommitted, got.Import.ID, audit.TargetSubscriberImport,
	).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit_log rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit_log rows for the commit = %d, want 1", auditCount)
	}
}

// TestAdminImports_Commit_RequiresConsentNote proves the handler surfaces
// the store's mandatory consent_note validation as a 400.
func TestAdminImports_Commit_RequiresConsentNote(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-note@example.com")
	seedSession(t, pool, admin, "admin-token-note")
	client := srv.Client()

	email := importHTTPEmail(t)
	csvBody := "email\n" + email + "\n"
	fields := defaultImportFormFields()
	fields.consentNote = ""

	previewResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "admin-token-note", csvBody, fields)
	var preview previewResponse
	_ = json.NewDecoder(previewResp.Body).Decode(&preview)
	previewResp.Body.Close()

	fields.checksum = preview.Checksum
	resp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "admin-token-note", csvBody, fields)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("commit with no consent_note status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}

// TestAdminImports_Commit_RequiresSourceDetail is #0291's handler-level
// proof, mirroring TestAdminImports_Commit_RequiresConsentNote above: a
// blank source_detail must 400 rather than commit, since PRD §6.10 names it
// as one of the four mandatory provenance fields.
func TestAdminImports_Commit_RequiresSourceDetail(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-detail@example.com")
	seedSession(t, pool, admin, "admin-token-detail")
	client := srv.Client()

	email := importHTTPEmail(t)
	csvBody := "email\n" + email + "\n"
	fields := defaultImportFormFields()
	fields.sourceDetail = ""

	previewResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "admin-token-detail", csvBody, fields)
	var preview previewResponse
	_ = json.NewDecoder(previewResp.Body).Decode(&preview)
	previewResp.Body.Close()

	fields.checksum = preview.Checksum
	resp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "admin-token-detail", csvBody, fields)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("commit with no source_detail status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}

// TestAdminImports_Revoke_UnsubscribesAndAudits commits a batch, revokes
// it, and verifies the subscriber moved to unsubscribed and one audit_log
// row was written.
func TestAdminImports_Revoke_UnsubscribesAndAudits(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-revoke@example.com")
	seedSession(t, pool, admin, "admin-token-revoke")
	client := srv.Client()

	email := importHTTPEmail(t)
	csvBody := "email\n" + email + "\n"
	fields := defaultImportFormFields()

	previewResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import/preview", "admin-token-revoke", csvBody, fields)
	var preview previewResponse
	if err := json.NewDecoder(previewResp.Body).Decode(&preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	previewResp.Body.Close()
	fields.checksum = preview.Checksum

	commitResp := doImportUpload(t, client, srv.URL+"/admin/subscribers/import", "admin-token-revoke", csvBody, fields)
	var committed commitResponse
	if err := json.NewDecoder(commitResp.Body).Decode(&committed); err != nil {
		t.Fatalf("decode commit: %v", err)
	}
	commitResp.Body.Close()

	revokeResp := doJSON(t, client, "POST", fmt.Sprintf("%s/admin/imports/%d/revoke", srv.URL, committed.Import.ID), "admin-token-revoke", `{"reason":"consent was not actually obtained"}`)
	defer revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		body := readBody(t, revokeResp)
		t.Fatalf("revoke status = %d, want 200; body: %s", revokeResp.StatusCode, body)
	}
	var revoked revokeResponse
	if err := json.NewDecoder(revokeResp.Body).Decode(&revoked); err != nil {
		t.Fatalf("decode revoke response: %v", err)
	}
	if revoked.RevokedCount != 1 {
		t.Errorf("RevokedCount = %d, want 1", revoked.RevokedCount)
	}
	if revoked.NoOp {
		t.Error("NoOp = true on a fresh revoke, want false")
	}

	var status, unsubSource string
	if err := pool.QueryRow(context.Background(),
		`SELECT status, unsubscribe_source FROM subscribers WHERE email = $1`, email,
	).Scan(&status, &unsubSource); err != nil {
		t.Fatalf("reading revoked subscriber: %v", err)
	}
	if status != subscribers.StatusUnsubscribed {
		t.Errorf("status = %q, want %q", status, subscribers.StatusUnsubscribed)
	}
	if unsubSource != subscribers.SourceAdmin {
		t.Errorf("unsubscribe_source = %q, want %q", unsubSource, subscribers.SourceAdmin)
	}

	var auditCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM audit_log WHERE action = $1 AND target_id = $2 AND target_type = $3`,
		audit.ActionSubscriberImportRevoked, committed.Import.ID, audit.TargetSubscriberImport,
	).Scan(&auditCount); err != nil {
		t.Fatalf("counting audit_log rows: %v", err)
	}
	if auditCount != 1 {
		t.Errorf("audit_log rows for the revoke = %d, want 1", auditCount)
	}
}

// TestAdminImports_Revoke_RequiresReason proves the JSON body's reason is
// mandatory.
func TestAdminImports_Revoke_RequiresReason(t *testing.T) {
	pool := adminImportsTestPool(t)
	srv := httptest.NewServer(adminImportsMux(pool))
	defer srv.Close()
	admin := seedAdminUser(t, pool, "admin-imports-revoke-reason@example.com")
	seedSession(t, pool, admin, "admin-token-revoke-reason")
	client := srv.Client()

	resp := doJSON(t, client, "POST", srv.URL+"/admin/imports/1/revoke", "admin-token-revoke-reason", `{"reason":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("revoke with empty reason status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}
