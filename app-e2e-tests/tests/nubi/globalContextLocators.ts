import { Page, Locator, expect } from "@playwright/test";
import { NubiLocators } from "./nubiLocators";

export class GlobalContextLocators {
  readonly page: Page;

  readonly globalContextTab: Locator;
  readonly addGlobalContextBtn: Locator;
  readonly emptyStateTitle: Locator;
  readonly createGlobalContextEmptyBtn: Locator;
  readonly nameInput: Locator;
  readonly descriptionInput: Locator;
  readonly contentTextarea: Locator;
  readonly characterCountLabel: Locator;
  readonly fileInput: Locator;
  readonly createBtn: Locator;
  readonly updateBtn: Locator;
  readonly formCancelBtn: Locator;
  readonly threeDotsMenuBtn: Locator;
  readonly editMenuItem: Locator;
  readonly deleteMenuItem: Locator;
  readonly deleteConfirmBtn: Locator;
  // scoped to .last() to avoid clashing with form Cancel
  readonly deleteCancelBtn: Locator;
  readonly successCreated: Locator;
  readonly successUpdated: Locator;
  readonly successDeleted: Locator;

  constructor(page: Page) {
    this.page = page;

    this.globalContextTab = page.locator("#settings-tab-global-context");
    this.addGlobalContextBtn = page.getByRole("button", { name: "Add Global Context" });
    this.emptyStateTitle = page.getByText("No global contexts found");
    this.createGlobalContextEmptyBtn = page.getByRole("button", { name: "Create Global Context" });

    this.nameInput = page.getByPlaceholder(/Enter a name for this global context/i);
    this.descriptionInput = page.getByPlaceholder("Enter a description for this global context");
    this.contentTextarea = page.getByPlaceholder("Paste or type your global context content here...");
    this.characterCountLabel = page.locator("p").filter({ hasText: /^\d+ characters$/ });
    this.fileInput = page.locator('input[type="file"]');
    // .last() to avoid matching any outer "Create" button in the settings header
    this.createBtn = page.getByRole("button", { name: "Create" }).last();
    this.updateBtn = page.getByRole("button", { name: "Update" }).last();
    this.formCancelBtn = page.getByRole("button", { name: "Cancel" }).last();

    this.threeDotsMenuBtn = page.locator("#three-dot-menu");
    this.editMenuItem = page.locator('[role="menuitem"]').filter({ hasText: /^Edit$/ });
    this.deleteMenuItem = page.locator('[role="menuitem"]').filter({ hasText: /^Delete$/ });

    this.deleteConfirmBtn = page.locator('[role="dialog"]').getByRole("button", { name: "Delete" });
    this.deleteCancelBtn = page.locator('[role="dialog"]').getByRole("button", { name: "Cancel" }).last();

    this.successCreated = page.getByText("Global context created successfully");
    this.successUpdated = page.getByText("Global context updated successfully");
    this.successDeleted = page.getByText("Global context deleted successfully");
  }

  async navigateToGlobalContext(nubiLocators: NubiLocators): Promise<void> {
    await nubiLocators.settingsBtn.click();
    await this.globalContextTab.waitFor({ state: "visible", timeout: 15000 });
    await this.globalContextTab.click();
  }

  // Falls back to the empty-state button when the header button is disabled
  async openCreateModal(): Promise<void> {
    const addEnabled =
      (await this.addGlobalContextBtn.isVisible().catch(() => false)) &&
      (await this.addGlobalContextBtn.isEnabled().catch(() => false));

    if (addEnabled) {
      await this.addGlobalContextBtn.click();
    } else {
      await this.createGlobalContextEmptyBtn.click();
    }
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async fillForm(name: string, content: string, description?: string): Promise<void> {
    await this.nameInput.fill(name);
    if (description) {
      await this.descriptionInput.fill(description);
    }
    await this.contentTextarea.fill(content);
  }

  async openEditModal(): Promise<void> {
    await this.threeDotsMenuBtn.click();
    await this.editMenuItem.waitFor({ state: "visible", timeout: 5000 });
    await this.editMenuItem.click();
    await this.nameInput.waitFor({ state: "visible", timeout: 10000 });
  }

  async openDeleteModal(): Promise<void> {
    await this.threeDotsMenuBtn.click();
    await this.deleteMenuItem.waitFor({ state: "visible", timeout: 5000 });
    await this.deleteMenuItem.click();
    await this.deleteConfirmBtn.waitFor({ state: "visible", timeout: 5000 });
  }

  async deleteAndVerify(): Promise<void> {
    await this.openDeleteModal();
    await this.deleteConfirmBtn.click();
    await expect(this.successDeleted).toBeVisible({ timeout: 15000 });
  }

  getContextCardByName(name: string): Locator {
    return this.page
      .locator("div")
      .filter({ has: this.page.getByText(name, { exact: true }) })
      .filter({ hasText: "Updated" })
      .first();
  }
}
