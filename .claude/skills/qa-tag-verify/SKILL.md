---
description: Verify a release-candidate (RC) tag OR a final release tag (e.g. 1.13.0) before/after it is promoted to prod, for either the on-prem (nudgebee-enterprise) or the OSS (nudgebee/nudgebee) release, in both modes - fresh install and upgrade from the previous release - so anyone on the team can run it. Always starts by asking which environment, which mode and which RC or release notes. Checks the tag (lineage), smoke-tests the install or upgrade, compares the environment before and after an upgrade to prove everything that worked still works, builds a point-wise risk checklist from the changelog and walks it one point at a time, then reviews the release notes against the tag and produces a short Slack-ready verdict. Use when someone is verifying an RC tag, asks what to check before prod tagging, shares an RC release/changelog link, or asks what is missing or wrong in an RC or its notes.
user-invocable: true
allowed-tools:
  - Bash
  - PowerShell
  - Read
  - Grep
  - Glob
  - WebFetch
  - AskUserQuestion
---

# Release Verification (RC or final)

Verify a release tag: either a release candidate (`X.Y.0-rc.N`) before it is promoted to prod, or a **final release** (`X.Y.0`, `X.Y.1` hotfix) that has been cut. Both are first-class; everywhere this skill says "RC" or "the tag", it means whichever one is being verified. Anyone can run this; it assumes nothing about who you are.

**RC vs final — what differs:**
- **Previous final** = the newest *non-RC* tag below the tag being verified (for `1.13.0` that was `1.12.1`; for `1.13.0-rc.3` also `1.12.1`). Changelog range and upgrade starting point are always `<previous final>..<tag>`.
- **A final's release notes** are the ones customers read, so the notes review (Step 6) matters most there. Earlier RC notes problems the tester posted must be re-checked against the final's notes.
- **Prod-line check:** both RC and final tags must be on `origin/prod`.
- **Expected build:** a final may be a rebuild of its last RC with hotfixes on top — do not assume the images equal the RC's; list what is actually deployed.
- **Wording:** headline uses the tag as given (`On-prem 1.13.0 (fresh install)` or `On-prem 1.13.0-rc.3 (...)`); "re-test on the next RC" becomes "re-test on the next release" when the tag is a final.

**Parity rule.** Every check in this skill applies to **both** environments (on-prem and OSS) and **both** modes (fresh install and upgrade) unless a line says otherwise. Nothing done for one environment is skipped for the other; only the repo, tag format, commands and shared-env cautions differ (see the table in Step 0).

**One point at a time.** Never batch. Never declare the checklist "sufficient" without re-reading the full changelog.

**Say how sure you are.** For every finding, state what you actually checked (commit read, PR number looked up, log line seen) and what you did not. A claim made from a PR title alone is a guess, and must be labelled as one.

**Run end to end without being told what to do next (applies to on-prem and OSS, RC and final, whoever runs it).** The person should only have to answer Step 0 and do the things only a human can. Do not wait to be asked for any of these — do them in this order and deliver one finished message:
1. Tag sanity (Step 2), release notes fetched, full changelog range read (Step 4) — all read-only, start immediately after Step 0.
2. The notes-vs-tag review (Step 6) with every item verified, **even before any install** — it needs no cluster. Do not postpone it until the install is done and do not wait for "now check the notes".
3. The install/upgrade smoke checks (Step 3) as soon as the person says the install is done; run every read-only `kubectl`/`helm` check yourself. Hand over **one list** of the exact UI clicks only a human can do (log in, Admin tab, cluster dropdown, one read-only Nubi question) in a single message — do not drip them one at a time.
4. The final Slack message in the Step 8 shape, built from what you verified. Offer it as the final draft without being asked for "the final draft", and without needing a reference post to copy the format from.
Pause **only** for live actions that need a yes (installing, restarting, upgrading a shared env, posting anywhere) and for facts you cannot get yourself (cluster/values file, a disposable namespace). Never ask "should I check the notes?", "do you want the final message?" or "which format?" — the skill already says.

## Step 0 — ALWAYS ask first: environment, mode, RC

Do this before running anything else, even if the user typed only `/qa-tag-verify`. Use `AskUserQuestion`.

1. **If the user already gave a release URL or tag**, derive what you can and say what you derived:
   - `github.com/nudgebee/nudgebee-enterprise/...` → **On-prem**
   - `github.com/nudgebee/nudgebee/...` → **OSS**
   - A pasted release link or tag **is** the answer to the environment and the RC/release question, even when it is a **final** tag (e.g. `1.13.0`, not an RC): verify that tag, do not ask Q3 again. Confirm in one line ("Verifying on-prem 1.13.0 — right?") and continue. You still need the **mode** unless the user stated it ("both" counts).
   - If you offered an RC and a newer **final** exists, say so in one line before asking anything (an RC is superseded once its final is out).
2. **Otherwise ask, in this order:**
   - **Q1 — "Which release are you verifying?"** `On-prem (nudgebee-enterprise)` / `OSS (nudgebee/nudgebee)`.
   - **Q2 — "Which mode?"** `Fresh install` / `Upgrade from the previous release` / `Both (recommended)`. For OSS, a fresh install needs a disposable namespace — ask the owner; if none is available, record "OSS fresh install: not run (no disposable namespace)" rather than silently skipping it.
   - Then list recent releases so the user can pick instead of typing:
     - On-prem: `gh release list --repo nudgebee/nudgebee-enterprise --limit 6`
     - OSS: `gh release list --repo nudgebee/nudgebee --limit 6`
   - **Q3 — "Which tag should I verify?"** Offer the 3 newest tags of **either kind** (RCs and finals, labelled "RC" / "final"). "Other" lets the user paste a tag or a release-notes URL.
3. **Previous final tag** (needed for the changelog range and for the upgrade starting point): derive it as the newest non-RC tag below the tag being verified and confirm it in one sentence.
4. Never guess the environment, mode or tag. Never start testing before all are confirmed.

### The two environments differ — do not mix them up

| | On-prem | OSS |
|---|---|---|
| Repo | `nudgebee/nudgebee-enterprise` | `nudgebee/nudgebee` (public mirror) |
| Tag format | `1.12.0-rc.1` / `1.12.0` (no `v`) | `v1.10.0-rc.3` / `v1.10.0` (with `v`) |
| Version numbers | enterprise numbering | **independent** numbering (v1.10 vs 1.12 is normal) |
| PR numbers | e.g. `#39293` | different series (e.g. `#779`) — the same change has a different number in each repo |
| Chart | `oci://registry.nudgebee.com/nudgebee` | `oci://ghcr.io/nudgebee/charts/nudgebee` |
| Images | enterprise registry | `ghcr.io/nudgebee/<service>:<version>` |
| Where it runs | tester's own cluster, **new namespace per run** | **fresh**: disposable namespace; **upgrade**: shared env `nudgebee-oss` |
| Local git | remote `origin` = enterprise | **no local clone of the OSS repo**: use `gh api` against `nudgebee/nudgebee` |

## Inputs (after Step 0)
- environment, mode, RC tag and release URL, previous final tag.

**Final release?** Its page lists everything since a previous tag, but check where the "Full Changelog" link starts (1.13.0's started at `1.12.0`, not `1.12.1`). Verify the range `<previous final>..<tag>` regardless.

**Not rc.1?** The release page for `rc.2`/`rc.3` lists only the delta from the **previous RC** (e.g. OSS v1.10.0-rc.3 lists one PR). The real changelog to verify is `<previous final>..<tag>` (OSS v1.9.0..v1.10.0-rc.3 is 365 commits). Build the checklist from that range, and mark points already verified on earlier RCs as such rather than skipping them.

## Step 1 — How to set up each mode

### On-prem
- No DB access. Verify through the product UI (chat/Nubi, settings pages), pod logs, or by reading repo code/migrations. Never direct SQL.
- **Fresh install** (new namespace): `helm upgrade nudgebee oci://registry.nudgebee.com/nudgebee -f <values>.yaml --install --namespace <ns> --version <rc> --create-namespace --timeout 20m`. Then `kubectl port-forward svc/app -n <ns> 3000:80`, log in at localhost:3000. Password: `kubectl get secret nudgebee -n <ns> -o jsonpath='{.data.NEXTAUTH_DUMMY_CREDS_PASSWORD}'`, base64-decoded. On GKE add `--server-side=false`.
- **Upgrade** (separate new namespace):
  1. Install the **previous final** first with the same values file: same command with `--version <prev final>`. Wait until healthy and log in. If the registry no longer serves that chart version, say so and record "on-prem upgrade: not run" instead of substituting a different starting version.
  2. **Capture the baseline** (Step 3C) and put some data in: connect/see the bundled cluster, create a harmless dashboard, an API token, a notification rule, upload a small KB document. Whatever the baseline records is what the upgrade must preserve.
  3. Upgrade in place with the **same values file**: `helm upgrade nudgebee oci://registry.nudgebee.com/nudgebee -f <values>.yaml --namespace <ns> --version <rc> --timeout 20m`. A values file that worked on the previous release but fails on the RC is a finding in itself.
- Use a **new namespace per run**. A recovered install is not a fresh one.

### OSS
- Env for the upgrade: the shared OSS environment (its hostname is internal and must not be written in the repo; ask the OSS owner or check the team's private notes), namespace `nudgebee-oss`, kubectl context of the dev GKE cluster that hosts it (`kubectl config get-contexts`), release `nudgebee`.
- **Upgrade is in place on the shared env**: `helm upgrade nudgebee oci://ghcr.io/nudgebee/charts/nudgebee --version <tag-without-v> -n nudgebee-oss --reuse-values --server-side=false`. Passing `-f` or `--set` wipes existing secrets/config. The release page's generic `helm install` example omits the namespace, `--reuse-values` and `--server-side=false` this env needs.
- **Check `helm list -n nudgebee-oss` first** — revisions land every few hours, so the env may already be on the RC; if so, capture the baseline from the previous revision's data you can still see and test what is running.
- **Do not upgrade the shared env without the owner's go-ahead** (Darrin and Rohit normally own OSS; others cover when unavailable) — it is a live action on a shared environment.
- **Fresh install** on a disposable namespace: `helm install nudgebee oci://ghcr.io/nudgebee/charts/nudgebee --version <tag-without-v> -n <ns> --create-namespace`, with the same cautions as on-prem.
- Limits on the shared env: the Qdrant instance has zero collections (KB/vector checks return "nothing found" by design; KB re-embedding is not exercisable), and no real customer data exists.

## Step 2 — Tag sanity (read-only)

**On-prem (git, remote `origin` = enterprise):** `git fetch --tags`, then
- `git merge-base --is-ancestor <prev> <tag>` must succeed. In PowerShell read `$LASTEXITCODE` **immediately**; it resets after the next git call.
- `git branch -r --contains <tag>` must include `origin/prod` (RC and final tags are both cut on prod's line). In PowerShell test it with `(git branch -r --contains <tag>) -match 'origin/prod$'`; the full list is huge.
- `git log --oneline <prev>..<tag> | wc -l` — note the size.

**OSS (no local clone, use the API):**
- `gh api repos/nudgebee/nudgebee/compare/<prev>...<tag> --jq '[.status,.ahead_by,.behind_by,.total_commits] | @tsv'` — expect `ahead`, `behind` = 0.
- Add `--paginate` when listing commits; a single page is capped.

Prod being **ahead of** the tag is normal (prod keeps taking work after a tag is cut). Do not flag it, and never ask for a re-cut for that reason.

## Step 3 — Smoke tests

### 3A — Checks for every run (both environments, both modes)

1. **Pods.** `kubectl get pods -n <ns>`. Everything `Running`/`Completed`. Record early restarts (on 1.12.0-rc.1 temporal restarted 4 times, llm-gateway 2 and cloud-collector 1 in the first minutes, then settled; the cause was not confirmed in the logs — look before assuming it is harmless).
2. **Everything is on the RC.** List the image tag of every pod and confirm each first-party service is on the target version — a partial upgrade leaves some services behind:
   `kubectl get pods -n <ns> -o custom-columns=NAME:.metadata.name,IMAGES:.spec.containers[*].image` (works in cmd, PowerShell and bash). Third-party images (postgres, redis, rabbitmq, temporal, qdrant) keep their own versions — only first-party services follow the RC tag.
3. **Release status.** `helm list -n <ns>` and `helm history nudgebee -n <ns>`: the new revision is `deployed`, not `failed`/`pending-upgrade`.
4. **Migration job** `postgres-migration-job-*` is `Completed`.
5. **Error scan in the services-server logs** — search for `first run`, `Error creating user role`, `local agent registration failed`, `unable to create user`, `does not exist`, `migration`:
   - PowerShell: `kubectl logs -n <ns> deploy/services-server | Select-String -Pattern "first run|Error creating user role|local agent registration failed|unable to create user|does not exist|migration"`
   - cmd: `kubectl logs -n <ns> deploy/services-server | findstr /i /c:"first run" /c:"Error creating user role" /c:"local agent registration failed" /c:"unable to create user" /c:"does not exist"`
   - bash: `... | grep -iE "first run|Error creating user role|local agent registration failed|unable to create user|does not exist|migration"`
   - `grep` does not exist in plain Windows cmd/PowerShell — never hand it to someone on Windows.
6. **Log in** and confirm in the UI: the **Admin** tab is present; the bundled cluster `nb-control-plane-k8s` is in the cluster dropdown (green); which account the app opens on after login (record it; opening on `Demo` instead of the real cluster was seen on 1.12.0-rc.1, cause not established).
7. **Not bugs:** the first admin gets **no welcome/invite email** (in the code only the invite-another-user flow sends one; whether a given install has SMTP configured was not checked). The services-server API/job split is **off by default**, so one `services-server` pod is expected.

### 3B — Fresh-install-only checks (both environments)

- A failed first-run step is only *logged*; the install still prints "first run: provisioned the first admin". Use the 3A log search and read every hit.
- **Fixed from 1.13.0 (commit `2938e0aa77`, nb-36774; absent in 1.12.x): setup now retries by itself.** On 1.13.0+ the first attempt may still hit the unfinished DB — that is expected and **not a failure**. Pass looks like: WARN `first run: setup did not complete, retrying in the background`, then `first run: setup completed on retry`, with no restart. The retry log may also show a transient `failed to attach the default LLM provider ... integrations_cloud_accounts does not exist`; confirm the provider is attached with one read-only Nubi question. Pass criteria: Admin tab present, `nb-control-plane-k8s` green and opened by default (not Demo), Nubi answers. Report a blocker only if setup never completes or the cluster stays missing. When writing the result, say "fixed — the cluster shows without a restart", never "setup still hits the unfinished database" (it reads like the bug is back).
- **Known failure on 1.12.x and earlier only: first-run setup races the migration job.** Symptom: admin looks read-only, no Admin tab, only the Demo cluster; logs show `column "tenant_id" of relation "user_roles" does not exist` and `column "access_secret_v2" of relation "cloud_accounts" does not exist`. Cause: services-server runs first-run setup at boot before the migration job finishes; the role and agent steps fail, are only logged, and are never retried. #38808 moved migrations before the *upgrade* only — fresh installs stay post-install on purpose. The first-run code has been in the repo since 1.11.0 (and is present in the OSS tag too: `api-server/services/bootstrap/provision.go`), so it is not new in a later RC, but it is not fixed either.
- **Confirm:** after the migration job is `Completed`, `kubectl rollout restart deploy/services-server -n <ns>`, wait for `1/1 Running`, re-run the log search (no errors; same tenant id, no second tenant), log out and back in. Admin tab and `nb-control-plane-k8s` appear = race confirmed. **Ask the tester before restarting** — it is a live action.
- **Verdict:** unless a fix has landed in the tag, report it as an RC blocker for fresh installs.

### 3C — Upgrade-only checks (both environments)

The point of an upgrade run: **what worked before must still work, and the data must still be there.**

**If the tester ran the upgrade herself** ("tried the upgrade and it worked"), do not repeat it. Record it as "upgrade: tester-reported, passed — not tested by me" and plan a proper baseline-and-diff run on the next RC (or next release, if the tag verified was a final). Never present it as verified by you.

1. **Baseline BEFORE upgrading.** Record, with screenshots or counts: chart version + revision (`helm list`), image tags, pods healthy, the admin login works, number of accounts/clusters, integrations, dashboards, notification rules, API tokens, KB documents, tickets list, a recent Troubleshoot event, and one read-only Nubi answer. Keep it in the working notes; it is the comparison key.
2. **Upgrade** with the Step 1 command. On an upgrade failure, stop and record the exact error; do not retry blindly. A 403 `denied` while pulling is the stale-credential gotcha below.
3. **Migration ordering — check the chart in the tag, it differs.** The enterprise chart (since #38808, `post-install,pre-upgrade`) migrates **before** the new pods roll out, and per that commit's message a failed migration aborts the upgrade with the old version still serving (do not try to force a failure to prove it; if an upgrade does fail, check `helm history` shows `failed` and the old pods are still running). The OSS chart at `v1.10.0-rc.3` is still `post-install,post-upgrade`: **new pods start against the old schema and the job migrates afterwards**. Read the hook in the tag you are verifying: `gh api "repos/<repo>/contents/deploy/kubernetes/nudgebee/templates/pre-install-postgres-migration-job.yaml?ref=<tag>" --jq .content`, base64-decode, find `helm.sh/hook`. Where it is post-upgrade, transient `column ... does not exist` errors are expected **until the job finishes**; errors that persist after it is `Completed` are a real finding.
4. **After the upgrade, repeat the 3A checks**, then **diff against the baseline**: every item in the baseline is still present and unchanged (accounts, integrations, dashboards, rules, tokens, KB docs, tickets), the admin still has the Admin tab, and the same read-only Nubi question still answers.
5. **Regression sweep of core flows** — open each and confirm it loads with data and no error banner: Home, Troubleshoot (events list + one event), Optimize, Infra/cluster detail, Dashboards, Automations, Tickets, Admin → Integrations / Notification Rules / Users, API Tokens (profile menu, bottom-left), Nubi chat (read-only prompt). "Whatever worked on the previous release still works" is the pass criterion; anything that worked in the baseline and does not now is a finding, even if no changelog line explains it.
6. **Values still valid.** The values file or reused values must not fail on removed/renamed chart values (e.g. on the enterprise side `branding.hostMap` and `branding.defaultTenant` now make the chart fail). Record any chart error verbatim.
7. **Upgrade-only changelog points are testable here** — backfills, rule/notification migrations on existing rows, behaviour changes to existing settings, re-embedding. Verify them in the UI against the data you created in the baseline. (On the shared OSS env, KB re-embedding is not exercisable — zero Qdrant collections.)

## Step 4 — Build the checklist from the changelog

Read the changelog for `<previous final>..<tag>`:
- on-prem: `gh release view <tag> --repo nudgebee/nudgebee-enterprise --json body --jq .body` (rc.1 or a final), or the commit range for later RCs
- OSS: `gh release view <tag> --repo nudgebee/nudgebee --json body --jq .body`, plus `gh api repos/nudgebee/nudgebee/compare/<prev>...<tag> --paginate` for the full range

Read **every PR title**. Do not filter to `feat` and `security`. **Default-behavior changes routinely hide inside `fix(...)` titles** — those are the ones that bite in prod. Verify the final default in the tag's code, not the title (an earlier PR can turn something on and a later one makes it opt-in).

Sort each PR into:
- **Proven by healthy pods + a completed migration job** — dependency, base-image and toolchain bumps.
- **Needs a live UI test** — feature changes, LLM-path changes, anything touching default behavior.
- **N/A for this run** — state why (needs an external account such as Jira DC, ServiceNow, AWS/GCP/Azure, Splunk, Datadog, Slack/Google Chat; or upgrade-only points in a fresh-install run; or fresh-install-only points in an upgrade run).

Present the consolidated checklist before starting. Keep it grouped and short.

## Step 5 — Walk it, one point at a time

For every point, state all three:

1. **What was checked and how** — cite the actual commit or diff, never just restate the PR title.
2. **Whether the repo-level check is sufficient on its own**, or whether it only proves intent/wiring and still needs a live UI test. Prompt-engineering changes (system prompt text edits) and resume/state-machine timing paths **always** need a live UI test — reading the diff only proves what the model was told to do, not what happens live.
3. **The updated status table** — keep it visible and update it as each point resolves:

| # | Point | Status | How verified |
|---|---|---|---|

**"Hold this" / "come back to it" / "I'll bring results later"** — mark it **pending** and keep it in the table. Never drop a held point. Never treat it as resolved.

## Step 6 — Review the release notes against the tag

Compare the notes to **the tag** (`<prev final>..<tag>`), never to prod. This is done for **both** environments. Raise only:

1. A feature **in the tag** but **missing from the notes**.
2. A claim **in the notes** that is **not in the tag**.
3. The tag **not on prod's line**, or something prod reverted (on-prem). For OSS, rely on the Step 2 compare result (`ahead`, `behind` = 0) and do not assume the tag mirrors enterprise's prod line.

Also flag: internal/benchmark jargon in customer-facing notes; leftover labels like `[DO NOT MERGE]` (check whether that PR actually merged); new capabilities or default changes labelled `fix` (the commit type should be honest); breaking changes with no upgrade note.

**Procedure (run all of it; the first-parent log alone hides most PRs — on 1.13.0 it showed 10 while the full count was ~119):**
1. Save the notes body to a file. Note which tag the notes' "Full Changelog" starts from (1.13.0's started at `1.12.0`, not `1.12.1`; it still covers everything).
2. **Notes → tag:** for every PR number in the notes, get `[.merged,.merge_commit_sha]` with `gh api repos/<repo>/pulls/<n> --jq '[.merged,.merge_commit_sha,.base.ref]|@tsv'`, then test that the sha is in the tag:
   - **On-prem (local git):** `git merge-base --is-ancestor <sha> <tag>`.
   - **OSS (no local clone):** `gh api repos/nudgebee/nudgebee/compare/<tag>...<sha> --jq .status` — `behind` or `identical` means the sha is in the tag; `ahead`/`diverged` means it is not.
   Anything not merged or not in the tag is claim type 2. If one comes back `NOTMERGED`, re-query it before reporting (an API blip gave a false hit on #39443).
3. **Tag → notes:** list every PR in the range, **not first-parent only**, and subtract the notes' numbers:
   - **On-prem:** `git log --merges --format='%h|%s' <prev>..<tag> | grep 'Merge pull request'`; read each remaining PR's title via `git log -1 --format=%s <merge>^2`.
   - **OSS:** `gh api repos/nudgebee/nudgebee/compare/<prev>...<tag> --paginate --jq '.commits[] | [.sha,(.commit.message|split("\n")[0])] | @tsv'` (bash/Git Bash; in PowerShell 5.1 pipe to a file and split there). Take the PR number from the `(#N)` suffix or `Merge pull request #N`; for a commit with no number use `gh api repos/nudgebee/nudgebee/commits/<sha>/pulls`. OSS numbers are a different series from enterprise.
4. Drop the ones that are a duplicate of a notes line by title (a hotfix re-PR of a main PR), and split the rest into internal (`chore`, `test`, `ci`, `docs`, backmerge, loadtest, dependabot) and real changes.
5. Lead with the customer-visible, security, breaking and default-changing ones; give the remaining count as "N small fixes also not in the notes", not as a list.
6. Carry over any notes problems the tester already posted for the earlier RC of this release and confirm each is still true.
7. **Verify every item you will list before it goes in the message** (the tester shares it as-is and cannot cross-check): the PR is merged, its commit is in the tag and **not** in the previous final (on-prem: `git merge-base --is-ancestor <sha> <prev>` must fail; OSS: `gh api repos/nudgebee/nudgebee/compare/<prev>...<sha> --jq .status` must be `ahead` — otherwise it shipped earlier and does not belong in this release's notes), the title says what you claim, any flag/env name exists in the tag's code (`git grep` at the tag), a default change is confirmed in the tag's code, and no later commit reverts it. Say "read, not tested live" where that is the case.

**How to establish "missing from the notes" properly:**
- Title matching alone is crude — the notes often list a feature under a bundling PR ("forward-port …", "cherry-pick … to test"). Look up the **PR that carried the commit** and compare its **number** with the PR numbers in the notes, **in the same repo** (enterprise and OSS number their PRs differently):
  `gh api repos/<repo>/commits/<full-sha>/pulls --jq '.[] | [.number,.base.ref,.title] | @tsv'`
  In Windows PowerShell 5.1 keep the jq expression free of double quotes (`@tsv`, not string interpolation or `split("\n")`) or it fails to parse.
- Read the commit body for anything you will call breaking, default-changing or customer-visible. Do not call something breaking from the title.
- Check the final state is not reverted or flipped by a later commit.
- Before calling a commit a "miss", check its ticket's sprint/status on the board: most apparent misses are fixes or polish already tracked elsewhere. Lead with "unaccounted for", not "N misses".
- Lines from `chore`/merge/backmerge/benchmark/lint-gate PRs can be removed. A line like "cherry-pick X to test" that is the **only** mention of a real feature must be **reworded**, not deleted.
- OSS: a feature present in enterprise but absent from the OSS tag is not a defect unless the OSS notes claim it.

Do **not** ask for prod-only-after-tag features to be added; at most a one-line FYI.

## Step 7 — Nubi/AI chat test prompts must be zero-impact

No environment is a disposable sandbox. Only suggest read-only prompts. If a write is genuinely needed (e.g. proving confirmation-approval re-triggers tool execution), pick the least-impactful write — `kubectl annotate` or a label, never delete, restart, scale, apply or patch.

**Why:** a suggested "restart pod X" prompt was once translated by the agent into `kubectl delete pod X`. It was caught before approval. Do not recreate that risk.

## Step 8 — Output format (for Slack)

The verdict is usually pasted into Slack, so:
- **Simple, short bullets. No tables** — they break when pasted. (Tables are fine inside the working checklist in this session, not in the Slack message.)
- Plain language; no jargon the reader would have to look up.
- Give PR references as **full links** (`https://github.com/<repo>/pull/<n>`, using the repo of the environment being verified), not bare `#123`, so they can be clicked. If asked not to use a code block, send it as plain text.
- One consolidated message: which environment, mode and tag were verified, tag result, **major issues**, upgrade result ("everything in the baseline still works" or the list of what broke), notes **remove / reword / edit / add**, and what was **not** checked. Do not restate it three times.
- **Completeness rule: whoever runs this skill — for on-prem or OSS, RC or final — must come away with the whole picture in one message, with no follow-up questions needed:** the tag and its lineage, the install/upgrade result, major issues, AND the full release-notes review (add / edit / reword / remove, plus anything missing from the notes or wrongly claimed in them), AND what was not checked. Never stop at "tag is fine". If a section has nothing, write "none found" for it rather than omitting it. The wording and format below are identical for OSS; only the repo, tag format and PR links change (OSS: `https://github.com/nudgebee/nudgebee/pull/<n>`, tag with `v`).
- **Use this exact shape (the format the tester approved on 1.13.0; do not make anyone ask for it again):**
  1. Headline `<On-prem|OSS> <tag> (<mode>)`, then one or two plain sentences on the main result (e.g. "The blocker from the earlier tags is fixed. After a fresh install, `nb-control-plane-k8s` shows without restarting services-server, and the app opens on it, not on Demo.").
  2. `Working:` bullets — tag on the prod line (OSS: `ahead`, `behind` = 0 from the compare), install deployed, migration completed, every service on the tag's build, Admin tab / cluster green / Nubi answers, the fix-specific evidence, any extra ticket the tester verified (link it), upgrade line (if tester-run: "tried by tester and worked; not tested by me; re-test on the next RC/release"; if you ran it: the baseline-vs-after result).
  3. `Release notes` (state which notes were reviewed: the release URL or tag, and the range checked, e.g. `1.12.1..1.13.0`) with, in this order: `Add:`, `Edit (default changes with no upgrade note):` , `Reword (listed only as "cherry-pick to test/prod"):`, `Remove:` (internal lines, and any claim in the notes that is not in the tag — say which). One line per item, a full PR link on each, written as what the user gets. Put breaking changes first in Add with "Breaking:" and a "needs an upgrade note" ask. Add = in the tag but missing from the notes. Write "none found" under any heading that is empty.
  4. `Not checked` — short bullets (board status, anything read-only and not tested live, external-account features, UI walk). Include only what is true.
- Never write a line that can be read as the old bug still being present. State the result, not the mechanism.
- Never include a PR in the message that you have not verified under Step 6 item 7. If unsure, say so to the tester instead of listing it.
- Keep the jargon-simplify and "N more small fixes" lines out unless asked; mention them only if the tester wants more detail.
- "Not ready to promote" is a recommendation; the decision belongs to the team.
- Never post to Slack, open an issue/PR, create a branch, restart a service or upgrade a shared env without an explicit yes.

## Step 9 — Verification comment on a ticket

Only after the tester confirms every step passed. Tickets are shared **one at a time** as a GitHub issue link — never grep or batch a set on your own.

When the ticket **already has a `Verified on test env ...` comment** and this is the prod pass:
1. Read the existing test comment first and treat its steps as the prod checklist.
2. Walk each step interactively — the tester performs the action live. Never assume or fabricate an outcome.
3. Match the test comment's structure: steps checked + screenshots/links + LGTM line.
4. **If the test comment already carries a QA TC line**, write "QA TC: already covered in test env comment above" and stop there.

## Known gotchas
- **403 `denied` pulling the chart is a stale credential, not a permission problem.** The chart is public and pulls anonymously; a leftover ghcr.io login forces an authenticated request that gets rejected. Fix: `helm registry logout ghcr.io && docker logout ghcr.io`, then retry.
- **`helm get values` prints plaintext secrets** (AWS/LLM/Slack). Never paste that output into docs, PRs or Slack.
- **Windows:** `gh` may not be on PATH (`%LOCALAPPDATA%\gh-cli\bin\gh.exe`). Git Bash mangles `ref:path` git arguments (a false "file missing") — set `MSYS_NO_PATHCONV=1`.
- **Enterprise vs OSS tags and PR numbers are different series** — never carry a tag or PR number from one repo to the other.
- **Welcome email, services-server split pods, `Demo` account** — see Step 3A before reporting any of them as bugs.
