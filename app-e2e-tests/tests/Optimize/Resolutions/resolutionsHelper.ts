// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { RESOLUTIONS_TABLE } from "../optimizeModuleLocators";
import { ResolutionsLocators, RESOLUTION_STATUSES, ResolutionStatus } from "./resolutionsLocators";

// playwright.config.ts sets `baseURL: process.env.BASE_URL`, so an unset BASE_URL does not
// fail here — it fails later as a goto against "about:blank". Named up front so the run says
// which key is missing instead of reporting an unexplained navigation error.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

// Preconditions named once, so an environment that never rendered these fails with the
// reason rather than with an unexplained missing element on every test.
const NO_STRIP_HINT =
  "The Optimize tab strip never rendered. /optimise gates its tabs on the session's module grants " +
  "(insights / recommendations / autooptimize) — check the run user still has read access to them.";

const NO_VIEW_HINT =
  "The Resolutions view never mounted. /optimise#resolutions renders ResolutionsView only once the " +
  "Resolutions tab is the active tab — check the tab is still reachable for the run user.";

// The placeholder ResolutionsView puts on every card's value while the counts request is in
// flight (`cardsLoading ? '…' : count`). Waiting it out is what makes a card count readable.
const CARDS_LOADING_GLYPH = "…";

// A uuid that is syntactically valid but cannot name a real recommendation, so the deep-link
// assertions are about the scoping working rather than about what the shared dev tenant
// holds. Suffixed per run because this string reaches the URL and the backend query.
export function noMatchRecommendationId(): string {
  const suffix = `${Date.now().toString(16)}${Math.random().toString(16).slice(2)}`.padEnd(12, "0").slice(0, 12);
  return `00000000-0000-4000-8000-${suffix}`;
}

// Lands on the Resolutions tab by hash rather than by clicking the strip. The page resolves
// its tab from window.location.hash on every filterOptions change (app/src/pages/optimise/
// index.jsx), so a deep link opens it directly. Clicking the strip is exercised on its own by
// the reload test, so the other tests do not all depend on that one interaction.
export async function openResolutionsTab(page: Page, query = ""): Promise<ResolutionsLocators> {
  const locators = new ResolutionsLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`/optimise${query}#resolutions`);
  await expect(locators.SummaryTab, NO_STRIP_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.ResolutionsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
  await expect(locators.resolutionsPage, NO_VIEW_HINT).toBeVisible({ timeout: 60000 });

  return locators;
}

// Waits out the listing fetch. CustomTable swaps in an unid'd skeleton tbody while loading,
// so the id'd body being attached means the response has landed; the empty state is the
// other terminal state.
export async function waitForResolutions(locators: ResolutionsLocators): Promise<void> {
  await locators.waitForTable(RESOLUTIONS_TABLE, locators.resolutionsEmpty);
}

// Rows or the empty state — the two ends of a settled table. Which one a shared environment
// produces is not what these tests are about, so this is what they assert instead.
export async function expectListingSettled(locators: ResolutionsLocators): Promise<void> {
  await waitForResolutions(locators);
  await expect(locators.resolutionsRows.first().or(locators.resolutionsEmpty).first()).toBeVisible({ timeout: 60000 });
}

// The counts request is separate from the listing's, so a card can still read "…" after the
// table has settled. Every count read goes through this first.
export async function waitForCards(locators: ResolutionsLocators): Promise<void> {
  await expect(locators.allResolutionsCard).toBeVisible({ timeout: 60000 });
  await expect(locators.allResolutionsCard).not.toContainText(CARDS_LOADING_GLYPH, { timeout: 60000 });
}

// A card renders its Stat label above the count, and the count is locale-formatted, so the
// value is the last digit group in the card's text. Read directly rather than through a
// catch: waitForCards has already settled the card, so a missing one is a real failure.
export async function readCardCount(card: Locator): Promise<number> {
  const text = (await card.innerText()).trim();
  const groups = text.match(/[\d,]+/g);
  if (!groups?.length) {
    throw new Error(`Status card rendered no numeric count — text was: ${text}`);
  }
  return Number(groups[groups.length - 1].replace(/,/g, ""));
}

// The first status whose card carries a non-zero count, or null when the tenant holds no
// resolutions at all. A zero card is muted and inert by design (ResolutionsView drops its
// onClick), so a test that clicked one blindly would assert against a control that cannot
// respond — which is a property of the shared environment, not of the module.
export async function pickPopulatedStatus(locators: ResolutionsLocators): Promise<ResolutionStatus | null> {
  await waitForCards(locators);

  for (const key of Object.keys(RESOLUTION_STATUSES) as ResolutionStatus[]) {
    if ((await readCardCount(locators.cardFor(key))) > 0) return key;
  }
  return null;
}

// aria-pressed is what WidgetCard exposes for "this card is the active filter", and unlike
// the row count it is in step with the click rather than with the refetch behind it.
export async function expectCardPressed(card: Locator): Promise<void> {
  await expect(card).toHaveAttribute("aria-pressed", "true", { timeout: 30000 });
}

export async function expectCardNotPressed(card: Locator): Promise<void> {
  await expect(card).toHaveAttribute("aria-pressed", "false", { timeout: 30000 });
}

// Picks one option out of a FilterDropdown.
export async function selectFilterOption(page: Page, trigger: Locator, optionLabel: string): Promise<void> {
  await expect(trigger).toBeEnabled();
  await trigger.click();

  // The popover paper is the only thing that always renders when the panel opens.
  // FilterDropdown draws its search box only for more than 8 options (or freeSolo), and
  // Severity has five — so asserting on the search box reported "never opened" for a panel
  // that had in fact opened (CI run 33131503138). `.last()` is the one just opened.
  const panel = page.locator(".MuiPopover-paper:visible").last();
  await expect(panel, "The filter panel never opened").toBeVisible({ timeout: 30000 });

  // Not getByRole("option"): MUI marks the popover's container aria-hidden while it is open,
  // so the accessibility tree exposes no options and getByRole matches zero. The CSS role
  // attribute survives — the form every other spec in this suite uses, including
  // AutoOptimize's pickFilterOption against this same component. Substring rather than an
  // anchored match: no severity label is a prefix of another, and the option row's checkbox
  // makes a whole-string match brittle.
  const option = panel.locator('[role="option"]').filter({ hasText: optionLabel }).first();
  await expect(option, `No "${optionLabel}" option in the filter panel`).toBeVisible({ timeout: 30000 });
  await option.click();
  await expect(option).toHaveAttribute("aria-selected", "true", { timeout: 30000 });

  // Escape closes the panel: a multi-select one stays open after a pick, and left open it
  // covers the listing the caller then asserts on.
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0, { timeout: 30000 });
}

// Opens the row-click side panel from a listed row. The click lands on the Account cell: the
// row handler ignores clicks that originate inside a button, link or role-bearing node
// (CustomTable's ExpandableTableRow), and Account is the one cell that renders plain text
// only — the Recommendation and Type cells carry links, and the actions cell stops
// propagation outright.
export async function openFirstResolutionPanel(locators: ResolutionsLocators): Promise<void> {
  await locators.resolutionsRows.first().locator("td:nth-child(1)").click();
  await expect(locators.detailPanel).toBeVisible({ timeout: 30000 });
}
