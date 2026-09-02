import { Page, Locator } from "@playwright/test";
import { writeFileSync, mkdirSync, readFileSync } from "fs";
import { AUTH_STATE_DIR, TENANT_FILE_PATH } from "../tests/utils/paths";
import { doDevLogin } from "./devLogin";
import { doCredentialsLogin } from "./ossLoginHelper";
import {
  registerWelcomeTourAutoDismiss,
  registerTourOverlayGuard,
  readGlobalClusterValue,
} from "../tests/utils/helpers";
import { suppressTourPopups } from "../tests/utils/tourSuppression";

// What a login actually settled on. Empty string means "not established by this
// run", which is how verifySelection() knows to skip that half of the check.
export type LoginSelection = { tenant: string; cluster: string };

// Pages whose login (session + tenant, and optionally cluster) is already
// established. Playwright gives every test a fresh page, so this never matches
// across tests — it exists for a single test that walks several areas of the
// app and therefore calls doFullLogin() more than once, directly or through
// helpers like navigateToDatabaseTab(). Without it, each such call would redo
// the tenant dialog and cluster dropdown mid-journey. Pass { force: true } to
// log in again anyway (tests that exercise the login flow itself, or that need
// a different tenant/cluster).
const establishedLogins = new WeakMap<Page, { clusterSelected: boolean; selection: LoginSelection }>();

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

  // True when a reused storageState session has already landed us inside the
  // app shell, so the LDAP form can be skipped. Races the authenticated signal
  // (account button visible) against the unauthenticated one (redirect to
  // /signin or /api/auth/error) so both outcomes resolve fast — otherwise an
  // unauthenticated start would always eat the full 8s button timeout.
  private async isAuthenticated(): Promise<boolean> {
    await Promise.race([
      this.accountSettingsButton.waitFor({ state: "visible", timeout: 8000 }),
      this.page.waitForURL(
        (url) => url.href.includes("/signin") || url.href.includes("/api/auth/error"),
        { timeout: 8000 },
      ),
    ]).catch(() => {});
    if (this.isSigninPage() || this.isAuthErrorPage()) return false;
    return this.accountSettingsButton.isVisible().catch(() => false);
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
    const enabledDeadline = Date.now() + 20000;
    while (await tenantSelect.isDisabled().catch(() => false)) {
      if (Date.now() > enabledDeadline) break;
      await this.page.waitForTimeout(200);
    }

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

    // SWITCH_TENANT names the tenant to switch into. Only when it is unset does
    // the highest iteration-N get auto-resolved — that convention exists on the
    // test env, and does not on environments whose tenant is named otherwise.
    const pinned = process.env.SWITCH_TENANT?.trim();

    const searchInput = this.page.getByPlaceholder(/search/i).first();
    const isSearchVisible = await searchInput.isVisible().catch(() => false);
    if (isSearchVisible) {
      await searchInput.fill(pinned || "iteration");
      await this.page.waitForTimeout(300);
    }

    // Tolerate an empty list so a missing tenant reports what was actually
    // offered, instead of an opaque locator timeout.
    const optionsAppeared = await this.page
      .locator('[role="option"]')
      .first()
      .waitFor({ state: "visible", timeout: 10000 })
      .then(() => true)
      .catch(() => false);
    const allOptions = optionsAppeared ? await this.page.locator('[role="option"]').allTextContents() : [];

    let tenantName: string | null;
    if (pinned) {
      tenantName = allOptions.map((o) => o.trim()).find((o) => o === pinned) ?? null;
      if (!tenantName) {
        throw new Error(
          `SWITCH_TENANT="${pinned}" is not in the tenant dropdown. Offered: ${allOptions.join(", ") || "(none)"}`,
        );
      }
      console.log(`Using tenant from SWITCH_TENANT: ${tenantName}`);
    } else {
      tenantName = this.resolveHighestIteration(allOptions);
      if (!tenantName) throw new Error("No iteration-N tenant found in tenant dropdown");
      console.log(`Auto-detected highest iteration tenant: ${tenantName}`);
    }

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
    await dialog.waitFor({ state: "hidden", timeout: 15000 });
    this.recordTenant(tenantName);
    console.log(`Switched to tenant: ${tenantName}`);
    await this.page.waitForTimeout(2000);
  }

  // The tenant this suite is required to run in.
  //
  // SWITCH_TENANT first — it is the tenant of record for the run: the value
  // GraphQLNetworkWatcher and SlackReporter label every alert with. Note that
  // switchTenant() does NOT honour it; it always auto-resolves the highest
  // iteration-N, which only exists on the test env. So on any environment whose
  // tenant is named something else (dev is "Nudgebee"), this var is the only
  // correct definition of "required".
  //
  // Otherwise the tenant a previous switch recorded, kept in playwright/.auth/
  // so it survives between runs; see TENANT_FILE_PATH.
  private expectedTenant(): string | null {
    const pinned = process.env.SWITCH_TENANT?.trim();
    if (pinned) return pinned;
    try {
      return readFileSync(TENANT_FILE_PATH, "utf-8").trim() || null;
    } catch {
      return null;
    }
  }

  private recordTenant(tenantName: string): void {
    try {
      mkdirSync(AUTH_STATE_DIR, { recursive: true });
      writeFileSync(TENANT_FILE_PATH, tenantName);
    } catch {
      /* best-effort: the record is an optimisation, never a requirement */
    }
  }

  // The tenant lives in the NextAuth JWT (switching calls session update()), so
  // it rides along in the cookies restored from storageState and one request
  // over the context's cookie jar answers what a multi-step dialog would.
  private async readSession(): Promise<{ tenant?: { name?: string }; hasMultipleTenantAccess?: boolean } | null> {
    return this.page.request
      .get("/api/auth/session")
      .then((res) => (res.ok() ? res.json() : null))
      .catch(() => null);
  }

  // The tenant the session currently holds, or "" when it carries none.
  private async activeTenant(): Promise<string> {
    const name = (await this.readSession())?.tenant?.name;
    return typeof name === "string" ? name.trim() : "";
  }

  /**
   * True when the Switch Tenant dialog can be skipped — either because there is
   * nothing to switch to, or because the session already holds the right tenant.
   *
   * Fail-safe: anything unknown — unreadable session, tenant absent from the
   * payload, no recorded tenant to compare against — returns false, so the
   * switch runs exactly as before.
   */
  private async isTenantAlreadyActive(): Promise<boolean> {
    const session = await this.readSession();

    const current = session?.tenant?.name;
    if (typeof current !== "string" || !current) {
      console.log("Tenant check: session carried no tenant — switching");
      return false;
    }

    // The check that matters: are we already in the tenant this run requires?
    const expected = this.expectedTenant();
    if (expected && current === expected) {
      this.recordTenant(current);
      console.log(`Already in tenant: ${current} — skipping tenant switch`);
      return true;
    }

    // No switch is possible: the app only renders a Switch Tenant menu item for
    // multi-tenant users (UserMenuItems.generateMenuItems gates it on
    // hasMultipleTenantAccess === tenants.length > 1). Without this, switchTenant()
    // spends its entire retry budget clicking for a control that was never
    // rendered, and still fails.
    if (!session?.hasMultipleTenantAccess) {
      if (expected && current !== expected) {
        // Failing here beats both alternatives: letting switchTenant() burn its
        // 180s budget on a control that was never rendered, and silently running
        // the suite against the wrong tenant's data.
        throw new Error(
          `This run requires tenant "${expected}" but the logged-in user's only tenant is "${current}", ` +
            `so no switch is possible. Fix SWITCH_TENANT or use a user with access to "${expected}".`,
        );
      }
      console.log(`Tenant check: user belongs to a single tenant ("${current}") — no switch needed`);
      this.recordTenant(current);
      return true;
    }

    if (!expected) {
      console.log(`Tenant check: no required tenant known (SWITCH_TENANT unset, nothing recorded) — switching`);
      return false;
    }
    console.log(`Tenant check: in "${current}", require "${expected}" — switching`);
    return false;
  }

  // Retries the whole flow from a clean reload; budgetMs stops before the 240s test timeout so the real error survives.
  // force skips the already-in-tenant fast path — used by global-setup, which is
  // what RESOLVES the tenant and so must not trust a previously recorded one.
  async switchTenantWithRetry({
    attempts = 6,
    budgetMs = 180000,
    force = false,
  }: { attempts?: number; budgetMs?: number; force?: boolean } = {}) {
    const deadline = Date.now() + budgetMs;

    for (let attempt = 1; attempt <= attempts; attempt++) {
      // Checked before EVERY attempt, not once by the caller. Two reasons:
      //  - every caller gets it, and no caller can forget it;
      //  - the common failure is the switch landing but a later wait timing out
      //    (dialog-hidden, loader), so on attempt 2+ the session frequently
      //    already holds the target tenant and the retry would redo a completed
      //    switch — or, where no Switch Tenant menu exists at all, burn the whole
      //    180s budget clicking for a control that was never rendered.
      // force bypasses it entirely: global-setup is what RESOLVES the tenant, so
      // it must switch for real rather than trust a previously recorded one.
      if (!force && (await this.isTenantAlreadyActive())) return;

      try {
        await this.switchTenant();
        return;
      } catch (error) {
        console.log(`Switch Tenant failed on attempt ${attempt}/${attempts}: ${error}`);
        if (attempt === attempts) throw error;
        if (Date.now() > deadline) {
          console.log(`Switch Tenant retry budget of ${budgetMs}ms exhausted after ${attempt} attempts`);
          throw error;
        }

        // Best-effort reset: a failed reload must not mask the real error or eat the remaining attempts.
        try {
          await this.page.keyboard.press("Escape");
          await this.page.goto(process.env.BASE_URL || "");
          await this.waitForLoaderToDisappear();
        } catch (resetError) {
          console.log(`Page reset before retry failed: ${resetError}`);
        }
      }
    }
  }

  // Returns the cluster it settled on, so the caller can verify that exact name later.
  async selectHighestIterationCluster(): Promise<string> {
    await this.clearAndTypeCluster("iteration");
    await this.page.locator("[role='option']").first().waitFor({ state: "visible", timeout: 10000 });

    const options = await this.page.locator("[role='option']").allTextContents();
    const clusterName = this.resolveHighestIteration(options);

    if (!clusterName) throw new Error("No iteration-N cluster found in cluster dropdown");
    console.log(`Auto-detected highest iteration cluster: ${clusterName}`);
    await this.selectCluster(clusterName);
    return clusterName;
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

  // Returns what this login settled on, so global-setup can verify it before caching.
  async doFullLogin(options: { selectCluster?: boolean; force?: boolean } = {}): Promise<LoginSelection> {
    const { selectCluster = true, force = false } = options;

    // Already logged in on this page: nothing to do, unless the caller now needs
    // a cluster that an earlier selectCluster:false call skipped.
    const established = force ? undefined : establishedLogins.get(this.page);
    if (established) {
      if (!selectCluster || established.clusterSelected) {
        console.log("Session already established on this page — skipping login");
        return established.selection;
      }
      const selection = { ...established.selection, cluster: await this.selectConfiguredCluster() };
      establishedLogins.set(this.page, { clusterSelected: true, selection });
      return selection;
    }

    // storageState already carries the tour flags; re-seeded here for specs that build their own context.
    await suppressTourPopups(this.page);
    await registerWelcomeTourAutoDismiss(this.page);
    await registerTourOverlayGuard(this.page);

    // Dev signs in through its own LDAP flow, but the cluster is selected by the
    // shared path below — doDevLogin used to drive the dropdown itself, which
    // meant dev got neither the skip-when-already-selected fast path nor the
    // read-back check. Tenant handling on dev is unchanged: no switch happens.
    if (process.env.E2E_ENVIRONMENT === "dev") {
      await doDevLogin(this.page);
      const selection = { tenant: "", cluster: selectCluster ? await this.selectConfiguredCluster() : "" };
      establishedLogins.set(this.page, { clusterSelected: selectCluster, selection });
      return selection;
    }

    // OSS-ONLY BRANCH — do not drop when syncing this file from EE. OSS has no
    // LDAP: it signs in through the credentials ("Admin login") form with
    // E2E_EMAIL/E2E_PASSWORD, which is all the .env.oss secret carries. Without
    // this branch, oss falls through to the LDAP path below and global-setup
    // dies on "LDAP_USERNAME or LDAP_PASSWORD missing" before a single test runs.
    //
    // Single-tenant, so no switchTenantWithRetry and an empty tenant in the
    // returned selection — verifySelection() reads that as "not established by
    // this run" and skips the tenant half of its check. The cluster goes through
    // the shared selectConfiguredCluster() for the same reason dev does: it gets
    // the skip-when-already-selected fast path and the read-back check.
    if (process.env.E2E_ENVIRONMENT === "oss") {
      await doCredentialsLogin(this.page);
      await this.waitForLoaderToDisappear();
      const selection = { tenant: "", cluster: selectCluster ? await this.selectConfiguredCluster() : "" };
      establishedLogins.set(this.page, { clusterSelected: selectCluster, selection });
      return selection;
    }

    const username = process.env.LDAP_USERNAME || "";
    const password = process.env.LDAP_PASSWORD || "";

    if (!username || !password) {
      throw new Error("LDAP_USERNAME or LDAP_PASSWORD missing");
    }

    await this.navigate();
    await this.waitForLoaderToDisappear();

    // Fast path: with a reused storageState session the app already loaded
    // authenticated, so skip the LDAP form. Only type credentials when the
    // session is missing/expired (fresh global-setup run or stale state file).
    if (!(await this.isAuthenticated())) {
      await this.login(username, password);
      await this.waitForLoaderToDisappear();

      if (this.isAuthErrorPage()) {
        await this.page.goto(process.env.BASE_URL || "");
        await this.login(username, password);
      } else if (this.isSigninPage()) {
        await this.login(username, password);
      }

      await this.page.waitForURL(`${process.env.BASE_URL}/**`, { timeout: 30000 });
    }

    // Self-guarding: returns immediately when already in the required tenant.
    await this.switchTenantWithRetry({ force });
    await this.waitForLoaderToDisappear();

    const selection: LoginSelection = {
      tenant: await this.activeTenant(),
      cluster: selectCluster ? await this.selectConfiguredCluster() : "",
    };

    establishedLogins.set(this.page, { clusterSelected: selectCluster, selection });
    return selection;
  }

  // Selects the cluster named by CLUSTER_NAME/CLUSTER, or the highest iteration-N
  // cluster when neither is set, and returns the name it settled on.
  //
  // Skipped when the dropdown already holds the target: ClusterDropDown persists
  // the choice to localStorage (`nudgebee.userPreferences.last_account`) and
  // restores it on mount, so the selection global-setup made is captured in
  // storageState and restored for every test — the dropdown does not need
  // driving again. Only an explicitly named cluster can be checked this way;
  // without one, the target is not known until the options are read.
  private async selectConfiguredCluster(): Promise<string> {
    const explicitCluster = process.env.CLUSTER_NAME || process.env.CLUSTER;

    if (!explicitCluster) {
      const resolved = await this.selectHighestIterationCluster();
      await this.waitForLoaderToDisappear();
      return resolved;
    }

    if ((await readGlobalClusterValue(this.page, 10000, explicitCluster)) === explicitCluster) {
      console.log(`Cluster check: already on "${explicitCluster}" — skipping cluster selection`);
      return explicitCluster;
    }

    await this.selectCluster(explicitCluster);
    await this.waitForLoaderToDisappear();

    // Read back rather than trusting the click. A dropdown that committed a
    // different account would otherwise let the whole suite run green against
    // the wrong cluster's data — the failure this check exists to prevent.
    const committed = await readGlobalClusterValue(this.page, 30000, explicitCluster);
    if (committed !== explicitCluster) {
      throw new Error(`Cluster selection did not commit: expected "${explicitCluster}", dropdown shows "${committed}"`);
    }
    return explicitCluster;
  }

  /**
   * Fails unless a freshly reloaded page is genuinely on `selection`.
   *
   * doFullLogin() already reads the dropdown back, but that only proves the LIVE
   * page is correct — not that the choice landed in the state about to be saved.
   * The cluster survives via localStorage (`last_account`) and the tenant via the
   * session cookie; if either fails to persist, every test restores a session on
   * the wrong cluster/tenant and the suite runs green against the wrong data,
   * because no spec asserts which one is active. This reload is the only step
   * that exercises the restore path the tests will actually take.
   *
   * Throws, rather than warns, so global-setup never caches an unverified state.
   */
  async verifySelection(selection: LoginSelection): Promise<void> {
    await this.navigate();
    await this.waitForLoaderToDisappear();

    if (selection.tenant) {
      const active = await this.activeTenant();
      if (active !== selection.tenant) {
        throw new Error(
          `Tenant did not persist: session is in "${active || "(none)"}", expected "${selection.tenant}"`,
        );
      }
      console.log(`Verified tenant after reload: ${selection.tenant}`);
    }

    if (selection.cluster) {
      const active = await readGlobalClusterValue(this.page, 30000, selection.cluster);
      if (active !== selection.cluster) {
        throw new Error(
          `Cluster did not persist: dropdown shows "${active || "(empty)"}", expected "${selection.cluster}"`,
        );
      }
      console.log(`Verified cluster after reload: ${selection.cluster}`);
    }
  }

  async waitForLoaderToDisappear() {
    const loader = this.page.getByAltText("Loading...");
    await loader.waitFor({ state: "hidden", timeout: 180000 }).catch(() => {});
  }
}
