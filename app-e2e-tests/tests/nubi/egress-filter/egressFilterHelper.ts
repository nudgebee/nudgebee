// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { EgressFilterLocators } from "./egressFilterLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";

export const OP_UPSERT_PATTERN = "UpsertEgressPattern";
export const OP_DELETE_PATTERN = "DeleteEgressPattern";
export const OP_UPDATE_CONFIG = "UpdateEgressFilterConfig";
export const OP_CLEAR_OVERRIDE = "ClearEgressOverride";

// The only thing this suite is allowed to write is a custom pattern it created
// itself. Mode, the enable flag, the PII block and the excluded-agent list are
// tenant-wide policy: they get read and dirtied, never saved. Every test asserts
// these two stayed at zero, which is what keeps a shared dev tenant safe.
export const CONFIG_WRITE_OPS = [OP_UPDATE_CONFIG, OP_CLEAR_OVERRIDE];

function uniqueSuffix(): string {
  return `${Date.now()}${Math.random().toString(36).slice(2, 6)}`;
}

export function uniquePatternName(): string {
  return `nb_e2e_egress_${uniqueSuffix()}`;
}

// Deliberately a literal token with no regex metacharacters: it compiles to a
// plain string match that no real outbound payload can contain, so a pattern
// briefly left enabled cannot flag live traffic on the shared dev tenant. The
// prefix is a parameter so an edit case can assert the old value is gone
// without the two tokens sharing a substring.
export function uniquePatternRegex(prefix = "NBE2E"): string {
  return `${prefix}${uniqueSuffix()}`;
}

export function uniqueAgentName(): string {
  return `nb-e2e-agent-${uniqueSuffix()}`;
}

// Logs in, opens the Nubi panel and lands on AI & Tools > Egress Filter
// (relocated from Settings, docs/ia-consolidation-plan.md PR 3).
export async function openEgressFilterTab(page: Page): Promise<EgressFilterLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new EgressFilterLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.aiToolsBtn.click();
  await locators.egressFilterTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.egressFilterTab.click();
  // The tab renders a spinner until egressfilter_get resolves, so the toggle is
  // the first element that exists only once the real config has landed.
  await locators.secretFilterToggle.waitFor({ state: "visible", timeout: 30000 });
  return locators;
}

// Leaves the tab and comes back, which unmounts EgressFilterTab and makes it
// refetch — the only way to tell a persisted pattern from one still held in
// React state, and the only way to discard an unsaved form edit.
export async function remountEgressFilterTab(locators: EgressFilterLocators): Promise<void> {
  await locators.agentsTab.click();
  await locators.secretFilterToggle.waitFor({ state: "detached", timeout: 20000 });
  await locators.egressFilterTab.click();
  await locators.secretFilterToggle.waitFor({ state: "visible", timeout: 30000 });
}

export interface PatternDraft {
  name: string;
  regex: string;
  enabled: boolean;
}

export async function openNewPatternDialog(locators: EgressFilterLocators): Promise<void> {
  await locators.newPatternBtn.click();
  await locators.patternNameInput.waitFor({ state: "visible", timeout: 15000 });
}

export async function setPatternEnabled(locators: EgressFilterLocators, enabled: boolean): Promise<void> {
  await locators.patternEnabledSwitch.setChecked(enabled);
  await expect(locators.patternEnabledSwitch).toBeChecked({ checked: enabled, timeout: 10000 });
}

export async function fillPatternForm(locators: EgressFilterLocators, draft: PatternDraft): Promise<void> {
  await locators.patternNameInput.fill(draft.name);
  await expect(locators.patternNameInput).toHaveValue(draft.name, { timeout: 10000 });
  await locators.patternRegexInput.fill(draft.regex);
  await expect(locators.patternRegexInput).toHaveValue(draft.regex, { timeout: 10000 });
  await setPatternEnabled(locators, draft.enabled);
}

// Fills and saves a new custom pattern, asserting the upsert actually left the
// browser and came back clean before the row is looked for.
export async function createPattern(page: Page, locators: EgressFilterLocators, draft: PatternDraft): Promise<void> {
  await openNewPatternDialog(locators);
  await fillPatternForm(locators, draft);
  await waitForGraphQLAndValidate(
    page,
    async () => {
      await locators.patternSaveBtn.click();
      await locators.patternDialog.waitFor({ state: "detached", timeout: 30000 });
    },
    { testName: "Egress Filter - create a custom detection pattern", operationNames: OP_UPSERT_PATTERN, timeoutMs: 45000 }
  );
  await expect(locators.patternRow(draft.name)).toBeVisible({ timeout: 30000 });
}

// Best-effort close of a pattern dialog a failed step may have left open, so
// cleanup's clicks are not swallowed by the modal backdrop.
async function closePatternDialogIfOpen(locators: EgressFilterLocators): Promise<void> {
  // A test that never opened the dialog is the normal case here, so absence has
  // to come back as false rather than throw.
  const open = await locators.patternDialog.isVisible().catch(() => false);
  if (!open) return;
  await locators.patternCancelBtn.click();
  await locators.patternDialog.waitFor({ state: "detached", timeout: 10000 });
}

// Teardown for a pattern this suite created. Scoped to the row's own delete
// control rather than a loose text match, and it asserts the row is gone so a
// silently failed cleanup cannot look like a success and leave the next run a
// duplicate name to trip over.
export async function deletePatternIfPresent(locators: EgressFilterLocators, name: string): Promise<void> {
  try {
    await closePatternDialogIfOpen(locators);
    const deleteBtn = locators.deletePatternBtn(name);
    // A test that never got as far as saving leaves nothing to delete, so
    // absence here is a normal outcome and must come back as false.
    const present = await deleteBtn
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;
    await deleteBtn.click();
    await locators.confirmDeleteBtn.waitFor({ state: "visible", timeout: 15000 });
    await locators.confirmDeleteBtn.click();
    await expect(locators.patternRow(name)).toHaveCount(0, { timeout: 30000 });
  } catch (error) {
    console.warn(`[cleanup] the generated egress pattern could not be deleted: ${error}`);
  }
}

// Counts outgoing GraphQL operations by name so a test can prove a mutation
// never left the browser. Watches several names at once, which every case here
// needs: each one has to clear both config-write operations as well as its own.
export function trackOperations(page: Page, opNames: string[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const opName of opNames) counts[opName] = 0;
  page.on("request", (req) => {
    if (req.method() !== "POST" || !req.url().includes("api/graphql")) return;
    const postData = req.postData();
    if (!postData) return;
    let sent: string[] = [];
    try {
      const payload = JSON.parse(postData);
      const operations = Array.isArray(payload) ? payload : [payload];
      sent = operations.map((op: { operationName?: string }) => op.operationName ?? "");
    } catch {
      // A body that is not JSON still carries the operation name in its text.
      sent = opNames.filter((opName) => postData.includes(opName));
    }
    for (const opName of opNames) {
      if (sent.includes(opName)) counts[opName] += 1;
    }
  });
  return counts;
}
