import { gqlStringify, queryGraphQL } from './HttpService';
import cache from './cache';
import { safeJSONParse } from 'src/utils/common';

export enum user_status_type_enum {
  active = 'active',
  inactive = 'inactive',
}

const USER_DETAILS_QUERY = `query UserDetails($where: UserDetailsWhereRequest) {
  users_get_details(where: $where) {
    rows { id display_name username status created_at updated_at user_roles user_auths tenants groups user_attrs }
  }
}`;

function parseUserRow(row: any) {
  return {
    id: row.id,
    display_name: row.display_name,
    username: row.username,
    status: row.status,
    created_at: row.created_at,
    updated_at: row.updated_at,
    user_roles: safeJSONParse(row.user_roles) || [],
    user_auths: safeJSONParse(row.user_auths) || [],
    tenants: (safeJSONParse(row.tenants) || []).map((t: any) => ({
      id: t.id,
      is_default: t.is_default,
      name: t.name,
    })),
    groups: (safeJSONParse(row.groups) || []).map((g: any) => ({ user_group: g })),
    user_attrs: safeJSONParse(row.user_attrs) || [],
  };
}

const USER_BY_PROVIDER_ACCOUNT_QUERY = `query UserByProviderAccount($where: UserByProviderAccountWhereRequest) {
  users_get_by_provider_account(where: $where) {
    rows { auth_id id display_name username status created_at updated_at account_id provider user_roles user_auths tenants groups }
  }
}`;

export const GET_USER_BY_PROVIDER = `
query GetUserByProvider($provider: String!, $id: String!) {
  users_get_auth_by_username(where: {provider: {_eq: $provider}, user_id: {_eq: $id}}) {
    rows {
      id
      username
    }
  }
}
`;

export const GET_USER_BY_USERNAME_PROVIDER_CREDENTIAL = `
query GetUserByProviderAndAccountId($username: String!, $provider: String!) {
  users_get_auth_by_username(where: {provider: {_eq: $provider}, username: {_eq: $username}}) {
    rows {
      auth_id
      credential
      auth_tenant_id
      expires_at
      auth_status
      provider
      user_id
      id
      display_name
      username
      user_status
      created_at
      updated_at
      user_roles
      user_attrs
      auth_accounts
      tenants
    }
  }
}
`;
const USER_UPDATE_ACCESSED_MUTATION = `mutation UserUpdateAccessed($object: user_update_accessed_input!) {
  userauths_update_accessed(object: $object) {
    updated
  }
}`;

const USER_CREATE_AUTH_MUTATION = `mutation UserCreateAuth($object: user_create_auth_input!) {
  userauths_create(object: $object) {
    id
    name
    user_status
    user_id
  }
}`;

export const CREATE_USER = `
mutation AddUser($username: String!, $firstname: String!, $lastname: String, $role: String) {
  users_create(user: {firstname: $firstname, lastname: $lastname, username: $username, role: $role}) {
    status
    message
    id
    tenant_id
  }
}
`;

export const GET_USERS = `
query GetUsers($offset: Int, $limit: Int) {
  users: users_list_by_tenant(limit: $limit, offset: $offset, order_by: [{column: "username", order: asc}], where: __WHERE__) {
    rows {
      display_name
      id
      status
      username
      user_roles
    }
  }
}
`;

export const GET_USERS_BY_TENANT = `
query GetUsersByTenant($offset: Int, $limit: Int) {
  users_list_by_tenant (limit: $limit, offset: $offset, order_by: __ORDERBY__, where: __WHERE__) {
    rows{
      display_name
      id
      status
      username
      created_at
      last_accessed_at
      user_roles
      user_groups
    }
  }
  users_aggregate_by_tenant (where: __WHERE__){
    rows{
      count
    }
  }
}
`;

const USER_UPDATE_STATUS_MUTATION = `mutation UserUpdateStatus($object: user_update_status_input!) {
  users_update_status(object: $object) {
    id
  }
}`;

const USER_DELETE_AUTH_MUTATION = `mutation UserDeleteAuth($object: user_delete_auth_input!) {
  userauths_delete(object: $object) {
    id
  }
}`;

const USER_UPDATE_DEFAULT_TENANT_MUTATION = `mutation UserUpdateDefaultTenant($object: user_update_default_tenant_input!) {
  users_update_default_tenant(object: $object) {
    updated
  }
}`;

export const FILTER_USERS_BY_STATUS = `
query FilterUserByStatus($limit: Int, $offset: Int) {
  users: users_list_by_tenant(where: __WHERE__, limit: $limit, offset: $offset) {
    rows {
      id
      display_name
      status
      username
      user_roles
    }
  }
}
`;

export const FILTER_USER_BY_ROLE = `
query FilterUserByRole($limit: Int, $offset: Int) {
  users: users_list_by_tenant(limit: $limit, offset: $offset) {
    rows {
      id
      status
      display_name
      username
      user_roles
    }
  }
}
`;

export const FILTER_USER_BY_ROLE_AND_STATUS = `
query FilterUserByRoleAndStatus($limit: Int, $offset: Int) {
  users: users_list_by_tenant(where: __WHERE__, limit: $limit, offset: $offset) {
    rows {
      id
      status
      display_name
      username
      user_roles
    }
  }
}
`;

export const GET_USER_GROUPS = `
query GetUserGroups($offset: Int, $limit: Int) {
  usergroups_list(limit: $limit, offset: $offset, order_by: [{column:"name", order:asc}], where: __WHERE__) {
    rows{
      id
      name
      description
      owner
      created_at
      group_roles
      owner_display_name
      member_count
    }
  }
  usergroups_aggregate(where: __WHERE__) {
    rows{
      count
    }
  }
}
`;

export const CREATE_USER_GROUPS = `
mutation CreateUserGroup($name: String!, $description: String) {
  usergroup_create(name: $name, description: $description) {
    id
  }
}
`;

export const GET_USER_GROUP = `
query GetUserGroup($id:uuid) {
  user_groups: usergroups_list(where: {id: {_eq: $id}}) {
    rows {
      id
      name
      description
      owner
      owner_display_name
    }
  }
}
`;

export const GET_USER_GROUP_USERS = `
query GetUserGroupUsers($offset: Int, $limit: Int, $id: String) {
  usergroups_list_users(limit: $limit, offset: $offset, where: {group_id: {_eq: $id}}) {
    rows {
      user_id
      display_name
      username
      status
      user_roles
      user_groups
    }
  }
  usergroups_aggregate_users(where: {group_id: {_eq: $id}}) {
    rows {
      count
    }
  }
}
`;

export const ACCOUNT_ATTRS = `
query CloudAccountAttrs($cloud_acc_id: String!) {
  cloud_account_attrs_v2(where: {cloud_account_id: {_eq: $cloud_acc_id}}) {
    rows {
      name
      value
      cloud_account_id
    }
  }
}
`;

export interface CreateUserAccountRequest {
  provider: string;
  name: string;
  account_id: string;
  user: string;
  provider_type: string;
  credential?: string;
  status?: string;
  accessed_at?: string;
  expires_at?: string;
}

export interface CreateUserRequest {
  username: string;
  status: string;
  display_name: string;
  tenant_name?: string;
  tenant_id?: string;
  role?: string;
  groups?: string[];
  existingUserId?: string;
}

export interface GetUsersRequest {
  limit?: number;
  offset?: number;
  status?: user_status_type_enum;
}

export interface GetUsersByTenantRequest {
  limit: number;
  offset: number;
  sortOrder?: string;
  sortCol?: string;
  nameSearch?: string;
  statusSearch?: string[];
}

export interface GetUserGroupsRequest {
  limit?: number;
  offset?: number;
  nameSearch?: string;
}

export interface GetUserGroupRequest {
  id: string;
  tenantId: string;
  limit?: number;
  offset?: number;
}

export interface FilterUserByStatusRequest {
  status?: string;
  limit?: number;
  offset?: number;
}

export interface FilterUserByRoleRequest {
  role?: string;
  limit?: number;
  offset?: number;
}

export interface FilterUserByRoleAndStatusRequest {
  role: string[];
  limit?: number;
  offset?: number;
  status: string[];
}

export async function getUserById({
  id,
}: {
  id: string;
  fetchRoles?: boolean;
  fetchAttrbutes?: boolean;
  fetchAccounts?: boolean;
  fetchGroups?: boolean;
}) {
  const response = await queryGraphQL(USER_DETAILS_QUERY, 'UserDetails', {
    where: { id: { _eq: id } },
  });
  if (response?.data?.errors) {
    return response.data;
  }
  const rows = response?.data?.data?.users_get_details?.rows || [];
  return { data: { users: rows.map(parseUserRow) } };
}

export async function getUserByUsername({
  username,
}: {
  username: string;
  fetchRoles?: boolean;
  fetchAttrbutes?: boolean;
  fetchAccounts?: boolean;
  fetchGroups?: boolean;
}) {
  const response = await queryGraphQL(USER_DETAILS_QUERY, 'UserDetails', {
    where: { username: { _eq: username?.toLowerCase() } },
  });
  if (response?.data?.errors) {
    return response.data;
  }
  const rows = response?.data?.data?.users_get_details?.rows || [];
  return { data: { users: rows.map(parseUserRow) } };
}

export async function getUserByAccountIdAndAccountProvider({
  accountId,
  accountProvider,
}: {
  accountId: string;
  accountProvider: string;
  fetchRoles?: boolean;
  fetchAttrbutes?: boolean;
  fetchAccounts?: boolean;
  fetchGroups?: boolean;
}) {
  const response = await queryGraphQL(USER_BY_PROVIDER_ACCOUNT_QUERY, 'UserByProviderAccount', {
    where: { account_id: { _eq: accountId }, provider: { _eq: accountProvider } },
  });
  // Single return contract: { data: { user_auths: User[] } }. Callers
  // (NextAuth's getUserByAccount, samlUserAdapter) read
  // `.data.user_auths.length` directly; the prior implementation
  // returned the raw axios body on the GraphQL-errors path, which had
  // no `user_auths` field and crashed those callers. Errors are logged
  // here and treated as "no user found", which is the same outcome the
  // callers handle for an empty result.
  if (response?.data?.errors) {
    console.error('[getUserByAccountIdAndAccountProvider] upstream GraphQL errors:', response.data.errors);
    return { data: { user_auths: [] } };
  }
  const rows = response?.data?.data?.users_get_by_provider_account?.rows || [];
  return {
    data: {
      user_auths: rows.map((row: any) => {
        const user = parseUserRow(row);
        const matchedAuth = user.user_auths.find((a: any) => a.id === row.auth_id);
        user.user_auths = matchedAuth ? [matchedAuth] : user.user_auths;
        return { user };
      }),
    },
  };
}

export async function getUserByUsernameAndAccountProviderAndCredential({
  userName,
  accountProvider,
  fetchRoles = false,
  fetchAttrbutes = false,
  fetchAccounts = false,
}: {
  userName: string;
  accountProvider: string;
  fetchRoles?: boolean;
  fetchAttrbutes?: boolean;
  fetchAccounts?: boolean;
}) {
  const response = await queryGraphQL(GET_USER_BY_USERNAME_PROVIDER_CREDENTIAL, 'GetUserByProviderAndAccountId', {
    username: userName?.toLowerCase(),
    provider: accountProvider,
  });
  if (response?.data?.errors) {
    return response.data;
  }
  const rows = response?.data?.data?.users_get_auth_by_username?.rows || [];
  // Transform flat v2 rows back into the nested shape expected by auth consumers:
  // { data: { user_auths: [{ user: {...}, id, status, tenant_id, credential }] } }
  return {
    data: {
      user_auths: rows.map((row: any) => {
        const userRoles = typeof row.user_roles === 'string' ? JSON.parse(row.user_roles) : row.user_roles || [];
        const userAttrs = typeof row.user_attrs === 'string' ? JSON.parse(row.user_attrs) : row.user_attrs || [];
        const authAccounts = typeof row.auth_accounts === 'string' ? JSON.parse(row.auth_accounts) : row.auth_accounts || [];
        const tenants = (typeof row.tenants === 'string' ? JSON.parse(row.tenants) : row.tenants || []).map((t: any) => ({
          id: t.id,
          is_default: t.is_default,
          tenant: { name: t.name },
        }));
        return {
          id: row.auth_id,
          status: row.auth_status,
          tenant_id: row.auth_tenant_id,
          credential: row.credential,
          expires_at: row.expires_at,
          user: {
            id: row.id,
            display_name: row.display_name,
            username: row.username,
            status: row.user_status,
            updated_at: row.updated_at,
            created_at: row.created_at,
            user_roles: fetchRoles ? userRoles : undefined,
            user_attrs: fetchAttrbutes ? userAttrs : undefined,
            user_auths: fetchAccounts ? authAccounts : undefined,
            tenants,
          },
        };
      }),
    },
  };
}

export async function getUserAccountsByProvider(userId: string, accountProvider: string) {
  const response = await queryGraphQL(GET_USER_BY_PROVIDER, 'GetUserByProvider', {
    id: userId,
    provider: accountProvider,
  });
  if (response?.data?.errors) {
    return response.data;
  }
  const rows = response?.data?.data?.users_get_auth_by_username?.rows || [];
  // Transform to maintain backward compatibility: { data: { user_auths: [{ user: { id, username } }] } }
  return {
    data: {
      user_auths: rows.map((row: any) => ({
        user: {
          id: row.id,
          username: row.username,
        },
      })),
    },
  };
}

export async function createUserAuthAccount(request: CreateUserAccountRequest) {
  const response = await queryGraphQL(USER_CREATE_AUTH_MUTATION, 'UserCreateAuth', {
    object: request,
  });
  if (response.data.errors) {
    return response.data;
  }
  const result = response.data.data.userauths_create;
  return {
    data: {
      id: result.id,
      name: result.name,
      userByUser: { status: result.user_status, id: result.user_id },
    },
  };
}

const USER_SUPER_ADMIN_ROLE_QUERY = `query UserSuperAdminRole($where: UserSuperAdminRoleWhereRequest) {
  users_get_super_admin_role(where: $where) {
    rows { role }
  }
}`;

export async function getUserSuperAdminRole(userId: string): Promise<string | null> {
  const response = await queryGraphQL(USER_SUPER_ADMIN_ROLE_QUERY, 'UserSuperAdminRole', {
    where: { user_id: { _eq: userId } },
  });
  return response?.data?.data?.users_get_super_admin_role?.rows?.[0]?.role || null;
}

export async function updateUserAccountAccessed(authId: string, tenant: string) {
  const response = await queryGraphQL(USER_UPDATE_ACCESSED_MUTATION, 'UserUpdateAccessed', {
    object: { auth_id: authId, tenant_id: tenant },
  });
  return response.data;
}

export async function updateUserAccountAccessedByUsername(username: string, tenant: string) {
  if (!username) {
    return {};
  }
  if (!tenant) {
    return {};
  }

  const response = await queryGraphQL(USER_UPDATE_ACCESSED_MUTATION, 'UserUpdateAccessed', {
    object: { username: username, tenant_id: tenant },
  });
  return response.data;
}

export async function createUser(request: CreateUserRequest) {
  const nameParts = (request.display_name || '').split(' ');
  const response = await queryGraphQL(CREATE_USER, 'AddUser', {
    username: request.username,
    firstname: nameParts[0] || request.username,
    lastname: nameParts.slice(1).join(' ') || undefined,
    role: request.role || undefined,
  });
  if (response.data.errors) {
    return response.data;
  }

  return {
    data: response.data.data.users_create,
  };
}

export async function onboardUser(request: CreateUserRequest) {
  const query = `
    mutation UserOnboard($object: user_onboard_input!) {
      signup_complete(object: $object) {
        id
        status
        message
      }
    }
  `;
  const response = await queryGraphQL(query, 'UserOnboard', {
    object: {
      username: request.username?.toLowerCase(),
      display_name: request.display_name,
      status: request.status || 'active',
      ...(request.tenant_name && { tenant_name: request.tenant_name }),
      ...(request.tenant_id && { tenant_id: request.tenant_id }),
      ...(request.role && { role: request.role }),
      ...(request.groups && { groups: request.groups }),
      ...(request.existingUserId && { existing_user_id: request.existingUserId }),
    },
  });
  if (response.data.errors) {
    return response.data;
  }
  return { data: response.data.data.signup_complete };
}

export async function createUserGroup(name: string, desc: string) {
  const response = await queryGraphQL(CREATE_USER_GROUPS, 'CreateUserGroup', {
    name: name,
    description: desc,
  });
  if (response.data.errors) {
    return response.data;
  }

  return {
    data: response.data.data.usergroup_create,
  };
}

export async function getUsers(request: GetUsersRequest) {
  let where: any = {};
  if (request.status) {
    where.status = { _eq: request.status };
  }
  let queryStr = GET_USERS;
  queryStr = queryStr.replaceAll('__WHERE__', gqlStringify(where));
  const response = await queryGraphQL(queryStr, 'GetUsers', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });
  return response?.data?.data.users?.rows || [];
}

export async function getUsersByTenant(request: GetUsersByTenantRequest) {
  let queryStr = GET_USERS_BY_TENANT;

  const orderBy = [
    {
      column: request.sortCol || 'display_name',
      order: request.sortOrder || 'asc',
    },
  ];

  const where: any = {};
  if (request.nameSearch) {
    where.display_name = { _ilike: '%' + request.nameSearch + '%' };
  }

  if (request.statusSearch) {
    where.status = { _eq: request.statusSearch };
  }
  queryStr = queryStr.replaceAll('__ORDERBY__', gqlStringify(orderBy, ['order'])).replaceAll('__WHERE__', gqlStringify(where));
  const response = await queryGraphQL(queryStr, 'GetUsersByTenant', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });
  return response?.data?.data || [];
}

export async function updateUserStatus(user_id: string, status: string) {
  const response = await queryGraphQL(USER_UPDATE_STATUS_MUTATION, 'UserUpdateStatus', {
    object: { id: user_id, status },
  });

  if (response.data.errors) {
    return response.data;
  }

  return {
    data: response.data.data.users_update_status,
  };
}

export async function filterUserByStatus(request: FilterUserByStatusRequest) {
  const where: any = { status: { _eq: request.status || 'inactive' } };
  const queryStr = FILTER_USERS_BY_STATUS.replaceAll('__WHERE__', gqlStringify(where));
  const response = await queryGraphQL(queryStr, 'FilterUserByStatus', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });

  if (response.data.errors) {
    return response.data;
  }

  return {
    data: response.data.data.users?.rows,
  };
}

export async function filterUserByRole(request: FilterUserByRoleRequest) {
  const response = await queryGraphQL(FILTER_USER_BY_ROLE, 'FilterUserByRole', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });

  if (response.data.errors) {
    return response.data;
  }

  const targetRole = request.role || 'user';
  const rows = (response.data?.data?.users?.rows || [])
    .map((user: any) => {
      const userRoles = safeJSONParse(user.user_roles) || [];
      return { ...user, user_roles: userRoles };
    })
    .filter((user: any) => user.user_roles.some((r: any) => r.role === targetRole));

  return {
    data: rows,
  };
}

export async function filterUserByStatusAndRole(request: FilterUserByRoleAndStatusRequest) {
  const where: any = {};
  if (request.status && request.status.length > 0) {
    where.status = { _in: request.status };
  }
  const queryStr = FILTER_USER_BY_ROLE_AND_STATUS.replaceAll('__WHERE__', gqlStringify(where));
  const response = await queryGraphQL(queryStr, 'FilterUserByRoleAndStatus', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });

  if (response.data.errors) {
    return response.data;
  }

  const targetRoles = request.role || [];
  const rows = (response.data?.data?.users?.rows || [])
    .map((user: any) => {
      const userRoles = safeJSONParse(user.user_roles) || [];
      return { ...user, user_roles: userRoles };
    })
    .filter((user: any) => targetRoles.length === 0 || user.user_roles.some((r: any) => targetRoles.includes(r.role)));

  return {
    data: rows,
  };
}

export async function getUserGroups(request: GetUserGroupsRequest) {
  const where: any = {};
  if (request.nameSearch) {
    if (Array.isArray(request.nameSearch)) {
      where.name = { _in: request.nameSearch };
    } else {
      where.name = { _ilike: '%' + request.nameSearch + '%' };
    }
  }

  const query = GET_USER_GROUPS.replaceAll('__WHERE__', gqlStringify(where));
  const response = await queryGraphQL(query, 'GetUserGroups', {
    limit: request.limit || 10,
    offset: request.offset || 0,
  });
  return response?.data.data || [];
}

export async function getUserGroup(request: GetUserGroupRequest) {
  const response = await queryGraphQL(GET_USER_GROUP, 'GetUserGroup', {
    id: request.id,
  });

  const rows = response.data?.data?.user_groups?.rows || [];
  let responseObj = null;
  if (rows.length > 0) {
    const row = rows[0];
    responseObj = {
      ...row,
      owner: { id: row.owner, display_name: row.owner_display_name },
    };
  }

  return responseObj;
}

export async function getUserGroupUsers(request: GetUserGroupRequest) {
  const response = await queryGraphQL(GET_USER_GROUP_USERS, 'GetUserGroupUsers', {
    id: request.id,
    offset: request.offset,
    limit: request.limit,
  });
  const rows = response.data?.data?.usergroups_list_users?.rows || [];
  const count = response.data?.data?.usergroups_aggregate_users?.rows?.[0]?.count || 0;
  // Transform to maintain backward compatibility with consumers
  return {
    usergroup_users: rows.map((row: any) => ({
      user: {
        id: row.user_id,
        display_name: row.display_name,
        username: row.username,
        status: row.status,
        user_roles: typeof row.user_roles === 'string' ? JSON.parse(row.user_roles) : row.user_roles,
        usergroupUsersByUser: typeof row.user_groups === 'string' ? JSON.parse(row.user_groups) : row.user_groups,
      },
    })),
    usergroup_users_aggregate: { aggregate: { count } },
  };
}

export async function listUserTenantRoles(username: string, tenantId: string) {
  const query = `
    mutation UserTenantRoles($object: user_tenant_roles_input!) {
      users_list_tenant_roles(object: $object) {
        roles {
          entity_id
          entity_type
          role
        }
        tenant_name
      }
    }
  `;
  const response = await queryGraphQL(query, 'UserTenantRoles', {
    object: { username, tenant_id: tenantId },
  });
  return {
    data: response?.data?.data?.users_list_tenant_roles?.roles || [],
    tenantName: response?.data?.data?.users_list_tenant_roles?.tenant_name,
  };
}

export async function deleteUserAuth(id: string) {
  const response = await queryGraphQL(USER_DELETE_AUTH_MUTATION, 'UserDeleteAuth', {
    object: { id },
  });
  return {
    data: response.data,
  };
}

const USER_ACCOUNT_IDS_BY_TENANT_QUERY = `query UserAccountIdsByTenant($where: UserAccountIdsByTenantWhereRequest) {
  users_list_account_ids_by_tenant(where: $where) {
    rows { id }
  }
}`;

export async function getAccountByTenant(tenantId: string) {
  const response = await queryGraphQL(USER_ACCOUNT_IDS_BY_TENANT_QUERY, 'UserAccountIdsByTenant', {
    where: { tenant: { _eq: tenantId } },
  });
  const rows = response?.data?.data?.users_list_account_ids_by_tenant?.rows || [];
  return {
    data: { cloud_accounts: rows },
  };
}

export async function getCloudAccountAttr(accountId: string) {
  const response = await queryGraphQL(ACCOUNT_ATTRS, 'CloudAccountAttrs', {
    cloud_acc_id: accountId,
  });
  // Transform v2 response to maintain backward compatibility
  const rows = response?.data?.data?.cloud_account_attrs_v2?.rows || [];
  if (response?.data?.data) {
    response.data.data.cloud_account_attrs = rows;
  }
  return response;
}

export async function upsertTenantAttributes(data: any, headers?: Record<string, string>) {
  const UPSERT_TENANT_ATTRIBUTE_ACTION = `
  mutation TenantAttributeAction($data: [TenantAttributeRequest!]!) {
    tenant_attribute_upsert(object: $data) {
      name
      value
      id
    }
  }
  `;
  const response = await queryGraphQL(
    UPSERT_TENANT_ATTRIBUTE_ACTION,
    'TenantAttributeAction',
    {
      data: data,
    },
    headers
  );
  return response;
}

const TENANT_ATTRIBUTES_QUERY = `query TenantAttributes {
  tenant_attributes_v2 {
    rows { id name value tenant_id }
  }
}`;

export async function getTenantAttributes(refresh = false) {
  let cachedTenantAttrList = cache.getWithSuffix('tenant.listTenantAttr', null, {});
  if (!cachedTenantAttrList || refresh) {
    const response = await queryGraphQL(TENANT_ATTRIBUTES_QUERY, 'TenantAttributes', {});
    const tenantAttrs = response?.data?.data?.tenant_attributes_v2?.rows || [];
    cache.setWithSuffix('tenant.listTenantAttr', tenantAttrs, {}, 60 * 60 * 1000);
    cachedTenantAttrList = tenantAttrs;
  }
  return cachedTenantAttrList;
}

export async function getFeatures() {
  const GET_FEATURES = `
  query GetFeatures {
    features_list {
      rows {
        value
        description
      }
    }
  }
  `;
  let cachedTenantAttrList = cache.getWithSuffix('tenant.listFeatures', null, {});
  if (!cachedTenantAttrList) {
    const response = await queryGraphQL(GET_FEATURES, 'GetFeatures', {});
    const features = response?.data?.data?.features_list?.rows || [];
    cache.setWithSuffix('tenant.listFeatures', features, {}, 60 * 60 * 1000);
    cachedTenantAttrList = features;
  }
  return cachedTenantAttrList;
}

export async function updateTenantFeatureFlag(features: any[]) {
  const response = await queryGraphQL(
    `
    mutation FeatureFlagUpsert($features: [featureflag_upsert_input!]!) {
      featureflag_upsert(features: $features) {
        status
        message
      }
    }
    `,
    'FeatureFlagUpsert',
    {
      features: features.map((f: any) => ({
        feature_id: f.feature_id,
        status: f.status,
      })),
    }
  );
  if (response?.data?.errors || response?.errors) {
    return response;
  }
  return { data: {} };
}

export async function updateFeatureFlagForAccount(features: any[]) {
  const response = await queryGraphQL(
    `
    mutation FeatureFlagUpsert($features: [featureflag_upsert_input!]!) {
      featureflag_upsert(features: $features) {
        status
        message
      }
    }
    `,
    'FeatureFlagUpsert',
    {
      features: features.map((f: any) => ({
        feature_id: f.feature_id,
        status: f.status,
        account_id: f.account_id,
      })),
    }
  );
  return response;
}

export async function updateTenantName(tenantId: string, tenantName: string) {
  const response = await queryGraphQL(
    `
    mutation TenantUpdateName($name: String!) {
      tenant_update_name(name: $name) {
        status
        message
      }
    }
    `,
    'TenantUpdateName',
    { name: tenantName }
  );
  return response;
}

export async function updateTenantUser(tenantId: string, username: string) {
  const response = await queryGraphQL(USER_UPDATE_DEFAULT_TENANT_MUTATION, 'UserUpdateDefaultTenant', {
    object: { tenant_id: tenantId, username },
  });
  return response;
}

export async function deleteTenantAttributes(data: any) {
  const response = await queryGraphQL(
    `
    mutation TenantAttributeDelete($names: [String!]!) {
      tenant_attribute_delete(names: $names) {
        status
        affected_rows
      }
    }
    `,
    'TenantAttributeDelete',
    { names: data }
  );
  return response;
}

export async function getTenantIdByName(tenantName: string, username: string, skipUserFilter = false): Promise<string | null> {
  if (!skipUserFilter) {
    const TENANT_BY_NAME_USER = `
    query TenantByNameUser($name: String!, $username: String!) {
      tenant_by_user_v2(where: {name: {_eq: $name}, username: {_eq: $username}}) {
        rows {
          id
        }
      }
    }
    `;
    const response = await queryGraphQL(TENANT_BY_NAME_USER, 'TenantByNameUser', { name: tenantName, username });
    return response?.data?.data?.tenant_by_user_v2?.rows?.[0]?.id ?? null;
  }
  const TENANT_BY_NAME = `
  query TenantByName($name: String!) {
    tenants_list(where: {name: {_eq: $name}}) {
      rows {
        id
      }
    }
  }
  `;
  const response = await queryGraphQL(TENANT_BY_NAME, 'TenantByName', { name: tenantName });
  return response?.data?.data?.tenants_list?.rows?.[0]?.id ?? null;
}

export async function getTenant() {
  const GET_TENANT = `
  query GetTenant {
    tenants_list {
      rows {
        name
        id
      }
    }
  }
  `;
  const response = await queryGraphQL(GET_TENANT, 'GetTenant', {});
  return response;
}

type SyncResult = { added: number; removed: number; errors?: any };

export async function syncUserRoles(
  username: string,
  tenantId: string,
  targetRoleNames: string[],
  removeOldRoles: boolean = true
): Promise<SyncResult> {
  try {
    const query = `
      mutation UserSyncRoles($object: user_sync_roles_input!) {
        userroles_sync(object: $object) {
          added
          removed
        }
      }
    `;
    const response = await queryGraphQL(query, 'UserSyncRoles', {
      object: {
        username,
        tenant_id: tenantId,
        target_roles: targetRoleNames,
        remove_old_roles: removeOldRoles,
      },
    });

    if (response?.data?.errors) {
      console.error('Error syncing roles:', response.data.errors);
      return { added: 0, removed: 0, errors: response.data.errors };
    }

    const result = response?.data?.data?.userroles_sync;
    return { added: result?.added ?? 0, removed: result?.removed ?? 0 };
  } catch (error) {
    console.error('Exception during role sync:', error);
    return { added: 0, removed: 0, errors: error };
  }
}
