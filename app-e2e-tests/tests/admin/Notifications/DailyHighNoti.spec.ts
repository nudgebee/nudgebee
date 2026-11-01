import { test } from "@playwright/test";
import * as channels from "./notificationConstants";
import {
  navigateToNewNotificationRule,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";
import { users } from "../Users/usersConstants";

test("Add Daily High notification rule", { tag: ["@dev", "@test", "@oss", "@regression", "@functional", "@crud"] }, async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  // There is one daily email rule; with one already saved, its chip is disabled and
  // the page edits that rule instead.
  if (await locators.isTypeChipDisabled(locators.dailyHighTab)) {
    await locators.ruleCancelEditorBtn.click();
    test.skip(true, "This tenant already has its daily email rule");
  }
  await locators.selectOnlyType(locators.dailyHighTab);

  // The daily email covers every account (no account picker) and sends by email only.
  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_daily_high,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_daily_high,
    gChat: channels.gchat.gchat_daily_high,
    email: users[0].email,
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_5, anyConfigured, testInfo);
});
