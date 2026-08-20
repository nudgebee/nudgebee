#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <ledger> <results-dir> <manual.tsv> <branch-base> <output>" >&2
  exit 2
fi

ledger=$1
results_dir=$2
manual=$3
branch_base=$4
output=$5
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

git log --format='%H%x09%s' "$branch_base"..HEAD >"$tmp_dir/branch.tsv"
for result in "$results_dir"/ee-1.6.0-results.tsv "$results_dir"/ee-1.6.0-retry1-results.tsv "$results_dir"/ee-1.6.0-safe-results.tsv; do
  awk -F '\t' '!/^#/ {print $2"\t"$3"\t"$4"\t"$5}' "$result"
done | awk -F '\t' '!seen[$1]++' >"$tmp_dir/results.tsv"

awk -F '\t' -v OFS='\t' \
  -v branch="$tmp_dir/branch.tsv" -v results="$tmp_dir/results.tsv" -v manual="$manual" '
  BEGIN {
    while ((getline < branch) > 0) by_subject[$2] = $1
    while ((getline < results) > 0) {
      result[$1] = $2
      result_commit[$1] = $3
      result_note[$1] = $4
    }
    while ((getline < manual) > 0) {
      if ($1 ~ /^#/) continue
      manual_disp[$1] = $2
      manual_commit[$1] = $3
      manual_note[$1] = $4
    }
  }
  /^#/ { print; next }
  {
    sha=$2; kind=$3; subject=$10
    disposition=""; oss_commit="-"; note=""
    if (sha in manual_disp) {
      disposition=manual_disp[sha]; oss_commit=manual_commit[sha]; note=manual_note[sha]
    } else if (kind == "merge") {
      disposition="applied/oss-synced"
      note="merge commit has no independent resolution delta; constituent commits are dispositioned separately"
    } else if (subject in by_subject) {
      disposition="applied/oss-synced"; oss_commit=by_subject[subject]
      note="source commit preserved or reconciled under the same subject"
    } else if (result[sha] == "applied" || result[sha] == "applied-adapted") {
      disposition="applied/oss-synced"; oss_commit=result_commit[sha]
      note=result_note[sha]
    } else if (result[sha] == "empty") {
      disposition="applied/oss-synced"
      note=result_note[sha]
    } else {
      disposition="pending"
      note="unresolved"
    }
    $8=disposition; $9="complete"; $11=note " | oss_commit=" oss_commit
    print
  }
' "$ledger" >"$output"

rows=$(grep -vc '^#' "$output")
unique=$(awk -F '\t' '!/^#/ {print $2}' "$output" | sort -u | wc -l | tr -d ' ')
pending=$(awk -F '\t' '!/^#/ && ($8 == "pending" || $9 != "complete") {count++} END {print count+0}' "$output")
invalid=$(awk -F '\t' '!/^#/ && $8 != "applied/oss-synced" && $8 != "oss-skip" && $8 != "oss-deferred" {count++} END {print count+0}' "$output")
if [[ "$rows" != 424 || "$unique" != 424 || "$pending" != 0 || "$invalid" != 0 ]]; then
  echo "final ledger invariant failed: rows=$rows unique=$unique pending=$pending invalid=$invalid" >&2
  exit 1
fi
echo "final ledger complete: rows=$rows unique=$unique pending=$pending invalid=$invalid"
