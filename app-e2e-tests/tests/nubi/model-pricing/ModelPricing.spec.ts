// Not for OSS
import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import {
  FIXTURE_PROVIDER,
  OP_DELETE,
  OP_UPSERT,
  createRate,
  fillNewRateForm,
  openModelPricingTab,
  remountModelPricingTab,
  removeRateIfPresent,
  searchModels,
  trackOperation,
  uniqueModelName,
} from "./modelPricingHelper";

// Nubi > Settings > Model Pricing (app/src/components/llm/ModelPricingTab.jsx).
// Rates are tenant-wide, so every case writes to the "custom" provider under a
// generated model id and removes what it wrote.

test.describe("Nubi Model Pricing Tab", () => {
  test(
    "Model Pricing sanity - open Nubi AI & Tools, select the Model Pricing tab, verify the rate table lists built-in models and offers the Add price control",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openModelPricingTab(page);

      await expect(locators.table).toBeVisible({ timeout: 30000 });
      await expect(locators.rateUnitCaption).toBeVisible({ timeout: 30000 });
      await expect(locators.searchInput).toBeVisible({ timeout: 15000 });
      await expect(locators.providerFilterTrigger).toBeVisible({ timeout: 15000 });
      await expect(locators.sourceFilterTrigger).toBeVisible({ timeout: 15000 });

      // The rates we ship are the tab's baseline content, so an empty table here
      // is a real failure rather than an empty tenant. count() does not retry,
      // so the first chip has to be on screen before it is read — the card
      // renders before ListModelPricing comes back.
      await expect(locators.builtInSourceChips().first()).toBeVisible({ timeout: 30000 });
      expect(await locators.builtInSourceChips().count()).toBeGreaterThan(0);

      // Writing a rate is tenant-admin only, so this is also the precondition
      // every CRUD case below depends on — failing it here names the reason.
      await expect(locators.addPriceBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.readOnlyBanner).toHaveCount(0);
    }
  );

  test(
    "Model Pricing - add a custom rate for a new model, leave the tab and return, verify the saved input and output rates persist",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openModelPricingTab(page);
      const model = uniqueModelName();

      try {
        await createRate(page, locators, { model, input: "1.25", output: "4.5" });

        // The remount is what separates a persisted row from one still sitting
        // in React state: the tab refetches ListModelPricing on mount.
        await remountModelPricingTab(locators);
        await searchModels(locators, model);

        const row = locators.row(model);
        await expect(row).toBeVisible({ timeout: 30000 });
        await expect(row).toContainText("$1.25");
        await expect(row).toContainText("$4.50");
        await expect(row).toContainText("Custom rate");
        await expect(locators.removeRowBtn(FIXTURE_PROVIDER, model)).toBeVisible({ timeout: 15000 });
      } finally {
        await removeRateIfPresent(locators, model);
      }
    }
  );

  test(
    "Model Pricing - add a custom rate, reopen it with Edit and change the output rate, verify the table shows the updated rate",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openModelPricingTab(page);
      const model = uniqueModelName();

      try {
        await createRate(page, locators, { model, input: "1.25", output: "4.5" });
        await searchModels(locators, model);
        await expect(locators.row(model)).toBeVisible({ timeout: 30000 });

        await locators.editRowBtn(FIXTURE_PROVIDER, model).click();
        await expect(locators.outputRateInput).toBeVisible({ timeout: 15000 });
        // Edit mode prefills from the stored row, so this doubles as proof the
        // saved output rate round-tripped through the API. Compared as a number
        // because the backend's own formatting of 4.5 is not part of the contract.
        expect(Number(await locators.outputRateInput.inputValue())).toBe(4.5);
        await locators.outputRateInput.fill("7.25");

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.formSaveBtn.click();
            await expect(locators.toastWithText(`Saved pricing for ${model}.`)).toBeVisible({ timeout: 20000 });
          },
          { testName: "Model Pricing - update an existing tenant rate", operationNames: OP_UPSERT, timeoutMs: 45000 }
        );

        await remountModelPricingTab(locators);
        await searchModels(locators, model);
        const row = locators.row(model);
        await expect(row).toContainText("$7.25", { timeout: 30000 });
        await expect(row).not.toContainText("$4.50");
      } finally {
        await removeRateIfPresent(locators, model);
      }
    }
  );

  test(
    "Model Pricing - add a custom rate then remove the override, verify the warning that nothing else prices the model and that the row is gone",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openModelPricingTab(page);
      const model = uniqueModelName();

      try {
        await createRate(page, locators, { model, input: "2", output: "6" });
        await searchModels(locators, model);
        await expect(locators.row(model)).toBeVisible({ timeout: 30000 });

        await locators.removeRowBtn(FIXTURE_PROVIDER, model).click();
        await expect(locators.removeDialog).toBeVisible({ timeout: 15000 });
        // A generated model id has no built-in rate behind it, so the tab must
        // warn that its spend reports as $0 rather than promising a fallback.
        await expect(locators.removeDialog).toContainText("No built-in rate to fall back on");

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.confirmRemoveBtn.click();
            await expect(locators.toastWithText(`Removed the override for ${model}.`)).toBeVisible({ timeout: 20000 });
          },
          { testName: "Model Pricing - remove a tenant override", operationNames: OP_DELETE, timeoutMs: 45000 }
        );

        await remountModelPricingTab(locators);
        await searchModels(locators, model);
        await expect(locators.row(model)).toHaveCount(0, { timeout: 30000 });
        await expect(locators.noMatchEmptyState).toBeVisible({ timeout: 30000 });
      } finally {
        await removeRateIfPresent(locators, model);
      }
    }
  );

  test(
    "Model Pricing - open Add price, enter an unrecognised provider, save, verify the unknown-provider error and that no pricing mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openModelPricingTab(page);
      const upserts = trackOperation(page, OP_UPSERT);
      const model = uniqueModelName();

      // "open-ai" is the exact typo the allowlist exists to catch: it would
      // store a row that never matches a usage row.
      await fillNewRateForm(locators, { model, input: "1", output: "2", provider: "open-ai" });
      await locators.formSaveBtn.click();

      await expect(locators.toastWithText(/^Unknown provider\. Use one of:/)).toBeVisible({ timeout: 20000 });
      await expect(locators.priceFormDialog).toBeVisible();
      expect(upserts.count).toBe(0);
    }
  );

  test(
    "Model Pricing - open Add price, leave the output rate blank, save, verify the missing-rate error and that no pricing mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openModelPricingTab(page);
      const upserts = trackOperation(page, OP_UPSERT);
      const model = uniqueModelName();

      await locators.addPriceBtn.click();
      await locators.modelInput.waitFor({ state: "visible", timeout: 15000 });
      await locators.providerInput.fill(FIXTURE_PROVIDER);
      await locators.modelInput.fill(model);
      await locators.inputRateInput.fill("1.5");
      await locators.formSaveBtn.click();

      await expect(locators.toastWithText("Enter both an input and an output rate.")).toBeVisible({ timeout: 20000 });
      await expect(locators.priceFormDialog).toBeVisible();
      expect(upserts.count).toBe(0);
    }
  );

  test(
    "Model Pricing - open Add price, enter a long-context threshold with no tier rates, save, verify the long-context tier error and that no pricing mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openModelPricingTab(page);
      const upserts = trackOperation(page, OP_UPSERT);
      const model = uniqueModelName();

      // A threshold with no rates behind it stores a tier that never fires — it
      // reads as configured and bills flat, which is why all three or none.
      await fillNewRateForm(locators, { model, input: "1", output: "2", threshold: "200000" });
      await locators.formSaveBtn.click();

      await expect(locators.toastWithText(/^Long-context pricing needs a threshold and both long-context rates/)).toBeVisible({ timeout: 20000 });
      await expect(locators.priceFormDialog).toBeVisible();
      expect(upserts.count).toBe(0);
    }
  );

  test(
    "Model Pricing - add a custom rate, filter the table by model name and then by the Custom rate source, verify only tenant rates are listed",
    { tag: ["@dev", "@regression", "@search", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openModelPricingTab(page);
      const model = uniqueModelName();

      try {
        await createRate(page, locators, { model, input: "3", output: "9" });

        await searchModels(locators, model);
        await expect(locators.row(model)).toBeVisible({ timeout: 30000 });
        expect(await locators.builtInSourceChips().count()).toBe(0);

        await searchModels(locators, `${model}_no_such_model`);
        await expect(locators.noMatchEmptyState).toBeVisible({ timeout: 30000 });

        // Clearing the search and switching Source to tenant rates must leave the
        // row listed while every shipped rate drops out.
        await searchModels(locators, "");
        await locators.chooseFilter(locators.sourceFilterTrigger, "Custom rate");
        await expect(locators.row(model)).toBeVisible({ timeout: 30000 });
        expect(await locators.builtInSourceChips().count()).toBe(0);
        expect(await locators.customRateSourceChips().count()).toBeGreaterThan(0);
      } finally {
        await removeRateIfPresent(locators, model);
      }
    }
  );

  test(
    "Model Pricing - open Add price, fill a valid rate, cancel, verify the model is never listed and no pricing mutation is sent",
    { tag: ["@dev", "@regression", "@functional", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openModelPricingTab(page);
      const upserts = trackOperation(page, OP_UPSERT);
      const model = uniqueModelName();

      await fillNewRateForm(locators, { model, input: "5", output: "10" });
      await locators.formCancelBtn.click();
      await expect(locators.priceFormDialog).toHaveCount(0, { timeout: 15000 });

      await remountModelPricingTab(locators);
      await searchModels(locators, model);
      await expect(locators.noMatchEmptyState).toBeVisible({ timeout: 30000 });
      expect(upserts.count).toBe(0);
    }
  );
});
