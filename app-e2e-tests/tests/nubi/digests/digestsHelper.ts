// Not for OSS
import { Page, Response, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { DigestsLocators } from "./digestsLocators";

// The four scoreboard tiles DigestsTab renders for every selected week, in order.
// Unconditional: each one falls back to a zero rather than being omitted, so a missing
// tile is a regression rather than an absent-by-data section.
export const SCOREBOARD_LABELS = ["Events fully analysed", "Real incidents", "Pipeline completion", "Volume hedged"];

// The statuses that make DigestsTab offer a re-run. A 'generated' week never offers one,
// whoever is looking at it.
export const RERUNNABLE_STATUSES = ["failed", "partial"];

// app/src/api1/digests/index.ts wraps every digest call as Digests_<action>, so this is
// the operation name the per-week read arrives under.
export const OP_DIGEST_GET = "Digests_events_get_analysis_digest";

// This suite never starts a digest generation. DigestsTab's own confirmation copy says a
// run "re-reads every event analysis in the week across all accounts in the tenant, so it
// costs model usage each time it runs" — it is tenant-wide, billable and takes up to
// thirty minutes, so nothing here clicks digest-rerun-confirm-btn. The re-run cases below
// assert which weeks the control is offered for, never that pressing it works.

// The period keys whose reads have come back, oldest completed read first.
export interface DigestGetLog {
  periods: string[];
}

// Records the period_start of every per-week read that COMPLETES. The rail shows a week as
// "Sep 1 – Sep 7" while the storage key is a YYYY-MM-DD the DOM never carries, and the rail
// entry has no aria-selected — its selected state is a border colour — so the wire is the
// only place the selected week is observable, which is what makes this the evidence that
// clicking a rail entry actually fetched that entry's week.
//
// Bound to 'response' rather than 'request' on purpose: a request that is issued and then
// fails at the network layer would still satisfy a request-based log, so a case could
// assert it asked for a different week and only discover the read never delivered at the
// next wait, with a worse message. Logging on completion makes this a real "the data
// landed" barrier. Responses can in principle land out of order, but every case here waits
// for the review to settle before clicking again, so at most one read is ever in flight.
//
// Only one operation per request: queryGraphQL posts a single { query, variables,
// operationName } object, and queryGraphQLParallel issues N separate POSTs rather than one
// batched array, so there is no array-of-operations body shape to unwrap here.
export function trackDigestGets(page: Page): DigestGetLog {
  const log: DigestGetLog = { periods: [] };

  page.on("response", (response: Response) => {
    const request = response.request();
    if (!request.url().includes("/api/graphql")) return;
    // A probe, not an assertion: most traffic on this page is other operations with other
    // body shapes, and "this is not a digest read" is the normal answer.
    let body: { operationName?: string; variables?: { request?: { period_start?: string } } } | null = null;
    try {
      body = request.postDataJSON();
    } catch {
      return;
    }
    if (body?.operationName !== OP_DIGEST_GET) return;
    const period = body?.variables?.request?.period_start;
    if (typeof period === "string" && period) log.periods.push(period);
  });

  return log;
}

// DigestsTab shows a spinner while the week list is in flight and then resolves to
// exactly one of three states: the load-failure empty state, the no-digests-yet empty
// state, or the week rail. Waiting for whichever of the three rendered is the only
// end-of-load signal there is, and it is why every case below reaches the tab through
// this rather than asserting a rail entry straight away.
export async function waitForDigestsLoaded(locators: DigestsLocators): Promise<void> {
  await expect(locators.weekIncidentCounts.first().or(locators.emptyState).or(locators.listErrorState)).toBeVisible({ timeout: 40000 });
}

// Logs in, opens the Nubi panel and lands on b-Cortex > Insights > Digests. Digests is
// the Insights group's default sub-tab (DEFAULT_TAB_STATE in BCortexModal.jsx), so
// selecting the group is enough — there is no separate sub-tab click.
export async function openDigestsTab(page: Page): Promise<{ locators: DigestsLocators; log: DigestGetLog }> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new DigestsLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openBCortex(locators.insightsGroupTab);

  // Attached before the group is selected so the tab's own first per-week read is
  // captured — it is issued as soon as the week list resolves.
  const log = trackDigestGets(page);
  await locators.insightsGroupTab.click();

  // A tenant with the b-Cortex module off renders the placeholder instead of the tab,
  // which would otherwise surface as every control below being absent. Raced rather than
  // asserted straight away: checking for the placeholder on its own right after the click
  // passes against the not-yet-rendered tab and proves nothing, so wait until whichever
  // of the two rendered, then name which it was.
  await expect(locators.tabDescription.or(locators.bCortexDisabledPanel).first()).toBeVisible({ timeout: 40000 });
  await expect(locators.bCortexDisabledPanel).toHaveCount(0);
  await waitForDigestsLoaded(locators);
  return { locators, log };
}

// Leaves Insights for Memory and comes back, which unmounts DigestsTab and makes it
// refetch both the week list and the selected week — the only way to tell reloaded state
// from state still held in React.
export async function remountDigestsTab(locators: DigestsLocators): Promise<void> {
  await locators.memoryGroupTab.click();
  await locators.tabDescription.waitFor({ state: "detached", timeout: 30000 });
  await locators.insightsGroupTab.click();
  await locators.tabDescription.waitFor({ state: "visible", timeout: 40000 });
  await waitForDigestsLoaded(locators);
}

// Fails with a message naming what was missing rather than letting a tenant with no
// generated digests surface as a locator timeout halfway down a case. Digests are written
// by the llm-server scheduler's weekly job (RegisterEventAnalysisDigestJob, which runs
// every six hours and backfills gaps), so this is a data precondition, not a product bug.
export async function requireDigestWeeks(locators: DigestsLocators, minimum: number): Promise<number> {
  await expect(locators.emptyState).toHaveCount(0);
  await expect(locators.listErrorState).toHaveCount(0);
  const weeks = await locators.weekIncidentCounts.count();
  if (weeks < minimum) {
    throw new Error(
      `this case needs at least ${minimum} generated week(s) in b-Cortex > Insights > Digests, found ${weeks} — no weekly digest has been generated for this tenant on this environment`
    );
  }
  return weeks;
}

// Waits until the selected week's review is on screen. DigestsTab clears the digest and
// shows a spinner on every week change, so the scoreboard returning is what says the read
// landed; the two failure texts are raced alongside it so a failed read reports itself by
// name instead of timing out on the scoreboard.
export async function waitForWeekReview(locators: DigestsLocators): Promise<void> {
  await expect(
    locators.scoreboardLabel(SCOREBOARD_LABELS[0]).or(locators.digestErrorText).or(locators.noWeekSelectedText)
  ).toBeVisible({ timeout: 40000 });
  await expect(locators.digestErrorText).toHaveCount(0);
  await expect(locators.noWeekSelectedText).toHaveCount(0);
}

// Reads the incident count a rail entry advertises, so a case can assert against the
// number the rail promised rather than against a number the test chose.
export async function railIncidentCount(locators: DigestsLocators, index: number): Promise<number> {
  await expect(locators.weekIncidentCounts.nth(index)).toBeVisible({ timeout: 20000 });
  const text = (await locators.weekIncidentCounts.nth(index).textContent()) ?? "";
  const parsed = Number.parseInt(text.trim(), 10);
  if (Number.isNaN(parsed)) {
    throw new Error(`could not read an incident count from the rail line "${text}"`);
  }
  return parsed;
}

// Fails by name when the selected week carries no per-class findings. DigestsTab renders
// the "What broke & why" section only when the week produced class summaries, so a week
// that was generated but found nothing to summarise has no incident rows to expand.
export async function requireFindings(locators: DigestsLocators, minimum: number): Promise<number> {
  const findings = await locators.findingSummaries.count();
  if (findings < minimum) {
    throw new Error(
      `this case needs at least ${minimum} incident row(s) under "What broke & why", found ${findings} — the selected week's digest carries no per-class findings on this environment`
    );
  }
  return findings;
}

// Waits for the next per-week read to come back and returns its period. Polled on the log
// rather than awaited as a single response so a read that completed between the click and
// this call is not missed.
export async function waitForNewWeekRead(log: DigestGetLog, seen: number): Promise<string> {
  await expect
    .poll(() => log.periods.length, { timeout: 40000, message: "no further week review came back from the backend" })
    .toBeGreaterThan(seen);
  return log.periods[log.periods.length - 1];
}
