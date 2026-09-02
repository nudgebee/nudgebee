import { test } from "@playwright/test";
import {
  checkPostgresIntegrationExists,
  deletePostgresIntegration,
  addPostgresIntegration,
  disablePostgresIntegration,
  enablePostgresIntegration,
} from "./postgresHelper";

// Each test is one step from postgresHelper. The same steps are re-used by
// journeys that walk several areas of the app in a single login — see
// tests/journeys/PostgresIntegrationNubi.spec.ts.

// CLUSTER is required too: the Add step picks the account by that name, and
// without it the form silently receives undefined rather than skipping.
const requiredEnv = ["POSTGRES_NAME", "POSTGRES_SECRET", "CLUSTER"];
const missingEnv = requiredEnv.filter((key) => !process.env[key]);
const configName = process.env.POSTGRES_NAME ?? "";

test.describe.serial("Postgresql Account Integration", () => {
  let integrationExists = false;

  test.beforeEach(() => {
    test.skip(
      missingEnv.length > 0,
      `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`,
    );
  });

  test("Check if Postgresql integration exists", async ({ page }) => {
    integrationExists = await checkPostgresIntegrationExists(page, configName);
  });

  test("Delete Postgresql integration if present", async ({ page }) => {
    test.skip(!integrationExists, "Postgresql integration not present — nothing to delete");
    await deletePostgresIntegration(page, configName);
  });

  test("Add Postgresql Account Integration", async ({ page }) => {
    await addPostgresIntegration(page, {
      configName,
      secret: process.env.POSTGRES_SECRET ?? "",
      cluster: process.env.CLUSTER ?? "",
    });
  });

  test("Disable Postgresql integration", async ({ page }) => {
    await disablePostgresIntegration(page, configName);
  });

  test("Enable Postgresql integration", async ({ page }) => {
    await enablePostgresIntegration(page, configName);
  });
});
