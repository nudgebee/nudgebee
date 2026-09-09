#!/usr/bin/env bash
# ab_test_logs.sh — repeatable A/B comparison between @logs and @logs_v3.
#
# Fires the same query at both agents (N times each, since a single run is
# not reliable evidence — see docs/logs-v3-agent-investigation.md §8/§9 on the
# LLM output-length blow-up that can spike any individual run by 50-150s+
# independent of everything else), then pulls the real structural numbers
# from Postgres for each run instead of trusting wall-clock time alone:
# llm_conversation_agent row count (DB-write overhead) and per-call
# input/output/thinking tokens (flags a blow-up run so it doesn't get read as
# "logs_v3 is slow" when it's actually the unrelated open issue).
#
# Requires: a running llm-server (`go run ./cmd`), all its dependent
# port-forwards, and psql. Reads DB connection info from llm-server's own
# .env (LLM_SERVER_DB_URL) unless AB_TEST_DB_URL is set.
#
# Usage:
#   AB_TEST_TENANT_ID=... AB_TEST_USER_ID=... AB_TEST_ACCOUNT_ID=... \
#     ./ab_test_logs.sh -n 3 -q "get me logs for relay_server"
#
#   Multiple -q flags run multiple queries, each compared independently.
#   Defaults to 1 run per agent and a single generic query if none given.

set -uo pipefail
cd "$(dirname "${BASH_SOURCE[0]}")/.."

SERVER_URL="${AB_TEST_SERVER_URL:-http://localhost:9999}"
TENANT_ID="${AB_TEST_TENANT_ID:?set AB_TEST_TENANT_ID (x-tenant-id)}"
USER_ID="${AB_TEST_USER_ID:?set AB_TEST_USER_ID (x-user-id)}"
ACCOUNT_ID="${AB_TEST_ACCOUNT_ID:?set AB_TEST_ACCOUNT_ID}"
DB_URL="${AB_TEST_DB_URL:-$(grep -E '^LLM_SERVER_DB_URL=' .env 2>/dev/null | cut -d= -f2-)}"

if [ -z "$DB_URL" ]; then
  echo "error: no DB URL — set AB_TEST_DB_URL or ensure LLM_SERVER_DB_URL is in .env" >&2
  exit 1
fi

RUNS=1
QUERIES=()
AGENTS=(logs logs_v3)

while getopts "n:q:a:" opt; do
  case $opt in
    n) RUNS="$OPTARG" ;;
    q) QUERIES+=("$OPTARG") ;;
    a) AGENTS=("$OPTARG") ;;
    *) echo "usage: $0 [-n runs_per_agent] [-q query]... [-a agent_name]..." >&2; exit 1 ;;
  esac
done
[ ${#QUERIES[@]} -eq 0 ] && QUERIES=("get me logs for relay_server")

WORKDIR=$(mktemp -d)
trap 'rm -rf "$WORKDIR"' EXIT

# run_once AGENT QUERY -> prints "wall_seconds<TAB>http_status<TAB>conversation_id"
run_once() {
  local agent="$1" query="$2" resp_file="$3"
  local start end wall status
  start=$(date +%s.%N)
  status=$(curl -s -o "$resp_file" -w "%{http_code}" -X POST "$SERVER_URL/v1/completions/chat" \
    -H "Content-Type: application/json" \
    -H "x-tenant-id: $TENANT_ID" \
    -H "x-user-id: $USER_ID" \
    -d "$(python3 -c "
import json, sys
print(json.dumps({
    'query': '@${agent} ' + sys.argv[1],
    'account_id': '$ACCOUNT_ID',
    'user_id': '$USER_ID',
    'async': False,
}))
" "$query")" \
    --max-time 600) || status="000"
  [ -z "$status" ] && status="000"
  end=$(date +%s.%N)
  wall=$(python3 -c "print(f'{$end - $start:.1f}')")
  local conv_id
  conv_id=$(python3 -c "
import json
try:
    d = json.load(open('$resp_file'))
    print(d.get('data', {}).get('conversation_id', ''))
except Exception:
    print('')
")
  printf '%s\t%s\t%s\n' "$wall" "$status" "$conv_id"
}

# db_summary CONVERSATION_ID -> prints "rows<TAB>total_in<TAB>total_out<TAB>max_out<TAB>blowup_flag"
db_summary() {
  local conv_id="$1"
  [ -z "$conv_id" ] && { echo -e "?\t?\t?\t?\tno-conv-id"; return; }
  psql "$DB_URL" -t -A -F $'\t' -c "
    WITH rows AS (
      SELECT count(*) AS n FROM llm_conversation_agent WHERE conversation_id = '$conv_id'
    ), toks AS (
      SELECT
        coalesce(sum(input_tokens), 0) AS total_in,
        coalesce(sum(output_tokens), 0) AS total_out,
        coalesce(max(output_tokens), 0) AS max_out
      FROM llm_conversation_token_usage
      WHERE conversation_id = '$conv_id'
        AND agent_name NOT IN ('summary_agent','session_extractor','context_memories_extractions','conversation_suggestion','memory_compose','webhook_subject_name_extractor')
    )
    SELECT rows.n, toks.total_in, toks.total_out, toks.max_out,
           CASE WHEN toks.max_out > 1000 THEN 'BLOWUP@' || toks.max_out ELSE 'clean' END
    FROM rows, toks;
  " 2>/dev/null || echo -e "?\t?\t?\t?\tdb-error"
}

for query in "${QUERIES[@]}"; do
  echo "================================================================"
  echo "QUERY: $query"
  echo "================================================================"
  printf '%-8s %-4s %8s %6s %6s %9s %9s %9s  %s\n' \
    "agent" "run" "wall(s)" "http" "rows" "tok_in" "tok_out" "max_out" "flag"
  for agent in "${AGENTS[@]}"; do
    total_wall=0
    for i in $(seq 1 "$RUNS"); do
      resp_file="$WORKDIR/${agent}_${i}.json"
      IFS=$'\t' read -r wall status conv_id <<< "$(run_once "$agent" "$query" "$resp_file")"
      IFS=$'\t' read -r rows tin tout tmax flag <<< "$(db_summary "$conv_id")"
      printf '%-8s %-4s %8s %6s %6s %9s %9s %9s  %s\n' \
        "$agent" "$i" "$wall" "$status" "$rows" "$tin" "$tout" "$tmax" "$flag"
      total_wall=$(python3 -c "print($total_wall + $wall)")
    done
    avg=$(python3 -c "print(f'{$total_wall / $RUNS:.1f}')")
    printf '%-8s %-4s %8s   (avg over %s run(s))\n' "$agent" "avg" "$avg" "$RUNS"
    echo
  done
done
