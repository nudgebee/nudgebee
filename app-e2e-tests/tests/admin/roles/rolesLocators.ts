import { Page, Locator } from "@playwright/test";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";

/**
 * Page object for Admin → Roles (dynamic RBAC / custom roles).
 *
 * LOCATOR POLICY — this suite runs against a DEPLOYED environment (BASE_URL),
 * not against the branch under review. So a locator may only depend on DOM that
 * is already live in the target environment; anything that relies on a
 * not-yet-deployed component change will fail every PR run until it ships.
 *
 * Concretely: `ds/Input` and `ds/Checkbox` historically dropped `data-testid`
 * (fixed prop destructure, no rest spread). Both now forward it, but the
 * deployed build may predate that, so:
 *   - the name field is located by its <label> (`htmlFor`/`id` pairing, stable
 *     in both builds) rather than by data-testid;
 *   - matrix cells are located by the `perm-tip-*` wrapper span, which is a MUI
 *     Box and has always forwarded data-testid, rather than by the checkbox's
 *     own `perm-*` testid.
 * Once the target environment carries the DS fixes, both can move to the direct
 * `data-testid` form.
 */
export class RolesLocators {
  readonly page: Page;

  // Listing
  readonly newRoleBtn: Locator;
  readonly customRolesTable: Locator;
  readonly builtInRolesTable: Locator;

  // Editor modal
  readonly roleEditorDialog: Locator;
  readonly roleNameInput: Locator;
  readonly saveRoleBtn: Locator;
  readonly cancelBtn: Locator;
  readonly tenantScopeTab: Locator;
  readonly accountScopeTab: Locator;
  readonly moduleSearch: Locator;

  // Delete confirm
  readonly deleteConfirmBtn: Locator;

  constructor(page: Page) {
    this.page = page;

    this.newRoleBtn = page.locator("#new-role");
    this.customRolesTable = page.locator("#custom-roles");
    this.builtInRolesTable = page.locator("#built-in-roles");

    // Scoped to the dialog so the listing behind the modal can't match, and
    // located by accessible name rather than data-testid — see the locator policy
    // above. Not getByLabel: the field is `required`, so ds/Input appends an
    // aria-hidden "*" INSIDE the <label>, making the label's text "Name*". The
    // accessible name stays "Name" because the asterisk is aria-hidden.
    this.roleEditorDialog = page.getByRole("dialog");
    this.roleNameInput = this.roleEditorDialog.getByRole("textbox", { name: "Name", exact: true });
    this.saveRoleBtn = page.locator('[data-testid="save-role-btn"]');
    this.cancelBtn = page.getByRole("button", { name: "Cancel" }).last();
    this.tenantScopeTab = page.locator("#role-scope-tab-tenant");
    this.accountScopeTab = page.locator("#role-scope-tab-account");
    this.moduleSearch = this.roleEditorDialog
      .getByRole("textbox", { name: "Search modules" })
      .or(this.roleEditorDialog.locator("#role-module-search"))
      .first();

    this.deleteConfirmBtn = page.getByRole("button", { name: "Delete", exact: true }).last();
  }

  /** Admin → Roles, via the hash route the page itself uses. */
  async open(): Promise<void> {
    // These specs navigate straight in on the stored auth state rather than
    // going through doFullLogin(), so the first-login tour handler is not yet
    // registered on this page — and its overlay would intercept clicks on the
    // listing. Registration is idempotent per page.
    await registerWelcomeTourAutoDismiss(this.page);
    // waitUntil domcontentloaded: the default "load" waits on every dashboard
    // subresource and times out on a slow dev env. The New role button is the
    // real readiness signal, so wait on that instead.
    await this.page.goto("/user-management#roles", { waitUntil: "domcontentloaded" });
    await this.newRoleBtn.waitFor({ state: "visible", timeout: 30000 });
  }

  /** The wrapper span around one module × class checkbox. */
  permCell(module: string, cls: "Read" | "Write" | "Execute"): Locator {
    return this.page.locator(`[data-testid="perm-tip-${module}-${cls}"]`);
  }

  /** The native <input type=checkbox> for that cell, reached via the wrapper. */
  permCheckbox(module: string, cls: "Read" | "Write" | "Execute"): Locator {
    return this.permCell(module, cls).locator('input[type="checkbox"]');
  }

  /**
   * The built-in-role summary table rendered under the editor's matrix. Absent
   * in environments that predate that feature — specs probe before asserting.
   */
  builtInSummary(scope: "tenant" | "account"): Locator {
    return this.page.locator(`#role-editor-builtin-${scope}`);
  }

  /** Whether the deployed build carries the built-in-role summary at all. */
  async hasBuiltInSummary(): Promise<boolean> {
    return (await this.page.locator('[id^="role-editor-builtin-"]').count()) > 0;
  }

  editRoleBtn(roleId: string): Locator {
    return this.page.locator(`[data-testid="edit-role-${roleId}"]`);
  }

  deleteRoleBtn(roleId: string): Locator {
    return this.page.locator(`[data-testid="delete-role-${roleId}"]`);
  }

  viewSystemRoleBtn(systemKey: string): Locator {
    return this.page.locator(`[data-testid="view-system-role-${systemKey}"]`);
  }

  /** Switch the editor's permission matrix between the two scope groups. */
  async selectScope(scope: "tenant" | "account"): Promise<void> {
    const tab = scope === "tenant" ? this.tenantScopeTab : this.accountScopeTab;
    await tab.click();
    // The matrix re-renders with the tab selection, so the selected state is
    // the signal - a fixed sleep here is either too short or pure dead time.
    await this.page
      .locator(`#role-scope-tab-${scope}[aria-selected="true"]`)
      .waitFor({ state: "visible", timeout: 15000 });
  }
}

/** Page object for the Users / Groups tabs where a role is ASSIGNED. */
export class AssignmentLocators {
  readonly page: Page;

  readonly usersTab: Locator;
  readonly groupsTab: Locator;
  readonly editUserBtn: Locator;
  readonly editGroupBtn: Locator;

  // User modal
  readonly userTenantRolePicker: Locator;
  readonly userSubmitBtn: Locator;

  // Group modal
  readonly groupTenantRolePicker: Locator;
  readonly groupAccountPicker: Locator;
  readonly groupAccountRolePicker: Locator;
  readonly groupAccountAddBtn: Locator;
  readonly selectedAccountsTable: Locator;
  readonly saveTenantRolesBtn: Locator;
  readonly saveAccountRolesBtn: Locator;

  constructor(page: Page) {
    this.page = page;

    this.usersTab = page.getByText("Users", { exact: true }).first();
    this.groupsTab = page.getByText("Groups", { exact: true }).first();

    // AllUsers.jsx and UserGroup.jsx render zero data-testid, so the accessible
    // name is the highest rung available for these row actions.
    this.editUserBtn = page.getByRole("button", { name: "Edit user" }).or(page.locator("#edit-user-button")).first();
    // No id and no testid on the group row action, so the name stands alone.
    this.editGroupBtn = page.getByRole("button", { name: "Edit group" }).first();

    this.userTenantRolePicker = page.locator("#user-modal-tenant-role");
    this.userSubmitBtn = page.locator("#user-modal-submit-button");

    this.groupTenantRolePicker = page.locator("#group-tenant-role");
    this.groupAccountPicker = page.locator("#group-account");
    this.groupAccountRolePicker = page.locator("#group-account-role");
    this.groupAccountAddBtn = page.getByRole("button", { name: "Add", exact: true });
    this.selectedAccountsTable = page.locator("#selected-accounts");
    this.saveTenantRolesBtn = page.locator('[data-testid="save-tenant-roles"]');
    this.saveAccountRolesBtn = page.locator('[data-testid="save-account-roles"]');
  }

  /**
   * The group modal's RBAC sub-tab. Only Tenant and Account remain — the K8s
   * Namespace tab was removed with the namespace roles (NB-35348).
   */
  rbacTab(kind: "tenant" | "account"): Locator {
    return this.page.getByRole("tab", { name: new RegExp(`^${kind}$`, "i") });
  }

  // One row of an open ds/Select popup, matched by the role name it offers.
  roleOption(name: string): Locator {
    // Not getByRole("option"): ds/Select renders its popup with disablePortal
    // INSIDE the modal, and MUI's ModalManager then marks that whole container
    // aria-hidden while the popup is open — so the accessibility tree holds no
    // options at all and getByRole matches zero. Same trap as the MUI menuitem
    // rows elsewhere in this suite. The CSS role attribute is still there.
    return this.page.locator('[role="listbox"] [role="option"]:visible', { hasText: name }).first();
  }
}
