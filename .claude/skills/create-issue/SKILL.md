---
description: Create GitHub issues using repo templates (feature, bug, spike)
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - AskUserQuestion
---

# Create GitHub Issue

Create a GitHub issue using the repository's issue templates. Optional argument: `$ARGUMENTS` (issue type: `feature`, `bug`, or `spike`).

## Available Issue Types

| Type | Template | Title Format | Labels |
|------|----------|--------------|--------|
| `feature` | FEATURE-REQUEST.yml | `[REQUEST] - <title>` | — |
| `bug` | BUG-REPORT.yml | `[BUG] - <title>` | `bug` |
| `spike` | SPIKE-REQUEST.yml | `[REQUEST] - <title>` | — |

---

## Audience & Tone (read this first)

Issues are read by a **mixed audience**: PMs, support, QA, and engineers. Most readers skim the title and the first paragraph before deciding whether to care. Write the top of every issue for that reader, not for the engineer who will eventually fix it.

**Two-layer structure for every issue:**
1. **Top half — plain language.** Title + description + impact + reproduction described in terms of what a *user of the product* sees or does. Anyone in the company should understand it.
2. **Bottom half — `## Technical Details`.** Code paths, error messages, commit SHAs, library names, struct fields, migration IDs, log fragments. This is for whoever picks up the work.

**Rules of thumb:**

- **DO** lead with user-visible symptom and impact.
- **DO** describe reproduction the way a tester or customer would do it (UI clicks, settings, observable behaviour), not the way a developer would (SQL queries against internal tables).
- **DO** put internal terminology, code references, commits, library versions, log lines, and SQL errors under `## Technical Details`.
- **DON'T** put internal symbol names, library names, file paths, struct fields, error messages, or commit SHAs in the **title**.
- **DON'T** use internal jargon in the description without a one-line plain-English gloss first.
- **DON'T** assume the reader knows the codebase. Service names are fine; internal struct names, DAO methods, and migration filenames are not (those go in Technical Details).

### Title — symptom-first, plain language

A good title names **what is broken from the outside**, not **what the code is doing wrong inside**.

Rule: if a non-engineer reading the title can't tell **what the user notices**, it's too technical.

Things that almost never belong in a title:
- Function, method, or class names
- Struct or column names
- Library names and versions
- Migration filenames or version numbers
- SQL or error-message fragments
- Commit SHAs

### Description — lead with what the user sees

Before writing, answer for yourself:
1. **What does a user / customer / operator actually observe is wrong?**
2. **What is the blast radius?** (One feature? All tenants? Just dev?)
3. **Since when?** (Date or version, if known.)

Open the description with those three things in plain English. Then, *only after that*, you may say "Internally, the cause is…" with a one-sentence summary. Save the deep dive for `## Technical Details`.

### Reproduction — user actions, not developer actions

Reproduction steps should be something a tester, support engineer, or PM could follow without opening the codebase. Use UI flows, settings, and observable behaviour. If the bug genuinely has no user-visible surface (a background job, a silent data drift), say so explicitly in the description, then put the developer-level probe (SQL, log query, kubectl command) under `## Technical Details`.

---

## Step 0: Check for an Existing Ticket First (mandatory)

Before drafting anything, search for an existing issue that already covers this — open **and** closed:

```bash
gh issue list --state all --search "<keywords>" --limit 10 --json number,title,state
gh issue list --assignee "@me" --state open --limit 30 --json number,title
```

- **A related issue exists** → propose linking or commenting on it instead of creating a duplicate. Only proceed to create if the user confirms the existing one doesn't cover this.
- **This work stems from a parent/original ticket** (sprint ticket, epic, prior bug) → carry that number forward; it MUST appear in the new issue's body (`Reference Issues` section or `Part of #<n>` in the description) AND be linked as a native sub-issue in Step 6.5, so traceability is preserved.

Never skip this step — orphan issues with no link to the work that spawned them are the failure mode this skill must avoid.

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

1. **Title**: Short descriptive title (user-outcome phrased, not implementation phrased)
2. **Summary** (required): What capability is missing and who needs it
3. **Basic Example** (required): How it should look from the user's side (UI flow, API call, etc.)
4. **Drawbacks** (required): Cost, complexity, who it might disrupt
5. **Unresolved Questions** (optional)
6. **Reference Issues** (optional)

### For Bug Report

Ask or infer from context. **Separate user-facing info from technical info up front** — you will need both, and they go in different sections of the body.

Form fields (top of body — these mirror `BUG-REPORT.yml` one for one):
1. **Title**: Symptom-first, no internal terminology. See "Title" rules above.
2. **Environment** (required): Production / QA / Test / Dev / Local only / Not sure. Pick the highest environment it reproduces in.
3. **Description** (required): What a user observes, in plain language.
4. **Impact** (required): Who is affected, how badly, since when.
5. **Link** (optional): The affected page, dashboard, or conversation — where a reader should look first.
6. **Reproduction steps** (required): As a user would do it. Fall back to "observable via logs/DB only" if no UI surface exists.
7. **Logs** (optional): Raw log lines, stack traces, SQL errors.
8. **Screenshots** (optional).
9. **Client details** (optional): Browser / OS, only if the bug is client-side. Skip for backend bugs.

Technical (bottom of body, under `## Technical Details` — no counterpart in the form):
10. **Root cause** (if known): One short paragraph naming code paths, commits, migrations, dependencies.
11. **Code links**: File/line, commit, or PR that explains the cause. This used to go in the form's URL field; that field is now a user-facing `Link`, so code pointers belong here instead.

### For Spike Request

Ask or infer:

1. **Title**: The question the spike answers, not the implementation
2. **Summary** (required)
3. **Objectives** (required): The specific questions to answer
4. **Result Summary** (required): What the deliverable looks like (doc, prototype, decision memo)
5. **Next Steps** (required): What this unblocks
6. **Unresolved Questions** (optional)
7. **Reference Issues** (optional)

## Step 3: Generate Issue Content

### Feature Request Body

```markdown
## Summary
{summary — what capability is missing, who needs it, plain language}

## Basic Example
{basic_example — user-side flow, screenshots or pseudo-UI welcome}

## Drawbacks
{drawbacks}

## Unresolved Questions
{unresolved_questions or "None"}

## Reference Issues
{reference_issues or "None"}
```

### Bug Report Body

**The web form is the source of truth for this schema.** GitHub renders
`.github/ISSUE_TEMPLATE/BUG-REPORT.yml` as `###` headings whose text matches each
field's `label` exactly. Emit the same headings, in the same order, with the same
capitalisation — an issue filed by this skill must be indistinguishable from one
filed through the form, so that anything parsing the corpus sees one schema rather
than two. Do **not** use `##` for these, and do not rename them.

```markdown
### Environment
{Exactly one of: Production | QA / Test | Dev | Local only | Not sure}

### Description
{One paragraph, plain language, what the user observes is wrong. No internal symbol names. If you must reference an internal concept, gloss it in plain English first.}

### Impact
- **Who is affected**: {all tenants / specific feature users / dev-only / etc.}
- **Severity**: {what the user can't do, or what they see incorrectly}
- **Since when**: {date or version, "unknown" if not known}

### Link
{URL to the affected page, resource, conversation or dashboard — whatever gets a reader to the problem fastest. Optional; omit the heading entirely if there is nothing useful to link.}

### Reproduction steps
{Numbered steps a tester or support engineer could follow without reading the codebase. If the bug has no user-visible surface, say so and explain how to detect it — then put the probe in Technical Details.}

### Logs
{Raw log lines, stack traces, SQL errors. Omit the heading if none.}

### Screenshots
{Omit the heading if none.}

### Client details
{Browser / OS, e.g. "Chrome 128 / macOS". Only for UI bugs where the client is actually relevant — omit the heading for backend bugs.}

---

## Technical Details

{Free-form for engineers, and the one section with no counterpart in the form. It is additive: it sits after every form field, so the form-shaped part of the body still parses cleanly. Include any of: root-cause analysis, code paths with file:line, commit SHAs, migration IDs, library names and versions, struct/field names, SQL queries used to confirm the bug. Be as deep as helpful — this section has no audience constraint.}
```

> Notes for the agent generating this:
> - **Omit optional headings entirely** rather than writing "N/A" or "None" — an empty section is worse than an absent one. This applies to `Link`, `Logs`, `Screenshots`, and `Client details`.
> - `Environment`, `Description`, `Impact`, and `Reproduction steps` are required by the form. Always emit all four, even when you have to write "unknown".
> - `Environment` must be one of the five literal dropdown options, spelled exactly as above — it is a `dropdown`, and any other string will not match what the form produces.
> - The `## Technical Details` heading is mandatory whenever you have any internal information to convey. If genuinely none, omit it. Keep it at `##`, not `###` — that is what marks it as the non-form appendix.
> - **If you change the form, change this block in the same PR.** The two drifting apart is what made half the bug corpus unparseable in the first place.

### Spike Request Body

```markdown
## Summary
{summary}

## Objectives
{objectives — the specific questions this spike answers}

## Result Summary
{deliverable format — doc, prototype, decision memo}

## Next Steps
{what this unblocks}

## Unresolved Questions
{unresolved_questions or "None"}

## Reference Issues
{reference_issues or "None"}
```

## Step 4: Self-check before showing the draft

Before showing the user the draft, re-read your own title and first paragraph and ask:

1. **Title test** — Could a PM who doesn't read code tell from the title alone what users will notice? If not, rewrite.
2. **Jargon test** — Does the description contain any of: a struct name, a library version, a migration filename, a SQL error message, a commit SHA, a function name? If yes, move it to Technical Details.
3. **Impact test** — Can a reader tell who is affected and how badly within the first two paragraphs? If not, add an Impact section.
4. **Reproduction test** — Could someone reproduce this without reading source code? If not, say so explicitly and put the developer-level repro under Technical Details.

If any test fails, fix it before Step 5.

## Step 5: Confirm with User

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

## Step 6: Create the Issue

Use GitHub CLI to create the issue. Always assign the creator (`@me`):

```bash
gh issue create \
  --title "{title}" \
  --body "$(cat <<'EOF'
{body}
EOF
)" \
  --assignee "@me" \
  --label "{labels}"  # Only if labels exist
```

## Step 6.5: Link to the Parent Ticket (native sub-issue)

If Step 0 identified a parent/original ticket (epic, sprint ticket, or the bug this work spun out of), create a **native sub-issue link** — not just a body mention. The body reference is for readers; the native link is what makes the relationship visible on the parent, the board, and in tracking views.

```bash
PARENT_NUMBER={parent_issue_number}
REPO_OWNER=nudgebee
REPO_NAME=$(gh repo view --json name -q .name)

ISSUE_ID_QUERY='query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { issue(number: $number) { id } } }'
PARENT_ID=$(gh api graphql -f query="$ISSUE_ID_QUERY" -f owner="$REPO_OWNER" -f name="$REPO_NAME" -F number="$PARENT_NUMBER" --jq '.data.repository.issue.id')
CHILD_ID=$(gh api graphql -f query="$ISSUE_ID_QUERY" -f owner="$REPO_OWNER" -f name="$REPO_NAME" -F number="$ISSUE_NUMBER" --jq '.data.repository.issue.id')

gh api graphql \
  -f query='mutation($parentId: ID!, $childId: ID!) { addSubIssue(input: {issueId: $parentId, subIssueId: $childId}) { subIssue { number } } }' \
  -f parentId="$PARENT_ID" -f childId="$CHILD_ID"
```

A closed parent is a valid link target (follow-up work). If the parent is a PR rather than an issue, skip the native link — sub-issues only accept issues — and keep the body reference. If the mutation fails, report it in Step 8; do not drop the link silently.

## Step 7: Add to Project, set Iteration + Story Point

After creating the issue, add it to the org-level `nudgebee` project (#1) and set the **current iteration** and **Story Point**.

> The project is **org-level** (`nudgebee` org, project #1) and shared across all repos including `nudgebee-enterprise`, so these commands are the same regardless of which repo the issue lives in. Pass the issue's actual repo in the `--url`.

**Story Point** — before running the commands, ask the user to pick a value (`1`, `2`, `3`, `5`, `8`, `13`). If they skip, omit the Story Point edit.

```bash
ISSUE_REPO="nudgebee/nudgebee-enterprise"   # or nudgebee/nudgebee, whichever the issue is in
ISSUE_NUMBER={extracted_issue_number}
STORY_POINT="{user_choice_or_empty}"        # one of 1/2/3/5/8/13, or empty to skip

PROJECT_ID="PVT_kwDOCG7t1c4ATt4G"
ITER_FIELD_ID="PVTIF_lADOCG7t1c4ATt4GzgMmEFQ"
SP_FIELD_ID="PVTSSF_lADOCG7t1c4ATt4GzgPeoDE"

# Add to project
gh project item-add 1 --owner nudgebee --url "https://github.com/${ISSUE_REPO}/issues/${ISSUE_NUMBER}"

# The board has >1000 items and new items land at the end, so `gh project item-list`
# with any --limit never finds a fresh item. Resolve the item id via the issue itself:
ITEM_ID=$(gh api graphql \
  -f query='query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { issue(number: $number) { projectItems(first: 5) { nodes { id project { number } } } } } }' \
  -f owner="${ISSUE_REPO%/*}" -f name="${ISSUE_REPO#*/}" -F number="$ISSUE_NUMBER" \
  --jq '.data.repository.issue.projectItems.nodes[] | select(.project.number == 1) | .id')

# Resolve the CURRENT iteration id (gh does NOT support an "@current" token —
# it requires a literal iteration node id). Pick the latest iteration whose
# startDate is on or before today.
CURRENT_ITER=$(gh api graphql -f query='
query {
  organization(login: "nudgebee") {
    projectV2(number: 1) {
      field(name: "Iteration") {
        ... on ProjectV2IterationField {
          configuration { iterations { id startDate } }
        }
      }
    }
  }
}' | jq -r --arg today "$(date +%Y-%m-%d)" \
      '[.data.organization.projectV2.field.configuration.iterations[]
        | select(.startDate <= $today)] | sort_by(.startDate) | last | .id')

gh project item-edit --project-id "$PROJECT_ID" --id "$ITEM_ID" \
  --field-id "$ITER_FIELD_ID" --iteration-id "$CURRENT_ITER"

# Story Point (single-select) — only if the user chose one
if [ -n "$STORY_POINT" ]; then
  SP_OPTION_ID=$(gh project field-list 1 --owner nudgebee --format json \
    | jq -r ".fields[] | select(.name==\"Story Point\") | .options[] | select(.name==\"${STORY_POINT}\") | .id")
  gh project item-edit --project-id "$PROJECT_ID" --id "$ITEM_ID" \
    --field-id "$SP_FIELD_ID" --single-select-option-id "$SP_OPTION_ID"
fi
```

**Verify — this is NOT best-effort.** An unassigned issue with no iteration is invisible on the sprint board, which defeats the point of filing it. After the commands above, confirm all three fields actually landed:

```bash
gh issue view "$ISSUE_NUMBER" --json assignees --jq '.assignees | length'   # must be >= 1

# Do NOT use `gh project item-list --limit 1000` here — the board has >1000 items and
# fresh items are appended at the end, so a new issue never shows up in the first page.
# Query the issue's own project items instead:
gh api graphql \
  -f query='query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { issue(number: $number) { projectItems(first: 5) { nodes { project { number } fieldValueByName(name: "Iteration") { ... on ProjectV2ItemFieldIterationValue { title } } } } } } }' \
  -f owner="${ISSUE_REPO%/*}" -f name="${ISSUE_REPO#*/}" -F number="$ISSUE_NUMBER" \
  --jq '.data.repository.issue.projectItems.nodes[] | select(.project.number == 1) | {iteration: (.fieldValueByName.title // null)}'
```

- Assignee count 0 → fix immediately: `gh issue edit "$ISSUE_NUMBER" --add-assignee "@me"`.
- Item missing or `iteration` null → re-run the item-add / item-edit commands once; if it still fails, **report the failure explicitly in Step 8** with the error text so the user can fix it — do not silently declare success.

## Step 8: Output Result

```
Issue created: {url}
Title: {title}
Type: {type}
Number: #{number}
Assignee: @me
Iteration: {current_iteration_title} (if project assignment succeeded)
Story Point: {value or "unset"}
```

---

## Context-Aware Creation

If the user is working on code changes and asks to create an issue, try to infer the type:

- **Feature**: They've implemented something new — document it as a feature request for tracking.
- **Bug**: They've fixed something — document it as a bug report.
- **Spike**: They've been exploring/researching — document findings as a spike.

When inferring, **still apply the audience/tone rules above**. A bug discovered by an engineer is still read by PMs.
