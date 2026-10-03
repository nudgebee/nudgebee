// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { UserFeedbackLocators } from "./userFeedbackLocators";

export const OP_LIST_FEEDBACK = "ListAiFeedback";

// api1/ask-nudgebee.listAiFeedback inlines its filter into the query text with
// gqlStringify instead of sending variables, so the committed filter state is
// readable straight off the outgoing query string. That is what makes these
// assertions independent of how much feedback the shared dev tenant happens to
// hold on the day the suite runs.
export const MODULE_TROUBLESHOOT = { label: "Troubleshoot", value: "investigate" };
export const MODULE_LOKI = { label: "Loki Query", value: "loki" };
export const MODULE_PROMETHEUS = { label: "Prometheus Query", value: "prometheus" };
export const MODULE_ELASTICSEARCH = { label: "ElasticSearch Query", value: "es" };

export interface FeedbackQueryLog {
  queries: string[];
}

// Records the query text of every ListAiFeedback listing the tab receives, so a
// test can prove which filter actually reached the backend. Deliberately hung
// off "response" rather than "request": a logged entry then means the data has
// landed, not merely that the browser asked for it. Waiting on the request
// leaves a window where the tab has not re-rendered yet, and an assertion that
// runs in it reads the pre-filter rows.
export function trackFeedbackQueries(page: Page): FeedbackQueryLog {
  const log: FeedbackQueryLog = { queries: [] };
  page.on("response", (res) => {
    const req = res.request();
    if (req.method() !== "POST" || !req.url().includes("api/graphql")) return;
    const postData = req.postData();
    if (!postData) return;
    try {
      const payload = JSON.parse(postData);
      const operations = Array.isArray(payload) ? payload : [payload];
      for (const op of operations as { operationName?: string; query?: string }[]) {
        if (op.operationName === OP_LIST_FEEDBACK && op.query) log.queries.push(op.query);
      }
    } catch {
      if (postData.includes(OP_LIST_FEEDBACK)) log.queries.push(postData);
    }
  });
  return log;
}

// Waits until the tab has received at least one more listing than it had before
// the action, then returns the newest query. expect.poll is the wait — the
// response is the observable signal, so no fixed sleep is involved.
export async function nextFeedbackQuery(log: FeedbackQueryLog, countBefore: number): Promise<string> {
  await expect.poll(() => log.queries.length, { timeout: 45000, message: `no new ${OP_LIST_FEEDBACK} query was sent` }).toBeGreaterThan(countBefore);
  return log.queries[log.queries.length - 1];
}

// Waits for a listing query that actually carries the filter the action was
// meant to commit, rather than for "the next query to arrive".
//
// chooseFilter returns as soon as the trigger shows its new value, which is one
// render after the click and well before the refetch it started comes back. A
// plain nextFeedbackQuery taken after the FOLLOWING action therefore settles on
// that still-in-flight response and reads back the previous filter — which is
// exactly how the two-filter, clear-filter and remount cases failed while the
// tab behaved correctly. Use this to settle a filter before recording the next
// baseline, and to read the query an action committed.
//
// Only safe for a positive predicate: a negative one ("carries no module
// filter") is satisfied by the tab's own unfiltered mount request, so those
// keep using nextFeedbackQuery — after settling the pending query with this.
export async function nextFeedbackQueryMatching(
  log: FeedbackQueryLog,
  countBefore: number,
  predicate: (query: string) => boolean,
  description: string
): Promise<string> {
  await expect
    .poll(() => log.queries.slice(countBefore).some(predicate), { timeout: 45000, message: `no ${OP_LIST_FEEDBACK} query matched ${description}` })
    .toBe(true);
  return log.queries.slice(countBefore).find(predicate) as string;
}

// Pulls the created_at window out of a captured query. The tab always sends
// both bounds, so a missing one is a real failure rather than a shape to absorb.
export function windowOf(query: string): { startMs: number; endMs: number; spanMs: number } {
  const gte = /created_at:\{_gte:"([^"]+)"\}/.exec(query);
  const lte = /created_at:\{_lte:"([^"]+)"\}/.exec(query);
  if (!gte || !lte) throw new Error(`the listing query carried no created_at window: ${query}`);
  const startMs = Date.parse(gte[1]);
  const endMs = Date.parse(lte[1]);
  return { startMs, endMs, spanMs: endMs - startMs };
}

export function hasModuleFilter(query: string, value: string): boolean {
  return query.includes(`module:{_eq:"${value}"}`);
}

export function hasUsefulFilter(query: string, value: boolean): boolean {
  return query.includes(`useful:{_eq:${value}}`);
}

export function hasAnyModuleFilter(query: string): boolean {
  return /module:\{_eq:/.test(query);
}

// Logs in, opens the Nubi panel via "b-Cortex" and lands on Insights >
// Feedback (User Feedback relocated out of Settings into b-Cortex's Insights
// group, see docs/ia-consolidation-plan.md PR 4 — it was never part of AI &
// Tools). The query log is attached before the tab is selected so the tab's
// own first listing request is captured too.
export async function openUserFeedbackTab(page: Page): Promise<{ locators: UserFeedbackLocators; log: FeedbackQueryLog }> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new UserFeedbackLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.insightsGroupTab);
  await locators.insightsGroupTab.click();
  await locators.feedbackTab.waitFor({ state: "visible", timeout: 20000 });

  const log = trackFeedbackQueries(page);
  await locators.feedbackTab.click();
  await locators.listingCard.waitFor({ state: "visible", timeout: 30000 });
  await locators.waitForRowsSettled();
  return { locators, log };
}

// Switches to the sibling "Digests" sub-tab and back, which unmounts
// UserFeedbackTab and drops the filter state it holds — the only way to tell
// a reset from a value still held in React state. Both are sub-tabs of the
// same "Insights" group, so there is no need to leave it.
export async function remountUserFeedbackTab(locators: UserFeedbackLocators): Promise<void> {
  await locators.digestsTab.click();
  await locators.listingCard.waitFor({ state: "detached", timeout: 20000 });
  await locators.feedbackTab.click();
  await locators.listingCard.waitFor({ state: "visible", timeout: 30000 });
}
