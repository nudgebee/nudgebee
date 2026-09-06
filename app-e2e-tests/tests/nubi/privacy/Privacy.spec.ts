// Not for OSS
import { test, expect } from "@playwright/test";
import { ensureLayerEnabled, openPrivacyTab, remountPrivacyTab, restoreLayer, setLayerEnabled } from "./privacyHelper";

// Nubi > b-Cortex > Memory > Privacy (app/src/ee/components/memory2/PrivacyTab.jsx).
// Every write here lands on the signed-in user's own consent rows (scope
// 'user'), never on the tenant rows, and each case puts back the state it found.
// The tab has no Save button: each switch commits on click, so there is no
// dirty-gating or discard path to cover — the read-back after a remount is what
// separates a committed row from optimistic React state.

test.describe("Nubi Privacy Tab", () => {
  test(
    "Nubi Privacy sanity - open b-Cortex, select the Privacy tab, verify the tab is selected and the personal consent description and Personal scope render",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPrivacyTab(page);

      await expect(locators.privacyTab).toHaveAttribute("aria-selected", "true", { timeout: 20000 });
      await expect(locators.personalDescription).toBeVisible({ timeout: 20000 });
      await expect(locators.scopeToggle).toBeVisible({ timeout: 15000 });
      await expect(locators.personalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 15000 });

      // The tenant-only copy must stay off the Personal scope: both scopes render
      // the same rows, so the description is the only thing that tells them apart.
      await expect(locators.tenantDescription).toHaveCount(0);
      await expect(locators.tenantWarningBanner).toHaveCount(0);
    }
  );

  test(
    "Nubi Privacy sanity - open the Privacy tab, verify the master row and all six memory layer rows each render exactly one switch",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openPrivacyTab(page);

      await expect(locators.masterRow).toBeVisible({ timeout: 20000 });
      await expect(locators.masterSwitch).toBeEnabled({ timeout: 15000 });

      // Count, not just visibility: the rows are reached by walking up from their
      // help line, so a row that resolved to a wrapper holding several switches
      // would let every toggle case below flip the wrong layer while still passing.
      await expect(locators.masterRowSwitches).toHaveCount(1);
      for (const row of locators.layerRows) {
        await expect(row).toBeVisible({ timeout: 15000 });
      }
      for (const switches of locators.layerRowSwitches) {
        await expect(switches).toHaveCount(1);
      }
    }
  );

  test(
    "Nubi Privacy - turn the Patterns layer off, verify the layer-disabled snackbar and that the Patterns switch reads off",
    { tag: ["@dev", "@regression", "@snackbar", "@crud"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPrivacyTab(page);
      await ensureLayerEnabled(page, locators, locators.patternsSwitch, "Nubi Privacy - baseline the Patterns layer");

      try {
        await setLayerEnabled(page, locators, locators.patternsSwitch, false, "Nubi Privacy - disable the Patterns layer");

        // Only Patterns was touched, so its neighbours must be untouched — a
        // mis-scoped row walk would show up here as a second switch that moved.
        await expect(locators.patternsSwitch).not.toBeChecked({ timeout: 15000 });
        await expect(locators.masterSwitch).toBeChecked({ timeout: 15000 });
      } finally {
        await restoreLayer(page, locators, locators.patternsSwitch, true, "Nubi Privacy - restore the Patterns layer");
      }
    }
  );

  test(
    "Nubi Privacy - turn the Patterns layer off, leave the tab and return, verify the layer is still off",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openPrivacyTab(page);
      await ensureLayerEnabled(page, locators, locators.patternsSwitch, "Nubi Privacy - baseline the Patterns layer");

      try {
        await setLayerEnabled(page, locators, locators.patternsSwitch, false, "Nubi Privacy - disable the Patterns layer");

        // The remount is what separates a committed consent row from one still
        // sitting in React state: the tab refetches ai_memory_get on mount.
        await remountPrivacyTab(page, locators, "Nubi Privacy - reload the consent rows");
        await expect(locators.patternsSwitch).not.toBeChecked({ timeout: 30000 });
      } finally {
        await restoreLayer(page, locators, locators.patternsSwitch, true, "Nubi Privacy - restore the Patterns layer");
      }
    }
  );

  test(
    "Nubi Privacy - turn the Decisions layer off and back on, verify the layer-enabled snackbar and that the switch returns to on",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openPrivacyTab(page);
      await ensureLayerEnabled(page, locators, locators.decisionsSwitch, "Nubi Privacy - baseline the Decisions layer");

      try {
        await setLayerEnabled(page, locators, locators.decisionsSwitch, false, "Nubi Privacy - disable the Decisions layer");
        await setLayerEnabled(page, locators, locators.decisionsSwitch, true, "Nubi Privacy - re-enable the Decisions layer");

        // Read back after the round trip rather than trusting the second toast:
        // the on state has to survive the refetch setLayer runs after the write.
        await remountPrivacyTab(page, locators, "Nubi Privacy - reload the consent rows");
        await expect(locators.decisionsSwitch).toBeChecked({ timeout: 30000 });
      } finally {
        await restoreLayer(page, locators, locators.decisionsSwitch, true, "Nubi Privacy - restore the Decisions layer");
      }
    }
  );

  test(
    "Nubi Privacy - turn the master memory switch off, verify every memory layer switch becomes non-interactive",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      test.setTimeout(210000);
      const locators = await openPrivacyTab(page);
      await ensureLayerEnabled(page, locators, locators.masterSwitch, "Nubi Privacy - baseline the master switch");

      try {
        for (const toggle of locators.layerSwitches) {
          await expect(toggle).toBeEnabled({ timeout: 15000 });
        }

        await setLayerEnabled(page, locators, locators.masterSwitch, false, "Nubi Privacy - disable memory in chats");

        // The master switch gates the six layers presentationally: with it off no
        // layer is injected, so none of them may still be flippable.
        for (const toggle of locators.layerSwitches) {
          await expect(toggle).toBeDisabled({ timeout: 20000 });
        }
        await expect(locators.masterSwitch).toBeEnabled({ timeout: 15000 });
      } finally {
        await restoreLayer(page, locators, locators.masterSwitch, true, "Nubi Privacy - restore memory in chats");
      }
    }
  );

  test(
    "Nubi Privacy - turn the master memory switch off and back on, verify the layer switches become interactive again and keep the values they had",
    { tag: ["@dev", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      test.setTimeout(240000);
      const locators = await openPrivacyTab(page);
      await ensureLayerEnabled(page, locators, locators.masterSwitch, "Nubi Privacy - baseline the master switch");

      // Captured before the cascade so the restore assertion compares against what
      // this run actually found, not against an assumed all-on tenant.
      const before: boolean[] = [];
      for (const toggle of locators.layerSwitches) {
        await expect(toggle).toBeEnabled({ timeout: 15000 });
        before.push(await toggle.isChecked());
      }

      try {
        await setLayerEnabled(page, locators, locators.masterSwitch, false, "Nubi Privacy - disable memory in chats");
        await expect(locators.layerSwitches[0]).toBeDisabled({ timeout: 20000 });

        await setLayerEnabled(page, locators, locators.masterSwitch, true, "Nubi Privacy - re-enable memory in chats");

        // Turning the master switch off must gate the layers, not rewrite them:
        // each one comes back interactive holding the value it went in with.
        for (let i = 0; i < locators.layerSwitches.length; i += 1) {
          await expect(locators.layerSwitches[i]).toBeEnabled({ timeout: 20000 });
          await expect(locators.layerSwitches[i]).toBeChecked({ checked: before[i], timeout: 20000 });
        }
      } finally {
        await restoreLayer(page, locators, locators.masterSwitch, true, "Nubi Privacy - restore memory in chats");
      }
    }
  );

  test(
    "Nubi Privacy - switch the scope to Global, verify the tenant-wide warning banner and the tenant-wide description replace the personal ones",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPrivacyTab(page);

      await expect(locators.personalDescription).toBeVisible({ timeout: 20000 });

      // Read-only by design: this case never flips a switch in Global scope. A
      // tenant consent write changes what every user in the tenant gets.
      await locators.globalScopeOption.click();
      await expect(locators.globalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 20000 });
      await expect(locators.tenantDescription).toBeVisible({ timeout: 30000 });
      await expect(locators.tenantWarningBanner).toBeVisible({ timeout: 20000 });
      await expect(locators.personalDescription).toHaveCount(0);

      // The rows themselves are scope-independent, so they must survive the swap
      // — a Global scope that rendered no rows would otherwise look like a pass.
      await expect(locators.masterRow).toBeVisible({ timeout: 20000 });
    }
  );

  test(
    "Nubi Privacy - switch the scope to Global and back to Personal, verify the personal description returns and the tenant warning banner is gone",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openPrivacyTab(page);

      await locators.globalScopeOption.click();
      await expect(locators.tenantDescription).toBeVisible({ timeout: 30000 });

      await locators.personalScopeOption.click();
      await expect(locators.personalScopeOption).toHaveAttribute("aria-checked", "true", { timeout: 20000 });
      await expect(locators.personalDescription).toBeVisible({ timeout: 30000 });
      await expect(locators.tenantDescription).toHaveCount(0);
      await expect(locators.tenantWarningBanner).toHaveCount(0);

      // Coming back re-runs the personal consent fetch, so the switches have to
      // become interactive again rather than staying in their loading state.
      await expect(locators.masterSwitch).toBeEnabled({ timeout: 30000 });
    }
  );
});
