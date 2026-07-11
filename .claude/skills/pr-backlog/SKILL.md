---
description: Check for pull requests requesting your review and selectively review them using the review-pr skill
user-invocable: true
allowed-tools:
  - Bash
  - AskUserQuestion
  - Skill
---

# PR Backlog

Check for pull requests where you are requested as a reviewer, display them in a list, and let you select which ones to review.

## Step 1: Fetch PRs Requesting Your Review

Use the GitHub CLI to fetch all open, non-draft PRs where you are requested as a reviewer:

```bash
gh pr list --search "review-requested:@me state:open draft:false" --json number,title,author,createdAt,updatedAt,url --limit 50
```

This returns JSON with PR details. Store this information.

## Step 2: Display PRs to User

Parse the JSON output and display the PRs in a clear, readable format:

```
Found {N} PRs requesting your review:

1. PR #{number}: {title}
   Author: {author.login}
   Created: {createdAt}
   URL: {url}

2. PR #{number}: {title}
   Author: {author.login}
   Created: {createdAt}
   URL: {url}

...
```

**Important:** If no PRs are found, display: "No PRs currently requesting your review. ✅"

## Step 3: Let User Select PRs to Review

Present options to the user using the AskUserQuestion tool:

```json
{
  "questions": [{
    "question": "Which PRs would you like to review?",
    "header": "Select PRs",
    "multiSelect": true,
    "options": [
      {
        "label": "PR #{number}: {short-title}",
        "description": "By {author} - {relative-time}"
      },
      ...
    ]
  }]
}
```

**Note:** Limit the displayed title to 50 characters in the label. If there are more than 4 PRs, show the first 4 and add an option "Show all" or let the user provide a specific PR number.

## Step 4: Review Selected PRs

For each selected PR number, invoke the `review-pr` skill:

```bash
# For each selected PR
/review-pr {pr_number}
```

**Implementation:** Use the Skill tool to invoke review-pr:
```
Skill tool with:
  skill: "review-pr"
  args: "{pr_number}"
```

## Step 5: Output Summary

After all reviews are complete, display a summary:

```
Review session complete!

Reviewed PRs:
- PR #{number}: {title} ✅
- PR #{number}: {title} ✅
- ...

Total: {N} PRs reviewed
```

## Alternative: Quick Review Mode

If the user provides arguments like `/pr-backlog all`, skip the selection step and review ALL PRs requesting your review automatically (use with caution).

## Error Handling

- If `gh` CLI is not installed or not authenticated, display: "GitHub CLI (`gh`) is required. Please install and authenticate: `gh auth login`"
- If fetching PRs fails, display the error and suggest checking GitHub connectivity
- If a specific PR review fails, continue with the next PR but note the failure in the final summary
