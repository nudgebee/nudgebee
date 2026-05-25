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
  readonly firstNameHelperText: Locator;
  readonly lastNameHelperText: Locator;
  readonly statusCombobox: Locator;
  readonly statusFilterBtn: Locator;
  readonly userSearchToggleBtn: Locator;
  readonly userSearchInput: Locator;

  constructor(page: Page) {
    super(page);

    this.addNewUserBtn = page.locator('#new-user');
    this.firstNameInput = page.locator('#user-modal-firstname');
    this.lastNameInput = page.locator('#user-modal-lastname');
    this.emailInput = page.locator('#user-modal-email');
    this.tenantRoleCombobox = page.locator('#auto-complete-user-modal-tenant-role');
    this.addUserSubmitBtn = page.locator('#user-modal-submit-button');
    this.cancelBtn = page.locator('#user-modal-cancel-button');
    this.successMsg = page.getByText("User Added Successfully").first();
    this.duplicateMsg = page.getByText("This email is already in use").first();
    this.editUserModal = page.locator('#edit-user-modal');
    this.successUpdateMsg = page.getByText("User updated");
    this.firstNameHelperText = page.locator('#user-modal-firstname-helper-text');
    this.lastNameHelperText = page.locator('#user-modal-lastname-helper-text');
    this.statusCombobox = page.locator('#auto-complete-user-modal-status');
    this.statusFilterBtn = page.locator('button').filter({ hasText: 'By Status' });
    this.userSearchToggleBtn = page.locator('#box-all-users-search-toggle-button');
    this.userSearchInput = page.locator('#box-all-users-search-input-text');
  }

  getRoleOption(role: string): Locator {
    const testIdMap: Record<string, string> = {
      'Admin': 'user-modal-role-tenant_admin',
      'ReadOnly Admin': 'user-modal-role-tenant_admin_readonly',
    };
    const testId = testIdMap[role];
    if (testId) return this.page.getByTestId(testId);
    return this.page.getByRole('radio', { name: role, exact: true });
  }

  getEditBtnForUser(email: string): Locator {
    return this.page.locator('tr').filter({ hasText: email }).locator('#edit-user-button');
  }

  getStatusOption(status: string): Locator {
    return this.page.locator('[role="presentation"]').getByText(status, { exact: true });
  }
}
