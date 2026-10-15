import { Page, Locator, expect } from "@playwright/test";

export class NubiLocators {
  readonly askNudgebeeBtn: Locator;
  readonly newChatBtn: Locator;
  readonly chatTextbox: Locator;
  readonly submitBtn: Locator;
  // SettingsModal is deleted (docs/ia-consolidation-plan.md, PR 3/Decision AA).
  // Its content now lives behind "AI & Tools" (admin-gated — every tab-name
  // locator below that used to open through "More", e.g. customAgentTab/
  // ToolButton, opens through this one now). A non-admin-gated
  // "Memory" button was tried as a fix for tenants without b-Cortex losing
  // access to Memory/Account Context, then reverted (Decision AB) — that
  // narrowing is an accepted trade-off. The rail is just these two buttons;
  // neither is unconditionally present ("AI & Tools" is admin-gated, "b-Cortex"
  // is hidden in OSS mode), so openPanel()'s own "did it open" check uses
  // chatTextbox instead.
  readonly aiToolsBtn: Locator;
  readonly bcortexBtn: Locator;
  readonly aiToolsDialog: Locator;
  readonly bcortexDialog: Locator;
  // Create Custom Agent Locators
  readonly customAgentTab: Locator;
  readonly searchAgentInput: Locator;
  readonly createCustomAgentBtn: Locator;
  // The agent form is one scrolling page of always-expanded FormCards
  // (llm/CreateAgentNew.jsx). The "Agent Identity" / "Behavior & Guidelines" /
  // "Tool/Agent Selection" / "Knowledge & Examples" controls in the left rail are
  // VerticalStepNavigation buttons whose only job is scrollIntoView — nothing is
  // gated behind them, so this suite reaches the fields directly and lets
  // Playwright scroll them into view.
  readonly agentNameInput: Locator;
  readonly agentDescriptionInput: Locator;
  readonly agenRole: Locator;
  readonly agentInstructionsInput: Locator;
  readonly selectAgentOrTool: Locator;
  readonly listOfAgentsOrTools: Locator;
  readonly agentToolUsage: Locator;
  readonly submitCreateAgentBtn: Locator;
  // create custom tool locators
  readonly ToolButton: Locator;
  readonly CreateToolButton: Locator;
  readonly ToolName: Locator;
  readonly ToolDescription: Locator;
  readonly ToolTypeRunbook: Locator;
  readonly ToolTypeMCP: Locator;
  readonly ToolTypeContainer: Locator;
  readonly RunbookAction: Locator;
  readonly RunbookAction1: Locator;
  readonly HTTPurl: Locator;
  readonly SubmitButton: Locator;
  readonly searchToolInput: Locator;
  readonly ContainerImage: Locator;
  readonly ContainerCommand: Locator;
  readonly editToolBtn: Locator;
  readonly updateToolBtn: Locator;
  readonly updateToolSuccessMessage: Locator;
  readonly toolStatusSelect: Locator;
  readonly toolStatusDisabledOption: Locator;
  readonly toolDisabledSuccessMessage: Locator;

  // Functions tab locators (LLM Functions)
  readonly knowledgeGroupTab: Locator;
  readonly functionsTab: Locator;
  readonly createFunctionBtn: Locator;
  readonly searchFunctionInput: Locator;
  readonly functionNameInput: Locator;
  readonly functionStatusSelect: Locator;
  readonly functionStatusDraftOption: Locator;
  readonly functionStatusActiveOption: Locator;
  readonly functionCancelBtn: Locator;
  readonly viewAgentListBtn: Locator;
  readonly agentListModalTitle: Locator;
  readonly agentListSearchInput: Locator;
  readonly rewritePromptBtn: Locator;
  readonly rewritePromptEmptyError: Locator;
  readonly variableDefaultsHeading: Locator;
  readonly functionDescriptionInput: Locator;
  readonly functionPromptInput: Locator;
  readonly saveFunctionBtn: Locator;
  readonly updateFunctionBtn: Locator;
  readonly functionMoreActionsBtn: Locator;
  readonly editFunctionMenuItem: Locator;
  readonly deleteFunctionMenuItem: Locator;
  readonly confirmDeleteFunctionBtn: Locator;
  readonly functionCreatedMessage: Locator;
  readonly functionCreationFailureMessage: Locator;
  readonly functionUpdatedMessage: Locator;
  readonly functionDeletedMessage: Locator;

  // Agent CRUD action locators
  readonly agentMoreActionsBtn: Locator;
  readonly editAgentMenuItem: Locator;
  readonly deleteAgentMenuItem: Locator;
  readonly updateAgentBtn: Locator;
  readonly confirmDeleteAgentBtn: Locator;
  readonly updateAgentSuccessMessage: Locator;
  readonly deleteAgentSuccessMessage: Locator;

  // Success and Failure Messages Locators
  readonly successMessage: Locator;
  readonly failureMessage: Locator;
  readonly toolCreatedMessage: Locator;
  readonly toolCreationFailureMessage: Locator;

  readonly page: Page;

  constructor(page: Page) {
    this.page = page;
    // Main-rail Nubi item renders on every page; the header "Ask nubi" button is absent on /ask-nudgebee, the landing page.
    this.askNudgebeeBtn = page
      .locator("#nubi-sidenavbutton")
      .or(page.locator("#global-search-trigger-ask-ai"))
      .or(page.getByRole("button", { name: "Ask nubi" }))
      .first();
    this.newChatBtn = page.locator('img[src*="plus-icon"]');
    // The same chat renders with two different placeholders: the long one on the
    // full /ask-nudgebee page, and "Ask a question..." when Nubi is opened as the
    // floating panel (KubernetesLLMResponseGeneratorV2 keys it off `popup`).
    // Match either, so a caller does not have to know which mode it landed in.
    this.chatTextbox = page
      .getByPlaceholder("Ask about troubleshooting, logs or optimizations")
      .or(page.getByPlaceholder("Ask a question..."))
      .first();
    this.submitBtn = page.locator('#set-config-btn')
    // Create Custom Agent Locators
    this.aiToolsBtn = page.getByTestId('nav-nubi-ai-tools-btn').or(page.getByRole('button', { name: 'AI & Tools', exact: true }));
    this.bcortexBtn = page.getByTestId('nav-bcortex-btn').or(page.getByRole('button', { name: 'b-Cortex', exact: true }));
    // Each modal identified by its own ds/Modal title. Matched by attribute
    // rather than getByRole("dialog"): MUI aria-hides a dialog the moment a
    // nested overlay (an open ds/Select panel, the pattern dialog, the Soul
    // expand editor) is up, and a role lookup would then read "the modal
    // closed" while it is still perfectly on screen.
    this.aiToolsDialog = page.locator('[role="dialog"]').filter({ hasText: 'AI & Tools' }).first();
    this.bcortexDialog = page.locator('[role="dialog"]').filter({ hasText: 'b-Cortex' }).first();
    this.createCustomAgentBtn = page.getByRole("button", { name: "Create Custom Agent" });
    this.customAgentTab = page.getByRole("tab", { name: /agents/i });
    this.searchAgentInput = page.getByPlaceholder('Search Agent')
    this.agentNameInput = page.getByRole("textbox", { name: "Agent Name" });
    this.agentDescriptionInput = page.getByRole("textbox", { name: 'Describe what this agent does' });
    this.agenRole = page.getByRole('textbox', { name: 'You are a [role], responsible' })
    this.agentInstructionsInput = page.getByRole('textbox', { name: 'Key responsibilities: 1. [' })
    this.selectAgentOrTool = page.getByRole("button", { name: "Select Tool/Agent" });
    this.listOfAgentsOrTools = page.getByText('anomaly_execute - system')
    
    this.agentToolUsage = page.getByRole('textbox', { name: 'Tool: [Tool Name] Purpose: [' })
    this.submitCreateAgentBtn = page.getByRole("button", { name: "Create Agent" });

    // create custom tool locators
    // "Tools" is now a ToggleGroup option (role=radio) nested inside the
    // "Tools & MCP" top-level tab (ToolsAndMCPAdminTab.jsx), not a top-level
    // tab itself — but that inner toggle already defaults to 'tools', so
    // clicking the top-level tab alone lands on the same view "Tools" used to.
    this.ToolButton = page.getByRole('tab', { name: 'Tools & MCP' });
    this.CreateToolButton = page.locator('#create-tool');
    this.ToolName = page.getByPlaceholder('Enter tool name');
    this.ToolDescription = page.getByPlaceholder('Describe what this tool does');
    this.ToolTypeRunbook = page.getByRole('radio', { name: 'Runbook Action' })
    this.ToolTypeMCP = page.getByRole('radio', { name: 'MCP HTTP' })
    this.ToolTypeContainer = page.getByRole('radio', { name: 'Container' })
    this.RunbookAction = page.locator('#auto-complete-runbook-action');
    this.RunbookAction1 = page.getByRole('option', { name: 'Create Ticket' });
    this.SubmitButton = page.getByRole("button", { name: "Submit" });
    this.searchToolInput = page.getByPlaceholder('Search Tool');
    this.HTTPurl = page.getByRole('textbox', { name: 'Enter MCP server URL' });
    this.ContainerImage = page.getByPlaceholder('e.g., alpine:latest or myrepo/myimage:tag');
    this.ContainerCommand = page.getByPlaceholder('e.g., /bin/sh or printenv (overrides image ENTRYPOINT)');
    this.editToolBtn = page.getByRole('button', { name: 'Edit tool' });
    this.updateToolBtn = page.getByRole('button', { name: 'Update' });
    this.updateToolSuccessMessage = page.getByText('Tool updated successfully');
    this.toolStatusSelect = page.getByLabel('Status');
    this.toolStatusDisabledOption = page.getByText('Disabled', { exact: true });
    this.toolDisabledSuccessMessage = page.getByText('Tool updated successfully');


    // Functions tab locators (LLM Functions) - b-Cortex > Knowledge > My Functions,
    // the only Functions mount since Admin > AI & Tools dropped its Functions tab.
    // exact:true on the group tab: "Knowledge" is also the prefix of the sibling
    // "Knowledge Graph" group tab. NOT exact on My Functions: its icon is a plain
    // require()d .svg, which Tabs.jsx renders as <SafeIcon alt={text}>, so the
    // image alt joins the tab's accessible name. The fallback is the id Tabs
    // stamps from the option's value (`tab-${value}`), which BCortexModal sets
    // to the sub-tab id, so it does not shift when a flag drops a sibling.
    this.knowledgeGroupTab = page.getByRole('tab', { name: 'Knowledge', exact: true });
    this.functionsTab = page.getByRole('tab', { name: 'My Functions' }).or(page.locator('#tab-my-functions')).first();
    this.createFunctionBtn = page.locator('#create-function');
    this.searchFunctionInput = page.getByPlaceholder('Search Function');
    this.functionNameInput = page.getByPlaceholder('e.g., get_user_data, process_payment');
    this.functionStatusSelect = page.getByRole('button', { name: 'Status', exact: true }).or(page.locator('#function-status')).first();
    this.functionStatusDraftOption = page.getByText('Draft', { exact: true });
    this.functionStatusActiveOption = page.getByText('Active', { exact: true });
    this.functionCancelBtn = page.getByRole('button', { name: 'Cancel' });
    this.viewAgentListBtn = page.getByRole('button', { name: 'View Agent List' });
    this.agentListModalTitle = page.getByText('Available Agents');
    this.agentListSearchInput = page.getByPlaceholder('Search agents...');
    this.rewritePromptBtn = page.getByRole('button', { name: 'Rewrite Prompt' });
    this.rewritePromptEmptyError = page.getByText('Please enter a prompt to validate');
    this.variableDefaultsHeading = page.getByText('Variable Default Values');
    this.functionDescriptionInput = page.getByPlaceholder('What is this function supposed to do?');
    this.functionPromptInput = page.getByPlaceholder('Write your prompt here...');
    this.saveFunctionBtn = page.getByRole('button', { name: 'Save Function' });
    this.updateFunctionBtn = page.getByRole('button', { name: 'Update Function' });
    this.functionMoreActionsBtn = page.getByRole('button', { name: 'More actions' });
    this.editFunctionMenuItem = page
      .locator('[role="menuitem"]#edit:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: 'Edit Function' }))
      .first();
    this.deleteFunctionMenuItem = page
      .locator('[role="menuitem"]#delete:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: 'Delete Function' }))
      .first();
    this.confirmDeleteFunctionBtn = page.getByRole('button', { name: 'Delete', exact: true });
    this.functionCreatedMessage = page.getByText('Function created successfully');
    this.functionCreationFailureMessage = page.getByText('A function with this name already exists');
    this.functionUpdatedMessage = page.getByText('Function updated successfully');
    this.functionDeletedMessage = page.getByText(/deleted successfully/);

    // Agent CRUD action locators
    this.agentMoreActionsBtn = page.getByRole('button', { name: 'More actions' });
    this.editAgentMenuItem = page
      .locator('[role="menuitem"]#edit:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: 'Edit Agent' }))
      .first();
    this.deleteAgentMenuItem = page
      .locator('[role="menuitem"]#delete:visible')
      .or(page.locator('[role="menuitem"]:visible', { hasText: 'Delete Agent' }))
      .first();
    this.updateAgentBtn = page.getByRole('button', { name: 'Update Agent' });
    this.confirmDeleteAgentBtn = page.getByRole('button', { name: 'Delete' });
    this.updateAgentSuccessMessage = page.getByText('Agent updated successfully');
    this.deleteAgentSuccessMessage = page.getByText(/deleted successfully/);

    // Success and Failure Messages
    this.successMessage = page.getByText('Agent created successfully');
    this.failureMessage = page.getByText('Please fill the following fields: - Agent name already exists');
    this.toolCreatedMessage = page.getByText('Tool created successfully');
    this.toolCreationFailureMessage = page.getByText('Failed to create tool');
  }

  /**
   * Closes an open ds/Select popover and waits until it is gone.
   *
   * A multiple Select stays open after a pick, and while it is up its invisible
   * MUI backdrop covers the page and MUI marks everything behind it aria-hidden —
   * so the next click lands on the backdrop and role-based lookups find nothing.
   */
  async closeSelectPopover(timeout = 5000): Promise<void> {
    const popover = this.page.locator(".MuiPopover-root").last();
    if (!(await popover.isVisible().catch(() => false))) return;

    // Escape pressed on the picker's OWN search input - never page-level. MUI closes the
    // topmost modal only, and while the popover is up that is the popover, so the dialog
    // underneath is untouched. A page-level Escape is the opposite: once the popover has
    // gone it reaches that dialog and dismisses the form being filled in.
    const search = popover.locator("input").first();
    if (await search.isVisible().catch(() => false)) {
      await search.press("Escape").catch(() => {});
    }

    if (await popover.isVisible().catch(() => false)) {
      // The invisible backdrop spans the viewport, but its CENTRE sits behind the options
      // panel, so Playwright's default centre-point click is intercepted and times out.
      // A corner lands on backdrop that nothing covers.
      await popover
        .locator(".MuiBackdrop-root")
        .first()
        .click({ position: { x: 5, y: 5 }, timeout })
        .catch(() => {});
    }

    await popover.waitFor({ state: "detached", timeout }).catch(() => {});
  }

  // Admin → AI & Tools' Agents/Tools & MCP sub-tabs each mount their own
  // AdminAccountFilter (AdminAccountFilter.jsx), defaulting to tenant-wide
  // (accountId=''). ListAgents/ListTools both hide their Create button
  // entirely at tenant-wide (`!isTenantWide && hasWriteAccess(...)`) —
  // the old Settings mount always carried a real accountId from the panel, so
  // this never came up there. Picks the first real account so Create becomes
  // reachable, same as an admin narrowing the filter by hand.
  //
  // AccountSelect (ds/Select, `grouped`) groups by cloud provider only when
  // there are 2+ groups — then every group renders collapsed behind a
  // group-header button, and expanding those headers in order is what makes an
  // option clickable regardless of which provider group holds the first real
  // account. With a single provider the list is flat: no headers, options
  // visible straight away.
  //
  // Every locator below is matched by ARIA attribute rather than through
  // getByRole. ds/Select renders its panel as a nested MUI Menu with
  // disablePortal, so while it is up MUI's ModalManager marks the AI & Tools
  // dialog that contains it aria-hidden - the whole dialog, panel included,
  // drops out of the accessibility tree and every role lookup inside it
  // resolves to nothing. Same trap soulLocators.ts already documents for the
  // expand editor; the attribute selectors see the DOM directly.
  async selectFirstAdminAccount(timeout = 30000): Promise<void> {
    await this.selectAdminAccount(undefined, timeout);
  }

  // The same picker, for a caller that needs one named account rather than whichever real one
  // comes first. The Nubi chat reads its own account from the global cluster selection, so a
  // function authored under any other account is simply absent from its /call list.
  async selectAdminAccount(accountName?: string, timeout = 30000): Promise<void> {
    const trigger = this.page.locator("#account-select");
    await trigger.click();
    const listbox = this.page.locator('[role="listbox"]');
    await listbox.waitFor({ state: "visible", timeout });

    // AccountSelect fetches its accounts on mount, and the panel opens on
    // whatever that fetch has got to: while it is in flight ds/Select renders
    // skeleton rows and NO group headers at all. Expanding the headers in a
    // single pass therefore ran against an empty panel and left every group
    // collapsed for good, so the option wait below could only time out. The
    // headers are what has to be waited for, not the panel.
    const groupHeaders = listbox.locator('[role="button"]');
    const options = listbox.locator('[role="option"]');
    await expect
      .poll(async () => (await groupHeaders.count()) + (await options.count()), {
        timeout,
        message: "the account picker never listed a group or an account",
      })
      .toBeGreaterThan(0);

    const groupCount = await groupHeaders.count();
    // Exact match, not hasText substring: AccountSelect lists sibling accounts that share a
    // name prefix (e.g. "k8s-dev" and "k8s-dev-oss"), and a substring match picks whichever
    // sorts first regardless of which one the caller asked for. That silently created a
    // function under the wrong account in the "Usage in Nubi" tests — CHAT_ACCOUNT ("k8s-dev")
    // resolved to a different accountId than the one the /call suggestions were read from,
    // so the created function could never appear there no matter how long the test waited.
    const escapeRegExp = (value: string) => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const realAccountOption = accountName
      ? listbox.locator('[role="option"]').filter({ hasText: new RegExp(`^${escapeRegExp(accountName)}$`) }).first()
      : listbox.locator('[role="option"]').filter({ hasNotText: "All accounts" }).first();
    for (let i = 0; i < groupCount; i++) {
      // Each header TOGGLES its group, so an already-expanded one would close
      // again — stop as soon as a real account is reachable.
      if (await realAccountOption.isVisible().catch(() => false)) break;
      await groupHeaders.nth(i).click();
      // Short wait, not the method timeout: "this group holds no real account"
      // is a normal branch here, so the loop has to move on quickly — but
      // reading visibility straight after the click can beat the re-render and
      // expand a group nothing needed.
      await realAccountOption.waitFor({ state: "visible", timeout: 2000 }).catch(() => {});
    }

    await realAccountOption.waitFor({ state: "visible", timeout });
    await realAccountOption.click();
    await listbox.waitFor({ state: "detached", timeout }).catch(() => {});
  }

  // Clicks the Nubi icon and retries up to 3 times if the panel does not open.
  // Uses a generous click timeout because on the /home page the icon navigates
  // to /ask-nudgebee (slow on dev env) before the panel settles.
  //
  // chatTextbox is the "did it open" signal because neither rail button is a
  // reliable universal one any more: "AI & Tools" only renders for an admin
  // user, and "Memory" only renders for a tenant without b-Cortex (Decision AA).
  // chatTextbox has no such condition — it is the panel's own chat input.
  async openPanel(): Promise<void> {
    await this.askNudgebeeBtn.waitFor({ state: "visible", timeout: 30000 });
    for (let attempt = 1; attempt <= 3; attempt++) {
      await this.askNudgebeeBtn.click({ timeout: 30000 });
      const opened = await this.chatTextbox
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        .catch(() => false);
      if (opened) return;
      if (attempt === 3) throw new Error("Nubi panel did not open after 3 click attempts");
    }
  }

  // Opens one of the two rail modals and waits until the tab it must land on is
  // on screen. Both modals build their tab strip behind an async round trip
  // (AIToolsModal: useBCortexEnabled; BCortexModal: useBCortexEnabled and
  // hasFeatureAccess('LLM_FUNCTION')), and a rail click landing while the panel is still
  // animating in opens nothing at all - so the click and the wait are retried
  // as a pair rather than the click being fired once.
  //
  // The retry re-clicks only while the modal is NOT up. Retrying on "the tab is
  // not visible yet" instead deadlocks the moment the modal opens but its strip
  // is slow: the modal's own backdrop now covers the rail button, so the next
  // click waits out its action timeout, and the pair can never pass inside
  // toPass' budget. That is exactly how CreateCustomAgent.spec.ts failed on CI
  // while passing locally.
  //
  // The dialog is matched by attribute, not getByRole: an open ds/Select panel
  // inside the modal makes MUI aria-hide the dialog, and a role lookup would
  // then read "the modal closed". Deliberately takes the tab to wait for -
  // "the modal is up" and "the tab this suite needs is up" are the same wait,
  // and every caller has to make it.
  private async openRailModal(dialog: Locator, railBtn: Locator, tab: Locator, timeout = 60000): Promise<void> {
    await expect(async () => {
      // Probe: "the modal is not up yet" is the normal state on the first pass
      // and must come back as false rather than throw, since that is what
      // drives the retry.
      if (!(await dialog.isVisible().catch(() => false))) {
        await railBtn.click();
      }
      await tab.waitFor({ state: "visible", timeout: 15000 });
    }).toPass({ timeout, intervals: [1000, 2000, 3000] });
  }

  // Admin -> AI & Tools, the rail button that replaced Settings
  // (docs/ia-consolidation-plan.md, PR 3). Admin-gated and rendered on the
  // Ask-Nubi rail only, so openPanel() has to have run first.
  async openAITools(tab: Locator, timeout = 60000): Promise<void> {
    await this.openRailModal(this.aiToolsDialog, this.aiToolsBtn, tab, timeout);
  }

  // One entry in the chat's /call suggestion popup. TextAreaV2 renders that popup
  // as a bare MUI Popper of unlabelled Boxes - no testid, role or id anywhere
  // inside it - so the function's own name is the only handle it offers.
  // No .or(): every wider match would resolve against the chat transcript behind
  // the popup rather than the suggestion itself.
  callSuggestion(functionName: string): Locator {
    return this.page.getByText(functionName, { exact: true }).first();
  }

  functionRowsMatching(text: string): Locator {
    return this.page.getByRole("row").filter({ hasText: text });
  }

  functionRowMoreActionsBtn(row: Locator): Locator {
    return row.getByRole("button", { name: "More actions" });
  }

  // b-Cortex, which took Soul, Privacy, Preferences, Feedback, My Usage and
  // My Functions out of Settings (docs/ia-consolidation-plan.md, PR 4).
  async openBCortex(tab: Locator, timeout = 60000): Promise<void> {
    await this.openRailModal(this.bcortexDialog, this.bcortexBtn, tab, timeout);
  }
}
