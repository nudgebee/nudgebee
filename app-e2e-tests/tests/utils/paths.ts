import path from "path";

export const PLAYWRIGHT_REPORT_DIR = path.resolve(process.cwd(), "playwright-report");
export const TENANT_FILE_PATH = path.join(PLAYWRIGHT_REPORT_DIR, "current-tenant.txt");
