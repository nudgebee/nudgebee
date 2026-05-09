import { test } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { users } from "./usersConstants";
import { UserLocators } from "./usersLocators";

test("Add multiple users from list", async ({ page }) => {
  test.setTimeout(180000);

  const loginPage = new LoginPage(page);
  const locators = new UserLocators(page);
  await loginPage.doFullLogin();
  await locators.homeBtn.click();
  await locators.adminBtn.click();
  await page.waitForURL("**/user-management**", { timeout: 15000 });
  await locators.addNewUserBtn.waitFor({ state: "visible", timeout: 15000 });

  for (const user of users) {
    await test.step(`Processing: ${user.email}`, async () => {
      await locators.addNewUserBtn.click();
      await locators.firstNameInput.fill(user.first);
      await locators.lastNameInput.fill(user.last);
      await locators.emailInput.fill(user.email);

      await locators.tenantRoleCombobox.click();
      await locators.getRoleOption(user.role).click();

      await locators.addUserSubmitBtn.click();

      const result = await Promise.race([
        locators.successMsg
          .waitFor({ state: "visible", timeout: 8000 })
          .then(() => "success"),
        locators.duplicateMsg
          .waitFor({ state: "visible", timeout: 8000 })
          .then(() => "duplicate"),
      ]).catch(() => "timeout");

      if (result === "success") {
        console.log(`SUCCESS: User "${user.email}" added successfully.`);
        await locators.addUserSubmitBtn.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
      }
      else if (result === "duplicate") {
        console.log(`DUPLICATE: User "${user.email}" already exists.`);
        await locators.cancelBtn.click();
      } 
      else {
        console.log(`ERROR: Timeout for "${user.email}". No response notification found.`);
        if (await locators.cancelBtn.isVisible()) {
          await locators.cancelBtn.click();
        }
      }
      await page.waitForTimeout(500); 
    });
  }
});