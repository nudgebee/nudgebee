// Not for OSS
import { test, expect } from "@playwright/test";
import {
  SESSION_TYPES,
  advertisedItemCount,
  expectOnlyType,
  openSessionsTab,
  remountSessionsTab,
  requireSessionRows,
  selectSessionType,
} from "./sessionsHelper";

// Nubi > b-Cortex > Memory > Sessions (app/src/ee/components/memory2/SessionsTab.jsx).
// The tab's only write is a per-row delete, which is irreversible and would take a
// shared dev tenant's session memory with it, so nothing here clicks it — the cases
// below cover the read surface: the five-row cap, the type filter and the inline
// working-memory expansion. That leaves the suite with no fixtures to clean up and
// safe to run twice, but it does depend on the signed-in user having recent Nubi
// conversations; requireSessionRows() says so by name when they are missing.
// The type filter is client-side, so switching it cannot change the underlying rows
// mid-test — only a remount refetches.

test.describe("Nubi Sessions Tab", () => {
  test(
    "Nubi Sessions sanity - open b-Cortex, select the Sessions tab, verify the recent-activity header, the last-five description and the Session type toggle with All checked",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openSessionsTab(page);

      await expect(locators.recentActivityHeader).toBeVisible({ timeout: 20000 });
      await expect(locators.lastFiveDescription).toBeVisible({ timeout: 15000 });
      await expect(locators.typeToggle).toBeVisible({ timeout: 15000 });

      await expect(locators.typeOption("All")).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
      for (const type of SESSION_TYPES) {
        await expect(locators.typeOption(type)).toHaveAttribute("aria-checked", "false", { timeout: 15000 });
      }
    }
  );

  test(
    "Nubi Sessions sanity - open the Sessions tab, verify the list is capped at five rows and every row carries a title, a type chip, a working-memory count and the View and Delete controls",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openSessionsTab(page);
      const rows = await requireSessionRows(locators, 1);

      // MAX_VISIBLE in SessionsTab.jsx, and the same "Showing the last 5" the header
      // promises — the fetch asks for 25, so the cap is the tab's own slice.
      expect(rows).toBeLessThanOrEqual(5);

      // One of each per row: a stray chip or a missing count line fails the count
      // rather than passing on the rows that happen to be well formed.
      await expect(locators.typeChips).toHaveCount(rows);
      await expect(locators.workingMemoryCounts).toHaveCount(rows);
      await expect(locators.deleteBtns).toHaveCount(rows);
      await expect(locators.viewToggles).toHaveCount(rows);

      const chipLabels = (await locators.typeChips.allTextContents()).map((label) => label.trim());
      for (const label of chipLabels) {
        expect(SESSION_TYPES).toContain(label);
      }

      // A row whose conversation has no title falls back to its session id, so the
      // title is never blank — an empty one means the row rendered without its data.
      for (let index = 0; index < rows; index++) {
        await expect(locators.sessionTitle(index)).not.toBeEmpty();
      }
    }
  );

  test(
    "Nubi Sessions - click View on the first session, verify the Working memory panel opens and lists exactly as many entries as the row's working-memory count",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSessionsTab(page);
      await requireSessionRows(locators, 1);

      // Read before expanding, so the panel is asserted against the number the
      // collapsed row promised rather than against a number this test chose.
      const expected = await advertisedItemCount(locators, 0);

      await locators.viewToggles.first().click();
      await expect(locators.workingMemoryHeading).toHaveCount(1, { timeout: 20000 });

      if (expected === 0) {
        await expect(locators.noWorkingMemoryText).toBeVisible({ timeout: 15000 });
        await expect(locators.openPanelItems()).toHaveCount(0);
      } else {
        await expect(locators.openPanelItems()).toHaveCount(expected, { timeout: 15000 });
        await expect(locators.noWorkingMemoryText).toHaveCount(0);
      }
    }
  );

  test(
    "Nubi Sessions - expand a session and click Hide, verify the Working memory panel closes and the control returns to View",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSessionsTab(page);
      const rows = await requireSessionRows(locators, 1);

      await locators.viewToggles.first().click();
      await expect(locators.hideToggles).toHaveCount(1, { timeout: 20000 });
      await expect(locators.workingMemoryHeading).toHaveCount(1, { timeout: 20000 });

      await locators.hideToggles.first().click();

      // The Collapse is unmountOnExit, so a closed panel detaches rather than just
      // going invisible, and every row is back to offering View.
      await expect(locators.workingMemoryHeading).toHaveCount(0, { timeout: 20000 });
      await expect(locators.hideToggles).toHaveCount(0, { timeout: 15000 });
      await expect(locators.viewToggles).toHaveCount(rows, { timeout: 15000 });
    }
  );

  test(
    "Nubi Sessions - expand one session then expand a second, verify the first collapses so only one Working memory panel is open",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSessionsTab(page);
      const rows = await requireSessionRows(locators, 2);

      await locators.viewToggles.first().click();
      await expect(locators.hideToggles).toHaveCount(1, { timeout: 20000 });

      // The expanded row now offers Hide instead of View, so viewToggles.first() is
      // the next still-collapsed row rather than the one just opened.
      await expect(locators.viewToggles).toHaveCount(rows - 1, { timeout: 15000 });
      await locators.viewToggles.first().click();

      // SessionsTab holds a single expandedId, so opening a second row must close the
      // first — two open panels would mean that state stopped being exclusive.
      await expect(locators.workingMemoryHeading).toHaveCount(1, { timeout: 20000 });
      await expect(locators.hideToggles).toHaveCount(1, { timeout: 15000 });
      await expect(locators.viewToggles).toHaveCount(rows - 1, { timeout: 15000 });
    }
  );

  test(
    "Nubi Sessions - select the Investigation type filter, verify Investigation is the only checked option and All, General and Automation read unchecked",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openSessionsTab(page);

      await selectSessionType(locators, "Investigation");

      // ds/ToggleGroup is single-selection here, so exactly one option may carry
      // aria-checked=true; the other three are named so a stuck option is reported
      // by name instead of as a bare count.
      await expect(locators.typeOption("Investigation")).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
      for (const label of ["All", "General", "Automation"]) {
        await expect(locators.typeOption(label)).toHaveAttribute("aria-checked", "false", { timeout: 15000 });
      }
    }
  );

  test(
    "Nubi Sessions - filter by General, Investigation and Automation in turn, verify each list keeps only that type and stays capped at five, or shows the no-sessions empty state when that type has none",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openSessionsTab(page);
      await requireSessionRows(locators, 1);

      for (const type of SESSION_TYPES) {
        await selectSessionType(locators, type);

        // Retrying and web-first, so the row count below is never read off a list
        // that is still mid-update, and so an unfiltered row surviving the switch
        // fails here by name rather than as an off-by-one on the count.
        await expectOnlyType(locators, type);
        const rows = await locators.rowToggles.count();

        if (rows === 0) {
          // A type nobody has a session for must say so rather than leave the tab
          // blank — the empty state is the only thing separating the two.
          await expect(locators.emptyState).toBeVisible({ timeout: 15000 });
          continue;
        }

        expect(rows).toBeLessThanOrEqual(5);
        await expect(locators.emptyState).toHaveCount(0);
        await expect(locators.typeChips).toHaveCount(rows);

        const chipLabels = (await locators.typeChips.allTextContents()).map((label) => label.trim());
        for (const label of chipLabels) {
          expect(label).toBe(type);
        }
      }
    }
  );

  test(
    "Nubi Sessions - filter to a single type and switch back to All, verify the recent-activity list returns unchanged and All is the checked option",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSessionsTab(page);
      const rows = await requireSessionRows(locators, 1);
      const firstTitle = ((await locators.sessionTitle(0).textContent()) ?? "").trim();

      // The type of the first row, so the filter is guaranteed to match something and
      // the round trip is not just All → empty → All.
      const firstType = ((await locators.typeChips.first().textContent()) ?? "").trim();
      await selectSessionType(locators, firstType);

      // Proves the filter actually narrowed the list before the round trip back to
      // All. A plain visibility check here would be vacuous — the first row was
      // already on screen before the filter was applied.
      await expectOnlyType(locators, firstType);

      await selectSessionType(locators, "All");

      // Filtering is client-side — SessionsTab derives the visible rows from state it
      // already holds and never refetches on a toggle — so the unfiltered list has to
      // come back exactly as it was, not merely non-empty.
      await expect(locators.typeOption("All")).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
      await expect(locators.rowToggles).toHaveCount(rows, { timeout: 15000 });
      await expect(locators.sessionTitle(0)).toHaveText(firstTitle, { timeout: 15000 });
    }
  );

  test(
    "Nubi Sessions - expand a session, switch to the Patterns tab and back to Sessions, verify the list reloads with every session collapsed",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSessionsTab(page);
      await requireSessionRows(locators, 1);

      await locators.viewToggles.first().click();
      await expect(locators.workingMemoryHeading).toHaveCount(1, { timeout: 20000 });

      await remountSessionsTab(locators);

      // expandedId lives in the tab's own state, so leaving the tab has to drop it —
      // a panel still open here would mean the row list came back stale. The row count
      // is deliberately not compared: the remount refetches, and a conversation
      // started elsewhere on this shared tenant may legitimately have joined the list.
      await expect(locators.workingMemoryHeading).toHaveCount(0, { timeout: 20000 });
      await expect(locators.hideToggles).toHaveCount(0, { timeout: 15000 });
      await expect(locators.rowToggles.first()).toBeVisible({ timeout: 20000 });
    }
  );
});
