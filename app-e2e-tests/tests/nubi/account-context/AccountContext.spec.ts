// Not for OSS
import { test, expect } from "@playwright/test";
import {
  SAMPLE_CONTEXT_FILE,
  cleanupContext,
  deleteContext,
  fillContextForm,
  openAccountContextTab,
  openCardMenu,
  openCreateForm,
  remountAccountContextTab,
  selectWritableAccount,
  submitCreate,
  submitUpdate,
  uniqueContextName,
} from "./accountContextHelper";

// Nubi > b-Cortex > Knowledge > Account Context (app/src/components/llm/GlobalContextTab.jsx).
//
// One property of this surface shapes every case below: the backend caps an account at ONE
// account context, so the "Add Account Context" button is disabled the moment a record
// exists — every write case therefore runs against an account the suite has found to be
// empty, and puts it back the way it found it.
//
// Nothing without this suite's own name prefix is ever edited or deleted: on a shared dev
// tenant a pre-existing context belongs to somebody else, and the one-per-account cap means
// removing it to make room would be destroying the very fixture it stands for.

test.describe("Nubi Account Context Tab", () => {
  test(
    "Nubi Account Context sanity - open b-Cortex, select Knowledge > Account Context, verify the sub-tab is selected and the tab body renders its description and account filter",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openAccountContextTab(page);

      // Both strips are MUI Tabs, so aria-selected is what distinguishes "the sub-tab was
      // clicked" from "the click landed on the group tab and the default sub-tab rendered".
      await expect(locators.knowledgeGroupTab).toHaveAttribute("aria-selected", "true", { timeout: 20000 });
      await expect(locators.accountContextTab).toHaveAttribute("aria-selected", "true", { timeout: 20000 });

      await expect(locators.tabDescription).toBeVisible({ timeout: 20000 });
      // The header filter is what every write case below depends on being present, since
      // Account Context is one of b-Cortex's `usesAccount` sub-tabs.
      await expect(locators.accountSelectTrigger).toBeVisible({ timeout: 20000 });
    }
  );

  // The tenant-wide ("All accounts") read is deliberately NOT covered. BCortexModal's
  // handleAccountChange does setPickedAccountId(id || null), and AccountSelect signals
  // All-accounts by passing '' — so that '' collapses into the same null the "nothing
  // picked yet" state uses, and `pickedAccountId ?? accountId ?? ''` falls straight back
  // to the accountId prop. Opened from an account-scoped page, b-Cortex therefore cannot
  // be switched back to tenant-wide at all, which contradicts the "or clear back off"
  // intent stated in that component's own comment. Covering GlobalContextTab's
  // isTenantWide branch means driving a state the product cannot currently reach, so it
  // is left to the ticket rather than asserted here or written to expect the defect.

  test(
    "Nubi Account Context - select a single account, verify the empty state, its one-per-account hint and that Add is enabled",
    { tag: ["@dev", "@regression", "@validation"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openAccountContextTab(page);

      await selectWritableAccount(page, locators);

      // selectWritableAccount only ever returns an account with no context, so the rule
      // under test resolves to its enabled half — and the empty state is what proves the
      // precondition rather than the assertion being circular.
      await expect(locators.emptyState).toBeVisible({ timeout: 20000 });
      await expect(locators.emptyStateHint).toBeVisible({ timeout: 15000 });
      await expect(locators.addContextBtn).toBeEnabled({ timeout: 20000 });
    }
  );

  test(
    "Nubi Account Context - open Add Account Context, leave the name empty, verify Create stays disabled until a name is typed",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);
      await openCreateForm(locators);

      // Name is the only required field, and the form opens with all three empty, so this
      // is the modal's initial state rather than something the test has to arrange.
      await expect(locators.nameInput).toHaveValue("");
      await expect(locators.formCreateBtn).toBeDisabled({ timeout: 15000 });

      // Description and content alone must not satisfy the requirement.
      await fillContextForm(locators, { description: "described but unnamed" });
      await expect(locators.formCreateBtn).toBeDisabled({ timeout: 15000 });

      // Whitespace is trimmed before the check, so a space is still no name.
      await fillContextForm(locators, { name: "   " });
      await expect(locators.formCreateBtn).toBeDisabled({ timeout: 15000 });

      await fillContextForm(locators, { name: uniqueContextName() });
      await expect(locators.formCreateBtn).toBeEnabled({ timeout: 15000 });

      // Left uncommitted on purpose: this case is about the gate, and cancelling keeps the
      // account empty for whichever case runs next.
      await locators.formCancelBtn.click();
      await expect(locators.formModal).toBeHidden({ timeout: 20000 });
    }
  );

  test(
    "Nubi Account Context - open Add Account Context, fill a name, cancel the modal, verify the empty state is unchanged and nothing was created",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);
      const name = uniqueContextName();

      await openCreateForm(locators);
      await fillContextForm(locators, { name, content: "cancelled before it was ever submitted" });
      await locators.formCancelBtn.click();
      await expect(locators.formModal).toBeHidden({ timeout: 20000 });

      // Remounted before the assertion: a card missing from a list that was never refetched
      // would prove only that React state did not change, not that no record was written.
      await remountAccountContextTab(locators);
      await expect(locators.contextCard(name)).toHaveCount(0);
      await expect(locators.emptyState).toBeVisible({ timeout: 20000 });
      await expect(locators.addContextBtn).toBeEnabled({ timeout: 15000 });
    }
  );

  test(
    "Nubi Account Context - create a context with a name, description and content, reopen the tab, verify the card persisted and Add is now disabled",
    { tag: ["@dev", "@smoke", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);
      const name = uniqueContextName();
      const description = `created by the automated Nubi coverage pass ${name}`;

      try {
        await openCreateForm(locators);
        await fillContextForm(locators, {
          name,
          description,
          content: "Escalate cost anomalies above the agreed threshold to the platform channel.",
        });
        await submitCreate(page, locators, "Nubi Account Context - create");

        // Read back after a remount, not off the post-submit render: the success toast and
        // the in-place refresh both survive a write the backend rejected downstream.
        await remountAccountContextTab(locators);
        await expect(locators.contextCard(name)).toBeVisible({ timeout: 30000 });
        await expect(locators.cardDescription(description)).toBeVisible({ timeout: 20000 });
        await expect(locators.emptyState).toHaveCount(0);
        // The cap is now in force, which is the other half of what a create changes.
        await expect(locators.addContextBtn).toBeDisabled({ timeout: 20000 });
      } finally {
        await cleanupContext(page, locators, name);
      }
    }
  );

  test(
    "Nubi Account Context - create a context, edit its description from the card menu, reopen the tab, verify the new description replaced the old one",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);
      const name = uniqueContextName();
      const original = `first description for ${name}`;
      const edited = `edited description for ${name}`;

      try {
        await openCreateForm(locators);
        await fillContextForm(locators, { name, description: original, content: "initial content" });
        await submitCreate(page, locators, "Nubi Account Context - create before edit");
        await expect(locators.cardDescription(original)).toBeVisible({ timeout: 30000 });

        await openCardMenu(locators, name, "edit");
        // Edit refetches the full record before opening, so the form arrives populated —
        // which is itself the proof the row round-tripped rather than being re-rendered
        // from the list payload, where `content` is not carried.
        await expect(locators.formModalIntro).toBeVisible({ timeout: 30000 });
        await expect(locators.nameInput).toHaveValue(name, { timeout: 20000 });
        await expect(locators.descriptionInput).toHaveValue(original, { timeout: 20000 });

        await fillContextForm(locators, { description: edited });
        await submitUpdate(page, locators, "Nubi Account Context - edit");

        await remountAccountContextTab(locators);
        await expect(locators.cardDescription(edited)).toBeVisible({ timeout: 30000 });
        await expect(locators.cardDescription(original)).toHaveCount(0);
        await expect(locators.contextCard(name)).toBeVisible({ timeout: 20000 });
      } finally {
        await cleanupContext(page, locators, name);
      }
    }
  );

  test(
    "Nubi Account Context - create a context, delete it from the card menu, reopen the tab, verify the empty state returns and Add is enabled again",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);
      const name = uniqueContextName();

      try {
        await openCreateForm(locators);
        await fillContextForm(locators, { name, content: "created so it can be deleted" });
        await submitCreate(page, locators, "Nubi Account Context - create before delete");
        await expect(locators.contextCard(name)).toBeVisible({ timeout: 30000 });

        await deleteContext(page, locators, name, "Nubi Account Context - delete");

        await remountAccountContextTab(locators);
        await expect(locators.contextCard(name)).toHaveCount(0);
        await expect(locators.emptyState).toBeVisible({ timeout: 20000 });
        await expect(locators.addContextBtn).toBeEnabled({ timeout: 20000 });
      } finally {
        // No-op on the happy path; it matters when the delete half failed and left a record.
        await cleanupContext(page, locators, name);
      }
    }
  );

  test(
    "Nubi Account Context - open Add Account Context, upload a .txt file, verify its text lands in the content field and the character count matches",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openAccountContextTab(page);
      await selectWritableAccount(page, locators);

      await openCreateForm(locators);
      await expect(locators.uploadZone).toBeVisible({ timeout: 15000 });
      await expect(locators.contentInput).toHaveValue("");

      // The real <input type=file> is display:none behind the drop zone, so the file is set
      // on it directly — clicking the zone only opens the OS picker, which Playwright
      // cannot drive.
      await locators.fileInput.setInputFiles(SAMPLE_CONTEXT_FILE);

      // FileReader fills the textarea asynchronously; the assertion is the wait.
      await expect(locators.contentInput).toHaveValue(/sample global context file for automation testing/, { timeout: 20000 });
      await expect(locators.uploadZone).toHaveCount(0);

      // The counter is the component's own read of what it will submit, so it is checked
      // against the field rather than against the file's size on disk.
      const loaded = await locators.contentInput.inputValue();
      await expect(locators.charCounter).toHaveText(`${loaded.length} characters`, { timeout: 15000 });

      await locators.formCancelBtn.click();
      await expect(locators.formModal).toBeHidden({ timeout: 20000 });
    }
  );
});
