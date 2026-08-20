#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 3 || $# -gt 5 ]]; then
  echo "usage: $0 <ee-repo> <ledger.tsv> <results.tsv> [start-ordinal] [end-ordinal]" >&2
  exit 2
fi

ee_repo=$1
ledger=$2
results=$3
start=${4:-1}
end=${5:-999999}

git rev-parse --show-toplevel >/dev/null
[[ -f "$ledger" ]] || { echo "missing ledger: $ledger" >&2; exit 2; }
[[ -f "$ee_repo/.oss-exclude" ]] || { echo "missing $ee_repo/.oss-exclude" >&2; exit 2; }

if [[ ! -f "$results" ]]; then
  printf '# ordinal\tsha\tresult\toss_commit\tdetail\n' >"$results"
fi

already_recorded() {
  awk -F '\t' -v sha="$1" '!/^#/ && $2 == sha { found=1 } END { exit !found }' "$results"
}

is_excluded_path() {
  local changed_path=$1 excluded_path

  case "$changed_path" in
    .jules/*|app/src/lib/license*|api-server/services/licensing/*)
      return 0
      ;;
    .github/workflows/nudgebee-build-*.yaml|.github/workflows/cost-server-dev-gke.yaml|.github/workflows/cost-server-test-gke.yaml|.github/workflows/cost-server-prod.yaml)
      return 0
      ;;
  esac

  while IFS= read -r excluded_path; do
    [[ -z "$excluded_path" || "$excluded_path" == \#* ]] && continue
    if [[ "$excluded_path" == */ ]]; then
      [[ "$changed_path" == "$excluded_path"* ]] && return 0
    elif [[ "$changed_path" == "$excluded_path" ]]; then
      return 0
    fi
  done <"$ee_repo/.oss-exclude"
  return 1
}

is_safe_ours_conflict() {
  local path=$1
  is_excluded_path "$path" && return 0
  case "$path" in
    .oss-exclude|.github/workflows/oss-boundary.yaml|.github/workflows/*-dev-gke.yaml|.github/workflows/*-test-gke.yaml|.github/workflows/*-prod.yaml|api-server/migrations/migrations/app/atlas.sum)
      return 0
      ;;
  esac
  return 1
}

resolve_safe_conflicts() {
  local path
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    is_safe_ours_conflict "$path" || continue
    if git cat-file -e ":2:$path" 2>/dev/null; then
      git restore --ours --worktree -- "$path"
      git add -- "$path"
    else
      git rm -f --ignore-unmatch -- "$path" >/dev/null
    fi
  done < <(git diff --name-only --diff-filter=U)
}

drop_excluded_changes() {
  local sha=$1 path
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    is_excluded_path "$path" || continue
    if git cat-file -e "HEAD:$path" 2>/dev/null; then
      git restore --source=HEAD --staged --worktree -- "$path"
    else
      git rm -f --ignore-unmatch -- "$path" >/dev/null
    fi
  done < <(git -C "$ee_repo" diff-tree --no-commit-id --name-only -r "$sha^" "$sha")
}

record() {
  local ordinal=$1 sha=$2 result=$3 oss_commit=$4 detail=$5
  detail=${detail//$'\t'/ }
  detail=${detail//$'\n'/; }
  [[ -n "$oss_commit" ]] || oss_commit=-
  [[ -n "$detail" ]] || detail=-
  printf '%s\t%s\t%s\t%s\t%s\n' "$ordinal" "$sha" "$result" "$oss_commit" "$detail" >>"$results"
}

while IFS=$'\t' read -r ordinal sha kind commit_date pr merge_resolution ee_boundary disposition status subject notes; do
  [[ "$ordinal" == \#* || -z "$ordinal" ]] && continue
  (( ordinal < start || ordinal > end )) && continue
  [[ "$kind" == merge ]] && continue
  already_recorded "$sha" && continue

  echo "[$ordinal] $sha $subject"
  if ! git cherry-pick --no-commit "$sha" >/tmp/oss-sync-pick.out 2>/tmp/oss-sync-pick.err; then
    if [[ "${SAFE_RESOLVE:-0}" == 1 ]]; then
      resolve_safe_conflicts
      if [[ -z "$(git diff --name-only --diff-filter=U)" ]]; then
        drop_excluded_changes "$sha"
        if git diff --cached --quiet && git diff --quiet; then
          git reset --merge HEAD
          record "$ordinal" "$sha" empty "" "already present after safe OSS boundary resolution"
        else
          git commit --no-verify -C "$sha" >/tmp/oss-sync-commit.out
          oss_commit=$(git rev-parse HEAD)
          record "$ordinal" "$sha" applied-adapted "$oss_commit" "safe OSS boundary or atlas.sum resolution"
        fi
        continue
      fi
    fi
    conflicts=$(git diff --name-only --diff-filter=U | paste -sd, -)
    if ! git cherry-pick --abort 2>/dev/null; then
      git reset --merge HEAD
    fi
    record "$ordinal" "$sha" conflict "" "${conflicts:-$(tail -n 1 /tmp/oss-sync-pick.err)}"
    continue
  fi

  drop_excluded_changes "$sha"

  if git diff --cached --quiet && git diff --quiet; then
    record "$ordinal" "$sha" empty "" "already present or EE-only after boundary strip"
    continue
  fi

  git commit --no-verify -C "$sha" >/tmp/oss-sync-commit.out
  oss_commit=$(git rev-parse HEAD)
  record "$ordinal" "$sha" applied "$oss_commit" "${ee_boundary}"
done <"$ledger"

echo "batch complete: ordinals $start..$end"
