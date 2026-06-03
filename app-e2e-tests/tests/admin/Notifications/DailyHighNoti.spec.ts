import { test } from "@playwright/test";
import * as channels from "./notificationConstants";
import { ensureSwitchEnabled } from "../../utils/helpers";
import {
  navigateToNewNotificationRule,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";
import { users } from "../Users/usersConstants";

test("Add Daily High notification rule", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  await locators.dailyHighTab.click();

  await page.waitForLoadState("networkidle");
  await ensureSwitchEnabled(page, locators.enableNotificationSwitch);

  // Daily High scope is global (no cluster selector).
  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_daily_high,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_daily_high,
    gChat: channels.gchat.gchat_daily_high,
    email: users[0].email,
    excludeUsers: [users[1].email, users[2].email],
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_5, anyConfigured, testInfo);
});
