# Nudgebee Monorepo - Development Guide

## Quick Overview

16 interconnected services (10 Go, 5 Python, 1 TypeScript) deployed on Kubernetes. Three-tier environment: main (dev) → test → prod.

## Module Structure

### Go Services (10 modules)
1. `api-server/services/` - Core backend (Gin)
2. `ticket-server/` - Ticket management
3. `collector-server/cloud-collector/` - AWS/cloud data collection
4. `collector-server/otel-collector/` - OpenTelemetry collector
5. `collector-server/k8s-collector/relay-server/` - K8s relay gateway
6. `llm/code-analysis/` - Code analysis engine
7. `llm/llm-server/` - LLM inference service
8. `app-e2e-tests/` - Integration tests
9. `api-server/test_servicemap/` - Service map tests
10. `ops/github-move-tickets/` - GitHub integration

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

<!-- Format:
- **[YYYY-MM] {title}**: Chose {approach} over {alternative}. Why: {reason}. Counterarguments considered: {brief summary}. Reconsider if: {condition}.
-->

- **[2026-04] Templates-as-bare-strings for object-typed workflow params**: Chose permissive `isTemplateString` (contains `{{` or `{%`) bypass in both frontend validators and backend `Schema.Validate` over introducing a `{ $template: "..." }` wrapper object. Why: `ProcessValue`'s simple-variable fast path (templating.go:219-223) and every existing workflow on disk already assume bare-string templates; a wrapper would force a dual-read migration for months. Counterarguments considered: (1) regex heuristic is leaky — literal strings containing `{{` get treated as templates; (2) accepting templates for `integer`/`number`/`boolean` removes the save-time tripwire, shifting type errors to execute time. Reconsider if: we ever need to template *parts* of a map (not just the whole field), or if silent runtime misrenders become a recurring support burden.
- **[2026-05] Single `Optimize` ConversationSource for Optimize-page chats (#29985)**: Chose one canonical `ConversationSource = "Optimize"` plumbed via the existing `source` column, over splitting K8s/Cloud subtypes or filtering on the `recom_` session_id prefix. Why: product wants a single Optimize tab, and `source` (added in migration V452) is the canonical categorization column — session_id-prefix filtering would be data-shape coupling. Counterarguments considered: no backfill (historical chats stay as `UserInvestigation`) and chip-list drift from the Go enum, both accepted as out-of-scope. Reconsider if: product later asks for K8s vs Cloud sub-tabs.

#### What We've Tried and Won't Try Again

<!-- Format:
- **[YYYY-MM] {title}**: Considered {approach} for {problem}. Rejected because: {concrete reason from counterarguments}. Current direction: {replacement}.
-->

_No entries yet — add them after post-mortems or abandoned spikes._

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

### Go Services with Makefile (7 services)
**Applies to:** api-server/services, ticket-server, collector-server/cloud-collector, collector-server/otel-collector, collector-server/k8s-collector/relay-server, llm/llm-server, llm/code-analysis

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

### Go Services WITHOUT Makefile (3 services)
**Applies to:** app-e2e-tests, api-server/test_servicemap, ops/github-move-tickets

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

## Docker Build Process (ALL Services)

**How it works:**
1. Multi-stage build (separate build and runtime stages)
2. Built only on `prod`/`test` branches in GitHub Actions
3. Pushed to AWS ECR: `740395098545.dkr.ecr.us-east-1.amazonaws.com`
4. Image tag format: `{timestamp}_{git-sha}` (e.g., `2025-11-12T14-30-45_abc123def`)

**Build command:**
```bash
docker buildx build --platform linux/amd64,linux/arm64 --push -t {ecr-url}/{service}:{tag} .
```

**Go Services Example (api-server/services):**
```dockerfile
FROM golang:1.26-alpine AS build
  COPY go.mod go.sum ./
  RUN go mod download
  RUN CGO_ENABLED=0 GOOS=linux go build -o /app/service cmd/*.go

FROM alpine:3.19
  RUN apk add git graphviz
  COPY --from=build /app/service /app/
  EXPOSE 8000
  CMD ["/app/service"]
```

**Python Services Example (ml-k8s-server):**
```dockerfile
FROM registry.nudgebee.com/nudgebee-ml-base:* AS builder
  COPY poetry.lock pyproject.toml ./
  RUN uv pip install --system --requirements pyproject.toml
  RUN conda clean --all

FROM registry.nudgebee.com/nudgebee-ml-base:*
  COPY --from=builder /opt/conda /opt/conda
  COPY server ./server
  EXPOSE 9999 8081
  CMD ["sh", "-c", "conda run -n myenv gunicorn ... & wait"]
```

**TypeScript Frontend Example (app):**
```dockerfile
FROM node:22-alpine AS deps
  RUN npm ci --legacy-peer-deps

FROM node:22-alpine AS builder
  COPY --from=deps /app/node_modules ./
  RUN npm run build

FROM node:22-alpine
  COPY --from=builder /app/.next ./.next
  CMD ["npm", "start"]
```

## CI/CD Workflow Pattern

Each service has workflows in `.github/workflows/{service}-{env}.yaml`

**Triggers:**
- Push to branch (main, test, or prod) in service-specific paths
- Pull request to that branch with service changes
- Manual trigger via `workflow_dispatch`

**Typical workflow steps:**
1. Checkout code
2. Setup language environment (Go/Python/Node)
3. Install dependencies
4. Run linters/type checks
5. Run tests
6. (On prod branch only) Build Docker image
7. (On prod branch only) Push to AWS ECR
8. (On prod branch only) Deploy via Helm to K8s

**Deployment (prod branch):**
```bash
# Build and push Docker image
docker buildx build --platform linux/amd64,linux/arm64 --push \
  -t {ecr}:{timestamp}_{git_sha} .

# Deploy via Helm
helm upgrade {service} -f deploy/kubernetes/{service}/values-prod.yaml \
  ./deploy/kubernetes/{service} \
  --set image.tag={timestamp}_{git_sha} \
  --install --namespace nudgebee
```

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

Each service has detailed CLAUDE.md:
- `api-server/services/CLAUDE.md` - API layer, Hasura integration, service patterns
- `ml-k8s-server/CLAUDE.md` - ML pipelines, scaling configuration
- `app/CLAUDE.md` - React components, Next.js patterns
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
| `api-server/hasura/` | Database schema & GraphQL API layer |
| Each service `Dockerfile` | Container image build configuration |
| Each service `Makefile` | Build automation (if service has one) |
| Each service `go.mod`/`pyproject.toml`/`package.json` | Dependencies |

## Hasura Migrations & Metadata

### Migrations
- Migration directories live in `api-server/hasura/migrations/default/`
- Directory names MUST use current timestamp (ms): `$(python3 -c "import time; print(int(time.time() * 1000))")_V{N}_{name}`
- Never use hardcoded/arbitrary timestamps
- NEVER use `CREATE INDEX CONCURRENTLY` — Hasura runs migrations inside a transaction block, which doesn't support it. Use plain `CREATE INDEX` instead.

### Action Naming Convention
When creating Hasura actions, follow this naming pattern:

```
<module>_<verb>_<description>_[<version>]
```

- **module** — Single word identifying the domain: `ai`, `runbooks`, `cloud`, `tickets`, `workflows`, etc.
- **verb** — The operation: `list`, `get`, `create`, `delete`, `enable`, `disable`, `update`, `sync`, etc.
- **description** — What the action does, snake_case.
- **version** — Optional. Only when creating a new version of an existing action (`v2`, `v3`, etc.).

**Examples:**
- `ai_get_tools`
- `ai_list_recommendations`
- `runbooks_create_playbook`
- `cloud_sync_accounts`
- `workflows_delete_schedule`
- `ai_get_tools_v2` (new version of `ai_get_tools`)

### Metadata (actions.yaml, tables, permissions, etc.)
- When modifying any Hasura metadata files (e.g., `actions.yaml`, `tables.yaml`, permissions), ALWAYS test locally first:
  ```bash
  cd api-server/hasura
  hasura metadata apply    # Apply metadata changes
  hasura migrate apply     # Apply pending migrations
  ```
- This catches schema errors, permission issues, and action definition problems before they hit CI/CD.

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
| collector-server/otel-collector | Go | ✓ | make validate |
| collector-server/k8s-collector/relay-server | Go | ✓ | make validate |
| llm/code-analysis | Go | ✓ | make check |
| llm/llm-server | Go | ✓ | make validate |
| app-e2e-tests | Go | ✗ | go test ./... |
| api-server/test_servicemap | Go | ✗ | go test ./... |
| ops/github-move-tickets | Go | ✗ | go test ./... |
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
Verified: 10 Go + 5 Python + 1 TypeScript = 16 total modules.
