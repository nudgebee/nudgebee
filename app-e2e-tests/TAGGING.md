# Test Tagging

Every test should carry two kinds of tag: **one category tag** (what kind of check this is) and **one environment tag** (where it's meant to run). This is what lets someone run "just the smoke suite" or "just regression" instead of the whole thing.

This convention already exists in ~30% of the suite — this doc makes it explicit and gives the rule for tagging the rest.

## Category tags

| Tag | Meaning | How to recognize one |
|---|---|---|
| `@smoke` | Fastest, broadest "is this basically alive" check | Titles like `"API testing <Area> -> <Page>"` — confirms the page loads and shows data, nothing deeper |
| `@sanity` | Per-feature "does the basic UI render" | Title literally says "sanity", or reads `"open <tab>, verify it renders its filters/columns"` |
| `@functional` | Proves a feature does what it claims | The umbrella tag — pairs with almost every test below except pure negative-path ones |
| `@regression` | Deeper, specific behavior that must not break | `"filter X by Y, verify every row..."`, `"expand a row, verify the drilldown..."`, CRUD flows |
| `@negative` | Failure / empty-state paths | `"search for X nothing matches, verify No Data"`, `"submit with a required field empty, verify the error"` |

A test can and often should carry more than one category tag (e.g. a filter test is both `@regression` and `@functional`).

## Modifier tags (optional, on top of a category)

| Tag | Meaning |
|---|---|
| `@crud` | Creates, updates, or deletes real data (not just reads) |
| `@search` | Exercises a search/filter control specifically |
| `@validation` | Proves a form rejects bad input (paired with `@negative`) |
| `@snackbar` | Asserts a specific toast/snackbar message, not just that some feedback appeared |
| `@rbac` | Exercises role-based access control — grants, scopes, or permission enforcement |
| `@quarantine` | Currently confirmed broken or permanently disabled — excluded from a trusted run via `--grep-invert @quarantine`, kept in its real category tag alongside so the gap is visible, not hidden |

## Environment tags

| Tag | Meaning |
|---|---|
| `@dev` | Safe to run against the dev environment |
| `@test` | Safe to run against the `test` (staging) environment |
| `@oss` | Also valid against the OSS build — omit if the feature is enterprise-only |

These are independent of category tags and stack with them: `{ tag: ["@dev", "@regression", "@functional"] }`.

## Running a category

```bash
# Smoke only, against dev
npx playwright test --project=chromium --grep @smoke

# Regression only, against dev
npx playwright test --project=chromium --grep @regression

# Everything except negative-path tests
npx playwright test --project=chromium --grep-invert @negative
```

Shortcut npm scripts (run from `app-e2e-tests/`): `npm run test:dev:smoke`, `test:dev:sanity`, `test:dev:regression`, `test:dev:negative` — and the same four with `:test:` instead of `:dev:` to target the `test` environment.

To exclude known-broken tests from a run: `npx playwright test --project=chromium --grep @smoke --grep-invert @quarantine`.

## Writing a new test

Every new `test(...)` must include a `tag` array with at least one category tag and one environment tag:

```typescript
test(
  "Audits - filter the log by an action the log actually holds, verify every Action cell reads that action",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => { ... }
);
```

## Current coverage

Every spec file in the suite is tagged (878 tests, 246 files at last count — verified via `npx playwright test --list tests`, zero parse errors). A CI check (`scripts/check-test-tags.js`) now requires every *new* test to carry tags going forward.

## Known-broken tests (`@quarantine`)

These were found broken while tagging (cross-checked against a real nightly full-suite run, or an explicit `test.skip()` already in the source) and tagged `@quarantine` rather than silently included as if healthy. Excluding them from a run: `--grep-invert @quarantine`.

| File | Why |
|---|---|
| `CloudAccount/{AWS,Azure,GCP}/Services/*Services.spec.ts` | "Resources → Details tab" — identical failure across all 3 providers, likely one shared bug |
| `CloudAccount/Azure/BlobContainer/AzureBlobContainerSummary.spec.ts` | Confirmed failing on the nightly run |
| `ClusterDetails/Optimize/OptimizeRightSizing.spec.ts` | 2 tests ("Recommendation Dropdown", "...→ Resolution") confirmed failing |
| `admin/Integrations/GCP.spec.ts` | Root-caused: CI's `GCP_SERVICE_ACCOUNT_KEY` secret fails client-side JSON validation, so "Check Permissions" never enables |
| `admin/Integrations/MCP.spec.ts`, `workflow/AILLM/mcpintegration.spec.ts` | Confirmed failing; the workflow test depends on the same MCP integration |
| `admin/Integrations/Azure.spec.ts` | Explicit `test.skip()` in source: "AZURE_SUBSCRIPTION_ID does not match the subscription dev discovery returns" — the Azure billing/subscription issue |
| `admin/Integrations/Zenduty.spec.ts`, `admin/Integrations/SSH.spec.ts`, `workflow/Gchatnotification.spec.ts`, `workflow/MsTeamsnotification.spec.ts` | Unconditional `test.skip()` in source — never run |
| `admin/Notifications/{Slo,Troubleshoot,optimization}Noti.spec.ts` | Confirmed failing on the nightly run |
| `nubi/function/Functions.spec.ts` (FN 10) | Confirmed failing on the nightly run. (`nubi/CreateCustomToolContainer.spec.ts` and `nubi/knowledge-base/KnowledgeBaseFunctional.spec.ts`'s 1st test were quarantined here too at one point, but a teammate's fix — commit `a63232c55e` — landed since; both confirmed passing and un-quarantined.) |
| `tempRecord.spec.ts` | **Not actually a test** — `test.setTimeout(0)` + `page.pause()` is a manual Playwright Inspector recording aid. Would hang indefinitely if a full-suite run ever reached it. Needs a human decision on whether it belongs in `tests/` at all — flagged here, not silently left as-is. |

None of these were filed as separate tickets yet — this table is the handoff for doing that.
