package handlers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// #0459: TestNoCommentCitesUnresolvedPathOrSection (citation_target_guard_test.go)
// checks that a cited repository PATH resolves — it does not, and by design
// cannot, check that a CLAIM about that path's CONTENTS is true. #0456 is
// the real defect this leaves open: two doc comments in
// internal/subscribers/store.go named an earlier migration for two CHECK
// constraints and five columns that actually live in a later one, and the
// path-resolution guard passed on both, because the earlier migration file
// genuinely exists — it just doesn't contain what the comment claimed.
//
// The scoping this file implements is #0456's own reviewer's proposal, and
// per this issue's own framing the scoping is the substance, not a caveat:
// for a comment naming both a "migrations/NNNNNN" file and a constraint or
// column identifier, assert that identifier appears in that migration's SQL
// — checkable because a migration file's contents are SQL (an identifier
// either appears or it doesn't), unlike a prose claim about what a PRD.md
// section means (#0438's parallel gap, deliberately NOT this file's job —
// see #0176, wontfix, on the regex-versus-parser cost of exactly that
// heavier kind of check). Scoped to the "migrations/NNNNNN" shorthand form
// #0456's own two citations used, not a fully-qualified migration filename
// with its extension: TestNoCommentCitesUnresolvedPathOrSection already
// resolves that shape by ordinary path lookup, and a citation naming an
// actual file is exactly the case that guard already covers.
//
// Two extraction paths feed one candidate set, both earned by real
// instances found while dry-running this guard over the whole tree (not
// invented in the abstract):
//
//  1. A literal snake_case token. This repo's constraint and column names
//     are uniformly snake_case at the database — never camelCase, the same
//     observation audit_email_metadata_guard_test.go's own comment makes
//     about Metadata keys — so a bare regex over the comment text finds
//     real constraint names ("subscribers_source_check") and column names
//     ("consent_basis") with no conversion needed. Scoped to the text
//     between the nearest preceding sentence/clause boundary (a ".", ":",
//     or ";") and the citation itself, not the whole comment group: a dry
//     run using the whole group's text produced real false positives —
//     unrelated snake_case words from an earlier sentence in the same
//     paragraph, a different clause entirely, being checked against a
//     migration they were never a claim about. Bounding to the current
//     clause is what a human reader would treat as "what this citation is
//     about", and is what closes that false-positive shape.
//  2. A struct field's own Go name, converted to snake_case. #0456's SECOND
//     real citation named Go field identifiers with no snake_case text
//     anywhere in the comment at all, so path 1 alone cannot catch it —
//     see TestMigrationCitationGuardCatchesBothRealHistoricalCases below
//     for the reconstructed proof. This repo's struct-field-to-column
//     naming is a reliable straight conversion, verified against the real
//     migration SQL while writing this guard for three separate fields on
//     Subscriber. This path is deliberately narrowed to ONLY a struct
//     field's own doc comment citing a migration, using that field's own
//     declared name — never a free-floating package, function, const, or
//     var doc, and never every name a field-group comment happens to
//     mention in prose. That narrowing is load-bearing, not incidental: one
//     real comment in this tree reads roughly "settingQueueMaxRetries is
//     migrations/000021's seeded settings row", and
//     "settingQueueMaxRetries" converts to "setting_queue_max_retries",
//     which that migration does NOT contain — the actual seeded key is
//     "queue_max_retries", one Go-naming-convention prefix short of the
//     constant's own name, because a const documents the KEY IT HOLDS, not
//     a column sharing its own name. A struct field is different: this
//     codebase's Subscriber fields really do name their own column 1:1.
//     Applying path 2 to const/var docs as well was tried during this
//     guard's dry run and produced exactly that false positive; scoping it
//     to struct fields only is what removes it. If a future struct field's
//     name ever legitimately diverges from its column (a wrapper type, a
//     computed value), this path will need a per-instance exception the
//     same shape as citationIsExcluded's nonTestIdentifierAllowlist — none
//     exists in this tree today.
//
// Deliberately NOT covered, all accepted gaps rather than false-positive
// risks (per CLAUDE.md §8's #0258 history: a missed detection is the safe
// side to err on, a false alarm is not):
//
//   - The alternate "migration NNNNNN" (no slash) shorthand this tree also
//     uses in a couple of places — #0459 itself, and every real motivating
//     citation, use the slash form.
//   - A citation whose migration number this guard's own tree walk cannot
//     resolve to a file under migrations/ at all is treated as "identifier
//     absent" (any candidate is flagged) rather than skipped — the same
//     "resolve against ground truth, don't assume" posture
//     TestNoCommentCitesUnresolvedPathOrSection already takes for a path.
//   - Any identifier candidate found in one comment is checked ONLY against
//     the migration number(s) cited in the SAME comment group — never
//     against "any migration in the tree", which would just be
//     TestNoCommentCitesUnresolvedPathOrSection's own path-existence check
//     restated with extra steps and would defeat the whole point of #0456
//     (the wrong number was itself a real, existing migration).
//   - Candidates are collected only from text PRECEDING a citation — the
//     idiomatic "migrations/000023's `source` column" ordering, identifier
//     after the citation, is not checked. A naive forward window was tried
//     during this guard's dry run and produced 5 false positives
//     (internal/db/prd_index_parity_test.go, internal/mailing/worker_store.go,
//     internal/subscribers/erase.go, internal/sesnotify/store.go, and this
//     guard's own doc comments), so backward-only is deliberate, not an
//     oversight.
//
// Same technique as #0196/#0220/#0265/#0267 throughout (go/ast comment
// walk, a regexp naming the candidate shape, exclusion rules earned by a
// real hit on this tree, a repo-relative pasteable failure message) —
// deliberately not a new design. Reuses citedTestScanRoots,
// citedTestScanRootsMinPlausibleFileCount, walkGoFiles,
// assertGoFileVisitCountPlausible, stripCommentMarkers, and
// toRepoRelativePath from dangling_test_citation_guard_test.go (same
// package, same file set to scan, same "_test.go files included"
// reasoning #0196 already established) rather than declaring second
// copies; that file was clean in git status as this one was written.
//
// Self-scan note (the #0392 shape): this file is itself walked by its own
// guard below, and by TestNoCommentCitesUnresolvedPathOrSection. Every
// concrete example above names the FICTITIOUS Go identifier this guard's
// dry run produced ("setting_queue_max_retries" vs. the real
// "queue_max_retries") rather than restating #0456's two real,
// already-corrected citations as a literal wrong-number/right-identifier
// pair in a real comment — doing that here would reproduce the exact
// defect shape this guard exists to catch, against itself. Where this
// guard's own reasoning needs the real #0456 numbers, it names ONLY the
// current, correct one (migrations/000023, which really does contain
// subscribers_source_check, subscribers_consent_basis_check, and the five
// provenance columns — true today and checked by this guard's own
// tree-wide run), never the stale 000010 paired with any of those
// identifiers. The synthetic proof tests below build their fictitious or
// historical-defect source text from Go string concatenation for the same
// reason #0220's and #0267's synthetic tests do: so the fixture itself
// never becomes a live comment token this file's own parser would visit.

// migrationCitationPattern matches the "migrations/NNNNNN" shorthand this
// tree's real citations use (see #0456's own two, both of that exact
// shape). No trailing \b: RE2 treats "_" as a word character, so a citation
// immediately followed by the rest of a full filename
// ("migrations/000009_create_interests.up.sql") would otherwise fail to
// match at all — this pattern only needs the six digits, not what follows
// them. \s* between the slash and the digits tolerates the one real
// instance in this tree where a citation wraps across a comment line break
// (internal/subscribers/store.go's "reads migrations/\n// 000023's CHECK
// constraint" — RE2's \s already matches "\n", so no extra flag is needed).
var migrationCitationPattern = regexp.MustCompile(`\bmigrations/\s*(\d{6})`)

// migrationCitationSnakeTokenPattern recognizes a bare snake_case
// identifier — at least two segments, each lowercase alphanumeric, joined
// by "_". Requiring at least one underscore is deliberate, mirroring
// metadataKeyIsSuspectedEmailCarrier's own reasoning in
// audit_email_metadata_guard_test.go: it is what keeps an ordinary single
// lowercase English word ("settings", "constraint") out of the candidate
// set, since every real constraint or column name a dry run of this tree
// found is multi-segment.
var migrationCitationSnakeTokenPattern = regexp.MustCompile(`\b[a-z][a-z0-9]*(?:_[a-z0-9]+)+\b`)

// migrationCitationNegationMarkers, checked in the 30 characters
// immediately preceding a snake-token candidate (not the whole clause —
// the marker needs to sit right next to the thing it negates), discounts a
// candidate the surrounding prose explicitly denies rather than claims,
// mirroring citationTargetHistoricalMarkers' own shape in
// citation_target_guard_test.go: a narrow, structural exclusion earned by
// one real hit, not a general negation parser. The real hit is this
// package's own audit_test.go, which cites a migration as evidence that a
// particular foreign key does NOT exist, not as a claim that migration
// contains the table on the other end of that absent key —
// TestMigrationCitationNegationMarkerExcludesRealAuditTestShape below
// reconstructs the exact shape as a fixture and proves it clean, rather
// than this comment quoting the live text (which would repeat the pairing
// enough times to trip this very guard against itself — the #0392 shape).
var migrationCitationNegationMarkers = []string{"no ", "not ", "never ", "n't ", "without "}

func migrationCitationCandidateIsNegated(text string, start int) bool {
	windowStart := start - 30
	if windowStart < 0 {
		windowStart = 0
	}
	lower := strings.ToLower(text[windowStart:start])
	for _, marker := range migrationCitationNegationMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// migrationCitationAcronymBoundary and migrationCitationCaseBoundary
// together convert a Go PascalCase identifier to snake_case, acronym-aware
// (so "ImportID" becomes "import_id", not "import_i_d"). Verified directly
// against the real migration SQL, not assumed — see
// TestGoFieldNameToSnakeCaseMatchesRealSubscriberFields below for the pinned
// conversions and the specific column each one checks against, one
// migration citation per sentence so this doc comment's own prose stays
// something this guard's tree-wide run can check without tripping itself
// (the #0392 shape a mixed sentence naming two migrations for one shared
// list of names would otherwise produce).
var (
	migrationCitationAcronymBoundary = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	migrationCitationCaseBoundary    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// goFieldNameToSnakeCase performs that conversion.
func goFieldNameToSnakeCase(name string) string {
	s := migrationCitationAcronymBoundary.ReplaceAllString(name, "${1}_${2}")
	s = migrationCitationCaseBoundary.ReplaceAllString(s, "${1}_${2}")
	return strings.ToLower(s)
}

// migrationCitationSentenceBoundary returns the index just past the
// nearest ".", ":", or ";" in text[:upto], or 0 if none exists — the left
// edge of "the current clause", used to bound path 1's snake-token
// extraction to prose that could plausibly be about the citation at upto,
// rather than the whole (possibly many-sentence) comment group.
func migrationCitationSentenceBoundary(text string, upto int) int {
	for i := upto - 1; i >= 0; i-- {
		switch text[i] {
		case '.', ':', ';':
			return i + 1
		}
	}
	return 0
}

// migrationCitationSnakeCandidate is one snake_case token found in a
// comment's joined text, already filtered for the truncation/filename
// exclusions collectMigrationCitationCandidates' callers rely on.
type migrationCitationSnakeCandidate struct {
	start, end int
	token      string
}

// collectMigrationCitationSnakeCandidates finds every
// migrationCitationSnakeTokenPattern match in text, excluding a negated
// candidate (see migrationCitationCandidateIsNegated).
func collectMigrationCitationSnakeCandidates(text string) []migrationCitationSnakeCandidate {
	var out []migrationCitationSnakeCandidate
	for _, m := range migrationCitationSnakeTokenPattern.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		if migrationCitationCandidateIsNegated(text, start) {
			continue
		}
		out = append(out, migrationCitationSnakeCandidate{start: start, end: end, token: text[start:end]})
	}
	return out
}

// migrationCitationFieldDoc pairs a struct field's own leading doc comment
// with that field's declared name — path 2's ground truth. Only fields
// with exactly one name are considered (this tree declares none any other
// way), and only a TypeSpec whose underlying type is a struct — an
// interface method or a non-struct type's field-shaped syntax (there is
// none in Go) is out of scope by construction.
type migrationCitationFieldDoc struct {
	doc  *ast.CommentGroup
	name string
}

// collectMigrationCitationFieldDocs walks file's top-level type
// declarations for struct fields carrying their own doc comment —
// deliberately NOT reusing misattached_doc_comment_guard_test.go's
// collectDeclDocs, which is scoped to FuncDecl/ValueSpec/TypeSpec doc
// comments and does not descend into a struct's field list at all (a
// different, and for that guard's purpose correct, scope — #0267 is about a
// declaration's OWN doc landing on the wrong declaration, not about a
// field's doc citing a migration).
func collectMigrationCitationFieldDocs(file *ast.File) []migrationCitationFieldDoc {
	var out []migrationCitationFieldDoc
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok || st.Fields == nil {
				continue
			}
			for _, field := range st.Fields.List {
				if field.Doc == nil || len(field.Names) != 1 {
					continue
				}
				out = append(out, migrationCitationFieldDoc{doc: field.Doc, name: field.Names[0].Name})
			}
		}
	}
	return out
}

// migrationCitationIsWordByte reports whether b can appear inside an
// identifier-shaped word — used by migrationContentHasIdentifier to
// implement a "\b...\b" check without compiling one regexp per identifier.
// Underscore counts (matching RE2's own \b semantics, which is what makes
// "\bsource\b" correctly NOT match inside "unsubscribe_source" — the
// substring trap this guard's design specifically avoids: migrations/000010
// contains "unsubscribe_source" and "utm_source" but no standalone
// "source" token, and a bare strings.Contains check would have missed that
// distinction and silently passed the #0456 defect this guard exists to
// catch).
func migrationCitationIsWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// migrationContentHasIdentifier reports whether identifier appears in
// content as a whole word — not merely as a substring of some longer
// identifier.
func migrationContentHasIdentifier(content, identifier string) bool {
	if identifier == "" {
		return false
	}
	from := 0
	for {
		i := strings.Index(content[from:], identifier)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(identifier)
		beforeOK := start == 0 || !migrationCitationIsWordByte(content[start-1])
		afterOK := end == len(content) || !migrationCitationIsWordByte(content[end])
		if beforeOK && afterOK {
			return true
		}
		from = start + 1
	}
}

// migrationCitationNumberPattern is the ground truth for
// loadMigrationContents: golang-migrate's own "NNNNNN_name.up|down.sql"
// naming convention (CLAUDE.md's "Migrations" table, migrations/ itself).
var migrationCitationNumberPattern = regexp.MustCompile(`^(\d{6})_`)

// loadMigrationContents reads every file directly under repoRoot/migrations
// (both .up.sql and .down.sql — an identifier a migration DROPS is exactly
// as much "in that migration" as one it CREATEs) and returns the
// concatenated content keyed by migration number, fresh off disk every run
// — the same "never hardcode what the tree itself can answer" posture
// loadClaudeMDSections (citation_target_guard_test.go) takes for CLAUDE.md's
// own headings.
func loadMigrationContents(t *testing.T, repoRoot string) map[string]string {
	t.Helper()
	dir := filepath.Join(repoRoot, "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}
	content := map[string]string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationCitationNumberPattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			t.Fatalf("read %s: %v", e.Name(), rerr)
		}
		content[m[1]] += string(data) + "\n"
	}
	// #0459 criterion 3, applied to this guard's OTHER ground truth: an
	// empty migrations/ read (a moved directory, a filter that matched
	// nothing) must fail loudly, not be silently read as "no migration
	// ever contains any cited identifier".
	if len(content) == 0 {
		t.Fatal("loadMigrationContents: found zero numbered migration files under migrations/ — the directory may have moved, or migrationCitationNumberPattern may no longer match its naming convention (#0459 criterion 3)")
	}
	return content
}

// migrationCitationHit is one confirmed defect: a comment cites migration
// for identifier, and that migration's own SQL (from migContent) does not
// contain identifier as a whole word.
type migrationCitationHit struct {
	pos        token.Position
	migration  string
	identifier string
}

// scanFileForMigrationCitationHits is the single-file half of the guard,
// factored out from collectMigrationCitationContentHits so
// TestMigrationCitationGuardCatchesBothRealHistoricalCases can call it
// directly on a reconstructed pre-fix copy of one file without re-walking
// the whole tree. Returns the hits found, plus the number of
// "migrations/NNNNNN" citations and the number of individual
// identifier-against-migration checks performed — the raw counts
// TestMigrationCitationIdentifierMatchesContent totals across every file
// and checks against a floor per #0459 criterion 3.
func scanFileForMigrationCitationHits(fset *token.FileSet, file *ast.File, migContent map[string]string) (hits []migrationCitationHit, citationCount, identifierCheckCount int) {
	fieldDocs := map[*ast.CommentGroup]string{}
	for _, fd := range collectMigrationCitationFieldDocs(file) {
		fieldDocs[fd.doc] = fd.name
	}

	for _, group := range file.Comments {
		// Join the comment group's lines into one text blob with a
		// position lookup, exactly mirroring
		// collectCitationTargetHits's own construction
		// (citation_target_guard_test.go) — same reasoning: a real
		// citation's excluding context can sit on the line before it
		// (that file's own "e.g." example), and this guard's
		// sentence-boundary scan needs the same cross-line text to
		// find a "." that may itself be on an earlier physical line.
		var lines []string
		var positions []token.Position
		for _, c := range group.List {
			base := fset.Position(c.Slash)
			stripped := stripCommentMarkers(c.Text)
			for j, part := range strings.Split(stripped, "\n") {
				pos := base
				pos.Line += j
				lines = append(lines, part)
				positions = append(positions, pos)
			}
		}
		type span struct {
			start int
			pos   token.Position
		}
		var b strings.Builder
		spans := make([]span, 0, len(lines))
		for i, line := range lines {
			spans = append(spans, span{start: b.Len(), pos: positions[i]})
			b.WriteString(line)
			b.WriteByte('\n')
		}
		text := b.String()

		posAt := func(offset int) token.Position {
			sp := spans[0]
			for _, s := range spans {
				if s.start > offset {
					break
				}
				sp = s
			}
			return sp.pos
		}

		migMatches := migrationCitationPattern.FindAllStringSubmatchIndex(text, -1)
		if len(migMatches) == 0 {
			continue
		}
		fieldName, isFieldDoc := fieldDocs[group]
		snakeCandidates := collectMigrationCitationSnakeCandidates(text)

		for _, m := range migMatches {
			wholeStart, wholeEnd, numStart, numEnd := m[0], m[1], m[2], m[3]
			if numEnd < len(text) && text[numEnd] >= '0' && text[numEnd] <= '9' {
				// A seventh digit follows — not this repo's
				// fixed-width convention (migrations/ has none
				// today); skip rather than mis-truncate to six.
				continue
			}
			num := text[numStart:numEnd]
			citationCount++

			candidates := map[string]bool{}
			lo := migrationCitationSentenceBoundary(text, wholeStart)
			for _, sc := range snakeCandidates {
				if sc.start >= lo && sc.end <= wholeStart {
					candidates[sc.token] = true
				}
			}
			if isFieldDoc {
				candidates[goFieldNameToSnakeCase(fieldName)] = true
			}
			if len(candidates) == 0 {
				continue
			}

			content, known := migContent[num]
			for ident := range candidates {
				identifierCheckCount++
				if !known || !migrationContentHasIdentifier(content, ident) {
					hits = append(hits, migrationCitationHit{pos: posAt(wholeEnd), migration: num, identifier: ident})
				}
			}
		}
	}
	return hits, citationCount, identifierCheckCount
}

// collectMigrationCitationContentHits is the tree-wide walk, reusing
// walkGoFiles exactly as citation_target_guard_test.go's
// collectCitationTargetHits does.
func collectMigrationCitationContentHits(t *testing.T, roots []string, migContent map[string]string) (hits []migrationCitationHit, citationCount, identifierCheckCount, visited int) {
	t.Helper()
	visited = walkGoFiles(t, roots, func(path string) {
		fset := token.NewFileSet()
		file, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if perr != nil {
			t.Fatalf("parse %s: %v", path, perr)
		}
		fileHits, fileCitations, fileChecks := scanFileForMigrationCitationHits(fset, file, migContent)
		hits = append(hits, fileHits...)
		citationCount += fileCitations
		identifierCheckCount += fileChecks
	})
	return hits, citationCount, identifierCheckCount, visited
}

// migrationCitationMinPlausibleCitationCount and
// migrationCitationMinPlausibleIdentifierCount are the #0459 criterion 3
// floors: measured directly by running TestMigrationCitationIdentifierMatchesContent
// while writing this guard (73 "migrations/NNNNNN" citations and 34
// identifier-against-migration checks across internal/ and cmd/, this
// guard's own file included), set comfortably below that so the tree can
// shrink without a false alarm, while still tripping if citedTestScanRoots
// were emptied or narrowed (which would report 0 for both) or if the
// identifier-extraction logic itself were disabled (which would report
// citationCount > 0 but identifierCheckCount == 0 — the subtler fail-open
// TestMigrationCitationExtractionGuardFiresOnEmptyOrLowCount proves
// separately, and the one #0258/#0428 actually shipped: a scan that
// matches comments but silently extracts nothing from them).
const (
	migrationCitationMinPlausibleCitationCount   = 40
	migrationCitationMinPlausibleIdentifierCount = 15
)

// migrationCitationExtractionImplausible is the pure #0459-criterion-3
// check, deliberately separated from TestMigrationCitationIdentifierMatchesContent
// the same way goFileVisitCountImplausible (dangling_test_citation_guard_test.go)
// is separated from assertGoFileVisitCountPlausible — so it can be tested
// directly against synthetic counts without needing a real *testing.T to
// stand in for a synthetic failure.
func migrationCitationExtractionImplausible(citationCount, identifierCheckCount int) string {
	if citationCount < migrationCitationMinPlausibleCitationCount {
		return fmt.Sprintf("only found %d migrations/NNNNNN citation(s) — expected at least %d; the scan roots may have been emptied or narrowed, or migrationCitationPattern itself may be broken, rather than the tree containing fewer citations (#0459 criterion 3)", citationCount, migrationCitationMinPlausibleCitationCount)
	}
	if identifierCheckCount < migrationCitationMinPlausibleIdentifierCount {
		return fmt.Sprintf("found %d migrations/NNNNNN citation(s) but only extracted %d identifier candidate(s) from them — expected at least %d; the identifier-extraction logic may be silently matching comments and finding nothing in them, the exact #0258/#0428 fail-open shape, rather than the tree citing fewer identifiers (#0459 criterion 3)", citationCount, identifierCheckCount, migrationCitationMinPlausibleIdentifierCount)
	}
	return ""
}

// TestMigrationCitationExtractionGuardFiresOnEmptyOrLowCount is #0459
// criterion 3's direct proof, mirroring
// TestGoFileVisitCountGuardFiresOnEmptyOrLowCount's own shape and reasoning
// in dangling_test_citation_guard_test.go: the oracle here is not a copy of
// this guard's own floor constants, so a change to either floor cannot make
// this test agree with itself regardless of whether
// migrationCitationExtractionImplausible still works. Proves BOTH fail-open
// shapes this issue names: zero citations found at all, and citations found
// but zero identifiers extracted from them.
func TestMigrationCitationExtractionGuardFiresOnEmptyOrLowCount(t *testing.T) {
	if reason := migrationCitationExtractionImplausible(0, 0); reason == "" {
		t.Fatal("expected zero citations to be reported implausible, got no reason")
	}
	if reason := migrationCitationExtractionImplausible(500, 0); reason == "" {
		t.Fatal("expected real citations paired with zero extracted identifiers to be reported implausible, got no reason")
	}
	if reason := migrationCitationExtractionImplausible(migrationCitationMinPlausibleCitationCount, migrationCitationMinPlausibleIdentifierCount); reason != "" {
		t.Fatalf("expected counts meeting both floors to be reported plausible, got: %s", reason)
	}
}

// TestMigrationCitationIdentifierMatchesContent is the guard: it fails if
// any Go comment anywhere in the tree (test files included) names both a
// "migrations/NNNNNN" file and a constraint or column identifier (by
// either extraction path above) that migration's own SQL does not contain.
func TestMigrationCitationIdentifierMatchesContent(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(baseDir, "..", "..")

	var roots []string
	for _, rel := range citedTestScanRoots {
		roots = append(roots, filepath.Join(baseDir, rel))
	}

	migContent := loadMigrationContents(t, repoRoot)
	hits, citationCount, identifierCheckCount, visited := collectMigrationCitationContentHits(t, roots, migContent)

	assertGoFileVisitCountPlausible(t, "TestMigrationCitationIdentifierMatchesContent", citedTestScanRoots, visited, citedTestScanRootsMinPlausibleFileCount)
	if reason := migrationCitationExtractionImplausible(citationCount, identifierCheckCount); reason != "" {
		t.Fatal("TestMigrationCitationIdentifierMatchesContent: " + reason)
	}

	if len(hits) == 0 {
		return
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].pos.Filename != hits[j].pos.Filename {
			return hits[i].pos.Filename < hits[j].pos.Filename
		}
		return hits[i].pos.Line < hits[j].pos.Line
	})
	var b strings.Builder
	b.WriteString("comment cites migrations/NNNNNN for a constraint or column identifier that migration does not contain — fix the citation or the migration number (#0456, #0459):\n")
	for _, h := range hits {
		fmt.Fprintf(&b, "  %s:%d: %q not found in migrations/%s\n", toRepoRelativePath(repoRoot, h.pos.Filename), h.pos.Line, h.identifier, h.migration)
	}
	t.Error(b.String())
}

// TestMigrationCitationNegationMarkerExcludesRealAuditTestShape
// reconstructs, as a Go string (never a live comment this file's own guard
// would visit), the real shape in this package's audit_test.go that
// motivated migrationCitationNegationMarkers: a citation offered as
// evidence a foreign key does NOT exist, naming the table on the other end
// of that absent key. Uses the real migration number (000005) and the real
// table name (email_campaigns) from that comment, and confirms both that
// the negation exclusion actually fires (zero hits) and that it is not
// simply "this identifier never gets flagged" — the same text with the
// negation phrase removed DOES produce a hit, proving the marker, not the
// fixture, is what's doing the work.
func TestMigrationCitationNegationMarkerExcludesRealAuditTestShape(t *testing.T) {
	table := "email" + "_campaigns"
	negatedSrc := "package fixture\n\n" +
		"// campaignID is a bare value in the unconstrained target_id column\n" +
		"// (no FK to " + table + " — see migrations/" + "000005" + ") so there is\n" +
		"// no seeded row to collide with.\n" +
		"func Example() {}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", negatedSrc, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse negated fixture: %v", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)

	if migrationContentHasIdentifier(migContent["000005"], table) {
		t.Fatalf("fixture assumption failed: migrations/000005 unexpectedly contains %q", table)
	}

	hits, citationCount, _ := scanFileForMigrationCitationHits(fset, file, migContent)
	if citationCount != 1 {
		t.Fatalf("expected exactly one migration citation, got %d", citationCount)
	}
	if len(hits) != 0 {
		t.Errorf("negation marker did not exclude the real audit_test.go shape: got hits %+v", hits)
	}

	// Control: the identical text with the negation phrase removed must
	// still hit — proving the marker itself is load-bearing, not that
	// "email_campaigns" is unconditionally excluded some other way.
	unnegatedSrc := strings.Replace(negatedSrc, "no FK to ", "matches ", 1)
	fset2 := token.NewFileSet()
	file2, err2 := parser.ParseFile(fset2, "fixture.go", unnegatedSrc, parser.ParseComments)
	if err2 != nil {
		t.Fatalf("parse control fixture: %v", err2)
	}
	controlHits, controlCitations, controlChecks := scanFileForMigrationCitationHits(fset2, file2, migContent)
	if controlCitations != 1 || controlChecks == 0 {
		t.Fatalf("control fixture: expected 1 citation with >0 identifier checks, got citations=%d checks=%d", controlCitations, controlChecks)
	}
	if len(controlHits) != 1 || controlHits[0].identifier != table {
		t.Fatalf("control fixture: expected exactly one hit naming %q, got %+v", table, controlHits)
	}
}

// TestMigrationCitationSnakeTokenExtractionCatchesSyntheticExample proves
// path 1 (bare snake_case token, clause-bounded) actually fires, against a
// fabricated source string rather than a live citation — mirroring
// TestCitationTargetPathPatternCatchesSyntheticExample's own reasoning in
// citation_target_guard_test.go. Built from string concatenation using a
// real migration number this repo has (000001) paired with a column name
// that migration does not define, never a bare literal token, so this
// fixture does not itself become a citation this file's OWN guard above
// would see when it scans this file's real comments (the #0392 shape).
func TestMigrationCitationSnakeTokenExtractionCatchesSyntheticExample(t *testing.T) {
	fictitiousColumn := "totally_fictitious_column" + "_never_created"
	src := "package fixture\n\n" +
		"// matches the " + fictitiousColumn + " CHECK constraint (migrations/" + "000001" + ").\n" +
		"func Example() {}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)

	if migrationContentHasIdentifier(migContent["000001"], fictitiousColumn) {
		t.Fatalf("fixture setup: %q unexpectedly appears in migrations/000001 — pick a different fictitious name", fictitiousColumn)
	}

	hits, citationCount, identifierCheckCount := scanFileForMigrationCitationHits(fset, file, migContent)
	if citationCount != 1 {
		t.Fatalf("expected exactly one migration citation, got %d", citationCount)
	}
	if identifierCheckCount != 1 {
		t.Fatalf("expected exactly one identifier check, got %d", identifierCheckCount)
	}
	if len(hits) != 1 || hits[0].migration != "000001" || hits[0].identifier != fictitiousColumn {
		t.Fatalf("expected one hit naming migration 000001 and identifier %q, got %+v", fictitiousColumn, hits)
	}
}

// TestMigrationCitationFieldNameConversionCatchesSyntheticExample proves
// path 2 (a struct field's own name, converted to snake_case) fires when
// NO snake_case text appears in the comment at all — the exact shape
// #0456's second real citation was, and the shape path 1 alone cannot see.
// The fixture's field name and migration citation are both built so this
// source parses as a real struct field doc, but only as a Go string
// handed to parser.ParseFile, never as a literal comment token in this
// file's own source (the #0392 shape again).
func TestMigrationCitationFieldNameConversionCatchesSyntheticExample(t *testing.T) {
	fieldName := "Fictitious" + "ProvenanceColumn"
	src := "package fixture\n\n" +
		"type Widget struct {\n" +
		"\t// " + fieldName + " is migrations/" + "000001" + "'s marker column.\n" +
		"\t" + fieldName + " string\n" +
		"}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)

	wantSnake := goFieldNameToSnakeCase(fieldName)
	if wantSnake != "fictitious_provenance_column" {
		t.Fatalf("fixture setup: conversion produced %q, expected \"fictitious_provenance_column\" — fixture no longer proves what it claims", wantSnake)
	}
	if migrationContentHasIdentifier(migContent["000001"], wantSnake) {
		t.Fatalf("fixture setup: %q unexpectedly appears in migrations/000001 — pick a different fictitious name", wantSnake)
	}

	fieldDocs := collectMigrationCitationFieldDocs(file)
	if len(fieldDocs) != 1 || fieldDocs[0].name != fieldName {
		t.Fatalf("expected collectMigrationCitationFieldDocs to find exactly one field doc named %q, got %+v", fieldName, fieldDocs)
	}

	hits, citationCount, identifierCheckCount := scanFileForMigrationCitationHits(fset, file, migContent)
	if citationCount != 1 {
		t.Fatalf("expected exactly one migration citation, got %d", citationCount)
	}
	if identifierCheckCount != 1 {
		t.Fatalf("expected exactly one identifier check, got %d", identifierCheckCount)
	}
	if len(hits) != 1 || hits[0].migration != "000001" || hits[0].identifier != wantSnake {
		t.Fatalf("expected one hit naming migration 000001 and identifier %q, got %+v", wantSnake, hits)
	}
}

// TestGoFieldNameToSnakeCaseMatchesRealSubscriberFields pins the converter
// against the three real Subscriber struct fields this guard's path 2
// actually depends on, each verified directly against the real migration
// SQL (not against this guard's own report) while writing this file.
// Source converts to "source", which migrations/000023 declares with its
// own "ADD COLUMN source TEXT NOT NULL" statement. ImportID is the
// acronym-boundary case — "ID" has no following lowercase letter for the
// naive camel-split to key off — and converts to "import_id", also
// migrations/000023's own. InviteResentAt converts to "invite_resent_at",
// which is migrations/000026's "ADD COLUMN invite_resent_at TIMESTAMPTZ"
// instead — a separate sentence for a separate migration on purpose, per
// this file's own package-doc note on the #0392 shape.
func TestGoFieldNameToSnakeCaseMatchesRealSubscriberFields(t *testing.T) {
	cases := map[string]string{
		"Source":         "source",
		"ImportID":       "import_id",
		"InviteResentAt": "invite_resent_at",
		"SourceDetail":   "source_detail",
		"ConsentBasis":   "consent_basis",
		"InvitedAt":      "invited_at",
	}
	for goName, want := range cases {
		if got := goFieldNameToSnakeCase(goName); got != want {
			t.Errorf("goFieldNameToSnakeCase(%q) = %q, want %q", goName, got, want)
		}
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)
	for _, ident := range []string{"source", "source_detail", "consent_basis", "import_id", "invited_at"} {
		if !migrationContentHasIdentifier(migContent["000023"], ident) {
			t.Errorf("migrations/000023 unexpectedly does not contain %q — this guard's own #0456 proof depends on it", ident)
		}
	}
	if !migrationContentHasIdentifier(migContent["000026"], "invite_resent_at") {
		t.Error("migrations/000026 unexpectedly does not contain \"invite_resent_at\" — this guard's own #0456 proof depends on it")
	}
}

// TestMigrationContentHasIdentifierRejectsSubstringMatch proves the
// whole-word check the #0456 defect actually depends on: migrations/000010
// contains "unsubscribe_source" and "utm_source" but never a standalone
// "source" token, so a plain strings.Contains check would have wrongly
// reported "source" as present there and silently passed the historical
// defect this guard exists to catch.
func TestMigrationContentHasIdentifierRejectsSubstringMatch(t *testing.T) {
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)

	if !strings.Contains(migContent["000010"], "unsubscribe_source") {
		t.Fatal("fixture assumption failed: migrations/000010 no longer contains \"unsubscribe_source\"")
	}
	if migrationContentHasIdentifier(migContent["000010"], "source") {
		t.Error(`migrationContentHasIdentifier(migrations/000010, "source") = true, want false — "source" appears only as a substring of "unsubscribe_source"/"utm_source" there, never as its own token`)
	}
	if !migrationContentHasIdentifier(migContent["000023"], "source") {
		t.Error(`migrationContentHasIdentifier(migrations/000023, "source") = false, want true — migrations/000023 declares a standalone "source" column`)
	}
}

// TestMigrationCitationGuardCatchesBothRealHistoricalCases is #0459
// criterion 2's proof: it reconstructs the EXACT pre-fix text of both
// comments #0456 corrected in internal/subscribers/store.go (commit
// a8e0986, both citing migrations/000010 for constraints/columns that
// migrations/000023 actually creates) as an in-memory Go source string —
// never by checking out or modifying the real, current
// internal/subscribers/store.go, and never via git stash (CLAUDE.md §8a)
// — and confirms this guard fires on both. A guard that cannot catch the
// case that motivated it would be decoration.
func TestMigrationCitationGuardCatchesBothRealHistoricalCases(t *testing.T) {
	// Reconstructed — abridged — from `git show a8e0986^:internal/subscribers/store.go`
	// (read directly, not from #0456's own report), trimmed to the two
	// doc comments and just enough surrounding declaration syntax for
	// each to parse and, in the second case, for the comment to attach
	// as the Source field's own Doc. The field comment's third line is a
	// paraphrase of the real text rather than a trim of it — inert here
	// (candidates are only taken from text preceding a citation, per this
	// file's "Deliberately NOT covered" list above), but "abridged" says
	// so rather than overclaiming exactness.
	src := "package fixture\n\n" +
		"// Provenance values (#0125, PRD §6.10), matching the subscribers_source_check\n" +
		"// and subscribers_consent_basis_check CHECK constraints (migrations/000010).\n" +
		"const (\n" +
		"\tSubscriberSourceSignupForm = \"signup_form\"\n" +
		")\n\n" +
		"type Subscriber struct {\n" +
		"\tUnsubscribeSource string\n" +
		"\t// Source, SourceDetail, ConsentBasis, ImportID, InvitedAt are #0125's\n" +
		"\t// provenance columns (migrations/000010, PRD §6.10): every address must\n" +
		"\t// be able to answer where this came from.\n" +
		"\tSource       string\n" +
		"\tSourceDetail *string\n" +
		"\tConsentBasis *string\n" +
		"\tImportID     int64\n" +
		"\tInvitedAt    string\n" +
		"}\n"

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "store.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse reconstructed pre-fix fixture: %v", err)
	}
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Join(repoRoot, "..", "..")
	migContent := loadMigrationContents(t, repoRoot)

	hits, citationCount, identifierCheckCount := scanFileForMigrationCitationHits(fset, file, migContent)
	if citationCount != 2 {
		t.Fatalf("expected exactly 2 migration citations (one per #0456 comment), got %d: %+v", citationCount, hits)
	}
	if identifierCheckCount == 0 {
		t.Fatal("expected at least one identifier check per citation, got zero — extraction itself is broken, not the fixture")
	}

	foundConstraintHit := false
	foundFieldHit := false
	for _, h := range hits {
		if h.migration != "000010" {
			t.Errorf("unexpected hit against migration %q, want \"000010\": %+v", h.migration, h)
			continue
		}
		switch h.identifier {
		case "subscribers_source_check", "subscribers_consent_basis_check":
			foundConstraintHit = true
		case "source":
			foundFieldHit = true
		}
	}
	if !foundConstraintHit {
		t.Errorf("expected a hit for the CHECK-constraint comment (subscribers_source_check or subscribers_consent_basis_check against migrations/000010), got %+v", hits)
	}
	if !foundFieldHit {
		t.Errorf(`expected a hit for the struct-field comment (the Source field's own name converting to "source", checked against migrations/000010), got %+v`, hits)
	}

	// And the control: the SAME two comments, corrected to the real
	// migration number, must produce NO hits — proving this is a
	// genuine before/after distinction, not a fixture that always fires.
	fixedSrc := strings.ReplaceAll(src, "migrations/000010", "migrations/000023")
	fixedFset := token.NewFileSet()
	fixedFile, ferr := parser.ParseFile(fixedFset, "store.go", fixedSrc, parser.ParseComments)
	if ferr != nil {
		t.Fatalf("parse corrected fixture: %v", ferr)
	}
	fixedHits, fixedCitations, fixedChecks := scanFileForMigrationCitationHits(fixedFset, fixedFile, migContent)
	if fixedCitations != 2 || fixedChecks == 0 {
		t.Fatalf("corrected fixture: expected 2 citations and >0 identifier checks, got citations=%d checks=%d", fixedCitations, fixedChecks)
	}
	if len(fixedHits) != 0 {
		t.Errorf("corrected fixture (migrations/000023) unexpectedly still hits: %+v", fixedHits)
	}
}
