#!/usr/bin/env bash
#
# go_file_visit_floor_guard_test.sh — external oracle for the three
# "plausible file count" floor constants #0275 added to internal/handlers'
# citation/dangling-citation/audit-metadata guard family, per issue #0300.
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

command -v find >/dev/null || fatal "find not on PATH"
command -v grep >/dev/null || fatal "grep not on PATH"
command -v sed  >/dev/null || fatal "sed not on PATH"

# extract_const <file> <const-name> -- reads FILE (real or mutated copy,
# never assumed) and prints the integer value of `const <const-name> = N`.
# Fails closed (fatal) rather than returning an empty/guessed value if the
# pattern is not found or is not a plain non-negative integer -- an
# extraction that silently produced nothing must never be treated as "0
# files" or "floor 0" by accident (CLAUDE.md §8).
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
  SHA_BEFORE="${SHA_BEFORE}$(shasum -a 256 "$REPO/$f" | awk '{print $1}') "
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

  REAL_VALUE="$(extract_const "$SRC" "$CONST")"
  if floor_plausible "$REAL_VALUE" "$POP"; then
    pass "committed ${CONST}=${REAL_VALUE} is plausible against an externally-measured population of ${POP} (>= half, <= population)"
  else
    fail "REGRESSION #0300: committed ${CONST}=${REAL_VALUE} is NOT plausible against an externally-measured population of ${POP} -- either it was lowered too far, raised above the real population (#0275's own failure mode), or the tree shrank. Affects: ${TESTS}"
  fi

  MUTANT="$WORKDIR/$(basename "$REL_FILE")"
  sed "s/const ${CONST} = [0-9][0-9]*/const ${CONST} = 0/" "$SRC" > "$MUTANT"
  if ! grep -q "const ${CONST} = 0" "$MUTANT"; then
    fatal "mutation did not take on the copy of ${REL_FILE} for ${CONST} -- aborting before judging anything, rather than judging a copy that is silently identical to the original (CLAUDE.md §8's 'assert the extraction produced something')."
  fi
  MUT_VALUE="$(extract_const "$MUTANT" "$CONST")"
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
  SHA_AFTER="${SHA_AFTER}$(shasum -a 256 "$REPO/$f" | awk '{print $1}') "
done
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "all three tracked guard files unchanged across this run -- every mutation happened on a private copy in $WORKDIR, never the tracked file"
else
  fail "CRITICAL: a tracked guard file's sha256 changed during this run. Investigate immediately with: git diff -- ${FLOOR_FILES[*]}"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "go_file_visit_floor_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "go_file_visit_floor_guard_test.sh: $FAILURES failure(s) -- see FAIL lines above"
  exit 1
fi
