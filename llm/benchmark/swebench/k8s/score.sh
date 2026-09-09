#!/usr/bin/env bash
# Read the SWE-bench scoreboard straight off Job outcomes.
#
# test.sh exits 0 only when every FAIL_TO_PASS passes and no PASS_TO_PASS
# regresses (ResolvedStatus.FULL), so Job Succeeded == resolved. No results
# file, no shared volume, no parsing.
set -euo pipefail
NS=nudgebee

kubectl -n "$NS" get jobs -l app=swebench-grade \
  -o jsonpath='{range .items[*]}{.metadata.labels.instance}{"\t"}{.status.succeeded}{"\t"}{.status.failed}{"\t"}{.status.active}{"\n"}{end}' \
| awk -F'\t' '
  { inst=$1; s=$2+0; f=$3+0; a=$4+0
    if (s>0)      { v="RESOLVED";   res++ }
    else if (f>0) { v="unresolved"; unres++ }
    else if (a>0) { v="running";    run++ }
    else          { v="pending";    pend++ }
    printf "  %-34s %s\n", inst, v
    total++
  }
  END {
    printf "\n  resolved   %d\n  unresolved %d\n", res, unres
    if (run+pend) printf "  in flight  %d\n", run+pend
    done_n = res+unres
    if (done_n) printf "\n  RESOLVED RATE: %.1f%%  (%d/%d graded)\n", 100*res/done_n, res, done_n
    printf "  total jobs: %d\n", total
  }'
