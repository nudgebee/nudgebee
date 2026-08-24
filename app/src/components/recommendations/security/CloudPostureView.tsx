import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import FilterDropdown from '@ui/FilterDropdown';
import DownloadButton from '@shared/buttons/DownloadButton';
import CustomTable from '@shared/tables/CustomTable';
import InfographicList from '@shared/widgets/InfographicList';
import Text from '@shared/format/Text';
import { SeverityIcon } from '@ui/SeverityIcon';
import { toSeverityLevel, safeJSONParse } from '@utils/common';
import { ds } from '@utils/colors';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { normalizeSeverity } from './securityFinding';
import { type CloudPostureRule, cloudRuleTitle, foldCloudPostureRules } from './cloudPosture';
import CloudRuleResources from './CloudRuleResources';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import { toast as snackbar } from '@ui/Toast';

/** What a posture ticket says, from the resource the reader acted on. */
const ticketDescription = (target: { rule: CloudPostureRule; resource: any } | null) => {
  if (!target) return '';
  const { rule, resource } = target;
  const payload = typeof resource?.recommendation === 'string' ? safeJSONParse(resource.recommendation) || {} : resource?.recommendation || {};
  return [
    `**Check**: ${rule.ruleName}`,
    `**Resource**: ${resource?.resource_name || payload.repository_name || resource?.resource_id || '-'}`,
    `**Severity**: ${normalizeSeverity(resource?.severity)}`,
    `**Provider**: ${rule.provider || '-'}`,
    payload?.reason ? `**Reason**: ${payload.reason}` : '',
  ]
    .filter(Boolean)
    .join('\n');
};

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
  const [isTicketFormOpen, setIsTicketFormOpen] = useState(false);
  const [ticketTarget, setTicketTarget] = useState<{ rule: CloudPostureRule; resource: any } | null>(null);
  const beginRequest = useLatestRequest();

  const closeTicketForm = useCallback(() => setIsTicketFormOpen(false), []);

  // Memoised: CloudRuleResources lists this in a dependency array, so an inline
  // object would rebuild every resource table on each render of the rollup.
  const resourceRowActions = useMemo(
    () => ({
      onCreateTicket: (rule: CloudPostureRule, resource: any) => {
        setTicketTarget({ rule, resource });
        setIsTicketFormOpen(true);
      },
    }),
    []
  );

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
  // Resources failing the checks currently in view — the same total the tab
  // strip's badge shows, so a severity filter narrows both together.
  const failingResources = useMemo(() => visible.reduce((total, rule) => total + (Number(rule.count) || 0), 0), [visible]);

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
      <TicketCreatePopupForm
        open={isTicketFormOpen}
        handleClose={closeTicketForm}
        onClose={closeTicketForm}
        onSuccess={({ ticketId }: any = {}) => snackbar.success(`Ticket ${ticketId || ''} created`)}
        onFailure={(res: any) => snackbar.error(`Failed! ${res}.`)}
        ticketData={{
          subject: `Cloud Posture - ${cloudRuleTitle(ticketTarget?.rule?.ruleName || '', undefined)}`,
          description: ticketDescription(ticketTarget),
          accountId: ticketTarget?.resource?.account_id,
        }}
        ticketUrl={{}}
        reference={{ id: ticketTarget?.resource?.id, type: 'cloud' }}
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
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          {/* Below the filter row, inside the card — the same slot and the same
              InfographicList the Image Scan tab uses for its Images/Apps pair.
              These totals were 11px grey text trailing the filters: output
              sitting in the input row, in the most de-emphasised text the app
              has, while the sibling Security tab gave identical information a
              block of its own. */}
          <Box sx={{ display: 'flex', justifyContent: 'space-between', mt: ds.space[3] }}>
            <InfographicList
              sequence={[
                { text: 'Checks', value: visible.length },
                { text: 'Resources', value: failingResources.toLocaleString() },
              ]}
            />
          </Box>
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
                  text: 'Resources',
                  value: 0,
                  key: 'cloud-posture-rule-resources',
                  componentFn: (_option: any, drilldownQuery: any) =>
                    drilldownQuery?.rule ? (
                      <CloudRuleResources
                        rule={drilldownQuery.rule}
                        scopeAccountId={accountId}
                        accountsById={accountsById}
                        rowActions={resourceRowActions}
                      />
                    ) : null,
                },
              ],
            }}
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
