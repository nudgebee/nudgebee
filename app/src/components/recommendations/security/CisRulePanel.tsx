import { useCallback, useEffect, useMemo, useState } from 'react';
import { Box, Typography } from '@mui/material';
import CloseIcon from '@mui/icons-material/Close';
import RuleOutlinedIcon from '@mui/icons-material/RuleOutlined';
import InventoryOutlinedIcon from '@mui/icons-material/Inventory2Outlined';
import HubOutlinedIcon from '@mui/icons-material/HubOutlined';
import CustomDrawer from '@shared/CustomDrawer';
import Tabs from '@shared/navigation/Tabs';
import Datetime from '@shared/format/Datetime';
import CustomTable from '@shared/tables/CustomTable';
import Text from '@shared/format/Text';
import { Label, type LabelTone } from '@ui/Label';
import { Button } from '@ui/Button';
import { Link } from '@ui/Link';
import { Skeleton } from '@ui/Skeleton';
import { ds } from '@utils/colors';
import recommendationApi from '@api1/recommendation';
import { useLatestRequest } from '@components/vm/common';
import { EmptyNote, Field, FieldList, PanelActions, Section, SectionHeading } from './panelPrimitives';
import { normalizeSeverity } from './securityFinding';

const SEVERITY_TONE: Record<string, LabelTone> = {
  Critical: 'critical',
  High: 'critical',
  Medium: 'warning',
  Low: 'info',
  Info: 'neutral',
};

const severityTone = (s: string): LabelTone => SEVERITY_TONE[normalizeSeverity(s)] ?? 'neutral';

const CIS_BENCHMARK_URL = 'https://www.cisecurity.org/benchmark/kubernetes';

const IMPACTED_HEADERS = [
  { name: 'Type', width: '22%' },
  { name: 'Resource', width: '33%' },
  { name: 'Message', width: '45%' },
];

/** One row of the CIS listing — a rule rolled up over the resources that fail it. */
export interface CisRule {
  account_id: string;
  rule_id: string;
  rule_name?: string;
  rule_description?: string;
  severity?: string;
  status?: string;
  count?: number;
  updated_at?: string;
  ticket?: any;
}

interface CisRulePanelProps {
  open: boolean;
  onClose: () => void;
  rule: CisRule | null;
  /** Display name for the rule's cluster, when the caller knows it. */
  accountName?: string;
  /** id → name for every account in view, used to label the other clusters. */
  accountsById?: Record<string, string>;
  /** Accounts the cross-cluster lookup may span — the current view's scope. */
  scopeAccountId?: string | string[];
  onCreateTicket?: (rule: CisRule) => void;
}

/**
 * Detail panel for one CIS benchmark rule on one cluster.
 *
 * A CIS row is a grouping — the scan writes a recommendation per (rule, class,
 * target) and the listing rolls them up by rule — so by the rule that governs
 * the CVE panel it would keep its accordion. It gets a panel anyway, on
 * different grounds: its members are not independently actionable, and the rule
 * is what you actually fix and raise a ticket against (the ticket reference is
 * keyed per rule). The rule, not the target, is the unit of work.
 *
 * Hence tabs that differ from the CVE panel's: the rule and its remediation,
 * the resources failing it here, and the same rule on other clusters.
 */
const CisRulePanel = ({ open, onClose, rule, accountName, accountsById, scopeAccountId, onCreateTicket }: CisRulePanelProps) => {
  const [activeTab, setActiveTab] = useState(0);

  const [resolution, setResolution] = useState('');
  const [references, setReferences] = useState<string[]>([]);
  const [impacted, setImpacted] = useState<any[]>([]);
  const [impactedTotal, setImpactedTotal] = useState(0);
  const [impactedPage, setImpactedPage] = useState(0);
  const [impactedRowsPerPage, setImpactedRowsPerPage] = useState(10);
  const [detailLoading, setDetailLoading] = useState(false);

  const [otherClusters, setOtherClusters] = useState<any[] | null>(null);
  const [otherLoading, setOtherLoading] = useState(false);

  const beginDetail = useLatestRequest();
  const beginOthers = useLatestRequest();

  useEffect(() => {
    setActiveTab(0);
    setImpactedPage(0);
    setOtherClusters(null);
  }, [rule?.rule_id, rule?.account_id]);

  /**
   * The rule's remediation text and references live on the misconfiguration of
   * the first failing target rather than on the rule itself, so the same fetch
   * serves both the Rule tab and the impacted-resources table.
   */
  const loadDetail = useCallback(async () => {
    if (!rule?.rule_id || !rule?.account_id) return;
    setDetailLoading(true);
    const isLatest = beginDetail();
    try {
      const res: any = await recommendationApi.getK8sRecommendation({
        accountId: rule.account_id,
        category: 'Security',
        ruleName: 'k8s-cis-1.23',
        recommendation: { Id: rule.rule_id },
        limit: impactedRowsPerPage,
        offset: impactedPage * impactedRowsPerPage,
      });
      if (!isLatest()) return;
      const rows = res?.data?.recommendation || [];
      const first = rows[0]?.recommendation?.Misconfigurations?.[0];
      setResolution(first?.Resolution || '');
      setReferences(Array.isArray(first?.References) ? first.References : []);
      setImpacted(
        rows.flatMap((item: any) => {
          const target = String(item?.recommendation?.Target || '').split('/');
          return (item?.recommendation?.Misconfigurations || []).map((misconfig: any) => [
            { component: <Text value={target[0]} showAutoEllipsis /> },
            { component: <Text value={target.slice(1).join('/')} showAutoEllipsis /> },
            { component: <Text value={misconfig?.Message} showAutoEllipsis /> },
          ]);
        })
      );
      setImpactedTotal(res?.data?.recommendation_aggregate?.aggregate?.count ?? rows.length);
    } catch (error) {
      console.error('Failed to load CIS rule detail:', error);
      if (isLatest()) {
        setImpacted([]);
        setImpactedTotal(0);
      }
    } finally {
      if (isLatest()) setDetailLoading(false);
    }
  }, [rule?.rule_id, rule?.account_id, impactedPage, impactedRowsPerPage, beginDetail]);

  useEffect(() => {
    if (open && rule?.rule_id) loadDetail();
  }, [open, rule?.rule_id, loadDetail]);

  const loadOtherClusters = useCallback(async () => {
    if (!rule?.rule_id) {
      setOtherClusters([]);
      return;
    }
    setOtherLoading(true);
    const isLatest = beginOthers();
    try {
      const rows = await recommendationApi.listCisRuleAcrossAccounts({
        accountId: scopeAccountId || rule.account_id,
        ruleId: rule.rule_id,
      });
      // The cluster being viewed is not "elsewhere".
      const others = (rows || []).filter((row: any) => row.account_id !== rule.account_id);
      if (isLatest()) setOtherClusters(others);
    } catch (error) {
      console.error('Failed to load CIS rule across clusters:', error);
      if (isLatest()) setOtherClusters([]);
    } finally {
      if (isLatest()) setOtherLoading(false);
    }
  }, [rule?.rule_id, rule?.account_id, scopeAccountId, beginOthers]);

  useEffect(() => {
    if (activeTab === 2 && otherClusters === null && !otherLoading) {
      loadOtherClusters();
    }
  }, [activeTab, otherClusters, otherLoading, loadOtherClusters]);

  const tabOptions = useMemo(
    () => [
      { value: 0, text: 'Rule', id: 'cis-rule-tab-rule', icon: <RuleOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 1, text: 'Impacted resources', id: 'cis-rule-tab-impacted', icon: <InventoryOutlinedIcon sx={{ fontSize: 16 }} /> },
      { value: 2, text: 'Other clusters', id: 'cis-rule-tab-others', icon: <HubOutlinedIcon sx={{ fontSize: 16 }} /> },
    ],
    []
  );

  if (!rule) return null;

  return (
    <CustomDrawer open={open} onClose={onClose} bare nonModal variant='modern' width='680px' storageKey='nb.securityFindingDrawer.width'>
      <Box data-testid='cis-rule-panel' sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
        {/* Header */}
        <Box sx={{ p: `${ds.space[4]} ${ds.space[5]} 0 ${ds.space[5]}`, display: 'flex', gap: ds.space[3], alignItems: 'flex-start' }}>
          <Box sx={{ flex: 1, minWidth: 0 }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2], flexWrap: 'wrap' }}>
              <Label size='sm' tone={severityTone(rule.severity || '')}>
                {normalizeSeverity(rule.severity)}
              </Label>
              {rule.status && (
                <Label size='sm' tone={rule.status === 'Open' ? 'info' : 'neutral'}>
                  {rule.status}
                </Label>
              )}
              <Label size='sm' tone='neutral'>
                CIS Benchmark
              </Label>
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'baseline', gap: ds.space[2], flexWrap: 'wrap' }}>
              <Link href={CIS_BENCHMARK_URL} openInNew>
                <Box component='span' sx={{ fontFamily: 'var(--ds-font-mono)', fontSize: ds.text.title, fontWeight: ds.weight.semibold }}>
                  {rule.rule_id}
                </Box>
              </Link>
            </Box>
            {rule.rule_name && (
              <Typography sx={{ fontSize: ds.text.body, color: ds.gray[700], mt: ds.space[1], lineHeight: 1.35 }}>{rule.rule_name}</Typography>
            )}
            <Typography sx={{ fontSize: ds.text.small, color: ds.gray[500], mt: ds.space[1] }}>
              {[accountName, rule.count !== undefined ? `${rule.count} failing ${rule.count === 1 ? 'resource' : 'resources'}` : null]
                .filter(Boolean)
                .join(' · ')}
            </Typography>
          </Box>
          <Button tone='ghost' composition='icon-only' size='sm' icon={<CloseIcon />} aria-label='Close' onClick={onClose} id='cis-rule-close' />
        </Box>

        {/* Tabs */}
        <Box sx={{ px: ds.space[5], pt: ds.space[3], borderBottom: `1px solid ${ds.gray[200]}` }}>
          <Tabs
            value={activeTab}
            onChange={(next: number) => setActiveTab(next)}
            behavior='filter'
            showSurface={false}
            variant='primary'
            ariaLabel='CIS rule detail tabs'
            options={{ tabOptions }}
          />
        </Box>

        <Box sx={{ flex: 1, overflow: 'auto', px: ds.space[5], py: ds.space[4] }}>
          {/* ── Rule: what it checks and how to satisfy it ── */}
          {activeTab === 0 && (
            <Box data-testid='cis-rule-detail'>
              <Section title='What this checks'>
                {rule.rule_description ? (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>{rule.rule_description}</Typography>
                ) : (
                  <EmptyNote>No description was recorded for this rule.</EmptyNote>
                )}
              </Section>

              <Section title='Remediation'>
                {detailLoading ? (
                  <Skeleton shape='rect' height={64} />
                ) : resolution ? (
                  <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700], whiteSpace: 'pre-wrap' }}>{resolution}</Typography>
                ) : (
                  <EmptyNote>The scanner did not supply remediation guidance for this rule.</EmptyNote>
                )}
              </Section>

              <Section title='Details'>
                <FieldList>
                  <Field label='Rule id'>{rule.rule_id}</Field>
                  {accountName && <Field label='Cluster'>{accountName}</Field>}
                  {rule.count !== undefined && <Field label='Failing resources'>{rule.count}</Field>}
                  {rule.updated_at && (
                    <Field label='Last scanned'>
                      <Datetime value={rule.updated_at} />
                    </Field>
                  )}
                  {rule.ticket?.ticket_id && (
                    <Field label='Ticket'>
                      <Link href={rule.ticket.url} openInNew>
                        {rule.ticket.ticket_id}
                      </Link>
                    </Field>
                  )}
                </FieldList>
              </Section>

              <Section title='References'>
                {detailLoading ? (
                  <Skeleton shape='rect' height={48} />
                ) : references.length > 0 ? (
                  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1] }}>
                    {references.map((ref, index) => (
                      <Link key={`${ref}-${index}`} href={ref} openInNew>
                        {ref}
                      </Link>
                    ))}
                  </Box>
                ) : (
                  <EmptyNote>No references were recorded for this rule.</EmptyNote>
                )}
              </Section>
            </Box>
          )}

          {/* ── Impacted resources: what fails the rule on this cluster ── */}
          {activeTab === 1 && (
            <Box data-testid='cis-rule-impacted'>
              <CustomTable
                id={`cis-rule-impacted-${rule.rule_id}`}
                headers={IMPACTED_HEADERS}
                tableData={impacted}
                loading={detailLoading}
                rowsPerPage={impactedRowsPerPage}
                totalRows={impactedTotal}
                pageNumber={impactedPage + 1}
                onPageChange={(page: number, limit: number) => {
                  setImpactedPage(page - 1);
                  setImpactedRowsPerPage(limit);
                }}
                showUpdatedEmptyData={impacted.length === 0}
              />
            </Box>
          )}

          {/* ── Other clusters: the same rule failing elsewhere in scope ── */}
          {activeTab === 2 && (
            <Box data-testid='cis-rule-others'>
              {otherLoading && (
                <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2] }}>
                  <Skeleton shape='rect' height={36} />
                  <Skeleton shape='rect' height={36} />
                </Box>
              )}
              {!otherLoading && otherClusters !== null && otherClusters.length === 0 && (
                <EmptyNote>No other cluster in view is failing this rule.</EmptyNote>
              )}
              {!otherLoading && otherClusters !== null && otherClusters.length > 0 && (
                <>
                  <SectionHeading>
                    {otherClusters.length === 1 ? '1 other cluster failing this rule' : `${otherClusters.length} other clusters failing this rule`}
                  </SectionHeading>
                  <Box sx={{ display: 'flex', flexDirection: 'column' }}>
                    {otherClusters.map((row: any) => (
                      <Box
                        key={row.account_id}
                        sx={{
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'space-between',
                          gap: ds.space[3],
                          py: ds.space[2],
                          borderBottom: `1px solid ${ds.gray[200]}`,
                        }}
                      >
                        <Typography sx={{ fontSize: ds.text.small, color: ds.gray[700] }}>
                          {accountsById?.[row.account_id] || row.account_id}
                        </Typography>
                        <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600], whiteSpace: 'nowrap' }}>
                          {row.count} {row.count === 1 ? 'resource' : 'resources'}
                        </Typography>
                      </Box>
                    ))}
                  </Box>
                </>
              )}
            </Box>
          )}
        </Box>

        {onCreateTicket && (
          <PanelActions testId='cis-rule-actions'>
            <Button
              id='cis-rule-create-ticket'
              tone='primary'
              size='sm'
              disabled={Boolean(rule.ticket?.ticket_id)}
              tooltip={rule.ticket?.ticket_id ? `Ticket ${rule.ticket.ticket_id} already exists` : undefined}
              onClick={() => onCreateTicket(rule)}
            >
              Create ticket
            </Button>
          </PanelActions>
        )}
      </Box>
    </CustomDrawer>
  );
};

export default CisRulePanel;
