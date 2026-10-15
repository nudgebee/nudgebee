// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Memory > Collective (app/src/ee/components/memory2/CollectiveTab.jsx).
// `grep -c data-testid` returns 0 for that file, and ds/Input, ds/Card, ds/Chip, ds/Button,
// ds/EmptyState and ds/Modal render none of their own either, so rung 1 is unavailable on
// this whole surface. Everything below is therefore rung 2 where a control has an
// accessible name (the tab strip, the row Delete buttons, the modal's Cancel/Delete), rung
// 3 for the account picker (ds/Select renders a stable id and no testid), and rung 4 text
// scoped to the b-Cortex dialog where the element carries no role at all (the header, the
// two empty states, the modal body).
export class CollectiveLocators extends CommonLocators {
  readonly memoryGroupTab: Locator;
  readonly collectiveTab: Locator;
  readonly patternsTab: Locator;
  readonly decisionsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly tabDescription: Locator;
  readonly searchInput: Locator;
  readonly noKnowledgeEmptyState: Locator;
  readonly noMatchEmptyState: Locator;
  readonly noMatchHint: Locator;
  readonly groupCountChips: Locator;
  readonly deleteDialog: Locator;
  readonly deleteDialogBody: Locator;
  readonly deleteConfirmBtn: Locator;
  readonly deleteCancelBtn: Locator;
  readonly accountSelect: Locator;
  readonly notificationsRegion: Locator;
  readonly bCortexDisabledPanel: Locator;

  constructor(page: Page) {
    super(page);

    // The b-Cortex tab strips are MUI Tabs (BCortexModal.jsx renders them through
    // shared/navigation/Tabs), so every tab is a real role=tab. Re-declared here rather
    // than imported from another surface's locators class, the same way digestsLocators.ts
    // re-declares its own strip: each tests/nubi/<surface>/ directory owning its strip
    // locators is what keeps one surface's rename from editing another surface's file.
    this.memoryGroupTab = page.getByRole("tab", { name: "Memory", exact: true });
    this.collectiveTab = page.getByRole("tab", { name: "Collective", exact: true });
    // Patterns is the Memory group's default sub-tab (DEFAULT_TAB_STATE in BCortexModal.jsx).
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });
    // Decisions carries `usesAccount` while tenant-wide Collective does not (#40743), so it is
    // the contrast case for the account-picker assertion.
    this.decisionsTab = page.getByRole("tab", { name: "Decisions", exact: true });

    // Everything below scopes to the b-Cortex dialog so a matching string on the page
    // behind the modal cannot satisfy it. Filtered on "b-Cortex" — the ds/Modal title —
    // rather than on "Collective": the delete confirmation is a second, sibling dialog
    // whose body reads "Remove this insight from the Collective?", so a "Collective"
    // filter would match both dialogs and .first() would then depend on portal order.
    // Deliberately single-rung: this is what every locator below scopes to, so a fallback
    // here would widen all of them at once. Matched by attribute rather than getByRole
    // because MUI aria-hides this dialog while the stacked delete modal is open, and a
    // role lookup would then read "the modal closed" — the same trap nubiLocators.ts and
    // soulLocators.ts already document.
    this.bCortexDialog = page.locator('[role="dialog"]').filter({ hasText: "b-Cortex" }).first();

    // TabHeader's description. This, not the title, is the "the Collective tab is mounted"
    // signal: the title starts with the bare word "Collective", which the Memory sub-tab
    // strip also carries, while this sentence appears exactly once on the surface.
    this.tabDescription = this.bCortexDialog
      .getByText("Module-scoped knowledge shared across your tenant. Curated, not personally identifying.", { exact: true })
      .first();

    // The search box. ds/Input forwards a caller's data-testid but CollectiveTab passes
    // none, so this is rung 4 on the placeholder, with the dialog's own textbox as the
    // fallback — Collective renders exactly one input, so the fallback cannot drift onto
    // another field the way an unscoped one would.
    this.searchInput = this.bCortexDialog
      .getByPlaceholder("Search insights…")
      .or(this.bCortexDialog.getByRole("textbox"))
      .first();

    // ds/EmptyState titles for the tab's two no-content states. CollectiveTab picks
    // between them on whether the search box has content, so which one rendered is itself
    // the assertion: "no matching insights" on an empty box would mean the browse read
    // came back empty and was mislabelled as a filtered miss.
    this.noKnowledgeEmptyState = this.bCortexDialog.getByText("No collective knowledge yet", { exact: true }).first();
    this.noMatchEmptyState = this.bCortexDialog.getByText("No matching insights", { exact: true }).first();
    // Copy widened to name the module/type filters too (#40743).
    this.noMatchHint = this.bCortexDialog.getByText("Try a different query or filter.", { exact: true }).first();

    // One per module group card: ds/Chip variant='tag' holding that group's row count.
    // Anchored with ^$ on digits so it cannot also collect a row's kind chip, which is
    // always a word ("Fact", "Config", "Troubleshooting"). ds/Chip renders neither a role
    // nor a testid, so this is rung 5 scoped to the dialog — and it doubles as the group
    // anchor, since the module label is its preceding sibling and the card its grandparent.
    this.groupCountChips = this.bCortexDialog.locator('[data-variant="tag"]').filter({ hasText: /^\d+$/ });

    // The delete confirmation is its own ds/Modal, a sibling dialog of b-Cortex rather
    // than a node inside it, so it is matched from the page and not from bCortexDialog.
    this.deleteDialog = page.locator('[role="dialog"]').filter({ hasText: "Delete insight" }).first();
    this.deleteDialogBody = this.deleteDialog
      .getByText("Remove this insight from the Collective? It will stop being injected into future prompts.", { exact: true })
      .first();
    // This suite NEVER clicks the confirm button — see collectiveHelper.ts. It is declared
    // so the cancel case can assert the dialog offered it and then left it unpressed.
    // Built through the same accessor the row buttons use: ds/Button gives the row control
    // its name from aria-label and the modal control its name from its text, so both are
    // the same role-and-name lookup and only the scope separates them.
    this.deleteConfirmBtn = this.deleteButtonsIn(this.deleteDialog);
    // Not CommonLocators.cancelBtn: that one is page-wide and unscoped, so with b-Cortex
    // open behind this modal it can resolve to a Cancel in the dialog underneath.
    this.deleteCancelBtn = this.deleteDialog.getByRole("button", { name: "Cancel", exact: true });

    // AccountSelect renders id={id || 'account-select'} and BCortexModal passes no id.
    // ds/Select renders no testid and the trigger has no accessible name, so rung 3 is the
    // highest available here. Scoped to the dialog: the page behind the modal carries its
    // own account picker with this same id. Deliberately no .or() fallback — the only
    // wider match left is the combobox role, and this case asserts the picker is ABSENT on
    // a sibling sub-tab, so a fallback that could resolve to any other select on the
    // surface would turn that assertion green against the wrong element.
    this.accountSelect = this.bCortexDialog.locator("#account-select");

    // SnackbarComponent mounts at the app root, so it is a sibling of the dialog portals
    // and MUI marks it aria-hidden while one is open — getByRole finds nothing even though
    // the node is in the DOM. Matched by attribute instead, same form accountContextLocators.ts uses.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');

    // Rendered in place of the whole tab when the tenant's b-Cortex flag is off
    // (BCortexDisabled.jsx). Asserted against so a disabled module reports itself by name
    // instead of surfacing as every locator above being absent.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();
  }

  // TabHeader puts the title Typography and the description Typography in one Box, title
  // first. Anchoring on the description rather than on the text "Collective" is what keeps
  // this off the sub-tab label, which carries that word too.
  get headerTitle(): Locator {
    return this.tabDescription.locator("xpath=preceding-sibling::*[1]");
  }

  // The one place the Delete control is built, for every scope that needs it. Every insight
  // row carries exactly one icon-only Delete button, so within the b-Cortex dialog these are
  // the row count — the row *anchor* is the kind chip instead, for the wrapper reason in
  // row() below. ds/Button forwards aria-label to the button element, which makes this
  // rung 2. Scoping is what separates the row controls from the confirmation dialog's own
  // Delete — the two resolve to the same role and name, so an unscoped build collects both.
  deleteButtonsIn(scope: Locator): Locator {
    return scope.getByRole("button", { name: "Delete", exact: true });
  }

  // Every insight row currently listed, across all module groups.
  get deleteButtons(): Locator {
    return this.deleteButtonsIn(this.bCortexDialog);
  }

  // The rows belonging to one module group card.
  groupDeleteButtons(index: number): Locator {
    return this.deleteButtonsIn(this.groupCard(index));
  }

  // One group's ds/Card, walked up from its count chip: the chip sits in the card's header
  // Box, so the card is the chip's grandparent.
  groupCard(index: number): Locator {
    return this.groupCountChips.nth(index).locator("xpath=../..");
  }

  // The group's module label ("AI-SRE", "AI-FinOps", …). CollectiveTab renders the label
  // Typography immediately before the count Chip in the same header Box, so it is the
  // chip's preceding sibling — read positionally off the chip rather than off the card, so
  // a header that gained another element cannot point this at the wrong text.
  groupModuleLabel(index: number): Locator {
    return this.groupCountChips.nth(index).locator("xpath=preceding-sibling::*[1]");
  }

  // Each row's kind chip ("Fact", "Config", "Troubleshooting", …) — the tag chips that are
  // NOT a group count, which is always bare digits. The complement of groupCountChips, so
  // the two partition the surface's tag chips between them.
  get rowKindChips(): Locator {
    return this.bCortexDialog.locator('[data-variant="tag"]').filter({ hasNotText: /^\d+$/ });
  }

  // One insight row, reached from its own kind chip: the chip sits in the row's first grid
  // column, so the row is its grandparent. Anchored on the chip rather than on the row's
  // Delete button, even though the Delete button is the better-named handle: that button
  // passes a `tooltip`, and ds/Button wraps a tooltipped button in an extra
  // `<Box component='span'>` so the tooltip still anchors when the button is disabled —
  // which puts the row three levels up from it, not two, and would silently resolve this to
  // the actions Box. ds/Chip adds no such wrapper (its own tooltip branch wraps in a MUI
  // Tooltip, which clones its child rather than nesting it), so this walk has no
  // conditional node in it. Row order matches deleteButtons order — both are DOM order,
  // one per row — so the same index means the same row in either.
  // Every insight row currently listed, across all module groups, in DOM order — so a case
  // can read the whole list in one round trip with allTextContents() instead of a per-row
  // await loop.
  get rows(): Locator {
    return this.rowKindChips.locator("xpath=../..");
  }

  row(index: number): Locator {
    return this.rows.nth(index);
  }

  // A row's headline. The row is a 3-column grid — kind chip, content, actions — and
  // CollectiveTab renders the title Typography as the content column's first child, so
  // this is that child. Read rather than matched by text because the title is exactly what
  // the delete dialog echoes back, and the case needs the string to compare the two.
  rowTitle(index: number): Locator {
    return this.row(index).locator("xpath=./*[2]/*[1]");
  }

  // Toast text, kept inside the notifications region so a matching string elsewhere on the
  // page cannot pass for a toast. The fallback matches the toast roles by attribute for
  // the aria-hidden reason above.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }
}
