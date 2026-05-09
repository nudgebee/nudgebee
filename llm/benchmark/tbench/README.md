# NuBi × terminal-bench

A [terminal-bench](https://www.tbench.ai/) `BaseAgent` adapter that drives
NuBi (`llm/llm-server`) via the `@tbench` custom agent and the `shell_execute`
client-tool callback protocol. Lets us measure NuBi's general-agent
capabilities (planning, recovery, tool use) on a standardized public
benchmark.

## How it works

```
tb harness → NuBiAgent → POST /v1/completions/chat (@tbench, async)
                       ← conversation_id

   loop:
   NuBiAgent → POST /v1/completions/chat_get
             ← {status: WAITING_FOR_CLIENT_TOOL,
                agent_step_response: [{tool_id, tool_name: shell_execute, tool_input: {command}}]}
   NuBiAgent → run command in tb's tmux/Docker session, capture pane
   NuBiAgent → POST /v1/completions/client-tool-result
                  {results: [{tool_id, result: <pane>, status: SUCCESS}]}
   NuBi resumes ↻

   until status ∈ {COMPLETED, FAILED, TERMINATED}
```

## Prerequisites

1. **NuBi running locally** — `cd llm/llm-server && make run` (default port 8005 or 9999).
2. **`@tbench` agent installed in NuBi** — created via the NuBi UI as a custom agent
   under your tenant, with `shell_execute` available as a tool.
3. **Docker daemon reachable** — terminal-bench builds a fresh container per task.
4. **`ghcr.io` reachable** — task images are pulled from GitHub Container Registry.
   See [Troubleshooting](#troubleshooting) if `docker compose build` times out.
5. **uv venv** with terminal-bench installed:
   ```bash
   cd llm/benchmark
   uv pip install -r uv.lock 'terminal-bench>=0.2.18'
   ```

## Required env vars

| Var | What |
|-----|------|
| `NUBI_URL` | NuBi base URL, e.g. `http://127.0.0.1:8005` |
| `NUBI_TOKEN` | Value of the `X-ACTION-TOKEN` header (LLM_SERVER_TOKEN in `.env`) |
| `NUBI_ACCOUNT_ID` | `cloud_accounts.id` where the `@tbench` agent is installed |
| `NUBI_TENANT_ID` | tenant id that owns the account |

## Optional env vars

| Var | Default | What |
|-----|---------|------|
| `NUBI_TOKEN_HEADER` | `X-ACTION-TOKEN` | Auth header name |
| `NUBI_USER_ID` | _(empty)_ | Empty falls back to tenant-admin path (no api-server callout) |
| `NUBI_AGENT_NAME` | `tbench` | Agent the query is routed to (`@<name>` prefix) |
| `NUBI_POLL_INTERVAL` | `2` | Seconds between `chat_get` polls |
| `NUBI_CMD_TIMEOUT` | `600` | Per-shell-command wallclock cap (`SIGINT` on timeout, partial output returned) |
| `NUBI_TASK_TIMEOUT` | `1800` | Outer cap on the whole trial |

## Running tests

All commands assume `cd llm/benchmark`. Common env-var prelude (export once):

```bash
export NUBI_URL=http://127.0.0.1:8005
export NUBI_TOKEN=<X-ACTION-TOKEN value from llm-server .env>
export NUBI_ACCOUNT_ID=<cloud_accounts.id>
export NUBI_TENANT_ID=<tenant id>
```

### Single task by id

```bash
TASK_ID=hello-world ./tbench/run.sh
```

### Multiple specific tasks (comma-separated)

```bash
TASK_ID=hello-world,fix-pandas-version,openssl-selfsigned-cert ./tbench/run.sh
```

### First N tasks of the dataset

```bash
N_TASKS=5 ./tbench/run.sh
```

### All 80 tasks of `terminal-bench-core==0.1.1`

```bash
./tbench/run.sh
```

### Different dataset version

```bash
DATASET=terminal-bench-core==head ./tbench/run.sh   # the latest, may be unstable
DATASET=usaco==head ./tbench/run.sh                 # USACO programming benchmark
```

List available datasets: `.venv/bin/tb datasets list`.

### Flags `run.sh` doesn't expose

For anything else (`--n-concurrent-trials`, `--exclude-task-id`, `--no-cleanup`, etc.)
call `tb run` directly:

```bash
.venv/bin/tb run \
  --dataset terminal-bench-core==0.1.1 \
  --agent-import-path tbench.nubi_agent:NuBiAgent \
  --task-id fix-pandas-version \
  --n-concurrent-trials 4 \
  --no-cleanup
```

`tb run --help` lists all options.

## Reading results

Each invocation creates `runs/<run-id>/`:

```
runs/2026-05-04__08-15-42/
  results.json                       # aggregate accuracy, resolved/unresolved counts
  run_metadata.json
  run.log
  hello-world/
    hello-world.1-of-1.<run-id>/
      results.json                   # per-trial: is_resolved, parser_results, timings
      commands.txt                   # every command NuBi typed
      sessions/agent.cast            # asciinema recording of the agent session
      sessions/tests.cast            # asciinema recording of the test pass
      panes/                         # tmux pane snapshots (post-agent, post-test, …)
      agent-logs/nubi_agent.log      # adapter-level log (poll cycles, tool calls)
```

Quick tally for a run:

```bash
for d in runs/<run-id>/*/*/; do
  jq -r '"\(.task_id): \(if .is_resolved then "✓" else "✗" end) \(.failure_mode)"' "$d/results.json" 2>/dev/null
done
```

## Troubleshooting

### `docker compose build` fails with `context deadline exceeded` against `ghcr.io`

Your DNS may be returning a CDN endpoint your ISP can't route to. Test:

```bash
dig +short ghcr.io
# if you see something like 20.207.73.86 (Azure) and curl times out:
curl -m 5 -s -o /dev/null -w "HTTP %{http_code} %{time_total}s\n" --resolve ghcr.io:443:140.82.114.33 https://ghcr.io/v2/
# 401 in <2s = that IP works
```

Workaround until the CDN IP rotates:

```bash
echo '140.82.114.33  ghcr.io' | sudo tee -a /etc/hosts
docker pull ghcr.io/laude-institute/t-bench/python-3-13:latest   # one-time prefetch
```

### `401 Unauthorized` from `/v1/completions/chat`

Auth header name doesn't match. Default is `X-ACTION-TOKEN`, set by
`LLM_SERVER_TOKEN_HEADER` in `llm-server/.env`. The token value is
`LLM_SERVER_TOKEN`. To override:

```bash
export NUBI_TOKEN_HEADER=Authorization
export NUBI_TOKEN="Bearer <token>"
```

### `unable to identify tenant`

llm-server couldn't resolve `account_id → tenant_id` from `cloud_accounts`.
Confirm:

```sql
SELECT id, account_name, tenant FROM cloud_accounts WHERE id = '<NUBI_ACCOUNT_ID>';
```

If the row exists, also send the tenant explicitly via the `x-tenant-id`
header — the adapter does this automatically once `NUBI_TENANT_ID` is set.

### `agent_name=help` instead of `tbench` in the response

The `@tbench` agent isn't installed for this account. Verify:

```sql
SELECT a.name, i.account_id::text
FROM llm_agents a JOIN llm_agents_installation i ON a.id::text = i.agent_id::text
WHERE a.name = 'tbench';
```

The `account_id` returned must match `NUBI_ACCOUNT_ID`.

### Trial times out at ~1200s even after bumping `NUBI_TASK_TIMEOUT`

Each task ships its own `task.yaml: max_agent_timeout_sec` that tb enforces
independently. Our `NUBI_TASK_TIMEOUT` is only the outer safety net.

### `parse_error` / `No such container: <id>`

The agent killed the trial container mid-run (e.g. `shutdown`, `kill -9 1`,
something that crashes the entrypoint). tb tries to exec into the dead
container for the test pass and 404s. Inspect
`runs/<run-id>/<task>/<trial>/agent-logs/nubi_agent.log` and `commands.txt`
to see the last commands NuBi issued.

## Cost / quota notes

Every action is an LLM call against whatever provider NuBi is configured for
(currently `googleai/gemini-3-flash-preview` by default). A full 80-task run
issues roughly 800–3000 LLM calls and runs 2–6 hours wall time. Don't kick
off the full set casually.
