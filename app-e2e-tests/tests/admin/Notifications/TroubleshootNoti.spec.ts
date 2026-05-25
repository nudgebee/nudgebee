import { test } from "@playwright/test";
import * as channels from "./notificationConstants";
import {
  navigateToNewNotificationRule,
  selectCluster,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";

test("Add Troubleshooting Notification Rule", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  await locators.troubleshootTab.click();

  await selectCluster(locators, page);

  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_troubleshoot,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_troubleshoot,
    gChat: channels.gchat.gchat_troubleshoot,
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_1, anyConfigured, testInfo);
});
