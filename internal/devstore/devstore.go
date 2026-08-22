// Package devstore provides an in-memory storage backend for local development.
//
// It implements every store interface the HTTP handlers depend on so the app can
// boot and serve the SPA and API with no PostgreSQL running. The store is
// selected ONLY by an explicit STORAGE=json (see config.Config.DevMode) — never
// by an empty DATABASE_URL — so production can't accidentally engage it.
//
// Design choices:
//   - In-memory (not JSON files) for simplicity and reliability. The issue's
//     acceptance criteria allow either; in-memory removes file I/O complexity
//     and is the preferred approach documented in the implementer brief.
//   - A single mutex guards all mutable state so concurrent requests are safe.
//   - Seeded with a mock admin user so the UI is not empty.
//   - Satisfies the Pinger interface with a no-op so /health returns 200.
//   - Auth/credential/passkey operations return stubs (dev auth bypass logs in
//     the mock admin directly; see middleware.DevAutoLogin).
//
// The entity set here covers what survives the #0002 shortener strip: users,
// sessions, settings, credentials (stubbed), and audit. Subscriber, interest,
// campaign (email), and workshop fakes are added incrementally as those later
// phases land.
package devstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
)

var logger = slog.Default()

// seedAdminID is the fixed user id for the seeded admin account.
const seedAdminID int64 = 1

// Store is the in-memory dev store. It implements all handler-facing interfaces
// in a single struct to keep wiring simple — main.go constructs one *Store and
// passes it to every constructor that needs a store argument.
type Store struct {
	mu       sync.Mutex
	users    []auth.ManagedUser
	audit    []audit.Record
	sessions map[string]sessionEntry // token → session
	settings []auth.Setting
	// nextAuditID is the auto-increment counter for audit record IDs.
	nextAuditID int64
}

type sessionEntry struct {
	userID    int64
	expiresAt time.Time
}

// New constructs a Store seeded with a mock admin user.
// adminEmail is the ADMIN_EMAIL config value; a sensible default is used when empty.
func New(adminEmail string) *Store {
	if adminEmail == "" {
		adminEmail = "admin@localhost"
	}
	now := time.Now()

	s := &Store{
		sessions:    make(map[string]sessionEntry),
		nextAuditID: 1,
	}

	// Seed the mock admin user (id=1).
	s.users = []auth.ManagedUser{
		{
			ID:        seedAdminID,
			Email:     adminEmail,
			IsAdmin:   true,
			Active:    true,
			CreatedAt: now,
		},
	}

	// Seed the settings the admin Settings tab edits: registrations_enabled
	// and physical_address (migrations 000004 and 000008 respectively on the
	// Postgres path). physical_address starts empty here too, matching the
	// migration seed.
	s.settings = []auth.Setting{
		{Key: "registrations_enabled", Value: "true"},
		{Key: "physical_address", Value: ""},
	}

	logger.Info("devstore: seeded in-memory store", "admin_email", adminEmail)
	return s
}

// ── Pinger ─────────────────────────────────────────────────────────────────

// Ping satisfies handlers.Pinger; always returns nil so /health reports "ok".
func (s *Store) Ping(_ context.Context) error { return nil }

// ── middleware.SessionResolver ──────────────────────────────────────────────

// ResolveSession validates a dev session token. In dev mode sessions are seeded
// directly (see CreateDevSession); passkey login is not available.
func (s *Store) ResolveSession(ctx context.Context, token string, now time.Time) (auth.SessionUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[token]
	if !ok || !entry.expiresAt.After(now) {
		return auth.SessionUser{}, auth.ErrSessionInvalid
	}
	for _, u := range s.users {
		if u.ID == entry.userID {
			return auth.SessionUser{ID: u.ID, Email: u.Email, IsAdmin: u.IsAdmin}, nil
		}
	}
	return auth.SessionUser{}, auth.ErrSessionInvalid
}

// CreateDevSession inserts a session for the given userID and returns the token
// and its expiry. Used by the dev auth bypass to log in the mock admin without a
// WebAuthn ceremony.
func (s *Store) CreateDevSession(userID int64, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	tok, err := auth.NewSessionToken()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("devstore: generating session token: %w", err)
	}
	exp := time.Now().Add(ttl)
	s.mu.Lock()
	s.sessions[tok] = sessionEntry{userID: userID, expiresAt: exp}
	s.mu.Unlock()
	return tok, exp, nil
}

// DeleteSession removes a session token (logout). Returns the user id or 0 when
// not found (idempotent, mirrors auth.Store.DeleteSession).
func (s *Store) DeleteSession(_ context.Context, token string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[token]
	if !ok {
		return 0, nil
	}
	delete(s.sessions, token)
	return e.userID, nil
}

// DeleteSessionsForUser removes every session belonging to userID and returns
// how many were deleted ("sign out everywhere"). Mirrors
// auth.Store.DeleteSessionsForUser; idempotent like DeleteSession.
func (s *Store) DeleteSessionsForUser(_ context.Context, userID int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int64
	for tok, e := range s.sessions {
		if e.userID == userID {
			delete(s.sessions, tok)
			n++
		}
	}
	return n, nil
}

// ── settingStore (handlers.SettingsHandler) ─────────────────────────────────

// ListSettings returns all dev settings.
func (s *Store) ListSettings(_ context.Context) ([]auth.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]auth.Setting, len(s.settings))
	copy(out, s.settings)
	return out, nil
}

// UpdateSetting updates an existing setting. Returns ErrSettingNotFound for an
// unknown key (no new keys are created via this path).
func (s *Store) UpdateSetting(_ context.Context, key, value string, now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, st := range s.settings {
		if st.Key == key {
			old := st.Value
			s.settings[i].Value = value
			s.settings[i].UpdatedAt = &now
			return old, nil
		}
	}
	return "", auth.ErrSettingNotFound
}

// ── credentialStore (handlers.CredentialsHandler) ──────────────────────────
// Passkey management is not available in dev; stub returns empty/not-found.

// ListCredentialsForUser returns an empty slice — no passkeys in dev mode.
func (s *Store) ListCredentialsForUser(_ context.Context, _ int64) ([]auth.ManagedCredential, error) {
	return []auth.ManagedCredential{}, nil
}

// RenameCredential always returns ErrCredentialNotFound in dev mode.
func (s *Store) RenameCredential(_ context.Context, _, _ int64, _ string) error {
	return auth.ErrCredentialNotFound
}

// RevokeCredential always returns ErrCredentialNotFound in dev mode.
func (s *Store) RevokeCredential(_ context.Context, _, _ int64) error {
	return auth.ErrCredentialNotFound
}

// ── userStore (handlers.AdminUsersHandler) ──────────────────────────────────

// ListUsers returns all dev users.
func (s *Store) ListUsers(_ context.Context) ([]auth.ManagedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]auth.ManagedUser, len(s.users))
	copy(out, s.users)
	return out, nil
}

// GetUser returns the user detail for id. PasskeyCount is always 0 in dev mode
// (no passkeys are ever enrolled).
func (s *Store) GetUser(_ context.Context, id int64) (auth.UserDetail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == id {
			return auth.UserDetail{ManagedUser: u, PasskeyCount: 0}, nil
		}
	}
	return auth.UserDetail{}, auth.ErrUserNotFound
}

// DeactivateUser sets active=false for the target user.
func (s *Store) DeactivateUser(_ context.Context, id int64, auditor *audit.Logger, entry audit.Entry) (auth.ManagedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if u.ID == id {
			if u.IsAdmin {
				return auth.ManagedUser{}, auth.ErrUserIsAdmin
			}
			if !u.Active {
				return auth.ManagedUser{}, auth.ErrUserAlreadyInactive
			}
			s.users[i].Active = false
			return s.users[i], nil
		}
	}
	return auth.ManagedUser{}, auth.ErrUserNotFound
}

// ReactivateUser sets active=true for the target user.
func (s *Store) ReactivateUser(_ context.Context, id int64, auditor *audit.Logger, entry audit.Entry) (auth.ManagedUser, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, u := range s.users {
		if u.ID == id {
			if u.Active {
				return auth.ManagedUser{}, auth.ErrUserAlreadyActive
			}
			s.users[i].Active = true
			return s.users[i], nil
		}
	}
	return auth.ManagedUser{}, auth.ErrUserNotFound
}

// ── auditReader (handlers.AdminAuditHandler) ────────────────────────────────

// ListAuditLog returns audit records newest-first with optional user_id/
// target_type/target_id filters (all ANDed together, mirroring
// audit.Reader.ListAuditLog's Filter — see internal/audit/read.go's doc
// comment for why a bare TargetID without TargetType is not treated as a
// filter). A NULL-actor row (e.g. #0045's send worker writes) is returned
// like any other row: none of these filters touch actor_id.
func (s *Store) ListAuditLog(_ context.Context, filter audit.Filter, limit, offset int) ([]audit.Record, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var filtered []audit.Record
	for i := len(s.audit) - 1; i >= 0; i-- {
		rec := s.audit[i]
		if filter.UserID != nil && (rec.UserID == nil || *rec.UserID != *filter.UserID) {
			continue
		}
		if filter.TargetType != "" {
			if rec.TargetType == nil || *rec.TargetType != filter.TargetType {
				continue
			}
			if filter.TargetID != nil && (rec.TargetID == nil || *rec.TargetID != *filter.TargetID) {
				continue
			}
		}
		filtered = append(filtered, rec)
	}
	total := int64(len(filtered))
	if offset >= len(filtered) {
		return []audit.Record{}, total, nil
	}
	end := offset + limit
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[offset:end], total, nil
}

// recordAudit appends an audit record (called by the audit.Logger shim below).
// The caller must NOT hold mu.
func (s *Store) recordAudit(e audit.Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	tt := e.TargetType
	var ttPtr *string
	if tt != "" {
		ttPtr = &tt
	}
	var metaRaw json.RawMessage
	if e.Metadata != nil {
		b, err := json.Marshal(e.Metadata)
		if err == nil {
			metaRaw = b
		}
	}
	var ipPtr *string
	if e.IP != "" {
		ipPtr = &e.IP
	}
	s.audit = append(s.audit, audit.Record{
		ID:         s.nextAuditID,
		ActorID:    e.ActorID,
		UserID:     e.UserID,
		Action:     e.Action,
		TargetType: ttPtr,
		TargetID:   e.TargetID,
		Metadata:   metaRaw,
		IP:         ipPtr,
		CreatedAt:  now,
	})
	s.nextAuditID++
}

// ── audit.Logger shim ───────────────────────────────────────────────────────
//
// DevAuditLogger would wrap *Store and satisfy the *audit.Logger drop-in by
// recording entries in memory rather than Postgres. Handlers receive
// *audit.Logger (a concrete type), so main.go passes nil for the *audit.Logger
// while using the devstore's own recordAudit path for audit.
//
// In practice the handlers check `if h.auditor != nil` before calling
// Record/WriteTx. So dev mode passes nil auditor to all handlers — audit
// entries are silently skipped. The dev store still implements the
// auditReader interface so GET /admin/audit works (returns whatever recordAudit
// has accumulated, empty on a fresh boot).

// RegistrationsEnabled returns the value of the registrations_enabled setting.
// Used by the registration service — not available in dev (passkey auth is
// stubbed), but included for completeness.
func (s *Store) RegistrationsEnabled(_ context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.settings {
		if st.Key == "registrations_enabled" {
			return st.Value == "true", nil
		}
	}
	return false, nil
}

// ── Auth service stubs ───────────────────────────────────────────────────────
// The full WebAuthn ceremony is not available in dev mode. The auth handler is
// still mounted (so the routes exist) but its services are backed by stubs that
// return errors that the handler already handles gracefully.

// DevRegistrar is a stub registrar that refuses all registration attempts in dev mode.
// It satisfies the handlers.registrar interface.
type DevRegistrar struct{}

func (DevRegistrar) StartRegistration(_ context.Context, _, _ string) error {
	return errors.New("devstore: registration not available in dev mode")
}
func (DevRegistrar) VerifyRegistration(_ context.Context, _ string) (*protocol.CredentialCreation, error) {
	return nil, auth.ErrTokenInvalid
}
func (DevRegistrar) FinishRegistration(_ context.Context, _, _, _ string, _ *http.Request) (auth.FinishResult, error) {
	return auth.FinishResult{}, auth.ErrTokenInvalid
}

// DevRecoverer is a stub recoverer for dev mode.
// It satisfies the handlers.recoverer interface.
type DevRecoverer struct{}

func (DevRecoverer) StartRecovery(_ context.Context, _, _ string) error { return nil }
func (DevRecoverer) VerifyRecovery(_ context.Context, _ string) (*protocol.CredentialCreation, error) {
	return nil, auth.ErrTokenInvalid
}
func (DevRecoverer) FinishRecovery(_ context.Context, _, _, _ string, _ *http.Request) (auth.RecoveryResult, error) {
	return auth.RecoveryResult{}, auth.ErrTokenInvalid
}

// ── seedAdminEmail helpers ───────────────────────────────────────────────────

// AdminEmail returns the email of the seeded admin user.
func (s *Store) AdminEmail() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.users {
		if u.ID == seedAdminID {
			return u.Email
		}
	}
	return ""
}

// AdminID returns the id of the seeded admin user.
func (s *Store) AdminID() int64 { return seedAdminID }

// ── Logout shim ─────────────────────────────────────────────────────────────

// devLoginService is a thin wrapper that satisfies the authenticator interface
// expected by handlers.NewAuthHandler, delegating Logout to the store's session
// deletion so the logout route still works in dev mode.
type devLoginService struct {
	store *Store
}

func (d *devLoginService) StartLogin(_ context.Context, _ string) (*protocol.CredentialAssertion, error) {
	return nil, fmt.Errorf("devstore: passkey login not available (use dev auth bypass)")
}

func (d *devLoginService) FinishLogin(_ context.Context, _ string, _ *http.Request) (auth.LoginResult, error) {
	return auth.LoginResult{}, fmt.Errorf("devstore: passkey login not available (use dev auth bypass)")
}

func (d *devLoginService) Logout(ctx context.Context, token, _ string) error {
	_, err := d.store.DeleteSession(ctx, token)
	return err
}

// LogoutAll deletes every dev session for userID ("sign out everywhere").
// Dev mode has no mailer/audit wiring, so this is just the bulk session delete.
func (d *devLoginService) LogoutAll(ctx context.Context, userID int64, _, _ string) (int64, error) {
	return d.store.DeleteSessionsForUser(ctx, userID)
}

// NewDevLoginService returns an authenticator-compatible service that handles
// logout (session deletion) but stubs out passkey start/finish.
func NewDevLoginService(s *Store) *devLoginService { return &devLoginService{store: s} }

// ── compile-time interface checks — the compiler will report any missing methods.

var _ interface {
	ListSettings(ctx context.Context) ([]auth.Setting, error)
	UpdateSetting(ctx context.Context, key, value string, now time.Time) (string, error)
} = (*Store)(nil)

var _ interface {
	ListCredentialsForUser(ctx context.Context, userID int64) ([]auth.ManagedCredential, error)
	RenameCredential(ctx context.Context, userID, id int64, deviceName string) error
	RevokeCredential(ctx context.Context, userID, id int64) error
} = (*Store)(nil)

var _ interface {
	ListUsers(ctx context.Context) ([]auth.ManagedUser, error)
	GetUser(ctx context.Context, id int64) (auth.UserDetail, error)
	DeactivateUser(ctx context.Context, id int64, auditor *audit.Logger, entry audit.Entry) (auth.ManagedUser, error)
	ReactivateUser(ctx context.Context, id int64, auditor *audit.Logger, entry audit.Entry) (auth.ManagedUser, error)
} = (*Store)(nil)

var _ interface {
	ListAuditLog(ctx context.Context, filter audit.Filter, limit, offset int) ([]audit.Record, int64, error)
} = (*Store)(nil)

var _ interface {
	ResolveSession(ctx context.Context, token string, now time.Time) (auth.SessionUser, error)
} = (*Store)(nil)

var _ interface {
	Ping(ctx context.Context) error
} = (*Store)(nil)
