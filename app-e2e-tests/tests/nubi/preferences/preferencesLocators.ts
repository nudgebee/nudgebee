// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > Preferences (app/src/ee/components/memory2/PreferencesTab.jsx).
// `grep -c data-testid` returns 0 for that file, for the row/section helpers it
// composes (memory2/shared.jsx) and for ScopeToggle.jsx, so there is no rung-1
// handle anywhere on this surface. ds/Input does accept a data-testid prop, but
// PreferencesTab passes none, so nothing reaches the DOM.
// Controls with an accessible name — the tab strip, Save/Reset, the scope radios
// — are taken at rung 2. The bare text inputs are labelled by a sibling
// <Typography> inside SettingRow rather than a <label for>, so they have no
// accessible name at all and are reached by walking up from their own label.
// Most locators here are single-rung on purpose: the strings they match are the
// same strings the assertions are about, so a wider fallback would let a
// different element satisfy the very check the test exists to make.
export class PreferencesLocators extends CommonLocators {
  readonly preferencesTab: Locator;
  readonly agentsTab: Locator;
  readonly settingsDialog: Locator;
  readonly personalHeader: Locator;
  readonly tenantHeader: Locator;
  readonly provenanceChip: Locator;
  readonly scopeToggle: Locator;
  readonly personalScopeOption: Locator;
  readonly globalScopeOption: Locator;
  readonly workingContextSection: Locator;
  readonly manualInputsSection: Locator;
  readonly notificationsSection: Locator;
  readonly timezoneInput: Locator;
  readonly defaultNamespaceInput: Locator;
  readonly defaultEnvironmentRow: Locator;
  readonly preferredChannelsRow: Locator;
  readonly manualInputsTextarea: Locator;
  readonly saveBtn: Locator;
  readonly resetBtn: Locator;
  readonly bCortexDisabledPanel: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its Settings button are not redeclared —
    // tests/nubi/nubiLocators.ts owns them and openPreferencesTab() drives it.
    // SettingsModal renders its strip through shared/navigation/Tabs (MUI Tabs),
    // so every tab is a real role=tab even while scrolled out of view.
    // No text fallback: "Preferences" is also the first word of the tab's own
    // header, so a text match could return the heading instead of the tab.
    this.preferencesTab = page.getByRole("tab", { name: "Preferences" });
    this.agentsTab = page.getByRole("tab", { name: "Agents" });

    // Every locator below scopes to the Settings dialog so a matching string on
    // the page behind the modal cannot satisfy it. Filtered on "Preferences"
    // because that label is in the tab strip whichever tab is showing, which
    // keeps the anchor stable while tab content is being switched. Deliberately
    // single-rung: this is what everything below scopes to, so a wider fallback
    // here would widen all of them at once.
    this.settingsDialog = page.getByRole("dialog").filter({ hasText: "Preferences" }).first();

    // TabHeader's title is the one string that separates the two scopes, so it
    // doubles as the assertion that a scope switch took effect. Matched exactly
    // and with no fallback for that reason — a substring match would also hit
    // the tab label and both scopes would look identical.
    this.personalHeader = this.settingsDialog.getByText("Preferences — durable facts", { exact: true }).first();
    this.tenantHeader = this.settingsDialog.getByText("Preferences — tenant defaults", { exact: true }).first();

    // ds/Chip renders a plain span when it is not interactive, so this is a
    // scoped text match by necessity. No fallback: "Explicit" is the whole
    // assertion, and a looser match would find the word in the section copy.
    this.provenanceChip = this.settingsDialog.getByText("Explicit", { exact: true }).first();

    // ds/ToggleGroup renders role=group with the ariaLabel it is given, and each
    // single-selection option as role=radio carrying aria-checked. The radios
    // are scoped to that group so the aria-checked assertions cannot drift onto
    // another control; a text fallback would match the scope labels in the copy.
    this.scopeToggle = this.settingsDialog.getByRole("group", { name: "Memory scope" }).first();
    this.personalScopeOption = this.scopeToggle.getByRole("radio", { name: "Personal" });
    this.globalScopeOption = this.scopeToggle.getByRole("radio", { name: "Global" });

    // SectionCard titles, uppercased with CSS text-transform — the DOM text that
    // getByText reads keeps its original casing. Exact and single-rung: each of
    // these words also appears in the surrounding prose.
    this.workingContextSection = this.settingsDialog.getByText("Working context", { exact: true }).first();
    this.manualInputsSection = this.settingsDialog.getByText("Preference — manual inputs", { exact: true }).first();
    this.notificationsSection = this.settingsDialog.getByText("Notifications", { exact: true }).first();

    this.timezoneInput = this.rowControl("Timezone");
    this.defaultNamespaceInput = this.rowControl("Default namespace");

    // Default environment renders either a ds/Select or the "No accounts
    // connected yet." copy depending on what the tenant has wired up, so the row
    // itself is the only handle that exists in both shapes.
    this.defaultEnvironmentRow = this.settingRow("Default environment");
    this.preferredChannelsRow = this.settingRow("Preferred channels");

    // The only control on the tab with a unique placeholder, so it needs no
    // container scoping to be unambiguous. The fallback stays inside the dialog,
    // where this is also the only textarea.
    this.manualInputsTextarea = this.settingsDialog
      .getByPlaceholder("e.g. Always tag me on cost spikes above $200/day.")
      .or(this.settingsDialog.locator("textarea"))
      .first();

    // Save reads "Saving…" while a write is in flight, so exact:true keeps this
    // off the in-flight state and makes "disabled again" a meaningful signal.
    // No fallback: the enabled/disabled state of these two buttons is the whole
    // assertion in three cases, so matching a different button would pass a test
    // that proved nothing.
    this.saveBtn = this.settingsDialog.getByRole("button", { name: "Save", exact: true });
    this.resetBtn = this.settingsDialog.getByRole("button", { name: "Reset", exact: true });

    // Rendered in place of the whole editor when the tenant's b-Cortex flag is
    // off, and the one part of this surface that does carry a testid. Asserted
    // against so a disabled module reports itself by name instead of surfacing
    // as every control being absent. Deliberately not matched on "b-Cortex"
    // text: SettingsModal's own header renders an "Open b-Cortex" action, so a
    // text match would report the module disabled on every run.
    this.bCortexDisabledPanel = this.settingsDialog
      .getByTestId("bcortex-view-old-memory-btn")
      .or(this.settingsDialog.getByRole("button", { name: /old memory/i }))
      .first();

    // SnackbarComponent mounts at the app root, so it is a sibling of the
    // Settings dialog's portal and MUI marks it aria-hidden while that dialog is
    // open. That drops it out of the accessibility tree, so getByRole finds
    // nothing even though the node is in the DOM — matched by attribute instead.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // One SettingRow, found by walking up from its own label: the label sits in a
  // <Typography> whose parent Box holds the label plus its hint, and that Box's
  // parent is the row. Anchoring on the label rather than on position means a
  // reordered section cannot silently point a field assertion at another row.
  // Single-rung because this walk is itself the scoping step for rowControl.
  settingRow(label: string): Locator {
    return this.settingsDialog.getByText(label, { exact: true }).locator("xpath=../..").first();
  }

  // The input inside one labelled row. Both rungs are scoped to that row,
  // because SettingRow gives its controls no accessible name and a dialog-wide
  // textbox lookup would resolve in document order and land on whichever field
  // happens to come first — writing Timezone into Default namespace.
  rowControl(label: string): Locator {
    const row = this.settingRow(label);
    return row.getByRole("textbox").or(row.locator("input")).first();
  }

  // Toast text, kept inside the notifications region so a matching string
  // elsewhere on the page cannot pass for a toast. The fallback matches the
  // toast roles by attribute for the aria-hidden reason above.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }
}
