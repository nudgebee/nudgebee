// Not for OSS

import { Page, expect } from "@playwright/test";
import { ToolsAndMcpLocators } from "./toolsAndMcpLocators";
import {
  ALL_ACCOUNTS_LABEL,
  ARIA_DELETE_TOOL,
  BUTTON_DELETE,
  CRUD_TOOL_NAME_STEM,
  FILTER_SETTLE_MS,
} from "./toolsAndMcpConstants";

// Opens Admin -> AI & Tools -> Tools & MCP on a fresh page object.
export async function openToolsAndMcp(page: Page): Promise<ToolsAndMcpLocators> {
  const locators = new ToolsAndMcpLocators(page);
  await locators.open();
  return locators;
}

// Re-mounts the tab through a real reload. The hash survives it, so the sub-tab
// resolves again — which is what separates a persisted record from one still
// sitting in React state.
export async function reloadToolsAndMcp(page: Page): Promise<ToolsAndMcpLocators> {
  await page.reload({ waitUntil: "domcontentloaded" });
  const locators = new ToolsAndMcpLocators(page);
  await locators.waitForToolsBody();
  return locators;
}

// The Name-cell text of every currently visible tools row. Used by the filter and
// search cases to assert over the whole result set rather than a sampled row.
export async function visibleToolNameCells(locators: ToolsAndMcpLocators): Promise<string[]> {
  const count = await locators.toolsRows.count();
  const cells: string[] = [];
  for (let i = 0; i < count; i += 1) {
    cells.push(((await locators.toolNameCell(i).innerText()) ?? "").trim());
  }
  return cells;
}

// The Status-cell text of every currently visible MCP row. Read through one
// helper so the spec stays assertions-only and the column index lives in the
// page object rather than in the test.
export async function visibleMcpStatuses(locators: ToolsAndMcpLocators): Promise<string[]> {
  const count = await locators.mcpRows.count();
  const statuses: string[] = [];
  for (let i = 0; i < count; i += 1) {
    statuses.push(((await locators.mcpStatusCell(i).innerText()) ?? "").trim());
  }
  return statuses;
}

// The Status-cell text of every currently visible tools row.
export async function visibleToolStatuses(locators: ToolsAndMcpLocators): Promise<string[]> {
  const count = await locators.toolsRows.count();
  const statuses: string[] = [];
  for (let i = 0; i < count; i += 1) {
    statuses.push(((await locators.toolStatusCell(i).innerText()) ?? "").trim());
  }
  return statuses;
}

// A search term taken from a real row rather than invented: the first word of the
// first row's tool name that is long enough to discriminate. Three characters,
// not four — a catalogue whose names are all short ("Git", "Aws", "Sql", "Cli")
// would otherwise yield nothing. A shorter term matches more rows, which the
// caller's assertion tolerates: it checks that every surviving row carries the
// term, not that the result set is narrow. Returns "" when the catalogue offers
// nothing usable, which the caller reports as the precondition it is.
export async function firstSearchableToolWord(locators: ToolsAndMcpLocators): Promise<string> {
  if ((await locators.toolsRows.count()) === 0) return "";
  const nameCell = ((await locators.toolNameCell(0).innerText()) ?? "").trim();
  // The cell holds the title-cased name on its first line and the OwnerTypeBadge
  // on the second, so only the first line is the name.
  const firstLine = nameCell.split("\n")[0] ?? "";
  const word = firstLine.split(/\s+/).find((part) => /^[A-Za-z]{3,}$/.test(part));
  return word ?? "";
}

// Types into the tools search box and waits for the client-side filter to commit.
// ListTools filters in a useEffect over already-loaded rows, so the settle signal
// is the input holding the value plus the table having repainted into one of its
// two terminal shapes.
export async function searchTools(locators: ToolsAndMcpLocators, term: string): Promise<void> {
  await locators.toolSearch.fill(term);
  await expect(locators.toolSearch).toHaveValue(term, { timeout: FILTER_SETTLE_MS });
  await expect(locators.toolsTableBody.or(locators.toolsEmptyState)).toBeVisible({ timeout: FILTER_SETTLE_MS });
}

// Picks the first real account the AI & Tools filter offers. Returns "" when the
// tenant exposes none, which every caller reports as the precondition it is.
export async function firstAccountLabel(locators: ToolsAndMcpLocators): Promise<string> {
  const labels = await locators.accountOptionLabels();
  return labels.find((label) => label !== ALL_ACCOUNTS_LABEL) ?? "";
}

// A tool name unique to this run. getLlmIdentifierValidationMessage allows only
// [a-zA-Z]\w*, so the suffix is digits joined by underscores rather than a dash.
export function uniqueToolName(): string {
  return `${CRUD_TOOL_NAME_STEM}_${Date.now()}`;
}

// Narrows the listing to one tool by its stored name, using the app's own search.
// ListTools matches the search against the raw snake_case name as well as the
// rendered one, so this needs no reproduction of snakeToTitleCase — which matters
// because that function uppercases whole parts listed in UPPERCASE_ACRONYMS
// ("mcp" among them), and a hand-rolled equivalent silently drifts from it.
// Leaves the listing filtered; the caller asserts on locators.toolsRows.
export async function findToolByStoredName(locators: ToolsAndMcpLocators, storedName: string): Promise<void> {
  await searchTools(locators, storedName);
}

// Deletes a tool this suite created, if its row is still there. Used as the
// cleanup arm of the CRUD case so a failure part-way through does not leave a
// record behind on the shared tenant. Absence is the expected outcome on the
// happy path, which is why the probe swallows it.
export async function deleteToolIfPresent(locators: ToolsAndMcpLocators, storedName: string): Promise<void> {
  await findToolByStoredName(locators, storedName);
  const row = locators.toolsRows.first();
  const present = await row
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  // Absence is the expected outcome on the happy path — the test deleted it
  // already — so the probe returns false rather than throwing.
  if (!present) return;

  const deleteBtn = row.getByRole("button", { name: ARIA_DELETE_TOOL });
  const deletable = await deleteBtn
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  // The per-row delete control only exists in the per-account mount; in the
  // tenant-wide read there is nothing to click, which is a normal state for this
  // cleanup to reach rather than a failure.
  if (!deletable) return;

  await deleteBtn.click();
  await locators.deleteDialog().waitFor({ state: "visible", timeout: 30000 });
  await locators.deleteDialog().getByRole("button", { name: BUTTON_DELETE, exact: true }).click();
  await expect(locators.toolsRows).toHaveCount(0, { timeout: 60000 });
}
