import { test, expect } from '@playwright/test';
import { LoginPage } from '../../pages/LoginPage';

test.describe('Authentication', () => {
  test('should login with LDAP credentials and reach home page', async ({ page }) => {
    const loginPage = new LoginPage(page);
    await loginPage.doFullLogin();

    // Verify we reached the home page after full login
    await expect(page).toHaveURL(/.*\/home/, { timeout: 15000 });
  });

  test('should not remain on signin page after valid credentials', async ({ page }) => {
    const loginPage = new LoginPage(page);
    await loginPage.doFullLogin();

    // Verify we're no longer on the sign-in page
    await expect(page).not.toHaveURL(/.*\/signin/, { timeout: 15000 });
  });
});
