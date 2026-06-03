import { test } from "@playwright/test";
import { navigateToDatabaseTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = ["POSTGRES_NAME", "POSTGRES_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add Postgresql Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToDatabaseTab(page);

  await locators.postgresqlBtn.click();
  await locators.addPostgresqlAccountBtn.click();

  await locators.postgresqlConfigNameInput.fill(process.env.POSTGRES_NAME!);
  await locators.postgresqlAccountIdDropdown.click();
  await locators.postgresqlAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.postgresqlAccountIdDropdown.press("Escape");
  await locators.postgresqlK8sSecretInput.fill(process.env.POSTGRES_SECRET!);

  await testConnection(page, {
    testConnectionBtn: locators.postgresqlTestConnectionBtn,
    successToast: locators.postgresqlTestConnectionSuccessToast,
    serviceName: "Postgresql",
    saveBtn: locators.saveBtn,
    operationNames: ["TestIntegrationConnectionConfig"],
    checkDataErrors: true,
  });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.postgresqlSuccessToast,
    testName: "Add Postgresql Account Integration",
    operationNames: ["AddIntegrations"],
    ignoreErrorMessages: ["already has a 'postgresql' integration"],
  });
});
