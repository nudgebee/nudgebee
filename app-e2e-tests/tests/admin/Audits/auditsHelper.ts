// Not for OSS
import { Page, Response, expect } from "@playwright/test";
import { AuditsLocators } from "./auditsLocators";
import { suppressTourPopups } from "../../utils/tourSuppression";
import { registerTourOverlayGuard, registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import { AUDITS_OPERATION, AUDITS_PATH, GRAPHQL_PATH } from "./auditsConstants";

// One row of audits_v2, as selected by LIST_AUDIT_EVENTS in
// app/src/api1/audits/index.ts:10.
export interface AuditRow {
  user_id: string | null;
  event_actor: string | null;
  account_id: string | null;
  event_time: string;
  event_category: string;
  event_type: string;
  event_status: string;
  event_target: string | null;
  event_action: string;
}

// What one ListAuditEvents round trip carried. `queryText` matters as much as
// the rows: listAudits inlines the filter predicates into the query string
// itself via gqlStringify, so the query is where a filter proves it was applied.
export interface AuditsCapture {
  queryText: string;
  variables: { limit?: number; offset?: number };
  rows: AuditRow[];
  count: number;
}

async function readAuditsResponse(response: Response): Promise<AuditsCapture> {
  const postData = response.request().postData();
  if (!postData) {
    throw new Error(`captured ${AUDITS_OPERATION} request carried no post data`);
  }

  let requestBody: unknown;
  try {
    requestBody = JSON.parse(postData);
  } catch {
    throw new Error(`could not parse the ${AUDITS_OPERATION} request body as JSON: ${postData.slice(0, 500)}`);
  }

  const queries = (Array.isArray(requestBody) ? requestBody : [requestBody]) as Array<Record<string, any>>;
  const index = queries.findIndex((q) => q?.operationName === AUDITS_OPERATION);
  if (index < 0) {
    throw new Error(`captured request carried no ${AUDITS_OPERATION} operation`);
  }

  // A live dev cluster can answer with an HTML gateway error rather than JSON.
  // Surfacing the status and the body beats an opaque parse error in a CI log
  // that nobody can reproduce locally.
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    // Diagnostic only, inside the failure path: an unreadable body must not
    // mask the error being raised about it.
    const text = await response.text().catch(() => "<unreadable>");
    throw new Error(`${AUDITS_OPERATION} returned a non-JSON body (status ${response.status()}): ${text.slice(0, 500)}`);
  }
  // The gateway answers a batched POST with an array aligned to the request's
  // operations, so the slice must be read at the same index. Falling back to
  // index 0 would attribute another operation's rows to the audit listing.
  const bodies = (Array.isArray(body) ? body : [body]) as Array<Record<string, any>>;
  if (index >= bodies.length) {
    throw new Error(`batched response held ${bodies.length} slice(s) for ${queries.length} operation(s)`);
  }
  const slice = bodies[index];

  return {
    queryText: queries[index]?.query ?? "",
    variables: queries[index]?.variables ?? {},
    rows: slice?.data?.audits_v2?.rows ?? [],
    count: slice?.data?.audit_groupings_v2?.rows?.[0]?.count ?? 0,
  };
}

// Arms a listener for the next audit listing round trip. Call it before the
// action that triggers the refetch, then await the returned promise — the
// response the table rendered from is the only payload the assertions may be
// compared against, so no second query can race the UI.
export function waitForAudits(page: Page, timeout = 60000): Promise<AuditsCapture> {
  return page
    .waitForResponse(
      (res) =>
        res.url().includes(GRAPHQL_PATH) &&
        res.request().method() === "POST" &&
        (res.request().postData() ?? "").includes(AUDITS_OPERATION),
      { timeout }
    )
    .then(readAuditsResponse);
}

// Runs `action` while capturing the listing refetch it causes.
export async function withAuditsCapture(page: Page, action: () => Promise<void>, timeout = 60000): Promise<AuditsCapture> {
  const pending = waitForAudits(page, timeout);
  await action();
  return pending;
}

export async function openAuditsTab(page: Page): Promise<{ locators: AuditsLocators; capture: AuditsCapture }> {
  await suppressTourPopups(page);
  await registerTourOverlayGuard(page);
  await registerWelcomeTourAutoDismiss(page);

  const locators = new AuditsLocators(page);
  const capture = await withAuditsCapture(page, async () => {
    await page.goto(AUDITS_PATH);
    await page.waitForURL("**/user-management**", { timeout: 30000 });
  });

  // UserManagement reads the hash to pick the section (index.jsx:70-77), so the
  // tab must already be the selected one — no click needed to get here.
  await expect(locators.auditsTab).toHaveAttribute("data-tab-selected", "true");
  await expect(locators.table).toBeVisible();
  return { locators, capture };
}

// Several cases below filter by a value and then assert the result is both
// non-empty and homogeneous. Choosing that value from what the unfiltered page
// actually returned is what keeps them meaningful: a hardcoded action or status
// that happens to have no rows on the shared dev cluster would leave the
// "every row matches" assertion vacuously true instead of failing.
export function pickPresentValue(rows: AuditRow[], field: keyof AuditRow): string {
  const tally = new Map<string, number>();
  for (const row of rows) {
    const value = (row?.[field] ?? "") as string;
    if (value) {
      tally.set(value, (tally.get(value) ?? 0) + 1);
    }
  }
  const ranked = [...tally.entries()].sort((a, b) => b[1] - a[1]);
  expect(ranked.length, `the first page of audits should carry at least one ${String(field)} to filter on`).toBeGreaterThan(0);
  return ranked[0][0];
}

// The most frequent (action, status) pair on the unfiltered page, for the
// combined-filter case — picking each independently could name a pair that
// never co-occurs and so return nothing.
export function pickPresentPair(rows: AuditRow[]): { action: string; status: string } {
  const tally = new Map<string, number>();
  for (const row of rows) {
    if (row?.event_action && row?.event_status) {
      const key = `${row.event_action}|${row.event_status}`;
      tally.set(key, (tally.get(key) ?? 0) + 1);
    }
  }
  const ranked = [...tally.entries()].sort((a, b) => b[1] - a[1]);
  expect(ranked.length, "the first page of audits should carry at least one action/status pair to filter on").toBeGreaterThan(0);
  const [action, status] = ranked[0][0].split("|");
  return { action, status };
}

// The predicate listAudits builds for an equality filter
// (app/src/api1/audits/index.ts:68-85), rendered by gqlStringify as
// `{field:{_op:"VALUE"}}` and inlined into the query string.
export function predicate(field: string, op: string, value: string): string {
  return `${field}:{${op}:"${value}"}`;
}
