// Not for OSS

import { test, expect } from "@playwright/test";
import {
  ACTIVE_BUDGETS_HEADING,
  APPLY_BOTH_MODULES,
  APPLY_EVENT_ANALYSIS,
  CRUD_TIMEOUT_MS,
  LIMIT_DAILY_COST,
  LIMIT_DAILY_COUNT,
  LIMIT_MONTHLY_COST,
  LIMIT_MONTHLY_COUNT,
  MODULE_LABEL_EVENT_ANALYSIS,
  NO_LIMITS_TEXT,
  SCOPE_ACCOUNT,
  SCOPE_TENANT,
  SPEC_TIMEOUT_MS,
  TOAST_DELETED,
  TOAST_SAVED,
} from "./usageLimitsConstants";
import {
  createBudgetWithNoLimits,
  deleteAccountBudgetIfPresent,
  openUsageLimits,
  reloadUsageLimits,
  requiredTenantName,
  selectAccountWithoutEventAnalysisBudget,
} from "./usageLimitsHelper";

// Admin -> AI & Tools -> Budgets & Limits -> Usage & Limits
// (app/src/components/llm/LLMConsumptionTab.jsx, mounted tenant-wide by
// BudgetsAndLimitsAdminTab.jsx). Model Pricing, the sibling toggle inside the
// same tab, is covered by tests/nubi/model-pricing and is only touched here to
// prove the toggle swaps the body.
//
// This runs against a shared tenant, so exactly one case writes: it creates an
// account-scoped budget with every limit switched off — a real persisted record
// that imposes no ceiling on anyone — and deletes it again. Every other case
// stops at a guard that returns before any network write.

test.describe("Admin AI & Tools Budgets & Limits - Usage & Limits", () => {
  test(
    "Usage & Limits sanity - deep-link to Admin AI & Tools Budgets & Limits, verify the Usage & Limits toggle is selected and the period header, Add Budget control and Active Budgets section render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);

      // The hash carries both levels, so landing on the right sub-tab is itself
      // part of what this asserts — a broken child fragment silently falls back
      // to the first sub-tab (Agents), which renders none of the below.
      await expect(locators.aiToolsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await expect(locators.budgetsLimitsTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      await expect(locators.modelPricingToggle).toHaveAttribute("aria-checked", "false", { timeout: 30000 });

      await expect(locators.periodHeading).toBeVisible({ timeout: 30000 });
      await expect(locators.activeBudgetsHeading).toBeVisible({ timeout: 60000 });

      // Writing a budget is tenant-admin only, so this is the precondition every
      // dialog case below depends on — failing it here names the reason.
      await expect(locators.addBudgetBtn).toBeVisible({ timeout: 30000 });
      await expect(locators.addBudgetBtn).toBeEnabled();
    }
  );

  test(
    "Usage & Limits sanity - switch the toggle to Model Pricing and back to Usage & Limits, verify the budget period header disappears and comes back with the toggle selection",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);

      await locators.modelPricingToggle.click();
      await expect(locators.modelPricingToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
      // BudgetsAndLimitsAdminTab renders one branch or the other, never both, so
      // the budget header going away is what proves the body actually swapped
      // rather than the toggle merely repainting.
      await expect(locators.periodHeading).toHaveCount(0, { timeout: 30000 });
      await expect(locators.addBudgetBtn).toHaveCount(0, { timeout: 30000 });

      await locators.usageLimitsToggle.click();
      await expect(locators.usageLimitsToggle).toHaveAttribute("aria-checked", "true", { timeout: 30000 });
      await expect(locators.periodHeading).toBeVisible({ timeout: 60000 });
      await expect(locators.addBudgetBtn).toBeVisible({ timeout: 30000 });
    }
  );

  test(
    "Usage & Limits sanity - open Budgets & Limits, verify the Active Budgets section lists this run's tenant scope or states that system defaults apply",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      const tenantName = requiredTenantName();

      await expect(locators.activeBudgetsHeading).toBeVisible({ timeout: 60000 });
      await expect(locators.activeBudgetsSection).toContainText(ACTIVE_BUDGETS_HEADING, { timeout: 30000 });

      // ActiveConfigsCompact drops the scope row entirely when the tenant holds
      // no custom config and prints its empty message instead, and which of the
      // two a shared tenant is in is not this suite's to fix — so both are
      // accepted, and the assertion is that one of them is actually rendered.
      const tenantScopeRow = locators.activeBudgetsSection.getByText(tenantName, { exact: true }).first();
      await expect(tenantScopeRow.or(locators.noActiveConfigsText)).toBeVisible({ timeout: 60000 });
    }
  );

  test(
    "Usage & Limits - open Add Budget, verify the form opens on the Tenant scope applied to both modules with Monthly Cost on and the Daily Cost amount field disabled",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      await locators.openCreateDialog();

      await expect(locators.scopeSelect()).toContainText(SCOPE_TENANT, { timeout: 30000 });
      await expect(locators.applyToSelect()).toContainText(APPLY_BOTH_MODULES, { timeout: 30000 });
      // The tenant field is a fixed read-only stand-in; the form has no tenant
      // picker because a session only ever has the one.
      await expect(locators.tenantField).toBeDisabled();
      await expect(locators.tenantField).toHaveValue("Current Tenant");

      await expect(locators.limitToggle(LIMIT_MONTHLY_COST)).toBeChecked();
      await expect(locators.limitInput(LIMIT_MONTHLY_COST)).toBeEnabled();
      await expect(locators.limitInput(LIMIT_MONTHLY_COST)).toHaveValue("");

      await expect(locators.limitToggle(LIMIT_DAILY_COST)).not.toBeChecked();
      await expect(locators.limitInput(LIMIT_DAILY_COST)).toBeDisabled();
      // Both limit groups render unconditionally, so a missing heading means the
      // count half of the form never mounted rather than that it is empty.
      await expect(locators.countLimitsHeading).toBeVisible();
      await expect(locators.limitToggle(LIMIT_MONTHLY_COUNT)).not.toBeChecked();
      await expect(locators.limitToggle(LIMIT_DAILY_COUNT)).not.toBeChecked();
    }
  );

  test(
    "Usage & Limits - open Add Budget, switch the scope to Account, leave the account unpicked, submit, verify the Please select an entity rejection and that the dialog stays open",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      await locators.openCreateDialog();

      await locators.chooseOption(locators.scopeSelect(), SCOPE_ACCOUNT);
      // Switching the scope clears the entity, so the Account picker comes up
      // empty — this is the one rejection that does not depend on what the
      // shared tenant already holds, because handleSave checks it before both
      // the duplicate-budget guard and the limit validation.
      await expect(locators.accountSelect()).toBeVisible({ timeout: 30000 });
      await expect(locators.createBtn).toBeEnabled();

      await locators.createBtn.click();

      await expect(locators.noEntityError).toBeVisible({ timeout: 30000 });
      await expect(locators.budgetDialog).toBeVisible();
      // The guard returns before apiBudget.upsertBudgetConfig is called, so no
      // save toast may appear — that is what proves nothing was written.
      await expect(locators.toastWithText(TOAST_SAVED)).toHaveCount(0);
    }
  );

  test(
    "Usage & Limits - open Add Budget, turn Daily Cost on, enter an amount, turn Daily Cost back off, verify the amount field goes disabled and keeps the typed amount",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      await locators.openCreateDialog();

      const dailyCostInput = locators.limitInput(LIMIT_DAILY_COST);
      await expect(dailyCostInput).toBeDisabled();

      await locators.limitToggle(LIMIT_DAILY_COST).check();
      await expect(dailyCostInput).toBeEnabled();
      await dailyCostInput.fill("25");
      await expect(dailyCostInput).toHaveValue("25");

      await locators.limitToggle(LIMIT_DAILY_COST).uncheck();
      await expect(dailyCostInput).toBeDisabled();
      // renderLimitRow only gates the input on the switch; it does not clear the
      // amount, so the typed value has to survive the toggle or the form would
      // silently drop an edit the user can still see.
      await expect(dailyCostInput).toHaveValue("25");
    }
  );

  test(
    "Usage & Limits - open Add Budget, turn Monthly Count on and enter a limit, cancel the dialog, reopen it, verify the form is back to its defaults with Monthly Count off and empty",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      await locators.openCreateDialog();

      await locators.limitToggle(LIMIT_MONTHLY_COUNT).check();
      await locators.limitInput(LIMIT_MONTHLY_COUNT).fill("4321");
      await expect(locators.limitInput(LIMIT_MONTHLY_COUNT)).toHaveValue("4321");

      await locators.dialogCancelBtn.click();
      await locators.budgetDialog.waitFor({ state: "detached", timeout: 30000 });
      // Cancelling never calls upsertBudgetConfig, so the absence of a save
      // toast is the side-effect half of this case.
      await expect(locators.toastWithText(TOAST_SAVED)).toHaveCount(0);

      await locators.openCreateDialog();
      await expect(locators.limitToggle(LIMIT_MONTHLY_COUNT)).not.toBeChecked();
      await expect(locators.limitInput(LIMIT_MONTHLY_COUNT)).toHaveValue("");
      await expect(locators.limitToggle(LIMIT_MONTHLY_COST)).toBeChecked();
      await expect(locators.limitInput(LIMIT_MONTHLY_COST)).toHaveValue("");
    }
  );

  test(
    "Usage & Limits - open Add Budget, turn Daily Count on with a limit, switch Apply to from Both Modules to Event Analysis, verify the limit rows reset to Monthly Cost on and every amount cleared",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      await locators.openCreateDialog();

      await locators.limitToggle(LIMIT_DAILY_COUNT).check();
      await locators.limitInput(LIMIT_DAILY_COUNT).fill("77");
      await expect(locators.limitInput(LIMIT_DAILY_COUNT)).toHaveValue("77");

      await locators.chooseOption(locators.applyToSelect(), APPLY_EVENT_ANALYSIS);

      // The form is scoped per (scope, entity, module), so narrowing the module
      // has to drop limits typed against the wider selection rather than carry
      // them into a config the user never reviewed for this module.
      await expect(locators.limitToggle(LIMIT_DAILY_COUNT)).not.toBeChecked({ timeout: 30000 });
      await expect(locators.limitInput(LIMIT_DAILY_COUNT)).toHaveValue("");
      await expect(locators.limitToggle(LIMIT_MONTHLY_COST)).toBeChecked();
      await expect(locators.limitInput(LIMIT_MONTHLY_COST)).toHaveValue("");
    }
  );

  test(
    "Usage & Limits - add an Event Analysis budget with every limit switched off for an account that has none, reload the page, verify the saved budget is listed under Budgets for All Accounts, then delete it and verify the chip is gone",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(CRUD_TIMEOUT_MS);
      const locators = await openUsageLimits(page);
      let accountLabel = "";

      try {
        await locators.openCreateDialog();
        await locators.chooseOption(locators.scopeSelect(), SCOPE_ACCOUNT);
        await locators.chooseOption(locators.applyToSelect(), APPLY_EVENT_ANALYSIS);
        accountLabel = await selectAccountWithoutEventAnalysisBudget(locators);

        await createBudgetWithNoLimits(locators);
        await expect(locators.toastWithText(TOAST_SAVED)).toBeVisible({ timeout: 30000 });

        // The reload is what separates a persisted config from one still sitting
        // in React state: listBudgetConfigs is refetched on mount, and the hash
        // survives the reload so the sub-tab resolves again.
        const reloaded = await reloadUsageLimits(page);
        await reloaded.expandOtherAccounts();
        // A saved config with no limits enabled prints this instead of limit
        // chips; the dashed placeholder an unconfigured module renders reads
        // "system defaults", so the two states are distinguishable.
        await expect(reloaded.moduleChip(accountLabel, MODULE_LABEL_EVENT_ANALYSIS)).toContainText(NO_LIMITS_TEXT, { timeout: 30000 });

        await reloaded.deleteChipBtn(accountLabel, MODULE_LABEL_EVENT_ANALYSIS).click();
        await expect(reloaded.deleteDialog).toBeVisible({ timeout: 30000 });
        await reloaded.confirmDeleteBtn.click();
        await expect(reloaded.toastWithText(TOAST_DELETED)).toBeVisible({ timeout: 30000 });

        // A second reload rather than reading the list handleDeleteConfirmed
        // refetches in place: while configsLoading is true the whole section,
        // heading included, is swapped for a <Loader/>, so a count assertion
        // taken in that window passes whether or not the delete ever reached
        // the backend. Re-reading a freshly mounted page has no such window.
        const afterDelete = await reloadUsageLimits(page);
        await expect(afterDelete.activeBudgetsHeading).toBeVisible({ timeout: 60000 });
        // The section is only rendered while some account config survives, so
        // it being gone is itself proof this one did not; when other accounts
        // still have budgets it has to be open before an absent chip means
        // anything.
        if (await afterDelete.otherAccountsSectionPresent()) await afterDelete.expandOtherAccounts();
        await expect(afterDelete.moduleChip(accountLabel, MODULE_LABEL_EVENT_ANALYSIS)).toHaveCount(0, { timeout: 30000 });
        accountLabel = "";
      } finally {
        if (accountLabel) await deleteAccountBudgetIfPresent(locators, accountLabel);
      }
    }
  );
});
