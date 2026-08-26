// Not for OSS
import { Page, Locator } from "@playwright/test";
import { TroubleshootEventsLocators } from "../troubleshootEventsLocators";

// The three tabs app/src/pages/investigate.jsx renders through a11yProps(index).
// Only Tasks is unconditional: Investigation Analysis mounts when an AskAiCard
// matched, RCA Report when an RCACard did — both depend on what the event
// produced, so every lookup here has to tolerate their absence.
export const InvestigateTabs = {
  analysis: { id: "simple-tab-0", panelId: "simple-tabpanel-0", label: "Investigation Analysis" },
  tasks: { id: "simple-tab-1", panelId: "simple-tabpanel-1", label: "Tasks" },
  rca: { id: "simple-tab-2", panelId: "simple-tabpanel-2", label: "RCA Report" },
} as const;

export type InvestigateTab = (typeof InvestigateTabs)[keyof typeof InvestigateTabs];

// Labels in the More Actions menu, exactly as investigate.jsx spells them. The
// menu is assembled per event — isK8s gates Knowledge Base / Event Trend /
// Update Event, write access gates Classify Event, and Create Ticket disappears
// once a ticket is linked — so a test that needs one has to check it is offered.
export const MoreActions = {
  knowledgeBase: "Knowledge Base",
  eventTrend: "Event Trend",
  createTicket: "Create Ticket",
  classifyEvent: "Classify Event",
  updateEvent: "Update Event",
} as const;

export class InvestigateLocators extends TroubleshootEventsLocators {
  readonly eventsTable: Locator;
  readonly investigateLink: Locator;
  readonly tabStrip: Locator;
  readonly sidebarCollapseToggle: Locator;
  readonly sidebarExpandToggle: Locator;
  readonly sidebarWhereLabel: Locator;
  readonly podNameLink: Locator;
  readonly toggleLabelsBtn: Locator;
  readonly ownershipChainToggle: Locator;
  readonly moreActionsBtn: Locator;
  readonly openMenu: Locator;
  readonly askForInvestigationBtn: Locator;
  readonly followUpBtn: Locator;
  readonly generateRcaBtn: Locator;
  readonly refreshInvestigationBtn: Locator;

  constructor(page: Page) {
    super(page);

    // CustomTable puts the id KubernetesEvents.jsx hands it (line 290,
    // 'kubernetesEventsTable') straight onto the table. Deliberately no .or():
    // this scopes the Investigate link below, and the Events sub-tab mounts
    // other CustomTables — a wider match would take the link from whichever
    // table happened to render first and navigate to an unrelated event.
    this.eventsTable = page.locator("#kubernetesEventsTable");

    // KubernetesEvents.jsx renders data-testid='investigate-btn' on the row's
    // Investigate control. ds/Button spreads its rest props onto ButtonBase and
    // swaps in component='a' whenever href is set, so the rendered node is an
    // anchor — the fallback has to ask for a link, not a button.
    this.investigateLink = this.eventsTable
      .getByTestId("investigate-btn")
      .or(this.eventsTable.getByRole("link", { name: "Investigate" }))
      .first();

    // MUI Tabs renders role='tablist'. Deliberately no .or(): every tab lookup
    // is scoped to this strip, and a wider match would let some other tab row
    // stand in for one that never rendered.
    this.tabStrip = page.getByRole("tablist");

    // InvestigateSidebar.jsx sets both a testid and an aria-label on these two,
    // so rung 1 leads and rung 2 backs it. They are never both mounted: the
    // sidebar returns the expand rail early when collapsed.
    this.sidebarCollapseToggle = page
      .getByTestId("sidebar-collapse-toggle")
      .or(page.getByRole("button", { name: "Collapse panel" }))
      .first();
    this.sidebarExpandToggle = page
      .getByTestId("sidebar-expand-toggle")
      .or(page.getByRole("button", { name: "Expand details panel" }))
      .first();

    // The 'Where' row label is the cheapest proof the expanded sidebar body is
    // mounted rather than the collapsed rail. Deliberately no .or(): it is a
    // literal from the sidebar's own markup and any wider text match would find
    // the word elsewhere on the investigation.
    this.sidebarWhereLabel = page.getByText("Where", { exact: true });

    // Deliberately no .or(): the subject name this wraps is event data, so the
    // only wider handle would be a text match on a value that changes per event.
    this.podNameLink = page.getByTestId("pod-name-link").first();

    // Label text carries a live count ('Show More (7)'), so the fallback matches
    // the stable half only.
    this.toggleLabelsBtn = page
      .getByTestId("toggle-labels-btn")
      .or(page.getByRole("button", { name: /Show (More|Less)/ }))
      .first();

    this.ownershipChainToggle = page
      .getByTestId("ownership-chain-toggle")
      .or(page.getByRole("button", { name: /(Show|Hide) chain/ }))
      .first();

    // investigate.jsx passes data-testid='more-actions-btn' to ThreeDotsMenu, but
    // that component destructures a fixed prop list and never spreads the rest,
    // so the attribute is dropped and rung 1 is unavailable. What does reach the
    // DOM is the DsButton trigger's aria-label and its default id — see the PR's
    // Follow-ups.
    this.moreActionsBtn = page
      .getByRole("button", { name: "More actions" })
      .or(page.locator("#three-dot-menu"))
      .first();

    // ds/DropdownMenu puts role='menu' on its OverlaySurface, which renders a
    // div; the header's account menu is a plain MUI Menu, which renders a ul and
    // stays mounted. A bare [role="menu"] therefore matches both and trips strict
    // mode, so this pins the tag, takes only the open one, and ends in .first().
    // A wrong pick cannot pass quietly: the "known entries" test asserts every
    // label against the module's own five, so the account menu would fail it.
    this.openMenu = page.locator('div[role="menu"]:visible').first();

    // Tenant branding supplies the assistant name in the middle of this label
    // ('Ask <assistant> for Investigation'), so only the tail is matchable.
    this.askForInvestigationBtn = page.getByRole("button", { name: /for Investigation$/ }).first();

    this.followUpBtn = page
      .getByTestId("continue-with-analysis-btn")
      .or(page.getByRole("button", { name: "Ask a follow up" }))
      .first();

    // Label flips to 'Generating RCA...' while a generation is in flight, so the
    // fallback matches the stem both states share.
    this.generateRcaBtn = page
      .getByTestId("generate-rca-btn")
      .or(page.getByRole("button", { name: /Generat.* RCA/ }))
      .first();

    this.refreshInvestigationBtn = page
      .getByTestId("refresh-investigation-btn")
      .or(page.getByRole("button", { name: "Refresh investigation" }))
      .first();
  }

  // Role+name leads: MUI Tab exposes its label as the accessible name. 'Tasks'
  // carries a live count in a sibling span ('Tasks (4)'), so the name match is a
  // substring by design and the dom id a11yProps stamped backs it up.
  tab(tab: InvestigateTab): Locator {
    return this.tabStrip
      .getByRole("tab", { name: tab.label })
      .or(this.tabStrip.locator(`[id="${tab.id}"]`))
      .first();
  }

  // TabPanel renders hidden={value !== index} rather than unmounting, so the
  // panel node exists for every tab and visibility is what says which is open.
  tabPanel(tab: InvestigateTab): Locator {
    return this.page.locator(`[id="${tab.panelId}"]`);
  }

  // ds/OverlayItem renders MenuItem with component='div' and an explicit role,
  // and ThreeDotsMenu only ever gives it an index-derived id ('tdm-0') because
  // the investigate menu items declare no id of their own. That id shifts as
  // permissions add or drop entries, so the label — scoped to the open menu — is
  // the only stable handle.
  menuItem(label: string): Locator {
    return this.openMenu.locator('[role="menuitem"]').filter({ hasText: label }).first();
  }

  // ds/Modal labels its MUI Dialog via id='alert-dialog-title', so the modal
  // title is the dialog's accessible name.
  dialog(title: string): Locator {
    return this.page.getByRole("dialog", { name: title });
  }

  // ds/Modal's close control carries both an id and aria-label='Close'.
  dialogCloseBtn(title: string): Locator {
    return this.dialog(title)
      .getByRole("button", { name: "Close" })
      .or(this.dialog(title).locator("#close-modal-btn"))
      .first();
  }
}
