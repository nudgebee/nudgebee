// Not for OSS
import { test, expect } from "@playwright/test";
import {
  absentConfigName,
  firstProviderName,
  hasAnyNameFilter,
  hasNameFilter,
  hasStatusFilter,
  hasTypeFilter,
  nextIntegrationQuery,
  nextIntegrationQueryMatching,
  openConfigurationsTab,
  openLlmSubTab,
  openMcpSubTab,
  searchByName,
  trackIntegrationQueries,
  waitForListingSettled,
} from "./llmConfigurationsHelper";

// Nubi > Settings > Configurations (app/src/components/llm/LLMModelConfigurationTab.jsx).
// Both sub-tabs are read-only by design — every management action lives on
// Admin > Integrations — so nothing in this suite writes to the shared tenant.
// The filter assertions read the outgoing ListIntegrations query rather than
// counting rows, which is what keeps them stable against changing dev data.

test.describe("Nubi Settings Configurations Tab", () => {
  test(
    "Configurations sanity - open Nubi Settings, select the Configurations tab, verify the LLM Providers sub-tab is the selected one and its table lists the provider columns",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openConfigurationsTab(page);

      await expect(locators.subTabGroup).toBeVisible({ timeout: 20000 });
      // ToggleGroup keeps selection in aria-checked, so this reads the real
      // selected state rather than inferring it from the highlight colour.
      await expect(locators.llmProvidersSubTab).toHaveAttribute("aria-checked", "true");
      await expect(locators.mcpServersSubTab).toHaveAttribute("aria-checked", "false");

      await expect(locators.llmTable).toBeVisible({ timeout: 30000 });
      await expect(locators.llmTable).toContainText("Name");
      await expect(locators.llmTable).toContainText("Account");
      await expect(locators.llmTable).toContainText("Created By");
      await expect(locators.llmTable).toContainText("Updated By");
      await expect(locators.llmTable).toContainText("Status");

      await expect(locators.llmSearchInput).toBeVisible({ timeout: 15000 });
      await expect(locators.llmStatusFilterTrigger).toBeVisible({ timeout: 15000 });

      // Nubi cannot answer without an LLM provider configured, so an empty
      // listing here is a real failure rather than an empty tenant.
      await expect(locators.llmRows().first()).toBeVisible({ timeout: 30000 });

      // The LLM listing deliberately ships with no status preselected so a
      // tenant whose only provider is disabled still sees it.
      await expect(locators.llmStatusFilterTrigger).not.toContainText("Enabled");
      await expect(locators.llmStatusFilterTrigger).not.toContainText("Disabled");
    }
  );

  test(
    "Configurations sanity - select the MCP Servers sub-tab, verify the Connection column replaces Updated By and the status filter arrives preset to Enabled",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      test.setTimeout(150000);
      const locators = await openConfigurationsTab(page);

      await openMcpSubTab(locators);

      await expect(locators.mcpServersSubTab).toHaveAttribute("aria-checked", "true");
      await expect(locators.llmProvidersSubTab).toHaveAttribute("aria-checked", "false");

      await expect(locators.mcpTable).toBeVisible({ timeout: 30000 });
      await expect(locators.mcpTable).toContainText("Connection");
      // "Updated By" is an LLM-only column, so its absence proves the switch
      // rendered the MCP listing rather than restyling the previous one.
      await expect(locators.mcpTable).not.toContainText("Updated By");

      await expect(locators.mcpStatusFilterTrigger).toContainText("Enabled", { timeout: 15000 });
    }
  );

  test(
    "Configurations - switch to MCP Servers and back to LLM Providers, verify the read-only banner names the matching Admin Integrations destination each time",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openConfigurationsTab(page);

      await expect(locators.readOnlyBanner).toBeVisible({ timeout: 20000 });
      await expect(locators.readOnlyBanner).toContainText(/Manage\s+LLM Providers\s+from/);
      await expect(locators.readOnlyBanner).toContainText(/Admin\s*→\s*Integrations\s*→\s*LLM/);

      await openMcpSubTab(locators);
      await expect(locators.readOnlyBanner).toContainText(/Manage\s+MCP Servers\s+from/, { timeout: 20000 });
      await expect(locators.readOnlyBanner).toContainText(/Admin\s*→\s*Integrations\s*→\s*MCP/);

      await openLlmSubTab(locators);
      await expect(locators.readOnlyBanner).toContainText(/Manage\s+LLM Providers\s+from/, { timeout: 20000 });
      await expect(locators.readOnlyBanner).not.toContainText(/Manage\s+MCP Servers\s+from/);
    }
  );

  test(
    "Configurations - read a configured provider name from the LLM Providers listing, search for it and press Enter, verify the request carries that name filter and only matching providers stay listed",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      const providerName = await firstProviderName(locators);
      const before = log.queries.length;
      await searchByName(locators.llmSearchInput, providerName);

      const query = await nextIntegrationQueryMatching(log, before, (q) => hasNameFilter(q, providerName), `the searched name filter for "${providerName}"`);
      expect(hasTypeFilter(query, "llm"), `the listing query did not stay scoped to llm: ${query}`).toBe(true);

      await expect(locators.llmRows().first()).toBeVisible({ timeout: 30000 });
      // Playwright's hasNotText is a case-insensitive substring match, so this
      // says every surviving row carries the searched term somewhere.
      await expect(locators.llmRows().filter({ hasNotText: providerName })).toHaveCount(0);
    }
  );

  test(
    "Configurations - search LLM Providers for a generated name no provider carries, verify the request carries that name filter and the listing falls back to the no-data state",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      const absent = absentConfigName();
      const before = log.queries.length;
      await searchByName(locators.llmSearchInput, absent);

      const query = await nextIntegrationQueryMatching(log, before, (q) => hasNameFilter(q, absent), `the generated name filter for "${absent}"`);
      expect(hasTypeFilter(query, "llm"), `the listing query did not stay scoped to llm: ${query}`).toBe(true);

      await expect(locators.llmEmptyState).toBeVisible({ timeout: 30000 });
      await expect(locators.llmRows()).toHaveCount(0);
    }
  );

  test(
    "Configurations - filter LLM Providers to a name with no match then clear the search box with its X control, verify the name filter is dropped from the request and the full listing returns",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      await searchByName(locators.llmSearchInput, absentConfigName());
      await expect(locators.llmEmptyState).toBeVisible({ timeout: 30000 });

      const before = log.queries.length;
      await locators.llmClearSearchBtn.click();

      const query = await nextIntegrationQuery(log, before);
      expect(hasAnyNameFilter(query), `the listing query still carried a name filter after clearing: ${query}`).toBe(false);
      expect(hasTypeFilter(query, "llm"), `the listing query did not stay scoped to llm: ${query}`).toBe(true);

      await expect(locators.llmSearchInput).toHaveValue("", { timeout: 15000 });
      await expect(locators.llmRows().first()).toBeVisible({ timeout: 30000 });
      await expect(locators.llmEmptyState).toHaveCount(0);
    }
  );

  test(
    "Configurations - set the LLM Providers status filter to Disabled, verify the request carries the disabled status filter and no enabled provider stays listed",
    { tag: ["@dev", "@regression", "@validation"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      const before = log.queries.length;
      await locators.chooseFilter(locators.llmStatusFilterTrigger, "Disabled");

      const query = await nextIntegrationQueryMatching(log, before, (q) => hasStatusFilter(q, "disabled"), "the committed disabled status filter");
      expect(hasTypeFilter(query, "llm"), `the listing query did not stay scoped to llm: ${query}`).toBe(true);

      // A dev tenant with every provider enabled is a legitimate outcome here,
      // and it renders the no-data state instead of rows — so both shapes are
      // asserted rather than assuming rows come back. count() does not retry,
      // so the listing has to be settled before it is read.
      await waitForListingSettled(locators.llmRows(), locators.llmEmptyState);
      const rowCount = await locators.llmRows().count();
      if (rowCount === 0) {
        await expect(locators.llmEmptyState).toBeVisible({ timeout: 20000 });
      } else {
        // ds/Label capitalizes with CSS, so the row text is the raw API value.
        await expect(locators.llmRows().filter({ hasNotText: "disabled" })).toHaveCount(0);
      }
    }
  );

  test(
    "Configurations - open the MCP Servers sub-tab and search it for a generated name no server carries, verify the request switches to the mcp type and keeps the preset enabled status filter",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      await openMcpSubTab(locators);

      const absent = absentConfigName();
      const before = log.queries.length;
      await searchByName(locators.mcpSearchInput, absent);

      const query = await nextIntegrationQueryMatching(log, before, (q) => hasNameFilter(q, absent), `the generated name filter for "${absent}"`);
      expect(hasTypeFilter(query, "mcp"), `the listing query did not switch to the mcp type: ${query}`).toBe(true);
      expect(hasStatusFilter(query, "enabled"), `the MCP listing dropped its preset status filter: ${query}`).toBe(true);

      await expect(locators.mcpEmptyState).toBeVisible({ timeout: 30000 });
      await expect(locators.mcpRows()).toHaveCount(0);
    }
  );

  test(
    "Configurations - filter LLM Providers to a name with no match, switch to MCP Servers and back, verify the search box comes back empty and the unfiltered listing is restored",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const log = trackIntegrationQueries(page);
      const locators = await openConfigurationsTab(page);

      await searchByName(locators.llmSearchInput, absentConfigName());
      await expect(locators.llmEmptyState).toBeVisible({ timeout: 30000 });

      await openMcpSubTab(locators);
      const before = log.queries.length;
      await openLlmSubTab(locators);

      // The sub-tabs are a conditional render, so returning remounts
      // LLMConfigList with fresh state — the filter must not survive it.
      const query = await nextIntegrationQuery(log, before);
      expect(hasAnyNameFilter(query), `the remounted listing still carried a name filter: ${query}`).toBe(false);

      await expect(locators.llmSearchInput).toHaveValue("", { timeout: 15000 });
      await expect(locators.llmRows().first()).toBeVisible({ timeout: 30000 });
      await expect(locators.llmEmptyState).toHaveCount(0);
    }
  );

  test(
    "Configurations - use the read-only banner's Manage in Admin action, verify a new tab opens on the Admin integrations form for the llm provider type and Settings stays open behind it",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      test.setTimeout(180000);
      const locators = await openConfigurationsTab(page);

      await expect(locators.manageInAdminBtn).toBeVisible({ timeout: 20000 });
      const [adminTab] = await Promise.all([page.context().waitForEvent("page", { timeout: 45000 }), locators.manageInAdminBtn.click()]);

      await adminTab.waitForURL(/\/accounts\/account-form\?cloudProvider=llm/, { timeout: 45000 });
      await adminTab.close();

      // The banner opens Admin in a new tab rather than navigating away, so the
      // listing the user was reading has to still be there afterwards.
      await expect(locators.llmListingCard).toBeVisible({ timeout: 20000 });
      await expect(locators.llmProvidersSubTab).toHaveAttribute("aria-checked", "true");
    }
  );
});
