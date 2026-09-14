package db

import (
	"bytes"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #0511 — the repo is public, and tracked files must not carry concrete
// infrastructure identifiers: the AWS account id, the two public IPs (current
// and retired), the two EC2 instance ids, the Route 53 hosted zone id, the
// two server hostnames, and the current IAM role name. The decision recorded
// in issues/0511.md is redact going forward, never rewrite history: live
// operational text carries placeholders plus a pointer to the private repo
// (brennanMKE/AWS), while resolved/closed/wontfix issue files stay
// byte-untouched as dated records.
//
// The values themselves live in exactly one tracked place:
// scripts/release.sh's RESTRICTED_B64 array, base64-encoded there (#0514) so
// no tracked file contains the decoded literals. This guard decodes that
// list at test time and sweeps every tracked file for the decoded bytes — so
// the list stays single-sourced, and a future value rotation (a new instance,
// say) updates this guard by editing that one array.
//
// Scope mirrors issues/0511.md: every tracked file, except issue files whose
// status is resolved, closed, or wontfix. open and in-progress issue files
// are in scope; the three settled statuses are dated records, the same
// boundary issues/0508.md established and issues/0511.md reuses.
//
// The sweep is whitespace-tolerant in the sense issues/0508.md's review
// demanded: each file is additionally searched as a copy with the separator
// characters markdown wraps values with (whitespace, backticks, emphasis
// markers, backslashes) removed, so an occurrence wrapped across a line
// break — the shape a line-anchored grep missed in issues/0508.md — is still
// found. TestRestrictedIdentifierScanIsNotVacuous proves both directions of
// that tolerance in memory, without writing the values to any file.
const identifierRedactionReleaseScriptPath = "../../scripts/release.sh"

// Fail-closed floors. A scan that walks nothing must fail, not pass (the
// claude_md_guard_table_parity_test.go lesson): if git ls-files returns an
// implausibly small tree, or the release script's list disappears, this
// guard reports a setup problem instead of a green no-hits result.
const (
	identifierRedactionMinListEntries    = 9
	identifierRedactionMinScannedFiles   = 100
	identifierRedactionMinDecodedTLength = 8
)

// identifierRedactionSeparatorPattern is stripped from a file's bytes before
// the second, tolerant search. The characters are the markdown-wrapping ones
// issues/0508.md's second review enumerated: runs of whitespace, backticks,
// asterisks, underscores, and backslashes between an identifier's parts.
var identifierRedactionSeparatorPattern = regexp.MustCompile("[ \t\r\n`*_\\\\]+")

// restrictedIdentifier is one decoded value plus the label scripts/release.sh
// carries beside it. The label lets a failure name what was found ("public
// IP") without ever printing the value itself — test output gets pasted into
// issue files, and issues/0511.md is itself a live, in-scope file.
type restrictedIdentifier struct {
	value []byte
	label string
}

// restrictedIdentifiers decodes scripts/release.sh's RESTRICTED_B64 array.
// It fails the test rather than returning an empty list: a missing, truncated,
// or undecodable list means the guard would silently scan for nothing.
func restrictedIdentifiers(t *testing.T) []restrictedIdentifier {
	t.Helper()
	raw, err := os.ReadFile(identifierRedactionReleaseScriptPath)
	if err != nil {
		t.Fatalf("read %s: %v — the single source of the restricted values is gone; the redaction guard cannot run", identifierRedactionReleaseScriptPath, err)
	}
	entry := regexp.MustCompile(`(?m)^\s*"([A-Za-z0-9+/=]+)"\s*#\s*(.+?)\s*$`)
	matches := entry.FindAllStringSubmatch(string(raw), -1)
	if len(matches) < identifierRedactionMinListEntries {
		t.Fatalf("%s's RESTRICTED_B64 list has %d entries, want at least %d — the list was truncated or restructured; restore it before this guard can mean anything",
			identifierRedactionReleaseScriptPath, len(matches), identifierRedactionMinListEntries)
	}
	seen := map[string]bool{}
	out := make([]restrictedIdentifier, 0, len(matches))
	for i, m := range matches {
		decoded, err := base64.StdEncoding.DecodeString(m[1])
		if err != nil {
			t.Fatalf("RESTRICTED_B64 entry %d does not decode: %v", i, err)
		}
		if len(decoded) < identifierRedactionMinDecodedTLength {
			t.Fatalf("RESTRICTED_B64 entry %d decodes to only %d bytes — too short to be one of the restricted identifiers", i, len(decoded))
		}
		if seen[string(decoded)] {
			t.Fatalf("RESTRICTED_B64 entry %d duplicates an earlier value", i)
		}
		seen[string(decoded)] = true
		out = append(out, restrictedIdentifier{value: decoded, label: strings.TrimSpace(m[2])})
	}
	return out
}

// identifierRedactionViolations reports one message per (file, identifier)
// pair where the identifier appears in the file's bytes, either literally or
// in the separator-stripped copy. It is a pure function of the inputs so the
// vacuity test can drive it with in-memory buffers.
func identifierRedactionViolations(name string, raw []byte, ids []restrictedIdentifier) []string {
	var violations []string
	stripped := identifierRedactionSeparatorPattern.ReplaceAll(raw, nil)
	for _, id := range ids {
		if bytes.Contains(raw, id.value) || bytes.Contains(stripped, id.value) {
			violations = append(violations,
				name+": contains a restricted identifier ("+id.label+"; value withheld) — redact it per issues/0511.md, leaving a pointer to the private AWS repo")
		}
	}
	return violations
}

// identifierRedactionIsBinary applies the usual NUL-in-the-first-chunk
// heuristic so assets/ PNGs and the like are skipped rather than read as
// text.
func identifierRedactionIsBinary(raw []byte) bool {
	chunk := raw
	if len(chunk) > 8000 {
		chunk = chunk[:8000]
	}
	return bytes.IndexByte(chunk, 0) >= 0
}

// TestRestrictedIdentifierReleaseScriptListIsIntact pins the single source of
// the values: scripts/release.sh's RESTRICTED_B64 array must still be there,
// with at least nine distinct, decodable entries. Deleting or emptying that
// array would otherwise disarm this guard silently; this test turns that
// edit into a loud failure.
func TestRestrictedIdentifierReleaseScriptListIsIntact(t *testing.T) {
	ids := restrictedIdentifiers(t)
	if len(ids) < identifierRedactionMinListEntries {
		t.Fatalf("decoded %d identifiers, want at least %d", len(ids), identifierRedactionMinListEntries)
	}
	for i, id := range ids {
		if id.label == "" {
			t.Fatalf("RESTRICTED_B64 entry %d carries no label comment — the label is what failures name instead of the value", i)
		}
	}
}

// TestRestrictedIdentifierScanIsNotVacuous drives identifierRedactionViolations
// entirely in memory: each decoded value is flagged when it appears in a
// synthetic buffer, still flagged when a line break and backticks are wrapped
// into the middle of it (issues/0508.md's wrapped-occurrence lesson, made
// into an assertion), and never flagged in a clean buffer. The real values
// exist here only as in-memory bytes produced from scripts/release.sh's
// base64 — nothing is written to disk, so no tracked file gains a literal.
func TestRestrictedIdentifierScanIsNotVacuous(t *testing.T) {
	ids := restrictedIdentifiers(t)
	for i, id := range ids {
		direct := append([]byte("deploy target: "), id.value...)
		direct = append(direct, []byte(" per the runbook")...)
		if v := identifierRedactionViolations("direct.bin", direct, ids); len(v) == 0 {
			t.Fatalf("identifier %d (%s) was not flagged in a buffer containing it verbatim — the scan is vacuous", i, id.label)
		}
		half := len(id.value) / 2
		wrapped := append([]byte("see `"), id.value[:half]...)
		wrapped = append(wrapped, []byte("`\n")...)
		wrapped = append(wrapped, id.value[half:]...)
		wrapped = append(wrapped, []byte("` now")...)
		if v := identifierRedactionViolations("wrapped.bin", wrapped, ids); len(v) == 0 {
			t.Fatalf("identifier %d (%s) was not flagged when wrapped across a line break and backticks — the separator-tolerant pass is broken", i, id.label)
		}
	}
	clean := []byte("the deploy host is reached by its ssh-config alias; the account id, instance ids, public IPs, hostnames, hosted zone id and role name live in the private repo")
	if v := identifierRedactionViolations("clean.bin", clean, ids); len(v) != 0 {
		t.Fatalf("a buffer naming the categories but carrying no value was flagged: %v", v)
	}
}

// TestLiveTrackedFilesCarryNoRestrictedIdentifier is the sweep acceptance
// criterion 1 of issues/0511.md asks for: every tracked file, other than
// resolved/closed/wontfix issue files, is free of the nine restricted
// identifiers, literally and wrapped. It fails closed: an implausibly small
// tracked-file list, an unreadable tracked file, or a missing issue-status
// row all stop the run rather than reporting a green zero.
func TestLiveTrackedFilesCarryNoRestrictedIdentifier(t *testing.T) {
	ids := restrictedIdentifiers(t)
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = issueGuardScanRoots[0]
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-files: %v — the sweep cannot enumerate the tree, so it cannot pass", err)
	}
	paths := bytes.Split(out, []byte{0})
	scanned := 0
	issuesSeen := 0
	var failures []string
	for _, p := range paths {
		path := string(p)
		if path == "" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(issueGuardScanRoots[0], path))
		if err != nil {
			t.Fatalf("read tracked file %s: %v", path, err)
		}
		status := ""
		if strings.HasPrefix(path, "issues/") && strings.Count(path, "/") == 1 && strings.HasSuffix(path, ".md") {
			issuesSeen++
			status = issueStatus(string(raw))
		}
		if status == "resolved" || status == "closed" || status == "wontfix" {
			continue
		}
		if identifierRedactionIsBinary(raw) {
			continue
		}
		scanned++
		failures = append(failures, identifierRedactionViolations(path, raw, ids)...)
	}
	if issuesSeen < 10 {
		t.Fatalf("saw only %d issue files — the issues/ filter stopped matching; the scope test means nothing", issuesSeen)
	}
	if scanned < identifierRedactionMinScannedFiles {
		t.Fatalf("scanned only %d tracked files, want at least %d — the tree enumeration is broken, so a zero-hit result would prove nothing", scanned, identifierRedactionMinScannedFiles)
	}
	for _, f := range failures {
		t.Errorf("%s", f)
	}
}
