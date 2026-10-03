// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Memory > Patterns (app/src/ee/components/memory2/PatternsTab.jsx).
// `grep -c data-testid` returns 0 for that file and for everything it composes —
// shared.jsx (TabHeader, ProvenanceChip), mocks.js and BCortexDisabled.jsx carry
// one between them, and it is the disabled panel this suite asserts is absent.
// ds/Chip and ds/Input both accept a data-testid prop, but PatternsTab passes
// none, so nothing reaches the DOM and there is no rung-1 handle on this surface.
// Controls with an accessible name are taken at rung 2: the tab strip, the five
// filter chips, the per-row Pin/Unpin/Edit/Delete buttons and the edit modal's
// buttons. ds/Chip renders an interactive chip as a MUI ButtonBase carrying
// aria-pressed, so a filter chip is a real role=button and its selected state is
// an attribute, never a class. The search box is labelled by nothing at all — the
// Input is given a placeholder and no `label` — so it is reached at rung 4,
// scoped to the dialog. Rows carry no handle of their own; they are reached from
// their own action buttons and their own description text by a one-step xpath.
export class PatternsLocators extends CommonLocators {
  readonly patternsTab: Locator;
  readonly sessionsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly tabHeaderTitle: Locator;
  readonly searchInput: Locator;
  readonly emptyPanel: Locator;
  readonly rowActionButtons: Locator;
  readonly pinButtons: Locator;
  readonly unpinButtons: Locator;
  readonly editButtons: Locator;
  readonly editDialog: Locator;
  readonly editTextarea: Locator;
  readonly editSaveBtn: Locator;
  readonly editCancelBtn: Locator;
  readonly closeDialogBtn: Locator;
  readonly bCortexDisabledPanel: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its "b-Cortex" rail button are not
    // redeclared — tests/nubi/nubiLocators.ts owns them and openPatternsTab()
    // drives them through NubiLocators.openBCortex(). BCortexModal renders its
    // strips through shared/navigation/Tabs (MUI Tabs), so every sub-tab is a
    // real role=tab as soon as the modal opens. Memory is b-Cortex's default
    // landing group and Patterns is Memory's own default sub-tab, so Patterns is
    // both the tab that proves the modal is up and the tab the suite lands on.
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });
    // Sibling Memory sub-tab used to unmount Patterns and force a refetch.
    this.sessionsTab = page.getByRole("tab", { name: "Sessions", exact: true });

    // Everything below scopes to the b-Cortex dialog so a matching string on the
    // page behind the modal cannot satisfy it. Matched by attribute rather than
    // getByRole("dialog") for the reason nubiLocators.ts gives: MUI aria-hides a
    // dialog the moment a nested overlay is up, and this tab has one — the "Edit
    // pattern" modal — so a role lookup would read "the modal closed" while it is
    // still on screen. Filtered on "b-Cortex" because that is the modal's own
    // title and so is present whichever sub-tab is showing, which keeps the
    // anchor stable while tab content is being swapped. Deliberately single-rung:
    // this is what every locator below scopes to, so a fallback here would widen
    // all of them at once.
    this.bCortexDialog = page.locator('[role="dialog"]').filter({ hasText: "b-Cortex" }).first();

    // TabHeader's title. Branded — PatternsTab interpolates the tenant's
    // assistant name into "Patterns — what <name> inferred" — so only the fixed
    // half is matched. The em-dash keeps it from also matching the bare
    // "Patterns" tab label, which is why the regex is anchored on it.
    this.tabHeaderTitle = this.bCortexDialog.getByText(/^Patterns —/);

    // The search box. ds/Input renders no label here, so the placeholder is the
    // only handle the app gives it. The fallback is the same rung and the same
    // container on purpose: it guards the single character most likely to be
    // edited by hand — the "…" is a real U+2026 in PatternsTab.jsx, and a rewrite
    // to three periods would otherwise take the whole suite red.
    this.searchInput = this.bCortexDialog
      .getByPlaceholder("Search patterns…")
      .or(this.bCortexDialog.getByPlaceholder(/^Search patterns/))
      .first();

    // ds/EmptyState renders role="status" on its outer box, which covers both of
    // the titles this tab can show — "No patterns yet" when the user has none at
    // all, "No matching patterns" when a search or filter excluded them. Filtered
    // on those two titles rather than taken bare: ds/Skeleton carries role=status
    // too, and a loading skeleton satisfying this would let waitForPatternsLoaded
    // return before the list had rendered. Scoped to the dialog so a toast, which
    // also carries role="status", cannot satisfy it either.
    this.emptyPanel = this.bCortexDialog
      .getByRole("status")
      .filter({ hasText: /^No (patterns yet|matching patterns)/ })
      .or(this.bCortexDialog.getByText(/^No (patterns yet|matching patterns)$/))
      .first();

    // One Pin-or-Unpin button per rendered row, so the count of these is the
    // count of rows. Anchored on the buttons rather than on the row card because
    // ds/Card renders a plain Box here — no role, no testid, no data attribute —
    // so the card itself has no handle at all. The regex is fully anchored so it
    // cannot also match the "Pinned" filter chip, which is a button too.
    this.rowActionButtons = this.bCortexDialog.getByRole("button", { name: /^(Pin|Unpin)$/ });
    // Split by current state: PatternsTab names the button for the action it
    // performs, so "Pin" is an unpinned row and "Unpin" is a pinned one. That
    // makes these counts the assertion a pin round-trip is actually about.
    this.pinButtons = this.bCortexDialog.getByRole("button", { name: "Pin", exact: true });
    this.unpinButtons = this.bCortexDialog.getByRole("button", { name: "Unpin", exact: true });
    this.editButtons = this.bCortexDialog.getByRole("button", { name: "Edit", exact: true });

    // The "Edit pattern" modal is a second ds/Modal stacked on the b-Cortex one,
    // so it is matched at the page level rather than inside bCortexDialog —
    // while it is open MUI aria-hides everything underneath it. Matched by
    // attribute for the same reason the dialog above is.
    this.editDialog = page.locator('[role="dialog"]').filter({ hasText: "Edit pattern" }).first();
    // ds/Input renders a <label for> for its `label` prop, so the textarea has a
    // real accessible name. The fallback drops to the only textarea the modal
    // holds, scoped to that modal, in case the label copy changes.
    this.editTextarea = this.editDialog
      .getByRole("textbox", { name: "Pattern" })
      .or(this.editDialog.locator("textarea"))
      .first();
    this.editSaveBtn = this.editDialog.getByRole("button", { name: "Save", exact: true });
    // CommonLocators.cancelBtn is not reused: it is unscoped, and with two
    // dialogs stacked an unscoped match resolves in document order and would
    // return whichever Cancel the page happens to render first.
    this.editCancelBtn = this.editDialog.getByRole("button", { name: "Cancel", exact: true });

    // ds/Modal's own close control. Scoped to the b-Cortex dialog so it cannot
    // pick up the identical button the stacked edit modal renders.
    this.closeDialogBtn = this.bCortexDialog
      .getByRole("button", { name: "Close", exact: true })
      .or(this.bCortexDialog.locator("#close-modal-btn"))
      .first();

    // Rendered in place of the whole tab when the tenant's b-Cortex flag is off
    // (BCortexDisabled.jsx). Asserted against so a disabled module reports itself
    // by name instead of surfacing as every control below being absent.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();
  }

  // One filter chip. Exact, and deliberately single-rung: a text fallback would
  // also match the "Pinned" tag chip that a pinned row carries, and that chip is
  // a plain span with no aria-pressed — so the wider match could not carry the
  // very attribute these cases assert on.
  filterChip(label: string): Locator {
    return this.bCortexDialog.getByRole("button", { name: label, exact: true }).first();
  }

  // The description line of the row that owns `actionButton`. PatternRow puts the
  // content box and the action box side by side inside the card, so one step up
  // to the nearest ancestor div (the action box, whether or not ds/Button's
  // tooltip wrapped the button in a span) and one step across reaches the content
  // box; the description is its last direct-child <p>, the type line being the
  // first. Anchored on a named button rather than on a position in the list.
  rowDescriptionFor(actionButton: Locator): Locator {
    return actionButton.locator("xpath=ancestor::div[1]/preceding-sibling::div[1]").locator("xpath=p").last();
  }

  // The description <p> of the row whose text reads exactly `description`.
  // Matched on an anchored regex rather than hasText's substring match: two
  // inferred patterns can legitimately share a prefix, and a substring match
  // would then point a case at the wrong row and still pass.
  rowDescription(description: string): Locator {
    return this.bCortexDialog.locator("p").filter({ hasText: exactText(description) }).first();
  }

  // The action buttons of the row whose description reads exactly `description`.
  // The description <p> is a direct child of the content box, so its parent's
  // next sibling div is that row's action box — the reverse of the walk above,
  // and the way a row is found again after a refetch has reordered the list.
  rowActionsForDescription(description: string): Locator {
    return this.rowDescription(description).locator("xpath=parent::div/following-sibling::div[1]");
  }
}

// Anchored, literal-safe regex for one row's description. Pattern descriptions
// are backend prose and routinely carry ( ) . * — characters a bare string would
// hand to the regex engine as syntax.
function exactText(value: string): RegExp {
  return new RegExp(`^${value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`);
}
