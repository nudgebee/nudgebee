import { queryGraphQL } from '@lib/HttpService';

// Get budget status for a specific account (enhanced with daily data)
const AI_BUDGET_STATUS = `
query AIBudgetStatus($account_id: String!) {
  ai_budget_status(request: {account_id: $account_id}) {
    data {
      tenant_id
      account_id
      period
      today
      investigation {
        tenant {
          budget_disabled
          disabled_by
          disabled_at
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
        account {
          budget_disabled
          disabled_by
          disabled_at
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
      }
      user_investigation {
        tenant {
          budget_disabled
          disabled_by
          disabled_at
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
        account {
          budget_disabled
          disabled_by
          disabled_at
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
      }
    }
    err
  }
}
`;

// List budget configs with optional filters
const AI_LIST_BUDGET_CONFIG = `
query AIListBudgetConfig($entity_type: String, $entity_id: String, $module: String) {
  ai_list_budget_config(request: {entity_type: $entity_type, entity_id: $entity_id, module: $module}) {
    data
    errors
  }
}
`;

// Upsert (create or update) a budget config
const AI_UPSERT_BUDGET_CONFIG = `
mutation AIUpsertBudgetConfig(
  $entity_type: String!,
  $entity_id: String!,
  $module: String!,
  $budget_disabled: Boolean,
  $monthly_cost_limit: Float,
  $monthly_cost_enabled: Boolean,
  $monthly_count_limit: Int,
  $monthly_count_enabled: Boolean,
  $daily_cost_limit: Float,
  $daily_cost_enabled: Boolean,
  $daily_count_limit: Int,
  $daily_count_enabled: Boolean
) {
  ai_upsert_budget_config(request: {
    entity_type: $entity_type,
    entity_id: $entity_id,
    module: $module,
    budget_disabled: $budget_disabled,
    monthly_cost_limit: $monthly_cost_limit,
    monthly_cost_enabled: $monthly_cost_enabled,
    monthly_count_limit: $monthly_count_limit,
    monthly_count_enabled: $monthly_count_enabled,
    daily_cost_limit: $daily_cost_limit,
    daily_cost_enabled: $daily_cost_enabled,
    daily_count_limit: $daily_count_limit,
    daily_count_enabled: $daily_count_enabled
  }) {
    data
    errors
  }
}
`;

// Delete a budget config (revert to system defaults)
const AI_DELETE_BUDGET_CONFIG = `
mutation AIDeleteBudgetConfig($id: String!) {
  ai_delete_budget_config(request: {id: $id}) {
    data
    errors
  }
}
`;

// Get system defaults and max caps (read-only)
const AI_GET_BUDGET_SYSTEM_DEFAULTS = `
query AIGetBudgetSystemDefaults {
  ai_get_budget_system_defaults {
    data
    errors
  }
}
`;

export interface LimitInfo {
  enabled: boolean;
  limit: number;
  usage: number;
  remaining: number;
  limit_source: string;
}

export interface CountLimitInfo {
  enabled: boolean;
  limit: number;
  usage: number;
  remaining: number;
  limit_source: string;
}

export interface EntityBudgetStatus {
  budget_disabled: boolean;
  disabled_by?: string;
  disabled_at?: string;
  monthly_cost: LimitInfo;
  daily_cost: LimitInfo;
  monthly_count: CountLimitInfo;
  daily_count: CountLimitInfo;
}

export interface BudgetConfig {
  id: string;
  entity_type: string;
  entity_id: string;
  module: string;
  budget_disabled: boolean;
  disabled_by?: string;
  disabled_at?: string;
  monthly_cost_limit?: number;
  monthly_cost_enabled: boolean;
  monthly_count_limit?: number;
  monthly_count_enabled: boolean;
  daily_cost_limit?: number;
  daily_cost_enabled: boolean;
  daily_count_limit?: number;
  daily_count_enabled: boolean;
  updated_by?: string;
  updated_at: string;
  created_at: string;
}

export interface BudgetConfigUpsertRequest {
  entity_type: string;
  entity_id: string;
  module: string;
  budget_disabled?: boolean;
  monthly_cost_limit?: number;
  monthly_cost_enabled?: boolean;
  monthly_count_limit?: number;
  monthly_count_enabled?: boolean;
  daily_cost_limit?: number;
  daily_cost_enabled?: boolean;
  daily_count_limit?: number;
  daily_count_enabled?: boolean;
}

export interface MaxCapsInfo {
  monthly_cost_tenant: number;
  monthly_cost_account: number;
  daily_cost_tenant: number;
  daily_cost_account: number;
  monthly_count: number;
  daily_count: number;
}

const apiBudget = {
  getBudgetStatus: async function (accountId: string) {
    const response = await queryGraphQL(AI_BUDGET_STATUS, 'AIBudgetStatus', {
      account_id: accountId,
    });
    return {
      data: response?.data?.data?.ai_budget_status?.data,
      errors: response?.data?.data?.ai_budget_status?.err || response?.data?.errors,
    };
  },

  listBudgetConfigs: async function (entityType?: string, entityId?: string, module?: string) {
    const response = await queryGraphQL(AI_LIST_BUDGET_CONFIG, 'AIListBudgetConfig', {
      entity_type: entityType || null,
      entity_id: entityId || null,
      module: module || null,
    });
    const result = response?.data?.data?.ai_list_budget_config;
    return {
      data: result?.data,
      errors: result?.errors || response?.data?.errors,
    };
  },

  upsertBudgetConfig: async function (request: BudgetConfigUpsertRequest) {
    const response = await queryGraphQL(AI_UPSERT_BUDGET_CONFIG, 'AIUpsertBudgetConfig', request);
    const result = response?.data?.data?.ai_upsert_budget_config;
    return {
      data: result?.data,
      errors: result?.errors || response?.data?.errors,
    };
  },

  deleteBudgetConfig: async function (id: string) {
    const response = await queryGraphQL(AI_DELETE_BUDGET_CONFIG, 'AIDeleteBudgetConfig', {
      id: id,
    });
    const result = response?.data?.data?.ai_delete_budget_config;
    return {
      data: result?.data,
      errors: result?.errors || response?.data?.errors,
    };
  },

  getSystemDefaults: async function () {
    const response = await queryGraphQL(AI_GET_BUDGET_SYSTEM_DEFAULTS, 'AIGetBudgetSystemDefaults', {});
    const result = response?.data?.data?.ai_get_budget_system_defaults;
    return {
      data: result?.data,
      errors: result?.errors || response?.data?.errors,
    };
  },
};

export default apiBudget;
