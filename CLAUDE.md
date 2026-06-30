# Nudgebee Monorepo - Development Guide

> **New here?** Start with the [root README](README.md) — it covers what Nudgebee is, how to bring up a local stack, and the per-service entry points. This file (`CLAUDE.md`) is primarily guidance for AI coding agents (Claude Code, etc.), but a few sections are worth a human contributor's time:
>
> - **[Architecture Decisions](docs/architecture-decisions.md)** — the living "why-behind-X" log; reasoning behind structural choices we don't want to re-litigate.
> - **[RPC action naming convention](docs/rpc-action-naming.md)** — the verb taxonomy every new action in `app/src/lib/actions.yaml` must follow.
> - **[Database Migrations & RPC Actions](#database-migrations--rpc-actions)** — the migration scaffolding script and `CREATE INDEX CONCURRENTLY` gotcha.
> - **Build commands** — the per-service `make validate` / `npm run lint2` flows.
>
> Everything else (AI principles, skills, slash commands, parallel-session patterns) is AI-workflow scaffolding — safe to skip if you're not driving an agent.

## Quick Overview

14 interconnected services (8 Go, 5 Python, 1 TypeScript) deployed on Kubernetes. Three-tier environment: main (dev) → test → prod.

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

Every PR gets an AI self-review pass *before* a human reviewer ever sees it. `/create-pr` performs this automatically: it reads the diff, flags correctness / security / performance / over-engineering risks, fixes what it can, and surfaces residual risks in the PR body under **Review Notes → Risks & Counterarguments**. Human reviewers should start from that list, not from zero.

### 3. Parallel Session Patterns

Parallel AI sessions are a workforce, not arbitrary task-splitting. Different tabs do different *kinds of thinking*, not different chunks of the same task. Recommended layout for any non-trivial change:

| Tab | Purpose | Skill / prompt |
|---|---|---|
| **Implement** | Write the code for the change. | freeform |
| **Challenge** | Adversarial pass — argue against the plan and the diff. | `/challenge` |
| **Review** | Correctness / security / style review of the diff. | `/review-pr` (on your own branch) |
| **Simplify** | Look for reuse, dead code, premature abstractions. | `simplify` skill (Claude Code global skill) |
| **Validate** | Run lint / test / build and auto-fix. | `/validate`, `/fix-lint` |

**Minimum: two tabs.** Sweet spot: three. You do **not** need ten.

### 4. Decisions & Lessons Learned (Living Constitution)

This file is not a static reference — it is a **living constitution**. When a decision is made about architecture, tooling, or patterns, record **why**, not just **what**. When an approach is tried and abandoned, record it here so we never waste cycles re-trying it.

**Rules for this section:**
- Keep entries tight (1–3 sentences).
- Always include the **reason** and a **reconsider-if** condition.
- Failed experiments go in the second subsection — they are as valuable as the decisions that stuck.
- The `/challenge` skill appends here automatically when its verdict is `PROCEED` for an architectural decision.

#### Architecture Decisions

The full decision log and the "What We've Tried and Won't Try Again" log live in **[`docs/architecture-decisions.md`](docs/architecture-decisions.md)** (extracted to keep this file lean). Read it before any change touching shared types, API contracts, DB schema, or cross-service behavior. Append new entries there using the format documented at the top of that file; the `/challenge` skill appends `PROCEED` architectural decisions there automatically.

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

## Build Commands by Service Type

### Go Services with Makefile (6 services)
**Applies to:** api-server/services, ticket-server, collector-server/cloud-collector, collector-server/k8s-collector/relay-server, llm/llm-server, llm/code-analysis

```bash
cd {service-path}

make fmt          # Format code (gofmt)
make lint         # Lint (golangci-lint)
make test         # Unit tests with coverage
make validate      # fmt + lint + test (must pass before build)
make run          # Run service locally (go run ./cmd)
make build        # Build binary (requires validate pass)
make benchmark    # Performance tests (some services)
```

**llm/code-analysis additional targets:**
```bash
make build-linux  # Build for Linux
make vet          # go vet
make check        # fmt + vet + lint + test (all checks)
make docker-build # Build Docker image
make docker-run   # Run in Docker
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

**Setup first:**
```bash
cd {service-path}
poetry install
```

**Then use direct poetry commands:**
```bash
poetry run black --check .    # Check format
poetry run black .            # Auto-format
poetry run flake8 .           # Lint
poetry run mypy ./**/*.py     # Type check
poetry run pytest             # Run tests
```

**CI Workflow:**
- Sets Python version (3.11 or 3.12)
- Installs Poetry
- Caches venv
- Runs: `poetry run black --check .`
- Runs: `poetry run flake8 .`
- Optional: `poetry run mypy`

### Go Services WITHOUT Makefile (2 services)
**Applies to:** app-e2e-tests, api-server/test_servicemap

**Use direct go commands:**
```bash
go mod download   # Download modules
go test ./...     # Run tests
go run ./cmd      # Run (if applicable)
go build ./...    # Build (if applicable)
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
- `npm ci --legacy-peer-deps` (cache: package-lock.json)
- `npm run lint2` (oxlint + prettier)
- `npm run build` (NODE_OPTIONS=--max_old_space_size=4096)

## Docker Build & CI/CD

Each service has its own `Dockerfile` and CI workflow under
`.github/workflows/{service}-{env}.yaml`. Read those files directly
when you need build, push, or deployment details — they are the
source of truth and stay in sync with reality automatically.

## Local Development Workflow

### 1. Create Feature Branch
```bash
git checkout -b feature/description
# or fix/description for bugfixes
```

### 2. Make Changes

### 3. Validate Locally (BEFORE COMMIT)

**Go services with Makefile:**
```bash
cd {service}
make validate    # This must pass
```

**Python services with Makefile:**
```bash
cd {service}
make lint && make test
```

**Python services WITHOUT Makefile:**
```bash
cd {service}
poetry install
poetry run black --check .
poetry run flake8 .
poetry run pytest
```

**TypeScript:**
```bash
cd app
npm run lint2
npm run test
npm run build --legacy-peer-deps
```

### 4. Commit & Push
```bash
git add .
git commit -m "type(scope): description"
# Types: feat, fix, docs, style, refactor, perf, test, chore, revert, ci, infra, release
# Scopes: autopilot, workflow, ml, llm, notifications, ui, tickets, relay, collector, deps
# Examples:
# fix(llm): handle null pointer in config
# feat(ui): add user settings page
# refactor(ml): update scaling algorithm
git push origin feature/description
```

### 5. Create PR to `main`

**GitHub Issue Required:** Every PR targeting `main` MUST link to a GitHub issue. Before creating a new issue, check if there's an existing issue (open or closed) related to the work — if so, link to that. Only create a new issue if no related issue exists and the user confirms. This does NOT apply to PRs targeting `test` or `prod` (cherry-picks / promotions).

When creating a PR, use the following template format (from `.github/pull_request_template.md`):

```markdown
# Description

[Summary of changes and motivation. Include relevant issue link.]

Fixes #<issue_number>

## Type of change

- [ ] Bug fix (non-breaking change which fixes an issue)
- [ ] Enhancement (non-breaking change which adds functionality)
- [ ] Refactor (non-breaking change which improves code structure)
- [ ] Documentation (non-breaking change which improves documentation)
- [ ] Performance (non-breaking change which improves performance)
- [ ] Chore (non-breaking change which does not modify src or test files)
- [ ] Build / CI (non-breaking change which does not modify src or test files)
- [ ] Security (non-breaking change which improves security)
- [ ] New feature (non-breaking change which adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)

# How Has This Been Tested?

[Describe tests run to verify changes. Provide reproduction instructions.]

- [ ] Test A
- [ ] Test B
```

**PR Creation via CLI:**
```bash
gh pr create --title "fix(llm): handle null pointer in config" --body "$(cat <<'EOF'
# Description

[Summary of changes]

Fixes #123

## Type of change

- [x] Bug fix (non-breaking change which fixes an issue)

# How Has This Been Tested?

- [x] Unit tests pass locally
- [x] Manual testing in dev environment

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

**PR Guidelines:**
- **PRs to `main` must include `Fixes #<issue>` linking to a GitHub issue.** Search for existing issues first and ask the user before creating a new one. PRs to `test`/`prod` are exempt.
- CI automatically runs validation
- All checks must pass
- Get code review
- Delete irrelevant "Type of change" options, keep only applicable ones checked
- Merge to `main`

### 6. Automated → Test Environment
- After merge to `main` → Automated PR `main` → `test`
- CI builds and deploys to test K8s cluster
- Manually validate in test environment

### 7. Manual → Production
- Create PR `test` → `prod`
- CI runs full validation
- CI builds multi-arch Docker image
- CI pushes to AWS ECR
- CI deploys via Helm to prod K8s cluster

## Service-Specific Documentation

Each service has its own CLAUDE.md where one exists:
- `app/CLAUDE.md` - React components, Next.js patterns, design system
- `llm/llm-server/CLAUDE.md` - LLM service architecture
- `llm/code-analysis/CLAUDE.md` - Code-analysis engine
- `collector-server/k8s-collector/relay-server/CLAUDE.md` - K8s relay gateway
- `api-server/services/knowledge_graph/CLAUDE.md` - Knowledge graph subsystem
- `api-server/services/anomoly/CLAUDE.md` - Anomaly detection subsystem (K8s metric + cloud spend, cross-service)
- Other services (to be documented)

**Always read service-specific CLAUDE.md before working on a service.**

## Quick Commands Reference

### Status Checks
```bash
git status                  # Current status
git diff main              # Changes vs main
git log --oneline -10      # Recent commits
```

### Local Validation
```bash
# Go
make validate

# Python with Makefile
make lint && make test

# Python without Makefile
poetry run black --check .
poetry run flake8 .
poetry run pytest

# TypeScript
npm run lint2
npm run test
npm run build
```

## Testing Strategy

- **Unit tests:** Alongside source code, run before commit
- **Integration tests:** `app-e2e-tests/` for cross-service validation
- **Validation order:** Unit → Integration → Deploy to test → Manual in test

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

## Key Files & Locations

| Path | Purpose |
|------|---------|
| `.github/workflows/` | CI/CD automation for all services |
| `deploy/kubernetes/` | Helm charts & values files for each service |
| `deploy/containers/` | Base Dockerfiles (clickhouse, rabbitmq) |
| `api-server/migrations/` | Database migrations — Postgres (`app/`), Clickhouse, RabbitMQ — applied by golang-migrate. See [`api-server/migrations/README.md`](api-server/migrations/README.md). |
| Each service `Dockerfile` | Container image build configuration |
| Each service `Makefile` | Build automation (if service has one) |
| Each service `go.mod`/`pyproject.toml`/`package.json` | Dependencies |

## Database Migrations & RPC Actions

### Postgres migrations (`api-server/migrations/migrations/app/`)
- **Flat layout** — files are named `{ts_ms}_V{N}_{snake_case_description}.up.sql` and `.down.sql`, no enclosing directory.
- **Use the scaffold script** — `./api-server/migrations/new-migration.sh <snake_case_name>` creates both files with a fresh unix-ms timestamp and the next `V<N>`. Never hand-pick timestamps: golang-migrate tracks state via a single-row `nudgebee.schema_migrations` table, and any new file whose leading integer is **lower than** the applied version is **silently skipped**.
- `.down.sql` is optional but recommended. Write idempotent SQL (`IF EXISTS` / `IF NOT EXISTS`).
- **NEVER use `CREATE INDEX CONCURRENTLY` or `DROP INDEX CONCURRENTLY`** — golang-migrate wraps each migration in a transaction by default and **ignores the dbmate-style `-- migrate:no-transaction` header**. A `CONCURRENTLY` statement inside the implicit tx fails mid-statement and leaves `nudgebee.schema_migrations` stuck `dirty=true`, blocking every later migration on that environment until an operator manually clears the flag. CI lints for `\bCONCURRENTLY\b` and rejects the PR (`.github/workflows/migrations-lint.yaml`). If you genuinely need to add/drop a large-table index without the brief `ACCESS EXCLUSIVE` lock a plain statement takes, apply the `CONCURRENTLY` form manually via `psql` against the live DB *before* opening the migration PR, then write the migration with plain `CREATE INDEX IF NOT EXISTS` / `DROP INDEX IF EXISTS` so it's a no-op where you pre-applied it and a quick locked statement where you haven't.
- Full details on tracking, recovery from `dirty=true`, and local apply commands: [`api-server/migrations/README.md`](api-server/migrations/README.md).

### Other migration trees
- `migrations/clickhouse/` — applied by the same golang-migrate binary when `CLICKHOUSE_ENABLED=true`. Files use a `NN_*.up.sql` numeric prefix.
- `migrations/rabbitmq/` — shell scripts (`NNN_*.sh`) run sequentially after RabbitMQ is healthy.

### RPC action naming convention
Actions are HTTP RPC handlers registered in [`app/src/lib/actions.yaml`](app/src/lib/actions.yaml) — the routing table the in-app gateway (`@lib/rpcGateway`) uses to dispatch each operation to its upstream handler (mounted under `/rpc/*` on each backend service). When adding a new action, follow this naming pattern:

```
<module>_<verb>_<description>_[<version>]
```

- **module** — Single word identifying the domain: `ai`, `runbooks`, `cloud`, `tickets`, `workflows`, etc. Prefer the shortest accurate noun (`accounts`, not `cloud_accounts`, unless the latter disambiguates from another `accounts` domain).
- **verb** — The operation. Pick from the taxonomy below; don't invent new verbs without reason.
- **description** — What the action does, snake_case. May be empty when the verb fully describes the action (e.g. `accounts_list`, `accounts_sync`).
- **version** — Optional. Only when a `v1` of the same name still exists in `actions.yaml` (disambiguation), **or** a `v3` is genuinely planned within ~6 months. Otherwise drop the suffix — `_v2` is legacy noise and a rename is the cheap moment to lose it.

#### Verb taxonomy

Pick the verb that matches the operation's *intent and return shape*, not the verb that "sounds close." The full taxonomy — read/write/job verb tables, the `create`/`update`/`upsert`/`apply` decision tree, the status-change and cross-tenant (`admin_*`) notes, and the "Avoid" list — lives in **[`docs/rpc-action-naming.md`](docs/rpc-action-naming.md)**. Quick reference: reads → `get` (one) / `list` (many) / `aggregate` / `count` / `check`; writes → `create` / `update` / `upsert` / `delete` / `apply`; jobs → `execute` / `sync` / `generate` / `cancel` / `pause`·`resume` / `publish` / `enable`·`disable`. Don't invent verbs (no `trigger_*`, `fetch_*`, `save_*`, `test_*`, `admin_*`). Hasura-style table queries (`k8s_pods_v2`) are a pre-existing carve-out, not renamed in bulk.

### Validating actions.yaml changes
```bash
cd app
npm run dev        # parses actions.yaml at boot; exercise the changed action
npm run lint2      # oxlint + prettier check
```
The dev server fails fast on a malformed `actions.yaml`. Type errors in upstream Go handlers surface as 400/500s from `/api/graphql` at runtime, so exercise the action end-to-end before opening a PR.

## Troubleshooting

**Format/Lint Failures:**
```bash
# Go
make fmt
git add . && git commit

# Python
make fmt          # If Makefile exists
poetry run black . # If no Makefile

# TypeScript
npm run lint2:fix
git add . && git commit
```

**Test Failures:**
- Run locally first to reproduce
- Check recent commits in the service
- Verify environment variables are set
- Check git history for similar issues

**Dependency Issues:**
```bash
# Go
go mod download
go mod tidy

# Python
poetry install
poetry update

# Node
rm -rf node_modules package-lock.json
npm ci --legacy-peer-deps
```

**Docker Build Issues:**
- Ensure Dockerfile exists in service directory
- Check base image availability
- Verify all RUN commands reference correct source files
- Test Dockerfile locally: `docker build -t test:latest .`

## Important Notes

- **No invented tools:** Only commands that exist in actual Makefiles, CI workflows, or Dockerfiles
- **Validate before push:** Always run local validation before committing
- **Makefiles are optional:** Some services use direct commands (poetry run, go test, npm run)
- **Docker as source of truth:** If unsure about build process, check the Dockerfile
- **CI as automation:** GitHub Actions workflows show actual build steps

## Module Build Status Summary

| Module | Type | Has Makefile | Build Command |
|--------|------|--------------|----------------|
| api-server/services | Go | ✓ | make validate |
| ticket-server | Go | ✓ | make validate |
| collector-server/cloud-collector | Go | ✓ | make validate |
| collector-server/k8s-collector/relay-server | Go | ✓ | make validate |
| llm/code-analysis | Go | ✓ | make check |
| llm/llm-server | Go | ✓ | make validate |
| app-e2e-tests | Go | ✗ | go test ./... |
| api-server/test_servicemap | Go | ✗ | go test ./... |
| ml-k8s-server | Python | ✓ | make lint && make test |
| llm/rag-server | Python | ✓ | make lint && make test |
| collector-server/k8s-collector/app | Python | ✓ | make lint && make test |
| auto-pilot | Python | ✗ | poetry run black --check . && poetry run flake8 . |
| notifications-server | Python | ✗ | poetry run black --check . && poetry run flake8 . |
| auto-pilot/sidecar | Python | ✗ | poetry run black --check . && poetry run flake8 . |
| llm/benchmark | Python | ✗ | poetry run pytest |
| app | TypeScript | ✗ | npm run lint2 && npm run test |

---

**Last updated:** Based on actual tested commands from Makefiles, CI/CD workflows, and Dockerfiles.
Verified: 8 Go + 5 Python + 1 TypeScript = 14 total modules.
