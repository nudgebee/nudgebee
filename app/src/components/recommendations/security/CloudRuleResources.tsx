import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import ThreeDotsMenu from '@ui/ThreeDotsMenu';
import { SeverityIcon } from '@ui/SeverityIcon';
import MarkDowns from '@shared/viewers/MarkDowns';
import { toSeverityLevel, safeJSONParse } from '@utils/common';
import { ds } from '@utils/colors';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { action } from '@utils/actionStyles';
import { normalizeSeverity } from './securityFinding';
import { type CloudPostureRule } from './cloudPosture';

const PAGE_SIZE = 5;

const HEADERS = [
  { name: 'Severity', width: '10%' },
  { name: 'Resource', width: '38%' },
  { name: 'Account', width: '24%' },
  { name: 'Last seen', width: '18%' },
  // Unnamed trailing actions column — the shape every other listing row in
  // Optimise carries its actions in.
  { name: '', width: '10%', align: 'right' as const },
];

export interface CloudResourceRowActions {
  onCreateTicket: (rule: CloudPostureRule, resource: any) => void;
}

/**
 * One labelled paragraph of the check's brief.
 *
 * Borrows the panel's section-label treatment (uppercase caption, gray-500) so
 * the drawer and the side panels read as the same product, but drops that
 * primitive's full-width rule: three horizontal lines across a 1300px drawer is
 * exactly the chrome this redesign is removing. Without a label these three
 * blocks are indistinguishable paragraphs — a reader cannot tell the problem
 * from the reason from the fix.
 */
const BriefBlock = ({ label, children, fullWidth }: { label: string; children: React.ReactNode; fullWidth?: boolean }) => (
  <Box sx={{ ...(fullWidth ? { gridColumn: '1 / -1' } : {}), minWidth: 0 }}>
    <Typography
      sx={{
        fontSize: ds.text.caption,
        fontWeight: ds.weight.medium,
        color: ds.gray[500],
        textTransform: 'uppercase',
        letterSpacing: '0.08em',
        mb: ds.space[1],
      }}
    >
      {label}
    </Typography>
    {typeof children === 'string' ? (
      <Typography sx={{ fontSize: ds.text.small, lineHeight: 1.6, color: ds.gray[700] }}>{children}</Typography>
    ) : (
      children
    )}
  </Box>
);

interface CloudRuleResourcesProps {
  rule: CloudPostureRule;
  /** Accounts in view — the page's Account filter, or the rule's own accounts. */
  scopeAccountId?: string | string[];
  accountsById?: Record<string, string>;
  rowActions?: CloudResourceRowActions;
}

/**
 * The resources failing one cloud posture check, shown inside its expanded row.
 *
 * Mirrors ConfigRuleFindings: posture findings are emitted per resource, so a
 * check routinely spans every bucket or instance in an account. Paginated
 * rather than capped, because a silent "first N" would read as the whole story.
 *
 * The brief above the list is what the rule's side panel used to carry. The
 * check's description and remediation are the only part of that panel a reader
 * could not reconstruct from the rows themselves, so it moves here rather than
 * being dropped when the panel does.
 */
const CloudRuleResources = ({ rule, scopeAccountId, accountsById, rowActions }: CloudRuleResourcesProps) => {
  const [rows, setRows] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  const catalog: any = useMemo(
    () => (rule?.ruleName ? recommendationApi.getRecommendationDetails('Security', rule.ruleName) || {} : {}),
    [rule?.ruleName]
  );

  // Catalog field names, not guesses: the rule panel read `mitigations` for the
  // remediation steps and `recommendations` for the why-it-matters line.
  const guidance: string = useMemo(() => (Array.isArray(catalog?.recommendations) ? catalog.recommendations[0] || '' : ''), [catalog]);
  const remediation: string = useMemo(() => (Array.isArray(catalog?.mitigations) ? catalog.mitigations.join('\n\n') : ''), [catalog]);
  // A fenced block or an indented shell line needs the full row to stay readable.
  const remediationHasCode = useMemo(() => /```|\n {4}\S|^\s{4}\S/m.test(remediation), [remediation]);
  const hasBrief = Boolean(catalog?.description || guidance || remediation);
  // Track count comes from how many blocks actually share the row, not from the
  // available width: `auto-fit` on a 1300px drawer mints five 260px tracks and
  // crams two paragraphs into the leftmost two, leaving half the drawer blank.
  const briefColumns = Math.max(1, [catalog?.description, guidance, remediation && !remediationHasCode].filter(Boolean).length);

  // Serialised rather than compared by reference so a caller building these
  // inline cannot pin the reader to page one on every render.
  const scopeKey = `${rule?.ruleName}|${[scopeAccountId ?? rule?.accountIds].flat().join(',')}`;
  useEffect(() => {
    setPage(0);
  }, [scopeKey]);

  const load = useCallback(async () => {
    if (!rule?.ruleName) return;
    setLoading(true);
    const isLatest = beginRequest();
    try {
      const res: any = await recommendationApi.listCloudPostureResources({
        accountId: (scopeAccountId || rule.accountIds) as any,
        ruleName: rule.ruleName,
        limit: PAGE_SIZE,
        offset: page * PAGE_SIZE,
      });
      if (!isLatest()) return;
      setRows(res?.rows || []);
      setTotal(res?.total || 0);
    } catch (error) {
      console.error('Failed to load cloud posture resources:', error);
      if (isLatest()) {
        setRows([]);
        setTotal(0);
      }
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [rule?.ruleName, rule?.accountIds, scopeAccountId, page, beginRequest]);

  useEffect(() => {
    load();
  }, [load]);

  const tableData = useMemo(
    () =>
      rows.map((row: any) => {
        const payload = typeof row.recommendation === 'string' ? safeJSONParse(row.recommendation) || {} : row.recommendation || {};
        const name = row.resource_name || payload.repository_name || row.resource_id || '—';
        const severity = normalizeSeverity(row.severity);
        return [
          {
            component: (
              <Box sx={{ display: 'flex', justifyContent: 'center' }}>
                <SeverityIcon level={toSeverityLevel(severity)} aria-label={severity || '-'} />
              </Box>
            ),
            data: severity,
          },
          { component: <Text value={name} showAutoEllipsis />, data: name },
          {
            component: <Text value={accountsById?.[row.account_id] || row.account_id || '—'} showAutoEllipsis />,
            data: row.account_id,
          },
          { component: <Datetime value={row.updated_at} />, data: row.updated_at },
          {
            component: rowActions ? (
              <Box display='flex' justifyContent='flex-end'>
                <ThreeDotsMenu
                  sx={{ ...action.primary }}
                  menuItems={[{ id: 'create-ticket', label: 'Create ticket' }]}
                  data={row}
                  onMenuClick={() => rowActions.onCreateTicket(rule, row)}
                />
              </Box>
            ) : null,
          },
        ];
      }),
    [rows, accountsById, rowActions, rule]
  );

  return (
    // CustomTable styles its cells with descendant selectors, so the parent
    // rollup's sticky last column also lands on this nested table. Nothing here
    // scrolls horizontally, so the stickiness is inherited by accident and only
    // costs us — it leaves an open action menu painted under the next row.
    <Box sx={{ '& tbody tr td:last-of-type': { position: 'static' } }}>
      {hasBrief && (
        <Box
          sx={{
            display: 'grid',
            // Three short paragraphs stacked down a 1300px drawer push the
            // resource list — the thing the reader expanded the row for — below
            // the fold. Side by side they stay one or two lines tall and the
            // width finally does some work. Remediation carrying a code block
            // takes the full row instead, since a shell command in a third of
            // the drawer wraps into nonsense.
            gridTemplateColumns: { xs: '1fr', md: `repeat(${briefColumns}, minmax(0, 1fr))` },
            columnGap: ds.space[5],
            rowGap: ds.space[3],
            pb: ds.space[4],
          }}
        >
          {catalog?.description && <BriefBlock label='What this checks'>{catalog.description}</BriefBlock>}
          {guidance && <BriefBlock label='Why it matters'>{guidance}</BriefBlock>}
          {remediation && (
            <BriefBlock label='Remediation' fullWidth={remediationHasCode}>
              <MarkDowns
                data={remediation}
                sx={{ padding: 0, fontSize: ds.text.small, '& p:last-child': { marginBottom: 0 } }}
                allowExecutable={false}
                onLinkClick={null}
              />
            </BriefBlock>
          )}
        </Box>
      )}
      <CustomTable
        id={`cloud-posture-resources-${rule?.ruleName}`}
        headers={HEADERS}
        tableData={tableData}
        loading={loading}
        rowsPerPage={PAGE_SIZE}
        totalRows={total}
        pageNumber={page + 1}
        onPageChange={(next: number) => setPage(Math.max(0, next - 1))}
        tableHeadingCenter={['Severity']}
        showUpdatedEmptyData={!loading && tableData.length === 0}
        emptyHeading='No failing resources'
        emptySubHeading='Nothing currently fails this check in the accounts in view.'
      />
    </Box>
  );
};

export default CloudRuleResources;
