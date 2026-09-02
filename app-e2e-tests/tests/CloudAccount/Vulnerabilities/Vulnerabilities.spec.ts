// Not for OSS
import { test, expect } from "@playwright/test";
import { openVulnerabilitiesTab, expectSelectedTab } from "./vulnerabilitiesHelper";
import { HEADERS, SEVERITIES } from "./vulnerabilitiesLocators";

// Infra > Cloud > <account> > Vulnerabilities — OS-package CVEs for the account's scanned
// hosts (app/src/components/vulnerabilities/VulnerabilityTable.tsx).
//
// The module is read-only: it lists findings, filters them by severity and offers a
// download. There is no create, edit or delete surface to cover, and the download is only
// asserted to render — clicking it exports the live account's findings. See the PR.
test.describe.configure({ timeout: 180000 });

// Severities driven end-to-end. Two rather than all five keeps the run bounded; each one
// costs a full login and navigation.
const FILTERED_SEVERITIES = ["Critical", "High"] as const;

test(
  "Cloud Vulnerabilities sanity - open a cloud account, click the Vulnerabilities tab, verify the tab is selected and the URL carries the vulnerabilities fragment",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    await expect(vuln.vulnerabilitiesTab).toHaveText(/Vulnerabilities/);
    await expectSelectedTab(vuln.vulnerabilitiesTab);
    // getTabUrl in AnchorComponent builds the href from the tab's own fragment.
    await expect(page).toHaveURL(/#vulnerabilities\b/);
  },
);

test(
  "Cloud Vulnerabilities sanity - open the Vulnerabilities tab, verify the severity filter, the download action and the open-findings-only note render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    await expect(vuln.severityFilter).toBeVisible();
    await expect(vuln.downloadBtn).toBeVisible();
    // The toolbar says so because the query is pinned to OPEN_STATUSES — the listing is not
    // the account's whole CVE history, and the note is the only thing on screen saying it.
    await expect(vuln.openFindingsNote).toBeVisible();
  },
);

test(
  "Cloud Vulnerabilities sanity - open the Vulnerabilities tab, verify the findings table renders its eight columns, or the empty panel when the account has no findings",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    const findings = await vuln.findingCount();

    if (findings > 0) {
      // Column contract from HEADERS in VulnerabilityTable. Host is present because this is
      // the account-wide table; the host-scoped one drops it.
      for (const header of HEADERS) {
        await expect(vuln.table.locator("th", { hasText: header }).first()).toBeVisible();
      }
    } else {
      await expect(vuln.emptyPanel).toBeVisible();
    }
  },
);

test(
  "Cloud Vulnerabilities - open the severity filter, verify it offers all five severity levels from Critical to Info",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    await vuln.severityFilter.click();

    // Independent of what the account holds: the options come from SEVERITY_ORDER, not from
    // the findings, so this is the one filter assertion an empty account still proves.
    for (const severity of SEVERITIES) {
      await expect(vuln.severityOption(severity)).toBeVisible({ timeout: 15000 });
    }
  },
);

for (const severity of FILTERED_SEVERITIES) {
  test(
    `Cloud Vulnerabilities - open the Vulnerabilities tab, filter the findings by ${severity} severity, verify every listed finding is ${severity}`,
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const vuln = await openVulnerabilitiesTab(page);

      await vuln.filterBySeverity(severity);

      // The trigger takes the committed label, so this proves the selection landed even on an
      // account with no findings of this severity — where the column assertion below is vacuous.
      await expect(vuln.severityFilter).toContainText(severity);

      // Polled rather than branched on a row count: the refetch replaces the rows inside a
      // tbody that never detaches, so a count taken now can still be the pre-filter table.
      // Both listed end states are legitimate; "mixed" and "loading" keep retrying.
      await expect
        .poll(() => vuln.severityColumnState(severity), {
          timeout: 30000,
          message: `The findings listing never settled on ${severity} — it kept rows of another severity, or stayed mid-refetch.`,
        })
        .toMatch(/^(filtered|empty)$/);
    },
  );
}

test(
  "Cloud Vulnerabilities - filter the findings by Critical, switch the filter to Low, verify the listing reflects Low and no Critical finding survives",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    await test.step("Critical is committed first", async () => {
      await vuln.filterBySeverity("Critical");
      await expect(vuln.severityFilter).toContainText("Critical");
    });

    await test.step("Switching to Low re-reads the listing", async () => {
      await vuln.filterBySeverity("Low");
      // Re-read after the switch rather than asserting against the Critical result still on
      // screen — the second fetch replaces the rows, it does not add to them.
      await expect(vuln.severityFilter).toContainText("Low");
      await expect(vuln.severityFilter).not.toContainText("Critical");

      // This is the switch the stale count got wrong: Critical can leave the table empty, so a
      // count read here reports 0 and the old branch asserted an empty panel that the arriving
      // Low rows then remove. Polling the settled state is what makes the chained flow honest.
      await expect
        .poll(() => vuln.severityColumnState("Low"), {
          timeout: 30000,
          message: "The findings listing never settled on Low after switching off Critical.",
        })
        .toMatch(/^(filtered|empty)$/);
    });
  },
);

test(
  "Cloud Vulnerabilities - switch to the Summary tab and back to Vulnerabilities, verify the findings listing is restored",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const vuln = await openVulnerabilitiesTab(page);

    const baseline = await vuln.findingCount();

    await test.step("Summary replaces the findings panel", async () => {
      await vuln.summaryTab.click();
      await page.mouse.move(640, 500);
      await expectSelectedTab(vuln.summaryTab);
      // The panel is unmounted, not hidden: [CloudAccountDetails].jsx renders each tab body
      // behind its own selectedFilter check.
      await expect(vuln.root).toBeHidden({ timeout: 30000 });
    });

    await test.step("Returning to Vulnerabilities re-renders the same listing", async () => {
      await vuln.vulnerabilitiesTab.click();
      await page.mouse.move(640, 500);
      await expectSelectedTab(vuln.vulnerabilitiesTab);
      await expect(vuln.root).toBeVisible({ timeout: 60000 });
      await vuln.waitForFindings();
      await expect(vuln.rows).toHaveCount(baseline);
    });
  },
);
