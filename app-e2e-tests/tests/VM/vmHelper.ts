import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { VmLocators } from "./vmLocators";

// Precondition for every test in this area: the target environment has at least one
// cloud account with cloud_provider = 'SelfHosted'. app/src/pages/vm/index.tsx renders
// VmEmptyState instead of the tab strip when none exists, so the module has no surface
// to exercise at all. Asserted once, here, with a message that names the precondition —
// otherwise a fleet-less environment fails every test on an unexplained missing tab.
const NO_ACCOUNT_HINT =
  "The VM tab strip did not render. /vm falls back to VmEmptyState when the tenant has no " +
  "SelfHosted cloud account — add one (Infra > VM > Add Self-Hosted Account) before running this suite.";

export type VmTab = "summary" | "instances" | "vulnerabilities" | "packages";

// Opens a VM tab the way a person does: land on /vm, let it settle, then click the tab.
//
// Deliberately NOT `goto('/vm#<fragment>')`, even though the page reads the tab from
// window.location.hash on mount. On a cold load that deep link does not survive: the
// page's own ?accountId= sync (router.replace, app/src/pages/vm/index.tsx) rewrites the
// URL without the fragment, AnchorComponent's router.asPath effect then reads an empty
// hash, resets to tab 0 and calls onChangeFilter(0) — so /vm#vulnerabilities lands on
// Summary. Verified in CI: #vm-vulnerability-tab-all and #vm-packages-search were
// "element(s) not found" because those tabs never rendered. Reported as a product bug
// in the PR; the strip is used here so the suite tests the module rather than that bug.
export async function openVmTab(page: Page, fragment: VmTab = "summary"): Promise<VmLocators> {
  const locators = new VmLocators(page);
  await new LoginPage(page).doFullLogin();

  await page.goto("/vm");
  // The page shows a spinner until the self-hosted account list resolves, then either
  // the tab strip or the empty state. Waiting on the strip covers both the spinner and
  // the account-adoption effect that follows it.
  await expect(locators.summaryTab, NO_ACCOUNT_HINT).toBeVisible({ timeout: 60000 });

  // Then wait for the tab body to stop being torn down under us.
  //
  // /vm renders its tabs inside <ErrorBoundary key={`${accountId}-${selectedTab}`}>,
  // and it adopts the first SelfHosted account asynchronously — so on a cold landing
  // accountId goes null -> id, and everything under that key mounts, unmounts and
  // mounts again. Interacting before it settles fails in ways that look like bad
  // locators but are not: "element was detached from the DOM, retrying" on a button
  // that plainly exists, or a search input that passes toBeVisible and is then gone.
  //
  // The same effect writes ?accountId= into the URL, so that parameter appearing is
  // the observable signal that the key has stopped changing.
  await expect(page).toHaveURL(/[?&]accountId=[^&#]+/, { timeout: 60000 });

  // Past that point the sync is done (it only fires while query.accountId differs from
  // the adopted account), so a tab click sticks and its content mounts once.
  const tabs: Record<VmTab, Locator> = {
    summary: locators.summaryTab,
    instances: locators.instancesTab,
    vulnerabilities: locators.vulnerabilitiesTab,
    packages: locators.packagesTab,
  };
  // Summary is tab 0, which a bare /vm already opens — not clicked, so the test that
  // claims Summary is the default landing tab still observes it rather than causing it.
  if (fragment !== "summary") {
    await tabs[fragment].click();
    // Park the cursor in open content. Left on the strip, AnchorComponent opens its
    // tab popover over the toolbar below and the next click hits that instead; parked
    // at 0,0 it sits on the sidebar rail, whose hover flyout is a Popover with an
    // invisible backdrop that swallows clicks page-wide. Mid-viewport is neither.
    await page.mouse.move(640, 500);
  }
  await expectSelectedTab(tabs[fragment]);

  return locators;
}

// AnchorComponent marks the current tab with data-tab-selected="true"
// (app/src/components/common/navigation/AnchorComponent.jsx). This is the app's own
// notion of which tab is open, and unlike the URL it is always in step with what is
// rendered — so it is what "which tab am I on" is asserted against.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 15000 });
}

// Adds the URL fragment on top, for navigation driven by the summary tiles: those go
// through router.push with an explicit hash, so the fragment is part of the behaviour
// under test (a copied deep link has to reopen the same tab).
//
// Deliberately NOT used for the initial landing. The page's ?accountId= sync
// (router.replace in app/src/pages/vm/index.tsx) can rewrite the URL without the
// fragment, so `/vm#summary` may settle as a bare `/vm?accountId=…` even though the
// Summary tab is open and correct. Asserting the fragment there tests a quirk of the
// page's history handling, not the tab strip — see PR Follow-ups.
export async function expectActiveTab(page: Page, fragment: string, tab: Locator): Promise<void> {
  await expect(page).toHaveURL(new RegExp(`#${fragment}\\b`), { timeout: 15000 });
  await expectSelectedTab(tab);
}
