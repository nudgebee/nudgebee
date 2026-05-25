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

Use GitHub CLI to create the issue:

```bash
gh issue create \
  --title "{title}" \
  --body "$(cat <<'EOF'
{body}
EOF
)" \
  --label "{labels}"  # Only if labels exist
```

## Step 7: Add to Project with Current Iteration

After creating the issue, automatically add it to the project board and set iteration to "current":

```bash
ISSUE_NUMBER={extracted_issue_number}

gh project item-add 1 --owner nudgebee --url "https://github.com/nudgebee/nudgebee/issues/${ISSUE_NUMBER}"

ITEM_ID=$(gh project item-list 1 --owner nudgebee --format json --limit 1000 | jq -r ".items[] | select(.content.number == ${ISSUE_NUMBER}) | .id")

gh project item-edit --project-id PVT_kwDOCG7t1c4ATt4G --id "${ITEM_ID}" --field-id PVTIF_lADOCG7t1c4ATt4GzgMmEFQ --iteration-id "@current"
```

**Note**: If the project commands fail (e.g., project not found or permissions), the issue is still created successfully. The iteration assignment is best-effort.

## Step 8: Output Result

```
Issue created: {url}
Title: {title}
Type: {type}
Number: #{number}
Iteration: Current (if project assignment succeeded)
```

---

## Context-Aware Creation

If the user is working on code changes and asks to create an issue, try to infer the type:

- **Feature**: They've implemented something new — document it as a feature request for tracking.
- **Bug**: They've fixed something — document it as a bug report.
- **Spike**: They've been exploring/researching — document findings as a spike.

When inferring, **still apply the audience/tone rules above**. A bug discovered by an engineer is still read by PMs.
