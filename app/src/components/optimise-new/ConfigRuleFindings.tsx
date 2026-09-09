import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel } from '@utils/common';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import RowActions from './RowActions';
import { DEFAULT_STATUS, getResourceDisplayName } from './utils';

const PAGE_SIZE = 5;

const HEADERS = [
  { name: 'Severity', width: '8%' },
  { name: 'Resource', width: '37%' },
  { name: 'Account', width: '22%' },
  { name: 'Last Seen', width: '21%' },
  // Unnamed trailing actions column — the same shape every listing row in
  // Optimise carries its actions in.
  { name: '', width: '12%', align: 'right' as const },
];

export interface FindingRowActions {
  assistantName: string | undefined;
  onAskNubi: (rec: any) => void;
  onResolve: (rec: any) => void;
  onCreateTicket: (rec: any) => void;
  onCopyCli: (rec: any) => void;
  onDismiss: (rec: any) => void;
}

interface ConfigRuleFindingsProps {
  /** The check whose findings this expands to show. */
  ruleName: string;
  accountId: string | string[];
  status: string[];
  severity?: string[];
  accounts?: Record<string, { name: string; cloud_provider: string }>;
  /** Opens one finding's detail panel — the individual item, not the group. */
  onSelectRecommendation: (rec: any) => void;
  /** The same per-row quick actions the Cost tab's rows carry. */
  rowActions?: FindingRowActions;
}

/**
 * The resources failing one configuration check, shown inside its expanded row.
 *
 * Paginated rather than capped: a single check routinely spans every Lambda in
 * an account, and a silent "first N" would read as the whole story.
 */
const ConfigRuleFindings = ({ ruleName, accountId, status, severity, accounts, onSelectRecommendation, rowActions }: ConfigRuleFindingsProps) => {
  const [rows, setRows] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  // Serialised rather than compared by reference: the reset must survive a
  // caller that builds these arrays inline, where an identity check would fire
  // every render and pin the reader to page one.
  const scopeKey = `${ruleName}|${[accountId].flat().join(',')}|${status.join(',')}|${(severity || []).join(',')}`;
  useEffect(() => {
    // Narrowing the filters shrinks the result set, so a page offset from the
    // previous scope can land past the end and read as "no findings".
    setPage(0);
  }, [scopeKey]);

  const load = useCallback(async () => {
    if (!ruleName) return;
    setLoading(true);
    const isLatest = beginRequest();
    try {
      const result: any = await recommendationApi.getK8sRecommendation({
        accountId: accountId as any,
        category: 'Configuration',
        ruleName: [ruleName],
        status: status.length > 0 ? status : DEFAULT_STATUS,
        ...(severity?.length ? { severity } : {}),
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      });
      if (!isLatest()) return;
      setRows(result?.data?.recommendation || []);
      setTotal(result?.data?.recommendation_aggregate?.aggregate?.count || 0);
    } catch (error) {
      console.error('Failed to load findings for check:', error);
      if (isLatest()) {
        setRows([]);
        setTotal(0);
      }
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [ruleName, accountId, status, severity, page, beginRequest]);

  useEffect(() => {
    load();
  }, [load]);

  const tableData = useMemo(
    () =>
      rows.map((rec: any) => [
        {
          component: (
            <Box sx={{ display: 'flex', justifyContent: 'center' }}>
              <SeverityIcon level={toSeverityLevel(rec.severity)} aria-label={rec.severity || '-'} />
            </Box>
          ),
          drilldownQuery: { rec },
          data: rec.severity,
        },
        { component: <Text value={getResourceDisplayName(rec, '—')} showAutoEllipsis />, data: getResourceDisplayName(rec, '') },
        { component: <Text value={accounts?.[rec.account_id]?.name || rec.account_id || '—'} showAutoEllipsis />, data: rec.account_id },
        { component: <Datetime value={rec.updated_at} />, data: rec.updated_at },
        ...(rowActions
          ? [
              {
                component: (
                  <RowActions
                    rowId={rec.id}
                    rec={rec}
                    ticketId={rec.ticket?.ticket_id || ''}
                    assistantName={rowActions.assistantName}
                    onAskNubi={rowActions.onAskNubi}
                    onResolve={rowActions.onResolve}
                    onCreateTicket={rowActions.onCreateTicket}
                    onCopyCli={rowActions.onCopyCli}
                    onDismiss={rowActions.onDismiss}
                  />
                ),
              },
            ]
          : [{ component: null }]),
      ]),
    [rows, accounts, rowActions]
  );

  return (
    // CustomTable styles its cells with descendant selectors, so the rollup's
    // `tbody tr td:last-of-type { position: sticky }` also lands on THIS table,
    // nested as it is inside the rollup's expanded row. A sticky cell is its own
    // stacking context, which left an action menu opened from the last column
    // painted over by the sticky cells of the rows it overflowed onto. Nothing
    // here scrolls horizontally on its own, so the stickiness is inherited by
    // accident and only costs us — drop it and the menu layers normally.
    <Box sx={{ '& tbody tr td:last-of-type': { position: 'static' } }}>
      <CustomTable
        id={`optimise-config-findings-${ruleName}`}
        headers={HEADERS}
        tableData={tableData}
        loading={loading}
        rowsPerPage={PAGE_SIZE}
        totalRows={total}
        pageNumber={page + 1}
        onPageChange={(next: number) => setPage(Math.max(0, next - 1))}
        tableHeadingCenter={['Severity']}
        onRowClick={(query: any) => query?.rec && onSelectRecommendation(query.rec)}
        showUpdatedEmptyData={!loading && tableData.length === 0}
        emptyHeading='No findings'
        emptySubHeading='Nothing currently fails this check in the accounts in view.'
      />
    </Box>
  );
};

export default ConfigRuleFindings;
