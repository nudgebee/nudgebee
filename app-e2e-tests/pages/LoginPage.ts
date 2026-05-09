import { Page, Locator, expect } from "@playwright/test";
import { getEnvironment } from "../config/environments";

export class LoginPage {
  readonly page: Page;
  readonly usernameInput: Locator;
  readonly passwordInput: Locator;
  readonly submitButton: Locator;
  readonly accountSettingsButton: Locator;
  readonly switchTenantMenu: Locator;
  readonly tenantInput: Locator;
  readonly switchTenantSubmitButton: Locator;
  readonly homeButton: Locator;
  readonly clusterInput: Locator;

  constructor(page: Page) {
    this.page = page;
    this.usernameInput = page.getByRole("textbox", { name: "LDAP Username" });
    this.passwordInput = page.getByRole("textbox", { name: "LDAP Password" });
    this.submitButton = page.getByRole("button", { name: "Submit" });
    this.accountSettingsButton = page.locator("#account-setting");
    this.switchTenantMenu = page.getByText("Switch Tenant");
    this.tenantInput = page.locator("#auto-complete-tenant");
    this.switchTenantSubmitButton = page.getByRole("button", {
      name: "Switch Tenant",
    });
    this.homeButton = page.getByText("Home", { exact: true }).first();
    this.clusterInput = page.locator("#auto-complete-global-cluster");
  }

  async navigate() {
    const env = getEnvironment();
    await this.page.goto(env.baseUrl);
  }

  async login(username: string, password: string) {
    await this.usernameInput.waitFor({ state: "visible", timeout: 10000 });
    await this.passwordInput.waitFor({ state: "visible", timeout: 10000 });

    console.log("Entering LDAP username");
    await this.usernameInput.click();
    await this.usernameInput.fill("");
    await this.usernameInput.pressSequentially(username, { delay: 20 });

    console.log("Entering LDAP password");
    await this.passwordInput.click();
    await this.passwordInput.fill("");
    await this.passwordInput.pressSequentially(password, { delay: 20 });

    console.log("Clicking Submit button");
    await this.submitButton.click();

    console.log("Waiting 5 seconds for redirect after submit");
    await this.page.waitForTimeout(5000);

    console.log("Current URL after submit:", this.page.url());
  }

  private isSigninPage(): boolean {
    const isSignin = this.page.url().includes("/signin");
    console.log("Is signin page:", isSignin);
    return isSignin;
  }

  private isAuthErrorPage(): boolean {
    const isAuthError = this.page.url().includes("/api/auth/error");
    console.log("Is auth error page:", isAuthError);
    return isAuthError;
  }

  async switchTenant(tenantName: string) {
    await this.accountSettingsButton.click();
    await this.switchTenantMenu.waitFor({ state: "visible", timeout: 10000 });
    await this.switchTenantMenu.click();

    // Wait for the Switch Tenant dialog (new UI uses FilterDropdownButton)
    const dialog = this.page.locator('[role="dialog"]');
    await dialog.waitFor({ state: "visible", timeout: 10000 });

    // Click the tenant FilterDropdownButton (renders as "Tenant <value>" button).
    // Wait for enabled state separately — the button starts disabled while loading tenant data.
    const tenantDropdownBtn = dialog.locator('button').filter({ hasText: /Tenant/ }).first();
    await tenantDropdownBtn.waitFor({ state: "visible", timeout: 10000 });
    await expect(tenantDropdownBtn).toBeEnabled({ timeout: 15000 });
    await tenantDropdownBtn.click();

    // If a search box is present (for large tenant lists), filter by name
    const searchInput = this.page.locator('input[placeholder="Search..."]');
    const isSearchVisible = await searchInput.isVisible().catch(() => false);
    if (isSearchVisible) {
      await searchInput.fill(tenantName);
      await this.page.waitForTimeout(300);
    }

    // Click the matching option in the popover
    const option = this.page.locator('[role="option"]').filter({ has: this.page.getByText(tenantName, { exact: true }) }).first();
    await option.waitFor({ state: "visible", timeout: 10000 });
    await option.click();

    await this.switchTenantSubmitButton.waitFor({ state: "visible" });
    await this.switchTenantSubmitButton.click();
    console.log(`Switched to tenant: ${tenantName}`);
    await this.page.waitForTimeout(2000);
  }

  private async clearAndTypeCluster(clusterName: string) {
    await this.clusterInput.click({ clickCount: 3 });
    await this.clusterInput.press("Control+a");
    await this.clusterInput.press("Delete");
    await this.clusterInput.fill("");
    await this.clusterInput.pressSequentially(clusterName, { delay: 50 });
  }

  async selectCluster(clusterName: string) {
    await this.clearAndTypeCluster(clusterName);

    // Wait briefly for dropdown to populate
    await this.page.waitForTimeout(500);

    const option = this.page
      .locator("[role='option']")
      .filter({ hasText: clusterName })
      .first();

    // If no matching option found, clear completely and retype once
    const isVisible = await option.isVisible().catch(() => false);
    if (!isVisible) {
      console.log(`No option found for '${clusterName}', retrying...`);
      await this.clearAndTypeCluster(clusterName);
      await this.page.waitForTimeout(500);
    }

    await option.waitFor({ state: "visible", timeout: 10000 });
    await option.click();
    // Move mouse to top-left corner after selection so the cursor does not
    // land on an AnchorComponent tab (e.g. "Security & Tools") when the page
    // navigates and re-renders, which would trigger onMouseOver and open a
    // popover that intercepts subsequent clicks.
    await this.page.mouse.move(0, 0);
    console.log(`Selected cluster: ${clusterName}`);
  }

  async doFullLogin() {
    const username = process.env.LDAP_USERNAME || "";
    const password = process.env.LDAP_PASSWORD || "";
    const env = getEnvironment();
    const tenantName = env.tenant;
    const clusterName = process.env.CLUSTER_NAME || process.env.CLUSTER || "iteration-test";

    if (!username || !password) {
      throw new Error("LDAP_USERNAME or LDAP_PASSWORD missing");
    }

    await this.navigate();
    await this.login(username, password);
    await this.waitForLoaderToDisappear();

    if (this.isAuthErrorPage()) {
      const env = getEnvironment();
      await this.page.goto(env.baseUrl);
      await this.login(username, password);
    } else if (this.isSigninPage()) {
      await this.login(username, password);
    }

    // Wait for the post-login redirect to settle before attempting tenant switch.
    // Without this, the page navigates to /home mid-dialog, detaching the tenant button.
    await this.page.waitForURL(/\/(home|workflow)/, { timeout: 30000 });
    if (process.env.E2E_ENVIRONMENT !== "dev") {
      await this.switchTenant(tenantName);
      await this.waitForLoaderToDisappear();
    }
    await this.selectCluster(clusterName);
    await this.waitForLoaderToDisappear();
  }

  async waitForLoaderToDisappear() {
    const loader = this.page.getByAltText("Loading...");
    await loader.waitFor({ state: "hidden", timeout: 180000 });
  }
}
