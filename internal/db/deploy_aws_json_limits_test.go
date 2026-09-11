package db

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #0057 review (2026-09-11, bounce #1): both Route 53 change-batch documents
// committed for this issue's AWS runbook carried a `Comment` field over
// Route 53's own 256-character limit -- `ChangeBatch.Comment` is shape
// `ResourceDescription`, `{"type": "string", "max": 256}` in Route 53's own
// service model (2013-04-01), confirmed against aws-cli 2.36.43's bundled
// botocore data. The committed values were 327 chars (D1) and 720 (D2).
// Botocore does not enforce string length client-side, so both calls would
// have reached the live API and been rejected there with an InvalidInput
// error naming Comment -- on D2, the runbook's last, mail-enabling step.
// `aws ... --generate-cli-skeleton` cannot catch this: a skeleton describes
// structure, not constraints, which is exactly why the implementation
// pass's own verification (every file parses, every file matches its
// skeleton) passed anyway.
//
// This guard scans every JSON file currently in deploy/aws/ -- not just
// D1/D2 by name, so a future file dropped into this directory is covered
// automatically -- against the AWS-documented length limits on the fields
// these files actually use:
//
//   - Route 53 ChangeBatch.Comment <= 256 chars (the ResourceDescription
//     shape above; deploy/aws/D1-route53-change-batch-txt.json and
//     deploy/aws/D2-route53-change-batch-mx.json carry this field).
//   - S3 LifecycleRule.ID <= 255 chars. The S3 2006-03-01 model's own ID
//     shape carries no machine-checkable `max` (`{"type": "string"}`), but
//     its field documentation states "The value cannot be longer than 255
//     characters" -- the same class of server-side-only constraint as
//     Comment, just documented in prose instead of the shape's own `max`
//     key. deploy/aws/A5-s3-lifecycle.json (the inbound bucket's lifecycle
//     rule) carries this field.
//
// Deliberately not modelled here, so as not to gold-plate: the other AWS
// field types this directory's files touch -- IAM/S3-policy `Sid`, SES
// `ReceiptRuleName`, SNS/S3 ARNs, `ObjectKeyPrefix` -- have no numeric `max`
// in the botocore model and no single citable figure. Inventing one would
// be exactly the "guard pinned to a guessed constant" failure this file
// exists to avoid; widen the table below only when a future AWS object
// type in this directory turns out to carry a real, citable limit.
const deployAWSDir = "../../deploy/aws"

const (
	route53CommentMaxLen = 256
	s3LifecycleIDMaxLen  = 255
)

// deployAWSJSONViolations checks one JSON document's raw bytes against the
// limits documented above and returns one human-readable message per
// breach (nil if none). It takes bytes, not a file path, so the guard-is-
// not-vacuous test below can feed it a mutated in-memory document without
// ever touching a file this repo has committed (CLAUDE.md §8a forbids
// mutating a committed file to prove a guard fails).
func deployAWSJSONViolations(name string, data []byte) []string {
	var violations []string

	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		return []string{fmt.Sprintf("%s: invalid JSON: %v", name, err)}
	}

	if c, ok := doc["Comment"].(string); ok {
		if n := len(c); n > route53CommentMaxLen {
			violations = append(violations, fmt.Sprintf(
				"%s: Comment is %d bytes, exceeds Route 53 ChangeBatch.Comment's %d-byte limit (ResourceDescription)",
				name, n, route53CommentMaxLen))
		}
	}

	if rules, ok := doc["Rules"].([]any); ok {
		for i, r := range rules {
			rule, ok := r.(map[string]any)
			if !ok {
				continue
			}
			id, ok := rule["ID"].(string)
			if !ok {
				continue
			}
			if n := len(id); n > s3LifecycleIDMaxLen {
				violations = append(violations, fmt.Sprintf(
					"%s: Rules[%d].ID is %d bytes, exceeds S3 LifecycleRule.ID's %d-byte limit",
					name, i, n, s3LifecycleIDMaxLen))
			}
		}
	}

	return violations
}

// TestDeployAWSJSONFieldLimits is the real guard: every *.json file
// anywhere under deploy/aws/ -- including subdirectories, per #0057 review
// (bounce #2): os.ReadDir does not recurse, so a file dropped into a nested
// directory (e.g. deploy/aws/nested/bad.json) was silently skipped, measured
// with a 900-byte Comment passing -- must satisfy deployAWSJSONViolations.
func TestDeployAWSJSONFieldLimits(t *testing.T) {
	found := 0
	walkErr := filepath.WalkDir(deployAWSDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".json" {
			return nil
		}
		found++
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, v := range deployAWSJSONViolations(path, data) {
			t.Errorf("%s", v)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk %s: %v", deployAWSDir, walkErr)
	}
	if found == 0 {
		t.Fatalf("found no *.json files under %s -- has the repo layout changed?", deployAWSDir)
	}
}

// TestDeployAWSJSONFieldLimitsCatchesOverLength proves the guard above is
// not vacuous. It builds documents entirely in memory -- never a committed
// file -- with a Comment and a lifecycle ID one character over their
// documented limits, and confirms deployAWSJSONViolations flags both. It
// also confirms a compliant document produces no violations, so the guard
// is not simply "always fail" either.
func TestDeployAWSJSONFieldLimitsCatchesOverLength(t *testing.T) {
	overLongComment := strings.Repeat("x", route53CommentMaxLen+1)
	commentDoc, err := json.Marshal(map[string]any{"Comment": overLongComment})
	if err != nil {
		t.Fatalf("marshal test fixture: %v", err)
	}
	if v := deployAWSJSONViolations("mutated-comment.json", commentDoc); len(v) == 0 {
		t.Fatalf("deployAWSJSONViolations did not flag a %d-char Comment against a %d-char limit -- guard is vacuous",
			len(overLongComment), route53CommentMaxLen)
	}

	overLongID := strings.Repeat("y", s3LifecycleIDMaxLen+1)
	lifecycleDoc, err := json.Marshal(map[string]any{
		"Rules": []map[string]any{{"ID": overLongID}},
	})
	if err != nil {
		t.Fatalf("marshal test fixture: %v", err)
	}
	if v := deployAWSJSONViolations("mutated-lifecycle.json", lifecycleDoc); len(v) == 0 {
		t.Fatalf("deployAWSJSONViolations did not flag a %d-char lifecycle ID against a %d-char limit -- guard is vacuous",
			len(overLongID), s3LifecycleIDMaxLen)
	}

	okDoc, err := json.Marshal(map[string]any{
		"Comment": "short",
		"Rules":   []map[string]any{{"ID": "short-id"}},
	})
	if err != nil {
		t.Fatalf("marshal test fixture: %v", err)
	}
	if v := deployAWSJSONViolations("compliant.json", okDoc); len(v) != 0 {
		t.Fatalf("deployAWSJSONViolations flagged a compliant document: %v", v)
	}
}

// TestDeployAWSJSONLimitConstantsMatchDocumented pins route53CommentMaxLen
// and s3LifecycleIDMaxLen against literals spelled out independently of the
// constants themselves. Per #0057 review (bounce #2) and CLAUDE.md's #0258
// rule ("a guard's oracle must not be the same bytes as its subject"):
// TestDeployAWSJSONFieldLimitsCatchesOverLength builds its fixtures as
// route53CommentMaxLen+1 / s3LifecycleIDMaxLen+1, so it moves with the
// constant rather than checking it -- measured by raising
// route53CommentMaxLen to 10000 and restoring D2's original 720-char
// Comment: both TestDeployAWSJSONFieldLimits and the vacuity test stayed
// green. This test is the independent oracle: it fails the moment either
// constant no longer equals the documented AWS limit, regardless of what
// the rest of the file's fixtures are built from.
func TestDeployAWSJSONLimitConstantsMatchDocumented(t *testing.T) {
	if route53CommentMaxLen != 256 {
		t.Errorf("route53CommentMaxLen = %d, want 256 (Route 53 ChangeBatch.Comment's documented ResourceDescription max)",
			route53CommentMaxLen)
	}
	if s3LifecycleIDMaxLen != 255 {
		t.Errorf("s3LifecycleIDMaxLen = %d, want 255 (S3 LifecycleRule.ID's documented max, per field prose -- the shape carries no machine-checkable max)",
			s3LifecycleIDMaxLen)
	}
}
