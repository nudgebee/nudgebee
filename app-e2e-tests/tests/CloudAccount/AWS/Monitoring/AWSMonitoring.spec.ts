// Not for OSS
import { test, expect } from "@playwright/test";
import { openMonitoringSubTab, switchMonitoringSubTab, alertRowCount, MONITORING_TIMEOUT_MS } from "./awsMonitoringHelper";
import {
  ALERT_HEADERS,
  ALERT_STATUS_OPTIONS,
  AWS_DEFAULT_LOG_QUERY,
  LOG_LIMIT_OPTIONS,
} from "./awsMonitoringLocators";

// Cloud Account > AWS > Monitoring — Alert Manager, Cloud Logs and Cloud Metrics
// (app/src/pages/cloud-account/details/[CloudAccountDetails].jsx:653-657).
//
// Read-only by construction. The module's only write action is the row menu's
// Enable/Disable toggle, which flips a rule on an account other suites share, so that
// flow is opened and cancelled but never confirmed. Create New Alert is shipped
// disabled (CloudAccountAlertManager.tsx:303), so the module offers no create journey
// to cover at all — see the PR's Follow-ups.

// A name no alert rule can carry, so the empty-listing assertion is about the search
// filtering rather than about what the dev account happens to hold.
const NO_MATCH_NAME = `zz-no-such-alert-${Date.now()}`;

test(
  "Cloud Monitoring sanity - open AWS Monitoring, verify the Alert Manager listing renders its four filters, name search and column headers",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");

    await test.step("The four toolbar filters are offered", async () => {
      for (const key of ["category", "severity", "source", "status"] as const) {
        await expect(m.alertFilterTrigger(key), `the ${key} filter should render`).toBeVisible();
      }
    });

    await test.step("The name search is present and empty", async () => {
      await expect(m.alertNameSearch).toBeVisible();
      await expect(m.alertNameSearch).toHaveValue("");
    });

    await test.step("The listing declares its column contract", async () => {
      // Column names from CloudAccountAlertManager.tsx's headers array. The seventh
      // header is deliberately blank (the row-menu column), so it is not asserted.
      for (const header of ALERT_HEADERS) {
        await expect(m.alertManagerRoot.locator("th", { hasText: header }).first()).toBeVisible();
      }
    });
  }
);

test(
  "Cloud Monitoring - filter the alert rules by Status Enabled, verify every listed rule reports Enabled",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");

    const baseline = await alertRowCount(m);
    test.skip(baseline === 0, "the dev AWS account has no alert rules, so there is no row for the filter to keep or drop");

    await m.chooseFilterOption(m.alertFilterTrigger("status"), ALERT_STATUS_OPTIONS[0]);

    // Polled rather than read once: committing the filter refetches getEventRules, and a
    // single read would race the in-flight request and grade the pre-filter rows.
    //
    // The empty branch requires the empty state to be on screen, not merely zero rows.
    // listAlertManager calls setData([]) before every fetch, so "no rows" is also what a
    // refetch in flight looks like — accepting a bare count of 0 would let this poll
    // succeed mid-refetch without ever grading a row.
    await expect
      .poll(
        async () => {
          const statuses = await m.alertColumnCells("status").allTextContents();
          if (statuses.length === 0) {
            return m.alertEmptyState.isVisible();
          }
          return statuses.every((s) => s.trim() === "Enabled");
        },
        { message: "every row left after the Enabled filter should report Enabled" }
      )
      .toBe(true);

    // The filter is a narrowing, so it can never return more rows than the unfiltered list.
    expect(await m.alertRows.count()).toBeLessThanOrEqual(baseline);
  }
);

test(
  "Cloud Monitoring - search the alert rules for a name no rule can match, verify the listing empties and clearing the search restores it",
  { tag: ["@dev", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");

    const baseline = await alertRowCount(m);

    await test.step("An unmatchable name empties the table", async () => {
      await m.alertNameSearch.fill(NO_MATCH_NAME);
      // The listing refetches on Enter, not on keystroke (CloudAccountAlertManager.tsx:362).
      await m.alertNameSearch.press("Enter");
      // The empty state first, then the count. CustomTable withholds it while `loading`,
      // so it proves the search actually came back empty rather than catching the
      // setData([]) blank that every refetch passes through on its way.
      await expect(m.alertEmptyState).toBeVisible();
      await expect(m.alertRows).toHaveCount(0);
    });

    await test.step("Emptying the search restores the original listing", async () => {
      // Blanking the field is its own reset path: onChange clears searchByName when the
      // value goes from non-empty to empty, without needing a second Enter.
      await m.alertNameSearch.fill("");
      await expect(m.alertNameSearch).toHaveValue("");
      // Waits for the restored count rather than snapshotting a table that is still
      // showing the empty result from the step above.
      await expect(m.alertRows).toHaveCount(baseline);
    });
  }
);

test(
  "Cloud Monitoring - search the alert rules for an existing rule name, verify only that rule stays listed",
  { tag: ["@dev", "@regression", "@search"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");

    const baseline = await alertRowCount(m);
    test.skip(baseline === 0, "the dev AWS account has no alert rules, so there is no existing name to search for");

    const name = (await m.alertCell(0, "name").textContent())?.trim() ?? "";
    expect(name, "the first rule should render a name to search for").not.toBe("");

    await m.alertNameSearch.fill(name);
    await m.alertNameSearch.press("Enter");

    // The search is a server-side contains filter, so it can legitimately return
    // sibling rules whose names embed this one — every surviving row carrying the term
    // is the assertion, not a row count of exactly one.
    await expect
      .poll(
        async () => {
          const names = await m.alertColumnCells("name").allTextContents();
          return names.length > 0 && names.every((n) => n.toLowerCase().includes(name.toLowerCase()));
        },
        { message: `every row left after searching '${name}' should carry that name` }
      )
      .toBe(true);
  }
);

test(
  "Cloud Monitoring - open a rule's row menu and cancel the enable/disable confirmation, verify the rule keeps its original status",
  { tag: ["@dev", "@regression", "@negative", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");

    const baseline = await alertRowCount(m);
    test.skip(baseline === 0, "the dev AWS account has no alert rules, so there is no row menu to open");

    const statusBefore = (await m.alertCell(0, "status").textContent())?.trim() ?? "";
    expect(statusBefore, "the first rule should report a status before the dialog opens").toMatch(/^(Enabled|Disabled)$/);

    await m.alertRowMenuTrigger(0).click();

    // The menu is gated on write access (getMenuItems returns [] without it), so an
    // absent toggle means the run's user cannot administer rules rather than a bad locator.
    const toggle = m.rowMenuItem("toggle-enabled", /^(Enable|Disable)$/);
    await expect(toggle, "the row menu should offer Enable/Disable — needs write access on the account").toBeVisible({
      timeout: 15000,
    });
    await toggle.click();

    await test.step("The confirmation names the rule it would change", async () => {
      await expect(m.confirmDialog).toBeVisible({ timeout: 15000 });
      await expect(m.confirmDialogTitle).toContainText(/^(Disable|Enable) the alert/);
      await expect(m.submitBtn).toBeVisible();
    });

    await test.step("Cancel closes it and changes nothing", async () => {
      // Deliberately never clicks #submit: confirming calls disableAlertManager and
      // flips a rule on an account every other AWS suite reads.
      await m.cancelBtn.click();
      await expect(m.confirmDialogTitle).toHaveCount(0, { timeout: 15000 });
      // No success snackbar means no mutation was issued (the handler raises one on
      // either outcome, CloudAccountAlertManager.tsx:251-253).
      await expect(page.getByText(/Rule .* (Enabled|Disabled) Successful/)).toHaveCount(0);
      // The row still reports what it reported before the dialog opened.
      await expect(m.alertCell(0, "status")).toHaveText(statusBefore);
    });
  }
);

test(
  "Cloud Monitoring sanity - open Cloud Logs, verify the region, log group and limit filters render with the prefilled CloudWatch query and the no-entries empty state",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "cloud-logs");

    await test.step("The AWS query filters are offered", async () => {
      await expect(m.logsFilterTrigger("cloud-logs-aws-region", "Region")).toBeVisible();
      await expect(m.logsFilterTrigger("cloud-logs-aws-log-group", "Log Group")).toBeVisible();
      await expect(m.logsFilterTrigger("cloud-logs-limit", "Limit")).toBeVisible();
      await expect(m.logsRunQueryBtn).toBeVisible();
    });

    await test.step("The query box is prefilled with the AWS default", async () => {
      await expect(m.logsQueryTextarea).toHaveValue(AWS_DEFAULT_LOG_QUERY);
    });

    await test.step("Nothing has been run yet, so the panel explains what to do", async () => {
      const empty = m.emptyStateIn(m.cloudLogsRoot);
      await expect(empty).toBeVisible();
      await expect(empty).toContainText("No log entries");
      await expect(m.logRows).toHaveCount(0);
    });
  }
);

test(
  "Cloud Monitoring - run the Cloud Logs query with no log group selected, verify the run is rejected with the 'Please select a log group' banner",
  { tag: ["@dev", "@regression", "@validation", "@negative"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "cloud-logs");

    // Log Group is never auto-selected on AWS — CloudLogsQueryPanel resets it to null
    // whenever the region changes and offers no default — so a freshly opened tab is
    // exactly the unselected state this refusal guards.
    await expect(m.logsFilterTrigger("cloud-logs-aws-log-group", "Log Group")).toBeVisible();
    await m.logsRunQueryBtn.click();

    const banner = m.bannerIn(m.cloudLogsRoot);
    await expect(banner).toBeVisible({ timeout: 20000 });
    await expect(banner).toContainText("Please select a log group");
    await expect(m.logRows).toHaveCount(0);
  }
);

test(
  "Cloud Monitoring - change the Cloud Logs row limit to 500, verify the Limit filter commits the new value",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "cloud-logs");

    const limit = m.logsFilterTrigger("cloud-logs-limit", "Limit");
    // 100 is the component's initial logLimit (CloudLogsViewer.tsx:84).
    await expect(limit).toContainText("100");

    await m.chooseFilterOption(limit, "500");

    // The committed value replaces the old one on the trigger rather than joining it.
    await expect(limit).toContainText("500");
    await expect(limit).not.toContainText("100");
  }
);

test(
  "Cloud Monitoring - run the Cloud Metrics query with no resource selected, verify the run is rejected with a 'Please select' banner naming what is missing",
  { tag: ["@dev", "@regression", "@validation", "@negative"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "metrics");

    await expect(m.metricsRunQueryBtn).toBeVisible();
    await m.metricsRunQueryBtn.click();

    // Which of the two refusals fires depends on the account, not on the test: the panel
    // auto-selects a region only when the account has exactly one, and never auto-selects
    // a resource. Metrics are auto-filled from the chosen resource, so the third refusal
    // ('at least one metric') is unreachable from a freshly opened tab.
    const banner = m.bannerIn(m.cloudMetricsRoot);
    await expect(banner).toBeVisible({ timeout: 20000 });
    await expect(banner).toHaveText(/^Please select (a region|at least one resource)$/);
  }
);

test(
  "Cloud Monitoring - move from Alert Manager through Cloud Logs to Cloud Metrics and back, verify each sub-tab lands on its own URL fragment and panel",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(MONITORING_TIMEOUT_MS);
    const m = await openMonitoringSubTab(page, "alert-manager");
    await expect(page).toHaveURL(m.MonitoringAlertManagerUrl);

    await test.step("Cloud Logs replaces the alert listing", async () => {
      await switchMonitoringSubTab(m, page, m.MonitoringCloudLogs, m.MonitoringCloudLogsUrl, m.cloudLogsRoot);
      await expect(page).toHaveURL(m.MonitoringCloudLogsUrl);
      // Each sub-tab is a distinct ListingLayout node, so the previous panel being gone
      // is what proves the switch rather than a restyle of one shared panel.
      await expect(m.alertManagerRoot).toHaveCount(0);
    });

    await test.step("Cloud Metrics replaces Cloud Logs", async () => {
      await switchMonitoringSubTab(m, page, m.MonitoringCloudMetrics, m.MonitoringCloudMetricsUrl, m.cloudMetricsRoot);
      await expect(page).toHaveURL(m.MonitoringCloudMetricsUrl);
      await expect(m.cloudLogsRoot).toHaveCount(0);
    });

    await test.step("The anchor flyout returns to Alert Manager", async () => {
      await switchMonitoringSubTab(m, page, m.MonitoringAlertManager, m.MonitoringAlertManagerUrl, m.alertManagerRoot);
      await expect(page).toHaveURL(m.MonitoringAlertManagerUrl);
      await expect(m.cloudMetricsRoot).toHaveCount(0);
      await expect(m.alertNameSearch).toBeVisible();
    });
  }
);
