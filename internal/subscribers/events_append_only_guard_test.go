package subscribers

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSubscriberEvents_AppendOnlyGuard is the source-scanning guard
// #0126's plan §6 requires for the "subscriber_events is append-only; no
// update or delete path outside erasure redaction" acceptance criterion.
// Nothing else in this codebase enforces that — the table itself has no
// trigger, and Go's type system doesn't distinguish an INSERT-only SQL
// string from any other.
//
// It walks every .go file under the repo's internal/ tree (this package
// included) and fails if any file OTHER than erase.go contains an UPDATE or
// DELETE statement naming subscriber_events. Text-based rather than
// AST-based: the property is about SQL string literals containing specific
// keywords, which a plain scan finds exactly as reliably as walking
// *ast.BasicLit nodes would, with far less code — see
// internal/handlers/citation_guard_test.go for a case where AST walking
// earns its keep (excluding comments); here comments are deliberately NOT
// excluded, since a comment showing an UPDATE/DELETE example against this
// table (the way this very file's doc comments might) should also be
// reviewed by a human before landing outside erase.go, not silently waved
// through.
func TestSubscriberEvents_AppendOnlyGuard(t *testing.T) {
	root := repoRootForEventsGuard(t)
	internalDir := filepath.Join(root, "internal")

	updatePattern := regexp.MustCompile(`(?i)UPDATE\s+subscriber_events`)
	deletePattern := regexp.MustCompile(`(?i)DELETE\s+FROM\s+subscriber_events`)

	var violations []string
	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		// erase.go is the one legitimate site (the redaction itself is an
		// UPDATE, not a DELETE — see that method's own comment). This
		// guard's own source is exempted too: its doc comment and pattern
		// strings necessarily spell out "UPDATE subscriber_events" and
		// "DELETE FROM subscriber_events" literally, which would otherwise
		// trip its own regexes.
		if rel == filepath.Join("internal", "subscribers", "erase.go") ||
			rel == filepath.Join("internal", "subscribers", "events_append_only_guard_test.go") {
			return nil
		}

		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(body)
		if updatePattern.MatchString(text) {
			violations = append(violations, rel+": UPDATE subscriber_events found outside erase.go")
		}
		if deletePattern.MatchString(text) {
			violations = append(violations, rel+": DELETE FROM subscriber_events found outside erase.go")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", internalDir, err)
	}

	for _, v := range violations {
		t.Error(v)
	}
}

// repoRootForEventsGuard resolves the repository root from this test file's
// own path (two directories up from internal/subscribers), matching the
// convention internal/handlers' citation guards use via runtime.Caller —
// this one uses os.Getwd plus a fixed relative walk instead, since `go
// test` always runs with the package directory as its working directory.
func repoRootForEventsGuard(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	// wd is .../internal/subscribers
	root := filepath.Dir(filepath.Dir(wd))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolved root %q does not contain go.mod: %v", root, err)
	}
	return root
}
