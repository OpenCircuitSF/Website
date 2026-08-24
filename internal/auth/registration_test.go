package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/protocol/webauthncose"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

// testRPID / testRPOrigin are the relying-party values used across the auth
// integration tests. They match the virtual authenticator's relying party.
const (
	testRPID     = "opencircuitsf.com"
	testRPOrigin = "https://www.opencircuitsf.com"
)

// recordingMailer captures the verification calls so a test can assert the
// mailer was invoked and recover the emailed token (which never leaves the
// server otherwise).
type recordingMailer struct {
	mu     sync.Mutex
	calls  int
	lastTo string
	token  string

	// sessionsRevoked* track SendSessionsRevoked calls for the "sign out
	// everywhere" tests. sessionsRevokedErr lets a test simulate a mailer
	// failure to prove LogoutAll swallows it (fire-and-forget, like the
	// Logout audit write).
	sessionsRevokedCalls int
	sessionsRevokedTo    string
	sessionsRevokedAt    time.Time
	sessionsRevokedErr   error
}

func (m *recordingMailer) SendVerification(_ context.Context, toEmail, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastTo = toEmail
	m.token = token
	return nil
}

func (m *recordingMailer) SendRecovery(_ context.Context, _, _ string) error { return nil }

func (m *recordingMailer) SendSessionsRevoked(_ context.Context, toEmail string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessionsRevokedCalls++
	m.sessionsRevokedTo = toEmail
	m.sessionsRevokedAt = at
	return m.sessionsRevokedErr
}

func (m *recordingMailer) recorded() (int, string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls, m.lastTo, m.token
}

// sessionsRevokedRecorded returns the SendSessionsRevoked call count, last
// recipient, and last timestamp, guarded by the same mutex as the other
// recorded fields.
func (m *recordingMailer) sessionsRevokedRecorded() (int, string, time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessionsRevokedCalls, m.sessionsRevokedTo, m.sessionsRevokedAt
}

// testPool returns the package's single shared pool (opened once in
// TestMain — #0091) or skips if TEST_DATABASE_URL was unset. It truncates
// the auth tables on entry only, so each test still starts from a clean
// slate and re-runs are deterministic.
func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testDBPool == nil {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	truncateAuthTables(t, testDBPool)
	return testDBPool
}

// truncateAuthTables clears every auth-related table (and its dependents) so
// the database is left clean. RESTART IDENTITY resets the serial sequences.
func truncateAuthTables(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pool.Exec(ctx,
		`TRUNCATE webauthn_challenges, pending_registrations, sessions,
		          passkey_credentials, audit_log, users
		 RESTART IDENTITY CASCADE`)
	if err != nil {
		t.Fatalf("truncate auth tables: %v", err)
	}
}

// setRegistrationsEnabled writes the registrations_enabled setting and
// registers a t.Cleanup that restores whatever value (or absence) the row
// held beforehand. Without this, the setting leaks across the package
// boundary: every DB-backed package shares one physical TEST_DATABASE_URL,
// and #0132's review demonstrated the leak by measuring
// registrations_enabled before and after `go test ./internal/auth/` alone
// (#0215). t.Cleanup is used rather than a plain deferred restore because it
// must still run when the test that changed the value fails — Go runs
// cleanups on failure, skip, and panic alike, which a bare defer in the test
// body would not survive a t.Fatalf in a later helper. This matches the
// shape #0130 used to fix the sibling leak in
// internal/handlers/audit_seams_test.go.
//
// The restore preserves the row's prior updated_at along with its prior
// value (#0217) — #0215's review measured that writing back an unchanged
// value with `updated_at = now()` still moved the full-row hash on exactly
// that field, so criterion 3's "byte-identical" held only on (key, value).
// Nothing in the tree reads an absolute settings.updated_at (the sole
// consumer, internal/handlers/settings_test.go's settingUpdatedAt, compares
// relatively), but restoring the real prior timestamp is cheap and makes the
// restore byte-identical on the whole row, not just two of its three
// columns.
//
// This read-then-write is safe only because internal/testdb.Lock() (every
// package's TestMain) serialises which package's binary touches the shared
// database, and because internal/auth itself has zero t.Parallel() calls —
// two tests here never interleave, so no other write can land between this
// read and the later Exec. Adding a t.Parallel() call anywhere in this
// package would reopen that window; do not add one without revisiting this
// helper (#0217, following #0215's review notes on the same hazard).
func setRegistrationsEnabled(t *testing.T, pool *pgxpool.Pool, enabled bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var prior string
	var priorUpdatedAt time.Time
	hadRow := true
	err := pool.QueryRow(ctx,
		`SELECT value, updated_at FROM settings WHERE key = $1`, settingRegistrationsEnabled).Scan(&prior, &priorUpdatedAt)
	switch {
	case err == nil:
		// hadRow already true.
	case errors.Is(err, pgx.ErrNoRows):
		hadRow = false
	default:
		t.Fatalf("read registrations_enabled: %v", err)
	}

	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		if hadRow {
			if _, err := pool.Exec(cctx,
				`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
				settingRegistrationsEnabled, prior, priorUpdatedAt); err != nil {
				t.Errorf("restore registrations_enabled: %v", err)
			}
		} else if _, err := pool.Exec(cctx,
			`DELETE FROM settings WHERE key = $1`, settingRegistrationsEnabled); err != nil {
			t.Errorf("cleanup registrations_enabled: %v", err)
		}
	})

	value := "false"
	if enabled {
		value = "true"
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, now())
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
		settingRegistrationsEnabled, value); err != nil {
		t.Fatalf("set registrations_enabled: %v", err)
	}
}

// settingsRow snapshots a settings row's value and updated_at together, so a
// test can assert nothing on the row moved across a restore — not just the
// value.
type settingsRow struct {
	value     string
	updatedAt time.Time
	present   bool
}

func (r settingsRow) String() string {
	if !r.present {
		return "<absent>"
	}
	return fmt.Sprintf("value=%q updated_at=%s", r.value, r.updatedAt.Format(time.RFC3339Nano))
}

// readSettingsRow reads a settings row's full state (value, updated_at, and
// whether the row exists at all), for before/after comparisons.
func readSettingsRow(t *testing.T, pool *pgxpool.Pool, key string) settingsRow {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var row settingsRow
	err := pool.QueryRow(ctx,
		`SELECT value, updated_at FROM settings WHERE key = $1`, key).Scan(&row.value, &row.updatedAt)
	switch {
	case err == nil:
		row.present = true
	case errors.Is(err, pgx.ErrNoRows):
		row.present = false
	default:
		t.Fatalf("read settings row %q: %v", key, err)
	}
	return row
}

// TestSetRegistrationsEnabled_RestoreIsByteIdenticalOnTheWholeRow is #0217's
// criterion 1 and 2: setRegistrationsEnabled's t.Cleanup restores the prior
// updated_at along with the prior value, so writing back an unchanged value
// through the helper moves nothing on the row — not even the timestamp.
// #0215's review measured the pre-fix behavior directly: restoring only
// value while stamping updated_at = now() moved the full-row hash
// (555f2c3c… -> fbac46c8…) on exactly that column. This seeds a known row
// directly (bypassing the helper under test, so the baseline doesn't depend
// on it), runs the helper inside a subtest so its t.Cleanup fires before
// this test's own assertion, and compares the full row before and after.
func TestSetRegistrationsEnabled_RestoreIsByteIdenticalOnTheWholeRow(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Snapshot the package's own baseline row (ordinarily the migration
	// seed's registrations_enabled=false) BEFORE overwriting it, and restore
	// exactly that — not an unconditional DELETE — on teardown. An
	// unconditional delete would leak this test's own setup into whichever
	// test in this package runs next, the same defect class #0217 exists to
	// close.
	baseline := readSettingsRow(t, pool, settingRegistrationsEnabled)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		if baseline.present {
			if _, err := pool.Exec(cctx,
				`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
				settingRegistrationsEnabled, baseline.value, baseline.updatedAt); err != nil {
				t.Errorf("restore baseline registrations_enabled: %v", err)
			}
		} else if _, err := pool.Exec(cctx,
			`DELETE FROM settings WHERE key = $1`, settingRegistrationsEnabled); err != nil {
			t.Errorf("cleanup baseline registrations_enabled: %v", err)
		}
	})

	seededAt := time.Now().Add(-time.Hour).Round(time.Microsecond)
	if _, err := pool.Exec(ctx,
		`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
		 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
		settingRegistrationsEnabled, "false", seededAt); err != nil {
		t.Fatalf("seed registrations_enabled: %v", err)
	}

	before := readSettingsRow(t, pool, settingRegistrationsEnabled)

	// The subtest's t.Cleanup runs when the subtest returns, which is
	// before this outer test's own code resumes — so the restore has
	// already happened by the time `after` is read below.
	t.Run("write back the same value through the helper", func(t *testing.T) {
		setRegistrationsEnabled(t, pool, false) // same value as seeded above
	})

	after := readSettingsRow(t, pool, settingRegistrationsEnabled)
	if before.present != after.present || before.value != after.value || !before.updatedAt.Equal(after.updatedAt) {
		t.Errorf("row moved across a write-back of the same value: before={%s} after={%s}", before, after)
	}
}

// TestSetRegistrationsEnabled_NeverExistedRowStaysAbsent is #0217's criterion
// 3, re-proving #0215's review probes E and F against the updated_at-aware
// helper: when the row never existed before the helper's call, the
// t.Cleanup must delete it again on teardown, not resurrect it at some
// default value.
func TestSetRegistrationsEnabled_NeverExistedRowStaysAbsent(t *testing.T) {
	pool := testPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Snapshot whatever this package's own baseline row looks like (the
	// migration seed's registrations_enabled=false, ordinarily) and restore
	// it on teardown — this test's own precondition (an ABSENT row) must not
	// leak into the package's next test any more than the helper under test
	// is allowed to leak. Using readSettingsRow/setRegistrationsEnabled here
	// (rather than a bespoke restore) would register a second, redundant
	// cleanup, so this restores directly.
	baseline := readSettingsRow(t, pool, settingRegistrationsEnabled)
	t.Cleanup(func() {
		cctx, ccancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer ccancel()
		if baseline.present {
			if _, err := pool.Exec(cctx,
				`INSERT INTO settings (key, value, updated_at) VALUES ($1, $2, $3)
				 ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`,
				settingRegistrationsEnabled, baseline.value, baseline.updatedAt); err != nil {
				t.Errorf("restore baseline registrations_enabled: %v", err)
			}
		} else if _, err := pool.Exec(cctx,
			`DELETE FROM settings WHERE key = $1`, settingRegistrationsEnabled); err != nil {
			t.Errorf("cleanup baseline registrations_enabled: %v", err)
		}
	})

	if _, err := pool.Exec(ctx,
		`DELETE FROM settings WHERE key = $1`, settingRegistrationsEnabled); err != nil {
		t.Fatalf("pre-delete registrations_enabled: %v", err)
	}
	if row := readSettingsRow(t, pool, settingRegistrationsEnabled); row.present {
		t.Fatalf("row present after delete: %s", row)
	}

	t.Run("helper call on an absent row", func(t *testing.T) {
		setRegistrationsEnabled(t, pool, true)
		if row := readSettingsRow(t, pool, settingRegistrationsEnabled); !row.present || row.value != "true" {
			t.Fatalf("expected row present with value=true during subtest, got %s", row)
		}
	})

	after := readSettingsRow(t, pool, settingRegistrationsEnabled)
	if after.present {
		t.Errorf("row resurrected after cleanup (got %s), want absent — a never-existed row must be deleted, not defaulted", after)
	}
}

// newService builds a RegistrationService over the test pool with the given
// mailer and admin email.
func newService(t *testing.T, pool *pgxpool.Pool, mailer Mailer, adminEmail string) *RegistrationService {
	t.Helper()
	cfg := &config.Config{
		WebAuthnRPID:     testRPID,
		WebAuthnRPOrigin: testRPOrigin,
		AdminEmail:       adminEmail,
	}
	wa, err := NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	return NewRegistrationService(NewStore(pool), wa, mailer, nil, cfg)
}

// TestStartRegistration_DisabledReturns403 confirms the registrations_enabled
// gate is enforced (read fresh from the DB) and no pending row is created.
func TestStartRegistration_DisabledReturns403(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, false)
	mailer := &recordingMailer{}
	svc := newService(t, pool, mailer, "")

	err := svc.StartRegistration(context.Background(), "alice@example.com", "")
	if err != ErrRegistrationsDisabled {
		t.Fatalf("StartRegistration error = %v, want ErrRegistrationsDisabled", err)
	}
	if calls, _, _ := mailer.recorded(); calls != 0 {
		t.Errorf("mailer called %d times, want 0", calls)
	}
	if n := countPending(t, pool, "alice@example.com"); n != 0 {
		t.Errorf("pending_registrations rows = %d, want 0", n)
	}
}

// TestStartRegistration_EnabledCreatesPendingAndMails confirms the happy start
// path: a pending row is created and the mailer is invoked with a token.
func TestStartRegistration_EnabledCreatesPendingAndMails(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	mailer := &recordingMailer{}
	svc := newService(t, pool, mailer, "")

	if err := svc.StartRegistration(context.Background(), "Bob@Example.com", ""); err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}

	calls, to, token := mailer.recorded()
	if calls != 1 {
		t.Fatalf("mailer calls = %d, want 1", calls)
	}
	if to != "bob@example.com" {
		t.Errorf("mailer recipient = %q, want lowercased bob@example.com", to)
	}
	if token == "" {
		t.Error("mailer token is empty")
	}
	if n := countPending(t, pool, "bob@example.com"); n != 1 {
		t.Errorf("pending_registrations rows = %d, want 1", n)
	}
}

// TestStartRegistration_DuplicateEmailNoLeak confirms an already-registered
// email does not error to the caller (no account-existence leak) and does not
// create a pending row or send mail.
func TestStartRegistration_DuplicateEmailNoLeak(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	insertUser(t, pool, "carol@example.com", false)
	mailer := &recordingMailer{}
	svc := newService(t, pool, mailer, "")

	if err := svc.StartRegistration(context.Background(), "carol@example.com", ""); err != ErrEmailRegistered {
		t.Fatalf("StartRegistration error = %v, want ErrEmailRegistered", err)
	}
	if calls, _, _ := mailer.recorded(); calls != 0 {
		t.Errorf("mailer called %d times for duplicate, want 0", calls)
	}
}

// TestVerifyRegistration_UnknownToken confirms an unknown token is rejected.
func TestVerifyRegistration_UnknownToken(t *testing.T) {
	pool := testPool(t)
	svc := newService(t, pool, &recordingMailer{}, "")

	if _, err := svc.VerifyRegistration(context.Background(), "no-such-token"); err != ErrTokenInvalid {
		t.Fatalf("VerifyRegistration error = %v, want ErrTokenInvalid", err)
	}
}

// TestVerifyRegistration_ExpiredToken confirms a token past its 5-minute TTL is
// rejected.
func TestVerifyRegistration_ExpiredToken(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	svc := newService(t, pool, &recordingMailer{}, "")

	// Backdate the clock so the pending row's expiry is already in the past.
	svc.now = func() time.Time { return time.Now().Add(-10 * time.Minute) }
	if err := svc.StartRegistration(context.Background(), "dave@example.com", ""); err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}
	token := lastPendingToken(t, pool, "dave@example.com")

	svc.now = time.Now // restore to now; the row is now expired
	if _, err := svc.VerifyRegistration(context.Background(), token); err != ErrTokenInvalid {
		t.Fatalf("VerifyRegistration error = %v, want ErrTokenInvalid", err)
	}
}

// TestVerifyRegistration_OptionsShape confirms BeginRegistration produces the
// PRD-mandated options: residentKey required, userVerification required,
// authenticatorAttachment omitted, and ES256+RS256 pubKeyCredParams with a
// random 16-byte user handle.
func TestVerifyRegistration_OptionsShape(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	svc := newService(t, pool, &recordingMailer{}, "")

	if err := svc.StartRegistration(context.Background(), "erin@example.com", ""); err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}
	token := lastPendingToken(t, pool, "erin@example.com")

	creation, err := svc.VerifyRegistration(context.Background(), token)
	if err != nil {
		t.Fatalf("VerifyRegistration: %v", err)
	}

	sel := creation.Response.AuthenticatorSelection
	if sel.ResidentKey != protocol.ResidentKeyRequirementRequired {
		t.Errorf("residentKey = %q, want required", sel.ResidentKey)
	}
	if sel.UserVerification != protocol.VerificationRequired {
		t.Errorf("userVerification = %q, want required", sel.UserVerification)
	}
	if sel.AuthenticatorAttachment != "" {
		t.Errorf("authenticatorAttachment = %q, want omitted (empty)", sel.AuthenticatorAttachment)
	}

	params := creation.Response.Parameters
	if len(params) != 2 ||
		params[0].Algorithm != webauthncose.AlgES256 ||
		params[1].Algorithm != webauthncose.AlgRS256 {
		t.Errorf("pubKeyCredParams = %+v, want [ES256, RS256]", params)
	}

	handle, ok := creation.Response.User.ID.(protocol.URLEncodedBase64)
	if !ok {
		t.Fatalf("user.id type = %T, want protocol.URLEncodedBase64", creation.Response.User.ID)
	}
	if len(handle) != userHandleLen {
		t.Errorf("user.id length = %d, want %d", len(handle), userHandleLen)
	}

	// Verify the authenticatorAttachment key is genuinely absent from the JSON
	// (not just empty), since iCloud Keychain compatibility depends on it.
	raw, err := json.Marshal(creation)
	if err != nil {
		t.Fatalf("marshal creation: %v", err)
	}
	if bytes.Contains(raw, []byte("authenticatorAttachment")) {
		t.Errorf("serialized options must omit authenticatorAttachment; got: %s", raw)
	}

	// The challenge must have been persisted, linked to the token.
	if n := countChallenges(t, pool, token); n != 1 {
		t.Errorf("webauthn_challenges rows for token = %d, want 1", n)
	}
}

// TestFullCeremony_EndToEnd drives start → verify → finish against a virtual
// authenticator, exercising the real cryptographic FinishRegistration path. It
// asserts a user, credential, and session land in the DB and that the first
// user is promoted to admin.
func TestFullCeremony_EndToEnd(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	svc := newService(t, pool, &recordingMailer{}, "")

	const email = "frank@example.com"
	ctx := context.Background()

	// Step 1: start.
	if err := svc.StartRegistration(ctx, email, ""); err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}
	token := lastPendingToken(t, pool, email)

	// Step 2: verify → options.
	creation, err := svc.VerifyRegistration(ctx, token)
	if err != nil {
		t.Fatalf("VerifyRegistration: %v", err)
	}

	// Build a virtual authenticator + credential and produce a real attestation.
	rp := virtualwebauthn.RelyingParty{ID: testRPID, Name: "Open Circuit SF", Origin: testRPOrigin}
	authenticator := virtualwebauthn.NewAuthenticator()
	cred := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)

	optionsJSON, err := json.Marshal(creation)
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	attOpts, err := virtualwebauthn.ParseAttestationOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAttestationOptions: %v", err)
	}
	attestationResponse := virtualwebauthn.CreateAttestationResponse(rp, authenticator, cred, *attOpts)

	// Step 3: finish. The attestation JSON is the request body.
	req := httptest.NewRequest(http.MethodPost,
		"/auth/register/finish?token="+token+"&device_name=Test+Key",
		bytes.NewReader([]byte(attestationResponse)))
	result, err := svc.FinishRegistration(ctx, token, "Test Key", "", req)
	if err != nil {
		t.Fatalf("FinishRegistration: %v", err)
	}

	if result.User.Email != email {
		t.Errorf("user email = %q, want %q", result.User.Email, email)
	}
	if !result.User.IsAdmin {
		t.Error("first user should be promoted to admin")
	}
	if result.SessionToken == "" {
		t.Error("session token is empty")
	}

	// Verify rows landed and ephemeral rows were cleaned up.
	if n := countCredentials(t, pool, result.User.ID); n != 1 {
		t.Errorf("passkey_credentials rows = %d, want 1", n)
	}
	if n := countSessions(t, pool, result.SessionToken); n != 1 {
		t.Errorf("sessions rows for token = %d, want 1", n)
	}
	if n := countPending(t, pool, email); n != 0 {
		t.Errorf("pending_registrations rows after finish = %d, want 0", n)
	}
	if n := countChallenges(t, pool, token); n != 0 {
		t.Errorf("webauthn_challenges rows after finish = %d, want 0", n)
	}

	// The stored credential id must match the authenticator's credential.
	storedID := credentialID(t, pool, result.User.ID)
	if !bytes.Equal(storedID, cred.ID) {
		t.Errorf("stored credential_id = %x, want %x", storedID, cred.ID)
	}

	// device_name was persisted.
	if got := credentialDeviceName(t, pool, result.User.ID); got != "Test Key" {
		t.Errorf("credential device_name = %q, want %q", got, "Test Key")
	}
}

// TestFullCeremony_AdminEmailPromotion confirms a non-first user whose email
// matches ADMIN_EMAIL is promoted to admin, while another non-matching user is
// not.
func TestFullCeremony_AdminEmailPromotion(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)

	// Pre-seed an existing user so the next registrant is NOT the first user.
	insertUser(t, pool, "existing@example.com", true)

	adminEmail := "boss@example.com"
	svc := newService(t, pool, &recordingMailer{}, adminEmail)

	// A registrant matching ADMIN_EMAIL is promoted even though not first.
	adminUser := runCeremony(t, svc, pool, adminEmail)
	if !adminUser.IsAdmin {
		t.Errorf("ADMIN_EMAIL registrant should be admin")
	}

	// A registrant not matching ADMIN_EMAIL and not first is a normal user.
	normalUser := runCeremony(t, svc, pool, "regular@example.com")
	if normalUser.IsAdmin {
		t.Errorf("non-admin, non-first registrant should not be admin")
	}
}

// runCeremony drives a full start→verify→finish ceremony for email and returns
// the created user. Used by promotion tests.
func runCeremony(t *testing.T, svc *RegistrationService, pool *pgxpool.Pool, email string) CreatedUser {
	t.Helper()
	ctx := context.Background()
	if err := svc.StartRegistration(ctx, email, ""); err != nil {
		t.Fatalf("StartRegistration(%s): %v", email, err)
	}
	token := lastPendingToken(t, pool, email)
	creation, err := svc.VerifyRegistration(ctx, token)
	if err != nil {
		t.Fatalf("VerifyRegistration(%s): %v", email, err)
	}

	rp := virtualwebauthn.RelyingParty{ID: testRPID, Name: "Open Circuit SF", Origin: testRPOrigin}
	authenticator := virtualwebauthn.NewAuthenticator()
	cred := virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2)
	optionsJSON, err := json.Marshal(creation)
	if err != nil {
		t.Fatalf("marshal options: %v", err)
	}
	attOpts, err := virtualwebauthn.ParseAttestationOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAttestationOptions: %v", err)
	}
	attestationResponse := virtualwebauthn.CreateAttestationResponse(rp, authenticator, cred, *attOpts)

	req := httptest.NewRequest(http.MethodPost, "/auth/register/finish?token="+token,
		bytes.NewReader([]byte(attestationResponse)))
	result, err := svc.FinishRegistration(ctx, token, "", "", req)
	if err != nil {
		t.Fatalf("FinishRegistration(%s): %v", email, err)
	}
	return result.User
}

// --- small query helpers used by the tests ---

func insertUser(t *testing.T, pool *pgxpool.Pool, email string, admin bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (email, is_admin, active, created_at) VALUES ($1, $2, TRUE, now())`,
		email, admin); err != nil {
		t.Fatalf("insert user %s: %v", email, err)
	}
}

func countPending(t *testing.T, pool *pgxpool.Pool, email string) int {
	return scanCount(t, pool, `SELECT COUNT(*) FROM pending_registrations WHERE email = $1`, email)
}

func countChallenges(t *testing.T, pool *pgxpool.Pool, token string) int {
	return scanCount(t, pool, `SELECT COUNT(*) FROM webauthn_challenges WHERE pending_registration_token = $1`, token)
}

func countCredentials(t *testing.T, pool *pgxpool.Pool, userID int64) int {
	return scanCount(t, pool, `SELECT COUNT(*) FROM passkey_credentials WHERE user_id = $1`, userID)
}

func countSessions(t *testing.T, pool *pgxpool.Pool, token string) int {
	return scanCount(t, pool, `SELECT COUNT(*) FROM sessions WHERE token = $1`, token)
}

func scanCount(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var n int
	if err := pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", sql, err)
	}
	return n
}

func lastPendingToken(t *testing.T, pool *pgxpool.Pool, email string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var token string
	if err := pool.QueryRow(ctx,
		`SELECT token FROM pending_registrations WHERE email = $1 ORDER BY id DESC LIMIT 1`,
		email).Scan(&token); err != nil {
		t.Fatalf("fetch pending token for %s: %v", email, err)
	}
	return token
}

func credentialID(t *testing.T, pool *pgxpool.Pool, userID int64) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var id []byte
	if err := pool.QueryRow(ctx,
		`SELECT credential_id FROM passkey_credentials WHERE user_id = $1`, userID).Scan(&id); err != nil {
		t.Fatalf("fetch credential_id: %v", err)
	}
	return id
}

func credentialDeviceName(t *testing.T, pool *pgxpool.Pool, userID int64) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var name string
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(device_name, '') FROM passkey_credentials WHERE user_id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("fetch device_name: %v", err)
	}
	return name
}

// TestRegistrationOptions_UserVerificationAndResidentKeyRequired pins the
// registration ceremony's authenticator selection. AuthenticatorSelection is
// set on the RP-level webauthn.Config so the login ceremony doesn't default
// to "preferred"; registration overrides that config wholesale via
// registrationOptions(), so this guards that enrollment still demands a
// discoverable credential AND user verification, independent of whatever the
// RP-level default happens to be.
func TestRegistrationOptions_UserVerificationAndResidentKeyRequired(t *testing.T) {
	cfg := &config.Config{WebAuthnRPID: testRPID, WebAuthnRPOrigin: testRPOrigin}
	wa, err := NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}
	user, err := NewRegistrationUser("options@example.com")
	if err != nil {
		t.Fatalf("NewRegistrationUser: %v", err)
	}

	creation, _, err := wa.BeginRegistration(user, registrationOptions()...)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}

	sel := creation.Response.AuthenticatorSelection
	if sel.UserVerification != protocol.VerificationRequired {
		t.Errorf("UserVerification = %q, want %q", sel.UserVerification, protocol.VerificationRequired)
	}
	if sel.ResidentKey != protocol.ResidentKeyRequirementRequired {
		t.Errorf("ResidentKey = %q, want %q", sel.ResidentKey, protocol.ResidentKeyRequirementRequired)
	}
}
