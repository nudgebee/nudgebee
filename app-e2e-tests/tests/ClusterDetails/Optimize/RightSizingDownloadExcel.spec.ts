import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator } from "./OptimizeTabLocator";
import { assert } from "console";

test("Download Excel from Right Sizing tab", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new OptimizeTabLocator(page);

  await loginPage.doFullLogin();
  await locators.openClusterFromConfig();

  // Ye step tab tak rukega jab tak element stable nahi ho jata
  await locators.OptimizeTab.waitFor({ state: "visible" });
  await locators.OptimizeTab.hover();
  await expect(locators.OptimizedropdownRightSizeButton).toBeVisible();
  await locators.OptimizedropdownRightSizeButton.click();

  await expect(locators.RightSizingTab).toBeVisible();
  await locators.RightSizingTab.click();

  await expect(locators.DownlaodBtn).toBeVisible();
  await locators.DownlaodBtn.click();

  await expect(locators.DownloadExcelBtn).toBeVisible();
  await locators.DownloadExcelBtn.click();
  await assert(await locators.DownloadExcelSuccessMaggage.isVisible());

});
