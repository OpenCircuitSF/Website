package handlers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// #0237: the privacy policy's "How to leave" erasure list (#0226's own
// guard, PrivacyPolicy.guard.test.ts) pins the SET OF CATEGORIES the page
// promises against a hardcoded fixture, checked with requiredPhrases. That
// caught drift in the page's own wording, but it has no way to notice that
// the underlying Go code writes a subscriber's email address into
// audit_log.metadata at more places than #0226 checked: #0226 examined five
// subscriber-driven audit.Entry call sites (signup, confirmation,
// unsubscribe, preference update, erasure) and found only two of them
// (confirmation, erasure) actually carry the email — the other three carry
// `kind`/`source`/`interest_count` metadata, not the address. This file
// widens that to five MORE real sites — admin-initiated (manual add,
// suppression removal) and SES-driven (two bounce paths, one complaint) —
// bringing the confirmed total to seven, because nothing was keyed to the
// actual SET OF CALL SITES that write an email. This file is that guard: it
// walks every audit.Entry{...} composite literal in production Go source
// (internal/ and cmd/; _test.go files are excluded because a test fixture
// never becomes a real inserted row) and classifies whether its Metadata
// could carry an email-shaped value under any of a fixed set of suspect key
// tokens (see metadataKeyIsSuspectedEmailCarrier — not just the literal key
// "email"; #0237's own phase-3 review found that exact-match blind to
// {"recipient_email": addr}), resolving inline map literals, the "built the
// map in a local variable, sometimes with a conditional index assignment"
// shape several real call sites use, and the query-string-shaped free-text
// case (see classifyMetadataExpr). The result is compared against
// auditEmailMetadataKnownSites below; drift in EITHER direction (a new site
// appears, a known site disappears, or a site's IP-presence changes — the
// other half of what the privacy page claims about this same set) fails
// with the exact file:line, so the fix is never "reword the policy and
// hope" — it is "read this failure, update this fixture to match the tree,
// and update the policy to match the fixture."
//
// A Metadata expression this guard cannot classify statically (a call to
// anything other than the one known-safe local helper below, a value built
// outside the enclosing function, etc.) is treated as a FAILURE, not a
// silent pass — the conservative direction, since an unresolvable site could
// be hiding an email the guard would otherwise miss entirely. Widening what
// this guard can resolve should always be a deliberate, reviewed change to
// this file, not a byproduct of the guard giving up quietly.
//
// One narrow, checked exception to that blanket rule: a composite-literal
// key that is not itself a string literal (an identifier constant, an
// integer, any other expression) is silently SKIPPED by
// compositeLitStringKeys rather than flagged unresolved — #0237's second
// phase-3 review ran this directly (map[string]any{emailKeyConst: addr})
// and got hasEmail=false, unresolved="", not a failure. Every Metadata key
// literal actually written in this tree today is a plain double-quoted
// string (verified while writing this guard, and again by the census
// behind the token widening above), so this exception is unencountered in
// practice, not exercised — narrowing the promise to say so is the fix
// here, since no real site needs the code changed and doing so would add
// complexity for a shape nothing in the tree uses.

// auditEmailMetadataScanRoots is deliberately narrower than
// citedTestScanRoots (#0196/#0220's comment-citation guards): the privacy
// policy's claim is about what internal/ and cmd/ ACTUALLY WRITE to
// audit_log in production, so web/ (no Go source at all) is out of scope by
// construction, and this guard's own walk additionally skips _test.go files
// (see skipGoTestFile below) that citedTestScanRoots's consumers
// deliberately include.
var auditEmailMetadataScanRoots = []string{"..", "../../cmd"}

// auditEmailMetadataKnownSafeHelpers names same-package, no-receiver Go
// functions this guard will resolve BY CALL, rather than requiring the
// Metadata value to be an inline literal or a traced local variable. Each
// name here is still checked at test time (classifyMetadataExpr calls
// classifyHelperReturnsForEmail) against the helper's OWN return statement,
// not merely trusted — this list
// only says WHICH calls get attempted resolution, not that they
// automatically pass. `credentialMetadata` (internal/auth/registration.go)
// is the only real call site of this shape today: it builds
// {"device_name": ..., "aaguid": ...} for a WebAuthn credential, never a
// subscriber's email.
var auditEmailMetadataKnownSafeHelpers = map[string]bool{
	"credentialMetadata": true,
}

// auditEmailMetadataKnownSite is one audit.Entry{} call site whose Metadata
// this guard has determined DOES carry an email-shaped value under one of
// metadataKeyIsSuspectedEmailCarrier's recognized keys — the tree-wide
// ground truth this test pins and re-derives every run. Identified
// by (repo-relative file, Action constant name) rather than by line number:
// a line number shifts on any unrelated edit above it in the same file,
// which would fail this guard for a reason that has nothing to do with what
// it exists to catch — an edit to an unrelated function earlier in the same
// file. `count` disambiguates the one action name (ActionSubscriberBounced)
// that legitimately appears twice in the same file, for two different
// bounce-severity branches (internal/handlers/ses_notifications.go:443 and
// :503).
type auditEmailMetadataKnownSite struct {
	file   string
	action string
	count  int
	hasIP  bool // whether the occurrence(s) set an IP field — checked per-action against foundIP in TestAuditEntryEmailMetadataMatchesKnownSites
}

// auditEmailMetadataKnownSites: verified directly against the tree by
// reading every audit.Entry{ construction in internal/ and cmd/ (issues/0237.md's
// own acceptance criterion), not copied from that issue's table. Each row's
// `hasIP` was checked the same way: does this action's audit.Entry literal
// carry an `IP:` key at all.
//   - confirm.go: ActionSubscriberConfirmed — IP set (clientIP(r))
//   - admin_subscribers.go: ActionSubscriberManualAdd — IP set (the acting admin's)
//   - admin_subscribers.go: ActionSubscriberErased — IP set (the acting admin's)
//   - admin_suppressions.go: ActionSuppressionRemoved — IP set (the acting admin's)
//   - ses_notifications.go: ActionSubscriberBounced (×2, permanent + repeated-soft) — no IP (no request; the event is Amazon's)
//   - ses_notifications.go: ActionSubscriberComplained — no IP (same reason)
//
// The six above are the SUBSCRIBER'S OWN address, and are what
// PrivacyPolicy.svelte's audit-log item discloses by name (#0237's original
// scope). Two more sites are pinned below because
// metadataKeyIsSuspectedEmailCarrier — widened after #0237's own phase-3
// review found the old exact-"email"-key match blind to
// {"recipient_email": addr} — now correctly flags them, but NEITHER is a
// real subscriber's own address, so pinning them here is not the same as
// disclosing them the same way:
//   - admin_campaign_preview.go: ActionEmailCampaignTestSent — IP set (the
//     acting admin's). Metadata carries "to": actor.Email (the ADMIN's own
//     address, sending a test message to themselves) and "recipient_email":
//     sub.Email, where sub is h.ensureTestRecipient's synthetic
//     campaign-test+admin-<id>@<ReservedTestEmailDomain> address — never a
//     real subscriber (traced by #0237's phase-3 review). Pinned so a
//     future edit that starts writing a REAL subscriber's address here is
//     caught; the privacy page does not name this site, because it never
//     touches subscriber data.
//   - admin_subscribers_export.go: ActionSubscriberExported — IP set (the
//     acting admin's). Metadata carries "filter_query": query, the raw text
//     of an admin's search box when running a CSV export of the list —
//     unbounded free text this guard cannot read, so if an admin happened
//     to search BY a subscriber's address, that address lands in this row.
//     Unlike the test-send site above, this one CAN carry a real
//     subscriber's own address, so the privacy page's audit-log item does
//     disclose it (as a possibility contingent on what the admin typed, not
//     a certainty like the six structural sites above).
var auditEmailMetadataKnownSites = []auditEmailMetadataKnownSite{
	{file: "internal/handlers/confirm.go", action: "ActionSubscriberConfirmed", count: 1, hasIP: true},
	{file: "internal/handlers/admin_subscribers.go", action: "ActionSubscriberManualAdd", count: 1, hasIP: true},
	{file: "internal/handlers/admin_subscribers.go", action: "ActionSubscriberErased", count: 1, hasIP: true},
	{file: "internal/handlers/admin_suppressions.go", action: "ActionSuppressionRemoved", count: 1, hasIP: true},
	{file: "internal/handlers/ses_notifications.go", action: "ActionSubscriberBounced", count: 2, hasIP: false},
	{file: "internal/handlers/ses_notifications.go", action: "ActionSubscriberComplained", count: 1, hasIP: false},
	{file: "internal/handlers/admin_campaign_preview.go", action: "ActionEmailCampaignTestSent", count: 1, hasIP: true},
	{file: "internal/handlers/admin_subscribers_export.go", action: "ActionSubscriberExported", count: 1, hasIP: true},
}

// auditEntrySite is one audit.Entry{...} composite literal found by the
// walk below, plus what this guard determined about it.
type auditEntrySite struct {
	file       string // repo-relative
	line       int
	action     string // best-effort text of the Action field's value; "" if absent
	hasEmail   bool
	hasIP      bool
	unresolved string // non-empty when Metadata could not be classified statically; the guard fails and names this reason
}

// skipGoTestFile reports whether path should be excluded from the
// production-only walk this guard needs — a _test.go file's audit.Entry
// literals are fixtures, never a row the running service actually inserts,
// so they carry no privacy-disclosure obligation.
func skipGoTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go")
}

// funcKey identifies a top-level (no-receiver) function by its containing
// directory (Go's own package boundary within this repo's layout) and name,
// for resolving a same-package helper CALL in Metadata (see
// classifyMetadataExpr's *ast.CallExpr case).
type funcKey struct {
	dir  string
	name string
}

// scannedGoFile pairs a parsed file with the token.FileSet needed to turn
// its positions into line numbers.
type scannedGoFile struct {
	path string
	file *ast.File
	fset *token.FileSet
}

// scanAuditEntrySites walks every production .go file under roots, finds
// every audit.Entry{...} composite literal, and classifies its Metadata.
// Two passes are needed: the first collects every top-level helper function
// (for resolving a Metadata: someHelper(...) call) and parses every file
// once; the second walks each parsed file's audit.Entry sites, tracing a
// bare identifier Metadata value back through its ENCLOSING function's
// assignments (both `x := map[string]any{...}` and `x["email"] = ...`,
// wherever either appears in that function, regardless of which branch —
// the conservative, correct reading for "could this call site ever write
// this key", not "does it on every code path").
func scanAuditEntrySites(t *testing.T, roots []string) []auditEntrySite {
	t.Helper()

	helperFuncs := map[funcKey]*ast.FuncDecl{}
	var files []scannedGoFile

	walkGoFiles(t, roots, func(path string) {
		if skipGoTestFile(path) {
			return
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		files = append(files, scannedGoFile{path: path, file: f, fset: fset})
		dir := filepath.Dir(path)
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}
			helperFuncs[funcKey{dir: dir, name: fn.Name.Name}] = fn
		}
	})

	var sites []auditEntrySite
	for _, sf := range files {
		dir := filepath.Dir(sf.path)
		ast.Inspect(sf.file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				return true
			}
			localKeys := collectLocalMapKeys(fn.Body)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok || !isAuditEntryType(cl.Type) {
					return true
				}
				pos := sf.fset.Position(cl.Pos())
				site := auditEntrySite{file: sf.path, line: pos.Line}
				for _, elt := range cl.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}
					key, ok := kv.Key.(*ast.Ident)
					if !ok {
						continue
					}
					switch key.Name {
					case "Action":
						site.action = exprText(kv.Value)
					case "IP":
						site.hasIP = true
					case "Metadata":
						site.hasEmail, site.unresolved = classifyMetadataExpr(kv.Value, dir, localKeys, helperFuncs)
					}
				}
				sites = append(sites, site)
				return true
			})
			return true
		})
	}
	return sites
}

// isAuditEntryType reports whether t is the type expression "audit.Entry".
// Every production file in this tree imports the audit package unaliased
// (verified: `grep -rn '"github.com/brennanMKE/OpenCircuitSF/internal/audit"'`
// shows no `x "..."` alias form anywhere), so matching the literal package
// identifier "audit" is exact for this tree, not a heuristic.
func isAuditEntryType(t ast.Expr) bool {
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "audit" && sel.Sel.Name == "Entry"
}

// collectLocalMapKeys walks a function body and returns, for every
// identifier assigned a map[string]any (or map[string]interface{})
// composite literal, OR later indexed with a string-literal key, the set of
// string keys that assignment could contribute — across the WHOLE function
// body regardless of nesting, since an `if` branch that conditionally adds
// "email" still means this call site can carry it.
func collectLocalMapKeys(body *ast.BlockStmt) map[string]map[string]bool {
	result := map[string]map[string]bool{}
	addKey := func(ident string, key string) {
		if result[ident] == nil {
			result[ident] = map[string]bool{}
		}
		result[ident][key] = true
	}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Lhs) != len(assign.Rhs) {
			return true
		}
		for i, lhs := range assign.Lhs {
			rhs := assign.Rhs[i]
			switch l := lhs.(type) {
			case *ast.Ident:
				// x := map[string]any{"k": v, ...} or x = map[string]any{...}
				if cl, ok := rhs.(*ast.CompositeLit); ok {
					if result[l.Name] == nil {
						result[l.Name] = map[string]bool{}
					}
					for _, k := range compositeLitStringKeys(cl) {
						addKey(l.Name, k)
					}
				}
			case *ast.IndexExpr:
				// x["k"] = v
				xIdent, ok := l.X.(*ast.Ident)
				if !ok {
					continue
				}
				if lit, ok := l.Index.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if key, err := strconv.Unquote(lit.Value); err == nil {
						addKey(xIdent.Name, key)
					}
				}
			}
		}
		return true
	})
	return result
}

// compositeLitStringKeys returns the string-literal keys of a map composite
// literal (map[string]any{"k": v, ...}); non-string or non-literal keys are
// skipped (this tree's audit Metadata maps use only string literal keys —
// verified while writing this guard).
func compositeLitStringKeys(cl *ast.CompositeLit) []string {
	var keys []string
	for _, elt := range cl.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		lit, ok := kv.Key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			continue
		}
		if key, err := strconv.Unquote(lit.Value); err == nil {
			keys = append(keys, key)
		}
	}
	return keys
}

// auditEmailMetadataSuspectKeyTokens is the set of underscore-delimited
// Metadata-key tokens this guard treats as carrying an address-shaped
// value — widened from a literal "email"-only match after #0237's phase-3
// review proved the hole: Probe C, a synthetic
// map[string]any{"recipient_email": addr, "subscriber_address": addr}
// site, passed the old exact-match check silently even though
// "recipient_email" is the SAME key style
// internal/handlers/admin_campaign_preview.go:419 already uses in
// production. Token-based (split the key on "_", lowercase, EXACT token
// match) rather than a bare substring test on purpose: a substring test on
// "recipient" would also fire on internal/mailing/worker.go's "recipients"
// key, which holds an int64 COUNT of recipients, never an address — a
// false positive this guard must not manufacture. Splitting on "_" and
// requiring an exact token match keeps "recipient_email" (tokens
// "recipient", "email") and "subscriber_address" (tokens "subscriber",
// "address") caught while leaving "recipients" (one token, "recipients",
// not "recipient") alone — checked against every Metadata key actually
// used in this tree while writing this change, not assumed. "query" is
// included because internal/handlers/admin_subscribers_export.go:312's
// "filter_query" key holds unbounded admin-typed search text that MAY be a
// subscriber's own address if that is what the admin searched by — a
// value this guard cannot read statically, so the conservative, correct
// answer is to always treat a "*query*" key as a possible carrier rather
// than guess at its content.
//
// "to", "mail", "contact" were added after #0237's SECOND phase-3 review
// (the one after the widening above) demonstrated a residual hole by
// injecting eight key shapes into a real handler's audit.Entry: "to",
// "mail", "contact", "user_mail", and "recipientEmail" all passed the
// four-token set above silently, and "to" is not hypothetical —
// internal/handlers/admin_campaign_preview.go:419 writes
// "to": actor.Email TODAY, and the guard's own ## Gotchas note had wrongly
// claimed that site already carried two suspect keys when, before this
// change, it carried exactly one ("recipient_email"; "to" did not match).
// Dumped every Metadata key literal actually used in internal/ and cmd/
// (86 distinct keys, the same census the review ran) before adding
// anything: no key in the tree contains "to", "mail", or "contact" as an
// underscore-delimited token today (the closest near-miss, "topic_arn",
// splits to "topic"/"arn" — neither collides), so all three are free:
// "to" newly matches the one real "to" key above (already pinned via
// "recipient_email", so no NEW site to add to
// auditEmailMetadataKnownSites — see TestAuditEntryEmailMetadataMatchesKnownSites),
// and "mail"/"contact" match nothing in the tree today but close the
// "user_mail"/"contact"-shaped gap the review named for free.
//
// "recipientEmail" (camelCase) is deliberately NOT covered. This split is
// underscore-only, so a camelCase key is invisible to it by construction —
// closing that gap would need a second, camelCase-aware tokenizer, not a
// one-line token addition. Every one of the 86 real Metadata keys in this
// tree is snake_case or a single lowercase word; none is camelCase. Given
// zero real benefit today against genuine added complexity (and a second
// place for that complexity to itself go stale), the deliberate choice is
// to leave this gap and say so here, rather than either silently ignoring
// it or building an underused code path. If a camelCase Metadata key is
// ever added to this tree, this guard will NOT catch it under a suspect
// token unless it is also snake_case, or this comment (and the tokenizer)
// are revisited.
var auditEmailMetadataSuspectKeyTokens = map[string]bool{
	"email":     true,
	"address":   true,
	"recipient": true,
	"query":     true,
	"to":        true,
	"mail":      true,
	"contact":   true,
}

// metadataKeyIsSuspectedEmailCarrier reports whether key, split on "_",
// contains a token this guard treats as address-shaped. See
// auditEmailMetadataSuspectKeyTokens for the exact set and why each token
// is there.
func metadataKeyIsSuspectedEmailCarrier(key string) bool {
	for _, tok := range strings.Split(strings.ToLower(key), "_") {
		if auditEmailMetadataSuspectKeyTokens[tok] {
			return true
		}
	}
	return false
}

// classifyMetadataExpr determines whether a Metadata: <expr> value could
// carry an email-shaped value under any key
// metadataKeyIsSuspectedEmailCarrier recognizes, returning (hasEmail,
// unresolved): unresolved is non-empty exactly when expr's shape is not one
// this guard can prove either way, and hasEmail is only meaningful when
// unresolved == "".
func classifyMetadataExpr(expr ast.Expr, dir string, localKeys map[string]map[string]bool, helperFuncs map[funcKey]*ast.FuncDecl) (hasEmail bool, unresolved string) {
	switch e := expr.(type) {
	case *ast.CompositeLit:
		for _, k := range compositeLitStringKeys(e) {
			if metadataKeyIsSuspectedEmailCarrier(k) {
				return true, ""
			}
		}
		return false, ""
	case *ast.Ident:
		if e.Name == "nil" {
			return false, ""
		}
		keys, ok := localKeys[e.Name]
		if !ok {
			return false, fmt.Sprintf("Metadata is identifier %q with no traceable map-literal or index assignment in its enclosing function", e.Name)
		}
		for k := range keys {
			if metadataKeyIsSuspectedEmailCarrier(k) {
				return true, ""
			}
		}
		return false, ""
	case *ast.CallExpr:
		callee, ok := e.Fun.(*ast.Ident)
		if !ok {
			return false, fmt.Sprintf("Metadata is a call this guard does not resolve: %s", exprText(e.Fun))
		}
		if !auditEmailMetadataKnownSafeHelpers[callee.Name] {
			return false, fmt.Sprintf("Metadata calls %q, which is not in auditEmailMetadataKnownSafeHelpers — verify it cannot write an email, then add it (or inline-resolve the call) rather than letting this guard pass silently", callee.Name)
		}
		fn, ok := helperFuncs[funcKey{dir: dir, name: callee.Name}]
		if !ok || fn.Body == nil {
			return false, fmt.Sprintf("Metadata calls %q, listed as a known-safe helper, but no same-directory function definition was found to verify it against", callee.Name)
		}
		return classifyHelperReturnsForEmail(fn.Body)
	default:
		return false, fmt.Sprintf("Metadata is an unrecognized expression shape (%T) this guard does not classify", expr)
	}
}

// classifyHelperReturnsForEmail checks every `return <expr>` in a
// known-safe helper's body: each returned expression must itself be a map
// composite literal (or nil) this guard can read keys off; anything else is
// unresolved, so a helper cannot silently start returning something this
// guard can no longer see into.
func classifyHelperReturnsForEmail(body *ast.BlockStmt) (hasEmail bool, unresolved string) {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return true
		}
		found = true
		switch r := ret.Results[0].(type) {
		case *ast.CompositeLit:
			for _, k := range compositeLitStringKeys(r) {
				if metadataKeyIsSuspectedEmailCarrier(k) {
					hasEmail = true
				}
			}
		case *ast.Ident:
			if r.Name != "nil" {
				unresolved = fmt.Sprintf("helper returns identifier %q, not a literal", r.Name)
			}
		default:
			unresolved = fmt.Sprintf("helper returns an unrecognized expression shape (%T)", r)
		}
		return true
	})
	if !found {
		unresolved = "helper has no single-value return statement to check"
	}
	return hasEmail, unresolved
}

// exprText renders a best-effort, human-readable form of an Action field's
// value for messages and identity comparisons — full fidelity is not the
// goal (this is not a general Go printer), just enough to name
// "audit.ActionX" as "ActionX" and fall back honestly otherwise.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		if x, ok := v.X.(*ast.Ident); ok {
			return x.Name + "." + v.Sel.Name
		}
		return "<complex>." + v.Sel.Name
	default:
		return "<complex>"
	}
}

// actionShortName strips a leading "audit." package qualifier, if present,
// so "audit.ActionSubscriberConfirmed" (the real form in source) compares
// equal to the "ActionSubscriberConfirmed" form auditEmailMetadataKnownSites
// uses for readability.
func actionShortName(s string) string {
	if idx := strings.LastIndex(s, "."); idx >= 0 {
		return s[idx+1:]
	}
	return s
}

// TestAuditEntryEmailMetadataMatchesKnownSites is the guard (#0237): every
// audit.Entry{} construction in internal/ and cmd/ production Go source
// whose Metadata could carry the subscriber's email address must be exactly
// the set auditEmailMetadataKnownSites pins, with matching IP-presence.
// Failing this test names precisely what changed, so the fix is never
// silent: update this fixture to match the tree, then update
// PrivacyPolicy.svelte's audit-log disclosure to match the fixture.
func TestAuditEntryEmailMetadataMatchesKnownSites(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	baseDir := filepath.Dir(thisFile)
	repoRoot := filepath.Join(baseDir, "..", "..")

	var roots []string
	for _, rel := range auditEmailMetadataScanRoots {
		roots = append(roots, filepath.Join(baseDir, rel))
	}

	sites := scanAuditEntrySites(t, roots)

	var unresolved []string
	type foundKey struct{ file, action string }
	found := map[foundKey]int{}
	foundIP := map[foundKey]bool{}
	for _, s := range sites {
		rel := toRepoRelativePath(repoRoot, s.file)
		if s.unresolved != "" {
			unresolved = append(unresolved, fmt.Sprintf("  %s:%d: %s", rel, s.line, s.unresolved))
			continue
		}
		if !s.hasEmail {
			continue
		}
		k := foundKey{file: rel, action: actionShortName(s.action)}
		found[k]++
		if s.hasIP {
			foundIP[k] = true
		}
	}

	if len(unresolved) > 0 {
		sort.Strings(unresolved)
		t.Fatalf("audit.Entry sites this guard could not classify (fix the site, or extend this guard's resolution deliberately — see classifyMetadataExpr):\n%s", strings.Join(unresolved, "\n"))
	}

	var problems []string
	seen := map[foundKey]bool{}
	for _, known := range auditEmailMetadataKnownSites {
		k := foundKey{file: known.file, action: known.action}
		seen[k] = true
		gotCount, ok := found[k]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("  MISSING: %s: %s no longer writes an email into Metadata (was pinned as retaining one) — if erasure.go/the schema changed, update auditEmailMetadataKnownSites AND PrivacyPolicy.svelte's audit-log item", known.file, known.action))
		case gotCount != known.count:
			problems = append(problems, fmt.Sprintf("  COUNT CHANGED: %s: %s now has %d email-writing occurrence(s), pinned as %d", known.file, known.action, gotCount, known.count))
		case foundIP[k] != known.hasIP:
			problems = append(problems, fmt.Sprintf("  IP PRESENCE CHANGED: %s: %s now %s an IP field, pinned as %s — the privacy policy's IP claim for this entry needs to match", known.file, known.action, ipWord(foundIP[k]), ipWord(known.hasIP)))
		}
	}
	for k, count := range found {
		if seen[k] {
			continue
		}
		problems = append(problems, fmt.Sprintf("  NEW: %s: %s writes an email into Metadata (%d occurrence(s)) and is not in auditEmailMetadataKnownSites — add it here AND describe it in PrivacyPolicy.svelte's audit-log disclosure", k.file, k.action, count))
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		t.Fatalf("audit.Entry email-metadata sites (#0237) drifted from auditEmailMetadataKnownSites:\n%s", strings.Join(problems, "\n"))
	}
}

func ipWord(has bool) string {
	if has {
		return "sets"
	}
	return "does not set"
}

// writeSyntheticGoFile writes src into a fresh file inside dir and returns
// its path — used by the synthetic-fixture tests below so they exercise
// scanAuditEntrySites' real disk-walking code path (walkGoFiles,
// go/parser.ParseFile) rather than hand-building an *ast.File, the same
// choice PrivacyPolicy.guard.test.ts's fixtureSource makes for its own
// mutation proofs. Package name and import path are irrelevant here:
// go/parser only checks syntax, never resolves imports, so these fixtures
// need no real audit package on disk.
func writeSyntheticGoFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatalf("write synthetic fixture %s: %v", path, err)
	}
	return path
}

// TestAuditEmailMetadataGuardCatchesInlineEmail proves scanAuditEntrySites
// actually fires on the simplest shape: an inline map[string]any literal
// carrying "email" directly, isolated in a synthetic fixture rather than a
// real tree instance (there is deliberately never an uncaught one
// committed).
func TestAuditEmailMetadataGuardCatchesInlineEmail(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	auditor.Record(ctx, audit.Entry{
		Action:     audit.ActionSubscriberSignup,
		TargetType: audit.TargetSubscriber,
		Metadata:   map[string]any{"email": subscriberEmail, "kind": "test"},
		IP:         ip,
	})
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved != "" {
		t.Fatalf("expected the inline literal to resolve cleanly, got unresolved: %s", sites[0].unresolved)
	}
	if !sites[0].hasEmail {
		t.Fatal("expected hasEmail=true for an inline literal carrying \"email\"")
	}
	if !sites[0].hasIP {
		t.Fatal("expected hasIP=true: this fixture sets an IP field")
	}
}

// TestAuditEmailMetadataGuardCatchesWidenedSuspectTokens is the committed
// regression test #0237's second phase-3 review asked for: it proved the
// hole (the four-token set missed "to", "mail", "contact", "user_mail",
// "recipientEmail") by injecting keys into a real handler in a throwaway
// worktree and deleting the probe afterward, so nothing pinned the widened
// rule down. This fixture is that pin. It covers the three tokens actually
// added ("to", "mail", "contact" — see auditEmailMetadataSuspectKeyTokens's
// doc comment for why those three and not the other two) in one synthetic
// site so a future narrowing of the token set fails here first, not only
// via a drift in auditEmailMetadataKnownSites.
func TestAuditEmailMetadataGuardCatchesWidenedSuspectTokens(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordToSite() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionEmailCampaignTestSent,
		Metadata: map[string]any{"to": actorEmail, "kind": "test"},
	})
}

func recordMailSite() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"user_mail": addr},
	})
}

func recordContactSite() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberManualAdd,
		Metadata: map[string]any{"contact": addr},
	})
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 3 {
		t.Fatalf("expected exactly three audit.Entry sites, found %d", len(sites))
	}
	for _, s := range sites {
		if s.unresolved != "" {
			t.Fatalf("expected %s to resolve cleanly, got unresolved: %s", s.action, s.unresolved)
		}
		if !s.hasEmail {
			t.Fatalf("expected hasEmail=true for %s (a %q/%q/%q-shaped key), the widened token set this test pins", s.action, "to", "mail", "contact")
		}
	}
}

// TestAuditEmailMetadataGuardCamelCaseKeyNotCaught documents, rather than
// hides, the accepted gap auditEmailMetadataSuspectKeyTokens's doc comment
// states: this guard's token match splits only on "_", so a camelCase key
// is invisible to it. If this test starts failing, either a camelCase
// tokenizer was added (update this test to match) or the underscore split
// was changed in some other way that now happens to catch this shape
// (same). It is not itself a passing guard against anything — it is a
// pinned record of a known, deliberate blind spot.
func TestAuditEmailMetadataGuardCamelCaseKeyNotCaught(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"recipientEmail": addr},
	})
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved != "" {
		t.Fatalf("expected the camelCase-keyed literal to resolve cleanly (not unresolved), got: %s", sites[0].unresolved)
	}
	if sites[0].hasEmail {
		t.Fatal("expected hasEmail=false: \"recipientEmail\" has no \"_\" so the token split never sees it — this is the documented gap, not a bug; if this now fails, the gap was closed and this test (and the doc comment above auditEmailMetadataSuspectKeyTokens) should be updated to say so")
	}
}

// TestAuditEmailMetadataGuardCatchesTracedLocalVariable proves the
// identifier-tracing branch: a Metadata built in a local variable, with
// "email" added by a conditional index assignment. No real call site in
// this tree carries "email" this way today — checked directly, not
// assumed: internal/handlers/admin_suppressions.go:274's suppression-
// removal entry builds Metadata as an INLINE composite literal (its
// "email" key is written directly in the literal, no local variable or
// index assignment involved), and internal/handlers/admin_subscribers.go:694's
// Complaint-clearing entry DOES use a traced local with conditional index
// assignments — but for "suppression_removed_note" and
// "suppression_removed_created_at", never "email" (an earlier draft of
// this comment claimed both sites used this exact shape for "email"; #0237's
// phase-3 review found that false for both — see that review for the full
// reproduction). This fixture exists because the general "traced local +
// conditional index assignment" pattern IS real in this tree
// (admin_subscribers.go:694 above is that real instance), so a future site
// that adds "email" the same way — the shape #0226's phase-3 review flagged
// as the most plausible place for a future email addition to slip past a
// phrase-keyed guard — must still be caught even though no site does it yet.
func TestAuditEmailMetadataGuardCatchesTracedLocalVariable(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething(flag bool) {
	metadata := map[string]any{"reason": reason}
	if flag {
		metadata["email"] = subscriberEmail
	}
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: metadata,
	})
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved != "" {
		t.Fatalf("expected the traced local variable to resolve cleanly, got unresolved: %s", sites[0].unresolved)
	}
	if !sites[0].hasEmail {
		t.Fatal("expected hasEmail=true: the conditional index assignment adds \"email\"")
	}
}

// TestAuditEmailMetadataGuardResolvesKnownSafeHelper proves a call to a
// listed known-safe helper (credentialMetadata's real shape) resolves as
// NOT carrying an email, by reading the helper's own return statement
// rather than trusting its name.
func TestAuditEmailMetadataGuardResolvesKnownSafeHelper(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionCredentialAdded,
		Metadata: credentialMetadata(deviceName, aaguid),
	})
}

func credentialMetadata(deviceName string, aaguid []byte) map[string]any {
	return map[string]any{"device_name": deviceName, "aaguid": "x"}
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved != "" {
		t.Fatalf("expected the known-safe helper call to resolve cleanly, got unresolved: %s", sites[0].unresolved)
	}
	if sites[0].hasEmail {
		t.Fatal("expected hasEmail=false: credentialMetadata's real shape carries no email")
	}
}

// TestAuditEmailMetadataGuardFailsOnUnresolvableMetadata proves the
// conservative default: a Metadata expression this guard cannot classify —
// here, a call to a function NOT in auditEmailMetadataKnownSafeHelpers —
// is reported as unresolved rather than silently passed as email-free. This
// is the mechanism that makes criterion 3 hold for a genuinely NEW call
// site this guard's author never anticipated, not just for the ones caught
// by name-matching a known shape.
func TestAuditEmailMetadataGuardFailsOnUnresolvableMetadata(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSignup,
		Metadata: someBrandNewHelperNobodyAddedToTheAllowlist(sub),
	})
}
`)
	sites := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved == "" {
		t.Fatal("expected an unresolvable-call Metadata expression to be reported as unresolved, not silently classified")
	}
}
