---
description: Picks the next un-commented ticket from the Nudgebee QA-testing project board, reads the issue and every linked PR to work out what actually changed, then posts QA testing steps on that ticket as a GitHub comment written in plain English a non-technical tester can follow. Skips any ticket that already carries a testing-steps comment, so two testers running it never double-post. Use when a tester asks for testing steps, wants the QA board processed, or asks what to test next.
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Grep
  - Glob
  - AskUserQuestion
---

# QA Testing Steps (board-driven)

Post QA testing steps onto tickets sitting in the **QA-testing** column of the Nudgebee project board.

**Repo:** `nudgebee/nudgebee-enterprise` · **Board:** org `nudgebee`, project `1`, view `9` ("QA-testing", filter `status:"🔎 QA"`) · **Review env:** test

**One ticket per run.** Pick the newest pending ticket, review it properly, post one comment, report what is left. The tester calls the skill again for the next one.

## Non-negotiables

- **Never post twice on the same ticket.** Every comment this skill writes starts with the marker `<!-- qa-testing-steps:v1 -->`. A ticket that already has a comment carrying that marker is *skipped* — see [Duplicate guard](#step-2-duplicate-guard).
- **The comment is pure English, plain and non-technical.** A tester who has never opened the codebase must be able to follow it. No file paths, no function names, no React/API jargon in the steps. Conversational output *to the user in chat* stays Hinglish in Roman script.
- **Never write test results.** This skill writes steps to run, not outcomes. No PASS/FAIL anywhere.
- **Never invent UI.** Every click path in the comment must come from something actually seen in the issue, the PR diff, or the code. If you cannot tell where a thing lives, say so in the comment and ask the developer, rather than guessing a menu path.
- **Never write a full URL — path only.** Write `accounts/account-form?cloudProvider=NEWRELIC`, never `https://<host>/accounts/account-form?...`. The tester is already signed in to the environment they are testing; a hardcoded host sends them somewhere else, and it goes stale the moment an environment is renamed. Say "test" once in the **Where** line and let every step carry the path alone.

## Arguments

| Invocation | Behaviour |
|---|---|
| *(none)* | Newest pending ticket from the board. |
| `<issue-number>` or issue URL | That specific ticket. Still honours the duplicate guard. |
| `next N` | Same as no-arg but processes N tickets in sequence. Use sparingly — each one is a full review. |
| `--force` | Post even if the marker is already present. Only when the user explicitly asks to redo a ticket; say plainly in chat that you are overriding the guard. |

---

## Phase 0 — Pick the ticket

### Step 1: Pull the board

`jq` is **not** installed on this machine. Use `gh`'s built-in `--jq` instead — a bare `jq` in a pipe fails with `command not found`.

The board has ~3500 items and Projects v2 has no server-side status filter, so page all items and filter locally. This takes ~85s.

Write the query to a file first, then page it:

```bash
SCRATCH=/tmp/qa-board; mkdir -p "$SCRATCH"
```

Query (`$SCRATCH/q.graphql`):

```graphql
query($cursor: String) {
  organization(login: "nudgebee") {
    projectV2(number: 1) {
      items(first: 100, after: $cursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          fieldValueByName(name: "Status") {
            ... on ProjectV2ItemFieldSingleSelectValue { name }
          }
          content {
            ... on Issue { number title url state repository { nameWithOwner } }
            ... on PullRequest { number title url state repository { nameWithOwner } }
          }
        }
      }
    }
  }
}
```

Paging loop:

Filter and format inside the `--jq` expression rather than parsing raw JSON afterwards — key order and missing fields make a `grep`/`sed` pass over JSON fragile. Each page emits `<number> <state>` lines plus one `PAGEINFO` line:

```bash
Q=$(cat "$SCRATCH/q.graphql")
JQ='.data.organization.projectV2.items
    | (.nodes[]
       | select(.fieldValueByName?.name == "🔎 QA")
       | select(.content?.number)
       | "\(.content.number) \(.content.state)"),
      "PAGEINFO \(.pageInfo.hasNextPage) \(.pageInfo.endCursor)"'
cursor=null; : > "$SCRATCH/qa.txt"
while :; do
  if [ "$cursor" = "null" ]; then out=$(gh api graphql -f query="$Q" --jq "$JQ")
  else out=$(gh api graphql -F cursor="$cursor" -f query="$Q" --jq "$JQ"); fi
  echo "$out" | grep -v '^PAGEINFO ' >> "$SCRATCH/qa.txt" || true
  info=$(echo "$out" | grep '^PAGEINFO ' | tail -1)
  [ "$(echo "$info" | awk '{print $2}')" = "true" ] || break
  cursor=$(echo "$info" | awk '{print $3}')
done
wc -l < "$SCRATCH/qa.txt"
```

The two `select`s matter: project items with no status set, and draft items with no issue behind them, both appear as nulls and would otherwise crash the format string.

Cache `qa.txt` for the session — if it already exists from minutes ago, reuse it rather than re-paging. Expect the set to drift between runs; tickets move in and out of the QA column while you work.

Both **OPEN and CLOSED** tickets in this column are in scope; a closed ticket still needs QA. Order **newest first** (highest issue number):

```bash
sort -rn "$SCRATCH/qa.txt"
```

### Step 2: Duplicate guard

Walk the ordered list and take the **first ticket with no marker**:

```bash
gh api repos/nudgebee/nudgebee-enterprise/issues/<N>/comments \
  --jq '.[].body' | grep -c 'qa-testing-steps:v1' || true
```

`0` → pending, this is your ticket. Anything else → already covered by another tester, skip it silently and move to the next number.

> `grep -c` exits `1` when the count is `0`. Keep the `|| true` so the guard reads as "not yet commented" instead of aborting the run.

> **Gotcha:** `gh issue view --comments` fails on this repo with a *"Projects (classic) is being deprecated"* GraphQL error. Always use the `gh api` form.

Tell the user in chat which ticket was picked and how many were skipped as already-covered.

---

## Phase 1 — Understand what actually changed

### The issue

```bash
gh issue view <N> --repo nudgebee/nudgebee-enterprise \
  --json number,title,state,labels,assignees,milestone,createdAt,closedAt,author,body
gh api repos/nudgebee/nudgebee-enterprise/issues/<N>/comments \
  --jq '.[] | "=== @\(.user.login) \(.created_at) ===\n\(.body)\n"'
```

Read triage comments fully — they usually carry the evidence for why parts of the ask were dropped.

### The linked PRs

The issue timeline is the reliable source; it returns base branch and merge state in one call.

```bash
gh api graphql -f query='
query { repository(owner:"nudgebee",name:"nudgebee-enterprise"){
  issue(number:<N>){
    timelineItems(first:50, itemTypes:[CROSS_REFERENCED_EVENT,CONNECTED_EVENT]){
      nodes{
        ... on CrossReferencedEvent { source { ... on PullRequest { number title state url baseRefName mergedAt } } }
        ... on ConnectedEvent { subject { ... on PullRequest { number title state url baseRefName mergedAt } } }
      }
    }
  }
}}' --jq '.data.repository.issue.timelineItems.nodes[] | (.source // .subject) | select(.number) | "PR #\(.number) [\(.state)] base=\(.baseRefName) merged=\(.mergedAt) \(.title)"'
```

Cross-check with `gh search prs --repo nudgebee/nudgebee-enterprise "<N>" --json number,title,state,url --limit 20` — it catches loose body mentions the timeline missed.

Then per PR:

```bash
gh pr view <PR> --repo nudgebee/nudgebee-enterprise \
  --json number,title,state,mergedAt,baseRefName,author,body,files,url
gh pr diff <PR> --repo nudgebee/nudgebee-enterprise
```

> **Gotcha:** there is no `merged` JSON field — use `state` + `mergedAt`.

**No linked PR at all?** Do not invent steps. Post nothing; tell the user the ticket has no PR yet and move to the next one.

### Is it live on test?

Review happens on **test**, so a PR merged to `main` is *not* automatically there. Every merge to `main` opens an auto PR to `test`, and the change only reaches the review env once that promotion merges. `baseRefName` tells you what to expect, but never assume — check the branch:

```bash
git fetch origin test --quiet
git show origin/test:<changed/file/path> | grep -n "<symbol the PR added>"
git log --oneline -3 origin/test -- <changed/file/path>
```

- Symbol **present** on `origin/test` → live on test, write the steps normally.
- Symbol **missing** → the promotion has not landed yet. Say so plainly at the top of the comment and mark those steps as not yet testable, rather than sending a tester to look for something that is not deployed.

A PR based on `prod` (hotfix) reaches test via the backmerge — same check applies.

### Read the changed code

The diff says *what*; the surrounding code says *when it runs*. This is where the sharp test cases come from. Look for:

- **Effect ordering** — a `useEffect` with `[]` runs once at mount, possibly before an async gate resolves. The single most common real defect in flag/permission work.
- **Fail-closed vs fail-open** — `hasFeatureAccessCached` is fail-closed and needs `fetchFeatureFlagsForTenant()` to warm the cache first.
- **Double guards** — a tab hidden in one place but still rendered in another.
- **Initial state** — a gate defaulting to `false` means a visible flash or a late-appearing element.

---

## Phase 2 — Classify: can QA even see this?

Decide from the **file paths** across all linked PRs. `app/` is the only frontend service in the monorepo; everything else is backend.

```bash
gh pr view <PR> --repo nudgebee/nudgebee-enterprise --json files --jq '.files[].path' | sed 's#/[^/]*$##' | sort -u
```

| What the PRs touched | Verdict | What the comment says |
|---|---|---|
| Only paths under `app/` | **UI testable** | Full click-level steps. |
| Paths under `app/` **and** elsewhere | **Partly testable** | Steps for the UI part; name the backend part plainly and say it needs developer evidence or log/API checking. |
| Nothing under `app/` | **Backend only** | Say there is no screen to check. Do not manufacture UI steps. |

For **backend only**, the comment should still be useful: name what changed in one plain sentence, state that nothing on screen changes, and list whatever a tester *can* still do — check a value the API returns, confirm a job produced output, confirm nothing that used to work broke — or say explicitly that the developer must attach evidence.

For **partly testable**, be precise about the seam. "The screen shows it; the calculation behind it cannot be checked from the UI" is the kind of sentence that helps.

---

## Phase 3 — Design the test cases

Cover each of these that applies. This checklist is for *your* thinking; the comment expresses them as ordinary steps, never as category names.

1. **Happy path** — feature on / permission granted. Does it render and work?
2. **Negative path** — feature off. Correctly hidden, no leftover gap or layout shift?
3. **Deep link while the feature is off** — open the path with the hash/route directly instead of clicking through. Highest-yield case in flag work: the mount-time route effect usually resolves before the async gate and strands the user on a blank screen. Predict it from Phase 1 and get it checked.
4. **Slow network** — DevTools → Network → Slow 3G, reload. Flicker, late-appearing element, layout shift?
5. **State change without a reload** — toggle the setting, navigate in-app. Does a `[]`-dependency effect leave stale state behind?
6. **RBAC** — read-only user with the feature on. The flag must not bypass permission checks.
7. **Regression on dropped items** — for anything the issue asked for but the PR did not ship, confirm nothing changed.
8. **Scope consistency** — tenant vs account vs cluster scoped. Check two of whatever the scope unit is.

Write each step so it names **where to click, what to do, and what should be seen**. Replace every piece of jargon:

- "feature flag gate" → "the setting that turns this on"
- "async race on mount" → "open the page directly from a pasted link, without clicking through"
- "RBAC read-only role" → "log in as a user who can only view, not edit"

---

## Phase 4 — Post the comment

### Re-check the guard immediately before posting

The review takes minutes. Another tester may have posted in that window — this is exactly the race the marker exists for. Re-run the check from Phase 0 Step 2 **right before** `gh issue comment`. If it now returns non-zero, abort the post, say so in chat, and move on.

### Comment shape

Keep the top short — verdict and setup visible without scrolling. Detail goes in `<details>`, per `docs/writing-for-readers.md`.

```markdown
<!-- qa-testing-steps:v1 -->
## QA Testing Steps

**Can QA test this on the UI?** Yes / Partly / No — backend only
**Where:** test · <menu path to the screen>
**You need:** <access level, any setting that must be on>

**What changed, in plain words**
<Two or three sentences. What was broken or missing before, what should happen now. No file names, no code terms.>

<details>
<summary>Test steps</summary>

### 1. <Short name of what this checks>
1. Go to ...
2. Click ...
3. **You should see:** ...

### 2. <...>
...

</details>

<details>
<summary>Notes and things to watch</summary>

- <Anything not testable on test yet, and why.>
- <Anything the PR deliberately did not fix, so it is not raised as a new bug.>
- <What to report back: which step, what you saw, a screenshot.>

</details>
```

Adapt freely — this is a shape, not a form to fill. For a backend-only ticket, drop the steps section and say what the tester should ask the developer for.

### Post

```bash
gh issue comment <N> --repo nudgebee/nudgebee-enterprise --body-file <path>
```

Write the body to a file first — inline `--body` mangles multi-line markdown. Put the file in the session scratchpad, never in the repo.

### Report back in chat

Hinglish. Say: which ticket got the comment, the comment URL, how many tickets were skipped as already-covered, and how many are still pending on the board.

---

## Checklist before reporting done

- [ ] Board pulled and filtered on the QA status; ordered newest-first
- [ ] Duplicate guard checked at pick time **and** re-checked immediately before posting
- [ ] Issue body, all comments, and every linked PR read — including diffs
- [ ] Confirmed whether the change is actually live on test (`git show origin/test:<path>`)
- [ ] UI / Partly / Backend-only verdict backed by real file paths
- [ ] Every applicable case type from Phase 3 covered
- [ ] Comment contains the `<!-- qa-testing-steps:v1 -->` marker as its first line
- [ ] Comment is plain English — no file paths or code terms in the steps
- [ ] Every link in the comment is a path only — no scheme, no host name
- [ ] No PASS/FAIL or invented result anywhere
- [ ] Nothing written into the repo working tree (`git status` clean)
