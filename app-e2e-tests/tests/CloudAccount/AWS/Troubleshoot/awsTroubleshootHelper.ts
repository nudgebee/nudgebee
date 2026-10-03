// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { AwsTroubleshootLocators } from "./awsTroubleshootLocators";

export type TroubleshootSubTab = "events" | "triage-rules" | "threshold-suggestions";

// Every test in this area drives a shared dev cloud account, so the timeout has to cover
// login, the integration check, account selection and the sub-tab's own first fetch.
export const TROUBLESHOOT_TIMEOUT_MS = 180000;

interface SubTabTarget {
  tab: Locator;
  url: RegExp;
  root: Locator;
}

function targetsFor(locators: AwsTroubleshootLocators): Record<TroubleshootSubTab, SubTabTarget> {
  return {
    events: {
      tab: locators.TroubleshootEvents,
      url: locators.TroubleshootEventsUrl,
      root: locators.eventsRoot,
    },
    "triage-rules": {
      tab: locators.TroubleshootTriageRules,
      url: locators.TroubleshootTriageRulesUrl,
      root: locators.rulesRoot,
    },
    "threshold-suggestions": {
      tab: locators.TroubleshootThresholdSuggestions,
      url: locators.TroubleshootThresholdSuggestionsUrl,
      root: locators.suggestionsRoot,
    },
  };
}

// Opens one Troubleshoot sub-tab the way a person does: log in, land on the AWS cloud
// account, then hover the Troubleshoot anchor and pick the sub-tab from its flyout.
//
// Deliberately NOT a goto('...#events/triage-rules') deep link — the detail page derives
// selectedSubTab from its own tab state, and the sub-tabs live behind AnchorComponent's hover
// popover, which is the only route a user has. navigateToSubTab (AWSLocators) is the merged,
// retrying implementation of that hover-and-click, so it is reused rather than rewritten here.
export async function openTroubleshootSubTab(page: Page, subTab: TroubleshootSubTab): Promise<AwsTroubleshootLocators> {
  const locators = new AwsTroubleshootLocators(page);

  // doFullLogin already suppresses the guided tours and installs the overlay guard.
  await new LoginPage(page).doFullLogin();
  await locators.openAWSCloudAccountFromConfig();

  await goToSubTab(locators, page, subTab);
  return locators;
}

// Moves to another sub-tab within one test, without paying for a second login. Same
// hover-and-click route as the initial landing.
export async function goToSubTab(locators: AwsTroubleshootLocators, page: Page, subTab: TroubleshootSubTab): Promise<void> {
  const target = targetsFor(locators)[subTab];

  await locators.navigateToSubTab(locators.AnchorTabTroubleshoot, target.tab, target.url);
  await parkCursor(page);

  // The panel root mounting is the signal the sub-tab finished switching. Waiting on it
  // rather than on networkidle: the events listing polls, so networkidle often never arrives.
  await expect(target.root).toBeVisible({ timeout: 60000 });
}

// The root of a sub-tab that should NOT be mounted, for asserting a switch actually replaced
// the previous panel rather than rendering alongside it.
export function rootOf(locators: AwsTroubleshootLocators, subTab: TroubleshootSubTab): Locator {
  return targetsFor(locators)[subTab].root;
}

// Parks the cursor in open page content after a tab change. Left on the anchor strip,
// AnchorComponent keeps its flyout open over the toolbar and the next click hits that instead;
// parked at 0,0 it sits on the sidebar rail, whose own flyout is a Popover with an invisible
// page-wide backdrop. Mid-viewport is neither.
async function parkCursor(page: Page): Promise<void> {
  await page.mouse.move(640, 500);
}

// Settles a listing to a row count, racing the first row against the empty state.
//
// The swallow is the point: neither side arriving is still an answer callers handle (0), so
// this must settle to a count rather than throw. Each side carries its own catch rather than
// one around the race — the loser stays pending and would otherwise reject unhandled after the
// winner has already returned.
async function settledRowCount(rows: Locator, emptyState: Locator, timeoutMs: number): Promise<number> {
  await Promise.race([
    rows
      .first()
      .waitFor({ state: "visible", timeout: timeoutMs })
      .catch(() => {}),
    emptyState.waitFor({ state: "visible", timeout: timeoutMs }).catch(() => {}),
  ]);
  return rows.count();
}

export async function ruleRowCount(locators: AwsTroubleshootLocators, timeoutMs = 30000): Promise<number> {
  return settledRowCount(locators.ruleRows, locators.rulesEmptyState, timeoutMs);
}

export async function suggestionRowCount(locators: AwsTroubleshootLocators, timeoutMs = 30000): Promise<number> {
  return settledRowCount(locators.suggestionRows, locators.suggestionsEmptyState, timeoutMs);
}

export async function eventRowCount(locators: AwsTroubleshootLocators, timeoutMs = 30000): Promise<number> {
  return settledRowCount(locators.eventRows, locators.eventsEmptyState, timeoutMs);
}

// A name no rule on the shared account can already carry, so a search or a create is about
// this test's own row rather than about what dev happens to hold.
export function uniqueRuleName(): string {
  return `e2e-auto-triage-${Date.now()}`;
}

// Removes a rule this suite created, through the row menu and the delete confirmation.
//
// Used both as the delete step under assertion and as the cleanup path when a later step
// fails, so it must be safe to call when the row is already gone — hence the count check
// rather than an unconditional click.
export async function deleteRuleNamed(locators: AwsTroubleshootLocators, name: string): Promise<void> {
  const row = locators.ruleRowNamed(name).first();
  if ((await row.count()) === 0) {
    return;
  }

  await locators.rowMenuTrigger(row).click();
  await locators.menuItem(/^Delete$/).click();

  const dialog = locators.deleteRuleDialog(name);
  await expect(dialog).toBeVisible({ timeout: 15000 });
  await locators.dialogSubmit(dialog, /^Delete$/).click();
  await expect(dialog).toBeHidden({ timeout: 30000 });
}
