// Not for OSS
import { Locator, Page, expect } from "@playwright/test";
import { MonitoringTabLocator } from "./MonitoringTabLocator";
import { navigateToIntegrationsPage } from "../../admin/Integrations/util";

// Query Metrics counterpart to monitoringLogQueryOperatorHelper; the builder is the same component, so its locators come from MonitoringTabLocator.

// The results table id, from QueryMetrics.tsx.
export const METRICS_TABLE_ID = "k8sProm";

// The run pins one provider because each one (Prometheus, Elasticsearch, Datadog) returns a different result shape.
export const TARGET_PROVIDER = "prometheus";

export function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class MetricsQueryLocators {
  readonly page: Page;
  readonly providerSwitcher: Locator;
  readonly resultsTable: Locator;
  readonly resultRows: Locator;
  readonly rowDetails: Locator;
  readonly labelChips: Locator;
  readonly headerCells: Locator;
  readonly nonFiniteNotice: Locator;

  constructor(page: Page) {
    this.page = page;
    // QueryMetrics.tsx renders no data-testid for the switcher, so the id is the only stable handle.
    this.providerSwitcher = page.locator("#metrics-provider-switcher");
    this.resultsTable = page.locator(`#${METRICS_TABLE_ID}`);
    // KubernetesTable puts the id on the table and `-body` on the tbody; a row with a second cell is data, not the drill-down.
    this.resultRows = page.locator(`#${METRICS_TABLE_ID}-body tr:has(td:nth-child(2))`);
    this.headerCells = page.locator(`#${METRICS_TABLE_ID} thead th`);
    // No data-testid on the row-details panel in QueryMetrics.tsx, so the id is the only stable handle.
    this.rowDetails = page.locator("#query-metrics-row-details");
    // Row Details renders each label as a "key=value" chip.
    this.labelChips = this.rowDetails.locator("text=/^[a-zA-Z_][a-zA-Z0-9_]*=/");
    this.nonFiniteNotice = page.getByTestId("metrics-non-finite-notice");
  }

  // Returns false when the provider is not offered on this cluster, which is a skip rather than a failure.
  async pinProvider(provider = TARGET_PROVIDER): Promise<boolean> {
    // A visibility wait with a timeout, because the tab is often still mounting and a one-shot check would skip the run.
    const appeared = await this.providerSwitcher
      .waitFor({ state: "visible", timeout: 60000 })
      .then(() => true)
      // Absence is the expected answer on a cluster that has no metrics provider switcher.
      .catch(() => false);
    if (!appeared) return false;

    // The switcher can show before its label renders, so wait for text; a badge that never fills in falls through to the dropdown path below.
    await expect(this.providerSwitcher).toHaveText(/.+/, { timeout: 30000 }).catch(() => {});

    // Already on the provider means already pinned; opening a dropdown to click the active option offers nothing.
    if (new RegExp(provider, "i").test(await this.currentProvider())) {
      // The cached metric list may still be the previous provider's, so reload to fetch the right one.
      await this.refetchAfterProviderChange();
      return true;
    }

    await this.providerSwitcher.click();
    const option = this.page.locator(`#metrics-provider-option-${provider}`);
    // An absent option is the normal answer when this cluster does not offer the provider.
    if (!(await option.isVisible({ timeout: 10000 }).catch(() => false))) {
      await this.page.keyboard.press("Escape");
      return false;
    }
    await option.click();
    await expect(this.providerSwitcher).toContainText(new RegExp(provider, "i"), { timeout: 30000 });
    await this.refetchAfterProviderChange();
    return true;
  }

  // The metric list is cached per provider for six hours, so a reload is what makes the tab fetch the new provider's list.
  async refetchAfterProviderChange(): Promise<void> {
    // A reload that is interrupted by a redirect is harmless because the switcher wait below confirms the page is back.
    await this.page.reload({ waitUntil: "domcontentloaded" }).catch(() => {});
    // The caller's next step waits on the metric picker with its own timeout, which is where a missing tab fails.
    await this.providerSwitcher.waitFor({ state: "visible", timeout: 60000 }).catch(() => {});
  }

  async currentProvider(): Promise<string> {
    // An unrendered badge reads as empty, which the caller treats as "not ready yet".
    return (await this.providerSwitcher.innerText().catch(() => "")).trim();
  }

  // Metrics show no data until one is chosen and submitted; returns the metric selected, or null when the picker offers none.
  async selectFirstMetric(): Promise<string | null> {
    const picker = this.page.getByRole("button", { name: /select a metric/i }).first();
    // Waited for because the reload above leaves the tab mounting; a one-shot check reported "none offered" on a page that was just slow.
    const present = await picker
      .waitFor({ state: "visible", timeout: 60000 })
      .then(() => true)
      // A picker that never renders is reported as "none offered" so the spec can skip with a reason.
      .catch(() => false);
    if (!present) {
      console.log("[QueryMetrics] metric picker never rendered");
      return null;
    }

    // The control is disabled while the metrics list loads asynchronously, so poll until it enables.
    const enabled = await expect(picker)
      .toBeEnabled({ timeout: 60000 })
      .then(() => true)
      // A picker that never enables means this environment has no metrics, which the spec treats as a skip.
      .catch(() => false);
    if (!enabled) return null;

    // The popover opens from the FilterDropdown's own trigger (plain #auto-complete); clicking by accessible name hit the label and opened nothing.
    const trigger = this.page.locator("#auto-complete").first();
    // A missing trigger falls back to clicking the picker button itself.
    if (await trigger.isVisible().catch(() => false)) {
      await trigger.click();
    } else {
      await picker.click();
    }

    // Option rows are plain boxes in a hand-rolled virtual list, not role=option, so anchor on the panel that holds the search box.
    const search = this.page.getByPlaceholder(/search/i).last();
    const popover = this.page.locator(".MuiPaper-root").filter({ has: search }).last();
    const options = popover.locator("div").filter({ hasText: /^[A-Za-z_:][\w:]*$/ });

    // An empty list is handled by the count check below, so a timeout here is not an error.
    await options.first().waitFor({ state: "visible", timeout: 20000 }).catch(() => {});

    // Always search "up": every Prometheus scrapes it and it answers fast, while the first listed metric is arbitrary and can stall the run.
    const searchBox = popover.locator("input").first();
    // A popover without a search box just keeps its full list.
    if (await searchBox.isVisible().catch(() => false)) {
      await searchBox.fill("up");
      // No match is handled by the count check below.
      await options.first().waitFor({ state: "visible", timeout: 20000 }).catch(() => {});
    }

    if ((await options.count()) === 0) {
      await this.page.keyboard.press("Escape");
      return null;
    }
    // The search also lists metrics containing "up" (backup_*), so take the exact match and fall back to the first row.
    const exactUp = options.filter({ hasText: /^up$/ });
    const option = (await exactUp.count()) > 0 ? exactUp.first() : options.first();

    // An unreadable name is returned empty and the spec reports it as "none offered".
    const name = (await option.innerText().catch(() => "")).trim();
    await option.click();
    // The label input is disabled until a metric is committed, so it becoming enabled proves the selection landed.
    await expect(this.page.getByPlaceholder(/Type label name/i).first()).toBeEnabled({ timeout: 30000 });
    return name;
  }

  // Waits for the query's own response, because the previous result table stays on screen while a new query runs and a visibility wait would read stale rows.
  async submitQuery(): Promise<void> {
    const responded = this.page
      .waitForResponse(
        (r) => r.url().includes("/api/graphql") && (r.request().postData() || "").includes("MetricsQuery"),
        { timeout: 45000 }
      )
      // A response that never arrives is caught by waitForResults, which asserts on the table or the empty state.
      .catch(() => null);

    await this.page.getByRole("button", { name: /^submit$/i }).first().click();
    await responded;
  }

  // Waits for the result set to settle into either rows or an empty state, so a read never races a query in flight.
  async waitForResults(timeoutMs = 45000): Promise<number> {
    await expect(this.resultsTable.or(this.page.getByText(/no data|no results/i)).first()).toBeVisible({ timeout: timeoutMs });

    // A missing table and a genuine empty result both count zero, so log which one settled.
    const tableThere = await this.resultsTable.isVisible().catch(() => false);
    const rows = await this.resultRows.count();
    if (rows === 0) {
      console.log(`[QueryMetrics] results: table present=${tableThere}, rows=0`);
    }
    return rows;
  }

  // Prometheus renders each label as its own table column, so labels are read straight off the row.
  async readRowLabels(rowIndex: number): Promise<Record<string, string>> {
    const headers = (await this.headerCells.allTextContents()).map((h) => h.trim());
    const row = this.resultRows.nth(rowIndex);
    await row.scrollIntoViewIfNeeded();
    const cells = (await row.locator("td").allTextContents()).map((c) => c.trim());

    const labels: Record<string, string> = {};
    headers.forEach((header, i) => {
      // The value column has no label name, and __name__ is the metric rather than a label to filter on.
      if (!header || header === "__name__") return;
      if (cells[i]) labels[header] = cells[i];
    });
    return labels;
  }
}

// Reads the operator list the builder offers for a given label, live.
export async function readOfferedOperators(locators: MonitoringTabLocator, label: string): Promise<string[]> {
  const input = locators.LogQueryBuilderInput;
  await input.click();
  await input.fill("");
  await input.pressSequentially(label, { delay: 20 });

  // Typing the full label sometimes accepts it on its own, so select it only if the list still offers it.
  if (await suggestionsInclude(locators, label)) await pickFromSuggestions(locators, label);

  // An empty list reads as no operators, which the spec reports as a mismatch.
  const operators = await locators.LogQuerySuggestions.allTextContents().catch(() => []);
  await input.page().keyboard.press("Escape");
  return operators.map((o) => o.trim()).filter(Boolean);
}

// A first attempt can miss while the builder is still settling (seen once in CI), so one retry from a cleared input is allowed; two misses still report false.
export async function addMetricFilterChip(
  locators: MonitoringTabLocator,
  label: string,
  operatorLabel: string,
  value: string
): Promise<boolean> {
  for (let attempt = 1; attempt <= 2; attempt++) {
    if (await tryAddMetricFilterChip(locators, label, operatorLabel, value)) return true;
    await locators.LogQueryBuilderInput.page().keyboard.press("Escape");
  }
  return false;
}

// Builds one label filter chip. Unlike the logs builder, typing the full label advances straight to the operator list, and the input placeholder tells each stage.
async function tryAddMetricFilterChip(
  locators: MonitoringTabLocator,
  label: string,
  operatorLabel: string,
  value: string
): Promise<boolean> {
  const input = locators.LogQueryBuilderInput;
  const page = input.page();

  try {
    await input.click();
    // Cleared then typed key by key, because fill() with an unchanged value fires no input event and the autocomplete never reopens.
    await input.fill("");
    await input.pressSequentially(label, { delay: 20 });

    // Whether the label still needs selecting depends on state, so look at what the list offers instead of assuming.
    if (await suggestionsInclude(locators, label)) {
      await pickFromSuggestions(locators, label);
    }

    // The operator is matched on exact trimmed text, because a contains match on "=" would also hit "!=" and "=~".
    if (!(await pickFromSuggestions(locators, operatorLabel))) return false;

    // The value is committed with Enter, because live data may not be offered as a suggestion.
    await expect(input).toHaveAttribute("placeholder", /value/i, { timeout: 15000 });
    await input.fill(value);
    await page.keyboard.press("Enter");

    // The chip is committed once the input asks for a label again.
    await expect(input).toHaveAttribute("placeholder", /label name/i, { timeout: 15000 });
    return true;
  } catch {
    return false;
  }
}

// Chips are ds/Chip with a dismiss button (data-testid "chip-dismiss", name "Remove <chip text>"); a "Remove filter" name never matches them.
export async function clearMetricFilterChips(page: Page): Promise<void> {
  const remove = page.getByTestId("chip-dismiss");

  for (let attempt = 0; attempt < 12; attempt++) {
    const before = await remove.count();
    if (before === 0) return;
    // A click on a chip that detached mid-click is retried by the next loop pass.
    await remove.first().click().catch(() => {});
    // Waits on the count dropping, since a detached-wait would repoint at the next chip; a count that stays is retried by the bounded loop.
    await expect(remove).toHaveCount(before - 1, { timeout: 5000 }).catch(() => {});
  }
}

// The builder can swap a typed value for an existing suggestion on Enter (issue #39819), so check the chip kept exactly what was typed.
export async function chipCommittedAs(page: Page, label: string, operatorLabel: string, value: string): Promise<boolean> {
  const chip = page.getByRole("button", { name: `Remove ${label} ${operatorLabel} ${value}`, exact: true });
  return expect(chip.first())
    .toBeVisible({ timeout: 5000 })
    .then(() => true)
    // A chip that carries a different value is the expected answer here, and the caller records it as a known limitation.
    .catch(() => false);
}

// Clicks the suggestion whose trimmed text equals `wanted`; returns false and logs what was offered when nothing matches.
export async function pickFromSuggestions(locators: MonitoringTabLocator, wanted: string): Promise<boolean> {
  // An empty list is handled by the index check below.
  await locators.LogQuerySuggestions.first().waitFor({ state: "visible", timeout: 15000 }).catch(() => {});
  // No suggestions read as an empty list, which returns false below.
  const texts = await locators.LogQuerySuggestions.allTextContents().catch(() => []);
  const index = texts.findIndex((t) => t.trim() === wanted);
  if (index < 0) {
    // Logging what the list held tells a wrong locator apart from a builder that already moved on.
    console.log(`[QueryMetrics] "${wanted}" not offered; list held [${texts.map((t) => t.trim()).join(", ") || "nothing"}]`);
    return false;
  }
  // Clicked by its text, not its position: the list re-renders while it loads, so an index goes stale and the click retries on a detached row.
  await locators.LogQuerySuggestions.filter({ hasText: new RegExp("^\\s*" + escapeForRegex(wanted) + "\\s*$") }).first().click();
  return true;
}

// Whether the open suggestion list offers `wanted`; used to decide if the label still needs selecting.
export async function suggestionsInclude(locators: MonitoringTabLocator, wanted: string): Promise<boolean> {
  // An empty list simply means the answer is false.
  await locators.LogQuerySuggestions.first().waitFor({ state: "visible", timeout: 15000 }).catch(() => {});
  // No suggestions read as an empty list.
  const texts = await locators.LogQuerySuggestions.allTextContents().catch(() => []);
  return texts.some((t) => t.trim() === wanted);
}

// Removes the one chip for `label`, found by the "Remove <label> " prefix of its dismiss button.
export async function removeMetricFilterChip(page: Page, label: string): Promise<boolean> {
  const all = page.getByTestId("chip-dismiss");
  const before = await all.count();
  const remove = page.getByRole("button", { name: new RegExp(`^Remove ${escapeForRegex(label)} `) }).first();
  // A chip that is not on screen is the expected answer when nothing was added, and the caller records it as a failure.
  if (before === 0 || !(await remove.isVisible().catch(() => false))) return false;

  await remove.click();
  return expect(all)
    .toHaveCount(before - 1, { timeout: 10000 })
    .then(() => true)
    // A count that does not drop means the chip was not removed, which the caller reports.
    .catch(() => false);
}

// MUI menu items are not role=menuitem here, and every row's menu stays mounted, so click the visible item by id and retry until the menu registers it.
async function clickRowMenuItem(page: Page, row: Locator, itemName: string): Promise<void> {
  const menuItem = page.locator(`[role="menuitem"]#${itemName.toLowerCase()}:visible`);
  for (let attempt = 0; attempt < 4; attempt++) {
    await row.getByRole("button", { name: "More actions" }).click();
    const attached = await menuItem
      .waitFor({ state: "attached", timeout: 4000 })
      .then(() => true)
      // A menu that has not opened yet is retried by the loop.
      .catch(() => false);
    if (attached) {
      // A click that detaches the item is the normal result, so a failure here is read from the detach wait below.
      await menuItem.dispatchEvent("click").catch(() => {});
      const registered = await menuItem
        .waitFor({ state: "detached", timeout: 3000 })
        .then(() => true)
        // An item that stays means the click did not register, so the loop retries.
        .catch(() => false);
      if (registered) return;
    }
    // Closing an already-closed menu is harmless.
    await page.keyboard.press("Escape").catch(() => {});
  }
  await row.getByRole("button", { name: "More actions" }).click();
  await menuItem.waitFor({ state: "attached", timeout: 5000 });
  await menuItem.dispatchEvent("click");
}

// Ensures a provider is the account's default metrics provider (the tab switcher only picks what the open tab queries) and returns a restore that undoes it only if this run changed it.
export async function ensureDefaultMetricsProvider(
  page: Page,
  { accountName, providerName }: { accountName: string; providerName: string },
): Promise<{ wasAlreadyDefault: boolean; restore: () => Promise<void> }> {
  const openSwitch = async () => {
    const locators = await navigateToIntegrationsPage(page);
    await locators.observabilityTab.waitFor({ state: "visible", timeout: 15000 });
    await locators.observabilityTab.click();

    const card = page.locator(`#${providerName}-section-card`);
    await card.waitFor({ state: "visible", timeout: 15000 });
    await card.click();
    // The URL commits before the account form data loads, so wait out the loader; one that never shows is fine because the row wait below decides.
    await page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 60000 }).catch(() => {});

    // A provider served by the K8s agent has no account row and the page says "No <provider> accounts connected", so detect that instead of timing out.
    const emptyState = page.getByText(new RegExp(`No ${escapeForRegex(providerName)} accounts connected`, "i"));
    const row = page.locator("tbody tr").filter({ hasText: accountName }).first();

    // Either the account row or the empty state settles the page; neither appearing is handled by the row check below.
    await expect(row.or(emptyState).first()).toBeVisible({ timeout: 20000 }).catch(() => {});
    if (!(await row.isVisible().catch(() => false))) return null;

    const dialog = page.getByRole("dialog");
    await clickRowMenuItem(page, row, "Edit");
    await dialog.waitFor({ state: "visible", timeout: 15000 });

    // The dialog lists one switch per account on this provider, so take the one inside this account's own card; the first checkbox belongs to another account.
    const accountCard = dialog
      .locator("div")
      .filter({ has: page.getByText(accountName, { exact: true }) })
      .filter({ has: page.getByRole("checkbox") })
      .last();
    const checkbox = accountCard.getByRole("checkbox").first();
    await checkbox.waitFor({ state: "visible", timeout: 15000 });
    return { dialog, checkbox };
  };

  const opened = await openSwitch();
  if (!opened) {
    // This provider is not a connected account on this tenant, so the tab's own switcher is the only control and there is nothing to restore.
    console.log(`${providerName} has no connected account on this tenant — no default-metrics setting to manage`);
    return { wasAlreadyDefault: true, restore: async () => {} };
  }
  const wasAlreadyDefault = await opened.checkbox.isChecked();

  if (!wasAlreadyDefault) {
    await opened.checkbox.click();
    await expect(opened.checkbox).toBeChecked({ timeout: 15000 });
    console.log(`Turned ${providerName} ON as the default metrics provider for "${accountName}"`);
  } else {
    console.log(`${providerName} was already the default metrics provider for "${accountName}"`);
  }
  // A dialog that is already closed is fine because the next step navigates away anyway.
  await opened.dialog.getByRole("button", { name: /close|cancel|save|done/i }).first().click().catch(() => {});

  const restore = async (): Promise<void> => {
    if (wasAlreadyDefault) return;
    // The dialog closed above, so reopen it from wherever the test finished.
    const fresh = await openSwitch();
    if (!fresh) return;
    // An unreadable switch is treated as unknown, not as off, so it is clicked below.
    const stillChecked = await fresh.checkbox.isChecked().catch(() => null);
    if (stillChecked === false) {
      console.log(`${providerName} default-metrics switch for "${accountName}" was already back off`);
    } else {
      // Log what happened rather than what was attempted, so a failed restore is not hidden.
      await fresh.checkbox.click().then(
        () => console.log(`Restored ${providerName} default-metrics switch to OFF for "${accountName}"`),
        (err) => console.log(`Failed to restore ${providerName} default-metrics switch for "${accountName}": ${err}`),
      );
    }
    // A dialog that is already closed is fine at this point.
    await fresh.dialog.getByRole("button", { name: /close|cancel|done/i }).first().click().catch(() => {});
  };

  return { wasAlreadyDefault, restore };
}
