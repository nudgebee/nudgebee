---
name: ship
description: Intent-anchored ship pipeline — capture intent, then run challenge → validate → verify → code-review → docs-gap → create-pr, then watch CI and triage review comments, as gated stages that pause only on findings needing a human decision. A nudgebee-native take on the "no-mistakes" pipeline.
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
  - Task
  - Skill
---

# Ship

Orchestrate the existing nudgebee skills into one intent-anchored pipeline that takes a
change from committed-on-a-branch to PR-ready. This is a thin driver — it does **not**
re-implement service detection, validation, review, or PR formatting. It invokes the
skills that already own those (`/challenge`, `/validate`, `/code-review`,
`/create-pr`, `/pr-comments`) and adds the two ideas worth borrowing from the `no-mistakes`
tool: an explicit **intent** that every stage is judged against, and a **gate taxonomy** so
the agent only stops the user for decisions that are genuinely theirs.

This is **opt-in and skill-only** — it blocks nothing on its own. There is no git proxy and
no push hook; a manual `git push` still works. The pipeline's authority ends at "PR opened."

Optional argument: `$ARGUMENTS` — the target base branch (defaults to `main`).

## Step 0: Preconditions

```bash
git branch --show-current
git status --short
git log --oneline -5
```

- If on `main`/`test`/`prod`, stop — ship runs from a feature branch. Tell the user to branch first.
- If there are **uncommitted** changes, stop and tell the user to commit them (suggest `/commit`). Ship reviews committed work; it does not commit for the user.
- If the branch has no commits ahead of base, stop — nothing to ship.

## Step 1: Capture intent (mandatory)

Intent is the anchor the whole pipeline is judged against — it is **not** a diff summary.
A good intent states: what the user set out to accomplish, the key decisions/tradeoffs
made, and anything deliberately left out of scope.

- If the user passed intent in the conversation or as free text, use it.
- Otherwise, draft a 2–4 sentence intent from the commits and diff, then **show it to the
  user and ask them to confirm or correct it** before proceeding. Do not silently invent it —
  a wrong intent poisons every downstream stage.

Hold the confirmed intent in context; pass it into the review stages below.

## Step 2: Gate model

Every finding surfaced by any stage below is classified into exactly one bucket:

- **auto-fix** — mechanical, low-risk, unambiguous (formatting, a lint autofix, an orphaned
  import your change created, an obviously-correct one-line correction). The agent applies it,
  notes it, and continues. No pause.
- **ask-user** — anything requiring judgment the user owns: a correctness concern, an API/schema/
  cross-service change, a design tradeoff, scope creep, or a finding that contradicts the stated
  intent. **Stop and escalate.** Never auto-approve an `ask-user` finding.
- **no-op** — informational; record it in the final summary, take no action.

When unsure whether a finding is `auto-fix` or `ask-user`, treat it as `ask-user`.

## Step 3: Challenge (adversarial pass)

Invoke the `challenge` skill against the intent + diff. It emits `PROCEED` / `REVISE` / `REDESIGN`.

- `PROCEED` → continue.
- `REVISE` / `REDESIGN` → this is an **ask-user** gate. Surface the objections and stop;
  do not paper over a structural objection by proceeding.

Skip this stage only for changes the repo's AI principles say skip `/challenge` (typos,
formatting, 1-line fixes, docs-only) — state that you skipped it and why.

## Step 4: Validate (lint / format / test)

Invoke the `validate` skill (no argument — let it detect affected services from the diff).

- All pass → continue.
- Format/lint failures that are purely mechanical → **auto-fix**: invoke `fix-lint`, re-run
  `validate` once, continue if green.
- Test failures or non-mechanical lint errors → **ask-user** gate. Report the failing output
  verbatim and stop. Do not "fix" a failing test by weakening it.

## Step 5: Verify (evidence, not just green)

Green tests prove nothing broke; they do not prove the change *does what the intent says*.
If the diff has runtime surface (product code, not docs/tests/config-only), exercise the
affected flow end-to-end and observe its behavior against the Step 1 intent: run the
service locally (or the relevant e2e test) and drive the changed path with a real
request/event, capturing what was exercised and what was observed as evidence. Per
`CLAUDE.md → Definition of Done`, behavior must be observed, not assumed.

- Behavior matches intent → continue, and record the evidence (what was exercised, what was
  observed) for the final report and PR body.
- Behavior does not match intent, or the flow can't be exercised and the change is
  non-trivial → **ask-user** gate. Do not proceed on unverified behavior.
- Diff has no runtime surface (docs/tests/config-only) → skip, and say so. Never fabricate
  evidence for a change you did not actually drive.

## Step 6: Code review

Invoke the `code-review` skill on the diff. Route each finding through the Step 2 gate model:
apply `auto-fix` findings, collect `ask-user` findings, record `no-op` findings. If any
`ask-user` findings exist, stop and present them before opening a PR.

Cross-check every finding against the **intent** from Step 1 — a change that review flags as
unexplained but the intent justifies is a `no-op`; a change the intent does *not* justify is
scope creep and an `ask-user` finding.

## Step 7: Docs-gap check

A code change often obsoletes or requires a doc. Map the changed paths to their likely doc
targets and check each for a gap:

| Changed | Likely doc target |
|---|---|
| `app/src/lib/actions.yaml` (new/renamed action) | naming vs [`docs/rpc-action-naming.md`](../../../docs/rpc-action-naming.md) |
| `api-server/migrations/**` | [`api-server/migrations/README.md`](../../../api-server/migrations/README.md) |
| A service's public behavior / setup | that service's `CLAUDE.md` |
| User-facing feature or install flow | `nudgebee-docs/` (if present in the workspace) |
| Shared type / API contract / cross-service change | [`docs/architecture-decisions.md`](../../../docs/architecture-decisions.md) entry |

Route findings through the Step 2 gate model:

- Obvious, mechanical doc update (a renamed action's doc line, a new migration's README note)
  → **auto-fix**: update the doc, include it in the change.
- A judgment call (does this behavior change warrant a new architecture-decision entry? is
  this the right doc home?) → **ask-user**.
- No doc affected → **no-op**. Do not invent docs for changes that don't need them; this repo's
  principles are surgical-changes and simplicity-first.

## Step 8: Open the PR

Only reached when the prior steps left no open `ask-user` gate. Invoke the `create-pr` skill with
the target base branch (`$ARGUMENTS`, default `main`). It already handles the issue-link
requirement, the template, self-review, and validation — do not duplicate that here.

Seed the PR's intent/summary from the Step 1 intent so the human reviewer starts from the same
anchor the pipeline used.

Capture the PR number/URL from `create-pr` for the next step.

## Step 9: Post-PR checks (CI + review comments)

Once the PR exists, watch it settle instead of declaring victory at "PR opened."

**CI status** — watch the checks for this PR **as a background task** (never a foreground
sleep/poll loop; keep working while it runs):

```bash
gh pr checks <pr-number> --watch    # run in the background; read the final matrix when it reports
gh pr checks <pr-number>            # final state, one line per check
```

Route the outcome through the Step 2 gate model:

- All checks green → continue.
- A check fails on something mechanical the pipeline can reproduce locally (lint/format that
  slipped, a flaky-looking build step) → **auto-fix**: reproduce with `/validate`, fix, push
  the follow-up commit, and re-watch once. Do not loop indefinitely — one auto-fix attempt,
  then escalate.
- A genuine test/build failure, or a failure you cannot reproduce or explain → **ask-user**
  gate. Report the failing check name and its log tail verbatim, and stop.

Do not merge — merging stays with the human reviewer and the repo's `main → test → prod`
automation.

**Review comments** — fetch any human/bot review feedback already on the PR:

```bash
gh pr view <pr-number> --json reviews,comments,reviewDecision
```

If there are actionable review comments, invoke the `pr-comments` skill to enumerate and
triage them. Apply `auto-fix`-class comments (obvious, unambiguous corrections) and push a
follow-up commit; collect judgment-class comments as `ask-user` items to present to the user.
If the PR is fresh and has no reviews yet, note that and move on — don't wait for a reviewer.

## Step 10: Final report

Emit a compact summary:

- **Intent** — the confirmed one-liner.
- **Stages** — challenge verdict, validate result, verify evidence, review + docs result
  (counts per gate bucket).
- **Auto-fixed** — bullet list of what the agent changed on its own.
- **Escalated** — any `ask-user` gate that stopped the run (if it stopped early).
- **CI** — final check status, and any follow-up commit pushed to fix it.
- **Review comments** — count triaged, auto-fixed vs. escalated.
- **Outcome** — PR URL, plus current state (checks green / checks failing / awaiting review).

## Notes

- Ship never pushes or opens a PR past an unresolved `ask-user` gate.
- Ship watches CI and triages existing review comments, but **does not auto-merge** — merge
  stays with the human reviewer and the repo's `main → test → prod` automation.
- CI auto-fix is capped at one attempt per failing run; then it escalates. No retry storms.
- If you find yourself re-implementing service detection, validation commands, or PR
  formatting here, stop — that logic lives in `validate` / `create-pr` and must stay there.
