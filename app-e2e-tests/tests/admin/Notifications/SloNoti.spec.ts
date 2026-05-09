import { test } from "@playwright/test";
import * as channels from "../../../channels";
import {
  navigateToNewNotificationRule,
  selectCluster,
  configureChannels,
  submitAndVerify,
} from "./notificationHelper";

test("Add SLO Notification Rule", async ({ page }, testInfo) => {
  test.setTimeout(120000);

  const locators = await navigateToNewNotificationRule(page);

  await locators.sloTab.click();

  await selectCluster(locators, page);

  const anyConfigured = await configureChannels(page, locators, {
    slack: channels.slack.slack_slo,
    msTeamsGroup: channels.msteams.msteams_group_name,
    msTeamsChannel: channels.msteams.msteams_slo,
    gChat: channels.gchat.gchat_slo,
  });

  await submitAndVerify(page, locators, channels.ruleNames.rule_4, anyConfigured, testInfo);
});
