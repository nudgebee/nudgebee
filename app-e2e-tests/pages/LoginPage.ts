import { Page, Locator } from "@playwright/test";
import { writeFileSync, mkdirSync } from "fs";
import { PLAYWRIGHT_REPORT_DIR, TENANT_FILE_PATH } from "../tests/utils/paths";
import { doDevLogin } from "./devLogin";

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
    this.submitButton = page.getByRole("button", { name: /^sign in$/i }).or(page.getByRole("button", { name: /^submit$/i })).first();
    this.accountSettingsButton = page.locator("#account-setting");
    this.switchTenantMenu = page.getByText("Switch Tenant");
    this.tenantInput = page.locator("#auto-complete-tenant");
    this.switchTenantSubmitButton = page.getByRole("button", { name: "Switch Tenant" });
    this.homeButton = page.getByText("Home", { exact: true }).first();
    this.clusterInput = page.locator("#auto-complete-global-cluster");
  }

  async navigate() {
    await this.page.goto(process.env.BASE_URL || "");
  }

  async login(username: string, password: string) {
    const isLdapFormVisible = await this.usernameInput.isVisible().catch(() => false);
    if (!isLdapFormVisible) {
      for (let attempt = 1; attempt <= 3; attempt++) {
        const ldapBtn = this.page.getByText("Login via LDAP", { exact: false }).first();
        await ldapBtn.waitFor({ state: "visible", timeout: 15000 });
        await ldapBtn.click();
        const appeared = await this.usernameInput
          .waitFor({ state: "visible", timeout: 5000 })
          .then(() => true)
          .catch(() => false);
        if (appeared) break;
        console.log(`LDAP form not visible after attempt ${attempt} — reloading`);
        await this.page.reload();
      }
    }

    await this.usernameInput.waitFor({ state: "visible", timeout: 20000 });
    await this.passwordInput.waitFor({ state: "visible", timeout: 20000 });

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

  private resolveHighestIteration(items: string[]): string | null {
    const pattern = /^iteration-(\d+)$/i;
    return items.reduce<{ item: string; num: number } | null>((best, raw) => {
      const item = raw.trim();
      const match = item.match(pattern);
      if (!match) return best;
      const num = parseInt(match[1], 10);
      return !best || num > best.num ? { item, num } : best;
    }, null)?.item ?? null;
  }

  async switchTenant() {
    const accountBtnVisible = await this.accountSettingsButton
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);

    if (!accountBtnVisible) {
      console.log("Account settings button not visible — navigating to base URL");
      await this.page.goto(process.env.BASE_URL || "");
      await this.accountSettingsButton.waitFor({ state: "visible", timeout: 15000 });
    }

    for (let attempt = 1; attempt <= 3; attempt++) {
      await this.accountSettingsButton.click();
      const menuVisible = await this.switchTenantMenu
        .waitFor({ state: "visible", timeout: 5000 })
        .then(() => true)
        .catch(() => false);
      if (menuVisible) break;
      console.log(`Switch Tenant menu not visible after attempt ${attempt} — retrying account button`);
      await this.page.keyboard.press("Escape");
    }

    await this.switchTenantMenu.click();

    const dialog = this.page.locator('[role="dialog"]');
    const dialogVisible = await dialog
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);

    if (!dialogVisible) {
      console.log("Switch Tenant dialog not visible — retrying full account settings flow");
      await this.accountSettingsButton.click();
      await this.switchTenantMenu.waitFor({ state: "visible", timeout: 10000 });
      await this.switchTenantMenu.click();
      await dialog.waitFor({ state: "visible", timeout: 10000 });
    }

    const tenantSelect = this.page.locator("#switch-tenant-select");
    await tenantSelect.waitFor({ state: "visible", timeout: 10000 });

    for (let attempt = 1; attempt <= 3; attempt++) {
      await tenantSelect.click();
      const optionsVisible = await this.page
        .locator('[role="option"]')
        .first()
        .waitFor({ state: "visible", timeout: 6000 })
        .then(() => true)
        .catch(() => false);
      if (optionsVisible) break;
      console.log(`Tenant dropdown options not visible after attempt ${attempt} — retrying`);
    }

    const searchInput = this.page.getByPlaceholder(/search/i).first();
    const isSearchVisible = await searchInput.isVisible().catch(() => false);
    if (isSearchVisible) {
      await searchInput.fill("iteration");
      await this.page.waitForTimeout(300);
    }

    await this.page.locator('[role="option"]').first().waitFor({ state: "visible", timeout: 10000 });
    const allOptions = await this.page.locator('[role="option"]').allTextContents();
    const tenantName = this.resolveHighestIteration(allOptions);
    if (!tenantName) throw new Error("No iteration-N tenant found in tenant dropdown");
    console.log(`Auto-detected highest iteration tenant: ${tenantName}`);

    if (isSearchVisible) {
      await searchInput.fill(tenantName);
      await this.page.waitForTimeout(300);
    }

    const option = this.page
      .locator('[role="option"]')
      .filter({ has: this.page.getByText(tenantName, { exact: true }) })
      .first();
    await option.waitFor({ state: "visible", timeout: 10000 });
    await option.click();

    await this.switchTenantSubmitButton.waitFor({ state: "visible", timeout: 10000 });
    await this.switchTenantSubmitButton.click();
    try {
      mkdirSync(PLAYWRIGHT_REPORT_DIR, { recursive: true });
      writeFileSync(TENANT_FILE_PATH, tenantName);
    } catch { }
    console.log(`Switched to tenant: ${tenantName}`);
    await this.page.waitForTimeout(2000);
  }

  async selectHighestIterationCluster(): Promise<void> {
    await this.clearAndTypeCluster("iteration");
    await this.page.locator("[role='option']").first().waitFor({ state: "visible", timeout: 10000 });

    const options = await this.page.locator("[role='option']").allTextContents();
    const clusterName = this.resolveHighestIteration(options);

    if (!clusterName) throw new Error("No iteration-N cluster found in cluster dropdown");
    console.log(`Auto-detected highest iteration cluster: ${clusterName}`);
    await this.selectCluster(clusterName);
  }

  private async clearAndTypeCluster(clusterName: string) {
    await this.clusterInput.waitFor({ state: "visible", timeout: 10000 });
    await this.clusterInput.click({ clickCount: 3 });
    await this.clusterInput.press("Control+a");
    await this.clusterInput.press("Delete");
    await this.clusterInput.fill("");
    await this.clusterInput.pressSequentially(clusterName, { delay: 50 });
  }

  async selectCluster(clusterName: string) {
    await this.clearAndTypeCluster(clusterName);
    await this.page.waitForTimeout(500);

    const option = this.page
      .locator("[role='option']")
      .filter({ hasText: clusterName })
      .first();

    const isVisible = await option.isVisible().catch(() => false);
    if (!isVisible) {
      console.log(`No option found for '${clusterName}', retrying...`);
      await this.clearAndTypeCluster(clusterName);
      await this.page.waitForTimeout(500);
    }

    await option.waitFor({ state: "visible", timeout: 10000 });
    await option.click();
    await this.page.mouse.move(0, 0);
    console.log(`Selected cluster: ${clusterName}`);
  }

  async doFullLogin() {
    if (process.env.E2E_ENVIRONMENT === "dev") {
      await doDevLogin(this.page);
      return;
    }

    const username = process.env.LDAP_USERNAME || "";
    const password = process.env.LDAP_PASSWORD || "";

    if (!username || !password) {
      throw new Error("LDAP_USERNAME or LDAP_PASSWORD missing");
    }

    await this.navigate();
    await this.login(username, password);
    await this.waitForLoaderToDisappear();

    if (this.isAuthErrorPage()) {
      await this.page.goto(process.env.BASE_URL || "");
      await this.login(username, password);
    } else if (this.isSigninPage()) {
      await this.login(username, password);
    }

    await this.page.waitForURL(`${process.env.BASE_URL}/**`, { timeout: 30000 });
    await this.switchTenant();
    await this.waitForLoaderToDisappear();
    const explicitCluster = process.env.CLUSTER_NAME || process.env.CLUSTER;
    if (explicitCluster) {
      await this.selectCluster(explicitCluster);
    } else {
      await this.selectHighestIterationCluster();
    }
    await this.waitForLoaderToDisappear();
  }

  async waitForLoaderToDisappear() {
    const loader = this.page.getByAltText("Loading...");
    await loader.waitFor({ state: "hidden", timeout: 180000 }).catch(() => {});
  }
}
