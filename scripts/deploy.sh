#!/usr/bin/env bash
#
# deploy.sh — one-command deploy from the workstation: build the release
# bundle exactly as scripts/release.sh does, ship it to the production host,
# and run the remote install/migrate/restart steps over ssh (#0512).
#
# Run on the workstation, never on the box — the production host has no Go
# or npm toolchain and never will (#0509). This script automates the Redeploy
# procedure in docs/deployment.md; that document owns the *why* for every
# step below (why cross-compile, why migrate-before-install, why the
# restore-point check) and is referenced, not restated, here.
#
# Usage:
#   scripts/deploy.sh [--dry-run] [VERSION]
#
#   scripts/deploy.sh              # VERSION derived from cmd/opencircuit/main.go
#   scripts/deploy.sh 0.1.2        # VERSION must match the version const
#   scripts/deploy.sh --dry-run    # every local gate + the real build, then
#                                  # prints the exact remote plan — no ssh,
#                                  # no scp, nothing touched on the host
#
# What it does, in order:
#   1. Preflight — every tool this script needs, here and on the host, is
#      checked BEFORE anything is built, so a missing piece refuses the run
#      instead of dying mid-sequence (#0512 criterion 2).
#   2. The #0424 gate on untracked web/public/ files, and the general
#      dirty-tree notice — retained verbatim in meaning; see the block
#      comments below. The build runs in a throwaway git worktree at HEAD,
#      so the deploy ships exactly the committed tree and the embedded
#      commit hash is honest by construction (CLAUDE.md §8b's npm-build
#      hazard never reaches the shared checkout).
#   3. Build the SPA, then cross-compile the Go binary — pinned
#      GOOS=linux GOARCH=arm64 CGO_ENABLED=0, with the commit hash embedded
#      via -ldflags (#0509) — and assemble the same bundle release.sh does:
#      binary, migrations/, opencircuit.service. Nothing else, ever.
#   4. Scan the assembled bundle for #0511's restricted identifiers, using
#      the single encoded source of those values: release.sh's
#      RESTRICTED_B64 array, extracted from that file at runtime. If the
#      list cannot be read, the run refuses rather than scanning for
#      nothing.
#   5. Ship with the #0466 scp-precondition discipline (#0512 criterion 3):
#      destination stated, writability confirmed on the host, then scp the
#      bundle plus its SHA256SUMS and run `sha256sum -c` on the host.
#   6. Prove the artifact runs (—version must name this commit) and back up
#      the previous binary — both free and non-mutating, so they run before
#      the one irreversible step (docs/deployment.md's fixed ordering).
#   7. [Y/n] gate — only then, in order: verify a database restore point,
#      run golang-migrate from the bundle's migrations against the live
#      database (IRREVERSIBLE — the on-box CLI installed per #0513), install
#      the binary, restart the service.
#   8. Health-check the live site and confirm the installed binary reports
#      this commit (docs/deployment.md, Provenance).
#
# Configuration, all via environment (no #0511 identifiers may ever be
# written into this file — take them from the environment or a config file
# kept in the private AWS repo):
#   DEPLOY_HOST   ssh destination; default is the operator's ssh-config
#                 alias (a label on this machine, not an address)
#   RELEASES_ROOT releases directory on the host
#   BIN_PATH      the binary path systemd runs on the host
#   SERVICE       systemd unit name
#   PUBLIC_URL    public site URL for the /health check
#
set -euo pipefail

# Resolve an absolute path BEFORE any cd (CLAUDE.md §8: ${BASH_SOURCE[0]}
# breaks identically to $0 once the script has changed directory).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

step(){ printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok(){   printf '    \033[32m\xe2\x9c\x93\033[0m %s\n' "$*"; }
info(){ printf '    %s\n' "$*"; }
warn(){ printf '    \033[33m! %s\033[0m\n' "$*" >&2; }
die(){  printf '\n\033[31m\xe2\x9c\x97 ERROR: %s\033[0m\n' "$*" >&2; exit 1; }

# Run a command string on the production host. Each R_* plan below is built
# on the workstation on purpose — $DEST and the paths interpolate HERE, while
# anything the HOST must expand (e.g. the escaped \$DATABASE_URL, expanded by
# the remote bash after sourcing config.env) is escaped so it survives the
# trip. That deliberate client/remote split is what SC2029 exists to flag, so
# it is acknowledged once, at this one site, instead of at every call.
run_remote() {
  # shellcheck disable=SC2029
  ssh "$DEPLOY_HOST" "$1"
}

case "${1:-}" in -h|--help) sed -n '2,62p' "$0"; exit 0 ;; esac

# ---- #0511 restricted identifiers — extracted, never written here -----------
# The nine identifiers live in exactly one tracked place: scripts/release.sh's
# RESTRICTED_B64 array, base64-encoded there (#0514). This script pulls that
# array out of the file at runtime rather than carrying a second encoded copy
# that could drift, then fails closed if the extraction produced nothing —
# a silently empty list would make the scan below vacuous.
RELEASE_SCRIPT="$SCRIPT_DIR/release.sh"
[ -f "$RELEASE_SCRIPT" ] || die "scripts/release.sh not found — it is the single source of the #0511 identifier list this script scans with."
RESTRICTED_B64=()
eval "$(awk '/^RESTRICTED_B64=\(/,/^\)/' "$RELEASE_SCRIPT")"
if [ "${#RESTRICTED_B64[@]}" -lt 1 ]; then
  die "could not extract the RESTRICTED_B64 identifier list from scripts/release.sh — the #0511 scan would be vacuous, so refusing to run."
fi

# ---- arg parsing -------------------------------------------------------------
DRY_RUN=0
VERSION_ARG=""
while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    --) shift ;;
    -*) die "unknown flag: $1 (see --help)" ;;
    *)
      if [ -n "$VERSION_ARG" ]; then
        die "unexpected extra argument: $1 (VERSION is already '$VERSION_ARG')"
      fi
      VERSION_ARG="$1"
      shift
      ;;
  esac
done

cd "$REPO_ROOT"

# ---- configuration -----------------------------------------------------------
DEPLOY_HOST="${DEPLOY_HOST:-photon}"
RELEASES_ROOT="${RELEASES_ROOT:-/opt/opencircuit/releases}"
BIN_PATH="${BIN_PATH:-/usr/local/bin/opencircuit}"
SERVICE="${SERVICE:-opencircuit}"
PUBLIC_URL="${PUBLIC_URL:-https://www.opencircuitsf.com}"

# ---- preflight: tools here, and (outside --dry-run) on the host -------------
step "Preflight — every tool, before anything is built"
for c in git go npm zip shasum ssh scp awk base64 grep curl; do
  command -v "$c" >/dev/null 2>&1 || die "required command not found: $c — a deploy that dies mid-sequence is worse than one that refuses to start (#0512 criterion 2)."
done
ok "all workstation tools present"

# The version this build must carry. Derived from the binary's own version
# const so --version output can never contradict what was deployed; an
# explicit argument must agree with it (scripts/release.sh gate 1's rule).
MAIN_GO="$REPO_ROOT/cmd/opencircuit/main.go"
CODE_VERSION="$(grep -m1 -E '^const version = "[^"]+"' "$MAIN_GO" | sed -E 's/^const version = "([^"]+)"/\1/')"
[ -n "$CODE_VERSION" ] || die "could not find 'const version = \"...\"' in $MAIN_GO — has it moved or been renamed?"
if [ -n "$VERSION_ARG" ] && [ "$VERSION_ARG" != "$CODE_VERSION" ]; then
  die "VERSION argument '$VERSION_ARG' does not match cmd/opencircuit/main.go's version const \"$CODE_VERSION\". Fix one or the other."
fi
VERSION="$CODE_VERSION"
TAG="v${VERSION}"
ok "version: $VERSION (tag $TAG)"

COMMIT="$(git rev-parse HEAD)"
HEAD_SHA="$(git rev-parse --short HEAD)"
ok "commit:  $HEAD_SHA — $(git log -1 --pretty=%s)"

# ---- host preflight: tools, sudo, and destination writability ---------------
# Checked before the build so a missing host piece costs seconds, not a
# built artifact (#0512 criterion 2). Nothing here mutates the host.
DEST="$RELEASES_ROOT/$TAG"
ZIP_NAME="opencircuit-${TAG}-linux-arm64.zip"

HOST_CHECK='set -e
command -v unzip >/dev/null 2>&1 || { echo "host preflight: unzip not found — sudo dnf install -y unzip"; exit 7; }
command -v migrate >/dev/null 2>&1 || { echo "host preflight: golang-migrate not found — install per docs/deployment.md Prerequisites (#0513)"; exit 7; }
sudo -n true 2>/dev/null || { echo "host preflight: passwordless sudo not available to this account"; exit 7; }
migrate -version'

if [ "$DRY_RUN" -eq 1 ]; then
  step "Dry-run: printing the host preflight instead of running it"
  info "[dry-run] ssh \"$DEPLOY_HOST\" 'bash -s' with:"
  printf '%s\n' "$HOST_CHECK" | sed 's/^/    [dry-run]   /'
else
  step "Host preflight: tools, sudo, and destination writability"
  if ! printf '%s' "$HOST_CHECK" | ssh "$DEPLOY_HOST" 'bash -s'; then
    die "production host preflight failed on $DEPLOY_HOST (its diagnostics are above) — refusing to build, ship, or touch anything."
  fi
  ok "host has unzip, golang-migrate, and passwordless sudo"
  # #0466 discipline, precondition half: state the destination and confirm
  # it is writable BEFORE shipping anything to it.
  info "destination: $DEPLOY_HOST:$DEST"
  # Intentional client-side interpolation of $DEST: the path is fixed here,
  # on the workstation, before anything is shipped (#0466's stated destination).
  # shellcheck disable=SC2029
  if ! run_remote "mkdir -p '$DEST' && test -w '$DEST'"; then
    die "cannot create or write $DEST on $DEPLOY_HOST — fix permissions before shipping."
  fi
  ok "destination is writable"
fi

# GATE-0424-BEGIN
# ---- hard gate: nothing untracked under web/public/ (#0424) ----------------
# This deploy builds the committed tree in a throwaway worktree, so untracked
# files under web/public/ can never reach the binary — but they are exactly
# the files an operator most likely INTENDED to ship (#0416 shipped two
# workshop covers for two days this way, and #0424's lesson is that a
# perma-lit general notice cannot carry that signal). Refusing here forces
# the file to be committed or deliberately removed before a deploy; the
# general dirty-tree notice below covers everything else.
UNTRACKED_PUBLIC="$(git status --porcelain -- web/public/ | awk '/^\?\? /{sub(/^\?\? /,""); print}')"
if [ -n "$UNTRACKED_PUBLIC" ]; then
  die "untracked file(s) under web/public/ — this deploy ships the committed tree only, so these would be silently LEFT OUT (issues/0416.md shipped the inverse by accident; see docs/media.md):
$UNTRACKED_PUBLIC
Commit the file if it belongs in this deploy, or remove it if it landed here by mistake. Workshop media and other server-placed content belongs on the box at /var/www/media, never in web/public/."
fi
# GATE-0424-END

[ -n "$(git status --porcelain)" ] && info "note: working tree has uncommitted changes — the build runs in a throwaway worktree at HEAD, so uncommitted changes are NOT part of this deploy; commit them to ship them"

# ---- build -------------------------------------------------------------------
WORKTREE_DIR=""
SCRATCH_DIR=""
cleanup() {
  if [ -n "$WORKTREE_DIR" ] && [ -d "$WORKTREE_DIR" ]; then
    git worktree remove --force "$WORKTREE_DIR" >/dev/null 2>&1 || rm -rf "$WORKTREE_DIR"
  fi
  # SCRATCH_DIR (bundle + checksums) is deliberately NOT removed here — it
  # is left in place on a dry run or on any failure, for inspection, and
  # removed explicitly after a successful deploy (scripts/release.sh's rule).
}
trap cleanup EXIT

step "Building in a disposable worktree at $COMMIT"
# Scratch stays inside the repo, under gitignored .scratch/ (#0537 — operator
# directive 2026-09-13: tooling writes never leave the project for TMPDIR).
# mkdir -p is required: mktemp -d does not create missing parents.
mkdir -p "$REPO_ROOT/.scratch"
WORKTREE_DIR="$(mktemp -u "$REPO_ROOT/.scratch/deploy-worktree-XXXXXX")"
git worktree add --detach --quiet "$WORKTREE_DIR" "$COMMIT"
ok "worktree: $WORKTREE_DIR"

SCRATCH_DIR="$(mktemp -d "$REPO_ROOT/.scratch/deploy-XXXXXX")"
STAGE="$SCRATCH_DIR/release-${TAG}"
mkdir -p "$STAGE"

step "Building the SPA (web/dist) — must happen before the Go build (//go:embed all:dist)"
( cd "$WORKTREE_DIR/web" && npm ci && npm run build )
BUILT_JS="$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$WORKTREE_DIR/web/dist/index.html" | head -1 || true)"
[ -n "$BUILT_JS" ] || die "the SPA build produced no hashed assets — web/dist may still hold the committed placeholder; inspect $WORKTREE_DIR/web/dist/ before retrying."
ok "SPA built: $BUILT_JS"

step "Cross-compiling the Go binary (linux/arm64, embeds commit $COMMIT)"
( cd "$WORKTREE_DIR" && GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build \
    -ldflags "-X main.commitHash=$COMMIT" \
    -o "$STAGE/opencircuit" \
    ./cmd/opencircuit )
chmod 0755 "$STAGE/opencircuit"
if command -v file >/dev/null 2>&1; then
  info "$(file "$STAGE/opencircuit")"
fi
ok "binary built: $STAGE/opencircuit"

step "Assembling the bundle"
cp -R "$WORKTREE_DIR/migrations" "$STAGE/migrations"
cp "$WORKTREE_DIR/deploy/systemd/opencircuit.service" "$STAGE/opencircuit.service"
ok "staged: opencircuit, migrations/, opencircuit.service"

# The worktree's job is done — remove it now rather than at exit, so the
# scan and ship steps below operate only on $STAGE (scripts/release.sh's rule).
git worktree remove --force "$WORKTREE_DIR" >/dev/null 2>&1 || rm -rf "$WORKTREE_DIR"
WORKTREE_DIR=""

# ---- scan for #0511 restricted identifiers, before anything is shipped ------
step "Scanning the bundle (including the binary) for #0511 restricted identifiers"
SCAN_HIT=0
for enc in "${RESTRICTED_B64[@]}"; do
  raw="$(printf '%s' "$enc" | base64 -d 2>/dev/null)" || die "an entry of scripts/release.sh's identifier list failed to decode — fix the list before deploying."
  case "$raw" in
    '') die "an entry of scripts/release.sh's identifier list decoded to an empty value — the scan would be vacuous, so refusing to run." ;;
  esac
  if grep -rlaF -- "$raw" "$STAGE" >/dev/null 2>&1; then
    SCAN_HIT=1
  fi
done
if [ "$SCAN_HIT" -eq 1 ]; then
  die "the assembled bundle contains at least one #0511 restricted identifier (a production host, account, instance, zone, or IAM role literal). VALUE WITHHELD from this message deliberately — the bundle is left at $STAGE for inspection; do NOT ship it. Fix the source of the leak and re-run."
fi
ok "no #0511 restricted identifiers found"

step "Computing checksums and zipping"
( cd "$STAGE" && zip -rq "$SCRATCH_DIR/$ZIP_NAME" . )
( cd "$SCRATCH_DIR" && shasum -a 256 "$ZIP_NAME" > SHA256SUMS )
ok "$(cat "$SCRATCH_DIR/SHA256SUMS")"

# ---- ship, verify, and run the remote sequence -------------------------------
# The exact remote commands, first as a printed plan (--dry-run), then for
# real. Each is a string so the dry run shows the bytes that would run.
R_SHIP="scp -q '$SCRATCH_DIR/$ZIP_NAME' '$SCRATCH_DIR/SHA256SUMS' '$DEPLOY_HOST:$DEST/'"
R_VERIFY="cd '$DEST' && sha256sum -c SHA256SUMS"
R_UNZIP="cd '$DEST' && unzip -o '$ZIP_NAME'"
R_PROVE="'$DEST/opencircuit' --version"
R_BACKUP="sudo cp '$BIN_PATH' '$RELEASES_ROOT/opencircuit-previous' && '$RELEASES_ROOT/opencircuit-previous' --version"
R_MIGRATE="sudo bash -c 'source /etc/opencircuit/config.env && migrate -path $DEST/migrations -database \"\$DATABASE_URL\" up'"
R_INSTALL="sudo install -m 0755 '$DEST/opencircuit' '$BIN_PATH' && sudo systemctl restart '$SERVICE' && sudo systemctl is-active --quiet '$SERVICE'"

if [ "$DRY_RUN" -eq 1 ]; then
  step "Dry-run: remote plan (nothing executed, nothing touched)"
  info "[dry-run] would run:"
  printf '    [dry-run] ssh %s -- mkdir -p %q && test -w %q\n' "$DEPLOY_HOST" "$DEST" "$DEST"
  printf '    [dry-run] %s\n' "${R_SHIP#scp -q }"
  printf '    [dry-run] ssh %s -- %s\n' "$DEPLOY_HOST" "$R_VERIFY"
  printf '    [dry-run] ssh %s -- %s\n' "$DEPLOY_HOST" "$R_UNZIP"
  printf '    [dry-run] ssh %s -- %s\n' "$DEPLOY_HOST" "$R_PROVE"
  printf '    [dry-run] ssh %s -- %s\n' "$DEPLOY_HOST" "$R_BACKUP"
  info "[dry-run] after a Y/n gate:"
  printf '    [dry-run] ssh %s -- %s   # restore-point check, then IRREVERSIBLE migrate\n' "$DEPLOY_HOST" "$R_MIGRATE"
  printf '    [dry-run] ssh %s -- %s\n' "$DEPLOY_HOST" "$R_INSTALL"
  printf '    [dry-run] curl -fsS --max-time 15 %s/health\n' "$PUBLIC_URL"
  info "[dry-run] bundle left for inspection at: $SCRATCH_DIR"
  exit 0
fi

step "Shipping the bundle to $DEPLOY_HOST:$DEST"
scp -q "$SCRATCH_DIR/$ZIP_NAME" "$SCRATCH_DIR/SHA256SUMS" "$DEPLOY_HOST:$DEST/" \
  || die "scp to $DEPLOY_HOST:$DEST failed — nothing on the host has been touched."
ok "shipped: $ZIP_NAME + SHA256SUMS"

step "Verifying the shipped bytes against SHA256SUMS on the host"
if ! run_remote "$R_VERIFY"; then
  die "checksum verification failed on the host — a failed or truncated transfer must never read as success; the bundle at $SCRATCH_DIR is left for comparison."
fi
ok "sha256sum -c reported OK"

step "Extracting the bundle on the host"
if ! run_remote "$R_UNZIP"; then
  die "unzip failed on the host — inspect $DEST by hand."
fi
ok "extracted into $DEST"

step "Proving the artifact runs, before it replaces anything live"
VEROUT="$(run_remote "$R_PROVE" || true)"
case "$VEROUT" in
  *"$COMMIT"*) ok "artifact runs on the host and reports commit $COMMIT: $VEROUT" ;;
  *) die "the fetched artifact did not run, or did not report this commit (got: ${VEROUT:-<no output>}) — stopping before anything is backed up, migrated, or installed." ;;
esac

step "Backing up the previous binary"
if ! run_remote "$R_BACKUP"; then
  die "backing up $BIN_PATH to $RELEASES_ROOT/opencircuit-previous failed — stopping before migrations."
fi
ok "previous binary preserved at $RELEASES_ROOT/opencircuit-previous (first deploy after #0509 it may print no commit hash — expected, exactly once)"

step "Ready to deploy — review before the destructive steps"
info "commit:        $COMMIT ($TAG)"
info "bundle:        $DEST/$ZIP_NAME (sha256 verified on host)"
info "binary target: $BIN_PATH"
info "on $DEPLOY_HOST, after you confirm, this script will:"
info "  1. verify a database restore point (newest /var/backups/host/*-db dump)"
info "  2. run: migrate -path $DEST/migrations up   — IRREVERSIBLE schema change"
info "  3. install $DEST/opencircuit -> $BIN_PATH"
info "  4. systemctl restart $SERVICE"
printf '\n'
ans=""
read -r -p "Proceed with backup-verify, migrate, install, and restart? [y/N] " ans || true
case "${ans:-}" in
  [yY]|[yY][eE][sS]) : ;;
  *) info "Aborted before the destructive steps. The verified bundle is left at: $SCRATCH_DIR"; exit 0 ;;
esac

step "Confirming a verified database restore point exists (docs/deployment.md step 6)"
# Quoted assignment: this body must reach the host unexpanded, and nothing
# in it needs a value from this workstation (CLAUDE.md §8's unquoted-heredoc
# lesson — SC2016 is the point here, not a defect).
# shellcheck disable=SC2016
RESTORE_CHECK='set -e
d="$(ls -td /var/backups/host/*-db 2>/dev/null | head -1 || true)"
[ -n "$d" ] || { echo "restore-point check: no /var/backups/host/*-db directory found"; exit 3; }
[ -s "$d/opencircuit.dump" ] || { echo "restore-point check: $d/opencircuit.dump is missing or zero-byte"; exit 7; }
( cd "$d" && sha256sum -c SHA256SUMS --ignore-missing ) || { echo "restore-point check: checksum verification failed"; exit 7; }
echo "restore point verified: $d"'
if ! printf '%s' "$RESTORE_CHECK" | ssh "$DEPLOY_HOST" 'bash -s'; then
  die "no verified restore point on the host — per docs/deployment.md step 6, do NOT run migrations until one exists (an unverified dump is not a restore point)."
fi
ok "restore point verified"

step "Applying migrations from the bundle (IRREVERSIBLE)"
if ! run_remote "$R_MIGRATE"; then
  die "migrate failed on the host — the schema may be half-migrated. Recovery is the verified dump from the restore-point check, restored per scripts/db/restore.sh (docs/deployment.md, Backups) — not a binary swap."
fi
ok "migrations applied (migrate up is a no-op when nothing is pending)"

step "Installing the binary and restarting '$SERVICE'"
if ! run_remote "$R_INSTALL"; then
  die "install/restart failed or '$SERVICE' is not active — inspect: ssh $DEPLOY_HOST journalctl -u $SERVICE -n 50"
fi
ok "'$SERVICE' restarted and active"

step "Verifying the live site and the installed binary"
if ! curl -fsS --max-time 15 "$PUBLIC_URL/health"; then
  die "$PUBLIC_URL/health did not return healthy — the service may be up but unhealthy, or something in front of it is stale."
fi
LIVE_VER="$(run_remote "'$BIN_PATH' --version" || true)"
case "$LIVE_VER" in
  *"$COMMIT"*) ok "live binary reports commit $COMMIT: $LIVE_VER" ;;
  *) die "the installed binary does not report this commit (got: ${LIVE_VER:-<no output>}) — the install did not take, or something else is at $BIN_PATH." ;;
esac

step "Cleaning up local build residue"
rm -rf "${STAGE:?}" "${SCRATCH_DIR:?}/$ZIP_NAME" "${SCRATCH_DIR:?}/SHA256SUMS"
rmdir "$SCRATCH_DIR" 2>/dev/null || true
ok "done"
printf '\n\033[1m\xe2\x9c\x85 Deploy complete: %s is live at %s\033[0m\n' "$HEAD_SHA" "$PUBLIC_URL"
