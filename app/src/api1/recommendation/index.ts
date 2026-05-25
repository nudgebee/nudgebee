import { queryGraphQL, splitAndParallelQuery, gqlStringify } from '@lib/HttpService';
export const RECOMMENDATION_STATUS = ['Open', 'Archive', 'Closed', 'Dismissed', 'Assigned', 'InProgress'];
export const RECOMMENDATION_SERVERITY = ['Critical', 'High', 'Medium', 'Low', 'Info'];
import { getStartOfYear, getEndOfYear, getStartOfDay, getEndOfDay } from '@lib/datetime';
import ticketsApi from '@api1/tickets';
import { recommendationDetails } from './data';
import getMockData from '@api1/mock';
import { safeJSONParse } from 'src/utils/common';
import cache from '@lib/cache';

// Slim projection used by the Optimization → Summary tab. Excludes large JSON
// blobs (resource_meta, finops_score_breakdown) and unused fields
// (resource_cloud_service, finops_band) that the summary transformer never reads.
// Distinct from LIST_k8_RECOMMENDATION_SUMMARY below, which returns aggregate
// counts + spend totals for infographics.
export const LIST_k8_OPTIMISE_SUMMARY_RECOMMENDATIONS = `
query list_k8_recommendation_summary($limit:Int, $offset:Int) {
  recommendation: recommendations_v2(where: __WHERE__, limit: $limit, offset:$offset, order_by: __ORDER_BY__) {
    rows{
      id
      account_id
      account_object_id
      resource_id
      resource_name
      resource_type
      resource_k8s_namespace
      severity
      category
      rule_name
      recommendation
      estimated_savings
      finops_score
      created_at
      updated_at
    }
  }
}`;

const OPTIMISE_SUMMARY_RECS_CACHE_KEY = 'optimise_summary_recommendations';
const OPTIMISE_SUMMARY_RECS_TTL_SEC = 10 * 60; // 10 minutes — recommendations refresh on a ~6h cron

export function invalidateOptimisationSummaryRecommendations() {
  cache.delWithSuffix(OPTIMISE_SUMMARY_RECS_CACHE_KEY);
}

export const LIST_k8_RECOMMENDATIONS = `
query list_k8_recommendation($limit:Int, $offset:Int) {
  recommendation: recommendations_v2(where: __WHERE__, limit: $limit, offset:$offset,order_by: __ORDER_BY__) {
    rows{
      resource_k8s_namespace
      resource_meta
      resource_name
      resource_type
      resource_id
      resource_cloud_service
      severity
      category
      rule_name
      recommendation
      estimated_savings
      account_object_id
      updated_at
      created_at
      id
      account_id
      finops_score
      finops_band
      finops_score_breakdown
    }
  }
  recommendation_aggregate: recommendation_groupings_v2(where: __WHERE__){
    rows{
      count
    }
  }
}`;

export const LIST_k8_RECOMMENDATION_SUMMARY = `
query k8s_recommendation_summary {
  recommendation_aggregate: recommendation_groupings_v2(where: __WHERE__){
    rows{
      count
      sum_estimated_savings
    }
  }
  recommendation_expense_aggregate:recommendation_groupings_v2(where: __WHERE2__){
    rows{
      count
      sum_estimated_savings
    }
  }
  spends_aggregate: spend_groupings_v2(where: __WHERE3__){
    rows{
      spend_amount
    }
  }
  yesterday_spends_aggregate: spend_groupings_v2(where: __WHERE4__){
    rows{
      spend_amount
    }
  }
}`;

export const LIST_k8_RECOMMENDATION_SUMMARY_BY_RULENAME = `
query k8s_recommendation_summary {
  recommendation_aggregate: recommendation_groupings_v2(where: __WHERE__){
    rows{
      count
      sum_estimated_savings
      rule_name
      category
      resource_cloud_service
      severity
    }
  }
}`;

export const LIST_k8_SECURITY_RECOMMENDATIONS = `
query list_k8_recommendation($limit:Int, $offset:Int) {
  recommendation: recommendation_security_v2(where: __WHERE__, limit: $limit, offset:$offset,order_by: [{column:"severity_weight", order: desc}]) {
    rows{
      severity
      recommendation
      image
      created_at
      id      
      namespace
      workload_name
      package_id
    }
  }
  recommendation_aggregate: recommendation_security_groupings_v2(where: __WHERE__){
    rows{
      count
    }
  }
}`;

export const LIST_k8_SECURITY_CIS_RECOMMENDATIONS = `
query list_k8_recommendation($limit:Int, $offset:Int) {
  recommendation: recommendation_security_cis_groupings_v2(where: __WHERE__, limit: $limit, offset:$offset,order_by: [{column:"severity_weight", order: desc}]) {
    rows{
      account_id
      severity
      severity_weight
      rule_id
      rule_name      
      rule_description
      count
      updated_at
    }
  }
}`;

export const LIST_AUTO_OPTIMIZE = `
query listAutoOptimize {
  auto_pilot_v2(where:__WHERE__) {
    rows {
      id
      category
      status
      notification
      auto_optimize_resource_maps
    }
  }
}
`;

export const ETL_APPLY_RECOMMENDATION = `
mutation ApplyRecommendation($account_id: String!, $recommendation_id: String!, $data: jsonb, $provider: String, $provider_config: ProviderConfig) {
  apply_recommendations(object: {account_id: $account_id, recommendation_id: $recommendation_id, data: $data, provider: $provider, provider_config: $provider_config }) {
    data
  }
}
`;

export const ETL_APPLY_EVENT_RECOMMENDATION = `
mutation ApplyRecommendation($account_id: String!, $event_id: String!, $data: jsonb, $provider: String, $provider_config: ProviderConfig) {
  event_resolve(object: {account_id: $account_id, event_id: $event_id, data: $data, provider: $provider, provider_config: $provider_config }) {
    data
  }
}
`;

export const GET_SECURITY_RECOMMENDATION_LISTING_APPS = `
query get_security_recommendation {
  recommendation_security_groupings_v2(where:__WHERE__, order_by:[{column: "count_image", order: desc}, {column: "count_severity_critical", order: desc},{column: "count_severity_high", order: desc},{column: "count_severity_medium", order: desc},{column: "count_severity_low", order: desc}]) {
    rows {
      count_severity_low
      count_image
      account_id
      tenant_id
      count_severity_high
      count_severity_critical
      count_severity_medium
      workload_name
      namespace
    }
  }
}
`;
export const LIST_k8_RECOMMENDATIONS_AGGREGATE = `
query ListK8sRecommendationAggregate {
  recommendation_aggregate: recommendation_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
}`;

export const GET_SECURITY_RECOMMENDATION_LISTING_IMAGES = `query get_security_recommendation {
  recommendation_security_groupings_v2(where:__WHERE__, order_by:[ {column: "count_severity_critical", order: desc},{column: "count_severity_high", order: desc},{column: "count_severity_medium", order: desc},{column: "count_severity_low", order: desc}]) {
    rows {
      count_severity_low
      account_id
      tenant_id
      count_severity_high
      count_severity_critical
      count_severity_medium
      image
      package_id
      created_at
    }
  }
}`;

export const GET_SECURITY_RECOMMENDATION_LISTING_CVE = `
query get_security_recommendation {
  recommendation_security_groupings_v2(where:__WHERE__, order_by: {column: "severity_weight", order: desc}) {
    rows {
      account_id
      tenant_id
      vulnerability_id
      severity
      count_image
      count_workload_name
      severity_weight
      count
    }
  }
}`;

export const GET_SECURITY_SEVERITY_GROUPING = `
query get_security_severity_groupings {
  recommendation_security_groupings_v2(where:__WHERE__) {
    rows {
      account_id
      tenant_id
      count_severity_high
      count_severity_medium
      count_severity_low
      count_severity_critical
      count_workload_name
      count_image
      count_vulnerability_id
    }
  }
}
`;

export const K8S_OPTIMIZE_SUMMARY_INFOGRAPHICS = `
query K8sOptimizeSummaryInfographics($accountId: String, $startDate: Datetime, $endDate: Datetime) {
  workload_rightsize: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["RightSizing"]}, rule_name: {_in: ["pod_right_sizing"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  replica_rightsize: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["RightSizing"]}, rule_name: {_in: ["replica_right_sizing"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  pv_rightsize: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["RightSizing"]}, rule_name: {_in: ["pv_rightsize"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  spot_instance: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["K8sSpotRecommendation"]}, rule_name: {_in: ["Spot instance recommendation"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  unused_pvc: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["RightSizing"]}, rule_name: {_in: ["unused_pvc"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  abandoned_resource: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["RightSizing"]}, rule_name: {_in: ["abandoned_resource"]}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
      sum_estimated_savings
    }
  }
  spends_aggregate: spend_groupings_v2(where: {account_id: {_eq: $accountId}, spend_date: {_gte: $startDate, _lte: $endDate}, exclude_aggregate: {_eq: false}}) {
    rows {
      spend_amount
    }
  }
  count_recommendations: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
    }
  }
  count_optimize_recommendations: recommendation_groupings_v2(where: {account_id: {_eq: $accountId}, category: {_in: ["K8sSpotRecommendation","RightSizing"]},status: {_in: ["Open", "InProgress"]}}) {
    rows {
      count
    }
  }
}
`;

export const NODE_RECOMMENDATION = `
mutation NodeRecommendation($account: String!, $graviton: Boolean!, $instance_groups: [String!]!, $tenant_id: String!, $number_of_recommendations: Int) {
  generate_node_recommendations(account: $account, graviton: $graviton, preferred_instance_groups: $instance_groups, tenant: $tenant_id, number_of_recommendations: $number_of_recommendations) {
    data
  }
}
`;

async function getK8sRecommendationMockData({
  _accountId,
  category,
  ruleName,
}: {
  _accountId?: string;
  category?: string;
  ruleName?: string | string[];
  severity?: string | string[];
  status?: string[];
  resourceNamespace?: string;
  resourceWorkloadType?: string;
  orderBy?: string;
  orderAsc?: boolean;
  limit?: number;
  offset?: number;
}) {
  const recommendationDemo = await getMockData('recommendations');
  if (category === 'InfraUpgrade' && ruleName === 'k8s_api_deprecated') {
    return {
      data: recommendationDemo.k8s_api_deleted.data,
    };
  } else if (category === 'InfraUpgrade' && ruleName === 'helm_chart_upgrade') {
    return {
      data: recommendationDemo.HelmUpgrade.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'pod_right_sizing') {
    return {
      data: recommendationDemo.RightSizing.list_k8_recommendation.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'unused_pvc') {
    return {
      data: recommendationDemo.UnusedVolume.list_k8_recommendation.data,
    };
  } else if (category === 'Configuration' && ruleName === 'certificate_expiry') {
    return {
      data: recommendationDemo.CertificateExpiry.data,
    };
  } else if (category === 'Configuration') {
    return {
      data: recommendationDemo.BestPractices.list_k8_recommendation.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'abandoned_resource') {
    return {
      data: recommendationDemo.AbandonedApplications.list_k8_recommendation.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'pv_rightsize') {
    return {
      data: recommendationDemo.PVRightSizing.list_k8_recommendation.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'replica_right_sizing') {
    return {
      data: recommendationDemo.ReplicaRightSizing.list_k8_recommendation.data,
    };
  } else if (category === 'K8sSpotRecommendation') {
    return {
      data: recommendationDemo.SpotRecommendation.list_k8_recommendation.data,
    };
  } else if (category === 'Security' && ruleName === 'k8s-cis-1.23') {
    return {
      data: recommendationDemo.CIS,
    };
  } else if (category === 'Security' && ruleName === 'image_scan') {
    return {
      data: {
        recommendation: recommendationDemo.image_scan.data.recommendation?.rows,
        recommendation_aggregate: recommendationDemo.image_scan.data.recommendation_aggregate?.rows[0],
      },
    };
  }
}

async function getK8sRecommendationSummaryMockData({
  _accountId,
  category,
  ruleName,
  _severity,
  _status = ['Open', 'Assigned'],
  _resourceWorkloadType,
  _resourceNamespace,
}: {
  _accountId: string;
  category?: string;
  ruleName?: string | string[];
  _severity?: string | string[];
  _status?: string[];
  _resourceWorkloadType?: string;
  _resourceNamespace?: string;
}) {
  const recommendationDemo = await getMockData('recommendations');
  if (category === 'InfraUpgrade' && ruleName === 'k8s_api_deleted') {
    return {
      data: recommendationDemo.k8s_api_deleted.k8s_recommendation_summary,
    };
  } else if (category === 'InfraUpgrade' && ruleName === 'helm_chart_upgrade') {
    return {
      data: recommendationDemo.HelmUpgrade.k8s_recommendation_summary,
    };
  } else if (category === 'RightSizing' && ruleName === 'pod_right_sizing') {
    return {
      data: recommendationDemo.RightSizing.k8s_recommendation_summary.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'unused_pvc') {
    return {
      data: recommendationDemo.UnusedVolume.k8s_recommendation_summary.data,
    };
  } else if (category === 'Configuration') {
    return {
      data: recommendationDemo.BestPractices.k8s_recommendation_summary.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'abandoned_resource') {
    return {
      data: recommendationDemo.AbandonedApplications.k8s_recommendation_summary.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'pv_rightsize') {
    return {
      data: recommendationDemo.PVRightSizing.k8s_recommendation_summary.data,
    };
  } else if (category === 'RightSizing' && ruleName === 'replica_right_sizing') {
    return {
      data: recommendationDemo.ReplicaRightSizing.k8s_recommendation_summary.data,
    };
  } else if (category === 'K8sSpotRecommendation') {
    return {
      data: recommendationDemo.SpotRecommendation.k8s_recommendation_summary.data,
    };
  }
}

const apiRecommendations = {
  async getAutoOptimize(query: any) {
    try {
      const gqlQuery: any = {};
      if (query.accountId) {
        gqlQuery['account_id'] = { _eq: query.accountId };
      }
      if (query.category) {
        if (Array.isArray(query.category)) {
          gqlQuery['category'] = { _in: query.category };
        } else {
          gqlQuery['category'] = { _eq: query.category };
        }
      }
      if (query.status) {
        gqlQuery['status'] = { _eq: query.status };
      }
      const response = await queryGraphQL(LIST_AUTO_OPTIMIZE.replaceAll('__WHERE__', gqlStringify(gqlQuery)), 'listAutoOptimize', {});
      const autoPilotRows = response?.data?.data?.auto_pilot_v2?.rows?.map((item: any) => ({
        ...item,
        auto_optimize_resource_maps:
          typeof item.auto_optimize_resource_maps === 'string' ? JSON.parse(item.auto_optimize_resource_maps) : item.auto_optimize_resource_maps,
        notification: typeof item.notification === 'string' ? JSON.parse(item.notification) : item.notification,
      }));
      return {
        data: {
          auto_pilot: autoPilotRows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async getK8sRecommendation({
    accountId,
    category,
    ruleName,
    severity,
    status = ['Open', 'Assigned'],
    resourceNamespace,
    resourceWorkloadType,
    resourceWorkloadName,
    accountObjectId,
    recommendation,
    resourceMeta,
    orderBy = 'estimated_savings',
    orderAsc = false,
    limit = 10,
    offset = 0,
    fetchTicket = false,
    serviceName = '',
    version = 0,
    resource_ids,
  }: {
    accountId?: string;
    category?: string;
    ruleName?: string | string[];
    severity?: string | string[];
    status?: string[];
    resourceNamespace?: string;
    resourceWorkloadType?: string;
    resourceWorkloadName?: string;
    accountObjectId?: string;
    recommendation?: any;
    resourceMeta?: any;
    orderBy?: any;
    orderAsc?: boolean;
    limit?: number;
    offset?: number;
    fetchTicket?: boolean;
    serviceName?: string;
    version?: number;
    resource_ids?: string[];
  }) {
    try {
      if (accountId === 'demo') {
        return await getK8sRecommendationMockData({
          _accountId: accountId,
          category,
          ruleName,
          severity,
          status,
          resourceNamespace,
          resourceWorkloadType,
          orderBy,
          orderAsc,
          limit,
          offset,
        });
      }

      const gqlQuery: any = {};
      if (Array.isArray(accountId)) {
        gqlQuery['account_id'] = { _in: accountId };
      } else if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (accountObjectId) {
        gqlQuery['account_object_id'] = { _ilike: '%' + accountObjectId + '%' };
      }
      if (Array.isArray(category)) {
        gqlQuery['category'] = { _in: category };
      } else if (category) {
        gqlQuery['category'] = { _eq: category };
      }
      if (Array.isArray(ruleName)) {
        gqlQuery['rule_name'] = { _in: ruleName };
      } else if (ruleName) {
        gqlQuery['rule_name'] = { _eq: ruleName };
      }
      if (Array.isArray(severity)) {
        gqlQuery['severity'] = { _in: severity };
      } else if (severity) {
        gqlQuery['severity'] = { _eq: severity };
      }
      if (status && status.length > 0) {
        gqlQuery['status'] = { _in: status };
      }
      if (recommendation) {
        gqlQuery['recommendation'] = { _contains: JSON.stringify(recommendation) };
      }
      if (resourceMeta) {
        gqlQuery['resource_meta'] = { _contains: JSON.stringify(resourceMeta) };
      }
      if (resourceNamespace) {
        gqlQuery['resource_k8s_namespace'] = { _eq: resourceNamespace };
      }

      if (resourceWorkloadType) {
        gqlQuery['resource_type'] = { _eq: resourceWorkloadType };
      }
      if (resourceWorkloadName) {
        gqlQuery['resource_name'] = { _ilike: resourceWorkloadName + '%' };
      }
      if (serviceName) {
        gqlQuery['resource_cloud_service'] = { _eq: serviceName };
      }
      if (version > 0) {
        gqlQuery['_and'] = [
          {
            _or: [
              {
                deleted_version: {
                  _eq: version,
                },
              },
              {
                deprecated_version: {
                  _eq: version,
                },
              },
            ],
          },
        ];
      }
      if (resource_ids) {
        gqlQuery['resource_id'] = { _in: resource_ids };
      }
      let query = LIST_k8_RECOMMENDATIONS;
      if (typeof orderBy === 'string') {
        query = query.replaceAll('__ORDER_BY__', `{column: "${orderBy}", order: ${orderAsc ? 'asc' : 'desc_nulls_last'} }`);
      } else if (Object.keys(orderBy).length !== 0) {
        query = query.replaceAll('__ORDER_BY__', `{column: "${orderBy.name}", order: ${orderBy.order} }`);
      } else {
        query = query.replaceAll('__ORDER_BY__', '[{column: "estimated_savings", order:desc}, {column: "severity", order:desc}]');
      }
      query = query.replaceAll('__WHERE__', gqlStringify(gqlQuery, []));

      const response = await queryGraphQL(query, 'list_k8_recommendation', {
        limit: limit,
        offset: offset,
      });

      // data update so that rest of things work same
      response?.data?.data?.recommendation?.rows?.forEach((item: any) => {
        if (item.recommendation) {
          const recommendation = safeJSONParse(item.recommendation);
          if (recommendation) {
            item.recommendation = recommendation;
            if (item.estimated_savings != null && category == 'InfraUpgrade') {
              item.recommendation.estimated_savings = item.estimated_savings;
            }
          }
        }
        item.cloud_resourse = {
          name: item.resource_name,
          id: item.resource_id,
          type: item.resource_type,
          meta: item.resource_meta ? JSON.parse(item.resource_meta) : {},
        };
      });

      if (fetchTicket) {
        const recommendationIds = response?.data?.data?.recommendation?.rows?.map((item: any) => item.id) || [];
        if (recommendationIds.length > 0) {
          const tickets: any = await ticketsApi.listTicketsSummary({ reference_id: recommendationIds });
          const ticketReferenceMap = new Map();
          tickets?.data?.tickets.forEach((element: any) => {
            ticketReferenceMap.set(element.reference_id, element);
          });

          response.data.data.recommendation.rows.forEach((item: any) => {
            item.ticket = ticketReferenceMap.get(item.id);
            return item;
          });

          // Fetch active PR resolutions for listed recommendations
          const resolutionMap: any = await this.listActiveResolutionsByRecommendationIds(recommendationIds);
          response.data.data.recommendation.rows.forEach((item: any) => {
            item.resolution = resolutionMap.get(item.id) || null;
            return item;
          });
        }
      }
      return {
        data: {
          recommendation: response?.data?.data?.recommendation?.rows,
          recommendation_aggregate: {
            aggregate: {
              count: response?.data?.data?.recommendation_aggregate?.rows[0]?.count,
            },
          },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  // Slim, cached fetcher dedicated to the Optimization → Summary tab.
  // Returns only the fields used by transformApiToInsight, with a 10-min TTL cache.
  // Invalidated by invalidateOptimisationSummaryRecommendations() when state mutates.
  async getOptimisationSummaryRecommendations({
    category,
    status = ['Open', 'InProgress'],
    orderBy = 'finops_score',
    orderAsc = false,
    limit = 100,
    refresh = false,
  }: {
    category?: string[];
    status?: string[];
    orderBy?: string;
    orderAsc?: boolean;
    limit?: number;
    refresh?: boolean;
  }) {
    const cacheSuffix = { category: (category || []).join(','), status: status.join(','), orderBy, orderAsc, limit };
    if (!refresh) {
      const cached = cache.getWithSuffix(OPTIMISE_SUMMARY_RECS_CACHE_KEY, null, cacheSuffix);
      if (cached) return cached;
    }

    const where: any = {};
    if (category && category.length > 0) where['category'] = { _in: category };
    if (status && status.length > 0) where['status'] = { _in: status };

    let query = LIST_k8_OPTIMISE_SUMMARY_RECOMMENDATIONS;
    query = query.replaceAll('__ORDER_BY__', `{column: "${orderBy}", order: ${orderAsc ? 'asc' : 'desc_nulls_last'} }`);
    query = query.replaceAll('__WHERE__', gqlStringify(where, []));

    try {
      const response = await queryGraphQL(query, 'list_k8_recommendation_summary', { limit, offset: 0 });
      const rows = response?.data?.data?.recommendation?.rows ?? [];
      rows.forEach((item: any) => {
        if (typeof item.recommendation === 'string') {
          const parsed = safeJSONParse(item.recommendation);
          if (parsed) item.recommendation = parsed;
        }
      });
      const result = { data: { recommendation: rows } };
      cache.setWithSuffix(OPTIMISE_SUMMARY_RECS_CACHE_KEY, result, cacheSuffix, OPTIMISE_SUMMARY_RECS_TTL_SEC);
      return result;
    } catch (error) {
      console.error('getOptimisationSummaryRecommendations error', error);
      return { data: { recommendation: [] } };
    }
  },

  async getK8sRecommendationSummary({
    accountId,
    accountObjectId,
    category,
    ruleName,
    severity,
    status = ['Open', 'Assigned'],
    recommendation,
    resourceWorkloadType,
    resourceNamespace,
    serviceName = '',
    resource_ids,
    version = 0,
  }: {
    accountId: string;
    accountObjectId?: string;
    category?: string;
    ruleName?: string | string[];
    severity?: string;
    status?: string[];
    recommendation?: any;
    resourceWorkloadType?: string;
    resourceNamespace?: string;
    resourceMeta?: any;
    serviceName?: string;
    resource_ids?: string[];
    version?: number;
  }) {
    if (accountId === 'demo') {
      return await getK8sRecommendationSummaryMockData({
        _accountId: accountId,
        category,
        ruleName,
        _severity: severity,
        _status: status,
        _resourceWorkloadType: resourceWorkloadType,
        _resourceNamespace: resourceNamespace,
      });
    }

    try {
      const yeserdayStartDay = getStartOfDay(new Date());
      yeserdayStartDay.setDate(yeserdayStartDay.getDate() - 1);

      const gqlQuery: any = {};
      if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (accountObjectId) {
        gqlQuery['account_object_id'] = { _ilike: '%' + accountObjectId + '%' };
      }
      if (Array.isArray(category)) {
        gqlQuery['category'] = { _in: category };
      } else if (category) {
        gqlQuery['category'] = { _eq: category };
      }
      if (Array.isArray(ruleName)) {
        gqlQuery['rule_name'] = { _in: ruleName };
      } else if (ruleName) {
        gqlQuery['rule_name'] = { _eq: ruleName };
      }
      if (severity) {
        gqlQuery['severity'] = { _eq: severity };
      }
      if (status && status.length > 0) {
        gqlQuery['status'] = { _in: status };
      }
      if (recommendation) {
        gqlQuery['recommendation'] = { _contains: JSON.stringify(recommendation) };
      }
      if (resourceWorkloadType) {
        gqlQuery['resource_type'] = { _eq: resourceWorkloadType };
      }
      if (resourceNamespace) {
        gqlQuery['resource_k8s_namespace'] = { _eq: resourceNamespace };
      }
      if (serviceName) {
        gqlQuery['resource_cloud_service'] = { _eq: serviceName };
      }
      if (resource_ids) {
        gqlQuery['resource_id'] = { _in: resource_ids };
      }
      if (version > 0) {
        gqlQuery['_and'] = [
          {
            _or: [
              {
                deleted_version: {
                  _eq: version,
                },
              },
              {
                deprecated_version: {
                  _eq: version,
                },
              },
            ],
          },
        ];
      }
      const where2: any = {
        ...(accountId ? { account_id: { _eq: accountId } } : {}),
        category: { _eq: 'RightSizing' },
        rule_name: { _eq: 'pod_right_sizing' },
        status: { _in: ['Open', 'InProgress'] },
        estimated_savings: { _lt: 0 },
      };
      const where3: any = {
        ...(accountId ? { account_id: { _eq: accountId } } : {}),
        spend_date: { _gt: getStartOfYear(new Date()).toISOString(), _lt: getEndOfYear(new Date()).toISOString() },
        exclude_aggregate: { _eq: false },
      };
      const where4: any = {
        ...(accountId ? { account_id: { _eq: accountId } } : {}),
        spend_date: { _gt: yeserdayStartDay.toISOString(), _lt: getEndOfDay(yeserdayStartDay).toISOString() },
        exclude_aggregate: { _eq: false },
      };
      const query = LIST_k8_RECOMMENDATION_SUMMARY.replace('__WHERE4__', gqlStringify(where4))
        .replace('__WHERE3__', gqlStringify(where3))
        .replace('__WHERE2__', gqlStringify(where2))
        .replace('__WHERE__', gqlStringify(gqlQuery));

      const response = await queryGraphQL(query, 'k8s_recommendation_summary', {});

      return {
        data: {
          yesterday_spends_aggregate: { aggregate: { sum: { amount: response?.data?.data?.yesterday_spends_aggregate?.rows?.[0]?.spend_amount } } },
          spends_aggregate: { aggregate: { sum: { amount: response?.data?.data?.spends_aggregate?.rows?.[0]?.spend_amount } } },
          recommendation_aggregate: {
            aggregate: {
              count: response?.data?.data?.recommendation_aggregate?.rows?.[0]?.count,
              sum: {
                estimated_savings: response?.data?.data?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings,
              },
            },
          },
          recommendation_expense_aggregate: {
            aggregate: {
              count: response?.data?.data?.recommendation_expense_aggregate?.rows?.[0]?.count,
              sum: {
                estimated_savings: response?.data?.data?.recommendation_expense_aggregate?.rows?.[0]?.sum_estimated_savings,
              },
            },
          },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sRecommendationSummaryByRuleName({
    accountId,
    accountObjectId,
    category,
    ruleName,
    severity,
    status = ['Open', 'Assigned'],
    recommendation,
    resourceWorkloadType,
    resourceNamespace,
    serviceName = '',
    resource_ids,
    version = 0,
  }: {
    accountId: string | string[];
    accountObjectId?: string;
    category?: string;
    ruleName?: string | string[];
    severity?: string | string[];
    status?: string[];
    recommendation?: any;
    resourceWorkloadType?: string;
    resourceNamespace?: string;
    resourceMeta?: any;
    serviceName?: string;
    resource_ids?: string[];
    version?: number;
  }) {
    if (accountId === 'demo') {
      return await getK8sRecommendationSummaryMockData({
        _accountId: accountId,
        category,
        ruleName,
        _severity: severity,
        _status: status,
        _resourceWorkloadType: resourceWorkloadType,
        _resourceNamespace: resourceNamespace,
      });
    }

    try {
      const gqlQuery: any = {};
      if (Array.isArray(accountId)) {
        gqlQuery['account_id'] = { _in: accountId };
      } else if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (accountObjectId) {
        gqlQuery['account_object_id'] = { _ilike: '%' + accountObjectId + '%' };
      }
      if (Array.isArray(category)) {
        gqlQuery['category'] = { _in: category };
      } else if (category) {
        gqlQuery['category'] = { _eq: category };
      }
      if (Array.isArray(ruleName)) {
        gqlQuery['rule_name'] = { _in: ruleName };
      } else if (ruleName) {
        gqlQuery['rule_name'] = { _eq: ruleName };
      }
      if (Array.isArray(severity)) {
        gqlQuery['severity'] = { _in: severity };
      } else if (severity) {
        gqlQuery['severity'] = { _eq: severity };
      }
      if (status && status.length > 0) {
        gqlQuery['status'] = { _in: status };
      }
      if (recommendation) {
        gqlQuery['recommendation'] = { _contains: JSON.stringify(recommendation) };
      }
      if (resourceWorkloadType) {
        gqlQuery['resource_type'] = { _eq: resourceWorkloadType };
      }
      if (resourceNamespace) {
        gqlQuery['resource_k8s_namespace'] = { _eq: resourceNamespace };
      }
      if (Array.isArray(serviceName)) {
        gqlQuery['resource_cloud_service'] = { _in: serviceName };
      } else if (serviceName) {
        gqlQuery['resource_cloud_service'] = { _eq: serviceName };
      }
      if (resource_ids) {
        gqlQuery['resource_id'] = { _in: resource_ids };
      }
      if (version > 0) {
        gqlQuery['_and'] = [
          {
            _or: [
              {
                deleted_version: {
                  _eq: version,
                },
              },
              {
                deprecated_version: {
                  _eq: version,
                },
              },
            ],
          },
        ];
      }
      const query = LIST_k8_RECOMMENDATION_SUMMARY_BY_RULENAME.replace('__WHERE__', gqlStringify(gqlQuery));

      const response = await queryGraphQL(query, 'k8s_recommendation_summary', {});
      return response?.data?.data?.recommendation_aggregate?.rows || [];
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async getK8sSecurityCISRecommendationGroups({
    accountId,
    severity,
    orderBy = 'severity_weight',
    orderAsc = false,
    status = '',
  }: {
    accountId?: string;
    severity?: string;
    orderBy?: string;
    orderAsc?: boolean;
    status?: string;
  }) {
    try {
      if (accountId === 'demo') {
        return await getK8sRecommendationMockData({
          _accountId: accountId,
          category: 'Security',
          ruleName: 'k8s-cis-1.23',
          severity,
          status: ['Open', 'Assigned'],
          resourceNamespace: '',
          resourceWorkloadType: '',
          orderBy,
          orderAsc,
          limit: 1000,
          offset: 0,
        });
      }

      const gqlQuery: any = {};
      if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (severity) {
        gqlQuery['severity'] = { _eq: severity };
      }
      if (status) {
        gqlQuery['status'] = { _eq: status };
      }
      const query = LIST_k8_SECURITY_CIS_RECOMMENDATIONS.replaceAll('__WHERE__', gqlStringify(gqlQuery, []));

      const response = await queryGraphQL(query, 'list_k8_recommendation', {
        limit: 1000,
        offset: 0,
      });

      return {
        data: {
          recommendation: response?.data?.data?.recommendation?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async getK8sSecurityRecommendation({
    accountId,
    severity,
    status = ['Open', 'Assigned'],
    ruleName = 'image_scan',
    resourceNamespace,
    resourceWorkload,
    image,
    orderBy = 'severity',
    orderAsc = false,
    limit = 10,
    offset = 0,
    fetchTicket = false,
    vulnerabilityId,
    package_id,
  }: {
    accountId?: string;
    severity?: string | string[];
    status?: string[];
    ruleName?: string | string[];
    resourceNamespace?: string;
    resourceWorkload?: string;
    image?: string;
    orderBy?: string;
    orderAsc?: boolean;
    limit?: number;
    offset?: number;
    fetchTicket?: boolean;
    vulnerabilityId?: string;
    package_id: string;
  }) {
    try {
      if (accountId === 'demo') {
        return await getK8sRecommendationMockData({
          _accountId: accountId,
          category: 'Security',
          ruleName: ruleName,
          severity,
          status,
          resourceNamespace,
          resourceWorkloadType: resourceWorkload,
          orderBy,
          orderAsc,
          limit,
          offset,
        });
      }

      const gqlQuery: any = {};
      if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (image) {
        gqlQuery['image'] = { _like: '%' + image + '%' };
      }
      if (severity) {
        if (Array.isArray(severity)) {
          gqlQuery['severity'] = { _in: severity };
        } else {
          gqlQuery['severity'] = { _eq: severity };
        }
      }
      if (status && status.length > 0) {
        gqlQuery['status'] = { _in: status };
      }
      if (resourceNamespace) {
        gqlQuery['namespace'] = { _eq: resourceNamespace };
      }
      if (resourceWorkload) {
        gqlQuery['workload_name'] = { _eq: resourceWorkload };
      }
      if (vulnerabilityId) {
        gqlQuery['vulnerability_id'] = { _eq: vulnerabilityId };
      }
      if (package_id) {
        gqlQuery['package_id'] = { _eq: package_id };
      }

      const query = LIST_k8_SECURITY_RECOMMENDATIONS.replaceAll('__WHERE__', gqlStringify(gqlQuery, []));

      const response = await queryGraphQL(query, 'list_k8_recommendation', {
        limit: limit,
        offset: offset,
      });

      if (fetchTicket) {
        const recommendationIds = response?.data?.data?.recommendation?.rows?.map((item: any) => item.id) ?? [];
        if (recommendationIds.length > 0) {
          const tickets: any = await ticketsApi.listTicketsSummary({ reference_id: recommendationIds });
          const ticketReferenceMap = new Map();
          tickets?.data?.tickets.forEach((element: any) => {
            ticketReferenceMap.set(element.reference_id, element);
          });

          response.data.data.recommendation.rows.forEach((item: any) => {
            item.ticket = ticketReferenceMap.get(item.id);
            return item;
          });

          // Fetch active PR resolutions for security recommendations
          const resolutionMap: any = await this.listActiveResolutionsByRecommendationIds(recommendationIds);
          response.data.data.recommendation.rows.forEach((item: any) => {
            item.resolution = resolutionMap.get(item.id) || null;
            return item;
          });
        }
      }

      return {
        data: {
          recommendation: response?.data?.data?.recommendation?.rows,
          recommendation_aggregate: response?.data?.data?.recommendation_aggregate?.rows[0],
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async listRecommendationNamesapces({
    accountId,
    status,
    category,
    ruleName,
  }: {
    accountId: string;
    status: string;
    category: string;
    ruleName: string;
  }): Promise<string[]> {
    if (accountId === 'demo') {
      return [];
    }
    const statusFilter = status ? `, status: {_eq: "${status}"}` : '';

    if (
      (category === 'RightSizing' && (ruleName === 'pv_rightsize' || ruleName === 'abandoned_resource' || ruleName === 'replica_right_sizing')) ||
      category === 'K8sSpotRecommendation'
    ) {
      const ruleFilter = ruleName ? `, rule_name: {_eq: "${ruleName}"}` : '';
      const NAMESAPCES = `
      query list_namespaces {
        recommendation: recommendation_groupings_v2(where: {account_id: {_eq: "${accountId}"}, category: {_eq: "${category}"}${statusFilter}${ruleFilter}}, column_transformations: [{expr: "distinct", name: "resource_k8s_namespace"}]) {
          rows {
            resource_k8s_namespace
          }
        }
      }
    `;
      const response = await queryGraphQL(NAMESAPCES, 'list_namespaces', {});
      return [
        ...new Set(response?.data?.data?.recommendation?.rows?.map((item: any) => item.resource_k8s_namespace).filter(Boolean) ?? []),
      ] as string[];
    } else if (category === 'Security') {
      const NAMESAPCES = `
      query list_namespaces {
        recommendation: recommendation_security_groupings_v2(where: {account_id:{_eq:"${accountId}"}${statusFilter}}, column_transformations: [{expr: "distinct", name: "namespace"}]) {
          rows{
            namespace
          }
        }
      }
    `;
      const response = await queryGraphQL(NAMESAPCES, 'list_namespaces', {});
      return [...new Set(response?.data?.data?.recommendation?.rows?.map((item: any) => item.namespace) ?? [])] as string[];
    } else if (category === 'Configuration') {
      // Get namespaces from both resource_k8s_namespace field and from recommendation JSON
      const withRuleName = !!ruleName;
      const withStatus = !!status;
      const NAMESAPCES = `
      query list_namespaces($accountId: String!${withStatus ? ', $status: String!' : ''}${withRuleName ? ', $ruleName: String!' : ''}) {
        recommendation: recommendations_v2(where: {
          account_id: {_eq: $accountId},
          category: {_eq: "Configuration"}${withStatus ? ', status: {_eq: $status}' : ''}${withRuleName ? ', rule_name: {_eq: $ruleName}' : ''}
        }) {
          rows{
            resource_k8s_namespace
            recommendation
          }
        }
      }
    `;
      const variables: { accountId: string; status?: string; ruleName?: string } = {
        accountId,
      };
      if (withStatus) {
        variables.status = status;
      }
      if (withRuleName) {
        variables.ruleName = ruleName;
      }

      const response = await queryGraphQL(NAMESAPCES, 'list_namespaces', variables);
      const namespaces = new Set<string>();

      response?.data?.data?.recommendation?.rows?.forEach((item: any) => {
        // Add namespace from resource_k8s_namespace field
        if (item.resource_k8s_namespace) {
          namespaces.add(item.resource_k8s_namespace);
        }

        // Parse recommendation JSON to extract additional namespaces
        if (item.recommendation) {
          try {
            const rec = typeof item.recommendation === 'string' ? JSON.parse(item.recommendation) : item.recommendation;
            if (Array.isArray(rec)) {
              rec.forEach((r: any) => {
                if (r?.namespace) {
                  namespaces.add(r.namespace);
                }
              });
            } else if (rec?.namespace) {
              namespaces.add(rec.namespace);
            }
          } catch (e) {
            console.log('error', e);
            // Ignore JSON parse errors
          }
        }
      });

      return Array.from(namespaces).filter((n) => n);
    }

    return [];
  },

  async listRecommendationWorkloads({
    accountId,
    status,
    category,
    _ruleName,
    namespaceName,
  }: {
    accountId: string;
    status: string;
    category: string;
    _ruleName: string;
    namespaceName: string;
  }): Promise<string[]> {
    if (accountId === 'demo' || !namespaceName) {
      return [];
    }

    status = status ?? 'Open';
    if (category === 'Security') {
      const NAMESAPCES = `
      query list_workloads {
        recommendation: recommendation_security_groupings_v2(where: {account_id:{_eq:"${accountId}"}, namespace:{_eq:"${namespaceName}"},status:{_in:["${status}"]}}, column_transformations: [{expr: "distinct", name: "workload_name"}]) {
          rows{
            workload_name
          }
        }
      }
    `;
      const response = await queryGraphQL(NAMESAPCES, 'list_workloads', {});
      return [...new Set(response?.data?.data?.recommendation?.rows?.map((item: any) => item.workload_name) ?? [])] as string[];
    }

    return [];
  },

  async applyRecommendation(
    accountId: string,
    recommendationId: string,
    data?: any,
    provider?: string,
    provider_config?: any,
    recommendationSource?: string
  ) {
    if (accountId === 'demo') {
      return {
        data: null,
        errors: [{ message: 'Apply recommendation is not supported for Demo account.' }],
      };
    }
    if (recommendationSource === 'event') {
      const response = await queryGraphQL(ETL_APPLY_EVENT_RECOMMENDATION, 'ApplyRecommendation', {
        account_id: accountId,
        event_id: recommendationId,
        data: data,
        provider: provider,
        provider_config: provider_config,
      });
      invalidateOptimisationSummaryRecommendations();
      return {
        data: response?.data?.data?.apply_recommendations?.data,
        errors: response?.data?.errors,
      };
    }
    const response = await queryGraphQL(ETL_APPLY_RECOMMENDATION, 'ApplyRecommendation', {
      account_id: accountId,
      recommendation_id: recommendationId,
      data: data,
      provider: provider,
      provider_config: provider_config,
    });
    invalidateOptimisationSummaryRecommendations();
    return {
      data: response?.data?.data?.apply_recommendations?.data,
      errors: response?.data?.errors,
    };
  },

  async listRecommendationResolution(recommendationId: string, limit = 0, offset = 0) {
    const GET_RESOLUTION = `
      query GetResolution($where: RecommendationResolutionWhereRequest, $whereAgg: RecommendationResolutionGroupingsWhereRequest, $limit: Int, $offset: Int) {
        recommendation_resolution: recommendation_resolution_v2(where: $where, limit: $limit, offset: $offset, order_by: [{column: "updated_at", order: desc}]) {
          rows {
            created_at
            updated_at
            data
            id
            resolver_id
            resolver_type
            status
            status_message
            type
            type_reference_id
          }
        }
        recommendation_resolution_aggregate: recommendation_resolution_groupings_v2(where: $whereAgg) {
          rows {
            count
          }
        }
      }
    `;
    const params: any = {
      where: { recommendation_id: { _eq: recommendationId } },
      whereAgg: { recommendation_id: { _eq: recommendationId } },
    };
    if (limit) {
      params.limit = limit;
    }
    if (offset) {
      params.offset = offset;
    }
    const response = await queryGraphQL(GET_RESOLUTION, 'GetResolution', params);
    const rows = response?.data?.data?.recommendation_resolution?.rows || [];
    const aggRows = response?.data?.data?.recommendation_resolution_aggregate?.rows;
    return {
      data: {
        recommendation_resolution: rows.map((r: any) => ({ ...r, data: typeof r.data === 'string' ? safeJSONParse(r.data) : r.data })),
        recommendation_resolution_aggregate: { aggregate: { count: aggRows?.[0]?.count || 0 } },
      },
      errors: response?.data?.errors,
    };
  },

  async listEventResolutions(eventId: string) {
    const GET_EVENT_RESOLUTIONS = `
      query GetEventResolutions($where: EventResolutionWhereRequest) {
        event_resolution: event_resolution_v2(where: $where, order_by: [{column: "updated_at", order: desc}]) {
          rows {
            id
            event_id
            type
            status
            data
            status_message
            type_reference_id
            resolver_type
            created_at
            updated_at
          }
        }
      }
    `;
    const response = await queryGraphQL(GET_EVENT_RESOLUTIONS, 'GetEventResolutions', {
      where: { event_id: { _eq: eventId } },
    });
    const rows = response?.data?.data?.event_resolution?.rows || [];
    return rows.map((r: any) => ({ ...r, data: typeof r.data === 'string' ? safeJSONParse(r.data) : r.data }));
  },

  async listAllEventResolutions(data: any) {
    const GET_ALL_EVENT_RESOLUTIONS = `
    query AllEventResolutions($where: EventResolutionWhereRequest, $whereAgg: EventResolutionGroupingsWhereRequest, $limit:Int, $offset:Int) {
      event_resolution: event_resolution_v2(where: $where, limit: $limit, offset: $offset, order_by: [{column: "updated_at", order: desc}]) {
        rows {
          id
          event_id
          type
          status
          status_message
          data
          type_reference_id
          resolver_type
          resolver_display_name
          created_at
          updated_at
          event_subject_name
          event_subject_namespace
          event_cloud_account_id
          event_priority
          event_category
        }
      }
      event_resolution_aggregate: event_resolution_groupings_v2(where: $whereAgg) {
        rows {
          count
        }
      }
    }
    `;
    const where: any = {};
    const whereAgg: any = {};
    if (data.accountId) {
      if (Array.isArray(data.accountId) && data.accountId.length) {
        where.event_cloud_account_id = { _in: data.accountId };
        whereAgg.event_cloud_account_id = { _in: data.accountId };
      } else if (typeof data.accountId === 'string') {
        where.event_cloud_account_id = { _eq: data.accountId };
        whereAgg.event_cloud_account_id = { _eq: data.accountId };
      }
    }
    if (data.status) {
      where.status = { _eq: data.status };
      whereAgg.status = { _eq: data.status };
    }
    if (data.type) {
      where.type = { _eq: data.type };
      whereAgg.type = { _eq: data.type };
    }
    if (data.resolverType) {
      where.resolver_type = { _eq: data.resolverType };
      whereAgg.resolver_type = { _eq: data.resolverType };
    }
    const response = await queryGraphQL(GET_ALL_EVENT_RESOLUTIONS, 'AllEventResolutions', {
      where,
      whereAgg,
      limit: data.limit,
      offset: data.offset,
    });
    const rows = response?.data?.data?.event_resolution?.rows || [];
    const aggRows = response?.data?.data?.event_resolution_aggregate?.rows;
    return {
      data: {
        data: {
          event_resolution: rows.map((r: any) => ({
            ...r,
            data: typeof r.data === 'string' ? safeJSONParse(r.data) : r.data,
            resolver_user: { display_name: r.resolver_display_name },
            event: {
              subject_name: r.event_subject_name,
              subject_namespace: r.event_subject_namespace,
              cloud_account_id: r.event_cloud_account_id,
              priority: r.event_priority,
              category: r.event_category,
            },
          })),
          event_resolution_aggregate: { aggregate: { count: aggRows?.[0]?.count || 0 } },
        },
      },
    };
  },

  async createRecommendationJob(accountId: string, jobName: string) {
    if (accountId === 'demo') return { data: null, errors: null };
    const ETL_APPLY_RECOMMENDATION = `
    mutation TriggerRecommendation($account_id: String!, $job_name: String!) {
      recommendation_job_create(account_id: $account_id, job_name: $job_name) {
        data
      }
    }
    `;

    const response = await queryGraphQL(ETL_APPLY_RECOMMENDATION, 'TriggerRecommendation', {
      account_id: accountId,
      job_name: jobName,
    });
    return {
      data: response?.data?.recommendation_job_create?.data,
      errors: response?.data?.errors,
    };
  },

  async listRecommendationFilter(accountId: string, output: any, query: any) {
    if (accountId === 'demo') return null;
    let RECOMMENDATION_FILTERS = `
    query ListRecommendationFilters {
      recommendation: recommendation_groupings_v2(where: __WHERE__) {
        rows {
          __OUTPUT__        
        }
      }
    }
    `;
    const where: any = {};
    where.account_id = { _eq: accountId };
    if (query.category) {
      where.category = { _eq: query.category };
    }
    if (query.serviceName) {
      where['resource_cloud_service'] = { _eq: query.serviceName };
    }
    if (query.ruleName) {
      if (Array.isArray(query.ruleName)) {
        where['rule_name'] = { _in: query.ruleName };
      } else {
        where['rule_name'] = { _eq: query.ruleName };
      }
    }
    if (output.includes('rule_name')) {
      RECOMMENDATION_FILTERS = RECOMMENDATION_FILTERS.replaceAll('__OUTPUT__', 'rule_name').replaceAll('__WHERE2__', 'rule_name');
    }
    if (output.includes('resource_cloud_service')) {
      RECOMMENDATION_FILTERS = RECOMMENDATION_FILTERS.replaceAll('__OUTPUT__', 'resource_cloud_service').replaceAll(
        '__WHERE2__',
        'resource_cloud_service'
      );
    }
    const response = await queryGraphQL(RECOMMENDATION_FILTERS.replaceAll('__WHERE__', gqlStringify(where, [])), 'ListRecommendationFilters', {});
    return {
      data: {
        data: {
          recommendation: response?.data?.data?.recommendation?.rows,
        },
      },
    };
  },
  async listPRResolutionsByRecommendationIds(recommendationIds: string[]) {
    if (!recommendationIds || recommendationIds.length === 0) {
      return new Map();
    }
    const GET_PR_RESOLUTIONS = `
      query GetPRResolutions($where: RecommendationResolutionWhereRequest) {
        recommendation_resolution: recommendation_resolution_v2(where: $where, order_by: [{column: "updated_at", order: desc}]) {
          rows {
            id
            recommendation_id
            type_reference_id
            status
            type
            updated_at
          }
        }
      }
    `;
    try {
      const response = await queryGraphQL(GET_PR_RESOLUTIONS, 'GetPRResolutions', {
        where: {
          recommendation_id: { _in: recommendationIds },
          type: { _eq: 'PullRequest' },
        },
      });
      const resolutions = response?.data?.data?.recommendation_resolution?.rows || [];
      const resolutionMap = new Map();
      for (const resolution of resolutions) {
        if (!resolutionMap.has(resolution.recommendation_id)) {
          resolutionMap.set(resolution.recommendation_id, resolution);
        }
      }
      return resolutionMap;
    } catch (error) {
      console.error('Error fetching PR resolutions:', error);
      return new Map();
    }
  },
  async listActiveResolutionsByRecommendationIds(recommendationIds: string[]) {
    if (!recommendationIds || recommendationIds.length === 0) {
      return new Map();
    }
    const GET_ACTIVE_RESOLUTIONS = `
      query GetActiveResolutions($where: RecommendationResolutionWhereRequest) {
        recommendation_resolution: recommendation_resolution_v2(where: $where, order_by: [{column: "updated_at", order: desc}]) {
          rows {
            id
            recommendation_id
            type_reference_id
            status
            status_message
            type
            updated_at
          }
        }
      }
    `;
    try {
      const response = await queryGraphQL(GET_ACTIVE_RESOLUTIONS, 'GetActiveResolutions', {
        where: {
          recommendation_id: { _in: recommendationIds },
          status: { _eq: 'InProgress' },
          type: { _eq: 'PullRequest' },
        },
      });
      const resolutions = response?.data?.data?.recommendation_resolution?.rows || [];
      const resolutionMap = new Map();
      for (const resolution of resolutions) {
        if (!resolutionMap.has(resolution.recommendation_id)) {
          resolutionMap.set(resolution.recommendation_id, resolution);
        }
      }
      return resolutionMap;
    } catch (error) {
      console.error('Error fetching active resolutions:', error);
      return new Map();
    }
  },
  async listAppSecurityRecommendation(accountId: string, query: any) {
    if (accountId == 'demo') {
      const recommendationDemo = await getMockData('recommendations');
      return recommendationDemo.K8sSecurity.app;
    }
    const where: any = {};
    where.account_id = { _eq: accountId };
    if (query.namespace) {
      where.namespace = { _eq: query.namespace };
    }
    if (query.workload_name) {
      where.workload_name = { _eq: query.workload_name };
    }
    if (query.severity?.length) {
      where.severity = Array.isArray(query.severity) ? { _in: query.severity } : { _eq: query.severity };
    }
    if (query.status) {
      where.status = { _eq: query.status };
    }
    const response = await queryGraphQL(
      GET_SECURITY_RECOMMENDATION_LISTING_APPS.replaceAll('__WHERE__', gqlStringify(where)),
      'get_security_recommendation'
    );
    return response?.data?.data;
  },
  async listImageSecurityRecommendation(accountId: string, query: any) {
    if (accountId == 'demo') {
      const recommendationDemo = await getMockData('recommendations');
      return recommendationDemo.K8sSecurity.images;
    }
    const where: any = {};
    where.account_id = { _eq: accountId };

    if (query.namespace) {
      where.namespace = { _eq: query.namespace };
    }
    if (query.workload_name) {
      where.workload_name = { _eq: query.workload_name };
    }
    if (query.severity?.length) {
      where.severity = Array.isArray(query.severity) ? { _in: query.severity } : { _eq: query.severity };
    }
    if (query.status) {
      where.status = { _eq: query.status };
    }

    if (query.image) {
      where.image = { _ilike: `%${query.image}%` };
    }

    if (query.package_id) {
      where.package_id = { _eq: query.package_id };
    }

    const response = await queryGraphQL(
      GET_SECURITY_RECOMMENDATION_LISTING_IMAGES.replaceAll('__WHERE__', gqlStringify(where)),
      'get_security_recommendation'
    );
    return response?.data?.data;
  },
  async listCVESecurityRecommendation(accountId: string, query: any) {
    if (accountId == 'demo') {
      const recommendationDemo = await getMockData('recommendations');
      return recommendationDemo.K8sSecurity.CVE;
    }
    const where: any = {};
    where.account_id = { _eq: accountId };

    if (query.namespace) {
      where.namespace = { _eq: query.namespace };
    }
    if (query.workload_name) {
      where.workload_name = { _eq: query.workload_name };
    }
    if (query.severity?.length) {
      where.severity = Array.isArray(query.severity) ? { _in: query.severity } : { _eq: query.severity };
    }
    if (query.status) {
      where.status = { _eq: query.status };
    }

    const response = await queryGraphQL(
      GET_SECURITY_RECOMMENDATION_LISTING_CVE.replaceAll('__WHERE__', gqlStringify(where)),
      'get_security_recommendation'
    );
    return response?.data?.data;
  },
  async getSecuritySeverityGrouping(query: any) {
    if (query.accountId == 'demo') {
      const recommendationDemo = await getMockData('recommendations');
      return recommendationDemo.K8sSecurity.infographics;
    }
    const where: any = {};
    where.account_id = { _eq: query.accountId };

    if (query.status) {
      where.status = { _eq: query.status };
    }
    if (query.namespace) {
      where['namespace'] = { _eq: query.namespace };
    }
    if (query.workload) {
      where['workload_name'] = { _eq: query.workload };
    }
    if (query.severity?.length) {
      where['severity'] = Array.isArray(query.severity) ? { _in: query.severity } : { _eq: query.severity };
    }
    const response = await queryGraphQL(
      GET_SECURITY_SEVERITY_GROUPING.replaceAll('__WHERE__', gqlStringify(where)),
      'get_security_severity_groupings'
    );
    return response?.data?.data;
  },

  getRecommendationDetails(category: string, ruleName: string): any {
    const categoryData = (recommendationDetails as any)[category] || {};
    if (categoryData[ruleName]) {
      return categoryData[ruleName];
    }
    // Fallback: search across all categories in case the rule is categorized differently
    // between the collector and frontend definitions
    for (const cat of Object.keys(recommendationDetails)) {
      const data = (recommendationDetails as any)[cat];
      if (data[ruleName]) {
        return data[ruleName];
      }
    }
    return null;
  },
  async getK8sRecommendationAggregate({
    accountId,
    category,
    ruleName,
    severity,
    status = ['Open', 'Assigned'],
    resourceNamespace,
    resourceWorkloadType,
    accountObjectId,
    recommendation,
    resourceMeta,
    serviceName = '',
    resource_ids,
  }: {
    accountId?: string;
    category?: string;
    ruleName?: string | string[];
    severity?: string;
    status?: string[];
    resourceNamespace?: string;
    resourceWorkloadType?: string;
    accountObjectId?: string;
    recommendation?: any;
    resourceMeta?: any;
    orderBy?: string;
    orderAsc?: boolean;
    limit?: number;
    offset?: number;
    fetchTicket?: boolean;
    serviceName?: string;
    resource_ids?: string[];
  }) {
    try {
      if (accountId === 'demo') {
        return {
          data: {
            recommendation_aggregate: {
              aggregate: {
                count: 0,
              },
            },
          },
        };
      }
      const gqlQuery: any = {};
      if (accountId) {
        gqlQuery['account_id'] = { _eq: accountId };
      }
      if (accountObjectId) {
        gqlQuery['account_object_id'] = { _ilike: '%' + accountObjectId + '%' };
      }
      if (category) {
        gqlQuery['category'] = { _eq: category };
      }
      if (Array.isArray(ruleName)) {
        gqlQuery['rule_name'] = { _in: ruleName };
      } else {
        gqlQuery['rule_name'] = { _eq: ruleName };
      }
      if (severity) {
        gqlQuery['severity'] = { _eq: severity };
      }
      if (status && status.length > 0) {
        gqlQuery['status'] = { _in: status };
      }
      if (recommendation) {
        gqlQuery['recommendation'] = { _contains: JSON.stringify(recommendation) };
      }
      if (resourceMeta) {
        gqlQuery['resourse_meta'] = { _contains: JSON.stringify(resourceMeta) };
      }
      if (resourceNamespace) {
        gqlQuery['resource_k8s_namespace'] = { _eq: resourceNamespace };
      }

      if (resourceWorkloadType) {
        gqlQuery['resourse_type'] = { _eq: resourceWorkloadType };
      }
      if (serviceName) {
        gqlQuery['resource_cloud_service'] = { _eq: serviceName };
      }
      if (resource_ids) {
        gqlQuery['resource_id'] = { _in: resource_ids };
      }

      const query = LIST_k8_RECOMMENDATIONS_AGGREGATE.replaceAll('__WHERE__', gqlStringify(gqlQuery, []));

      const response = await queryGraphQL(query, 'ListK8sRecommendationAggregate', {});
      return {
        data: {
          recommendation_aggregate: {
            aggregate: {
              count: response?.data?.data?.recommendation_aggregate?.rows[0]?.count,
            },
          },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async optimizeSummaryInfographic(accountId: string) {
    if (accountId == 'demo') {
      const response = await getMockData('optimizeSummaryInfographics');
      return response;
    }
    const response = await splitAndParallelQuery(K8S_OPTIMIZE_SUMMARY_INFOGRAPHICS, 'K8sOptimizeSummaryInfographics', {
      accountId,
      startDate: getStartOfYear(new Date()),
      endDate: getEndOfYear(new Date()),
    });
    return {
      data: {
        workload_rightsize: {
          aggregate: {
            count: response?.data?.data?.workload_rightsize?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.workload_rightsize?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        replica_rightsize: {
          aggregate: {
            count: response?.data?.data?.replica_rightsize?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.replica_rightsize?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        pv_rightsize: {
          aggregate: {
            count: response?.data?.data?.pv_rightsize?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.pv_rightsize?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        abandoned_resource: {
          aggregate: {
            count: response?.data?.data?.abandoned_resource?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.abandoned_resource?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        spot_instance: {
          aggregate: {
            count: response?.data?.data?.spot_instance?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.spot_instance?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        unused_pvc: {
          aggregate: {
            count: response?.data?.data?.unused_pvc?.rows[0]?.count,
            sum: {
              estimated_savings: response?.data?.data?.unused_pvc?.rows[0]?.sum_estimated_savings,
            },
          },
        },
        spends_aggregate: { aggregate: { sum: { amount: response?.data?.data?.spends_aggregate?.rows?.[0]?.spend_amount } } },
        count_recommendations: response?.data?.data?.count_recommendations?.rows[0]?.count,
        count_optimize_recommendations: response?.data?.data?.count_optimize_recommendations?.rows[0]?.count,
      },
    };
  },

  async getIndividualRecommendationRuleTypeCount(accountId: string) {
    if (accountId == 'demo') {
      const response = await getMockData('recommendationTabCounts');
      return response;
    }
    const GET_INIDIVIDUAL_RECOMMENDATION_RULE_TYPE_COUNT = `
    query GetCountOfIndividualRecommendationType($accountId: String!) {
      recommendation_groupings_v2(where: {status: {_in: ["Open", "InProgress"]}, account_id: {_eq: $accountId}}, group_by: ["category", "rule_name"]) {
        rows {
          category
          count
          rule_name
        }
      }
    }
    `;
    const response = await queryGraphQL(GET_INIDIVIDUAL_RECOMMENDATION_RULE_TYPE_COUNT, 'GetCountOfIndividualRecommendationType', {
      accountId: accountId,
    });
    return response;
  },
  async getRecommendationResolution(data: any) {
    const GET_RECOMMENDATION_RESOLUTION = `
    query RecommendationResolution($where: RecommendationResolutionWhereRequest, $whereAgg: RecommendationResolutionGroupingsWhereRequest, $limit:Int, $offset:Int) {
      recommendation_resolution: recommendation_resolution_v2(where: $where, limit: $limit, offset: $offset, order_by: [{column: "updated_at", order: desc}]) {
        rows {
          id
          status
          status_message
          resolver_type
          resolver_display_name
          type_reference_id
          data
          rec_recommendation
          rec_rule_name
          rec_severity
          rec_estimated_savings
          rec_resource_name
          rec_resource_meta
          updated_at
          type
        }
      }
      recommendation_resolution_aggregate: recommendation_resolution_groupings_v2(where: $whereAgg) {
        rows {
          count
        }
      }
    }
    `;
    if (data.accountId === 'demo') {
      return {
        data: {
          data: {
            recommendation_resolution: [],
            recommendation_resolution_aggregate: { aggregate: { count: 0 } },
          },
        },
      };
    }
    const where: any = {};
    const whereAgg: any = {};
    if (data.accountId) {
      where.account_id = { _eq: data.accountId };
      whereAgg.account_id = { _eq: data.accountId };
    }
    if (data.status) {
      where.status = { _eq: data.status };
      whereAgg.status = { _eq: data.status };
    }
    if (data.type) {
      where.type = { _eq: data.type };
      whereAgg.type = { _eq: data.type };
    }
    if (data.resolverType) {
      where.resolver_type = { _eq: data.resolverType };
      whereAgg.resolver_type = { _eq: data.resolverType };
    }
    const response = await queryGraphQL(GET_RECOMMENDATION_RESOLUTION, 'RecommendationResolution', {
      where,
      whereAgg,
      limit: data.limit,
      offset: data.offset,
    });
    const rows = response?.data?.data?.recommendation_resolution?.rows || [];
    const aggRows = response?.data?.data?.recommendation_resolution_aggregate?.rows;
    return {
      data: {
        data: {
          recommendation_resolution: rows.map((r: any) => ({
            ...r,
            data: typeof r.data === 'string' ? safeJSONParse(r.data) : r.data,
            recommendation: {
              recommendation: r.rec_recommendation,
              rule_name: r.rec_rule_name,
              severity: r.rec_severity,
              estimated_savings: r.rec_estimated_savings,
              cloud_resourse: {
                name: r.rec_resource_name,
                meta: typeof r.rec_resource_meta === 'string' ? safeJSONParse(r.rec_resource_meta) : r.rec_resource_meta,
              },
            },
          })),
          recommendation_resolution_aggregate: { aggregate: { count: aggRows?.[0]?.count || 0 } },
        },
      },
    };
  },
  async getDistinctResolverTypes(filter = 'resolver_type') {
    const query = `
    query getDistinctResolverTypes($where: RecommendationResolutionGroupingsWhereRequest) {
      recommendation_resolution: recommendation_resolution_groupings_v2(where: $where, group_by: ["${filter}"]) {
        rows {
          ${filter}
        }
      }
    }
    `;

    const response = await queryGraphQL(query, 'getDistinctResolverTypes', { where: {} });
    const rows = response?.data?.data?.recommendation_resolution?.rows || [];
    return {
      data: {
        data: {
          recommendation_resolution: rows,
        },
      },
    };
  },
  async getDistinctRuleName() {
    let query = `
    query GetDistinctRuleNames {
      recommendation_groupings_v2(where: {category: {_in: ["RightSizing", "InfraUpgrade"]}}, order_by: {column: "rule_name", order: asc}) {
        rows {
          rule_name
        }
      }
    }
    `;

    const response = await queryGraphQL(query, 'GetDistinctRuleNames');
    return response;
  },
  async exportRecommendations(params: any) {
    const mutation = `
    mutation ExportRecommendations($request: ExportRecommendationRequest!) {
      recommendation_export(request: $request) {
        file_data
        filename
        content_type
        record_count
      }
    }
    `;
    if (params.accountId === 'demo') return null;

    const variables = {
      request: {
        account_id: params.accountId,
        category: params.category,
        rule_name: params.ruleName,
        namespace: params.namespace || null,
        workload_type: params.workloadType || null,
        workload_name: params.workloadName || null,
        status: params.status || null,
        format: params.format || 'xlsx',
      },
    };

    const response = await queryGraphQL(mutation, 'ExportRecommendations', variables);
    return response;
  },
};

export default apiRecommendations;
