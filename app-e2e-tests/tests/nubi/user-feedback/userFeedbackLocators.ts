// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > User Feedback (app/src/components/llm/UserFeedbackTab.jsx).
// The tab renders ZERO data-testids of its own — verified with
// `grep -c data-testid app/src/components/llm/UserFeedbackTab.jsx` => 0 — and it
// hands CustomTable no `id`, so there is no table id either. What it does give
// is accessible names: ds/FilterDropdown renders its trigger as a real
// <button> carrying its label, so the filters are reachable at rung 2 with the
// component's derived id as the fallback. The only testids on the surface come
// from CustomDateTimeRangePicker's popover.
export class UserFeedbackLocators extends CommonLocators {
  readonly insightsGroupTab: Locator;
  readonly feedbackTab: Locator;
  readonly digestsTab: Locator;
  readonly listingCard: Locator;
  readonly listingTitle: Locator;
  readonly table: Locator;
  readonly tableBody: Locator;
  readonly loadingSkeletons: Locator;
  readonly emptyState: Locator;
  readonly moduleFilterTrigger: Locator;
  readonly usefulFilterTrigger: Locator;
  readonly dateRangeTrigger: Locator;

  constructor(page: Page) {
    super(page);

    // The panel entry point and its b-Cortex button stay in tests/nubi/nubiLocators.ts;
    // openUserFeedbackTab() drives that class to get into the modal. User Feedback
    // relocated from Settings to b-Cortex's "Insights" group (docs/ia-consolidation-plan.md,
    // PR 4), relabeled "Feedback" there, alongside its sibling "Digests". Both the
    // top-level group tab and its sub-tabs render through shared/navigation/Tabs
    // (MUI Tabs), so every one of them is a real role=tab even while scrolled out
    // of view. exact:true on the group tab: without it "Insights" would also match
    // nothing else here, but it stays exact for the same reason the Knowledge
    // group tab does (knowledgeBaseLocators.ts) — and so does every sub-tab
    // below whose accessible name is not doubled (see Feedback).
    this.insightsGroupTab = page.getByRole("tab", { name: "Insights", exact: true });
    // Not exact, unlike its two siblings: Tabs.jsx passes the tab's own text as
    // the icon's `alt`, and Feedback is the one sub-tab here whose icon is an
    // image asset rather than an MUI icon component (FeedbackBlueIcon in
    // BCortexModal's INSIGHTS_SUB_TABS_CONFIG) — so its accessible name is
    // "Feedback Feedback" and an exact match resolves to nothing. The text
    // filter is what keeps the loose name off "My Usage" or any later sibling
    // that merely contains the word.
    this.feedbackTab = page.getByRole("tab", { name: "Feedback" }).filter({ hasText: /^Feedback$/ });
    this.digestsTab = page.getByRole("tab", { name: "Digests", exact: true });

    // ListingLayout puts its `id` on the wrapping DS Card, so this is the whole
    // listing: toolbar, table and empty state. Deliberately id-only — the Card is
    // a plain styled div with no role, and it is what every locator below scopes
    // to, so a wider fallback here would widen all of them.
    this.listingCard = page.locator("#user-feedback-tab");

    // Not exact: ListingLayout.Toolbar renders the ScopeChip inside the same
    // title node, so the element's own text is "User Feedback" plus the chip's
    // account label and an exact match never resolves.
    this.listingTitle = this.listingCard.getByText("User Feedback").first();

    this.table = this.listingCard.getByRole("table").or(this.listingCard.locator("table")).first();
    this.tableBody = this.table.locator("tbody").first();

    // ds/Skeleton renders aria-busy='true', which CustomTable puts in every cell
    // of its skeleton body. It is the only signal that a refetch is still in
    // flight, and waiting on it is what keeps a row count off the placeholder rows.
    this.loadingSkeletons = this.listingCard.locator('[aria-busy="true"]');

    // CustomTable's EmptyData heading when the filtered query returns nothing.
    this.emptyState = this.listingCard.getByText("No Data Available", { exact: true }).first();

    // FilterDropdown's trigger always renders its label, plus the selected
    // option's label once one is picked — so the accessible name starts with the
    // label either way. The fallback is the component's own derived id:
    // `auto-complete-${toKebabCase(id || label)}`, and neither dropdown here is
    // given an `id`, so both fall back to their label.
    this.moduleFilterTrigger = this.listingCard
      .getByRole("button", { name: /^Module/ })
      .or(this.listingCard.locator("#auto-complete-module"))
      .first();
    this.usefulFilterTrigger = this.listingCard
      .getByRole("button", { name: /^Useful/ })
      .or(this.listingCard.locator("#auto-complete-useful"))
      .first();

    // Matched on text, NOT on role+name: CustomDateTimeRangePicker wraps this
    // button in a MUI Tooltip, and a Tooltip with describeChild unset puts its
    // title on the child as aria-label — here the absolute range as a locale
    // datetime string. That aria-label overrides the button's text as its
    // accessible name, so getByRole({ name }) can never see the display text.
    // Text content is the display text: a shortcut name ("Last 24 Hours") or the
    // range form, which is month-first because toLocaleDateString("en-US",
    // { day: "numeric", month: "short" }) renders "Aug 26", not "26 Aug". Both
    // icons are <img>, so neither adds text. No fallback: the trigger is given no
    // id and no testid, and every other button in this card is a filter trigger
    // whose text is its own label, so a wider match would grab one of those.
    this.dateRangeTrigger = this.listingCard
      .locator("button")
      .filter({ hasText: /^(Last\s|Current\s|\w{3}\s\d{1,2}\s-\s\w{3}\s\d{1,2})/ })
      .first();
  }

  columnHeader(name: string): Locator {
    return this.table.getByRole("columnheader", { name, exact: true }).or(this.table.locator("th").filter({ hasText: name })).first();
  }

  rows(): Locator {
    return this.tableBody.locator("tr");
  }

  // The two settled shapes of the listing. Asserting this before counting cells
  // is what stops a count being taken while the tab is between renders: an empty
  // cell set only counts as "nothing matched the filter" once the empty state is
  // actually on screen.
  firstRowOrEmptyState(): Locator {
    return this.rows().first().or(this.emptyState);
  }

  // Module and Useful have no per-cell handle, so each is addressed by the
  // position its header occupies in HEADERS_USER_FEEDBACK. Column order is part
  // of what the sanity case asserts, which is what keeps these honest.
  moduleCells(): Locator {
    return this.tableBody.locator("tr > td:nth-child(1)");
  }

  usefulCells(): Locator {
    return this.tableBody.locator("tr > td:nth-child(2)");
  }

  // FilterDropdown swaps the trigger's chevron for a clear (x) svg once a value
  // is selected, and that svg owns the clear handler. It carries no role or
  // name, so it is a scoped CSS match by necessity — scoped to the trigger, so
  // it can only ever be that dropdown's own control.
  clearControl(trigger: Locator): Locator {
    return trigger.locator("svg").last();
  }

  dateShortcut(label: string): Locator {
    const testId = `date-range-shortcut-${label.replace(/\s+/g, "-").toLowerCase()}`;
    return this.page.getByTestId(testId).or(this.page.getByRole("button", { name: label, exact: true })).first();
  }

  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  visibleOptions(): Locator {
    return this.page.locator('[role="option"]:visible');
  }

  // Opens a toolbar filter and commits `label`. FilterDropdown only renders its
  // search box above eight options, so the wait is on the options themselves.
  async chooseFilter(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.visibleOptions().first().waitFor({ state: "visible", timeout: 15000 });
    await this.filterOption(label).click();
    // The trigger shows its label plus the committed value, so this is the
    // signal that the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label, { timeout: 15000 });
  }

  // Waits out the skeleton body so a row count cannot be taken off placeholders.
  async waitForRowsSettled(): Promise<void> {
    await expect(this.loadingSkeletons).toHaveCount(0, { timeout: 45000 });
  }
}
