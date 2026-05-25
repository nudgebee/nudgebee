import { test } from "@playwright/test";
import { LoginPage } from "../../pages/LoginPage";
import { questions } from "./questionsData";
import { NubiLocators } from "./nubiLocators";

test("Login once and ask all questions", async ({ page }) => {
  test.setTimeout(300000);
  const loginPage = new LoginPage(page);
  const locators = new NubiLocators(page);

  await loginPage.doFullLogin();
  await locators.askNudgebeeBtn.click();

  for (const [index, q] of questions.entries()) {
    console.log(`[${index + 1}/${questions.length}] Asking: ${q}`);

    await test.step(`Question ${index + 1}: ${q}`, async () => {
      await locators.newChatBtn.click();
      const textbox = locators.chatTextbox;
      await textbox.fill(q);
      await locators.submitBtn.click();
      await page.waitForTimeout(3000);
    });
  }

  console.log("All questions completed! Closing browser...");
});
