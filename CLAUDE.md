# Nudgebee Monorepo - Development Guide

> **New here?** Start with the [root README](README.md) — it covers what Nudgebee is, how to bring up a local stack, and the per-service entry points. This file (`CLAUDE.md`) is primarily guidance for AI coding agents (Claude Code, etc.), but a few sections are worth a human contributor's time:
>
> - **[Architecture Decisions](docs/architecture-decisions.md)** — the living "why-behind-X" log; reasoning behind structural choices we don't want to re-litigate.
> - **[RPC action naming convention](docs/rpc-action-naming.md)** — the verb taxonomy every new action in `app/src/lib/actions.yaml` must follow.
> - **[Database Migrations & RPC Actions](#database-migrations--rpc-actions)** — Atlas engine, the migration scaffolding script, and `CREATE INDEX CONCURRENTLY` via `-- atlas:txmode none`.
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

### 1. Baseline Behavioral Rules (Every Change, Including Small Ones)

These four rules govern **every individual edit** — including the small ones where the `/challenge` pass is skipped, which is exactly where over-editing happens (principles 2–5 below govern the *process* around a change). They bias toward caution over speed; for trivial tasks, use judgment.

1. **Think before coding — don't assume, don't hide confusion.** State assumptions explicitly. If multiple interpretations of the request exist, present them — don't pick one silently. If a simpler approach exists, say so. If something is unclear, stop and ask. (For non-trivial changes, this escalates into the full adversarial pass in principle 2.)
2. **Simplicity first.** Write the minimum code that solves the stated problem. No features beyond what was asked, no abstractions for single-use code, no unrequested "flexibility" or configurability, no error handling for impossible scenarios. Ask: *"Would a senior engineer say this is overcomplicated?"* If yes, simplify.
3. **Surgical changes.** Touch only what the request requires. Don't "improve" adjacent code, comments, or formatting; don't refactor what isn't broken; match existing style even where you'd do it differently. If you notice unrelated dead code, mention it — don't delete it. **Do** remove imports/variables/functions that *your own change* orphaned. The test: every changed line traces directly to the user's request.
4. **Goal-driven execution.** Turn the task into a verifiable goal before starting: "fix the bug" → "write a test that reproduces it, then make it pass"; "refactor X" → "tests pass before and after". Loop until verified — in this repo the closing check is the affected service's validation command (`make validate` / `make lint && make test` / `npm run lint2 && npm run test`, see [Build Commands](#build-commands-by-service-type)).

**These rules are working if:** diffs contain fewer unnecessary changes, fewer rewrites are caused by overcomplication, and clarifying questions come *before* implementation rather than after mistakes.

### 2. Argue Before You Accept (Adversarial Pre-Implementation)

Before implementing any non-trivial change, run an adversarial pass **first**:

1. Restate the proposed approach in one paragraph.
2. Find the **three strongest reasons the plan is wrong**. Not nitpicks — structural objections.
3. Ask: *"What would a senior engineer reviewing this six months from now criticize first?"*
4. Ask: *"What does this plan optimize for, and what does it sacrifice?"*
5. Emit a binding verdict: `PROCEED`, `REVISE`, or `REDESIGN`. Do not start coding on anything but `PROCEED`.

**Use aggressively for:** new features touching shared types, API contracts, DB schema, cross-service behavior, architectural choices.
**Skip for:** typos, formatting, 1-line bug fixes, docs-only changes, purely internal refactors with no interface change.

The `/challenge` skill runs this pass on demand. For larger changes, run it explicitly before editing any code.

### 3. AI First-Pass Review Before Human Review

Every PR gets an AI self-review pass *before* a human reviewer ever sees it. `/create-pr` performs this automatically: it reads the diff, flags correctness / security / performance / over-engineering risks, fixes what it can, and surfaces residual risks in the PR body under **Review Notes → Risks & Counterarguments**. Human reviewers should start from that list, not from zero.

### 4. Parallel Session Patterns

Parallel AI sessions are a workforce, not arbitrary task-splitting. Different tabs do different *kinds of thinking*, not different chunks of the same task. Recommended layout for any non-trivial change:

| Tab | Purpose | Skill / prompt |
|---|---|---|
| **Implement** | Write the code for the change. | freeform |
| **Challenge** | Adversarial pass — argue against the plan and the diff. | `/challenge` |
| **Review** | Correctness / security / style review of the diff. | `/review-pr` (on your own branch) |
| **Simplify** | Look for reuse, dead code, premature abstractions. | `simplify` skill (Claude Code global skill) |
| **Validate** | Run lint / test / build and auto-fix. | `/validate`, `/fix-lint` |

**Minimum: two tabs.** Sweet spot: three. You do **not** need ten.

### 5. Decisions & Lessons Learned (Living Constitution)

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

Run the affected service's validation command from [Build Commands by Service Type](#build-commands-by-service-type) — `make validate` (Go), `make lint && make test` (Python with Makefile), the direct `poetry run` commands (Python without), or `npm run lint2 && npm run test` (TypeScript). It must pass before you commit.

### 4. Commit & Push
```bash
git add .
git commit -m "type(scope): description"   # e.g. fix(llm): handle null pointer in config
git push origin feature/description
```

Allowed commit/PR-title types and scopes live in [`.github/semantic.yml`](.github/semantic.yml) — CI validates PR titles against it, so read that file rather than a copy here. Gotcha: the `label-prs` check only accepts **lowercase** scopes in practice (`nb-1234`, not `NB-1234`).

### 5. Create PR to `main`

**GitHub Issue Required:** Every PR targeting `main` MUST link to the **original ticket** that motivated the work (`Fixes #<number>`, or `Part of #<number>` when the ticket spans multiple PRs). Search for the existing ticket first — the user's sprint tickets, then open **and** closed issues by keyword. **Never auto-create a new issue to satisfy this rule** — a fresh unlinked issue per PR destroys traceability and litters the tracker with unassigned orphans. A new issue is a last resort, requires explicit user confirmation, goes through `/create-issue` (which assigns it and adds it to the sprint board), and must reference the parent/original ticket if one exists. This does NOT apply to PRs targeting `test` or `prod` (cherry-picks / promotions).

Use the PR body template at [`.github/pull_request_template.md`](.github/pull_request_template.md) — keep only the applicable "Type of change" options, checked. The `/create-pr` skill automates the full flow (validation, self-review, template, issue link).

**PR Guidelines:**
- CI automatically runs validation; all checks must pass
- Get code review, then merge to `main`

**Opening the PR is not the end of the task.** After opening **any** PR — by whatever means, not only via `/create-pr` — watch it to a settled state: poll CI checks AND review comments (bot reviewers like Gemini post within minutes), fix actionable findings, push, and repeat until every check is green and every actionable comment is fixed or answered. The full loop lives in `/create-pr` Step 11; follow it even when the PR was created manually. Bound the loop (≤ ~4 fix-and-push cycles) and surface anything that needs a human decision instead of looping.

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

## Testing Strategy

- **Unit tests:** Alongside source code, run before commit
- **Integration tests:** `app-e2e-tests/` for cross-service validation
- **Validation order:** Unit → Integration → Deploy to test → Manual in test

## Deployment Checklist

Before merging to production:
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
- **Engine: Atlas Community.** Per-file revisions tracked in `nudgebee.atlas_schema_revisions`. golang-migrate was removed (PR #33008 / Fixes #33007) — its single-row tracker kept producing silent-skip / phantom-version / CONCURRENTLY-in-tx incidents (V752, V756, V758, V760).
- **Flat layout** — files named `{ts_ms}_V{N}_{snake_case_description}.up.sql` and `.down.sql`, no enclosing directory.
- **Use the scaffold script** — `./api-server/migrations/new-migration.sh <snake_case_name>` creates both files with a fresh unix-ms timestamp + next `V<N>` AND regenerates `migrations/app/atlas.sum`. Requires `brew install atlas`; the script refuses without it (atlas.sum staleness fails the migration Job at deploy).
- **Out-of-order arrivals are first-class.** `--exec-order non-linear` (in `atlas.hcl`) applies files with ts < tracker normally. Cherry-picks and HF backmerges no longer silently skip — that incident class is structurally impossible. `validate_migrations.py` now warns (not errors) on out-of-order ts.
- `.down.sql` is optional but recommended. Write idempotent SQL (`IF EXISTS` / `IF NOT EXISTS`).
- **`CREATE INDEX CONCURRENTLY` etc. now work** — put `-- atlas:txmode none` as the first line of the `.up.sql`. Atlas honors the directive (golang-migrate ignored the equivalent `-- migrate:no-transaction` hint, which is why it produced the V752 wedge). `.github/workflows/migrations-lint.yaml` still rejects `CONCURRENTLY` in files that lack the directive.
- **`atlas.sum`** is the integrity manifest. Committed. Edits to applied files cause `atlas migrate apply` to refuse with `checksum mismatch` (desired). Regenerated automatically by `new-migration.sh`; PR-time CI gate (`migrations-validate.yaml`) catches drift.
- Full details on cutover, recovery, and local apply commands: [`api-server/migrations/README.md`](api-server/migrations/README.md).

### Other migration trees
- `migrations/clickhouse/` — kept for historical reference but not applied (CLICKHOUSE_ENABLED has been `false` on every env since cluster setup; the code path was dead and was removed from run-migrations.sh).
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

**Format/Lint Failures:** run the service's auto-format command (`make fmt` / `poetry run black .` / `npm run lint2:fix` — see [Build Commands](#build-commands-by-service-type)), then re-commit.

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

---

**Last updated:** Based on actual tested commands from Makefiles, CI/CD workflows, and Dockerfiles.
Verified: 8 Go + 5 Python + 1 TypeScript = 14 total modules.
