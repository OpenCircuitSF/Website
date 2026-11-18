#!/usr/bin/env bash
#
# ship.sh — cut a release and deploy it in one step.
#
#   scripts/ship.sh 0.13.0          # asks [Y/n] before touching production
#   scripts/ship.sh 0.13.0 --yes    # answers yes for you (needed when run
#                                   # with Claude Code's `!` prefix, which
#                                   # has no keyboard input)
#
# Safe to re-run: if vX.Y.Z is already tagged at HEAD it just deploys again.
#
# Bumps `const version` in cmd/opencircuit/main.go, commits "Release vX.Y.Z",
# tags vX.Y.Z, then runs scripts/deploy.sh — which still asks [Y/n] before it
# migrates the database or touches the live service. Answer "n" there and
# nothing on production changes (the local commit and tag remain).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-}"
YES="${2:-}"
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "usage: scripts/ship.sh X.Y.Z [--yes]" >&2
  exit 2
fi

if [ "$(git rev-parse --abbrev-ref HEAD)" != "main" ]; then
  echo "refusing: not on main" >&2; exit 1
fi
if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "refusing: uncommitted changes — commit or set them aside first" >&2
  git status --short --untracked-files=no >&2
  exit 1
fi
RESUME=
if git rev-parse -q --verify "refs/tags/v$VERSION" >/dev/null; then
  if [ "$(git rev-list -n1 "v$VERSION")" = "$(git rev-parse HEAD)" ]; then
    echo "v$VERSION is already tagged at HEAD — deploying it again."
    RESUME=1
  else
    echo "refusing: tag v$VERSION exists but is not HEAD" >&2; exit 1
  fi
fi

MAIN=cmd/opencircuit/main.go
CURRENT="$(sed -n 's/^const version = "\(.*\)"$/\1/p' "$MAIN")"
if [ -z "$CURRENT" ]; then
  echo "refusing: could not find const version in $MAIN" >&2; exit 1
fi

if [ -z "$RESUME" ] && [ "$CURRENT" != "$VERSION" ]; then
  echo "Bumping version $CURRENT -> $VERSION"
  sed -i '' "s/^const version = \"$CURRENT\"$/const version = \"$VERSION\"/" "$MAIN"
  git add "$MAIN"
  git commit -q -m "Release v$VERSION" -- "$MAIN"
fi
[ -n "$RESUME" ] || git tag "v$VERSION"
git show --stat --oneline HEAD | head -5

echo
echo "Deploying v$VERSION — deploy.sh will ask before changing production."
if [ "$YES" = "--yes" ]; then
  # #0714: yes dies of SIGPIPE (141) once deploy.sh exits; || true keeps the
  # pipeline status deploy.sh's own under pipefail.
  { yes y || true; } | scripts/deploy.sh "$VERSION"
elif [ ! -t 0 ]; then
  echo "No keyboard input here, so the [Y/n] prompt would abort. Re-run with --yes:" >&2
  echo "  scripts/ship.sh $VERSION --yes" >&2
  exit 1
else
  exec scripts/deploy.sh "$VERSION"
fi
