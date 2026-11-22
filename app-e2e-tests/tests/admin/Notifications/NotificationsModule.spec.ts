// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openNotificationsTab,
  openCreateRuleModal,
  makeRuleInert,
  createSuppressedRule,
  deleteRuleByName,
  waitForRulesListing,
  confirmDeleteAndWaitForRelist,
} from "./notificationsModuleHelper";
import { RULE_COLUMNS, RULE_TYPE_LABEL, RULE_STATUS_LABEL, NAME_FORMAT_ERROR, INVALID_RULE_NAME, uniqueRuleName } from "./notificationsModuleConstants";

// Admin > Notification Rules — the notification-rule listing at /user-management#notification-rules
// (app/src/components/notifications/index.tsx).
//
// Every rule created here is a one-hour weekly-digest Don't send (makeRuleInert), so it
// holds no channel mapping, never sends to anyone, and ends by itself. Each test deletes
// the rule it created; names are unique per rule, and no test reads, edits or deletes a
// rule it did not create.
test.describe.configure({ timeout: 240000 });

test(
  "Notifications sanity - open the Notifications tab, verify the rules table, Add rule button, type filters, search and Check where it goes render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await test.step("The listing and its create action are present", async () => {
      await expect(noti.listingRoot).toBeVisible();
      await expect(noti.notificationRuleBtn).toBeVisible();
      await expect(noti.notificationRuleBtn).toHaveText(/Add rule/i);
    });

    await test.step("The type filters, search and the route check render in the toolbar", async () => {
      await expect(noti.allFilterChip).toBeVisible();
      await expect(noti.weeklyDigestFilterChip).toBeVisible();
      await expect(noti.searchInput).toBeVisible();
      await expect(noti.testNotificationBtn).toBeVisible();
    });
  }
);

test(
  "Notifications sanity - open the Notifications tab, verify the rules listing renders its columns and the When no rule matches line",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    for (const column of RULE_COLUMNS) {
      await expect(noti.rulesTable.locator("th", { hasText: column }).first()).toBeVisible();
    }
    // Shown for every tenant, with or without rules.
    await expect(noti.fallbackLine).toContainText("When no rule matches:");
  }
);

test(
  "Notifications - add a blank rule, make it a one-hour weekly digest Don't send, save, verify it appears in the listing as paused",
  { tag: ["@dev", "@regression", "@crud", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    try {
      await createSuppressedRule(page, noti, ruleName);

      await test.step("The persisted row carries the rule's own type and status", async () => {
        const row = noti.rowByName(ruleName);
        await expect(row).toContainText(RULE_TYPE_LABEL);
        // The status is read back from the saved rule's end time, not the form's state.
        await expect(row).toContainText(RULE_STATUS_LABEL);
      });
    } finally {
      await deleteRuleByName(page, noti, ruleName);
    }
  }
);

test(
  "Notifications - create a paused rule, delete it from the listing, verify the rule is removed from the table",
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    test.setTimeout(240000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    try {
      await createSuppressedRule(page, noti, ruleName);

      await test.step("The confirmation names the rule about to be deleted", async () => {
        await noti.openDeleteForRule(ruleName);
        await expect(noti.deleteDialog).toBeVisible({ timeout: 30000 });
        await expect(noti.deleteDialog).toContainText(ruleName);
      });

      await test.step("Confirming removes the row from the re-fetched listing", async () => {
        await confirmDeleteAndWaitForRelist(page, noti);
        await expect(noti.rowByName(ruleName)).toHaveCount(0, { timeout: 60000 });
      });
    } finally {
      await deleteRuleByName(page, noti, ruleName);
    }
  }
);

test(
  "Notifications - add a rule, type a name starting with a hyphen, save, verify the name format rejection",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await openCreateRuleModal(noti);
    await makeRuleInert(page, noti);

    await test.step("Saving a name that starts with a hyphen is refused", async () => {
      await noti.notificationNameInput.fill(INVALID_RULE_NAME);
      await expect(noti.notificationNameInput).toHaveValue(INVALID_RULE_NAME);
      await noti.ruleSubmitBtn.click();
      await expect(noti.nameError).toHaveText(NAME_FORMAT_ERROR, { timeout: 30000 });
    });

    await test.step("The form stays open so nothing was saved", async () => {
      await expect(noti.ruleDialog).toBeVisible();
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
      await expect(noti.rowByName(INVALID_RULE_NAME)).toHaveCount(0);
    });
  }
);

test(
  "Notifications - pick the Weekly digest type filter, verify the listing narrows and the When no rule matches line follows the type",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await noti.weeklyDigestFilterChip.click();
    await expect(noti.weeklyDigestFilterChip).toHaveAttribute("aria-pressed", "true");
    await expect(noti.fallbackLine).toContainText("Weekly digests", { timeout: 30000 });

    // The chip under the pointer shows a tooltip that can cover its neighbours; move away first.
    await page.mouse.move(0, 0);
    await expect(page.getByRole("tooltip")).toHaveCount(0, { timeout: 10000 });
    await noti.allFilterChip.click();
    await expect(noti.fallbackLine).toContainText("Notifications", { timeout: 30000 });
    await waitForRulesListing(noti);
  }
);

test(
  "Notifications - add a rule, enter a name, cancel the editor, verify no rule was created and the form is empty on reopen",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    await openCreateRuleModal(noti);
    await noti.notificationNameInput.fill(ruleName);
    await expect(noti.notificationNameInput).toHaveValue(ruleName);

    await test.step("Cancel closes the form without saving", async () => {
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
      await expect(noti.rowByName(ruleName)).toHaveCount(0, { timeout: 30000 });
    });

    await test.step("Reopening starts from a blank form", async () => {
      await openCreateRuleModal(noti);
      await expect(noti.notificationNameInput).toHaveValue("");
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
    });
  }
);

test(
  "Notifications sanity - switch from Notifications to the Access & Users > Audit Log tab and back, verify the rules listing is restored",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await test.step("Audits replaces the notification listing", async () => {
      // Hovering Access & Users opens a modal dropdown whose backdrop swallows clicks on the sub-tab row, so use its Audit Log item.
      await noti.accessUsersTab.hover();
      await noti.auditLogMenuItem.click();
      await expect(noti.auditsTab).toHaveAttribute("aria-selected", "true", { timeout: 30000 });
      // The tab body is swapped, not hidden — /user-management renders only the selected
      // section's Body, so the notifications toolbar leaves the DOM entirely.
      await expect(noti.notificationRuleBtn).toHaveCount(0, { timeout: 30000 });
    });

    await test.step("Returning restores the rules listing and its toolbar", async () => {
      await noti.notificationsTab.click();
      await expect(noti.notificationsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
      await waitForRulesListing(noti);
      await expect(noti.notificationRuleBtn).toBeVisible({ timeout: 30000 });
    });
  }
);
