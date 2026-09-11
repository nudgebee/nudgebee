// Not for OSS
import { expect, Locator, Page } from "@playwright/test";

// Admin > Notification Rules is reached by fragment, and /user-management redirects to the
// first enabled tab when the fragment is missing — so the fragment is part of the route.
export const NOTIFICATIONS_PATH = "/user-management#notification-rules";

// CustomTable is given id='Notifications' by app/src/components/notifications/index.tsx
// (the `notificationId` const). It puts that id on the <table>, `${id}-body` on the
// <tbody>, and EmptyData renders `${id}-no-data` in their place when there are no rows.
export const RULES_TABLE = "Notifications";

// Column contract from BEST_PRACTICES_HEADER in app/src/components/notifications/index.tsx.
// The ninth header is the empty actions column, which has no label to assert.
export const RULE_COLUMNS = ["Name", "Source", "Cluster", "Application", "Channels", "Status", "Created By", "Created At"];

// Every rule this suite creates is tenant-wide daily_recap and suppressed, so it is
// inert: `suppressed` short-circuits the mappings block in NotificationRuleModal's
// handleSubmit, so the rule carries no channel and can never deliver anything.
//
// The form's tab for this source is labelled "Daily Email", but the listing renders
// snakeToTitleCase('daily_recap') — so the row says "Daily Recap". Both strings are the
// same source and both are asserted, each where the app actually shows it.
export const RULE_SOURCE_LABEL = "Daily Recap";

// Prefix every created rule so anything this suite leaks behind on the shared dev
// tenant is identifiable as test data rather than someone's real rule.
const RULE_PREFIX = "e2e-noti";

// Unique per rule, not per run: two tests creating a rule in the same millisecond
// would otherwise collide on the name's unique constraint.
let ruleCounter = 0;
export function uniqueRuleName(): string {
  ruleCounter += 1;
  return `${RULE_PREFIX} ${Date.now()} ${ruleCounter}`;
}

// The app's own rejection string, copied from NotificationRuleModal.tsx so a reworded
// message fails here rather than silently passing a test that asserts nothing.
//
// handleSubmit's other name message, "Notification rule name is required", is deliberately
// not asserted anywhere: it is assigned and then always overwritten by this one, because
// isValidString("") is false for an empty name too. See the PR's Follow-ups.
export const NAME_FORMAT_ERROR = "Rule name must start with letter or number and can include spaces, underscores, and hyphens.";

// isValidString in NotificationRuleModal.tsx is /^[A-Za-z0-9][\w\s_-]*$/, so a leading
// hyphen is rejected while the rest of the string is otherwise legal — the name fails
// on the rule under test and nothing else.
export const INVALID_RULE_NAME = "-starts-with-a-hyphen";

// Reads the first environment value that is set, naming every key it looked at so a
// missing one is a setup error rather than a mid-test locator timeout.
export function requireEnv(...keys: string[]): string {
  for (const key of keys) {
    const value = process.env[key];
    if (value) return value;
  }
  throw new Error(`None of ${keys.join(" / ")} is set — export one or add it to .env / .env.dev`);
}

// The dev workflow writes CLUSTER_NAME into .env.dev and exports it, while a local run
// usually only exports CLUSTER — notificationHelper.ts in this folder reads the same pair.
export function configuredClusterName(): string {
  return requireEnv("CLUSTER_NAME", "CLUSTER");
}

// Selects `optionText` from a FilterDropdown and returns the label clicked — always the named option, never a fallback to another.
export async function pickFilterOption(page: Page, trigger: Locator, optionText: string): Promise<string> {
  await expect(trigger).toBeEnabled();
  await trigger.click();

  // The panel is a body-level MUI Popover with no id, testid or role, and only one FilterDropdown panel is open at a time.
  const panel = page.locator(".MuiPopover-paper:visible").last();
  await expect(panel).toBeVisible({ timeout: 30000 });
  await expect(panel.locator('[aria-busy="true"]')).toHaveCount(0, { timeout: 30000 });

  const search = panel.locator('input[placeholder^="Search"]');
  const option = panel.locator('[role="option"]').filter({ hasText: optionText }).first();
  // FilterDropdown renders its search box only above 8 options; below that, grouped options sit in collapsed groups.
  if (await search.isVisible()) {
    await search.fill(optionText);
  } else {
    await openGroupsUntilVisible(panel, option);
  }

  await expect(
    option,
    `No option matching "${optionText}" in the filter — check the value of CLUSTER_NAME / CLUSTER against the accounts this tenant holds.`
  ).toBeVisible({ timeout: 30000 });

  const label = ((await option.textContent()) ?? "").trim();
  await option.click();
  // Single-select handleToggle calls setAnchorEl(null), so the panel closing is the signal the selection was committed.
  await expect(panel).toBeHidden({ timeout: 15000 });
  return label;
}

// Every group starts collapsed with only its header (role="button", text = group name) rendered, so open each in turn.
async function openGroupsUntilVisible(panel: Locator, option: Locator): Promise<void> {
  const headers = panel.locator('[role="button"]');
  const names = (await headers.allTextContents()).map((name) => name.trim()).filter(Boolean);
  for (const name of names) {
    if (await option.isVisible()) return;
    const exact = new RegExp(`^\\s*${name.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`);
    await headers.filter({ hasText: exact }).first().click();
  }
}
