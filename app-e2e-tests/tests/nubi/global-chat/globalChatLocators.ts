import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { NubiLocators } from "../nubiLocators";

// Nubi's global chat — the floating launcher and the docked panel that _app.tsx mounts on
// every page (app/src/components/llm/NubiGlobalChat.tsx). `grep -c data-testid`
// returns 10 for that file, so every control this suite drives is reached at rung 1. Three
// elements are not its own and are called out where they are built: the drawer shell and its resize
// handle come from CustomDrawer.jsx, the account chip's trigger is a ds/FilterDropdown, and
// the Your Chats list is ConversationListDrawer's Popper — none of the three renders a testid.
export class GlobalChatLocators extends CommonLocators {
  readonly launcher: Locator;
  readonly panel: Locator;
  readonly panelPaper: Locator;
  readonly resizeHandle: Locator;
  readonly openDetailBtn: Locator;
  readonly accountChip: Locator;
  readonly accountChipTrigger: Locator;
  readonly accountPopover: Locator;
  readonly accountGroupHeaders: Locator;
  readonly accountOptions: Locator;
  readonly newChatBtn: Locator;
  readonly historyBtn: Locator;
  readonly expandToggle: Locator;
  readonly closeBtn: Locator;
  readonly yourChatsHeading: Locator;
  readonly composer: Locator;

  constructor(page: Page) {
    super(page);

    // Deliberately testid-only. The launcher's accessible name is `Open ${assistantName}`,
    // built from tenant branding, and the panel header's own title carries
    // `Open ${assistantName} full page` — so a name-based fallback would either drift with a
    // white-label tenant's assistant name or match both elements on the same page.
    this.launcher = page.getByTestId("nubi-global-chat-launcher").first();

    // The drawer is a MUI temporary Drawer, so it renders no role and CustomDrawer.jsx adds no
    // testid; this attribute is the app's own "the chat is open" signal, written by
    // NubiGlobalChat.tsx for the Cmd/Ctrl+K page-search handler to read. Built once into a
    // local and reused below so the selector is not written out twice.
    const openFlag = page.locator('[data-nubi-chat-open="true"]');
    this.panel = openFlag.first();

    // The Paper, not the content box: the resize handle is the content box's SIBLING, and the
    // width the drawer actually commits is set on the Paper. Matched through the content box so
    // it cannot pick up some other drawer's paper.
    this.panelPaper = page.locator(".MuiDrawer-paper").filter({ has: openFlag }).first();
    // Single-rung attribute match, scoped to this panel's paper. CustomDrawer's handle is a bare
    // styled div — no role, no testid — so its aria-label is the only thing on it to match, and
    // getByLabel is documented for labelled controls rather than for a plain div.
    this.resizeHandle = this.panelPaper.locator('[aria-label="Resize drawer"]').first();

    // Header controls. Every one carries a fixed aria-label, so the fallback is a real second
    // rung scoped to the same panel — except the two whose name is branding-derived.
    this.openDetailBtn = this.panel.getByTestId("nubi-global-chat-open-detail").first();
    this.newChatBtn = this.panel
      .getByTestId("nubi-global-chat-new-chat")
      .or(this.panel.getByRole("button", { name: "New chat" }))
      .first();
    this.historyBtn = this.panel
      .getByTestId("nubi-global-chat-history")
      .or(this.panel.getByRole("button", { name: "Your Chats" }))
      .first();
    // Name-less on purpose: this one control's aria-label flips between Expand and Collapse, and
    // that flip is what the expand case asserts — a name-based fallback would bake in one state.
    this.expandToggle = this.panel.getByTestId("nubi-global-chat-expand").first();
    this.closeBtn = this.panel
      .getByTestId("nubi-global-chat-close")
      .or(this.panel.getByRole("button", { name: "Close" }))
      .first();

    // The chip is the testid'd wrapper; the clickable trigger inside it is a ds/FilterDropdown,
    // which renders `auto-complete-<id>` and no testid, and whose accessible name is whichever
    // account is selected — so the id is the right rung here and the fallback is the only button
    // the wrapper holds.
    this.accountChip = this.panel.getByTestId("nubi-global-chat-account").first();
    this.accountChipTrigger = this.accountChip
      .locator("#auto-complete-nubi-global-chat-account-select")
      .or(this.accountChip.locator("button"))
      .first();

    // ds/FilterDropdown's panel is a MUI Popover that portals to the body, so it cannot be
    // scoped to the chip — its paper is the container everything below hangs off. Matched by
    // class because MUI gives the paper no role, no name and no testid.
    //
    // Both filters are load-bearing, not belt-and-braces. The composer's own Agent and Model
    // dropdowns are MUI Menus, which are Popovers too and stay mounted after use — a trace of
    // the failing run showed `MuiPopover-paper MuiMenu-paper` on a `MuiModal-hidden` root
    // sitting AHEAD of the picker in document order, so a bare `.first()` resolves to that
    // hidden menu and finds none of the rows below.
    this.accountPopover = page.locator(".MuiPopover-paper:not(.MuiMenu-paper):visible").first();
    // Grouped mode opens COLLAPSED: one role=button row per cloud provider, and zero option rows
    // until a group is toggled open. This also matches the per-group "Clear" control that an
    // expanded group with a selection renders inside its own header — see pickOtherAccount.
    this.accountGroupHeaders = this.accountPopover.locator('[role="button"]');
    this.accountOptions = this.accountPopover.locator('[role="option"]');

    // ConversationListDrawer's Popper heading. The history button is icon-only, so this text
    // match cannot be satisfied by the button that opened it.
    this.yourChatsHeading = page.getByText("Your Chats", { exact: true }).first();

    // The same composer NubiLocators already builds — read off that property rather than
    // repeating its placeholder pair, which differs between the panel and the full page.
    this.composer = new NubiLocators(page).chatTextbox;
  }

  // One account row in the open chip picker, by its position in the list the case just read.
  // Positional because the row to click is whichever account the tenant happens to offer second
  // — there is no fixed name to match on, and re-querying by the label just read would resolve
  // through the same list anyway.
  accountOptionAt(index: number): Locator {
    return this.accountOptions.nth(index);
  }
}
