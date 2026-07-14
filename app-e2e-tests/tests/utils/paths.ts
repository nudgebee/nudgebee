import path from "path";

export const PLAYWRIGHT_REPORT_DIR = path.resolve(process.cwd(), "playwright-report");
export const TENANT_FILE_PATH = path.join(PLAYWRIGHT_REPORT_DIR, "current-tenant.txt");

// Authenticated session saved once by global-setup and reused by every test's
// context so the slow LDAP login runs a single time per run. Kept under the
// gitignored playwright/.auth/ folder because it contains session cookies.
export const AUTH_STATE_PATH = path.resolve(process.cwd(), "playwright/.auth/state.json");
