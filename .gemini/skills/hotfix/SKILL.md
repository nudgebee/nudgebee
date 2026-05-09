---
name: hotfix
description: Create a hotfix PR directly to prod with backmerge instructions
---

# Create Hotfix PR

Create a hotfix PR targeting the `prod` branch with backmerge guidance. Argument: `$ARGUMENTS` (PR numbers to cherry-pick, optional description of the hotfix).

## Branch Strategy Reminder

```
Hotfix flow:  hotfix/description -> prod -> backmerge to test -> backmerge to main
```

## Step 1: Setup Hotfix Branch

```bash
# Ensure we have latest prod
git fetch origin prod

# Create hotfix branch from prod
git checkout -b hotfix/$(date +%Y%m%d)-description origin/prod
```

If already on a `hotfix/*` branch, skip branch creation.

## Step 2: Cherry-Pick or Verify Changes

If `$ARGUMENTS` contains PR numbers (e.g., `23368 23371`):
1. Fetch each PR's commits using `gh pr view {number} --json mergeCommit,commits`
2. Cherry-pick the individual commits (not merge commits) in order
3. If cherry-pick conflicts, stop and ask the user to resolve

If no PR numbers, assume the changes are already on the branch.

## Step 3: Verify Changes

```bash
# Show what will be in the hotfix
git log origin/prod..HEAD --oneline
git diff origin/prod...HEAD --stat
git diff origin/prod...HEAD
```

Confirm with the user that these are the intended hotfix changes.

## Step 4: Identify Affected Services

Map changed files to services using this table:

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
| `notifications-server/" | notifications-server | Python | `poetry run black --check . && poetry run flake8 .` | `notifications-server/` |
| `app/` | frontend | TypeScript | `npm run lint2` | `app/` |
| `deploy/` | infrastructure | — | Manual review | — |

## Step 5: Run Validation

For each affected service, run its validation command from the correct working directory. Report results to the user. If validation fails, ask the user whether to fix the issues or proceed anyway.

## Step 6: Push and Create PR

```bash
git push -u origin $(git branch --show-current)
```

**Title format:** `fix(scope): subject` (per `.github/semantic.yml`)

**Allowed scopes (required):**
| Scope | Services / paths |
|---|---|
| `ui` | `app/` (frontend) |
| `autopilot` | `auto-pilot/`, `auto-pilot/sidecar/` |
| `ml` | `ml-k8s-server/`, `llm/code-analysis/`, `llm/llm-server/`, `llm/rag-server/`, `llm/benchmark/` |
| `notifications` | `notifications-server/` |
| `tickets` | `ticket-server/` |
| `relay` | `collector-server/k8s-collector/relay-server/` |
| `collector` | `collector-server/cloud-collector/`, `collector-server/otel-collector/`, `collector-server/k8s-collector/app/` |
| `deps` | Dependency updates |
| `#xxx` | Issue number — use for `api-server/`, `deploy/`, or cross-service changes |

- Hotfixes almost always use the `fix` type. Use `perf` or `revert` only if applicable.
- Example: `fix(collector): handle nil pointer in cloud sync`

**Body MUST follow the repo's PR template (`.github/pull_request_template.md`) with hotfix-specific additions:**

```bash
gh pr create 
  --base prod 
  --title "fix(scope): subject" 
  --body "$(cat <<'EOF'
# Description

**HOTFIX** — **Urgency:** {Critical / High}

{Summary of the problem being fixed and motivation for the hotfix. Include relevant context about the production issue.}

Cherry-picked from: {list PR numbers with # prefix if applicable, e.g., #23368, #23371}

## Type of change

- [x] Bug fix (non-breaking change which fixes an issue)

# How Has This Been Tested?

{Describe validation steps taken.}

- [x] {Validation step, e.g., "Black formatting check passes"}
- [x] {Validation step, e.g., "Flake8 lint passes"}
- [ ] CI tests pass

## Backmerge Checklist

After merging to `prod`, backmerge is required:
- [ ] Create PR: `prod` -> `test`
- [ ] Merge to `test`
- [ ] Create PR: `test` -> `main`
- [ ] Merge to `main`

## Rollback Plan

{Describe how to rollback if this causes issues, e.g., revert commits and redeploy previous image tag.}
EOF
)"
```

**Rules for filling the template:**
- Select the matching "Type of change" with `[x]` — only include checked types, delete all unchecked options
- The full list of types from the PR template is: Bug fix, Enhancement, Refactor, Documentation, Performance, Chore, Build / CI, Security, New feature, Breaking change
- The Description should explain **why** the hotfix is needed (the production issue), not just what changed
- Testing section should list concrete verification steps actually performed
- If cherry-picking from existing PRs, reference them in the Description
- Always fill in the Rollback Plan with a concrete rollback strategy

## Step 7: Output Result and Reminders

```
HOTFIX PR created: {url}
Title: fix(scope): subject
Target: prod <- {branch}
Services: {list}
Validation: {pass/fail status per service}

IMPORTANT: After merge, remember to backmerge:
  1. gh pr create --base test --head prod --title "chore(release): backmerge prod -> test"
  2. After test merge: gh pr create --base main --head test --title "chore(release): backmerge test -> main"
  Or use: /backmerge
```
