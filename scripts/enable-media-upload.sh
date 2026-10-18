#!/usr/bin/env bash
#
# enable-media-upload.sh — turn on the admin image upload on the production
# host, once (#0465, #0433). Run from the workstation; it works over ssh.
#
# What it does on the host, in order (docs/deployment.md, "Workshop media
# upload enablement"):
#   1. Records the SHA-256 of every file already in /var/www/media.
#   2. chgrp opencircuit + chmod 2775 on /var/www/media, so the service can
#      write it (apache keeps reading through the unchanged "other" bits).
#   3. Installs the deployed release's opencircuit.service, which carries
#      ReadWritePaths=/var/www/media (ProtectSystem=strict otherwise makes the
#      whole filesystem read-only to the service). The port drop-in in
#      opencircuit.service.d/ is untouched.
#   4. Adds MEDIA_DIR=/var/www/media to /etc/opencircuit/config.env if absent,
#      after copying the file to config.env.bak-<timestamp>.
#   5. daemon-reload and restart (a few seconds of downtime), then checks
#      /health and that every existing image's hash is unchanged.
#
# Safe to re-run: each step checks before it changes anything.
#
# Usage: scripts/enable-media-upload.sh
set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:-photon}"
PUBLIC_URL="${PUBLIC_URL:-https://www.opencircuitsf.com}"
RELEASES_ROOT="${RELEASES_ROOT:-/opt/opencircuit/releases}"

echo "==> Enabling image upload on $DEPLOY_HOST"
# Quoted heredoc: everything below expands on the host, not here.
ssh "$DEPLOY_HOST" "RELEASES_ROOT='$RELEASES_ROOT' bash -s" <<'REMOTE'
set -euo pipefail
MEDIA=/var/www/media
CONF=/etc/opencircuit/config.env
UNIT=/etc/systemd/system/opencircuit.service

before="$(cd "$MEDIA" && sudo find . -type f -exec sha256sum {} + | sort -k2)"
echo "    existing files: $(printf '%s\n' "$before" | grep -c . || true)"

sudo chgrp opencircuit "$MEDIA"
sudo chmod 2775 "$MEDIA"
echo "    ✓ $(stat -c '%U:%G %a' "$MEDIA") $MEDIA"

src="$(ls -td "$RELEASES_ROOT"/v*/opencircuit.service 2>/dev/null | head -1 || true)"
[ -n "$src" ] || { echo "no deployed opencircuit.service under $RELEASES_ROOT"; exit 1; }
grep -q '^ReadWritePaths=/var/www/media$' "$src" || { echo "$src lacks ReadWritePaths=/var/www/media; deploy a current build first"; exit 1; }
if ! sudo cmp -s "$src" "$UNIT"; then
  sudo cp "$UNIT" "$UNIT.bak-$(date +%Y%m%d%H%M%S)"
  sudo install -m 0644 "$src" "$UNIT"
  echo "    ✓ installed $src"
else
  echo "    ✓ unit already current"
fi

if ! sudo grep -q '^MEDIA_DIR=' "$CONF"; then
  sudo cp -p "$CONF" "$CONF.bak-$(date +%Y%m%d%H%M%S)"
  printf '\n# Admin image upload target (#0433, #0465)\nMEDIA_DIR=/var/www/media\n' | sudo tee -a "$CONF" >/dev/null
  echo "    ✓ MEDIA_DIR added to $CONF"
else
  echo "    ✓ MEDIA_DIR already set in $CONF"
fi

sudo systemctl daemon-reload
sudo systemctl restart opencircuit
sudo systemctl is-active --quiet opencircuit
echo "    ✓ restarted; ReadWritePaths: $(systemctl show opencircuit -p ReadWritePaths --value)"

after="$(cd "$MEDIA" && sudo find . -type f -exec sha256sum {} + | sort -k2)"
[ "$before" = "$after" ] || { echo "existing image hashes changed!"; diff <(echo "$before") <(echo "$after") || true; exit 1; }
echo "    ✓ existing images unchanged by hash"
sudo -u opencircuit test -w "$MEDIA" && echo "    ✓ the service user can write $MEDIA"
REMOTE

sleep 2
curl -fsS --max-time 15 "$PUBLIC_URL/health" && echo
echo "✅ Image upload enabled. Try the Upload button in a workshop's cover image field."
