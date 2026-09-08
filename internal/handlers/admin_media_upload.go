// admin_media_upload.go implements the admin-only image upload endpoint
// (#0433, reopening #0153): POST /admin/media/upload writes a validated,
// EXIF-stripped image under the configured media directory and returns the
// same-site path the admin can paste into a workshop's cover_image field —
// the same field #0432's plain text box already writes to. This does not
// replace that field or scp (docs/media.md's fallback stays documented); it
// removes the need for shell access on the common path.
//
// # Why this route can exist with no production write access yet
//
// #0433's planning pass split the production enablement into #0465 (the
// media directory's group/mode and the systemd unit's ReadWritePaths=,
// both approval-gated and outside an agent's reach). This handler is
// provably correct against ANY writable directory — every test in this
// package points mediaDir at a t.TempDir() — and is deliberately mounted
// unconditionally on the Postgres serve path (never nil-guarded out of
// adminRoutes the way, say, a nil workshopsStore would be), returning a
// named 503 when mediaDir is empty rather than omitting the route entirely.
// Omitting it would reproduce docs/obstacles.md §11's failure shape: a run
// mode that silently lacks a subsystem while everything else looks
// healthy. See NewAdminMediaHandler's doc comment for the startup-time
// writability probe that backs this up with a log line, not just a
// request-time check.
//
// # Validation order — never trust the client for format or name
//
// 1. http.MaxBytesReader + ParseMultipartForm bound the upload at
// mediaMaxUploadBytes, mirroring admin_subscribers_import.go's identical
// construct and error shape.
// 2. media.Sniff identifies the format from magic bytes ONLY — never the
// multipart filename's extension, never the client's Content-Type header.
// HEIC gets its own message (heicErrorMessage) because an iPhone shoots
// HEIC by default, and a generic "unsupported format" 415 would make a
// supported workflow look like a bug on the very first real upload.
// 3. image.DecodeConfig (never image.Decode — see internal/media's package
// doc comment on why a full decode is a memory hazard on this box) proves
// the header is structurally coherent and bounds its declared dimensions
// via media.CheckDimensions.
// 4. media.Strip removes every non-allowlisted segment/chunk, and
// media.VerifyStripped re-walks the OUTPUT before it is ever written — the
// strip's own output is the thing served, so it is the thing checked
// (issues/0433.md Plan §3).
//
// # Filename and isSafeCoverImage
//
// media.Filename generates "<sanitised-stem>-<hash-of-stripped-bytes>.<ext>"
// server-side; nothing the caller sends ever reaches a filesystem path
// component (SanitizeStem's output alphabet excludes '.' and '/', and the
// extension comes from the sniffed format, never the client). The
// resulting "/media/<name>" path is then checked against isSafeCoverImage
// (admin_workshops.go) as a POST-CONDITION sanity check, not an input
// filter — issues/0433.md Plan §3 is explicit that this is the correct use
// of that validator here, and that no second, narrower definition of "safe
// path" should be added alongside it.
//
// # The write itself
//
// See internal/media.WriteAtomic's doc comment for the temp-file-in-target-
// directory + Sync + Chmod(0644) + Rename sequence and why each step is
// there. ENOSPC is mapped to 507 (a status naming disk space) rather than a
// generic 500.
package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // register the JPEG decoder for image.DecodeConfig
	_ "image/png"  // register the PNG decoder for image.DecodeConfig
	"io"
	"log/slog"
	"net/http"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/media"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// mediaMaxUploadBytes bounds the multipart upload — 5 MiB, matching
// admin_subscribers_import.go's importMaxFileBytes and stated in the UI
// copy per the same convention (web/src/lib/media.ts's own mirrored
// constant). The two live workshop covers are 63 KB and 89 KB
// (docs/media.md), so this leaves generous headroom for a real phone photo
// while still bounding the worst case.
const mediaMaxUploadBytes = 5 << 20 // 5 MiB

// heicErrorMessage is the 415 body for an HEIC/HEIF upload — broken out
// from the generic unsupported-format message because an iPhone shoots
// HEIC unless its camera is set to "Most Compatible" (issues/0433.md Plan
// §2), so this is the single most likely thing a real admin's first
// upload attempt hits.
const heicErrorMessage = `this looks like an HEIC/HEIF photo, which this server cannot convert — on an iPhone, use Share > Options and choose JPEG (or set Settings > Camera > Formats > Most Compatible), then upload the exported JPEG`

// mediaUnsupportedFormatMessage is the 415 body for a recognizable but
// deliberately refused format (GIF, WebP, SVG).
const mediaUnsupportedFormatMessage = "unsupported image format — only JPEG and PNG are accepted"

// mediaUnrecognizedFormatMessage is the 415 body when the bytes match no
// known image format signature at all.
const mediaUnrecognizedFormatMessage = "not a recognized image file"

// AdminMediaHandler serves the admin-only image upload route (#0433):
//
//	POST /admin/media/upload — validate, strip metadata, and store an image
//
// MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like every other admin handler — see
// cmd/opencircuit/main.go's adminRoutes. Unlike most handlers in this
// package, it is constructed unconditionally on the Postgres serve path
// even when mediaDir is empty (see the package doc comment): Upload itself
// returns a named 503 in that case, rather than the route being omitted.
type AdminMediaHandler struct {
	// mediaDir is the directory uploads are written to — /var/www/media in
	// production (once #0465 lands), a t.TempDir() in every test. An empty
	// string means "not configured": Upload refuses with 503 rather than
	// attempting to write anywhere.
	mediaDir string
	auditor  *audit.Logger
}

// NewAdminMediaHandler constructs an AdminMediaHandler. A nil auditor
// disables audit writes (matching every other admin handler's
// nil-tolerance). mediaDir may be empty — see the package doc comment for
// why the route is mounted unconditionally rather than nil-guarded out of
// existence.
//
// This constructor does not itself probe mediaDir for writability —
// cmd/opencircuit/main.go's wiring does that once at startup via
// media.CheckWritable, logging a warning rather than failing to boot, so a
// misconfigured or not-yet-permissioned directory (#0465 is what grants the
// real permission) is visible in the logs immediately rather than only on
// the first upload attempt.
func NewAdminMediaHandler(mediaDir string, auditor *audit.Logger) *AdminMediaHandler {
	return &AdminMediaHandler{mediaDir: mediaDir, auditor: auditor}
}

// mediaUploadResponse is POST /admin/media/upload's 200 body: the same-site
// path the admin console slots into a workshop's cover_image field.
type mediaUploadResponse struct {
	Path string `json:"path"`
}

// Upload handles POST /admin/media/upload. See the package doc comment for
// the full validation order.
func (h *AdminMediaHandler) Upload(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	if h.mediaDir == "" {
		writeError(w, http.StatusServiceUnavailable,
			"image uploads are not configured on this server (MEDIA_DIR is unset) — use the cover image path field and the documented scp workflow instead")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, mediaMaxUploadBytes+1<<16) // file + form overhead
	if err := r.ParseMultipartForm(mediaMaxUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse upload (too large, or not multipart/form-data)")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()

	limited := io.LimitReader(file, mediaMaxUploadBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return
	}
	if len(body) > mediaMaxUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds the %d byte upload limit", mediaMaxUploadBytes))
		return
	}

	format, err := media.Sniff(body)
	switch {
	case errors.Is(err, media.ErrHEICUnsupported):
		writeError(w, http.StatusUnsupportedMediaType, heicErrorMessage)
		return
	case errors.Is(err, media.ErrUnsupportedFormat):
		writeError(w, http.StatusUnsupportedMediaType, mediaUnsupportedFormatMessage)
		return
	case errors.Is(err, media.ErrUnrecognizedFormat):
		writeError(w, http.StatusUnsupportedMediaType, mediaUnrecognizedFormatMessage)
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	// image.DecodeConfig only, never image.Decode — see internal/media's
	// package doc comment for why a full decode is a memory hazard here.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusUnsupportedMediaType, "could not read image header — the file may be corrupt")
		return
	}
	if err := media.CheckDimensions(cfg.Width, cfg.Height); err != nil {
		writeError(w, http.StatusUnsupportedMediaType, fmt.Sprintf("image dimensions %dx%d exceed the allowed maximum", cfg.Width, cfg.Height))
		return
	}

	stripped, err := media.Strip(format, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not process image")
		return
	}
	if err := media.VerifyStripped(format, stripped); err != nil {
		// The strip's own output still carries something it should have
		// dropped -- a bug in internal/media, not a client error. Refuse
		// rather than write a file we can't stand behind.
		slog.Default().Error("admin_media_upload: stripped output failed verification", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	filename := media.Filename(header.Filename, stripped, format)
	storedPath := "/media/" + filename
	if !isSafeCoverImage(storedPath) {
		// Cannot happen by construction (see media.Filename's doc
		// comment) -- this is the endpoint's own post-condition check,
		// not an input filter (issues/0433.md Plan §3).
		slog.Default().Error("admin_media_upload: generated path failed isSafeCoverImage", "path", storedPath)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if err := media.WriteAtomic(h.mediaDir, filename, stripped); err != nil {
		if media.IsOutOfSpace(err) {
			writeError(w, http.StatusInsufficientStorage, "no space left on the media directory — the image was not saved")
			return
		}
		slog.Default().Error("admin_media_upload: write failed", "err", err, "dir", h.mediaDir)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		actorID := actor.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID: &actorID,
			Action:  audit.ActionMediaImageUploaded,
			Metadata: map[string]any{
				"filename":    filename,
				"stored_path": storedPath,
				"format":      string(format),
				"size_bytes":  len(stripped),
				"width":       cfg.Width,
				"height":      cfg.Height,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, mediaUploadResponse{Path: storedPath})
}
