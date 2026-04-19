#!/usr/bin/env bash
# scripts/git-version.sh
# Handles automatic versioning based on Git tags.

set -euo pipefail

PUSH=false
VERSION_OVERRIDE=""

# Parse arguments
while [[ $# -gt 0 ]]; do
  case $1 in
    --push) PUSH=true; shift ;;
    v*) VERSION_OVERRIDE="$1"; shift ;;
    *) shift ;;
  esac
done

# 1. Sync tags from remote to ensure we have the latest truth
git fetch --tags origin >/dev/null 2>&1 || true

# 2. Check if the current HEAD already has a version tag (vX.Y.Z)
CURRENT_TAG=$(git tag --points-at HEAD | grep '^v[0-9]' | head -n1 || true)

# 3. Determine the target version
if [ -n "$VERSION_OVERRIDE" ]; then
    TARGET_VERSION="$VERSION_OVERRIDE"
elif [ -n "$CURRENT_TAG" ]; then
    # Current commit is already tagged, reuse it
    TARGET_VERSION="$CURRENT_TAG"
else
    # Calculate next version by incrementing the latest patch
    LATEST_TAG=$(git tag -l "v*" | sort -V | tail -n1 || true)
    if [ -z "$LATEST_TAG" ]; then
        TARGET_VERSION="v0.0.1"
    else
        # Increment patch version (e.g., v0.1.2 -> v0.1.3)
        BASE=$(echo "$LATEST_TAG" | sed 's/^v//')
        IFS='.' read -r major minor patch rest <<< "$BASE"
        major=${major:-0}; minor=${minor:-0}; patch=${patch:-0}
        new_patch=$((patch + 1))
        TARGET_VERSION="v${major}.${minor}.${new_patch}"
    fi
fi

# 4. If we determined a new version that isn't on HEAD, tag and push it
if [ "$PUSH" == "true" ] && [ "$TARGET_VERSION" != "$CURRENT_TAG" ]; then
    # Ensure the tag doesn't exist elsewhere to avoid conflicts
    if git rev-parse "$TARGET_VERSION" >/dev/null 2>&1; then
        # If it exists but not on HEAD, we can't automatically move it safely
        # Just use it but don't push (or fail if we strictly want a new tag)
        echo "$TARGET_VERSION"
        exit 0
    fi
    
    # Create the local tag
    git tag "$TARGET_VERSION" >/dev/null 2>&1
    
    # Try to push to remote
    git push origin "$TARGET_VERSION" >/dev/null 2>&1 || {
        # If push fails (e.g., no remote, no permission), we still use the local tag
        true
    }
fi

# Output the final version for the build process
echo "$TARGET_VERSION"
