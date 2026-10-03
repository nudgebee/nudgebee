// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { ServiceCriticalityLocators } from "./serviceCriticalityLocators";

// The Service Criticality listing is scoped to one cloud account, so the area cannot be
// opened without a cluster. Read inside the function rather than at module scope: a
// throw at import time would break `playwright test --list` on a machine with no env.
export function requireCluster(): string {
  const cluster = process.env.CLUSTER_NAME || process.env.CLUSTER || "";
  if (!cluster) {
    throw new Error("CLUSTER_NAME or CLUSTER is not set — add it to .env / .env.dev");
  }
  return cluster;
}

// Opens Cluster Details > Events > Service Criticality the way a person does.
//
// Deliberately NOT a `goto` on `#events/service-criticality`: the sub-tab is reached
// through AnchorComponent, which renders `#dropdown-<id>` items and drives the hash
// itself — TroubleshootTabLocator.clickTab already owns that flyout dance and is reused
// here rather than duplicated.
export async function openServiceCriticalityTab(page: Page): Promise<ServiceCriticalityLocators> {
  requireCluster();
  const locators = new ServiceCriticalityLocators(page);

  await new LoginPage(page).doFullLogin();
  await locators.openClusterFromConfig();
  await locators.navigateToTroubleshootTab();
  await locators.clickTab(locators.TroubleshootServiceCriticality);

  // AnchorComponent composes the sub-tab hash as `<parent fragment>/<child fragment>`.
  await expect(page).toHaveURL(/#events\/service-criticality/, { timeout: 30000 });
  await locators.waitForListing();

  return locators;
}

// A term no workload name can hold, so the "no results" assertions are about the filter
// working rather than about what the dev cluster happens to be running.
export function noMatchTerm(): string {
  return `zz-no-such-workload-${Date.now()}`;
}
