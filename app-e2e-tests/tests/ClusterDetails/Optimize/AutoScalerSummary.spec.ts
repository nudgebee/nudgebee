import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { OptimizeTabLocator } from "./OptimizeTabLocator";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

test("API testing Cluster Details->Optimize-> AutoScaler-> Summary", async ({ page }, testInfo) => {
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
  await locators.Summary.click();

  // API validation is pending 
  

  // await waitForGraphQLAndValidate(
  //   page,
  //   async () => {
  //     // The trigger action
  //     await locators.Summary.click();
  //   },
  //   {
  //     testName: testInfo.title, 
  //     urlContains: "api/graphql", // Adjust if your endpoint is different
  //     operationNames: [
  //       "GetAutoscalerSummary", 
  //       "GetAutoscalerMetrics",
  //       "GetClusterStatus"
  //     ],
  //     timeoutMs: 60000 
  //   }
  // );
  // await expect(page.getByText("Summary Data Loaded")).toBeVisible(); 
});