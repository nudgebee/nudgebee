// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { EventResolutionsLocators } from "./eventResolutionsLocators";
import {
  EVENT_RESOLUTIONS_FRAGMENT,
  PAGINATION_SUMMARY_PATTERN,
  TROUBLESHOOT_PATH,
} from "./eventResolutionsConstants";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not fail
// here — it fails later as a goto against "about:blank". Named up front so the run says which
// key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

const NO_LISTING_HINT =
  "The Event Resolutions pane never settled: neither the loaded table body " +
  "(#eventResolutionsTable-body) nor CustomTable's empty heading (#eventResolutionsTable-no-data) " +
  "appeared. Those are the two post-fetch shapes of app/src/components/troubleshoot/" +
  "EventResolutions.jsx, so neither showing means the listAllEventResolutions call never came back.";

// Opens the pane by hash rather than by clicking the strip. The page resolves its tab from
// window.location.hash on every hash change (pages/troubleshoot/index.jsx:236), so a deep link
// lands on it directly. Clicking the strip is exercised on its own by the navigation case, so
// the other tests do not all depend on that one interaction.
export async function openEventResolutions(page: Page, query = ""): Promise<EventResolutionsLocators> {
  const locators = new EventResolutionsLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`${TROUBLESHOOT_PATH}${query}#${EVENT_RESOLUTIONS_FRAGMENT}`);
  await expect(locators.listing, NO_LISTING_HINT).toBeVisible({ timeout: 60000 });
  await settleListing(locators);
  return locators;
}

// Waits out the listing fetch. While `loading` is true CustomTable swaps in a skeleton <tbody>
// that carries no id (CustomTable.jsx:859), so the id'd body being attached means the response
// has landed and the rows on screen are the current ones. The empty heading is the other
// settled outcome — which of the two shows depends on what the shared dev tenant holds, so
// neither can be demanded on its own.
export async function settleListing(locators: EventResolutionsLocators, timeout = 120000): Promise<void> {
  await expect(locators.tableBody.or(locators.emptyState).first(), NO_LISTING_HINT).toBeAttached({ timeout });
}

// Rows or the empty state — the two ends of a settled table.
export async function expectListingSettled(locators: EventResolutionsLocators): Promise<void> {
  await settleListing(locators);
  await expect(locators.rows.first().or(locators.emptyState).first()).toBeVisible({ timeout: 60000 });
}

// A cell can stack a secondary line under its primary one — the Status cell adds
// `status_message` (EventResolutions.jsx:259), the Resolver cell adds the resolver's name
// (:272). The value a filter selects on is always the first line.
export function firstLine(cellText: string): string {
  return (cellText.split("\n")[0] ?? "").trim();
}

// Opens a single-select filter's panel and picks one option. FilterDropdown's single-select
// path closes the popover itself as part of committing the pick (FilterDropdown.jsx:962-964),
// so the panel detaching is the signal the value was taken — not a sleep, and not a guess.
export async function selectFilterOption(locators: EventResolutionsLocators, trigger: Locator, label: string): Promise<void> {
  await expect(trigger).toBeEnabled();
  await trigger.click();

  const panel = locators.openFilterPanel();
  await expect(panel, `The ${label} filter panel never opened`).toBeVisible({ timeout: 30000 });

  const option = locators.filterOption(panel, label);
  await expect(option, `No "${label}" option in the filter panel`).toBeVisible({ timeout: 30000 });
  await option.click();
  await expect(panel).toHaveCount(0, { timeout: 30000 });
}

// Opens the Account filter and makes its options reachable. This one is a grouped MULTI-select,
// and grouped panels mount with every group collapsed — `openGroups` starts empty and only a
// search auto-expands a group (FilterDropdown.jsx:501, :577) — so no option row exists in the
// DOM until a group header is clicked. Which provider group is first is tenant data and does
// not matter; that its accounts become selectable is what the caller needs.
export async function openAccountFilterOptions(locators: EventResolutionsLocators): Promise<Locator> {
  await expect(locators.accountFilter).toBeEnabled();
  await locators.accountFilter.click();

  const panel = locators.openFilterPanel();
  await expect(panel, "The Account filter panel never opened").toBeVisible({ timeout: 30000 });

  const options = locators.filterOptions(panel);
  const groupHeaders = locators.filterGroupHeaders(panel);

  // The accounts arrive from an async getCloudAccounts (EventResolutions.jsx:47-51), so the panel
  // can open before the list lands. Wait for it to render something selectable — a group header
  // or an option row — BEFORE probing the option count: on an unpopulated panel that count reads
  // 0 for the wrong reason, and the branch below would then click into an empty popover.
  await expect(
    options.first().or(groupHeaders.first()),
    "The Account filter never populated — getCloudAccounts did not come back"
  ).toBeVisible({ timeout: 30000 });

  // Now 0 options means exactly one thing: the groups are still collapsed.
  if ((await options.count()) === 0) {
    await groupHeaders.first().click();
  }
  await expect(options.first(), "The Account filter offered no accounts").toBeVisible({ timeout: 30000 });
  return panel;
}

// Picks the first account the filter offers and returns its label. Multi-select keeps the panel
// open after a pick (only the single-select path closes it), so Escape is what dismisses it —
// left open it covers the listing the caller then asserts on.
export async function selectFirstAccount(locators: EventResolutionsLocators, page: Page): Promise<string> {
  const panel = await openAccountFilterOptions(locators);
  const option = locators.filterOptions(panel).first();

  const label = firstLine(await option.innerText());
  await option.click();
  await expect(option).toHaveAttribute("aria-selected", "true", { timeout: 30000 });

  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0, { timeout: 30000 });
  return label;
}

// The distinct first-line values a column currently renders, as the option spellings that
// select them. Used to choose a filter value the shared dev tenant actually holds, so a filter
// case asserts the filter working rather than asserting that dev happens to carry a given kind
// of resolution.
export async function distinctColumnValues(
  locators: EventResolutionsLocators,
  column: number,
  toOption: (cellText: string) => string = (cellText) => cellText
): Promise<string[]> {
  const texts = await locators.cellsInColumn(column).allInnerTexts();
  return Array.from(new Set(texts.map((text) => toOption(firstLine(text))).filter(Boolean)));
}

// The first option the filter offers that the rendered rows also carry, or null when the
// listing holds none of them. Null is a normal outcome on a shared environment and the caller
// asserts the empty branch instead, rather than driving a filter that can only return nothing.
export async function pickPopulatedOption(
  locators: EventResolutionsLocators,
  column: number,
  offered: readonly string[],
  toOption: (cellText: string) => string = (cellText) => cellText
): Promise<string | null> {
  await settleListing(locators);
  if ((await locators.rows.count()) === 0) return null;

  const present = await distinctColumnValues(locators, column, toOption);
  return offered.find((option) => present.includes(option)) ?? null;
}

// Asserts a whole column reads one value once the filtered fetch has landed. Wrapped in toPass
// so it retries around the refetch the filter kicks off: a plain read-then-compare races that
// request and judges the stale rows. Reads the cells directly — settleListing has already put
// the table in a terminal state, so a column that yields nothing is a real failure.
export async function expectColumnToRead(
  locators: EventResolutionsLocators,
  column: number,
  expected: string,
  toOption: (cellText: string) => string = (cellText) => cellText
): Promise<void> {
  await expect(async () => {
    const texts = await locators.cellsInColumn(column).allInnerTexts();
    expect(texts.length, `The filtered listing rendered no rows for "${expected}"`).toBeGreaterThan(0);
    for (const text of texts) {
      expect(toOption(firstLine(text))).toBe(expected);
    }
  }).toPass({ timeout: 60000, intervals: [1000, 2000, 4000] });
}

// The "Showing a-b of n results" line, parsed. CustomTable withholds the pagination block
// entirely while loading or with no rows (CustomTable.jsx:969), so this is only ever called
// after the listing has settled with at least one row.
export async function readPaginationSummary(
  locators: EventResolutionsLocators
): Promise<{ start: number; end: number; total: number }> {
  await expect(locators.paginationSummary).toBeVisible({ timeout: 60000 });
  const text = await locators.paginationSummary.innerText();
  const match = text.match(PAGINATION_SUMMARY_PATTERN);
  if (!match) {
    throw new Error(`The pagination summary did not read as a range — text was: ${text}`);
  }
  const toNumber = (value: string) => Number(value.replace(/,/g, ""));
  return { start: toNumber(match[1]), end: toNumber(match[2]), total: toNumber(match[3]) };
}

// The accountIds the URL currently carries, or "" when the filter has written none.
export function accountIdsInUrl(page: Page, param: string): string {
  return new URL(page.url()).searchParams.get(param) ?? "";
}
