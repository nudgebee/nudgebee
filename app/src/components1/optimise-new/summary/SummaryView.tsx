import { useState, useCallback, useMemo } from 'react';
import { Box, Typography, Chip, Select, MenuItem, FormControl } from '@mui/material';
import SortOutlinedIcon from '@mui/icons-material/SortOutlined';
import ViewListOutlinedIcon from '@mui/icons-material/ViewListOutlined';
import ViewStreamOutlinedIcon from '@mui/icons-material/ViewStreamOutlined';
import { colors } from 'src/utils/colors';
import { useTenantBranding } from '@hooks/useTenantBranding';
import SafeIcon from '@common/SafeIcon';
import CustomButton from '@components1/common/NewCustomButton';
import CloudProviderIcon from '@common/CloudProviderIcon';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import WidgetCard from '@common/WidgetCard';
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
import { snackbar } from '@components1/common/snackbarService';
import { useSummaryData } from './useSummaryData';

// ─── Constants ─────────────────────────────────────────────────────────────

const SORT_OPTIONS: { key: SortKey; label: string }[] = [
  { key: 'savings', label: 'Savings' },
  { key: 'age', label: 'Age' },
  { key: 'confidence', label: 'Confidence' },
  { key: 'resource', label: 'Resource' },
];

const CATEGORY_CHIPS: { value: MainCategory; label: string; accent: string }[] = [
  { value: 'cost', label: 'Cost', accent: '#16A34A' },
  { value: 'performance', label: 'Performance', accent: '#EA580C' },
  { value: 'security_config', label: 'Security & Config', accent: '#DC2626' },
];

const PROVIDER_CHIPS: { value: Provider; label: string; accent: string }[] = [
  { value: 'aws', label: 'AWS', accent: '#FF9900' },
  { value: 'azure', label: 'Azure', accent: '#0078D4' },
  { value: 'gcp', label: 'GCP', accent: '#4285F4' },
  { value: 'k8s', label: 'K8S', accent: '#326CE5' },
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

// ─── Shared chip style builder ─────────────────────────────────────────────

const chipSx = (active: boolean, accent: string) => ({
  fontSize: '11px',
  fontWeight: active ? 600 : 400,
  height: '26px',
  backgroundColor: active ? accent + '14' : colors.background.white,
  color: active ? colors.text.secondary : colors.text.tertiary,
  border: `1px solid ${active ? accent + '50' : colors.border.secondaryLightest}`,
  '& .MuiChip-label': { px: '6px' },
  '& .MuiChip-icon': { mr: '-2px' },
});

// ─── Severity display helper ──────────────────────────────────────────────

const toSeverityLabel = (sev: string): string => sev.charAt(0).toUpperCase() + sev.slice(1);

// ─── Loading skeleton ─────────────────────────────────────────────────────

const LoadingSkeleton = () => (
  <Box sx={{ display: 'flex', flexDirection: 'column', gap: '10px', px: '16px', pt: '10px' }}>
    {[1, 2, 3, 4].map((id) => (
      <Box key={id} sx={{ border: '1px solid #E5E7EB', borderRadius: '10px', p: '16px 18px', backgroundColor: '#FFFFFF' }}>
        <Box sx={{ height: '14px', backgroundColor: '#E5E7EB', borderRadius: '4px', width: '60%', mb: '8px', animation: 'pulse 1.5s infinite' }} />
        <Box sx={{ height: '10px', backgroundColor: '#F3F4F6', borderRadius: '4px', width: '40%', mb: '6px' }} />
        <Box sx={{ height: '10px', backgroundColor: '#F3F4F6', borderRadius: '4px', width: '80%' }} />
      </Box>
    ))}
    <style>{`@keyframes pulse { 0%, 100% { opacity: 1; } 50% { opacity: 0.5; } }`}</style>
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
      // Close detail panel so Nubi sidebar is visible
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

  // Handler for Ask Nubi from InsightCard (receives InsightItem, needs _raw)
  const handleAskNubiFromCard = useCallback(
    (item: InsightItem) => {
      if (item._raw) handleAskNubi(item._raw);
    },
    [handleAskNubi]
  );

  // ── Derived data ──
  // Filtered by category/provider only — drives the right-rail account summary
  // so per-account counts don't collapse to zero when an account is selected.
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

  // Group savings by currency to avoid mixing USD and INR
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

  const savingsHeadline = useMemo(() => {
    if (savingsByCurrency.length === 0) return '$0/mo';
    return savingsByCurrency.map(({ symbol, amount }) => `${formatDollars(amount).replace('$', symbol)}/mo`).join(' + ');
  }, [savingsByCurrency]);

  const top3 = useMemo(() => getTop3(filtered), [filtered]);
  const accountSummaries = useMemo(() => getAccountSummaries(filteredByCatProvider), [filteredByCatProvider]);
  const accountOptions = useMemo(
    () =>
      Object.entries(accounts)
        .map(([id, a]) => ({ id, name: a.account_name }))
        .sort((a, b) => a.name.localeCompare(b.name)),
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

  const cycleSort = () => {
    const idx = SORT_OPTIONS.findIndex((s) => s.key === sortBy);
    setSortBy(SORT_OPTIONS[(idx + 1) % SORT_OPTIONS.length].key);
  };
  const toggle = <T,>(current: T | null, value: T, setter: (v: T | null) => void) => {
    setter(current === value ? null : value);
  };
  const clearAll = () => {
    setCategoryFilter(null);
    setProviderFilter(null);
    setAccountFilter(null);
  };
  const hasActiveFilter = categoryFilter || providerFilter || accountFilter;

  return (
    <Box sx={{ display: 'flex', pb: '48px', pt: '16px', minHeight: 'calc(100vh - 120px)', gap: '20px' }}>
      {/* ════════ LEFT COLUMN ════════ */}
      <Box sx={{ flex: 1, minWidth: 0 }}>
        {/* Headline bar */}
        <WidgetCard
          sx={{
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'space-between',
            padding: '12px 16px',
            mt: 0,
            position: 'sticky',
            top: 0,
            zIndex: 2,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '8px' }}>
            <Typography sx={{ fontSize: '24px', fontWeight: 800, color: colors.text.secondary, lineHeight: 1 }}>
              {loading ? '—' : savingsHeadline}
            </Typography>
            <Typography sx={{ fontSize: '13px', color: colors.text.tertiary }}>potential savings</Typography>
          </Box>
          <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px' }}>
            <Typography sx={{ fontSize: '12px', color: colors.text.quaternary }}>
              {loading ? 'Loading...' : `${filtered.length} findings`}
              {lastScannedMinutes != null && ` · ${lastScannedMinutes}m ago`}
            </Typography>
            <CustomButton
              variant='secondary'
              size='xSmall'
              text={SORT_OPTIONS.find((s) => s.key === sortBy)?.label || 'Sort'}
              startIcon={<SortOutlinedIcon sx={{ fontSize: '14px !important' }} />}
              onClick={cycleSort}
              id='sort-toggle'
            />
            <Box sx={{ display: 'flex', border: `1px solid ${colors.border.secondaryLightest}`, borderRadius: '6px', overflow: 'hidden' }}>
              {(['cards', 'list'] as const).map((mode) => (
                <Box
                  key={mode}
                  onClick={() => setViewMode(mode)}
                  sx={{
                    p: '4px 6px',
                    cursor: 'pointer',
                    backgroundColor: viewMode === mode ? colors.background.suggestionCardHover : 'transparent',
                    display: 'flex',
                    alignItems: 'center',
                  }}
                >
                  {mode === 'cards' ? (
                    <ViewStreamOutlinedIcon sx={{ fontSize: '16px', color: viewMode === mode ? colors.text.secondary : colors.text.quaternary }} />
                  ) : (
                    <ViewListOutlinedIcon sx={{ fontSize: '16px', color: viewMode === mode ? colors.text.secondary : colors.text.quaternary }} />
                  )}
                </Box>
              ))}
            </Box>
          </Box>
        </WidgetCard>

        {/* Category + Provider filters */}
        <Box
          sx={{
            display: 'flex',
            gap: '5px',
            px: '16px',
            py: '12px',
            flexWrap: 'wrap',
            alignItems: 'center',
          }}
        >
          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary, mr: '4px' }}>Category</Typography>
          <Chip
            key='cat-all'
            label='All'
            size='small'
            clickable
            onClick={() => setCategoryFilter(null)}
            sx={chipSx(categoryFilter === null, '#737373')}
          />
          {CATEGORY_CHIPS.map(({ value, label, accent }) => (
            <Chip
              key={value}
              icon={<Box sx={{ width: '7px', height: '7px', borderRadius: '2px', backgroundColor: accent, ml: '6px' }} />}
              label={label}
              size='small'
              clickable
              onClick={() => toggle(categoryFilter, value, setCategoryFilter)}
              sx={chipSx(categoryFilter === value, accent)}
            />
          ))}

          <Box sx={{ width: '1px', height: '18px', backgroundColor: colors.border.secondaryLightest, mx: '3px' }} />

          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary, mr: '4px' }}>Provider</Typography>
          <Chip
            key='prov-all'
            label='All'
            size='small'
            clickable
            onClick={() => setProviderFilter(null)}
            sx={chipSx(providerFilter === null, '#737373')}
          />
          {PROVIDER_CHIPS.map(({ value, label, accent }) => (
            <Chip
              key={value}
              icon={
                <Box sx={{ ml: '4px', display: 'flex', alignItems: 'center' }}>
                  <CloudProviderIcon cloud_provider={value} width='14px' height='14px' />
                </Box>
              }
              label={label}
              size='small'
              clickable
              onClick={() => toggle(providerFilter, value, setProviderFilter)}
              sx={chipSx(providerFilter === value, accent)}
            />
          ))}

          <Box sx={{ width: '1px', height: '18px', backgroundColor: colors.border.secondaryLightest, mx: '3px' }} />

          <Typography sx={{ fontSize: '11px', color: colors.text.quaternary, mr: '4px' }}>Account</Typography>
          <FormControl size='small' sx={{ minWidth: '160px' }}>
            <Select
              value={accountFilter ?? ''}
              displayEmpty
              onChange={(e) => setAccountFilter(e.target.value || null)}
              inputProps={{ 'aria-label': 'Filter by account', 'data-testid': 'account-filter-select' }}
              sx={{
                fontSize: '11px',
                height: '26px',
                color: colors.text.tertiary,
                backgroundColor: colors.background.white,
                '& .MuiOutlinedInput-notchedOutline': { borderColor: colors.border.secondaryLightest },
                '& .MuiSelect-select': { py: '4px', px: '8px' },
              }}
            >
              <MenuItem value=''>
                <Typography sx={{ fontSize: '11px', color: colors.text.tertiary }}>All accounts</Typography>
              </MenuItem>
              {accountOptions.map((a) => (
                <MenuItem key={a.id} value={a.id}>
                  <Typography sx={{ fontSize: '11px' }}>{a.name}</Typography>
                </MenuItem>
              ))}
            </Select>
          </FormControl>
          {accountFilter && (
            <Chip
              label={selectedAccountName}
              size='small'
              onDelete={() => setAccountFilter(null)}
              sx={chipSx(true, '#2563EB')}
              data-testid='account-filter-chip'
            />
          )}

          {hasActiveFilter && (
            <Chip
              label='Clear all'
              size='small'
              clickable
              onDelete={clearAll}
              onClick={clearAll}
              sx={{ fontSize: '11px', height: '26px', color: colors.text.tertiary, '& .MuiChip-label': { px: '6px' } }}
            />
          )}
        </Box>

        {/* Content area */}
        <Box sx={{ px: '16px' }}>
          {loading && <LoadingSkeleton />}
          {!loading && filtered.length === 0 && (
            <Box sx={{ p: '40px', textAlign: 'center', backgroundColor: '#FFFFFF', borderRadius: '12px', border: '1px solid #E5E7EB' }}>
              <Typography sx={{ fontSize: '14px', color: colors.text.greyDark || '#6B7280' }}>
                {hasActiveFilter ? 'No findings match the current filters.' : 'Your infrastructure looks well-optimized. Check back later.'}
              </Typography>
            </Box>
          )}
          {!loading && filtered.length > 0 && (
            <>
              {/* Nubi briefing + Top 3 */}
              <Box
                sx={{
                  mb: '14px',
                  backgroundColor: colors.background.suggestionCardBG,
                  border: `1px solid ${colors.border.secondaryLightest}`,
                  borderRadius: '12px',
                  overflow: 'hidden',
                  p: '20px',
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '8px', p: '0px 8px 12px 8px' }}>
                  {nubiIconUrl ? (
                    <SafeIcon src={nubiIconUrl} alt={assistantName || 'Nubi'} width={24} height={24} />
                  ) : (
                    <Box
                      sx={{
                        width: '32px',
                        height: '32px',
                        borderRadius: '50%',
                        backgroundColor: colors.primary,
                        display: 'flex',
                        alignItems: 'center',
                        justifyContent: 'center',
                        flexShrink: 0,
                      }}
                    >
                      <Typography sx={{ color: colors.text.white, fontSize: '9px', fontWeight: 700 }}>
                        {(assistantName || 'N')[0].toUpperCase()}
                      </Typography>
                    </Box>
                  )}
                  <Typography className='nb-text-body-compact' sx={{ fontSize: '13px', fontWeight: 600, lineHeight: '19px' }}>
                    {nubiBriefing}
                  </Typography>
                </Box>
                <Box
                  sx={{
                    borderRadius: '12px',
                    overflow: 'hidden',
                    backgroundColor: colors.background.white,
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
                      p: '10px 8px 0px 8px',
                    }}
                  >
                    <Typography sx={{ fontSize: '12px', color: colors.text.tertiary }}>
                      <Box component='span' sx={{ fontWeight: 700, color: colors.text.secondary }}>
                        {filtered.length - top3.length}
                      </Box>{' '}
                      more issues this week
                    </Typography>
                    <Typography
                      component='a'
                      href='/auto-pilot'
                      data-testid='top3-open-autopilot'
                      sx={{
                        fontSize: '12px',
                        fontWeight: 500,
                        color: colors.text.primary,
                        textDecoration: 'none',
                        cursor: 'pointer',
                        '&:hover': { textDecoration: 'underline' },
                      }}
                    >
                      Open Autopilot queue &rarr;
                    </Typography>
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
                    { component: <SeverityBadge severity={toSeverityLabel(item.severity) as any} size='small' /> },
                    {
                      component: (
                        <Typography
                          className='nb-text-body-compact'
                          sx={{ fontSize: '12px', fontWeight: 500, whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}
                        >
                          {item.summary}
                        </Typography>
                      ),
                    },
                    {
                      component: (
                        <Typography
                          sx={{
                            fontSize: '10.5px',
                            fontFamily: 'monospace',
                            color: colors.text.tertiary,
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
                        <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                          <CloudProviderIcon cloud_provider={item.provider} width='14px' height='14px' />
                          <Typography sx={{ fontSize: '10.5px', color: colors.text.tertiary }}>{item.provider.toUpperCase()}</Typography>
                        </Box>
                      ),
                    },
                    {
                      component: (
                        <Box>
                          <Typography sx={{ fontSize: '11.5px', fontWeight: 700, color: colors.text.secondary, lineHeight: 1.1 }}>
                            {item.impactValue}
                          </Typography>
                          <Typography sx={{ fontSize: '9px', color: colors.text.quaternary, lineHeight: 1.1 }}>{item.impactLabel}</Typography>
                        </Box>
                      ),
                    },
                    {
                      component: (
                        <Typography
                          component='a'
                          onClick={(e: React.MouseEvent) => {
                            e.stopPropagation();
                            e.preventDefault();
                            handleOpenResource(item.id);
                          }}
                          sx={{
                            fontSize: '11px',
                            fontWeight: 500,
                            color: colors.text.primary,
                            whiteSpace: 'nowrap',
                            cursor: 'pointer',
                            textDecoration: 'none',
                            borderBottom: `1px dashed ${colors.text.primary}`,
                            pb: '1px',
                            '&:hover': { opacity: 0.75 },
                          }}
                        >
                          {item.nextStep.label}
                        </Typography>
                      ),
                    },
                  ])}
                  rowsPerPage={10}
                  onRowClick={(_row: unknown, index: number) => handleOpenResource(sortInsights(filtered, sortBy)[index].id)}
                  cellFontSize='12px'
                />
              )}

              <Box sx={{ mt: '24px', pt: '16px', borderTop: `1px solid ${colors.border.secondaryLightest}` }}>
                <Typography
                  component='a'
                  href='#'
                  onClick={(e: React.MouseEvent) => {
                    e.preventDefault();
                    const firstAccountId = Object.keys(accounts)[0] || '';
                    const critCount = filtered.filter((i) => i.severity === 'critical').length;
                    const prompt = `I have ${filtered.length} optimization findings across my infrastructure (${critCount} critical). Give me a prioritized action plan — what should I tackle first and why?`;
                    setNubiQuery(prompt);
                    setNubiAccountId(firstAccountId);
                    setNubiConversationId('optimize_summary_overview');
                    setNubiSidebarVisible(true);
                  }}
                  data-testid='ask-nubi-footer'
                  sx={{
                    fontSize: '13px',
                    color: colors.text.quaternary,
                    textDecoration: 'none',
                    cursor: 'pointer',
                    '&:hover': { color: colors.text.primary, textDecoration: 'underline' },
                  }}
                >
                  Ask {assistantName || 'Nubi'} about any of this &rarr;
                </Typography>
              </Box>
            </>
          )}
        </Box>
      </Box>

      {/* ════════ RIGHT COLUMN ════════ */}
      <Box sx={{ borderLeft: `1px solid ${colors.border.secondaryLightest}`, pl: '24px', pr: '16px', pt: '16px' }}>
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
