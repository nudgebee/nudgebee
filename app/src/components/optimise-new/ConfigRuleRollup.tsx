import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import ConfigRuleFindings, { type FindingRowActions } from './ConfigRuleFindings';
import { formatRuleName } from './utils';
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
const ConfigRuleRollup = ({ accountId, status, severity, accounts, onSelectRecommendation, rowActions }: ConfigRuleRollupProps) => {
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
      });
      if (!isLatest()) return;
      setRules(foldConfigRules(rows));
    } catch (error) {
      console.error('Failed to load configuration rules:', error);
      if (isLatest()) setRules([]);
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, status, beginRequest]);

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
      // Medium describes two different sets of findings in one line.
      if (matched > 0) kept.push({ ...rule, count: matched, severity: worst });
      return kept;
    }, []);
    // Re-sorted because both keys just changed: the fold ordered by the rule's
    // totals, and the list has to be ordered by the numbers it is showing.
    return matching.sort((a, b) => rankSeverity(b.severity) - rankSeverity(a.severity) || b.count - a.count);
  }, [rules, severity]);

  // The accounts a check fires in, named rather than counted — "3" says a check
  // is not isolated but not which estate to go and fix. Sorted so the same set
  // always reads the same way, and falling back to the id for an account that
  // has not loaded yet rather than dropping it from the list.
  const accountLabel = useCallback(
    (accountIds: string[]) =>
      accountIds
        .map((id) => accounts?.[id]?.name || id)
        .sort((a, b) => a.localeCompare(b))
        .join(', ') || '—',
    [accounts]
  );

  const tableData = useMemo(
    () =>
      visible.map((rule) => {
        const catalog: any = recommendationApi.getRecommendationDetails('Configuration', rule.ruleName) || {};
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
                <Text value={catalog.title || formatRuleName(rule.ruleName, 'Configuration')} showAutoEllipsis />
                <Text secondaryText value={rule.ruleName} showAutoEllipsis />
              </Box>
            ),
            data: rule.ruleName,
          },
          { component: <Text value={accountLabel(rule.accountIds)} showAutoEllipsis />, data: accountLabel(rule.accountIds) },
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
