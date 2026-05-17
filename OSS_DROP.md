# OSS Drop Process

This repo (`nudgebee/nudgebee`, the public OSS repo) is a curated
snapshot of the internal `nudgebee/nudgebee-enterprise` repository,
prepared for open-source release.

> The repos were renamed on 2026-05-16: the public OSS repo (formerly
> `nudgebee/nudgebee-oss`) is now `nudgebee/nudgebee`, and what used
> to be `nudgebee/nudgebee` is now `nudgebee/nudgebee-enterprise`.
> Local working-tree paths still reflect the old names
> (`/Users/shiv/Workspace/nudgebee-oss` for the public repo,
> `/Users/shiv/Workspace/nudgebee` for the enterprise repo).

It is not a fork. There is **no shared git history** with the source repo —
each drop is a single squashed commit representing a point-in-time snapshot
of the source's `main` branch, plus a fixed set of OSS-prep cleanups applied
on top.

> This document is itself part of the drop. When you run a fresh drop, the
> snapshot extraction will wipe this file along with everything else; you
> must re-create it (or `git checkout HEAD -- OSS_DROP.md` after extraction
> and update the metadata at the top).
>
> This file is **dry-run scaffolding**. Before the final public-launch push,
> remove it (`git rm OSS_DROP.md`) — it should not ship in the public OSS
> repo.

---

## Current snapshot

| Field | Value |
| --- | --- |
| Source repo | `nudgebee/nudgebee-enterprise` |
| Source branch | `main` |
| Source commit | `1ef6a5bdb5e24a5d12ebbadc66190615800785c8` |
| Drop date | 2026-05-16 |
| Iteration | 3 |

### Notable upstream changes since the previous snapshot
(For team awareness; cleanup behavior is the same regardless.)

- 353 upstream commits between iter-2 (`9667e0c0c2`) and iter-3
  (`1ef6a5bdb5`).
- **Hasura removed entirely** (#30525). The Hasura service is gone;
  migrations now run via golang-migrate (#30524 replaces the
  migration runner). `hasura-prod.yaml` and `migrations-prod.yaml`
  workflows were no-op stubs upstream and are deleted by the OSS
  workflow filter regardless.
- **Helm charts default to GHCR** (#30269, plus pin third-party chart
  images to GHCR mirrors). Affects `deploy/kubernetes/` chart values.
- **Pinot migration V734 flattened** + tracker auto-bootstrap on
  cutover (#30569).
- **Back-port from OSS landed** in #30544 (`49b6b9650a`): BuildKit
  cache mounts on all Dockerfiles, `postgresql-server-dev-16` apt
  fix in K8sCollector + Notifications CI workflows, committed
  `llm/code-analysis/go.sum`. As a result, **the entire Group B
  overlay is dropped this iteration** — the snapshot now carries
  these changes (plus further upstream improvements on top, see
  below).
- **Further upstream improvements on top of the back-port**, surfaced
  by the iter-3 drift check and kept (NOT overridden):
  - Go service Dockerfiles: `go build cmd/*.go` → `go build ./cmd`
    (proper package-path invocation, commit `650392de3e`).
  - `notifications-server/Dockerfile`: COPY list reshuffle
    (`static` → `branding`).
  - `llm/benchmark/Dockerfile`: forces `UVICORN_WORKERS=1` with a
    long comment explaining a per-process magic-link token store
    issue; adds proxy-header forwarding to uvicorn.
- Workflow keep-set is still 13 service `*-prod.yaml` files plus
  `release.yaml` (the OSS-only release pipeline). Workflow filter
  regex was updated this iteration to spare `release.yaml` from
  deletion — see runbook step 4.

---

## Cleanup applied on top of the snapshot

Each item below is removed or modified after extracting the source snapshot.
The cleanup is **idempotent** — items that have already been removed
upstream are silently skipped.

**Internal tooling / config**
- `.jules/` — bolt/palette/sentinel notes (internal product docs).
- `.claude/hooks/`, `.claude/settings.json` — internal sandbox setup that
  builds Go from source and assumes `/home/user/nudgebee` paths.
- `.claude/skills/loki-logs/`, `.gemini/skills/loki-logs/` — references the
  internal Loki cluster and namespace conventions.
- `.claude/skills/my-tickets/`, `.gemini/skills/my-tickets/` — hardcoded to
  `nudgebee` org's GitHub project board (ID `PVT_kwDOCG7t1c4ATt4G`).
- `.claude/skills/pr-backlog/` — personal cron-based macOS notification
  setup with hardcoded user paths.

**Skill content scrub**
- Replaced `NB-xxxx` internal-ticket-scope examples with generic `#xxx`
  GitHub-issue style in: `commit/`, `create-pr/`, `hotfix/`, `review-pr/`,
  `pr-comments/` (both `.claude` and `.gemini` copies, where present).

**Env files**
- `api-server/hasura/.env.prod`, `api-server/hasura/.env.dev` — both
  contained dummy local values, but they're conventionally gitignored;
  removed from the working tree.
- Kept `llm/benchmark/.env.example` and `llm/code-analysis/.env.example`
  (placeholder values, conventional OSS templates).

**OSS overlay files (preserved across drops)**

Files maintained directly in the OSS repo that must survive each drop.
The runbook restores them via `git checkout HEAD -- <path>` after the
snapshot extract (see step 4 below).

The overlay splits into two groups with different long-term policies:

**Group A — Pure OSS-only.** Don't exist upstream, never will. Stay in
the overlay forever.

- `OSS_DROP.md` — this file (removed before final public-launch push).
- `SECURITY.md` — vulnerability disclosure policy pointing at GitHub's
  private vulnerability reporting. OSS norm; upstream has no equivalent.
- `.github/dependabot.yml` — OSS variant: drops the stale `/auto-pilot`
  entry, adds a `github-actions` ecosystem entry, groups minor+patch
  updates per directory (`groups: routine-updates`), and during the
  dry-run **pauses dependabot entirely** via `open-pull-requests-limit:
  0` on every entry. Removing those lines re-enables dependabot.
- `.github/ISSUE_TEMPLATE/BUG-REPORT.yml` — rewritten for OSS use:
  pre-flight checklist, component multi-select, optional version field,
  expected/actual split, frequency dropdown, screenshots without the
  placeholder image-markdown default, OS dropdown with `macOS`.
- `.github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml` — rewritten: pre-flight
  checklist, component multi-select, "Use case / motivation" field,
  alternatives-considered field, `[FEATURE]` title prefix,
  auto-applies `enhancement` label.
- `.github/pull_request_template.md` — simplified Type-of-change list
  (6 items, was 10), replaced "Test A / Test B" with a real verification
  checklist, added optional Screenshots section for UI changes.
- `.github/workflows/release.yaml` — tag-driven multi-arch image
  publish to GHCR + Helm subchart push, native per-arch matrix with
  `buildx imagetools` manifest merge. OSS-only; internal uses a
  different deploy pipeline.

**Group B — OSS-modified upstream files.** _(Currently empty.)_

This group is for files that exist in both repos but have OSS-only
edits not yet in enterprise. The overlay mechanism silently overrides
whatever upstream ships, so each entry must be paired with a back-port
plan — once the change lands in `nudgebee/nudgebee-enterprise` main,
drop the file from the overlay so future drops pick up upstream
changes again.

A **drift check** in step 4 (run before the overlay) surfaces upstream
edits to overridden files so we notice when an override is about to
discard real upstream work.

**Historical Group B entries (kept here for traceability):**

The iter-2 → iter-3 drop carried a 19-file Group B covering BuildKit
cache mounts on every Dockerfile, the `postgresql-server-dev-16` apt
fix in two CI workflows, and the committed `llm/code-analysis/go.sum`.
All 19 were back-ported to `nudgebee-enterprise` in PR #30544
(`49b6b9650a`) and dropped from the overlay on iter-3 once the drift
check confirmed the snapshot carried the same content (and further
genuine upstream improvements on top — `go build ./cmd` package-path
fix, notifications-server COPY reshuffle, benchmark Dockerfile
proxy-header config).

**Files deleted post-extract that upstream still ships**

- `.github/ISSUE_TEMPLATE/SPIKE-REQUEST.yml` — internal Agile-team
  concept, useless to OSS contributors. Deleted after each extract.

**CI workflows**
- Filter: keep only the 13 service-CI `*-prod.yaml` files. Drop the
  rest (build pipelines, ops automation, dev/test deploys, base-image
  builds, all `auto-pilot-*`/`nudgebee-tag-*`/`app-merge-*`/`ops-*`).
  The keep-list excludes `hasura` and `migrations` because their
  upstream workflows are no-op stubs (just `actions/checkout`, no
  validation), and `runbook-side-car` because it was removed upstream
  between iterations 1 and 2.
- Rename: drop the `-prod` suffix from each surviving workflow file
  (`app-prod.yaml` → `app.yaml`). Also update each file's self-reference
  in its `paths:` filter so the workflow still auto-reruns on its own
  modification.
- Runner swap: change `runs-on: [self-hosted, X64]` → `runs-on:
  ubuntu-latest`. The internal repo's self-hosted runners aren't
  attached to the OSS repo; ubuntu-latest is the standard public option.
- Trigger retarget: change `pull_request: branches: ['prod']` →
  `branches: ['main']` (handling all four quote/spacing variants seen
  upstream). After this, the workflows actually run on OSS PRs.
- Display-name cleanup: strip `Prod`/`PROD`/`-Prod-GKE`/etc. from the
  `name:` field at the top of each workflow (`App Prod CI` → `App CI`,
  `llm-server-PROD-GKE CI` → `llm-server CI`).
- Generic GitHub config kept untouched: `.github/dependabot.yml`,
  `.github/labeler.yml`, `.github/release.yml`, `.github/semantic.yml`,
  `.github/ISSUE_TEMPLATE/*`, `.github/pull_request_template.md`,
  `.github/config/*`.

---

## How to redo the drop

Each iteration produces a single squashed commit on `main`, replacing the
previous snapshot. There is no incremental history.

Prerequisites: the OSS repo is at `/Users/shiv/Workspace/nudgebee-oss`
(local path predates the GitHub rename — repo on GitHub is now
`nudgebee/nudgebee`), and the enterprise repo is wired up as a remote
called `internal` inside the OSS clone:

```bash
# One-time setup (already done):
cd /Users/shiv/Workspace/nudgebee-oss
git remote add internal git@github.com:nudgebee/nudgebee-enterprise.git
# If the remote was added before the 2026-05-16 rename it still points
# at the old URL — GitHub auto-redirects, but update it for clarity:
#   git remote set-url internal git@github.com:nudgebee/nudgebee-enterprise.git
```

### 1. Refresh internal/main

```bash
git fetch internal main
SRC_SHA=$(git rev-parse internal/main)
SRC_SHORT=$(git rev-parse --short=10 internal/main)
echo "Source commit: $SRC_SHA"
```

### 2. Switch to main and wipe tracked files

```bash
git checkout main
git ls-files -z | xargs -0 rm -f
find . -type d -empty -not -path './.git/*' -not -path './.git' -not -path '.' -delete 2>/dev/null
```

This removes everything tracked in the previous snapshot. `.git/` and
local-only gitignored files (e.g. `.claude/settings.local.json`) survive.

### 3. Extract the source snapshot

```bash
git archive internal/main | tar -xf - -C .
```

`git archive` includes only files tracked at that commit — no uncommitted
changes, no other-branch contamination.

### 4. Apply OSS cleanup

```bash
# Drift check: before applying the overlay, compare the just-extracted
# snapshot (working tree) against the OSS version we're about to
# restore (HEAD). Group-A files (pure OSS-only — don't exist upstream)
# always diff; that's informational. Any non-empty diff for a Group-B
# path (exists upstream but OSS-modified) means upstream edited a file
# we're about to override — review before proceeding, or back-port the
# OSS change so the file falls out of the overlay.
#
# Group A (OSS-only):
#   CLA.md, OSS_DROP.md, SECURITY.md,
#   .github/dependabot.yml,
#   .github/ISSUE_TEMPLATE/BUG-REPORT.yml,
#   .github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml,
#   .github/pull_request_template.md,
#   .github/workflows/release.yaml
#
# Group B (exists upstream, OSS-modified):
#   CONTRIBUTING.md — has a "Contributor License Agreement" section
#     pointing at CLA.md. Cannot be back-ported because enterprise has
#     no CLA flow. Review the diff before overlay-restoring.
git diff --stat HEAD -- \
  CLA.md \
  CONTRIBUTING.md \
  OSS_DROP.md \
  SECURITY.md \
  .github/dependabot.yml \
  .github/ISSUE_TEMPLATE/BUG-REPORT.yml \
  .github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml \
  .github/pull_request_template.md \
  .github/workflows/release.yaml

# OSS overlay: restore files maintained in the OSS repo only. HEAD
# still points at the previous iteration's commit until we amend, so
# we can checkout these files from it.
git checkout HEAD -- \
  CLA.md \
  CONTRIBUTING.md \
  OSS_DROP.md \
  SECURITY.md \
  .github/dependabot.yml \
  .github/ISSUE_TEMPLATE/BUG-REPORT.yml \
  .github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml \
  .github/pull_request_template.md \
  .github/workflows/release.yaml

# Delete templates that upstream still ships but we don't want in OSS
rm -f .github/ISSUE_TEMPLATE/SPIKE-REQUEST.yml

# Internal tooling (idempotent: -f / -rf tolerate missing files)
rm -rf .jules .claude/hooks .claude/settings.json
rm -rf .claude/skills/loki-logs .gemini/skills/loki-logs
rm -rf .claude/skills/my-tickets .gemini/skills/my-tickets
rm -rf .claude/skills/pr-backlog

# Env files (untracked but on disk)
rm -f api-server/hasura/.env.prod api-server/hasura/.env.dev

# CI workflows step 1: keep only the 13 service CI *-prod files plus
# release.yaml (which we just restored from HEAD as part of the
# overlay). The `release\.yaml|...` outer alternation is essential —
# without it the filter would delete release.yaml because it doesn't
# end in `-prod.yaml`. Drops hasura, migrations, all build/ops/dev/
# test workflows. Idempotent: extra patterns harmless if not present.
cd .github/workflows
ls | grep -vE '^(release\.yaml|(app|benchmark-server|cloud-collector-server|code-analysis|k8s-collector-server|llm-server|ml-k8s-server|notifications|rag-server|relay-server|services-server|ticket-server|workflow-server)-prod\.ya?ml)$' | xargs rm -f

# CI workflows step 2: drop -prod suffix from filenames, fix self-
# references in path filters, and switch runners to ubuntu-latest
for f in *-prod.yaml; do
  base="${f%-prod.yaml}"
  sed -i '' "s|${f}|${base}.yaml|g" "$f"               # self-reference
  sed -i '' 's|runs-on: \[self-hosted, X64\]|runs-on: ubuntu-latest|g' "$f"
  mv "$f" "${base}.yaml"
done

# CI workflows step 3: retarget pull_request triggers from prod → main
# (handles single/double quotes and tight/loose spacing)
for f in *.yaml; do
  sed -i '' \
    -e "s/branches: \['prod'\]/branches: ['main']/" \
    -e "s/branches: \[ 'prod' \]/branches: ['main']/" \
    -e 's/branches: \["prod"\]/branches: ["main"]/' \
    -e 's/branches: \[ "prod" \]/branches: ["main"]/' \
    "$f"
done

# CI workflows step 4: strip "Prod"/"PROD"/"-Prod-GKE"/etc. from the
# `name:` field at the top of each workflow file
for f in *.yaml; do
  sed -i '' -E '1s/[ -][Pp][Rr][Oo][Dd][A-Za-z-]*( CI)$/\1/' "$f"
done
cd -

# NB-xxxx → #xxx scrub (idempotent — sed s/// is a no-op if the pattern
# isn't found)
for f in .claude/skills/commit/SKILL.md \
         .claude/skills/create-pr/SKILL.md \
         .claude/skills/hotfix/SKILL.md \
         .claude/skills/review-pr/SKILL.md \
         .gemini/skills/commit/SKILL.md \
         .gemini/skills/create-pr/SKILL.md \
         .gemini/skills/hotfix/SKILL.md \
         .gemini/skills/review-pr/SKILL.md \
         .gemini/skills/pr-comments/SKILL.md; do
  [ -f "$f" ] || continue
  sed -i '' \
    -e 's/`NB-xxx` | Ticket number/`#xxx` | Issue number/g' \
    -e 's/Ticket number scope/Issue number scope/g' \
    -e 's/use a ticket number `NB-xxx` or `nb-xxx`/use an issue number `#xxx`/g' \
    -e 's/feat(NB-1234)/feat(#123)/g' \
    -e 's/`NB-\\d+`/`#\\d+`/g' \
    -e 's|→ `NB-xxx`|→ `#xxx`|g' \
    -e 's|fix/NB-1234-description|fix/123-description|g' \
    "$f"
done

# Verify nothing left
grep -rn 'NB-' .claude .gemini 2>/dev/null || echo "(NB-xxxx scrub clean)"
```

### 5. Update this file

Update the `Source commit`, `Drop date`, and `Iteration` fields above.
Add an entry to "Notable upstream changes since the previous snapshot"
listing files newly added or removed in the source between iterations
(useful context for the team).

### 6. Squash into a single commit

```bash
git add -A
git commit --amend -m "Initial OSS drop from nudgebee@${SRC_SHORT}" \
  -m "Snapshot of nudgebee/nudgebee-enterprise main at ${SRC_SHA}." \
  -m "See OSS_DROP.md for the cleanup applied on top."
```

For the very first drop in a fresh repo, use `git commit` (no `--amend`)
and any historical iteration to manually set the message body.

### 7. Force-push when ready

```bash
git push --force-with-lease origin main
```

`--force-with-lease` is safer than `--force`: it refuses if someone else
has pushed to `origin/main` since you last fetched. (For the very first
push to an empty remote, use plain `git push -u origin main` instead —
no force needed.)

---

## Things this process intentionally does *not* do

- **No history preservation.** Each drop is a single fresh commit. We
  intentionally do not carry over the source repo's commit history,
  authors, or internal PR/issue references.
- **No cherry-picking.** We snapshot `main`. We don't selectively pull
  files from other branches, even ones that look more "OSS-ready".
- **No automated rebasing of past drops.** Each iteration replaces the
  previous snapshot wholesale.
- **Force-push is not safe once the repo is public.** Once `nudgebee-oss`
  is publicly announced, the "redo the drop" process above stops being
  viable. After launch, every internal change must come in via a
  *forward-port PR* (separate workflow — see internal team docs).

If a later iteration needs to *preserve* contributions made in
`nudgebee-oss` itself (e.g. an external contributor's fix), that work
should be reapplied on top of the new snapshot, not merged with it.

## Pre-launch checklist

Things deferred during the dry-run that must be done before / at the
moment the repo flips public:

- [ ] **Install the cla-assistant GitHub App** on `nudgebee/nudgebee`
      (<https://github.com/apps/cla-assistant>). The CLA text and the
      `CONTRIBUTING.md` flow are already in place; the bot just needs
      to be wired. cla-assistant.io's free tier requires a public repo,
      which is why this is deferred. Steps once public:
      1. Install the app on the repo.
      2. On <https://cla-assistant.io>, configure with **CLA source =
         repo**, path `CLA.md` on `main`.
      3. Allowlist bot accounts (`dependabot[bot]`,
         `cla-assistant[bot]`, any internal bots).
      4. Add `license/cla` to required status checks in branch
         protection on `main`.
- [ ] **Replace `Nudgebee, Inc.` placeholder** in `CLA.md` with the
      final legal entity name (counsel review).
- [ ] **Remove `OSS_DROP.md`** from the repo (this file is dry-run
      scaffolding only).
- [ ] **Re-enable Dependabot** by removing the
      `open-pull-requests-limit: 0` lines from `.github/dependabot.yml`.
- [ ] **Turn branch protection back on** (currently relaxed to allow
      drop-iteration force-pushes — see project memo
      `project_dryrun_force_push_ok`).
