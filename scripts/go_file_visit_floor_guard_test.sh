#!/usr/bin/env bash
#
# go_file_visit_floor_guard_test.sh — external oracle for the three
# "plausible file count" floor constants #0275 added to internal/handlers'
# citation/dangling-citation/audit-metadata guard family, per issue #0300 —
# PLUS (further down, its own clearly-delimited section) the two "plausible
# call-site count" floor constants in internal/outbox's claim-kinds guard
# (#0281, #0304), which #0304 chose to fold in here rather than build as a
# second script, for the reasons that section's own header explains.
#
# THE PROBLEM (#0300)
#
# #0275 made five internal/handlers guards assert they parsed a plausible
# number of .go files before trusting an empty findings slice, closing a
# fail-open where a narrowed or emptied scan-roots list silently reported
# "no problems found". But the floors themselves --
# citedTestScanRootsMinPlausibleFileCount (150), citationGuardMinPlausible-
# FileCount (80), and auditEmailMetadataMinPlausibleFileCount (80) -- are
# ordinary `const` declarations living in the exact files they guard.
# Setting any one of them to 0 in the tracked source leaves the whole
# `internal/handlers` suite green: nothing outside that file's own `go test`
# run ever re-checks that the number is sane.
#
# #0275's own TestGoFileVisitCountGuardFiresOnEmptyOrLowCount cannot close
# this -- deliberately: it calls goFileVisitCountImplausible with its OWN
# literals (0, 3, 150), not any guard's real constant, which is what keeps
# it oracle-independent. It proves the mechanism fires; it cannot pin a
# value. CLAUDE.md §8 names the general shape ("a guard inside the file it
# guards becomes new mutable surface") and the remedy: an external harness
# that mutates a COPY of the subject and checks the result from outside,
# exactly as scripts/db_reset_guard_test.sh, scripts/testdb_gc_guard_test.sh,
# and scripts/dev_guard_test.sh already do for their own subjects. This
# script is that harness for the three floor constants.
#
# WHY THIS SCRIPT NEVER RUNS `go test` AS ITS ORACLE
#
# It is tempting to "mutate the constant to 0 in a copy, run `go test`, and
# assert it fails" -- but that is mathematically impossible to satisfy for
# THIS mutation and must not be attempted. goFileVisitCountImplausible's
# floor check is `if got < floor { return failure }`. With floor forced to
# 0 and the real scan roots left intact, `got` (a real, non-negative file
# count) is never less than 0, so the check can never fire -- the mutated
# `go test` run stays GREEN. That is not a bug in this script's reasoning;
# it is a precise restatement of #0300's own description ("leaves the whole
# package green"). Chasing a `go test` exit code as the pass/fail oracle for
# this specific mutation would therefore either (a) silently degenerate
# into asserting something already true regardless of the floor, which is
# the "guard that agrees with itself" failure CLAUDE.md §8 warns about, or
# (b) require ALSO mutating the scan-roots list in the same step to force a
# failure some other way, which would stop isolating what a floor-only
# regression does and start re-proving #0275's already-covered roots-empty
# case instead.
#
# So the oracle here is NOT Go's test framework. It is this script's own
# independent, from-scratch computation, in a different language (bash) over
# a different data source (a live `find` over the tree, not the guard's own
# `filepath.WalkDir`) than the mechanism it is checking on. A single edit to
# the Go source cannot touch both at once. Per CLAUDE.md §8 ("a copy of the
# answer stored next to the question is not a check" / "can an edit to the
# subject also satisfy the oracle"), that independence -- not merely being
# "external" in the sense of living in a different file -- is what this
# script is actually for.
#
# WHAT IT PROVES, per floor, per #0300's acceptance criteria:
#
#   1. The floor is extracted from a COPY of the real, tracked file (never
#      the tracked file itself -- this script only ever reads it), with the
#      constant's numeric value re-extracted via the same grep pipeline
#      whether the copy is unmutated or mutated -- never a hand-typed "0"
#      substituted for what a mutation "would" produce (CLAUDE.md §8's
#      "assert the extraction produced something" and "oracle must not be
#      the same bytes as its subject" both apply: the number this script
#      judges always comes from parsing bytes on disk, in both the real and
#      mutated case, through the identical extraction path).
#   2. The REAL, committed floor is judged PLAUSIBLE against a population
#      this script recomputes itself, from outside Go, via `find` --
#      replicating each guard's own filter (skip node_modules/dist, .go
#      suffix, and for the two 80-floors, also excluding _test.go, since
#      #0275 criterion 4a requires the PARSED population, not the merely
#      WALKED one -- citedTestScanRoots's three guards walk and use every
#      .go file including tests; citationGuardMinPlausibleFileCount and
#      auditEmailMetadataMinPlausibleFileCount both parse only non-test
#      files, per scanDirForCitations and scanAuditEntrySites respectively).
#   3. A floor of 0 (the mutation) is judged IMPLAUSIBLE against that same
#      externally-measured population -- one mutation per floor, each named
#      by both its constant and the guard test(s) it protects.
#   4. The margin: PLAUSIBLE requires 0 < floor <= population AND
#      floor >= population/2. The upper bound (floor <= population) is
#      #0275's own failure mode restated as a guard -- criterion 2 of
#      #0300 names it directly: "the failure mode that forced #0275 to
#      lower 150 to 80" was a floor raised ABOVE the real population, which
#      makes the guard permanently fail even with nothing wrong. The lower
#      bound (floor >= half the population) is this script's own margin: a
#      floor that clears "greater than zero" but sits far below the real
#      count (say, 5 out of 258) offers only token protection -- most of a
#      narrowing attack would go undetected before the floor ever tripped.
#      Half is comfortable slack for ordinary repo growth between reviews
#      (every committed floor here clears it: 150/258 = 58%, 80/111 = 72%,
#      80/110 = 73%) while still catching a floor that has drifted badly out
#      of proportion to what it is meant to protect.
#
#      GROWTH CEILING, not just a shrink/lower-too-far detector: the same
#      lower bound also fails if the population grows too far ABOVE a
#      static floor, with no code change at all. For
#      citedTestScanRootsMinPlausibleFileCount=150 against today's
#      population of 258 `internal cmd web` .go files (150/258 = 58%),
#      `floor_plausible`'s integer-division `half=$((population/2))` trips
#      once population reaches 302 -- roughly 44 ordinary new .go files away
#      at today's count, not a distant hypothetical for an actively
#      developed tree. That is a legitimate, intended trip (the floor
#      really has drifted out of proportion once the population has grown
#      that far past it) and not a bug in this script, but it means a
#      passing run today is not evidence the floor stays passing after a
#      few dozen ordinary commits -- raising the constant in the guarded Go
#      source, not loosening this script's margin, is the correct response
#      when it fires for that reason. This script does not track how close
#      the ceiling is; if that becomes a recurring nuisance, a WARN band
#      (e.g. population > 1.5 * floor) reported alongside PASS would be a
#      reasonable follow-up, but is not implemented here since it is
#      outside #0300's scope and no such follow-up has been needed yet.
#
# This script touches no database and changes nothing under version control
# -- it only reads the three tracked files (never edits them) and writes
# mutated copies into a private mktemp(1) directory, removed on exit.
#
# Usage: scripts/go_file_visit_floor_guard_test.sh
# Exit 0 = all floors plausible and all zero-mutations correctly rejected.
# Exit 1 = a regression was detected (message names it).

set -uo pipefail  # NOT -e: several checks here are expected to "fail" (that's the assertion)

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORKDIR="$(mktemp -d)"
FAILURES=0

# shellcheck disable=SC2329  # invoked indirectly via `trap cleanup EXIT` below
cleanup() {
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }
fatal() { printf 'FATAL: %s\n' "$1" >&2; exit 1; }

command -v find   >/dev/null || fatal "find not on PATH"
command -v grep   >/dev/null || fatal "grep not on PATH"
command -v sed    >/dev/null || fatal "sed not on PATH"
command -v shasum >/dev/null || fatal "shasum not on PATH -- required for the byte-identity check at the end of this script; without it, the check would compare two empty strings and PASS having measured nothing (CLAUDE.md §8's fail-open shape)"
command -v awk    >/dev/null || fatal "awk not on PATH -- required alongside shasum for the byte-identity check"

# extract_const <file> <const-name> -- reads FILE (real or mutated copy,
# never assumed) and prints the integer value of `const <const-name> = N`.
# Calls fatal() rather than returning an empty/guessed value if the pattern
# is not found or is not a plain non-negative integer -- an extraction that
# silently produced nothing must never be treated as "0 files" or "floor 0"
# by accident (CLAUDE.md §8).
#
# CAUTION for every call site: this function is always invoked inside a
# `$(...)` command substitution (e.g. `x="$(extract_const ...)"`), so its
# internal `fatal` -> `exit 1` only terminates that subshell, not this
# script's own process -- bash still propagates the subshell's exit status
# to the assignment, but only if the caller checks it. A bare
# `x="$(extract_const ...)"` with no `||` would leave $x empty and let the
# script carry on, eventually reaching floor_plausible's `-gt`/`-le` on an
# empty string, which fails closed too (a non-numeric `[` comparison is an
# error, not a pass) but reports a misleading "REGRESSION" verdict instead
# of the real FATAL reason. So every call site below is written
# `x="$(extract_const ...)" || fatal "..."` to make the fatal genuinely
# fatal for the whole script, not just the subshell.
extract_const() {
  local file="$1" name="$2" line value
  line="$(grep -E "^const[[:space:]]+${name}[[:space:]]*=[[:space:]]*[0-9]+[[:space:]]*$" "$file")"
  if [ -z "$line" ]; then
    fatal "extract_const: no 'const ${name} = <int>' line found in $file -- the file's shape changed, or the mutation did not take. Refusing to guess."
  fi
  value="$(printf '%s\n' "$line" | grep -oE '[0-9]+' | tail -1)"
  case "$value" in
    '' | *[!0-9]*) fatal "extract_const: extracted a non-numeric value ('${value}') for ${name} from $file" ;;
  esac
  printf '%s' "$value"
}

# count_go_files <include-test: yes|no> <root> [root...] -- the from-outside-
# Go population: a fresh `find` over the given repo-root-relative dirs,
# skipping node_modules/ and dist/ (the same two names skipVendoredDir /
# skipVendoredDirNames prune in the Go source), counting .go files. With
# include-test=no, _test.go files are excluded, matching the PARSED
# population #0275 criterion 4a requires for the two 80-floor guards.
count_go_files() {
  local include_test="$1"; shift
  local n
  if [ "$include_test" = "yes" ]; then
    n="$(find "$@" \( -name node_modules -o -name dist \) -prune -o -type f -name '*.go' -print | wc -l | tr -d ' ')"
  else
    n="$(find "$@" \( -name node_modules -o -name dist \) -prune -o -type f -name '*.go' -print | grep -vc '_test\.go$')"
  fi
  printf '%s' "$n"
}

# sha_of <file> -- prints a validated sha256 hex digest for FILE. Refuses to
# return an empty or malformed value: absent `shasum`, `shasum -a 256 ... |
# awk '{print $1}'` would previously produce an empty string, and comparing
# two empty strings at the end of this script would PASS having measured
# nothing (CLAUDE.md §8's "assert the extraction produced something" -- an
# empty result must be an error, never an input). The preflight `command -v
# shasum`/`command -v awk` checks above catch the missing-tool case before
# this ever runs; this function is the second, independent layer that also
# catches a `shasum` that produced something present but not a real digest.
sha_of() {
  local f="$1" digest
  digest="$(shasum -a 256 "$f" | awk '{print $1}')"
  case "$digest" in
    '' | *[!0-9a-fA-F]*) fatal "sha_of: shasum/awk produced an empty or non-hex digest ('${digest}') for $f -- refusing to use it in the byte-identity check." ;;
  esac
  if [ "${#digest}" -ne 64 ]; then
    fatal "sha_of: digest for $f is ${#digest} hex chars, not the 64 a sha256 digest must be ('${digest}') -- refusing to use it in the byte-identity check."
  fi
  printf '%s' "$digest"
}

# floor_plausible <floor> <population> -- this script's own oracle, entirely
# independent of goFileVisitCountImplausible (different language, different
# walk, different bound). See the margin rationale in the header comment.
floor_plausible() {
  local floor="$1" population="$2" half
  [ "$floor" -gt 0 ] || return 1
  [ "$floor" -le "$population" ] || return 1
  half=$((population / 2))
  [ "$floor" -ge "$half" ] || return 1
  return 0
}

# Each floor: const name | tracked file (repo-relative) | population kind
# (yes = include _test.go, matching walkGoFiles' raw yield; no = non-test
# only, matching the parsed population) | space-separated repo-relative
# scan roots | the guard test name(s) this floor protects, for messages.
FLOOR_NAMES=(citedTestScanRootsMinPlausibleFileCount citationGuardMinPlausibleFileCount auditEmailMetadataMinPlausibleFileCount)
FLOOR_FILES=(internal/handlers/dangling_test_citation_guard_test.go internal/handlers/citation_guard_test.go internal/handlers/audit_email_metadata_guard_test.go)
FLOOR_INCLUDE_TEST=(yes no no)
FLOOR_ROOTS=("internal cmd web" "internal cmd web" "internal cmd")
FLOOR_TESTS=(
  "TestNoCommentCitesUnresolvedPathOrSection, TestNoCommentCitesUndefinedTestFunction, TestNoDocCommentNamesADifferentDeclarationInSameFile"
  "TestNoAdminFacingStringCitesInternalDocs"
  "TestAuditEntryEmailMetadataMatchesKnownSites"
)

SHA_BEFORE=""
for f in "${FLOOR_FILES[@]}"; do
  [ -f "$REPO/$f" ] || fatal "$f not found -- has it moved? Update FLOOR_FILES."
  digest="$(sha_of "$REPO/$f")" || fatal "sha_of fatal-exited while hashing $REPO/$f -- see the FATAL line above. Aborting rather than letting an empty digest silently join SHA_BEFORE (sha_of's own fatal only exits its command-substitution subshell, same as extract_const -- see that function's header note -- so this call site must check the exit status itself)."
  SHA_BEFORE="${SHA_BEFORE}${digest} "
done

i=0
while [ "$i" -lt "${#FLOOR_NAMES[@]}" ]; do
  CONST="${FLOOR_NAMES[$i]}"
  REL_FILE="${FLOOR_FILES[$i]}"
  SRC="$REPO/$REL_FILE"
  INCLUDE_TEST="${FLOOR_INCLUDE_TEST[$i]}"
  ROOTS_REL="${FLOOR_ROOTS[$i]}"
  TESTS="${FLOOR_TESTS[$i]}"

  ABS_ROOTS=()
  # shellcheck disable=SC2086  # deliberate word-splitting: ROOTS_REL is a space-separated list of bare dir names, none containing spaces
  for r in $ROOTS_REL; do
    ABS_ROOTS+=("$REPO/$r")
  done

  echo "== ${CONST} (protects: ${TESTS}) =="

  POP="$(count_go_files "$INCLUDE_TEST" "${ABS_ROOTS[@]}")"
  case "$POP" in
    '' | *[!0-9]*) fatal "count_go_files returned a non-numeric population ('${POP}') for ${CONST}" ;;
  esac

  REAL_VALUE="$(extract_const "$SRC" "$CONST")" || fatal "extract_const fatal-exited while extracting ${CONST} from $SRC -- see the FATAL line above for the reason. Aborting the whole run rather than letting an empty extraction fall through to floor_plausible and print a misleading REGRESSION verdict."
  if floor_plausible "$REAL_VALUE" "$POP"; then
    pass "committed ${CONST}=${REAL_VALUE} is plausible against an externally-measured population of ${POP} (>= half, <= population)"
  else
    fail "REGRESSION #0300: committed ${CONST}=${REAL_VALUE} is NOT plausible against an externally-measured population of ${POP} -- either it was lowered too far, raised above the real population (#0275's own failure mode), the tree shrank, or the tree grew past the floor's margin (floor must stay >= population/2; see the header comment's growth-ceiling note). Affects: ${TESTS}"
  fi

  MUTANT="$WORKDIR/$(basename "$REL_FILE")"
  sed "s/const ${CONST} = [0-9][0-9]*/const ${CONST} = 0/" "$SRC" > "$MUTANT"
  if ! grep -q "const ${CONST} = 0" "$MUTANT"; then
    fatal "mutation did not take on the copy of ${REL_FILE} for ${CONST} -- aborting before judging anything, rather than judging a copy that is silently identical to the original (CLAUDE.md §8's 'assert the extraction produced something')."
  fi
  MUT_VALUE="$(extract_const "$MUTANT" "$CONST")" || fatal "extract_const fatal-exited while extracting ${CONST} from the mutated copy $MUTANT -- see the FATAL line above for the reason. Aborting rather than judging a mutation this script cannot even read back."
  if [ "$MUT_VALUE" != "0" ]; then
    fatal "re-extraction from the mutated copy of ${REL_FILE} returned '${MUT_VALUE}', not '0' -- the mutation and the extraction disagree; refusing to judge."
  fi
  if floor_plausible "$MUT_VALUE" "$POP"; then
    fail "REGRESSION #0300: this harness judged ${CONST}=0 PLAUSIBLE -- the external oracle failed to catch the exact regression #0300 describes (a floor of 0 leaving the guard vacuous). Affects: ${TESTS}"
  else
    pass "${CONST}=0 (mutated copy) is correctly judged IMPLAUSIBLE by this external oracle, independent of go test -- \`go test\` on the same mutation would stay green (see header comment for why), which is exactly why this check cannot live inside internal/handlers. Affects: ${TESTS}"
  fi

  i=$((i + 1))
done

SHA_AFTER=""
for f in "${FLOOR_FILES[@]}"; do
  digest="$(sha_of "$REPO/$f")" || fatal "sha_of fatal-exited while hashing $REPO/$f -- see the FATAL line above. Aborting rather than letting an empty digest silently join SHA_AFTER."
  SHA_AFTER="${SHA_AFTER}${digest} "
done
if [ -z "$SHA_BEFORE" ] || [ -z "$SHA_AFTER" ]; then
  fail "CRITICAL: sha256 digest computation produced an empty result (SHA_BEFORE='${SHA_BEFORE}' SHA_AFTER='${SHA_AFTER}') -- refusing to treat two blanks as a match. This is the exact fail-open CLAUDE.md §8 warns against: an empty result must be an error, never an input, and this check must have MEASURED something to pass."
elif [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "all three tracked guard files unchanged across this run -- every mutation happened on a private copy in $WORKDIR, never the tracked file"
else
  fail "CRITICAL: a tracked guard file's sha256 changed during this run. Investigate immediately with: git diff -- ${FLOOR_FILES[*]}"
fi

# ---------------------------------------------------------------------------
# Outbox claim-kinds guard floors (#0304, folded into this harness rather
# than a new script)
#
# internal/outbox/claim_kinds_call_site_guard_test.go (#0281, hardened by
# #0304) carries two more floor consts of the EXACT same shape as the three
# above: claimKindsGuardMinPlausibleCallSiteCount (the original, #0281) and
# claimKindsGuardMinPlausibleNonExemptCallSiteCount (#0304's addition).
#
# WHICH FLOORS `go test` CAN AND CANNOT PIN (#0321, correcting a claim this
# comment used to make -- "setting EITHER to 0 leaves go test green" -- that
# #0304's own review measured to be only half true)
#
# Zeroing claimKindsGuardMinPlausibleCallSiteCount (the TOTAL floor) in the
# tracked file DOES leave internal/outbox's own `go test` run green, for the
# identical mathematical reason the header comment above gives for the
# handlers floors: `got < 0` can never fire for a real, non-negative count.
# THIS harness is that floor's only oracle.
#
# Zeroing claimKindsGuardMinPlausibleNonExemptCallSiteCount (the NON-EXEMPT
# floor) does NOT leave `go test` green. #0304's own
# TestNonExemptFloorCatchesScanRootsNarrowedToSelf, in
# internal/outbox/claim_kinds_call_site_guard_test.go, permanently asserts
# (unconditionally, not only when narrowing is applied) that nonExemptCount
# does NOT satisfy claimKindsGuardMinPlausibleNonExemptCallSiteCount under a
# {"."} scan-roots narrowing; with the floor forced to 0, that assertion
# degenerates to the always-true "0 >= 0", which is exactly what its own
# REGRESSION Fatalf exists to catch, and the standing `go test` run fails.
# So the standing Go test already pins THIS floor against 0 -- a property of
# that regression test's own construction, measured (not assumed) by
# #0304's review, not a designed property of the floor.
#
# Neither picture generalizes past 0. Measured across all four combinations
# (both floors x {0, 1}, real scan roots left intact): NEITHER oracle --
# not `go test`, not this external harness -- rejects a floor of 1.
# `outbox_floor_plausible` below only requires `0 < floor <= population`,
# and `go test`'s own two floor checks are both `got < floor`, unfalsifiable
# once floor is at or below the smallest real `got` either check will ever
# see against the real tree.
#
# So this section's actual, non-redundant contribution for this family is
# narrower than "catches any bad floor": for the non-exempt floor it is
# exactly the single value 0 (redundant with the in-package regression
# test); for the total floor it is the full `0 < floor <= population` range,
# which has no in-package protection at all. What earns this section its
# place regardless, per CLAUDE.md §8's remedy for "a guard inside the file
# it guards becomes new mutable surface": that protection SURVIVES an edit
# to the file it guards, which no in-file check -- including
# TestNonExemptFloorCatchesScanRootsNarrowedToSelf itself -- can do by
# construction. Zeroing a floor and disabling the in-package regression test
# that happens to also catch it is a single-file edit; this harness lives in
# a different file, recomputes its populations independently from source
# text rather than trusting the guard's own parser, and is what
# `scripts/check.sh guards` still catches if only
# claim_kinds_call_site_guard_test.go was touched.
#
# #0304's own acceptance criterion 5 asks whether its new floor belongs here
# instead of becoming a second in-file check -- it does, for the same reason
# #0300 gives for the other three: every helper this needs (extract_const,
# sha_of) already lives in this file, so a second script would just
# duplicate them.
#
# #0304's OTHER proof -- that narrowing claimKindsGuardScanRoots to
# []string{"."} makes the guard fail closed -- is intentionally NOT
# duplicated here. That is a behavioral claim about `go test`'s real
# output under a roots mutation (which, unlike a floor-to-0 mutation, DOES
# change what `go test` reports -- narrowing the walk changes `got`, not
# just the threshold `got` is compared against), so `go test` is a valid,
# non-circular oracle for it and it is proved permanently and directly in
# Go, in internal/outbox/claim_kinds_call_site_guard_test.go's own
# TestNonExemptFloorCatchesScanRootsNarrowedToSelf -- see that test's doc
# comment for why a roots mutation and a floor mutation need different
# oracles. This section covers only the floor-constant class of mutation,
# which is what actually requires an oracle outside internal/outbox.
echo
echo "== outbox claim-kinds guard floors (#0304) =="

OUTBOX_GUARD_FILE="internal/outbox/claim_kinds_call_site_guard_test.go"
OUTBOX_SRC="$REPO/$OUTBOX_GUARD_FILE"
[ -f "$OUTBOX_SRC" ] || fatal "$OUTBOX_GUARD_FILE not found -- has it moved? Update OUTBOX_GUARD_FILE."

# count_outbox_call_sites <exclude-dir-abs-or-empty> <root> [root...] --
# textual population for the two outbox floors: every `.ClaimDue(` /
# `.OrphanSweep(` occurrence in .go files under roots (skip node_modules/
# dist), optionally excluding one whole directory tree (used for the
# non-exempt floor, to exclude internal/outbox itself). Deliberately a raw
# grep over source text, not an AST parse like the guard itself uses --
# same looseness as count_go_files above, and for the same reason: this is
# a plausibility oracle from a different tool and a different data source
# than go/ast, not a re-implementation of the guard's own parser (CLAUDE.md
# §8: "can an edit to the subject also satisfy the oracle" -- a change to
# findOutboxCallSitesInFile's AST logic cannot touch this grep, and a
# change to this grep cannot touch that AST logic).
#
# #0323: this grep population and the guard's own go/ast population are NOT
# the same number, on purpose, and that must never be "fixed" by making
# this oracle parse Go. For the TOTAL population (no exclude), this
# function measures 35; internal/outbox/claim_kinds_call_site_guard_test.go's
# own doc comment (claimKindsGuardMinPlausibleCallSiteCount) measures 32 by
# go/ast, and now records this same divergence and names the three
# sites. For the NON-EXEMPT population (internal/outbox excluded), both
# methods agree exactly at 12 -- every non-exempt site matches one for one
# -- because all three of the divergent sites sit INSIDE internal/outbox
# (so they only ever affect the total, exempt-inclusive count, never the
# one claimKindsGuardMinPlausibleNonExemptCallSiteCount is judged against).
# The three: one is inside a doc comment
# (nameMatchesGuardedMethod's, which quotes ".ClaimDue(" and
# ".OrphanSweep(" as prose), and two are inside the raw-string Go-source
# fixtures TestClaimKindsGuardFiresOnFixtureWithNoKinds builds in memory
# (fixtureSrc, scopedFixtureSrc) -- text that looks like a real call inside
# a Go string literal, which this grep cannot distinguish from a real one
# and go/ast correctly never parses as one, since those fixtures are handed
# to the parser as an in-memory `src` argument, not discovered by walking
# the tree. The direction matters: grep only ever INFLATES the total
# relative to go/ast here, which only loosens outbox_floor_plausible's
# `floor <= population` upper bound and never tightens it -- nothing is
# under-protected by the gap. Separately, `grep -c` counts matching LINES,
# not occurrences, so it would UNDER-count (not over-count) if two guarded
# calls ever shared a single source line -- a different risk from the one
# above, recorded here because both are ways this population can diverge
# from a true occurrence count, in opposite directions.
count_outbox_call_sites() {
  local exclude="$1"; shift
  local total=0 f n
  while IFS= read -r f; do
    if [ -n "$exclude" ]; then
      case "$f" in
        "$exclude"/*) continue ;;
      esac
    fi
    n="$(grep -cE '\.(ClaimDue|OrphanSweep)\(' "$f")"
    total=$((total + n))
  done < <(find "$@" \( -name node_modules -o -name dist \) -prune -o -type f -name '*.go' -print)
  printf '%s' "$total"
}

OUTBOX_SHA_BEFORE="$(sha_of "$OUTBOX_SRC")" || fatal "sha_of fatal-exited hashing $OUTBOX_SRC -- see the FATAL line above."

TOTAL_POP="$(count_outbox_call_sites "" "$REPO/internal" "$REPO/cmd")"
case "$TOTAL_POP" in '' | *[!0-9]*) fatal "count_outbox_call_sites returned a non-numeric total population ('$TOTAL_POP')" ;; esac

NONEXEMPT_POP="$(count_outbox_call_sites "$REPO/internal/outbox" "$REPO/internal" "$REPO/cmd")"
case "$NONEXEMPT_POP" in '' | *[!0-9]*) fatal "count_outbox_call_sites returned a non-numeric non-exempt population ('$NONEXEMPT_POP')" ;; esac

# outbox_floor_plausible <floor> <population> -- deliberately NOT
# floor_plausible() above: that function's `floor >= population/2` margin
# fits the handlers family's FILE-count populations, which are in the
# hundreds -- today, citedTestScanRootsMinPlausibleFileCount sits at
# floor=150 against a re-measured population of 267 (#0300 measured 258
# when it picked this margin; it drifts as the tree grows). At that scale
# `population/2` gives real headroom before the margin itself needs
# attention: by the same `floor >= population/2` arithmetic, that floor
# does not fail until the population reaches roughly 302 -- on the order of
# 35 files of growth, re-measured today (#0300 measured ~44 files of
# headroom at the time, against the smaller population then; the number is
# a moving target by design, not a constant to keep in sync).
#
# A call-site population is an order of magnitude smaller, so the identical
# margin behaves completely differently here. #0304's own non-exempt floor
# is 6 against a population of 12 -- exactly population/2 already -- so
# reusing floor_plausible()'s margin verbatim would put this floor AT its
# own failure boundary today, and it would trip after just TWO new
# non-exempt call sites (population 12 -> 14 fails the identical
# `floor >= population/2` check the handlers family relies on). That is not
# "the tree grew enough to warrant a look"; it is "the next two ordinary
# commits that add a caller." #0304's own comment previously justified the
# weaker margin here by claiming the population "CAN be dominated by one
# file (worst case 3, in internal/mailing/worker_store_test.go)" -- but 3 of
# the 12 non-exempt sites this floor actually governs is 25%, not
# domination; the population that genuinely is file-dominated (~20 of 32,
# inside internal/outbox's own store_test.go) belongs to the TOTAL floor
# above, not this one.
#
# So the bar that matters for THIS family is deliberately weaker than
# floor_plausible()'s: greater than zero (closes the #0300/#0304 fail-open
# this section exists for) and no greater than the real population (#0275's
# own failure mode -- a floor raised above the true count, which fails the
# guard permanently even with nothing wrong) -- WITHOUT floor_plausible()'s
# additional `>= population/2` bound, because at call-site scale that bound
# would make this family's margin flap on ordinary growth rather than catch
# a real regression.
outbox_floor_plausible() {
  local floor="$1" population="$2"
  [ "$floor" -gt 0 ] || return 1
  [ "$floor" -le "$population" ] || return 1
  return 0
}

for spec in \
  "claimKindsGuardMinPlausibleCallSiteCount:${TOTAL_POP}:TestNoUnscopedOutboxClaimCallOutsidePackage total-population floor (#0281)" \
  "claimKindsGuardMinPlausibleNonExemptCallSiteCount:${NONEXEMPT_POP}:TestNoUnscopedOutboxClaimCallOutsidePackage non-exempt floor (#0304)" \
; do
  CONST="${spec%%:*}"
  rest="${spec#*:}"
  POP="${rest%%:*}"
  TESTNAME="${rest#*:}"

  echo "-- ${CONST} (protects: ${TESTNAME}) --"

  REAL_VALUE="$(extract_const "$OUTBOX_SRC" "$CONST")" || fatal "extract_const fatal-exited extracting ${CONST} from $OUTBOX_SRC -- see the FATAL line above."
  if outbox_floor_plausible "$REAL_VALUE" "$POP"; then
    pass "committed ${CONST}=${REAL_VALUE} is plausible against an externally-measured (grep-based) population of ${POP}"
  else
    fail "REGRESSION #0304: committed ${CONST}=${REAL_VALUE} is NOT plausible against an externally-measured population of ${POP} (must be > 0 and <= population). Affects: ${TESTNAME}"
  fi

  MUTANT="$WORKDIR/$(basename "$OUTBOX_GUARD_FILE").${CONST}"
  sed "s/const ${CONST} = [0-9][0-9]*/const ${CONST} = 0/" "$OUTBOX_SRC" > "$MUTANT"
  if ! grep -q "const ${CONST} = 0" "$MUTANT"; then
    fatal "mutation did not take on the copy of ${OUTBOX_GUARD_FILE} for ${CONST} -- aborting before judging anything."
  fi
  MUT_VALUE="$(extract_const "$MUTANT" "$CONST")" || fatal "extract_const fatal-exited extracting ${CONST} from the mutated copy $MUTANT -- see the FATAL line above."
  if [ "$MUT_VALUE" != "0" ]; then
    fatal "re-extraction from the mutated copy of ${OUTBOX_GUARD_FILE} returned '${MUT_VALUE}', not '0' -- the mutation and the extraction disagree; refusing to judge."
  fi
  if outbox_floor_plausible "$MUT_VALUE" "$POP"; then
    fail "REGRESSION #0304: this harness judged ${CONST}=0 PLAUSIBLE -- the external oracle failed to catch a zeroed floor. Affects: ${TESTNAME}"
  else
    pass "${CONST}=0 (mutated copy) is correctly judged IMPLAUSIBLE by this external oracle, independent of go test (same mathematical-impossibility reasoning as the header comment above: got < 0 can never fire). Affects: ${TESTNAME}"
  fi
done

OUTBOX_SHA_AFTER="$(sha_of "$OUTBOX_SRC")" || fatal "sha_of fatal-exited re-hashing $OUTBOX_SRC -- see the FATAL line above."
if [ -z "$OUTBOX_SHA_BEFORE" ] || [ -z "$OUTBOX_SHA_AFTER" ]; then
  fail "CRITICAL: outbox guard file sha256 computation produced an empty result -- refusing to treat two blanks as a match."
elif [ "$OUTBOX_SHA_BEFORE" = "$OUTBOX_SHA_AFTER" ]; then
  pass "$OUTBOX_GUARD_FILE unchanged across this run -- every mutation happened on a private copy in $WORKDIR, never the tracked file"
else
  fail "CRITICAL: $OUTBOX_GUARD_FILE's sha256 changed during this run. Investigate immediately with: git diff -- $OUTBOX_GUARD_FILE"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "go_file_visit_floor_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "go_file_visit_floor_guard_test.sh: $FAILURES failure(s) -- see FAIL lines above"
  exit 1
fi
