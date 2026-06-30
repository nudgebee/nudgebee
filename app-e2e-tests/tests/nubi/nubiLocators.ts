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
  readonly ageentIdentityButton: Locator;  //1
  readonly agentNameInput: Locator;
  readonly agentDescriptionInput: Locator;
  readonly agentSetAgentBehaviorAndGuidelines: Locator;   //2
  readonly agenRole: Locator;
  readonly agentInstructionsInput: Locator;
  readonly ageentToolsOrAgentselectionButton: Locator;   //3
  readonly selectAgentOrTool: Locator;
  readonly listOfAgentsOrTools: Locator;
  readonly agentToolUsage: Locator;
  readonly agentKnoowledgeAndExample: Locator;         //4
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
    this.askNudgebeeBtn = page.locator('img[alt="Ask nubi"]');
    this.newChatBtn = page.locator('img[src*="plus-icon"]');
    this.chatTextbox = page.getByPlaceholder(
      "Ask me about troubleshooting, error logs, resource usage, or optimizations.");
    this.submitBtn = page.locator('#set-config-btn')
    // Create Custom Agent Locators
    this.settingsBtn = page.getByRole('button', { name: 'Settings', exact: true });
    this.createCustomAgentBtn = page.getByRole("button", { name: "Create Custom Agent" });
    this.customAgentTab = page.getByRole("tab", { name: /agents/i });
    this.searchAgentInput = page.getByPlaceholder('Search Agent')
    this.agentNameInput = page.getByRole("textbox", { name: "Agent Name" });
    this.agentDescriptionInput = page.getByRole("textbox", { name: 'Describe what this agent does' });
    this.ageentIdentityButton = page.getByRole("button", { name: 'Agent Identity' });            //1
    this.agentSetAgentBehaviorAndGuidelines = page.getByRole('button', { name: 'Behavior & Guidelines' })   //2
    this.agenRole = page.getByRole('textbox', { name: 'You are a [role], responsible' })
    this.agentInstructionsInput = page.getByRole('textbox', { name: 'Key responsibilities: 1. [' })
    this.ageentToolsOrAgentselectionButton = page.getByText('Tool/Agent Selection').first()   //3
    this.selectAgentOrTool = page.locator('[id="auto-complete-field-for-tool/agent"]');
    this.listOfAgentsOrTools = page.getByText('anomaly_execute - system')
    
    this.agentToolUsage = page.getByRole('textbox', { name: 'Tool: [Tool Name] Purpose: [' })
    this.agentKnoowledgeAndExample = page.getByRole('button', { name: 'Knowledge & Examples' })      //4
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


    // Agent CRUD action locators
    this.agentMoreActionsBtn = page.getByRole('button', { name: 'More actions' });
    this.editAgentMenuItem = page.getByRole('menuitem', { name: 'Edit Agent' });
    this.deleteAgentMenuItem = page.getByRole('menuitem', { name: 'Delete Agent' });
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