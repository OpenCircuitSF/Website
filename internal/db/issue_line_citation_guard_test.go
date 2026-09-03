package db

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// #0384: `#0352` and `#0356` settled that a Go comment must never cite
// another file by a bare line number — replace it with a stable identifier,
// because every edit to the CITED file above the cited line moves what the
// number points at, for reasons that have nothing to do with the citation
// itself. `internal/handlers`'s TestNoCommentCitesGoFileByLineNumber
// enforces that rule over every Go comment under internal/, cmd/ and web/ —
// but issue prose is Markdown, not a Go comment, and nothing scanned it.
// Three instances reached issues/*.md in one phase, all traced by a
// reviewer rather than by any tool, and the third went stale inside its own
// issue's lifetime — the sharpest demonstration available that these
// citations do not survive.
//
// # Decision one — scope: convention-wide, not a historical-record carve-out
//
// Two positions were on the table. One exempts an issue's own review and
// verification records wholesale, on the reasoning that they are a record
// of a run at a specific tree state and rewriting one would falsify it. The
// other — chosen here — is the stronger analogue of `#0352`'s own settled
// rule: nothing about "this text sits under a Review notes heading" makes a
// positional citation any less likely to drift, and a carve-out keyed on a
// heading is not a property of the citation itself — it is a property of
// where somebody chose to put it, and a heading costs nothing to add. So
// this guard's scanned sections (below) still include Verification and
// Review notes, exactly where the sibling Test-name-citation guard above
// already looks — a citation in either is judged the same way as one
// anywhere else this guard reads.
//
// What makes the instruction not to override #0379's reviewer moot rather
// than contradicted: this guard, like the sibling guard above, enforces the
// convention only against open and in-progress issue files (see
// includeStatus below). A resolved issue's own historical citations —
// #0377's and #0379's among them — are never re-examined by this guard, for
// the same reason #0265 gives for the sibling guard: by the time an issue is
// resolved, the citation is no longer actionable by the agent that wrote it,
// and re-litigating a closed record is not this guard's business. So the
// reviewer's decision to leave #0379's record exactly as written stands
// undisturbed — this guard would never have reached it either way, and
// nothing here proposes to.
//
// # Decision two — build, having verified the reuse
//
// `#0381` recorded a limit rather than building, because the mechanism under
// discussion there — widening every scoped check.sh run to include seven
// slow packages, forever — carried a real, recurring cost. This guard is a
// different shape: it is one more assertion layered onto a walk this
// package already performs. extractNamedSections, issueCitationSectionHeaders,
// issueStatus and the `os.ReadDir` of the issues directory (see the sibling
// file above) all predate this issue and are unmodified by it, so the
// marginal cost is one extra pattern match and exemption check per already-
// extracted section, not a new traversal. See this issue's `## Verification`
// for the measured added runtime.
//
// # The exemption, and why it is not the section split above
//
// Inside a scanned section, a citation is exempt only when its own
// immediate text proves it is a verbatim quotation of real command output
// rather than an assertion about where something lives — the same kind of
// distinction #0356's own transcript exemption draws for Go comments, built
// from properties of the text at the match site rather than from a list.
// Two independent, structural predicates, either one sufficient alone:
//
//  1. A second colon-number pair immediately after the citation. That is
//     exactly and only how go build, go vet and gofmt -l format every
//     diagnostic they print — an ordinary citation never carries a third
//     field, so a second one appearing is the toolchain's own signature, not
//     a choice a prose citation could accidentally make. This issue's own
//     survey found the real, live instance this predicate exists for — see
//     `## Verification` — so the guard is proved against real bytes, not
//     only a synthetic fixture.
//  2. A go test -v output marker anywhere in the same fenced code block as
//     the citation — the markdown analogue of #0356's own "enclosing
//     comment GROUP" scope, for the identical reason: a captured
//     transcript's file:line message and its own marker line are printed by
//     the tool in the tool's own output order, which does not put the
//     marker immediately adjacent to the citation. A first draft of this
//     predicate reused the sibling Test-name-citation guard's fixed
//     lookback window above and failed against real corpus content
//     (issues/0389.md's own `## Verification`, whose transcript prints the
//     citation's file:line message BEFORE the marker line it belongs
//     to — the real go test -v ordering, not the reverse). Scanning the
//     whole enclosing fenced block is what the real shape requires; see
//     `## Verification` for the corpus hit this predicate was corrected
//     against.
//
// Neither predicate is a list of names or files, and neither can be
// satisfied by choosing a heading to write under — proving that is
// criterion 3's discrimination test and criterion 2's load-bearing proof in
// this issue, both exercised below.
//
// # The section split is reused, not invented, and its residual is named
//
// A correcting quotation — an issue discussing another issue's bad
// citation, exactly as this issue's own Description does — is not caught by
// either predicate above; it is caught by staying inside the same
// evidence/subject-matter section split #0268 already measured and tested
// for the sibling guard above (issueCitationSectionHeaders): a citation in
// Description, Notes, Plan or Acceptance criteria is never scanned by this
// guard either, on the same reasoning #0268 gives for why those sections
// quote and propose rather than assert.
//
// This is a location-based signal, and this issue's own review is right to
// name that as a residual: nothing stops a future citation from being
// written under an unscanned heading to dodge this guard. It is accepted
// rather than closed, for three reasons measured rather than assumed.
// First, it is not a new exemption invented for this issue — it is the same
// split #0268 already shipped, tested
// (TestIssueCitationGuardScansEvidenceSectionsNotSubjectMatter, above) and
// has carried since without being gamed. Second, the real corpus this pass
// measured — every open or in-progress issue file at the time this guard
// was written — carries zero citations moved into an unscanned section to
// survive; the citations that do live in Description in this very issue are
// corrective, in the same file that documents why the guard exists, exactly
// the legitimate use #0268's own motivating case (#0126's false citations,
// quoted as subject matter) already established for those sections. Third,
// and stated plainly rather than argued away: a section split is a
// convention-level control, not a cryptographic one, and #0356's own
// transcript exemption carries an equivalent, explicitly accepted blind
// spot — a fabricated citation dressed as real test output is
// indistinguishable from a genuine one, by that file's own account. This
// guard's convention-not-truth honesty applies here exactly as it does
// there: a green run means no non-exempt citation was found in a scanned
// section, never that every citation anywhere in the file is accurate.
var issueLineCitationPattern = regexp.MustCompile(`\b[A-Za-z0-9_][A-Za-z0-9_./\-]*\.go:[0-9]+\b`)

// issueLineCitationDiagnosticSuffixPattern recognizes the second colon-number
// pair go build, go vet and gofmt -l append to every diagnostic they print —
// the toolchain's own signature for "this is real captured output", distinct
// from an ordinary citation, which carries only one colon-number pair.
var issueLineCitationDiagnosticSuffixPattern = regexp.MustCompile(`^:[0-9]+:`)

// issueLineCitationTestMarkerPattern recognizes go test -v's own three
// output-line markers, scanned for anywhere inside the fenced code block
// enclosing a citation rather than in a fixed window immediately before or
// after it — see the file-level comment's account of why a fixed window is
// the wrong shape for this predicate.
var issueLineCitationTestMarkerPattern = regexp.MustCompile(`(?m)^\s*(?:===\s+RUN\b|---\s+(?:FAIL|PASS|SKIP):)`)

// issueLineCitationGuardMinPlausibleFileCount is this guard's fail-closed
// floor on the issues directory read below — CLAUDE.md §8's "a check that
// cannot fail pins nothing" applied to this guard's own directory read, so
// an emptied or misdirected issues/ read is a hard failure rather than a
// silent, narrowed "clean". 389 numbered issue files exist under issues/ at
// the time this guard was written; comfortably below that and well above
// zero.
const issueLineCitationGuardMinPlausibleFileCount = 300

// issueLineCitationGuardScanIsImplausible reports whether mdFileCount is too
// low to trust an empty failures list from scanIssueDirForLineCitations
// against the real issues directory. Kept as its own pure function, rather
// than inlined at the call site, so the floor's boundary can be pinned
// directly (TestIssueLineCitationGuardScanIsImplausibleBelowFloor) without
// needing a real, thinned-out issues directory to prove it.
func issueLineCitationGuardScanIsImplausible(mdFileCount int) bool {
	return mdFileCount < issueLineCitationGuardMinPlausibleFileCount
}

// issueLineCitationExcluded reports whether the citation at text[start:end]
// should be discounted as a verbatim quotation of real command output. See
// the file-level comment for what each predicate is for and why it is
// structural rather than a list.
func issueLineCitationExcluded(text string, start, end int) bool {
	if issueLineCitationDiagnosticSuffixPattern.MatchString(text[end:]) {
		return true
	}
	return issueLineCitationFencedBlockContainsTestMarker(text, start)
}

// issueLineCitationFenceLinePattern recognizes a markdown fence delimiter
// line-anchored (#0391): a fence is a line whose first non-space run is
// three or more backtick characters, matched per line rather than by
// indexing every raw three-backtick substring in the section text. That is
// what keeps a bare mention of a fence in the middle of an ordinary
// sentence — never alone on its own line — from being counted as a fence
// at all. See issueLineCitationFencedBlockContainsTestMarker's doc comment
// for why the raw-substring approach this replaces was wrong, not merely
// less precise.
var issueLineCitationFenceLinePattern = regexp.MustCompile(`(?m)^[ \t]*` + "`{3,}")

// issueLineCitationFencedBlockContainsTestMarker reports whether the fenced
// code block enclosing the byte offset start also contains, anywhere in
// that same block, a go test -v output marker line. Start outside every
// fence pair (including before the first fence in the section) reports
// false — a citation outside a fenced block gets no benefit from this
// predicate, matching predicate 1's own text-only-what-you-can-prove
// discipline.
//
// #0391 rewrote this function's fence detection. #0384's original version
// collected every raw three-backtick substring offset anywhere in the
// section text and paired them positionally — first with second, third
// with fourth — regardless of what those backticks actually were. An odd
// total count then silently shifted every later pair, two different ways:
// a stray fence-shaped run sitting mid-sentence in ordinary prose (not on
// its own line) could pair with a real fence and turn the prose between
// them into a bogus "block" — which could exempt a stale citation sitting
// in that prose if a marker-shaped line happened to sit there too, a
// fail-open; and a genuinely unterminated final fence (the block's closing
// fence never arrives before the section ends) was simply dropped, since
// the pairing loop stops rather than treating a lone trailing fence as
// open — which false-flagged a real, uncut transcript's own citation. This
// function fixes both: fences are recognized line-anchored via
// issueLineCitationFenceLinePattern, so a fence-shaped run embedded in
// prose is never counted, and an unterminated final fence is treated as
// open through the end of the section rather than as unpaired and
// dropped. See TestIssueLineCitationFencePairingClosesFailOpen and
// TestIssueLineCitationFencePairingStopsFalseFlaggingUnterminatedTranscript
// below for both defects reproduced against the pre-#0391 pairing
// (preserved as preFix0391FencedBlockContainsTestMarker, test-only) and
// closed here.
//
// This is a parsing correction, not a new exemption: it changes what
// counts as a fence, not what an exemption is willing to forgive. Two
// residuals remain, and they are different in kind rather than one
// shared cause: this predicate infers a semantic property — that the
// text around a citation is real captured output — from purely
// syntactic ones, in two independent places. The forged-marker residual
// the file-level comment above already discloses — wrapping a citation
// in a fence carrying a fabricated marker line still exempts it — is
// unchanged and still open; pairing is correct in that case, and this
// function does not, and cannot, tell a genuine transcript from a
// fabricated one. The second (#0395) is a pairing limit instead:
// pairing stays positional over the fence lines it finds and does not
// consider fence length, so a nested fence of a different length is
// paired as though it were an ordinary delimiter. A four-backtick
// wrapper around one complete three-backtick block is harmless — the
// shifted pairs are subranges of the block a length-aware parse would
// produce, so the verdict is unchanged. A wrapper enclosing an odd
// number of inner fence lines is not: it shifts every pair after it,
// so the wrapper's own closing fence is itself read as an opener and
// sweeps prose sitting outside every real block into a pseudo-block,
// which a marker line anywhere inside then exempts where a
// length-aware parse would not. That pseudo-block is bounded by the
// next fence line when one follows, and runs unterminated to the end
// of the section when the wrapper's close is the last fence. Neither
// form is reached by #0391's line-anchoring, whose subject is fence
// shape rather than fence length; the unterminated form is in fact
// reachable only because #0391 treats a lone trailing fence as an open
// block rather than dropping it, which is the right call for a
// genuinely uncut transcript and the wrong one for a wrapper's close
// misread as an opener. issues/0092.md carries that unterminated form,
// a four-backtick wrapper around five three-backtick lines whose close
// is the last fence in its section, and it is resolved, so nothing in
// scope is affected today. Accepted rather than closed: teaching
// pairing about fence length would be exactly the second mechanism
// #0391's criterion 6 already declined to add.
func issueLineCitationFencedBlockContainsTestMarker(text string, start int) bool {
	fenceLines := issueLineCitationFenceLinePattern.FindAllStringIndex(text, -1)
	for i := 0; i < len(fenceLines); i += 2 {
		open := fenceLines[i][0]
		blockEnd := len(text) // unterminated final fence runs to end of section (#0391)
		if i+1 < len(fenceLines) {
			blockEnd = issueLineCitationLineEnd(text, fenceLines[i+1][1])
		}
		if start >= open && start <= blockEnd {
			return issueLineCitationTestMarkerPattern.MatchString(text[open:blockEnd])
		}
	}
	return false
}

// issueLineCitationLineEnd returns the offset of the end of the line
// containing byte offset from — the position of the following newline, or
// len(text) when from's line is the last line in text and carries no
// trailing newline.
func issueLineCitationLineEnd(text string, from int) int {
	if idx := strings.IndexByte(text[from:], '\n'); idx >= 0 {
		return from + idx
	}
	return len(text)
}

// scanIssueDirForLineCitations resolves every `<file>.go:<int>` citation in
// dir's *.md files' evidence-asserting sections — the same
// issueCitationSectionHeaders set the sibling Test-name-citation guard
// scans, including everything nested under them — restricted to files whose
// `**Status**` row satisfies includeStatus. It returns one formatted
// "path:line: citation" string per non-exempt hit, sorted for a stable,
// pasteable report, and the number of *.md files it read, so a caller
// pointed at the real issues directory can assert that count plausible
// before trusting an empty result.
func scanIssueDirForLineCitations(t *testing.T, dir string, includeStatus func(status string) bool) (failures []string, mdFileCount int) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		mdFileCount++
		path := filepath.Join(dir, entry.Name())
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			t.Fatalf("read %s: %v", path, rerr)
		}
		text := string(raw)
		if !includeStatus(issueStatus(text)) {
			continue
		}
		for _, section := range extractNamedSections(text) {
			for _, loc := range issueLineCitationPattern.FindAllStringIndex(section.text, -1) {
				if issueLineCitationExcluded(section.text, loc[0], loc[1]) {
					continue
				}
				cited := section.text[loc[0]:loc[1]]
				line := section.startLine + strings.Count(section.text[:loc[0]], "\n")
				failures = append(failures, path+":"+itoa(line)+": "+cited)
			}
		}
	}
	sort.Strings(failures)
	return failures, mdFileCount
}

// TestNoOpenIssueEvidenceCitesGoFileByLineNumber is this guard's real-corpus
// run: every open or in-progress issues/NNNN.md file's evidence-asserting
// sections must cite a Go source location by stable identifier, never by a
// bare position, unless issueLineCitationExcluded's structural exemption
// holds. See the file-level comment for the two decisions this guard makes
// and why; resolved/closed/wontfix files are out of scope for the same
// reason the sibling Test-name-citation guard gives.
func TestNoOpenIssueEvidenceCitesGoFileByLineNumber(t *testing.T) {
	failures, mdFileCount := scanIssueDirForLineCitations(t, "../../issues", func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if issueLineCitationGuardScanIsImplausible(mdFileCount) {
		t.Fatalf("scanned only %d issues/*.md files, want at least %d — a narrowed or emptied issues directory must not be read as a clean bill of health", mdFileCount, issueLineCitationGuardMinPlausibleFileCount)
	}
	if len(failures) > 0 {
		t.Fatalf("issue file cites a Go source location by line number, which drifts silently on any unrelated edit to the cited file — replace with a stable identifier, not a corrected number (#0352, #0356, #0384):\n  %s",
			strings.Join(failures, "\n  "))
	}
}

// TestIssueLineCitationGuardScanIsImplausibleBelowFloor pins
// issueLineCitationGuardScanIsImplausible's boundary directly, per CLAUDE.md
// §8's "a check that cannot fail pins nothing": zero files, and one short of
// the floor, must both be implausible; the floor itself must not be.
func TestIssueLineCitationGuardScanIsImplausibleBelowFloor(t *testing.T) {
	if !issueLineCitationGuardScanIsImplausible(0) {
		t.Errorf("0 files must be implausible")
	}
	if !issueLineCitationGuardScanIsImplausible(issueLineCitationGuardMinPlausibleFileCount - 1) {
		t.Errorf("one below the floor must be implausible")
	}
	if issueLineCitationGuardScanIsImplausible(issueLineCitationGuardMinPlausibleFileCount) {
		t.Errorf("the floor itself must not be implausible")
	}
}

// TestIssueLineCitationGuardCatchesPlantedStaleCitationAndSparesCorrectingQuotation
// is criterion 3's discrimination proof, run against an isolated t.TempDir()
// fixture (CLAUDE.md §8a: never plant a failure into a shared file another
// agent may be mid-edit on). It asserts all three things #0384 requires at
// once: a citation in an evidence-asserting section fires and names the
// file and line; a correcting quotation of the same shape in Description —
// discussing a citation as subject matter, exactly as this issue's own
// Description does — does not; and replacing the flagged citation with a
// stable identifier clears the guard.
func TestIssueLineCitationGuardCatchesPlantedStaleCitationAndSparesCorrectingQuotation(t *testing.T) {
	dir := t.TempDir()
	const verificationLine = "The check lives at `store.go:8888`, confirmed by reading the file."
	planted := "# 9996 — scratch fixture for #0384's mutation proof\n\n" +
		"| | |\n|---|---|\n| **Status** | open |\n\n" +
		"## Description\n\nQuoting a bad citation as subject matter, exactly as #0384 itself does: `handlers.go:9999` was cited and was wrong.\n\n" +
		"## Verification\n\n" + verificationLine + "\n"
	fixturePath := filepath.Join(dir, "9996.md")
	if err := os.WriteFile(fixturePath, []byte(planted), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	failures, _ := scanIssueDirForLineCitations(t, dir, func(status string) bool { return status == "open" })
	if len(failures) != 1 {
		t.Fatalf("expected exactly one non-exempt citation (the Description quotation must not fire), got %d: %v", len(failures), failures)
	}
	if !strings.Contains(failures[0], "store.go:8888") {
		t.Fatalf("expected the failure to name the Verification-section citation, got %q", failures[0])
	}
	if !strings.Contains(failures[0], "9996.md:") {
		t.Fatalf("expected the failure to point into 9996.md, got %q", failures[0])
	}

	corrected := strings.Replace(planted, verificationLine,
		"The check lives in Store.LoadByBasename, confirmed by reading the file.", 1)
	if err := os.WriteFile(fixturePath, []byte(corrected), 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	failures, _ = scanIssueDirForLineCitations(t, dir, func(status string) bool { return status == "open" })
	if len(failures) != 0 {
		t.Fatalf("expected clean after replacing the citation with a stable identifier, got %v", failures)
	}
}

// TestIssueLineCitationGuardExcludesResolvedFilesByDefault mirrors
// TestIssueVerificationCitationGuardExcludesResolvedFilesByDefault above:
// the status filter, not the section split, is what keeps a resolved
// issue's own historical citations — #0377's and #0379's among them — out
// of scope. A fixture with a non-exempt citation and Status resolved is not
// reported when includeStatus only admits open/in-progress; the identical
// fixture with Status changed to open is.
func TestIssueLineCitationGuardExcludesResolvedFilesByDefault(t *testing.T) {
	dir := t.TempDir()
	body := "## Verification\n\nSee `resolved_only.go:321` for the check.\n"

	resolved := "# 9995 — scratch fixture\n\n| | |\n|---|---|\n| **Status** | resolved |\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "9995.md"), []byte(resolved), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	failures, _ := scanIssueDirForLineCitations(t, dir, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if len(failures) != 0 {
		t.Fatalf("resolved-status fixture should be out of scope, got %v", failures)
	}

	open := "# 9995 — scratch fixture\n\n| | |\n|---|---|\n| **Status** | open |\n\n" + body
	if err := os.WriteFile(filepath.Join(dir, "9995.md"), []byte(open), 0o600); err != nil {
		t.Fatalf("rewrite fixture: %v", err)
	}
	failures, _ = scanIssueDirForLineCitations(t, dir, func(status string) bool {
		return status == "open" || status == "in-progress"
	})
	if len(failures) != 1 {
		t.Fatalf("open-status fixture with the same non-exempt citation should be in scope, got %v", failures)
	}
}

// TestIssueLineCitationExcludedPredicatesAreEachIndependentlySufficient is
// criterion 2's load-bearing proof. Unlike #0356's AND-of-three for Go
// comments, this exemption is an OR of two predicates describing two
// genuinely different real artifacts — compiler/vet output and go test -v
// output — so "disabling one in turn" here means proving the OTHER alone
// still carries the exemption when the first is absent, and that a citation
// satisfying NEITHER is not excluded at all.
func TestIssueLineCitationExcludedPredicatesAreEachIndependentlySufficient(t *testing.T) {
	find := func(text, needle string) (int, int) {
		i := strings.Index(text, needle)
		if i < 0 {
			t.Fatalf("fixture %q missing %q", text, needle)
		}
		return i, i + len(needle)
	}

	t.Run("predicate 1 alone (compiler diagnostic triple, no test marker nearby)", func(t *testing.T) {
		text := "reported a build failure from `bad.go:42:7: undefined: x`, a real compiler diagnostic"
		s, e := find(text, "bad.go:42")
		if !issueLineCitationExcluded(text, s, e) {
			t.Errorf("a citation immediately followed by a second colon-number pair must be excluded")
		}
	})

	t.Run("predicate 2 alone (go test marker elsewhere in the same fenced block, no diagnostic triple)", func(t *testing.T) {
		text := "before the block\n\n```\n=== RUN   TestSomething\n    helper.go:99: assertion failed\n--- FAIL: TestSomething (0.00s)\n```\n\nafter the block"
		s, e := find(text, "helper.go:99")
		if !issueLineCitationExcluded(text, s, e) {
			t.Errorf("a citation inside a fenced block that also contains a go test -v marker line must be excluded")
		}
	})

	t.Run("neither predicate — the guard's whole value, must not be excluded", func(t *testing.T) {
		text := "the check lives at `plain.go:17`, cited by line rather than name"
		s, e := find(text, "plain.go:17")
		if issueLineCitationExcluded(text, s, e) {
			t.Errorf("a plain citation satisfying neither predicate must NOT be excluded")
		}
	})

	t.Run("predicate 1 disabled by construction: same text with the second colon-number pair removed is no longer excluded", func(t *testing.T) {
		text := "reported a build failure from `bad.go:42`, no longer a diagnostic triple"
		s, e := find(text, "bad.go:42")
		if issueLineCitationExcluded(text, s, e) {
			t.Errorf("removing the second colon-number pair must remove the exclusion")
		}
	})

	t.Run("predicate 2 disabled by construction: same marker text present, but the citation sits outside any fenced block", func(t *testing.T) {
		text := "=== RUN   TestSomething, noted here, but the citation `helper.go:99` sits in plain unfenced prose, not inside any ``` block at all"
		s, e := find(text, "helper.go:99")
		if issueLineCitationExcluded(text, s, e) {
			t.Errorf("a marker present in the text but outside any fenced block enclosing the citation must not exclude it")
		}
	})

	t.Run("predicate 2 disabled by construction: citation is inside a fenced block, but that block carries no marker line", func(t *testing.T) {
		text := "```\nsome ordinary quoted text mentioning helper.go:99 with no test-output marker anywhere in this block\n```"
		s, e := find(text, "helper.go:99")
		if issueLineCitationExcluded(text, s, e) {
			t.Errorf("a fenced block with no marker line must not exclude a citation inside it")
		}
	})
}

// TestIssueLineCitationPatternMatchesRealGoVetDiagnosticShape proves
// predicate 1 against the shape of real, live corpus content rather than
// only a hand-built fixture: `go vet`'s own diagnostic format is a
// repo-relative Go source path, then a colon, a line number, a second
// colon, a column number and a third colon, then the message — and this
// test constructs that exact shape (without asserting anything about a
// specific issue file's current, editable text, which would itself be a
// citation this guard would have to police) and confirms both that
// issueLineCitationPattern matches only the file:line portion and that
// issueLineCitationExcluded discounts it.
func TestIssueLineCitationPatternMatchesRealGoVetDiagnosticShape(t *testing.T) {
	text := "`internal/subscribers/suppressions.go:344:65: undefined:\nnow`"
	loc := issueLineCitationPattern.FindStringIndex(text)
	if loc == nil {
		t.Fatalf("pattern did not match a real go vet diagnostic shape in %q", text)
	}
	if got := text[loc[0]:loc[1]]; got != "internal/subscribers/suppressions.go:344" {
		t.Errorf("expected the match to stop before the column field, got %q", got)
	}
	if !issueLineCitationExcluded(text, loc[0], loc[1]) {
		t.Errorf("a real go vet diagnostic triple must be excluded by predicate 1")
	}
}

// preFix0391FencedBlockContainsTestMarker is #0384's original
// implementation of issueLineCitationFencedBlockContainsTestMarker,
// preserved byte-for-byte (renamed only) so #0391's two demonstrations
// below exercise the exact bug they fixed rather than a description of
// it. Test-only: nothing in the production exemption path calls this.
// It collects every raw three-backtick substring offset anywhere in text
// and pairs the resulting offsets positionally — first with second, third
// with fourth — regardless of whether a given occurrence sits alone on
// its own line or in the middle of a sentence, and regardless of whether
// the final occurrence has a partner at all.
func preFix0391FencedBlockContainsTestMarker(text string, start int) bool {
	var fenceOffsets []int
	for i := 0; i+3 <= len(text); {
		idx := strings.Index(text[i:], "```")
		if idx < 0 {
			break
		}
		fenceOffsets = append(fenceOffsets, i+idx)
		i = i + idx + 3
	}
	for i := 0; i+1 < len(fenceOffsets); i += 2 {
		open, close := fenceOffsets[i], fenceOffsets[i+1]
		if start >= open && start <= close {
			return issueLineCitationTestMarkerPattern.MatchString(text[open:close])
		}
	}
	return false
}

// TestIssueLineCitationFencePairingClosesFailOpen is criterion 2's
// deliverable: the serious half of #0391. It constructs a section with an
// odd total of raw three-backtick occurrences — one a stray mention in the
// middle of an ordinary sentence, never alone on its own line, and two
// forming the section's one real fenced block — where a stale citation and
// a marker-shaped line both sit in the plain prose between the stray
// mention and the real block. #0384's original positional pairing
// (preFix0391FencedBlockContainsTestMarker) pairs the stray mid-sentence
// mention with the real block's opening fence, so the resulting "block"
// spans that stretch of ordinary prose — and wrongly exempts the stale
// citation inside it, because the prose happens to contain a
// marker-shaped line too. #0391's line-anchored fix never counts the
// mid-sentence mention as a fence at all, so no block covers that prose,
// and the same citation is correctly not exempted.
func TestIssueLineCitationFencePairingClosesFailOpen(t *testing.T) {
	text := "See the fence delimiter ``` mentioned here, not a real block start.\n\n" +
		"The check lives at `store.go:8888`, confirmed by reading the file.\n" +
		"--- PASS: a decoy marker-shaped line sitting in plain prose here, not inside any real block\n\n" +
		"Later, an unrelated real fenced block:\n\n" +
		"```\n" +
		"genuine, unrelated content — the one real fenced block in this fixture\n" +
		"```\n"
	start := strings.Index(text, "store.go:8888")
	if start < 0 {
		t.Fatalf("fixture missing the planted citation")
	}

	if !preFix0391FencedBlockContainsTestMarker(text, start) {
		t.Fatalf("expected #0384's original positional pairing to (wrongly) exempt the planted citation via the shifted pseudo-block — the fail-open this issue exists to close is not reproduced")
	}
	if issueLineCitationFencedBlockContainsTestMarker(text, start) {
		t.Fatalf("expected #0391's line-anchored fix to leave the planted citation un-exempted instead of pairing the stray mid-sentence mention into a block")
	}
}

// TestIssueLineCitationFencePairingStopsFalseFlaggingUnterminatedTranscript
// is criterion 3's deliverable: the other half of #0391. It constructs a
// section with an odd total fence count from a different, equally real
// cause — a second, genuine block whose closing fence never arrives before
// the section text ends (extractNamedSections truncates a section at the
// next level-2 heading, so a block that runs past that boundary is
// unterminated from this function's point of view). #0384's original
// pairing drops that trailing, unpaired fence on the floor — the loop
// simply stops rather than treating it as open — so the transcript's own
// citation gets no benefit from predicate 2 and is wrongly flagged.
// #0391's fix treats the unterminated final fence as open through the end
// of the section, so the same citation is correctly exempted.
func TestIssueLineCitationFencePairingStopsFalseFlaggingUnterminatedTranscript(t *testing.T) {
	text := "Some intro.\n\n" +
		"```\n" +
		"irrelevant content, no marker in this first block\n" +
		"```\n\n" +
		"Later, a genuine but unterminated transcript:\n\n" +
		"```\n" +
		"=== RUN   TestSomething\n" +
		"    helper.go:99: assertion failed\n" +
		"--- FAIL: TestSomething (0.00s)\n"
	start := strings.Index(text, "helper.go:99")
	if start < 0 {
		t.Fatalf("fixture missing the planted citation")
	}

	if preFix0391FencedBlockContainsTestMarker(text, start) {
		t.Fatalf("expected #0384's original positional pairing to (wrongly) NOT exempt the transcript's own citation — the false-flag this issue exists to close is not reproduced")
	}
	if !issueLineCitationFencedBlockContainsTestMarker(text, start) {
		t.Fatalf("expected #0391's fix to exempt the transcript's citation via the unterminated-fence-runs-to-end-of-section rule")
	}
}
