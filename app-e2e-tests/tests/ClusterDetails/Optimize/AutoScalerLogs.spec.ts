import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator } from "./OptimizeTabLocator";


test("API testing Cluster Details->Optimize-> AutoScaler-> Logs", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();

  await locators.OptimizeTab.waitFor({ state: "visible" });
  await locators.OptimizeTab.hover();
  await expect(locators.OptimizedropdownRightSizeButton).toBeVisible();
  await locators.OptimizedropdownRightSizeButton.click();
  await page.waitForTimeout(3000);
  await expect(locators.AutoScalerTab).toBeVisible();
  await locators.AutoScalerTab.click();
  await locators.Logs.click();

  //Api Validation is pending
});

