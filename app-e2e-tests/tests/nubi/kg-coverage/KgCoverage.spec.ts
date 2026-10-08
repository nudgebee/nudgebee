// Not for OSS
import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { trackOperations } from "../egress-filter/egressFilterHelper";
import { FLOW_SOURCE_LABELS } from "./kgCoverageLocators";
import {
  OP_UPSERT_FILTER,
  accountLabel,
  captureOperationExchange,
  closeSettings,
  expectCheckboxesMirrorSavedFilter,
  expectedMatchCount,
  openCoverage,
  openCoverageWithAccounts,
  openKnowledgeGraph,
  searchAccounts,
  snapshotCoverage,
  unsafeSaveReason,
  waitForCoverageLoaded,
} from "./kgCoverageHelper";

// Saving a deselected account or flow source soft-deletes it from the tenant's graph at once, so no case saves a changed selection.
// Toggling cases assert zero upsert mutations; the save case first proves the screen equals the saved filter, making it a no-op.

test.describe("Nubi KG Coverage", () => {
  test(
    "Nubi KG Coverage sanity - open b-Cortex, select Knowledge Graph, open Settings, verify Coverage is the selected tab and shows its guidance, the Cloud accounts and Flow sources columns, the four flow sources, and enabled Cancel and Save",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverage(page, locators);

      await expect(locators.coverageTab).toHaveAttribute("aria-selected", "true");
      await expect(locators.manualDeclarationsTab).toHaveAttribute("aria-selected", "false");
      await expect(locators.manualDepsDescription).toHaveCount(0);
      // The rebuild cadence is product copy (nightly -> hourly), so only the sentence shape is pinned.
      await expect(locators.coverageDescription).toContainText(/Newly enabled items appear after the next \w+ rebuild/);

      await expect(locators.accountsColumn).toBeVisible();
      await expect(locators.flowSourcesColumn).toBeVisible();
      await expect(locators.flowSourceCheckboxes).toHaveCount(FLOW_SOURCE_LABELS.length);
      for (const label of FLOW_SOURCE_LABELS) {
        await expect(locators.flowSourceCheckbox(label)).toBeEnabled();
      }

      // The search box and account rows render only for a tenant with active accounts; otherwise the empty copy stands in.
      if (load.accounts.length > 0) {
        await expect(locators.accountSearchInput).toBeVisible();
        await expect(locators.accountCheckboxes).toHaveCount(load.accounts.length);
      } else {
        await expect(locators.noAccountsState).toBeVisible();
        await expect(locators.accountSearchInput).toHaveCount(0);
      }

      await expect(locators.coverageCancelBtn).toBeEnabled();
      await expect(locators.coverageSaveBtn).toBeEnabled();
    }
  );

  test(
    "Nubi KG Coverage sanity - open Settings, verify every account and flow source checkbox mirrors the saved Knowledge Graph filter, with an empty saved list shown as everything checked",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverage(page, locators);

      await expectCheckboxesMirrorSavedFilter(locators, load);
    }
  );

  test(
    "Nubi KG Coverage - search accounts by an account's name, verify only the accounts matching that name are listed and that account is among them, clear the search, verify every account is listed again",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverageWithAccounts(page, locators);
      const named = load.accounts.find((acc) => (acc.account_name ?? "").trim().length > 0);
      expect(named, "no active cloud account has a name to search by").toBeTruthy();
      const term = named!.account_name!.trim();
      const expected = expectedMatchCount(load.accounts, term);

      await searchAccounts(locators, term);
      await expect(locators.accountCheckboxes).toHaveCount(expected, { timeout: 15000 });
      await expect(locators.accountCheckbox(accountLabel(named!))).toBeVisible();

      await locators.accountSearchInput.fill("");
      await expect(locators.accountSearchInput).toHaveValue("");
      await expect(locators.accountCheckboxes).toHaveCount(load.accounts.length, { timeout: 15000 });
    }
  );

  test(
    "Nubi KG Coverage - search accounts by a cloud provider typed in upper case, verify the match is case-insensitive and lists every account for that provider, clear with the search X, verify every account is listed and the box is empty",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverageWithAccounts(page, locators);
      const withProvider = load.accounts.find((acc) => (acc.cloud_provider ?? "").trim().length > 0);
      expect(withProvider, "no active cloud account reports a cloud provider").toBeTruthy();
      const provider = withProvider!.cloud_provider!.trim();
      const term = provider.toUpperCase();
      const expected = expectedMatchCount(load.accounts, term);
      const sameProvider = load.accounts.filter((acc) => (acc.cloud_provider ?? "").trim().toLowerCase() === provider.toLowerCase()).length;
      expect(expected).toBeGreaterThanOrEqual(sameProvider);

      await searchAccounts(locators, term);
      await expect(locators.accountCheckboxes).toHaveCount(expected, { timeout: 15000 });
      await expect(locators.accountCheckbox(accountLabel(withProvider!))).toBeVisible();

      await locators.accountSearchClearBtn.click();
      await expect(locators.accountSearchInput).toHaveValue("", { timeout: 10000 });
      await expect(locators.accountCheckboxes).toHaveCount(load.accounts.length, { timeout: 15000 });
    }
  );

  test(
    "Nubi KG Coverage - search accounts for a term no account carries, verify the no-match message names the term, no account row is listed and the four flow sources are untouched",
    { tag: ["@dev", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverageWithAccounts(page, locators);
      const term = `nb-e2e-kgcov-nomatch-${Date.now()}`;
      expect(expectedMatchCount(load.accounts, term)).toBe(0);
      const flowBefore = (await snapshotCoverage(locators)).flowSources;

      await searchAccounts(locators, term);
      await expect(locators.noAccountsMatch).toBeVisible({ timeout: 15000 });
      await expect(locators.noAccountsMatch).toContainText(term);
      await expect(locators.accountCheckboxes).toHaveCount(0);
      expect((await snapshotCoverage(locators)).flowSources).toEqual(flowBefore);
    }
  );

  test(
    "Nubi KG Coverage - toggle a flow source and an account, verify both checkboxes flip, press Cancel, verify Settings closes with no save sent, reopen Settings, verify both are back to their saved state",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_UPSERT_FILTER]);
      const locators = await openKnowledgeGraph(page);
      await openCoverageWithAccounts(page, locators);
      const before = await snapshotCoverage(locators);
      const flowBox = locators.flowSourceCheckbox("eBPF");
      const accountBox = locators.accountCheckboxes.first();
      const flowWas = await flowBox.isChecked();
      const accountWas = await accountBox.isChecked();

      await flowBox.setChecked(!flowWas);
      await accountBox.setChecked(!accountWas);
      await expect(flowBox).toBeChecked({ checked: !flowWas });
      await expect(accountBox).toBeChecked({ checked: !accountWas });

      await locators.coverageCancelBtn.click();
      await locators.settingsDialog.waitFor({ state: "detached", timeout: 20000 });
      expect(counts[OP_UPSERT_FILTER]).toBe(0);

      await openCoverage(page, locators);
      expect(await snapshotCoverage(locators)).toEqual(before);
      expect(counts[OP_UPSERT_FILTER]).toBe(0);
    }
  );

  test(
    "Nubi KG Coverage - toggle a flow source, switch to Manual Declarations and back to Coverage, verify the tab reloads with the flow source back to its saved state and no save is sent",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_UPSERT_FILTER]);
      const locators = await openKnowledgeGraph(page);
      await openCoverage(page, locators);
      const flowBox = locators.flowSourceCheckbox("Traces");
      const was = await flowBox.isChecked();

      await flowBox.setChecked(!was);
      await expect(flowBox).toBeChecked({ checked: !was });

      await locators.manualDeclarationsTab.click();
      await expect(locators.manualDepsDescription).toBeVisible({ timeout: 30000 });
      await expect(locators.coverageDescription).toHaveCount(0);

      await locators.coverageTab.click();
      await waitForCoverageLoaded(locators);
      await expect(locators.coverageTab).toHaveAttribute("aria-selected", "true");
      await expect(flowBox).toBeChecked({ checked: was });
      expect(counts[OP_UPSERT_FILTER]).toBe(0);
    }
  );

  test(
    "Nubi KG Coverage - enter an account search and toggle a flow source, close Settings with the X, reopen Settings, verify the search box is empty, every account is listed and the flow source is back to its saved state",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_UPSERT_FILTER]);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverageWithAccounts(page, locators);
      const flowBox = locators.flowSourceCheckbox("New Relic APM");
      const was = await flowBox.isChecked();

      await searchAccounts(locators, `nb-e2e-kgcov-${Date.now()}`);
      await expect(locators.noAccountsMatch).toBeVisible({ timeout: 15000 });
      await flowBox.setChecked(!was);
      await expect(flowBox).toBeChecked({ checked: !was });

      await closeSettings(locators);
      await openCoverage(page, locators);
      await expect(locators.accountSearchInput).toHaveValue("");
      await expect(locators.accountCheckboxes).toHaveCount(load.accounts.length, { timeout: 15000 });
      await expect(flowBox).toBeChecked({ checked: was });
      expect(counts[OP_UPSERT_FILTER]).toBe(0);
    }
  );

  test(
    "Nubi KG Coverage - open Settings on the saved filter, press Save without changes, verify one save is sent that removes no account or flow source, the saved toast shows and Settings closes, reopen Settings, verify every checkbox still matches the saved filter",
    { tag: ["@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const counts = trackOperations(page, [OP_UPSERT_FILTER]);
      const locators = await openKnowledgeGraph(page);
      const load = await openCoverage(page, locators);
      // Saving on such a tenant would soft-delete real graph data, so the case must not run there; this is a safety gate, not a flake mask.
      const unsafe = unsafeSaveReason(load);
      test.skip(unsafe !== null, `unchanged Save would not be a no-op: ${unsafe}`);
      // With the gate above, nothing on screen differing from what is saved makes the save below a no-op on the shared tenant.
      await expectCheckboxesMirrorSavedFilter(locators, load);
      const before = await snapshotCoverage(locators);
      const allAccountsChecked = before.accounts.length > 0 && before.accounts.every((s) => s.checked);
      const allFlowSourcesChecked = before.flowSources.every((s) => s.checked);

      const exchange = captureOperationExchange(page, OP_UPSERT_FILTER);
      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.coverageSaveBtn.click();
          // The success toast lives only 3s (ds/Toast), so it is awaited alongside the close rather than after the watcher settles.
          await Promise.all([
            expect(locators.toastWithText(/Knowledge Graph settings saved/)).toBeVisible({ timeout: 15000 }),
            locators.settingsDialog.waitFor({ state: "detached", timeout: 30000 }),
          ]);
        },
        { testName: "Nubi KG Coverage - save the unchanged filter", operationNames: OP_UPSERT_FILTER, timeoutMs: 45000 }
      );
      const { body, query } = await exchange;

      expect(body.errors, `${OP_UPSERT_FILTER} returned GraphQL errors`).toBeFalsy();
      const result = body?.data?.kg_upsert_tenant_filter;
      expect(result, `${OP_UPSERT_FILTER} returned no result`).toBeTruthy();
      expect(result.removed_accounts ?? []).toEqual([]);
      expect(result.removed_flow_sources ?? []).toEqual([]);
      // A full selection is sent as an empty list, which the backend reads as "all".
      if (allAccountsChecked) expect(query).toMatch(/account_ids\s*:\s*\[\s*\]/);
      if (allFlowSourcesChecked) expect(query).toMatch(/flow_sources\s*:\s*\[\s*\]/);
      expect(counts[OP_UPSERT_FILTER]).toBe(1);

      await expect(locators.kgCanvas).toBeVisible({ timeout: 30000 });

      const reloaded = await openCoverage(page, locators);
      await expectCheckboxesMirrorSavedFilter(locators, reloaded);
      expect(await snapshotCoverage(locators)).toEqual(before);
    }
  );
});
