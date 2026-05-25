# LLM Agent Benchmark System

## Overview

Automated evaluation framework for LLM agents. Runs test scenarios against live agents, scores responses using RAGAS metrics, and generates reports with accuracy, latency, cost, and token usage breakdowns.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                        Streamlit Dashboard                         │
│              (dashboard.py — port 8501)                            │
│   ┌──────────┐ ┌──────────────┐ ┌────────────┐ ┌───────────────┐  │
│   │  Active   │ │     Run      │ │    View    │ │    Compare    │  │
│   │  Runs     │ │  Benchmark   │ │   Report   │ │    Reports    │  │
│   └──────────┘ └──────────────┘ └────────────┘ └───────────────┘  │
└────────────────────────────┬────────────────────────────────────────┘
                             │ HTTP
┌────────────────────────────▼────────────────────────────────────────┐
│                    FastAPI Benchmark Server                         │
│                 (app.py — port 9999)                                │
│                                                                     │
│  Controllers:                                                       │
│    agent_benchmark_controller.py  — /agent-benchmark/*              │
│    health_controller.py           — /health                         │
│    ragas.py                       — /ragas/*                        │
│                                                                     │
│  Utils:                                                             │
│    run_manager.py  — Run lifecycle (create/pause/resume/stop/rerun) │
│    llm_client.py   — Call LLM server, extract responses             │
│    db_utils.py     — PostgreSQL connection, migrations              │
│    email_utils.py  — Send report emails                             │
│                                                                     │
│  Models:                                                            │
│    BenchmarkRun         — Run state, config, progress               │
│    BenchmarkTestResult  — Per-test scores, tokens, errors           │
└────────────────────────────┬────────────────────────────────────────┘
                             │ subprocess (pytest)
┌────────────────────────────▼────────────────────────────────────────┐
│                     Test Runner (pytest)                             │
│              llm/agents/common/benchmark.py                         │
│                                                                     │
│  1. Load config.yaml for the agent                                  │
│  2. Discover fixtures (with tag/index filtering)                    │
│  3. Run before_test hooks (k8s manifests, shell commands)           │
│  4. Call LLM → extract answer → retry on transient failures         │
│  5. RAGAS evaluation (answer_similarity, answer_relevancy)          │
│  6. Planner quality scoring (orchestrator_quality metric)           │
│  7. Store results to DB progressively                               │
│  8. Run after_test hooks (cleanup)                                  │
│  9. Agent-specific enrichment (RCA rubric, signal analysis)         │
└─────────────────────────────────────────────────────────────────────┘
```

## Directory Structure

```
llm/benchmark/
├── app.py                          # FastAPI server entrypoint
├── dashboard.py                    # Streamlit UI
├── Dockerfile                      # Multi-stage build (server + dashboard)
├── pyproject.toml                  # Dependencies (uv)
│
├── benchmark_server/
│   ├── controllers/
│   │   └── agent_benchmark_controller.py   # API endpoints
│   ├── models/
│   │   └── benchmark_run.py                # SQLAlchemy models
│   ├── utils/
│   │   ├── run_manager.py                  # Run lifecycle state machine
│   │   ├── llm_client.py                   # LLM API client
│   │   ├── db_utils.py                     # DB init + migrations
│   │   └── email_utils.py                  # Report email sender
│   └── common/
│       └── llm.py                          # RAGAS LLM/embeddings setup
│
└── llm/agents/
    ├── common/                     # Shared test infrastructure
    │   ├── benchmark.py            # Unified pytest test file (all agents)
    │   ├── fixtures.py             # Test case discovery + tag filtering
    │   ├── runner.py               # Legacy runner (non-pytest path)
    │   ├── lifecycle.py            # before_test/after_test + scenario resolution
    │   ├── metrics.py              # Token/tool/planner metrics collection
    │   ├── orchestrator_metric.py  # Planner quality RAGAS metric
    │   └── compare_reports.py      # Report comparison engine
    │
    ├── aws/                        # AWS agent
    │   ├── config.yaml             # Agent config (tool_names, hooks)
    │   ├── conftest.py             # Session-scoped infra deploy/nuke
    │   ├── aws-agent-test/         # CloudFormation scenarios
    │   │   ├── bootstrap.yaml      # Shared VPC/S3 (deploy once)
    │   │   ├── scenarios/          # easy/ medium/ hard/ CF templates
    │   │   └── scripts/            # deploy.sh, nuke.sh
    │   └── fixtures/               # Test cases (YAML)
    │
    ├── azure/                      # Azure agent (same pattern as AWS)
    │   ├── config.yaml
    │   ├── conftest.py
    │   ├── azure-agent-test/       # ARM templates
    │   └── fixtures/
    │
    ├── kubectl/                    # K8s command agent
    │   ├── config.yaml
    │   └── fixtures/
    │
    └── <agent>/                    # promql, loki, trace, events, rca,
        ├── config.yaml             # datadog, es, kubectl_log, recommendations
        └── fixtures/
```

## Agent Configuration

Each agent has a `config.yaml` that defines its behavior:

```yaml
# config.yaml
agent: aws                           # Agent name (used in @agent prefix)
tool_names:                           # Tools for answer extraction
  - aws_execute
db_tool_name: kubectl_execute         # Optional: fetch command from DB
prefix_query: true                    # Prefix query with @agent (default: true)
max_retries: 2                        # Retry count for transient failures

# Optional Python hooks (dotted import paths)
before_query: pkg.mod.func           # Called before each LLM query
after_query: pkg.mod.func            # Called after each LLM query
answer_extractor: pkg.mod.func       # Custom answer extraction
result_enricher: pkg.mod.func        # Per-result scoring (RCA rubric, etc.)

# Extra tool confirmations (auto-approve during benchmarks)
extra_tool_confirmations:
  aws_observability: "yes"
```

## Test Fixtures

Each test is a directory with a `test_case.yaml`:

```
fixtures/
├── 001_cluster_info/
│   └── test_case.yaml
├── 002_list_pods/
│   ├── test_case.yaml
│   └── manifests.yaml          # Optional: k8s resources for before_test
└── ...
```

### test_case.yaml Format

```yaml
user_prompt: "give me cluster information"
expected_output:
  - "kubectl cluster-info"
tags:                               # Used for filtering in UI
  - command
  - easy
  - kubernetes
scenario: E11                       # Links to cloud infra scenario (AWS/Azure only)
type: rca                           # Optional: command | rca | question-answer
agent: kubectl                      # Optional: override agent for this test
before_test: |                      # Optional: shell commands run before test
  kubectl apply -f manifests.yaml
  sleep 30
after_test: |                       # Optional: shell commands run after test
  kubectl delete -f manifests.yaml
setup_timeout: 300                  # Optional: max seconds for before_test (default 300)
```

## Test Selection & Filtering

Tests can be filtered in multiple ways (applied in order):

| Method | How | Example |
|---|---|---|
| **Tags** | Comma-separated, test must match at least one | `TAG_FILTER=easy,medium` |
| **Indices** | Specific positions in the sorted fixture list | `TEST_INDICES=0,2,5-7` |
| **Max tests** | First N tests after other filters | `MAX_TESTS=10` |
| **Skip indices** | Skip completed tests (used for resume) | `SKIP_INDICES=0,1,3` |
| **pytest -k** | Pytest keyword expression | `test_filter=cluster` |

Filter order: tags → indices/max_tests → skip_indices

All filters are available in the Dashboard UI (Run Benchmark page) and via API.

## Infrastructure Lifecycle (AWS/Azure)

AWS and Azure agents have RCA test scenarios that require live cloud infrastructure.

### How It Works

```
                  Tag Filter: "easy"
                       │
                       ▼
              ┌────────────────┐
              │ find_test_cases │  ← filters fixtures by tag
              └───────┬────────┘
                      │ 13 easy fixtures
                      ▼
         ┌─────────────────────────┐
         │resolve_scenarios_from   │  ← reads scenario: field from each fixture
         │        fixtures         │
         └───────────┬─────────────┘
                     │ scenarios: [E01, E02, ..., E15]
                     ▼
         ┌─────────────────────────┐
         │  conftest.py session    │
         │  fixture (autouse)      │
         │                         │
         │  1. Deploy bootstrap    │  ← shared VPC/S3 (idempotent)
         │  2. Deploy E01..E15     │  ← only needed scenarios
         │     in Broken mode      │
         │  3. yield → tests run   │
         │  4. Nuke all stacks     │  ← automatic cleanup
         └─────────────────────────┘
```

- `DEPLOY_INFRA=true` is set by default when running from the server
- Only scenarios referenced by the filtered test set are deployed
- `SKIP_NUKE=true` keeps infra up after tests (for debugging)
- Agents without a conftest (kubectl, promql, etc.) ignore these env vars

### Difficulty Tags

AWS fixtures with a `scenario:` field have difficulty tags derived from the scenario prefix:

| Prefix | Tag | Example |
|---|---|---|
| E01-E22 | `easy` | Lambda missing permission |
| M01-M25 | `medium` | ECS stale task definition |
| H01-H21 | `hard` | Multi-service cascading failure |

Select these tags in the UI to run only easy/medium/hard scenarios.

## Per-Test Hooks (All Agents)

For agents that need per-test setup/teardown (k8s manifests, feature flags):

```
before_test (shell)     →  before_query (Python)  →  LLM call  →  after_query (Python)  →  after_test (shell)
   │                            │                                        │                        │
   ├─ kubectl apply             ├─ enable feature flag                   ├─ disable flag          ├─ kubectl delete
   ├─ wait for ready            └─ set config                           └─ reset config          └─ cleanup
   └─ seed data
```

- `before_test`/`after_test`: Shell commands defined in test_case.yaml
- `before_query`/`after_query`: Python callables defined in config.yaml
- If `before_test` fails, the test is marked `setup_failed` and skipped

## Run Lifecycle

```
    create_run()
        │
        ▼
    RUNNING ──pause──► PAUSED ──resume──► RUNNING
        │                                     │
        ├──stop──► STOPPED                    │
        │              │                      │
        │          restart (skip completed)    │
        │              │                      │
        ▼              ▼                      ▼
    COMPLETED     RUNNING (resume)        COMPLETED
        │
        ├── rerun (fresh, delete old results)
        └── re-evaluate (re-score existing results with RAGAS)
```

Phases within RUNNING: `INITIALIZING → RUNNING_TESTS → EVALUATING → GENERATING_REPORT → SENDING_REPORT`

## API Endpoints

### Benchmark Execution

| Method | Path | Description |
|---|---|---|
| `POST` | `/agent-benchmark/run` | Start a new benchmark run |
| `POST` | `/agent-benchmark/{run_id}/pause` | Pause after current test |
| `POST` | `/agent-benchmark/{run_id}/resume` | Resume paused run |
| `POST` | `/agent-benchmark/{run_id}/stop` | Stop and generate partial report |
| `POST` | `/agent-benchmark/{run_id}/rerun` | Fresh rerun (deletes old results) |
| `POST` | `/agent-benchmark/{run_id}/restart` | Resume failed run (skip completed) |
| `POST` | `/agent-benchmark/{run_id}/re-evaluate` | Re-score existing results |
| `DELETE` | `/agent-benchmark/{run_id}` | Delete run and results |

### Data & Reports

| Method | Path | Description |
|---|---|---|
| `GET` | `/agent-benchmark/agents` | List agents with fixture counts |
| `GET` | `/agent-benchmark/agents/{name}/tags` | List tags for an agent |
| `GET` | `/agent-benchmark/runs/list` | List all runs |
| `GET` | `/agent-benchmark/{run_id}/status` | Run status and progress |
| `GET` | `/agent-benchmark/{run_id}/report` | Download JSON report |
| `POST` | `/agent-benchmark/compare-runs` | Compare two runs |
| `DELETE` | `/agent-benchmark/runs/cleanup` | Remove completed run state files |

### Run Request Payload

```json
{
    "account_id": "acc-123",
    "user_id": "user-456",
    "tenant_id": "tenant-789",
    "agent": "aws",
    "max_tests": 5,
    "test_indices": "0,2,5-7",
    "tag_filter": "easy,rca",
    "test_filter": "cluster",
    "cc_emails": ["user@example.com"]
}
```

Agent can also be an object for tool config: `{"name": "aws", "tool_config": "dev-new"}`

## Scoring Metrics

| Metric | Range | Description |
|---|---|---|
| Answer Similarity | 0-100% | Semantic similarity between actual and expected answer |
| Answer Relevancy | 0-100% | How relevant the answer is to the question |
| Planner Relevancy | 0-100% | Quality of the orchestrator's plan (1-5 rubric → 0-100) |
| Overall Accuracy | 0-100% | Average of similarity + relevancy |

Additional per-test metrics: latency, cost, token usage (input/output/cache), tool calls (total/successful), model names.

## Required Services

The benchmark server is **not standalone**. To run it (locally or in
production) the following Nudgebee services must be reachable:

| Service | Why benchmark needs it | Configured via |
|---|---|---|
| **PostgreSQL (app DB)** | Persists runs, test results, run metadata. Reads the shared `users` / `tenant_users` / `user_roles` / `cloud_accounts` tables for authentication and authorization (the same schema the main UI app uses). | `APP_DATABASE_URL` |
| **`llm-server`** | The agent under test. Every benchmark fixture sends a question here and scores the response. AWS/Azure RCA fixtures also rely on its tool-config endpoint. | `LLM_SERVER_URL` |
| **`notifications-server`** | Renders and sends the email magic-link template (`magic_link.html`) used for browser sign-in, plus benchmark completion-report emails when SMTP is delegated to it. Mirrors the call signature used by the main UI app's NextAuth `EmailProvider`. | `NOTIFICATION_SERVICE_URL` |
| **SMTP relay** | Used by `email_utils.send_email_async` for benchmark completion reports (separate from the magic-link path, which goes through notifications-server). Optional for "no email reports" mode. | `EMAIL_SERVER_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `EMAIL_FROM` |
| **Main UI app (Next.js)** | Mints the RS256 JWTs that programmatic clients present as `Authorization: Bearer <jwt>`. The benchmark server only verifies them — it does not issue them. The UI app and benchmark server share `NEXTAUTH_PRIVATE_KEY` for this. Browser sign-in does **not** depend on the UI app being up. | `NEXTAUTH_PRIVATE_KEY` |

**Authentication flow at a glance:**
- **Browser sign-in:** user enters email at `/signin` → benchmark server asks `notifications-server` to send the magic-link email → user clicks the link → benchmark server mints its own RS256-signed session cookie (using `NEXTAUTH_PRIVATE_KEY`).
- **Programmatic / API:** caller presents a session JWT (issued by the main UI app) in `Authorization: Bearer <jwt>`. Benchmark server validates it against `NEXTAUTH_PRIVATE_KEY`.
- **Authorization (which tenants / accounts a user can act on):** fetched from the app DB on first request after login, cached in-process per user. The session JWT carries identity only — no roles, no tenants — so the JWT stays small and RBAC changes pick up without the user re-logging-in.

## Environment Variables

| Variable | Required | Description |
|---|---|---|
| `APP_DATABASE_URL` | Yes | PostgreSQL connection string (app DB). |
| `LLM_SERVER_URL` | Yes | `llm-server` base URL — the agent under test. |
| `NOTIFICATION_SERVICE_URL` | Yes (browser sign-in) | `notifications-server` base URL. Default `http://notifications:80`. Required for magic-link emails. |
| `NEXTAUTH_PRIVATE_KEY` | Yes (auth) | RSA private key (PEM). Same value the main UI app uses; benchmark signs session JWTs with it and verifies API-client JWTs with the matching public key. |
| `MAGIC_LINK_TTL_SECONDS` | No | Magic-link token lifetime (default: 600 = 10 min, mirrors UI app's NextAuth EmailProvider). |
| `SESSION_MAX_AGE_HOURS` | No | Session cookie lifetime in hours (default: 24). |
| `AUTHZ_CACHE_TTL_SECONDS` | No | In-process authz cache TTL (default: 86400 = 24h). Lower for faster RBAC propagation, raise for fewer DB queries. |
| `EMAIL_SERVER_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `EMAIL_FROM` | No | SMTP relay for benchmark completion-report emails (separate from magic-link, which uses notifications-server). |
| `BENCHMARK_PORT` | No | Server port (default: 9999). |
| `DASHBOARD_PORT` | No | Legacy Streamlit dashboard port (default: 8501) — the bundled HTML dashboard at `/` is preferred and has no separate port. |
| `BENCHMARK_API_URL` | No | URL the legacy Streamlit dashboard uses to reach the server (default: http://localhost:9999). |
| `UVICORN_WORKERS` | No | Server worker count (default: 2). |
| `ROOT_PATH` | No | Reverse-proxy mount prefix (e.g. `/benchmark`) so `request.url_for(...)` generates correct absolute URLs for magic-link callbacks behind a proxy. |
| `ACCOUNT_ID` | Test | Account for LLM calls when running pytest directly (not via server). |
| `USER_ID` | Test | User for LLM calls when running pytest directly. |
| `TENANT_ID` | Test | Tenant for LLM calls when running pytest directly. |
| `DEPLOY_INFRA` | No | Deploy cloud infra before tests (default: true from server). |
| `SKIP_NUKE` | No | Keep infra after tests (default: false). |

## Running Locally

### Server + Dashboard

```bash
cd llm/benchmark

# Generate/update uv.lock (after changing pyproject.toml)
uv pip compile pyproject.toml -o uv.lock

# Install dependencies
uv pip install -e .

# Start server
python app.py

# Start dashboard (separate terminal)
streamlit run dashboard.py --server.port 8501
```

### CLI (pytest directly)

```bash
cd llm/benchmark

# Run all tests for an agent
BENCHMARK_AGENT_DIR=llm/agents/kubectl \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark

# Run with tag filter
BENCHMARK_AGENT_DIR=llm/agents/aws TAG_FILTER=easy \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark

# Run specific indices
BENCHMARK_AGENT_DIR=llm/agents/aws TEST_INDICES=0,2,5 \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark

# AWS with infra deploy
BENCHMARK_AGENT_DIR=llm/agents/aws TAG_FILTER=easy DEPLOY_INFRA=true \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark
```

## Deployment

Docker image runs both server and dashboard:

```dockerfile
CMD ["sh", "-c", "\
    streamlit run dashboard.py --server.port ${DASHBOARD_PORT} & \
    uvicorn --workers ${UVICORN_WORKERS} --port ${BENCHMARK_PORT} app:app"]
```

Helm chart: `deploy/kubernetes/benchmark-server/`
- Server port and dashboard port are injected from `values.yaml`
- `BENCHMARK_API_URL` is set to `http://localhost:<serverPort>` in the deployment template

## Adding a New Test to an Existing Agent

### 1. Create fixture directory

```bash
cd llm/agents/<agent>/fixtures/
mkdir 042_descriptive_name_of_test
```

Naming convention: `<number>_<short_snake_case_description>/`

### 2. Create test_case.yaml

```yaml
user_prompt: "What pods are crashlooping in the payments namespace?"
expected_output:
  - "kubectl get pods -n payments --field-selector=status.phase!=Running"
tags:
  - command
  - kubernetes
  - easy
```

Required fields:
- `user_prompt` — The question sent to the agent
- `expected_output` — List of acceptable answers (used for RAGAS scoring)
- `tags` — List of tags for filtering (e.g. `easy`, `medium`, `hard`, `command`, `rca`)

### 3. (Optional) Add setup/teardown hooks

If the test needs infrastructure:

```yaml
user_prompt: "Why is the payment-api pod crashing?"
expected_output:
  - "OOMKilled — memory limit too low"
tags:
  - rca
  - medium
setup_timeout: 300
before_test: |
  kubectl apply -f manifests.yaml
  kubectl wait --for=condition=ready pod -l app=payment-api --timeout=120s
after_test: |
  kubectl delete -f manifests.yaml --ignore-not-found
```

Place supporting files (manifests, scripts) in the same fixture directory.

### 4. (Optional) For AWS/Azure RCA scenarios

If the test needs cloud infrastructure, add a `scenario` field linking to a CloudFormation/ARM template:

```yaml
user_prompt: "Our Lambda 'order-writer' fails with AccessDeniedException..."
expected_output:
  - "The IAM role is missing dynamodb:PutItem permission"
tags:
  - rca
  - aws
  - easy
scenario: E11
type: rca
```

Then create the matching template at `aws-agent-test/scenarios/easy/E11.yaml`.

### 5. Verify

```bash
# Check the fixture is discovered
BENCHMARK_AGENT_DIR=llm/agents/<agent> \
  python -c "
from pathlib import Path
from llm.agents.common.fixtures import find_test_cases
cases = find_test_cases(Path('llm/agents/<agent>/fixtures'))
for fp, tid, idx in cases:
    print(f'{idx}: {tid}')
"

# Run just your new test
BENCHMARK_AGENT_DIR=llm/agents/<agent> TEST_INDICES=42 \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark
```

No server restart needed — fixtures are discovered dynamically at test time.

## Adding a New Agent

### 1. Create agent directory

```bash
mkdir -p llm/agents/<name>/fixtures
```

### 2. Create config.yaml

Minimal config:

```yaml
agent: <name>                    # Must match the @agent prefix used in LLM queries
tool_names:
  - <tool_name>                  # Tool names for answer extraction from LLM response
```

Full config with all options:

```yaml
agent: <name>
tool_names:
  - <tool_name>
db_tool_name: <tool_name>       # Optional: fetch command from DB instead of response
prefix_query: true               # Optional: prefix query with @agent (default: true)
max_retries: 2                   # Optional: retry count for transient failures

# Optional Python hooks (dotted import paths)
before_query: llm.agents.<name>.utils.before_func
after_query: llm.agents.<name>.utils.after_func
answer_extractor: llm.agents.<name>.utils.custom_extractor
result_enricher: llm.agents.<name>.utils.custom_scorer

# Optional: auto-approve additional tools during benchmarks
extra_tool_confirmations:
  some_other_tool: "yes"
```

### 3. Add test fixtures

Create fixture directories with `test_case.yaml` files (see "Adding a New Test" above).

### 4. (Optional) Add conftest.py for infra lifecycle

Only needed if tests require session-level infrastructure (like AWS CloudFormation or Azure ARM).
See `llm/agents/aws/conftest.py` for the pattern.

### 5. Verify

```bash
# Check agent appears in API
curl http://localhost:9999/agent-benchmark/agents | jq

# Check tags
curl http://localhost:9999/agent-benchmark/agents/<name>/tags | jq

# Run a quick test
BENCHMARK_AGENT_DIR=llm/agents/<name> MAX_TESTS=1 \
  python -m pytest llm/agents/common/benchmark.py -v -m benchmark
```

The agent auto-appears in the dashboard — no code changes needed. The server discovers agents by scanning for `config.yaml` files under `llm/agents/`.

---

## Development Context

### Project Overview
The **LLM Benchmark** service is a Python-based infrastructure for evaluating the quality and performance of AI agents using standardized scenarios and metrics.

### Key Technologies
- **Language:** Python 3.11+
- **Framework:** FastAPI (Server), Streamlit (Dashboard)
- **Testing:** Pytest (execution engine)
- **Scoring:** RAGAS (Retrieval Augmented Generation Assessment Service)
- **Infrastructure:** AWS CloudFormation / Azure ARM (for live RCA scenarios)

### Development Conventions
- **Build System:** UV (for fast dependency management and locking)
- **Agent Testing:** Agents must provide `config.yaml` and `fixtures/` with `test_case.yaml`.
- **Metrics:** Automatically collects Answer Similarity, Answer Relevancy, and Planner Relevancy.
- **Validation:** Run `pytest` directly or via the API/Dashboard to confirm agent performance.
