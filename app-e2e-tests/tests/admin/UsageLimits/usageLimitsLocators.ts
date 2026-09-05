// Not for OSS

import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import {
  ACTIVE_BUDGETS_HEADING,
  ADD_BUDGET_BUTTON,
  AI_TOOLS_TAB_ID,
  APPLY_EVENT_ANALYSIS,
  ARIA_DELETE_CONFIG,
  BUDGETS_LIMITS_PATH,
  BUDGETS_LIMITS_TAB_NAME,
  BUTTON_CANCEL,
  BUTTON_CREATE,
  BUTTON_DELETE,
  COST_LIMITS_HEADING,
  COUNT_LIMITS_HEADING,
  CREATE_DIALOG_TITLE,
  DELETE_DIALOG_TITLE,
  ERROR_NO_ENTITY,
  FIELD_TENANT,
  ICON_CHEVRON_DOWN,
  ICON_CHEVRON_UP,
  NO_ACTIVE_CONFIGS_TEXT,
  OTHER_ACCOUNTS_HEADING,
  PERIOD_HEADING_PREFIX,
  SCOPE_ACCOUNT,
  SELECT_ACCOUNT,
  SELECT_APPLY_TO,
  SELECT_SCOPE,
  TOGGLE_GROUP_NAME,
  TOGGLE_MODEL_PRICING,
  TOGGLE_USAGE_LIMITS,
} from "./usageLimitsConstants";

// Page object for Admin -> AI & Tools -> Budgets & Limits -> Usage & Limits,
// whose body is app/src/components/llm/LLMConsumptionTab.jsx mounted by
// app/src/components/llm/admin/BudgetsAndLimitsAdminTab.jsx.
//
// Locator note for the whole file:
//   grep -c 'data-testid' app/src/components/llm/LLMConsumptionTab.jsx -> 0
//   grep -c 'data-testid' app/src/components/llm/admin/BudgetsAndLimitsAdminTab.jsx -> 0
// so rung 1 is unavailable everywhere here and rung 2 is the highest rung that
// actually exists. What this surface does give us is real ARIA: ds/ToggleGroup
// renders role="group" + role="radio" with aria-checked, ds/Modal is a MUI
// Dialog (role="dialog"), ds/Select's trigger carries aria-haspopup="listbox",
// and the per-config icon buttons pass an explicit aria-label through ds/Button
// onto its ButtonBase. The exceptions are the limit rows and the config chips,
// which the component builds out of unlabelled MUI Boxes — those are reached by
// walking up from the one piece of text each of them does render, which is a
// labelled anchor rather than an index chain.
export class UsageLimitsLocators extends CommonLocators {
  // Admin page chrome.
  readonly aiToolsTab: Locator;
  readonly budgetsLimitsTab: Locator;

  // BudgetsAndLimitsAdminTab's own ToggleGroup.
  readonly toggleGroup: Locator;
  readonly usageLimitsToggle: Locator;
  readonly modelPricingToggle: Locator;

  // Usage & Limits body.
  readonly periodHeading: Locator;
  readonly addBudgetBtn: Locator;
  readonly activeBudgetsHeading: Locator;
  readonly activeBudgetsSection: Locator;
  readonly noActiveConfigsText: Locator;
  readonly otherAccountsHeader: Locator;
  readonly otherAccountsExpandedIcon: Locator;
  readonly otherAccountsCollapsedIcon: Locator;

  // Create/Edit dialog.
  readonly budgetDialog: Locator;
  readonly costLimitsHeading: Locator;
  readonly countLimitsHeading: Locator;
  readonly tenantField: Locator;
  readonly createBtn: Locator;
  readonly dialogCancelBtn: Locator;
  readonly noEntityError: Locator;

  // Delete confirmation dialog.
  readonly deleteDialog: Locator;
  readonly confirmDeleteBtn: Locator;

  // Whichever ds/Select popover is currently open.
  readonly openOptionList: Locator;

  readonly notificationsRegion: Locator;

  constructor(page: Page) {
    super(page);

    // AnchorComponent renders every top-level tab as an <a> carrying
    // `anchor-tab-${opt.id || opt.name}`, and the AI & Tools filter pins
    // id:'AITools', so the plain "#" form is safe here. Deliberately id-only
    // even though the link has an accessible name: the sidebar's own admin
    // flyout renders a second "AI & Tools" link, so a role+name match is
    // ambiguous and would resolve in document order to the wrong one.
    this.aiToolsTab = page.locator(`#${AI_TOOLS_TAB_ID}`);

    // The sub-tab strip is a MUI Tabs, so the sub-tab is a real role="tab" and
    // "Budgets & Limits" is unique among the page's tabs. No .or(): the only
    // rung underneath is ds/Tabs' `#tab-${index}`, and this strip's indices
    // shift with the feature flags that drop the Functions and Gateway
    // sub-tabs, so that fallback would silently select a different tab.
    this.budgetsLimitsTab = page.getByRole("tab", { name: BUDGETS_LIMITS_TAB_NAME, exact: true });

    // ds/ToggleGroup sets role="group" + aria-label on its container and
    // role="radio" + aria-checked on each option, so the two toggles are scoped
    // to their own group rather than matched page-wide. No .or() on any of the
    // three: BudgetsAndLimitsAdminTab passes the group no id and ds/ToggleGroup
    // renders no testid, so role is both the highest and the only rung here.
    this.toggleGroup = page.getByRole("group", { name: TOGGLE_GROUP_NAME });
    this.usageLimitsToggle = this.toggleGroup.getByRole("radio", { name: TOGGLE_USAGE_LIMITS, exact: true });
    this.modelPricingToggle = this.toggleGroup.getByRole("radio", { name: TOGGLE_MODEL_PRICING, exact: true });

    // The header is a bare Typography with no role, id or testid — its own copy
    // is the only handle, so there is nothing to fall back to, and the month
    // interpolated after the stem changes with the calendar, hence the prefix
    // regex. Its ancestors carry the same text, so .first() pins one element.
    this.periodHeading = page.getByText(PERIOD_HEADING_PREFIX).first();

    // ds/Button gives this one no id or testid, so role+name is the top rung and
    // "Add Budget" appears nowhere else on the page — no fallback to add.
    this.addBudgetBtn = page.getByRole("button", { name: ADD_BUDGET_BUTTON, exact: true });
    // Section headings are unroled Typography; their copy is the only handle.
    this.activeBudgetsHeading = page.getByText(ACTIVE_BUDGETS_HEADING, { exact: true }).first();
    // The configs area: the Box that wraps the heading, the Active Budgets rows
    // and the collapsed all-accounts section. Reached from the heading because
    // the component gives the Box no role, id or testid — an ancestor axis from
    // a labelled anchor, not a positional chain. Scoping matters for the tenant
    // name, which also appears in the page header's tenant switcher.
    this.activeBudgetsSection = this.activeBudgetsHeading.locator("xpath=ancestor::div[1]");
    this.noActiveConfigsText = page.getByText(NO_ACTIVE_CONFIGS_TEXT, { exact: true }).first();

    // The collapsible section's own header row is a Box given role="button" and
    // a tabIndex by the component, so role+name is genuinely available. Its
    // accessible name is its contents — the heading plus the count chip — hence
    // the substring regex. Deliberately no text .or(): the chevrons below have
    // to be scoped to this same row, and the heading Typography does not contain
    // them, so a fallback that matched it would break the open-state check.
    this.otherAccountsHeader = page.getByRole("button", { name: new RegExp(OTHER_ACCOUNTS_HEADING) }).first();

    // The component writes no aria-expanded; what it does swap is the chevron,
    // and MUI stamps every icon with data-testid="<Name>Icon" — so these are a
    // real rung-1 handle on the section's open state, scoped to its own header.
    this.otherAccountsExpandedIcon = this.otherAccountsHeader.getByTestId(ICON_CHEVRON_UP);
    this.otherAccountsCollapsedIcon = this.otherAccountsHeader.getByTestId(ICON_CHEVRON_DOWN);

    // ds/Modal portals to the body, so both dialogs are DOM siblings of the page
    // rather than descendants of it, and both stamp the same title id. Filtering
    // role="dialog" on the title copy is what separates them.
    this.budgetDialog = page.getByRole("dialog").filter({ hasText: CREATE_DIALOG_TITLE }).first();
    this.deleteDialog = page.getByRole("dialog").filter({ hasText: DELETE_DIALOG_TITLE }).first();

    this.costLimitsHeading = this.budgetDialog.getByText(COST_LIMITS_HEADING, { exact: true }).first();
    this.countLimitsHeading = this.budgetDialog.getByText(COUNT_LIMITS_HEADING, { exact: true }).first();

    // Every control below is a ds/Input or ds/Button that BudgetEditModal passes
    // no id and no testid, and ds/Input derives its DOM id from React.useId() —
    // regenerated per render and unselectable. So role+name is both the highest
    // and the only rung available, and each one is scoped to its own dialog,
    // which is what keeps the two dialogs' identically named buttons apart.
    this.tenantField = this.budgetDialog.getByRole("textbox", { name: FIELD_TENANT, exact: true });
    this.createBtn = this.budgetDialog.getByRole("button", { name: BUTTON_CREATE, exact: true });
    this.dialogCancelBtn = this.budgetDialog.getByRole("button", { name: BUTTON_CANCEL, exact: true });
    this.confirmDeleteBtn = this.deleteDialog.getByRole("button", { name: BUTTON_DELETE, exact: true });

    // MUI's Alert renders role="alert"; scoped to the dialog and filtered on the
    // guard's own copy so the toast region cannot satisfy it.
    this.noEntityError = this.budgetDialog
      .getByRole("alert")
      .filter({ hasText: ERROR_NO_ENTITY })
      .or(this.budgetDialog.getByText(ERROR_NO_ENTITY, { exact: true }))
      .first();

    // ds/Select's popover is a MUI Menu whose list carries role="listbox". Only
    // one can be open at a time, and :visible is what keeps a closed, still
    // mounted popover from matching.
    this.openOptionList = page.locator('[role="listbox"]:visible').first();

    // SnackbarComponent mounts at the app root. While a ds/Modal is open MUI
    // marks the rest of the app aria-hidden, which removes the toast from the
    // accessibility tree — so it is matched by attribute, which is DOM-based,
    // exactly as tests/nubi/model-pricing does.
    this.notificationsRegion = page.locator('[aria-label="Notifications"]');
  }

  // Opens Admin -> AI & Tools with the Budgets & Limits sub-tab selected, via
  // the hash route the page resolves on mount.
  async open(): Promise<void> {
    // These specs navigate straight in on the stored auth state rather than
    // through doFullLogin(), so the first-login tour is not yet handled on this
    // page and would intercept clicks. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // domcontentloaded, not the default "load": the dashboard's subresources keep
    // the load event pending well past the point the tab is usable on dev.
    await this.page.goto(BUDGETS_LIMITS_PATH, { waitUntil: "domcontentloaded" });
    await this.waitForUsageBody();
  }

  // The readiness signal for the tab body. LLMConsumptionTab renders a bare
  // <Loader/> until getBudgetStatus resolves, so the toggle plus the period
  // header being painted means the first fetch has landed.
  async waitForUsageBody(): Promise<void> {
    await this.usageLimitsToggle.waitFor({ state: "visible", timeout: 120000 });
    await expect(this.usageLimitsToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
    await this.periodHeading.waitFor({ state: "visible", timeout: 90000 });
  }

  // One limit row of the create/edit dialog: the flex Box that holds the row's
  // Switch and its amount Input. The Switch's label is a Typography element
  // rather than a string, so ds/Switch leaves the checkbox with no accessible
  // name (Switch.tsx only forwards a string label as aria-label) — that text is
  // still the row's one stable handle, so the row is the nearest ancestor of it
  // that also contains the amount field. An ancestor axis from a labelled
  // anchor, not a positional chain.
  limitRow(label: string): Locator {
    return this.budgetDialog
      .getByText(label, { exact: true })
      .first()
      .locator('xpath=ancestor::div[.//input[@type="number"]][1]');
  }

  // MUI's Switch renders its control as <input type="checkbox">, so role and
  // attribute forms name the same element inside the row.
  limitToggle(label: string): Locator {
    return this.limitRow(label).getByRole("checkbox").or(this.limitRow(label).locator('input[type="checkbox"]')).first();
  }

  // ds/Input with type='number' renders <input type="number">, which is
  // role="spinbutton". Both branches stay inside the same row.
  limitInput(label: string): Locator {
    return this.limitRow(label).getByRole("spinbutton").or(this.limitRow(label).locator('input[type="number"]')).first();
  }

  // A ds/Select trigger inside the dialog. The component renders it as
  // <button id={useId()} aria-haspopup="listbox"> under a <label htmlFor> —
  // the id is regenerated per render and unusable, and a <label> does not
  // reliably name a <button> in the accessibility tree, so the name match is
  // narrowed with .and() to the element that actually carries the popup
  // attribute and the fallback walks from the same label to its sibling
  // trigger. Same shape tests/ApplicationGroup already relies on.
  selectTrigger(label: string): Locator {
    const trigger = this.budgetDialog.locator('button[aria-haspopup="listbox"]');
    return this.budgetDialog
      .getByRole("button", { name: label })
      .and(trigger)
      .or(this.budgetDialog.getByText(label, { exact: true }).locator('xpath=following-sibling::button[@aria-haspopup="listbox"][1]'))
      .first();
  }

  scopeSelect(): Locator {
    return this.selectTrigger(SELECT_SCOPE);
  }

  accountSelect(): Locator {
    return this.selectTrigger(SELECT_ACCOUNT);
  }

  applyToSelect(): Locator {
    return this.selectTrigger(SELECT_APPLY_TO);
  }

  // One option inside the open popover. Anchored on both ends because account
  // labels share prefixes — a substring match would pick the wrong account. No
  // .or(): getByRole("option", { name }) matches zero against these MUI list
  // rows, the same nested-span reason menu items fail on in this app.
  option(label: string): Locator {
    return this.openOptionList
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  // Opens a ds/Select and commits one option, waiting on the trigger's own text
  // rather than on a fixed pause — the trigger renders the selected label, so
  // that is the signal the selection landed.
  async chooseOption(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.openOptionList.waitFor({ state: "visible", timeout: 20000 });
    await this.option(label).click();
    await expect(trigger).toContainText(label, { timeout: 20000 });
  }

  // The Account select is grouped by cloud provider, and ds/Select collapses
  // every group until it is clicked (Select.tsx only auto-expands while a search
  // is active). A tenant whose accounts share one provider renders the flat list
  // instead, where there is nothing to expand — hence the loop over whatever
  // headers exist rather than an assumption either way.
  async revealAccountOptions(): Promise<void> {
    await this.openOptionList.waitFor({ state: "visible", timeout: 20000 });
    const headers = this.openOptionList.locator('[role="button"]');
    const headerCount = await headers.count();
    for (let i = 0; i < headerCount; i += 1) {
      await headers.nth(i).click();
    }
    await this.openOptionList.locator('[role="option"]').first().waitFor({ state: "visible", timeout: 20000 });
  }

  // Every account label the Account select currently offers, read with the
  // popover open. Used to walk candidates until one has no Event Analysis
  // budget yet.
  async accountOptionLabels(): Promise<string[]> {
    await this.accountSelect().click();
    await this.revealAccountOptions();
    const labels = await this.openOptionList.locator('[role="option"]').allInnerTexts();
    await this.page.keyboard.press("Escape");
    await this.openOptionList.waitFor({ state: "hidden", timeout: 20000 });
    return labels.map((label) => label.trim()).filter((label) => label.length > 0);
  }

  // Commits one account, expanding the provider groups first when the select is
  // in its grouped shape.
  async chooseAccount(label: string): Promise<void> {
    await this.accountSelect().click();
    await this.revealAccountOptions();
    await this.option(label).click();
    await expect(this.accountSelect()).toContainText(label, { timeout: 20000 });
  }

  // Whether the all-accounts section exists at all. It is only rendered while at
  // least one account-scoped config does, so its absence is a normal outcome —
  // and the one that means there is nothing in there to look at.
  async otherAccountsSectionPresent(timeout = 20000): Promise<boolean> {
    return this.otherAccountsHeader
      .waitFor({ state: "visible", timeout })
      .then(() => true)
      .catch(() => false);
  }

  // Opens "Budgets for All Accounts". The header is a plain toggle rather than
  // an open button, so the chevron is read first: clicking unconditionally would
  // close the section on the paths that arrive with it already open (creating a
  // budget sets configsOpen, a reload does not).
  async expandOtherAccounts(): Promise<void> {
    await this.otherAccountsHeader.waitFor({ state: "visible", timeout: 60000 });
    if (await this.otherAccountsCollapsedIcon.isVisible()) {
      await this.otherAccountsHeader.click();
    }
    await expect(this.otherAccountsExpandedIcon).toBeVisible({ timeout: 30000 });
  }

  // The scope block for one account inside the configs list: the nearest
  // ancestor of the account's own heading that also holds a per-config control.
  // The heading text is the account label the Account select offers, because
  // both are built from `${account_name} (${cloud_provider})`. ActiveConfigsCompact
  // gives the block no role, id or testid, so this walk is the only handle and
  // there is no wider match to fall back to.
  accountScopeRow(accountLabel: string): Locator {
    return this.page
      .getByText(accountLabel, { exact: true })
      .first()
      .locator(`xpath=ancestor::div[.//button[@aria-label="${ARIA_DELETE_CONFIG}"]][1]`);
  }

  // One module's chip inside that scope block. A chip that has a config carries
  // the edit/delete pair; the dashed placeholder for a module with no config
  // does not, which is why this resolves only for a configured module.
  moduleChip(accountLabel: string, moduleLabel: string): Locator {
    return this.accountScopeRow(accountLabel)
      .getByText(moduleLabel, { exact: true })
      .first()
      .locator(`xpath=ancestor::div[.//button[@aria-label="${ARIA_DELETE_CONFIG}"]][1]`);
  }

  deleteChipBtn(accountLabel: string, moduleLabel: string): Locator {
    return this.moduleChip(accountLabel, moduleLabel).getByRole("button", { name: ARIA_DELETE_CONFIG });
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

  // Opens the create dialog from the tab header. In the Admin mount
  // LLMConsumptionTab is tenant-wide, so openManage() never raises the
  // "Existing budgets found" nudge and this lands straight on the form.
  async openCreateDialog(): Promise<void> {
    await this.addBudgetBtn.click();
    await this.budgetDialog.waitFor({ state: "visible", timeout: 30000 });
    await this.costLimitsHeading.waitFor({ state: "visible", timeout: 30000 });
  }

  // Narrows the form to a single account-scoped Event Analysis budget. Order
  // matters: switching the scope clears the picked entity and every limit
  // field, so the account is chosen last.
  async selectAccountScope(accountLabel: string): Promise<void> {
    await this.chooseOption(this.scopeSelect(), SCOPE_ACCOUNT);
    await this.chooseOption(this.applyToSelect(), APPLY_EVENT_ANALYSIS);
    await this.chooseAccount(accountLabel);
  }
}
