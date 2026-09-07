// Not for OSS

import { test, expect } from "@playwright/test";
import { discardUnsavedEdits, ensureSelfOnboardingChecked, openTenantSettings, requiredTenantName, toast } from "./tenantSettingsHelper";
import {
  ERROR_EMPTY_DOMAINS,
  ERROR_INVALID_ROLE,
  FEATURE_TABLE_HEADERS,
  FEATURES_ALL_TAB,
  INVALID_AUTH_ROLE,
  LOG_LABEL_TESTIDS,
  SAVE_SUCCESS_TEXT,
  SPEC_TIMEOUT_MS,
  TRACE_ADVANCED_FIRST_TESTID,
  TRACE_ADVANCED_HIDE_TEXT,
  TRACE_ADVANCED_SHOW_TEXT,
  TRACE_LABEL_TESTIDS,
  WEBHOOK_DROPDOWN_LABELS,
} from "./tenantSettingsConstants";

// Admin -> Tenant Settings: the #tenant-settings tab of /user-management, whose
// body is app/src/components/common/settings/TenantSettings.jsx.
//
// Every control on this screen writes TENANT-WIDE configuration on a shared dev
// tenant — a feature flag, the tenant's display name, the log/trace label
// mappings the whole estate queries through. There is no per-record create here
// to give a unique suffix to and delete afterwards, so no test in this file ever
// saves a CHANGED value. The write path is still covered end to end, by saving
// the form unmodified and proving the values survive the round trip (see the
// save test); the two validation guards return before any network call, which is
// what makes the negative cases safe to run here at all. See "Follow-ups" in the
// PR for what a throwaway tenant would let this suite add.
//
// Both permission shapes are handled rather than assumed: every Save button on
// this screen is behind `canEdit` (tenants:Write), and a viewer without it gets
// a read-only banner instead. Tests that need to write assert the correct
// behaviour for whichever shape the run's user actually has.
test.beforeEach(async ({}, testInfo) => {
  testInfo.setTimeout(SPEC_TIMEOUT_MS);
});

test(
  "Tenant Settings sanity - open Admin, open the Tenant Settings tab, verify General, Label Mapping and Features are offered and General is the tab that opens",
  { tag: ["@dev", "@sanity", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await expect(ts.generalTab).toBeVisible();
    await expect(ts.labelMappingTab).toBeVisible();
    await expect(ts.featuresTab).toBeVisible();

    // activeTab is seeded to 'general' in component state, so General is the
    // landing tab without anything being clicked.
    await ts.expectTabSelected(ts.generalTab);
    await expect(ts.tenantNameInput).toBeVisible();
  },
);

test(
  "Tenant Settings - open General, verify the Tenant Name field holds the tenant this run is pointed at and the two self-onboarding fields follow the checkbox",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    // Tenant Name is hydrated from listUserTenants, matched against the session
    // tenant — so it is the run's own tenant, not whatever happens to be first.
    await expect(ts.tenantNameInput).toHaveValue(requiredTenantName(), { timeout: 60000 });

    // Allowed Domains and Default Auth Role are disabled unless the checkbox is
    // on (`disabled={!canEdit || !checkboxEnabled}`), which is the coupling this
    // step is about — asserted in whichever direction the tenant is configured.
    const selfOnboardingOn = await ts.selfOnboardingCheckbox.isChecked();
    if (selfOnboardingOn) {
      await expect(ts.allowedDomainsInput).toBeEnabled();
      await expect(ts.defaultAuthRoleInput).toBeEnabled();
    } else {
      await expect(ts.allowedDomainsInput).toBeDisabled();
      await expect(ts.defaultAuthRoleInput).toBeDisabled();
    }
  },
);

test(
  "Tenant Settings - open General, save the form unmodified, verify the saved-successfully toast and that the tenant name and log label mappings still hold their values after a reload",
  { tag: ["@dev", "@regression", "@functional", "@snackbar", "@crud"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    if (!(await ts.canEdit())) {
      // The correct product behaviour for a viewer without tenants:Write: no
      // Save anywhere on the screen, and the banner naming the missing grant.
      await expect(ts.readOnlyBanner).toBeVisible();
      await expect(ts.saveButton()).toHaveCount(0);
      return;
    }

    // Captured before the write so the post-reload comparison is against what
    // was really on screen, not against a value this test chose.
    const nameBefore = await ts.tenantNameInput.inputValue();
    await ts.openTab(ts.labelMappingTab);
    await expect(ts.logsSubTab).toBeVisible();
    // Read in parallel: these are independent property reads that drive no UI,
    // so serialising them only costs round-trips to the browser.
    const logValuesBefore = await Promise.all(LOG_LABEL_TESTIDS.map((testId) => ts.mapperField(testId).inputValue()));

    await ts.openTab(ts.generalTab);
    await ts.saveButton().click();
    await expect(toast(page, SAVE_SUCCESS_TEXT)).toBeVisible({ timeout: 60000 });

    // The toast alone proves nothing persisted — handleSaveSettings writes
    // log_labels, trace_labels and default_log_provider on every save, so a
    // reload is what shows the round trip actually landed.
    const reloaded = await discardUnsavedEdits(page);
    await expect(reloaded.tenantNameInput).toHaveValue(nameBefore, { timeout: 60000 });

    await reloaded.openTab(reloaded.labelMappingTab);
    await expect(reloaded.logsSubTab).toBeVisible();
    for (let i = 0; i < LOG_LABEL_TESTIDS.length; i++) {
      await expect(reloaded.mapperField(LOG_LABEL_TESTIDS[i])).toHaveValue(logValuesBefore[i]);
    }
  },
);

test(
  "Tenant Settings - open General, turn on self-onboarding, clear Allowed Domains, save, verify the empty-domains rejection and that a reload shows the checkbox unchanged",
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    if (!(await ts.canEdit())) {
      await expect(ts.selfOnboardingCheckbox).toBeDisabled();
      await expect(ts.readOnlyBanner).toBeVisible();
      return;
    }

    const checkboxWasOff = await ensureSelfOnboardingChecked(ts);
    await ts.allowedDomainsInput.fill("");
    await expect(ts.allowedDomainsInput).toHaveValue("");

    await ts.saveButton().click();
    await expect(toast(page, ERROR_EMPTY_DOMAINS)).toBeVisible({ timeout: 60000 });

    // This guard returns before upsertTenantAttributes, so nothing reached the
    // backend — a reload showing the checkbox back where it started is what
    // proves the rejected submit left no trace.
    const reloaded = await discardUnsavedEdits(page);
    if (checkboxWasOff) {
      await expect(reloaded.selfOnboardingCheckbox).not.toBeChecked();
    } else {
      await expect(reloaded.selfOnboardingCheckbox).toBeChecked();
    }
  },
);

test(
  `Tenant Settings - open General, turn on self-onboarding, enter a domain and the role "${INVALID_AUTH_ROLE}", save, verify the invalid-role rejection naming the two allowed roles`,
  { tag: ["@dev", "@regression", "@negative", "@validation"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    if (!(await ts.canEdit())) {
      await expect(ts.defaultAuthRoleInput).toBeDisabled();
      await expect(ts.readOnlyBanner).toBeVisible();
      return;
    }

    await ensureSelfOnboardingChecked(ts);
    // A non-empty domain is required to get past the first guard, so the
    // rejection under test is unambiguously the role one.
    await ts.allowedDomainsInput.fill("e2e-example.com");
    await ts.defaultAuthRoleInput.fill(INVALID_AUTH_ROLE);
    await expect(ts.defaultAuthRoleInput).toHaveValue(INVALID_AUTH_ROLE);

    await ts.saveButton().click();
    await expect(toast(page, ERROR_INVALID_ROLE)).toBeVisible({ timeout: 60000 });

    await discardUnsavedEdits(page);
  },
);

test(
  "Tenant Settings - open Label Mapping, verify the Logs sub-tab renders the three log mapper fields, then return to General",
  { tag: ["@dev", "@smoke", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.labelMappingTab);
    await ts.expectTabSelected(ts.logsSubTab);

    for (const testId of LOG_LABEL_TESTIDS) {
      await expect(ts.mapperField(testId)).toBeVisible();
    }

    // Back to General: the strip is component state, so this proves the body
    // swaps back rather than that a URL changed.
    await ts.openTab(ts.generalTab);
    await expect(ts.tenantNameInput).toBeVisible();
    await expect(ts.mapperField(LOG_LABEL_TESTIDS[0])).toHaveCount(0);
  },
);

test(
  "Tenant Settings - open Label Mapping, switch to Traces, expand the advanced trace fields, verify Trace ID appears, collapse it, verify Trace ID is hidden again",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.labelMappingTab);
    await ts.openTab(ts.tracesSubTab);

    for (const testId of TRACE_LABEL_TESTIDS) {
      await expect(ts.mapperField(testId)).toBeVisible();
    }

    // The disclosure auto-expands when any advanced field already holds a value
    // (hasAdvancedValue in TenantAccountCommonSettings), so which half of the
    // toggle is showing depends on the tenant's stored mapping, not on the code
    // under test. The button's own label is what says which state it is in.
    const advancedAlreadyOpen = (await ts.traceAdvancedToggle.textContent())?.includes(TRACE_ADVANCED_HIDE_TEXT) ?? false;
    if (!advancedAlreadyOpen) {
      await expect(ts.traceAdvancedToggle).toContainText(TRACE_ADVANCED_SHOW_TEXT);
      await ts.traceAdvancedToggle.click();
    }

    await expect(ts.traceAdvancedPanel).toBeVisible();
    await expect(ts.mapperField(TRACE_ADVANCED_FIRST_TESTID)).toBeVisible();
    await expect(ts.traceAdvancedToggle).toContainText(TRACE_ADVANCED_HIDE_TEXT);

    await ts.traceAdvancedToggle.click();
    await expect(ts.traceAdvancedPanel).toHaveCount(0);
    await expect(ts.mapperField(TRACE_ADVANCED_FIRST_TESTID)).toHaveCount(0);
    await expect(ts.traceAdvancedToggle).toContainText(TRACE_ADVANCED_SHOW_TEXT);
  },
);

test(
  "Tenant Settings - open Label Mapping, switch to Webhook alerts, verify the Subject Name, Namespace and Severity label pickers all render",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.labelMappingTab);
    await ts.openTab(ts.webhookSubTab);

    for (const label of WEBHOOK_DROPDOWN_LABELS) {
      await expect(ts.webhookDropdown(label)).toBeVisible();
    }

    // The Logs mapper belongs to a sibling sub-tab, so its absence is what shows
    // the body swapped rather than stacked.
    await expect(ts.mapperField(LOG_LABEL_TESTIDS[0])).toHaveCount(0);
  },
);

test(
  "Tenant Settings - open Features, verify the flag table renders its four columns and that a category tab's badge count matches the rows it filters the table down to",
  { tag: ["@dev", "@regression", "@functional", "@search"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.featuresTab);
    await expect(ts.featuresTable).toBeVisible({ timeout: 60000 });

    for (const header of FEATURE_TABLE_HEADERS) {
      await expect(ts.featuresTable.locator("th", { hasText: header }).first()).toBeVisible();
    }

    // Located by position, not by accessible name. Each feature-group tab renders
    // its count Chip as the MUI Tab's `icon`, which lands in the accessible name
    // ahead of the label — the All tab reads "24All", so matching on the label
    // alone resolves to nothing. Order is the stable contract instead:
    // TenantSettings.jsx builds tabOptions with the synthetic All group first,
    // then the catalog's categories. The text assertion below is what keeps that
    // positional assumption honest if the order ever changes.
    const groupTabs = ts.featureGroupTabList.getByRole("tab");
    expect(await groupTabs.count(), "the feature catalog rendered no category tabs beyond All").toBeGreaterThan(1);

    const allTab = groupTabs.first();
    await expect(allTab).toContainText(FEATURES_ALL_TAB);
    await ts.expectTabSelected(allTab);
    const allRows = await ts.featureRows.count();
    expect(allRows).toBeGreaterThan(0);

    // Every group tab carries a count Chip; the tabs partition the same catalog
    // the All tab lists, so a category's badge is the exact number of rows the
    // table should be filtered down to. Comparing the two is what proves the
    // strip filters rather than just re-rendering the full list.
    const categoryTab = groupTabs.nth(1);
    const badge = ((await categoryTab.textContent()) ?? "").match(/\d+/);
    expect(badge, "the first category tab rendered no count badge to compare rows against").not.toBeNull();

    const expectedRows = Number(badge?.[0]);
    await categoryTab.click();
    await ts.expectTabSelected(categoryTab);
    await expect(ts.featureRows).toHaveCount(expectedRows);
    expect(expectedRows).toBeLessThanOrEqual(allRows);
  },
);

test(
  "Tenant Settings - open Features, flip a feature switch without saving, reload, verify the switch is back to the state it was found in",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.featuresTab);
    await expect(ts.featuresTable).toBeVisible({ timeout: 60000 });

    // ds/Switch is disabled without tenants:Write, so a read-only viewer's
    // correct behaviour is that the row cannot be flipped at all.
    const firstToggle = ts.featureRows.first().getByRole("checkbox").first();
    await expect(firstToggle).toBeVisible({ timeout: 30000 });
    if (!(await ts.canEdit())) {
      await expect(firstToggle).toBeDisabled();
      return;
    }

    // The flag's identity, so the same row is re-read after the reload rather
    // than whichever row sorts first the second time.
    const featureName = (await firstToggle.getAttribute("aria-label")) ?? "";
    expect(featureName, "the first feature row's switch carries no aria-label to re-find it by").not.toEqual("");
    const wasChecked = await firstToggle.isChecked();

    await firstToggle.click();
    await expect(firstToggle).toBeChecked({ checked: !wasChecked });

    // Nothing is saved: this tab has no Cancel, and navigating away is how the
    // component discards pending edits (the tab body remounts on ErrorBoundary's
    // key). A reload is the same discard, and the flag coming back unchanged is
    // what proves the flip never reached the tenant.
    const reloaded = await discardUnsavedEdits(page);
    await reloaded.openTab(reloaded.featuresTab);
    await expect(reloaded.featuresTable).toBeVisible({ timeout: 60000 });
    await expect(reloaded.featureToggle(featureName)).toBeChecked({ checked: wasChecked });
  },
);

test(
  "Tenant Settings - open Features, turn on Show flag ids, verify the raw flag id column appears and the control flips to Hide flag ids",
  { tag: ["@dev", "@regression", "@functional"] },
  async ({ page }) => {
    const ts = await openTenantSettings(page);

    await ts.openTab(ts.featuresTab);
    await expect(ts.featuresTable).toBeVisible({ timeout: 60000 });

    // showFlagIds starts false, so the control opens as "Show flag ids".
    await expect(ts.flagIdsToggle).toHaveAttribute("aria-label", "Show flag ids");
    await ts.flagIdsToggle.click();
    await expect(ts.flagIdsToggle).toHaveAttribute("aria-label", "Hide flag ids");

    // Flag ids are the catalog's SCREAMING_SNAKE values, rendered only for rows
    // that also have a display name — so at least one has to appear, and the
    // pattern is what distinguishes them from the human-readable column.
    await expect(ts.featuresTable.getByText(/^[A-Z][A-Z0-9_]{4,}$/).first()).toBeVisible({ timeout: 30000 });

    await ts.flagIdsToggle.click();
    await expect(ts.flagIdsToggle).toHaveAttribute("aria-label", "Show flag ids");
  },
);
