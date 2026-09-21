// Not for OSS
import { test, expect } from "@playwright/test";
import { parkCursor } from "../optimizeModuleHelper";
import { APPS_TABLE, CIS_TABLE, IMAGES_TABLE, POSTURE_TABLE, SECURITY_SUBTABS, VM_TABLE } from "./securityLocators";
import {
  expectSecurityBody,
  expectSelectedSubTab,
  expectTableSettled,
  expectUnselectedSubTab,
  noMatchImage,
  NO_ACCOUNTS_COPY,
  openSecuritySubTab,
  openSecurityTab,
  waitForFindingsTable,
} from "./securityHelper";

// Optimize -> Security: the tenant-level findings module at /optimise#security
// (app/src/components/optimise-new/SecurityView.tsx), which spans every account in the
// tenant. Not the per-cluster Security & Tools tabs already covered under
// tests/ClusterDetails/SecurityAndTools.
//
// Everything here is read-only. The writes this module offers act on shared dev data and
// cannot be undone from the UI: raising a PR on a finding moves it through the recommendation
// lifecycle, and the row actions file a real ticket. This suite therefore never opens a write
// modal — not even to cancel out of it, since a stray click inside one is a permanent change
// to a shared tenant. That also means the module has no create or form-validation surface
// this suite can reach; written up in the PR.
//
// Each sub-tab has two legitimate bodies — its view, or the copy naming the account type the
// tenant has none of — so every body assertion runs through expectSecurityBody and each test
// asserts something real on whichever branch this environment is in.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Optimize Security", () => {
  test(
    "Optimize Security sanity - open the Security tab with no sub-fragment, verify the sub-tab strip lists Image Scan, CIS Scan, VM Vulnerabilities and Cloud Posture and opens on Image Scan",
    { tag: ["@dev", "@test", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openSecurityTab(page);

      await test.step("Every sub-tab the module declares is on the strip", async () => {
        for (const tab of [
          locators.imageScanSubTab,
          locators.cisScanSubTab,
          locators.vmVulnerabilitiesSubTab,
          locators.cloudPostureSubTab,
        ]) {
          await expect(tab).toBeVisible({ timeout: 30000 });
        }
      });

      await test.step("Image Scan is the no-fragment default and the other three are closed", async () => {
        await expectSelectedSubTab(locators.imageScanSubTab);
        for (const tab of [locators.cisScanSubTab, locators.vmVulnerabilitiesSubTab, locators.cloudPostureSubTab]) {
          await expectUnselectedSubTab(tab);
        }
      });
    }
  );

  test(
    "Optimize Security - open the Image Scan sub-tab, verify the Apps view is the selected one and the Account, Severity and Status filters render above the apps table",
    { tag: ["@dev", "@test", "@smoke", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "imageScan");

      const populated = await expectSecurityBody(locators, locators.imageScanRoot, NO_ACCOUNTS_COPY.cluster);
      if (!populated) return;

      await test.step("Apps is the mount default of the view toggle", async () => {
        await expect(locators.imageScanAppsOption).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
        await expect(locators.imageScanImagesOption).toHaveAttribute("aria-checked", "false");
      });

      await test.step("The account filter SecurityView injects sits beside the view's own filters", async () => {
        await expect(locators.securityAccountFilter).toBeVisible({ timeout: 30000 });
        await expect(locators.imageScanSeverityFilter).toBeVisible();
        await expect(locators.imageScanStatusFilter).toBeVisible();
      });

      await test.step("The apps table reaches a terminal state", async () => {
        await expectTableSettled(locators, APPS_TABLE, locators.imageScanRoot);
      });
    }
  );

  test(
    "Optimize Security - open Image Scan, switch the view toggle from Apps to Images, verify the Image search box appears and the images table replaces the apps table",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "imageScan");

      const populated = await expectSecurityBody(locators, locators.imageScanRoot, NO_ACCOUNTS_COPY.cluster);
      if (!populated) return;

      await waitForFindingsTable(locators, APPS_TABLE, locators.imageScanRoot);

      await test.step("The Image search belongs to the Images view, so it is absent on Apps", async () => {
        await expect(locators.imageSearch).toHaveCount(0);
      });

      await parkCursor(page);
      await locators.imageScanImagesOption.click();

      await test.step("The toggle moves and the Images view takes over", async () => {
        await expect(locators.imageScanImagesOption).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
        await expect(locators.imageSearch).toBeVisible({ timeout: 30000 });
      });

      await test.step("The apps table is gone and the images table has landed", async () => {
        await expect(page.locator(`#${APPS_TABLE}`)).toHaveCount(0, { timeout: 30000 });
        await waitForFindingsTable(locators, IMAGES_TABLE, locators.imageScanRoot);
        await expect(page.locator(`#${IMAGES_TABLE}`)).toBeVisible({ timeout: 60000 });
      });
    }
  );

  test(
    "Optimize Security - open Image Scan, switch to Images, search an image reference that cannot exist, verify the search term is kept and the images table falls to its no-data state",
    { tag: ["@dev", "@test", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "imageScan");

      const populated = await expectSecurityBody(locators, locators.imageScanRoot, NO_ACCOUNTS_COPY.cluster);
      if (!populated) return;

      await parkCursor(page);
      await locators.imageScanImagesOption.click();
      await expect(locators.imageSearch).toBeVisible({ timeout: 30000 });
      await waitForFindingsTable(locators, IMAGES_TABLE, locators.imageScanRoot);

      const term = noMatchImage();
      await locators.searchAndApply(locators.imageSearch, term);

      await test.step("The field keeps what was typed", async () => {
        await expect(locators.imageSearch).toHaveValue(term, { timeout: 30000 });
      });

      await test.step("Nothing matches, so the table empties to its no-data state", async () => {
        await expect(locators.emptyStateFor(IMAGES_TABLE, locators.imageScanRoot)).toBeVisible({ timeout: 60000 });
        await expect(locators.rowsFor(IMAGES_TABLE)).toHaveCount(0);
      });
    }
  );

  // Deep-linked rather than reached by clicking the strip. Clicking it is covered by the
  // navigation test below, which asserts what that click reliably moves — the selected tab
  // and the URL. Asserting the BODY follows that click is not in this suite because it
  // currently does not, reliably: the counts behind the sub-tab chips land asynchronously
  // and rebuild filterOptions, which re-runs the page's hash effect against a stale asPath
  // and resets subTab, leaving the strip on the clicked tab and the body on the previous
  // one. Filed as a product bug in the PR, not worked around with a retry here.
  test(
    "Optimize Security - deep-link to the CIS Scan sub-tab, verify the Status filter renders and the CIS rules table reaches rows or its no-data state",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "cisScan");

      const populated = await expectSecurityBody(locators, locators.cisRoot, NO_ACCOUNTS_COPY.cluster);
      if (!populated) return;

      await test.step("The CIS view is the one rendered, not the image-scan view", async () => {
        await expect(locators.imageScanRoot).toHaveCount(0, { timeout: 30000 });
        await expect(locators.cisStatusFilter).toBeVisible({ timeout: 30000 });
      });

      await test.step("The CIS rules table reaches a terminal state", async () => {
        await expectTableSettled(locators, CIS_TABLE, locators.cisRoot);
      });
    }
  );

  test(
    "Optimize Security - deep-link to the VM Vulnerabilities sub-tab, verify the grouping toggle and Severity filter render and the findings table reaches rows or its no-data state",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "vmVulnerabilities");

      const populated = await expectSecurityBody(locators, locators.vmRoot, NO_ACCOUNTS_COPY.vm);
      if (!populated) return;

      await test.step("The view's own controls render beside the injected account filter", async () => {
        await expect(locators.vmGroupingToggle).toBeVisible({ timeout: 30000 });
        await expect(locators.vmSeverityFilter).toBeVisible();
        await expect(locators.securityAccountFilter).toBeVisible();
      });

      await test.step("The ungrouped findings table reaches a terminal state", async () => {
        await expectTableSettled(locators, VM_TABLE, locators.vmRoot);
      });
    }
  );

  test(
    "Optimize Security - deep-link to the Cloud Posture sub-tab, verify the Severity filter renders and the posture rules table reaches rows or its no-data state",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "cloudPosture");

      const populated = await expectSecurityBody(locators, locators.postureRoot, NO_ACCOUNTS_COPY.cloud);
      if (!populated) return;

      await test.step("The view's own controls render beside the injected account filter", async () => {
        await expect(locators.postureSeverityFilter).toBeVisible({ timeout: 30000 });
        await expect(locators.securityAccountFilter).toBeVisible();
      });

      await test.step("The posture rules table reaches a terminal state", async () => {
        await expectTableSettled(locators, POSTURE_TABLE, locators.postureRoot);
      });
    }
  );

  test(
    "Optimize Security - deep-link to the Cloud Posture sub-tab, reload the page, verify the Cloud Posture sub-tab is still the open one rather than falling back to Image Scan",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "cloudPosture");
      await expectSecurityBody(locators, locators.postureRoot, NO_ACCOUNTS_COPY.cloud);

      await page.reload();

      await test.step("The Security tab and its Cloud Posture sub-tab both come back", async () => {
        await expect(locators.securityTab).toHaveAttribute("data-tab-selected", "true", { timeout: 60000 });
        await expectSelectedSubTab(locators.cloudPostureSubTab);
        await expectUnselectedSubTab(locators.imageScanSubTab);
      });

      await test.step("The URL still carries the sub-fragment", async () => {
        await expect(page).toHaveURL(new RegExp(`#security/${SECURITY_SUBTABS.cloudPosture.fragment}$`), { timeout: 30000 });
      });
    }
  );

  test(
    "Optimize Security - open Image Scan, click CIS Scan, click back to Image Scan, verify each click moves both the selected sub-tab and the URL fragment",
    { tag: ["@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openSecuritySubTab(page, "imageScan");
      await expectSecurityBody(locators, locators.imageScanRoot, NO_ACCOUNTS_COPY.cluster);

      await parkCursor(page);
      await locators.cisScanSubTab.click();

      await test.step("The first click lands on CIS Scan", async () => {
        await expectSelectedSubTab(locators.cisScanSubTab);
        await expect(page).toHaveURL(new RegExp(`#security/${SECURITY_SUBTABS.cisScan.fragment}$`), { timeout: 30000 });
      });

      await parkCursor(page);
      await locators.imageScanSubTab.click();

      await test.step("The second click comes back to Image Scan, URL included", async () => {
        await expectSelectedSubTab(locators.imageScanSubTab);
        await expectUnselectedSubTab(locators.cisScanSubTab);
        await expect(page).toHaveURL(new RegExp(`#security/${SECURITY_SUBTABS.imageScan.fragment}$`), { timeout: 30000 });
      });
    }
  );
});
