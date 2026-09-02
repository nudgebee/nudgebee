// Not for OSS
import { Page, Locator } from "@playwright/test";
import { WorkflowLocators } from "../workflowlocators";

// NodeCategoriesSidebar.tsx and TriggerNode/ActionNode's add-edge buttons render zero
// data-testid attributes (grep -c 'data-testid' returns 0 for NodeCategoriesSidebar.tsx),
// so an id primary is the highest rung available for the palette locators below.
export class AiLlmBuilderLocators extends WorkflowLocators {
  readonly nodePaletteHeading: Locator;
  readonly nodePaletteSearchInput: Locator;
  readonly nodePaletteCloseBtn: Locator;
  readonly llmCategoryAccordion: Locator;
  readonly llmActionButtons: Locator;
  readonly llmSummaryActionBtn: Locator;
  readonly addNodeFromTriggerBtn: Locator;
  readonly canvasNodes: Locator;
  readonly actionSidebarSaveBtn: Locator;
  readonly actionSidebarParametersTab: Locator;
  readonly actionSidebarSettingsTab: Locator;
  readonly actionSidebarCloseBtn: Locator;
  readonly requiredFieldError: Locator;
  readonly workflowListingRow: Locator;
  readonly summaryMessageField: Locator;

  constructor(page: Page) {
    super(page);

    this.nodePaletteHeading = page.getByRole("heading", { name: "Add Node to Automation" }).or(page.getByText("Add Node to Automation", { exact: true })).first();
    // "Search actions..." is the only occurrence of that placeholder in app/src, so the page-level fallback cannot bind to another box.
    this.nodePaletteSearchInput = page.locator("#wf-node-categories-search-input").or(page.getByPlaceholder("Search actions...")).first();
    // Deliberately id-only: the palette popup has no container id, and its aria-label "Close" is shared
    // with every other dialog on the page, so any wider fallback would resolve to a different close button.
    this.nodePaletteCloseBtn = page.locator("#wf-node-categories-close-btn");
    this.llmCategoryAccordion = page.locator("#wf-node-categories-llm-accordion").or(page.getByText("AI/LLM", { exact: true })).first();
    // Subcategory keys are the full task name (nodeCategories.ts: taskKey = task.name), so ids read
    // "wf-node-categories-action-llm-llm.summary-btn". A prefix match survives the catalog gaining or
    // losing an AI/LLM action, which a hardcoded list would not.
    this.llmActionButtons = page.locator('[id^="wf-node-categories-action-llm-"]');
    // The dot in the task name makes "#...llm.summary-btn" parse as a class chain, so this must stay attribute form.
    this.llmSummaryActionBtn = page
      .locator('[id="wf-node-categories-action-llm-llm.summary-btn"]')
      .or(this.llmActionButtons.filter({ hasText: "Summary" }))
      .first();

    this.addNodeFromTriggerBtn = page.locator("#wf-node-trigger-add-edge-btn").or(page.locator("#wf-node-action-add-edge-btn")).first();
    this.canvasNodes = page.locator(".react-flow__node");

    this.actionSidebarSaveBtn = page.locator("#action-sidebar-save-btn").or(this.dialog.getByRole("button", { name: "Save", exact: true })).first();
    this.actionSidebarParametersTab = page.locator("#action-sidebar-parameters-tab").or(this.dialog.getByRole("tab", { name: /Parameters/i })).first();
    this.actionSidebarSettingsTab = page.locator("#action-sidebar-settings-tab").or(this.dialog.getByRole("tab", { name: /Settings/i })).first();
    this.actionSidebarCloseBtn = page.locator("#action-sidebar-close-btn").or(this.dialog.locator('[data-testid="CloseIcon"]')).first();

    // useTaskValidation.ts builds every required-field message as "<field> is required", rendered as the field's helper text.
    this.requiredFieldError = this.dialog.getByText(/is required/i).first();
    this.workflowListingRow = page.locator('[id^="workflow-menu-"]');
    // The param textarea has no accessible name (StableFormFields renders the label as a sibling
    // Typography, not a <label>) and no id of its own — FormComponents falls back to a shared
    // "field-for-label" id when no label prop is passed. So anchor on the per-field char counter,
    // the one element StableFormFields keys by fieldName, and step up to the box holding both.
    // MUI's autosize adds an aria-hidden shadow textarea that must not be the one we type into.
    this.summaryMessageField = this.dialog
      .locator('[data-testid="message-char-counter"]')
      .locator("xpath=..")
      .locator('textarea:not([aria-hidden="true"])')
      .or(this.dialog.locator('textarea#field-for-label:not([aria-hidden="true"])'))
      .first();
  }

  llmNodeOnCanvas(taskId: string): Locator {
    return this.page.getByTestId(`rf__node-${taskId}`).or(this.canvasNodes.filter({ hasText: taskId })).first();
  }
}
