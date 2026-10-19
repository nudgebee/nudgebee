/**
 * Data layer for the self-hosted VM fleet page (/vm).
 *
 * A "VM account" is a cloud_accounts row with cloud_provider = 'SelfHosted' and
 * account_type = 'vm' (see AddSelfHostedAccountModal). Four things hang off it,
 * and this module is the single place that knows where each one lives:
 *
 *   VMs           cloud_resourses rows owned by the account (cloud_resources_list_v2)
 *   Agents        vm_agent integration configs + relay connection health
 *   Scan targets  forager discovery integrations; their id is the `datasource_id`
 *                 a scan runs through
 *   Packages      vm_package rows, written by services/vmpackage after a scan
 *   Findings      recommendation rows with rule_name = 'vm_package_vulnerability'
 *
 * The last two arrive with the services-server VM-scan pipeline (#35405); the
 * queries here are inert until that lands.
 */
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { joinFleetMetrics, vmPrivateIp } from '@components/vm/vmMetricsData';
import apiHome from '@api1/home';
import k8sApi from '@api1/kubernetes';
import ticketsApi from '@api1/tickets';

/** rule_name every VM vulnerability finding is written under. */
export const VM_VULNERABILITY_RULE = 'vm_package_vulnerability';

/** Statuses that count as an unresolved finding. */
export const OPEN_STATUSES = ['Open', 'InProgress'];

// The scan pipeline's mapSeverity (services/vmpackage/persist.go) only ever
// writes these five — negligible/unknown/blank all collapse to Info — so a
// filter offering Negligible or Unknown matched zero rows while Info, a real
// value, was unfilterable.
export const SEVERITY_ORDER = ['Critical', 'High', 'Medium', 'Low', 'Info'];

export interface VmResource {
  id: string;
  name: string;
  resourse_id: string;
  type: string;
  status: string;
  region: string;
  meta: Record<string, any>;
  tags: Record<string, any>;
  created_at: string;
  resourse_created_on: string;
  total_count?: number;
}

/**
 * One installed package, rolled up across the machines that carry it. vm_package
 * stores a row per (package, VM); the listing groups those back together so the
 * table reads "this package, on these VMs" rather than repeating the package
 * once per machine. Every displayed field is part of the group key, so a row is
 * never an average of two different things — only the VM list is aggregated.
 */
export interface VmPackage {
  os_family: string;
  os_version: string;
  pkg_type: string;
  name: string;
  version: string;
  arch: string;
  epoch: number | null;
  source_name: string;
  source_version: string;
  /** cloud_resourses ids of the VMs this package is installed on. */
  resource_ids: string[];
  last_seen_at: string;
}

/**
 * The `recommendation` JSON column as services/vmpackage writes it
 * (findingPayload in persist.go).
 */
export interface VmVulnerabilityPayload {
  vuln_id?: string;
  package?: { name?: string; version?: string; arch?: string; type?: string };
  fixed_version?: string;
  fix_state?: string;
  fix_channel?: string;
  cvss_v3_score?: number;
  cvss_v3_vector?: string;
  epss?: number;
  kev?: boolean;
  risk?: number;
  advisory_ids?: string[];
  data_source?: string;
  description?: string;
}

export interface VmVulnerability {
  id: string;
  account_id: string;
  resource_id: string;
  resource_name: string;
  severity: string;
  /** Ranked severity — sort key only, never rendered. */
  severity_weight: number;
  status: string;
  account_object_id: string;
  updated_at: string;
  created_at: string;
  recommendation: VmVulnerabilityPayload;
  ticket?: { ticket_id: string; url?: string };
}

/** Dimension the vulnerability list can be rolled up by. */
export type VmVulnerabilityGrouping = 'vulnerability' | 'package' | 'vm';

export interface VmVulnerabilityGroup {
  /** Value the findings were grouped on — CVE id, package name, or VM resource id. */
  key: string;
  label: string;
  count: number;
  /** Worst severity in the group, as the ranked weight (Critical 10 → Info 1). */
  max_severity_weight: number;
  /** VMs the group's findings sit on. Empty for the VM grouping — the row is one. */
  resource_names: string[];
}

export interface VmAgent {
  id: string;
  name: string;
  status: string;
  created_at: string;
  connected: boolean;
  last_seen_at?: string;
  version?: string;
}

export interface VmScanTarget {
  id: string;
  name: string;
  status: string;
  host: string;
  username: string;
}

// Credential-free listing of the integrations attached to one account, readable
// by account-scoped roles. integrations_list is tenant-admin-only (its rows carry
// integration_config_values) and apiIntegrations.listIntegrations swallows the
// resulting 403 into an empty list, which is how account users saw "0 / 0"
// proxy agents and an empty scan-target picker (#39858).
const LIST_ACCOUNT_INTEGRATIONS = `
query ListAccountIntegrations {
  integrations: cloud_list_account_integrations(where: __WHERE__, limit: 100, offset: 0, order_by: [{column: "name", order: asc}]) {
    rows {
      id
      name
      type
      status
      created_at
    }
  }
}`;
/** One vm_scan_status row — see #38466. status is 'started' | 'completed' | 'failed'. */
export interface VmScanStatus {
  cloudResourceId: string;
  status: string;
  error: string;
  packageCount: number;
  findingCount: number;
  scannedAt: string;
  updatedAt: string;
}

const LIST_VMS = `
query ListVms($limit: Int, $offset: Int) {
  vms: cloud_resources_list_v2(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "name", order: asc}]) {
    rows {
      id
      name
      resourse_id
      type
      status
      region
      meta
      tags
      created_at
      resourse_created_on
      total_count
    }
  }
}`;

// Grouped by everything the table shows except the VM, so the only thing the
// aggregate collapses is which machines carry the package. The total counts
// groups, not rows, so it is a count(DISTINCT …) over the same key.
//
// Both fields pass `columns:` explicitly: the gateway forwards the whole
// document to each field and the handler picks the selection set by field name,
// not alias, so the second call to an action would otherwise inherit the first
// one's columns.
const PACKAGE_GROUP_KEY = ['name', 'version', 'arch', 'epoch', 'pkg_type', 'source_name', 'source_version', 'os_family', 'os_version'];

const LIST_VM_PACKAGES = `
query ListVmPackages($limit: Int, $offset: Int) {
  packages: cloud_vm_package_groupings_v2(where: __WHERE__, group_by: __GROUP_BY__, columns: __COLUMNS__, limit: $limit, offset: $offset, order_by: [{column: "name", order: asc}, {column: "version", order: asc}]) {
    rows {
      name
      version
      arch
      epoch
      pkg_type
      source_name
      source_version
      os_family
      os_version
      resource_ids
      max_last_seen_at
    }
  }
  packages_aggregate: cloud_vm_package_groupings_v2(where: __WHERE__, columns: ["count"], column_transformations: [{name: "count", expr: "distinct", args: __GROUP_BY__}]) {
    rows {
      count
    }
  }
}`;

const PACKAGE_COUNTS_BY_VM = `
query VmPackageCounts {
  packages_aggregate: cloud_vm_package_groupings_v2(where: __WHERE__, group_by: ["cloud_resource_id"]) {
    rows {
      cloud_resource_id
      count
      max_last_seen_at
    }
  }
}`;

const VM_OS_BY_VM = `
query VmOsFamilies {
  packages_aggregate: cloud_vm_package_groupings_v2(where: __WHERE__, group_by: ["cloud_resource_id", "os_family", "os_version"]) {
    rows {
      cloud_resource_id
      os_family
      os_version
    }
  }
}`;

// severity_weight is the ranked form of severity (Critical 10 → Info 1); ordering
// by the raw string would put Medium above High. It has to be selected as well as
// ordered on — the query engine emits the bare column name in ORDER BY, which
// Postgres resolves against the SELECT alias.
const LIST_VM_VULNERABILITIES = `
query ListVmVulnerabilities($limit: Int, $offset: Int) {
  findings: recommendations_list(where: __WHERE__, limit: $limit, offset: $offset, order_by: [{column: "severity_weight", order: desc}, {column: "updated_at", order: desc}]) {
    rows {
      id
      account_id
      resource_id
      resource_name
      severity
      severity_weight
      status
      account_object_id
      updated_at
      created_at
      recommendation
    }
  }
  findings_aggregate: recommendation_groupings_v2(where: __WHERE__) {
    rows {
      count
    }
  }
}`;

const VULNERABILITY_GROUPINGS = `
query VmVulnerabilityGroupings {
  groupings: recommendation_groupings_v2(where: __WHERE__, group_by: __GROUP_BY__) {
    rows {
      resource_id
      severity
      count
    }
  }
}`;

// One page of vulnerability groups plus the number of groups the filter matches.
// The total is a count(DISTINCT <dimension>) rather than count(*) — paging is over
// groups, not findings. It is a second round trip, so callers that only want a
// top-N (the summary cards) drop it.
//
// Both fields MUST pass `columns:` explicitly. The gateway forwards the whole
// document to every field it dispatches, and the upstream handler picks the
// selection set by matching the *field name* (not the alias) — so with the same
// action aliased twice, the second call would silently inherit the first one's
// columns. An explicit `columns:` skips that parse entirely.
const VULNERABILITY_GROUPS = `
query VmVulnerabilityGroups($limit: Int, $offset: Int) {
  groups: recommendation_groupings_v2(where: __WHERE__, group_by: __GROUP_BY__, columns: __GROUP_COLUMNS__, limit: $limit, offset: $offset, order_by: __ORDER_BY__) {
    rows {
      __COLUMNS__
      count
      max_severity_weight
      __VM_FIELD__
    }
  }
  __TOTAL__
}`;

const VULNERABILITY_GROUPS_TOTAL = `groups_aggregate: recommendation_groupings_v2(where: __WHERE__, columns: ["count"], column_transformations: [{name: "count", expr: "distinct", args: __DISTINCT_ARGS__}]) {
    rows {
      count
    }
  }`;

const SCAN_VM = `
mutation ScanVm($accountId: String!, $datasourceId: String!, $cloudResourceId: String!) {
  security_scan_vm(object: {
    account_id: $accountId,
    datasource_id: $datasourceId,
    cloud_resource_id: $cloudResourceId
  }) {
    data
  }
}`;

const SCAN_VM_ACCOUNT = `
mutation ScanVmAccount($accountId: String!) {
  security_scan_vm_account(object: {
    account_id: $accountId
  }) {
    data
  }
}`;

const LIST_VM_SCAN_STATUSES = `
query ListVmScanStatuses($accountId: String!) {
  security_list_vm_scan_statuses(object: {
    account_id: $accountId
  }) {
    data
  }
}`;

const LIST_VM_SCAN_DATASOURCES = `
query ListVmScanDatasources($accountId: String!) {
  security_list_vm_scan_datasources(object: {
    account_id: $accountId
  }) {
    data
  }
}`;

const GET_VM_SERVICE_MAP = `
query VmServiceMap($accountId: String!, $windowSeconds: Int) {
  vm_get_service_map(request: {
    account_id: $accountId
    window_seconds: $windowSeconds
  }) {
    data {
      labels
      applications {
        Id { kind name namespace }
        Category { category }
        Labels
        Type
        Status
        IsHealthy
        HealthReason
        DesiredInstances
        FailedInstances
        Instances { Id { kind name namespace } IsFailed }
        Upstreams { Id RequestCount FailureCount Latency Protocol Stats Status Weight BytesSent BytesReceived }
        Downstreams { Id RequestCount FailureCount Latency Protocol Stats Status Weight BytesSent BytesReceived }
      }
    }
  }
}`;

const safeParse = (value: any) => {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

/**
 * Per-grouping query shape. `groupBy` are the columns the aggregate groups on
 * (and therefore the fields selected alongside the counts); `key` is the one the
 * drill-down filters the finding list by; `label` is what the row shows.
 */
/**
 * How a page of groups is ranked. `severity` leads with the worst finding, which
 * is what the listing tabs want; `count` leads with the biggest group, which is
 * what a "most affected" card means (and what makes its bars descend).
 */
const GROUP_ORDER_BY: Record<'severity' | 'count', string> = {
  severity: '[{column: "max_severity_weight", order: desc}, {column: "count", order: desc}]',
  count: '[{column: "count", order: desc}, {column: "max_severity_weight", order: desc}]',
};

const GROUPING_DIMENSIONS: Record<VmVulnerabilityGrouping, { groupBy: string[]; key: string; label: string; canListVms: boolean }> = {
  vulnerability: { groupBy: ['vuln_id'], key: 'vuln_id', label: 'vuln_id', canListVms: true },
  package: { groupBy: ['package_name'], key: 'package_name', label: 'package_name', canListVms: true },
  // A VM group is one machine — listing its VMs would restate the label.
  //
  // Grouped by resource_id ALONE (not resource_name too): resource_name is
  // one of the columns that forces recommendation_groupings_v2 onto the
  // query engine's joined CTE + window-function plan, which windows every
  // recommendation row on the account (all rules, all statuses) before
  // filtering by rule_name/status — measured at 7-8s cold on a tenant with
  // ~24k findings (#38494). Names are resolved separately afterwards via a
  // cheap indexed point lookup against cloud_resourses (resolveVmNames), not
  // by widening the GROUP BY.
  vm: { groupBy: ['resource_id'], key: 'resource_id', label: 'resource_id', canListVms: false },
};

/**
 * resource_id -> name for a small set of ids, via cloud_resources_list_v2 —
 * a plain indexed lookup on a different table, unrelated to the
 * recommendation query's CTE cost. Used to label "by VM" groups without
 * putting resource_name back in recommendation_groupings_v2's GROUP BY.
 */
const resolveVmNames = async (accountId: string | string[], ids: string[]): Promise<Record<string, string>> => {
  if (!ids.length) return {};
  const where = { account: Array.isArray(accountId) ? { _in: accountId } : { _eq: accountId }, id: { _in: ids } };
  const response = await queryGraphQLCached(LIST_VMS.replaceAll('__WHERE__', gqlStringify(where)), 'ListVms', { limit: ids.length, offset: 0 });
  const rows = response?.data?.data?.vms?.rows || [];
  const nameById: Record<string, string> = {};
  for (const row of rows) {
    if (row?.id) nameById[row.id] = row.name || row.resourse_id || row.id;
  }
  return nameById;
};

const vmVulnerabilityWhere = (accountId: string | string[], extra: Record<string, any> = {}, status: string[] = OPEN_STATUSES) => ({
  account_id: Array.isArray(accountId) ? { _in: accountId } : { _eq: accountId },
  // VM findings are always Security (see vmpackage/persist.go's recommendationCategory).
  // Pushed into the where-clause explicitly so the partial index on
  // (tenant_id, cloud_account_id, rule_name) WHERE category='Security' AND
  // status IN ('Open','InProgress') applies — see #38494.
  category: { _eq: 'Security' },
  rule_name: { _eq: VM_VULNERABILITY_RULE },
  status: { _in: status },
  ...extra,
});

/**
 * The page unmounts a tab's component on every switch, so without this each
 * Summary → Vulnerabilities → Instances hop re-ran the same 7s aggregate queries
 * (#38494). Responses are kept per (operation, query, variables) for a few
 * minutes and shared with in-flight callers; a failed or errored response is
 * never kept. Anything that writes (a scan) drops the lot.
 */
const READ_CACHE_TTL_MS = 2 * 60 * 1000;
const readCache = new Map<string, { expires: number; pending: Promise<any> }>();

function cachedCall<T>(key: string, fetcher: () => Promise<T>, isFailure: (result: T) => boolean = () => false): Promise<T> {
  const hit = readCache.get(key);
  if (hit && hit.expires > Date.now()) return hit.pending;
  const now = Date.now();
  // Entries are otherwise only replaced when the same key is asked for again, so
  // paging and searching would grow the map for the life of the tab.
  readCache.forEach((entry, entryKey) => {
    if (entry.expires <= now) readCache.delete(entryKey);
  });
  // .then, so a fetcher that throws synchronously becomes a rejection, not a throw.
  const pending = Promise.resolve().then(fetcher);
  readCache.set(key, { expires: now + READ_CACHE_TTL_MS, pending });
  const drop = () => {
    if (readCache.get(key)?.pending === pending) readCache.delete(key);
  };
  pending.then((result) => isFailure(result) && drop()).catch(drop);
  return pending;
}

/** Rows of LIST_ACCOUNT_INTEGRATIONS for one account and integration type; throws on a GraphQL error instead of returning an empty list. */
async function listAccountIntegrations(accountId: string, type: string): Promise<any[]> {
  const where = { account_id: { _eq: accountId }, type: { _eq: type } };
  const response: any = await queryGraphQL(LIST_ACCOUNT_INTEGRATIONS.replaceAll('__WHERE__', gqlStringify(where)), 'ListAccountIntegrations', {});
  if (response?.data?.errors?.length) {
    throw new Error(response.data.errors[0]?.message || `Failed to list ${type} integrations`);
  }
  // queryGraphQL does not throw on a transport failure — it hands back the bare
  // axios response / XHR (or undefined on a 401 redirect). A missing payload is a
  // failed call, not "no integrations": that silent-empty reading is the bug
  // this function replaces.
  const result = response?.data?.data?.integrations;
  if (!result) {
    throw new Error(`Failed to list ${type} integrations`);
  }
  return result.rows || [];
}

function queryGraphQLCached(query: string, operationName: string, variables: Record<string, any>): Promise<any> {
  return cachedCall(
    `${operationName}|${query}|${JSON.stringify(variables)}`,
    () => queryGraphQL(query, operationName, variables),
    (response) => !!response?.data?.errors?.length
  );
}

/** Utilisation keys the VM metrics panel asks for (see services/observability/prometheus_vm_queries.go). */
export const VM_METRIC_KEYS = [
  'cpu_usage_line',
  'cpu_total',
  'memory_usage_line',
  'mem_total',
  'disk_read_bytes',
  'disk_write_bytes',
  'disk_used',
  'disk_total',
  'vm_service_cpu_usage',
  'vm_service_memory_usage',
  'vm_service_disk_used',
  'vm_service_git',
] as const;

/** Fleet-wide keys behind the VM list's CPU and memory gauges (see prometheus_vm_queries.go). */
export const VM_FLEET_METRIC_KEYS = [
  'vm_fleet_cpu_used',
  'vm_fleet_cpu_cores',
  'vm_fleet_mem_used',
  'vm_fleet_mem_total',
  'vm_fleet_identity',
  'vm_fleet_hostname',
] as const;

/** Current load of one VM, as a share of what it has. Undefined fields were not reported. */
export interface VmFleetMetric {
  cpuPct?: number;
  memPct?: number;
}

export interface VmMetricSeries {
  /** Labels of the series; per-service keys carry `container_id`. */
  metric: Record<string, string>;
  /** Epoch seconds. */
  timestamps: number[];
  values: number[];
}

export interface VmMetricResult {
  key: string;
  series: VmMetricSeries[];
  /** Why the key has no data (no provider, unsupported key, query error). */
  reason?: string;
}

const METRICS_QUERY_UTILISATION = `
  query MetricsQueryUtilisation($jsonFilter: jsonb!, $accountId: String!, $startTime: Float!, $endTime: Float!) {
    metrics_aggregate_utilisation(request: {account_id: $accountId, request: $jsonFilter, end_time: $endTime, start_time: $startTime}) {
      results
    }
  }
`;

/** The backend returns series values as strings in some providers; normalise to numbers and drop non-finite points. */
export function parseVmMetricResults(raw: any[]): VmMetricResult[] {
  return (Array.isArray(raw) ? raw : []).map((r: any) => {
    const series: VmMetricSeries[] = (r?.payload || []).map((p: any) => {
      const timestamps: number[] = [];
      const values: number[] = [];
      (p?.timestamps || []).forEach((t: any, i: number) => {
        const v = Number(p?.values?.[i]);
        // Number(null) and Number('') are 0, which would pass as an epoch timestamp.
        const ts = t === null || t === undefined || t === '' ? NaN : Number(t);
        // A point needs both: a bad timestamp breaks the chart axis as surely as a bad value.
        if (Number.isFinite(v) && Number.isFinite(ts)) {
          timestamps.push(ts);
          values.push(v);
        }
      });
      return { metric: p?.metric || {}, timestamps, values };
    });
    return { key: r?.query_key, series, reason: r?.error || r?.note || undefined };
  });
}

const apiVm = {
  /**
   * CPU, memory, disk and per-service metrics for one VM, read from the account's
   * metrics provider. The VM is identified by whichever of its cloud instance id,
   * private IP and hostname we know; the backend resolves the node-agent `instance`
   * label from them.
   */
  async getVmMetrics({
    accountId,
    vm,
    startDate,
    endDate,
    metrics = [...VM_METRIC_KEYS],
  }: {
    accountId: string;
    vm: Pick<VmResource, 'name' | 'resourse_id' | 'meta'>;
    /** Epoch ms. */
    startDate: number;
    endDate: number;
    metrics?: string[];
  }): Promise<VmMetricResult[]> {
    const response = await queryGraphQL(METRICS_QUERY_UTILISATION, 'MetricsQueryUtilisation', {
      accountId,
      startTime: startDate,
      endTime: endDate,
      jsonFilter: {
        kind: 'vm',
        vm_instance_id: vm.resourse_id && !vm.resourse_id.startsWith('vm-') ? vm.resourse_id : undefined,
        vm_ip: vmPrivateIp(vm.meta),
        vm_hostname: vm.name,
        metrics,
        instant: false,
      },
    });
    const errors = response?.data?.errors;
    if (errors?.length) throw new Error(errors[0]?.message || 'Failed to load VM metrics');
    return parseVmMetricResults(response?.data?.data?.metrics_aggregate_utilisation?.results);
  },

  /**
   * CPU and memory use of every VM in `vms`, from one batched call: the backend answers per
   * node-agent `instance` for the whole store, and the VMs are matched to those instances
   * here by cloud instance id, then private IP, then hostname. A VM with no match is absent
   * from the result (no agent, or one that stopped reporting).
   */
  async getVmFleetMetrics({
    accountId,
    vms,
  }: {
    accountId: string;
    vms: Array<Pick<VmResource, 'id' | 'name' | 'resourse_id' | 'meta'>>;
  }): Promise<Record<string, VmFleetMetric>> {
    if (!vms.length) return {};
    const end = Date.now();
    const response = await queryGraphQL(METRICS_QUERY_UTILISATION, 'MetricsQueryUtilisation', {
      accountId,
      startTime: end - 5 * 60 * 1000,
      endTime: end,
      jsonFilter: { kind: 'vm_fleet', metrics: [...VM_FLEET_METRIC_KEYS], instant: true },
    });
    const errors = response?.data?.errors;
    if (errors?.length) throw new Error(errors[0]?.message || 'Failed to load VM metrics');
    return joinFleetMetrics(parseVmMetricResults(response?.data?.data?.metrics_aggregate_utilisation?.results), vms);
  },

  /** Self-hosted accounts, i.e. everything the /vm page can be scoped to. */
  async getSelfHostedAccounts(refresh = false) {
    const accounts = await apiHome.getCloudAccounts('SelfHosted', refresh);
    return Array.isArray(accounts) ? accounts : [];
  },

  async listVms({
    accountId,
    search,
    resourceIds,
    limit = 10,
    offset = 0,
  }: {
    accountId: string;
    search?: string;
    /** Narrow to exactly these VMs (e.g. the ones a specific CVE was scoped to) instead of paging the full fleet. */
    resourceIds?: string[];
    limit?: number;
    offset?: number;
  }) {
    const where: any = { account: { _eq: accountId } };
    if (resourceIds?.length) where.id = { _in: resourceIds };
    if (search) {
      where._and = [{ _or: [{ name: { _ilike: `%${search}%` } }, { resourse_id: { _ilike: `%${search}%` } }] }];
    }
    const response = await queryGraphQLCached(LIST_VMS.replaceAll('__WHERE__', gqlStringify(where)), 'ListVms', { limit, offset });
    const rows = response?.data?.data?.vms?.rows || [];
    return {
      rows: rows.map((row: any) => ({ ...row, meta: safeParse(row.meta) || {}, tags: safeParse(row.tags) || {} })) as VmResource[],
      total: rows[0]?.total_count || 0,
    };
  },

  async listPackages({
    accountId,
    cloudResourceId,
    search,
    pkgType,
    limit = 10,
    offset = 0,
  }: {
    accountId: string;
    cloudResourceId?: string;
    search?: string;
    pkgType?: string;
    limit?: number;
    offset?: number;
  }) {
    const where: any = { account_id: { _eq: accountId }, is_active: { _eq: true } };
    if (cloudResourceId) where.cloud_resource_id = { _eq: cloudResourceId };
    if (pkgType) where.pkg_type = { _eq: pkgType };
    if (search) {
      where._and = [{ _or: [{ name: { _ilike: `%${search}%` } }, { source_name: { _ilike: `%${search}%` } }] }];
    }
    const query = LIST_VM_PACKAGES.replaceAll('__WHERE__', gqlStringify(where))
      .replaceAll('__GROUP_BY__', JSON.stringify(PACKAGE_GROUP_KEY))
      .replaceAll('__COLUMNS__', JSON.stringify([...PACKAGE_GROUP_KEY, 'resource_ids', 'max_last_seen_at']));
    const response = await queryGraphQLCached(query, 'ListVmPackages', { limit, offset });
    const rows = response?.data?.data?.packages?.rows || [];
    return {
      rows: rows.map((row: any) => ({
        ...row,
        resource_ids: String(row.resource_ids || '')
          .split(',')
          .filter(Boolean),
        last_seen_at: row.max_last_seen_at,
      })) as VmPackage[],
      total: response?.data?.data?.packages_aggregate?.rows?.[0]?.count || 0,
    };
  },

  /** resource id → { count, lastSeenAt } for the inventory table's Packages column. */
  async getPackageCountsByVm(accountId: string) {
    const where = { account_id: { _eq: accountId }, is_active: { _eq: true } };
    const response = await queryGraphQLCached(PACKAGE_COUNTS_BY_VM.replaceAll('__WHERE__', gqlStringify(where)), 'VmPackageCounts', {});
    const rows = response?.data?.data?.packages_aggregate?.rows || [];
    const byResource: Record<string, { count: number; lastSeenAt: string }> = {};
    for (const row of rows) {
      if (!row?.cloud_resource_id) continue;
      byResource[row.cloud_resource_id] = { count: row.count || 0, lastSeenAt: row.max_last_seen_at };
    }
    return byResource;
  },

  /**
   * resource id → "ubuntu 22.04". The OS is a property of the package inventory,
   * not of cloud_resourses, so it only exists once a VM has been scanned.
   */
  async getOsByVm(accountId: string) {
    const where = { account_id: { _eq: accountId }, is_active: { _eq: true } };
    const response = await queryGraphQLCached(VM_OS_BY_VM.replaceAll('__WHERE__', gqlStringify(where)), 'VmOsFamilies', {});
    const rows = response?.data?.data?.packages_aggregate?.rows || [];
    const byResource: Record<string, string> = {};
    for (const row of rows) {
      if (!row?.cloud_resource_id || !row?.os_family) continue;
      byResource[row.cloud_resource_id] = [row.os_family, row.os_version].filter(Boolean).join(' ');
    }
    return byResource;
  },

  async listVulnerabilities({
    accountId,
    cloudResourceId,
    vulnId,
    packageName,
    severity,
    status,
    limit = 10,
    offset = 0,
  }: {
    accountId: string | string[];
    cloudResourceId?: string;
    vulnId?: string;
    packageName?: string;
    severity?: string;
    /** Row statuses to list; defaults to the open ones. */
    status?: string[];
    limit?: number;
    offset?: number;
  }) {
    const extra: Record<string, any> = {};
    if (cloudResourceId) extra.resource_id = { _eq: cloudResourceId };
    if (vulnId) extra.vuln_id = { _eq: vulnId };
    if (packageName) extra.package_name = { _eq: packageName };
    if (severity) extra.severity = { _eq: severity };
    const where = vmVulnerabilityWhere(accountId, extra, status);
    const response = await queryGraphQLCached(LIST_VM_VULNERABILITIES.replaceAll('__WHERE__', gqlStringify(where)), 'ListVmVulnerabilities', {
      limit,
      offset,
    });
    const rows = response?.data?.data?.findings?.rows || [];
    const findings = rows.map((row: any) => ({ ...row, recommendation: safeParse(row.recommendation) || {} })) as VmVulnerability[];
    // Tickets store the finding id as reference_id; one batched lookup maps them back.
    const findingIds = findings.map((finding) => finding.id);
    if (findingIds.length > 0) {
      const tickets: any = await ticketsApi.listTicketsSummary({ reference_id: findingIds });
      const ticketByFinding = new Map<string, { ticket_id: string; url?: string }>();
      (tickets?.data?.tickets || []).forEach((ticket: any) => {
        if (ticket.ticket_id) ticketByFinding.set(ticket.reference_id, { ticket_id: ticket.ticket_id, url: ticket.url || undefined });
      });
      findings.forEach((finding) => {
        finding.ticket = ticketByFinding.get(finding.id);
      });
    }
    return {
      rows: findings,
      total: response?.data?.data?.findings_aggregate?.rows?.[0]?.count || 0,
    };
  },

  /**
   * One page of vulnerability groups — the Vulnerability / Package / VM tabs of
   * the findings table. Rolled up in Postgres rather than in the browser: an
   * account carries tens of thousands of findings, so grouping a fetched page
   * would only ever group that page.
   */
  async listVulnerabilityGroups({
    accountId,
    grouping,
    cloudResourceId,
    vulnId,
    packageName,
    severity,
    status,
    limit = 10,
    offset = 0,
    includeTotal = true,
    includeVms = false,
    orderBy = 'severity',
  }: {
    accountId: string | string[];
    grouping: VmVulnerabilityGrouping;
    cloudResourceId?: string;
    vulnId?: string;
    packageName?: string;
    severity?: string;
    /** Row statuses to group; defaults to the open ones. */
    status?: string[];
    limit?: number;
    offset?: number;
    /** Drop the group-count round trip when the caller only wants a top-N. */
    includeTotal?: boolean;
    /**
     * Aggregate the VMs behind each group. Off by default: it reads
     * resource_name, which only the joined (CTE + window) plan supplies, so a
     * caller that does not render the VMs should not pay for it.
     */
    includeVms?: boolean;
    orderBy?: 'severity' | 'count';
  }) {
    const dimension = GROUPING_DIMENSIONS[grouping];
    // Same scope filters the flat list takes, so a grouping stays correct when
    // the view is already narrowed to one VM / package / CVE.
    const extra: Record<string, any> = {};
    if (cloudResourceId) extra.resource_id = { _eq: cloudResourceId };
    if (vulnId) extra.vuln_id = { _eq: vulnId };
    if (packageName) extra.package_name = { _eq: packageName };
    if (severity) extra.severity = { _eq: severity };
    const where = vmVulnerabilityWhere(accountId, extra, status);
    const listVms = includeVms && dimension.canListVms;
    const groupColumns = [...dimension.groupBy, 'count', 'max_severity_weight', ...(listVms ? ['resource_names'] : [])];
    const query = VULNERABILITY_GROUPS.replaceAll('__TOTAL__', includeTotal ? VULNERABILITY_GROUPS_TOTAL : '')
      .replaceAll('__ORDER_BY__', GROUP_ORDER_BY[orderBy])
      .replaceAll('__WHERE__', gqlStringify(where))
      .replaceAll('__GROUP_BY__', JSON.stringify(dimension.groupBy))
      .replaceAll('__GROUP_COLUMNS__', JSON.stringify(groupColumns))
      .replaceAll('__COLUMNS__', dimension.groupBy.join('\n      '))
      .replaceAll('__VM_FIELD__', listVms ? 'resource_names' : '')
      .replaceAll('__DISTINCT_ARGS__', JSON.stringify([dimension.key]));
    const response = await queryGraphQLCached(query, 'VmVulnerabilityGroups', { limit, offset });
    const rows = response?.data?.data?.groups?.rows || [];
    // The 'vm' dimension groups by resource_id alone (see GROUPING_DIMENSIONS'
    // comment), so its label needs a separate name lookup — a handful of ids
    // (at most `limit`), not the full account.
    const vmNameById = grouping === 'vm' ? await resolveVmNames(accountId, rows.map((row: any) => row.resource_id).filter(Boolean)) : {};
    return {
      rows: rows.map((row: any) => ({
        key: row[dimension.key] || '',
        label: grouping === 'vm' ? vmNameById[row.resource_id] || row.resource_id || '' : row[dimension.label] || row[dimension.key] || '',
        count: row.count || 0,
        max_severity_weight: row.max_severity_weight || 0,
        resource_names: String(row.resource_names || '')
          .split(', ')
          .filter(Boolean),
      })) as VmVulnerabilityGroup[],
      total: response?.data?.data?.groups_aggregate?.rows?.[0]?.count || 0,
    };
  },

  /** Open-finding counts keyed by severity, for the page's summary tiles. */
  async getSeverityCounts({ accountId, cloudResourceId }: { accountId: string | string[]; cloudResourceId?: string }) {
    const extra = cloudResourceId ? { resource_id: { _eq: cloudResourceId } } : {};
    const where = vmVulnerabilityWhere(accountId, extra);
    const query = VULNERABILITY_GROUPINGS.replaceAll('__WHERE__', gqlStringify(where)).replaceAll('__GROUP_BY__', '["severity"]');
    const response = await queryGraphQLCached(query, 'VmVulnerabilityGroupings', {});
    const rows = response?.data?.data?.groupings?.rows || [];
    const bySeverity: Record<string, number> = {};
    for (const row of rows) {
      if (!row?.severity) continue;
      bySeverity[row.severity] = (bySeverity[row.severity] || 0) + (row.count || 0);
    }
    return bySeverity;
  },

  /** resource id → { Critical: n, High: n, ... } for the inventory table. */
  async getVulnerabilityCountsByVm(accountId: string) {
    const where = vmVulnerabilityWhere(accountId);
    const query = VULNERABILITY_GROUPINGS.replaceAll('__WHERE__', gqlStringify(where)).replaceAll('__GROUP_BY__', '["resource_id", "severity"]');
    const response = await queryGraphQLCached(query, 'VmVulnerabilityGroupings', {});
    const rows = response?.data?.data?.groupings?.rows || [];
    const byResource: Record<string, Record<string, number>> = {};
    for (const row of rows) {
      if (!row?.resource_id) continue;
      if (!byResource[row.resource_id]) byResource[row.resource_id] = {};
      const severity = row.severity || 'Unknown';
      byResource[row.resource_id][severity] = (byResource[row.resource_id][severity] || 0) + (row.count || 0);
    }
    return byResource;
  },

  /**
   * The foragers installed for this account, merged with the relay's view of
   * which ones are actually connected. Health is keyed by account, not by
   * config, so every agent on an account shares one connection verdict — the
   * same approximation the integrations listing makes.
   */
  async listAgents(accountId: string): Promise<VmAgent[]> {
    return cachedCall(`listAgents|${accountId}`, () => apiVm.fetchAgents(accountId));
  },

  async listDiscoveryTargets(accountId: string): Promise<VmScanTarget[]> {
    return cachedCall(`listDiscoveryTargets|${accountId}`, () => apiVm.fetchDiscoveryTargets(accountId));
  },

  /** Uncached; use listAgents. */
  async fetchAgents(accountId: string): Promise<VmAgent[]> {
    const [rows, healthRes]: any[] = await Promise.all([
      listAccountIntegrations(accountId, 'vm_agent'),
      // Health is decoration on top of the agent list — if it fails, still show
      // the agents (as Disconnected) rather than rejecting the whole tab.
      k8sApi.getAgentHealth({ accountId, type: 'proxy' }).catch((error: unknown) => {
        console.error('Failed to fetch proxy agent health:', error);
        return { data: [] };
      }),
    ]);
    const health = (healthRes?.data || []).find((agent: any) => agent?.cloud_account_id === accountId);
    // Same verdict agentHealth.jsx uses — agents_list_health.status is the
    // relay's own CONNECTED/DISCONNECTED state, not the integration's
    // enabled/disabled state (which is `row.status` below).
    const connected = health?.status === 'CONNECTED';
    return rows.map((row: any) => ({
      id: row.id,
      name: row.name,
      status: row.status,
      created_at: row.created_at,
      connected,
      last_seen_at: health?.last_connected_at,
      version: health?.version,
    }));
  },

  /**
   * Forager discovery integrations — the pickable `datasource_id`s for a scan.
   * Read through the scan's own action, not the integrations list: that one is
   * tenant-admin only, while account and namespace admins may scan a VM too,
   * and it left them with an empty picker (#39859). The server returns exactly
   * the datasources `security_scan_vm` accepts for this account.
   */
  /** Uncached; use listDiscoveryTargets. */
  async fetchDiscoveryTargets(accountId: string): Promise<VmScanTarget[]> {
    const response: any = await queryGraphQL(LIST_VM_SCAN_DATASOURCES, 'ListVmScanDatasources', { accountId });
    if (!response || response instanceof Error) {
      throw response || new Error('Failed to list discovery targets');
    }
    if (response?.data?.errors?.length) {
      throw new Error(response.data.errors[0]?.message || 'Failed to list discovery targets');
    }
    return (response?.data?.data?.security_list_vm_scan_datasources?.data || []).map((row: any) => ({
      id: row.id,
      name: row.name,
      status: row.status,
      // Discovery integrations cover a network, not one saved SSH host.
      // The VM's IP is supplied by the scan RPC.
      host: '',
      username: '',
    }));
  },

  /**
   * Fire an on-demand inventory + CVE scan. The RPC acks immediately and the
   * pipeline runs detached server-side, so a success here means "started", not
   * "finished" — the caller has to re-poll for results.
   */
  async scanVm({ accountId, datasourceId, cloudResourceId }: { accountId: string; datasourceId: string; cloudResourceId: string }) {
    readCache.clear();
    const response = await queryGraphQL(SCAN_VM, 'ScanVm', { accountId, datasourceId, cloudResourceId });
    const errors = response?.data?.errors;
    if (errors?.length) {
      throw new Error(errors[0]?.message || 'Failed to start VM scan');
    }
    return response?.data?.data?.security_scan_vm?.data || [];
  },

  /**
   * Fire an on-demand scan for every instance reachable by the account's
   * discovery datasource(s). Same ack-now/runs-detached contract as scanVm;
   * fails with a clear error when no discovery agent is configured.
   */
  async scanVmAccount({ accountId }: { accountId: string }) {
    readCache.clear();
    const response = await queryGraphQL(SCAN_VM_ACCOUNT, 'ScanVmAccount', { accountId });
    const errors = response?.data?.errors;
    if (errors?.length) {
      throw new Error(errors[0]?.message || 'Failed to start account scan');
    }
    return response?.data?.data?.security_scan_vm_account?.data || [];
  },

  /**
   * Each VM's most recent scan outcome for the account, so a row can show
   * "Scanning…" / "Scan failed" instead of just an ageing Last Scan
   * timestamp with no explanation (see #38466).
   */
  async getScanStatuses(accountId: string): Promise<VmScanStatus[]> {
    const response = await queryGraphQL(LIST_VM_SCAN_STATUSES, 'ListVmScanStatuses', { accountId });
    const errors = response?.data?.errors;
    if (errors?.length) {
      throw new Error(errors[0]?.message || 'Failed to load VM scan statuses');
    }
    const rows = response?.data?.data?.security_list_vm_scan_statuses?.data || [];
    return rows.map((row: any) => ({
      cloudResourceId: row.cloud_resource_id,
      status: row.status,
      error: row.error,
      packageCount: row.package_count,
      findingCount: row.finding_count,
      scannedAt: row.scanned_at,
      updatedAt: row.updated_at,
    }));
  },
  /**
   * Whether this account's metrics store is connected and VM agents are writing to
   * it. Probes the store through the forager, so it can take a few seconds: call it
   * on demand rather than caching it.
   */
  async getMetricsStatus(accountId: string): Promise<VmMetricsStatus> {
    const response = await queryGraphQL(METRICS_CHECK_VM_STATUS, 'MetricsCheckVmStatus', { accountId });
    const errors = response?.data?.errors;
    if (errors?.length) throw new Error(errors[0]?.message || 'Failed to check VM metrics');
    const status = response?.data?.data?.metrics_check_vm_status;
    if (!status) throw new Error('Failed to check VM metrics');
    return { ...status, reporting: status.reporting || [] };
  },
  /**
   * Whether this account's log store is connected and holding recent logs. Probes the
   * store through the forager, so it can take a few seconds: call it on demand.
   */
  async getLogsStatus(accountId: string): Promise<VmLogsStatus> {
    const response = await queryGraphQL(LOGS_CHECK_VM_STATUS, 'LogsCheckVmStatus', { accountId });
    const errors = response?.data?.errors;
    if (errors?.length) throw new Error(errors[0]?.message || 'Failed to check VM logs');
    const status = response?.data?.data?.logs_check_vm_status;
    if (!status) throw new Error('Failed to check VM logs');
    return status;
  },

  /**
   * Whether this account can receive Alertmanager alerts and when one last did. The
   * webhook is push-only, so there is no connection to probe: the status is the
   * integration's own state plus the newest event it produced.
   */
  async getAlertWebhookStatus(accountId: string): Promise<VmAlertWebhookStatus> {
    const integrations = await listAccountIntegrations(accountId, ALERTMANAGER_WEBHOOK_INTEGRATION);
    if (!integrations.length) return { state: 'not_configured' };
    const enabled = integrations.filter((row: any) => row.status === 'enabled');
    if (!enabled.length) return { state: 'disabled', integration_name: integrations[0].name };

    const res: any = await k8sApi.getK8sEvents(
      1,
      0,
      { account_id: accountId, source: [ALERTMANAGER_WEBHOOK_INTEGRATION], sort_by: 'updated_at', timeFilter: false },
      ['updated_at']
    );
    const events = res?.data?.events;
    if (!Array.isArray(events)) throw new Error('Failed to read received alerts');
    const lastReceived = events[0]?.updated_at;
    return lastReceived
      ? { state: 'receiving', integration_name: enabled[0].name, last_received_at: lastReceived }
      : { state: 'no_alerts', integration_name: enabled[0].name };
  },

  /**
   * The account's service map, built by its forager from node-agent metrics and
   * named against inventory on the server. Same application shape as the K8s
   * map, so the K8s node and edge components render it unchanged.
   */
  async getServiceMap(accountId: string, windowSeconds: number): Promise<any[]> {
    const response = await queryGraphQL(GET_VM_SERVICE_MAP, 'VmServiceMap', { accountId, windowSeconds });
    const errors = response?.data?.errors;
    if (errors?.length) {
      throw new Error(errors[0]?.message || 'Failed to load the service map');
    }
    return response?.data?.data?.vm_get_service_map?.data?.applications || [];
  },
};

export type VmAlertWebhookState = 'not_configured' | 'disabled' | 'no_alerts' | 'receiving';

export interface VmAlertWebhookStatus {
  state: VmAlertWebhookState;
  integration_name?: string;
  /** When the most recent alert (or a repeat of one) reached the account. */
  last_received_at?: string;
}

const ALERTMANAGER_WEBHOOK_INTEGRATION = 'prometheus_alertmanager_webhook';

export type VmMetricsState = 'not_configured' | 'check_failed' | 'unreachable' | 'no_data' | 'connected';

export interface VmMetricsStatus {
  state: VmMetricsState;
  provider?: string;
  integration_id?: string;
  integration_name?: string;
  error?: string;
  /** What `reporting` counts: this account's VMs, or every agent in the store when the account has no VM inventory to match against. */
  scope?: 'account' | 'store';
  /** VMs the account has on record. */
  inventory?: number;
  /** Agents writing to the same store that are not this account's VMs. */
  other_agents?: number;
  /** node-agents seen writing to the account's metrics store recently. */
  reporting: Array<{ instance: string; hostname?: string }>;
}

export type VmLogsState = VmMetricsState;

export interface VmLogsStatus {
  state: VmLogsState;
  provider?: string;
  integration_id?: string;
  integration_name?: string;
  error?: string;
}

const LOGS_CHECK_VM_STATUS = `
  query LogsCheckVmStatus($accountId: String!) {
    logs_check_vm_status(request: {account_id: $accountId}) {
      state
      provider
      integration_id
      integration_name
      error
    }
  }
`;

const METRICS_CHECK_VM_STATUS = `
  query MetricsCheckVmStatus($accountId: String!) {
    metrics_check_vm_status(request: {account_id: $accountId}) {
      state
      provider
      integration_id
      integration_name
      error
      scope
      inventory
      other_agents
      reporting {
        instance
        hostname
      }
    }
  }
`;

export default apiVm;
