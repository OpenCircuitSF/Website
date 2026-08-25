#!/usr/bin/env bash
#
# check.sh — the canonical verification run. Use this instead of hand-rolling
# `go test` invocations, so every agent verifies the same way.
#
#     scripts/check.sh                       # build + vet + scoped Go tests + web checks
#     scripts/check.sh go ./internal/mailing/...   # just those Go packages
#     scripts/check.sh web                   # just npm run check + npm test
#     scripts/check.sh all                   # the whole Go suite (a batch's review pass only)
#     scripts/check.sh guards                # the standalone shell guard tests (see below)
#
# `guards` runs scripts/testdb_gc_guard_test.sh, scripts/dev_guard_test.sh,
# and scripts/db_reset_guard_test.sh — not part of any other mode, since
# #0117's third review measured dev_guard_test.sh alone at ~48s and binding
# :5173 in several parts, which does not belong in every ordinary run. #0207
# named the actual defect this solves: two prior guard tests
# (testdb_gc_guard_test.sh from #0150, dev_guard_test.sh from #0117) existed
# but nothing ran them automatically, so a safety property only held when
# someone remembered the guard existed. This is that "something someone
# actually runs" — it is not wired into `go`/`web`/`all`/the default, so it
# costs nothing on the common path.
#
# WHAT IT ENFORCES, so you cannot forget:
#
#   * TEST_DATABASE_URL is exported. Without it the DB-backed suites SKIP
#     SILENTLY and a green run proves nothing (CLAUDE.md §5). If ISSUE is set,
#     this script gives you your own database via scripts/testdb.sh instead of
#     sharing one — see that script for why that lifts the concurrency cap.
#   * -p 2 on every Go run (CLAUDE.md §5a).
#   * -count=N for N>1 is refused (CLAUDE.md §5a — banned for flake-hunting).
#   * Output is bounded with tail, because a full run emits more tokens than
#     the PRD.
#   * `uptime` is printed first. Above ~8 the machine is busy and any TIMING
#     failure is that, not your code. A state assertion is never explained by
#     load.
#
# Set ISSUE=0123 to get an isolated test database, created before the run and
# dropped after:
#
#     ISSUE=0123 scripts/check.sh go ./internal/handlers/...

set -uo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# #0262: resolved to an ABSOLUTE path HERE, before "cd $REPO" below changes
# the working directory — "${BASH_SOURCE[0]}" on its own is whatever
# string this script was invoked with, same as "$0"; if that string is
# relative (e.g. "../Website/scripts/check.sh", run from outside the repo
# root), it stops resolving the moment cwd changes, the same failure mode
# this was meant to fix. $SELF is what every guard below scans instead of
# a bare "$0"/"${BASH_SOURCE[0]}" — see GUARD-0208's SCAN comment for why
# it isn't a hardcoded "$REPO/scripts/check.sh" either.
SELF="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/$(basename "${BASH_SOURCE[0]}")"
cd "$REPO"
TAIL="${TAIL:-40}"
FAILED=0

step() { printf '\n\033[1m=== %s\033[0m\n' "$*"; }
run()  { "$@"; local rc=$?; [ $rc -eq 0 ] || { FAILED=1; printf '\033[31mFAILED (%d): %s\033[0m\n' "$rc" "$*"; }; return 0; }

# #0208: every build/vet/test/npm step below that pipes into `tail` (so the
# piped command's real exit code isn't discarded — see #0140: without
# -o pipefail, `go test ... | tail -40` reports tail's exit status, which is
# always 0, so a failing run prints FAILED output and this script still says
# VERIFICATION PASSED) funnels through this ONE function, the single place
# that spells the flag. #0140's original fix was a source grep asserting
# every "run bash -c" call site individually said "-o pipefail" — reformatted
# per CLAUDE.md §5's example set, that guard was shown (issues/0208.md) to
# miss `/bin/bash -c`, `env bash -c`, `$SH -c`, a bare `bash -c` outside
# `run`, and a `bash \`-newline-`-c` split across two lines, plus a false
# "clean" reading from a trailing `# pipefail` comment. Structure removes the
# whole evasion surface: there is now nowhere else in this file to spell (or
# lose) the flag, so none of those forms is expressible as a way to lose it —
# a call site either goes through runpipe() or it doesn't run through a shell
# pipe at all.
# BEGIN RUNPIPE-0208
runpipe() { run bash -o pipefail -c "$1"; }
# END RUNPIPE-0208

# BEGIN FUNCDEF-0258
# #0258 (3rd bounce) / #0262: the checks below used to anchor on the
# literal column-0 spelling "^run()" / "^runpipe()" — a per-spelling
# regex. Bash accepts several OTHER spellings of the same top-level
# definition (a leading space, the "function" keyword, a space before
# the parens, a tab), each of which shadows the real definition at call
# time exactly the same way a caught decoy would, while the narrow
# anchor stayed at 1 and never noticed. Three review rounds on this
# guard each closed the shape they were shown and the next found the
# adjacent one (issues/0258.md) — the fix is not a wider enumeration,
# it is to stop enumerating spellings at all.
#
# resolve_func_hash NAME FILE lets bash's OWN parser decide what NAME
# resolves to — the same way running FILE for real would — instead of a
# grep guessing at spelling:
#   1. Extract every line in FILE that is a COMPLETE, single-line
#      function definition (optional "function" keyword, optional
#      "()", any leading whitespace, PROVIDED the opening brace's
#      match closes on the same line). This describes "a function
#      definition sits on this line", not "the function is named
#      run/runpipe" — it does not care what the function is called, so
#      there is nothing left to enumerate per name.
#   2. Source those candidate lines, and only those lines, into a
#      private bash subshell, in file order. Sourcing a "name() { ...
#      }" construct only ever REGISTERS the function; it never
#      executes the body — so this is safe even though the candidate
#      set includes functions this check doesn't care about (step,
#      cleanup) and, in a mutated copy, an attacker-planted decoy:
#      nothing in either its header or its body runs merely by being
#      defined. Sourcing in file order means bash's own shadowing rule
#      (last definition wins) resolves a duplicate/decoy exactly the
#      way running the real script would — no separate "count the
#      definitions" step is needed, because the property that matters
#      is not how many spellings of NAME appear, it's what NAME ends
#      up bound to.
#   3. `declare -f NAME` prints bash's own canonical, reformatted
#      rendering of whatever won the shadowing — measured to be
#      byte-identical regardless of the source spelling's leading
#      whitespace, "function" keyword, or indentation (leading space,
#      "function run {", "run () {", and a tab-indented copy all
#      produced the same declare -f output for the same body).
#      Hashing THAT, rather than a copy of the source line, is what
#      makes the oracle spelling-independent: bash's parser does the
#      recognition, not this script's regex, and CLAUDE.md §8's rule
#      still holds — the pinned hex digest is unrelated bytes to the
#      code it verifies, so no edit to the code can also rewrite it.
#
# What this does NOT close, by design, because closing it needs
# executing untrusted code rather than merely defining functions: a
# definition built at runtime (eval, sourcing another file, unset -f
# then a later indirect define) is invisible to a scan that only looks
# at literal "name() { ... }" text — verified: an eval-built run()
# ADDED alongside the real definition is not caught (only an eval that
# REPLACES the real line is, since the scan then finds nothing else to
# shadow it with — narrower than a prior draft of this fix claimed).
# #0208's mutant 7 (`run "$SHELL" -c`, an unrelated shell invocation,
# not a redefinition of run) is a separate, already-accepted gap
# elsewhere in GUARD-0208, unaffected by this check. A definition split
# across multiple lines (the brace on its own line) is likewise outside
# a one-line candidate scan; nothing in this file is written that way
# today. Closing either needs an actual shell parser reading the WHOLE
# file's control flow, not a grep over single lines.
command -v shasum >/dev/null || { echo "error: shasum not on PATH (needed by FUNCDEF-0258)" >&2; exit 2; }
command -v mktemp >/dev/null || { echo "error: mktemp not on PATH (needed by FUNCDEF-0258)" >&2; exit 2; }
FUNCDEF_CANDIDATE_RE='^[[:space:]]*(function[[:space:]]+)?[A-Za-z_][A-Za-z0-9_]*[[:space:]]*(\(\))?[[:space:]]*\{.*\}[[:space:]]*(#.*)?$'
resolve_func_hash() {
  local name="$1" file="$2" tmp
  tmp="$(mktemp)" || return 1
  grep -E "$FUNCDEF_CANDIDATE_RE" "$file" | sed -E 's/[[:space:]]*#.*$//' > "$tmp"
  # A forked (), not a new "bash -c" interpreter: sourcing candidates
  # only needs an isolated function table, not a separate process, and a
  # literal "bash ... -c" here would itself trip GUARD-0208's shell-"-c"
  # scan below (that scan exists to make sure the ONLY place this file
  # loses "-o pipefail" is runpipe() — this subshell has nothing to do
  # with the pipefail funnel at all, so it must not spell "-c" and get
  # dragged into that check).
  # shellcheck disable=SC1090 # $tmp is our own mktemp scratch file, populated
  # two lines up from a grep/sed of $file — nothing for shellcheck to follow.
  ( source "$tmp" >/dev/null 2>&1; declare -f "$name" 2>/dev/null ) \
    | shasum -a 256 | awk '{print $1}'
  rm -f "$tmp"
}
# END FUNCDEF-0258

# BEGIN GUARD-0208
# Self-check: confirms the funnel above hasn't been bypassed by a later edit.
# #0140's original guard was a same-line textual match: a call site excluded
# itself from its own "missing -o pipefail" search only because that same
# source line ALSO happened to contain the word "pipefail" elsewhere on it
# (in a `grep -v 'pipefail'` clause). Reformatting either of the two lines
# that coincidence depended on made the guard match its OWN source and exit 2
# on every invocation, permanently (issues/0208.md). This guard cannot repeat
# that failure by construction: it deletes its own block (between the
# BEGIN/END GUARD-0208 markers, right here, which is also why the `step` call
# announcing this check lives inside them) and runpipe()'s own definition
# block (BEGIN/END RUNPIPE-0208, above) from a scratch copy of this file
# before scanning anything — so no wording inside either block, however it's
# laid out or reformatted, can ever be mistaken for a violation of itself.
# What must NOT appear anywhere else in the file: the word "pipefail" (a
# second spelling means either a call site is spelling the flag directly
# instead of going through runpipe(), or runpipe() itself was duplicated —
# the script's OWN top-of-file `set -uo pipefail` is excluded by exact line
# match below, since that is this script's own option, not a call site), or
# any other bash/sh invocation carrying a "-c" flag (a bypass that lost the
# flag entirely wouldn't otherwise show up as a second "pipefail").
#
# Known, accepted gap (#0208's review, recorded by #0251): `run "$SHELL" -c
# "… | tail"` would evade every check here — it spells neither a literal
# "pipefail" nor a literal "bash"/"sh" token, and adding it as a new step
# leaves the runpipe() call count unchanged. Contrived; no realistic edit to
# this file produces it. Not closed.
step "self-check: pipefail funnel guard (#0208)"
# The four sed patterns below are each built from two concatenated pieces
# (no separator between the closing and opening quotes) rather than spelled
# as one literal token — so THIS line's own source text never contains a
# contiguous "# BEGIN GUARD-0208" (etc.) substring. That matters because sed
# range-deletion reopens a range if a LATER line matches the start pattern
# again: had this line spelled the markers whole, it would itself re-trigger
# the very ranges it exists to close, deleting everything from here to EOF —
# exactly the self-referential trap this design otherwise avoids.
BG="# BEGIN"" GUARD-0208"; EG="# END"" GUARD-0208"
BR="# BEGIN"" RUNPIPE-0208"; ER="# END"" RUNPIPE-0208"
# Scans "$SELF" (the absolute path this invocation actually resolved to,
# computed once near the top of the file — see its own comment), not a
# hardcoded "$REPO/scripts/check.sh" — the latter would let a mutated
# copy of this script (used to mutation-test this very guard, per
# CLAUDE.md §8a: never edit the shared tracked file to prove a guard
# catches a regression) scan straight past its own mutation and check
# the untouched tracked file instead, silently proving nothing. Not a
# bare "$0" or "${BASH_SOURCE[0]}" (#0262): after "cd \"$REPO\"" above,
# either one, if it names a RELATIVE path (e.g. this script invoked as
# "../Website/scripts/check.sh" from outside the repo root — both $0 and
# BASH_SOURCE[0] hold the same invocation string, so switching between
# them alone does not fix this), no longer resolves against the new
# cwd — reproduced with a bare "$0": "sed: ../Website/scripts/check.sh:
# No such file or directory", every check below reads an empty scan, and
# the run fails with a spurious "found 0" rather than ever reaching real
# work. $SELF is resolved to an absolute path BEFORE the "cd", so it
# stays valid regardless of how this script was invoked or what the cwd
# becomes afterward.
SCAN="$(sed -e "/${BG}/,/${EG}/d" -e "/${BR}/,/${ER}/d" "$SELF" \
    | grep -vE '^[[:space:]]*#' \
    | grep -vxF 'set -uo pipefail')"

PF_HITS="$(printf '%s\n' "$SCAN" | grep -c 'pipefail' || true)"
if [ "${PF_HITS:-0}" -ne 0 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): "pipefail" appears in scripts/check.sh outside runpipe()'"'"'s definition. A call site may be spelling the flag directly instead of calling runpipe(), or runpipe() was duplicated. Offending line(s):\033[0m\n' >&2
  printf '%s\n' "$SCAN" | grep -n 'pipefail' >&2
  exit 2
fi

# #0258: the previous pattern was literally '-c\b' — a word-boundary check
# right after the "c", which a COMBINED short-option cluster defeats, since
# there is no boundary between adjacent letters. `run bash -cu "$1"` (or
# -ce, -cx, or the reversed -uc) is valid shell (verified: it runs the
# command with pipefail off) and evaded this scan entirely. The replacement
# matches a "-" preceded by start-of-line or whitespace (so it can't fire
# inside a GNU long option like "--color", where the option-introducing "-"
# is never preceded by whitespace) followed by any short-option letters
# containing a "c" anywhere in the cluster, in either order.
SHELLC_HITS="$(printf '%s\n' "$SCAN" | grep -nE '\b(bash|sh)\b' | grep -E -- '(^|[[:space:]])-[a-zA-Z]*c[a-zA-Z]*\b' || true)"
if [ -n "$SHELLC_HITS" ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): scripts/check.sh has a shell "-c" invocation outside runpipe() — it could silently lose "-o pipefail" the same way #0140 did. Offending line(s):\033[0m\n' >&2
  printf '%s\n' "$SHELLC_HITS" >&2
  exit 2
fi

RUNPIPE_CALLS="$(printf '%s\n' "$SCAN" | grep -c 'runpipe "' || true)"
if [ "${RUNPIPE_CALLS:-0}" -lt 9 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): expected at least 9 runpipe() call sites (one per go build/vet/test and npm check/test step); found %s. A step may have reverted to spelling a shell invocation directly instead of calling runpipe().\033[0m\n' "$RUNPIPE_CALLS" >&2
  exit 2
fi

# #0251: everything above is a NEGATIVE assertion over $SCAN — a copy of
# this file with runpipe()'s own definition (the RUNPIPE-0208 block) already
# deleted, precisely so the checks above can't flag their own necessary
# mention of "pipefail" and "-c". That makes the guard structurally BLIND to
# the one line that actually carries the property: whether runpipe() itself
# still spells "-o pipefail". Strip that one flag from runpipe()'s
# definition and every check above still passes — no stray "pipefail", no
# stray shell "-c", still >= 9 "runpipe \"" call sites — while the property
# this whole self-check exists to guarantee is gone. Demonstrated in
# issues/0251.md: with the flag stripped, a failing test produced
# VERIFICATION PASSED and exit 0.
#
# #0251's first fix (bounced on re-review) extracted the whole RUNPIPE-0208
# marker BLOCK and grepped it for "-o pipefail" — not the definition LINE.
# The review demonstrated three one-line fakes that all still produced
# VERIFICATION PASSED on a real failing test while the flag was genuinely
# gone, and all three are invisible to the checks above too, since $SCAN
# deletes the same ranges:
#   A) strip the flag, leave it in a TRAILING COMMENT on the same line
#      (e.g. `# was: run bash -o pipefail -c`) — a whole-line comment strip
#      does not touch a comment that trails real code on the same line.
#   B) strip the flag, add a SECOND RUNPIPE-0208 marker pair elsewhere
#      wrapping an unused decoy that still spells the flag — sed
#      range-printing reopens on every later start-pattern match, so both
#      blocks were concatenated and the decoy supplied the match.
#   C) strip the flag, add a NON-DEFINITION line inside the real block that
#      happens to spell the phrase (e.g. a variable assignment) — matching
#      against the whole block's text, rather than only a line that is
#      actually the function definition, let that line stand in.
# Fixed by narrowing in two steps, each closing one fake:
#   1. Assert the RUNPIPE-0208 marker pair appears exactly once in
#      "$SELF" — closes (B), and is worth having regardless,
#      since a duplicate block would silently change what EVERY check in
#      this guard scans.
#   2. Compare runpipe()'s SHADOW-RESOLVED definition (FUNCDEF-0258's
#      resolve_func_hash, above) against a pinned SHA-256 digest — closes
#      (A) and (C), since neither a trailing comment nor a stray
#      non-definition line changes what bash itself resolves "runpipe" to.
#
# #0262: this anchor used to be "^runpipe\(\)" — the same per-spelling
# regex #0258's third review defeated for run() with four one-line decoys
# (leading space, "function runpipe {", "runpipe () {", a tab). Verified:
# the same four shapes defeat "^runpipe\(\)" too. Unlike run(), this one
# was not exploitable in practice — an indented runpipe() decoy still had
# to spell "bash -c" to do anything useful, and SHELLC_HITS above happens
# to catch that. But that cover was INCIDENTAL: nothing stated the
# dependency, nothing tested it, and it would have silently stopped
# working the moment a decoy dropped "-o pipefail" without also dropping
# "bash -c" (SHELLC_HITS only fires on a bare "-c" flag, not on a missing
# "pipefail"). FUNCDEF-0258's resolve_func_hash below closes this
# directly, on its own, independent of SHELLC_HITS or any other check in
# this file — the masking dependency is removed, not just documented.
BR_COUNT="$(grep -cxF "$BR" "$SELF")"
ER_COUNT="$(grep -cxF "$ER" "$SELF")"
if [ "${BR_COUNT:-0}" -ne 1 ] || [ "${ER_COUNT:-0}" -ne 1 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251): expected the RUNPIPE-0208 marker pair exactly once in scripts/check.sh; found %s BEGIN and %s END. A duplicate marker block can supply a decoy match for every check in this guard, not just this one.\033[0m\n' "$BR_COUNT" "$ER_COUNT" >&2
  exit 2
fi

RUNPIPE_HASH="$(resolve_func_hash runpipe "$SELF")"
RUNPIPE_EXPECTED_SHA256="546fb9901c1dff71af78735fcfa14427eaea0f2d7bc8a2a0b5e977111e5a5c3a"
if [ "$RUNPIPE_HASH" != "$RUNPIPE_EXPECTED_SHA256" ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251/#0262): runpipe()'"'"'s resolved definition (bash'"'"'s own "declare -f runpipe" rendering, hashed — see FUNCDEF-0258 above) no longer matches the pinned SHA-256 digest. The funnel this self-check exists to guarantee may have lost the -o pipefail flag, or been shadowed by a later redefinition in any spelling bash accepts. See the recompute recipe in GUARD-0258 below (substitute runpipe for run) if this change is legitimate.\033[0m\n' >&2
  printf 'sha256: %s (expected %s)\n' "$RUNPIPE_HASH" "$RUNPIPE_EXPECTED_SHA256" >&2
  exit 2
fi
# END GUARD-0208

# BEGIN GUARD-0258
# #0258: run()'s own FAILED=1 accounting is the entire failure-reporting
# contract for this whole script — GUARD-0208 above proves the pipefail
# funnel is intact, but nothing proved run() itself still SETS FAILED=1 on
# a non-zero exit. Strip that accounting (run() { "$@"; return 0; }) and
# every failing step passes silently: a --- FAIL prints and this script
# still reports VERIFICATION PASSED, exit 0 — reproduced end to end
# (issues/0258.md). GUARD-0208 never catches this: the mutation spells no
# "bash", no "-c", no "pipefail", so all four checks in that block stay
# silent, and nothing outside this file asserted anything about run()
# either (grepped across internal/, cmd/, web/src/, scripts/).
#
# run() is not wrapped in its own BEGIN/END marker block the way runpipe()
# is — it doesn't need one: FUNCDEF-0258's candidate scan already covers
# the whole file unconditionally, so there is no decoy-marker-block class
# to close here the way GUARD-0208 needed BR_COUNT/ER_COUNT for a second
# RUNPIPE-0208 pair.
#
# This guard has been through three review rounds (issues/0258.md), each
# closing the specific bypass it was shown, and this pass is the fourth:
#   - pass 1 compared run()'s definition against a verbatim copy of that
#     text sitting in a quoted heredoc a few lines below — defeated by a
#     single sed rewriting both the real line AND the heredoc at once,
#     since they were the same bytes in the same file (CLAUDE.md §8: an
#     oracle must not be the same bytes as its subject).
#   - pass 2 replaced the heredoc with a pinned SHA-256 digest of the
#     definition LINE, extracted by the narrow anchor "^run\(\)" — an
#     independent oracle by construction, but the anchor itself still only
#     recognized one spelling. The third review found four one-line
#     decoys (leading space, "function run {", "run () {", a tab) that
#     each shadow run() at runtime while the narrow anchor stayed at 1 —
#     #0251's own decoy/duplicate class, one function over, again.
#   - this pass keeps the pinned digest — CLAUDE.md §8's rule was already
#     satisfied and should not be re-litigated — and replaces the narrow
#     anchor with FUNCDEF-0258's resolve_func_hash, which lets bash's own
#     parser resolve what "run" is bound to instead of a per-spelling
#     regex. See FUNCDEF-0258 above for the mechanism, what it closes
#     (all four decoy shapes, verified end to end), and what it still
#     doesn't (an eval-built definition, or one split across multiple
#     lines — narrower gaps than "any unrecognized spelling", and named
#     as such rather than glossed over the way "any second run() line,
#     decoy-marked or not, is always counted" glossed over C1–C4 in the
#     pass-2 comment this replaces).
#
# To recompute the pinned digest after a legitimate change to run() (or,
# substituting runpipe for run, to runpipe() in GUARD-0208 above) — the
# marker range, not a line number, so this recipe doesn't drift as the
# file grows:
#   sed -n '/^# BEGIN FUNCDEF-0258/,/^# END FUNCDEF-0258/p' scripts/check.sh \
#     > /tmp/funcdef.sh && source /tmp/funcdef.sh \
#     && resolve_func_hash run scripts/check.sh
step "self-check: run() FAILED-accounting guard (#0258)"
RUN_HASH="$(resolve_func_hash run "$SELF")"
RUN_EXPECTED_SHA256="b5ca2df355ef7f7c518968a3d77eff626b48ce546cde2c8a820d62c38a2f9a24"
if [ "$RUN_HASH" != "$RUN_EXPECTED_SHA256" ]; then
  printf '\033[31mFAILED-ACCOUNTING REGRESSION (#0258): run()'"'"'s resolved definition (bash'"'"'s own "declare -f run" rendering, hashed — see FUNCDEF-0258 above) no longer matches the pinned SHA-256 digest. The FAILED=1 accounting this whole script'"'"'s pass/fail report depends on may have been removed, or shadowed by a later redefinition in any spelling bash accepts. See the recompute recipe above if this change is legitimate.\033[0m\n' >&2
  printf 'sha256: %s (expected %s)\n' "$RUN_HASH" "$RUN_EXPECTED_SHA256" >&2
  exit 2
fi
# END GUARD-0258

for a in "$@"; do
  case "$a" in
    -count=1|-count) ;;
    -count=*) echo "error: $a is banned (CLAUDE.md §5a). A test that fails only under load is not flaky." >&2; exit 2 ;;
  esac
done

step "machine"
uptime
LOAD="$(uptime | sed 's/.*load averages*: *//' | awk '{print $1}' | tr -d ,)"
if awk "BEGIN{exit !($LOAD > 8)}"; then
  echo "WARNING: load average $LOAD is above ~8. Something else is running."
  echo "Any TIMING failure you see is that, not a defect. Do not widen a deadline to accommodate it."
fi

OWN_DB=""
if [ -n "${ISSUE:-}" ]; then
  step "isolated test database for issue $ISSUE"
  if TEST_DATABASE_URL="$(scripts/testdb.sh create "$ISSUE")"; then
    export TEST_DATABASE_URL; OWN_DB="$ISSUE"
    echo "$TEST_DATABASE_URL"
  else
    echo "could not create an isolated database; aborting rather than silently sharing one" >&2
    exit 1
  fi
else
  export TEST_DATABASE_URL="${TEST_DATABASE_URL:-postgres://opencircuit:opencircuit@localhost:5432/opencircuit_test?sslmode=disable}"
  echo "TEST_DATABASE_URL=$TEST_DATABASE_URL  (shared — set ISSUE=NNNN for your own)"
fi
cleanup() { [ -n "$OWN_DB" ] && scripts/testdb.sh drop "$OWN_DB" >/dev/null 2>&1; }
trap cleanup EXIT

MODE="${1:-default}"; shift || true

go_test() {
  # #0212: the default package list must include every package that can hold
  # a Go test, not just the ones that happen to today. ./web/... (web/embed.go,
  # currently no _test.go) used to be absent here, so a test placed in that
  # package would compile, pass locally under a bare `go test ./...`, and then
  # never run again under the command every issue is told to use — exactly
  # what happened to #0141's first guard placement before it was relocated to
  # internal/seo. Add a new top-level Go package here when it is created;
  # don't rely on `scripts/check.sh all`, which CLAUDE.md reserves for a
  # batch's single review pass.
  local pkgs="$*"; [ -n "$pkgs" ] || pkgs="./internal/... ./cmd/... ./web/..."
  step "go test $pkgs -p 2"
  runpipe "go test $pkgs -p 2 -count=1 2>&1 | tail -$TAIL"
  step "skip audit — a package reporting [no test files] or 'no test files' proves nothing"
  go test $pkgs -p 2 -count=1 2>&1 | grep -E 'no test files|SKIP|--- SKIP' | head -20 || echo "(no skips)"
}

web_check() {
  step "npm run check"; runpipe "cd web && npm run check 2>&1 | tail -$TAIL"
  step "npm test";      runpipe "cd web && npm test 2>&1 | tail -$TAIL"
}

# #0161: gofmt drift is otherwise invisible — `go vet` does not check
# formatting, so an unformatted file passes build+vet+test silently and
# stays unformatted indefinitely. Scoped to the whole repo (`.`), not a list
# of package roots: gofmt only ever looks at *.go files, so scanning the
# whole tree can't false-positive on non-Go content (web/, migrations/,
# etc.) and needs no maintenance as packages are added or moved. Must FAIL,
# not just print — `gofmt -l` exits 0 even when it lists files (the #0140
# shape of bug: a real signal sitting in output nobody checks), so the
# non-empty output itself is what flips FAILED here.
gofmt_check() {
  step "gofmt -l (#0161 — formatting drift)"
  local out
  out="$(gofmt -l . 2>&1)"
  if [ -n "$out" ]; then
    FAILED=1
    printf '\033[31mFAILED: gofmt -l found unformatted file(s):\033[0m\n%s\n' "$out"
    printf '\033[31mFix: gofmt -w %s\033[0m\n' "$(echo "$out" | tr '\n' ' ' | sed 's/ *$//')"
  else
    echo "(clean)"
  fi
}

case "$MODE" in
  go)  step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       go_test "$@" ;;
  web) web_check ;;
  guards)
       # #0207: the "something someone actually runs" for the standalone
       # shell guard tests — see the header comment for why this is its own
       # mode rather than folded into go/all/default. Each script manages
       # its own scratch database(s) and prints its own PASS/FAIL lines;
       # `run` here only needs to observe the exit code.
       step "scripts/testdb_gc_guard_test.sh (#0150)"; run scripts/testdb_gc_guard_test.sh
       step "scripts/dev_guard_test.sh (#0117)";        run scripts/dev_guard_test.sh
       step "scripts/db_reset_guard_test.sh (#0207)";   run scripts/db_reset_guard_test.sh
       ;;
  all) step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       go_test "./..."; web_check ;;
  *)   step "go build"; runpipe "go build ./... 2>&1 | tail -$TAIL"
       step "go vet";   runpipe "go vet ./... 2>&1 | tail -$TAIL"
       gofmt_check
       # #0212 review (bounced bfdfe04): this arm used to spell its own
       # explicit package list ("./internal/... ./cmd/..."), a second source
       # of truth that go_test()'s default-list edit never reached — so
       # ./web/... joined the `go` mode's scope but not the BARE default's,
       # which is the command CLAUDE.md §5 and every issue actually tells
       # agents to run. Calling go_test with no arguments makes this arm read
       # the exact same default list `go)` reads when given none, so there is
       # only one place that list is spelled.
       go_test; web_check ;;
esac

step "leftover processes you may have started"
pgrep -fl 'go test|\.test ' || echo "(none)"

if [ "$FAILED" -ne 0 ]; then printf '\n\033[31mVERIFICATION FAILED\033[0m\n'; exit 1; fi
printf '\n\033[32mVERIFICATION PASSED\033[0m\n'
