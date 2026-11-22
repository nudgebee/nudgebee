// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { GatewayConfigLocators } from "./gatewayConfigLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

// app/src/api1/gateway-config/index.ts names each llm_gateway_* call this way.
export const OP_SET_TIER = "SetTier";
export const OP_LIST_RULES = "ListRoutingRules";
export const OP_CREATE_RULE = "CreateRoutingRule";
export const OP_UPDATE_RULE = "UpdateRoutingRule";
export const OP_DELETE_RULE = "DeleteRoutingRule";
export const OP_LIST_QUOTAS = "ListRateLimits";
export const OP_UPSERT_QUOTA = "UpsertRateLimit";
export const OP_DELETE_QUOTA = "DeleteRateLimit";
export const OP_UPDATE_SETTINGS = "UpdateGatewaySettings";

export type GatewaySection = "tiers" | "routing" | "quotas" | "privacy";

function uniqueSuffix(): string {
  return `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

// A model name no client will ever request, so a rule matching it — even one
// left enabled — rewrites nothing on the shared dev tenant. It doubles as the
// row's identity, since rules carry no name.
export function uniqueRuleModel(): string {
  return `nb-e2e-gw-${uniqueSuffix()}`;
}

// Five digits keeps the value well behind every real rule (the form defaults to
// 100, and lower is evaluated first) while staying distinct across runs.
export function uniquePriority(): number {
  return 10000 + Math.floor(Math.random() * 89999);
}

// Seven digits: large enough that even an enabled quota could never trip on dev
// traffic, and unique enough to identify its row in a table that has no name.
export function uniqueQuotaLimit(): number {
  return 1000000 + Math.floor(Math.random() * 8999999);
}

// The Limit cell renders through toLocaleString, so the digits arrive with a
// locale's thousands separators between them — matched as optional.
export function limitPattern(limit: number): RegExp {
  return new RegExp(String(limit).split("").join("[,.\\s\\u00a0]?"));
}

// Logs in, opens the Nubi panel and lands on AI & Tools > Gateway, which opens
// on its Tiers section. A tenant whose gateway upstream is unreachable renders
// the critical banner in place of rows, which is asserted by name so that
// failure mode does not surface as every control being absent.
export async function openGatewayTab(page: Page): Promise<GatewayConfigLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new GatewayConfigLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.openAITools(locators.gatewayTab);
  await locators.gatewayTab.click();
  await locators.root.waitFor({ state: "visible", timeout: 30000 });
  await waitForSectionLoaded(locators, "tiers");
  return locators;
}

// Settles on whichever loaded shape a section lands in — rows or its empty
// state — and then insists no load error is showing, so a non-retrying read
// taken afterwards cannot catch the table mid-fetch.
export async function waitForSectionLoaded(locators: GatewayConfigLocators, section: GatewaySection): Promise<void> {
  if (section === "tiers") {
    await expect(locators.tierRows().first().or(locators.tiersEmptyState)).toBeVisible({ timeout: 30000 });
  } else if (section === "routing") {
    await expect(locators.ruleRows().first().or(locators.routingEmptyState)).toBeVisible({ timeout: 30000 });
  } else if (section === "quotas") {
    await expect(locators.quotaRows().first().or(locators.quotaEmptyState)).toBeVisible({ timeout: 30000 });
  } else {
    // Data & privacy renders a spinner until llm_gateway_get_settings resolves,
    // so the DLP trigger is the first control that exists only once it has.
    await expect(locators.dlpModeSelect).toBeVisible({ timeout: 30000 });
  }
  await expect(locators.loadErrorBanner, "the Gateway tab reported a load error from the gateway upstream").toHaveCount(0);
}

const SECTION_OPTION: Record<GatewaySection, (l: GatewayConfigLocators) => Locator> = {
  tiers: (l) => l.tiersOption,
  routing: (l) => l.routingOption,
  quotas: (l) => l.quotasOption,
  privacy: (l) => l.privacyOption,
};

export async function openSection(locators: GatewayConfigLocators, section: GatewaySection): Promise<void> {
  const option = SECTION_OPTION[section](locators);
  await option.click();
  await expect(option).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
  await waitForSectionLoaded(locators, section);
}

// Leaves the tab and comes back, which unmounts GatewayConfigTab and makes every
// section refetch — the only way to tell a persisted row from one still held in
// React state. The remount lands on Tiers again, since the section is local state.
export async function remountGatewayTab(locators: GatewayConfigLocators): Promise<void> {
  await locators.agentsTab.click();
  await locators.root.waitFor({ state: "detached", timeout: 20000 });
  await locators.gatewayTab.click();
  await locators.root.waitFor({ state: "visible", timeout: 30000 });
  await waitForSectionLoaded(locators, "tiers");
}

// Opens a ds/Select and commits one option. A single-selection panel closes on
// its own after the pick, and the trigger then shows the chosen label, which is
// the signal that the selection landed rather than a fixed pause.
export async function pickSelectOption(locators: GatewayConfigLocators, trigger: Locator, label: string): Promise<void> {
  await trigger.click();
  await locators.openListbox.waitFor({ state: "visible", timeout: 15000 });
  await locators.selectOption(label).click();
  await locators.openListbox.waitFor({ state: "detached", timeout: 15000 });
  await expect(trigger).toContainText(label, { timeout: 15000 });
}

export interface RuleDraft {
  model: string;
  priority: number;
  enabled: boolean;
}

export async function openNewRuleDialog(locators: GatewayConfigLocators): Promise<void> {
  await locators.newRuleBtn.click();
  await locators.newRuleDialog.waitFor({ state: "visible", timeout: 15000 });
  await locators.rulePriorityInput.waitFor({ state: "visible", timeout: 15000 });
}

export async function fillRuleForm(locators: GatewayConfigLocators, draft: RuleDraft): Promise<void> {
  await locators.rulePriorityInput.fill(String(draft.priority));
  await expect(locators.rulePriorityInput).toHaveValue(String(draft.priority), { timeout: 10000 });
  await locators.ruleMatchModelInput.fill(draft.model);
  await expect(locators.ruleMatchModelInput).toHaveValue(draft.model, { timeout: 10000 });
  // A rule that rewrites nothing is a no-op the form refuses (nb-39222), so the rule rewrites to a target model.
  const targetModel = `${draft.model}-target`;
  await locators.ruleTargetModelInput.fill(targetModel);
  await expect(locators.ruleTargetModelInput).toHaveValue(targetModel, { timeout: 10000 });
  await locators.ruleEnabledSwitch.setChecked(draft.enabled);
  await expect(locators.ruleEnabledSwitch).toBeChecked({ checked: draft.enabled, timeout: 10000 });
}

// Fills and creates a routing rule, asserting the create and the reload that
// follows it both left the browser and came back clean before the row is looked for.
export async function createRule(page: Page, locators: GatewayConfigLocators, draft: RuleDraft): Promise<void> {
  await openNewRuleDialog(locators);
  await fillRuleForm(locators, draft);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.ruleSaveBtn.click();
      await locators.ruleDialog.waitFor({ state: "detached", timeout: 30000 });
    },
    { testName: "Gateway Routing rules - create a rule", operationNames: [OP_CREATE_RULE, OP_LIST_RULES], timeoutMs: 45000 }
  );
  await expect(locators.ruleRow(draft.model)).toBeVisible({ timeout: 30000 });
}

export interface QuotaDraft {
  metric: "requests" | "tokens" | "cost";
  period: "minute" | "hour" | "day" | "month";
  limit: number;
  enabled: boolean;
}

export async function openNewQuotaDialog(locators: GatewayConfigLocators): Promise<void> {
  await locators.newQuotaBtn.click();
  await locators.newQuotaDialog.waitFor({ state: "visible", timeout: 15000 });
  await locators.quotaLimitInput.waitFor({ state: "visible", timeout: 15000 });
}

export async function fillQuotaForm(locators: GatewayConfigLocators, draft: QuotaDraft): Promise<void> {
  await pickSelectOption(locators, locators.quotaMetricSelect, draft.metric);
  await pickSelectOption(locators, locators.quotaPeriodSelect, draft.period);
  await locators.quotaLimitInput.fill(String(draft.limit));
  await expect(locators.quotaLimitInput).toHaveValue(String(draft.limit), { timeout: 10000 });
  await locators.quotaEnabledSwitch.setChecked(draft.enabled);
  await expect(locators.quotaEnabledSwitch).toBeChecked({ checked: draft.enabled, timeout: 10000 });
}

export async function createQuota(page: Page, locators: GatewayConfigLocators, draft: QuotaDraft): Promise<void> {
  await openNewQuotaDialog(locators);
  await fillQuotaForm(locators, draft);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.quotaSaveBtn.click();
      await locators.quotaDialog.waitFor({ state: "detached", timeout: 30000 });
    },
    { testName: "Gateway Quotas - create a quota", operationNames: [OP_UPSERT_QUOTA, OP_LIST_QUOTAS], timeoutMs: 45000 }
  );
  await expect(locators.quotaRow(limitPattern(draft.limit))).toBeVisible({ timeout: 30000 });
}

// Best-effort close of any dialog a failed step may have left open, so the
// cleanup clicks below are not swallowed by a modal backdrop.
async function closeDialogIfOpen(dialog: Locator, cancelBtn: Locator): Promise<void> {
  // A test that never opened this dialog is the normal case here, so absence
  // has to come back as false rather than throw.
  const open = await dialog.isVisible().catch(() => false);
  if (!open) return;
  await cancelBtn.click();
  await dialog.waitFor({ state: "detached", timeout: 10000 });
}

async function closeGatewayDialogs(locators: GatewayConfigLocators): Promise<void> {
  await closeDialogIfOpen(locators.deleteRuleDialog, locators.cancelDeleteRuleBtn);
  await closeDialogIfOpen(locators.ruleDialog, locators.ruleCancelBtn);
  await closeDialogIfOpen(locators.deleteQuotaDialog, locators.cancelDeleteQuotaBtn);
  await closeDialogIfOpen(locators.quotaDialog, locators.quotaCancelBtn);
  await closeDialogIfOpen(locators.tierDialog, locators.tierCancelBtn);
}

// Teardown for a rule this suite created. It re-opens the Routing rules section
// itself, since a case may end on Tiers after a remount, scopes the delete to
// the row's own control rather than a loose text match, and asserts the row is
// gone so a silently failed cleanup cannot leave the next run a stray rule.
export async function deleteRuleIfPresent(locators: GatewayConfigLocators, model: string): Promise<void> {
  try {
    await closeGatewayDialogs(locators);
    await openSection(locators, "routing");
    const deleteBtn = locators.deleteRuleBtn(model);
    // A test that never got as far as saving leaves nothing to delete, so
    // absence here is a normal outcome and must come back as false.
    const present = await deleteBtn
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;
    await deleteBtn.click();
    await locators.confirmDeleteRuleBtn.waitFor({ state: "visible", timeout: 15000 });
    await locators.confirmDeleteRuleBtn.click();
    await expect(locators.ruleRow(model)).toHaveCount(0, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the generated routing rule could not be deleted: ${error}`);
  }
}

export async function deleteQuotaIfPresent(locators: GatewayConfigLocators, limit: number): Promise<void> {
  try {
    await closeGatewayDialogs(locators);
    await openSection(locators, "quotas");
    const deleteBtn = locators.deleteQuotaBtn(limitPattern(limit));
    // Same probe as the rule teardown: nothing saved means nothing to delete.
    const present = await deleteBtn
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;
    await deleteBtn.click();
    await locators.confirmDeleteQuotaBtn.waitFor({ state: "visible", timeout: 15000 });
    await locators.confirmDeleteQuotaBtn.click();
    await expect(locators.quotaRow(limitPattern(limit))).toHaveCount(0, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the generated quota could not be deleted: ${error}`);
  }
}
