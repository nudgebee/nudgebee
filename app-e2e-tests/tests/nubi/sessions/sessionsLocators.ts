// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > b-Cortex > Memory > Sessions (app/src/ee/components/memory2/SessionsTab.jsx).
// `grep -c data-testid` returns 0 for that file, for shared.jsx (TabHeader) and for
// ds/ToggleGroup, ds/EmptyState and ds/Button, so there is no rung-1 handle on this
// surface. ds/Chip is the one component in the chain that forwards a testid, but
// SessionsTab never passes one, so its row chips are reached at rung 5 instead.
// Rung 2 covers everything with an accessible name: the tab strip, the Session type
// radios, the per-row View/Hide and Delete buttons. The header title, the row counts
// and the expanded panel have no role at all and are matched by their own text,
// scoped to the b-Cortex dialog.
export class SessionsLocators extends CommonLocators {
  readonly sessionsTab: Locator;
  readonly patternsTab: Locator;
  readonly bCortexDialog: Locator;
  readonly recentActivityHeader: Locator;
  readonly lastFiveDescription: Locator;
  readonly typeToggle: Locator;
  readonly rowToggles: Locator;
  readonly viewToggles: Locator;
  readonly hideToggles: Locator;
  readonly deleteBtns: Locator;
  readonly workingMemoryCounts: Locator;
  readonly typeChips: Locator;
  readonly workingMemoryHeading: Locator;
  readonly noWorkingMemoryText: Locator;
  readonly emptyState: Locator;
  readonly bCortexDisabledPanel: Locator;

  constructor(page: Page) {
    super(page);

    // Sessions is a Memory sub-tab and Memory is b-Cortex's default landing group
    // (BCortexModal.jsx DEFAULT_TAB_STATE), so the tab is clickable as soon as the
    // modal opens — no top-level group click first. BCortexModal renders its strips
    // through shared/navigation/Tabs (MUI Tabs), so every tab is a real role=tab.
    this.sessionsTab = page.getByRole("tab", { name: "Sessions", exact: true });
    // Memory's own default sub-tab, used to unmount Sessions and force a refetch.
    this.patternsTab = page.getByRole("tab", { name: "Patterns", exact: true });

    // Everything below scopes to the b-Cortex dialog so a matching string on the page
    // behind the modal cannot satisfy it. Filtered on "Sessions" because that label is
    // in the Memory tab strip whichever sub-tab is showing, which keeps the anchor
    // stable while tab content is being swapped. Deliberately single-rung: this is
    // what every locator below scopes to, so a fallback here would widen all of them.
    this.bCortexDialog = page.getByRole("dialog").filter({ hasText: "Sessions" }).first();

    // TabHeader's title. Matched exactly and without a fallback — a substring match
    // would also hit the "Sessions" tab label, and this string is what the tests use
    // to tell "the Sessions tab is mounted" from "some other sub-tab is".
    this.recentActivityHeader = this.bCortexDialog.getByText("Sessions — recent activity", { exact: true }).first();
    this.lastFiveDescription = this.bCortexDialog
      .getByText("Your most recent sessions and their short-term working memory. Showing the last 5.", { exact: true })
      .first();

    // ds/ToggleGroup renders role=group with the ariaLabel it is given and each
    // single-selection option as role=radio carrying aria-checked. Scoped to the group
    // so the aria-checked assertions cannot drift onto another control; a text fallback
    // would match the same words in the row type chips.
    this.typeToggle = this.bCortexDialog.getByRole("group", { name: "Session type" }).first();

    // One View/Hide button per listed session, so their count is the row count. The
    // label is the row's own expand state, which is what makes hideToggles usable as
    // "which rows are open" without walking the DOM. Anchored with ^$ so "View" cannot
    // also match a longer button label elsewhere in the dialog.
    this.rowToggles = this.bCortexDialog.getByRole("button", { name: /^(View|Hide)$/ });
    this.viewToggles = this.bCortexDialog.getByRole("button", { name: "View", exact: true });
    this.hideToggles = this.bCortexDialog.getByRole("button", { name: "Hide", exact: true });
    // ds/Button forwards aria-label for an icon-only button, so the row's delete
    // affordance has a real accessible name. This suite only ever asserts it renders —
    // deleting a session on a shared dev tenant is irreversible.
    this.deleteBtns = this.bCortexDialog.getByRole("button", { name: "Delete", exact: true });

    // The per-row secondary line, "N working-memory item(s)". Anchored so it matches
    // the row line only, and it doubles as a per-row anchor: the row title is its
    // immediately preceding sibling in the same Box.
    this.workingMemoryCounts = this.bCortexDialog.getByText(/^\d+ working-memory items?$/);

    // The row type chip. ds/Chip renders variant='tag' with no onClick as a plain
    // span carrying data-variant, so it has neither a role nor a testid — rung 5,
    // scoped to the dialog. SessionsTab is the only thing rendering tag chips while
    // it is mounted (its TabHeader carries the ToggleGroup, not chips), and the tests
    // assert one chip per row, so a stray chip fails the count rather than passing.
    this.typeChips = this.bCortexDialog.locator('[data-variant="tag"]');

    // The expanded working-memory panel. SessionsTab keeps at most one row expanded
    // (a single expandedId) and the Collapse is unmountOnExit, so the count of this
    // heading is exactly the number of open panels.
    this.workingMemoryHeading = this.bCortexDialog.getByText("Working memory", { exact: true });
    this.noWorkingMemoryText = this.bCortexDialog.getByText("No working memory in this session.", { exact: true });

    // ds/EmptyState's title. Matched exactly so it cannot also match the description
    // line underneath it, which is branded with the assistant's name and so is not a
    // fixed string on every tenant.
    this.emptyState = this.bCortexDialog.getByText("No sessions", { exact: true }).first();

    // Rendered in place of the whole tab when the tenant's b-Cortex flag is off
    // (BCortexDisabled.jsx). Asserted against so a disabled module reports itself by
    // name instead of surfacing as every control below being absent.
    this.bCortexDisabledPanel = this.bCortexDialog.getByText("b-Cortex not enabled for your tenant", { exact: true }).first();
  }

  // One session type radio. Exact, because "General" would otherwise also match
  // nothing else here but "All" is a substring of no label and the three type
  // labels are the same words the row chips carry.
  typeOption(label: string): Locator {
    return this.typeToggle.getByRole("radio", { name: label, exact: true });
  }

  // The row title, reached from the row's working-memory count line: SessionsTab puts
  // the title Typography and the count Typography in one Box, title first. Anchoring
  // on the count rather than on position in the list means a row that gained another
  // line cannot silently point this at the wrong text.
  sessionTitle(index: number): Locator {
    return this.workingMemoryCounts.nth(index).locator("xpath=preceding-sibling::*[1]");
  }

  // The open panel's body, walked up from its own heading — the heading Typography
  // sits directly inside the panel Box that also holds the item list.
  openPanel(): Locator {
    return this.workingMemoryHeading.locator("xpath=..").first();
  }

  // The bullets inside the open panel. Scoped to the panel so a list rendered by
  // another part of the dialog cannot be counted as working-memory entries.
  openPanelItems(): Locator {
    return this.openPanel().locator("li");
  }
}
