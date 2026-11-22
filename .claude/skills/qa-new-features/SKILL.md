---
description: Build a QA-facing new-feature list straight from the sprint tickets on the project board. Ask the user for the project board view URL every time, resolve the sprints that view points at, pull every ticket in those sprints, keep the ones that are new features, and print a ticket-first feature checklist grouped by product area, each row carrying its assignees and linked PRs. By default every feature ticket is listed whatever its board status; a ready-to-test / not-in-QA-yet split is produced only when the user asks for it. No deployment PR and no git range are involved. Invoke ONLY when the user explicitly calls this skill by name or types /qa-new-features. Never start it on your own from a mention of a deploy, a sprint, or a question about what shipped.
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Write
  - Glob
  - Grep
  - AskUserQuestion
---

# QA New Features

**Invocation-only.** Run this skill exclusively when the user calls it — `/qa-new-features`, or by naming it. Do not start it because a sprint or a deploy came up in conversation, or because a question sounds like it wants a feature list. Answer that question directly instead, and offer the skill if it fits. Once invoked, the skill drives to completion on its own.

Turn **one or more sprints** into a QA-ready feature checklist.

The list is built **from the tickets**, not from pull requests. The board is the single input: every ticket in the chosen sprints is read, the ones that are new features are kept, and each becomes one row. Pull requests appear only as references hanging off a ticket row — they are never the source of the list, and no promotion PR, merge commit or git range is resolved.

The output is for a **tester**, not a release manager. Every row must be something a person can go and exercise. Bug fixes, UI tweaks, refactors, CI work and dependency bumps are deliberately excluded — the QA team tracks those separately.

## Nothing is hardcoded

No organisation, repository, project number, view number, iteration name, sprint length, issue type or board Status value appears anywhere in this skill. Every one of them is resolved at runtime from the URL the user pastes. This keeps the skill portable across forks and mirrors, and keeps the private board's contents out of a file that ships with the source.

The same rule applies to what you *write*. The report goes into a shared QA doc, so quote ticket titles as they are, but never paste board internals the user did not ask for — customer-account labels, priority or story-point fields. The board query below deliberately does not request them. Assignees are the one exception: the tester needs to know who to ask about a ticket, so each row carries them.

## Step 1 — Get the board link, every time

**Ask for the board link on every run, even when this conversation already used one.** Board views, filters and sprint boundaries move between runs. Reusing a link from earlier in the conversation silently reports against the wrong sprint. If the user passed the URL as the skill's argument, use it; otherwise ask, and stop until they answer:

> The project board **view** URL for the sprint you want the feature list for — `https://github.com/orgs/<org>/projects/<N>/views/<V>`.

**Do not ask for a deployment PR.** If the user pastes one anyway, say this skill now reads the sprint tickets and does not need it.

Parse the board URL into `ORG`, `PROJ` and `VIEW`:

```bash
SPRINT_URL="<pasted URL>"
ORG=$(echo "$SPRINT_URL"  | sed -nE 's#.*/orgs/([^/]+)/projects/.*#\1#p')
PROJ=$(echo "$SPRINT_URL" | sed -nE 's#.*/projects/([0-9]+).*#\1#p')
VIEW=$(echo "$SPRINT_URL" | sed -nE 's#.*/views/([0-9]+).*#\1#p')
```

A user-scoped board (`/users/<login>/projects/<N>`) uses the same queries with `user(login:)` in place of `organization(login:)`. If the URL is neither — a milestone, an issue-search link, a doc — say the sprint lives on a GitHub Projects board and ask for that view's URL.

## Step 2 — Resolve the sprints and the Status columns

One query returns the view's own filter, the full iteration calendar, and the board's Status options in column order:

```bash
gh api graphql -f query='
query($org:String!,$num:Int!,$view:Int!){
  organization(login:$org){ projectV2(number:$num){
    title
    view(number:$view){ number name filter }
    statusField: field(name:"Status"){ ... on ProjectV2SingleSelectField { options{ name } } }
    field(name:"Iteration"){ ... on ProjectV2IterationField {
      configuration { duration startDay
        iterations{ title startDate duration }
        completedIterations{ title startDate duration } } } }
  } }
}' -f org="$ORG" -F num="$PROJ" -F view="$VIEW"
```

Map the `filter` string to concrete iteration titles:

| Filter contains | Iteration to use |
|---|---|
| `iteration:@current` | the one whose `startDate` + `duration` window contains today |
| `iteration:@previous` | the one immediately before that, by `startDate` |
| `iteration:@next` | the one immediately after |
| `iteration:"<title>"` | that title, verbatim |
| **no `iteration:` term at all** | **ask the user which iterations** — offer the current one and the few before it |

That last row is common and is not an error: plenty of QA views filter on `status:` rather than on an iteration. Print the view's name and filter back to the user so they can see why they are being asked.

Build one list from `iterations` + `completedIterations`, sort by `startDate`, and index off the current window — do not assume `completedIterations[0]` is the previous sprint, and do not assume iterations are weekly. `duration` is a field for a reason, and a board can carry stale iterations from years back.

**Several sprints are allowed.** The user will often name them (*"178 and 179"*). Everything downstream takes `SPRINT` as a comma-separated list.

Echo the resolved names and dates back to the user before going further — Step 3 costs about eighty seconds, and running it against the wrong sprints wastes all of it.

Keep the Status options in the order returned. That order is the board's workflow, left to right, and Step 6 uses it to split "ready to test" from "not there yet" without naming any column in this file.

### Gotchas that will bite you

- **`jq` is not installed on this machine** (exit 127). Use `gh api --jq`, or Python. Never pipe to a bare `jq`.
- **Git Bash rewrites `/`-leading strings into Windows paths.** It breaks jq expressions that join with `"/"`, and any argument that looks like an absolute POSIX path. Build such strings in the shell, not inside `--jq`.
- **Nested heredocs break in Git Bash.** Keep scripts flat, and write long files with the Write tool rather than a heredoc.

## Step 3 — Pull the sprint board

Set `SC` to this session's scratchpad directory first — every intermediate goes there, and none of them belong in the repo:

```bash
SC="<your session scratchpad path>"   # from the Scratchpad Directory in your environment
mkdir -p "$SC"
```

ProjectV2 has **no server-side filter on a field**, so the whole board comes down and the iterations are filtered client-side. On a busy board that is thousands of items and **about eighty seconds**. Start it in the background as soon as the URL is parsed; reuse `board.json` for the rest of the session.

```bash
gh api graphql --paginate -f query='
query($org:String!,$num:Int!,$endCursor:String) {
  organization(login:$org) {
    projectV2(number:$num) {
      items(first: 100, after: $endCursor) {
        pageInfo { hasNextPage endCursor }
        nodes {
          iter: fieldValueByName(name: "Iteration") { ... on ProjectV2ItemFieldIterationValue { title startDate } }
          status: fieldValueByName(name: "Status") { ... on ProjectV2ItemFieldSingleSelectValue { name } }
          lpr: fieldValueByName(name: "Linked pull requests") { ... on ProjectV2ItemFieldPullRequestValue { pullRequests(first:10){nodes{number state}} } }
          content { ... on Issue {
              number title url state
              issueType { name }
              assignees(first:10){nodes{login}}
              labels(first:15){nodes{name}}
              parent { number title }
              closedByPullRequestsReferences(first:15, includeClosedPrs:true){nodes{number state}}
          } }
        }
      }
    }
  }
}' -f org="$ORG" -F num="$PROJ" > "$SC/board.json" 2>"$SC/board.err"
```

This asks for the fields the feature call needs and nothing else. Do not widen it to priority, story points or account labels — none of them affect the output, and all of them are board internals that would then have to be kept out of the report by hand.

Notes on the query, each one learned the hard way:

- **Redirect stderr to its own file, never `2>&1`.** `gh` prints a plain-text summary of any GraphQL error to stderr, and folding that into the response file breaks the JSON parse.
- `gh api graphql --paginate` emits **one JSON document per page**, concatenated. `json.load` chokes. Parse with `JSONDecoder().raw_decode` in a loop — the script in Step 4 does.
- `issueType` is the cleanest feature signal a ticket carries, and it is frequently set where no label is.
- `closedByPullRequestsReferences(includeClosedPrs: true)` plus the `Linked pull requests` board field give the ticket's PRs for the row's reference links. Without `includeClosedPrs` you lose merged PRs, which is most of them.
- `parent` gives sub-issue rollup — an epic's PRs hang off its children, not off the epic.
- A board carries draft and PR items with no issue `content`. Skip them rather than crashing.
- The board is live. Tickets move between iterations between runs, so a sprint's ticket count will not match what an earlier run reported. That is the board changing, not a bug.

## Step 4 — Extract the sprint's tickets

Write this to `$SC/tickets.py` with the Write tool, then run it. Do not try to inline it as `python -c`.

```python
import json, io, sys, collections

SC, SPRINTS = sys.argv[1], sys.argv[2].split(',')

def load_multi(fn):
    s = io.open(fn, encoding='utf-8').read()
    dec = json.JSONDecoder(); i = 0; out = []
    while i < len(s):
        while i < len(s) and s[i].isspace(): i += 1
        if i >= len(s): break
        o, i = dec.raw_decode(s, i); out.append(o)
    return out

items = []
for o in load_multi(SC + '/board.json'):
    items += o['data']['organization']['projectV2']['items']['nodes']

board = {}
for it in items:
    c = it.get('content') or {}
    if 'number' not in c: continue
    lp = ((it.get('lpr') or {}).get('pullRequests') or {}).get('nodes', [])
    cb = c['closedByPullRequestsReferences']['nodes']
    board[c['number']] = dict(
        num=c['number'], title=c['title'], url=c['url'], state=c['state'],
        type=(c.get('issueType') or {}).get('name'),
        assignees=[a['login'] for a in (c.get('assignees') or {}).get('nodes', [])],
        labels=[l['name'] for l in c['labels']['nodes']],
        parent=(c.get('parent') or {}).get('number'),
        prs={p['number']: p['state'] for p in cb + lp},
        iter=(it.get('iter') or {}).get('title'),
        status=(it.get('status') or {}).get('name'))

sprint = {n: t for n, t in board.items() if t['iter'] in SPRINTS}
if not sprint:
    sys.exit('no board items carry an iteration in %r' % SPRINTS)

kids = collections.defaultdict(list)
for n, t in board.items():
    if t['parent']: kids[t['parent']].append(n)

rows = []
for n, t in sorted(sprint.items()):
    prs = dict(t['prs'])
    for c in kids.get(n, []): prs.update(board[c]['prs'])
    rows.append(dict(
        num=n, title=t['title'], url=t['url'], state=t['state'], type=t['type'],
        labels=t['labels'], status=t['status'], iter=t['iter'], assignees=t['assignees'],
        parent=t['parent'], parent_in_sprint=t['parent'] in sprint,
        children_in_sprint=sorted(c for c in kids.get(n, []) if c in sprint),
        children_status={str(c): board[c]['status'] for c in kids.get(n, []) if c in sprint},
        assignees_rollup=sorted(set(t['assignees']).union(
            *[board[c]['assignees'] for c in kids.get(n, []) if c in sprint])),
        children_elsewhere=sorted(c for c in kids.get(n, []) if c not in sprint),
        prs_merged=sorted(p for p, s in prs.items() if s == 'MERGED'),
        prs_open=sorted(p for p, s in prs.items() if s == 'OPEN')))

io.open(SC + '/tickets.json', 'w', encoding='utf-8').write(json.dumps(rows, ensure_ascii=False, indent=1))

def tally(key):
    c = collections.Counter(r[key] or '(none)' for r in rows)
    return ' | '.join('%s %d' % kv for kv in c.most_common())

print('sprints %s | tickets %d' % ('+'.join(SPRINTS), len(rows)))
print('  by status : ' + tally('status'))
print('  by type   : ' + tally('type'))
print('  no PR at all: %d' % sum(1 for r in rows if not r['prs_merged'] and not r['prs_open']))
```

```bash
PYTHONIOENCODING=utf-8 python "$SC/tickets.py" "$SC" "$SPRINT"
```

Read `tickets.json`. **It is input to your judgement, not the report.** The script only narrows the board to the sprint and attaches each ticket's PRs and sub-issues; the feature call is yours.

## Step 5 — Decide what counts as a feature

The issue type and labels are the starting point, not the answer. **Read every ticket title in the sprint** — a sprint is a few hundred tickets at most, and a feature filed under the wrong type is exactly what this list exists to catch.

**Include** — new capability a tester can exercise:

- Tickets whose issue type or label marks them as a feature, enhancement or epic
- Titles carrying the repo's feature-request or epic prefix — read the templates in `.github/ISSUE_TEMPLATE/` for the prefixes actually in use
- New services, pages, tabs, panels, integrations, providers, agents
- Tickets whose title and label disagree — a bug label on a title that reads as an enhancement. Go by what the title describes, not by the label.
- Capability filed under a non-feature type — a task that first wires a service in, a bug ticket whose fix adds a whole new mode. Catch these by scanning non-feature titles for `add`, `support`, `introduce`, `enable`, `new`.

**Exclude:**

- Anything typed, labelled or titled as a bug
- Cleanup, refactor, performance, documentation, CI, infra and dependency tickets with no observable surface
- Tickets whose board Status marks them invalid or won't-fix
- Spikes and investigations — they produce a decision, not a surface
- Internal engine tuning — unless it sits under everything, in which case a planner or client-wrapper rewrite does earn a row

Test-automation and developer-tooling tickets are a judgement call: real capability, but a tester cannot exercise it in the product. Keep them out of the feature list and note them once at the end instead.

**Roll sub-issues up.** When a ticket's parent is in the same sprint (`parent_in_sprint`), fold it into the parent's row rather than giving it a row of its own, unless the child is a separately testable feature. When the parent is not in the sprint, the child stands alone — mention the parent epic in an aside.

When the parent epic is not in the sprint but several of its children are, and together they make up one integration or one surface, write them as one row that lists every child ticket and names the epic in an aside. Seven rows for one integration's backend, frontend and migration slices is noise.

When Step 6 splits the report, a folded row takes the **parent's** Status. Check `children_status` before writing it: when a folded child sits in an earlier column than its parent, name that child in an aside — *"webhook alert ingestion (#NNNNN) is still in review"* — so the tester knows which slice of the epic is not there yet. An epic in QA with half its sub-issues still in review is the commonest way this list overstates what is testable.

When a title is too thin to tell what a tester would exercise, read the ticket — `gh issue view <n> --json title,body --jq '.body'`. Do this for the unclear ones only, not for the whole sprint.

## Step 6 — One list by default; split by status only on request

**Default: one combined list.** Every feature ticket on the board in the chosen sprints goes into the list whatever its Status — QA, Done, In review, In progress, Ready and New alike. The tester wants the sprint's full set of new features, not only what has reached QA. Do not put a Status on the rows, and do not print ready / not-ready parts or a by-status tally. The only tickets that stay out on Status grounds are the invalid and won't-fix ones from Step 5. A row with no merged PR still gets a plain *no PR linked*, and Step 9 says that such rows may not be on any environment yet.

**Split only when the user asks** for what is ready to test, or for the split by name. Then follow the rest of this step.

The Status column is the only deployment signal this skill uses. Take the ordered Status options from Step 2 and place every feature ticket in one of two parts:

- **Part 1 — Ready to test.** Status is a QA column or anything to the right of it in the workflow — the ticket has been handed over, or is already past QA.
- **Part 2 — In the sprint, not in QA yet.** Status is anything to the left of the first QA column: new, ready, in progress, in review, reopened, blocked. Listed so the tester knows it is coming and does not go looking for it today.

Identify the first QA column by reading the option names at runtime, not by a name held here. If the board has no column that reads as a QA hand-off, say so and print one list ordered by Status instead of guessing a split.

Carry the ticket's **Status** onto every row, spelled as the board spells it minus any leading emoji.

Say plainly in the report that the split comes from the board, not from git — a ticket moved to QA by hand before its change was deployed will sit in Part 1 anyway. A Part 1 ticket with **no merged PR** (`prs_merged` empty) is the visible symptom; flag those rows.

## Step 7 — Group into product areas

Group each part by **product area as a tester navigates it**, not by service directory. Order areas by how much changed — the biggest new surface leads.

Derive the areas from the product itself rather than a list held here, which would go stale and would publish the roadmap into the source tree:

- the app's own navigation — the route segments under the frontend's page tree, and the labels in its sidebar component
- area labels on the tickets themselves, where the board uses them

Invent an area when the sprint calls for one. Mark an area `(entirely new module)` when nothing of it existed before. Keep the names to what a tester would recognise from the UI.

## Step 8 — Output

Print the **full** report in chat as Markdown, every row with its links exactly as in the file. Never shorten the chat copy to a summary table or a bare list of ticket numbers: the user copies the list from chat as well. **Also save the same Markdown to a file** outside the repo — `new-features-<sprint>.md` in the user's Downloads folder, or the scratchpad when there is none — and give the path.

The file is what goes into Google Docs. Copying the rendered report out of a dark-themed editor carries the dark background and code-chip styling into the doc. Tell the user the two clean routes: upload the `.md` file to Drive and open it with Google Docs, or copy the file's raw text and use Edit → Paste from Markdown (enable Markdown under Tools → Preferences once).

**Lists, not tables.** Every feature is a checkbox line. The report is pasted into a shared QA doc where wide tables wrap and become unreadable, and a tester ticks rows off as they go. **No Before/After lines** — the user asked for the feature list only.

Default template, one combined list (Step 6):

````markdown
# New Features — <sprints>

**Sprints scanned:** <iteration> (DD–DD Mon) — [board view](url)
**Source:** sprint tickets on the board, read DD Mon YYYY

**Sprint size:** N tickets in the sprint; M feature rows (K more tickets folded into a parent row). Every ticket is listed whatever its board status.

Bug fixes, UI polish, refactors, CI work and dependency bumps excluded.

---

### <Product area> — N

- [ ] **<Feature stated as capability, not as a ticket title>** — [#NNNNN](url) · @<assignee>, @<assignee> · PRs [#PR](url), [#PR](url) — *folds [#NNNNN](url), [#NNNNN](url)*
- [ ] <Feature> — [#NNNNN](url) · @<assignee> · no PR linked

---

## Notes for your QA plan

<3–6 numbered notes — see Step 9.>
````

Split template, only when the user asks for it (Step 6):

````markdown
# New Features — <sprints>

**Sprints scanned:** <iteration> (DD–DD Mon) + <iteration> (DD–DD Mon) — [board view](url)
**Source:** sprint tickets on the board, read DD Mon YYYY

**Sprint size:**
- N tickets in the sprint; M feature rows (K tickets folded into a parent row)
- N ready to test · N not in QA yet
- By status: <Status — count · Status — count · …>

Bug fixes, UI polish, refactors and dependency bumps excluded.

---

## Part 1 — Ready to test

### <Product area> — N

- [ ] **<Feature stated as capability, not as a ticket title>** — [#NNNNN](url) <Status> · @<assignee>, @<assignee> · PRs [#PR](url), [#PR](url)
- [ ] <…>

---

## Part 2 — In the sprint, not in QA yet

### <Product area> — N

- [ ] <Feature> — [#NNNNN](url) <Status> · @<assignee> · PR [#PR](url) open

---

## Notes for your QA plan

<3–6 numbered notes — see Step 9.>
````

Rules for the rows:

- **Every** ticket and PR number is a live Markdown link. Use the ticket's own `url` from the board, and derive PR links from it by swapping `/issues/<n>` for `/pull/<n>`. Never plain text: the user pastes this into a shared doc and needs the links to survive.
- Write the capability, not the raw ticket title. Strip the issue-template prefixes and conventional-commit scopes off titles.
- Put the ticket's assignees on every row as `@login`, exactly as GitHub returns them — do not expand logins into real names. Write *unassigned* when there are none. When sub-issues are folded into a parent row, use `assignees_rollup`, the union across the parent and its in-sprint children.
- In the split report only, write the Status as plain text — no backticks and no emoji. Inline code turns into a coloured chip in Google Docs, and the board's status emoji paste as stray icons.
- PRs are references, not the subject. When a ticket carries more than about four, cite the count and the first and last rather than all of them. When it carries none, write *no PR linked*.
- Bold the one or two rows in each area that are the real headline.
- Add a short italic aside only when a tester would otherwise waste time — *"ships disabled"*, *"backend only, no UI surface"*, *"only verifiable on a fresh install"*, *"marked QA but no merged PR"*, *"part of epic #NNNNN"*.
- Drop any part or area that is empty rather than printing an empty heading.

Keep the language plain and neutral. This gets pasted into a shared QA doc and read by people who were not in the conversation.

## Step 9 — The QA notes

This section is what makes the list worth more than a board export. Look for:

- **Status that the evidence does not support.** A ticket in a QA column with no merged PR, or a closed ticket still sitting in an early column. Lead with these — the tester will otherwise test something that is not there. In the default combined list, say instead which rows have no PR linked and so may not be on any environment yet.
- **Restructures.** A renamed tab or a reorganised page stales every existing test case and e2e locator for that area. Say so explicitly.
- **The riskiest single item.** Usually a rewrite under a shared path — a planner, a client wrapper, an auth layer. Name it and say why. A ticket reopened inside the sprint belongs here too.
- **Features that cannot be verified on an existing environment.** Install-time provisioning, first-boot credential adoption, migration backfills.
- **Removals.** A ticket that deletes a tab, page or agent is not a feature, but a tester must know.
- **Epics split across sprints.** A feature whose sub-issues sit partly in this sprint and partly in another (`children_elsewhere`, or a parent outside the sprint) is only partly testable — say which half.
- **Excluded-but-notable work.** The one line on automation and tooling tickets from Step 5.

## Working notes

- Put every intermediate file in the session scratchpad directory, never in the repo. `board.json` in particular is a full dump of a private board — it must not land in the working tree.
- This is a read-only reporting skill. It does not commit, push, comment on tickets, edit the working tree, or move anything on the board.
- Report the counts you actually derived — sprint tickets, feature tickets, and how many folded into a parent row — rather than quietly reconciling the numbers.
- This skill does not look at git. It cannot tell you which environment a change is on, only what the board says. If the user needs "what actually landed on test or prod", say that is a git-range question outside this skill and answer it directly with `git log <target-before>..<source-head>`.
