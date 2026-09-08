package handlers

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// --- fixture builders -------------------------------------------------
//
// These are deliberately self-contained here rather than importing
// internal/media's unexported segment walkers (internal/media has its own,
// more thorough positive-control fixtures in its own _test.go files — see
// internal/media/jpeg_fixture_test.go). What THIS file needs is only enough
// of a real JPEG/PNG to prove the HANDLER's own plumbing: magic-byte
// sniffing, image.DecodeConfig succeeding, the strip actually running, and
// the stripped result being what lands on disk and in the audit row.

func jpegSeg(marker byte, payload []byte) []byte {
	l := len(payload) + 2
	out := []byte{0xFF, marker, byte(l >> 8), byte(l)}
	return append(out, payload...)
}

// buildTestJPEG returns a minimal, structurally valid JPEG (image.DecodeConfig
// succeeds against it) at width x height. When withMetadata is true, it
// carries a fake APP1 (Exif-shaped, naming "Apple"/"iPhone 16"/"GPS") and a
// fake APP13 (IPTC-shaped) segment that a correct strip must remove.
func buildTestJPEG(t *testing.T, width, height int, withMetadata bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xD8})
	buf.Write(jpegSeg(0xE0, []byte{'J', 'F', 'I', 'F', 0, 1, 2, 0, 0, 1, 0, 1, 0, 0}))
	if withMetadata {
		buf.Write(jpegSeg(0xE1, []byte("Exif\x00\x00FAKE-TIFF-Make:Apple-Model:iPhone 16-GPSLatitude:37.7749-GPSLongitude:-122.4194")))
		buf.Write(jpegSeg(0xED, []byte("Photoshop 3.0\x00fake IPTC block")))
	}
	dqt := make([]byte, 65)
	for i := 1; i < len(dqt); i++ {
		dqt[i] = 16
	}
	buf.Write(jpegSeg(0xDB, dqt))
	sof := []byte{
		8,
		byte(height >> 8), byte(height),
		byte(width >> 8), byte(width),
		1, 1, 0x11, 0,
	}
	buf.Write(jpegSeg(0xC0, sof))
	buf.Write(jpegSeg(0xDA, []byte{1, 1, 0x00, 0, 63, 0}))
	buf.Write([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07})
	buf.Write([]byte{0xFF, 0xD9})
	return buf.Bytes()
}

func pngChunk(typ string, data []byte) []byte {
	var buf bytes.Buffer
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(data)))
	buf.Write(l[:])
	buf.WriteString(typ)
	buf.Write(data)
	sum := crc32.ChecksumIEEE(append([]byte(typ), data...))
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], sum)
	buf.Write(c[:])
	return buf.Bytes()
}

// buildTestPNG returns a minimal, structurally valid PNG at width x height.
// When withMetadata is true it carries a fake eXIf chunk naming the same
// device/GPS strings as buildTestJPEG's APP1, plus a tEXt chunk.
func buildTestPNG(width, height int, withMetadata bool) []byte {
	sig := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}
	var ihdr bytes.Buffer
	var wb, hb [4]byte
	binary.BigEndian.PutUint32(wb[:], uint32(width))
	binary.BigEndian.PutUint32(hb[:], uint32(height))
	ihdr.Write(wb[:])
	ihdr.Write(hb[:])
	ihdr.Write([]byte{8, 2, 0, 0, 0})

	var buf bytes.Buffer
	buf.Write(sig)
	buf.Write(pngChunk("IHDR", ihdr.Bytes()))
	if withMetadata {
		buf.Write(pngChunk("eXIf", []byte("FAKE-TIFF-Make:Apple-Model:iPhone 16-GPS")))
		buf.Write(pngChunk("tEXt", []byte("Author\x00Taken on an iPhone 16")))
	}
	buf.Write(pngChunk("IDAT", []byte{0x78, 0x9c, 0x01, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0x00, 0x01}))
	buf.Write(pngChunk("IEND", nil))
	return buf.Bytes()
}

// --- mux + request helpers ---------------------------------------------

// adminMediaMux wires the real admin media upload route guarded by
// RequireSession then RequireAdmin, backed by a real audit.Logger and the
// given mediaDir — mirrors adminImportsMux (admin_subscribers_import_test.go).
func adminMediaMux(pool *pgxpool.Pool, mediaDir string) http.Handler {
	authStore := auth.NewStore(pool)
	h := NewAdminMediaHandler(mediaDir, audit.New(pool))
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("POST /admin/media/upload", requireAdmin(http.HandlerFunc(h.Upload)))
	return mux
}

func adminMediaTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateCredsTables(t, testDBPool)
	return testDBPool
}

// doMediaUpload POSTs a multipart upload with the given file bytes/name
// under the "file" field, with an optional session cookie ("" means none).
func doMediaUpload(t *testing.T, client *http.Client, url, token string, fileBytes []byte, filename string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req = withCookie(req, token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

type mediaUploadOK struct {
	Path string `json:"path"`
}

// --- tests ---------------------------------------------------------------

func TestAdminMedia_Unauthenticated(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "", buildTestJPEG(t, 8, 8, false), "photo.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAdminMedia_NonAdminForbidden(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	user := seedUser(t, pool, "regular-media@example.com")
	seedSession(t, pool, user, "user-token-media")

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "user-token-media", buildTestJPEG(t, 8, 8, false), "photo.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status = %d, want 403", resp.StatusCode)
	}
}

func TestAdminMedia_NotConfigured(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, "")) // mediaDir unset
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-unconfigured@example.com")
	seedSession(t, pool, admin, "admin-token-unconfigured")

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-unconfigured", buildTestJPEG(t, 8, 8, false), "photo.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 503; body: %s", resp.StatusCode, body)
	}
	body := readBody(t, resp)
	if !strings.Contains(string(body), "MEDIA_DIR") {
		t.Errorf("503 body = %q, want it to name MEDIA_DIR", body)
	}
}

// TestAdminMedia_HappyPath_JPEG proves the whole plumbing for a clean
// (no-metadata) JPEG: 200, a same-site path in the response satisfying
// isSafeCoverImage, the file present on disk mode 0644, and an audit row.
func TestAdminMedia_HappyPath_JPEG(t *testing.T) {
	pool := adminMediaTestPool(t)
	dir := t.TempDir()
	srv := httptest.NewServer(adminMediaMux(pool, dir))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-happy@example.com")
	seedSession(t, pool, admin, "admin-token-happy")

	input := buildTestJPEG(t, 8, 8, false)
	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-happy", input, "soldering-101.jpg")
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}

	var out mediaUploadOK
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, body)
	}
	if !strings.HasPrefix(out.Path, "/media/") || !strings.HasSuffix(out.Path, ".jpg") {
		t.Errorf("Path = %q, want a /media/*.jpg path", out.Path)
	}
	if !isSafeCoverImage(out.Path) {
		t.Errorf("Path %q fails isSafeCoverImage", out.Path)
	}

	diskPath := filepath.Join(dir, strings.TrimPrefix(out.Path, "/media/"))
	info, err := os.Stat(diskPath)
	if err != nil {
		t.Fatalf("stat %s: %v", diskPath, err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Errorf("file mode = %o, want 0644", got)
	}

	diskBytes, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("reading %s: %v", diskPath, err)
	}
	if err := media.VerifyStripped(media.FormatJPEG, diskBytes); err != nil {
		t.Errorf("stored file fails VerifyStripped: %v", err)
	}

	// Audit row.
	var action string
	var actorID int64
	err = pool.QueryRow(context.Background(),
		`SELECT action, actor_id FROM audit_log WHERE action = $1 ORDER BY id DESC LIMIT 1`,
		audit.ActionMediaImageUploaded,
	).Scan(&action, &actorID)
	if err != nil {
		t.Fatalf("querying audit_log: %v", err)
	}
	if action != audit.ActionMediaImageUploaded {
		t.Errorf("audit action = %q, want %q", action, audit.ActionMediaImageUploaded)
	}
	if actorID != admin {
		t.Errorf("audit actor_id = %d, want %d", actorID, admin)
	}
}

// TestAdminMedia_StripsDeviceAndGPSMetadata is #0433's core acceptance
// criterion proven at the HTTP boundary: upload a file carrying device/GPS
// tags, then assert the bytes actually written to disk and returned to the
// caller do not carry an APP1/APP13 segment at all — proven structurally via
// media.VerifyStripped, never a text search of the stored bytes.
func TestAdminMedia_StripsDeviceAndGPSMetadata(t *testing.T) {
	pool := adminMediaTestPool(t)
	dir := t.TempDir()
	srv := httptest.NewServer(adminMediaMux(pool, dir))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-strip@example.com")
	seedSession(t, pool, admin, "admin-token-strip")

	input := buildTestJPEG(t, 8, 8, true)
	// Positive control: the fixture actually carries the bytes we're about
	// to prove are gone.
	if !bytes.Contains(input, []byte("Apple")) || !bytes.Contains(input, []byte("iPhone 16")) || !bytes.Contains(input, []byte("GPSLatitude")) {
		t.Fatal("fixture does not carry the expected device/GPS bytes -- fixture is broken")
	}

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-strip", input, "iphone-photo.jpg")
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var out mediaUploadOK
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, body)
	}

	diskPath := filepath.Join(dir, strings.TrimPrefix(out.Path, "/media/"))
	diskBytes, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("reading %s: %v", diskPath, err)
	}

	// The structural proof: no APP1/APP13/COM/non-ICC-APP2 segment survives.
	if err := media.VerifyStripped(media.FormatJPEG, diskBytes); err != nil {
		t.Errorf("stored file fails VerifyStripped: %v", err)
	}
	// Belt-and-braces text check on top of the structural proof above (not
	// a substitute for it): the device/GPS strings themselves are gone too.
	if bytes.Contains(diskBytes, []byte("Apple")) || bytes.Contains(diskBytes, []byte("iPhone 16")) || bytes.Contains(diskBytes, []byte("GPSLatitude")) {
		t.Error("stored file still contains device/GPS bytes")
	}
}

func TestAdminMedia_StripsDeviceAndGPSMetadata_PNG(t *testing.T) {
	pool := adminMediaTestPool(t)
	dir := t.TempDir()
	srv := httptest.NewServer(adminMediaMux(pool, dir))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-strip-png@example.com")
	seedSession(t, pool, admin, "admin-token-strip-png")

	input := buildTestPNG(8, 8, true)
	if !bytes.Contains(input, []byte("Apple")) || !bytes.Contains(input, []byte("iPhone 16")) {
		t.Fatal("fixture does not carry the expected device bytes -- fixture is broken")
	}

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-strip-png", input, "photo.png")
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, body)
	}
	var out mediaUploadOK
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, body)
	}
	if !strings.HasSuffix(out.Path, ".png") {
		t.Errorf("Path = %q, want a .png path", out.Path)
	}

	diskPath := filepath.Join(dir, strings.TrimPrefix(out.Path, "/media/"))
	diskBytes, err := os.ReadFile(diskPath)
	if err != nil {
		t.Fatalf("reading %s: %v", diskPath, err)
	}
	if err := media.VerifyStripped(media.FormatPNG, diskBytes); err != nil {
		t.Errorf("stored file fails VerifyStripped: %v", err)
	}
	if bytes.Contains(diskBytes, []byte("Apple")) || bytes.Contains(diskBytes, []byte("iPhone 16")) {
		t.Error("stored PNG still contains device bytes")
	}
}

// TestAdminMedia_HEICGetsSpecificMessage proves #0433's single most
// important error message: an iPhone shooting HEIC by default must not
// look like a generic failure.
func TestAdminMedia_HEICGetsSpecificMessage(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-heic@example.com")
	seedSession(t, pool, admin, "admin-token-heic")

	heic := make([]byte, 20)
	heic[3] = 20
	copy(heic[4:8], "ftyp")
	copy(heic[8:12], "heic")

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-heic", heic, "IMG_0001.HEIC")
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415; body: %s", resp.StatusCode, body)
	}
	if !strings.Contains(strings.ToUpper(string(body)), "HEIC") {
		t.Errorf("415 body = %q, want it to name HEIC specifically", body)
	}
}

func TestAdminMedia_UnsupportedFormatGIF(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-gif@example.com")
	seedSession(t, pool, admin, "admin-token-gif")

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-gif", []byte("GIF89a"+strings.Repeat("x", 20)), "animated.gif")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 415; body: %s", resp.StatusCode, body)
	}
}

func TestAdminMedia_UnrecognizedFormat(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-garbage@example.com")
	seedSession(t, pool, admin, "admin-token-garbage")

	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-garbage", []byte("this is not an image"), "notes.txt")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 415; body: %s", resp.StatusCode, body)
	}
}

// TestAdminMedia_ClientContentTypeIgnored proves format is sniffed from
// magic bytes only: a GIF's real bytes with a claimed image/jpeg
// Content-Type must still be refused as unsupported, not accepted as JPEG.
func TestAdminMedia_ClientContentTypeIgnored(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-lying-content-type@example.com")
	seedSession(t, pool, admin, "admin-token-lying")

	// A GIF signature named "photo.jpg" -- if the handler trusted the
	// filename extension it would try to treat this as a JPEG.
	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-lying", []byte("GIF89a"+strings.Repeat("x", 20)), "photo.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 415 (format must be sniffed, not trusted from the filename); body: %s", resp.StatusCode, body)
	}
}

func TestAdminMedia_AbsurdDimensionsRejected(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-huge@example.com")
	seedSession(t, pool, admin, "admin-token-huge")

	// The plan's own example of what must be rejected.
	huge := buildTestJPEG(t, 8000, 6000, false)
	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-huge", huge, "huge.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 415; body: %s", resp.StatusCode, body)
	}
}

func TestAdminMedia_OversizeRejected(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-oversize@example.com")
	seedSession(t, pool, admin, "admin-token-oversize")

	oversize := bytes.Repeat([]byte{0x00}, mediaMaxUploadBytes+1)
	resp := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-oversize", oversize, "huge.jpg")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}

// TestAdminMedia_DuplicateUploadsConverge proves re-uploading the exact
// same file (same original filename, same bytes) overwrites the same
// stored path rather than accumulating a second copy — media.Filename's
// "<sanitised-stem>-<hash>" scheme means the stem AND the hash must both
// match for two uploads to land on the identical name; this is that case.
// See TestAdminMedia_IdenticalBytesDifferentNamesConverge_OnHashOnly below
// for the narrower claim when the original filenames differ.
func TestAdminMedia_DuplicateUploadsConverge(t *testing.T) {
	pool := adminMediaTestPool(t)
	dir := t.TempDir()
	srv := httptest.NewServer(adminMediaMux(pool, dir))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-dup@example.com")
	seedSession(t, pool, admin, "admin-token-dup")

	input := buildTestJPEG(t, 8, 8, false)

	first := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-dup", input, "soldering.jpg")
	var firstOut mediaUploadOK
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	first.Body.Close()

	second := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-dup", input, "soldering.jpg")
	var secondOut mediaUploadOK
	if err := json.NewDecoder(second.Body).Decode(&secondOut); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	second.Body.Close()

	if firstOut.Path != secondOut.Path {
		t.Errorf("re-uploading the identical file produced different paths: %q vs %q", firstOut.Path, secondOut.Path)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("expected exactly 1 stored file for an exact re-upload, got %v", names)
	}
}

// TestAdminMedia_IdenticalBytesDifferentNamesConverge_OnHashOnly proves the
// narrower half of the same claim for two DIFFERENT original filenames
// carrying identical bytes: the scheme's dedup is at the hash-of-stripped-
// bytes level, not the full stored filename, so the two responses differ
// (different stems) but share a hash suffix and neither response's file
// clobbers the other on disk.
func TestAdminMedia_IdenticalBytesDifferentNamesConverge_OnHashOnly(t *testing.T) {
	pool := adminMediaTestPool(t)
	dir := t.TempDir()
	srv := httptest.NewServer(adminMediaMux(pool, dir))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-dup-names@example.com")
	seedSession(t, pool, admin, "admin-token-dup-names")

	input := buildTestJPEG(t, 8, 8, false)

	first := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-dup-names", input, "a.jpg")
	var firstOut mediaUploadOK
	if err := json.NewDecoder(first.Body).Decode(&firstOut); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	first.Body.Close()

	second := doMediaUpload(t, srv.Client(), srv.URL+"/admin/media/upload", "admin-token-dup-names", input, "b.jpg")
	var secondOut mediaUploadOK
	if err := json.NewDecoder(second.Body).Decode(&secondOut); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	second.Body.Close()

	hashSuffix := func(path string) string {
		name := strings.TrimPrefix(path, "/media/")
		i := strings.LastIndex(name, "-")
		j := strings.LastIndex(name, ".")
		return name[i+1 : j]
	}
	if hashSuffix(firstOut.Path) != hashSuffix(secondOut.Path) {
		t.Errorf("identical bytes produced different hash suffixes: %q vs %q", firstOut.Path, secondOut.Path)
	}
	if firstOut.Path == secondOut.Path {
		t.Errorf("different original filenames produced the identical stored path %q — expected differing stems", firstOut.Path)
	}
}

func TestAdminMedia_MissingFileField(t *testing.T) {
	pool := adminMediaTestPool(t)
	srv := httptest.NewServer(adminMediaMux(pool, t.TempDir()))
	defer srv.Close()

	admin := seedAdminUser(t, pool, "admin-media-missing-file@example.com")
	seedSession(t, pool, admin, "admin-token-missing-file")

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if err := w.WriteField("not_a_file", "x"); err != nil {
		t.Fatalf("WriteField: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/admin/media/upload", &buf)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = withCookie(req, "admin-token-missing-file")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body := readBody(t, resp)
		t.Fatalf("status = %d, want 400; body: %s", resp.StatusCode, body)
	}
}
