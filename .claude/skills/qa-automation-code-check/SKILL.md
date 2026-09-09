---
description: Pre-commit gate for Playwright automation code in app-e2e-tests. Checks the QA's changed spec/locator/helper files against seven mandatory parameters — testid/role-first locators with a safe fallback, single-line comments only, secrets from .env, required tags, structure and reuse (no waitForTimeout, no swallowed assertions, titles that state the journey and the expected result), proven run evidence, and the OSS-push decision — then returns a COMMIT READY or BLOCKED verdict with the exact gaps to close. Also use it on its own to write or audit test titles, or to replace flaky waits.
user-invocable: true
allowed-tools:
  - Bash
  - Read
  - Write
  - Edit
  - Glob
  - Grep
  - AskUserQuestion
---

# QA Automation Code Check

**This is a gate, not a linter.** A QA has finished writing automation code (usually with Claude's help) and wants to know whether it is fit to commit. Run every parameter, then return one verdict:

- **✅ COMMIT READY** — all seven parameters PASS. Tell the QA to run `/commit`.
- **❌ BLOCKED** — one or more FAIL. List the exact gaps, with `file:line` and the fix, and tell the QA not to commit yet.

Never commit or push from this skill. Never soften a FAIL into a warning to reach a green verdict.

**Language: English only.** This file, every question this skill asks the QA, every verdict line, every gap description, and every comment or test title it writes into code are written in plain English — never Hindi or Hinglish. This holds even when the QA is chatting in Hinglish.

---

## Step 0: Scope — only what the QA changed

The gate judges **the QA's own work**, not the legacy suite. The existing suite carries hundreds of pre-existing violations; scoping to the diff is what keeps the gate honest and actionable.

Run from the **repo root** — `git status --porcelain` prints repo-root-relative paths regardless of cwd, so resolving them from inside `app-e2e-tests/` gives "No such file or directory" on every one. `-u` is required too: without it git collapses a new folder into a single `?? path/` line and every new spec inside it disappears from the target set.

```bash
cd "$(git rev-parse --show-toplevel)"
T=$(
  { git status --porcelain -u -- app-e2e-tests | awk '{print $2}'
    git diff --name-only -- app-e2e-tests
    git diff --name-only --cached -- app-e2e-tests
    git diff --name-only main...HEAD -- app-e2e-tests
  } | grep '\.ts$' | sort -u
)
echo "$T"
```

Target set = `$T`, plus anything the QA named in `$ARGUMENTS`.

- If `$ARGUMENTS` names a path, use that path (a file or a folder) instead of the diff.
- If the target set is empty, say so and stop — there is nothing to gate.
- **Untracked files count.** A brand-new spec is exactly what this gate exists for.

Read every target file in full before judging it, plus its sibling `*Locators.ts` / `*Helper.ts` and [`tests/GlobalLocators.ts`](../../../app-e2e-tests/tests/GlobalLocators.ts). The greps below are triage; the verdict comes from reading the code.

A pre-existing violation on a line the QA did not touch is **not** a FAIL — report it separately as "pre-existing, out of scope".

---

## Step 1: Run the seven parameters

Each parameter returns PASS or FAIL. Collect `file:line` evidence for every FAIL.

### P1 — Locators: test-contract primary, safe fallback behind `.or()`

Every element locator is declared in a `*Locators.ts` class (never inline in the spec) and written with a primary **and** a fallback behind `.or()`, so one attribute changing in the app degrades to a slower match instead of a red suite.

```ts
this.saveBtn = page.getByTestId("save-btn").or(page.getByRole("button", { name: "Save" })).first();
```

Fallback ladder — pick the highest rung that is unambiguous:

| Priority | Form | Use when |
|---|---|---|
| 1 | `page.getByTestId("...")` | a `data-testid` exists — it is a test contract, put there for us, and survives styling and markup refactors |
| 2 | `getByRole(role, { name })` | the element has an accessible name — user-facing, so it changes only when the UX does |
| 3 | `page.locator("#the-id")` | no testid and no accessible name, but the app renders a stable id |
| 4 | `getByPlaceholder` / `getByText` / `getByLabel`, **scoped to the parent container** | no role/name, but unique text inside a known box |
| 5 | attribute CSS scoped to the container (`container.locator('[href="/automation"]')`) | last resort |

A raw CSS `id` is an implementation detail — a developer can rename it during an unrelated refactor without ever knowing a test depended on it. Prefer the two rungs above it whenever they exist.

**Where the app renders no `data-testid` and the element has no accessible name, an id primary with a structural fallback is correct, not a violation.** Several areas are still like this — `TaskRunner.tsx`, for instance, renders four ids and zero testids. Check before assuming:

```bash
grep -c 'data-testid' app/src/components/<area>/<Component>.tsx
```

If it returns 0, use rung 3 and say so in a `//` line. If it returns more than 0, using an id as the primary is a FAIL.

FAIL when:

- a locator uses a raw CSS `#id` as its primary while the same component renders a `data-testid`, or the element has an accessible name `getByRole` could reach;
- any locator has no `.or()` fallback **and** no `//` line above it explaining why a wider match would be unsafe;
- a locator is written inline in the spec instead of in the locators class;
- the fallback is **not scoped to the same container** as the id — `.or()` resolves in *document order*, so an unscoped fallback silently returns the first matching element on the page. Writes then land in the wrong field and the run fails on a parameter the test believed it filled. See the `fieldControl` note in [`taskRunnerLocators.ts`](../../../app-e2e-tests/tests/workflow/TaskRunner/taskRunnerLocators.ts);
- a dotted/templated id uses `#` CSS form — `#task-runner-task-k8s.cli-btn` is parsed as a class chain. Must be `page.locator('[id="task-runner-task-k8s.cli-btn"]')`;
- the fallback is an index chain (`nth-child`, positional xpath). `.locator("xpath=..")` from a labelled anchor is fine;
- a multi-candidate locator does not end in `.first()`;
- MUI menu items use `getByRole("menuitem", { name })` — matches **zero** in this app (nested spans + `aria-hidden`). Must be `[role="menuitem"]#edit:visible` with a `hasText` fallback.

**No fallback is better than a wrong fallback.** A deliberate id-only locator with a one-line reason is a PASS.

### P2 — Comments: single-line only, only where needed

- `//` only. **`/* */` and `/** */` JSDoc blocks are a FAIL anywhere in the target files.**
- One line per comment. If it does not fit on one line it is almost certainly explaining *what* the code does — delete it or shorten it.
- Comment the **why** — a gotcha, a non-obvious wait, a value that cannot be pinned. Step narration (`// click the save button` above a click) is a FAIL.
- Commented-out test code is a FAIL. Delete it, or `test.skip()` with a one-line reason.

### P3 — Secrets and environment data come from `.env`

- **Hardcoded** credentials, tokens, webhook URLs, base URLs, tenant/cluster/account names, user emails, or project keys are a FAIL. `.env` / `.env.dev` are gitignored; a hardcoded secret leaks the moment the repo is shared — and this repo has an OSS path (P7).
- Read via `process.env` and fail fast with a message that names the key:

```ts
const CLUSTER = process.env.CLUSTER || "";
if (!CLUSTER) throw new Error("CLUSTER is not set — add it to .env / .env.dev");
```

- `console.log` of a secret value is a FAIL. Log the key name or a masked form.
- A new key is only PASS when all four are done:
  1. added to local `.env` **and** `.env.dev`,
  2. added to the env table in [`app-e2e-tests/README.md`](../../../app-e2e-tests/README.md),
  3. the QA has been told to update the **`E2E_DEV_ENV`** GitHub secret — CI rebuilds `.env.dev` from it, so a key that exists only locally fails CI with "undefined env var",
  4. multi-line values (service-account JSON, PEM) stored **base64-encoded** — the line-by-line `$GITHUB_ENV` export truncates them.

#### Identity leakage in run logs

A hardcoded secret is not the only thing that escapes. A `console.log` of an **env value** escapes too, and it is easier to miss because the value never appears in the source. CI runs Playwright with the configured reporter — no `--reporter` override, no output suppression — so every `console.log` lands in the GitHub Actions job log. `SlackReporter` only alerts on `failed` / `timedOut` / `interrupted`, so a leak on a **green** run is never surfaced to anyone. On an `@oss` file (P7) those lines travel to the public repo with the test.

FAIL when a log prints the **value** of anything identifying a person, tenant, or repository owner — `GITHUB_ASSIGNEE`, `*_USERNAME`, `*_EMAIL`, `LDAP_USERNAME`, `USER_n_EMAIL` — or a composite containing one. Log which key or which branch was used instead:

| Leaks | Write instead |
|---|---|
| `Selected assignee: ${assignee}` | `"Selected assignee from GITHUB_ASSIGNEE"` |
| `Fell back to "${picked}"` | `"GITHUB_ASSIGNEE not assignable - used a repo-offered assignee"` |
| `Project Key: ${projectKey}` | mask the owner segment, log `***/<repo>` |

**Composite values are the trap.** `GITHUB_PROJECT_KEY` is `<owner>/<repo>` and the owner is a real username, so de-identifying the assignee log alone still leaves that name printed one line earlier. Sweep every log in the flow, not only the line you changed.

Both checks below are required.

**Static** — read every hit; a `mask*()` wrapper around the value is the fix, not a violation:

```bash
grep -nE 'console[.]log[(].*[$][{]' $T
grep -nEi 'console[.]log[(].*[$][{][^}]*(assignee|username|user|email|owner|project_?key)' $T
```

**Empirical** — the one that actually proves it. Run the spec, then search the output for the literal identity values in the env files:

```bash
cd app-e2e-tests
npm run test:dev -- <spec> --workers=1 > run.log 2>&1

IDENT='^(GITHUB_ASSIGNEE|GITHUB_USERNAME|JIRA_USERNAME|GITLAB_USERNAME|PAGER_DUTY_EMAIL|ZENDUTY_EMAIL|CONFLUENCE_USER_NAME|LDAP_USERNAME|USER_[0-9]+_EMAIL)='
PAT=$(grep -hE "$IDENT" .env .env.dev | cut -d= -f2- | grep -v '^$' | sort -u | paste -sd'|' -)
grep -nE "$PAT" run.log && echo "LEAK" || echo "clean"
```

Any hit is a FAIL. Run it for **both branches** of a conditional flow — a fallback path logs different lines than the happy path, and it is usually the fallback that names the person. Confirm the check can actually fire before trusting a clean result: point it at a file containing a known identity and check it reports `LEAK`.

**What this does not cover:** screenshots, videos and traces upload as CI artifacts on failure and show the names visually. Log masking does not touch them. If that matters for an OSS-bound spec, raise it rather than assuming this check closed the hole.

A non-secret fixture constant (`HASH_INPUT = "nudgebee-task-runner"`, a pinned sha256 digest, a crypto test vector) is **not** a secret. Pinned expected values must stay in the file — moving them to `.env` would make the assertion unverifiable. Do not FAIL these.

### P4 — Tags on every test

Playwright's tag argument, second position:

```ts
test(
  "Task Runner - run crypto.hash and verify the sha256 digest",
  { tag: ["@test", "@sanity", "@functional"] },
  async ({ page }) => { ... }
);
```

Every test carries **at least one tag from each of the first three axes**. Axis 4 comes from P7.

| Axis | Tags | Rule |
|---|---|---|
| **Environment** | `@dev`, `@test` | which env the case is proven on. Both if it runs on either. Add `@env` when the case *only* validates environment/config wiring. |
| **Suite depth** | `@sanity`, `@smoke`, `@regression` | `@sanity` = must pass before anything else ships; `@smoke` = core happy path; `@regression` = full-depth coverage. Exactly one. |
| **Type** | `@functional`, `@negative`, `@validation`, `@crud`, `@search`, `@rbac`, `@snackbar` | one or more. Reuse the repo's existing tags; a coined synonym is a FAIL. |
| **Distribution** | `@oss` | added **only** when P7 comes back yes. |

A missing axis, or a tag outside this table that the tester did not confirm, is a FAIL.

Then **ask the tester** whether any additional tags are needed for this batch — feature tag, ticket tag, `@flaky`, `@slow`. Use `AskUserQuestion` with the axis-3 list plus an "Other" path. Asking is mandatory even when the three axes are already satisfied; do not invent a tag the tester did not confirm.

Existing inventory — check before coining anything new:

```bash
grep -rho '"@[a-zA-Z0-9_-]*"' app-e2e-tests/tests --include=*.ts | sort | uniq -c | sort -rn
```

Run by tag: `npx playwright test --grep "@sanity"` · exclude: `--grep-invert "@slow"`.

### P5 — Structure and reuse

- Locators → `*Locators.ts` (extend `CommonLocators`). Flows → `*Helper.ts`. Assertions → the spec.
- A locator re-declared when `CommonLocators` or the sibling locators class already has it is a FAIL.
- Test title states the action **and** the expected result — see **Test titles** below. A title failing that section is a P5 FAIL.
- Every spec sets its own `test.setTimeout(...)`.
- [`app-e2e-tests/README.md`](../../../app-e2e-tests/README.md) is updated when the change adds something a future contributor cannot infer from the specs: a new env variable, a new fixture or data-generation step, a new tag, or a new folder with its own conventions. A change that only adds cases to an existing pattern needs no README edit — say so rather than padding it.
- Assertions check the outcome, not just a toast. A success snackbar alone is not proof the record persisted — that is a FAIL.
- A test that asserts nothing, or whose only assertion is `toBeVisible()` on something that was already visible before the action, is a FAIL.
- Chained flows (one task's output feeding the next) must re-read state after the switch. Asserting against a previous step's still-on-screen result is a stale-assert FAIL.

#### Waits: no `waitForTimeout`

`page.waitForTimeout(n)` is a FAIL anywhere in the target files. A fixed sleep is a guess: too short and the suite is flaky on a slow runner, too long and every run pays for it forever. Wait for the condition the sleep was standing in for.

| Sleeping for | Wait on instead |
|---|---|
| a field to commit its value | `await expect(input).toHaveValue(value)` |
| a filtered list to re-render | `await expect(rows.first().or(emptyState)).toBeVisible()` |
| an accordion or drawer to open | `await expect(summary).toHaveAttribute("aria-expanded", "true")` |
| a request to land | `waitForGraphQLAndValidate(...)`, or `page.waitForResponse` |
| a row to disappear after delete | `await expect(row).toHaveCount(0)` |

Playwright's `expect` already retries until its timeout, so the assertion *is* the wait — an explicit sleep before it adds nothing but delay. If a sleep genuinely cannot be replaced, it needs a `//` line naming the condition that has no observable signal.

#### Swallowed assertions

`.catch(() => false)`, `.catch(() => "")` and `.catch(() => {})` are legitimate for a **probe** — a check whose negative answer is a normal, expected branch. They are a FAIL when they sit on something the test is actually asserting, because a missing element then reads as an ordinary value mismatch, or passes silently.

```ts
// FAIL — a panel that never rendered becomes an ordinary status mismatch
const status = ((await locators.statusChip.textContent().catch(() => "")) || "").trim();

// PASS — the wait is the assertion, the read is direct
await expect(locators.statusChip).toBeVisible({ timeout: 15000 });
const status = ((await locators.statusChip.textContent()) ?? "").trim();
```

```ts
// PASS — a category that does not hold the row is the normal case, so absence
// must come back as false rather than throw
async function rowAppears(row: Locator, timeoutMs: number): Promise<boolean> {
  return row.waitFor({ state: "visible", timeout: timeoutMs }).then(() => true).catch(() => false);
}
```

Rules: every surviving `.catch` carries a `//` line saying **why absence is an expected outcome**; a swallowed `expect(...)` whose result is never read is a FAIL (either assert it or turn it into a boolean probe you act on); and `.catch` never wraps the assertion a test exists to make.

#### Test titles

A failing test reaches the team as one line in Slack — no code, no trace. If the title does not say **what journey ran** and **what should have been true**, the first thing anyone does is open the file, and the alert has failed at its job.

```
<Feature> - <step>, <step>, <step>, verify <expected result>
```

Real examples from `tests/workflow/TaskRunner/`:

```
Task Runner - select account, select Crypto Hash task, enter data, pick md5 algorithm, run task, verify the digest
Task Runner - select account, select Network TCP task, enter host and port, run task, verify the port is reachable
Task Runner - run task, edit command, re-run task, verify the second result replaces the first
```

Read one aloud: it is the manual test case, followable in the UI by someone who has never seen the code.

**1. Open with the feature, then ` - `.** This groups the suite and tells a reader which part of the product broke. Specs exercising the page's own structure — loading, search, empty states, form reset — use `<Feature> sanity - `.

**2. Steps in the order they happen, in the user's words.** `select account`, `enter data`, `pick GET method`. Not `setTaskField`, not `#task-runner-box`, not `clicks the button with role=button` — helpers and locators are implementation and change without the journey changing.

**3. End with `verify <the actual expected result>`.** This is the half most titles get wrong.

| Weak | Strong |
|---|---|
| `verify the output` | `verify the digest` |
| `verify no error` | `verify the port is reachable` |
| `verify the result` | `verify only matching items are kept` |
| `verify it fails` | `verify the key length error` |
| `verify the response` | `verify 200 status_code` |

If the test asserts more than one thing, name them: `validate triggerTask API, verify output`.

**4. Name the subject under test, never the machinery.** `k8s.cli`, `core.switch` and `triggerTask` are the product's own vocabulary and belong in a title. A CSS id, a helper function or a fixture filename does not.

**5. Parameterised tests interpolate the parameter**, or the report shows the same line N times and you cannot tell which case failed.

```typescript
for (const algorithm of inputs.hashAlgorithms) {
  test(`Task Runner - ... pick ${algorithm} algorithm, run task, verify the digest`, ...);
}
```

**Negative cases** name the invalid input *and* the expected rejection, so three similar failure tests never blur into one: `enter data that is not valid base64 … verify the decode error` · `enter a valid ciphertext with the wrong key … verify the run fails` · `enter a base64 key of the wrong length … verify the key length error`.

**Chained flows** say so explicitly — the chaining is the point: `run Crypto Encode on a unicode string, feed the output to Crypto Decode, verify the original string comes back`.

Rewrites:

| Before | After | Why |
|---|---|---|
| `Add User Groups` | `User Groups - open Admin, add a group with a name and role, save, verify the group appears in the listing` | Stated neither journey nor result |
| `Check hash task` | `Task Runner - select account, select Crypto Hash task, enter data, pick sha256 algorithm, run task, verify the digest` | "Check" is not an outcome |
| `Test that decrypt fails` | `Task Runner - … enter a valid ciphertext with the wrong key, run task, verify the run fails` | Which failure? On what input? |
| `verify readRunOutputValue returns data` | `Task Runner - … pick base64 algorithm, run task, verify the encoded value` | Named the helper, not the behaviour |

FAIL a title when any of these is true: no ` - ` feature prefix · no outcome verb, so it stops at the actions · the outcome is generic (`works`, `is successful`, `no error`, `the result`, `the response`) · it contains a locator, CSS id, helper or fixture name · it is a bare noun phrase (`Add X`, `Delete Y`, `Check Z`) · it is loop-generated with no interpolated parameter.

Titles are cheap to change with no behaviour risk — but re-run the spec afterwards if any test is selected by name (`-g`) in CI or a workflow input.

### P6 — Run evidence

Code that has never been executed does not pass this gate.

```bash
cd app-e2e-tests
npx tsc --noEmit                                              # types
npx playwright test <spec> --project=chromium --workers=1     # .env  → test env
npm run test:dev -- <spec> --workers=1                        # .env.dev → dev env
```

- Run it against **every environment the test is tagged for** in P4. `@dev` and `@test` both tagged → both runs.
- `--workers=1` is required for workflow specs — parallel workers fight over shared tenant state.
- Do **not** pass `--reporter=list`: it replaces the reporter array and disables the custom `SlackReporter`, so failures post no Slack alert.
- FAIL if `tsc --noEmit` errors, if any targeted test fails, or if the QA cannot show a run.
- A flaky pass (green only on retry) is a FAIL — report it as flake to fix, not as a pass.

Paste the real command and output into the report. Reasoning about correctness is not evidence.

### P7 — OSS decision (ask every time)

Ask the QA Automation Engineer / developer explicitly, every run, even for a one-line edit:

> "Do these test changes need to be pushed to the OSS repository or not?"

Use `AskUserQuestion` with **Yes — push to OSS** / **No — internal only**. Never assume, never carry forward a previous answer.

**If YES:**
- Add `@oss` to the `tag` array of **every** test in the affected file(s).
- Re-run the P3 secrets pass on those files — OSS-bound code gets a second look for tenant names, internal URLs, and customer-identifying strings.
- Run the P3 *Identity leakage in run logs* check as well. Hardcoded-string greps pass clean while a logged env value still ships: a green run publishes its job log, and an `@oss` spec carries those lines to the public repo.

**If NO:**
- Add this as the **first line of the file**, above the imports:

```ts
// Not for OSS
```

- Do not add `@oss` to any test in the file.

**Either way**, the PR body must carry an explicit line, which you hand to `/create-pr`:

```
OSS: yes — tagged @oss
```
```
OSS: no — marked "Not for OSS" in <file>
```

P7 FAILs if the question was not asked and answered, or if the answer is not reflected in the file yet.

---

## Step 2: Triage greps

Fast signal before reading. Every hit needs confirmation by reading the code — these produce false positives.

`$T` is already set by Step 0, and these run from the repo root too.

```bash
# P2 — block comments; must return nothing
grep -n '/\*' $T

# P1 — id locators with no .or() fallback on the same line
grep -n 'locator("#' $T | grep -v '\.or('

# P4 — files where the test count and the tag count disagree
for f in $T; do
  t=$(grep -c '^\s*test(' "$f"); g=$(grep -c 'tag: \[' "$f")
  [ "$t" -ne "$g" ] && echo "$f: $t tests, $g tagged"
done

# P3 — identity values reaching the run log (see "Identity leakage in run logs")
grep -nEi 'console[.]log[(].*[$][{][^}]*(assignee|username|user|email|owner|project_?key)' $T

# P3 — hardcoded secret-ish literals
grep -niE '(password|passwd|token|secret|api[_-]?key|webhook)\s*[:=]\s*"' $T
grep -n '"https\?://' $T | grep -v localhost

# P4 — coined tag synonyms
grep -ho '"@[a-zA-Z0-9_-]*"' $T | sort -u

# P5 — hardcoded sleeps; must return nothing
grep -n 'waitForTimeout' $T

# P5 — swallowed results; each hit needs a `//` reason above it
grep -n 'catch(() =>' $T

# P5 — every title, to read against the Test titles section
grep -hoE 'test\(\s*[`"][^`"]+[`"]' $T | sed -E 's/^test\(\s*[`"]//'

# P5 — titles with no outcome verb, or a generic one
grep -hoE 'test\(\s*[`"][^`"]+[`"]' $T | grep -viE 'verify|assert|expect'
grep -hoE 'test\(\s*[`"][^`"]+[`"]' $T | grep -iE 'verify (it works|success|the result|the response|no error)'
```

Note: `grep -E` has no lookahead in this environment — `(?!localhost)` silently matches nothing. Use the piped `grep -v` form above.

---

## Step 3: Verdict

Report in exactly this shape.

**Scorecard** — one row per parameter, no parameter omitted:

| # | Parameter | Verdict | Evidence |
|---|---|---|---|
| P1 | Locators — testid/role primary + safe fallback | ✅ / ❌ | `file:line` |
| P2 | Comments — single-line only | ✅ / ❌ | |
| P3 | Secrets + identities from `.env`, none logged | ✅ / ❌ | |
| P4 | Tags — env + depth + type | ✅ / ❌ | |
| P5 | Structure, waits, assertions, titles | ✅ / ❌ | |
| P6 | Run evidence | ✅ / ❌ | command + result |
| P7 | OSS decision recorded | ✅ / ❌ | `@oss` / `// Not for OSS` |

**If every row is ✅:**

> ✅ **COMMIT READY** — all seven parameters pass. You can run `/commit` now.
> PR body line: `OSS: <yes/no> — <how it was recorded>`

**If any row is ❌:**

> ❌ **BLOCKED — do not commit yet.** `<n>` gaps to close:

Then a numbered gap list. Each entry:

1. **Which parameter** failed and at **`file:line`**.
2. **What is wrong**, in one sentence.
3. **The concrete fix** — the actual replacement line where one exists.
4. **Why it matters** — one line, so the QA does not hit the same class again.

Offer to apply the fixes. Do not apply them silently; the QA owns the code. After fixes, re-run the failed parameters (and P6 again if any code changed) before flipping the verdict.

Close with **pre-existing, out of scope** — violations you saw in untouched lines. Informational only; they never affect the verdict.

---

## Authoring mode

If the QA asks for a **new** spec rather than a check, write it to the same seven parameters — locators first (P1), env-driven data (P3), tags (P4), outcome assertions (P5) — then run Step 1's gate on your own output before handing it back. The gate applies to Claude-written code exactly as it does to hand-written code.
