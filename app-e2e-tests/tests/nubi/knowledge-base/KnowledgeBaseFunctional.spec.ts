import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { KnowledgeBaseLocators } from "./knowledgeBaseLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

const KB_NAME = "auto_kb_e2e_test";

test.describe("Knowledge Base", () => {
  test.describe.configure({ mode: "serial" });

  test("Clicking Knowledge Base tab API validation", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await nubi.settingsBtn.click();

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await kb.knowledgeBaseTab.waitFor({ state: "visible", timeout: 10000 });
        await kb.knowledgeBaseTab.click();
        await page.waitForLoadState("networkidle");
      },
      {
        testName: "Clicking Knowledge Base tab API validation",
        operationNames: [],
        timeoutMs: 20000,
      }
    );
  });


  test("Add Knowledge Base button is visible", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);

    await expect(kb.addKBBtn.first()).toBeVisible({ timeout: 10000 });
    console.log("Add Knowledge Base button is visible");
  });

  test("Create Knowledge Base and shows success snackbar", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);
    await kb.navigateToUserTab();
    const existingCard = kb.getKBCardByName(KB_NAME);
    if (await existingCard.isVisible().catch(() => false)) {
      await kb.deleteKBByName(KB_NAME);
    }

    await kb.openCreateModal();
    await kb.fillForm(
      KB_NAME,
      "E2E test content for knowledge base functional testing.",
      "E2E test KB description"
    );

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await kb.createBtn.click();
        await expect(kb.successCreated.first()).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "Create Knowledge Base and shows success snackbar",
        operationNames: ["CreateKnowledgeBase"],
        timeoutMs: 30000,
      }
    );
  });


  test("Hovering info button on KB form shows tooltip", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);

    await kb.openCreateModal();
    await kb.infoBtn.hover();
    await expect(kb.infoTooltip).toBeVisible({ timeout: 5000 });

    await kb.formCancelBtn.click();
    console.log("Hovering info button on KB form shows tooltip");
  });


  test("Creating KB with invalid name shows validation error snackbar", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);

    await kb.openCreateModal();
    await kb.nameInput.fill("1invalid_name");
    await kb.createBtn.click();

    await expect(
      page.getByText(/Name must start with a letter/i)
    ).toBeVisible({ timeout: 10000 });

    await kb.formCancelBtn.click();
    console.log("Creating KB with invalid name shows validation error snackbar");
  });


  test("User tab shows created KB card after ListKnowledgeBases query", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await nubi.settingsBtn.click();
    await waitForGraphQLAndValidate(
      page,
      async () => {
        await kb.knowledgeBaseTab.waitFor({ state: "visible", timeout: 10000 });
        await kb.knowledgeBaseTab.click();
        await page.waitForLoadState("networkidle");
      },
      {
        testName: "User tab shows created KB card after ListKnowledgeBases query",
        operationNames: [],
        timeoutMs: 20000,
      }
    );

    await kb.navigateToUserTab();
    await expect(kb.getKBCardByName(KB_NAME)).toBeVisible({ timeout: 15000 });
    console.log("User tab shows created KB after ListKnowledgeBases");
  });

  test("Editing KB-> UpdateKnowledgeBase and shows success snackbar", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);
    await kb.navigateToUserTab();

    await kb.clickEditForCard(KB_NAME);
    await kb.descriptionInput.fill("Updated E2E test description");
    await kb.contentTextarea.fill("Updated E2E test content for edit validation.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await kb.updateBtn.click();
        await expect(kb.successUpdated.first()).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "UpdateKnowledgeBase mutation",
        operationNames: ["UpdateKnowledgeBase"],
        timeoutMs: 30000,
      }
    );
    console.log("UpdateKnowledgeBase mutation passed: KB edited, mutation captured, success snackbar shown");
  });


  test("Three-dot menu Load History option opens history panel", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);
    await kb.navigateToUserTab();

    await kb.clickThreeDotsForCard(KB_NAME);
    await kb.loadHistoryMenuItem.first().waitFor({ state: "visible", timeout: 5000 });
    await kb.loadHistoryMenuItem.first().click();

    await expect(
      page.getByText(/Load History/i).first()
    ).toBeVisible({ timeout: 10000 });

    await page.keyboard.press("Escape");
    console.log("Three-dot menu Load History option opens history panel");
  });

  test("Deleting KB -> shows success snackbar", async ({ page }) => {
    const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const kb = new KnowledgeBaseLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await kb.navigateToKnowledgeBase(nubi);
    await kb.navigateToUserTab();

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await kb.openDeleteForCard(KB_NAME);
        await kb.deleteConfirmBtn.first().click();
        await expect(kb.successDeleted.first()).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "DeleteKnowledgeBase mutation",
        operationNames: ["DeleteKnowledgeBase"],
        timeoutMs: 30000,
      }
    );
    console.log("DeleteKnowledgeBase mutation passed: KB deleted, mutation captured, success snackbar shown");
  });
});
