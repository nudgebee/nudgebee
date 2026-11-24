import { test, expect } from "@playwright/test";
import { openRdsSubTab, openInstances, firstEnabledLifecycleAction, isMenuItemDisabled } from "./rdsHelper";
import { stateOptionLabel } from "../EC2/ec2Constants";
import { DRILLDOWN_TABS, INSTANCE_COLUMNS, LIFECYCLE_ACTION_LABEL, SEARCH_PLACEHOLDER, STRICT_CONFIRM_ACTIONS } from "./rdsConstants";

// Infra > Cloud > AWS > RDS — the resource tab rendered by
// app/src/components/cloudaccount/rds/{Summary,Instances}.tsx.
//
// The dev AWS account is shared, so everything here is read-only: no database instance
// is started, stopped or rebooted, and nothing is created, renamed or deleted. The one
// write path the module offers is exercised as far as its confirmation dialog and then
// cancelled.

// A term no instance name can match, so the "no results" assertions are about the filter
// working rather than about what the dev account happens to hold.
const NO_MATCH_TERM = `zz-no-such-database-${Date.now()}`;

// Named so a skip reads as the environment precondition it is rather than as a gap in
// coverage: several journeys below need at least one RDS row to act on.
const NO_INSTANCES_HINT =
  "The AWS account holds no RDS instances, so there is no row to expand or act on. " +
  "Point AWS_CLUSTER_NAME at an account with at least one database instance to cover this journey.";

test("RDS sanity - open the AWS account, hover the RDS tab, open Summary, verify the instance, alarm and optimization tiles render their counts", { tag: ["@dev", "@sanity", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const rds = await openRdsSubTab(page, "summary");

  await test.step("The three headline tiles are present", async () => {
    for (const tile of [rds.statTotalInstances, rds.statFiredAlarmCount, rds.statOptimizeCount]) {
      await expect(tile).toBeVisible({ timeout: 60000 });
    }
  });

  await test.step("Each tile settles on a value rather than staying on its skeleton", async () => {
    // ds/Stat renders a placeholder "-" for a missing or zero count — a normal state for
    // a small dev account. Anchoring on the tile's own label and requiring either a
    // number or that placeholder is what separates "rendered" from "still loading",
    // which is what this step guards. Unlike EC2, the RDS alarm tile carries no
    // "last 7 days" qualifier (Summary.tsx:420-425), so it is matched the same way as
    // the other two rather than on that copy.
    await expect(rds.statTotalInstances).toHaveText(/^Total Instances\s*(?:[\d,]+|-)$/);
    await expect(rds.statFiredAlarmCount).toHaveText(/^Fired Alarm Count\s*(?:[\d,]+|-)$/);
    await expect(rds.statOptimizeCount).toHaveText(/^Optimize Count\s*(?:[\d,]+|-)$/);
  });

  await test.step("The metrics card renders below the tiles", async () => {
    await expect(rds.metricsCard).toBeVisible({ timeout: 60000 });
    await expect(rds.metricsCard).toContainText("Metrics");
  });
});

test("RDS sanity - open the RDS Instances sub-tab, verify the listing renders its eight named columns and the search, state, region and tag filters", { tag: ["@dev", "@sanity", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount } = await openInstances(page);

  await test.step("The toolbar exposes the search box and all four filters", async () => {
    await expect(rds.instancesSearch).toBeVisible();
    await expect(rds.instancesSearch).toHaveAttribute("placeholder", SEARCH_PLACEHOLDER);
    await expect(rds.filterTrigger("state")).toBeVisible();
    await expect(rds.filterTrigger("region")).toBeVisible();
    await expect(rds.filterTrigger("tagKey")).toBeVisible();
    await expect(rds.filterTrigger("tagValue")).toBeVisible();
    await expect(rds.refreshButton).toBeVisible();
    await expect(rds.downloadButton).toBeVisible();
  });

  await test.step("The column contract from INSTANCE_HEADER is on screen", async () => {
    if (rowCount === 0) {
      // With no rows CustomTable replaces the whole table with its empty panel, so
      // there are no column headers to assert — the panel is the contract instead.
      await expect(rds.instancesEmpty).toBeVisible();
      return;
    }
    for (const column of INSTANCE_COLUMNS) {
      await expect(rds.columnHeader(column)).toBeVisible();
    }
  });
});

test("RDS - open Instances, search an instance name that cannot exist, verify the listing empties to the No Data Available panel", { tag: ["@dev", "@regression", "@search", "@negative", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds } = await openInstances(page);

  await rds.searchAndApply(NO_MATCH_TERM);

  // The empty panel is asserted first, deliberately. While `loading` is true CustomTable
  // unmounts the id'd <tbody> and swaps in an id-less skeleton one, so a row count taken
  // straight after the search reads 0 whatever the response holds — it would pass before
  // the request even landed. renderEmptyState bails out while loading, so the panel
  // appearing is the signal the response has landed, and only after it does the count
  // below mean anything.
  await expect(rds.instancesEmpty).toBeVisible();
  await expect(rds.instancesRows).toHaveCount(0);
  await expect(rds.instancesSearch).toHaveValue(NO_MATCH_TERM);
});

test("RDS - open Instances, search a term that matches nothing, clear the search, verify the original instance count comes back", { tag: ["@dev", "@regression", "@search", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount: baseline } = await openInstances(page);
  // With no rows to start from, both counts below are 0 and every assertion in this test
  // passes without the search or the clear having done anything — so an empty account
  // makes the case vacuous rather than merely thin.
  test.skip(baseline === 0, NO_INSTANCES_HINT);

  await test.step("The unmatchable term empties the listing", async () => {
    await rds.searchAndApply(NO_MATCH_TERM);
    // Empty panel first, then the count — see the note in the no-match test above.
    await expect(rds.instancesEmpty).toBeVisible();
    await expect(rds.instancesRows).toHaveCount(0);
  });

  await test.step("Clearing the search restores the original listing", async () => {
    await rds.clearSearch();
    await expect(rds.instancesSearch).toHaveValue("");
    // Waits for the restored rows rather than counting whatever is on screen now — at
    // this point the table still holds the empty result from the step above.
    await expect(rds.instancesRows).toHaveCount(baseline);
  });
});

test("RDS - open Instances, read the first instance's state, filter State by that state, verify every listed instance reports it", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  // The State cell holds the raw provider state through ds/Label ("available"), which the
  // component then capitalizes in CSS — so this reads textContent to get the underlying
  // value rather than the rendered one. The dropdown Title Cases that same value into its
  // option ("Available") — see getStateDropdownOptions in
  // app/src/components/cloudaccount/stateFilter.ts. Driving the filter from a state the
  // account actually holds is what keeps the assertion below about the filter rather than
  // about the dev fleet's composition.
  const rawState = ((await rds.stateCells().first().textContent()) ?? "").trim();
  expect(rawState, "The first instance's State cell was empty, so there is no state to filter by").not.toBe("");
  expect(rawState).not.toBe("-");

  const wantedOption = stateOptionLabel(rawState);
  await rds.filterTrigger("state").click();
  await expect(rds.visibleFilterOptions().first()).toBeVisible({ timeout: 20000 });

  // NATIVE_STATE_OPTIONS.AmazonRDS lists six states and 'stopped' is not among them
  // (stateFilter.ts:23), so an account whose first instance is stopped legitimately
  // offers no option to filter by. That is a gap in the app's option list rather than a
  // fault in the filter this test covers, so it is reported as a skip with the state
  // named rather than failing on a click that could never land.
  const offered = (await rds.visibleFilterOptions().allInnerTexts()).map((label) => label.trim());
  if (!offered.includes(wantedOption)) {
    await page.keyboard.press("Escape");
    test.skip(true, `The State filter offers no "${wantedOption}" option, so this state cannot be filtered by.`);
    return;
  }

  await rds.filterOption(wantedOption).click();
  await expect(rds.filterTrigger("state")).toContainText(wantedOption);

  await test.step(`Every listed instance reports ${rawState}`, async () => {
    // A retrying block rather than a single read: the filter refetches, so reading the
    // cells once would assert against the pre-filter rows.
    await expect(async () => {
      const states = await rds.stateCells().allInnerTexts();
      expect(states.length).toBeGreaterThan(0);
      for (const state of states) {
        expect(state.trim().toLowerCase()).toBe(rawState.toLowerCase());
      }
    }).toPass({ timeout: 60000, intervals: [1000, 2000, 5000] });
  });
});

test("RDS - open Instances, verify the Tag Value filter stays disabled until a Tag Key is chosen", { tag: ["@dev", "@regression", "@validation", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds } = await openInstances(page);

  // InstancesView passes disabled={!selectedTagKey} (Instances.tsx:976) and
  // ds/FilterDropdown puts that straight on its trigger <button>.
  await expect(rds.filterTrigger("tagValue")).toBeDisabled();

  await rds.filterTrigger("tagKey").click();

  // The key list comes from the account's own tags, so an account whose instances carry
  // none legitimately offers none. Absence is therefore an expected branch, not a
  // failure, which is what this probe returns rather than throwing.
  const hasKeys = await rds
    .visibleFilterOptions()
    .first()
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (!hasKeys) {
    await page.keyboard.press("Escape");
    // The other half of the same rule: with no key selectable, Tag Value must stay shut.
    await expect(rds.filterTrigger("tagValue")).toBeDisabled();
    return;
  }

  const firstKey = (await rds.visibleFilterOptions().first().innerText()).trim();
  await rds.filterOption(firstKey).click();

  await test.step("Choosing a key unlocks the Tag Value filter", async () => {
    await expect(rds.filterTrigger("tagKey")).toContainText(firstKey);
    await expect(rds.filterTrigger("tagValue")).toBeEnabled();
  });
});

test("RDS - open Instances, expand the first instance row, verify the drill-down opens on Details and offers the Monitoring, Events, Performance Insights, Logs and Action History tabs", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  const toggle = rds.expandToggle(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");

  await test.step("Details is the drill-down's default tab", async () => {
    await expect(rds.drilldownTab("Details")).toHaveAttribute("aria-selected", "true");
    await expect(rds.drilldownPanel("Details")).toBeVisible({ timeout: 30000 });
  });

  await test.step("The RDS tab strip is complete", async () => {
    // CloudAccountTable only populates `requiredTabs` when the first expand fires its
    // checkForTabsWithData callback (CloudAccountTable.jsx:73-92), so the strip does not
    // exist at all until the click above — which is why it is asserted here rather than
    // on load. Every DataBlock inside the Details panel itself is conditional on the
    // instance's metadata (Instances.tsx:220-240), so the tab contract is what this
    // surface can be held to; the panel's contents are not.
    for (const tabName of DRILLDOWN_TABS) {
      await expect(rds.drilldownTab(tabName)).toBeVisible();
    }
  });
});

test("RDS - open Instances, expand a row, switch the drill-down to Performance Insights, collapse the row, verify the drill-down closes", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  const toggle = rds.expandToggle(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(rds.drilldownPanel("Details")).toBeVisible({ timeout: 30000 });

  await test.step("Performance Insights replaces Details in the open drill-down", async () => {
    await rds.drilldownTab("Performance Insights").click();
    await expect(rds.drilldownTab("Performance Insights")).toHaveAttribute("aria-selected", "true");
    await expect(rds.drilldownPanel("Performance Insights")).toBeVisible({ timeout: 30000 });
    // TabPanel marks every unselected panel `hidden` and renders none of its children
    // (CustomTable.jsx:95-96), so the Details panel is gone from the visible tree rather
    // than merely stacked behind the new one.
    await expect(rds.drilldownPanel("Details")).toHaveCount(0);
  });

  await test.step("Collapsing the row closes the drill-down and leaves the listing intact", async () => {
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(rds.drilldownPanel("Performance Insights")).toHaveCount(0);
    await expect(rds.instancesRows).toHaveCount(rowCount);
  });
});

test("RDS - open Instances, open a row's actions menu, start a lifecycle action, cancel the confirmation, verify the dialog closes and the database keeps the state it had", { tag: ["@dev", "@regression", "@negative", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: rds, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  // textContent, not innerText: ds/Label capitalizes the state in CSS, so innerText
  // returns the rendered "Available" while toHaveText below compares against textContent,
  // the raw "available". Both sides have to read the value the same way.
  const stateBefore = ((await rds.stateCells().first().textContent()) ?? "").trim();
  expect(stateBefore).not.toBe("");

  await rds.rowActionsTrigger(0).click();
  await expect(rds.visibleMenuItems().first()).toBeVisible({ timeout: 20000 });

  const action = await firstEnabledLifecycleAction(rds);

  if (!action) {
    // buildMenuItems greys out every action the instance's state does not allow, and all
    // of them when the account is read-only (resourceActions.ts:269-274). Both are
    // legitimate states of a shared dev account, and the gating is itself worth
    // asserting, so this branch checks it rather than skipping.
    const items = await rds.visibleMenuItems().all();
    expect(items.length).toBeGreaterThan(0);
    for (const item of items) {
      // Read through the same helper the picker above used, so this branch asserts
      // exactly the condition that led the picker into it.
      expect(await isMenuItemDisabled(item), "A menu item the action picker judged disabled reports as enabled").toBe(true);
    }
    await page.keyboard.press("Escape");
    await expect(rds.visibleMenuItems()).toHaveCount(0);
    return;
  }

  await rds.actionMenuItem(action).click();

  await test.step(`The ${LIFECYCLE_ACTION_LABEL[action]} confirmation explains what will happen`, async () => {
    await expect(rds.confirmDialog).toBeVisible({ timeout: 20000 });
    await expect(rds.confirmDialogTitle).toHaveText(LIFECYCLE_ACTION_LABEL[action]);
    await expect(rds.confirmDialogSubmit).toBeVisible();
    if (STRICT_CONFIRM_ACTIONS.includes(action)) {
      // Stop Instance is the one RDS action declared confirmationType: 'strict'
      // (resourceActions.ts:117), so its dialog additionally demands the resource name
      // be typed back. The input is asserted present and deliberately left empty.
      await expect(rds.strictConfirmInput).toBeVisible();
    }
  });

  await test.step("Cancel closes it and the database keeps the state it had", async () => {
    // Deliberately never clicks #submit: that dispatches a real start/stop/reboot against
    // a database instance of a shared AWS account.
    await rds.confirmDialogCancel.click();
    // toHaveCount(0), not toBeHidden: ds/Modal is a MUI Dialog and unmounts on close, so
    // the dialog leaving the DOM is the real end state — and the unfiltered locator also
    // fails if a second dialog was left open behind this one.
    await expect(rds.allDialogs).toHaveCount(0, { timeout: 20000 });
    await expect(rds.instancesRows).toHaveCount(rowCount);
    await expect(rds.stateCells().first()).toHaveText(stateBefore);
  });
});

test("RDS - open Summary, click the Total Instances tile, verify the Instances sub-tab opens, reopen Summary from the RDS tab, verify the tiles come back", { tag: ["@dev", "@smoke", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const rds = await openRdsSubTab(page, "summary");
  await expect(rds.statTotalInstances).toBeVisible({ timeout: 60000 });

  await test.step("The tile deep-links into the Instances sub-tab", async () => {
    // Summary.tsx:196-206 pushes `?subtab=2#…/instances`, so the fragment is part of the
    // behaviour under test — a copied deep link has to reopen the same sub-tab.
    await rds.statTotalInstances.click();
    await expect(page).toHaveURL(rds.RDSInstancesUrl, { timeout: 30000 });
    await expect(rds.instancesRoot).toBeVisible({ timeout: 60000 });
    await expect(rds.instancesSearch).toBeVisible();
  });

  await test.step("The RDS tab strip is the route back to Summary", async () => {
    await rds.navigateToSubTab(rds.AnchorTabRDS, rds.RDSSummary, rds.RDSSummaryUrl);
    await page.mouse.move(640, 500);
    await expect(rds.statTotalInstances).toBeVisible({ timeout: 60000 });
    await expect(rds.statOptimizeCount).toBeVisible();
  });
});

test("RDS - open Summary, click the Optimize Count tile, verify the RDS Optimize sub-tab opens", { tag: ["@dev", "@smoke", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const rds = await openRdsSubTab(page, "summary");
  await expect(rds.statOptimizeCount).toBeVisible({ timeout: 60000 });

  // handleOptimizeClick (Summary.tsx:408-414) pushes `?subtab=1#…/optimize`, the sibling
  // of the Total Instances deep link and the only other tile on this surface that
  // navigates. Asserted on the URL plus the tile being gone, because what the Optimize
  // sub-tab itself renders is covered by the AWS Optimize specs, not here.
  await rds.statOptimizeCount.click();
  await expect(page).toHaveURL(rds.RDSOptimizeUrl, { timeout: 30000 });
  await expect(rds.statOptimizeCount).toHaveCount(0);
});
