// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > Soul (app/src/ee/components/memory2/SoulTab.jsx).
// `grep -c data-testid` returns 0 for that file and for every component it
// composes — OptionCards.jsx, ExpandableTextArea.jsx, ScopeToggle.jsx and
// shared.jsx — so there is no rung-1 handle anywhere on this surface. The one
// exception is BCortexDisabled.jsx, which carries a single testid and is the
// panel this suite asserts is absent.
// Controls with an accessible name are taken at rung 2: the tab strip, the
// Save/Reset/Done buttons, the scope radios, the View radios, and the option
// cards — ds/Card renders role="button" whenever `interactive` is set, so every
// tone / verbosity / format choice is a real button named by its own copy.
// Section titles and field labels have no role at all and are reached at rung 4,
// scoped to the Settings dialog, with an xpath walk from the label to the box
// that holds its controls.
// Most locators here are single-rung on purpose: the strings they match are the
// same strings the assertions are about, so a wider fallback would let a
// different element satisfy the very check the test exists to make.
export class SoulLocators extends CommonLocators {
  readonly soulTab: Locator;
  readonly patternsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly personalHeader: Locator;
  readonly tenantHeader: Locator;
  readonly provenanceChip: Locator;
  readonly scopeToggle: Locator;
  readonly personalScopeOption: Locator;
  readonly globalScopeOption: Locator;
  readonly toneVerbositySection: Locator;
  readonly formatSection: Locator;
  readonly domainShorthandSection: Locator;
  readonly domainShorthandTextarea: Locator;
  readonly expandEditorBtn: Locator;
  readonly expandDialog: Locator;
  readonly expandViewToggle: Locator;
  readonly expandEditOption: Locator;
  readonly expandPreviewOption: Locator;
  readonly expandTextarea: Locator;
  readonly expandPreview: Locator;
  readonly expandDoneBtn: Locator;
  readonly saveBtn: Locator;
  readonly resetBtn: Locator;
  readonly bCortexDisabledPanel: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its "b-Cortex" button are not redeclared —
    // tests/nubi/nubiLocators.ts owns them and openSoulTab() drives it. Soul
    // relocated from the (now-deleted) Settings modal into BCortexModal.jsx's
    // Memory group as a sub-tab (docs/ia-consolidation-plan.md PR 4) — Memory
    // is b-Cortex's default landing group, so "Soul" is a real role=tab as
    // soon as the modal opens, no extra top-level-tab click needed.
    // BCortexModal renders its strips through shared/navigation/Tabs (MUI
    // Tabs), so every tab is a real role=tab even while scrolled out of view.
    this.soulTab = page.getByRole("tab", { name: "Soul" });
    // Sibling Memory sub-tab used to remount Soul (leave and come back) —
    // Memory's own default sub-tab, so it's always reachable without a filter
    // check of its own the way Soul itself needs one.
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });

    // Every locator below scopes to the b-Cortex dialog so a matching string on
    // the page behind the modal cannot satisfy it. Filtered on "Soul" because
    // that label is in the tab strip whichever tab is showing, which keeps the
    // anchor stable while tab content is being switched. Deliberately
    // single-rung: this is what everything below scopes to, so a wider fallback
    // here would widen all of them at once.
    // Resolves to nothing while the expand editor is open — MUI marks the
    // dialog underneath a second modal aria-hidden, which drops it out of the
    // accessibility tree. Anything done inside that editor is scoped to
    // expandDialog instead.
    this.bCortexDialog = page.getByRole("dialog").filter({ hasText: "Soul" }).first();

    // TabHeader's title is the one string that separates the two scopes, so it
    // doubles as the assertion that a scope switch took effect. Matched exactly
    // and with no fallback for that reason — a substring match would also hit
    // the tab label and both scopes would look identical.
    this.personalHeader = this.bCortexDialog.getByText("Soul — your style profile", { exact: true }).first();
    this.tenantHeader = this.bCortexDialog.getByText("Soul — tenant style profile", { exact: true }).first();

    // Either chip is correct here: SoulTab derives the kind from the row's
    // `sources` map, so a profile nobody has edited reads "Inferred" and one
    // with a user-set field reads "Explicit". The assertion is that the header
    // rail rendered its provenance at all, not which value it carries.
    this.provenanceChip = this.bCortexDialog
      .getByText("Explicit", { exact: true })
      .or(this.bCortexDialog.getByText("Inferred", { exact: true }))
      .first();

    // ds/ToggleGroup renders role=group with the ariaLabel it is given, and each
    // single-selection option as role=radio carrying aria-checked. The radios
    // are scoped to that group so the aria-checked assertions cannot drift onto
    // another control; a text fallback would match the scope labels in the copy.
    this.scopeToggle = this.bCortexDialog.getByRole("group", { name: "Memory scope" }).first();
    this.personalScopeOption = this.scopeToggle.getByRole("radio", { name: "Personal" });
    this.globalScopeOption = this.scopeToggle.getByRole("radio", { name: "Global" });

    this.toneVerbositySection = this.sectionCard("Tone & verbosity");
    this.formatSection = this.sectionCard("Format preferences");
    this.domainShorthandSection = this.sectionCard("Domain shorthand");

    // The only control on the tab with a placeholder, so it needs no container
    // scoping to be unambiguous. The fallback stays inside the Settings dialog,
    // where this is also the only textarea. The expand editor renders a second
    // textarea carrying the same placeholder, but it portals to a sibling
    // dialog, so it is outside this scope.
    this.domainShorthandTextarea = this.bCortexDialog
      .getByPlaceholder("`prod` = ...")
      .or(this.bCortexDialog.locator("textarea"))
      .first();

    // ds/Button forwards aria-label for an icon-only button, so the expand
    // affordance has a real accessible name. No fallback: an icon-only button
    // has no text, and the tooltip only exists while hovered.
    this.expandEditorBtn = this.bCortexDialog.getByRole("button", { name: "Expand editor" });

    // The expand editor is a second MUI Dialog portaled to the body, so it is a
    // sibling of the Settings dialog rather than a descendant. Identified by the
    // Edit/Preview ToggleGroup it alone renders — filtering on its "Domain
    // shorthand" title would also match the Settings dialog, which carries a
    // section of that name.
    this.expandDialog = page.getByRole("dialog").filter({ has: page.getByRole("group", { name: "View" }) }).first();
    this.expandViewToggle = this.expandDialog.getByRole("group", { name: "View" }).first();
    this.expandEditOption = this.expandViewToggle.getByRole("radio", { name: "Edit" });
    this.expandPreviewOption = this.expandViewToggle.getByRole("radio", { name: "Preview" });
    this.expandTextarea = this.expandDialog.getByPlaceholder("`prod` = ...").or(this.expandDialog.locator("textarea")).first();

    // Preview swaps the textarea for a <pre>, which has no role and no name, so
    // the element itself is the only handle. Scoped to the expand dialog, where
    // it is the only preformatted block.
    this.expandPreview = this.expandDialog.locator("pre").first();
    this.expandDoneBtn = this.expandDialog.getByRole("button", { name: "Done", exact: true });

    // Save reads "Saving…" while a write is in flight, so exact:true keeps this
    // off the in-flight state. Unlike PreferencesTab these two are not gated on
    // the form being dirty — SoulTab disables them only while it is loading or
    // saving, which is what makes "Save is enabled" the tab's load signal.
    // No fallback: their enabled state is load-bearing, so matching a different
    // button would pass a test that proved nothing.
    this.saveBtn = this.bCortexDialog.getByRole("button", { name: "Save", exact: true });
    this.resetBtn = this.bCortexDialog.getByRole("button", { name: "Reset", exact: true });

    // Rendered in place of the whole editor when the tenant's b-Cortex flag is
    // off (BCortexDisabled.jsx). Asserted against so a disabled module reports
    // itself by name instead of surfacing as every control being absent.
    // Matched on its own title, not its former "View legacy memory layer"
    // button — that button only rendered while NubiBrainNav.jsx passed
    // BCortexModal an onOpenSettingsMemory callback, removed along with the
    // non-admin-gated "Memory" rail button it opened (Decision AB) — the title
    // still renders unconditionally whenever the module is off.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();

    // SnackbarComponent mounts at the app root, so it is a sibling of the
    // Settings dialog's portal and MUI marks it aria-hidden while that dialog is
    // open. That drops it out of the accessibility tree, so getByRole finds
    // nothing even though the node is in the DOM — matched by attribute instead.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // One SectionCard, found by walking up from its own title: the title sits in a
  // <Typography> inside the card's header Box, whose parent is the Card itself.
  // Anchoring on the title rather than on position means a reordered tab cannot
  // silently point a section assertion at another card.
  sectionCard(title: string): Locator {
    return this.bCortexDialog.getByText(title, { exact: true }).locator("xpath=../..").first();
  }

  // The option cards under one FieldLabel. SoulTab wraps each label, its hint
  // and its OptionCards grid in a single Box, so the label's parent is that
  // group. Scoping matters here: "Detailed" names a Verbosity option and
  // "Detailed + tradeoffs" an Explain reasoning one, so a dialog-wide lookup
  // would resolve in document order and assert against the wrong grid.
  optionGroup(label: string): Locator {
    return this.bCortexDialog.getByText(label, { exact: true }).locator("xpath=..").first();
  }

  // One choice inside a labelled group. ds/Card sets role="button" only while
  // `interactive` is true, which SoulTab ties to the tab having finished
  // loading — so this locator resolving is itself proof the fetch came back.
  // The name is matched loosely because the accessible name of a card is its
  // label plus its description.
  optionCard(label: string, name: string | RegExp): Locator {
    return this.optionGroup(label).getByRole("button", { name });
  }

  // One format toggle, scoped to its own SectionCard for the same reason as
  // optionCard — these are ds/Cards too, named by their label plus description.
  formatToggle(name: string | RegExp): Locator {
    return this.formatSection.getByRole("button", { name });
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
