# app-e2e-tests

End-to-end test suite for the Nudgebee frontend, written in **TypeScript + Playwright**. Drives a real Chromium browser against either the dev or test environment, captures traces/screenshots/videos on failure, and posts a summary to Slack from CI.

## Prerequisites

- Node.js 20+ (matches the Playwright base image used in CI)
- npm
- A reachable Nudgebee environment (`dev` or `test`) — see [`Environments`](#environments)
- Test-account credentials in a local `.env` / `.env.dev` file (see below)

## Quickstart

```bash
cd app-e2e-tests
npm ci

# Required once per machine: install browser binaries
npx playwright install

# Create local env files (these are not committed) — see "Environments" below
# for the variables to fill in.

# Run the full suite against `test` (default)
npm test
```

## npm scripts

| Script                | What it runs                                                                |
|-----------------------|-----------------------------------------------------------------------------|
| `npm test`            | Run the full Playwright suite (uses `.env` for credentials)                  |
| `npm run test:ui`     | Open Playwright's interactive UI mode for debugging                          |
| `npm run test:dev`    | Run against the **dev** environment (loads `.env.dev`)                       |
| `npm run test:test`   | Run against the **test** environment (loads `.env`)                          |
| `npm run test:dev:ui` | Dev + UI mode                                                                |
| `npm run test:dev:headed` | Dev + visible browser window                                             |
| `npm run report`      | Open the last HTML report in `playwright-report/`                            |
| `npm run test:slack`  | Run the suite then post a summary to Slack (CI-style local run)              |

Run a single test file:

```bash
npx playwright test tests/nubi/askNudgebee.spec.ts
```

Run a single named test:

```bash
npx playwright test -g "Magic link Sent Successfully"
```

## Project layout

```
app-e2e-tests/
├── tests/          # Spec files, grouped by feature area (admin, CloudAccount,
│                   # ClusterDetails, loginPage, nubi, workflow, ...). `utils/`
│                   # holds test-side helpers; locators reused across multiple
│                   # specs live in this directory.
├── pages/          # Page-Object Model — encapsulates UI interactions
├── specs/          # Test-plan notes (markdown)
└── notifications/  # Slack reporter + post-run notifier
```

Top-level: `playwright.config.ts` (config), `playwright-report/` (generated HTML/JSON), `Dockerfile` (CI image).

## Environments

Playwright picks the env file via `E2E_ENVIRONMENT`:

| `E2E_ENVIRONMENT` | File loaded | Use for                                |
|-------------------|-------------|----------------------------------------|
| _unset_ / `test`  | `.env`      | Runs against the staging (`test`) env  |
| `dev`             | `.env.dev`  | Runs against the dev env               |

In CI the env vars are injected from GitHub secrets — no file is needed.

A typical `.env` looks like:

```ini
BASE_URL=https://test.example.com
USER_EMAIL=qa-bot@example.com
USER_PASSWORD=...
```

## Writing a new test

Tests use the Page-Object Model — UI selectors live in `pages/`, not in the spec. Example:

```typescript
import { test } from '@playwright/test';
import { LoginPage } from '../pages/LoginPage';

test('Magic link Sent Successfully', async ({ page }) => {
  const login = new LoginPage(page);
  await login.requestMagicLink('qa-bot@example.com');
  await login.expectMagicLinkSent();
});
```

When you need a new UI interaction, prefer adding/extending a Page Object over inlining `page.locator(...)` calls in specs.

## CI

Three GitHub Actions workflows run this suite:

| Workflow                          | Trigger                                                   | Environment |
|-----------------------------------|-----------------------------------------------------------|-------------|
| `.github/workflows/app-e2e-tests.yaml`        | Push/PR to `test`, daily 10:30 AM IST cron, manual         | test |
| `.github/workflows/app-e2e-tests-dev.yaml`    | Push/PR to `main`, daily 2:00 PM IST cron, manual          | dev  |
| `.github/workflows/app-e2e-tests-hourly.yaml` | Manual only — on-demand dashboard run (`workflow_dispatch`) | test / prod |

`workflow_dispatch` inputs let you run a specific file, named test, or the full suite from the Actions UI.

## Known pitfalls

**Datadog APM auto-instrumentation breaks Playwright on self-hosted runners.** Datadog's `require-in-the-middle` hook collides with Playwright's TypeScript loader worker — the worker silently dies, the parent exits 0 with no test report. The CI workflows already disable APM injection for this job; if you reproduce a similar symptom locally, unset `DD_TRACE_ENABLED` / `NODE_OPTIONS`.

**Test data is environment-scoped.** Tests assume specific tenants/users/clusters exist in the target environment. Clobbering shared fixtures from a local run can break other engineers' test runs against the same environment.

## Reports + artifacts

After any local or CI run:

- HTML report: `playwright-report/index.html` (open with `npm run report`)
- JSON results: `playwright-report/results.json`
- On-failure: traces (`.zip`), screenshots, videos — all under `test-results/`
