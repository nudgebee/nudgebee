// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { ModelPricingLocators } from "./modelPricingLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

export const OP_UPSERT = "UpsertModelPricing";
export const OP_DELETE = "DeleteModelPricing";

// Every rate this suite writes is a tenant override, so it must never collide
// with a real one. "custom" is the tab's own allowlisted placeholder provider
// and the model id is generated per test, which also keeps a leftover from a
// failed run from failing the next one.
export const FIXTURE_PROVIDER = "custom";

export function uniqueModelName(): string {
  return `nb_e2e_price_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
}

// Logs in, opens the Nubi panel and lands on AI & Tools > Budgets & Limits >
// Model Pricing.
export async function openModelPricingTab(page: Page): Promise<ModelPricingLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new ModelPricingLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.aiToolsBtn.click();
  await locators.budgetsLimitsTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.budgetsLimitsTab.click();
  await locators.modelPricingToggle.waitFor({ state: "visible", timeout: 15000 });
  await locators.modelPricingToggle.click();
  await locators.listingCard.waitFor({ state: "visible", timeout: 30000 });
  return locators;
}

// Switches to the sibling "Usage & Limits" toggle and back, which unmounts
// ModelPricingTab and makes it refetch — the only way to tell a saved row from
// one still held in React state. Both are ToggleGroup options inside the same
// "Budgets & Limits" tab, so there is no need to leave it.
export async function remountModelPricingTab(locators: ModelPricingLocators): Promise<void> {
  await locators.usageLimitsToggle.click();
  await locators.listingCard.waitFor({ state: "detached", timeout: 20000 });
  await locators.modelPricingToggle.click();
  await locators.listingCard.waitFor({ state: "visible", timeout: 30000 });
}

export async function searchModels(locators: ModelPricingLocators, text: string): Promise<void> {
  await locators.searchInput.click();
  await locators.searchInput.fill(text);
  await expect(locators.searchInput).toHaveValue(text, { timeout: 10000 });
}

export interface RateDraft {
  model: string;
  input: string;
  output: string;
  threshold?: string;
  provider?: string;
}

// Opens the Add price dialog and fills the identity and rate fields.
export async function fillNewRateForm(locators: ModelPricingLocators, draft: RateDraft): Promise<void> {
  await locators.addPriceBtn.click();
  await locators.modelInput.waitFor({ state: "visible", timeout: 15000 });
  await locators.providerInput.fill(draft.provider ?? FIXTURE_PROVIDER);
  await locators.modelInput.fill(draft.model);
  await locators.inputRateInput.fill(draft.input);
  await locators.outputRateInput.fill(draft.output);
  if (draft.threshold !== undefined) {
    await locators.thresholdInput.fill(draft.threshold);
  }
}

// Fills and saves a new tenant rate, asserting the UpsertModelPricing mutation
// actually left the browser and came back clean.
export async function createRate(page: Page, locators: ModelPricingLocators, draft: RateDraft): Promise<void> {
  await fillNewRateForm(locators, draft);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.formSaveBtn.click();
      await expect(locators.toastWithText(`Saved pricing for ${draft.model}.`)).toBeVisible({ timeout: 20000 });
    },
    { testName: "Model Pricing - save a tenant rate", operationNames: OP_UPSERT, timeoutMs: 45000 }
  );
  await locators.priceFormDialog.waitFor({ state: "detached", timeout: 20000 });
}

// Best-effort teardown of a rate this suite created. Scoped to the row's own
// Remove control rather than a loose text match, and it asserts the row is gone
// so a silently failed cleanup cannot look like a success.
export async function removeRateIfPresent(locators: ModelPricingLocators, model: string, provider = FIXTURE_PROVIDER): Promise<void> {
  try {
    await searchModels(locators, model);
    const removeBtn = locators.removeRowBtn(provider, model);
    // A test that never got as far as saving leaves nothing to remove, so
    // absence here is a normal outcome and must come back as false.
    const present = await removeBtn
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;
    await removeBtn.click();
    await locators.confirmRemoveBtn.waitFor({ state: "visible", timeout: 15000 });
    await locators.confirmRemoveBtn.click();
    await locators.row(model).waitFor({ state: "detached", timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the generated pricing row could not be removed: ${error}`);
  }
}

// Counts outgoing GraphQL operations by name, so a negative test can prove the
// invalid draft never reached the backend.
export function trackOperation(page: Page, opName: string): { count: number } {
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
