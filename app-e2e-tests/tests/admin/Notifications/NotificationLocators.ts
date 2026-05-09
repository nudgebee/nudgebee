import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

export class NotificationLocators extends CommonLocators {
  readonly notificationsTab!: Locator;

  readonly notificationRuleBtn!: Locator;
  readonly clusterSelector!: Locator;
  readonly notificationNameInput!: Locator;
  readonly successToast!: Locator;
  readonly updatedSuccessToast!: Locator;
  readonly dailyHighTab!: Locator;
  readonly optimizationTab!: Locator;
  readonly sloTab!: Locator;
  readonly troubleshootTab!: Locator;
  readonly cloudTab!: Locator;

  readonly enableNotificationSwitch = "#enable-notification-switch";

  readonly slackBadge!: Locator;
  readonly msTeamsBadge!: Locator;
  readonly gChatBadge!: Locator;
  readonly emailBadge!: Locator;

  readonly slackChannelSelector!: Locator;
  readonly msTeamsGroupSelector!: Locator;
  readonly msTeamsChannelSelector!: Locator;
  readonly gChatChannelSelector!: Locator;

  constructor(page: Page) {
    super(page);

    this.notificationsTab = page.locator("#anchor-tab-Notifications");

    this.notificationRuleBtn = page.locator("#notification-rule");
    this.clusterSelector = page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Account/ }).first();
    this.notificationNameInput = page.locator("#notificationName");
    this.successToast = page.getByText("Rule Created Successfully");
    this.updatedSuccessToast = page.getByText("Rule Updated Successfully");

    this.dailyHighTab = page.locator("#tab-daily-recap");
    this.optimizationTab = page.locator("#tab-optimize");
    this.sloTab = page.locator("#tab-slo");
    this.troubleshootTab = page.locator("#tab-troubleshoot");
    this.cloudTab = page.locator("#tab-cloud");

    this.slackBadge = page.locator("#slack-badge");
    this.msTeamsBadge = page.locator("#msteams-badge");
    this.gChatBadge = page.locator("#gchat-badge");
    this.emailBadge = page.locator("#email-badge");

    this.slackChannelSelector = page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Channel/ }).first();
    this.msTeamsGroupSelector = page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Team/ }).first();
    this.msTeamsChannelSelector = page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Channel/ }).first();
    this.gChatChannelSelector = page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Space/ }).first();
  }

  getDuplicateError(): Locator {
    return this.page.getByText("A notification rule with this name already exists", {
      exact: false,
    });
  }

  get duplicateConstraintError(): Locator {
    return this.page.getByText("Duplicate value violates unique constraint");
  }
}
