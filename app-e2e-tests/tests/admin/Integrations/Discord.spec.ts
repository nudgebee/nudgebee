import { test, expect, Page } from "@playwright/test";
import { IntegrationLocators } from "./IntegrationLocators";
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import { navigateToMessagingTab } from "./util";

// One Discord bot per tenant, so the order matters: delete the bot, try the bad inputs while nothing is connected, install it, then work on a connected bot.
const missingEnv = ["DISCORD_BOT_TOKEN"].filter((key) => !process.env[key]);
const WRONG_TOKEN = "e2e-not-a-real-discord-bot-token";
const MOCK_INVITE_URL = "/e2e-mock-discord-invite";
const INSTALL_PATH = "/api/integrations/discord/install";
// A run posts only into its own environment's channel: test -> test-default, oss -> oss-default. DISCORD_CHANNEL_NAME overrides it.
const CHANNEL_NAME = process.env.DISCORD_CHANNEL_NAME || `${process.env.E2E_ENVIRONMENT || "test"}-default`;
const CHANNEL_LABEL = new RegExp(`/\\s*${CHANNEL_NAME.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);

async function openDiscord(page: Page, beforeOpen?: (page: Page) => Promise<void>): Promise<IntegrationLocators> {
  const locators = await navigateToMessagingTab(page);
  // A mock must be in place before the tile loads, because the tile fetches its channel list on mount.
  if (beforeOpen) await beforeOpen(page);
  await locators.discordBtn.click();
  await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
  return locators;
}

// The Test Notification button is on the page before the platform list loads, so it proves nothing. Wait for the list's own outcome: the connect prompt, or the disabled Connected button.
async function isDiscordConnected(locators: IntegrationLocators): Promise<boolean> {
  await expect(locators.discordNotConnected.or(locators.discordConnectedBtn).first()).toBeVisible({ timeout: 30000 });
  return locators.discordConnectedBtn.isVisible();
}

async function mockInstallWithBotInNoServer(page: Page): Promise<void> {
  await page.route("**/api/integrations/discord/install", (route) =>
    route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ bot_username: "e2e-mock-bot", guild_count: 0, invite_url: MOCK_INVITE_URL }),
    }),
  );
}

async function mockChannelListWithBotInNoServer(page: Page): Promise<void> {
  await page.route("**/api/graphql", async (route) => {
    if (!(route.request().postData() ?? "").includes("GetChannelList")) {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        data: { notifications_list_channels: { data: [], hint: "bot_not_in_any_server", invite_url: MOCK_INVITE_URL } },
      }),
    });
  });
}

// When this run created the state itself (it deleted the bot, or installed it), a surprise is a real failure and must be red; when it did not, the tenant's own state is respected and the test skips.
function expectOrSkip(stateIsKnown: boolean, ok: boolean, message: string): void {
  if (stateIsKnown) {
    expect(ok, message).toBe(true);
  } else {
    test.skip(!ok, message);
  }
}

async function openRowMenuItem(locators: IntegrationLocators, item: "edit" | "update" | "delete"): Promise<void> {
  const menuItem = { edit: locators.discordEditMenuItem, update: locators.discordUpdateTokenMenuItem, delete: locators.discordDeleteMenuItem }[item];
  await locators.discordRowMenuBtn.click();
  // MUI menu items do not accept a plain click in this app (the existing row-menu helper in util.ts does the same), so confirm the item is shown, then fire the click.
  await expect(menuItem).toBeVisible({ timeout: 10000 });
  await menuItem.dispatchEvent("click");
}

test.describe.serial("Discord Integration", () => {
  // Set once the install test has seen the bot connected after a reload; later tests then know the bot exists.
  let installedByThisRun = false;
  // With a token the delete step always runs, so a bot is never expected to be there when the bad-input tests start.
  const startsWithoutBot = missingEnv.length === 0;

  test(
    "Discord - open the Discord tile, verify it lists the platform and shows either the connect prompt or Connected",
    { tag: ["@test", "@oss", "@smoke", "@functional", "@discord"] },
    async ({ page }, testInfo) => {
      test.setTimeout(120000);

      const locators = await navigateToMessagingTab(page);

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.discordBtn.click();
          await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
        },
        {
          testName: testInfo.title,
          operationNames: ["ListMessagingPlatforms"],
        },
      );

      const connected = await isDiscordConnected(locators);
      await expect(locators.addToDiscordBtn).toBeVisible({ timeout: 10000 });
      if (connected) {
        await expect(locators.addToDiscordBtn).toBeDisabled();
      } else {
        await expect(locators.addToDiscordBtn).toBeEnabled();
      }
    },
  );

  test(
    "Discord - delete the connected bot from the row menu, verify the tile goes back to the connect prompt",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }, testInfo) => {
      test.setTimeout(180000);
      test.skip(missingEnv.length > 0, `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`);

      const locators = await openDiscord(page);
      test.skip(!(await isDiscordConnected(locators)), "Discord is not connected — nothing to delete");

      await openRowMenuItem(locators, "delete");

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.discordDeleteConfirmBtn.click();
          await expect(locators.discordNotConnected).toBeVisible({ timeout: 30000 });
        },
        {
          testName: testInfo.title,
          operationNames: ["DeleteIntegrationConfig"],
          checkDataErrors: true,
        },
      );
      await expect(locators.addToDiscordBtn).toBeEnabled();
    },
  );

  test(
    "Discord - open Connect with an empty token box, verify Install stays disabled, cancel, verify the tile is still not connected",
    { tag: ["@test", "@oss", "@regression", "@negative", "@validation", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page);
      expectOrSkip(startsWithoutBot, !(await isDiscordConnected(locators)), "Discord is connected — the empty-token check needs a tenant without a bot.");

      await locators.addToDiscordBtn.click();
      await expect(locators.discordBotTokenInput).toBeVisible({ timeout: 10000 });
      await expect(locators.discordInstallBtn).toBeDisabled();

      await locators.discordInstallCancelBtn.click();
      await expect(locators.discordBotTokenInput).toBeHidden({ timeout: 10000 });
      await expect(locators.discordNotConnected).toBeVisible();
    },
  );

  test(
    "Discord - connect with a wrong bot token, verify an install error shows and the tile is still not connected",
    { tag: ["@test", "@oss", "@regression", "@negative", "@validation", "@snackbar", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page);
      expectOrSkip(startsWithoutBot, !(await isDiscordConnected(locators)), "Discord is connected — the wrong-token check needs a tenant without a bot.");

      await locators.addToDiscordBtn.click();
      await locators.discordBotTokenInput.fill(WRONG_TOKEN);
      const rejected = page.waitForResponse((r) => r.url().includes(INSTALL_PATH));
      await locators.discordInstallBtn.click();

      expect((await rejected).status()).toBe(400);
      await expect(locators.discordInstallErrorToast).toBeVisible({ timeout: 10000 });
      await expect(locators.discordBotTokenInput).toBeVisible();

      await locators.discordInstallCancelBtn.click();
      await page.reload();
      await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
      expect(await isDiscordConnected(locators)).toBe(false);
    },
  );

  test(
    "Discord - connect with a token whose bot is in no server, verify the invite step shows and nothing is connected",
    { tag: ["@test", "@oss", "@regression", "@negative", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page, mockInstallWithBotInNoServer);
      expectOrSkip(startsWithoutBot, !(await isDiscordConnected(locators)), "Discord is connected — the invite-step check needs a tenant without a bot.");

      await locators.addToDiscordBtn.click();
      await locators.discordBotTokenInput.fill(WRONG_TOKEN);
      await locators.discordInstallBtn.click();

      await expect(locators.discordInviteBtn).toBeVisible({ timeout: 30000 });
      await locators.discordInstallDoneBtn.click();
      await expect(locators.discordInviteBtn).toBeHidden({ timeout: 10000 });

      // The install call was mocked, so no bot was stored.
      await page.reload();
      await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
      expect(await isDiscordConnected(locators)).toBe(false);
    },
  );

  test(
    "Discord - connect the bot with the token from the env file, verify the tile shows Connected after a reload",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }) => {
      test.setTimeout(180000);
      test.skip(missingEnv.length > 0, `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`);

      const locators = await openDiscord(page);
      expectOrSkip(startsWithoutBot, !(await isDiscordConnected(locators)), "Discord is still connected after the delete step, so the install cannot run.");

      await locators.addToDiscordBtn.click();
      await expect(locators.discordBotTokenInput).toBeVisible({ timeout: 10000 });
      await locators.discordBotTokenInput.fill(process.env.DISCORD_BOT_TOKEN!);
      await locators.discordInstallBtn.click();

      // A bot that is in no server ends on an invite step with a Done button; otherwise the modal just closes.
      await expect(locators.discordInstallDoneBtn.or(locators.discordBotTokenInput).first()).toBeVisible({ timeout: 30000 });
      if (await locators.discordInstallDoneBtn.isVisible()) {
        await locators.discordInstallDoneBtn.click();
      }
      await expect(locators.discordBotTokenInput).toBeHidden({ timeout: 30000 });

      await page.reload();
      await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
      expect(await isDiscordConnected(locators)).toBe(true);
      await expect(locators.addToDiscordBtn).toBeDisabled();
      installedByThisRun = true;
    },
  );

  test(
    "Discord - open a freshly connected bot with no channel mapped, verify Test Notification is disabled and Map Channel is offered",
    { tag: ["@test", "@oss", "@regression", "@negative", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      expectOrSkip(installedByThisRun, await locators.discordMapChannelBtn.isVisible(), "A freshly installed bot should have no channel mapped, but Map Channel is not offered.");

      await expect(locators.testDiscordNotificationBtn).toBeDisabled();
      await expect(locators.discordMapChannelBtn).toBeEnabled();
    },
  );

  test(
    "Discord - open Map Channel for a bot that sees no server, verify the invite hint and invite button show",
    { tag: ["@test", "@oss", "@regression", "@negative", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page, mockChannelListWithBotInNoServer);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      expectOrSkip(installedByThisRun, await locators.discordMapChannelBtn.isVisible(), "A freshly installed bot should have no channel mapped, but Map Channel is not offered.");

      await locators.discordMapChannelBtn.click();
      await expect(locators.discordMapInviteBtn).toBeVisible({ timeout: 30000 });
      await expect(locators.discordMapSaveBtn).toBeDisabled();
    },
  );

  test(
    "Discord - map a channel to the new bot, verify the row shows it and Test Notification turns on",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }) => {
      test.setTimeout(180000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      expectOrSkip(installedByThisRun, await locators.discordMapChannelBtn.isVisible(), "A freshly installed bot should have no channel mapped, but Map Channel is not offered.");

      await locators.discordMapChannelBtn.click();
      await locators.discordChannelSelect.click();
      await expect(locators.discordChannelOptions.first()).toBeVisible({ timeout: 30000 });
      const environmentChannel = locators.discordChannelOptions.filter({ hasText: CHANNEL_LABEL });
      // Never fall back to another channel: that would post this environment's alerts into someone else's.
      await expect(environmentChannel.first(), `The bot has no "${CHANNEL_NAME}" channel to map for this environment`).toBeVisible({ timeout: 15000 });
      await environmentChannel.first().click();
      await locators.discordMapSaveBtn.click();

      await expect(locators.discordMapSaveBtn).toBeHidden({ timeout: 30000 });
      await expect(locators.discordRow).toContainText(CHANNEL_NAME, { timeout: 30000 });
      await expect(locators.testDiscordNotificationBtn).toBeEnabled({ timeout: 15000 });
    },
  );

  test(
    "Discord - edit the mapped channel, turn off Reply to repeat alerts in the thread, save, reopen, verify it stayed off, turn it back on",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }) => {
      test.setTimeout(180000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      expectOrSkip(installedByThisRun, !(await locators.discordMapChannelBtn.isVisible()), "A channel was mapped earlier in this run, but Map Channel is offered again.");

      await openRowMenuItem(locators, "edit");
      await expect(locators.discordThreadRepeats).toBeVisible({ timeout: 30000 });
      const startedOn = await locators.discordThreadRepeats.isChecked();
      await locators.discordThreadRepeats.setChecked(!startedOn);
      await locators.discordMapSaveBtn.click();
      await expect(locators.discordMapSaveBtn).toBeHidden({ timeout: 30000 });

      await openRowMenuItem(locators, "edit");
      await expect(locators.discordThreadRepeats).toBeVisible({ timeout: 30000 });
      expect(await locators.discordThreadRepeats.isChecked()).toBe(!startedOn);

      // Put the setting back so the tenant ends as it started.
      await locators.discordThreadRepeats.setChecked(startedOn);
      await locators.discordMapSaveBtn.click();
      await expect(locators.discordMapSaveBtn).toBeHidden({ timeout: 30000 });
    },
  );

  test(
    "Discord - edit the mapped channel and pick a different one, verify the row shows it, then pick the original back",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }) => {
      test.setTimeout(180000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      expectOrSkip(installedByThisRun, !(await locators.discordMapChannelBtn.isVisible()), "A channel was mapped earlier in this run, but Map Channel is offered again.");

      await openRowMenuItem(locators, "edit");
      await locators.discordChannelSelect.click();
      await expect(locators.discordChannelOptions.first()).toBeVisible({ timeout: 30000 });
      const labels = (await locators.discordChannelOptions.allInnerTexts()).map((s) => s.trim()).filter((s) => s.length > 0);
      const other = labels.find((label) => !CHANNEL_LABEL.test(label));
      test.skip(!other, "The bot has no channel besides this environment's, so there is nothing else to pick.");
      const otherName = other!.split("/").pop()!.trim();

      await locators.discordChannelOptions.filter({ hasText: other! }).first().click();
      await locators.discordMapSaveBtn.click();
      await expect(locators.discordMapSaveBtn).toBeHidden({ timeout: 30000 });
      await expect(locators.discordRow).toContainText(otherName, { timeout: 30000 });

      // Always end on this environment's channel, whatever it was mapped to before.
      await openRowMenuItem(locators, "edit");
      await locators.discordChannelSelect.click();
      await locators.discordChannelOptions.filter({ hasText: CHANNEL_LABEL }).first().click();
      await locators.discordMapSaveBtn.click();
      await expect(locators.discordMapSaveBtn).toBeHidden({ timeout: 30000 });
      await expect(locators.discordRow).toContainText(CHANNEL_NAME, { timeout: 30000 });
    },
  );

  test(
    "Discord - choose Update Token and enter a wrong token, verify an install error shows and the bot stays connected",
    { tag: ["@test", "@oss", "@regression", "@negative", "@validation", "@snackbar", "@discord"] },
    async ({ page }) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");

      await openRowMenuItem(locators, "update");
      await locators.discordBotTokenInput.fill(WRONG_TOKEN);
      const rejected = page.waitForResponse((r) => r.url().includes(INSTALL_PATH));
      await locators.discordInstallBtn.click();

      expect((await rejected).status()).toBe(400);
      await expect(locators.discordInstallErrorToast).toBeVisible({ timeout: 10000 });
      await locators.discordInstallCancelBtn.click();

      await page.reload();
      await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
      expect(await isDiscordConnected(locators)).toBe(true);
    },
  );

  test(
    "Discord - choose Update Token and enter the env file token again, verify the bot stays connected with its channel",
    { tag: ["@test", "@oss", "@regression", "@functional", "@crud", "@discord"] },
    async ({ page }) => {
      test.setTimeout(180000);
      test.skip(missingEnv.length > 0, `Missing required env vars: ${missingEnv.join(", ")} — add them to the E2E_TEST_ENV secret`);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");
      const hadChannel = !(await locators.discordMapChannelBtn.isVisible());

      await openRowMenuItem(locators, "update");
      await locators.discordBotTokenInput.fill(process.env.DISCORD_BOT_TOKEN!);
      await locators.discordInstallBtn.click();
      await expect(locators.discordInstallDoneBtn.or(locators.discordBotTokenInput).first()).toBeVisible({ timeout: 30000 });
      if (await locators.discordInstallDoneBtn.isVisible()) {
        await locators.discordInstallDoneBtn.click();
      }
      await expect(locators.discordBotTokenInput).toBeHidden({ timeout: 30000 });

      await page.reload();
      await expect(locators.discordIntegrationBox).toBeVisible({ timeout: 30000 });
      expect(await isDiscordConnected(locators)).toBe(true);
      // The channel the bot had must survive a token update, or notifications silently stop.
      if (hadChannel) {
        await expect(locators.discordMapChannelBtn).toBeHidden();
        await expect(locators.testDiscordNotificationBtn).toBeEnabled({ timeout: 15000 });
      }
    },
  );

  test(
    "Discord - send a test notification, verify Nudgebee reports it delivered",
    { tag: ["@test", "@oss", "@regression", "@functional", "@snackbar", "@discord"] },
    async ({ page }, testInfo) => {
      test.setTimeout(120000);

      const locators = await openDiscord(page);
      expectOrSkip(installedByThisRun, await isDiscordConnected(locators), "Discord is not connected, but this run installed the bot.");

      // Once this run has mapped a channel the button must turn on, so wait for it and fail if it does not.
      if (installedByThisRun) {
        await expect(locators.testDiscordNotificationBtn, "A channel was mapped earlier in this run, but Test Notification is disabled.").toBeEnabled({ timeout: 15000 });
      } else {
        // A short retrying probe: the button can render disabled for a moment while the page loads, and a bot with no channel mapped is an expected state to skip on.
        const channelMapped = await expect(locators.testDiscordNotificationBtn)
          .toBeEnabled({ timeout: 5000 })
          .then(() => true)
          .catch(() => false);
        test.skip(!channelMapped, "@qa - Discord has no channel mapped, so Test Notification is disabled. Map a channel first.");
      }

      await waitForGraphQLAndValidate(
        page,
        async () => {
          await locators.testDiscordNotificationBtn.click();
          await locators.discordTestSuccessToast
            .or(locators.genericErrorToast)
            .first()
            .waitFor({ state: "visible", timeout: 30000 });
        },
        {
          testName: testInfo.title,
          operationNames: ["SendTestNotification"],
          checkDataErrors: true,
        },
      );

      await expect(locators.discordTestSuccessToast).toBeVisible();
    },
  );
});
