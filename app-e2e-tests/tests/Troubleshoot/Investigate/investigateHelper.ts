// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { assertTroubleshootEnv, openEventSubTab } from "../troubleshootEventsHelper";
import { EventSubTabs } from "../troubleshootEventsLocators";
import { InvestigateLocators, InvestigateTab, InvestigateTabs } from "./investigateLocators";

// Named so a tenant with nothing to investigate fails with the reason rather
// than with an unexplained missing link. KubernetesEvents.jsx only renders the
// Investigate control for rows that carry an aggregation_key, so an events
// listing that is merely non-empty is not enough.
export const NO_INVESTIGABLE_EVENT_HINT =
  "No row on Troubleshoot > Events offered an Investigate link. KubernetesEvents.jsx renders that control only " +
  "when the event has an aggregation_key, so this tenant has no classified event to investigate.";

const NO_INVESTIGATION_HINT =
  "The investigation card never rendered. /investigate shows either the card itself or, when the cluster carries " +
  "show_investigation_button=true, a CTA that has to be clicked first — neither was reachable.";

// Long by default: reaching an investigation means a full login, the Troubleshoot
// listing, and then a page that fans out to the assistant for its cards.
export const INVESTIGATE_TIMEOUT_MS = 240000;

// A syntactically valid uuid that no event can own, for the unknown-id case.
// Fixed rather than random: the page's behaviour must not depend on which one.
export const UNKNOWN_EVENT_ID = "00000000-0000-4000-8000-000000000000";

// Lands on Troubleshoot > Events, the listing that links into this module.
export async function openEventsListing(page: Page): Promise<InvestigateLocators> {
  assertTroubleshootEnv();
  const locators = new InvestigateLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.navigateToTroubleshoot();
  await expect(locators.eventTabsBox).toBeVisible({ timeout: 60000 });
  await openEventSubTab(locators, EventSubTabs.events);
  await expect(locators.eventsTable).toBeVisible({ timeout: 60000 });
  return locators;
}

// The page has two shapes and the cluster decides which: with the cloud-account
// attribute show_investigation_button=true it parks behind a CTA, otherwise it
// renders the investigation card straight away. Probing for the CTA and clicking
// it only when present is what makes this work on either kind of cluster.
export async function revealInvestigationCard(locators: InvestigateLocators): Promise<void> {
  // Wait for whichever shape this cluster renders rather than probing for the CTA
  // on a fixed timeout: investigate.jsx picks exactly one of the two branches
  // (isRenderInvestigationCard = !showDemoMessage), so they can never both be on
  // screen. Racing them is both safe on a CTA cluster and free on a direct-render
  // one, where a probe long enough to be safe would be dead time in every test.
  await expect(
    locators.askForInvestigationBtn.or(locators.tab(InvestigateTabs.tasks)),
    NO_INVESTIGATION_HINT
  ).toBeVisible({ timeout: 120000 });

  // Safe as a non-waiting read only because the race above already settled: one
  // of the two is on screen, so a false here means the card rendered directly.
  if (await locators.askForInvestigationBtn.isVisible()) {
    await locators.askForInvestigationBtn.click();
  }

  await expect(locators.tab(InvestigateTabs.tasks), NO_INVESTIGATION_HINT).toBeVisible({ timeout: 120000 });
}

// Follows the first Investigate link on the Events listing, the way a person
// reaches this module, and waits for the investigation to be on screen.
export async function openFirstInvestigation(page: Page): Promise<InvestigateLocators> {
  const locators = await openEventsListing(page);

  await expect(locators.investigateLink, NO_INVESTIGABLE_EVENT_HINT).toBeVisible({ timeout: 60000 });
  await locators.investigateLink.click();
  await page.waitForURL(/\/investigate\?/, { timeout: 60000 });

  await revealInvestigationCard(locators);
  return locators;
}

// MUI Tab marks the open tab with aria-selected. That is the app's own record of
// which panel is rendered and, unlike the URL, it is never mid-rewrite.
export async function expectSelectedTab(locators: InvestigateLocators, tab: InvestigateTab): Promise<void> {
  await expect(locators.tab(tab)).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
}

// Clicks a tab and waits until the app agrees it is open.
export async function openInvestigateTab(locators: InvestigateLocators, tab: InvestigateTab): Promise<void> {
  const target = locators.tab(tab);
  await expect(target).toBeVisible({ timeout: 30000 });
  await target.click();
  await expectSelectedTab(locators, tab);
}

// Opens the More Actions menu and reports whether the wanted entry is offered.
// The menu is assembled from the event's own permissions and source, so a
// missing entry is a normal branch rather than a failure.
export async function openMoreActions(locators: InvestigateLocators, label: string): Promise<boolean> {
  await expect(locators.moreActionsBtn).toBeVisible({ timeout: 30000 });
  await locators.moreActionsBtn.click();
  await expect(locators.openMenu).toBeVisible({ timeout: 30000 });

  // Short: the menu surface is already open above and its items render with it,
  // so an entry that is not there after a moment is not coming.
  return locators
    .menuItem(label)
    .waitFor({ state: "visible", timeout: 3000 })
    .then(() => true)
    // Absence is expected: isK8s, write access and an already-linked ticket each
    // remove entries from this menu.
    .catch(() => false);
}

// Closes an open menu without choosing anything, so a probe leaves no side effect.
export async function dismissMenu(page: Page): Promise<void> {
  await page.keyboard.press("Escape");
}

// Reads the count investigate.jsx renders beside the Tasks label. Returns null
// when the label carries no count, which is itself a fact a test can assert on.
export function parseTaskCount(label: string): number | null {
  const match = label.match(/\((\d+)\)/);
  return match ? Number(match[1]) : null;
}

// Pulls the two query parameters the module is addressed by out of a listing
// link or the browser url. Relative hrefs are resolved against BASE_URL so the
// same parser works on both.
export function parseInvestigateParams(href: string): { id: string; accountId: string } {
  const url = new URL(href, process.env.BASE_URL);
  return {
    id: url.searchParams.get("id") ?? "",
    accountId: url.searchParams.get("accountId") ?? "",
  };
}
