// Not for OSS
import { test, expect } from "@playwright/test";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import {
  CONFIG_WRITE_OPS,
  OP_CLEAR_OVERRIDE,
  OP_DELETE_PATTERN,
  OP_UPDATE_CONFIG,
  OP_UPSERT_PATTERN,
  createPattern,
  deletePatternIfPresent,
  fillPatternForm,
  openEgressFilterTab,
  openNewPatternDialog,
  remountEgressFilterTab,
  setPatternEnabled,
  trackOperations,
  uniqueAgentName,
  uniquePatternName,
  uniquePatternRegex,
} from "./egressFilterHelper";

// Nubi > Settings > Egress Filter (app/src/ee/components/egress-filter/EgressFilterTab.tsx).
// Mode, the enable flag, the excluded-agent list and the PII block are all
// tenant-wide policy on a shared dev tenant, so this suite dirties them to prove
// the form gates on them and then discards the edit by remounting the tab. Only
// custom patterns are written, always under a generated name and always deleted.
// Every case asserts UpdateEgressFilterConfig and ClearEgressOverride stayed at
// zero, which is what makes that promise checkable rather than a claim.

test.describe("Nubi Egress Filter Tab", () => {
  test(
    "Egress Filter sanity - open Nubi Settings, select the Egress Filter tab, verify the secret filter controls, the custom pattern section and the PII block render with Save changes disabled",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openEgressFilterTab(page);

      await expect(locators.secretFilterToggle).toBeVisible({ timeout: 30000 });
      await expect(locators.modeSelect).toBeVisible({ timeout: 15000 });
      await expect(locators.agentInput).toBeVisible({ timeout: 15000 });
      await expect(locators.newPatternBtn).toBeVisible({ timeout: 15000 });
      await expect(locators.piiToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.saveChangesBtn).toBeVisible({ timeout: 15000 });

      // Nothing has been touched, so the dirty gate must still be closed. Every
      // discard case below reads this same state as its "back to pristine" proof.
      await expect(locators.saveChangesBtn).toBeDisabled();

      // Writing a pattern needs egressfilter:Write or tenant admin, so this is
      // the precondition every CRUD case depends on — failing here names why.
      await expect(locators.newPatternBtn).toBeEnabled();

      // has_override drives both of these and they are mutually exclusive: with
      // no override row the mode description names the platform default and the
      // reset button is not rendered, and with one it is the other way round.
      const inheritingCount = await locators.inheritingDefaultText.count();
      const resetCount = await locators.resetDefaultsBtn.count();
      expect(inheritingCount + resetCount).toBe(1);

      // The tab swaps the pattern table for a dashed note at zero patterns
      // rather than rendering an empty CustomTable, so exactly one shows.
      const tableCount = await locators.patternsTable.count();
      const noteCount = await locators.noPatternsNote.count();
      expect(tableCount + noteCount).toBe(1);
    }
  );

  test(
    "Egress Filter - create a disabled custom detection pattern, leave the tab and return, verify the pattern persists with its regex and a Disabled status",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);
      const name = uniquePatternName();
      const regex = uniquePatternRegex();

      try {
        await createPattern(page, locators, { name, regex, enabled: false });

        // The remount is what separates a persisted pattern from one still held
        // in React state: the tab refetches egressfilter_get on mount.
        await remountEgressFilterTab(locators);

        const row = locators.patternRow(name);
        await expect(row).toBeVisible({ timeout: 30000 });
        await expect(row).toContainText(regex);
        await expect(row).toContainText("Disabled");
        await expect(locators.deletePatternBtn(name)).toBeVisible({ timeout: 15000 });
        expect(counts[OP_UPSERT_PATTERN]).toBe(1);
      } finally {
        await deletePatternIfPresent(locators, name);
      }

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - create a pattern, reopen it with Edit, replace the regex and switch it on, leave the tab and return, verify the row shows the new regex and an Enabled status",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);
      const name = uniquePatternName();
      const regex = uniquePatternRegex();
      const updatedRegex = uniquePatternRegex("NBE2EUPD");

      try {
        await createPattern(page, locators, { name, regex, enabled: false });

        await locators.editPatternBtn(name).click();
        await expect(locators.editPatternDialog).toBeVisible({ timeout: 15000 });

        // Edit mode prefills from the stored pattern, so these three are proof
        // the created values round-tripped through the API rather than proof of
        // what the form last held.
        await expect(locators.patternNameInput).toHaveValue(name, { timeout: 15000 });
        await expect(locators.patternRegexInput).toHaveValue(regex);
        await expect(locators.patternEnabledSwitch).not.toBeChecked();

        await locators.patternRegexInput.fill(updatedRegex);
        await expect(locators.patternRegexInput).toHaveValue(updatedRegex, { timeout: 10000 });
        await setPatternEnabled(locators, true);

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.patternSaveBtn.click();
            await locators.patternDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "Egress Filter - update an existing custom pattern", operationNames: OP_UPSERT_PATTERN, timeoutMs: 45000 }
        );

        await remountEgressFilterTab(locators);

        const row = locators.patternRow(name);
        await expect(row).toContainText(updatedRegex, { timeout: 30000 });
        await expect(row).not.toContainText(regex);
        await expect(row).toContainText("Enabled");
        expect(counts[OP_UPSERT_PATTERN]).toBe(2);
      } finally {
        await deletePatternIfPresent(locators, name);
      }

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - create a pattern then delete it from the confirm dialog, leave the tab and return, verify the confirm named the pattern and the row is gone",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, OP_DELETE_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);
      const name = uniquePatternName();
      const regex = uniquePatternRegex();

      try {
        await createPattern(page, locators, { name, regex, enabled: false });

        await locators.deletePatternBtn(name).click();
        await expect(locators.deletePatternDialog).toBeVisible({ timeout: 15000 });
        // The confirm has to name the pattern it is about to drop, or a misfired
        // row control would delete something else with no warning.
        await expect(locators.deletePatternDialog).toContainText(name);

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.confirmDeleteBtn.click();
            await locators.deletePatternDialog.waitFor({ state: "detached", timeout: 30000 });
          },
          { testName: "Egress Filter - delete a custom pattern", operationNames: OP_DELETE_PATTERN, timeoutMs: 45000 }
        );

        await remountEgressFilterTab(locators);
        await expect(locators.patternRow(name)).toHaveCount(0, { timeout: 30000 });

        // The section still renders one of its two shapes, so the absence above
        // is a deleted row rather than a tab that failed to come back.
        const tableCount = await locators.patternsTable.count();
        const noteCount = await locators.noPatternsNote.count();
        expect(tableCount + noteCount).toBe(1);
        expect(counts[OP_DELETE_PATTERN]).toBe(1);
      } finally {
        await deletePatternIfPresent(locators, name);
      }

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - open New pattern, enter a regex that does not compile, verify the invalid-expression error blocks Create and no pattern mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);

      await openNewPatternDialog(locators);
      await locators.patternNameInput.fill(uniquePatternName());
      await locators.patternRegexInput.fill("[unclosed");

      await expect(locators.patternRegexError).toHaveText("Not a valid regular expression.", { timeout: 15000 });
      await expect(locators.patternSaveBtn).toBeDisabled();

      await locators.patternCancelBtn.click();
      await expect(locators.patternDialog).toHaveCount(0, { timeout: 15000 });

      expect(counts[OP_UPSERT_PATTERN]).toBe(0);
      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - open New pattern, enter a name one character over the 80 character cap, verify the length error blocks Create and no pattern mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);

      await openNewPatternDialog(locators);
      // Both fields are required, so the form opens with Create already gated.
      await expect(locators.patternSaveBtn).toBeDisabled();

      await locators.patternNameInput.fill("n".repeat(81));
      await locators.patternRegexInput.fill(uniquePatternRegex());

      await expect(locators.patternNameError).toHaveText("Name must be 80 characters or less.", { timeout: 15000 });
      await expect(locators.patternSaveBtn).toBeDisabled();

      await locators.patternCancelBtn.click();
      await expect(locators.patternDialog).toHaveCount(0, { timeout: 15000 });

      expect(counts[OP_UPSERT_PATTERN]).toBe(0);
      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - open New pattern, fill a valid name and regex then cancel, leave the tab and return, verify the pattern is never listed and no pattern mutation is sent",
    { tag: ["@dev", "@regression", "@negative", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, [OP_UPSERT_PATTERN, ...CONFIG_WRITE_OPS]);
      const locators = await openEgressFilterTab(page);
      const name = uniquePatternName();

      await openNewPatternDialog(locators);
      await fillPatternForm(locators, { name, regex: uniquePatternRegex(), enabled: false });

      // Create is reachable, so the cancel below is discarding a draft the form
      // would otherwise have accepted rather than one it was rejecting anyway.
      await expect(locators.patternSaveBtn).toBeEnabled();

      await locators.patternCancelBtn.click();
      await expect(locators.patternDialog).toHaveCount(0, { timeout: 15000 });

      await remountEgressFilterTab(locators);
      await expect(locators.patternRow(name)).toHaveCount(0, { timeout: 30000 });

      expect(counts[OP_UPSERT_PATTERN]).toBe(0);
      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - add an excluded agent chip and dismiss it again, verify Save changes follows the dirty state and that leaving the tab sends no config update",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, CONFIG_WRITE_OPS);
      const locators = await openEgressFilterTab(page);
      const agent = uniqueAgentName();

      await expect(locators.saveChangesBtn).toBeDisabled();
      // The agent field is gated on the secret filter being on for this tenant,
      // so a disabled field here is a precondition failure, not a locator bug.
      await expect(locators.agentInput).toBeEnabled({ timeout: 15000 });

      await locators.agentInput.fill(agent);
      await expect(locators.agentInput).toHaveValue(agent, { timeout: 10000 });
      await expect(locators.agentAddBtn).toBeEnabled({ timeout: 10000 });
      await locators.agentAddBtn.click();

      await expect(locators.agentChip(agent)).toBeVisible({ timeout: 15000 });
      await expect(locators.saveChangesBtn).toBeEnabled();

      await locators.agentChipDismiss(agent).click();
      await expect(locators.agentChip(agent)).toHaveCount(0, { timeout: 15000 });
      // Dropping the same name returns the list to its stored value, so the form
      // is pristine again — the dirty flag compares the set, not the keystrokes.
      await expect(locators.saveChangesBtn).toBeDisabled();

      await remountEgressFilterTab(locators);
      await expect(locators.agentChip(agent)).toHaveCount(0, { timeout: 15000 });

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - flip the PII scrubbing switch, verify the outage policy, NER and category controls follow it and that leaving the tab restores the stored setting",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, CONFIG_WRITE_OPS);
      const locators = await openEgressFilterTab(page);

      // Whether this tenant has opted in is its own data, so the case asserts
      // the switch's effect in whichever direction it starts from.
      const initiallyOn = await locators.piiToggle.isChecked();
      await expect(locators.saveChangesBtn).toBeDisabled();

      await locators.piiToggle.setChecked(!initiallyOn);
      await expect(locators.piiToggle).toBeChecked({ checked: !initiallyOn, timeout: 15000 });

      if (initiallyOn) {
        await expect(locators.piiModeSelect).toHaveCount(0, { timeout: 15000 });
        await expect(locators.piiNerSelect).toHaveCount(0);
        await expect(locators.piiCategoryChip("EMAIL")).toHaveCount(0);
      } else {
        await expect(locators.piiModeSelect).toBeVisible({ timeout: 15000 });
        await expect(locators.piiNerSelect).toBeVisible();
        await expect(locators.piiCategoryChip("EMAIL")).toBeVisible();
      }

      await expect(locators.saveChangesBtn).toBeEnabled();

      await remountEgressFilterTab(locators);
      await expect(locators.piiToggle).toBeChecked({ checked: initiallyOn, timeout: 15000 });
      await expect(locators.saveChangesBtn).toBeDisabled();

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );

  test(
    "Egress Filter - turn PII scrubbing on and click the EMAIL category chip, verify the chip is a button whose selected tone flips and that leaving the tab discards it",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const counts = trackOperations(page, CONFIG_WRITE_OPS);
      const locators = await openEgressFilterTab(page);

      const initiallyOn = await locators.piiToggle.isChecked();
      if (!initiallyOn) {
        await locators.piiToggle.setChecked(true);
        await expect(locators.piiToggle).toBeChecked({ timeout: 15000 });
      }

      const emailChip = locators.piiCategoryChip("EMAIL");
      await expect(emailChip).toBeVisible({ timeout: 15000 });
      // ds/Chip renders an interactive chip as a MUI ButtonBase, so the category
      // is exposed as a real button rather than a styled div.
      await expect(locators.settingsPaper.getByRole("button", { name: "EMAIL", exact: true })).toBeVisible({ timeout: 15000 });

      // Read from data-tone, not aria-pressed: the tab passes aria-pressed but
      // ds/Chip drops it for a chip given neither `pressed` nor `selected`
      // (Chip.tsx:677,838), so tone is the only state the component renders.
      const toneBefore = await emailChip.getAttribute("data-tone");
      expect(toneBefore === "neutral" || toneBefore === "warning").toBe(true);

      await emailChip.click();
      await expect(emailChip).toHaveAttribute("data-tone", toneBefore === "warning" ? "neutral" : "warning", { timeout: 15000 });

      await expect(locators.saveChangesBtn).toBeEnabled();

      await remountEgressFilterTab(locators);
      await expect(locators.piiToggle).toBeChecked({ checked: initiallyOn, timeout: 15000 });
      await expect(locators.saveChangesBtn).toBeDisabled();

      expect(counts[OP_UPDATE_CONFIG]).toBe(0);
      expect(counts[OP_CLEAR_OVERRIDE]).toBe(0);
    }
  );
});
