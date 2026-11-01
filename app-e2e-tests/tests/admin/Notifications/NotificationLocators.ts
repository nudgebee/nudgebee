import { Page, Locator } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";

// The rule editor (app/src/components/notifications/rules/RuleEditorModal.tsx):
// type chips under "Which", an account picker under "From", and one channel
// picker per connected app under "Do".
export class NotificationLocators extends CommonLocators {
  readonly notificationsTab!: Locator;

  readonly notificationRuleBtn!: Locator;
  readonly blankRuleItem!: Locator;
  readonly scopeSelector!: Locator;
  readonly clusterSelector!: Locator;
  readonly notificationNameInput!: Locator;
  readonly successToast!: Locator;
  readonly updatedSuccessToast!: Locator;
  readonly dailyHighTab!: Locator;
  readonly optimizationTab!: Locator;
  readonly sloTab!: Locator;
  readonly troubleshootTab!: Locator;
  readonly cloudTab!: Locator;
  readonly weeklyDigestTab!: Locator;

  readonly sendAction!: Locator;
  readonly dontSendAction!: Locator;
  readonly pauseForSelector!: Locator;
  readonly ruleSaveBtn!: Locator;
  readonly ruleCancelEditorBtn!: Locator;
  readonly ruleError!: Locator;

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

    // "Add rule" opens a menu of starting points; "Blank rule" opens the editor.
    this.notificationRuleBtn = page.locator("#notification-rule")
      .or(page.getByRole("button", { name: /Add rule/i }));
    this.blankRuleItem = page.locator("#add-rule-blank")
      .or(page.getByRole("menuitem", { name: "Blank rule" }));

    this.scopeSelector = page.locator("#rule-scope");
    this.clusterSelector = page.locator("#account-1");

    this.notificationNameInput = page.locator("#notificationName");

    this.successToast = page.getByText("Rule Created Successfully").first();
    this.updatedSuccessToast = page.getByText("Rule Updated Successfully").first();

    this.dailyHighTab = page.locator("#rule-type-daily_recap");
    this.optimizationTab = page.locator("#rule-type-optimize");
    this.sloTab = page.locator("#rule-type-slo");
    this.troubleshootTab = page.locator("#rule-type-troubleshoot");
    this.cloudTab = page.locator("#rule-type-cloud");
    this.weeklyDigestTab = page.locator("#rule-type-weekly_digest");

    this.sendAction = page.locator("#rule-action").getByRole("radio", { name: "Send them" });
    this.dontSendAction = page.locator("#rule-action").getByRole("radio", { name: /Don.t send them/ });
    this.pauseForSelector = page.locator("#rule-pause-for");
    this.ruleSaveBtn = page.locator("#save-rule");
    this.ruleCancelEditorBtn = page.locator("#cancel-rule");
    this.ruleError = page.locator("#rule-error");

    this.slackChannelSelector = page.locator("#slack-channel");
    this.msTeamsGroupSelector = page.locator("#msteam-groups");
    this.msTeamsChannelSelector = page.locator("#msteam-channel");
    this.gChatChannelSelector = page.locator("#gchat-channel");
    this.emailInput = page.locator("#email-input");
    this.excludeUsersSelector = page.locator("#exclude-users");
  }

  get dropdownListbox(): Locator {
    return this.page.locator('[role="listbox"]');
  }

  // Scoped to the open listbox: the rules table has its own "Search rules" box that is always visible.
  get dropdownSearchInput(): Locator {
    return this.dropdownListbox.locator('input[placeholder^="Search"]');
  }

  get dropdownGroupHeaders(): Locator {
    return this.dropdownListbox.locator('[role="button"]');
  }

  ruleNameText(name: string): Locator {
    return this.page.getByText(name, { exact: true });
  }

  // A disabled chip is a plain span with no disabled attribute, so only pointer-events gives it away.
  async isTypeChipDisabled(chip: Locator): Promise<boolean> {
    return chip.evaluate((el) => getComputedStyle(el).pointerEvents === "none");
  }

  // Selecting a type chip replaces the default Troubleshooting when it is the
  // only one picked; a chip is a toggle, so this clicks only when it is off.
  async selectOnlyType(chip: Locator): Promise<void> {
    if ((await chip.getAttribute("aria-pressed")) !== "true") {
      await chip.click();
    }
    if (chip !== this.troubleshootTab && (await this.troubleshootTab.getAttribute("aria-pressed")) === "true") {
      await this.troubleshootTab.click();
    }
  }

  getDuplicateError(): Locator {
    return this.page.getByText("A notification rule with this name already exists.", { exact: false });
  }

  get duplicateConstraintError(): Locator {
    return this.page.getByText("Duplicate value violates unique constraint");
  }
}
