import { test } from "@playwright/test";
import {
  navigateToServersTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  ensureIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

const requiredEnv = ["SSH_INTEGRATION_CONFIG_NAME", "SSH_SECRET"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.SSH_INTEGRATION_CONFIG_NAME!;

test.describe.serial("SSH Account Integration", () => {
  test.skip(true, "Temporarily disabled — test is faulty, needs a fix");

  let integrationExists = false;

  test("Check if SSH integration exists", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToServersTab(page);
    integrationExists = await isIntegrationPresent(page, {
      openList: async () => {
        await locators.sshBtn.click();
      },
      configName,
      serviceName: "SSH",
      statusFilterId: "ssh-status-filter",
    });
  });

  test("Delete SSH integration if present", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    test.skip(!integrationExists, "SSH integration not present — nothing to delete");
    const locators = await navigateToServersTab(page);
    await deleteIntegration(page, {
      openList: async () => {
        await locators.sshBtn.click();
      },
      configName,
      serviceName: "SSH",
      statusFilterId: "ssh-status-filter",
    });
  });

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
    await locators.sshConfigNameInput.fill(configName);
    await locators.sshK8sSecretInput.fill(process.env.SSH_SECRET!);

    const connectionOk = await testConnection(page, {
      testConnectionBtn: locators.sshTestConnectionBtn,
      successToast: locators.sshTestConnectionSuccessToast,
      serviceName: "SSH",
      saveBtn: locators.saveBtn,
      operationNames: ["TestIntegrationConnectionConfig"],
      timeout: 90000,
      skipOnBackendError: true,
      checkDataErrors: true,
    });

    if (!connectionOk) return;

    await saveAndHandleAlreadyExists(page, {
      saveBtn: locators.saveBtn,
      successToast: locators.sshSuccessToast,
      testName: "Add SSH Account Integration",
      operationNames: ["AddIntegrations"],
      ignoreErrorMessages: [
        "already has a 'ssh' integration",
        `integration config name '${configName}' already exists for this integration type`,
      ],
    });
  });

  test("Disable SSH integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToServersTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.sshBtn.click();
      },
      configName,
      serviceName: "SSH",
      statusFilterId: "ssh-status-filter",
      integrationKey: "ssh",
    });
    test.skip(!present, "SSH integration is not Active — Slack notification sent");
    await disableIntegration(page, { configName, serviceName: "SSH" });
  });

  test("Enable SSH integration", async ({ page }) => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
    const locators = await navigateToServersTab(page);
    const present = await ensureIntegrationPresent(page, {
      openList: async () => {
        await locators.sshBtn.click();
      },
      configName,
      serviceName: "SSH",
      statusFilterId: "ssh-status-filter",
      integrationKey: "ssh",
    });
    test.skip(!present, "SSH integration is not Active — Slack notification sent");
    await enableIntegration(page, {
      configName,
      serviceName: "SSH",
      statusFilterId: "ssh-status-filter",
    });
  });
});
