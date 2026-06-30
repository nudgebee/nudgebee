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

User-facing (top of body):
1. **Title**: Symptom-first, no internal terminology. See "Title" rules above.
2. **Description** (required): What a user observes, in plain language.
3. **Impact** (required): Who is affected, how badly, since when.
4. **Reproduction Steps** (required): As a user would do it. Fall back to "observable via logs/DB only" if no UI surface exists.

Technical (bottom of body, under `## Technical Details`):
5. **Root cause** (if known): One short paragraph naming code paths, commits, migrations, dependencies.
6. **Reproduction URL** (required by template): Link to the file/line, commit, or PR that explains the cause.
7. **Logs / errors** (optional): Raw log lines, stack traces, SQL errors.
8. **Screenshots** (optional).
9. **Browsers / OS** (optional): Only if the bug is client-side. Skip for backend bugs.

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

```markdown
## Description
{One paragraph, plain language, what the user observes is wrong. No internal symbol names. If you must reference an internal concept, gloss it in plain English first.}

## Impact
- **Who is affected**: {all tenants / specific feature users / dev-only / etc.}
- **Severity**: {what the user can't do, or what they see incorrectly}
- **Since when**: {date or version, "unknown" if not known}

## Reproduction Steps
{Numbered steps a tester or support engineer could follow without reading the codebase. If the bug has no user-visible surface, say so and explain how to detect it — then put the probe in Technical Details.}

## Reproduction URL
{GitHub link to the most relevant file/line/commit. Required by template.}

---

## Technical Details

{Free-form for engineers. Include any of: root-cause analysis, code paths with file:line, commit SHAs, migration IDs, library names and versions, struct/field names, SQL queries used to confirm the bug, stack traces, log lines. Be as deep as helpful — this section has no audience constraint.}

### Logs / Errors
```
{logs}
```

### Screenshots
{screenshots}

### Environment (client-side bugs only)
- **Browsers**: {browsers}
- **OS**: {os}
```

> Notes for the agent generating this:
> - If there is no useful screenshot, browsers list, or OS list, **omit those subsections entirely** rather than writing "N/A" — keep the issue tight.
> - If the bug is purely backend, omit the **Environment** subsection.
> - The `## Technical Details` heading is mandatory whenever you have any internal information to convey. If genuinely none, omit it.

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

ITEM_ID=$(gh project item-list 1 --owner nudgebee --format json --limit 1000 \
  | jq -r ".items[] | select(.content.number == ${ISSUE_NUMBER}) | .id")

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

**Note**: If the project commands fail (e.g., project not found or permissions), the issue is still created successfully. The iteration/story-point assignment is best-effort. Assignee is set on the issue itself in Step 6 (`--assignee "@me"`), not as a project field — the project's Assignees column mirrors the issue's assignees automatically.

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
