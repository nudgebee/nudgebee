// Not for OSS
import { test, expect } from "@playwright/test";
import { openEc2SubTab, openInstances, firstEnabledLifecycleAction } from "./ec2Helper";
import { INSTANCE_COLUMNS, LIFECYCLE_ACTION_LABEL, stateOptionLabel } from "./ec2Constants";

// Infra > Cloud > AWS > EC2 — the resource tab rendered by
// app/src/components/cloudaccount/ec2/{Summary,Instances}.tsx.
//
// The dev AWS account is shared, so everything here is read-only: no instance is
// started, stopped, rebooted or sent an SSM command. The one write path the module
// offers is exercised as far as its confirmation dialog and then cancelled.

// A term no instance id or name can match, so the "no results" assertions are about the
// filter working rather than about what the dev account happens to hold.
const NO_MATCH_TERM = `zz-no-such-instance-${Date.now()}`;

// Named so a skip reads as the environment precondition it is rather than as a gap in
// coverage: several journeys below need at least one EC2 row to act on.
const NO_INSTANCES_HINT =
  "The AWS account holds no EC2 instances, so there is no row to expand or act on. " +
  "Point AWS_CLUSTER_NAME at an account with at least one instance to cover this journey.";

test("EC2 sanity - open the AWS account, hover the EC2 tab, open Summary, verify the instance, alarm and optimization tiles render their counts", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const ec2 = await openEc2SubTab(page, "summary");

  await test.step("The three headline tiles are present", async () => {
    for (const tile of [ec2.statTotalInstances, ec2.statFiredAlarmCount, ec2.statOptimizeCount]) {
      await expect(tile).toBeVisible({ timeout: 60000 });
    }
  });

  await test.step("Each tile settles on a value rather than staying on its skeleton", async () => {
    // ds/Stat formats counts through formatNumber, which renders a placeholder "-" for
    // a missing or zero value — a normal state for a small dev account. Anchoring on
    // the tile's own label and requiring either a number or that placeholder is what
    // separates "rendered" from "still loading", which is what this step guards.
    await expect(ec2.statTotalInstances).toHaveText(/^Total Instances\s*(?:[\d,]+|-)$/);
    await expect(ec2.statFiredAlarmCount).toContainText("Fired Alarm Count");
    await expect(ec2.statFiredAlarmCount).toContainText("last 7 days");
    await expect(ec2.statOptimizeCount).toHaveText(/^Optimize Count\s*(?:[\d,]+|-)$/);
  });

  await test.step("The metrics card renders below the tiles", async () => {
    await expect(ec2.metricsCard).toBeVisible({ timeout: 60000 });
    await expect(ec2.metricsCard).toContainText("Metrics");
  });
});

test("EC2 sanity - open the EC2 Instances sub-tab, verify the listing renders its eight named columns and the search, state, region and tag filters", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount } = await openInstances(page);

  await test.step("The toolbar exposes the search box and all four filters", async () => {
    await expect(ec2.instancesSearch).toBeVisible();
    await expect(ec2.instancesSearch).toHaveAttribute("placeholder", "Search By Instance Id/Name");
    await expect(ec2.filterTrigger("state")).toBeVisible();
    await expect(ec2.filterTrigger("region")).toBeVisible();
    await expect(ec2.filterTrigger("tagKey")).toBeVisible();
    await expect(ec2.filterTrigger("tagValue")).toBeVisible();
    await expect(ec2.refreshButton).toBeVisible();
    await expect(ec2.downloadButton).toBeVisible();
  });

  await test.step("The column contract from INSTANCE_HEADER is on screen", async () => {
    if (rowCount === 0) {
      // With no rows CustomTable replaces the whole table with its empty panel, so
      // there are no column headers to assert — the panel is the contract instead.
      await expect(ec2.instancesEmpty).toBeVisible();
      return;
    }
    for (const column of INSTANCE_COLUMNS) {
      await expect(ec2.columnHeader(column)).toBeVisible();
    }
  });
});

test("EC2 - open Instances, search an instance id that cannot exist, verify the listing empties to the No Data Available panel", { tag: ["@dev", "@regression", "@search", "@negative"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2 } = await openInstances(page);

  await ec2.searchAndApply(NO_MATCH_TERM);

  // The empty panel is asserted first, deliberately. While `loading` is true
  // CustomTable unmounts the id'd <tbody> and swaps in an id-less skeleton one
  // (CustomTable.jsx:1345, 899), so a row count taken straight after the search reads
  // 0 whatever the response holds — it would pass before the request even landed.
  // renderEmptyState bails out while loading (CustomTable.jsx:930), so the panel
  // appearing is the signal the response has landed, and only after it does the count
  // below mean anything.
  await expect(ec2.instancesEmpty).toBeVisible();
  await expect(ec2.instancesRows).toHaveCount(0);
  await expect(ec2.instancesSearch).toHaveValue(NO_MATCH_TERM);
});

test("EC2 - open Instances, search a term that matches nothing, clear the search, verify the original instance count comes back", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount: baseline } = await openInstances(page);
  // With no rows to start from, both counts below are 0 and every assertion in this
  // test passes without the search or the clear having done anything — so an empty
  // account makes the case vacuous rather than merely thin.
  test.skip(baseline === 0, NO_INSTANCES_HINT);

  await test.step("The unmatchable term empties the listing", async () => {
    await ec2.searchAndApply(NO_MATCH_TERM);
    // Empty panel first, then the count — see the note in the no-match test above.
    await expect(ec2.instancesEmpty).toBeVisible();
    await expect(ec2.instancesRows).toHaveCount(0);
  });

  await test.step("Clearing the search restores the original listing", async () => {
    await ec2.clearSearch();
    await expect(ec2.instancesSearch).toHaveValue("");
    // Waits for the restored rows rather than counting whatever is on screen now — at
    // this point the table still holds the empty result from the step above.
    await expect(ec2.instancesRows).toHaveCount(baseline);
  });
});

test("EC2 - open Instances, read the first instance's state, filter State by that state, verify every listed instance reports it", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  // The State cell holds the raw provider state through ds/Label ("running"), which the
  // component then capitalizes in CSS — so this reads textContent to get the underlying
  // value rather than the rendered one. The dropdown Title Cases that same value into
  // its option ("Running") — see getStateDropdownOptions in
  // app/src/components/cloudaccount/stateFilter.ts. Driving the filter from a state the
  // account actually holds is what keeps the assertion below about the filter rather
  // than about the dev fleet's composition.
  const rawState = ((await ec2.stateCells().first().textContent()) ?? "").trim();
  expect(rawState, "The first instance's State cell was empty, so there is no state to filter by").not.toBe("");
  expect(rawState).not.toBe("-");

  await ec2.chooseFilter("state", stateOptionLabel(rawState));

  await test.step(`Every listed instance reports ${rawState}`, async () => {
    // A retrying block rather than a single read: the filter refetches, so reading the
    // cells once would assert against the pre-filter rows.
    await expect(async () => {
      const states = await ec2.stateCells().allInnerTexts();
      expect(states.length).toBeGreaterThan(0);
      for (const state of states) {
        expect(state.trim().toLowerCase()).toBe(rawState.toLowerCase());
      }
    }).toPass({ timeout: 60000, intervals: [1000, 2000, 5000] });
  });
});

test("EC2 - open Instances, verify the Tag Value filter stays disabled until a Tag Key is chosen", { tag: ["@dev", "@regression", "@validation"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2 } = await openInstances(page);

  // InstancesView passes disabled={!selectedTagKey} and ds/FilterDropdown puts that
  // straight on its trigger <button> (FilterDropdown.jsx:1093).
  await expect(ec2.filterTrigger("tagValue")).toBeDisabled();

  await ec2.filterTrigger("tagKey").click();

  // The key list comes from getDistinctTagKeys, so an account whose instances carry no
  // tags legitimately offers none. Absence is therefore an expected branch, not a
  // failure, which is what this probe returns rather than throwing.
  const hasKeys = await ec2
    .visibleFilterOptions()
    .first()
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);

  if (!hasKeys) {
    await page.keyboard.press("Escape");
    // The other half of the same rule: with no key selectable, Tag Value must stay shut.
    await expect(ec2.filterTrigger("tagValue")).toBeDisabled();
    return;
  }

  const firstKey = (await ec2.visibleFilterOptions().first().innerText()).trim();
  await ec2.filterOption(firstKey).click();

  await test.step("Choosing a key unlocks the Tag Value filter", async () => {
    await expect(ec2.filterTrigger("tagKey")).toContainText(firstKey);
    await expect(ec2.filterTrigger("tagValue")).toBeEnabled();
  });
});

test("EC2 - open Instances, expand the first instance row, verify the Details drill-down names the instance the row belongs to", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  // CustomText stacks text1 (the resource id) above its subtexts (region, instance
  // type), so the first line of the Instance ID cell is the id on its own.
  const resourceId = (await ec2.columnCells(0).first().innerText()).split("\n")[0].trim();
  expect(resourceId).not.toBe("");

  const toggle = ec2.expandToggle(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");

  await test.step("Details is the drill-down's default tab and describes this instance", async () => {
    const details = ec2.drilldownPanel("Details");
    await expect(details).toBeVisible({ timeout: 30000 });
    // The AWS branch of the Details panel leads with <DataBlock title='Instance Id'>
    // (Instances.tsx:882). Asserting the id as well as the label is what proves the
    // panel belongs to the row that was expanded rather than to some other row.
    await expect(details).toContainText("Instance Id");
    await expect(details).toContainText(resourceId);
  });
});

test("EC2 - open Instances, expand a row, switch the drill-down to Action History, collapse the row, verify the drill-down closes", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  const toggle = ec2.expandToggle(0);
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(ec2.drilldownPanel("Details")).toBeVisible({ timeout: 30000 });

  await test.step("Action History replaces Details in the open drill-down", async () => {
    await ec2.drilldownTab("Action History").click();
    await expect(ec2.drilldownTab("Action History")).toHaveAttribute("aria-selected", "true");
    await expect(ec2.drilldownPanel("Action History")).toBeVisible({ timeout: 30000 });
    // TabPanel renders only the selected tab's children (CustomTable.jsx:96), so the
    // Details panel is gone rather than merely hidden behind the new one.
    await expect(ec2.drilldownPanel("Details")).toHaveCount(0);
  });

  await test.step("Collapsing the row closes the drill-down and leaves the listing intact", async () => {
    await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "false");
    await expect(ec2.drilldownPanel("Action History")).toHaveCount(0);
    await expect(ec2.instancesRows).toHaveCount(rowCount);
  });
});

test("EC2 - open Instances, open a row's actions menu, start a lifecycle action, cancel the confirmation, verify the dialog closes and the instance is untouched", { tag: ["@dev", "@regression", "@negative", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const { locators: ec2, rowCount } = await openInstances(page);
  test.skip(rowCount === 0, NO_INSTANCES_HINT);

  // textContent, not innerText: ds/Label capitalizes the state in CSS, so innerText
  // returns the rendered "Running" while toHaveText below compares against textContent,
  // the raw "running". Both sides have to read the value the same way.
  const stateBefore = ((await ec2.stateCells().first().textContent()) ?? "").trim();
  expect(stateBefore).not.toBe("");

  await ec2.rowActionsTrigger(0).click();
  await expect(ec2.visibleMenuItems().first()).toBeVisible({ timeout: 20000 });

  const action = await firstEnabledLifecycleAction(ec2);

  if (!action) {
    // buildMenuItems greys out every action the instance's state does not allow, and
    // all of them when the account is read-only (resourceActions.ts:269-274). Both are
    // legitimate states of a shared dev account, and the gating is itself worth
    // asserting, so this branch checks it rather than skipping.
    const items = await ec2.visibleMenuItems().all();
    expect(items.length).toBeGreaterThan(0);
    for (const item of items) {
      await expect(item).toHaveAttribute("aria-disabled", "true");
    }
    await page.keyboard.press("Escape");
    await expect(ec2.visibleMenuItems()).toHaveCount(0);
    return;
  }

  await ec2.actionMenuItem(action).click();

  await test.step(`The ${LIFECYCLE_ACTION_LABEL[action]} confirmation explains what will happen`, async () => {
    await expect(ec2.confirmDialog).toBeVisible({ timeout: 20000 });
    await expect(ec2.confirmDialogTitle).toHaveText(LIFECYCLE_ACTION_LABEL[action]);
    await expect(ec2.confirmDialogSubmit).toBeVisible();
  });

  await test.step("Cancel closes it and the instance keeps the state it had", async () => {
    // Deliberately never clicks #submit: that dispatches a real start/stop/reboot
    // against an instance of a shared AWS account.
    await ec2.confirmDialogCancel.click();
    // toHaveCount(0), not toBeHidden: ds/Modal is a MUI Dialog and unmounts on close,
    // so the dialog leaving the DOM is the real end state — and the unfiltered locator
    // also fails if a second dialog was left open behind this one.
    await expect(ec2.allDialogs).toHaveCount(0, { timeout: 20000 });
    await expect(ec2.instancesRows).toHaveCount(rowCount);
    await expect(ec2.stateCells().first()).toHaveText(stateBefore);
  });
});

test("EC2 - open Summary, click the Total Instances tile, verify the Instances sub-tab opens, reopen Summary from the EC2 tab, verify the tiles come back", { tag: ["@dev", "@smoke", "@functional"] }, async ({ page }) => {
  test.setTimeout(180000);

  const ec2 = await openEc2SubTab(page, "summary");
  await expect(ec2.statTotalInstances).toBeVisible({ timeout: 60000 });

  await test.step("The tile deep-links into the Instances sub-tab", async () => {
    // Summary.tsx:237-248 pushes `?subtab=2#…/instances`, so the fragment is part of
    // the behaviour under test — a copied deep link has to reopen the same sub-tab.
    await ec2.statTotalInstances.click();
    await expect(page).toHaveURL(ec2.EC2InstancesUrl, { timeout: 30000 });
    await expect(ec2.instancesRoot).toBeVisible({ timeout: 60000 });
    await expect(ec2.instancesSearch).toBeVisible();
  });

  await test.step("The EC2 tab strip is the route back to Summary", async () => {
    await ec2.navigateToSubTab(ec2.AnchorTabEC2, ec2.EC2Summary, ec2.EC2SummaryUrl);
    await page.mouse.move(640, 500);
    await expect(ec2.statTotalInstances).toBeVisible({ timeout: 60000 });
    await expect(ec2.statOptimizeCount).toBeVisible();
  });
});
