import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

export const Groups = [
  { name: "Tenant Admin" },
  { name: "Tenant Readonly" },
  { name: "Account Admin" },
  { name: "Account Readonly" },
  { name: "K8 Admin" },
  { name: "K8 Readonly" },
];

export class GroupLocators extends CommonLocators {
  readonly groupsTab!: Locator;
  readonly newUserGroupIdentifier!: Locator;
  readonly addUserGroupBtn!: Locator;
  readonly groupNameInput!: Locator;
  readonly descriptionInput!: Locator;
  readonly createGroupBtn!: Locator;
  readonly group_creation_successMsg!: Locator;
  readonly group_creation_duplicateMsg!: Locator;

  constructor(page: Page) {
    super(page);

    this.groupsTab = page.locator("#anchor-tab-Groups");
    this.newUserGroupIdentifier = page.locator("#new-user-group");

    this.addUserGroupBtn = page.getByText("Add User Group");
    this.groupNameInput = page.getByRole("textbox", { name: "Group Name" });
    this.descriptionInput = page.getByRole("textbox", { name: "Description" });
    this.createGroupBtn = page.getByRole("button", { name: "Create Group" });

    this.group_creation_successMsg = page.getByText("Group added successfully");
    this.group_creation_duplicateMsg = page.getByText("Group name already in use");
  }
}
