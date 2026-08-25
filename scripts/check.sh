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
# #0258 (6th pass): set to 1 only where this script accounts for its own
# exit -- the verdict at the bottom, or a self-check that deliberately
# exits 2. The EXIT trap installed further down turns any OTHER exit into
# a loud NO VERDICT report, so a shadowed helper that simply calls `exit 0`
# cannot end the run with a success status and no verdict line. An
# interrupted run (Ctrl-C) reports NO VERDICT too, which is accurate.
EXIT_ACCOUNTED=0

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
# #0258 / #0262 — a TEXT SCAN over this file, in two parts, that fails
# CLOSED on the shapes it recognises. Read LIVEBIND-0258 further down first:
# that block holds the check that does not have to recognise anything, and it
# is what makes this one a defence-in-depth early warning rather than the
# thing the guarantee rests on.
#
# BE CLEAR ABOUT WHAT THIS IS. Part 2 below inverted the failure direction —
# an unrecognised line is an error rather than a silent skip — and that was
# the right move. But the test for "could this line bind a name" is ITSELF a
# recogniser, spelled as a list of keywords, so the guard is sound only for
# the binding constructs that list enumerates. #0258's fifth review proved
# the point at once: the list read eval/source/unset and omitted ".", the
# POSIX spelling of source, which is what any script that splits into
# libraries uses; one `. lib.sh` line bound a decoy the scan never saw and
# the run reported success over a failed step. "." and an adjacent
# builtin/command are in the list now. THAT DOES NOT MAKE THE LIST COMPLETE,
# and no edit to it ever will — this is a maintained enumeration, not a
# proof. It is worth keeping because it fires EARLY (before any database or
# test) and because it names the offending line, but the completeness
# argument lives in LIVEBIND-0258, not here.
#
# Four passes tried to answer this with a recogniser, and each failed OPEN
# on the shapes it had not been shown. Pass 3 anchored on the literal
# column-0 spelling "^run\(\)" and missed a leading space, the `function`
# keyword, a space before the parens, and a tab. Pass 4 widened that to a
# general one-line shape and missed a trailing ";", a subshell body
# `run() ( ... )`, a group command `{ run() { ...; }; }`, and
# `if true; then run() { ...; }; fi`. All four are valid bash 3.2.57, all
# four shadow the real definition at call time, and all four made the
# pass-4 script report `VERIFICATION PASSED`, exit 0, over steps that had
# genuinely failed (the first of them re-confirmed end to end against a
# real failing Go test: `--- FAIL` printed, exit 0). Widening a fifth
# time would lose to a sixth shape; the recogniser itself is the problem,
# so this pass makes an unrecognised shape a HARD ERROR instead.
#
# Two parts:
#
#   1. resolve_func_hash NAME FILE — take the lines that ARE recognised as
#      complete single-line definitions, hand ONLY those to a real bash in
#      file order, and hash bash's own `declare -f NAME` rendering of
#      whatever won the shadowing. Bash's parser decides what the text
#      means; last definition wins, exactly as it would when the script
#      runs. One deliberate over-approximation: a candidate nested inside
#      another function's body is treated as if it took effect, even
#      though it would only bind when that outer function is called.
#      Measured — a `run() { ...; }` line inside `go_test`'s body is
#      CAUGHT. That errs towards firing, which is the safe direction.
#      `declare -f` canonicalises away non-semantic differences
#      only — #0258's fourth review measured ten pairs and found collisions
#      solely on inter-token whitespace and `;`-vs-newline, while every
#      meaning-bearing pair stayed distinct (`[ $r -eq 0 ]` vs
#      `[ "$r" -eq 0 ]`, `'a b'` vs `"a b"`, `a; b` vs `a && b`, `"$@"` vs
#      `$@`). The pinned digests the two guards below compare against are
#      unrelated bytes to the code they verify, so CLAUDE.md §8's
#      oracle-independence rule still holds.
#
#   2. funcdef_unexplained FILE — assert that the recognised set EXPLAINS
#      THE WHOLE FILE. Any line that could bind, rebind or unbind either
#      name must be one of the admitted candidate lines, or else be a
#      whole-line comment. Anything else is a hard error. "Could bind" is
#      spelled out as: the name followed by "(" (covers `run()`,
#      `run ()`, the trailing-";" / subshell-body / group-command /
#      `if`-wrapped shapes above, a multi-line `run() {` header, and
#      `eval 'run() { ... }'`); the `function` keyword followed by the
#      name (covers `function run {` in any position); a "." in COMMAND
#      POSITION; or one of FUNCDEF_BIND_KEYWORDS, or a builtin/command
#      adjacent to either, anywhere on a code line — each being a way to
#      bind a name to text this scan never sees. A shape this list DOES
#      name is a loud, actionable failure rather than a silent skip. A
#      binding construct it does not name is still invisible here; that is
#      the residual LIVEBIND-0258 exists to cover, and an alias is the
#      standing example of one deliberately left out of this list.
#
# CONSEQUENCES for maintaining this file — real constraints, not caveats:
#   * run() and runpipe() must stay ONE-LINE definitions. Reformatting
#     either across several lines is refused by (2) with an explanation.
#     #0258's fourth review judged that gap MORE reachable than #0208's
#     accepted mutant 7, precisely because multi-line is the ordinary way
#     to write a function; it is now a loud error rather than a silent
#     hole.
#   * This file may not spell `eval`, `source`, `unset`, or a "." in
#     command position, outside a whole-line comment. None appears today. This is also what makes (2)'s
#     comment exclusion sound: a line whose first non-blank character is
#     "#" is a comment, and in the only other place such a line could
#     appear — inside a heredoc or a quoted string — it is inert data,
#     because nothing in this file evaluates data as code.
#   * A mention of "run(" or "runpipe(" inside a STRING on a code line is
#     refused too: (2) reads lines, not bash's grammar, and cannot tell a
#     string from code. The guard messages below say "the run helper" and
#     "the runpipe funnel" for exactly this reason.
#
# WHAT REMAINS OPEN HERE: any binding construct the suspect list does not
# name. Five passes established there is always another one — the list is a
# best effort, not a boundary. This is stated rather than argued away
# because the previous four passes each argued the opposite and were each
# proved wrong by the next review. What closes the residual is not a wider
# list; it is LIVEBIND-0258, which measures the binding instead of
# predicting it.
#
# ISOLATION (CLAUDE.md §8, from #0258's fourth review). The candidate lines
# run in `env -i "$BASH" --noprofile --norc FILE` — a separate process with
# an EMPTY environment — NOT in a `( ... )` subshell of this script. A
# subshell inherits this script's own run/runpipe/step/cleanup, so whenever
# the extraction contributed nothing, `declare -f` fell back to the LIVE
# definition and the guard silently stopped measuring the file and started
# measuring the running process, which always agrees with itself. Measured
# both ways: with an empty candidate file the old subshell printed the live
# run(); this form prints nothing and returns 1. An empty result is an
# ERROR, never an input to the hash — both callers below exit 2 on it.
#
# NO COMMENT-STRIPPING SED. Pass 4 ran the candidates through
# `sed -E 's/[[:space:]]*#.*$//'`, which is not quote-aware: an ordinary
# helper such as `hdr() { printf '# %s\n' "$1"; }` was truncated mid-string
# and its unterminated quote swallowed every later candidate, blinding both
# digests with no signal at all. bash discards comments itself and
# `declare -f` never renders them, so the sed was unnecessary as well as
# harmful. Measured after removing it: that helper is now an ordinary
# candidate that parses fine and changes nothing (adding it alone fires no
# check), and helper-plus-decoy — the combination that produced a silent
# `VERIFICATION PASSED` under pass 4 — is caught by the digest, because the
# decoy now actually reaches bash and wins the shadowing. The empty-result
# assertion below is the backstop for every OTHER way the extraction can
# come back with nothing.
#
# Why a FILE and not `bash -c '...'`: GUARD-0208's shell-"-c" scan below
# exists to prove the runpipe funnel is the only place in this file that
# runs a shell with a command string, and a literal `bash ... -c` here
# would trip it. Passing a file avoids the flag entirely.
#
# To recompute either pinned digest after a legitimate change (a marker
# range, not a line number, so it does not drift as the file grows):
#   sed -n '/^# BEGIN FUNCDEF-0258/,/^# END FUNCDEF-0258/p' scripts/check.sh \
#     > /tmp/funcdef.sh \
#     && bash -c '. /tmp/funcdef.sh; resolve_func_hash run scripts/check.sh'
command -v shasum >/dev/null || { echo "error: shasum not on PATH (needed by FUNCDEF-0258)" >&2; exit 2; }
command -v mktemp >/dev/null || { echo "error: mktemp not on PATH (needed by FUNCDEF-0258)" >&2; exit 2; }
command -v env >/dev/null || { echo "error: env not on PATH (needed by FUNCDEF-0258)" >&2; exit 2; }
FUNCDEF_CANDIDATE_RE='[[:space:]]*(function[[:space:]]+[A-Za-z_][A-Za-z0-9_]*([[:space:]]*\(\))?|[A-Za-z_][A-Za-z0-9_]*[[:space:]]*\(\))[[:space:]]*\{.*\}[[:space:]]*(#.*)?'
# The "()" is REQUIRED unless the `function` keyword is present. Pass 4 made
# it optional, which admitted `NAME { ...; }` — and for a shell keyword that
# takes a brace group that is a COMMAND, not a definition: `time { touch X; }`
# matched, and sourcing it ran and created the file. Requiring one of the two
# definition markers is what makes "sourcing a candidate only registers a
# function" true rather than merely asserted.
# Built from concatenated pieces (no separator between the closing and
# opening quotes) so THIS line's own text does not match the pattern it
# defines — the same trick BG/EG use in GUARD-0208 below.
FUNCDEF_BIND_KEYWORDS="ev""al|sou""rce|un""set"
# #0258 (6th pass): "." -- the POSIX spelling of the source builtin -- was
# missing from the list above, and it is the spelling a script that splits
# into libraries ordinarily uses. It CANNOT join the alternation as a
# word-boundary keyword: as one it matches 54 lines of this file (./internal/...,
# 2>&1, ordinary prose), which is unusable. It is matched in COMMAND POSITION
# instead -- start of line, or straight after a ";", "&", "|" or "{". Measured
# on this file: catches ". f", a leading-space ". f" and "true; . f", with no
# false positive. "builtin" and "command" cannot join as bare words either --
# they collide with this file's own `command -v shasum|mktemp|env` lines -- so
# they are matched only when ADJACENT to a binding word.
FUNCDEF_SUSPECT_RE="(^|[^A-Za-z0-9_])(run|runpipe)[[:space:]]*\\(|(^|[^A-Za-z0-9_])function[[:space:]]+(run|runpipe)([^A-Za-z0-9_]|\$)|(^|[^A-Za-z0-9_])($FUNCDEF_BIND_KEYWORDS)([^A-Za-z0-9_]|\$)|(^|[;&|{][[:space:]]*)[[:space:]]*\\.[[:space:]]|(^|[^A-Za-z0-9_])(builtin|command)[[:space:]]+(\\.|$FUNCDEF_BIND_KEYWORDS)([^A-Za-z0-9_]|\$)"

# Prints every line of FILE that could bind one of the guarded names and was
# NOT admitted as a candidate, "N:text". Empty output means the candidate
# set explains the whole file.
funcdef_unexplained() {
  grep -nE "$FUNCDEF_SUSPECT_RE" "$1" \
    | grep -vE '^[0-9]+:[[:space:]]*#' \
    | grep -vE "^[0-9]+:${FUNCDEF_CANDIDATE_RE}\$"
}

# Prints the SHA-256 of bash's own `declare -f NAME` rendering of whatever
# the candidate lines of FILE bind NAME to. Returns 1 — printing nothing —
# if that is empty, so a broken extraction can never be mistaken for a
# matching one.
resolve_func_hash() {
  local name="$1" file="$2" tmp out
  [ -n "${BASH:-}" ] || return 1
  tmp="$(mktemp)" || return 1
  grep -E "^${FUNCDEF_CANDIDATE_RE}\$" "$file" > "$tmp"
  printf 'declare -f %s\n' "$name" >> "$tmp"
  out="$(env -i "$BASH" --noprofile --norc "$tmp" 2>/dev/null)"
  rm -f "$tmp"
  [ -n "$out" ] || return 1
  printf '%s\n' "$out" | shasum -a 256 | awk '{print $1}'
}
# END FUNCDEF-0258

step "self-check: function-definition scan is complete (#0258/#0262)"
FUNCDEF_UNEXPLAINED="$(funcdef_unexplained "$SELF")"
if [ -n "$FUNCDEF_UNEXPLAINED" ]; then
  printf '\033[31mFUNCTION-DEFINITION SCAN INCOMPLETE (#0258/#0262): scripts/check.sh has line(s) matching this scan'"'"'s SUSPECT pattern that its single-line candidate scan did not admit. THAT IS ALL IT DETECTED. The scan reads lines, not bash'"'"'s grammar, so it cannot tell whether such a line really touches the run helper or the runpipe funnel and does not claim to -- it fails CLOSED because every silent skip in this guard'"'"'s history turned out to be a live bypass. The pattern matches either helper name before a "(", the "function" keyword before either name, a "." in command position, or any of: %s. Keeping those out of code in this file is a real maintenance constraint (see FUNCDEF-0258 above) and it applies even when the line is innocent: an unrelated variable being cleared has to be written as an empty assignment instead, and an unrelated file being read in has to move. So: rewrite the line, or -- only when the mention is purely illustrative -- move it into a whole-line comment. Offending line(s):\033[0m\n' "$FUNCDEF_BIND_KEYWORDS" >&2
  printf '%s\n' "$FUNCDEF_UNEXPLAINED" >&2
  exit 2
fi

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
  printf '\033[31mPIPEFAIL REGRESSION (#0208): "pipefail" appears in scripts/check.sh outside the runpipe funnel'"'"'s own definition. A call site may be spelling the flag directly instead of calling the funnel, or the funnel was duplicated. Offending line(s):\033[0m\n' >&2
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
  printf '\033[31mPIPEFAIL REGRESSION (#0208): scripts/check.sh has a shell "-c" invocation outside the runpipe funnel — it could silently lose "-o pipefail" the same way #0140 did. Offending line(s):\033[0m\n' >&2
  printf '%s\n' "$SHELLC_HITS" >&2
  exit 2
fi

# 11 = the 9 real call sites (one per go build/vet/test and npm check/test
# step) plus the 2 in accounting_probe below, which are load-bearing: the live
# probe is what closes every binding shape this file's patterns cannot name, so
# deleting it must fail this floor rather than quietly reduce the coverage.
RUNPIPE_CALLS="$(printf '%s\n' "$SCAN" | grep -c 'runpipe "' || true)"
if [ "${RUNPIPE_CALLS:-0}" -lt 11 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208): expected at least 11 runpipe call sites (9 real steps + 2 in the live accounting probe); found %s. A step may have reverted to spelling a shell invocation directly instead of calling the funnel, or the live probe was removed.\033[0m\n' "$RUNPIPE_CALLS" >&2
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
# independently of SHELLC_HITS: the four shapes were re-measured with
# bodies reaching the shell through a variable, so SHELLC_HITS could not
# supply the catch, and the digest caught all four on its own.
#
# #0258 (4th bounce): that is the whole truth only for shapes the
# candidate scan admits. Four MORE one-line spellings (a trailing ";",
# a subshell body, a group command, an `if`-wrapper) were admitted by
# neither, and each silently lost "-o pipefail". They are not closed by
# widening the recogniser — they are closed by funcdef_unexplained above,
# which refuses to let ANY unadmitted line that could bind these names
# pass. So the masking dependency is removed for every static one-line
# shape: an admitted one is caught by the digest, an unadmitted one is a
# hard error. The residual is named in FUNCDEF-0258's own comment, not
# here.
BR_COUNT="$(grep -cxF "$BR" "$SELF")"
ER_COUNT="$(grep -cxF "$ER" "$SELF")"
if [ "${BR_COUNT:-0}" -ne 1 ] || [ "${ER_COUNT:-0}" -ne 1 ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251): expected the RUNPIPE-0208 marker pair exactly once in scripts/check.sh; found %s BEGIN and %s END. A duplicate marker block can supply a decoy match for every check in this guard, not just this one.\033[0m\n' "$BR_COUNT" "$ER_COUNT" >&2
  exit 2
fi

# An empty resolution is an ERROR, never an input (CLAUDE.md §8): it means
# the extraction produced nothing bash could parse, which is exactly the
# state pass 4 silently mistook for agreement.
RUNPIPE_HASH="$(resolve_func_hash runpipe "$SELF")" || {
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251/#0262): could not resolve the runpipe funnel from scripts/check.sh at all — the candidate extraction produced nothing bash could parse and bind. Failing closed; see FUNCDEF-0258 above.\033[0m\n' >&2
  exit 2
}
RUNPIPE_EXPECTED_SHA256="546fb9901c1dff71af78735fcfa14427eaea0f2d7bc8a2a0b5e977111e5a5c3a"
if [ "$RUNPIPE_HASH" != "$RUNPIPE_EXPECTED_SHA256" ]; then
  printf '\033[31mPIPEFAIL REGRESSION (#0208/#0251/#0262): the runpipe funnel'"'"'s resolved definition (bash'"'"'s own "declare -f runpipe" rendering, hashed — see FUNCDEF-0258 above) no longer matches the pinned SHA-256 digest. The funnel this self-check exists to guarantee may have lost the -o pipefail flag, or been shadowed by a later redefinition in any spelling bash accepts. See the recompute recipe in FUNCDEF-0258 above (substitute runpipe for run) if this change is legitimate.\033[0m\n' >&2
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
# is — it doesn't need one: FUNCDEF-0258's candidate scan reads the whole
# file with nothing excluded, so there is no decoy-marker-block class to
# close here the way GUARD-0208 needed BR_COUNT/ER_COUNT for a second
# RUNPIPE-0208 pair.
#
# This guard has been through four review rounds (issues/0258.md), each
# closing the specific bypass it was shown and each losing to the next
# shape. This pass is the fifth, and it stops closing shapes:
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
#   - pass 4 replaced the narrow anchor with a general one-line shape
#     recogniser feeding bash's own parser, keeping the pinned digest.
#     The fourth review found four MORE one-line spellings it did not
#     recognise (`run() { ...; };`, `run() ( ... )`,
#     `{ run() { ...; }; }`, `if true; then run() { ...; }; fi`), plus
#     two structural defects: the extraction ran in a subshell of THIS
#     script, so a failed extraction measured the live process instead
#     of the file, and its comment-stripping sed was not quote-aware, so
#     one ordinary helper containing a "#" inside a string blinded both
#     digests at once.
#   - pass 5 kept the pinned digest — CLAUDE.md §8's rule was already
#     satisfied and should not be re-litigated — kept bash as the
#     recogniser, fixed both structural defects, and made the scan FAIL
#     CLOSED: an unadmitted line that could bind either name became a
#     hard error rather than a silent skip. The fifth review then showed
#     that the "could bind" test is itself a recogniser: its keyword list
#     omitted "." (source) and could not see an alias at all, and each
#     was a live bypass.
#   - pass 6 stops trying to make that list complete. It widens it once
#     more, honestly labelled as an enumeration, and adds LIVEBIND-0258
#     below: a probe that EXERCISES whatever the two helpers are bound to
#     — before the dispatch and again immediately before the verdict — so
#     the guarantee no longer depends on recognising how a binding got
#     there. Everything above stays as defence in depth and as the early,
#     line-naming signal.
#
# The recompute recipe for both pinned digests lives in FUNCDEF-0258's
# comment above, next to the function it drives.
step "self-check: run FAILED-accounting guard (#0258)"
# An empty resolution is an ERROR, never an input — see the sibling check
# in GUARD-0208 above.
RUN_HASH="$(resolve_func_hash run "$SELF")" || {
  printf '\033[31mFAILED-ACCOUNTING REGRESSION (#0258): could not resolve the run helper from scripts/check.sh at all — the candidate extraction produced nothing bash could parse and bind. Failing closed; see FUNCDEF-0258 above.\033[0m\n' >&2
  exit 2
}
RUN_EXPECTED_SHA256="b5ca2df355ef7f7c518968a3d77eff626b48ce546cde2c8a820d62c38a2f9a24"
if [ "$RUN_HASH" != "$RUN_EXPECTED_SHA256" ]; then
  printf '\033[31mFAILED-ACCOUNTING REGRESSION (#0258): the run helper'"'"'s resolved definition (bash'"'"'s own "declare -f run" rendering, hashed — see FUNCDEF-0258 above) no longer matches the pinned SHA-256 digest. The FAILED=1 accounting this whole script'"'"'s pass/fail report depends on may have been removed, or shadowed by a later redefinition in any spelling bash accepts. See the recompute recipe in FUNCDEF-0258 above if this change is legitimate.\033[0m\n' >&2
  printf 'sha256: %s (expected %s)\n' "$RUN_HASH" "$RUN_EXPECTED_SHA256" >&2
  exit 2
fi
# END GUARD-0258

# BEGIN LIVEBIND-0258
# #0258 / #0262 -- the check that does not have to recognise anything, and
# the reason the two scans above no longer have to be complete.
#
# Everything above this point PREDICTS. It reads this file's text and asks
# "could this line rebind the run helper or the runpipe funnel?" Five review
# rounds established that the question cannot be answered by reading lines.
# Pass 3 lost to four spellings of a definition; pass 4 to four more; pass 5
# to "." -- the POSIX spelling of the source builtin, which needs no
# obfuscation, is what any script that splits into libraries uses, and spells
# neither guarded name anywhere -- and to a two-line alias. Each pass closed
# the shapes it had been shown and the next review found the adjacent one.
# The suspect list above is the fourth generation of that same enumeration and
# it is still an enumeration; widening it again would only move the treadmill.
#
# So the load-bearing check is not up there. It is here, and it is a
# MEASUREMENT rather than a prediction: do not ask what could have changed the
# binding, EXERCISE the binding and observe what it does.
#
#   * `run false` must set FAILED to 1, and `run true` must not. That is the
#     entire contract #0258 is about, stated as behaviour.
#   * A pipeline through the funnel whose FIRST stage exits non-zero must set
#     FAILED to 1, and one that succeeds must not. That is #0140's property
#     and #0262's, stated as behaviour.
#
# This does not care HOW the name got its current meaning. A definition in any
# spelling bash accepts, a multi-line definition, one built by eval, one read
# in from another file by either spelling of source, an unset, a PATH or hash
# shadow: all of them change what the two probes observe, and every one of the
# shapes in this issue's five-round history is caught by the same four lines.
#
# THE ONE THING BEHAVIOUR ALONE CANNOT SEE is an alias, and the reason is
# worth writing down because it is not obvious: alias expansion happens when a
# command is PARSED, not when it runs, and a function body is parsed when its
# definition is read. Measured under bash 3.2.57: `alias run=true` set after
# accounting_probe's own definition does not affect the `run` calls inside it,
# while it does affect the dispatch case below, which bash parses later. So
# the probe would call the real helper and the real steps would call the
# alias. The alias table is therefore queried directly instead -- also a live
# measurement, and a complete one for that class.
#
# WHY TWICE. The probe is exact only about the moment it runs. Called once
# before the dispatch, it proves the helpers are intact when the work starts
# and fails before any database is created or any test runs; called again
# immediately before the verdict, it proves they were still intact when the
# verdict was computed, which closes anything introduced by, or after, the
# dispatch. Between those two points the run is covered by the pair.
#
# WHAT REMAINS OPEN, named rather than glossed:
#   * A decoy scoped to exactly the dispatch and undone before the second
#     probe -- e.g. wrapping the case below in a construct that redefines a
#     helper only inside it. That is an edit to the dispatch itself, not a
#     line added beside it, and is the same cost as deleting this block.
#   * The verdict's own inputs. Anything that assigns FAILED directly, or
#     edits the verdict, is outside what a probe of the helpers can see; so is
#     a line inserted between the second probe and the verdict.
#   * A helper that terminates the script is not silent, but it is not a
#     verdict either -- that is what EXIT_ACCOUNTED and the EXIT trap below
#     turn into a loud NO VERDICT report.
# None of these is reachable by reformatting, which is the property the five
# earlier bypasses all had and is what made them worth closing.
accounting_probe() {
  local when="$1" saved="$FAILED" why=""
  if alias run >/dev/null 2>&1 || alias runpipe >/dev/null 2>&1; then
    why="an alias named run or runpipe is defined; alias expansion precedes function lookup, so the steps that go through it would not be calling the helper this script defines"
  fi
  if [ -z "$why" ]; then
    FAILED=0; run true >/dev/null 2>&1
    [ "$FAILED" -eq 0 ] || why="the run helper recorded a failure for a command that SUCCEEDED"
  fi
  if [ -z "$why" ]; then
    FAILED=0; run false >/dev/null 2>&1
    [ "$FAILED" -eq 1 ] || why="the run helper did NOT record a failure for a command that exited non-zero -- with that accounting gone every failing step passes silently and this script reports success over a --- FAIL, which is the whole of #0258"
  fi
  if [ -z "$why" ]; then
    FAILED=0; runpipe "true | tail -1" >/dev/null 2>&1
    [ "$FAILED" -eq 0 ] || why="the runpipe funnel recorded a failure for a pipeline that succeeded"
  fi
  if [ -z "$why" ]; then
    FAILED=0; runpipe "false | tail -1" >/dev/null 2>&1
    [ "$FAILED" -eq 1 ] || why="the runpipe funnel did NOT record a failure for a pipeline whose FIRST stage exited non-zero -- the flag the funnel exists to carry is not in force and #0140 is back"
  fi
  FAILED="$saved"
  [ -z "$why" ] && return 0
  EXIT_ACCOUNTED=1
  printf '\033[31mLIVE-ACCOUNTING REGRESSION (#0258/#0262), measured %s: %s. This check exercises whatever the two helpers are bound to at this moment, so unlike the scans above it depends on no pattern recognising how they got that way. Something in scripts/check.sh, or in a file it reads in, has replaced, shadowed or removed them.\033[0m\n' "$when" "$why" >&2
  exit 2
}
# END LIVEBIND-0258

step "self-check: live run/runpipe behaviour (#0258/#0262)"
accounting_probe "before the dispatch"

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
# #0258 (6th pass): also the NO VERDICT backstop. A helper shadowed by a decoy
# that simply terminates the script would otherwise end the run with status 0
# and no verdict line at all -- which a caller reading only the exit code reads
# as success. Every exit this script makes on purpose sets EXIT_ACCOUNTED first;
# any other one lands here and is reported.
cleanup() { [ -n "$OWN_DB" ] && scripts/testdb.sh drop "$OWN_DB" >/dev/null 2>&1; [ "${EXIT_ACCOUNTED:-0}" -eq 1 ] || { printf '\033[31mNO VERDICT (#0258): scripts/check.sh exited without reporting VERIFICATION PASSED or VERIFICATION FAILED. Treat this as a failure: either the run was interrupted, or something ended it early -- a helper shadowed by a decoy that terminates instead of reporting would look exactly like this.\033[0m\n' >&2; exit 2; }; }
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

# Re-measured HERE, immediately before the verdict, so that the pass/fail line
# below is backed by helpers proved intact at the moment it is computed -- not
# only at the moment the run started. See LIVEBIND-0258 above.
step "self-check: live run/runpipe behaviour, re-measured (#0258/#0262)"
accounting_probe "after the dispatch, immediately before the verdict"

EXIT_ACCOUNTED=1
if [ "$FAILED" -ne 0 ]; then printf '\n\033[31mVERIFICATION FAILED\033[0m\n'; exit 1; fi
printf '\n\033[32mVERIFICATION PASSED\033[0m\n'
