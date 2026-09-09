import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import RuleOutlinedIcon from '@mui/icons-material/RuleOutlined';
import InventoryOutlinedIcon from '@mui/icons-material/Inventory2Outlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import CustomDrawer from '@shared/CustomDrawer';
import Tabs from '@shared/navigation/Tabs';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import Datetime from '@shared/format/Datetime';
import MarkDowns from '@shared/viewers/MarkDowns';
import { Label, type LabelTone } from '@ui/Label';
import { Button } from '@ui/Button';
import { ds } from '@utils/colors';
import { safeJSONParse } from '@utils/common';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { EmptyNote, Field, FieldList, PanelActions, Section, SectionHeading } from './panelPrimitives';
import { normalizeSeverity } from './securityFinding';
import { type CloudPostureRule, cloudRuleTitle } from './cloudPosture';

const SEVERITY_TONE: Record<string, LabelTone> = {
  Critical: 'critical',
  High: 'critical',
  Medium: 'warning',
  Low: 'info',
  Info: 'neutral',
};

const severityTone = (s: string): LabelTone => SEVERITY_TONE[normalizeSeverity(s)] ?? 'neutral';

const RESOURCE_HEADERS = [
  { name: 'Resource', width: '38%' },
  { name: 'Account', width: '22%' },
  { name: 'Severity', width: '15%' },
  { name: 'Last seen', width: '25%' },
];

interface CloudRulePanelProps {
  open: boolean;
  onClose: () => void;
  rule: CloudPostureRule | null;
  accountsById?: Record<string, string>;
  /** Accounts in view — the resource list and account rollup stay inside it. */
  scopeAccountId?: string | string[];
  onCreateTicket?: (rule: CloudPostureRule) => void;
}

/**
 * Detail panel for one cloud security posture check.
 *
 * Same shape as the CIS rule panel rather than the CVE finding panel, because
 * the data has the same shape: a policy check failing on N resources, with no
 * CVE, CVSS or fixed version to show. The check is what you act on — you enable
 * encryption for a service, not for one bucket — so the rule gets the panel and
 * the resources are its evidence.
 *
 * Titles and remediation come from the recommendation catalog rather than the
 * rule name, which matters because the vocabulary is open-ended: a fifth of the
 * rules are `azure_defender_assessment_<uuid>` and unreadable raw.
 */
const CloudRulePanel = ({ open, onClose, rule, accountsById, scopeAccountId, onCreateTicket }: CloudRulePanelProps) => {
  const [activeTab, setActiveTab] = useState(0);
  const [resources, setResources] = useState<any[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(10);
  const [loading, setLoading] = useState(false);
  const beginRequest = useLatestRequest();

  useEffect(() => {
    setActiveTab(0);
    setPage(0);
  }, [rule?.ruleName]);

  const catalog = useMemo(
    () => (rule?.ruleName ? recommendationApi.getRecommendationDetails('Security', rule.ruleName) || {} : {}),
    [rule?.ruleName]
  );

  const loadResources = useCallback(async () => {
    if (!rule?.ruleName) return;
    setLoading(true);
    const isLatest = beginRequest();
    try {
      const res = await recommendationApi.listCloudPostureResources({
        accountId: scopeAccountId || rule.accountIds,
        ruleName: rule.ruleName,
        limit: rowsPerPage,
        offset: page * rowsPerPage,
      });
      if (!isLatest()) return;
      setResources(
        (res.rows || []).map((row: any) => {
          const payload = typeof row.recommendation === 'string' ? safeJSONParse(row.recommendation) || {} : row.recommendation || {};
          return [
            { component: <Text value={row.resource_name || payload.repository_name || row.resource_id || '—'} showAutoEllipsis /> },
            { component: <Text value={accountsById?.[row.account_id] || row.account_id} showAutoEllipsis /> },
            { component: <Text value={normalizeSeverity(row.severity)} /> },
            { component: <Datetime value={row.updated_at} /> },
          ];
        })
      );
      setTotal(res.total || 0);
    } catch (error) {
      console.error('Failed to load cloud posture resources:', error);
      if (isLatest()) {
        setResources([]);
        setTotal(0);
      }
    } finally {
      if (isLatest()) setLoading(false);
    }
  }, [rule?.ruleName, rule?.accountIds, scopeAccountId, page, rowsPerPage, accountsById, beginRequest]);

  // Loaded with the panel: the resource list is the rule's evidence, and the
  // count in the header is meaningless without being able to see them.
  useEffect(() => {
    if (open && rule?.ruleName && activeTab === 1) loadResources();
  }, [open, rule?.ruleName, activeTab, loadResources]);

  const tabOptions = useMemo(
    () => [
      { value: 0, text: 'Check', id: 'cloud-rule-tab-check', icon: <RuleOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 1, text: 'Resources', id: 'cloud-rule-tab-resources', icon: <InventoryOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 2, text: 'Accounts', id: 'cloud-rule-tab-accounts', icon: <HubOutlinedIcon sx={{ fontSize: 16 }} /> },
    ],
    []
  );

  if (!rule) return null;

  const remediation: string[] = Array.isArray(catalog?.mitigations) ? catalog.mitigations : [];
  const guidance: string[] = Array.isArray(catalog?.recommendations) ? catalog.recommendations : [];

  return (
    <CustomDrawer open={open} onClose={onClose} bare nonModal variant='modern' width='680px' storageKey='nb.securityFindingDrawer.width'>
      <Box data-testid='cloud-rule-panel' sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        <Box sx={{ p: `${ds.space[4]} ${ds.space[5]} 0 ${ds.space[5]}`, display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2], flexWrap: 'wrap' }}>
              <Label size='sm' tone={severityTone(rule.severity)}>
                {normalizeSeverity(rule.severity)}
              </Label>
              {rule.provider && (
                <Label size='sm' tone='neutral'>
                  {rule.provider}
                </Label>
              )}
              {catalog?.serviceName && (
                <Label size='sm' tone='neutral'>
                  {catalog.serviceName}
                </Label>
              )}
            </Box>
            <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.3 }}>
              {cloudRuleTitle(rule.ruleName, catalog)}
            </Typography>
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[1] }}>
              {rule.count} {rule.count === 1 ? 'resource' : 'resources'} · {rule.accountIds.length}{' '}
              {rule.accountIds.length === 1 ? 'account' : 'accounts'}
            </Typography>
          </Box>
          <Button tone='ghost' composition='icon-only' size='sm' icon={<CloseIcon />} aria-label='Close' onClick={onClose} id='cloud-rule-close' />
        </Box>

        <Box sx={{ px: ds.space[5], pt: ds.space[3], borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(next: number) => setActiveTab(next)}
            behavior='filter'
            showSurface={false}
            variant='primary'
            ariaLabel='cloud posture rule tabs'
            options={{ tabOptions }}
          />
        </Box>

        <Box sx={{ flex: 1, overflow: 'auto', px: ds.space[5], py: ds.space[4] }}>
          {activeTab === 0 && (
            <Box data-testid='cloud-rule-check'>
              <Section title='What this checks'>
                {catalog?.description ? (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{catalog.description}</Typography>
                ) : (
                  <EmptyNote>
                    No catalog entry for <code>{rule.ruleName}</code>. The scanner’s own reason is shown against each resource.
                  </EmptyNote>
                )}
              </Section>

              {guidance.length > 0 && (
                <Section title='Why it matters'>
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{guidance[0]}</Typography>
                </Section>
              )}

              {remediation.length > 0 && (
                <Section title='Remediation'>
                  <Box sx={{ fontSize: ds.text.small }}>
                    <MarkDowns
                      data={remediation.join('\n\n')}
                      sx={{ padding: 0, '& p:last-child': { marginBottom: 0 } }}
                      allowExecutable={false}
                      onLinkClick={null}
                    />
                  </Box>
                </Section>
              )}

              <Section title='Details'>
                <FieldList>
                  <Field label='Rule'>
                    <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: ds.text.small, wordBreak: 'break-all' }}>
                      {rule.ruleName}
                    </Box>
                  </Field>
                  <Field label='Failing resources'>{rule.count}</Field>
                  {rule.provider && <Field label='Provider'>{rule.provider}</Field>}
                </FieldList>
              </Section>
            </Box>
          )}

          {activeTab === 1 && (
            <Box data-testid='cloud-rule-resources'>
              <CustomTable
                id={`cloud-rule-resources-${rule.ruleName}`}
                headers={RESOURCE_HEADERS}
                tableData={resources}
                loading={loading}
                rowsPerPage={rowsPerPage}
                totalRows={total}
                pageNumber={page + 1}
                onPageChange={(next: number, limit: number) => {
                  setPage(next - 1);
                  setRowsPerPage(limit);
                }}
                showUpdatedEmptyData={resources.length === 0}
              />
            </Box>
          )}

          {activeTab === 2 && (
            <Box data-testid='cloud-rule-accounts'>
              <SectionHeading>
                {rule.accountIds.length === 1 ? '1 account failing this check' : `${rule.accountIds.length} accounts failing this check`}
              </SectionHeading>
              <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                {rule.accountIds.map((id) => (
                  <Box
                    key={id}
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      gap: ds.space[3],
                      py: ds.space[2],
                      borderBottom: `1px solid ${ds.gray[200]}`,
                    }}
                  >
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{accountsById?.[id] || id}</Typography>
                    <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], whiteSpace: 'nowrap' }}>
                      {rule.countByAccount[id]} {rule.countByAccount[id] === 1 ? 'resource' : 'resources'}
                    </Typography>
                  </Box>
                ))}
              </Box>
            </Box>
          )}
        </Box>

        {onCreateTicket && (
          <PanelActions testId='cloud-rule-actions'>
            <Button id='cloud-rule-create-ticket' tone='primary' size='sm' onClick={() => onCreateTicket(rule)}>
              Create ticket
            </Button>
          </PanelActions>
        )}
      </Box>
    </CustomDrawer>
  );
};

export default CloudRulePanel;
