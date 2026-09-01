// Not for OSS
import { test, expect } from "@playwright/test";
import { SignInLocators } from "./signInLocators";
import {
  NO_TENANT_ACCESS_PATH,
  SIGNIN_PATH,
  fillLdapCredentials,
  openLdapForm,
  openMagicLinkForm,
  openSignInPage,
  unknownLdapUsername,
} from "./signInHelper";

// The sign-in surface itself — app/src/pages/signin.tsx and its NO_TENANT_ACCESS
// redirect target app/src/pages/no-tenant-access.tsx.
//
// Everything here is read-only against a shared dev tenant. The two writes this page
// can perform are deliberately not driven: a valid LDAP login is already covered by
// LdapLogin.spec.ts, and requesting a real magic link sends a real email, so the magic
// link case stops at the client-side email check in handleMagicLink (signin.tsx:214),
// which rejects before any request is made. The one credential submission below uses a
// generated username no directory holds, so no real account is probed or locked out.
//
// These tests exercise the logged-out page, so the shared storageState from
// global-setup has to be dropped — same override LdapLogin.spec.ts uses.
test.use({ storageState: { cookies: [], origins: [] } });

// File-level budget, following tests/VM/VM.spec.ts. Generous because a cold signin
// page on dev pays for a full SSR render plus the branding fetch.
test.describe.configure({ timeout: 90000 });

test(
  "Sign in sanity - open the sign in page, verify the welcome heading and the LDAP and Magic Link options render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);

    await expect(signIn.welcomeHeading).toBeVisible();
    await expect(signIn.ldapOption).toBeVisible();
    await expect(signIn.magicLinkOption).toBeVisible();
    // The three form views are state-gated (signin.tsx:858-861), so none of their
    // fields may exist until an option is chosen.
    await expect(signIn.ldapUsername).toHaveCount(0);
    await expect(signIn.magicEmail).toHaveCount(0);
  },
);

test(
  "Sign in sanity - open the sign in page, verify the browser tab title ends in Login",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    await openSignInPage(page);

    // Head renders `${brandTitle || 'Nudgebee'}: Login` (signin.tsx:840). The prefix is
    // whatever tenant branding resolves to, so only the suffix is a fixed contract.
    await expect(page).toHaveTitle(/: Login$/);
  },
);

test(
  "Sign in - open the sign in page, choose Login via LDAP, verify the LDAP heading, the username and password fields and the Sign in button render",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await expect(signIn.ldapHeading).toBeVisible();
    await expect(signIn.ldapUsername).toBeVisible();
    await expect(signIn.ldapPassword).toBeVisible();
    await expect(signIn.signInSubmit).toBeVisible();
    await expect(signIn.backToOptions).toBeVisible();
    // Choosing a view replaces the option list rather than adding to it.
    await expect(signIn.ldapOption).toHaveCount(0);
  },
);

test(
  "Sign in - open the LDAP form, submit with both fields empty, verify the LDAP username required and LDAP password required errors",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await signIn.signInSubmit.click();

    await expect(signIn.ldapUsernameError).toHaveText("LDAP username is required.");
    await expect(signIn.ldapPasswordError).toHaveText("LDAP password is required.");
    // handleLdapSubmit returns before calling signIn() when either field is blank
    // (signin.tsx:198), so nothing may have navigated.
    await expect(page).toHaveURL(new RegExp(`${SIGNIN_PATH}$`));
  },
);

test(
  "Sign in - open the LDAP form, enter a username only, submit, verify only the LDAP password required error is raised",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await signIn.ldapUsername.fill(unknownLdapUsername());
    await signIn.signInSubmit.click();

    await expect(signIn.ldapPasswordError).toHaveText("LDAP password is required.");
    await expect(signIn.ldapUsernameError).toHaveCount(0);
  },
);

test(
  "Sign in - open the LDAP form, submit empty to raise the username error, type a username, verify the LDAP username required error clears",
  { tag: ["@dev", "@regression", "@validation"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await signIn.signInSubmit.click();
    await expect(signIn.ldapUsernameError).toHaveText("LDAP username is required.");

    await signIn.ldapUsername.fill(unknownLdapUsername());

    await expect(signIn.ldapUsernameError).toHaveCount(0);
    // The password error is cleared only by editing the password, so it must survive.
    await expect(signIn.ldapPasswordError).toHaveText("LDAP password is required.");
  },
);

test(
  "Sign in - open the LDAP form, enter an unknown username and password, submit, verify the invalid credentials error and that the browser stays on the sign in page",
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await fillLdapCredentials(signIn, unknownLdapUsername(), "not-a-real-password");
    await signIn.signInSubmit.click();

    // handleLdapSubmit reports a rejected directory lookup on the password field
    // (signin.tsx:205). This is a real round trip to the LDAP provider, so it gets a
    // longer budget than the client-side checks above.
    await expect(signIn.ldapPasswordError).toHaveText("Invalid credentials. Please try again.", { timeout: 45000 });
    await expect(page).toHaveURL(new RegExp(`${SIGNIN_PATH}$`));
  },
);

test(
  "Sign in - open the LDAP form, type a password, verify the field masks the value",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await signIn.ldapPassword.fill("not-a-real-password");

    await expect(signIn.ldapPassword).toHaveAttribute("type", "password");
    await expect(signIn.ldapPassword).toHaveValue("not-a-real-password");
    await expect(signIn.ldapUsername).toHaveAttribute("type", "text");
  },
);

test(
  "Sign in - open the LDAP form, go back to sign in options, verify the LDAP fields close and the LDAP and Magic Link options return",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    await signIn.backToOptions.click();

    await expect(signIn.ldapUsername).toHaveCount(0);
    await expect(signIn.ldapPassword).toHaveCount(0);
    await expect(signIn.welcomeHeading).toBeVisible();
    await expect(signIn.ldapOption).toBeVisible();
    await expect(signIn.magicLinkOption).toBeVisible();
  },
);

test(
  "Sign in - open the LDAP form, enter a username and submit to raise the password error, go back and reopen the form, verify the error is cleared and the username is retained",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openLdapForm(signIn);

    const username = unknownLdapUsername();
    await signIn.ldapUsername.fill(username);
    await signIn.signInSubmit.click();
    await expect(signIn.ldapPasswordError).toHaveText("LDAP password is required.");

    await signIn.backToOptions.click();
    await expect(signIn.welcomeHeading).toBeVisible();
    await openLdapForm(signIn);

    // handleBack resets helperText but not the field state (signin.tsx:159-169), so
    // going back and forward must lose the error and keep what was typed.
    await expect(signIn.ldapPasswordError).toHaveCount(0);
    await expect(signIn.ldapUsername).toHaveValue(username);
  },
);

test(
  "Sign in - open the Magic Link form, enter an address with no domain, request the link, verify the invalid email address error",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const signIn = await openSignInPage(page);
    await openMagicLinkForm(signIn);

    await expect(signIn.magicLinkHeading).toBeVisible();
    await signIn.magicEmail.fill("not-an-email");
    await signIn.magicLinkSubmit.click();

    // EmailRegEx (app/src/lib/validation.ts:4) rejects before handleMagicLink calls
    // signIn(), so no link is ever requested and no mail is sent.
    await expect(signIn.magicEmailError).toHaveText("Please enter a valid email address.");
    await expect(page).toHaveURL(new RegExp(`${SIGNIN_PATH}$`));
  },
);

test(
  "Sign in - open the sign in page with a NO_TENANT_ACCESS error, verify it redirects to the access denied page naming the missing tenant",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const signIn = new SignInLocators(page);

    // Not openSignInPage(): this URL redirects straight back out again, so the
    // welcome heading it waits for is never guaranteed to paint.
    await page.goto(`${SIGNIN_PATH}?error=NO_TENANT_ACCESS`);

    await expect(page).toHaveURL(new RegExp(NO_TENANT_ACCESS_PATH));
    await expect(signIn.accessDeniedCode).toBeVisible();
    await expect(signIn.accessDeniedHeading).toBeVisible();
    await expect(signIn.accessDeniedMessage).toBeVisible();
  },
);

test(
  "Access Denied sanity - open the access denied page directly, choose Try Again, verify it returns to the sign in page",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const signIn = new SignInLocators(page);
    await page.goto(NO_TENANT_ACCESS_PATH);

    // Opened with no `message` param, so the page shows its own default copy.
    await expect(signIn.accessDeniedDefaultMessage).toBeVisible();
    await expect(signIn.tryAgainBtn).toBeVisible();

    await signIn.tryAgainBtn.click();

    await expect(page).toHaveURL(new RegExp(`${SIGNIN_PATH}$`));
    await expect(signIn.welcomeHeading).toBeVisible();
  },
);
