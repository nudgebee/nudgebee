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

---

## Current snapshot

| Field | Value |
| --- | --- |
| Source repo | `nudgebee/nudgebee` |
| Source branch | `main` |
| Source commit | `46cbea0904740e11b29f76db325382db20f05e7b` |
| Drop date | 2026-05-09 |
| Iteration | 1 (initial dry-run) |

---

## Cleanup applied on top of the snapshot

Each item below was removed or modified after extracting the source snapshot,
so the team can keep iterating on what should and shouldn't be public.

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

**CI workflows: 64 of 80 dropped**
- **Kept (16):** all `*-prod.yaml` service deploys —
  `app-prod`, `benchmark-server-prod`, `cloud-collector-server-prod`,
  `code-analysis-prod`, `hasura-prod`, `k8s-collector-server-prod`,
  `llm-server-prod`, `migrations-prod`, `ml-k8s-server-prod`,
  `notifications-prod`, `rag-server-prod`, `relay-server-prod`,
  `runbook-side-car-prod`, `services-server-prod`, `ticket-server-prod`,
  `workflow-server-prod`.
- **Dropped:** all `*-dev-gke.yaml` and `*-test-gke.yaml` variants;
  internal ops automation (`ops-*`, `auto-pilot-*`, `nudgebee-tag-*`,
  `app-merge-*`, `app-version-update`, `aws-cf-templates-upload`); build
  pipelines tied to internal infra (`nudgebee-build-*`, `app-e2e-tests*`,
  `*-base-image-*`, `debugger-image-push*`, `cloud-collector-base-image*`,
  `e2e-dashboard`, `k8s-collector-agent`).
- Generic GitHub config kept untouched: `.github/dependabot.yml`,
  `.github/labeler.yml`, `.github/release.yml`, `.github/semantic.yml`,
  `.github/ISSUE_TEMPLATE/*`, `.github/pull_request_template.md`,
  `.github/config/*`.

---

## How to redo the drop

Each iteration produces a single squashed commit on `main`, replacing the
previous snapshot. There is no incremental history.

Prerequisites: you have a local clone of `nudgebee/nudgebee` at
`/Users/shiv/Workspace/nudgebee` (adjust paths as needed) and this repo
at `/Users/shiv/Workspace/nudgebee-oss`.

### 1. Refresh source main

```bash
cd /Users/shiv/Workspace/nudgebee
git fetch origin main
SRC_SHA=$(git rev-parse origin/main)
echo "Source commit: $SRC_SHA"
```

Use `origin/main` (not local `main`) so a stale checkout in the source
clone doesn't matter.

### 2. Wipe the dest working tree

From `nudgebee-oss`, drop everything except `.git/` (and the local-only
gitignored files like `.claude/settings.local.json`):

```bash
cd /Users/shiv/Workspace/nudgebee-oss
git ls-files -z | xargs -0 rm -f
find . -type d -empty -not -path './.git/*' -not -path './.git' -delete
```

### 3. Extract the source snapshot

```bash
git -C /Users/shiv/Workspace/nudgebee archive origin/main | tar -xf - -C .
```

`git archive` includes only files tracked at that commit — no uncommitted
changes, no other-branch contamination.

### 4. Apply OSS cleanup

Re-apply the cleanup list above. Some of it may already be done in upstream
`main` by the time you re-drop (the OSS-prep team is also pruning at the
source); skip any item that's already absent.

```bash
# Internal tooling
rm -rf .jules .claude/hooks .claude/settings.json
rm -rf .claude/skills/loki-logs .gemini/skills/loki-logs
rm -rf .claude/skills/my-tickets .gemini/skills/my-tickets
rm -rf .claude/skills/pr-backlog

# Env files (untracked but on disk)
rm -f api-server/hasura/.env.prod api-server/hasura/.env.dev

# CI workflows: keep only the 16 *-prod service deploys
cd .github/workflows
ls | grep -vE '^(app|benchmark-server|cloud-collector-server|code-analysis|hasura|k8s-collector-server|llm-server|migrations|ml-k8s-server|notifications|rag-server|relay-server|runbook-side-car|services-server|ticket-server|workflow-server)-prod\.ya?ml$' | xargs rm -f
cd -
```

For the `NB-xxxx` → `#xxx` scrub, the affected files are:
`.claude/skills/{commit,create-pr,hotfix,review-pr}/SKILL.md`,
`.gemini/skills/{commit,create-pr,hotfix,review-pr,pr-comments}/SKILL.md`.
Search for `NB-` and replace usages of `NB-xxx` / `NB-1234` / `NB-\d+`
with `#xxx` / `#123` / `#\d+` respectively. Update the column header in
each scope-table from "Ticket number" to "Issue number".

### 5. Update this file

Update the `Source commit` and `Drop date` fields above, bump the
`Iteration` counter, and add or amend the cleanup list to match what
this iteration actually changed.

### 6. Squash into a single commit

```bash
git add -A
git commit --amend -m "Initial OSS drop from nudgebee@<short-sha>" \
  -m "Snapshot of nudgebee/nudgebee main at <full-sha>." \
  -m "See OSS_DROP.md for the cleanup applied on top."
```

(For the very first drop in a fresh repo, use `git commit` instead of
`git commit --amend`.)

### 7. Push when ready

```bash
git push -u origin main
```

If the upstream branch is gone (because the remote is empty or was reset),
this command also re-establishes tracking.

---

## Things this process intentionally does *not* do

- **No history preservation.** Each drop is a single fresh commit. We
  intentionally do not carry over the source repo's commit history,
  authors, or internal PR/issue references.
- **No cherry-picking.** We snapshot `main`. We don't selectively pull
  files from other branches, even ones that look more "OSS-ready".
- **No automated rebasing of past drops.** Each iteration replaces the
  previous snapshot wholesale.

If a later iteration needs to *preserve* contributions made in
`nudgebee-oss` itself (e.g. an external contributor's fix), that work
should be reapplied on top of the new snapshot, not merged with it.
