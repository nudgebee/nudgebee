import { queryGraphQL, unwrapGraphQL } from '@lib/HttpService';
import { getUserById, getUsers, getUserGroups, getUserGroup, getUserGroupUsers, createUserGroup } from '@lib/UserService';
import cache from '@lib/cache';

export const PREFERENCE_LAST_ACCOUNT_ID = 'last_account';
// Map of tenantId -> cloud_provider (K8s/AWS/Azure/GCP) -> last account id
// selected for that provider within that tenant. Separate from
// PREFERENCE_LAST_ACCOUNT_ID (which only tracks the single most-recent
// account across all providers) so a feature that needs "the last K8s
// cluster" can find one even when the user's most recent pick overall was
// e.g. an AWS account. Scoped by tenant since account ids aren't unique
// across tenants.
export const PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER = 'last_account_by_provider';
export const PREFERENCE_TABLE_PAGE_SIZE = 'table_page_size';
// Map of tenantId -> array of up to RECENT_PAGE_SEARCHES_LIMIT header-search
// result `value`s (paths), most-recent-first. Scoped per tenant like
// PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER — the header search's option list
// (and therefore its `value`s) differs per tenant. Only the identifying
// `value` is stored (not the whole option object, which can carry a JSX
// `icon` element that doesn't survive JSON.stringify) — callers re-resolve
// each value against their current options list to render it.
export const PREFERENCE_RECENT_PAGE_SEARCHES = 'recent_page_searches';
const RECENT_PAGE_SEARCHES_LIMIT = 3;
// Map of clusterValue -> true for K8s-agent banners the user has dismissed.
// Replaces the old per-cluster `latest-<clusterValue>-K8sAgentSnackbar` keys so
// we keep a single consolidated entry under `nudgebee.userPreferences`.
export const PREFERENCE_K8S_AGENT_SNACKBAR = 'k8s_agent_snackbar';
// Set once the first-login app-overview tour has been shown (per browser).
export const PREFERENCE_APP_TOUR_SEEN = 'app_tour_seen';
// Epoch-ms until which the app-overview welcome card stays snoozed (24h per
// Snooze click). Stale values are harmless — past timestamps just fail the
// `Date.now() < value` check.
export const PREFERENCE_APP_TOUR_SNOOZED_UNTIL = 'app_tour_snoozed_until';
// Set once the corresponding first-visit Troubleshoot tour has been shown, one
// flag per top-level view (All Events overview / Investigations / Knowledge
// Graph). Per browser, like the app-overview flag above.
export const PREFERENCE_TROUBLESHOOT_TOUR_SEEN = 'troubleshoot_tour_seen';
export const PREFERENCE_TROUBLESHOOT_INVESTIGATIONS_TOUR_SEEN = 'troubleshoot_investigations_tour_seen';
export const PREFERENCE_TROUBLESHOOT_KG_TOUR_SEEN = 'troubleshoot_kg_tour_seen';
// Same, for the other sections that offer a tour on first visit.
export const PREFERENCE_OPTIMIZE_TOUR_SEEN = 'optimize_tour_seen';
export const PREFERENCE_TICKETS_TOUR_SEEN = 'tickets_tour_seen';
export const PREFERENCE_CLOUD_TOUR_SEEN = 'cloud_tour_seen';
// ISO timestamp high-water-mark for the Product Updates drawer — updates newer
// than this are "unread". Replaces the standalone `nb.productUpdates.lastSeenAt`
// key so it lives in the consolidated `nudgebee.userPreferences` entry.
export const PREFERENCE_PRODUCT_UPDATES_LAST_SEEN = 'product_updates_last_seen';

const availablePreferences = [
  PREFERENCE_LAST_ACCOUNT_ID,
  PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER,
  PREFERENCE_RECENT_PAGE_SEARCHES,
  PREFERENCE_TABLE_PAGE_SIZE,
  PREFERENCE_K8S_AGENT_SNACKBAR,
  PREFERENCE_APP_TOUR_SEEN,
  PREFERENCE_APP_TOUR_SNOOZED_UNTIL,
  PREFERENCE_TROUBLESHOOT_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_INVESTIGATIONS_TOUR_SEEN,
  PREFERENCE_TROUBLESHOOT_KG_TOUR_SEEN,
  PREFERENCE_OPTIMIZE_TOUR_SEEN,
  PREFERENCE_TICKETS_TOUR_SEEN,
  PREFERENCE_CLOUD_TOUR_SEEN,
  PREFERENCE_PRODUCT_UPDATES_LAST_SEEN,
];

export const GET_CLOUD_ACCOUNTS = `
query GetCloudAccounts {
    cloud_accounts: accounts_list(where: {status:{_eq:"active"}}) {
      rows {
        id
        account_name
        cloud_provider
      }
    }
}
`;

export const GET_K8s_NAMESPACES = `
query GetNamespaces {
    k8s_namespaces: k8s_namespaces_v2(where: {is_active:{_eq:true}}, limit : 1000) {
      rows{
        name
        account_id
      }
    }
}
`;

export const MANAGE_GROUP_USERS = `
mutation ManageGroupUsers($group_id: String!, $add_usernames: [String!]!, $remove_usernames: [String!]!) {
  usergroups_update_members(group_id: $group_id, add_usernames: $add_usernames, remove_usernames: $remove_usernames) {
    status
    message
  }
}
`;

export const UPDATE_USER = `
mutation UpdateUser($username: String!, $display_name: String, $status: String, $role: String) {
  users_update_profile(username: $username, display_name: $display_name, status: $status, role: $role) {
    status
    message
  }
}
`;

export const GET_ALL_STATUSES = `
query GetAllStatuses {
  users_list_status_types {
    value
  }
}`;

export const USER_TENANTS = `
query UserTenant($username:String!){
  users_list_tenants(object:{username:$username}){
    name
  }
}
`;

// Cross-tenant read — lists ALL tenants in the system (super_admin only;
// the gateway and the upstream `tenant_list_all` handler both enforce that).
// Used by the super-admin Switch-Tenant modal to reach tenants the user
// isn't a member of. Distinct from USER_TENANTS, which is member-scoped.
export const LIST_ALL_TENANTS = `
query ListAllTenants {
  tenant_list_all {
    id
    name
  }
}
`;

export const CREATE_USER = `
mutation CreateUser($username:String!, $firstname:String!, $lastname:String, $role:String){
  users_create(user:{firstname:$firstname, lastname:$lastname, username:$username role:$role}){
    status
    message
    id
    tenant_id 
  }
}
`;
export const UPDATE_USER_GROUP = `
mutation UpdateUserGroup($id: String!, $name: String, $description: String, $role: String) {
  usergroup_update(id: $id, name: $name, description: $description, role: $role) {
    status
    message
  }
}
`;

export const UPSERT_USER_GROUP_ACCOUNT_ROLES = `
mutation UpsertUserGroupAccountRoles($data:auth_account_group_roles_upsert_one_input!) {
  userroles_upsert_account_group(role:$data) {
    status
  }
}
`;

export const UPSERT_USER_GROUP_ACCOUNT_NAMESPACE_ROLES = `
mutation UpsertUserGroupAccountNamespaceRoles($data:auth_k8saccount_namespace_group_roles_upsert_one_input!) {
  userroles_upsert_k8s_namespace_group(role:$data) {
    status
  }
}
`;

// The group mutations below route their result through unwrapGraphQL
// (@lib/HttpService): queryGraphQL resolves rather than rejects on a
// GraphQL-level error, and these previously swallowed the failure (returning
// the error as a value), reporting a successful save after the write had
// actually been rejected upstream.

// Sets (or clears, with role: "") a group's tenant-level built-in role, without
// touching name/description. usergroup_update also accepts a `role`, but its
// name/description columns are unconditional overwrites — calling it to change
// only the role blanks both. Use this action for role-only saves.
export const UPSERT_USER_GROUP_TENANT_ROLE = `
mutation UpsertUserGroupTenantRole($data: auth_tenant_group_roles_upsert_one_input!) {
  userroles_upsert_group(role: $data) {
    status
  }
}
`;

export const GET_ALL_TENANT_ROLES = `
query getAllRoles {
  roles_list(object:{filter:"tenant%"}) {
    display_name
    value
  }
}
`;

const USER_HISTROY = `
query GetUserHistroy($accountId: String!, $module: String!, $limit: Int!, $offset: Int!) {
  users_list_history(where:{account_id:{_eq: $accountId}, module:{_eq: $module}}, limit: $limit, offset: $offset, order_by: [{column: "created_at", order: desc}]) {
    rows {
      data
      created_at
      status
      module
      duration
      meta
    }
  }
}`;

const CHECK_GROUPNAME_EXISTS = `
query checkGroupNameExists($name: String!) {
  usergroups_check_name_exists(object: {name: $name}) {
    id
    name
  }
}
`;

const LIST_USER_TOKENS = `
query ListUserTokens {
  users_list_token {
    tokens {
      id
      name
      provider
      status
      created_at
      accessed_at
    }
  }
}
`;

const CREATE_USER_TOKEN = `
mutation CreateUserToken($name: String!) {
  users_create_token(user: {name: $name}) {
    token
    name
  }
}
`;

const DELETE_USER_TOKEN = `
mutation DeleteUserToken($name: String!) {
  users_delete_token(user: {name: $name}) {
    name
  }
}
`;

const LIST_INTEGRATION_ACCOUNTS = `
query ListIntegrationAccounts($user_id: String!) {
  users_list_integration_accounts(user_id: $user_id) {
    id
    account_id
    account_name
    integration_type
    integration_id
    integration_name
    external_user_id
    username
    email
    display_name
    mapped_user_id
    mapped_via
    mapped_by_name
    last_synced_at
  }
}
`;

const LIST_UNMAPPED_ACCOUNTS = `
query ListUnmappedAccounts($integration_type: String) {
  users_list_unmapped_accounts(integration_type: $integration_type) {
    id
    account_id
    account_name
    integration_type
    integration_id
    integration_name
    external_user_id
    username
    email
    display_name
  }
}
`;

const CREATE_ACCOUNT_MAPPING = `
mutation CreateAccountMapping($mapping_id: String!, $user_id: String!) {
  users_create_account_mapping(mapping_id: $mapping_id, user_id: $user_id) {
    id
    status
  }
}
`;

const DELETE_ACCOUNT_MAPPING = `
mutation DeleteAccountMapping($mapping_id: String!) {
  users_delete_account_mapping(mapping_id: $mapping_id) {
    id
    status
  }
}
`;

const apiUser = {
  listK8sNamespaces: async function () {
    try {
      let response = await queryGraphQL(GET_K8s_NAMESPACES, 'GetNamespaces', {});
      return response.data.data;
    } catch (err) {
      return err;
    }
  },
  addUser: async function (bodyData) {
    let data = {
      firstname: bodyData['firstname'],
      username: bodyData.email,
      lastname: bodyData['lastname'],
      role: bodyData['role'] ?? '',
    };

    try {
      let response = await queryGraphQL(CREATE_USER, 'CreateUser', data);
      cache.delWithSuffix('user.listUsers');
      return response.data;
    } catch (err) {
      return err;
    }
  },
  listUsers: async function (bodyData) {
    if (!bodyData) {
      bodyData = {};
    }
    let params = { offset: bodyData.offset || 0, limit: bodyData.limit || 1000 };
    if (bodyData.status) {
      params.status = bodyData.status;
    }
    let cachedUserList = cache.getWithSuffix('user.listUsers', null, params);
    if (!cachedUserList) {
      let response = await getUsers(params);
      cache.setWithSuffix('user.listUsers', response, params, 60 * 60 * 1000);
      cachedUserList = response;
    }
    return {
      data: cachedUserList,
    };
  },
  listAccounts: async function () {
    try {
      const response = await queryGraphQL(GET_CLOUD_ACCOUNTS, 'GetCloudAccounts', {});
      const data = response.data?.data?.cloud_accounts?.rows ?? [];
      return data;
    } catch (err) {
      console.log('getWidget1Query Error is', err);
      return err;
    }
  },
  getUser: async function (id) {
    let response = await getUserById({ id: id });
    if (response.data && response.data.users && response.data.users.length > 0) {
      return {
        data: response.data.users[0],
      };
    }
    return {};
  },
  getAllStatuses: async function () {
    try {
      let response = await queryGraphQL(GET_ALL_STATUSES, 'GetAllStatuses');
      const items = response?.data?.data?.users_list_status_types || [];
      return {
        data: { user_status_type: items },
      };
    } catch (err) {
      return err;
    }
  },
  listUserGroups: async function (bodyData) {
    if (!bodyData) {
      bodyData = {};
    }
    let response = await getUserGroups({ offset: bodyData.offset || 0, limit: bodyData.limit || 100, nameSearch: bodyData.nameSearch });
    return {
      data: response,
    };
  },
  getUserGroup: async function (id) {
    let response = await getUserGroup({ id: id });
    return {
      data: response,
    };
  },
  listUserGroupUsers: async function (bodyData) {
    let response = await getUserGroupUsers({
      offset: bodyData.offset || 0,
      limit: bodyData.limit || 100,
      id: bodyData.id,
    });
    return {
      data: response,
    };
  },
  // role: '' clears the group's tenant role (backend DELETEs the group_roles row).
  upsertGroupTenantRole: async function ({ group_id, role }) {
    const response = await queryGraphQL(UPSERT_USER_GROUP_TENANT_ROLE, 'UpsertUserGroupTenantRole', {
      data: { group_id, role: role ?? '' },
    });
    return unwrapGraphQL(response, 'Failed to update tenant role')?.userroles_upsert_group;
  },
  upsertGroupAccountRoles: async function (data) {
    const response = await queryGraphQL(UPSERT_USER_GROUP_ACCOUNT_ROLES, 'UpsertUserGroupAccountRoles', { data: data });
    unwrapGraphQL(response, 'Failed to update account roles');
    return { data: response.data };
  },
  upsertGroupAccountNamespaceRoles: async function (data) {
    const response = await queryGraphQL(UPSERT_USER_GROUP_ACCOUNT_NAMESPACE_ROLES, 'UpsertUserGroupAccountNamespaceRoles', { data: data });
    unwrapGraphQL(response, 'Failed to update namespace roles');
    return { data: response.data };
  },
  manageGroupUsers: async function (data) {
    const response = await queryGraphQL(MANAGE_GROUP_USERS, 'ManageGroupUsers', {
      group_id: data.group_id,
      add_usernames: data.add_usernames || [],
      remove_usernames: data.remove_usernames || [],
    });
    unwrapGraphQL(response, 'Failed to update group members');
    return { data: response };
  },
  updateUser: async function (data) {
    try {
      let response = await queryGraphQL(UPDATE_USER, 'UpdateUser', {
        username: data.username,
        display_name: data.display_name,
        status: data.status,
        role: data.role,
      });
      cache.delWithSuffix('user.listUsers');
      return response.data;
    } catch (err) {
      return err;
    }
  },
  addUserGroup: async function (group, desc) {
    let response = await createUserGroup(group, desc);
    return {
      data: response,
    };
  },
  listIntegrationAccounts: async function (userId) {
    try {
      const response = await queryGraphQL(LIST_INTEGRATION_ACCOUNTS, 'ListIntegrationAccounts', { user_id: userId });
      return response?.data?.data?.users_list_integration_accounts ?? [];
    } catch {
      return [];
    }
  },
  listUnmappedAccounts: async function (integrationType) {
    try {
      const response = await queryGraphQL(LIST_UNMAPPED_ACCOUNTS, 'ListUnmappedAccounts', { integration_type: integrationType ?? null });
      return response?.data?.data?.users_list_unmapped_accounts ?? [];
    } catch {
      return [];
    }
  },
  createAccountMapping: async function ({ mappingId, userId }) {
    const response = await queryGraphQL(CREATE_ACCOUNT_MAPPING, 'CreateAccountMapping', { mapping_id: mappingId, user_id: userId });
    return response?.data?.data?.users_create_account_mapping;
  },
  deleteAccountMapping: async function ({ mappingId }) {
    const response = await queryGraphQL(DELETE_ACCOUNT_MAPPING, 'DeleteAccountMapping', { mapping_id: mappingId });
    return response?.data?.data?.users_delete_account_mapping;
  },
  listUserTenants: async function (username) {
    let userResponse = await queryGraphQL(USER_TENANTS, 'UserTenant', { username: username });
    return {
      data: userResponse?.data?.data?.users_list_tenants,
    };
  },
  // Super-admin only: list ALL tenants in the system (see LIST_ALL_TENANTS).
  listAllTenants: async function () {
    let response = await queryGraphQL(LIST_ALL_TENANTS, 'ListAllTenants', {});
    return {
      data: response?.data?.data?.tenant_list_all,
    };
  },
  updateUserGroup: async function (request) {
    const response = await queryGraphQL(UPDATE_USER_GROUP, 'UpdateUserGroup', {
      id: request.id,
      name: request.name,
      description: request.description,
      role: request.role,
    });
    return unwrapGraphQL(response, 'Failed to update group')?.usergroup_update;
  },
  getAllRoles: async function (_request) {
    const response = await queryGraphQL(GET_ALL_TENANT_ROLES, 'getAllRoles');
    return response?.data?.data?.roles_list || [];
  },
  getUserPreferences: function () {
    let data = localStorage.getItem('nudgebee.userPreferences');
    if (data) {
      try {
        return JSON.parse(data);
      } catch (err) {
        console.error('Error parsing user preferences', err);
      }
    }
    return {};
  },

  getUserPreferencesTablePageSize: function () {
    let data = localStorage.getItem('nudgebee.userPreferences');
    if (data) {
      try {
        data = JSON.parse(data);
      } catch (err) {
        console.error('Error parsing user preferences', err);
      }
    }
    return data?.[PREFERENCE_TABLE_PAGE_SIZE] ?? 10;
  },

  storeUserPreferences: function (key, value) {
    if (!availablePreferences.includes(key)) {
      console.error('Invalid user preference key', key);
      throw new Error('Invalid user preference key');
    }

    let data = localStorage.getItem('nudgebee.userPreferences');
    if (data) {
      try {
        data = JSON.parse(data);
      } catch (err) {
        console.error('Error parsing user preferences', err);
        data = null;
      }
    }
    if (data === null) {
      data = {};
    }
    data[key] = value;
    localStorage.setItem('nudgebee.userPreferences', JSON.stringify(data));
  },

  // Last account id the user selected for a given cloud_provider (K8s/AWS/Azure/GCP),
  // scoped per tenant. Lets a feature that needs "an account of this provider type"
  // fall back to the user's most recent pick for that specific provider, even if
  // their overall most-recent pick (PREFERENCE_LAST_ACCOUNT_ID) was a different
  // provider. Scoped by tenantId because account ids are tenant-specific — without
  // this, a user who belongs to/switches between multiple tenants could have a
  // cached account id from one tenant resolved while viewing another.
  getLastAccountIdForProvider: function (provider, tenantId) {
    if (!provider || !tenantId) {
      return null;
    }
    const prefs = apiUser.getUserPreferences() || {};
    const map = prefs[PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER] || {};
    // Keyed by the uppercased provider — callers pass cloud_provider verbatim
    // (e.g. ClusterDropDown.jsx's newClusterObj.cloud_provider), whose casing
    // isn't guaranteed consistent across accounts/backends, so normalize here
    // rather than relying on every call site to do it.
    return map[tenantId]?.[provider.toUpperCase()] || null;
  },

  setLastAccountIdForProvider: function (provider, accountId, tenantId) {
    if (!provider || !accountId || !tenantId) {
      return;
    }
    const prefs = apiUser.getUserPreferences() || {};
    const existing = prefs[PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER] || {};
    const map = { ...existing, [tenantId]: { ...(existing[tenantId] || {}), [provider.toUpperCase()]: accountId } };
    apiUser.storeUserPreferences(PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER, map);
  },

  // Reverse lookup of the per-provider map: the provider (uppercased, as
  // stored) whose last-selected account id for this tenant is `accountId`,
  // or null. Lets a page that only has an account id (e.g. /home?accountId=…)
  // resolve its provider type without waiting for the accounts list to load.
  getLastAccountProviderById: function (accountId, tenantId) {
    if (!accountId || !tenantId) {
      return null;
    }
    const prefs = apiUser.getUserPreferences() || {};
    const map = prefs[PREFERENCE_LAST_ACCOUNT_ID_BY_PROVIDER] || {};
    const entry = Object.entries(map[tenantId] || {}).find(([, id]) => id === accountId);
    return entry ? entry[0] : null;
  },

  // Up to RECENT_PAGE_SEARCHES_LIMIT most-recently-selected header search
  // result values (paths) for a tenant, most-recent first.
  getRecentPageSearches: function (tenantId) {
    if (!tenantId) {
      return [];
    }
    const prefs = apiUser.getUserPreferences() || {};
    const map = prefs[PREFERENCE_RECENT_PAGE_SEARCHES] || {};
    return map[tenantId] || [];
  },

  // Moves `value` to the front of the tenant's recent list, dedupes it out of
  // any existing position, and caps at RECENT_PAGE_SEARCHES_LIMIT.
  addRecentPageSearch: function (value, tenantId) {
    if (!value || !tenantId) {
      return;
    }
    const prefs = apiUser.getUserPreferences() || {};
    const existing = prefs[PREFERENCE_RECENT_PAGE_SEARCHES] || {};
    const currentList = existing[tenantId] || [];
    const updated = [value, ...currentList.filter((v) => v !== value)].slice(0, RECENT_PAGE_SEARCHES_LIMIT);
    const map = { ...existing, [tenantId]: updated };
    apiUser.storeUserPreferences(PREFERENCE_RECENT_PAGE_SEARCHES, map);
  },

  getHistory: async function ({ accountId, module, limit, offset }) {
    if (accountId === 'demo') {
      return {
        data: {
          users_create_history: [],
        },
      };
    }
    const response = await queryGraphQL(USER_HISTROY, 'GetUserHistroy', {
      accountId: accountId,
      module: module,
      limit: limit,
      offset: offset,
    });
    const rows = response.data?.data?.users_list_history?.rows || [];
    return {
      data: { users_create_history: rows },
    };
  },
  checkGroupNameExists: async function (name) {
    const response = await queryGraphQL(CHECK_GROUPNAME_EXISTS, 'checkGroupNameExists', { name: name });
    return {
      data: response?.data?.data.usergroups_check_name_exists,
      errors: response?.data?.errors,
    };
  },
  listUserTokens: async function () {
    try {
      const response = await queryGraphQL(LIST_USER_TOKENS, 'ListUserTokens', {});
      return {
        data: response?.data?.data?.users_list_token?.tokens || [],
        errors: response?.data?.errors,
      };
    } catch (err) {
      return {
        data: [],
        errors: [err],
      };
    }
  },
  createUserToken: async function (name) {
    try {
      const response = await queryGraphQL(CREATE_USER_TOKEN, 'CreateUserToken', { name: name });
      return {
        data: response?.data?.data?.users_create_token,
        errors: response?.data?.errors,
      };
    } catch (err) {
      return {
        data: null,
        errors: [err],
      };
    }
  },
  deleteUserToken: async function (name) {
    try {
      const response = await queryGraphQL(DELETE_USER_TOKEN, 'DeleteUserToken', { name: name });
      return {
        data: response?.data?.data?.users_delete_token,
        errors: response?.data?.errors,
      };
    } catch (err) {
      return {
        data: null,
        errors: [err],
      };
    }
  },
};
export default apiUser;
