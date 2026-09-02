import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { Groups, GroupLocators } from "./groupLocatorsConstants";
import { CommonLocators } from "../../GlobalLocators";
// Added for the CRUD/edge-case suite below.
import type { Page } from "@playwright/test";
import {
  setup,
  ensureGroupExists,
  restoreGroup,
  FIXTURE_GROUPS,
  FIXTURE_DESCRIPTION,
  GROUP_ERRORS,
  GROUP_TOASTS,
} from "./groupLocatorsConstants";

test("Add User Groups", async ({ page }) => {
  test.setTimeout(120000);

  const loginPage = new LoginPage(page);
  const locators = new GroupLocators(page);
  const commonLocators = new CommonLocators(page);
  await loginPage.doFullLogin();

  await expect(locators.homeBtn).toBeVisible({ timeout: 3000 });
  await locators.homeBtn.click();
  await commonLocators.adminBtn.click();

  console.log("Clicked on Admin button", commonLocators.adminBtn);
  await locators.groupsTab.click();

  await expect(locators.newUserGroupIdentifier).toBeVisible();

  for (const group of Groups) {
    await test.step(`Processing Group: ${group.name}`, async () => {
      await locators.newUserGroupIdentifier.click();

      await locators.groupNameInput.fill(group.name);
      await locators.descriptionInput.fill("Auto-test generated");

      await locators.createGroupBtn.click();

      const result = await Promise.race([
        locators.group_creation_successMsg
          .waitFor({ state: "visible", timeout: 300000 })
          .then(() => "success"),
        locators.group_creation_duplicateMsg
          .waitFor({ state: "visible", timeout: 300000 })
          .then(() => "duplicate"),
      ]);

      if (result === "success") {
        await expect(locators.group_creation_successMsg).toBeVisible();
      } else {
        await locators.cancelBtn.click();
      }
    });
  }
});

test.describe("Groups - CRUD & edge cases", () => {
  test.describe.configure({ timeout: 180000 });

  // ── CREATE ──

  test(
    "Create - group is created and appears in the list",
    { tag: ["@smoke", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      // A run-unique name. The product has no delete, so a fixed name would be
      // created once and every later run would take the duplicate path and assert
      // nothing. The cost of staying honest is one permanent group per run.
      const name = `e2e grp create ${Date.now()}`;

      await locators.newUserGroupIdentifier.click();
      await expect(locators.modalTitle).toHaveText("Add Group");

      await locators.nameInput.fill(name);
      await locators.descInput.fill("Created by e2e create test");
      await locators.modalSubmitBtn.click();

      await expect(locators.toast(GROUP_TOASTS.created)).toBeVisible();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      // The toast alone is not proof the group persisted — confirm it reached the list.
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toBeVisible();
    }
  );

  test(
    "Create - group with members shows correct member count",
    { tag: ["@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `e2e grp members create ${Date.now()}`;

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);

      // Pick the first available user. Capture its username first so the member
      // can be verified by identity rather than by row count alone.
      await locators.membersPicker.click();
      const firstOption = page.locator('[role="option"]').first();
      await firstOption.waitFor({ state: "visible", timeout: 15000 });
      const username = ((await firstOption.textContent()) ?? "").trim();
      // Every string contains "", so an empty username would make the
      // toContainText assertion below pass vacuously.
      expect(username, "member picker option had no text").not.toBe("");
      await firstOption.click();
      await page.keyboard.press("Escape"); // multi-select stays open after picking

      // The picked user lands in the members table and the card header counts it.
      await expect(locators.selectedUsersTable).toContainText(username);
      await expect(page.getByText("Members · 1")).toBeVisible();

      await locators.modalSubmitBtn.click();
      await expect(locators.toast(GROUP_TOASTS.created)).toBeVisible();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      // Total Members in the list must reflect the single member. Matched by cell
      // content rather than column index — the list has a leading expander cell.
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toBeVisible();
      await expect(locators.getGroupRow(name).locator("td").filter({ hasText: /^1$/ })).toBeVisible();
    }
  );

  // ── VIEW / SEARCH (non-mutating) ──

  test(
    "Search - finds a group by name",
    { tag: ["@smoke", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Fixture is created on the first run against a tenant, reused thereafter.
      // Nothing in the suite ever mutates this group, so its row content is stable.
      await ensureGroupExists(locators, FIXTURE_GROUPS.read);

      await locators.searchGroup(FIXTURE_GROUPS.read);

      const row = locators.getGroupRow(FIXTURE_GROUPS.read);
      await expect(row).toBeVisible();
      await expect(row).toContainText(FIXTURE_DESCRIPTION);
    }
  );

  test(
    "Search - no match shows empty state, clear restores list",
    { tag: ["@regression", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      // A name that cannot exist -> empty state.
      await locators.searchGroup("zzz_nonexistent_group_000");
      await expect(page.getByText("No Data Available")).toBeVisible();

      // Clearing the search restores the list.
      await locators.clearGroupSearch();
      await expect(page.getByText("No Data Available")).toBeHidden();
    }
  );

  test(
    "View - expanding a group row lists its members",
    { tag: ["@regression"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.read);
      await locators.searchGroup(FIXTURE_GROUPS.read);

      const row = locators.getGroupRow(FIXTURE_GROUPS.read);
      await expect(row).toBeVisible();

      // A row toggles its expanded panel when non-interactive cell chrome is
      // clicked (clicks originating from buttons/links are ignored), so the
      // plain-text group-name cell is a safe target.
      await row.getByText(FIXTURE_GROUPS.read).click();

      await expect(page.getByRole("tab", { name: "Users" })).toBeVisible();
    }
  );

  // ── EDIT ──
  // The edit modal saves per section: each card has its own Save button, enabled
  // only while that section is dirty, and each reports its own toast. There is no
  // combined submit, and the footer button is Close.

  test(
    "Edit - description update persists",
    { tag: ["@smoke", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);
      const editedDescription = `edited by e2e ${Date.now()}`;

      try {
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await expect(locators.modalTitle).toHaveText("Edit Group");

        // Nothing changed yet, so the section's Save is inert.
        await expect(locators.saveGroupInfoBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.fillStable(locators.descInput, editedDescription);
        });

        await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
        await locators.closeModal();

        // A toast only says the request was accepted. Reopen the group so the
        // value is read back from the API, and check the list rendered it too.
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await expect(locators.descInput).toHaveValue(editedDescription);
        await locators.closeModal();

        await locators.searchGroup(FIXTURE_GROUPS.update);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.update)).toContainText(editedDescription);
      } finally {
        await restoreGroup(locators, [FIXTURE_GROUPS.update], {
          name: FIXTURE_GROUPS.update,
          description: FIXTURE_DESCRIPTION,
        });
      }
    }
  );

  test(
    "Edit - group name update persists",
    { tag: ["@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Unique per run. A fixed rename target collides with itself: groups cannot
      // be deleted, so one crashed run leaves that name taken forever and every
      // later run is rejected with "Group name already in use".
      const editedName = `${FIXTURE_GROUPS.update} r${Date.now()}`;
      await ensureGroupExists(locators, FIXTURE_GROUPS.update);

      try {
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await expect(locators.saveGroupInfoBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.fillStable(locators.nameInput, editedName);
        });

        await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
        await locators.closeModal();

        // Found under the new name, and no longer under the old one.
        await locators.searchGroup(editedName);
        await expect(locators.getGroupRow(editedName)).toBeVisible();
        await expect(locators.getGroupRow(FIXTURE_GROUPS.update)).toHaveCount(0);
      } finally {
        await restoreGroup(locators, [editedName, FIXTURE_GROUPS.update], {
          name: FIXTURE_GROUPS.update,
          description: FIXTURE_DESCRIPTION,
        });
      }
    }
  );

  test(
    "Edit - tenant role assignment persists",
    { tag: ["@regression", "@rbac", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.rbac);

      // ReadOnly Admin rather than Admin: least privilege, in case a crashed run
      // ever leaves the role assigned on a shared tenant.
      const roleLabel = "ReadOnly Admin";
      // The list humanizes the stored role, so it reads differently from the
      // picker's label and from the raw value (tenant_admin_readonly).
      const roleInList = "Tenant Admin Readonly";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.rbac);

        // Start from a known baseline — a crashed run may have left a role set,
        // and re-picking the same role would leave the section clean and Save inert.
        await locators.clearTenantRoles();
        await expect(locators.saveTenantRolesBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveTenantRolesBtn, async () => {
          await locators.selectTenantRole(roleLabel);
        });

        await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
        await locators.closeModal();

        await locators.searchGroup(FIXTURE_GROUPS.rbac);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.rbac)).toContainText(roleInList);
      } finally {
        // Clear the role again so the fixture goes back to having none.
        try {
          await locators.closeModalIfOpen();
          await locators.openEditFor(FIXTURE_GROUPS.rbac);
          await locators.armAndClickSave(locators.saveTenantRolesBtn, async () => {
            await locators.clearTenantRoles();
          });
          await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
          await locators.closeModal();
        } catch (error) {
          console.warn(`Could not clear tenant role on "${FIXTURE_GROUPS.rbac}":`, error);
        }
      }
    }
  );

  // ── MEMBERS ──

  test(
    "Members - adding an active user persists after save",
    { tag: ["@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      let addedUser = "";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.saveMembersBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          addedUser = await locators.addFirstAvailableMember();
        });

        // The member appears in the table before saving — the table is local
        // state until Save is pressed.
        await expect(locators.getMemberRow(addedUser)).toBeVisible();

        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Reopen so membership is read back from the API.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(addedUser)).toBeVisible();
        await locators.closeModal();
      } finally {
        // Remove the member again so the fixture goes back to empty.
        try {
          await locators.closeModalIfOpen();
          if (addedUser) {
            await locators.openEditFor(FIXTURE_GROUPS.members);
            await locators.armAndClickSave(locators.saveMembersBtn, async () => {
              await locators.getMemberDeleteBtn(addedUser).click();
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove member "${addedUser}" from "${FIXTURE_GROUPS.members}":`, error);
        }
      }
    }
  );

  test(
    "Members - removing a member persists after save",
    { tag: ["@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      let member = "";

      try {
        // Seed a member to remove, so this test does not depend on another one
        // having left the fixture populated.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          member = await locators.addFirstAvailableMember();
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Now remove it.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member)).toBeVisible();

        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.getMemberDeleteBtn(member).click();
        });
        await expect(locators.getMemberRow(member)).toHaveCount(0);

        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Reopen: the removal must have stuck.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member)).toHaveCount(0);
        await locators.closeModal();
      } finally {
        await locators.closeModalIfOpen();
      }
    }
  );

  // The three tests below never press Save. The members table is local state
  // until then, so they exercise real behaviour while writing nothing at all —
  // which also means they need no cleanup.

  test(
    "Members - status filter shows only members with that status",
    { tag: ["@regression"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      await locators.openEditFor(FIXTURE_GROUPS.members);
      // The picker only offers active users, so anything added here is Active.
      const addedUser = await locators.addFirstAvailableMember();

      await locators.selectMemberFilter("Active");
      await expect(locators.getMemberRow(addedUser)).toBeVisible();

      // The same member must disappear under the other two statuses.
      await locators.selectMemberFilter("Inactive");
      await expect(locators.getMemberRow(addedUser)).toHaveCount(0);

      await locators.selectMemberFilter("Suspended");
      await expect(locators.getMemberRow(addedUser)).toHaveCount(0);

      await locators.selectMemberFilter("Active");
      await expect(locators.getMemberRow(addedUser)).toBeVisible();

      await locators.closeModal(); // discard — nothing was saved
    }
  );

  test(
    "Members - picker search narrows the user list",
    { tag: ["@regression", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      await locators.openEditFor(FIXTURE_GROUPS.members);

      await locators.membersPicker.click();
      const options = page.locator('[role="option"]');
      await options.first().waitFor({ state: "visible", timeout: 15000 });

      const firstUser = ((await options.first().textContent()) ?? "").trim();
      // An empty value would make both search assertions below meaningless.
      expect(firstUser, "member picker option had no text").not.toBe("");
      const fragment = firstUser.slice(0, 4);

      await locators.searchInMemberPicker(fragment);
      await expect(locators.memberOption(firstUser)).toBeVisible();

      // A fragment that cannot match anything empties the list.
      await locators.searchInMemberPicker("zzz_nonexistent_user_000");
      await expect(options).toHaveCount(0);

      await page.keyboard.press("Escape");
      await locators.closeModal();
    }
  );

  test(
    "Members - an existing member is not offered in the picker",
    { tag: ["@regression"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      await locators.openEditFor(FIXTURE_GROUPS.members);

      const addedUser = await locators.addFirstAvailableMember();
      await expect(locators.getMemberRow(addedUser)).toBeVisible();

      // Reopen the picker: the user just added must no longer be selectable,
      // so the same person cannot be added twice.
      await locators.membersPicker.click();
      await expect(locators.memberOption(addedUser)).toHaveCount(0);

      await page.keyboard.press("Escape");
      await locators.closeModal(); // discard — nothing was saved
    }
  );

  test(
    "Members - removing a member then closing discards the change",
    { tag: ["@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      let member = "";

      try {
        // Seed a saved member, so there is something whose removal could persist.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          member = await locators.addFirstAvailableMember();
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Remove it, then close WITHOUT saving.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.getMemberDeleteBtn(member).click();
        await expect(locators.getMemberRow(member)).toHaveCount(0);
        await locators.closeModal();

        // The member must still be there — table edits only apply on Save.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member)).toBeVisible();
        await locators.closeModal();
      } finally {
        // Actually remove the seeded member so the fixture ends up empty.
        try {
          await locators.closeModalIfOpen();
          if (member) {
            await locators.openEditFor(FIXTURE_GROUPS.members);
            await locators.armAndClickSave(locators.saveMembersBtn, async () => {
              await locators.getMemberDeleteBtn(member).click();
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove seeded member "${member}":`, error);
        }
      }
    }
  );

  // ── VALIDATION ──
  // Group name is checked on every keystroke against four rules, in order:
  // required -> must start with a letter or digit -> at least 5 characters ->
  // letters/digits/dash/underscore/space only. The first failure wins, so each
  // test below uses a value that reaches exactly the rule it is checking.
  // These never submit, so nothing is ever written and no cleanup is needed.

  test(
    "Create - name is required",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // Type then clear: validation runs on change, so an untouched field is silent.
      await locators.nameInput.fill("Valid name");
      await locators.nameInput.fill("");
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.required);

      // Submitting an invalid form is rejected and the modal stays open.
      await locators.modalSubmitBtn.click();
      await expect(locators.nameInput).toBeVisible();

      await locators.closeModal();
    }
  );

  test(
    "Create - whitespace-only name is rejected",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // The required rule trims, so spaces alone count as empty.
      await locators.nameInput.fill("     ");
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.required);

      await locators.closeModal();
    }
  );

  test(
    "Create - name must start with a letter or digit",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // A dash is a legal character, just not as the first one.
      await locators.nameInput.fill("-abc12");
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.firstChar);

      await locators.closeModal();
    }
  );

  test(
    "Create - name below 5 characters is rejected, 5 is accepted",
    { tag: ["@regression", "@validation"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // Boundary: 4 rejected, 5 accepted.
      await locators.nameInput.fill("abcd");
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.minLength);

      await locators.nameInput.fill("abcde");
      await expect(locators.nameError).toHaveCount(0);

      await locators.closeModal();
    }
  );

  test(
    "Create - name rejects special characters",
    { tag: ["@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // Long enough and starts with a letter, so it reaches the character rule.
      await locators.nameInput.fill("test@#$%");
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.alphaNum);

      await locators.closeModal();
    }
  );

  test(
    "Create - name accepts dashes, underscores and spaces",
    { tag: ["@regression", "@validation"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // The positive case for the character rule — these are all permitted.
      await locators.nameInput.fill("test-grp_1 x");
      await expect(locators.nameError).toHaveCount(0);

      await locators.closeModal();
    }
  );

  test(
    "Create - name has no maximum length",
    { tag: ["@regression", "@validation"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // Documents that no upper bound is enforced: a 255-character name is
      // accepted client-side. If a limit is ever added, this test should fail
      // and be updated to assert it.
      await locators.nameInput.fill("a".repeat(255));
      await expect(locators.nameError).toHaveCount(0);

      await locators.closeModal();
    }
  );

  // ── DUPLICATE NAME ──

  test(
    "Create - duplicate name is rejected inline",
    { tag: ["@regression", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.read);

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(FIXTURE_GROUPS.read);

      // Uniqueness is checked on submit, not while typing, so the field is clean
      // until Create is pressed.
      await expect(locators.nameError).toHaveCount(0);
      await locators.modalSubmitBtn.click();

      // Rejected inline — this is a field error, not a snackbar — and the modal
      // stays open so the name can be corrected.
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.duplicate);
      await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);
      await expect(locators.nameInput).toBeVisible();

      await locators.closeModal();

      // No second group was created under that name.
      await locators.searchGroup(FIXTURE_GROUPS.read);
      await expect(locators.getGroupRow(FIXTURE_GROUPS.read)).toHaveCount(1);
    }
  );

  // ── CANCEL / CLOSE ──

  test(
    "Cancel - cancelling create does not create a group",
    { tag: ["@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `e2e grp cancelled ${Date.now()}`;

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);
      await locators.descInput.fill("should never be saved");
      await locators.closeModal(); // Cancel

      await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);

      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toHaveCount(0);

      // Reopening gives a clean form rather than the abandoned input.
      await locators.newUserGroupIdentifier.click();
      await expect(locators.nameInput).toHaveValue("");
      await expect(locators.descInput).toHaveValue("");
      await locators.closeModal();
    }
  );

  test(
    "Cancel - closing the create modal with X does not create a group",
    { tag: ["@regression"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `e2e grp dismissed ${Date.now()}`;

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);

      // The header X must behave exactly like Cancel.
      await locators.closeModalBtn.click();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);

      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toHaveCount(0);
    }
  );

  test(
    "Cancel - closing the edit modal discards unsaved changes",
    { tag: ["@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);

      await locators.openEditFor(FIXTURE_GROUPS.update);
      // Read the stored value rather than assuming it, so the test still holds
      // if a previous run left a different description behind.
      const originalDescription = await locators.descInput.inputValue();

      await locators.fillStable(locators.descInput, `discarded edit ${Date.now()}`);
      await locators.closeModal(); // Close without saving any section

      await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

      // Reopen: the edit must be gone.
      await locators.openEditFor(FIXTURE_GROUPS.update);
      await expect(locators.descInput).toHaveValue(originalDescription);
      await locators.closeModal();
    }
  );

  // ── ERROR HANDLING ──
  // These force the API to fail so the error paths can be exercised. The stub
  // only matches the one operation under test and lets every other request
  // through, and nothing is ever written, so they need no cleanup.

  // Fail a single GraphQL operation with the given errors payload.
  async function failOperation(page: Page, operationName: string, errors: unknown[]) {
    await page.route("**/api/graphql", async (route) => {
      const body = route.request().postData() ?? "";
      if (body.includes(operationName)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ errors }),
        });
        return;
      }
      await route.continue();
    });
  }

  test(
    "Create - a failed create is not reported as success",
    { tag: ["@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `e2e grp failcreate ${Date.now()}`;
      await failOperation(page, "CreateUserGroup", [{ message: "Simulated backend failure" }]);

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);
      await locators.modalSubmitBtn.click();

      // Wait for the app to report *something* before judging, otherwise the
      // assertions below would pass simply by running before any toast rendered.
      const success = locators.toast(GROUP_TOASTS.created);
      await Promise.race([
        success.waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
        page.getByRole("alert").first().waitFor({ state: "visible", timeout: 15000 }).catch(() => {}),
      ]);

      // The create failed, so success must not be claimed.
      await expect(success).toHaveCount(0);

      // And no group may exist under that name.
      await page.unroute("**/api/graphql");
      await locators.closeModalIfOpen();
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toHaveCount(0);
    }
  );

  test(
    "Edit - a failed section save surfaces the API error message",
    { tag: ["@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);
      const apiMessage = "Simulated backend failure";

      await locators.openEditFor(FIXTURE_GROUPS.update);
      await failOperation(page, "UpdateUserGroup", [{ message: apiMessage }]);

      await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
        await locators.descInput.fill(`will not save ${Date.now()}`);
      });

      // The API's own message is surfaced to the user, not swallowed.
      await expect(page.getByText(apiMessage, { exact: true }).first()).toBeVisible();
      await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

      await page.unroute("**/api/graphql");
      await locators.closeModal();

      // Nothing was written.
      await locators.openEditFor(FIXTURE_GROUPS.update);
      await expect(locators.descInput).not.toHaveValue(/will not save/);
      await locators.closeModal();
    }
  );

  test(
    "Edit - a failed section save falls back to a generic error message",
    { tag: ["@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);

      await locators.openEditFor(FIXTURE_GROUPS.update);
      // An error with no message, so the app has to supply its own wording.
      await failOperation(page, "UpdateUserGroup", [{}]);

      await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
        await locators.descInput.fill(`will not save ${Date.now()}`);
      });

      await expect(page.getByText("Failed to update group", { exact: true }).first()).toBeVisible();
      await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

      await page.unroute("**/api/graphql");
      await locators.closeModal();
    }
  );
});
