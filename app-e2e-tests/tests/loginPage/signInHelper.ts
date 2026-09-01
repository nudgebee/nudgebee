// Not for OSS
import { Page, expect } from "@playwright/test";
import { SignInLocators } from "./signInLocators";

// Paths are relative so they resolve against `use.baseURL` (playwright.config.ts:47),
// which is BASE_URL. Nothing here may hardcode an environment.
export const SIGNIN_PATH = "/signin";
export const NO_TENANT_ACCESS_PATH = "/no-tenant-access";

// A username no LDAP directory on any environment can hold, so the rejection under
// test is the directory saying "unknown", never a real account being probed or locked.
export function unknownLdapUsername(): string {
  return `zz-no-such-user-${Date.now()}`;
}

export async function openSignInPage(page: Page): Promise<SignInLocators> {
  const signIn = new SignInLocators(page);
  await page.goto(SIGNIN_PATH);
  await expect(signIn.welcomeHeading).toBeVisible();
  return signIn;
}

// The option buttons only swap React state; on a slow first paint the click can land
// before the handler is attached and the view never changes, which is why the click is
// retried at all — pages/LoginPage.ts retries it for the same reason.
//
// `settled` is checked BEFORE the click, and that ordering is load-bearing. Choosing a
// view unmounts the option list (signin.tsx:858-861), so once the click has worked the
// button is gone: a retry that clicked first would wait out `actionTimeout` (10s on
// dev) on an element that can never appear, and the loop could never recover even
// though the view had opened. Checking first makes a slow transition cost one interval
// instead of failing the test.
//
// `settled` uses isVisible(), which answers immediately rather than waiting — the
// waiting is toPass's job, and a blocking probe here would stall the pre-click check.
async function openView(settled: () => Promise<boolean>, open: () => Promise<void>) {
  await expect(async () => {
    if (await settled()) return;
    await open();
    expect(await settled()).toBe(true);
  }).toPass({ timeout: 45000, intervals: [250, 500, 1000, 2000] });
}

export async function openLdapForm(signIn: SignInLocators) {
  await openView(
    async () => (await signIn.ldapUsername.isVisible()) && (await signIn.ldapPassword.isVisible()),
    async () => await signIn.ldapOption.click(),
  );
}

export async function openMagicLinkForm(signIn: SignInLocators) {
  await openView(
    async () => await signIn.magicEmail.isVisible(),
    async () => await signIn.magicLinkOption.click(),
  );
}

// Fill through the value assertion rather than trusting fill(): ds/Input is a
// controlled component, so the DOM value only settles once React has re-rendered.
export async function fillLdapCredentials(signIn: SignInLocators, username: string, password: string) {
  await signIn.ldapUsername.fill(username);
  await expect(signIn.ldapUsername).toHaveValue(username);
  await signIn.ldapPassword.fill(password);
  await expect(signIn.ldapPassword).toHaveValue(password);
}
