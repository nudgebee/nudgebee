#!/usr/bin/env bash
# Runs the cross-account e2e suite (#38588, #38596) against a live environment
# and prints pass/fail per test with the conversation each one preserved.
#
# Needs llm-server/.env (TEST_ACCOUNT, TEST_USER, TEST_TENANT, DB and service
# URLs) and the services it points at reachable, e.g. port-forwards to dev:
#   services-server :8000, cloud-collector-server :8001, relay-server :8006,
#   postgres :5433.
# Account names default to dev's (k8s-dev, k8s-prod, aws-prod, ...); override
# with the TEST_DELEGATE_* / TEST_OWN_* variables the tests read.
#
# Usage: scripts/e2e-cross-account.sh [go test -run regex]
# With a regex, it runs once in each package the suite covers.
# Writes logs to $E2E_LOG_DIR (default: a temp dir) and exits non-zero if any
# test failed. Some tests write throwaway objects (configmaps, a temp table,
# a /tmp file) and remove them.
set -uo pipefail
cd "$(dirname "$0")/.."

if [[ ! -f .env ]]; then
  echo "llm-server/.env not found" >&2
  exit 2
fi
set -a
# shellcheck disable=SC1091
source .env 2>/dev/null
set +a

for url in "${SERVICE_API_SERVER_URL:-}" "${CLOUD_COLLECTOR_SERVER_URL:-}" "${RELAY_SERVER_ENDPOINT:-}"; do
  [[ -z "$url" ]] && continue
  hostport=${url#*://}; hostport=${hostport%%/*}
  host=${hostport%:*}; port=${hostport##*:}
  if [[ "$host" == "$hostport" ]]; then
    [[ "$url" == https://* ]] && port=443 || port=80
  fi
  timeout_opt=-w
  [[ "$OSTYPE" == darwin* ]] && timeout_opt=-G # macOS: -G is the connect timeout
  if ! nc -z "$timeout_opt" 2 "$host" "$port" 2>/dev/null; then
    echo "not reachable: $url (start the port-forwards first)" >&2
    exit 2
  fi
done

logs=${E2E_LOG_DIR:-$(mktemp -d -t e2e-cross-account.XXXXXX)}
filter=${1:-}

# package ; -run regex ; timeout  (';' because the regexes use '|')
groups=(
  "./agents/;^(TestNaturalQuestion_|TestCrossAccountScenario_);150m"
  "./agents/;^(TestE2E_|TestWrapperAgent_|TestPrometheusExecute_PerCallTarget|TestTargetedChild_|TestRegression_MultiTargetSession|TestShellExecute_AwsRegionLoopWithoutTarget);150m"
  "./agents/core/;^TestCallNbTool_(VMAccount|IntegrationOnAnother|NamedCloudAccount);60m"
  "./tools/;^(TestCrossAccount|TestShellTargetLive|TestVMToolsLive);60m"
)

echo "logs: $logs"
i=0
pids=()
seen=" "
for g in "${groups[@]}"; do
  IFS=';' read -r pkg run timeout <<<"$g"
  if [[ -n "$filter" ]]; then
    # One run per package: the regex may match tests in any of them.
    [[ "$seen" == *" $pkg "* ]] && continue
    seen+="$pkg "
    run="$filter"
  fi
  i=$((i + 1))
  go test -tags e2e "$pkg" -run "$run" -count=1 -v -timeout "$timeout" >"$logs/group$i.log" 2>&1 &
  pids+=($!)
done
failed=0
for pid in "${pids[@]}"; do
  wait "$pid" || failed=1
done

printf '\n%-6s %-70s %s\n' RESULT TEST CONVERSATION
ran=0
for log in "$logs"/group*.log; do
  conv=""
  if grep -q "no tests to run" "$log"; then
    continue # the regex matched nothing in this package
  fi
  if ! grep -qE '^--- (PASS|FAIL|SKIP)' "$log"; then
    # go test failed before running anything (build error, bad flag).
    echo "ERROR  $(basename "$log"): no test ran:"
    tail -5 "$log" | sed 's/^/       /'
    failed=1
  fi
  while IFS= read -r line; do
    if [[ "$line" =~ conversationId=([0-9a-f-]{36}) ]]; then
      conv=${BASH_REMATCH[1]}
    elif [[ "$line" =~ ^---\ (PASS|FAIL|SKIP):\ ([^[:space:]]+) ]]; then
      printf '%-6s %-70s %s\n' "${BASH_REMATCH[1]}" "${BASH_REMATCH[2]}" "$conv"
      ran=1
      [[ ${BASH_REMATCH[1]} == FAIL ]] && failed=1
      conv=""
    fi
  done <"$log"
  # The RAG server is not forwarded; its refusals are expected noise.
  if grep "connection refused" "$log" | grep -qv "rag:"; then
    echo "note: $(basename "$log") has 'connection refused' lines; check the forwards before trusting failures"
  fi
done
if [[ $ran == 0 ]]; then
  echo "no test ran" >&2
  failed=1
fi
exit $failed
