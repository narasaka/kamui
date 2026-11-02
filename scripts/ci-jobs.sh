#!/usr/bin/env bash
# Selects the CI job groups that a pull request's changed paths need.
#
# Reads changed paths, one per line, on standard input, or selects every group
# with --all. Prints `jobs=<JSON array of group names>` for $GITHUB_OUTPUT.
set -euo pipefail
set -f

# Each line names a job group and the path globs it covers. In these globs,
# * also matches /.
filters='
go             *.go go.mod go.sum .golangci* Makefile cmd/* internal/* integration/* scripts/* Casks/* macos/Resources/*
macos-app      macos/* Makefile scripts/build-macos-app.sh Casks/* cmd/* internal/app/* internal/cliapp/* internal/session/*
browser-smoke  go.mod go.sum cmd/* internal/* integration/*
'

# Paths that change CI itself select every group.
ci_paths='.github/* scripts/ci-jobs.sh'

# Paths that no job checks. Any other path that no filter covers selects every
# group, so new kinds of files are checked until they are given a filter.
unchecked_paths='docs/* assets/* Formula/* *.md LICENSE .gitignore'

matches() {
  local path="$1" pattern
  for pattern in $2; do
    # shellcheck disable=SC2254
    case "$path" in
      $pattern) return 0 ;;
    esac
  done
  return 1
}

groups=()
while read -r group _; do
  [[ -n "$group" ]] && groups+=("$group")
done <<<"$filters"

selected=()
select_group() {
  local group
  for group in "${selected[@]+"${selected[@]}"}"; do
    [[ "$group" == "$1" ]] && return
  done
  selected+=("$1")
}

select_all=false
case "$#:${1:-}" in
  0:) ;;
  1:--all) select_all=true ;;
  *)
    echo "usage: scripts/ci-jobs.sh [--all] < changed-paths" >&2
    exit 2
    ;;
esac

if [[ "$select_all" == false ]]; then
  # Read every path even after selecting all groups: stopping early would break
  # the pipe and fail the `git diff` that feeds this script.
  while IFS= read -r path || [[ -n "$path" ]]; do
    [[ -n "$path" && "$select_all" == false ]] || continue
    if matches "$path" "$ci_paths"; then
      select_all=true
      continue
    fi
    covered=false
    while read -r group globs; do
      [[ -n "$group" ]] || continue
      if matches "$path" "$globs"; then
        select_group "$group"
        covered=true
      fi
    done <<<"$filters"
    if [[ "$covered" == false ]] && ! matches "$path" "$unchecked_paths"; then
      select_all=true
    fi
  done
fi

if [[ "$select_all" == true ]]; then
  selected=("${groups[@]}")
fi

json=""
for group in "${groups[@]}"; do
  for chosen in "${selected[@]+"${selected[@]}"}"; do
    if [[ "$chosen" == "$group" ]]; then
      json+="${json:+,}\"${group}\""
    fi
  done
done
echo "jobs=[${json}]"
