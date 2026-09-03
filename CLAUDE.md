# Nudgebee Monorepo - Development Guide

> **New here?** Start with the [root README](README.md) — it covers what Nudgebee is, how to bring up a local stack, and the per-service entry points. This file (`CLAUDE.md`) is primarily guidance for AI coding agents (Claude Code, etc.), but a few sections are worth a human contributor's time:
>
> - **[Architecture Decisions](docs/architecture-decisions.md)** — the living "why-behind-X" log; reasoning behind structural choices we don't want to re-litigate.
> - **[RPC action naming convention](docs/rpc-action-naming.md)** — the verb taxonomy every new action in `app/src/lib/actions.yaml` must follow.
> - **[Database Migrations & RPC Actions](#database-migrations--rpc-actions)** — Atlas engine, the migration scaffolding script, and why `CREATE INDEX CONCURRENTLY` and batched data migrations must be run out-of-band.
> - **Build commands** — the per-service `make validate` / `npm run lint2` flows.
> - **[Definition of Done](#2-definition-of-done)** — what "done" means here beyond green unit tests.

## Module Structure

15 interconnected services deployed on Kubernetes. Three-tier environment: main (dev) → test → prod.

**Go (10):** `api-server/services/` (core backend, Gin) · `ticket-server/` · `collector-server/cloud-collector/` · `collector-server/k8s-collector/relay-server/` (K8s relay gateway) · `collector-server/cost-server/` (OpenCost-based) · `llm/gateway/` (AI Gateway) · `llm/code-analysis/` · `llm/llm-server/` · `app-e2e-tests/` · `api-server/test_servicemap/`

**Python (5+):** `ml-k8s-server/` · `llm/rag-server/` · `collector-server/k8s-collector/app/` · `auto-pilot/` (+ `auto-pilot/sidecar/`) · `notifications-server/` · `llm/benchmark/`

**TypeScript (1):** `app/` — frontend dashboard (Next.js)

## AI Coding Principles

These principles apply to every agent (Claude, Gemini, human) working in this repo. Two failure modes dominate AI-assisted coding here, and the principles target both: code that exactly matches a *wrong* request (principles 1, 4), and changes that ship incomplete and get patched one follow-up fix at a time (principles 2, 3).

### 1. Baseline Behavioral Rules (Every Change, Including Small Ones)

1. **Think before coding — don't assume, don't hide confusion.** State assumptions explicitly. If multiple interpretations of the request exist, present them — don't pick one silently. If a simpler approach exists, say so. If something is unclear, stop and ask.
2. **Simplicity first.** Write the minimum code that solves the stated problem. No features beyond what was asked, no abstractions for single-use code, no unrequested "flexibility". Ask: *"Would a senior engineer say this is overcomplicated?"* If yes, simplify.
3. **Surgical changes.** Touch only what the request requires. Don't "improve" adjacent code, comments, or formatting; match existing style. If you notice unrelated dead code, mention it — don't delete it. **Do** remove imports/variables/functions that *your own change* orphaned. The test: every changed line traces directly to the user's request.
4. **Goal-driven execution.** Turn the task into a verifiable goal before starting: "fix the bug" → "write a test that reproduces it, then make it pass". Loop until verified — the closing check is the affected service's validation command ([Build Commands](#build-commands)).

### 2. Definition of Done

"Merged" is not "done". A change is done when:

- **Validation passes locally** for every affected service ([Build Commands](#build-commands)) — before commit, always.
- **Cross-cutting changes ship complete.** If the change threads something through many places — a payload field, trace/log context, a rename, an API contract — enumerate every producer/consumer/call site with a sweep (grep) *before* opening the PR, and cover them all in that PR. Do not ship the happy path and discover the remaining call sites one follow-up fix at a time.
- **Behavior was observed, not assumed.** For anything with cross-service runtime surface, exercise the affected flow once (local run, e2e test, or dev environment) and put the evidence — command + output, event id, screenshot — in the PR body. Green unit tests prove nothing broke; they don't prove the change does what it claims.
- **Feature flags ship in the first PR** of a risky feature, not retrofitted after the incident.
- **The commit type is honest.** `fix` repairs a defect; new capability is `feat` even when it closes a gap. Metrics, release notes, and future churn analysis depend on this.
- **One logical change = one PR.** Don't split one change into many same-day PRs; batch mechanical work (dependency bumps, version bumps) instead of one PR each.

### 3. Fix the Class, Not the Instance (Rule of Three)

Before fixing a bug, check whether it has been fixed before: `git log --oneline --grep=<symptom>` and a search of open/closed issues. If this is the **third fix for the same failure class** in a subsystem, stop patching — file and do the structural fix instead. (Precedent: seven golang-migrate patches never stuck; the Atlas cutover ended that incident class permanently. The structural fix is usually cheaper than the next three patches.)

### 4. Argue Before You Accept (Adversarial Pre-Implementation)

Before implementing any non-trivial change, run an adversarial pass **first**: restate the approach, find the three strongest structural objections, ask what a senior engineer would criticize six months from now, and emit a binding verdict — `PROCEED`, `REVISE`, or `REDESIGN`. Do not start coding on anything but `PROCEED`. The `/challenge` skill runs this on demand.

**Mandatory for:** anything multi-day, new features touching shared types, API contracts, DB schema, cross-service behavior, architectural choices. For multi-day work, also write a short spec with concrete acceptance criteria (which artifact — event, screen, output — proves it works) *before* any code; execute against the spec, ideally in a fresh session.
**Skip for:** typos, formatting, 1-line bug fixes, docs-only changes, purely internal refactors with no interface change.

### 5. Fresh-Context Review (Generator ≠ Evaluator)

The agent that wrote a change is a poor judge of it. Every PR gets a review pass from a **fresh context** before a human sees it — a subagent reviewing the diff, `/review-pr` from another session, or the `/create-pr` self-review step. The reviewer sees the diff and the intent, not the reasoning that produced the change. Human reviewers start from its findings, not from zero.

### 6. Decisions & Lessons Learned

The architecture-decision log and the "What We've Tried and Won't Try Again" log live in **[`docs/architecture-decisions.md`](docs/architecture-decisions.md)**. Read it before any change touching shared types, API contracts, DB schema, or cross-service behavior; append new entries there (the `/challenge` skill appends `PROCEED` architectural decisions automatically). When a gotcha bites a second time, don't just note it — turn it into a check (test, lint rule, CI gate) so it can't bite a third.

## Environment & Branches

```
main (dev) ─PR─> test (staging) ─PR─> prod (production)
   ↑                   ↑                    ↑
   └─── Backmerge ───┴─── Backmerge ──────┘ (hotfixes)
```

- Every merge to `main` → Auto PR to `test`; every merge to `test` → Auto PR to `prod`
- Hotfix: direct to `prod` → backmerge to test → backmerge to main

## Build Commands

Each service's Makefile / package.json is the source of truth — read it for the full target list. The validation commands (must pass before every commit):

| Service type | Services | Validate with |
|---|---|---|
| Go with Makefile | api-server/services, ticket-server, cloud-collector, relay-server, cost-server, llm-server, llm/gateway, code-analysis | `make validate` (fmt + lint + test); `make fmt` to auto-format |
| Python with Makefile | ml-k8s-server, rag-server, k8s-collector/app | `make lint && make test`; `make fmt` to auto-format (black, line-length 120) |
| Python without Makefile | auto-pilot (+sidecar), notifications-server, llm/benchmark | `poetry install`, then `poetry run black --check . && poetry run flake8 . && poetry run pytest` |
| Go without Makefile | app-e2e-tests, api-server/test_servicemap | `go test ./...` |
| TypeScript | app | `npm run lint2 && npm run test`; `npm run lint2:fix` to auto-fix. Install with `npm ci --legacy-peer-deps` |

Docker builds and deploy steps: each service's `Dockerfile` and `.github/workflows/{service}-{env}.yaml` are the source of truth — read them directly; don't invent commands that aren't there.

## Local Development Workflow

1. **Branch** from `main`: `feature/description` or `fix/description`.
2. **Make changes**, then **validate locally** (table above) before committing.
3. **Commit** as `type(scope): description`. Allowed types/scopes live in [`.github/semantic.yml`](.github/semantic.yml) — CI validates PR titles against it. Gotcha: the `label-prs` check only accepts **lowercase** scopes (`nb-1234`, not `NB-1234`).
4. **Create PR to `main`** — use the template at [`.github/pull_request_template.md`](.github/pull_request_template.md); the `/create-pr` skill automates the full flow.

**GitHub Issue Required:** Every PR targeting `main` MUST link to the **original ticket** that motivated the work (`Fixes #<number>`, or `Part of #<number>` when the ticket spans multiple PRs). Search for the existing ticket first — the user's sprint tickets, then open **and** closed issues by keyword. **Never auto-create a new issue to satisfy this rule** — a fresh unlinked issue per PR destroys traceability. A new issue is a last resort, requires explicit user confirmation, goes through `/create-issue`, and must reference the parent/original ticket if one exists. Does not apply to PRs targeting `test` or `prod` (cherry-picks / promotions).

**Opening the PR is not the end of the task.** Watch it to a settled state — but **never with foreground sleep/poll loops**. Start the watch as a background task (`gh pr checks --watch` in the background, or a Monitor on the check status) and keep working; when it reports, triage per `/create-pr` Step 11: fix actionable findings (CI failures AND review comments — bot reviewers post within minutes), push, re-watch. Bound the loop (≤ ~4 fix-and-push cycles) and surface anything needing a human decision instead of looping.

After merge, the automation promotes `main` → `test`; validate there ([Definition of Done](#2-definition-of-done)) before promoting `test` → `prod`.

## Service-Specific Documentation

Each service has its own CLAUDE.md where one exists — **always read it before working on that service:**
- `app/CLAUDE.md` — React components, Next.js patterns, design system
- `llm/llm-server/CLAUDE.md` — LLM service architecture
- `llm/code-analysis/CLAUDE.md` — Code-analysis engine
- `collector-server/k8s-collector/relay-server/CLAUDE.md` — K8s relay gateway
- `api-server/services/knowledge_graph/CLAUDE.md` — Knowledge graph subsystem
- `api-server/services/anomoly/CLAUDE.md` — Anomaly detection subsystem

## Key Files & Locations

| Path | Purpose |
|------|---------|
| `.github/workflows/` | CI/CD automation for all services |
| `deploy/kubernetes/` | Helm charts & values files for each service |
| `deploy/containers/` | Base Dockerfiles (clickhouse, rabbitmq) |
| `api-server/migrations/` | Database migrations — Postgres (`app/`, applied by Atlas), RabbitMQ. See [`api-server/migrations/README.md`](api-server/migrations/README.md). |

## Database Migrations & RPC Actions

### Postgres migrations (`api-server/migrations/migrations/app/`)
- **Engine: Atlas Community.** Per-file revisions tracked in `nudgebee.atlas_schema_revisions`. golang-migrate was removed (PR #33008 / Fixes #33007) — its single-row tracker kept producing silent-skip / phantom-version / CONCURRENTLY-in-tx incidents (V752, V756, V758, V760).
- **Flat layout** — files named `{ts_ms}_V{N}_{snake_case_description}.up.sql` and `.down.sql`, no enclosing directory.
- **Use the scaffold script** — `./api-server/migrations/new-migration.sh <snake_case_name>` creates both files with a fresh unix-ms timestamp + next `V<N>` AND regenerates `api-server/migrations/migrations/app/atlas.sum`. Requires `brew install atlas`; the script refuses without it (atlas.sum staleness fails the migration Job at deploy).
- **Out-of-order arrivals are first-class.** `--exec-order non-linear` (in `atlas.hcl`) applies files with ts < tracker normally. Cherry-picks and HF backmerges no longer silently skip. `validate_migrations.py` warns (not errors) on out-of-order ts.
- `.down.sql` is optional but recommended. Write idempotent SQL (`IF EXISTS` / `IF NOT EXISTS`).
- **`CREATE INDEX CONCURRENTLY` does NOT work in a migration file, and `-- atlas:txmode none` does not enable it.** That directive is a paid-tier Atlas feature; the pinned Community **v1.3.0 silently ignores it**, so the statement still runs inside the per-file transaction and dies with `cannot run inside a transaction block`. Proved by the V868 CI failure on commit `d91deefa`, which carried the directive on line 1 and failed anyway. `.github/workflows/migrations-lint.yaml` therefore rejects `CONCURRENTLY` in executable migration SQL **unconditionally** (the directive does not exempt a file). Instead, follow that gate's recipe: build the index out-of-band with `CREATE INDEX CONCURRENTLY IF NOT EXISTS` against each live database *before* merging, and write the migration as plain `CREATE INDEX IF NOT EXISTS` — a no-op where you pre-applied it. The same constraint rules out batched / `COMMIT`-per-chunk data migrations: a data backfill is one transaction, so order its statements to be as cheap as possible (add the column, backfill, *then* build the index — an UPDATE to an indexed column can't be a HOT update) and state the expected lock window in a comment at the top of the file. Note that out-of-band operator scripts are **not** an option for anything on-prem installs need: they run the same migration image via a `post-install,post-upgrade` Helm hook with `backoffLimit: 0` and have none of our context, so whatever must happen everywhere has to be in the migration itself.
- **`atlas.sum`** is the integrity manifest. Committed. Edits to applied files cause `atlas migrate apply` to refuse with `checksum mismatch` (desired). Regenerated automatically by `new-migration.sh`; PR-time CI gate (`migrations-validate.yaml`) catches drift.
- Full details on cutover, recovery, and local apply commands: [`api-server/migrations/README.md`](api-server/migrations/README.md).

### Other migration trees
- `migrations/clickhouse/` — kept for historical reference but not applied (CLICKHOUSE_ENABLED is `false` on every env; the code path was removed from run-migrations.sh).
- `migrations/rabbitmq/` — shell scripts (`NNN_*.sh`) run sequentially after RabbitMQ is healthy.

### RPC action naming convention
Actions are HTTP RPC handlers registered in [`app/src/lib/actions.yaml`](app/src/lib/actions.yaml) — the routing table the in-app gateway (`@lib/rpcGateway`) uses to dispatch each operation to its upstream handler (mounted under `/rpc/*` on each backend service). New actions follow:

```
<module>_<verb>_<description>_[<version>]
```

- **module** — single word identifying the domain (`ai`, `runbooks`, `cloud`, `tickets`, …); shortest accurate noun.
- **verb** — from the taxonomy in **[`docs/rpc-action-naming.md`](docs/rpc-action-naming.md)**. Quick reference: reads → `get` / `list` / `aggregate` / `count` / `check`; writes → `create` / `update` / `upsert` / `delete` / `apply`; jobs → `execute` / `sync` / `generate` / `cancel` / `pause`·`resume` / `publish` / `enable`·`disable`. Don't invent verbs (no `trigger_*`, `fetch_*`, `save_*`, `test_*`, `admin_*`).
- **description** — snake_case; may be empty when the verb fully describes the action (`accounts_list`).
- **version** — only when a `v1` still exists in `actions.yaml`, or a `v3` is genuinely planned. Otherwise drop the suffix. Hasura-style table queries (`k8s_pods_v2`) are a pre-existing carve-out, not renamed in bulk.

**Validating actions.yaml changes:** `cd app && npm run dev` (parses actions.yaml at boot — fails fast on malformed files; exercise the changed action end-to-end, since type errors in upstream Go handlers only surface as 400/500s at runtime), then `npm run lint2`.
