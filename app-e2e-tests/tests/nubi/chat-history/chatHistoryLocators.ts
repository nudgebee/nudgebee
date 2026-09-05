// Not for OSS
import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Ask Nubi (full page) > Chat History rail
// (app/src/components/llm/ConversationListV2.jsx).
//
// Pinned to the full /ask-nudgebee page on purpose. The same idea renders twice:
// KubernetesLLMResponseGeneratorV2 mounts ConversationListDrawer ("Your Chats", a
// 320px popper with no search, no scope toggle and no filter chips) when Nubi runs
// as the floating panel, and ConversationListV2 ("Chat History") only when it does
// not. A locator that matched either would let these tests pass against the
// component they are not about, so every locator here is anchored inside the rail.
//
// ConversationListV2 renders exactly two data-testid sites — the type-filter
// trigger and the two conv-state-* chips — and those are taken at rung 1. The
// scope buttons, the collapse button and the per-row kebab all carry accessible
// names and are rung 2. SearchInput (ds/SearchInput.jsx) renders zero testids and
// no role beyond the bare input, so its caller-supplied id is rung 3 there.
// Conversation rows have no handle of their own at all and are reached at rung 5,
// scoped to the rail, via the role ButtonBase gives every ListItemButton.
export class ChatHistoryLocators extends CommonLocators {
  readonly appHeader: Locator;
  readonly askNubiPageBtn: Locator;
  readonly chatsRailBtn: Locator;
  readonly panel: Locator;
  readonly panelTitle: Locator;
  readonly searchInput: Locator;
  readonly clearSearchBtn: Locator;
  readonly collapseBtn: Locator;
  readonly mineScopeBtn: Locator;
  readonly everyoneScopeBtn: Locator;
  readonly typeMenuTrigger: Locator;
  readonly savedChip: Locator;
  readonly waitingChip: Locator;
  readonly conversationRows: Locator;
  readonly dateGroupHeadings: Locator;
  readonly deleteMenuItem: Locator;
  readonly toggleSaveMenuItem: Locator;
  readonly savedSnackbar: Locator;
  readonly unsavedSnackbar: Locator;
  readonly deletedSnackbar: Locator;

  constructor(page: Page) {
    super(page);

    // Single-rung on purpose: this is a scoping anchor, not an assertion target,
    // and the header carries no role or testid a fallback could use — a wider
    // match would silently widen the one locator scoped to it.
    this.appHeader = page.locator("#app-sticky-header");

    // The header's Nubi shortcut is the only entry point that lands on the full
    // /ask-nudgebee page carrying an accountId (GlobalPageSearch.jsx's
    // handleAskAi builds `/ask-nudgebee?accountId=...`); every other Nubi entry
    // opens the floating panel instead. Its accessible name is tenant-branded
    // (`Ask ${assistantName}`), so the name is matched as a prefix and the id is
    // kept as the fallback, both scoped to the header so neither rung can
    // resolve into the home page's own Nubi input.
    this.askNubiPageBtn = this.appHeader
      .getByRole("button", { name: /^Ask / })
      .or(this.appHeader.locator("#global-search-trigger-ask-ai"))
      .first();

    // AskNudgebeeLayoutV2's left rail. The item renders its own "Chats" caption
    // inside the button, and the icon repeats it as alt text — used as the
    // fallback because the button's aria-labelledby points at an id the layout
    // never renders, so the accessible name comes from that caption alone.
    this.chatsRailBtn = page
      .getByRole("button", { name: "Chats", exact: true })
      .or(page.locator("button").filter({ has: page.locator('img[alt="Chats"]') }))
      .first();

    // The rail root has no id, testid or role of its own. Anchoring on the search
    // box and walking up to the nearest ancestor that also holds the list is what
    // separates the panel from its header strip — the header contains the search
    // box but no list, so the walk cannot stop short. Deliberately single-rung:
    // every locator below scopes to this, so a wider fallback would widen all of
    // them at once.
    this.panel = page.locator("#search-chat").locator("xpath=ancestor::div[.//ul][1]");

    // Single-rung: "Chat History" is the string that tells this rail apart from
    // the drawer's "Your Chats", so a fallback would defeat the check it backs.
    this.panelTitle = this.panel.getByText("Chat History", { exact: true }).first();

    this.searchInput = page
      .locator("#search-chat")
      .or(this.panel.getByPlaceholder("Search conversations..."))
      .first();

    // Rendered by ds/SearchInput only while the box holds a value. Single-rung:
    // the aria-label is the only handle on that bare MUI icon, and a positional
    // fallback would land on the leading search icon beside it.
    this.clearSearchBtn = this.panel.getByLabel("clear search").first();

    this.collapseBtn = this.panel
      .getByRole("button", { name: "Collapse Recent" })
      .or(this.panel.locator("button").filter({ has: page.locator('img[alt="collapse"]') }))
      .first();

    // workflow/NewToggleButtons renders MUI Buttons that carry both their label
    // and a `workflow-tab-<value>` id; the label is the rung-2 primary and the id
    // the fallback, both scoped to the rail so "Mine" cannot match a chat bubble.
    this.mineScopeBtn = this.panel
      .getByRole("button", { name: "Mine", exact: true })
      .or(this.panel.locator("#workflow-tab-Mine"))
      .first();
    this.everyoneScopeBtn = this.panel
      .getByRole("button", { name: "Everyone", exact: true })
      .or(this.panel.locator("#workflow-tab-Everyone"))
      .first();

    // Single-rung by design: the trigger is a bare Box with no role, and the only
    // text it carries is the current selection ("Type: All" → "Chats"), which is
    // the very thing the filter test asserts on — a text fallback would let the
    // assertion satisfy itself.
    this.typeMenuTrigger = page.getByTestId("conv-type-menu");

    // Same reasoning: the chips are Boxes with no role, and their labels are the
    // words the filter tests read back.
    this.savedChip = page.getByTestId("conv-state-saved");
    this.waitingChip = page.getByTestId("conv-state-waiting");

    // Conversation rows carry no id, testid or accessible name — the only thing
    // that marks one is the role MUI's ButtonBase puts on every ListItemButton.
    // The direct-child combinator keeps the date group headings (plain <p>) and
    // the nested kebab button out of the set.
    this.conversationRows = this.panel.locator('ul > [role="button"]');

    // The five buckets ConversationListV2's getDateGroup can emit. Matched
    // exactly, and single-rung for that reason: a conversation whose own title
    // says "Today" would otherwise pass for a heading.
    this.dateGroupHeadings = this.panel.getByText(/^(Today|Yesterday|Last 7 Days|Last 30 Days|Older)$/);

    // ds/ThreeDotsMenu keeps its items mounted, so an item from a row whose kebab
    // is closed is still in the DOM — `:visible` is what pins these to the menu
    // that is actually open. Ids come from the v1 menuItem ids ConversationListV2
    // sets; the hasText fallbacks cover a rename of those ids.
    this.deleteMenuItem = page
      .locator('[role="menuitem"]#delete:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: "Delete" }))
      .first();
    // One item, two labels: it reads "Save" or "Unsave" depending on the row's
    // current state, which is why the id is the primary here.
    this.toggleSaveMenuItem = page
      .locator('[role="menuitem"]#toggle-save:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: /^(Save|Unsave)$/ }))
      .first();

    // ds/Toast renders no role or testid, so these are rung 4 on the exact copy
    // useConversationManager and ConversationListV2 emit. Single-rung on purpose:
    // the three strings differ only in one word, and a looser match would let the
    // save toast satisfy an unsave assertion.
    this.savedSnackbar = page.getByText("Conversation saved successfully");
    this.unsavedSnackbar = page.getByText("Conversation unsaved successfully");
    this.deletedSnackbar = page.getByText("Conversation deleted successfully");
  }

  // The kebab only mounts while its own row is hovered, so callers hover first.
  // Single-rung: ds/ThreeDotsMenu gives every kebab in the app the same
  // `three-dot-menu` id, so scoping to the row plus the accessible name is what
  // makes this one unambiguous — an id fallback would reintroduce the collision.
  rowMenuBtn(row: Locator): Locator {
    return row.getByRole("button", { name: "More actions" }).first();
  }

  // Type-filter options are DropdownMenu items with no id of their own, so they
  // are matched on the label text ConversationListV2 gives them, inside the one
  // menu that is open.
  typeMenuItem(label: string): Locator {
    return this.page.locator('[role="menuitem"]:visible').filter({ hasText: label }).first();
  }
}
