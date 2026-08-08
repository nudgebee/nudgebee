import type { AccountOption, Panel, PanelTarget, PanelType } from '@api1/dashboards';
import { buildEntityQuery, defaultDraft, findTable, type EntityQueryDraft } from './entityQuery';
import { panelScopeFromTypes, type PanelScope } from './panelAccounts';
import { nextPanelId } from './panelDefaults';

/**
 * The widget library — one authored panel each, ready to drop onto a dashboard.
 *
 * Everything here is a starting point, not a fixed asset: a widget is copied
 * into the dashboard the moment it is picked, and from then on it is an
 * ordinary panel the author owns. Nothing links back, so editing a copy never
 * changes the library and a library change never rewrites someone's dashboard.
 *
 * A widget carries no account scope of its own, because accounts are per panel
 * and belong to the tenant, not the catalogue. It does carry enough to derive
 * one: `accountKind` says what the query is about, and `defaultWidgetScope`
 * turns that into the account TYPES it can run against, so a copy opens
 * pre-scoped rather than on an empty account field. Types, never a pinned list
 * of somebody's accounts — that is a choice the author makes, not the library.
 *
 * Two rules the whole catalogue keeps:
 *
 *  - ONE target per panel. The panel editor rewrites target A in place, so a
 *    two-target widget would silently lose its second query the first time
 *    someone opened it.
 *  - NO template variables. A query that narrows to a namespace or a workload
 *    writes the matcher as `=~".*"` and the author edits it in the panel editor.
 *    `$name` tokens are only substituted when a dashboard is opened from a page
 *    that supplies them, so anywhere else they run as literal text — a panel
 *    querying for a namespace called `$namespace` finds nothing and looks like
 *    an empty chart rather than a mistake.
 */

export type TemplateRole = 'cto' | 'manager' | 'sre' | 'devops' | 'developer';

/** Roles in seniority order, which is also the order the filters read in. */
export const TEMPLATE_ROLES: { value: TemplateRole; label: string }[] = [
  { value: 'cto', label: 'CTO' },
  { value: 'manager', label: 'Manager' },
  { value: 'sre', label: 'SRE' },
  { value: 'devops', label: 'DevOps' },
  { value: 'developer', label: 'Developer' },
];

export function roleLabel(role: TemplateRole): string {
  return TEMPLATE_ROLES.find((r) => r.value === role)?.label || role;
}

export type WidgetCategory = 'Cost' | 'Issues' | 'Reliability' | 'Capacity' | 'Performance' | 'Workload';

/** A widget's panel body — everything except the identity a dashboard gives it. */
export type TemplatePanel = Pick<Panel, 'title' | 'description' | 'type' | 'datasource' | 'targets' | 'unit' | 'grid_pos'>;

/**
 * The kind of account a widget's query belongs on.
 *
 * Not derivable from the datasource, which is why it is declared. PromQL and
 * spans reach a cluster, so those are `cluster`. The query engine takes any
 * account, so what decides a findings widget is what it is ABOUT: savings and
 * recommendations live against cloud accounts, K8s events against the cluster.
 * A CloudWatch-native metrics widget, when one is written, declares `cloud` for
 * the same reason.
 */
export type WidgetAccountKind = 'cluster' | 'cloud';

export interface PanelTemplate {
  id: string;
  category: WidgetCategory;
  roles: TemplateRole[];
  /** The question this panel answers — the line under its title in the picker. */
  summary: string;
  /** Defaults to `cluster`, which is what all but the cost widgets want. */
  accountKind?: WidgetAccountKind;
  panel: TemplatePanel;
}

/** A chart or stat over a provider metric. */
function metricPanel(args: { title: string; description: string; expr: string; unit?: string; type?: PanelType; width?: number }): TemplatePanel {
  return {
    title: args.title,
    description: args.description,
    type: args.type || 'timeseries',
    datasource: 'metrics',
    targets: [{ ref_id: 'A', expr: args.expr }],
    unit: args.unit || '',
    grid_pos: { x: 0, y: 0, w: args.width ?? 6, h: 8 },
  };
}

/**
 * A table over the internal query engine (events, recommendations) or the
 * traces service.
 *
 * The query is compiled through the same `buildEntityQuery` the panel editor
 * uses, so a widget round-trips through the builder unchanged — a hand-written
 * query object would drift from whatever the builder produces the moment either
 * side gained a field.
 */
function entityPanel(args: {
  title: string;
  description: string;
  draft: Partial<EntityQueryDraft> & { table: string };
  width?: number;
}): TemplatePanel {
  const table = findTable(args.draft.table);
  const full: EntityQueryDraft = { ...defaultDraft(table.value), ...args.draft };
  const target: PanelTarget = {
    ref_id: 'A',
    query: buildEntityQuery(full) as unknown as Record<string, unknown>,
    time_column: full.applyTimeRange ? full.timeColumn : '',
  };
  return {
    title: args.title,
    description: args.description,
    type: 'table',
    datasource: table.datasource,
    targets: [target],
    unit: '',
    // Taller than a chart: a table showing five rows of a ten-row query reads as
    // if that were all there was.
    grid_pos: { x: 0, y: 0, w: args.width ?? 6, h: 10 },
  };
}

export const PANEL_TEMPLATES: PanelTemplate[] = [
  // ── Cost ────────────────────────────────────────────────────────────────
  {
    id: 'savings-by-rule',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager', 'devops'],
    summary: 'Which kind of waste is worth the most, ranked by total savings.',
    panel: entityPanel({
      title: 'Savings opportunity by rule',
      description: 'Open recommendations grouped by the rule that raised them, ranked by what they add up to.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['rule_name', 'category', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'savings-by-account',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager'],
    summary: 'Where the money is, by cloud account.',
    panel: entityPanel({
      title: 'Savings opportunity by account',
      description: 'The same recommendations grouped by account, so the spend conversation has an owner.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['account_name', 'account_cloud_provider', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'savings-by-service',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['cto', 'manager', 'devops'],
    summary: 'Which cloud service the waste sits in — compute, storage, database.',
    panel: entityPanel({
      title: 'Savings opportunity by service',
      description: 'Recommendations grouped by the cloud service and resource type they were raised against.',
      draft: {
        table: 'recommendation_groupings_v2',
        columns: ['resource_cloud_service', 'resource_type', 'count', 'sum_estimated_savings'],
        sortColumn: 'sum_estimated_savings',
        limit: 15,
      },
    }),
  },
  {
    id: 'top-savings-resources',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['manager', 'devops', 'sre'],
    summary: 'The individual resources to act on first.',
    panel: entityPanel({
      title: 'Top resources to right-size',
      description:
        'Open recommendations, biggest saving first. Filtered to primary revisions so one finding is counted once rather than once per revision.',
      draft: {
        table: 'recommendations_v2',
        columns: ['resource_name', 'resource_type', 'rule_name', 'estimated_savings', 'severity', 'status'],
        filters: [
          { column: 'status', operator: '_eq', value: 'Open' },
          { column: 'is_primary_recommendation', operator: '_eq', value: 'true' },
        ],
        sortColumn: 'estimated_savings',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'critical-recommendations',
    category: 'Cost',
    accountKind: 'cloud',
    roles: ['sre', 'devops', 'manager'],
    summary: 'The findings rated Critical or High, whatever they are worth.',
    panel: entityPanel({
      title: 'Critical & high findings',
      description: 'Open recommendations at the top two severities — risk rather than spend, so savings are not the sort order.',
      draft: {
        table: 'recommendations_v2',
        columns: ['created_at', 'rule_name', 'category', 'severity', 'resource_name', 'resource_type'],
        filters: [
          { column: 'status', operator: '_eq', value: 'Open' },
          { column: 'is_primary_recommendation', operator: '_eq', value: 'true' },
          { column: 'severity', operator: '_in', value: 'Critical, High' },
        ],
        sortColumn: 'created_at',
        limit: 20,
      },
      width: 12,
    }),
  },

  // ── Issues ──────────────────────────────────────────────────────────────
  {
    id: 'noisiest-issues',
    category: 'Issues',
    roles: ['cto', 'manager', 'sre'],
    summary: 'What is firing most often — the backlog behind the on-call load.',
    panel: entityPanel({
      title: 'Noisiest issues',
      description: 'Repeat occurrences of the same issue collapsed into one row, most frequent first.',
      draft: {
        table: 'event_groupings_v2',
        columns: ['title', 'subject_namespace', 'subject_name', 'priority', 'event_count', 'max_created_at'],
        sortColumn: 'event_count',
        limit: 15,
      },
      width: 12,
    }),
  },
  {
    id: 'issues-by-namespace',
    category: 'Issues',
    roles: ['manager', 'sre', 'devops'],
    summary: 'Which team’s namespace is generating the alerts.',
    panel: entityPanel({
      title: 'Issues by namespace',
      description: 'Event volume per namespace, split by priority and by what the issue was about.',
      draft: {
        table: 'event_groupings_v2',
        columns: ['subject_namespace', 'event_count', 'count_priority_p0', 'count_priority_p1', 'count_new_issues'],
        sortColumn: 'event_count',
        limit: 15,
      },
    }),
  },
  {
    id: 'p0-p1-events',
    category: 'Issues',
    roles: ['manager', 'sre', 'devops'],
    summary: 'The events that actually warranted waking someone.',
    panel: entityPanel({
      title: 'P0 & P1 events',
      description: 'Individual events at the top two priorities in the selected time range.',
      draft: {
        table: 'events_v2',
        columns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'subject_namespace', 'status'],
        filters: [{ column: 'priority', operator: '_in', value: 'P0, P1' }],
        sortColumn: 'starts_at',
        limit: 25,
      },
      width: 12,
    }),
  },
  {
    id: 'new-issues',
    category: 'Issues',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Problems seen for the first time — usually the last deploy.',
    panel: entityPanel({
      title: 'New issues in this window',
      description: 'Events flagged as a first occurrence rather than a recurrence of something already known.',
      draft: {
        table: 'events_v2',
        columns: ['starts_at', 'title', 'priority', 'subject_type', 'subject_name', 'subject_namespace'],
        filters: [{ column: 'is_new_issue', operator: '_eq', value: 'true' }],
        sortColumn: 'starts_at',
        limit: 25,
      },
      width: 12,
    }),
  },

  // ── Capacity ────────────────────────────────────────────────────────────
  {
    id: 'cluster-cpu-utilisation',
    category: 'Capacity',
    roles: ['cto', 'manager', 'sre', 'devops'],
    summary: 'How much of the CPU you pay for is actually being burned.',
    panel: metricPanel({
      title: 'Cluster CPU utilisation',
      description: 'Non-idle CPU across every node, as a share of the cores the cluster has.',
      expr: '100 * sum(rate(node_cpu_seconds_total{mode!="idle"}[5m])) / sum(machine_cpu_cores)',
      unit: '%',
    }),
  },
  {
    id: 'cluster-memory-utilisation',
    category: 'Capacity',
    roles: ['cto', 'manager', 'sre', 'devops'],
    summary: 'The same question for memory.',
    panel: metricPanel({
      title: 'Cluster memory utilisation',
      description: 'Memory in use across every node, as a share of what is installed.',
      expr: '100 * (1 - sum(node_memory_MemAvailable_bytes) / sum(node_memory_MemTotal_bytes))',
      unit: '%',
    }),
  },
  {
    id: 'cpu-requests-vs-allocatable',
    category: 'Capacity',
    roles: ['sre', 'devops', 'manager'],
    summary: 'How much CPU is reserved but idle — the gap that costs money.',
    panel: metricPanel({
      title: 'CPU requests vs allocatable',
      description:
        'What workloads have reserved, against what the nodes can hand out. Read next to actual utilisation: a high number here with a low number there is over-request.',
      expr: '100 * sum(kube_pod_container_resource_requests{resource="cpu"}) / sum(kube_node_status_allocatable{resource="cpu"})',
      unit: '%',
    }),
  },
  {
    id: 'memory-requests-vs-allocatable',
    category: 'Capacity',
    roles: ['sre', 'devops', 'manager'],
    summary: 'The same reservation gap for memory.',
    panel: metricPanel({
      title: 'Memory requests vs allocatable',
      description: 'Reserved memory against what the nodes can hand out.',
      expr: '100 * sum(kube_pod_container_resource_requests{resource="memory"}) / sum(kube_node_status_allocatable{resource="memory"})',
      unit: '%',
    }),
  },
  {
    id: 'node-cpu-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'The ten hottest nodes — where a hotspot hides behind a healthy average.',
    panel: metricPanel({
      title: 'Busiest nodes by CPU',
      description: 'Per-node CPU utilisation, top ten. A cluster averaging 40% can still have a node at 95%.',
      expr: 'topk(10, 100 * sum by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[5m])) / count by (instance) (node_cpu_seconds_total{mode="idle"}))',
      unit: '%',
    }),
  },
  {
    id: 'node-filesystem-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Root disks filling up, before the kubelet starts evicting.',
    panel: metricPanel({
      title: 'Node root filesystem used',
      description: 'Used share of the root filesystem, per node.',
      expr: '100 * (1 - sum by (instance) (node_filesystem_avail_bytes{mountpoint="/"}) / sum by (instance) (node_filesystem_size_bytes{mountpoint="/"}))',
      unit: '%',
    }),
  },
  {
    id: 'pvc-utilisation',
    category: 'Capacity',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Volumes about to run out — the failure nobody sees coming.',
    panel: metricPanel({
      title: 'Fullest persistent volumes',
      description: 'Used share of each PersistentVolumeClaim, top ten.',
      expr: 'topk(10, 100 * sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_used_bytes) / sum by (namespace, persistentvolumeclaim) (kubelet_volume_stats_capacity_bytes))',
      unit: '%',
    }),
  },
  {
    id: 'ready-nodes',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Nodes currently Ready.',
    panel: metricPanel({
      title: 'Ready nodes',
      description: 'Nodes reporting the Ready condition.',
      expr: 'sum(kube_node_status_condition{condition="Ready",status="true"})',
      unit: 'nodes',
      type: 'stat',
      width: 3,
    }),
  },
  {
    id: 'unschedulable-nodes',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Nodes cordoned off — deliberately, or by a stuck drain.',
    panel: metricPanel({
      title: 'Unschedulable nodes',
      description: 'Nodes marked unschedulable. A number that stays above zero is usually a drain nobody finished.',
      expr: 'sum(kube_node_spec_unschedulable)',
      unit: 'nodes',
      type: 'stat',
      width: 3,
    }),
  },
  {
    id: 'pods-per-node',
    category: 'Capacity',
    roles: ['sre', 'devops'],
    summary: 'Scheduling skew — pods piled onto a few nodes.',
    panel: metricPanel({
      title: 'Pods per node',
      description: 'Scheduled pods per node, top ten.',
      expr: 'topk(10, count by (node) (kube_pod_info))',
      unit: 'pods',
    }),
  },

  // ── Reliability ─────────────────────────────────────────────────────────
  {
    id: 'pending-pods',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Pods that cannot be scheduled — capacity or affinity, never both.',
    panel: metricPanel({
      title: 'Pending pods',
      description: 'Pods stuck in Pending. Sustained above zero means the scheduler cannot place them.',
      expr: 'sum(kube_pod_status_phase{phase="Pending"})',
      unit: 'pods',
    }),
  },
  {
    id: 'failed-pods',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Pods that gave up entirely.',
    panel: metricPanel({
      title: 'Failed pods',
      description: 'Pods in the Failed phase.',
      expr: 'sum(kube_pod_status_phase{phase="Failed"})',
      unit: 'pods',
    }),
  },
  {
    id: 'pod-restarts-by-namespace',
    category: 'Reliability',
    roles: ['manager', 'sre', 'devops', 'developer'],
    summary: 'Restart churn, and whose namespace it is in.',
    panel: metricPanel({
      title: 'Container restarts by namespace',
      description: 'Restarts in the last hour, top ten namespaces. The single best proxy for "something is quietly broken".',
      expr: 'topk(10, sum by (namespace) (increase(kube_pod_container_status_restarts_total[1h])))',
      unit: 'restarts',
    }),
  },
  {
    id: 'container-waiting-reasons',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Why containers are not starting — CrashLoop, ImagePull, config.',
    panel: metricPanel({
      title: 'Containers waiting, by reason',
      description: 'Containers stuck in a waiting state, grouped by the reason the kubelet reported.',
      expr: 'sum by (reason) (kube_pod_container_status_waiting_reason) > 0',
      unit: 'containers',
    }),
  },
  {
    id: 'oomkilled-containers',
    category: 'Reliability',
    roles: ['sre', 'devops', 'developer'],
    summary: 'Containers killed for exceeding their memory limit.',
    panel: metricPanel({
      title: 'OOMKilled containers',
      description: 'Containers whose last termination was an out-of-memory kill, by namespace. Either the limit is too low or the service leaks.',
      expr: 'sum by (namespace) (kube_pod_container_status_terminated_reason{reason="OOMKilled"})',
      unit: 'containers',
    }),
  },
  {
    id: 'deployments-unavailable',
    category: 'Reliability',
    roles: ['manager', 'sre', 'devops'],
    summary: 'Deployments not running the replicas they were asked for.',
    panel: metricPanel({
      title: 'Deployments with unavailable replicas',
      description: 'Replicas short of the desired count, per deployment. During a rollout this spikes and recovers; a flat line is a stuck rollout.',
      expr: 'sum by (namespace, deployment) (kube_deployment_status_replicas_unavailable) > 0',
      unit: 'replicas',
    }),
  },
  {
    id: 'daemonsets-unavailable',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Node agents missing from nodes they should be on.',
    panel: metricPanel({
      title: 'DaemonSets with unavailable pods',
      description: 'DaemonSet pods missing on nodes that should be running them — often the reason a node looks unmonitored.',
      expr: 'sum by (namespace, daemonset) (kube_daemonset_status_number_unavailable) > 0',
      unit: 'pods',
    }),
  },
  {
    id: 'failed-jobs',
    category: 'Reliability',
    roles: ['devops', 'developer'],
    summary: 'Batch jobs and CronJobs that failed.',
    panel: metricPanel({
      title: 'Failed jobs',
      description: 'Jobs reporting failed pods. Nothing pages on these, which is exactly why they need a panel.',
      expr: 'sum by (namespace, job_name) (kube_job_status_failed) > 0',
      unit: 'jobs',
    }),
  },
  {
    id: 'pvc-not-bound',
    category: 'Reliability',
    roles: ['sre', 'devops'],
    summary: 'Volume claims that never got a volume.',
    panel: metricPanel({
      title: 'PVCs not bound',
      description: 'PersistentVolumeClaims in any phase other than Bound — a pod waiting on one of these will never start.',
      expr: 'sum by (namespace, persistentvolumeclaim) (kube_persistentvolumeclaim_status_phase{phase!="Bound"}) > 0',
      unit: 'claims',
    }),
  },

  // ── Performance ─────────────────────────────────────────────────────────
  {
    id: 'request-rate-by-service',
    category: 'Performance',
    roles: ['sre', 'developer', 'manager'],
    summary: 'Traffic, per service. The first of the four golden signals.',
    panel: metricPanel({
      title: 'Request rate by service',
      description: 'HTTP requests per second reaching each workload.',
      expr: 'sum by (destination_workload_name) (rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m]))',
      unit: 'req/s',
    }),
  },
  {
    id: 'error-rate-percent',
    category: 'Performance',
    roles: ['cto', 'manager', 'sre', 'developer'],
    summary: 'The share of requests failing — the number an SLO is written against.',
    panel: metricPanel({
      title: 'Error rate',
      description: '5xx responses as a share of all requests.',
      expr: '100 * sum(rate(container_http_requests_total{status=~"5..", destination_workload_namespace=~".*"}[5m])) / sum(rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m]))',
      unit: '%',
    }),
  },
  {
    id: 'p99-latency-by-service',
    category: 'Performance',
    roles: ['sre', 'developer', 'manager'],
    summary: 'Tail latency, per service — what your slowest users actually feel.',
    panel: metricPanel({
      title: 'p99 latency by service',
      description: '99th percentile request duration per workload.',
      expr: 'histogram_quantile(0.99, sum by (le, destination_workload_name) (rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace=~".*"}[5m])))',
      unit: 's',
    }),
  },
  {
    id: 'p50-latency-by-service',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Median latency, to tell a slow tail from a slow service.',
    panel: metricPanel({
      title: 'p50 latency by service',
      description: 'Median request duration per workload. Read beside p99: both rising is a slow service, only p99 rising is a tail problem.',
      expr: 'histogram_quantile(0.50, sum by (le, destination_workload_name) (rate(container_http_requests_duration_seconds_total_bucket{destination_workload_namespace=~".*"}[5m])))',
      unit: 's',
    }),
  },
  {
    id: 'top-endpoints-by-traffic',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'The busiest routes, which is where optimisation pays.',
    panel: metricPanel({
      title: 'Busiest endpoints',
      description: 'Request rate per path, top ten.',
      expr: 'topk(10, sum by (path, destination_workload_name) (rate(container_http_requests_total{destination_workload_namespace=~".*"}[5m])))',
      unit: 'req/s',
    }),
  },
  {
    id: 'error-log-volume',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Which containers are shouting — errors nobody turned into an alert.',
    panel: metricPanel({
      title: 'Error log volume by container',
      description: 'Error and critical log lines per container, top ten.',
      expr: 'topk(10, sum by (container_id) (increase(container_log_messages_total{level=~"critical|error"}[5m])))',
      unit: 'lines',
    }),
  },
  {
    id: 'network-receive-by-namespace',
    category: 'Performance',
    roles: ['sre', 'devops'],
    summary: 'Inbound traffic per namespace — cross-zone bills start here.',
    panel: metricPanel({
      title: 'Network received by namespace',
      description: 'Bytes per second received by containers, per namespace.',
      expr: 'sum by (namespace) (rate(container_network_receive_bytes_total[5m]))',
      unit: 'B/s',
    }),
  },
  {
    id: 'slowest-service-calls',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'The slowest calls between services, from traces rather than metrics.',
    panel: entityPanel({
      title: 'Slowest service calls',
      description: 'Spans grouped by caller and operation, worst p99 first. Latency percentiles exist only on this table.',
      draft: {
        table: 'traces_groupings_v2',
        columns: ['workload_name', 'workload_namespace', 'span_name', 'count', 'error_count', 'p99_latency'],
        sortColumn: 'p99_latency',
        limit: 20,
      },
      width: 12,
    }),
  },
  {
    id: 'failing-service-calls',
    category: 'Performance',
    roles: ['sre', 'developer'],
    summary: 'Which call between which two services is failing.',
    panel: entityPanel({
      title: 'Failing service calls',
      description: 'The same grouping ranked by error count — the caller/callee pair behind an error-rate spike.',
      draft: {
        table: 'traces_groupings_v2',
        columns: ['workload_name', 'span_name', 'destination_workload_name', 'http_status_code', 'count', 'error_count'],
        sortColumn: 'error_count',
        limit: 20,
      },
      width: 12,
    }),
  },

  // ── Workload ────────────────────────────────────────────────────────────
  {
    id: 'workload-cpu-usage',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'CPU burned by one service’s pods.',
    panel: metricPanel({
      title: 'CPU usage by pod',
      description: 'CPU cores used per pod of the selected workload.',
      expr: 'sum by (pod) (rate(container_cpu_usage_seconds_total{namespace=~".*", pod=~".*"}[5m]))',
      unit: 'cores',
    }),
  },
  {
    id: 'workload-memory-usage',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'Working-set memory per pod — the number the OOM killer reads.',
    panel: metricPanel({
      title: 'Memory usage by pod',
      description: 'Working-set bytes per pod of the selected workload.',
      expr: 'sum by (pod) (container_memory_working_set_bytes{namespace=~".*", pod=~".*"})',
      unit: 'bytes',
    }),
  },
  {
    id: 'workload-restarts',
    category: 'Workload',
    roles: ['developer', 'sre'],
    summary: 'Restarts of one service, pod by pod.',
    panel: metricPanel({
      title: 'Restarts by pod',
      description: 'Container restarts in the last hour, per pod of the selected workload.',
      expr: 'sum by (pod) (increase(kube_pod_container_status_restarts_total{namespace=~".*", pod=~".*"}[1h]))',
      unit: 'restarts',
    }),
  },
  {
    id: 'workload-replicas-available',
    category: 'Workload',
    roles: ['developer', 'devops'],
    summary: 'Replicas actually serving, through a deploy and after it.',
    panel: metricPanel({
      title: 'Available replicas',
      description: 'Replicas reporting available for the selected deployment.',
      expr: 'sum by (deployment) (kube_deployment_status_replicas_available{namespace=~".*", deployment=~".*"})',
      unit: 'replicas',
    }),
  },
];

export function findPanelTemplate(id: string): PanelTemplate | undefined {
  return PANEL_TEMPLATES.find((t) => t.id === id);
}

export function widgetAccountKind(widget: PanelTemplate): WidgetAccountKind {
  return widget.accountKind || 'cluster';
}

/**
 * The scope to open a widget on, given the accounts the viewer can see.
 *
 * Always an account TYPE, never a list of accounts. A type means "every account
 * of this provider" and is resolved at render, so the panel widens on its own as
 * accounts are connected; pinning ids at authoring time freezes it, and a
 * pre-ticked list of somebody's accounts is a choice we have no business making
 * for them. Leaving Accounts empty in the editor says the same thing and reads
 * as a default rather than a decision.
 *
 * The provider is read off a connected account by `kind` rather than compared
 * against the literal 'K8S' / 'AWS' — those are backend vocabulary, and `kind`
 * is the field that actually means "cluster" or "cloud".
 *
 * Empty when nothing fits. A tenant with no cluster cannot scope a PromQL
 * widget, and the red "No account" chip in the editor is the honest answer —
 * better than pre-selecting an account that will not answer.
 */
export function defaultWidgetScope(widget: PanelTemplate, accounts: AccountOption[]): PanelScope {
  const wanted = widgetAccountKind(widget) === 'cloud' ? 'cloud' : 'kubernetes';
  const providers = [...new Set(accounts.filter((a) => a.kind === wanted).map((a) => a.cloud_provider))].filter(Boolean);

  // A findings widget reads the query engine, which is the one datasource that
  // spans providers — so a cost widget opens on EVERY cloud provider rather than
  // whichever happened to be connected first. Everything else resolves one
  // provider's integration and takes the first match.
  if (widget.panel.datasource === 'nudgebee') return panelScopeFromTypes(providers, [], accounts);
  return { account_type: providers[0] || undefined, account_ids: [] };
}

/**
 * Copies a widget into a dashboard's panel list.
 *
 * The copy opens on the scope its datasource implies — a PromQL widget on the
 * cluster accounts, a findings widget on all of them — rather than on an empty
 * account field. We know where the query has to run; making the author work
 * that out for each of 41 widgets is asking them to re-derive something the
 * datasource already settled.
 *
 * It is a starting point, not a decision: the copy goes to the panel editor
 * with the scope pre-filled and visible, where it can be changed before
 * anything is saved. Passing no accounts leaves the scope empty, which is the
 * honest answer for a caller that has none to offer.
 */
export function panelFromTemplate(template: PanelTemplate, existing: Panel[], accounts: AccountOption[] = []): Panel {
  return {
    ...template.panel,
    id: nextPanelId(existing),
    ...defaultWidgetScope(template, accounts),
    // Deep-copied: the catalogue is a module-level singleton, and a panel edited
    // in place would otherwise rewrite the library for the rest of the session.
    targets: (template.panel.targets || []).map((t) => ({ ...t, ...(t.query ? { query: JSON.parse(JSON.stringify(t.query)) } : {}) })),
    grid_pos: { ...template.panel.grid_pos },
  };
}
