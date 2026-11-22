// Not for OSS
import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { trackOperations } from "../egress-filter/egressFilterHelper";
import {
  OP_CREATE_RULE,
  OP_DELETE_RULE,
  OP_LIST_QUOTAS,
  OP_LIST_RULES,
  OP_SET_TIER,
  OP_UPDATE_RULE,
  OP_UPDATE_SETTINGS,
  OP_UPSERT_QUOTA,
  OP_DELETE_QUOTA,
  createQuota,
  createRule,
  deleteQuotaIfPresent,
  deleteRuleIfPresent,
  fillRuleForm,
  limitPattern,
  openGatewayTab,
  openNewQuotaDialog,
  openNewRuleDialog,
  openSection,
  pickSelectOption,
  remountGatewayTab,
  uniquePriority,
  uniqueQuotaLimit,
  uniqueRuleModel,
} from "./gatewayConfigHelper";

// Nubi > AI & Tools > Gateway (app/src/ee/components/gateway-config/GatewayConfigTab.tsx).
// Tier mappings and Data & privacy are tenant-wide gateway policy on a shared
// dev tenant, so this suite opens their editors and discards the edit, never
// saving; every such case asserts the write operation stayed at zero. Routing
// rules and quotas are written, always under a run-unique match model or limit
// that no real traffic can hit, and always deleted again.

test.describe("Nubi Gateway Config Tab", () => {
  test(
    "Gateway sanity - open Nubi AI & Tools, select the Gateway tab, verify the section toggle offers Tiers, Routing rules, Quotas and Data & privacy, opens on Tiers, and the tiers table lists nb-fast, nb-cheap and nb-smart with an enabled Remap control each",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openGatewayTab(page);

      await expect(locators.sectionToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.tiersOption).toHaveAttribute("aria-checked", "true");
      await expect(locators.routingOption).toHaveAttribute("aria-checked", "false");
      await expect(locators.quotasOption).toHaveAttribute("aria-checked", "false");
      await expect(locators.privacyOption).toHaveAttribute("aria-checked", "false");

      // The three aliases are the gateway's shipped platform defaults
      // (llm/gateway/proxy/generic.go), so a tenant that lists none has lost its
      // tiering rather than merely having no data yet.
      for (const alias of ["nb-fast", "nb-cheap", "nb-smart"]) {
        await expect(locators.tierRow(alias)).toBeVisible({ timeout: 30000 });
        await expect(locators.tierRow(alias)).toContainText("/");
        await expect(locators.tierSourceChip(alias)).toHaveText(/^(Default|Custom)$/);
        // Remap only enables for a writer (llm:Write or tenant admin), so this
        // is the precondition every CRUD case below depends on.
        await expect(locators.remapTierBtn(alias)).toBeEnabled();
      }
    }
  );

  test(
    "Gateway sanity - switch the section toggle to Routing rules, Quotas and Data & privacy in turn and back to Tiers, verify each section's own table or form replaces the previous one and the toggle marks it checked",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openGatewayTab(page);

      await openSection(locators, "routing");
      await expect(locators.newRuleBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.tiersTableBody).toHaveCount(0);

      await openSection(locators, "quotas");
      await expect(locators.newQuotaBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.routingTableBody).toHaveCount(0);
      await expect(locators.newRuleBtn).toHaveCount(0);

      await openSection(locators, "privacy");
      await expect(locators.captureBodyToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.privacySaveBtn).toBeVisible({ timeout: 15000 });
      // Nothing has been touched, so the dirty gate must still be closed.
      await expect(locators.privacySaveBtn).toBeDisabled();
      await expect(locators.newQuotaBtn).toHaveCount(0);

      await openSection(locators, "tiers");
      await expect(locators.tierRow("nb-fast")).toBeVisible({ timeout: 30000 });
      await expect(locators.dlpModeSelect).toHaveCount(0);
      await expect(locators.routingOption).toHaveAttribute("aria-checked", "false");
    }
  );

  test(
    "Gateway Tiers - open Remap for the first tier, verify the dialog names its platform default and prefills the provider and model, cancel, verify the Source column is unchanged and no set-tier mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const counts = trackOperations(page, [OP_SET_TIER]);
      const locators = await openGatewayTab(page);

      await expect(locators.firstTierAliasCell).toHaveText(/\S/, { timeout: 30000 });
      const alias = ((await locators.firstTierAliasCell.innerText()) ?? "").trim();
      const sourceBefore = ((await locators.tierSourceChip(alias).innerText()) ?? "").trim();

      await locators.remapTierBtn(alias).click();
      await expect(locators.tierDialog).toBeVisible({ timeout: 15000 });
      await expect(locators.tierDialog).toContainText(`Remap ${alias}`);
      await expect(locators.tierPlatformDefault).toContainText("/");
      // The form opens on what the tier currently resolves to, so a blank model
      // here would mean the row's effective mapping never reached the dialog.
      await expect(locators.tierProviderSelect).toHaveText(/\S/);
      await expect(locators.tierModelInput).toHaveValue(/\S/);
      await expect(locators.tierSaveBtn).toBeVisible();

      await locators.tierCancelBtn.click();
      await locators.tierDialog.waitFor({ state: "detached", timeout: 15000 });

      await expect(locators.tierSourceChip(alias)).toHaveText(sourceBefore);
      expect(counts[OP_SET_TIER]).toBe(0);
    }
  );

  test(
    "Gateway Routing rules - open New rule, enter a unique priority and match model, create, verify the row lists that priority and model with an Enabled status, leave the tab and return, verify the rule persisted",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_CREATE_RULE]);
      const locators = await openGatewayTab(page);
      const model = uniqueRuleModel();
      const priority = uniquePriority();

      try {
        await openSection(locators, "routing");
        await createRule(page, locators, { model, priority, enabled: true });

        const row = locators.ruleRow(model);
        await expect(row).toContainText(String(priority));
        await expect(row).toContainText(`any / ${model}`);
        await expect(locators.ruleStatusChip(model)).toHaveText("Enabled");

        // The remount is what separates a persisted rule from one still held in
        // React state: the section refetches llm_gateway_list_routing_rules on mount.
        await remountGatewayTab(locators);
        await openSection(locators, "routing");
        await expect(locators.ruleRow(model)).toBeVisible({ timeout: 30000 });
        await expect(locators.ruleRow(model)).toContainText(String(priority));
        await expect(locators.ruleStatusChip(model)).toHaveText("Enabled");
        expect(counts[OP_CREATE_RULE]).toBe(1);
      } finally {
        await deleteRuleIfPresent(locators, model);
      }
    }
  );

  test(
    "Gateway Routing rules - create a rule, reopen it with Edit, change the priority and switch it off, save, leave the tab and return, verify the row shows the new priority and a Disabled status",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openGatewayTab(page);
      const model = uniqueRuleModel();
      const priority = uniquePriority();
      const updatedPriority = priority + 1;

      try {
        await openSection(locators, "routing");
        await createRule(page, locators, { model, priority, enabled: true });

        await locators.editRuleBtn(model).click();
        await expect(locators.editRuleDialog).toBeVisible({ timeout: 15000 });

        // Edit mode prefills from the stored rule, so these are proof the created
        // values round-tripped through the gateway rather than of what the form last held.
        await expect(locators.rulePriorityInput).toHaveValue(String(priority), { timeout: 15000 });
        await expect(locators.ruleMatchModelInput).toHaveValue(model);
        await expect(locators.ruleEnabledSwitch).toBeChecked();

        await fillRuleForm(locators, { model, priority: updatedPriority, enabled: false });
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.ruleSaveBtn.click();
            await locators.ruleDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "Gateway Routing rules - update a rule", operationNames: [OP_UPDATE_RULE, OP_LIST_RULES], timeoutMs: 45000 }
        );

        await remountGatewayTab(locators);
        await openSection(locators, "routing");
        const row = locators.ruleRow(model);
        await expect(row).toBeVisible({ timeout: 30000 });
        await expect(row).toContainText(String(updatedPriority));
        await expect(locators.ruleStatusChip(model)).toHaveText("Disabled");
      } finally {
        await deleteRuleIfPresent(locators, model);
      }
    }
  );

  test(
    "Gateway Routing rules - create a rule then delete it from the confirm dialog, verify the confirm names the rule's priority, the row is gone, and it stays gone after leaving the tab and returning",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openGatewayTab(page);
      const model = uniqueRuleModel();
      const priority = uniquePriority();

      try {
        await openSection(locators, "routing");
        await createRule(page, locators, { model, priority, enabled: false });

        await locators.deleteRuleBtn(model).click();
        await expect(locators.deleteRuleDialog).toBeVisible({ timeout: 15000 });
        await expect(locators.deleteRuleDialog).toContainText(`priority ${priority} rule`);

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.confirmDeleteRuleBtn.click();
            await locators.deleteRuleDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "Gateway Routing rules - delete a rule", operationNames: [OP_DELETE_RULE, OP_LIST_RULES], timeoutMs: 45000 }
        );
        await expect(locators.ruleRow(model)).toHaveCount(0, { timeout: 30000 });

        await remountGatewayTab(locators);
        await openSection(locators, "routing");
        await expect(locators.ruleRow(model)).toHaveCount(0);
      } finally {
        await deleteRuleIfPresent(locators, model);
      }
    }
  );

  test(
    "Gateway Routing rules - open New rule, pick openai as the target provider with the match provider left as Any and no target model, verify the required-model error blocks Create, enter a model, verify Create enables, cancel, verify no create mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, [OP_CREATE_RULE]);
      const locators = await openGatewayTab(page);

      await openSection(locators, "routing");
      await openNewRuleDialog(locators);
      // An untouched rule changes nothing, so Create starts blocked (nb-39222).
      await expect(locators.ruleSaveBtn).toBeDisabled({ timeout: 15000 });

      // A target provider that differs from the matched one is a substitution,
      // and the requested model name does not exist there — so the form refuses
      // it without an explicit target model, mirroring the backend's own check.
      await pickSelectOption(locators, locators.ruleTargetProviderSelect, "openai");
      await expect(locators.ruleTargetModelError).toBeVisible({ timeout: 15000 });
      await expect(locators.ruleTargetModelError).toContainText("target model is required");
      await expect(locators.ruleSaveBtn).toBeDisabled();

      await locators.ruleTargetModelInput.fill(uniqueRuleModel());
      await expect(locators.ruleTargetModelError).toHaveCount(0, { timeout: 15000 });
      await expect(locators.ruleSaveBtn).toBeEnabled({ timeout: 15000 });

      await locators.ruleCancelBtn.click();
      await locators.ruleDialog.waitFor({ state: "detached", timeout: 15000 });
      expect(counts[OP_CREATE_RULE]).toBe(0);
    }
  );

  test(
    "Gateway Routing rules - open New rule, fill a unique priority and match model then cancel, leave the tab and return, verify no rule with that model is listed and no create mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, [OP_CREATE_RULE]);
      const locators = await openGatewayTab(page);
      const model = uniqueRuleModel();

      await openSection(locators, "routing");
      await openNewRuleDialog(locators);
      await fillRuleForm(locators, { model, priority: uniquePriority(), enabled: true });

      await locators.ruleCancelBtn.click();
      await locators.ruleDialog.waitFor({ state: "detached", timeout: 15000 });
      await expect(locators.ruleRow(model)).toHaveCount(0);

      await remountGatewayTab(locators);
      await openSection(locators, "routing");
      await expect(locators.ruleRow(model)).toHaveCount(0);
      expect(counts[OP_CREATE_RULE]).toBe(0);
    }
  );

  test(
    "Gateway Quotas - open New quota, verify Create is blocked while the limit is 0, enter a positive limit, verify Create enables, clear the limit back to 0, verify it is blocked again, cancel, verify no quota mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, [OP_UPSERT_QUOTA]);
      const locators = await openGatewayTab(page);

      await openSection(locators, "quotas");
      await openNewQuotaDialog(locators);

      // The form opens on limit 0, and a quota must cap something, so Create is
      // gated on a positive number rather than on a message.
      await expect(locators.quotaLimitInput).toHaveValue("0", { timeout: 15000 });
      await expect(locators.quotaSaveBtn).toBeDisabled();

      await locators.quotaLimitInput.fill("25");
      await expect(locators.quotaLimitInput).toHaveValue("25", { timeout: 10000 });
      await expect(locators.quotaSaveBtn).toBeEnabled({ timeout: 15000 });

      await locators.quotaLimitInput.fill("0");
      await expect(locators.quotaLimitInput).toHaveValue("0", { timeout: 10000 });
      await expect(locators.quotaSaveBtn).toBeDisabled({ timeout: 15000 });

      await locators.quotaCancelBtn.click();
      await locators.quotaDialog.waitFor({ state: "detached", timeout: 15000 });
      expect(counts[OP_UPSERT_QUOTA]).toBe(0);
    }
  );

  test(
    "Gateway Quotas - create a disabled tenant-wide tokens-per-month quota with a unique limit, verify the row lists the tenant scope, metric, period, limit and a Disabled status, leave the tab and return, verify it persisted, then delete it and verify the row is gone",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_UPSERT_QUOTA, OP_DELETE_QUOTA]);
      const locators = await openGatewayTab(page);
      const limit = uniqueQuotaLimit();
      const limitText = limitPattern(limit);

      try {
        await openSection(locators, "quotas");
        // Disabled so the shared tenant is never capped, even for the seconds
        // the row exists; the seven-digit limit is what identifies the row.
        await createQuota(page, locators, { metric: "tokens", period: "month", limit, enabled: false });

        const row = locators.quotaRow(limitText);
        await expect(row).toContainText("Tenant (all users)");
        await expect(row).toContainText("tokens");
        await expect(row).toContainText("month");
        await expect(locators.quotaStatusChip(limitText)).toHaveText("Disabled");

        await remountGatewayTab(locators);
        await openSection(locators, "quotas");
        await expect(locators.quotaRow(limitText)).toBeVisible({ timeout: 30000 });
        await expect(locators.quotaStatusChip(limitText)).toHaveText("Disabled");
        expect(counts[OP_UPSERT_QUOTA]).toBe(1);

        await locators.deleteQuotaBtn(limitText).click();
        await expect(locators.deleteQuotaDialog).toBeVisible({ timeout: 15000 });
        await expect(locators.deleteQuotaDialog).toContainText("tokens / month quota (tenant-wide)");
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.confirmDeleteQuotaBtn.click();
            await locators.deleteQuotaDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "Gateway Quotas - delete a quota", operationNames: [OP_DELETE_QUOTA, OP_LIST_QUOTAS], timeoutMs: 45000 }
        );
        await expect(locators.quotaRow(limitText)).toHaveCount(0, { timeout: 30000 });
        expect(counts[OP_DELETE_QUOTA]).toBe(1);
      } finally {
        await deleteQuotaIfPresent(locators, limit);
      }
    }
  );

  test(
    "Gateway Data & privacy - change the DLP mode to a different option, verify Save changes enables, put the stored mode back, verify Save changes disables again and no settings update is sent",
    { tag: ["@dev", "@regression", "@negative", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, [OP_UPDATE_SETTINGS]);
      const locators = await openGatewayTab(page);

      await openSection(locators, "privacy");
      await expect(locators.privacySaveBtn).toBeDisabled({ timeout: 15000 });

      // The trigger shows the stored option's label, which is what has to be
      // re-picked afterwards; the alternative is chosen against it so the pick
      // always actually changes the value on whichever mode the tenant stores.
      await expect(locators.dlpModeSelect).toHaveText(/\S/, { timeout: 15000 });
      const stored = ((await locators.dlpModeSelect.innerText()) ?? "").trim();
      const alternative = stored === "Off" ? "Detect — record only" : "Off";

      await pickSelectOption(locators, locators.dlpModeSelect, alternative);
      await expect(locators.privacySaveBtn).toBeEnabled({ timeout: 15000 });

      await pickSelectOption(locators, locators.dlpModeSelect, stored);
      await expect(locators.privacySaveBtn).toBeDisabled({ timeout: 15000 });
      expect(counts[OP_UPDATE_SETTINGS]).toBe(0);
    }
  );
});
