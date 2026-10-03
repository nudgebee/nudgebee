// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { ClusterSummaryLocators } from "./clusterSummaryLocators";

// The cluster this suite runs against. ClusterDetailsLocators.openClusterFromConfig()
// reads the same pair and aborts on a mismatch, but it is read here too so a missing
// key fails with the key's name instead of surfacing as a tab that never appeared.
export function requiredCluster(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
  if (!cluster) {
    throw new Error("CLUSTER_NAME or CLUSTER is not set — add it to .env / .env.dev");
  }
  return cluster;
}

// AnchorComponent marks the open tab with data-tab-selected="true"
// (app/src/components/common/navigation/AnchorComponent.jsx). That attribute, not the
// URL, is the app's own answer to "which tab is open" and is always in step with what
// is rendered — the hash can lag a shallow router.replace by a frame.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
}

// The cluster's account id, taken from the detail-page URL the sidenav redirect landed
// on. Deep links are built from this rather than from an env key so the suite always
// addresses the very cluster the dropdown resolved to.
export function accountIdFromUrl(page: Page): string {
  const match = /\/kubernetes\/details\/([^/?#]+)/.exec(page.url());
  if (!match) {
    throw new Error(`Not on a cluster detail page — URL is ${page.url()}`);
  }
  return match[1];
}

// Opens Infra > K8s the way a person does and settles on the Summary tab.
//
// Deliberately not a bare goto('/kubernetes'): that route is only a redirector, and
// which cluster it opens depends on the global dropdown. openClusterFromConfig() drives
// the same redirect and then verifies the dropdown actually landed on CLUSTER, so a
// wrong-cluster run fails loudly here instead of quietly asserting against another
// tenant's fleet.
export async function openClusterSummary(page: Page): Promise<ClusterSummaryLocators> {
  requiredCluster();

  const locators = new ClusterSummaryLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.openClusterFromConfig();

  // Summary is tab 0 and the redirect already lands on #summary, so this observes the
  // default rather than causing it — the sanity test below depends on that.
  await expectSelectedTab(locators.SummaryTab);
  await settleSummaryTab(locators);

  return locators;
}

// Waits out the summary fetch. [KubernetesDetails].jsx renders a bare <Loader/> in place
// of all four summary widgets until clusterSummary has keys, so nothing on this tab —
// not the stats, not the jump-nav targets — exists before the response lands. The Cost
// Summary section is the last of the four in DOM order, which makes it the cheapest
// single signal that the whole tab has mounted.
export async function settleSummaryTab(locators: ClusterSummaryLocators, timeout = 90000): Promise<void> {
  await locators.costSection.waitFor({ state: "visible", timeout });
}

// Reads the label a FilterDropdown trigger is currently showing. The trigger renders its
// label and its selected value in one button, so the frequency assertions match on the
// value within that string rather than on an exact equality that would also have to
// pin the label's wording.
export async function filterText(filter: Locator): Promise<string> {
  return ((await filter.innerText()) ?? "").replace(/\s+/g, " ").trim();
}

// Clicks a sub-tab in the horizontal strip, falling back to the anchor dropdown when the
// strip is collapsed — the same two-plan approach AppsAndInfraLocators.clickTab() uses,
// kept here so this folder does not depend on a sibling suite's page object.
//
// parentTab is required by the fallback, not decoration: the `dropdown-<id>` items live in
// AnchorComponent's hover popover and are not in the DOM at all until that popover opens,
// so plan B has to hover the parent before it can wait for its item.
export async function clickSubTab(page: Page, parentTab: Locator, subTabId: string): Promise<void> {
  const stripTab = page.locator(`[id="${subTabId}"]`);
  const dropdownItem = page.locator(`[id="dropdown-${subTabId}"]`);

  // Absence is the expected other branch here: which of the two renders depends on the
  // viewport width, so a miss on the strip is routine and must not throw. Kept short —
  // this timeout is paid in full on every run that takes the fallback, and the strip is
  // already mounted by the time the parent tab's body has rendered.
  const onStrip = await stripTab
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);

  if (onStrip) {
    await stripTab.click();
  } else {
    await parentTab.hover();
    await dropdownItem.waitFor({ state: "visible", timeout: 10000 });
    await dropdownItem.click();
  }

  // Park the cursor off the tab strip: left on it, AnchorComponent opens its hover
  // popover over the content below and swallows the next click.
  await page.mouse.move(640, 500);
}
