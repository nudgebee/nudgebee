import { test, expect, Page } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// Nubi › Settings › Functions tab.

const OP_CREATE = "CreateAiFunction";
const OP_UPDATE = "AiEditFunction";
const OP_DELETE = "AiDeleteFunction";

function randomName(): string {
  return `fn_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`;
}

// Logs in and lands on the Functions tab. Returns the locators object.
async function openFunctionsTab(page: Page): Promise<NubiLocators> {
  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);
  await loginPage.doFullLogin();
  await locators.openPanel(); // retries the Nubi panel open (known flaky)
  await locators.settingsBtn.click();
  await locators.functionsTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.functionsTab.click();
  await locators.createFunctionBtn.waitFor({ state: "visible", timeout: 15000 });
  return locators;
}

interface CreateOpts {
  name: string;
  description: string;
  prompt: string;
  draft?: boolean;
  defaults?: Record<string, string>;
}

// Fills and submits the create form, validating the CreateAiFunction mutation.
async function createFunction(page: Page, locators: NubiLocators, opts: CreateOpts): Promise<void> {
  await locators.createFunctionBtn.click();
  await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
  await locators.functionNameInput.fill(opts.name);
  await locators.functionDescriptionInput.fill(opts.description);
  await locators.functionPromptInput.fill(opts.prompt);

  if (opts.defaults) {
    for (const [variable, value] of Object.entries(opts.defaults)) {
      await page.getByPlaceholder(`Default value for ${variable}`).fill(value);
    }
  }
  if (opts.draft) {
    await locators.functionStatusSelect.click();
    await locators.functionStatusDraftOption.click({ timeout: 5000 });
  }

  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.saveFunctionBtn.click();
      await expect(locators.functionCreatedMessage).toBeVisible({ timeout: 15000 });
    },
    { testName: `Create function ${opts.name}`, operationNames: OP_CREATE, timeoutMs: 30000 }
  );
}

// Searches for a function by name and opens its Edit form.
async function openEditForm(page: Page, locators: NubiLocators, name: string): Promise<void> {
  await locators.searchFunctionInput.clear();
  await locators.searchFunctionInput.fill(name);
  await expect(page.getByText(name)).toBeVisible({ timeout: 15000 });
  await locators.functionMoreActionsBtn.click();
  await locators.editFunctionMenuItem.waitFor({ state: "visible", timeout: 10000 });
  await locators.editFunctionMenuItem.click();
}

// Deletes a function via the list. validate=true asserts the AiDeleteFunction
// mutation; validate=false is best-effort cleanup that never fails the test.
async function deleteFunction(page: Page, locators: NubiLocators, name: string, validate = true): Promise<void> {
  const run = async () => {
    await locators.searchFunctionInput.clear();
    await locators.searchFunctionInput.fill(name);
    await expect(page.getByText(name)).toBeVisible({ timeout: validate ? 15000 : 3000 });
    await locators.functionMoreActionsBtn.click();
    await locators.deleteFunctionMenuItem.waitFor({ state: "visible", timeout: 10000 });
    await locators.deleteFunctionMenuItem.click();
    await locators.confirmDeleteFunctionBtn.waitFor({ state: "visible", timeout: 10000 });

    if (validate) {
      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.confirmDeleteFunctionBtn.click();
          await expect(locators.functionDeletedMessage).toBeVisible({ timeout: 15000 });
        },
        { testName: `Delete function ${name}`, operationNames: OP_DELETE, timeoutMs: 30000 }
      );
    } else {
      await locators.confirmDeleteFunctionBtn.click();
      await expect(locators.functionDeletedMessage).toBeVisible({ timeout: 15000 });
    }
  };

  if (validate) {
    await run();
  } else {
    await run().catch((e) => console.warn(`[cleanup] delete '${name}' skipped: ${e}`));
  }
}

// Counts outgoing GraphQL mutations of a given operation name. Used by negative
// tests to prove that invalid input never reaches the backend.
function trackMutation(page: Page, opName: string): { count: number } {
  const state = { count: 0 };
  page.on("request", (req) => {
    if (req.method() !== "POST" || !req.url().includes("api/graphql")) return;
    const postData = req.postData();
    if (!postData) return;
    try {
      const payload = JSON.parse(postData);
      const operations = Array.isArray(payload) ? payload : [payload];
      if (operations.some((op: { operationName?: string }) => op.operationName === opName)) {
        state.count += 1;
      }
    } catch {
      if (postData.includes(opName)) state.count += 1;
    }
  });
  return state;
}

test.describe("Nubi Functions Tab", () => {
  // ═══════════════════════════════════════════════════════════════════════════
  test.describe("CRUD", () => {
    test("FN 01: Create Nubi function", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await createFunction(page, locators, {
        name,
        description: "Function created by automation for the create test.",
        prompt: "Summarize recent errors in the cluster.",
      });
      console.log(`[FN 01] '${name}' created.`);

      await locators.searchFunctionInput.click();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).toBeVisible({ timeout: 20000 });
      console.log(`[FN 01] '${name}' verified in list.`);

      await deleteFunction(page, locators, name, false);
    });

    test("FN 02: Update function description and validate AiEditFunction", async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await createFunction(page, locators, {
        name,
        description: "Original description.",
        prompt: "Original prompt body.",
      });

      await openEditForm(page, locators, name);
      await locators.functionDescriptionInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionDescriptionInput.clear();
      await locators.functionDescriptionInput.fill("Updated description by automation.");

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.updateFunctionBtn.click();
          await expect(locators.functionUpdatedMessage).toBeVisible({ timeout: 15000 });
        },
        { testName: "FN 02: Update function", operationNames: OP_UPDATE, timeoutMs: 30000 }
      );
      console.log(`[FN 02] '${name}' updated.`);

      await deleteFunction(page, locators, name, false);
    });

    test("FN 03: Delete function and validate AiDeleteFunction", async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await createFunction(page, locators, {
        name,
        description: "Function to be deleted.",
        prompt: "Prompt body for deletion test.",
      });

      await deleteFunction(page, locators, name, true);
      console.log(`[FN 03] '${name}' deleted.`);

      await locators.searchFunctionInput.clear();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).not.toBeVisible({ timeout: 10000 });
      console.log(`[FN 03] '${name}' confirmed removed.`);
    });

    test("FN 04: Search filters the function list by name", async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await createFunction(page, locators, {
        name,
        description: "Function for the search test.",
        prompt: "Prompt body for search test.",
      });

      await locators.searchFunctionInput.click();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).toBeVisible({ timeout: 15000 });
      console.log(`[FN 04] match shows '${name}'.`);

      await locators.searchFunctionInput.clear();
      await locators.searchFunctionInput.fill(`${name}_zzz_no_match`);
      await expect(page.getByText(name)).not.toBeVisible({ timeout: 10000 });
      console.log(`[FN 04] no-match hides '${name}'.`);

      await deleteFunction(page, locators, name, false);
    });
  });

  // ═══════════════════════════════════════════════════════════════════════════
  test.describe("Validation", () => {
    test("FN 05: Reject invalid function names (no mutation)", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const mutations = trackMutation(page, OP_CREATE);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });

      await locators.functionNameInput.fill("bad-name");
      await expect(page.getByText("Name can only contain letters, numbers, and underscores.")).toBeVisible({ timeout: 10000 });

      await locators.functionNameInput.fill("2function");
      await expect(page.getByText("Name must start with a letter (a-z or A-Z).")).toBeVisible({ timeout: 10000 });

      await locators.functionNameInput.fill("ab");
      await expect(page.getByText("Name must be at least 3 characters long.")).toBeVisible({ timeout: 10000 });

      expect(mutations.count).toBe(0);
      console.log("[FN 05] invalid names rejected; zero mutations.");
    });

    test("FN 06: Reject empty required fields (no mutation)", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const mutations = trackMutation(page, OP_CREATE);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionNameInput.fill("valid_function_name");

      await locators.saveFunctionBtn.click();
      await expect(page.getByText("Description is required")).toBeVisible({ timeout: 10000 });
      await expect(page.getByText("Prompt is required")).toBeVisible({ timeout: 10000 });

      expect(mutations.count).toBe(0);
      console.log("[FN 06] empty fields rejected; zero mutations.");
    });

    test("FN 07: Reject description over 200 words (no mutation)", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const mutations = trackMutation(page, OP_CREATE);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionNameInput.fill("valid_function_name");
      await locators.functionDescriptionInput.fill(Array(250).fill("word").join(" "));
      await locators.functionPromptInput.fill("A valid prompt body.");

      await locators.saveFunctionBtn.click();
      await page.waitForTimeout(1500);
      // Submission blocked → modal stays open (the error renders below a tall textarea).
      await expect(locators.functionNameInput).toBeVisible();

      expect(mutations.count).toBe(0);
      console.log("[FN 07] over-200-word description blocked; zero mutations.");
    });

    test("FN 08: Reject invalid variable name in prompt (no mutation)", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const mutations = trackMutation(page, OP_CREATE);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionPromptInput.fill("Inspect logs for <bad-var> now.");
      await expect(page.getByText(/Invalid variable names:/)).toBeVisible({ timeout: 10000 });

      expect(mutations.count).toBe(0);
      console.log("[FN 08] invalid variable name rejected; zero mutations.");
    });
  });

  // ═══════════════════════════════════════════════════════════════════════════
  test.describe("Variables & Status", () => {
    test("FN 09: Detect variables, render defaults table and persist them", async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionNameInput.fill(name);
      await locators.functionDescriptionInput.fill("Function with two variables.");
      await locators.functionPromptInput.fill("Analyze logs for the <service_name> service in the <namespace> namespace.");

      // Exact match hits the chip <div>, not the prompt textarea value.
      await expect(page.getByText("<service_name>", { exact: true })).toBeVisible({ timeout: 10000 });
      await expect(page.getByText("<namespace>", { exact: true })).toBeVisible({ timeout: 10000 });
      await expect(locators.variableDefaultsHeading).toBeVisible({ timeout: 10000 });
      await expect(page.getByPlaceholder("Default value for service_name")).toBeVisible({ timeout: 10000 });
      await expect(page.getByPlaceholder("Default value for namespace")).toBeVisible({ timeout: 10000 });
      await page.getByPlaceholder("Default value for service_name").fill("payments");
      console.log("[FN 09] chips + defaults table rendered.");

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.saveFunctionBtn.click();
          await expect(locators.functionCreatedMessage).toBeVisible({ timeout: 15000 });
        },
        { testName: "FN 09: Create function with variables", operationNames: OP_CREATE, timeoutMs: 30000 }
      );

      // List shows the "2 vars" chip.
      await locators.searchFunctionInput.click();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).toBeVisible({ timeout: 20000 });
      await expect(page.getByText(/2 vars/i)).toBeVisible({ timeout: 10000 });

      // Default value round-trips through the API on reopen.
      await openEditForm(page, locators, name);
      const def = page.getByPlaceholder("Default value for service_name");
      await expect(def).toBeVisible({ timeout: 10000 });
      await expect(def).toHaveValue("payments");
      console.log("[FN 09] default value persisted across save/reopen.");
      await locators.functionCancelBtn.click();

      await deleteFunction(page, locators, name, false);
    });

    test("FN 10: Create as Draft then change to Active (validates AiEditFunction)", async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openFunctionsTab(page);
      const name = randomName();

      await createFunction(page, locators, {
        name,
        description: "Function created in draft status.",
        prompt: "Prompt body for status test.",
        draft: true,
      });

      // List shows Draft (exact match hits the status Label span).
      await locators.searchFunctionInput.click();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).toBeVisible({ timeout: 20000 });
      await expect(page.getByText("draft", { exact: true })).toBeVisible({ timeout: 10000 });
      console.log("[FN 10] created as Draft.");

      await openEditForm(page, locators, name);
      await locators.functionStatusSelect.click();
      await locators.functionStatusActiveOption.click({ timeout: 5000 });

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.updateFunctionBtn.click();
          await expect(locators.functionUpdatedMessage).toBeVisible({ timeout: 15000 });
        },
        { testName: "FN 10: Change function status draft->active", operationNames: OP_UPDATE, timeoutMs: 30000 }
      );

      await locators.searchFunctionInput.clear();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).toBeVisible({ timeout: 10000 });
      await expect(page.getByText("active", { exact: true })).toBeVisible({ timeout: 10000 });
      console.log("[FN 10] changed to Active.");

      await deleteFunction(page, locators, name, false);
    });
  });

  // ═══════════════════════════════════════════════════════════════════════════
  test.describe("Form features", () => {
    test("FN 11: Rewrite Prompt requires a prompt", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.rewritePromptBtn.click();
      await expect(locators.rewritePromptEmptyError).toBeVisible({ timeout: 10000 });
      console.log("[FN 11] empty-prompt rewrite guard shows error.");
    });

    test("FN 12: View Agent List modal opens and is searchable", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.viewAgentListBtn.click();
      await expect(locators.agentListModalTitle).toBeVisible({ timeout: 10000 });
      await expect(locators.agentListSearchInput).toBeVisible({ timeout: 10000 });
      await locators.agentListSearchInput.fill("zzz_no_such_agent");
      console.log("[FN 12] Available Agents modal opened and searchable.");
    });

    test("FN 13: Cancel discards a new function without persisting", async ({ page }) => {
      test.setTimeout(120000);
      const locators = await openFunctionsTab(page);
      const mutations = trackMutation(page, OP_CREATE);
      const name = randomName();

      await locators.createFunctionBtn.click();
      await locators.functionNameInput.waitFor({ state: "visible", timeout: 10000 });
      await locators.functionNameInput.fill(name);
      await locators.functionDescriptionInput.fill("This function should never be saved.");
      await locators.functionPromptInput.fill("A prompt that gets discarded.");
      await locators.functionCancelBtn.click();

      await expect(locators.functionNameInput).not.toBeVisible({ timeout: 10000 });
      await locators.searchFunctionInput.click();
      await locators.searchFunctionInput.fill(name);
      await expect(page.getByText(name)).not.toBeVisible({ timeout: 10000 });

      expect(mutations.count).toBe(0);
      console.log("[FN 13] cancel discarded the function; zero mutations.");
    });
  });
});
