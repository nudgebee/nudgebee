// Not for OSS

import { test, expect } from "@playwright/test";
import { OwnershipLocators } from "./ownershipLocators";
import {
  ruleName,
  listRules,
  findRule,
  deleteRuleById,
  sweepE2ERules,
  firstActiveOwner,
  seedLabelRule,
  withApiPage,
  openOwnership,
  selectOption,
  expectScopeOptions,
  pickFirstOwner,
  searchInOpenPicker,
  type OwnerRef,
} from "./ownershipHelper";
import {
  SPEC_TIMEOUT_MS,
  LISTING_TITLE,
  DOMAIN_KUBERNETES,
  DOMAIN_CLOUD,
  K8S_SCOPE_LABELS,
  CLOUD_SCOPE_LABELS,
  ADD_MODAL_TITLE,
  EDIT_MODAL_TITLE,
  NO_RESULTS_TEXT,
  UNMATCHABLE_QUERY,
} from "./ownershipConstants";

// Admin -> Ownership: the ownership-rules tab of /user-management.
//
// This suite runs against a SHARED dev tenant, so every rule it creates carries
// the e2e-own- prefix plus a timestamp suffix, and the prefix is swept by API
// before and after the run. No pre-existing rule, user, group or account is ever
// edited or deleted — the assertions below only ever read those.
//
// BASE_URL drives playwright.config's baseURL, which every relative goto here
// depends on, so it is asserted once up front rather than failing later as a
// navigation error that names no cause.
const BASE_URL = process.env.BASE_URL || "";
if (!BASE_URL) {
  throw new Error("BASE_URL is not set — add it to .env / .env.dev");
}

test.beforeEach(() => {
  test.setTimeout(SPEC_TIMEOUT_MS);
});

test.describe("Admin -> Ownership: ownership rules", () => {
  let owner: OwnerRef;

  test.beforeAll(async ({ browser }) => {
    await withApiPage(browser, async (page) => {
      owner = await firstActiveOwner(page);
      await sweepE2ERules(page);
    });
  });

  test.afterAll(async ({ browser }) => {
    await withApiPage(browser, (page) => sweepE2ERules(page));
  });

  test(
    "Ownership sanity - open /user-management#ownership, verify the Ownership tab is selected and the Ownership rules listing renders with its table and Add rule action",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const locators = await openOwnership(page);

      await test.step("The module opens on its own tab of the admin strip", async () => {
        await expect(locators.ownershipTab).toHaveAttribute("data-tab-selected", "true");
      });

      await test.step("The listing renders its heading and its rules table", async () => {
        await expect(locators.listingTitle).toHaveText(LISTING_TITLE);
        await expect(locators.rulesTable).toBeVisible();
      });

      await test.step("The create control is on the toolbar", async () => {
        // Rendered only when canManage('ownership','Write') passes, so a run user
        // without that grant fails here with a reason rather than a locator error.
        await expect(locators.addRuleBtn, "Add rule is gated on ownership:Write — check the run user's role").toBeVisible();
      });
    }
  );

  test(
    "Ownership - open Add rule, enter a name, keep the Label scope, enter a label key and value, select an owner, create, verify the rule is listed and the rules API returns it",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const locators = await openOwnership(page);
      const name = ruleName("create");
      const labelKey = "team";
      const labelValue = "payments";

      await test.step("The add form opens on its own title", async () => {
        await locators.addRuleBtn.click();
        await expect(locators.ruleDialog).toBeVisible();
        await expect(locators.ruleDialog).toContainText(ADD_MODAL_TITLE);
      });

      await test.step("Fill the rule the Label scope needs", async () => {
        await locators.nameInput.fill(name);
        await expect(locators.nameInput).toHaveValue(name);
        await locators.matchKeyInput.fill(labelKey);
        await locators.matchValueInput.fill(labelValue);
        await expect(locators.matchValueInput).toHaveValue(labelValue);
        await pickFirstOwner(locators);
      });

      await test.step("Create is enabled once the form is complete, and submits", async () => {
        await expect(locators.saveBtn).toBeEnabled();
        await locators.saveBtn.click();
        await expect(locators.ruleDialog).toBeHidden();
      });

      await test.step("The new rule is on the listing with its match rendered", async () => {
        const row = locators.ruleRow(name);
        await expect(row).toBeVisible();
        await expect(row).toContainText(`${labelKey} = ${labelValue}`);
      });

      await test.step("It really persisted — the rules API returns it, not just the screen", async () => {
        const saved = await findRule(page, name);
        expect(saved, `rule "${name}" is on screen but absent from ownership_list_rules`).toBeTruthy();
        expect(saved?.match_scope).toBe("label");
        expect(saved?.match_key).toBe(labelKey);
        expect(saved?.match_value).toBe(labelValue);
        expect(saved?.owner_id).toBeTruthy();
        expect(saved?.enabled).toBe(true);
      });
    }
  );

  test(
    "Ownership - open Add rule, enter a label key and value but leave the name and owner empty, verify Create stays disabled until both are supplied",
    { tag: ["@dev", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      const locators = await openOwnership(page);
      const name = ruleName("validation");

      await locators.addRuleBtn.click();
      await expect(locators.ruleDialog).toBeVisible();

      await test.step("An untouched form cannot be submitted", async () => {
        await expect(locators.saveBtn).toBeDisabled();
      });

      await test.step("Match fields alone are not enough — the name is still missing", async () => {
        await locators.matchKeyInput.fill("team");
        await locators.matchValueInput.fill("platform");
        await expect(locators.matchValueInput).toHaveValue("platform");
        await expect(locators.saveBtn, "Create must stay disabled while the required Name is empty").toBeDisabled();
      });

      await test.step("Adding the name is still not enough — the owner is required too", async () => {
        await locators.nameInput.fill(name);
        await expect(locators.nameInput).toHaveValue(name);
        await expect(locators.saveBtn, "Create must stay disabled while no owner is selected").toBeDisabled();
      });

      await test.step("Selecting an owner is what releases the form", async () => {
        await pickFirstOwner(locators);
        await expect(locators.saveBtn).toBeEnabled();
      });

      await test.step("Nothing was written while the form was invalid", async () => {
        await locators.cancelRuleBtn.click();
        await expect(locators.ruleDialog).toBeHidden();
        expect(await findRule(page, name)).toBeUndefined();
      });
    }
  );

  test(
    "Ownership - open the row menu on a seeded rule, edit its name, save, verify the renamed rule replaces the old name in the listing and in the rules API",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const original = ruleName("edit-before");
      const renamed = ruleName("edit-after");
      const id = await seedLabelRule(page, original, owner);

      try {
        const locators = await openOwnership(page);
        await expect(locators.ruleRow(original)).toBeVisible();

        await test.step("The row's kebab offers Edit and opens the rule in the edit form", async () => {
          await locators.rowMenuBtn(original).click();
          await locators.rowMenuItem("edit").click();
          await expect(locators.ruleDialog).toBeVisible();
          await expect(locators.ruleDialog).toContainText(EDIT_MODAL_TITLE);
        });

        await test.step("The form opens prefilled with the rule under edit", async () => {
          await expect(locators.nameInput).toHaveValue(original);
        });

        await test.step("Rename and save", async () => {
          await locators.nameInput.fill(renamed);
          await expect(locators.nameInput).toHaveValue(renamed);
          await locators.saveBtn.click();
          await expect(locators.ruleDialog).toBeHidden();
        });

        await test.step("The listing shows the new name and no longer the old one", async () => {
          await expect(locators.ruleRow(renamed)).toBeVisible();
          await expect(locators.rulesTable.locator("tr").filter({ hasText: original })).toHaveCount(0);
        });

        await test.step("The rename persisted against the same rule id, not a new rule", async () => {
          const saved = await findRule(page, renamed);
          expect(saved?.id, "editing must update the existing rule rather than insert a second one").toBe(id);
          expect(await findRule(page, original)).toBeUndefined();
        });
      } finally {
        // Best-effort cleanup: absence is expected when the test already removed
        // the rule, and the afterAll sweep is the backstop either way.
        await deleteRuleById(page, id).catch(() => undefined);
      }
    }
  );

  test(
    "Ownership - open Add rule, open the Owner picker, search a prefix of the first offered owner, verify only matching owners remain and an unmatchable query shows No results found",
    { tag: ["@dev", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await openOwnership(page);

      await locators.addRuleBtn.click();
      await expect(locators.ruleDialog).toBeVisible();

      await test.step("The picker opens with the tenant's owner directory listed", async () => {
        await locators.ownerSelect.click();
        await expect(locators.openListbox).toBeVisible();
        // The directory is fetched lazily when the first picker mounts.
        await expect(locators.listboxOptions.first()).toBeVisible({ timeout: 60000 });
      });

      // A prefix taken from a real option rather than a hardcoded name, so the
      // test asserts filtering without ever naming a person. Never logged.
      const firstLabel = ((await locators.listboxOptions.first().textContent()) ?? "").trim();
      const query = firstLabel.slice(0, 3);
      expect(query.length, "the first owner option rendered no label to search on").toBeGreaterThan(0);

      await test.step("Searching that prefix keeps only the owners that contain it", async () => {
        const before = await locators.listboxOptions.count();
        await searchInOpenPicker(locators, query);
        const labels = await locators.listboxOptions.allTextContents();
        expect(labels.length).toBeGreaterThan(0);
        expect(labels.length).toBeLessThanOrEqual(before);
        for (const label of labels) {
          expect(label.toLowerCase()).toContain(query.toLowerCase());
        }
      });

      await test.step("A query no owner can match falls through to the empty state", async () => {
        await searchInOpenPicker(locators, UNMATCHABLE_QUERY);
        await expect(locators.listboxOptions).toHaveCount(0);
        await expect(locators.openListbox.getByText(NO_RESULTS_TEXT, { exact: true })).toBeVisible();
      });
    }
  );

  test(
    "Ownership - open Add rule, fill the form completely, cancel instead of creating, verify the form closes and no rule by that name reaches the listing or the rules API",
    { tag: ["@dev", "@regression", "@negative"] },
    async ({ page }) => {
      const locators = await openOwnership(page);
      const name = ruleName("cancelled");
      const before = (await listRules(page)).length;

      await locators.addRuleBtn.click();
      await expect(locators.ruleDialog).toBeVisible();

      await test.step("Fill everything the form needs, so only the cancel decides the outcome", async () => {
        await locators.nameInput.fill(name);
        await locators.matchKeyInput.fill("team");
        await locators.matchValueInput.fill("cancelled");
        await pickFirstOwner(locators);
        await expect(locators.saveBtn).toBeEnabled();
      });

      await test.step("Cancel closes the form", async () => {
        await locators.cancelRuleBtn.click();
        await expect(locators.ruleDialog).toBeHidden();
      });

      await test.step("The cancelled rule was never written", async () => {
        await expect(locators.rulesTable.locator("tr").filter({ hasText: name })).toHaveCount(0);
        expect(await findRule(page, name)).toBeUndefined();
        expect(await listRules(page)).toHaveLength(before);
      });
    }
  );

  test(
    "Ownership - open Add rule, enter a label key, switch the Domain to Cloud, verify the Match list swaps to the cloud scopes and the entered label key is cleared",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await openOwnership(page);

      await locators.addRuleBtn.click();
      await expect(locators.ruleDialog).toBeVisible();

      await test.step("The form opens on the Kubernetes domain and its Label scope", async () => {
        await expect(locators.domainSelect).toContainText(DOMAIN_KUBERNETES);
        await expect(locators.scopeSelect).toContainText(K8S_SCOPE_LABELS[0]);
      });

      await test.step("The Match list offers exactly the Kubernetes scopes", async () => {
        await expectScopeOptions(locators, K8S_SCOPE_LABELS);
      });

      await test.step("A label key typed under the Kubernetes domain", async () => {
        await locators.matchKeyInput.fill("team");
        await expect(locators.matchKeyInput).toHaveValue("team");
      });

      await test.step("Switching the Domain to Cloud moves the form onto the cloud scopes", async () => {
        await selectOption(locators, locators.domainSelect, DOMAIN_CLOUD);
        await expect(locators.scopeSelect).toContainText(CLOUD_SCOPE_LABELS[0]);
      });

      await test.step("The Kubernetes label key did not survive the switch", async () => {
        // Asserted BEFORE touching the Match select: changeScope clears these
        // fields too, so picking a scope first would make this pass on its own
        // and prove nothing about changeDomain.
        await expect(locators.cloudTagKeyInput).toHaveValue("");
      });

      await test.step("The Match list now offers exactly the cloud scopes", async () => {
        await expectScopeOptions(locators, CLOUD_SCOPE_LABELS);
      });
    }
  );

  test(
    "Ownership - toggle a seeded enabled rule off from the listing, verify the row reports disabled and the rules API returns enabled false",
    { tag: ["@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const name = ruleName("toggle");
      const id = await seedLabelRule(page, name, owner, { enabled: true });

      try {
        const locators = await openOwnership(page);
        const toggle = locators.enabledToggle(name);

        await test.step("The seeded rule is listed as enabled", async () => {
          await expect(locators.ruleRow(name)).toBeVisible();
          await expect(toggle).toBeChecked();
        });

        await test.step("Turning the toggle off flips the control", async () => {
          await toggle.click();
          // The write is optimistic and reverts on failure, so the settled
          // unchecked state is the signal that the upsert was accepted.
          await expect(toggle).not.toBeChecked();
        });

        await test.step("The disable persisted — a reload still shows it off", async () => {
          await expect
            .poll(async () => (await findRule(page, name))?.enabled, {
              message: "ownership_list_rules should report the rule disabled after the toggle",
              timeout: 30000,
            })
            .toBe(false);
          await locators.open();
          await expect(locators.enabledToggle(name)).not.toBeChecked();
        });
      } finally {
        // Best-effort cleanup: absence is expected when the test already removed
        // the rule, and the afterAll sweep is the backstop either way.
        await deleteRuleById(page, id).catch(() => undefined);
      }
    }
  );

  test(
    "Ownership - open the row menu on a seeded rule, choose Delete, confirm, verify the row leaves the listing and the rules API no longer returns it",
    { tag: ["@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const name = ruleName("delete");
      const id = await seedLabelRule(page, name, owner);
      let deleted = false;

      try {
        const locators = await openOwnership(page);
        await expect(locators.ruleRow(name)).toBeVisible();

        await test.step("The row's kebab offers Delete and asks to confirm", async () => {
          await locators.rowMenuBtn(name).click();
          await locators.rowMenuItem("delete").click();
          await expect(locators.deleteDialog).toBeVisible();
          await expect(locators.deleteDialog).toContainText(`Delete rule "${name}"?`);
        });

        await test.step("Confirming removes the row from the listing", async () => {
          await locators.deleteConfirmBtn.click();
          await expect(locators.rulesTable.locator("tr").filter({ hasText: name })).toHaveCount(0);
          deleted = true;
        });

        await test.step("The delete persisted, not just the rendered row", async () => {
          expect(await findRule(page, name)).toBeUndefined();
        });
      } finally {
        if (!deleted) {
          // Best-effort cleanup: absence is expected when the UI delete already
          // landed, and the afterAll sweep is the backstop either way.
          await deleteRuleById(page, id).catch(() => undefined);
        }
      }
    }
  );

  test(
    "Ownership - leave Ownership for the Users tab and come back, verify Ownership is selected again and the seeded rule is still listed",
    { tag: ["@dev", "@smoke", "@functional"] },
    async ({ page }) => {
      const name = ruleName("nav");
      const id = await seedLabelRule(page, name, owner);

      try {
        const locators = await openOwnership(page);
        await expect(locators.ruleRow(name)).toBeVisible();

        await test.step("The Users tab takes over the page", async () => {
          await locators.usersTab.click();
          await expect(locators.usersTab).toHaveAttribute("data-tab-selected", "true");
          // The ownership listing owns this id, so its absence is what proves the
          // other tab's body actually replaced it.
          await expect(locators.listingRoot).toBeHidden();
        });

        await test.step("Coming back re-selects Ownership and re-renders its listing", async () => {
          await locators.ownershipTab.click();
          await expect(locators.ownershipTab).toHaveAttribute("data-tab-selected", "true");
          await expect(locators.listingRoot).toBeVisible();
          await expect(locators.ruleRow(name)).toBeVisible();
        });
      } finally {
        // Best-effort cleanup: absence is expected when the test already removed
        // the rule, and the afterAll sweep is the backstop either way.
        await deleteRuleById(page, id).catch(() => undefined);
      }
    }
  );

  test(
    "Ownership sanity - open the listing on a seeded account-wide label rule, verify the table headers render and the row shows the Label scope chip, its key = value match, All accounts and an owner",
    { tag: ["@dev", "@sanity", "@functional"] },
    async ({ page }) => {
      const name = ruleName("row");
      const id = await seedLabelRule(page, name, owner, { matchKey: "squad", matchValue: "checkout" });

      try {
        const locators = await openOwnership(page);

        await test.step("The table renders the columns the module declares", async () => {
          // HEADERS in OwnershipRules.jsx — the trailing actions column is unlabelled.
          for (const header of ["Name", "Match", "Account scope", "Owner", "Enabled"]) {
            await expect(locators.rulesTable.getByRole("columnheader", { name: header, exact: true })).toBeVisible();
          }
        });

        await test.step("The row renders the rule the way matchCell composes it", async () => {
          const row = locators.ruleRow(name);
          await expect(row).toBeVisible();
          // matchCell() puts a scope chip next to a "key = value" detail for a
          // label rule, and a rule with no cloud_account_id reads "All accounts".
          await expect(row).toContainText("Label");
          await expect(row).toContainText("squad = checkout");
          await expect(row).toContainText("All accounts");
        });

        await test.step("The row resolves its owner to a name rather than a raw id", async () => {
          // ownerLabel() falls back to the raw owner_id when the directory has no
          // entry, so the row must not simply echo the id back.
          const ownerCell = locators.ruleRow(name).locator("td").nth(3);
          await expect(ownerCell).not.toBeEmpty();
          await expect(ownerCell).not.toHaveText(owner.ownerId);
        });
      } finally {
        // Best-effort cleanup: absence is expected when the test already removed
        // the rule, and the afterAll sweep is the backstop either way.
        await deleteRuleById(page, id).catch(() => undefined);
      }
    }
  );
});
