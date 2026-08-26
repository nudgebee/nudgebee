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

// Selects `optionText` from a FilterDropdown and returns the label actually clicked.
//
// Types into the panel's search box rather than reading the option list straight off the
// open panel, because on a `grouped` dropdown there is nothing to read: GroupedOptionsList
// starts with `openGroups = {}` — every group collapsed — and only renders its role="option"
// rows once a group is expanded or its `forceOpen={!!search.trim()}` is set. The cluster
// filter is grouped by cloud provider, so an unsearched panel shows provider headers and
// zero options. Proved in CI: waiting on role="option" straight after the click timed out
// at 30s, on the first attempt and the retry.
//
// Searching is also the flow a person uses, and it is what the sibling notificationHelper
// does. There is deliberately no "fall back to the first option": silently selecting a
// different account than the one asked for would let a filter test pass while proving
// nothing about the account it named.
export async function pickFilterOption(page: Page, trigger: Locator, optionText: string): Promise<string> {
  await expect(trigger).toBeEnabled();
  await trigger.click();

  // The panel is a body-level Popover, so it is not inside the dropdown's own container.
  // Only one FilterDropdown panel is open at a time.
  const search = page.locator('input[placeholder^="Search"]').first();
  await expect(search).toBeVisible({ timeout: 30000 });
  await search.fill(optionText);

  const option = page.locator('[role="option"]').filter({ hasText: optionText }).first();
  await expect(
    option,
    `No option matching "${optionText}" in the filter — check the value of CLUSTER_NAME / CLUSTER against the accounts this tenant holds.`
  ).toBeVisible({ timeout: 30000 });

  const label = ((await option.textContent()) ?? "").trim();
  await option.click();
  // Single-select handleToggle calls setAnchorEl(null), so the panel closing is the signal
  // the selection was committed rather than merely hovered.
  await expect(search).toBeHidden({ timeout: 15000 });
  return label;
}
