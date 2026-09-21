import { test, expect } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import { Groups, GroupLocators } from "./groupLocatorsConstants";
import { CommonLocators } from "../../GlobalLocators";
// Added for the CRUD/edge-case suite below.
import type { Page } from "@playwright/test";
import {
  setup,
  ensureGroupExists,
  prepareMember,
  requireMemberAccount,
  restoreGroup,
  FIXTURE_GROUPS,
  FIXTURE_DESCRIPTION,
  GROUP_ERRORS,
  GROUP_TOASTS,
} from "./groupLocatorsConstants";

test("Add User Groups", { tag: ["@dev", "@test", "@oss", "@regression", "@functional", "@crud"] }, async ({ page }) => {
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
  // 5 minutes: a members test activates its account, normalises membership, then does its own work, and each modal cycle is slow on a loaded runner.
  test.describe.configure({ timeout: 300000 });

  // ── CREATE ──

  test(
    "User Groups - open Admin Groups, add a group with a name and description, save, verify the success snackbar and the group in the listing",
    { tag: ["@oss", "@dev", "@smoke", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Run-unique name: with no delete in the product, a fixed name would be created once and every later run would assert nothing.
      const name = `zz e2e created ${Date.now()}`;

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
    "User Groups - add a group and pick a member during creation, save, verify the member count in the listing",
    { tag: ["@oss", "@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `zz e2e created with member ${Date.now()}`;

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);

      // Captures the username so the member is verified by identity rather than by row count.
      await locators.membersPicker.click();
      const firstOption = page.locator('[role="option"]').first();
      await firstOption.waitFor({ state: "visible", timeout: 15000 });
      const username = ((await firstOption.textContent()) ?? "").trim();
      // An empty username would make the toContainText assertion below pass vacuously.
      expect(username, "member picker option had no text").not.toBe("");
      await firstOption.click();
      await page.keyboard.press("Escape"); // multi-select stays open after picking

      // The picked user lands in the members table and the card header counts it.
      await expect(locators.selectedUsersTable).toContainText(username);
      await expect(page.getByText("Members · 1")).toBeVisible();

      await locators.modalSubmitBtn.click();
      await expect(locators.toast(GROUP_TOASTS.created)).toBeVisible();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      // Matched by cell content, not column index, because the list has a leading expander cell.
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toBeVisible();
      await expect(locators.getGroupRow(name).locator("td").filter({ hasText: /^1$/ })).toBeVisible();
    }
  );

  // ── VIEW / SEARCH (non-mutating) ──

  test(
    "User Groups sanity - search the listing by group name, verify the matching row with its description",
    { tag: ["@oss", "@dev", "@smoke", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Nothing in the suite mutates this fixture, so its row content is stable.
      await ensureGroupExists(locators, FIXTURE_GROUPS.read);

      await locators.searchGroup(FIXTURE_GROUPS.read);

      const row = locators.getGroupRow(FIXTURE_GROUPS.read);
      await expect(row).toBeVisible();
      await expect(row).toContainText(FIXTURE_DESCRIPTION);
    }
  );

  test(
    "User Groups sanity - search a name that cannot exist, verify the empty state, clear the search, verify the listing returns",
    { tag: ["@oss", "@dev", "@regression", "@search", "@negative"] },
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
    "User Groups sanity - expand a group row, verify the members sub-table opens",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.read);
      await locators.searchGroup(FIXTURE_GROUPS.read);

      const row = locators.getGroupRow(FIXTURE_GROUPS.read);
      await expect(row).toBeVisible();

      // A row expands when non-interactive cell chrome is clicked, so the plain-text name cell is a safe target.
      await row.getByText(FIXTURE_GROUPS.read).click();

      await expect(page.getByRole("tab", { name: "Users" })).toBeVisible();
    }
  );

  // EDIT: each card saves itself and reports its own toast; there is no combined submit and the footer button is Close.

  test(
    "User Groups - open a group, edit the description, save the section, verify the snackbar and that the new description persists",
    { tag: ["@oss", "@dev", "@smoke", "@crud", "@snackbar"] },
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

        // Reopen the group so the value is read back from the API, then check the list rendered it too.
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
    "User Groups - open a group, rename it, save the section, verify the group is found under the new name and not the old one",
    { tag: ["@oss", "@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Unique per run: groups cannot be deleted, so a fixed rename target would stay taken after one crashed run.
      const editedName = `zz e2e renamed ${Date.now()}`;
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
    "User Groups - open a group, assign the ReadOnly Admin tenant role, save the section, verify the role appears in the listing",
    { tag: ["@oss", "@dev", "@regression", "@rbac", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.rbac);

      // ReadOnly Admin for least privilege, in case a crashed run leaves the role assigned on a shared tenant.
      const roleLabel = "ReadOnly Admin";
      // The list humanizes the stored role, so it differs from both the picker label and the raw value.
      const roleInList = "Tenant Admin Readonly";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.rbac);

        // Start from a genuinely empty baseline: a role left behind by an earlier run has to be cleared *and*
        // saved, because an unsaved clear leaves the section dirty and Save armed before the real edit is made.
        if (await locators.clearTenantRoles()) {
          await locators.saveTenantRolesBtn.click();
          await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
          // Let it fade, or the identical toast from the save below would match this one instead.
          await page
            .getByText(GROUP_TOASTS.tenantUpdated, { exact: true })
            .first()
            .waitFor({ state: "hidden", timeout: 15000 });
        }
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
    "User Groups - open a group, add an active user, save the members section, verify the member persists after reopening",
    { tag: ["@oss", "@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const addedUser = requireMemberAccount("add");
      await prepareMember(locators, page, addedUser);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      try {
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.saveMembersBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.addMember(addedUser.email);
        });

        // The members table is local state until Save is pressed.
        await expect(locators.getMemberRow(addedUser.email)).toBeVisible();

        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Reopen so membership is read back from the API.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(addedUser.email)).toBeVisible();
        await locators.closeModal();
      } finally {
        // Remove the member again so the fixture goes back to empty.
        try {
          await locators.closeModalIfOpen();
          if (addedUser) {
            await locators.openEditFor(FIXTURE_GROUPS.members);
            await locators.armAndClickSave(locators.saveMembersBtn, async () => {
              await locators.getMemberDeleteBtn(addedUser.email).click();
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove the seeded member from "${FIXTURE_GROUPS.members}":`, error);
        }
      }
    }
  );

  test(
    "User Groups - open a group, remove a member, save the members section, verify the member is gone after reopening",
    { tag: ["@oss", "@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const member = requireMemberAccount("remove");
      await prepareMember(locators, page, member);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      try {
        // Seeds its own member, so this test does not depend on another having left the fixture populated.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.addMember(member.email);
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Now remove it.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member.email)).toBeVisible();

        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.getMemberDeleteBtn(member.email).click();
        });
        await expect(locators.getMemberRow(member.email)).toHaveCount(0);

        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Reopen: the removal must have stuck.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member.email)).toHaveCount(0);
        await locators.closeModal();
      } finally {
        await locators.closeModalIfOpen();
      }
    }
  );

  // The three tests below never press Save, so they exercise real behaviour while writing nothing and needing no cleanup.

  test(
    "User Groups - add a member, switch the member filter across Active, Inactive and Suspended, verify only matching members are listed",
    { tag: ["@oss", "@dev", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      const addedUser = requireMemberAccount("add");
      await prepareMember(locators, page, addedUser);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      await locators.openEditFor(FIXTURE_GROUPS.members);
      // The picker only offers active users, so anything added here is Active.
      await locators.addMember(addedUser.email);

      await locators.selectMemberFilter("Active");
      await expect(locators.getMemberRow(addedUser.email)).toBeVisible();

      // The same member must disappear under the other two statuses.
      await locators.selectMemberFilter("Inactive");
      await expect(locators.getMemberRow(addedUser.email)).toHaveCount(0);

      await locators.selectMemberFilter("Suspended");
      await expect(locators.getMemberRow(addedUser.email)).toHaveCount(0);

      await locators.selectMemberFilter("Active");
      await expect(locators.getMemberRow(addedUser.email)).toBeVisible();

      await locators.closeModal(); // discard — nothing was saved
    }
  );

  test(
    "User Groups - open the member picker, search a partial username, verify only matching users are offered",
    { tag: ["@oss", "@dev", "@regression", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      const knownUser = requireMemberAccount("discard");
      await prepareMember(locators, page, knownUser);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      await locators.openEditFor(FIXTURE_GROUPS.members);

      await locators.membersPicker.click();
      const options = page.locator('[role="option"]');
      await options.first().waitFor({ state: "visible", timeout: 15000 });

      await locators.searchInMemberPicker(knownUser.email.slice(0, 4));
      await expect(locators.memberOption(knownUser.email)).toBeVisible();

      // A fragment that cannot match anything empties the list.
      await locators.searchInMemberPicker("zzz_nonexistent_user_000");
      await expect(options).toHaveCount(0);

      await page.keyboard.press("Escape");
      await locators.closeModal();
    }
  );

  test(
    "User Groups - add a member, reopen the picker, verify that user is no longer offered",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      const addedUser = requireMemberAccount("remove");
      await prepareMember(locators, page, addedUser);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      await locators.openEditFor(FIXTURE_GROUPS.members);

      await locators.addMember(addedUser.email);
      await expect(locators.getMemberRow(addedUser.email)).toBeVisible();

      // Reopening the picker must not offer the user just added, so the same person cannot be added twice.
      await locators.membersPicker.click();
      await expect(locators.memberOption(addedUser.email)).toHaveCount(0);

      await page.keyboard.press("Escape");
      await locators.closeModal(); // discard — nothing was saved
    }
  );

  test(
    "User Groups - remove a member, close without saving, discard the guard, verify the member is still there",
    { tag: ["@oss", "@dev", "@regression", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const member = requireMemberAccount("discard");
      await prepareMember(locators, page, member);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      try {
        // Seed a saved member, so there is something whose removal could persist.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.addMember(member.email);
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // Remove it, then close WITHOUT saving.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.getMemberDeleteBtn(member.email).click();
        await expect(locators.getMemberRow(member.email)).toHaveCount(0);

        // The removal is unsaved, so closing raises the guard; discard it.
        await locators.modalCancelBtn.click();
        await expect(locators.unsavedGuard).toBeVisible();
        await locators.discardChangesBtn.click();
        await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });
        await locators.waitForBackdropGone();

        // The member must still be there — table edits only apply on Save.
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member.email)).toBeVisible();
        await locators.closeModal();
      } finally {
        // Actually remove the seeded member so the fixture ends up empty.
        try {
          await locators.closeModalIfOpen();
          if (member) {
            await locators.openEditFor(FIXTURE_GROUPS.members);
            await locators.armAndClickSave(locators.saveMembersBtn, async () => {
              await locators.getMemberDeleteBtn(member.email).click();
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove the seeded member from "${FIXTURE_GROUPS.members}":`, error);
        }
      }
    }
  );

  // VALIDATION: the four name rules short-circuit in order, so each test below uses a value that reaches exactly the rule it checks.

  test(
    "User Groups - open the add-group form, clear the name, submit, verify the required-field error and that the form stays open",
    { tag: ["@oss", "@dev", "@regression", "@validation", "@negative"] },
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
    "User Groups - enter a name of only spaces, verify the required-field error",
    { tag: ["@oss", "@dev", "@regression", "@validation", "@negative"] },
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
    "User Groups - enter a name starting with a dash, verify the first-character error",
    { tag: ["@oss", "@dev", "@regression", "@validation", "@negative"] },
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
    "User Groups - enter a four-character name, verify the minimum-length error, then enter five characters, verify the error clears",
    { tag: ["@oss", "@dev", "@regression", "@validation", "@negative"] },
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
    "User Groups - enter a name containing special characters, verify the alpha-numeric error",
    { tag: ["@oss", "@dev", "@regression", "@validation", "@negative"] },
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
    "User Groups - enter a name with dashes, underscores and spaces, verify no validation error",
    { tag: ["@oss", "@dev", "@regression", "@validation"] },
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
    "User Groups - enter a 255-character name, verify no maximum-length error is enforced",
    { tag: ["@oss", "@dev", "@regression", "@validation"] },
    async ({ page }) => {
      const locators = await setup(page);
      await locators.newUserGroupIdentifier.click();

      // Documents that no maximum length is enforced; if a limit is ever added this test should fail and be updated.
      await locators.nameInput.fill("a".repeat(255));
      await expect(locators.nameError).toHaveCount(0);

      await locators.closeModal();
    }
  );

  // ── DUPLICATE NAME ──

  test(
    "User Groups - add a group using an existing name, submit, verify the duplicate error inline and that no second group is created",
    { tag: ["@oss", "@dev", "@regression", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.read);

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(FIXTURE_GROUPS.read);

      // Uniqueness is checked on submit, not while typing, so the field stays clean until Create is pressed.
      await expect(locators.nameError).toHaveCount(0);
      await locators.modalSubmitBtn.click();

      // Rejected as a field error rather than a snackbar, and the modal stays open so the name can be corrected.
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
    "User Groups - fill the add-group form, cancel, verify no group is created and the form is blank on reopen",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `zz e2e cancelled ${Date.now()}`;

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
    "User Groups - fill the add-group form, close with the X, verify no group is created",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `zz e2e dismissed ${Date.now()}`;

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
    "User Groups - edit a description, close without saving, discard the guard, verify the edit is discarded",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);

      await locators.openEditFor(FIXTURE_GROUPS.update);
      // Reads the stored value rather than assuming it, so the test holds if a previous run left a different description.
      const originalDescription = await locators.descInput.inputValue();

      await locators.fillStable(locators.descInput, `discarded edit ${Date.now()}`);

      // Closing with unsaved edits raises a guard rather than closing outright.
      await locators.modalCancelBtn.click();
      await expect(locators.unsavedGuard).toBeVisible();
      await locators.discardChangesBtn.click();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });
      await locators.waitForBackdropGone();

      await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

      // Reopen: the edit must be gone.
      await locators.openEditFor(FIXTURE_GROUPS.update);
      await expect(locators.descInput).toHaveValue(originalDescription);
      await locators.closeModal();
    }
  );

  test(
    "User Groups - edit a description, close without saving, choose Continue Editing, verify the form returns with the edit intact",
    { tag: ["@oss", "@dev", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);
      const pendingDescription = `kept by continue editing ${Date.now()}`;

      try {
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await locators.fillStable(locators.descInput, pendingDescription);

        await locators.modalCancelBtn.click();
        await expect(locators.unsavedGuard).toBeVisible();

        // Continue Editing dismisses the guard and leaves the edit in place.
        await locators.continueEditingBtn.click();
        await expect(locators.unsavedGuard).toBeHidden();
        await expect(locators.descInput).toHaveValue(pendingDescription);

        // Still unsaved, so the section Save is still armed.
        await expect(locators.saveGroupInfoBtn).toBeEnabled();
      } finally {
        // Leave without saving; closeModal discards through the guard.
        await locators.closeModalIfOpen();
      }
    }
  );

  test(
    "User Groups - edit a description, close without saving, choose Save and Exit, verify the snackbar and that the edit persists",
    { tag: ["@oss", "@dev", "@regression", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.update);
      const editedDescription = `saved via save-and-exit ${Date.now()}`;

      try {
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await locators.fillStable(locators.descInput, editedDescription);

        await locators.modalCancelBtn.click();
        await expect(locators.unsavedGuard).toBeVisible();

        // Save & Exit saves every dirty section, then closes.
        await locators.saveAndExitBtn.click();
        await expect(locators.toastText(GROUP_TOASTS.infoUpdated)).toBeVisible();
        await locators.nameInput.waitFor({ state: "hidden", timeout: 15000 });
        await locators.waitForBackdropGone();

        // The edit was written, not discarded.
        await locators.openEditFor(FIXTURE_GROUPS.update);
        await expect(locators.descInput).toHaveValue(editedDescription);
        await locators.closeModal();
      } finally {
        await restoreGroup(locators, [FIXTURE_GROUPS.update], {
          name: FIXTURE_GROUPS.update,
          description: FIXTURE_DESCRIPTION,
        });
      }
    }
  );

  // ERROR HANDLING: each stub matches only the operation under test, lets every other request through, and writes nothing.

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
    "User Groups - add a group with the create request failing, verify no success snackbar and no group created",
    { tag: ["@oss", "@dev", "@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = `zz e2e failcreate ${Date.now()}`;
      await failOperation(page, "CreateUserGroup", [{ message: "Simulated backend failure" }]);

      await locators.newUserGroupIdentifier.click();
      await locators.nameInput.fill(name);
      await locators.modalSubmitBtn.click();

      // Wait for the app to report something first, or the assertions below pass simply by running before any toast rendered.
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
    "User Groups - save a section with the update request failing, verify the API error message is surfaced and nothing is written",
    { tag: ["@oss", "@dev", "@regression", "@negative", "@snackbar"] },
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
    "User Groups - save a section with the update failing without a message, verify the generic update error is shown",
    { tag: ["@oss", "@dev", "@regression", "@negative", "@snackbar"] },
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
