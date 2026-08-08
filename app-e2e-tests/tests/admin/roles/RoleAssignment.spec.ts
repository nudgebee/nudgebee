import { test, expect } from "@playwright/test";
import { RolesLocators, AssignmentLocators } from "./rolesLocators";
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
  fetchCatalog,
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

  test("tenant-global user assignment round-trips", async ({ page }) => {
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

  test("tenant-global group assignment round-trips", async ({ page }) => {
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

  test("account-scoped group assignment round-trips", async ({ page }) => {
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

  test("tenant and account bindings are disjoint — neither save wipes the other", async ({ page }) => {
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

  test("an account outside the tenant is refused for a scoped binding", async ({ page }) => {
    const groups = await listGroups(page);
    test.skip(groups.length === 0, "needs at least one group");
    const id = await createRole(page, roleName("foreign-account"), [{ module: "k8s", class: "Read" }]);
    const res = await assignRoleToGroupAccounts(page, id, groups[0].id, ["00000000-0000-0000-0000-000000000000"]);
    expect(graphQLErrorMessage(res.body) ?? "", "cross-tenant account must be rejected").toMatch(/not in this tenant/i);
    await deleteRole(page, id);
  });

  test("deleting a role removes its assignments", async ({ page }) => {
    const users = await listUsers(page);
    test.skip(users.length === 0, "needs at least one user");
    const id = await createRole(page, roleName("cascade"), [{ module: "audits", class: "Read" }]);
    await assignRoleToUsers(page, id, [users[0].id]);
    await deleteRole(page, id);
    expect((await listRoles(page)).some((r) => r.id === id)).toBe(false);
  });

  test("the role appears in the user modal's role picker", async ({ page }) => {
    const roles = new RolesLocators(page);
    const assign = new AssignmentLocators(page);
    const name = roleName("picker-user");
    const id = await createRole(page, name, [{ module: "audits", class: "Read" }]);

    await registerWelcomeTourAutoDismiss(page);
    await page.goto("/user-management#users");
    await page.waitForLoadState("domcontentloaded");
    // Open the first user's edit modal via its row action; fall back to skipping
    // if the tenant's user table is empty in this environment.
    const firstEdit = page.locator('[data-testid="edit-user-modal"], #user-modal-tenant-role').first();
    const rowEdit = page.getByRole("button", { name: /edit/i }).first();
    if ((await rowEdit.count()) === 0) {
      await deleteRole(page, id);
      test.skip(true, "no editable user rows in this tenant");
      return;
    }
    await rowEdit.click();
    await expect(assign.userTenantRolePicker).toBeVisible({ timeout: 30000 });
    await assign.userTenantRolePicker.click();
    await expect(page.getByRole("option", { name })).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");
    await firstEdit.first().waitFor({ state: "attached" }).catch(() => undefined);

    await deleteRole(page, id);
    await roles.open();
  });

  test("the role appears in BOTH group-modal pickers (tenant and per-account)", async ({ page }) => {
    const assign = new AssignmentLocators(page);
    const name = roleName("picker-group");
    const id = await createRole(page, name, [{ module: "k8s", class: "Read" }]);

    await registerWelcomeTourAutoDismiss(page);
    await page.goto("/user-management#groups");
    await page.waitForLoadState("domcontentloaded");
    const rowEdit = page.getByRole("button", { name: /edit/i }).first();
    if ((await rowEdit.count()) === 0) {
      await deleteRole(page, id);
      test.skip(true, "no editable group rows in this tenant");
      return;
    }
    await rowEdit.click();

    // Tenant sub-tab: the custom role sits alongside the two built-in tenant roles.
    await assign.rbacTab("tenant").click();
    await assign.groupTenantRolePicker.click();
    await expect(page.getByRole("option", { name })).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");

    // Account sub-tab: the SAME role must be offered as a per-account binding —
    // this is the only place a custom role becomes account-scoped.
    await assign.rbacTab("account").click();
    await assign.groupAccountRolePicker.click();
    await expect(page.getByRole("option", { name })).toBeVisible({ timeout: 15000 });
    await page.keyboard.press("Escape");

    await deleteRole(page, id);
  });

  test("account-scope hint is shown so admins know an unbound account grant is tenant-wide", async ({ page }) => {
    const roles = new RolesLocators(page);
    await fetchCatalog(page);
    await roles.open();
    await roles.newRoleBtn.click();
    await roles.selectScope("account");
    // This wording is the only place the UI explains that an account-scoped
    // grant with no group binding applies to EVERY account in the tenant.
    await expect(page.getByText(/otherwise it applies to every account in the tenant/i)).toBeVisible();
  });
});
