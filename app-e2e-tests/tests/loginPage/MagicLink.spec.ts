import { test, expect } from '@playwright/test';
import { LoginPageLocators } from './LoginpageLocators';
import { users } from '../admin/Users/usersConstants';

test('Magic link testing', async ({ page }) => {
  const locators = new LoginPageLocators(page);
  const email = users[0].email;

  const SIGNIN_URL = process.env.BASE_URL ? `${process.env.BASE_URL}/signin` : '';
  const ERROR_URL_PART = '/api/auth/error';
  const SUCCESS_URL_PART ='/auth/verify-request';

  await page.goto(SIGNIN_URL);
  await expect(page.getByText('Welcome back!')).toBeVisible();

  // Wait for providers to load and magic link input to render
  await locators.magicLinkInputField.waitFor({ state: "visible", timeout: 30000 });

  const maxRetries = 3;
  let attempt = 0;

  while (attempt < maxRetries) {
    attempt++;

    await locators.magicLinkInputField.fill(email);
    await locators.sendMagicLinkButton.click();
    await page.waitForTimeout(5000);

    const currentUrl = page.url();

    if (currentUrl.includes(SUCCESS_URL_PART)) {
      await expect(page.url()).toContain(SUCCESS_URL_PART);
      await expect(
        locators.magicLinkLoginSuccessMessage
      ).toBeVisible();

      break;
    }

    if (currentUrl.includes(ERROR_URL_PART)) {
      console.log(`Retrying magic link flow, attempt: ${attempt}`);
      await page.goto(SIGNIN_URL);
      await expect(page.getByText('Welcome back!')).toBeVisible();
    }
  }
  await expect(page.url()).toContain(SUCCESS_URL_PART);
});
