import { queryGraphQL } from '@lib/HttpService';
import { getStartOfMonth, getEndOfMonth } from '@lib/datetime';

export const BILLING_LISTING = `
query getBillingsListing($limit: Int, $offset: Int) {
  billing_list(limit: $limit, offset: $offset) {
    billing {
      id
      amount_due
      last_billed_amount
      last_billed_date
      tier
      created_at
      updated_at
    }
    total_count {
      aggregate {
        count
      }
    }
  }
}
`;

const GET_BILLING_DETAILS_BY_MONTH = `
query getBillingDetailsByMonth($limit: Int, $offset: Int, $startDate: String!, $endDate: String!) {
  billing_usage_cost_list(limit: $limit, offset: $offset, start_date: $startDate, end_date: $endDate) {
    billing_usage_cost {
      billing_date
      cost_per_unit
      created_at
      id
      name
      service_name
      total_cost
      units
      updated_at
      account_id
      cloud_account {
        account_name
      }
    }
    billing_usage_cost_aggregate {
      aggregate {
        count
      }
    }
  }
}
`;

export const BILLING_INFOGRAPHICS = `
query BillingInfographics {
  billing_infographics {
    total_amount_due {
      aggregate {
        sum {
          amount_due
        }
      }
    }
    total_billed_amount {
      aggregate {
        sum {
          last_billed_amount
        }
      }
    }
  }
}
`;

export const AI_BUDGET_STATUS = `
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
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
        account {
          budget_disabled
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
      }
      user_investigation {
        tenant {
          budget_disabled
          monthly_cost { enabled limit usage remaining limit_source }
          daily_cost { enabled limit usage remaining limit_source }
          monthly_count { enabled limit usage remaining limit_source }
          daily_count { enabled limit usage remaining limit_source }
        }
        account {
          budget_disabled
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

const apiBilling = {
  getBillingsListing: async function (limit: number, offset: number) {
    const response = await queryGraphQL(BILLING_LISTING, 'getBillingsListing', {
      limit: limit,
      offset: offset,
    });
    return {
      data: response?.data?.data?.billing_list,
      errors: response?.data?.errors,
    };
  },
  getBillingsInfographics: async function () {
    const response = await queryGraphQL(BILLING_INFOGRAPHICS, 'BillingInfographics', {});
    return {
      data: response?.data?.data?.billing_infographics,
      errors: response?.data?.errors,
    };
  },
  getBillingDetailsByMonth: async function (query: any) {
    const startDate = getStartOfMonth(new Date(query.date));
    const endDate = getEndOfMonth(new Date(query.date));

    const response = await queryGraphQL(GET_BILLING_DETAILS_BY_MONTH, 'getBillingDetailsByMonth', {
      startDate: startDate.toISOString(),
      endDate: endDate.toISOString(),
      limit: query.limit,
      offset: query.offset,
    });
    return {
      data: response?.data?.data?.billing_usage_cost_list,
      errors: response?.data?.errors,
    };
  },
  getAIBudgetStatus: async function (accountId: string) {
    const response = await queryGraphQL(AI_BUDGET_STATUS, 'AIBudgetStatus', {
      account_id: accountId,
    });
    return {
      data: response?.data?.data?.ai_budget_status?.data,
      errors: response?.data?.data?.ai_budget_status?.err || response?.data?.errors,
    };
  },
};

export default apiBilling;
