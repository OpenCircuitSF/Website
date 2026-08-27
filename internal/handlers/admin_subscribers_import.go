// admin_subscribers_import.go implements the admin-only CSV import path
// (#0125, PRD §6.10): POST /admin/subscribers/import/preview (dry run,
// writes nothing), POST /admin/subscribers/import (commit), and POST
// /admin/imports/{id}/revoke (undo a whole batch in one action). This issue
// implements consent_mode=prior_consent only — invite mode is #0129; see
// internal/subscribers/imports.go's package doc comment for why Commit
// refuses ConsentModeInvite rather than silently downgrading it.
//
// # Column mapping is explicit, never guessed
//
// The email column is chosen by the caller as a 0-based index into each
// CSV row — never inferred from a header name — per #0125's acceptance
// criteria. The wizard (web/src/views/Admin.svelte) reads the file's
// header row client-side to populate a dropdown; this handler only ever
// receives the admin's explicit choice.
//
// # Preview/commit share the checksum, not server-side state
//
// This handler is stateless between requests: Preview computes a SHA-256
// checksum over the exact multipart file bytes and returns it; Commit
// requires the caller to resubmit the SAME file and re-derives the checksum
// itself, refusing with 409 if it does not match what the caller claims to
// have previewed. This is what "requiring the preview's checksum so the
// file cannot change between preview and commit" means in practice — there
// is no server-side session or temp-file storage of an uploaded CSV between
// the two requests.
//
// # Bound
//
// importMaxFileBytes/importMaxDataRows cap what a single import can carry —
// stated in the wizard's own copy, not just here, per #0125's acceptance
// criteria ("Import size is bounded, and the bound is stated in the UI").
package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/interests"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// importMaxFileBytes bounds the multipart upload (#0125's acceptance
// criteria: "the bound is stated in the UI" — see
// web/src/lib/admin.ts's importMaxFileBytes constant, which must be kept in
// sync with this one; there is no runtime way to derive one from the other
// since they live in different processes).
const importMaxFileBytes = 5 << 20 // 5 MiB

// importMaxDataRows bounds the number of CSV data rows (excluding the
// header) a single import may carry — import is explicitly the SECONDARY,
// low-volume path (PRD §6.10's Notes; CLAUDE.md's issue notes: "not
// expected to carry much volume"), not a bulk-data tool.
const importMaxDataRows = 5000

// adminImportSubscriberStore is the behavior AdminImportsHandler needs from
// internal/subscribers.ImportStore.
type adminImportSubscriberStore interface {
	Preview(ctx context.Context, rows []subscribers.ImportRow, knownSlugs map[string]bool) (subscribers.PreviewResult, error)
	Commit(ctx context.Context, in subscribers.CommitInput, now time.Time) (subscribers.CommitResult, error)
	Revoke(ctx context.Context, id int64, reason string, now time.Time) (imp subscribers.Import, revokedEmails []string, alreadyRevoked bool, err error)
}

// adminImportInterestReader is the behavior AdminImportsHandler needs from
// internal/interests to resolve the optional interest-slug column.
type adminImportInterestReader interface {
	ListAll(ctx context.Context) ([]interests.Interest, error)
}

// AdminImportsHandler serves the admin-only subscriber import routes (PRD
// §5.2/§6.10; #0125):
//
//	POST /admin/subscribers/import/preview  — dry run: counts, writes nothing
//	POST /admin/subscribers/import          — commit
//	POST /admin/imports/{id}/revoke         — undo a whole batch
//
// All routes MUST be mounted behind middleware.RequireSession then
// middleware.RequireAdmin, exactly like the other admin handlers in this
// package — see cmd/opencircuit/main.go's adminRoutes. May be constructed
// with a nil auditor (disables audit writes) — never with a nil store or
// interests reader; main.go always supplies both together since both come
// from the same STORAGE=postgres wiring subscribers/interests already
// require (see AdminSubscribersHandler's identical convention).
type AdminImportsHandler struct {
	store     adminImportSubscriberStore
	interests adminImportInterestReader
	auditor   *audit.Logger
	now       func() time.Time
}

// NewAdminImportsHandler constructs an AdminImportsHandler.
func NewAdminImportsHandler(store adminImportSubscriberStore, il adminImportInterestReader, auditor *audit.Logger) *AdminImportsHandler {
	return &AdminImportsHandler{store: store, interests: il, auditor: auditor, now: time.Now}
}

// importView is the JSON shape for one subscriber_imports row.
type importView struct {
	ID            int64   `json:"id"`
	Source        string  `json:"source"`
	SourceDetail  *string `json:"source_detail,omitempty"`
	ConsentMode   string  `json:"consent_mode"`
	ConsentNote   string  `json:"consent_note"`
	CollectedAt   string  `json:"collected_at"` // YYYY-MM-DD
	Filename      *string `json:"filename,omitempty"`
	RowCount      int     `json:"row_count"`
	InsertedCount int     `json:"inserted_count"`
	SkippedCount  int     `json:"skipped_count"`
	Status        string  `json:"status"`
	RevokedAt     *string `json:"revoked_at,omitempty"`
	RevokedReason *string `json:"revoked_reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

func toImportView(imp subscribers.Import) importView {
	return importView{
		ID:            imp.ID,
		Source:        imp.Source,
		SourceDetail:  imp.SourceDetail,
		ConsentMode:   imp.ConsentMode,
		ConsentNote:   imp.ConsentNote,
		CollectedAt:   imp.CollectedAt.Format("2006-01-02"),
		Filename:      imp.Filename,
		RowCount:      imp.RowCount,
		InsertedCount: imp.InsertedCount,
		SkippedCount:  imp.SkippedCount,
		Status:        imp.Status,
		RevokedAt:     formatTimePtr(imp.RevokedAt),
		RevokedReason: imp.RevokedReason,
		CreatedAt:     imp.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// parsedImportUpload is what parseImportUpload extracts from a multipart
// request — shared by Preview and Commit so the two can never disagree on
// how a column index or a CSV row is interpreted.
type parsedImportUpload struct {
	fileBytes       []byte
	checksum        string // hex sha256 of fileBytes
	source          string
	sourceDetail    string
	consentMode     string
	consentNote     string
	collectedAt     time.Time
	filename        string
	rows            []subscribers.ImportRow
	malformedCount  int
	sampleMalformed []string
}

const importSampleSize = 10

// parseImportUpload reads and validates the shared multipart fields (file,
// source, source_detail, consent_mode, consent_note, collected_at,
// email_column, interest_column), bounds the upload, parses the CSV with
// encoding/csv (a real parser — quoting, embedded commas/newlines all
// handled correctly, unlike a naive split), and classifies each data row's
// email as malformed (failing validEmailSyntax) or a candidate. It does NOT
// touch the database — that is Preview/Commit's job — so this is safe to
// call from both without any risk of the two disagreeing on parsing.
func parseImportUpload(w http.ResponseWriter, r *http.Request) (parsedImportUpload, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, importMaxFileBytes+1<<16) // file + form overhead
	if err := r.ParseMultipartForm(importMaxFileBytes); err != nil {
		writeError(w, http.StatusBadRequest, "could not parse upload (too large, or not multipart/form-data)")
		return parsedImportUpload{}, false
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file")
		return parsedImportUpload{}, false
	}
	defer file.Close()

	limited := io.LimitReader(file, importMaxFileBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return parsedImportUpload{}, false
	}
	if len(body) > importMaxFileBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds the %d byte import limit", importMaxFileBytes))
		return parsedImportUpload{}, false
	}

	source := strings.TrimSpace(r.FormValue("source"))
	sourceDetail := strings.TrimSpace(r.FormValue("source_detail"))
	consentMode := strings.TrimSpace(r.FormValue("consent_mode"))
	consentNote := strings.TrimSpace(r.FormValue("consent_note"))
	collectedAtRaw := strings.TrimSpace(r.FormValue("collected_at"))

	collectedAt, err := time.Parse("2006-01-02", collectedAtRaw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid collected_at (want YYYY-MM-DD)")
		return parsedImportUpload{}, false
	}

	emailCol, err := strconv.Atoi(strings.TrimSpace(r.FormValue("email_column")))
	if err != nil || emailCol < 0 {
		writeError(w, http.StatusBadRequest, "invalid or missing email_column")
		return parsedImportUpload{}, false
	}
	interestCol := -1
	if raw := strings.TrimSpace(r.FormValue("interest_column")); raw != "" {
		interestCol, err = strconv.Atoi(raw)
		if err != nil || interestCol < 0 {
			writeError(w, http.StatusBadRequest, "invalid interest_column")
			return parsedImportUpload{}, false
		}
	}

	cr := csv.NewReader(strings.NewReader(string(body)))
	cr.FieldsPerRecord = -1 // rows may have ragged lengths; missing columns are treated as empty below
	records, err := cr.ReadAll()
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not parse CSV")
		return parsedImportUpload{}, false
	}
	if len(records) == 0 {
		writeError(w, http.StatusBadRequest, "CSV has no rows")
		return parsedImportUpload{}, false
	}
	dataRecords := records[1:] // first row is always the header
	if len(dataRecords) > importMaxDataRows {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("CSV has %d data rows, exceeding the %d row import limit", len(dataRecords), importMaxDataRows))
		return parsedImportUpload{}, false
	}

	var out parsedImportUpload
	sum := sha256.Sum256(body)
	out.fileBytes = body
	out.checksum = hex.EncodeToString(sum[:])
	out.source = source
	out.sourceDetail = sourceDetail
	out.consentMode = consentMode
	out.consentNote = consentNote
	out.collectedAt = collectedAt
	if header != nil {
		out.filename = header.Filename
	}

	for _, rec := range dataRecords {
		email := ""
		if emailCol < len(rec) {
			email = strings.TrimSpace(rec[emailCol])
		}
		if email == "" {
			continue // a blank line — nothing to classify, nothing to insert
		}
		if !validEmailSyntax(strings.ToLower(email)) {
			out.malformedCount++
			if len(out.sampleMalformed) < importSampleSize {
				out.sampleMalformed = append(out.sampleMalformed, email)
			}
			continue
		}
		var slugs []string
		if interestCol >= 0 && interestCol < len(rec) {
			for _, s := range strings.Split(rec[interestCol], ";") {
				s = strings.ToLower(strings.TrimSpace(s))
				if s != "" {
					slugs = append(slugs, s)
				}
			}
		}
		out.rows = append(out.rows, subscribers.ImportRow{Email: email, InterestSlugs: slugs})
	}

	return out, true
}

func (h *AdminImportsHandler) knownInterestSlugs(ctx context.Context) (map[string]bool, map[string]int64, error) {
	all, err := h.interests.ListAll(ctx)
	if err != nil {
		return nil, nil, err
	}
	known := make(map[string]bool, len(all))
	toID := make(map[string]int64, len(all))
	for _, it := range all {
		known[it.Slug] = true
		toID[it.Slug] = it.ID
	}
	return known, toID, nil
}

// previewResponse is POST /admin/subscribers/import/preview's 200 body.
type previewResponse struct {
	Checksum             string   `json:"checksum"`
	RowCount             int      `json:"row_count"`
	NewCount             int      `json:"new_count"`
	DuplicateCount       int      `json:"duplicate_count"`
	SuppressedCount      int      `json:"suppressed_count"`
	MalformedCount       int      `json:"malformed_count"`
	SampleNew            []string `json:"sample_new,omitempty"`
	SampleDuplicate      []string `json:"sample_duplicate,omitempty"`
	SampleSuppressed     []string `json:"sample_suppressed,omitempty"`
	SampleMalformed      []string `json:"sample_malformed,omitempty"`
	UnknownInterestSlugs []string `json:"unknown_interest_slugs,omitempty"`
}

// Preview handles POST /admin/subscribers/import/preview. Writes nothing.
func (h *AdminImportsHandler) Preview(w http.ResponseWriter, r *http.Request) {
	if _, ok := middleware.UserFromContext(r.Context()); !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	up, ok := parseImportUpload(w, r)
	if !ok {
		return
	}

	knownSlugs, _, err := h.knownInterestSlugs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	result, err := h.store.Preview(r.Context(), up.rows, knownSlugs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	writeJSON(w, http.StatusOK, previewResponse{
		Checksum:             up.checksum,
		RowCount:             len(up.rows) + up.malformedCount,
		NewCount:             result.NewCount,
		DuplicateCount:       result.DuplicateCount,
		SuppressedCount:      result.SuppressedCount,
		MalformedCount:       up.malformedCount,
		SampleNew:            result.SampleNew,
		SampleDuplicate:      result.SampleDuplicate,
		SampleSuppressed:     result.SampleSuppressed,
		SampleMalformed:      up.sampleMalformed,
		UnknownInterestSlugs: result.UnknownInterestSlugs,
	})
}

// commitResponse is POST /admin/subscribers/import's 200 body.
type commitResponse struct {
	Import importView `json:"import"`
}

// Commit handles POST /admin/subscribers/import. Requires checksum to match
// the SHA-256 of the resubmitted file — see the package doc comment.
func (h *AdminImportsHandler) Commit(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	up, ok := parseImportUpload(w, r)
	if !ok {
		return
	}

	claimedChecksum := strings.TrimSpace(r.FormValue("checksum"))
	if claimedChecksum == "" {
		writeError(w, http.StatusBadRequest, "missing checksum")
		return
	}
	if claimedChecksum != up.checksum {
		writeError(w, http.StatusConflict, "checksum mismatch — the file changed since preview; run preview again")
		return
	}

	_, slugToID, err := h.knownInterestSlugs(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	actorID := actor.ID
	now := h.now()
	result, err := h.store.Commit(r.Context(), subscribers.CommitInput{
		Source:           up.source,
		SourceDetail:     up.sourceDetail,
		ConsentMode:      up.consentMode,
		ConsentNote:      up.consentNote,
		CollectedAt:      up.collectedAt,
		Filename:         up.filename,
		Rows:             up.rows,
		InterestSlugToID: slugToID,
		ImportedBy:       &actorID,
	}, now)
	switch {
	case errors.Is(err, subscribers.ErrInvalidImportSource):
		writeError(w, http.StatusBadRequest, "invalid source")
		return
	case errors.Is(err, subscribers.ErrInvalidConsentMode):
		writeError(w, http.StatusBadRequest, "invalid consent_mode")
		return
	case errors.Is(err, subscribers.ErrSourceDetailRequired):
		writeError(w, http.StatusBadRequest, "source_detail is required")
		return
	case errors.Is(err, subscribers.ErrConsentNoteRequired):
		writeError(w, http.StatusBadRequest, "consent_note is required")
		return
	case errors.Is(err, subscribers.ErrCollectedAtRequired):
		writeError(w, http.StatusBadRequest, "collected_at is required")
		return
	case errors.Is(err, subscribers.ErrConsentModeNotSupported):
		writeError(w, http.StatusBadRequest, "invite mode is not available yet — use prior_consent")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		targetID := result.Import.ID
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberImportCommitted,
			TargetType: audit.TargetSubscriberImport,
			TargetID:   &targetID,
			Metadata: map[string]any{
				"source":          up.source,
				"source_detail":   up.sourceDetail,
				"consent_mode":    up.consentMode,
				"consent_note":    up.consentNote,
				"collected_at":    up.collectedAt.Format("2006-01-02"),
				"filename":        up.filename,
				"row_count":       result.Import.RowCount,
				"inserted_count":  result.Import.InsertedCount,
				"skipped_count":   result.Import.SkippedCount,
				"malformed_count": up.malformedCount,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, commitResponse{Import: toImportView(result.Import)})
}

// revokeRequest is POST /admin/imports/{id}/revoke's body.
type revokeRequest struct {
	Reason string `json:"reason"`
}

// revokeResponse is POST /admin/imports/{id}/revoke's 200 body.
type revokeResponse struct {
	Import       importView `json:"import"`
	RevokedCount int        `json:"revoked_count"`
	NoOp         bool       `json:"no_op"`
}

// Revoke handles POST /admin/imports/{id}/revoke.
func (h *AdminImportsHandler) Revoke(w http.ResponseWriter, r *http.Request) {
	actor, ok := middleware.UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated")
		return
	}

	idRaw := r.PathValue("id")
	id, err := strconv.ParseInt(idRaw, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}

	var req revokeRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	}

	actorID := actor.ID
	now := h.now()
	imp, revokedEmails, noOp, err := h.store.Revoke(r.Context(), id, reason, now)
	switch {
	case errors.Is(err, subscribers.ErrImportNotFound):
		writeError(w, http.StatusNotFound, "import not found")
		return
	case errors.Is(err, subscribers.ErrRevokeReasonRequired):
		writeError(w, http.StatusBadRequest, "reason is required")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if h.auditor != nil {
		h.auditor.Record(r.Context(), audit.Entry{
			ActorID:    &actorID,
			Action:     audit.ActionSubscriberImportRevoked,
			TargetType: audit.TargetSubscriberImport,
			TargetID:   &id,
			Metadata: map[string]any{
				"reason":        reason,
				"revoked_count": len(revokedEmails),
				"no_op":         noOp,
			},
			IP: clientIP(r),
		})
	}

	writeJSON(w, http.StatusOK, revokeResponse{
		Import:       toImportView(imp),
		RevokedCount: len(revokedEmails),
		NoOp:         noOp,
	})
}
