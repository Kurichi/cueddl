#!/usr/bin/env bash
# Diffs the current schema against the same schema path at another git ref
# (e.g. a PR's base commit), without touching a live database. Prints the
# same output as `cueddl plan`.
#
# A schema package often imports sibling files (shared table definitions,
# multi-env packages, ...), so a bare `git show <ref>:file` isn't enough —
# this materializes the whole tree at that ref via `git worktree` and runs
# `cue export` from inside it.
#
# Usage: plan-against-commit.sh <ref> [schema-path]
#   ref:         git ref to diff against, e.g. HEAD~1, a branch, or a SHA
#   schema-path: path to the CUE package holding `database` (default ".")
set -euo pipefail
cd "$(dirname "$0")/.."

REF="${1:?usage: plan-against-commit.sh <ref> [schema-path]}"
SCHEMA_PATH="${2:-.}"
# `cue export` treats a bare path as a package import path rather than a
# filesystem path unless it starts with "." or "/", so normalize it.
case "$SCHEMA_PATH" in
./* | / | /*) ;;
.) ;;
*) SCHEMA_PATH="./$SCHEMA_PATH" ;;
esac

WORKTREE="$(mktemp -d)"
OLD_JSON="$(mktemp)"
cleanup() {
	rm -f "$OLD_JSON"
	git worktree remove --force "$WORKTREE" >/dev/null 2>&1 || rm -rf "$WORKTREE"
}
trap cleanup EXIT

git worktree add --detach --quiet "$WORKTREE" "$REF"

(cd "$WORKTREE/$SCHEMA_PATH" && go tool cue export . -e database) > "$OLD_JSON"
go tool cue export "$SCHEMA_PATH" -e database | go tool cueddl plan --against "$OLD_JSON"
