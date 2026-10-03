// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Insights > Digests (app/src/ee/components/memory2/DigestsTab.jsx).
// `grep -c data-testid` returns 2 for that file — both on the re-run controls, which
// are therefore reached at rung 1 below. Everything else on the tab has no testid:
// shared.jsx (TabHeader), ds/Card, ds/Chip and ds/EmptyState render none, so the rest
// of this file is rung 2 where a control has an accessible name (the tab strip, the
// week rail buttons, the modal's Cancel) and rung 4 text scoped to the b-Cortex dialog
// where the element has no role at all (the header, the scoreboard, the section
// headings, the rail's per-week count line).
export class DigestsLocators extends CommonLocators {
  readonly insightsGroupTab: Locator;
  readonly memoryGroupTab: Locator;
  readonly digestsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly tabDescription: Locator;
  readonly emptyState: Locator;
  readonly listErrorState: Locator;
  readonly digestErrorText: Locator;
  readonly noWeekSelectedText: Locator;
  readonly weekIncidentCounts: Locator;
  readonly weekButtons: Locator;
  readonly secondaryFigures: Locator;
  readonly rerunWeekBtn: Locator;
  readonly rerunConfirmDialog: Locator;
  readonly rerunConfirmBtn: Locator;
  readonly rerunCancelBtn: Locator;
  readonly whatBrokeHeading: Locator;
  readonly findingSummaries: Locator;
  readonly openFindingBodies: Locator;
  readonly bCortexDisabledPanel: Locator;

  constructor(page: Page) {
    super(page);

    // The b-Cortex tab strips are MUI Tabs (BCortexModal.jsx renders them through
    // shared/navigation/Tabs), so every tab is a real role=tab. Re-declared here rather
    // than imported from another surface's locators class: each tests/nubi/<surface>/
    // directory owns its own strip locators, which is what keeps one surface's rename
    // from editing another surface's file.
    this.insightsGroupTab = page.getByRole("tab", { name: "Insights", exact: true });
    // Memory is b-Cortex's default landing group, so it is the cheapest sibling to
    // bounce off to unmount DigestsTab and force a refetch. Preferred over the two
    // Insights siblings: Feedback is gated behind requiresFeedbackScope and My Usage
    // mounts the consumption tab, while Memory is unconditional.
    this.memoryGroupTab = page.getByRole("tab", { name: "Memory", exact: true });
    this.digestsTab = page.getByRole("tab", { name: "Digests", exact: true });

    // Everything below scopes to the b-Cortex dialog so a matching string on the page
    // behind the modal cannot satisfy it. Filtered on "Digests" because that label is
    // in the Insights tab strip whichever Insights sub-tab is showing, which keeps the
    // anchor stable while tab content is being swapped. Deliberately single-rung: this
    // is what every locator below scopes to, so a fallback here would widen all of them.
    this.bCortexDialog = page.locator('[role="dialog"]').filter({ hasText: "Digests" }).first();

    // TabHeader's description. This, not the title, is the "the Digests tab is mounted"
    // signal: the title is the bare word "Digests", which the Insights tab label also
    // carries, while this sentence appears exactly once on the surface.
    this.tabDescription = this.bCortexDialog
      .getByText(
        "Weekly reliability reviews synthesised from this account’s event analyses — what broke, what recurred, and what to do next.",
        { exact: true }
      )
      .first();

    // ds/EmptyState titles for the tab's two no-content states. DigestsTab tracks the
    // failure explicitly rather than collapsing it into "nothing yet", so these are two
    // different strings and the tests assert which one rendered.
    this.emptyState = this.bCortexDialog.getByText("No digests yet", { exact: true }).first();
    this.listErrorState = this.bCortexDialog.getByText("Could not load digests", { exact: true }).first();

    // The per-week read failing, as opposed to the list failing.
    this.digestErrorText = this.bCortexDialog
      .getByText("Could not load the review for this week. Select it again to retry.", { exact: true })
      .first();
    this.noWeekSelectedText = this.bCortexDialog.getByText("Select a week to view its review.", { exact: true }).first();

    // The rail's per-week secondary line, "N incidents". Anchored with ^$ so it cannot
    // also match a sentence elsewhere in the review that happens to end in that word,
    // and it doubles as a per-week anchor: the date range is its immediately preceding
    // sibling and the week button is its parent.
    this.weekIncidentCounts = this.bCortexDialog.getByText(/^\d+ incidents$/);

    // Each rail entry is a Box rendered as component='button', so it is a real
    // role=button. Filtered on the count line because the rail entries are the only
    // buttons on this surface that carry it.
    this.weekButtons = this.bCortexDialog.getByRole("button").filter({ hasText: /\d+ incidents/ });

    // The muted line under the scoreboard. Matched as a whole rather than by one of its
    // parts because DigestsTab joins all five figures unconditionally — every one of
    // them always resolves to a number — so a missing segment is a real regression
    // rather than an absent-by-data section.
    this.secondaryFigures = this.bCortexDialog.getByText(
      /^\d+ recurring \(\d+%\) · \d+ new · \d+ classes across \d+ services · \d+% P1 · \d+ learnings captured$/
    );

    // Rung 1: DigestsTab passes this testid explicitly and ds/Button forwards it. The
    // accessible-name fallback is scoped to the same dialog, and covers the label
    // flipping to "Generating…" while a run is in flight.
    this.rerunWeekBtn = this.bCortexDialog
      .getByTestId("digest-rerun-week-btn")
      .or(this.bCortexDialog.getByRole("button", { name: /^(Generate this week again|Generating…)$/ }))
      .first();

    // The confirmation is its own ds/Modal, a sibling dialog of b-Cortex rather than a
    // node inside it, so it is matched from the page and not from bCortexDialog.
    this.rerunConfirmDialog = page.locator('[role="dialog"]').filter({ hasText: "Generate this week again?" }).first();
    // Rung 1 again. This suite NEVER clicks this button — see digestsHelper.ts.
    this.rerunConfirmBtn = this.rerunConfirmDialog
      .getByTestId("digest-rerun-confirm-btn")
      .or(this.rerunConfirmDialog.getByRole("button", { name: "Generate", exact: true }))
      .first();
    this.rerunCancelBtn = this.rerunConfirmDialog.getByRole("button", { name: "Cancel", exact: true }).first();

    // The "What broke & why" section heading, and the incident rows under it.
    this.whatBrokeHeading = this.bCortexDialog.getByText("What broke & why", { exact: true }).first();

    // ds/Accordion renders each incident through MUI's Accordion, so the clickable row
    // is a role=button carrying aria-expanded. Reached by MUI class rather than by role
    // name and with no .or() fallback on purpose: the summary's accessible name is the
    // whole row — label, headline and every meta chip (priority, account, environment,
    // event count, confidence, owner) concatenated — so a name-based match would be a
    // different string per finding and per tenant, and a bare getByRole("button") would
    // also collect the week rail and the re-run control.
    this.findingSummaries = this.bCortexDialog.locator(".MuiAccordionSummary-root");
    // MUI keeps a collapsed panel's details mounted but hidden, so the open bodies are
    // the visible ones — a plain count of details would report every row as open.
    this.openFindingBodies = this.bCortexDialog.locator(".MuiAccordionDetails-root:visible");

    // Rendered in place of the whole tab when the tenant's b-Cortex flag is off
    // (BCortexDisabled.jsx). Asserted against so a disabled module reports itself by
    // name instead of surfacing as every locator above being absent.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();
  }

  // TabHeader puts the title Typography and the description Typography in one Box,
  // title first. Anchoring on the description rather than on the word "Digests" is what
  // keeps this off the Insights tab label, which carries the same string.
  get headerTitle(): Locator {
    return this.tabDescription.locator("xpath=preceding-sibling::*[1]");
  }

  // One rail entry, reached from its own count line: DigestsTab renders the count
  // Typography inside the week button, so the button is its parent.
  weekButton(index: number): Locator {
    return this.weekIncidentCounts.nth(index).locator("xpath=..");
  }

  // The week's "Mon D – Mon D" range, the count line's immediately preceding sibling.
  // Anchoring on the count rather than on position means a week that gained another
  // line cannot silently point this at the wrong text.
  weekDateRange(index: number): Locator {
    return this.weekIncidentCounts.nth(index).locator("xpath=preceding-sibling::*[1]");
  }

  // The status chip a rail entry carries when its digest is not fully generated.
  // DigestsTab renders it only for a non-'generated' status, so its absence is the
  // healthy case rather than a missing element. ds/Chip variant='tag' has neither a
  // role nor a testid, so this is rung 5, scoped to the one week button.
  weekStatusChip(index: number): Locator {
    return this.weekButton(index).locator('[data-variant="tag"]');
  }

  // One scoreboard tile's value, reached from its own label. DigestsTab renders the
  // value Typography first and the label second inside the same Card, so the value is
  // the label's preceding sibling — which is why this cannot be read positionally.
  scoreboardValue(label: string): Locator {
    return this.bCortexDialog.getByText(label, { exact: true }).first().locator("xpath=preceding-sibling::*[1]");
  }

  scoreboardLabel(label: string): Locator {
    return this.bCortexDialog.getByText(label, { exact: true }).first();
  }
}
