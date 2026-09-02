import { Page, Locator, expect } from "@playwright/test";
import { CommonLocators } from "../../GlobalLocators";
import { LoginPage } from "../../../pages/LoginPage";

export const Groups = [
  { name: "Tenant Admin" },
  { name: "Tenant Readonly" },
  { name: "Account Admin" },
  { name: "Account Readonly" },
  { name: "K8 Admin" },
  { name: "K8 Readonly" },
];

// ── Fixture groups reused by the CRUD/edge-case suite ────────────────────────
// The product has no way to delete a group, so the suite never creates throwaway
// groups. These four are created once (on the first run against a tenant) and
// reused forever; every test that mutates one restores it in a finally block.
export const FIXTURE_GROUPS = {
  read: "e2e grp read", // never mutated
  update: "e2e grp update", // name/description edits
  members: "e2e grp members", // member add/remove
  rbac: "e2e grp rbac", // role assignment
} as const;

export const FIXTURE_DESCRIPTION = "e2e fixture group";

// Inline field errors under the group-name input (#groupname-error), NOT toasts.
export const GROUP_ERRORS = {
  required: "This field required",
  firstChar: "Should start with an alphabet or a digit",
  minLength: "Name should have atleast 5 characters",
  alphaNum: "This field should be alpha-numeric",
  duplicate: "Group name already in use",
} as const;

// Snackbar messages. The edit modal saves per section, and each section reports
// its own success message; there is no single "group updated" toast.
export const GROUP_TOASTS = {
  created: "Group added successfully",
  infoUpdated: "Group details updated",
  tenantUpdated: "Tenant permissions updated",
  membersUpdated: "Members updated",
} as const;

// Usernames are email addresses, so they contain "." and sometimes "+" — both
// regex metacharacters. Escape before building an anchored match.
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

  // ── Added for the CRUD/edge-case suite ──
  // id-based, so they work in BOTH create and edit mode. The placeholder-based
  // locators above only resolve in create mode — the edit modal renders the name
  // input without a placeholder.
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

    // ── Added for the CRUD/edge-case suite ──
    this.nameInput = page.locator("#groupname");
    this.descInput = page.locator("#description");
    this.nameError = page.locator("#groupname-error");
    this.modalTitle = page.locator("#alert-dialog-title");
    this.modalSubmitBtn = page.locator("#submit");
    this.modalCancelBtn = page.locator("#cancel");
    this.closeModalBtn = page.locator("#close-modal-btn");
    this.groupSearchInput = page.locator("#user-groups-search");
    this.membersPicker = page.locator("#all-users-for-group");
    this.selectedUsersTable = page.locator("#selected-users");
    this.tenantRoleSelect = page.locator("#group-tenant-role");
    this.toastRegion = page.getByRole("region", { name: "Notifications" });

    // Edit mode saves per section; each card has its own Save button, disabled
    // until that section is dirty. There is no combined submit in edit mode.
    this.saveGroupInfoBtn = page.getByTestId("save-group-info");
    this.saveTenantRolesBtn = page.getByTestId("save-tenant-roles");
    this.saveMembersBtn = page.getByTestId("save-group-members");
  }

  // A snackbar with the given text, scoped to the notifications region. Used for
  // asserting a toast is ABSENT — scoping keeps it from matching the same words
  // rendered inline on the form. Presence is asserted page-wide instead (see
  // expectSectionToast), because the region's markup differs between builds.
  toast(message: string): Locator {
    return this.toastRegion.getByText(message, { exact: true });
  }

  // Presence of a toast, matched page-wide. Scoping to the notifications region
  // proved unreliable across builds, so presence is asserted on the text itself
  // (distinctive enough to be unambiguous) and `toast()` is kept for absence.
  toastText(message: string): Locator {
    return this.page.getByText(message, { exact: true }).first();
  }

  // Pick a role in the merged tenant-role picker. It is a multi-select (one
  // built-in role plus any custom roles), so it stays open after a choice —
  // Escape closes it.
  async selectTenantRole(label: string): Promise<void> {
    await this.tenantRoleSelect.click();
    // Options carry no accessible name (the label sits in a nested element), so
    // getByRole(..., { name }) never matches — filter on exact text instead, the
    // same approach the Users role picker uses. Anchored so "Admin" cannot also
    // match "ReadOnly Admin".
    await this.page
      .locator('[role="option"]')
      .filter({ hasText: new RegExp(`^${label}$`) })
      .click();
    await this.page.keyboard.press("Escape");
  }

  // Remove every selected role. The inline clear (✕) only renders while
  // something is selected, so its absence means there is nothing to clear.
  async clearTenantRoles(): Promise<void> {
    const clear = this.tenantRoleSelect.getByRole("button", { name: "Clear selection" });
    if (await clear.isVisible().catch(() => false)) {
      await clear.click();
    }
  }

  // ── Members section ──

  // A row in the members table, matched by username.
  getMemberRow(username: string): Locator {
    return this.selectedUsersTable.locator("tr").filter({ hasText: username });
  }

  // The row's remove control. It carries no id or testid — only the icon's alt
  // text — so it has to be reached through the row.
  getMemberDeleteBtn(username: string): Locator {
    return this.getMemberRow(username).locator("button").filter({ has: this.page.locator('img[alt="delete icon"]') });
  }

  // Open the member picker, add a user, and return that username so the caller
  // can assert on it by identity rather than by count. Already-added members are
  // excluded from the options by the app.
  //
  // Prefers the automation account this suite logs in as, so runs are repeatable
  // and normally only our own account is added to (and removed from) the test
  // group. The dedicated USER_1..3 accounts can't be used: the Users suite keeps
  // them Inactive and this picker only lists Active users. Falls back to the
  // first option when the automation account isn't offered — e.g. it is already
  // a member.
  async addFirstAvailableMember(): Promise<string> {
    await this.membersPicker.click();

    const options = this.page.locator('[role="option"]');
    await options.first().waitFor({ state: "visible", timeout: 15000 });

    const automationUser = process.env.LDAP_USERNAME ?? "";
    const preferred = automationUser
      ? options.filter({ hasText: new RegExp(`^${escapeForRegex(automationUser)}$`) })
      : null;

    const target = preferred && (await preferred.count()) > 0 ? preferred.first() : options.first();
    const username = ((await target.textContent()) ?? "").trim();
    // Callers assert on this value; "" would make toContainText and row lookups
    // pass without proving anything.
    expect(username, "member picker option had no text").not.toBe("");

    await target.click();
    await this.page.keyboard.press("Escape"); // multi-select stays open after picking
    return username;
  }

  // An option in the member picker, matched on exact username. Options carry no
  // accessible name, so this filters on text; the username is regex-escaped
  // because it is an email address.
  memberOption(username: string): Locator {
    return this.page.locator('[role="option"]').filter({ hasText: new RegExp(`^${escapeForRegex(username)}$`) });
  }

  // Type into the picker's own search box. Only rendered while the picker is open.
  async searchInMemberPicker(text: string): Promise<void> {
    // Substring match — the real placeholder ends in a "…" character.
    await this.page.getByPlaceholder("Search active users").fill(text);
  }

  // The Active / Inactive / Suspended filter above the members table. Rendered as
  // a radio group; "Active" is a substring of "Inactive", so match exactly.
  async selectMemberFilter(status: "Active" | "Inactive" | "Suspended"): Promise<void> {
    await this.page.getByRole("radio", { name: status, exact: true }).click();
  }

  // Click a section's Save and assert the toast it reports.
  //
  // A save validates first and returns early when the input is rejected: no
  // toast is shown at all, only an inline error under the name field. Waiting
  // solely for a toast therefore burns the full timeout and reports "element not
  // found", hiding the actual reason — so watch both outcomes and surface the
  // inline text when that is the one that happens.
  // Apply `edits` and click the section's Save as one retried unit.
  //
  // Arming and clicking must not be separate steps: the modal can re-fill its
  // fields between the two, which disables Save again and makes the click time
  // out. Rare with a visible browser, common headless — there the steps run
  // close enough to modal-open to land inside the reset window.
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
    // Out of attempts: assert so the failure reports the button's real state
    // rather than a bare click timeout.
    await expect(saveBtn).toBeEnabled();
    await saveBtn.click();
  }

  // Assert the outcome of a section save that has already been clicked.
  async expectSectionToast(expectedToast: string): Promise<void> {
    // Matched page-wide rather than scoped to the notifications region: the
    // messages are distinctive enough to be unambiguous, and scoping made the
    // assertion depend on container markup that can differ between builds.
    const toastText = this.page.getByText(expectedToast, { exact: true }).first();

    // Wait for whichever outcome arrives first. A save that is rejected shows an
    // inline field error and no toast at all, so waiting only for the toast would
    // burn the full timeout and report nothing useful.
    await toastText.or(this.nameError).first().waitFor({ state: "visible", timeout: 20000 });

    if (await this.nameError.isVisible().catch(() => false)) {
      throw new Error(`Save was rejected with inline error: "${(await this.nameError.textContent())?.trim()}"`);
    }
    await expect(toastText).toBeVisible();
  }

  // A list row for a group, matched on an EXACT cell value. Substring matching
  // would make "e2e grp update" also match "e2e grp update renamed", so a fixture
  // left renamed by a crashed run looks like the original and every later
  // assertion targets the wrong row.
  getGroupRow(name: string): Locator {
    return this.page.locator("tr").filter({ has: this.page.getByText(name, { exact: true }) });
  }

  // Row-scoped Edit button. It carries no id/testid, only aria-label — the most
  // fragile locator in this suite; worth asking the frontend team for an id.
  getEditBtnForGroup(name: string): Locator {
    return this.getGroupRow(name).getByRole("button", { name: "Edit group" });
  }

  // Type a name into the groups search box and submit. Enter is required — the
  // query only fires on Enter (or on clear), not on keystroke.
  async searchGroup(name: string): Promise<void> {
    // The search box sits behind the modal. A dialog that has just closed can
    // still have its backdrop animating out, and that backdrop swallows the
    // click — so make sure nothing is overlaying the list first.
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
    await this.searchGroup(name);
    await this.getEditBtnForGroup(name).click();
    await this.nameInput.waitFor({ state: "visible", timeout: 15000 });
    // The modal's populate effect depends on the async `accounts` fetch, so it
    // re-runs and resets Group name / Description once that request lands. Let it
    // settle before typing, or the edit is silently discarded.
    await this.page.waitForLoadState("networkidle").catch(() => {});
  }

  // Fill a field and confirm the value stuck. Guards against the reset described
  // in openEditFor: losing the typed value also clears the section's dirty flag,
  // which re-disables its Save button and makes the click time out.
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
    await this.nameInput.waitFor({ state: "hidden", timeout: 10000 });
    await this.waitForBackdropGone();
  }

  // The dialog's backdrop outlives its content: the fields are already hidden
  // while MUI is still animating the overlay out, and that overlay swallows
  // pointer events. Without this, the next click on the list behind the modal
  // (typically the search box) fails with "backdrop intercepts pointer events".
  async waitForBackdropGone(timeout = 10000): Promise<void> {
    await this.page
      .locator(".MuiModal-backdrop")
      .first()
      .waitFor({ state: "detached", timeout })
      .catch(() => {});
  }

  // Dismiss the modal if it happens to be open. Used at the head of restore
  // blocks: when a test fails mid-edit the modal is left open, and the restore
  // would otherwise try to search the list behind it and fail too, masking the
  // original failure and stranding the fixture.
  async closeModalIfOpen(): Promise<void> {
    if (!(await this.nameInput.isVisible().catch(() => false))) return;
    await this.modalCancelBtn.click().catch(() => {});
    await this.nameInput.waitFor({ state: "hidden", timeout: 5000 }).catch(() => {});
    await this.waitForBackdropGone(5000);
  }
}

// Shared setup: log in (session reused via global-setup) and land on the
// Admin → Groups tab. Mirrors the Users module's setup().
export async function setup(page: Page): Promise<GroupLocators> {
  const locators = new GroupLocators(page);
  await new LoginPage(page).doFullLogin();
  await locators.homeBtn.click();
  await locators.adminBtn.waitFor({ state: "visible", timeout: 15000 });
  await locators.adminBtn.click();
  // The sidenav click can land before React attaches its handler on a slow first
  // paint, leaving the app on /home. Navigate directly rather than fail the test
  // on nav flake — these tests are about the Groups tab, not the sidenav.
  await page.waitForURL("**/user-management**", { timeout: 20000 }).catch(async () => {
    await page.goto("/user-management");
    await page.waitForURL("**/user-management**", { timeout: 20000 });
  });
  await locators.groupsTab.click();
  await locators.newUserGroupIdentifier.waitFor({ state: "visible", timeout: 15000 });
  return locators;
}

// Guarantee a fixture group exists under its canonical name, creating it only if
// missing. Leaves the list filtered to that group, so callers can act on its row.
//
// altNames: other names the group might currently be listed under — e.g. a rename
// test that crashed before restoring. Because groups can't be deleted, a stranded
// alias would otherwise linger forever AND a fresh duplicate would be created
// beside it. Finding the alias and renaming it back keeps the suite idempotent.
export async function ensureGroupExists(
  locators: GroupLocators,
  name: string,
  altNames: string[] = []
): Promise<void> {
  // 15s, not 5s: under CI load the list can take longer than that to respond, and
  // a false "missing" here sends the helper down the create path for a group that
  // already exists.
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

  // Watch for both outcomes. A slow list response can still make the search above
  // miss an existing fixture, and the create then comes back as a duplicate —
  // which is fine, because this helper only guarantees the group exists. Waiting
  // solely for the success toast would instead burn the full timeout.
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

// Best-effort fixture restore. Never throws: a restore runs in a finally block,
// so letting it fail would bury the assertion error that actually caused the
// test to fail. A failed restore is logged instead — the next run's
// ensureGroupExists re-normalizes anything left behind.
// candidateNames: every name the group might currently be listed under. A rename
// test must look under the edited name too, and — if the rename silently failed —
// under the original.
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

      // Reuse openEditFor rather than clicking directly: it re-searches and waits
      // for the modal's fetches to settle, which the restore needs just as much
      // as the test body does.
      await locators.openEditFor(candidate);
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
