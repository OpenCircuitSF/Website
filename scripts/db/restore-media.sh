#!/usr/bin/env bash
#
# restore-media.sh — restore a backup produced by backup-media.sh (#0434) into
# a target directory, with the same "prove it end to end" bar as restore.sh:
# files come back with their names, and ownership/permissions are put back to
# what Apache and the app expect, not left however extraction happened to
# leave them.
#
#     # restore the newest media backup into a scratch directory (a "restore
#     # drill" — always do this before ever restoring over the live path)
#     bash scripts/db/restore-media.sh /var/backups/postgres/media/media-latest.tar.gz /tmp/media-restore-test
#
#     # restore over the live path on the server (DANGEROUS — see below;
#     # needs root/sudo to chown to RESTORE_MEDIA_OWNER)
#     sudo bash scripts/db/restore-media.sh /var/backups/postgres/media/media-latest.tar.gz /var/www/media
#
# Arguments:
#   $1  path to the archive (.tar.gz, as written by backup-media.sh)
#   $2  target directory (created if missing)
#
# Configuration:
#   RESTORE_MEDIA_OWNER     user:group to chown the restored tree to.
#                             Default: ec2-user:ec2-user — docs/media.md and
#                             #0434 acceptance #4: /var/www/media is
#                             ec2-user-owned in production, verified directly
#                             on the box (read-only `stat`, 2026-09-05).
#                             Set empty to skip chown entirely (e.g. local
#                             testing as a non-root user, where chown to an
#                             arbitrary owner would just fail).
#   RESTORE_MEDIA_DIR_MODE  Mode for restored directories. Default: 0755
#   RESTORE_MEDIA_FILE_MODE Mode for restored files.       Default: 0644
#                             Both defaults match what Apache's `<Directory
#                             "/var/www/media">` carve-out (docs/media.md)
#                             needs to serve the files, and what the box's
#                             files actually carry today (verified 2026-09-05).
#
# A restore that lands root-owned or over-permissive files is exactly
# docs/media.md's documented 403 symptom (wrong file mode) — this script sets
# ownership and mode explicitly rather than trusting whatever `tar -x` left,
# since extracting as a non-root user cannot set arbitrary ownership at all
# (tar silently keeps the extracting user's own uid/gid in that case).
#
# Safety: this never deletes anything at TARGET first. Restoring over a
# populated directory overlays the archive's files on top of what's there;
# for a clean-slate restore, empty or recreate TARGET yourself first.

set -euo pipefail

ARCHIVE="${1:?usage: restore-media.sh <archive.tar.gz> <target-dir>}"
TARGET="${2:?usage: restore-media.sh <archive.tar.gz> <target-dir>}"

RESTORE_MEDIA_OWNER="${RESTORE_MEDIA_OWNER-ec2-user:ec2-user}"
RESTORE_MEDIA_DIR_MODE="${RESTORE_MEDIA_DIR_MODE:-0755}"
RESTORE_MEDIA_FILE_MODE="${RESTORE_MEDIA_FILE_MODE:-0644}"

[ -f "$ARCHIVE" ] || { echo "ERROR: archive not found: $ARCHIVE" >&2; exit 2; }

mkdir -p "$TARGET"

echo "Restoring '$ARCHIVE' → '$TARGET'"
tar -xzf "$ARCHIVE" -C "$TARGET"

restored_count="$(find "$TARGET" -type f | wc -l | tr -d ' ')"
echo "Extracted $restored_count file(s)."

if [ -n "$RESTORE_MEDIA_OWNER" ]; then
  if chown -R "$RESTORE_MEDIA_OWNER" "$TARGET" 2>/dev/null; then
    echo "Ownership set to '$RESTORE_MEDIA_OWNER'."
  else
    echo "WARNING: chown to '$RESTORE_MEDIA_OWNER' failed — likely not running as root/sudo." >&2
    echo "         Re-run as root (or via sudo) to fix ownership, or set RESTORE_MEDIA_OWNER=" >&2
    echo "         to skip this step deliberately (e.g. local drills as a non-root user)." >&2
  fi
else
  echo "RESTORE_MEDIA_OWNER is empty — skipping ownership change."
fi

find "$TARGET" -type d -exec chmod "$RESTORE_MEDIA_DIR_MODE" {} +
find "$TARGET" -type f -exec chmod "$RESTORE_MEDIA_FILE_MODE" {} +
echo "Permissions set: directories $RESTORE_MEDIA_DIR_MODE, files $RESTORE_MEDIA_FILE_MODE."

echo "Done. Verify with: ls -la $TARGET"
