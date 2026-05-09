import { queryGraphQL, gqlStringify } from '@lib/HttpService';
import { getGlobalStartDate, getGlobalEndDate } from '@lib/contexts';
import { getBudgetExpectedMonthlyExpense } from '@lib/budget';

export const GET_DISTINCT_RESOURCE_TYPE = `
query ListDistinctResourceType {
  cloud_resourses: resource_groupings_v2(column_transformations: {expr: "distinct", name: "resource_type"}) {
    rows {
      type: resource_type
    }
  }
}
`;

export const GET_DISTINCT_REGION = `
query ListDistinctRegion($account:String) {
  cloud_resourses: resource_groupings_v2(where:{account_id:{_eq:$account}}, column_transformations: {expr: "distinct", name: "resource_region"}) {
    rows {
      region: resource_region
    }
  }
}
`;

export const GET_DISTINCT_SERVICE = `
query ListDistinctService($account:String) {
  cloud_resourses: resource_groupings_v2(where:{account_id:{_eq:$account}}, column_transformations: {expr: "distinct", name: "resource_service_name"}) {
    rows {
      service_name: resource_service_name
    }
  }
}
`;

const RESOURCE_DETAILS = `query ListCloudResources {
  cloud_resourses: resource_details_v2(where: __WHERE__, limit: 1) {
    rows {
      account
      arn
      account_name
      cloud_provider
      account_synced_at
      account_type
      sync_status
      status
      region
      created_at
      updated_at
      first_seen
      last_seen
      resource_created_on
      tags
      meta
      type
      name
      spend_amount
      recommendation_count
      recommendation_estimated_savings
      critical_recommendation_count
    }
  }
}`;

const apiResources = {
  getResourceType: async function () {
    try {
      const response = await queryGraphQL(GET_DISTINCT_RESOURCE_TYPE, 'ListDistinctResourceType', {});
      return {
        data: (response.data.data.cloud_resourses?.rows || []).map((c: any) => c.type),
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  getResourceServices: async function (account_id: string) {
    try {
      const response = await queryGraphQL(GET_DISTINCT_SERVICE, 'ListDistinctService', {
        account: account_id,
      });
      return {
        data: (response.data.data.cloud_resourses?.rows || []).map((c: any) => c.service_name),
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  getResourceDetils: async function (id: string, query: any = {}) {
    try {
      query = query || {};
      const startDate = query['startDate'] || getGlobalStartDate();
      const endDate = query['endDate'] || getGlobalEndDate();

      const whereParams: any = {
        id: { _eq: id },
        spend_date: {
          _gte: startDate instanceof Date ? startDate.toISOString() : startDate,
          _lte: endDate instanceof Date ? endDate.toISOString() : endDate,
        },
      };

      const formattedQuery = RESOURCE_DETAILS.replace('__WHERE__', gqlStringify(whereParams));
      const response = await queryGraphQL(formattedQuery, 'ListCloudResources', {});
      const rows = response?.data?.data?.cloud_resourses?.rows || [];
      const resource = rows[0] || null;

      if (resource) {
        // Map flat fields to nested structure for backward compatibility
        resource.cloud_account = {
          id: resource.account,
          account_name: resource.account_name,
          cloud_provider: resource.cloud_provider,
          synced_at: resource.account_synced_at,
          account_type: resource.account_type,
          sync_status: resource.sync_status,
        };
        resource.resourse_created_on = resource.resource_created_on;
        resource.spends_aggregate = {
          aggregate: { sum: { amount: resource.spend_amount } },
        };
        resource.recommendations_aggregate = {
          aggregate: {
            count: resource.recommendation_count,
            sum: { estimated_savings: resource.recommendation_estimated_savings },
          },
        };
        resource.critical_recommendations_aggregate = {
          aggregate: { count: resource.critical_recommendation_count },
        };
        resource.spend_estimated = getBudgetExpectedMonthlyExpense(resource.spend_amount);
      }

      return {
        data: resource,
      };
    } catch (err) {
      console.log('Your Error is', err);
      return err;
    }
  },
  async getResourceGroupings(
    limit = 10,
    offset = 0,
    query: {
      account_id: string;
      resource_service_name?: string;
      resource_type?: string;
      resource_type_nin?: string[];
      resource_region?: string;
      spend_start_date?: Date;
      spend_end_date?: Date;
      resourceStatus?: string;
      recommendationStatus?: string;
      resource_tag_key?: string;
    },
    groupBy = ['resource_service_name', 'account_id', 'tenant_id'],
    cols: Array<string> = [
      'tenant_id',
      'account_id',
      'resource_service_name',
      'count_resource',
      'sum_spend_amount',
      'sum_recommendation_estimated_savings',
      'count_recommendation',
    ],
    orderBy = { name: '', order: '' }
  ) {
    try {
      const filterParams: any = {};
      if (query?.account_id) {
        filterParams['account_id'] = { _eq: query.account_id };
      }

      const endDate = query.spend_end_date;
      const startDate = query.spend_start_date;

      if (startDate && endDate) {
        filterParams['spend_date'] = {
          _between: {
            _gte: startDate.toISOString(),
            _lte: endDate.toISOString(),
          },
        };
      }

      query.resourceStatus = query.resourceStatus ?? 'Active';
      filterParams['resource_status'] = { _eq: query.resourceStatus };

      query.recommendationStatus = query.recommendationStatus ?? 'Open';
      filterParams['recommendation_status'] = { _eq: query.recommendationStatus };

      if (query.resource_service_name) {
        filterParams['resource_service_name'] = { _eq: query.resource_service_name };
      }

      if (query.resource_type) {
        filterParams['resource_type'] = { _eq: query.resource_type };
      }

      // Support for excluding resource types (e.g., billing line items)
      if (query.resource_type_nin) {
        filterParams['resource_type'] = { _nin: query.resource_type_nin };
      }

      if (query.resource_region) {
        filterParams['resource_region'] = { _eq: query.resource_region };
      }

      if (query.resource_tag_key) {
        filterParams['resource_tags'] = { _has_key: query.resource_tag_key };
      }

      const LIST_RESOURCE_GROUPINGS = `
query k8s_resource_groupings($limit:Int,$offset:Int){
  resource_groupings: resource_groupings_v2(where:__WHERE__, group_by:__GROUP_BY__, limit:$limit,offset:$offset,order_by:__ORDER_BY__){
    rows{
      __COLS__
    }
  }

  resource_groupings_aggregate: resource_groupings_v2(where:__WHERE__, group_by:__GROUP_BY__, columns:__FIRST_COL__){
    rows{
      __FIRST_COL__
    }
  }
}
`;
      let formattedQuery = LIST_RESOURCE_GROUPINGS.replaceAll('__WHERE__', gqlStringify(filterParams));
      formattedQuery = formattedQuery.replaceAll('__GROUP_BY__', gqlStringify(groupBy));
      formattedQuery = formattedQuery.replaceAll('__COLS__', cols.join(' '));
      formattedQuery = formattedQuery.replaceAll('__FIRST_COL__', cols[0]);
      if (orderBy.name) {
        const orderWithNulls = orderBy.order === 'desc' ? 'desc_nulls_last' : 'asc_nulls_last';
        formattedQuery = formattedQuery.replaceAll('__ORDER_BY__', gqlStringify([{ column: orderBy.name, order: orderWithNulls }], ['order']));
      } else {
        formattedQuery = formattedQuery.replaceAll('__ORDER_BY__', `[]`);
      }

      const response = await queryGraphQL(formattedQuery, 'k8s_resource_groupings', {
        limit: limit,
        offset: offset,
      });

      // Count the number of groups (rows) returned by the aggregate query
      const aggregateCount = response?.data?.data?.resource_groupings_aggregate?.rows?.length ?? 0;

      return {
        data: {
          resource_groupings: response?.data?.data?.resource_groupings?.rows,
          resource_groupings_aggregate: {
            aggregate: {
              count: aggregateCount,
            },
          },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  // Get filtered resource counts - excludes billing line items using resource_groupings_v2
  async getFilteredResourceCounts(accountId: string) {
    if (accountId === 'demo') return { data: [] };
    try {
      // Billing line item types to exclude (not actual resources)
      const excludedTypes = [
        // AWS billing line items
        'data-transfer',
        'api-request',
        'usage',
        'cpu-credits',
        'data-payload',
        'storage-snapshot',
        'request',
        'requests',
        'data',
        'bandwidth',
        'tax',
        // GCP billing aggregates (actual resources have googleapis.com in type)
        'compute-engine',
        'cloud-sql',
        'kubernetes-engine',
        'networking',
        'bigquery',
        'vertex-ai',
        'vertex-ai-model',
        'subnet',
      ];

      // Build filter params safely using gqlStringify
      const filterParams = {
        account_id: { _eq: accountId },
        resource_status: { _eq: 'Active' },
        resource_type: { _not_in: excludedTypes },
      };

      // Use resource_groupings_v2 with _not_in filter to properly aggregate resource counts
      const FILTERED_RESOURCES_QUERY = `
query FilteredResourceCounts {
  resource_groupings: resource_groupings_v2(
    where: __WHERE__,
    group_by: ["resource_service_name"],
    columns: ["resource_service_name", "count_resource"]
  ) {
    rows {
      resource_service_name
      count_resource
    }
  }
}
`;

      const formattedQuery = FILTERED_RESOURCES_QUERY.replace('__WHERE__', gqlStringify(filterParams));
      const response = await queryGraphQL(formattedQuery, 'FilteredResourceCounts', {});

      // Map the response to expected format
      const rows = response?.data?.data?.resource_groupings?.rows || [];
      const services = rows
        .map((r: { resource_service_name: string; count_resource: number }) => ({
          service_name: r.resource_service_name,
          count: r.count_resource || 0,
        }))
        .filter((s: { service_name: string; count: number }) => s.service_name)
        .sort((a: { count: number }, b: { count: number }) => b.count - a.count);

      return { data: services };
    } catch (error) {
      console.log('Error fetching filtered resource counts:', error);
      throw error;
    }
  },
};

export default apiResources;
