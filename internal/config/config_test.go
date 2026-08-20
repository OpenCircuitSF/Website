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
	t.Setenv("SES_EVENTS_TOPIC_ARN", "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events")
	t.Setenv("MAILER_NOOP", "true")
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
		{"SESEventsTopicARN", cfg.SESEventsTopicARN, "arn:aws:sns:us-west-2:123456789012:opencircuit-ses-events"},
		{"MailerNoOp", cfg.MailerNoOp, true},
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

// TestLoad_BaseURLTrailingSlashNormalised proves BASE_URL with any number of
// trailing slashes loads to the identical value (#0085). Production's value
// happens to lack the slash, which is the only reason a doubled "//path" was
// latent rather than live; a trailing slash is the single most common way an
// operator writes a base URL in /etc/opencircuit/config.env. The doubled-
// slash case ("https://example.com//") is the one the #0085 review bounce
// was filed over: strings.TrimSuffix only removed one slash, so that input
// still produced "//login" downstream. This test covers the whole boundary
// table from the review, not just the single-slash case.
//
// Mutation proof: revert normalizeBaseURL to strings.TrimSuffix(raw, "/")
// and the "https://example.com//" case fails with
// BaseURL = "https://example.com/", want "https://example.com" — see
// issues/0085.md Verification for the observed output.
func TestLoad_BaseURLTrailingSlashNormalised(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{"no slash", "https://example.com"},
		{"one slash", "https://example.com/"},
		{"two slashes", "https://example.com//"},
		{"three slashes", "https://example.com///"},
		{"path with one slash", "https://example.com/base/"},
		{"path with two slashes", "https://example.com/base//"},
	}

	const want = "https://example.com"
	const wantPathBase = "https://example.com/base"

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setRequired(t)
			t.Setenv("BASE_URL", tc.env)

			cfg, err := loadFromFile(noEnvFile)
			if err != nil {
				t.Fatalf("loadFromFile returned error: %v", err)
			}

			wantVal := want
			if strings.Contains(tc.env, "/base") {
				wantVal = wantPathBase
			}
			if cfg.BaseURL != wantVal {
				t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, wantVal)
			}
		})
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
	if cfg.MailerNoOp != false {
		t.Errorf("MailerNoOp = %v, want default false — production must default to the real SES mailer", cfg.MailerNoOp)
	}
}

// TestLoad_SESEventsTopicARNOptional proves SES_EVENTS_TOPIC_ARN is NOT a
// required variable (#0037): the topic is not provisioned yet (CLAUDE.md §10
// item 2), so the binary must still boot with it unset, loading to "". A
// verifier that treats an empty configured topic ARN as "reject everything"
// (rather than "check nothing") is what keeps this safe — see
// internal/sesnotify.Verifier.
func TestLoad_SESEventsTopicARNOptional(t *testing.T) {
	setRequired(t)
	t.Setenv("SES_EVENTS_TOPIC_ARN", "")

	cfg, err := loadFromFile(noEnvFile)
	if err != nil {
		t.Fatalf("loadFromFile returned error with SES_EVENTS_TOPIC_ARN unset: %v", err)
	}
	if cfg.SESEventsTopicARN != "" {
		t.Errorf("SESEventsTopicARN = %q, want empty", cfg.SESEventsTopicARN)
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

func TestLoad_InvalidMailerNoOpBoolean(t *testing.T) {
	setRequired(t)
	t.Setenv("MAILER_NOOP", "not-a-bool")

	_, err := loadFromFile(noEnvFile)
	if err == nil {
		t.Fatal("expected error for invalid MAILER_NOOP, got nil")
	}
	if !strings.Contains(err.Error(), "MAILER_NOOP") {
		t.Errorf("error = %q, want substring %q", err.Error(), "MAILER_NOOP")
	}
}

// TestLoad_RemovedVariablesIgnored guards against #0007 regressing: the
// removed SMTP and cache variables from the source skeleton must have no
// effect on Config even if still present in the environment (e.g. a stale
// systemd EnvironmentFile).
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
