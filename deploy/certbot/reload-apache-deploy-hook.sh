#!/usr/bin/env bash
#
# Certbot deploy hook: reload Apache so mod_ssl picks up a freshly renewed
# certificate (#0447).
#
# Certbot only runs scripts placed in
# /etc/letsencrypt/renewal-hooks/deploy/ after a certificate has actually
# been renewed -- that directory is scanned unconditionally by `certbot
# renew`, independent of how renew itself was invoked (the bare
# `certbot renew --quiet` in certbot-renew.service, a manual `certbot
# renew`, or `certbot renew --dry-run --run-deploy-hooks`). So this script
# never needs its own "did anything change" check: if it runs at all, a
# renewal just succeeded, and reload-not-restart plus that built-in
# no-op-on-no-renewal behavior are exactly what issue #0447 asked for.
#
# This is deliberately a deploy-hooks-directory script rather than
# DEPLOY_HOOK in /etc/sysconfig/certbot. Read-only checks on the box
# (2026-09-06) show certbot-renew.service's ExecStart is the bare
# `certbot renew --quiet` -- it does not source /etc/sysconfig/certbot or
# splice its PRE_HOOK/POST_HOOK/DEPLOY_HOOK values onto the command line, so
# setting DEPLOY_HOOK there would sit inert for the unattended timer path
# unless the systemd unit were also edited to consume it. A script dropped
# into renewal-hooks/deploy/ needs no change to the unit, the sysconfig
# file, or the renewal conf -- certbot finds and runs it on its own for
# every renewal on the box (this host renews certs for several unrelated
# domains under the same certbot install; harmlessly reloading Apache after
# any of them is fine, since they all terminate on the same httpd).
#
# Install (root, once per box):
#   sudo install -m 0755 deploy/certbot/reload-apache-deploy-hook.sh \
#     /etc/letsencrypt/renewal-hooks/deploy/reload-apache.sh
#
# Certbot exports RENEWED_LINEAGE and RENEWED_DOMAINS to every deploy hook
# invocation (see `certbot --help renew`). Require RENEWED_LINEAGE so this
# script only ever acts when certbot itself invoked it, and fails loudly
# with a clear message if run by hand outside that context.

set -euo pipefail

if [ -z "${RENEWED_LINEAGE:-}" ]; then
  echo "reload-apache-deploy-hook: RENEWED_LINEAGE is unset -- refusing to run outside a certbot deploy hook" >&2
  exit 1
fi

echo "reload-apache-deploy-hook: certbot renewed ${RENEWED_DOMAINS:-<unknown domains>} (lineage: ${RENEWED_LINEAGE})" >&2

# Syntax-check before touching the running server, matching the
# `sudo httpd -t` step docs/deployment.md already requires before every
# other Apache reload on this box.
if ! httpd -t; then
  echo "reload-apache-deploy-hook: 'httpd -t' failed -- NOT reloading; the previously loaded certificate stays in service until this is fixed" >&2
  exit 1
fi

systemctl reload httpd
echo "reload-apache-deploy-hook: reloaded httpd" >&2
