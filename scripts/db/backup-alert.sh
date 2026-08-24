#!/usr/bin/env bash
#
# backup-alert.sh — fires when the nightly backup run fails. Wired via
# deploy/systemd/opencircuit-backup.service's OnFailure=opencircuit-backup-
# alert.service, so systemd runs this automatically whenever that unit exits
# non-zero — no cron MAILTO, no polling.
#
# #0229: backup.sh already exits non-zero on any dump failure (verified in
# #0062) — but until this script and the two units alongside it existed,
# nothing consumed that exit code. A backup that fails silently every night is
# worse than no backup, because it is believed.
#
# What this script does, always: logs a high-priority message to the system
# journal/syslog (`logger -p daemon.err`), so `journalctl -p err` or any
# journal-scraping monitor surfaces it with zero extra configuration, and so
# does the plain fact of `opencircuit-backup-alert.service` itself showing up
# in `systemctl --failed` if logger is somehow unavailable.
#
# What this script does, optionally: if BACKUP_ALERT_WEBHOOK_URL is set (via
# /etc/opencircuit/backup-alert.env, loaded by the alert unit's
# `EnvironmentFile=-...` — the leading `-` makes it optional so a fresh box
# without that file yet does not fail to start the unit), POST a short JSON
# body to it. This is written to work with a generic Slack/Discord/Mattermost
# "incoming webhook" shape ({"text": "..."}) or a healthchecks.io-style "fail"
# URL (which ignores the body). No such webhook is configured anywhere in this
# repo or on this machine — CLAUDE.md §10 items 2 and 6 record that no server
# and no external-notification credential exists yet. This leg is therefore
# UNVERIFIED beyond "the curl command is syntactically correct and degrades
# gracefully with no URL configured" — see docs/deployment.md's Backups
# section for what would verify it.
#
# Deliberately best-effort past the journal log: a webhook failure here must
# never mask the underlying backup failure (already logged and already
# visible via `systemctl --failed`) or cause this script itself to exit
# non-zero, which would just cascade into a second failed unit instead of
# delivering the alert.

set -uo pipefail   # not -e: every external step below is intentionally best-effort

MSG="Open Circuit SF: nightly PostgreSQL backup FAILED on $(hostname) at $(date -u +'%Y-%m-%dT%H:%M:%SZ') — see: journalctl -u opencircuit-backup -n 100"

logger -p daemon.err -t opencircuit-backup-alert "$MSG" || echo "opencircuit-backup-alert: $MSG" >&2

if [ -n "${BACKUP_ALERT_WEBHOOK_URL:-}" ]; then
  escaped="${MSG//\\/\\\\}"
  escaped="${escaped//\"/\\\"}"
  curl -fsS -m 10 -X POST -H 'Content-Type: application/json' \
    -d "{\"text\":\"${escaped}\"}" \
    "$BACKUP_ALERT_WEBHOOK_URL" \
    || echo "opencircuit-backup-alert: webhook POST to BACKUP_ALERT_WEBHOOK_URL failed (non-fatal — the journal log above is still the record of this failure)" >&2
fi

exit 0
