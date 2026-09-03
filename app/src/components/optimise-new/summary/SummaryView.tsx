import { useState, useCallback, useMemo, type ReactNode, type ElementType } from 'react';
import { useRouter } from 'next/router';
import { Box, Typography } from '@mui/material';
import SortOutlinedIcon from '@mui/icons-material/SortOutlined';
import RefreshOutlinedIcon from '@mui/icons-material/RefreshOutlined';
import ChecklistOutlinedIcon from '@mui/icons-material/ChecklistOutlined';
import ListAltOutlinedIcon from '@mui/icons-material/ListAltOutlined';
import { ds } from 'src/utils/colors';
import { useTenantBranding } from '@hooks/useTenantBranding';
import SafeIcon from '@shared/icons/SafeIcon';
import CloudProviderIcon from '@shared/icons/CloudProviderIcon';
import CustomTable from '@shared/tables/CustomTable';

import { Card } from '@ui/Card';
import { Skeleton } from '@ui/Skeleton';
import { EmptyState } from '@ui/EmptyState';
import { Chip } from '@ui/Chip';
import { Button } from '@ui/Button';
import Tooltip from '@ui/Tooltip';
import FilterDropdown from '@ui/FilterDropdown';
import { DropdownMenu } from '@ui/DropdownMenu';
import { Label, type LabelTone } from '@ui/Label';
import { toast as snackbar } from '@ui/Toast';

import PriorityCard from './PriorityCard';
import { resourceAnchorName } from './ResourceLabel';
import AccountClusterPane from './AccountClusterPane';
import SummaryInsightWidget from './SummaryInsightWidget';
import {
  getTopRanked,
  getAccountSummaries,
  generateNubiBriefing,
  sortInsights,
  secondaryLine,
  subtotal,
  type BriefingEmphasis,
  type InsightItem,
  type SortKey,
  type Provider,
  type MainCategory,
  type Environment,
} from './insights';
import RecommendationDetailPanel from '../RecommendationDetailPanel';
import ResolveModal from '../ResolveModal';
import { useNubiGlobalChat } from '@context/NubiGlobalChatContext';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { buildKubectlCommand, formatRuleName, getRecommendationBrief, getResourceDisplayName, safeParseJSON } from '../utils';
import { useSummaryData } from './useSummaryData';

// ─── Constants ─────────────────────────────────────────────────────────────

const SORT_OPTIONS: { key: SortKey; label: string }[] = [
  { key: 'savings', label: 'Savings' },
  { key: 'age', label: 'Age' },
  { key: 'confidence', label: 'Confidence' },
  { key: 'resource', label: 'Resource' },
];

// How many findings the "Do this first" queue shows.
const PRIORITY_COUNT = 5;

// Filter chips for the "Top findings" table. Scoped to that section only —
// "Do this first" is the curated master list and stays unfiltered by design.
type CategoryChipMeta = { value: MainCategory; label: string; tone: 'savings' | 'warning' | 'critical' };
const CATEGORY_CHIPS: CategoryChipMeta[] = [
  { value: 'cost', label: 'Cost', tone: 'savings' },
  { value: 'performance', label: 'Performance', tone: 'warning' },
  { value: 'security_config', label: 'Security & Config', tone: 'critical' },
];

// cloud_accounts.account_env is binary, so this facet has exactly two values —
// it is not a prod/staging/dev tier list. Chips rather than a dropdown: two
// options read faster inline and match the Category/Provider facets below.
const ENV_CHIPS: { value: Environment; label: string }[] = [
  { value: 'prod', label: 'Production' },
  { value: 'non_prod', label: 'Non-production' },
];

const PROVIDER_CHIPS: { value: Provider; label: string }[] = [
  { value: 'aws', label: 'AWS' },
  { value: 'azure', label: 'Azure' },
  { value: 'gcp', label: 'GCP' },
  { value: 'k8s', label: 'K8S' },
];

// A single labelled filter facet: caption label tightly coupled to its controls.
const FilterFacet = ({ id, label, children }: { id?: string; label: string; children: ReactNode }) => (
  <Box id={id} sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1] }}>
    <Typography sx={{ fontSize: ds.text.caption, fontWeight: ds.weight.medium, color: ds.gray[500], whiteSpace: 'nowrap' }}>{label}</Typography>
    <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], flexWrap: 'wrap' }}>{children}</Box>
  </Box>
);

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

// Light rounded-square icon chip for section headings — matches the "Quick
// Links" pattern used on the home page (28px, radius-md), kept neutral gray
// rather than per-section color.
const SectionIcon = ({ icon: Icon }: { icon: ElementType }) => (
  <Box
    sx={{
      width: 28,
      height: 28,
      borderRadius: ds.radius.md,
      backgroundColor: ds.gray[100],
      color: ds.gray[600],
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      flexShrink: 0,
    }}
  >
    <Icon sx={{ fontSize: 16 }} />
  </Box>
);

// Severity → DS Label tone for the findings table (high folds into the critical tone).
const SEVERITY_TONE: Record<string, LabelTone> = {
  critical: 'critical',
  high: 'critical',
  medium: 'warning',
  low: 'info',
};
const severityLabel = (severity: string) => {
  const word = severity ? severity.charAt(0).toUpperCase() + severity.slice(1) : 'Info';
  return (
    <Label size='sm' tone={SEVERITY_TONE[severity?.toLowerCase()] ?? 'neutral'}>
      {word}
    </Label>
  );
};

// Category → DS Label tone for the findings table. Mirrors CATEGORY_CHIPS'
// labels; tones map to the closest Label equivalent ('savings' has no Label tone).
const CATEGORY_LABEL_META: Record<MainCategory, { label: string; tone: LabelTone }> = {
  cost: { label: 'Cost', tone: 'success' },
  performance: { label: 'Performance', tone: 'warning' },
  security_config: { label: 'Security & Config', tone: 'critical' },
};
const categoryLabel = (category: MainCategory) => {
  const meta = CATEGORY_LABEL_META[category];
  return (
    <Label size='sm' tone={meta?.tone ?? 'neutral'}>
      {meta?.label ?? category}
    </Label>
  );
};

// Nubi's avatar — tenant icon when branding provides one, initial-badge fallback.
const NubiAvatar = ({ iconUrl, name }: { iconUrl?: string; name?: string }) =>
  iconUrl ? (
    <SafeIcon src={iconUrl} alt={name || 'Nubi'} width={24} height={24} />
  ) : (
    <Box
      sx={{
        width: ds.space[6],
        height: ds.space[6],
        borderRadius: ds.radius.pill,
        backgroundColor: ds.blue[600],
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        flexShrink: 0,
      }}
    >
      <Typography sx={{ color: ds.background[100], fontSize: ds.text.caption, fontWeight: ds.weight.semibold }}>
        {(name || 'N')[0].toUpperCase()}
      </Typography>
    </Box>
  );

// Emphasis styling for the (still static) briefing sentence — bold + colour on
// the two numbers that matter, plain weight everywhere else.
const BRIEFING_EMPHASIS: Record<BriefingEmphasis, { color: string; fontWeight: string }> = {
  money: { color: ds.green[600], fontWeight: ds.weight.semibold },
  alert: { color: ds.red[600], fontWeight: ds.weight.semibold },
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
  const router = useRouter();

  // ── UI state ──
  const [detailOpen, setDetailOpen] = useState(false);
  const [selectedResourceId, setSelectedResourceId] = useState<string | null>(null);
  const [sortBy, setSortBy] = useState<SortKey>('savings');
  const [accountFilter, setAccountFilter] = useState<string | null>(null);
  const [envFilter, setEnvFilter] = useState<Environment | null>(null);
  // Scoped to the "Top findings" table only — "Do this first" above is the
  // curated master list and stays unfiltered by design.
  const [categoryFilter, setCategoryFilter] = useState<MainCategory | null>(null);
  const [providerFilter, setProviderFilter] = useState<Provider | null>(null);

  // ── Data (fetched via hook) ──
  const {
    accounts,
    insights,
    totalFindingsCount,
    loading,
    lastUpdated,
    totalSavings,
    categoryBreakdown,
    worstAccount,
    wipCount,
    scopedAccountIds,
    resolvedCount,
    safeToApply,
    costByCurrency,
    accountCosts,
    costLoading,
    savingsCurrency,
    savingsSymbol,
  } = useSummaryData(accountFilter, envFilter);

  // ── Action modal state ──
  const [resolveModalRec, setResolveModalRec] = useState<any>(null);
  const [ticketRec, setTicketRec] = useState<any>(null);
  // NuBi chat — opens the global drawer preloaded with the entry's context.
  const { openWithContext: openNubiChat } = useNubiGlobalChat();

  // Account ids every cross-tab link carries, so a drill-down opens on the same
  // slice this page is showing. A picked account is one id; an environment is
  // the set of account ids in it — neither destination tab has an environment
  // filter of its own, but both accept a repeated account param.
  const scopeAccountIds = useMemo(
    () => (Array.isArray(scopedAccountIds) ? scopedAccountIds : scopedAccountIds ? [scopedAccountIds] : []),
    [scopedAccountIds]
  );
  // Recommendations reads `account`, Resolutions reads `accountId`. Built inside
  // each handler rather than hoisted, so the callbacks' dependency lists stay
  // honest instead of needing an exhaustive-deps exemption.

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
  // Deep-links into the Resolutions tab, carrying the same account scope this
  // page is already narrowed to (if any) plus the InProgress status filter —
  // ResolutionsView reads both off the URL on mount.
  const handleViewWorkInProgress = useCallback(() => {
    const scope = scopeAccountIds.length > 0 ? { accountId: scopeAccountIds } : {};
    router.push({ pathname: '/optimise', query: { status: 'InProgress', ...scope }, hash: 'resolutions' });
  }, [router, scopeAccountIds]);
  // Same-page drill-downs: the "Top findings" table below already filters on the
  // exact vocabulary these tiles use (MainCategory, age), so narrowing it in
  // place is precise — where a cross-tab link would have to translate into
  // OptimizeNewPage's differently-shaped filters and lose fidelity.
  const scrollToTopFindings = () => document.getElementById('summary-top-findings')?.scrollIntoView({ behavior: 'smooth', block: 'start' });
  const handleFocusCategory = useCallback((category: MainCategory) => {
    setCategoryFilter(category);
    scrollToTopFindings();
  }, []);
  const handleFocusAging = useCallback(() => {
    setSortBy('age');
    scrollToTopFindings();
  }, []);
  const handleFocusAllFindings = useCallback(() => {
    setCategoryFilter(null);
    setProviderFilter(null);
    scrollToTopFindings();
  }, []);
  const handleSelectAccount = useCallback((id: string) => setAccountFilter(id), []);
  // Blast-radius safety is a real facet on the Recommendations tab (`?safety=`),
  // so this one is an accurate cross-tab link rather than a same-page narrow.
  const handleViewSafeToApply = useCallback(() => {
    const scope = scopeAccountIds.length > 0 ? { account: scopeAccountIds } : {};
    router.push({ pathname: '/optimise', query: { safety: 'safe', ...scope }, hash: 'cost' });
  }, [router, scopeAccountIds]);
  const handleViewResolved = useCallback(() => {
    const scope = scopeAccountIds.length > 0 ? { accountId: scopeAccountIds } : {};
    router.push({ pathname: '/optimise', query: { status: 'Success', ...scope }, hash: 'resolutions' });
  }, [router, scopeAccountIds]);
  // Same account-scope pattern as above, into the recommendations list
  // (OptimizeNewPage, the "Cost" tab) instead — its account query param is
  // `account`, not `accountId`, and severity/last-seen are its own filter
  // vocabulary. The fragment is `cost`: `recommendations` is a LEGACY alias that
  // pages/optimise/index.jsx rewrites exactly once per mount (didAliasFragment),
  // so pushing it from inside the page matches no tab and silently does nothing.
  const handleViewCritical = useCallback(() => {
    const scope = scopeAccountIds.length > 0 ? { account: scopeAccountIds } : {};
    router.push({ pathname: '/optimise', query: { severity: 'Critical', ...scope }, hash: 'cost' });
  }, [router, scopeAccountIds]);
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
        ruleName: formatRuleName(rec.rule_name || '', rec.category),
        category: rec.category || '',
        severity: rec.severity || 'Info',
        resourceName: getResourceDisplayName(rec, ''),
        resourceType: rec.resource_type || rec.cloud_resourse?.type || '',
        namespace: rec.resource_k8s_namespace || rec.cloud_resourse?.meta?.namespace || '',
        accountName: accountInfo?.account_name || '',
        estimatedSavings: rec.estimated_savings || undefined,
        brief: getRecommendationBrief(rec) || undefined,
        alarmConfig: safeParseJSON(rec.recommendation)?.alarm_config || undefined,
      });
      openNubiChat({
        accountId: rec.account_id || '',
        sessionId: `recom_${rec.id}`,
        query: prompt,
        categorySource: 'Optimize',
      });
    },
    [accounts, openNubiChat]
  );

  const handleAskNubiFromCard = useCallback(
    (item: InsightItem) => {
      if (item._raw) handleAskNubi(item._raw);
    },
    [handleAskNubi]
  );

  // ── Derived data ──
  // Account (picked from the right-hand rail or the "Top findings" toolbar,
  // both drive this same state) narrows everything on the page, including
  // "Do this first" and the headline metrics.
  const filtered = useMemo(() => {
    let items = insights;
    if (accountFilter) items = items.filter((i) => i.accountId === accountFilter);
    if (envFilter) items = items.filter((i) => i.env === envFilter);
    return items;
  }, [insights, accountFilter, envFilter]);

  // Headline savings = total across ALL in-scope recommendations (the same aggregate
  // the Recommendations tab uses), so the two tabs always agree. It is intentionally
  // decoupled from the curated list below, which shows only the top urgent +
  // highest-impact recs rather than the full set the total is summed over.
  const ranked = useMemo(() => getTopRanked(filtered, PRIORITY_COUNT), [filtered]);
  const accountSummaries = useMemo(() => getAccountSummaries(insights), [insights]);
  // The "of N total" framing is tenant-wide — only meaningful when nothing is
  // narrowing `filtered` to a single account, otherwise it'd compare a
  // one-account subset against an unfiltered tenant-wide count.
  const nubiBriefing = useMemo(
    () => generateNubiBriefing(filtered, totalSavings, savingsSymbol, accountFilter || envFilter ? undefined : totalFindingsCount),
    [filtered, totalSavings, savingsSymbol, accountFilter, envFilter, totalFindingsCount]
  );

  // "Top findings" table only — category/provider narrow just this list, not
  // "Do this first" or the headline metrics above.
  const allFindingsItems = useMemo(() => {
    let items = filtered;
    if (categoryFilter) items = items.filter((i) => i.category === categoryFilter);
    if (providerFilter) items = items.filter((i) => i.provider === providerFilter);
    return items;
  }, [filtered, categoryFilter, providerFilter]);

  const accountOptions = useMemo(
    () =>
      Object.entries(accounts)
        .map(([id, a]) => ({ value: id, label: a.account_name, group: a.cloud_provider || 'Other' }))
        .sort((a, b) => a.label.localeCompare(b.label)),
    [accounts]
  );

  const lastScannedMinutes = lastUpdated ? Math.max(1, Math.floor((Date.now() - lastUpdated.getTime()) / 60000)) : null;

  // Surfaced on hover over the refresh icon rather than as standing text.
  const syncedLabel = loading ? 'Syncing…' : lastScannedMinutes != null ? `Last synced ${lastScannedMinutes}m ago` : 'Refresh';

  // Total $ across the "Do this first" top-3 queue — feeds the insight widget's
  // "Top 3 impact" tile below (moved down from the old headline metrics row).
  const rankedDollars = useMemo(() => subtotal(ranked), [ranked]);

  const selectedRecommendation = useMemo(() => {
    if (!selectedResourceId) return null;
    return insights.find((i) => i.id === selectedResourceId)?._raw || null;
  }, [selectedResourceId, insights]);

  const clearAll = () => {
    setAccountFilter(null);
    setEnvFilter(null);
  };
  const hasActiveFilter = !!accountFilter || !!envFilter;

  const toggleFilterValue = <T,>(current: T | null, value: T, setter: (v: T | null) => void) => {
    setter(current === value ? null : value);
  };
  const clearTableFilters = () => {
    setCategoryFilter(null);
    setProviderFilter(null);
  };
  const hasTableFilter = !!(categoryFilter || providerFilter);

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
        {/* Page-wide filters. Unlike the Category/Provider facets in the "Top
            findings" toolbar, these narrow everything below them — the briefing,
            the insight widget, "Do this first", the table and the account rail.
            Account is the same state the rail's cards toggle, so selecting there
            reflects here and vice versa. */}
        <Box id='summary-global-filters' sx={{ display: 'flex', alignItems: 'center', gap: ds.space[5], flexWrap: 'wrap', mb: ds.space[3] }}>
          <FilterDropdown
            id='account-filter-select'
            label='Account'
            placeholder='All accounts'
            sx={{ minWidth: 260 }}
            grouped
            groupIcon={renderAccountGroupIcon}
            options={accountOptions}
            value={accountOptions.find((opt) => opt.value === accountFilter) ?? null}
            onSelect={(_: unknown, opt: { value: string } | null) => setAccountFilter(opt?.value || null)}
          />

          <FilterFacet id='summary-filter-env' label='Environment'>
            <Chip size='sm' pressed={envFilter === null} onClick={() => setEnvFilter(null)}>
              All
            </Chip>
            {ENV_CHIPS.map(({ value, label }) => (
              <Chip key={value} size='sm' pressed={envFilter === value} onClick={() => toggleFilterValue(envFilter, value, setEnvFilter)}>
                {label}
              </Chip>
            ))}
          </FilterFacet>

          {hasActiveFilter && (
            <Chip size='sm' onDismiss={clearAll} onClick={clearAll}>
              Clear filters
            </Chip>
          )}
        </Box>

        {/* Headline bar — KPI focus */}
        <Card
          id='summary-savings-card'
          variant='outlined'
          size='sm'
          sx={{
            display: 'flex',
            flexDirection: 'column',
            position: 'sticky',
            top: 0,
            zIndex: 1,
            // Squared off to butt directly against the insight widget below —
            // no gap, no doubled border line.
            borderBottomLeftRadius: 0,
            borderBottomRightRadius: 0,
          }}
        >
          <Box sx={{ display: 'flex', alignItems: 'flex-start', gap: ds.space[3] }}>
            <Box>
              <NubiAvatar iconUrl={nubiIconUrl} name={assistantName} />
            </Box>
            {loading ? (
              <Skeleton shape='text' size='text' width={460} />
            ) : (
              <Typography
                sx={{
                  flex: 1,
                  minWidth: 0,
                  fontSize: ds.text.body,
                  fontWeight: ds.weight.regular,
                  lineHeight: 2,
                  color: ds.gray[700],
                }}
              >
                {nubiBriefing.map((seg, i) => (
                  <Box key={`${i}-${seg.text}`} component='span' sx={seg.emphasis ? BRIEFING_EMPHASIS[seg.emphasis] : undefined}>
                    {seg.text}
                  </Box>
                ))}
              </Typography>
            )}
            {/* Visual affordance only for now — refetching the summary and
                regenerating the briefing is not wired up yet. The last-synced
                time lives in the tooltip rather than as standing text. */}
            <Tooltip title={syncedLabel} placement='left'>
              <Box component='span' sx={{ ml: 'auto', flexShrink: 0 }}>
                <Button
                  id='summary-refresh'
                  tone='ghost'
                  size='xs'
                  composition='icon-only'
                  icon={<RefreshOutlinedIcon />}
                  aria-label={`Refresh findings — ${syncedLabel}`}
                />
              </Box>
            </Tooltip>
          </Box>
        </Card>

        {/* Insight widget — butts directly against the Nubi briefing card
            above (both cards square off the shared edge). Also carries the
            old headline metrics row (Potential savings, Critical, Top 3
            impact) now folded into "What Nubi found" below; account count
            was dropped rather than relocated. Every figure is real: the
            savings, category and safety columns are full-set aggregates, the
            age-based callouts are derived from the curated `filtered` set and
            labelled as such. */}
        {!loading && filtered.length > 0 && (
          <SummaryInsightWidget
            items={filtered}
            totalSavings={totalSavings}
            totalFindingsCount={totalFindingsCount}
            savingsSymbol={savingsSymbol}
            savingsCurrency={savingsCurrency}
            rankedDollars={rankedDollars}
            categoryBreakdown={categoryBreakdown}
            worstAccount={worstAccount}
            wipCount={wipCount}
            resolvedCount={resolvedCount}
            safeToApply={safeToApply}
            onViewWorkInProgress={handleViewWorkInProgress}
            onViewResolved={handleViewResolved}
            onFocusCategory={handleFocusCategory}
            onFocusAging={handleFocusAging}
            onFocusAllFindings={handleFocusAllFindings}
            onSelectAccount={handleSelectAccount}
            onViewSafeToApply={handleViewSafeToApply}
            onViewCritical={handleViewCritical}
            onOpenResource={handleOpenResource}
          />
        )}

        {/* Content area */}
        <Box sx={{ px: ds.space[0], pt: ds.space[6] }}>
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
              // The success tone draws its own green card; the neutral (filtered)
              // tone is box-less by default, which read as inconsistent next to it.
              // Give the filtered state a matching neutral card so both empty
              // states share the same boxed treatment.
              sx={hasActiveFilter ? { backgroundColor: ds.gray[100], border: `1px solid ${ds.gray[200]}` } : undefined}
            />
          )}
          {!loading && filtered.length > 0 && (
            <>
              {/* "Do this first" — the ranked queue. Replaces the old filter
                  toolbar + "Top 3 by impact" box: the list is the master list,
                  so there is nothing to filter it by. */}
              <Box id='summary-do-this-first' sx={{ mb: ds.space[6] }}>
                <Box
                  sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2], mb: ds.space[4], flexWrap: 'wrap' }}
                >
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], justifyContent: 'center' }}>
                    <SectionIcon icon={ChecklistOutlinedIcon} />
                    <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, fontFamily: ds.font.display, color: ds.gray[700] }}>
                      Do this first
                    </Typography>
                  </Box>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>Ranked by severity, impact and age</Typography>
                </Box>

                <Card size='sm' variant='outlined' elevation='raised' sx={{ backgroundColor: ds.background[200] }}>
                  {ranked.map((item, i) => (
                    <PriorityCard
                      key={item.id}
                      item={item}
                      onOpen={handleOpenResource}
                      onAskNubi={handleAskNubiFromCard}
                      assistantName={assistantName}
                      showDivider={i < ranked.length - 1}
                    />
                  ))}
                </Card>
              </Box>

              <Box
                id='summary-top-findings'
                sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[2], mb: ds.space[3], flexWrap: 'wrap' }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
                  <SectionIcon icon={ListAltOutlinedIcon} />
                  <Typography sx={{ fontSize: ds.text.title, fontWeight: ds.weight.semibold, fontFamily: ds.font.display, color: ds.gray[700] }}>
                    Top findings
                  </Typography>
                </Box>
                {/* Same "tenant-wide, unfiltered by account" caveat as the Nubi
                    briefing's totalCount — see its comment above. */}
                {!accountFilter && !envFilter && totalFindingsCount > filtered.length && (
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>
                    Top {filtered.length.toLocaleString()} of {totalFindingsCount.toLocaleString()}, by urgency + impact
                  </Typography>
                )}
              </Box>

              {/* Filter toolbar — narrows only this table; "Do this first" above
                  is the curated master list and stays unfiltered by design. Sort
                  lives here too, directly above the list it reorders — parking it
                  in the page-top toolbar made it look inert (#32478). */}
              <Box
                sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: ds.space[4], flexWrap: 'wrap', mb: ds.space[3] }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[4], flexWrap: 'wrap' }}>
                  <FilterFacet id='summary-filter-category' label='Category'>
                    <Chip size='sm' pressed={categoryFilter === null} onClick={() => setCategoryFilter(null)}>
                      All
                    </Chip>
                    {CATEGORY_CHIPS.map(({ value, label, tone }) => (
                      <Chip
                        key={value}
                        size='sm'
                        tone={tone}
                        pressed={categoryFilter === value}
                        onClick={() => toggleFilterValue(categoryFilter, value, setCategoryFilter)}
                      >
                        {label}
                      </Chip>
                    ))}
                  </FilterFacet>

                  <FilterFacet id='summary-filter-provider' label='Provider'>
                    <Chip size='sm' pressed={providerFilter === null} onClick={() => setProviderFilter(null)}>
                      All
                    </Chip>
                    {PROVIDER_CHIPS.map(({ value, label }) => (
                      <Chip
                        key={value}
                        size='sm'
                        pressed={providerFilter === value}
                        icon={<CloudProviderIcon cloud_provider={value} width='14px' height='14px' />}
                        onClick={() => toggleFilterValue(providerFilter, value, setProviderFilter)}
                      >
                        {label}
                      </Chip>
                    ))}
                  </FilterFacet>

                  {hasTableFilter && (
                    <Chip size='sm' onDismiss={clearTableFilters} onClick={clearTableFilters}>
                      Clear filters
                    </Chip>
                  )}
                </Box>

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
              </Box>

              <Card variant='outlined' size='sm' sx={{ py: ds.space[2] }}>
                <CustomTable
                  id='summary-findings-table'
                  // Fixed layout: respect the declared column widths and let long resource
                  // ARNs ellipsize, instead of auto-layout stretching the row past its
                  // container and clipping the header bar on the right.
                  sx={{ '& > table': { tableLayout: 'fixed' } }}
                  headers={[
                    { name: 'Severity', width: '8%' },
                    { name: 'Finding', width: '28%' },
                    { name: 'Resource', width: '24%' },
                    { name: 'Category', width: '12%' },
                    { name: 'Impact', width: '14%' },
                    { name: 'Action', width: '14%' },
                  ]}
                  tableData={sortInsights(allFindingsItems, sortBy).map((item) => [
                    {
                      component: severityLabel(item.severity),
                    },
                    {
                      component: (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], minWidth: 0 }}>
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
                            {item.title || item.summary}
                          </Typography>
                          {secondaryLine(item) && (
                            <Typography
                              sx={{
                                fontSize: ds.text.caption,
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                                color: ds.gray[500],
                              }}
                            >
                              {secondaryLine(item)}
                            </Typography>
                          )}
                        </Box>
                      ),
                    },
                    {
                      component: (
                        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[1], minWidth: 0 }}>
                          <Tooltip title={item.resourceId} placement='top'>
                            <Typography
                              sx={{
                                fontSize: ds.text.caption,
                                fontFamily: ds.font.mono,
                                color: ds.gray[700],
                                whiteSpace: 'nowrap',
                                overflow: 'hidden',
                                textOverflow: 'ellipsis',
                              }}
                            >
                              {resourceAnchorName(item.resourceId)}
                            </Typography>
                          </Tooltip>
                          {item.accountName && (
                            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[1], minWidth: 0 }}>
                              <CloudProviderIcon cloud_provider={item.provider} width='12px' height='12px' />
                              <Typography
                                sx={{
                                  fontSize: ds.text.caption,
                                  color: ds.gray[500],
                                  whiteSpace: 'nowrap',
                                  overflow: 'hidden',
                                  textOverflow: 'ellipsis',
                                }}
                              >
                                {item.accountName}
                              </Typography>
                            </Box>
                          )}
                        </Box>
                      ),
                    },
                    {
                      component: categoryLabel(item.category),
                    },
                    {
                      component: item.impactValue ? (
                        <Box>
                          <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700], lineHeight: 1.1 }}>
                            {item.impactValue}
                          </Typography>
                          <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500], lineHeight: 1.1 }}>{item.impactLabel}</Typography>
                        </Box>
                      ) : (
                        <Box component='span' sx={{ color: ds.gray[500] }}>
                          —
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
                  onRowClick={(_row: unknown, index: number) => handleOpenResource(sortInsights(allFindingsItems, sortBy)[index].id)}
                />
              </Card>
            </>
          )}
        </Box>
      </Box>

      {/* ════════ RIGHT COLUMN ════════ */}
      <Box sx={{ pl: ds.space[1] }}>
        <AccountClusterPane
          accounts={accountSummaries}
          costByCurrency={costByCurrency}
          accountCosts={accountCosts}
          costLoading={costLoading}
          defaultCurrencySymbol={savingsSymbol}
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

      {resolveModalRec && (
        <ResolveModal
          open={!!resolveModalRec}
          onClose={() => setResolveModalRec(null)}
          recommendation={resolveModalRec}
          clusterName={accounts[resolveModalRec.account_id]?.account_name}
          // ResolveModal fires its own action-specific toast (deploy fix / auto-optimize rule); only close here.
          onSuccess={() => setResolveModalRec(null)}
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
            ticketRec?.dollarImpact > 0
              ? `Potential savings: ${
                  (ticketRec?._raw?.account_id ? accountCosts[ticketRec._raw.account_id]?.currencySymbol : undefined) || savingsSymbol
                }${ticketRec.dollarImpact}/mo`
              : ''
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
