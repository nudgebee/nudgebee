import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../GlobalLocators";

export class WorkflowLocators extends CommonLocators {
  readonly autoPilotSidenavBtn: Locator;
  readonly createAutomationBtn: Locator;
  readonly createNewAutomationModal: Locator;
  readonly makeAnAutomationCard: Locator;

  readonly editTitleBtn: Locator;
  readonly titleInput: Locator;
  readonly confirmTitleBtn: Locator;
  readonly manualTriggerOption: Locator;
  readonly scheduleTriggerOption: Locator;
  readonly webhookTriggerOption: Locator;
  readonly eventTriggerOption: Locator;
  readonly jsonPanelToggleBtn: Locator;
  readonly codeMirrorEditor: Locator;
  readonly applyJsonBtn: Locator;
  readonly saveBtn: Locator;
  readonly runBtn: Locator;
  readonly triggerAutomationBtn: Locator;
  readonly statusDropdown: Locator;
  readonly activeStatusOption: Locator;

  readonly action_tickets_create: Locator;
  readonly action_tickets_get: Locator;
  readonly action_tickets_resolve: Locator;
  readonly action_tickets_add_comment: Locator;
  readonly action_tickets_get_comments: Locator;
  readonly action_tickets_transition: Locator;
  readonly action_tickets_update: Locator;
  readonly action_tickets_assign: Locator;
  readonly action_notifications_im: Locator;
  readonly action_notifications_email: Locator;
  readonly action_llm_nubi: Locator;
  readonly action_llm_mcp_call: Locator;
  readonly action_llm_investigate: Locator;
  readonly action_llm_summary: Locator;
  readonly action_k8s_cli: Locator;
  readonly select_channel: Locator;
  readonly select_team: Locator;

  readonly dialog: Locator;
  readonly dialogContent: Locator;
  readonly integrationIdDropdown: Locator;
  readonly projectKeyDropdown: Locator;
  readonly account_id_input: Locator;
  readonly project_id_input: Locator;
  readonly ticket_id_input: Locator;
  readonly project_key_input: Locator;
  readonly Integration_Id_github: Locator;
  readonly project_key_input_github: Locator;
  readonly integration_id_input: Locator;
  readonly actionPanelCloseBtn: Locator;

  readonly dryRunBtn: Locator;
  readonly runTaskBtn: Locator;
  readonly actionTestResultChip: Locator;

  constructor(page: Page) {
    super(page);

    this.autoPilotSidenavBtn = page.locator("#auto-pilot-sidenavbutton");
    this.createAutomationBtn = page.getByRole("button", { name: "Create Automation" });
    this.createNewAutomationModal = page.getByText("Create a New Automation", { exact: true });
    this.makeAnAutomationCard = page.getByText("Make an Automation", { exact: true });

    this.editTitleBtn = page.locator('button:has([data-testid="EditIcon"])').first();
    this.titleInput = page.locator(".MuiOutlinedInput-input").first();
    this.confirmTitleBtn = page.locator('button:has([data-testid="CheckIcon"])').first();
    this.manualTriggerOption = page.getByTestId("trigger-option-manual");
    this.scheduleTriggerOption = page.getByTestId("trigger-option-schedule");
    this.webhookTriggerOption = page.getByTestId("trigger-option-webhook");
    this.eventTriggerOption = page.getByTestId("trigger-option-event");
    this.jsonPanelToggleBtn = page.getByText("JSON", { exact: true }).locator("..");
    this.codeMirrorEditor = page.locator(".cm-content");
    this.applyJsonBtn = page.getByRole("button", { name: "Apply" });
    this.saveBtn = page.locator("#workflow-save-btn");
    this.runBtn = page.locator("#workflow-run-btn");
    this.triggerAutomationBtn = page.getByRole("button", { name: "Trigger Automation" });
    this.statusDropdown = page
      .locator(".MuiAutocomplete-root")
      .filter({ hasNot: page.locator("#auto-complete-global-cluster") })
      .last();
    this.activeStatusOption = page.getByRole("option", { name: "ACTIVE", exact: true });

    this.action_tickets_create = page.getByRole("button", { name: /tickets create/i }).first();
    this.action_tickets_get = page.getByRole("button", { name: /tickets get/i }).first();
    this.action_tickets_resolve = page.getByRole("button", { name: /tickets resolve/i }).first();
    this.action_tickets_add_comment = page.getByRole("button", { name: /tickets add comment/i }).first();
    this.action_tickets_get_comments = page.getByRole("button", { name: /tickets get comments/i }).first();
    this.action_tickets_transition = page.getByRole("button", { name: /tickets transition/i }).first();
    this.action_tickets_update = page.getByRole("button", { name: /tickets update/i }).first();
    this.action_tickets_assign = page.getByRole("button", { name: /tickets assign/i }).first();
    this.action_notifications_im = page.getByRole("button", { name: /notifications im/i }).first();
    this.action_notifications_email = page.getByRole("button", { name: /notifications email/i }).first();
    this.action_llm_nubi = page.getByRole("button", { name: /llm nubi/i }).first();
    this.action_llm_mcp_call = page.getByRole("button", { name: /llm mcp call/i }).first();
    this.action_llm_investigate = page.getByRole("button", { name: /llm investigate/i }).first();
    this.action_llm_summary = page.getByRole("button", { name: /llm summary/i }).first();
    this.action_k8s_cli = page.getByRole("button", { name: /k8s cli/i }).first();
    this.select_channel = page.locator("#auto-complete-channel");
    this.select_team = page.locator("#auto-complete-team-id");

    this.dialog = page.locator("div.MuiDialog-container");
    this.dialogContent = page.locator("div.MuiDialog-container .MuiDialogContent-root");
    this.integrationIdDropdown = page.locator("div.MuiDialog-container #auto-complete-integration-id");
    this.projectKeyDropdown = page.locator("div.MuiDialog-container #auto-complete-project-key");
    this.account_id_input = page.locator("div.MuiDialog-container").getByRole("combobox").first();
    this.project_id_input = page.getByPlaceholder("Select project");
    this.ticket_id_input = page.getByRole("textbox", { name: "Ticket ID to retrieve" });
    this.project_key_input = page.getByRole("textbox", { name: "Project key (required for GitHub/GitLab in owner/repo format)" });
    this.Integration_Id_github = page.locator("div.MuiDialog-container").getByPlaceholder("Ticket integration to use");
    this.project_key_input_github = page.locator("div.MuiDialog-container").getByPlaceholder("Project key (required for GitHub/GitLab in owner/repo format)");
    this.integration_id_input = page.locator("#auto-complete-field-for-label").nth(1);
    this.actionPanelCloseBtn = page.locator('div.MuiDialog-container [data-testid="CloseIcon"]').first();

    this.dryRunBtn = page.locator("div.MuiDialog-container").getByRole("button", { name: "Dry Run" });
    this.runTaskBtn = page.locator("div.MuiDialog-container").getByRole("button", { name: "Run Task" });
    this.actionTestResultChip = page.locator("div.MuiDialog-container .MuiChip-label");
  }

  getSuccessMessage(workflowName: string): Locator {
    return this.page.getByText(`Automation "${workflowName}" created successfully`);
  }
}
