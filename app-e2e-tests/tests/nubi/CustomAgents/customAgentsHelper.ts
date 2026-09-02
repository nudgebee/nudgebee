// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { NubiLocators } from "../nubiLocators";
import { CustomAgentsLocators } from "./customAgentsLocators";

// A term no agent name can contain, so the empty-state assertion is about the
// search working rather than about what the dev tenant happens to hold.
export const NO_MATCH_TERM = "zz_no_such_agent_e2e";

export interface AgentsView {
  nubi: NubiLocators;
  agents: CustomAgentsLocators;
}

// Names must start with a letter and hold only letters, digits and underscores
// (getLlmIdentifierValidationMessage in app/src/utils/common.ts), so the unique
// suffix is alphanumeric rather than a timestamp with separators.
export function uniqueAgentName(): string {
  return `e2e_custom_agent_${Math.random().toString(36).slice(2, 8).toUpperCase()}`;
}

// Logs in and opens Nubi Settings on the Agents tab with the listing settled.
//
// This is the entry path CreateCustomAgent.spec.ts already proves: Settings opened
// from the Nubi panel carries an accountId, and ListAgents hides Create / Edit /
// Delete entirely when it has none (its isTenantWide branch).
export async function openAgentsTab(page: Page): Promise<AgentsView> {
  const nubi = new NubiLocators(page);
  const agents = new CustomAgentsLocators(page);

  await new LoginPage(page).doFullLogin();
  await nubi.openPanel();

  // SettingsModal builds its tab strip from an async hasFeatureAccess('LLM_FUNCTION')
  // round trip, and a Settings click landing while the panel is still animating in
  // opens nothing at all. Retry the pair until the tabs are actually there.
  await expect(async () => {
    // Probe: "the tabs are not up yet" is the normal state on the first pass and
    // must come back as false rather than throw, since that is what drives the retry.
    if (!(await nubi.customAgentTab.isVisible().catch(() => false))) {
      await nubi.settingsBtn.click();
    }
    await nubi.customAgentTab.waitFor({ state: "visible", timeout: 5000 });
  }).toPass({ timeout: 60000, intervals: [1000, 2000, 3000] });

  await nubi.customAgentTab.click();
  await expect(agents.listingBox).toBeVisible({ timeout: 30000 });
  await expectListingSettled(agents);

  return { nubi, agents };
}

// The listing always resolves into one of two states, so waiting for either is what
// separates "the fetch has landed" from "the skeleton is still up". The alternation
// ends in .first() so a render holding both a row and the empty state is an
// either-one wait rather than a strict-mode violation.
export async function expectListingSettled(agents: CustomAgentsLocators): Promise<void> {
  await expect(agents.nameCells.first().or(agents.emptyState).first()).toBeVisible({
    timeout: 60000,
  });
}

// Search is client-side over the already-fetched rows (the useEffect on
// searchAgentByName in ListAgents.jsx), so the filtered table is the signal that
// the term landed — but Enter also refetches, so the settle wait still applies.
export async function searchAgents(agents: CustomAgentsLocators, nubi: NubiLocators, term: string): Promise<void> {
  await nubi.searchAgentInput.click();
  await nubi.searchAgentInput.fill(term);
  await expect(nubi.searchAgentInput).toHaveValue(term, { timeout: 10000 });
  await nubi.searchAgentInput.press("Enter");
  await expectListingSettled(agents);
}

export async function clearAgentSearch(agents: CustomAgentsLocators, nubi: NubiLocators): Promise<void> {
  await nubi.searchAgentInput.fill("");
  await expect(nubi.searchAgentInput).toHaveValue("", { timeout: 10000 });
  await nubi.searchAgentInput.press("Enter");
  await expectListingSettled(agents);
}

export async function pickAgentType(agents: CustomAgentsLocators, optionLabel: RegExp): Promise<void> {
  await agents.agentTypeFilter.click();
  const option = agents.filterOption(optionLabel);
  await expect(option).toBeVisible({ timeout: 15000 });
  await option.click();
  // Agent Type is single-select, and that branch of ds/FilterDropdown's handleToggle
  // ends in setAnchorEl(null) (FilterDropdown.jsx:963), so the panel closes itself.
  // Never press Escape here: this listing sits inside the Settings dialog, so once the
  // popover has gone an Escape reaches that dialog and closes the whole modal — the
  // trap NubiLocators.closeSelectPopover() documents.
  await expect(option).toBeHidden({ timeout: 15000 });
  await expectListingSettled(agents);
}

// Every agent name currently listed. Read through expect.poll at the call sites so
// a re-render in flight is retried rather than snapshotted.
export async function listedAgentNames(agents: CustomAgentsLocators): Promise<string[]> {
  const cells = await agents.nameCells.allInnerTexts();
  return cells.map(agentNameFromCell).filter((name) => name.length > 0);
}

// The agent's own name out of one Name cell.
//
// The cell is three stacked parts (ListAgents.jsx:531-574): a leading badge, the name,
// then type/status chips — each its own innerText line. The badge is an icon for agents
// in the shipped catalog, but getIcon() returns nothing for a user-created agent, and
// those fall back to an <Avatar> holding agent.name[0] — so the first line is a single
// letter for exactly the agents this suite creates. Taking line 0 read "E" instead of
// "e2e_custom_agent_JFNHEQ" and made every created agent invisible to these helpers.
//
// The name is therefore the first line longer than one character: the initial is always
// one, and the chips can never be reached because the name always precedes them. A name
// of one character cannot occur — getLlmIdentifierValidationMessage requires three.
function agentNameFromCell(cellText: string): string {
  const lines = cellText
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0);
  return lines.find((line) => line.length > 1) ?? "";
}

// Fills the create form's four text fields and picks one tool. Only name,
// description and instructions are validated (validateForm in CreateAgentNew.jsx),
// but the tool pick mirrors the proven CreateCustomAgent.spec.ts path so the
// created agent matches what the backend already accepts on dev.
export async function fillAgentForm(nubi: NubiLocators, name: string): Promise<void> {
  await nubi.agentNameInput.waitFor({ state: "visible", timeout: 30000 });
  await nubi.agentNameInput.fill(name);
  await nubi.agentDescriptionInput.fill("Created by the automated e2e coverage pass.");
  await nubi.agenRole.fill("You are a helpful assistant.");
  await nubi.agentInstructionsInput.fill("Testing Only");

  await nubi.selectAgentOrTool.click();
  await nubi.listOfAgentsOrTools.waitFor({ state: "visible", timeout: 15000 });
  await nubi.listOfAgentsOrTools.click({ timeout: 15000 });
  // The picker is a multiple Select — it stays open after a pick, and its backdrop
  // would swallow the next click.
  await nubi.closeSelectPopover();
  await nubi.agentToolUsage.fill("Used for automated testing.");
}

// How many listed agents carry exactly this name. Counted off listedAgentNames() rather
// than a locator: the Name cell also holds the avatar initial and a type chip, so only the
// extracted first line is the agent's own name and only it can be compared exactly.
export async function countNamed(agents: CustomAgentsLocators, name: string): Promise<number> {
  const names = await listedAgentNames(agents);
  return names.filter((listed) => listed === name).length;
}

// The row whose Name cell reads exactly `name`, resolved by position so it shares the one
// extraction path above. Throws rather than returning an empty locator, so a missing row
// reports the name it looked for instead of failing later on an unrelated timeout.
export async function rowForName(agents: CustomAgentsLocators, name: string): Promise<Locator> {
  const names = await listedAgentNames(agents);
  const index = names.indexOf(name);
  if (index < 0) {
    throw new Error(`No agent named "${name}" is listed — the listing held: ${names.join(", ") || "(no rows)"}`);
  }
  return agents.nameCellAt(index).locator("xpath=ancestor::tr[1]");
}

// Opens one row's three-dot menu and clicks an item in it.
export async function chooseRowMenuItem(agents: CustomAgentsLocators, row: Locator, item: Locator): Promise<void> {
  const trigger = agents.rowMenuTrigger(row);
  await expect(trigger).toBeVisible({ timeout: 15000 });
  await trigger.click();
  await expect(item).toBeVisible({ timeout: 15000 });
  await item.click();
}

// Deletes one agent by name and waits until a fresh search no longer returns it.
// Re-searches first: the row it must act on is the one the server has now, not the
// one the table held before the create.
export async function deleteAgentByName(agents: CustomAgentsLocators, nubi: NubiLocators, name: string): Promise<void> {
  await searchAgents(agents, nubi, name);
  const row = await rowForName(agents, name);
  await expect(row).toBeVisible({ timeout: 30000 });

  await chooseRowMenuItem(agents, row, nubi.deleteAgentMenuItem);
  await expect(nubi.confirmDeleteAgentBtn).toBeVisible({ timeout: 15000 });
  await nubi.confirmDeleteAgentBtn.click();
  await expect(agents.nameCellContaining(name)).toHaveCount(0, { timeout: 30000 });
}

// Best-effort cleanup for a test that may have failed before its own delete. The
// listing is re-read from scratch, so an agent that was never created is a normal
// outcome and must come back as "nothing to do" rather than throw.
export async function removeAgentIfPresent(agents: CustomAgentsLocators, nubi: NubiLocators, name: string): Promise<void> {
  // Cleanup runs after a failure, so the modal may already be gone or the search box
  // unreachable; a search that cannot run means there is nothing to clean up either.
  await searchAgents(agents, nubi, name).catch(() => {});
  const present = await agents
    .nameCellContaining(name)
    .first()
    .waitFor({ state: "visible", timeout: 5000 })
    .then(() => true)
    .catch(() => false);
  if (!present) return;
  await deleteAgentByName(agents, nubi, name);
}
