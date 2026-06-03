# Nudgebee Monorepo - Development Guide

> **New here?** Start with the [root README](README.md) — it covers what Nudgebee is, how to bring up a local stack, and the per-service entry points. This file (`CLAUDE.md`) is primarily guidance for AI coding agents (Claude Code, etc.), but a few sections are worth a human contributor's time:
>
> - **[Architecture Decisions](#architecture-decisions)** — the living "why-behind-X" log; reasoning behind structural choices we don't want to re-litigate.
> - **[RPC action naming convention](#rpc-action-naming-convention)** — the verb taxonomy every new action in `app/src/lib/actions.yaml` must follow.
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

<!-- Format:
- **[YYYY-MM] {title}**: Chose {approach} over {alternative}. Why: {reason}. Counterarguments considered: {brief summary}. Reconsider if: {condition}.
-->

- **[2026-04] Templates-as-bare-strings for object-typed workflow params**: Chose permissive `isTemplateString` (contains `{{` or `{%`) bypass in both frontend validators and backend `Schema.Validate` over introducing a `{ $template: "..." }` wrapper object. Why: `ProcessValue`'s simple-variable fast path (templating.go:219-223) and every existing workflow on disk already assume bare-string templates; a wrapper would force a dual-read migration for months. Counterarguments considered: (1) regex heuristic is leaky — literal strings containing `{{` get treated as templates; (2) accepting templates for `integer`/`number`/`boolean` removes the save-time tripwire, shifting type errors to execute time. Reconsider if: we ever need to template *parts* of a map (not just the whole field), or if silent runtime misrenders become a recurring support burden.
- **[2026-05] Single `Optimize` ConversationSource for Optimize-page chats (#29985)**: Chose one canonical `ConversationSource = "Optimize"` plumbed via the existing `source` column, over splitting K8s/Cloud subtypes or filtering on the `recom_` session_id prefix. Why: product wants a single Optimize tab, and `source` (added in migration V452) is the canonical categorization column — session_id-prefix filtering would be data-shape coupling. Counterarguments considered: no backfill (historical chats stay as `UserInvestigation`) and chip-list drift from the Go enum, both accepted as out-of-scope. Reconsider if: product later asks for K8s vs Cloud sub-tabs.
- **[2026-05] ClusterIP→K8sService resolution: defense-in-depth via shared `ResolveIPToK8sService` helper**: Chose two coordinated fixes (source-level short-circuit in traces/ebpf flow sources + new `K8sServiceIPMatchStrategy` enricher backstop) backed by a single canonical helper in `flow_sources/k8s_service_ip_resolver.go`, over enricher-only or source-only fixes. Why: source-level prevents new orphan ExternalService nodes at creation time; backstop catches anything that slips through (future flow sources, stale `req.ExistingNodes`); shared helper owns port-stripping + special-IP skip list + plurality caller-cluster rule so the 3 call sites can't drift. Plurality (most-common cluster, tie→empty→global-unique fallback) chosen over a hard-threshold majority rule to avoid boundary oscillation between builds. Counterarguments considered: (1) interface change to `MatchingStrategy.Match` to pass `extSvc` — rejected, only one strategy needs it; key `CallerClusterIndex` by name instead; (2) DRY across 4 resolution sites including pre-existing newrelic — partial: helper exists, newrelic migration is a follow-up; (3) legacy 672 DB orphan rows survive (SaveNodes upserts, `markInactiveNodes` doesn't touch ExternalService) — user accepted as residual, cleanup deferred to a separate `kg_cleanup_orphan_ip_external_services` RPC action. Reconsider if: pod-IP-named ExternalServices show up at scale (needs a parallel `PodIPResolver`), or if the plurality rule produces wrong resolutions in multi-cluster tenants with skewed traffic.
- **[2026-05] LLM config precedence: DB always beats ENV at any specificity** (llm-server `agents/core/llm_config.go`): Reordered every layered resolver — `ResolveLLMConfig`, `getLLMApiKey`, `getLLMFallbackModelName`, `getLLMApiEndpoint`, `getLLMApiVersion`, `getLLMApiType`, `getLLMRegion`, `resolveLLMSecret` — from the old "specificity wins regardless of source" (env-global → db-global → env-tier → db-tier → env-agent → db-agent) to "ENV block first, DB block on top" (env-global → env-tier → env-agent → db-global → db-tier → db-agent → conversation → context-override). Why: the old order let a stale operator `LLM_PROVIDER_API_KEY_<AGENT>` ENV override silently win over a tenant's canonical DB-global key, fragmenting Google AI CachedContent ownership across calls in one process → 403 PERMISSION_DENIED storms when sibling calls referenced slots owned by a different project. In a multi-tenant product where clients onboard via UI (→ `integration_config_values`), the DB row is authoritative; ENV is operator process default, never a tenant override. Counterarguments considered: (1) operators relying on ENV-agent for emergency overrides now need a DB UPDATE — accepted given the tenant-fragmentation risk; (2) the older "specificity wins" rule was internally consistent and easier to explain — true, but only in ENV-only deployments. Reconsider if: an operator-only emergency override mechanism is needed that bypasses DB without UPDATE access (suggests a dedicated emergency key namespace, not re-elevating ENV-agent).
- **[2026-05] Ticket create-meta: backend-declared field ownership + live-fetch behind Redis (#29541)**: Redesigned the workflow "Create Ticket" field subsystem instead of re-patching. (1) **Ownership contract** — added `FieldInfo.Group` (`severity`/`title`/`description`/`""`) to the `{"data":[Template]}` create-meta payload; the tool builder (which alone knows platform semantics) tags the field backing each basic control, and the frontend renders Platform Fields as `!group` and sources Severity from the `group==='severity'` field. This replaced a hardcoded `key !== 'summary'/'description'/'priority'/'urgency'` filter **plus** a separate `urgency→priority` string alias **plus** a hook skip — three drift-prone encodings of the same fact — with one. Why: a Jira custom field named "Severity" or any synonym slipped the string filter and rendered twice. (2) **Severity flows only via `ticket.Severity`** end-to-end; option `value` is the exact API token each tool consumes (Jira priority **ID**, ZD/PD/SNOW urgency token), and the hook stops preferring `id` — fixing "loads ≠ applies" (ZenDuty silently defaulted to Medium; PagerDuty create ignored urgency entirely). Jira create sets `Priority{ID}` once (removed the `additionalFields["priority"]` double-write); runbook `tickets.create` migrates legacy `additional_fields.priority/urgency` → severity. (3) **Reliability** — Jira severity/assignee fetch live (`Priority.GetList` + first-page assignable users) and seed/synthesize, so they populate even when createmeta omits allowedValues or GDPR-strict blocks user search; **no dependency on the `integration_config_values` snapshot** (product decision). PagerDuty gained an urgency field + create wiring; ServiceNow `GetCreateMeta` implemented (urgency + live `sys_user` assignee, no labels). (4) **Caching** — `services/cache` (eko/gocache redis+bigcache mirror of api-server, separate module so can't import) wraps the unified registry seam `fetchCreateMetaCached`, 30-min TTL, graceful degradation (Redis down ⇒ live), invalidated by integration tag inside `updateMetadataConfigValues` (covers save/test/bi-hourly sync). Redis comes free via the shared `nudgebee` secret (`CACHE_PROVIDER=redis`) — no k8s change. Counterarguments considered: (1) extra live Jira calls per cache miss — bounded (one priority call + first-page users, only on 30-min miss); (2) snapshot-only with no Redis would also fix the matrix since #29541 is a correctness not latency bug — Redis kept per product decision. Reconsider if: create-meta latency was never the real pain (then drop Redis for snapshot-warm), or pod-scale Jira user lists need server-side search instead of first-page seeding.
- **[2026-06] Webhook auth model — edge verifies provider identity, notifications gates on an internal token**: Inbound chat webhooks are authenticated at the Next.js edge (`app/src/pages/api/webhooks/*`): Slack by HMAC signature (signing secret required; reject missing headers; 5-min replay window), Google Chat and MS Teams by provider JWT (Google certs / Bot Framework `login.botframework.com`, audience = app id; MS Teams adds a `serviceurl`-claim match + 5-min clock tolerance). The edge forwards to `notifications-server` stamping `X-ACTION-TOKEN`; every notifications `/webhooks/*` endpoint requires that token (constant-time check in `notifications_server/security/webhook_auth.py`). `/webhooks/slack/events` is also routed directly to notifications by the app ingress, so that endpoint additionally accepts a Slack HMAC over the raw body (`slack_sdk.SignatureVerifier`, reading `settings.slack.signing_secret` — not the placeholder-defaulting `slack_app` secret). MS Teams uses a Bot Framework Bearer JWT, not an `x-ms-signature` body HMAC (see the entry below). Every check requires its secret/token to be configured. Reconsider if: the direct `/webhooks/slack/events` ingress route is removed (Slack verification then lives only at the edge). Follow-ups: a dedicated `EDGE_TO_NOTIFICATIONS_TOKEN` instead of reusing `ACTION_API_SERVER_TOKEN`; `bodyParser:false` + raw-body read on the Slack edge handlers (they currently recompute the HMAC base string from a re-serialized body). Ops note: `ACTION_API_SERVER_TOKEN` must be pinned (explicit value or `existingSecretName`) so the edge and notifications share one value — a GitOps lookup-or-generate can otherwise regenerate it per reconcile, after which edge-forwarded webhooks stop being accepted.

#### What We've Tried and Won't Try Again

<!-- Format:
- **[YYYY-MM] {title}**: Considered {approach} for {problem}. Rejected because: {concrete reason from counterarguments}. Current direction: {replacement}.
-->

- **[2026-06] `x-ms-signature` HMAC for the MS Teams webhook** (`app/src/pages/api/webhooks/msteams/events.ts`): MS Teams (Azure Bot Service) authenticates inbound activities with a **Bearer JWT** validated against Microsoft's OpenID metadata (issuer `https://api.botframework.com`, audience = app id) — **not** an `x-ms-signature` body HMAC keyed by the OAuth client secret. That header is the Teams Outgoing-Webhook/connector scheme and is not part of the Bot Framework protocol (the OAuth client secret is the wrong key regardless). Validate the Bearer JWT (with a `serviceurl`-claim match) instead — see `verifyBotFrameworkJwt` in `msteams/events.ts`.

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
- **NEVER use `CREATE INDEX CONCURRENTLY`** — golang-migrate wraps each migration in a transaction by default, and `CREATE INDEX CONCURRENTLY` cannot run inside one. If you genuinely need it, add `-- migrate:no-transaction` at the top of the file.
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

Pick the verb that matches the operation's *intent and return shape*, not the verb that "sounds close." When two verbs both fit, prefer the more specific one.

**Read operations**

| Verb | Use when | Returns | Example |
|---|---|---|---|
| `get` | Fetch **one** record by id or unique key | single object or 404 | `users_get_current`, `recommendations_get` |
| `list` | Fetch a **collection** (with filters / pagination / ordering, including relevance ordering) | array | `accounts_list`, `insights_list` |
| `aggregate` | Group / sum / count / bucket | aggregation object | `accounts_aggregate` |
| `count` | Just a number | int | `alerts_count` |
| `check` | Validate / probe, returns yes/no/status | bool or status enum | `clusters_check_health`, `usergroups_check_name_exists` |

**Write operations**

| Verb | Use when | Example |
|---|---|---|
| `create` | Insert a new record | `agents_create_token` |
| `update` | Modify an existing record | `tickets_update_status` |
| `upsert` | Insert-or-update | `settings_upsert` |
| `delete` | Remove a record | `agents_delete` |
| `apply` | Execute already-prepared changes (i.e. caller has the diff/plan in hand) | `recommendations_apply` |

> **Decision tree — `create` vs `update` vs `upsert` vs `apply`:**
> - **Caller knows the record doesn't exist** → `create`. Fails if duplicate.
> - **Caller knows the record exists** → `update`. Fails if missing.
> - **Caller is indifferent / idempotent** → `upsert`. Always succeeds if input is valid.
> - **Caller has a pre-computed diff/plan and the server just executes it** → `apply` (e.g. `recommendations_apply` takes a ready-made set of changes). `apply` is closer to `execute` than to `update` — the server's job is to enact, not to compute.
> - **Status / flag change** → `update` (see status-change note below). Don't reach for `apply` just because the input looks like a state transition.

> **`convert` is a one-off, not a general verb.** Used only for `applications_convert_profile` (transforms a profile payload between two representations — no record is created, updated, or deleted; the operation is a pure transformation that returns the converted form). Don't generalize: a `convert` that writes a new record is `create`; a `convert` that mutates an existing record in place is `update`; a `convert` that exchanges format for client display only is `get_*_as_<format>`. If you find yourself wanting a second `convert`, audit whether `create` / `update` / `get` would fit before adding it.

**Action / job operations**

| Verb | Use when | Example |
|---|---|---|
| `execute` | Run a defined operation (sync or async) | `anomaly_execute`, `playbook_execute` |
| `replay` | Re-run an existing prior execution (lineage/state from the original matters) | `workflow_replay_execution` |
| `cancel` | Halt a long-running operation gracefully (allows cleanup) | `ai_cancel_investigation`, `workflow_cancel_execution` |
| `pause` / `resume` | State-machine transitions on a running operation (preferred over `update_state` when the transition has side-effects like checkpointing/freezing schedules) | `workflow_pause`, `workflow_resume` |
| `publish` / `unpublish` | Versioning + rollout operations (different from a simple field update because rollout semantics apply) | `workflow_publish_version` |
| `sync` | Trigger an external-data sync | `accounts_sync` |
| `generate` | Compute & return a fresh artifact (recommendation, token, report) | `ml_generate_node_recommendations` |
| `enable` / `disable` | Toggle a boolean flag (no state-machine complexity) | `integrations_enable` |

> **Note on messaging/notification dispatch:** There is no `send` verb. For programmatic delivery use `execute` (`notifications_execute_alert`). For rollout-style fan-out use `publish`. Test/verify-the-setup actions use `check` (`notifications_check_connection`).

> **Note on status / flag changes:** Don't invent verbs for state transitions on a flag field (`activate`/`deactivate`/`toggle`/`approve`/`reject`/`acknowledge`/`snooze`). Use `update` — the action name describes the field being updated, not the direction. Examples: `events_update_resolution`, `events_update_classification`, `events_update_rule_override`, `tenant_update_registration_status`. Reserve `pause`/`resume`/`cancel` for state-machine transitions on a *running operation* (where the transition has side-effects like checkpointing) and `enable`/`disable` only when there's a long-standing convention on the resource (e.g. `integrations_enable`).

> **Note on integration onboarding URLs:** Cloud-integration handlers that return a deep-link URL for the user to complete onboarding in the provider console (CloudFormation, ARM template, GCP Deployment Manager) are `get` actions, not `create`. Name them `<provider>_get_onboard_<service>_url` (e.g. `aws_get_onboard_eventbridge_url`, `azure_get_onboard_eventgrid_url`). The `onboard` qualifier disambiguates the URL's purpose; `get` reflects that no persistent record is created server-side — the actual integration record gets created by a separate callback (`*_create_*`) once the user completes the provider-side flow.

> **Note on tenant-scope and cross-tenant ops.** Tenant scope is intrinsic to every action (see [[project_auth_model]]). NB has **no super-admin UI that does cross-tenant operations** — super-admin in UI just unlocks elevated *within-tenant* permissions (e.g. `LLMConsumptionTab` budget toggle). The genuinely cross-tenant operations all live **server-side**: NextAuth callbacks, `/api/auth/signup*`, `/api/auth/token`, and SAML use a synthetic admin context to look up users by email before login, route domains to tenants, etc.
>
> The `admin_*` module convention introduced in PR #31324 was based on the wrong premise (it assumed a super-admin cross-tenant UI exists). The only `admin_*` action that shipped (`admin_list_tenants`) had zero UI callers after the SwitchTenant.jsx fix in PR #31372 — deleted. **Don't introduce `admin_*` actions.** When NB eventually formalizes the "server-side-only cross-tenant" surface, the right convention will be either a dedicated `internal_*` module or a separate backend mount (`/internal/*`) — see [[project_action_rename_followups]] item 6a for the open design question.

**Avoid**

- `trigger_*` — redundant with `execute` / `sync`; pick one.
- `fetch_*` — use `get` or `list`.
- `find_*`, `search_*` — use `get` (by id) or `list` (collection, including relevance-ordered).
- `do_*`, `handle_*`, `process_*` — too vague to convey intent.
- Bare `query` as a verb — that's the transport, not the operation. Be specific: `list` or `aggregate`.
- `test_*`, `validate_*` — use `check` (umbrella verb for probe/validate/test operations).
- `save_*` — use `create` (write-only) or `upsert` (idempotent write). If the operation marks/bookmarks a record by inserting into a side table, name the action after the side table: `<module>_create_<side_table>` (e.g. `ai_create_saved_conversation`, not `ai_save_conversation`).
- `onboard_*` / `*_onboard` — use `create` (for integration onboarding) or `register` (for self-service signup).
- `map_*` / `unmap_*`, `link_*` / `unlink_*` — model the mapping as a first-class resource and use `create` / `delete` on it (e.g. `ai_create_kb_mapping`, `ai_delete_kb_mapping`).
- `stop_*` — use `cancel`.

**Examples (good):**
- `ai_get_tools`
- `accounts_list`
- `runbooks_create_playbook`
- `accounts_sync`
- `workflows_delete_schedule`
- `ai_get_tools_v2` (only because `ai_get_tools` still exists)

**Hasura-style table queries (carve-out):** A subset of read operations are named `<module>_<entity>_v[N]` (e.g. `k8s_pods_v2`, `cloud_resource_v2`) and predate this convention. They are internally consistent and **not** renamed in bulk. New table queries should follow the verb taxonomy above (`<module>_list`, `<module>_aggregate`, etc.).

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
