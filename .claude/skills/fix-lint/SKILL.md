---
description: Auto-fix lint and format issues across all affected services
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
---

# Fix Lint & Format Issues

Auto-fix linting and formatting issues for affected services. Optional argument: `$ARGUMENTS` (specific service name, or `--all` to fix everything).

## Step 1: Determine Scope

**If `$ARGUMENTS` is a service name:** fix only that service.
**If `$ARGUMENTS` is `--all`:** fix all services in the repo.
**If `$ARGUMENTS` is empty:** detect affected services from changed files:

```bash
# Changes vs main (committed + uncommitted)
git diff --name-only main...HEAD
git diff --name-only
git diff --name-only --cached
```

Combine and deduplicate, then map to services.

## Step 2: Run Auto-Fix Per Service

Run the appropriate fix command for each affected service. Execute independent services in parallel where possible.

| Service | Fix command | Working directory |
|---|---|---|
| api-server | `make fmt` | `api-server/services/` |
| ticket-server | `make fmt` | `ticket-server/` |
| cloud-collector | `make fmt` | `collector-server/cloud-collector/` |
| otel-collector | `make fmt` | `collector-server/otel-collector/` |
| relay-server | `make fmt` | `collector-server/k8s-collector/relay-server/` |
| k8s-collector-app | `poetry run black .` | `collector-server/k8s-collector/app/` |
| code-analysis | `make fmt` | `llm/code-analysis/` |
| llm-server | `make fmt` | `llm/llm-server/` |
| rag-server | `poetry run black .` | `llm/rag-server/` |
| benchmark | `poetry run black .` | `llm/benchmark/` |
| ml-k8s-server | `make fmt` | `ml-k8s-server/` |
| auto-pilot | `poetry run black .` | `auto-pilot/` |
| auto-pilot-sidecar | `poetry run black .` | `auto-pilot/sidecar/` |
| notifications-server | `poetry run black .` | `notifications-server/` |
| frontend | `npm run lint2:fix` | `app/` |

## Step 3: Verify Fixes

After fixing, re-run the **check** (not fix) command for each service to confirm everything passes:

| Service type | Verify command |
|---|---|
| Go (with Makefile) | `make lint` |
| Python (with Makefile) | `make lint` |
| Python (no Makefile) | `poetry run black --check . && poetry run flake8 .` |
| TypeScript | `npm run lint2` |

## Step 4: Report Results

```
## Fix Results

| Service | Fix | Verify | Status |
|---------|-----|--------|--------|
| api-server | make fmt | make lint | PASS/FAIL |
| app | npm run lint2:fix | npm run lint2 | PASS/FAIL |

{N} services fixed, {P} passing, {F} still failing
```

If any service still fails after auto-fix, show the remaining errors. These likely need manual intervention (e.g., flake8 issues that black can't fix, type errors from mypy).

## Step 5: Show Changed Files

```bash
git diff --name-only
```

Tell the user what files were modified by the auto-fix so they can review before staging.
