package db

import (
	"bytes"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// gitignoreSourceRoots are the source trees no .gitignore pattern may reach
// (#0688). An unanchored coverage.* once matched a file named coverage.go in
// the cadence package: git never tracked it, main stopped building, and
// removing the worktree deleted the only copy.
var gitignoreSourceRoots = []string{"internal", "cmd", "web/src"}

// gitignoreSourceExts are the source kinds the guard checks.
var gitignoreSourceExts = []string{".go", ".ts", ".svelte"}

// gitignoreProbeStems are names a source file could plausibly take that a
// tool-output pattern might also match. Each is probed with every extension
// in every directory under the roots, so a too-broad pattern fails here
// before anyone writes the file it would swallow.
var gitignoreProbeStems = []string{"coverage", "cover", "profile", "debug", "build", "dist", "tmp", "out", "test", "bin"}

// gitignoreSourceMinFiles is a fail-closed floor: a walk that finds fewer real
// source files than this found the wrong tree, and must not pass.
const gitignoreSourceMinFiles = 200

// TestGitignoreMatchesNoSourcePath (#0688) asks git itself, with
// check-ignore --no-index (so a tracked file is still tested against the
// patterns), whether any source path under gitignoreSourceRoots is ignored:
// every real file on disk, tracked or not, plus a probe set of plausible new
// names in every directory.
func TestGitignoreMatchesNoSourcePath(t *testing.T) {
	root := issueGuardScanRoots[0]
	var paths []string
	real := 0
	dirs := map[string]bool{}
	for _, r := range gitignoreSourceRoots {
		err := filepath.WalkDir(filepath.Join(root, r), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				rel, _ := filepath.Rel(root, p)
				dirs[filepath.ToSlash(rel)] = true
				return nil
			}
			for _, ext := range gitignoreSourceExts {
				if strings.HasSuffix(p, ext) {
					rel, _ := filepath.Rel(root, p)
					paths = append(paths, filepath.ToSlash(rel))
					real++
					break
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", r, err)
		}
	}
	if real < gitignoreSourceMinFiles {
		t.Fatalf("found %d source files under %v; expected at least %d — the walk is in the wrong place", real, gitignoreSourceRoots, gitignoreSourceMinFiles)
	}
	for d := range dirs {
		for _, stem := range gitignoreProbeStems {
			for _, ext := range gitignoreSourceExts {
				paths = append(paths, d+"/"+stem+ext)
			}
		}
	}
	ignored := checkIgnored(t, root, paths)
	sort.Strings(ignored)
	for _, p := range ignored {
		t.Errorf(".gitignore matches source path %s; narrow or anchor the pattern (git check-ignore -v --no-index %s)", p, p)
	}
}

// TestGitignoreGuardSeesAPattern proves checkIgnored is not vacuous: a path
// .gitignore does ignore comes back. Without it, a git that answered nothing
// would leave the guard above green.
func TestGitignoreGuardSeesAPattern(t *testing.T) {
	got := checkIgnored(t, issueGuardScanRoots[0], []string{"internal/x/y.test", "internal/x/y.go"})
	if len(got) != 1 || got[0] != "internal/x/y.test" {
		t.Fatalf("check-ignore returned %v; want only internal/x/y.test (the *.test pattern)", got)
	}
}

// checkIgnored returns the paths .gitignore matches. git check-ignore exits 1
// when none match, which is not an error here.
func checkIgnored(t *testing.T, root string, paths []string) []string {
	t.Helper()
	cmd := exec.Command("git", "check-ignore", "--no-index", "--stdin", "-z")
	cmd.Dir = root
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := cmd.Output()
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return nil
	} else if err != nil {
		t.Fatalf("git check-ignore: %v", err)
	}
	var got []string
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			got = append(got, string(p))
		}
	}
	return got
}
