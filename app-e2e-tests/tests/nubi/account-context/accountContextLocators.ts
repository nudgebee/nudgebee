// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { NubiLocators } from "../nubiLocators";

// Every record this suite writes carries this prefix, which is what makes a leftover from
// a failed run distinguishable from a context a human created. Nothing without it is ever
// edited or deleted — GlobalContextTab caps an account at one context, so a pre-existing
// record is the one thing that must survive untouched. Declared here rather than in the
// helper because the locators below match on it and must not import back from the helper.
export const E2E_NAME_PREFIX = "nb_e2e_acctx_";

// Nubi > b-Cortex > Knowledge > Account Context (app/src/components/llm/GlobalContextTab.jsx).
// `grep -c data-testid app/src/components/llm/GlobalContextTab.jsx` returns 0, and the
// same holds for ScopeChip.jsx and ds/WidgetCard, so there is no rung-1 handle anywhere
// on this surface. ds/Input DOES render a real <label htmlFor>, so the three form fields
// are reached at rung 2 by their accessible name with the placeholder as the scoped
// fallback; the cards, banners and empty state carry no role at all and are taken at
// rung 4, scoped to the dialog.
//
// The same component is also mounted at Admin > AI & Tools > Account Context, but the two
// mounts are mutually exclusive: aiToolsConfig.js marks it `legacyOnly` (shown only when
// bcortexEnabled === false) while BCortexModal marks it `memoryOnly` (shown only when
// bcortexEnabled !== false). This suite is pinned to the b-Cortex mount, the one every
// other tests/nubi b-Cortex suite already proves is the live path on dev.
export class AccountContextLocators extends CommonLocators {
  readonly nubi: NubiLocators;
  readonly dialog: Locator;
  readonly knowledgeGroupTab: Locator;
  readonly accountContextTab: Locator;
  readonly knowledgeBaseTab: Locator;
  readonly tabDescription: Locator;
  readonly addContextBtn: Locator;
  readonly emptyState: Locator;
  readonly emptyStateHint: Locator;
  readonly loadErrorBanner: Locator;
  readonly soleCardMenuBtn: Locator;
  readonly e2eLeftover: Locator;
  readonly accountSelectTrigger: Locator;
  readonly accountListbox: Locator;
  readonly formModal: Locator;
  readonly formModalIntro: Locator;
  readonly nameInput: Locator;
  readonly descriptionInput: Locator;
  readonly contentInput: Locator;
  readonly uploadZone: Locator;
  readonly fileInput: Locator;
  readonly charCounter: Locator;
  readonly formCancelBtn: Locator;
  readonly formCreateBtn: Locator;
  readonly formUpdateBtn: Locator;
  readonly deleteModal: Locator;
  readonly deleteCancelBtn: Locator;
  readonly deleteConfirmBtn: Locator;
  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // The panel entry point, the "b-Cortex" rail button and the b-Cortex dialog itself
    // are owned by tests/nubi/nubiLocators.ts and reused rather than re-declared.
    this.nubi = new NubiLocators(page);
    this.dialog = this.nubi.bcortexDialog;

    // BCortexModal builds both strips through shared/navigation/Tabs (MUI Tabs), so every
    // entry is a real role=tab. exact:true is load-bearing on both: "Knowledge" is also the
    // prefix of the sibling "Knowledge Graph" group tab, and "Account Context" is also the
    // tab body's own heading, which a substring match would happily return instead.
    this.knowledgeGroupTab = page.getByRole("tab", { name: "Knowledge", exact: true });
    this.accountContextTab = page.getByRole("tab", { name: "Account Context", exact: true });
    // Sibling sub-tab in the same group, used to remount Account Context so a persisted
    // value can be told apart from one still held in React state.
    this.knowledgeBaseTab = page.getByRole("tab", { name: "Knowledge Base", exact: true });

    // The tab's own description copy — the one string on the surface that appears nowhere
    // else, so it is the "the tab body rendered" signal the heading cannot be. Matched on a
    // stable fragment rather than in full: the copy contains a hyphen that is easy to
    // reflow, and the leading clause is what identifies it.
    this.tabDescription = this.dialog.getByText(/Account-level rules and identity that define how your AI/).first();

    // ScopeChip renders a non-interactive ds/Chip, which is a plain span, so both are
    // scoped text matches. The tenant chip's text is the whole assertion in the
    // tenant-wide case, so it gets a widened fallback rather than a looser primary.


    this.addContextBtn = this.dialog.getByRole("button", { name: "Add Account Context" });

    this.emptyState = this.dialog.getByText("No account contexts found", { exact: true }).first();
    this.emptyStateHint = this.dialog.getByText(/Only one account context is allowed per account/).first();

    // Rendered in place of the whole tab body when the list request fails. Raced against
    // the loaded body so a backend error reports itself by name instead of surfacing as
    // every control below being absent.
    this.loadErrorBanner = this.dialog.getByText(/(Failed to fetch account contexts|An error occurred while fetching account contexts)/).first();

    // At most one card can exist per account, so its kebab is the "a context is present"
    // signal — and the handle for removing one whose name this suite has not been told.
    this.soleCardMenuBtn = this.dialog.getByRole("button", { name: "More actions" });
    // Whether the context on screen is one this suite wrote. Only ever read as a boolean:
    // a regex getByText can resolve to an ancestor as well as to the name itself, so its
    // text is never trusted, only its presence.
    this.e2eLeftover = this.dialog.getByText(new RegExp(`^${E2E_NAME_PREFIX}`)).first();

    // Rung 3 on purpose. AccountSelect passes ds/Select no `label` and no aria-label, so
    // the trigger <button> has no accessible name for getByRole to reach, and
    // `grep -c data-testid ds/Select.tsx` returns 1 — on the clear button, not on this.
    // The id it defaults to is the same handle NubiLocators.selectFirstAdminAccount()
    // already drives on the AI & Tools side. The fallback is kept inside the b-Cortex
    // dialog, where the header filter is the only listbox trigger on this tab.
    this.accountSelectTrigger = page
      .locator("#account-select")
      .or(this.dialog.locator('button[aria-haspopup="listbox"]'))
      .first();

    // ds/Select portals its panel outside the dialog, so this is page-scoped by necessity.
    // Single-rung: role=listbox is the panel's own contract and a wider match could only
    // find a different widget's list.
    this.accountListbox = page.locator('[role="listbox"]');

    // Both modals are matched by attribute rather than getByRole("dialog"): each is a MUI
    // Dialog opened on top of the b-Cortex Dialog, and MUI aria-hides everything below the
    // topmost overlay — a role lookup then reads "the modal closed" while it is on screen.
    // Filtered on their own ds/Modal titles, which are the only strings that separate them.
    this.formModal = page.locator('[role="dialog"]').filter({ hasText: /(Create|Edit) Account Context/ }).first();
    this.formModalIntro = this.formModal.getByText(/Provide account-level knowledge that will be used by the AI planner/).first();

    // ds/Input renders <label htmlFor>, so these three have real accessible names. The
    // asterisk ds/Input appends for `required` is aria-hidden, so "Name" stays exact.
    // Every fallback is scoped to the form modal: role=textbox resolves in document order,
    // and an unscoped one would land on the chat box behind two dialogs.
    this.nameInput = this.formModal
      .getByRole("textbox", { name: "Name", exact: true })
      .or(this.formModal.getByPlaceholder(/Enter a name for this account context/))
      .first();
    this.descriptionInput = this.formModal
      .getByRole("textbox", { name: "Description", exact: true })
      .or(this.formModal.getByPlaceholder("Enter a description for this account context"))
      .first();
    this.contentInput = this.formModal
      .getByRole("textbox", { name: "Content", exact: true })
      .or(this.formModal.getByPlaceholder("Paste or type your account context content here..."))
      .first();

    this.uploadZone = this.formModal.getByText("Click or drag a .txt file to upload", { exact: true }).first();
    // The <input type=file> is display:none, so it is set directly rather than clicked.
    // No fallback: it is the only file input inside the modal, and a wider match could
    // only find one belonging to another surface.
    this.fileInput = this.formModal.locator('input[type="file"]');

    // Rendered as `{content.length} characters` under the Content field — the observable
    // signal that a file's text actually reached the textarea.
    this.charCounter = this.formModal.getByText(/^\d+ characters$/).first();

    this.formCancelBtn = this.formModal.getByRole("button", { name: "Cancel", exact: true });
    // The primary button is labelled by mode, so the two are separate locators — asserting
    // "Create" while the modal opened in edit mode is exactly the confusion to avoid.
    this.formCreateBtn = this.formModal.getByRole("button", { name: "Create", exact: true });
    this.formUpdateBtn = this.formModal.getByRole("button", { name: "Update", exact: true });

    this.deleteModal = page.locator('[role="dialog"]').filter({ hasText: /Delete Account Context:/ }).first();
    this.deleteCancelBtn = this.deleteModal.getByRole("button", { name: "Cancel", exact: true });
    this.deleteConfirmBtn = this.deleteModal.getByRole("button", { name: "Delete", exact: true });

    // SnackbarComponent mounts at the app root, so it is a sibling of the dialog portals
    // and MUI marks it aria-hidden while any of them is open — getByRole finds nothing
    // even though the node is in the DOM. Matched by attribute instead.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // One context card, anchored on its own name rather than on position, so a second card
  // (or a reordered list) can never point an assertion at the wrong record. The name
  // <Typography> sits in a Box holding the name/description/timestamps, whose parent is the
  // card's flex row — the row that also holds the card's ThreeDotsMenu, which is why the
  // walk stops there rather than at the WidgetCard above it.
  contextCard(name: string): Locator {
    return this.dialog.getByText(name, { exact: true }).locator("xpath=../..").first();
  }

  // A card's description line. Matched exactly, and the exactness is load-bearing: the
  // description strings these cases write embed the record's unique name, so a substring
  // match would let the name <Typography> above satisfy an assertion about the description.
  cardDescription(text: string): Locator {
    return this.dialog.getByText(text, { exact: true }).first();
  }

  // The kebab menu on one named card. ds/ThreeDotsMenu renders a ds/Button with
  // aria-label='More actions'; scoping to the card is what keeps it off a sibling's menu.
  cardMenuBtn(name: string): Locator {
    return this.contextCard(name).getByRole("button", { name: "More actions" });
  }

  // MUI menu items in this app never match getByRole("menuitem", { name }) — the label is
  // in a nested span and the row carries aria-hidden — so they are matched by the id
  // DropdownMenu derives from each item's `value`, with a text fallback. Same form
  // nubiLocators.ts already uses for the agent and function menus.
  menuItem(id: "edit" | "delete", label: string): Locator {
    return this.page
      .locator(`[role="menuitem"]#${id}:visible`)
      .or(this.page.locator('[role="menuitem"]:visible', { hasText: label }))
      .first();
  }

  // Toast text, kept inside the notifications region so a matching string elsewhere on the
  // page cannot pass for a toast. The fallback matches the toast roles by attribute for the
  // aria-hidden reason above.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }

  // The synthetic "All accounts" entry, and the nth real account, in the open picker.
  // Both are built off the listbox field rather than a passed-in handle so a caller can
  // name the option it wants *before* the panel is open — which is what lets
  // openAccountPanel below expand only as far as that option.

  realAccountOption(index: number): Locator {
    return this.accountListbox.locator('[role="option"]').filter({ hasNotText: "All accounts" }).nth(index);
  }

  // Opens the account picker and expands group headers until `target` is reachable.
  //
  // Idempotent on purpose. Clicking the trigger while the panel is already up is not a
  // no-op: the MUI popover puts an invisible full-viewport backdrop over the trigger, so
  // the click never becomes actionable and burns its entire timeout. That is exactly how
  // the first version of this suite failed 8 of 9 cases on CI, and `aria-expanded` on the
  // trigger is the app's own flag for it.
  //
  // Expansion stops at `target` rather than clicking every header, because a header
  // TOGGLES its group: blind expansion is right only if the panel reopens fully collapsed,
  // and would close a group that survived otherwise. Stopping at the target is correct
  // either way. The headers are waited for rather than the panel — AccountSelect fetches
  // on mount and the panel opens on skeleton rows with no headers at all.
  async openAccountPanel(target: Locator, timeout = 30000): Promise<void> {
    if ((await this.accountSelectTrigger.getAttribute("aria-expanded")) !== "true") {
      await this.accountSelectTrigger.click();
    }
    await this.accountListbox.waitFor({ state: "visible", timeout });

    const groupHeaders = this.accountListbox.locator('[role="button"]');
    await expect
      .poll(() => groupHeaders.count(), { timeout, message: "the account picker never listed a group to expand" })
      .toBeGreaterThan(0);

    const groupCount = await groupHeaders.count();
    for (let i = 0; i < groupCount; i++) {
      // Probe: "not expanded far enough yet" is the normal state on every pass but the
      // last, so this must come back as false rather than throw.
      if (await target.isVisible().catch(() => false)) return;
      await groupHeaders.nth(i).click();
      // Short wait, not the method timeout: "this group does not hold the target" is a
      // normal branch, so the loop has to move on quickly — but reading visibility
      // straight after the click can beat the re-render and expand a group nothing needed.
      await target.waitFor({ state: "visible", timeout: 2000 }).catch(() => {});
    }
  }
}
