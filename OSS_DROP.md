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
| Source commit | `fdd5ab0f4cd6ecf2366cc58eaab376ad38671a57` |
| Drop date | 2026-05-25 |
| Iteration | 13 |

### Notable upstream changes since the previous snapshot
(For team awareness; cleanup behavior is the same regardless.)

- 7 upstream commits between iter-12 (`2c584b9e4b`) and iter-13
  (`fdd5ab0f4c`). Full extract — gate 1 tripped (PR #31003 gofmt
  touched 3 EE Go files: jwt.go + marketplace aws.go/azure.go).
- **PR #31003** (merge `fdd5ab0f4c`): enforce `gofmt` via root
  `.golangci.yml` + pin golangci-lint to v2.11.3. Reformats 100+
  Go files across the monorepo as part of the lint enforcement.
- **PR #30904** (merge `a7fd70e8da`): Hive log-source integration
  with column autocomplete (substantial new code in observability +
  llm-server tools).
- **PR #30993** (`8db99727c0`): CustomTable2 header + expand-design
  UX update.
- **PR #30975** (`6deb6cbb4e`): SigNoz Cloud login hardening against
  tenant leak + parse-on-error.
- All EE-leakage greps still 0 (verified).

### Older iterations

- 25 upstream commits between iter-11 (`75e1261f76`) and iter-12
  (`2c584b9e4b`). Full extract — both gates tripped (EE paths +
  `_app.tsx` touched by hasura-URL rename).
- **PR #30974** (`1261c8e2f6`): rename upstream `/hasura/*` URL paths
  to `/rpc/*`. Removes the last Hasura-era naming from the URL surface
  area. Touched `_app.tsx`; oss-patches.sh applied cleanly so the
  EE-init blank-import strip still works.
- **PR #30994**: Cloud Logs + Metrics migrated to new DS V2.
- **PR #30950**: Usage & Limits tab redesign (KPI tiles + pivoted
  matrix + inline configs).
- **PR #30972**: branded NextAuth error page.
- **PR #30992**: llm-server workflow-builder clarification stops
  asking which Slack channel.
- **PR #30977**: hoist `RUNTIME_BASE` ARG above first FROM in
  llm-server Dockerfile (the same ARG-resolution bug pattern we
  flagged earlier on the cloud-collector Dockerfile).
- **PR #30954**: dependabot security-vuln remediation in app +
  llm-server.
- **PR #30963**: observability `defaultSource = "agent"` fallback
  hotfix.
- Customer-name + `x-hasura-*` scrubs from iter-11 are still clean
  (verified 0 references each).

### Older iterations

- 14 upstream commits between iter-10 (`aeb2f3103a`) and iter-11
  (`75e1261f76`). Full extract — gate 1 tripped (PR #30951 touched
  `ee/auth/superAdmin.ts` + `docs/ee/licensing-design.md`).
- **PR #30951** (merge `46920d4b15`): Hasura cleanup phase 3 — the
  big one.
  - Drops `x-hasura-*` dual-fallback from all receivers (api-server,
    llm-server, runbook-server, ticket-server, notifications-server).
    Source `x-hasura-*` refs in non-test/non-swagger/non-mock code
    now **0**.
  - Drops autopilot dual-emit (`autopilot/service.go`, `actions.py`).
  - Frontend JWT decoders drop `https://hasura.io/jwt/claims`
    namespace fallback (token TTL ≤1h, no stragglers).
  - `fromPgArray` helper deleted (legacy account-ids parsing).
  - **`jest.config.js` uuid-ESM fix**: closes the 56-test-suite jest
    transform cascade. Override resolves the async next/jest config
    and splices an allowlist into every node_modules pattern so
    pure-ESM deps (`uuid@14+`) get transformed.
  - **Customer-name scrub**: all 10 hardcoded customer-domain
    references and one customer-employee email in
    pagerduty_webhook_test.go replaced with `example.*` per RFC 2606.
- **PR #30903**: workflow publish/live-version model.
- **PR #30947**: recursion-depth guard for `core.call-workflow`.
- **PR #30449**: triage scoring rules now overridable (was hardcoded).
- **PR #30944**: UI Select / FilterDropdown / Input → DS V2.
- **PR #30955**: `NUDGEBEE_LICENSE` bound to viper config.
- **PR #30959/30962/30965/30966**: small fixes — automation button,
  signoz validation, zenduty user-lookup, last-run label.
- **A1/A2 closed won't-fix** (per EE memo
  `project_auth_middleware_failopen.md`): services are behind
  ingress; mandatory-token DX cost outweighs marginal security
  benefit. Boot warning stays at WARN.
- **L5 partial**: customer-internal-domain suffix removed from
  `knowledge_graph_service.go`; `.nudgebee.internal` still present
  (per user instruction to ignore).

### Older iterations

- 3 upstream commits between iter-9 (`9285583cc1`) and iter-10
  (`aeb2f3103a`). Applied via fast-forward patch (§6.5) — both safety
  gates clean. 50 files touched (UI DS V2 migrations + deploy fix).
  - **PR #30942**: K8s pages migrated to new ListingLayout DS
    components.
  - **PR #30933**: cloud-account summary tabs migrated to new DS.
  - **PR #30941**: migration jobs (postgres pre-install + golang-migrate)
    use `bash` instead of `sh` — `sh` was missing `[[`/`set -o pipefail`
    syntax used in the wrapper script.

### Older iterations

- 6 upstream commits between iter-8 (`e609cda1ee`) and iter-9
  (`9285583cc1`). Full extract — safety gate 1 tripped (EE paths
  touched: `ee/api/marketplace.go` adjusted by PR #30936's
  SecurityContext rename + `docs/ee/licensing-design.md` updated).
- **PR #30936** (merge `531d8984f2`): wire-format migration phase 2.
  All in-monorepo Go + Python senders now emit snake_case session
  variables (`user_id`, `tenant_id`) and headers (`x-user-id`,
  `x-tenant-id`); receivers keep dual-fallback parsers.
  `NewSecurityContextForSuperAdminAndTenant` → `NewSecurityContextForTenantAdmin`
  (misleading-name fix). JWT session claims moved from
  `https://hasura.io/jwt/claims` namespace wrapper to flat snake_case
  at the JWT root; decoders accept both shapes for in-flight tokens
  (≤2h TTL). Two senders (`autopilot/service.go`, `actions.py`)
  dual-emit to keep external auto-pilot repo working until its parser
  migrates. 5 service Dockerfiles parameterized via `ARG RUNTIME_BASE` /
  `ARG PYTHON_BASE` defaulting to `ghcr.io/nudgebee/<base-image>` so
  OSS docker builds don't need internal registry access. `.gitignore`
  adds 7 service-binary paths + dev debris; deletes two previously
  committed relay-server binaries.
- **PR #30907**: scrub stale Hasura references from remaining docs.
- **PR #30923 + #30899**: finops cost-showback tool added to llm-server.
- **UI DS V2 migrations** (#30921 home page, #30937 auto-pilot task
  details).
- **L5 unchanged**: `knowledge_graph_service.go:439` still hardcodes
  `.nudgebee.internal`.
- **Dependency: GHCR base-image mirrors** — PR #30936's Dockerfile
  `ARG`-default bases assume these tags exist publicly on GHCR:
  `nudgebee-cloud-collector-base:20260112-0715`,
  `nudgebee-llm-server-base:ubuntu-noble-20260430`,
  `nudgebee-ml-base:3.12-20250926-063300`,
  `nudgebee-ml-base:20250627-141845`,
  `nudgebee-python:3.12-20250919-140344`. OSS `docker build` of
  collector/llm/rag/ml/notifications fails until these are mirrored.

### Older iterations

- 18 upstream commits between iter-7 (`36f2ef1958`) and iter-8
  (`e609cda1ee`). Full extract — safety gate 1 tripped (EE paths
  touched: `.oss-exclude` added marketplace, `ee/license/deployment_mode.go`
  + `ee/auth/superAdmin.ts` + `ee/components/billing/*` all changed
  by PR #30920's RBAC tightening).
- **PR #30920** (merge `e609cda1ee`, fix `51eeef9535`): big bundle —
  - super_admin_readonly RBAC privilege-escalation fix (readonly admins
    could call `tenant_delete`; closed via distinct token emission +
    exact-match destructive-op gates across api-server, llm-server,
    runbook-server, cloud-collector).
  - Synthetic-admin typed flag (`isServerInternal`) replaces brittle
    `"admin"` role-string match in `IsSuperAdmin()`.
  - Helm chart secrets: lookup-or-generate `ACTION_API_SERVER_TOKEN`
    (no more `myadminsecretkey` default), fail-fast template if
    `NUDGEBEE_ENCRYPTION_KEY` left as `__REPLACE__`.
  - Hasura wire-format cleanup: rpcGateway emits new keys (`x-role`,
    `x-allowed-roles` JSON array), drops 4 dead `x-hasura-user-*-account-ids`
    fields. Backend parsers accept old + new via dual fallback.
  - Marketplace stragglers: `app/src/pages/api/marketplace/` added to
    `.oss-exclude`, dead `CreateSubscription()` removed.
  - `Header1.test.jsx` stale `@pages/PendoInitializer` mock fixed
    (52/52 pass).
  - docker-compose.yaml: 11 services switched `registry.nudgebee.com/...`
    → `ghcr.io/nudgebee/<service>:latest`.
- **PR #30920 follow-up amend** (`bf2315f8b7`): `app-e2e-tests/values.yaml`
  → `ghcr.io/nudgebee/app-e2e-tests`; `NEXT_PUBLIC_DEFAULT_IMAGE_REGISTRY`
  default set to `ghcr.io/nudgebee` for OSS; stale "baked-in default
  (registry.nudgebee.com)" comments fixed.
- **PR #30935**: workflow webhook trigger match across legacy `wf-<id>-`
  prefix without mutating `Internal.Name`.
- **PR #30932** (`7e9f28c287`): bearer-token decode fix for `/api/graphql`
  and `/api/rpc`.
- **A1/A2 status correction** (per EE team 2026-05-23): middleware
  tightening was attempted in #30920 and reverted within hours —
  tighter middleware broke SaaS signup-page flow where unauthenticated
  callers read `tier` for `SignupLink` visibility. Boot warning stays
  WARN (not ERROR). Structural fix deferred to a separate PR with
  proper endpoint split.
- **OSS-side patches applied in iter-8**:
  - `docker-compose.yaml` overlay patches all 11 `ghcr.io/nudgebee/<service>:latest`
    refs → `:edge` so the file works against edge.yaml's rolling builds.
    Added to Group B; back-port to EE will let it fall out.
  - `OSS_DROP.md` §6.5 safety-gate-1 fixed: was using a case-loop that
    silently passed under zsh (no word-splitting of unquoted vars).
    Replaced with portable `awk` form. Discovered the hard way when
    the gate didn't catch this iteration's `ee/` touches.

### Older iterations

- 1 upstream commit between iter-6 (`91073f275d`) and iter-7
  (`36f2ef1958`). Applied via **fast-forward patch** (§6.5). Single
  file: `api-server/migrations/run-migrations.sh` — fix to read
  Hasura v2 `cli_state` as baseline source (#30926). No EE overlap,
  no oss-patches.sh target touched.

- 6 upstream commits between iter-5 (`1cb10eba96`) and iter-6
  (`91073f275d`). Applied as a **fast-forward patch** — 21 files
  touched (cloud-collector AWS RDS, llm-server finops + spend_forecast
  tool, runbook-server workflow service, UI cloud-account RDS +
  cluster-upgrade-planner DS-V2 migration, llm/benchmark deps), zero
  EE overlap. See §6.5 for patch-forward procedure.

- 252 upstream commits between iter-4 (`8eebc31eaa`) and iter-5
  (`1cb10eba96`).
- **OSS source-leakage cleanup landed** (PR #30886, merge
  `1cb10eba96`, refactor `95c28d8544`). Closes most of the
  information-leakage cleanup TODO. Server-side EE policy details
  (email-allowlist, license-bound tenant pre-creation, OAuth-domain
  backfill, `validateOnPremNoTenantAccess` flow) are out of OSS code:
  - `bootstrap-check` handler delegates to `BootstrapCheckImpl`
    function pointer; OSS default is "any-email, single-tenant"; EE
    registers via `ee/license/bootstrap.go` `init()`.
  - `[...nextauth].ts` lost ~190 lines: 4 `licenseType === 'on-prem'`
    branches replaced with hooks (`resolveLicensedTenantUser`,
    `isOnPremAdmin`, `onReturningOAuthSignIn`, `onUnknownOAuthSignIn`).
    EE registers all 4 in `ee/auth/onPremPolicy.ts`.
  - UI: Billing tab + Pendo widget + "Chat with us" menu item moved
    into `ee/` via slot/hook patterns. `legacyLicenseType()` deleted
    along with the `licenseType` field on `/v1/license/me`.
  - Side-effect bug fixes: SaaS bootstrap-check now correctly rejects;
    EE-non-license-bound users no longer see Billing tab; non-admin
    EE users no longer see "Chat with us" menu.
  - **Deferred** (per PR review notes): structural auth-middleware
    fix for empty `ACTION_API_SERVER_TOKEN` (currently startup warning
    only); per-IP rate-limiting on `bootstrap-check`.
- **Earlier OSS-prep follow-up** (PR #30765, merge `3fa91d8c84`):
  helm `JWT_PRIVATE_KEY` / `NEXTAUTH_PRIVATE_KEY` dead-config removed,
  `NUDGEBEE_ENCRYPTION_KEY` sample replaced with `__REPLACE__`
  placeholder, GA/Chatwoot widgets switched from `!onPrem` to
  `tier === 'saas'`. OSS login fix landed via rpcGateway slice-copy
  (this iteration's reason for dropping `authHooks.ts` from Group B).
- **`authHooks.ts` removed from Group B overlay this iteration** —
  PR #30886 restructured: elevation moved inline into rpcGateway.ts
  (OSS code), `authHooks.ts` default elevator back to clean
  `(roles) => roles`. Upstream's authHooks.ts also adds 4 new hooks;
  overlay-restoring our old version would erase those.
- Workflow keep-set unchanged: 13 service `*-prod.yaml` files plus
  `release.yaml`.

### Older iterations

- 193 upstream commits between iter-3 (`1ef6a5bdb5`) and iter-4
  (`8eebc31eaa`).
- **EE/OSS boundary tooling landed** (PR #30583, merge
  `2c4cc638f1`, refactor `7fb523111a`). This is the headline change
  of this iteration:
  - EE-only Go code moved under `api-server/services/ee/` (billing,
    marketplace, license JWT, deployment-mode resolver, feature_flag
    bridge).
  - EE-only TS code moved under `app/src/ee/` (SwitchTenant, SAML
    components, super-admin token enricher, EE init).
  - SAML pages-router pages and self-signup pages also flagged
    OSS-strippable (can't live under `app/src/ee/` because Next
    pages-router requires filesystem paths under `app/src/pages/`).
  - Registry/slot pattern (`ee_registry.go`, `slots.ts`,
    `authHooks.ts`) replaces direct OSS→EE imports. OSS code now has
    *zero* references to any EE symbol; EE plugs in via `init()` /
    blank-imports.
  - License authority moved from Next.js into services-server behind
    `/v1/license/me` and `/v1/license/bootstrap-check`. OSS ships
    with a default no-features impl; EE registers a JWT impl via
    `init()`.
  - `.oss-exclude` is the canonical contract; `scripts/oss-patches.sh`
    is the canonical sed implementation; both consumers (in-repo
    `build-oss-snapshot.sh` and external OSS_DROP.md) source the same
    script so patch logic can't drift.
  - `scripts/check-oss-boundary.sh` lints import edges (Go, Python,
    TS/TSX) and runs in a new `oss-boundary` CI workflow on EE.
- **Demo-account guards across API modules** (#30734) — defence-in-depth
  for the upcoming hosted demo deployment.
- **License-gate errors now surface to UI** (#30741) instead of
  silently succeeding — pairs with the new `/v1/license/me` flow.
- **RS256 / NEXTAUTH_PRIVATE_KEY dropped from app, `api-server/deploy`
  deleted** (#30685). Cleanup of dead deploy paths.
- The Group A overlay grew by two entries (`CLA.md`, `CONTRIBUTING.md`)
  that landed on the OSS side between drops; Group B is still
  CONTRIBUTING.md only (CLA section can't be back-ported because
  enterprise has no CLA flow).
- Workflow keep-set unchanged: 13 service `*-prod.yaml` files plus
  `release.yaml`.

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
- `.github/workflows/release.yaml` — tag-driven (`git tag v*`)
  multi-arch image publish to GHCR + Helm subchart push, native
  per-arch matrix with `buildx imagetools` manifest merge. OSS-only;
  internal uses a different deploy pipeline.
- `.github/workflows/edge.yaml` — rolling-build companion to release.yaml.
  Fires on every push to `main`, publishes `:edge` per service to GHCR
  (multi-arch where supported, amd64-only for ML images). Used by the
  top-level `docker-compose.yaml` so `docker compose up` always tracks
  tip-of-main without waiting for a versioned release. OSS-only.

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
# ── EE-content strip ────────────────────────────────────────────────
# Forward-looking: as the EE/OSS separation design lands on the
# enterprise side (see internal `docs/ee/licensing-and-tiering.md`),
# EE-only code will live under `ee/` subtrees within each module, and
# `.oss-exclude` at the repo root will list every path that must not
# ship to OSS. The drop process applies that contract here.
#
# All blocks below are idempotent — they're no-ops on the current
# snapshot (which has no `.oss-exclude`, no `ee/` subtrees, and no
# boundary script). They become load-bearing the moment the first
# EE/OSS-separation PR merges to enterprise/main.

# Block 1: strip every path listed in .oss-exclude. The file is the
# canonical contract — EE side owns it, drop side honours it.
if [ -f .oss-exclude ]; then
  while IFS= read -r line; do
    line="${line%%#*}"             # strip inline comments
    line="$(echo "$line" | xargs)" # trim whitespace
    [ -z "$line" ] && continue
    rm -rf "$line"
  done < .oss-exclude
fi

# Block 1.5: apply companion patches via the EE-side canonical script.
# The `.oss-exclude` footer mandates that both consumers (the in-repo
# `scripts/build-oss-snapshot.sh` and this runbook) source the same
# `scripts/oss-patches.sh` so patch logic can't drift across them.
# The script is GNU/BSD-sed-portable, asserts each patch matched
# (silent regex drift → exit 1), and covers all three current targets:
#   - api-server/services/cmd/main.go    (Go EE blank-import)
#   - app/src/pages/_app.tsx             (@ee/init blank-import)
#   - app/src/instrumentation.ts         (range delete between
#                                         OSS-STRIP-EE-INIT-BEGIN/END
#                                         markers; can't be a single
#                                         regex because the EE call
#                                         lives inside a runtime
#                                         conditional)
# Skip silently if the script hasn't merged to enterprise/main yet
# (pre-Phase-1) — the file simply won't exist in the extracted tree.
if [ -f scripts/oss-patches.sh ]; then
  # shellcheck disable=SC1091
  source scripts/oss-patches.sh
  apply_oss_patches
fi

# Block 2: defense-in-depth strips that do NOT depend on .oss-exclude.
# These paths must never appear in OSS even if someone forgets to list
# them on the EE side. `docs/ee/` contains the licensing/tiering
# design doc (competitive intel, license claim shape, threat model)
# and is the highest-sensitivity directory.
rm -rf docs/ee

# Block 3: run the boundary check on the post-strip tree. Proves that
# no surviving non-EE file imports an `/ee/` path (catches transitive
# leakage that a raw EE-main scan would miss because the ee/ subtrees
# are still present there). The script ships from the EE side; if it
# hasn't landed yet, skip.
if [ -x scripts/check-oss-boundary.sh ]; then
  scripts/check-oss-boundary.sh
fi

# Block 4: strip the EE-only drop tooling itself. `.oss-exclude`,
# the boundary-check script, the patch-canonical script, and the
# snapshot builder all describe the EE→OSS contract and have no
# purpose in OSS.
rm -f .oss-exclude \
      scripts/check-oss-boundary.sh \
      scripts/oss-patches.sh \
      scripts/build-oss-snapshot.sh
rm -f .github/workflows/oss-boundary.yaml

# ── Existing OSS cleanup continues below ────────────────────────────

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
#   .github/workflows/release.yaml,
#   .github/workflows/edge.yaml
#
# Group B (exists upstream, OSS-modified):
#   CONTRIBUTING.md — has a "Contributor License Agreement" section
#     pointing at CLA.md. Cannot be back-ported because enterprise has
#     no CLA flow. Review the diff before overlay-restoring.
#   docker-compose.yaml — references `ghcr.io/nudgebee/<service>:edge`
#     for local-dev bootstraps. Upstream uses `:latest`, but `:latest`
#     is only published by release.yaml on `git tag v*` (which we don't
#     do for routine main commits). `:edge` is published by edge.yaml
#     on every main push, which is the right rolling-tag semantic for
#     a dev compose file. **Back-port to EE so this falls out of
#     Group B**: change `image: ghcr.io/nudgebee/<service>:latest` →
#     `:edge` on enterprise/main, then drop this entry.
#
# Removed from Group B in iter-5:
#   app/src/lib/authHooks.ts — was carrying a temporary OSS-only fix
#     to the default `_elevator`. PR #30886 restructured: elevation
#     now happens inline in rpcGateway.ts (OSS code), authHooks.ts
#     default elevator back to `(roles) => roles` as a clean extension
#     point. Upstream now adds 4+ new hooks; overlay-restoring our
#     old version would erase those.
git diff --stat HEAD -- \
  CLA.md \
  CONTRIBUTING.md \
  OSS_DROP.md \
  SECURITY.md \
  .github/dependabot.yml \
  .github/ISSUE_TEMPLATE/BUG-REPORT.yml \
  .github/ISSUE_TEMPLATE/FEATURE-REQUEST.yml \
  .github/pull_request_template.md \
  .github/workflows/release.yaml \
  .github/workflows/edge.yaml \
  docker-compose.yaml

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
  .github/workflows/release.yaml \
  .github/workflows/edge.yaml \
  docker-compose.yaml

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
ls | grep -vE '^(release\.yaml|edge\.yaml|(app|benchmark-server|cloud-collector-server|code-analysis|k8s-collector-server|llm-server|ml-k8s-server|notifications|rag-server|relay-server|services-server|ticket-server|workflow-server)-prod\.ya?ml)$' | xargs rm -f

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

### 4.5. Verify the snapshot builds (sanity check)

Block 3 (boundary check) proves no surviving file *imports* an `/ee/`
path, but it can't catch orphaned references or type errors that
surface only when the EE tree is gone. The cheapest way to be sure is
to compile the post-strip tree. Each command must exit 0 — a non-zero
exit means the snapshot won't build, so investigate before squashing.

```bash
# Go — every module that had (or could have had) EE entanglement.
( cd api-server/services                          && go mod download && go build ./... && go vet ./... )
( cd ticket-server                                && go build ./... )
( cd collector-server/cloud-collector             && go build ./... )
( cd collector-server/k8s-collector/relay-server  && go build ./... )
( cd llm/llm-server                               && go build ./... )
( cd llm/code-analysis                            && go build ./... )

# TypeScript — `tsc --noEmit` catches @ee/* leftovers and missing modules.
# Requires app/node_modules; install if absent.
( cd app && [ -d node_modules ] || npm ci --legacy-peer-deps --no-audit --no-fund )
( cd app && npx tsc --noEmit )
```

This is the local equivalent of the EE-side
`scripts/build-oss-snapshot.sh --verify` step (which we stripped in
Block 4). Iter-4 ran all eight commands cleanly; future iterations
should as well unless EE introduces a new orphaned reference.

For a deeper check (catches webpack/turbo issues `tsc` misses), also
run `cd app && npm run build`. Skip unless you suspect a runtime-side
problem — it takes ~3–5 minutes and is rarely needed.

### 5. Update this file

Update the `Source commit`, `Drop date`, and `Iteration` fields above.
Add an entry to "Notable upstream changes since the previous snapshot"
listing files newly added or removed in the source between iterations
(useful context for the team).

### 6. Squash into a single commit

The OSS repo accumulates additive commits between drops (CLA tweaks,
runbook updates, README work, entity-name swaps, etc.), so HEAD is
usually NOT the previous drop commit. Default path is to write a fresh
parentless commit with `git commit-tree` and re-point `main` at it:

```bash
SRC_SHA="<full sha from step 1>"
SRC_SHORT="<short sha>"

git add -A
TREE_SHA=$(git write-tree)
NEW_SHA=$(git commit-tree "$TREE_SHA" \
  -m "Initial OSS drop from nudgebee@${SRC_SHORT}" \
  -m "Snapshot of nudgebee/nudgebee-enterprise main at ${SRC_SHA}." \
  -m "See OSS_DROP.md for the cleanup applied on top.")
git update-ref refs/heads/main "$NEW_SHA"
git log --oneline -1   # sanity-check the new HEAD
```

The parentless commit cleanly discards all intermediate work — §4's
overlay step has already restored the OSS-only files we want to keep,
so nothing is lost. The previous main is still reachable on
`origin/main` until §7 replaces it.

**Fallback — `--amend` when HEAD is the previous drop**

If you've just done a drop and not pushed anything else on top, you
can amend in place. In practice this is rare — between every drop
we've ended up making a few overlay/runbook tweaks first, so
commit-tree has been the consistent path.

```bash
git add -A
git commit --amend -m "Initial OSS drop from nudgebee@${SRC_SHORT}" \
  -m "Snapshot of nudgebee/nudgebee-enterprise main at ${SRC_SHA}." \
  -m "See OSS_DROP.md for the cleanup applied on top."
```

### 6.5. Alternative — fast-forward patch (small deltas only)

If the upstream delta between drops is small and **completely outside
the `.oss-exclude` paths and `oss-patches.sh` targets**, you can skip
the full extract and apply a patch directly. Used in iter-6 (6 commits,
21 files, zero EE-overlap). Procedure:

```bash
PREV_SHA=$(grep -oE '[0-9a-f]{40}' OSS_DROP.md | head -1)
git fetch internal main
NEW_SHA=$(git rev-parse internal/main)

# Safety gate 1: confirm no .oss-exclude paths are touched.
# NB run under `bash`, not zsh — zsh doesn't word-split unquoted $EE_PATHS,
# so the case-loop silently passes even when EE paths *are* touched.
# Either invoke as `bash -c '...'` or use the awk variant below.
TOUCHED_EE=$(git diff --name-only $PREV_SHA..$NEW_SHA | \
  awk -v paths="$(git show $NEW_SHA:.oss-exclude | grep -vE '^\s*(#|$)' | tr '\n' ' ')" '
    BEGIN { n = split(paths, p, " ") }
    {
      for (i = 1; i <= n; i++) {
        if (p[i] != "" && index($0, p[i]) == 1) { print; next }
      }
    }
  ')
[ -z "$TOUCHED_EE" ] || { echo "❌ patch touches EE paths — must do full extract:"; echo "$TOUCHED_EE"; exit 1; }

# Safety gate 2: confirm no oss-patches.sh targets touched.
git diff --name-only $PREV_SHA..$NEW_SHA | grep -E 'cmd/main\.go|_app\.tsx|instrumentation\.ts' \
  && { echo "❌ patch touches oss-patches targets — must do full extract"; exit 1; } || true

# Apply.
git diff $PREV_SHA..$NEW_SHA | git apply --3way --whitespace=nowarn

# Update OSS_DROP.md metadata (Source commit, Drop date, Iteration) +
# Notable-upstream-changes entry, then squash via §6 commit-tree path.
```

If either safety gate trips, fall back to the full extract (§1–§4.5).
This path skips Blocks 1–4 entirely — they're for stripping EE content
the patch never introduced. Build-verify (§4.5) should still run on
modules touched by the patch. Squash and force-push via §6 → §7.

### 7. Force-push when ready

```bash
git push --force-with-lease origin main
```

`--force-with-lease` is safer than `--force`: it refuses if someone else
has pushed to `origin/main` since you last fetched. (For the very first
drop in a fresh repo, plain `git push -u origin main` is fine.)

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
- [x] ~~Replace `Nudgebee, Inc.` placeholder in `CLA.md` with the
      final legal entity name~~ — done 2026-05-19, set to
      **Nudgebee CloudXP Pvt Ltd** in both `CLA.md` and `CONTRIBUTING.md`.
- [ ] **Remove `OSS_DROP.md`** from the repo (this file is dry-run
      scaffolding only).
- [ ] **Re-enable Dependabot** by removing the
      `open-pull-requests-limit: 0` lines from `.github/dependabot.yml`.
- [ ] **Turn branch protection back on** (currently relaxed to allow
      drop-iteration force-pushes — see project memo
      `project_dryrun_force_push_ok`).
