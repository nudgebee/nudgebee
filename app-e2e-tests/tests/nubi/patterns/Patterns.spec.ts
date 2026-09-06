// Not for OSS
import { test, expect } from "@playwright/test";
import { NubiLocators } from "../nubiLocators";
import {
  PATTERN_FILTERS,
  openPatternsTab,
  readRowDescription,
  remountPatternsTab,
  requirePatternRows,
  requireUnpinnedRow,
  restoreUnpinned,
  search,
  selectFilter,
  unmatchableQuery,
} from "./patternsHelper";

// Nubi > b-Cortex > Memory > Patterns (app/src/ee/components/memory2/PatternsTab.jsx).
// Patterns are inferred by the backend, never entered by hand, so this tab has no
// create path — the only writes it offers are pin, edit and delete, all against
// rows the signed-in user already owns. Delete is not exercised at all: it is
// irreversible and would destroy inferred memory belonging to a shared dev user.
// The one case that writes pins a row and puts it back to unpinned afterwards.
test.describe("Nubi Patterns Tab", () => {
  test(
    "Nubi Patterns sanity - open b-Cortex, land on Memory > Patterns, verify the inferred-behaviours header, the search box and the five filter chips render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);

      await expect(locators.tabHeaderTitle).toBeVisible({ timeout: 20000 });
      await expect(locators.searchInput).toBeVisible({ timeout: 15000 });

      for (const label of PATTERN_FILTERS) {
        await expect(locators.filterChip(label)).toBeVisible({ timeout: 15000 });
      }
    }
  );

  test(
    "Nubi Patterns sanity - open the Patterns tab, verify All is the selected filter and Active, Fading, Stale and Pinned are all unselected",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);

      // ds/Chip renders an interactive chip as a MUI ButtonBase and puts its
      // selected state on aria-pressed, so this is the attribute that says which
      // filter is applied — the chip's colour is a class and says nothing.
      await expect(locators.filterChip("All")).toHaveAttribute("aria-pressed", "true", { timeout: 20000 });

      for (const label of PATTERN_FILTERS.filter((f) => f !== "All")) {
        await expect(locators.filterChip(label)).toHaveAttribute("aria-pressed", "false", { timeout: 15000 });
      }
    }
  );

  test(
    "Nubi Patterns - select the Pinned filter, verify Pinned becomes the selected chip and All is deselected, select All again, verify the default selection returns",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);

      await selectFilter(locators, "Pinned");
      await expect(locators.filterChip("All")).toHaveAttribute("aria-pressed", "false", { timeout: 15000 });

      await selectFilter(locators, "All");
      await expect(locators.filterChip("Pinned")).toHaveAttribute("aria-pressed", "false", { timeout: 15000 });
    }
  );

  test(
    "Nubi Patterns - step through the Active, Fading, Stale and Pinned filters, verify each selection replaces the previous one so exactly one chip stays pressed",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);

      for (const label of PATTERN_FILTERS.filter((f) => f !== "All")) {
        await selectFilter(locators, label);

        for (const other of PATTERN_FILTERS.filter((f) => f !== label)) {
          await expect(locators.filterChip(other)).toHaveAttribute("aria-pressed", "false", { timeout: 15000 });
        }
      }
    }
  );

  test(
    "Nubi Patterns - search for a token no pattern can contain, verify every row is filtered out and the no-results panel is shown, clear the search, verify the original rows come back",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);
      const rowsBefore = await locators.rowActionButtons.count();

      await search(locators, unmatchableQuery());
      await expect(locators.rowActionButtons).toHaveCount(0, { timeout: 20000 });
      await expect(locators.emptyPanel).toBeVisible({ timeout: 15000 });

      // The count coming back is what proves the search filtered rather than
      // dropped them: the empty panel alone is also what a tab holding no
      // patterns at all would show.
      await search(locators, "");
      await expect(locators.rowActionButtons).toHaveCount(rowsBefore, { timeout: 20000 });
    }
  );

  test(
    "Nubi Patterns - type a search and select the Stale filter, switch to the Sessions tab and back, verify the search box comes back empty and All is the selected filter again",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPatternsTab(page);

      await search(locators, unmatchableQuery());
      await selectFilter(locators, "Stale");

      // Search and filter live in PatternsTabContent's own state, so leaving the
      // tab unmounts them. This is the case that pins that down — a future
      // change lifting either into the modal would keep a stale filter applied
      // on the way back in, and nothing else here would notice.
      await remountPatternsTab(locators);

      await expect(locators.searchInput).toHaveValue("", { timeout: 20000 });
      await expect(locators.filterChip("All")).toHaveAttribute("aria-pressed", "true", { timeout: 15000 });
      await expect(locators.filterChip("Stale")).toHaveAttribute("aria-pressed", "false", { timeout: 15000 });
    }
  );

  test(
    "Nubi Patterns sanity - close b-Cortex from the Patterns tab, verify the dialog is gone and the Nubi chat box takes a message again",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPatternsTab(page);
      const nubi = new NubiLocators(page);

      await locators.closeDialogBtn.click();
      await expect(locators.bCortexDialog).toHaveCount(0, { timeout: 20000 });

      // b-Cortex is a MUI Dialog, so while it is up its container swallows every
      // click aimed at the panel behind it. Typing into the chat box is what
      // proves the overlay actually went away rather than merely turning
      // invisible. The draft is cleared and never sent, so this leaves no
      // conversation behind.
      const draft = `nb_e2e_patterns_${Date.now()}`;
      await nubi.chatTextbox.fill(draft);
      await expect(nubi.chatTextbox).toHaveValue(draft, { timeout: 15000 });
      await nubi.chatTextbox.fill("");
    }
  );

  test(
    "Nubi Patterns - pin the first unpinned pattern, leave the tab and come back, filter by Pinned, verify the pattern is listed as pinned after the refetch",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openPatternsTab(page);
      await requirePatternRows(locators, 1);
      await requireUnpinnedRow(locators);

      const target = locators.pinButtons.first();
      const description = await readRowDescription(locators, target);
      const pinnedBefore = await locators.unpinButtons.count();

      try {
        await target.click();
        // PatternsTab flips the row optimistically and only refetches when the
        // write failed, so this says the click was accepted, not that it landed.
        await expect(locators.unpinButtons).toHaveCount(pinnedBefore + 1, { timeout: 20000 });

        // The remount is the proof: it unmounts the tab and refetches from the
        // backend, so the row still reading as pinned afterwards can only come
        // from the stored row and not from the optimistic flip above.
        await remountPatternsTab(locators);
        await selectFilter(locators, "Pinned");
        await expect(locators.rowDescription(description)).toBeVisible({ timeout: 20000 });
        await expect(locators.rowActionsForDescription(description).getByRole("button", { name: "Unpin", exact: true })).toBeVisible({
          timeout: 15000,
        });
      } finally {
        await restoreUnpinned(locators, description);
      }
    }
  );

  test(
    "Nubi Patterns - open the edit modal on the first pattern, replace its text, cancel, verify the modal closes and the row still reads the original pattern",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPatternsTab(page);
      await requirePatternRows(locators, 1);

      const target = locators.editButtons.first();
      const original = await readRowDescription(locators, target);

      await target.click();
      await expect(locators.editTextarea).toBeVisible({ timeout: 20000 });
      // The draft is seeded from the row's stored description, which carries the
      // _intent_ / **entity** markers the list strips out when it renders them.
      // The two are therefore not the same string, so the check is that the
      // modal opened bound to a real row rather than to an empty draft.
      await expect(locators.editTextarea).not.toHaveValue("", { timeout: 15000 });

      const discarded = `nb_e2e_discarded_pattern_${Date.now()}`;
      await locators.editTextarea.fill(discarded);
      await expect(locators.editTextarea).toHaveValue(discarded, { timeout: 15000 });

      await locators.editCancelBtn.click();
      await expect(locators.editDialog).toHaveCount(0, { timeout: 20000 });

      await expect(locators.rowDescription(original)).toBeVisible({ timeout: 20000 });
      await expect(locators.bCortexDialog.getByText(discarded)).toHaveCount(0, { timeout: 15000 });
    }
  );
});
