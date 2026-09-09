---
description: Create a pull request with proper formatting, validation, and conventions for this monorepo
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
  - Glob
  - Grep
  - Task
---

# Create Pull Request

Create a pull request for the current branch, then watch it through to a green, review-clean state — polling CI and review threads, fixing what can be auto-fixed, and surfacing what only a human can resolve (Step 11). Optional argument: `$ARGUMENTS` (target base branch, defaults to `main`).

**Before writing any prose — the PR body, review replies, the final report — read [`docs/writing-for-readers.md`](../../../docs/writing-for-readers.md).** It is the binding style contract for anything this skill posts to GitHub. A correct PR that nobody finishes reading has failed.

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

## Step 2: Link the Original Ticket (main branch only)

**This step applies ONLY when the base branch is `main`.** Skip this step entirely for PRs targeting `test` or `prod` (cherry-picks / promotions).

**Goal: traceability.** The PR must link back to the ticket that *motivated* this work — the sprint ticket or existing issue — NOT a freshly minted issue. Creating a new issue per PR destroys traceability and litters the tracker with unassigned orphans. **Never create a new issue in this step.**

Find the original ticket, in this order — stop at the first hit:

1. **Explicit reference.** Issue number in `$ARGUMENTS`, the branch name (`fix/nb-1234-description`), commit messages, or mentioned anywhere in the conversation (including the ticket the session started from, e.g. via `/my-tickets`). Verify it exists:
   ```bash
   gh issue view <issue_number> --json number,title,state 2>&1
   ```
2. **My sprint tickets.** Check the user's assigned issues for one this work belongs to:
   ```bash
   gh issue list --assignee "@me" --state open --limit 30 --json number,title
   ```
3. **Keyword search — open AND closed.** The original ticket may already be closed or filed by someone else:
   ```bash
   gh issue list --state all --search "<keywords from branch/commits>" --limit 10 --json number,title,state
   ```

**If candidates are found**, present them and ask the user which one to link (or none). A closed issue is still a valid link target for follow-up work.

**If no related ticket exists**, stop and ask the user how to proceed. Do NOT run `gh issue create` and do NOT invoke `/create-issue` on your own — only the user can decide a new ticket is warranted. If they confirm one, create it via `/create-issue` (which assigns it and adds it to the sprint board) and reference the motivating context in its body.

**Once an issue number is confirmed**, store it for the PR body:
- `Fixes #<number>` if this PR fully resolves the ticket (auto-closes it on merge).
- `Part of #<number>` if this PR is one piece of a larger ticket that must stay open.

## Step 3: Identify Affected Services

Map changed files to services using this table:

| Path prefix | Service | Type | Validation |
|---|---|---|---|
| `api-server/services/` | api-server | Go | `make validate` |
| `ticket-server/` | ticket-server | Go | `make validate` |
| `collector-server/cloud-collector/` | cloud-collector | Go | `make validate` |
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
- **Scope creep:** changes outside the stated goal of the PR (per `CLAUDE.md → AI Coding Principles`, surgical changes: every changed line traces directly to the request).
- **Test coverage:** are new code paths covered? Are edge cases tested?
- **Cross-service impact:** shared types, API contracts, DB schema, Hasura metadata — anything that affects other services.
- **Convention compliance:** Go idioms + `slog` + `testify`, Python `black` 120 + flake8 + mypy, TypeScript oxlint + prettier, commit scope correctness.

### Act on What You Find

Categorize each finding into one of three buckets:

1. **Fix now.** Clear bugs, security issues, style violations, obvious over-engineering. Fix them in-place, re-run validation for the affected service, then **commit the fixes** (e.g., `git commit -am "chore: fix issues found during self-review"`). This commit will be squashed in Step 6. Do not push a PR with known issues that you could have fixed. **A clean working tree is required for the rebase in Step 5 — uncommitted changes will cause it to fail.**
2. **Flag to reviewer.** Genuine judgment calls — design trade-offs, architectural questions, risks you mitigated but didn't eliminate. These go in the **Risks** section of the PR body (see Step 8) — max 3 bullets, phrased for a reviewer who has not read the diff.
3. **Ask the user.** Anything that requires product or business context the agent doesn't have. Surface these **before** pushing — do not ship a PR with open questions buried in the description.

### Adversarial Pass (for non-trivial PRs)

If the PR touches shared contracts, DB schema, cross-service behavior, or any architectural decision, also run the logic from `/challenge` against the diff itself: what are the three strongest reasons this diff is wrong? Include the surviving counterarguments in the **Risks** section of the PR body. Skip this sub-step for typo / docs / 1-line fixes.

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
| `collector` | `collector-server/cloud-collector/`, `collector-server/k8s-collector/app/` |
| `deps` | Dependency updates |
| `NB-xxx` | Ticket number — use for `api-server/services/`, `api-server/migrations/`, `deploy/`, `.github/`, or any cross-service change |

**Examples:** `fix(ui): handle null state in settings`, `feat(NB-1234): add Azure onboarding flow`

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

**Body MUST follow the repo's PR template (`.github/pull_request_template.md`), written to the
rules in [`docs/writing-for-readers.md`](../../../docs/writing-for-readers.md) — read that file
before drafting. The short version: one screen above the fold, everything else demoted into a
`<details>` block, and "How Has This Been Tested?" answers *how the reader verifies this*, not
*which commands you ran*.**

```markdown
# Description

{THE LEAD — max 3 sentences, no symbol names, no file paths, no SHAs. What changed, who it
affects, why it mattered. One number if you have one. A PM must be able to read this and stop.}

Fixes #{issue_number}   ← MANDATORY for PRs to main (from Step 2; use "Part of #N" if the ticket stays open). Remove this line ONLY for PRs to test/prod.

## Type of change

- [x] {Matching type from mapping above}

# How Has This Been Tested?

{READER-SIDE STEPS. Numbered. What someone else does to confirm this works — where to click, what
to run, what they should see, and what they'd have seen before. No internal symbols.

If the change has no observable surface, write exactly one line saying so — e.g. "No user-visible
surface; verified via unit tests and the log line in the fold below" — and put the developer probe
in the fold. Do not invent a UI flow.}

1. {step}
2. {step — expected result}

# Risks

{Max 3 bullets, only things a reviewer should actively evaluate: trade-offs accepted, risks
mitigated but not eliminated, surviving counterarguments from Step 4.5 / `/challenge`. Each bullet:
the risk, why it was accepted, what would trigger a revisit. DELETE THIS HEADING ENTIRELY if there
is nothing real — do not write "None".}

<details>
<summary>Engineering detail</summary>

{No length limit. Everything the fold exists to hold, in whatever structure fits:

- Root cause, with file:line, symbols, commit SHAs, migration ids.
- Evidence you ran: validation commands and their output, test counts, benchmark numbers.
- Measurements, comparison tables, per-model / per-service breakdowns.
- Cross-service impact, performance, UX notes — only where non-obvious. Omit what doesn't apply.
- "Not in this PR" — adjacent problems deliberately left alone, and where they're tracked.}

</details>
```

**Rules for filling the template:**
- Select the "Type of change" based on the semantic type → PR checkbox mapping above
- Mark applicable types with `[x]` — only include the checked types, delete all unchecked options
- **Above the fold: 200 words, hard cap.** Over budget means demote into the fold, never trim meaning
- The lead explains **why**, not what — the diff already shows what
- **No per-file / per-function change table.** GitHub renders the diff directly above your text; a table restating it is machine output pasted back at the reader
- **Omit empty sections entirely.** No "None", no "Negligible", no "N/A" — an empty heading is worse than an absent one
- For PRs to `main`: the issue link is **mandatory** — the number comes from Step 2; `Fixes #` closes the ticket on merge, `Part of #` keeps it open for multi-PR tickets
- For PRs to `test`/`prod`: remove the `Fixes #` line entirely
- Dependency-bump and other mechanical PRs get a lead and nothing else — no fold, no risks

## Step 8.5: Reader Test (mandatory, before Step 9)

Re-read your own draft and answer these four. Any failure is a rewrite, not a caveat — and the fix
is almost always "move it into the fold", not "delete it".

1. **Skim test** — reading only the first three lines, can a PM tell what changed and whether it's risky?
2. **Jargon test** — is there a function name, file path, SHA, library version, migration id, or error string above the fold?
3. **Reproduce test** — could QA follow "How Has This Been Tested?" without opening the codebase? If it lists only commands you ran, it is not written yet.
4. **Slop test** — delete every sentence that restates the diff, hedges, or exists to look thorough. If the meaning survived the deletion, leave it deleted.

Then check the budget: `wc -w` on everything above `<details>` must be ≤ 200.

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

## Step 11: Watch CI & Review Until Green

Creating the PR is not the finish line. Drive it to a green, review-clean state: poll CI and the review threads, fix everything you can, and surface only what a human must do. **Never fabricate approval, self-approve, or mark a human-only gate as done.**

### 11.1 Watch the checks (in the background — never foreground sleep loops)

```bash
gh pr checks {pr_number} --repo {owner/repo} --watch    # run as a BACKGROUND task
```

Do **not** foreground-poll with `sleep N && gh pr checks` loops — that burns the session doing nothing. Start the watch as a background task (or arm a Monitor on the check status) and continue with other work; when it completes, read the final matrix. Cap the total wait at ~25 min of wall-clock; if checks are still pending past that, report the current matrix and hand back rather than spinning. Treat the pass as "settled" once no check is `pending`.

### 11.2 Triage each failing check

For every `fail`, fetch the failing job's log and classify it:

```bash
gh run view --repo {owner/repo} --job {job_id} --log-failed | tail -80
```

| Failure | Class | Action |
|---|---|---|
| lint / prettier / gofmt / black | auto-fix | run the service's auto-format/fix (`make fmt`, `npm run lint2:fix`, `poetry run black .`), re-validate |
| build / vet / type error | auto-fix | fix the code, re-run the service's validation |
| unit test | auto-fix if cause is clear | reproduce locally, fix, re-run; if the failure is unrelated/flaky, re-run the job once |
| migration version collision (`Vlabel-reuse`) | auto-fix | rebase on the base branch, then `./api-server/migrations/new-migration.sh` to renumber (fresh V + timestamp), move the SQL, delete the old files (see CLAUDE.md → Migrations) |
| PR-title / semantic-scope check | auto-fix | correct the title via `gh pr edit` / `gh api` (scopes must be **lowercase**) |
| screenshots-for-UI gate (`label-prs`) | **needs human** | the rule (external binary) wants real images dragged into the PR body — you cannot upload them; surface it |
| flaky / external infra (timeouts, registry 5xx) | retry | `gh run rerun`; if it re-fails, surface it |
| check needing secrets/env you don't have | **needs human** | surface it |

Fix all auto-fixable failures **together**, re-run the affected service's validation locally, then follow the single-commit discipline (Steps 5–7): rebase if needed, **amend/squash into the one commit**, `git push --force-with-lease`. Do **not** add fix-up commits.

### 11.3 Handle review comments

Fetch review threads, including **bot reviewers** (e.g. `gemini-code-assist`, CodeRabbit):

```bash
gh api repos/{owner/repo}/pulls/{pr_number}/comments --paginate      # inline comments
gh pr view {pr_number} --repo {owner/repo} --json reviews,comments    # review states + top-level
```

Triage each actionable comment — and **verify before acting** (bots hallucinate; confirm the symbol/line/behavior actually exists in your diff):

- **In-scope + correct** → fix in code, fold into the single commit.
- **Out-of-scope / pre-existing** (the flagged line isn't in your diff, or is code you didn't change) → do **not** edit; post a short reply so the thread resolves:
  ```bash
  gh api -X POST repos/{owner/repo}/pulls/{pr_number}/comments/{comment_id}/replies -f body="..."
  ```
- **False positive** → reply with the evidence.
- **Needs product/business context** → surface to the user; don't guess.

### 11.4 Loop

Pushing fixes re-runs the checks and re-triggers bot review on `synchronize`. Return to 11.1. Repeat until **every required check is green AND every actionable review comment is fixed or answered**. Bound the loop (≤ ~4 fix-and-push cycles); if the same check keeps failing after a genuine fix, stop and surface it — don't loop blindly.

### 11.5 Terminal report

You **cannot** self-approve or upload screenshots, so stop at the first stable state and report which one it is:

- ✅ **All checks green, review comments addressed — awaiting human approval.** (Success for the skill; approval is the human's to give.)
- ⚠️ **Green except for human-only gates** (screenshots, human decision) — list exactly what the user must do.
- ❌ **Blocked** — a check fails you couldn't fix; give the log excerpt and your diagnosis.

Print the final check matrix, the review-comment disposition (fixed / replied / needs-user), and a one-sentence next action for the user.
