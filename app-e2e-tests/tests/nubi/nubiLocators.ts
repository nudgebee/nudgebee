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
    this.settingsBtn = page.getByRole('button', { name: 'settings', exact: true });
    this.createCustomAgentBtn = page.getByRole("button", { name: "Create Custom Agent" });
    this.customAgentTab = page.getByRole("tab", { name: /agents/i });
    this.searchAgentInput = page.getByRole('searchbox', { name: 'Search Agent' })
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
    this.ToolButton = page.locator('#settings-tab-tools');
    this.CreateToolButton = page.locator('#create-tool');
    this.ToolName = page.getByRole('textbox', { name: 'Enter tool name' });
    this.ToolDescription = page.getByRole('textbox', { name: 'Describe what this tool does' });
    this.ToolTypeRunbook = page.getByRole('radio', { name: 'Runbook Action' })
    this.ToolTypeMCP = page.getByRole('radio', { name: 'MCP HTTP' })
    this.ToolTypeContainer = page.getByRole('radio', { name: 'Container' })
    this.RunbookAction = page.locator('#auto-complete-runbook-action');
    this.RunbookAction1 = page.getByRole('option', { name: 'Create Ticket' });
    this.SubmitButton = page.getByRole("button", { name: "Submit" });
    this.searchToolInput = page.getByRole('searchbox', { name: 'Search Tool' });
    this.HTTPurl = page.getByRole('textbox', { name: 'Enter MCP server URL' });
    this.ContainerImage = page.getByRole('textbox', { name: 'e.g., alpine:latest or myrepo/myimage:tag' });


    // Success and Failure Messages
    this.successMessage = page.getByText('Agent created successfully');
    this.failureMessage = page.getByText('Please fill the following fields: - Agent name already exists');
    this.toolCreatedMessage = page.getByText('Tool created successfully');
    this.toolCreationFailureMessage = page.getByText('Failed to create tool');
  }

  // Clicks the Nubi icon and retries up to 3 times if the panel does not open
  async openPanel(): Promise<void> {
    for (let attempt = 1; attempt <= 3; attempt++) {
      await this.askNudgebeeBtn.click();
      const opened = await this.settingsBtn
        .waitFor({ state: "visible", timeout: 8000 })
        .then(() => true)
        .catch(() => false);
      if (opened) return;
      if (attempt === 3) throw new Error("Nubi panel did not open after 3 click attempts");
    }
  }
}