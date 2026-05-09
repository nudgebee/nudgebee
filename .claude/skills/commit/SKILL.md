---
description: Smart commit — detect affected services, run validation, commit with proper format
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
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
| `ml` | `ml-k8s-server/` |
| `llm` | `ml-k8s-server/`, `llm/code-analysis/`, `llm/llm-server/`, `llm/rag-server/`, `llm/benchmark/` |
| `workflow` | `workflow-server/` |
| `notifications` | `notifications-server/` |
| `tickets` | `ticket-server/` |
| `relay` | `collector-server/k8s-collector/relay-server/` |
| `collector` | `collector-server/cloud-collector/`, `collector-server/otel-collector/`, `collector-server/k8s-collector/app/` |
| `deps` | Dependency updates |
| `#xxx` | Issue number — use for `api-server/services/`, `api-server/hasura/`, `deploy/`, `.github/`, or any cross-service change |

**Rules:**
- **Single scope:** `fix(ui): handle null pointer in settings page`
- **Issue number scope:** `feat(#123): add Azure onboarding flow`
- Scope is **required** — if no dedicated scope matches, use an issue number `#xxx`

If no `$ARGUMENTS`, analyze the diff to generate an appropriate message. Present the proposed message to the user for confirmation before committing.

## Step 5: Stage and Commit

**IMPORTANT — Single commit per PR policy (main branch only):** PRs targeting `main` MUST have exactly one commit. This does NOT apply to branches targeting `test` or `prod` (cherry-picks / promotions) — those can have multiple commits.

**NEVER use `git add -A` or `git add .`** — always add specific files:

```bash
git add <specific files>
```

Then determine the base branch and commit count:

```bash
# Detect base branch (default: main)
BASE_BRANCH="main"
COMMIT_COUNT=$(git log origin/${BASE_BRANCH}..HEAD --oneline 2>/dev/null | wc -l | tr -d ' ')
```

**For branches targeting `main`**, enforce single commit:

```bash
if [ "$COMMIT_COUNT" -eq 0 ]; then
  # First commit on this branch
  git commit -m "type(scope): subject"
elif [ "$COMMIT_COUNT" -eq 1 ]; then
  # Amend the existing single commit
  git commit --amend --no-edit  # or with -m "..." to update the message
else
  # Multiple commits exist — squash all into one
  git reset --soft origin/${BASE_BRANCH}
  git commit -m "type(scope): subject"
fi
```

- If amending or squashing, ask the user if the commit message should be updated to reflect the new changes.
- After squash, a `--force-with-lease` push will be needed (handled by the create-pr skill).

**For branches targeting `test` or `prod`**, just commit normally — no amend/squash.

## Step 6: Output Summary

```
Committed: type(scope): subject
  {N} files changed, {A} insertions(+), {D} deletions(-)
  Services: {list}
  Validation: all passed
```
