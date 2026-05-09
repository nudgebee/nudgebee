---
name: pr-comments
description: Fetch PR review comments, display them, and optionally fix code issues raised by reviewers
---

# PR Comments — Fetch, Display, and Fix

Fetch and display comments from the current branch's pull request. Optional argument: `$ARGUMENTS` can be a PR number, `fix` to automatically address review comments, or `fix 12345` to fix comments on a specific PR.

## Step 1: Determine the PR

```bash
# Extract PR number from arguments (handles "fix", "fix 12345", "12345", or empty)
PR_NUMBER=$(echo "$ARGUMENTS" | grep -o '[0-9]\+' | head -n 1)
if [[ -z "$PR_NUMBER" ]]; then
  PR_NUMBER=$(gh pr view --json number -q .number)
fi
```

If no PR is found, stop and tell the user: "No open PR found for the current branch. Usage: `/pr-comments [PR_NUMBER]`"

## Step 2: Fetch All Comments

Run these commands to gather all comment types:

```bash
REPO=$(gh repo view --json nameWithOwner -q .nameWithOwner)

# PR-level comments (issue comments)
gh api /repos/${REPO}/issues/${PR_NUMBER}/comments

# Code review comments (inline on diff)
gh api /repos/${REPO}/pulls/${PR_NUMBER}/comments
```

## Step 3: Display Comments

Format and display all comments in a readable way:

### PR-level Comments
```
- @author (date):
  > comment body
```

### Code Review Comments (inline)
```
- @author file.go#line:
  ```diff
  [diff_hunk]
  ```
  > comment body
```

**Rules:**
- Filter out bot noise (labeler messages, generic bot summaries) — but keep actionable bot comments (code suggestions, warnings)
- Preserve threading/nesting of replies
- Show file and line number context for code review comments
- Group comments by file when there are multiple

## Step 4: Analyze and Fix (when `$ARGUMENTS` contains "fix")

If the user invoked `/pr-comments fix` or asks to fix the comments:

1. **Categorize** each review comment:
   - **Actionable code change**: Reviewer suggests a specific code change (e.g., add a parameter, rename, fix logic)
   - **Question/discussion**: Reviewer asks a question or raises a discussion point — skip these, surface to user
   - **Bot suggestion**: Automated tool suggests a change — evaluate if valid before applying

2. **For each actionable comment:**
   - Read the file referenced in the comment
   - Understand the reviewer's suggestion in context of the full file
   - Apply the fix using Edit tool
   - Track which comments were addressed

3. **After all fixes:**
   - Run the appropriate validation for affected services (see service map below)
   - Commit the changes with message: `fix(scope): address PR review feedback` (derive `scope` from the modified file paths using the scope map in the create-pr skill — e.g., `api-server/services/` → `#xxx`, `app/` → `ui`)
   - Push to the branch

4. **Post a reply** on each addressed comment:
   ```bash
   # For inline review comments (code review comments):
   gh api /repos/${REPO}/pulls/${PR_NUMBER}/comments/${COMMENT_ID}/replies 
     -f body="Fixed in $(git rev-parse --short HEAD)"

   # For PR-level comments (issue comments):
   gh api /repos/${REPO}/issues/${PR_NUMBER}/comments 
     -f body="Addressed in $(git rev-parse --short HEAD)"
   ```

5. **Report to user:**
   ```
   Addressed N review comments:
   - file.go#L42: <short description of fix>
   - file.go#L88: <short description of fix>

   Skipped M comments (questions/discussion):
   - @reviewer: "question text..." — needs your input

   Pushed commit: <sha>
   ```

## Service Validation Map

| Path prefix | Validation command |
|---|---|
| `api-server/services/` | `cd api-server/services && make validate` |
| `ticket-server/` | `cd ticket-server && make validate` |
| `collector-server/cloud-collector/` | `cd collector-server/cloud-collector && make validate` |
| `collector-server/otel-collector/` | `cd collector-server/otel-collector && make validate` |
| `collector-server/k8s-collector/relay-server/` | `cd collector-server/k8s-collector/relay-server && make validate` |
| `collector-server/k8s-collector/app/` | `cd collector-server/k8s-collector/app && make lint && make test` |
| `llm/code-analysis/` | `cd llm/code-analysis && make check` |
| `llm/llm-server/` | `cd llm/llm-server && make validate` |
| `llm/rag-server/` | `cd llm/rag-server && make lint && make test` |
| `ml-k8s-server/` | `cd ml-k8s-server && make lint && make test` |
| `auto-pilot/` | `cd auto-pilot && poetry run black --check . && poetry run flake8 .` |
| `notifications-server/` | `cd notifications-server && poetry run black --check . && poetry run flake8 .` |
| `app/` | `cd app && npm run lint2` |
