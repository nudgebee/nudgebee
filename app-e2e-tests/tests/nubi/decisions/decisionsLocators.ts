// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { NubiLocators } from "../nubiLocators";

// Nubi > b-Cortex > Memory > Decisions (app/src/ee/components/memory2/DecisionsTab.jsx).
//
// `grep -c data-testid` returns 1 for that file — the Show all history / Show current
// only button — and 0 for shared.jsx (TabHeader), ScopeToggle.jsx, ds/ToggleGroup,
// ds/EmptyState and ds/Button, so that button is the only rung-1 handle on this
// surface. Rung 2 covers everything with an accessible name: the tab strip, the
// Memory scope radios, the per-row Edit / Save to global buttons, and the empty
// state (ds/EmptyState renders role=status). The header title, the description and
// the scope banner have no role at all and are matched by their own text, scoped to
// the b-Cortex dialog.
//
// Most locators here are deliberately single-rung. The tab is one card of prose plus
// one row list, and every label it renders in a control — Personal, Global, Decisions,
// Edit, Save — is also a word in its own body text, so a text-shaped `.or()` fallback
// would resolve into the panel rather than onto the control and quietly assert against
// the wrong element. Each one below says which wider match it is refusing.
//
// BCortexModal mounts this tab as <DecisionsTab scope='mine' readOnly={false} />, so
// Personal is always the scope a freshly-opened tab lands on and its rows always
// carry an Edit button. Global scope resolves readOnly from isTenantAdmin(), which
// is why nothing here assumes a row action exists outside Personal.
export class DecisionsLocators extends CommonLocators {
  readonly decisionsTab: Locator;
  readonly patternsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly personalHeader: Locator;
  readonly tenantHeader: Locator;
  readonly immutabilityDescription: Locator;
  readonly scopeToggle: Locator;
  readonly historyToggleBtn: Locator;
  readonly adminScopeNote: Locator;
  readonly readOnlyLockChip: Locator;
  readonly emptyState: Locator;
  readonly editBtns: Locator;
  readonly promoteBtns: Locator;
  readonly supersededChips: Locator;
  readonly bCortexDisabledPanel: Locator;

  constructor(page: Page) {
    super(page);

    const nubi = new NubiLocators(page);

    // Decisions is a Memory sub-tab and Memory is b-Cortex's default landing group
    // (BCortexModal.jsx DEFAULT_TAB_STATE), so the tab is clickable as soon as the
    // modal opens. BCortexModal renders its strips through shared/navigation/Tabs
    // (MUI Tabs), so every tab is a real role=tab. Single-rung: "Decisions" is also
    // the first word of the tab's own header, so a text fallback would match the
    // panel body and click into it instead of onto the strip.
    this.decisionsTab = page.getByRole("tab", { name: "Decisions", exact: true });
    // Memory's own default sub-tab, used to unmount Decisions and force a refetch.
    // Single-rung for the same reason — PatternsTab renders "Patterns" in its header.
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });

    // Reused rather than re-declared: NubiLocators already owns the b-Cortex dialog
    // anchor, matched by attribute instead of getByRole because MUI aria-hides a
    // dialog the moment a nested overlay is up — this tab's own edit modal does it,
    // so a role lookup here would read "the modal closed" while it is on screen.
    this.bCortexDialog = nubi.bcortexDialog;

    // TabHeader's title, which is the only thing on the tab that names the current
    // scope. Matched exactly and single-rung: both variants start with "Decisions — ",
    // so any wider match could not tell the personal view from the tenant one, which
    // is the single distinction these two locators exist to make.
    this.personalHeader = this.bCortexDialog.getByText("Decisions — your decision history", { exact: true }).first();
    this.tenantHeader = this.bCortexDialog.getByText("Decisions — tenant decisions", { exact: true }).first();

    // The description renders in both scopes, which makes it the tab's own
    // mounted/unmounted signal. Matched on a distinctive fragment rather than in
    // full — the whole sentence carries typographic punctuation a copy edit would
    // churn without the tab changing at all. Single-rung: it is the fallback, and
    // widening it further would start matching the Patterns tab it waits to replace.
    this.immutabilityDescription = this.bCortexDialog.getByText(/Immutable by design/).first();

    // ds/ToggleGroup renders role=group with the ariaLabel ScopeToggle gives it and
    // each single-selection option as role=radio carrying aria-checked. Single-rung
    // and scoped to the group: the words Personal and Global appear in the tab's
    // prose and in the scope banner, so a text fallback would let the aria-checked
    // assertions drift off the radios onto plain copy that carries no such attribute.
    this.scopeToggle = this.bCortexDialog.getByRole("group", { name: "Memory scope" }).first();

    // The one rung-1 handle on this surface. The role fallback has to match both
    // labels because the button relabels itself from the state it toggles, and it is
    // scoped to the same dialog as the primary so the two cannot resolve apart.
    this.historyToggleBtn = this.bCortexDialog
      .getByTestId("decisions-toggle-superseded-btn")
      .or(this.bCortexDialog.getByRole("button", { name: /^(Show all history|Show current only)$/ }))
      .first();

    // ScopeToggle renders exactly one of these two beside the toggle while the scope
    // is Global, picked on isTenantAdmin(). Which one is a property of the signed-in
    // user, so the tests assert that one of them is there rather than which. Exact
    // and single-rung: the counts are the assertion, so a fallback that matched a
    // second element would turn a one-of-two check into a passing two-of-two.
    this.adminScopeNote = this.bCortexDialog.getByText("Tenant-shared memory · editable as admin", { exact: true }).first();
    this.readOnlyLockChip = this.bCortexDialog.getByText("Read-only · tenant-managed", { exact: true }).first();

    // ds/EmptyState wraps itself in role=status, so the empty state is reachable at
    // rung 2; the text fallback covers the container losing that role and is scoped
    // to the same dialog. Filtered on the title rather than matching it directly so
    // the locator is the whole empty state, not just its heading line.
    this.emptyState = this.bCortexDialog
      .getByRole("status")
      .filter({ hasText: "No decisions yet" })
      .or(this.bCortexDialog.getByText("No decisions yet", { exact: true }))
      .first();

    // ds/Button forwards aria-label for an icon-only button, so both row actions have
    // a real accessible name. In Personal scope readOnly is always false, so there is
    // exactly one Edit button per row and this count is the row count — which is why
    // it is a collection with no .first(). Single-rung and exact on purpose: the
    // count is load-bearing, so any fallback that also matched the edit modal's own
    // buttons once it is open would inflate the row count the suite reads off it.
    this.editBtns = this.bCortexDialog.getByRole("button", { name: "Edit", exact: true });
    // Only rendered for a personal row that has not been promoted yet, and asserted
    // to be absent in Global scope. Asserted to exist, never clicked — promotion is a
    // one-way write into tenant-shared memory. Single-rung for the same count reason,
    // and exact so it cannot also match "Save to global for all" in the modal.
    this.promoteBtns = this.bCortexDialog.getByRole("button", { name: "Save to global", exact: true });

    // ds/Chip renders variant='tag' as a span carrying data-variant and no role, so
    // rung 5, scoped to the dialog and narrowed by the chip's own text — the tab also
    // renders module and provenance chips with the same data-variant. Single-rung:
    // a bare text fallback would match the word inside a decision's own subject line.
    this.supersededChips = this.bCortexDialog.locator('[data-variant="tag"]').filter({ hasText: "Superseded" });

    // Rendered in place of the whole tab when the tenant's b-Cortex flag is off
    // (BCortexDisabled.jsx). Asserted against so a disabled module reports itself by
    // name instead of surfacing as every control below being absent. Single-rung: it
    // is an exact-match guard, and a looser one would fire on unrelated flag copy.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();
  }

  // One Memory scope radio, scoped to the toggle group so the aria-checked reads
  // cannot land on the same word in the tab's prose. Exact: neither label is a
  // substring of the other, and a loose match would let "Global" hit the promote
  // button's own label.
  scopeOption(label: string): Locator {
    return this.scopeToggle.getByRole("radio", { name: label, exact: true });
  }
}
