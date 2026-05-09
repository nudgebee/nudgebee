import { test } from "@playwright/test";
import * as channels from "../../../channels";
import {
  navigateToNewNotificationRule,
  selectCluster,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";

test("Add Optimization Notification Rule", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  await locators.optimizationTab.click();

  await selectCluster(locators, page);

  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_optimization,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_optimization,
    gChat: channels.gchat.gchat_optimization,
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_2, anyConfigured, testInfo);
});
