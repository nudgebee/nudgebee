import { test } from "@playwright/test";
import { navigateToServersTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = ["SSH_HOST", "SSH_INTEGRATION_CONFIG_NAME", "SSH_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add SSH Account Integration", async ({ page }) => {
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToServersTab(page);

  await locators.sshBtn.click();
  await locators.addSshAccountBtn.click();

  await locators.sshAccountIdDropdown.click();
  await locators.sshAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.sshAccountIdDropdown.press("Escape");
  await locators.sshHostInput.fill(process.env.SSH_HOST!);
  await locators.sshConfigNameInput.fill(process.env.SSH_INTEGRATION_CONFIG_NAME!);
  await locators.sshK8sSecretInput.fill(process.env.SSH_SECRET!);

  await testConnection(page, {
    testConnectionBtn: locators.sshTestConnectionBtn,
    successToast: locators.sshTestConnectionSuccessToast,
    serviceName: "SSH",
    saveBtn: locators.saveBtn,
    operationNames: ["TestIntegrationConnectionConfig"],
    checkDataErrors: true,
  });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.sshSuccessToast,
    testName: "Add SSH Account Integration",
    operationNames: ["AddIntegrations"],
    ignoreErrorMessages: ["already has a 'ssh' integration"],
  });
});
