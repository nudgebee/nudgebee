// Not for OSS
import { test, expect } from "@playwright/test";
import {
  openNotificationsTab,
  openCreateRuleModal,
  selectDailyRecapSource,
  disableDelivery,
  createSuppressedRule,
  deleteRuleByName,
  waitForRulesListing,
} from "./notificationsModuleHelper";
import {
  RULE_COLUMNS,
  RULE_SOURCE_LABEL,
  NAME_FORMAT_ERROR,
  INVALID_RULE_NAME,
  uniqueRuleName,
  pickFilterOption,
  configuredClusterName,
} from "./notificationsModuleConstants";

// Admin > Notification Rules — the notification-rule listing at /user-management#notification-rules
// (app/src/components/notifications/index.tsx).
//
// Every rule created here is tenant-wide Daily Recap AND suppressed, so it holds no
// channel mapping and can never deliver to a real Slack, Teams or inbox on the shared dev
// tenant. Each test deletes the rule it created; names are unique per rule, and no test
// reads, edits or deletes a rule it did not create.
test.describe.configure({ timeout: 240000 });

test(
  "Notifications sanity - open the Notifications tab, verify the rules table, Create Rule button and the three scope filters render",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await test.step("The listing and its create action are present", async () => {
      await expect(noti.listingRoot).toBeVisible();
      await expect(noti.notificationRuleBtn).toBeVisible();
      await expect(noti.notificationRuleBtn).toHaveText(/Create Rule/i);
    });

    await test.step("All three scope filters render in the toolbar", async () => {
      await expect(noti.clusterFilter).toBeVisible();
      await expect(noti.namespaceFilter).toBeVisible();
      await expect(noti.applicationFilter).toBeVisible();
    });
  }
);

test(
  "Notifications sanity - open the Notifications tab, verify the rules listing renders its eight columns",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    const rows = await noti.ruleRows.count();

    if (rows > 0) {
      for (const column of RULE_COLUMNS) {
        await expect(noti.rulesTable.locator("th", { hasText: column }).first()).toBeVisible();
      }
    } else {
      // A tenant with no rules renders EmptyData in place of the table, so there are no
      // headers to assert — the empty panel is the correct rendering, not a miss.
      await expect(noti.emptyState).toBeVisible();
    }
  }
);

test(
  "Notifications - open Create Rule, pick Daily Email, name the rule, turn delivery off, save, verify the suppressed rule appears in the listing",
  { tag: ["@dev", "@regression", "@crud", "@functional"] },
  async ({ page }) => {
    test.setTimeout(240000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    try {
      await createSuppressedRule(page, noti, ruleName);

      await test.step("The persisted row carries the rule's own source and status", async () => {
        const row = noti.rowByName(ruleName);
        // The listing renders snakeToTitleCase(source), so the saved daily_recap source
        // reads as "Daily Recap" here even though its form tab is "Daily Email".
        await expect(row).toContainText(RULE_SOURCE_LABEL);
        // is_suppressed renders as the 'Suppressed' Label in index.tsx, so this is the
        // saved record's state read back from the server, not the form's state.
        await expect(row).toContainText("Suppressed");
      });
    } finally {
      await deleteRuleByName(page, noti, ruleName);
    }
  }
);

test(
  "Notifications - create a suppressed rule, delete it from the listing, verify the rule is removed from the table",
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    test.setTimeout(240000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    try {
      await createSuppressedRule(page, noti, ruleName);

      await test.step("The confirmation names the rule about to be deleted", async () => {
        await noti.deleteBtnForRule(ruleName).click();
        await expect(noti.deleteDialog).toBeVisible({ timeout: 30000 });
        await expect(noti.deleteDialog).toContainText("Are you sure you want to Delete Notification?");
        await expect(noti.deleteDialog).toContainText(ruleName);
      });

      await test.step("Confirming removes the row from the re-fetched listing", async () => {
        await noti.deleteConfirmBtn.click();
        await expect(noti.deleteDialog).toBeHidden({ timeout: 30000 });
        // The table is re-fetched by listNotificationRules() after a successful delete,
        // so an absent row is the server's answer rather than a stale client filter.
        await expect(noti.rowByName(ruleName)).toHaveCount(0, { timeout: 60000 });
      });
    } finally {
      await deleteRuleByName(page, noti, ruleName);
    }
  }
);

test(
  "Notifications - open Create Rule, leave Rule Name empty, save, verify the name rejection blocks the save",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await openCreateRuleModal(noti);
    await selectDailyRecapSource(noti);
    await disableDelivery(page, noti);

    await test.step("Saving with no name is rejected by name, not by scope", async () => {
      await expect(noti.notificationNameInput).toHaveValue("");
      await noti.ruleSubmitBtn.click();
      await expect(noti.nameError).toBeVisible({ timeout: 30000 });
      // Asserts the format message, not NAME_REQUIRED_ERROR: handleSubmit sets the
      // "is required" message first and then unconditionally overwrites it, because
      // isValidString("") is false for the empty string too. The specific message is
      // therefore unreachable — raised as a product bug in the PR, not worked around.
      await expect(noti.nameError).toHaveText(NAME_FORMAT_ERROR);
    });

    await test.step("The form stays open so nothing was saved", async () => {
      await expect(noti.ruleDialog).toBeVisible();
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
    });
  }
);

test(
  "Notifications - open Create Rule, type a rule name starting with a hyphen, verify the name format rejection",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await openCreateRuleModal(noti);
    await selectDailyRecapSource(noti);

    await test.step("The format error is raised while typing, before any save", async () => {
      await noti.notificationNameInput.fill(INVALID_RULE_NAME);
      await expect(noti.notificationNameInput).toHaveValue(INVALID_RULE_NAME);
      // handleNameChange validates on every keystroke, so the rejection does not wait
      // for submit — asserting it here also proves it is the name that was rejected.
      await expect(noti.nameError).toBeVisible({ timeout: 30000 });
      await expect(noti.nameError).toHaveText(NAME_FORMAT_ERROR);
    });

    await test.step("Correcting the name clears the error", async () => {
      await noti.notificationNameInput.fill(uniqueRuleName());
      await expect(noti.nameError).toHaveCount(0, { timeout: 30000 });
    });

    await noti.ruleCancelBtn.click();
    await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
  }
);

test(
  "Notifications - open the Notifications tab, select a Cluster, verify Namespace unlocks and Application stays locked until a Namespace is picked",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await test.step("Namespace and Application start locked behind Cluster", async () => {
      // FilterDropdown renders a real <button disabled>, so this is the native disabled
      // state rather than a styling check.
      await expect(noti.clusterFilter).toBeEnabled();
      await expect(noti.namespaceFilter).toBeDisabled();
      await expect(noti.applicationFilter).toBeDisabled();
    });

    await test.step("Selecting a Cluster unlocks Namespace only", async () => {
      // Selects the configured account by name. The panel's groups render collapsed, so
      // pickFilterOption searches rather than reading the open list — see its comment.
      const cluster = configuredClusterName();
      const picked = await pickFilterOption(page, noti.clusterFilter, cluster);
      expect(picked).toContain(cluster);

      await expect(noti.namespaceFilter).toBeEnabled({ timeout: 30000 });
      await expect(noti.applicationFilter).toBeDisabled();
    });

    await test.step("The listing re-fetches under the new scope", async () => {
      await waitForRulesListing(noti);
    });
  }
);

test(
  "Notifications - open Create Rule, enter a rule name, cancel the modal, verify no rule was created and the form is empty on reopen",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);
    const ruleName = uniqueRuleName();

    await openCreateRuleModal(noti);
    await selectDailyRecapSource(noti);
    await noti.notificationNameInput.fill(ruleName);
    await expect(noti.notificationNameInput).toHaveValue(ruleName);

    await test.step("Cancel closes the form without saving", async () => {
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
      await expect(noti.rowByName(ruleName)).toHaveCount(0, { timeout: 30000 });
    });

    await test.step("Reopening starts from a blank form", async () => {
      // clearAllAndClose resets the modal's state on cancel, so a name left behind here
      // would be carried into the next rule someone creates.
      await openCreateRuleModal(noti);
      await expect(noti.notificationNameInput).toHaveValue("");
      await noti.ruleCancelBtn.click();
      await expect(noti.ruleDialog).toBeHidden({ timeout: 30000 });
    });
  }
);

test(
  "Notifications sanity - switch from Notifications to the Audits tab and back, verify the rules listing is restored",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    test.setTimeout(180000);
    const noti = await openNotificationsTab(page);

    await test.step("Audits replaces the notification listing", async () => {
      await noti.auditsTab.click();
      await expect(noti.auditsTab).toHaveAttribute("data-tab-selected", "true", { timeout: 30000 });
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
