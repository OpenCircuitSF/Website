#!/usr/bin/env bash
#
# backup-media.sh — nightly filesystem backup of the workshop cover images at
# /var/www/media (#0434), companion to backup.sh's database dumps. Shares
# backup.sh's BACKUP_ROOT so the existing offsite leg (pull-backups.sh, which
# rsyncs the whole root) picks these up with no changes of its own.
#
#     # back up the default source into the default root (production shape)
#     MEDIA_SOURCE_DIR=/var/www/media BACKUP_RUN_AS=postgres sudo -E bash scripts/db/backup-media.sh
#
#     # local dev / testing — no sudo, a scratch source and root
#     MEDIA_SOURCE_DIR=/tmp/src BACKUP_ROOT=/tmp/dest BACKUP_RUN_AS="" bash scripts/db/backup-media.sh
#
# Layout, mirroring backup.sh's own convention:
#
#     $BACKUP_ROOT/
#     └── media/
#         ├── media-20260905-073001Z.tar.gz
#         ├── media-latest.tar.gz -> media-20260905-073001Z.tar.gz
#         └── media-latest.manifest        # content manifest of the last dump written
#
# ── Why content-addressed, not "dump every night" (#0434 acceptance #3) ──────
# Workshop covers have no upload endpoint (docs/media.md) — the only way a file
# under MEDIA_SOURCE_DIR changes is a manual `scp` by an operator, which is
# rare. Images are also larger and compress worse than the SQL dumps
# backup.sh writes. A naive "tar it every night, prune after N days" policy
# would multiply an unchanging directory's size by the retention window for
# zero benefit — on a t4g.nano (418 MB RAM, CLAUDE.md §7) with limited disk,
# that is pure waste, not safety margin.
#
# So this script hashes the source tree's contents (relative path + sha256 of
# each file, sorted — reproducible regardless of mtimes, and portable across
# GNU coreutils and macOS/BSD, see sha256_of below) and compares that manifest
# to the one recorded alongside the last dump it wrote. Unchanged → skip
# writing a new archive (still a successful, zero-exit run) and prune on the
# existing schedule; changed, or no prior manifest → write a fresh archive and
# refresh the manifest. In steady state (no edits) this keeps exactly one
# archive on disk indefinitely, rather than RETENTION_DAYS copies of the same
# bytes. Measured against the two live covers (#0434): 152 KB total, so even
# the old naive policy would have cost under 2.2 MB at 14 nights — the dedup
# matters for headroom against future, larger, more frequently-updated covers,
# not against today's footprint.
#
# #0434 review: pruning excludes whatever media-latest.tar.gz currently
# resolves to, regardless of its age, so the skip path above can never age
# the last remaining archive out from under itself — "keeps exactly one
# archive on disk indefinitely" is therefore actually true, not just true
# until MEDIA_BACKUP_RETENTION_DAYS nights of unchanged content have passed.
# And if that archive is ever lost some other way (manual cleanup, a full
# disk, a partial rsync), the skip condition also checks for its existence,
# so a missing media-latest.tar.gz forces a fresh write instead of trusting a
# stale manifest forever.
#
# ── Configuration (all overridable via environment) ──────────────────────────
#   MEDIA_SOURCE_DIR              Directory to back up.      Default: /var/www/media
#   BACKUP_ROOT                   Common backup root — shared with backup.sh
#                                   on purpose (see header above).
#                                                              Default: /var/backups/postgres
#   MEDIA_BACKUP_RETENTION_DAYS   Prune archives older than N.
#                                                              Default: $BACKUP_RETENTION_DAYS, else 14
#   BACKUP_RUN_AS                 OS user to run tar as.      Default: postgres
#                                   Uses `sudo -u <user>` like backup.sh. Set
#                                   empty (or to the current user) to skip sudo.
#
# Exit status is non-zero on failure, so systemd's OnFailure= (wired the same
# way as backup.sh, see deploy/systemd/opencircuit-backup.service) fires the
# existing alert path for this leg too.
#
# ── Permissions this script needs (#0434 acceptance — say this explicitly) ───
# READING MEDIA_SOURCE_DIR: none beyond what already exists. Verified directly
# on the box (read-only `ls -la` / `stat`, 2026-09-05): /var/www is
# root:root 0755, /var/www/media is ec2-user:ec2-user 0755, and its files are
# ec2-user:ec2-user 0644 — all readable via the "other" bits Apache already
# relies on to serve them, so BACKUP_RUN_AS=postgres can traverse and read
# them with no new grant.
#
# WRITING $BACKUP_ROOT: the SAME open permission gap #0435 already documents
# for the database dumps — /var/backups/postgres is currently root:root 0700,
# so `postgres` cannot even `mkdir` under it. This script does not need a
# second grant; it needs #0435's fix (chown/chmod so BACKUP_RUN_AS can write
# under BACKUP_ROOT, or create BACKUP_ROOT owned appropriately before the
# timer first runs). Nothing here should be installed on the box ahead of
# that, and installing it at all needs the user's go-ahead (#0434 acceptance
# #6) — this script is prepared and verified locally, not deployed.

set -euo pipefail

MEDIA_SOURCE_DIR="${MEDIA_SOURCE_DIR:-/var/www/media}"
BACKUP_ROOT="${BACKUP_ROOT:-/var/backups/postgres}"
MEDIA_BACKUP_RETENTION_DAYS="${MEDIA_BACKUP_RETENTION_DAYS:-${BACKUP_RETENTION_DAYS:-14}}"
#0062/#0434: `-` (unset-only), not `:-` (unset-or-empty) — matching
# backup.sh's own BACKUP_RUN_AS handling, so BACKUP_RUN_AS="" actually skips
# sudo instead of silently falling back to "postgres".
BACKUP_RUN_AS="${BACKUP_RUN_AS-postgres}"

if [ ! -d "$MEDIA_SOURCE_DIR" ]; then
  echo "ERROR: MEDIA_SOURCE_DIR '$MEDIA_SOURCE_DIR' does not exist or is not a directory." >&2
  echo "This script does not silently skip a missing source — set MEDIA_SOURCE_DIR to a" >&2
  echo "real directory (a scratch one for local testing, /var/www/media in production)." >&2
  exit 2
fi

runner=()
if [ -n "$BACKUP_RUN_AS" ] && [ "$BACKUP_RUN_AS" != "$(id -un)" ]; then
  runner=(sudo -u "$BACKUP_RUN_AS")
fi

# Portable content hash: GNU coreutils' sha256sum on the production box
# (Amazon Linux), macOS's shasum -a 256 for local dev/testing — neither
# assumed present, checked once up front (#0434, per CLAUDE.md §8's zsh/bash
# and BSD-tool portability notes).
if command -v sha256sum >/dev/null 2>&1; then
  HASH_BIN="sha256sum"
elif command -v shasum >/dev/null 2>&1; then
  HASH_BIN="shasum -a 256"
else
  echo "ERROR: neither sha256sum nor shasum is available on PATH." >&2
  exit 2
fi

cd /tmp

dir="$BACKUP_ROOT/media"
mkdir -p "$dir"
chmod 0700 "$BACKUP_ROOT" 2>/dev/null || true
chmod 0700 "$dir" 2>/dev/null || true

timestamp="$(date -u +%Y%m%d-%H%M%SZ)"
base="media-$timestamp.tar.gz"
final="$dir/$base"
tmp="$final.tmp"
manifest_file="$dir/media-latest.manifest"
manifest_tmp="$manifest_file.tmp"

echo "============================================================"
echo " Media backup — $(date -u +'%Y-%m-%d %H:%M:%SZ')"
echo " source:     $MEDIA_SOURCE_DIR"
echo " root:       $dir"
echo " retention:  ${MEDIA_BACKUP_RETENTION_DAYS}d"
echo " run as:     ${BACKUP_RUN_AS:-$(id -un)}"
echo "============================================================"

# Build the manifest: NUL-delimited so odd filenames (spaces, newlines) can't
# corrupt the listing; sorted for reproducibility; relative paths (via cd)
# so the manifest doesn't encode MEDIA_SOURCE_DIR's absolute location.
set +e
new_manifest="$(${runner[@]+"${runner[@]}"} bash -c '
  set -e
  cd "$1" || exit 1
  find . -type f -print0 | LC_ALL=C sort -z | xargs -0 '"$HASH_BIN"'
' _ "$MEDIA_SOURCE_DIR")"
manifest_rc=$?
set -e

if [ "$manifest_rc" -ne 0 ]; then
  echo "ERROR: failed to read/hash '$MEDIA_SOURCE_DIR' (rc=$manifest_rc)" >&2
  exit 1
fi

if [ -z "$new_manifest" ]; then
  echo "NOTE: '$MEDIA_SOURCE_DIR' contains no files — nothing to archive."
fi

if [ -f "$manifest_file" ] && [ -e "$dir/media-latest.tar.gz" ] && [ "$new_manifest" = "$(cat "$manifest_file")" ]; then
  echo "Unchanged since the last backup (manifest matches $manifest_file) — skipping a new archive."
else
  echo "Content changed (or no prior manifest) — writing a fresh archive."
  set +e
  ${runner[@]+"${runner[@]}"} tar -C "$MEDIA_SOURCE_DIR" -czf "$tmp" .
  rc=$?
  set -e
  if [ "$rc" -ne 0 ] || [ ! -s "$tmp" ]; then
    echo "ERROR: tar failed (rc=$rc)" >&2
    rm -f "$tmp"
    exit 1
  fi
  chmod 0600 "$tmp"
  mv -f "$tmp" "$final"
  ln -sfn "$base" "$dir/media-latest.tar.gz"
  printf '%s' "$new_manifest" >"$manifest_tmp"
  mv -f "$manifest_tmp" "$manifest_file"
  echo "    ok — $(du -h "$final" | cut -f1)"
fi

# Prune old archives (after any fresh write, so today's archive — age 0 — can
# never be the thing pruned away, matching backup.sh's own ordering).
#
# #0434 review: unlike backup.sh, this script does NOT write an archive on
# every run — an unchanged source takes the skip path above. So "age 0"
# alone does not protect the last copy: with no edits, the one archive on
# disk ages past retention and gets deleted, and the manifest still matches
# forever after, so nothing replaces it. Exclude whatever media-latest
# currently resolves to, so the newest archive is retained regardless of age.
keep_args=()
if [ -L "$dir/media-latest.tar.gz" ]; then
  keep_target="$(readlink "$dir/media-latest.tar.gz")"
  [ -n "$keep_target" ] && keep_args=(! -name "$keep_target")
fi
pruned="$(find "$dir" -maxdepth 1 -type f -name 'media-*.tar.gz' \
            ${keep_args[@]+"${keep_args[@]}"} \
            -mtime +"$MEDIA_BACKUP_RETENTION_DAYS" -print -delete | wc -l | tr -d ' ')"
[ "$pruned" -gt 0 ] && echo "    pruned $pruned archive(s) older than ${MEDIA_BACKUP_RETENTION_DAYS}d"

echo
echo "Done — media backup complete."
