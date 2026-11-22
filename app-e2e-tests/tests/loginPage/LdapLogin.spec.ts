import { test, expect } from '@playwright/test';
import { LoginPage } from '../../pages/LoginPage';

// This suite exercises the LDAP login flow itself, so it must start
// unauthenticated — override the shared storageState with an empty session.
test.use({ storageState: { cookies: [], origins: [] } });

test.describe('Authentication', () => {
  test('should login with LDAP credentials and reach the default landing page', { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] }, async ({ page }) => {
    const loginPage = new LoginPage(page);
    await loginPage.doFullLogin();

    // The default landing page: Nubi (/ask-nudgebee) on builds with Nubi in the left nav,
    // /home before that. The suite runs against whatever build an env has, so accept both.
    await expect(page).toHaveURL(/.*\/(ask-nudgebee|home)/, { timeout: 15000 });
  });

  test('should not remain on signin page after valid credentials', { tag: ["@dev", "@test", "@oss", "@smoke", "@functional"] }, async ({ page }) => {
    const loginPage = new LoginPage(page);
    await loginPage.doFullLogin();

    // Verify we're no longer on the sign-in page
    await expect(page).not.toHaveURL(/.*\/signin/, { timeout: 15000 });
  });
});
