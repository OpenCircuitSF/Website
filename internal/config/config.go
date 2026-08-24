package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

// Default values applied when an optional variable is unset.
const (
	defaultPort              = 8080
	defaultMaxSendRate       = 10
	defaultSendBatchSize     = 50
	defaultSendWorkerEnabled = true
)

// Config holds all runtime configuration loaded from the environment.
type Config struct {
	// Server
	Port    int
	BaseURL string

	// Database
	DatabaseURL string

	// Storage backend selector. ONLY the explicit value "json" selects the
	// in-memory dev backend (see DevMode); anything else — including the default
	// empty value — uses Postgres. An empty DATABASE_URL does NOT engage dev mode,
	// so production can't accidentally fall back to the in-memory store.
	Storage string

	// WebAuthn
	WebAuthnRPID     string
	WebAuthnRPOrigin string

	// Sessions
	SessionSecret string

	// AWS / SES. There are no static credentials here: the EC2 instance role
	// supplies them and the AWS SDK's default credential chain finds them.
	// Locally the chain falls back to ~/.aws/credentials.
	AWSRegion           string
	SESConfigurationSet string
	EmailFrom           string
	EmailReplyTo        string

	// EmailListDomain is the subdomain mailing.CampaignHeaders (#0035)
	// interpolates into the mailto: form of every campaign's List-Unsubscribe
	// header (RFC 8058). Required, with no default (#0105): the value is a
	// domain identity, not an operational knob, so it follows the same
	// convention as WEBAUTHN_RP_ID and EMAIL_FROM below rather than being
	// silently defaulted to "lists.opencircuitsf.com" — a misconfigured
	// deploy should fail loud at boot, not send a header advertising an
	// unsubscribe address that may not route. Must never be the bare apex
	// opencircuitsf.com: #0057 points this subdomain's MX at SES for inbound
	// Path-3 handling, and pointing the apex's own MX at SES would hijack
	// all mail to the domain (CLAUDE.md §9).
	EmailListDomain  string
	SESInboundBucket string

	// SESEventsTopicARN is the single SNS topic ARN internal/sesnotify (#0037)
	// will accept SES bounce/complaint notifications from. A valid SNS
	// signature only proves SNS sent a message, not that it came through our
	// topic — any AWS account can subscribe our public endpoint to a topic of
	// their own. This is deliberately NOT in the `required` table below: the
	// topic does not exist yet (CLAUDE.md §10 item 2), and until it is
	// provisioned an empty value means sesnotify rejects every message rather
	// than failing to boot.
	SESEventsTopicARN string

	// MailerNoOp selects auth.NoOpMailer instead of the real SES v2 API mailer
	// on the Postgres serve path (#0027). Default false: production always
	// gets the real mailer. Set true for local development against Postgres
	// before SES is set up (CLAUDE.md §10 item 2) — NoOpMailer logs the full
	// verification/recovery link to stdout instead of sending it, which is how
	// #0008's manual passkey verification procedure reads the magic link.
	MailerNoOp bool

	// SESSandbox reports whether the SES account is still sandboxed (200
	// messages/day, verified recipients only — PRD §10.4). There is no live
	// AWS call anywhere in this codebase that could detect this (the
	// account doesn't exist yet as of CLAUDE.md §10 item 2, "not started"),
	// so this is a manually-set operational flag, the same shape as
	// MailerNoOp above: it defaults to true (sandboxed, the safe assumption
	// for a project that has never requested production access) and an
	// operator flips it to false once AWS approves the production-access
	// request. #0061's admin overview dashboard surfaces it as a warning
	// while true, so the operator sees "still sandboxed" on every visit
	// rather than needing to remember to check.
	SESSandbox bool

	// Sending. These are the environment-level ceiling/toggles; the operator
	// dial beneath MaxSendRate and the signup/registration toggles live in the
	// settings table, not here.
	MaxSendRate       int
	SendBatchSize     int
	SendWorkerEnabled bool

	// Bootstrap
	AdminEmail string
}

// DevMode reports whether the dev store should be used instead of Postgres.
// It is true only when STORAGE=json is explicitly set. DATABASE_URL remains
// required in non-dev mode; set STORAGE=json to skip the DB entirely.
func (c *Config) DevMode() bool {
	return c.Storage == "json"
}

// Load reads configuration from the environment. In development it first loads
// a .env file (via godotenv) if one is present in the current working
// directory; a missing .env file is not an error, since the variables may
// already be set in the process environment (e.g. by systemd in production).
//
// Defaults are applied for unset optional variables, integer/bool fields are
// parsed, and required fields are validated. If any required field is missing
// or any typed field fails to parse, Load returns an error describing every
// problem it found rather than just the first.
func Load() (*Config, error) {
	return loadFromFile(".env")
}

// loadFromFile is the internal implementation of Load with an explicit .env
// path, kept unexported so tests can avoid touching the real working directory.
func loadFromFile(path string) (*Config, error) {
	// Load .env if present. A missing file is not an error; any other error
	// (e.g. malformed file) is surfaced to the caller.
	if _, statErr := os.Stat(path); statErr == nil {
		if err := godotenv.Load(path); err != nil {
			return nil, fmt.Errorf("config: loading %s: %w", path, err)
		}
	}

	var errs []string

	cfg := &Config{
		Port:                getInt("PORT", defaultPort, &errs),
		BaseURL:             normalizeBaseURL(os.Getenv("BASE_URL")),
		DatabaseURL:         os.Getenv("DATABASE_URL"),
		Storage:             os.Getenv("STORAGE"),
		WebAuthnRPID:        os.Getenv("WEBAUTHN_RP_ID"),
		WebAuthnRPOrigin:    os.Getenv("WEBAUTHN_RP_ORIGIN"),
		SessionSecret:       os.Getenv("SESSION_SECRET"),
		AWSRegion:           os.Getenv("AWS_REGION"),
		SESConfigurationSet: os.Getenv("SES_CONFIGURATION_SET"),
		EmailFrom:           os.Getenv("EMAIL_FROM"),
		EmailReplyTo:        os.Getenv("EMAIL_REPLY_TO"),
		EmailListDomain:     os.Getenv("EMAIL_LIST_DOMAIN"),
		SESInboundBucket:    os.Getenv("SES_INBOUND_BUCKET"),
		SESEventsTopicARN:   os.Getenv("SES_EVENTS_TOPIC_ARN"),
		MailerNoOp:          getBool("MAILER_NOOP", false, &errs),
		SESSandbox:          getBool("SES_SANDBOX", true, &errs),
		MaxSendRate:         getInt("MAX_SEND_RATE", defaultMaxSendRate, &errs),
		SendBatchSize:       getInt("SEND_BATCH_SIZE", defaultSendBatchSize, &errs),
		SendWorkerEnabled:   getBool("SEND_WORKER_ENABLED", defaultSendWorkerEnabled, &errs),
		AdminEmail:          os.Getenv("ADMIN_EMAIL"),
	}

	// Validate required fields. When STORAGE=json (dev mode) DATABASE_URL is
	// not required; in all other cases every variable below must be present.
	// Collect every missing required variable so the error is comprehensive.
	devMode := cfg.DevMode()
	required := []struct {
		name      string
		value     string
		skipInDev bool // when true, skip this check in dev mode
	}{
		{"BASE_URL", cfg.BaseURL, false},
		{"DATABASE_URL", cfg.DatabaseURL, true}, // not required when STORAGE=json
		{"WEBAUTHN_RP_ID", cfg.WebAuthnRPID, false},
		{"WEBAUTHN_RP_ORIGIN", cfg.WebAuthnRPOrigin, false},
		{"SESSION_SECRET", cfg.SessionSecret, false},
		{"ADMIN_EMAIL", cfg.AdminEmail, false},
		{"AWS_REGION", cfg.AWSRegion, false},
		{"EMAIL_FROM", cfg.EmailFrom, false},
		{"EMAIL_LIST_DOMAIN", cfg.EmailListDomain, false},
	}
	for _, r := range required {
		if r.value == "" && !(r.skipInDev && devMode) {
			errs = append(errs, fmt.Sprintf("missing required variable %s", r.name))
		}
	}

	if len(errs) > 0 {
		return nil, fmt.Errorf("config: %s", strings.Join(errs, "; "))
	}

	return cfg, nil
}

// normalizeBaseURL strips every trailing slash from an operator-supplied
// BASE_URL, so "https://www.opencircuitsf.com/",
// "https://www.opencircuitsf.com//", and
// "https://www.opencircuitsf.com" all load to the identical value (#0085).
//
// This is the single normalisation point for BASE_URL. Every consumer —
// the five transactional email builders (internal/mailing), internal/seo's
// canonical/og:url/og:image rendering, the sitemap's <loc> values, and
// robots.txt's Sitemap: line — concatenates baseURL+"/path" directly and
// trusts this value to already be clean, per #0072's argument that one place
// should define BASE_URL and everything downstream should trust it rather
// than re-guard at each call site.
//
// strings.TrimRight, not strings.TrimSuffix: TrimSuffix removes one instance
// of the suffix, so "https://x.com//" would normalise to "https://x.com/"
// and still produce a doubled-slash "//login" when a caller appends
// "/login" — the exact defect this issue was filed about (#0085 review,
// 2026-08-19). TrimRight removes the whole trailing run. An input of "/" or
// "//" collapses to "" and is caught by the required-field check below (the
// resulting error names BASE_URL as "missing" rather than "malformed",
// which is acceptable — see issues/0085.md ## Gotchas). An empty input
// round-trips unchanged. Whitespace is deliberately not handled here:
// godotenv trims it on the .env load path and systemd's EnvironmentFile
// does the same on the production path, so it never reaches this function.
func normalizeBaseURL(raw string) string {
	return strings.TrimRight(raw, "/")
}

// getInt reads an integer environment variable, returning def when the variable
// is unset or empty. If the variable is set but cannot be parsed as an integer,
// an error message is appended to errs and def is returned.
func getInt(name string, def int, errs *[]string) int {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("invalid integer for %s: %q", name, raw))
		return def
	}
	return v
}

// getBool reads a boolean environment variable, returning def when the variable
// is unset or empty. If the variable is set but cannot be parsed as a boolean
// (per strconv.ParseBool: "1", "t", "T", "TRUE", "true", "True", "0", "f", "F",
// "FALSE", "false", "False"), an error message is appended to errs and def is
// returned.
func getBool(name string, def bool, errs *[]string) bool {
	raw := os.Getenv(name)
	if raw == "" {
		return def
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		*errs = append(*errs, fmt.Sprintf("invalid boolean for %s: %q", name, raw))
		return def
	}
	return v
}
