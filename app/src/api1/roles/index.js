import { queryGraphQL, unwrapGraphQL } from '@lib/HttpService';

// Client API for dynamic-RBAC custom roles. Each function maps to a
// customroles_* RPC action (see app/src/lib/actions.yaml). The action input is
// passed as top-level field arguments — the gateway forwards them as the
// handler's input map (no nesting). Management actions are tenant_admin-gated
// upstream.
//
// Every call routes its result through unwrapGraphQL (@lib/HttpService) so a
// handler error like "A role with this name already exists" propagates as a
// thrown Error instead of a null payload that surfaces as a generic failure.

export const LIST_CUSTOM_ROLES = `
query ListCustomRoles {
  customroles_list {
    roles {
      id
      name
      description
      permissions { module class entity_type entity_id }
      user_ids
      group_ids
      # Account-scoped group bindings ("Group G holds this role on account X").
      # The gateway prunes responses to the selection set, so omitting this made
      # the group modal's Account tab blind to them: existing bindings never
      # hydrated, and clearing one could not persist because the diff had no
      # record that it was ever there.
      group_account_assignments { principal_id entity_type entity_id }
    }
  }
}
`;

export async function listCustomRoles() {
  const response = await queryGraphQL(LIST_CUSTOM_ROLES, 'ListCustomRoles', {});
  return unwrapGraphQL(response, 'Failed to list roles')?.customroles_list?.roles ?? [];
}

export const GET_CUSTOM_ROLE = `
query GetCustomRole($id: String!) {
  customroles_get(id: $id) {
    id
    name
    description
    permissions { module class }
    user_ids
    group_ids
  }
}
`;

export async function getCustomRole(id) {
  const response = await queryGraphQL(GET_CUSTOM_ROLE, 'GetCustomRole', { id });
  return unwrapGraphQL(response, 'Failed to load role')?.customroles_get ?? null;
}

export const CREATE_CUSTOM_ROLE = `
mutation CreateCustomRole($name: String!, $description: String, $permissions: [CustomRolePermissionInput!]) {
  customroles_create(name: $name, description: $description, permissions: $permissions) {
    id
  }
}
`;

export async function createCustomRole({ name, description, permissions }) {
  const response = await queryGraphQL(CREATE_CUSTOM_ROLE, 'CreateCustomRole', {
    name,
    description: description ?? '',
    permissions: permissions ?? [],
  });
  return unwrapGraphQL(response, 'Failed to create role')?.customroles_create;
}

export const UPDATE_CUSTOM_ROLE = `
mutation UpdateCustomRole($id: String!, $name: String!, $description: String, $permissions: [CustomRolePermissionInput!]) {
  customroles_update(id: $id, name: $name, description: $description, permissions: $permissions) {
    status
    message
  }
}
`;

export async function updateCustomRole({ id, name, description, permissions }) {
  const response = await queryGraphQL(UPDATE_CUSTOM_ROLE, 'UpdateCustomRole', {
    id,
    name,
    description: description ?? '',
    permissions: permissions ?? [],
  });
  return unwrapGraphQL(response, 'Failed to update role')?.customroles_update;
}

export const DELETE_CUSTOM_ROLE = `
mutation DeleteCustomRole($id: String!) {
  customroles_delete(id: $id) {
    status
    message
  }
}
`;

export async function deleteCustomRole(id) {
  const response = await queryGraphQL(DELETE_CUSTOM_ROLE, 'DeleteCustomRole', { id });
  return unwrapGraphQL(response, 'Failed to delete role')?.customroles_delete;
}

export const UPDATE_ROLE_USER_ASSIGNMENTS = `
mutation UpdateRoleUserAssignments($id: String!, $user_ids: [String!]) {
  customroles_update_user_assignments(id: $id, user_ids: $user_ids) {
    status
    message
  }
}
`;

export async function updateRoleUserAssignments(id, userIds) {
  const response = await queryGraphQL(UPDATE_ROLE_USER_ASSIGNMENTS, 'UpdateRoleUserAssignments', {
    id,
    user_ids: userIds ?? [],
  });
  return unwrapGraphQL(response, 'Failed to update user assignments')?.customroles_update_user_assignments;
}

export const UPDATE_ROLE_GROUP_ASSIGNMENTS = `
mutation UpdateRoleGroupAssignments($id: String!, $group_ids: [String!]) {
  customroles_update_group_assignments(id: $id, group_ids: $group_ids) {
    status
    message
  }
}
`;

export async function updateRoleGroupAssignments(id, groupIds) {
  const response = await queryGraphQL(UPDATE_ROLE_GROUP_ASSIGNMENTS, 'UpdateRoleGroupAssignments', {
    id,
    group_ids: groupIds ?? [],
  });
  return unwrapGraphQL(response, 'Failed to update group assignments')?.customroles_update_group_assignments;
}

export const UPDATE_ROLE_GROUP_ACCOUNT_ASSIGNMENTS = `
mutation UpdateRoleGroupAccountAssignments($id: String!, $group_id: String!, $account_ids: [String!]) {
  customroles_update_group_account_assignments(id: $id, group_id: $group_id, account_ids: $account_ids) {
    status
    message
  }
}
`;

// Replace the accounts a group holds a custom role on ("Group G holds Role R on
// these accounts"). Disjoint from updateRoleGroupAssignments, which manages the
// tenant-global bindings — the two can be saved independently.
export async function updateRoleGroupAccountAssignments(id, groupId, accountIds) {
  const response = await queryGraphQL(UPDATE_ROLE_GROUP_ACCOUNT_ASSIGNMENTS, 'UpdateRoleGroupAccountAssignments', {
    id,
    group_id: groupId,
    account_ids: accountIds ?? [],
  });
  return unwrapGraphQL(response, 'Failed to update account assignments')?.customroles_update_group_account_assignments;
}

// The permission matrix the role editor renders + the built-in roles' grant
// sets (rendered read-only). Served by a Next API route (not an RPC action)
// computed from actions.yaml, so it's always in sync. Returns
// { catalog: CatalogEntry[], systemRoles: SystemRoleView[] }.
export async function getPermissionCatalog() {
  const res = await fetch('/api/permissions/catalog', { headers: { 'Content-Type': 'application/json' } });
  if (!res.ok) return { catalog: [], systemRoles: [] };
  const body = await res.json();
  return { catalog: body?.catalog ?? [], systemRoles: body?.systemRoles ?? [] };
}

// Active cloud accounts (id + name) for the role editor's account-scope picker.
// Reads the accounts catalog (accounts:Read / any tenant role), so a tenant admin
// editing roles can always list them. Shaped as { value, label } for @ui/Select.
export const LIST_ACCOUNTS_FOR_SCOPE = `
query ListAccountsForScope {
  accounts_list(where: { status: { _eq: "active" } }, order_by: [{ column: "account_name", order: asc }]) {
    rows { id account_name }
  }
}
`;

export async function listAccountsForScope() {
  const response = await queryGraphQL(LIST_ACCOUNTS_FOR_SCOPE, 'ListAccountsForScope', {});
  const rows = response?.data?.data?.accounts_list?.rows ?? [];
  return rows.map((a) => ({ value: a.id, label: a.account_name || a.id }));
}
