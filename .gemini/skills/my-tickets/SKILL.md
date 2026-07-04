---
name: my-tickets
description: Show my pending sprint tickets and help knock them off one by one
---

# My Tickets — Sprint Productivity Helper

Show my pending GitHub issues for the current sprint and help me knock them off. The goal is to streamline productivity: see what's pending, pick a ticket, understand it, and get it done — whether that means implementing it directly or researching first.

## Phase 1: Fetch My Pending Tickets

### Step 1: Get GitHub username

```bash
GH_USER=$(gh api user --jq '.login')
```

### Step 2: Query the project board

Fetch all items from the Nudgebee project board (project #1, org: nudgebee) using GraphQL:

```bash
gh api graphql --paginate -f query='
query($endCursor: String) {
  organization(login: "nudgebee") {
    projectV2(number: 1) {
      items(first: 100, after: $endCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          id
          fieldValueByName(name: "Status") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          fieldValueByName(name: "Iteration") {
            ... on ProjectV2ItemFieldIterationValue { title startDate duration }
          }
          fieldValueByName(name: "Priority") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          fieldValueByName(name: "Story Point") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          content {
            ... on Issue {
              number
              title
              url
              state
              assignees(first: 10) { nodes { login } }
              labels(first: 10) { nodes { name } }
            }
          }
        }
      }
    }
  }
}'
```

### Step 3: Filter and display

From the results, filter for items that match ALL of:
- **Current iteration** — iteration whose date range includes today
- **Assigned to me** — assignees includes `$GH_USER`
- **Pending status** — only these statuses:
  - 🆕 New
  - 🔖 Ready
  - 🔁 Re Open
  - 🏗 In progress

Exclude: 👀 In review, 🔎 QA, 🎬 QA - Prod, ✅ Done, ❌Invalid, Won't Fix.

### Step 4: Show the ticket board

Display as a sorted markdown table (In progress first, then Ready, Re Open, New):

```
### My Pending Tickets — Sprint {iteration_title}

| # | Title | Status | Priority | SP |
|---|-------|--------|----------|----|
| 1 | #123 Fix login bug | In progress | High | 3 |
| 2 | #456 Add dark mode | Ready | Medium | 5 |
| 3 | #789 Update docs | New | Low | 1 |

Summary: X tickets pending, Y story points remaining
```

If no tickets found, report "No pending tickets for this sprint!" and stop.

## Phase 2: Help Pick and Work on a Ticket

### Step 5: Ask which ticket to work on

Use AskUserQuestion to ask the user which ticket they want to tackle. List the tickets as options (use the ticket number + short title as the label). Recommend tickets in this priority: "In progress" first (already started), then "Ready" (clear requirements), then others.

### Step 6: Load and analyze ticket details

Once the user picks a ticket, fetch the full issue details:

```bash
gh issue view {ISSUE_NUMBER} --json title,body,labels,comments,assignees
```

Read the issue body and all comments carefully. Then proceed to triage.

## Phase 3: Triage — Identify Cross-Team Dependencies

Before starting any work, analyze the ticket for **cross-service impact**. A single ticket often requires changes across multiple services/teams.

### Step 7: Analyze the full scope of changes

Search the codebase to understand the impact:

1. **Identify all affected services** — Based on the ticket description, search for related code across the monorepo:
   - Backend services: api-server, ticket-server, collector-server, llm-server, etc.
   - Frontend: app (Next.js)
   - ML/Data: ml-k8s-server, rag-server
   - Infrastructure: auto-pilot, notifications-server

2. **Check for cross-service dependencies** — Look for:
   - **Backend ticket needing UI changes**: Does the backend change add/modify an API response, a new field, a new endpoint, or change behavior that the frontend consumes? Search `app/src/` for usages of the affected API endpoints or GraphQL queries.
   - **UI ticket needing backend/Hasura changes**: Does the UI need new data that doesn't exist yet? Check if the required API endpoints or Hasura permissions exist.
   - **Backend ticket needing Hasura migration**: Does it require new DB columns, tables, or permission changes? Check `api-server/migrations/`.
   - **Feature needing notification support**: Does the feature trigger notifications? Check notifications-server.
   - **Feature needing collector changes**: Does it affect data collection or metrics? Check collector-server.

3. **Map the dependency chain** — For each affected service, note:
   - What changes are needed
   - Which team owns it
   - Whether it blocks your work or can be done in parallel

### Step 8: Present triage findings

Present the triage to the user:

```
Triage for #{number} — {title}

Your scope (what you'll implement):
- {service}: {changes needed}

Dependencies found:
- [UI] {description of UI changes needed} — needs a ticket for the frontend team
- [Hasura] {description of migration needed} — needs a migration before/after your changes
- [Notifications] {description} — can be done in parallel
(or "No cross-team dependencies found.")

Blockers:
- {any dependency that must be done BEFORE your work}
(or "None — you can start immediately.")
```

### Step 9: Create dependency tickets (with user approval)

If cross-team dependencies are found, ask the user:

```
I found dependencies that need separate tickets. Should I create them?
```

For each dependency the user approves, create a GitHub issue using the `/create-issue` skill pattern:

```bash
gh issue create 
  --title "[REQUEST] - {dependency description}" 
  --body "$(cat <<'EOF'
## Summary
This is a dependency of #{parent_issue_number} — {parent_title}.

{Description of what needs to be done in this service}

## Context
Parent ticket: #{parent_issue_number}
Changes in parent: {brief summary of parent changes that create this dependency}

## Acceptance Criteria
- {specific criterion 1}
- {specific criterion 2}

## Reference Issues
- #{parent_issue_number}
EOF
)"
```

After creating, add each new issue to the project board with current iteration:
```bash
gh project item-add 1 --owner nudgebee --url "https://github.com/nudgebee/nudgebee/issues/${NEW_ISSUE_NUMBER}"
```

Link the dependency tickets back to the parent by adding a comment:
```bash
gh issue comment {PARENT_ISSUE_NUMBER} --body "Dependencies created:
- #{dep1_number} — {dep1_title}
- #{dep2_number} — {dep2_title}"
```

## Phase 4: Assess Readiness and Start Work

After triage, assess the ticket's **readiness**:

**A ticket has CLEAR requirements if it has:**
- Specific acceptance criteria or a clear description of what to build/fix
- Enough context to identify which files/services are affected
- Reproduction steps (for bugs) or feature specification (for features)
- No unresolved blockers from triage

**A ticket NEEDS RESEARCH if:**
- The description is vague, one-liner, or missing key details
- It's unclear which service or area of code is affected
- It references external systems or concepts that need investigation
- It's a spike or exploration task
- Comments show ongoing discussion without resolution
- Triage revealed unknowns that need investigation

## Phase 4A: Ticket Has Clear Requirements — Implement

If the ticket has clear requirements:

### Step 10A: Present implementation plan

1. Identify affected services from labels, title, or description.
2. Read the relevant service CLAUDE.md if one exists.
3. Search the codebase to understand the current state of the code that needs changing.
4. Use EnterPlanMode to present a thorough implementation plan:

```
Ticket #{number} — {title}

What needs to be done:
{extracted requirements}

Affected service(s): {services}

Implementation plan:
1. {step 1}
2. {step 2}
...
```

### Step 11A: Implement after plan approval

1. Create a branch from latest main:
   ```bash
   git fetch origin main
   git checkout -b {type}/{issue-number}-short-description origin/main
   ```
   Use `fix/` for bugs, `feature/` for features, `spike/` for spikes — infer from issue labels/title.

2. Implement the changes following project conventions.

3. After implementation, offer to validate (`/validate`) and commit (`/commit`).

## Phase 4B: Ticket Needs Research — Investigate First

If the ticket needs research:

### Step 10B: Tell the user and start research

Inform the user:
```
This ticket needs some research before we can start. Let me investigate and I'll keep you in the loop.
```

Then do a thorough investigation:

1. **Search the codebase** — Find all code related to the ticket's topic. Use Grep, Glob, and Read to understand the current implementation.

2. **Read related issues/PRs** — Check for linked issues, parent issues, or referenced PRs:
   ```bash
   gh issue view {number} --json body --jq '.body' | grep -oE '#[0-9]+'
   ```
   Fetch those related issues for context.

3. **Check issue comments** — Read through all comments for additional context from team members.

4. **Explore external references** — If the issue references external docs, APIs, or tools, use WebSearch/WebFetch to gather information.

5. **Identify the affected code** — Map out which files, functions, and services are involved.

### Step 11B: Present research findings and ask for direction

Present findings to the user with AskUserQuestion:

```
Research Summary for #{number} — {title}

What I found:
- {finding 1}
- {finding 2}
- {finding 3}

Affected code:
- {file1}: {what it does}
- {file2}: {what it does}

Open questions:
- {question 1}
- {question 2}

Possible approaches:
1. {approach A — pros/cons}
2. {approach B — pros/cons}
```

Ask the user to clarify open questions and pick an approach. Once the user confirms direction, proceed to Phase 4A (plan and implement).

## Fallback

If the GraphQL query fails (permissions, network, etc.), fall back to:

```bash
gh issue list --assignee @me --state open --limit 30 --json number,title,labels,url
```

Note to the user that sprint filtering is unavailable and showing all open issues instead.
