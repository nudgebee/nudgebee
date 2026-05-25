import { test, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { GlobalContextLocators } from "./globalContextLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

const GC_NAME = "automation_sanity_gc";
const GC_CONTENT = "This is an automated sanity test context. Used by QA automation only.";
const GC_DESCRIPTION = "Sanity test global context";
const GC_UPDATED_NAME = "automation_sanity_gc_updated";
const GC_UPDATED_CONTENT = "Updated content for sanity test. QA automation only.";

test("Nubi Global Context - Sanity - Full CRUD flow Create Edit Delete", async ({ page }) => {
  test.setTimeout(300000);

  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const gc = new GlobalContextLocators(page);

  await loginPage.doFullLogin();
  await expect(nubi.askNudgebeeBtn).toBeVisible();
  await nubi.openPanel();

  await test.step("S1: Global Context tab loads and shows correct state", async () => {
    await expect(nubi.settingsBtn).toBeVisible();
    await gc.navigateToGlobalContext(nubi);

    await expect(gc.addGlobalContextBtn).toBeVisible({ timeout: 10000 });

    // If a leftover GC exists from a previous failed run, delete it first
    const cardExists = await gc.threeDotsMenuBtn.isVisible().catch(() => false);
    if (cardExists) {
      console.log("Found existing GC from previous run — cleaning up");
      await gc.deleteAndVerify();
    }

    await expect(gc.emptyStateTitle).toBeVisible({ timeout: 10000 });
    console.log("S1 passed: empty state is displayed");
  });

  await test.step("S2: Create Global Context with name, description, and content", async () => {
    await gc.openCreateModal();

    await gc.fillForm(GC_NAME, GC_CONTENT, GC_DESCRIPTION);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.createBtn.click();
        await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "Global Context Sanity - Create",
        operationNames: ["CreateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.getContextCardByName(GC_NAME)).toBeVisible({ timeout: 10000 });
    await expect(gc.emptyStateTitle).not.toBeVisible();
    await expect(gc.addGlobalContextBtn).toBeDisabled();
    console.log(`S2 passed: GC '${GC_NAME}' created and card is visible`);
  });

  await test.step("S3: Edit Global Context — update name and content", async () => {
    await gc.openEditModal();

    await expect(gc.nameInput).toHaveValue(GC_NAME);

    await gc.nameInput.fill(GC_UPDATED_NAME);
    await gc.contentTextarea.fill(GC_UPDATED_CONTENT);

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.updateBtn.click();
        await expect(gc.successUpdated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "Global Context Sanity - Update",
        operationNames: ["UpdateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.getContextCardByName(GC_UPDATED_NAME)).toBeVisible({ timeout: 10000 });
    await expect(gc.getContextCardByName(GC_NAME)).not.toBeVisible();
    console.log(`S3 passed: GC renamed to '${GC_UPDATED_NAME}'`);
  });

  await test.step("S4: Delete Global Context and verify empty state returns", async () => {
    await gc.openDeleteModal();

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.deleteConfirmBtn.click();
        await expect(gc.successDeleted).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "Global Context Sanity - Delete",
        operationNames: ["DeleteGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.emptyStateTitle).toBeVisible({ timeout: 10000 });
    await expect(gc.addGlobalContextBtn).toBeEnabled();
    console.log("S4 passed: GC deleted, empty state restored");
  });
});
