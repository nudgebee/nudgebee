// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Memory > Privacy (app/src/ee/components/memory2/PrivacyTab.jsx).
// `grep -c data-testid` returns 0 for that file and for the ScopeToggle it
// composes, so the only rung-1 handle on this surface is the b-Cortex
// placeholder. The tab strip and the scope radios carry accessible names and are
// taken at rung 2. PrivacyRow passes neither `label` nor `aria-label` to
// ds/Switch, so inputProps['aria-label'] resolves to undefined and the switches
// have no accessible name at all — each is reached by walking up from its own
// help line, the only string unique to its row.
export class PrivacyLocators extends CommonLocators {
  readonly privacyTab: Locator;
  readonly patternsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly personalDescription: Locator;
  readonly tenantDescription: Locator;
  readonly tenantWarningBanner: Locator;
  readonly scopeToggle: Locator;
  readonly personalScopeOption: Locator;
  readonly globalScopeOption: Locator;
  readonly masterRow: Locator;
  readonly masterSwitch: Locator;
  readonly soulRow: Locator;
  readonly soulSwitch: Locator;
  readonly preferencesRow: Locator;
  readonly preferencesSwitch: Locator;
  readonly patternsRow: Locator;
  readonly patternsSwitch: Locator;
  readonly decisionsRow: Locator;
  readonly decisionsSwitch: Locator;
  readonly sessionsRow: Locator;
  readonly sessionsSwitch: Locator;
  readonly collectiveRow: Locator;
  readonly collectiveSwitch: Locator;
  readonly layerRows: Locator[];
  readonly layerSwitches: Locator[];
  readonly masterRowSwitches: Locator;
  readonly layerRowSwitches: Locator[];
  readonly bCortexDisabledPanel: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its "b-Cortex" button are not redeclared —
    // tests/nubi/nubiLocators.ts owns them and openPrivacyTab() drives them.
    // Privacy relocated from the (now-deleted) Settings modal into
    // BCortexModal.jsx's Memory group as a sub-tab (docs/ia-consolidation-plan.md
    // PR 4) — Memory is b-Cortex's default landing group, so "Privacy" is a real
    // role=tab as soon as the modal opens, no extra top-level-tab click needed.
    // BCortexModal renders its strips through shared/navigation/Tabs (MUI Tabs),
    // so every tab is a real role=tab carrying aria-selected even while scrolled
    // out of view. No text fallback: "Privacy" is also this tab's own header
    // title, so a text match could return the heading instead of the tab.
    this.privacyTab = page.getByRole("tab", { name: "Privacy" });
    // Sibling Memory sub-tab used to remount Privacy (leave and come back) —
    // Memory's own default sub-tab, so it's always reachable without a filter
    // check of its own the way Privacy itself needs one.
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });

    // Everything below scopes to the b-Cortex dialog so a matching string on the
    // page behind the modal cannot satisfy it. Filtered on "Privacy" because
    // that label is in the tab strip whichever tab is showing, which keeps the
    // anchor stable while tab content is being switched. Deliberately
    // single-rung: this is what every locator below scopes to, so a wider
    // fallback here would widen all of them at once.
    this.bCortexDialog = page.getByRole("dialog").filter({ hasText: "Privacy" }).first();

    // The TabHeader title is the bare word "Privacy", which is also the tab
    // label, so it cannot tell the two apart. The header's description can:
    // these two substrings are the one string that differs between the scopes,
    // so each doubles as the assertion that a scope switch took effect. Matched
    // as substrings that exclude the tenant's assistant name, which is branding
    // config and differs per tenant, and that carry no typographic punctuation.
    this.personalDescription = this.bCortexDialog.getByText(/Turning a layer off stops injection/).first();
    this.tenantDescription = this.bCortexDialog.getByText(/Tenant-wide opt-out/).first();

    // ds/Banner renders plain copy, so this is a scoped text match by necessity.
    // Anchored on the banner's own opening clause rather than on the phrase it
    // shares with the Global description, which would match both.
    this.tenantWarningBanner = this.bCortexDialog.getByText(/These toggles apply to every user in this tenant/).first();

    // ds/ToggleGroup renders role=group with the ariaLabel it is given, and each
    // single-selection option as role=radio carrying aria-checked. The radios are
    // scoped to that group so the aria-checked assertions cannot drift onto
    // another control; a text fallback would match the scope labels in the copy.
    this.scopeToggle = this.bCortexDialog.getByRole("group", { name: "Memory scope" }).first();
    this.personalScopeOption = this.scopeToggle.getByRole("radio", { name: "Personal" });
    this.globalScopeOption = this.scopeToggle.getByRole("radio", { name: "Global" });

    // Rows are anchored on their help line, not their label: four of the seven
    // labels ("Soul", "Preferences", "Sessions", "Privacy" itself) are also
    // b-Cortex tab labels inside this same dialog, so a label match would resolve
    // to the tab strip. Every help line here is unique to its own row.
    this.masterRow = this.privacyRow("Master switch. When off, no layer is injected.");
    this.masterSwitch = this.rowSwitch(this.masterRow);
    this.soulRow = this.privacyRow("Communication style, voice, and values.");
    this.soulSwitch = this.rowSwitch(this.soulRow);
    this.preferencesRow = this.privacyRow("Explicit defaults like namespace, cloud, channels.");
    this.preferencesSwitch = this.rowSwitch(this.preferencesRow);
    this.patternsRow = this.privacyRow("Behaviour patterns inferred across your chats.");
    this.patternsSwitch = this.rowSwitch(this.patternsRow);
    this.decisionsRow = this.privacyRow("Choices you made during investigations.");
    this.decisionsSwitch = this.rowSwitch(this.decisionsRow);
    this.sessionsRow = this.privacyRow("Short-term working memory within a chat.");
    this.sessionsSwitch = this.rowSwitch(this.sessionsRow);
    this.collectiveRow = this.privacyRow("Team-wide knowledge shared across users.");
    this.collectiveSwitch = this.rowSwitch(this.collectiveRow);

    // The six layers the master switch gates, in the order PrivacyTab renders
    // them. The master row is deliberately not a member: it is the control being
    // flipped in the cascade cases, never one of the rows that reacts to it.
    this.layerRows = [this.soulRow, this.preferencesRow, this.patternsRow, this.decisionsRow, this.sessionsRow, this.collectiveRow];
    this.layerSwitches = [
      this.soulSwitch,
      this.preferencesSwitch,
      this.patternsSwitch,
      this.decisionsSwitch,
      this.sessionsSwitch,
      this.collectiveSwitch,
    ];

    // The same switches without the trailing .first(), so a row can be asserted
    // to hold exactly one. Counting the .first() form would prove nothing: it
    // resolves to at most one element by construction, so a row walk that landed
    // on a wrapper holding several switches would still report a count of 1.
    this.masterRowSwitches = this.rowSwitches(this.masterRow);
    this.layerRowSwitches = this.layerRows.map((row) => this.rowSwitches(row));

    // Rendered in place of the whole panel when the tenant's b-Cortex flag is
    // off (BCortexDisabled.jsx). Asserted against so a disabled module reports
    // itself by name instead of surfacing as every row being absent. Matched on
    // its own title, not its former "View legacy memory layer" button — that
    // button only rendered while NubiBrainNav.jsx passed BCortexModal an
    // onOpenSettingsMemory callback, removed along with the non-admin-gated
    // "Memory" rail button it opened (Decision AB) — the title still renders
    // unconditionally whenever the module is off.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();

    // SnackbarComponent mounts at the app root, so it is a sibling of the
    // b-Cortex dialog's portal and MUI marks it aria-hidden while that dialog is
    // open. That drops it out of the accessibility tree, so getByRole finds
    // nothing even though the node is in the DOM — matched by attribute instead.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // One PrivacyRow, found by walking up from its own help line: the help sits in
  // a <Typography> whose parent Box holds the label, the help and the optional
  // provenance line, and that Box's parent is the row that also holds the switch.
  // Anchoring on the help text rather than on position means a reordered list
  // cannot silently point a switch assertion at another layer. Single-rung
  // because this walk is itself the scoping step for rowSwitch.
  privacyRow(help: string): Locator {
    return this.bCortexDialog.getByText(help, { exact: true }).locator("xpath=../..").first();
  }

  // The switch inside one row. Both rungs are scoped to that row, because
  // PrivacyRow gives its switches no accessible name and a dialog-wide checkbox
  // lookup would resolve in document order and land on whichever row comes first
  // — turning off Soul while the test believed it was turning off Patterns.
  rowSwitch(row: Locator): Locator {
    return row.getByRole("checkbox").or(row.locator('input[type="checkbox"]')).first();
  }

  // Every switch in one row, for the count assertion. Single-rung on purpose:
  // .or() unions its two sides, so the role and attribute forms would each match
  // the same input and report a count of 2 for a row that holds exactly one.
  rowSwitches(row: Locator): Locator {
    return row.locator('input[type="checkbox"]');
  }

  // Toast text, kept inside the notifications region so a matching string
  // elsewhere on the page cannot pass for a toast. The fallback matches the toast
  // roles by attribute for the aria-hidden reason above.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }
}
