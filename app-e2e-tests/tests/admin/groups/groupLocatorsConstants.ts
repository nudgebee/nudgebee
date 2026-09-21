import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { LoginPage } from "../../../pages/LoginPage";
import { UserLocators } from "../Users/usersLocators";

export const Groups = [
  { name: "Tenant Admin" },
  { name: "Tenant Readonly" },
  { name: "Account Admin" },
  { name: "Account Readonly" },
  { name: "K8 Admin" },
  { name: "K8 Readonly" },
];

// Groups cannot be deleted in the product, so these fixtures are created once and restored after each mutating test.
export const FIXTURE_GROUPS = {
  read: "e2e grp read", // never mutated
  update: "e2e grp update", // name/description edits
  members: "e2e grp members", // member add/remove
  rbac: "e2e grp rbac", // role assignment
} as const;

export const FIXTURE_DESCRIPTION = "e2e fixture group";

// Groups a test creates are named with this prefix, never with a fixture name inside them.
// The list search matches on substring, so "e2e grp members create <ts>" made every fixture
// search return dozens of throwaway rows, which is slower and more likely to time out.
export const THROWAWAY_PREFIX = "zz e2e";

// Members are the Users-spec accounts, already provisioned in .env.
// USER_3 is deliberately avoided: the Users name-edit test renames it, and a crashed restore strands it under the edited name.
// USER_1 and USER_2 only ever change role or status, which ensureUserActive already handles.
// Name is carried alongside the email because the users list can only be searched by name.
const MEMBER_SLOTS = { add: 1, remove: 2, discard: 1 } as const;

export type MemberAccount = { name: string; email: string };

// Names the key rather than the value: an email in a log or a Slack alert is an identity leak.
export function requireMemberAccount(slot: keyof typeof MEMBER_SLOTS): MemberAccount {
  const n = MEMBER_SLOTS[slot];
  const first = process.env[`USER_${n}_FIRST`] ?? "";
  const last = process.env[`USER_${n}_LAST`] ?? "";
  const email = process.env[`USER_${n}_EMAIL`] ?? "";
  if (!first || !last || !email) throw new Error(`USER_${n}_FIRST/_LAST/_EMAIL are not all set - add them to .env and .env.dev`);
  return { name: `${first} ${last}`, email };
}

// Inline field errors under the group-name input (#groupname-error), NOT toasts.
export const GROUP_ERRORS = {
  required: "This field required",
  firstChar: "Should start with an alphabet or a digit",
  minLength: "Name should have atleast 5 characters",
  alphaNum: "This field should be alpha-numeric",
  duplicate: "Group name already in use",
} as const;

// The edit modal saves per section, so each section reports its own success message.
export const GROUP_TOASTS = {
  created: "Group added successfully",
  infoUpdated: "Group details updated",
  tenantUpdated: "Tenant permissions updated",
  membersUpdated: "Members updated",
} as const;

// Usernames are email addresses, so they carry regex metacharacters that must be escaped.
function escapeForRegex(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

export class GroupLocators extends CommonLocators {
  readonly groupsTab!: Locator;
  readonly newUserGroupIdentifier!: Locator;
  readonly addUserGroupBtn!: Locator;
  readonly groupNameInput!: Locator;
  readonly descriptionInput!: Locator;
  readonly createGroupBtn!: Locator;
  readonly group_creation_successMsg!: Locator;
  readonly group_creation_duplicateMsg!: Locator;

  // id-based so they resolve in both create and edit mode; the placeholder locators above are create-only.
  readonly nameInput!: Locator;
  readonly descInput!: Locator;
  readonly nameError!: Locator;
  readonly modalTitle!: Locator;
  readonly modalSubmitBtn!: Locator;
  readonly modalCancelBtn!: Locator;
  readonly closeModalBtn!: Locator;
  readonly groupSearchInput!: Locator;
  readonly membersPicker!: Locator;
  readonly selectedUsersTable!: Locator;
  readonly tenantRoleSelect!: Locator;
  readonly toastRegion!: Locator;
  readonly saveGroupInfoBtn!: Locator;
  readonly saveTenantRolesBtn!: Locator;
  readonly saveMembersBtn!: Locator;
  readonly unsavedGuard!: Locator;
  readonly discardChangesBtn!: Locator;
  readonly continueEditingBtn!: Locator;
  readonly saveAndExitBtn!: Locator;

  constructor(page: Page) {
    super(page);

    this.groupsTab = page.locator("#anchor-tab-Groups");
    this.newUserGroupIdentifier = page.locator("#new-user-group");

    this.addUserGroupBtn = page.getByText("Add User Group");
    this.groupNameInput = page.getByRole("textbox", { name: "e.g. Platform-Eng" });
    this.descriptionInput = page.getByRole("textbox", { name: "What is this group for? (optional)" });
    this.createGroupBtn = page.getByRole("button", { name: "Create Group" });

    this.group_creation_successMsg = page.getByText("Group added successfully").first();
    this.group_creation_duplicateMsg = page.getByText("Group name already in use").first();

    // No data-testid on these fields and the placeholders exist only in create mode, so an id primary is the highest safe rung.
    this.nameInput = page.locator("#groupname");
    this.descInput = page.locator("#description");
    // Rendered only while invalid, so a wider text match would collide with the same words elsewhere on the form.
    this.nameError = page.locator("#groupname-error");
    // Shared by the edit modal and the unsaved-changes guard, so this resolves to whichever dialog is on top.
    this.modalTitle = page.locator("#alert-dialog-title").first();
    // Label differs by mode (Create group / Save changes), so the role fallback matches either.
    this.modalSubmitBtn = page
      .locator("#submit")
      .or(page.getByRole("button", { name: /^(Create group|Save changes)$/ }))
      .first();
    // Labelled Cancel in create mode and Close in edit mode.
    this.modalCancelBtn = page
      .locator("#cancel")
      .or(page.getByRole("button", { name: /^(Cancel|Close)$/ }))
      .first();
    this.closeModalBtn = page.locator("#close-modal-btn").or(page.getByRole("button", { name: "Close" })).first();
    this.groupSearchInput = page.locator("#user-groups-search").or(page.getByPlaceholder("Enter Name")).first();
    this.membersPicker = page.locator("#all-users-for-group").or(page.getByPlaceholder("Add active user")).first();
    // No accessible name and no unique text of its own, so the id is the only stable handle.
    this.selectedUsersTable = page.locator("#selected-users");
    this.tenantRoleSelect = page.locator("#group-tenant-role").or(page.getByPlaceholder("Select role(s)")).first();
    this.toastRegion = page.getByRole("region", { name: "Notifications" });

    // Edit mode has no combined submit: each card saves itself and stays disabled until that section is dirty.
    this.saveGroupInfoBtn = page.getByTestId("save-group-info");
    this.saveTenantRolesBtn = page.getByTestId("save-tenant-roles");
    this.saveMembersBtn = page.getByTestId("save-group-members");

    // Guard shown on closing a dirty edit modal; matched on body copy because its title reuses #alert-dialog-title.
    this.unsavedGuard = page.getByText("You have unsaved changes. Do you want to save your changes before leaving?");
    this.discardChangesBtn = page.getByTestId("unsaved-discard-changes");
    this.continueEditingBtn = page.getByTestId("unsaved-continue-editing");
    this.saveAndExitBtn = page.getByTestId("unsaved-save-exit");
  }

  // For asserting a toast is ABSENT; scoping stops it matching the same words rendered inline on the form.
  toast(message: string): Locator {
    return this.toastRegion.getByText(message, { exact: true });
  }

  // For asserting presence; page-wide because the notifications container markup differs between builds.
  toastText(message: string): Locator {
    return this.page.getByText(message, { exact: true }).first();
  }

  // Multi-select picker of the built-in role plus custom roles; it stays open after a choice, so Escape closes it.
  async selectTenantRole(label: string): Promise<void> {
    // Options expose no accessible name, so match exact text, anchored so "Admin" cannot also match "ReadOnly Admin".
    const option = this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${label}$`) })
      .first();

    // armAndClickSave replays this callback, so open the picker only when it is shut: clicking the trigger
    // of an already-open menu lands on the popover backdrop and times out. The options are the reliable
    // signal here - the trigger keeps aria-expanded="true" after the menu closes.
    if (!(await option.isVisible().catch(() => false))) {
      await this.tenantRoleSelect.click();
    }
    await option.click();

    // Escape goes to the option so it reaches the menu; a page-level press depends on where focus landed.
    // Best effort on purpose: a menu left open is only a problem for the next click, which retries anyway.
    await option.press("Escape").catch(() => {});
    await option.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  // Clears all selected roles; the inline clear renders only while something is selected.
  // Returns whether anything was actually cleared, so a caller can tell a real edit from a no-op.
  async clearTenantRoles(): Promise<boolean> {
    const clear = this.tenantRoleSelect.getByRole("button", { name: "Clear selection" });
    if (!(await clear.isVisible().catch(() => false))) return false;
    await clear.click();
    return true;
  }

  // ── Members section ──

  // A row in the members table, matched by username.
  getMemberRow(username: string): Locator {
    return this.selectedUsersTable.locator("tr").filter({ hasText: username });
  }

  // Row remove control: no id or testid, only the icon alt text, so it is reached through the row.
  getMemberDeleteBtn(username: string): Locator {
    return this.getMemberRow(username).locator("button").filter({ has: this.page.locator('img[alt="delete icon"]') });
  }

  // Adds one named member. Throws when the account is not offered rather than silently substituting another user.
  // Clears a leftover membership first, in the modal that is already open: a crashed run leaves the account in the group,
  // and the picker will not offer someone who is already a member.
  async addMember(email: string): Promise<void> {
    const alreadyMember = await this.getMemberRow(email)
      .waitFor({ state: "visible", timeout: 3000 })
      .then(() => true)
      .catch(() => false);
    if (alreadyMember) {
      await this.getMemberDeleteBtn(email).click();
      await expect(this.getMemberRow(email)).toHaveCount(0);
    }

    await this.membersPicker.click();
    const option = this.memberOption(email);
    const offered = await option
      .first()
      .waitFor({ state: "visible", timeout: 15000 })
      .then(() => true)
      .catch(() => false);
    if (!offered) {
      await this.page.keyboard.press("Escape");
      // Names the key, never the address, so the failure cannot leak an identity into CI logs or Slack.
      throw new Error("Member account is not offered by the picker - it may already be a member or not be Active");
    }
    await option.first().click();
    await this.page.keyboard.press("Escape");
  }

  // Picker option matched on exact username; options expose no accessible name and usernames need regex escaping.
  memberOption(username: string): Locator {
    return this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^${escapeForRegex(username)}$`) });
  }

  // Type into the picker's own search box. Only rendered while the picker is open.
  async searchInMemberPicker(text: string): Promise<void> {
    // Substring match — the real placeholder ends in a "…" character.
    await this.page.getByPlaceholder("Search active users").fill(text);
  }

  // Member status filter, matched exactly because "Active" is a substring of "Inactive".
  async selectMemberFilter(status: "Active" | "Inactive" | "Suspended"): Promise<void> {
    await this.page.getByRole("radio", { name: status, exact: true }).click();
  }

  // Applies the edit and clicks the section Save as one retried unit; the modal can re-fill its fields between the two and re-disable Save.
  async armAndClickSave(saveBtn: Locator, edits: () => Promise<void>, attempts = 3): Promise<void> {
    for (let attempt = 1; attempt <= attempts; attempt++) {
      await edits();

      const armed = await expect(saveBtn)
        .toBeEnabled({ timeout: 3000 })
        .then(() => true)
        .catch(() => false);
      if (!armed) continue;

      try {
        await saveBtn.click({ timeout: 5000 });
        return;
      } catch {
        // Disabled again between arming and clicking — redo the edit and retry.
      }
    }
    // Final attempt asserts, so an exhausted retry reports the button real state rather than a bare click timeout.
    await expect(saveBtn).toBeEnabled();
    await saveBtn.click();
  }

  // Assert the outcome of a section save that has already been clicked.
  async expectSectionToast(expectedToast: string): Promise<void> {
    // Matched page-wide: scoping to the notifications region made this depend on container markup that differs between builds.
    const toastText = this.page.getByText(expectedToast, { exact: true }).first();

    // A rejected save shows an inline field error and no toast, so wait for whichever outcome arrives first.
    await toastText.or(this.nameError).first().waitFor({ state: "visible", timeout: 20000 });

    if (await this.nameError.isVisible().catch(() => false)) {
      throw new Error(`Save was rejected with inline error: "${(await this.nameError.textContent())?.trim()}"`);
    }
    await expect(toastText).toBeVisible();
  }

  // Exact cell match: substring matching would make "e2e grp update" also match "e2e grp update renamed".
  getGroupRow(name: string): Locator {
    return this.page.locator("tr").filter({ has: this.page.getByText(name, { exact: true }) });
  }

  // Row Edit button: no id or testid, only aria-label, so it is reached through the row.
  getEditBtnForGroup(name: string): Locator {
    return this.getGroupRow(name).getByRole("button", { name: "Edit group" });
  }

  // Search fires on Enter or on clear, never on keystroke.
  async searchGroup(name: string): Promise<void> {
    // A just-closed dialog can still have its backdrop animating out, and that backdrop swallows the click.
    await this.waitForBackdropGone();
    await this.groupSearchInput.click(); // ensure focus so Enter reliably lands
    await this.groupSearchInput.fill(name);
    await this.groupSearchInput.press("Enter");
  }

  async clearGroupSearch(): Promise<void> {
    await this.groupSearchInput.fill("");
    await this.groupSearchInput.press("Enter");
  }

  // Search for a group and open its edit modal.
  async openEditFor(name: string): Promise<void> {
    // The list refetch is keyed on the search term, so pressing Enter on unchanged text is a no-op in React.
    // A retry therefore has to clear the box first, or it re-displays the same stale result.
    for (let attempt = 1; attempt <= 3; attempt++) {
      if (attempt > 1) await this.clearGroupSearch();
      await this.searchGroup(name);
      const listed = await this.getGroupRow(name)
        .waitFor({ state: "visible", timeout: 10000 })
        .then(() => true)
        .catch(() => false);
      if (listed) break;
      if (attempt === 3) await expect(this.getGroupRow(name)).toBeVisible();
    }
    await this.getEditBtnForGroup(name).click();
    await this.nameInput.waitFor({ state: "visible", timeout: 15000 });
    // The modal re-populates Group name and Description when its accounts fetch lands, so let that settle before typing.
    await this.page.waitForLoadState("networkidle").catch(() => {});
  }

  // Fills a field and confirms the value stuck; losing it also clears the section dirty flag and re-disables Save.
  async fillStable(field: Locator, value: string, attempts = 3): Promise<void> {
    for (let attempt = 1; attempt < attempts; attempt++) {
      await field.fill(value);
      try {
        await expect(field).toHaveValue(value, { timeout: 2000 });
        return;
      } catch {
        // The modal repopulated the field mid-type — enter it again.
      }
    }
    await field.fill(value);
    await expect(field).toHaveValue(value); // final attempt surfaces a real failure
  }

  async closeModal(): Promise<void> {
    await this.modalCancelBtn.click();
    await this.dismissUnsavedGuard();
    await this.nameInput.waitFor({ state: "hidden", timeout: 10000 });
    await this.waitForBackdropGone();
  }

  // Closing a dirty edit modal raises the unsaved-changes guard; every close here means leave without saving, so discard.
  async dismissUnsavedGuard(): Promise<void> {
    // Race the two outcomes rather than waiting out a fixed timeout: a clean modal just closes, and a
    // dirty one cannot close until the guard is dismissed, so whichever happens first ends the wait.
    const discard = this.page.getByTestId("unsaved-discard-changes");
    await Promise.race([
      this.nameInput.waitFor({ state: "hidden", timeout: 5000 }),
      discard.waitFor({ state: "visible", timeout: 5000 }),
    ]).catch(() => {});

    if (await discard.isVisible().catch(() => false)) {
      await discard.click();
    }
  }

  // The backdrop outlives the dialog content and swallows pointer events, so wait for it to detach.
  async waitForBackdropGone(timeout = 10000): Promise<void> {
    await this.page
      .locator(".MuiModal-backdrop")
      .first()
      .waitFor({ state: "detached", timeout })
      .catch(() => {});
  }

  // Dismisses a modal left open by a failed test, so the restore can reach the list behind it.
  async closeModalIfOpen(): Promise<void> {
    if (!(await this.nameInput.isVisible().catch(() => false))) return;
    await this.modalCancelBtn.click().catch(() => {});
    await this.dismissUnsavedGuard().catch(() => {});
    await this.nameInput.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
    await this.waitForBackdropGone(5000);
  }
}

// Logs in (session reused via global-setup) and lands on the Admin > Groups tab.
export async function setup(page: Page): Promise<GroupLocators> {
  const locators = new GroupLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.homeBtn.click();
  await locators.adminBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.adminBtn.click();
  // The sidenav click can land before React attaches its handler, so navigate directly rather than fail on nav flake.
  await page.waitForURL("**/user-management**", { timeout: 20000 }).catch(async () => {
    await page.goto("/user-management");
    await page.waitForURL("**/user-management**", { timeout: 20000 });
  });
  await locators.groupsTab.click();
  await locators.newUserGroupIdentifier.waitFor({ state: "visible", timeout: 15000 });
  return locators;
}

// Makes a member account Active before it is used: the picker lists only Active users, and the Users spec normalises its accounts to Inactive.
export async function ensureUserActive(page: Page, account: MemberAccount): Promise<void> {
  const users = new UserLocators(page);
  await users.adminBtn.click();
  await page.waitForURL("**/user-management**", { timeout: 20000 }).catch(async () => {
    await page.goto("/user-management");
    await page.waitForURL("**/user-management**", { timeout: 20000 });
  });

  for (const status of ["Active", "Inactive", "Suspended"] as const) {
    await users.selectStatusFilter(status);
    await users.searchByName(account.name);
    const found = await users
      .getUserRow(account.email)
      .waitFor({ state: "visible", timeout: 5000 })
      .then(() => true)
      .catch(() => false);
    if (!found) continue;
    if (status === "Active") return;

    await users.getEditBtnForUser(account.email).click();
    await users.editUserModal.waitFor({ state: "visible", timeout: 15000 });
    await users.setUserStatus("Active");
    await users.addUserSubmitBtn.click();
    await users.editUserModal.waitFor({ state: "hidden", timeout: 15000 });
    return;
  }

  // Names the key, never the address, so a missing account cannot leak an identity into CI logs.
  throw new Error("Member account not found under any status - run the Users spec first, or check USER_n_EMAIL in .env");
}

// Remembers which accounts this worker already activated, so six tests do not each pay for the Users round trip.
const activatedAccounts = new Set<string>();

// Activates the account once per worker, then returns to the Groups tab ready for member work.
export async function prepareMember(locators: GroupLocators, page: Page, account: MemberAccount): Promise<void> {
  if (activatedAccounts.has(account.email)) return;
  await ensureUserActive(page, account);
  activatedAccounts.add(account.email);
  await locators.groupsTab.click();
  await locators.newUserGroupIdentifier.waitFor({ state: "visible", timeout: 15000 });
}

// Ensures the fixture exists under its canonical name, renaming it back if a crashed run left it under an alias.
export async function ensureGroupExists(
  locators: GroupLocators,
  name: string,
  altNames: string[] = []
): Promise<void> {
  // 15s because a slow list response would otherwise look like a missing group and trigger a duplicate create.
  const findRow = async (candidate: string) => {
    await locators.searchGroup(candidate);
    return locators
      .getGroupRow(candidate)
      .waitFor({ state: "visible", timeout: 15000 })
      .then(() => true)
      .catch(() => false);
  };

  if (await findRow(name)) return;

  for (const alias of altNames) {
    if (!(await findRow(alias))) continue;
    console.log(`Fixture "${name}" was left renamed as "${alias}" - restoring it.`);
    await locators.openEditFor(alias);
    await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
      await locators.fillStable(locators.nameInput, name);
    });
    await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
    await locators.closeModal();
    await locators.searchGroup(name);
    await locators.getGroupRow(name).waitFor({ state: "visible", timeout: 15000 });
    return;
  }

  await locators.newUserGroupIdentifier.click();
  await locators.nameInput.fill(name);
  await locators.descInput.fill(FIXTURE_DESCRIPTION);
  await locators.modalSubmitBtn.click();

  // A duplicate rejection is fine here: this helper only guarantees the group exists.
  await locators
    .toastText(GROUP_TOASTS.created)
    .or(locators.nameError)
    .first()
    .waitFor({ state: "visible", timeout: 20000 });

  if (await locators.nameError.isVisible().catch(() => false)) {
    const message = (await locators.nameError.textContent())?.trim();
    if (message !== GROUP_ERRORS.duplicate) {
      throw new Error(`Could not create fixture "${name}": ${message}`);
    }
    console.log(`Fixture "${name}" already existed; the earlier search missed it.`);
    await locators.closeModal();
  } else {
    await locators.nameInput.waitFor({ state: "hidden", timeout: 10000 }).catch(() => {});
  }

  await locators.searchGroup(name);
  await locators.getGroupRow(name).waitFor({ state: "visible", timeout: 15000 });
}

// Best-effort restore that never throws, so a cleanup failure cannot bury the assertion that failed the test.
export async function restoreGroup(
  locators: GroupLocators,
  candidateNames: string[],
  original: { name: string; description: string }
): Promise<void> {
  try {
    await locators.closeModalIfOpen();

    for (const candidate of candidateNames) {
      await locators.searchGroup(candidate);
      const found = await locators
        .getGroupRow(candidate)
        .waitFor({ state: "visible", timeout: 5000 })
        .then(() => true)
        .catch(() => false);
      if (!found) continue;

      // Reuses openEditFor so the restore gets the same re-search and settle wait as the test body.
      await locators.openEditFor(candidate);
      await locators.fillStable(locators.nameInput, original.name);
      await locators.fillStable(locators.descInput, original.description);

      // Already at baseline: nothing is dirty, so Save stays disabled and there is nothing to write.
      if (!(await locators.saveGroupInfoBtn.isEnabled().catch(() => false))) {
        await locators.closeModal();
        return;
      }

      await locators.armAndClickSave(locators.saveGroupInfoBtn, async () => {
        await locators.fillStable(locators.nameInput, original.name);
        await locators.fillStable(locators.descInput, original.description);
      });
      await locators.expectSectionToast(GROUP_TOASTS.infoUpdated);
      await locators.closeModal();
      return;
    }

    console.warn(`Restore skipped - group not found under: ${candidateNames.join(", ")}`);
  } catch (error) {
    console.warn(`Restore of group "${original.name}" failed - may need manual cleanup:`, error);
  }
}
