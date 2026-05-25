import { queryGraphQL } from '@lib/HttpService';
import { getUserById, getUsers, getUserGroups, getUserGroup, getUserGroupUsers, createUserGroup } from '@lib/UserService';
import cache from '@lib/cache';

export const PREFERENCE_LAST_ACCOUNT_ID = 'last_account';
export const PREFERENCE_TABLE_PAGE_SIZE = 'table_page_size';

const availablePreferences = [PREFERENCE_LAST_ACCOUNT_ID, PREFERENCE_TABLE_PAGE_SIZE];

export const GET_CLOUD_ACCOUNTS = `
query GetCloudAccounts {
    cloud_accounts: get_cloud_accounts_v2(where: {status:{_eq:"active"}}) {
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
  auth_manage_group_users(group_id: $group_id, add_usernames: $add_usernames, remove_usernames: $remove_usernames) {
    status
    message
  }
}
`;

export const UPDATE_USER = `
mutation UpdateUser($username: String!, $display_name: String, $status: String, $role: String) {
  user_update_profile(username: $username, display_name: $display_name, status: $status, role: $role) {
    status
    message
  }
}
`;

export const GET_ALL_STATUSES = `
query GetAllStatuses {
  user_status_types_list {
    value
  }
}`;

export const USER_TENANTS = `
query UserTenant($username:String!){
  user_list_tenants(object:{username:$username}){
    name
  }
}
`;

export const LIST_ALL_TENANTS = `
query ListAllTenants {
  tenant_list_all {
    name
  }
}
`;

export const CREATE_USER = `
mutation CreateUser($username:String!, $firstname:String!, $lastname:String, $role:String){
  users_insert_one(user:{firstname:$firstname, lastname:$lastname, username:$username role:$role}){
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
  auth_account_group_roles_upsert_one(role:$data) {
    status
  }
}
`;

export const UPSERT_USER_GROUP_ACCOUNT_NAMESPACE_ROLES = `
mutation UpsertUserGroupAccountNamespaceRoles($data:auth_k8saccount_namespace_group_roles_upsert_one_input!) {
  auth_k8saccount_namespace_group_roles_upsert_one(role:$data) {
    status
  }
}
`;

export const UPSERT_USER_TENANT_ACCOUNT_ROLES = `
mutation UpsertUserTenantAccountRoles($data:auth_tenant_group_roles_upsert_one_input) {
  tenant_group_roles_upsert_one: auth_tenant_group_roles_upsert_one(role:$auth_tenant_group_roles_upsert_one_input) {
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
  user_history_v2(where:{account_id:{_eq: $accountId}, module:{_eq: $module}}, limit: $limit, offset: $offset, order_by: [{column: "created_at", order: desc}]) {
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
  check_group_name_exists(object: {name: $name}) {
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
      const items = response?.data?.data?.user_status_types_list || [];
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
  upsertGroupAccountRoles: async function (data) {
    try {
      let response = await queryGraphQL(UPSERT_USER_GROUP_ACCOUNT_ROLES, 'UpsertUserGroupAccountRoles', { data: data });
      return {
        data: response.data,
      };
    } catch (err) {
      return err;
    }
  },
  upsertGroupAccountNamespaceRoles: async function (data) {
    try {
      let response = await queryGraphQL(UPSERT_USER_GROUP_ACCOUNT_NAMESPACE_ROLES, 'UpsertUserGroupAccountNamespaceRoles', { data: data });
      return {
        data: response.data,
      };
    } catch (err) {
      return err;
    }
  },
  manageGroupUsers: async function (data) {
    try {
      let response = await queryGraphQL(MANAGE_GROUP_USERS, 'ManageGroupUsers', {
        group_id: data.group_id,
        add_usernames: data.add_usernames || [],
        remove_usernames: data.remove_usernames || [],
      });
      return {
        data: response,
      };
    } catch (err) {
      return err;
    }
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
  listUserTenants: async function (username) {
    let userResponse = await queryGraphQL(USER_TENANTS, 'UserTenant', { username: username });
    return {
      data: userResponse?.data?.data?.user_list_tenants,
    };
  },
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
    return response.data.data.usergroup_update;
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

  getHistory: async function ({ accountId, module, limit, offset }) {
    if (accountId === 'demo') {
      return {
        data: {
          user_history: [],
        },
      };
    }
    const response = await queryGraphQL(USER_HISTROY, 'GetUserHistroy', {
      accountId: accountId,
      module: module,
      limit: limit,
      offset: offset,
    });
    const rows = response.data?.data?.user_history_v2?.rows || [];
    return {
      data: { user_history: rows },
    };
  },
  checkGroupNameExists: async function (name) {
    const response = await queryGraphQL(CHECK_GROUPNAME_EXISTS, 'checkGroupNameExists', { name: name });
    return {
      data: response?.data?.data.check_group_name_exists,
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
