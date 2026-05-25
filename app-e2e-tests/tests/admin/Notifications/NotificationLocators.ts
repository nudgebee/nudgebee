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
  readonly emailInput!: Locator;
  readonly excludeUsersSelector!: Locator;

  constructor(page: Page) {
    super(page);

    this.notificationsTab = page.locator("#anchor-tab-Notifications")
      .or(page.getByRole("tab", { name: "Notifications" }));

    this.notificationRuleBtn = page.locator("#notification-rule")
      .or(page.getByRole("button", { name: /Create Rule/i }));

    this.clusterSelector = page.locator("#notification-account-selector")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Account/ }).first());

    this.notificationNameInput = page.locator("#notificationName")
      .or(page.getByRole("textbox", { name: /rule name/i }));

    this.successToast = page.locator('[data-testid="rule-created-toast"]')
      .or(page.getByText("Rule Created Successfully").first());

    this.updatedSuccessToast = page.locator('[data-testid="rule-updated-toast"]')
      .or(page.getByText("Rule Updated Successfully").first());

    this.dailyHighTab = page.locator("#tab-daily-recap")
      .or(page.getByRole("tab", { name: /daily/i }));

    this.optimizationTab = page.locator("#tab-optimize")
      .or(page.getByRole("tab", { name: /optim/i }));

    this.sloTab = page.locator("#tab-slo")
      .or(page.getByRole("tab", { name: /slo/i }));

    this.troubleshootTab = page.locator("#tab-troubleshoot")
      .or(page.getByRole("tab", { name: /troubleshoot/i }));

    this.cloudTab = page.locator("#tab-cloud")
      .or(page.getByRole("tab", { name: /cloud/i }));

    this.slackBadge = page.locator("#slack-badge")
      .or(page.getByRole("button", { name: /^slack$/i }));

    this.msTeamsBadge = page.locator("#msteams-badge")
      .or(page.getByRole("button", { name: /ms teams|microsoft teams/i }));

    this.gChatBadge = page.locator("#gchat-badge")
      .or(page.getByRole("button", { name: /google chat|gchat/i }));

    this.emailBadge = page.locator("#email-badge")
      .or(page.getByRole("button", { name: /^email$/i }));

    this.slackChannelSelector = page.locator("#notification-slack-channel")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Channel/ }).first());

    this.msTeamsGroupSelector = page.locator("#notification-msteams-group")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Team/ }).first());

    this.msTeamsChannelSelector = page.locator("#notification-msteams-channel")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Channel/ }).first());

    this.gChatChannelSelector = page.locator("#notification-gchat-space")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Space/ }).first());

    this.emailInput = page.locator('#email-input')
      .or(page.getByRole("textbox", { name: /email/i }));

    this.excludeUsersSelector = page.locator("#notification-exclude-users")
      .or(page.locator('[role="dialog"]').locator('button').filter({ hasText: /^Exclude Users/ }).first());
  }

  getDuplicateError(): Locator {
    return this.page.locator('[data-testid="rule-duplicate-error"]')
      .or(this.page.getByText("A notification rule with this name already exists", { exact: false }));
  }

  get duplicateConstraintError(): Locator {
    return this.page.locator('[data-testid="rule-constraint-error"]')
      .or(this.page.getByText("Duplicate value violates unique constraint"));
  }
}
