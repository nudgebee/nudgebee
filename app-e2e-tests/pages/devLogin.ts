import { Page } from "@playwright/test";

export async function doDevLogin(page: Page): Promise<void> {
  const baseUrl     = process.env.BASE_URL;
  const username    = process.env.LDAP_USERNAME || "";
  const password    = process.env.LDAP_PASSWORD || "";
  const clusterName = process.env.CLUSTER_NAME  || process.env.CLUSTER || "";

  if (!baseUrl)               throw new Error("BASE_URL is not set in environment");
  if (!username || !password) throw new Error("LDAP_USERNAME or LDAP_PASSWORD is not set");

  await page.goto(baseUrl);
  await page
    .waitForURL(url => url.href.includes("/home") || url.href.includes("/signin"), { timeout: 8000 })
    .catch(() => {});

  const alreadyLoggedIn = page.url().includes("/home") || page.url().includes("/workflow");

  if (!alreadyLoggedIn) {
    try {
      await ldapLogin(page, username, password);
    } catch (e) {
      if (!page.url().includes("/home") && !page.url().includes("/workflow")) throw e;
    }

    await page
      .waitForURL(url => !url.href.includes("/signin"), { timeout: 15000 })
      .catch(() => {});

    await page.getByAltText("Loading...").waitFor({ state: "visible", timeout: 2000 }).catch(() => {});
    await waitForLoaderToDisappear(page);

    const urlAfterLogin = page.url();
    if (urlAfterLogin.includes("/api/auth/error")) {
      await page.goto(baseUrl);
      await ldapLogin(page, username, password);
    } else if (urlAfterLogin.includes("/signin")) {
      try {
        await ldapLogin(page, username, password);
      } catch (e) {
        if (!page.url().includes("/home") && !page.url().includes("/workflow")) throw e;
      }
    }
  }

  await page.waitForURL(/\/(home|workflow)/, { timeout: 50000 });
  await waitForLoaderToDisappear(page);

  if (clusterName) {
    await selectCluster(page, clusterName);
    await waitForLoaderToDisappear(page);
  }
}

async function ldapLogin(page: Page, username: string, password: string): Promise<void> {
  const usernameInput = page.getByRole("textbox", { name: "LDAP Username" });
  const passwordInput = page.getByRole("textbox", { name: "LDAP Password" });
  const submitButton  =
    page.getByRole("button", { name: /^sign in$/i })
      .or(page.getByRole("button", { name: /^submit$/i }))
      .first();

  const formAlreadyVisible = await usernameInput.waitFor({ state: "visible", timeout: 3000 }).then(() => true).catch(() => false);
  if (!formAlreadyVisible) {
    const ldapBtn = page
      .locator("button, a, div[role='button'], div[tabindex]")
      .filter({ hasText: /login via ldap/i })
      .first();
    await ldapBtn.waitFor({ state: "visible", timeout: 15000 });
    await ldapBtn.click();
    await page.waitForLoadState("domcontentloaded", { timeout: 10000 }).catch(() => {});
    await page.waitForTimeout(500);
  }

  await usernameInput.waitFor({ state: "visible", timeout: 15000 });
  await passwordInput.waitFor({ state: "visible", timeout: 10000 });

  await usernameInput.click();
  await usernameInput.fill("");
  await usernameInput.pressSequentially(username, { delay: 20 });

  await passwordInput.click();
  await passwordInput.fill("");
  await passwordInput.pressSequentially(password, { delay: 20 });

  await submitButton.click();
  await page
    .waitForURL(url => !url.href.includes("/signin"), { timeout: 15000 })
    .catch(() => page.waitForTimeout(3000));
}

async function selectCluster(page: Page, clusterName: string): Promise<void> {
  const clusterInput = page.locator("#auto-complete-global-cluster");
  await clusterInput.waitFor({ state: "visible", timeout: 30000 });

  const clearAndType = async () => {
    await clusterInput.fill("");
    await page.waitForTimeout(400);
    await clusterInput.pressSequentially(clusterName, { delay: 50 });
  };

  const option = page.locator("[role='option']").filter({ hasText: clusterName }).first();

  for (let attempt = 1; attempt <= 5; attempt++) {
    if (attempt > 1) await page.waitForTimeout(3000);
    await clearAndType();
    await page.waitForTimeout(1000);
    const isVisible = await option.isVisible().catch(() => false);
    if (isVisible) break;
    console.log(`Cluster option '${clusterName}' not found (attempt ${attempt}/5), retrying...`);
  }

  await option.waitFor({ state: "visible", timeout: 30000 });
  await option.click();
  await page.mouse.move(0, 0);
  console.log(`Selected cluster: ${clusterName}`);
}

async function waitForLoaderToDisappear(page: Page): Promise<void> {
  await page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 180000 });
}
