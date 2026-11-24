import { test, expect } from "@playwright/test";
import {
  chooseFilter,
  expandableServiceRow,
  firstFilterOption,
  isResourcesListing,
  isServicesListing,
  nextQueryMatching,
  openDrilldown,
  openServicesTab,
  orderBy,
  parseCurrency,
  regionFilter,
  serviceNameAt,
  serviceNameFilter,
  serviceRowCount,
  spendWindow,
  trackGroupingsQueries,
  waitForServices,
} from "./servicesHelper";
import {
  COLUMN,
  DATE_SHORTCUT,
  DRILLDOWN_TABS,
  RECOMMENDATION_COLUMNS,
  RESOURCE_COLUMNS,
  ROW_ACTIONS,
  SERVICE_COLUMNS,
  SORT_FIELD,
} from "./servicesConstants";

// Infra > Cloud > AWS > Services — the per-service cost listing rendered by
// app/src/components/cloudaccount/CloudAccountServices.tsx, with its Resources, Cost
// Trend and Recommendations drill-downs.
//
// Everything here is read-only. The listing's only write controls are the two row
// actions, which the app ships permanently disabled (cloudaccount/common.tsx), and the
// Recommendations drill-down's Create Ticket, which this suite never opens.

// Named so a skip reads as the environment precondition it is rather than as a gap in
// coverage: most journeys below need at least one billed service to act on.
const NO_SERVICES_HINT =
  "The AWS account lists no services for the default 30-day window, so there is no row to filter, sort or expand. " +
  "Point AWS_CLUSTER_NAME at an account with billed resources to cover this journey.";

const NO_RESOURCE_ROWS_HINT =
  "No listed service carries a positive resource count outside the ECS special case, so the Resources drill-down " +
  "has no row to open. Point AWS_CLUSTER_NAME at an account with inventoried resources to cover this journey.";

test("AWS Services sanity - open the AWS account, select the Services tab, verify the six named columns, the date range, the download control and the Service Name, Region and Tag Key filters render", { tag: ["@dev", "@sanity", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);

  await test.step("The toolbar exposes the date range, download and all three filters", async () => {
    await expect(services.dateRangeTrigger).toBeVisible();
    await expect(services.downloadButton).toBeVisible();
    await expect(services.filterTrigger("serviceName")).toBeVisible();
    await expect(services.filterTrigger("region")).toBeVisible();
    await expect(services.filterTrigger("tagKey")).toBeVisible();
  });

  await test.step("The column contract from servicesHeader is on screen", async () => {
    if (rowCount === 0) {
      // With no rows CustomTable replaces the whole table with its empty panel, so
      // there are no column headers to assert — the panel is the contract instead.
      await expect(services.servicesEmpty).toBeVisible();
      return;
    }
    for (const column of SERVICE_COLUMNS) {
      await expect(services.columnHeader(column)).toBeVisible();
    }
  });
});

test("AWS Services sanity - open the Services tab, verify every listed row names a service, shows a resource count and a currency total cost, and the footer reports the row range", { tag: ["@dev", "@sanity", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  await test.step("Each row settles on real cell values rather than skeletons", async () => {
    for (let i = 0; i < rowCount; i++) {
      await expect(services.columnCells(COLUMN.serviceName).nth(i)).toHaveText(/\S/);
      // count_resource renders as a number or "-" when the grouping has none.
      await expect(services.columnCells(COLUMN.resources).nth(i)).toHaveText(/^\s*(\d[\d,]*|-)\s*$/);
      // Currency renders "$1,234.56", "<$1" for sub-dollar spend, or "-" (Currency.jsx:7-13).
      await expect(services.columnCells(COLUMN.totalCost).nth(i)).toHaveText(/^\s*(<?\$[\d,]+(\.\d+)?|-)\s*$/);
    }
  });

  await test.step("The pagination footer accounts for exactly the rows on screen", async () => {
    await expect(services.paginationSummary).toBeVisible();
    const summary = ((await services.paginationSummary.innerText()) ?? "").trim();
    const match = summary.match(/^Showing ([\d,]+)-([\d,]+) of ([\d,]+) results$/);
    expect(match, `unexpected pagination copy: "${summary}"`).not.toBeNull();
    const [, first, last] = match as RegExpMatchArray;
    expect(Number(first.replace(/,/g, ""))).toBe(1);
    expect(Number(last.replace(/,/g, ""))).toBe(rowCount);
  });
});

test("AWS Services - pick a service in the Service Name filter, verify the listing request carries that service and only its row stays listed, clear the filter, verify the full listing returns", { tag: ["@dev", "@regression", "@search", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const baseline = await serviceRowCount(services);
  test.skip(baseline === 0, NO_SERVICES_HINT);

  const target = await serviceNameAt(services, 0);
  const trigger = services.filterTrigger("serviceName");

  await test.step("Filtering narrows both the request and the table to that service", async () => {
    const before = log.entries.length;
    await chooseFilter(services, trigger, target);

    const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && serviceNameFilter(e.query) === target, `a listing filtered to ${target}`);
    expect(serviceNameFilter(sent.query)).toBe(target);

    // The response has landed (the log is hung off "response"), so the table is
    // already re-rendered and every surviving row must be the filtered service.
    await expect(services.servicesRows).toHaveCount(1, { timeout: 30000 });
    await expect(services.columnCells(COLUMN.serviceName).first()).toHaveText(target);
  });

  await test.step("Clearing the filter drops it from the request and restores the listing", async () => {
    const before = log.entries.length;
    await services.filterClear(trigger).click();

    const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && serviceNameFilter(e.query) === null, "an unfiltered listing");
    expect(serviceNameFilter(sent.query)).toBeNull();
    await expect(trigger).not.toContainText(target);
    await expect(services.servicesRows).toHaveCount(baseline, { timeout: 30000 });
  });
});

test("AWS Services - pick the first region in the Region filter, verify the listing request carries that region, the trigger shows it and no more rows than before stay listed", { tag: ["@dev", "@regression", "@search", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const baseline = await serviceRowCount(services);
  test.skip(baseline === 0, NO_SERVICES_HINT);

  const trigger = services.filterTrigger("region");
  const region = await firstFilterOption(page, services, trigger);
  test.skip(region === null, "The AWS account reports no resource regions, so the Region filter has nothing to offer.");

  const before = log.entries.length;
  await chooseFilter(services, trigger, region as string);

  const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && regionFilter(e.query) === region, `a listing filtered to region ${region}`);
  expect(regionFilter(sent.query)).toBe(region);
  await expect(trigger).toContainText(region as string);

  // A region can only ever narrow the grouping, never widen it. The response is in, so
  // this reads the re-rendered table rather than racing the fetch.
  await waitForServices(services);
  expect(await services.servicesRows.count()).toBeLessThanOrEqual(baseline);
});

test("AWS Services - click the Total Cost header, verify the listing re-requests costs ascending and the listed costs rise down the page, click it again, verify they fall", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount < 2, "Sorting needs at least two listed services to compare.");

  // The listing mounts sorted by Total Cost descending (CloudAccountServices.tsx:104-107),
  // so the first click flips it to ascending (CustomTable.jsx:836-846).
  const readCosts = async (): Promise<number[]> => {
    const texts = await services.columnCells(COLUMN.totalCost).allInnerTexts();
    return texts.map(parseCurrency).filter((v): v is number => v !== null);
  };
  const isSorted = (values: number[], ascending: boolean): boolean =>
    values.every((v, i) => i === 0 || (ascending ? v >= values[i - 1] : v <= values[i - 1]));

  await test.step("First click sorts ascending", async () => {
    const before = log.entries.length;
    await services.sortHandle("Total Cost").click();
    const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && orderBy(e.query)?.order === "asc_nulls_last", "an ascending listing");
    expect(orderBy(sent.query)).toEqual({ column: SORT_FIELD["Total Cost"], order: "asc_nulls_last" });
    await waitForServices(services);
    expect(isSorted(await readCosts(), true)).toBe(true);
  });

  await test.step("Second click sorts descending", async () => {
    const before = log.entries.length;
    await services.sortHandle("Total Cost").click();
    const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && orderBy(e.query)?.order === "desc_nulls_last", "a descending listing");
    expect(orderBy(sent.query)).toEqual({ column: SORT_FIELD["Total Cost"], order: "desc_nulls_last" });
    await waitForServices(services);
    expect(isSorted(await readCosts(), false)).toBe(true);
  });
});

test("AWS Services - click the Estimated Savings header, verify the listing request switches its order column to estimated savings ascending", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  const before = log.entries.length;
  await services.sortHandle("Estimated Savings").click();

  // A column that was not the active sort starts ascending (CustomTable.jsx:841).
  const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && orderBy(e.query)?.column === SORT_FIELD["Estimated Savings"], "a listing ordered by savings");
  expect(orderBy(sent.query)).toEqual({ column: SORT_FIELD["Estimated Savings"], order: "asc_nulls_last" });
  await waitForServices(services);
  await expect(services.servicesRows.first().or(services.servicesEmpty)).toBeVisible();
});

test("AWS Services - expand a service row, verify the Resources drill-down requests that service's resources and lists them under the seven resource columns with Type, Region and Tag Key filters, collapse the row, verify the drill-down closes", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const rowIndex = await expandableServiceRow(services);
  test.skip(rowIndex < 0, NO_RESOURCE_ROWS_HINT);
  const serviceName = await serviceNameAt(services, rowIndex);

  await test.step("The drill-down asks for exactly this service's resources", async () => {
    const before = log.entries.length;
    await openDrilldown(services, rowIndex, DRILLDOWN_TABS[0]);
    const sent = await nextQueryMatching(log, before, (e) => isResourcesListing(e) && serviceNameFilter(e.query) === serviceName, `a resources listing for ${serviceName}`);
    expect(serviceNameFilter(sent.query)).toBe(serviceName);
  });

  await test.step("The resources table renders its toolbar and column contract", async () => {
    await expect(services.resourcesRoot).toBeVisible({ timeout: 30000 });
    await expect(services.resourceFilterTrigger("type")).toBeVisible();
    await expect(services.resourceFilterTrigger("region")).toBeVisible();
    await expect(services.resourceFilterTrigger("tagKey")).toBeVisible();
    // The row carried a positive resource count, so the grouping must list at least one.
    await expect(services.resourcesRows.first()).toBeVisible({ timeout: 60000 });
    for (const column of RESOURCE_COLUMNS) {
      await expect(services.resourceColumnHeader(column)).toBeVisible();
    }
  });

  await test.step("Collapsing the row takes the drill-down with it", async () => {
    await services.expandToggle(rowIndex).click();
    await expect(services.expandToggle(rowIndex)).toHaveAttribute("aria-expanded", "false", { timeout: 15000 });
    await expect(services.resourcesRoot).toBeHidden({ timeout: 15000 });
  });
});

test("AWS Services - expand a service row and open Cost Trend, verify the card titles itself Cost Trend and renders its Frequency filter and chart canvas", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  await openDrilldown(services, 0, DRILLDOWN_TABS[1]);

  await expect(services.costTrendRoot).toBeVisible({ timeout: 30000 });
  await expect(services.costTrendTitle).toBeVisible();
  await expect(services.frequencyFilter).toBeVisible();
  // The chart's own date picker and download sit in the card's toolbar; the canvas is
  // the one node that proves Chart.Bar mounted rather than only its chrome.
  await expect(services.costCanvas).toBeVisible({ timeout: 30000 });
  await expect(services.drilldownTab(DRILLDOWN_TABS[1])).toHaveAttribute("aria-selected", "true");
});

test("AWS Services - expand a service row and open Recommendations, verify the drill-down renders its Category filter and six columns or empty panel, set Category to Security, verify the trigger commits it and the table settles", { tag: ["@dev", "@regression", "@search", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  await openDrilldown(services, 0, DRILLDOWN_TABS[2]);

  await test.step("The recommendations listing mounts with its filter toolbar", async () => {
    await expect(services.recommendationsToolbar).toBeVisible({ timeout: 30000 });
    await expect(services.categoryFilter).toBeVisible();
    // The category filter arrives preset to the ALL sentinel (ServiceRecommendations.tsx:65-84).
    await expect(services.categoryFilter).toContainText("All Categories");
    await expect(services.recommendationsRows.first().or(services.recommendationsEmpty)).toBeVisible({ timeout: 60000 });
    if ((await services.recommendationsRows.count()) > 0) {
      for (const column of RECOMMENDATION_COLUMNS) {
        await expect(services.recommendationColumnHeader(column)).toBeVisible();
      }
    }
  });

  await test.step("Narrowing to Security commits and refetches", async () => {
    await chooseFilter(services, services.categoryFilter, "Security");
    await expect(services.categoryFilter).toContainText("Security");
    // A service with no security findings legitimately empties the table, so both the
    // rows and the empty panel count as the refetch having settled.
    await expect(services.recommendationsRows.first().or(services.recommendationsEmpty)).toBeVisible({ timeout: 60000 });
    for (let i = 0; i < (await services.recommendationsRows.count()); i++) {
      await expect(services.recommendationsRows.nth(i)).toContainText("Security");
    }
  });
});

test("AWS Services - expand a service row, expand its first resource in the Resources drill-down, verify the Details panel renders the resource's identity blocks, collapse it, verify the panel closes", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowIndex = await expandableServiceRow(services);
  test.skip(rowIndex < 0, NO_RESOURCE_ROWS_HINT);

  await openDrilldown(services, rowIndex, DRILLDOWN_TABS[0]);
  await expect(services.resourcesRows.first()).toBeVisible({ timeout: 60000 });

  const resourceToggle = services.resourceExpandToggle(0);
  await resourceToggle.click();
  await expect(resourceToggle).toHaveAttribute("aria-expanded", "true", { timeout: 15000 });
  await expect(services.resourceDetailsPanel).toBeVisible({ timeout: 30000 });
  // ResourceDetails shows a Loader until ListCloudResources answers; the labelled blocks
  // (or the no-detail fallback) are what proves the fetch completed and rendered.
  await expect(services.resourceDetailsContent).toBeVisible({ timeout: 60000 });

  await resourceToggle.click();
  await expect(resourceToggle).toHaveAttribute("aria-expanded", "false", { timeout: 15000 });
  await expect(services.resourceDetailsPanel).toBeHidden({ timeout: 15000 });
});

test("AWS Services - open the date range and choose Last Month, verify the trigger reads Last Month and the listing re-requests a spend window covering the previous calendar month", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);

  const before = log.entries.length;
  await services.dateRangeTrigger.click();
  await expect(services.dateShortcut(DATE_SHORTCUT)).toBeVisible({ timeout: 15000 });
  // A shortcut applies immediately and closes the popover (CustomDateTimeRangePicker.jsx:166-215).
  await services.dateShortcut(DATE_SHORTCUT).click();

  await expect(services.dateRangeTrigger).toContainText(DATE_SHORTCUT, { timeout: 15000 });

  const now = new Date();
  const expectedStart = new Date(now.getFullYear(), now.getMonth() - 1, 1);
  const expectedEnd = new Date(now.getFullYear(), now.getMonth(), 0, 23, 59, 59, 999);
  // The picker builds the window in the browser's local zone and the request carries
  // it as ISO/UTC, so the comparison allows a day and a half of zone drift either side.
  const tolerance = 36 * 60 * 60 * 1000;
  const sent = await nextQueryMatching(
    log,
    before,
    (e) => {
      const window = isServicesListing(e) ? spendWindow(e.query) : null;
      return window !== null && Math.abs(window.gte.getTime() - expectedStart.getTime()) < tolerance;
    },
    "a listing whose spend window starts last month"
  );
  const window = spendWindow(sent.query);
  expect(window).not.toBeNull();
  expect(Math.abs((window as { lte: Date }).lte.getTime() - expectedEnd.getTime())).toBeLessThan(tolerance);
  await waitForServices(services);
  await expect(services.servicesRows.first().or(services.servicesEmpty)).toBeVisible();
});

test("AWS Services - open a service row's actions menu, verify the Start, Stop, Terminate and Add Alarms actions are listed but every one is disabled, press Escape, verify the menu closes with the listing untouched", { tag: ["@dev", "@regression", "@negative", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  await services.rowActionsTrigger(0).click();

  for (const action of ROW_ACTIONS) {
    const item = services.rowActionItem(action.id, action.label);
    await expect(item).toBeVisible({ timeout: 15000 });
    // MUI MenuItem marks a disabled entry aria-disabled rather than removing it.
    await expect(item).toHaveAttribute("aria-disabled", "true");
  }
  await expect(services.visibleMenuItems()).toHaveCount(ROW_ACTIONS.length);

  await page.keyboard.press("Escape");
  await expect(services.visibleMenuItems()).toHaveCount(0, { timeout: 15000 });
  await expect(services.servicesRows).toHaveCount(rowCount);
});

test("AWS Services - read the footer's total, when it exceeds one page go to page 2 and verify the request offsets by ten and the footer starts at row 11, otherwise verify no second page is offered", { tag: ["@dev", "@regression", "@functional", "@oss"] }, async ({ page }) => {
  test.setTimeout(180000);

  const log = trackGroupingsQueries(page);
  const services = await openServicesTab(page);
  const rowCount = await serviceRowCount(services);
  test.skip(rowCount === 0, NO_SERVICES_HINT);

  const summary = ((await services.paginationSummary.innerText()) ?? "").trim();
  const total = Number((summary.match(/of ([\d,]+) results$/)?.[1] ?? "0").replace(/,/g, ""));
  expect(total, `unexpected pagination copy: "${summary}"`).toBeGreaterThan(0);

  if (total <= rowCount) {
    // Everything fits on one page, so MUI renders a single page button and no second.
    await expect(services.pageButton(2)).toHaveCount(0);
    return;
  }

  const before = log.entries.length;
  await services.pageButton(2).click();
  const sent = await nextQueryMatching(log, before, (e) => isServicesListing(e) && e.variables.offset === rowCount, "a second-page listing");
  expect(sent.variables.offset).toBe(rowCount);
  await expect(services.paginationSummary).toHaveText(new RegExp(`^Showing ${rowCount + 1}-`), { timeout: 30000 });
  await expect(services.currentPageButton(2)).toHaveAttribute("aria-current", /true|page/);
  await expect(services.pageButton(2)).toHaveCount(0);
});
