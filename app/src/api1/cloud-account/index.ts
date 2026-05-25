import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { getEndOfDay, getEndOfMonth, getEndOfYear, getLast7Days, getStartOfMonth, getStartOfYear } from '@lib/datetime';

const GET_DISTINCT_REGIONS = `
query GetDistinctRegions($where: CloudResourceGroupingsWhereRequest) {
  cloud_resource_groupings_v2(where: $where, column_transformations: [{expr: "distinct", name: "region"}]) {
    rows {
      region
    }
  }
}
`;

export const LIST_CLOUD_RESOURCE = `
query ListCloudResources ($limit:Int, $offset:Int)  {
    cloud_resourses: cloud_resources_list_v2(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "created_at", order: desc}]) {
      rows {
        region
        resourse_id
        service_name
        status
        name
        id
        meta
        account
        tags
        created_at
        resourse_created_on
        type
        spend_amount
        latest_metric
        latest_metric_value
        latest_metric_timestamp
        total_count
      }
    }
}
`;

export const CLOUD_APPLY_COMMAND = `
mutation CloudApplyCommand(
  $account_id: String!
  $service_name: String!
  $region: String!
  $resource_id: String!
  $command: String!
  $args: jsonb
) {
  cloud_apply_command(
    account_id: $account_id
    service_name: $service_name
    region: $region
    resource_id: $resource_id
    command: $command
    args: $args
  ) {
    success
    message
  }
}
`;

export const LIST_RESOURCE_ACTION_HISTORY = `
query ListResourceActionHistory($limit: Int, $offset: Int) {
  audit_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
  audits_v2(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "event_time", order: desc}]) {
    rows {
      user_id
      account_id
      event_time
      event_category
      event_type
      event_status
      event_target
      event_action
      event_prev_state
      event_state
      event_attr
    }
  }
}
`;

export const LIST_CLOUD_ISSUES = `
 query list_cloud_issues_data($limit:Int, $offset:Int) {
  events_aggregate: event_groupings_v2(where: __WHERE__) {
    rows{
      count: event_count
    }
  }
  events: events_v2(where: __WHERE__, order_by: [{column: "starts_at", order: desc}], limit: $limit, offset: $offset) {
    rows{
      account_id
      subject_type
      subject_name
      subject_namespace
      starts_at
      title
      finding_type
      cluster
      evidences
      status
      aggregation_key
      priority
      id
      resource_id
      fingerprint
      principal
      source
      nb_status
      snoozed_until
      nb_status_changed_at
      nb_status_changed_by
      computed_priority
      computed_score
      score_factors
      score_confidence
    }
  }
}`;

// Same as LIST_CLOUD_ISSUES minus the heavy `evidences` and `score_factors` JSONB
// columns. Used by Summary widgets that only render top-line metadata — the
// dropped fields can each be tens of KB per row and dominate response size for
// tenants with verbose detectors.
export const LIST_CLOUD_ISSUES_LIGHT = `
 query list_cloud_issues_data_light($limit:Int, $offset:Int) {
  events_aggregate: event_groupings_v2(where: __WHERE__) {
    rows{
      count: event_count
    }
  }
  events: events_v2(where: __WHERE__, order_by: [{column: "starts_at", order: desc}], limit: $limit, offset: $offset) {
    rows{
      account_id
      subject_type
      subject_name
      subject_namespace
      starts_at
      title
      finding_type
      cluster
      status
      aggregation_key
      priority
      id
      resource_id
      fingerprint
      principal
      source
      nb_status
      snoozed_until
      nb_status_changed_at
      nb_status_changed_by
      computed_priority
      computed_score
      score_confidence
    }
  }
}`;

export const LIST_CLOUD_RESOURCE_METRIC = `
query ListCloudResourcesMetrics {
  cloud_metric_groupings_v2(where: __WHERE__) {
    rows {
      avg_value
      metric
      region_name
      resource_id
      service_name
      timestamp
    }
  }
}
`;

// Direct database query for cloud_resource_metrics - much faster than the
// cloud_metric_groupings_v2 action which calls cloud provider APIs in real-time
export const GET_INSTANCE_TYPE_SPECS = `
query GetInstanceTypeSpecs {
  cloud_resource_details_v2(where: __WHERE__) {
    rows {
      resource_type
      resource_capacity
      resource_cost
      database_engine
      service_type
    }
  }
}
`;

export const LIST_CLOUD_RESOURCE_METRIC_DIRECT = `
query ListCloudResourceMetricsDirect($where: CloudResourceMetricsWhereRequest, $limit: Int) {
  cloud_resource_metrics_v2(where: $where, order_by: [{column: "timestamp", order: asc}], limit: $limit) {
    rows {
      metric
      value
      timestamp
      cloud_resource_id
      resource_name
      resource_id
    }
  }
}
`;

// Lightweight query for fetching only the latest metric values (no nested relations, small time window)
export const LATEST_RESOURCE_METRICS_LIGHT = `
query LatestResourceMetricsLight($where: CloudResourceMetricsWhereRequest, $limit: Int) {
  cloud_resource_metrics_v2(where: $where, order_by: [{column: "timestamp", order: desc}], limit: $limit) {
    rows {
      metric
      value
      timestamp
      cloud_resource_id
    }
  }
}
`;

export const CLOUD_ACC_COST_TREND = `
query cloudAccountCostTrend($dateUnit:String!){
  spend_groupings: spend_groupings_v2(where: __WHERE__, order_by: [{column: "spend_date", order: asc}], column_transformations:[{name: "spend_date", expr: "date_unit", args: [$dateUnit]}]){
    rows{
      tenant_id
      account_id
      spend_date
      spend_amount  
      currency_type
    }
  }
}
`;

export const CLOUD_RESOURCE_COST_TREND = `
query cloudResourceCostTrend($dateUnit:String!) {
  spend_trend: resource_spend_trend_v2(where: __WHERE__, order_by: [{column: "spend_date", order: asc}], column_transformations:[{name: "spend_date", expr: "date_unit", args: [$dateUnit]}]) {
    rows {
      spend_date
      spend_amount
      currency_type
    }
  }
}
`;

export const CLOUD_ACC_SUMMARY = `
query CloudAccountSummary(
  $recWhere: RecommendationGroupingWhereRequest,
  $spendsWhere: SpendGroupingsWhereRequest,
  $grossSpendsWhere: SpendGroupingsWhereRequest,
  $creditsWhere: SpendGroupingsWhereRequest,
  $lmSpendsWhere: SpendGroupingsWhereRequest,
  $lmGrossSpendsWhere: SpendGroupingsWhereRequest,
  $lmCreditsWhere: SpendGroupingsWhereRequest,
  $yearlySpendsWhere: SpendGroupingsWhereRequest,
  $yearlyGrossSpendsWhere: SpendGroupingsWhereRequest,
  $eventsWhere: EventGroupingsWhereRequest,
  $ec2Where: CloudResourceGroupingsWhereRequest,
  $rdsWhere: CloudResourceGroupingsWhereRequest,
  $s3Where: CloudResourceGroupingsWhereRequest
) {
  recommendation_aggregate: recommendation_groupings_v2(where: $recWhere) {
    rows {
      count
      sum_estimated_savings
    }
  }
  spends_aggregate: spend_groupings_v2(where: $spendsWhere) {
    rows { spend_amount }
  }
  gross_spends_aggregate: spend_groupings_v2(where: $grossSpendsWhere) {
    rows { spend_amount }
  }
  credits_aggregate: spend_groupings_v2(where: $creditsWhere) {
    rows { spend_amount }
  }
  lm_spends_aggregate: spend_groupings_v2(where: $lmSpendsWhere) {
    rows { spend_amount }
  }
  lm_gross_spends_aggregate: spend_groupings_v2(where: $lmGrossSpendsWhere) {
    rows { spend_amount }
  }
  lm_credits_aggregate: spend_groupings_v2(where: $lmCreditsWhere) {
    rows { spend_amount }
  }
  yearly_spends_aggregate: spend_groupings_v2(where: $yearlySpendsWhere) {
    rows { spend_amount }
  }
  yearly_gross_spends_aggregate: spend_groupings_v2(where: $yearlyGrossSpendsWhere) {
    rows { spend_amount }
  }
  events_aggregate: event_groupings_v2(where: $eventsWhere) {
    rows { event_count }
  }
  ec2_count: cloud_resource_groupings_v2(where: $ec2Where) {
    rows { count }
  }
  rds_count: cloud_resource_groupings_v2(where: $rdsWhere) {
    rows { count }
  }
  s3_count: cloud_resource_groupings_v2(where: $s3Where) {
    rows { count }
  }
}
`;

export const CLOUD_ACC_EC2_SUMMARY = `
query CloudAccEC2Summary(
  $resourcesWhere: CloudResourcesWhereRequest,
  $resourcesCountWhere: CloudResourceGroupingsWhereRequest,
  $ebsWhere: CloudResourceGroupingsWhereRequest,
  $nicsWhere: CloudResourceGroupingsWhereRequest,
  $recWhere: RecommendationGroupingWhereRequest,
  $eventsWhere: EventGroupingsWhereRequest,
  $spendsWhere: SpendGroupingsWhereRequest,
  $grossSpendsWhere: SpendGroupingsWhereRequest,
  $creditsWhere: SpendGroupingsWhereRequest,
  $lmSpendsWhere: SpendGroupingsWhereRequest,
  $lmGrossSpendsWhere: SpendGroupingsWhereRequest,
  $lmCreditsWhere: SpendGroupingsWhereRequest,
  $yearlySpendsWhere: SpendGroupingsWhereRequest,
  $yearlyGrossSpendsWhere: SpendGroupingsWhereRequest
) {
  cloud_resourses: cloud_resource_v2(where: $resourcesWhere) {
    rows { service_name meta name status }
  }
  cloud_resourses_count: cloud_resource_groupings_v2(where: $resourcesCountWhere) {
    rows { count }
  }
  ebs_count: cloud_resource_groupings_v2(where: $ebsWhere) {
    rows { count }
  }
  nics_count: cloud_resource_groupings_v2(where: $nicsWhere) {
    rows { count }
  }
  recommendation_aggregate: recommendation_groupings_v2(where: $recWhere) {
    rows { count sum_estimated_savings }
  }
  events_aggregate: event_groupings_v2(where: $eventsWhere) {
    rows { event_count }
  }
  spends_aggregate: spend_groupings_v2(where: $spendsWhere) {
    rows { spend_amount }
  }
  gross_spends_aggregate: spend_groupings_v2(where: $grossSpendsWhere) {
    rows { spend_amount }
  }
  credits_aggregate: spend_groupings_v2(where: $creditsWhere) {
    rows { spend_amount }
  }
  lm_spends_aggregate: spend_groupings_v2(where: $lmSpendsWhere) {
    rows { spend_amount }
  }
  lm_gross_spends_aggregate: spend_groupings_v2(where: $lmGrossSpendsWhere) {
    rows { spend_amount }
  }
  lm_credits_aggregate: spend_groupings_v2(where: $lmCreditsWhere) {
    rows { spend_amount }
  }
  yearly_spends_aggregate: spend_groupings_v2(where: $yearlySpendsWhere) {
    rows { spend_amount }
  }
  yearly_gross_spends_aggregate: spend_groupings_v2(where: $yearlyGrossSpendsWhere) {
    rows { spend_amount }
  }
}
`;

export const CLOUD_ACC_RDS_SUMMARY = `
query CloudAccRDSSummary(
  $resourcesWhere: CloudResourcesWhereRequest,
  $recWhere: RecommendationGroupingWhereRequest,
  $eventsWhere: EventGroupingsWhereRequest,
  $spendsWhere: SpendGroupingsWhereRequest,
  $grossSpendsWhere: SpendGroupingsWhereRequest,
  $creditsWhere: SpendGroupingsWhereRequest,
  $lmSpendsWhere: SpendGroupingsWhereRequest,
  $lmGrossSpendsWhere: SpendGroupingsWhereRequest,
  $lmCreditsWhere: SpendGroupingsWhereRequest,
  $yearlySpendsWhere: SpendGroupingsWhereRequest,
  $yearlyGrossSpendsWhere: SpendGroupingsWhereRequest
) {
  cloud_resourses: cloud_resource_v2(where: $resourcesWhere) {
    rows { service_name meta name }
  }
  recommendation_aggregate: recommendation_groupings_v2(where: $recWhere) {
    rows { count sum_estimated_savings }
  }
  events_aggregate: event_groupings_v2(where: $eventsWhere) {
    rows { event_count }
  }
  spends_aggregate: spend_groupings_v2(where: $spendsWhere) {
    rows { spend_amount }
  }
  gross_spends_aggregate: spend_groupings_v2(where: $grossSpendsWhere) {
    rows { spend_amount }
  }
  credits_aggregate: spend_groupings_v2(where: $creditsWhere) {
    rows { spend_amount }
  }
  lm_spends_aggregate: spend_groupings_v2(where: $lmSpendsWhere) {
    rows { spend_amount }
  }
  lm_gross_spends_aggregate: spend_groupings_v2(where: $lmGrossSpendsWhere) {
    rows { spend_amount }
  }
  lm_credits_aggregate: spend_groupings_v2(where: $lmCreditsWhere) {
    rows { spend_amount }
  }
  yearly_spends_aggregate: spend_groupings_v2(where: $yearlySpendsWhere) {
    rows { spend_amount }
  }
  yearly_gross_spends_aggregate: spend_groupings_v2(where: $yearlyGrossSpendsWhere) {
    rows { spend_amount }
  }
}
`;

export const CLOUD_ACC_S3_SUMMARY = `
query CloudAccS3Summary(
  $s3Where: CloudResourceGroupingsWhereRequest,
  $recWhere: RecommendationGroupingWhereRequest,
  $spendsWhere: SpendGroupingsWhereRequest,
  $grossSpendsWhere: SpendGroupingsWhereRequest,
  $creditsWhere: SpendGroupingsWhereRequest,
  $lmSpendsWhere: SpendGroupingsWhereRequest,
  $lmGrossSpendsWhere: SpendGroupingsWhereRequest,
  $lmCreditsWhere: SpendGroupingsWhereRequest,
  $yearlySpendsWhere: SpendGroupingsWhereRequest,
  $yearlyGrossSpendsWhere: SpendGroupingsWhereRequest
) {
  s3_count: cloud_resource_groupings_v2(where: $s3Where) {
    rows { count }
  }
  recommendation_aggregate: recommendation_groupings_v2(where: $recWhere) {
    rows { count sum_estimated_savings }
  }
  spends_aggregate: spend_groupings_v2(where: $spendsWhere) {
    rows { spend_amount }
  }
  gross_spends_aggregate: spend_groupings_v2(where: $grossSpendsWhere) {
    rows { spend_amount }
  }
  credits_aggregate: spend_groupings_v2(where: $creditsWhere) {
    rows { spend_amount }
  }
  lm_spends_aggregate: spend_groupings_v2(where: $lmSpendsWhere) {
    rows { spend_amount }
  }
  lm_gross_spends_aggregate: spend_groupings_v2(where: $lmGrossSpendsWhere) {
    rows { spend_amount }
  }
  lm_credits_aggregate: spend_groupings_v2(where: $lmCreditsWhere) {
    rows { spend_amount }
  }
  yearly_spends_aggregate: spend_groupings_v2(where: $yearlySpendsWhere) {
    rows { spend_amount }
  }
  yearly_gross_spends_aggregate: spend_groupings_v2(where: $yearlyGrossSpendsWhere) {
    rows { spend_amount }
  }
}
`;

export const CLOUD_ACC_ECS_SUMMARY = `
query CloudAccECSSummary(
  $resourcesWhere: CloudResourcesWhereRequest,
  $recWhere: RecommendationGroupingWhereRequest,
  $eventsWhere: EventGroupingsWhereRequest,
  $spendsWhere: SpendGroupingsWhereRequest,
  $grossSpendsWhere: SpendGroupingsWhereRequest,
  $creditsWhere: SpendGroupingsWhereRequest,
  $lmSpendsWhere: SpendGroupingsWhereRequest,
  $lmGrossSpendsWhere: SpendGroupingsWhereRequest,
  $lmCreditsWhere: SpendGroupingsWhereRequest,
  $yearlySpendsWhere: SpendGroupingsWhereRequest,
  $yearlyGrossSpendsWhere: SpendGroupingsWhereRequest
) {
  cloud_resourses: cloud_resource_v2(where: $resourcesWhere) {
    rows { service_name meta name type status }
  }
  recommendation_aggregate: recommendation_groupings_v2(where: $recWhere) {
    rows { count sum_estimated_savings }
  }
  events_aggregate: event_groupings_v2(where: $eventsWhere) {
    rows { event_count }
  }
  spends_aggregate: spend_groupings_v2(where: $spendsWhere) {
    rows { spend_amount }
  }
  gross_spends_aggregate: spend_groupings_v2(where: $grossSpendsWhere) {
    rows { spend_amount }
  }
  credits_aggregate: spend_groupings_v2(where: $creditsWhere) {
    rows { spend_amount }
  }
  lm_spends_aggregate: spend_groupings_v2(where: $lmSpendsWhere) {
    rows { spend_amount }
  }
  lm_gross_spends_aggregate: spend_groupings_v2(where: $lmGrossSpendsWhere) {
    rows { spend_amount }
  }
  lm_credits_aggregate: spend_groupings_v2(where: $lmCreditsWhere) {
    rows { spend_amount }
  }
  yearly_spends_aggregate: spend_groupings_v2(where: $yearlySpendsWhere) {
    rows { spend_amount }
  }
  yearly_gross_spends_aggregate: spend_groupings_v2(where: $yearlyGrossSpendsWhere) {
    rows { spend_amount }
  }
}
`;

const GET_TAG_KEYS = `
query GetTagKeys($where: CloudResourcesWhereRequest) {
  cloud_resource_v2(where: $where, limit: 500) {
    rows {
      tags
    }
  }
}
`;

function applyTypeFilter(where: any, type: string | string[] | undefined) {
  if (Array.isArray(type)) {
    where.type = { _in: type };
  } else if (type) {
    where.type = { _eq: type };
  }
}

function buildServiceSpendWhere(
  accountId: string,
  serviceName: string,
  s: string | Date,
  e: string | Date,
  amountFilter?: any,
  excludeAggregate = false
) {
  const conditions: any[] = [{ spend_date: { _gte: s } }, { spend_date: { _lte: e } }, { exclude_aggregate: { _eq: excludeAggregate } }];
  if (amountFilter) conditions.push(amountFilter);
  return {
    account_id: { _eq: accountId },
    resource_service_name: { _eq: serviceName },
    _and: conditions,
  };
}

// Lookup maps for multi-cloud service name mappings (EC2/VM summary)
const EC2_RESOURCE_TYPE_MAP: Record<string, string[]> = {
  AmazonEC2: ['compute-instance'],
  'Compute Engine': ['compute.googleapis.com/Instance'],
  'Cloud SQL': ['sqladmin.googleapis.com/Instance'],
};
const EC2_DEFAULT_RESOURCE_TYPE = ['virtualmachines'];

const EC2_EBS_SERVICE_MAP: Record<string, string> = {
  'microsoft.compute/virtualmachines': 'microsoft.compute/disks',
  'Compute Engine': 'compute.googleapis.com/Disk',
};

const EC2_NICS_SERVICE_MAP: Record<string, string> = {
  AmazonEC2: 'AmazonVPC',
  'microsoft.compute/virtualmachines': 'microsoft.network/networkinterfaces',
  'Compute Engine': 'compute.googleapis.com/NetworkInterface',
};

const EC2_NICS_TYPE_MAP: Record<string, string> = {
  'microsoft.compute/virtualmachines': 'networkinterfaces',
  'Compute Engine': 'compute.googleapis.com/NetworkInterface',
};
const EC2_DEFAULT_NICS_TYPE = 'network-interface';

const EC2_EVENT_SOURCE_MAP: Record<string, string> = {
  AmazonEC2: 'AWS_CloudWatch_Alarm',
  'microsoft.compute/virtualmachines': 'azure_monitor_webhook',
};

function buildEC2EventsWhere(accountId: string, startDate: string, endDate: string, serviceName: string) {
  const eventsWhere: any = {
    account_id: { _eq: accountId },
    _and: [{ created_at: { _gte: startDate } }, { created_at: { _lte: endDate } }],
  };
  const source = EC2_EVENT_SOURCE_MAP[serviceName];
  if (source) {
    eventsWhere.source = { _eq: source };
  }
  if (serviceName) {
    eventsWhere.subject_namespace = { _eq: serviceName };
  }
  return eventsWhere;
}

// Action error responses come back wrapped under
//   errors[0].extensions.internal.response.body
// where `body` mirrors the handler's HTTP body — for /rpc/cloud that's
// `[{message: "..."}]` (the api-server's common.Error shape) or the bare
// cloud-collector envelope `{data:null, errors:[{message: ...}]}`. This
// helper digs through both shapes so the UI surfaces the actual cause
// instead of a generic "Unknown error".
export function extractGraphQLErrorMessage(response: any): string {
  const errors = response?.data?.errors;
  if (!Array.isArray(errors) || errors.length === 0) {
    return 'Unknown error';
  }
  const top = errors[0] || {};
  const body = top?.extensions?.internal?.response?.body;
  if (Array.isArray(body) && body[0]?.message) {
    return body[0].message;
  }
  if (body && typeof body === 'object' && Array.isArray(body.errors) && body.errors[0]?.message) {
    return body.errors[0].message;
  }
  if (body && typeof body === 'object' && typeof body.message === 'string' && body.message) {
    return body.message;
  }
  return top.message || 'Unknown error';
}

const apiCloudAccount = {
  getDistinctTagKeys: async function (
    accountId: string,
    serviceName?: string,
    type?: string | string[]
  ): Promise<{ label: string; value: string }[]> {
    try {
      if (accountId === 'demo') {
        return [];
      }
      const where: any = {
        account: { _eq: accountId },
        status: { _eq: 'Active' },
        tags: { _is_null: false },
      };
      if (serviceName) {
        where.service_name = { _eq: serviceName };
      }
      applyTypeFilter(where, type);
      const response = await queryGraphQL(GET_TAG_KEYS, 'GetTagKeys', { where });
      const resources = response?.data?.data?.cloud_resource_v2?.rows || [];
      const keySet = new Set<string>();
      resources.forEach((r: any) => {
        const tags = typeof r.tags === 'string' ? JSON.parse(r.tags) : r.tags;
        if (tags && typeof tags === 'object') {
          Object.keys(tags).forEach((k) => {
            if (!k.startsWith('nb_')) {
              keySet.add(k);
            }
          });
        }
      });
      return Array.from(keySet)
        .sort((a, b) => a.localeCompare(b))
        .map((k) => ({ label: k, value: k }));
    } catch (error) {
      console.log('failed to fetch tag keys-', error);
      return [];
    }
  },
  getDistinctTagValues: async function (
    accountId: string,
    tagKey: string,
    serviceName?: string,
    type?: string | string[]
  ): Promise<{ label: string; value: string }[]> {
    try {
      if (accountId === 'demo') {
        return [];
      }
      const where: any = {
        account: { _eq: accountId },
        status: { _eq: 'Active' },
        tags: { _has_key: tagKey },
      };
      if (serviceName) {
        where.service_name = { _eq: serviceName };
      }
      applyTypeFilter(where, type);
      const response = await queryGraphQL(GET_TAG_KEYS, 'GetTagKeys', { where });
      const resources = response?.data?.data?.cloud_resource_v2?.rows || [];
      const valueSet = new Set<string>();
      resources.forEach((r: any) => {
        const tags = typeof r.tags === 'string' ? JSON.parse(r.tags) : r.tags;
        const values = tags?.[tagKey];
        if (Array.isArray(values)) {
          values.forEach((v: string) => valueSet.add(v));
        } else if (typeof values === 'string') {
          valueSet.add(values);
        }
      });
      return Array.from(valueSet)
        .sort((a, b) => a.localeCompare(b))
        .map((v) => ({ label: v, value: v }));
    } catch (error) {
      console.log('failed to fetch tag values-', error);
      return [];
    }
  },
  getDistinctRegions: async function (accountId: string, serviceName?: string): Promise<{ label: string; value: string }[]> {
    try {
      if (accountId === 'demo') {
        return [];
      }
      const where: any = { account_id: { _eq: accountId } };
      if (serviceName) {
        where.service_name = { _eq: serviceName };
      }
      const response = await queryGraphQL(GET_DISTINCT_REGIONS, 'GetDistinctRegions', { where });
      const rows: { region: string }[] = response?.data?.data?.cloud_resource_groupings_v2?.rows || [];
      return rows
        .map((r) => r.region)
        .filter((region): region is string => Boolean(region))
        .sort((a, b) => a.localeCompare(b))
        .map((region) => ({ label: region, value: region }));
    } catch (error) {
      console.log('failed to fetch distinct regions-', error);
      return [];
    }
  },
  getCloudResource: async function (data: any, limit?: number, offset?: number) {
    try {
      if (data.account_id === 'demo') {
        return {
          data: {
            data: {
              cloud_resourses: [],
              cloud_resourses_aggregate: { aggregate: { count: 0 } },
            },
          },
        };
      }
      const where: any = {};
      where.account = { _eq: data.account_id };
      if (data.serviceName) {
        where.service_name = { _eq: data.serviceName };
      }
      applyTypeFilter(where, data.type);
      if (Array.isArray(data.status)) {
        where.status = { _in: data.status };
      } else {
        where.status = { _eq: data.status || 'Active' };
      }
      // Filter by native cloud-provider state via meta JSON (e.g. running/stopped/deallocated).
      // Caller supplies a pre-built JSON string (see buildStateFilter in stateFilter.ts).
      if (data.metaStateFilter) {
        where.meta = { _contains: data.metaStateFilter };
      }
      if (data.metric) {
        where.metric = Array.isArray(data.metric) ? { _in: data.metric } : { _eq: data.metric };
      }
      if (data.region) {
        where.region = { _eq: data.region };
      }
      const andConditions: any[] = [];
      if (data.nameFilter) {
        andConditions.push({
          _or: [{ resourse_id: { _ilike: '%' + data.nameFilter + '%' } }, { name: { _ilike: '%' + data.nameFilter + '%' } }],
        });
      }
      // Handle tag filtering with JSON _contains (values serialized as JSON strings)
      if (data.tagFilterKey && data.tagFilterValue) {
        andConditions.push({
          _or: [
            { tags: { _contains: JSON.stringify({ [data.tagFilterKey]: [data.tagFilterValue] }) } },
            { tags: { _contains: JSON.stringify({ [data.tagFilterKey]: data.tagFilterValue }) } },
          ],
        });
      } else if (data.tagFilterKey) {
        andConditions.push({ tags: { _has_key: data.tagFilterKey } });
      }
      if (andConditions.length > 0) {
        where._and = andConditions;
      }
      const query = LIST_CLOUD_RESOURCE.replaceAll('__WHERE__', gqlStringify(where));
      const variables: any = { limit, offset };
      const response = await queryGraphQL(query, 'ListCloudResources', variables);

      // Map response for backward compatibility with consumers
      const rows = response?.data?.data?.cloud_resourses?.rows || [];
      const totalCount = rows[0]?.total_count || 0;
      const safeParse = (val: any) => {
        if (typeof val === 'string') {
          try {
            return JSON.parse(val);
          } catch {
            return val;
          }
        }
        return val;
      };
      const mappedResources = rows.map((item: any) => ({
        ...item,
        meta: safeParse(item.meta),
        tags: safeParse(item.tags),
        cloud_resource_metrics: item.latest_metric
          ? [{ metric: item.latest_metric, value: item.latest_metric_value, timestamp: item.latest_metric_timestamp }]
          : [],
        spends_aggregate: { aggregate: { sum: { amount: item.spend_amount || 0 } } },
      }));

      return {
        data: {
          data: {
            cloud_resourses: mappedResources,
            cloud_resourses_aggregate: { aggregate: { count: totalCount } },
          },
        },
      };
    } catch (error) {
      console.log('failed to fetch cloud resource-', error);
      throw error;
    }
  },
  listEvents: async function (
    query: {
      accountId: string | string[];
      subjectNamespace?: string | string[];
      subjectType?: string | string[];
      subjectName?: string | string[];
      cluster?: string | string[];
      title?: string | string[];
      findingType?: string | string[];
      aggregationKey?: string | string[];
      priority?: string | string[];
      status?: string | string[];
      resourceId?: string | string[];
      principal?: string | string[];
      source?: string | string[];
      nbStatus?: string | string[];
      startDate?: Date;
      endDate?: Date;
    },
    limit?: number,
    offset?: number,
    options?: { light?: boolean }
  ) {
    try {
      const filterParams: any = {};
      if (query?.accountId) {
        if (Array.isArray(query.accountId)) {
          filterParams['account_id'] = { _in: query.accountId };
        } else {
          filterParams['account_id'] = { _eq: query.accountId };
        }
      }
      if (query?.source) {
        if (Array.isArray(query.source)) {
          filterParams['source'] = { _in: query.source };
        } else {
          filterParams['source'] = { _eq: query.source };
        }
      }
      if (query?.subjectNamespace) {
        if (Array.isArray(query.subjectNamespace)) {
          filterParams['subject_namespace'] = { _in: query.subjectNamespace };
        } else {
          filterParams['subject_namespace'] = { _eq: query.subjectNamespace };
        }
      }
      if (query?.subjectType) {
        if (Array.isArray(query.subjectType)) {
          filterParams['subject_type'] = { _in: query.subjectType };
        } else {
          filterParams['subject_type'] = { _eq: query.subjectType };
        }
      }
      if (query?.subjectName) {
        if (Array.isArray(query.subjectName)) {
          filterParams['subject_name'] = { _in: query.subjectName };
        } else {
          filterParams['subject_name'] = { _eq: query.subjectName };
        }
      }
      if (query?.cluster) {
        if (Array.isArray(query.cluster)) {
          filterParams['cluster'] = { _in: query.cluster };
        } else {
          filterParams['cluster'] = { _eq: query.cluster };
        }
      }
      if (query?.title) {
        if (Array.isArray(query.title)) {
          filterParams['title'] = { _in: query.title };
        } else {
          filterParams['title'] = { _eq: query.title };
        }
      }
      if (query?.findingType) {
        if (Array.isArray(query.findingType)) {
          filterParams['finding_type'] = { _in: query.findingType };
        } else {
          filterParams['finding_type'] = { _eq: query.findingType };
        }
      }
      if (query?.aggregationKey) {
        if (Array.isArray(query.aggregationKey)) {
          filterParams['aggregation_key'] = { _in: query.aggregationKey };
        } else {
          filterParams['aggregation_key'] = { _eq: query.aggregationKey };
        }
      }
      if (query?.priority) {
        if (Array.isArray(query.priority)) {
          filterParams['priority'] = { _in: query.priority.map((g) => g.toUpperCase()) };
        } else {
          filterParams['priority'] = { _eq: query.priority.toUpperCase() };
        }
      }
      if (query?.status) {
        if (Array.isArray(query.status)) {
          filterParams['status'] = { _in: query.status };
        } else {
          filterParams['status'] = { _eq: query.status };
        }
      }
      if (query?.resourceId) {
        if (Array.isArray(query.resourceId)) {
          filterParams['resource_id'] = { _in: query.resourceId };
        } else {
          filterParams['resource_id'] = { _eq: query.resourceId };
        }
      }

      if (query?.principal) {
        if (Array.isArray(query.principal)) {
          filterParams['principal'] = { _in: query.principal };
        } else {
          filterParams['principal'] = { _eq: query.principal };
        }
      }

      if (query?.nbStatus) {
        if (Array.isArray(query.nbStatus)) {
          filterParams['nb_status'] = { _in: query.nbStatus };
        } else {
          filterParams['nb_status'] = { _eq: query.nbStatus };
        }
      }

      const endDate = query.endDate || query.endDate || getEndOfMonth(new Date());
      const startDate = query.startDate || query.startDate || getStartOfMonth(new Date());

      filterParams['_and'] = [{ starts_at: { _gte: startDate.toISOString() } }, { starts_at: { _lte: endDate.toISOString() } }];

      const useLight = options?.light === true;
      let queryStr = useLight ? LIST_CLOUD_ISSUES_LIGHT : LIST_CLOUD_ISSUES;
      const operationName = useLight ? 'list_cloud_issues_data_light' : 'list_cloud_issues_data';

      queryStr = queryStr.replaceAll('__WHERE__', gqlStringify(filterParams));
      const response = await queryGraphQL(queryStr, operationName, {
        limit: limit,
        offset: offset,
      });
      return {
        data: {
          events: response?.data?.data?.events?.rows,
          events_aggregate: {
            aggregate: {
              count: response?.data?.data?.events_aggregate?.rows?.[0]?.count,
            },
          },
        },
      };
    } catch (error) {
      console.log('failed to fetch cloud resource recommendation-', error);
      throw error;
    }
  },
  getCloudResourceMetrics: async function (data: any) {
    try {
      if (data.account_id === 'demo') return null;
      const where: any = {};
      where.account_id = { _eq: data.account_id };
      if (data.serviceName) {
        where.service_name = { _eq: data.serviceName };
      }
      if (data.resourceIds && Array.isArray(data.resourceIds) && data.resourceIds.length > 0) {
        where.resource_id = { _in: data.resourceIds };
      } else if (data.resourceId) {
        where.resource_id = { _eq: data.resourceId };
      }
      if (data.resourceType) {
        where.resource_type = { _eq: data.resourceType };
      }
      if (data.startDate && data.endDate) {
        where['timestamp'] = {
          _gte: data.startDate.toISOString(),
          _lte: data.endDate.toISOString(),
        };
      }
      const response = await queryGraphQL(LIST_CLOUD_RESOURCE_METRIC.replaceAll('__WHERE__', gqlStringify(where)), 'ListCloudResourcesMetrics', {});
      return response;
    } catch (error) {
      console.log('failed to fetch cloud resource metric-', error);
      throw error;
    }
  },
  // Fast database query for cloud metrics - reads from cloud_resource_metrics table via v2 action
  // instead of calling cloud provider APIs (Azure Monitor/CloudWatch) in real-time.
  // Use this for Summary pages where pre-collected metrics are sufficient.
  getCloudResourceMetricsDirect: async function (data: {
    account_id: string;
    serviceName?: string;
    resourceId?: string;
    startDate?: Date;
    endDate?: Date;
  }) {
    try {
      if (data.account_id === 'demo') return null;
      const where: any = {};
      where.cloud_account_id = { _eq: data.account_id };
      if (data.serviceName) {
        where.service_name = { _eq: data.serviceName };
      }
      if (data.resourceId) {
        where.resource_id = { _eq: data.resourceId };
      }
      if (data.startDate && data.endDate) {
        where.timestamp = {
          _gte: data.startDate.toISOString(),
          _lte: data.endDate.toISOString(),
        };
      }
      const response = await queryGraphQL(LIST_CLOUD_RESOURCE_METRIC_DIRECT, 'ListCloudResourceMetricsDirect', {
        where,
        limit: 5000,
      });
      // Transform response to match the format expected by components
      // (same shape as cloud_metric_groupings_v2 response)
      const rawMetrics = response?.data?.data?.cloud_resource_metrics_v2?.rows || [];
      const transformedRows = rawMetrics.map((m: any) => ({
        avg_value: m.value,
        metric: m.metric,
        timestamp: m.timestamp,
        resource_id: m.cloud_resource_id,
        resource_name: m.resource_name || m.resource_id || m.cloud_resource_id,
        region_name: '',
        service_name: data.serviceName || '',
      }));
      // Return in the same shape as the cloud_metric_groupings_v2 action response
      return {
        ...response,
        data: {
          ...response?.data,
          data: {
            ...response?.data?.data,
            cloud_metric_groupings_v2: {
              rows: transformedRows,
            },
          },
        },
      };
    } catch (error) {
      console.log('failed to fetch cloud resource metric (direct)-', error);
      throw error;
    }
  },
  /**
   * Fetch the latest metric value per resource per metric type.
   * Uses the existing cloud_resource_metrics query with a 24-hour window,
   * then picks the latest value per resource per metric client-side.
   * Returns a map: { [cloud_resource_id]: { [metric_name]: { value, timestamp } } }
   */
  getLatestMetricsForResources: async function (data: {
    account_id: string;
    resourceIds: string[];
    metrics: string[];
  }): Promise<Record<string, Record<string, { value: number; timestamp: string }>>> {
    try {
      if (data.account_id === 'demo') return {};
      const now = new Date();
      // Use a 2-hour window instead of 24h to keep the query lightweight
      const twoHoursAgo = new Date(now.getTime() - 2 * 60 * 60 * 1000);
      const where: any = {
        cloud_account_id: { _eq: data.account_id },
        cloud_resource_id: { _in: data.resourceIds },
        metric: { _in: data.metrics },
        timestamp: {
          _gte: twoHoursAgo.toISOString(),
          _lte: now.toISOString(),
        },
      };
      const response = await queryGraphQL(LATEST_RESOURCE_METRICS_LIGHT, 'LatestResourceMetricsLight', {
        where,
        limit: 500,
      });
      const rows = response?.data?.data?.cloud_resource_metrics_v2?.rows || [];
      // Pick the latest value per resource per metric (rows ordered desc, so first match wins)
      const result: Record<string, Record<string, { value: number; timestamp: string }>> = {};
      rows.forEach((row: any) => {
        if (!result[row.cloud_resource_id]) {
          result[row.cloud_resource_id] = {};
        }
        // Since ordered desc, first entry per resource+metric is the latest — only store if not yet set
        if (!result[row.cloud_resource_id][row.metric]) {
          result[row.cloud_resource_id][row.metric] = {
            value: row.value,
            timestamp: row.timestamp,
          };
        }
      });
      return result;
    } catch (error) {
      console.log('failed to fetch latest resource metrics-', error);
      return {};
    }
  },
  /**
   * Fetch live CPU/memory metrics directly from cloud provider APIs.
   * Uses 5-min window + 300s step for a single data point per metric (instant query pattern).
   * Returns a map: { [cloud_resource_id]: { cpu: { value, timestamp }, memory: { value, timestamp } } }
   */
  getLiveMetricsForResources: async function (data: {
    account_id: string;
    service_name: string;
    resources: { resourse_id: string; region: string; meta?: any }[];
  }): Promise<Record<string, Record<string, { value: number; timestamp: string }>>> {
    try {
      if (!data.resources.length || data.account_id === 'demo') {
        return {};
      }

      const CPU_NAMES = ['cpu/utilization', 'CPUUtilization', 'Percentage CPU'];
      const MEMORY_NAMES = ['memory/utilization', 'MemoryUtilization', 'Available Memory Bytes'];

      // Build a lookup for total memory (MiB) per resource for Azure bytes→% conversion
      const totalMemoryByResource: Record<string, number> = {};
      for (const r of data.resources) {
        const sizeMiB = r.meta?.InstanceTypeDetails?.MemoryInfo?.SizeInMiB;
        if (sizeMiB && r.resourse_id) {
          totalMemoryByResource[r.resourse_id] = sizeMiB;
        }
      }

      const sn = data.service_name.toLowerCase();
      let metricNames: string[] = [];
      if (sn.includes('amazon') || sn.includes('ec2')) {
        metricNames = ['CPUUtilization'];
      } else if (sn.includes('compute engine')) {
        metricNames = ['cpu/utilization', 'memory/utilization'];
      } else if (sn.includes('virtualmachine')) {
        metricNames = ['Percentage CPU', 'Available Memory Bytes'];
      }

      // Group resources by region for parallel calls
      const byRegion: Record<string, string[]> = {};
      for (const r of data.resources) {
        if (!r.resourse_id || !r.region) {
          continue;
        }
        if (!byRegion[r.region]) {
          byRegion[r.region] = [];
        }
        byRegion[r.region].push(r.resourse_id);
      }

      const now = new Date();
      // Use 10-min window to account for monitoring data propagation delays
      const windowStart = new Date(now.getTime() - 10 * 60 * 1000);

      const CLOUD_METRICS_MUTATION = `
mutation CloudMetrics($request: CloudMetricsRequestInput!) {
  cloud_metrics(request: $request) {
    items {
      name
      resource_id
      values
      timestamps
    }
  }
}`;

      // Azure only supports specific time grains: PT1M, PT5M, PT15M, PT30M, PT1H, etc.
      // Use PT5M (300s) for Azure, PT10M (600s) for others.
      const isAzure = sn.includes('virtualmachine') || sn.includes('azure');
      const stepNs = isAzure ? 300000000000 : 600000000000;

      const regionPromises = Object.entries(byRegion).map(([region, resourceIds]) =>
        queryGraphQL(CLOUD_METRICS_MUTATION, 'CloudMetrics', {
          request: {
            account_id: data.account_id,
            query: {
              service_name: data.service_name,
              region,
              resource_ids: resourceIds,
              metric_names: metricNames.length > 0 ? metricNames : undefined,
              start_date: windowStart.toISOString(),
              end_date: now.toISOString(),
              step: stepNs,
            },
          },
        })
      );

      const responses = await Promise.all(regionPromises);
      const result: Record<string, Record<string, { value: number; timestamp: string }>> = {};

      for (const resp of responses) {
        const items = resp?.data?.data?.cloud_metrics?.items || [];
        for (const item of items) {
          if (!item.resource_id || !item.values?.length) {
            continue;
          }
          if (!result[item.resource_id]) {
            result[item.resource_id] = {};
          }
          // Take last value (most recent data point)
          const value = item.values[item.values.length - 1];
          const timestamp = item.timestamps?.[item.timestamps.length - 1] || '';

          let key = '';
          let finalValue = value;
          if (CPU_NAMES.includes(item.name)) {
            key = 'cpu';
          } else if (MEMORY_NAMES.includes(item.name)) {
            key = 'memory';
            // Azure returns 'Available Memory Bytes' (raw bytes, not %).
            // Convert to utilization % using total memory from instance metadata.
            if (item.name === 'Available Memory Bytes') {
              const totalMiB = totalMemoryByResource[item.resource_id];
              if (totalMiB) {
                const totalBytes = totalMiB * 1024 * 1024;
                finalValue = ((totalBytes - value) / totalBytes) * 100;
              } else {
                // Can't convert without total memory — skip this metric
                continue;
              }
            }
          } else {
            continue;
          }
          result[item.resource_id][key] = { value: finalValue, timestamp };
        }
      }

      return result;
    } catch (error) {
      console.log('failed to fetch live metrics-', error);
      return {};
    }
  },
  /**
   * Fetch instance type specs (memory, vCPU, cost) from cloud_resource_details.
   * Returns a map: { [resource_type]: { cpu_virtual, memory_gb, resource_cost } }
   */
  getInstanceTypeSpecs: async function (data: {
    resourceTypes: string[];
    cloudProvider?: string;
    serviceType?: string;
  }): Promise<Record<string, { cpu_virtual: string; memory_gb: string; resource_cost: number }>> {
    try {
      // Also try without 'db.' prefix for RDS instance types (they share EC2 specs)
      const allTypes = [...data.resourceTypes];
      const dbPrefixMap: Record<string, string> = {};
      data.resourceTypes.forEach((t) => {
        if (t.startsWith('db.')) {
          const stripped = t.replace('db.', '');
          allTypes.push(stripped);
          dbPrefixMap[stripped] = t;
        }
      });
      const where: any = {
        resource_type: { _in: allTypes },
      };
      if (data.cloudProvider) {
        where.cloud_provider = { _eq: data.cloudProvider };
      }
      if (data.serviceType) {
        where.service_type = { _eq: data.serviceType };
      }
      const response = await queryGraphQL(GET_INSTANCE_TYPE_SPECS.replaceAll('__WHERE__', gqlStringify(where)), 'GetInstanceTypeSpecs', {});
      const rows = response?.data?.data?.cloud_resource_details_v2?.rows || [];
      const result: Record<string, { cpu_virtual: string; memory_gb: string; resource_cost: number }> = {};
      rows.forEach((row: any) => {
        const capacity = typeof row.resource_capacity === 'string' ? JSON.parse(row.resource_capacity) : row.resource_capacity;
        const specs = {
          cpu_virtual: capacity?.cpu_virtual || '',
          memory_gb: capacity?.memory_gb || '',
          resource_cost: row.resource_cost || 0,
        };
        // Store under original resource_type
        if (!result[row.resource_type]) {
          result[row.resource_type] = specs;
        }
        // Also map stripped EC2 type back to original db. prefixed type
        const originalDbType = dbPrefixMap[row.resource_type];
        if (originalDbType && !result[originalDbType]) {
          result[originalDbType] = specs;
        }
      });
      return result;
    } catch (error) {
      console.log('failed to fetch instance type specs-', error);
      return {};
    }
  },
  listCloudAccountTrend: async function (
    query: { accountId: string; resourceServiceName?: string; resourceId?: string },
    start_date: Date | null = null,
    end_date: Date | null = null,
    dateUnit = 'day'
  ) {
    try {
      if (query.accountId === 'demo') {
        return {
          data: {
            spend_groupings: [],
          },
        };
      }
      start_date = start_date || getLast7Days();
      end_date = end_date || getEndOfDay();

      // If resourceId is provided, use resource_spend_trend_v2 action for resource-level costs
      if (query?.resourceId) {
        const filterParams: any = {
          account_id: { _eq: query.accountId },
          resource_external_id: { _eq: query.resourceId },
          exclude_aggregate: { _eq: false },
          spend_date: {
            _between: {
              _gte: start_date.toISOString(),
              _lte: end_date.toISOString(),
            },
          },
        };

        const finalQuery = CLOUD_RESOURCE_COST_TREND.replaceAll('__WHERE__', gqlStringify(filterParams));
        const response = await queryGraphQL(finalQuery, 'cloudResourceCostTrend', {
          dateUnit: dateUnit,
        });

        return {
          data: {
            spend_groupings: response?.data?.data?.spend_trend?.rows || [],
          },
        };
      }

      // Otherwise use aggregated view for service-level costs
      const filterParams: any = {};
      if (query?.accountId) {
        filterParams['account_id'] = { _eq: query.accountId };
      }
      if (query?.resourceServiceName) {
        filterParams['resource_service_name'] = { _eq: query.resourceServiceName };
      }

      filterParams['exclude_aggregate'] = { _eq: false };
      filterParams['spend_date'] = {
        _between: {
          _gte: start_date.toISOString(),
          _lte: end_date.toISOString(),
        },
      };

      const finalQuery = CLOUD_ACC_COST_TREND.replaceAll('__WHERE__', gqlStringify(filterParams));
      const response = await queryGraphQL(finalQuery, 'cloudAccountCostTrend', {
        dateUnit: dateUnit,
      });
      return {
        data: {
          spend_groupings: response?.data?.data?.spend_groupings?.rows || [],
        },
      };
    } catch (error) {
      console.log('failed to fetch cost of cloud account trend- ', error);
      return error;
    }
  },
  cloudAccountSummary: async function (accountId: string, data: any = null) {
    if (accountId === 'demo') return null;
    try {
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);
      const startDate = data?.start_date || getLast7Days().toISOString();
      const endDate = data?.end_date || getEndOfDay().toISOString();

      const cmStart = getStartOfMonth(currentDate);
      const cmEnd = getEndOfMonth(currentDate);
      const lmStart = getStartOfMonth(lastMonthStart);
      const lmEnd = getEndOfMonth(lastMonthStart);
      const yrStart = getStartOfYear(currentDate);
      const yrEnd = getEndOfYear(currentDate);

      const buildSpendWhere = (s: string | Date, e: string | Date, amountFilter?: any, excludeAggregate = false) => {
        const conditions: any[] = [{ spend_date: { _gte: s } }, { spend_date: { _lte: e } }, { exclude_aggregate: { _eq: excludeAggregate } }];
        if (amountFilter) conditions.push(amountFilter);
        return { account_id: { _eq: accountId }, _and: conditions };
      };

      const eventsWhere: any = {
        account_id: { _eq: accountId },
        source: { _in: ['AWS_CloudWatch_Alarm', 'azure_monitor_webhook', 'gcp_monitoring_alert'] },
        _and: [{ created_at: { _gte: startDate } }, { created_at: { _lte: endDate } }],
      };
      if (data?.serviceName) {
        eventsWhere.subject_name = { _eq: data.serviceName };
      }

      const response = await queryGraphQL(CLOUD_ACC_SUMMARY, 'CloudAccountSummary', {
        recWhere: { account_id: { _eq: accountId }, status: { _in: ['Open', 'Assigned'] } },
        spendsWhere: buildSpendWhere(cmStart, cmEnd),
        grossSpendsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _gte: 0 } }),
        creditsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _lt: 0 } }, true),
        lmSpendsWhere: buildSpendWhere(lmStart, lmEnd),
        lmGrossSpendsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _gte: 0 } }),
        lmCreditsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _lt: 0 } }, true),
        yearlySpendsWhere: buildSpendWhere(yrStart, yrEnd),
        yearlyGrossSpendsWhere: buildSpendWhere(yrStart, yrEnd, { amount: { _gte: 0 } }),
        eventsWhere,
        ec2Where: {
          account_id: { _eq: accountId },
          service_name: { _eq: 'AmazonEC2' },
          status: { _eq: 'Active' },
          meta: { _contains: JSON.stringify({ State: { Name: 'running' } }) },
        },
        rdsWhere: { account_id: { _eq: accountId }, service_name: { _eq: 'AmazonRDS' }, status: { _eq: 'Active' } },
        s3Where: { account_id: { _eq: accountId }, service_name: { _eq: 'AmazonS3' }, type: { _eq: 'storage' }, status: { _eq: 'Active' } },
      });

      const d = response?.data?.data;
      const spendAgg = (key: string) => ({ aggregate: { sum: { amount: d?.[key]?.rows?.[0]?.spend_amount || 0 } } });
      const countAgg = (key: string) => ({ aggregate: { count: d?.[key]?.rows?.[0]?.count || 0 } });

      return {
        recommendation_aggregate: {
          aggregate: {
            count: d?.recommendation_aggregate?.rows?.[0]?.count || 0,
            sum: { estimated_savings: d?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings || 0 },
          },
        },
        spends_aggregate: spendAgg('spends_aggregate'),
        gross_spends_aggregate: spendAgg('gross_spends_aggregate'),
        credits_aggregate: spendAgg('credits_aggregate'),
        lm_spends_aggregate: spendAgg('lm_spends_aggregate'),
        lm_gross_spends_aggregate: spendAgg('lm_gross_spends_aggregate'),
        lm_credits_aggregate: spendAgg('lm_credits_aggregate'),
        yearly_spends_aggregate: spendAgg('yearly_spends_aggregate'),
        yearly_gross_spends_aggregate: spendAgg('yearly_gross_spends_aggregate'),
        events_aggregate: { aggregate: { count: d?.events_aggregate?.rows?.[0]?.event_count || 0 } },
        ec2_count: countAgg('ec2_count'),
        rds_count: countAgg('rds_count'),
        s3_count: countAgg('s3_count'),
      };
    } catch (error) {
      console.log('failed to fetch cost of cloud account trend- ', error);
      return error;
    }
  },
  cloudAccountEC2Summary: async function (accountId: string, data: any = null) {
    if (accountId === 'demo') return null;
    try {
      const startDate = data?.start_date || getLast7Days().toISOString();
      const endDate = data?.end_date || getEndOfDay().toISOString();
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);

      const cmStart = getStartOfMonth(currentDate);
      const cmEnd = getEndOfMonth(currentDate);
      const lmStart = getStartOfMonth(lastMonthStart);
      const lmEnd = getEndOfMonth(lastMonthStart);
      const yrStart = getStartOfYear(currentDate);
      const yrEnd = getEndOfYear(currentDate);

      const svc = data.serviceName;
      const resourceType = EC2_RESOURCE_TYPE_MAP[svc] || EC2_DEFAULT_RESOURCE_TYPE;
      const eventsWhere = buildEC2EventsWhere(accountId, startDate, endDate, svc);

      const recWhere: any = { account_id: { _eq: accountId }, status: { _in: ['Open', 'Assigned'] } };
      if (svc) {
        recWhere.resource_cloud_service = { _eq: svc };
      }

      const buildSpendWhere = (s: string | Date, e: string | Date, amountFilter?: any, excludeAggregate = false) =>
        buildServiceSpendWhere(accountId, svc, s, e, amountFilter, excludeAggregate);

      const ebsServiceName = EC2_EBS_SERVICE_MAP[svc] || svc;
      const ebsType = svc === 'microsoft.compute/virtualmachines' ? 'disks' : 'storage';
      const nicsServiceName = EC2_NICS_SERVICE_MAP[svc] || svc;
      const nicsType = EC2_NICS_TYPE_MAP[svc] || EC2_DEFAULT_NICS_TYPE;

      const response = await queryGraphQL(CLOUD_ACC_EC2_SUMMARY, 'CloudAccEC2Summary', {
        resourcesWhere: {
          account: { _eq: accountId },
          service_name: { _eq: svc },
          type: { _in: resourceType },
          status: { _in: ['Active', 'Inactive'] },
        },
        resourcesCountWhere: {
          account_id: { _eq: accountId },
          service_name: { _eq: svc },
          type: { _in: resourceType },
          status: { _in: ['Active', 'Inactive'] },
        },
        ebsWhere: {
          account_id: { _eq: accountId },
          service_name: { _eq: ebsServiceName },
          status: { _eq: 'Active' },
          type: { _eq: ebsType },
        },
        nicsWhere: {
          account_id: { _eq: accountId },
          service_name: { _eq: nicsServiceName },
          status: { _eq: 'Active' },
          type: { _eq: nicsType },
        },
        recWhere,
        eventsWhere,
        spendsWhere: buildSpendWhere(cmStart, cmEnd),
        grossSpendsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _gte: 0 } }),
        creditsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _lt: 0 } }, true),
        lmSpendsWhere: buildSpendWhere(lmStart, lmEnd),
        lmGrossSpendsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _gte: 0 } }),
        lmCreditsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _lt: 0 } }, true),
        yearlySpendsWhere: buildSpendWhere(yrStart, yrEnd),
        yearlyGrossSpendsWhere: buildSpendWhere(yrStart, yrEnd, { amount: { _gte: 0 } }),
      });

      const d = response?.data?.data;
      const rows = d?.cloud_resourses?.rows || [];
      const spendAgg = (key: string) => ({ aggregate: { sum: { amount: d?.[key]?.rows?.[0]?.spend_amount || 0 } } });

      return {
        cloud_resourses: rows.map((r: any) => ({ ...r, meta: typeof r.meta === 'string' ? JSON.parse(r.meta) : r.meta })),
        cloud_resourses_count: d?.cloud_resourses_count?.rows?.[0]?.count || 0,
        ebs_count: { aggregate: { count: d?.ebs_count?.rows?.[0]?.count || 0 } },
        nics_count: { aggregate: { count: d?.nics_count?.rows?.[0]?.count || 0 } },
        recommendation_aggregate: {
          aggregate: {
            count: d?.recommendation_aggregate?.rows?.[0]?.count,
            sum: { estimated_savings: d?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings },
          },
        },
        events_aggregate: { aggregate: { count: d?.events_aggregate?.rows?.[0]?.event_count || 0 } },
        spends_aggregate: spendAgg('spends_aggregate'),
        gross_spends_aggregate: spendAgg('gross_spends_aggregate'),
        credits_aggregate: spendAgg('credits_aggregate'),
        lm_spends_aggregate: spendAgg('lm_spends_aggregate'),
        lm_gross_spends_aggregate: spendAgg('lm_gross_spends_aggregate'),
        lm_credits_aggregate: spendAgg('lm_credits_aggregate'),
        yearly_spends_aggregate: spendAgg('yearly_spends_aggregate'),
        yearly_gross_spends_aggregate: spendAgg('yearly_gross_spends_aggregate'),
      };
    } catch (error) {
      console.log('failed to fetch ec2 summary-', error);
      return error;
    }
  },
  cloudAccountRDSSummary: async function (accountId: string, data: any = null) {
    if (accountId === 'demo') return null;
    try {
      const startDate = data?.start_date || getLast7Days().toISOString();
      const endDate = data?.end_date || getEndOfDay().toISOString();
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);

      const cmStart = getStartOfMonth(currentDate);
      const cmEnd = getEndOfMonth(currentDate);
      const lmStart = getStartOfMonth(lastMonthStart);
      const lmEnd = getEndOfMonth(lastMonthStart);
      const yrStart = getStartOfYear(currentDate);
      const yrEnd = getEndOfYear(currentDate);

      const eventsWhere: any = {
        account_id: { _eq: accountId },
        _and: [{ created_at: { _gte: startDate } }, { created_at: { _lte: endDate } }],
      };
      if (data.serviceName == 'AmazonRDS') {
        eventsWhere.source = { _eq: 'AWS_CloudWatch_Alarm' };
      } else if (data.serviceName == 'microsoft.sql/servers' || data.serviceName == 'microsoft.sql/managedinstances') {
        eventsWhere.source = { _eq: 'azure_monitor_webhook' };
      }
      if (data?.serviceName) {
        eventsWhere.subject_namespace = { _eq: data.serviceName };
      }

      const recWhere: any = { account_id: { _eq: accountId }, status: { _in: ['Open', 'Assigned'] } };
      if (data?.serviceName) {
        recWhere.resource_cloud_service = { _eq: data.serviceName };
      }

      const buildSpendWhere = (s: string | Date, e: string | Date, amountFilter?: any, excludeAggregate = false) =>
        buildServiceSpendWhere(accountId, data.serviceName, s, e, amountFilter, excludeAggregate);

      const response = await queryGraphQL(CLOUD_ACC_RDS_SUMMARY, 'CloudAccRDSSummary', {
        resourcesWhere: { account: { _eq: accountId }, service_name: { _eq: data.serviceName }, status: { _eq: 'Active' } },
        recWhere,
        eventsWhere,
        spendsWhere: buildSpendWhere(cmStart, cmEnd),
        grossSpendsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _gte: 0 } }),
        creditsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _lt: 0 } }, true),
        lmSpendsWhere: buildSpendWhere(lmStart, lmEnd),
        lmGrossSpendsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _gte: 0 } }),
        lmCreditsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _lt: 0 } }, true),
        yearlySpendsWhere: buildSpendWhere(yrStart, yrEnd),
        yearlyGrossSpendsWhere: buildSpendWhere(yrStart, yrEnd, { amount: { _gte: 0 } }),
      });

      const d = response?.data?.data;
      const rows = d?.cloud_resourses?.rows || [];
      const spendAgg = (key: string) => ({ aggregate: { sum: { amount: d?.[key]?.rows?.[0]?.spend_amount || 0 } } });

      return {
        cloud_resourses: rows.map((r: any) => ({ ...r, meta: typeof r.meta === 'string' ? JSON.parse(r.meta) : r.meta })),
        recommendation_aggregate: {
          aggregate: {
            count: d?.recommendation_aggregate?.rows?.[0]?.count,
            sum: { estimated_savings: d?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings },
          },
        },
        events_aggregate: { aggregate: { count: d?.events_aggregate?.rows?.[0]?.event_count || 0 } },
        spends_aggregate: spendAgg('spends_aggregate'),
        gross_spends_aggregate: spendAgg('gross_spends_aggregate'),
        credits_aggregate: spendAgg('credits_aggregate'),
        lm_spends_aggregate: spendAgg('lm_spends_aggregate'),
        lm_gross_spends_aggregate: spendAgg('lm_gross_spends_aggregate'),
        lm_credits_aggregate: spendAgg('lm_credits_aggregate'),
        yearly_spends_aggregate: spendAgg('yearly_spends_aggregate'),
        yearly_gross_spends_aggregate: spendAgg('yearly_gross_spends_aggregate'),
      };
    } catch (error) {
      console.log('failed to fetch rds summary-', error);
      return error;
    }
  },
  cloudAccountS3Summary: async function (accountId: string, data: any = null) {
    if (accountId === 'demo') return null;
    try {
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);

      const cmStart = getStartOfMonth(currentDate);
      const cmEnd = getEndOfMonth(currentDate);
      const lmStart = getStartOfMonth(lastMonthStart);
      const lmEnd = getEndOfMonth(lastMonthStart);
      const yrStart = getStartOfYear(currentDate);
      const yrEnd = getEndOfYear(currentDate);

      const buildSpendWhere = (s: string | Date, e: string | Date, amountFilter?: any, excludeAggregate = false) =>
        buildServiceSpendWhere(accountId, data.serviceName, s, e, amountFilter, excludeAggregate);

      const response = await queryGraphQL(CLOUD_ACC_S3_SUMMARY, 'CloudAccS3Summary', {
        s3Where: {
          account_id: { _eq: accountId },
          service_name: { _eq: data.serviceName },
          status: { _eq: 'Active' },
          type: { _eq: data.storageType },
        },
        recWhere: { account_id: { _eq: accountId }, resource_cloud_service: { _eq: data.serviceName }, status: { _in: ['Open', 'Assigned'] } },
        spendsWhere: buildSpendWhere(cmStart, cmEnd),
        grossSpendsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _gte: 0 } }),
        creditsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _lt: 0 } }, true),
        lmSpendsWhere: buildSpendWhere(lmStart, lmEnd),
        lmGrossSpendsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _gte: 0 } }),
        lmCreditsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _lt: 0 } }, true),
        yearlySpendsWhere: buildSpendWhere(yrStart, yrEnd),
        yearlyGrossSpendsWhere: buildSpendWhere(yrStart, yrEnd, { amount: { _gte: 0 } }),
      });

      const d = response?.data?.data;
      const spendAgg = (key: string) => ({ aggregate: { sum: { amount: d?.[key]?.rows?.[0]?.spend_amount || 0 } } });

      return {
        s3_count: { aggregate: { count: d?.s3_count?.rows?.[0]?.count || 0 } },
        recommendation_aggregate: {
          aggregate: {
            count: d?.recommendation_aggregate?.rows?.[0]?.count,
            sum: { estimated_savings: d?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings },
          },
        },
        spends_aggregate: spendAgg('spends_aggregate'),
        gross_spends_aggregate: spendAgg('gross_spends_aggregate'),
        credits_aggregate: spendAgg('credits_aggregate'),
        lm_spends_aggregate: spendAgg('lm_spends_aggregate'),
        lm_gross_spends_aggregate: spendAgg('lm_gross_spends_aggregate'),
        lm_credits_aggregate: spendAgg('lm_credits_aggregate'),
        yearly_spends_aggregate: spendAgg('yearly_spends_aggregate'),
        yearly_gross_spends_aggregate: spendAgg('yearly_gross_spends_aggregate'),
      };
    } catch (error) {
      console.log('failed to fetch s3 summary-', error);
      return error;
    }
  },
  cloudAccountECSSummary: async function (accountId: string, data: any = null) {
    if (accountId === 'demo') return null;
    try {
      data = data || {};
      data.serviceName = 'AmazonECS';

      const startDate = data?.start_date || getLast7Days().toISOString();
      const endDate = data?.end_date || getEndOfDay().toISOString();
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);

      const cmStart = getStartOfMonth(currentDate);
      const cmEnd = getEndOfMonth(currentDate);
      const lmStart = getStartOfMonth(lastMonthStart);
      const lmEnd = getEndOfMonth(lastMonthStart);
      const yrStart = getStartOfYear(currentDate);
      const yrEnd = getEndOfYear(currentDate);

      const eventsWhere: any = {
        account_id: { _eq: accountId },
        source: { _eq: 'AWS_CloudWatch_Alarm' },
        subject_namespace: { _eq: data.serviceName },
        _and: [{ created_at: { _gte: startDate } }, { created_at: { _lte: endDate } }],
      };

      const recWhere: any = {
        account_id: { _eq: accountId },
        resource_cloud_service: { _eq: data.serviceName },
        status: { _in: ['Open', 'Assigned'] },
      };
      if (data?.accountObjectId) {
        recWhere.account_object_id = { _ilike: 'arn:aws:' + data.accountObjectId + '%' };
      }

      const buildSpendWhere = (s: string | Date, e: string | Date, amountFilter?: any, excludeAggregate = false) =>
        buildServiceSpendWhere(accountId, 'AmazonECS', s, e, amountFilter, excludeAggregate);

      const response = await queryGraphQL(CLOUD_ACC_ECS_SUMMARY, 'CloudAccECSSummary', {
        resourcesWhere: {
          account: { _eq: accountId },
          service_name: { _eq: 'AmazonECS' },
          status: { _eq: 'Active' },
          meta: { _contains: JSON.stringify({}) },
        },
        recWhere,
        eventsWhere,
        spendsWhere: buildSpendWhere(cmStart, cmEnd),
        grossSpendsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _gte: 0 } }),
        creditsWhere: buildSpendWhere(cmStart, cmEnd, { amount: { _lt: 0 } }, true),
        lmSpendsWhere: buildSpendWhere(lmStart, lmEnd),
        lmGrossSpendsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _gte: 0 } }),
        lmCreditsWhere: buildSpendWhere(lmStart, lmEnd, { amount: { _lt: 0 } }, true),
        yearlySpendsWhere: buildSpendWhere(yrStart, yrEnd),
        yearlyGrossSpendsWhere: buildSpendWhere(yrStart, yrEnd, { amount: { _gte: 0 } }),
      });

      const d = response?.data?.data;
      const rows = d?.cloud_resourses?.rows || [];
      const spendAgg = (key: string) => ({ aggregate: { sum: { amount: d?.[key]?.rows?.[0]?.spend_amount || 0 } } });

      return {
        cloud_resourses: rows.map((r: any) => ({ ...r, meta: typeof r.meta === 'string' ? JSON.parse(r.meta) : r.meta })),
        recommendation_aggregate: {
          aggregate: {
            count: d?.recommendation_aggregate?.rows?.[0]?.count,
            sum: { estimated_savings: d?.recommendation_aggregate?.rows?.[0]?.sum_estimated_savings },
          },
        },
        events_aggregate: { aggregate: { count: d?.events_aggregate?.rows?.[0]?.event_count || 0 } },
        spends_aggregate: spendAgg('spends_aggregate'),
        gross_spends_aggregate: spendAgg('gross_spends_aggregate'),
        credits_aggregate: spendAgg('credits_aggregate'),
        lm_spends_aggregate: spendAgg('lm_spends_aggregate'),
        lm_gross_spends_aggregate: spendAgg('lm_gross_spends_aggregate'),
        lm_credits_aggregate: spendAgg('lm_credits_aggregate'),
        yearly_spends_aggregate: spendAgg('yearly_spends_aggregate'),
        yearly_gross_spends_aggregate: spendAgg('yearly_gross_spends_aggregate'),
      };
    } catch (error) {
      console.log('failed to fetch ecs summary-', error);
      return error;
    }
  },
  applyCommand: async function (params: {
    account_id: string;
    service_name: string;
    region: string;
    resource_id: string;
    command: string;
    args?: Record<string, any>;
  }): Promise<{ success: boolean; message: string }> {
    try {
      if (params.account_id === 'demo') {
        return { success: false, message: 'Demo account does not have access to execute commands.' };
      }
      const response = await queryGraphQL(CLOUD_APPLY_COMMAND, 'CloudApplyCommand', params);
      const data = response?.data?.data?.cloud_apply_command;
      if (data) {
        return data;
      }
      // Hasura action returned an error envelope. The api-server now folds
      // provider-side failures into success=false (see api-server/services/
      // cloud/service.go parseApplyCommandResponse), so this branch only
      // catches true transport / parse failures. Surface whatever detail the
      // backend gave us instead of "Unknown error".
      return { success: false, message: extractGraphQLErrorMessage(response) };
    } catch (error: any) {
      console.error('failed to apply cloud command-', error);
      return { success: false, message: error?.message || 'Network error' };
    }
  },
  listResourceActionHistory: async function (
    accountId: string,
    resourceId: string,
    limit = 10,
    offset = 0
  ): Promise<{ audits: any[]; count: number }> {
    try {
      if (accountId === 'demo') {
        return { audits: [], count: 0 };
      }
      const where: any = {
        account_id: { _eq: accountId },
        event_type: { _eq: 'RESOURCE_ACTION' },
        event_target: { _eq: resourceId },
      };
      const queryStr = LIST_RESOURCE_ACTION_HISTORY.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(queryStr, 'ListResourceActionHistory', { limit, offset });
      return {
        audits: response?.data?.data?.audits_v2?.rows || [],
        count: response?.data?.data?.audit_groupings_v2?.rows?.[0]?.count || 0,
      };
    } catch (error) {
      console.error('failed to fetch resource action history-', error);
      return { audits: [], count: 0 };
    }
  },
};

export default apiCloudAccount;
