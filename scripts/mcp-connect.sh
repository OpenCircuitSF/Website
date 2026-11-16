#!/usr/bin/env bash
#
# mcp-connect.sh — connect Claude Code to the site's MCP server.
#
#   scripts/mcp-connect.sh
#
# First create a key in the admin console: Settings → API tokens → preset
# "Agent". Copy it, run this script, and paste it when asked (input is
# hidden). Re-run any time to replace the key.
set -euo pipefail

URL="${OCSF_MCP_URL:-https://www.opencircuitsf.com/mcp}"
NAME=ocsf

command -v claude >/dev/null || { echo "claude CLI not found on PATH" >&2; exit 1; }

KEY="${OCSF_API_KEY:-}"
if [ -z "$KEY" ]; then
  printf 'Paste the API key (hidden): '
  IFS= read -rs KEY; echo
fi
KEY="$(printf '%s' "$KEY" | tr -d '[:space:]')"
[ -n "$KEY" ] || { echo "no key entered" >&2; exit 1; }

# Check the key works before saving it.
STATUS="$(curl -s -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $KEY" "${URL%/mcp}/api/v1/me")"
if [ "$STATUS" != "200" ]; then
  echo "The site rejected that key (HTTP $STATUS). Check it was copied whole and has not expired." >&2
  exit 1
fi
echo "Key accepted."

claude mcp remove "$NAME" -s user >/dev/null 2>&1 || true
claude mcp add --scope user --transport http "$NAME" "$URL" \
  --header "Authorization: Bearer $KEY"

echo
echo "Done. Start a new Claude Code session and try:"
echo '  "What events are we missing next month?"'
