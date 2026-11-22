import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
// The in-scope account id, resolved from the CLUSTER the environment configures. Reused from
// the Home area rather than re-derived here — it is the repo's one way to read back the account
// the app itself considers selected, and it already fails fast when CLUSTER is unset, which is
// what keeps this module free of a hardcoded account or cluster name.
import { resolveAccountId } from "../../Home/homeHelper";
import { TriageRulesLocators } from "./triageRulesLocators";
import {
  CLUSTER_DETAILS_PATH,
  CLUSTER_TRIAGE_RULES_FRAGMENT,
  DELETE_MENU_ITEM,
  EDIT_MENU_ITEM,
  RETRO_MATCH_FIXTURE_TITLE,
  TRIAGE_RULES_FRAGMENT,
  TROUBLESHOOT_PATH,
} from "./triageRulesConstants";

const NO_LISTING_HINT =
  "The Triage Rules pane never settled: neither the loaded table body (#triageRulesManager-body) " +
  "nor the module's own empty heading (#triage-rules-empty-no-data) appeared. Those are the two " +
  "post-fetch shapes of app/src/components/triage/TriageRulesManager.tsx, so neither showing means " +
  "the getTriageRules call never came back.";

// Opens Triage Rules scoped to one cloud account — the view that renders Create Rule, and so
// the only one on which a write case can run.
export async function openTriageRulesForCluster(page: Page): Promise<TriageRulesLocators> {
  const locators = new TriageRulesLocators(page);
  const accountId = await resolveAccountId(page);

  await page.goto(`${CLUSTER_DETAILS_PATH}/${accountId}#${CLUSTER_TRIAGE_RULES_FRAGMENT}`);
  await settleListing(locators);
  return locators;
}

// Opens the same module from /troubleshoot, where it runs unscoped across every account.
export async function openTriageRulesGlobal(page: Page): Promise<TriageRulesLocators> {
  const locators = new TriageRulesLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto(`${TROUBLESHOOT_PATH}#${TRIAGE_RULES_FRAGMENT}`);
  await settleListing(locators);
  return locators;
}

// Waits out the rules fetch. While it is in flight CustomTable swaps in a skeleton <tbody> that
// carries no id, so the id'd body being attached means the response has landed and the rows on
// screen are the current ones. The empty heading is the other settled outcome — which of the two
// shows depends on what the shared dev tenant holds, so neither can be demanded on its own.
export async function settleListing(locators: TriageRulesLocators, timeout = 120000): Promise<void> {
  await expect(locators.tableBody.or(locators.emptyState).first(), NO_LISTING_HINT).toBeAttached({ timeout });
}

// A regex that matches no real alert, so the suppression rule a case creates cannot change how
// any genuine event is processed for the tenant while it briefly exists.
export function inertAlertPattern(ruleName: string): string {
  return `E2ENeverMatches${ruleName.replace(/[^a-zA-Z0-9]/g, "")}`;
}

// Types into the name search and waits for the listing to reflect it. The field filters on
// every keystroke (TriageRulesManager.tsx:552 sets searchQuery straight from onChange), so
// there is nothing to commit with Enter — but the value has to be committed to the input before
// the row assertions run, or they race the controlled re-render.
const MAX_LEFTOVER_RULES = 25;

export async function searchByName(locators: TriageRulesLocators, term: string): Promise<void> {
  await locators.search.click();
  await locators.search.fill(term);
  await expect(locators.search).toHaveValue(term);
  await settleListing(locators);
}

// Picks one option from a ds/FilterDropdown. It is single-select on this toolbar, so the pick
// fires onSelect and closes the popover — the option leaving the DOM is the signal it committed.
export async function pickFilterOption(locators: TriageRulesLocators, trigger: Locator, label: string): Promise<void> {
  await trigger.click();
  const option = locators.filterOption(label);
  await option.click();
  await expect(option).toBeHidden();
  await settleListing(locators);
}

// Opens the create modal and waits for it to be interactive rather than merely mounted — MUI
// runs an open transition, and a fill issued mid-transition lands on a field that is about to
// be re-rendered from the reset effect (TriageRuleModal.tsx:122-138).
export async function openCreateModal(locators: TriageRulesLocators): Promise<void> {
  await locators.createButton.click();
  await expect(locators.ruleModalTitle).toBeVisible();
  await expect(locators.ruleNameField).toBeEditable();
}

// Opens the edit modal for a named rule through its row action menu, and waits for it to be
// interactive on the rule's own pre-filled values — same reset-effect race as openCreateModal.
export async function openEditModal(locators: TriageRulesLocators, name: string): Promise<void> {
  await searchByName(locators, name);
  const row = locators.rowByName(name);
  await locators.rowMenuTrigger(row).click();
  await locators.rowMenuItem(EDIT_MENU_ITEM).click();
  await expect(locators.editRuleModalTitle).toBeVisible();
  await expect(locators.ruleNameField).toHaveValue(name);
}

// Deletes a rule through the row action menu and proves it is gone from the listing. Written to
// be callable from a cleanup path, so it tolerates the row already being absent — a create that
// failed leaves nothing to delete, and that must not mask the original failure.
export async function deleteRuleByName(locators: TriageRulesLocators, name: string): Promise<void> {
  // A failed step can leave a dialog open and failed runs leave duplicates, so close it and delete until none remain.
  await locators.search.press("Escape");
  await searchByName(locators, name);

  for (let removed = 0; removed < MAX_LEFTOVER_RULES; removed++) {
    const row = locators.rowByName(name);
    // A missing row is expected on the cleanup path, so absence returns false instead of throwing.
    const present = await row
      .waitFor({ state: "visible", timeout: removed === 0 ? 15000 : 5000 })
      .then(() => true)
      .catch(() => false);
    if (!present) return;

    await locators.rowMenuTrigger(row).click();
    await locators.rowMenuItem(DELETE_MENU_ITEM).click();
    await locators.deleteConfirmButton.click();
    await expect(locators.ruleModal).toBeHidden();
  }
}

// API-level helper for the retroactive-apply case, which has to read and restore real
// pre-existing events rather than a rule this suite created — no UI flow does either of those.
// Goes through /api/graphql, sharing the authenticated storageState cookie with the browser
// context, so it runs as the signed-in user through the same gateway the UI itself uses.
// Mirrors the established `gql()` shape from tests/admin/Ownership/ownershipHelper.ts.
async function gql(page: Page, query: string, operationName: string, variables: Record<string, unknown> = {}): Promise<any> {
  const res = await page.request.post("/api/graphql", {
    data: { query, operationName, variables },
    headers: { "Content-Type": "application/json" },
    failOnStatusCode: false,
    // playwright.config.ts's actionTimeout (10s on dev) is tuned for UI interactions and is too
    // tight for a real data query under load — this overrides it for this call specifically. A
    // ceiling, not a fixed wait: adds nothing when the call is fast, which is the normal case.
    timeout: 30000,
  });
  const text = await res.text();
  let body: any;
  try {
    body = JSON.parse(text);
  } catch {
    throw new Error(`${operationName} returned non-JSON (HTTP ${res.status()})`);
  }
  const err = body?.errors?.[0];
  if (err) throw new Error(`${operationName} failed: ${err.message ?? JSON.stringify(err)}`);
  return body?.data;
}

const FIND_OPEN_FIXTURE_EVENTS = `
query TriageFixtureOpenEvents($accountId: String!, $title: String!) {
  events_list(where: {_and: [{account_id: {_eq: $accountId}}, {title: {_eq: $title}}, {nb_status: {_eq: "OPEN"}}]}, limit: 500) {
    rows { id }
  }
}`;

const REOPEN_EVENT = `
mutation TriageFixtureReopenEvent($event_id: String!) {
  event_update_nb_status(event_id: $event_id, nb_status: "OPEN") {
    success
  }
}`;

// The exact real, currently-open events under the recurring test-alert fixture title, for this
// account. Read once before a rule ever touches them, so cleanup can restore precisely these.
export async function findOpenFixtureEventIds(page: Page, accountId: string): Promise<string[]> {
  const data = await gql(page, FIND_OPEN_FIXTURE_EVENTS, "TriageFixtureOpenEvents", {
    accountId,
    title: RETRO_MATCH_FIXTURE_TITLE,
  });
  return (data?.events_list?.rows ?? []).map((row: { id: string }) => row.id);
}

// Reverts each event back to OPEN. Real events pre-dating this test, so a triage rule suppressing
// them here must not leave them suppressed once the rule that touched them is gone.
export async function reopenFixtureEvents(page: Page, eventIds: string[]): Promise<void> {
  await Promise.all(eventIds.map((eventId) => gql(page, REOPEN_EVENT, "TriageFixtureReopenEvent", { event_id: eventId })));
}
