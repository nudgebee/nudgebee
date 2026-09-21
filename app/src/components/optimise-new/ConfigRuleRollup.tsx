import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import ConfigRuleFindings, { type FindingRowActions } from './ConfigRuleFindings';
import { formatRuleName, lastSeenBucketToParams } from './utils';
import { type ConfigRule, foldConfigRules, rankSeverity } from './configRollup';

const TABLE_ID = 'optimise-config-rules';

const HEADERS = [
  { name: 'Severity', width: '8%' },
  { name: 'Check', width: '46%' },
  { name: 'Accounts', width: '32%' },
  { name: 'Findings', width: '14%' },
];

interface ConfigRuleRollupProps {
  /** Accounts in view — the page's Account filter, or every account when unset. */
  accountId: string | string[];
  status: string[];
  severity?: string[];
  /** Safety bands in view — the page's Safety chips. Applied server-side. */
  safety?: string[];
  /** Last-seen bucket key — the page's Last seen filter. Applied server-side. */
  lastSeen?: string;
  accounts?: Record<string, { name: string; cloud_provider: string }>;
  /** Opens one finding's detail panel, from inside an expanded check. */
  onSelectRecommendation: (rec: any) => void;
  /** Per-row quick actions, passed through to the expanded findings. */
  rowActions?: FindingRowActions;
}

/**
 * Configuration findings across the accounts in view, one row per check.
 *
 * These findings are emitted per resource, so a tenant with a few hundred Lambda
 * functions carries one row per function per check — thousands of rows for around
 * ninety distinct checks, none of which carry savings. Grouping by check restores
 * the scale a reader can act on; the row drills into the resources.
 */
const ConfigRuleRollup = ({ accountId, status, severity, safety, lastSeen, accounts, onSelectRecommendation, rowActions }: ConfigRuleRollupProps) => {
  const [rules, setRules] = useState<ConfigRule[]>([]);
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  const load = useCallback(async () => {
    const scope = accountId;
    if (!scope || (Array.isArray(scope) && scope.length === 0)) {
      setRules([]);
      return;
    }
    setLoading(true);
    const isLatest = beginRequest();
    try {
      const rows = await recommendationApi.listRecommendationRuleRollup({
        accountId: scope,
        category: 'Configuration',
        status,
        safetyBand: safety?.length ? safety : undefined,
        // Translated at call time, not at render: the bucket resolves against
        // Date.now(), so an ISO string prop would change identity every render
        // and re-run this effect in a loop.
        ...lastSeenBucketToParams(lastSeen || ''),
      });
      if (!isLatest()) return;
      setRules(foldConfigRules(rows));
    } catch (error) {
      console.error('Failed to load configuration rules:', error);
      if (isLatest()) setRules([]);
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, status, safety, lastSeen, beginRequest]);

  useEffect(() => {
    load();
  }, [load]);

  // Severity is filtered here rather than in the query: the aggregate returns a
  // row per (rule, severity, account), so narrowing server-side would drop a
  // rule's other bands and understate its count. A check matches when it has any
  // finding in a selected band, not when its worst band happens to be selected —
  // and the count then reports only the findings in those bands, so a check with
  // 2 Critical and 159 Medium reads as 2 under a Critical filter, not 161.
  const visible = useMemo(() => {
    if (!severity?.length) return rules;
    const matching = rules.reduce<ConfigRule[]>((kept, rule) => {
      let matched = 0;
      let worst = 'Unknown';
      for (const band of severity) {
        const inBand = rule.countBySeverity[band] || 0;
        if (inBand <= 0) continue;
        matched += inBand;
        if (rankSeverity(band) > rankSeverity(worst)) worst = band;
      }
      // The badge reports the worst band *within the selection*, not the worst
      // the rule reaches: the count beside it is already the in-selection count,
      // and a row reading "Critical · 159" for a check with 2 Critical and 159
      // Medium describes two different sets of findings in one line. The account
      // and band splits narrow with it, for the same reason — and an account
      // whose findings are all in unselected bands drops off the row entirely.
      if (matched > 0) {
        const countByAccount: Record<string, number> = {};
        for (const [acct, bands] of Object.entries(rule.countByAccountSeverity)) {
          let inSelection = 0;
          for (const band of severity) inSelection += bands[band] || 0;
          if (inSelection > 0) countByAccount[acct] = inSelection;
        }
        const countBySeverity: Record<string, number> = {};
        for (const band of severity) {
          if (rule.countBySeverity[band]) countBySeverity[band] = rule.countBySeverity[band];
        }
        kept.push({ ...rule, count: matched, severity: worst, countByAccount, countBySeverity, accountIds: Object.keys(countByAccount) });
      }
      return kept;
    }, []);
    // Re-sorted because both keys just changed: the fold ordered by the rule's
    // totals, and the list has to be ordered by the numbers it is showing.
    return matching.sort((a, b) => rankSeverity(b.severity) - rankSeverity(a.severity) || b.count - a.count);
  }, [rules, severity]);

  // The accounts a check fires in, named and counted — "3 accounts" says a
  // check is not isolated but not which estate to go and fix, and names alone
  // hide that 110 of 117 findings sit in one cluster. Loudest first, because
  // that is the order a reader visits them in; a lone account keeps just its
  // name, since the Findings column already carries its number. Falls back to
  // the id for an account that has not loaded yet rather than dropping it.
  const accountLabel = useCallback(
    (rule: ConfigRule) => {
      const parts = rule.accountIds
        .map((id) => ({ name: accounts?.[id]?.name || id, count: rule.countByAccount[id] || 0 }))
        .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
      if (parts.length === 0) return '—';
      if (parts.length === 1) return parts[0].name;
      return parts.map(({ name, count }) => `${name} (${count.toLocaleString()})`).join(', ');
    },
    [accounts]
  );

  const tableData = useMemo(
    () =>
      visible.map((rule) => {
        const catalog: any = recommendationApi.getRecommendationDetails('Configuration', rule.ruleName) || {};
        const title = catalog.title || formatRuleName(rule.ruleName, 'Configuration');
        // Most checks have no catalog title, so the display name is the slug
        // re-cased and the subtitle would print the same words twice. The slug
        // earns its line only when the title is a real mapping ("Missing
        // Resource Requests" over pod_right_sizing), where it is the name a
        // reader greps their alerts or the API for.
        const showSlug = title.toLowerCase() !== rule.ruleName.replace(/_/g, ' ').toLowerCase();
        return [
          {
            component: (
              <Box sx={{ display: 'flex', justifyContent: 'center' }}>
                <SeverityIcon level={toSeverityLevel(rule.severity)} aria-label={rule.severity || '-'} />
              </Box>
            ),
            drilldownQuery: { ruleName: rule.ruleName },
            data: rule.severity,
          },
          {
            component: (
              <Box>
                <Text value={title} showAutoEllipsis />
                {showSlug && <Text secondaryText value={rule.ruleName} showAutoEllipsis />}
              </Box>
            ),
            data: rule.ruleName,
          },
          { component: <Text value={accountLabel(rule)} showAutoEllipsis />, data: accountLabel(rule) },
          { component: <Text value={rule.count.toLocaleString()} />, data: rule.count },
        ];
      }),
    [visible, accountLabel]
  );

  return (
    <CustomTable
      id={TABLE_ID}
      headers={HEADERS}
      tableData={tableData}
      loading={loading}
      rowsPerPage={tableData.length}
      totalRows={tableData.length}
      tableHeadingCenter={['Severity']}
      showExpandable
      expandable={{
        tabs: [
          {
            text: 'Findings',
            value: 0,
            key: 'optimise-config-rule-findings',
            componentFn: (_option: any, drilldownQuery: any) => (
              <ConfigRuleFindings
                ruleName={drilldownQuery?.ruleName}
                accountId={accountId}
                status={status}
                severity={severity}
                safety={safety}
                lastSeen={lastSeen}
                accounts={accounts}
                onSelectRecommendation={onSelectRecommendation}
                rowActions={rowActions}
              />
            ),
          },
        ],
      }}
      showUpdatedEmptyData={tableData.length === 0}
      emptyHeading='No configuration findings'
      emptySubHeading='Checks appear here once an account has been scanned.'
    />
  );
};

export default ConfigRuleRollup;
