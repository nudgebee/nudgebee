// Not for OSS
import { Page, expect } from "@playwright/test";
import { LoginPage } from "../../../../pages/LoginPage";
import { Ec2Locators } from "./ec2Locators";
import { LIFECYCLE_ACTIONS } from "./ec2Constants";

export type Ec2SubTab = "summary" | "instances";

// The shared AWS entry flow — Admin > Integrations status check, then sidenav > Cloud,
// then the account picker — runs against a shared dev environment, and its internal
// budgets occasionally blow under the two-worker CI layout (PW_WORKERS, PR #37745).
// Seen twice on this branch: run 33950875562 timed out on #Aws-section-card (10s) and
// run 33951202794 on waitForURL(/cloud-account/) (15s), both passing on Playwright's
// own retry. Retrying here keeps the fix inside this module: raising those budgets in
// AWSLocators instead would change a file every AWS spec shares, which pulls the whole
// tests/CloudAccount/AWS tree into `RUN_MODE: affected` on every run of this PR.
//
// Only a timeout is retried. A test.skip() raised when the AWS integration is inactive,
// and any assertion failure, must propagate on the first attempt rather than being
// retried into a slower version of the same outcome.
async function openAccountWithRetry(locators: Ec2Locators, attempts = 3): Promise<void> {
  for (let attempt = 1; attempt <= attempts; attempt++) {
    try {
      await locators.openAWSCloudAccountFromConfig();
      return;
    } catch (error) {
      const isTimeout =
        error instanceof Error && (error.name === "TimeoutError" || /Timeout \d+ms exceeded/.test(error.message));
      if (attempt === attempts || !isTimeout) {
        throw error;
      }
      console.log(`AWS cloud account navigation timed out (attempt ${attempt}/${attempts}), retrying...`);
    }
  }
}

// Lands on Infra > Cloud > the AWS account and opens one EC2 sub-tab the way a person
// does: pick the account, hover the EC2 anchor tab, click the entry in its dropdown.
//
// Deliberately NOT goto('/cloud-account/details/<id>#ec2/instances'): the account id is
// per-environment data this suite has no business hardcoding, and the page resolves its
// tab from filterOptions, which is only final once cloud_provider has loaded
// ([CloudAccountDetails].jsx:500-516) — a cold deep link can therefore settle on
// Summary. openAWSCloudAccountFromConfig also carries the integration gate, so an
// environment with no active AWS account skips rather than failing on a missing tab.
export async function openEc2SubTab(page: Page, subTab: Ec2SubTab): Promise<Ec2Locators> {
  const locators = new Ec2Locators(page);

  await new LoginPage(page).doFullLogin();
  await openAccountWithRetry(locators);

  if (subTab === "instances") {
    await locators.navigateToSubTab(locators.AnchorTabEC2, locators.EC2Instances, locators.EC2InstancesUrl);
  } else {
    await locators.navigateToSubTab(locators.AnchorTabEC2, locators.EC2Summary, locators.EC2SummaryUrl);
  }

  // Park the cursor in open content. Left on the anchor strip, the tab's own dropdown
  // reopens over the toolbar below and the next click hits that instead; parked at 0,0
  // it sits on the sidebar rail, whose hover flyout is a Popover with an invisible
  // backdrop that swallows clicks page-wide. Mid-viewport is neither.
  await page.mouse.move(640, 500);

  return locators;
}

// Opens the Instances sub-tab and waits for its first fetch to land, returning how many
// instances the shared dev account currently holds. Several tests branch on this: an
// account with no EC2 rows renders the empty panel instead of a table, and that is a
// legitimate state of the environment rather than a failure of the code under test.
export async function openInstances(page: Page): Promise<{ locators: Ec2Locators; rowCount: number }> {
  const locators = await openEc2SubTab(page, "instances");
  await expect(locators.instancesRoot).toBeVisible({ timeout: 60000 });
  const rowCount = await locators.instanceRowCount();
  return { locators, rowCount };
}

// The first lifecycle action a row actually offers, or null when every one is greyed
// out. buildMenuItems disables an action the instance's state does not allow and every
// action when the account is read-only (resourceActions.ts:269-274), so an all-disabled
// menu is a normal outcome that the caller asserts on rather than an error.
export async function firstEnabledLifecycleAction(locators: Ec2Locators): Promise<string | null> {
  for (const action of LIFECYCLE_ACTIONS) {
    const item = locators.actionMenuItem(action);
    if ((await item.count()) === 0) continue;
    const disabled = await item.getAttribute("aria-disabled");
    const muiDisabled = await item.evaluate((node) => node.classList.contains("Mui-disabled"));
    if (disabled !== "true" && !muiDisabled) {
      return action;
    }
  }
  return null;
}
