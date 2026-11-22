import { Page } from "@playwright/test";

// Sign-in lands on /ask-nudgebee now; /home was the landing page before that change
// deployed, and this helper runs against whatever build is live, so accept both.
const LANDING_PATHS = ["/ask-nudgebee", "/home", "/workflow"];
const isLanded = (href: string): boolean => LANDING_PATHS.some((p) => href.includes(p));

// LDAP sign-in only; tenant + cluster are owned by LoginPage.doFullLogin() for every env.
export async function doDevLogin(page: Page): Promise<void> {
  const baseUrl     = process.env.BASE_URL;
  const username    = process.env.LDAP_USERNAME || "";
  const password    = process.env.LDAP_PASSWORD || "";

  if (!baseUrl)               throw new Error("BASE_URL is not set in environment");
  if (!username || !password) throw new Error("LDAP_USERNAME or LDAP_PASSWORD is not set");

  await page.goto(baseUrl);
  await page
    .waitForURL(url => isLanded(url.href) || url.href.includes("/signin"), { timeout: 8000 })
    .catch(() => {});

  const alreadyLoggedIn = isLanded(page.url());

  if (!alreadyLoggedIn) {
    try {
      await ldapLogin(page, username, password);
    } catch (e) {
      if (!isLanded(page.url())) throw e;
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
        if (!isLanded(page.url())) throw e;
      }
    }
  }

  await page.waitForURL(url => isLanded(url.href), { timeout: 50000 });
  await waitForLoaderToDisappear(page);
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

async function waitForLoaderToDisappear(page: Page): Promise<void> {
  await page.getByAltText("Loading...").waitFor({ state: "hidden", timeout: 180000 });
}
