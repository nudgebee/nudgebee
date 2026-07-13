import { Page } from "@playwright/test";

export async function doCredentialsLogin(page: Page): Promise<void> {
  const baseUrl = process.env.BASE_URL;
  const email = process.env.E2E_EMAIL || "";
  const password = process.env.E2E_PASSWORD || "";

  if (!baseUrl) throw new Error("BASE_URL is not set in environment");
  if (!email || !password)
    throw new Error("E2E_EMAIL or E2E_PASSWORD is not set");

  await page.goto(baseUrl);
  await page
    .waitForURL(
      (url) => url.href.includes("/home") || url.href.includes("/signin"),
      { timeout: 15000 }
    )
    .catch(() => {});

  const alreadyLoggedIn =
    page.url().includes("/home") || page.url().includes("/workflow");

  if (!alreadyLoggedIn) {
    let loginSuccess = false;
    for (let attempt = 1; attempt <= 3; attempt++) {
      console.log(`Credentials login attempt ${attempt}/3`);
      await credentialsLogin(page, email, password);

      const redirected = await page
        .waitForURL((url) => !url.href.includes("/signin"), { timeout: 15000 })
        .then(() => true)
        .catch(() => false);

      if (redirected && !page.url().includes("/api/auth/error")) {
        console.log(`Login succeeded on attempt ${attempt}`);
        loginSuccess = true;
        break;
      }

      if (page.url().includes("/api/auth/error")) {
        console.log(`Auth error page on attempt ${attempt} — retrying`);
        await page.goto(baseUrl);
      } else {
        console.log(`Still on signin page after attempt ${attempt} — retrying`);
      }
    }

    if (!loginSuccess) {
      throw new Error("Login failed after 3 attempts");
    }
  }

  await page.waitForURL(/\/(home|workflow)/, { timeout: 50000 });
}

async function credentialsLogin(
  page: Page,
  email: string,
  password: string
): Promise<void> {
  const adminLoginBtn = page
    .locator("button, a, div[role='button'], div[tabindex]")
    .filter({ hasText: /admin login/i })
    .first();

  const adminBtnVisible = await adminLoginBtn
    .waitFor({ state: "visible", timeout: 10000 })
    .then(() => true)
    .catch(() => false);

  if (adminBtnVisible) {
    await adminLoginBtn.click();
    await page.waitForTimeout(500);
  }

  const emailInput = page.locator("#credsEmail input, input#credsEmail");
  const passwordInput = page.locator("#credsPassword input, input#credsPassword");
  const submitButton = page
    .getByRole("button", { name: /^sign in$/i })
    .first();

  await emailInput.waitFor({ state: "visible", timeout: 15000 });
  await passwordInput.waitFor({ state: "visible", timeout: 10000 });

  await emailInput.click();
  await emailInput.fill("");
  await emailInput.fill(email);
  await page.waitForTimeout(200);

  const filledEmail = await emailInput.inputValue();
  if (filledEmail !== email) {
    console.log(`Email mismatch: got "${filledEmail}", expected "${email}" — refilling`);
    await emailInput.fill("");
    await emailInput.pressSequentially(email, { delay: 30 });
  }

  await passwordInput.click();
  await passwordInput.fill("");
  await passwordInput.fill(password);
  await page.waitForTimeout(200);

  const filledPassword = await passwordInput.inputValue();
  if (filledPassword !== password) {
    console.log(`Password mismatch: got length ${filledPassword.length}, expected ${password.length} — refilling`);
    await passwordInput.fill("");
    await passwordInput.pressSequentially(password, { delay: 30 });
  }

  const maskedEmail = email.replace(/^(.)[^@]*(@.*)$/, "$1***$2");
  console.log(`Submitting login — email: ${maskedEmail}, password length: ${password.length}`);
  await submitButton.click();
}
