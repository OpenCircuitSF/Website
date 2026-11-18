package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/api/apitest"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// mediaV1Fixture is a MediaV1 over a temp directory, its v1 router with a
// media:write key, and a mux serving the ticket upload route.
type mediaV1Fixture struct {
	m      *MediaV1
	dir    string
	router http.Handler
	upload http.Handler
	key    string
}

func newMediaV1Fixture(t *testing.T, dir string) *mediaV1Fixture {
	t.Helper()
	m := NewMediaV1(func() string { return dir }, "http://example.test", nil)
	res := apitest.NewResolver()
	key, _ := res.Key(t, "media", apitokens.ScopeMediaWrite)
	mux := http.NewServeMux()
	mux.Handle(MediaUploadPattern, m.UploadHandler(nil))
	return &mediaV1Fixture{m: m, dir: dir, router: apitest.Router(t, res, mediaOperations(m)...), upload: mux, key: key}
}

func (f *mediaV1Fixture) mint(t *testing.T) mediaUploadTicketV1 {
	t.Helper()
	rec := apitest.Do(t, f.router, http.MethodPost, "/api/v1/media/uploads", f.key, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("create_media_upload: %d %s", rec.Code, rec.Body)
	}
	return v1Decode[mediaUploadTicketV1](t, rec)
}

// sendUpload posts file to the ticket's upload_url with bearer (if any).
func (f *mediaV1Fixture) sendUpload(t *testing.T, url, bearer string, file []byte, name string) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(file)
	w.Close()
	req := httptest.NewRequest(http.MethodPost, strings.TrimPrefix(url, "http://example.test"), &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	f.upload.ServeHTTP(rec, req)
	return rec
}

func TestV1MediaUpload_StripsAndIsSingleUse(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	if strings.Contains(tk.UploadURL, tk.UploadToken) {
		t.Fatalf("upload_url carries the ticket: %s", tk.UploadURL)
	}
	if tk.MaxBytes != 5<<20 || !strings.Contains(tk.Curl, "Bearer "+tk.UploadToken) {
		t.Fatalf("ticket = %+v", tk)
	}

	input := buildTestJPEG(t, 8, 8, true)
	if !bytes.Contains(input, []byte("GPSLatitude")) {
		t.Fatal("fixture does not carry GPS bytes")
	}
	rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, input, "cover.jpg")
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	var out mediaUploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !strings.HasPrefix(out.Path, "/media/cover-") {
		t.Fatalf("response %s (%v)", rec.Body, err)
	}
	disk, err := os.ReadFile(filepath.Join(f.dir, strings.TrimPrefix(out.Path, "/media/")))
	if err != nil {
		t.Fatal(err)
	}
	if err := media.VerifyStripped(media.FormatJPEG, disk); err != nil || bytes.Contains(disk, []byte("GPSLatitude")) {
		t.Fatalf("stored file not stripped: %v", err)
	}

	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, input, "cover.jpg"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("second use: %d, want 401", rec.Code)
	}
}

func TestV1MediaUpload_NoOrWrongTicket(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	png := buildTestPNG(4, 4, false)
	if rec := f.sendUpload(t, tk.UploadURL, "", png, "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no ticket: %d, want 401", rec.Code)
	}
	if rec := f.sendUpload(t, tk.UploadURL, "oup_wrong", png, "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong ticket: %d, want 401", rec.Code)
	}
	// A ticket presented on another ticket's id is refused too.
	other := f.mint(t)
	if rec := f.sendUpload(t, other.UploadURL, tk.UploadToken, png, "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("mismatched id: %d, want 401", rec.Code)
	}
	// Failed attempts do not burn the real ticket.
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, png, "a.png"); rec.Code != http.StatusOK {
		t.Fatalf("real ticket: %d %s", rec.Code, rec.Body)
	}
}

func TestV1MediaUpload_TicketExpires(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	f.m.tickets.now = func() time.Time { return now }
	tk := f.mint(t)
	now = now.Add(mediaUploadTicketTTL)
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("expired ticket: %d, want 401", rec.Code)
	}
	if len(f.m.tickets.tickets) != 0 {
		t.Fatalf("expired ticket not swept")
	}
}

func TestV1MediaUpload_HEIC(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	heic := make([]byte, 20)
	heic[3] = 20
	copy(heic[4:8], "ftyp")
	copy(heic[8:12], "heic")
	rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, heic, "IMG_0001.HEIC")
	if rec.Code != http.StatusUnsupportedMediaType || !strings.Contains(rec.Body.String(), "HEIC") {
		t.Fatalf("HEIC: %d %s", rec.Code, rec.Body)
	}
	// #0708: a refused upload leaves the ticket usable; success spends it.
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusOK {
		t.Fatalf("after HEIC: %d %s", rec.Code, rec.Body)
	}
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("third use: %d, want 401", rec.Code)
	}
}

func TestV1MediaUpload_CorruptKeepsTicket(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, []byte("not an image"), "a.png"); rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("corrupt: %d %s, want 4xx", rec.Code, rec.Body)
	}
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusOK {
		t.Fatalf("after corrupt: %d %s", rec.Code, rec.Body)
	}
}

func TestV1MediaUpload_TooLarge(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, make([]byte, mediaMaxUploadBytes+1), "big.jpg")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "exceeds") {
		t.Fatalf("too large: %d %s", rec.Code, rec.Body)
	}
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusOK {
		t.Fatalf("after too large: %d %s", rec.Code, rec.Body)
	}
}

func TestUploadTickets_ClaimReleaseConsume(t *testing.T) {
	s := newUploadTickets(time.Now)
	id, secret, _, err := s.mint(audit.APITokenActor{Name: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if a, err := s.claim(id, secret); err != nil || a.Name != "k" {
		t.Fatalf("claim: %+v %v", a, err)
	}
	if _, err := s.claim(id, secret); err != errTicketBusy {
		t.Fatalf("second claim: %v, want busy", err)
	}
	if _, err := s.claim(id, "oup_wrong"); err != errTicketInvalid {
		t.Fatalf("wrong secret: %v, want invalid", err)
	}
	s.release(id)
	if _, err := s.claim(id, secret); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
	s.consume(id)
	if _, err := s.claim(id, secret); err != errTicketInvalid {
		t.Fatalf("claim after consume: %v, want invalid", err)
	}
	s.release(id) // no-op once gone
}

func TestV1MediaUpload_BusyTicketIs409(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	tk := f.mint(t)
	id := strings.TrimPrefix(tk.UploadURL, "http://example.test"+mediaUploadPathPrefix)
	if _, err := f.m.tickets.claim(id, tk.UploadToken); err != nil {
		t.Fatal(err)
	}
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusConflict {
		t.Fatalf("concurrent upload: %d %s, want 409", rec.Code, rec.Body)
	}
}

func TestV1MediaUpload_FailedTicketsAreRateLimited(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	mux := http.NewServeMux()
	mux.Handle(MediaUploadPattern, f.m.UploadHandler(middleware.NewAPIAuthFailureLimiter()))
	f.upload = mux
	tk := f.mint(t)
	for i := 0; i < middleware.APIAuthFailuresPerMinute; i++ {
		if rec := f.sendUpload(t, tk.UploadURL, "oup_wrong", nil, "a.png"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d, want 401", i, rec.Code)
		}
	}
	if rec := f.sendUpload(t, tk.UploadURL, "oup_wrong", nil, "a.png"); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d, want 429", rec.Code)
	}
}

func TestV1Media_NotConfigured(t *testing.T) {
	f := newMediaV1Fixture(t, "")
	rec := apitest.Do(t, f.router, http.MethodPost, "/api/v1/media/uploads", f.key, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("create_media_upload: %d, want 503", rec.Code)
	}
	if rec := apitest.Do(t, f.router, http.MethodGet, "/api/v1/media", f.key, ""); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("list_media: %d, want 503", rec.Code)
	}
}

func TestV1ListMedia_NewestFirstPaged(t *testing.T) {
	f := newMediaV1Fixture(t, t.TempDir())
	base := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i, n := range []string{"a.png", "b.jpg", "c.png"} {
		p := filepath.Join(f.dir, n)
		if err := os.WriteFile(p, []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		os.Chtimes(p, base.Add(time.Duration(i)*time.Hour), base.Add(time.Duration(i)*time.Hour))
	}
	os.Mkdir(filepath.Join(f.dir, "sub"), 0o755)
	os.WriteFile(filepath.Join(f.dir, ".tmp-x"), []byte("x"), 0o644)

	rec := apitest.Do(t, f.router, http.MethodGet, "/api/v1/media?limit=2", f.key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list_media: %d %s", rec.Code, rec.Body)
	}
	page := v1Decode[api.List[mediaFileV1]](t, rec)
	if len(page.Items) != 2 || page.Items[0].Name != "c.png" || page.Items[1].Name != "b.jpg" || page.NextCursor == nil {
		t.Fatalf("page 1 = %+v", page)
	}
	if page.Items[0].Path != "/media/c.png" || page.Items[0].SizeBytes != 3 || page.Items[0].ModifiedAt != "2026-10-31T19:00:00-07:00" {
		t.Fatalf("item = %+v", page.Items[0])
	}
	rec = apitest.Do(t, f.router, http.MethodGet, "/api/v1/media?limit=2&cursor="+*page.NextCursor, f.key, "")
	page = v1Decode[api.List[mediaFileV1]](t, rec)
	if len(page.Items) != 1 || page.Items[0].Name != "a.png" || page.NextCursor != nil {
		t.Fatalf("page 2 = %+v", page)
	}
}

// TestV1MediaUpload_TicketNeverLoggedOrAudited (#0705 review): across a
// mint, a wrong-secret attempt, a HEIC refusal and a successful upload, the
// ticket secret reaches no slog/log line and not the audit row; the audit
// row is media.image_uploaded with a null actor, attributed to the minting
// API key.
func TestV1MediaUpload_TicketNeverLoggedOrAudited(t *testing.T) {
	pool := adminMediaTestPool(t)
	var buf bytes.Buffer
	prevSlog, prevOut := slog.Default(), log.Writer()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	log.SetOutput(&buf)
	t.Cleanup(func() {
		slog.SetDefault(prevSlog)
		log.SetOutput(prevOut)
	})

	f := newMediaV1Fixture(t, t.TempDir())
	f.m.auditor = audit.New(pool)
	tk := f.mint(t)
	if rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken+"x", buildTestPNG(4, 4, false), "a.png"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	heic := make([]byte, 20)
	heic[3] = 20
	copy(heic[4:8], "ftyp")
	copy(heic[8:12], "heic")
	other := f.mint(t)
	if rec := f.sendUpload(t, other.UploadURL, other.UploadToken, heic, "x.heic"); rec.Code != http.StatusUnsupportedMediaType || strings.Contains(rec.Body.String(), other.UploadToken) {
		t.Fatalf("HEIC: %d %s", rec.Code, rec.Body)
	}
	rec := f.sendUpload(t, tk.UploadURL, tk.UploadToken, buildTestPNG(4, 4, true), "a.png")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), tk.UploadToken) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}
	for _, s := range []string{tk.UploadToken, other.UploadToken} {
		if strings.Contains(buf.String(), s) {
			t.Fatalf("ticket secret was logged: %s", buf.String())
		}
	}

	ctx := context.Background()
	var n int
	var actorNull bool
	var meta, tokID string
	if err := pool.QueryRow(ctx, `SELECT count(*) OVER (), actor_id IS NULL, metadata::text, metadata->'api_token'->>'id'
		FROM audit_log WHERE action = $1`, audit.ActionMediaImageUploaded).Scan(&n, &actorNull, &meta, &tokID); err != nil {
		t.Fatalf("audit row: %v", err)
	}
	if n != 1 || !actorNull || tokID != "1" {
		t.Fatalf("audit: count %d, actor null %v, api_token.id %q, meta %s", n, actorNull, tokID, meta)
	}
	if strings.Contains(meta, tk.UploadToken) || strings.Contains(meta, strings.TrimPrefix(tk.UploadToken, "oup_")) {
		t.Fatalf("ticket secret in audit metadata: %s", meta)
	}
}
