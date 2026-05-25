import { useState, useCallback, useMemo } from 'react';
import { Box, Typography } from '@mui/material';
import SortOutlinedIcon from '@mui/icons-material/SortOutlined';
import ViewListOutlinedIcon from '@mui/icons-material/ViewListOutlined';
import ViewStreamOutlinedIcon from '@mui/icons-material/ViewStreamOutlined';
import { ds } from 'src/utils/colors';
import { useTenantBranding } from '@hooks/useTenantBranding';
import SafeIcon from '@common/SafeIcon';
import CloudProviderIcon from '@common/CloudProviderIcon';
import CustomTable2 from '@components1/common/tables/CustomTable2';

import WidgetCard from '@components1/ds/WidgetCard';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import { Skeleton } from '@components1/ds/Skeleton';
import { EmptyState } from '@components1/ds/EmptyState';
import { StatusIndicator } from '@components1/ds/StatusIndicator';
import { Chip } from '@components1/ds/Chip';
import { Button } from '@components1/ds/Button';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import { toast as snackbar } from '@components1/ds/Toast';

import CategorySection from './InsightSection';
import InsightCard from './InsightCard';
import AccountClusterPane from './AccountClusterPane';
import SeverityBadge from '../SeverityBadge';
import {
  COST_SUBCATEGORIES,
  PERF_SUBCATEGORIES,
  SEC_CONFIG_SUBCATEGORIES,
  getTop3,
  getAccountSummaries,
  generateNubiBriefing,
  formatDollars,
  sortInsights,
  type InsightItem,
  type SortKey,
  type Provider,
  type MainCategory,
} from './insights';
import RecommendationDetailPanel from '../RecommendationDetailPanel';
import ResolveModal from '../ResolveModal';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { buildKubectlCommand, formatRuleName, getRecommendationBrief, getResourceDisplayName } from '../utils';
import { useSummaryData } from './useSummaryData';

// ─── Constants ─────────────────────────────────────────────────────────────

const SORT_OPTIONS: { key: SortKey; label: string }[] = [
  { key: 'savings', label: 'Savings' },
  { key: 'age', label: 'Age' },
  { key: 'confidence', label: 'Confidence' },
  { key: 'resource', label: 'Resource' },
];

type CategoryChipMeta = { value: MainCategory; label: string; tone: 'savings' | 'warning' | 'critical' };
const CATEGORY_CHIPS: CategoryChipMeta[] = [
  { value: 'cost', label: 'Cost', tone: 'savings' },
  { value: 'performance', label: 'Performance', tone: 'warning' },
  { value: 'security_config', label: 'Security & Config', tone: 'critical' },
];

const PROVIDER_CHIPS: { value: Provider; label: string }[] = [
  { value: 'aws', label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp', label: 'GCP' },
  { value: 'k8s', label: 'K8S' },
];

// ─── Conversational summaries ──────────────────────────────────────────────

const costConvoSummary = (items: InsightItem[]) => {
  const critCount = items.filter((i) => i.severity === 'critical').length;
  const topDollars = [...items].sort((a, b) => b.dollarImpact - a.dollarImpact)[0]?.dollarImpact || 0;
  if (critCount > 0) return `${critCount} critical anomalies — largest opportunity is ${formatDollars(topDollars)}/mo.`;
  if (topDollars > 0) return `Right-sizing and cleanup dominate. Savings plans alone could recover ${formatDollars(topDollars)}/mo.`;
  return `${items.length} cost optimization opportunities identified.`;
};

const perfConvoSummary = (items: InsightItem[]) => {
  const critCount = items.filter((i) => i.severity === 'critical').length;
  if (critCount > 0) return `${critCount} critical workloads at risk — fix before they page you.`;
  if (items.length > 0) return `${items.length} performance findings to review.`;
  return 'No active performance findings.';
};

const secConvoSummary = (items: InsightItem[]) => {
  const critCount = items.filter((i) => i.severity === 'critical').length;
  if (critCount >= 3) return `${critCount} critical vulnerabilities — public buckets and open ports are the immediate priority.`;
  if (critCount > 0) return `${critCount} critical finding${critCount > 1 ? 's' : ''} need immediate attention.`;
  return 'EOL versions and missing encryption are the long-tail risks worth planning around.';
};

// ─── Loading skeleton ─────────────────────────────────────────────────────

const LoadingSkeleton = () => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[2], px: ds.space[4], pt: ds.space[2] }}>
    {[1, 2, 3, 4].map((id) => (
      <Skeleton.Card key={id} width='100%' lines={2} />
    ))}
  </Box>
);

// ─── Main component ────────────────────────────────────────────────────────

const SummaryView = () => {
  const { nubiIconUrl, assistantName } = useTenantBranding();

  // ── UI state ──
  const [detailOpen, setDetailOpen] = useState(false);
  const [selectedResourceId, setSelectedResourceId] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>('savings');
  const [categoryFilter, setCategoryFilter] = useState<MainCategory | null>(null);
  const [providerFilter, setProviderFilter] = useState<Provider | null>(null);
  const [accountFilter, setAccountFilter] = useState<string | null>(null);
  const [viewMode, setViewMode] = useState<'cards' | 'list'>('cards');

  // ── Data (fetched via hook) ──
  const { accounts, insights, loading, lastUpdated, costByCurrency, accountCosts, costLoading } = useSummaryData();

  // ── Action modal state ──
  const [resolveModalRec, setResolveModalRec] = useState<any>(null);
  const [ticketRec, setTicketRec] = useState<any>(null);
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  // ── Handlers ──
  const handleOpenResource = useCallback((id: string) => {
    setSelectedResourceId(id);
    setDetailOpen(true);
  }, []);
  const handleCloseDetail = useCallback(() => {
    setDetailOpen(false);
    setSelectedResourceId(null);
  }, []);
  const handleCreateTicket = useCallback(
    (rec: any) => {
      const insight = insights.find((i) => i.id === rec.id);
      setTicketRec({ ...insight, _raw: rec });
    },
    [insights]
  );
  const handleResolve = useCallback((rec: any) => {
    setResolveModalRec(rec);
  }, []);
  const handleCopyCli = useCallback((rec: any) => {
    const cmd = buildKubectlCommand(rec);
    navigator.clipboard.writeText(cmd);
    snackbar.success('Command copied to clipboard');
  }, []);
  const handleAskNubi = useCallback(
    (rec: any) => {
      setDetailOpen(false);
      setSelectedResourceId(null);

      const accountInfo = accounts[rec.account_id];
      const prompt = buildNubiOptimizePrompt({
        ruleName: formatRuleName(rec.rule_name || ''),
        category: rec.category || '',
        severity: rec.severity || 'Info',
        resourceName: getResourceDisplayName(rec, ''),
        resourceType: rec.resource_type || rec.cloud_resourse?.type || '',
        namespace: rec.resource_k8s_namespace || rec.cloud_resourse?.meta?.namespace || '',
        accountName: accountInfo?.account_name || '',
        estimatedSavings: rec.estimated_savings || undefined,
        brief: getRecommendationBrief(rec) || undefined,
      });
      setNubiQuery(prompt);
      setNubiAccountId(rec.account_id || '');
      setNubiConversationId(`recom_${rec.id}`);
      setNubiSidebarVisible(true);
    },
    [accounts]
  );

  const handleAskNubiFromCard = useCallback(
    (item: InsightItem) => {
      if (item._raw) handleAskNubi(item._raw);
    },
    [handleAskNubi]
  );

  // ── Derived data ──
  const filteredByCatProvider = useMemo(() => {
    let items = insights;
    if (categoryFilter) items = items.filter((i) => i.category === categoryFilter);
    if (providerFilter) items = items.filter((i) => i.provider === providerFilter);
    return items;
  }, [insights, categoryFilter, providerFilter]);

  const filtered = useMemo(() => {
    if (!accountFilter) return filteredByCatProvider;
    return filteredByCatProvider.filter((i) => i.accountId === accountFilter);
  }, [filteredByCatProvider, accountFilter]);

  const savingsByCurrency = useMemo(() => {
    const byCurr: Record<string, number> = {};
    for (const item of filtered) {
      if (item.dollarImpact <= 0) continue;
      const symbol = accountCosts[item.accountId]?.currencySymbol || '$';
      byCurr[symbol] = (byCurr[symbol] || 0) + item.dollarImpact;
    }
    return Object.entries(byCurr)
      .filter(([, v]) => v > 0)
      .map(([symbol, amount]) => ({ symbol, amount }));
  }, [filtered, accountCosts]);

  const totalSavingsUsd = useMemo(
    () => savingsByCurrency.find((s) => s.symbol === '$')?.amount ?? savingsByCurrency[0]?.amount ?? 0,
    [savingsByCurrency]
  );
  const headlineSymbol = savingsByCurrency[0]?.symbol ?? '$';
  const savingsTone: 'high-savings' | 'medium-savings' | 'low-savings' | 'neutral' =
    totalSavingsUsd <= 0 ? 'neutral' : totalSavingsUsd > 10000 ? 'high-savings' : totalSavingsUsd > 1000 ? 'medium-savings' : 'low-savings';
  const multiCurrencyExtra = savingsByCurrency.slice(1);

  const top3 = useMemo(() => getTop3(filtered), [filtered]);
  const accountSummaries = useMemo(() => getAccountSummaries(filteredByCatProvider), [filteredByCatProvider]);
  const accountOptions = useMemo(
    () =>
      Object.entries(accounts)
        .map(([id, a]) => ({ value: id, label: a.account_name }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [accounts]
  );
  const selectedAccountName = accountFilter ? accounts[accountFilter]?.account_name || accountFilter : '';
  const nubiBriefing = useMemo(() => generateNubiBriefing(filtered), [filtered]);

  const costItems = useMemo(() => filtered.filter((i) => i.category === 'cost'), [filtered]);
  const perfItems = useMemo(() => filtered.filter((i) => i.category === 'performance'), [filtered]);
  const secItems = useMemo(() => filtered.filter((i) => i.category === 'security_config'), [filtered]);

  const costOneLiner = `${costItems.length} findings`;
  const perfOneLiner = `${perfItems.length} findings, ${perfItems.filter((i) => i.severity === 'critical').length} critical`;
  const secOneLiner = `${secItems.filter((i) => i.severity === 'critical').length} critical vulnerabilities, ${secItems.length} total`;

  const lastScannedMinutes = lastUpdated ? Math.max(1, Math.floor((Date.now() - lastUpdated.getTime()) / 60000)) : null;

  const selectedRecommendation = useMemo(() => {
    if (!selectedResourceId) return null;
    return insights.find((i) => i.id === selectedResourceId)?._raw || null;
  }, [selectedResourceId, insights]);

  const toggle = <T,>(current: T | null, value: T, setter: (v: T | null) => void) => {
    setter(current === value ? null : value);
  };
  const clearAll = () => {
    setCategoryFilter(null);
    setProviderFilter(null);
    setAccountFilter(null);
  };
  const hasActiveFilter = !!(categoryFilter || providerFilter || accountFilter);

  // Sort menu items for DropdownMenu
  const sortMenuItems = SORT_OPTIONS.map((opt) => ({
    label: opt.label,
    onSelect: () => setSortBy(opt.key),
    id: `sort-${opt.key}`,
  }));
  const sortLabel = SORT_OPTIONS.find((s) => s.key === sortBy)?.label || 'Sort';

  return (
    <Box sx={{ display: 'flex', pb: ds.space[7], pt: ds.space[4], minHeight: 'calc(100vh - 120px)', gap: ds.space[5] }}>
      {/* ════════ LEFT COLUMN ════════ */}
      <Box sx={{ flex: 1, minWidth: 0 }}>
        {/* Headline bar */}
        <WidgetCard
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: `${ds.space[3]} ${ds.space[4]}`,
            mt: 0,
            position: 'sticky',
            top: 0,
            zIndex: 2,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[3] }}>
            {loading ? (
              <Skeleton shape='text' size='heading' width={140} />
            ) : (
              <Stat
                label='Potential savings'
                size='hero'
                align='start'
                value={
                  <CostCallout
                    value={totalSavingsUsd}
                    size='display'
                    tone={savingsTone}
                    period='/ mo'
                    currency={headlineSymbol === '₹' ? 'INR' : 'USD'}
                  />
                }
                sub={
                  multiCurrencyExtra.length > 0
                    ? multiCurrencyExtra.map(({ symbol, amount }) => `+ ${symbol}${amount.toLocaleString()}/mo`).join(' ')
                    : undefined
                }
              />
            )}
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
            <StatusIndicator
              tone={loading ? 'pending' : filtered.length > 0 ? 'degraded' : 'healthy'}
              size='sm'
              label={loading ? 'Loading…' : `${filtered.length} findings${lastScannedMinutes != null ? ` · ${lastScannedMinutes}m ago` : ''}`}
            />
            <DropdownMenu
              align='end'
              size='sm'
              trigger={
                <Button tone='secondary' size='xs' icon={<SortOutlinedIcon />} iconPlacement='start' id='sort-toggle'>
                  {sortLabel}
                </Button>
              }
              items={sortMenuItems}
            />
            <ToggleGroup
              selection='single'
              size='sm'
              value={viewMode}
              onChange={(v) => setViewMode(v)}
              ariaLabel='View mode'
              options={[
                { value: 'cards', icon: <ViewStreamOutlinedIcon sx={{ fontSize: 16 }} />, ariaLabel: 'Cards view', tooltip: 'Card view' },
                { value: 'list', icon: <ViewListOutlinedIcon sx={{ fontSize: 16 }} />, ariaLabel: 'List view', tooltip: 'List view' },
              ]}
            />
          </Box>
        </WidgetCard>

        {/* Category + Provider filters */}
        <Box
          sx={{
            display: 'flex',
            gap: ds.space[1],
            px: ds.space[4],
            py: ds.space[3],
            flexWrap: 'wrap',
            alignItems: 'center',
          }}
        >
          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mr: ds.space[1] }}>Category</Typography>
          <Chip size='sm' tone='neutral' pressed={categoryFilter === null} onClick={() => setCategoryFilter(null)}>
            All
          </Chip>
          {CATEGORY_CHIPS.map(({ value, label, tone }) => (
            <Chip
              key={value}
              size='sm'
              tone={tone}
              pressed={categoryFilter === value}
              onClick={() => toggle(categoryFilter, value, setCategoryFilter)}
            >
              {label}
            </Chip>
          ))}

          <Box sx={{ width: '1px', height: '18px', backgroundColor: ds.gray[200], mx: ds.space[1] }} />

          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mr: ds.space[1] }}>Provider</Typography>
          <Chip size='sm' tone='neutral' pressed={providerFilter === null} onClick={() => setProviderFilter(null)}>
            All
          </Chip>
          {PROVIDER_CHIPS.map(({ value, label }) => (
            <Chip
              key={value}
              size='sm'
              tone='neutral'
              pressed={providerFilter === value}
              icon={<CloudProviderIcon cloud_provider={value} width='14px' height='14px' />}
              onClick={() => toggle(providerFilter, value, setProviderFilter)}
            >
              {label}
            </Chip>
          ))}

          <Box sx={{ width: '1px', height: '18px', backgroundColor: ds.gray[200], mx: ds.space[1] }} />

          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], mr: ds.space[1] }}>Account</Typography>
          <FilterDropdown
            id='account-filter-select'
            placeholder='All accounts'
            options={accountOptions}
            value={accountOptions.find((opt) => opt.value === accountFilter) ?? null}
            onSelect={(_: unknown, opt: { value: string } | null) => setAccountFilter(opt?.value || null)}
          />
          {accountFilter && (
            <Chip size='sm' tone='info' pressed onDismiss={() => setAccountFilter(null)} id='account-filter-chip'>
              {selectedAccountName}
            </Chip>
          )}

          {hasActiveFilter && (
            <Chip size='sm' tone='neutral' onDismiss={clearAll} onClick={clearAll}>
              Clear all
            </Chip>
          )}
        </Box>

        {/* Content area */}
        <Box sx={{ px: ds.space[4] }}>
          {loading && <LoadingSkeleton />}
          {!loading && filtered.length === 0 && (
            <EmptyState
              size='section'
              illustration={hasActiveFilter ? 'no-results' : 'clear-skies'}
              tone={hasActiveFilter ? 'neutral' : 'success'}
              title={hasActiveFilter ? 'No findings match the current filters' : 'No optimisation findings'}
              description={
                hasActiveFilter
                  ? 'Try clearing one of the filters to see more results.'
                  : 'Your infrastructure looks well-optimised. Check back later.'
              }
              action={hasActiveFilter ? { label: 'Clear filters', onClick: clearAll } : undefined}
            />
          )}
          {!loading && filtered.length > 0 && (
            <>
              {/* Nubi briefing + Top 3 */}
              <Box
                sx={{
                  mb: ds.space[3],
                  backgroundColor: ds.gray[100],
                  border: `1px solid ${ds.gray[200]}`,
                  borderRadius: ds.radius.md,
                  overflow: 'hidden',
                  p: ds.space[5],
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], px: ds.space[2], pb: ds.space[3] }}>
                  {nubiIconUrl ? (
                    <SafeIcon src={nubiIconUrl} alt={assistantName || 'Nubi'} width={24} height={24} />
                  ) : (
                    <Box
                      sx={{
                        width: '32px',
                        height: '32px',
                        borderRadius: ds.radius.pill,
                        backgroundColor: ds.blue[600],
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                      }}
                    >
                      <Typography sx={{ color: ds.background[100], fontSize: ds.text.caption, fontWeight: ds.weight.semibold }}>
                        {(assistantName || 'N')[0].toUpperCase()}
                      </Typography>
                    </Box>
                  )}
                  <Typography sx={{ fontSize: ds.text.body, fontWeight: ds.weight.semibold, lineHeight: 1.45, color: ds.gray[700] }}>
                    {nubiBriefing}
                  </Typography>
                </Box>
                <Box
                  sx={{
                    borderRadius: ds.radius.md,
                    overflow: 'hidden',
                    backgroundColor: ds.background[100],
                  }}
                >
                  {top3.map((item) => (
                    <InsightCard key={item.id} item={item} onClickResource={handleOpenResource} onAskNubi={handleAskNubiFromCard} />
                  ))}
                </Box>
                {filtered.length > top3.length && (
                  <Box
                    sx={{
                      display: 'flex',
                      alignItems: 'center',
                      justifyContent: 'space-between',
                      pt: ds.space[3],
                      px: ds.space[2],
                    }}
                  >
                    <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>
                      <Box component='span' sx={{ fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                        {filtered.length - top3.length}
                      </Box>{' '}
                      more issues this week
                    </Typography>
                    <Button tone='link' size='sm' href='/auto-pilot' id='top3-open-autopilot'>
                      Open Autopilot queue →
                    </Button>
                  </Box>
                )}
              </Box>

              {/* Category sections or list view */}
              {viewMode === 'cards' ? (
                <>
                  {(!categoryFilter || categoryFilter === 'cost') && costItems.length > 0 && (
                    <CategorySection
                      category='cost'
                      label='Cost'
                      oneLiner={costOneLiner}
                      conversationalSummary={costConvoSummary(costItems)}
                      subCategories={COST_SUBCATEGORIES}
                      items={costItems}
                      sortBy={sortBy}
                      onClickResource={handleOpenResource}
                      onAskNubi={handleAskNubiFromCard}
                    />
                  )}
                  {(!categoryFilter || categoryFilter === 'performance') && perfItems.length > 0 && (
                    <CategorySection
                      category='performance'
                      label='Performance'
                      oneLiner={perfOneLiner}
                      conversationalSummary={perfConvoSummary(perfItems)}
                      subCategories={PERF_SUBCATEGORIES}
                      items={perfItems}
                      sortBy={sortBy}
                      onClickResource={handleOpenResource}
                      onAskNubi={handleAskNubiFromCard}
                    />
                  )}
                  {(!categoryFilter || categoryFilter === 'security_config') && secItems.length > 0 && (
                    <CategorySection
                      category='security_config'
                      label='Security & Configuration'
                      oneLiner={secOneLiner}
                      conversationalSummary={secConvoSummary(secItems)}
                      subCategories={SEC_CONFIG_SUBCATEGORIES}
                      items={secItems}
                      sortBy={sortBy}
                      onClickResource={handleOpenResource}
                      onAskNubi={handleAskNubiFromCard}
                    />
                  )}
                </>
              ) : (
                <CustomTable2
                  id='summary-findings-table'
                  headers={[
                    { name: 'Severity', width: '8%' },
                    { name: 'Finding', width: '30%' },
                    { name: 'Resource', width: '18%' },
                    { name: 'Provider', width: '10%' },
                    { name: 'Impact', width: '14%' },
                    { name: 'Action', width: '14%' },
                  ]}
                  tableData={sortInsights(filtered, sortBy).map((item) => [
                    {
                      component: <SeverityBadge severity={(item.severity.charAt(0).toUpperCase() + item.severity.slice(1)) as any} size='small' />,
                    },
                    {
                      component: (
                        <Typography
                          sx={{
                            fontSize: ds.text.small,
                            fontWeight: ds.weight.medium,
                            whiteSpace: 'nowrap',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                            color: ds.gray[700],
                          }}
                        >
                          {item.summary}
                        </Typography>
                      ),
                    },
                    {
                      component: (
                        <Typography
                          sx={{
                            fontSize: ds.text.caption,
                            fontFamily: ds.font.mono,
                            color: ds.gray[600],
                            whiteSpace: 'nowrap',
                            overflow: 'hidden',
                            textOverflow: 'ellipsis',
                          }}
                        >
                          {item.resourceId}
                        </Typography>
                      ),
                    },
                    {
                      component: (
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
                          <CloudProviderIcon cloud_provider={item.provider} width='14px' height='14px' />
                          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[600] }}>{item.provider.toUpperCase()}</Typography>
                        </Box>
                      ),
                    },
                    {
                      component: (
                        <Box>
                          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.1 }}>
                            {item.impactValue}
                          </Typography>
                          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.1 }}>{item.impactLabel}</Typography>
                        </Box>
                      ),
                    },
                    {
                      component: (
                        <Button
                          tone='link'
                          size='xs'
                          onClick={(e) => {
                            e.stopPropagation();
                            handleOpenResource(item.id);
                          }}
                        >
                          {item.nextStep.label}
                        </Button>
                      ),
                    },
                  ])}
                  rowsPerPage={10}
                  onRowClick={(_row: unknown, index: number) => handleOpenResource(sortInsights(filtered, sortBy)[index].id)}
                  cellFontSize='12px'
                />
              )}

              <Box sx={{ mt: ds.space[5], pt: ds.space[4], borderTop: `1px solid ${ds.gray[200]}` }}>
                <Button
                  tone='link'
                  size='sm'
                  id='ask-nubi-footer'
                  onClick={() => {
                    const firstAccountId = Object.keys(accounts)[0] || '';
                    const critCount = filtered.filter((i) => i.severity === 'critical').length;
                    const prompt = `I have ${filtered.length} optimization findings across my infrastructure (${critCount} critical). Give me a prioritized action plan — what should I tackle first and why?`;
                    setNubiQuery(prompt);
                    setNubiAccountId(firstAccountId);
                    setNubiConversationId('optimize_summary_overview');
                    setNubiSidebarVisible(true);
                  }}
                >
                  Ask {assistantName || 'Nubi'} about any of this →
                </Button>
              </Box>
            </>
          )}
        </Box>
      </Box>

      {/* ════════ RIGHT COLUMN ════════ */}
      <Box sx={{ borderLeft: `1px solid ${ds.gray[200]}`, pl: ds.space[5], pr: ds.space[4], pt: ds.space[4] }}>
        <AccountClusterPane
          accounts={accountSummaries}
          costByCurrency={costByCurrency}
          accountCosts={accountCosts}
          costLoading={costLoading}
          selectedAccountId={accountFilter}
          onSelectAccount={setAccountFilter}
        />
      </Box>

      <RecommendationDetailPanel
        open={detailOpen}
        onClose={handleCloseDetail}
        recommendation={selectedRecommendation}
        accounts={Object.fromEntries(Object.entries(accounts).map(([id, a]) => [id, { name: a.account_name, cloud_provider: a.cloud_provider }]))}
        onCreateTicket={handleCreateTicket}
        onResolve={handleResolve}
        onCopyCli={handleCopyCli}
        onAskNubi={handleAskNubi}
      />

      <NubiChatSidebar
        isVisible={nubiSidebarVisible}
        onClose={() => setNubiSidebarVisible(false)}
        accountId={nubiAccountId}
        query={nubiQuery}
        context={{ type: 'general', data: { conversationId: nubiConversationId } }}
        apiMode='investigate'
        categorySource='Optimize'
        position='right'
        mode='overlay'
        width='720px'
      />

      {resolveModalRec && (
        <ResolveModal
          open={!!resolveModalRec}
          onClose={() => setResolveModalRec(null)}
          recommendation={resolveModalRec}
          clusterName={accounts[resolveModalRec.account_id]?.account_name}
          onSuccess={() => {
            setResolveModalRec(null);
            snackbar.success('Recommendation resolved');
          }}
        />
      )}

      <TicketCreatePopupForm
        open={!!ticketRec}
        handleClose={() => setTicketRec(null)}
        onClose={() => setTicketRec(null)}
        onSuccess={() => setTicketRec(null)}
        onFailure={(msg: string) => snackbar.error(msg || 'Failed to create ticket')}
        ticketData={{
          subject: ticketRec?.summary || '',
          description: `${ticketRec?.accountName || ''}\n\nSeverity: ${ticketRec?.severity || ''}\n${
            ticketRec?.dollarImpact > 0 ? `Potential savings: $${ticketRec.dollarImpact}/mo` : ''
          }`,
          accountId: ticketRec?._raw?.account_id,
        }}
        reference={{
          id: ticketRec?.id,
          type: 'recommendation',
        }}
      />
    </Box>
  );
};

export default SummaryView;
