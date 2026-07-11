import { queryGraphQL } from '@lib/HttpService';

// RPC module for the ownership feature. Each function maps 1:1 to an
// `ownership_*` action registered in src/lib/actions.yaml (handler:
// /rpc/ownership on api-server). The GraphQL shape is parsed by @lib/rpcGateway,
// which forwards the operation arguments verbatim as the action Input and wraps
// the upstream JSON under response.data.data.<action>.

const OWNER_FIELDS = `resource_type resource_key found owner_type owner_id owner_name source via`;
const RULE_FIELDS = `id name resource_domain match_scope match_key match_value cloud_account_id owner_type owner_id priority enabled`;

export const GET_OWNER = `
query OwnershipGet($resource_type: String!, $resource_key: String!) {
  ownership_get(resource_type: $resource_type, resource_key: $resource_key) {
    ${OWNER_FIELDS}
  }
}`;

export const RESOLVE_OWNERS = `
query OwnershipResolve($resources: jsonb!) {
  ownership_resolve(resources: $resources) {
    ${OWNER_FIELDS}
  }
}`;

export const ASSIGN_OWNER = `
mutation OwnershipAssign($resource_type: String!, $resource_key: String!, $owner_type: String!, $owner_id: String!, $cloud_account_id: String) {
  ownership_assign(resource_type: $resource_type, resource_key: $resource_key, owner_type: $owner_type, owner_id: $owner_id, cloud_account_id: $cloud_account_id) {
    status id
  }
}`;

export const REMOVE_OWNER = `
mutation OwnershipDelete($resource_type: String!, $resource_key: String!) {
  ownership_delete(resource_type: $resource_type, resource_key: $resource_key) {
    status count
  }
}`;

export const LIST_RULES = `
query OwnershipListRules {
  ownership_list_rules {
    ${RULE_FIELDS}
  }
}`;

export const UPSERT_RULE = `
mutation OwnershipUpsertRule($id: String, $name: String!, $resource_domain: String, $match_scope: String!, $match_key: String, $match_value: String, $cloud_account_id: String, $owner_type: String!, $owner_id: String!, $enabled: Boolean) {
  ownership_upsert_rule(id: $id, name: $name, resource_domain: $resource_domain, match_scope: $match_scope, match_key: $match_key, match_value: $match_value, cloud_account_id: $cloud_account_id, owner_type: $owner_type, owner_id: $owner_id, enabled: $enabled) {
    status id
  }
}`;

export const DELETE_RULE = `
mutation OwnershipDeleteRule($id: String!) {
  ownership_delete_rule(id: $id) {
    status count
  }
}`;

const apiOwnership = {
  // Effective owner of a single resource. Returns an OwnerResult (found=false
  // when unowned).
  getOwner: async function ({ resourceType, resourceKey }) {
    const response = await queryGraphQL(GET_OWNER, 'OwnershipGet', {
      resource_type: resourceType,
      resource_key: resourceKey,
    });
    return response?.data?.data?.ownership_get || null;
  },

  // Batch-resolve effective owners. `resources` is an array of
  // { resource_type, resource_key }. Returns OwnerResult[] aligned to input.
  resolveOwners: async function (resources) {
    if (!resources || resources.length === 0) {
      return [];
    }
    const response = await queryGraphQL(RESOLVE_OWNERS, 'OwnershipResolve', { resources });
    return response?.data?.data?.ownership_resolve || [];
  },

  // Manually assign (replace) the direct owner of a resource.
  assignOwner: async function ({ resourceType, resourceKey, ownerType, ownerId, cloudAccountId }) {
    const response = await queryGraphQL(ASSIGN_OWNER, 'OwnershipAssign', {
      resource_type: resourceType,
      resource_key: resourceKey,
      owner_type: ownerType,
      owner_id: ownerId,
      cloud_account_id: cloudAccountId || '',
    });
    return response?.data?.data?.ownership_assign || null;
  },

  // Remove a resource's direct owner.
  removeOwner: async function ({ resourceType, resourceKey }) {
    const response = await queryGraphQL(REMOVE_OWNER, 'OwnershipDelete', {
      resource_type: resourceType,
      resource_key: resourceKey,
    });
    return response?.data?.data?.ownership_delete || null;
  },

  listRules: async function () {
    const response = await queryGraphQL(LIST_RULES, 'OwnershipListRules', {});
    return response?.data?.data?.ownership_list_rules || [];
  },

  // Create (no id) or update (with id) a rule. Surfaces a backend validation /
  // conflict error (e.g. an overlapping rule) as a thrown Error so callers can
  // show the message.
  upsertRule: async function (rule) {
    const response = await queryGraphQL(UPSERT_RULE, 'OwnershipUpsertRule', {
      id: rule.id || '',
      name: rule.name,
      resource_domain: rule.resourceDomain || 'k8s',
      match_scope: rule.matchScope,
      match_key: rule.matchKey || '',
      match_value: rule.matchValue || '',
      cloud_account_id: rule.cloudAccountId || '',
      owner_type: rule.ownerType,
      owner_id: rule.ownerId,
      enabled: rule.enabled,
    });
    if (response?.data?.errors?.length) {
      throw new Error(response.data.errors[0]?.message || 'Failed to save rule');
    }
    return response?.data?.data?.ownership_upsert_rule || null;
  },

  deleteRule: async function (id) {
    const response = await queryGraphQL(DELETE_RULE, 'OwnershipDeleteRule', { id });
    return response?.data?.data?.ownership_delete_rule || null;
  },
};

export default apiOwnership;
