import { test } from "@playwright/test";
import { navigateToInMemoryTab, saveAndHandleAlreadyExists } from "./util";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

const requiredEnv = ["REDIS_INTEGRATION_CONFIG_NAME", "REDIS_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add Redis Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToInMemoryTab(page);

  await locators.redisBtn.click();
  await locators.addRedisAccountBtn.click();

  await locators.redisConfigNameInput.fill(process.env.REDIS_INTEGRATION_CONFIG_NAME!);
  await locators.redisAccountIdDropdown.click();
  await locators.redisAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.redisAccountIdDropdown.press("Escape");
  await locators.redisK8sSecretInput.fill(process.env.REDIS_SECRET!);

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.redisTestConnectionBtn.click();
      await locators.saveBtn.waitFor({ state: "attached" });
      await locators.saveBtn.isEnabled();
      console.log("Test connection SUCCESS: save button is now enabled");
    },
    {
      testName: "Add Redis Account Integration - Test Connection",
      operationNames: ["TestIntegrationConnectionConfig"],
      checkDataErrors: true,
    },
  );

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.redisSuccessToast,
    testName: "Add Redis Account Integration",
    operationNames: ["AddIntegrations"],
    ignoreErrorMessages: ["already has a 'redis' integration"],
  });
});
