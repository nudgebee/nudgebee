#!/usr/bin/env bash
# Run terminal-bench against NuBi (llm-server).
#
# Prerequisites:
#   1. NuBi running locally:  cd llm/llm-server && make run
#   2. Docker daemon available (terminal-bench spins up task containers)
#   3. uv venv at llm/benchmark/.venv with terminal-bench installed
#         cd llm/benchmark && uv pip install -r uv.lock 'terminal-bench>=0.2.18'
#
# Required env vars:
#   NUBI_URL         e.g. http://127.0.0.1:8005
#   NUBI_TOKEN       value of the X-ACTION-TOKEN header
#   NUBI_ACCOUNT_ID  cloud_accounts.id where the @tbench agent is installed
#   NUBI_TENANT_ID   tenant id that owns the account
#
# Optional env vars:
#   NUBI_TOKEN_HEADER   Header name (default: X-ACTION-TOKEN)
#   NUBI_USER_ID        User id (default: empty -> tenant-admin path)
#   NUBI_AGENT_NAME     NuBi agent to route to (default: tbench)
#   NUBI_POLL_INTERVAL  Seconds between chat_get polls (default: 2)
#   NUBI_TASK_TIMEOUT   Max seconds per task (default: 1800)
#
# Usage examples:
#
#   # Single task smoke test
#   TASK_ID=hello-world ./tbench/run.sh
#
#   # First N tasks of the core dataset
#   N_TASKS=5 ./tbench/run.sh
#
#   # Full core dataset
#   ./tbench/run.sh

set -euo pipefail

: "${NUBI_URL:?NUBI_URL must be set, e.g. http://127.0.0.1:8005}"
: "${NUBI_TOKEN:?NUBI_TOKEN must be set}"
: "${NUBI_ACCOUNT_ID:?NUBI_ACCOUNT_ID must be set (cloud_accounts.id)}"
: "${NUBI_TENANT_ID:?NUBI_TENANT_ID must be set}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

if [[ ! -x "$BENCH_DIR/.venv/bin/tb" ]]; then
    echo "[nubi-tbench] tb CLI not found at $BENCH_DIR/.venv/bin/tb" >&2
    echo "[nubi-tbench] Run: cd $BENCH_DIR && uv pip install -r uv.lock 'terminal-bench>=0.2.18'" >&2
    exit 1
fi

export PYTHONPATH="$BENCH_DIR:${PYTHONPATH:-}"

TB_ARGS=(
    --dataset "${DATASET:-terminal-bench-core==0.1.1}"
    --agent-import-path "tbench.nubi_agent:NuBiAgent"
)

if [[ -n "${TASK_ID:-}" ]]; then
    # TASK_ID may be a single id or a comma-separated list.
    IFS=',' read -ra _ids <<< "$TASK_ID"
    for _id in "${_ids[@]}"; do
        TB_ARGS+=(--task-id "$_id")
    done
fi

if [[ -n "${N_TASKS:-}" ]]; then
    TB_ARGS+=(--n-tasks "$N_TASKS")
fi

echo "[nubi-tbench] NUBI_URL=$NUBI_URL"
echo "[nubi-tbench] NUBI_ACCOUNT_ID=$NUBI_ACCOUNT_ID"
echo "[nubi-tbench] tb run ${TB_ARGS[*]}"
echo ""

cd "$BENCH_DIR"
exec .venv/bin/tb run "${TB_ARGS[@]}"
