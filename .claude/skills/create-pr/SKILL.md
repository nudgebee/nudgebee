---
description: Create a pull request with proper formatting, validation, and conventions for this monorepo
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Task
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

## Step 2: Require GitHub Issue (main branch only)

**This step applies ONLY when the base branch is `main`.** Skip this step entirely for PRs targeting `test` or `prod` (cherry-picks / promotions).

Check if the user has provided a GitHub issue number (e.g., in the branch name like `fix/123-description`, in `$ARGUMENTS`, or mentioned in conversation). If an issue number is present, verify it exists:

```bash
gh issue view <issue_number> --json number,title,state 2>&1
```

**If no issue number is found or provided:**

1. Search for potentially related open issues based on the branch name and commit messages:
   ```bash
   gh issue list --state open --search "<keywords from branch/commits>" --limit 5 --json number,title
   ```
2. If matching issues are found, present them to the user and ask which one to link (or none).
3. If no matching issues exist, **stop and ask the user to create a GitHub issue first** before proceeding with the PR. Suggest using the `/create-issue` skill. Do NOT proceed with PR creation until the user provides an issue number.

**Once an issue number is confirmed**, store it for use in the PR body (`Fixes #<number>` or `Closes #<number>`).

## Step 3: Identify Affected Services

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

## Step 4: Run Validation

For each affected service, run its validation command. Report results to the user. If validation fails, ask the user whether to fix the issues or proceed anyway.

## Step 4.5: AI Self-Review (First-Pass Review Before Human Review)

**Mandatory.** Before pushing, read the diff and run an AI first-pass review. The goal is to catch issues **before** a human reviewer ever sees them, and to surface residual risks explicitly rather than hoping the reviewer finds them.

Run:

```bash
git diff {base_branch}...HEAD
```

Read the diff in full and evaluate against these dimensions:

### Review Dimensions

- **Correctness:** logic errors, off-by-one, nil/null risks, missing error handling, race conditions, incorrect API contracts.
- **Security:** injection (SQL, command, XSS), hardcoded secrets, missing input validation at system boundaries, insecure deserialization, OWASP Top 10.
- **Over-engineering:** premature abstractions, unused hooks, speculative flexibility, helper functions with a single caller, scenarios that can't happen being "handled".
- **Scope creep:** changes outside the stated goal of the PR (per `CLAUDE.md → Doing tasks`: "Don't add features, refactor code, or make 'improvements' beyond what was asked").
- **Test coverage:** are new code paths covered? Are edge cases tested?
- **Cross-service impact:** shared types, API contracts, DB schema, Hasura metadata — anything that affects other services.
- **Convention compliance:** Go idioms + `slog` + `testify`, Python `black` 120 + flake8 + mypy, TypeScript oxlint + prettier, commit scope correctness.

### Act on What You Find

Categorize each finding into one of three buckets:

1. **Fix now.** Clear bugs, security issues, style violations, obvious over-engineering. Fix them in-place, re-run validation for the affected service, then **commit the fixes** (e.g., `git commit -am "chore: fix issues found during self-review"`). This commit will be squashed in Step 6. Do not push a PR with known issues that you could have fixed. **A clean working tree is required for the rebase in Step 5 — uncommitted changes will cause it to fail.**
2. **Flag to reviewer.** Genuine judgment calls — design trade-offs, architectural questions, risks you mitigated but didn't eliminate. These go in the **Risks & Counterarguments** section of the PR body (see Step 8).
3. **Ask the user.** Anything that requires product or business context the agent doesn't have. Surface these **before** pushing — do not ship a PR with open questions buried in the description.

### Adversarial Pass (for non-trivial PRs)

If the PR touches shared contracts, DB schema, cross-service behavior, or any architectural decision, also run the logic from `/challenge` against the diff itself: what are the three strongest reasons this diff is wrong? Include the surviving counterarguments in the **Risks & Counterarguments** section of the PR body. Skip this sub-step for typo / docs / 1-line fixes.

## Step 5: Rebase on Base Branch

**MANDATORY:** The branch MUST be rebased on the latest base branch before creating or updating a PR. This ensures a clean, linear history.

```bash
# Fetch latest from remote
git fetch origin {base_branch}

# Rebase onto the latest base branch
git rebase origin/{base_branch}
```

If there are conflicts, resolve them and continue the rebase (`git rebase --continue`). If conflicts are too complex, inform the user and ask how to proceed.

## Step 6: Enforce Single Commit

PRs MUST contain exactly one commit. Check the commit count:

```bash
git log origin/{base_branch}..HEAD --oneline | wc -l
```

If there is more than one commit, squash them into a single commit:

```bash
# Squash all commits into one
git reset --soft origin/{base_branch}
git commit -m "<combined commit message covering all changes>"
```

The squashed commit message should summarize all changes coherently. Use the PR title as the first line, and list key changes as bullet points in the body.

## Step 7: Push to Remote

```bash
# Push with force-with-lease (required after rebase/squash)
git push --force-with-lease -u origin $(git branch --show-current) 2>&1 || true
```

Always use `--force-with-lease` since rebase and squash rewrite history.

## Step 8: Generate PR Content

Based on the commits and diff, generate:

**Title format:** `type(scope): subject` (per `.github/semantic.yml`)

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

**Body MUST follow the repo's PR template (`.github/pull_request_template.md`) with review notes appended:**

```markdown
# Description

{Summary of the changes and the related issue. Include relevant motivation and context.
List any dependencies that are required for this change.}

Fixes #{issue_number}   ← MANDATORY for PRs to main (from Step 2). Remove this line ONLY for PRs to test/prod.

## Type of change

- [x] {Matching type from mapping above}

# How Has This Been Tested?

{Describe the tests that you ran to verify your changes. Provide instructions so we can reproduce.}

- [x] {Test A description}
- [x] {Test B description}

---

# Review Notes

## 1. Changes Involved
{Bullet list of what was added, modified, or removed — focus on behavior changes, not line counts.}

## 2. Files & Functions Changed
| File | Function / Section | Change |
|------|-------------------|--------|
| `path/to/file.ts` | `functionName()` | {Brief description of what changed and why} |

## 3. Impact Analysis — Other Functions
{List any upstream/downstream components, shared utilities, or API contracts affected by this change. State "None" if the change is self-contained.}

## 4. Impact Analysis — Performance
{Note any render-cycle, memory, network, or bundle-size implications. Call out memoization, lazy loading, or caching changes. State "Negligible" if no meaningful impact.}

## 5. Impact Analysis — UX
{Describe user-facing changes: new interactions, visual updates, accessibility, empty/error states. State "None" if purely internal.}

## 6. Risks & Counterarguments
{Residual risks from the AI self-review in Step 4.5 — things that were considered and mitigated but not eliminated, plus any counterarguments from /challenge that the implementation accepted rather than resolved. Format as a bulleted list, each bullet stating: the risk, why it was accepted, and what would trigger a revisit. State "None — fully resolved during self-review" if there are no residual concerns. Do NOT put trivial concerns here; save this section for things a human reviewer should actively evaluate.}
```

**Rules for filling the template:**
- Select the "Type of change" based on the semantic type → PR checkbox mapping above
- Mark applicable types with `[x]` — only include the checked types, delete all unchecked options
- The Description should explain **why** the change was made, not just what changed
- Testing section should list concrete verification steps
- For PRs to `main`: the `Fixes #` line is **mandatory** — the issue number comes from Step 2
- For PRs to `test`/`prod`: remove the `Fixes #` line entirely
- Review Notes sections should be concise — one-liners per item, no filler
- The Files & Functions table should cover every modified file
- Impact sections should state "None" or "Negligible" explicitly when there is no impact, rather than omitting the section

## Step 9: Create the PR

Ask the user to confirm the title and body, then create:

```bash
gh pr create \
  --base {base_branch} \
  --title "type(scope): subject" \
  --body "$(cat <<'EOF'
{body}
EOF
)"
```

## Step 10: Output Result

Print the PR URL and a summary:

```
PR created: {url}
Title: type(scope): subject
Base: {base} <- {head}
Services: {list}
Validation: {pass/fail status per service}
```
