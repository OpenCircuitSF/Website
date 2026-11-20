#!/usr/bin/env bash
#
# mcp_connect_guard_test.sh — guard test for scripts/mcp-connect.sh's URL
# check (issue #0737).
#
# The admin MCP server moved to /admin/mcp and /mcp became the public,
# read-only server. mcp-connect.sh must refuse an OCSF_MCP_URL that does not
# end in /admin/mcp (trailing slash tolerated) before contacting anything or
# saving a key, and must save a good URL without its trailing slash.
#
# Stub `claude` and `curl` on PATH record their calls; no network, no port,
# no database. MCP_CONNECT_SH overrides the file under test (mutation proof
# against a copy). Exit: 0 pass, 1 regression, 2 harness broken.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
SUBJECT="${MCP_CONNECT_SH:-$REPO/scripts/mcp-connect.sh}"
[ -f "$SUBJECT" ] || { echo "FATAL: $SUBJECT not found" >&2; exit 2; }

FAILURES=0
fail() { FAILURES=$((FAILURES + 1)); printf 'FAIL: %s\n' "$1" >&2; }
pass() { printf 'PASS: %s\n' "$1"; }

T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
for stub in claude curl; do
  {
    echo '#!/usr/bin/env bash'
    echo "echo \"$stub \$*\" >> \"$T/calls\""
    [ "$stub" = curl ] && echo 'echo 200'
    echo 'exit 0'
  } > "$T/$stub"
  chmod +x "$T/$stub"
done

if bash -n "$SUBJECT"; then pass "bash -n $SUBJECT"; else fail "bash -n $SUBJECT"; fi

# run URL -> sets RC and ERR; clears the call log first.
run() {
  rm -f "$T/calls"
  ERR="$(env PATH="$T:$PATH" OCSF_API_KEY=dummy OCSF_SKIP_KEY_CHECK=1 OCSF_MCP_URL="$1" \
    bash "$SUBJECT" </dev/null 2>&1 >/dev/null)"
  RC=$?
}

refused() {
  run "$1"
  if [ "$RC" -ne 0 ] && printf '%s' "$ERR" | grep -q 'moved to /admin/mcp' && [ ! -s "$T/calls" ]; then
    pass "refuses $1 (exit $RC, nothing contacted)"
  else
    fail "$1: exit $RC, stderr '$ERR', calls: $(cat "$T/calls" 2>/dev/null)"
  fi
}

accepted() { # $1 = URL given, $2 = URL expected in the saved entry
  run "$1"
  if [ "$RC" -ne 0 ] && [ ! -s "$T/calls" ] && printf '%s' "$ERR" | grep -q 'claude CLI not found'; then
    echo "FATAL: stub claude not found on PATH — harness broken" >&2; exit 2
  fi
  if [ "$RC" -eq 0 ] && grep -E "^claude mcp add .* $2( |$)" "$T/calls" >/dev/null; then
    pass "accepts $1 and saves $2"
  else
    fail "$1: exit $RC, stderr '$ERR', calls: $(cat "$T/calls" 2>/dev/null)"
  fi
}

refused  "https://www.opencircuitsf.com/mcp"
refused  "https://example.test/mcp/"
accepted "https://example.test/admin/mcp"  "https://example.test/admin/mcp"
accepted "https://example.test/admin/mcp/" "https://example.test/admin/mcp"

if [ "$FAILURES" -gt 0 ]; then echo "$FAILURES failure(s)" >&2; exit 1; fi
echo "all passed"
