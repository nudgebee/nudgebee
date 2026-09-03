// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// Nubi > Settings > Configurations (app/src/components/llm/LLMModelConfigurationTab.jsx,
// which mounts LLMConfigList.jsx or MCPConfigList.jsx one at a time).
// All three components render zero data-testid, so rung 1 does not exist on this
// surface. The sub-tab strip and the banner action carry real roles (ToggleGroup
// renders role=radio inside role=group, Banner renders role=status with a
// ButtonBase action), so those are rung 2. Everything else is a ds primitive the
// tab gives an explicit id — ListingLayout, CustomTable, SearchInput,
// FilterDropdown — which is rung 3, each with a fallback scoped to the same
// listing card so `.or()` cannot resolve into the other sub-tab's listing.
export class LLMConfigurationsLocators extends CommonLocators {
  readonly configurationsTab: Locator;
  readonly agentsTab: Locator;
  readonly subTabGroup: Locator;
  readonly llmProvidersSubTab: Locator;
  readonly mcpServersSubTab: Locator;
  readonly readOnlyBanner: Locator;
  readonly manageInAdminBtn: Locator;
  readonly llmListingCard: Locator;
  readonly llmTable: Locator;
  readonly llmTableBody: Locator;
  readonly llmSearchInput: Locator;
  readonly llmClearSearchBtn: Locator;
  readonly llmStatusFilterTrigger: Locator;
  readonly llmEmptyState: Locator;
  readonly mcpListingCard: Locator;
  readonly mcpTable: Locator;
  readonly mcpTableBody: Locator;
  readonly mcpSearchInput: Locator;
  readonly mcpStatusFilterTrigger: Locator;
  readonly mcpEmptyState: Locator;

  constructor(page: Page) {
    super(page);

    // The Nubi panel entry point and its Settings button stay in
    // tests/nubi/nubiLocators.ts; openConfigurationsTab() drives that class.
    // SettingsModal renders its strip through shared/navigation/Tabs (a MUI
    // Tabs), so every tab is a real role=tab even while scrolled out of view.
    this.configurationsTab = page.getByRole("tab", { name: "Configurations" });
    this.agentsTab = page.getByRole("tab", { name: "Agents" });

    // ds/ToggleGroup wraps its options in role=group named by ariaLabel, and a
    // single-selection option is role=radio carrying aria-checked — not a class.
    this.subTabGroup = page.getByRole("group", { name: "LLM Configuration" });
    this.llmProvidersSubTab = this.subTabGroup.getByRole("radio", { name: "LLM Providers" }).or(this.subTabGroup.getByText("LLM Providers", { exact: true })).first();
    this.mcpServersSubTab = this.subTabGroup.getByRole("radio", { name: "MCP Servers" }).or(this.subTabGroup.getByText("MCP Servers", { exact: true })).first();

    // ds/Banner renders role=status for the info tone. The toast region uses the
    // same role, so the text filter is what keeps this off a snackbar.
    this.readOnlyBanner = page.getByRole("status").filter({ hasText: /This view is read-only/ }).or(page.getByText(/This view is read-only/)).first();
    this.manageInAdminBtn = this.readOnlyBanner.getByRole("button", { name: "Manage in Admin" }).first();

    // ListingLayout puts its `id` on the wrapping DS Card, so this is the whole
    // listing: toolbar, table and empty state. Deliberately id-only — the Card is
    // a plain styled div with no role, and it is what the locators below scope
    // to, so a wider fallback here would widen all of them.
    this.llmListingCard = page.locator("#llm-config-list");
    this.mcpListingCard = page.locator("#mcp-config-list");

    // CustomTable renders id={id} on the <table> and `${id}-body` on its tbody.
    this.llmTable = page.locator("#llm-config").or(this.llmListingCard.getByRole("table")).first();
    this.mcpTable = page.locator("#mcp-config").or(this.mcpListingCard.getByRole("table")).first();

    // Deliberately id-only, and a `tbody` fallback would be actively wrong here:
    // CustomTable.jsx:1336 swaps in a whole separate skeleton TableBody while
    // loading, and only the data body carries `${id}-body`. A `tbody` fallback
    // therefore matches the skeleton — visible rows holding no text — so every
    // wait below would settle on the loading state instead of the loaded one.
    // Scoping to the id makes the rows simply not exist until the data lands,
    // which is what lets Playwright's own retries do the waiting.
    this.llmTableBody = page.locator("#llm-config-body");
    this.mcpTableBody = page.locator("#mcp-config-body");

    // ds/SearchInput passes `id` straight through to the <input> and uses its
    // `label` prop as the placeholder. Both sub-tabs use "Enter Name", so the
    // fallback is scoped to its own listing card.
    this.llmSearchInput = page.locator("#llm-config-name-search").or(this.llmListingCard.getByPlaceholder("Enter Name")).first();
    this.mcpSearchInput = page.locator("#mcp-config-name-search").or(this.mcpListingCard.getByPlaceholder("Enter Name")).first();

    // The clear control is a CloseIcon carrying aria-label, and it only renders
    // while the field holds a value. No role of its own, so this is rung 4.
    this.llmClearSearchBtn = this.llmListingCard.getByLabel("clear search").first();

    // FilterDropdown renders its trigger as <button id={`auto-complete-${kebab(id)}`}>;
    // the ids these two lists pass kebab to themselves.
    this.llmStatusFilterTrigger = page.locator("#auto-complete-llm-config-status-filter").or(this.llmListingCard.getByRole("button", { name: /^Status/ })).first();
    this.mcpStatusFilterTrigger = page.locator("#auto-complete-mcp-config-status-filter").or(this.mcpListingCard.getByRole("button", { name: /^Status/ })).first();

    // CustomTable renders EmptyData with id={tableId}, which stamps the heading
    // as `${tableId}-no-data`. It replaces the rows rather than sitting beside
    // them, so its presence is the zero-result signal.
    this.llmEmptyState = page.locator("#llm-config-no-data").or(this.llmListingCard.getByRole("heading", { name: "No Data Available" })).first();
    this.mcpEmptyState = page.locator("#mcp-config-no-data").or(this.mcpListingCard.getByRole("heading", { name: "No Data Available" })).first();
  }

  // Every data row currently rendered in a listing's tbody. CustomTable renders
  // no rows at all when the result is empty, so a count of 0 is meaningful.
  llmRows(): Locator {
    return this.llmTableBody.getByRole("row").or(this.llmTableBody.locator("tr"));
  }

  mcpRows(): Locator {
    return this.mcpTableBody.getByRole("row").or(this.mcpTableBody.locator("tr"));
  }

  // ds/Label capitalizes with CSS textTransform, so the DOM text stays whatever
  // the API returned ("enabled"), not what the eye reads ("Enabled"). Callers
  // must therefore compare case-insensitively.
  llmRowsWithStatus(status: string): Locator {
    return this.llmRows().filter({ has: this.page.getByText(new RegExp(`^${status}$`, "i")) });
  }

  filterOption(label: string): Locator {
    return this.page
      .locator('[role="option"]:visible')
      .filter({ hasText: new RegExp(`^${label.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}$`) })
      .first();
  }

  // Opens a toolbar filter and commits `label`. FilterDropdown only renders its
  // own search box above eight options, so the wait is on the options themselves.
  async chooseFilter(trigger: Locator, label: string): Promise<void> {
    await trigger.click();
    await this.page.locator('[role="option"]:visible').first().waitFor({ state: "visible", timeout: 15000 });
    await this.filterOption(label).click();
    // The trigger shows its label plus the committed value, so this is the
    // signal that the selection landed rather than a fixed pause.
    await expect(trigger).toContainText(label, { timeout: 15000 });
  }
}
