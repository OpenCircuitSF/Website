package handlers

import (
	"bytes"
	"encoding/json"
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
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
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
	mux.Handle(MediaUploadPattern, http.HandlerFunc(m.Upload))
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
	if page.Items[0].Path != "/media/c.png" || page.Items[0].SizeBytes != 3 {
		t.Fatalf("item = %+v", page.Items[0])
	}
	rec = apitest.Do(t, f.router, http.MethodGet, "/api/v1/media?limit=2&cursor="+*page.NextCursor, f.key, "")
	page = v1Decode[api.List[mediaFileV1]](t, rec)
	if len(page.Items) != 1 || page.Items[0].Name != "a.png" || page.NextCursor != nil {
		t.Fatalf("page 2 = %+v", page)
	}
}
