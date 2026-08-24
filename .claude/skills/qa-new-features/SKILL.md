---
description: Build a QA-facing new-feature list by reconciling a promotion PR (main to test, or test to prod) against one or more sprints. Ask the user for the GitHub PR URL and the project board view URL every time, resolve the merge commit's parents, pull every PR in the range, pull every ticket in those sprints, match tickets to PRs, and print a ticket-first feature checklist plus two reconciliation lists — what shipped without a ticket in those sprints, and what is in the sprints but did not ship. Invoke ONLY when the user explicitly calls this skill by name or types /qa-new-features. Never start it on your own from a mention of a deploy, a merge PR link, or a question about what shipped.
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

**Invocation-only.** Run this skill exclusively when the user calls it — `/qa-new-features`, or by naming it. Do not start it because a deploy came up in conversation, because someone pasted a merge PR link, or because a question sounds like it wants a feature list. Answer that question directly instead, and offer the skill if it fits. Once invoked, the skill drives to completion on its own.

Turn a promotion PR (`chore: Merge main into test`, `chore: Merge test into prod`) **plus one or more sprints** into a QA-ready feature checklist.

The skill takes **two** inputs and reconciles them:

- **the deployment PR** — what actually shipped, derived from git
- **the sprint board view** — what the team said it was building

The headline list is **ticket-first**: one row per sprint ticket that shipped in this drop, with its PRs attached. Tickets are better consolidators than commit titles — a nine-PR programme is one ticket, and no heuristic guessing is needed. The two reconciliation lists that follow are where the value is: work that shipped with **no ticket in those sprints**, and tickets in the sprints that **did not ship**.

The output is for a **tester**, not a release manager. Every row must be something a person can go and exercise. Bug fixes, UI tweaks, refactors, CI work and dependency bumps are deliberately excluded — the QA team tracks those separately.

## Nothing is hardcoded

No organisation, repository, project number, view number, iteration name, sprint length or board Status value appears anywhere in this skill. Every one of them is resolved at runtime — the repo from the git remote, the rest from the two URLs the user pastes. This keeps the skill portable across forks and mirrors, and keeps the private board's contents out of a file that ships with the source.

The same rule applies to what you *write*. The report goes into a shared QA doc, so quote ticket and PR titles as they are, but never paste board internals the user did not ask for — customer-account labels, assignee names, priority or story-point fields. The board query below deliberately does not request them.

## Step 1 — Get both links, every time

**Ask for the board link on every run, even when this conversation already used one.** Board views, filters and sprint boundaries move between drops, and a prod run is rarely checked against the same sprint as the test run that preceded it. Reusing a link from earlier in the conversation silently reports against the wrong sprint. Ask, and stop until they answer:

> Two things:
> 1. The GitHub PR link for the deployment — the `chore: Merge main into test` or `chore: Merge test into prod` PR.
> 2. The project board **view** URL for the sprint you want this checked against — `https://github.com/orgs/<org>/projects/<N>/views/<V>`.

For the PR: accept a full URL, a bare `#12345`, or a number. Extract the number.

If the user gives a PR that is **not** a promotion merge (it carries its own code changes rather than merging one branch into another), say so, and ask whether they want the feature list for the promotion PR that shipped it instead.

Resolve the repo and the board coordinates — never type them in:

```bash
OWNER=$(gh repo view --json owner --jq '.owner.login')
REPO=$(gh repo view --json name --jq '.name')
```

Do **not** join those in a jq expression. `--jq '.owner.login+"/"+.name'` returns mangled output under Git Bash — MSYS rewrites the bare `"/"` string literal into a Windows path. Keep them as two variables and join them in the shell.

Parse the board URL into `ORG`, `PROJ` and `VIEW`:

```bash
SPRINT_URL="<pasted URL>"
ORG=$(echo "$SPRINT_URL"  | sed -nE 's#.*/orgs/([^/]+)/projects/.*#\1#p')
PROJ=$(echo "$SPRINT_URL" | sed -nE 's#.*/projects/([0-9]+).*#\1#p')
VIEW=$(echo "$SPRINT_URL" | sed -nE 's#.*/views/([0-9]+).*#\1#p')
```

A user-scoped board (`/users/<login>/projects/<N>`) uses the same query with `user(login:)` in place of `organization(login:)`. If the URL is neither — a milestone, an issue-search link, a doc — say the sprint lives on a GitHub Projects board and ask for that view's URL.

## Step 2 — Resolve the sprints from the view

One query returns the view's own filter and the full iteration calendar:

```bash
gh api graphql -f query='
query($org:String!,$num:Int!,$view:Int!){
  organization(login:$org){ projectV2(number:$num){
    title
    view(number:$view){ number name filter }
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

**Several sprints are normal.** A drop usually spans two or three, and the user will often name them (*"178 and 179"*). Everything downstream takes `SPRINT` as a comma-separated list.

Echo the resolved names and dates back to the user before going further — Step 7 costs about eighty seconds, and running it against the wrong sprints wastes all of it.

## Step 3 — Resolve the range

A promotion PR's merge commit has **two parents**. Parent 1 is the target branch before the merge; parent 2 is the source branch head. Everything new is `parent1..parent2`.

```bash
gh pr view <NUM> --json number,title,state,baseRefName,headRefName,createdAt,mergedAt,mergeCommit \
  --jq '{number,title,state,base:.baseRefName,head:.headRefName,createdAt,mergedAt,merge:.mergeCommit.oid}'

git fetch origin --quiet
git log -1 --format='%P%n%ci%n%s' <MERGE_SHA>
```

Set `B` = first parent, `A` = second parent.

**Then descend, because `A` is often itself a merge.** Some promotion branches are cut from the *target* and then have the source merged into them, so the branch head is a merge whose first parent is the target. Taking it as-is yields a range containing exactly one commit and **zero PRs**. Walk down the source side until the head is a real branch head:

```bash
H=$A
while :; do
  PP=$(git log -1 --format='%P' $H); P1=$(echo $PP|cut -d' ' -f1); P2=$(echo $PP|cut -d' ' -f2)
  if [ -n "$P2" ] && git merge-base --is-ancestor $P1 $B 2>/dev/null; then H=$P2; else break; fi
done
echo "source head: $H"
```

`H` is the real source head from here on. Confirm `B` matches the target branch's state before the merge before going further.

### Gotchas that will bite you

- **Never `git rev-parse origin/<branch>`** for a branch that also exists locally. The name resolves ambiguously and the command dies with `fatal: Needed a single revision`. Use `git ls-remote origin <branch>` or the full `refs/remotes/origin/<branch>`.
- **`jq` is not installed on this machine** (exit 127). Use `gh api --jq` / `gh pr view --jq`, or Python. Never pipe to a bare `jq`.
- **Git Bash rewrites `/`-leading strings into Windows paths.** It breaks jq expressions that join with `"/"`, and any argument that looks like an absolute POSIX path. Build such strings in the shell, not inside `--jq`.
- **`comm` needs lexically sorted input.** Files written with `sort -un` are numerically sorted and `comm` silently produces nonsense on them. Compare number sets in Python.
- **Nested heredocs break in Git Bash.** Keep scripts flat, and write long files with the Write tool rather than a heredoc.

## Step 4 — Collect the range facts

Set `SC` to this session's scratchpad directory first — every later step writes intermediates there, and none of them belong in the repo:

```bash
SC="<your session scratchpad path>"   # from the Scratchpad Directory in your environment
mkdir -p "$SC"
echo "$B $H" > "$SC/range.txt"
```

```bash
git diff --stat $B $H | tail -1
git log --oneline --no-merges $B..$H | wc -l
git log --format='%ci' $B..$H | tail -1   # oldest
git log --format='%ci' $B..$H | head -1   # newest

# The cut date, in UTC — Step 8 compares it against GitHub mergedAt, which is always UTC.
# Plain %ci / %cI carry the local offset and misclassify by that offset.
CUT=$(TZ=UTC git log --date=iso-strict-local --format='%cd' $B..$H | head -1)

# New DB migrations — always report these, they gate the deploy
git diff --diff-filter=A --name-only $B $H -- api-server/migrations/migrations/app \
  | grep 'up.sql' | sed 's#.*/##'
```

Also worth a look when the drop is large — these feed the QA risk notes:

```bash
# New services / pages / modules
git diff --diff-filter=A --name-only $B $H | awk -F/ '{print $1"/"$2}' | sort -u
# Deleted code (retired agents, removed tabs) — often the biggest regression risk
git diff --diff-filter=D --name-only $B $H
# Deploy surface
git diff --name-only $B $H -- deploy/
```

## Step 5 — Enumerate every PR in the range

Two rules, both learned by getting a wrong answer:

**Match both merge shapes.** Squash-merged PRs carry their number as a `(#NNNN)` suffix rather than a `Merge pull request #N` subject. Grepping only the latter loses roughly half of a `main` drop.

**Scan the full log, not `--first-parent`.** A `main → test` drop is fine either way, but on a `test → prod` drop `--first-parent` collapses to almost nothing: the target branch never receives PRs directly, only promotion merges, so the PRs sit one level deeper. Measured across four real drops:

| Drop | `--first-parent` | full log |
|---|---|---|
| main → test | 231 | 237 |
| main → test | 219 | 221 |
| test → prod | 54 | 620 |
| test → prod | 18 | 256 |

The full log is a strict superset — it loses nothing and adds real PRs the first-parent walk never sees.

```bash
git log --format='%s' $B..$H > "$SC/all.txt"
grep -oE '(Merge pull request #[0-9]+|\(#[0-9]+\)$)' "$SC/all.txt" \
  | grep -oE '[0-9]+' | sort -un > "$SC/prs.txt"
wc -l < "$SC/prs.txt"

# Sanity check on the FIRST-PARENT log only — a direct-to-branch commit shows up here.
# Running this over the full log just prints every feature-branch commit; it is noise.
git log --first-parent --format='%s' $B..$H \
  | grep -vE '(Merge pull request #[0-9]+|\(#[0-9]+\)$)'
```

Also capture what was already on the target branch before this drop. Step 8 uses it to tell "shipped in an earlier promotion" apart from "not shipped yet", which is most of Part 3 on a `test → prod` run:

```bash
git log --format='%s' $B \
  | grep -oE '(Merge pull request #[0-9]+|\(#[0-9]+\)$)' \
  | grep -oE '[0-9]+' | sort -un > "$SC/base_prs.txt"
```

## Step 6 — Fetch PR metadata in bulk

One GraphQL request per ~55 PRs using aliases. Looping `gh pr view` two hundred times is far too slow.

`headRefName` and `bodyText` are not decoration — Step 8 matches on scoped branch names and on `Part of #N` bodies, and `closingIssuesReferences` alone misses both.

```bash
rm -f "$SC"/chunk_*
split -l 55 "$SC/prs.txt" "$SC/chunk_"
for c in "$SC"/chunk_[a-z][a-z]; do
  { echo 'query($owner:String!,$repo:String!) { repository(owner:$owner, name:$repo) {'
    while read -r n; do
      echo "  p$n: pullRequest(number: $n) { number title headRefName bodyText mergedAt labels(first:10){nodes{name}} closingIssuesReferences(first:8){nodes{number title}} }"
    done < "$c"
    echo '} }'; } > "$c.gql"
  gh api graphql -f query="$(cat "$c.gql")" -f owner="$OWNER" -f repo="$REPO" > "$c.json" 2>"$c.err"
done
```

**Redirect stderr to its own file, never `2>&1`.** `gh` prints a plain-text summary of any GraphQL error to stderr, and folding that into the response file appends text after the closing brace — every later `json.load` then dies with `Extra data`.

**`NOT_FOUND` errors are expected and harmless.** The `(#NNNN)` pattern occasionally catches a number that is an issue rather than a PR. GraphQL returns partial data with a `NOT_FOUND` entry per bad alias and a `null` for that field, which the flattener already skips. Check that nothing *else* went wrong:

```bash
PYTHONIOENCODING=utf-8 python -c "
import json,io,glob
ok=0; nf=[]; other=0
for fn in sorted(glob.glob(r'$SC/chunk_*.json')):
    d=json.load(io.open(fn,encoding='utf-8'))
    ok+=len([k for k,v in ((d.get('data') or {}).get('repository') or {}).items() if v])
    for e in d.get('errors',[]):
        if e.get('type')=='NOT_FOUND': nf.append(e['path'][-1])
        else: other+=1
print('PRs resolved:',ok,'| not real PRs:',nf,'| other errors:',other)
"
```

Only `other errors: 0` matters. If it is not zero, read the matching `.err` file.

## Step 7 — Pull the sprint board

ProjectV2 has **no server-side filter on a field**, so the whole board comes down and the iterations are filtered client-side. On a busy board that is thousands of items and **about eighty seconds**. Start it in the background and do Steps 3–6 while it runs; reuse `board.json` for the rest of the session.

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
          lpr: fieldValueByName(name: "Linked pull requests") { ... on ProjectV2ItemFieldPullRequestValue { pullRequests(first:10){nodes{number mergedAt}} } }
          content { ... on Issue {
              number title url state
              labels(first:15){nodes{name}}
              parent { number title }
              closedByPullRequestsReferences(first:15, includeClosedPrs:true){nodes{number title mergedAt}}
          } }
        }
      }
    }
  }
}' -f org="$ORG" -F num="$PROJ" > "$SC/board.json"
```

This asks for the four fields the reconciliation needs and nothing else. Do not widen it to assignees, priority, story points or account labels — none of them affect the output, and all of them are board internals that would then have to be kept out of the report by hand.

Notes on the query, each one learned the hard way:

- `gh api graphql --paginate` emits **one JSON document per page**, concatenated. `json.load` chokes. Parse with `JSONDecoder().raw_decode` in a loop — the script in Step 8 does.
- `closedByPullRequestsReferences(includeClosedPrs: true)` is the issue-side reverse of `closingIssuesReferences` and is the strongest ticket→PR signal available. Without `includeClosedPrs` you lose merged PRs, which is all of them.
- `parent` gives sub-issue rollup — an epic's PRs hang off its children, not off the epic.
- A board carries draft and PR items with no issue `content`. Skip them rather than crashing.
- The board is live. Tickets move between iterations between runs, so a sprint's ticket count will not match what an earlier run reported. That is the board changing, not a bug.

## Step 8 — Match tickets to PRs

Five signals, all unioned. Any one of them is enough to bind a PR to a ticket:

1. **`closingIssuesReferences`** on the PR — a `Fixes #N` that GitHub parsed.
2. **`closedByPullRequestsReferences`** on the ticket — the same link from the other side; catches links made in the UI.
3. **`Linked pull requests`** board field — manual links a tester or lead added.
4. **A scoped branch or title** — this repo writes the issue number into the commit scope and the branch (`feat(nb-1234): …`, `fix/nb-1234-short-slug`). Catches PRs whose author never wrote a `Fixes` line. Read the repo's own convention from `.github/semantic.yml` and recent history rather than assuming this prefix.
5. **`Part of #N` / `Related to #N`** in the PR body — GitHub does not parse these into `closingIssuesReferences`, and multi-PR programmes use them constantly.

Plus **sub-issue rollup**: a ticket also owns every PR bound to its children.

Write this to `$SC/recon.py` with the Write tool, then run it. Do not try to inline it as `python -c`.

```python
import json, io, glob, re, sys, collections

SC, SPRINTS, CUT, SCOPE = sys.argv[1], sys.argv[2].split(','), sys.argv[3], sys.argv[4]

def load_multi(fn):
    s = io.open(fn, encoding='utf-8').read()
    dec = json.JSONDecoder(); i = 0; out = []
    while i < len(s):
        while i < len(s) and s[i].isspace(): i += 1
        if i >= len(s): break
        o, i = dec.raw_decode(s, i); out.append(o)
    return out

prs = {}
for fn in glob.glob(SC + '/chunk_*.json'):
    d = json.load(io.open(fn, encoding='utf-8'))
    for k, v in ((d.get('data') or {}).get('repository') or {}).items():
        if v: prs[v['number']] = v

try:
    shipped = set(int(l) for l in io.open(SC + '/base_prs.txt') if l.strip())
except IOError:
    shipped = set()

scope_re = re.compile(r'\b' + SCOPE + r'[-_ ]?(\d{3,7})\b', re.I)
link_re = re.compile(r'\b(?:fixes|closes|resolves|part of|related to)\s+#(\d{3,7})\b', re.I)

def refs(p):
    r = set(i['number'] for i in p['closingIssuesReferences']['nodes'])
    hay = (p['title'] or '') + ' ' + (p['headRefName'] or '')
    for m in scope_re.finditer(hay): r.add(int(m.group(1)))
    for m in link_re.finditer(p['bodyText'] or ''): r.add(int(m.group(1)))
    return r

pr_issues = {n: refs(p) for n, p in prs.items()}

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
        num=c['number'], title=c['title'], url=c['url'],
        labels=[l['name'] for l in c['labels']['nodes']],
        parent=(c.get('parent') or {}).get('number'),
        pr_all={p['number'] for p in cb} | {p['number'] for p in lp},
        pr_merged={p['number']: p.get('mergedAt') for p in cb + lp if p.get('mergedAt')},
        iter=(it.get('iter') or {}).get('title'),
        status=(it.get('status') or {}).get('name'))

sprint = {n: t for n, t in board.items() if t['iter'] in SPRINTS}
if not sprint:
    sys.exit('no board items carry an iteration in %r' % SPRINTS)

kids = collections.defaultdict(list)
for n, t in board.items():
    if t['parent']: kids[t['parent']].append(n)
i2p = collections.defaultdict(set)
for n, r in pr_issues.items():
    for i in r: i2p[i].add(n)

def prs_for(t):
    h = set(t['pr_all']) | i2p.get(t['num'], set())
    for c in kids.get(t['num'], []):
        h |= board[c]['pr_all'] | i2p.get(c, set())
    return h

def merged_for(t):
    h = dict(t['pr_merged'])
    for c in kids.get(t['num'], []): h.update(board[c]['pr_merged'])
    return h

R = set(prs)
out = {'sprints': SPRINTS, 'A': [], 'C_already': [], 'C_pending': [], 'C_nopr': [], 'B': []}
matched = set()
for n, t in sorted(sprint.items()):
    inr = sorted(prs_for(t) & R)
    row = dict(num=n, title=t['title'], url=t['url'], status=t['status'],
               labels=t['labels'], iter=t['iter'])
    if inr:
        matched |= set(inr)
        row['prs'] = [dict(n=p, title=prs[p]['title'],
                           labels=[l['name'] for l in prs[p]['labels']['nodes']]) for p in inr]
        out['A'].append(row)
        continue
    mg = merged_for(t)
    if not mg:
        out['C_nopr'].append(row)
        continue
    row['elsewhere'] = sorted(mg)
    row['not_on_target'] = sorted(set(mg) - shipped)
    row['after_cut'] = sorted(p for p, d in mg.items() if d and d > CUT)
    if shipped and not row['not_on_target']:
        out['C_already'].append(row)
    else:
        out['C_pending'].append(row)

for n in sorted(R - matched):
    p = prs[n]
    on = [board[i] for i in pr_issues[n] if i in board]
    out['B'].append(dict(n=n, title=p['title'],
                         labels=[l['name'] for l in p['labels']['nodes']],
                         tickets=[dict(num=o['num'], iter=o['iter'], title=o['title'],
                                       status=o['status']) for o in on],
                         other_issues=sorted(pr_issues[n] - set(board))))

io.open(SC + '/recon.json', 'w', encoding='utf-8').write(json.dumps(out, ensure_ascii=False, indent=1))
print('sprints %s | tickets %d' % ('+'.join(SPRINTS), len(sprint)))
print('  Part 1  shipped in this drop      : %d tickets covering %d of %d PRs' % (len(out['A']), len(matched), len(R)))
print('  Part 2  PRs with no sprint ticket : %d' % len(out['B']))
print('  Part 3  already on target earlier : %d | still pending: %d | no merged PR: %d'
      % (len(out['C_already']), len(out['C_pending']), len(out['C_nopr'])))
```

```bash
PYTHONIOENCODING=utf-8 python "$SC/recon.py" "$SC" "$SPRINT" "$CUT" nb
```

The second argument is the comma-separated sprint list from Step 2. The fourth is the repo's issue-scope prefix from signal 4. `CUT` is the UTC cut date from Step 4 — it separates "merged to the source branch after this drop was cut, so it arrives in the next one" from "merged long ago and never promoted". Pass it in UTC or the split is wrong by the local offset.

Read `recon.json`. **It is input to your judgement, not the report.** The buckets are mechanical; the feature call is yours.

## Step 9 — The three buckets

`recon.json` gives five arrays. Turn them into three parts of the report.

### Part 1 — `A`: sprint tickets that shipped in this drop

The headline feature list. Filter to features (Step 10), group into areas (Step 11). One row per **ticket**, listing all its PRs. A ticket already consolidates its programme of work, so do not re-consolidate across tickets unless two tickets are plainly the same feature.

Carry the ticket's **Status** onto the row, verbatim as the board spells it. A tester reads status to know whether the ticket is even expecting them yet.

### Part 2 — `B`: shipped in this drop, no ticket in these sprints

The list that finds gaps. **A drop spans several sprints**, so this bucket is always large — on a 253-PR drop it was 126 PRs. Never print it raw. Split it three ways using the fields the script already resolved:

- **2a — ticket exists, different sprint** (`tickets` non-empty). Show the other iteration and its status on each row. Expect *later* iterations here as well as earlier ones: on a prod drop this is work that reached prod ahead of its own sprint's QA cycle, which is worth saying out loud. Lead with a one-line count per iteration.
- **2b — issue referenced, but not on the board** (`other_issues` non-empty, `tickets` empty). A real tracking gap: work was done against an issue nobody put in a sprint.
- **2c — no issue reference at all** (both empty). The blind spot. Read every one of these titles; a feature genuinely hides here.

Then apply the feature filter (Step 10) to all three — most of the bucket is fixes and chores and must not reach the report. Collapse a repeated mechanical programme into one row (a dozen `chore(e2e): add <area> coverage` PRs is one line, not a dozen), and mark a row that is only a cherry-pick of something already in Part 1.

### Part 3 — `C_already` / `C_pending` / `C_nopr`: in these sprints, not in this drop

So the tester does not go looking for something that is not deployed yet.

- **`C_already`** — every merged PR on the ticket was already on the target branch before this drop. On a `test → prod` run this is the overwhelming majority (128 of 134 on one real drop) and it means *shipped in an earlier promotion*, not *pending*. Report it as a single count. Listing these as "not deployed" is actively misleading.
- **`C_pending`** — the ticket has merged PRs that are **not** on the target branch. This is the real list. `after_cut` narrows it further: those landed after this drop was cut, so they arrive in the next one.
- **`C_nopr`** — no merged PR at all. Not built yet. Give the count and list only the feature-labelled ones; a large number here usually means board hygiene, not testing work.

## Step 10 — Decide what counts as a feature

Applies to both ticket rows (Part 1, Part 3) and PR rows (Part 2). The label is the starting point, not the answer. Read every title.

**Include** — new capability a tester can exercise:

- Tickets and PRs whose label or conventional-commit type marks them as a feature or enhancement
- Titles marked with the repo's feature-request or epic convention — these frequently carry no label at all, so read the templates in `.github/ISSUE_TEMPLATE/` for the prefixes actually in use
- New services, pages, tabs, panels, integrations, providers, agents
- New DB tables that back a user-visible surface
- Capability that shipped under a non-feature type — a forward-port bundling upstream PRs, a `chore` that first wires a service into Helm/CI, a `fix` that adds a whole auth mode. Catch these by scanning non-feature titles for `add`, `support`, `introduce`, `enable`, `new`.

**Exclude:**

- Anything labelled or titled as a bug
- `fix`, `refactor`, `perf`, `style`, `test`, `ci`, `build`, `docs` PRs
- Dependabot and all dependency bumps
- `chore: Merge …` and backmerge commits
- Tickets whose board Status marks them invalid or won't-fix
- Internal engine tuning with no observable surface — unless it sits under everything, in which case a planner or client-wrapper rewrite does earn a row

Test-automation and developer-tooling work is a judgement call: it is real capability, but a tester cannot exercise it in the product. Keep it out of Part 1 and note it once at the end instead.

**Check what was removed.** A deleted tab or page is not a feature, but a tester must know. Report it in the notes.

## Step 11 — Group into product areas

Group Part 1 by **product area as a tester navigates it**, not by service directory. Order areas by how much changed — the biggest new surface leads.

Derive the areas from the product itself rather than a list held here, which would go stale and would publish the roadmap into the source tree:

- the app's own navigation — the route segments under the frontend's page tree, and the labels in its sidebar component
- the top-level service directories the diff touched, mapped to the screen a tester reaches them from

Invent an area when the drop calls for one. Mark an area `(entirely new module)` when nothing of it existed before. Keep the names to what a tester would recognise from the UI.

## Step 12 — Output

Print the report in chat as Markdown.

**Lists, not tables.** Every feature is a checkbox line; every reconciliation entry is a bullet. The report is pasted into a shared QA doc where wide tables wrap and become unreadable, and a tester ticks rows off as they go. The only table in the output is the header's drop-size summary, and even that reads better as a short bullet list. **No Before/After lines** — the user asked for the feature list only.

````markdown
# New Features on `<branch>`

**Deployment:** PR [#NNNNN](url) — `<source>` → `<target>`, merged **DD Mon YYYY, HH:MM**
**Sprints scanned:** <iteration> (DD–DD Mon) + <iteration> (DD–DD Mon) — [board view](url)
**Comparison base:** `<B7>` (target before) vs `<H7>` (source head)

**Drop size:**
- N PRs · N files · +N / −N lines
- N new migrations (V### → V###)
- N sprint tickets scanned, N shipped in this drop, M of them features
- Work spans DD Mon – DD Mon

Bug fixes, UI polish, refactors and dependency bumps excluded.

---

## Part 1 — New features from <sprints>, live on `<branch>`

### <Product area> — N

- [ ] **<Feature stated as capability, not as a commit subject>** — ticket [#NNNNN](url) `<Status>` → [#PR](url), [#PR](url)
- [ ] <…>

---

## Part 2 — Shipped in this deploy, not in <sprints>

*N PRs total, M of them features.*

### 2a. Belongs to another sprint — N PRs, M features

Spread: <iteration — count · iteration — count · …>. <One line on what the spread means.>

- [ ] <Feature> — [#PR](url) → ticket [#NNNNN](url), <iteration> `<Status>`

### 2b. Issue exists but is on no sprint board — N PRs

<Either the feature bullets, or one line saying none of them are features.>

### 2c. No ticket at all — N PRs, M feature-shaped ⚠️

- [ ] **<Feature>** — [#PR](url) — *no ticket, no acceptance criteria*
- [ ] <Collapsed mechanical programme> — [#first](url) … [#last](url) — *automation only, no product surface*

---

## Part 3 — In <sprints>, not in this deploy

<One line: of the N sprint tickets with no PR in this drop, N were already on
`<branch>` from earlier promotions.>

Genuinely pending and feature-shaped — N tickets:

- [#NNNNN](url) <title> — `<Status>`, but PR [#N](url) is not on `<branch>`
- [#NNNNN](url) <title> — `<Status>`, merged after the cut, arrives in the next drop

*A further N sprint tickets have no merged PR at all — M of those are feature-shaped and still marked live, which is worth a board hygiene pass rather than a test pass.*

---

## Notes for your QA plan

<3–6 numbered notes — see Step 13.>
````

Rules for the rows:

- **Every** PR and issue number is a live Markdown link, built from the `$OWNER` and `$REPO` resolved in Step 1 — `https://github.com/<owner>/<repo>/pull/<n>` and `/issues/<n>`. Never plain text: the user pastes this into a shared doc and needs the links to survive.
- Write the capability, not the commit subject or the raw ticket title. A title like `feat(ui): add gauge panel type to custom dashboards` becomes *"Gauge panel type for custom dashboards"*. Strip the issue-template prefixes off ticket titles.
- When a ticket carries more than about six PRs, cite the count and the first and last rather than all of them.
- Append the migration in parentheses when one backs the feature — `(V###)`.
- Bold the one or two rows in each area that are the real headline.
- Add a short italic aside only when a tester would otherwise waste time — *"ships disabled"*, *"backend only, no UI surface"*, *"only verifiable on a fresh install"*, *"cherry-pick of the Part 1 row"*.
- Drop any Part 2 or Part 3 sub-section that is empty rather than printing an empty heading.

Keep the language plain and neutral. This gets pasted into a shared QA doc and read by people who were not in the conversation.

## Step 13 — The QA notes

This section is what makes the list worth more than a changelog. Look for:

- **Untracked features.** If Part 2c is non-empty, lead with it — features shipped with no ticket are the ones with no acceptance criteria and no test plan.
- **Restructures.** A renamed tab or a reorganised page stales every existing test case and e2e locator for that area. Say so explicitly.
- **The riskiest single item.** Usually a rewrite under a shared path — a planner, a client wrapper, an auth layer. Name it and say why. A feature whose approach was changed, reverted and re-approached inside one sprint belongs here too.
- **Features that cannot be verified on an existing environment.** Install-time provisioning, first-boot credential adoption, migration backfills.
- **Migrations that delete or deactivate data.** Call these out by number — existing records will look different after deploy and a tester needs to know that is intended.
- **Removals.** Something present on the other branch but deleted here.
- **Deleted code.** Retired agents and deleted prompt files are silent regression risk with no UI to look at — say how to verify them instead (a chat question and the tool trace, not an agent row).
- **Un-backmerged work.** Run `git log --no-merges <lower>..<upper>` for commits that exist only on the higher environment and owe a backmerge.

## If asked to compare two drops

Same procedure for each, then diff the PR number sets **in Python** — `comm` mis-sorts numerically sorted files:

```bash
PYTHONIOENCODING=utf-8 python -c "
import io,sys
a=set(int(l) for l in io.open(sys.argv[1],encoding='utf-8') if l.strip())
b=set(int(l) for l in io.open(sys.argv[2],encoding='utf-8') if l.strip())
print('only in A:',sorted(a-b)); print('only in B:',sorted(b-a))
" "$SC/a_prs.txt" "$SC/b_prs.txt"
```

For "what is on one environment but not the next" **right now**, skip the PRs entirely and compare live heads — it is exact and takes one command:

```bash
U=$(git ls-remote origin <upper> | cut -f1); L=$(git ls-remote origin <lower> | cut -f1)
git log --format='%h | %an | %ci | %s' $U..$L
git log --format='%h | %an | %ci | %s' $L..$U
```

## Working notes

- Put every intermediate file in the session scratchpad directory, never in the repo. `board.json` in particular is a full dump of a private board — it must not land in the working tree.
- This is a read-only reporting skill. It does not commit, push, comment on PRs, edit the working tree, or move anything on the board.
- Report the counts you actually derived. Part 1 + Part 2 will not equal the PR count and are not meant to — say how many PRs collapsed into how many features rather than quietly reconciling the numbers.
- The matching is deliberately generous: five signals unioned, so a PR binds to a ticket on any one of them. It over-matches rather than under-matches, because a missed link lands in Part 2c and looks like a governance failure that is not real. When a Part 1 row's PRs look unrelated to the ticket, check whether a body `Related to #N` pulled it in, and say so rather than deleting the row silently.
