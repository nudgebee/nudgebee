// Not for OSS
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

// A name no other rule can hold, so every case stays order-free and safe to run twice against
// the shared dev tenant. The random tail matters as much as the clock: two workers can enter
// the same millisecond.
export function uniqueRuleName(): string {
  return `e2e-triage-rule-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
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

// Deletes a rule through the row action menu and proves it is gone from the listing. Written to
// be callable from a cleanup path, so it tolerates the row already being absent — a create that
// failed leaves nothing to delete, and that must not mask the original failure.
export async function deleteRuleByName(locators: TriageRulesLocators, name: string): Promise<void> {
  await searchByName(locators, name);

  const row = locators.rowByName(name);
  // A missing row is an expected outcome on the cleanup path, so absence has to come back as
  // false rather than throw.
  const present = await row
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true)
    .catch(() => false);
  if (!present) return;

  await locators.rowMenuTrigger(row).click();
  await locators.deleteMenuItem().click();
  await locators.deleteConfirmButton.click();

  await expect(locators.rowByName(name)).toHaveCount(0, { timeout: 60000 });
}
