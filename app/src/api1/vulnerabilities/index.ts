/**
 * Package-vulnerability findings produced by the VM package scan
 * (`api-server/services/vmpackage`).
 *
 * There is no findings table: the scan writes into `recommendation` with
 * `category = 'Security'` and `rule_name = 'vm_package_vulnerability'`, keyed to
 * the scanned host by `resource_id` — a `cloud_resourses.id`. That is what makes
 * this reusable beyond self-hosted fleets: an EC2 instance is a `cloud_resourses`
 * row like any other, so the same findings hang off it, and scoping to one host
 * is a `resource_id` filter rather than a different query.
 *
 * Reads go through the existing `recommendations_list` /
 * `recommendation_groupings_v2` actions — this module adds no new RPC surface.
 */
import { gqlStringify, queryGraphQL } from '@lib/HttpService';

/** rule_name every package-vulnerability finding is written under. */
export const VULNERABILITY_RULE = 'vm_package_vulnerability';

/** Statuses that count as unresolved. */
export const OPEN_STATUSES = ['Open', 'InProgress'];

/**
 * Worst-first. Matches `mapSeverity` in vmpackage/persist.go, whose fallback for
 * anything vuln-matcher-server doesn't classify is 'Info' — not 'Unknown'.
 */
export const SEVERITY_ORDER = ['Critical', 'High', 'Medium', 'Low', 'Info'];

/** The `recommendation` JSON column as vmpackage writes it (findingPayload). */
export interface VulnerabilityPayload {
  vuln_id?: string;
  package?: { name?: string; version?: string; arch?: string; type?: string };
  fixed_version?: string;
  fix_state?: string;
  fix_channel?: string;
  cvss_v3_score?: number;
  cvss_v3_vector?: string;
  epss?: { score?: number; percentile?: number } | null;
  kev?: boolean;
  risk?: number;
  advisory_ids?: string[];
  data_source?: string;
  description?: string;
}

export interface Vulnerability {
  id: string;
  account_id: string;
  resource_id: string;
  resource_name: string;
  severity: string;
  status: string;
  account_object_id: string;
  updated_at: string;
  created_at: string;
  recommendation: VulnerabilityPayload;
}

const LIST_VULNERABILITIES = `
query ListVulnerabilities($limit: Int, $offset: Int) {
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

const SEVERITY_GROUPINGS = `
query VulnerabilitySeverityGroupings {
  groupings: recommendation_groupings_v2(where: __WHERE__, group_by: __GROUP_BY__) {
    rows {
      resource_id
      severity
      count
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
 * `queryGraphQL` resolves a 200 that carries a GraphQL `errors` array, so a
 * query that failed upstream reads as an empty result unless the caller looks.
 * On a vulnerability listing that is the worst failure mode available — "the
 * query broke" and "this account is clean" would render identically — so turn
 * it into a throw and let the caller say so.
 */
const assertNoErrors = (response: any, operation: string) => {
  const errors = response?.data?.errors;
  if (errors?.length) {
    throw new Error(errors[0]?.message || `${operation} failed`);
  }
};

const buildWhere = (accountId: string, extra: Record<string, any> = {}) => ({
  account_id: { _eq: accountId },
  rule_name: { _eq: VULNERABILITY_RULE },
  status: { _in: OPEN_STATUSES },
  ...extra,
});

const apiVulnerabilities = {
  /**
   * Open findings for an account, or for one host when `resourceId` is set.
   * `resourceId` is a `cloud_resourses.id` (the UUID), not the provider-side
   * instance id — findings are keyed to the resource row, not to `resourse_id`.
   */
  async list({
    accountId,
    resourceId,
    severity,
    limit = 10,
    offset = 0,
  }: {
    accountId: string;
    resourceId?: string;
    severity?: string;
    limit?: number;
    offset?: number;
  }) {
    if (!accountId || accountId === 'demo') return { rows: [], total: 0 };
    const extra: Record<string, any> = {};
    if (resourceId) extra.resource_id = { _eq: resourceId };
    if (severity) extra.severity = { _eq: severity };
    const query = LIST_VULNERABILITIES.replaceAll('__WHERE__', gqlStringify(buildWhere(accountId, extra)));
    const response = await queryGraphQL(query, 'ListVulnerabilities', { limit, offset });
    assertNoErrors(response, 'ListVulnerabilities');
    const rows = response?.data?.data?.findings?.rows || [];
    return {
      rows: rows.map((row: any) => ({ ...row, recommendation: safeParse(row.recommendation) || {} })) as Vulnerability[],
      total: response?.data?.data?.findings_aggregate?.rows?.[0]?.count || 0,
    };
  },

  /** Open-finding counts by severity, for an account or one host. */
  async getSeverityCounts({ accountId, resourceId }: { accountId: string; resourceId?: string }) {
    if (!accountId || accountId === 'demo') return {};
    const extra = resourceId ? { resource_id: { _eq: resourceId } } : {};
    const query = SEVERITY_GROUPINGS.replaceAll('__WHERE__', gqlStringify(buildWhere(accountId, extra))).replaceAll('__GROUP_BY__', '["severity"]');
    const response = await queryGraphQL(query, 'VulnerabilitySeverityGroupings', {});
    assertNoErrors(response, 'VulnerabilitySeverityGroupings');
    const bySeverity: Record<string, number> = {};
    for (const row of response?.data?.data?.groupings?.rows || []) {
      if (!row?.severity) continue;
      bySeverity[row.severity] = (bySeverity[row.severity] || 0) + (row.count || 0);
    }
    return bySeverity;
  },

  /** resource id → severity counts, for badging a resource listing. */
  async getSeverityCountsByResource(accountId: string) {
    if (!accountId || accountId === 'demo') return {};
    const query = SEVERITY_GROUPINGS.replaceAll('__WHERE__', gqlStringify(buildWhere(accountId))).replaceAll(
      '__GROUP_BY__',
      '["resource_id", "severity"]'
    );
    const response = await queryGraphQL(query, 'VulnerabilitySeverityGroupings', {});
    assertNoErrors(response, 'VulnerabilitySeverityGroupings');
    const byResource: Record<string, Record<string, number>> = {};
    for (const row of response?.data?.data?.groupings?.rows || []) {
      if (!row?.resource_id) continue;
      if (!byResource[row.resource_id]) byResource[row.resource_id] = {};
      const severity = row.severity || 'Info';
      byResource[row.resource_id][severity] = (byResource[row.resource_id][severity] || 0) + (row.count || 0);
    }
    return byResource;
  },
};

export default apiVulnerabilities;
