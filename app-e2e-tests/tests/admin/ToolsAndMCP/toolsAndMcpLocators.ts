// Not for OSS

import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import {
  ACCOUNT_SELECT_ID,
  AI_TOOLS_TAB_ID,
  BUTTON_CANCEL,
  BUTTON_SUBMIT,
  CREATED_BY_FILTER_ID,
  CREATE_DIALOG_TITLE,
  CREATE_TOOL_BTN_ID,
  CREATE_TOOL_BTN_NAME,
  DELETE_DIALOG_TITLE_STEM,
  FIELD_NAME,
  MCP_BANNER_TEXT,
  MCP_EMPTY_ID,
  MCP_LISTING_ID,
  MCP_STATUS_FILTER_ID,
  MCP_TABLE_BODY_ID,
  STATUS_FILTER_ID,
  TOGGLE_GROUP_NAME,
  TOGGLE_MCP,
  TOGGLE_TOOLS,
  TOOLS_EMPTY_ID,
  TOOLS_LISTING_ID,
  TOOLS_MCP_PATH,
  TOOLS_MCP_TAB_FRAGMENT,
  TOOLS_MCP_TAB_NAME,
  TOOLS_TABLE_BODY_ID,
  TOOL_SEARCH_ID,
  TOOL_SEARCH_PLACEHOLDER,
} from "./toolsAndMcpConstants";

// Page object for Admin -> AI & Tools -> Tools & MCP, whose body is
// app/src/components/llm/admin/ToolsAndMCPAdminTab.jsx mounting
// app/src/components/llm/ListTools.jsx and app/src/components/llm/MCPConfigList.jsx.
//
// Locator note for the whole file:
//   grep -c 'data-testid' app/src/components/llm/admin/ToolsAndMCPAdminTab.jsx -> 0
//   grep -c 'data-testid' app/src/components/llm/ListTools.jsx                 -> 0
//   grep -c 'data-testid' app/src/components/llm/MCPConfigList.jsx             -> 0
//   grep -c 'data-testid' app/src/components/llm/CreateTool.jsx                -> 0
//   grep -c 'data-testid' app/src/components/llm/admin/AdminAccountFilter.jsx  -> 0
// so rung 1 is unavailable everywhere on this surface. What it does give us is a
// deliberate set of ids on every toolbar control (ListTools passes id='tool-search',
// 'tool-created-by-filter', 'tool-status-filter', 'create-tool', 'integration';
// MCPConfigList passes 'mcp-config-status-filter', 'mcp-config-name-search') plus
// real ARIA: ds/ToggleGroup renders role="group" + role="radio" with aria-checked,
// ds/Modal is a MUI Dialog (role="dialog"), MUI's TableCell in a TableHead is a
// role="columnheader", ds/Input associates its label through htmlFor, and ds/Input's
// error message is a role="alert". Where an id is the primary the fallback stays
// inside the same listing container, because .or() resolves in document order and
// the two listings render structurally identical toolbars.
export class ToolsAndMcpLocators extends CommonLocators {
  // Admin page chrome.
  readonly aiToolsTab: Locator;
  readonly toolsMcpTab: Locator;

  // ToolsAndMCPAdminTab's own ToggleGroup.
  readonly toggleGroup: Locator;
  readonly toolsToggle: Locator;
  readonly mcpToggle: Locator;

  // The account filter, shown only while the Tools sub-view is active.
  readonly accountFilter: Locator;

  // Tools sub-view.
  readonly toolsListing: Locator;
  readonly toolsTableBody: Locator;
  readonly toolsRows: Locator;
  readonly toolsEmptyState: Locator;
  readonly toolSearch: Locator;
  readonly createdByFilter: Locator;
  readonly statusFilter: Locator;
  readonly createToolBtn: Locator;

  // MCP Servers sub-view.
  readonly mcpListing: Locator;
  readonly mcpTableBody: Locator;
  readonly mcpRows: Locator;
  readonly mcpEmptyState: Locator;
  readonly mcpBanner: Locator;
  readonly mcpStatusFilter: Locator;

  // Create/Edit tool dialog. Named apart from CommonLocators' own submitBtn and
  // cancelBtn, which point at a different page's #submit and an unscoped Cancel.
  readonly createDialog: Locator;
  readonly dialogSubmitBtn: Locator;
  readonly dialogCancelBtn: Locator;

  // Whichever FilterDropdown popover or ds/Select listbox is currently open.
  readonly openOptionList: Locator;

  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders every top-level tab as an <a> carrying
    // `anchor-tab-${opt.id || opt.name}`, and the AI & Tools entry pins
    // id:'AITools'. Deliberately id-only even though the link has an accessible
    // name: the sidebar's own admin flyout renders a second "AI & Tools" link,
    // so a role+name match is ambiguous and would resolve in document order to
    // the wrong one. Same reasoning tests/admin/UsageLimits records.
    this.aiToolsTab = page.locator(`#${AI_TOOLS_TAB_ID}`);

    // The sub-tab strip is a MUI Tabs, so this is a real role="tab". Deliberately
    // NOT exact: Tabs.jsx renders each icon as <SafeIcon src={opt.icon} alt={opt.text}/>,
    // and this tab's ToolsIconTabBlue is a plain require()d .svg, which SafeIcon
    // renders as <Image alt> — a real <img alt> that joins the accessible name.
    // The tab is therefore named "Tools & MCP Tools & MCP", and an exact match
    // finds nothing. A sibling whose icon came through SVGR contributes no alt and
    // is named once, so the doubling differs per tab in the same strip. The
    // fallback is the tab's own id: Tabs stamps a11yProps(opt.value, opt.id) and
    // takes the option's id when it has one, which AI_TOOLS_SUB_TABS entries do —
    // so unlike ds/Tabs' positional `#tab-${index}` it does not shift when a
    // feature flag drops the Functions or Gateway sub-tab.
    this.toolsMcpTab = page
      .getByRole("tab", { name: TOOLS_MCP_TAB_NAME })
      .or(page.locator(`#${TOOLS_MCP_TAB_FRAGMENT}`))
      .first();

    // ds/ToggleGroup sets role="group" + aria-label on its container and
    // role="radio" + aria-checked on each option, so the two toggles are scoped
    // to their own group rather than matched page-wide — which matters because
    // "Tools" is also a substring of the tab strip's own labels. No .or() on any
    // of the three: ToolsAndMCPAdminTab passes the group no id and ds/ToggleGroup
    // renders no testid, so role is both the highest and the only rung here.
    this.toggleGroup = page.getByRole("group", { name: TOGGLE_GROUP_NAME });
    this.toolsToggle = this.toggleGroup.getByRole("radio", { name: TOGGLE_TOOLS, exact: true });
    this.mcpToggle = this.toggleGroup.getByRole("radio", { name: TOGGLE_MCP, exact: true });

    // AdminAccountFilter mounts ds/Select through AccountSelect, which passes
    // id='account-select'; ds/Select puts that id straight on its trigger button.
    // Deliberately id-only: the trigger's accessible name is its own contents, so
    // it changes to whichever account is picked, and the wider
    // '[aria-haspopup="listbox"]' form would also match the Status select inside
    // the edit dialog. A fallback here would be a wrong fallback.
    this.accountFilter = page.locator(`#${ACCOUNT_SELECT_ID}`);

    // ListingLayout renders <Box id={id}>, so each sub-view has its own container
    // and every control below is scoped into it.
    this.toolsListing = page.locator(`#${TOOLS_LISTING_ID}`);
    this.mcpListing = page.locator(`#${MCP_LISTING_ID}`);

    // CustomTable derives the body id from its own `id` prop. Child combinator so
    // a cell that itself renders a table cannot inflate the row count.
    this.toolsTableBody = page.locator(`#${TOOLS_TABLE_BODY_ID}`);
    this.toolsRows = page.locator(`#${TOOLS_TABLE_BODY_ID} > tr`);
    this.mcpTableBody = page.locator(`#${MCP_TABLE_BODY_ID}`);
    this.mcpRows = page.locator(`#${MCP_TABLE_BODY_ID} > tr`);

    // EmptyData renders <h2 id={`${id}-no-data`}>. CustomTable swaps the body for
    // it, so exactly one of body/empty-state exists at a time — which is what
    // makes "no rows" distinguishable from "still loading".
    this.toolsEmptyState = page.locator(`#${TOOLS_EMPTY_ID}`);
    this.mcpEmptyState = page.locator(`#${MCP_EMPTY_ID}`);

    // ds/SearchInput renders <input id={id} placeholder={label}>, so the id is the
    // input itself. The fallback is the placeholder, scoped to this listing so it
    // cannot reach the MCP toolbar's own search box.
    this.toolSearch = this.toolsListing
      .locator(`#${TOOL_SEARCH_ID}`)
      .or(this.toolsListing.getByPlaceholder(TOOL_SEARCH_PLACEHOLDER))
      .first();

    // ds/FilterDropdown renders its trigger as
    // <button id={`auto-complete-${toKebabCase(id)}`}> and shows the filter label
    // plus the selected option's label inside it. The label text is the fallback,
    // scoped to the owning listing because both toolbars render a "Status" filter.
    this.createdByFilter = this.toolsListing.locator(`#${CREATED_BY_FILTER_ID}`).first();
    this.statusFilter = this.toolsListing.locator(`#${STATUS_FILTER_ID}`).first();
    this.mcpStatusFilter = this.mcpListing.locator(`#${MCP_STATUS_FILTER_ID}`).first();

    // ds/Button forwards `id` onto its ButtonBase. The role+name fallback is
    // scoped to the listing's own toolbar; "Create Tool" also appears as the
    // dialog's heading copy on other Nubi surfaces, so an unscoped text match
    // would resolve in document order to the wrong element.
    this.createToolBtn = this.toolsListing
      .locator(`#${CREATE_TOOL_BTN_ID}`)
      .or(this.toolsListing.getByRole("button", { name: CREATE_TOOL_BTN_NAME }))
      .first();

    // ds/Banner with tone='info' renders role="status" (Banner.tsx picks 'alert'
    // only for critical/warning). Filtered on its own copy so the toast region,
    // which carries the same role, cannot satisfy it.
    this.mcpBanner = page
      .getByRole("status")
      .filter({ hasText: MCP_BANNER_TEXT })
      .or(page.getByText(MCP_BANNER_TEXT, { exact: false }))
      .first();

    // ds/Modal portals to the body, so the dialog is a DOM sibling of the page
    // rather than a descendant. Filtering role="dialog" on the title copy is what
    // separates the create dialog from the delete confirmation, which the same
    // component mounts alongside it.
    this.createDialog = page.getByRole("dialog").filter({ hasText: CREATE_DIALOG_TITLE }).first();

    // Every control in the dialog is a ds/Input or ds/Button that CreateTool
    // passes no id and no testid, and ds/Input derives its DOM id from
    // React.useId() — regenerated per render and unselectable. So role+name is
    // both the highest and the only rung, and each one is scoped to the dialog,
    // which is what keeps the page's own buttons out of the match.
    this.dialogSubmitBtn = this.createDialog.getByRole("button", { name: BUTTON_SUBMIT, exact: true });
    this.dialogCancelBtn = this.createDialog.getByRole("button", { name: BUTTON_CANCEL, exact: true });

    // ds/FilterDropdown's panel is a MUI Popover and ds/Select's is a MUI Menu;
    // both render their rows as [role="option"], and only one can be open at a
    // time. :visible is what keeps a closed, still-mounted popover from matching.
    this.openOptionList = page.locator('[role="option"]:visible');

    // SnackbarComponent mounts at the app root. While a ds/Modal is open MUI marks
    // the rest of the app aria-hidden, which removes the toast from the
    // accessibility tree — so it is matched by attribute, which is DOM-based,
    // exactly as tests/admin/UsageLimits and tests/nubi/model-pricing do.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // Opens Admin -> AI & Tools with the Tools & MCP sub-tab selected, via the hash
  // route the page resolves on mount.
  async open(): Promise<void> {
    // These specs navigate straight in on the stored auth state rather than
    // through doFullLogin(), so the first-login tour is not yet handled on this
    // page and would intercept clicks. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // domcontentloaded, not the default "load": the dashboard's subresources keep
    // the load event pending well past the point the tab is usable on dev.
    await this.page.goto(TOOLS_MCP_PATH, { waitUntil: "domcontentloaded" });
    await this.waitForToolsBody();
  }

  // The readiness signal for the Tools sub-view. CustomTable renders skeleton rows
  // while `loading` is true and swaps in either the body or the empty state once
  // listTools resolves, so one of those two existing means the first fetch landed.
  async waitForToolsBody(): Promise<void> {
    await this.toolsToggle.waitFor({ state: "visible", timeout: 120000 });
    await expect(this.toolsToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
    await this.toolSearch.waitFor({ state: "visible", timeout: 90000 });
    await expect(this.toolsTableBody.or(this.toolsEmptyState)).toBeVisible({ timeout: 120000 });
  }

  // The same signal for the MCP sub-view, whose fetch is a separate round trip.
  async waitForMcpBody(): Promise<void> {
    await this.mcpStatusFilter.waitFor({ state: "visible", timeout: 90000 });
    await expect(this.mcpTableBody.or(this.mcpEmptyState)).toBeVisible({ timeout: 120000 });
  }

  // Switches to the MCP sub-view and waits for its own listing, rather than for
  // the toggle's paint — ToolsAndMCPAdminTab renders one branch or the other, so
  // the Tools listing going away is part of the transition.
  async showMcpServers(): Promise<void> {
    await this.mcpToggle.click();
    await expect(this.mcpToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
    await expect(this.toolsListing).toHaveCount(0, { timeout: 30000 });
    await this.waitForMcpBody();
  }

  async showTools(): Promise<void> {
    await this.toolsToggle.click();
    await expect(this.toolsToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
    await expect(this.mcpListing).toHaveCount(0, { timeout: 30000 });
    await this.waitForToolsBody();
  }

  // A column header of either listing's table. MUI renders a TableHead TableCell
  // as <th>, which is role="columnheader"; scoping to the listing is what keeps
  // the Tools and MCP tables' identically named headers apart.
  toolsHeader(name: string): Locator {
    return this.toolsListing.getByRole("columnheader", { name, exact: true }).first();
  }

  mcpHeader(name: string): Locator {
    return this.mcpListing.getByRole("columnheader", { name, exact: true }).first();
  }

  // The Name cell of one tools row. It carries the title-cased tool name and the
  // OwnerTypeBadge, which is what the Created By assertions read.
  toolNameCell(index: number): Locator {
    return this.toolsRows.nth(index).locator("td").first();
  }

  // The Status cell. ListTools renders it as the third column in both the
  // tenant-wide and per-account shapes, so the index is stable across the account
  // filter — the columns that differ are the fourth and fifth.
  toolStatusCell(index: number): Locator {
    return this.toolsRows.nth(index).locator("td").nth(2);
  }

  // The Status cell of one MCP row — the fifth of MCPConfigList's five columns,
  // rendering the raw stored value through ds/Label. MCPConfigList gives the row
  // no id or testid, so the ordinal is the only handle available.
  mcpStatusCell(index: number): Locator {
    return this.mcpRows.nth(index).locator("td").nth(4);
  }

  // One option inside whichever popover is open. Anchored on both ends because
  // option labels share prefixes — a substring match would pick the wrong one. No
  // .or(): getByRole("option", { name }) matches zero against these rows, the same
  // nested-span reason menu items fail on in this app.
  option(label: string): Locator {
    return this.openOptionList.filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) }).first();
  }

  // Opens a ds/FilterDropdown and commits one option, waiting on the trigger's own
  // text rather than on a fixed pause — the trigger renders the selected label
  // beside the filter label, so that is the signal the selection landed.
  async chooseFilterOption(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.openOptionList.first().waitFor({ state: "visible", timeout: 20000 });
    await this.option(label).click();
    await expect(trigger).toContainText(label, { timeout: 20000 });
  }

  // Every label the tools Status filter currently offers. ListTools derives these
  // from the loaded rows rather than hard-coding them, so a spec that needs a real
  // status has to read them at runtime.
  async statusFilterOptionLabels(): Promise<string[]> {
    await this.statusFilter.click();
    await this.openOptionList.first().waitFor({ state: "visible", timeout: 20000 });
    const labels = await this.openOptionList.allInnerTexts();
    await this.page.keyboard.press("Escape");
    await expect(this.openOptionList).toHaveCount(0, { timeout: 20000 });
    return labels.map((label) => label.trim()).filter((label) => label.length > 0);
  }

  // Commits one account in the AI & Tools account filter. ds/Select groups the
  // options by cloud provider and leaves every group collapsed until it is clicked
  // (Select.tsx only auto-expands while a search is active); a tenant whose
  // accounts share one provider renders the flat list instead, where there is
  // nothing to expand — hence the loop over whatever headers exist rather than an
  // assumption either way. Same shape tests/admin/UsageLimits relies on.
  async revealAccountOptions(): Promise<void> {
    await this.page.locator('[role="listbox"]:visible').first().waitFor({ state: "visible", timeout: 20000 });
    const headers = this.page.locator('[role="listbox"]:visible [role="button"]');
    const headerCount = await headers.count();
    for (let i = 0; i < headerCount; i += 1) {
      await headers.nth(i).click();
    }
    await this.openOptionList.first().waitFor({ state: "visible", timeout: 20000 });
  }

  // Every account label the filter offers, minus the synthetic "All accounts"
  // entry AccountSelect prepends via includeAllOption.
  async accountOptionLabels(): Promise<string[]> {
    await this.accountFilter.click();
    await this.revealAccountOptions();
    const labels = await this.openOptionList.allInnerTexts();
    await this.page.keyboard.press("Escape");
    await expect(this.openOptionList).toHaveCount(0, { timeout: 20000 });
    return labels.map((label) => label.trim()).filter((label) => label.length > 0);
  }

  async chooseAccount(label: string): Promise<void> {
    await this.accountFilter.click();
    await this.revealAccountOptions();
    await this.option(label).click();
    await expect(this.accountFilter).toContainText(label, { timeout: 20000 });
  }

  // A ds/Input inside the create dialog. ds/Input associates its <label> through
  // htmlFor, and the required marker is an aria-hidden span, so the accessible
  // name stays the bare label.
  dialogField(label: string): Locator {
    return this.createDialog.getByRole("textbox", { name: label, exact: true });
  }

  // ds/Input renders its error message as <span role="alert">, scoped to the
  // dialog so the app's toast region cannot satisfy it.
  dialogError(text: string): Locator {
    return this.createDialog.getByRole("alert").filter({ hasText: text }).first();
  }

  // Toast text, kept inside the notifications region so the same words rendered in
  // the page body cannot pass for a toast. The fallback matches the toast's own
  // roles by attribute, for the aria-hidden reason noted on the region.
  toastWithText(text: string | RegExp): Locator {
    return this.notificationsRegion
      .getByText(text)
      .or(this.page.locator('[role="status"], [role="alert"]').filter({ hasText: text }))
      .first();
  }

  // The delete confirmation dialog. Matched on the title stem only — the name it
  // interpolates goes through snakeToTitleCase, which uppercases parts listed in
  // UPPERCASE_ACRONYMS ("mcp" among them), so reproducing the rendered label here
  // would be a second implementation of app code for this suite to keep in sync.
  // Only one delete dialog can be open at a time, so the stem is unambiguous.
  deleteDialog(): Locator {
    return this.page.getByRole("dialog").filter({ hasText: DELETE_DIALOG_TITLE_STEM }).first();
  }

  async openCreateDialog(): Promise<void> {
    await this.createToolBtn.click();
    await this.createDialog.waitFor({ state: "visible", timeout: 30000 });
    await expect(this.dialogField(FIELD_NAME)).toBeVisible({ timeout: 30000 });
  }
}
