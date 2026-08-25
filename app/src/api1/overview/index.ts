import { gqlStringify, queryGraphQL } from '@lib/HttpService';
import { getEndOfDay, getEndOfMonth, getEndOfYear, getLast7Days, getStartOfMonth, getStartOfYear } from '@lib/datetime';
import { OPEN_STATUSES, VM_VULNERABILITY_RULE } from '@api1/vm';

/**
 * Fleet-wide account rollups for the Account Overview page (`/overview`).
 *
 * The K8s cards on that page keep their own per-cluster fan-out (four calls per
 * cluster, see components/k8s/common/*), but everything added here is batched:
 * one query returns a row per account because the query engine auto-groups an
 * Aggregate table by every non-aggregated column in the selection set (see
 * generateGroupClause in api-server/services/query/sql.go). A per-account
 * fan-out across AWS + Azure + GCP + VM would be dozens of round trips on a
 * page that is already request-heavy.
 *
 * Cloud and VM are two separate documents rather than one, deliberately: both
 * would read `recommendation_groupings_v2` with different selection sets, and
 * the gateway resolves a selection set by FIELD name and not by alias — the
 * second alias of a repeated field silently inherits the first one's columns.
 */

/** Alert sources that count as an "alert" on a cloud account, per provider. */
const CLOUD_ALERT_SOURCES = ['AWS_CloudWatch_Alarm', 'azure_monitor_webhook', 'gcp_monitoring_alert'];

export interface CloudAccountOverviewSummary {
  accountId: string;
  currency: string;
  mtdSpend: number;
  lastMonthSpend: number;
  ytdSpend: number;
  resourceCount: number;
  recommendationCount: number;
  /** Sum of open recommendations' estimated (monthly) savings. */
  estimatedSavings: number;
  /** Provider alerts raised in the last 7 days. */
  alertCount: number;
}

export interface VmAccountOverviewSummary {
  accountId: string;
  vmCount: number;
  /** Open package-vulnerability findings keyed by severity. */
  severities: Record<string, number>;
  vulnerabilityCount: number;
}

const CLOUD_ACCOUNTS_OVERVIEW = `
query CloudAccountsOverview {
  mtd: spend_groupings_v2(where: __WHERE_CM__){
    rows{ account_id spend_amount currency_type }
  }
  prev_month: spend_groupings_v2(where: __WHERE_LM__){
    rows{ account_id spend_amount currency_type }
  }
  ytd: spend_groupings_v2(where: __WHERE_YR__){
    rows{ account_id spend_amount currency_type }
  }
  recommendations: recommendation_groupings_v2(where: __WHERE_REC__, columns: ["account_id", "count", "sum_estimated_savings"]){
    rows{ account_id count sum_estimated_savings }
  }
  resources: cloud_resource_groupings_v2(where: __WHERE_RES__){
    rows{ account_id count }
  }
  alerts: event_groupings_v2(where: __WHERE_EVT__){
    rows{ account_id event_count }
  }
}`;

const VM_ACCOUNTS_OVERVIEW = `
query VmAccountsOverview {
  resources: cloud_resource_groupings_v2(where: __WHERE_RES__){
    rows{ account_id count }
  }
  vulnerabilities: recommendation_groupings_v2(where: __WHERE_VULN__, columns: ["account_id", "severity", "count"]){
    rows{ account_id severity count }
  }
}`;

const emptyCloudSummary = (accountId: string): CloudAccountOverviewSummary => ({
  accountId,
  currency: '',
  mtdSpend: 0,
  lastMonthSpend: 0,
  ytdSpend: 0,
  resourceCount: 0,
  recommendationCount: 0,
  estimatedSavings: 0,
  alertCount: 0,
});

const apiOverview = {
  /**
   * One summary row per AWS / Azure / GCP / CloudFoundry account. Accounts with
   * no data at all still get a zeroed entry so a card renders rather than
   * disappearing.
   */
  async listCloudAccountSummaries(accountIds: string[]): Promise<Record<string, CloudAccountOverviewSummary>> {
    if (!accountIds?.length) {
      return {};
    }
    const now = new Date();
    // Pin to the 1st before stepping back a month — on the 29th-31st setMonth
    // alone overflows (e.g. Jul 31 -> "Jun 31" -> Jul 1).
    const lastMonth = new Date();
    lastMonth.setDate(1);
    lastMonth.setMonth(lastMonth.getMonth() - 1);

    const iso = (d: Date | string) => (d instanceof Date ? d.toISOString() : d);
    const spendWhere = (start: Date | string, end: Date | string) => ({
      account_id: { _in: accountIds },
      _and: [{ spend_date: { _gte: iso(start) } }, { spend_date: { _lte: iso(end) } }, { exclude_aggregate: { _eq: false } }],
    });

    const query = CLOUD_ACCOUNTS_OVERVIEW.replace('__WHERE_CM__', gqlStringify(spendWhere(getStartOfMonth(now), getEndOfMonth(now))))
      .replace('__WHERE_LM__', gqlStringify(spendWhere(getStartOfMonth(lastMonth), getEndOfMonth(lastMonth))))
      .replace('__WHERE_YR__', gqlStringify(spendWhere(getStartOfYear(now), getEndOfYear(now))))
      .replace('__WHERE_REC__', gqlStringify({ account_id: { _in: accountIds }, status: { _in: ['Open', 'Assigned'] } }))
      .replace('__WHERE_RES__', gqlStringify({ account_id: { _in: accountIds }, status: { _eq: 'Active' } }))
      .replace(
        '__WHERE_EVT__',
        gqlStringify({
          account_id: { _in: accountIds },
          source: { _in: CLOUD_ALERT_SOURCES },
          _and: [{ created_at: { _gte: iso(getLast7Days()) } }, { created_at: { _lte: iso(getEndOfDay()) } }],
        })
      );

    const response = await queryGraphQL(query, 'CloudAccountsOverview', {});
    const data = response?.data?.data;
    const summaries: Record<string, CloudAccountOverviewSummary> = {};
    accountIds.forEach((id) => {
      summaries[id] = emptyCloudSummary(id);
    });
    const forAccount = (id: string) => (id && summaries[id] ? summaries[id] : null);

    (data?.mtd?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.mtdSpend += row?.spend_amount || 0;
      summary.currency = summary.currency || row?.currency_type || '';
    });
    (data?.prev_month?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.lastMonthSpend += row?.spend_amount || 0;
      summary.currency = summary.currency || row?.currency_type || '';
    });
    (data?.ytd?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.ytdSpend += row?.spend_amount || 0;
      summary.currency = summary.currency || row?.currency_type || '';
    });
    (data?.recommendations?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.recommendationCount += row?.count || 0;
      summary.estimatedSavings += row?.sum_estimated_savings || 0;
    });
    (data?.resources?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.resourceCount += row?.count || 0;
    });
    (data?.alerts?.rows || []).forEach((row: any) => {
      const summary = forAccount(row?.account_id);
      if (!summary) return;
      summary.alertCount += row?.event_count || 0;
    });

    return summaries;
  },

  /** One summary row per self-hosted (VM) account. */
  async listVmAccountSummaries(accountIds: string[]): Promise<Record<string, VmAccountOverviewSummary>> {
    if (!accountIds?.length) {
      return {};
    }
    const query = VM_ACCOUNTS_OVERVIEW.replace('__WHERE_RES__', gqlStringify({ account_id: { _in: accountIds } })).replace(
      '__WHERE_VULN__',
      gqlStringify({ account_id: { _in: accountIds }, rule_name: { _eq: VM_VULNERABILITY_RULE }, status: { _in: OPEN_STATUSES } })
    );

    const response = await queryGraphQL(query, 'VmAccountsOverview', {});
    const data = response?.data?.data;
    const summaries: Record<string, VmAccountOverviewSummary> = {};
    accountIds.forEach((id) => {
      summaries[id] = { accountId: id, vmCount: 0, severities: {}, vulnerabilityCount: 0 };
    });

    (data?.resources?.rows || []).forEach((row: any) => {
      const summary = summaries[row?.account_id];
      if (!summary) return;
      summary.vmCount += row?.count || 0;
    });
    (data?.vulnerabilities?.rows || []).forEach((row: any) => {
      const summary = summaries[row?.account_id];
      if (!summary) return;
      const severity = row?.severity || 'Unknown';
      const count = row?.count || 0;
      summary.severities[severity] = (summary.severities[severity] || 0) + count;
      summary.vulnerabilityCount += count;
    });

    return summaries;
  },
};

export default apiOverview;
