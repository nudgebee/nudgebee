import { test } from '@playwright/test';
import { LoginPage } from '../pages/LoginPage';

test('Record steps after login', async ({ page }) => {
  test.setTimeout(0); 
  const loginPage = new LoginPage(page);
  await loginPage.doFullLogin(); 
  console.log('Login complete. Browser is paused for recording...');
  await page.pause(); 
});