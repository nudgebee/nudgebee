// Not for OSS
import { Page, Locator } from "@playwright/test";
import { TroubleshootLocators } from "../TroubleshootLocators";
import {
  ACCOUNT_FILTER_ID,
  ANALYTICS_TAB_NAME,
  CHART_ID,
  ERROR_BANNER_TITLE,
  NOT_ENOUGH_HISTORY_COPY,
  NO_RECURRENCE_COPY,
  RECURRING_PANEL_TITLE,
  SEVEN_DAY_SWITCH_COPY,
  VOLUME_PANEL_TITLE,
} from "./analyticsConstants";

// This whole surface renders ZERO data-testid attributes, so rung 1 of the ladder does not
// exist here. Verified before writing a locator:
//   grep -c data-testid app/src/components/troubleshoot/analytics/TroubleshootAnalytics.tsx -> 0
//   ... and the same for every primitive it composes: ds/Stat, ds/Banner, ds/WidgetCard,
//   ds/Skeleton, ds/FilterDropdown and common/charts/TimeSeriesChart -> all 0.
// So each locator below takes the highest rung that IS available: role+name for anything the
// component gives an accessible name, the three real ids the app renders, then exact text.
export class AnalyticsLocators extends TroubleshootLocators {
  readonly analyticsTab: Locator;
  readonly volumeChart: Locator;
  readonly accountFilter: Locator;
  readonly filterPopover: Locator;
  readonly errorBanner: Locator;
  readonly recurringPanelTitle: Locator;
  readonly volumePanelTitle: Locator;
  readonly noRecurrenceCopy: Locator;
  readonly notEnoughHistoryCopy: Locator;
  readonly sevenDaySwitch: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders the tab as a MUI Button with component={Link}, so the id is
    // primary and the anchor's accessible name is a same-element fallback.
    this.analyticsTab = page
      .locator(`[id="anchor-tab-${ANALYTICS_TAB_NAME}"]`)
      .or(page.getByRole("link", { name: ANALYTICS_TAB_NAME, exact: true }))
      .first();

    // TimeSeriesChart puts the `id` prop on its wrapper Box; the canvas inside it is the
    // only canvas this pane draws, so it is a fallback scoped to the same pane.
    this.volumeChart = page.locator(`#${CHART_ID}`).or(page.locator(`#${CHART_ID} canvas`)).first();

    // FilterDropdown renders its trigger as `component='button'` carrying
    // `auto-complete-${toKebabCase(id)}`, so the accessible name is a same-element fallback.
    this.accountFilter = page.locator(`#${ACCOUNT_FILTER_ID}`).or(page.getByRole("button", { name: /Account/ })).first();

    // The options render in a MUI Popover portalled out of the filter bar, so every option
    // and group-header locator scopes to this rather than to the trigger's container.
    this.filterPopover = page.locator(".MuiPopover-root").first();

    this.errorBanner = page.getByText(ERROR_BANNER_TITLE, { exact: true }).first();

    // Panel titles and empty-state copy are plain Typography with no role and no id. Each
    // string was confirmed unique across app/src before being used, so an exact-text match
    // cannot collide with another surface.
    this.recurringPanelTitle = page.getByText(RECURRING_PANEL_TITLE, { exact: true }).first();
    this.volumePanelTitle = page.getByText(VOLUME_PANEL_TITLE, { exact: true }).first();
    this.noRecurrenceCopy = page.getByText(NO_RECURRENCE_COPY).first();
    this.notEnoughHistoryCopy = page.getByText(NOT_ENOUGH_HISTORY_COPY).first();

    // Rendered as a Box with role='button', so its text is its accessible name.
    this.sevenDaySwitch = page
      .getByRole("button", { name: new RegExp(SEVEN_DAY_SWITCH_COPY) })
      .or(page.getByText(SEVEN_DAY_SWITCH_COPY))
      .first();
  }

  // A section is the Box wrapping SectionHeading plus its content. The heading Typography is
  // the only labelled node in it, so the section is reached from that label rather than from a
  // class chain — `xpath=..` from a labelled anchor, not a positional guess.
  section(heading: string): Locator {
    return this.page.getByText(heading, { exact: true }).first().locator("xpath=ancestor::div[2]");
  }

  // ds/Stat nests its label four divs deep: root > column > row > label group > <span>. Taking
  // the ancestor rather than the span is what makes the tile's VALUE readable, since the value
  // is a sibling of the label group, not of the label. Structure per app/src/components/common/ds/Stat.tsx.
  statTile(label: string): Locator {
    return this.page.getByText(label, { exact: true }).first().locator("xpath=ancestor::div[4]");
  }

  // The clickable tiles are WidgetCards with role='button' whose accessible name is composed
  // from their contents, so the label is enough to name them — rung 2, no id needed.
  clickableTile(label: string): Locator {
    return this.page.getByRole("button", { name: new RegExp(label) }).first();
  }

  // Rows of the "what keeps coming back" list. Each is a role='button' inside the panel, and
  // the panel is reached from its own title so a row cannot resolve against another list.
  //
  // Filtered on BOTH halves of a chain row: the occurrence count it ends with ("<n>×") and the
  // age label ageLabel() always emits. role='button' alone is not specific enough — CI proved
  // it also matches something carrying a bare "<n>×"-shaped number, which sorted out of place
  // and read as an ordering bug in the panel rather than as a stray element in the locator.
  recurringRows(): Locator {
    return this.section("What keeps coming back?")
      .getByRole("button")
      .filter({ hasText: /\d+×/ })
      .filter({ hasText: /recurring for|first seen|age unknown/ });
  }
}
