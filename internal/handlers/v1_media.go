package handlers

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/api"
	"github.com/brennanMKE/OpenCircuitSF/internal/apitokens"
	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
)

// The /api/v1 media operations (#0705). An agent cannot send a file through
// an MCP tool call, so create_media_upload mints a single-use upload ticket
// and the agent sends the file with curl to a plain REST route
// (MediaV1.Upload) mounted outside the registry. That route authenticates
// with the ticket in the Authorization header only: the URL carries a
// non-secret ticket id, so nothing secret reaches a URL or an access log,
// and the ticket is never logged or written to audit metadata.

// mediaUploadTicketTTL is how long an upload ticket stays usable.
const mediaUploadTicketTTL = 10 * time.Minute

// mediaUploadPathPrefix is the upload route's path; the ticket id follows.
const mediaUploadPathPrefix = "/api/v1/media/uploads/"

// MediaUploadPattern is the mux pattern cmd/opencircuit mounts Upload at.
const MediaUploadPattern = "POST " + mediaUploadPathPrefix + "{id}"

// uploadTicket is one minted ticket. Only the secret's hash is kept.
type uploadTicket struct {
	secretHash [32]byte
	actor      audit.APITokenActor
	expires    time.Time
	inUse      bool
}

// uploadTickets is an in-memory, one-process ticket store keyed by ticket
// id. A ticket is removed only by a successful upload (#0708); expired ones are swept on every access.
type uploadTickets struct {
	mu      sync.Mutex
	now     func() time.Time
	tickets map[string]uploadTicket
}

func newUploadTickets(now func() time.Time) *uploadTickets {
	return &uploadTickets{now: now, tickets: map[string]uploadTicket{}}
}

func (s *uploadTickets) sweepLocked(now time.Time) {
	for id, t := range s.tickets {
		if !now.Before(t.expires) {
			delete(s.tickets, id)
		}
	}
}

// mint returns a new ticket's id, secret and expiry.
func (s *uploadTickets) mint(actor audit.APITokenActor) (id, secret string, expires time.Time, err error) {
	idb, sb := make([]byte, 16), make([]byte, 32)
	if _, err = rand.Read(idb); err != nil {
		return "", "", time.Time{}, err
	}
	if _, err = rand.Read(sb); err != nil {
		return "", "", time.Time{}, err
	}
	id, secret = hex.EncodeToString(idb), "oup_"+hex.EncodeToString(sb)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	expires = now.Add(mediaUploadTicketTTL)
	s.tickets[id] = uploadTicket{secretHash: sha256.Sum256([]byte(secret)), actor: actor, expires: expires}
	return id, secret, expires, nil
}

var (
	errTicketInvalid = errors.New("invalid, used or expired upload token")
	errTicketBusy    = errors.New("this upload token is already in use")
)

// claim marks the ticket in use if secret matches and it has not expired
// (#0708). An unknown, expired or mismatched ticket is errTicketInvalid; one
// already claimed by a concurrent upload is errTicketBusy. The caller must
// release or consume it.
func (s *uploadTickets) claim(id, secret string) (audit.APITokenActor, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(s.now())
	t, ok := s.tickets[id]
	if !ok {
		return audit.APITokenActor{}, errTicketInvalid
	}
	h := sha256.Sum256([]byte(secret))
	if subtle.ConstantTimeCompare(h[:], t.secretHash[:]) != 1 {
		return audit.APITokenActor{}, errTicketInvalid
	}
	if t.inUse {
		return audit.APITokenActor{}, errTicketBusy
	}
	t.inUse = true
	s.tickets[id] = t
	return t.actor, nil
}

// release makes a claimed ticket usable again after a refused upload. It is
// a no-op when the ticket is gone.
func (s *uploadTickets) release(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tickets[id]; ok {
		t.inUse = false
		s.tickets[id] = t
	}
}

// consume deletes the ticket; a successful upload spends it.
func (s *uploadTickets) consume(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tickets, id)
}

// MediaV1 backs the v1 media operations and the ticket upload route.
type MediaV1 struct {
	mediaDir func() string
	baseURL  string
	auditor  *audit.Logger
	tickets  *uploadTickets
}

// NewMediaV1 constructs a MediaV1. mediaDir is read on every call, so a
// directory saved under Settings → Server settings applies without a
// restart; an empty one answers with the admin's named 503. A nil auditor
// disables audit writes.
func NewMediaV1(mediaDir func() string, baseURL string, auditor *audit.Logger) *MediaV1 {
	return &MediaV1{mediaDir: mediaDir, baseURL: strings.TrimRight(baseURL, "/"), auditor: auditor, tickets: newUploadTickets(time.Now)}
}

type mediaUploadTicketV1 struct {
	UploadURL   string `json:"upload_url" jsonschema:"POST the file here as multipart field file"`
	UploadToken string `json:"upload_token" jsonschema:"single use; send it as Authorization: Bearer, never in the URL"`
	ExpiresAt   string `json:"expires_at"`
	MaxBytes    int    `json:"max_bytes"`
	Curl        string `json:"curl" jsonschema:"a ready-to-run command; replace cover.jpg with the file"`
}

type mediaFileV1 struct {
	Name       string `json:"name"`
	Path       string `json:"path" jsonschema:"the same-site path for update_event cover_image or update_flyer photo"`
	SizeBytes  int64  `json:"size_bytes"`
	ModifiedAt string `json:"modified_at"`
}

type listMediaIn struct {
	api.Page
}

// mediaCursor is list_media's cursor: newest first, then by name.
type mediaCursor struct {
	ModifiedAt time.Time `json:"m"`
	Name       string    `json:"n"`
}

type mediaEntry struct {
	file mediaFileV1
	mod  time.Time
}

func mediaOperations(m *MediaV1) []api.Operation {
	return []api.Operation{
		api.Define(api.Operation{
			Name: "create_media_upload", Method: http.MethodPost, Path: "/api/v1/media/uploads",
			Scope:            apitokens.ScopeMediaWrite,
			Summary:          "Get a single-use, 10-minute URL and token to upload one JPEG or PNG image with curl.",
			Description:      "Run the returned curl command; its response is {path}, a /media/… path for update_event cover_image or update_flyer photo. Metadata is stripped from the image.",
			Status:           http.StatusCreated,
			MayBeUnavailable: true,
		}, func(ctx context.Context, _ struct{}) (mediaUploadTicketV1, error) {
			if m.mediaDir() == "" {
				return mediaUploadTicketV1{}, api.Unavailable(mediaNotConfiguredMessage)
			}
			var actor audit.APITokenActor
			if t, ok := middleware.APITokenFromContext(ctx); ok {
				actor = audit.APITokenActor{ID: t.ID, Name: t.Name, Prefix: t.Prefix, CreatedBy: t.CreatedBy}
			}
			id, secret, expires, err := m.tickets.mint(actor)
			if err != nil {
				return mediaUploadTicketV1{}, err
			}
			u := m.baseURL + mediaUploadPathPrefix + id
			return mediaUploadTicketV1{
				UploadURL: u, UploadToken: secret, ExpiresAt: expires.In(announceLocation).Format(time.RFC3339),
				MaxBytes: mediaMaxUploadBytes,
				Curl:     fmt.Sprintf(`curl -H "Authorization: Bearer %s" -F file=@cover.jpg %s`, secret, u),
			}, nil
		}),
		api.Define(api.Operation{
			Name: "list_media", Method: http.MethodGet, Path: "/api/v1/media",
			Scope:            apitokens.ScopeMediaRead,
			Summary:          "List uploaded images in the media directory, newest first.",
			MayBeUnavailable: true,
		}, func(ctx context.Context, in listMediaIn) (api.List[mediaFileV1], error) {
			dir := m.mediaDir()
			if dir == "" {
				return api.List[mediaFileV1]{}, api.Unavailable(mediaNotConfiguredMessage)
			}
			ents, err := os.ReadDir(dir)
			if err != nil {
				return api.List[mediaFileV1]{}, api.Unavailable("could not read the media directory")
			}
			items := make([]mediaEntry, 0, len(ents))
			for _, e := range ents {
				if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				fi, err := e.Info()
				if err != nil {
					continue
				}
				mod := fi.ModTime().UTC()
				items = append(items, mediaEntry{mod: mod, file: mediaFileV1{
					Name: e.Name(), Path: "/media/" + e.Name(), SizeBytes: fi.Size(), ModifiedAt: mod.In(announceLocation).Format(time.RFC3339),
				}})
			}
			slices.SortFunc(items, func(a, b mediaEntry) int {
				if c := b.mod.Compare(a.mod); c != 0 {
					return c
				}
				return cmp.Compare(a.file.Name, b.file.Name)
			})
			page, err := paginate(items, in.Page,
				func(v mediaEntry) mediaCursor { return mediaCursor{ModifiedAt: v.mod, Name: v.file.Name} },
				func(v mediaEntry, c mediaCursor) bool {
					return v.mod.Before(c.ModifiedAt) || (v.mod.Equal(c.ModifiedAt) && v.file.Name > c.Name)
				})
			if err != nil {
				return api.List[mediaFileV1]{}, err
			}
			out := make([]mediaFileV1, 0, len(page.Items))
			for _, e := range page.Items {
				out = append(out, e.file)
			}
			return api.List[mediaFileV1]{Items: out, NextCursor: page.NextCursor}, nil
		}),
	}
}

// Upload handles POST /api/v1/media/uploads/{id}: the ticket from
// create_media_upload, as a Bearer header, and a multipart "file" field. It
// answers {path} like POST /admin/media/upload and shares its pipeline
// (storeImage). An unknown, used or expired ticket gets 401, and one that a
// concurrent upload holds gets 409. A refused upload (wrong type, too large,
// unreadable) releases the ticket; only a stored image spends it (#0708).
func (m *MediaV1) Upload(w http.ResponseWriter, r *http.Request) {
	secret, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || secret == "" {
		writeError(w, http.StatusUnauthorized, "missing upload token")
		return
	}
	id := r.PathValue("id")
	actor, err := m.tickets.claim(id, strings.TrimSpace(secret))
	if errors.Is(err, errTicketBusy) {
		writeError(w, http.StatusConflict, errTicketBusy.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusUnauthorized, errTicketInvalid.Error())
		return
	}
	consumed := false
	defer func() {
		if !consumed {
			m.tickets.release(id)
		}
	}()
	dir := m.mediaDir()
	if dir == "" {
		writeError(w, http.StatusServiceUnavailable, mediaNotConfiguredMessage)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, mediaMaxUploadBytes+1<<16)
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
	body, err := io.ReadAll(io.LimitReader(file, mediaMaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return
	}
	if len(body) > mediaMaxUploadBytes {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("file exceeds the %d byte upload limit", mediaMaxUploadBytes))
		return
	}

	stored, serr := storeImage(dir, header.Filename, body)
	if serr != nil {
		writeError(w, serr.status, serr.msg)
		return
	}
	m.tickets.consume(id)
	consumed = true
	if m.auditor != nil {
		m.auditor.Record(r.Context(), audit.AttributeAPIToken(audit.Entry{
			Action: audit.ActionMediaImageUploaded,
			Metadata: map[string]any{
				"filename":    stored.filename,
				"stored_path": stored.path,
				"format":      string(stored.format),
				"size_bytes":  len(stored.data),
				"width":       stored.cfg.Width,
				"height":      stored.cfg.Height,
			},
			IP: clientIP(r),
		}, actor))
	}
	writeJSON(w, http.StatusOK, mediaUploadResponse{Path: stored.path})
}

// UploadHandler is Upload behind the failed-auth limiter (#0708). A ticket
// is not an API token, so it uses BearerAuthFailureLimit, which counts any
// bearer's 401; pass the same limiter as /api/v1/ and /mcp. A nil limiter
// disables the limit.
func (m *MediaV1) UploadHandler(authFail *middleware.RateLimiter) http.Handler {
	return middleware.BearerAuthFailureLimit(authFail)(http.HandlerFunc(m.Upload))
}
