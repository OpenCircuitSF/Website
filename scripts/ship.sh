#!/usr/bin/env bash
#
# ship.sh — cut a release and deploy it in one step.
#
#   scripts/ship.sh 0.13.0
#
# Bumps `const version` in cmd/opencircuit/main.go, commits "Release vX.Y.Z",
# tags vX.Y.Z, then runs scripts/deploy.sh — which still asks [Y/n] before it
# migrates the database or touches the live service. Answer "n" there and
# nothing on production changes (the local commit and tag remain).
set -euo pipefail
cd "$(dirname "$0")/.."

VERSION="${1:-}"
if ! printf '%s' "$VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "usage: scripts/ship.sh X.Y.Z" >&2
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
if git rev-parse -q --verify "refs/tags/v$VERSION" >/dev/null; then
  echo "refusing: tag v$VERSION already exists" >&2; exit 1
fi

MAIN=cmd/opencircuit/main.go
CURRENT="$(sed -n 's/^const version = "\(.*\)"$/\1/p' "$MAIN")"
if [ -z "$CURRENT" ]; then
  echo "refusing: could not find const version in $MAIN" >&2; exit 1
fi

if [ "$CURRENT" != "$VERSION" ]; then
  echo "Bumping version $CURRENT -> $VERSION"
  sed -i '' "s/^const version = \"$CURRENT\"$/const version = \"$VERSION\"/" "$MAIN"
  git add "$MAIN"
  git commit -q -m "Release v$VERSION" -- "$MAIN"
fi
git tag "v$VERSION"
git show --stat --oneline HEAD | head -5

echo
echo "Deploying v$VERSION — deploy.sh will ask before changing production."
exec scripts/deploy.sh "$VERSION"
