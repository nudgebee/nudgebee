import { Page } from "@playwright/test";
import {
  navigateToDatabaseTab,
  testConnection,
  saveAndHandleAlreadyExists,
  isIntegrationPresent,
  deleteIntegration,
  disableIntegration,
  enableIntegration,
} from "./util";

// The Postgres integration steps, extracted from Postgresql.spec.ts so both that
// spec and multi-area journeys can run them. Each function is one step: it
// assumes the caller is logged in, and never calls test()/test.step() itself, so
// it composes either as a standalone test body or as a stage inside a journey.

const SERVICE_NAME = "Postgresql";
const STATUS_FILTER_ID = "postgres-status-filter";

export interface PostgresConfig {
  configName: string;
  /** Name of the K8s secret holding the connection details. */
  secret: string;
  /** Cloud/cluster account the integration is attached to. */
  cluster: string;
}

/** Opens the Postgres list and reports whether `configName` exists, at any status. */
export async function checkPostgresIntegrationExists(page: Page, configName: string): Promise<boolean> {
  const locators = await navigateToDatabaseTab(page);
  return isIntegrationPresent(page, {
    openList: async () => {
      await locators.postgresqlBtn.click();
    },
    configName,
    serviceName: SERVICE_NAME,
    statusFilterId: STATUS_FILTER_ID,
  });
}

export async function deletePostgresIntegration(page: Page, configName: string): Promise<void> {
  const locators = await navigateToDatabaseTab(page);
  await deleteIntegration(page, {
    openList: async () => {
      await locators.postgresqlBtn.click();
    },
    configName,
    serviceName: SERVICE_NAME,
    statusFilterId: STATUS_FILTER_ID,
  });
}

/** Fills the Add Postgres form, tests the connection, and saves. */
export async function addPostgresIntegration(page: Page, { configName, secret, cluster }: PostgresConfig): Promise<void> {
  const locators = await navigateToDatabaseTab(page);

  await locators.postgresqlBtn.click();
  await locators.addPostgresqlAccountBtn.click();

  await locators.postgresqlConfigNameInput.fill(configName);
  await locators.postgresqlAccountIdDropdown.click();
  await locators.postgresqlAccountIdOption(cluster).first().click();
  await locators.postgresqlAccountIdDropdown.press("Escape");
  await locators.postgresqlK8sSecretInput.fill(secret);

  await testConnection(page, {
    testConnectionBtn: locators.postgresqlTestConnectionBtn,
    successToast: locators.postgresqlTestConnectionSuccessToast,
    serviceName: SERVICE_NAME,
    saveBtn: locators.saveBtn,
    operationNames: ["TestIntegrationConnectionConfig"],
    checkDataErrors: true,
  });

  await saveAndHandleAlreadyExists(page, {
    saveBtn: locators.saveBtn,
    successToast: locators.postgresqlSuccessToast,
    testName: "Add Postgresql Account Integration",
    operationNames: ["AddIntegrations"],
  });
}

export async function disablePostgresIntegration(page: Page, configName: string): Promise<void> {
  const locators = await navigateToDatabaseTab(page);
  await locators.postgresqlBtn.click();
  await disableIntegration(page, { configName, serviceName: SERVICE_NAME });
}

export async function enablePostgresIntegration(page: Page, configName: string): Promise<void> {
  const locators = await navigateToDatabaseTab(page);
  await locators.postgresqlBtn.click();
  await enableIntegration(page, { configName, serviceName: SERVICE_NAME, statusFilterId: STATUS_FILTER_ID });
}
