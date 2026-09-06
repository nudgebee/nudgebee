// Not for OSS
import { test, expect } from "@playwright/test";
import { openClusterSummary, expectSelectedTab, accountIdFromUrl, settleSummaryTab, filterText, clickSubTab } from "./clusterSummaryHelper";

// Infra > K8s > cluster detail, Summary tab — the screen /kubernetes redirects into, and
// so the default landing page of the whole K8s section
// (app/src/pages/kubernetes/details/[KubernetesDetails].jsx, tabOptions[0]).
//
// The module is entirely read-only: it has no create, edit or delete surface, and the
// only state a user can change is the two chart toolbars, which hold their selection in
// component state. Nothing here writes to the shared dev tenant.
//
// Timeouts are set per test rather than once via test.describe.configure: the house
// standard (qa-automation-code-check P5) requires every spec to set its own
// test.setTimeout, so the per-test calls are the ones that stay.

test(
  "Cluster Summary sanity - open Infra > K8s from the sidenav, land on the cluster detail page, verify the Summary tab is the tab that opens by default",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);

    await expect(page).toHaveURL(/\/kubernetes\/details\/[^/?#]+/);
    await expectSelectedTab(cs.SummaryTab);

    await test.step("The jump-nav offers all three summary sections", async () => {
      await expect(cs.jumpNavClusterSummary).toBeVisible();
      await expect(cs.jumpNavCostSummary).toBeVisible();
      await expect(cs.jumpNavUtilization).toBeVisible();
    });

    await test.step("The other five top-level tabs are reachable from here", async () => {
      for (const tab of [cs.OptimizeTab, cs.AnchorTabTroubleshoot, cs.AnchorTabAppsAndInfra, cs.AnchorTabMonitoring, cs.AnchorTabSecurityAndTools]) {
        await expect(tab).toBeVisible();
      }
    });
  }
);

test(
  "Cluster Summary sanity - open the Summary tab, verify the Nodes, Applications and Pods stats render counts and the Insights, Utilization & Health and Quick Links cards are present",
  { tag: ["@dev", "@test", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);

    await test.step("Each headline stat shows its label followed by a number", async () => {
      // ds/Stat concatenates label and value into one node, so the label prefix anchors
      // the assertion to its own stat while \d proves the count actually rendered rather
      // than leaving the tile on its placeholder.
      await expect(cs.nodesStat).toHaveText(/^Nodes\s*[\d,]+/);
      await expect(cs.applicationsStat).toHaveText(/^Applications\s*[\d,]+/);
      await expect(cs.podsStat).toHaveText(/^Pods\s*[\d,]+/);
    });

    await test.step("The three cards beside the stats render", async () => {
      await expect(cs.insightsHeading).toBeVisible();
      await expect(cs.utilizationAndHealthHeading).toBeVisible();
      await expect(cs.quickLinksHeading).toBeVisible();
    });

    await test.step("Both scroll-anchored sections and their charts are on the page", async () => {
      await expect(cs.costSection).toBeVisible();
      await expect(cs.utilizationSection).toBeVisible();
      await cs.waitForCostChart();
      for (const label of ["CPU", "Memory (GB)", "Network Ingress (GB)", "Network Egress (GB)"]) {
        await expect(cs.utilizationCard(label)).toBeVisible();
      }
    });
  }
);

test(
  "Cluster Summary - open the Summary tab, click the Cost Summary jump-nav button then the Utilization one, verify each click scrolls its own section into the viewport",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);

    // Both sections sit well below the fold on landing — the three-column
    // KubernetesClusterSummary grid and the whole Events/Errors block render above them —
    // so this is a real precondition, not an assertion that was already true of the
    // section under test.
    await expect(cs.costSection).not.toBeInViewport();

    await test.step("Cost Summary scrolls its section up into view", async () => {
      await cs.jumpNavCostSummary.click();
      await expect(cs.costSection).toBeInViewport({ ratio: 0.1 });
    });

    await test.step("Utilization scrolls past it to the section below", async () => {
      await cs.jumpNavUtilization.click();
      await expect(cs.utilizationSection).toBeInViewport({ ratio: 0.1 });
      // The page really moved rather than the button merely lighting up. Asserted in this
      // direction only: Utilization sits below Cost Summary, and scrollIntoView aligns the
      // target to the top of the viewport, so reaching Utilization pushes Cost Summary off
      // the top. The reverse would not hold — Cost Summary is one ~300px chart card, so
      // scrolling back to it leaves Utilization still on screen underneath.
      await expect(cs.costSection).not.toBeInViewport();
    });
  }
);

test(
  "Cluster Summary - open the Cost Summary toolbar, set the frequency filter to Day, switch the chart to Line, verify the filter reads Day and Line becomes the checked chart type",
  { tag: ["@dev", "@test", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);
    await cs.waitForCostChart();

    await test.step("Month is the frequency the section opens on", async () => {
      await expect(cs.costFrequencyFilter).toContainText("Month");
      await expect(cs.costChartBarToggle).toHaveAttribute("aria-checked", "true");
    });

    await test.step("Picking Day re-queries the cost trend at daily granularity", async () => {
      await cs.openFilter(cs.costFrequencyFilter);
      await cs.filterOption("Day").click();
      await expect(cs.costFrequencyFilter).toContainText("Day");
      await expect(cs.costFrequencyFilter).not.toContainText("Month");
      // The chart is torn down for a Loader while the refetch is in flight, so waiting
      // for the canvas back proves the new frequency produced a rendered chart rather
      // than only relabelling the trigger.
      await cs.waitForCostChart();
    });

    await test.step("The chart switcher moves the selection from Bar to Line", async () => {
      await cs.costChartLineToggle.click();
      await expect(cs.costChartLineToggle).toHaveAttribute("aria-checked", "true");
      await expect(cs.costChartBarToggle).toHaveAttribute("aria-checked", "false");
      await cs.waitForCostChart();
      // The frequency survived the chart-type change — the two toolbar controls are
      // independent pieces of state, and a reset here would be a regression.
      await expect(cs.costFrequencyFilter).toContainText("Day");
    });
  }
);

test(
  "Cluster Summary - open the Cost Summary frequency filter, dismiss it with Escape, verify the panel closes and the frequency stays on Month",
  { tag: ["@dev", "@test", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);
    await cs.waitForCostChart();

    const before = await filterText(cs.costFrequencyFilter);
    expect(before).toContain("Month");

    await test.step("The panel opens with all three frequencies", async () => {
      await cs.openFilter(cs.costFrequencyFilter);
      for (const option of ["Day", "Week", "Month"]) {
        await expect(cs.filterOption(option)).toBeVisible();
      }
    });

    await test.step("Escape closes it without committing a selection", async () => {
      await page.keyboard.press("Escape");
      await expect(cs.filterOption("Day")).toBeHidden();
      // The trigger reads exactly what it read before the panel was opened, so the
      // dismissal applied nothing — a changed frequency would also have refetched.
      expect(await filterText(cs.costFrequencyFilter)).toBe(before);
      await cs.waitForCostChart();
    });
  }
);

test(
  "Cluster Summary - click the View Pods quick link, reload the browser on the resulting URL, verify the Pods sub-tab is still the open tab after the reload",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);

    await test.step("The quick link opens the Apps & Infra Pods sub-tab", async () => {
      await expect(cs.quickLink("View Pods")).toBeVisible();
      await cs.quickLink("View Pods").click();
      await expect(page).toHaveURL(/#kubernetes\/pods/, { timeout: 30000 });
      await expectSelectedTab(cs.AnchorTabAppsAndInfra);
    });

    await test.step("A reload reopens the same sub-tab from the URL alone", async () => {
      // This is the module's one piece of persisted state: the tab lives in the URL
      // fragment, so a copied link has to come back to Pods rather than to Summary.
      await page.reload({ waitUntil: "domcontentloaded" });
      await expect(page).toHaveURL(/#kubernetes\/pods/, { timeout: 30000 });
      await expectSelectedTab(cs.AnchorTabAppsAndInfra);
      await expect(cs.SummaryTab).toHaveAttribute("data-tab-selected", "false");
    });
  }
);

test(
  "Cluster Summary - open the cluster detail page at a summary fragment that does not exist, verify the Summary tab still opens instead of an empty body",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);
    const accountId = accountIdFromUrl(page);

    await test.step("An unknown child fragment is ignored rather than blanking the tab", async () => {
      await page.goto(`/kubernetes/details/${accountId}#summary/no-such-section`, { waitUntil: "domcontentloaded" });
      // AnchorComponent only resolves a child fragment against a tab's `tabOptions`, and
      // Summary declares `options` (jump-nav anchors) instead — so the unknown child is
      // dropped and the parent fragment alone decides the tab.
      await expectSelectedTab(cs.SummaryTab);
    });

    await test.step("The summary content renders in full behind that URL", async () => {
      await settleSummaryTab(cs);
      await expect(cs.nodesStat).toBeVisible();
      await expect(cs.quickLinksHeading).toBeVisible();
      await cs.waitForCostChart();
    });
  }
);

test(
  "Cluster Summary - click the Nodes stat to open the Apps & Infra Nodes sub-tab, return through the Summary tab, verify the summary stats and Cost Summary section are back on screen",
  { tag: ["@dev", "@test", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const cs = await openClusterSummary(page);

    await test.step("The Nodes stat drills into the Nodes listing", async () => {
      await cs.nodesStat.click();
      await expect(page).toHaveURL(/#kubernetes\/nodes/, { timeout: 30000 });
      await expectSelectedTab(cs.AnchorTabAppsAndInfra);
      // The summary is genuinely gone, not merely scrolled past — the tab body is keyed
      // on the selected tab and remounts on every switch.
      await expect(cs.costSection).toHaveCount(0);
    });

    await test.step("Another sub-tab in the same section still works from here", async () => {
      await clickSubTab(page, cs.AnchorTabAppsAndInfra, "namespaces");
      await expectSelectedTab(cs.AnchorTabAppsAndInfra);
    });

    await test.step("The Summary tab restores the summary", async () => {
      await cs.SummaryTab.click();
      await page.mouse.move(640, 500);
      await expectSelectedTab(cs.SummaryTab);
      await settleSummaryTab(cs);
      await expect(cs.nodesStat).toBeVisible();
      await expect(cs.costSection).toBeVisible();
    });
  }
);
