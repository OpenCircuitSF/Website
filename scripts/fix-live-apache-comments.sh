#!/usr/bin/env bash
#
# fix-live-apache-comments.sh — optional, cosmetic. Updates two comments in
# the live Apache vhost so they cite AGENTS.md instead of CLAUDE.md, matching
# deploy/apache/installed-001-www.opencircuitsf.com-le-ssl.conf. Comments only:
# no reload is needed, and configtest is run to prove the file still parses.
set -euo pipefail
HOST="${DEPLOY_HOST:-photon}"
CONF=/etc/httpd/conf.d/001-www.opencircuitsf.com-le-ssl.conf

# shellcheck disable=SC2029  # $CONF is meant to expand locally
ssh "$HOST" "sudo cp -p $CONF /root/001-vhost.conf.bak-\$(date +%Y%m%d%H%M%S) && \
  sudo sed -i 's/(CLAUDE\.md §7,/(AGENTS.md §7,/; s/# CLAUDE\.md §8 records/# AGENTS.md §8 records/' $CONF && \
  sudo apachectl configtest && sudo grep -c 'CLAUDE\.md' $CONF || true"
echo "Done (the number above is remaining CLAUDE.md mentions; 0 is expected)."
