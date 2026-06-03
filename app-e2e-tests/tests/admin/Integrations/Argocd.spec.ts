import { test } from "@playwright/test";
import { navigateToCicdTab, testConnection, saveAndHandleAlreadyExists } from "./util";

const requiredEnv = [
  "ARGOCD_INTEGRATION_CONFIG_NAME",
  "ARGOCD_SECRET",
  "ARGOCD_SERVER",
];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);

test("Add Argocd Account Integration", async ({ page }) => {
  test.setTimeout(180000);
  test.skip(
    missingEnv.length > 0,
    `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
  );
  const locators = await navigateToCicdTab(page);

  await locators.argocdBtn.click();
  await locators.addArgocdAccountBtn.click();

  await locators.argocdConfigNameInput.fill(process.env.ARGOCD_INTEGRATION_CONFIG_NAME!);
  await locators.argocdServerInput.fill(process.env.ARGOCD_SERVER!);
  await locators.argocdK8sSecretInput.fill(process.env.ARGOCD_SECRET!);
  await locators.argocdAccountIdDropdown.click();
  await locators.argocdAccountIdOption(process.env.CLUSTER!).first().click();
  await locators.argocdAccountIdDropdown.press("Escape");

  const connected = await testConnection(page, {
    testConnectionBtn: locators.argocdTestConnectionBtn,
    successToast: locators.argocdTestConnectionSuccessToast,
    serviceName: "ArgoCD",
    saveBtn: locators.saveBtn,
    operationNames: ["TestIntegrationConnectionConfig"],
    skipOnBackendError: true,
    checkDataErrors: true,
    timeout: 20000,
  });
  if (!connected) return;

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.argocdSuccessToast,
    testName: "Add Argocd Account Integration",
    operationNames: ["AddIntegrations"],
    ignoreErrorMessages: ["already exists", "already has"],
  });
});
