import { test, expect, Page } from "@playwright/test";
import * as path from "path";
import { LoginPage } from "../../pages/LoginPage";
import { NubiLocators } from "./nubiLocators";
import { GlobalContextLocators } from "./globalContextLocators";
import { waitForGraphQLAndValidate } from "../utils/GraphQLNetworkWatcher";

function gcName(suffix: string): string {
  return `auto_gc_${suffix}`;
}

const FIXTURE_DIR = path.join(__dirname, "fixtures");
const SAMPLE_TXT = path.join(FIXTURE_DIR, "sample-context.txt");

async function setup(page: Page) {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const gc = new GlobalContextLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await gc.navigateToGlobalContext(nubi);

  return { gc };
}

async function ensureEmptyState(gc: GlobalContextLocators): Promise<void> {
  const exists = await gc.threeDotsMenuBtn.isVisible().catch(() => false);
  if (exists) {
    await gc.deleteAndVerify();
  }
}

async function createGC(
  gc: GlobalContextLocators,
  page: Page,
  name: string,
  content: string,
  description?: string
): Promise<void> {
  await gc.openCreateModal();
  await gc.fillForm(name, content, description);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await gc.createBtn.click();
      await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
    },
    {
      testName: `Setup: create GC '${name}'`,
      operationNames: ["CreateGlobalContext"],
      timeoutMs: 30000,
    }
  );
}

test.describe("Nubi Global Context - Navigation & Rendering", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Navigation - All Nubi Settings tabs render correctly", async ({ page }) => {
await setup(page);

    const tabs = [
      "#settings-tab-agents",
      "#settings-tab-tools",
      "#settings-tab-consumption",
      "#settings-tab-global-context",
      "#settings-tab-knowledge-base",
      "#settings-tab-memory",
      "#settings-tab-rca-format",
    ];

    for (const tabId of tabs) {
      await expect(page.locator(tabId)).toBeVisible({ timeout: 10000 });
    }
    console.log("A1 passed: all Settings tabs visible");
  });

  test("Nubi Global Context - Navigation - Tab shows correct header description text", async ({ page }) => {
const { gc } = await setup(page);

    await expect(
      page.getByText(
        "Account-level rules and identity that define how your AI debugger/planner behaves and reasons - set once, applies to all sessions."
      )
    ).toBeVisible({ timeout: 10000 });
    await expect(gc.addGlobalContextBtn).toBeVisible();
    console.log("A2 passed: header description is correct");
  });

  test("Nubi Global Context - Navigation - Empty state message and Create button visible when no context exists", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);

    await expect(gc.emptyStateTitle).toBeVisible();
    await expect(gc.createGlobalContextEmptyBtn).toBeVisible();
    console.log("A3 passed: empty state displays correctly");
  });

  test("Nubi Global Context - Navigation - Add button disabled when one Global Context already exists", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("a4"), "Content for A4 test.");

    await expect(gc.addGlobalContextBtn).toBeDisabled();

    await gc.deleteAndVerify();
    console.log("A4 passed: Add button disabled when GC exists");
  });
});

test.describe("Nubi Global Context - Create Happy Path", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Create - Card appears with correct name after successful creation", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.fillForm(gcName("b1"), "B1 functional test content.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.createBtn.click();
        await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "B1: Create GC",
        operationNames: ["CreateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.getContextCardByName(gcName("b1"))).toBeVisible({ timeout: 10000 });
    await expect(gc.emptyStateTitle).not.toBeVisible();

    await gc.deleteAndVerify();
    console.log("B1 passed");
  });

  test("Nubi Global Context - Create - Description is visible on card when provided during creation", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.fillForm(gcName("b2"), "B2 content with description.", "B2 description text");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.createBtn.click();
        await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "B2: Create GC with description",
        operationNames: ["CreateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    const card = gc.getContextCardByName(gcName("b2"));
    await expect(card).toBeVisible({ timeout: 10000 });
    await expect(card).toContainText("B2 description text");

    await gc.deleteAndVerify();
    console.log("B2 passed");
  });

  test("Nubi Global Context - Create - Character count updates live as user types in content field", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();

    await gc.nameInput.fill(gcName("b3"));
    await gc.contentTextarea.fill("Hello");
    await expect(gc.characterCountLabel).toContainText("5 characters");

    await gc.contentTextarea.fill("Hello World");
    await expect(gc.characterCountLabel).toContainText("11 characters");

    await gc.formCancelBtn.click();
    console.log("B3 passed: character count updates correctly");
  });

  test("Nubi Global Context - Create - File upload populates content textarea and creates context successfully", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.nameInput.fill(gcName("b4"));

    await gc.fileInput.setInputFiles(SAMPLE_TXT);

    await expect(gc.contentTextarea).not.toHaveValue("", { timeout: 5000 });

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.createBtn.click();
        await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "B4: Create GC via file upload",
        operationNames: ["CreateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.getContextCardByName(gcName("b4"))).toBeVisible({ timeout: 10000 });

    await gc.deleteAndVerify();
    console.log("B4 passed: file upload populates content and creates GC");
  });
});

test.describe("Nubi Global Context - Create Validation & Error Paths", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Validation - Create button stays disabled until name field is filled", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();

    await expect(gc.createBtn).toBeDisabled();

    await gc.nameInput.fill("valid_name");
    await expect(gc.createBtn).toBeEnabled();

    await gc.formCancelBtn.click();
    console.log("C1 passed: Create button disabled without name");
  });

  test("Nubi Global Context - Validation - Submitting with empty content field shows backend error", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.nameInput.fill(gcName("c2"));

    await gc.createBtn.click();
    await expect(
      page.getByText(/please provide content|content.*required/i)
    ).toBeVisible({ timeout: 10000 });

    await gc.formCancelBtn.click();
    console.log("C2 passed: error shown when content is empty");
  });

  test("Nubi Global Context - Validation - Special characters in name field trigger invalid name error", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.nameInput.fill("invalid@name!");
    await gc.contentTextarea.fill("Some valid content here.");

    await gc.createBtn.click();
    await expect(
      page.getByText(/invalid characters|Use only letters/i)
    ).toBeVisible({ timeout: 10000 });

    await gc.formCancelBtn.click();
    console.log("C3 passed: invalid name characters show error");
  });

  test("Nubi Global Context - Validation - Cancel closes create modal without saving any data", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.fillForm(gcName("c4"), "Content that should not be saved.");

    await gc.formCancelBtn.click();

    await expect(gc.nameInput).not.toBeVisible({ timeout: 5000 });
    await expect(gc.emptyStateTitle).toBeVisible({ timeout: 5000 });
    console.log("C4 passed: Cancel closes modal, no GC created");
  });

  test("Nubi Global Context - Validation - Uploading non-txt file shows frontend file type error", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();

    await gc.fileInput.setInputFiles({
      name: "document.pdf",
      mimeType: "application/pdf",
      buffer: Buffer.from("fake pdf content"),
    });

    await expect(
      page.getByText(/Please select only .txt files/i)
    ).toBeVisible({ timeout: 5000 });

    await gc.formCancelBtn.click();
    console.log("C5 passed: non-.txt file upload shows error");
  });
});

test.describe("Nubi Global Context - Edit", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Edit - Modal pre-populates all fields with existing context values", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("d1"), "D1 original content.", "D1 original description");

    await gc.openEditModal();

    await expect(gc.nameInput).toHaveValue(gcName("d1"));
    await expect(gc.descriptionInput).toHaveValue("D1 original description");
    await expect(gc.contentTextarea).toHaveValue("D1 original content.");

    await gc.formCancelBtn.click();

    await gc.deleteAndVerify();
    console.log("D1 passed: edit modal pre-populates all fields");
  });

  test("Nubi Global Context - Edit - Updated name and content reflect on card after saving changes", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("d2"), "D2 original content.");

    await gc.openEditModal();
    await gc.nameInput.fill(gcName("d2_updated"));
    await gc.contentTextarea.fill("D2 updated content.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.updateBtn.click();
        await expect(gc.successUpdated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "D2: Edit GC",
        operationNames: ["UpdateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.getContextCardByName(gcName("d2_updated"))).toBeVisible({ timeout: 10000 });
    await expect(gc.getContextCardByName(gcName("d2"))).not.toBeVisible();

    await gc.deleteAndVerify();
    console.log("D2 passed: card shows updated name after edit");
  });

  test("Nubi Global Context - Edit - Cancel discards changes and preserves original context card", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("d3"), "D3 original content.");

    await gc.openEditModal();
    await gc.nameInput.fill("should_not_be_saved");
    await gc.formCancelBtn.click();

    await expect(gc.getContextCardByName(gcName("d3"))).toBeVisible({ timeout: 5000 });

    await gc.deleteAndVerify();
    console.log("D3 passed: Cancel in edit keeps original data");
  });

  test("Nubi Global Context - Edit - Opening edit modal fires GetGlobalContext API to fetch full content", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("d4"), "D4 content to verify on edit.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.openEditModal();
      },
      {
        testName: "D4: Edit opens GetGlobalContext",
        operationNames: ["GetGlobalContext"],
        timeoutMs: 15000,
      }
    );

    await gc.formCancelBtn.click();

    await gc.deleteAndVerify();
    console.log("D4 passed: GetGlobalContext called when opening edit modal");
  });
});

test.describe("Nubi Global Context - Delete", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Delete - Confirmation modal displays correct context name", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("e1"), "E1 content.");

    await gc.openDeleteModal();

    await expect(
      page.getByText(new RegExp(`Delete Global Context.*${gcName("e1")}`, "i"))
    ).toBeVisible({ timeout: 5000 });

    await gc.deleteCancelBtn.click();

    await gc.deleteAndVerify();
    console.log("E1 passed: delete modal shows correct GC name");
  });

  test("Nubi Global Context - Delete - Confirmation modal shows irreversibility warning message", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("e2"), "E2 content.");

    await gc.openDeleteModal();
    await expect(
      page.getByText(/This action cannot be undone/i)
    ).toBeVisible({ timeout: 5000 });

    await gc.deleteCancelBtn.click();

    await gc.deleteAndVerify();
    console.log("E2 passed: delete modal shows irreversibility warning");
  });

  test("Nubi Global Context - Delete - Cancel on delete modal keeps context card intact", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("e3"), "E3 content.");

    await gc.openDeleteModal();
    await gc.deleteCancelBtn.click();

    await expect(gc.getContextCardByName(gcName("e3"))).toBeVisible({ timeout: 5000 });

    await gc.deleteAndVerify();
    console.log("E3 passed: Cancel delete keeps the card");
  });

  test("Nubi Global Context - Delete - Confirm delete restores empty state and re-enables Add button", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("e4"), "E4 content.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.openDeleteModal();
        await gc.deleteConfirmBtn.click();
        await expect(gc.successDeleted).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "E4: Delete GC",
        operationNames: ["DeleteGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await expect(gc.emptyStateTitle).toBeVisible({ timeout: 10000 });
    await expect(gc.addGlobalContextBtn).toBeEnabled();
    console.log("E4 passed: empty state restored after delete");
  });
});

test.describe("Nubi Global Context - Card Display", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - Card - Shows context name with Updated and Created timestamps", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("f1"), "F1 display test content.");

    const card = gc.getContextCardByName(gcName("f1"));
    await expect(card).toBeVisible({ timeout: 10000 });
    await expect(card).toContainText("Updated");
    await expect(card).toContainText("Created");

    await gc.deleteAndVerify();
    console.log("F1 passed: card shows all required display fields");
  });

  test("Nubi Global Context - Card - Three-dots menu contains Edit and Delete options", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("f2"), "F2 menu test content.");

    await gc.threeDotsMenuBtn.click();
    await expect(gc.editMenuItem).toBeVisible({ timeout: 5000 });
    await expect(gc.deleteMenuItem).toBeVisible({ timeout: 5000 });

    await page.keyboard.press("Escape");

    await gc.deleteAndVerify();
    console.log("F2 passed: three-dots menu has Edit and Delete");
  });
});

test.describe("Nubi Global Context - GraphQL API Validation", () => {
  test.describe.configure({ mode: "serial" });

  test("Nubi Global Context - GraphQL - Opening tab fires ListGlobalContexts query", async ({ page }) => {
const loginPage = new LoginPage(page);
    const nubi = new NubiLocators(page);
    const gc = new GlobalContextLocators(page);

    await loginPage.doFullLogin();
    await nubi.openPanel();
    await nubi.settingsBtn.click();

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.globalContextTab.waitFor({ state: "visible", timeout: 10000 });
        await gc.globalContextTab.click();
        await page.waitForLoadState("networkidle");
      },
      {
        testName: "G1: ListGlobalContexts on tab open",
        operationNames: ["ListGlobalContexts"],
        timeoutMs: 20000,
      }
    );

    console.log("G1 passed: ListGlobalContexts fired on tab open");
  });

  test("Nubi Global Context - GraphQL - Create action fires CreateGlobalContext mutation", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await gc.openCreateModal();
    await gc.fillForm(gcName("g2"), "G2 GraphQL validation content.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.createBtn.click();
        await expect(gc.successCreated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "G2: CreateGlobalContext mutation",
        operationNames: ["CreateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await gc.deleteAndVerify();
    console.log("G2 passed: CreateGlobalContext mutation captured");
  });

  test("Nubi Global Context - GraphQL - Saving edit fires UpdateGlobalContext mutation", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("g3"), "G3 original content.");

    await gc.openEditModal();
    await gc.contentTextarea.fill("G3 updated content.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.updateBtn.click();
        await expect(gc.successUpdated).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "G3: UpdateGlobalContext mutation",
        operationNames: ["UpdateGlobalContext"],
        timeoutMs: 30000,
      }
    );

    await gc.deleteAndVerify();
    console.log("G3 passed: UpdateGlobalContext mutation captured");
  });

  test("Nubi Global Context - GraphQL - Confirm delete fires DeleteGlobalContext mutation", async ({ page }) => {
const { gc } = await setup(page);

    await ensureEmptyState(gc);
    await createGC(gc, page, gcName("g4"), "G4 content for delete validation.");

    await waitForGraphQLAndValidate(
      page,
      async () => {
        await gc.openDeleteModal();
        await gc.deleteConfirmBtn.click();
        await expect(gc.successDeleted).toBeVisible({ timeout: 20000 });
      },
      {
        testName: "G4: DeleteGlobalContext mutation",
        operationNames: ["DeleteGlobalContext"],
        timeoutMs: 30000,
      }
    );

    console.log("G4 passed: DeleteGlobalContext mutation captured");
  });
});
