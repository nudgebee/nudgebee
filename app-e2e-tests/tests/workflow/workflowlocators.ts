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

  // Action node locators (canvas nodes)
  readonly action_tickets_resolve: Locator;
  readonly action_tickets_create: Locator;
  readonly action_tickets_get: Locator;
  readonly action_tickets_add_comment: Locator;
  readonly action_tickets_get_comments: Locator;
  readonly action_tickets_transition: Locator;
  readonly action_tickets_update: Locator;
  readonly action_tickets_assign: Locator;
  readonly action_notifications_im: Locator;
  readonly action_notifications_email: Locator;

  // Action panel field locators
  readonly account_id_input: Locator;
  readonly integration_id_input: Locator;
  readonly project_id_input: Locator;
  readonly ticket_id_input: Locator;
  readonly project_key_input: Locator;
  readonly actionPanelCloseBtn: Locator;

  // Action panel — Test Action section
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

    // getByText exact:true matches only the toggle label, not "Workflow JSON Editor"; .. walks up to the clickable parent
    this.jsonPanelToggleBtn = page.getByText("JSON", { exact: true }).locator("..");
    this.codeMirrorEditor = page.locator(".cm-content");
    this.applyJsonBtn = page.getByRole("button", { name: "Apply" });

    this.saveBtn = page.locator("#workflow-save-btn");
    this.runBtn = page.locator("#workflow-run-btn");
    this.triggerAutomationBtn = page.getByRole("button", { name: "Trigger Automation" });

    // Excludes the global cluster picker so we target only the workflow status dropdown
    this.statusDropdown = page
      .locator(".MuiAutocomplete-root")
      .filter({ hasNot: page.locator("#auto-complete-global-cluster") })
      .last();
    this.activeStatusOption = page.getByRole("option", { name: "ACTIVE", exact: true });

    this.action_tickets_resolve = page.getByText("tickets_resolve", { exact: false }).first();
    this.action_tickets_create = page.getByText("tickets_create", { exact: false }).first();
    this.action_tickets_get = page.getByText("tickets_get", { exact: false }).first();
    this.action_tickets_add_comment = page.getByText("tickets_add_comment", { exact: false }).first();
    this.action_tickets_get_comments = page.getByText("tickets_get_comments", { exact: false }).first();
    this.action_tickets_transition = page.getByText("tickets_transition", { exact: false }).first();
    this.action_tickets_update = page.getByText("tickets_update", { exact: false }).first();
    this.action_tickets_assign = page.getByText("tickets_assign", { exact: false }).first();
    this.action_notifications_im = page.getByText("notifications_im", { exact: false }).first();
    this.action_notifications_email = page.getByText("notifications_email", { exact: false }).first();

    this.account_id_input = page.locator("#auto-complete-field-for-label").nth(0);
    this.integration_id_input = page.locator("#auto-complete-field-for-label").nth(1);
    this.project_id_input = page.getByPlaceholder("Select project");
    this.ticket_id_input = page.getByRole("textbox", { name: "Ticket ID to retrieve" });
    this.project_key_input = page.getByRole("textbox", { name: "Project key (required for GitHub/GitLab in owner/repo format)" });
    this.actionPanelCloseBtn = page.locator('div.MuiDialog-container [data-testid="CloseIcon"]').first();

    this.dryRunBtn = page.locator("div.MuiDialog-container").getByRole("button", { name: "Dry Run" });
    this.runTaskBtn = page.locator("div.MuiDialog-container").getByRole("button", { name: "Run Task" });
    this.actionTestResultChip = page.locator("div.MuiDialog-container .MuiChip-label");
  }

  getSuccessMessage(workflowName: string): Locator {
    return this.page.getByText(`Automation "${workflowName}" created successfully`);
  }
}
