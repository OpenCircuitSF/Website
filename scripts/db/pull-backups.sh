#!/usr/bin/env bash
#
# pull-backups.sh — pull the EC2 PostgreSQL backup tree to this machine (the Mac
# mini) over rsync-over-SSH. Run THIS on the Mac mini, not on the server.
#
# The pull model is deliberate: the server never holds credentials for the Mac
# mini, so an instance compromise cannot reach the offsite copy. Because every
# database lives under one common root on the server, a single rsync mirrors all
# of them at once.
#
#     # pull from this project's own EC2 host (BACKUP_SSH_HOST is required —
#     # see #0245; there is no default host, on purpose)
#     BACKUP_SSH_HOST=ec2-user@<this-project's-host> bash scripts/db/pull-backups.sh
#
# Schedule it a bit after the server's nightly dump window (e.g. server dumps at
# 07:30 UTC, the Mac pulls at 08:00 UTC). On macOS prefer a launchd agent; cron
# works too.
#
# ── Configuration (override via environment) ─────────────────────────────────
#   BACKUP_SSH_HOST    user@host of the EC2 box.   REQUIRED — no default (#0245);
#                         unset or empty is a hard error, this never guesses a host.
#   BACKUP_REMOTE_DIR  Remote backup root.         Default: /var/backups/postgres/
#   BACKUP_LOCAL_DIR   Local mirror destination.   Default: ~/Backups/postgres/
#   BACKUP_SSH_KEY     Path to a dedicated SSH key. Default: ssh default key
#
# Use a dedicated, restricted SSH key for this (ideally a read-only command or a
# user that can only read the backup tree).

set -euo pipefail

# #0245: this used to default BACKUP_SSH_HOST to "ec2-user@go.sstools.co" — a
# real ShortLinks production hostname, inherited when this script was ported
# wholesale from that project (#0001). Run unmodified with BACKUP_SSH_HOST
# unset, the offsite pull opened an SSH connection to another project's
# server instead of this one's. This is worse than #0236's same-shaped defect
# on the local-dump leg: that one failed loudly against a wrong database name
# on the same box, while this one reaches out over the network to
# infrastructure this project has no business touching, and if that host
# happened to accept the connection, backups would land somewhere nobody here
# is looking for them. There is no correct value to substitute (this
# project's own EC2 host is not yet provisioned — CLAUDE.md §10 item 6), so
# this now fails closed before making any network call, rather than guessing.
if [ -z "${BACKUP_SSH_HOST:-}" ]; then
  cat >&2 <<EOF
ERROR: BACKUP_SSH_HOST is not set, and this script no longer defaults to a
host to pull backups from.

A prior version defaulted to "ec2-user@go.sstools.co" — a different
project's (ShortLinks') production server, inherited when this script was
ported by #0001. Run unmodified, that silently opened an SSH connection to
infrastructure this project does not own.

Fix: set BACKUP_SSH_HOST to this project's own EC2 host, e.g.:

    BACKUP_SSH_HOST=ec2-user@<this-project's-host> bash scripts/db/pull-backups.sh

(CLAUDE.md §10 item 6: the real server layout, including its hostname, is not
yet documented — capture it there once the box exists.)
EOF
  exit 2
fi
SSH_HOST="$BACKUP_SSH_HOST"
REMOTE_DIR="${BACKUP_REMOTE_DIR:-/var/backups/postgres/}"
LOCAL_DIR="${BACKUP_LOCAL_DIR:-$HOME/Backups/postgres/}"
SSH_KEY="${BACKUP_SSH_KEY:-}"

# Ensure trailing slashes so rsync mirrors the *contents* of the root.
REMOTE_DIR="${REMOTE_DIR%/}/"
LOCAL_DIR="${LOCAL_DIR%/}/"

mkdir -p "$LOCAL_DIR"

ssh_cmd="ssh"
[ -n "$SSH_KEY" ] && ssh_cmd="ssh -i $SSH_KEY"

echo "Pulling $SSH_HOST:$REMOTE_DIR → $LOCAL_DIR"

# --delete-after mirrors server-side pruning (old dumps removed locally too, but
# only after a successful transfer). --partial-dir keeps interrupted transfers
# out of the live tree.
rsync -avz --delete-after --partial --partial-dir=.rsync-partial \
  -e "$ssh_cmd" \
  "$SSH_HOST:$REMOTE_DIR" "$LOCAL_DIR"

echo "Done. Local mirror: $LOCAL_DIR"
