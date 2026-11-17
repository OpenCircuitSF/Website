#!/usr/bin/env bash
#
# mcp-connect.sh — connect Claude Code to the site's MCP server.
#
#   scripts/mcp-connect.sh                                  # user scope
#   scripts/mcp-connect.sh --scope local --dir <path>       # one directory
#
# First create a key in the admin console: Settings → API tokens → preset
# "Agent (every scope)" or "Read-only". Copy it, run this script, and paste
# it when asked (input is hidden). Re-run any time to replace the key.
#
# Environment:
#   OCSF_MCP_URL          server URL (default https://www.opencircuitsf.com/mcp)
#   OCSF_API_KEY          the key itself; skips the hidden prompt
#   OCSF_SKIP_KEY_CHECK=1 skip the live whoami check before saving the key
#
# User scope (~/.claude.json) is inherited by every Claude Code or Agent SDK
# process this OS user runs. A local-scope entry for an agent's working
# directory overrides it there, so that agent can have its own key
# (docs/mcp.md §7). Project scope is refused: it writes the key into
# .mcp.json, which may be committed.
#
# Bash 3.2 compatible (AGENTS.md §8).
set -euo pipefail

URL="${OCSF_MCP_URL:-https://www.opencircuitsf.com/mcp}"
NAME=ocsf
SCOPE=user
DIR=

usage() {
  echo "usage: $0 [--scope user|local] [--dir <path>]" >&2
  exit 2
}

while [ $# -gt 0 ]; do
  case "$1" in
    --scope) [ $# -ge 2 ] || usage; SCOPE="$2"; shift 2 ;;
    --scope=*) SCOPE="${1#--scope=}"; shift ;;
    --dir) [ $# -ge 2 ] || usage; DIR="$2"; shift 2 ;;
    --dir=*) DIR="${1#--dir=}"; shift ;;
    -h|--help) usage ;;
    *) echo "unknown argument: $1" >&2; usage ;;
  esac
done

case "$SCOPE" in
  user)
    [ -z "$DIR" ] || { echo "--dir applies only to --scope local" >&2; exit 2; } ;;
  local)
    [ -n "$DIR" ] || { echo "--scope local needs --dir <path>" >&2; exit 2; }
    [ -d "$DIR" ] || { echo "no such directory: $DIR" >&2; exit 2; }
    DIR="$(cd "$DIR" && pwd)" ;;
  project)
    echo "refusing --scope project: it writes the API key into .mcp.json, which may be committed. Use user or local." >&2
    exit 2 ;;
  *) echo "unknown scope: $SCOPE (use user or local)" >&2; exit 2 ;;
esac

command -v claude >/dev/null || { echo "claude CLI not found on PATH" >&2; exit 1; }

KEY="${OCSF_API_KEY:-}"
if [ -z "$KEY" ]; then
  printf 'Paste the API key (hidden): '
  IFS= read -rs KEY; echo
fi
KEY="$(printf '%s' "$KEY" | tr -d '[:space:]')"
[ -n "$KEY" ] || { echo "no key entered" >&2; exit 1; }

# Check the key works before saving it. OCSF_SKIP_KEY_CHECK=1 skips this, for
# testing against a dummy key without contacting the site.
if [ "${OCSF_SKIP_KEY_CHECK:-}" != "1" ]; then
  STATUS="$(curl -s -o /dev/null -w '%{http_code}' \
    -H "Authorization: Bearer $KEY" "${URL%/mcp}/api/v1/me")"
  if [ "$STATUS" != "200" ]; then
    echo "The site rejected that key (HTTP $STATUS). Check it was copied whole and has not expired." >&2
    exit 1
  fi
  echo "Key accepted."
fi

# claude mcp add/remove at local scope act on the current directory, so run
# them from DIR in a subshell.
(
  if [ "$SCOPE" = local ]; then cd "$DIR"; fi
  claude mcp remove "$NAME" -s "$SCOPE" >/dev/null 2>&1 || true
  claude mcp add --scope "$SCOPE" --transport http "$NAME" "$URL" \
    --header "Authorization: Bearer $KEY"
)

echo
if [ "$SCOPE" = local ]; then
  echo "Done: a local-scope entry for $DIR. Check it there with: claude mcp get $NAME"
else
  echo "Done. Start a new Claude Code session and try:"
  echo '  "What events are we missing next month?"'
fi
