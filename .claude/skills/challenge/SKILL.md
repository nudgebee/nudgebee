---
name: challenge
description: Adversarial pre-implementation pass — argue against a proposed plan before writing code, and emit a binding PROCEED / REVISE / REDESIGN verdict
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Glob
  - Grep
---

<!--
  Single-source-of-truth skill: this file is canonical under .claude/skills/challenge/.
  .gemini/skills/challenge is a directory symlink to this directory. Both agents parse
  the YAML frontmatter above — Claude reads `user-invocable` + `allowed-tools`, Gemini
  reads `name`, and both tolerate the extra fields. If you edit this file, both agents
  pick up the change automatically. Do NOT copy this skill into .gemini/skills/ — use
  the symlink.
-->

# Challenge the Plan (Adversarial Pre-Implementation)

Run an adversarial pass against a proposed plan **before** any code is written. This catches wrong-direction work before it accumulates sunk cost.

The failure mode we are defending against: the AI writing exactly what was asked for — when what was asked for was wrong.

Takes a plan description via `$ARGUMENTS`. If `$ARGUMENTS` is empty, challenge the most recently discussed plan in the current session (or the current branch's diff if there is already code).

## When to Use

**Use aggressively for:**
- New features touching shared types, API contracts, or DB schema
- Cross-service changes
- New library / framework / pattern adoption
- Anything architectural — anything that would be expensive to undo in three months
- Hasura actions, metadata, migrations (per `CLAUDE.md → Hasura Migrations & Metadata`)

**Skip for:**
- Typo / formatting / 1-line bug fixes
- Docs-only changes
- Purely internal refactors with no interface change

When in doubt, run it. The cost is five minutes; the cost of building the wrong thing is much higher.

## Step 0: Confirm There Is a Concrete Plan

Before challenging, confirm the plan is **concrete enough to attack**:

- Is the approach stated in one paragraph?
- Are the affected files / services / contracts named?
- Is the success criterion named?

If any of those are missing, **stop and ask the user to sharpen the plan first.** An ambiguous plan cannot be meaningfully challenged — you will end up arguing with straw men.

## Step 1: Restate the Plan

In one paragraph, restate the proposed approach as precisely as possible. Include: the problem it solves, the approach, the affected services, and how you'd know it worked.

## Step 2: Find Three Strongest Counterarguments

Produce **exactly three** independent reasons this plan is wrong. Not nitpicks — structural objections. Each counterargument must include:

1. **The objection** (one sentence)
2. **The concrete risk** it creates — what breaks, and when
3. **The cost of ignoring it** — cheap to fix later? or permanent?

**Bar for a valid counterargument:**

- "This might be slightly slower" — **not** a counterargument.
- "This forces a second round-trip on every request, which will 3x the P95 latency on the hot path used by the dashboard's overview page" — **yes**, this is a counterargument.

- "This is a bit complex" — **not** a counterargument.
- "This couples the collector-server directly to the ticket-server schema, so any ticket schema change will require a coordinated cross-service deploy" — **yes**, this is a counterargument.

If you cannot find three, you either (a) don't understand the plan well enough, or (b) the plan is actually fine for a trivial change — in which case, skip this skill.

## Step 3: Future-Self Review

Answer these three questions plainly:

1. **Six-month senior-engineer review:** What would a senior engineer reviewing this diff six months from now criticize first? Be specific — name the pattern or smell.
2. **Optimization trade-off:** What does this plan optimize for, and what does it sacrifice? Every design trades something.
3. **Simpler alternative:** Is there a simpler approach that achieves ~80% of the benefit with ~20% of the complexity? If yes, state it. If the answer is "yes but we still want the full approach", explain **why** — that reason becomes part of the decision record.

## Step 4: Verdict (Binding)

End with exactly one of these three verdicts. **The verdict is binding.** Implementation MUST NOT start on anything but `PROCEED`.

- **`PROCEED`** — The objections are real but the plan is still the right call. Note which objections to actively mitigate during implementation.
- **`REVISE`** — The plan mostly holds but needs specific changes before implementation. List the exact required changes. Re-run `/challenge` on the revised plan if any change is material.
- **`REDESIGN`** — One or more counterarguments are fatal, or a simpler alternative dominates. Do NOT implement this plan. Return to the research/strategy phase.

## Step 5: Log the Decision

If the verdict is `PROCEED` **and** the change is architectural (affects shared contracts, schema, cross-service behavior, framework choice, or tooling), append one line to the root `CLAUDE.md` under `## AI Coding Principles → ### 4. Decisions & Lessons Learned → #### Architecture Decisions` (removing the `_No entries yet_` placeholder if this is the first entry). Format:

```
- **[YYYY-MM] {title}**: Chose {approach} over {alternative}. Why: {reason}. Counterarguments considered: {brief summary}. Reconsider if: {condition}.
```

If the verdict is `REDESIGN` **and** the plan had been seriously considered (not just a half-formed idea), append to `## Decisions & Lessons Learned → What We've Tried and Won't Try Again`:

```
- **[YYYY-MM] {title}**: Considered {approach} for {problem}. Rejected because: {concrete reason from counterarguments}. Current direction: {replacement}.
```

Do NOT log day-to-day implementation details — only decisions that affect how future work should be done.

## Output Format

Always output in this shape so the result is parseable and reviewable:

```
## Adversarial Review: {plan title}

### Restated Plan
{one paragraph}

### Counterargument 1 — {short title}
- **Objection:** ...
- **Risk:** ...
- **Cost of ignoring:** ...

### Counterargument 2 — {short title}
- **Objection:** ...
- **Risk:** ...
- **Cost of ignoring:** ...

### Counterargument 3 — {short title}
- **Objection:** ...
- **Risk:** ...
- **Cost of ignoring:** ...

### Future-Self Review
- **Senior-engineer critique (6 months out):** ...
- **Optimizes for:** ...
- **Sacrifices:** ...
- **Simpler alternative:** ... (or: "none found — full approach justified because ...")

### Verdict: {PROCEED | REVISE | REDESIGN}

{For PROCEED: which objections must be actively mitigated during implementation.}
{For REVISE: the exact required changes. Re-run /challenge if material.}
{For REDESIGN: a one-line pointer to what to explore instead.}
```

## Anti-Patterns (Do Not Do These)

- **Do not hedge.** "This might be okay but could also be risky" is not a counterargument — it's a non-answer. Commit to a position.
- **Do not invent objections.** If you cannot find three real ones for a trivial change, the right move is to skip this skill, not to manufacture filler.
- **Do not rubber-stamp.** `PROCEED` on every run means the skill is broken. If you haven't emitted `REVISE` or `REDESIGN` in a while, check whether you're being critical enough.
- **Do not start implementing after a `REVISE` or `REDESIGN`.** Stop. Surface the verdict to the user. Let them decide the next move.
