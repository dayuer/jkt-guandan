#!/bin/sh
# Install the repository's commit-msg guard. Linked worktrees share the common
# hooks directory, so one run covers every agent worktree of this clone.
set -eu

root=$(git rev-parse --show-toplevel)
hooks=$(git rev-parse --git-path hooks)
case "$hooks" in
  /*) ;;
  *) hooks="$root/$hooks" ;;
esac
mkdir -p "$hooks"
target="$hooks/commit-msg"
# Never replace a hook this script did not install: another tool's commit-msg
# may be doing real work. A symlink is ours (or harmless to replace); a regular
# file is not.
if [ -e "$target" ] && [ ! -L "$target" ]; then
  echo "install-hooks: $target exists and is not a symlink; refusing to overwrite it." >&2
  echo "install-hooks: merge scripts/hooks/commit-msg into it, or remove it and re-run." >&2
  exit 1
fi
ln -sfn "$root/scripts/hooks/commit-msg" "$target"
# The hook needs a ym-check-trailers binary on PATH; it is not vendored here.
# Build it from the yardmaster project's cmd/ym-check-trailers.
echo "installed commit-msg hook: $target"
echo "note: the hook is a symlink into this checkout. If this worktree is removed the symlink dangles and git silently skips it; CI remains the gate that every PR passes."
echo "it rejects AI attribution in commit messages; CI enforces the same rule for commits and PR bodies"
