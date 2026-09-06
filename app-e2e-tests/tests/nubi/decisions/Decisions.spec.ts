// Not for OSS
import { test, expect } from "@playwright/test";
import {
  HISTORY_OFF_LABEL,
  HISTORY_ON_LABEL,
  openDecisionsTab,
  remountDecisionsTab,
  selectScope,
  toggleHistory,
  waitForPersonalDecisionsLoaded,
} from "./decisionsHelper";
import { CASE_TIMEOUT, CONTROL_TIMEOUT, LOAD_TIMEOUT, MOUNT_TIMEOUT } from "./decisionsConstants";

// Nubi > b-Cortex > Memory > Decisions (app/src/ee/components/memory2/DecisionsTab.jsx).
//
// The tab has no create path at all — decision rows are written by the agent during
// investigations — and its only two writes, Edit (append-supersede) and Save to
// global (promote to tenant memory), are both irreversible from the UI and land on
// rows this suite did not create. Every case below is therefore non-mutating, and the
// promote affordance is asserted absent in tenant scope but never clicked.
//
// The edit modal is deliberately NOT covered. Cases for its prefill, its empty and
// whitespace validation and its cancel path were written and run, and all three
// failed on dev for the same reason: the signed-in user has no decision rows, and
// nothing in the product can create one on demand. They were dropped rather than
// skipped or weakened. Covering that modal needs a seeded, disposable decision.
//
// Every case below therefore holds whether the list is empty or populated, which is
// what the second case pins down explicitly.
test.describe("Nubi Decisions Tab", () => {
  test(
    "Nubi Decisions sanity - open b-Cortex, select Memory > Decisions, verify the personal decision-history header, the immutability description and the Personal memory scope render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await expect(locators.personalHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });
      await expect(locators.immutabilityDescription).toBeVisible({ timeout: CONTROL_TIMEOUT });
      await expect(locators.scopeToggle).toBeVisible({ timeout: CONTROL_TIMEOUT });

      // BCortexModal mounts the tab with scope='mine', so Personal is the scope a
      // freshly-opened tab must land on and Global must not be the selected one.
      await expect(locators.scopeOption("Personal")).toHaveAttribute("aria-checked", "true", { timeout: CONTROL_TIMEOUT });
      await expect(locators.scopeOption("Global")).toHaveAttribute("aria-checked", "false", { timeout: CONTROL_TIMEOUT });

      // The history toggle only renders in Personal scope, and it opens on the
      // current-only view, so its label is the tab's initial showSuperseded state.
      await expect(locators.historyToggleBtn).toHaveText(HISTORY_OFF_LABEL, { timeout: CONTROL_TIMEOUT });
    }
  );

  test(
    "Nubi Decisions sanity - open the Decisions tab, verify the list settles into either decision rows or the No decisions yet empty state and never both",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      // DecisionsTab renders the row card and the empty state out of one ternary, so
      // exactly one of the two has to be on screen once the fetch has settled. Which
      // one depends on the signed-in user's decision history, so the case asserts the
      // exclusivity and then the contract of whichever shape it landed in.
      const rowCount = await locators.editBtns.count();
      const hasEmptyState = (await locators.emptyState.count()) === 1;
      expect(rowCount > 0).toBe(!hasEmptyState);

      if (rowCount > 0) {
        // The default view asks the backend for includeSuperseded:false, so a
        // Superseded chip here would mean a superseded row leaked into it.
        await expect(locators.supersededChips).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
      } else {
        await expect(locators.emptyState).toContainText("Decisions you make during investigations will land here.", { timeout: CONTROL_TIMEOUT });
      }
    }
  );

  test(
    "Nubi Decisions - open the Decisions tab, switch the memory scope to Global, verify the header retitles to tenant decisions and the history toggle is withdrawn",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await selectScope(locators, "Global");

      await expect(locators.tenantHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });
      await expect(locators.personalHeader).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
      await expect(locators.scopeOption("Personal")).toHaveAttribute("aria-checked", "false", { timeout: CONTROL_TIMEOUT });

      // Tenant scope always reads live rows only, so DecisionsTab withdraws the
      // superseded toggle there rather than rendering it inert.
      await expect(locators.historyToggleBtn).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
    }
  );

  test(
    "Nubi Decisions - switch the memory scope to Global and back to Personal, verify the personal header and the history toggle return",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await selectScope(locators, "Global");
      await expect(locators.tenantHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });

      await selectScope(locators, "Personal");

      await expect(locators.personalHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });
      await expect(locators.tenantHeader).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
      await expect(locators.historyToggleBtn).toHaveText(HISTORY_OFF_LABEL, { timeout: CONTROL_TIMEOUT });
      await waitForPersonalDecisionsLoaded(locators);
    }
  );

  test(
    "Nubi Decisions - switch the memory scope to Global, verify the scope banner reports either tenant-admin edit rights or the read-only tenant-managed lock and that no row offers promotion",
    { tag: ["@dev", "@regression", "@rbac"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await selectScope(locators, "Global");
      await expect(locators.tenantHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });

      // ScopeToggle renders exactly one of these while the scope is Global, chosen on
      // isTenantAdmin(). Which one is a property of the signed-in user rather than of
      // the tab, so the assertion is that one and only one of them is there.
      const adminNote = await locators.adminScopeNote.count();
      const lockChip = await locators.readOnlyLockChip.count();
      expect(adminNote + lockChip).toBe(1);

      // Promotion is a personal-scope action — a tenant row has nowhere to be
      // promoted to — so it must be absent here whichever branch the banner took.
      await expect(locators.promoteBtns).toHaveCount(0, { timeout: CONTROL_TIMEOUT });

      if (lockChip === 1) {
        // The lock chip is DecisionsTab resolving readOnly for a non-admin, which is
        // the same flag that suppresses the whole per-row action cluster.
        await expect(locators.editBtns).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
      }
    }
  );

  test(
    "Nubi Decisions - click Show all history, click Show current only, verify the toggle relabels each way and the list returns to the row count it started with",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      const currentOnlyRows = await locators.editBtns.count();

      await toggleHistory(locators, HISTORY_ON_LABEL);
      await waitForPersonalDecisionsLoaded(locators);
      // Showing history adds the superseded rows back to the live ones, so the list
      // can only grow — it can never drop a row that was already standing.
      expect(await locators.editBtns.count()).toBeGreaterThanOrEqual(currentOnlyRows);

      await toggleHistory(locators, HISTORY_OFF_LABEL);
      await expect(locators.editBtns).toHaveCount(currentOnlyRows, { timeout: LOAD_TIMEOUT });
    }
  );

  test(
    "Nubi Decisions - turn on Show all history, switch the scope to Global and back to Personal, verify the history toggle resets to Show all history",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await toggleHistory(locators, HISTORY_ON_LABEL);
      await waitForPersonalDecisionsLoaded(locators);

      await selectScope(locators, "Global");
      await expect(locators.tenantHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });
      await selectScope(locators, "Personal");

      // ScopeToggle's onChange resets showSuperseded alongside the scope, so coming
      // back has to land on the current-only view rather than on the history view the
      // tab was left in.
      await expect(locators.historyToggleBtn).toHaveText(HISTORY_OFF_LABEL, { timeout: MOUNT_TIMEOUT });
      await waitForPersonalDecisionsLoaded(locators);
      await expect(locators.supersededChips).toHaveCount(0, { timeout: CONTROL_TIMEOUT });
    }
  );

  test(
    "Nubi Decisions - turn on Show all history, switch to Global, leave for Patterns and return, verify the tab remounts on Personal scope with the history toggle reset",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(CASE_TIMEOUT);
      const locators = await openDecisionsTab(page);

      await toggleHistory(locators, HISTORY_ON_LABEL);
      await selectScope(locators, "Global");
      await expect(locators.tenantHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });

      await remountDecisionsTab(locators);

      // BCortexModal unmounts the tab body on a sub-tab switch, so coming back
      // re-mounts DecisionsTab with its scope='mine' prop and a fresh fetch — none of
      // the scope or history state the case left behind may survive it.
      await expect(locators.personalHeader).toBeVisible({ timeout: MOUNT_TIMEOUT });
      await expect(locators.scopeOption("Personal")).toHaveAttribute("aria-checked", "true", { timeout: CONTROL_TIMEOUT });
      await expect(locators.historyToggleBtn).toHaveText(HISTORY_OFF_LABEL, { timeout: CONTROL_TIMEOUT });
    }
  );

});
