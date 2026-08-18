import { test, expect } from "@playwright/test";
import { AssignmentLocators } from "./rolesLocators";
import { registerWelcomeTourAutoDismiss } from "../../utils/helpers";
import {
  createRole,
  deleteRole,
  listRoles,
  listGroups,
  listUsers,
  listAccounts,
  assignRoleToUsers,
  assignRoleToGroups,
  assignRoleToGroupAccounts,
  cleanupE2ERoles,
  skipUnlessCustomRoles,
  roleName,
  graphQLErrorMessage,
} from "./rolesHelper";

/**
 * Assigning a custom role — the second half of dynamic RBAC.
 *
 * The model this suite pins down (api-server/services/ee/customrole):
 *
 *   principal   scope      where it is edited            storage
 *   ---------   --------   ---------------------------   -------------------------------
 *   user        tenant     Users tab → user modal        entity_type IS NULL
 *   group       tenant     Groups tab → Tenant sub-tab   entity_type IS NULL
 *   group       account    Groups tab → Account sub-tab  entity_type = 'account'
 *   user        account    NOT SUPPORTED — a user gets an account-scoped custom role
 *                          only by being a member of a group that holds it.
 *
 * The critical invariant is that the tenant-global and account-scoped writes are
 * DISJOINT: each is a replace-all over its own row set, so saving one tab must
 * never wipe the other's bindings. That is asserted explicitly below.
 */

test.describe.configure({ mode: "serial" });

test.describe("Admin → Roles: assignment", () => {
  test.beforeAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanupE2ERoles(page);
    await page.close();
  });

  // Every test below drives the Roles tab, which only exists when the tenant
  // has CUSTOM_ROLES enabled.
  test.beforeEach(async ({ page }) => {
    await skipUnlessCustomRoles(page, test);
  });

  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanupE2ERoles(page);
    await page.close();
  });

  test("Roles - assign a role to a user then clear the list, verify the tenant-global assignment round-trips", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const users = await listUsers(page);
    expect(users.length, "tenant must have at least one user").toBeGreaterThan(0);
    const user = users[0];

    const id = await createRole(page, roleName("user-tenant"), [{ module: "audits", class: "Read" }]);
    const res = await assignRoleToUsers(page, id, [user.id]);
    expect(res.body?.errors, JSON.stringify(res.body?.errors)).toBeUndefined();

    const role = (await listRoles(page)).find((r) => r.id === id);
    expect(role?.user_ids).toContain(user.id);
    // A user assignment is tenant-global by construction: no account rows.
    expect(role?.group_account_assignments ?? []).toEqual([]);

    // Replace-all semantics: an empty list clears it.
    await assignRoleToUsers(page, id, []);
    expect((await listRoles(page)).find((r) => r.id === id)?.user_ids).toEqual([]);
    await deleteRole(page, id);
  });

  test("Roles - assign a role to a group then clear the list, verify the tenant-global group assignment round-trips", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const groups = await listGroups(page);
    test.skip(groups.length === 0, "tenant has no user groups — run CreateGroups.spec.ts first");
    const group = groups[0];

    const id = await createRole(page, roleName("group-tenant"), [{ module: "audits", class: "Read" }]);
    const res = await assignRoleToGroups(page, id, [group.id]);
    expect(res.body?.errors, JSON.stringify(res.body?.errors)).toBeUndefined();

    const role = (await listRoles(page)).find((r) => r.id === id);
    expect(role?.group_ids).toContain(group.id);

    await assignRoleToGroups(page, id, []);
    expect((await listRoles(page)).find((r) => r.id === id)?.group_ids).toEqual([]);
    await deleteRole(page, id);
  });

  test("Roles - bind a role to a group on one account then clear it, verify the account-scoped assignment round-trips", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const groups = await listGroups(page);
    const accounts = await listAccounts(page);
    test.skip(groups.length === 0 || accounts.length === 0, "needs at least one group and one active account");
    const group = groups[0];
    const account = accounts[0];

    const id = await createRole(page, roleName("group-account"), [{ module: "k8s", class: "Read" }]);
    const res = await assignRoleToGroupAccounts(page, id, group.id, [account.id]);
    expect(res.body?.errors, JSON.stringify(res.body?.errors)).toBeUndefined();

    const role = (await listRoles(page)).find((r) => r.id === id);
    const scoped = role?.group_account_assignments ?? [];
    expect(scoped.map((a) => `${a.principal_id}|${a.entity_type}|${a.entity_id}`)).toContain(
      `${group.id}|account|${account.id}`
    );
    // An account-scoped binding must NOT show up as a tenant-global group id —
    // the group modal's two tabs read disjoint row sets.
    expect(role?.group_ids ?? []).not.toContain(group.id);

    await assignRoleToGroupAccounts(page, id, group.id, []);
    expect((await listRoles(page)).find((r) => r.id === id)?.group_account_assignments ?? []).toEqual([]);
    await deleteRole(page, id);
  });

  test("Roles - save tenant and account bindings for the same group, re-save and clear the tenant tab, verify the account rows survive", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const groups = await listGroups(page);
    const accounts = await listAccounts(page);
    test.skip(groups.length === 0 || accounts.length === 0, "needs at least one group and one active account");
    const group = groups[0];
    const account = accounts[0];

    const id = await createRole(page, roleName("disjoint"), [{ module: "k8s", class: "Read" }]);

    await assignRoleToGroups(page, id, [group.id]);
    await assignRoleToGroupAccounts(page, id, group.id, [account.id]);

    let role = (await listRoles(page)).find((r) => r.id === id);
    expect(role?.group_ids, "tenant-global binding survives the account save").toContain(group.id);
    expect((role?.group_account_assignments ?? []).length, "account binding survives too").toBe(1);

    // Re-save the TENANT tab with the same content. The account rows must live.
    await assignRoleToGroups(page, id, [group.id]);
    role = (await listRoles(page)).find((r) => r.id === id);
    expect((role?.group_account_assignments ?? []).length, "tenant save must not wipe account rows").toBe(1);

    // Clear the TENANT tab. Account rows must still live.
    await assignRoleToGroups(page, id, []);
    role = (await listRoles(page)).find((r) => r.id === id);
    expect(role?.group_ids).toEqual([]);
    expect((role?.group_account_assignments ?? []).length, "clearing tenant must not wipe account rows").toBe(1);

    await deleteRole(page, id);
  });

  test("Roles - bind a role to a group on an account outside the tenant, verify the API refuses it as not in this tenant", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const groups = await listGroups(page);
    test.skip(groups.length === 0, "needs at least one group");
    const id = await createRole(page, roleName("foreign-account"), [{ module: "k8s", class: "Read" }]);
    const res = await assignRoleToGroupAccounts(page, id, groups[0].id, ["00000000-0000-0000-0000-000000000000"]);
    expect(graphQLErrorMessage(res.body) ?? "", "cross-tenant account must be rejected").toMatch(/not in this tenant/i);
    await deleteRole(page, id);
  });

  test("Roles - create a role, open the Edit User modal, open the Role picker, verify the new role is offered", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const assign = new AssignmentLocators(page);
    const name = roleName("picker-user");
    const id = await createRole(page, name, [{ module: "audits", class: "Read" }]);

    await registerWelcomeTourAutoDismiss(page);
    await page.goto("/user-management#users", { waitUntil: "domcontentloaded" });
    // Open the first user's edit modal via its row action. The row action is
    // rendered only after the users query resolves, so wait for it rather than
    // counting immediately — a bare count() here reports 0 on every run and
    // turns a real assertion into a silent skip.
    const rowEdit = assign.editUserBtn;
    // Probe, not an assertion: a tenant whose only user is the logged-in admin
    // renders no Edit button at all, and that is a legitimate skip below.
    const hasEditableUser = await rowEdit
      .waitFor({ state: "visible", timeout: 30000 })
      .then(() => true)
      .catch(() => false);
    if (!hasEditableUser) {
      await deleteRole(page, id);
      test.skip(true, "no editable user rows in this tenant");
      return;
    }
    await rowEdit.click();
    await expect(assign.userTenantRolePicker).toBeVisible({ timeout: 30000 });
    await assign.userTenantRolePicker.click();
    await expect(assign.roleOption(name)).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");

    await deleteRole(page, id);
  });

  test("Roles - create a role, open the Edit Group modal, open the Tenant and Account pickers, verify the new role is offered in both", { tag: ["@dev", "@test", "@regression", "@rbac", "@oss"] }, async ({ page }) => {
    const assign = new AssignmentLocators(page);
    const name = roleName("picker-group");
    const id = await createRole(page, name, [{ module: "k8s", class: "Read" }]);

    await registerWelcomeTourAutoDismiss(page);
    await page.goto("/user-management#groups", { waitUntil: "domcontentloaded" });
    const rowEdit = assign.editGroupBtn;
    // Probe, not an assertion: a tenant with no groups renders no Edit button,
    // which is the legitimate skip below rather than a failure.
    const hasEditableGroup = await rowEdit
      .waitFor({ state: "visible", timeout: 30000 })
      .then(() => true)
      .catch(() => false);
    if (!hasEditableGroup) {
      await deleteRole(page, id);
      test.skip(true, "no editable group rows in this tenant");
      return;
    }
    await rowEdit.click();

    // Tenant sub-tab: the custom role sits alongside the two built-in tenant roles.
    await assign.rbacTab("tenant").click();
    await assign.groupTenantRolePicker.click();
    await expect(assign.roleOption(name)).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");

    // Account sub-tab: the SAME role must be offered as a per-account binding —
    // this is the only place a custom role becomes account-scoped.
    await assign.rbacTab("account").click();
    await assign.groupAccountRolePicker.click();
    await expect(assign.roleOption(name)).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");

    await deleteRole(page, id);
  });
});
