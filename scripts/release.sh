#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."

version="$(tr -d '\r\n' < VERSION)"
if [[ ! "$version" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "VERSION must contain a semantic version such as 1.1.0" >&2
  exit 1
fi
tag="v$version"

if [[ "$(git branch --show-current)" != main ]]; then
  echo "Release must run from the main branch" >&2
  exit 1
fi
git remote get-url origin >/dev/null
git fetch --quiet origin main --tags

if ! git merge-base --is-ancestor refs/remotes/origin/main HEAD; then
  echo "Local main is behind or diverged from origin/main" >&2
  exit 1
fi

if git show-ref --verify --quiet "refs/tags/$tag"; then
  if [[ "$(git rev-parse "refs/tags/$tag^{commit}")" != "$(git rev-parse HEAD)" ]] || [[ -n "$(git status --porcelain)" ]]; then
    echo "Tag $tag already exists and cannot be reused" >&2
    exit 1
  fi
  if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null; then
    echo "Tag $tag is already published" >&2
    exit 1
  else
    status=$?
    if [[ "$status" != 2 ]]; then exit "$status"; fi
  fi
  echo "Retrying atomic push of $tag..."
  git push --atomic origin HEAD:main "refs/tags/$tag"
  exit 0
fi

git add -A
if ! git diff --cached --quiet; then
  git diff --cached --check
  git commit -m "release $tag"
else
  echo "No changes to commit; tagging current HEAD as $tag"
fi
git tag -a "$tag" -m "Release $tag"
git push --atomic origin HEAD:main "refs/tags/$tag"
echo "Released $tag"
