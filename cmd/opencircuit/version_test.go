package main

import (
	"strings"
	"testing"
)

// TestVersionStringIncludesSemVerAndCommitHash pins the format `main()`'s
// default branch (no argument, `version`, `--version`, or any other
// unrecognized subcommand) prints. #0509's cross-compile procedure builds
// the deployed binary away from any on-box checkout, so the commit hash
// embedded here — via `-ldflags "-X main.commitHash=..."` at build time,
// see docs/deployment.md's redeploy procedure — is the only way to ask a
// running production process what source it actually contains.
func TestVersionStringIncludesSemVerAndCommitHash(t *testing.T) {
	got := versionString()

	if !strings.Contains(got, version) {
		t.Errorf("versionString() = %q, want it to contain the semantic version %q", got, version)
	}
	if !strings.Contains(got, commitHash) {
		t.Errorf("versionString() = %q, want it to contain commitHash %q", got, commitHash)
	}
	if !strings.HasPrefix(got, "opencircuit ") {
		t.Errorf("versionString() = %q, want it to start with %q", got, "opencircuit ")
	}
}

// TestVersionStringReflectsLdflagsOverride proves commitHash is a real
// build-time seam, not a constant that merely looks like one. -ldflags -X
// only works on package-level string vars, so this asserts commitHash is
// exactly that and that versionString() picks up a changed value — the same
// mechanism the linker uses, exercised without invoking the linker.
func TestVersionStringReflectsLdflagsOverride(t *testing.T) {
	original := commitHash
	defer func() { commitHash = original }()

	commitHash = "deadbeefcafef00d"
	got := versionString()
	if !strings.Contains(got, "deadbeefcafef00d") {
		t.Errorf("versionString() = %q, want it to reflect the overridden commitHash", got)
	}
}
