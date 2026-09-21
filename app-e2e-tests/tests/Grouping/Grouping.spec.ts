// Not for OSS
import { test, expect } from "@playwright/test";
import {
  deepLinkFirstGroup,
  digitLeadingName,
  expectListingHolds,
  noMatchApplication,
  NO_GROUP_HINT,
  openFirstGroupDetail,
  openTab,
  openUpdateModal,
  uniqueGroupName,
} from "./groupingHelper";

// Application Group detail — the group dashboard at /grouping?groupId=<id>
// (app/src/pages/grouping/index.jsx). Its listing is the Application Grouping tab of
// /dashboards and is covered by tests/ApplicationGroup; that suite asserts only that
// the row link reaches this route, never what the route renders.
//
// Everything here is read-only. The module's only write is the Update Grouping modal,
// which edits a pre-existing group on a shared tenant, and the module has no delete on
// any layer — so the modal is opened, validated and cancelled, never submitted. See
// the PR's Follow-ups.
const SPEC_TIMEOUT_MS = 180000;

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Application Group detail", () => {
  test(
    "Application Group detail sanity - open the first group from the listing, verify Summary, Events, Applications and Monitoring render with Summary selected and Monitoring disabled",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      // Skipped, not failed: this suite creates no groups (the module offers no
      // delete, so anything it created would be permanent on the shared tenant), and
      // a tenant holding none is a missing fixture rather than a product defect.
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await test.step("All four tabs the page declares are rendered", async () => {
        await expect(locators.summaryTab).toBeVisible();
        await expect(locators.eventsTab).toBeVisible();
        await expect(locators.applicationsTab).toBeVisible();
        await expect(locators.monitoringTab).toBeVisible();
      });

      await test.step("Summary owns the selection on arrival", async () => {
        await expect(locators.summaryTab).toHaveAttribute("aria-selected", "true");
        await expect(locators.applicationsTab).toHaveAttribute("aria-selected", "false");
      });

      await test.step("Monitoring is declared disabled and does not take the selection", async () => {
        // tabOptions marks Monitoring disabled:true, which ds/Tabs turns into a
        // MUI-disabled Tab and returns early from onChange, so the click is inert
        // by design rather than merely unstyled.
        await expect(locators.monitoringTab).toBeDisabled();
        await locators.monitoringTab.click({ force: true });
        await expect(locators.monitoringTab).toHaveAttribute("aria-selected", "false");
        await expect(locators.summaryTab).toHaveAttribute("aria-selected", "true");
      });

      await test.step("The edit action is offered alongside the tabs", async () => {
        await expect(locators.editGroupBtn).toBeVisible();
      });
    },
  );

  test(
    "Application Group detail sanity - deep-link a group by its groupId, verify the Application Summary block carries the Applications, Events and Optimizations metrics above the Events/Errors block",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const opened = await deepLinkFirstGroup(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await test.step("The summary settles on either its blocks or its no-data panel", async () => {
        // A group can legitimately map no application, in which case the whole
        // summary is replaced by one panel — so waiting on either is what proves the
        // response landed, rather than a block that was never going to render.
        await expect(locators.applicationSummaryHeading.or(locators.summaryEmptyState).first()).toBeVisible({ timeout: 60000 });
      });

      const isEmpty = await locators.summaryEmptyState.isVisible();
      if (isEmpty) {
        // The empty branch is the assertion here: the page told the user the group
        // maps nothing, rather than rendering blank blocks.
        await expect(locators.applicationSummaryHeading).toBeHidden();
        return;
      }

      await test.step("Both summary blocks and all three metrics are present", async () => {
        await expect(locators.applicationSummaryHeading).toBeVisible();
        await expect(locators.applicationsMetric).toBeVisible();
        await expect(locators.eventsMetric).toBeVisible();
        await expect(locators.optimizationsMetric).toBeVisible();
        await expect(locators.eventsErrorsHeading).toBeVisible();
      });

      await test.step("The Applications metric resolves to a value rather than staying on its skeleton", async () => {
        await expect(locators.metricValue(locators.applicationsMetric)).toHaveText(/\S/, { timeout: 60000 });
      });
    },
  );

  test(
    "Application Group detail - open a group, click the Applications count on Summary, verify the view moves to the Applications tab and lists the group's workloads",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await expect(locators.applicationSummaryHeading.or(locators.summaryEmptyState).first()).toBeVisible({ timeout: 60000 });
      test.skip(await locators.summaryEmptyState.isVisible(), "This group maps no application, so the Summary drill-down has nothing to open.");

      const value = locators.metricValue(locators.applicationsMetric);
      await expect(value).toHaveText(/\S/, { timeout: 60000 });
      const count = Number(((await value.textContent()) ?? "").replace(/[^0-9]/g, ""));

      await value.click();

      if (count > 0) {
        await test.step("The click hands the selection to Applications and its listing renders", async () => {
          // KubernetesApplicationGroupingSummary calls setTab(2) only when the
          // workload count is above zero, so this is the drill-down proper.
          await expect(locators.applicationsTab).toHaveAttribute("aria-selected", "true", { timeout: 60000 });
          await expect(locators.workloadsListing).toBeVisible({ timeout: 60000 });
        });
      } else {
        await test.step("A zero count leaves the selection where it was", async () => {
          // The handler is gated on the same count, so a zero-count click is inert
          // by design — asserting that is what keeps this case honest on an empty group.
          await expect(locators.summaryTab).toHaveAttribute("aria-selected", "true");
          await expect(locators.applicationsTab).toHaveAttribute("aria-selected", "false");
        });
      }
    },
  );

  test(
    "Application Group detail - open a group, select the Applications tab, verify the workloads listing renders either its rows or its No Data Available panel",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await openTab(locators, "applications");

      await test.step("The workloads listing replaces the summary", async () => {
        await expect(locators.workloadsListing).toBeVisible({ timeout: 60000 });
        await expect(locators.applicationSummaryHeading).toBeHidden();
      });

      await test.step("The table reaches a terminal state", async () => {
        await locators.waitForWorkloadsSettled();
        await expect(locators.workloadsRows.first().or(locators.workloadsEmptyState).first()).toBeVisible({ timeout: 60000 });
      });
    },
  );

  test(
    "Application Group detail - open the Applications tab, search for an application name nothing can match, verify the workloads table empties and shows the No Data Available panel",
    { tag: ["@dev", "@regression", "@search", "@negative"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await openTab(locators, "applications");
      await locators.waitForWorkloadsSettled();
      await expect(locators.workloadsSearch).toBeVisible({ timeout: 60000 });

      await locators.searchWorkloads(noMatchApplication());

      await test.step("Nothing survives the filter", async () => {
        // Retrying assertions, not a snapshot count: the search refetches, and
        // reading the table once would race the in-flight request and see the
        // pre-search rows.
        await expect(locators.workloadsRows).toHaveCount(0);
        await expect(locators.workloadsEmptyState).toBeVisible({ timeout: 60000 });
      });
    },
  );

  test(
    "Application Group detail - open a group, select the Events tab, verify the events listing replaces the summary blocks",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await openTab(locators, "events");

      await test.step("Events owns the page", async () => {
        await expect(locators.eventsListing).toBeVisible({ timeout: 60000 });
        await expect(locators.applicationSummaryHeading).toBeHidden();
        await expect(locators.workloadsListing).toBeHidden();
      });
    },
  );

  test(
    "Application Group detail - open a group, move to the Events tab and back to Summary, verify the Application Summary block returns and Events is no longer selected",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await expect(locators.applicationSummaryHeading.or(locators.summaryEmptyState).first()).toBeVisible({ timeout: 60000 });

      await openTab(locators, "events");
      await expect(locators.eventsListing).toBeVisible({ timeout: 60000 });

      await openTab(locators, "summary");

      await test.step("The summary is re-rendered, not left behind on the events listing", async () => {
        await expect(locators.applicationSummaryHeading.or(locators.summaryEmptyState).first()).toBeVisible({ timeout: 60000 });
        await expect(locators.eventsListing).toBeHidden();
        await expect(locators.eventsTab).toHaveAttribute("aria-selected", "false");
      });
    },
  );

  test(
    "Application Group detail - open Edit Application Group, verify the Update Grouping modal opens carrying the group's own name and its Cancel and Update actions",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await openUpdateModal(locators);

      await test.step("The form is bound to the group that was opened", async () => {
        // The title alone would not prove binding — an update modal that failed to
        // load the record would still say "Update Grouping" over an empty form.
        await expect(locators.nameInput).toHaveValue(opened!.groupName, { timeout: 60000 });
      });

      await test.step("Both actions are offered, and the primary one reads Update rather than Create", async () => {
        await expect(locators.dialogCancelBtn).toBeVisible();
        await expect(locators.dialogUpdateBtn).toBeVisible();
        await expect(locators.dialogCreateBtn).toBeHidden();
      });
    },
  );

  test(
    "Application Group detail - open Edit Application Group, clear the Grouping Name, verify the This field required error, then enter a name starting with a digit, verify the Should start with an alphabet error",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;

      await openUpdateModal(locators);
      await expect(locators.nameInput).toHaveValue(opened!.groupName, { timeout: 60000 });

      await test.step("An empty name is rejected as required", async () => {
        // Validation runs on the Input's own onChange, so the error appears without
        // submitting — which is what keeps this test off the write path entirely.
        await locators.nameInput.fill("");
        await expect(locators.nameError).toHaveText("This field required");
      });

      await test.step("A name starting with a digit is rejected for its first character", async () => {
        await locators.nameInput.fill(digitLeadingName());
        await expect(locators.nameError).toHaveText("Should start with an alphabet");
      });

      await test.step("A valid name clears the error", async () => {
        await locators.nameInput.fill(uniqueGroupName());
        await expect(locators.nameError).toBeHidden();
      });

      await locators.dialogCancelBtn.click();
      await expect(locators.dialog).toBeHidden({ timeout: 30000 });
    },
  );

  test(
    "Application Group detail - open Edit Application Group, type a new name, cancel the modal, verify the group still carries its original name on the detail view and in the listing",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const opened = await openFirstGroupDetail(page);
      test.skip(opened === null, NO_GROUP_HINT);
      const locators = opened!.locators;
      const originalName = opened!.groupName;
      const typedName = uniqueGroupName();

      await openUpdateModal(locators);
      await locators.nameInput.fill(typedName);
      await expect(locators.nameInput).toHaveValue(typedName);

      await test.step("Cancel closes the modal", async () => {
        await locators.dialogCancelBtn.click();
        await expect(locators.dialog).toBeHidden({ timeout: 30000 });
      });

      await test.step("Reopening the form shows the stored name, not the typed one", async () => {
        // clearAllAndClose resets the local state, so this also proves the cancel
        // discarded the edit rather than merely hiding the dialog.
        await openUpdateModal(locators);
        await expect(locators.nameInput).toHaveValue(originalName, { timeout: 60000 });
        await locators.dialogCancelBtn.click();
        await expect(locators.dialog).toBeHidden({ timeout: 30000 });
      });

      await test.step("The listing is unchanged too", async () => {
        // The modal's own state could be reset while a write still landed, so the
        // listing is re-read from the server rather than trusted from this page.
        await expectListingHolds(page, originalName, typedName);
      });
    },
  );
});
