// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openAutomations,
  expectAutomationsTabSelected,
  expectListingSettled,
  searchByName,
  clearNameSearch,
  pickFilterOption,
  listedNames,
  columnTexts,
  chooseRowMenuItem,
  deleteAutomationByName,
  leaveBuilder,
  countNamed,
  openConfigsModal,
  NO_MATCH_TERM,
} from "./automationsHelper";

// Automations tab of /automation — app/src/components/workflow/WorkflowListing.tsx.
// Sibling of tests/workflow/TaskRunner, which covers the third tab of the same page.
//
// This drives a shared dev tenant, so the only automation these tests create is a
// duplicate of a manual-trigger automation, deleted again in the same test. Nothing
// pre-existing is renamed, paused, activated or run: Manual run and the state toggles
// all have real side effects on a fleet other people are using.

// The listing has no automations to act on if the tenant holds none, and every test
// below would then fail on a missing row rather than on the behaviour it covers.
const NO_AUTOMATIONS_HINT =
  "The Automations listing rendered no rows. These tests read and duplicate existing " +
  "automations, so the tenant needs at least one before this suite can run.";

const MANUAL_ONLY_HINT =
  "No Manual-trigger automation is listed. The duplicate test deliberately copies a " +
  "manual automation so the copy cannot fire on a schedule while it exists.";

test(
  "Automations sanity - open Automation from the side nav, land on the Automations tab, verify the listing toolbar and the automations table render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(150000);

    const locators = await openAutomations(page);

    await test.step("The Automations tab is the one selected", async () => {
      await expectAutomationsTabSelected(locators);
      await expect(locators.executionsTab).toBeVisible();
    });

    await test.step("The toolbar exposes both searches and all four filters", async () => {
      await expect(locators.nameSearch).toBeVisible();
      await expect(locators.tagsSearch).toBeVisible();
      await expect(locators.accountFilter).toBeVisible();
      await expect(locators.statusFilter).toBeVisible();
      await expect(locators.lastExecutionStatusFilter).toBeVisible();
      await expect(locators.triggerTypeFilter).toBeVisible();
      await expect(locators.refreshBtn).toBeVisible();
      await expect(locators.configsBtn).toBeVisible();
      await expect(locators.createBtn).toBeVisible();
    });

    await test.step("The table carries the six named columns from tableHeaders", async () => {
      // arrayContaining, not toEqual: the seventh header is the actions column,
      // which tableHeaders declares with an empty name.
      await expect
        .poll(async () => locators.headerNames(), { timeout: 30000 })
        .toEqual(expect.arrayContaining(["Name", "Account", "Last Execution", "Trigger Type", "Tags", "Status"]));
    });

    await test.step("The listing reports its row range", async () => {
      await expect(locators.rowRangeSummary).toBeVisible({ timeout: 30000 });
    });
  }
);

test(
  "Automations - search an existing automation by name, press Enter, verify only automations whose name contains the term are listed",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await expect(locators.nameLinks.first(), NO_AUTOMATIONS_HINT).toBeVisible({
      timeout: 60000,
    });
    const firstName = (await locators.nameLinks.first().innerText()).trim();
    expect(firstName).not.toEqual("");

    // A prefix rather than the whole name: it proves the search matches on a
    // substring instead of only echoing back an exact-name lookup.
    const term = firstName.slice(0, Math.min(firstName.length, 6)).trim();
    expect(term).not.toEqual("");
    await searchByName(locators, term);

    await test.step("Every row that came back carries the term", async () => {
      // Polls rather than reading once: the search refetches, and a single read can
      // still see the pre-search rows.
      await expect
        .poll(async () => (await listedNames(locators)).filter((name) => !name.toLowerCase().includes(term.toLowerCase())), { timeout: 30000 })
        .toEqual([]);
    });

    await test.step("The automation the term came from is one of them", async () => {
      await expect(locators.nameLink(firstName).first()).toBeVisible({
        timeout: 30000,
      });
    });
  }
);

test(
  "Automations - search a name no automation can match, press Enter, verify the No Data Available empty state, then clear the search and verify the listing returns",
  { tag: ["@dev", "@regression", "@search", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await expect(locators.nameLinks.first(), NO_AUTOMATIONS_HINT).toBeVisible({
      timeout: 60000,
    });
    const baseline = await locators.nameLinks.count();
    expect(baseline).toBeGreaterThan(0);

    await test.step("The unmatchable term empties the table", async () => {
      await searchByName(locators, NO_MATCH_TERM);
      await expect(locators.nameLinks).toHaveCount(0, { timeout: 30000 });
      await expect(locators.emptyState).toBeVisible({ timeout: 30000 });
      // The row-range line is deliberately not asserted here: CustomTable's
      // renderPaginationOrViewAll returns null at zero rows, so the summary —
      // and its "No results found" text — is unmounted in exactly this state.
      await expect(locators.rowRangeSummary).toHaveCount(0);
    });

    await test.step("Clearing the search restores the original listing", async () => {
      await clearNameSearch(locators);
      // Waits for the restored count rather than counting what is on screen now: the
      // table still holds the empty result from the step above.
      await expect(locators.nameLinks).toHaveCount(baseline, {
        timeout: 30000,
      });
      await expect(locators.emptyState).toBeHidden({ timeout: 30000 });
    });
  }
);

test(
  "Automations - filter Trigger Type by Manual, verify every listed automation shows Manual in its Trigger Type column",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await pickFilterOption(page, locators, locators.triggerTypeFilter, "Manual");

    await test.step("No row is left showing a trigger other than Manual", async () => {
      await expect
        .poll(
          async () => {
            const cells = await columnTexts(locators, "Trigger Type");
            // Contains, not equals: an automation can carry several triggers, and a
            // Manual+Schedule row is a legitimate match. A row with no trigger at all
            // renders "-", which this still rejects.
            return cells.filter((cell) => !cell.includes("Manual"));
          },
          { timeout: 30000 }
        )
        .toEqual([]);
    });

    await test.step("The filter narrowed to a real set rather than emptying the table", async () => {
      await expect(locators.nameLinks.first(), MANUAL_ONLY_HINT).toBeVisible({
        timeout: 30000,
      });
    });
  }
);

test(
  "Automations - filter Status by Paused, verify every listed automation reads Paused in its Status column",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await pickFilterOption(page, locators, locators.statusFilter, "Paused");
    await expectListingSettled(locators);

    await test.step("Either every row reads Paused, or the filter emptied the table", async () => {
      // The dev tenant may legitimately hold no paused automation, and an empty
      // result is then the correct outcome of the filter — but a row showing any
      // other status never is, which is what this asserts either way. The Status
      // cell also carries the live-version line, so the match is on the status word.
      await expect
        .poll(
          async () => {
            // Case-insensitive: the app renders the status lowercase and capitalises
            // it with CSS, so the rendered text depends on text-transform resolving.
            const cells = await columnTexts(locators, "Status");
            return cells.filter((cell) => !cell.toLowerCase().startsWith("paused"));
          },
          { timeout: 30000 }
        )
        .toEqual([]);
    });
  }
);

test(
  "Automations - open the three-dot menu on an automation, choose Delete, cancel the confirmation, verify the automation is still listed",
  { tag: ["@dev", "@regression", "@negative", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await expect(locators.nameLinks.first(), NO_AUTOMATIONS_HINT).toBeVisible({
      timeout: 60000,
    });
    const targetName = (await locators.nameLinks.first().innerText()).trim();
    const row = locators.rowFor(targetName);

    await chooseRowMenuItem(locators, row, "delete", "Delete");

    await test.step("The confirmation names the automation and warns the delete is final", async () => {
      await expect(page.getByText(`Delete Automation "${targetName}"`)).toBeVisible({ timeout: 15000 });
      await expect(page.getByText("Are you sure you want to delete this automation? This action cannot be undone.")).toBeVisible();
      await expect(locators.deleteModalConfirmBtn).toBeVisible();
    });

    await test.step("Cancel closes it and deletes nothing", async () => {
      // Deliberately never clicks #workflow-delete-confirm-btn: this automation
      // belongs to the shared dev tenant, not to the test.
      await locators.deleteModalCancelBtn.click();
      await expect(locators.deleteModalConfirmBtn).toBeHidden({
        timeout: 15000,
      });
      await expect(locators.toast(/deleted successfully/i)).toHaveCount(0);
      // Re-read from the server rather than trusting the table the modal opened
      // over: only a fresh listing proves the row is still there.
      await searchByName(locators, targetName);
      await expect(locators.nameLink(targetName).first()).toBeVisible({
        timeout: 30000,
      });
    });
  }
);

test(
  'Automations - duplicate a Manual automation, verify "Copy of <name>" is returned by a fresh search, delete the copy, verify it is gone',
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    test.setTimeout(240000);

    const locators = await openAutomations(page);

    // A manual-trigger source on purpose: the copy is created ACTIVE, and a copy of a
    // scheduled automation would start firing against the shared dev fleet.
    await pickFilterOption(page, locators, locators.triggerTypeFilter, "Manual");
    await expect(locators.nameLinks.first(), MANUAL_ONLY_HINT).toBeVisible({
      timeout: 30000,
    });

    const sourceName = (await locators.nameLinks.first().innerText()).trim();
    const copyName = `Copy of ${sourceName}`;

    await searchByName(locators, copyName);
    // Counted rather than assumed to be zero: a copy left behind by an interrupted
    // earlier run must not make this test fail, and the delta below still proves
    // exactly one new record was created and removed.
    const copiesBefore = await countNamed(locators, copyName);

    await clearNameSearch(locators);
    await searchByName(locators, sourceName);
    const sourceRow = locators.rowFor(sourceName);
    await expect(sourceRow).toBeVisible({ timeout: 30000 });

    try {
      await chooseRowMenuItem(locators, sourceRow, "duplicate", "Duplicate");

      await test.step("The copy exists on the server, not just in a snackbar", async () => {
        // The snackbar is not the assertion — this searches again, which refetches
        // the listing from workflow_list, and counts what the server returns.
        await searchByName(locators, copyName);
        await expect.poll(async () => countNamed(locators, copyName), { timeout: 60000 }).toEqual(copiesBefore + 1);
        await expect(locators.nameLink(copyName).first()).toBeVisible({
          timeout: 30000,
        });
      });
    } finally {
      // Cleanup runs even when the assertion above fails, so a red test does not
      // leave an automation behind on a shared tenant. Guarded on the copy count
      // having actually gone up: when the duplicate never happened there is
      // nothing to remove, and deleting blindly would replace the real failure
      // with a "row not found" from the cleanup.
      await searchByName(locators, copyName);
      if ((await countNamed(locators, copyName)) > copiesBefore) {
        await deleteAutomationByName(locators, copyName);
      }
    }

    await test.step("The copy is gone from a fresh listing", async () => {
      await searchByName(locators, copyName);
      await expect.poll(async () => countNamed(locators, copyName), { timeout: 60000 }).toEqual(copiesBefore);
    });
  }
);

test(
  "Automations - open an automation from the listing, verify the builder opens on that automation, go back, verify the listing returns",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await expect(locators.nameLinks.first(), NO_AUTOMATIONS_HINT).toBeVisible({
      timeout: 60000,
    });
    const targetName = (await locators.nameLinks.first().innerText()).trim();

    await locators.nameLink(targetName).first().click();

    await test.step("The automation's own page opens with its name in the header", async () => {
      // toHaveURL, not waitForURL: the header's Back is a router.push, a
      // same-document navigation that fires no load event, so waitForURL's
      // default waitUntil:"load" hangs there. Both directions use the polling
      // form so neither depends on how the app happens to navigate.
      await expect(page).toHaveURL(/\/automation\/[0-9a-fA-F-]{8,}/, { timeout: 60000 });
      await expect(page.getByRole("heading", { name: targetName })).toBeVisible({ timeout: 60000 });
      await expect(locators.builderBackBtn).toBeVisible();
    });

    await test.step("Back returns to the Automations listing", async () => {
      await leaveBuilder(page, locators);
      await expect(page).toHaveURL(/\/automation(\?|#|$)/, { timeout: 60000 });
      await expectAutomationsTabSelected(locators);
      await expect(locators.listingBox).toBeVisible({ timeout: 30000 });
      await expectListingSettled(locators);
    });
  }
);

test(
  "Automations - open Configs, add a configuration, leave Key and Value empty and enter invalid JSON metadata, verify each field names its own rejection and Save stays disabled",
  { tag: ["@dev", "@regression", "@validation", "@negative"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);
    await openConfigsModal(locators);

    await locators.addConfigBtn.click();
    await expect(locators.configFormModal).toBeVisible({ timeout: 30000 });

    await test.step("An untouched form names both required fields and refuses to save", async () => {
      await expect(locators.configKeyError).toHaveText("Key is required", {
        timeout: 15000,
      });
      await expect(locators.configValueError).toHaveText("Value is required");
      await expect(locators.saveConfigBtn).toBeDisabled();
    });

    // Unique per run so that, if a later change ever lets this reach the server, it
    // cannot collide with a real config. Nothing here is saved — Cancel closes it.
    const probeKey = `e2e-automations-probe-${Date.now()}`;

    await test.step("Filling Key and Value clears both rejections and enables Save", async () => {
      await locators.configKeyInput.fill(probeKey);
      await expect(locators.configKeyInput).toHaveValue(probeKey);
      await locators.configValueInput.fill("e2e-probe-value");
      await expect(locators.configValueInput).toHaveValue("e2e-probe-value");

      await expect(locators.configKeyError).toHaveCount(0);
      await expect(locators.configValueError).toHaveCount(0);
      await expect(locators.saveConfigBtn).toBeEnabled();
    });

    await test.step("Metadata that is not JSON is rejected as Invalid JSON format and disables Save again", async () => {
      await locators.configMetadataInput.fill("{ not valid json");
      await expect(locators.configMetadataInput).toHaveValue("{ not valid json");
      await expect(locators.configMetadataError).toHaveText("Invalid JSON format", { timeout: 15000 });
      await expect(locators.saveConfigBtn).toBeDisabled();
    });

    await test.step("Cancel closes the form and saves nothing", async () => {
      await locators.cancelConfigBtn.click();
      await expect(locators.configFormModal).toBeHidden({ timeout: 15000 });
      await expect(locators.toast(/Configuration created successfully/i)).toHaveCount(0);
      // The list the form returns to is the proof: an unsaved key is not in it.
      await expect(locators.configsModal).toBeVisible({ timeout: 15000 });
      await expect(locators.configsModal.getByText(probeKey, { exact: true })).toHaveCount(0);
    });
  }
);

test(
  "Automations - switch to the Executions tab and back to Automations, verify each tab renders its own listing",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);

    const locators = await openAutomations(page);

    await test.step("Executions replaces the automations listing with the execution dashboard", async () => {
      await locators.executionsTab.click();
      await page.mouse.move(640, 500);
      await expect(locators.executionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 15000 });
      await expect(locators.executionsRoot).toBeVisible({ timeout: 60000 });
      await expect(locators.listingBox).toBeHidden({ timeout: 30000 });
    });

    await test.step("Automations brings the automations listing back", async () => {
      await locators.automationsTab.click();
      await page.mouse.move(640, 500);
      await expectAutomationsTabSelected(locators);
      await expect(locators.listingBox).toBeVisible({ timeout: 60000 });
      await expect(locators.executionsRoot).toBeHidden({ timeout: 30000 });
      await expectListingSettled(locators);
    });
  }
);
