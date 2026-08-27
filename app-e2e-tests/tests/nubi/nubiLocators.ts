import { Page, Locator } from "@playwright/test";

export class NubiLocators {
  readonly askNudgebeeBtn: Locator;
  readonly newChatBtn: Locator;
  readonly chatTextbox: Locator;
  readonly submitBtn: Locator;
  readonly settingsBtn: Locator;
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
  readonly ContainerArguments: Locator;
  readonly editToolBtn: Locator;
  readonly updateToolBtn: Locator;
  readonly updateToolSuccessMessage: Locator;
  readonly toolStatusSelect: Locator;
  readonly toolStatusDisabledOption: Locator;
  readonly toolDisabledSuccessMessage: Locator;

  // Functions tab locators (LLM Functions)
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
    this.askNudgebeeBtn = page
      .getByRole("button", { name: "Ask nubi" })
      .or(page.locator('img[alt="Ask nubi"]'));
    this.newChatBtn = page.locator('img[src*="plus-icon"]');
    // The same chat renders with two different placeholders: the long one on the
    // full /ask-nudgebee page, and "Ask a question..." when Nubi is opened as the
    // floating panel (KubernetesLLMResponseGeneratorV2 keys it off `popup`).
    // Match either, so a caller does not have to know which mode it landed in.
    this.chatTextbox = page
      .getByPlaceholder("Ask me about troubleshooting, error logs, resource usage, or optimizations.")
      .or(page.getByPlaceholder("Ask a question..."))
      .first();
    this.submitBtn = page.locator('#set-config-btn')
    // Create Custom Agent Locators
    this.settingsBtn = page.getByRole('button', { name: 'Settings', exact: true });
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
    this.ToolButton = page.getByRole('tab', { name: 'Tools' });
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
    this.ContainerArguments = page.getByPlaceholder('e.g., -c "echo hello" or --verbose');
    this.editToolBtn = page.getByRole('button', { name: 'Edit tool' });
    this.updateToolBtn = page.getByRole('button', { name: 'Update' });
    this.updateToolSuccessMessage = page.getByText('Tool updated successfully');
    this.toolStatusSelect = page.getByLabel('Status');
    this.toolStatusDisabledOption = page.getByText('Disabled', { exact: true });
    this.toolDisabledSuccessMessage = page.getByText('Tool updated successfully');


    // Functions tab locators (LLM Functions)
    this.functionsTab = page.getByRole('tab', { name: 'Functions' });
    this.createFunctionBtn = page.locator('#create-function');
    this.searchFunctionInput = page.getByPlaceholder('Search Function');
    this.functionNameInput = page.getByPlaceholder('e.g., get_user_data, process_payment');
    this.functionStatusSelect = page.getByLabel('Status');
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

  // Clicks the Nubi icon and retries up to 3 times if the panel does not open.
  // Uses a generous click timeout because on the /home page the icon navigates
  // to /ask-nudgebee (slow on dev env) before the panel settles.
  async openPanel(): Promise<void> {
    await this.askNudgebeeBtn.waitFor({ state: "visible", timeout: 30000 });
    for (let attempt = 1; attempt <= 3; attempt++) {
      await this.askNudgebeeBtn.click({ timeout: 30000 });
      const opened = await this.settingsBtn
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        .catch(() => false);
      if (opened) return;
      if (attempt === 3) throw new Error("Nubi panel did not open after 3 click attempts");
    }
  }
}