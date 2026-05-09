import { test } from "@playwright/test";
import * as channels from "../../../channels";
import { ensureSwitchEnabled } from "../../utils/helpers";
import {
  navigateToNewNotificationRule,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";

test("Add Daily High notification rule", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  await locators.dailyHighTab.click();

  // The daily_recap tab triggers fetchDailyHighlightsData() which may load
  // an existing rule with is_suppressed=true, setting pointerEvents:'none'
  // on the channel badges' ancestor Box. Wait for the API to settle, then
  // re-ensure the notification switch is ON so badges become clickable.
  await page.waitForLoadState("networkidle");
  await ensureSwitchEnabled(page, locators.enableNotificationSwitch);

  // Daily High scope is global (no cluster selector).
  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_daily_high,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_daily_high,
    gChat: channels.gchat.gchat_daily_high,
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_5, anyConfigured, testInfo);
});
