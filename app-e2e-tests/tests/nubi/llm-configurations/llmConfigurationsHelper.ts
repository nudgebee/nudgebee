// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { LLMConfigurationsLocators } from "./llmConfigurationsLocators";

export const OP_LIST_INTEGRATIONS = "ListIntegrations";

export interface IntegrationQueryLog {
  queries: string[];
}

// api1/integrations.listIntegrations builds its where clause with gqlStringify
// and inlines it into the query text rather than sending it as a variable, so
// the committed filter state is readable straight off the outgoing query. That
// is what keeps these assertions independent of how many providers the shared
// dev tenant happens to hold on the day the suite runs. Hung off "response"
// rather than "request": a logged entry then means the data has landed, not
// merely that the browser asked for it, so an assertion cannot run in the gap
// before the listing re-renders. waitForGraphQLAndValidate is not used here
// because it reports only that the operation succeeded, and every assertion
// below is about which filter the query carried.
export function trackIntegrationQueries(page: Page): IntegrationQueryLog {
  const log: IntegrationQueryLog = { queries: [] };
  page.on("response", (res) => {
    const req = res.request();
    if (req.method() !== "POST" || !req.url().includes("api/graphql")) return;
    const postData = req.postData();
    if (!postData) return;
    try {
      const payload = JSON.parse(postData);
      const operations = Array.isArray(payload) ? payload : [payload];
      for (const op of operations as { operationName?: string; query?: string }[]) {
        if (op.operationName === OP_LIST_INTEGRATIONS && op.query) log.queries.push(op.query);
      }
    } catch {
      if (postData.includes(OP_LIST_INTEGRATIONS)) log.queries.push(postData);
    }
  });
  return log;
}

// Waits until the listing has received at least one more response than it had
// before the action, then returns the newest query. expect.poll is the wait —
// the response is the observable signal, so no fixed sleep is involved.
export async function nextIntegrationQuery(log: IntegrationQueryLog, countBefore: number): Promise<string> {
  await expect.poll(() => log.queries.length, { timeout: 45000, message: `no new ${OP_LIST_INTEGRATIONS} query was sent` }).toBeGreaterThan(countBefore);
  return log.queries[log.queries.length - 1];
}

// Waits for a listing query that actually carries the filter the action was
// meant to commit, rather than for "the next query to arrive". The tab issues
// its own unfiltered listing on mount, and that response can land after a test
// has recorded its baseline count — in which case a next-query read returns the
// mount request and the assertion fails against a filter nobody asked for. Only
// safe for a positive predicate: a negative one ("carries no name filter") is
// satisfied by that same mount request, so those keep using nextIntegrationQuery.
export async function nextIntegrationQueryMatching(
  log: IntegrationQueryLog,
  countBefore: number,
  predicate: (query: string) => boolean,
  description: string
): Promise<string> {
  await expect
    .poll(() => log.queries.slice(countBefore).some(predicate), { timeout: 45000, message: `no ${OP_LIST_INTEGRATIONS} query matched ${description}` })
    .toBe(true);
  return log.queries.slice(countBefore).find(predicate) as string;
}

export function hasTypeFilter(query: string, type: string): boolean {
  return query.includes(`type:{_eq:"${type}"}`);
}

export function hasNameFilter(query: string, name: string): boolean {
  return query.includes(`name:{_ilike:"%${name}%"}`);
}

export function hasStatusFilter(query: string, status: string): boolean {
  return query.includes(`status:{_eq:"${status}"}`);
}

export function hasAnyNameFilter(query: string): boolean {
  return query.includes("name:{_ilike:");
}

// A name no tenant fixture can carry, so the listing it produces is empty for a
// reason the test controls rather than one the dev data happens to supply.
export function absentConfigName(): string {
  return `nb_e2e_absent_${Date.now()}_${Math.random().toString(36).slice(2, 6)}`;
}

// Logs in, opens the Nubi panel and lands on Settings > Configurations, which
// mounts the LLM Providers sub-tab by default.
export async function openConfigurationsTab(page: Page): Promise<LLMConfigurationsLocators> {
  const loginPage = new LoginPage(page);
  const nubi = new NubiLocators(page);
  const locators = new LLMConfigurationsLocators(page);

  await loginPage.doFullLogin();
  await nubi.openPanel();
  await nubi.settingsBtn.click();
  await locators.configurationsTab.waitFor({ state: "visible", timeout: 20000 });
  await locators.configurationsTab.click();
  await locators.llmListingCard.waitFor({ state: "visible", timeout: 30000 });
  // The card renders before its first ListIntegrations response arrives. Every
  // caller that records a query count straight afterwards would otherwise race
  // that initial request and read it back as the one its action triggered.
  await waitForListingSettled(locators.llmRows(), locators.llmEmptyState);
  return locators;
}

// The two sub-tabs are a conditional render, not a hidden panel, so the one
// being left really does detach — waiting on that is what proves the switch
// landed before the next assertion reads the new listing.
export async function openMcpSubTab(locators: LLMConfigurationsLocators): Promise<void> {
  await locators.mcpServersSubTab.click();
  await locators.llmListingCard.waitFor({ state: "detached", timeout: 20000 });
  await locators.mcpListingCard.waitFor({ state: "visible", timeout: 30000 });
  await waitForListingSettled(locators.mcpRows(), locators.mcpEmptyState);
}

export async function openLlmSubTab(locators: LLMConfigurationsLocators): Promise<void> {
  await locators.llmProvidersSubTab.click();
  await locators.mcpListingCard.waitFor({ state: "detached", timeout: 20000 });
  await locators.llmListingCard.waitFor({ state: "visible", timeout: 30000 });
  await waitForListingSettled(locators.llmRows(), locators.llmEmptyState);
}

// Types a name and commits it with Enter, which is the only thing SearchInput
// treats as "search" — typing alone leaves the previous filter in force.
export async function searchByName(input: Locator, text: string): Promise<void> {
  await input.click();
  await input.fill(text);
  await expect(input).toHaveValue(text, { timeout: 10000 });
  await input.press("Enter");
}

// Settles on whichever of the two loaded shapes the listing lands in, so a
// non-retrying read taken afterwards (a .count(), say) cannot catch the table
// mid-refetch and mistake "not rendered yet" for "no results".
export async function waitForListingSettled(rows: Locator, emptyState: Locator): Promise<void> {
  await expect(rows.first().or(emptyState)).toBeVisible({ timeout: 30000 });
}

// Reads a provider name out of the listing's first row so the search cases run
// against real tenant data instead of a hardcoded fixture name. The Name cell
// appends a "Default" / "Default (n of m)" chip for a provider that is some
// account's default, and that chip text is not part of the stored name, so it
// has to come off before the value is used as a filter.
export async function firstProviderName(locators: LLMConfigurationsLocators): Promise<string> {
  const firstCell = locators.llmRows().first().locator("td").first();
  // The wait is on real text, not on visibility: a cell can be visible while
  // still holding nothing, and reading it then yields "" rather than a name.
  await expect(firstCell).toHaveText(/\S/, { timeout: 30000 });
  const raw = ((await firstCell.innerText()) ?? "").trim();
  const name = raw
    .replace(/\s*Default(\s*\(\d+\s+of\s+\d+\))?\s*$/i, "")
    .split("\n")[0]
    .trim();
  if (!name || name === "-") {
    throw new Error(`the first LLM provider row carried no readable name (cell text: "${raw}")`);
  }
  return name;
}
