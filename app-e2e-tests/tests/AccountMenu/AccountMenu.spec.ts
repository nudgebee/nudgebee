import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { AccountMenuLocators } from "./accountMenuLocators";

test.describe("Account Menu", () => {
  test(
    "Account Menu - open the user menu and verify the displayed app Version is not blank, N/A, or 0.0.0",
    { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] },
    async ({ page }) => {
      test.setTimeout(60000);
      const loginPage = new LoginPage(page);
      const locators = new AccountMenuLocators(page);
      await loginPage.doFullLogin();

      await locators.accountSettingBtn.click();
      await expect(locators.userMenu).toBeVisible({ timeout: 10000 });
      await expect(locators.versionMenuItem).toBeVisible({ timeout: 10000 });

      // toHaveText retries until the assertion passes or times out, instead of a
      // one-shot textContent() read that could race the version text populating.
      await expect(locators.versionMenuItem).not.toHaveText(/^Version:\s*$/);
      await expect(locators.versionMenuItem).not.toHaveText("Version: N/A");
      await expect(locators.versionMenuItem).not.toHaveText("Version: 0.0.0");

      const version = ((await locators.versionMenuItem.textContent()) || "").replace("Version:", "").trim();
      console.log(`[account-menu] displayed version: "${version}"`);
    }
  );
});
