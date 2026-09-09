import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import { ds } from '@utils/colors';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { normalizeSeverity } from './securityFinding';
import { type CloudPostureRule, cloudRuleTitle, foldCloudPostureRules } from './cloudPosture';
import CloudRulePanel from './CloudRulePanel';

const TABLE_ID = 'cloud-posture-rules';

const HEADERS = [
  { name: 'Severity', width: '8%' },
  { name: 'Check', width: '48%' },
  { name: 'Accounts', width: '14%' },
  { name: 'Resources', width: '14%' },
  { name: 'Provider', width: '16%' },
];

const SEVERITY_OPTIONS = ['Critical', 'High', 'Medium', 'Low', 'Info'].map((s) => ({ label: s, value: s }));

interface CloudPostureViewProps {
  /** Cloud accounts in view. */
  accountId: string | string[];
  accountsById?: Record<string, string>;
  providerById?: Record<string, string>;
  /** Rendered first in the toolbar — the Security tab's Account picker. */
  leadingFilters?: React.ReactNode;
}

/**
 * Cloud security posture across accounts, one row per check.
 *
 * The per-account cloud page lists these findings flat, one row per failing
 * resource. Across accounts that is thousands of rows for about a hundred
 * distinct checks, so this view groups by check and drills into the resources —
 * the same treatment the CIS sub-tab gives Kubernetes benchmark rules, and for
 * the same reason: the check is the unit you act on.
 */
const CloudPostureView = ({ accountId, accountsById, providerById, leadingFilters }: CloudPostureViewProps) => {
  const [rules, setRules] = useState<CloudPostureRule[]>([]);
  const [loading, setLoading] = useState(false);
  const [severity, setSeverity] = useState<string | null>(null);
  const [panelRule, setPanelRule] = useState<CloudPostureRule | null>(null);
  const beginRequest = useLatestRequest();

  const providerOf = useCallback((id: string) => providerById?.[id], [providerById]);

  const load = useCallback(async () => {
    const scope = accountId;
    if (!scope || (Array.isArray(scope) && scope.length === 0)) {
      setRules([]);
      return;
    }
    setLoading(true);
    const isLatest = beginRequest();
    try {
      const rows = await recommendationApi.listCloudPostureRules({ accountId: scope });
      if (!isLatest()) return;
      setRules(foldCloudPostureRules(rows, providerOf));
    } catch (error) {
      console.error('Failed to load cloud posture rules:', error);
      if (isLatest()) setRules([]);
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [accountId, providerOf, beginRequest]);

  useEffect(() => {
    load();
  }, [load]);

  const visible = useMemo(() => (severity ? rules.filter((r) => normalizeSeverity(r.severity) === severity) : rules), [rules, severity]);

  const tableData = useMemo(
    () =>
      visible.map((rule) => {
        const catalog = recommendationApi.getRecommendationDetails('Security', rule.ruleName) || {};
        return [
          {
            component: (
              <Box sx={{ display: 'flex', justifyContent: 'center' }}>
                <SeverityIcon level={toSeverityLevel(rule.severity)} aria-label={normalizeSeverity(rule.severity)} />
              </Box>
            ),
            drilldownQuery: { rule },
            data: rule.severity,
          },
          {
            component: (
              <Box>
                <Text value={cloudRuleTitle(rule.ruleName, catalog)} showAutoEllipsis />
                <Text secondaryText value={rule.ruleName} showAutoEllipsis />
              </Box>
            ),
            data: rule.ruleName,
          },
          { component: <Text value={String(rule.accountIds.length)} />, data: rule.accountIds.length },
          { component: <Text value={String(rule.count)} />, data: rule.count },
          { component: <Text value={rule.provider || '—'} />, data: rule.provider || '' },
        ];
      }),
    [visible]
  );

  return (
    <>
      <CloudRulePanel
        open={Boolean(panelRule)}
        onClose={() => setPanelRule(null)}
        rule={panelRule}
        accountsById={accountsById}
        scopeAccountId={accountId}
      />
      <ListingLayout id='cloud-posture'>
        <ListingLayout.Toolbar actions={<DownloadButton id={`${TABLE_ID}-download`} onClick={() => ({ tableId: TABLE_ID })} />}>
          {leadingFilters}
          <FilterDropdown
            id='cloud-posture-severity'
            label='Severity'
            value={severity}
            options={SEVERITY_OPTIONS}
            onSelect={(event: any) => setSeverity(event?.target?.value || null)}
          />
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], alignSelf: 'center' }}>
            {visible.length} {visible.length === 1 ? 'check' : 'checks'} failing
          </Typography>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable
            id={TABLE_ID}
            headers={HEADERS}
            tableData={tableData}
            loading={loading}
            rowsPerPage={tableData.length}
            totalRows={tableData.length}
            tableHeadingCenter={['Severity']}
            onRowClick={(query: any) => query?.rule && setPanelRule(query.rule)}
            showUpdatedEmptyData={tableData.length === 0}
            emptyHeading='No cloud posture findings'
            emptySubHeading='Checks appear here after a cloud account is scanned.'
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default CloudPostureView;
