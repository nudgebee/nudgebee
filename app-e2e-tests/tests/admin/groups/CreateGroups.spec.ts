import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";
import {
  setup,
  ensureGroupExists,
  groupExists,
  prepareMember,
  requireMemberAccount,
  requireAccountName,
  openUsersPage,
  assignGroupFromUsersPage,
  restoreGroup,
  FIXTURE_GROUPS,
  FIXTURE_DESCRIPTIONS,
  FIXTURE_ALIASES,
  EDIT_DESCRIPTIONS,
  SAVE_EXIT_DESCRIPTIONS,
  otherDescription,
  UNSAVED_GROUP_NAMES,
  GROUP_ERRORS,
  GROUP_TOASTS,
  ACCOUNT_ROLE_VALUES,
  errorSummary,
} from "./groupLocatorsConstants";

test.describe("Groups - CRUD & edge cases", () => {
  // mode "default" because the project sets fullyParallel, which splits even one file across workers - and these
  // tests share three fixture groups, so concurrent runs of them fight over the same rows. Not "serial": that
  // skips every remaining test once one fails, which would cost the whole file's signal for a single failure.
  // Per-test isolation is the usual answer instead, but here it would mean a group per test and groups cannot
  // be deleted, which is the leak this suite exists to stop.
  // 5 minutes: a members test activates its account, normalises membership, then does its own work, and each modal cycle is slow on a loaded runner.
  test.describe.configure({ mode: "default", timeout: 300000 });

  // ── CREATE ──

  test(
    "User Groups - add the roles fixture when it is missing, verify the success snackbar and the listing row with its description",
    { tag: ["@oss", "@dev", "@test", "@smoke", "@functional", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = FIXTURE_GROUPS.roles;
      const description = FIXTURE_DESCRIPTIONS[name];

      // Groups cannot be deleted, so creating unconditionally left one undeletable row per run. On a tenant that
      // still has the fixture this asserts the fixture is intact; on a fresh one it creates it and proves the
      // create flow. The test env builds a new tenant per run, so the create path is exercised there every time.
      if (await groupExists(locators, name)) {
        await expect(locators.getGroupRow(name)).toContainText(description);
        return;
      }

      await locators.openAddGroupForm();
      await expect(locators.modalTitle).toHaveText("Add Group");

      await locators.nameInput.fill(name);
      await locators.descInput.fill(description);
      await locators.modalSubmitBtn.click();

      // Page-wide for presence; the region-scoped locator stays for the absence checks further down, where
      // scoping is what makes "no success toast" mean something.
      await expect(locators.toastText(GROUP_TOASTS.created)).toBeVisible();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      // The toast alone is not proof the group persisted — confirm it reached the list.
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toContainText(description);
    }
  );

  test(
    "User Groups - add the members fixture with a member picked during creation, verify the member count in the listing",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = FIXTURE_GROUPS.members;
      const description = FIXTURE_DESCRIPTIONS[name];

      // Same find-or-create shape as the test above. The member count is only asserted on the create path:
      // the members tests each seed and then remove their own member, so this fixture settles back to empty.
      if (await groupExists(locators, name)) {
        await expect(locators.getGroupRow(name)).toContainText(description);
        return;
      }

      // A named account from .env, never whichever user the picker happens to list first. This branch only
      // runs on a tenant without the fixture, so it went unnoticed on dev and seeded the fixture with a real
      // colleague's account - permanently, since the create path never removes what it added.
      const member = requireMemberAccount("add");
      await prepareMember(locators, page, member);

      await locators.openAddGroupForm();
      await locators.nameInput.fill(name);
      await locators.descInput.fill(description);

      await locators.addMember(member.email);

      // The picked user lands in the members table and the card header counts it.
      await expect(locators.selectedUsersTable).toContainText(member.name);
      await expect(page.getByText("Members · 1")).toBeVisible();

      await locators.modalSubmitBtn.click();
      // Page-wide, like every other toast assertion here: scoping to the Notifications region ties the test
      // to container markup that differs between builds.
      await expect(locators.toastText(GROUP_TOASTS.created)).toBeVisible();
      await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

      // Matched by cell content, not column index, because the list has a leading expander cell.
      await locators.searchGroup(name);
      await expect(locators.getGroupRow(name)).toBeVisible();
      await expect(locators.getGroupRow(name).locator("td").filter({ hasText: /^1$/ })).toBeVisible();
    }
  );

  // ── VIEW / SEARCH (non-mutating) ──

  test(
    "User Groups sanity - search the listing by name and by a name that cannot exist, verify the matching row and the empty state",
    { tag: ["@oss", "@dev", "@test", "@sanity", "@search", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      // The roles fixture's name and description are never written by any test - only its role assignments are,
      // and those live in a different column - so the row content asserted below is stable whatever else ran first.
      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);

      await test.step("a matching name returns the row with its description", async () => {
        await locators.searchGroup(FIXTURE_GROUPS.roles);

        const row = locators.getGroupRow(FIXTURE_GROUPS.roles);
        await expect(row).toBeVisible();
        await expect(row).toContainText(FIXTURE_DESCRIPTIONS[FIXTURE_GROUPS.roles]);
      });

      await test.step("a name that cannot exist shows the empty state, and clearing brings the listing back", async () => {
        await locators.searchGroup("zzz_nonexistent_group_000");
        await expect(page.getByText("No Data Available")).toBeVisible();

        await locators.clearGroupSearch();
        // Count, not toBeHidden: the empty state is conditionally rendered, so it leaves the DOM entirely
        // and a hidden-but-present element would mean the listing had not actually come back.
        await expect(page.getByText("No Data Available")).toHaveCount(0);
      });
    }
  );

  // EDIT: each card saves itself and reports its own toast; there is no combined submit and the footer button is Close.

  test(
    "User Groups - open a group, edit the description, save the section, verify the snackbar and that the new description persists",
    { tag: ["@oss", "@dev", "@test", "@smoke", "@functional", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.info);
      let editedDescription: string = EDIT_DESCRIPTIONS[0];

      try {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await expect(locators.modalTitle).toHaveText("Edit Group");

        // Nothing changed yet, so the section's Save is inert.
        await expect(locators.saveGroupInfoBtn).toBeDisabled();

        // Whichever value is not stored right now, so a leftover from a crashed run cannot make this a no-op.
        editedDescription = otherDescription(await locators.descInput.inputValue(), EDIT_DESCRIPTIONS);

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.fillStable(locators.descInput, editedDescription);
        });

        await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
        await locators.closeModal();

        // Reopen the group so the value is read back from the API, then check the list rendered it too.
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await expect(locators.descInput).toHaveValue(editedDescription);
        await locators.closeModal();

        await locators.searchGroup(FIXTURE_GROUPS.info);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.info)).toContainText(editedDescription);
      } finally {
        await restoreGroup(locators, [FIXTURE_GROUPS.info], {
          name: FIXTURE_GROUPS.info,
          description: FIXTURE_DESCRIPTIONS[FIXTURE_GROUPS.info],
        });
      }
    }
  );

  test(
    "User Groups - open a group, rename it, save the section, verify the group is found under the new name and not the old one",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      // Fixed, not timestamped, and taken from FIXTURE_ALIASES so every other test heals a crashed rename too.
      // A per-run name could never be found again, which is how "e2e grp update r<ts>" rows were orphaned on
      // dev, one per crash.
      const editedName = FIXTURE_ALIASES[FIXTURE_GROUPS.info][0];
      await ensureGroupExists(locators, FIXTURE_GROUPS.info);

      try {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await expect(locators.saveGroupInfoBtn).toBeDisabled();

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.fillStable(locators.nameInput, editedName);
        });

        await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
        await locators.closeModal();

        // Found under the new name, and no longer under the old one.
        await locators.searchGroup(editedName);
        await expect(locators.getGroupRow(editedName)).toBeVisible();
        await expect(locators.getGroupRow(FIXTURE_GROUPS.info)).toHaveCount(0);
      } finally {
        await restoreGroup(locators, [editedName, FIXTURE_GROUPS.info], {
          name: FIXTURE_GROUPS.info,
          description: FIXTURE_DESCRIPTIONS[FIXTURE_GROUPS.info],
        });
      }
    }
  );

  test(
    "User Groups - open a group, assign the ReadOnly Admin tenant role, save the section, verify the role appears in the listing",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@rbac", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);

      // ReadOnly Admin for least privilege, in case a crashed run leaves the role assigned on a shared tenant.
      const roleLabel = "ReadOnly Admin";
      // The list humanizes the stored role, so it differs from both the picker label and the raw value.
      const roleInList = "Tenant Admin Readonly";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.roles);

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

        await locators.searchGroup(FIXTURE_GROUPS.roles);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).toContainText(roleInList);
      } finally {
        // Clear the role again so the fixture goes back to having none.
        try {
          await locators.closeModalIfOpen();
          await locators.openEditFor(FIXTURE_GROUPS.roles);
          // clearTenantRoles reports whether anything was actually selected. Handing a no-op to armAndClickSave
          // left Save disabled and its final assert threw, so an already-clean fixture logged a swallowed
          // "toBeEnabled failed" that read like the cleanup had failed when there was nothing to clean.
          if (await locators.clearTenantRoles()) {
            await locators.saveTenantRolesBtn.click();
            await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
          }
        } catch (error) {
          console.warn(`Could not clear tenant role on "${FIXTURE_GROUPS.roles}":`, errorSummary(error));
        } finally {
          await locators.closeModalIfOpen();
        }
      }
    }
  );

  test(
    "User Groups - assign an account role, save the section, verify it persists after reopening",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@rbac", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);
      const roleLabel = "ReadOnly Admin";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();

        // The account comes back from the picker rather than from .env: which accounts a tenant has differs
        // per environment, and the assertions below only need to name the one that was actually used.
        const accountName = await locators.addAccountRole(roleLabel, requireAccountName("CLUSTER"));

        await locators.saveAccountRolesBtn.click();
        await locators.expectSectionToast(GROUP_TOASTS.accountUpdated);
        await locators.closeModal();

        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();
        await expect(locators.selectedAccountsTable).toContainText(accountName);
        await locators.closeModal();
      } finally {
        await locators.clearAccountRoles(FIXTURE_GROUPS.roles);
      }
    }
  );

  test(
    "User Groups - assign an account role then save the info section on its own, verify the account role is not wiped",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@rbac"] },
    async ({ page }) => {
      const locators = await setup(page);

      // The info fixture, not the roles one: proving the account role survives needs a second section to be
      // saved, and the only other section is the group's own name and description. The roles fixture's
      // description is asserted verbatim by the create and sanity tests, so churning it there left those two
      // failing on a LATER run whenever this test's best-effort restore did not land. Nothing asserts the info
      // fixture's description - churning it is what that fixture is for.
      await ensureGroupExists(locators, FIXTURE_GROUPS.info);

      try {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await locators.accountRbacTab.click();

        const accountName = await locators.addAccountRole("ReadOnly Admin", requireAccountName("CLUSTER"));
        await locators.saveAccountRolesBtn.click();
        await locators.expectSectionToast(GROUP_TOASTS.accountUpdated);
        await locators.closeModal();

        // The account save is replace-all and runs off a list the modal fetches on open. Saving a different
        // section without ever opening the Account tab used to write that empty list back and drop every
        // account role. This is the regression test for that.
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.fillStable(locators.descInput, otherDescription(await locators.descInput.inputValue(), EDIT_DESCRIPTIONS));
        });
        await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
        await locators.closeModal();

        await locators.openEditFor(FIXTURE_GROUPS.info);
        await locators.accountRbacTab.click();
        await expect(locators.selectedAccountsTable).toContainText(accountName);
        await locators.closeModal();
      } finally {
        await locators.clearAccountRoles(FIXTURE_GROUPS.info);
        await restoreGroup(locators, [FIXTURE_GROUPS.info], {
          name: FIXTURE_GROUPS.info,
          description: FIXTURE_DESCRIPTIONS[FIXTURE_GROUPS.info],
        });
      }
    }
  );

  test(
    "User Groups - change an assigned account role to the other one, then delete the row, verify the account keeps a single row with the new role and that the removal persists",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@rbac"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);
      // Filled in by the first add: the override this test is about has to land on the SAME account, and which
      // account that is depends on what the tenant holds.
      let accountName = "";

      // Three saves raise the same toast, so each one has to be watched away before the next is asserted,
      // or a lingering toast satisfies the following assertion without a save having happened.
      const settle = async () => {
        await locators.expectSectionToast(GROUP_TOASTS.accountUpdated);
        await page.getByText(GROUP_TOASTS.accountUpdated, { exact: true }).first().waitFor({ state: "hidden", timeout: 15000 });
      };

      try {
        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();
        accountName = await locators.addAccountRole("ReadOnly Admin", requireAccountName("CLUSTER"));
        await locators.saveAccountRolesBtn.click();
        await settle();
        await locators.closeModal();

        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();
        // Assert the saved role before changing it: the table fills in only once the modal's account fetch
        // lands, and adding to an empty table would be a fresh add, not the override this test is about.
        await expect(locators.getAccountRoleCell(accountName)).toHaveText(ACCOUNT_ROLE_VALUES["ReadOnly Admin"]);

        await locators.addAccountRole("Admin", accountName);
        // The app also raises an info snackbar here ("Replaced the existing role for 1 account."), and asserting
        // it was a mistake: whether it fires depends on the modal classifying the picked role as built-in, which
        // is internal state this test cannot see. When that went wrong the failure read "no toast" while the real
        // question is whether the role was replaced - and the two assertions below answer exactly that.
        // Only one built-in role per account, so the new one must take the old row's place rather than stack beside it.
        await expect(locators.getAccountRoleRow(accountName)).toHaveCount(1);
        await expect(locators.getAccountRoleCell(accountName)).toHaveText(ACCOUNT_ROLE_VALUES.Admin);

        await locators.saveAccountRolesBtn.click();
        await settle();
        await locators.closeModal();

        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();
        await expect(locators.getAccountRoleCell(accountName)).toHaveText(ACCOUNT_ROLE_VALUES.Admin);

        // The account save is replace-all, and clearing the last row is its own write path - an emptied role
        // still needs an explicit empty write or the removal never reaches the server.
        await locators.getAccountRoleDeleteBtn(accountName).click();
        await expect(locators.getAccountRoleRow(accountName)).toHaveCount(0);
        await locators.saveAccountRolesBtn.click();
        await settle();
        await locators.closeModal();

        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.accountRbacTab.click();
        await expect(locators.getAccountRoleRow(accountName)).toHaveCount(0);
        await locators.closeModal();
      } finally {
        await locators.clearAccountRoles(FIXTURE_GROUPS.roles);
      }
    }
  );

  test(
    "User Groups - change the tenant role from ReadOnly Admin to Admin and then clear it, verify the listing shows the new role alone and then none",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@rbac", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);
      // The listing humanizes the stored role, so both differ from the picker labels.
      const readonlyInList = "Tenant Admin Readonly";
      const adminInList = "Tenant Admin";

      // The picker toggles, so a role left behind by an earlier run would be switched off by the first select.
      // Clearing has to be saved as well: an unsaved clear leaves the section dirty and Save armed before the
      // real edit is made.
      const settle = async () => {
        await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
        // Let it fade, or the identical toast from the next save would match this one instead.
        await page.getByText(GROUP_TOASTS.tenantUpdated, { exact: true }).first().waitFor({ state: "hidden", timeout: 15000 });
      };

      try {
        await locators.openEditFor(FIXTURE_GROUPS.roles);

        if (await locators.clearTenantRoles()) {
          await locators.saveTenantRolesBtn.click();
          await settle();
        }

        await locators.armAndClickSave(locators.saveTenantRolesBtn, async () => {
          await locators.selectTenantRole("ReadOnly Admin");
        });
        await settle();

        // One picker holds the built-in role and the custom roles together and allows a single built-in, so
        // choosing the other one has to replace it rather than leave both assigned.
        await locators.armAndClickSave(locators.saveTenantRolesBtn, async () => {
          await locators.selectTenantRole("Admin");
        });
        await settle();
        await locators.closeModal();

        await locators.searchGroup(FIXTURE_GROUPS.roles);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).toBeVisible();
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).toContainText(adminInList);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).not.toContainText(readonlyInList);

        // Removing sends an empty role rather than skipping the call, so the clear needs its own assertion.
        await locators.openEditFor(FIXTURE_GROUPS.roles);
        await locators.armAndClickSave(locators.saveTenantRolesBtn, async () => {
          await locators.clearTenantRoles();
        });
        await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
        await locators.closeModal();

        await locators.searchGroup(FIXTURE_GROUPS.roles);
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).toBeVisible();
        await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).not.toContainText(adminInList);
      } finally {
        try {
          await locators.closeModalIfOpen();
          await locators.openEditFor(FIXTURE_GROUPS.roles);
          // clearTenantRoles reports whether anything was actually selected. Handing a no-op to armAndClickSave
          // left Save disabled and its final assert threw, so an already-clean fixture logged a swallowed
          // "toBeEnabled failed" that read like the cleanup had failed when there was nothing to clean.
          if (await locators.clearTenantRoles()) {
            await locators.saveTenantRolesBtn.click();
            await locators.expectSectionToast(GROUP_TOASTS.tenantUpdated);
          }
        } catch (error) {
          console.warn(`Could not clear tenant role on "${FIXTURE_GROUPS.roles}":`, errorSummary(error));
        } finally {
          await locators.closeModalIfOpen();
        }
      }
    }
  );

  // ── CROSS-SCREEN: membership is writable from the group and from the user, and both write the same rows ──

  test(
    "User Groups - assign a group from the Users page, verify the user appears in that group's members",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const member = requireMemberAccount("remove");
      await prepareMember(locators, page, member);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      try {
        await assignGroupFromUsersPage(page, member, FIXTURE_GROUPS.members);

        // Written from the Users side, read back from the group: the two screens must agree.
        await locators.openGroupsTab();
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await expect(locators.getMemberRow(member.email)).toBeVisible();
        await locators.closeModal();
      } finally {
        await locators.closeModalIfOpen();
        await locators.openGroupsTab();
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.removeMemberIfPresent(member.email);
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();
      }
    }
  );

  test(
    "User Groups - add a member from the group, verify the group is listed against that user on the Users page",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud"] },
    async ({ page }) => {
      const locators = await setup(page);

      const member = requireMemberAccount("add");
      await prepareMember(locators, page, member);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);

      try {
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.addMember(member.email);
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();

        // The Users listing carries a Group column, so the write has to show up there too.
        const users = await openUsersPage(page);
        await users.selectStatusFilter("Active");
        await users.searchByName(member.name);
        await expect(users.getUserRow(member.email)).toContainText(FIXTURE_GROUPS.members);
      } finally {
        await locators.closeModalIfOpen();
        await locators.openGroupsTab();
        await locators.openEditFor(FIXTURE_GROUPS.members);
        await locators.armAndClickSave(locators.saveMembersBtn, async () => {
          await locators.removeMemberIfPresent(member.email);
        });
        await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
        await locators.closeModal();
      }
    }
  );

  // ── MEMBERS ──

  test(
    "User Groups - open a group, add an active user, save the members section, verify the member persists after reopening",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud", "@snackbar"] },
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
              await locators.removeMemberIfPresent(addedUser.email);
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove the seeded member from "${FIXTURE_GROUPS.members}":`, errorSummary(error));
        }
      }
    }
  );

  test(
    "User Groups - open a group, remove a member, save the members section, verify the member is gone after reopening",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud", "@snackbar"] },
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
          await locators.removeMemberIfPresent(member.email);
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
    "User Groups - search the member picker, add the user, filter by status and reopen the picker, verify the picker and filters track the pending member",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@search"] },
    async ({ page }) => {
      const locators = await setup(page);

      // One account, one activation and one modal for all three checks. Run separately these each paid a
      // login, a navigation and a prepareMember, which was most of their runtime.
      const addedUser = requireMemberAccount("add");
      await prepareMember(locators, page, addedUser);
      await ensureGroupExists(locators, FIXTURE_GROUPS.members);
      await locators.openEditFor(FIXTURE_GROUPS.members);

      const options = page.locator('[role="option"]');

      // Ordered so each step sets up the next: search the picker while the user is still offered, add them,
      // then reopen the picker to prove they are gone. Nothing is saved, so the fixture is untouched.
      await test.step("a partial username offers the matching user, a fragment that cannot match offers none", async () => {
        await locators.membersPicker.click();
        await options.first().waitFor({ state: "visible", timeout: 15000 });

        // The ds Select renders its search box only above 8 options, and a tenant with few users stays under that.
        if ((await options.count()) > 8) {
          await locators.searchInMemberPicker(addedUser.email.slice(0, 4));
          await expect(locators.memberOption(addedUser.email)).toBeVisible();

          await locators.searchInMemberPicker("zzz_nonexistent_user_000");
          await expect(options).toHaveCount(0);
        }

        await page.keyboard.press("Escape");
      });

      await test.step("the added member is listed under Active and hidden under Inactive and Suspended", async () => {
        // The picker only offers active users, so anything added here is Active.
        await locators.addMember(addedUser.email);

        await locators.selectMemberFilter("Active");
        await expect(locators.getMemberRow(addedUser.email)).toBeVisible();

        await locators.selectMemberFilter("Inactive");
        await expect(locators.getMemberRow(addedUser.email)).toHaveCount(0);

        await locators.selectMemberFilter("Suspended");
        await expect(locators.getMemberRow(addedUser.email)).toHaveCount(0);

        await locators.selectMemberFilter("Active");
        await expect(locators.getMemberRow(addedUser.email)).toBeVisible();
      });

      await test.step("reopening the picker no longer offers the user just added", async () => {
        await locators.membersPicker.click();
        await expect(locators.memberOption(addedUser.email)).toHaveCount(0);
        await page.keyboard.press("Escape");
      });

      await locators.closeModal(); // discard — nothing was saved
    }
  );

  test(
    "User Groups - remove a member, close without saving, discard the guard, verify the member is still there",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud"] },
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
        await locators.cancelExpectingGuard(async () => {
          await locators.openEditFor(FIXTURE_GROUPS.members);
          await locators.removeMemberIfPresent(member.email);
        });
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
              await locators.removeMemberIfPresent(member.email);
            });
            await locators.expectSectionToast(GROUP_TOASTS.membersUpdated);
            await locators.closeModal();
          }
        } catch (error) {
          console.warn(`Could not remove the seeded member from "${FIXTURE_GROUPS.members}":`, errorSummary(error));
        }
      }
    }
  );

  // VALIDATION: the four name rules short-circuit in order, so each test below uses a value that reaches exactly the rule it checks.

  test(
    "User Groups - enter each kind of invalid and valid name in the add-group form, verify the inline error for each",
    { tag: ["@oss", "@dev", "@test", "@regression", "@validation", "@negative"] },
    async ({ page }) => {
      const locators = await setup(page);

      // One form open for every case: these only type into the name field and read the inline error, so
      // paying the login and navigation cost seven times bought nothing.
      await locators.openAddGroupForm();

      // `error: null` means the value is legal and no message should be rendered.
      const cases: { label: string; name: string; error: string | null }[] = [
        { label: "only spaces, which the required rule trims", name: "     ", error: GROUP_ERRORS.required },
        { label: "a leading dash, legal elsewhere in the name", name: "-abc12", error: GROUP_ERRORS.firstChar },
        { label: "four characters, one below the minimum", name: "abcd", error: GROUP_ERRORS.minLength },
        { label: "five characters, the first legal length", name: "abcde", error: null },
        { label: "special characters", name: "test@#$%", error: GROUP_ERRORS.alphaNum },
        { label: "dashes, underscores and spaces, all permitted", name: "test-grp_1 x", error: null },
        // Documents that no maximum is enforced; if a limit is ever added this case should fail and be updated.
        { label: "255 characters, since no maximum is enforced", name: "a".repeat(255), error: null },
      ];

      for (const { label, name, error } of cases) {
        await test.step(`${label} -> ${error ?? "no error"}`, async () => {
          await locators.nameInput.fill(name);
          // Soft: the cases are independent, so one wrong message must not hide the six behind it.
          if (error) await expect.soft(locators.nameError).toHaveText(error);
          else await expect.soft(locators.nameError).toHaveCount(0);
        });
      }

      await test.step("clearing a filled name reports it as required and submitting keeps the form open", async () => {
        // Validation runs on change, so the field has to be typed into before it can be emptied.
        await locators.nameInput.fill("Valid name");
        await locators.nameInput.fill("");
        await expect.soft(locators.nameError).toHaveText(GROUP_ERRORS.required);

        await locators.modalSubmitBtn.click();
        await expect.soft(locators.nameInput).toBeVisible();
      });

      await locators.closeModal();
    }
  );

  // ── DUPLICATE NAME ──

  test(
    "User Groups - add a group using an existing name, submit, verify the duplicate error inline and that no second group is created",
    { tag: ["@oss", "@dev", "@test", "@regression", "@negative", "@validation"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.roles);

      await locators.openAddGroupForm();
      await locators.nameInput.fill(FIXTURE_GROUPS.roles);

      // Uniqueness is checked on submit, not while typing, so the field stays clean until Create is pressed.
      await expect(locators.nameError).toHaveCount(0);
      await locators.modalSubmitBtn.click();

      // Rejected as a field error rather than a snackbar, and the modal stays open so the name can be corrected.
      await expect(locators.nameError).toHaveText(GROUP_ERRORS.duplicate);
      await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);
      await expect(locators.nameInput).toBeVisible();

      await locators.closeModal();

      // No second group was created under that name.
      await locators.searchGroup(FIXTURE_GROUPS.roles);
      await expect(locators.getGroupRow(FIXTURE_GROUPS.roles)).toHaveCount(1);
    }
  );

  // ── CANCEL / CLOSE ──

  test(
    "User Groups - fill the add-group form and abandon it with Cancel and with the X, verify neither creates a group and the form reopens blank",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await test.step("Cancel discards the form and creates nothing", async () => {
        const name = UNSAVED_GROUP_NAMES.cancelled;

        await locators.openAddGroupForm();
        await locators.nameInput.fill(name);
        await locators.descInput.fill("should never be saved");
        await locators.closeModal(); // Cancel

        await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);

        await locators.searchGroup(name);
        // The empty state first: the table blanks while the search refetches, so a bare toHaveCount(0) would
        // pass during loading and report "no group created" without the search having finished.
        await expect(page.getByText("No Data Available")).toBeVisible();
        await expect(locators.getGroupRow(name)).toHaveCount(0);

        // Reopening gives a clean form rather than the abandoned input.
        await locators.openAddGroupForm();
        await expect(locators.nameInput).toHaveValue("");
        await expect(locators.descInput).toHaveValue("");
        await locators.closeModal();
      });

      await test.step("the header X behaves exactly like Cancel", async () => {
        const name = UNSAVED_GROUP_NAMES.dismissed;

        await locators.openAddGroupForm();
        await locators.nameInput.fill(name);

        await locators.closeModalBtn.click();
        await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });

        await expect(locators.toast(GROUP_TOASTS.created)).toHaveCount(0);

        await locators.searchGroup(name);
        // Settled empty state before the count, for the same reason as the Cancel step above.
        await expect(page.getByText("No Data Available")).toBeVisible();
        await expect(locators.getGroupRow(name)).toHaveCount(0);
      });
    }
  );

  test(
    "User Groups - edit a description and close without saving, verify Continue Editing keeps the edit and Discard throws it away",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.info);
      const pendingDescription = "Kept by Continue Editing";

      try {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        // Read rather than assumed, so the test holds if a previous run left a different description.
        const originalDescription = await locators.descInput.inputValue();

        // Must stay dirty: an un-dirtied form closes without ever raising the guard this test is about.
        await locators.fillUntilDirty(locators.descInput, pendingDescription, locators.saveGroupInfoBtn);

        // One journey through both answers: the edit has to survive Continue Editing before Discard has
        // anything to throw away, so these read better together than as two tests that each set it up.
        const redirtyDescription = async () => {
          await locators.openEditFor(FIXTURE_GROUPS.info);
          await locators.fillUntilDirty(locators.descInput, pendingDescription, locators.saveGroupInfoBtn);
        };

        await test.step("Continue Editing dismisses the guard and leaves the edit in place", async () => {
          await locators.cancelExpectingGuard(redirtyDescription);

          await locators.continueEditingBtn.click();
          await expect(locators.unsavedGuard).toBeHidden();
          await expect(locators.descInput).toHaveValue(pendingDescription);

          // Still unsaved, so the section Save is still armed.
          await expect(locators.saveGroupInfoBtn).toBeEnabled();
        });

        await test.step("Discard closes without saving and the edit is gone on reopen", async () => {
          await locators.cancelExpectingGuard(redirtyDescription);
          await locators.discardChangesBtn.click();
          await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 });
          await locators.waitForBackdropGone();

          await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

          await locators.openEditFor(FIXTURE_GROUPS.info);
          await expect(locators.descInput).toHaveValue(originalDescription);
          await locators.closeModal();
        });
      } finally {
        // Leave without saving; closeModalIfOpen discards through the guard.
        await locators.closeModalIfOpen();
      }
    }
  );

  test(
    "User Groups - edit a description, close without saving, choose Save and Exit, verify the snackbar and that the edit persists",
    { tag: ["@oss", "@dev", "@test", "@regression", "@functional", "@crud", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.info);
      let editedDescription: string = SAVE_EXIT_DESCRIPTIONS[0];

      try {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        // Whichever value is not stored right now, so a leftover from a crashed run cannot make this a no-op.
        editedDescription = otherDescription(await locators.descInput.inputValue(), SAVE_EXIT_DESCRIPTIONS);
        await locators.fillUntilDirty(locators.descInput, editedDescription, locators.saveGroupInfoBtn);

        await locators.cancelExpectingGuard(async () => {
          await locators.openEditFor(FIXTURE_GROUPS.info);
          await locators.fillUntilDirty(locators.descInput, editedDescription, locators.saveGroupInfoBtn);
        });

        // Save & Exit saves every dirty section, then closes.
        await locators.saveAndExitBtn.click();
        await expect(locators.toastText(GROUP_TOASTS.infoUpdated)).toBeVisible();
        await locators.nameInput.waitFor({ state: "hidden", timeout: 15000 });
        await locators.waitForBackdropGone();

        // The edit was written, not discarded.
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await expect(locators.descInput).toHaveValue(editedDescription);
        await locators.closeModal();
      } finally {
        await restoreGroup(locators, [FIXTURE_GROUPS.info], {
          name: FIXTURE_GROUPS.info,
          description: FIXTURE_DESCRIPTIONS[FIXTURE_GROUPS.info],
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
    { tag: ["@oss", "@dev", "@test", "@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      const name = UNSAVED_GROUP_NAMES.failedCreate;
      await failOperation(page, "CreateUserGroup", [{ message: "Simulated backend failure" }]);

      await locators.openAddGroupForm();
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
      // Settled empty state before the count: a blank table mid-refetch would otherwise report "not created".
      await expect(page.getByText("No Data Available")).toBeVisible();
      await expect(locators.getGroupRow(name)).toHaveCount(0);
    }
  );

  test(
    "User Groups - save a section with the update request failing with and without an API message, verify each error is surfaced and nothing is written",
    { tag: ["@oss", "@dev", "@test", "@regression", "@negative", "@snackbar"] },
    async ({ page }) => {
      const locators = await setup(page);

      await ensureGroupExists(locators, FIXTURE_GROUPS.info);
      const apiMessage = "Simulated backend failure";
      const pendingDescription = "Will not save, the API is forced to fail";

      // Snapshot what the fixture holds right now rather than comparing against the canonical description:
      // earlier tests in the file legitimately leave this fixture on one of the EDIT_DESCRIPTIONS, so a
      // canonical comparison would fail here for something this test did not do. The only claim being made
      // is that these two forced-failure saves changed nothing, which is exactly before === after.
      await locators.openEditFor(FIXTURE_GROUPS.info);
      const descriptionBefore = await locators.descInput.inputValue();
      await locators.closeModal();

      await test.step("the API's own message is surfaced rather than swallowed", async () => {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await failOperation(page, "UpdateUserGroup", [{ message: apiMessage }]);

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.descInput.fill(pendingDescription);
        });

        await expect(page.getByText(apiMessage, { exact: true }).first()).toBeVisible();
        await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

        await page.unroute("**/api/graphql");
        await locators.closeModal();
      });

      await test.step("an error carrying no message falls back to the app's own wording", async () => {
        await locators.openEditFor(FIXTURE_GROUPS.info);
        await failOperation(page, "UpdateUserGroup", [{}]);

        await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
          await locators.descInput.fill(pendingDescription);
        });

        await expect(page.getByText("Failed to update group", { exact: true }).first()).toBeVisible();
        await expect(locators.toast(GROUP_TOASTS.infoUpdated)).toHaveCount(0);

        await page.unroute("**/api/graphql");
        await locators.closeModal();
      });

      // Neither failed save reached the database. Asserted as a positive match on the value read before the
      // two attempts: the previous check here was `not.toHaveValue(/will not save/)`, which could never match
      // "Will not save..." - capital W, no `i` flag - so the one line proving nothing was written passed even
      // if both writes had landed.
      await locators.openEditFor(FIXTURE_GROUPS.info);
      await expect(locators.descInput).toHaveValue(descriptionBefore);
      await locators.closeModal();
    }
  );
});
