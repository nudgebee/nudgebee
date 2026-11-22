import path from "path";

// Anchored to this file rather than process.cwd(): the VS Code Playwright
// extension can launch the runner from the workspace root, which would resolve
// these to a different directory than a CLI run uses — leaving the auth state
// and tenant record where nothing looks for them.
const E2E_ROOT = path.resolve(__dirname, "../..");

export const PLAYWRIGHT_REPORT_DIR = path.join(E2E_ROOT, "playwright-report");

// Session state saved once by global-setup and reused by every test's context so
// the slow LDAP login runs a single time per run. Gitignored — it holds cookies.
export const AUTH_STATE_DIR = path.join(E2E_ROOT, "playwright/.auth");
export const AUTH_STATE_PATH = path.join(AUTH_STATE_DIR, "state.json");

// The tenant switchTenant() resolved, recorded so later runs can tell they are
// already in it. Deliberately NOT under playwright-report/: the HTML reporter
// clears that folder on every run, so a tenant recorded there never survives to
// the next run — and never exists at all for a VS Code ▶ Run Test, which does
// not execute global-setup.
export const TENANT_FILE_PATH = path.join(AUTH_STATE_DIR, "current-tenant.txt");
