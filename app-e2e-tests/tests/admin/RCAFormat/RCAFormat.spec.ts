// Not for OSS

import { test, expect } from "@playwright/test";
import {
  BUDGETS_LIMITS_TAB_NAME,
  EMPTY_STATE_DESCRIPTION,
  EMPTY_STATE_TITLE,
  MAX_SAMPLED_ACCOUNTS,
  NO_RESULTS_TEXT,
  RCA_HEADER,
  RCA_SUBHEADER,
  SPEC_TIMEOUT_MS,
  TOAST_LOAD_FAILED,
  TOAST_SAVED,
  TOAST_SAVE_FAILED,
  WRITE_TIMEOUT_MS,
} from "./rcaFormatConstants";
import {
  deleteFirstLine,
  insertMarkerLine,
  openRCAFormat,
  reloadRCAFormat,
  restoreTemplateIfMarked,
  saveTemplate,
  uniqueMarker,
} from "./rcaFormatHelper";

// Admin -> AI & Tools -> RCA Format
// (app/src/components/llm/RCAFormatTab.jsx, mounted by
// app/src/components/llm/admin/RCAFormatAdminTab.jsx behind a required
// AdminAccountFilter). Unlike every sibling sub-tab this one has no tenant-wide
// view — the template is stored one per account — so the account picker is a
// precondition rather than a narrowing, and that shapes most of the cases here.
//
// This runs against a shared tenant, so exactly one case writes: it prepends a
// run-unique marker line to one account's template, proves the write survived a
// reload, then removes the line and saves again, with the restore repeated in a
// finally. Every other case stops before any mutation reaches the backend.

test.describe("Admin AI & Tools RCA Format", () => {
  test(
    "RCA Format sanity - deep-link to Admin AI & Tools RCA Format, verify the AI & Tools tab and the RCA Format sub-tab are both selected and the account picker renders",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);

      // The hash carries both levels, so landing on the right sub-tab is itself
      // part of what this asserts — a broken child fragment silently falls back
      // to the first sub-tab (Agents), which renders no account picker at all.
      await expect(locators.aiToolsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await expect(locators.rcaFormatTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      await expect(locators.accountSelect).toBeVisible({ timeout: 30000 });
      await expect(locators.accountSelect).toBeEnabled();
    }
  );

  test(
    "RCA Format sanity - open RCA Format without picking an account, verify the Select an account empty state explains the per-account storage and no editor or Save Changes control is mounted",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);

      await expect(locators.emptyState).toBeVisible({ timeout: 30000 });
      await expect(locators.emptyState).toContainText(EMPTY_STATE_TITLE);
      await expect(locators.emptyState).toContainText(EMPTY_STATE_DESCRIPTION);

      // RCAFormatAdminTab renders one branch or the other, never both, so the
      // editor and its Save control being absent is what proves the empty state
      // is the mounted branch rather than a banner painted above a live form.
      await expect(locators.editor).toHaveCount(0);
      await expect(locators.saveChangesBtn).toHaveCount(0);
      await expect(locators.rcaHeader).toHaveCount(0);
    }
  );

  test(
    "RCA Format - pick the first account, verify the template header and editor replace the empty state and Save Changes starts disabled on the freshly loaded template",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      await locators.chooseFirstAccount();

      await expect(locators.rcaHeader).toBeVisible({ timeout: 30000 });
      await expect(page.getByText(RCA_SUBHEADER, { exact: true }).first()).toBeVisible({ timeout: 30000 });
      await expect(locators.emptyState).toHaveCount(0, { timeout: 30000 });

      // The editor is seeded from getRcaFormat, falling back to the component's
      // DEFAULT_RCA_FORMAT when the account has none saved — either way it is
      // never blank, so an empty body means the fetch never painted.
      await expect(locators.editor).not.toBeEmpty({ timeout: 30000 });

      // isDirty compares against the baseline set by the load, so a just-loaded
      // template must leave Save disabled — an enabled button here would mean
      // the component thinks there is an unsaved edit nobody made.
      await expect(locators.saveChangesBtn).toBeVisible();
      await expect(locators.saveChangesBtn).toBeDisabled();

      // A load that failed still renders the editor, on empty content — pinning
      // the failure toast's absence is what separates the two.
      await expect(locators.toastWithText(TOAST_LOAD_FAILED)).toHaveCount(0);
    }
  );

  test(
    "RCA Format - pick the first account, type a character into the template, verify Save Changes becomes enabled, then delete the character and verify it goes disabled again",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      await locators.chooseFirstAccount();
      await expect(locators.saveChangesBtn).toBeDisabled();

      await locators.editor.click();
      await locators.editor.press("ControlOrMeta+Home");
      await locators.editor.pressSequentially("X", { delay: 10 });
      await expect(locators.saveChangesBtn).toBeEnabled({ timeout: 30000 });

      // Typing the edit away again rather than reloading is the point: Save is
      // gated on format !== savedFormat, so returning to the baseline content
      // has to disable it, not merely stop it from getting more enabled.
      await locators.editor.press("Backspace");
      await expect(locators.saveChangesBtn).toBeDisabled({ timeout: 30000 });

      // Nothing above calls updateRcaFormat, so neither save toast may appear —
      // that is what proves this whole case wrote nothing.
      await expect(locators.toastWithText(TOAST_SAVED)).toHaveCount(0);
      await expect(locators.toastWithText(TOAST_SAVE_FAILED)).toHaveCount(0);
    }
  );

  test(
    "RCA Format - pick the first account, prepend a run-unique marker line, save, reload and re-pick the account, verify the marker survived the reload, then delete it and verify the restored template saves clean",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(WRITE_TIMEOUT_MS);
      const marker = uniqueMarker();
      const locators = await openRCAFormat(page);
      const accountLabel = await locators.chooseFirstAccount();
      let marked = false;

      try {
        await insertMarkerLine(locators, marker);
        marked = true;
        await saveTemplate(locators, TOAST_SAVED);

        // The reload is what separates a persisted template from one still
        // sitting in React state: RCAFormatTab refetches getRcaFormat on mount,
        // and the hash survives the reload so the sub-tab resolves again. The
        // account has to be re-picked because the picker's own state does not.
        const reloaded = await reloadRCAFormat(page);
        await reloaded.chooseAccount(accountLabel);
        await expect(reloaded.editor).toContainText(marker, { timeout: 60000 });
        await expect(reloaded.saveChangesBtn).toBeDisabled();

        await deleteFirstLine(reloaded);
        await saveTemplate(reloaded, TOAST_SAVED);
        marked = false;

        // A second reload rather than reading the editor the restore left
        // behind: the on-screen content is this test's own edit until it has
        // been refetched, so only a freshly mounted page proves the marker is
        // gone from the stored template rather than just from the DOM.
        const afterRestore = await reloadRCAFormat(page);
        await afterRestore.chooseAccount(accountLabel);
        await expect(afterRestore.editor).not.toContainText(marker, { timeout: 60000 });
      } finally {
        if (marked) await restoreTemplateIfMarked(page, accountLabel, marker, TOAST_SAVED);
      }
    }
  );

  test(
    "RCA Format - pick an account and edit its template, switch the picker to a second account without saving, verify the editor reloads that account's own template and Save Changes is disabled again",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      const labels = await locators.accountOptionLabels();
      // A tenant with a single account cannot exercise a switch at all, and
      // failing here would report a missing fixture as a product defect.
      test.skip(labels.length < 2, "this tenant offers fewer than two accounts, so there is no second account to switch to");

      await locators.chooseAccount(labels[0]);
      await locators.editor.click();
      await locators.editor.press("ControlOrMeta+Home");
      await locators.editor.pressSequentially("X", { delay: 10 });
      await expect(locators.saveChangesBtn).toBeEnabled({ timeout: 30000 });

      await locators.chooseAccount(labels[1]);

      // fetchRCAFormat clears format and savedFormat together before refetching,
      // so the incoming account arrives with a fresh baseline — a Save that
      // stayed enabled would mean the abandoned edit is still pending against
      // an account it was never typed into.
      await expect(locators.saveChangesBtn).toBeDisabled({ timeout: 60000 });
      await expect(locators.editor).not.toBeEmpty({ timeout: 30000 });

      // Abandoning the edit never calls updateRcaFormat, so the absence of a
      // save toast is the side-effect half of this case.
      await expect(locators.toastWithText(TOAST_SAVED)).toHaveCount(0);
      await expect(locators.toastWithText(TOAST_SAVE_FAILED)).toHaveCount(0);
    }
  );

  test(
    "RCA Format - open the account picker and search for one account by its exact name, verify that account is listed and the other offered accounts are filtered out",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      const labels = await locators.accountOptionLabels();
      expect(labels.length, "the account picker offered no accounts to search").toBeGreaterThan(0);

      const target = labels[0];
      await locators.openAccountOptions();
      await locators.optionSearch.fill(target);

      await expect(locators.option(target)).toBeVisible({ timeout: 30000 });

      // ds/Select auto-expands every surviving group while a search is active,
      // so an account filtered out is genuinely absent rather than merely
      // hidden inside a collapsed group it was never expanded out of.
      const others = labels.slice(1, MAX_SAMPLED_ACCOUNTS).filter((label) => label !== target && !label.includes(target));
      for (const other of others) {
        await expect(locators.option(other)).toHaveCount(0);
      }
    }
  );

  test(
    "RCA Format sanity - search the account picker for a name no account uses, verify the No results found message replaces the option rows and the editor stays unmounted",
    { tag: ["@dev", "@regression", "@negative", "@search"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      await locators.openAccountOptions();

      await locators.optionSearch.fill(uniqueMarker());

      // ds/Select prints this only when a search matched nothing; its sibling
      // copy for a picker offered no options at all is "No options available",
      // so the two states stay distinguishable.
      await expect(locators.optionList.getByText(NO_RESULTS_TEXT, { exact: true })).toBeVisible({ timeout: 30000 });
      await expect(locators.optionList.locator('[role="option"]')).toHaveCount(0);

      // A search that matches nothing must not commit anything either, so the
      // tab is still on its empty-state branch behind the open popover.
      await page.keyboard.press("Escape");
      await expect(locators.emptyState).toBeVisible({ timeout: 30000 });
      await expect(locators.editor).toHaveCount(0);
    }
  );

  test(
    "RCA Format sanity - navigate from RCA Format to the Budgets & Limits sub-tab and back, verify each sub-tab is selected in turn and RCA Format returns to its unpicked empty state",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(SPEC_TIMEOUT_MS);
      const locators = await openRCAFormat(page);
      await expect(locators.emptyState).toBeVisible({ timeout: 30000 });

      await locators.budgetsLimitsTab.click();
      await expect(locators.budgetsLimitsTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      // The sibling tab renders a different body, so RCA Format's own empty
      // state going away is what proves the strip actually swapped the body
      // rather than only repainting the selected tab.
      await expect(locators.emptyState).toHaveCount(0, { timeout: 60000 });
      await expect(page.getByRole("group", { name: BUDGETS_LIMITS_TAB_NAME })).toBeVisible({ timeout: 90000 });

      await locators.rcaFormatTab.click();
      await expect(locators.rcaFormatTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      await expect(locators.accountSelect).toBeVisible({ timeout: 60000 });
      // Coming back remounts RCAFormatAdminTab, whose accountId state starts
      // empty again — so the empty state, not a previously picked account, is
      // the correct landing state.
      await expect(locators.emptyState).toBeVisible({ timeout: 60000 });
      await expect(page.getByText(RCA_HEADER, { exact: true })).toHaveCount(0);
    }
  );
});
