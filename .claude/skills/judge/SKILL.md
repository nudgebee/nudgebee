---
name: judge
description: Pre-commit cross-check — spawn an isolated, fresh-context critic to review the working diff for correctness, grounded in a real build/test run. The fast per-commit gate that feeds fewer issues into /code-review and /ship.
user-invocable: true
allowed-tools:
  - Task
  - Agent
  - Bash
  - Read
  - Glob
  - Grep
---

<!--
  Single-source-of-truth skill: this file is canonical under .claude/skills/judge/.
  .gemini/skills/judge is a directory symlink to this directory (create it with:
  ln -s ../../.claude/skills/judge .gemini/skills/judge). Both agents parse the YAML
  frontmatter — Claude reads `user-invocable` + `allowed-tools`, Gemini reads `name`.
  Do NOT copy this skill into .gemini/skills/ — use the symlink.
-->

# Judge the Diff (Pre-Commit Cross-Check)

Run an **independent** correctness check on the current working diff **before committing**. The critic runs in a *fresh context* — it did not write the code, so it evaluates the artifact, not the reasoning that produced it. This is self-review's blind spot removed.

The failure mode we defend against: committing code that looks right to the author (who is attached to it) but is wrong.

Relationship to your other gates: `/judge` is the **fast, per-commit** gate (1 critic, correctness only, in Phase 1). `/code-review` stays the **deep, per-PR** gate (all lenses). `/challenge` is the **pre-plan** gate. Don't collapse them — different granularity, same philosophy.

## When to Use

**Use before committing** any non-trivial code change — especially:
- logic changes in Go/Python/TS service code
- anything touching error paths, boundaries, concurrency, or shared state
- a change you're about to commit without having run the build yourself

**Skip for:**
- docs-only / comment-only / formatting-only diffs
- generated files, lockfiles, config value tweaks
- a diff you've already fully built and tested this turn

If the diff is trivial by the above, say so in one line and stop — do not spawn a critic for a typo.

## Step 0: Scope the diff

Run `git diff --stat` (working tree; unstaged + staged). Decide:
- **Trivial?** → report "skip — trivial diff (<reason>)" and stop.
- **Non-trivial?** → proceed.

Do not paste the whole diff here — the critic will read it itself in its own context. Keeping the diff out of this context is the point (that's the token saving).

## Step 1: Spawn the correctness critic (isolated)

Use the **Task/Agent tool** with `subagent_type: correctness-critic`. Give it a one-line scope prompt, e.g.:

> Review the current working diff for correctness. Build the affected module(s), run the changed packages' tests, read the changed lines for logic defects, and report grounded findings only. Working tree diff (not the branch). The task this change claims to do: `<one line — what the user asked for>`.

Pass the *claimed intent* so the critic can check "does it actually do that," not just "does it compile." Let the critic run its own build — do not run it here and hand results in; its independence is the value.

**Fallback (Gemini / no subagent support):** if the Task/Agent tool is unavailable, perform the `correctness-critic` procedure inline (build the affected module, run targeted tests, read the changed lines, ground every finding in evidence). Weaker — same context that may have written the code — but better than nothing. Note in the output that it ran in-loop, not isolated.

## Step 2: Relay the verdict

Present the critic's report to the user as-is (it's already formatted). Then:

- **CLEAN** → say so plainly. Safe to commit.
- **ISSUES-FOUND** → list the findings. **Do not auto-fix and do not commit.** Surface them and let the user decide. If they ask you to fix, fix on the current state (no revert) and re-run `/judge`.
- **UNVERIFIED** → the critic couldn't build (env gap). Say so honestly — this is *not* a pass. Report what blocked it.

## Anti-Patterns (Do Not Do These)

- **Do not run the build yourself and pass results to the critic.** That defeats the isolation — the critic must reach its own verdict from its own run.
- **Do not treat "no findings" as a reason to skip evidence.** A CLEAN verdict must still be backed by a real build/test run, or it's UNVERIFIED.
- **Do not auto-commit after CLEAN.** `/judge` reports; the human commits. It is a gate, not a pipeline step.
- **Do not spawn the critic for a trivial diff.** The Step 0 gate exists so this stays lean.
- **Do not widen scope.** Correctness of the diff only. Style, simplification, and cross-service architecture come later (Phase 3 critics / `/code-review`), not here.
