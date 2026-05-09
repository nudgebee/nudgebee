---
description: Review a pull request in an isolated worktree, post comments on the PR, and clean up
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Task
---

# Review Pull Request

Review the pull request specified by `$ARGUMENTS` (PR number or URL).

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
gh pr view $PR_NUMBER --json number,title,body,baseRefName,headRefName,additions,deletions,changedFiles,author

# Fetch latest and create worktree
git fetch origin
HEAD_BRANCH=$(gh pr view $PR_NUMBER --json headRefName -q .headRefName)
git worktree add "$WORKTREE_DIR" "origin/${HEAD_BRANCH}" 2>/dev/null || echo "Worktree already exists"

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

## Step 2: Identify Affected Services

Get the list of changed files from the PR:
```bash
gh pr diff $PR_NUMBER --name-only
```

Map changed files to services:

| Path prefix | Service | Type |
|---|---|---|
| `api-server/services/` | api-server | Go |
| `ticket-server/` | ticket-server | Go |
| `collector-server/cloud-collector/` | cloud-collector | Go |
| `collector-server/otel-collector/` | otel-collector | Go |
| `collector-server/k8s-collector/relay-server/` | relay-server | Go |
| `collector-server/k8s-collector/app/` | k8s-collector-app | Python |
| `llm/code-analysis/` | code-analysis | Go |
| `llm/llm-server/` | llm-server | Go |
| `llm/rag-server/` | rag-server | Python |
| `llm/benchmark/` | benchmark | Python |
| `ml-k8s-server/` | ml-k8s-server | Python |
| `auto-pilot/` | auto-pilot | Python |
| `auto-pilot/sidecar/` | auto-pilot-sidecar | Python |
| `notifications-server/` | notifications-server | Python |
| `app/` | frontend | TypeScript |
| `deploy/` | infrastructure | Helm/K8s |

## Step 3: Read Service-Specific CLAUDE.md

For each affected service, use the Read tool to check if `$WORKTREE_DIR/{service}/CLAUDE.md` exists and read it for service-specific conventions.

## Step 4: Read Changed Files and Diff

Read each changed file from the worktree using the Read tool with paths like `$WORKTREE_DIR/path/to/changed/file`.

Also fetch the full diff for line number context:
```bash
cd $WORKTREE_DIR && gh pr diff $PR_NUMBER
```

## Step 5: Review the Code

Analyze the diff and the files you read against these dimensions:

### Correctness
- Logic errors, off-by-one, nil/null pointer risks
- Missing error handling
- Race conditions in concurrent code
- Incorrect API contracts

### Security
- SQL injection, command injection, XSS
- Hardcoded secrets or credentials
- Insecure deserialization
- Missing input validation at system boundaries
- OWASP Top 10 concerns

### Conventions & Style
- **Go services:** Standard Go idioms, `slog` for logging, `testify` for tests
- **Python services:** Black formatting (line-length 120), flake8, mypy compliance
- **TypeScript:** oxlint + prettier compliance
- PR title format: `type(scope): subject` (per `.github/semantic.yml`)
  - Types: `feat`, `fix`, `docs`, `style`, `refactor`, `perf`, `test`, `chore`, `revert`, `ci`, `infra`, `release`
  - Scopes: `autopilot`, `ml`, `notifications`, `ui`, `tickets`, `relay`, `collector`, `deps`, `#\d+`

### Testing
- Are new features covered by tests?
- Are edge cases tested?
- Do existing tests still pass with these changes?

### Architecture
- Does this change follow existing patterns in the service?
- Are there unnecessary abstractions or over-engineering?
- Cross-service impact (shared types, API contracts, database schema)

### Deployment
- Database migration concerns
- Breaking API changes
- Environment variable additions
- Helm chart / K8s config changes needed

## Step 6: Post Review Comments on the PR

For each finding, post a review comment directly on the PR using `gh`.

### Inline Comment Format (Gemini-style)

Each inline comment MUST start with a **severity icon** on the first line, followed by a blank line, then the explanation. Use these exact icon URLs based on severity:

| Severity | Icon markdown |
|---|---|
| Critical bug / must fix | `![critical](https://www.gstatic.com/codereviewagent/critical.svg)` |
| High importance | `![high](https://www.gstatic.com/codereviewagent/high-priority.svg)` |
| Medium importance | `![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)` |
| Low / nit | `![low](https://www.gstatic.com/codereviewagent/low-priority.svg)` |
| Security critical | `![security-high](https://www.gstatic.com/codereviewagent/security-high-priority.svg)` followed by `![high](https://www.gstatic.com/codereviewagent/high-priority.svg)` |
| Security medium | `![security-medium](https://www.gstatic.com/codereviewagent/security-medium-priority.svg)` followed by `![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)` |
| Positive / looks good | `> ✅ **Looks Good**` (blockquote format — the positive.svg URL is a 404) |

**Comment structure:**
1. Severity icon(s) on the first line
2. Blank line
3. Clear explanation of the issue or observation
4. If applicable, a suggested fix with a code block (use ` ```suggestion ` fenced blocks for single-line replacements, or regular fenced blocks for multi-line examples)

**Example inline comment body:**
```
![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg)

The dependency array for this `useEffect` is missing `mode`. This violates the `react-hooks/exhaustive-deps` rule and can lead to bugs from stale closures.

` ` `suggestion
  }, [value, mode]);
` ` `
```

**IMPORTANT: Posting inline comments**

Always use --input with a JSON payload to avoid gh api -f escaping the exclamation mark in image markdown:

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

**Note:** Use `position` (the line offset within the diff hunk, starting from 1), NOT `line` + `subject_type`. Get the head commit SHA via: `gh pr view $PR_NUMBER --json headRefOid -q .headRefOid`

### Summary Comment Format (Gemini-style)

Post a single summary comment on the PR with this structure:

```bash
cd $WORKTREE_DIR && gh pr comment $PR_NUMBER --body "$(cat <<'EOF'
## Summary of Changes

{1-3 sentence summary of what this PR does and its motivation}

### Highlights

- **{Feature/Fix 1}**: {Brief description}
- **{Feature/Fix 2}**: {Brief description}
- ...

<details>
<summary>Changelog</summary>

{For each changed file, a bullet with the filename in bold and a sub-list of what changed}

- **`path/to/file1.go`**
    - {change description}
    - {change description}
- **`path/to/file2.ts`**
    - {change description}
</details>

### Review Summary

**Services affected:** {list}
**Risk level:** Low / Medium / High

| Category | Finding | Severity |
|---|---|---|
| {Correctness/Security/Style/...} | {Brief description — file:line} | ![critical](https://www.gstatic.com/codereviewagent/critical.svg) |
| {Category} | {Brief description — file:line} | ![medium](https://www.gstatic.com/codereviewagent/medium-priority.svg) |
| ... | ... | ... |

### Checklist
- [ ] Tests cover new/changed behavior
- [ ] No hardcoded secrets
- [ ] Error handling is appropriate
- [ ] No breaking API changes (or documented)
- [ ] Linting/formatting passes for affected services

---
*Automated PR Review*
EOF
)"
```

**Rules:**
- If there are no findings in a severity, omit those rows from the table.
- Always reference file paths and line numbers in findings.
- Use the severity icons consistently between inline comments and the summary table.
- Keep the changelog in a `<details>` block to avoid overwhelming the summary.
- Omit the Changelog section if there are more than 15 changed files (too verbose).

## Step 7: Clean Up Worktree

After the review is complete and all comments are posted, remove the worktree:

```bash
REPO_ROOT=$(git rev-parse --show-toplevel)
WORKTREE_DIR="${REPO_ROOT}-pr-review-${PR_NUMBER}"

git -C "$REPO_ROOT" worktree remove "$WORKTREE_DIR" --force
```

Verify cleanup:
```bash
git -C "$REPO_ROOT" worktree list
```

## Step 8: Output Summary

Print a brief summary to the user:

```
Review posted on PR #{number}: {title}
  - {N} inline comments posted
  - {1} summary comment posted
  - Worktree cleaned up

PR URL: {url}
```
