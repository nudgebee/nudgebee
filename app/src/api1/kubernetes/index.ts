import { queryGraphQL, gqlStringify, hitRelayServer } from '@lib/HttpService';

import {
  getStartOfYear,
  getEndOfYear,
  getStartOfDay,
  getEndOfDay,
  getStartOfMonth,
  getEndOfMonth,
  getYesterday,
  getLast7Days,
  formatDateTime,
} from '@lib/datetime';
import { getBudgetExpectedMonthlyExpense, getExpectedYearlyExpense } from '@lib/budget';
import { convertNumberToTimestampPromFormat, parseHttpResponseBodyMessage, safeJSONParse } from 'src/utils/common';
import getMockData from '@api1/mock';
import { getClusterData } from '@context/DataContext';

function parseInsightJsonFields(rows: any[]) {
  if (!rows) return rows;
  return rows.map((row: any) => ({
    ...row,
    applications: typeof row.applications === 'string' ? JSON.parse(row.applications) : row.applications,
    rule: typeof row.rule === 'string' ? JSON.parse(row.rule) : row.rule,
  }));
}

export const LIST_k8_CLUSTER_DATA = `
query listK8ClusterData {
  k8s_cluster_groupings: k8s_cluster_groupings_v2 {
    rows{
      account_id
      node_count
      node_cpu_capacity
      node_cpu_allocatable
      node_memory_capacity
      node_memory_allocatable
      node_spot_count
      workload_type_counts
      pod_status_counts
    }
  }

  cloud_accounts: get_cloud_accounts_v2(where:{cloud_provider:{_eq:"K8s"}, status:{_eq:"active"}}){
    rows {
      id
      account_name
    }
  }
}`;

const SPEND_AGGREGATES_FIELDS = `
  spends_aggregate: spend_groupings_v2(where:{account_id: {_eq:$accountId1}, spend_date:{_gte:$startDate, _lte:$endDate}, exclude_aggregate: {_eq: false}}){
    rows{
      spend_amount
    }
  }
  yearly_spends_aggregate: spend_groupings_v2(where:{account_id: {_eq:$accountId1}, spend_date:{_gte:$yearStartDate, _lte:$yearEndDate}, exclude_aggregate: {_eq: false}}){
    rows{
      spend_amount
    }
  }
  lm_spends_aggregate: spend_groupings_v2(where:{account_id: {_eq:$accountId1}, spend_date:{_gte:$lmStartDate, _lte:$lmEndDate}, exclude_aggregate: {_eq: false}}){
    rows{
      spend_amount
    }
  }
`;

export const LIST_k8_CLUSTER_YEARLY_SAVING = `
query listK8ClusterYearlySaving($accountId1: String,$startDate:Datetime, $endDate:Datetime,$yearStartDate:Datetime, $yearEndDate:Datetime, $lmStartDate: Datetime, $lmEndDate: Datetime){
  recommendation_aggregate: recommendation_groupings_v2(where: {account_id: {_eq: $accountId1}, status:{_in:["Open", "Assigned"]}}){
    rows{
      count
      sum_estimated_savings
    }
  }
  ${SPEND_AGGREGATES_FIELDS}
}`;
export const GET_k8_CLUSTER_GROUPINGS = `
query get_k8_cluster_groupings($accountId1: String) {
  k8s_cluster_groupings: k8s_cluster_groupings_v2(where:{account_id:{_eq: $accountId1}}){
    rows{
      account_id
      node_count
      node_cpu_capacity
      node_cpu_allocatable
      node_memory_capacity
      node_memory_allocatable
      node_spot_count
      pod_status_counts
      workload_type_counts
    }
  }
}`;

export const GET_k8_RECOMMENDATION_AGGREGATE = `
query get_k8_recommendation_aggregate($accountId1: String) {
  recommendation_aggregate: recommendation_groupings_v2(where: {account_id: {_eq: $accountId1}, status:{_in:["Open", "InProgess"]}, category: {_in: ["Configuration","RightSizing","K8sSpotRecommendation"]} }){
    rows{
      sum_estimated_savings
      count
    }
  }
}`;

export const GET_k8_SPEND_AGGREGATES = `
query get_k8_spend_aggregates($accountId1: String, $startDate:Datetime, $endDate:Datetime, $lmStartDate:Datetime, $lmEndDate:Datetime, $yearStartDate:Datetime, $yearEndDate:Datetime) {
  ${SPEND_AGGREGATES_FIELDS}
}`;

export const GET_k8_EVENT_GROUPINGS = `
query get_k8_event_groupings($accountId1: String, $todayStartDate: Datetime) {
  events: event_groupings_v2(group_by: ["tenant_id", "account_id", "aggregation_key"], where: {account_id: {_eq: $accountId1}, created_at:{_gte: $todayStartDate}}) {
    rows{
      aggregation_key
      event_count
    }
  }
}`;

export const K8S_CLUSTER_DATA_TREND = `
query listK8ClusterTrend($accountId:String!,$startDate:Datetime,$endDate:Datetime, $dateUnit:String!){ 
  cloudaccount_k8s_aggregate: k8s_metrics_groupings_v2(where:{account_id:{_eq:$accountId}, timestamp:{_between:{_gte:$startDate, _lte:$endDate}}}, column_transformations: [{name: "timestamp", expr: "date_unit", args:[$dateUnit]}]){
    rows{
      tenant_id
      account_id
      timestamp
      total_cpu_allocatable: avg_cpu_request
      avg_cpu_used_node: avg_cpu_used
      avg_memory_used_node:avg_memory_used
      total_memory_allocatable: avg_memory_request
      cost
    }
  }
}`;

export const K8S_CLUSTER_COST_GROUPINGS = `
query listK8ClusterTrend($accountId:String!,$startDate:Datetime!,$endDate:Datetime!, $dateUnit:String!){
  spend_groupings: spend_groupings_v2(where: {exclude_aggregate:{_eq: true}, account_id: {_eq: $accountId}, spend_date: {_between: {_gte: $startDate, _lt: $endDate}}}, order_by: [{column: "spend_date", order: asc}], column_transformations:[{name: "spend_date", expr: "date_unit", args: [$dateUnit]}]){
    rows{
      tenant_id
      account_id
      spend_date
      spend_amount  
    }
  }
}
`;

export const LIST_k8_ISSUES_NAME = `
query list_k8_issues_name($limit:Int, $offset:Int) {
  events: events_v2(where: __WHERE__, order_by: [{column:"starts_at",  order:desc}], limit: $limit, offset: $offset) {
    rows{
      title
      subject_name
      id
      resource_id
      starts_at  
    }
  }
}`;

export const LIST_k8_POD_EXCEPTION = `
query list_k8_pod_exceptions($limit:Int,$aggregationKey: [String!], $startDate: Datetime, $endDate: Datetime) {
  events: events_v2(order_by: [{column: "starts_at", order: desc}],
     where: {_and:[__WHERE__,
      {starts_at: {_gte: $startDate, _lte: $endDate}},
      {aggregation_key: {_in: $aggregationKey}}
    ]},
      limit:$limit) {
    rows {
      cloud_account_id: account_id
      subject_type
      subject_name
      subject_namespace
      starts_at
      id
      cluster
      title
      finding_type
      evidences
    }
  }
}`;

export const LIST_k8_NODE_EXCEPTION = `
query list_k8_node_exceptions($limit:Int,$subjectType: [String!], $startDate: Datetime, $endDate: Datetime) {
  events: events_v2(order_by: [{column: "starts_at", order: desc}],
     where: {_and:[__WHERE__,
      {starts_at: {_gte: $startDate, _lte: $endDate}},
      {subject_type: {_in: $subjectType}}
    ]},
    limit:$limit) {
    rows {
      cloud_account_id: account_id
      subject_type
      subject_name
      subject_namespace
      cluster
      starts_at
      title
      id
      finding_type
      evidences
    }
  }
}`;

export const LIST_k8_NODES = `
query k8s_nodes_list($limit:Int, $offset:Int) {
  k8s_nodes: k8s_nodes_v2(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "node_creation_time", order: desc}]){
    rows {
      name
      is_active
      node_creation_time
      updated_at
      conditions
      node_type
      node_flavor
      node_region
      node_zone
      memory_capacity
      cpu_capacity
      memory_allocatable
      cpu_allocatable
      memory_limits
      cpu_limits
      cloud_resource_id
      external_ip
      internal_ip
      labels
      taints
      cost
      meta
    }
  }
  k8s_nodes_aggregate: k8s_nodes_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
}`;

export const GET_K8S_RESOURCE_COST = `
query get_k8s_resource_cost {
  cloud_resource_details_v2(where: __WHERE__, limit: 1){
    rows {
      resource_cost
    }
  }
}`;

export const LIST_k8_NAMESPACES = `
query k8s_namespace_list($limit: Int, $offset: Int) {
  k8s_namespaces_aggregate: k8s_namespace_groupings_v2(where: {_and: [__WHERE__]}) {
    rows {
      count
    }
  }
  k8s_namespaces: k8s_namespaces_v2(where: __WHERE__, order_by: [{column: "pod_count", order: desc}], limit: $limit, offset: $offset) {
    rows{
      name
      workload_count
      pod_count
      creation_time
    }
  }
}
`;

export const LIST_k8_WORKLOADS = `
query k8s_workloads_list($limit:Int, $offset:Int) {
  k8s_workloads_aggregate: k8s_workload_groupings_v2(where: {_and:[__WHERE__]}){
    rows{
      count
    }
  }
  k8s_workloads:k8s_workloads_v2(where: __WHERE__,  limit:$limit, offset:$offset, order_by: __ORDER_BY__){
    rows{
      namespace
      name
      kind
      is_active
      creation_time
      total_pods
      ready_pods
      cloud_resource_id: resource_id
      cloud_account_id: account_id
      tenant_id
      meta
    }
  }
}
`;

export const LIST_k8_PODS = `
query k8s_pods_list($limit:Int, $offset:Int) {
  k8s_pods_aggregate: k8s_pod_groupings_v2(where: {_and:[__WHERE__]}){
    rows{
      count
    }
  }
  k8s_pods: k8s_pods_v2(where: __WHERE__,  limit:$limit, offset:$offset, order_by: [{column: "creation_time", order: desc}]){
    rows{
      id: resource_id
      namespace
      name
      status
      is_active
      node_name
      workload_name
      workload_type
      timestamp: creation_time
      restart_count
    }
  }
}`;

export const LIST_k8_WORKLOAD_NAMESPACES = `
query k8s_workload_namespace_list($accountId: String!) {
  k8s_namespaces: k8s_namespaces_v2(where: {account_id: {_eq: $accountId}, is_active: {_eq: true}}, order_by: [{column: "name", order: asc}]){
    rows{
      namespace_name: name
    }
  }
}`;

export const LIST_NAMESPACES = `
query ListNamespaces($limit: Int) {
  k8s_namespaces_v2(where: __WHERE__, order_by: [{column: "name", order: asc}], limit: $limit){
    rows{
      namespace_name: name
      account_id
    }
  }
}`;

export const LIST_k8_POD_GROUPING = `
query k8s_pod_groupings($limit:Int) {
  k8s_pod_groupings: k8s_metrics_groupings_v2(where:__WHERE__, limit:$limit){
    rows{
      account_id
      tenant_id
      timestamp
      pod_cost: cost
      avg_cpu_used
      avg_cpu_request
      avg_memory_used
      avg_memory_request
    }
  }
}`;

export const RESOLVE_EVENT_RECORD = `
query ResolveEventRecord($id:String!) {
  events: events_v2(where: {id: {_eq:$id}}) {
    rows {
      evidences
      id
      subject_name
      starts_at
      subject_namespace
      cluster
      subject_node
      subject_owner
      subject_type
      title
      priority
      description
      aggregation_key
      service_key
      cloud_resource_id: resource_id
      cloud_account_id: account_id
      status
      fingerprint
      source
      labels
      urgency
      nb_status
      snoozed_until
      computed_priority
      computed_score
    }
  }
}
`;
export const EVENT_SIMILAR_AND_INSIGHTS = `
query eventsSimilarAndInsightData($id:String!,$aggregation_key: String!, $service_key: String!, $sdate_1_days:Datetime!,$sdate_7_days:Datetime!,$endDate: Datetime!) {
  similar_issue_in_7_days: event_groupings_v2(where: {starts_at: {_gte: $sdate_7_days, _lte: $endDate}, aggregation_key: {_eq: $aggregation_key}, id: {_neq:$id}}) {
    rows {
      event_count
    }
  }
  similar_issue_on_same_service_in_7days: event_groupings_v2(where: {service_key: {_eq: $service_key}, aggregation_key: {_eq: $aggregation_key}, id: {_neq: $id}, starts_at: {_gte: $sdate_7_days, _lte: $endDate}}) {
    rows {
      event_count
    }
  }
  similar_issue_on_same_service_in_1days: event_groupings_v2(where: {service_key: {_eq: $service_key}, aggregation_key: {_eq: $aggregation_key}, id: {_neq: $id}, starts_at: {_gte: $sdate_1_days, _lte: $endDate}}) {
    rows {
      event_count
    }
  }
  corelatedDeployment: event_groupings_v2(where: {service_key: {_eq: $service_key}, aggregation_key: {_eq: "ConfigurationChange/KubernetesResource/Change"}, starts_at: {_gte: $sdate_1_days, _lte: $endDate}}) {
    rows {
      event_count
    }
  }
}`;

export const K8S_POD_WORKLOAD_TYPE = `
query ListK8PodWorkloadType {
  k8s_pods: k8s_pod_groupings_v2(where:__WHERE__){
    rows{
   		workload_type   
    }
  }
}
`;

export const LIST_K8S_WORKLOAD_NAMES = `
query ListK8sWorkloadNames {
  k8s_workloads: k8s_workload_groupings_v2(where:__WHERE__){
    rows{
      name
    }
  }
}
`;

export const K8S_POD_STATUS_TYPE = `
query ListK8PodStatusType {
  k8s_pods: k8s_pod_groupings_v2(where:__WHERE__){
    rows{
   		status   
    }
  }
}
`;

export const K8S_WORKLOAD_WORKLOAD_TYPE = `
query ListK8WorkloadWorkloadType {
  k8s_workloads: k8s_workload_groupings_v2(where:__WHERE__){
    rows{
 		  workload_type:kind
    }
  }
}
`;

export const K8S_POD_DETAILS = `
query getPodDetails {
  cloud_resourses: cloud_resource_v2(where: __WHERE__, limit: 1) {
    rows {
      id
      meta
      last_seen
      is_active
      first_seen
      tags
      account
      name
      created_at
      service_name
      account_name
    }
  }
}
`;

export const K8S_METRICS_GROUPINGS = `
query MetricsGroupings($limit:Int!, $dateUnit: String!) {
  cloud_resource_metrics_groupings: metric_groupings_v2(where:__WHERE__, column_transformations: [{name: "timestamp", expr: "date_unit", args:[$dateUnit]}], limit: $limit){
    rows{
      tenant_id
      account_id
      timestamp
      metric
      avg_value
    }
  }
}
`;

export const K8S_POD_GROUPINGS = `
query PodGroupings($limit:Int!, $dateUnit: String!) {
  k8s_pod_groupings: k8s_metrics_groupings_v2(where:__WHERE__, limit:$limit, column_transformations: [{name: "timestamp", expr: "date_unit", args:[$dateUnit]}]){
    rows {
      tenant_id
      cost
      avg_efficiency
      max_efficiency
      avg_cpu_used
      max_cpu_used
      avg_memory_used
      max_memory_used
      avg_cpu_request
      max_cpu_request
      avg_memory_request
      max_memory_request
      avg_cpu_efficiency
      max_cpu_efficiency
      avg_ram_efficiency
      max_ram_efficiency
      sum_ingress
      sum_egress
      __ADDITIONAL_COLUMNS__
    }
  }
}
`;

export const GET_K8s_INSIGHTS = `
query GetK8sInsights {
  insight_v2(where:__WHERE__) {
    rows {
      id
      title
      type
      unique_id
      resource_id
      status
      source
      account_id
    }
  }
}
`;

export const SINGLE_CONFIG_AUTO_PILOT = `
mutation singleConfigAuotPilot($data: auto_optimize_insert_one!) {
  autopilot_insert_one: auto_optimize_insert_one(arg1: $data) {
    id
  }
}
`;

export const AGENT_HEALTH = `
query GetAgentHealth {
  agent: get_agent_health_v2( where:__WHERE__) {
    rows {
      cloud_account_id
      type
      version
      status_message
      status
      last_connected_at
      k8s_version
      k8s_provider
      connection_status
    }
  }
}`;

const RELAY_FORWARD_REQUEST = `
mutation RelayForwardRequest($body: jsonb!, $no_sinks: Boolean, $cache: Boolean, $track_history: Boolean) {
  relay_forward_request(body: $body, no_sinks: $no_sinks, cache: $cache, track_history: $track_history) {
    data
  }
}`;

const TRIGGER_CLOUD_ACCOUNT_SYNC = `
mutation TriggerCloudAccountSync($account_id: String!) {
  trigger_cloud_account_sync(account_id: $account_id) {
    success
    message
  }
}`;

export const REPLICA_RIGHT_SIZING_SINGLE_WORKLOAD = `
query getMetricFromML($account:String!,$deployment:String!,$namespace:String!) {
  get_metrics_from_ml: ml_get_metrics(account: $account, deployment: $deployment, namespace: $namespace) {
    data
  }
}`;

export const GET_CURRENT_REPLICA_BY_IDS = `
query GetCurrentReplicaByResourceIds {
  cloud_resourses: cloud_resource_v2(where: __WHERE__) {
    rows {
      meta
      id
      name
    }
  }
}
`;

export const GET_KNOWLEDGE_BASE = `
query GetKnowledgeBase($rulename: String!) {
  knowledge_base_v2(where: {rule_name: {_eq: $rulename}}) {
    rows {
      impact
      diagnosis
      description
      mitigation
      rule_name
    }
  }
}
`;

export const GET_NAMESPACES = `
query GetK8sNamespaces {
  k8s_namespaces: k8s_namespaces_v2(where: {is_active: {_eq: true}}, order_by: [{column: "name", order: asc}]) {
    rows {
      namespace_name: name
      cloud_account_id: account_id    
    }
  }
}
`;

export const LIST_ALL_WORKLOADS = `
query ListAllWorkloads {
  k8s_workloads: k8s_workloads_v2(where: __WHERE__, order_by: [{column: "name", order: asc}], limit: 500){
    rows{
      namespace
      name
      kind
      is_active
      creation_time
      cloud_resource_id: resource_id    
    }
  }
}
`;

export const GET_CLUSTER_EVENTS = `
query getClusterEvents($accountId1: String!, $todayEnd: Datetime, $yesterdayStart: Datetime, $yesterdayEnd: Datetime) {
  pod_event_count_today: event_groupings_v2(where: {account_id: {_eq: $accountId1}, subject_type: {_eq: "pod"}, created_at: {_lte: $todayEnd, _gt: $yesterdayEnd}}) {
    rows {
      event_count
    }
  }
  pod_event_count_yesterday: event_groupings_v2(where: {account_id: {_eq: $accountId1}, subject_type: {_eq: "pod"}, created_at: {_lte: $yesterdayEnd, _gt: $yesterdayStart}}) {
    rows {
      event_count
    }
  }
  node_event_count_today: event_groupings_v2(where: {account_id: {_eq: $accountId1}, subject_type: {_eq: "node"}, created_at: {_lte: $todayEnd, _gt: $yesterdayEnd}}) {
    rows {
      event_count
    }
  }
  node_event_count_yesterday: event_groupings_v2(where: {account_id: {_eq: $accountId1}, subject_type: {_eq: "node"}, created_at: {_lte: $yesterdayEnd, _gt: $yesterdayStart}}) {
    rows {
      event_count
    }
  }
}
`;

export const GENEREATE_AI_RECOMMENDATION = `
query GenerateAIRecommendation($eventId: String!, $accountId: String!, $recommendationType: String!, $regenerate: Boolean) {
  generate_ai_recommendation: ai_get_recommendation(account_id: $accountId, event_id: $eventId, recommendation_type: $recommendationType, regenerate: $regenerate) {
    data
  }
}
`;

export const NB_LATEST_VERSIONS = `
query NBVersions{
  nb_versions{
      agent_version_latest
  }
}
`;

export const ISSUE_EVENT_COUNT = `
query IssueEventCount {
  event_groupings_v2(where: __WHERE__, column_transformations: {expr: "distinct", name: "subject_type"}) {
    rows {
      __COLS__
    }
  }
}
`;

export const priorityFilter = [
  { value: 'HIGH', label: 'High' },
  { value: 'MEDIUM', label: 'Medium' },
  { value: 'DEBUG', label: 'Debug' },
  { value: 'LOW', label: 'Low' },
  { value: 'INFO', label: 'Info' },
];

export const statusFilter = [
  { value: 'FIRING', label: 'Firing' },
  { value: 'RESOLVED', label: 'Resolved' },
  { value: 'CLOSED', label: 'Closed' },
];

export const GET_RESOURCE_ATTRIBUTES = `
query GetResourceAttributes {
  cloud_resource_attributes: cloud_resource_attributes_v2(where: __WHERE__) {
    rows {
      id
      name
      value
    }
  }
}`;

export const UPSERT_RESOURCE_ATTRIBUTES = `
mutation UpsertResourceAttributes($objects: [cloud_resource_attribute_item!]!) {
  cloud_resource_attributes_upsert(objects: $objects) {
    affected_rows
  }
}`;

function buildEventFilterParams(query: any) {
  const filterParams: any = {};
  const and: any[] = [];
  query = Object.fromEntries(Object.entries(query).filter(([_key, value]) => value !== '__ALL__'));
  if (query?.account_id) {
    if (Array.isArray(query['account_id'])) {
      filterParams['account_id'] = { _in: query['account_id'] };
    } else {
      filterParams['account_id'] = { _eq: query['account_id'] };
    }
  }
  if (query?.subject_namespace) {
    if (Array.isArray(query['subject_namespace'])) {
      filterParams['subject_namespace'] = { _in: query['subject_namespace'] };
    } else {
      filterParams['subject_namespace'] = { _eq: query['subject_namespace'] };
    }
  }
  if (query?.subject_type) {
    if (Array.isArray(query['subject_type'])) {
      filterParams['subject_type'] = { _in: query['subject_type'] };
    } else {
      filterParams['subject_type'] = { _eq: query['subject_type'] };
    }
  }
  const exactSubjectNameSearch = query?.exact_subject_name_search || false;
  if (query?.subject_name && !exactSubjectNameSearch) {
    if (Array.isArray(query['subject_name'])) {
      const subjectNames: any[] = [];
      for (const subjectName of query['subject_name']) {
        if (subjectName) {
          subjectNames.push({ subject_name: { _ilike: `${subjectName}%` } });
        }
      }
      and.push({ _or: subjectNames });
    } else {
      filterParams['subject_name'] = { _like: query['subject_name'] + '%' };
    }
  } else if (query?.subject_name && exactSubjectNameSearch) {
    if (Array.isArray(query['subject_name'])) {
      const subjectNames: any[] = [];
      for (const subjectName of query['subject_name']) {
        if (subjectName) {
          subjectNames.push({ subject_name: { _eq: `${subjectName}` } });
        }
      }
      and.push({ _or: subjectNames });
    } else {
      filterParams['subject_name'] = { _eq: query['subject_name'] };
    }
  }
  if (query?.cluster) {
    if (Array.isArray(query['cluster'])) {
      filterParams['cluster'] = { _in: query['cluster'] };
    } else {
      filterParams['cluster'] = { _eq: query['cluster'] };
    }
  }
  if (query?.title) {
    if (Array.isArray(query['title'])) {
      filterParams['title'] = { _in: query['title'] };
    } else {
      filterParams['title'] = { _eq: query['title'] };
    }
  }
  if (query?.finding_type) {
    if (Array.isArray(query['finding_type'])) {
      filterParams['finding_type'] = { _in: query['finding_type'] };
    } else {
      filterParams['finding_type'] = { _eq: query['finding_type'] };
    }
  }
  if (query?.finding_id) {
    if (Array.isArray(query['finding_id'])) {
      filterParams['finding_id'] = { _in: query['finding_id'] };
    } else {
      filterParams['finding_id'] = { _eq: query['finding_id'] };
    }
  }
  if (query?.aggregation_key) {
    if (Array.isArray(query['aggregation_key'])) {
      filterParams['aggregation_key'] = { _in: query['aggregation_key'] };
    } else {
      filterParams['aggregation_key'] = { _eq: query['aggregation_key'] };
    }
  }
  if (query['fingerprint'] && Array.isArray(query['fingerprint'])) {
    filterParams['fingerprint'] = { _in: query['fingerprint'] };
  } else if (query['fingerprint']) {
    filterParams['fingerprint'] = { _eq: query['fingerprint'] };
  }
  if (query?.priority) {
    if (Array.isArray(query['priority'])) {
      filterParams['priority'] = { _in: query['priority'] };
    } else {
      filterParams['priority'] = { _eq: query['priority'] };
    }
  }
  if (query?.status) {
    if (Array.isArray(query['status'])) {
      filterParams['status'] = { _in: query['status'] };
    } else {
      filterParams['status'] = { _eq: query['status'] };
    }
  }
  if (query?.resource_id) {
    if (Array.isArray(query['resource_id'])) {
      filterParams['resource_id'] = { _in: query['resource_id'] };
    } else {
      filterParams['resource_id'] = { _eq: query['resource_id'] };
    }
  }
  if (query?.searchByLabel) {
    filterParams['labels'] = { _contains: query.searchByLabel };
  }
  if (query?.resource_ids) {
    filterParams['resource_id'] = { _in: query.resource_ids };
  }
  if (query?.source) {
    if (Array.isArray(query.source)) {
      filterParams['source'] = { _in: query.source };
    } else {
      filterParams['source'] = { _eq: query.source };
    }
  }
  if (query?.nb_status) {
    if (Array.isArray(query['nb_status'])) {
      filterParams['nb_status'] = { _in: query['nb_status'] };
    } else {
      filterParams['nb_status'] = { _eq: query['nb_status'] };
    }
  }
  if (Array.isArray(query?.eventIds)) {
    filterParams['id'] = { _in: query.eventIds };
  }
  if (query?.is_new_issue !== undefined) {
    filterParams['is_new_issue'] = { _eq: query.is_new_issue };
  }

  const endDate = query.end_date || query.endDate || getEndOfMonth(new Date());
  const startDate = query.start_date || query.startDate || getStartOfMonth(new Date());

  and.push({ created_at: { _gte: startDate.toISOString() } });
  and.push({ created_at: { _lte: endDate.toISOString() } });
  filterParams['_and'] = and;
  if (query.timeFilter === false) {
    delete filterParams['_and'];
  }

  return filterParams;
}

const apiKubernetes = {
  async getLatestVersions() {
    try {
      const response = await queryGraphQL(NB_LATEST_VERSIONS, 'NBVersions', {});
      return {
        data: response?.data?.data,
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async listK8sWorkloadWorkloadType({ accountId }: { accountId?: string }) {
    try {
      const where: any = {};
      if (accountId) {
        where['account_id'] = { _eq: accountId };
      }
      const query = K8S_WORKLOAD_WORKLOAD_TYPE.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListK8WorkloadWorkloadType', {});
      return {
        data: {
          k8s_workloads: response?.data?.data?.k8s_workloads?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async listK8sPodWorkloadType({ accountId }: { accountId?: string }) {
    try {
      const where: any = {};
      if (accountId) {
        where['account_id'] = { _eq: accountId };
      }
      where['_and'] = [{ workload_type: { _is_null: false } }, { workload_type: { _neq: '' } }];
      const query = K8S_POD_WORKLOAD_TYPE.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListK8PodWorkloadType', {});
      return {
        data: {
          k8s_pods: response?.data?.data?.k8s_pods?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sWorkloadNames({ accountId, namespace }: { accountId?: string; namespace?: string }) {
    try {
      const where: any = {};
      if (accountId) {
        where['account_id'] = { _eq: accountId };
      }
      if (namespace) {
        where['namespace'] = { _eq: namespace };
      }
      where['_and'] = [{ name: { _is_null: false } }, { name: { _neq: '' } }, { is_active: { _eq: true } }];
      const query = LIST_K8S_WORKLOAD_NAMES.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListK8sWorkloadNames', {});
      const rows = response?.data?.data?.k8s_workloads?.rows || [];
      return {
        data: {
          workloadNames: rows.map((r: any) => r.name),
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getAgentHealth({ accountId, type }: { accountId?: string; type?: string }) {
    try {
      const where: any = {};
      if (accountId) {
        where['cloud_account_id'] = { _eq: accountId };
      }
      if (type && type.toLowerCase() !== 'k8s') {
        const lowerCaseType = type.toLowerCase();
        const typeMap: { [key: string]: string } = {
          aws: 'AWS',
          gcp: 'GCP',
          azure: 'Azure',
        };
        where['type'] = { _eq: typeMap[lowerCaseType] || type };
      } else if (type?.toLowerCase() === 'k8s') {
        where['type'] = { _eq: 'k8s' };
      }
      const query = AGENT_HEALTH.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'GetAgentHealth', {});
      const rows = response?.data?.data?.agent?.rows || [];
      const data = rows.map((row: any) => ({
        ...row,
        connection_status: typeof row.connection_status === 'string' ? safeJSONParse(row.connection_status) || {} : row.connection_status || {},
      }));
      return {
        data,
        error: response?.data?.errors,
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async listK8sPodStatusType({ accountId }: { accountId?: string }) {
    try {
      const where: any = {};
      if (accountId) {
        where['account_id'] = { _eq: accountId };
      }
      const query = K8S_POD_STATUS_TYPE.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'ListK8PodStatusType', {});
      return {
        data: {
          k8s_pods: response?.data?.data?.k8s_pods?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async resolveEventRecord(id: string, accountId?: string) {
    if (accountId === 'demo') {
      const troubleshootingDemo = await getMockData('k8s-investigation');
      return troubleshootingDemo['52c4e53d-47d2-4e76-8a05-dfa8c83447c'];
    }
    try {
      const response = await queryGraphQL(RESOLVE_EVENT_RECORD, 'ResolveEventRecord', {
        id: id,
      });
      const rows = (response?.data?.data?.events?.rows || []).map((row: any) => ({
        ...row,
        evidences: typeof row.evidences === 'string' ? JSON.parse(row.evidences) : row.evidences,
        labels: typeof row.labels === 'string' ? JSON.parse(row.labels) : row.labels,
      }));
      return {
        data: { events: rows },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getEventsSimilarDataAndInsights(aggregation_key: string, service_key: string, id: string, end_date: string | null = null) {
    try {
      let parsedEndDate: Date | null = null;

      if (end_date) {
        parsedEndDate = new Date(end_date);
        if (isNaN(parsedEndDate.getTime())) {
          throw new Error('Invalid date format');
        }
      } else {
        // If end_date is null, you can use the current date, or handle it as needed.
        // For this example, we'll use the current date.
        parsedEndDate = new Date();
      }
      const sdate_1_days = new Date(parsedEndDate);
      sdate_1_days.setDate(parsedEndDate.getDate() - 1);

      const sdate_7_days = new Date(parsedEndDate);
      sdate_7_days.setDate(parsedEndDate.getDate() - 7);

      const response = await queryGraphQL(EVENT_SIMILAR_AND_INSIGHTS, 'eventsSimilarAndInsightData', {
        id: id,
        aggregation_key: aggregation_key,
        service_key: service_key,
        sdate_1_days: getYesterday(new Date()),
        sdate_7_days: getLast7Days(new Date()),
        endDate: parsedEndDate,
      });
      const d = response?.data?.data;
      return {
        data: {
          similar_issue_in_7_days: { aggregate: { count: d?.similar_issue_in_7_days?.rows?.[0]?.event_count || 0 } },
          similar_issue_on_same_service_in_7days: { aggregate: { count: d?.similar_issue_on_same_service_in_7days?.rows?.[0]?.event_count || 0 } },
          similar_issue_on_same_service_in_1days: { aggregate: { count: d?.similar_issue_on_same_service_in_1days?.rows?.[0]?.event_count || 0 } },
          corelatedDeployment: { aggregate: { count: d?.corelatedDeployment?.rows?.[0]?.event_count || 0 } },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async listk8ClusterData() {
    try {
      const currentDate = new Date();
      const lastMonthStart = new Date(currentDate);
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);
      const accountGroups: Record<string, any> = {};

      const response = await queryGraphQL(LIST_k8_CLUSTER_DATA, 'listK8ClusterData', {});
      const cloudAccounts = response?.data?.data?.cloud_accounts?.rows || [];
      const k8sClusterGroupings = response?.data?.data?.k8s_cluster_groupings?.rows || [];
      k8sClusterGroupings.forEach((group: any) => {
        accountGroups[group.account_id] = group;
      });
      cloudAccounts.forEach((acc: any) => {
        const account = acc;
        const accountGroup = accountGroups[acc.id] || {};
        acc.account_id = acc.id;
        acc.account_name = account?.account_name;
        acc.node_count = accountGroup.node_count ?? 0;
        acc.spot_node_count = accountGroup.node_spot_count ?? 0;
        acc.ondemand_node_count = acc.node_count - acc.spot_node_count;
        acc.pod_status_counts = accountGroup.pod_status_counts ? JSON.parse(accountGroup.pod_status_counts) : {};
        acc.workload_type_counts = accountGroup.workload_type_counts ? JSON.parse(accountGroup.workload_type_counts) : {};
      });
      return {
        cloudaccount_k8s_aggregate: cloudAccounts,
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async listk8ClusterEventsData(accountId: string) {
    if (accountId === 'demo') return null;
    try {
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);
      const response = await queryGraphQL(GET_CLUSTER_EVENTS, 'getClusterEvents', {
        todayEnd: getEndOfDay(new Date()),
        yesterdayStart: getStartOfDay(getYesterday(new Date())),
        yesterdayEnd: getEndOfDay(getYesterday(new Date())),
        accountId1: accountId,
      });
      return {
        pod_issue_count: response?.data?.data?.pod_event_count_today?.rows?.[0]?.event_count || 0,
        old_pod_issue_count: response?.data?.data?.pod_event_count_yesterday?.rows?.[0]?.event_count || 0,
        node_issue_count: response?.data?.data?.node_event_count_today?.rows?.[0]?.event_count || 0,
        old_node_issue_count: response?.data?.data?.node_event_count_yesterday?.rows?.[0]?.event_count || 0,
      };
    } catch (error) {
      return error;
    }
  },
  async listk8ClustersYearlySaving(accountId: string) {
    if (accountId === 'demo') return null;
    try {
      const currentDate = new Date();
      const lastMonthStart = new Date();
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);
      const response = await queryGraphQL(LIST_k8_CLUSTER_YEARLY_SAVING, 'listK8ClusterYearlySaving', {
        accountId1: accountId,
        startDate: getStartOfMonth(currentDate),
        endDate: getEndOfMonth(currentDate),
        yearStartDate: getStartOfYear(currentDate),
        yearEndDate: getEndOfYear(currentDate),
        lmStartDate: getStartOfMonth(lastMonthStart),
        lmEndDate: getEndOfMonth(lastMonthStart),
      });
      return {
        data: {
          yearly_recommendation_saving: (response?.data?.data?.recommendation_aggregate?.rows?.[0].sum_estimated_savings || 0) * 12,
          current_year_projected_spend: getExpectedYearlyExpense(
            getBudgetExpectedMonthlyExpense(response?.data?.data?.spends_aggregate?.rows?.[0]?.spend_amount || 0),
            response?.data?.data?.yearly_spends_aggregate?.rows?.[0]?.spend_amount || 0
          ),
          current_month_projected_spend: getBudgetExpectedMonthlyExpense(response?.data?.data?.spends_aggregate?.rows?.[0]?.spend_amount || 0),
          previous_cost: response?.data?.data?.lm_spends_aggregate?.rows?.[0]?.spend_amount || 0,
          mtd_cost: response?.data?.data?.spends_aggregate?.rows?.[0]?.spend_amount || 0,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async getk8ClusterData(accountId: string) {
    const currentDate = new Date();
    const lastMonthStart = new Date();
    if (accountId == 'demo') {
      const dashboardDemo = await getMockData('k8s-dashboard');
      return {
        data: {
          cluster_data: dashboardDemo.cluster_data.data.cloudaccount_k8s_aggregate?.map((d: any) => {
            d.node_count = dashboardDemo.cluster_data.data.all_node_count?.aggregate?.count || 0;
            d.spot_node_count = dashboardDemo.cluster_data.data.spot_node_count?.aggregate?.count || 0;
            d.ondemand_node_count = d.node_count - d.spot_node_count;
            d.pod_count = dashboardDemo.cluster_data.data.pod_count?.aggregate?.count || 0;
            d.failed_pod_count = dashboardDemo.cluster_data.data.failed_pod_count?.aggregate?.count || 0;
            d.total_memory_allocated = d?.avg_memory_used_node || 0;
            d.total_memory_capacity = dashboardDemo.cluster_data.data.all_node_count?.aggregate?.sum?.memory_capacity || 0;
            d.total_cpu_allocated = d?.avg_cpu_used_node || 0;
            d.total_cpu_capacity = dashboardDemo.cluster_data.data.all_node_count?.aggregate?.sum?.cpu_capacity || 0;
            d.replicaSet = dashboardDemo.cluster_data.data.workload_replicaSet?.aggregate?.count || 0;
            d.statefulSet = dashboardDemo.cluster_data.data.workload_statefulSet?.aggregate?.count || 0;
            d.daemonSet = dashboardDemo.cluster_data.data.workload_daemonSet?.aggregate?.count || 0;
            d.deployment = dashboardDemo.cluster_data.data.workload_deployement?.aggregate?.count || 0;
            d.job = dashboardDemo.cluster_data.data.workload_job?.aggregate?.count || 0;
            d.event = dashboardDemo.cluster_data.data.events;

            return d;
          })?.[0],
          last_month_spend: dashboardDemo.cluster_data.data.lm_spends_aggregate?.aggregate?.sum?.amount || 0,
          current_month_spend: dashboardDemo.cluster_data.data.spends_aggregate?.aggregate?.sum?.amount || 0,
          current_month_projected_spend: getBudgetExpectedMonthlyExpense(
            dashboardDemo.cluster_data.data.spends_aggregate?.aggregate?.sum?.amount || 0
          ),
          recommended_saving: dashboardDemo.cluster_data.data.recommendation_aggregate?.aggregate?.sum?.estimated_savings || 0,
          yearly_recommendation_saving: (dashboardDemo.cluster_data.data.recommendation_aggregate?.aggregate?.sum?.estimated_savings || 0) * 12,
          current_month_avg_daily_cost: (dashboardDemo.cluster_data.data.spends_aggregate?.aggregate?.sum?.amount || 0) / currentDate.getDate(),
          last_month_avg_daily_cost:
            (dashboardDemo.cluster_data.data.lm_spends_aggregate?.aggregate?.sum?.amount || 0) / getEndOfMonth(lastMonthStart).getDate(),
          current_year_spend: dashboardDemo.cluster_data.data.yearly_spends_aggregate?.aggregate?.sum?.amount || 0,
          current_year_projected_spend: getExpectedYearlyExpense(
            getBudgetExpectedMonthlyExpense(dashboardDemo.cluster_data.data.spends_aggregate?.aggregate?.sum?.amount || 0),
            dashboardDemo.cluster_data.data.yearly_spends_aggregate?.aggregate?.sum?.amount || 0
          ),
          total_recommendations: dashboardDemo.cluster_data.data.recommendation_aggregate?.count || 0,
        },
      };
    }

    try {
      lastMonthStart.setMonth(lastMonthStart.getMonth() - 1);

      const [clusterRes, recommendationRes, spendRes, eventRes] = await Promise.all([
        queryGraphQL(GET_k8_CLUSTER_GROUPINGS, 'get_k8_cluster_groupings', {
          accountId1: accountId,
        }),
        queryGraphQL(GET_k8_RECOMMENDATION_AGGREGATE, 'get_k8_recommendation_aggregate', {
          accountId1: accountId,
        }),
        queryGraphQL(GET_k8_SPEND_AGGREGATES, 'get_k8_spend_aggregates', {
          accountId1: accountId,
          startDate: getStartOfMonth(currentDate),
          endDate: getEndOfMonth(currentDate),
          lmStartDate: getStartOfMonth(lastMonthStart),
          lmEndDate: getEndOfMonth(lastMonthStart),
          yearStartDate: getStartOfYear(currentDate),
          yearEndDate: getEndOfYear(currentDate),
        }),
        queryGraphQL(GET_k8_EVENT_GROUPINGS, 'get_k8_event_groupings', {
          accountId1: accountId,
          todayStartDate: getStartOfDay(currentDate),
        }),
      ]);

      const hasErrors = clusterRes?.data?.errors || recommendationRes?.data?.errors || spendRes?.data?.errors || eventRes?.data?.errors;
      if (!hasErrors) {
        const cluster = clusterRes?.data?.data?.k8s_cluster_groupings?.rows?.[0];
        const recommendation = recommendationRes?.data?.data?.recommendation_aggregate?.rows?.[0];
        const spendData = spendRes?.data?.data;
        const currentMonthSpend = spendData?.spends_aggregate?.rows?.[0]?.spend_amount || 0;
        const lastMonthSpend = spendData?.lm_spends_aggregate?.rows?.[0]?.spend_amount || 0;
        const yearlySpend = spendData?.yearly_spends_aggregate?.rows?.[0]?.spend_amount || 0;
        const recommendedSaving = recommendation?.sum_estimated_savings || 0;
        const nodeCount = cluster?.node_count ?? 0;
        const spotCount = cluster?.node_spot_count ?? 0;

        return {
          data: {
            cluster_data: {
              node_count: nodeCount,
              spot_node_count: spotCount,
              ondemand_node_count: nodeCount - spotCount,
              pod_status_counts: cluster?.pod_status_counts ? JSON.parse(cluster.pod_status_counts) : {},
              workload_type_counts: cluster?.workload_type_counts ? JSON.parse(cluster.workload_type_counts) : {},
              event: eventRes?.data?.data?.events?.rows,
            },
            last_month_spend: lastMonthSpend,
            current_month_spend: currentMonthSpend,
            current_month_projected_spend: getBudgetExpectedMonthlyExpense(currentMonthSpend),
            recommended_saving: recommendedSaving,
            yearly_recommendation_saving: recommendedSaving * 12,
            total_recommendations: recommendation?.count || 0,
            current_month_avg_daily_cost: currentMonthSpend / currentDate.getDate(),
            last_month_avg_daily_cost: lastMonthSpend / getEndOfMonth(lastMonthStart).getDate(),
            current_year_spend: yearlySpend,
            current_year_projected_spend: getExpectedYearlyExpense(getBudgetExpectedMonthlyExpense(currentMonthSpend), yearlySpend),
          },
        };
      }
      return { errors: hasErrors };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getk8ClusterTrendData(accountId: string, start_date: Date | null = null, end_date: Date | null = null, dateUnit = 'day') {
    if (accountId == 'demo') {
      const dashboardDemo = await getMockData('k8s-dashboard');
      return {
        data: dashboardDemo.listK8ClusterTrend.data,
      };
    }
    try {
      const response = await queryGraphQL(K8S_CLUSTER_DATA_TREND, 'listK8ClusterTrend', {
        accountId: accountId,
        startDate: start_date || getStartOfMonth(new Date()),
        endDate: end_date || getEndOfDay(new Date()),
        dateUnit: dateUnit,
      });
      return {
        data: {
          cloudaccount_k8s_aggregate: response?.data?.data?.cloudaccount_k8s_aggregate?.rows ?? [],
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sClusterCostTrendData(accountId: string, start_date: Date | null = null, end_date: Date | null = null, dateUnit = 'day') {
    if (accountId == 'demo') {
      const dashboardDemo = await getMockData('k8s-dashboard');
      return {
        data: dashboardDemo.spend_groupings.data,
      };
    }
    try {
      const response = await queryGraphQL(K8S_CLUSTER_COST_GROUPINGS, 'listK8ClusterTrend', {
        accountId: accountId,
        startDate: start_date || getStartOfMonth(new Date()),
        endDate: end_date || getEndOfDay(new Date()),
        dateUnit: dateUnit,
      });
      return {
        data: {
          spend_groupings: response?.data?.data?.spend_groupings?.rows || [],
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sEvents(
    limit = 10,
    offset = 0,
    query: any = {},
    cols: Array<string> = [
      'subject_type',
      'subject_name',
      'subject_namespace',
      'starts_at',
      'title',
      'finding_type',
      'cluster',
      'labels',
      'status',
      'aggregation_key',
      'priority',
      'computed_score',
      'computed_priority',
      'score_factors',
      'score_confidence',
    ]
  ) {
    if (query?.account_id == 'demo') {
      const eventDemoData: any = await getMockData('k8s-events');
      if (query?.subject_type == 'pod' && query?.aggregation_key == 'pod_oom_killer_enricher') {
        return {
          data: eventDemoData.PodErrors.pod_oom_killer_enricher.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key == 'image_pull_backoff_reporter') {
        return {
          data: eventDemoData.PodErrors.image_pull_backoff_reporter.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key == 'KubePodCrashLooping') {
        return {
          data: eventDemoData.PodErrors.KubePodCrashLooping.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key == 'PodMemoryReachingLimit') {
        return {
          data: eventDemoData.PodErrors.PodMemoryReachingLimit.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key == 'Kubernetes Warning Event') {
        return {
          data: eventDemoData.PodErrors.KubernetesWarningEvent.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key == 'KubeDeploymentReplicasMismatch') {
        return {
          data: eventDemoData.PodErrors.KubeDeploymentReplicasMismatch.list_k8_issues_data.data,
        };
      } else if (query?.subject_name == 'notifications-7b465bc6f7-s5zht') {
        return {
          data: eventDemoData.ApplicationLevelErrors['notifications-7b465bc6f7-s5zht'].data,
        };
      } else if (query?.subject_name == 'notifications-6bcd9698d6-fzv4f') {
        return {
          data: eventDemoData.ApplicationLevelErrors['notifications-6bcd9698d6-fzv4f'].data,
        };
      } else if (query?.subject_name == 'auto-pilot-worker-6c7f696dcd-2jsl4') {
        return {
          data: eventDemoData.ApplicationLevelErrors['auto-pilot-worker-6c7f696dcd-2jsl4'].data,
        };
      } else if (query?.subject_name == 'hasura-c59f5bf8c-p9n2h') {
        return {
          data: eventDemoData.ApplicationLevelErrors['hasura-c59f5bf8c-p9n2h'].data,
        };
      } else if (query?.subject_name == 'auto-pilot-worker-7f85c49979-vlp9g') {
        return {
          data: eventDemoData.ApplicationLevelErrors['auto-pilot-worker-7f85c49979-vlp9g'].data,
        };
      } else if (query?.subject_name == 'notifications-785b9868d-g64n4') {
        return {
          data: eventDemoData.ApplicationLevelErrors['notifications-785b9868d-g64n4'].data,
        };
      } else if (query?.subject_name == 'hasura-c59f5bf8c-8pbwm') {
        return {
          data: eventDemoData.ApplicationLevelErrors['hasura-c59f5bf8c-8pbwm'].data,
        };
      } else if (query?.subject_type == 'node') {
        return {
          data: eventDemoData.NodeErrors.list_k8_issues_data.data,
        };
      } else if (query?.aggregation_key?.includes('ApplicationAPIFailures')) {
        return {
          data: eventDemoData.ApplicationErrors.ApiErrors.data,
        };
      } else if (query?.aggregation_key?.includes('HighErrorCriticalLogs')) {
        return {
          data: eventDemoData.ApplicationErrors.LogErrors.data,
        };
      } else if (Array.isArray(query?.aggregation_key)) {
        return {
          data: eventDemoData.ApplicationErrors.AllErrors.list_k8_issues_data.data,
        };
      }
      return {
        data: eventDemoData.AllEvents.list_k8_issues_data.data,
      };
    }

    try {
      const filterParams = buildEventFilterParams(query);

      // Build order_by clause
      const sortColumn = query?.sort_by || 'created_at';
      const sortOrder = query?.sort_order || 'desc';
      const orderByClause = `[{column: "${sortColumn}", order: ${sortOrder}}]`;

      // Conditionally include expensive JOIN columns only when needed
      const needsIssueTypeFields = query?.is_new_issue !== undefined;
      const issueTypeFields = needsIssueTypeFields
        ? `
      is_new_issue
      fingerprint_first_seen_at`
        : '';

      let LIST_k8_ISSUES = `
 query list_k8_issues_data($limit:Int, $offset:Int) {
  events_aggregate: event_groupings_v2(where: __WHERE__) {
    rows{
      count: event_count
    }
  }
  events: events_v2(where: __WHERE__, order_by: __ORDER_BY__, limit: $limit, offset: $offset) {
    rows{
      account_id
      subject_type
      subject_name
      subject_namespace
      created_at
      updated_at
      starts_at
      title
      finding_type
      cluster
      labels
      status
      nb_status
      snoozed_until
      aggregation_key
      priority
      id
      resource_id
      fingerprint
      source
      urgency
      computed_score
      computed_priority
      score_factors
      score_confidence${issueTypeFields}
    }
  }
}`;

      if (query?.onlyData) {
        LIST_k8_ISSUES = `
  query list_k8_issues_data($limit:Int, $offset:Int) {
   events: events_v2(where: __WHERE__, order_by: __ORDER_BY__, limit: $limit, offset: $offset) {
     rows{
       account_id
       subject_type
       subject_name
       subject_namespace
       created_at
       updated_at
       starts_at
       title
       finding_type
       cluster
       labels
       status
       nb_status
       snoozed_until
       aggregation_key
       priority
       id
       resource_id
       finding_id
       fingerprint
       subject_owner
       computed_score
       computed_priority
       score_factors
       score_confidence${issueTypeFields}
     }
   }
 }`;
      }
      let queryStr = LIST_k8_ISSUES;

      queryStr = queryStr.replaceAll('__WHERE__', gqlStringify(filterParams));
      queryStr = queryStr.replaceAll('__ORDER_BY__', orderByClause);
      queryStr = queryStr.replaceAll('__COLS__', cols.join(' '));
      const response = await queryGraphQL(queryStr, 'list_k8_issues_data', {
        limit: limit,
        offset: offset,
      });
      response?.data?.data?.events?.rows?.forEach((item: any) => {
        if (item.evidences && item.evidences.length > 0) {
          try {
            const events = item.evidences;
            if (events && events.length > 0) {
              for (const e of events) {
                if (e.data && e.data.includes('restart count:')) {
                  item.restart_count = e.data.split('restart count:')[1].trim();
                  break;
                }
              }
            }
          } catch {
            //ignore
          }
        }
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
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sEventsCount(query: any = {}) {
    try {
      const filterParams = buildEventFilterParams(query);

      const COUNT_QUERY = `
query list_k8_issues_count {
  events_aggregate: event_groupings_v2(where: __WHERE__) {
    rows{
      count: event_count
    }
  }
}`;
      let queryStr = COUNT_QUERY.replaceAll('__WHERE__', gqlStringify(filterParams));
      const response = await queryGraphQL(queryStr, 'list_k8_issues_count', {});
      return {
        count: response?.data?.data?.events_aggregate?.rows?.[0]?.count ?? 0,
      };
    } catch (error) {
      console.log('Error fetching events count', error);
      return { count: 0 };
    }
  },
  async getK8sEventsName(limit = 10, offset = 0, query: any = {}) {
    try {
      const filterParams: any = {};
      query = Object.fromEntries(Object.entries(query).filter(([_key, value]) => value !== '__ALL__'));
      if (query?.account_id) {
        if (Array.isArray(query['account_id'])) {
          filterParams['account_id'] = { _in: query['account_id'] };
        } else {
          filterParams['account_id'] = { _eq: query['account_id'] };
        }
      }
      if (query?.subject_type) {
        if (Array.isArray(query['subject_type'])) {
          filterParams['subject_type'] = { _in: query['subject_type'] };
        } else {
          filterParams['subject_type'] = { _eq: query['subject_type'] };
        }
      }

      if (query?.finding_type) {
        if (Array.isArray(query['finding_type'])) {
          filterParams['finding_type'] = { _in: query['finding_type'] };
        } else {
          filterParams['finding_type'] = { _eq: query['finding_type'] };
        }
      }
      if (query?.aggregation_key) {
        if (Array.isArray(query['aggregation_key'])) {
          filterParams['aggregation_key'] = { _in: query['aggregation_key'] };
        } else {
          filterParams['aggregation_key'] = { _eq: query['aggregation_key'] };
        }
      }
      if (query?.subject_name) {
        if (Array.isArray(query['subject_name'])) {
          filterParams['subject_name'] = { _in: query['subject_name'] };
        } else {
          filterParams['subject_name'] = { _eq: query['subject_name'] };
        }
      }
      if (query?.subject_namespace) {
        if (Array.isArray(query['subject_namespace'])) {
          filterParams['subject_namespace'] = { _in: query['subject_namespace'] };
        } else {
          filterParams['subject_namespace'] = { _eq: query['subject_namespace'] };
        }
      }
      const endDate = query.end_date || query.endDate || getEndOfMonth(new Date());
      const startDate = query.start_date || query.startDate || getStartOfMonth(new Date());

      filterParams['_and'] = [{ starts_at: { _gte: startDate.toISOString() } }, { starts_at: { _lte: endDate.toISOString() } }];

      const response = await queryGraphQL(
        LIST_k8_ISSUES_NAME.replaceAll('__WHERE__', gqlStringify(filterParams, ['priority', 'status'])),
        'list_k8_issues_name',
        {
          limit: limit,
          offset: offset,
        }
      );
      response?.data?.data?.events?.rows?.forEach((item: any) => {
        if (item.evidences && item.evidences.length > 0) {
          try {
            const events = item.evidences;
            if (events && events.length > 0) {
              for (const e of events) {
                if (e.data.includes('restart count:')) {
                  item.restart_count = e.data.split('restart count:')[1].trim();
                  break;
                }
              }
            }
          } catch {
            //ignore
          }
        }
      });
      return {
        data: {
          events: response?.data?.data?.events?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sEventGroupings(
    limit = 10,
    offset = 0,
    query: {
      account_id: string;
      subject_namespace?: string | string[];
      subject_type?: string;
      subject_name?: string | string[];
      subject_owner?: string | string[];
      finding_type?: string;
      aggregation_key?: string | Array<string>;
      priority?: string;
      priority_nin?: string[];
      status?: string;
      start_date?: Date;
      end_date?: Date;
      resource_ids?: string[];
      onlyGroupingCount?: boolean;
      fingerprint?: string;
      source?: string[];
      nb_priority?: string;
      nb_status?: string | string[];
      is_new_issue?: boolean;
    } = {
      account_id: 'demo',
      onlyGroupingCount: false,
    },
    groupBy = ['tenant_id', 'account_id', 'created_at'],
    cols: Array<string> = ['tenant_id', 'account_id', 'created_at', 'event_count'],
    orderBy = { name: '', order: '' }
  ) {
    if (query?.account_id == 'demo') {
      const eventDemoData: any = await getMockData('k8s-events');
      const eventsgrouping: any = await getMockData('k8s-events-grouping');

      if (groupBy.includes('subject_name')) {
        return { data: eventsgrouping.subject_name.data };
      }
      if (groupBy.includes('aggregation_key')) {
        return { data: eventsgrouping.aggregation_key.data };
      }
      if (query?.subject_type == 'pod' && query?.aggregation_key == 'pod_oom_killer_enricher') {
        return {
          data: eventDemoData.PodErrors.pod_oom_killer_enricher.k8s_event_groupings.data,
        };
      } else if (query?.subject_type == 'pod' && query?.aggregation_key == 'image_pull_backoff_reporter') {
        return {
          data: eventDemoData.PodErrors.image_pull_backoff_reporter.k8s_event_groupings.data,
        };
      } else if (query?.subject_type == 'pod' && query?.aggregation_key == 'KubePodCrashLooping') {
        return {
          data: eventDemoData.PodErrors.KubePodCrashLooping.k8s_event_groupings.data,
        };
      } else if (query?.subject_type == 'deployment' && query?.aggregation_key == 'KubeDeploymentReplicasMismatch') {
        return {
          data: eventDemoData.PodErrors.KubeDeploymentReplicasMismatch.k8s_event_groupings.data,
        };
      } else if (query?.subject_type == 'pod') {
        return {
          data: eventDemoData.PodErrors.AllPodEvents.k8s_event_groupings.data,
        };
      } else if (query?.subject_type == 'node') {
        return {
          data: eventDemoData.NodeErrors.k8s_event_groupings.data,
        };
      } else if (query?.aggregation_key == 'HighErrorCriticalLogs') {
        return {
          data: eventDemoData.ApplicationErrors.AllErrors.k8s_event_groupings.data,
        };
      }
      return {
        data: eventDemoData.AllEvents.k8s_event_groupings.data,
      };
    }
    try {
      const filterParams: any = {};
      if (query?.account_id) {
        filterParams['account_id'] = { _eq: query.account_id };
      }
      if (Array.isArray(query?.['subject_namespace'])) {
        filterParams['subject_namespace'] = { _in: query['subject_namespace'] };
      } else if (query?.['subject_namespace']) {
        filterParams['subject_namespace'] = { _eq: query['subject_namespace'] };
      }
      if (query?.['subject_type']) {
        filterParams['subject_type'] = { _eq: query['subject_type'] };
      }
      if (Array.isArray(query?.['subject_name'])) {
        filterParams['subject_name'] = { _in: query['subject_name'] };
      } else if (query?.['subject_name']) {
        filterParams['subject_name'] = { _like: query['subject_name'] + '%' };
      }
      if (Array.isArray(query?.['subject_owner'])) {
        filterParams['subject_owner'] = { _in: query['subject_owner'] };
      } else if (query?.['subject_owner']) {
        filterParams['subject_owner'] = { _eq: query['subject_owner'] };
      }
      if (query?.['finding_type']) {
        filterParams['finding_type'] = { _eq: query['finding_type'] };
      }
      if (query?.['aggregation_key'] && Array.isArray(query['aggregation_key']) && query['aggregation_key'].length) {
        filterParams['aggregation_key'] = { _in: query['aggregation_key'] };
      } else if (query?.['aggregation_key'] && typeof query['aggregation_key'] === 'string') {
        filterParams['aggregation_key'] = { _eq: query['aggregation_key'] };
      }
      if (query?.['priority']) {
        filterParams['priority'] = { _eq: query['priority'] };
      } else if (query?.['priority_nin']?.length) {
        filterParams['priority'] = { _not_in: query['priority_nin'] };
      }
      if (query?.['status']) {
        filterParams['status'] = { _eq: query['status'] };
      }
      if (query['resource_ids']?.length) {
        filterParams['resource_id'] = { _in: query['resource_ids'] };
      }
      if (query['fingerprint'] && Array.isArray(query['fingerprint']) && query['fingerprint'].length) {
        filterParams['fingerprint'] = { _in: query['fingerprint'] };
      } else if (query['fingerprint'] && !Array.isArray(query['fingerprint'])) {
        filterParams['fingerprint'] = { _eq: query['fingerprint'] };
      }
      if (query['source'] && Array.isArray(query['source']) && query['source'].length) {
        filterParams['source'] = { _in: query['source'] };
      } else if (query['source'] && !Array.isArray(query['source'])) {
        filterParams['source'] = { _eq: query['source'] };
      }
      if (query['nb_priority']) {
        filterParams['computed_priority'] = { _eq: query['nb_priority'] };
      }
      if (query['nb_status']) {
        if (Array.isArray(query['nb_status'])) {
          filterParams['nb_status'] = { _in: query['nb_status'] };
        } else {
          filterParams['nb_status'] = { _eq: query['nb_status'] };
        }
      }
      if (query.is_new_issue !== undefined) {
        filterParams['is_new_issue'] = { _eq: query.is_new_issue };
      }

      const endDate = query.end_date;
      const startDate = query.start_date;

      if (startDate && endDate) {
        filterParams['created_at'] = {
          _between: {
            _gte: startDate.toISOString(),
            _lte: endDate.toISOString(),
          },
        };
      }

      let LIST_K8_EVENTS_GROUPINGS = `
query k8s_event_groupings($limit:Int,$offset:Int){
  event_groupings: event_groupings_v2(where:__WHERE__, group_by:__GROUP_BY__, limit:$limit,offset:$offset,order_by:__ORDER_BY__){
    rows{
      __COLS__
    }
  }

  event_groupings_aggregate: event_groupings_v2(where:__WHERE__, group_by:[], column_transformations:[{name: "event_count", expr: "distinct", args: __GROUP_BY__}], columns:["event_count"]){
    rows{
      event_count
    }
  }
}
`;
      if (query.onlyGroupingCount) {
        LIST_K8_EVENTS_GROUPINGS = `
query k8s_event_groupings($limit:Int,$offset:Int){
  event_groupings: event_groupings_v2(where:__WHERE__, group_by:__GROUP_BY__, limit:$limit,offset:$offset,order_by:__ORDER_BY__, column_transformations:[{name: "created_at", expr: "date_unit", args: ["day"]}]){
    rows{
      __COLS__
    }
  }
}
`;
      }
      let formattedQuery = LIST_K8_EVENTS_GROUPINGS.replaceAll('__WHERE__', gqlStringify(filterParams));
      formattedQuery = formattedQuery.replaceAll('__GROUP_BY__', gqlStringify(groupBy));
      formattedQuery = formattedQuery.replaceAll('__COLS__', cols.join(' '));
      if (orderBy.name) {
        formattedQuery = formattedQuery.replaceAll(
          '__ORDER_BY__',
          gqlStringify([{ column: orderBy.name, order: orderBy.order ?? 'asc' }], ['order'])
        );
      } else {
        formattedQuery = formattedQuery.replaceAll('__ORDER_BY__', `[]`);
      }
      const response = await queryGraphQL(formattedQuery, 'k8s_event_groupings', {
        limit: limit,
        offset: offset,
      });
      return {
        data: {
          event_groupings: response?.data?.data?.event_groupings?.rows,
          event_groupings_aggregate: {
            aggregate: {
              count: response?.data?.data?.event_groupings_aggregate?.rows?.[0]?.event_count,
            },
          },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },

  async getK8sNamespaceNames(accountId: string) {
    if (accountId === 'demo') {
      return {
        data: {
          namespaces: [],
        },
      };
    }
    const response = await queryGraphQL(LIST_k8_WORKLOAD_NAMESPACES, 'k8s_workload_namespace_list', {
      accountId: accountId,
    });

    const data = response?.data?.data?.k8s_namespaces?.rows?.map((item: any) => {
      return item.namespace_name;
    });

    return {
      data: {
        namespaces: [...new Set(data)],
      },
    };
  },

  async getK8sNamespaces(limit = 50, offset = 0, query: { accountId?: string; name?: string } = {}) {
    if (query?.accountId === 'demo') {
      const namespaceDemo: any = await getMockData('k8s-namespaces');
      return {
        data: namespaceDemo.k8s_namespace_list.data,
      };
    }

    query = query || {};

    const where = [];
    if (query.accountId) {
      where.push({ account_id: { _eq: query.accountId }, is_active: { _eq: true } });
    }
    if (query.name) {
      where.push({ name: { _ilike: query.name + '%' } });
    }

    const formattedQuery = LIST_k8_NAMESPACES.replaceAll('__WHERE__', gqlStringify({ _and: where }));

    const response = await queryGraphQL(formattedQuery, 'k8s_namespace_list', {
      limit: limit,
      offset: offset,
    });

    const namespaces = response?.data?.data?.k8s_namespaces?.rows || [];
    const sortedNamespaces = namespaces.sort((a: any, b: any) => (a.name || '').toLowerCase().localeCompare((b.name || '').toLowerCase()));

    return {
      data: {
        k8s_namespaces: sortedNamespaces,
        k8s_namespaces_aggregate: {
          aggregate: {
            count: response?.data?.data?.k8s_namespaces_aggregate?.rows[0]?.count,
          },
        },
      },
    };
  },
  async getK8sPods(
    limit = 10,
    offset = 0,
    query: {
      cloud_account_id?: string;
      account_Id?: string;
      accountId?: string;
      namespace?: string;
      namespaceName?: string;
      namespace_name?: string;
      workloadName?: string;
      workload_name?: string;
      node_name?: string;
      nodeName?: string;
      is_active?: boolean;
      isActive?: boolean;
      workload_type?: string;
      status?: string;
      workloadType?: string;
      pod_name?: string;
      podName?: string;
      name?: string;
      startDate?: Date;
      endDate?: Date;
      labels?: string | string[];
    } = {},
    withCount = true
  ) {
    if (query?.cloud_account_id === 'demo' || query?.account_Id === 'demo' || query?.accountId === 'demo') {
      const podsDemo: any = await getMockData('k8s-pods');
      return {
        data: podsDemo.k8s_pod_list.data,
      };
    }

    query = query || {};
    const where = [];
    if (query.accountId || query.account_Id || query.cloud_account_id) {
      where.push({ account_id: { _eq: query.accountId || query.account_Id || query.cloud_account_id } });
    }
    if (query.namespaceName || query.namespace_name || query.namespace) {
      where.push({ namespace: { _eq: query.namespaceName || query.namespace_name || query.namespace } });
    }
    if (query.workloadName || query.workload_name) {
      where.push({ workload_name: { _eq: query.workloadName || query.workload_name } });
    }
    if (query.nodeName || query.node_name) {
      where.push({ node_name: { _eq: query.nodeName || query.node_name } });
    }
    if (query.workloadType || query.workload_type) {
      where.push({ workload_type: { _eq: query.workloadType || query.workload_type } });
    }
    if (query.isActive === true) {
      where.push({ is_active: { _eq: true } });
    } else if (query.isActive === false) {
      where.push({ is_active: { _eq: false } });
    }
    if (query.status) {
      where.push({ status: { _eq: query.status } });
    }
    if (query.podName || query.pod_name || query.name) {
      where.push({ name: { _ilike: '%' + (query.podName || query.pod_name || query.name) + '%' } });
    }
    if (query.startDate) {
      const startDate = new Date(query.startDate);
      where.push({ creation_time: { _gte: startDate.toISOString() } });
    }
    if (query.endDate) {
      const endDate = new Date(query.endDate);
      where.push({ creation_time: { _lte: endDate.toISOString() } });
    }
    if (Array.isArray(query.labels)) {
      const orConditions = [];
      for (const label of query.labels) {
        if (label) {
          orConditions.push({ labels: { _eq: label } });
        }
      }
      if (orConditions.length > 0) {
        where.push({ _or: orConditions });
      }
    } else if (query.labels) {
      where.push({ labels: { _eq: query.labels } });
    }
    let LIST_POD_QUERY = LIST_k8_PODS;
    if (!withCount) {
      LIST_POD_QUERY = `
      query k8s_pods_list($limit:Int, $offset:Int) {
        k8s_pods: k8s_pods_v2(where: __WHERE__,  limit:$limit, offset:$offset, order_by: [{column: "creation_time", order: desc}]){
          rows{
            id: resource_id
            namespace
            name
            status
            is_active
            node_name
            workload_name
            workload_type
            timestamp: creation_time
            restart_count
          }
        }
      }`;
    }
    const formattedQuery = LIST_POD_QUERY.replaceAll('__WHERE__', gqlStringify({ _and: where }));

    const response = await queryGraphQL(formattedQuery, 'k8s_pods_list', {
      limit: limit,
      offset: offset,
    });

    return {
      data: {
        k8s_pods: response?.data?.data?.k8s_pods?.rows?.map((item: any) => {
          if (typeof item.restart_count === 'string') {
            try {
              item.restart_count = JSON.parse(item.restart_count);
            } catch {
              // do nothing
              item.restart_count = [];
            }
          }
          return item;
        }),
        k8s_pods_aggregate: {
          aggregate: {
            count: response?.data?.data?.k8s_pods_aggregate?.rows?.[0]?.count,
          },
        },
      },
    };
  },
  async getK8sWorkload(limit = 10, offset = 0, query: any = {}, orderBy = { name: '', order: '' }, withCount = true) {
    if (query?.cloud_account_id === 'demo' || query?.account_id === 'demo' || query?.accountId === 'demo') {
      const appsDemo: any = await getMockData('k8s-applications');
      return {
        data: appsDemo.k8s_workloads_list.data,
      };
    }

    query = query || {};
    const where = [];
    if (query.accountId || query.account_id || query.cloud_account_id) {
      where.push({ account_id: { _eq: query.accountId || query.account_id } });
    }
    if (query.namespaceName || query.namespace_name || query.namespace) {
      where.push({ namespace: { _eq: query.namespaceName || query.namespace_name } });
    }

    if (query.namespaceList) {
      where.push({ namespace: { _in: query.namespaceList } });
    }
    if (query.workloadName || query.workload_name || query.name) {
      const workloadNameValue = query.workloadName || query.workload_name;
      // Use exact match if exactNameMatch is true, otherwise use partial match (ilike)
      if (query.exactNameMatch) {
        where.push({ name: { _eq: workloadNameValue } });
      } else {
        where.push({ name: { _ilike: '%' + workloadNameValue + '%' } });
      }
    }
    if (query.workloadType || query.workload_type || query.kind) {
      where.push({ kind: { _eq: query.workloadType || query.workload_type } });
    } else {
      where.push({ kind: { _neq: 'ReplicaSet' } });
    }
    if (query?.resource_ids && query?.resource_ids.length > 0) {
      where.push({ resource_id: { _in: query.resource_ids } });
    }
    let isActive = query.isActive || query.is_active;
    if (isActive === undefined || isActive === null) {
      isActive = true;
    }
    where.push({ is_active: { _eq: isActive } });
    if (Array.isArray(query.labels)) {
      const orConditions = [];
      for (const label of query.labels) {
        if (label) {
          orConditions.push({ labels: { _eq: label } });
        }
      }
      if (orConditions.length > 0) {
        where.push({ _or: orConditions });
      }
    } else if (query.labels) {
      where.push({ labels: { _eq: query.labels } });
    }
    let LIST_WORKLOAD_QUERY = LIST_k8_WORKLOADS;
    if (!withCount) {
      LIST_WORKLOAD_QUERY = `query k8s_workloads_list($limit:Int, $offset:Int) {
        k8s_workloads:k8s_workloads_v2(where: __WHERE__,  limit:$limit, offset:$offset, order_by: __ORDER_BY__){
          rows{
            namespace
            name
            kind
            is_active
            creation_time
            total_pods
            ready_pods
            cloud_resource_id: resource_id
            cloud_account_id: account_id
            tenant_id
            meta
          }
        }
      }
      `;
    }
    let formattedQuery = LIST_WORKLOAD_QUERY.replaceAll('__WHERE__', gqlStringify({ _and: where }));

    if (orderBy.name) {
      let orderByName = orderBy.name;
      if (orderBy.name == 'Replicas') {
        orderByName = 'ready_pods';
      }
      formattedQuery = formattedQuery.replaceAll('__ORDER_BY__', gqlStringify([{ column: orderByName, order: orderBy.order ?? 'asc' }], ['order']));
    } else {
      formattedQuery = formattedQuery.replaceAll('__ORDER_BY__', gqlStringify([{ column: 'creation_time', order: 'desc' }], ['order']));
    }
    const response = await queryGraphQL(formattedQuery, 'k8s_workloads_list', {
      limit: limit,
      offset: offset,
    });

    return {
      data: {
        k8s_workloads: response?.data?.data?.k8s_workloads?.rows?.map((item: any) => {
          if (typeof item.meta === 'string') {
            try {
              item.meta = JSON.parse(item.meta);
            } catch (e) {
              console.log('Error parsing meta', e);
            }
          }
          return item;
        }),
        k8s_workloads_aggregate: {
          aggregate: {
            count: response?.data?.data?.k8s_workloads_aggregate?.rows?.[0]?.count,
          },
        },
      },
    };
  },
  async getK8sNodes({
    accountId,
    isActive = true,
    nodeName = '',
    limit = 10,
    offset = 0,
  }: {
    accountId: string;
    isActive: boolean | null;
    nodeName: string;
    limit: number;
    offset: number;
  }) {
    if (accountId === 'demo') {
      const nodesDemo: any = await getMockData('k8s-nodes');
      return {
        data: nodesDemo.node_list.data,
      };
    }
    try {
      const query: any = {};
      if (accountId) {
        query['cloud_account_id'] = { _eq: accountId };
      }

      if (isActive != null || isActive != undefined) {
        if (isActive) {
          query['_or'] = [{ is_active: { _eq: true } }, { is_active: { _is_null: true } }];
        } else {
          query['is_active'] = { _in: [isActive] };
        }
      }
      if (nodeName) {
        query['name'] = { _ilike: `%${nodeName}%` };
      }

      const response = await queryGraphQL(LIST_k8_NODES.replaceAll('__WHERE__', gqlStringify(query)), 'k8s_nodes_list', {
        limit: limit,
        offset: offset,
      });

      const nodesRows = (response?.data?.data?.k8s_nodes?.rows || []).map((node: any) => ({
        ...node,
        conditions: typeof node.conditions === 'string' ? safeJSONParse(node.conditions) : node.conditions,
        labels: typeof node.labels === 'string' ? safeJSONParse(node.labels) : node.labels,
        taints: typeof node.taints === 'string' ? safeJSONParse(node.taints) : node.taints,
        meta: typeof node.meta === 'string' ? safeJSONParse(node.meta) : node.meta,
      }));
      const aggregateRows = response?.data?.data?.k8s_nodes_aggregate?.rows;
      return {
        data: {
          k8s_nodes: nodesRows,
          k8s_nodes_aggregate: { aggregate: { count: aggregateRows?.[0]?.count || 0 } },
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sPodGroupings(
    limit = 10,
    query: any = {},
    groupBy: string[] = ['tenant_id', 'account_id', 'timestamp'],
    datasource = 'auto',
    gpuTrend = false,
    timeRangeInMinutes = 1
  ) {
    if (query?.account_id === 'demo' || query?.accountId === 'demo') {
      const podGroupingsDemo: any = await getMockData('k8s-pod-groupings');
      return {
        data: podGroupingsDemo.pod_cpu_memory.data,
      };
    }

    datasource = datasource || 'auto';

    if (!groupBy) {
      groupBy = ['tenant_id', 'account_id', 'timestamp'];
    }
    if (!groupBy.includes('tenant_id')) {
      groupBy.push('tenant_id');
    }
    if (!groupBy.includes('account_id')) {
      groupBy.push('account_id');
    }

    const last7Days = getStartOfDay(new Date());
    last7Days.setDate(last7Days.getDate() - 7);
    query = query || {};
    query.startDate = query.startDate || getStartOfDay(last7Days);
    query.endDate = query.endDate || getEndOfDay(new Date());

    const time1 = new Date(query.startDate).getTime();
    const time2 = new Date(query.endDate).getTime();
    const differenceInMilliseconds = Math.abs(time2 - time1);
    const differenceInHours = differenceInMilliseconds / (1000 * 60 * 60);
    let thresholdHours = 24.1;

    const clusterData = getClusterData(query.accountId);
    if (clusterData != null) {
      if (clusterData?.agent?.connection_status?.prometheusRetentionTime) {
        let retentionTime = clusterData.agent?.connection_status?.prometheusRetentionTime;
        if (typeof retentionTime === 'string') {
          retentionTime = parseInt(retentionTime);
        }
        thresholdHours = retentionTime * 24 + 0.1;
      }
    }
    if (datasource == 'nb' || (datasource == 'auto' && differenceInHours > thresholdHours)) {
      const where: any = {};
      if (query.accountId || query.account_Id) {
        where.account_id = { _eq: query.accountId || query.account_Id };
      }
      if (query.namespaceName || query.namespace_name) {
        where.namespace_name = { _eq: query.namespaceName || query.namespace_name };
      }
      if (query.workloadName || query.workload_name) {
        where.workload_name = { _eq: query.workloadName || query.workload_name };
      }
      if (query.nodeName || query.node_name) {
        where.node_name = { _eq: query.nodeName || query.node_name };
      }
      if (query.podName || query.pod_name) {
        where.pod_name = { _eq: query.podName || query.pod_name };
      }
      where.timestamp = { _between: { _gt: query.startDate.toISOString(), _lt: query.endDate.toISOString() } };

      let formattedQuery = LIST_k8_POD_GROUPING.replaceAll('__WHERE__', gqlStringify(where));
      formattedQuery = formattedQuery.replaceAll('__GROUP_BY__', gqlStringify(`{${groupBy.join(',')}}`));

      const response = await queryGraphQL(formattedQuery, 'k8s_pod_groupings', {
        limit: limit,
      });

      return {
        data: {
          k8s_pod_groupings: response?.data?.data?.k8s_pod_groupings?.rows,
        },
      };
    }
    const queries = [];
    if ((query.containerName || query.container_name) && (query.podName || query.pod_name) && (query.namespaceName || query.namespace_name)) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(container_cpu_usage_seconds_total{ __CLUSTER__ container = "${query.containerName || query.container_name}", pod = "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(container_memory_working_set_bytes{ __CLUSTER__ container = "${query.containerName || query.container_name}", pod = "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", container = "${
            query.containerName || query.container_name
          }", pod = "${query.podName || query.pod_name}", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory", container = "${
            query.containerName || query.container_name
          }", pod = "${query.podName || query.pod_name}", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", container = "${
            query.containerName || query.container_name
          }", pod = "${query.podName || query.pod_name}", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", container = "${
            query.containerName || query.container_name
          }", pod = "${query.podName || query.pod_name}", namespace="${query.namespaceName || query.namespace_name}"})`,
        }
      );
      if (gpuTrend) {
        queries.push(
          {
            key: 'gpu_usage',
            query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource="nvidia_com_gpu", container = "${
              query.containerName || query.container_name
            }", pod = "${query.podName || query.pod_name}", namespace="${query.namespaceName || query.namespace_name}"})`,
          },
          {
            key: 'gpu_temp',
            query: `sum(DCGM_FI_DEV_GPU_TEMP{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          },
          {
            key: 'gpu_mem_temp',
            query: `sum(DCGM_FI_DEV_MEMORY_TEMP{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          },
          {
            key: 'gpu_mem_usage',
            query: `sum(DCGM_FI_DEV_MEM_COPY_UTIL{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          }
        );
      }
    } else if ((query.podName || query.pod_name) && (query.namespaceName || query.namespace_name)) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(container_cpu_usage_seconds_total{ __CLUSTER__ container != "", pod =~ "${query.podName || query.pod_name}", namespace="${
            query.namespaceName || query.namespace_name
          }"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(container_memory_working_set_bytes{ __CLUSTER__ container != "", pod =~ "${query.podName || query.pod_name}", namespace="${
            query.namespaceName || query.namespace_name
          }"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", container != "", pod =~ "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory", container != "", pod =~ "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", container != "", pod =~ "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", container != "", pod =~ "${
            query.podName || query.pod_name
          }", namespace="${query.namespaceName || query.namespace_name}"})`,
        }
      );
      if (gpuTrend) {
        queries.push(
          {
            key: 'gpu_usage',
            query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource="nvidia_com_gpu", container != "", pod =~ "${
              query.podName || query.pod_name
            }", namespace="${query.namespaceName || query.namespace_name}"})`,
          },
          {
            key: 'gpu_temp',
            query: `sum(DCGM_FI_DEV_GPU_TEMP{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          },
          {
            key: 'gpu_mem_temp',
            query: `sum(DCGM_FI_DEV_MEMORY_TEMP{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          },
          {
            key: 'gpu_mem_usage',
            query: `sum(DCGM_FI_DEV_MEM_COPY_UTIL{ __CLUSTER__ exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
          }
        );
      }
    } else if (
      (query.workloadName || query.workload_name) &&
      (query.namespaceName || query.namespace_name) &&
      (query.containerName || query.container_name)
    ) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(container_cpu_usage_seconds_total{ __CLUSTER__ container = "${query.containerName || query.container_name}", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(container_memory_working_set_bytes{ __CLUSTER__ container = "${query.containerName || query.container_name}", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", container = "${
            query.containerName || query.container_name
          }", pod =~ "${query.workloadName || query.workload_name}.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory",container = "${
            query.containerName || query.container_name
          }", pod =~ "${query.workloadName || query.workload_name}.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", container = "${
            query.containerName || query.container_name
          }", pod =~ "${query.workloadName || query.workload_name}.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", container = "${
            query.containerName || query.container_name
          }", pod =~ "${query.workloadName || query.workload_name}.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        }
      );
    } else if ((query.workloadName || query.workload_name) && (query.namespaceName || query.namespace_name)) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(container_cpu_usage_seconds_total{ __CLUSTER__ container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(container_memory_working_set_bytes{ __CLUSTER__ container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory",container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", container != "", pod =~ "${
            query.workloadName || query.workload_name
          }.*", namespace="${query.namespaceName || query.namespace_name}"})`,
        }
      );
    } else if (query.namespaceName || query.namespace_name) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(container_cpu_usage_seconds_total{ __CLUSTER__ container != "", namespace="${
            query.namespaceName || query.namespace_name
          }"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(container_memory_working_set_bytes{__CLUSTER__ container != "", namespace="${query.namespaceName || query.namespace_name}"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", container != "", namespace="${
            query.namespaceName || query.namespace_name
          }"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory",container != "", namespace="${
            query.namespaceName || query.namespace_name
          }"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", container != "", namespace="${
            query.namespaceName || query.namespace_name
          }"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", container != "", namespace="${
            query.namespaceName || query.namespace_name
          }"})`,
        }
      );
    } else if (query.internalIp) {
      queries.push(
        {
          key: 'cpu_usage',
          query: `sum(irate(node_cpu_seconds_total{ __CLUSTER__ mode != "idle", instance=~"${query.internalIp}.*"}[${timeRangeInMinutes}m])) OR sum(irate(node_resources_cpu_usage_seconds_total{ __CLUSTER__ mode != "idle", instance=~"${query.nodeName}.*"}[${timeRangeInMinutes}m]))`,
        },
        {
          key: 'memory_usage',
          query: `sum(node_memory_Active_bytes{__CLUSTER__ instance=~"${query.internalIp}.*"}) or sum(node_resources_memory_total_bytes{ __CLUSTER__ instance=~"${query.nodeName}.*"} - node_resources_memory_available_bytes{ __CLUSTER__ instance=~"${query.nodeName}.*"})`,
        },
        {
          key: 'cpu_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "cpu", node=~"${query.nodeName}.*"})`,
        },
        {
          key: 'memory_request',
          query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource = "memory", node=~"${query.nodeName}.*"})`,
        },
        {
          key: 'cpu_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "cpu", node=~"${query.nodeName}.*"})`,
        },
        {
          key: 'memory_limit',
          query: `sum(kube_pod_container_resource_limits{ __CLUSTER__ resource = "memory", node=~"${query.nodeName}.*"})`,
        },
        {
          key: 'disk_total',
          query: `sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"${query.internalIp}.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"${query.nodeName}.*"}) or sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"${query.nodeIp}.*"})`,
        },
        {
          key: 'disk_used',
          query: `(sum(node_filesystem_size_bytes{ __CLUSTER__ mountpoint="/", instance=~"${query.internalIp}.*"}) - sum(node_filesystem_free_bytes{ __CLUSTER__ mountpoint="/", instance=~"${query.internalIp}.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"${query.nodeName}.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"${query.nodeName}.*"})) or (sum(kubelet_volume_stats_capacity_bytes{ __CLUSTER__ instance=~"${query.nodeIp}.*"}) - sum(kubelet_volume_stats_available_bytes{ __CLUSTER__ instance=~"${query.nodeIp}.*"}))`,
        }
      );
      if (gpuTrend) {
        queries.push(
          {
            key: 'gpu_usage',
            query: `sum(kube_pod_container_resource_requests{ __CLUSTER__ resource="nvidia_com_gpu", container != "",container != "POD", node=~"${query.nodeName}.*"})`,
          },
          {
            key: 'gpu_temp',
            query: `sum(DCGM_FI_DEV_GPU_TEMP{ __CLUSTER__ instance=~"${query.internalIp}.*"})`,
          },
          {
            key: 'gpu_mem_temp',
            query: `sum(DCGM_FI_DEV_MEMORY_TEMP{ __CLUSTER__ instance=~"${query.internalIp}.*"})`,
          },
          {
            key: 'gpu_mem_usage',
            query: `sum(DCGM_FI_DEV_MEM_COPY_UTIL{ __CLUSTER__ instance=~"${query.internalIp}.*"})`,
          }
        );
        if (gpuTrend) {
          queries.push(
            {
              key: 'gpu_usage',
              query: `sum(kube_pod_container_resource_requests{resource="nvidia_com_gpu", container != "", pod =~ "${
                query.podName || query.pod_name
              }", namespace="${query.namespaceName || query.namespace_name}"})`,
            },
            {
              key: 'gpu_temp',
              query: `sum(DCGM_FI_DEV_GPU_TEMP{exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
            },
            {
              key: 'gpu_mem_temp',
              query: `sum(DCGM_FI_DEV_MEMORY_TEMP{exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
            },
            {
              key: 'gpu_mem_usage',
              query: `sum(DCGM_FI_DEV_MEM_COPY_UTIL{exported_pod=~"${query.podName ?? query.pod_name}.*"})`,
            }
          );
        }
      }
    }

    const data = {
      no_sinks: true,
      body: {
        account_id: query.accountId || query.account_Id,
        action_name: 'prometheus_queries_enricher',
        action_params: {
          promql_query: '',
          promql_queries: queries,
          steps: '',
          duration: {
            starts_at: convertNumberToTimestampPromFormat(time1),
            ends_at: convertNumberToTimestampPromFormat(time2),
          },
        },
        origin: 'Nudgebee UI',
      },
    };
    const response = await this.relayForwardRequest(data);

    let result: any[] = [];
    const isSuccess = response?.data?.success || false;

    if (isSuccess) {
      const findings = response?.data?.findings || [];
      if (findings && findings.length == 1) {
        const findingEvidence = findings[0]?.evidence || [];
        if (findingEvidence && findingEvidence.length == 1) {
          const evidenceData = findingEvidence[0]?.data || '';
          if (evidenceData) {
            const evidenceParsed = JSON.parse(evidenceData);
            if (evidenceParsed && evidenceParsed.length == 1) {
              const promqlData = evidenceParsed[0]?.data || '';
              if (promqlData) {
                const promqlParsed = JSON.parse(promqlData);
                const metrics = [
                  'cpu_usage',
                  'memory_usage',
                  'cpu_request',
                  'memory_request',
                  'cpu_limit',
                  'memory_limit',
                  'disk_used',
                  'disk_total',
                ];
                const hasData = metrics.some((metric) => promqlParsed[metric]?.series_list_result?.length > 0);

                if (hasData) {
                  const firstAvailableMetric = metrics.find((metric) => promqlParsed[metric]?.series_list_result?.length > 0);

                  if (firstAvailableMetric) {
                    result = promqlParsed[firstAvailableMetric].series_list_result[0].timestamps.map((timestamp: any, index: any) => ({
                      timestamp: formatDateTime(timestamp),
                      avg_cpu_used: parseFloat(promqlParsed.cpu_usage?.series_list_result[0]?.values?.[index]),
                      avg_memory_used: parseFloat(promqlParsed.memory_usage?.series_list_result[0]?.values?.[index]),
                      avg_cpu_request: parseFloat(promqlParsed.cpu_request?.series_list_result[0]?.values?.[index]),
                      avg_cpu_limit: parseFloat(promqlParsed.cpu_limit?.series_list_result[0]?.values?.[index]),
                      avg_memory_request: parseFloat(promqlParsed.memory_request?.series_list_result[0]?.values?.[index]),
                      avg_memory_limit: parseFloat(promqlParsed.memory_limit?.series_list_result[0]?.values?.[index]),
                      sum_gpu_used: parseFloat(promqlParsed.gpu_usage?.series_list_result[0]?.values?.[index]),
                      sum_gpu_temp: parseFloat(promqlParsed.gpu_temp?.series_list_result[0]?.values?.[index]),
                      sum_gpu_mem_temp: parseFloat(promqlParsed.gpu_temp?.series_list_result[0]?.values?.[index]),
                      sum_gpu_mem_usage: parseFloat(promqlParsed.gpu_mem_usage?.series_list_result[0]?.values?.[index]),
                      disk_total: parseFloat(promqlParsed.disk_total?.series_list_result[0]?.values?.[index]),
                      disk_used: parseFloat(promqlParsed.disk_used?.series_list_result[0]?.values?.[index]),
                      pod_cost: '',
                      account_id: query.accountId || query.account_Id,
                    }));
                  }
                }
              }
            }
          }
        }
      }
    }
    return {
      data: {
        k8s_pod_groupings: result,
      },
    };
  },
  async getK8sPodGroupings2(limit = 10, query: any = {}, groupBy: string[] = ['tenant_id', 'account_id', 'timestamp'], datasource = 'auto') {
    if (query?.account_id === 'demo' || query?.accountId === 'demo') {
      const podGroupingsDemo: any = await getMockData('k8s-pod-groupings');
      return {
        data: podGroupingsDemo.pod_cpu_memory.data,
      };
    }

    datasource = datasource || 'auto';

    if (!groupBy) {
      groupBy = ['tenant_id', 'account_id', 'timestamp'];
    }
    if (!groupBy.includes('tenant_id')) {
      groupBy.push('tenant_id');
    }
    if (!groupBy.includes('account_id')) {
      groupBy.push('account_id');
    }

    const last7Days = getStartOfDay(new Date());
    last7Days.setDate(last7Days.getDate() - 7);
    query = query || {};
    query.startDate = query.startDate || getStartOfDay(last7Days);
    query.endDate = query.endDate || getEndOfDay(new Date());

    const time1 = new Date(query.startDate).getTime();
    const time2 = new Date(query.endDate).getTime();
    const differenceInMilliseconds = Math.abs(time2 - time1);
    const differenceInHours = differenceInMilliseconds / (1000 * 60 * 60);
    let thresholdHours = 24.1;

    const clusterData = getClusterData(query.accountId);
    if (clusterData != null) {
      if (clusterData?.agent?.connection_status?.prometheusRetentionTime) {
        let retentionTime = clusterData.agent?.connection_status?.prometheusRetentionTime;
        if (typeof retentionTime === 'string') {
          retentionTime = parseInt(retentionTime);
        }
        thresholdHours = retentionTime * 24 + 0.1;
      }
    }
    if (datasource == 'nb' || (datasource == 'auto' && differenceInHours > thresholdHours)) {
      const where: any = {};
      if (query.accountId || query.account_Id) {
        where.account_id = { _eq: query.accountId || query.account_Id };
      }
      if (query.namespaceName || query.namespace_name) {
        where.namespace_name = { _eq: query.namespaceName || query.namespace_name };
      }
      if (query.workloadName || query.workload_name) {
        where.workload_name = { _eq: query.workloadName || query.workload_name };
      }
      if (query.nodeName || query.node_name) {
        where.node_name = { _eq: query.nodeName || query.node_name };
      }
      if (query.podName || query.pod_name) {
        where.pod_name = { _eq: query.podName || query.pod_name };
      }
      where.timestamp = { _between: { _gt: query.startDate.toISOString(), _lt: query.endDate.toISOString() } };

      let formattedQuery = LIST_k8_POD_GROUPING.replaceAll('__WHERE__', gqlStringify(where));
      formattedQuery = formattedQuery.replaceAll('__GROUP_BY__', gqlStringify(`{${groupBy.join(',')}}`));

      const response = await queryGraphQL(formattedQuery, 'k8s_pod_groupings', {
        limit: limit,
      });

      return {
        data: {
          k8s_pod_groupings: response?.data?.data?.k8s_pod_groupings?.rows,
        },
      };
    }
    const METRICS_QUERY_UTILISATION = `
    query MetricsQueryUtilisation($jsonFilter: jsonb!, $accountId: String!, $startTime: Float!, $endTime: Float!) {
      metrics_query_utilisation(request: {account_id: $accountId, request: $jsonFilter, end_time: $endTime, start_time: $startTime}) {
        results
      }
    }    
    `;
    const data: any = {};
    if ((query.pod_name || query.podName) && (query.namespaceName || query.namespace_name)) {
      data.kind = 'pod';
      data.workload_namespace = query.namespaceName || query.namespace_name;
      data.workload_name = query.pod_name || query.podName || query.workload_name;
    } else if ((query.workloadName || query.workload_name) && (query.namespaceName || query.namespace_name)) {
      data.kind = query.workloadType;
      data.workload_namespace = query.namespaceName || query.namespace_name;
      data.workload_name = query.workloadName || query.workload_name;
    } else if (query.namespaceName || query.namespace_name) {
      data.kind = 'namespace';
      data.workload_namespace = query.namespaceName || query.namespace_name;
    } else if (query.nodeName || query.nodeIp || query.internalIp) {
      data.kind = 'node';
      data.node_name = query.nodeName;
      data.node_ip = query.nodeIp;
      data.internal_ip = query.internalIp;
    }
    data.metrics = query.metrics;

    const response = await queryGraphQL(METRICS_QUERY_UTILISATION, 'MetricsQueryUtilisation', {
      accountId: query.accountId || query.account_id,
      startTime: time1,
      endTime: time2,
      jsonFilter: data,
    });
    const metricsResults = response?.data?.data?.metrics_query_utilisation?.results || [];
    const getDataByKey = (key: string) => {
      const found = metricsResults.find((item: any) => item.query_key === key);
      return found?.payload?.[0]?.values || [];
    };

    const memUsageVals = getDataByKey('memory_usage'); // key from your API
    const memRequestVals = getDataByKey('memory_request'); // key from your API
    const memLimitVals = getDataByKey('memory_limit'); // key from your API

    const cpuUsageVals = getDataByKey('cpu_usage'); // key from your API
    const cpuRequestVals = getDataByKey('cpu_request'); // key from your API
    const cpuLimitVals = getDataByKey('cpu_limit'); // key from your API
    const timestamps = metricsResults.find((item: any) => item.payload?.[0]?.timestamps)?.payload[0].timestamps || [];
    const result = timestamps.map((timestamp: number, index: number) => {
      return {
        timestamp: formatDateTime(timestamp),
        avg_cpu_used: parseFloat(cpuUsageVals[index]) || null,
        avg_cpu_request: parseFloat(cpuRequestVals[index]) || null,
        avg_cpu_limit: parseFloat(cpuLimitVals[index]) || null,
        avg_memory_used: parseFloat(memUsageVals[index]) || null,
        avg_memory_request: parseFloat(memRequestVals[index]) || null,
        avg_memory_limit: parseFloat(memLimitVals[index]) || null,
        sum_gpu_used: null,
        sum_gpu_temp: null,
        sum_gpu_mem_temp: null,
        sum_gpu_mem_usage: null,
        disk_total: null,
        disk_used: null,
        pod_cost: '',
        account_id: query.accountId || query.account_Id,
      };
    });

    const promQueries: Record<string, string> = {};
    metricsResults.forEach((item: any) => {
      if (item.query_key && item.query) {
        promQueries[item.query_key] = item.query;
      }
    });

    return {
      data: {
        k8s_pod_groupings: result,
        promQueries,
      },
    };
  },
  async getPodDetails(id: string) {
    try {
      const formattedQuery = K8S_POD_DETAILS.replaceAll('__WHERE__', gqlStringify({ id: { _eq: id } }));
      const response = await queryGraphQL(formattedQuery, 'getPodDetails', {});
      const row = response?.data?.data?.cloud_resourses?.rows?.[0];
      const data = row
        ? {
            cloud_resourses: [
              {
                ...row,
                meta: typeof row.meta === 'string' ? safeJSONParse(row.meta) : row.meta,
                tags: typeof row.tags === 'string' ? safeJSONParse(row.tags) : row.tags,
                cloud_account: { account_name: row.account_name },
              },
            ],
          }
        : { cloud_resourses: [] };
      return {
        data,
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getClusterMetrices2({
    accountId,
    metric,
    startDate,
    endDate,
    dateUnit = 'day',
  }: {
    accountId?: string;
    resourceId?: string;
    metric?: string[];
    startDate?: Date;
    endDate?: Date;
    groupBy?: string[];
    limit: number;
    dateUnit: string;
  }) {
    if (accountId === 'demo' && metric && metric.includes('networkTransferBytes')) {
      const dashboardDemo = await getMockData('k8s-dashboard');
      return {
        data: dashboardDemo.network_groupings.data,
      };
    }

    let steps = '1d';
    if (dateUnit == 'week') {
      steps = '1w';
    } else if (dateUnit == 'month') {
      steps = '30d';
    }
    try {
      const queries = [];
      if (metric?.includes('networkReceiveBytes')) {
        queries.push({
          key: 'networkReceiveBytes',
          query: `sum(increase(container_network_receive_bytes_total{__CLUSTER__}[${steps}]))`,
        });
      }
      if (metric?.includes('networkTransferBytes')) {
        queries.push({
          key: 'networkTransferBytes',
          query: `sum(increase(container_network_transmit_bytes_total{__CLUSTER__}[${steps}]))`,
        });
      }
      if (metric?.includes('cpu')) {
        queries.push({
          key: 'cpu_usage',
          query: `sum(irate(node_cpu_seconds_total{ __CLUSTER__ mode!="idle" }[${steps}]))`,
        });
        queries.push({
          key: 'cpu_total',
          query: `sum(irate(node_cpu_seconds_total{ __CLUSTER__}[${steps}]))`,
        });
      }
      if (metric?.includes('memory')) {
        queries.push({
          key: 'memory_usage',
          query: `sum(node_memory_Active_bytes{__CLUSTER__})`,
        });
        queries.push({
          key: 'memory_total',
          query: `sum(node_memory_MemTotal_bytes{__CLUSTER__})`,
        });
      }

      if (!startDate) {
        startDate = new Date();
        startDate.setDate(startDate.getDate() - 7);
      }
      if (!endDate) {
        endDate = new Date();
      }

      const data = {
        no_sinks: true,
        body: {
          account_id: accountId,
          action_name: 'prometheus_queries_enricher',
          action_params: {
            promql_query: '',
            promql_queries: queries,
            step: steps,
            duration: {
              starts_at: convertNumberToTimestampPromFormat(startDate.getTime()),
              ends_at: convertNumberToTimestampPromFormat(endDate.getTime()),
            },
          },
          origin: 'Nudgebee UI',
        },
      };
      const response = await this.relayForwardRequest(data);
      let result: any[] = [];
      const isSuccess = response?.data?.success || false;
      if (isSuccess) {
        const findings = response?.data?.findings || [];
        if (findings && findings.length == 1) {
          const findingEvidence = findings[0]?.evidence || [];
          if (findingEvidence && findingEvidence.length == 1) {
            const evidenceData = findingEvidence[0]?.data || '';
            if (evidenceData) {
              const evidenceParsed = JSON.parse(evidenceData);
              if (evidenceParsed && evidenceParsed.length == 1) {
                const promqlData = evidenceParsed[0]?.data || '';
                if (promqlData) {
                  const promqlParsed = JSON.parse(promqlData);
                  const metrics = ['networkReceiveBytes', 'networkTransferBytes', 'cpu_usage', 'cpu_total', 'memory_usage', 'memory_total'];
                  const hasData = metrics.some((metric) => promqlParsed[metric]?.series_list_result?.length > 0);
                  if (hasData) {
                    const firstAvailableMetric = metrics.find((metric) => promqlParsed[metric]?.series_list_result?.length > 0);

                    if (firstAvailableMetric) {
                      result = promqlParsed[firstAvailableMetric].series_list_result[0].timestamps.flatMap((timestamp: any, index: any) => {
                        const response: any = [];
                        if (metric?.includes('networkTransferBytes')) {
                          response.push({
                            timestamp: formatDateTime(timestamp),
                            metric: 'networkTransferBytes',
                            avg_value: parseFloat(promqlParsed.networkTransferBytes?.series_list_result[0]?.values?.[index]),
                            account_id: accountId,
                          });
                        }
                        if (metric?.includes('networkReceiveBytes')) {
                          response.push({
                            timestamp: formatDateTime(timestamp),
                            metric: 'networkReceiveBytes',
                            avg_value: parseFloat(promqlParsed.networkReceiveBytes?.series_list_result[0]?.values?.[index]),
                            account_id: accountId,
                          });
                        }
                        if (metric?.includes('cpu')) {
                          response.push({
                            timestamp: formatDateTime(timestamp),
                            avg_cpu_used_node: parseFloat(promqlParsed.cpu_usage?.series_list_result[0]?.values?.[index]),
                            total_cpu_allocatable: parseFloat(promqlParsed.cpu_total?.series_list_result[0]?.values?.[index]),
                            account_id: accountId,
                          });
                        }
                        if (metric?.includes('memory')) {
                          response.push({
                            timestamp: formatDateTime(timestamp),
                            avg_memory_used_node: parseFloat(promqlParsed.memory_usage?.series_list_result[0]?.values?.[index]),
                            total_memory_allocatable: parseFloat(promqlParsed.memory_total?.series_list_result[0]?.values?.[index]),
                            account_id: accountId,
                          });
                        }
                        return response;
                      });
                    }
                  }
                }
              }
            }
          }
        }
      }
      return {
        data: {
          cloud_resource_metrics_groupings: result,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getMetrices({
    accountId,
    resourceId,
    metric,
    startDate,
    endDate,
    groupBy = ['tenant_id', 'account_id', 'timestamp'],
    limit = 100,
    dateUnit = 'day',
  }: {
    accountId?: string;
    resourceId?: string;
    metric?: string[];
    startDate?: Date;
    endDate?: Date;
    groupBy?: string[];
    limit: number;
    dateUnit: string;
  }) {
    if (accountId === 'demo' && metric && metric.includes('networkTransferBytes')) {
      const dashboardDemo = await getMockData('k8s-dashboard');
      return {
        data: dashboardDemo.network_groupings.data,
      };
    }
    try {
      const where: any = {};
      if (accountId) {
        where['account_id'] = { _eq: accountId };
      }
      if (resourceId) {
        where['resource_id'] = { _eq: resourceId };
      }
      if (metric) {
        where['metric'] = { _in: metric };
      }
      if (startDate && endDate) {
        where['timestamp'] = { _between: { _gte: startDate.toISOString(), _lte: endDate.toISOString() } };
      } else if (startDate) {
        where['timestamp'] = { _gte: startDate.toISOString() };
      } else if (endDate) {
        where['timestamp'] = { _lte: endDate.toISOString() };
      }

      let query = K8S_METRICS_GROUPINGS.replaceAll('__WHERE__', gqlStringify(where));
      query = query.replaceAll('__GROUPBY__', gqlStringify(`{${groupBy.join(',')}}`));
      const response = await queryGraphQL(query, 'MetricsGroupings', {
        limit: limit || 100,
        dateUnit: dateUnit || 'day',
      });
      return {
        data: {
          cloud_resource_metrics_groupings: response?.data?.data?.cloud_resource_metrics_groupings?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async getK8sMetrices({
    accountId,
    workloadFqdn,
    workloadType,
    podFqdn,
    resourceId,
    namespaceName,
    nodeName,
    startDate,
    endDate,
    isTrend = false,
    limit = 100,
    dateUnit = 'day',
  }: {
    accountId?: string;
    workloadFqdn?: string | string[];
    workloadType?: string[];
    podFqdn?: string[];
    resourceId?: string | string[];
    namespaceName?: string | string[];
    nodeName?: string | string[];
    startDate?: Date;
    endDate?: Date;
    isTrend?: boolean;
    limit: number;
    dateUnit: string;
  }) {
    try {
      const where: any = {};
      const groupBy = ['tenant_id'];

      if (accountId) {
        where['account_id'] = { _eq: accountId };
        groupBy.push('account_id');
      }
      if (resourceId) {
        if (Array.isArray(resourceId)) {
          where['resource_id'] = { _in: resourceId };
        } else {
          where['resource_id'] = { _eq: resourceId };
        }
        groupBy.push('resource_id');
      }
      if (workloadFqdn) {
        if (Array.isArray(workloadFqdn)) {
          where['workload_fqdn'] = { _in: workloadFqdn };
        } else {
          where['workload_fqdn'] = { _eq: workloadFqdn };
        }
        groupBy.push('namespace_name');
        groupBy.push('workload_name');
      }
      if (podFqdn) {
        if (Array.isArray(podFqdn)) {
          where['pod_fqdn'] = { _in: podFqdn };
        } else {
          where['pod_fqdn'] = { _eq: podFqdn };
        }
        groupBy.push('namespace_name');
        groupBy.push('pod_name');
      }
      if (workloadType) {
        if (Array.isArray(workloadType)) {
          where['workload_type'] = { _in: workloadType };
        } else {
          where['workload_type'] = { _eq: workloadType };
        }
        groupBy.push('workload_type');
      }
      if (namespaceName) {
        if (Array.isArray(namespaceName)) {
          where['namespace_name'] = { _in: namespaceName };
        } else {
          where['namespace_name'] = { _eq: namespaceName };
        }
        groupBy.push('namespace_name');
      }
      if (nodeName) {
        if (Array.isArray(nodeName)) {
          where['node_name'] = { _in: nodeName };
        } else {
          where['node_name'] = { _eq: nodeName };
        }
        groupBy.push('node_name');
      }

      if (isTrend) {
        groupBy.push('timestamp');
      }

      const last7Days = getStartOfDay(new Date());
      last7Days.setDate(last7Days.getDate() - 7);
      startDate = startDate || getStartOfDay(last7Days);
      endDate = endDate || getEndOfDay(new Date());

      if (startDate && endDate) {
        where['timestamp'] = { _between: { _gte: startDate.toISOString(), _lte: endDate.toISOString() } };
      } else if (startDate) {
        where['timestamp'] = { _gte: startDate.toISOString() };
      } else if (endDate) {
        where['timestamp'] = { _lte: endDate.toISOString() };
      }

      let query = K8S_POD_GROUPINGS.replaceAll('__WHERE__', gqlStringify(where));
      query = query.replaceAll('__GROUPBY__', gqlStringify(`{${groupBy.join(',')}}`));
      query = query.replaceAll('__ADDITIONAL_COLUMNS__', `${groupBy.join(' ')}`);
      const response = await queryGraphQL(query, 'PodGroupings', {
        limit: limit || 100,
        dateUnit: dateUnit || 'day',
      });
      return {
        data: {
          k8s_pod_groupings: response?.data?.data?.k8s_pod_groupings?.rows,
        },
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async hitRelayServer(data?: any, query_type_hint?: string) {
    if (data.body.account_id === 'demo') {
      const relayDemo = await getMockData('k8s-relay');
      if (data.body.action_name === 'query_loki' && query_type_hint) {
        return relayDemo['log_query'];
      } else if (data.body.action_name === 'service_map') {
        return relayDemo['service_map'];
      } else if (data.body.action_name === 'prometheus_enricher' && data.body.action_params?.promql_query?.includes('container_log_messages_total')) {
        return relayDemo['log_groups'];
      } else if (
        data.body.action_name == 'prometheus_enricher' &&
        data.body.action_params.promql_query.includes('container_sensitive_log_messages_total')
      ) {
        return relayDemo['container_sensitive_log_messages_total'];
      } else if (data.body.action_name === 'prometheus_enricher') {
        return relayDemo['prometheus_enricher'];
      } else if (data.body.action_name === 'prometheus_labels') {
        return relayDemo['prometheus_labels'];
      } else if (data.body.action_name === 'get_silences') {
        return relayDemo['get_silences'];
      } else if (data.body.action_name === 'get_resource' && data.body.action_params?.resource_type === 'services') {
        return relayDemo['k8s_services'];
      } else if (data.body.action_name === 'get_resource' && data.body.action_params?.resource_type === 'persistentvolumeclaims') {
        return relayDemo['k8s_persistentvolumeclaims'];
      } else if (data.body.action_name === 'get_resource' && data.body.action_params?.resource_type === 'persistentvolumes') {
        return relayDemo['k8s_persistentvolumes'];
      }
    }
    try {
      const response: any = await hitRelayServer(data);
      return response?.data;
    } catch (error) {
      console.error('failed to fetch data from relay', error);
      return error;
    }
  },
  async getInsights(accountId: string[], source: string[], resource_ids: string[]) {
    try {
      const where: any = { status: { _neq: 'CLOSED' } };
      if (accountId && accountId.length > 0) {
        where['account_id'] = { _in: accountId };
      }

      if (source) {
        where['source'] = { _in: source };
      }
      if (resource_ids.length > 0) {
        where['resource_id'] = { _in: resource_ids };
      }
      const query = GET_K8s_INSIGHTS.replaceAll('__WHERE__', gqlStringify(where));
      const response = await queryGraphQL(query, 'GetK8sInsights');
      return {
        data: parseInsightJsonFields(response?.data?.data?.insight_v2?.rows),
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.log('Your Error is', error);
      return error;
    }
  },
  async singleConfigAuotPilot(data?: any) {
    const response = await queryGraphQL(SINGLE_CONFIG_AUTO_PILOT, 'singleConfigAuotPilot', {
      data: data,
    });
    return {
      data: response?.data?.data?.autopilot_insert_one?.id,
      errors: response?.data?.errors,
    };
  },

  async getReplicaRightSizingData(account: string, namespace: string, deployment: string) {
    const response = await queryGraphQL(REPLICA_RIGHT_SIZING_SINGLE_WORKLOAD, 'getMetricFromML', {
      account: account,
      deployment: deployment,
      namespace: namespace,
    });
    return {
      data: response?.data?.data?.get_metrics_from_ml,
      error: response?.data?.error,
    };
  },
  async listCurrentReplicaByResourceIds(ids: string[]) {
    const formattedQuery = GET_CURRENT_REPLICA_BY_IDS.replaceAll('__WHERE__', gqlStringify({ id: { _in: ids } }));
    const response = await queryGraphQL(formattedQuery, 'GetCurrentReplicaByResourceIds', {});
    const rows = (response?.data?.data?.cloud_resourses?.rows || []).map((row: any) => {
      const meta = typeof row.meta === 'string' ? safeJSONParse(row.meta) : row.meta;
      return {
        ...row,
        total_pods: meta?.total_pods,
        namespace: meta?.namespace,
      };
    });
    return {
      data: rows,
      errors: response?.data?.errors,
    };
  },
  /**
   * Get event filter values using consolidated API
   * Fetches multiple filter types in a single request for better performance
   */
  async getEventFilterValues(params: { accountId?: string; filterTypes: string[]; startTime?: string; endTime?: string; limit?: number }) {
    if (params.accountId === 'demo') {
      return {
        data: { event_get_filter_values: { filters: [], account_id: null } },
      };
    }

    const query = `
      query GetEventFilterValues($request: EventFilterValuesRequest!) {
        event_get_filter_values(request: $request) {
          filters {
            filter_type
            values {
              value
            }
            total
          }
          account_id
        }
      }
    `;

    try {
      const response = await queryGraphQL(query, 'GetEventFilterValues', {
        request: {
          account_id: params.accountId || null,
          filter_types: params.filterTypes,
          start_time: params.startTime || null,
          end_time: params.endTime || null,
          limit: params.limit || null,
        },
      });
      return {
        data: response?.data?.data?.event_get_filter_values,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.log('failed to get event filter values', error);
      return { data: null, errors: [error] };
    }
  },
  async getKnowledgeBase(ruleName: string) {
    try {
      const response = await queryGraphQL(GET_KNOWLEDGE_BASE, 'GetKnowledgeBase', {
        rulename: ruleName,
      });
      return {
        data: response?.data?.data?.knowledge_base_v2?.rows,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.log('Failed to fetch knowledgeb base', error);
      return error;
    }
  },
  async getAllK8sNamespaces() {
    try {
      const response = await queryGraphQL(GET_NAMESPACES, 'GetK8sNamespaces', {});
      return {
        data: response?.data?.data?.k8s_namespaces?.rows,
        errors: response?.data?.errors,
      };
    } catch (error) {
      console.log('failed to fetch namespaces', error);
      return error;
    }
  },
  async getAllK8sWorkload(query: any = {}) {
    if (query.accountId == 'demo' || query.account_id == 'demo' || query.cloud_account_id == 'demo') {
      return {
        data: [],
      };
    }
    query = query || {};
    const where = [];
    if (query.accountId || query.account_id || query.cloud_account_id) {
      where.push({ account_id: { _eq: query.accountId || query.account_id || query.cloud_account_id } });
    }
    if (query.namespaceName || query.namespace_name || query.namespace) {
      if (Array.isArray(query.namespaceName || query.namespace_name || query.namespace)) {
        where.push({ namespace: { _in: query.namespaceName || query.namespace_name || query.namespace } });
      } else {
        where.push({ namespace: { _eq: query.namespaceName || query.namespace_name || query.namespace } });
      }
    }
    if (query.workloadName || query.workload_name || query.name) {
      where.push({ name: { _ilike: '%' + (query.workloadName || query.workload_name) + '%' } });
    }
    if (query.workloadType || query.workload_type || query.kind) {
      if (Array.isArray(query.workloadType) || Array.isArray(query.workload_type) || Array.isArray(query.kind)) {
        where.push({ kind: { _in: query.workloadType || query.workload_type || query.kind } });
      } else {
        where.push({ kind: { _eq: query.workloadType || query.workload_type || query.kind } });
      }
    } else {
      where.push({ kind: { _neq: 'ReplicaSet' } });
    }
    if (query.resource_ids) {
      where.push({ resource_id: { _in: query.resource_ids } });
    }

    let isActive = query.isActive || query.is_active;
    if (isActive === undefined || isActive === null) {
      isActive = true;
    }
    if ('allow_in_active_pod' in query && query.allow_in_active_pod) {
      // Do nothing, as we're allowing inactive pods
    } else {
      where.push({ is_active: { _eq: isActive } });
    }

    const formattedQuery = LIST_ALL_WORKLOADS.replaceAll('__WHERE__', gqlStringify({ _and: where }));

    const response = await queryGraphQL(formattedQuery, 'ListAllWorkloads', {});

    return {
      data: response?.data?.data?.k8s_workloads?.rows,
    };
  },
  async generateAiRecommendation(accountId: string, eventId: string, recommendationType: string, regenerate?: boolean) {
    const response = await queryGraphQL(GENEREATE_AI_RECOMMENDATION, 'GenerateAIRecommendation', {
      accountId: accountId,
      eventId: eventId,
      recommendationType: recommendationType,
      regenerate: regenerate || false,
    });
    return response?.data?.data?.generate_ai_recommendation?.data || parseHttpResponseBodyMessage(response?.data);
  },
  async scanImage({ accountId, namespace, workloadName }: { accountId: string; namespace: string; workloadName: string }) {
    const SCAN_IMAGE = `
      mutation ScanImage($accountId: String!, $namespace: String!, $workload: String!){
        security_scan_image(object:{
          account_id:$accountId,
          namespace:$namespace,
          workload:$workload
        }){
          data
        }
      }
    `;
    const response = await queryGraphQL(SCAN_IMAGE, 'ScanImage', {
      accountId: accountId,
      namespace: namespace,
      workload: workloadName,
    });
    return response?.data?.data?.security_scan_image?.data;
  },
  async getIssueEventCounts(
    query: any = {},
    cols: Array<string> = [
      'subject_type',
      'subject_name',
      'subject_namespace',
      'starts_at',
      'title',
      'finding_type',
      'cluster',
      'evidences',
      'status',
      'aggregation_key',
      'priority',
    ]
  ) {
    try {
      const filterParams: any = {};
      query = Object.fromEntries(Object.entries(query).filter(([_key, value]) => value !== '__ALL__'));
      if (query?.account_id) {
        if (Array.isArray(query['account_id'])) {
          filterParams['account_id'] = { _in: query['account_id'] };
        } else {
          filterParams['account_id'] = { _eq: query['account_id'] };
        }
      }
      if (query?.subject_namespace) {
        if (Array.isArray(query['subject_namespace'])) {
          filterParams['subject_namespace'] = { _in: query['subject_namespace'] };
        } else {
          filterParams['subject_namespace'] = { _eq: query['subject_namespace'] };
        }
      }
      if (query?.subject_type) {
        if (Array.isArray(query['subject_type'])) {
          filterParams['subject_type'] = { _in: query['subject_type'] };
        } else {
          filterParams['subject_type'] = { _eq: query['subject_type'] };
        }
      }
      if (query?.subject_name) {
        if (Array.isArray(query['subject_name'])) {
          filterParams['subject_name'] = { _in: query['subject_name'] };
        } else {
          filterParams['subject_name'] = { _like: query['subject_name'] + '%' };
        }
      }
      if (query?.cluster) {
        if (Array.isArray(query['cluster'])) {
          filterParams['cluster'] = { _in: query['cluster'] };
        } else {
          filterParams['cluster'] = { _eq: query['cluster'] };
        }
      }
      if (query?.title) {
        if (Array.isArray(query['title'])) {
          filterParams['title'] = { _in: query['title'] };
        } else {
          filterParams['title'] = { _eq: query['title'] };
        }
      }
      if (query?.finding_type) {
        if (Array.isArray(query['finding_type'])) {
          filterParams['finding_type'] = { _in: query['finding_type'] };
        } else {
          filterParams['finding_type'] = { _eq: query['finding_type'] };
        }
      }
      if (query?.aggregation_key) {
        if (Array.isArray(query['aggregation_key'])) {
          filterParams['aggregation_key'] = { _in: query['aggregation_key'] };
        } else {
          filterParams['aggregation_key'] = { _eq: query['aggregation_key'] };
        }
      }
      if (query?.priority) {
        if (Array.isArray(query['priority'])) {
          filterParams['priority'] = { _in: query['priority'] };
        } else {
          filterParams['priority'] = { _eq: query['priority'] };
        }
      }
      if (query?.status) {
        if (Array.isArray(query['status'])) {
          filterParams['status'] = { _in: query['status'] };
        } else {
          filterParams['status'] = { _eq: query['status'] };
        }
      }
      if (query?.resource_id) {
        if (Array.isArray(query['resource_id'])) {
          filterParams['resource_id'] = { _in: query['resource_id'] };
        } else {
          filterParams['resource_id'] = { _eq: query['resource_id'] };
        }
      }
      if (query?.resource_ids) {
        filterParams['resource_id'] = { _in: query.resource_ids };
      }

      const endDate = query.end_date || query.endDate || getEndOfMonth(new Date());
      const startDate = query.start_date || query.startDate || getStartOfMonth(new Date());

      filterParams['_and'] = [{ starts_at: { _gte: startDate.toISOString() } }, { starts_at: { _lte: endDate.toISOString() } }];

      let queryStr = ISSUE_EVENT_COUNT;

      queryStr = queryStr.replaceAll('__WHERE__', gqlStringify(filterParams));
      queryStr = queryStr.replaceAll('__COLS__', cols.join(' '));
      const response = await queryGraphQL(queryStr, 'IssueEventCount', {});
      return response?.data?.data?.event_groupings_v2?.rows ?? [];
    } catch (error) {
      console.log('Your Error is for IssueEventCount', error);
      return error;
    }
  },
  async getK8sNamespacesList(accountId: string | string[]) {
    if (accountId === 'demo') {
      return {
        data: {
          namespaces: [],
        },
      };
    }
    const query: any = {};
    query.is_active = { _eq: true };
    if (Array.isArray(accountId)) {
      query.account_id = { _in: accountId };
    } else {
      query.account_id = { _eq: accountId };
    }
    const response = await queryGraphQL(LIST_NAMESPACES.replaceAll('__WHERE__', gqlStringify(query)), 'ListNamespaces', {
      limit: 1000,
    });
    const data = response?.data?.data?.k8s_namespaces_v2?.rows || [];
    return data;
  },
  async getK8sResourceCost(query: any) {
    const where: any = {};

    if (query?.cloudProvider) {
      where.cloud_provider = { _eq: query.cloudProvider };
    }
    if (query?.resourceType) {
      where.resource_type = { _eq: query.resourceType };
    }
    if (query?.resourceRegion) {
      where.resource_region = { _eq: query.resourceRegion };
    }

    const response = await queryGraphQL(GET_K8S_RESOURCE_COST.replaceAll('__WHERE__', gqlStringify(where)), 'get_k8s_resource_cost');
    return {
      data: response?.data?.data,
      error: response?.data?.errors,
    };
  },
  async getNodeInfographics(query: any) {
    const where1: any = {};
    where1.cloud_account_id = { _eq: query.accountId };
    where1.is_active = { _eq: true };

    const GET_NODE_INFOGRAPHICS = `
    query NodeInfographics {
      full_nodes_aggregate: k8s_nodes_groupings_v2(where: __WHERE1__) {
        rows {
          count
        }
      }
      full_nodes: k8s_nodes_v2(where: __WHERE1__) {
        rows {
          meta
          node_type
          node_flavor
        }
      }
    }
    `;
    const response = await queryGraphQL(GET_NODE_INFOGRAPHICS.replaceAll('__WHERE1__', gqlStringify(where1)), 'NodeInfographics');
    const rawData = response?.data?.data;
    const count = rawData?.full_nodes_aggregate?.rows?.[0]?.count || 0;
    const nodes = (rawData?.full_nodes?.rows || []).map((node: any) => ({
      ...node,
      meta: typeof node.meta === 'string' ? safeJSONParse(node.meta) : node.meta,
    }));
    return {
      data: {
        data: {
          data: {
            full_nodes: {
              aggregate: { count },
              nodes,
            },
          },
        },
      },
      error: response?.data?.errors,
    };
  },
  async generateRCA(eventId: string, accountId: string, generate = false) {
    try {
      const query = `
        query getRcaForEvent($account_id:String!, $event_id:String!, $generate: Boolean!) {
          ai_get_rca(account_id: $account_id, event_id: $event_id, generate: $generate) {
            data
          }
        }
 `;
      const response = await queryGraphQL(query, 'getRcaForEvent', {
        account_id: accountId,
        event_id: eventId,
        generate: generate,
      });
      return response?.data?.data?.ai_get_rca?.data || null; // Return null if no data
    } catch (error) {
      console.error('Error generating RCA:', error);
      return null; // Return null in case of error
    }
  },
  async listFrameworkResources(accountId: string, frameworks: string[], status = '') {
    try {
      const CLOUD_RESOURCE_ATTRIBUTES = `
        query CloudResourceAttributes {
            cloud_resource_attributes: cloud_resource_attributes_v2(where: __WHERE__){
              rows {
                name
                value
                resource_uuid
                resource_arn
                resource_name
                resource_type
                resource_meta
                resource_status
                resource_created_at
                resource_updated_at
              }
            }
          }
    `;
      const query: any = {};
      query.account_id = { _eq: accountId };
      query.name = { _ilike: 'framework%' };
      if (frameworks) {
        query.value = { _in: frameworks };
      }
      const response = await queryGraphQL(CLOUD_RESOURCE_ATTRIBUTES.replace('__WHERE__', gqlStringify(query)), 'CloudResourceAttributes', {});
      const rows = response?.data?.data?.cloud_resource_attributes?.rows || [];
      // Transform to match original nested structure for backward compatibility
      const transformed = rows
        .filter((row: any) => {
          if (status) {
            const isActive = row.resource_status === 'active' || row.resource_status === 'Active';
            return status === 'Active' ? isActive : !isActive;
          }
          return true;
        })
        .map((row: any) => {
          const meta = typeof row.resource_meta === 'string' ? safeJSONParse(row.resource_meta) : row.resource_meta;
          return {
            name: row.name,
            value: row.value,
            cloud_resourse: {
              id: row.resource_uuid,
              arn: row.resource_arn,
              name: row.resource_name,
              type: row.resource_type,
              namespace: meta?.namespace,
              created_at: row.resource_created_at,
              updated_at: row.resource_updated_at,
              status: row.resource_status,
            },
          };
        });
      return transformed;
    } catch (error) {
      console.error('Error checking cloud_resource_attributes:', error);
      return []; // Return empty array in case of error
    }
  },
  async tracesServiceMap(data: any) {
    const SERVICE_MAP_VIA_TRACES = `
    mutation ServiceMapViaTraces {
      traces_service_map(request: __WHERE__) {
        data {
          labels
          applications {
            Id {
              kind
              name
              namespace
            }
            Instances {
              Id {
                kind
                name
                namespace
              }
              IsFailed
            }
            Downstreams {
              Id
              RequestCount
              FailureCount
              BytesReceived
              BytesSent
              DrillDown {
                error_types
                failed_trace_ids
                filter_hints {
                  protocol
                  span_attribute_filters
                  source_service
                  target_service
                }
                http_status_codes
                operations
                sample_trace_ids
                time_range {
                  end_time
                  start_time
                }
              }
              Latency
              Protocol
              Stats
              Status
              Weight
            }
            Upstreams {
              Id
              DrillDown {
                filter_hints {
                  target_service
                  span_attribute_filters
                  source_service
                  protocol
                }
                time_range {
                  end_time
                  start_time
                }
                failed_trace_ids
                sample_trace_ids
                error_types
                http_status_codes
                operations
              }
              RequestCount
              BytesReceived
              BytesSent
              FailureCount
              Latency
              Protocol
              Stats
              Status
              Weight
            }
            Category {
              category
            }
            CPUThrottlingTime
            DesiredInstances
            FailedInstances
            HealthReason
            Indicators
            IsHealthy
            Labels
            OOMKills
            Restarts
            Status
            Type
            VolumeSize
            VolumeUsed
          }
        }
      }
    }
    `;
    const query: any = {};
    query.account_id = data.accountId;
    if (data.namespace) {
      query.workload_namespace = data.namespace;
    }
    if (data.workloadName) {
      query.workload_name = data.workloadName;
    }
    if (data.start_time) {
      query.start_time = data.start_time;
    }
    if (data.end_time) {
      query.end_time = data.end_time;
    }
    if (Array.isArray(data.label_filter) && data.label_filter.length > 0) {
      query.label_filter = data.label_filter;
    }
    const response = await queryGraphQL(SERVICE_MAP_VIA_TRACES.replace('__WHERE__', gqlStringify(query)), 'ServiceMapViaTraces', {});
    return response;
  },

  async getResourceAttributes(resourceId: string) {
    try {
      const formattedQuery = GET_RESOURCE_ATTRIBUTES.replaceAll('__WHERE__', gqlStringify({ resource_id: { _eq: resourceId } }));
      const response = await queryGraphQL(formattedQuery, 'GetResourceAttributes', {});
      return response?.data?.data?.cloud_resource_attributes?.rows || [];
    } catch (error) {
      console.error('Error fetching resource attributes:', error);
      return [];
    }
  },

  async triggerCloudSync(accountId: string) {
    try {
      const response = await queryGraphQL(TRIGGER_CLOUD_ACCOUNT_SYNC, 'TriggerCloudAccountSync', {
        account_id: accountId,
      });
      return { data: response?.data?.data?.trigger_cloud_account_sync, errors: response?.data?.errors };
    } catch (error) {
      console.error('Error triggering cloud sync:', error);
      throw error;
    }
  },

  async relayForwardRequest(input: { body: Record<string, any>; no_sinks?: boolean; cache?: boolean; track_history?: boolean }) {
    if (input.body?.account_id === 'demo') {
      const relayDemo = await getMockData('k8s-relay');
      const actionName = input.body.action_name;
      const actionParams = input.body.action_params;
      if (actionName === 'service_map') {
        return { data: relayDemo['service_map']?.data, errors: null };
      } else if (actionName === 'query_loki') {
        return { data: relayDemo['log_query']?.data, errors: null };
      } else if (actionName === 'prometheus_enricher' && actionParams?.promql_query?.includes('container_log_messages_total')) {
        return { data: relayDemo['log_groups']?.data, errors: null };
      } else if (actionName === 'prometheus_enricher' && actionParams?.promql_query?.includes('container_sensitive_log_messages_total')) {
        return { data: relayDemo['container_sensitive_log_messages_total']?.data, errors: null };
      } else if (actionName === 'prometheus_enricher') {
        return { data: relayDemo['prometheus_enricher']?.data, errors: null };
      } else if (actionName === 'prometheus_labels') {
        return { data: relayDemo['prometheus_labels']?.data, errors: null };
      } else if (actionName === 'get_silences') {
        return { data: relayDemo['get_silences']?.data, errors: null };
      } else if (actionName === 'get_resource' && actionParams?.resource_type === 'services') {
        return { data: relayDemo['k8s_services']?.data, errors: null };
      } else if (actionName === 'get_resource' && actionParams?.resource_type === 'persistentvolumeclaims') {
        return { data: relayDemo['k8s_persistentvolumeclaims']?.data, errors: null };
      }
    }
    const response = await queryGraphQL(RELAY_FORWARD_REQUEST, 'RelayForwardRequest', {
      body: input.body,
      no_sinks: input.no_sinks,
      cache: input.cache,
      track_history: input.track_history,
    });
    return {
      data: response?.data?.data?.relay_forward_request?.data,
      errors: response?.data?.errors,
    };
  },

  async upsertResourceAttributes(resourceId: string, accountId: string, attributes: { name: string; value: string }[]) {
    try {
      const objects = attributes.map((attr) => ({
        resource_id: resourceId,
        account_id: accountId,
        name: attr.name,
        value: attr.value,
        labels: '{}',
      }));
      const response = await queryGraphQL(UPSERT_RESOURCE_ATTRIBUTES, 'UpsertResourceAttributes', { objects });
      return response;
    } catch (error) {
      console.error('Error upserting resource attributes:', error);
      throw error;
    }
  },
};

export default apiKubernetes;
