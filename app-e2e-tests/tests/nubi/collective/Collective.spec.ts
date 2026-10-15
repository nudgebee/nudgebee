// Not for OSS
import { test, expect } from "@playwright/test";
import {
  clearSearch,
  longestWord,
  openCollectiveTab,
  requireCollectiveRows,
  searchFor,
  trackCollectiveDeletes,
  unmatchableQuery,
  waitForCollectiveSettled,
} from "./collectiveHelper";

// Nubi > b-Cortex > Memory > Collective (app/src/ee/components/memory2/CollectiveTab.jsx).
// The tab browses the tenant's shared agent memory, grouped by module, with an
// enter-to-search full-text search over it. Its one mutating control is a per-row Delete, and Collective
// rows are tenant-wide memory the agents wrote from real conversations — injected into
// everyone's future prompts, with no create path on this tab to put one back — so nothing
// here confirms a delete. The two delete cases open the confirmation, assert what it says,
// and Cancel.
//
// That leaves the suite with no fixtures to create and nothing to clean up, so every case
// is order-free and safe to run twice, which matters because CI sets retries: 1. The search
// cases type into the box and every case opens the tab fresh, so no case inherits a filter
// from the one before it.
test.describe("Nubi Collective Tab", () => {
  test(
    "Nubi Collective sanity - open b-Cortex, select the Memory group, select the Collective sub-tab, verify the shared-team-memory title and its tenant-scope description render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openCollectiveTab(page);

      await expect(locators.tabDescription).toBeVisible({ timeout: 20000 });
      await expect(locators.headerTitle).toHaveText("Collective — shared team memory", { timeout: 15000 });
      await expect(locators.searchInput).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Collective sanity - open the Collective tab, verify it settles on either module group cards or the no-collective-knowledge-yet empty state and raises no load-failure toast",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openCollectiveTab(page);

      // The tab has no rendered error state — a failed read toasts and leaves the list
      // empty — so without this the empty-state branch below would pass green on an
      // environment where the read is simply broken.
      await expect(locators.toastWithText("Could not load Collective")).toHaveCount(0);
      // On an unfiltered browse the filtered-miss title must not be the one that rendered.
      await expect(locators.noMatchEmptyState).toHaveCount(0);

      const rows = await locators.deleteButtons.count();
      if (rows === 0) {
        await expect(locators.noKnowledgeEmptyState).toBeVisible({ timeout: 15000 });
      } else {
        await expect(locators.noKnowledgeEmptyState).toHaveCount(0);
        // Rows are only ever rendered inside a module group card, so a tenant with rows
        // and no group card would mean the client-side grouping dropped them.
        await expect(locators.groupCountChips.first()).toBeVisible({ timeout: 15000 });
      }
    }
  );

  test(
    "Nubi Collective sanity - open the Collective tab, verify every module group card names its module and its count chip matches the number of insights it lists",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openCollectiveTab(page);
      await requireCollectiveRows(locators, 1);

      const groups = await locators.groupCountChips.count();
      expect(groups).toBeGreaterThan(0);

      let counted = 0;
      for (let i = 0; i < groups; i++) {
        const label = ((await locators.groupModuleLabel(i).textContent()) ?? "").trim();
        expect(label.length).toBeGreaterThan(0);

        const advertised = Number.parseInt(((await locators.groupCountChips.nth(i).textContent()) ?? "").trim(), 10);
        expect(Number.isNaN(advertised)).toBe(false);
        // groupByModule builds the chip from the same array it renders the rows from, so a
        // disagreement is a real grouping regression rather than a data-dependent skew.
        await expect(locators.groupDeleteButtons(i)).toHaveCount(advertised);
        counted += advertised;
      }

      // Every row belongs to exactly one group, so the per-group counts must account for
      // the whole list — this is what catches a row rendered into two groups at once.
      await expect(locators.deleteButtons).toHaveCount(counted);
    }
  );

  test(
    "Nubi Collective - take a word from the first insight and search for it, verify the tab issues a collective search for that word and lists a matching insight",
    { tag: ["@dev", "@regression", "@search", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openCollectiveTab(page);
      const before = await requireCollectiveRows(locators, 1);

      const headline = ((await locators.rowTitle(0).textContent()) ?? "").trim();
      const term = longestWord(headline);

      const { readQ } = await searchFor(locators, log, term);
      // The tab and the server agree that a non-empty q is what switches ai_memory_list
      // from a browse to a search, so the q on the wire is the evidence the search ran.
      expect(readQ).toBe(term);

      await expect(locators.noMatchEmptyState).toHaveCount(0);
      const after = await locators.deleteButtons.count();
      expect(after).toBeGreaterThan(0);
      // A search narrows the browse or holds it; it can never return more.
      expect(after).toBeLessThanOrEqual(before);

      // The term came verbatim out of a listed insight, so at least one result has to carry
      // it. Asserted across the results rather than on row 0, because the server ranks
      // full-text matches and the source row need not come back first.
      const texts = (await locators.rows.allTextContents()).map((t) => t.toLowerCase());
      expect(texts.some((t) => t.includes(term.toLowerCase()))).toBe(true);
    }
  );

  test(
    "Nubi Collective - search for a token no insight can contain, verify the no-matching-insights empty state and its try-a-different-query hint replace the module groups",
    { tag: ["@dev", "@regression", "@search", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openCollectiveTab(page);
      await requireCollectiveRows(locators, 1);

      const term = unmatchableQuery();
      const { readQ } = await searchFor(locators, log, term);
      expect(readQ).toBe(term);

      await expect(locators.noMatchEmptyState).toBeVisible({ timeout: 20000 });
      await expect(locators.noMatchHint).toBeVisible({ timeout: 15000 });
      // The two empty states are one ternary on whether the search box has content, so the
      // browse title rendering here would mean a filtered miss was mislabelled as an empty
      // tenant — which reads to the user as "your team has captured nothing".
      await expect(locators.noKnowledgeEmptyState).toHaveCount(0);
      await expect(locators.deleteButtons).toHaveCount(0);
      await expect(locators.groupCountChips).toHaveCount(0);
    }
  );

  test(
    "Nubi Collective - search for a token no insight can contain then clear the search box, verify the tab re-reads the unfiltered collective and the module groups come back",
    { tag: ["@dev", "@regression", "@search", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openCollectiveTab(page);
      const before = await requireCollectiveRows(locators, 1);
      const groupsBefore = await locators.groupCountChips.count();

      await searchFor(locators, log, unmatchableQuery());
      await expect(locators.noMatchEmptyState).toBeVisible({ timeout: 20000 });

      const readQ = await clearSearch(locators, log);
      // An empty box is a browse, not a search for the empty string — CollectiveTab calls
      // collectiveList, which sends no q at all, and fetches without waiting for Enter.
      expect(readQ).toBe("");

      await expect(locators.noMatchEmptyState).toHaveCount(0);
      await expect(locators.deleteButtons).toHaveCount(before);
      await expect(locators.groupCountChips).toHaveCount(groupsBefore);
    }
  );

  test(
    "Nubi Collective - type a multi-character query into the search box and press Enter, verify the keystrokes send no search and Enter sends a single collective search for the whole query",
    { tag: ["@dev", "@regression", "@search", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators, log } = await openCollectiveTab(page);
      await requireCollectiveRows(locators, 1);

      // Long enough that one-request-per-keystroke is unmistakable in the log.
      const term = unmatchableQuery();
      expect(term.length).toBeGreaterThan(5);

      const { readQ, readsBefore } = await searchFor(locators, log, term);
      expect(readQ).toBe(term);

      // Enter-to-search is what stops a typed word firing a search per character against a
      // full-text query on shared memory. Read after the results settled, so a per-keystroke
      // request would already have been issued and logged by now.
      expect(log.reads.length - readsBefore).toBe(1);
    }
  );

  test(
    "Nubi Collective - open the delete confirmation for the first insight, verify the dialog echoes that insight's headline and warns it will stop being injected into future prompts",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openCollectiveTab(page);
      await requireCollectiveRows(locators, 1);

      const headline = ((await locators.rowTitle(0).textContent()) ?? "").trim();
      expect(headline.length).toBeGreaterThan(0);

      await locators.deleteButtons.first().click();

      await expect(locators.deleteDialog).toBeVisible({ timeout: 20000 });
      await expect(locators.deleteDialogBody).toBeVisible({ timeout: 15000 });
      // The dialog names the row it is about, which is the only thing distinguishing it
      // from a confirmation that would delete whichever row the state last held.
      await expect(locators.deleteDialog).toContainText(headline, { timeout: 15000 });
      await expect(locators.deleteConfirmBtn).toBeVisible({ timeout: 15000 });

      // Left unpressed on purpose — see the file header. Closing it is what keeps the
      // insight in the tenant's shared memory.
      await locators.deleteCancelBtn.click();
      await expect(locators.deleteDialog).toHaveCount(0, { timeout: 20000 });
    }
  );

  test(
    "Nubi Collective - open the delete confirmation for the first insight and press Cancel, verify the dialog closes, no delete is sent and the insight is still listed",
    { tag: ["@dev", "@regression", "@functional", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      // Attached before the confirmation is opened so a delete fired by opening it — rather
      // than by confirming it — would be caught too.
      const deletes = trackCollectiveDeletes(page);
      const { locators } = await openCollectiveTab(page);
      const before = await requireCollectiveRows(locators, 1);

      const headline = ((await locators.rowTitle(0).textContent()) ?? "").trim();

      await locators.deleteButtons.first().click();
      await expect(locators.deleteDialog).toBeVisible({ timeout: 20000 });

      await locators.deleteCancelBtn.click();
      await expect(locators.deleteDialog).toHaveCount(0, { timeout: 20000 });

      await waitForCollectiveSettled(locators);
      await expect(locators.deleteButtons).toHaveCount(before);
      await expect(locators.rowTitle(0)).toHaveText(headline, { timeout: 15000 });
      // Asserted on the wire, not just on the row still being there: a delete that was sent
      // and failed server-side would also leave the row on screen.
      expect(deletes.count).toBe(0);
    }
  );

  test(
    "Nubi Collective - leave Collective for the Decisions sub-tab and come back, verify the account filter is withheld on tenant-wide Collective and offered on Decisions",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const { locators } = await openCollectiveTab(page);

      // Collective is tenant-wide and carries no usesAccount, and the node is rendered behind
      // that flag rather than hidden with CSS, so it is absent from the DOM.
      await expect(locators.accountSelect).toHaveCount(0, { timeout: 20000 });

      await locators.decisionsTab.click();
      await locators.tabDescription.waitFor({ state: "detached", timeout: 30000 });
      // Decisions carries usesAccount, so BCortexModal renders the filter beside the strip.
      await expect(locators.accountSelect).toBeVisible({ timeout: 20000 });

      await locators.collectiveTab.click();
      await expect(locators.tabDescription).toBeVisible({ timeout: 40000 });
      await waitForCollectiveSettled(locators);
      await expect(locators.accountSelect).toHaveCount(0, { timeout: 20000 });
    }
  );
});
