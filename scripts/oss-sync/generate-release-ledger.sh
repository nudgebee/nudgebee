#!/usr/bin/env bash
set -euo pipefail

usage() {
  echo "usage: $0 <ee-repo> <base-tag> <target-tag> <output.tsv>" >&2
  exit 2
}

[[ $# -eq 4 ]] || usage

ee_repo=$1
base_tag=$2
target_tag=$3
output=$4

base_sha=$(git -C "$ee_repo" rev-parse "${base_tag}^{}")
target_sha=$(git -C "$ee_repo" rev-parse "${target_tag}^{}")

mkdir -p "$(dirname "$output")"

{
  printf '# schema\tordinal\tsha\tkind\tcommit_date\tpr\tmerge_resolution\tee_boundary\tdisposition\tstatus\tsubject\tnotes\n'
  printf '# base\t%s\t%s\n' "$base_tag" "$base_sha"
  printf '# target\t%s\t%s\n' "$target_tag" "$target_sha"
  printf '# invariant\tevery commit in %s..%s appears exactly once; no pending disposition at completion\n' "$base_tag" "$target_tag"

  ordinal=0
  while IFS=$'\t' read -r sha parents commit_date subject; do
    ordinal=$((ordinal + 1))
    parent_count=$(wc -w <<<"$parents" | tr -d ' ')
    if (( parent_count > 1 )); then
      kind=merge
      if [[ -n "$(git -C "$ee_repo" show --remerge-diff --format= --no-ext-diff "$sha")" ]]; then
        merge_resolution=required
      else
        merge_resolution=none
      fi
    else
      kind=commit
      merge_resolution=not-applicable
    fi

    ee_boundary=oss-paths-only
    while IFS= read -r changed_path; do
      while IFS= read -r excluded_path; do
        [[ -z "$excluded_path" || "$excluded_path" == \#* ]] && continue
        if [[ "$excluded_path" == */ ]]; then
          [[ "$changed_path" == "$excluded_path"* ]] && ee_boundary=touches-ee-path
        elif [[ "$changed_path" == "$excluded_path" ]]; then
          ee_boundary=touches-ee-path
        fi
      done <"$ee_repo/.oss-exclude"
    done < <(git -C "$ee_repo" diff-tree --root --no-commit-id --name-only -r -m "$sha" | sort -u)
    pr=$(sed -nE 's/.*\(#([0-9]+)\)(.*)?$/\1/p' <<<"$subject")
    if [[ -z "$pr" ]]; then
      pr=$(sed -nE 's/^Merge pull request #([0-9]+).*/\1/p' <<<"$subject")
    fi
    [[ -n "$pr" ]] || pr=-
    printf '%d\t%s\t%s\t%s\t%s\t%s\t%s\tpending\tpending\t%s\t-\n' \
      "$ordinal" "$sha" "$kind" "$commit_date" "$pr" "$merge_resolution" "$ee_boundary" "$subject"
  done < <(git -C "$ee_repo" log --reverse --format='%H%x09%P%x09%aI%x09%s' "${base_tag}..${target_tag}")
} >"$output"

expected=$(git -C "$ee_repo" rev-list --count "${base_tag}..${target_tag}")
actual=$(grep -vc '^#' "$output")
unique=$(awk -F '\t' '!/^#/ {print $2}' "$output" | sort -u | wc -l | tr -d ' ')

if [[ "$actual" != "$expected" || "$unique" != "$expected" ]]; then
  echo "ledger invariant failed: expected=$expected rows=$actual unique=$unique" >&2
  exit 1
fi

echo "ledger generated: $output ($actual commits, $unique unique)"
