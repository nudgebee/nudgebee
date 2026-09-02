// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AwsMonitoringLocators } from "./awsMonitoringLocators";

export type MonitoringSubTab = "alert-manager" | "cloud-logs" | "metrics";

// Every test in this area drives a shared dev cloud account, so the timeout has to cover
// login, the integration check, account selection and the sub-tab's own first fetch.
export const MONITORING_TIMEOUT_MS = 180000;

// Opens one Monitoring sub-tab the way a person does: log in, land on the AWS cloud
// account, then hover the Monitoring anchor and pick the sub-tab from its flyout.
//
// Deliberately NOT a goto('...#monitoring/metrics') deep link — the detail page derives
// selectedSubTab from its own tab state, and the sub-tabs live behind AnchorComponent's
// hover popover, which is the only route a user has. navigateToSubTab (AWSLocators) is
// the merged, retrying implementation of that hover-and-click, so it is reused rather
// than rewritten here.
export async function openMonitoringSubTab(page: Page, subTab: MonitoringSubTab): Promise<AwsMonitoringLocators> {
  const locators = new AwsMonitoringLocators(page);

  // doFullLogin already suppresses the guided tours and installs the overlay guard.
  await new LoginPage(page).doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  const targets: Record<MonitoringSubTab, { tab: Locator; url: RegExp; root: Locator }> = {
    "alert-manager": {
      tab: locators.MonitoringAlertManager,
      url: locators.MonitoringAlertManagerUrl,
      root: locators.alertManagerRoot,
    },
    "cloud-logs": {
      tab: locators.MonitoringCloudLogs,
      url: locators.MonitoringCloudLogsUrl,
      root: locators.cloudLogsRoot,
    },
    metrics: {
      tab: locators.MonitoringCloudMetrics,
      url: locators.MonitoringCloudMetricsUrl,
      root: locators.cloudMetricsRoot,
    },
  };

  const target = targets[subTab];
  await locators.navigateToSubTab(locators.AnchorTabMonitoring, target.tab, target.url);
  await parkCursor(page);

  // The panel root mounting is the signal the sub-tab finished switching. Waiting on it
  // rather than on networkidle: Cloud Logs and Cloud Metrics both idle without fetching
  // anything until a query is run, and the alert listing polls.
  await expect(target.root).toBeVisible({ timeout: 60000 });

  return locators;
}

// Moves back to an already-open sub-tab within one test, without paying for a second
// login. Same hover-and-click route as the initial landing.
export async function switchMonitoringSubTab(
  locators: AwsMonitoringLocators,
  page: Page,
  tab: Locator,
  url: RegExp,
  root: Locator
): Promise<void> {
  await locators.navigateToSubTab(locators.AnchorTabMonitoring, tab, url);
  await parkCursor(page);
  await expect(root).toBeVisible({ timeout: 60000 });
}

// Parks the cursor in open page content after a tab change. Left on the anchor strip,
// AnchorComponent keeps its flyout open over the toolbar and the next click hits that
// instead; parked at 0,0 it sits on the sidebar rail, whose own flyout is a Popover with
// an invisible page-wide backdrop. Mid-viewport is neither.
async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}

// Number of rows currently in the alert listing, once the table has settled.
//
// CustomTable only renders <tbody id='cloudMgr-body'> when it holds rows, so an account
// with no alert rules resolves to 0 here instead of timing out — absence is the answer,
// not a failure, which is why the wait is allowed to come back false.
export async function alertRowCount(locators: AwsMonitoringLocators): Promise<number> {
  // Races the first row against the empty state so an account with no alert rules resolves
  // as soon as CustomTable says so, instead of burning the full timeout on every call —
  // this runs in four tests, so waiting it out would cost minutes on an empty account.
  //
  // The swallow is the point: neither side arriving is still an answer callers handle (0),
  // so this must settle to a count rather than throw. Each side carries its own catch
  // rather than one around the race — the loser stays pending and would otherwise reject
  // unhandled after the winner has already returned.
  await Promise.race([
    locators.alertRows.first().waitFor({ state: "visible", timeout: 30000 }).catch(() => {}),
    locators.alertEmptyState.waitFor({ state: "visible", timeout: 30000 }).catch(() => {}),
  ]);
  return locators.alertRows.count();
}
