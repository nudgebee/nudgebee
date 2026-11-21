import { clickSidenavRail } from "../../utils/helpers";
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

// First line of a caught error for best-effort cleanup warnings; logging the raw error dumps its matcherResult and page snapshot (~300 lines) into CI.
export function errorSummary(error: unknown): string {
  const message = error instanceof Error ? error.message : String(error);
  return message.split("\n")[0];
}

// Groups cannot be deleted in the product, so these fixtures are created once and reused by every later run.
// Each key names the one thing that mutates its group; read-only tests may share a group with its mutator.
// "E2E" prefix on purpose: the tenant already has real groups called "New Test Group 1..7" and
// "test group1", and the list search matches on substring, so a bare "Test Group 1" pulled them all in.
export const FIXTURE_GROUPS = {
  roles: "E2E Test Group 1", // tenant role assignment
  info: "E2E Test Group 2", // name/description edits
  members: "E2E Test Group 3", // member add/remove
} as const;

// Written when a fixture is first created, so anyone finding one in the listing knows why it is there.
export const FIXTURE_DESCRIPTIONS: Record<string, string> = {
  [FIXTURE_GROUPS.roles]: "E2E: listing checks and tenant role tests.",
  [FIXTURE_GROUPS.info]: "E2E: name and description edit tests.",
  [FIXTURE_GROUPS.members]: "E2E: member add and remove tests.",
};

// Cloud accounts are named per environment, so they come from .env like every other environment value.
// CLUSTER already holds the k8s account name on both (k8s-dev on dev, iteration-test on test); AWS_CLUSTER_NAME
// holds the AWS one and is only set on dev, which is why the multi-account test is tagged @dev alone.
export function requireAccountName(key: "CLUSTER" | "AWS_CLUSTER_NAME"): string {
  const value = process.env[key] ?? "";
  if (!value) throw new Error(`${key} is not set - add it to .env and .env.dev`);
  return value;
}

// Names a crashed run can leave a fixture stranded under. Held here rather than inside the test that does the
// renaming, because whichever test runs first after the crash has to heal it: one that did not know the alias
// simply found the canonical name missing and created a second group, leaving a permanent duplicate behind.
export const FIXTURE_ALIASES: Record<string, string[]> = {
  [FIXTURE_GROUPS.info]: [`${FIXTURE_GROUPS.info} renamed`],
};

// A test that saves a description must always change the stored value, or the section never goes dirty and
// Save stays inert. A crashed run can leave either value behind, so each saving test alternates between two
// fixed strings and picks whichever is not currently stored - the same guarantee a timestamp gave, without
// writing throwaway data.
export const EDIT_DESCRIPTIONS = ["Edited by the description test", "Edited by the description test, again"] as const;
export const SAVE_EXIT_DESCRIPTIONS = ["Saved via Save and Exit", "Saved via Save and Exit, again"] as const;

export function otherDescription(current: string, pair: readonly [string, string]): string {
  return current === pair[0] ? pair[1] : pair[0];
}

// Typed into the add-group form by tests that never finish creating, so no row should ever carry these names.
// Fixed rather than timestamped on purpose: if one of these does leak, the next run fails on the duplicate
// instead of quietly adding another undeletable row.
export const UNSAVED_GROUP_NAMES = {
  cancelled: "E2E Test Group Never Saved cancel",
  dismissed: "E2E Test Group Never Saved close",
  failedCreate: "E2E Test Group Never Saved failed",
} as const;

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
  accountUpdated: "Account permissions updated",
  membersUpdated: "Members updated",
} as const;

// The picker offers labels, but the selected-accounts table renders the stored value for a built-in role -
// only custom roles show their display name there. Confirmed against the rendered table.
export const ACCOUNT_ROLE_VALUES = {
  "ReadOnly Admin": "account_admin_readonly",
  Admin: "account_admin",
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
  readonly emptyState!: Locator;
  readonly listingRows!: Locator;
  readonly membersPicker!: Locator;
  readonly selectedUsersTable!: Locator;
  readonly dropdownOption!: Locator;
  readonly tenantRoleSelect!: Locator;
  readonly toastRegion!: Locator;
  readonly accountRbacTab!: Locator;
  readonly accountSelect!: Locator;
  readonly accountRoleSelect!: Locator;
  readonly addAccountRoleBtn!: Locator;
  readonly selectedAccountsTable!: Locator;
  readonly saveAccountRolesBtn!: Locator;
  readonly saveGroupInfoBtn!: Locator;
  readonly saveTenantRolesBtn!: Locator;
  readonly saveMembersBtn!: Locator;
  readonly unsavedGuard!: Locator;
  readonly discardChangesBtn!: Locator;
  readonly continueEditingBtn!: Locator;
  readonly saveAndExitBtn!: Locator;

  constructor(page: Page) {
    super(page);

    // Groups is a sub-tab of Access & Users now (AnchorComponent's 2-level hash routing), rendered by the
    // shared Tabs.jsx as a real MUI Tab rather than its own top-level anchor-tab-<name> button.
    // The fallback matches on text, not on role name: these tabs expose no accessible name - the label sits
    // in a nested element - so getByRole("tab", { name }) resolves to nothing and would be a dead fallback.
    this.groupsTab = page
      .locator("#groups")
      .or(page.locator('[role="tab"]').filter({ hasText: /^Groups$/ }))
      .first();
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
    // Text-only on purpose: the empty state is rendered by the shared table with no id, testid or role.
    this.emptyState = page.getByText("No Data Available").first();
    // Body rows of the groups listing, used to tell one rendered result set from another.
    this.listingRows = page.locator("tbody tr");
    this.membersPicker = page.locator("#all-users-for-group").or(page.getByPlaceholder("Add active user")).first();
    // No accessible name and no unique text of its own, so the id is the only stable handle.
    this.selectedUsersTable = page.locator("#selected-users");
    this.dropdownOption = page.locator('[role="option"]');
    this.tenantRoleSelect = page.locator("#group-tenant-role").or(page.getByPlaceholder("Select role(s)")).first();
    this.toastRegion = page.getByRole("region", { name: "Notifications" });

    // Edit mode has no combined submit: each card saves itself and stays disabled until that section is dirty.
    this.saveGroupInfoBtn = page.getByTestId("save-group-info");
    this.saveTenantRolesBtn = page.getByTestId("save-tenant-roles");
    this.saveAccountRolesBtn = page.getByTestId("save-account-roles");

    // Id-only on purpose. The Assign Roles card carries testids on its save buttons and nowhere else, and
    // these three expose no accessible name - their labels sit in nested elements, confirmed against the
    // rendered DOM. A role or text fallback here would resolve to the wrong element rather than a slower one.
    this.accountRbacTab = page.locator("#tab-account");
    this.accountSelect = page.locator("#group-account");
    this.accountRoleSelect = page.locator("#group-account-role");
    // No id and no accessible name, and the label sits in a child element so the button's own text carries
    // surrounding whitespace - hence the anchored regex with \s*. Anchored so "Add User Group" and
    // "Add active user" cannot match.
    this.addAccountRoleBtn = page.locator("button").filter({ hasText: /^\s*Add\s*$/ }).first();
    // Same reason as the three above, plus this table only exists once a row has been added.
    this.selectedAccountsTable = page.locator("#selected-accounts");
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

  // An option in whichever dropdown is currently open, matched on its exact label.
  //
  // Scoped to the open listbox rather than the page, because the tenant and the account role pickers offer the
  // SAME two labels ("Admin", "ReadOnly Admin") - a page-wide match can resolve into the wrong picker, and both
  // are mounted inside the same dialog. The ds Select gives an option no id, testid or data attribute carrying
  // its value (it passes only a React key), so the label is the only thing there is to match on.
  //
  // `.first()` resolves the other collision, between a built-in role and a custom role of the same name: the
  // modal builds its options as [...BUILT_INS, ...customRoles], and a single-select list renders them in that
  // order, so the built-in is always the earlier match. If that order ever changes this picks the custom role
  // instead, and the row assertions - which expect the stored value, not a display name - are what will say so.
  openDropdownOption(label: string): Locator {
    return this.page
      .locator('[role="listbox"]:visible')
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${escapeForRegex(label)}$`) })
      .first();
  }

  // Multi-select picker of the built-in role plus custom roles; it stays open after a choice, so Escape closes it.
  async selectTenantRole(label: string): Promise<void> {
    const option = this.openDropdownOption(label);

    // armAndClickSave replays this callback, so open the picker only when it is shut: clicking the trigger
    // of an already-open menu lands on the popover backdrop and times out. The options are the reliable
    // signal here - the trigger keeps aria-expanded="true" after the menu closes.
    if (!(await option.isVisible().catch(() => false))) {
      await this.tenantRoleSelect.click();
    }

    // Clicked unconditionally. Guarding on the option's aria-selected looked like the way to make this
    // idempotent for armAndClickSave's replay, but on the test env that attribute read "true" while the
    // trigger still showed its placeholder, so the click was skipped and the role was never set - a latent
    // risk traded for a real failure. The replay hazard is covered by armAndClickSave's documented contract.
    await option.click();

    // Escape goes to the option so it reaches the menu; a page-level press depends on where focus landed.
    // Best effort on purpose: a menu left open is only a problem for the next click, which retries anyway.
    await option.press("Escape").catch(() => {});
    await option.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
  }

  // Picks the first account offered and gives it the named role. Returns the account name so the caller can
  // assert on identity; which account it is does not matter, only that the assignment survives.
  // Dismisses whichever dropdown is open in the Assign Roles card, without disturbing anything else.
  // Escape closes it but also resets the Tenant/Account tab, which unmounts the panel and drops the
  // selections with it. Clicking the trigger again is refused while its own popover covers it. So: a raw
  // mouse click inside the modal but outside the dropdown, which lands on the popover backdrop and dismisses
  // it. Raw because a normal click would be turned away by that same backdrop.
  async closeOpenDropdown(): Promise<void> {
    const box = await this.page.getByText("Assign Roles").first().boundingBox();
    if (box) await this.page.mouse.click(box.x + box.width / 2, box.y + box.height / 2);
  }

  // Picks one account out of the open accounts dropdown and returns the name it used.
  //
  // The tenant decides the dropdown's shape, so both are handled rather than assumed. The ds Select renders
  // its search box only above 8 options, and drops the provider group headers entirely when every account
  // belongs to one provider. dev holds 357 accounts across 6 providers, so it gets search plus collapsed
  // headers; a tenant with a handful gets no search, and either bare options or a header per provider it
  // happens to have. Nothing here names a provider - whatever headers exist get opened - so k8s alone,
  // k8s + gcp, or gcp + aws without k8s all behave the same.
  private async pickAccount(wanted?: string): Promise<string> {
    const listbox = this.page.locator('[role="listbox"]:visible');
    const search = listbox.getByPlaceholder("Search…");
    const options = listbox.locator('[role="option"]');
    const headers = listbox.locator('[role="button"]');

    // Wait for the list to arrive before reading its shape. Which shape it is depends on how many options
    // there are, and while the accounts are still being fetched there are none - so an instant check sees no
    // search box, no headers and no options, and picks the wrong branch. Whichever shape lands, one of these
    // three is there, so this is a wait that always resolves.
    await search.or(headers.first()).or(options.first()).first().waitFor({ state: "visible", timeout: 20000 });

    if (await search.isVisible().catch(() => false)) {
      // Typing is what makes an option reachable here: it filters AND auto-expands every group, where an empty
      // search leaves them all collapsed. So this branch cannot fall back to "take whatever is listed first".
      if (!wanted) throw new Error("This tenant's account dropdown is searchable, so an account name is required");
      await search.fill(wanted);
    } else {
      // No search box, so the options have to be uncovered by hand. Expanding every header covers both
      // remaining shapes and is cheap, since this shape only happens on a tenant with 8 or fewer accounts.
      // The header is the only [role="button"] in this popover - the Select-All row is a native <button>,
      // which an attribute selector does not match - so this cannot toggle anything else. Headers are always
      // collapsed on open (the Select resets its group state whenever the popup closes), so a click opens.
      for (let i = 0, headerCount = await headers.count(); i < headerCount; i++) {
        await headers.nth(i).click();
      }
    }

    // The named account when one was asked for and the tenant actually has it, otherwise whatever is listed
    // first - which is what a tenant too small to search needs, and is stable within a run.
    const named = wanted ? this.openDropdownOption(wanted) : options.first();
    const option = (await named.isVisible().catch(() => false)) ? named : options.first();

    await option.waitFor({ state: "visible", timeout: 15000 });
    const accountName = ((await option.textContent()) ?? "").trim();
    await option.click();
    return accountName;
  }

  // Gives one account the named role. Returns the account it used, so the caller asserts against what was
  // really picked rather than what it hoped for.
  async addAccountRole(roleLabel: string, wanted?: string): Promise<string> {
    await this.accountSelect.click();
    const accountName = await this.pickAccount(wanted);
    await this.closeOpenDropdown();

    await this.accountRoleSelect.click();
    await this.openDropdownOption(roleLabel).click();
    await this.closeOpenDropdown();

    await this.addAccountRoleBtn.click();
    await expect(this.selectedAccountsTable).toContainText(accountName);
    return accountName;
  }

  // A row in the selected-accounts table, matched by account name.
  getAccountRoleRow(accountName: string): Locator {
    return this.selectedAccountsTable.locator("tbody tr").filter({ hasText: accountName });
  }

  // Role is the second cell. Read it on its own because "account_admin" is a prefix of
  // "account_admin_readonly", so a substring assertion on the whole row passes for either role.
  getAccountRoleCell(accountName: string): Locator {
    return this.getAccountRoleRow(accountName).getByRole("cell").nth(1);
  }

  // Row remove control: no id or testid, only the icon alt text, so it is reached through the row.
  getAccountRoleDeleteBtn(accountName: string): Locator {
    return this.getAccountRoleRow(accountName)
      .locator("button")
      .filter({ has: this.page.locator('img[alt="delete icon"]') });
  }

  // Removes every assigned account role and saves. Best effort: a test that failed before assigning anything
  // leaves nothing to clear, and the Save stays inert in that case.
  async clearAccountRoles(groupName: string): Promise<void> {
    try {
      await this.closeModalIfOpen();
      await this.openEditFor(groupName);
      await this.accountRbacTab.click();

      const rows = this.selectedAccountsTable.locator("tbody tr");
      if ((await rows.count()) === 0) {
        await this.closeModal();
        return;
      }

      // Bounded, matched on the delete icon rather than "whatever the last button in the row is", and asserting
      // the row count actually dropped. Unbounded, a click that stops removing rows spins until the 300s test
      // timeout and reports that instead of the cause. Rows are addressed by position, not by account name:
      // one account can hold several rows at once, since custom roles stack alongside the built-in one.
      for (let i = 0; i < 10; i++) {
        const before = await rows.count();
        if (before === 0) break;
        await rows.first().locator("button").filter({ has: this.page.locator('img[alt="delete icon"]') }).first().click();
        await expect(rows).toHaveCount(before - 1);
      }

      await this.saveAccountRolesBtn.click();
      await this.expectSectionToast(GROUP_TOASTS.accountUpdated);
      await this.closeModal();
    } catch (error) {
      console.warn(`Could not clear account roles on "${groupName}":`, errorSummary(error));
    }
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

  // Safe to hand to armAndClickSave, which replays its callback: the row is gone on the second pass, and a
  // bare click on it would time out and throw from inside the helper instead of being retried.
  async removeMemberIfPresent(email: string): Promise<void> {
    if (await this.getMemberRow(email).isVisible().catch(() => false)) {
      await this.getMemberDeleteBtn(email).click();
    }
    await expect(this.getMemberRow(email)).toHaveCount(0);
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
    await this.showAllMemberRows();
  }

  // The members table pages at 10 rows, so on a fixture with more members a row a test adds lands on page 2 and every row locator misses it.
  // The pagination of the open modal is the last one in the DOM, because the modal is portalled after the page behind it.
  async showAllMemberRows(): Promise<void> {
    // Scoped to the modal: the Groups list behind it prints the same "Showing x-y of n results" line.
    const modal = this.page.locator(".MuiModal-root").last();
    // The members load after the modal opens, so wait until the table shows a member (its username is an email) or its empty state.
    const memberRow = this.selectedUsersTable.locator("tbody tr").filter({ hasText: "@" }).first();
    await memberRow.or(modal.getByText("No Data Available")).first().waitFor({ state: "visible", timeout: 15000 });
    // Pagination renders with the rows and only above 10 of them, so its absence now means there is a single page.
    const resultLine = modal.getByText(/Showing \d+-\d+ of \d+ results/).last();
    if (!(await resultLine.isVisible())) return;
    const rowsPerPage = modal.getByRole("button", { name: /^\d+$/ }).last();
    await rowsPerPage.click();
    await this.dropdownOption.filter({ hasText: /^100$/ }).first().click();
  }

  // Applies the edit and clicks the section Save as one retried unit; the modal can re-fill its fields between the two and re-disable Save.
  // `edits` MUST be idempotent - it is replayed whenever Save did not arm, so it has to describe the state it
  // wants rather than an action to repeat. A toggle or a one-shot click undoes itself or throws on the second
  // pass: see selectTenantRole, which reads aria-selected first, and removeMemberIfPresent.
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

  // Opens the Add Group form. Two different things cover this button, and both report it as visible, enabled
  // and stable while the click never lands:
  // - a dialog that has just closed leaves an invisible backdrop behind for a few frames
  // - the button is the first element of the page content, so scrolling it into view from further down the
  //   list parks it directly under the sticky header, and the click point resolves to the header instead
  // Scrolling to the top fixes the second properly; retrying alone did not, because the position never moved.
  async openAddGroupForm(): Promise<void> {
    await this.waitForBackdropGone();
    await this.page.evaluate(() => window.scrollTo(0, 0));
    await this.newUserGroupIdentifier.click();
  }

  // Opens Admin > Groups and waits for the listing to be usable.
  // The tab strip renders after the URL has already changed, and dev runs with actionTimeout 10s, so a bare
  // click had to find the tab, wait for it to settle and click it inside that one budget - which it lost
  // whenever the environment was slow. Waiting for the tab separately gives the page room without loosening
  // the action timeout for every other click in the suite.
  async openGroupsTab(): Promise<void> {
    // Admin now defaults to the Overview tab; Groups is a sub-tab of Access &
    // Users, so land on that tab before its sub-tabs are reachable.
    await this.page.goto("/user-management#access-users");
    await this.groupsTab.waitFor({ state: "visible", timeout: 20000 });
    await this.groupsTab.click();
    await this.newUserGroupIdentifier.waitFor({ state: "visible", timeout: 15000 });

    // Wait for the tab's own first load to finish before anyone searches. Opening the tab starts an unfiltered
    // fetch, and typing a term while that is still in flight puts two requests in the air: a trace showed the
    // filtered response land first and the tab's unfiltered one 100ms later, wiping the row the search had
    // just found. The button above renders before the rows, so it is not enough on its own.
    await this.listingRows.first().or(this.emptyState).first().waitFor({ state: "visible", timeout: 20000 });
  }

  // Exact cell match: substring matching would make "Test Group 2" also match "Test Group 2 renamed".
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
    // Focus rather than click: the only reason to touch the box is so Enter lands in it, and after the Admin
    // redesign the tab strip's "Roles" tab overlaps this input and intercepts the pointer. The element is
    // visible, enabled and stable throughout - a click just never reaches it, so no wait can fix that.
    await this.groupSearchInput.focus();
    // Never clears the box first. Clearing starts an UNFILTERED refetch, and the two responses are not
    // ordered: a trace showed the clear and the re-search issued 200ms apart, the unfiltered one landing last
    // and replacing the filtered list. On dev that hid the row (359 groups, so page 1 of the whole list does
    // not hold the fixture) and the search looked broken; on the test env, whose tenant fits on one page, the
    // row the click had already resolved became a DIFFERENT group - and the test saved account roles onto it.
    // Re-entering the same term is a React no-op, so typing it again is pointless; pressing Enter is not.
    // The fetch is keyed on searchName, so Enter re-sends nothing when the term is already the one in state,
    // and sends the FILTERED request when an earlier Enter never registered. Either way: never unfiltered.
    if ((await this.groupSearchInput.inputValue()) !== name) {
      await this.groupSearchInput.fill(name);
      // The box is a controlled input and its Enter handler searches for the value in React state, not the
      // one in the DOM. Waiting for the rendered value holds until that commit, so Enter searches for what
      // was typed rather than the term before it.
      await expect(this.groupSearchInput).toHaveValue(name);
    }
    await this.groupSearchInput.press("Enter");
  }

  async clearGroupSearch(): Promise<void> {
    await this.groupSearchInput.fill("");
    await this.groupSearchInput.press("Enter");
  }

  // Search for a group and open its edit modal.
  async openEditFor(name: string): Promise<void> {
    // The retry used to clear the box first, on the theory that the listing was showing a stale result. It was
    // not stale - it was the unfiltered response from that very clear, landing late (see searchGroup). So the
    // retry is now just another search: one request, and no second one racing it.
    for (let attempt = 1; attempt <= 3; attempt++) {
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
    await this.showAllMemberRows();
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

  // Types a value and only returns once the section has actually stayed dirty.
  // The modal refills its fields whenever one of its own queries lands, which reverts what was typed and
  // silently un-dirties the section - Cancel then closes outright instead of raising the unsaved guard.
  // Deliberately not tied to a named request: any query that repopulates the form does this, and pinning to
  // one would quietly stop protecting anything the day it is renamed or starts failing.
  async fillUntilDirty(field: Locator, value: string, saveBtn: Locator, attempts = 3): Promise<void> {
    for (let attempt = 1; attempt <= attempts; attempt++) {
      await this.fillStable(field, value);

      // Held across consecutive polls, not just true once: a late refill lands between two reads.
      let consecutive = 0;
      const held = await expect
        .poll(
          async () => {
            const ok = (await field.inputValue()) === value && (await saveBtn.isEnabled());
            consecutive = ok ? consecutive + 1 : 0;
            return consecutive;
          },
          { timeout: 5000, intervals: [200, 200, 200, 200, 200] }
        )
        .toBeGreaterThanOrEqual(3)
        .then(() => true)
        .catch(() => false);

      if (held) return;
    }

    // Exhausted: assert so the failure names the real state rather than surfacing later as a missing guard.
    await expect(field).toHaveValue(value);
    await expect(saveBtn).toBeEnabled();
  }

  // Cancel raises the unsaved guard only while the form is dirty, and the modal refills its own fields when its
  // queries land - which silently un-dirties it. fillUntilDirty holds that signal for as long as it can, but
  // nothing announces "no more refills are coming", so a clean form still slips through occasionally and Cancel
  // then just closes the dialog. Recovering beats waiting: reopen, dirty it again, cancel again.
  async cancelExpectingGuard(redirty: () => Promise<void>, attempts = 3): Promise<void> {
    for (let attempt = 1; attempt <= attempts; attempt++) {
      await this.modalCancelBtn.click();

      const raised = await this.unsavedGuard
        .waitFor({ state: "visible", timeout: 5000 })
        .then(() => true)
        .catch(() => false);
      if (raised) return;

      if (attempt < attempts) await redirty();
    }

    // Exhausted: assert so the failure says the guard never came up, rather than surfacing as a later mismatch.
    await expect(this.unsavedGuard).toBeVisible();
  }

  async closeModal(): Promise<void> {
    try {
      await this.modalCancelBtn.click({ timeout: 5000 });
    } catch {
      // Snackbars render above the dialog and swallow this click - an error toast from a forced-failure save
      // covered Close for its whole lifetime. They auto-dismiss, so the retry is on a condition that really
      // does become true. Only on failure, not before every close: the happy path closes with a toast still
      // on screen most of the time, and waiting each one out would cost seconds on every modal in the suite.
      await expect(this.page.locator('[role="alert"]:visible')).toHaveCount(0, { timeout: 10000 }).catch(() => {});
      await this.modalCancelBtn.click();
    }
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

  // A backdrop outlives its dialog content and swallows pointer events, so wait for it to go before clicking.
  // Two details, both read off a trace of a run that failed here:
  // - the header's profile menu (#menu-appbar) stays mounted for the whole session with a hidden backdrop, so
  //   counting every backdrop can never reach zero and each call burned its full timeout instead
  // - what actually blocks the click is #mouse-over-popover, a hover popover that stays open while the pointer
  //   rests on whatever opened it. Waiting cannot close that one, since Playwright leaves the mouse where its
  //   last action put it - so park the pointer first, then wait.
  // Best effort, because callers that are mid-dialog have a legitimate backdrop on screen and must not fail here.
  async waitForBackdropGone(timeout = 10000): Promise<void> {
    await this.page.mouse.move(0, 0);
    await expect(this.page.locator(".MuiModal-backdrop:visible")).toHaveCount(0, { timeout }).catch(() => {});
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
  await clickSidenavRail(locators.homeBtn);
  await locators.adminBtn.waitFor({ state: "visible", timeout: 15000 });
  await clickSidenavRail(locators.adminBtn);
  // The sidenav click can land before React attaches its handler, so navigate directly rather than fail on nav flake.
  await page.waitForURL("**/user-management**", { timeout: 20000 }).catch(async () => {
    await page.goto("/user-management");
    await page.waitForURL("**/user-management**", { timeout: 20000 });
  });
  await locators.openGroupsTab();
  return locators;
}

// Lands on Admin > Users. The sidenav click can fire before React attaches its handler, so a direct
// navigation is the fallback rather than a failed test.
export async function openUsersPage(page: Page): Promise<UserLocators> {
  const users = new UserLocators(page);
  await clickSidenavRail(users.adminBtn);
  await page.waitForURL("**/user-management**", { timeout: 20000 }).catch(async () => {
    await page.goto("/user-management");
    await page.waitForURL("**/user-management**", { timeout: 20000 });
  });
  // Admin now defaults to the Overview tab; the Users controls live under Access
  // & Users, so land on that tab explicitly before waiting on them. A hash-only goto on
  // /user-management keeps the Overview body mounted, so the reload is what renders Users.
  await page.goto("/user-management#access-users", { waitUntil: "domcontentloaded" });
  await page.reload({ waitUntil: "domcontentloaded" });
  // The URL changes before the list renders, and a click on a control that is not there yet only gets the
  // action timeout to find it - 10s on dev, which a slow load loses.
  await users.statusButton.waitFor({ state: "visible", timeout: 20000 });
  return users;
}

// Toggles a group on a user from the Users page. Membership is writable from both sides - this modal and the
// group's Members section - and both write the same rows, which is what the cross-screen tests check.
// Named for what the caller wants: every call site assigns, and the cleanup removes from the group side.
// The option is still a toggle, so a membership left behind by a crashed run makes this remove instead of add
// and the test fails on "the member should be here". Deliberately not guarded on aria-selected - that read
// wrong on the test env for the tenant role picker and skipped a click that had to happen.
export async function assignGroupFromUsersPage(page: Page, account: MemberAccount, groupName: string): Promise<void> {
  const users = await openUsersPage(page);
  await users.selectStatusFilter("Active");
  await users.searchByName(account.name);
  await users.getEditBtnForUser(account.email).click();
  await users.editUserModal.waitFor({ state: "visible", timeout: 15000 });

  await page.getByTestId("user-modal-group").click();
  // Options expose no accessible name, so the group name is matched exactly.
  const option = page
    .locator('[role="option"]')
    .filter({ hasText: new RegExp(`^${escapeForRegex(groupName)}$`) })
    .first();
  await option.waitFor({ state: "visible", timeout: 15000 });
  await option.click();
  await page.keyboard.press("Escape");

  await users.addUserSubmitBtn.click();
  await users.editUserModal.waitFor({ state: "hidden", timeout: 15000 });
}

// Makes a member account Active before it is used: the picker lists only Active users, and the Users spec normalises its accounts to Inactive.
export async function ensureUserActive(page: Page, account: MemberAccount): Promise<void> {
  const users = await openUsersPage(page);

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
  await locators.openGroupsTab();
}

// Searches the listing for a group and reports whether it is there.
// 15s because a slow list response would otherwise look like a missing group and trigger a duplicate create.
export async function groupExists(locators: GroupLocators, name: string): Promise<boolean> {
  const row = locators.getGroupRow(name);

  // A searched listing is either the match or the empty state. Anything else means the search did not run and
  // the previous, unfiltered page is still up - where the row's absence proves nothing, since that page holds
  // whichever groups sorted first. Reporting "missing" from it made the caller create a group that already
  // existed, which is the exact bug the fixtures exist to prevent, so the search is re-issued instead.
  for (let attempt = 1; attempt <= 2; attempt++) {
    await locators.searchGroup(name);
    const settled = await row
      .or(locators.emptyState)
      .first()
      .waitFor({ state: "visible", timeout: 15000 })
      .then(() => true)
      .catch(() => false);
    if (settled) return row.isVisible();
  }
  return false;
}

// Ensures the fixture exists under its canonical name, renaming it back if a crashed run left it under an alias.
export async function ensureGroupExists(
  locators: GroupLocators,
  name: string,
  altNames: string[] = []
): Promise<void> {
  const findRow = (candidate: string) => groupExists(locators, candidate);
  // Every caller heals a stranded fixture, not only the test that renames it.
  const aliases = [...new Set([...altNames, ...(FIXTURE_ALIASES[name] ?? [])])];

  if (await findRow(name)) return;

  for (const alias of aliases) {
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

  await locators.openAddGroupForm();
  await locators.nameInput.fill(name);
  await locators.descInput.fill(FIXTURE_DESCRIPTIONS[name] ?? "");
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

      // Compare the stored values, not the Save button. Asking isEnabled() straight after typing raced the
      // modal's own re-render and read false, so the restore returned without writing and the fixture kept
      // the edited description.
      const atBaseline =
        (await locators.nameInput.inputValue()) === original.name &&
        (await locators.descInput.inputValue()) === original.description;
      if (atBaseline) {
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
    console.warn(`Restore of group "${original.name}" failed - may need manual cleanup:`, errorSummary(error));
  }
}
