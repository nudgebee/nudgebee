// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

// Header global search — app/src/components/common/navigation/GlobalPageSearch.jsx,
// mounted by Header1.jsx on every page, so these locators work from any route.
//
// `grep -c 'data-testid' GlobalPageSearch.jsx` returns 1: the close button is the only
// element carrying a test contract. Every other handle below is either an accessible
// name (the trigger, the result rows) or one of the component's own stable ids — which
// is rung 3, correct here rather than a violation, since those elements render no
// testid and no stable accessible name of their own.

// The popover paper, keyed on the search input it contains rather than on
// `.MuiPopover-paper` alone — the header also mounts the cluster dropdown and the
// sidebar flyout as popovers, and an unkeyed match would resolve to whichever opened
// first. Every fallback below is scoped to this so `.or()` cannot drift to another
// overlay's copy of the same role.
const SEARCH_POPOVER = ".MuiPopover-paper:has(#global-search-input)";

// SectionCaption's id is the section label lowercased with spaces hyphenated.
const sectionId = (label: string): string => `#global-search-section-${label.toLowerCase().replace(/\s+/g, "-")}`;

export class GlobalSearchLocators extends CommonLocators {
  readonly popover: Locator;

  // Header trigger and the Ask-assistant shortcut beside it.
  readonly searchTrigger: Locator;
  readonly askAiTrigger: Locator;

  // Panel chrome.
  readonly searchInput: Locator;
  readonly optionsList: Locator;
  readonly footerHints: Locator;
  readonly closeBtn: Locator;
  readonly askAiTopBtn: Locator;

  // Every result row in the open panel. Deliberately plural and un-narrowed — it backs
  // the toHaveCount assertions; call sites that want one row take .first() themselves.
  readonly optionRows: Locator;

  // Mention-mode empty state — the only empty state that renders text of its own.
  readonly mentionNoResults: Locator;

  constructor(page: Page) {
    super(page);

    this.popover = page.locator(SEARCH_POPOVER);

    this.searchTrigger = page
      .getByRole("button", { name: "Search pages" })
      .or(page.locator("#auto-complete-global-page-search"))
      .first();

    // Accessible name is `Ask ${assistantName}`, and assistantName is tenant branding,
    // so the id leads and the name pattern backs it up.
    this.askAiTrigger = page.locator("#global-search-trigger-ask-ai").or(page.getByRole("button", { name: /^Ask / })).first();

    // The placeholder is the input's only accessible name and it changes with the
    // "@account" scope (searchPlaceholder in GlobalPageSearch.jsx), so it cannot be the
    // primary — the id is the stable handle, with the popover's single textbox behind it.
    this.searchInput = page.locator("#global-search-input").or(page.locator(SEARCH_POPOVER).getByRole("textbox")).first();

    this.optionsList = page
      .locator("#global-search-options-list")
      .or(page.locator(SEARCH_POPOVER).locator("[data-mention-mode]"))
      .first();

    this.footerHints = page
      .locator("#global-search-footer-hints")
      .or(page.locator(SEARCH_POPOVER).locator("div").filter({ hasText: /^Navigate/ }))
      .first();

    this.closeBtn = page.getByTestId("global-search-close-btn").or(page.getByRole("button", { name: "Close" })).first();

    this.askAiTopBtn = page.locator("#global-search-ask-ai-top").or(page.locator(SEARCH_POPOVER).getByRole("button", { name: /^Ask / })).first();

    this.optionRows = this.optionsList.getByRole("option");

    this.mentionNoResults = this.optionsList.getByText("No results found", { exact: true });
  }

  // Scoped result row. CommonLocators.getOption is page-wide, and the header's cluster
  // autocomplete renders role="option" too — reusing it here would resolve against
  // whichever list is higher in document order, so this narrows to the search panel.
  searchResultRow(name: string): Locator {
    return this.optionsList.getByRole("option", { name }).first();
  }

  // Caption above a run of rows sharing a sectionLabel — "Recents", "Suggested Pages",
  // "Dashboards", "Automations", "Integrations".
  sectionCaption(label: string): Locator {
    return this.page
      .locator(sectionId(label))
      .or(this.page.locator(SEARCH_POPOVER).getByText(label, { exact: true }))
      .first();
  }
}
