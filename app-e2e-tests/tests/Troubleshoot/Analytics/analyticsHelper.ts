// Not for OSS
import { Page, Locator, Response, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { AnalyticsLocators } from "./analyticsLocators";
import {
  ANALYTICS_FRAGMENT,
  ANALYTICS_OP,
  DAY_MS,
  ERROR_BANNER_TITLE,
  OVERVIEW_HEADING,
  TROUBLESHOOT_PATH,
} from "./analyticsConstants";

const NO_PANE_HINT =
  "The Analytics pane never rendered its Overview heading or its error banner. Both are the " +
  "post-load shapes of app/src/components/troubleshoot/analytics/TroubleshootAnalytics.tsx, so " +
  "neither appearing means the tab stayed on its loading skeleton past the timeout.";

// A window of exactly `days` ending now, in the epoch-ms form the range picker writes.
export function windowQuery(days: number): string {
  const end = Date.now();
  return `start_time=${end - days * DAY_MS}&end_time=${end}`;
}

// Resolves once the aggregates query has come back. Attached BEFORE the navigation that
// triggers it — a listener added afterwards can miss a response that already landed.
function watchAggregates(page: Page, timeout = 120000): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    const onResponse = (response: Response) => {
      if (!response.url().includes("/api/graphql")) return;
      if (!(response.request().postData() || "").includes(ANALYTICS_OP)) return;
      cleanup();
      resolve();
    };
    const timer = setTimeout(() => {
      cleanup();
      reject(new Error(`Analytics never issued ${ANALYTICS_OP} within ${timeout}ms`));
    }, timeout);
    const cleanup = () => {
      clearTimeout(timer);
      page.off("response", onResponse);
    };
    page.on("response", onResponse);
  });
}

// Lands directly on the Analytics fragment.
//
// Deep-linking is safe here, unlike /vm: the page re-derives its tab from router.asPath on
// every navigation, and #analytics carries no sub-tab, so the effect in
// app/src/pages/troubleshoot/index.jsx sets selectedTab and leaves the hash alone rather than
// canonicalising it away.
//
// `extraQuery` pins a window. Omit it to observe the tab's own default, which is the thing
// under test in the seven-day case — passing a range there would cause the state it asserts.
export async function openAnalytics(page: Page, extraQuery = ""): Promise<AnalyticsLocators> {
  const locators = new AnalyticsLocators(page);
  await new LoginPage(page).doFullLogin();

  const settled = watchAggregates(page);
  await page.goto(`${TROUBLESHOOT_PATH}${extraQuery ? `?${extraQuery}` : ""}#${ANALYTICS_FRAGMENT}`);
  await settled;

  await expectAnalyticsPane(locators);
  return locators;
}

// Re-opens Analytics scoped to one account, the way the Viewing filter's own URL write does.
// Already logged in by the time this is called, so it skips straight to the navigation.
export async function openAnalyticsScoped(page: Page, accountId: string, days: number): Promise<AnalyticsLocators> {
  const locators = new AnalyticsLocators(page);

  const settled = watchAggregates(page);
  await page.goto(`${TROUBLESHOOT_PATH}?accountIds=${accountId}&${windowQuery(days)}#${ANALYTICS_FRAGMENT}`);
  await settled;

  await expectAnalyticsPane(locators);
  return locators;
}

// The pane has two post-load shapes and which one shows depends on whether the aggregates
// query succeeded for this tenant, not on the code under test. Waiting on either keeps a
// backend hiccup from failing as an unexplained missing heading.
export async function expectAnalyticsPane(locators: AnalyticsLocators, timeout = 90000): Promise<void> {
  await expect(
    locators.section(OVERVIEW_HEADING).or(locators.errorBanner).first(),
    NO_PANE_HINT
  ).toBeVisible({ timeout });
}

// Fails the test when the tab came up on its error banner. Called by every case whose
// assertions read a number, so "the aggregates were unavailable" reports as itself rather
// than as a tile that would not appear.
export async function requireDataLoaded(locators: AnalyticsLocators): Promise<void> {
  // A missing banner is the normal case — the probe must answer false rather than throw.
  if (await locators.errorBanner.isVisible().catch(() => false)) {
    throw new Error(`Analytics rendered "${ERROR_BANNER_TITLE}" — the aggregates query failed for this tenant and window`);
  }
}

// AnchorComponent marks the open tab with data-tab-selected="true". This is the app's own
// notion of which tab is showing and, unlike the URL, it is always in step with what rendered.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
}

// Reads a window out of the current URL. Returns null when either bound is missing, which is
// how the caller tells "the tab has not pinned a range yet" from "it pinned this one".
export function readWindow(url: string): { start: number; end: number } | null {
  const start = Number(/[?&]start_time=(\d+)/.exec(url)?.[1]);
  const end = Number(/[?&]end_time=(\d+)/.exec(url)?.[1]);
  if (!Number.isFinite(start) || !Number.isFinite(end)) return null;
  return { start, end };
}

// Parks the cursor in open content. Left on the anchor strip, AnchorComponent opens its tab
// popover over whatever is below and the next click hits that instead; parked at 0,0 it sits
// on the sidebar rail, whose hover flyout swallows clicks page-wide. Mid-viewport is neither.
export async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}
