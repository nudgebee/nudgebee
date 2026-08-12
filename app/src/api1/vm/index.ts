/**
 * Data layer for the self-hosted VM fleet page (/vm).
 *
 * A "VM account" is a cloud_accounts row with cloud_provider = 'SelfHosted' and
 * account_type = 'vm' (see AddSelfHostedAccountModal). Four things hang off it,
 * and this module is the single place that knows where each one lives:
 *
 *   VMs           cloud_resourses rows owned by the account (cloud_resources_list_v2)
 *   Agents        vm_agent integration configs + relay connection health
 *   SSH targets   ssh integration configs in connection_mode = vm_agent — one per
 *                 reachable host; their id is the `datasource_id` a scan runs through
 *   Packages      vm_package rows, written by services/vmpackage after a scan
 *   Findings      recommendation rows with rule_name = 'vm_package_vulnerability'
 *
 * The last two arrive with the services-server VM-scan pipeline (#35405); the
 * queries here are inert until that lands.
 */
import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import apiHome from '@api1/home';
import apiIntegrations from '@api1/integrations';
import k8sApi from '@api1/kubernetes';

/** rule_name every VM vulnerability finding is written under. */
export const VM_VULNERABILITY_RULE = 'vm_package_vulnerability';

/** Statuses that count as an unresolved finding. */
export const OPEN_STATUSES = ['Open', 'InProgress'];

export const SEVERITY_ORDER = ['Critical', 'High', 'Medium', 'Low', 'Negligible', 'Unknown'];

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

export interface VmSshTarget {
  id: string;
  name: string;
  status: string;
  host: string;
  username: string;
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

const safeParse = (value: any) => {
  if (typeof value !== 'string') return value;
  try {
    return JSON.parse(value);
  } catch {
    return value;
  }
};

/** Pull one named config value out of the integration_config_values blob. */
const configValue = (item: any, key: string): string => {
  const values = Array.isArray(item?.integration_config_values) ? item.integration_config_values : safeParse(item?.integration_config_values) || [];
  const match = Array.isArray(values) ? values.find((v: any) => v?.name === key) : undefined;
  return match?.value ?? '';
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
  vm: { groupBy: ['resource_id', 'resource_name'], key: 'resource_id', label: 'resource_name', canListVms: false },
};

const vmVulnerabilityWhere = (accountId: string, extra: Record<string, any> = {}) => ({
  account_id: { _eq: accountId },
  rule_name: { _eq: VM_VULNERABILITY_RULE },
  status: { _in: OPEN_STATUSES },
  ...extra,
});

const apiVm = {
  /** Self-hosted accounts, i.e. everything the /vm page can be scoped to. */
  async getSelfHostedAccounts(refresh = false) {
    const accounts = await apiHome.getCloudAccounts('SelfHosted', refresh);
    return Array.isArray(accounts) ? accounts : [];
  },

  async listVms({ accountId, search, limit = 10, offset = 0 }: { accountId: string; search?: string; limit?: number; offset?: number }) {
    const where: any = { account: { _eq: accountId } };
    if (search) {
      where._and = [{ _or: [{ name: { _ilike: `%${search}%` } }, { resourse_id: { _ilike: `%${search}%` } }] }];
    }
    const response = await queryGraphQL(LIST_VMS.replaceAll('__WHERE__', gqlStringify(where)), 'ListVms', { limit, offset });
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
    const response = await queryGraphQL(query, 'ListVmPackages', { limit, offset });
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
    const response = await queryGraphQL(PACKAGE_COUNTS_BY_VM.replaceAll('__WHERE__', gqlStringify(where)), 'VmPackageCounts', {});
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
    const response = await queryGraphQL(VM_OS_BY_VM.replaceAll('__WHERE__', gqlStringify(where)), 'VmOsFamilies', {});
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
    limit = 10,
    offset = 0,
  }: {
    accountId: string;
    cloudResourceId?: string;
    vulnId?: string;
    packageName?: string;
    severity?: string;
    limit?: number;
    offset?: number;
  }) {
    const extra: Record<string, any> = {};
    if (cloudResourceId) extra.resource_id = { _eq: cloudResourceId };
    if (vulnId) extra.vuln_id = { _eq: vulnId };
    if (packageName) extra.package_name = { _eq: packageName };
    if (severity) extra.severity = { _eq: severity };
    const where = vmVulnerabilityWhere(accountId, extra);
    const response = await queryGraphQL(LIST_VM_VULNERABILITIES.replaceAll('__WHERE__', gqlStringify(where)), 'ListVmVulnerabilities', {
      limit,
      offset,
    });
    const rows = response?.data?.data?.findings?.rows || [];
    return {
      rows: rows.map((row: any) => ({ ...row, recommendation: safeParse(row.recommendation) || {} })) as VmVulnerability[],
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
    limit = 10,
    offset = 0,
    includeTotal = true,
    includeVms = false,
    orderBy = 'severity',
  }: {
    accountId: string;
    grouping: VmVulnerabilityGrouping;
    cloudResourceId?: string;
    vulnId?: string;
    packageName?: string;
    severity?: string;
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
    const where = vmVulnerabilityWhere(accountId, extra);
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
    const response = await queryGraphQL(query, 'VmVulnerabilityGroups', { limit, offset });
    const rows = response?.data?.data?.groups?.rows || [];
    return {
      rows: rows.map((row: any) => ({
        key: row[dimension.key] || '',
        label: row[dimension.label] || row[dimension.key] || '',
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
  async getSeverityCounts({ accountId, cloudResourceId }: { accountId: string; cloudResourceId?: string }) {
    const extra = cloudResourceId ? { resource_id: { _eq: cloudResourceId } } : {};
    const where = vmVulnerabilityWhere(accountId, extra);
    const query = VULNERABILITY_GROUPINGS.replaceAll('__WHERE__', gqlStringify(where)).replaceAll('__GROUP_BY__', '["severity"]');
    const response = await queryGraphQL(query, 'VmVulnerabilityGroupings', {});
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
    const response = await queryGraphQL(query, 'VmVulnerabilityGroupings', {});
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
    const [listRes, healthRes]: any[] = await Promise.all([
      apiIntegrations.listIntegrations({ type: 'vm_agent', cloudAccountId: accountId, limit: 100 }),
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
    return (listRes?.data?.data?.integrations_list?.rows || []).map((row: any) => ({
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
   * SSH integration configs in vm_agent mode — the pickable `datasource_id`s for
   * a scan. k8s-mode SSH configs are excluded: they route through the in-cluster
   * relay, not a forager, so discovery_inventory has nothing to run against.
   */
  async listSshTargets(accountId: string): Promise<VmSshTarget[]> {
    const response: any = await apiIntegrations.listIntegrations({ type: 'ssh', cloudAccountId: accountId, limit: 100 });
    return (response?.data?.data?.integrations_list?.rows || [])
      .filter((row: any) => configValue(row, 'connection_mode') === 'vm_agent')
      .map((row: any) => ({
        id: row.id,
        name: row.name,
        status: row.status,
        host: configValue(row, 'host'),
        username: configValue(row, 'username'),
      }));
  },

  /**
   * Fire an on-demand inventory + CVE scan. The RPC acks immediately and the
   * pipeline runs detached server-side, so a success here means "started", not
   * "finished" — the caller has to re-poll for results.
   */
  async scanVm({ accountId, datasourceId, cloudResourceId }: { accountId: string; datasourceId: string; cloudResourceId: string }) {
    const response = await queryGraphQL(SCAN_VM, 'ScanVm', { accountId, datasourceId, cloudResourceId });
    const errors = response?.data?.errors;
    if (errors?.length) {
      throw new Error(errors[0]?.message || 'Failed to start VM scan');
    }
    return response?.data?.data?.security_scan_vm?.data || [];
  },
};

export default apiVm;
