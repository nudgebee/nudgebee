---
description: Review a pull request design-first in an isolated worktree — premise, root cause, simpler alternatives, blast radius, rollout — then line-level issues; post comments on the PR and clean up
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Task
  - Agent
---

# Review Pull Request

Review the pull request specified by `$ARGUMENTS` (PR number or URL).

## What this review is for

A reviewer approves the **decision**, not the lines. The comments that change outcomes here are design comments: the premise doesn't hold, the fix treats a symptom, a much smaller change would do, the change quietly alters a flow everyone uses, the rollout order loses data, the next call site will bring the bug back. Line-level bugs come second. Style nits come last, capped, and never block.

A review that lists ten nits and misses that the approach is wrong has failed. A review that says "the approach is right, here is what I checked" with zero findings has succeeded.

## Step 0: Validate Arguments

**If `$ARGUMENTS` is empty or not provided, you MUST stop and ask the user for a PR number.** Do NOT proceed without an explicit PR number or URL. Do NOT fall back to the current branch's PR.

Example prompt: "Please provide a PR number or URL. Usage: `/review-pr 123`"

## Step 1: Fetch PR Metadata and Create Worktree

Run these commands sequentially in a single bash call:

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
PR_NUMBER=<pr number from arguments>
WORKTREE_DIR="${REPO_ROOT}-pr-review-${PR_NUMBER}"

# Fetch PR metadata
gh pr view $PR_NUMBER --json number,title,body,baseRefName,headRefName,headRefOid,additions,deletions,changedFiles,author,closingIssuesReferences

# Fetch latest and create worktree
git fetch origin
HEAD_BRANCH=$(gh pr view $PR_NUMBER --json headRefName -q .headRefName)
# A leftover worktree from an aborted run would sit on an old head — always start from the current one
git worktree remove "$WORKTREE_DIR" --force 2>/dev/null
git worktree add --detach "$WORKTREE_DIR" "origin/${HEAD_BRANCH}"

echo "WORKTREE_DIR=${WORKTREE_DIR}"
```

Store the `WORKTREE_DIR` path. You will use it for ALL subsequent operations.

## CRITICAL: Worktree Path Rule

**From this point forward, you MUST:**
- Use `$WORKTREE_DIR` as the working directory for ALL bash commands (`cd $WORKTREE_DIR && ...`)
- Use `$WORKTREE_DIR/path/to/file` for ALL Read tool calls
- Use `$WORKTREE_DIR` as the `path` for ALL Glob and Grep tool calls
- **NEVER** use the original repo root for any file operation
- `gh` commands can run from anywhere (they query the GitHub API), but even those should `cd $WORKTREE_DIR` first for consistency

## Step 2: Read the Intent Before the Code

You cannot judge a design without knowing what it is for. Before opening the diff, collect:

1. **The PR body.** Note its Decision line, its claims ("X is excluded", "refused before LLM spend", "no change for other providers"), its Risks, and its "Not in this PR". Every claim is something to check in Step 5.
2. **The linked ticket** (`Fixes #N` / `Part of #N`, or `closingIssuesReferences`) and its parent epic: `gh issue view <N> --comments`. Note the acceptance criteria and any requirement the ticket states. Also note what the *next* tickets in the epic need: a design that has to be migrated away from next sprint is a finding now.
3. **Decision log.** Read the entries in [`docs/architecture-decisions.md`](../../../docs/architecture-decisions.md) that touch this area. A PR that re-opens a settled decision, or tries something listed under "won't try again", needs to say why.
4. **Sibling PRs.** Open PRs touching the same files or adding migrations: `gh pr list --state open --search "<file or area>"`. Look for migration `V<N>` collisions with `main` and each other, `atlas.sum` conflicts, and required merge order.
5. **Earlier review rounds** on this PR: inline comments (`gh api repos/{owner}/{repo}/pulls/$PR_NUMBER/comments`), reviews (`.../pulls/$PR_NUMBER/reviews`), and summary comments, which are issue comments (`gh api repos/{owner}/{repo}/issues/$PR_NUMBER/comments`). Do not repeat a point already raised. On a re-review, check each earlier point against the new head and say which are fixed.

Write down two lines for yourself before going further:
- **Goal:** what must be true when this ships (from the ticket, not from the PR).
- **Approach:** what the PR does to get there.

## Step 3: Identify Affected Services and Read Their CLAUDE.md

```bash
gh pr diff $PR_NUMBER --name-only
```

| Path prefix | Service | Type |
|---|---|---|
| `api-server/services/` | api-server | Go |
| `ticket-server/` | ticket-server | Go |
| `collector-server/cloud-collector/` | cloud-collector | Go |
| `collector-server/k8s-collector/relay-server/` | relay-server | Go |
| `collector-server/k8s-collector/app/` | k8s-collector-app | Python |
| `llm/code-analysis/` | code-analysis | Go |
| `llm/llm-server/` | llm-server | Go |
| `llm/gateway/` | llm-gateway | Go |
| `llm/rag-server/` | rag-server | Python |
| `llm/benchmark/` | benchmark | Python |
| `ml-k8s-server/` | ml-k8s-server | Python |
| `auto-pilot/` | auto-pilot | Python |
| `auto-pilot/sidecar/` | auto-pilot-sidecar | Python |
| `notifications-server/` | notifications-server | Python |
| `runbook-server/` | runbook-server | Go |
| `app/` | frontend | TypeScript |
| `api-server/migrations/` | migrations | SQL |
| `deploy/` | infrastructure | Helm/K8s |

For each affected service, read `$WORKTREE_DIR/{path prefix}CLAUDE.md` (the path prefix from the table, e.g. `llm/llm-server/CLAUDE.md`) if it exists. For `app/`, also check the relevant sections of `app/design-system.md`.

## Step 4: Read the Diff, Then Read Outward

```bash
cd $WORKTREE_DIR && gh pr diff $PR_NUMBER
```

Read each changed file in full from the worktree. Then read **outward** — design problems live at the edges of the diff, not inside it:

- For every changed function, type, field, action name, column, header, or prompt contract: grep its callers and consumers **outside** the diff (other services included — llm-server prompts, the UI, collectors). Grep the wire string, not just the Go/TS identifier.
- For every new check or gate: find the other paths that reach the same effect without passing through it.
- For every shared code path the PR touches: find the default / non-feature caller and trace what it now does.

## Step 5: Review — Design First

Work through these in order. Each later pass matters less than the one before it; do not let a long list of small findings crowd out one finding from 5.1–5.4.

**For PRs over ~100 non-test lines, or that touch shared types, API contracts, DB schema, or cross-service behavior**, spawn the `plan-alternative` agent in the background at the start of this step. Give it the Goal from Step 2 (not the PR's framing) and a one-paragraph summary of the PR's approach, and ask it to design the simplest thing that meets the goal in this codebase and say whether it materially dominates. Use its answer in 5.3; do not paste it verbatim.

### 5.1 Premise — is the problem real, and does the fix reach it?

- Does the code path the fix lives on actually run for the case in the ticket? (A link that needs a field the producer never sets; a gate keyed on a signal that also matches the cases it should allow.)
- Check against real data when you can: read-only queries on the dev database, or dev logs. If a PR is premised on "nobody answers these prompts", a count showing most of them are answered by a person within minutes sinks it. Quote the window and the counts. Never query customer systems.
- Does the PR's evidence represent the default case? (A dev environment holding millions of rows of data that a default install never collects.)

### 5.2 Root cause or symptom?

- Does the change remove the cause, or explain / retry / sweep around it? Typical symptom fixes: a prompt rule telling the model about a limitation the tool could stop imposing; a cleanup sweep for orphans a transaction would make impossible; a clamp that silently shortens a request, plus a message explaining the shortening.
- If a cause-level fix exists, name it and its cost. If it is larger than this PR should be, say "as a follow-up" — but say it.

### 5.3 Could this be simpler — or different?

- Estimate the smallest change that fully meets the goal, in lines. If the PR is several times that, say so with a size breakdown (part → lines → needed for the goal?).
- Does an existing pattern in the repo already solve this shape? Cite it (file / migration / helper). Reuse beats a parallel mechanism.
- When the design is questionable, give **2–3 options**, each with approach, what it takes, and cost, and say which you'd pick and why. Leave the call to the author when it is a genuine trade-off.
- When the approach is right, still record what you considered and why it loses ("Simpler? An api-server check would be easier to wire but takes a different network path and could disagree with what the agent sees") in the Checked-and-fine fold. This tells the author the alternatives were weighed.

### 5.4 Make the class impossible, not the instance

- **Next call site.** If correctness depends on every caller remembering to do X (pass the acting user, apply the filter, call the guard), move X inside the callee so forgetting is impossible.
- **Fail closed for future values.** A denylist lets every backend / provider / role added later through by default. Prefer an allowlist when the safe answer for "unknown" is "no".
- **Guard where the effect happens.** A check at write time misses rows written by other paths and conditions that change later (a built-in added next release); a check at lookup / use time covers both.
- **One source of truth.** A key format, name, or mapping built independently in two places will drift silently. Share it, or pin both sides with one fixture test.
- **One meaning per name.** If a field's or function's meaning narrows ("needed for a direct write"), rename it or move it to where the meaning lives; if a helper now answers two questions, split it.
- **Typed over open-ended.** An open header map or `map[string]any` that can overwrite auth headers or accept any shape is a future bug; a typed field is not.

### 5.5 Boundaries and ownership

- Is this the right service and layer? (Logic in the service that already owns the data and the network path; a webhook in the service that handles the rest of that flow.)
- Does an EE / product concept leak into core code that ships in OSS? Core should expose a generic, concept-free hook with a no-op default; the concept lives behind it.
- Is data stored with its owner? Feature policy columns added to an identity / auth table, or a feature's state added to a shared table, become a migration later. Check whether the epic's next ticket (per-user, per-tenant default, per-account) fits the chosen shape without another migration.

### 5.6 Blast radius on existing flows

- Trace every changed **shared** hunk for a caller that does not use the new feature. List exactly what changes for everyone — "almost all identical to main; two things change for everyone: …" is the useful form.
- New failure modes on old paths: an extra DB read that can now fail a resume that worked before; stricter decoding of third-party input that turns a malformed optional field into "integration unavailable"; a wrapper that hides an interface the wrapped type implemented.
- Behavior changes hidden behind refactors (error now classified differently → circuit breaker never opens; wrapped error overrides a tailored user message).

### 5.7 Failure modes and state

- **Atomicity:** a crash between two writes — what is left behind? One transaction usually removes the orphan *and* the code that cleans it up.
- **Concurrency:** two writers at once (two pods, cron + manual run, patch + full rebuild). Is there a lock, a unique index, a compare-and-swap? Does a stale writer overwrite a newer one?
- **Time:** TTLs and leases after a pod dies mid-request; retry windows that let every caller through at once; work that starves later tenants because each run restarts from the same first key.
- **Stale state:** a flag or row that outlives its source (agent rolled back, key no longer sent, integration unlinked, a removed enum value still stored in rows).
- **Silent failure:** an empty result that reads as "healthy" or "no errors in this window"; a refused path that logs nothing. Per repo convention, fail loudly.
- **Flapping:** one broken sub-call marking a whole integration down for everyone, then up again a minute later.
- **Fail-open UI / API:** unselecting the last option meaning "everything"; a missing value meaning "unrestricted".

### 5.8 Rollout and operations

- **Order:** service deployed before its migration (deploy order between migrations and services is not guaranteed; on-prem installs run migrations from a Helm hook). What does the new code do against the old schema for that window — fail every insert, lose usage, error every heartbeat? Ask for a tolerant fallback or a release-note line.
- **Rollback:** does the feature flag / switch actually cover the risky write path, or only the harmless half?
- **Data already present:** rows produced by an older version that the new code treats differently.
- **Migrations:** `V<N>` collision with `main` or a sibling PR; `atlas.sum` regenerated with `new-migration.sh`; lock windows on large tables; `CONCURRENTLY` rules from the root CLAUDE.md.
- **Cost at scale:** per-cron or per-request queries that grow with tenants × rows; unbounded time windows or result sizes; regexes over large text columns. Ask for an `EXPLAIN ANALYZE` on dev when a hot query changes.
- **Observability:** can an operator tell it is broken without reading code — a log line on refusal, a metric, a visible status?

### 5.9 Authorization and security semantics

- For each new action: its roles in `app/src/lib/actions.yaml` **and** the server-side check. Can a narrower role (account / namespace admin, read-only) reach a tenant-wide effect? Is it in the non-grantable list when it should be?
- Can a restricted caller reach the same effect another way: path traversal (`..`), case-variant JSON keys, model in body vs path, a different HTTP method, a different provider prefix?
- Does a response return other accounts' or tenants' data to a lower role, even if the UI filters it?
- Injection, secrets, input validation at boundaries — the usual list, applied to the new surface.

### 5.10 Cross-service consumers and claims

- Who else reads what changed — LLM prompts that describe it, the UI, another service's mapping by name? Do the UI and the AI agree on the same signal (one says Disconnected, the other says healthy)?
- Prompts and comments that state facts the change makes false ("saved to these exact files" after a GC deletes them; "the data does not exist" when only the query window is limited).
- **PR body vs code:** check each claim noted in Step 2. A ticket requirement dropped to make the fix easier is a finding; `Fixes #N` that only partly fixes it should be `Part of #N`.

### 5.11 Scope

- Unrelated fixes riding along (a different bug, a separate security change, another service). Ask to call them out in the body or split them — "one logical change = one PR".
- A PR that is mostly tests and harness for a small behavior change is fine; say so if the size alarms nobody else.

### 5.12 Line-level correctness, tests, conventions

Only now: logic errors, nil risks, missing error handling, races inside a function, off-by-one, language idioms (Go `slog` / `testify`; Python black 120 / flake8; TS oxlint / prettier; `ds/*` primitives in `app/`).

For tests, ask one question: **would this test fail on `main`?** A test that stays green when the bug is present (it asserts the eventual outcome, not that the first attempt succeeded) is not a regression guard. Suggest the assertion that would be.

**Nits: at most three per review in total (inline and summary combined), labelled `Nit`, never blocking.** If you have more, drop the least useful.

### Evidence rules (all passes)

- Every blocking finding states a **concrete scenario**: input / state → what happens → why it's wrong. "Could be a problem" is not a finding.
- Reproduce when cheap: a throwaway test or probe in the worktree, run against the PR head (and against `main` when the claim is "this changed"). Show input → result, ideally as a small `main` vs PR table. Never commit or push probe code.
- When you did not reproduce, say "(from reading the code, not reproduced)".
- Data claims carry their source and window ("<environment>, last <N> days: <count> of <total> have the label").
- Don't restate bot reviewers' findings. If you agree or disagree with one, say so in one line.

## Step 6: Decide the Verdict

Pick one:

| Verdict | When |
|---|---|
| **Approve** | Approach right, nothing blocking. Findings are non-blocking or follow-ups. |
| **Revise** | Approach right, but specific things must change before merge. |
| **Redesign** | Premise wrong, or a materially simpler / safer design exists that the PR should switch to. |

The verdict is about the approach first. A PR with a wrong approach is **Redesign** even if every line is correct.

## Step 7: Post the Review

### Inline comments

Post inline only for findings anchored to a specific line. Design findings spanning several files go in the summary, not scattered across inline comments.

Each inline comment starts with a severity icon, then a blank line, then:
1. **Label, then the claim.** Match the label to the icon: critical/high → `**Fix before merge.**`; medium → `**Should:**` (blocking) or `Non-blocking.`; low → `Non-blocking.` or `Nit:`. Only the blocking labels are bold.
2. The scenario that breaks (input → outcome).
3. The fix, with a ` ```suggestion ` block when it's a line replacement.

Aim for five lines; a scenario that needs a short bullet list is fine. Never restate the code back to the author — GitHub renders it above your comment. If a finding needs more than ~8 lines, it belongs in the summary.

| Severity | Icon markdown |
|---|---|
| Critical bug / must fix | `![critical](https://www.gstatic.com/codereviewagent/critical.svg)` |
| High importance | `![high](https://www.gstatic.com/codereviewagent/high-priority.svg)` |
| Medium importance | `![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)` |
| Low / nit | `![low](https://www.gstatic.com/codereviewagent/low-priority.svg)` |
| Security critical | `![security-high](https://www.gstatic.com/codereviewagent/security-high-priority.svg)` followed by `![high](https://www.gstatic.com/codereviewagent/high-priority.svg)` |
| Security medium | `![security-medium](https://www.gstatic.com/codereviewagent/security-medium-priority.svg)` followed by `![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)` |

**Example inline comment body:**
```
![high](https://www.gstatic.com/codereviewagent/high-priority.svg)

**Fix before merge.** Save the tasks and the approval row in one transaction. A crash between the two leaves tasks parked with no approval — that is why the orphan sweep exists, and the sweep has an edge case of its own (line 264). With one transaction there are no orphans and the sweep can go.
```

**Posting:** always use `--input` with a JSON payload, so `gh api -f` doesn't escape the `!` in image markdown:

```bash
cd $WORKTREE_DIR && cat <<'JSONEOF' | gh api repos/{owner}/{repo}/pulls/$PR_NUMBER/comments --input -
{
  "body": "<comment-with-icon>",
  "path": "<file-path-relative-to-repo-root>",
  "commit_id": "<head-commit-sha>",
  "position": <diff-hunk-position>
}
JSONEOF
```

Use `position` (the line offset within the diff hunk, starting from 1), NOT `line` + `subject_type`. Head SHA: `gh pr view $PR_NUMBER --json headRefOid -q .headRefOid`.

### Summary comment

One comment. Above the fold ≤ 200 words (per [`docs/writing-for-readers.md`](../../../docs/writing-for-readers.md)); everything else in folds. No changelog of the diff — the author knows what they changed.

```markdown
**{Approve | Revise | Redesign}.** {One or two sentences: is the approach right, and the single most important thing. E.g. "The design is right: plan, park, approve into a separate run. Two things to fix before merge."}

**Before merge**
1. **{Short title}.** {Scenario → fix, 1–3 lines.} ([inline]({link}))
2. ...

**Non-blocking**
1. {Short title — one line.}

**Could this be simpler?** {Only when a materially simpler or different design exists: options A/B with what each takes and its cost, and which you'd pick. Omit otherwise.}

<details>
<summary>Checked and fine</summary>

- **{Area — authz / blast radius / rollout / perf / migration}:** {what you verified and how — "traced every changed core hunk for a non-feature session: identical to main"}
- **Simpler?** {alternatives considered and why this approach wins}
</details>

<details>
<summary>Follow-ups (not for this PR)</summary>

1. {Item — ask for a ticket under the epic so it doesn't drop}
</details>
```

**Rules:**
- Non-blocking items go in a short numbered list labelled **Non-blocking**, after "Before merge" (or in its place on an Approve with nothing blocking). Never fold them into "Before merge".
- "Checked and fine" is required, even on a clean Approve. It is what tells the author which risks were actually examined.
- On a re-review, open with `Re-checked at <short sha>.` and say which earlier points are addressed and how you checked each.
- If you got something wrong in an earlier round, correct it explicitly ("Correction from my side: …").
- Mention merge order, migration renumbering, and deploy-order notes when they apply — they are easy to miss and costly after merge.

```bash
cd $WORKTREE_DIR && gh pr comment $PR_NUMBER --body-file - <<'EOF'
<summary comment>
EOF
```

## Step 8: Clean Up Worktree

After the review is posted, remove the worktree (this also removes any throwaway probe tests):

```bash
# The shell may still be inside the review worktree, where --show-toplevel would return the worktree
# itself; the first `worktree list` entry is always the main checkout.
REPO_ROOT=$(git worktree list --porcelain | head -1 | cut -d' ' -f2-)
WORKTREE_DIR="${REPO_ROOT}-pr-review-${PR_NUMBER}"

cd "$REPO_ROOT"
git -C "$REPO_ROOT" worktree remove "$WORKTREE_DIR" --force
git -C "$REPO_ROOT" worktree list
```

## Step 9: Output Summary

```
Review posted on PR #{number}: {title}
  Verdict: {Approve | Revise | Redesign} — {one-line reason}
  - {N} before-merge items, {M} non-blocking, {K} follow-ups
  - {N} inline comments + 1 summary comment
  - Worktree cleaned up

PR URL: {url}
```
