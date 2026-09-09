import { test, expect } from "@playwright/test";
import { openVmTab, expectActiveTab, expectSelectedTab } from "./vmHelper";
import { INVENTORY_TABLE, PACKAGES_TABLE, VULNERABILITIES_TABLE } from "./vmLocators";

// Infra > VM — the self-hosted fleet module (app/src/pages/vm/index.tsx).
//
// Everything here is read-only. The module's two write actions both start real
// background scans against a shared fleet (ScanVmDialog / ScanAccountDialog), so the
// dialogs are only ever opened and dismissed — see "Follow-ups" in the PR.
test.describe.configure({ timeout: 180000 });

// A term no VM name or resource id can match, so the "no results" assertion is about
// the filter working rather than about what the dev fleet happens to hold.
const NO_MATCH_TERM = `zz-no-such-vm-${Date.now()}`;

test("VM Summary tab renders the five fleet tiles and the scan-coverage breakdown", async ({ page }) => {
  const vm = await openVmTab(page, "summary");

  await test.step("The headline tiles are present", async () => {
    for (const tile of [vm.statVms, vm.statAgents, vm.statPackages, vm.statVulnerabilities, vm.statLastScan]) {
      await expect(tile).toBeVisible();
    }
  });

  await test.step("Counts are rendered as values, not blanks", async () => {
    // Virtual Machines / Packages Tracked / Open Vulnerabilities are plain counts.
    await expect(vm.statVms).toContainText(/\d+/);
    await expect(vm.statPackages).toContainText(/\d+/);
    await expect(vm.statVulnerabilities).toContainText(/\d+/);
    // Proxy Agents Connected is rendered as "<connected> / <total>".
    await expect(vm.statAgents).toContainText(/\d+\s*\/\s*\d+/);
  });

  await test.step("Scan coverage splits the fleet into inventoried and never-scanned", async () => {
    await expect(vm.statInventoried).toBeVisible();
    await expect(vm.statNeverScanned).toBeVisible();
    await expect(vm.statInventoried).toContainText("Inventoried");
    await expect(vm.statNeverScanned).toContainText("Never scanned");
  });
});

test("VM tab strip exposes all four views and lands on Summary by default", async ({ page }) => {
  const vm = await openVmTab(page, "summary");

  await expect(vm.summaryTab).toBeVisible();
  await expect(vm.instancesTab).toBeVisible();
  await expect(vm.vulnerabilitiesTab).toBeVisible();
  await expect(vm.packagesTab).toBeVisible();

  await expect(vm.summaryTab).toHaveText(/Summary/);
  await expect(vm.instancesTab).toHaveText(/Virtual Machines/);
  await expect(vm.vulnerabilitiesTab).toHaveText(/Vulnerabilities/);
  await expect(vm.packagesTab).toHaveText(/Packages/);

  // The selected tab, not the URL: landing on /vm can settle without the fragment.
  await expectSelectedTab(vm.summaryTab);
});

test("Virtual Machines tab lists the fleet inventory with its toolbar", async ({ page }) => {
  const vm = await openVmTab(page, "instances");

  await expect(vm.inventoryRoot).toBeVisible();
  await expect(vm.inventorySearch).toBeVisible();
  await expect(vm.inventorySearch).toHaveAttribute("placeholder", "Search By Name Or Resource ID");

  const rows = await vm.rowCount(INVENTORY_TABLE, vm.inventoryRoot);

  if (rows > 0) {
    // Column contract from VmInventory.tsx's HEADERS.
    for (const header of ["Name", "Host / Resource ID", "Operating System", "Status", "Packages", "Open Vulnerabilities", "Last Scan"]) {
      await expect(vm.inventoryTable.locator("th", { hasText: header }).first()).toBeVisible();
    }
  } else {
    // The account is known but has reported no machines yet. Note the panel says
    // "All good here!", not VmInventory's emptyHeading — see emptyIn() in vmLocators.
    await expect(vm.emptyIn(vm.inventoryRoot)).toBeVisible();
  }
});

test("Inventory search filters the VM list and restores it when cleared", async ({ page }) => {
  const vm = await openVmTab(page, "instances");

  const baseline = await vm.rowCount(INVENTORY_TABLE, vm.inventoryRoot);

  await test.step("A term that matches nothing empties the table", async () => {
    await vm.searchAndApply(vm.inventorySearch, NO_MATCH_TERM);
    // Retrying assertions, not a snapshot count: the search refetches, and reading the
    // table once would race the in-flight request and see the pre-search rows.
    await expect(vm.inventoryRows).toHaveCount(0);
    await expect(vm.emptyIn(vm.inventoryRoot)).toBeVisible();
  });

  await test.step("Clearing the search restores the original listing", async () => {
    await vm.clearSearch(vm.inventorySearch);
    await expect(vm.inventorySearch).toHaveValue("");
    // Waits for the restored rows rather than counting whatever is on screen now —
    // at this point the table still holds the empty result from the step above.
    await expect(vm.inventoryRows).toHaveCount(baseline);
  });
});

test("Vulnerabilities tab regroups findings across all four grouping tabs", async ({ page }) => {
  const vm = await openVmTab(page, "vulnerabilities");

  await expect(vm.vulnerabilitiesRoot).toBeVisible();

  await test.step("The flat finding list is the default grouping", async () => {
    await expect(vm.vulnGroupTabAll).toBeVisible();
    await vm.waitForTable(VULNERABILITIES_TABLE, vm.vulnerabilitiesRoot);
  });

  // Each grouping swaps the table for a server-rolled-up one with its own id, so
  // switching tabs is observable as a different table node, not just a restyle.
  for (const [tab, grouping] of [
    [vm.vulnGroupTabVulnerability, "vulnerability"],
    [vm.vulnGroupTabPackage, "package"],
    [vm.vulnGroupTabVm, "vm"],
  ] as const) {
    await test.step(`Grouped by ${grouping}`, async () => {
      await tab.click();
      await vm.waitForTable(`VM_VULNERABILITY_GROUPS_${grouping}`, vm.vulnerabilitiesRoot);
    });
  }

  await test.step("Returning to All restores the flat list", async () => {
    await vm.vulnGroupTabAll.click();
    await vm.waitForTable(VULNERABILITIES_TABLE, vm.vulnerabilitiesRoot);
  });
});

// A severity-filter test was written for this tab and dropped rather than shipped red:
// picking an option in the vulnerabilities severity FilterDropdown could not be made to
// pass against dev. The dropdown's trigger opens (its panel's backdrop appears), but
// getByRole("option", { name: "Critical", exact: true }) never becomes visible, so the
// option's accessible name is evidently not the plain label. Left uncovered and written
// up in the PR instead of weakening it into a test that asserts nothing.

test("Packages tab search filters the installed-package inventory", async ({ page }) => {
  const vm = await openVmTab(page, "packages");

  await expect(vm.packagesRoot).toBeVisible();
  await expect(vm.packagesSearch).toBeVisible();
  await expect(vm.packagesSearch).toHaveAttribute("placeholder", "Search By Package Name");
  await expect(vm.packageTypeFilter).toBeVisible();

  const baseline = await vm.rowCount(PACKAGES_TABLE, vm.packagesRoot);

  await test.step("A term that matches no package empties the table", async () => {
    await vm.searchAndApply(vm.packagesSearch, NO_MATCH_TERM);
    await expect(vm.rowsFor(PACKAGES_TABLE)).toHaveCount(0);
    await expect(vm.emptyIn(vm.packagesRoot)).toBeVisible();
  });

  await test.step("Clearing the search restores the original listing", async () => {
    await vm.clearSearch(vm.packagesSearch);
    // Same as the inventory search: wait for the restored count rather than snapshot
    // a table that is still showing the empty result from the step above.
    await expect(vm.rowsFor(PACKAGES_TABLE)).toHaveCount(baseline);
  });
});

test("Account scan dialog opens and cancels without starting a scan", async ({ page }) => {
  const vm = await openVmTab(page, "instances");

  await expect(vm.inventoryRoot).toBeVisible();

  // The Scan action is gated on write access (hasWriteAccess in VmInventory.tsx).
  await expect(vm.scanAccountBtn).toBeVisible({ timeout: 30000 });
  await vm.scanAccountBtn.click();

  await test.step("The dialog explains what an account-wide scan does", async () => {
    await expect(vm.scanAccountDialog).toBeVisible({ timeout: 15000 });
    await expect(vm.scanAccountDialog).toContainText("Collects the installed package inventory from every reachable instance");
    await expect(vm.scanAccountSubmitBtn).toBeVisible();
  });

  await test.step("Cancel closes it and starts nothing", async () => {
    // Deliberately never clicks #vm-scan-account-submit: it queues a real scan across
    // every instance of a shared account.
    await vm.scanAccountCancelBtn.click();
    await expect(vm.scanAccountDialog).toBeHidden({ timeout: 15000 });
    await expect(page.getByText("Account scan started.")).toHaveCount(0);
    // The table is still the one that was there before the dialog opened.
    await expect(vm.inventoryRoot).toBeVisible();
  });
});

test("Summary tiles deep-link into the tab that owns their numbers", async ({ page }) => {
  const vm = await openVmTab(page, "summary");

  await test.step("Virtual Machines tile opens the inventory tab", async () => {
    await vm.statVms.click();
    await expectActiveTab(page, "instances", vm.instancesTab);
    await expect(vm.inventoryRoot).toBeVisible({ timeout: 30000 });
  });

  await test.step("The tab strip returns to Summary", async () => {
    // Deliberately not page.goBack(): the page's own ?accountId= sync rewrites history
    // without the fragment, so Back lands on a hash-less /vm. The strip is the user's
    // route back and the one the module actually supports — see PR Follow-ups.
    await vm.summaryTab.click();
    await expectSelectedTab(vm.summaryTab);
    await expect(vm.statVms).toBeVisible({ timeout: 30000 });
  });

  await test.step("Open Vulnerabilities tile opens the vulnerabilities tab", async () => {
    await vm.statVulnerabilities.click();
    await expectActiveTab(page, "vulnerabilities", vm.vulnerabilitiesTab);
    await expect(vm.vulnerabilitiesRoot).toBeVisible({ timeout: 30000 });
  });
});
