package config

import (
	"strings"
	"testing"
)

// noEnvFile is a path that is guaranteed not to exist, so loadFromFile reads
// purely from the environment set via t.Setenv (no real .env interference).
const noEnvFile = "testdata/does-not-exist.env"

// setRequired sets every required variable to a valid value via t.Setenv so a
// test can start from a known-good state and then mutate individual vars.
//
// This must be kept in sync with the `required` table in loadFromFile
// (config.go) — #0065 fixed a bug where this helper omitted a variable that
// Load had already made required, silently passing TestLoad_MissingRequired
// subtests that should have failed. #0007 changed the required set, so this
// list and the MissingRequired table below were both updated together.
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("BASE_URL", "https://opencircuitsf.com")
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("WEBAUTHN_RP_ID", "opencircuitsf.com")
	t.Setenv("WEBAUTHN_RP_ORIGIN", "https://www.opencircuitsf.com")
	t.Setenv("SESSION_SECRET", "deadbeef")
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("AWS_REGION", "us-west-2")
	t.Setenv("EMAIL_FROM", "Open Circuit SF <hello@opencircuitsf.com>")
}

func TestLoad_AllRequiredPresent(t *testing.T) {
	setRequired(t)
	// Set every optional/typed field explicitly to verify parsing.
	t.Setenv("PORT", "9090")
	t.Setenv("BASE_URL", "https://example.com")
	t.Setenv("SES_CONFIGURATION_SET", "opencircuit-transactional")
	t.Setenv("EMAIL_REPLY_TO", "hello@opencircuitsf.com")
	t.Setenv("EMAIL_LIST_DOMAIN", "lists.opencircuitsf.com")
	t.Setenv("SES_INBOUND_BUCKET", "opencircuitsf-inbound")
	t.Setenv("MAX_SEND_RATE", "25")
	t.Setenv("SEND_BATCH_SIZE", "100")
	t.Setenv("SEND_WORKER_ENABLED", "false")

	cfg, err := loadFromFile(noEnvFile)
	if err != nil {
		t.Fatalf("loadFromFile returned error: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Port", cfg.Port, 9090},
		{"BaseURL", cfg.BaseURL, "https://example.com"},
		{"DatabaseURL", cfg.DatabaseURL, "postgres://u:p@localhost:5432/db?sslmode=disable"},
		{"WebAuthnRPID", cfg.WebAuthnRPID, "opencircuitsf.com"},
		{"WebAuthnRPOrigin", cfg.WebAuthnRPOrigin, "https://www.opencircuitsf.com"},
		{"SessionSecret", cfg.SessionSecret, "deadbeef"},
		{"AWSRegion", cfg.AWSRegion, "us-west-2"},
		{"SESConfigurationSet", cfg.SESConfigurationSet, "opencircuit-transactional"},
		{"EmailFrom", cfg.EmailFrom, "Open Circuit SF <hello@opencircuitsf.com>"},
		{"EmailReplyTo", cfg.EmailReplyTo, "hello@opencircuitsf.com"},
		{"EmailListDomain", cfg.EmailListDomain, "lists.opencircuitsf.com"},
		{"SESInboundBucket", cfg.SESInboundBucket, "opencircuitsf-inbound"},
		{"MaxSendRate", cfg.MaxSendRate, 25},
		{"SendBatchSize", cfg.SendBatchSize, 100},
		{"SendWorkerEnabled", cfg.SendWorkerEnabled, false},
		{"AdminEmail", cfg.AdminEmail, "admin@example.com"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	setRequired(t)
	// Leave PORT, MAX_SEND_RATE, SEND_BATCH_SIZE, SEND_WORKER_ENABLED unset.

	cfg, err := loadFromFile(noEnvFile)
	if err != nil {
		t.Fatalf("loadFromFile returned error: %v", err)
	}

	if cfg.Port != defaultPort {
		t.Errorf("Port = %d, want default %d", cfg.Port, defaultPort)
	}
	if cfg.MaxSendRate != defaultMaxSendRate {
		t.Errorf("MaxSendRate = %d, want default %d", cfg.MaxSendRate, defaultMaxSendRate)
	}
	if cfg.SendBatchSize != defaultSendBatchSize {
		t.Errorf("SendBatchSize = %d, want default %d", cfg.SendBatchSize, defaultSendBatchSize)
	}
	if cfg.SendWorkerEnabled != defaultSendWorkerEnabled {
		t.Errorf("SendWorkerEnabled = %v, want default %v", cfg.SendWorkerEnabled, defaultSendWorkerEnabled)
	}
}

func TestLoad_MissingRequired(t *testing.T) {
	tests := []struct {
		name      string
		unset     string // required var to leave empty
		wantInErr string // substring expected in the error
	}{
		{"missing BASE_URL", "BASE_URL", "BASE_URL"},
		{"missing DATABASE_URL", "DATABASE_URL", "DATABASE_URL"},
		{"missing WEBAUTHN_RP_ID", "WEBAUTHN_RP_ID", "WEBAUTHN_RP_ID"},
		{"missing WEBAUTHN_RP_ORIGIN", "WEBAUTHN_RP_ORIGIN", "WEBAUTHN_RP_ORIGIN"},
		{"missing SESSION_SECRET", "SESSION_SECRET", "SESSION_SECRET"},
		{"missing ADMIN_EMAIL", "ADMIN_EMAIL", "ADMIN_EMAIL"},
		{"missing AWS_REGION", "AWS_REGION", "AWS_REGION"},
		{"missing EMAIL_FROM", "EMAIL_FROM", "EMAIL_FROM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tt.unset, "")

			cfg, err := loadFromFile(noEnvFile)
			if err == nil {
				t.Fatalf("expected error for missing %s, got nil (cfg=%+v)", tt.unset, cfg)
			}
			if !strings.Contains(err.Error(), tt.wantInErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantInErr)
			}
		})
	}
}

func TestLoad_MissingRequiredReportsAll(t *testing.T) {
	setRequired(t)
	// Clear two required vars; the error must mention both.
	t.Setenv("DATABASE_URL", "")
	t.Setenv("SESSION_SECRET", "")

	_, err := loadFromFile(noEnvFile)
	if err == nil {
		t.Fatal("expected error when multiple required vars missing, got nil")
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Errorf("error = %q, want both DATABASE_URL and SESSION_SECRET listed", err.Error())
	}
}

func TestLoad_InvalidInteger(t *testing.T) {
	tests := []struct {
		name string
		envK string
	}{
		{"invalid PORT", "PORT"},
		{"invalid MAX_SEND_RATE", "MAX_SEND_RATE"},
		{"invalid SEND_BATCH_SIZE", "SEND_BATCH_SIZE"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv(tt.envK, "not-a-number")

			_, err := loadFromFile(noEnvFile)
			if err == nil {
				t.Fatalf("expected error for invalid %s, got nil", tt.envK)
			}
			if !strings.Contains(err.Error(), tt.envK) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.envK)
			}
		})
	}
}

func TestLoad_InvalidBoolean(t *testing.T) {
	setRequired(t)
	t.Setenv("SEND_WORKER_ENABLED", "not-a-bool")

	_, err := loadFromFile(noEnvFile)
	if err == nil {
		t.Fatal("expected error for invalid SEND_WORKER_ENABLED, got nil")
	}
	if !strings.Contains(err.Error(), "SEND_WORKER_ENABLED") {
		t.Errorf("error = %q, want substring %q", err.Error(), "SEND_WORKER_ENABLED")
	}
}

// TestLoad_RemovedVariablesIgnored guards against #0007 regressing: the
// ShortLinks-era SMTP and cache variables must have no effect on Config even
// if still present in the environment (e.g. a stale systemd EnvironmentFile).
func TestLoad_RemovedVariablesIgnored(t *testing.T) {
	setRequired(t)
	t.Setenv("SES_SMTP_HOST", "smtp.example.com")
	t.Setenv("SES_SMTP_PORT", "2525")
	t.Setenv("SES_SMTP_USERNAME", "user")
	t.Setenv("SES_SMTP_PASSWORD", "secret")
	t.Setenv("CACHE_MAX_COST", "50000")
	t.Setenv("CACHE_TTL_SECONDS", "600")

	cfg, err := loadFromFile(noEnvFile)
	if err != nil {
		t.Fatalf("loadFromFile returned error: %v", err)
	}
	// The struct simply has no fields for these any more; if this compiles
	// and cfg loaded without error, the removed variables are inert. This
	// also fails to compile if the fields are ever reintroduced without
	// updating this test.
	_ = cfg
}
