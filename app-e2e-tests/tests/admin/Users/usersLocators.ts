import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

export class UserLocators extends CommonLocators {
  readonly addNewUserBtn: Locator;
  readonly firstNameInput: Locator;
  readonly lastNameInput: Locator;
  readonly emailInput: Locator;
  readonly tenantRoleCombobox: Locator;
  readonly addUserSubmitBtn: Locator;
  readonly cancelBtn: Locator;
  readonly successMsg: Locator;
  readonly duplicateMsg: Locator;
  readonly editUserModal: Locator;
  readonly successUpdateMsg: Locator;
  readonly statusButton: Locator;
  readonly nameSearchInput: Locator;

  constructor(page: Page) {
    super(page);

    this.addNewUserBtn = page.locator('#new-user');
    this.firstNameInput = page.locator('#user-modal-firstname');
    this.lastNameInput = page.locator('#user-modal-lastname');
    this.emailInput = page.locator('#user-modal-email');
    this.tenantRoleCombobox = page.locator('#user-modal-tenant-role');
    this.addUserSubmitBtn = page.locator('#user-modal-submit-button');
    this.cancelBtn = page.locator('#user-modal-cancel-button');
    this.successMsg = page.getByText("User Added Successfully").first();
    this.duplicateMsg = page.getByText("This email is already in use").first();
    this.editUserModal = page.locator('#edit-user-modal');
    this.successUpdateMsg = page.getByText("User updated");
    this.statusButton = page.locator('#auto-complete-all-users-status-filter');
    this.nameSearchInput = page.locator('#all-users-name-search');
  }

  async selectRole(role: string): Promise<void> {
    await this.tenantRoleCombobox.click();
    const option = this.getRoleOption(role);
    await option.waitFor({ state: "visible", timeout: 5000 });
    // The role dropdown is a MUI Menu (disablePortal) rendered inside the modal.
    // Dispatch the click straight to the option so its handler runs regardless of the
    // invisible backdrop that overlays the items in headless (a normal click gets
    // swallowed by that backdrop). This sets the value but, headless, does not always
    // close the menu — so press Escape to dismiss it. Escape targets the topmost
    // overlay (the menu), not the dialog beneath, then confirm the menu closed.
    await option.dispatchEvent("click");
    if (await option.isVisible().catch(() => false)) {
      await this.page.keyboard.press("Escape");
    }
    await option.waitFor({ state: "detached", timeout: 5000 });
  }

  getRoleOption(role: string): Locator {
    return this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^${role}$`) });
  }

  getEditBtnForUser(email: string): Locator {
    return this.page.locator('tr').filter({ hasText: email }).locator('#edit-user-button');
  }

  // Set the user's status inside the edit/add modal.
  // Radio group with no id/testid — match by exact text ("Active" is a substring of "Inactive").
  async setUserStatus(status: "Active" | "Inactive" | "Suspended"): Promise<void> {
    await this.page.getByRole("radio", { name: status, exact: true }).click();
  }

  // The table row for a given user, matched by email — used to verify presence after filter/search.
  getUserRow(email: string): Locator {
    return this.page.locator("tr").filter({ hasText: email });
  }

  // Open the list Status filter dropdown and pick Active / Inactive / Suspended.
  // Options render only after the button is clicked and carry no id/testid — match by exact text.
  async selectStatusFilter(status: string): Promise<void> {
    await this.statusButton.click();
    await this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${status}$`) })
      .click();
  }

  // Type a name into the users search box and submit.
  // Enter is required — fill() alone does not trigger the filter.
  async searchByName(name: string): Promise<void> {
    await this.nameSearchInput.click(); // ensure focus so Enter reliably lands
    await this.nameSearchInput.fill(name);
    await this.nameSearchInput.press("Enter");
  }
}
