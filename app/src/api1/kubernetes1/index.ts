import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import getMockData from '@api1/mock';
import cache from '@lib/cache';
import observability from '@api1/observability';
import { safeJSONParse } from 'src/utils/common';

export const GET_EVENT_RULES = `
query GetEventRules($limit: Int, $offset: Int) {
  event_rules_v2(where: __WHERE__, order_by: [{column: "updated_at", order: desc}], limit: $limit, offset: $offset) {
    rows {
      category
      alert
      annotations
      created_at
      duration
      enabled
      expr
      id
      is_editable
      labels
      severity
      source
      group
      name
      namespace
    }
  }
  event_rules_aggregate: event_rules_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
}
`;

export const GET_ALL_EVENT_RULE_NAMES = `
query GetAllEventRuleNames {
  event_rules_v2(where: __WHERE__) {
    rows {
      alert
    }
  }
}
`;

export const CREATE_ALERT_MANAGER = `
mutation CreateAlertManager {
  create_alert_manager_rule: alertmanager_create_rule(request: __REQUEST__) {
    response
  }
}
`;

export const UPDATE_ALERT_MANAGER = `
mutation UpdateAlertManagerRule {
  update_alert_manager_rule: alertmanager_update_rule(request: __REQUEST__) {
    response
  }
}
`;

export const GET_DISTINCT_DATA = `
query GetDistinctCategorySourceSeverity {
  distinct_category: event_rules_groupings_v2(columns: ["category"], column_transformations: [{expr: "distinct", name: "category"}], where: __WHERE__) {
    rows {
      category
    }
  }
  distinct_source: event_rules_groupings_v2(columns: ["source"], column_transformations: [{expr: "distinct", name: "source"}], where: __WHERE__) {
    rows {
      source
    }
  }
  distinct_severity: event_rules_groupings_v2(columns: ["severity"], column_transformations: [{expr: "distinct", name: "severity"}], where: __WHERE__) {
    rows {
      severity
    }
  }
}
`;

export const DISABLE_ALERT_MANAGER = `
mutation DisableAlertManagerRule {
  disable_alert_manager_rule: alertmanager_disable_rule(request: __REQUEST__) {
    response
  }
}
`;

export const LIST_ACC = `
query ListAcc($limit: Int, $offset: Int) {
  get_cloud_accounts_v2(where: __WHERE__, order_by: [{column: "created_at", order: desc}] limit: $limit, offset: $offset) {
    rows {
      account_name
      account_number
      id
      created_at
      created_by
      created_by_name
      status
      data
      cloud_account_attrs
      agents
    }
  }
  get_cloud_accounts_grouping_v2(where: __WHERE__){
    rows{
      count
    }
  }
}
`;

export const LIST_AWS_ACC = `
query ListAWSAcc {
  cloud_accounts: get_cloud_accounts_v2(where: {cloud_provider: {_eq: "AWS"}}) {
    rows {
      account_name
      id
      created_at
      created_by
      created_by_name
      status
      account_number
    }
    status
    account_number
    account_access
  }
}
`;

export const LIST_GCP_ACC = `
query ListGCPAcc {
  cloud_accounts: get_cloud_accounts_v2(where: {cloud_provider: {_eq: "GCP"}}) {
    rows {
      account_name
      id
      created_at
      created_by
      created_by_name
      account_number
      status
      cloud_account_attrs
    }
  }
}
`;

export const LIST_K8s_ACC_AGENT = `
query AccountAgentHealth {
  agent: get_agent_health_v2(where: __WHERE__) {
    rows {
      cloud_account_id
      last_connected_at
      k8s_version
      version
    }
  }
}
`;

export const SLO_CREATE = `
mutation SLOCreate($data: SLOCreateRequest!) {
  slo_config_create(request: $data) {
    data {
      success
    }
  }
}
`;

export const SLO_LIST = `
mutation SLOConfigList($data: SLOListRequest!) {
  slo_config_list(request: $data) {
    data {
      namespace
      workload_name
      cloud_account_id
      config {
        enabled
        goal
        id
        name
        threshold
      }
    }
  }
}
`;

export const SLO_UPDATE = `
mutation SLOUpdate($data: SLOUpdateRequest!) {
  slo_config_update(request: $data) {
    data {
      success
    }
  }
}
`;

export const SLO_REPORT = `
query SLOReport {
  slo_report_v2(where: __WHERE__, order_by: [{column: "updated_at", order: desc}]) {
    rows {
      slo_config
      id
      error_budget_burn_rate
      cloud_account_id
      workload_name
      workload_namespace
      status
      events_count
      good_events_count
      bad_events_count
      config_id
      created_at
      updated_at
    }
  }
}
`;

export const AWS_CLOUD_FORMATION_URL = `
mutation AWSCloudFormation($object: AWSCloudFormationInput!) {
  aws_cloud_formation(object: $object) {
    url
    bucket_name
    external_id
    auto_detection_enabled
  }
}
`;

export const AWS_EVENTBRIDGE_ONBOARD = `
mutation AwsEventBridgeOnboard($object: AwsEventBridgeOnboardInput!) {
  aws_eventbridge_onboard(object: $object) {
    url
    external_id
  }
}
`;

export const CLOUD_UPDATE_CLOUDFORMATION_PERMISSIONS = `
mutation CloudUpdateCloudformationPermissions($object: CloudUpdateCloudformationPermissionsInput!) {
  cloud_update_cloudformation_permissions(object: $object) {
    url
    stack_name
    template_version
    latest_version
    needs_update
  }
}
`;

export const AZURE_EVENTGRID_ONBOARD = `
mutation AzureEventGridOnboard($object: AzureEventGridOnboardInput!) {
  azure_eventgrid_onboard(object: $object) {
    url
    external_id
    webhook_url
  }
}
`;

export const GCP_PUBSUB_ONBOARD = `
mutation GcpPubSubOnboard($object: GcpPubSubOnboardInput!) {
  gcp_pubsub_onboard(object: $object) {
    deployment_manager_url
    external_id
    pubsub_project_id
    subscription_name
    template_yaml_url
  }
}
`;

export const GET_SLO_CONFIGS = `
query GetSLOConfigs {
  slo_config_v2(where: __WHERE__) {
    rows {
      window
      workload_name
      workload_namespace
      goal
      cloud_account_id
      enabled
      id
      name
      threshold
      created_at
      updated_at
    }
  }
}
`;

export const K8s_WORKLOAD_KIND_COUNT = `
query K8sWorkloadKindCount($account_id: String) {
  workload_counts: k8s_workload_groupings_v2(where: {account_id: {_eq: $account_id}, is_active: {_eq: true}  __WHERE__}) {
    rows {
      count
      deployment_count
      statefulset_count
      daemonset_count
      replicaset_count
      job_count
      cronjob_count
      rollout_count
    }
  }
}
`;

export const K8s_EVENT_AGGREGATE = `
query EventAggregate {
  event_groupings_v2(where: __WHERE__) {
    rows {
      __COLS__
    }
  }
}
`;

export const K8s_WORKLOAD_MTD = `
query WorkloadGroupings {
  k8s_metrics_groupings_v2(where: __WHERE__, column_transformations: [{name: "timestamp", expr: "date_unit", args: ["day"]}]) {
    rows {
      cost
    }
  }
}
`;

export const TRIGGER_ANOMALY_EXECUTE = `
mutation TriggerAnomalyExecute($request: TriggerAnomalyExecuteRequest!) {
  trigger_anomaly_execute(request: $request) {
    status
    message
  }
}
`;

const FLIGHT_CHECK_QUERY = `
query FlightCheck($where: RecommendationWhereRequest) {
  recommendation: recommendations_v2(where: $where) {
    rows {
      recommendation
      created_at
      updated_at
    }
  }
}`;

async function fetchFlightCheck(accountId: string, planId: string, ruleName: string) {
  try {
    const where = {
      account_object_id: { _eq: planId },
      account_id: { _eq: accountId },
      rule_name: { _eq: ruleName },
    };
    const response = await queryGraphQL(FLIGHT_CHECK_QUERY, 'FlightCheck', { where });
    const rows = (response?.data?.data?.recommendation?.rows || []).map((row: any) => ({
      ...row,
      recommendation: typeof row.recommendation === 'string' ? safeJSONParse(row.recommendation) : row.recommendation,
    }));
    return { data: { data: { recommendation: rows } } };
  } catch {
    return { data: { data: { recommendation: [] } } };
  }
}

const KG_GET_NODE = `
mutation KgGetNode {
  kg_get_node(request: __WHERE__) {
    data
  }
}
`;

const KG_GET_EDGE = `
mutation KgGetEdge {
  kg_get_edge(request: __WHERE__) {
    data
  }
}
`;

const apiKubernetes1 = {
  async triggerAnomalyExecute(accountId: string) {
    if (accountId === 'demo') return null;
    try {
      const response = await queryGraphQL(TRIGGER_ANOMALY_EXECUTE, 'TriggerAnomalyExecute', {
        request: {
          account_id: accountId,
        },
      });
      return response;
    } catch (error) {
      console.log('failed to trigger anomaly execute-', error);
      return error;
    }
  },
  async getSLOReport(sloReportParams: any) {
    const query: any = {};
    if (sloReportParams?.workload_namespace) {
      if (Array.isArray(sloReportParams?.workload_namespace)) {
        query['workload_namespace'] = { _in: sloReportParams.workload_namespace };
      } else {
        query['workload_namespace'] = { _eq: sloReportParams.workload_namespace };
      }
    }
    if (sloReportParams?.workload_name) {
      if (Array.isArray(sloReportParams?.workload_name)) {
        query['workload_name'] = { _in: sloReportParams.workload_name };
      } else {
        query['workload_name'] = { _eq: sloReportParams.workload_name };
      }
    }
    if (sloReportParams?.end_date && sloReportParams?.start_date) {
      query['_and'] = [{ timestamp: { _gte: sloReportParams.start_date } }, { timestamp: { _lt: sloReportParams.end_date } }];
    }
    if (sloReportParams?.config_id) {
      query['config_id'] = { _eq: sloReportParams.config_id };
    }
    if (sloReportParams?.account_id) {
      query['cloud_account_id'] = { _eq: sloReportParams.account_id };
    }
    if (sloReportParams?.status) {
      query['status'] = { _eq: sloReportParams.status };
    }
    try {
      const response = await queryGraphQL(SLO_REPORT.replaceAll('__WHERE__', gqlStringify(query)), 'SLOReport');
      if (response?.data?.data?.slo_report_v2?.rows) {
        response.data.data.slo_report = response.data.data.slo_report_v2.rows.map((item: any) => ({
          ...item,
          slo_config: typeof item.slo_config === 'string' ? JSON.parse(item.slo_config) : item.slo_config,
        }));
      }
      return response;
    } catch (error) {
      console.log('failed to get slo report-', error);
    }
  },
  async getSLOConfig(query: any) {
    try {
      const response = await queryGraphQL(SLO_LIST, 'SLOConfigList', {
        data: query,
      });
      return response;
    } catch (error) {
      console.log('failed to get slo config-', error);
      return error;
    }
  },
  async createSLOConfig(data: any) {
    try {
      const response = await queryGraphQL(SLO_CREATE, 'SLOCreate', {
        data: data,
      });
      return response;
    } catch (error) {
      console.log('failed to create slo config-', error);
      return error;
    }
  },
  async updateSLOConfig(data: any) {
    try {
      const response = await queryGraphQL(SLO_UPDATE, 'SLOUpdate', {
        data: data,
      });
      return response;
    } catch (error) {
      console.log('failed to update slo config-', error);
      return error;
    }
  },
  async getEventRules(query: any, limit: number, offset: number) {
    if (query.accountId === 'demo') {
      const demoData = await getMockData('event-rules');
      return demoData.GetEventRules;
    }
    try {
      const filterParams: any = {};
      if (query?.accountId && query.accountId !== 'undefined') {
        filterParams['account_id'] = { _eq: query.accountId };
      }
      if (query?.category) {
        filterParams['category'] = { _eq: query.category };
      }
      if (query?.source) {
        filterParams['source'] = { _eq: query.source };
      }
      if (query?.severity) {
        filterParams['severity'] = { _eq: query.severity };
      }
      if (query?.status) {
        filterParams['enabled'] = { _eq: query.status == 'Enabled' };
      }
      if (query?.searchByName) {
        filterParams['alert'] = { _ilike: '%' + query.searchByName + '%' };
      }
      const response = await queryGraphQL(GET_EVENT_RULES.replaceAll('__WHERE__', gqlStringify(filterParams)), 'GetEventRules', {
        limit: limit,
        offset: offset,
      });
      const rawData = response?.data?.data;
      return {
        data: {
          event_rules: rawData?.event_rules_v2?.rows,
          event_rules_aggregate: {
            aggregate: {
              count: rawData?.event_rules_aggregate?.rows?.[0]?.count,
            },
          },
        },
      };
    } catch (error) {
      console.log('failed to fetch event rules-', error);
      return error;
    }
  },
  async getAllEventRuleNames(query: any) {
    if (query.accountId === 'demo') {
      const demoData = await getMockData('event-rules');
      return demoData.GetEventRules;
    }
    const cacheResponse = cache.get(`${query.accountId}.listAllEventRuleNames`);
    if (!cacheResponse) {
      try {
        const filterParams: any = {};
        if (query?.accountId && query.accountId !== 'undefined') {
          filterParams['account_id'] = { _eq: query.accountId };
        }
        const response = await queryGraphQL(GET_ALL_EVENT_RULE_NAMES.replaceAll('__WHERE__', gqlStringify(filterParams)), 'GetAllEventRuleNames');
        const transformedData = {
          event_rules: response?.data?.data?.event_rules_v2?.rows || [],
        };
        cache.set(`${query.accountId}.listAllEventRuleNames`, {
          data: transformedData,
        });
        return {
          data: transformedData,
        };
      } catch (error) {
        console.log('failed to fetch event rules-', error);
        return [];
      }
    } else {
      return cacheResponse;
    }
  },
  async createAlertManager(data: any) {
    try {
      const response = await queryGraphQL(CREATE_ALERT_MANAGER.replace('__REQUEST__', gqlStringify(data)), 'CreateAlertManager', {});
      return {
        data: response?.data,
      };
    } catch (error) {
      console.log('failed to create event rules-', error);
      return error;
    }
  },
  async updateAlertManager(data: any) {
    try {
      const response = await queryGraphQL(UPDATE_ALERT_MANAGER.replace('__REQUEST__', gqlStringify(data)), 'UpdateAlertManagerRule', {});
      return {
        data: response?.data,
      };
    } catch (error) {
      console.log('failed to update the alert rule, ', error);
      return error;
    }
  },
  async getDistinctData(accountId: string) {
    if (accountId === 'demo') {
      const demoData = await getMockData('event-rules');
      return demoData.GetDistinctCategorySourceSeverity.data;
    }

    if (!accountId || accountId === 'undefined') {
      return { data: { distinct_category: [], distinct_source: [], distinct_severity: [] } };
    }

    try {
      const where: any = { account_id: { _eq: accountId } };
      const response = await queryGraphQL(GET_DISTINCT_DATA.replaceAll('__WHERE__', gqlStringify(where)), 'GetDistinctCategorySourceSeverity');
      const rawData = response?.data?.data;
      return {
        data: {
          distinct_category: rawData?.distinct_category?.rows,
          distinct_source: rawData?.distinct_source?.rows,
          distinct_severity: rawData?.distinct_severity?.rows,
        },
      };
    } catch (error) {
      console.log('failed to fetch distinct data- ', error);
      return error;
    }
  },
  async disableAlertManager(data: any) {
    try {
      const response = await queryGraphQL(DISABLE_ALERT_MANAGER.replace('__REQUEST__', gqlStringify(data)), 'DisableAlertManagerRule', {});
      return {
        data: response?.data,
      };
    } catch (error) {
      console.log('failed to disable the alert rule, ', error);
      return error;
    }
  },
  async listAcc({
    nameSearch,
    statusSearch,
    cloudProvider,
    limit,
    offset,
  }: { nameSearch?: string; statusSearch?: string; cloudProvider?: string; limit?: number; offset?: number } = {}) {
    try {
      const where: any = { cloud_provider: { _eq: 'K8s' } };
      if (cloudProvider) {
        where.cloud_provider = { _eq: cloudProvider };
      }
      if (nameSearch) {
        where.account_name = { _ilike: `%${nameSearch}%` };
      }
      if (statusSearch) {
        where.status = { _eq: statusSearch };
      }

      const queryStr = LIST_ACC.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(queryStr, 'ListAcc', {
        limit: limit || 100,
        offset: offset || 0,
      });
      return response;
    } catch (error) {
      console.log('failed to fetch acc-', error);
      return error;
    }
  },
  async listK8sAccAgentHealth(cloudAccountId?: string | string[]) {
    try {
      const where: Record<string, unknown> = { type: { _eq: 'k8s' } };
      if (Array.isArray(cloudAccountId) && cloudAccountId.length > 0) {
        where.cloud_account_id = { _in: cloudAccountId };
      } else if (cloudAccountId) {
        where.cloud_account_id = { _eq: cloudAccountId };
      }
      const queryStr = LIST_K8s_ACC_AGENT.replace('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(queryStr, 'AccountAgentHealth');
      if (response?.data?.data?.agent?.rows) {
        response.data.data.agent = response.data.data.agent.rows;
      }
      return response;
    } catch (error) {
      console.log('failed to fetch acc agent-', error);
      return error;
    }
  },
  async getAWSCloudFormationURL(query: any) {
    try {
      const response = await queryGraphQL(AWS_CLOUD_FORMATION_URL, 'AWSCloudFormation', {
        object: query,
      });
      return response;
    } catch (error) {
      console.log('failed to get aws cloud formation url-', error);
      return error;
    }
  },
  async getAwsEventBridgeOnboardURL(accountId: string) {
    try {
      const response = await queryGraphQL(AWS_EVENTBRIDGE_ONBOARD, 'AwsEventBridgeOnboard', {
        object: { account_id: accountId },
      });
      return response;
    } catch (error) {
      console.error('failed to get aws eventbridge onboard url-', error);
      return error;
    }
  },
  async getCloudUpdateCloudformationPermissions(accountId: string) {
    try {
      const response = await queryGraphQL(CLOUD_UPDATE_CLOUDFORMATION_PERMISSIONS, 'CloudUpdateCloudformationPermissions', {
        object: { account_id: accountId },
      });
      return response;
    } catch (error) {
      console.error('failed to get cloud update cloudformation permissions-', error);
      return error;
    }
  },
  async getAzureARMTemplateURL(accountId: string) {
    try {
      const response = await queryGraphQL(AZURE_EVENTGRID_ONBOARD, 'AzureEventGridOnboard', {
        object: { account_id: accountId },
      });
      return response;
    } catch (error) {
      console.error('failed to get azure arm template url-', error);
      return error;
    }
  },
  async getGcpDeploymentManagerURL(accountId: string) {
    try {
      const response = await queryGraphQL(GCP_PUBSUB_ONBOARD, 'GcpPubSubOnboard', {
        object: { account_id: accountId },
      });
      return response;
    } catch (error) {
      console.error('failed to get gcp deployment manager url-', error);
      return error;
    }
  },
  async listAWSAcc() {
    try {
      const response = await queryGraphQL(LIST_AWS_ACC, 'ListAWSAcc');
      if (response?.data?.data?.cloud_accounts?.rows) {
        response.data.data.cloud_accounts = response.data.data.cloud_accounts.rows.map((item: any) => ({
          ...item,
          user: { display_name: item.created_by_name },
        }));
      }
      return response;
    } catch (error) {
      console.log('failed to fetch aws acc-', error);
      return error;
    }
  },
  async listGCPAcc() {
    try {
      const response = await queryGraphQL(LIST_GCP_ACC, 'ListGCPAcc');
      if (response?.data?.data?.cloud_accounts?.rows) {
        response.data.data.cloud_accounts = response.data.data.cloud_accounts.rows.map((item: any) => ({
          ...item,
          user: { display_name: item.created_by_name },
        }));
      }
      return response;
    } catch (error) {
      console.log('failed to fetch gcp acc-', error);
      return error;
    }
  },
  async listSLOConfigs(queryParams: any) {
    try {
      if (queryParams?.cloud_account_id === 'demo') {
        const sloDemoData = await getMockData('slo');
        return sloDemoData.slo_config;
      }

      const query: any = {};
      if (queryParams?.cloud_account_id) {
        query.cloud_account_id = { _eq: queryParams.cloud_account_id };
      }
      if (queryParams?.namespace) {
        if (Array.isArray(queryParams?.namespace)) {
          query.workload_namespace = { _in: queryParams.namespace };
        } else {
          query.workload_namespace = { _eq: queryParams.namespace };
        }
      }
      if (queryParams?.workload_name) {
        if (Array.isArray(queryParams?.workload_name)) {
          query.workload_name = { _in: queryParams.workload_name };
        } else {
          query.workload_name = { _eq: queryParams.workload_name };
        }
      }
      if (queryParams?.created_after) {
        query.created_at = { _gte: queryParams.created_after };
      }
      query.enabled = { _eq: true };
      const response = await queryGraphQL(GET_SLO_CONFIGS.replaceAll('__WHERE__', gqlStringify(query)), 'GetSLOConfigs');
      if (response?.data?.data?.slo_config_v2?.rows) {
        response.data.data.slo_config = response.data.data.slo_config_v2.rows;
      }
      return response;
    } catch (error) {
      console.log('failed to list slo configs-', error);
      return error;
    }
  },
  async consumePrometheusQueries(requestBody: any) {
    const response = await observability.metricsQuery(requestBody);
    return response;
  },
  async getWorkloadEventCounts(workloadFqdn: string[], start_date: string, end_date: string, accountId: string) {
    if (accountId === 'demo') return null;
    const subjectNameConditions: { service_key: { _eq: string } }[] = [];
    const namespaceSet: Set<string> = new Set();

    workloadFqdn.forEach((obj: string) => {
      const [namespaceName, workloadName, workloadKind] = obj.split('.');

      namespaceSet.add(namespaceName);
      subjectNameConditions.push({ service_key: { _eq: `${namespaceName}/${workloadKind}/${workloadName}` } });
    });

    const namespaces: string[] = Array.from(namespaceSet);

    const EVENT_WORKLOAD_COUNT = `
    query EventWorkloadCount {
      event_groupings_v2(
        where: __WHERE__
      ) {
        rows {
          count: event_count
          service_key
          subject_namespace
        }
      }
    }
  `;
    const query: any = {};
    query.account_id = { _eq: accountId };
    query.priority = { _eq: 'HIGH' };
    query.finding_type = { _eq: 'issue' };
    query._and = [{ starts_at: { _gte: start_date } }, { starts_at: { _lte: end_date } }, { _or: subjectNameConditions }];
    query.subject_namespace = { _in: namespaces };
    const response = await queryGraphQL(EVENT_WORKLOAD_COUNT.replace('__WHERE__', gqlStringify(query)), 'EventWorkloadCount', {});
    return response;
  },
  async getWorkloadRecommendationCounts(accountObjectIds: string[], accountId: string) {
    if (accountId === 'demo') return null;
    const RECOMMENDATION_WORKLOAD_COUNT = `
    query RecommendationWorkloadCount {
      recommendation_groupings_v2(
        where: __WHERE__
      ) {
        rows {
          count
          account_object_id
        }
      }
    }
  `;
    const query: any = {};
    query.account_id = { _eq: accountId };
    query.status = { _in: ['Open', 'InProgress'] };
    query.category = { _eq: 'RightSizing' };
    query.account_object_id = { _in: accountObjectIds.map((aOId) => aOId.replaceAll('___', '/').replace(/(?<![_])__(?![_])/g, '-')) };
    const response = await queryGraphQL(RECOMMENDATION_WORKLOAD_COUNT.replace('__WHERE__', gqlStringify(query)), 'RecommendationWorkloadCount', {});
    return response;
  },
  async listK8sWorkloadKindCount(accountId: string, namespace: string, resource_ids: string[]) {
    if (accountId === 'demo') return null;
    let where = namespace ? `, namespace: { _eq: "${namespace}" }` : ``;
    if (resource_ids) {
      where += `, resource_id : {_in: [${resource_ids.map((i) => `"${i}"`)}]}`;
    }
    const response = await queryGraphQL(K8s_WORKLOAD_KIND_COUNT.replaceAll('__WHERE__', where), 'K8sWorkloadKindCount', {
      account_id: accountId,
    });
    return response;
  },
  async getEventAggregate(data: any, columns: string[]) {
    const query: any = {};
    query.account_id = { _eq: data.account_id };

    if (data.namespace) {
      query.subject_namespace = { _eq: data.namespace };
    }
    if (data.startDate && data.endDate) {
      query['_and'] = [{ starts_at: { _gte: data.startDate.toISOString() } }, { starts_at: { _lte: data.endDate.toISOString() } }];
    }
    if (Array.isArray(data.aggregationKey)) {
      query.aggregation_key = { _in: data.aggregationKey };
    } else if (data.aggregationKey) {
      query.aggregation_key = { _eq: data.aggregationKey };
    }

    if (data.resource_ids) {
      query.resource_id = { _in: data.resource_ids };
    }
    const response = await queryGraphQL(
      K8s_EVENT_AGGREGATE.replaceAll('__COLS__', columns.join(' ')).replaceAll('__WHERE__', gqlStringify(query)),
      'EventAggregate',
      {}
    );
    return response;
  },
  async getWorkloadMTDAggregate(data: any) {
    const query: any = {};
    query.account_id = { _eq: data.account_id };

    if (data.namespace) {
      query.namespace_name = { _eq: data.namespace };
    }
    if (data.startDate && data.endDate) {
      query.timestamp = { _between: { _gt: data.startDate.toISOString(), _lt: data.endDate.toISOString() } };
    }

    if (data.workloads) {
      query.resource_id = { _in: data.workloads };
    }

    const response = await queryGraphQL(K8s_WORKLOAD_MTD.replaceAll('__WHERE__', gqlStringify(query)), 'WorkloadGroupings', {});
    return response;
  },
  async getIndividualEventTypeCount(accountId: string, startDate: string, endDate: string) {
    if (accountId == 'demo') {
      const response = await getMockData('eventTypesCount');
      return response;
    }
    const GET_INIDIVIDUAL_EVENT_TYPE_COUNT = `   
      query GetCountOfEventType($accountId: String!, $startDate: Datetime!, $endDate: Datetime!) {
        pod_error_count: event_groupings_v2(where: {account_id:{_eq: $accountId},subject_type:{_eq:"pod"},finding_type:{_eq:"issue"}, status:{_eq: "FIRING"}, _and: [{starts_at: {_gte: $startDate}}, {starts_at: {_lte: $endDate}}]}) {
          rows{
            count: event_count
          }
        }
        node_error_count: event_groupings_v2(where: {account_id:{_eq: $accountId},subject_type:{_eq:"node"},finding_type:{_eq:"issue"}, status:{_eq: "FIRING"}, _and: [{starts_at: {_gte: $startDate}}, {starts_at: {_lte: $endDate}}]}) {
          rows{
            count: event_count
          }
        }
        application_error_count: event_groupings_v2(where: {account_id:{_eq: $accountId},finding_type:{_eq:"issue"},aggregation_key:{_in:["HighErrorCriticalLogs","ApplicationAPIFailures"]}, status:{_eq: "FIRING"}, _and: [{starts_at: {_gte: $startDate}}, {starts_at: {_lte: $endDate}}]}) {
          rows{
            count: event_count
          }
        }
        event_count: event_groupings_v2(where: {account_id:{_eq: $accountId},finding_type:{_eq:"issue"}, status:{_eq: "FIRING"}, _and: [{starts_at: {_gte: $startDate}}, {starts_at: {_lte: $endDate}}]}) {
          rows{
            count: event_count
          }
        }
      }
    `;
    const response = await queryGraphQL(GET_INIDIVIDUAL_EVENT_TYPE_COUNT, 'GetCountOfEventType', {
      accountId: accountId,
      startDate: startDate,
      endDate: endDate,
    });
    return response;
  },
  async getIndividualAggregationKeyCount(data: any) {
    if (data.account_id == 'demo') {
      const response = await getMockData('individualAggregationKeyCount');
      return response;
    }
    const GET_INIDIVIDUAL_AGGREGATION_KEY_COUNT = `   
    query AggregationKeyCountForTabs {
      event_groupings_v2(where: __WHERE__) {
        rows {
          count: event_count
          aggregation_key
        }
      }
    }
    `;
    const query: any = {};
    query['account_id'] = { _eq: data['account_id'] };
    if (data['subject_type']) {
      query['subject_type'] = { _eq: data['subject_type'] };
    }
    if (data?.finding_type) {
      query['finding_type'] = { _eq: data['finding_type'] };
    }
    if (data?.aggregation_key) {
      if (Array.isArray(data['aggregation_key'])) {
        query['aggregation_key'] = { _in: data['aggregation_key'] };
      } else {
        query['aggregation_key'] = { _eq: data['aggregation_key'] };
      }
    }
    if (data?.status) {
      query['status'] = { _eq: data['status'] };
    }

    const response = await queryGraphQL(
      GET_INIDIVIDUAL_AGGREGATION_KEY_COUNT.replace('__WHERE__', gqlStringify(query)),
      'AggregationKeyCountForTabs',
      {}
    );
    return response;
  },
  async listInsights(accountId: string | string[]) {
    try {
      const insights: any = {};
      const accountIds = Array.isArray(accountId) ? accountId : [accountId];
      for (const id of accountIds) {
        if (id == 'demo') {
          const response = await getMockData('home');
          return { demo: response['GetInsights'].data.list_insights.data };
        }
        const LIST_K8s_INSIGHTS = `
          query ListK8sInsights {
            insight_v2(where:__WHERE__) {
              rows {
                applications
                source
                tenant_id
                title
                unique_id
                type
                rule
              }
            }
          }
        `;
        insights[id] = [];
        const where: any = {};
        where.account_id = { _eq: id };
        where.status = { _eq: 'Open' };
        const query = LIST_K8s_INSIGHTS.replaceAll('__WHERE__', gqlStringify(where));
        const response = await queryGraphQL(query, 'ListK8sInsights');
        insights[id] = (response?.data?.data?.insight_v2?.rows ?? []).map((item: any) => ({
          ...item,
          tenant: item.tenant_id,
          rule: typeof item.rule === 'string' ? safeJSONParse(item.rule) : item.rule,
        }));
      }
      return insights;
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getSLOObservation(data: any) {
    const SLO_OBSERVATION = `
    query SloReportObservationV2 {
      slo_report_observation_v2(where: __WHERE__) {
        rows {
          config_name
          total_bad_events
          total_events
          total_good_events
          workload_name
          workload_namespace
          status
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = { _eq: data.accountId };
    if (data.timestamp) {
      query.timestamp = { _gte: data.timestamp };
    }
    if (data.workloads) {
      if (Array.isArray(data.workloads)) {
        query.workload_name = { _in: data.workloads };
      } else {
        query.workload_name = { _eq: data.workloads };
      }
    }
    if (data.namespaces) {
      if (Array.isArray(data.namespaces)) {
        query.workload_namespace = { _in: data.namespaces };
      } else {
        query.workload_namespace = { _eq: data.namespaces };
      }
    }
    const response = await queryGraphQL(SLO_OBSERVATION.replace('__WHERE__', gqlStringify(query)), 'SloReportObservationV2', {});
    return response;
  },
  async listK8sVersions() {
    const LIST_K8s_VERSIONS = `
    query ListK8sVersions {
      k8s_versions {
        version
        release_date
      }
    }
    `;
    try {
      const response = await queryGraphQL(LIST_K8s_VERSIONS, 'ListK8sVersions', {});
      return response;
    } catch (error) {
      console.log('failed to fetch list k8s version-', error);
      return error;
    }
  },
  async listK8sAnomaliesData(data: any) {
    if (data.accountId == 'demo') {
      const demoData = await getMockData('k8s-anomaly');
      return { data: { data: demoData } };
    }
    const LIST_K8s_ANOMALIES_DATA = `
    query ListK8sAnomaliesData(
      $offset: Int,
      $limit: Int
    ) {
      anomaly_grouping_v2(where: __WHERE__){
        rows {
          count
        }
      }
      anomaly_v2(where: __WHERE__, offset: $offset, limit: $limit, order_by: {column: "evaluated_at", order: desc}) {
        rows {
          anomaly_type
          updated_at
          config_id
          current_value
          evaluated_at
          is_anomaly
          name
          namespace
          reference_value
          account_id
          id
          insights {
            timestamp
            value
            baseline_value
            deviation_absolute
            deviation_percent
            severity
            anomaly_score
            comparison_window
          }
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = { _eq: data.accountId };
    query.is_anomaly = { _eq: true };
    if (data.namespace) {
      query.namespace = { _eq: data.namespace };
    }
    if (data.workload) {
      query.name = { _eq: data.workload };
    }
    if (data.anomalyType) {
      query.anomaly_type = { _eq: data.anomalyType };
    }
    const response = await queryGraphQL(LIST_K8s_ANOMALIES_DATA.replaceAll('__WHERE__', gqlStringify(query)), 'ListK8sAnomaliesData', {
      offset: data.offset ?? 0,
      limit: data.limit ?? 10,
    });
    return response;
  },
  async listK8sAnomalies(data: any) {
    if (data.accountId == 'demo') {
      const demoData = await getMockData('k8s-anomaly');
      return { data: { data: demoData } };
    }
    const LIST_K8s_ANOMALIES = `
    query ListK8sAnomalies(
      $offset: Int,
      $limit: Int
    ) {
      anomaly_v3(where: __WHERE__, offset: $offset, limit: $limit) {
        rows {
          anomaly_type
          name
          namespace
          anomaly_count
          evaluated_at
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = { _eq: data.accountId };
    if (data.namespace) {
      query.namespace = { _eq: data.namespace };
    }
    if (data.workload) {
      query.name = { _eq: data.workload };
    }
    if (data.anomalyType) {
      query.anomaly_type = { _eq: data.anomalyType };
    }
    const response = await queryGraphQL(LIST_K8s_ANOMALIES.replaceAll('__WHERE__', gqlStringify(query)), 'ListK8sAnomalies', {
      offset: data.offset ?? 0,
      limit: data.limit ?? 10,
    });
    return response;
  },
  async listK8sAnomaliesCount(data: any) {
    if (data.accountId == 'demo') {
      const demoData = await getMockData('k8s-anomaly');
      return { data: { data: demoData } };
    }
    const ANOMALIES_COUNT = `
    query K8sAnomaliesCount {
      anomaly_v3(where: __WHERE__) {
        rows {
          count
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = { _eq: data.accountId };
    if (data.namespace) {
      query.namespace = { _eq: data.namespace };
    }
    if (data.workload) {
      query.name = { _eq: data.workload };
    }
    if (data.anomalyType) {
      query.anomaly_type = { _eq: data.anomalyType };
    }
    const response = await queryGraphQL(ANOMALIES_COUNT.replaceAll('__WHERE__', gqlStringify(query)), 'K8sAnomaliesCount', {});
    return response;
  },
  async listDistinctAnomalyTypes() {
    const query = `
    query ListDistinctK8sAnomalyTypes {
      anomaly_type_v2 {
        rows {
          value
        }
      }
    }`;

    try {
      const response = await queryGraphQL(query, 'ListDistinctK8sAnomalyTypes', {});
      return { anomaly_type: response.data.data?.anomaly_type_v2?.rows || [] };
    } catch {
      return { anomaly_type: [] };
    }
  },
  async listAnomalyTemplate() {
    const LIST_ANOMALY_TEMPLATE = `
    mutation ListAnomalyTemplate {
      anomaly_template_list(request: {account_id: ""}) {
        data {
          anomaly_type
          buffer_percentage
          change_operator
          description
          title
        }
      }
    }    
    `;
    try {
      const response = await queryGraphQL(LIST_ANOMALY_TEMPLATE, 'ListAnomalyTemplate', {});
      return response;
    } catch (error) {
      console.log('failed to fetch list anomaly template-', error);
      return error;
    }
  },
  async listActionPlaybookActions(data: any) {
    const LIST_ACTION_PLAYBOOK_ACTION = `
    mutation ListAgentPlaybookAction {
      alertmanager_list_actions(request: __REQUEST__) {
        actions
      }
    }
    `;
    try {
      const response = await queryGraphQL(LIST_ACTION_PLAYBOOK_ACTION.replace('__REQUEST__', gqlStringify(data)), 'ListAgentPlaybookAction', {});
      return response;
    } catch (error) {
      console.log('failed to fetch list agent playbook action-', error);
      return error;
    }
  },
  async getAgentPlaybookOfEvent(data: any) {
    const GET_AGENT_PLAYBOOK_EVENT = `
    query GetAgentPlaybookOfEvent {
      agent_playbook_v2(where: __WHERE__) {
        rows {
          id
          trigger_params
          alert_name
          action_params
        }
      }
    }
    `;
    const where: any = {};
    where.cloud_account_id = { _eq: data.accountId };
    if (Array.isArray(data.alertName)) {
      where.alert_name = { _in: data.alertName };
    } else {
      where.alert_name = { _eq: data.alertName };
    }
    try {
      const response = await queryGraphQL(GET_AGENT_PLAYBOOK_EVENT.replace('__WHERE__', gqlStringify(where)), 'GetAgentPlaybookOfEvent', {});
      if (response?.data?.data?.agent_playbook_v2?.rows) {
        response.data.data.agent_playbook = response.data.data.agent_playbook_v2.rows;
      }
      return response;
    } catch (error) {
      console.log('failed to get agent playbook for event- ', error);
      return error;
    }
  },
  async applicationProfileConvert(data: any) {
    const APPLICATION_PROFILE_CONVERT = `
    mutation ApplicationProfileConvert {
      application_profile_convert(request: __WHERE__) {
        data {
          svg_profile
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = data.accountId;
    query.base64_profile = data.base64_profile;
    try {
      const response = await queryGraphQL(APPLICATION_PROFILE_CONVERT.replace('__WHERE__', gqlStringify(query)), 'ApplicationProfileConvert', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  async applicationProfile(data: any) {
    const APPLICATION_PROFILE = `
    mutation ApplicationProfile {
      application_profile(request: __WHERE__) {
        data {
          status
          base64_profile
          error_message
          profile_task_id
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(APPLICATION_PROFILE.replace('__WHERE__', gqlStringify(data)), 'ApplicationProfile', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  async applicationProfileStatus(data: any) {
    const GET_APPLICATION_PROFILE = `
    mutation GetApplicationProfile {
      application_get_profile(request: __WHERE__) {
        data {
          status
          base64_profile
          error_message
          profile_task_id
        }
      }
    }
    `;
    try {
      const response = await queryGraphQL(GET_APPLICATION_PROFILE.replace('__WHERE__', gqlStringify(data)), 'GetApplicationProfile', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  async applicationProfileHistory(data: any) {
    const APPLICATION_PROFILE_HISTORY = `
    query ApplicationProfileHistory {
      application_profile_v2(where: __WHERE__, order_by: {order: desc, column: "updated_at"}) {
        rows {
          pod_name
          workload_name
          namespace
          profile
          created_at
          updated_at
          profile_duration
          profile_language
          output_type
          profile_type
        }
      }
    }
    `;
    const query: any = {};
    query.cloud_account_id = { _eq: data.accountId };
    query.namespace = { _eq: data.namespace };
    if (data.workloadName) {
      query.workload_name = { _eq: data.workloadName };
    }
    if (data.podName) {
      query.pod_name = { _eq: data.podName };
    }

    try {
      const response = await queryGraphQL(APPLICATION_PROFILE_HISTORY.replace('__WHERE__', gqlStringify(query)), 'ApplicationProfileHistory', {});
      return response;
    } catch (error) {
      return error;
    }
  },
  getUpgradePlans: async function (accountId: string) {
    if (accountId === 'demo') return null;
    try {
      const query = `query FetchUpgradePlans($accountId: String! ) {
        upgrade_plan: upgrade_plan_fetch_all(account_id: $accountId) {
          id
          created_at
          updated_at
          current_version
          target_version
          owner
          k8s_provider
          account_id
          tenant_id
          status
          steps
        }
      }
  `;
      const response = await queryGraphQL(query, 'FetchUpgradePlans', { accountId: accountId });
      return response.data;
    } catch (err) {
      console.error('failed to get upgrade plans ', err);
      throw err;
    }
  },
  generateUpgradePlan: async function (accountId: string) {
    if (accountId === 'demo') return null;
    try {
      const query = `mutation GenerateUpgradePlan($accountId: String! ) {
        upgrade_plan_create_one(account_id: $accountId, steps: []) {
          current_version
          target_version
          steps
        }
      }
  `;
      const response = await queryGraphQL(query, 'GenerateUpgradePlan', { accountId: accountId });
      return response.data;
    } catch (err) {
      console.log('failed to generate upgrade ', err);
      return err;
    }
  },
  setUpgradePlanTaskStatus: async function (accountId: string, planId: string, stepId: string, taskId: string, status: string) {
    if (accountId === 'demo') return null;
    try {
      const query = `mutation SetUpgradePlanTaskStatus($accountId: String!, $planId: String!, $stepId: String!, $taskId: String!, $status: String) {
        upgrade_plan_task_upsert_one(account_id: $accountId, plan_id: $planId, step_id: $stepId, task_id: $taskId, status: $status) {
          id
          status
        }
      }
`;
      let statusValue = '';
      switch (status.toLowerCase()) {
        case 'pending':
          statusValue = 'Pending';
          break;
        case 'skipped':
          statusValue = 'Skipped';
          break;
        case 'completed':
          statusValue = 'Completed';
          break;
        case 'failed':
          statusValue = 'Failed';
          break;
        default:
          throw new Error('Invalid status value');
      }
      const response = await queryGraphQL(query, 'SetUpgradePlanTaskStatus', {
        accountId: accountId,
        planId: planId,
        stepId: stepId,
        taskId: taskId,
        status: statusValue,
      });
      return response.data;
    } catch (err) {
      console.log('failed to set upgrade plan task status ', err);
      return err;
    }
  },
  setUpgradePlanTaskOwner: async function (accountId: string, planId: string, stepId: string, taskId: string, owner: string) {
    if (accountId === 'demo') return null;
    try {
      const query = `mutation SetUpgradePlanTaskOwner($accountId: String!, $planId: String!, $stepId: String!, $taskId: String!, $owner: String) {
        upgrade_plan_task_upsert_one(account_id: $accountId, plan_id: $planId, step_id: $stepId, task_id: $taskId, owner: $owner) {
          id
          owner
        }
      }
`;
      const response = await queryGraphQL(query, 'SetUpgradePlanTaskOwner', {
        accountId: accountId,
        planId: planId,
        stepId: stepId,
        taskId: taskId,
        owner: owner,
      });
      return response.data;
    } catch (err) {
      console.log('failed to set upgrade plan task owner ', err);
      return err;
    }
  },
  getUpgradePlanTaskAudits: async function (taskId: string) {
    try {
      const where: any = { task_id: { _eq: taskId } };
      const query = `query getUpgradePlanTaskAudits {
        upgrade_plan_audit_v2(where: __WHERE__, order_by: [{column: "created_at", order: desc}]) {
          rows {
            id
            action
            actioned_by
            new_value
            old_value
            step_id
            task_id
            plan_id
            field
            tenant_id
            created_at
            user_actioned_by
          }
        }
      }`;

      const response = await queryGraphQL(query.replaceAll('__WHERE__', gqlStringify(where)), 'getUpgradePlanTaskAudits');
      if (response?.data?.data?.upgrade_plan_audit_v2?.rows) {
        response.data.data.upgrade_plan_audit = response.data.data.upgrade_plan_audit_v2.rows.map((item: any) => ({
          ...item,
          userActionedBy: item.user_actioned_by
            ? typeof item.user_actioned_by === 'string'
              ? JSON.parse(item.user_actioned_by)
              : item.user_actioned_by
            : null,
        }));
      }
      return response.data;
    } catch (err) {
      console.log('failed to get upgrade plan task audits ', err);
      return err;
    }
  },
  getClusterHealth: async function (accountId: string, resourceType: string) {
    if (accountId === 'demo') return null;
    try {
      const query = `query CheckClusterHealth($accountId: String!, $resourceType: String!) {
        check_cluster_health(account_id: $accountId, resource_type: $resourceType) {
          account_id
          nodes
          persistentVolumes
          services
          workloads
          load_balancers
          node_groups
          helm_compatibility
        }
      }`;

      const response = await queryGraphQL(query, 'CheckClusterHealth', { accountId: accountId, resourceType: resourceType });
      return {
        res: response?.data?.data?.check_cluster_health,
        error: response?.data?.errors,
      };
    } catch (err) {
      console.log('failed to get cluster health ', err);
      return err;
    }
  },
  metricsList: async function (accountId: string) {
    if (accountId === 'demo') return null;
    const METRICS_LIST = `
    mutation MetricsList($accountId: String!) {
      metrics_list(request: {account_id: $accountId}) {
        metric
        attributes
      }
    }
    `;
    try {
      const response = await queryGraphQL(METRICS_LIST, 'MetricsList', {
        accountId,
      });
      return response;
    } catch (err) {
      return err;
    }
  },
  executeClusterUpgradePlannerCommand: async function (
    accountId: string,
    command: string,
    commandType: string,
    planId: string,
    stepId: string,
    taskId: string
  ) {
    const EXECUTE_CLUSTER_UPGRADE_PLANNER_COMMAND = `
    mutation upgradeExecuteCommand($accountId:String!, $command: String!, $commandType:String!, $planId:String!, $stepId:String!, $taskId:String! ) {
      upgrade_execute_command(account_id: $accountId, command: $command, command_type: $commandType, plan_id: $planId, step_id: $stepId, task_id: $taskId) {
        error
        output
        success
      }
    }

`;
    try {
      const response = await queryGraphQL(EXECUTE_CLUSTER_UPGRADE_PLANNER_COMMAND, 'upgradeExecuteCommand', {
        accountId: accountId,
        command: command,
        commandType: commandType,
        planId: planId,
        stepId: stepId,
        taskId: taskId,
      });
      return response;
    } catch (err) {
      return err;
    }
  },
  executeUpgradePreFlightCheck: async function (accountId: string, planId: string) {
    if (accountId === 'demo') return null;
    const EXECUTE_UPGRADE_PRE_FLIGHT_CHECK = `
    mutation upgradePreFlightCheck($accountId:String!, $planId:String!) {
      upgrade_pre_flight_check(account_id: $accountId, plan_id: $planId) {
        id
        plan_id
        account_id
        status
        health_check
      }
    }
`;
    try {
      return await queryGraphQL(EXECUTE_UPGRADE_PRE_FLIGHT_CHECK, 'upgradePreFlightCheck', {
        accountId: accountId,
        planId: planId,
      });
    } catch (err) {
      return err;
    }
  },
  getPreFlightCheck: async function (accountId: string, planId: string) {
    return fetchFlightCheck(accountId, planId, 'pre_flight');
  },
  executeUpgradePostFlightCheck: async function (accountId: string, planId: string) {
    if (accountId === 'demo') return null;
    const EXECUTE_UPGRADE_POST_FLIGHT_CHECK = `
    mutation upgradePostFlightCheck($accountId:String!, $planId:String!) {
      upgrade_post_flight_check(account_id: $accountId, plan_id: $planId) {
        id
        plan_id
        account_id
        status
        health_check
        comparison
        pre_flight_summary
      }
    }
  `;
    try {
      return await queryGraphQL(EXECUTE_UPGRADE_POST_FLIGHT_CHECK, 'upgradePostFlightCheck', {
        accountId: accountId,
        planId: planId,
      });
    } catch (err) {
      return err;
    }
  },
  getPostFlightCheck: async function (accountId: string, planId: string) {
    return fetchFlightCheck(accountId, planId, 'post_flight');
  },
  getTimelineData: async function (eventId: string) {
    const GET_EVENT_TIMELINE = `
    mutation GetEventTimeline {
      event_get_timeline(event_id: "__WHERE__") {
        timeline {
          action
          ref_id
          ref_type
          summary
          timestamp
        }
        event_id
      }
    }
    `;
    try {
      return await queryGraphQL(GET_EVENT_TIMELINE.replace('__WHERE__', eventId), 'GetEventTimeline', {});
    } catch (err) {
      return err;
    }
  },
  updateEvent: async function (data: any) {
    const UPDATE_EVENT = `
    mutation EventUpdate($eventId: String!, $urgency: String!) {
      event_update(request: {event_id: $eventId, urgency: $urgency}) {
        urgency
      }
    }
`;

    try {
      return await queryGraphQL(UPDATE_EVENT, 'EventUpdate', {
        eventId: data.eventId,
        urgency: data.urgency,
      });
    } catch (err) {
      return err;
    }
  },
  logGroup: async function (data: any) {
    const LOG_GROUP_REQUEST = `
    query LogGroup {
      log_group(request: __WHERE__) {
        groups {
          sample
          namespace
          workload
          container
          container_id
          pattern_hash
          level
          count
          timestamps
          values
        }
      }
    }
    `;
    try {
      return await queryGraphQL(LOG_GROUP_REQUEST.replace('__WHERE__', gqlStringify(data)), 'LogGroup', {});
    } catch (err) {
      return err;
    }
  },
  knowledgeGraph: async function (data: any, signal?: AbortSignal) {
    const KNOWLEDGE_GRAPH = `
    mutation KnowledgeGraph {
      kg_get_complete_graph(request: __WHERE__) {
        data {
          edges
          generated_at
          nodes
          tenant_id
        }
      }
    }
    `;
    const request: any = {};
    if (data?.accountIds?.length > 0) {
      request['account_ids'] = data.accountIds;
    }
    if (data?.nodeIds?.length > 0) {
      request['node_ids'] = data.nodeIds;
    }
    if (data?.nodeTypes?.length > 0) {
      request['node_types'] = data.nodeTypes;
    }
    if (data?.attributes) {
      request['attributes'] = data?.attributes;
    }
    if (data?.labels) {
      request['labels'] = data?.labels;
    }
    if (data?.levels) {
      request['levels'] = data.levels;
    }
    try {
      return await queryGraphQL(KNOWLEDGE_GRAPH.replace('__WHERE__', gqlStringify(request)), 'KnowledgeGraph', {}, undefined, signal);
    } catch (err) {
      throw err;
    }
  },
  knowledgeGraphFilterOptions: async function (data?: { accountIds?: string[]; nodeTypes?: string[] }) {
    const KNOWLEDGE_GRAPH_FILTER_OPTIONS = `
    query KgFilterOptions {
      kg_get_filter_options(request: __WHERE__) {
        data {
          account_ids
          attribute_keys
          label_keys
          node_types
          last_sync_time
          node_id_map
          node_count
        }
      }
    }
    `;
    const request: { account_ids?: string[]; node_types?: string[] } = {};
    if (data?.accountIds?.length) request.account_ids = data.accountIds;
    if (data?.nodeTypes?.length) request.node_types = data.nodeTypes;
    try {
      return await queryGraphQL(KNOWLEDGE_GRAPH_FILTER_OPTIONS.replace('__WHERE__', gqlStringify(request)), 'KgFilterOptions', {});
    } catch (err) {
      throw err;
    }
  },
  knowledgeGraphGetNode: async function (nodeId: string) {
    return await queryGraphQL(KG_GET_NODE.replace('__WHERE__', gqlStringify({ node_id: nodeId })), 'KgGetNode', {});
  },
  knowledgeGraphGetEdge: async function (edgeId: string) {
    return await queryGraphQL(KG_GET_EDGE.replace('__WHERE__', gqlStringify({ edge_id: edgeId })), 'KgGetEdge', {});
  },
  knowledgeGraphFilterOptionLabelValues: async function (data: any) {
    const KNOWLEDGE_GRAPH_FILTER_LABEL_VALUES = `
    query KgFilterOptionLabelValues {
      kg_get_filter_values(request: __WHERE__) {
        data {
          filter_key
          filter_type
          values
        }
      }
    }
    `;
    const request: any = {};
    request.filter_type = data.filterType;
    request.filter_key = data.filterKey;
    try {
      return await queryGraphQL(KNOWLEDGE_GRAPH_FILTER_LABEL_VALUES.replace('__WHERE__', gqlStringify(request)), 'KgFilterOptionLabelValues', {});
    } catch (err) {
      throw err;
    }
  },
  eventComparsion: async function (data: any) {
    const EVENT_COMPARISON = `
    query EventComparison {
      current: event_groupings_v2(
        where: __WHERE__
      ) {
        rows {
          event_count
        }
      }
    
      previous: event_groupings_v2(
        where: __WHERE1__
      ) {
        rows {
          event_count
        }
      }
    }
    `;
    const request: any = {};
    request['_and'] = [{ created_at: { _gte: data.startDate } }, { created_at: { _lte: data.endDate } }];
    const request1: any = {};
    request1['_and'] = [{ created_at: { _gte: data.previousStartDate } }, { created_at: { _lte: data.previousEndDate } }];
    try {
      return await queryGraphQL(
        EVENT_COMPARISON.replace('__WHERE__', gqlStringify(request)).replace('__WHERE1__', gqlStringify(request1)),
        'EventComparison',
        {}
      );
    } catch (err) {
      console.error('Error in eventComparison:', err);
      throw err;
    }
  },
  utilisationApi: async function (query: any) {
    const METRICS_QUERY_UTILISATION = `
    query MetricsQueryUtilisation($jsonFilter: jsonb!, $accountId: String!, $startTime: Float!, $endTime: Float!) {
      metrics_query_utilisation(request: {account_id: $accountId, request: $jsonFilter, end_time: $endTime, start_time: $startTime}) {
        results
      }
    }    
    `;
    const data: any = {};
    const time1 = query.startDate;
    const time2 = query.endDate;
    const namespace = query.namespaceName || query.namespace_name;
    const workloadName = query.workloadName || query.workload_name;

    // 1. Pods (Most specific)
    if (query.pod_name && namespace) {
      data.kind = 'pod';
      data.workload_namespace = namespace;
      data.workload_name = query.pod_name;
    }
    // 2. PVCs (Specific resource)
    else if (query.pvcName && namespace) {
      data.kind = 'pvc';
      data.workload_namespace = namespace;
      data.pvc_name = query.pvcName;
    }
    // 3. Workloads (Deployments, StatefulSets, etc.)
    else if (workloadName && namespace) {
      data.kind = query.workloadType || 'workload';
      data.workload_namespace = namespace;
      data.workload_name = workloadName;
    }
    // 4. Namespaces (General)
    else if (namespace) {
      data.kind = 'namespace';
      data.workload_namespace = namespace;
    }
    // 5. Infrastructure (Nodes - usually non-namespaced)
    else if (query.nodeName || query.nodeIp || query.internalIp) {
      data.kind = query.kind || 'node';
      data.node_name = query.nodeName;
      data.node_ip = query.nodeIp;
      data.internal_ip = query.internalIp;
    } else if (query.kind) {
      data.kind = query.kind;
      data.workload_name = workloadName;
      data.container_name = query?.containerName;
    }
    data.metrics = query.metrics;
    data.instant = query.instant ?? false;
    data.regex = query?.regex || false;

    try {
      const response = await queryGraphQL(METRICS_QUERY_UTILISATION, 'MetricsQueryUtilisation', {
        accountId: query.accountId || query.account_id,
        startTime: time1,
        endTime: time2,
        jsonFilter: data,
      });
      const metricsResults = response?.data?.data?.metrics_query_utilisation?.results || [];
      return metricsResults;
    } catch (err) {
      return err;
    }
  },
};

export default apiKubernetes1;
