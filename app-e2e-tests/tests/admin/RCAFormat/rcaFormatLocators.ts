// Not for OSS

import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import {
  ACCOUNT_PLACEHOLDER,
  ACCOUNT_SELECT_ID,
  AI_TOOLS_TAB_ID,
  BUDGETS_LIMITS_TAB_ELEMENT_ID,
  BUDGETS_LIMITS_TAB_NAME,
  RCA_FORMAT_TAB_ELEMENT_ID,
  EMPTY_STATE_TITLE,
  OVERLAY_SEARCH_PLACEHOLDER,
  RCA_FORMAT_PATH,
  RCA_FORMAT_TAB_NAME,
  RCA_HEADER,
  SAVE_BUTTON,
} from "./rcaFormatConstants";

// Page object for Admin -> AI & Tools -> RCA Format, whose body is
// app/src/components/llm/RCAFormatTab.jsx mounted by
// app/src/components/llm/admin/RCAFormatAdminTab.jsx behind a required
// AdminAccountFilter.
//
// Locator note for the whole file:
//   grep -c 'data-testid' app/src/components/llm/RCAFormatTab.jsx -> 0
//   grep -c 'data-testid' app/src/components/llm/admin/RCAFormatAdminTab.jsx -> 0
//   grep -c 'data-testid' app/src/components/llm/admin/AdminAccountFilter.jsx -> 0
//   grep -c 'data-testid' app/src/components/ownership/AccountSelect.jsx -> 0
// so rung 1 is unavailable across this surface. What it does give us is real
// ARIA: the sub-tab strip is a MUI Tabs (role="tab"), ds/Select's popover list
// carries role="listbox" with role="option" rows, ds/EmptyState renders
// role="status", and ds/Button forwards its label as the accessible name. The
// two exceptions are the select trigger, which ds/Select renders as an
// unlabelled <button> (AdminAccountFilter passes no `label`), and the editor,
// which is CodeMirror's own contenteditable — both are noted where declared.
export class RCAFormatLocators extends CommonLocators {
  // Admin page chrome.
  readonly aiToolsTab: Locator;
  readonly rcaFormatTab: Locator;
  readonly budgetsLimitsTab: Locator;

  // RCAFormatAdminTab's required account picker and its unpicked state.
  readonly accountSelect: Locator;
  readonly emptyState: Locator;

  // RCAFormatTab body.
  readonly rcaHeader: Locator;
  // Not named saveBtn: CommonLocators already owns that name for Integrations'
  // #create-integration-acc, which is a different control on a different page.
  readonly saveChangesBtn: Locator;
  readonly editor: Locator;

  // Whichever ds/Select popover is currently open, and its own search box.
  readonly optionList: Locator;
  readonly optionSearch: Locator;

  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders every top-level tab as an <a> carrying
    // `anchor-tab-${opt.id || opt.name}`, and the AI & Tools filter pins
    // id:'AITools'. Deliberately id-only even though the link has an accessible
    // name: the sidebar's own admin flyout renders a second "AI & Tools" link,
    // so a role+name match is ambiguous and would resolve in document order to
    // the wrong one. Same call tests/admin/UsageLimits documents.
    this.aiToolsTab = page.locator(`#${AI_TOOLS_TAB_ID}`);

    // The sub-tab strip is a MUI Tabs, so these are real role="tab" nodes and
    // both names are unique among the page's tabs.
    //
    // Deliberately NOT exact. Tabs.jsx renders each sub-tab's icon as
    // <SafeIcon src={opt.icon} alt={opt.text}/>, and SafeIcon has two branches:
    // an SVGR icon renders as a React component with no alt, while a plain
    // required .svg resolves to a static asset with .src and renders as
    // <Image alt={alt}/> — a real <img alt> that joins the accessible name. So a
    // tab's name is "<text>" for one kind of icon and "<text> <text>" for the
    // other, purely by how the asset happens to be exported. RCA Format's
    // FileOutlineIcon is the second kind (require('…/file-outline.svg')) and
    // Budgets & Limits' LLMConsumptionIcon is the first (…icon.svg via SVGR),
    // which is why an exact match found this tab zero times while the identical
    // pattern passes in tests/admin/UsageLimits. A substring match is correct
    // for both, and stays correct if either icon is ever swapped.
    this.rcaFormatTab = page
      .getByRole("tab", { name: RCA_FORMAT_TAB_NAME })
      .or(page.locator(`#${RCA_FORMAT_TAB_ELEMENT_ID}`))
      .first();
    this.budgetsLimitsTab = page
      .getByRole("tab", { name: BUDGETS_LIMITS_TAB_NAME })
      .or(page.locator(`#${BUDGETS_LIMITS_TAB_ELEMENT_ID}`))
      .first();

    // ds/Select renders its trigger as <button id={id} aria-haspopup="listbox">
    // and AccountSelect defaults that id to 'account-select'. AdminAccountFilter
    // passes no `label`, so the button has no accessible name and rung 2 does
    // not exist here — the id is the highest rung available.
    //
    // Deliberately id-only. The only wider handle is the popup attribute, and
    // RCAFormatAdminTab gives its filter row no role, id or testid to scope that
    // to — so the fallback would have to be page-wide, and .or() resolves in
    // document order, which on this page reaches the header's own ds/Select
    // pickers first. A fallback that silently drives the cluster switcher is
    // worse than no fallback at all.
    this.accountSelect = page.locator(`#${ACCOUNT_SELECT_ID}`).first();

    // ds/EmptyState renders role="status" on its container. Filtered on its own
    // title because the toast region shares that role — the filter is what keeps
    // a snackbar from satisfying this.
    this.emptyState = page.getByRole("status").filter({ hasText: EMPTY_STATE_TITLE }).first();

    // Both are bare Typography with no role, id or testid, so their own copy is
    // the only handle and there is nothing to fall back to. Ancestors carry the
    // same text, hence .first().
    this.rcaHeader = page.getByText(RCA_HEADER, { exact: true }).first();

    // ds/Button forwards its children as the accessible name and this is the
    // only button on the tab body, so role+name is the top rung with no wider
    // match worth adding.
    this.saveChangesBtn = page.getByRole("button", { name: SAVE_BUTTON, exact: true });

    // RCAFormatTab mounts @uiw/react-codemirror directly and passes it no id or
    // testid, so CodeMirror's own contenteditable is the editable surface. Same
    // handle tests/Dashboards and tests/workflow/TaskRunner already rely on.
    this.editor = page.locator(".cm-content").first();

    // ds/Select's popover is a MUI Menu whose list carries role="listbox". Only
    // one can be open at a time, and :visible is what keeps a closed but still
    // mounted popover from matching.
    this.optionList = page.locator('[role="listbox"]:visible').first();

    // OverlaySearch is a MUI InputBase, so it is a real textbox; both branches
    // stay scoped to the open popover rather than matching a page-level input.
    this.optionSearch = this.optionList
      .getByPlaceholder(OVERLAY_SEARCH_PLACEHOLDER)
      .or(this.optionList.getByRole("textbox"))
      .first();

    // SnackbarComponent mounts at the app root. While a ds/Select popover is up
    // MUI marks the rest of the app aria-hidden, which removes the toast from
    // the accessibility tree — so it is matched by attribute, which is DOM-based,
    // exactly as tests/admin/UsageLimits does.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // Opens Admin -> AI & Tools with the RCA Format sub-tab selected, via the hash
  // route the page resolves on mount.
  async open(): Promise<void> {
    // These specs navigate straight in on the stored auth state rather than
    // through doFullLogin(), so the first-login tour is not yet handled on this
    // page and would intercept clicks. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // domcontentloaded, not the default "load": the dashboard's subresources keep
    // the load event pending well past the point the tab is usable on dev.
    await this.page.goto(RCA_FORMAT_PATH, { waitUntil: "domcontentloaded" });
    await this.waitForTabShell();
  }

  // The readiness signal for the sub-tab itself, before any account is picked.
  // Asserted outermost-first — parent tab, then sub-tab, then body — so a hash
  // that resolved to the wrong level names the level that went wrong. The
  // picker cannot lead: Agents, Tools & MCP and Functions each mount their own
  // AdminAccountFilter, and Access & Users' Ownership tab renders an
  // AccountSelect too, so a visible #account-select is not on its own evidence
  // that this is the right tab.
  async waitForTabShell(): Promise<void> {
    await expect(this.aiToolsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 120000 });
    await expect(this.rcaFormatTab).toHaveAttribute("aria-selected", "true", { timeout: 60000 });
    await this.accountSelect.waitFor({ state: "visible", timeout: 60000 });
  }

  // The readiness signal for the editor body. RCAFormatTab renders a Skeleton
  // until getRcaFormat resolves and RCAFormatAdminTab renders a Loader until the
  // CodeMirror chunk arrives, so the contenteditable being painted means both
  // have landed.
  async waitForEditorBody(): Promise<void> {
    await this.rcaHeader.waitFor({ state: "visible", timeout: 90000 });
    await this.editor.waitFor({ state: "visible", timeout: 90000 });
  }

  // One option inside the open popover, anchored on both ends because account
  // labels share prefixes — a substring match would pick the wrong account. No
  // .or(): getByRole("option", { name }) matches zero against these MUI list
  // rows, the same nested-span reason menu items fail on in this app.
  option(label: string): Locator {
    return this.optionList
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  // Toast text, kept inside the notifications region so the same words rendered
  // in the page body cannot pass for a toast. The fallback matches the toast's
  // own roles by attribute, for the aria-hidden reason noted on the region.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }

  // Opens the account picker and makes its options reachable. ds/Select drops
  // group headers below two distinct groups, so a tenant whose accounts all
  // share one cloud provider renders a flat list with nothing to expand — hence
  // the branch rather than an assumption either way.
  async openAccountOptions(): Promise<void> {
    await this.accountSelect.click();
    await this.optionList.waitFor({ state: "visible", timeout: 30000 });

    const headers = this.optionList.locator('[role="button"]');
    const options = this.optionList.locator('[role="option"]');

    // AccountSelect fetches its accounts on mount, and the popover opens on
    // whatever that fetch has got to: while it is in flight ds/Select paints
    // skeleton rows and neither headers nor options exist. Waiting for either to
    // appear is what separates "still loading" from "flat list", which reading
    // the header count alone cannot do.
    await expect
      .poll(async () => (await headers.count()) + (await options.count()), {
        timeout: 60000,
        message: "the account picker listed neither a group header nor an account",
      })
      .toBeGreaterThan(0);

    if ((await headers.count()) === 0) {
      await options.first().waitFor({ state: "visible", timeout: 30000 });
      return;
    }

    const headerCount = await headers.count();
    for (let i = 0; i < headerCount; i += 1) {
      // Each header TOGGLES its group, so an already-expanded one would close
      // again — stop as soon as an option is reachable. isVisible() is the right
      // probe here precisely because it does not retry or throw: it answers
      // "expanded yet?" about the current DOM, and .first() keeps it out of
      // strict mode, so no absence handling is needed around it.
      if (await options.first().isVisible()) break;
      await headers.nth(i).click();
      // Short wait, not the method timeout: "this group is empty" is a normal
      // branch here, so the loop has to move on quickly — but reading visibility
      // straight after the click can beat the re-render and expand a group
      // nothing needed.
      await options.first().waitFor({ state: "visible", timeout: 2000 }).catch(() => {});
    }

    await options.first().waitFor({ state: "visible", timeout: 30000 });
  }

  // Every account label the picker currently offers, read with the popover open
  // and closed again afterwards so the caller lands back on a settled page.
  async accountOptionLabels(): Promise<string[]> {
    await this.openAccountOptions();
    const labels = await this.optionList.locator('[role="option"]').allInnerTexts();
    await this.page.keyboard.press("Escape");
    // Every caller goes on to click the trigger again, which a popover still
    // covering it would swallow — so the close is asserted rather than swallowed.
    // "hidden" already covers the detached case, which is how ds/Select usually
    // closes, so this passes on either shape and only fires if the popover is
    // genuinely stuck.
    await this.optionList.waitFor({ state: "hidden", timeout: 30000 });
    return labels.map((label) => label.trim()).filter((label) => label.length > 0);
  }

  // Commits one account and waits on the trigger's own text rather than on a
  // fixed pause — ds/Select renders the selected label there, so that is the
  // signal the selection landed.
  async chooseAccount(label: string): Promise<void> {
    await this.openAccountOptions();
    await this.option(label).click();
    await expect(this.accountSelect).toContainText(label, { timeout: 30000 });
    await this.waitForEditorBody();
  }

  // Commits whichever account the picker offers first, and returns its label so
  // the caller can assert against the same account later.
  async chooseFirstAccount(): Promise<string> {
    await this.openAccountOptions();
    const first = this.optionList.locator('[role="option"]').first();
    const label = ((await first.innerText()) ?? "").trim();
    if (!label) throw new Error("the account picker offered an option with no label");
    await first.click();
    await expect(this.accountSelect).toContainText(label, { timeout: 30000 });
    await this.waitForEditorBody();
    return label;
  }
}
