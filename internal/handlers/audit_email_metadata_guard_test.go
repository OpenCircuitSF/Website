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
// walks every audit.Entry{...} composite literal FOUND INSIDE A TOP-LEVEL
// FUNCTION'S BODY in production Go source (internal/ and cmd/; _test.go
// files are excluded because a test fixture never becomes a real inserted
// row) — not, as an earlier draft of this sentence claimed, every
// audit.Entry{...} composite literal in production Go source, full stop;
// #0252 demonstrated three shapes that sit entirely outside a function body
// and are invisible to this walk as a result (see the "structurally
// invisible" paragraph below) — and classifies whether its Metadata
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
// A call this guard genuinely cannot resolve — an unlisted helper, a method
// call (h.buildMetadata(...)), a package-qualified call (maps.Clone(...)),
// or a bare identifier that no recognized assignment form (see below) ever
// touches in its enclosing function — is treated as a FAILURE, not a silent
// pass. That is the conservative direction, and #0237's third phase-3
// review confirmed it holds for exactly those shapes by running them
// through scanAuditEntrySites directly.
//
// The real boundary is narrower than "cannot classify ⇒ FAILURE" reads at a
// glance, and stating it required running eight synthetic Metadata shapes
// through scanAuditEntrySites, not reading the code (#0237's third phase-3
// review — the first two drafts of this paragraph each asserted a boundary
// that turned out false the moment it was actually run). A bare identifier
// is resolved ENTIRELY from two assignment forms collectLocalMapKeys looks
// for, anywhere in the enclosing function body: `x := map[string]any{...}`
// (composite-literal keys) and `x["literal"] = ...` (an index assignment
// whose index is itself a string literal). The instant either form is seen
// for that identifier, it is treated as resolved using ONLY what those
// forms reveal — nothing else about the identifier's value is considered,
// including how it was first obtained. That produces four silent passes
// (hasEmail=false, unresolved=""), not the single one this header used to
// name:
//
//   - a composite-literal key that is not itself a string literal (an
//     identifier constant, an integer, any other expression) — SKIPPED by
//     compositeLitStringKeys: map[string]any{emailKeyConst: addr}. This is
//     the one this header previously documented alone.
//   - an index assignment whose index key is itself not a string literal —
//     x[k] = addr, k a variable — collectLocalMapKeys's IndexExpr case only
//     matches a literal index, so the assignment contributes no key at all.
//   - a call that mutates the map in place rather than assigning it —
//     addEmailTo(metadata, addr) is not an *ast.AssignStmt, so
//     collectLocalMapKeys never sees it regardless of what it does.
//   - the sharpest one: an identifier first assigned from an unresolvable
//     call (metadata := buildMeta(sub)) is correctly FAILURE while nothing
//     else touches it — see the "e" row below — but the instant one more
//     string-literal index assignment is added anywhere in the function
//     (metadata["reason"] = r, including via a `var` declaration instead of
//     `:=`), the identifier becomes "resolved" from that one visible key
//     alone, and whatever buildMeta itself put into the map — an "email"
//     key included — is invisible. This shape directly contradicts the "a
//     value built outside the enclosing function ... is treated as a
//     FAILURE" framing an earlier draft of this header used as its
//     canonical example of what fails: it is, right up until one more line
//     is added.
//
// None of these four shapes is exercised by any site in this tree today —
// checked directly while writing this correction (the same 45-site,
// 87-key census below), not assumed: the only non-inline Metadata
// identifiers in the tree are credentialMetadata's two call sites (resolved
// through the helper's own return, by name, in
// auditEmailMetadataKnownSafeHelpers) and admin_subscribers.go:694 /
// admin_subscribers_export.go:327, both `metadata := map[string]any{...}`
// literals whose only further mutation is a string-literal index
// assignment — the one case this guard reads completely. So the code is
// fine and only this description was overstated; the four shapes are
// pinned as documented, unfixed gaps by
// TestAuditEmailMetadataGuardDocumentedSilentPassShapes below, the same
// treatment the camelCase key gap gets. Widening what this guard resolves
// should always be a deliberate, reviewed change to this file, not a
// byproduct of loosening this description.
//
// #0252 found two more false claims in this same file, both predating the
// paragraphs above and untouched by them — the header's "walks every
// audit.Entry{...} composite literal" opening claim, and a doc comment on
// auditEmailMetadataSuspectKeyTokens (see that var). Fixing the first
// required actually running four candidate shapes through
// scanAuditEntrySites, not reasoning about the code, the same standard the
// paragraphs above hold themselves to:
//
//   - a package-level var initialized directly from an audit.Entry{}
//     literal (var seedEntry = audit.Entry{...}) — SITES=0. The outer walk
//     below only descends into composite literals found inside a
//     *ast.FuncDecl's Body; a package-level *ast.GenDecl is walked for
//     nothing.
//   - an audit.Entry{} literal inside a func literal assigned to a
//     package-level var (var recordFn = func() { ... audit.Entry{...} }) —
//     SITES=0, for the same reason: the outer walk keys specifically on
//     *ast.FuncDecl, and a *ast.FuncLit hanging off a package-level var is
//     never one.
//   - an elided-type element of a []audit.Entry{{...}} slice literal —
//     SITES=0 at the time. isAuditEntryType type-asserts a literal's Type
//     field as *ast.SelectorExpr naming "audit.Entry"; an elided element
//     type (Go's own syntax for "same type as the slice") leaves that
//     field nil, so the assertion fails and the element is never
//     recognized as an audit.Entry site to begin with. #0290 later fixed
//     this one (elidedAuditEntryLiterals, wired into scanAuditEntrySites'
//     site-detection predicate) — see
//     TestAuditEmailMetadataGuardCatchesElidedCompositeLiteral, which is
//     where its pin moved to.
//   - `e := audit.Entry{}` followed by `e.Metadata = map[string]any{...}`
//     — SEEN (the empty literal IS a real site) but, before this pass, a
//     SILENT PASS (hasEmail=false, unresolved=""): the Metadata-reading
//     loop only ever looked at the literal's own Elts, never at a later
//     struct-field assignment on the identifier it produced. This is the
//     one #0252 named as the plausible refactor shape — the form someone
//     naturally reaches for when an entry needs building across a few
//     lines — and it is now FIXED, not merely documented: see
//     collectDeferredMetadataFieldIdents and compositeLitAssignedIdents
//     below, which connect any `audit.Entry{}` literal assigned to an
//     identifier — bare, with other fields already set (e.g. Action), or
//     already carrying its own Metadata key — to any later
//     `.Metadata = ...` OR `.Metadata[...] = ...` (#0257 widened this from
//     the field-assignment form alone) on the same identifier and report
//     the site as unresolved (the guard's existing conservative default
//     everywhere else) rather than passing it silently. #0257's review
//     measured this directly: a bare literal, one with Action set, and one
//     already carrying an unrelated Metadata key are all connected and all
//     report unresolved once a later assignment follows — "bare" in an
//     earlier draft of this paragraph was not just imprecise, it was
//     factually wrong.
//     TestAuditEmailMetadataGuardFailsOnDeferredStructFieldAssignment pins
//     this, mutation-proved against the pre-fix code.
//
// The first three were pinned, unfixed, structural blind spots — the walk
// finds nothing to even classify — by
// TestAuditEmailMetadataGuardDocumentedInvisibleShapes below, the same
// documented-gap treatment the camelCase key gap and the four silent-pass
// shapes above get. #0290 fixed the third (the elided-literal shape, see
// the correction above), so that test now pins the remaining two. None of
// the three was exercised by any real site in this tree at the time
// (checked directly, not assumed: every real audit.Entry{}
// construction in internal/ and cmd/ is either an inline literal inside a
// named function or reached through the traced-local-variable path this
// file already resolves).
//
// #0252's phase-3 review found #0252's own fix incomplete — four more
// shapes, all predating that pass, three of them still blind to it. Each was
// confirmed by actually running it through scanAuditEntrySites, not by
// reasoning about the code, the same standard every paragraph above holds
// itself to:
//
//   - `e := audit.Entry{Metadata: map[string]any{"slug": s}}` followed by
//     `e.Metadata = map[string]any{"email": addr}` — SEEN but, before this
//     pass, a SILENT PASS (hasEmail=false, unresolved=""): the deferred-
//     assignment check #0252 added ran `if !metadataKeyFound`, so a literal
//     that ALSO set its own Metadata key skipped the check entirely, and
//     classification came from the literal's own map — the value that LOSES
//     at runtime, since the later assignment is what the running code
//     actually inserts. This is the shape a developer writes without
//     thinking (initialize some metadata inline, replace it a line later),
//     which is what made it the priority finding. Now FIXED: the check runs
//     unconditionally whenever the identifier is later mutated with a
//     struct-field .Metadata assignment, regardless of whether the literal
//     itself also set one.
//     TestAuditEmailMetadataGuardFailsWhenLiteralAlsoSetsMetadataThenOverridden
//     pins this.
//   - `w.e = audit.Entry{}` followed by `w.e.Metadata = map[string]any{...}`
//     (the entry held in a struct field, not a bare identifier) — SILENT
//     PASS, unfixed: both compositeLitAssignedIdents and
//     collectDeferredMetadataFieldIdents require a bare *ast.Ident on the
//     relevant side of the assignment; `w.e` is an *ast.SelectorExpr on
//     both sides, so neither helper ever connects the literal to the later
//     field write. A structural blind spot, not a live hole — pinned, not
//     fixed, by TestAuditEmailMetadataGuardDocumentedSilentPassShapes.
//   - `e := audit.Entry{}` followed by `setMeta(&e)`, where setMeta sets
//     `e.Metadata` on the pointer it receives — SILENT PASS, unfixed:
//     collectDeferredMetadataFieldIdents only recognizes a direct
//     `ident.Metadata = ...` *ast.AssignStmt in the enclosing function's own
//     body; a helper call that mutates the identifier's field through a
//     pointer argument is invisible to it regardless of what the call does.
//     Same pinned-not-fixed treatment, same test.
//   - two FALSE POSITIVES, both failing closed, in the very check the first
//     bullet above just widened: collectDeferredMetadataFieldIdents matches
//     a later `.Metadata = ...` assignment by identifier NAME across the
//     whole function body, with no Go scope resolution and no statement-
//     order awareness. A same-named variable of an unrelated type in a
//     disjoint block, or a `.Metadata = ...` that textually precedes the
//     `audit.Entry{...}` literal it gets attributed to, both make an
//     innocent site report unresolved. The direction is safe — it fails
//     closed, never a silent pass — but the message used to assert a
//     same-variable runtime override it had not established. FIXED the
//     message, not the detection: it now says a same-named struct-field
//     assignment appears "somewhere in this function" — hedged with
//     "possibly before or after ... and possibly on an unrelated variable of
//     the same name" — rather than claiming a specific override. Pinned as
//     an accepted, documented false positive by
//     TestAuditEmailMetadataGuardDocumentedFalsePositiveShapes, the same
//     treatment every other known gap in this file gets.
//
// None of the three unfixed shapes above (struct field, pointer helper, the
// two false positives) is exercised by any real site in this tree today —
// checked directly while making this change: `grep -rn '\.Metadata\s*='
// internal cmd --include='*.go'` excluding _test.go files finds exactly one
// hit tree-wide (internal/audit/read.go:159, a *different* type's Metadata
// field, unrelated to audit.Entry), and no production audit.Entry is ever
// held in a struct field or mutated through a pointer argument. The
// 45-site/87-key census below is unchanged by this pass — re-derived with
// the guard's own key-extraction functions
// (compositeLitStringKeys/collectLocalMapKeys), not assumed, because this
// pass touches only the deferred-assignment resolution gate, not any
// key-extraction or token-matching code.
//
// #0255's review found an eighth shape, the same class one level deeper:
// `e := audit.Entry{Metadata: map[string]any{"slug": s}}` followed by
// `e.Metadata["email"] = addr` — SEEN but a SILENT PASS (hasEmail=false,
// unresolved=""), because collectDeferredMetadataFieldIdents matched only a
// bare `ident.Metadata = ...` *ast.SelectorExpr assignment; this LHS is an
// *ast.IndexExpr (`e.Metadata["email"]`) whose X happens to BE that same
// SelectorExpr, one syntactic layer further down, and nothing was looking
// there. What made this one worth closing rather than pinning: the IDENTICAL
// mutation through a bare local variable — `m := map[string]any{"slug": s};
// m["email"] = addr; e := audit.Entry{Metadata: m}` — was ALREADY caught, by
// collectLocalMapKeys's own *ast.IndexExpr case; the two shapes express the
// same intent and differ only by whether the map sits behind one more level
// of indirection (a struct field vs. a bare identifier), not by anything
// about what a developer is doing. Now FIXED: collectDeferredMetadataFieldIdents
// recognizes both `ident.Metadata = ...` and `ident.Metadata[...] = ...`,
// treating either as a deferred mutation this guard cannot see into — the
// index itself is deliberately not inspected, so the fix triggers on ANY
// key, not just "email" (TestAuditEmailMetadataGuardFailsOnDeferredIndexAssignment
// pins both an "email" and an unrelated-key case, to prove it is not
// special-cased to the one string this issue happened to use as an
// example). No production site uses this form today (checked directly:
// `grep -rn '\.Metadata\[' internal cmd --include='*.go'`, excluding
// _test.go, returns nothing), so the 45-site/87-key/0-unresolved census
// below is unaffected by this fix — re-derived, not assumed.
//
// #0257 also asked a design question worth answering directly, since seven
// review rounds finding one adjacent AST shape after another is itself a
// signal: instead of a ninth node type someday, should ANY Metadata write
// this guard cannot fully resolve report unresolved by default, rather than
// enumerating recognized shapes and defaulting to pass? Two things are worth
// separating in that question. On the READ side — classifyMetadataExpr,
// where a Metadata: <expr> value's own shape is examined — this is ALREADY
// the design: an unrecognized *ast.Ident, *ast.CallExpr, or any other
// expression type returns unresolved, not hasEmail=false (see the switch's
// default case, and classifyHelperReturnsForEmail's own default). Every
// silent pass this file's history documents, #0257 included, was never a
// case of the reader defaulting to "pass" on an unrecognized shape; it was
// the WALK failing to notice a mutation had happened at all, because
// collectDeferredMetadataFieldIdents pattern-matches specific *ast.AssignStmt
// LHS shapes rather than asking a general question like "was this
// identifier's Metadata touched anywhere, by any means". That is not a
// default to invert — there is no dispatch point where "unrecognized ⇒ fail"
// already lives for this half of the problem; each new shape needs a new
// pattern taught to the walk, the same way this fix teaches it one more.
// Widening the pattern to something maximal — e.g. flag ANY assignment
// anywhere in the function whose LHS is an *ast.SelectorExpr or *ast.IndexExpr
// matching an audit.Entry-holding identifier's name, regardless of which
// FIELD is touched — was considered and rejected: it would flag ordinary,
// unrelated field writes (`e.Action = "..."`) as if they were Metadata
// mutations, and it would multiply the ALREADY-accepted same-name false-
// positive class documented two paragraphs above (a same-named variable of
// an unrelated type gets flagged today only when its OWN Metadata field is
// touched; matching on any field of any same-named identifier would catch
// far more innocent code doing nothing related to Metadata at all). The fix
// above is the narrow form of "invert the default" that this shape actually
// calls for: it recognizes the one further AST shape (index-on-selector)
// that mutates Metadata specifically, checks nothing about WHICH key or
// value, and reports unresolved unconditionally once seen — conservative
// exactly where #0252/#0255's precedent already is, without widening what
// counts as "touches Metadata" to fields that are not Metadata.

// #0261: #0257's own review reported, without filing, a ninth shape: `e :=
// &audit.Entry{...}` followed by either `e.Metadata = ...` or
// `e.Metadata[...] = ...` was a silent pass (hasEmail=false, unresolved="")
// — the pointer form of #0252's and #0257's own fixes, both of which only
// ever handled a VALUE-typed identifier. Reproduced directly before fixing:
// both deferred-mutation forms on a pointer-held entry resolved cleanly with
// no warning. The break was NOT in collectDeferredMetadataFieldIdents — Go's
// selector syntax `e.Metadata = ...` is the identical *ast.SelectorExpr
// whether e holds a *audit.Entry or an audit.Entry, and the parser never
// resolves types, so that half already saw the deferred assignment
// correctly. It was entirely in compositeLitAssignedIdents, which required
// an *ast.AssignStmt's RHS to be an *ast.CompositeLit directly; `&audit.Entry
// {...}` is an *ast.UnaryExpr (Op == token.AND) wrapping one, so the type
// assertion failed and the literal was never connected to the identifier
// that held it — `literalIdents[cl]` had no entry, so the lookup in
// scanAuditEntrySites never fired regardless of what
// collectDeferredMetadataFieldIdents already knew. Now FIXED: unwrap a
// single leading `&` before the *ast.CompositeLit type assertion, so both
// pointer forms (`ident := &audit.Entry{...}` and `ident = &audit.Entry
// {...}`) are connected the same way the value forms already are. See
// compositeLitAssignedIdents below for the fix itself.
//
// The same review's follow-on observation — mutation of a Metadata map is
// not only assignment; maps.Copy(e.Metadata, src) and delete(e.Metadata, k)
// both change the map through a plain function call, with no *ast.AssignStmt
// anywhere for either deferred-mutation helper to match — is also FIXED
// here, not merely pinned: collectDeferredMetadataFieldIdents now also
// recognizes both calls (matched by call shape alone — callee name "delete"
// or "maps.Copy", first argument's selector naming "Metadata" — with
// neither call's arguments read, the same conservative non-inspection its
// existing assignment cases already use) and reports the identifier's
// Metadata as an unresolved deferred mutation. Reproduced directly before
// fixing: a "slug"-only literal followed by `maps.Copy(e.Metadata, extra)`
// resolved hasEmail=false, unresolved="" — the consequential direction this
// issue named, since `extra` could carry an email this guard never
// inspects. `delete` cannot itself introduce a key, so its risk runs the
// opposite way (a stale hasEmail=true, if a delete call removes the very key
// that made the literal look risky) — but this guard's established answer
// to "cannot prove what the running code ends up with" is unresolved
// regardless of which direction the specific gap points, so both calls are
// treated identically rather than reasoned about case by case. The failure
// message previously said "a struct-field assignment"; it now says "a
// Metadata mutation" and enumerates all four recognized shapes, so it stays
// true now that two of them are calls, not assignments.
//
// No production site uses any of these four shapes today (checked directly:
// `grep -rn '&audit\.Entry{' internal cmd --include='*.go'`,
// `grep -rn 'maps\.Copy(' internal cmd --include='*.go'`, and
// `grep -rn 'delete(' internal cmd --include='*.go' | grep -i metadata`,
// each excluding _test.go, all return nothing), so this pass changes
// detection capability only — it adds and removes no site from
// auditEmailMetadataKnownSites, and TestAuditEntryEmailMetadataMatchesKnownSites
// (which fails on ANY newly-unresolved site) passed unchanged against the
// real tree. The site/key census itself has moved since the 45-site/87-key
// figure earlier paragraphs cite, but not because of this pass: #0290
// corrects the attribution the paragraph used to carry here — #0261's
// review bisected the growth and found it was NOT #0126 alone. The 48→49
// step came from #0126 (cfc8caa); the 49→52 step — three of the four sites
// added since — came from #0124 (fce9071, 7703464, the delivery-health
// breaker and its admin surface), landing concurrently with this pass and
// with #0126 both. Re-deriving both figures directly against the real tree
// (scanAuditEntrySites over auditEmailMetadataScanRoots, plus an independent
// walker reusing only this file's key-extraction primitives —
// compositeLitStringKeys and collectLocalMapKeys, not its classification
// decisions) finds **49 sites, 95 distinct keys, 0 unresolved** as of the
// pass that first wrote this paragraph. The invariant this issue actually
// cares about — no site newly reports unresolved, and
// auditEmailMetadataKnownSites' set of email-carrying sites is unchanged —
// holds; the raw site/key counts are expected to keep moving as #0124 and
// #0126 add call sites and are not, by themselves, evidence of a regression
// here.

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
	// #0124: ActionSubscriberSoftBounceStreakReset (POST
	// /admin/deliverability/{email}/reset-streak) is a seventh
	// SUBSCRIBER'S-OWN-ADDRESS site, the same shape as the six above (an
	// explicit, admin-initiated action on the address itself, IP set to
	// the acting admin's) — PrivacyPolicy.svelte's audit-log item's email
	// enumeration was widened to name it in the same commit.
	{file: "internal/handlers/admin_deliverability.go", action: "ActionSubscriberSoftBounceStreakReset", count: 1, hasIP: true},
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
// scanAuditEntrySites also returns the number of .go files it actually
// PARSED — #0275 criterion 4a: this guard skips _test.go files after
// walkGoFiles yields each path (see skipGoTestFile below), so walkGoFiles'
// own raw yield count is a larger, wrong population to measure a coverage
// floor against. len(files) below, taken from the same walk that populates
// `files`, is the exact population this guard's Metadata classification
// actually covers — the single source of truth, not a second walk that
// could drift out of sync with this one's own filtering.
func scanAuditEntrySites(t *testing.T, roots []string) ([]auditEntrySite, int) {
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
			deferredMetadataIdents := collectDeferredMetadataFieldIdents(fn.Body)
			literalIdents := compositeLitAssignedIdents(fn.Body)
			// #0290: elidedSites are composite literals whose own Type is
			// nil (Go's "same type as the containing literal" syntax) but
			// that containing literal is a []audit.Entry or map[K]audit.Entry
			// — see elidedAuditEntryLiterals for why isAuditEntryType(cl.Type)
			// alone cannot see these.
			elidedSites := elidedAuditEntryLiterals(fn.Body)
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				cl, ok := n.(*ast.CompositeLit)
				if !ok || (!isAuditEntryType(cl.Type) && !elidedSites[cl]) {
					return true
				}
				pos := sf.fset.Position(cl.Pos())
				site := auditEntrySite{file: sf.path, line: pos.Line}
				metadataKeyFound := false
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
						metadataKeyFound = true
						site.hasEmail, site.unresolved = classifyMetadataExpr(kv.Value, dir, localKeys, helperFuncs)
					}
				}
				// #0252: a literal that leaves Metadata out entirely (`e :=
				// audit.Entry{}`) used to resolve as hasEmail=false,
				// unresolved="" by the zero-value default above — a silent
				// pass — even when the SAME identifier gets Metadata set a
				// moment later via a struct-field assignment
				// (`e.Metadata = ...`), which this guard has no way to read
				// the content of. Report that shape as unresolved instead,
				// matching the guard's conservative default everywhere else:
				// unclassifiable fails, it does not pass.
				//
				// #0255: this used to run ONLY `if !metadataKeyFound`, which
				// meant `e := audit.Entry{Metadata: map[string]any{"slug": s}}`
				// followed by `e.Metadata = map[string]any{"email": addr}`
				// skipped the check entirely — metadataKeyFound was true, so
				// classification came from the literal's OWN map, the value
				// that LOSES at runtime, since the later assignment is what
				// the running code actually inserts. Run this check
				// unconditionally: if the identifier is later mutated with a
				// struct-field .Metadata assignment anywhere in the function,
				// this guard cannot prove which value the running code uses —
				// whether or not the literal itself also set one.
				//
				// This lookup has no Go scope or statement-order awareness:
				// identName is matched by NAME against every .Metadata =
				// assignment ast.Inspect finds anywhere in the function body,
				// not the specific variable this literal produced and not
				// only assignments that come after it textually. That means
				// it also fires on two shapes that are not actually this
				// hazard — a same-named variable of an unrelated type in a
				// disjoint block, and a `.Metadata = ...` that textually
				// precedes the literal it gets attributed to. Both still fail
				// closed (unresolved, never a silent pass), which is the safe
				// direction, so the message below is written to describe what
				// was actually found — a same-named Metadata mutation
				// somewhere in this function — rather than asserting a
				// same-variable runtime override this lookup has not
				// established.
				//
				// #0261 widened deferredMetadataIdents to also fire on
				// delete(ident.Metadata, ...) and maps.Copy(ident.Metadata, ...),
				// neither of which is an assignment, so this message was reworded
				// from "a struct-field assignment" (once the only possibility) to
				// "a Metadata mutation" to stay accurate for all four shapes it now
				// covers: ident.Metadata = ..., ident.Metadata[...] = ...,
				// delete(ident.Metadata, ...), and maps.Copy(ident.Metadata, ...).
				if identName, ok := literalIdents[cl]; ok && deferredMetadataIdents[identName] {
					literalNote := ""
					if metadataKeyFound {
						literalNote = " (this literal also sets its own Metadata, which that mutation may or may not override)"
					}
					site.unresolved = fmt.Sprintf("a Metadata mutation (%s.Metadata = ..., %s.Metadata[...] = ..., delete(%s.Metadata, ...), or maps.Copy(%s.Metadata, ...)) appears somewhere in this function%s — possibly before or after this audit.Entry{} literal, and possibly on an unrelated variable of the same name, since this guard matches by identifier name only and has no scope or statement-order awareness — so it cannot prove which value the running code actually uses; move Metadata into the literal exclusively, or extend this guard deliberately", identName, identName, identName, identName, literalNote)
				}
				sites = append(sites, site)
				return true
			})
			return true
		})
	}
	return sites, len(files)
}

// collectDeferredMetadataFieldIdents returns the set of identifier names that
// have Metadata set OUTSIDE the identifier's own audit.Entry{} composite
// literal, in either of two shapes: a struct-field assignment
// (`ident.Metadata = ...`, #0252) or an index assignment directly on that
// field (`ident.Metadata["key"] = ...`, #0257 — any key, not just "email";
// this check does not read the index at all, matching the field-assignment
// case above, which does not read the assigned value either). Paired with
// compositeLitAssignedIdents below to catch `e := audit.Entry{}` followed by
// either form — a literal that carries no Metadata key of its own (or only
// an unrelated one) and would otherwise resolve as "nothing to see" with
// nothing reported wrong, even though the site plainly does carry Metadata a
// statement later.
//
// #0257: `e.Metadata["email"] = addr` is an *ast.AssignStmt whose LHS is an
// *ast.IndexExpr — X is the *ast.SelectorExpr `e.Metadata`, Index is the
// string literal "email" — not an *ast.SelectorExpr itself, so the
// SelectorExpr-only case above never matched it: the literal's own "slug"-
// only Metadata (or no Metadata key at all) resolved as hasEmail=false,
// unresolved="" while an email landed in the map a statement later. The
// identical mutation through a bare local variable — `m := map[string]any{
// "slug": s}; m["email"] = addr; e := audit.Entry{Metadata: m}` — was
// already caught, by collectLocalMapKeys's own *ast.IndexExpr case (its `l.X
// .(*ast.Ident)` matches `m` directly); the difference is one level of
// indirection, `e.Metadata` being a *ast.SelectorExpr rather than a bare
// identifier, not a different kind of mutation.
//
// #0261 adds a third shape, and it is not an *ast.AssignStmt at all: a
// second review of #0257 observed that mutation of a Metadata map is not
// only assignment — `maps.Copy(e.Metadata, src)` and `delete(e.Metadata, k)`
// both change the map through a plain function call, with no assignment
// statement anywhere for either case above to match. `maps.Copy` is the
// consequential direction (it can introduce an "email" key from a variable
// this guard never inspects — reproduced directly: a literal carrying only
// "slug" followed by `maps.Copy(e.Metadata, extra)` resolved hasEmail=false,
// unresolved="" before this change); `delete` cannot itself introduce a key,
// only remove one, so its risk runs the other way (a stale hasEmail=true
// from the literal, when a delete call actually removed that key before the
// row is inserted) — but the guard's own established answer to "cannot
// prove what the running code ends up with" is unresolved either way, not a
// judgment call on which direction is safer per call, so both are treated
// identically here. Matched the same way #0257 treats an index assignment:
// the arguments are deliberately NOT inspected (which key, which source map)
// — recognizing that the mutating call happened at all is enough to know
// this guard cannot resolve the identifier's Metadata from its literal
// alone.
func collectDeferredMetadataFieldIdents(body *ast.BlockStmt) map[string]bool {
	result := map[string]bool{}
	markMetadataField := func(expr ast.Expr) {
		sel, ok := expr.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Metadata" {
			return
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return
		}
		result[ident.Name] = true
	}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				switch l := lhs.(type) {
				case *ast.SelectorExpr:
					// ident.Metadata = ...
					markMetadataField(l)
				case *ast.IndexExpr:
					// ident.Metadata[...] = ... — #0257. The index itself is
					// deliberately not inspected, the same way the field-
					// assignment case above never reads what it's assigned:
					// either shape means this guard cannot prove which keys
					// the running code ends up with, so both are reported
					// unresolved regardless of the specific key or value.
					markMetadataField(l.X)
				}
			}
		case *ast.CallExpr:
			// #0261: delete(ident.Metadata, k) and maps.Copy(ident.Metadata,
			// src) both mutate the map with no *ast.AssignStmt anywhere —
			// see the function doc above for why both are matched by call
			// shape alone, with neither argument inspected.
			//
			// #0290 adds three siblings, all matched the same way — call
			// shape alone, first argument only, nothing about the call
			// inspected further: maps.Insert(ident.Metadata, seq) is the
			// key-INTRODUCING sibling maps.Copy already has (it can add an
			// "email" key from a seq.Seq2 this guard never inspects — the
			// consequential direction, same as maps.Copy); maps.DeleteFunc
			// and clear() are removal-only, the same safe direction delete
			// already is. Reproduced directly before fixing: a "slug"-only
			// literal followed by `maps.Insert(e.Metadata, seq)` resolved
			// hasEmail=false, unresolved="" — the same silent pass
			// maps.Copy had before #0261.
			if isBuiltinDeleteCall(node) && len(node.Args) >= 1 {
				markMetadataField(node.Args[0])
			}
			if isMapsCopyCall(node) && len(node.Args) >= 1 {
				markMetadataField(node.Args[0])
			}
			if isMapsInsertCall(node) && len(node.Args) >= 1 {
				markMetadataField(node.Args[0])
			}
			if isMapsDeleteFuncCall(node) && len(node.Args) >= 1 {
				markMetadataField(node.Args[0])
			}
			if isBuiltinClearCall(node) && len(node.Args) >= 1 {
				markMetadataField(node.Args[0])
			}
		}
		return true
	})
	return result
}

// isBuiltinDeleteCall reports whether call invokes the builtin delete(...).
// Matched by the literal identifier name "delete" with no scope resolution —
// the same approximation isAuditEntryType makes for the "audit" package
// identifier below, acceptable for the same reason: nothing in this tree
// shadows the builtin with a local function or import named "delete"
// (checked: `grep -rn 'func delete(\|delete :=' internal cmd --include='*.go'`
// finds nothing).
func isBuiltinDeleteCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "delete"
}

// isMapsCopyCall reports whether call invokes maps.Copy(...) from the
// standard library "maps" package. Matched by the literal package
// identifier "maps" and selector name "Copy", the same approximation
// isAuditEntryType makes for "audit.Entry" — this tree imports "maps"
// unaliased everywhere it appears today (checked the same way).
func isMapsCopyCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Copy" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "maps"
}

// isMapsInsertCall reports whether call invokes maps.Insert(...) — #0290,
// the key-INTRODUCING sibling of maps.Copy that #0261 left unmatched
// despite adding delete "for completeness" alongside it. Matched the same
// approximate way as isMapsCopyCall, for the same reason (nothing in this
// tree imports "maps" aliased or dot-imported — checked the same way
// isMapsCopyCall's own doc comment checked it).
func isMapsInsertCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Insert" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "maps"
}

// isMapsDeleteFuncCall reports whether call invokes maps.DeleteFunc(...) —
// #0290, the removal-only sibling of the builtin delete matched the same
// way isMapsCopyCall is.
func isMapsDeleteFuncCall(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "DeleteFunc" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "maps"
}

// isBuiltinClearCall reports whether call invokes the builtin clear(...) —
// #0290, the other removal-only shape named alongside maps.DeleteFunc.
// Matched the same way isBuiltinDeleteCall matches "delete": nothing in
// this tree shadows the builtin with a local function or import named
// "clear" (checked: `grep -rn 'func clear(\|clear :=' internal cmd
// --include='*.go'` finds nothing).
func isBuiltinClearCall(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == "clear"
}

// #0261: the pointer forms (`&audit.Entry{...}`) used to be invisible here —
// this function required the RHS to be an *ast.CompositeLit directly, and
// `&audit.Entry{...}` is an *ast.UnaryExpr (Op == token.AND) wrapping one, so
// the RHS type assertion failed and no entry was ever recorded for `e := &
// audit.Entry{...}`. collectDeferredMetadataFieldIdents was NOT the affected
// half: `e.Metadata = ...` is the identical *ast.SelectorExpr whether e holds
// a *audit.Entry or an audit.Entry — Go's selector syntax does not
// distinguish, and the parser never resolves types — so it already saw the
// deferred assignment correctly. The break was entirely in this function's
// literal-to-identifier mapping, which is why `literalIdents[cl]` in
// scanAuditEntrySites never found the pointer-held literal even though
// deferredMetadataIdents[identName] was already true: the lookup key itself
// was never populated. Reproduced directly before fixing: `e := &audit.Entry
// {Action: ...}` followed by either `e.Metadata = map[string]any{"email":
// addr}` or `e.Metadata["email"] = addr` both resolved hasEmail=false,
// unresolved="" — a silent pass carrying an email, the same class as every
// prior round in this file.

// auditEntryLiteralBehindIdent unwraps a single leading `&` (the #0261
// pointer form) and reports whether what remains is an `audit.Entry{...}`
// composite literal, returning it if so. Shared by both cases
// compositeLitAssignedIdents matches below (`:=`/`=` and `var`) so the two
// forms stay behaviorally identical rather than two hand-written copies of
// the same unwrap-then-check drifting apart.
func auditEntryLiteralBehindIdent(rhs ast.Expr) (*ast.CompositeLit, bool) {
	if u, ok := rhs.(*ast.UnaryExpr); ok && u.Op == token.AND {
		rhs = u.X
	}
	cl, ok := rhs.(*ast.CompositeLit)
	if !ok || !isAuditEntryType(cl.Type) {
		return nil, false
	}
	return cl, true
}

// compositeLitAssignedIdents maps each audit.Entry composite literal in
// body that is the direct right-hand side of `ident := audit.Entry{...}`,
// `ident = audit.Entry{...}`, `ident := &audit.Entry{...}`,
// `ident = &audit.Entry{...}`, or — #0290 — `var ident = audit.Entry{...}`
// / `var ident = &audit.Entry{...}`, to that identifier's name.
//
// #0290: a `var` declaration was invisible here entirely — this function
// only ever inspected *ast.AssignStmt, and `var e = audit.Entry{...}` is a
// *ast.GenDecl (Tok == token.VAR) holding an *ast.ValueSpec, a different
// node shape the walk below never matched. That meant the whole
// #0252/#0257/#0261 deferred-mutation family — connecting a later
// `e.Metadata = ...` back to the literal that produced e — never engaged
// for a `var`-declared entry, even though collectDeferredMetadataFieldIdents
// (the OTHER half of that family) already saw the deferred assignment fine:
// `e.Metadata = ...` is the identical *ast.SelectorExpr regardless of how e
// was declared. Reproduced directly before fixing: `var e = audit.Entry{
// Action: ...}` followed by `e.Metadata = map[string]any{"email": addr}`
// resolved hasEmail=false, unresolved="" — a silent pass, the same class
// every prior round in this file closed for `:=`. Now matched the same way
// #0261 connected the pointer form: a `*ast.ValueSpec` with one Value per
// Name is walked exactly like an *ast.AssignStmt's paired Lhs/Rhs, reusing
// auditEntryLiteralBehindIdent so the pointer-unwrap logic is not
// duplicated. Only a VALUE-initialized var is matched (`var e = ...`, i.e.
// len(Names) == len(Values)) — a bare `var e audit.Entry` with no
// initializer has no literal on the right to connect, and Go's ValueSpec
// already represents that case with an empty Values slice, so the length
// check excludes it by construction rather than needing a separate case.
func compositeLitAssignedIdents(body *ast.BlockStmt) map[*ast.CompositeLit]string {
	result := map[*ast.CompositeLit]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			if len(node.Lhs) != len(node.Rhs) {
				return true
			}
			for i, lhs := range node.Lhs {
				ident, ok := lhs.(*ast.Ident)
				if !ok {
					continue
				}
				cl, ok := auditEntryLiteralBehindIdent(node.Rhs[i])
				if !ok {
					continue
				}
				result[cl] = ident.Name
			}
		case *ast.GenDecl:
			if node.Tok != token.VAR {
				return true
			}
			for _, spec := range node.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					cl, ok := auditEntryLiteralBehindIdent(vs.Values[i])
					if !ok {
						continue
					}
					result[cl] = name.Name
				}
			}
		}
		return true
	})
	return result
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

// isAuditEntrySliceOrArrayType reports whether t is `[]audit.Entry` or
// `[N]audit.Entry` — an *ast.ArrayType whose element type is audit.Entry.
// Go represents both a slice (Len == nil) and a fixed-size array (Len set)
// with the same node shape, and this check does not need to distinguish
// them: either can hold an elided-type element, the #0290 shape below.
func isAuditEntrySliceOrArrayType(t ast.Expr) bool {
	at, ok := t.(*ast.ArrayType)
	return ok && isAuditEntryType(at.Elt)
}

// isAuditEntryMapType reports whether t is `map[K]audit.Entry` for any key
// type K — an *ast.MapType whose value type is audit.Entry.
func isAuditEntryMapType(t ast.Expr) bool {
	mt, ok := t.(*ast.MapType)
	return ok && isAuditEntryType(mt.Value)
}

// elidedAuditEntryLiterals returns the set of *ast.CompositeLit nodes in
// body that are an ELIDED-type element of a `[]audit.Entry{{...}}` slice
// (or fixed-size array) literal, or an elided-type value in a
// `map[K]audit.Entry{"k": {...}}` map literal — Go's own syntax for
// "repeat the type the containing literal already established", which
// leaves the inner literal's own Type field nil.
//
// #0290: before this, an elided literal was not a recognized site AT ALL —
// isAuditEntryType type-asserts cl.Type as *ast.SelectorExpr naming
// "audit.Entry", and a nil Type fails that assertion the same way any
// other mismatched type would, so scanAuditEntrySites' own site-detection
// walk (see the `!isAuditEntryType(cl.Type) && !elided[cl]` check there)
// skipped the node outright — nothing to classify, not even a wrong
// classification. This is the sharpest of #0290's three gaps: a direct,
// inline `"email"` key inside an elided literal escapes with no deferred
// mutation involved at all, unlike the other two gaps this issue closes.
// Reproduced directly before fixing: `[]audit.Entry{{Action: ...,
// Metadata: map[string]any{"email": addr}}}` produced ZERO sites from
// scanAuditEntrySites — not a silent pass on a found site, an entirely
// invisible one.
//
// Scoped to one level of nesting (a container literal whose OWN Type is
// explicit, holding elements/values whose Type is elided) — matching
// exactly the shape #0290 names and the shape
// TestAuditEmailMetadataGuardDocumentedInvisibleShapes used to pin as
// invisible (that case is now moved into
// TestAuditEmailMetadataGuardCatchesElidedCompositeLiteral, since it is no
// longer invisible). A container whose own type is ALSO elided one level
// further out (`[][]audit.Entry{{{...}}}`, or an elided literal nested
// inside another elided literal) is not handled — no real site in this
// tree uses either shape (checked: `grep -rn '\[\]audit\.Entry\|map\[.*\]
// audit\.Entry' internal cmd --include='*.go'`, excluding _test.go, finds
// only the two already-fixed forms this file's own header documents), and
// widening this to arbitrary nesting depth would need a type-inference
// pass this file has repeatedly declined to add (see the go/types
// discussion in this issue's header paragraph and #0282's own decision).
func elidedAuditEntryLiterals(body *ast.BlockStmt) map[*ast.CompositeLit]bool {
	result := map[*ast.CompositeLit]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		switch {
		case isAuditEntrySliceOrArrayType(cl.Type):
			for _, elt := range cl.Elts {
				if inner, ok := elt.(*ast.CompositeLit); ok && inner.Type == nil {
					result[inner] = true
				}
			}
		case isAuditEntryMapType(cl.Type):
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if inner, ok := kv.Value.(*ast.CompositeLit); ok && inner.Type == nil {
					result[inner] = true
				}
			}
		}
		return true
	})
	return result
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
// (87 distinct keys across the 45 non-test audit.Entry sites — recomputed
// independently during #0237's fourth pass with a from-scratch walker that
// resolves credentialMetadata's return the same way classifyMetadataExpr
// does, matching the third phase-3 review's own independent count exactly;
// an earlier pass had written 86 here, which no independent recount
// reproduces) before adding anything: no key in the tree contains "mail" or
// "contact" as an underscore-delimited token today (the closest near-miss,
// "topic_arn", splits to "topic"/"arn" — neither collides), so those two
// are free — they match nothing in the tree today, closing the
// "user_mail"/"contact"-shaped gap the review named for zero new sites.
// "to" is NOT free in that same sense, and an earlier draft of this
// paragraph wrongly said it was: admin_campaign_preview.go:419 writes the
// literal key "to" TODAY (Metadata: map[string]any{"to": actor.Email, ...})
// — the exact site named three paragraphs above as the reason "to" was
// added in the first place. Adding "to" to this token set does not
// introduce a new site to auditEmailMetadataKnownSites, because that site
// is already pinned there via its OTHER suspect key,
// "recipient_email" — but the key "to" itself is real, present, and the
// entire motivation for this token, not absent from the tree the way
// "mail" and "contact" are.
//
// "recipientEmail" (camelCase) is deliberately NOT covered. This split is
// underscore-only, so a camelCase key is invisible to it by construction —
// closing that gap would need a second, camelCase-aware tokenizer, not a
// one-line token addition. Every one of the 87 real Metadata keys in this
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

// auditEmailMetadataMinPlausibleFileCount is #0261's answer, scoped to just
// this guard, to the #0275 fail-open shape: a tree-walking guard whose pass
// condition is "the findings slice is empty" cannot distinguish "the tree is
// clean" from "the walk visited nothing", so an emptied or narrowed
// auditEmailMetadataScanRoots would silently disarm this guard while the
// suite stays green — sites would read 0, unresolved would read 0, and
// TestAuditEntryEmailMetadataMatchesKnownSites would pass having checked
// nothing.
//
// #0275 corrected the population this floor is measured against.
// #0261's original 150 was sized against countVisitedGoFiles' raw walk
// count (everything walkGoFiles yields, _test.go included) — but this
// guard's own scanAuditEntrySites skips _test.go files (skipGoTestFile)
// AFTER the walk yields them, so the population it actually classifies is
// smaller: measured directly (#0261's review measured 108, this pass
// re-measured 110 — the ±2 drift #0261's own review already characterized
// as concurrent test-file churn, not a fitted constant), against a raw
// walked count of 255. A floor of 150 sized against the wrong (larger)
// population would now FAIL against the real tree the moment
// scanAuditEntrySites' own parsed-file count (len(files), returned
// alongside its sites) is used instead of a second, mismeasuring walk —
// which is exactly the fail-open-adjacent hazard #0275 criterion 4a warns
// about: a floor that reads as headroom while actually measuring nothing
// about the coverage it claims to protect.
//
// 80 is the corrected floor, chosen the same way 150 was: comfortably below
// the real parsed population (110, room for the tree to shrink without a
// false alarm) while still tripping if the roots were narrowed to any one
// package — internal/handlers alone has 40 non-test .go files, cmd/ alone
// has 3 — which is the "a real narrowing would trip it" bar #0275
// criterion 3 sets.
const auditEmailMetadataMinPlausibleFileCount = 80

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

	// #0261/#0275: assert the walk actually visited a plausible number of
	// files BEFORE trusting anything scanAuditEntrySites reports below — an
	// empty or narrowed roots list must be a hard failure here, never
	// silently read as "0 unresolved, 0 email sites, all clear". parsedFiles
	// is scanAuditEntrySites' own count of what it PARSED (len(files),
	// after its _test.go skip) — not a second, independently-filtered walk
	// that could drift out of sync with the skip logic it is meant to
	// measure (#0275 criterion 4a).
	sites, parsedFiles := scanAuditEntrySites(t, roots)
	assertGoFileVisitCountPlausible(t, "TestAuditEntryEmailMetadataMatchesKnownSites", auditEmailMetadataScanRoots, parsedFiles, auditEmailMetadataMinPlausibleFileCount)

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
	sites, _ := scanAuditEntrySites(t, []string{dir})
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
	sites, _ := scanAuditEntrySites(t, []string{dir})
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
	sites, _ := scanAuditEntrySites(t, []string{dir})
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

// TestAuditEmailMetadataGuardDocumentedSilentPassShapes pins the six
// silent-pass shapes named in this file's header comment: the original four
// found by #0237's third phase-3 review running eight synthetic Metadata
// expressions through scanAuditEntrySites rather than reading the code
// (reproduced again during the header's own correction), plus two more
// #0255 found — an audit.Entry held in a struct field, and Metadata set
// through a pointer passed to a helper — both structural blind spots the
// deferred-struct-field-assignment fix (#0252, tightened by #0255's own
// shape-1 fix) still cannot see. Each subtest asserts hasEmail=false,
// unresolved="" (a silent pass, not a FAILURE) for a shape the header now
// says is unresolved but not caught. None is exercised by any real site in
// this tree (see the header comment and the 45-site/87-key census it
// cites), so this is the same treatment
// TestAuditEmailMetadataGuardCamelCaseKeyNotCaught gives the camelCase gap:
// a deliberate, unfixed gap pinned as a named, asserted behavior rather than
// a silent absence of coverage. If any of these starts failing, either the
// gap was closed (update this test and the header to say so) or something
// about collectLocalMapKeys/classifyMetadataExpr regressed.
func TestAuditEmailMetadataGuardDocumentedSilentPassShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// The one exception this header used to document alone: a
			// composite-literal key that is not itself a string literal.
			name: "composite literal, non-string-literal key",
			src: `package fixture

const emailKeyConst = "email"

func recordSomething() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{emailKeyConst: addr},
	})
}
`,
		},
		{
			// A traced local's index assignment whose index key is itself
			// not a string literal — collectLocalMapKeys's IndexExpr case
			// only matches a literal index, so this assignment contributes
			// no key at all.
			name: "traced local, non-literal index key",
			src: `package fixture

func recordSomething() {
	metadata := map[string]any{"reason": reason}
	k := "email"
	metadata[k] = addr
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: metadata,
	})
}
`,
		},
		{
			// A traced local later mutated by a helper CALL rather than an
			// assignment statement — collectLocalMapKeys only inspects
			// *ast.AssignStmt, so a call that mutates the map in place is
			// invisible to it regardless of what it does.
			name: "traced local, mutated by a call rather than an assignment",
			src: `package fixture

func recordSomething() {
	metadata := map[string]any{"reason": reason}
	addEmailTo(metadata, addr)
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: metadata,
	})
}
`,
		},
		{
			// The sharpest one: an identifier first assigned from an
			// unresolvable call is FAILURE alone (see
			// TestAuditEmailMetadataGuardFailsOnUnresolvableMetadata's
			// sibling shape without the second line below) but becomes a
			// silent pass the instant one more string-literal index
			// assignment touches it — whatever the helper itself put in the
			// map, including an "email" key, is invisible.
			name: "helper-built local plus one further literal index assignment",
			src: `package fixture

func recordSomething() {
	metadata := buildMeta(sub)
	metadata["reason"] = r
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: metadata,
	})
}
`,
		},
		{
			// #0255, shape 2: the entry is held in a struct field rather than
			// a bare identifier. Both compositeLitAssignedIdents (`ident :=
			// audit.Entry{...}`) and collectDeferredMetadataFieldIdents
			// (`ident.Metadata = ...`) require the assignment's relevant side
			// to be a bare *ast.Ident; `w.e` is an *ast.SelectorExpr on both
			// sides here, so neither helper ever connects the empty literal
			// to the later field write. Not exercised by any real site in
			// this tree today (checked directly: no production audit.Entry
			// is held in a struct field — see the header comment's #0255
			// paragraph).
			name: "entry held in a struct field, not a bare identifier",
			src: `package fixture

type worker struct {
	e audit.Entry
}

func (w *worker) recordSomething() {
	w.e = audit.Entry{}
	w.e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, w.e)
}
`,
		},
		{
			// #0255, shape 3: Metadata is set through a pointer passed to a
			// helper, not through any assignment statement in the enclosing
			// function's own body. collectDeferredMetadataFieldIdents only
			// recognizes a direct `ident.Metadata = ...` *ast.AssignStmt; it
			// has no model of a function call mutating a field through a
			// pointer argument, so setMeta(&e) is invisible regardless of
			// what setMeta does inside it. Not exercised by any real site in
			// this tree today — checked directly the same way as the struct-
			// field shape above.
			name: "pointer mutation through a helper function",
			src: `package fixture

func setMeta(e *audit.Entry) {
	e.Metadata = map[string]any{"email": addr}
}

func recordSomething() {
	e := audit.Entry{}
	setMeta(&e)
	auditor.Record(ctx, e)
}
`,
		},
		{
			// #0290 ("also worth pinning"): a SECOND pointer alias — `p :=
			// &e` followed by `p.Metadata = ...` — is a variant of the
			// already-fixed pointer-held-entry shape (#0261) that still
			// escapes. compositeLitAssignedIdents maps the literal to "e"
			// (the identifier that directly holds `audit.Entry{...}`, a
			// value type here); collectDeferredMetadataFieldIdents marks
			// "p" (the identifier the LATER `.Metadata = ...` assignment's
			// selector names), not "e". Since the two names differ,
			// literalIdents[cl] ("e") is never found in
			// deferredMetadataIdents ("p" only), so the connection never
			// fires. Reproduced directly: SILENT PASS (hasEmail=false,
			// unresolved=""). Deliberately NOT fixed here — closing it
			// would mean tracking arbitrary pointer aliasing across
			// assignments, a different and open-ended kind of analysis from
			// the single-level `&`-unwrap #0261 added; no production site
			// takes a second reference to an audit.Entry before mutating it
			// (checked: `grep -rn ':= &e\b' internal cmd --include='*.go'`,
			// excluding _test.go, finds nothing that touches an audit.Entry).
			name: "second pointer alias (p := &e; p.Metadata = ...)",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{
		Action: audit.ActionSubscriberSuppressed,
	}
	p := &e
	p.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, e)
}
`,
		},
		{
			// #0290 ("also worth pinning"): an explicit dereference-then-
			// select, `(*e).Metadata = ...`, on a pointer-held entry.
			// collectDeferredMetadataFieldIdents' markMetadataField requires
			// the assignment's selector base (sel.X) to be a bare *ast.Ident;
			// `(*e)` is an *ast.ParenExpr wrapping an *ast.StarExpr, not an
			// *ast.Ident, so the type assertion fails and "e" is never added
			// to deferredMetadataIdents from this shape — even though the
			// SAME identifier's bare-selector form (`e.Metadata = ...`,
			// #0261's own fix) is already caught. Reproduced directly:
			// SILENT PASS. Not fixed: no production site writes this form
			// (checked: `grep -rn '(\*[a-zA-Z_][a-zA-Z0-9_]*)\.Metadata'
			// internal cmd --include='*.go'`, excluding _test.go, finds
			// nothing) — Go's selector syntax lets `e.Metadata` and
			// `(*e).Metadata` mean the same thing when e is a pointer, and
			// every real pointer-held site in this tree uses the shorter,
			// already-caught form.
			name: "explicit dereference before selector ((*e).Metadata = ...)",
			src: `package fixture

func recordSomething() {
	e := &audit.Entry{
		Action: audit.ActionSubscriberSuppressed,
	}
	(*e).Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, *e)
}
`,
		},
		{
			// #0290 ("also worth pinning") / #0282's own question: an
			// ALIASED import of "maps" (`import m "maps"`) calling
			// `m.Copy(...)`. isMapsCopyCall (and its #0290 siblings
			// isMapsInsertCall/isMapsDeleteFuncCall) match the literal
			// package identifier "maps" by name, with no import-alias
			// resolution — go/parser never resolves which package an
			// identifier refers to, only go/types does, and #0261/#0282
			// both declined go/types for this file (synthetic single-file
			// fixtures import nothing and would fail type-checking; #0282
			// additionally verified empirically that an untyped constant
			// resolves to "untyped string", not the named type, so type
			// analysis would not even answer the read-side classification
			// question). #0290 re-evaluated this decision specifically for
			// the two NEW matchers (isMapsInsertCall, isMapsDeleteFuncCall)
			// this issue adds and reached the same answer: nothing in this
			// tree imports "maps" aliased or dot-imported today (checked
			// directly: `grep -rn 'maps "maps"\|\. "maps"' internal cmd
			// --include='*.go'`, excluding _test.go, finds nothing), so
			// widening the match would buy coverage against a shape that
			// does not exist here at the cost of every synthetic fixture in
			// this file needing real imports. Reproduced directly: SILENT
			// PASS. The false-positive direction stays safe either way — a
			// LOCAL VARIABLE named "maps" calling .Copy(...) still resolves
			// "unresolved" (fails closed), since this guard has no scope
			// awareness in either direction.
			name: "aliased maps import (import m \"maps\"; m.Copy(...))",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"slug": s},
	}
	m.Copy(e.Metadata, extra)
	auditor.Record(ctx, e)
}
`,
		},
		{
			// #0290 ("also worth pinning"), the dot-import sibling of the
			// aliased-import case above: `import . "maps"` makes the call
			// site read as a bare `Copy(...)`, not a selector at all —
			// isMapsCopyCall's own type assertion (`call.Fun.(*ast.SelectorExpr)`)
			// fails outright, a different failure mode than the aliased
			// case (which IS a selector, just with the wrong package
			// identifier name) but the same silent-pass outcome. Same
			// decision as the aliased case: no production site dot-imports
			// "maps" (checked: `grep -rn '\. "maps"' internal cmd
			// --include='*.go'` finds nothing), and the false-positive
			// direction (a same-named local function called bare) still
			// fails closed via classifyMetadataExpr's unresolved default
			// for an unrecognized call, not this guard's Metadata-mutation
			// detection specifically.
			name: "dot-imported maps (import . \"maps\"; Copy(...))",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"slug": s},
	}
	Copy(e.Metadata, extra)
	auditor.Record(ctx, e)
}
`,
		},
		{
			// #0290 ("also worth pinning"): a factory function that
			// CONSTRUCTS the audit.Entry{} literal and returns it BY
			// POINTER, with the caller mutating Metadata afterward — the
			// mirror image of "pointer mutation through a helper function"
			// above (there, the literal is in the caller and the mutation
			// is in the helper; here, the literal is in the helper and the
			// mutation is in the caller). newEntry()'s own audit.Entry{}
			// literal IS found as a site and IS correctly classified in
			// isolation (its own body sets no Metadata at all, so
			// hasEmail=false, unresolved="" is the right answer for THAT
			// literal) — this is not a misclassification of a found site,
			// it is scanAuditEntrySites' fundamental scope boundary: it
			// analyzes Metadata mutations only within the SAME top-level
			// function body an audit.Entry{} literal appears in (stated in
			// this file's header), and recordSomething's `e.Metadata = ...`
			// is a different function's body than newEntry's literal. The
			// running code inserts a row carrying "email" that no site this
			// guard reports actually flags. Not fixed: doing so would
			// require inter-procedural analysis this guard has never
			// attempted at any point in its five prior rounds, and no
			// production site constructs an audit.Entry via a pointer-
			// returning factory (checked: every production audit.Entry{} in
			// this tree is either an inline literal or built and used
			// within one function — the same census the header cites).
			name: "factory returning *audit.Entry, caller mutates Metadata",
			src: `package fixture

func newEntry() *audit.Entry {
	return &audit.Entry{Action: audit.ActionSubscriberSuppressed}
}

func recordSomething() {
	e := newEntry()
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, *e)
}
`,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", c.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 1 {
				t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
			}
			if sites[0].unresolved != "" {
				t.Fatalf("expected this documented gap to resolve cleanly (not unresolved) — if it is now unresolved, the gap was closed in the opposite direction from expected; update this test and the header comment: %s", sites[0].unresolved)
			}
			if sites[0].hasEmail {
				t.Fatal("expected hasEmail=false: this is the documented silent-pass gap, not a bug — if this now fails, the gap was closed; update this test and the header comment to say so")
			}
		})
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
	sites, _ := scanAuditEntrySites(t, []string{dir})
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

// TestAuditEmailMetadataGuardCatchesVarDeclaredEntry proves #0290's first
// fix: a `var e = audit.Entry{...}` (and the pointer form, `var e =
// &audit.Entry{...}`) followed by a deferred `e.Metadata = ...` is now
// reported unresolved, the same way the `:=` form already was. Reproduced
// as a silent pass (hasEmail=false, unresolved="") before this fix landed.
func TestAuditEmailMetadataGuardCatchesVarDeclaredEntry(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	var e = audit.Entry{
		Action: audit.ActionSubscriberSuppressed,
	}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, e)
}

func recordSomethingElse() {
	var e = &audit.Entry{
		Action: audit.ActionSubscriberSuppressed,
	}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, *e)
}
`)
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 2 {
		t.Fatalf("expected exactly two audit.Entry sites, found %d", len(sites))
	}
	for _, s := range sites {
		if s.unresolved == "" {
			t.Fatalf("expected a var-declared entry with a deferred Metadata assignment to be reported unresolved, not silently classified as hasEmail=%v — this is the #0290 hole this test pins closed", s.hasEmail)
		}
	}
}

// TestAuditEmailMetadataGuardCatchesElidedCompositeLiteral proves #0290's
// second fix, the sharpest of the three: an elided-type element inside a
// []audit.Entry{{...}} slice literal now IS a recognized site, carrying an
// inline "email" key with no deferred mutation involved at all. Before this
// fix, scanAuditEntrySites found ZERO sites for this fixture — not a wrong
// classification of a found site, an entirely invisible one (reproduced
// directly). This case used to be pinned as a documented, invisible shape
// in TestAuditEmailMetadataGuardDocumentedInvisibleShapes; it moved here
// once the gap closed.
func TestAuditEmailMetadataGuardCatchesElidedCompositeLiteral(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSlice() {
	entries := []audit.Entry{{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"email": addr},
	}}
	auditor.RecordAll(ctx, entries)
}

func recordMap() {
	entries := map[string]audit.Entry{
		"a": {
			Action:   audit.ActionSubscriberSuppressed,
			Metadata: map[string]any{"email": addr},
		},
	}
	auditor.RecordMap(ctx, entries)
}
`)
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 2 {
		t.Fatalf("expected exactly two audit.Entry sites (one in the slice literal, one in the map literal), found %d", len(sites))
	}
	for _, s := range sites {
		if s.unresolved != "" {
			t.Fatalf("expected the elided literal to resolve cleanly, got unresolved: %s", s.unresolved)
		}
		if !s.hasEmail {
			t.Fatal("expected hasEmail=true: the elided literal carries an inline \"email\" key directly — this is the #0290 hole this test pins closed")
		}
	}
}

// TestAuditEmailMetadataGuardCatchesMapsInsertAndFriends proves #0290's
// third fix: maps.Insert(ident.Metadata, seq) — the key-introducing sibling
// of maps.Copy that #0261 left unmatched — is now reported unresolved,
// matching maps.Copy's own treatment. Reproduced directly before fixing: a
// "slug"-only literal followed by maps.Insert resolved hasEmail=false,
// unresolved="". maps.DeleteFunc and clear() are also matched (both
// removal-only, the safe direction delete already is), pinned here too so
// a future narrowing of any of the three is caught by a fixture, not left
// to a real site.
func TestAuditEmailMetadataGuardCatchesMapsInsertAndFriends(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordInsert() {
	e := audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"slug": s},
	}
	maps.Insert(e.Metadata, seq)
	auditor.Record(ctx, e)
}

func recordDeleteFunc() {
	e := audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"slug": s},
	}
	maps.DeleteFunc(e.Metadata, pred)
	auditor.Record(ctx, e)
}

func recordClear() {
	e := audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"slug": s},
	}
	clear(e.Metadata)
	auditor.Record(ctx, e)
}
`)
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 3 {
		t.Fatalf("expected exactly three audit.Entry sites, found %d", len(sites))
	}
	for _, s := range sites {
		if s.unresolved == "" {
			t.Fatalf("expected %s to be reported unresolved once a maps.Insert/maps.DeleteFunc/clear() call touches its Metadata — this is the #0290 hole this test pins closed", s.action)
		}
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
	sites, _ := scanAuditEntrySites(t, []string{dir})
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
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
	}
	if sites[0].unresolved == "" {
		t.Fatal("expected an unresolvable-call Metadata expression to be reported as unresolved, not silently classified")
	}
}

// TestAuditEmailMetadataGuardFailsOnDeferredStructFieldAssignment proves the
// fix for #0252's blocking finding: `e := audit.Entry{}` followed by
// `e.Metadata = map[string]any{"email": addr}` used to resolve as
// hasEmail=false, unresolved="" — SEEN (the empty literal is a real site)
// but a SILENT PASS, because scanAuditEntrySites only ever read Metadata out
// of the composite literal's own Elts, never out of a later struct-field
// assignment on the identifier it was assigned to. That was the one shape,
// of the four #0252 named, that was genuinely live risk rather than a
// structural blind spot: it is the natural shape a future refactor reaches
// for when an entry needs building across a few lines, and it is exactly
// the kind of hole this guard's own conservative philosophy — unclassifiable
// fails, it does not pass — exists to close everywhere else.
// collectDeferredMetadataFieldIdents + compositeLitAssignedIdents together
// now catch it: this asserts the site comes back unresolved rather than
// silently classified as email-free.
func TestAuditEmailMetadataGuardFailsOnDeferredStructFieldAssignment(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	e := audit.Entry{}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, e)
}
`)
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site (the empty literal itself), found %d", len(sites))
	}
	if sites[0].unresolved == "" {
		t.Fatal("expected a struct-field Metadata assignment after construction to be reported as unresolved, not silently classified as hasEmail=false — this is the #0252 hole this test pins closed")
	}
}

// TestAuditEmailMetadataGuardFailsWhenLiteralAlsoSetsMetadataThenOverridden
// proves the fix for #0255's shape 1, the priority finding of that issue: a
// literal that sets its OWN Metadata key AND is later mutated with a
// struct-field .Metadata assignment used to resolve as hasEmail=false,
// unresolved="" — a silent pass carrying whatever hasEmail value the
// literal's map happened to produce, which is not the value the running
// code actually inserts (the later assignment is). The #0252 deferred-
// assignment check only ran `if !metadataKeyFound`, so a literal that ALSO
// set Metadata skipped it entirely; #0255 removed that condition. This
// fixture is exactly the one-line variant #0255 named: a "slug"-only
// literal immediately overridden by an "email"-carrying assignment.
func TestAuditEmailMetadataGuardFailsWhenLiteralAlsoSetsMetadataThenOverridden(t *testing.T) {
	dir := t.TempDir()
	writeSyntheticGoFile(t, dir, "fixture.go", `package fixture

func recordSomething() {
	e := audit.Entry{Metadata: map[string]any{"slug": s}}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, e)
}
`)
	sites, _ := scanAuditEntrySites(t, []string{dir})
	if len(sites) != 1 {
		t.Fatalf("expected exactly one audit.Entry site (the literal itself), found %d", len(sites))
	}
	if sites[0].unresolved == "" {
		t.Fatal("expected the site to be reported as unresolved — the literal's own \"slug\"-only Metadata is not what the running code uses; the later e.Metadata = ... assignment is, and this guard cannot read its content. Silently passing this as hasEmail=false is the #0255 shape-1 hole this test pins closed")
	}
}

// TestAuditEmailMetadataGuardFailsOnDeferredIndexAssignment proves the fix
// for #0257, the eighth shape found in this file: `e.Metadata["email"] =
// addr` — an index assignment directly on the struct field, LHS an
// *ast.IndexExpr wrapping the *ast.SelectorExpr `e.Metadata` — used to
// resolve as hasEmail=false, unresolved="" (a silent pass), because
// collectDeferredMetadataFieldIdents only recognized a bare
// `ident.Metadata = ...` *ast.SelectorExpr assignment, not this one level of
// indirection. The identical mutation through a bare local variable (`m :=
// map[string]any{"slug": s}; m["email"] = addr; e := audit.Entry{Metadata:
// m}`) was already caught, by collectLocalMapKeys's own *ast.IndexExpr case
// — see TestAuditEmailMetadataGuardCatchesTracedLocalVariable, which pins
// that shape stays caught. Both fixtures below use the SAME "slug"-only
// literal immediately mutated by an "email"-carrying index assignment, on a
// second, unrelated key ("phone") too, proving the fix is not keyed to the
// literal string "email" — any index triggers it, matching
// collectDeferredMetadataFieldIdents' field-assignment case, which likewise
// never reads what it's assigned.
func TestAuditEmailMetadataGuardFailsOnDeferredIndexAssignment(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "email key",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{Metadata: map[string]any{"slug": s}}
	e.Metadata["email"] = addr
	auditor.Record(ctx, e)
}
`,
		},
		{
			// Proves the fix is general-purpose, not special-cased to the
			// literal "email" — an unrelated key must be caught identically,
			// since this guard cannot prove WHICH keys a deferred index
			// assignment ends up contributing, only that one occurred.
			name: "unrelated key",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{Metadata: map[string]any{"slug": s}}
	e.Metadata["phone"] = p
	auditor.Record(ctx, e)
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", tc.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 1 {
				t.Fatalf("expected exactly one audit.Entry site (the literal itself), found %d", len(sites))
			}
			if sites[0].unresolved == "" {
				t.Fatal("expected the site to be reported as unresolved — the literal's own \"slug\"-only Metadata is not what the running code uses; the later e.Metadata[...] = ... index assignment is, and this guard cannot read its content. Silently passing this as hasEmail=false is the #0257 hole this test pins closed")
			}
		})
	}
}

// TestAuditEmailMetadataGuardFailsOnPointerHeldEntry proves the fix for
// #0261's primary shape: an audit.Entry taken by pointer
// (`e := &audit.Entry{...}`) followed by either deferred-mutation form —
// `e.Metadata = ...` (the #0252 shape) or `e.Metadata[...] = ...` (the
// #0257 shape) — one indirection out from what those two fixes covered.
// Before this fix, both resolved hasEmail=false, unresolved="": the break
// was in compositeLitAssignedIdents, which required an *ast.AssignStmt's
// RHS to be an *ast.CompositeLit directly and so never connected
// `&audit.Entry{...}`'s *ast.UnaryExpr-wrapped literal back to the
// identifier holding it.
func TestAuditEmailMetadataGuardFailsOnPointerHeldEntry(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "pointer-held, struct-field assignment",
			src: `package fixture

func recordSomething() {
	e := &audit.Entry{Action: audit.ActionSubscriberSuppressed}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, *e)
}
`,
		},
		{
			name: "pointer-held, index assignment",
			src: `package fixture

func recordSomething() {
	e := &audit.Entry{Action: audit.ActionSubscriberSuppressed}
	e.Metadata["email"] = addr
	auditor.Record(ctx, *e)
}
`,
		},
		{
			// Proves the fix handles `ident = &audit.Entry{...}` (plain
			// assignment), not only `:=`, since compositeLitAssignedIdents
			// matches both forms for the value-typed case and must for the
			// pointer form too.
			name: "pointer-held via plain assignment, not :=",
			src: `package fixture

func recordSomething() {
	var e *audit.Entry
	e = &audit.Entry{Action: audit.ActionSubscriberSuppressed}
	e.Metadata = map[string]any{"email": addr}
	auditor.Record(ctx, *e)
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", tc.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 1 {
				t.Fatalf("expected exactly one audit.Entry site (the pointer literal itself), found %d", len(sites))
			}
			if sites[0].unresolved == "" {
				t.Fatal("expected the site to be reported as unresolved — a pointer-held audit.Entry whose Metadata is set after construction is exactly as unreadable to this guard as the value-typed form #0252/#0257 already cover. Silently passing this as hasEmail=false is the #0261 hole this test pins closed")
			}
		})
	}
}

// TestAuditEmailMetadataGuardFailsOnMetadataMapMutatingCalls proves the fix
// for #0261's second finding: maps.Copy(e.Metadata, src) and
// delete(e.Metadata, k) both mutate a Metadata map through a plain function
// call, with no *ast.AssignStmt anywhere for collectDeferredMetadataFieldIdents'
// pre-#0261 cases to match — so neither was visible to this guard at all.
// maps.Copy is the consequential subtest: it can introduce an "email" key
// from a variable this guard never inspects, and before this fix a
// "slug"-only literal followed by maps.Copy(e.Metadata, extra) resolved
// hasEmail=false, unresolved="" — a silent pass carrying whatever extra
// happened to hold. delete cannot itself introduce a key (it can only
// remove one), so it is included for completeness against the issue's own
// acceptance criteria rather than because it can smuggle an email past this
// guard; both are treated identically because this guard's answer to
// "cannot prove what the running code ends up with" does not depend on
// which direction a specific gap points.
func TestAuditEmailMetadataGuardFailsOnMetadataMapMutatingCalls(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			name: "maps.Copy",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{Metadata: map[string]any{"slug": s}}
	maps.Copy(e.Metadata, extra)
	auditor.Record(ctx, e)
}
`,
		},
		{
			name: "delete",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{Metadata: map[string]any{"slug": s, "email": addr}}
	delete(e.Metadata, "slug")
	auditor.Record(ctx, e)
}
`,
		},
		{
			// Both forms should be caught on a pointer-held entry too — the
			// same shape #0261's primary finding closes, composed with the
			// call-based mutation this test's other subtests pin.
			name: "maps.Copy on a pointer-held entry",
			src: `package fixture

func recordSomething() {
	e := &audit.Entry{Metadata: map[string]any{"slug": s}}
	maps.Copy(e.Metadata, extra)
	auditor.Record(ctx, *e)
}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", tc.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 1 {
				t.Fatalf("expected exactly one audit.Entry site (the literal itself), found %d", len(sites))
			}
			if sites[0].unresolved == "" {
				t.Fatal("expected the site to be reported as unresolved — a call that mutates the Metadata map in place (maps.Copy or delete) is exactly as unreadable to this guard as a deferred assignment; silently classifying it from the literal's own Metadata alone is the #0261 hole this test pins closed")
			}
		})
	}
}

// TestAuditEmailMetadataGuardDocumentedFalsePositiveShapes pins the two
// false-positive shapes #0255 found in the #0252/#0255 deferred-assignment
// check: collectDeferredMetadataFieldIdents matches a later .Metadata =
// assignment by identifier NAME across the whole function body, with no Go
// scope resolution and no statement-order awareness, so it also fires on an
// innocent site that happens to share a name with an unrelated variable, or
// whose .Metadata = assignment textually precedes the literal it gets
// attributed to. Both fail closed (unresolved, never a silent pass), which
// is the safe direction — this test does not change that — but pins that
// the failure message describes what was actually found (a same-named
// struct-field assignment somewhere in the function) rather than asserting
// a same-variable runtime override it has not established. If either
// subtest starts passing cleanly (unresolved==""), scope or statement-order
// awareness was added and this test (and the header) should be updated to
// say so.
func TestAuditEmailMetadataGuardDocumentedFalsePositiveShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// A same-named variable of an unrelated type, in a disjoint
			// block of the SAME function, has its own field also named
			// Metadata set — collectDeferredMetadataFieldIdents cannot tell
			// this "e" is not the audit.Entry "e" below.
			name: "unrelated same-named variable in a disjoint block",
			src: `package fixture

func recordSomething() {
	e := audit.Entry{Action: audit.ActionSubscriberSuppressed}
	if false {
		var e otherType
		e.Metadata = something
	}
	auditor.Record(ctx, e)
}
`,
		},
		{
			// The .Metadata = assignment textually precedes the literal it
			// gets attributed to. In real Go this is a genuine false
			// positive: `e = audit.Entry{...}` below discards whatever the
			// earlier e.Metadata = ... wrote, since it replaces the whole
			// struct value.
			name: "struct-field assignment precedes the literal, not follows it",
			src: `package fixture

func recordSomething() {
	var e audit.Entry
	e.Metadata = map[string]any{"reason": r}
	e = audit.Entry{Action: audit.ActionSubscriberSuppressed}
	auditor.Record(ctx, e)
}
`,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", c.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 1 {
				t.Fatalf("expected exactly one audit.Entry site, found %d", len(sites))
			}
			if sites[0].unresolved == "" {
				t.Fatal("expected this false positive to still fail closed (unresolved != \"\") — if it now resolves cleanly, scope/ordering awareness was added; update this test and the header to say so")
			}
			if !strings.Contains(sites[0].unresolved, "possibly") {
				t.Fatalf("message should hedge (\"possibly ...\") rather than assert a same-variable override this lookup has not established: %s", sites[0].unresolved)
			}
		})
	}
}

// TestAuditEmailMetadataGuardDocumentedInvisibleShapes pins the remaining
// two of the three structural blind spots #0252 found and this file's
// header documents: shapes where scanAuditEntrySites finds ZERO sites at
// all, so a real audit.Entry{} written this way contradicts neither
// auditEmailMetadataKnownSites nor the "unresolved" failure path — it is
// simply never visited. Each subtest asserts len(sites) == 0. #0290 fixed
// the third shape this test used to pin (the elided-type slice/map element
// — see the header's correction and
// TestAuditEmailMetadataGuardCatchesElidedCompositeLiteral, which now
// covers it as a caught shape instead). Neither of the two remaining is
// exercised by any real site in this tree today (checked directly while
// writing this pin, the same way TestAuditEmailMetadataGuardDocumentedSilentPassShapes'
// four shapes were): the same treatment given every other documented,
// deliberate gap in this file — a named, asserted absence rather than a
// silent one. If either of these starts finding a site, either the walk was
// widened (update this test and the header to say so) or something about
// how scanAuditEntrySites locates audit.Entry{} literals regressed.
func TestAuditEmailMetadataGuardDocumentedInvisibleShapes(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			// A package-level var initialized directly from an audit.Entry{}
			// literal. The outer walk only descends into composite literals
			// found INSIDE a *ast.FuncDecl's body; a package-level
			// *ast.GenDecl/*ast.ValueSpec is never inspected for one.
			name: "package-level var composite literal",
			src: `package fixture

var seedEntry = audit.Entry{
	Action:   audit.ActionSubscriberSuppressed,
	Metadata: map[string]any{"email": addr},
}
`,
		},
		{
			// An audit.Entry{} literal inside a func literal assigned to a
			// package-level var. The literal's body is reachable syntactically
			// (it is still part of the file), but nothing walks INTO a
			// *ast.FuncLit outside a *ast.FuncDecl — the outer walk keys
			// specifically on *ast.FuncDecl nodes.
			name: "audit.Entry inside a func literal on a package-level var",
			src: `package fixture

var recordFn = func() {
	auditor.Record(ctx, audit.Entry{
		Action:   audit.ActionSubscriberSuppressed,
		Metadata: map[string]any{"email": addr},
	})
}
`,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			writeSyntheticGoFile(t, dir, "fixture.go", c.src)
			sites, _ := scanAuditEntrySites(t, []string{dir})
			if len(sites) != 0 {
				t.Fatalf("expected 0 sites (this shape is invisible to the walk, the documented gap this test pins), found %d", len(sites))
			}
		})
	}
}
