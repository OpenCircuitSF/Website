#!/usr/bin/env bash
#
# release.sh — build the SPA and the Go binary, assemble the release bundle,
# scan it for restricted identifiers, and publish a GitHub Release with `gh`.
#
# This automates docs/deployment.md's Redeploy procedure, steps 1-2 (#0514).
# Read that doc for the full procedure this replaces the first two steps of,
# and #0509 for why photon fetches a release instead of getting a toolchain.
#
# Usage:
#   scripts/release.sh [--dry-run] [--allow-non-main] VERSION
#
#   scripts/release.sh 0.1.0             # builds, tags, and publishes v0.1.0
#   scripts/release.sh --dry-run 0.1.0   # everything except the publish
#
# VERSION is a plain semver number X.Y.Z (a leading "v"/"V" is accepted and
# stripped). Everything else — the tag, the commit, the repo slug, the
# bundle name — is derived, not asked for.
#
# --dry-run runs every gate and the full build, assembles and scans the real
# bundle, and prints exactly what `gh release create` would run — without
# running it. The assembled bundle is left on disk (path printed) so it can
# be inspected.
#
# --allow-non-main overrides the "must be on main" gate, deliberately, for
# the rare case of cutting a release from another branch.
#
# TAG SCHEME (decided by the user, 2026-09-12; the repo had zero tags and
# zero releases before this issue):
#
#   Plain semver: v<major>.<minor>.<patch> — e.g. v0.1.0. No date component,
#   no commit suffix. `sort -V` and git's own `--sort=version:refname` order
#   these correctly; a lexical sort does not (e.g. "v0.10.0" sorts before
#   "v0.2.0" lexically, which is wrong — version sort gets it right, and
#   this script and the check below both use it, never plain `sort`).
#
#   Because the tag no longer names the commit the way a date+sha suffix
#   would, provenance moves entirely to the release body and the binary:
#   the full commit sha is written into the release notes AND embedded via
#   `-ldflags -X main.commitHash=$COMMIT`, so `opencircuit --version` proves
#   the same thing the tag only labels — see docs/deployment.md's
#   "Provenance" section.
#
#   "The previous release" (the rollback path) is therefore NOT `gh release
#   list`'s default ordering — that sorts by creation date, and a hotfix
#   published out of numeric order would make the date-latest release and
#   the version-latest release disagree. This script determines it instead
#   by inserting the new tag into the full set of existing tags and sorting
#   by VERSION (`sort -V`), then taking the entry immediately before it —
#   see determine_previous_tag() below. Reproduce it by hand with:
#
#     git fetch --tags origin
#     { git tag --list 'v*'; echo "vX.Y.Z"; } | sort -V
#
#   and read the line above "vX.Y.Z". Never read release dates for this.
#
# Gates, before anything destructive or public, in the order they run:
#   1. cmd/opencircuit/main.go's `version` const equals VERSION — so the
#      binary's own --version output can never contradict the tag it ships
#      under. (Not one of #0514's named gates, but the same "provenance
#      must not lie" concern the tag scheme above exists to serve.)
#   2. Working tree clean (`git status --porcelain` empty).
#   3. HEAD is not detached (see DETACHED HEAD below).
#   4. On `main`, unless --allow-non-main is passed explicitly.
#   5. HEAD is an ancestor of origin/main — via
#      `git merge-base --is-ancestor "$COMMIT" origin/main`, NOT
#      `git ls-remote origin "$COMMIT"` (#0514 residual 1: ls-remote matches
#      ref *names*, not object ids, and can never match a full commit sha —
#      that check would silently never fire on the intended path).
#   6. The tag doesn't already exist, locally or on origin.
#   7. The assembled bundle — binary included — contains none of #0511's
#      restricted identifiers (see SCAN below).
#
# DETACHED HEAD (#0514 residual 2): docs/deployment.md's manual procedure
# recommends building in a throwaway `git worktree`, which detaches by
# construction, and `git push origin HEAD` fails from a detached HEAD. This
# script never runs `git push`, but gate 4 (on `main`) cannot be answered
# honestly from a detached HEAD either — there is no branch name to compare
# — so a detached HEAD in the CALLER's checkout is refused outright with a
# clear message rather than guessed at. The build step below always makes
# its OWN internal worktree regardless of what HEAD looks like in the
# caller's checkout, so the two detachments never interact: gates run
# against the originating checkout's HEAD before any worktree exists, and
# CLAUDE.md §8b's "npm run build overwrites the tracked web/dist/index.html
# placeholder" hazard never reaches the shared checkout at all, because the
# build never touches it — only the disposable worktree.
#
# SCAN (#0511): the nine restricted identifiers — the AWS account id, two
# public IPs, two EC2 instance ids, the Route 53 hosted zone id, two
# hostnames, and the IAM role name — are base64-encoded below rather than
# written as literals, so this tracked, published script does not itself
# become one more place they appear (#0514 criterion 5: "no host, account,
# instance, zone or role literals in the script or in release notes").
# Decoded only at scan time and grepped as a fixed string against every file
# in the assembled, unzipped bundle — which includes the binary, so this
# covers "the binary's strings" as well as the bundle's other files without
# a separate pass.
#
# FAILURE MODES AND CLEANUP:
#   - Any gate failing leaves no tag and no release — all seven gates run,
#     in order, before the tag is created (which `gh release create` does
#     itself, atomically with the release, via --target).
#   - If `gh release create` fails outright, nothing was created.
#   - If it fails PARTWAY (the release/tag object created remotely, then an
#     asset upload fails), this script runs
#     `gh release delete "$TAG" --yes --cleanup-tag` to remove both, and
#     prints the exact commands to check and finish that removal by hand if
#     the automatic cleanup cannot confirm it worked.
#   - This script never runs `git push`. A failed publish never touches any
#     ref on origin — `gh release create --target` pushes the tag itself,
#     and `--cleanup-tag` above is what removes it again on failure.
#
set -euo pipefail

# Resolve an absolute path BEFORE any `cd` (CLAUDE.md §8: ${BASH_SOURCE[0]}
# holds the same relative string as $0 and breaks identically once the
# script has changed directory).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

step(){ printf '\n\033[1m==> %s\033[0m\n' "$*"; }
ok(){   printf '    \033[32m\xe2\x9c\x93\033[0m %s\n' "$*"; }
info(){ printf '    %s\n' "$*"; }
warn(){ printf '    \033[33m! %s\033[0m\n' "$*" >&2; }
die(){  printf '\n\033[31m\xe2\x9c\x97 ERROR: %s\033[0m\n' "$*" >&2; exit 1; }

case "${1:-}" in -h|--help) sed -n '2,110p' "$0"; exit 0 ;; esac

# ---- #0511 restricted identifiers, base64-encoded — see SCAN above --------
# Never write the decoded values into this file, into a commit message, or
# into release notes.
RESTRICTED_B64=(
  "Mzc4MTUyMzMwNzE5"             # AWS account id
  "OTguODQuNzUuMTg0"             # public IP
  "NDQuMjIyLjIwOS4xODM="         # public IP
  "aS0wMWM0NTQyOWM3OGYzYWRmNw==" # EC2 instance id
  "aS0wZTNiZDg5ZTg3ZDFjMjM2NA==" # EC2 instance id
  "WjA4MjUwNjdSVjhRWTVVSUtTOTY=" # Route 53 hosted zone id
  "cGhvdG9uLnNzdG9vbHMuY28="     # hostname
  "Ymx1ZXNreS5zc3Rvb2xzLmNv"     # hostname
  "b3BlbmNpcmN1aXQtd2ViLTIwMjY=" # IAM role name
)

# ---- arg parsing ------------------------------------------------------------
DRY_RUN=0
ALLOW_NON_MAIN=0
VERSION=""

while [ $# -gt 0 ]; do
  case "$1" in
    --dry-run) DRY_RUN=1; shift ;;
    --allow-non-main) ALLOW_NON_MAIN=1; shift ;;
    -h|--help) sed -n '2,110p' "$0"; exit 0 ;;
    --) shift ;;
    -*) die "unknown flag: $1 (see --help)" ;;
    *)
      if [ -n "$VERSION" ]; then
        die "unexpected extra argument: $1 (VERSION is already '$VERSION')"
      fi
      VERSION="$1"
      shift
      ;;
  esac
done

[ -n "$VERSION" ] || die "usage: scripts/release.sh [--dry-run] [--allow-non-main] VERSION (see --help)"

# Strip a leading v/V — "0.1.0" and "v0.1.0" both mean tag v0.1.0.
VERSION="${VERSION#[vV]}"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
  die "VERSION must be plain semver X.Y.Z (got: '$VERSION'). No pre-release or build suffix — see the TAG SCHEME comment at the top of this script."
fi

TAG="v${VERSION}"

cd "$REPO_ROOT"

# ---- preflight: tools --------------------------------------------------------
step "Preflight"
for c in git gh go npm zip shasum base64 grep; do
  command -v "$c" >/dev/null 2>&1 || die "required command not found: $c"
done
gh auth status >/dev/null 2>&1 || die "gh is not authenticated — run 'gh auth login' first."
ok "all required tools present, gh authenticated"

# ---- gate 1: cmd/opencircuit/main.go's version const matches VERSION -------
step "Gate: cmd/opencircuit/main.go version const"
MAIN_GO="$REPO_ROOT/cmd/opencircuit/main.go"
CODE_VERSION="$(grep -m1 -E '^const version = "[^"]+"' "$MAIN_GO" | sed -E 's/^const version = "([^"]+)"/\1/')"
[ -n "$CODE_VERSION" ] || die "could not find 'const version = \"...\"' in $MAIN_GO — has it moved or been renamed?"
[ "$CODE_VERSION" = "$VERSION" ] || die "cmd/opencircuit/main.go's version const is \"$CODE_VERSION\" but you asked to release \"$VERSION\". Update and commit that const first, so the built binary's --version output matches the tag it ships under."
ok "version const matches: $VERSION"

# ---- gate 2: clean working tree ---------------------------------------------
step "Gate: working tree is clean"
DIRTY="$(git status --porcelain)"
[ -z "$DIRTY" ] || die "working tree is dirty; commit or set aside your changes before releasing (a release built from uncommitted work has a tag that does not describe it):
$DIRTY"
ok "working tree clean"

# ---- gate 3: not detached HEAD ----------------------------------------------
step "Gate: HEAD is a branch, not detached"
CURRENT_BRANCH="$(git symbolic-ref --short -q HEAD || true)"
if [ -z "$CURRENT_BRANCH" ]; then
  die "HEAD is detached — refusing (#0514 residual 2). This script's own build step uses a disposable worktree internally regardless, but the CALLER's checkout must be on a real branch so the 'must be on main' gate can be answered honestly. Run 'git checkout main' (or the branch you mean) and re-run."
fi
ok "on branch: $CURRENT_BRANCH"

# ---- gate 4: on main, unless overridden -------------------------------------
step "Gate: on main"
if [ "$CURRENT_BRANCH" != "main" ]; then
  if [ "$ALLOW_NON_MAIN" -eq 1 ]; then
    warn "releasing from '$CURRENT_BRANCH', not 'main' — allowed via --allow-non-main"
  else
    die "refusing to release from branch '$CURRENT_BRANCH' (expected 'main'). Pass --allow-non-main to override deliberately."
  fi
else
  ok "on main"
fi

# ---- gate 5: commit is on origin/main ---------------------------------------
step "Gate: commit is pushed to origin/main"
COMMIT="$(git rev-parse HEAD)"
git fetch origin --quiet
if ! git merge-base --is-ancestor "$COMMIT" origin/main; then
  die "commit $COMMIT is not on origin/main. Push it first (git push origin $CURRENT_BRANCH) and re-run. (#0514 residual 1: this check is 'git merge-base --is-ancestor', not 'git ls-remote', which only matches ref tips and can never match a full commit sha.)"
fi
ok "commit $COMMIT is on origin/main"

# ---- gate 6: tag does not already exist -------------------------------------
step "Gate: tag $TAG does not already exist"
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
  die "tag $TAG already exists locally. Pick a different VERSION, or if this local tag was created in error: git tag -d $TAG"
fi
if git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
  die "tag $TAG already exists on origin. Pick a different VERSION."
fi
ok "tag $TAG is free"

# ---- determine the previous release, by version, not date -------------------
step "Determining the previous release (rollback target)"
determine_previous_tag() {
  # Insert TAG into the sorted set of existing tags and print the one
  # immediately before it. Correct even for an out-of-order release
  # (a hotfix tagged lower than the newest existing tag) because it sorts
  # every existing tag together with the candidate, rather than assuming
  # the newest existing tag is always "previous".
  { git tag --list 'v*'; printf '%s\n' "$TAG"; } | sort -V | awk -v cur="$TAG" '
    $0 == cur { print prev; exit }
    { prev = $0 }
  '
}
PREV_TAG="$(determine_previous_tag)"
if [ -n "$PREV_TAG" ]; then
  ok "previous release (by version): $PREV_TAG"
else
  ok "no previous release — this would be the first tag"
fi

# ---- derive the repo slug ----------------------------------------------------
step "Deriving repo"
GH_REPO="$(gh repo view --json nameWithOwner -q .nameWithOwner)"
[ -n "$GH_REPO" ] || die "could not derive the repo via 'gh repo view' — run it by hand to see why."
ok "repo: $GH_REPO"

# ---- build ------------------------------------------------------------------
WORKTREE_DIR=""
SCRATCH_DIR=""
NOTES_FILE=""
cleanup() {
  if [ -n "$WORKTREE_DIR" ] && [ -d "$WORKTREE_DIR" ]; then
    git worktree remove --force "$WORKTREE_DIR" >/dev/null 2>&1 || rm -rf "$WORKTREE_DIR"
  fi
  [ -n "$NOTES_FILE" ] && [ -f "$NOTES_FILE" ] && rm -f "$NOTES_FILE"
  # SCRATCH_DIR (the assembled bundle + checksums) is deliberately NOT
  # removed here — it is left in place on a dry run or on any failure, for
  # inspection, and removed explicitly after a successful real publish.
}
trap cleanup EXIT

step "Building in a disposable worktree at $COMMIT"
WORKTREE_DIR="$(mktemp -u "${TMPDIR:-/tmp}/opencircuit-release-worktree-XXXXXX")"
git worktree add --detach --quiet "$WORKTREE_DIR" "$COMMIT"
ok "worktree: $WORKTREE_DIR"

SCRATCH_DIR="$(mktemp -d "${TMPDIR:-/tmp}/opencircuit-release-XXXXXX")"
STAGE="$SCRATCH_DIR/release-${TAG}"
mkdir -p "$STAGE"

step "Building the SPA (web/dist) — must happen before the Go build (//go:embed all:dist)"
( cd "$WORKTREE_DIR/web" && npm ci && npm run build )
ok "SPA built"

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
# scan and zip steps below operate only on $STAGE (nothing left in a
# throwaway worktree could be mistaken for part of the release).
git worktree remove --force "$WORKTREE_DIR" >/dev/null 2>&1 || rm -rf "$WORKTREE_DIR"
WORKTREE_DIR=""

# ---- scan for #0511 restricted identifiers, before anything is zipped ------
step "Scanning the bundle (including the binary) for #0511 restricted identifiers"
SCAN_HIT=0
for enc in "${RESTRICTED_B64[@]}"; do
  raw="$(printf '%s' "$enc" | base64 -d)"
  if grep -rlaF -- "$raw" "$STAGE" >/dev/null 2>&1; then
    SCAN_HIT=1
  fi
done
if [ "$SCAN_HIT" -eq 1 ]; then
  die "the assembled bundle contains at least one #0511 restricted identifier (a production host, account, instance, zone, or IAM role literal). VALUE WITHHELD from this message deliberately — the bundle is left at $STAGE for inspection; do NOT publish it. Fix the source of the leak (check migrations/, deploy/systemd/opencircuit.service, and whatever went into the binary) and re-run."
fi
ok "no #0511 restricted identifiers found"

step "Computing checksums and zipping"
ZIP_NAME="opencircuit-${TAG}-linux-arm64.zip"
( cd "$STAGE" && zip -rq "$SCRATCH_DIR/$ZIP_NAME" . )
( cd "$SCRATCH_DIR" && shasum -a 256 "$ZIP_NAME" > SHA256SUMS )
ok "$(cat "$SCRATCH_DIR/SHA256SUMS")"

# ---- release notes ------------------------------------------------------------
NOTES_FILE="$(mktemp "${TMPDIR:-/tmp}/opencircuit-release-notes-XXXXXX")"
{
  printf 'Commit: %s\n' "$COMMIT"
  printf 'Tag: %s\n' "$TAG"
  printf 'Previous release: %s\n' "${PREV_TAG:-none (first release)}"
  printf '\nBuilt via scripts/release.sh from a clean, pushed main checkout.\n'
} > "$NOTES_FILE"

# ---- publish, or report what would be published -----------------------------
step "Publishing release $TAG"
TITLE="opencircuit $TAG"
if [ "$DRY_RUN" -eq 1 ]; then
  info "[dry-run] would run:"
  printf '    gh release create %s %s %s --repo %s --title %q --notes-file <notes below> --target %s\n' \
    "$TAG" "$SCRATCH_DIR/$ZIP_NAME" "$SCRATCH_DIR/SHA256SUMS" "$GH_REPO" "$TITLE" "$COMMIT"
  info "[dry-run] release notes would be:"
  sed 's/^/    /' "$NOTES_FILE"
  info "[dry-run] nothing published. Bundle left for inspection at: $SCRATCH_DIR"
  exit 0
fi

if ! gh release create "$TAG" "$SCRATCH_DIR/$ZIP_NAME" "$SCRATCH_DIR/SHA256SUMS" \
      --repo "$GH_REPO" \
      --title "$TITLE" \
      --notes-file "$NOTES_FILE" \
      --target "$COMMIT"; then
  warn "gh release create failed or partially failed — attempting cleanup of any half-created release/tag for $TAG"
  if gh release delete "$TAG" --repo "$GH_REPO" --yes --cleanup-tag >/dev/null 2>&1; then
    ok "cleaned up: no release, no tag left for $TAG"
  else
    die "publish failed AND automatic cleanup could not confirm removal. Check by hand:
    gh release view $TAG --repo $GH_REPO
    git ls-remote --tags origin refs/tags/$TAG
  If either shows the tag/release still exists, remove it before retrying:
    gh release delete $TAG --repo $GH_REPO --yes --cleanup-tag"
  fi
  die "release $TAG was not published (see above)."
fi

ok "published: https://github.com/$GH_REPO/releases/tag/$TAG"

step "Cleaning up local build residue"
rm -rf "${STAGE:?}" "${SCRATCH_DIR:?}/$ZIP_NAME" "${SCRATCH_DIR:?}/SHA256SUMS"
rmdir "$SCRATCH_DIR" 2>/dev/null || true
ok "done"
