#!/usr/bin/env bash
#
# check_guard_test.sh — external guard test for scripts/check.sh's own
# failure-reporting contract (issues #0258 and #0262).
#
# WHY THIS EXISTS, AND WHY IT IS NOT INSIDE check.sh
#
# check.sh's job is to report VERIFICATION PASSED only when every step it ran
# actually passed. #0140 found it could report success over a failing step;
# #0208, #0251, #0258 and #0262 each hardened it, and every one of those
# hardenings lives INSIDE check.sh. That is the problem CLAUDE.md §8 now
# records: a guard inside the file it guards becomes new mutable surface in
# that file. Six review rounds of #0258 each closed the shape they were shown
# and each lost to the adjacent one — and by the sixth, the newest mechanism
# was itself the target: the live probe that pass had just made load-bearing
# was pinned to nothing, so three added lines replaced it with a stub and the
# script reported VERIFICATION PASSED, exit 0, over three steps that had
# exited 127.
#
# So the load-bearing check moved out here, to the shape this repo already
# uses for exactly this class (scripts/testdb_gc_guard_test.sh #0150,
# scripts/dev_guard_test.sh #0117, scripts/db_reset_guard_test.sh #0207): copy
# the subject, exercise it, and assert its exit code and output FROM OUTSIDE.
# An assertion that lives in another file cannot be disarmed by editing the
# subject.
#
# THE ORACLE
#
# A copy of check.sh is run in a sandbox where its steps genuinely fail, and
# the verdict it prints is compared with the truth:
#
#   * `guards` mode in a sandbox holding only scripts/check.sh — the guard
#     scripts it invokes do not exist there, so every one of those `run` steps
#     really exits 127. Honest verdict: VERIFICATION FAILED, exit 1. This is
#     the `run` helper's FAILED=1 accounting, which is #0258.
#   * `go` mode in the same sandbox — there is no go.mod, so `go build ./...`,
#     `go vet` and `go test` each fail as the FIRST stage of a pipeline into
#     `tail`. Honest verdict: VERIFICATION FAILED, exit 1 — but ONLY if
#     runpipe still carries -o pipefail, since without it the pipeline reports
#     tail's status, which is always 0. That is #0140/#0208/#0262.
#   * the same sandbox with the guard scripts present as `exit 0` stubs —
#     nothing fails, so the honest verdict is VERIFICATION PASSED, exit 0.
#     Without this row the two above could be satisfied by a check.sh that
#     simply always fails.
#
# Outcomes are classified from the exit code plus the verdict line:
#   passed    = exit 0 and VERIFICATION PASSED   (in a failing sandbox: a BYPASS)
#   failed    = VERIFICATION FAILED              (accounting intact)
#   selfcheck = neither verdict line             (one of check.sh's in-file
#               self-checks fired first — exit 2, or the NO VERDICT backstop)
#
# WHAT EACH PART ASSERTS
#
#   Part 1  The TRACKED scripts/check.sh is honest. This is the assertion that
#           closes the class: any edit to check.sh that makes it report
#           success over a failing step — including ones no in-file check can
#           see, such as a bare `FAILED=0` before the verdict — fails here,
#           because this part runs the tracked file itself.
#   Part 2  Sensitivity. Mutants that are EXPECTED to defeat check.sh's
#           in-file checks, proving Part 1's assertion is not vacuous. Same
#           role as db_reset_guard_test.sh's Part 3.
#   Part 3  Regression rows: every bypass shape #0208/#0251/#0258/#0262 have
#           reported, injected into a private copy, must not produce a
#           dishonest verdict. These assert the EARLY, line-naming signal the
#           in-file scans exist for — Part 1 would catch them anyway if they
#           reached the tracked file.
#   Part 4  No false positives: benign additions must leave check.sh working
#           rather than tripping a self-check.
#   Part 5  Byte-identity of the tracked scripts/check.sh across the run.
#
# SAFETY (CLAUDE.md §8a, §8b)
#
#   * The tracked scripts/check.sh is only ever READ. Every mutant is a copy
#     under a per-run mktemp directory, removed by the EXIT trap; nothing is
#     written into scripts/. (db_reset_guard_test.sh has to write its copies
#     into scripts/ because db-reset.sh resolves its repo root from its own
#     location; here that is exactly what we WANT to relocate, since check.sh
#     scanning and cd-ing to the sandbox is what makes the sandbox work.)
#   * ISSUE is unset for every child run, so no copy of check.sh ever creates
#     or drops a database. No port is bound. `shortlinks` is never touched.
#   * Every mutant is `bash -n` validated and confirmed to differ from its
#     source before it is run, so a payload that failed to inject cannot
#     masquerade as "not a bypass".
#
# Usage: scripts/check_guard_test.sh
# Exit 0 = all guards hold. Exit 1 = a regression was detected (message names it).

set -uo pipefail  # NOT -e: most commands here are EXPECTED to fail; that is the assertion

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
REAL="$REPO/scripts/check.sh"
BASHBIN="${BASH:-/bin/bash}"

# #0253's convention, matched here: namespace anything that could collide with
# a concurrent run of this script off ISSUE, falling back to $$.
RUNID_RAW="${ISSUE:-$$}"
RUNID="$(printf '%s' "$RUNID_RAW" | tr -cd '[:alnum:]' | tr '[:upper:]' '[:lower:]')"
[ -n "$RUNID" ] || { echo "error: empty run id (derived from ISSUE=$RUNID_RAW)" >&2; exit 1; }

command -v shasum >/dev/null || { echo "error: shasum not on PATH" >&2; exit 1; }
command -v awk    >/dev/null || { echo "error: awk not on PATH" >&2; exit 1; }
command -v go     >/dev/null || { echo "error: go not on PATH (needed for the pipefail oracle)" >&2; exit 1; }
command -v gofmt  >/dev/null || { echo "error: gofmt not on PATH (check.sh's go mode runs it)" >&2; exit 1; }
[ -f "$REAL" ] || { echo "error: $REAL not found" >&2; exit 1; }

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/check_guard_${RUNID}.XXXXXX")" || { echo "error: mktemp failed" >&2; exit 1; }
# shellcheck disable=SC2329  # invoked indirectly via `trap cleanup EXIT`
cleanup() { rm -rf "$WORKDIR"; }
trap cleanup EXIT
mkdir -p "$WORKDIR/pay" "$WORKDIR/sb" "$WORKDIR/mut"

FAILURES=0
fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }
fatal() { printf 'FATAL: %s\n' "$1" >&2; exit 1; }

SHA_BEFORE="$(shasum -a 256 "$REAL" | awk '{print $1}')"

# ---------------------------------------------------------------------------
# Anchors. Every insertion point is asserted to exist EXACTLY ONCE in the
# tracked file before anything is injected — an anchor that silently stopped
# matching would turn every row below into a vacuous pass.
# ---------------------------------------------------------------------------
A_PROBE1='^accounting_probe "before the dispatch"$'
A_PROBE2='^accounting_probe "after the dispatch'
A_END258='^# END GUARD-0258$'
A_VERDICT='^EXIT_ACCOUNTED=1$'
for a in "$A_PROBE1" "$A_PROBE2" "$A_END258" "$A_VERDICT"; do
  n="$(grep -cE "$a" "$REAL")"
  [ "$n" = "1" ] || fatal "anchor /$a/ matches $n lines of scripts/check.sh (expected exactly 1) — this test cannot inject at a point it cannot find"
done
RUN_LINE="$(grep -m1 -E '^[[:space:]]*run[[:space:]]*\(\)' "$REAL")"
RUNPIPE_LINE="$(grep -m1 -E '^[[:space:]]*runpipe[[:space:]]*\(\)' "$REAL")"
[ -n "$RUN_LINE" ] || fatal "could not find the run helper's definition line in scripts/check.sh"
[ -n "$RUNPIPE_LINE" ] || fatal "could not find the runpipe funnel's definition line in scripts/check.sh"

# The stub set is derived from the tracked file rather than hardcoded, so a
# new guard script wired into `guards` does not silently break Part 1.3.
GUARD_SCRIPTS="$(grep -oE 'scripts/[A-Za-z0-9_]*guard_test\.sh' "$REAL" | sort -u)"
[ -n "$GUARD_SCRIPTS" ] || fatal "found no guard scripts named in scripts/check.sh"

# ---------------------------------------------------------------------------
# Sandbox + run plumbing
# ---------------------------------------------------------------------------
mk_sandbox() {  # <name> <script> <kind: bare|stubs|web>  -> prints the sandbox dir
  local sb="$WORKDIR/sb/$1"
  rm -rf "$sb"; mkdir -p "$sb/scripts"
  cp "$2" "$sb/scripts/check.sh"; chmod +x "$sb/scripts/check.sh"
  if [ "$3" = "stubs" ]; then
    local g
    # deliberately unquoted: GUARD_SCRIPTS is a newline-separated list
    # shellcheck disable=SC2086
    for g in $GUARD_SCRIPTS; do
      printf '#!/bin/sh\nexit 0\n' > "$sb/$g"; chmod +x "$sb/$g"
    done
  fi
  [ "$3" = "web" ] && mkdir -p "$sb/web"
  printf '%s' "$sb"
}

RC=0; OUTFILE=""
run_case() {  # <sandbox> <mode>
  OUTFILE="$1/out"
  # stdin from /dev/null: a child that reads it would block on the terminal,
  # and under `scripts/check.sh guards` that is a silent hang.
  ( cd "$1" && env -u ISSUE "$BASHBIN" "$1/scripts/check.sh" "$2" ) > "$OUTFILE" 2>&1 < /dev/null
  RC=$?
}

# The colour escape is part of the pattern ON PURPOSE. check.sh's NO VERDICT
# message contains the literal words "VERIFICATION PASSED or VERIFICATION
# FAILED" in its prose, so a bare grep for either string reads a NO VERDICT run
# as an ordinary verdict -- an oracle matching the wrong occurrence, which is
# the exact defect class this whole issue is about. The real verdict lines are
# the only ones that carry the colour code immediately before the word.
classify() {  # -> passed | failed | selfcheck
  if [ "$RC" -eq 0 ] && grep -q '\[32mVERIFICATION PASSED' "$OUTFILE"; then echo passed
  elif grep -q '\[31mVERIFICATION FAILED' "$OUTFILE"; then echo failed
  else echo selfcheck; fi
}

mechanism() {  # a short tag naming what fired, read from the output rather than assumed
  local m
  m="$(grep -oE '(LIVE-PROBE REGRESSION|LIVE-ACCOUNTING REGRESSION|FAILED-ACCOUNTING REGRESSION|PIPEFAIL REGRESSION|FUNCTION-DEFINITION SCAN INCOMPLETE|NO VERDICT)' "$OUTFILE" | head -1)"
  [ -n "$m" ] || m="exit $RC"
  printf '%s' "$m"
}

check_mutant() {  # <mutant-path> <label> — differs from the tracked file, and parses
  cmp -s "$REAL" "$1" && fatal "$2: the mutation did not take effect (the copy is byte-identical to scripts/check.sh)"
  "$BASHBIN" -n "$1" 2>"$WORKDIR/synerr" || {
    printf '%s\n' "$(cat "$WORKDIR/synerr")" >&2
    fatal "$2: the mutant is not valid bash — it would be 'caught' for the wrong reason"
  }
}

# inject <payload-file> <anchor> <before|after> <src> <dst>
inject() {
  awk -v anchor="$2" -v pf="$1" -v pos="$3" '
    { if (pos == "before" && !done && $0 ~ anchor) { while ((getline l < pf) > 0) print l; close(pf); done = 1 }
      print
      if (pos == "after"  && !done && $0 ~ anchor) { while ((getline l < pf) > 0) print l; close(pf); done = 1 } }
  ' "$4" > "$5"
}

# TWO helpers on purpose. payload() reads the body from stdin, so it MUST be
# called with a heredoc; calling it without one made this script block forever
# reading the terminal, and under `scripts/check.sh guards` that is a hang with
# no output at all. paypath() is the no-stdin form, for payloads written by a
# following printf.
payload() {  # <name>, body on stdin -> prints the payload path
  local p="$WORKDIR/pay/$1"
  cat > "$p"
  printf '%s' "$p"
}

paypath() {  # <name> -> prints the payload path, reading nothing
  printf '%s' "$WORKDIR/pay/$1"
}

# The three builders below set $MUT rather than printing the path. That is not
# a style choice: called as "$(mut1 ...)" they would run in a SUBSHELL, and
# check_mutant's fatal() would then exit only that subshell -- the run would
# carry on with an empty path, "catch" a mutant that does not exist, and report
# PASS. That happened while writing this test; a guard test whose fixtures can
# silently fail to build is the exact failure mode it exists to prevent.
MUT=""
mut1() {  # <name> <payload> <anchor> <before|after>
  MUT="$WORKDIR/mut/$1.sh"
  inject "$2" "$3" "$4" "$REAL" "$MUT"
  check_mutant "$MUT" "$1"
}

mut2() {  # <name> <p1> <a1> <pos1> <p2> <a2> <pos2>
  local t="$WORKDIR/mut/$1.tmp"
  MUT="$WORKDIR/mut/$1.sh"
  inject "$2" "$3" "$4" "$REAL" "$t"
  inject "$5" "$6" "$7" "$t" "$MUT"
  check_mutant "$MUT" "$1"
}

# The two definition lines reach awk through the ENVIRONMENT, not through -v.
# awk processes escape sequences in a -v assignment, and both lines contain
# \033[31m...\033[0m\n -- so -v old="$RUN_LINE" hands awk a string with a real
# ESC and a real newline in it, which then matches nothing and the mutation
# silently does not happen. ENVIRON values are taken verbatim.
mutawk() {  # <name> <awk-program, reads ENVIRON["RUN_LINE"] / ENVIRON["RUNPIPE_LINE"]>
  MUT="$WORKDIR/mut/$1.sh"
  RUN_LINE="$RUN_LINE" RUNPIPE_LINE="$RUNPIPE_LINE" awk "$2" "$REAL" > "$MUT"
  check_mutant "$MUT" "$1"
}

# --- the two assertions every row is built from ----------------------------
assert_not_bypass() {  # <label> <mode> <kind> <script>
  local sb; sb="$(mk_sandbox "row_${1}" "$4" "$3")"
  run_case "$sb" "$2"
  case "$(classify)" in
    passed) fail "$1 [$2]: *** BYPASS *** — check.sh reported VERIFICATION PASSED, exit 0, over steps that genuinely failed" ;;
    failed) pass "$1 [$2]: not a bypass — VERIFICATION FAILED, exit $RC (the accounting survived the mutation)" ;;
    *)      pass "$1 [$2]: not a bypass — $(mechanism), exit $RC (a self-check fired before any work ran)" ;;
  esac
}

assert_bypass() {  # <label> <mode> <kind> <script> <why-this-row-exists>
  local sb; sb="$(mk_sandbox "row_${1}" "$4" "$3")"
  run_case "$sb" "$2"
  case "$(classify)" in
    passed) pass "$1 [$2]: reported VERIFICATION PASSED over failing steps, as this row requires — Part 1 is therefore sensitive to $5" ;;
    *)      fail "$1 [$2]: expected a DISHONEST verdict here (this row exists to prove Part 1 is not vacuous) but got $(classify), exit $RC. If a later pass genuinely closed this shape inside check.sh, move this row to Part 3 and assert the opposite." ;;
  esac
}

# ---------------------------------------------------------------------------
echo "== Part 1: the tracked scripts/check.sh reports honestly =="
# ---------------------------------------------------------------------------
SB="$(mk_sandbox p1_guards "$REAL" bare)"
run_case "$SB" guards
if [ "$RC" -eq 1 ] && grep -q '\[31mVERIFICATION FAILED' "$OUTFILE" && grep -q 'FAILED (127)' "$OUTFILE"; then
  pass "guards mode over steps that exit 127: VERIFICATION FAILED, exit 1, with the per-step FAILED lines printed (#0258 — the run helper's accounting)"
else
  fail "REGRESSION #0258: scripts/check.sh did not report a failing guards run honestly. exit=$RC, verdict=$(classify). Every step in that sandbox exits 127; anything but VERIFICATION FAILED means the failure accounting is gone. Last lines: $(tail -5 "$OUTFILE" | tr '\n' '|')"
fi

SB="$(mk_sandbox p1_go "$REAL" bare)"
run_case "$SB" go
if [ "$RC" -eq 1 ] && grep -q '\[31mVERIFICATION FAILED' "$OUTFILE"; then
  pass "go mode where each piped step fails in its FIRST stage: VERIFICATION FAILED, exit 1 (#0140/#0208/#0262 — the funnel still carries -o pipefail)"
else
  fail "REGRESSION #0140/#0262: scripts/check.sh did not report a failing piped run honestly. exit=$RC, verdict=$(classify). Without -o pipefail the pipeline reports tail's status, which is always 0, and a failing build passes silently. Last lines: $(tail -5 "$OUTFILE" | tr '\n' '|')"
fi

SB="$(mk_sandbox p1_pass "$REAL" stubs)"
run_case "$SB" guards
if [ "$RC" -eq 0 ] && grep -q '\[32mVERIFICATION PASSED' "$OUTFILE"; then
  pass "guards mode with every guard script stubbed to exit 0: VERIFICATION PASSED, exit 0 (so the two rows above are not satisfied by a check.sh that always fails)"
else
  fail "scripts/check.sh did not report a SUCCESSFUL run as passing. exit=$RC, verdict=$(classify). Either a self-check is firing on the tracked file, or the verdict never reports success — both make every other row in this test meaningless. Last lines: $(tail -8 "$OUTFILE" | tr '\n' '|')"
fi

if command -v npm >/dev/null; then
  SB="$(mk_sandbox p1_web "$REAL" web)"
  run_case "$SB" web
  if [ "$RC" -eq 1 ] && grep -q '\[31mVERIFICATION FAILED' "$OUTFILE"; then
    pass "web mode against an empty web/ (npm fails inside the pipeline): VERIFICATION FAILED, exit 1 — the npm|tail shape #0262's reviews used, cross-checking the go-mode oracle above"
  else
    fail "REGRESSION #0262: scripts/check.sh did not report a failing npm pipeline honestly. exit=$RC, verdict=$(classify)."
  fi
else
  echo "SKIP: npm not on PATH — the go-mode row above already covers the pipefail property"
fi

# ---------------------------------------------------------------------------
echo
echo "== Part 2: sensitivity — mutants that MUST come back dishonest =="
# These prove Part 1 can tell an honest verdict from a dishonest one. They are
# the only rows here that expect VERIFICATION PASSED over failing steps.
# ---------------------------------------------------------------------------
mutawk verdict_gone '/^if \[ "\$FAILED" -ne 0 \]/ { next } { print }'
assert_bypass verdict_gone guards bare "$MUT" "a verdict that no longer consults the failure state at all"

P="$(payload failed_zero <<'PAY'
FAILED=0
PAY
)"
mut1 failed_zero "$P" "$A_VERDICT" before
# #0258's sixth review, bypass 4: ONE appended line, in plain sight, discarding
# the whole verdict with the FAILED (127) lines still printed above it. It is
# NOT an accepted gap and does not belong beside #0208's mutant 7 — but it is
# also not closable inside check.sh: nothing can stop an assignment to the
# variable the verdict reads. Part 1 is its closure, because Part 1 runs the
# TRACKED file; this row is what proves Part 1 would notice.
assert_bypass failed_zero guards bare "$MUT" "a bare FAILED=0 inserted before the verdict (#0258 sixth review, bypass 4)"
mut1 failed_zero_go "$P" "$A_VERDICT" before
assert_bypass failed_zero_go go bare "$MUT" "the same assignment on the piped-step path"

# ---------------------------------------------------------------------------
echo
echo "== Part 3: known bypass shapes must not produce a dishonest verdict =="
# ---------------------------------------------------------------------------

# -- #0258's sixth review, bypass 1: the live probe itself was pinned to
#    nothing, so one added line replaced it with a stub and the alias route
#    below it was then unopposed. Three lines, nothing existing edited.
P="$(payload probe_stub_run <<'PAY'
accounting_probe() { :; }
shopt -s expand_aliases
alias run=true
PAY
)"
mut1 probe_stub_run "$P" "$A_PROBE1" after
assert_not_bypass probe_stub_run guards bare "$MUT"

P="$(payload probe_stub_runpipe <<'PAY'
accounting_probe() { :; }
shopt -s expand_aliases
alias runpipe=true
PAY
)"
mut1 probe_stub_runpipe "$P" "$A_PROBE1" after
assert_not_bypass probe_stub_runpipe go bare "$MUT"

# -- the same idea in spellings the single-line candidate scan cannot see, so
#    only the live pin can catch them.
P="$(payload probe_multiline <<'PAY'
accounting_probe() {
  :
}
shopt -s expand_aliases
alias run=true
PAY
)"
mut1 probe_multiline "$P" "$A_PROBE1" after
assert_not_bypass probe_multiline guards bare "$MUT"

P="$(payload probe_unset <<'PAY'
unset -f accounting_probe
shopt -s expand_aliases
alias run=true
PAY
)"
mut1 probe_unset "$P" "$A_PROBE1" after
assert_not_bypass probe_unset guards bare "$MUT"

# -- bypass 2: a function named `alias` shadows the builtin, because function
#    lookup precedes builtin lookup, so the probe's own alias query reports
#    nothing while the dispatch gets the aliased helper.
P="$(payload alias_fn_shadow <<'PAY'
shopt -s expand_aliases
alias run=true
alias() { return 1; }
PAY
)"
mut1 alias_fn_shadow "$P" "$A_PROBE1" after
assert_not_bypass alias_fn_shadow guards bare "$MUT"

# -- bypass 3: a decoy after the first probe plus a BYTE-IDENTICAL copy of the
#    real definition before the second. Both probes pass (the helper really is
#    intact at both moments) and the digest passes (last definition wins, and
#    the last one is the real line). Each half is separately benign; the
#    pairing is the bypass.
P1="$(payload decoy_run <<'PAY'
run() { "$@"; return 0; }
PAY
)"
P2="$WORKDIR/pay/restore_run"; printf '%s\n' "$RUN_LINE" > "$P2"
mut2 decoy_restore "$P1" "$A_PROBE1" after "$P2" "$A_PROBE2" before
assert_not_bypass decoy_restore guards bare "$MUT"

P1P="$(payload decoy_runpipe <<'PAY'
runpipe() { run bash -c "$1"; }
PAY
)"
P2P="$WORKDIR/pay/restore_runpipe"; printf '%s\n' "$RUNPIPE_LINE" > "$P2P"
mut2 decoy_restore_pipe "$P1P" "$A_PROBE1" after "$P2P" "$A_PROBE2" before
assert_not_bypass decoy_restore_pipe go bare "$MUT"

# -- #0258's fifth review: "." is the POSIX spelling of source and spells
#    neither guarded name, so it bound a decoy the scan never saw.
# shellcheck disable=SC2016  # the "$@" and "$1" here are payload TEXT for the
# decoy files, not expansions this script wants performed.
printf 'run() { "$@"; return 0; }\n' > "$WORKDIR/decoy.sh"
# shellcheck disable=SC2016
printf 'runpipe() { run bash -c "$1"; }\n' > "$WORKDIR/decoy_pipe.sh"
P="$(paypath dot_source)" ; printf '. %s\n' "$WORKDIR/decoy.sh" > "$P"
mut1 dot_source "$P" "$A_END258" after
assert_not_bypass dot_source guards bare "$MUT"
P="$(paypath kw_source)"  ; printf 'source %s\n' "$WORKDIR/decoy.sh" > "$P"
mut1 kw_source "$P" "$A_END258" after
assert_not_bypass kw_source guards bare "$MUT"
P="$(paypath dot_source_pipe)"; printf '. %s\n' "$WORKDIR/decoy_pipe.sh" > "$P"
mut1 dot_source_pipe "$P" "$A_END258" after
assert_not_bypass dot_source_pipe go bare "$MUT"

# -- aliases on their own, at a position where the probe body is parsed AFTER
#    the alias is set (so behaviour alone cannot see them and the alias/shopt
#    queries have to).
P="$(payload alias_run <<'PAY'
shopt -s expand_aliases
alias run=true
PAY
)"
mut1 alias_run "$P" "$A_END258" after
assert_not_bypass alias_run guards bare "$MUT"
P="$(payload alias_runpipe <<'PAY'
shopt -s expand_aliases
alias runpipe=true
PAY
)"
mut1 alias_runpipe "$P" "$A_END258" after
assert_not_bypass alias_runpipe go bare "$MUT"

# -- removal rather than replacement
P="$(payload unset_run <<'PAY'
unset -f run
PAY
)"
mut1 unset_run "$P" "$A_END258" after
assert_not_bypass unset_run guards bare "$MUT"
P="$(payload unset_runpipe <<'PAY'
unset -f runpipe
PAY
)"
mut1 unset_runpipe "$P" "$A_END258" after
assert_not_bypass unset_runpipe go bare "$MUT"

# -- the third review's four one-line decoy spellings, which defeated the
#    original "^run\(\)" anchor
P="$(paypath sp_leading)"   ; printf ' run() { "$@"; return 0; }\n' > "$P"
mut1 sp_leading "$P" "$A_END258" after
assert_not_bypass sp_leading guards bare "$MUT"
P="$(paypath kw_function)"  ; printf 'function run { "$@"; return 0; }\n' > "$P"
mut1 kw_function "$P" "$A_END258" after
assert_not_bypass kw_function guards bare "$MUT"
P="$(paypath sp_parens)"    ; printf 'run () { "$@"; return 0; }\n' > "$P"
mut1 sp_parens "$P" "$A_END258" after
assert_not_bypass sp_parens guards bare "$MUT"
P="$(paypath tab_indent)"   ; printf '\trun() { "$@"; return 0; }\n' > "$P"
mut1 tab_indent "$P" "$A_END258" after
assert_not_bypass tab_indent guards bare "$MUT"

# -- the fourth review's four one-line shapes, which defeated the general
#    one-line recogniser
P="$(paypath trailing_semi)"; printf 'run() { "$@"; return 0; };\n' > "$P"
mut1 trailing_semi "$P" "$A_END258" after
assert_not_bypass trailing_semi guards bare "$MUT"
P="$(paypath subshell_body)"; printf 'run() ( "$@"; return 0 )\n' > "$P"
mut1 subshell_body "$P" "$A_END258" after
assert_not_bypass subshell_body guards bare "$MUT"
P="$(paypath group_cmd)"    ; printf '{ run() { "$@"; return 0; }; }\n' > "$P"
mut1 group_cmd "$P" "$A_END258" after
assert_not_bypass group_cmd guards bare "$MUT"
P="$(paypath if_wrapped)"   ; printf 'if true; then run() { "$@"; return 0; }; fi\n' > "$P"
mut1 if_wrapped "$P" "$A_END258" after
assert_not_bypass if_wrapped guards bare "$MUT"

# -- a multi-line reformat of the helper, which the candidate scan refuses by
#    design (run and runpipe must stay one-line definitions)
P="$(payload multiline_run <<'PAY'
run() {
  "$@"
  return 0
}
PAY
)"
mut1 multiline_run "$P" "$A_END258" after
assert_not_bypass multiline_run guards bare "$MUT"

# -- a definition built by eval, which no text scan of the definition can read
P="$(paypath eval_built)"; printf 'eval %s\n' "'run() { \"\$@\"; return 0; }'" > "$P"
mut1 eval_built "$P" "$A_END258" after
assert_not_bypass eval_built guards bare "$MUT"

# -- the original #0258 defect: the accounting stripped IN PLACE, and #0251's:
#    the flag stripped from the funnel in place
mutawk strip_accounting '$0 == ENVIRON["RUN_LINE"] { print "run()  { \"$@\"; return 0; }"; next } { print }'
assert_not_bypass strip_accounting guards bare "$MUT"
mutawk strip_pipefail '$0 == ENVIRON["RUNPIPE_LINE"] { print "runpipe() { run bash -c \"$1\"; }"; next } { print }'
assert_not_bypass strip_pipefail go bare "$MUT"

# -- #0208's combined-short-option evasion (-cu has no word boundary after the
#    "c", which the original '-c\b' pattern needed)
P="$(payload combined_shortopt <<'PAY'
run bash -cu "true | tail -1"
PAY
)"
mut1 combined_shortopt "$P" "$A_END258" after
assert_not_bypass combined_shortopt guards bare "$MUT"

# -- a step that ENDS the script instead of reporting. #0258's sixth pass found
#    the fifth-pass script exited 0 with NO verdict line at all under a
#    terminating helper, which a caller reading only the exit code reads as
#    success; EXIT_ACCOUNTED plus the EXIT trap turn that into a loud NO
#    VERDICT, exit 2. Delivered here as a bare `exit 0` right after the trap is
#    installed, because a terminating helper cannot in fact be delivered by
#    dot-sourcing any more -- the widened "." pattern catches that first, which
#    is a correction to the sixth pass's own note about which check fires.
P="$(paypath early_exit)"; printf 'exit 0\n' > "$P"
mut1 early_exit "$P" '^trap cleanup EXIT$' after
assert_not_bypass early_exit guards bare "$MUT"

# ---------------------------------------------------------------------------
echo
echo "== Part 4: benign edits must NOT trip a self-check =="
# A guard that fires on innocent lines gets disabled by the next person who
# hits it, so the false-positive direction matters as much as the other one.
# ---------------------------------------------------------------------------
assert_benign() {  # <label> <script>
  local sb; sb="$(mk_sandbox "row_${1}" "$2" bare)"
  run_case "$sb" guards
  case "$(classify)" in
    failed) pass "$1: benign — check.sh still ran and reported VERIFICATION FAILED honestly" ;;
    passed) fail "$1: this benign edit produced VERIFICATION PASSED over failing steps" ;;
    *)      fail "$1: FALSE POSITIVE — a self-check fired ($(mechanism), exit $RC) on an edit that changes nothing about the helpers. Output: $(tail -3 "$OUTFILE" | tr '\n' '|')" ;;
  esac
}

# an ordinary helper containing a "#" inside a string — the shape that blinded
# both digests under pass 4's comment-stripping sed
P="$(payload ordinary_helper <<'PAY'
hdr_0258_probe() { printf '# %s\n' "$1"; }
PAY
)"
mut1 ordinary_helper "$P" "$A_END258" after
assert_benign ordinary_helper "$MUT"

# a whole-line comment naming every guarded token at once
P="$(paypath comment_only)"; printf '%s\n' '# mentions run() runpipe() eval source unset and . /tmp/x all at once' > "$P"
mut1 comment_only "$P" "$A_END258" after
assert_benign comment_only "$MUT"

# trailing whitespace after the helper's closing brace — a no-op edit that an
# earlier version of this guard rejected (#0258's third item)
mutawk trailing_ws '$0 == ENVIRON["RUN_LINE"] { print $0 "   "; next } { print }'
assert_benign trailing_ws "$MUT"

# ---------------------------------------------------------------------------
echo
echo "== Part 5: byte-identity of the tracked scripts/check.sh =="
# ---------------------------------------------------------------------------
SHA_AFTER="$(shasum -a 256 "$REAL" | awk '{print $1}')"
if [ "$SHA_BEFORE" = "$SHA_AFTER" ]; then
  pass "scripts/check.sh unchanged across the run (sha256 $SHA_AFTER) — every mutation happened on a private copy under $WORKDIR, never the tracked file"
else
  fail "CRITICAL: scripts/check.sh's sha256 changed during this test run ($SHA_BEFORE -> $SHA_AFTER). Investigate immediately with: git diff -- scripts/check.sh"
fi

echo
if [ "$FAILURES" -eq 0 ]; then
  echo "check_guard_test.sh: all guards hold (0 failures)"
  exit 0
else
  echo "check_guard_test.sh: $FAILURES failure(s) -- see FAIL lines above"
  exit 1
fi
