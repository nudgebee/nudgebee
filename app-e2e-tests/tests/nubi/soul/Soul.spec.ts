// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openSoulTab,
  remountSoulTab,
  restoreDomainShorthand,
  saveSoul,
  setDomainShorthand,
  uniqueShorthand,
} from "./soulHelper";

// Nubi > Settings > Soul (app/src/ee/components/memory2/SoulTab.jsx).
// Every write here lands on the signed-in user's own Soul row (scope 'user'),
// never on the tenant profile, and the one case that saves puts back the text it
// found. The tab has no autosave: edits live in React state until Save, which is
// what the Reset and discard cases below pin down.
// Tone, Verbosity, Explain reasoning and the format toggles are ds/Cards that
// carry no selected-state attribute — only a border colour — so this suite
// asserts that they render and are interactive, and leaves which one is picked
// to the follow-up noted in the PR.

test.describe("Nubi Soul Tab", () => {
  test(
    "Nubi Soul sanity - open Nubi Settings, select the Soul tab, verify the personal style-profile header, the provenance chip and the Personal scope render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openSoulTab(page);

      await expect(locators.personalHeader).toBeVisible({ timeout: 20000 });
      await expect(locators.provenanceChip).toBeVisible({ timeout: 15000 });
      await expect(locators.scopeToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.personalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 15000 });

      // Save and Reset only render while the tab is editable, so their presence
      // is the precondition every edit case below depends on.
      await expect(locators.saveBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.resetBtn).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Soul sanity - open the Soul tab, verify the Tone & verbosity, Format preferences and Domain shorthand sections each render their own controls",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openSoulTab(page);

      await expect(locators.toneVerbositySection).toBeVisible({ timeout: 20000 });
      await expect(locators.optionCard("Tone", /Formal/)).toBeVisible({ timeout: 15000 });
      await expect(locators.optionCard("Tone", /Direct \/ Blunt/)).toBeVisible({ timeout: 15000 });
      await expect(locators.optionCard("Verbosity", /Terse/)).toBeVisible({ timeout: 15000 });
      await expect(locators.optionCard("Explain reasoning", /Step-by-step/)).toBeVisible({ timeout: 15000 });

      await expect(locators.formatSection).toBeVisible({ timeout: 15000 });
      await expect(locators.formatToggle(/Code-first/)).toBeVisible({ timeout: 15000 });
      await expect(locators.formatToggle(/Tables over prose/)).toBeVisible({ timeout: 15000 });

      await expect(locators.domainShorthandSection).toBeVisible({ timeout: 15000 });
      await expect(locators.domainShorthandTextarea).toBeVisible({ timeout: 15000 });
      await expect(locators.expandEditorBtn).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Soul - add a unique alias line to Domain shorthand, save, leave the Soul tab and return, verify the alias persisted",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(300000);
      const locators = await openSoulTab(page);
      const original = await locators.domainShorthandTextarea.inputValue();
      // Appended rather than replacing, so a shared dev user's existing profile
      // survives even if the restore below never runs.
      const alias = uniqueShorthand();
      const next = original ? `${original}\n${alias}` : alias;

      try {
        await setDomainShorthand(locators, next);
        await saveSoul(page, locators, "Nubi Soul - save domain shorthand");

        // The remount is what separates a persisted value from one still sitting
        // in React state: the tab refetches ai_memory_get on mount.
        await remountSoulTab(page, locators);
        await expect(locators.domainShorthandTextarea).toHaveValue(next, { timeout: 30000 });
      } finally {
        await restoreDomainShorthand(page, locators, original);
      }
    }
  );

  test(
    "Nubi Soul - edit Domain shorthand, click Reset, verify the field returns to its saved text and the edit was never written",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSoulTab(page);
      const original = await locators.domainShorthandTextarea.inputValue();
      const alias = uniqueShorthand();

      await setDomainShorthand(locators, alias);
      await locators.resetBtn.click();

      // Reset reverts to the last loaded snapshot, so the field goes back
      // without a write; the remount then proves nothing reached the backend.
      await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 15000 });
      await remountSoulTab(page, locators);
      await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 30000 });
    }
  );

  test(
    "Nubi Soul - type into Domain shorthand, leave the Soul tab without saving and return, verify the edit was discarded and never persisted",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSoulTab(page);
      const original = await locators.domainShorthandTextarea.inputValue();
      const alias = uniqueShorthand();

      await setDomainShorthand(locators, alias);

      // Switching tabs unmounts the editor, so coming back refetches from the
      // backend. The tab has no autosave, so the alias must not survive that.
      await remountSoulTab(page, locators);

      await expect(locators.domainShorthandTextarea).not.toHaveValue(alias, { timeout: 30000 });
      await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 30000 });
    }
  );

  test(
    "Nubi Soul - type an alias into Domain shorthand, open the expand editor and switch to Preview, verify the preview renders the unsaved alias",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSoulTab(page);
      const original = await locators.domainShorthandTextarea.inputValue();
      const alias = uniqueShorthand();

      // Planted first so the preview has something only this run could produce —
      // a profile that is empty on dev would otherwise render the "—" dash and
      // any preview at all would look correct.
      await setDomainShorthand(locators, alias);
      await locators.expandEditorBtn.click();
      await expect(locators.expandDialog).toBeVisible({ timeout: 20000 });
      await expect(locators.expandTextarea).toHaveValue(alias, { timeout: 15000 });

      await locators.expandPreviewOption.click();
      await expect(locators.expandPreviewOption).toHaveAttribute("aria-checked", "true", { timeout: 15000 });
      await expect(locators.expandPreview).toContainText(alias, { timeout: 15000 });

      await locators.expandDoneBtn.click();
      await expect(locators.expandDialog).toHaveCount(0, { timeout: 20000 });

      // Reset rather than Save, so this case leaves no write behind at all.
      await locators.resetBtn.click();
      await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 15000 });
    }
  );

  test(
    "Nubi Soul - open the expand editor, replace the shorthand inside it, click Done, verify the inline field carries the edit and Reset drops it",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSoulTab(page);
      const original = await locators.domainShorthandTextarea.inputValue();
      const alias = uniqueShorthand();

      await locators.expandEditorBtn.click();
      await expect(locators.expandDialog).toBeVisible({ timeout: 20000 });
      await expect(locators.expandEditOption).toHaveAttribute("aria-checked", "true", { timeout: 15000 });

      await locators.expandTextarea.fill(alias);
      await expect(locators.expandTextarea).toHaveValue(alias, { timeout: 15000 });
      await locators.expandDoneBtn.click();
      await expect(locators.expandDialog).toHaveCount(0, { timeout: 20000 });

      // The modal and the inline field are two views of one piece of tab state,
      // so the edit made in the modal must be sitting in the inline field.
      await expect(locators.domainShorthandTextarea).toHaveValue(alias, { timeout: 15000 });

      await locators.resetBtn.click();
      await expect(locators.domainShorthandTextarea).toHaveValue(original, { timeout: 15000 });
    }
  );

  test(
    "Nubi Soul - switch the scope toggle to Global and back to Personal, verify the header follows the selected scope",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openSoulTab(page);

      await expect(locators.personalHeader).toBeVisible({ timeout: 20000 });

      // Read-only on purpose: the tenant profile is shared by everyone on this
      // dev tenant, so this case only looks at it.
      await locators.globalScopeOption.click();
      await expect(locators.globalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 20000 });
      await expect(locators.tenantHeader).toBeVisible({ timeout: 30000 });
      await expect(locators.personalHeader).toHaveCount(0);

      await locators.personalScopeOption.click();
      await expect(locators.personalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 20000 });
      await expect(locators.personalHeader).toBeVisible({ timeout: 30000 });
      await expect(locators.tenantHeader).toHaveCount(0);
    }
  );
});
