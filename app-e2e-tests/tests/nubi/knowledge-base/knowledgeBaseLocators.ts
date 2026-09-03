import { Page, Locator, expect } from "@playwright/test";
import { NubiLocators } from "../nubiLocators";

export class KnowledgeBaseLocators {
  readonly page: Page;

  readonly bCortexBtn: Locator;
  readonly knowledgeGroupTab: Locator;
  readonly knowledgeBaseTab: Locator;
  readonly userSubTab: Locator;
  readonly addKBBtn: Locator;

  readonly nameInput: Locator;
  readonly descriptionInput: Locator;
  readonly contentTextarea: Locator;

  readonly agentSectionLabel: Locator;
  readonly allAgentsChip: Locator;
  readonly specificAgentsChip: Locator;
  readonly agentSearchInput: Locator;
  readonly firstAgentCheckbox: Locator;

  readonly createBtn: Locator;
  readonly updateBtn: Locator;
  readonly formCancelBtn: Locator;

  readonly loadHistoryMenuItem: Locator;
  readonly deleteMenuItem: Locator;

  readonly deleteConfirmBtn: Locator;

  readonly infoBtn: Locator;
  readonly infoTooltip: Locator;

  readonly successCreated: Locator;
  readonly successUpdated: Locator;
  readonly successDeleted: Locator;

  constructor(page: Page) {
    this.page = page;

    this.bCortexBtn = page
      .getByTestId("nav-bcortex-btn")
      .or(page.getByRole("button", { name: "b-Cortex", exact: true }));

    // Top-level "Knowledge" tab (docs/ia-consolidation-plan.md, PR 2) — Knowledge
    // Base is one of its sub-tabs now, not reachable until this is selected.
    // exact:true is required: without it this also matches "Knowledge Base" and
    // "Knowledge Graph", both of which are also `role=tab` at that point.
    this.knowledgeGroupTab = page.getByRole("tab", { name: "Knowledge", exact: true });

    this.knowledgeBaseTab = page.locator("#tab-knowledge-base").or(page.getByRole("tab", { name: /Knowledge Base/i }));

    this.userSubTab = page
      .locator('#tab-manual')
      .or(page.getByRole("tab", { name: /User/i }));

    this.addKBBtn = page.locator("button").filter({ hasText: /Add Knowledge Base/ });

    this.nameInput = page
      .getByPlaceholder("e.g. aws_ec2_runbook")
      .or(page.locator('input[placeholder*="aws_ec2"]'));

    this.descriptionInput = page
      .getByPlaceholder("e.g. Steps to debug OOM errors in production K8s pods")
      .or(page.locator('textarea[placeholder*="Steps to debug"]'));

    this.contentTextarea = page
      .getByPlaceholder("Paste or type your knowledge base content here...")
      .or(page.locator('textarea[placeholder*="knowledge base content"]'));

    // Agent mapping block of the create/edit form. ds/Chip renders an interactive
    // chip as a ButtonBase, so the selected state is readable from aria-pressed.
    this.agentSectionLabel = page.getByText("Which agents should use this?", { exact: false });
    this.allAgentsChip = page.getByRole("button", { name: "All agents", exact: true });
    this.specificAgentsChip = page.getByRole("button", { name: "Specific agents", exact: true });
    this.agentSearchInput = page.getByPlaceholder("Search agents");
    // The agent list is tenant data, so no fixed name exists to target — the
    // aria-label prefix is the only stable handle on "whichever agent is first".
    this.firstAgentCheckbox = page.locator('input[type="checkbox"][aria-label^="Select agent "]').first();

    this.createBtn = page.getByRole("button", { name: "Create" }).last();
    this.updateBtn = page.getByRole("button", { name: "Update" }).last();
    this.formCancelBtn = page.getByRole("button", { name: "Cancel" }).last();

    this.loadHistoryMenuItem = page
      .locator('[role="menuitem"]')
      .filter({ hasText: /^Load History$/ })
      .or(page.locator("li, [role='option']").filter({ hasText: /^Load History$/ }));

    this.deleteMenuItem = page
      .locator('[role="menuitem"]')
      .filter({ hasText: /^Delete$/ })
      .or(page.locator("li, [role='option']").filter({ hasText: /^Delete$/ }));

    this.deleteConfirmBtn = page
      .locator('[role="dialog"]')
      .getByRole("button", { name: "Delete" })
      .or(page.locator('[role="dialog"] button').filter({ hasText: /^Delete$/ }));

    this.infoBtn = page
      .locator('[role="dialog"]')
      .getByText('Name *', { exact: true })
      .locator('xpath=..')
      .locator('svg, [data-testid="kb-form-name-info"]')
      .first();
    this.infoTooltip = page.locator('[role="tooltip"]').first();

    this.successCreated = page
      .getByText("Knowledge base created successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "created successfully" }));

    this.successUpdated = page
      .getByText("Knowledge base updated successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "updated successfully" }));

    this.successDeleted = page
      .getByText("Knowledge base deleted successfully")
      .or(page.locator("[class*='snackbar'], [class*='toast'], [role='alert']")
          .filter({ hasText: "deleted successfully" }));
  }

  async openBCortex(): Promise<void> {
    await this.waitNubiPageSettled();

    await this.bCortexBtn.first().waitFor({ state: "visible", timeout: 15000 });
    await this.bCortexBtn.first().click();
    await this.openKnowledgeGroup();

    if (await this.modalGenuinelyClosed()) {
      await this.bCortexBtn.first().click();
      await this.openKnowledgeGroup();
    }
  }

  // b-Cortex opens on its Memory tab by default; Knowledge Base is a sub-tab
  // under the separate top-level "Knowledge" tab, so it only exists in the DOM
  // once that tab is selected.
  private async openKnowledgeGroup(): Promise<void> {
    await this.knowledgeGroupTab.first().waitFor({ state: "visible", timeout: 15000 });
    await this.knowledgeGroupTab.first().click();
    await this.knowledgeBaseTab.first().waitFor({ state: "visible", timeout: 15000 });
  }

  private async waitNubiPageSettled(): Promise<void> {
    await this.page.waitForLoadState("networkidle").catch(() => {});
    await this.page
      .getByText("What can we Optimize?", { exact: false })
      .first()
      .waitFor({ state: "visible", timeout: 15000 })
      .catch(() => {});
  }

  private async modalGenuinelyClosed(): Promise<boolean> {
    await this.page.waitForTimeout(1000);
    if (await this.knowledgeBaseTab.first().isVisible().catch(() => false)) return false;
    await this.page.waitForTimeout(200);
    return !(await this.knowledgeBaseTab.first().isVisible().catch(() => false));
  }

  async navigateToKnowledgeBase(_nubiLocators?: NubiLocators): Promise<void> {
    await this.openBCortex();
    await this.knowledgeBaseTab.first().click();
    await this.page.waitForLoadState("networkidle");
  }

  async navigateToUserTab(): Promise<void> {
    const appeared = await this.userSubTab.first()
      .waitFor({ state: "visible", timeout: 8000 })
      .then(() => true)
      .catch(() => false);
    if (!appeared) return;

    await this.userSubTab.first().click();
    await this.addKBBtn.first().waitFor({ state: "visible", timeout: 5000 });
  }

  agentCheckbox(agentName: string): Locator {
    return this.page.getByLabel(`Select agent ${agentName}`);
  }

  async firstAgentName(): Promise<string> {
    const label = await this.firstAgentCheckbox.getAttribute("aria-label");
    return (label ?? "").replace("Select agent ", "");
  }

  async openCreateModal(): Promise<void> {
    await this.addKBBtn.first().waitFor({ state: "visible", timeout: 10000 });
    await this.addKBBtn.first().click();
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async fillForm(name: string, content: string, description?: string): Promise<void> {
    await this.nameInput.fill(name);
    if (description) {
      await this.descriptionInput.fill(description);
    }
    await this.contentTextarea.fill(content);
  }

  getKBCardByName(name: string): Locator {
    return this.page
      .locator("div")
      .filter({ has: this.page.getByText(name, { exact: true }) })
      .first();
  }

  async clickEditForCard(name: string): Promise<void> {
    const card = this.getKBCardByName(name);
    const editBtn = card
      .getByRole("button", { name: "Edit" })
      .or(card.locator("button[title='Edit'], button[aria-label='Edit']"));

    await editBtn.first().click();
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async clickThreeDotsForCard(name: string): Promise<void> {
    const card = this.getKBCardByName(name);
    const menu = card
      .locator("#three-dot-menu")
      .or(card.locator("button[aria-label*='more'], button[aria-label*='menu'], button[aria-label*='options']"));

    await menu.first().click();
  }

  async openDeleteForCard(name: string): Promise<void> {
    await this.clickThreeDotsForCard(name);
    await this.deleteMenuItem.first().waitFor({ state: "visible", timeout: 5000 });
    await this.deleteMenuItem.first().click();
    await this.deleteConfirmBtn.first().waitFor({ state: "visible", timeout: 5000 });
  }

  async deleteKBByName(name: string): Promise<void> {
    await this.navigateToUserTab();
    await this.openDeleteForCard(name);
    await this.deleteConfirmBtn.first().click();
    await expect(this.successDeleted.first()).toBeVisible({ timeout: 15000 });
  }
}
