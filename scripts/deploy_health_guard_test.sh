#!/usr/bin/env bash
#
# deploy_health_guard_test.sh — guard test for scripts/deploy.sh's post-restart
# health retry and running-process identity check (issue #0712).
#
# #0712: deploy.sh made ONE curl to /health right after `systemctl restart`;
# Apache answers 503 until the service listens, so three successful deploys
# reported failure. The fix (HEALTH-0712 block) retries for a bounded window,
# and a second block (IDENTITY-0712) confirms the running process's
# /proc/<pid>/exe hashes the same as the installed and shipped binaries.
#
# This harness never runs deploy.sh as a whole and never reaches anything but
# 127.0.0.1. It extracts the file's OWN bytes between the marker pairs (plus
# die/ok/info), runs them against a python3 stub server bound to an ephemeral
# port (AGENTS.md §8b — never a fixed port), and feeds the pure identity
# comparison sha256sum-shaped fixtures. The remote IDENTITY_CHECK script only
# ever runs on the host; here it is `bash -n`'d.
#
# Non-vacuity (scenario A′): the stub that A passes against (two 503s, then
# 200) defeats HEALTH_ATTEMPTS=1 — the pre-fix single-attempt behaviour — so
# A passes BECAUSE of the retry. An env change, not a sed mutation (§8, #0304).
#
# Exit: 0 pass, 1 regression, 2 harness broken.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
REAL_SCRIPT="$REPO/scripts/deploy.sh"
[ -f "$REAL_SCRIPT" ] || { echo "FATAL: $REAL_SCRIPT not found" >&2; exit 2; }
command -v python3 >/dev/null || { echo "FATAL: python3 not found" >&2; exit 2; }
command -v curl >/dev/null || { echo "FATAL: curl not found" >&2; exit 2; }

mkdir -p "$REPO/.scratch"
WORKDIR="$(mktemp -d "$REPO/.scratch/deploy_health_guard.XXXXXX")"
FAILURES=0
STUB_PID=""

stop_stub() {
  if [ -n "$STUB_PID" ]; then
    kill "$STUB_PID" 2>/dev/null
    wait "$STUB_PID" 2>/dev/null
    STUB_PID=""
  fi
}
# shellcheck disable=SC2329  # invoked via the EXIT trap
cleanup() { stop_stub; rm -rf "$WORKDIR"; }
trap cleanup EXIT

fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

# ---- exactly-once anchors (GUARD-0208 rule) ---------------------------------
for anchor in '^# HEALTH-0712-BEGIN' '^# HEALTH-0712-END' \
              '^# IDENTITY-0712-BEGIN' '^# IDENTITY-0712-END' \
              '^die(){' '^ok(){' '^info(){'; do
  n="$(grep -c "$anchor" "$REAL_SCRIPT")"
  [ "$n" = "1" ] || { echo "FATAL: anchor '$anchor' appears $n times in deploy.sh (want exactly 1)" >&2; exit 2; }
done
pass "all seven extraction anchors appear exactly once"

# ---- call sites (#0712 review) ----------------------------------------------
# The scenarios below prove the extracted functions; they cannot see whether
# deploy.sh still CALLS them. Reverting the call site to the old single curl,
# or dropping the identity call, left every scenario green. Pin each call line
# (whole-line, fixed-string) to exactly one occurrence. This oracle lives in
# this file, so an edit to deploy.sh cannot also satisfy it (AGENTS.md §8).
# shellcheck disable=SC2016  # literal deploy.sh lines, not expansions
for callsite in \
  'if ! wait_healthy "$PUBLIC_URL/health"; then' \
  'ID_OUT="$(printf '"'%s'"' "$IDENTITY_CHECK" | ssh "$DEPLOY_HOST" bash -s -- "$SERVICE" "$BIN_PATH" "$DEST/opencircuit" 2>&1 || true)"' \
  'confirm_live_identity "$ID_OUT"'; do
  n="$(grep -cxF -- "$callsite" "$REAL_SCRIPT")"
  if [ "$n" = "1" ]; then pass "call site present exactly once: $callsite"
  else fail "call site must appear exactly once in deploy.sh (found $n): $callsite"; fi
done

# ---- extraction -------------------------------------------------------------
extract_block() { awk -v b="# $1-BEGIN" -v e="# $1-END" '$0==b{f=1; next} $0==e{f=0} f' "$REAL_SCRIPT"; }
HELPERS="$(grep -m1 '^die(){' "$REAL_SCRIPT"; grep -m1 '^ok(){' "$REAL_SCRIPT"; grep -m1 '^info(){' "$REAL_SCRIPT")"
HEALTH_BLOCK="$(extract_block HEALTH-0712)"
IDENTITY_BLOCK="$(extract_block IDENTITY-0712)"
for v in HELPERS HEALTH_BLOCK IDENTITY_BLOCK; do
  [ -n "${!v}" ] || { echo "FATAL: extraction of $v produced nothing" >&2; exit 2; }
done
case "$HEALTH_BLOCK" in *'wait_healthy()'*) ;; *) echo "FATAL: HEALTH block lacks wait_healthy()" >&2; exit 2 ;; esac
case "$IDENTITY_BLOCK" in *'confirm_live_identity()'*) ;; *) echo "FATAL: IDENTITY block lacks confirm_live_identity()" >&2; exit 2 ;; esac

HEALTH_RUNNER="$WORKDIR/health_runner.sh"
{
  printf '#!/usr/bin/env bash\nset -euo pipefail\n'
  printf '%s\n' "$HELPERS" "$HEALTH_BLOCK"
  # shellcheck disable=SC2016
  printf '%s\n' 'wait_healthy "$URL" || die "never healthy: HTTP $LAST_HEALTH_CODE: $LAST_HEALTH_BODY"'
} > "$HEALTH_RUNNER"
IDENTITY_RUNNER="$WORKDIR/identity_runner.sh"
{
  printf '#!/usr/bin/env bash\nset -euo pipefail\n'
  printf '%s\n' "$HELPERS" "$IDENTITY_BLOCK"
  # shellcheck disable=SC2016
  printf '%s\n' 'confirm_live_identity "$FIXTURE"'
} > "$IDENTITY_RUNNER"
bash -n "$HEALTH_RUNNER" || { echo "FATAL: extracted health runner does not parse" >&2; exit 2; }
bash -n "$IDENTITY_RUNNER" || { echo "FATAL: extracted identity runner does not parse" >&2; exit 2; }

# ---- stub server ------------------------------------------------------------
STUB="$WORKDIR/stub.py"
cat > "$STUB" <<'PY'
import sys, http.server
fail_n = int(sys.argv[1]); mode = sys.argv[2]; portfile = sys.argv[3]
state = {"n": 0}
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        if self.path != "/health":
            self.send_response(404); self.end_headers(); return
        state["n"] += 1
        if mode == "degraded":
            code, body = 200, b'{"status":"degraded","db":"error"}'
        elif fail_n < 0 or state["n"] <= fail_n:
            code, body = 503, b'{"status":"starting"}'
        else:
            code, body = 200, b'{"status":"ok","db":"ok"}'
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers(); self.wfile.write(body)
srv = http.server.HTTPServer(("127.0.0.1", 0), H)
with open(portfile + ".tmp", "w") as f: f.write(str(srv.server_address[1]))
import os; os.rename(portfile + ".tmp", portfile)
srv.serve_forever()
PY

PORT=""
# start_stub FAIL_N MODE — sets STUB_PID and PORT. The ownership probe uses a
# path other than /health so it never consumes one of the stub's counted
# responses; a 404 from our own handler shape is enough to know it is ours.
start_stub() {
  local pf="$WORKDIR/port.$RANDOM" i=0
  python3 -I "$STUB" "$1" "$2" "$pf" &
  STUB_PID=$!
  while [ ! -s "$pf" ] && [ "$i" -lt 50 ]; do sleep 0.1; i=$((i + 1)); done
  [ -s "$pf" ] || { echo "FATAL: stub server never reported its port" >&2; exit 2; }
  PORT="$(cat "$pf")"
  kill -0 "$STUB_PID" 2>/dev/null || { echo "FATAL: stub server (pid $STUB_PID) died" >&2; exit 2; }
  local code
  code="$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 "http://127.0.0.1:$PORT/__probe")"
  [ "$code" = "404" ] || { echo "FATAL: port $PORT did not answer like our stub (HTTP $code)" >&2; exit 2; }
}

OUT=""; RC=0; ELAPSED=0
run_health() { # ATTEMPTS URL
  local t0=$SECONDS
  OUT="$(URL="$2" HEALTH_ATTEMPTS="$1" HEALTH_INTERVAL=0.1 HEALTH_MAX_TIME=2 bash "$HEALTH_RUNNER" 2>&1)"; RC=$?
  ELAPSED=$((SECONDS - t0))
}
has() { case "$OUT" in *"$1"*) return 0 ;; esac; return 1; }

# A: two 503s then 200, five attempts -> healthy on the third.
start_stub 2 normal
run_health 5 "http://127.0.0.1:$PORT/health"
stop_stub
if [ "$RC" -eq 0 ] && has 'attempt 1/5: HTTP 503' && has 'attempt 3/5: HTTP 200' \
   && has 'healthy after 3/5' && ! has 'attempt 4/5'; then
  pass "A: retries through two 503s and succeeds on attempt 3/5"
else fail "A: expected success on attempt 3/5 (rc=$RC): $OUT"; fi

# A′: the same stub defeats a single attempt (the pre-fix behaviour).
start_stub 2 normal
run_health 1 "http://127.0.0.1:$PORT/health"
stop_stub
if [ "$RC" -ne 0 ] && has 'attempt 1/1: HTTP 503'; then
  pass "A′: a single attempt fails against the same stub — A passes because of the retry"
else fail "A′: expected failure with one attempt (rc=$RC): $OUT"; fi

# B: never healthy -> fails, bounded, shows the last response.
start_stub -1 normal
run_health 3 "http://127.0.0.1:$PORT/health"
stop_stub
if [ "$RC" -ne 0 ] && has 'attempt 3/3' && ! has 'attempt 4/3' && has '503' && has 'starting' \
   && [ "$ELAPSED" -lt 10 ]; then
  pass "B: never-healthy fails after exactly 3 attempts in ${ELAPSED}s, last response shown"
else fail "B: expected bounded failure with last response (rc=$RC, ${ELAPSED}s): $OUT"; fi

# C: 200 with a degraded body is not healthy.
start_stub 0 degraded
run_health 2 "http://127.0.0.1:$PORT/health"
stop_stub
if [ "$RC" -ne 0 ] && has 'attempt 2/2: HTTP 200' && has 'degraded'; then
  pass "C: HTTP 200 with a degraded body is not treated as healthy"
else fail "C: expected failure on degraded body (rc=$RC): $OUT"; fi

# D: nothing listening (port from a stub we started and killed).
start_stub 0 normal
DEAD_PORT="$PORT"
stop_stub
run_health 2 "http://127.0.0.1:$DEAD_PORT/health"
if [ "$RC" -ne 0 ] && has 'HTTP 000' && [ "$ELAPSED" -lt 10 ]; then
  pass "D: connection refused fails as HTTP 000, bounded (${ELAPSED}s)"
else fail "D: expected bounded HTTP 000 failure (rc=$RC, ${ELAPSED}s): $OUT"; fi

# E: a vacuous loop is refused.
for bad in 0 x; do  # an empty value takes the default 10, by design
  run_health "$bad" "http://127.0.0.1:1/health"
  if [ "$RC" -ne 0 ] && has 'HEALTH_ATTEMPTS'; then
    pass "E: HEALTH_ATTEMPTS='$bad' is refused"
  else fail "E: HEALTH_ATTEMPTS='$bad' should be refused (rc=$RC): $OUT"; fi
done

# ---- identity ---------------------------------------------------------------
H1="$(printf 'a%.0s' $(seq 1 64))"
H2="$(printf 'b%.0s' $(seq 1 64))"
run_identity() { OUT="$(FIXTURE="$1" bash "$IDENTITY_RUNNER" 2>&1)"; RC=$?; }

run_identity "$(printf 'pid 123\n%s  /proc/123/exe\n%s  /usr/local/bin/opencircuit\n%s  /srv/rel/opencircuit\n' "$H1" "$H1" "$H1")"
if [ "$RC" -eq 0 ] && has 'pid 123' && has "${H1:0:12}"; then
  pass "identity: three equal hashes pass, naming pid and hash prefix"
else fail "identity: equal hashes should pass (rc=$RC): $OUT"; fi

run_identity "$(printf 'pid 123\n%s  /proc/123/exe\n%s  /usr/local/bin/opencircuit\n%s  /srv/rel/opencircuit\n' "$H2" "$H1" "$H1")"
if [ "$RC" -ne 0 ] && has 'stale process'; then
  pass "identity: a differing running-process hash fails as a stale process"
else fail "identity: differing exe hash should fail naming a stale process (rc=$RC): $OUT"; fi

run_identity "$(printf 'pid 123\n%s  /proc/123/exe\n%s  /usr/local/bin/opencircuit\n%s  /srv/rel/opencircuit\n' "$H1" "$H1" "$H2")"
if [ "$RC" -ne 0 ] && has 'install did not take'; then
  pass "identity: installed != shipped fails"
else fail "identity: installed != shipped should fail (rc=$RC): $OUT"; fi

for fx in '' 'no MainPID for opencircuit'; do
  run_identity "$fx"
  if [ "$RC" -ne 0 ]; then pass "identity: output '${fx:-<empty>}' fails"
  else fail "identity: output '${fx:-<empty>}' should fail: $OUT"; fi
done

# The remote script only ever runs on the host — at least prove it parses.
REMOTE="$(bash -c "$IDENTITY_BLOCK"'
printf "%s" "$IDENTITY_CHECK"' 2>/dev/null)"
if [ -n "$REMOTE" ] && printf '%s' "$REMOTE" | bash -n; then
  pass "identity: the remote IDENTITY_CHECK script parses (bash -n)"
else fail "identity: IDENTITY_CHECK empty or does not parse"; fi

if [ "$FAILURES" -eq 0 ]; then echo "deploy_health_guard_test: all checks passed"; exit 0; fi
echo "deploy_health_guard_test: $FAILURES check(s) FAILED" >&2
exit 1
