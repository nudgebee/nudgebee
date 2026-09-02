// Not for OSS
import { test, expect } from "@playwright/test";
import { openServiceCriticalityTab, noMatchTerm } from "./serviceCriticalityHelper";
import { CRITICALITY_LEVELS, DEFAULT_TIER } from "./serviceCriticalityLocators";

// Cluster Details > Events > Service Criticality
// (app/src/components/criticality/WorkloadCriticalityManager.tsx).
//
// Read-only by design. The module's only write actions — "Set <tier>" and "Reset to
// derived" — reclassify workloads that already exist on the shared dev cluster, and
// criticality feeds incident triage scoring for everyone using it. There is no way to
// create a throwaway workload from this screen, so the menu is opened and dismissed
// rather than committed; see "Follow-ups" in the PR.
const TEST_TIMEOUT = 180000;

test("Service Criticality sanity - open the cluster Events tab, select Service Criticality, verify the info banner, toolbar controls and workload listing render", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  await test.step("The explainer banner and its dismiss control render", async () => {
    await expect(sc.infoBanner).toBeVisible();
    await expect(sc.infoBanner).toContainText("Why set service criticality?");
    await expect(sc.infoBannerDismissBtn).toBeVisible();
  });

  await test.step("The toolbar offers search, both filters and the classified-only switch", async () => {
    await expect(sc.toolbar).toBeVisible();
    await expect(sc.searchInput).toBeVisible();
    await expect(sc.namespaceFilter).toBeVisible();
    await expect(sc.tierFilter).toBeVisible();
    await expect(sc.onlyClassifiedSwitch).not.toBeChecked();
  });

  await test.step("The listing resolved rather than failing its request", async () => {
    // WorkloadCriticalityManager swallows a failed list call into a snackbar and leaves
    // `items` empty, which renders the same empty panel as a genuinely empty cluster.
    // Without this the suite would report green coverage over a broken backend.
    await expect(sc.loadFailureToast).toHaveCount(0);
    await expect(sc.tableBody.or(sc.emptyState).first()).toBeAttached();
  });
});

test("Service Criticality sanity - open the tab, verify the table exposes the Namespace, Workload, Kind, Criticality, Source and Why columns", { tag: ["@dev", "@sanity", "@functional"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const rows = await sc.rows.count();

  if (rows === 0) {
    // The cluster has reported no workload criticality yet: the module replaces the
    // whole table with its own panel, so there are no headers to assert.
    await expect(sc.emptyState).toBeVisible();
    await expect(sc.table).toHaveCount(0);
    return;
  }

  // Column contract from WorkloadCriticalityManager's `headers` array.
  for (const header of ["Namespace", "Workload", "Kind", "Criticality", "Source", "Why"]) {
    await expect(sc.table.locator("th", { hasText: header }).first()).toBeVisible();
  }

  // Every row carries one of the four tiers the module defines — a chip rendering an
  // unmapped value would come back as an empty or unexpected cell here.
  const tiers = await sc.columnValues("Criticality");
  expect(tiers.length).toBe(rows);
  expect(tiers.every((tier) => CRITICALITY_LEVELS.includes(tier.toLowerCase() as (typeof CRITICALITY_LEVELS)[number]))).toBe(true);
});

test("Service Criticality - read the first workload name, search for it, verify only matching workloads stay listed and the full list returns when the search is cleared", { tag: ["@dev", "@regression", "@search"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();
  if (baseline === 0) {
    await expect(sc.emptyState).toBeVisible();
    return;
  }

  const firstName = await sc.cellValue(sc.rows.first(), "Workload");
  expect(firstName.length).toBeGreaterThan(0);
  const term = firstName.toLowerCase();

  await sc.setSearch(firstName);

  // Filtering is client-side and re-renders on every keystroke, so the settled state is
  // polled rather than read once off a list that is still shrinking.
  await expect
    .poll(async () => {
      const names = await sc.columnValues("Workload");
      return names.length > 0 && names.every((name) => name.toLowerCase().includes(term));
    }, { timeout: 30000 })
    .toBe(true);
  expect(await sc.rows.count()).toBeLessThanOrEqual(baseline);

  await sc.clearSearch();
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - search for a workload name that cannot exist, verify the No Data Available panel replaces the table, clear the search, verify the full listing returns", { tag: ["@dev", "@regression", "@search", "@negative"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();

  await sc.setSearch(noMatchTerm());

  await expect(sc.emptyState).toBeVisible();
  await expect(sc.emptyState).toHaveText("No Data Available");
  await expect(sc.rows).toHaveCount(0);

  await sc.clearSearch();
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - filter by the first row's namespace, verify every listed workload belongs to that namespace, clear the filter, verify the full listing returns", { tag: ["@dev", "@regression", "@functional", "@search"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();
  if (baseline === 0) {
    // With no workloads the namespace options are derived from an empty list, so there
    // is nothing to filter by — the empty panel is the whole behaviour on this cluster.
    await expect(sc.emptyState).toBeVisible();
    return;
  }

  const namespace = await sc.cellValue(sc.rows.first(), "Namespace");
  expect(namespace.length).toBeGreaterThan(0);

  await sc.applyFilter(sc.namespaceFilter, namespace);

  await expect
    .poll(async () => {
      const namespaces = await sc.columnValues("Namespace");
      return namespaces.length > 0 && namespaces.every((value) => value === namespace);
    }, { timeout: 30000 })
    .toBe(true);
  expect(await sc.rows.count()).toBeLessThanOrEqual(baseline);

  // The filter is multi-select, so clicking the same option again deselects it.
  await sc.applyFilter(sc.namespaceFilter, namespace);
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - filter by the first row's criticality tier, verify every listed workload carries that tier, clear the filter, verify the full listing returns", { tag: ["@dev", "@regression", "@functional", "@search"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();
  if (baseline === 0) {
    await expect(sc.emptyState).toBeVisible();
    return;
  }

  const tier = (await sc.cellValue(sc.rows.first(), "Criticality")).toLowerCase();
  expect(CRITICALITY_LEVELS).toContain(tier);

  await sc.applyFilter(sc.tierFilter, tier);

  await expect
    .poll(async () => {
      const tiers = await sc.columnValues("Criticality");
      return tiers.length > 0 && tiers.every((value) => value.toLowerCase() === tier);
    }, { timeout: 30000 })
    .toBe(true);
  expect(await sc.rows.count()).toBeLessThanOrEqual(baseline);

  await sc.applyFilter(sc.tierFilter, tier);
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - turn on Only classified, verify the listing refetches without the default medium tier, turn it off, verify the full listing returns", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();

  // This toggle is the module's one server-side filter (`tiered_only`), so the refetch
  // is waited on directly — the previous <tbody> stays attached while it is in flight.
  await sc.waitForListRequest(async () => {
    await sc.onlyClassifiedSwitch.check();
  });
  await expect(sc.onlyClassifiedSwitch).toBeChecked();

  const classified = await sc.rows.count();
  expect(classified).toBeLessThanOrEqual(baseline);

  if (classified > 0) {
    // Medium is the tier workloads sit at when nothing has been persisted for them, so
    // it is exactly what `tiered_only` drops.
    const tiers = await sc.columnValues("Criticality");
    expect(tiers.some((tier) => tier.toLowerCase() === DEFAULT_TIER)).toBe(false);
  } else {
    await expect(sc.emptyState).toBeVisible();
  }

  await sc.waitForListRequest(async () => {
    await sc.onlyClassifiedSwitch.uncheck();
  });
  await expect(sc.onlyClassifiedSwitch).not.toBeChecked();
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - dismiss the info banner, verify it is removed and the workload listing stays on screen", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();
  await expect(sc.infoBanner).toBeVisible();

  await sc.infoBannerDismissBtn.click();

  await expect(sc.infoBanner).toHaveCount(0);
  await expect(sc.toolbar).toBeVisible();
  await expect(sc.rows).toHaveCount(baseline);
});

test("Service Criticality - open the first workload's criticality menu, verify it offers the tiers the row is not on, press Escape, verify the row keeps its original tier", { tag: ["@dev", "@regression", "@functional", "@negative"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  if ((await sc.rows.count()) === 0) {
    await expect(sc.emptyState).toBeVisible();
    return;
  }

  const row = sc.rows.first();
  const originalTier = (await sc.cellValue(row, "Criticality")).toLowerCase();
  const trigger = sc.rowActionTrigger(row);

  // renderActions returns null without write access on the account, so a missing
  // trigger here means the run's user cannot administer criticality — a precondition
  // failure worth seeing, not a branch to pass through quietly.
  await expect(trigger, "The row's criticality menu did not render — the run user needs write access on the cluster's cloud account").toBeVisible();
  await trigger.click();

  for (const level of CRITICALITY_LEVELS) {
    if (level === originalTier) {
      // The row's current tier is filtered out of its own menu.
      await expect(sc.menuItem(`Set ${level}`)).toHaveCount(0);
    } else {
      await expect(sc.menuItem(`Set ${level}`)).toBeVisible();
    }
  }

  await page.keyboard.press("Escape");
  await expect(sc.menuItem(`Set ${CRITICALITY_LEVELS[0]}`)).toHaveCount(0);

  // Nothing was selected, so no upsert ran and the row must read exactly as before.
  expect((await sc.cellValue(row, "Criticality")).toLowerCase()).toBe(originalTier);
  await expect(sc.loadFailureToast).toHaveCount(0);
});

test("Service Criticality - switch to the Triage Rules sub-tab and back to Service Criticality, verify the workload listing reloads", { tag: ["@dev", "@regression", "@functional"] }, async ({ page }) => {
  test.setTimeout(TEST_TIMEOUT);
  const sc = await openServiceCriticalityTab(page);

  const baseline = await sc.rows.count();

  await sc.clickTab(sc.TroubleshootTriageRules);
  await expect(page).toHaveURL(/#events\/triage-rules/, { timeout: 30000 });
  // The tab body is keyed on the sub-tab, so leaving unmounts the criticality listing.
  await expect(sc.listingCard).toHaveCount(0);

  await sc.waitForListRequest(async () => {
    await sc.clickTab(sc.TroubleshootServiceCriticality);
  });
  await expect(page).toHaveURL(/#events\/service-criticality/, { timeout: 30000 });

  await expect(sc.toolbar).toBeVisible();
  await expect(sc.rows).toHaveCount(baseline);
});
