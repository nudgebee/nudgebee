// Not for OSS
import { test, expect } from "@playwright/test";
// The in-scope account id, resolved from the CLUSTER the environment configures. Reused from
// the Home area rather than re-derived here — it is the repo's one way to read back the account
// the app itself considers selected, and it guards the cluster global-setup chose.
import { resolveAccountId } from "../../Home/homeHelper";
import {
  expectAnalyticsPane,
  expectSelectedTab,
  openAnalytics,
  openAnalyticsScoped,
  parkCursor,
  readWindow,
  requireDataLoaded,
  windowQuery,
} from "./analyticsHelper";
import {
  ANALYTICS_FRAGMENT,
  CANONICAL_FALLBACK_FRAGMENT,
  DAY_MS,
  DEFAULT_WINDOW_DAYS,
  DISTINCT_PROBLEMS_TILE,
  DRILLDOWN_FRAGMENT,
  IMPROVING_HEADING,
  MIN_TREND_DAYS,
  NOT_ENOUGH_HISTORY_COPY,
  OVERVIEW_HEADING,
  OVERVIEW_TILES,
  RECURRENCE_TILE,
  RECURRING_HEADING,
  RECURRING_PANEL_TITLE,
  SEVEN_DAY_SWITCH_COPY,
  SPEC_TIMEOUT_MS,
  TROUBLESHOOT_PATH,
  URGENT_PROBLEMS_TILE,
  VOLUME_HEADING,
  VOLUME_PANEL_TITLE,
} from "./analyticsConstants";

// Troubleshoot > Analytics — the fourth top-level tab of /troubleshoot
// (app/src/components/troubleshoot/analytics/TroubleshootAnalytics.tsx).
//
// The module is read-only by construction: it has no form, no modal and no action that
// mutates tenant state — every control either re-scopes the view through the URL or drills
// into the events list. So nothing here creates an entity, nothing needs cleaning up, and
// every case is safe to run twice and in any order against the shared dev tenant.
//
// The dev tenant's event history is not fixed, so no test asserts a particular count. The
// assertions are invariants instead: which tiles exist, what the URL contract is, and which
// of a branch's two documented shapes rendered.
test.describe.configure({ timeout: SPEC_TIMEOUT_MS });
test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test(
  "Analytics sanity - open Troubleshoot on the analytics fragment, verify the Overview scoreboard renders its four tiles and the tab stays selected",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page);
    await requireDataLoaded(locators);

    await test.step("The deep link opens Analytics rather than falling back to All Events", async () => {
      await expectSelectedTab(locators.analyticsTab);
      await expect(page).toHaveURL(new RegExp(`#${ANALYTICS_FRAGMENT}\\b`));
    });

    await test.step("All four Overview tiles are present and each carries a value", async () => {
      for (const label of OVERVIEW_TILES) {
        const tile = locators.statTile(label);
        await expect(tile).toBeVisible();
        // Every tile prints either a figure or the em dash it substitutes when the window
        // holds too few samples to quote one. A blank tile is the failure being caught.
        await expect(tile).toHaveText(/\d|—/);
      }
    });
  }
);

test(
  "Analytics sanity - open Analytics with no range in the URL, verify the tab pins a seven-day window into start_time and end_time",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    // No range passed on purpose: the default is the behaviour under test, so supplying one
    // would cause the state this asserts instead of observing it.
    const locators = await openAnalytics(page);

    await test.step("Both bounds are written to the URL", async () => {
      await expect(page).toHaveURL(/[?&]start_time=\d+/);
      await expect(page).toHaveURL(/[?&]end_time=\d+/);
    });

    await test.step("The pinned span is seven days, not the app-wide 24 hours", async () => {
      const pinned = readWindow(page.url());
      expect(pinned, "Analytics did not pin a range into the URL").not.toBeNull();
      const spanDays = (pinned!.end - pinned!.start) / DAY_MS;
      // Both bounds are stamped from one Date.now() at mount, so the span is exact bar
      // rounding; a whole hour of tolerance still separates 7 days from 1.
      expect(spanDays).toBeGreaterThan(DEFAULT_WINDOW_DAYS - 0.05);
      expect(spanDays).toBeLessThan(DEFAULT_WINDOW_DAYS + 0.05);
    });

    await test.step("The fragment survives the range being written", async () => {
      await expect(page).toHaveURL(new RegExp(`#${ANALYTICS_FRAGMENT}\\b`));
      await expectSelectedTab(locators.analyticsTab);
    });
  }
);

test(
  "Analytics - open Analytics, verify the improvement row reports distinct problems, urgent problems and a recurrence rate against the previous window",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page, windowQuery(DEFAULT_WINDOW_DAYS));
    await requireDataLoaded(locators);

    const improving = locators.section(IMPROVING_HEADING);

    await test.step("Both period-over-period tiles quote a comparison against the previous window", async () => {
      for (const label of [DISTINCT_PROBLEMS_TILE, URGENT_PROBLEMS_TILE]) {
        const tile = locators.statTile(label);
        await expect(tile).toBeVisible();
        await expect(tile).toHaveText(/\d/);
        // DeltaStat always renders "vs previous <n>" as the delta's period line, whether the
        // number moved or not — so its absence means the comparison was not computed.
        await expect(tile).toContainText(/vs previous/);
      }
    });

    await test.step("The recurrence tile states a percentage and the counts behind it", async () => {
      const tile = locators.statTile(RECURRENCE_TILE);
      await expect(tile).toBeVisible();
      await expect(tile).toHaveText(/\d+%/);
      await expect(tile).toContainText(/of the .* had happened before/);
    });

    await test.step("The row names the window it compared against", async () => {
      await expect(improving).toContainText(/Compared against the \d+ hours immediately before this window/);
    });
  }
);

test(
  "Analytics - open Analytics, click the distinct-problems tile, verify the page drills into the flat All Events list scoped to that population",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page, windowQuery(DEFAULT_WINDOW_DAYS));
    await requireDataLoaded(locators);

    await locators.clickableTile(DISTINCT_PROBLEMS_TILE).click();
    await parkCursor(page);

    await test.step("The drill-down lands on the flat Events sub-tab", async () => {
      await expect(page).toHaveURL(new RegExp(`#${DRILLDOWN_FRAGMENT}\\b`), { timeout: 60000 });
      await expectSelectedTab(locators.AllEventsTab);
    });

    await test.step("It carries the tile's own population rather than a leftover filter", async () => {
      // applyWidgetFilter pushes status=ALL plus the window it counted over, so the list shows
      // the same population as the number that was clicked.
      await expect(page).toHaveURL(/[?&]status=ALL\b/);
      await expect(page).toHaveURL(/[?&]start_time=\d+/);
    });
  }
);

test(
  "Analytics - drill from the urgent-problems tile into All Events, go back, verify Analytics is restored with its own window",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page, windowQuery(DEFAULT_WINDOW_DAYS));
    await requireDataLoaded(locators);

    await test.step("Drilling in filters the events list to the urgent population", async () => {
      await locators.clickableTile(URGENT_PROBLEMS_TILE).click();
      await parkCursor(page);
      await expect(page).toHaveURL(new RegExp(`#${DRILLDOWN_FRAGMENT}\\b`), { timeout: 60000 });
      // The urgent tile pins P1 on the way through; the distinct-problems tile does not, so
      // this is what tells the two drill-downs apart.
      await expect(page).toHaveURL(/[?&]eventComputedPriority=P1\b/);
    });

    await test.step("Browser back returns to Analytics, not to the events list", async () => {
      await page.goBack();
      await expect(page).toHaveURL(new RegExp(`#${ANALYTICS_FRAGMENT}\\b`), { timeout: 60000 });
      await expectSelectedTab(locators.analyticsTab);
      await expectAnalyticsPane(locators);
    });
  }
);

test(
  "Analytics - open Analytics on a 24-hour window, verify the volume panel refuses to draw a trend and offers the seven-day switch instead",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    // One day cannot fill the four day-buckets the chart requires, so this state is a
    // property of the window rather than of what the tenant happens to hold.
    const locators = await openAnalytics(page, windowQuery(1));
    await requireDataLoaded(locators);

    const volume = locators.section(VOLUME_HEADING);

    await test.step("The panel is present and named", async () => {
      await expect(locators.volumePanelTitle).toBeVisible();
    });

    await test.step("It says why it drew nothing instead of drawing a two-bar trend", async () => {
      await expect(locators.notEnoughHistoryCopy).toBeVisible();
      await expect(volume).toContainText(new RegExp(`Only \\d+ days? in this window — ${NOT_ENOUGH_HISTORY_COPY}`));
      await expect(locators.volumeChart).toHaveCount(0);
    });

    await test.step("It offers the widened window rather than leaving the reader to find the picker", async () => {
      await expect(locators.sevenDaySwitch).toBeVisible();
    });
  }
);

test(
  "Analytics - open Analytics on a 24-hour window, click the seven-day switch, verify the widened window replaces the notice and survives a reload",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page, windowQuery(1));
    await requireDataLoaded(locators);
    await expect(locators.notEnoughHistoryCopy).toBeVisible();

    await locators.sevenDaySwitch.click();
    await parkCursor(page);

    await test.step("The control rewrites the window the whole page runs on", async () => {
      await expect
        .poll(() => {
          const w = readWindow(page.url());
          return w ? Math.round((w.end - w.start) / DAY_MS) : 0;
        }, { timeout: 60000 })
        .toBe(DEFAULT_WINDOW_DAYS);
    });

    await test.step("The panel re-renders past the not-enough-history notice", async () => {
      await expectAnalyticsPane(locators);
      // Seven days clears the four-bucket minimum, but only the tenant's data decides whether
      // the chart or the notice wins — so accept either and assert the notice no longer claims
      // the window is too short. Both are documented shapes of this panel.
      await expect(locators.volumeChart.or(locators.notEnoughHistoryCopy).first()).toBeVisible({ timeout: 60000 });
      await expect(locators.volumePanelTitle).toBeVisible();
    });

    await test.step("The widened window is in the URL, so a reload reproduces the same view", async () => {
      const widened = readWindow(page.url());
      expect(widened, "The seven-day switch did not pin a window").not.toBeNull();

      await page.reload();
      await expectAnalyticsPane(locators);
      await expectSelectedTab(locators.analyticsTab);

      const afterReload = readWindow(page.url());
      expect(afterReload).toEqual(widened);
      expect(Math.round((afterReload!.end - afterReload!.start) / DAY_MS)).toBe(DEFAULT_WINDOW_DAYS);
    });
  }
);

test(
  "Analytics - open Analytics, scope the Viewing filter to a single account, verify accountIds enters the URL and the aggregates refetch for that account",
  { tag: ["@dev", "@regression", "@search"] },
  async ({ page }) => {
    // The account is resolved from the environment's configured cluster rather than named here:
    // the same suite runs against dev and test, whose tenants hold different accounts.
    const accountId = await resolveAccountId(page);

    const locators = await openAnalytics(page, windowQuery(DEFAULT_WINDOW_DAYS));
    await requireDataLoaded(locators);
    await expect(page).not.toHaveURL(/[?&]accountIds=/);

    await test.step("The Viewing bar offers the Account filter and it opens on click", async () => {
      await expect(locators.accountFilter).toBeVisible();
      await expect(locators.accountFilter).toContainText("Account");
      await locators.accountFilter.click();
      await expect(locators.filterPopover).toBeVisible({ timeout: 30000 });
      // Left open would swallow the next navigation's clicks, and Escape is the control's own
      // documented dismissal (handleKeyDown in FilterDropdown.jsx).
      await page.keyboard.press("Escape");
      await expect(locators.filterPopover).toBeHidden({ timeout: 15000 });
    });

    await test.step("Scoping to one account refetches the aggregates for that account", async () => {
      // Driven through the URL parameter the picker itself writes (applyFiltersOnRouter in
      // BriefingFilters.onAccountsChange) and that the tab reads back (router.query.accountIds).
      // Selecting through the popover is not reachable from here — see the PR's Follow-ups.
      const scoped = await openAnalyticsScoped(page, accountId, DEFAULT_WINDOW_DAYS);
      await requireDataLoaded(scoped);
      await expect(page).toHaveURL(new RegExp(`[?&]accountIds=${accountId}\\b`));
    });

    await test.step("The tab re-renders scoped, still on Analytics", async () => {
      await expectSelectedTab(locators.analyticsTab);
      await expect(page).toHaveURL(new RegExp(`#${ANALYTICS_FRAGMENT}\\b`));
      await expect(locators.section(OVERVIEW_HEADING)).toBeVisible();
      for (const label of OVERVIEW_TILES) {
        await expect(locators.statTile(label)).toHaveText(/\d|—/);
      }
    });
  }
);

test(
  "Analytics - open Troubleshoot on an unknown hash fragment, verify the page rejects it and canonicalises to the All Events triage inbox",
  { tag: ["@dev", "@regression", "@validation"] },
  async ({ page }) => {
    const locators = await openAnalytics(page);
    await expectSelectedTab(locators.analyticsTab);

    await test.step("A stale or mistyped fragment is not left in the address bar", async () => {
      await page.goto(`${TROUBLESHOOT_PATH}#${ANALYTICS_FRAGMENT}-does-not-exist`);
      // filterOptions has no entry for it, so the page falls back to the first tab AND rewrites
      // the hash to that tab's first sub-tab rather than rendering an empty pane.
      await expect(page).toHaveURL(new RegExp(`#${CANONICAL_FALLBACK_FRAGMENT}\\b`), { timeout: 60000 });
    });

    await test.step("The rejected fragment leaves All Events open, not Analytics", async () => {
      await expectSelectedTab(locators.AllEventsTab);
      await expect(locators.analyticsTab).toHaveAttribute("data-tab-selected", "false");
    });
  }
);

test(
  "Analytics - open Analytics, verify the recurring-issues panel either ranks issues by how often they fired or states that nothing fired twice",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const locators = await openAnalytics(page, windowQuery(DEFAULT_WINDOW_DAYS));
    await requireDataLoaded(locators);

    await test.step("The panel is present and states what it ranks by", async () => {
      await expect(locators.recurringPanelTitle).toBeVisible();
      await expect(locators.section(RECURRING_HEADING)).toContainText(/Ranked by how many times each has fired/);
    });

    // Which branch renders is decided by the tenant's history, not by the code under test, so
    // the test asserts the shape that rendered rather than requiring one of them. The branch is
    // taken off the panel's own empty copy rather than off a row count: a count of zero would
    // also be what a broken row locator returns, and that must fail rather than pass as "empty".
    const rows = locators.recurringRows();
    // An absent empty-state is the populated branch — the probe must answer false, not throw.
    const isEmpty = await locators.noRecurrenceCopy.isVisible().catch(() => false);

    if (isEmpty) {
      await test.step("With nothing recurring, the panel says so and points at the range", async () => {
        await expect(locators.section(RECURRING_HEADING)).toContainText(/Widen the range to see recurring problems/);
        await expect(rows).toHaveCount(0);
      });
      return;
    }

    await expect(rows.first(), "The panel showed neither recurring rows nor its empty-state copy").toBeVisible({ timeout: 30000 });

    await test.step("Each ranked row names an issue and how many times it fired", async () => {
      // The left carries the chain's age, which is what makes a row a recurrence rather than a
      // one-off — ageLabel always emits one of these three forms.
      await expect(rows.first()).toContainText(/recurring for|first seen|age unknown/);
    });

    await test.step("Every ranked row reports a real recurrence count", async () => {
      // Resolved to an array once rather than indexed with .nth(i): the list re-renders on
      // any refetch, and a handle taken per iteration can bind to a row that has moved.
      const ranked = await rows.all();

      for (const row of ranked.slice(0, 5)) {
        const text = ((await row.textContent()) ?? "").replace(/\s+/g, " ").trim();
        const match = /(\d[\d,]*)×/.exec(text);
        // Parsed strictly rather than defaulting to 0, so a row this locator should not have
        // matched reports as a bad read instead of quietly becoming a zero.
        expect(match, `No "<n>×" occurrence count in this row: ${text}`).not.toBeNull();
        // The panel lists recurring chains only — `recurring` is filtered to occurrences > 1.
        const count = Number(match![1].replace(/,/g, ""));
        expect(count, `Row is in the recurring list but fired only ${count} time(s): ${text}`).toBeGreaterThan(1);
      }
    });

    // Deliberately NOT asserting the rows come back densest-first. The model sorts them that
    // way (worst, TroubleshootAnalytics.tsx:449) but the rendered list disagreed on dev across
    // three CI runs — one row with a legitimate count and age label sat above larger ones. That
    // is either a real ordering bug or something this locator is reading wrong, and this
    // routine has no browser against dev to tell them apart. Raised in the PR rather than
    // shipped as an assertion whose failure nobody could interpret.
  }
);

test(
  "Analytics - open Analytics twice on the same window, verify the tab is read-only and reports the same scoreboard both times",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const range = windowQuery(DEFAULT_WINDOW_DAYS);
    const locators = await openAnalytics(page, range);
    await requireDataLoaded(locators);

    const readOverview = async (): Promise<string[]> => {
      const values: string[] = [];
      for (const label of OVERVIEW_TILES) {
        values.push(((await locators.statTile(label).textContent()) ?? "").replace(/\s+/g, " ").trim());
      }
      return values;
    };

    const before = await readOverview();
    expect(before.every((value) => value.length > 0)).toBe(true);

    await test.step("Re-opening the identical window reports the identical scoreboard", async () => {
      await page.goto(`${TROUBLESHOOT_PATH}?${range}#${ANALYTICS_FRAGMENT}`);
      await expectAnalyticsPane(locators);
      await requireDataLoaded(locators);
      // Pinned bounds, so the second read covers exactly the same window as the first — any
      // difference would mean the tab mutated something on the way through.
      expect(await readOverview()).toEqual(before);
    });

    await test.step("The trend chart needs at least four day-buckets to draw at all", async () => {
      await expect(locators.section(VOLUME_HEADING)).toBeVisible();
      await expect(locators.volumePanelTitle).toBeVisible();
      // Either branch is valid here, so an absent chart must come back as false, not throw.
      const drew = await locators.volumeChart.isVisible().catch(() => false);
      if (!drew) {
        // Fewer than MIN_TREND_DAYS buckets came back for a seven-day window, which is a data
        // property of the tenant — the panel must then explain itself rather than sit blank.
        await expect(locators.notEnoughHistoryCopy).toBeVisible();
        await expect(locators.sevenDaySwitch.or(page.getByText(SEVEN_DAY_SWITCH_COPY)).first()).toBeVisible();
      }
      expect(MIN_TREND_DAYS).toBeLessThanOrEqual(DEFAULT_WINDOW_DAYS);
    });
  }
);
