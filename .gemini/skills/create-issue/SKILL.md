---
name: create-issue
description: Create GitHub issues using repo templates (feature, bug, spike)
---

# Create GitHub Issue

Create a GitHub issue using the repository's issue templates. Optional argument: `$ARGUMENTS` (issue type: `feature`, `bug`, or `spike`).

## Available Issue Types

| Type | Template | Title Format | Labels |
|------|----------|--------------|--------|
| `feature` | FEATURE-REQUEST.yml | `[REQUEST] - <title>` | — |
| `bug` | BUG-REPORT.yml | `[BUG] - <title>` | `bug` |
| `spike` | SPIKE-REQUEST.yml | `[REQUEST] - <title>` | — |

## Step 0: Check for Existing Related Issues

Before creating a new issue, search for existing issues (open or closed) that may already cover this work or could be a parent for it:

```bash
# Search for related issues using keywords from the user's description
gh issue list --search "<keywords>" --state all --limit 10 --json number,title,state,labels,url
```

Also check the current sprint for related work:

```bash
# List current sprint issues from the project board
gh project item-list 1 --owner nudgebee --format json --limit 200 | jq '[.items[] | select(.status != "Done") | {number: .content.number, title: .content.title, type: .content.type, status: .status}]' 2>/dev/null | head -50
```

If related issues exist, present them to the user:

```
I found these potentially related issues:
- #1234 [open] - <title>
- #5678 [closed] - <title>

Options:
1. Link to an existing issue (add a comment or reference)
2. Create a new issue anyway
3. Skip issue creation
```

Only proceed to create a new issue if the user confirms none of the existing issues cover the work.

## Step 1: Determine Issue Type

If `$ARGUMENTS` specifies a type (`feature`, `bug`, `spike`), use that. Otherwise, ask the user:

```
What type of issue would you like to create?
- feature: New feature or enhancement request
- bug: Report a bug or defect
- spike: Exploratory work to answer a question
```

## Step 2: Gather Information Based on Type

### For Feature Request

Ask or infer from context:

1. **Title**: Short descriptive title for the feature
2. **Summary** (required): Brief explanation of the feature
3. **Basic Example** (required): Specific examples of how the feature would work
4. **Drawbacks** (required): Potential drawbacks or impacts
5. **Unresolved Questions** (optional): Questions that remain unresolved
6. **Reference Issues** (optional): Related issue numbers

### For Bug Report

Ask or infer from context:

1. **Title**: Short descriptive title for the bug
2. **Description** (required): Explicit description of the issue
3. **Reproduction URL** (required): GitHub URL or relevant link
4. **Reproduction Steps** (required): Step-by-step instructions to reproduce
5. **Screenshots** (optional): Screenshots if applicable
6. **Logs** (optional): Relevant log output
7. **Browsers** (optional): Affected browsers (Firefox, Chrome, Safari, Edge, Opera)
8. **OS** (optional): Affected operating systems (Windows, Linux, Mac)

### For Spike Request

Ask or infer from context:

1. **Title**: Short descriptive title for the spike
2. **Summary** (required): Brief explanation of the exploration
3. **Objectives** (required): What you want to learn/answer
4. **Result Summary** (required): Expected outcome format
5. **Next Steps** (required): What happens after the spike
6. **Unresolved Questions** (optional): Open questions
7. **Reference Issues** (optional): Related issue numbers

## Step 3: Generate Issue Content

Based on the type, format the issue body in markdown:

### Feature Request Body

```markdown
## Summary
{summary}

## Basic Example
{basic_example}

## Drawbacks
{drawbacks}

## Unresolved Questions
{unresolved_questions or "None"}

## Reference Issues
{reference_issues or "None"}
```

### Bug Report Body

```markdown
## Description
{description}

## Reproduction URL
{reproduction_url}

## Reproduction Steps
{reproduction_steps}

## Screenshots
{screenshots or "N/A"}

## Logs
```
{logs or "N/A"}
```

## Environment
- **Browsers**: {browsers or "N/A"}
- **OS**: {os or "N/A"}
```

### Spike Request Body

```markdown
## Summary
{summary}

## Objectives
{objectives}

## Result Summary
{result_summary}

## Next Steps
{next_steps}

## Unresolved Questions
{unresolved_questions or "None"}

## Reference Issues
{reference_issues or "None"}
```

## Step 4: Confirm with User

Show the user the formatted issue:

```
Title: {title_with_prefix}
Labels: {labels}
Body:
---
{body}
---

Create this issue? (yes/no)
```

## Step 5: Create the Issue

Use GitHub CLI to create the issue:

```bash
gh issue create 
  --title "{title}" 
  --body "$(cat <<'EOF'
{body}
EOF
)" 
  --label "{labels}"  # Only if labels exist
```

## Step 6: Add to Project with Current Iteration

After creating the issue, automatically add it to the project board and set iteration to "current":

```bash
# Get the issue number from the created issue URL
ISSUE_NUMBER={extracted_issue_number}

# Add issue to the project (project number 1 = "Nudgebee" main project)
gh project item-add 1 --owner nudgebee --url "https://github.com/nudgebee/nudgebee/issues/${ISSUE_NUMBER}"

# Get the item ID for the newly added issue
ITEM_ID=$(gh project item-list 1 --owner nudgebee --format json --limit 1000 | jq -r ".items[] | select(.content.number == ${ISSUE_NUMBER}) | .id")

# Set the iteration field to current iteration
# Iteration field ID: PVTIF_lADOCG7t1c4ATt4GzgMmEFQ
gh project item-edit --project-id PVT_kwDOCG7t1c4ATt4G --id "${ITEM_ID}" --field-id PVTIF_lADOCG7t1c4ATt4GzgMmEFQ --iteration-id "@current"
```

**Note**: If the project commands fail (e.g., project not found or permissions), the issue is still created successfully. The iteration assignment is a best-effort addition.

## Step 7: Output Result

```
Issue created: {url}
Title: {title}
Type: {type}
Number: #{number}
Iteration: Current (if project assignment succeeded)
```

## Context-Aware Creation

If the user is working on code changes and asks to create an issue, try to infer:

- **Feature**: If they've implemented something new, suggest documenting it as a feature request for tracking
- **Bug**: If they've fixed something, suggest creating a bug report to document the issue
- **Spike**: If they've been exploring/researching, suggest a spike to document findings

Example: After implementing the new/recurring issues feature, suggest:
```
Would you like to create a feature request issue to track this work?
Title: [REQUEST] - Add new vs recurring issue tracking to Kubernetes events
```
