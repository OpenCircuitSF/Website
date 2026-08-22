package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/brennanMKE/OpenCircuitSF/internal/audit"
	"github.com/brennanMKE/OpenCircuitSF/internal/auth"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
	"github.com/brennanMKE/OpenCircuitSF/internal/middleware"
	"github.com/brennanMKE/OpenCircuitSF/internal/subscribers"
)

// recordingCampaignMailer is a fake mailing.Mailer that records every
// Message handed to Send, for asserting on To/Headers/bodies without a
// network call. err, when set, is returned instead of recording anything —
// for TestAdminCampaignPreview_Test_MailerSendFails.
type recordingCampaignMailer struct {
	mu   sync.Mutex
	sent []mailing.Message
	err  error
}

func (m *recordingCampaignMailer) Send(_ context.Context, msg mailing.Message) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	m.sent = append(m.sent, msg)
	return fmt.Sprintf("test-message-id-%d", len(m.sent)), nil
}

func (m *recordingCampaignMailer) messages() []mailing.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]mailing.Message, len(m.sent))
	copy(out, m.sent)
	return out
}

// fakeUnsafeCampaignRenderer is a fake mailing.CampaignRenderer whose output
// deliberately contains a remote-asset tag (an <img> pointed at a
// third-party host) — the exact shape of output the real renderer
// (#0042/#0043's goldmark configuration) can never produce, since it
// disables image rendering entirely. Used only by
// TestContainsRemoteAsset_DetectsInjectedImage's sibling test to prove
// containsRemoteAsset (below) actually detects a violation rather than
// being a tautological always-pass check.
type fakeUnsafeCampaignRenderer struct{}

func (fakeUnsafeCampaignRenderer) Campaign(mailing.CampaignRenderInput) (string, string, error) {
	return `<html><body><p>hi</p><img src="https://evil.example/track.gif"></body></html>`,
		"hi", nil
}

// containsRemoteAsset reports whether html contains a tag or CSS construct
// that would make a mail client fetch a third-party resource — an <img>,
// <script>, <link>, or <iframe> tag, or a CSS @import/url(http...). This is
// this test file's OWN independent check of the actual HTTP response
// Preview/Test produce — not a re-assertion of #0042/#0043's internal
// goldmark contract, which has its own tests in internal/mailing.
func containsRemoteAsset(html string) bool {
	lower := strings.ToLower(html)
	for _, needle := range []string{"<img", "<script", "<link", "<iframe", "@import", "url(http"} {
		if strings.Contains(lower, needle) {
			return true
		}
	}
	return false
}

// TestContainsRemoteAsset_DetectsInjectedImage proves containsRemoteAsset
// (used by every "no remote assets" assertion below) is a real check, not a
// tautological pass — it must actually flag the fake unsafe renderer's
// output.
func TestContainsRemoteAsset_DetectsInjectedImage(t *testing.T) {
	unsafe, _, _ := fakeUnsafeCampaignRenderer{}.Campaign(mailing.CampaignRenderInput{})
	if !containsRemoteAsset(unsafe) {
		t.Fatal("containsRemoteAsset did not detect an injected <img> tag — this guard is not testing anything")
	}
	if containsRemoteAsset("<html><body><p>hi <a href=\"https://example.com\">link</a></p></body></html>") {
		t.Fatal("containsRemoteAsset flagged an ordinary <a href> link — that is legitimate campaign content, not a remote asset")
	}
}

// adminCampaignPreviewMux wires the real admin campaign preview/test routes
// guarded by RequireSession then RequireAdmin, backed by real stores —
// mirrors adminCampaignsMux (admin_campaigns_test.go). mailer may be nil
// (TestAdminCampaignPreview_Test_NilMailer_Returns503). render, when nil,
// defaults to the real mailing.MarkdownCampaignRenderer{}.
func adminCampaignPreviewMux(pool *pgxpool.Pool, mailer mailing.Mailer, render mailing.CampaignRenderer) http.Handler {
	authStore := auth.NewStore(pool)
	campaignsStore := mailing.NewCampaignStore(pool)
	subsStore := subscribers.NewStore(pool)
	if render == nil {
		render = mailing.MarkdownCampaignRenderer{}
	}
	h := NewAdminCampaignPreviewHandler(
		campaignsStore, subsStore, fakePhysicalAddress{value: "123 Main St, Springfield"}, render, mailer,
		audit.New(pool), "https://www.example.com", "lists.example.com", "hello@example.com",
	)
	requireSession := middleware.RequireSession(authStore)
	requireAdmin := func(next http.Handler) http.Handler {
		return requireSession(middleware.RequireAdmin(next))
	}
	mux := http.NewServeMux()
	mux.Handle("POST /admin/campaigns/{id}/preview", requireAdmin(http.HandlerFunc(h.Preview)))
	mux.Handle("POST /admin/campaigns/{id}/test", requireAdmin(http.HandlerFunc(h.Test)))
	return mux
}

// cleanupTestRecipientRows deletes every dedicated test-recipient row this
// file's Test calls create (see ensureTestRecipient's own doc comment on
// the campaign-test+admin-<id>@... address shape) — a t.Cleanup, not a
// truncate, since adminSubscribersTestPool's table set is shared across
// this package's other tests.
func cleanupTestRecipientRows(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE email LIKE 'campaign-test+admin-%'`)
	})
}

func seedPreviewCampaign(t *testing.T, pool *pgxpool.Pool, subject, bodyMD string) mailing.Campaign {
	t.Helper()
	store := mailing.NewCampaignStore(pool)
	c, err := store.Create(context.Background(), mailing.CampaignInput{
		Name: uniqueAdminCampaignName(t), Subject: subject, BodyMD: bodyMD, AudienceMode: mailing.AudienceAll,
	})
	if err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
	cleanupAdminCampaign(t, pool, c.ID)
	return c
}

// ── Preview ──────────────────────────────────────────────────────────────

func TestAdminCampaignPreview_Preview_RendersStoredRow(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, nil, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-preview-render@example.com")
	seedSession(t, pool, admin, "admin-token-preview-render")

	c := seedPreviewCampaign(t, pool, "Hello subject", "Hello **world**, visit https://opencircuitsf.com.")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/preview", srv.URL, c.ID), "admin-token-preview-render", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subject != "Hello subject" {
		t.Errorf("Subject = %q, want %q", out.Subject, "Hello subject")
	}
	if !strings.Contains(out.HTML, "world") {
		t.Errorf("HTML = %q, want it to contain the rendered body", out.HTML)
	}
	if !strings.Contains(out.Text, "world") {
		t.Errorf("Text = %q, want it to contain the rendered body", out.Text)
	}
	// Both parts required by #0046's carried-in criterion from #0047's plan.
	if out.HTML == "" || out.Text == "" {
		t.Fatalf("HTML and Text must both be non-empty, got HTML=%q Text=%q", out.HTML, out.Text)
	}
	if containsRemoteAsset(out.HTML) {
		t.Errorf("Preview HTML contains a remote-asset tag: %s", out.HTML)
	}
	// The sentinel token, never a real one — Preview's links are
	// deliberately non-functional (see previewManageToken's doc comment).
	if !strings.Contains(out.HTML, previewManageToken) {
		t.Errorf("Preview HTML footer does not carry the sentinel manage token; want it to contain %q", previewManageToken)
	}
}

// TestAdminCampaignPreview_Preview_RawHTMLInBodyNeverPassesThrough proves
// Preview always renders through mailing.CampaignRenderer (goldmark's safe
// mode, #0042) rather than ever reflecting campaign.BodyMD back
// unprocessed. A campaign body containing a literal <img> tag (raw HTML
// embedded in the Markdown source) must never surface as a live <img> in
// Preview's HTML — goldmark's safe mode neutralizes it into an HTML
// comment.
func TestAdminCampaignPreview_Preview_RawHTMLInBodyNeverPassesThrough(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, nil, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-preview-rawhtml@example.com")
	seedSession(t, pool, admin, "admin-token-preview-rawhtml")

	c := seedPreviewCampaign(t, pool, "Raw HTML subject", `Some text.<img src="https://evil.example/track.gif">More text.`)

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/preview", srv.URL, c.ID), "admin-token-preview-rawhtml", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		HTML string `json:"html"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if containsRemoteAsset(out.HTML) {
		t.Fatalf("Preview HTML contains a remote-asset tag from raw HTML embedded in body_md — the renderer's safe mode was bypassed: %s", out.HTML)
	}
}

func TestAdminCampaignPreview_Preview_CampaignNotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, nil, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-preview-404@example.com")
	seedSession(t, pool, admin, "admin-token-preview-404")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/preview", srv.URL, int64(999999999)), "admin-token-preview-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
}

// TestAdminCampaignPreview_Preview_IgnoresRequestBody proves the "no
// unsaved-body override" carried-in constraint from #0047's plan: Preview
// renders the campaign's STORED subject/body_md regardless of what the
// request body contains. A JSON body carrying a different subject/body_md
// has zero effect on the response.
func TestAdminCampaignPreview_Preview_IgnoresRequestBody(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, nil, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-preview-nooverride@example.com")
	seedSession(t, pool, admin, "admin-token-preview-nooverride")

	c := seedPreviewCampaign(t, pool, "Stored subject", "Stored body content")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/preview", srv.URL, c.ID),
		"admin-token-preview-nooverride",
		`{"subject":"ATTACKER SUBJECT","body_md":"<script>alert(1)</script> attacker body"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	var out struct {
		Subject string `json:"subject"`
		HTML    string `json:"html"`
	}
	if err := json.Unmarshal(readBody(t, resp), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Subject != "Stored subject" {
		t.Errorf("Subject = %q, want the STORED subject %q (request body must be ignored)", out.Subject, "Stored subject")
	}
	if strings.Contains(out.HTML, "ATTACKER") || strings.Contains(out.HTML, "attacker body") {
		t.Errorf("Preview HTML reflects the request body's override, want only the stored row: %s", out.HTML)
	}
}

// ── Test send ────────────────────────────────────────────────────────────

func TestAdminCampaignPreview_Test_SendsToRequestingAdminOnly(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	adminEmail := "admin-test-recipient@example.com"
	admin := seedAdmin(t, pool, adminEmail)
	seedSession(t, pool, admin, "admin-token-test-recipient")

	c := seedPreviewCampaign(t, pool, "Test subject", "Test body")

	// A body naming an attacker-chosen destination — Test must never read
	// it. There is no "to" field in this handler's request shape at all;
	// this proves sending one has zero effect, not merely that the field
	// happens to be unused today.
	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID),
		"admin-token-test-recipient", `{"to":"attacker@evil.example","address":"attacker@evil.example"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sent := mailer.messages()
	if len(sent) != 1 {
		t.Fatalf("mailer recorded %d messages, want 1", len(sent))
	}
	if sent[0].To != adminEmail {
		t.Errorf("Message.To = %q, want the requesting admin's own address %q (never an attacker-supplied one)", sent[0].To, adminEmail)
	}
}

// TestAdminCampaignPreview_Test_IgnoresRequestBody is Test's counterpart to
// TestAdminCampaignPreview_Preview_IgnoresRequestBody — #0046's review
// finding E, a genuine coverage gap: TestAdminCampaignPreview_Test_
// SendsToRequestingAdminOnly already sends a body claiming an attacker
// recipient, but only asserts on Message.To, never on whether a body-
// supplied subject/body_md leaked into the rendered content. This proves
// the STORED row, not the request body, is what a test send actually
// mails — the same "no unsaved-body override" structural guarantee as
// Preview (neither handler ever calls decodeJSON).
func TestAdminCampaignPreview_Test_IgnoresRequestBody(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-nooverride@example.com")
	seedSession(t, pool, admin, "admin-token-test-nooverride")

	c := seedPreviewCampaign(t, pool, "Stored test subject", "Stored test body content")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID),
		"admin-token-test-nooverride",
		`{"subject":"ATTACKER SUBJECT","body_md":"<script>alert(1)</script> attacker body"}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sent := mailer.messages()
	if len(sent) != 1 {
		t.Fatalf("mailer recorded %d messages, want 1", len(sent))
	}
	if sent[0].Subject != "Stored test subject" {
		t.Errorf("Subject = %q, want the STORED subject %q (request body must be ignored)", sent[0].Subject, "Stored test subject")
	}
	if strings.Contains(sent[0].HTMLBody, "ATTACKER") || strings.Contains(sent[0].HTMLBody, "attacker body") {
		t.Errorf("test message HTML reflects the request body's override, want only the stored row: %s", sent[0].HTMLBody)
	}
}

// TestAdminCampaignPreview_Test_SyntheticRecipientInvisibleOnAdminScreens is
// #0046's review finding A, closed: the dedicated synthetic test-recipient
// row ensureTestRecipient creates must never appear on the #0032 admin
// subscribers screen — neither in StatusCounts' counts-by-status header nor
// in List's table/pagination total (internal/subscribers/store.go), even
// though (proven elsewhere in this file and untouched by this test) it
// never reaches a real audience. Fails without migration 000019's synthetic
// column and its exclusion in StatusCounts/List.
func TestAdminCampaignPreview_Test_SyntheticRecipientInvisibleOnAdminScreens(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-invisible@example.com")
	seedSession(t, pool, admin, "admin-token-test-invisible")

	c := seedPreviewCampaign(t, pool, "Invisible subject", "Invisible body")

	subsStore := subscribers.NewStore(pool)
	before, err := subsStore.StatusCounts(context.Background())
	if err != nil {
		t.Fatalf("StatusCounts before: %v", err)
	}
	_, totalBefore, err := subsStore.List(context.Background(), subscribers.ListFilter{})
	if err != nil {
		t.Fatalf("List before: %v", err)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-invisible", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	// The synthetic row genuinely exists and is marked as such — this is
	// not a test that nothing happened.
	testRecipient, err := subsStore.FindByEmail(context.Background(), testRecipientEmail(admin))
	if err != nil {
		t.Fatalf("FindByEmail dedicated test recipient: %v", err)
	}
	if !testRecipient.Synthetic {
		t.Error("dedicated test-recipient row is not marked synthetic")
	}

	after, err := subsStore.StatusCounts(context.Background())
	if err != nil {
		t.Fatalf("StatusCounts after: %v", err)
	}
	for status, n := range after {
		if n != before[status] {
			t.Errorf("StatusCounts[%q] = %d, want unchanged %d — the synthetic row must not inflate the admin screen's counts", status, n, before[status])
		}
	}

	_, totalAfter, err := subsStore.List(context.Background(), subscribers.ListFilter{})
	if err != nil {
		t.Fatalf("List after: %v", err)
	}
	if totalAfter != totalBefore {
		t.Errorf("List total = %d, want unchanged %d — the synthetic row must not appear in the admin subscriber table", totalAfter, totalBefore)
	}

	// Direct proof the row itself is unreachable through List, independent
	// of any total-count race against concurrently running test packages:
	// search for the exact address and confirm nothing comes back.
	rows, matchTotal, err := subsStore.List(context.Background(), subscribers.ListFilter{Query: testRecipientEmail(admin)})
	if err != nil {
		t.Fatalf("List (filtered) after: %v", err)
	}
	if matchTotal != 0 || len(rows) != 0 {
		t.Errorf("List filtered to the synthetic recipient's own address returned %d row(s), want 0", matchTotal)
	}
}

func TestAdminCampaignPreview_Test_MarksTestSentAndAudits(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-marks@example.com")
	seedSession(t, pool, admin, "admin-token-test-marks")

	c := seedPreviewCampaign(t, pool, "Marks subject", "Marks body")

	store := mailing.NewCampaignStore(pool)
	before, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID before: %v", err)
	}
	if before.TestSentAt != nil {
		t.Fatalf("TestSentAt = %v before any test send, want nil", before.TestSentAt)
	}

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-marks", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	updated := decodeCampaign(t, readBody(t, resp))
	if updated.TestSentAt == nil {
		t.Fatal("response test_sent_at is nil, want set after a successful test send")
	}

	after, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID after: %v", err)
	}
	if after.TestSentAt == nil {
		t.Fatal("stored TestSentAt is nil after a successful test send")
	}

	actions := auditActionsForCampaign(t, pool, c.ID)
	if len(actions) != 1 || actions[0] != audit.ActionEmailCampaignTestSent {
		t.Errorf("audit actions = %v, want [%s]", actions, audit.ActionEmailCampaignTestSent)
	}
}

func TestAdminCampaignPreview_Test_CarriesRFC8058Headers(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-headers@example.com")
	seedSession(t, pool, admin, "admin-token-test-headers")

	c := seedPreviewCampaign(t, pool, "Headers subject", "Headers body")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-headers", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sent := mailer.messages()
	if len(sent) != 1 {
		t.Fatalf("mailer recorded %d messages, want 1", len(sent))
	}
	names := map[string]string{}
	for _, h := range sent[0].Headers {
		names[h.Name] = h.Value
	}
	for _, want := range []string{"List-Unsubscribe", "List-Unsubscribe-Post", "List-Id"} {
		if _, ok := names[want]; !ok {
			t.Errorf("test message headers = %+v, missing %q", names, want)
		}
	}
}

// TestAdminCampaignPreview_Test_UsesDedicatedRecipientRow_NotARealSubscriber
// is the recipient/unsubscribe-safety proof named in this file's package
// doc comment: an admin who is ALSO a genuine list member must never have
// their real subscription affected by clicking their own test message's
// unsubscribe link.
func TestAdminCampaignPreview_Test_UsesDedicatedRecipientRow_NotARealSubscriber(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	adminEmail := "admin-test-realmember@example.com"
	admin := seedAdmin(t, pool, adminEmail)
	seedSession(t, pool, admin, "admin-token-test-realmember")

	// This admin is ALSO a real, active subscriber at the SAME address.
	subsStore := subscribers.NewStore(pool)
	realSub, err := subsStore.Create(context.Background(), subscribers.NewSignup{Email: adminEmail, ConfirmTTL: time.Hour}, time.Now())
	if err != nil {
		t.Fatalf("seed real subscriber: %v", err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM subscribers WHERE id = $1`, realSub.ID) })
	if _, err := subsStore.Confirm(context.Background(), *realSub.ConfirmToken, time.Now()); err != nil {
		t.Fatalf("confirm real subscriber: %v", err)
	}
	realSub, err = subsStore.GetByID(context.Background(), realSub.ID)
	if err != nil {
		t.Fatalf("reload real subscriber: %v", err)
	}
	if realSub.Status != subscribers.StatusActive {
		t.Fatalf("real subscriber status = %q, want active", realSub.Status)
	}

	c := seedPreviewCampaign(t, pool, "Realmember subject", "Realmember body")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-realmember", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	sent := mailer.messages()
	if len(sent) != 1 {
		t.Fatalf("mailer recorded %d messages, want 1", len(sent))
	}
	var listUnsub string
	for _, h := range sent[0].Headers {
		if h.Name == "List-Unsubscribe" {
			listUnsub = h.Value
		}
	}
	if listUnsub == "" {
		t.Fatal("test message carries no List-Unsubscribe header")
	}
	if strings.Contains(listUnsub, realSub.ManageToken) {
		t.Fatalf("List-Unsubscribe header carries the REAL subscriber's manage_token — clicking it would unsubscribe a real list member: %s", listUnsub)
	}

	// The token actually embedded resolves to a DIFFERENT, non-active row.
	testRecipient, err := subsStore.FindByEmail(context.Background(), testRecipientEmail(admin))
	if err != nil {
		t.Fatalf("FindByEmail dedicated test recipient: %v", err)
	}
	if testRecipient.ID == realSub.ID {
		t.Fatal("the dedicated test-recipient row IS the real subscriber row — must be a separate row")
	}
	if !strings.Contains(listUnsub, testRecipient.ManageToken) {
		t.Fatalf("List-Unsubscribe header does not carry the dedicated test-recipient's manage_token: %s", listUnsub)
	}
	if testRecipient.Status == subscribers.StatusActive {
		t.Errorf("dedicated test-recipient status = %q, must never be active (would count toward a real audience)", testRecipient.Status)
	}

	// Simulate the click: unsubscribing via the embedded token must affect
	// only the dedicated row, never the real one.
	if _, err := subsStore.Unsubscribe(context.Background(), testRecipient.ID, subscribers.SourceOneClick, time.Now()); err != nil {
		t.Fatalf("simulate one-click unsubscribe on dedicated row: %v", err)
	}
	realAfter, err := subsStore.GetByID(context.Background(), realSub.ID)
	if err != nil {
		t.Fatalf("reload real subscriber after simulated click: %v", err)
	}
	if realAfter.Status != subscribers.StatusActive {
		t.Errorf("real subscriber status after clicking the TEST message's unsubscribe link = %q, want unchanged %q", realAfter.Status, subscribers.StatusActive)
	}
}

func TestAdminCampaignPreview_Test_RepeatedSend_RotatesToken(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-rotate@example.com")
	seedSession(t, pool, admin, "admin-token-test-rotate")

	c := seedPreviewCampaign(t, pool, "Rotate subject", "Rotate body")

	for i := 0; i < 2; i++ {
		resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-rotate", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("send %d: status = %d, want 200 (body=%s)", i, resp.StatusCode, readBody(t, resp))
		}
	}
	sent := mailer.messages()
	if len(sent) != 2 {
		t.Fatalf("mailer recorded %d messages, want 2", len(sent))
	}
	var tok1, tok2 string
	for _, h := range sent[0].Headers {
		if h.Name == "List-Unsubscribe" {
			tok1 = h.Value
		}
	}
	for _, h := range sent[1].Headers {
		if h.Name == "List-Unsubscribe" {
			tok2 = h.Value
		}
	}
	if tok1 == "" || tok2 == "" {
		t.Fatal("missing List-Unsubscribe header on one of the two sends")
	}
	if tok1 == tok2 {
		t.Error("both test sends carried the SAME manage token; want a freshly rotated one per send")
	}
}

func TestAdminCampaignPreview_Test_NilMailer_Returns503(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, nil, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-nilmailer@example.com")
	seedSession(t, pool, admin, "admin-token-test-nilmailer")

	c := seedPreviewCampaign(t, pool, "Nilmailer subject", "Nilmailer body")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-nilmailer", "")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	// A refused test send must not stamp test_sent_at.
	store := mailing.NewCampaignStore(pool)
	after, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.TestSentAt != nil {
		t.Error("TestSentAt was set despite the mailer being unconfigured (503)")
	}
}

func TestAdminCampaignPreview_Test_MailerSendFails(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{err: fmt.Errorf("ses: throttled")}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()
	cleanupTestRecipientRows(t, pool)

	admin := seedAdmin(t, pool, "admin-test-sendfails@example.com")
	seedSession(t, pool, admin, "admin-token-test-sendfails")

	c := seedPreviewCampaign(t, pool, "Sendfails subject", "Sendfails body")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, c.ID), "admin-token-test-sendfails", "")
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 (body=%s)", resp.StatusCode, readBody(t, resp))
	}

	store := mailing.NewCampaignStore(pool)
	after, err := store.GetByID(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if after.TestSentAt != nil {
		t.Error("TestSentAt was set despite the mailer Send call failing")
	}
}

func TestAdminCampaignPreview_Test_CampaignNotFound(t *testing.T) {
	pool := adminSubscribersTestPool(t)
	mailer := &recordingCampaignMailer{}
	srv := httptest.NewServer(adminCampaignPreviewMux(pool, mailer, nil))
	defer srv.Close()

	admin := seedAdmin(t, pool, "admin-test-404@example.com")
	seedSession(t, pool, admin, "admin-token-test-404")

	resp := doJSON(t, srv.Client(), "POST", fmt.Sprintf("%s/admin/campaigns/%d/test", srv.URL, int64(999999999)), "admin-token-test-404", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body=%s)", resp.StatusCode, readBody(t, resp))
	}
	if len(mailer.messages()) != 0 {
		t.Error("mailer was invoked for a nonexistent campaign")
	}
}
