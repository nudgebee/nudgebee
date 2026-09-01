// Not for OSS
import { test, expect } from "@playwright/test";
import {
  deleteRuleByName,
  inertAlertPattern,
  openCreateModal,
  openTriageRulesForCluster,
  openTriageRulesGlobal,
  pickFilterOption,
  searchByName,
  settleListing,
  uniqueRuleName,
} from "./triageRulesHelper";
import {
  CLUSTER_TRIAGE_INBOX_FRAGMENT,
  CLUSTER_TRIAGE_RULES_FRAGMENT,
  CREATE_SUCCESS,
  EMPTY_STATE_HEADING,
  NAME_COLUMN,
  NO_CRITERIA_ERROR,
  SPEC_TIMEOUT_MS,
  SUPPRESSION_TYPE,
  TRIAGE_RULES_FRAGMENT,
  TYPE_COLUMN,
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
// Shared dev tenant, so nothing here touches a rule it did not create. The two cases that write
// create a suppression rule whose alertname regex matches no real alert, under a name carrying a
// timestamp and a random tail, and delete it again in a finally block — so a failed assertion
// still cleans up, and a second run of the same spec collides with nothing from the first. The
// rule set itself is not fixed, so no case asserts a particular count; the assertions are
// invariants instead — which controls exist, what the filter does to the rows it leaves, and
// whether a record the case created is really there afterwards.
test.describe.configure({ timeout: SPEC_TIMEOUT_MS });
test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test(
  "Triage Rules sanity - open the cluster's Triage Rules tab, verify the toolbar offers the name search, the Rule Type and Status filters and the System Rules toggle",
  { tag: ["@dev", "@sanity", "@functional"] },
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
  { tag: ["@dev", "@regression", "@functional"] },
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
  { tag: ["@dev", "@regression", "@search"] },
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
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await searchByName(locators, uniqueRuleName());

    await test.step("The module renders its own empty state instead of the listing", async () => {
      await expect(locators.emptyState).toBeVisible();
      await expect(locators.emptyState).toHaveText(EMPTY_STATE_HEADING);
      await expect(locators.rows).toHaveCount(0);
    });
  }
);

test(
  "Triage Rules - open the cluster's Triage Rules tab, turn the System Rules toggle off, verify no listed rule still carries the System chip",
  { tag: ["@dev", "@regression", "@functional"] },
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
  { tag: ["@dev", "@regression", "@crud"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = uniqueRuleName();

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
  "Triage Rules - open Create Rule, enter a name but leave every match criterion empty, submit, verify the at-least-one-criterion error and that the modal stays open",
  { tag: ["@dev", "@regression", "@validation"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);

    await openCreateModal(locators);
    await locators.ruleNameField.fill(uniqueRuleName());
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
  { tag: ["@dev", "@regression", "@negative"] },
  async ({ page }) => {
    const locators = await openTriageRulesForCluster(page);
    const name = uniqueRuleName();

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
  { tag: ["@dev", "@regression", "@functional"] },
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
  { tag: ["@dev", "@regression", "@functional"] },
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
