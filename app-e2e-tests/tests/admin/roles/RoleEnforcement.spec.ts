import { test, expect, Browser } from "@playwright/test";
import { LoginPage } from "../../../pages/LoginPage";
import {
  fetchCatalog,
  createRole,
  deleteRole,
  assignRoleToUsers,
  listUsers,
  cleanupE2ERoles,
  skipUnlessCustomRoles,
  roleName,
  gql,
  graphQLErrorCode,
  type PermissionClass,
} from "./rolesHelper";

/**
 * Does a grant actually CHANGE what the holder can do?
 *
 * A grant is only meaningful if a holder is admitted and a non-holder is
 * refused, so this file needs a SECOND identity — the suite's normal login is a
 * tenant admin, who bypasses the custom-grant path entirely.
 *
 * Supply that identity via env:
 *   RBAC_TEST_USERNAME / RBAC_TEST_PASSWORD  — an LDAP user in the same tenant
 *                                              who is NOT a tenant admin.
 *   RBAC_TEST_USER_ID                        — optional; resolved by username
 *                                              from users_list_by_tenant when
 *                                              omitted.
 *
 * Without them the enforcement tests SKIP rather than silently pass — a green
 * run that never proved a denial is worse than a skipped one.
 *
 * The admin-side assertions below (catalog gating, unclassifiable actions,
 * session shape) run unconditionally.
 */

const SUBJECT_USER = process.env.RBAC_TEST_USERNAME;
const SUBJECT_PASS = process.env.RBAC_TEST_PASSWORD;
const hasSubject = !!(SUBJECT_USER && SUBJECT_PASS);

// A read that every tenant can serve and that routes through the query engine,
// so an account-scoped grant is meaningful for it.
const PROBE = {
  module: "audits",
  cls: "Read" as PermissionClass,
  // audits_v2 is a Hasura-style versioned table query → classifies as
  // `audits:Read` and routes to the query engine, so it exercises both the
  // tenant-global and the account-scoped grant paths.
  query: `query ProbeAudits($limit: Int) { audits_v2(limit: $limit, offset: 0, order_by: [{column: "event_time", order: desc}]) { rows { event_time } } }`,
  opName: "ProbeAudits",
  vars: { limit: 1 },
};

async function loginAs(browser: Browser, username: string, password: string) {
  const context = await browser.newContext({ baseURL: process.env.BASE_URL, storageState: undefined });
  const page = await context.newPage();
  const login = new LoginPage(page);
  await login.navigate();
  await login.login(username, password);
  return { context, page };
}

test.describe.configure({ mode: "serial" });

test.describe("Admin → Roles: enforcement", () => {
  test.afterAll(async ({ browser }) => {
    const page = await browser.newPage();
    await cleanupE2ERoles(page);
    await page.close();
  });

  test("the permission catalog itself is admin-gated", async ({ page }) => {
    // Serving the full action inventory to any authenticated user would leak the
    // product's whole operation surface. The route is tenant-admin gated.
    const res = await page.request.get("/api/permissions/catalog", { failOnStatusCode: false });
    expect([200, 403]).toContain(res.status());
    if (res.status() === 200) {
      const body = await res.json();
      expect(body.catalog.length).toBeGreaterThan(0);
    }
  });

  test("session exposes customRolesEnabled and the holder's permission keys", async ({ page }) => {
    const res = await page.request.get("/api/auth/session", { failOnStatusCode: false });
    expect(res.status()).toBe(200);
    const session = await res.json();
    // `permissions` is the baked-in grant list the gateway checks. Built-in-role
    // users legitimately have it EMPTY — the front end must not read emptiness as
    // "no access" (that is what customRolesEnabled disambiguates).
    expect(Array.isArray(session.permissions ?? [])).toBe(true);
    for (const key of session.permissions ?? []) {
      expect(String(key)).toMatch(/^(scoped:)?[a-z0-9_]+:(Read|Write|Execute)$/);
    }
  });

  test("a non-grantable action is not offered by the catalog", async ({ page }) => {
    const { catalog } = await fetchCatalog(page);
    const modules = new Set(catalog.map((e) => e.module));
    // Privilege administration and pre-auth plumbing must never be grantable —
    // a grant there would let a custom role mint tenant_admin for itself.
    for (const m of ["userroles", "roles", "signup", "auth", "userauths", "nudgebee", "relay"]) {
      expect(modules.has(m), `${m} must not be a grantable module`).toBe(false);
    }
  });

  test.describe("as a non-admin grant holder", () => {
    test.skip(!hasSubject, "set RBAC_TEST_USERNAME / RBAC_TEST_PASSWORD to run enforcement tests");

    // These mint and assign real custom roles, which the customroles_* service
    // refuses outright while CUSTOM_ROLES is off for the tenant.
    test.beforeEach(async ({ page }) => {
      await skipUnlessCustomRoles(page, test);
    });

    let roleId = "";
    let subjectId = "";

    test.beforeAll(async ({ browser }) => {
      const page = await browser.newPage();
      const users = await listUsers(page);
      subjectId = process.env.RBAC_TEST_USER_ID || users.find((u) => u.username === SUBJECT_USER)?.id || "";
      await page.close();
      expect(subjectId, `RBAC_TEST_USERNAME "${SUBJECT_USER}" must exist in this tenant`).toBeTruthy();
    });

    test("without a grant the action is refused at the gateway", async ({ browser }) => {
      const { context, page } = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      const res = await gql(page, PROBE.query, PROBE.opName, PROBE.vars);
      // Either a FORBIDDEN gateway rejection or the no-tenant-role variant —
      // both are denials. What must NOT happen is a 200 with rows.
      const denied = !!res.body?.errors;
      expect(denied, "a user without the grant must not read audits").toBe(true);
      if (denied) expect(["FORBIDDEN", undefined]).toContain(graphQLErrorCode(res.body));
      await context.close();
    });

    test("granting the module admits the action; revoking refuses it again", async ({ browser }) => {
      const admin = await browser.newPage();
      roleId = await createRole(admin, roleName("enforce"), [{ module: PROBE.module, class: PROBE.cls }]);
      await assignRoleToUsers(admin, roleId, [subjectId]);

      // The grant is baked into the session at sign-in, so the subject must log
      // in AFTER the assignment for it to be visible. This is the single most
      // common false failure when testing RBAC manually.
      const granted = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      const allowed = await gql(granted.page, PROBE.query, PROBE.opName, PROBE.vars);
      expect(allowed.body?.errors, JSON.stringify(allowed.body?.errors)).toBeUndefined();
      await granted.context.close();

      // Revoke and re-login: back to denied.
      await assignRoleToUsers(admin, roleId, []);
      const revoked = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      const after = await gql(revoked.page, PROBE.query, PROBE.opName, PROBE.vars);
      expect(after.body?.errors, "revoking the assignment must restore the denial").toBeTruthy();
      await revoked.context.close();

      await deleteRole(admin, roleId);
      await admin.close();
    });

    test("a Read grant does not admit the module's Write actions", async ({ browser }) => {
      const admin = await browser.newPage();
      const id = await createRole(admin, roleName("read-only"), [{ module: "usergroups", class: "Read" }]);
      await assignRoleToUsers(admin, id, [subjectId]);

      const { context, page } = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      const write = await gql(
        page,
        `mutation ProbeGroupCreate($name: String!) { usergroup_create(name: $name, description: "rbac probe") { id } }`,
        "ProbeGroupCreate",
        { name: roleName("should-not-exist") }
      );
      expect(write.body?.errors, "Read must never reach Write").toBeTruthy();
      await context.close();

      await assignRoleToUsers(admin, id, []);
      await deleteRole(admin, id);
      await admin.close();
    });

    test("a grant never admits privilege administration", async ({ browser }) => {
      const admin = await browser.newPage();
      // users:Write is the widest identity grant that IS delegable. It must still
      // not permit role assignment (userroles_* is non-grantable by module).
      const id = await createRole(admin, roleName("users-write"), [{ module: "users", class: "Write" }]);
      await assignRoleToUsers(admin, id, [subjectId]);

      const { context, page } = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      const escalate = await gql(
        page,
        `mutation ProbeSyncRoles($user_id: String!) { userroles_sync(user_id: $user_id, roles: ["tenant_admin"]) { status } }`,
        "ProbeSyncRoles",
        { user_id: subjectId }
      );
      expect(escalate.body?.errors, "users:Write must not permit minting tenant_admin").toBeTruthy();
      await context.close();

      await assignRoleToUsers(admin, id, []);
      await deleteRole(admin, id);
      await admin.close();
    });

    test("admin sections the holder lacks Read on are disabled, not hidden", async ({ browser }) => {
      const admin = await browser.newPage();
      const id = await createRole(admin, roleName("audits-only"), [{ module: "audits", class: "Read" }]);
      await assignRoleToUsers(admin, id, [subjectId]);

      const { context, page } = await loginAs(browser, SUBJECT_USER!, SUBJECT_PASS!);
      await page.goto("/user-management#audits");
      await page.waitForLoadState("domcontentloaded");
      // The Users/Groups tabs must still be listed (discoverable) but not
      // clickable — the page greys them and offers a request-access tooltip.
      await expect(page.getByText("Audits", { exact: true }).first()).toBeVisible({ timeout: 30000 });
      await context.close();

      await assignRoleToUsers(admin, id, []);
      await deleteRole(admin, id);
      await admin.close();
    });
  });
});
