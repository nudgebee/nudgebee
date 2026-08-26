#!/usr/bin/env bash
# Run the official ORCA-Bench Harbor dataset with NuBi as a custom agent.

set -euo pipefail

: "${NUBI_URL:?NUBI_URL must be set and reachable from the host running Harbor}"
: "${NUBI_TOKEN:?NUBI_TOKEN must be set}"
: "${NUBI_ACCOUNT_ID:?NUBI_ACCOUNT_ID must be set}"
: "${NUBI_TENANT_ID:?NUBI_TENANT_ID must be set}"
: "${OPENAI_API_KEY:?OPENAI_API_KEY must be set for the official ORCA verifier}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCH_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ORCA_REPO="${ORCA_BENCH_DIR:-$BENCH_DIR/.orca-bench-upstream}"
ORCA_COMMIT="${ORCA_BENCH_COMMIT:-b8ab93ae5d2716ff8e7190196151acd4a15bf3d7}"
SNAPSHOT_IMAGE="${SNAPSHOT_IMAGE:-orcabench/sre-otel-snapshot:data-0418-harbor-template}"
SNAPSHOT_PLATFORM="${SNAPSHOT_PLATFORM:-linux/amd64}"
CACHE_ROOT="${ORCA_SNAPSHOT_CACHE:-$HOME/.cache/orca-bench}"

for command in git docker uv; do
    if ! command -v "$command" >/dev/null 2>&1; then
        echo "[nubi-orca] required command not found: $command" >&2
        exit 1
    fi
done
docker info >/dev/null 2>&1 || {
    echo "[nubi-orca] Docker daemon is not reachable" >&2
    exit 1
}

if [[ ! -d "$ORCA_REPO/.git" ]]; then
    git clone https://github.com/ORCA-bench/ORCA-bench.git "$ORCA_REPO"
fi
git -C "$ORCA_REPO" fetch --quiet origin "$ORCA_COMMIT"
git -C "$ORCA_REPO" checkout --quiet --detach "$ORCA_COMMIT"

(
    cd "$ORCA_REPO"
    uv sync --frozen
)

image_id="$(docker image inspect --format '{{.Id}}' "$SNAPSHOT_IMAGE" 2>/dev/null || true)"
if [[ -z "$image_id" ]]; then
    docker pull --platform "$SNAPSHOT_PLATFORM" "$SNAPSHOT_IMAGE"
    image_id="$(docker image inspect --format '{{.Id}}' "$SNAPSHOT_IMAGE")"
fi
cache_key="${image_id#sha256:}"
cache_dir="$CACHE_ROOT/$cache_key"
if [[ ! -f "$cache_dir/.complete" ]]; then
    mkdir -p "$cache_dir"
    container_id="$(docker create --platform "$SNAPSHOT_PLATFORM" "$SNAPSHOT_IMAGE")"
    trap 'docker rm -f "$container_id" >/dev/null 2>&1 || true' EXIT
    docker cp "$container_id:/app/." "$cache_dir/"
    docker rm "$container_id" >/dev/null
    trap - EXIT
    touch "$cache_dir/.complete"
fi

mounts="$(printf '[{"type":"bind","source":"%s","target":"%s","read_only":true}]' "$cache_dir" "$cache_dir")"
args=(
    run
    --mounts-json "$mounts"
    -c job-config.yaml
    -d orca-bench/orca-bench@latest
    -a orcabench.nubi_agent:NuBiHarborAgent
    --ae 'NUBI_URL=${NUBI_URL}'
    --ae 'NUBI_TOKEN=${NUBI_TOKEN}'
    --ae 'NUBI_ACCOUNT_ID=${NUBI_ACCOUNT_ID}'
    --ae 'NUBI_TENANT_ID=${NUBI_TENANT_ID}'
    --ve 'OPENAI_API_KEY=${OPENAI_API_KEY}'
    --n-concurrent "${N_CONCURRENT_TRIALS:-1}"
)

for optional in NUBI_TOKEN_HEADER NUBI_USER_ID NUBI_AGENT_NAME NUBI_POLL_INTERVAL NUBI_CMD_TIMEOUT NUBI_TASK_TIMEOUT NUBI_MAX_TOOL_OUTPUT_CHARS NUBI_LLM_PROVIDER NUBI_LLM_MODEL; do
    if [[ -n "${!optional:-}" ]]; then
        args+=(--ae "$optional=\${$optional}")
    fi
done
if [[ -n "${OPENAI_BASE_URL:-}" ]]; then
    args+=(--ve 'OPENAI_BASE_URL=${OPENAI_BASE_URL}')
fi
if [[ -n "${TASK_ID:-}" ]]; then
    IFS=',' read -ra task_ids <<< "$TASK_ID"
    for task_id in "${task_ids[@]}"; do
        args+=(--include-task-name "$task_id")
    done
fi
if [[ "${N_TASKS:-1}" != "all" ]]; then
    args+=(--n-tasks "${N_TASKS:-1}")
fi

export PYTHONPATH="$BENCH_DIR:${PYTHONPATH:-}"
export SNAPSHOT_CACHE_HOST_DIR="$cache_dir"
export DOCKER_DEFAULT_PLATFORM="$SNAPSHOT_PLATFORM"
echo "[nubi-orca] ORCA commit: $ORCA_COMMIT"
echo "[nubi-orca] snapshot: $SNAPSHOT_IMAGE"
echo "[nubi-orca] snapshot platform: $SNAPSHOT_PLATFORM"
echo "[nubi-orca] cache: $cache_dir"
echo "[nubi-orca] concurrency: ${N_CONCURRENT_TRIALS:-1}"
echo "[nubi-orca] tasks: ${N_TASKS:-1}"
cd "$ORCA_REPO"
exec uv run harbor "${args[@]}" "$@"
