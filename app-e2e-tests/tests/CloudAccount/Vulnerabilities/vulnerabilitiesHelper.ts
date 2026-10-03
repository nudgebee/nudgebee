// Not for OSS
import { Page, Locator, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { VulnerabilitiesLocators } from "./vulnerabilitiesLocators";

// Precondition for every test here: the tenant has at least one active non-K8s cloud
// account. /cloud-account redirects to /cloud-account/details/<id> for the selected
// cluster, or for the first active non-K8s account when the selected one is a K8s cluster
// (app/src/pages/cloud-account/index.jsx); with none it renders a "no accounts" message
// and there is no detail page to test. Named here so a cloud-less tenant fails once with
// the reason rather than every test on an unexplained missing tab.
const NO_ACCOUNT_HINT =
  "The cloud account detail page never loaded. /cloud-account falls back to a 'no accounts' " +
  "message when the tenant has no active non-K8s cloud account — add one (Admin > Integrations) " +
  "before running this suite.";

// Reaching the tab the way a person does: sidenav > Infra > Cloud, which lands on a cloud
// account's detail page, then the tab strip. Deliberately not a goto() of
// /cloud-account/details/<id>#vulnerabilities — that needs an account id this suite does not
// hardcode, and the page's own redirect rewrites the fragment on a cold load.
export async function openVulnerabilitiesTab(page: Page): Promise<VulnerabilitiesLocators> {
  const locators = new VulnerabilitiesLocators(page);
  await new LoginPage(page).doFullLogin();

  await locators.openCloudAccountsFromSidenav();
  await expect(page, NO_ACCOUNT_HINT).toHaveURL(/cloud-account\/details\//, { timeout: 60000 });

  // The strip renders once the account resolves; Summary is the tab the redirect lands on.
  await expect(locators.summaryTab, NO_ACCOUNT_HINT).toBeVisible({ timeout: 60000 });
  await expect(locators.vulnerabilitiesTab).toBeVisible({ timeout: 30000 });
  await locators.vulnerabilitiesTab.click();

  // Park the cursor in open content: left on the strip, AnchorComponent opens the hovered
  // tab's popover over the panel below and the next click hits that instead.
  await page.mouse.move(640, 500);

  await expectSelectedTab(locators.vulnerabilitiesTab);
  await expect(locators.root).toBeVisible({ timeout: 60000 });
  await locators.waitForFindings();

  return locators;
}

// AnchorComponent marks the open tab with data-tab-selected="true". Unlike the URL it is
// always in step with what is rendered, so it is what "which tab am I on" is asserted against.
export async function expectSelectedTab(tab: Locator): Promise<void> {
  await expect(tab).toHaveAttribute("data-tab-selected", "true", { timeout: 15000 });
}
