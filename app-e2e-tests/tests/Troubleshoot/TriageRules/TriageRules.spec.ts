import { test, expect } from "@playwright/test";
import { resolveAccountId } from "../../Home/homeHelper";
// Targeted, not auto-capture: this app routes every operation through one shared /api/graphql
// endpoint, so auto-mode would also fail this test on an unrelated background GraphQL call
// (e.g. the header's notification/product-update badges) erroring — nothing to do with what
// this test actually exercises. Naming the exact operation avoids that.
import { waitForGraphQLAndValidate } from "../../utils/GraphQLNetworkWatcher";
import {
  deleteRuleByName,
  findOpenFixtureEventIds,
  inertAlertPattern,
  openCreateModal,
  openEditModal,
  openTriageRulesForCluster,
  openTriageRulesGlobal,
  pickFilterOption,
  reopenFixtureEvents,
  searchByName,
  settleListing,
} from "./triageRulesHelper";
import {
  APPLIED_TO_SUFFIX,
  APPLY_EXISTING_NEGATIVE_RULE_NAME,
  CANCEL_RULE_NAME,
  CLASSIFICATION_RULE_NAME,
  CLASSIFICATION_TYPE,
  CLUSTER_TRIAGE_INBOX_FRAGMENT,
  CLUSTER_TRIAGE_RULES_FRAGMENT,
  CREATE_SUCCESS,
  CROSS_SCREEN_RULE_NAME,
  CRUD_ROUND_TRIP_RULE_NAME,
  DISABLE_MENU_ITEM,
  DISABLED_STATUS,
  DUPLICATE_ACTION_LABEL,
  DUPLICATE_ACTION_VALUE,
  EMPTY_STATE_HEADING,
  ENABLE_MENU_ITEM,
  ENABLED_STATUS,
  EXPIRATION_RULE_NAME,
  EXPIRES_AT_VALUE,
  NAME_COLUMN,
  NO_CRITERIA_ERROR,
  NO_SUCH_RULE_NAME,
  REENABLE_BLOCKED_MESSAGE,
  RETRO_MATCH_FIXTURE_TITLE,
  RETRO_MATCH_RULE_NAME,
  SCORING_RULE_NAME,
  SCORING_TYPE,
  SEEDED_CLICKHOUSE_SCORING_RULE_NAME,
  SEEDED_DUPLICATE_SYSTEM_RULE_NAME,
  SPEC_TIMEOUT_MS,
  STATUS_FILTER_RULE_NAME,
  SUPPRESSION_TYPE,
  SYSTEM_RULE_DISABLE_MENU_ITEM,
  SYSTEM_RULE_ENABLE_MENU_ITEM,
  TRIAGE_RULES_FRAGMENT,
  TYPE_COLUMN,
  UPDATE_SUCCESS,
  VALIDATION_RULE_NAME,
  ZERO_EVENTS_PREVIEW,
} from "./triageRulesConstants";

// Triage Rules — app/src/components/triage/TriageRulesManager.tsx plus its create/edit modal,
// TriageRuleModal.tsx.
//
// Where each case runs is forced by the component, not chosen: Create Rule renders only when the
// manager is given an accountId AND the user holds write on it (TriageRulesManager.tsx:539), so
// every write case opens the module from the cluster's Troubleshoot section. The Account filter
// is the mirror image — it renders only in the unscoped /troubleshoot mount (:557) — and gets the
// one case that asserts that contract.
//
// Shared dev tenant, so nothing here touches a rule it did not create. Every case that writes
// creates its rule under one of the fixed names in triageRulesConstants.ts and deletes it again
// in a finally block — so a failed assertion still cleans up, and each name is unique enough
// across this file that no two tests' searches can resolve to each other's row. The rule set
// itself is not fixed, so no case asserts a particular count; the assertions are invariants
// instead — which controls exist, what the filter does to the rows it leaves, and whether a
// record the case created is really there afterwards.
test.describe.configure({ timeout: SPEC_TIMEOUT_MS });
test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test(
  "Triage Rules sanity - open the cluster's Triage Rules tab, verify the toolbar offers the name search, the Rule Type and Status filters and the System Rules toggle",
  { tag: ["@dev", "@test", "@sanity", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await test.step("The deep link lands on Triage Rules rather than the section's first sub-tab", async () => {
      await expect(page).toHaveURL(new RegExp(`#${CLUSTER_TRIAGE_RULES_FRAGMENT}\\b`));
      await expect(locators.listing).toBeVisible();
    });

    await test.step("Every toolbar control the module declares is rendered", async () => {
      await expect(locators.search).toBeVisible();
      await expect(locators.ruleTypeFilter).toBeVisible();
      await expect(locators.statusFilter).toBeVisible();
      await expect(locators.systemRulesSwitch).toBeAttached();
    });

    await test.step("The System Rules toggle starts on, which is what puts system rules in the default listing", async () => {
      await expect(locators.systemRulesSwitch).toBeChecked();
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, filter Rule Type by Suppression, verify every listed rule's Type cell reads Suppression",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await pickFilterOption(locators, locators.ruleTypeFilter, SUPPRESSION_TYPE);

    await test.step("No row of another type survives the filter", async () => {
      // The tenant may hold no suppression rule at all, in which case the module swaps the whole
      // table for its empty state — a legitimate outcome of the same filter, so the assertion has
      // to admit both shapes rather than demand rows.
      const emptied = await locators.emptyState
        .waitFor({ state: "visible", timeout: 15000 })
        .then(() => true)
        .catch(() => false);

      if (emptied) {
        await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING);
        return;
      }

      // Polled over the whole column rather than asserted as "no off-type row remains". A
      // negative count is satisfied by an empty table exactly as well as by a correctly
      // filtered one, so it would pass vacuously the moment the rows blank out mid-refilter.
      // Requiring at least one cell AND every cell on-type is what makes the pass mean something.
      const typeCells = locators.cellsInColumn(TYPE_COLUMN);
      await expect
        .poll(async () => {
          const texts = await typeCells.allInnerTexts();
          return texts.length > 0 && texts.every((text) => text.trim() === SUPPRESSION_TYPE);
        })
        .toBe(true);
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, search for a listed rule by its own name, verify that rule is still listed and the rows narrow to it",
  { tag: ["@dev", "@test", "@regression", "@search", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    const firstRowCount = await locators.rows.count();
    test.skip(firstRowCount === 0, "The dev tenant holds no triage rule to search for on this account");

    const name = ((await locators.cellsInColumn(NAME_COLUMN).first().innerText()) ?? "").split("\n")[0].trim();
    expect(name, "The first row's Name cell was empty, so there is no term to search with").not.toEqual("");

    await searchByName(locators, name);

    await test.step("The rule searched for is still listed, and nothing that fails the term is", async () => {
      // The term comes off the first row, so that row is already on screen before the search
      // runs — asserting it is visible would pass against pre-filter state and prove nothing.
      // Polling the whole Name column instead states the real outcome: rows remain, and every
      // one of them carries the term. `includes` rather than equality because a system rule's
      // Name cell also holds its "System" chip.
      const nameCells = locators.cellsInColumn(NAME_COLUMN);
      await expect
        .poll(async () => {
          const texts = await nameCells.allInnerTexts();
          return texts.length > 0 && texts.every((text) => text.includes(name));
        })
        .toBe(true);
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, search a name no rule holds, verify the No Data Available empty state replaces the table",
  { tag: ["@dev", "@test", "@regression", "@negative", "@search", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await searchByName(locators, NO_SUCH_RULE_NAME);

    await test.step("The module renders its own empty state instead of the listing", async () => {
      await expect(locators.emptyState).toBeVisible();
      await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING);
      await expect(locators.rows).toHaveCount(0);
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, turn the System Rules toggle off, verify no listed rule still carries the System chip",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await expect(locators.systemRulesSwitch).toBeChecked();
    // Without at least one System chip on screen first, "no chips afterwards" is true however the
    // toggle behaves, and the case would report a pass having demonstrated nothing. Whether this
    // account carries a system rule is tenant data, not the code under test, so the honest answer
    // to none being present is to skip rather than to assert vacuously.
    const chipsBefore = await locators.systemChips().count();
    test.skip(chipsBefore === 0, "No system rule is listed on this account, so hiding them cannot be observed");

    await locators.systemRulesSwitch.uncheck();
    await expect(locators.systemRulesSwitch).not.toBeChecked();
    await settleListing(locators);

    await test.step("Every remaining row is a user rule", async () => {
      await expect(locators.systemChips()).toHaveCount(0);
    });

    await test.step("Turning it back on restores them", async () => {
      await locators.systemRulesSwitch.check();
      await expect(locators.systemRulesSwitch).toBeChecked();
      await settleListing(locators);
    });
  }
);

test(
  "Triage Rules - open Create Rule, enter a unique name and an alert-name pattern, create the rule, verify it is listed afterwards, then delete it and verify it is gone",
  { tag: ["@dev", "@test", "@regression", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = CRUD_ROUND_TRIP_RULE_NAME;

    try {
      await openCreateModal(locators);
      await locators.ruleNameField.fill(name);
      await locators.alertNameField.fill(inertAlertPattern(name));
      await locators.modalSubmit.click();

      await test.step("The create is accepted and the modal closes", async () => {
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("The rule is really in the listing, not just announced", async () => {
        // A success toast only says the mutation returned success. Searching the refetched
        // listing for the name is what proves the record persisted and came back from the
        // server — the toast alone would pass even if the row never arrived.
        await searchByName(locators, name);
        await expect(locators.rowByName(name)).toBeVisible({ timeout: 60000 });
      });
    } finally {
      // Runs even when an assertion above failed, so a half-finished case cannot leave a rule
      // behind on the shared tenant.
      await deleteRuleByName(locators, name);
    }

    await test.step("The deleted rule is gone from the listing", async () => {
      await searchByName(locators, name);
      await expect(locators.rowByName(name)).toHaveCount(0);
      await expect(locators.emptyState).toBeVisible();
    });
  }
);

test(
  "Triage Rules - create a rule from the cluster view, verify it's visible in the global unscoped view, edit it through the row menu, change the alert-name pattern, save, verify the listing reflects the new pattern",
  { tag: ["@dev", "@test", "@regression", "@crud", "@oss"] },
  async ({ page }) => {
    const name = CROSS_SCREEN_RULE_NAME;
    const updatedPattern = inertAlertPattern(`${name}-updated`);

    try {
      let locators = await openTriageRulesForCluster(page);

      await test.step("Create a throwaway rule on the cluster-scoped view", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("The rule created on the cluster view is also visible on the global unscoped view", async () => {
        const globalLocators = await openTriageRulesGlobal(page);
        await searchByName(globalLocators, name);
        await expect(globalLocators.rowByName(name)).toBeVisible({ timeout: 30000 });
      });

      // Back on the cluster-scoped view for the write actions the global view withholds.
      locators = await openTriageRulesForCluster(page);

      await test.step("Edit opens on the rule's own pre-filled values", async () => {
        await openEditModal(locators, name);
      });

      await test.step("Changing the alert-name pattern and saving updates the rule", async () => {
        await locators.alertNameField.fill(updatedPattern);
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(UPDATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("The listing reflects the new pattern, not the old one", async () => {
        await searchByName(locators, name);
        await expect(locators.rowByName(name)).toContainText(updatedPattern);
      });
    } finally {
      // Re-navigate rather than reuse whatever locators were in scope when a failure hit —
      // the global-view step above can leave the page somewhere delete's own locators were
      // never built for. Starting fresh here is what keeps cleanup reachable either way.
      const cleanupLocators = await openTriageRulesForCluster(page);
      await deleteRuleByName(cleanupLocators, name);
    }
  }
);

test(
  "Triage Rules - open Create Rule, create a rule, filter Status by Enabled, verify it's listed, disable it through the row menu, filter Status by Disabled, verify the disabled rule is listed, then verify re-enabling is blocked",
  { tag: ["@dev", "@test", "@regression", "@functional", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = STATUS_FILTER_RULE_NAME;

    try {
      await test.step("Create a throwaway rule, enabled by default", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      // Searched once, up front — the term stays applied across every status pick below (the
      // two filters combine, they don't replace each other), so this is the only search needed
      // for the rest of the test to keep narrowing to just this rule.
      await searchByName(locators, name);

      await test.step("Filtering Status by Enabled lists the rule", async () => {
        await pickFilterOption(locators, locators.statusFilter, ENABLED_STATUS);
        await expect(locators.rowByName(name)).toBeVisible();
      });

      await test.step("Disabling it through the row menu is immediate — no confirmation step", async () => {
        const row = locators.rowByName(name);
        await locators.rowMenuTrigger(row).click();
        await locators.rowMenuItem(DISABLE_MENU_ITEM).click();
      });

      await test.step("Filtering Status by Disabled now lists the same rule, showing Disabled", async () => {
        // The row just fell out of the Enabled filter still active from above (optimistic
        // local update, no refetch) — switching the filter is what brings it back into view.
        await pickFilterOption(locators, locators.statusFilter, DISABLED_STATUS);
        await expect(locators.rowByName(name)).toContainText(DISABLED_STATUS);
      });

      await test.step("Re-enabling from the row menu is blocked, not silently ignored", async () => {
        const row = locators.rowByName(name);
        await locators.rowMenuTrigger(row).click();
        await locators.rowMenuItem(ENABLE_MENU_ITEM).click();
        await expect(locators.toastRegion).toContainText(REENABLE_BLOCKED_MESSAGE);
        // The block has to actually refuse the change, not just say so — still Disabled here is
        // what proves the info toast isn't papering over a mutation that happened anyway.
        await expect(locators.rowByName(name)).toContainText(DISABLED_STATUS);
      });
    } finally {
      await deleteRuleByName(locators, name);
    }
  }
);

test(
  "Triage Rules - open Create Rule, pick Scoring rule type, set a namespace match and a score adjustment, create the rule, verify the listing shows the adjustment",
  { tag: ["@dev", "@test", "@regression", "@functional", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = SCORING_RULE_NAME;
    const scoreAdjustment = "15";

    try {
      await test.step("Create a Scoring rule with a namespace match and a score adjustment", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.ruleTypeTab(SCORING_TYPE).click();
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.scoreAdjustmentField.fill(scoreAdjustment);
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("The listing shows the rule as Scoring, carrying the adjustment", async () => {
        await searchByName(locators, name);
        const row = locators.rowByName(name);
        await expect(row).toBeVisible({ timeout: 60000 });
        await expect(row).toContainText(SCORING_TYPE);
        // The Action cell prints the raw stored action_value JSON rather than a formatted
        // string (confirmed against the seeded system scoring rules' own rows), so this checks
        // the adjustment reached the record rather than pinning an unconfirmed exact format.
        await expect(row).toContainText(scoreAdjustment);
      });
    } finally {
      await deleteRuleByName(locators, name);
    }
  }
);

test(
  "Triage Rules - open Create Rule, pick Classification rule type, choose the Duplicate action, create the rule, verify the listing shows the Duplicate classification",
  { tag: ["@dev", "@test", "@regression", "@functional", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = CLASSIFICATION_RULE_NAME;

    try {
      await test.step("Create a Classification rule with the Duplicate action", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.ruleTypeTab(CLASSIFICATION_TYPE).click();
        // Switching to the Classification tab defaults the action to False Positive, so this
        // explicit click proves the radio selection itself works rather than accepting whatever
        // the tab-switch default happened to leave selected.
        await locators.actionRadio(DUPLICATE_ACTION_LABEL).click();
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("The listing shows the rule as Classification, carrying the Duplicate action", async () => {
        await searchByName(locators, name);
        const row = locators.rowByName(name);
        await expect(row).toBeVisible({ timeout: 60000 });
        await expect(row).toContainText(CLASSIFICATION_TYPE);
        await expect(row).toContainText(DUPLICATE_ACTION_VALUE);
      });
    } finally {
      await deleteRuleByName(locators, name);
    }
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, disable the seeded 'System: Auto-mark Duplicates' rule for this account via its row menu, verify it shows Disabled (Override), then re-enable it and verify it returns to Enabled",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    try {
      await test.step("Disable the seeded system rule for this account and confirm it flips to Disabled (Override)", async () => {
        await searchByName(locators, SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
        let row = locators.rowByName(SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
        await expect(row).toContainText(ENABLED_STATUS);
        await locators.rowMenuTrigger(row).click();
        await expect(locators.rowMenuItem(SYSTEM_RULE_DISABLE_MENU_ITEM)).toBeVisible();
        await locators.rowMenuItem(SYSTEM_RULE_DISABLE_MENU_ITEM).click();

        // No toast to wait on here — handleToggleSystemRuleOverride applies the change
        // optimistically and only toasts on failure, so the row's own text is the wait.
        row = locators.rowByName(SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
        await expect(row).toContainText(DISABLED_STATUS);
        await locators.rowMenuTrigger(row).click();
        await expect(locators.rowMenuItem(SYSTEM_RULE_ENABLE_MENU_ITEM)).toBeVisible();
      });

      await test.step("Re-enable it and confirm it returns to Enabled", async () => {
        await locators.rowMenuItem(SYSTEM_RULE_ENABLE_MENU_ITEM).click();

        const row = locators.rowByName(SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
        await expect(row).toContainText(ENABLED_STATUS);
        await locators.rowMenuTrigger(row).click();
        await expect(locators.rowMenuItem(SYSTEM_RULE_DISABLE_MENU_ITEM)).toBeVisible();
        await page.keyboard.press("Escape");
      });
    } finally {
      await searchByName(locators, SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
      const row = locators.rowByName(SEEDED_DUPLICATE_SYSTEM_RULE_NAME);
      // A missing row is an expected cleanup-path outcome — absence must come back as false
      // rather than throw. Raced against the empty state instead of a flat timeout, so the
      // negative case resolves as soon as it's observable rather than waiting one out.
      const present = await Promise.race([
        row.waitFor({ state: "visible" }).then(() => true),
        locators.emptyState.waitFor({ state: "visible" }).then(() => false),
      ]);
      if (!present) return;

      await locators.rowMenuTrigger(row).click();
      // A menu already back to "Disable for this Account" means the test's own round trip
      // completed and there is nothing to restore — that is the normal, expected outcome here.
      const stillOverridden = await locators
        .rowMenuItem(SYSTEM_RULE_ENABLE_MENU_ITEM)
        .isVisible()
        .catch(() => false);
      if (stillOverridden) {
        await locators.rowMenuItem(SYSTEM_RULE_ENABLE_MENU_ITEM).click();
      } else {
        await page.keyboard.press("Escape");
      }
    }
  }
);

test(
  "Triage Rules - open Create Rule, set an alert-name match, expand Expiration and set an Expires At date, create the rule, reopen it in Edit, verify the Expires At value persisted",
  { tag: ["@dev", "@test", "@regression", "@functional", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = EXPIRATION_RULE_NAME;

    try {
      await test.step("Create a rule with an expiration date set", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.expirationToggle.click();
        await locators.expiresAtField.fill(EXPIRES_AT_VALUE);
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        await expect(locators.ruleModal).toBeHidden();
      });

      await test.step("Reopening the rule in Edit shows the same Expires At value", async () => {
        // The listing has no column for this field, so re-reading it through Edit is the only
        // observable proof it actually persisted rather than just being accepted by the form.
        await openEditModal(locators, name);
        await locators.expirationToggle.click();
        await expect(locators.expiresAtField).toHaveValue(EXPIRES_AT_VALUE);
        await locators.modalCancel.click();
        await expect(locators.ruleModal).toBeHidden();
      });
    } finally {
      await deleteRuleByName(locators, name);
    }
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, expand the seeded 'Service Tier 1: clickhouse' scoring rule, verify the Matched Events panel shows real matched events",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await test.step("The seeded rule carries real matches to verify against", async () => {
      await searchByName(locators, SEEDED_CLICKHOUSE_SCORING_RULE_NAME);
      const row = locators.rowByName(SEEDED_CLICKHOUSE_SCORING_RULE_NAME);
      await expect(row).toBeVisible();
      // Fails loudly rather than skipping — a 0-match run means this test has nothing to prove,
      // which is itself a signal worth seeing rather than hiding.
      const matchesText = (await (await locators.matchesCellFor(row)).textContent())?.trim() ?? "";
      expect(Number(matchesText)).toBeGreaterThan(0);
    });

    await test.step("Expanding the row shows the Matched Events panel with real rows", async () => {
      const row = locators.rowByName(SEEDED_CLICKHOUSE_SCORING_RULE_NAME);
      await locators.expandRowButton(row).click();
      await expect(locators.matchedEventsPanel).toContainText(/Showing \d+ of \d+ matched events/);
      await expect(locators.matchedEventsPanel.locator("tbody tr").first()).toBeVisible();
    });
  }
);

test(
  "Triage Rules - open Create Rule, enable Apply to Existing Events with an inert alert-name pattern, verify the preview shows 0 events will be affected, create the rule, verify the success toast carries no Applied-to suffix",
  { tag: ["@dev", "@test", "@regression", "@negative", "@crud", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = APPLY_EXISTING_NEGATIVE_RULE_NAME;

    try {
      await test.step("An inert pattern previews as matching 0 events", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.alertNameField.fill(inertAlertPattern(name));
        await locators.applyToExistingToggle.click();
        await expect(locators.applyToExistingPreview).toHaveText(ZERO_EVENTS_PREVIEW);
      });

      await test.step("Creating it raises the base success toast with no Applied-to suffix", async () => {
        await locators.modalSubmit.click();
        await expect(locators.toastRegion).toContainText(CREATE_SUCCESS);
        // The actual claim under test: 0 matched events must never grow an "Applied to N
        // existing events" suffix onto the toast (TriageRuleModal.tsx:284-285 only appends it
        // when events_to_update is truthy, and 0 is falsy).
        await expect(locators.toastRegion).not.toContainText(APPLIED_TO_SUFFIX);
        await expect(locators.ruleModal).toBeHidden();
      });
    } finally {
      await deleteRuleByName(locators, name);
    }
  }
);

test(
  "Triage Rules - open Create Rule, enter a name but leave every match criterion empty, submit, verify the at-least-one-criterion error and that the modal stays open",
  { tag: ["@dev", "@test", "@regression", "@validation", "@negative", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await openCreateModal(locators);
    await locators.ruleNameField.fill(VALIDATION_RULE_NAME);
    await locators.modalSubmit.click();

    await test.step("The rejection names the missing criterion", async () => {
      await expect(locators.toastRegion).toContainText(NO_CRITERIA_ERROR);
    });

    await test.step("Nothing was submitted — the modal is still open on the entered values", async () => {
      // handleSubmit returns before it issues any request, so the modal staying open is the
      // observable proof no rule was created (TriageRuleModal.tsx:231).
      await expect(locators.ruleModal).toBeVisible();
      await expect(locators.modalSubmit).toBeEnabled();
    });
  }
);

test(
  "Triage Rules - open Create Rule, enter a name, cancel, verify the modal closes and no rule under that name was created",
  { tag: ["@dev", "@test", "@regression", "@negative", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = CANCEL_RULE_NAME;

    await openCreateModal(locators);
    await locators.ruleNameField.fill(name);
    await locators.modalCancel.click();

    await test.step("The modal closes without saving", async () => {
      await expect(locators.ruleModal).toBeHidden();
    });

    await test.step("The cancelled name reaches no listing", async () => {
      await searchByName(locators, name);
      await expect(locators.rowByName(name)).toHaveCount(0);
      await expect(locators.emptyState).toBeVisible();
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, switch to the Triage Inbox sub-tab and back, verify the hash returns to triage-rules and the listing re-renders",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await test.step("Leaving for the neighbouring sub-tab changes the hash and unmounts the listing", async () => {
      await locators.triageInboxSubTab.click();
      await expect(page).toHaveURL(new RegExp(`#${CLUSTER_TRIAGE_INBOX_FRAGMENT}\\b`));
      await expect(locators.listing).toBeHidden();
    });

    await test.step("Coming back re-renders Triage Rules rather than leaving a blank pane", async () => {
      await locators.clusterTriageRulesSubTab.click();
      await expect(page).toHaveURL(new RegExp(`#${CLUSTER_TRIAGE_RULES_FRAGMENT}\\b`));
      await settleListing(locators);
      await expect(locators.listing).toBeVisible();
      await expect(locators.search).toBeVisible();
    });
  }
);

test(
  "Triage Rules - open the module from Troubleshoot instead of a cluster, verify the unscoped view offers the Account filter and withholds Create Rule",
  { tag: ["@dev", "@test", "@regression", "@functional", "@oss"] },
  async ({ page }) => {
    const locators = await openTriageRulesGlobal(page);

    await test.step("The deep link opens the Triage Rules sub-tab of All Events", async () => {
      await expect(page).toHaveURL(new RegExp(`#${TRIAGE_RULES_FRAGMENT.replace("/", "\\/")}\\b`));
      await expect(locators.listing).toBeVisible();
    });

    await test.step("Only the unscoped view offers an Account filter", async () => {
      await expect(locators.accountFilter).toBeVisible();
    });

    await test.step("Create Rule is withheld, because this view has no account to create against", async () => {
      // Not an oversight in the test: the button is gated on `accountId && hasWriteAccess`
      // (TriageRulesManager.tsx:539) and this mount passes no accountId, so its absence here is
      // the contract that keeps a rule from being created without a target account.
      await expect(locators.createButton).toHaveCount(0);
    });
  }
);

test(
  "Triage Rules - apply a rule to a real open backlog, verify the Matches count is recorded and consistent across the scoped and global views, then re-save the rule and verify the count does not double",
  { tag: ["@dev", "@test", "@regression", "@functional", "@crud"] },
  async ({ page }) => {
    const accountId = await resolveAccountId(page);
    const fixtureEventIds = await findOpenFixtureEventIds(page, accountId);
    // Organic PagerDuty test-alert traffic, not a seeded fixture — see the constant's own
    // comment. An empty backlog means this run has nothing to prove, so it skips rather than
    // failing on data this suite does not control.
    test.skip(fixtureEventIds.length === 0, `No open "${RETRO_MATCH_FIXTURE_TITLE}" events on this account right now`);

    const parseCount = (text: string): number => {
      const match = text.match(/\d+/);
      expect(match, `Expected a number in "${text}"`).toBeTruthy();
      return Number(match![0]);
    };

    let locators = await openTriageRulesForCluster(page);
    const name = RETRO_MATCH_RULE_NAME;
    let matchCount = 0;

    try {
      await test.step("Applying to the real backlog previews and records a positive match count", async () => {
        await openCreateModal(locators);
        await locators.ruleNameField.fill(name);
        await locators.alertNameField.fill(`^${RETRO_MATCH_FIXTURE_TITLE}$`);
        await locators.applyToExistingToggle.click();
        await expect(locators.applyToExistingPreview).toHaveText(/^\d+ events? will be affected$/);
        matchCount = parseCount((await locators.applyToExistingPreview.textContent()) ?? "");
        expect(matchCount).toBeGreaterThan(0);

        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.modalSubmit.click();
            await expect(locators.toastRegion).toContainText(`Applied to ${matchCount} existing events`);
          },
          { testName: "TriageRules retro-match: create", operationNames: "EventCreateTriageRule", timeoutMs: 5000 }
        );
        await expect(locators.ruleModal).toBeHidden();

        await searchByName(locators, name);
        const row = locators.rowByName(name);
        await expect(row).toBeVisible({ timeout: 60000 });
        await expect(await locators.matchesCellFor(row)).toHaveText(String(matchCount));
      });

      await test.step("The global unscoped view shows the same count, not a scoped-view artifact", async () => {
        locators = await openTriageRulesGlobal(page);
        await searchByName(locators, name);
        const row = locators.rowByName(name);
        await expect(row).toBeVisible({ timeout: 30000 });
        await expect(await locators.matchesCellFor(row)).toHaveText(String(matchCount));

        await locators.expandRowButton(row).click();
        await expect(locators.matchedEventsPanel).toContainText(new RegExp(`of ${matchCount} matched events`));
        await expect(locators.matchedEventsPanel.locator("tbody tr").first()).toBeVisible();
      });

      await test.step("Re-saving the rule resends Apply to Existing Events, but the count does not double", async () => {
        locators = await openTriageRulesForCluster(page);
        await openEditModal(locators, name);
        // Carried over from creation (TriageRuleModal.tsx:142 seeds it from rule.apply_to_existing)
        // — confirming this before saving is what proves the resend below is real, not assumed.
        await expect(locators.applyToExistingToggle).toBeChecked();
        await waitForGraphQLAndValidate(
          page,
          async () => {
            await locators.modalSubmit.click();
            await expect(locators.toastRegion).toContainText(UPDATE_SUCCESS);
          },
          { testName: "TriageRules retro-match: re-save", operationNames: "EventUpdateTriageRule", timeoutMs: 5000 }
        );
        // The actual claim: every event this rule already matched hits ON CONFLICT DO NOTHING on
        // the second apply, so events_to_update is 0 and no Applied-to suffix grows onto the toast.
        await expect(locators.toastRegion).not.toContainText(APPLIED_TO_SUFFIX);

        await searchByName(locators, name);
        const row = locators.rowByName(name);
        await expect(await locators.matchesCellFor(row)).toHaveText(String(matchCount));
      });
    } finally {
      const cleanupLocators = await openTriageRulesForCluster(page);
      await deleteRuleByName(cleanupLocators, name);
      await reopenFixtureEvents(page, fixtureEventIds);
    }
  }
);
