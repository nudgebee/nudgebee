# OSS Drop Process

This repo (`nudgebee/nudgebee-oss`) is a curated snapshot of the internal
`nudgebee/nudgebee` repository, prepared for open-source release.

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
| Source repo | `nudgebee/nudgebee` |
| Source branch | `main` |
| Source commit | `9667e0c0c2ef37cb1c1927190784f8780f0449e8` |
| Drop date | 2026-05-09 |
| Iteration | 2 |

### Notable upstream changes since the previous snapshot
(For team awareness; cleanup behavior is the same regardless.)

- Added: `LICENSE`, `CODE_OF_CONDUCT.md`, `CONTRIBUTING.md`, `NOTICE`,
  `TRADEMARKS.md` (OSS-prep landed upstream).
- Removed: `Graphql.postman_collection.json`, `DEVELOPMENT_INTERNALS.md`,
  all `runbook-side-car-*` workflows.
- Workflow count went from 80 → 73 in the source; the per-snapshot keep
  set is now 15 (no `runbook-side-car-prod.yaml`).

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

**OSS-only overlay files (preserved across drops)**

The following files exist only in `nudgebee-oss` (not upstream) and are
maintained directly in the OSS repo. Each iteration restores them from
the previous commit's tree via `git checkout HEAD -- <path>` after the
snapshot extract — see runbook step 4 below.

- `OSS_DROP.md` — this file.
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

Prerequisites: the OSS repo is at `/Users/shiv/Workspace/nudgebee-oss`,
and the internal repo is wired up as a remote inside the OSS clone:

```bash
# One-time setup (already done):
cd /Users/shiv/Workspace/nudgebee-oss
git remote add internal git@github.com:nudgebee/nudgebee.git
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
# OSS overlay: restore files maintained in the OSS repo only (not in
# upstream main). HEAD still points at the previous iteration's commit
# until we amend, so we can checkout these files from it.
git checkout HEAD -- \
  OSS_DROP.md \
  SECURITY.md \
  .github/dependabot.yml \
  .github/ISSUE_TEMPLATE/BUG-REPORT.yml \
  .github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml \
  .github/pull_request_template.md

# Delete templates that upstream still ships but we don't want in OSS
rm -f .github/ISSUE_TEMPLATE/SPIKE-REQUEST.yml

# Internal tooling (idempotent: -f / -rf tolerate missing files)
rm -rf .jules .claude/hooks .claude/settings.json
rm -rf .claude/skills/loki-logs .gemini/skills/loki-logs
rm -rf .claude/skills/my-tickets .gemini/skills/my-tickets
rm -rf .claude/skills/pr-backlog

# Env files (untracked but on disk)
rm -f api-server/hasura/.env.prod api-server/hasura/.env.dev

# CI workflows step 1: keep only the 13 service CI *-prod files
# (drops hasura, migrations, runbook-side-car, plus all build/ops/dev/
# test workflows). Idempotent: extra patterns harmless if not present.
cd .github/workflows
ls | grep -vE '^(app|benchmark-server|cloud-collector-server|code-analysis|k8s-collector-server|llm-server|ml-k8s-server|notifications|rag-server|relay-server|services-server|ticket-server|workflow-server)-prod\.ya?ml$' | xargs rm -f

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
  -m "Snapshot of nudgebee/nudgebee main at ${SRC_SHA}." \
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
