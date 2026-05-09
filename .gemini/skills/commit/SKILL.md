---
name: commit
description: Smart commit — detect affected services, run validation, commit with proper format
---

# Smart Commit

Create a validated commit with proper service-scoped message format. Optional argument: `$ARGUMENTS` (commit message override).

## Step 1: Detect Changed Files

```bash
# Staged changes
git diff --cached --name-only

# Unstaged changes (for reference)
git diff --name-only

# Untracked files
git ls-files --others --exclude-standard
```

If nothing is staged, show the user what's unstaged/untracked and ask what to stage.

## Step 2: Map Changes to Services

Map every changed file to its service:

| Path prefix | Service | Type | Validation | Working directory |
|---|---|---|---|---|
| `api-server/services/` | api-server | Go | `make validate` | `api-server/services/` |
| `ticket-server/` | ticket-server | Go | `make validate` | `ticket-server/` |
| `collector-server/cloud-collector/` | cloud-collector | Go | `make validate` | `collector-server/cloud-collector/` |
| `collector-server/otel-collector/` | otel-collector | Go | `make validate` | `collector-server/otel-collector/` |
| `collector-server/k8s-collector/relay-server/` | relay-server | Go | `make validate` | `collector-server/k8s-collector/relay-server/` |
| `collector-server/k8s-collector/app/` | k8s-collector-app | Python | `make lint && make test` | `collector-server/k8s-collector/app/` |
| `llm/code-analysis/` | code-analysis | Go | `make check` | `llm/code-analysis/` |
| `llm/llm-server/` | llm-server | Go | `make validate` | `llm/llm-server/` |
| `llm/rag-server/` | rag-server | Python | `make lint && make test` | `llm/rag-server/` |
| `llm/benchmark/` | benchmark | Python | `poetry run pytest` | `llm/benchmark/` |
| `ml-k8s-server/` | ml-k8s-server | Python | `make lint && make test` | `ml-k8s-server/` |
| `auto-pilot/` | auto-pilot | Python | `poetry run black --check . && poetry run flake8 .` | `auto-pilot/` |
| `auto-pilot/sidecar/` | auto-pilot-sidecar | Python | `poetry run black --check . && poetry run flake8 .` | `auto-pilot/sidecar/` |
| `notifications-server/` | notifications-server | Python | `poetry run black --check . && poetry run flake8 .` | `notifications-server/` |
| `app/` | frontend | TypeScript | `npm run lint2` | `app/` |
| `deploy/` | infrastructure | — | — | — |
| `.github/` | ci | — | — | — |

## Step 3: Run Validation for Each Affected Service

For each affected service, run its validation command. Report results as a table:

```
| Service | Validation | Status |
|---------|-----------|--------|
| api-server | make validate | PASS/FAIL |
```

**If any validation fails:**
- Show the error output
- Ask the user: "Validation failed for {service}. Fix issues and retry, or commit anyway?"
- Do NOT proceed with commit unless user explicitly says to skip validation

## Step 4: Generate Commit Message

If `$ARGUMENTS` is provided, use it as the commit subject but still prepend the semantic type and scope.

**Format:** `type(scope): subject` (per `.github/semantic.yml`)

**Allowed types:**
| Type | Use when |
|---|---|
| `feat` | New feature or functionality |
| `fix` | Bug fix |
| `docs` | Documentation only |
| `style` | Formatting, whitespace, no code change |
| `refactor` | Code restructure, no behavior change |
| `perf` | Performance improvement |
| `test` | Adding or updating tests only |
| `chore` | Maintenance, deps, config |
| `revert` | Reverting a previous commit |
| `ci` | CI/CD workflow changes |
| `infra` | Infrastructure, Helm, K8s changes |
| `release` | Release-related changes |

**Allowed scopes (required):**
| Scope | Services / paths |
|---|---|
| `ui` | `app/` (frontend) |
| `autopilot` | `auto-pilot/`, `auto-pilot/sidecar/` |
| `ml` | `ml-k8s-server/`, `llm/code-analysis/`, `llm/llm-server/`, `llm/rag-server/`, `llm/benchmark/` |
| `notifications` | `notifications-server/` |
| `tickets` | `ticket-server/` |
| `relay` | `collector-server/k8s-collector/relay-server/` |
| `collector" | `collector-server/cloud-collector/`, `collector-server/otel-collector/`, `collector-server/k8s-collector/app/` |
| `deps` | Dependency updates |
| `#xxx` | Issue number — use for `api-server/services/`, `api-server/hasura/`, `deploy/`, `.github/`, or any cross-service change |

**Rules:**
- **Single scope:** `fix(ui): handle null pointer in settings page`
- **Issue number scope:** `feat(#123): add Azure onboarding flow`
- Scope is **required** — if no dedicated scope matches, use an issue number `#xxx`

If no `$ARGUMENTS`, analyze the diff to generate an appropriate message. Present the proposed message to the user for confirmation before committing.

## Step 5: Stage and Commit

Stage files if not already staged (ask user first if there are unstaged changes to include):

```bash
git add <specific files>
```

Commit:
```bash
git commit -m "type(scope): subject"
```

**NEVER use `git add -A` or `git add .`** — always add specific files.

## Step 6: Output Summary

```
Committed: type(scope): subject
  {N} files changed, {A} insertions(+), {D} deletions(-)
  Services: {list}
  Validation: all passed
```
