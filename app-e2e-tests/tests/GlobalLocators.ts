import { Page, Locator, expect } from "@playwright/test";

export class CommonLocators {
  protected page: Page;

  readonly homeBtn: Locator;
  readonly adminBtn: Locator;
  readonly saveBtn: Locator;
  readonly gitlabsavebutton: Locator;
  readonly servicenowsavebutton: Locator;
  readonly submitBtn: Locator;
  readonly cancelBtn: Locator;
  readonly TroubleshootBtn: Locator;
  readonly OptimizeBtn: Locator;
  readonly InfraBtn: Locator;
  readonly SidenavInfraK8s: Locator;
  readonly SidenavInfraCloud: Locator;

  constructor(page: Page) {
    this.page = page;

    this.homeBtn = page.locator("#home-sidenavbutton");
    this.adminBtn = page.locator("#admin-sidenav");
    this.saveBtn = page.locator("#create-integration-acc");
    this.gitlabsavebutton = page.locator("#create-gitlab-acc");
    this.servicenowsavebutton = page.locator("#create-servicenow-acc");
    this.submitBtn = page.locator("#submit");
    this.cancelBtn = page.getByRole("button", { name: "Cancel" });
    this.OptimizeBtn = page.locator("#optimize-sidenavbutton");
    this.TroubleshootBtn = page.locator("#troubleshoot-sidenavbutton");
    this.InfraBtn = page.locator("#infra-sidenavbutton");
    this.SidenavInfraK8s = page.locator("#sidenav-infra-k8s");
    this.SidenavInfraCloud = page.locator("#sidenav-infra-cloud");
  }

  // K8s and Cloud now live behind the Infra rail button's hover flyout
  // (SubNavFlyout in app/src/components/common/layout/index.jsx), so the old
  // #clusters-sidenavbutton / #cloud-sidenavbutton top-level ids are gone.
  async openInfraSection(subItem: Locator, expectedUrl: RegExp) {
    await expect(this.InfraBtn).toBeVisible();

    await expect(async () => {
      await this.InfraBtn.hover();
      await subItem.waitFor({ state: "visible", timeout: 3000 });
      await subItem.click();
      await this.page.waitForURL(expectedUrl, { timeout: 5000 });
    }).toPass({ timeout: 30000, intervals: [500, 1000, 2000] });

    await this.page.mouse.move(0, 0);
    // Best-effort: /kubernetes redirects into the cluster dashboard, which polls, so networkidle often never arrives.
    await this.page.waitForLoadState("networkidle", { timeout: 15000 }).catch(() => {});
  }

  async openClustersFromSidenav() {
    await this.openInfraSection(this.SidenavInfraK8s, /kubernetes/);
  }

  async openCloudAccountsFromSidenav() {
    await this.openInfraSection(this.SidenavInfraCloud, /cloud-account/);
  }

  // Picks a cloud account in the global account autocomplete.
  //
  // The keyboard commit (ArrowDown + Enter) sometimes lands on the wrong entry:
  // it fires against a list that has not finished re-filtering the freshly typed
  // text, so a stale first option gets committed (e.g. "aws-demo" instead of
  // "dev-aws"). Keyboard selection is still preferred over clicking the option —
  // a click leaves the cursor over the AnchorComponent tabs and trips their
  // hover popovers — so instead the committed value is verified and the whole
  // type-and-select is retried up to `attempts` times.
  async selectCloudAccount(accountName: string, attempts = 3) {
    const input = this.page.locator("#auto-complete-global-cluster");
    const expected = new RegExp(accountName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "i");

    for (let attempt = 1; attempt <= attempts; attempt++) {
      await input.waitFor({ state: "visible", timeout: 10000 });
      await input.click({ clickCount: 3 });
      await input.press("Control+a");
      await input.press("Delete");
      await input.fill("");
      await input.pressSequentially(accountName, { delay: 50 });

      // The option list re-filters asynchronously. Committing while the first
      // entry is still the pre-filter one is what picks the wrong account, so
      // wait for the list to settle instead of racing it.
      const listSettled = await expect(this.page.locator("[role='option']").first())
        .toHaveText(expected, { timeout: 10000 })
        .then(() => true)
        .catch(() => false);

      if (!listSettled) {
        console.log(
          `Option list did not settle on '${accountName}' (attempt ${attempt}/${attempts}), retrying...`,
        );
        continue;
      }

      await this.page.keyboard.press("ArrowDown");
      await this.page.keyboard.press("Enter");
      await this.page.mouse.move(0, 0);

      // Confirm what actually got committed. The input re-renders a beat after
      // Enter, so this polls — reading the value once reports the stale name and
      // would retry a selection that was already correct.
      const committed = await expect(input)
        .toHaveValue(expected, { timeout: 5000 })
        .then(() => true)
        .catch(() => false);

      if (committed) {
        console.log(`Selected cloud account: ${accountName}`);
        return;
      }

      const selected = (await input.inputValue().catch(() => "")).trim();
      console.log(
        `Wrong cloud account selected ('${selected}' instead of '${accountName}') ` +
          `(attempt ${attempt}/${attempts}), retrying...`,
      );
    }

    throw new Error(`Failed to select cloud account '${accountName}' after ${attempts} attempts`);
  }

  getExactText(text: string): Locator {
    return this.page.getByText(text, { exact: true });
  }

  getOption(name: string): Locator {
    return this.page.getByRole("option", { name });
  }
}
