import { Page, APIResponse, expect } from "@playwright/test";

/**
 * API-level helpers for the RBAC suite.
 *
 * Everything goes through `page.request`, which shares the authenticated
 * storageState cookie with the browser context — so these calls are made AS the
 * logged-in user and exercise the same gateway gate the UI hits. That is
 * deliberate: the negative cases below (a grant that must NOT admit an action)
 * are only meaningful when they traverse the real /api/graphql gateway.
 */

export const E2E_ROLE_PREFIX = "e2e-rbac-";

export type PermissionClass = "Read" | "Write" | "Execute";
export type CatalogEntry = {
  module: string;
  scope: "tenant" | "account";
  classes: PermissionClass[];
  actions: Partial<Record<PermissionClass, Array<{ name: string; description: string }>>>;
};
export type SystemRoleView = {
  systemKey: string;
  label: string;
  permissions: Array<{ module: string; class: PermissionClass }>;
};

export type CustomRole = {
  id: string;
  name: string;
  description: string;
  permissions: Array<{ module: string; class: PermissionClass }>;
  user_ids: string[];
  group_ids: string[];
  group_account_assignments?: Array<{ principal_id: string; entity_type: string; entity_id: string }>;
};

/** Unique-but-recognisable name so a crashed run's leftovers are still sweepable. */
export function roleName(suffix: string): string {
  return `${E2E_ROLE_PREFIX}${suffix}-${Date.now().toString(36)}`;
}

async function readJson(res: APIResponse): Promise<any> {
  const text = await res.text();
  try {
    return JSON.parse(text);
  } catch {
    return { __raw: text, __status: res.status() };
  }
}

/**
 * Raw GraphQL POST. Returns the parsed envelope AND the HTTP status so a test
 * can distinguish a gateway rejection (FORBIDDEN in `errors`) from a handler
 * error, which matters for the enforcement assertions.
 */
export async function gql(
  page: Page,
  query: string,
  operationName: string,
  variables: Record<string, unknown> = {}
): Promise<{ status: number; body: any }> {
  const res = await page.request.post("/api/graphql", {
    data: { query, operationName, variables },
    headers: { "Content-Type": "application/json" },
    failOnStatusCode: false,
  });
  return { status: res.status(), body: await readJson(res) };
}

/** Throws with the gateway's message when the envelope carries GraphQL errors. */
function unwrap(body: any, what: string): any {
  const err = body?.errors?.[0];
  if (err) throw new Error(`${what}: ${err.message ?? JSON.stringify(err)}`);
  return body?.data;
}

export function graphQLErrorCode(body: any): string | undefined {
  return body?.errors?.[0]?.extensions?.code;
}

export function graphQLErrorMessage(body: any): string | undefined {
  return body?.errors?.[0]?.message;
}

// ------------------------------------------------------------ feature gate ----

/**
 * Is the dynamic-RBAC layer switched on for this tenant?
 *
 * The whole Roles surface is behind the CUSTOM_ROLES feature flag. With it off
 * the tab is never registered (`shouldShow` returns false in RolePermissions.jsx),
 * the `customroles_*` service refuses every management call, and the resolver
 * ignores grants outright — so every locator in this suite resolves to nothing
 * and every assertion fails for a reason that has nothing to do with the code
 * under test.
 *
 * `customRolesEnabled` is baked into the session at sign-in
 * (pages/api/auth/[...nextauth].ts) and is the same signal the UI gates on, so
 * reading it here matches exactly what the front end sees.
 */
export async function customRolesEnabled(page: Page): Promise<boolean> {
  const res = await page.request.get("/api/auth/session", { failOnStatusCode: false });
  if (!res.ok()) return false;
  const session = await readJson(res);
  return session?.customRolesEnabled === true;
}

/**
 * Guard for a spec's `beforeEach`. Skips rather than fails when the tenant has
 * the feature off — a red run there would mean "not enabled here", not "broken".
 */
export async function skipUnlessCustomRoles(page: Page, test: { skip: (condition: boolean, reason: string) => void }): Promise<void> {
  test.skip(
    !(await customRolesEnabled(page)),
    "CUSTOM_ROLES is disabled for this tenant — the Roles tab is not registered, so there is nothing to exercise"
  );
}

// ---------------------------------------------------------------- catalog ----

export async function fetchCatalog(page: Page): Promise<{ catalog: CatalogEntry[]; systemRoles: SystemRoleView[] }> {
  const res = await page.request.get("/api/permissions/catalog", { failOnStatusCode: false });
  expect(res.status(), "permission catalog must be readable by a tenant admin").toBe(200);
  const body = await readJson(res);
  return { catalog: body?.catalog ?? [], systemRoles: body?.systemRoles ?? [] };
}

/** Every module × class cell the Roles editor renders, flattened. */
export function catalogCells(catalog: CatalogEntry[]): Array<{ module: string; cls: PermissionClass; scope: "tenant" | "account" }> {
  return catalog.flatMap((e) => e.classes.map((cls) => ({ module: e.module, cls, scope: e.scope })));
}

// ------------------------------------------------------------- role CRUD ----

const LIST = `query ListCustomRoles { customroles_list { roles {
  id name description permissions { module class } user_ids group_ids
  group_account_assignments { principal_id entity_type entity_id }
} } }`;

const CREATE = `mutation CreateCustomRole($name: String!, $description: String, $permissions: [CustomRolePermissionInput!]) {
  customroles_create(name: $name, description: $description, permissions: $permissions) { id } }`;

const UPDATE = `mutation UpdateCustomRole($id: String!, $name: String!, $description: String, $permissions: [CustomRolePermissionInput!]) {
  customroles_update(id: $id, name: $name, description: $description, permissions: $permissions) { status message } }`;

const DELETE = `mutation DeleteCustomRole($id: String!) { customroles_delete(id: $id) { status message } }`;

const ASSIGN_USERS = `mutation UpdateRoleUserAssignments($id: String!, $user_ids: [String!]) {
  customroles_update_user_assignments(id: $id, user_ids: $user_ids) { status message } }`;

const ASSIGN_GROUPS = `mutation UpdateRoleGroupAssignments($id: String!, $group_ids: [String!]) {
  customroles_update_group_assignments(id: $id, group_ids: $group_ids) { status message } }`;

const ASSIGN_GROUP_ACCOUNTS = `mutation UpdateRoleGroupAccountAssignments($id: String!, $group_id: String!, $account_ids: [String!]) {
  customroles_update_group_account_assignments(id: $id, group_id: $group_id, account_ids: $account_ids) { status message } }`;

export async function listRoles(page: Page): Promise<CustomRole[]> {
  const { body } = await gql(page, LIST, "ListCustomRoles");
  return unwrap(body, "list roles")?.customroles_list?.roles ?? [];
}

export async function createRole(
  page: Page,
  name: string,
  permissions: Array<{ module: string; class: PermissionClass }>,
  description = "created by app-e2e-tests"
): Promise<string> {
  const { body } = await gql(page, CREATE, "CreateCustomRole", { name, description, permissions });
  const id = unwrap(body, "create role")?.customroles_create?.id;
  expect(id, `role "${name}" should be created`).toBeTruthy();
  return id;
}

/** Non-throwing create — for the negative/validation cases. */
export async function tryCreateRole(
  page: Page,
  name: string,
  permissions: Array<Record<string, unknown>>,
  description = ""
): Promise<{ status: number; body: any }> {
  return gql(page, CREATE, "CreateCustomRole", { name, description, permissions });
}

export async function updateRole(
  page: Page,
  id: string,
  name: string,
  permissions: Array<{ module: string; class: PermissionClass }>,
  description = ""
): Promise<{ status: number; body: any }> {
  return gql(page, UPDATE, "UpdateCustomRole", { id, name, description, permissions });
}

export async function deleteRole(page: Page, id: string): Promise<{ status: number; body: any }> {
  return gql(page, DELETE, "DeleteCustomRole", { id });
}

export async function assignRoleToUsers(page: Page, id: string, userIds: string[]) {
  return gql(page, ASSIGN_USERS, "UpdateRoleUserAssignments", { id, user_ids: userIds });
}

export async function assignRoleToGroups(page: Page, id: string, groupIds: string[]) {
  return gql(page, ASSIGN_GROUPS, "UpdateRoleGroupAssignments", { id, group_ids: groupIds });
}

export async function assignRoleToGroupAccounts(page: Page, id: string, groupId: string, accountIds: string[]) {
  return gql(page, ASSIGN_GROUP_ACCOUNTS, "UpdateRoleGroupAccountAssignments", { id, group_id: groupId, account_ids: accountIds });
}

/**
 * Delete every role this suite created. Called from afterAll AND at suite start,
 * so a run that crashed mid-test doesn't leave the tenant littered or trip the
 * duplicate-name conflict on the next run.
 */
export async function cleanupE2ERoles(page: Page): Promise<number> {
  const roles = await listRoles(page).catch(() => [] as CustomRole[]);
  const mine = roles.filter((r) => r.name.startsWith(E2E_ROLE_PREFIX));
  for (const r of mine) await deleteRole(page, r.id).catch(() => undefined);
  return mine.length;
}

// -------------------------------------------------------- tenant fixtures ----

export async function listAccounts(page: Page): Promise<Array<{ id: string; name: string }>> {
  const { body } = await gql(
    page,
    `query ListAccountsForScope { accounts_list(where: { status: { _eq: "active" } }, order_by: [{ column: "account_name", order: asc }]) { rows { id account_name } } }`,
    "ListAccountsForScope"
  );
  const rows = body?.data?.accounts_list?.rows ?? [];
  return rows.map((r: any) => ({ id: r.id, name: r.account_name || r.id }));
}

export async function listGroups(page: Page): Promise<Array<{ id: string; name: string }>> {
  const { body } = await gql(
    page,
    `query ListUserGroupsForRbac($limit: Int, $offset: Int) {
       usergroups_list(limit: $limit, offset: $offset, order_by: [{ column: "name", order: asc }]) { rows { id name } }
     }`,
    "ListUserGroupsForRbac",
    { limit: 100, offset: 0 }
  );
  const rows = body?.data?.usergroups_list?.rows ?? [];
  return rows.map((r: any) => ({ id: r.id, name: r.name }));
}

export async function listUsers(page: Page): Promise<Array<{ id: string; username: string }>> {
  const { body } = await gql(
    page,
    `query ListUsersForRbac($limit: Int, $offset: Int) {
       users_list_by_tenant(limit: $limit, offset: $offset, order_by: [{ column: "username", order: asc }]) { rows { id username } }
     }`,
    "ListUsersForRbac",
    { limit: 100, offset: 0 }
  );
  const rows = body?.data?.users_list_by_tenant?.rows ?? [];
  return rows.map((r: any) => ({ id: r.id, username: r.username }));
}
