import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { Groups, GroupLocators } from "./groupLocatorsConstants";
import { CommonLocators } from "../../GlobalLocators";

test("Add User Groups", async ({ page }) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new GroupLocators(page);
  const commonLocators = new CommonLocators(page);
  await loginPage.doFullLogin();

  await expect(locators.homeBtn).toBeVisible({ timeout: 3000 });
  await locators.homeBtn.click();
  await commonLocators.adminBtn.click();

  console.log("Clicked on Admin button", commonLocators.adminBtn);
  await locators.groupsTab.click();

  await expect(locators.newUserGroupIdentifier).toBeVisible();

  for (const group of Groups) {
    await test.step(`Processing Group: ${group.name}`, async () => {
      await locators.newUserGroupIdentifier.click();

      await locators.groupNameInput.fill(group.name);
      await locators.descriptionInput.fill("Auto-test generated");

      await locators.createGroupBtn.click();

      const result = await Promise.race([
        locators.group_creation_successMsg
          .waitFor({ state: "visible", timeout: 300000 })
          .then(() => "success"),
        locators.group_creation_duplicateMsg
          .waitFor({ state: "visible", timeout: 300000 })
          .then(() => "duplicate"),
      ]);

      if (result === "success") {
        await expect(locators.group_creation_successMsg).toBeVisible();
      } else {
        await locators.cancelBtn.click();
      }
    });
  }
});
