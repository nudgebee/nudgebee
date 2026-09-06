// Not for OSS
import { test, expect } from "@playwright/test";
import { AiLlmBuilderLocators } from "./aiLlmBuilderLocators";
import {
  loginAndNavigateToNewWorkflow,
  pasteAndApplyWorkflowJson,
  saveNewWorkflow,
  deleteCreatedWorkflow,
} from "../workflowHelper";
import {
  uniqueAiLlmWorkflowName,
  requireCluster,
  openNodePalette,
  expandLlmCategory,
  reopenWorkflowFromListing,
} from "./aiLlmBuilderHelper";

test.setTimeout(300000);

const SUMMARY_MESSAGE = "Summarize the e2e coverage fixture text";

function summaryWorkflowJson(name: string) {
  return {
    name,
    definition: {
      version: "v1",
      timeout: "",
      inputs: [],
      output: {},
      tasks: [
        {
          id: "llm_summary",
          type: "llm.summary",
          params: { message: SUMMARY_MESSAGE },
        },
      ],
      triggers: [{ type: "manual", params: {} }],
    },
  };
}

function investigateWorkflowJson(name: string) {
  return {
    name,
    definition: {
      version: "v1",
      timeout: "",
      inputs: [],
      output: {},
      tasks: [
        {
          id: "llm_investigate",
          type: "llm.investigate",
          params: { message: SUMMARY_MESSAGE },
        },
      ],
      triggers: [{ type: "manual", params: {} }],
    },
  };
}

test.beforeEach(() => {
  requireCluster();
});

test(
  "Automation AI/LLM sanity - open a new automation, open the node palette, expand the AI/LLM category, verify the AI/LLM actions are listed",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    await loginAndNavigateToNewWorkflow(page, locators);

    await openNodePalette(page, locators);
    await expandLlmCategory(locators);

    await expect(locators.llmActionButtons.first()).toBeVisible();
    expect(await locators.llmActionButtons.count()).toBeGreaterThan(0);
    await expect(locators.llmSummaryActionBtn).toBeVisible();
  }
);

test(
  "Automation AI/LLM sanity - open the node palette, search \"summary\", verify only matching actions remain listed",
  { tag: ["@dev", "@regression", "@search", "@functional"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    await loginAndNavigateToNewWorkflow(page, locators);

    await openNodePalette(page, locators);
    await expandLlmCategory(locators);
    const unfilteredCount = await locators.llmActionButtons.count();

    await locators.nodePaletteSearchInput.fill("summary");
    await expect(locators.nodePaletteSearchInput).toHaveValue("summary");
    await expect(locators.llmSummaryActionBtn).toBeVisible();

    const filteredCount = await locators.llmActionButtons.count();
    expect(filteredCount).toBeLessThan(unfilteredCount);
    expect(filteredCount).toBeGreaterThan(0);
    for (const label of await locators.llmActionButtons.allTextContents()) {
      expect(label.toLowerCase()).toContain("summary");
    }

    await locators.nodePaletteSearchInput.fill("");
    await expect(locators.nodePaletteSearchInput).toHaveValue("");
    await expect(locators.llmActionButtons).toHaveCount(unfilteredCount);
  }
);

test(
  "Automation AI/LLM - open the node palette, add the AI/LLM Summary action, verify the action node is added to the canvas",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    await loginAndNavigateToNewWorkflow(page, locators);

    const nodesBefore = await locators.canvasNodes.count();
    await openNodePalette(page, locators);
    await expandLlmCategory(locators);
    await locators.llmSummaryActionBtn.click();

    await expect(locators.nodePaletteHeading).toBeHidden({ timeout: 15000 });
    await expect(locators.canvasNodes).toHaveCount(nodesBefore + 1, { timeout: 20000 });
    await expect(locators.canvasNodes.filter({ hasText: /summary/i }).first()).toBeVisible();
  }
);

test(
  "Automation AI/LLM - build an llm.summary automation, save it, reopen it from the listing, verify the llm_summary node persisted",
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    const workflowName = uniqueAiLlmWorkflowName("Persist");
    await loginAndNavigateToNewWorkflow(page, locators);

    await pasteAndApplyWorkflowJson(page, locators, summaryWorkflowJson(workflowName));
    await expect(locators.llmNodeOnCanvas("llm_summary")).toBeVisible({ timeout: 20000 });
    await saveNewWorkflow(page, locators, workflowName);

    // Reopening from the listing is what proves the automation was stored; the create toast alone does not.
    await reopenWorkflowFromListing(page, locators, workflowName);
    await expect(locators.llmNodeOnCanvas("llm_summary")).toBeVisible({ timeout: 30000 });

    await deleteCreatedWorkflow(page, locators, workflowName);
  }
);

test(
  "Automation AI/LLM - open the llm_summary action, clear the required message field, verify the message is required error",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    const workflowName = uniqueAiLlmWorkflowName("Validation");
    await loginAndNavigateToNewWorkflow(page, locators);

    await pasteAndApplyWorkflowJson(page, locators, summaryWorkflowJson(workflowName));
    await locators.llmNodeOnCanvas("llm_summary").dblclick();
    await expect(locators.dialog).toBeVisible({ timeout: 20000 });

    await expect(locators.summaryMessageField).toHaveValue(SUMMARY_MESSAGE, { timeout: 20000 });
    await locators.summaryMessageField.fill("");
    await expect(locators.summaryMessageField).toHaveValue("");

    await expect(locators.requiredFieldError).toBeVisible({ timeout: 20000 });
    await expect(locators.requiredFieldError).toContainText(/is required/i);
  }
);

test(
  "Automation AI/LLM - open the llm_investigate action, switch to the Settings tab and back to Parameters, verify the parameters form returns",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    const workflowName = uniqueAiLlmWorkflowName("Tabs");
    await loginAndNavigateToNewWorkflow(page, locators);

    await pasteAndApplyWorkflowJson(page, locators, investigateWorkflowJson(workflowName));
    await locators.llmNodeOnCanvas("llm_investigate").dblclick();
    await expect(locators.dialog).toBeVisible({ timeout: 20000 });
    await expect(locators.actionSidebarParametersTab).toHaveAttribute("aria-selected", "true", { timeout: 20000 });

    await locators.actionSidebarSettingsTab.click();
    await expect(locators.actionSidebarSettingsTab).toHaveAttribute("aria-selected", "true", { timeout: 15000 });
    await expect(locators.actionSidebarParametersTab).toHaveAttribute("aria-selected", "false");

    await locators.actionSidebarParametersTab.click();
    await expect(locators.actionSidebarParametersTab).toHaveAttribute("aria-selected", "true", { timeout: 15000 });
    await expect(locators.summaryMessageField).toBeVisible();
  }
);

test(
  "Automation AI/LLM - open the node palette, close it without adding an action, verify the canvas node count is unchanged",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    const locators = new AiLlmBuilderLocators(page);
    await loginAndNavigateToNewWorkflow(page, locators);

    const nodesBefore = await locators.canvasNodes.count();
    await openNodePalette(page, locators);
    await expandLlmCategory(locators);

    await locators.nodePaletteCloseBtn.click();
    await expect(locators.nodePaletteHeading).toBeHidden({ timeout: 15000 });
    await expect(locators.canvasNodes).toHaveCount(nodesBefore);
  }
);
