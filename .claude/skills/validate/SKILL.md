---
description: Run validation (lint, format, tests) for affected services based on changed files
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
---

# Validate Changed Services

Run validation for services affected by current changes. Optional argument: `$ARGUMENTS` (specific service name to validate, e.g., `api-server`).

## Step 1: Determine What to Validate

If `$ARGUMENTS` is provided, validate only that service. Otherwise, detect affected services from changed files:

```bash
# Get changed files vs main (both staged and unstaged)
git diff --name-only main...HEAD
git diff --name-only
git diff --name-only --cached
```

Map files to services and their validation commands:

| Path prefix | Service | Validation command | Working directory |
|---|---|---|---|
| `api-server/services/` | api-server | `make validate` | `api-server/services/` |
| `ticket-server/` | ticket-server | `make validate` | `ticket-server/` |
| `collector-server/cloud-collector/` | cloud-collector | `make validate` | `collector-server/cloud-collector/` |
| `collector-server/k8s-collector/relay-server/` | relay-server | `make validate` | `collector-server/k8s-collector/relay-server/` |
| `collector-server/k8s-collector/app/` | k8s-collector-app | `make lint && make test` | `collector-server/k8s-collector/app/` |
| `llm/code-analysis/` | code-analysis | `make check` | `llm/code-analysis/` |
| `llm/llm-server/` | llm-server | `make validate` | `llm/llm-server/` |
| `llm/rag-server/` | rag-server | `make lint && make test` | `llm/rag-server/` |
| `llm/benchmark/` | benchmark | `poetry run pytest` | `llm/benchmark/` |
| `ml-k8s-server/` | ml-k8s-server | `make lint && make test` | `ml-k8s-server/` |
| `auto-pilot/` | auto-pilot | `poetry run black --check . && poetry run flake8 .` | `auto-pilot/` |
| `auto-pilot/sidecar/` | auto-pilot-sidecar | `poetry run black --check . && poetry run flake8 .` | `auto-pilot/sidecar/` |
| `notifications-server/` | notifications-server | `poetry run black --check . && poetry run flake8 .` | `notifications-server/` |
| `app/` | frontend | `npm run lint2` | `app/` |

## Step 2: Run Validation

For each affected service, run its validation command from the correct working directory. Capture both stdout and stderr. Use a timeout of 5 minutes per service.

Run independent service validations in parallel where possible.

## Step 3: Report Results

Output a summary table:

```
## Validation Results

| Service | Status | Details |
|---------|--------|---------|
| api-server | PASS/FAIL | {brief detail or error} |
| app | PASS/FAIL | {brief detail or error} |

{N} service(s) checked, {P} passed, {F} failed
```

If any service fails, show the relevant error output and suggest fixes.

## Step 4: Auto-Fix (if requested)

If the user invokes with `--fix` in `$ARGUMENTS`, attempt to auto-fix issues:

- **Go:** `make fmt` then re-validate
- **Python:** `poetry run black .` then re-validate
- **TypeScript:** `npm run lint2:fix` then re-validate
