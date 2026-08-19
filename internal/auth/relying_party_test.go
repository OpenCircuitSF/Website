package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/descope/virtualwebauthn"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
)

// This file exists because a reviewer mutation-tested the rest of the auth
// suite and proved it does not actually verify the relying party at all:
// testRPID / testRPOrigin (registration_test.go) feed BOTH the server's
// WebAuthn config AND the virtualwebauthn.RelyingParty the test authenticator
// signs with. The two sides are self-consistent by construction and never
// cross-check against anything external, so setting both to the same wrong
// domain — or swapping which one holds the apex vs. the www host — leaves
// every other test in this package green.
//
// The tests below use their OWN literal apex/www strings (prodRPID /
// prodRPOrigin), independent of testRPID / testRPOrigin, so a transposition
// of either constant pair is caught here even if it silently passes
// elsewhere. See CLAUDE.md §7 for why RP_ID is the apex and RP_ORIGIN is the
// www host: "one passkey covers apex and www" — but the two must never be
// interchanged, since RP_ORIGIN must equal the browser's real origin.

// prodRPID / prodRPOrigin are the actual production relying-party values
// (CLAUDE.md §7), hardcoded independently of the shared testRPID /
// testRPOrigin constants used elsewhere in this package. If a future edit
// transposes testRPID and testRPOrigin (or sets them to some other
// self-consistent-but-wrong pair), these literals give the suite an external
// oracle that does not move in sympathy.
const (
	prodRPID     = "opencircuitsf.com"
	prodRPOrigin = "https://www.opencircuitsf.com"
)

// newServiceForRP builds a RegistrationService and LoginService sharing one
// *webauthn.WebAuthn constructed from the given RP ID / origin, letting a
// test choose values other than testRPID / testRPOrigin.
func newServiceForRP(t *testing.T, pool *pgxpool.Pool, mailer Mailer, rpID, rpOrigin string) (*RegistrationService, *LoginService, *webauthn.WebAuthn) {
	t.Helper()
	cfg := &config.Config{WebAuthnRPID: rpID, WebAuthnRPOrigin: rpOrigin}
	wa, err := NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn(RPID=%q, RPOrigin=%q): %v", rpID, rpOrigin, err)
	}
	regSvc := NewRegistrationService(NewStore(pool), wa, mailer, nil, cfg)
	loginSvc := NewLoginService(NewStore(pool), wa, mailer, nil, nil)
	return regSvc, loginSvc, wa
}

// TestNewWebAuthn_ConfiguredRPPinsProductionApexAndWWW asserts that the
// WebAuthn instance NewWebAuthn constructs actually carries the production
// apex (RPID) and www origin (RPOrigins) — read from a config built with
// literal, independently-declared strings, not from testRPID / testRPOrigin.
// This is the direct fix for the mutation-testing finding: it fails if
// NewWebAuthn's field mapping is ever swapped (e.g. RPID: cfg.WebAuthnRPOrigin)
// and it fails if the two literal constants above are transposed, since
// prodRPID then carries a scheme and NewWebAuthn's config validation rejects
// it outright (go-webauthn's ValidateRPID forbids a scheme component).
//
// It also carries the suffix assertion that pins the apex/www relationship
// itself (the fix for the second review bounce): go-webauthn and
// virtualwebauthn both accept any RPID/origin pair, so nothing upstream of
// this test would ever fail if RPID and the origin host were entirely
// unrelated strings. This assertion reads wa.Config.RPOrigins[0]'s host
// directly off the constructed instance and requires it to equal RPID or be
// a subdomain of it — the one property that actually encodes "one passkey
// covers apex and www" (CLAUDE.md §7). It fails, for example, if
// prodRPOrigin were "https://www.completely-different.test": the config
// would still be well-formed and NewWebAuthn would still succeed, but the
// origin host shares no suffix relationship with the RP ID.
func TestNewWebAuthn_ConfiguredRPPinsProductionApexAndWWW(t *testing.T) {
	cfg := &config.Config{WebAuthnRPID: prodRPID, WebAuthnRPOrigin: prodRPOrigin}
	wa, err := NewWebAuthn(cfg)
	if err != nil {
		t.Fatalf("NewWebAuthn: %v", err)
	}

	if wa.Config.RPID != "opencircuitsf.com" {
		t.Errorf("RPID = %q, want %q", wa.Config.RPID, "opencircuitsf.com")
	}
	if len(wa.Config.RPOrigins) != 1 || wa.Config.RPOrigins[0] != "https://www.opencircuitsf.com" {
		t.Errorf("RPOrigins = %v, want [%q]", wa.Config.RPOrigins, "https://www.opencircuitsf.com")
	}

	// The apex must not literally equal the origin string: that gap is the
	// entire reason RP_ID is set to the apex rather than to a specific host.
	if wa.Config.RPID == wa.Config.RPOrigins[0] {
		t.Fatalf("RPID must not equal the full RPOrigins[0] string; got both = %q", wa.Config.RPID)
	}

	// The suffix assertion itself: the configured origin's host must equal
	// the RP ID or be a subdomain of it. Neither go-webauthn nor
	// virtualwebauthn enforces this — the ceremony would succeed for any
	// well-formed RPID/origin pair regardless — so this is the only check in
	// the suite that actually pins the registrable-domain relationship
	// rather than merely observing that the ceremony didn't reject it.
	u, err := url.Parse(wa.Config.RPOrigins[0])
	if err != nil {
		t.Fatalf("parse RPOrigins[0] %q: %v", wa.Config.RPOrigins[0], err)
	}
	host := u.Hostname()
	if host != wa.Config.RPID && !strings.HasSuffix(host, "."+wa.Config.RPID) {
		t.Errorf("origin host %q is not the RP ID %q nor a subdomain of it; "+
			"a passkey scoped to the RP ID would not be usable on this origin", host, wa.Config.RPID)
	}
}

// TestFinishRegistration_RejectsOriginOutsideConfiguredScope drives a real
// registration ceremony whose attestation claims an origin
// ("https://evil.example") that is not in the server's configured RPOrigins
// (only prodRPOrigin, the www host). FinishRegistration must reject it, and
// no user/credential/session rows may land.
func TestFinishRegistration_RejectsOriginOutsideConfiguredScope(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	mailer := &recordingMailer{}
	regSvc, _, _ := newServiceForRP(t, pool, mailer, prodRPID, prodRPOrigin)

	const email = "mallory-reg@example.com"
	ctx := context.Background()
	if err := regSvc.StartRegistration(ctx, email, ""); err != nil {
		t.Fatalf("StartRegistration: %v", err)
	}
	token := lastPendingToken(t, pool, email)
	creation, err := regSvc.VerifyRegistration(ctx, token)
	if err != nil {
		t.Fatalf("VerifyRegistration: %v", err)
	}

	// Sign the attestation as if it came from an entirely different origin.
	// The RP ID hash still matches (same prodRPID), isolating the assertion
	// to the origin check specifically.
	rogueRP := virtualwebauthn.RelyingParty{ID: prodRPID, Name: "Open Circuit SF", Origin: "https://evil.example"}
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
	attestationResponse := virtualwebauthn.CreateAttestationResponse(rogueRP, authenticator, cred, *attOpts)

	req := httptest.NewRequest(http.MethodPost, "/auth/register/finish?token="+token,
		bytes.NewReader([]byte(attestationResponse)))
	if _, err := regSvc.FinishRegistration(ctx, token, "", "", req); err == nil {
		t.Fatal("FinishRegistration succeeded for an origin outside RPOrigins, want error")
	}

	if n := countCredentialsByEmail(t, pool, email); n != 0 {
		t.Errorf("passkey_credentials rows after rejected origin = %d, want 0", n)
	}
}

// TestFinishLogin_RejectsOriginOutsideConfiguredScope registers a real
// credential under prodRPID/prodRPOrigin, then attempts to authenticate with
// that same credential using an assertion signed for
// "https://evil.example". FinishLogin must reject it.
func TestFinishLogin_RejectsOriginOutsideConfiguredScope(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	mailer := &recordingMailer{}
	regSvc, loginSvc, _ := newServiceForRP(t, pool, mailer, prodRPID, prodRPOrigin)

	acct := registerWithAuthenticatorRP(t, regSvc, pool, "mallory-login@example.com", prodRPID, prodRPOrigin)

	ctx := context.Background()
	assertion, err := loginSvc.StartLogin(ctx, "")
	if err != nil {
		t.Fatalf("StartLogin: %v", err)
	}
	optionsJSON, err := json.Marshal(assertion)
	if err != nil {
		t.Fatalf("marshal assertion options: %v", err)
	}
	assertOpts, err := virtualwebauthn.ParseAssertionOptions(string(optionsJSON))
	if err != nil {
		t.Fatalf("ParseAssertionOptions: %v", err)
	}

	rogueRP := virtualwebauthn.RelyingParty{ID: acct.rp.ID, Name: acct.rp.Name, Origin: "https://evil.example"}
	assertionResponse := virtualwebauthn.CreateAssertionResponse(rogueRP, acct.authenticator, acct.cred, *assertOpts)

	req := httptest.NewRequest(http.MethodPost, "/auth/login/finish", bytes.NewReader([]byte(assertionResponse)))
	if _, err := loginSvc.FinishLogin(ctx, "", req); err == nil {
		t.Fatal("FinishLogin succeeded for an origin outside RPOrigins, want error")
	}
}

// TestCeremonySucceedsWhenRPIDDiffersFromOriginHost drives a full
// registration + login ceremony end to end with RP ID set to the bare apex
// (prodRPID, "opencircuitsf.com") and origin set to the www host
// (prodRPOrigin, "https://www.opencircuitsf.com") — the pair a real browser
// on www.opencircuitsf.com would present.
//
// What this test proves — and, after the second review bounce, what it does
// NOT prove: it demonstrates that a ceremony succeeds end-to-end when RPID
// and the origin host are not byte-identical. It does NOT, on its own, pin
// the apex/www *relationship* — neither go-webauthn nor virtualwebauthn
// enforces "origin host must be RPID or a subdomain of it", so this exact
// test body would pass unchanged if prodRPID/prodRPOrigin were replaced with
// any other pair of unrelated, individually well-formed strings (e.g.
// RPID="totally-unrelated.invalid", origin="https://nonsense.example.test").
// That gap is exactly what the suffix assertion in
// TestNewWebAuthn_ConfiguredRPPinsProductionApexAndWWW closes; this test is
// still worth keeping because it proves the ceremony machinery itself (not
// just config construction) tolerates RPID and origin host being different
// strings, which the apex/www split fundamentally requires.
//
// This test uses prodRPID/prodRPOrigin, not testRPID/testRPOrigin, so it does
// not move if the shared test constants are ever edited or transposed.
func TestCeremonySucceedsWhenRPIDDiffersFromOriginHost(t *testing.T) {
	pool := testPool(t)
	setRegistrationsEnabled(t, pool, true)
	mailer := &recordingMailer{}
	regSvc, loginSvc, wa := newServiceForRP(t, pool, mailer, prodRPID, prodRPOrigin)

	if wa.Config.RPID == prodRPOrigin {
		t.Fatalf("test setup: RPID must not equal the origin string")
	}

	acct := registerWithAuthenticatorRP(t, regSvc, pool, "apex-www@example.com", prodRPID, prodRPOrigin)

	result, err := driveLoginRP(t, loginSvc, acct, "")
	if err != nil {
		t.Fatalf("FinishLogin under apex RPID / www origin: %v", err)
	}
	if result.UserID != acct.user.ID {
		t.Errorf("login UserID = %d, want %d", result.UserID, acct.user.ID)
	}
	if result.SessionToken == "" {
		t.Error("session token is empty")
	}
}

// registerWithAuthenticatorRP is registerWithAuthenticator (login_test.go)
// generalized to take an explicit RP ID / origin instead of hardcoding
// testRPID / testRPOrigin, so the apex/www pinning tests above can exercise
// prodRPID / prodRPOrigin specifically.
func registerWithAuthenticatorRP(t *testing.T, svc *RegistrationService, pool *pgxpool.Pool, email, rpID, rpOrigin string) registeredAccount {
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

	rp := virtualwebauthn.RelyingParty{ID: rpID, Name: "Open Circuit SF", Origin: rpOrigin}
	handle, ok := creation.Response.User.ID.(protocol.URLEncodedBase64)
	if !ok {
		t.Fatalf("user.id type = %T, want protocol.URLEncodedBase64", creation.Response.User.ID)
	}
	authenticator := virtualwebauthn.NewAuthenticatorWithOptions(virtualwebauthn.AuthenticatorOptions{
		UserHandle: []byte(handle),
	})
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
	result, err := svc.FinishRegistration(ctx, token, "Apex WWW Key", "", req)
	if err != nil {
		t.Fatalf("FinishRegistration(%s): %v", email, err)
	}

	authenticator.AddCredential(cred)
	return registeredAccount{user: result.User, rp: rp, authenticator: authenticator, cred: cred}
}

// driveLoginRP is driveLogin (login_test.go) reused verbatim; kept as a
// distinctly-named wrapper here purely for readability at the call sites
// above (it signs using acct.rp, which the apex/www tests set explicitly).
func driveLoginRP(t *testing.T, loginSvc *LoginService, acct registeredAccount, email string) (LoginResult, error) {
	t.Helper()
	return driveLogin(t, loginSvc, acct, email)
}

// countCredentialsByEmail counts passkey_credentials rows for the user with
// the given email, or 0 if no such user exists (a rejected registration never
// creates one).
func countCredentialsByEmail(t *testing.T, pool *pgxpool.Pool, email string) int {
	t.Helper()
	return scanCount(t, pool,
		`SELECT COUNT(*) FROM passkey_credentials pc
		 JOIN users u ON u.id = pc.user_id
		 WHERE u.email = $1`, email)
}
