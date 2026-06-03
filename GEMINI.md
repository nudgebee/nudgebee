# Nudgebee Monorepo - Development Guide

## Quick Overview

14 interconnected services (8 Go, 5 Python, 1 TypeScript) deployed on Kubernetes via EKS.
- **Environments:** `main` (dev) → `test` (staging) → `prod` (production).
- **Core Strategy:** Standardized validation loops, domain-driven services, and agent-assisted automation.

---

## System Architecture & Service Map

### Core Infrastructure
| Service | Domain | Tech Stack |
| :--- | :--- | :--- |
| `api-server/services` | Central API & Auth | Go / Gin / Postgres |
| `app` | Frontend Dashboard | Next.js / TS / MUI |
| `ticket-server` | Ticket Management | Go / PostgreSQL / RabbitMQ |
| `notifications-server` | Alert Delivery | Python / Poetry |

### AI & LLM Engine
| Service | Domain | Tech Stack |
| :--- | :--- | :--- |
| `llm/llm-server` | Agent Orchestration | Go / Bedrock / OpenAI |
| `llm/rag-server` | Knowledge Retrieval | Python / Vector DB |
| `llm/code-analysis` | Source Code Intelligence | Go / Static Analysis |
| `llm/benchmark` | Model Evaluation | Python / Pytest |

### Observability & Data Collection
| Service | Domain | Tech Stack |
| :--- | :--- | :--- |
| `collector-server/cloud-collector` | AWS/GCP Collector | Go / AWS SDK |
| `collector-server/k8s-collector` | K8s Metrics & Relay | Python (App) & Go (Relay) |

### Automation & ML
| Service | Domain | Tech Stack |
| :--- | :--- | :--- |
| `ml-k8s-server` | Anomaly Detection | Python / Scikit-learn |
| `auto-pilot` | Automation & Remediation | Python / Poetry |
| `runbook-server` | Workflow Execution | Go / Temporal.io |

---

## Module Structure

### Go Services (8 modules)
1. `api-server/services/` - Core backend (Gin)
2. `ticket-server/` - Ticket management
3. `collector-server/cloud-collector/` - AWS/cloud data collection
4. `collector-server/k8s-collector/relay-server/` - K8s relay gateway
5. `llm/code-analysis/` - Code analysis engine
6. `llm/llm-server/` - LLM inference service
7. `app-e2e-tests/` - Integration tests
8. `api-server/test_servicemap/` - Service map tests

### Python Services (5+ modules)
1. `ml-k8s-server/` - ML models & K8s autoscaling
2. `llm/rag-server/` - RAG (Retrieval Augmented Generation)
3. `collector-server/k8s-collector/app/` - K8s metrics aggregation
4. `auto-pilot/` - Automation & remediation engine
5. `notifications-server/` - Notification delivery
6. `auto-pilot/sidecar/` - Automation sidecar
7. `llm/benchmark/` - LLM benchmarking

### TypeScript Service (1 module)
- `app/` - Frontend dashboard (Next.js)

---

## AI Coding Principles

These principles apply to every agent (Claude, Gemini, human) working in this repo. They exist because the dominant failure mode of AI-assisted coding is not bad code — it is code that exactly matches a *wrong* request. The goal of this section is to make the agent **less wrong**, not just faster.

### 1. Argue Before You Accept (Adversarial Pre-Implementation)

Before implementing any non-trivial change, run an adversarial pass **first**:

1. Restate the proposed approach in one paragraph.
2. Find the **three strongest reasons the plan is wrong**. Not nitpicks — structural objections.
3. Ask: *"What would a senior engineer reviewing this six months from now criticize first?"*
4. Ask: *"What does this plan optimize for, and what does it sacrifice?"*
5. Emit a binding verdict: `PROCEED`, `REVISE`, or `REDESIGN`. Do not start coding on anything but `PROCEED`.

**Use aggressively for:** new features touching shared types, API contracts, DB schema, cross-service behavior, architectural choices.
**Skip for:** typos, formatting, 1-line bug fixes, docs-only changes, purely internal refactors with no interface change.

The `/challenge` skill runs this pass on demand. For larger changes, run it explicitly before editing any code.

### 2. AI First-Pass Review Before Human Review

Every PR gets an AI self-review pass *before* a human reviewer ever sees it. `/create-pr` performs this automatically: it reads the diff, flags correctness / security / performance / over-engineering risks, fixes what it can, and surfaces residual risks in the PR body under **Risks & Counterarguments**. Human reviewers should start from that list, not from zero.

### 3. Parallel Session Patterns

Parallel AI sessions are a workforce, not arbitrary task-splitting. Different tabs do different *kinds of thinking*, not different chunks of the same task. Recommended layout for any non-trivial change:

| Tab | Purpose | Skill / prompt |
|---|---|---|
| **Implement** | Write the code for the change. | freeform |
| **Challenge** | Adversarial pass — argue against the plan and the diff. | `/challenge` |
| **Review** | Correctness / security / style review of the diff. | `/review-pr` (on your own branch) |
| **Simplify** | Look for reuse, dead code, premature abstractions. | `simplify` skill |
| **Validate** | Run lint / test / build and auto-fix. | `/validate`, `/fix-lint` |

**Minimum: two tabs.** Sweet spot: three. You do **not** need ten.

### 4. Decisions & Lessons Learned (Living Constitution)

`CLAUDE.md` is the shared decisions log for all agents. When a decision is made about architecture, tooling, or patterns, record **why**, not just **what**. When an approach is tried and abandoned, record it there so we never waste cycles re-trying it.

See `CLAUDE.md → AI Coding Principles → Decisions & Lessons Learned` for the log and format.

---

## Gemini Toolbox

| Objective | Command | Action |
| :--- | :--- | :--- |
| **Challenge a Plan** | `/challenge` | Adversarial review — argue against a proposed plan or an existing diff to catch structural risks before they compound. |
| **Validate Changes** | `/validate` | Runs linters and tests for affected services. |
| **Fix Lint** | `/fix-lint` | Auto-fix formatting and lint issues across affected services. |
| **Commit Changes** | `/commit` | Final validation + drafts a commit message. |
| **Create a PR** | `/create-pr` | Validates, AI self-reviews the diff, then opens the PR. |
| **Review a PR** | `/review-pr` | Creates worktree + technical review. |
| **Emergency Fix** | `/hotfix` | Direct-to-prod PR + backmerge plan. |
| **My Tickets** | `/my-tickets` | Show pending sprint tickets. |
| **Loki Logs** | `/loki-logs` | Search logs in Loki. |

---

## Interaction Protocol

All agents (AI or Human) must follow this protocol for every task.

1. **Research:** Map files and dependencies. **Reproduce bugs first.**
2. **Strategy:** Propose solution with **Pros & Cons**. List alternatives.
3. **Adversarial Pass:** Before committing to the plan, find **three strongest reasons it is wrong**. What would a senior engineer reviewing this in six months criticize? Emit a binding verdict: `PROCEED`, `REVISE`, or `REDESIGN`. Use `/challenge` for any non-trivial change. **Do not skip this step for architectural, schema, or cross-service work.**
4. **Refinement:** Ask clarifying questions. **Wait for user approval.**
5. **Execution:** Surgical implementation followed by **Mandatory Validation** (run the service's validation command from the Build & Validation Matrix below).
6. **Self-Review:** Before handing off, run an AI first-pass review of the diff (done automatically by `/create-pr`, or invoke `/review-pr` on your own branch). Surface residual risks to the human reviewer rather than hiding them.

---

## Environment & Branches

```
main (dev) ─PR─> test (staging) ─PR─> prod (production)
   ↑                   ↑                    ↑
   └─── Backmerge ───┴─── Backmerge ──────┘ (hotfixes)
```

**CI/CD Automation:**
- Every merge to `main` → Auto PR to `test`
- Every merge to `test` → Auto PR to `prod`
- Hotfix: Direct to `prod` → Backmerge to test → Backmerge to main

---

## Build Commands by Service Type

### Go Services with Makefile (6 services)
**Applies to:** api-server/services, ticket-server, collector-server/cloud-collector, collector-server/k8s-collector/relay-server, llm/llm-server, llm/code-analysis

```bash
cd {service-path}

make fmt          # Format code (gofmt)
make lint         # Lint (golangci-lint)
make test         # Unit tests with coverage
make validate     # fmt + lint + test (must pass before build)
make run          # Run service locally (go run ./cmd)
make build        # Build binary (requires validate pass)
make benchmark    # Performance tests (some services)
```

**llm/code-analysis additional targets:**
```bash
make check        # fmt + vet + lint + test (all checks)
make build-linux  # Build for Linux
make docker-build # Build Docker image
make clean        # Clean artifacts
make deps         # go mod download + tidy
```

**Validation in CI:** `golangci-lint run` (timeout: 10-20m)

### Python Services with Makefile (3 services)
**Applies to:** ml-k8s-server, llm/rag-server, collector-server/k8s-collector/app

```bash
cd {service-path}

make install      # Install: poetry install
make fmt          # Format: poetry run black .
make lint         # Lint: poetry run black --check . + flake8 + mypy
make test         # Test: poetry run pytest
make run          # Run service
make clean        # Clean cache directories
```

**Validation in CI:**
- `poetry run black --check .` (line-length: 120)
- `poetry run flake8 .`
- `poetry run mypy {path}` (namespace_packages: true)

### Python Services WITHOUT Makefile (2+ services)
**Applies to:** auto-pilot, notifications-server, auto-pilot/sidecar, llm/benchmark

```bash
cd {service-path}
poetry install

poetry run black --check .    # Check format
poetry run black .            # Auto-format
poetry run flake8 .           # Lint
poetry run mypy ./**/*.py     # Type check
poetry run pytest             # Run tests
```

### Go Services WITHOUT Makefile (2 services)
**Applies to:** app-e2e-tests, api-server/test_servicemap

```bash
go mod download   # Download modules
go test ./...     # Run tests
go build ./...    # Build
```

Check service's CI workflow (`.github/workflows/{service}*.yaml`) for specific build steps.

### TypeScript Frontend
**Applies to:** app

```bash
cd app

npm ci --legacy-peer-deps     # Clean install (use in CI/CD)
npm install                   # Install locally

npm run dev                   # Dev server (port 3000, turbo)
npm run build                 # Production build
npm run lint2                 # oxlint + prettier check
npm run lint2:fix             # Auto-fix linting
npm run test                  # Jest tests
npm run prettier:check        # Prettier format check
npm run analyze               # Bundle size analysis
```

**Validation in CI:**
- `npm ci --legacy-peer-deps`
- `npm run lint2` (oxlint + prettier)
- `npm run build` (NODE_OPTIONS=--max_old_space_size=4096)

---

## Build & Validation Matrix (Quick Reference)

| Module | Type | Has Makefile | Validation Command |
|--------|------|--------------|-------------------|
| api-server/services | Go | ✓ | `make validate` |
| ticket-server | Go | ✓ | `make validate` |
| collector-server/cloud-collector | Go | ✓ | `make validate` |
| collector-server/k8s-collector/relay-server | Go | ✓ | `make validate` |
| llm/code-analysis | Go | ✓ | `make check` |
| llm/llm-server | Go | ✓ | `make validate` |
| app-e2e-tests | Go | ✗ | `go test ./...` |
| api-server/test_servicemap | Go | ✗ | `go test ./...` |
| ml-k8s-server | Python | ✓ | `make lint && make test` |
| llm/rag-server | Python | ✓ | `make lint && make test` |
| collector-server/k8s-collector/app | Python | ✓ | `make lint && make test` |
| auto-pilot | Python | ✗ | `poetry run black --check . && poetry run flake8 .` |
| notifications-server | Python | ✗ | `poetry run black --check . && poetry run flake8 .` |
| auto-pilot/sidecar | Python | ✗ | `poetry run black --check . && poetry run flake8 .` |
| llm/benchmark | Python | ✗ | `poetry run pytest` |
| app | TypeScript | ✗ | `npm run lint2 && npm run test` |

---

## Docker Build & CI/CD

Each service has its own `Dockerfile` and CI workflow under
`.github/workflows/{service}-{env}.yaml`. Read those files directly
when you need build, push, or deployment details — they are the
source of truth and stay in sync with reality automatically.

---

## Local Development Workflow

### 1. Create Feature Branch
```bash
git checkout -b feature/description   # new feature
git checkout -b fix/description       # bug fix
```

### 2. Make Changes

### 3. Validate Locally (BEFORE COMMIT)

```bash
# Go (with Makefile)
cd {service} && make validate

# Python (with Makefile)
cd {service} && make lint && make test

# Python (no Makefile)
cd {service} && poetry run black --check . && poetry run flake8 . && poetry run pytest

# TypeScript
cd app && npm run lint2 && npm run test
```

### 4. Commit & Push
```bash
git add <specific files>
git commit -m "type(scope): description"
git push origin feature/description
```

**Commit format:** `type(scope): subject`

| Type | Use when |
|---|---|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `refactor` | Code restructure, no behavior change |
| `perf` | Performance improvement |
| `test` | Tests only |
| `chore` | Maintenance, deps, config |
| `ci` / `infra` | CI/CD or infrastructure changes |

| Scope | Paths |
|---|---|
| `ui` | `app/` |
| `autopilot` | `auto-pilot/`, `auto-pilot/sidecar/` |
| `ml` | `ml-k8s-server/`, LLM services |
| `llm` | `llm/*` |
| `notifications` | `notifications-server/` |
| `tickets` | `ticket-server/` |
| `relay` | `collector-server/k8s-collector/relay-server/` |
| `collector` | `collector-server/*` |
| `deps` | Dependency updates |
| `NB-xxx` | Ticket number — api-server, migrations, deploy, cross-service |

### 5. Create PR to `main`

**GitHub Issue Required:** Every PR targeting `main` MUST link to a GitHub issue. Use `/create-issue` if none exists. PRs targeting `test` or `prod` are exempt.

**PR Guidelines:**
- Use `/create-pr` skill — it validates, self-reviews, and opens the PR
- Include `Fixes #<issue>` in the body (required for PRs to `main`)
- CI must pass before merge
- Single commit per PR (enforced by `/create-pr`)

### 6. Automated → Test Environment
- After merge to `main` → Automated PR `main` → `test`
- CI builds and deploys to test K8s cluster

### 7. Manual → Production
- Create PR `test` → `prod`
- CI builds multi-arch Docker image, pushes to ECR, deploys via Helm

---

## Service-Specific Documentation

Each service has its own `CLAUDE.md` where one exists:
- `app/CLAUDE.md` - React components, Next.js patterns, design system
- `llm/llm-server/CLAUDE.md` - LLM service architecture
- `llm/code-analysis/CLAUDE.md` - Code-analysis engine
- `collector-server/k8s-collector/relay-server/CLAUDE.md` - K8s relay gateway
- `api-server/services/knowledge_graph/CLAUDE.md` - Knowledge graph subsystem
- Other services (to be documented)

**Always read the service-specific `CLAUDE.md` before working on a service.**

---

## Database Migrations & RPC Actions

### Postgres migrations (`api-server/migrations/migrations/app/`)
- **Flat layout** — files named `{ts_ms}_V{N}_{snake_case_description}.up.sql` / `.down.sql`, no enclosing directory.
- **Use the scaffold** — `./api-server/migrations/new-migration.sh <snake_case_name>` creates both files with a fresh unix-ms timestamp and the next `V<N>`. Never hand-pick timestamps: golang-migrate tracks state in a single-row `nudgebee.schema_migrations` table, and a new file with a leading integer **below** the applied version is **silently skipped**.
- `.down.sql` is optional but recommended. Write idempotent SQL (`IF EXISTS` / `IF NOT EXISTS`).
- **NEVER use `CREATE INDEX CONCURRENTLY`** — golang-migrate wraps each migration in a transaction by default; `CREATE INDEX CONCURRENTLY` cannot run inside one. If you genuinely need it, add `-- migrate:no-transaction` at the top of the file.
- Full details: [`api-server/migrations/README.md`](api-server/migrations/README.md).

### Other migration trees
- `migrations/clickhouse/` — applied by the same golang-migrate binary when `CLICKHOUSE_ENABLED=true`. Files use a numeric prefix (`NN_*.up.sql`).
- `migrations/rabbitmq/` — shell scripts (`NNN_*.sh`) run sequentially after RabbitMQ is healthy.

### RPC action naming convention
Actions are HTTP RPC handlers registered in [`app/src/lib/actions.yaml`](app/src/lib/actions.yaml) — the routing table `@lib/rpcGateway` uses to dispatch each operation to its upstream handler (mounted under `/rpc/*` on each backend service).

```
<module>_<verb>_<description>_[<version>]
```

- **module** — Shortest accurate noun: `ai`, `runbooks`, `cloud`, `accounts`, `agents`, etc.
- **verb** — Pick from the taxonomy below; don't invent new verbs without reason.
- **description** — snake_case. May be empty when the verb is self-explanatory (`accounts_list`, `accounts_sync`).
- **version** — Optional. Only when a `v1` still exists, or a `v3` is planned within ~6 months. Otherwise drop it — `_v2` is legacy noise; renames are the cheap moment to lose it.

#### Verb taxonomy

Pick the verb that matches the operation's *intent and return shape*, not the verb that "sounds close." When two verbs both fit, prefer the more specific one.

**Read**

| Verb | Use when | Returns | Example |
|---|---|---|---|
| `get` | Fetch **one** record by id | single object or 404 | `users_get_current` |
| `list` | Fetch a **collection** (filters / pagination / ordering — including relevance) | array | `accounts_list`, `insights_list` |
| `aggregate` | Group / sum / count / bucket | aggregation | `accounts_aggregate` |
| `count` | Just a number | int | `alerts_count` |
| `check` | Validate / probe | bool or status | `clusters_check_health` |

**Write**

| Verb | Use when | Example |
|---|---|---|
| `create` | Insert a new record | `agents_create_token` |
| `update` | Modify an existing record | `tickets_update_status` |
| `upsert` | Insert-or-update | `settings_upsert` |
| `delete` | Remove a record | `agents_delete` |
| `apply` | Execute prepared changes | `recommendations_apply` |

> **Decision tree — create vs update vs upsert vs apply:** `create` = caller knows record doesn't exist (fails on duplicate); `update` = caller knows record exists (fails if missing); `upsert` = idempotent insert-or-update; `apply` = server enacts a pre-computed diff/plan supplied by caller. `apply` is closer to `execute` than `update` — don't pick it just because input looks state-transition-shaped. Status/flag changes → `update`.

> **`convert` is a one-off, not a general verb.** Used only for `applications_convert_profile` (pure transformation of a payload between representations — no record written). Don't generalize: a `convert` that writes is `create`; that mutates is `update`; that returns a re-formatted view is `get_*_as_<format>`. Audit before adding a second `convert`.

**Action / job**

| Verb | Use when | Example |
|---|---|---|
| `execute` | Run a defined operation (sync or async) | `anomaly_execute`, `playbook_execute` |
| `replay` | Re-run an existing prior execution | `workflow_replay_execution` |
| `cancel` | Halt a long-running operation gracefully | `ai_cancel_investigation`, `workflow_cancel_execution` |
| `pause` / `resume` | State-machine transitions on a running op (preferred over `update_state` when transition has side-effects) | `workflow_pause`, `workflow_resume` |
| `publish` / `unpublish` | Versioning + rollout | `workflow_publish_version` |
| `sync` | Trigger an external-data sync | `accounts_sync` |
| `generate` | Compute & return a fresh artifact | `ml_generate_node_recommendations` |
| `enable` / `disable` | Toggle a boolean flag (no state-machine complexity) | `integrations_enable` |

> **Note on messaging/notification dispatch:** No `send` verb. For programmatic delivery use `execute`; for rollout-style fan-out use `publish`; for test/verify-setup actions use `check`.

> **Note on status/flag changes:** Don't invent verbs for state transitions on a flag field (no `activate`/`deactivate`/`toggle`/`approve`/`reject`/`acknowledge`/`snooze`). Use `update` and name the action after the *field* being updated, not the direction. Examples: `events_update_resolution`, `events_update_classification`, `events_update_rule_override`, `tenant_update_registration_status`. Reserve `pause`/`resume`/`cancel` for state-machine transitions on a running operation; reserve `enable`/`disable` only when there's an existing convention on the resource (e.g. `integrations_enable`).

> **Note on integration onboarding URLs:** Handlers that return a deep-link URL for the user to complete onboarding in the cloud provider console (CloudFormation, ARM, GCP Deployment Manager) are `get` actions, not `create`. Name them `<provider>_get_onboard_<service>_url` (e.g. `aws_get_onboard_eventbridge_url`, `gcp_get_onboard_pubsub_url`). The `onboard` qualifier disambiguates the URL's purpose; `get` reflects that no persistent record is created server-side — the integration record gets created by a separate callback once the user finishes the provider-side flow.

**Avoid:**
- `trigger_*` (use `execute`/`sync`); `fetch_*` (use `get`/`list`); `find_*`/`search_*` (use `get`/`list`); `do_*`/`handle_*`/`process_*` (too vague); bare `query` (be specific).
- `test_*`/`validate_*` — use `check` (umbrella verb).
- `save_*` — use `create` or `upsert`. If it inserts into a side/marker table, name after the side table (`ai_create_saved_conversation`, not `ai_save_conversation`).
- `onboard_*` / `*_onboard` — use `create` (integrations) or `register` (signup).
- `map_*`/`unmap_*`, `link_*`/`unlink_*` — model the mapping as a resource; use `create`/`delete` on it.
- `stop_*` — use `cancel`.

**Examples:** `ai_get_tools`, `accounts_list`, `runbooks_create_playbook`, `accounts_sync`.

**Hasura-style table queries (carve-out):** A subset of read operations are named `<module>_<entity>_v[N]` (e.g. `k8s_pods_v2`) and predate this convention. They are internally consistent and not renamed in bulk. New table queries should follow the verb taxonomy (`<module>_list`, `<module>_aggregate`, etc.).

### Validating actions.yaml changes
```bash
cd app
npm run dev       # parses actions.yaml at boot; exercise the changed action end-to-end
npm run lint2     # oxlint + prettier check
```

---

## Key Files & Locations

| Path | Purpose |
|------|---------|
| `.github/workflows/` | CI/CD automation for all services |
| `deploy/kubernetes/` | Helm charts & values files |
| `deploy/containers/` | Base Dockerfiles (clickhouse, rabbitmq) |
| `api-server/migrations/` | Database migrations — Postgres (`app/`), Clickhouse, RabbitMQ — applied by golang-migrate. See [`api-server/migrations/README.md`](api-server/migrations/README.md). |
| Each service `Dockerfile` | Container image build configuration |
| Each service `Makefile` | Build automation (if service has one) |
| Each service `go.mod`/`pyproject.toml`/`package.json` | Dependencies |

---

## Deployment Checklist

Before merging to production:
- [ ] PR to `main` links to a GitHub issue (`Fixes #<number>`)
- [ ] Local validation passes
- [ ] All tests pass
- [ ] No linting errors
- [ ] Code reviewed and approved
- [ ] Tested in `test` environment
- [ ] No hardcoded secrets in code
- [ ] Documentation updated (if needed)
- [ ] All GitHub secrets configured (if needed)

---

## Troubleshooting

**Format/Lint Failures:**
```bash
# Go
make fmt && git add . && git commit

# Python
make fmt          # If Makefile exists
poetry run black . # If no Makefile

# TypeScript
npm run lint2:fix && git add . && git commit
```

**Test Failures:**
- Run locally first to reproduce
- Check recent commits in the service
- Verify environment variables are set

**Dependency Issues:**
```bash
# Go
go mod download && go mod tidy

# Python
poetry install

# Node
rm -rf node_modules package-lock.json && npm ci --legacy-peer-deps
```

**Docker Build Issues:**
- Ensure Dockerfile exists in service directory
- Test locally: `docker build -t test:latest .`

---

## Important Notes

- **No invented tools:** Only commands that exist in actual Makefiles, CI workflows, or Dockerfiles
- **Validate before push:** Always run local validation before committing
- **Makefiles are optional:** Some services use direct commands (poetry run, go test, npm run)
- **Docker as source of truth:** If unsure about build process, check the Dockerfile
- **CI as automation:** GitHub Actions workflows show actual build steps

---

**Last updated:** April 2026. Verified: 8 Go + 5 Python + 1 TypeScript = 14 total modules.
