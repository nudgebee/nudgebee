---
name: create-pr
description: Create a pull request with proper formatting, validation, and conventions for this monorepo
---

# Create Pull Request

Create a pull request for the current branch. Optional argument: `$ARGUMENTS` (target base branch, defaults to `main`).

## Step 1: Gather Context

Run these commands to understand the current state:

```bash
# Current branch and tracking info
git branch --show-current
git status

# Commits on this branch not in base
git log main..HEAD --oneline

# Full diff against base
git diff main...HEAD --stat
git diff main...HEAD
```

If `$ARGUMENTS` specifies a different base branch (e.g., `test` or `prod`), use that instead of `main`.

## Step 2: Identify Affected Services

Map changed files to services using this table:

| Path prefix | Service | Type | Validation |
|---|---|---|---|
| `api-server/services/` | api-server | Go | `make validate` |
| `ticket-server/` | ticket-server | Go | `make validate` |
| `collector-server/cloud-collector/` | cloud-collector | Go | `make validate` |
| `collector-server/otel-collector/` | otel-collector | Go | `make validate` |
| `collector-server/k8s-collector/relay-server/` | relay-server | Go | `make validate` |
| `collector-server/k8s-collector/app/` | k8s-collector-app | Python | `make lint && make test` |
| `llm/code-analysis/` | code-analysis | Go | `make check` |
| `llm/llm-server/` | llm-server | Go | `make validate` |
| `llm/rag-server/` | rag-server | Python | `make lint && make test` |
| `llm/benchmark/` | benchmark | Python | `poetry run pytest` |
| `ml-k8s-server/` | ml-k8s-server | Python | `make lint && make test` |
| `auto-pilot/` | auto-pilot | Python | `poetry run black --check . && poetry run flake8 .` |
| `auto-pilot/sidecar/` | auto-pilot-sidecar | Python | `poetry run black --check . && poetry run flake8 .` |
| `notifications-server/` | notifications-server | Python | `poetry run black --check . && poetry run flake8 .` |
| `app/` | frontend | TypeScript | `npm run lint2` |
| `deploy/` | infrastructure | — | Manual review |

## Step 3: Run Validation

For each affected service, run its validation command. Report results to the user. If validation fails, ask the user whether to fix the issues or proceed anyway.

## Step 3.5: AI Self-Review (First-Pass Review Before Human Review)

**Mandatory.** Before pushing, read the diff and run an AI first-pass review. The goal is to catch issues **before** a human reviewer ever sees them, and to surface residual risks explicitly rather than hoping the reviewer finds them.

Run:

```bash
git diff {base_branch}...HEAD
```

Read the diff in full and evaluate against these dimensions:

- **Correctness:** logic errors, off-by-one, nil/null risks, missing error handling, race conditions, incorrect API contracts.
- **Security:** injection (SQL, command, XSS), hardcoded secrets, missing input validation at system boundaries, insecure deserialization, OWASP Top 10.
- **Over-engineering:** premature abstractions, unused hooks, speculative flexibility, helper functions with a single caller, scenarios that can't happen being "handled".
- **Scope creep:** changes outside the stated goal of the PR (per `CLAUDE.md → Doing tasks`: "Don't add features, refactor code, or make 'improvements' beyond what was asked").
- **Test coverage:** are new code paths covered? Are edge cases tested?
- **Cross-service impact:** shared types, API contracts, DB schema, Hasura metadata — anything that affects other services.
- **Convention compliance:** Go idioms + `slog` + `testify`, Python `black` 120 + flake8 + mypy, TypeScript oxlint + prettier, commit scope correctness.

Categorize each finding into one of three buckets:

1. **Fix now.** Clear bugs, security issues, style violations, obvious over-engineering. **Fix them in-place** and re-run validation for the affected service. Do not push a PR with known issues that you could have fixed.
2. **Flag to reviewer.** Genuine judgment calls — design trade-offs, architectural questions, risks you mitigated but didn't eliminate. These go in the **Risks & Counterarguments** section of the PR body (see Step 5).
3. **Ask the user.** Anything that requires product or business context the agent doesn't have. Surface these **before** pushing — do not ship a PR with open questions buried in the description.

If the PR touches shared contracts, DB schema, cross-service behavior, or any architectural decision, also run the logic from `/challenge` against the diff itself: what are the three strongest reasons this diff is wrong? Include the surviving counterarguments in the **Risks & Counterarguments** section of the PR body. Skip this sub-step for typo / docs / 1-line fixes.

## Step 4: Find Related Issue

Search for an existing GitHub issue (open or closed) to link to this PR:

```bash
# Search using keywords from the branch name and commit messages
gh issue list --search "<keywords from branch/commits>" --state all --limit 10 --json number,title,state,url
```

If related issues are found, present them to the user and ask which to link. If none found, ask the user if they have an issue number. Do NOT auto-create a new issue — only create one if the user explicitly asks.

## Step 5: Check Remote Status

```bash
# Ensure branch is pushed
git remote -v
git push -u origin $(git branch --show-current) 2>&1 || true
```

If there are unpushed commits, push them before creating the PR.

## Step 6: Generate PR Content

Based on the commits and diff, generate:

**Title format:** `type(scope): subject` (per `.github/semantic.yml`)

**Allowed types:**
| Type | Use when |
|---|---|
| `feat` | New feature or functionality |
| `fix` | Bug fix |
| `docs" | Documentation only |
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
| `collector` | `collector-server/cloud-collector/`, `collector-server/otel-collector/`, `collector-server/k8s-collector/app/` |
| `deps` | Dependency updates |
| `#xxx` | Issue number — use for `api-server/services/`, `api-server/hasura/`, `deploy/`, `.github/`, or any cross-service change |

**Examples:** `fix(ui): handle null state in settings`, `feat(#123): add Azure onboarding flow`

**Semantic type → PR "Type of change" mapping:**
| Semantic type | PR checkbox |
|---|---|
| `feat` | New feature |
| `fix` | Bug fix |
| `docs` | Documentation |
| `style` | Chore |
| `refactor` | Refactor |
| `perf` | Performance |
| `test` | Chore |
| `chore` | Chore |
| `ci` | Build / CI |
| `infra` | Build / CI |
| `revert` | Bug fix |
| `release` | Chore |

**Body MUST follow the repo's PR template (`.github/pull_request_template.md`):**

```markdown
# Description

{Summary of the changes and the related issue. Include relevant motivation and context.
List any dependencies that are required for this change.}

Fixes # (issue)   ← include if there's a linked issue, otherwise remove this line

## Type of change

- [x] {Matching type from mapping above}

# How Has This Been Tested?

{Describe the tests that you ran to verify your changes. Provide instructions so we can reproduce.}

- [x] {Test A description}
- [x] {Test B description}

---

# Risks & Counterarguments

{Residual risks from the AI self-review in Step 3.5 — things that were considered and mitigated but not eliminated, plus any counterarguments from /challenge that the implementation accepted rather than resolved. Format as a bulleted list, each bullet stating: the risk, why it was accepted, and what would trigger a revisit. State "None — fully resolved during self-review" if there are no residual concerns. Do NOT put trivial concerns here; save this section for things a human reviewer should actively evaluate.}
```

**Rules for filling the template:**
- Select the "Type of change" based on the semantic type → PR checkbox mapping above
- Mark applicable types with `[x]` — only include the checked types, delete all unchecked options
- The Description should explain **why** the change was made, not just what changed
- Testing section should list concrete verification steps
- If there's no linked issue, remove the `Fixes #` line entirely
- The `Risks & Counterarguments` section is required — state "None — fully resolved during self-review" if there are no residual concerns

## Step 7: Create the PR

Ask the user to confirm the title and body, then create:

```bash
gh pr create 
  --base {base_branch} 
  --title "type(scope): subject" 
  --body "$(cat <<'EOF'
{body}
EOF
)"
```

## Step 8: Output Result

Print the PR URL and a summary:

```
PR created: {url}
Title: type(scope): subject
Base: {base} <- {head}
Services: {list}
Validation: {pass/fail status per service}
```
