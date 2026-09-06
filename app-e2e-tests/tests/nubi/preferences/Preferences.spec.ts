// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openPreferencesTab,
  remountPreferencesTab,
  restoreManualInputs,
  restoreTimezone,
  savePreferences,
  setManualInputs,
  setTimezone,
  uniqueNote,
} from "./preferencesHelper";

// Nubi > b-Cortex > Preferences > Typed (app/src/ee/components/memory2/PreferencesTab.jsx).
// Every write here lands on the signed-in user's own preference rows (scope
// 'user'), never on tenant defaults, and each case puts back the value it found.
// The tab has no autosave: edits live in React state until Save, which is what
// the discard and Reset cases below pin down.

test.describe("Nubi Preferences Tab", () => {
  test(
    "Nubi Preferences sanity - open b-Cortex, select Preferences > Typed, verify the durable-facts header, the Explicit provenance chip and the Personal scope render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPreferencesTab(page);

      await expect(locators.personalHeader).toBeVisible({ timeout: 20000 });
      await expect(locators.provenanceChip).toBeVisible({ timeout: 15000 });
      await expect(locators.scopeToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.personalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 15000 });

      // Save and Reset only render when the tab is editable, so their presence
      // is the precondition every write case below depends on.
      await expect(locators.saveBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.resetBtn).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Preferences sanity - open the Preferences tab, verify the Working context, manual inputs and Notifications sections each render their own controls",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPreferencesTab(page);

      await expect(locators.workingContextSection).toBeVisible({ timeout: 20000 });
      await expect(locators.timezoneInput).toBeVisible({ timeout: 15000 });
      await expect(locators.defaultNamespaceInput).toBeVisible({ timeout: 15000 });
      // Default environment renders a picker or the "no accounts" copy depending
      // on what the tenant has connected, so the row is asserted, not its shape.
      await expect(locators.defaultEnvironmentRow).toBeVisible({ timeout: 15000 });

      await expect(locators.manualInputsSection).toBeVisible({ timeout: 15000 });
      await expect(locators.manualInputsTextarea).toBeVisible({ timeout: 15000 });

      await expect(locators.notificationsSection).toBeVisible({ timeout: 15000 });
      await expect(locators.preferredChannelsRow).toBeVisible({ timeout: 15000 });
    }
  );

  test(
    "Nubi Preferences - open the Preferences tab, type into manual inputs, verify Save and Reset are disabled until the edit and enabled after it",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPreferencesTab(page);
      const original = await locators.manualInputsTextarea.inputValue();

      await expect(locators.saveBtn).toBeDisabled({ timeout: 15000 });
      await expect(locators.resetBtn).toBeDisabled({ timeout: 15000 });

      await setManualInputs(locators, `${original}${uniqueNote()}`);

      await expect(locators.saveBtn).toBeEnabled({ timeout: 15000 });
      await expect(locators.resetBtn).toBeEnabled({ timeout: 15000 });

      // Reset rather than Save, so this case leaves no write behind at all.
      await locators.resetBtn.click();
      await expect(locators.manualInputsTextarea).toHaveValue(original, { timeout: 15000 });
    }
  );

  test(
    "Nubi Preferences - edit both the timezone and manual inputs, click Reset, verify both fields return to their saved values and Save goes disabled",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPreferencesTab(page);
      const originalNote = await locators.manualInputsTextarea.inputValue();
      const originalZone = await locators.timezoneInput.inputValue();

      await setManualInputs(locators, uniqueNote());
      await setTimezone(locators, originalZone === "UTC" ? "Asia/Kolkata" : "UTC");
      await expect(locators.saveBtn).toBeEnabled({ timeout: 15000 });

      await locators.resetBtn.click();

      // Reset restores from the last loaded snapshot, so both fields go back in
      // one action — nothing was written, so there is nothing to undo after.
      await expect(locators.manualInputsTextarea).toHaveValue(originalNote, { timeout: 15000 });
      await expect(locators.timezoneInput).toHaveValue(originalZone, { timeout: 15000 });
      await expect(locators.saveBtn).toBeDisabled({ timeout: 15000 });
    }
  );

  test(
    "Nubi Preferences - type a unique note into manual inputs, save, leave the tab and return, verify the note persisted",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openPreferencesTab(page);
      const original = await locators.manualInputsTextarea.inputValue();
      const note = uniqueNote();

      try {
        await setManualInputs(locators, note);
        await savePreferences(page, locators, "Nubi Preferences - save manual inputs");

        // The remount is what separates a persisted value from one still sitting
        // in React state: the tab refetches ai_memory_list on mount.
        await remountPreferencesTab(locators);
        await expect(locators.manualInputsTextarea).toHaveValue(note, { timeout: 30000 });
      } finally {
        await restoreManualInputs(page, locators, original);
      }
    }
  );

  test(
    "Nubi Preferences - change the timezone, save, leave the tab and return, verify the new timezone persisted",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openPreferencesTab(page);
      const original = await locators.timezoneInput.inputValue();
      // Flipped against whatever is stored so the value always actually changes;
      // an unchanged field leaves the form clean and Save disabled.
      const next = original === "UTC" ? "Asia/Kolkata" : "UTC";

      try {
        await setTimezone(locators, next);
        await savePreferences(page, locators, "Nubi Preferences - save timezone");

        await remountPreferencesTab(locators);
        await expect(locators.timezoneInput).toHaveValue(next, { timeout: 30000 });
      } finally {
        await restoreTimezone(page, locators, original);
      }
    }
  );

  test(
    "Nubi Preferences - type into manual inputs, leave the tab without saving and return, verify the edit was discarded and never persisted",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPreferencesTab(page);
      const original = await locators.manualInputsTextarea.inputValue();
      const note = uniqueNote();

      await setManualInputs(locators, note);

      // Switching tabs unmounts the editor, so coming back refetches from the
      // backend. The tab has no autosave, so the note must not survive that.
      await remountPreferencesTab(locators);

      await expect(locators.manualInputsTextarea).not.toHaveValue(note, { timeout: 30000 });
      await expect(locators.manualInputsTextarea).toHaveValue(original, { timeout: 30000 });
      await expect(locators.saveBtn).toBeDisabled({ timeout: 15000 });
    }
  );

  test(
    "Nubi Preferences - switch the scope toggle to Global and back to Personal, verify the header follows the selected scope",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPreferencesTab(page);

      await expect(locators.personalHeader).toBeVisible({ timeout: 20000 });

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
