// Not for OSS
import { Page, Locator } from "@playwright/test";
import { TroubleshootLocators } from "./TroubleshootLocators";

// The seven All Events sub-tabs, in the order filterOptions declares them in
// app/src/pages/troubleshoot/index.jsx. `fragment` is the child half of the
// `#<parent>/<child>` hash; `id` is the dom id that page hands to Tabs.jsx.
export const EventSubTabs = {
  triageInbox: { id: "tab-fingerprint", fragment: "fingerprint", label: "Triage Inbox" },
  events: { id: "tab-all-events", fragment: "all", label: "Events" },
  groupByType: { id: "tab-event-type", fragment: "event-type", label: "Events group by type" },
  groupByApp: { id: "tab-event-app", fragment: "event-app", label: "Events group by app" },
  triageRules: { id: "tab-triage-rules", fragment: "triage-rules", label: "Triage Rules" },
  alertTuning: { id: "tab-threshold-suggestions", fragment: "threshold-suggestions", label: "Alert Tuning" },
  eventResolutions: { id: "tab-event-resolutions", fragment: "event-resolutions", label: "Event Resolutions" },
} as const;

export type EventSubTab = (typeof EventSubTabs)[keyof typeof EventSubTabs];

// Every All Events sub-tab, in render order — drives the "the strip is complete" check.
export const ALL_EVENT_SUB_TABS: EventSubTab[] = [
  EventSubTabs.triageInbox,
  EventSubTabs.events,
  EventSubTabs.groupByType,
  EventSubTabs.groupByApp,
  EventSubTabs.triageRules,
  EventSubTabs.alertTuning,
  EventSubTabs.eventResolutions,
];

// The Investigations tab's own two sub-tabs (filterOptions[1] in the same page).
export const InvestigationSubTabs = {
  autoInvestigated: { id: "tab-auto-investigated", fragment: "auto-investigated", label: "Auto Investigated" },
  manualInvestigated: { id: "tab-manual-investigated", fragment: "manual-investigated", label: "Manual Investigated" },
} as const;

export type InvestigationSubTab = (typeof InvestigationSubTabs)[keyof typeof InvestigationSubTabs];

export class TroubleshootEventsLocators extends TroubleshootLocators {
  readonly eventTabsBox: Locator;
  readonly triageRulesToolbar: Locator;
  readonly triageRulesListBox: Locator;
  readonly triageRulesSearch: Locator;
  readonly triageRulesStatusFilter: Locator;
  readonly triageRulesTableRows: Locator;
  readonly triageRulesEmptyState: Locator;
  readonly thresholdToolbar: Locator;
  readonly thresholdSourceFilter: Locator;
  readonly thresholdConfidenceFilter: Locator;
  readonly eventResolutionsListBox: Locator;
  readonly eventResolutionsDownload: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately no .or(): this is the container every sub-tab locator below is
    // scoped to, and the page renders a second tab strip (the AnchorComponent
    // parent pills) that a wider match would happily return instead — every
    // sub-tab lookup would then resolve against the wrong strip.
    this.eventTabsBox = page.locator("#troubleshoot-event-tabs");

    // Deliberately no .or(): TriageRulesManager.tsx puts this testid on the toolbar
    // itself. The only wider handle is the surrounding #triage-rules-list-box, which
    // would still be visible when the toolbar failed to render — a fallback that
    // turns a real failure into a pass is worse than none.
    this.triageRulesToolbar = page.getByTestId("triage-rules-filter-toolbar");
    // Deliberately no .or(): this scopes the empty-state lookup below, and the page
    // renders other ListingLayout cards on sibling sub-tabs — a wider match could
    // scope that lookup to a different listing and report the wrong tab's state.
    this.triageRulesListBox = page.locator("#triage-rules-list-box");

    // ds/SearchInput renders no data-testid and its <input> carries no accessible
    // name, so the id — which ds/Input puts straight onto the input element
    // (Input.tsx: `const inputId = id ?? reactId`) — is the highest usable rung.
    this.triageRulesSearch = this.triageRulesToolbar
      .locator("#triage-rules-search")
      .or(this.triageRulesToolbar.getByPlaceholder("Search by name"))
      .first();

    // FilterDropdown rewrites the id it is given as `auto-complete-<kebab id>`.
    this.triageRulesStatusFilter = this.triageRulesToolbar
      .locator("#auto-complete-triage-rules-filter-status")
      .or(this.triageRulesToolbar.getByText("Status", { exact: true }))
      .first();

    // CustomTable stamps `${id}-body` on its TableBody; the manager passes
    // tableId = 'triageRulesManager'. showExpandable renders a second <tr> per
    // record for the collapse panel, so counts here are only ever compared against
    // each other, never asserted as a record count.
    // Deliberately no .or(): any wider row match would pick up the rows of whatever
    // other table is mounted, turning "this listing emptied" into a false pass.
    this.triageRulesTableRows = page.locator("#triageRulesManager-body").locator("tr");

    // With no matching rule the manager swaps the table out for <EmptyData>, which
    // stamps `${id}-no-data` on its heading (common/EmptyData.jsx).
    this.triageRulesEmptyState = this.triageRulesListBox
      .getByRole("heading", { name: "No Data Available" })
      .or(this.triageRulesListBox.locator("#triage-rules-empty-no-data"))
      .first();

    // Same shape as the triage-rules toolbar, and no .or() for the same reason.
    this.thresholdToolbar = page.getByTestId("threshold-suggestions-filter-toolbar");
    this.thresholdSourceFilter = this.thresholdToolbar
      .locator("#auto-complete-threshold-suggestions-filter-source")
      .or(this.thresholdToolbar.getByText("Source", { exact: true }))
      .first();
    this.thresholdConfidenceFilter = this.thresholdToolbar
      .locator("#auto-complete-threshold-suggestions-filter-confidence")
      .or(this.thresholdToolbar.getByText("Confidence", { exact: true }))
      .first();

    // Deliberately no .or(): this is both the assertion that the resolutions listing
    // mounted and the scope for the download control, so a wider match would let a
    // neighbouring card stand in for a listing that never rendered.
    this.eventResolutionsListBox = page.locator("#event-resolutions");
    // DownloadButton sets aria-label='Download', so role+name leads over its id.
    this.eventResolutionsDownload = this.eventResolutionsListBox
      .getByRole("button", { name: "Download" })
      .or(this.eventResolutionsListBox.locator("#event-resolutions-download"))
      .first();
  }

  // CommonLocators keeps `page` protected, so the helpers cannot reach the mouse
  // through it. Parking the cursor in open content after a tab click is the fix
  // for AnchorComponent's hover popover swallowing the next click.
  async parkCursor(): Promise<void> {
    await this.page.mouse.move(640, 500);
  }

  // Role+name leads for six of the seven sub-tabs. It cannot lead for "Events":
  // getByRole matches the accessible name as a substring, and "Events" also sits
  // inside "Events group by type" and "Events group by app", so it resolves three
  // tabs. That one leads with the dom id filterOptions declares, and falls back to
  // the href Tabs.jsx builds for it — both scoped to the same strip. Tabs.jsx
  // renders no data-testid at all, so rung 1 is unavailable throughout.
  eventSubTab(tab: EventSubTab): Locator {
    if (tab === EventSubTabs.events) {
      return this.eventTabsBox
        .locator(`[id="${tab.id}"]`)
        .or(this.eventTabsBox.locator(`[href$="#all-events/${tab.fragment}"]`))
        .first();
    }
    return this.eventTabsBox
      .getByRole("tab", { name: tab.label })
      .or(this.eventTabsBox.locator(`[id="${tab.id}"]`))
      .first();
  }

  // The Investigations sub-tab row has no id of its own, so both rungs resolve at
  // page scope. Safe here: neither label is a substring of anything else rendered
  // on that tab, so the two cannot cross-resolve the way the All Events strip can.
  investigationSubTab(tab: InvestigationSubTab): Locator {
    return this.page
      .getByRole("tab", { name: tab.label })
      .or(this.page.locator(`[id="${tab.id}"]`))
      .first();
  }
}
