// worker_wiring_test.go proves #0045's send-worker construction decisions —
// SEND_WORKER_ENABLED, MAILER_NOOP, and the shutdown budget — at the exact
// call sites servePostgres uses (newSendStoreIfEnabled/
// newSendWorkerIfEnabled), rather than only at mailing.NewWorker's own
// narrower unit-test level.
package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/brennanMKE/OpenCircuitSF/internal/config"
	"github.com/brennanMKE/OpenCircuitSF/internal/db"
	"github.com/brennanMKE/OpenCircuitSF/internal/mailing"
)

func baseWorkerTestConfig() *config.Config {
	return &config.Config{
		BaseURL:           "https://www.example-oc-test.com",
		EmailListDomain:   "lists.example-oc-test.com",
		EmailFrom:         "hello@example-oc-test.com",
		EmailReplyTo:      "hello@example-oc-test.com",
		MaxSendRate:       10,
		SendBatchSize:     50,
		SendWorkerEnabled: true,
		MailerNoOp:        false,
	}
}

// TestNewSendWorkerIfEnabled_MailerNoOp_Refuses proves the send worker
// never constructs under MAILER_NOOP=true, regardless of
// SEND_WORKER_ENABLED — matching mailing.NewWorker's own doc comment: the
// no-op mailer's literal "noop" message id would poison #0038's
// bounce/complaint join key.
func TestNewSendWorkerIfEnabled_MailerNoOp_Refuses(t *testing.T) {
	cfg := baseWorkerTestConfig()
	cfg.MailerNoOp = true

	sendStore := newSendStoreIfEnabled(cfg, nil, nil, nil)
	if sendStore != nil {
		t.Fatal("newSendStoreIfEnabled under MAILER_NOOP=true = non-nil, want nil")
	}

	w, err := newSendWorkerIfEnabled(cfg, sendStore, nil, nil, nil, nil, nil, nil, discardLogger())
	if err != nil {
		t.Fatalf("newSendWorkerIfEnabled: %v", err)
	}
	if w != nil {
		t.Fatal("newSendWorkerIfEnabled under MAILER_NOOP=true = non-nil worker, want nil")
	}
}

// TestNewSendWorkerIfEnabled_SendWorkerDisabled_ReturnsNil proves
// SEND_WORKER_ENABLED=false produces no worker on this instance — the
// scaling knob #0045's plan §1 describes (exactly one instance runs the
// worker if the site is ever scaled to two).
func TestNewSendWorkerIfEnabled_SendWorkerDisabled_ReturnsNil(t *testing.T) {
	cfg := baseWorkerTestConfig()
	cfg.SendWorkerEnabled = false

	sendStore := newSendStoreIfEnabled(cfg, nil, nil, nil)
	if sendStore == nil {
		t.Fatal("newSendStoreIfEnabled = nil, want non-nil — SEND_WORKER_ENABLED must not affect the preflight adapter's availability")
	}

	w, err := newSendWorkerIfEnabled(cfg, sendStore, nil, nil, nil, nil, nil, nil, discardLogger())
	if err != nil {
		t.Fatalf("newSendWorkerIfEnabled: %v", err)
	}
	if w != nil {
		t.Fatal("newSendWorkerIfEnabled under SEND_WORKER_ENABLED=false = non-nil worker, want nil")
	}
}

// TestNewSendWorkerIfEnabled_Enabled_ConstructsWorker proves the ordinary
// case: MAILER_NOOP=false and SEND_WORKER_ENABLED=true produce a real
// worker. Construction alone touches no database (mailing.NewSendStore and
// mailing.NewWorker only validate and store their dependencies), so this
// case needs no TEST_DATABASE_URL.
func TestNewSendWorkerIfEnabled_Enabled_ConstructsWorker(t *testing.T) {
	cfg := baseWorkerTestConfig()
	audienceStore := mailing.NewAudienceStore(nil) // construction only — no DB call

	sendStore := newSendStoreIfEnabled(cfg, nil, audienceStore, nullSettings{})
	if sendStore == nil {
		t.Fatal("newSendStoreIfEnabled = nil, want non-nil")
	}

	w, err := newSendWorkerIfEnabled(cfg, sendStore, audienceStore, nil, nullMailer{}, nullSettings{}, nil, nil, discardLogger())
	if err != nil {
		t.Fatalf("newSendWorkerIfEnabled: %v", err)
	}
	if w == nil {
		t.Fatal("newSendWorkerIfEnabled under normal config = nil, want a real worker")
	}
}

// TestSendWorker_StopReturnsWithinBudgetOnSIGTERM proves the worker.Stop
// call mountAndServe's shutdown sequence makes (workerCloseTimeout-bounded,
// the FIRST step of that sequence per #0045's plan §1) actually returns
// promptly once Run is looping, rather than hanging until its context
// budget is exhausted. Uses a real DB-backed SendStore since Worker.Run
// polls it every tick.
func TestSendWorker_StopReturnsWithinBudgetOnSIGTERM(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping live DB integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := db.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer pool.Close()

	audienceStore := mailing.NewAudienceStore(pool)
	settings := nullSettings{}
	sendStore := mailing.NewSendStore(pool, audienceStore, settings, nil,
		"https://www.example-oc-test.com", "lists.example-oc-test.com", "hello@example-oc-test.com")

	w, err := mailing.NewWorker(mailing.WorkerDeps{
		Store: sendStore, Audience: audienceStore, Mailer: nullMailer{}, Settings: settings,
		BaseURL: "https://www.example-oc-test.com", ListDomain: "lists.example-oc-test.com",
		FromAddr: "hello@example-oc-test.com", ReplyTo: "hello@example-oc-test.com",
		EnvMaxSendRate: 10, BatchSize: 50, PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewWorker: %v", err)
	}

	runDone := make(chan struct{})
	go func() {
		w.Run(context.Background())
		close(runDone)
	}()

	// Give Run at least one poll cycle before signalling stop, so this
	// genuinely exercises "stop while looping" rather than "stop before it
	// ever started".
	time.Sleep(20 * time.Millisecond)

	stopCtx, stopCancel := context.WithTimeout(context.Background(), workerCloseTimeout)
	defer stopCancel()

	start := time.Now()
	if err := w.Stop(stopCtx); err != nil {
		t.Fatalf("Stop() = %v, want nil (returned within budget)", err)
	}
	elapsed := time.Since(start)
	if elapsed >= workerCloseTimeout {
		t.Errorf("Stop() took %v, want comfortably under workerCloseTimeout (%v)", elapsed, workerCloseTimeout)
	}

	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("Run() goroutine did not exit after Stop() returned")
	}
}

// ── fakes used only by this file's construction-only tests ─────────────────

type nullMailer struct{}

func (nullMailer) Send(context.Context, mailing.Message) (string, error) { return "id", nil }

type nullSettings struct{}

func (nullSettings) GetSetting(context.Context, string) (string, error) { return "", nil }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
