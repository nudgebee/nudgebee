import { Box, Typography } from '@mui/material';
import { useEffect, useState, useCallback, useRef, useMemo, memo } from 'react';
import NubiChatSidebar from '@shared/layout/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import { transformClusters } from '@shared/layout/UpdateDataContext';
import recommendationApi from '@api1/recommendation';
import { toast as snackbar } from '@ui/Toast';
import { SeverityIcon, type SeverityLevel as DsSeverityLevel } from '@ui/SeverityIcon';
import { Skeleton } from '@ui/Skeleton';
import CustomTable2 from '@shared/tables/CustomTable2';
import { DropdownMenu } from '@ui/DropdownMenu';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import CloseIcon from '@mui/icons-material/Close';
import KeyboardArrowDownIcon from '@mui/icons-material/KeyboardArrowDown';
import OptimizeIcon from 'src/assets/images/home/optimize-icon-button.svg';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import SafeIcon from '@shared/icons/SafeIcon';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import Currency from '@shared/format/Currency';
import Datetime from '@shared/format/Datetime';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import CopyButton from '@shared/buttons/CopyButton';
import Tooltip, { TooltipBody, OverflowTooltip } from '@ui/Tooltip';
import TicketCreatePopupForm from '@components/tickets/TicketCreatePopupForm';
import CustomTicketLink from '@shared/CustomTicketLink';
import { hasWriteAccess } from '@lib/auth';
import { formatMemory } from '@lib/formatter';
import ResolveModal from './ResolveModal';
import CliCommandModal from './CliCommandModal';
import WidgetCard from '@ui/WidgetCard';
import { ListingLayout } from '@ui/ListingLayout';
import { Stat } from '@ui/Stat';
import { CostCallout } from '@ui/CostCallout';
import { Chip } from '@ui/Chip';
import CustomSearch from '@shared/CustomSearch';
import NewToggleButtons from '@components/workflow/NewToggleButtons';
import { safetyBandTone, safetyBandLabel } from './safetyBand';
import FilterDropdown from '@ui/FilterDropdown';
import { Button } from '@ui/Button';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import RecommendationDetailPanel from './RecommendationDetailPanel';
import { type SeverityLevel } from './SeverityBadge';
import {
  NON_SECURITY_CATEGORIES,
  UPGRADE_PLANNER_RULES,
  DEFAULT_STATUS,
  CATEGORY_LABELS,
  formatRuleName,
  getRecommendationBrief,
  getResourceDisplayName,
  safeParseJSON,
  type SortField,
  type SortDirection,
} from './utils';

// Inlined from the deleted SeveritySummaryBar — only the row shape, the
// component itself is replaced by the DS-Chip strip rendered in this file.
interface SeveritySummaryData {
  severity: SeverityLevel;
  count: number;
  savings: number;
}

interface FilterState {
  severity: SeverityLevel[];
  account: string[];
  category: string[];
  search: string;
}

const SEVERITY_ORDER: SeverityLevel[] = ['Critical', 'High', 'Medium', 'Low', 'Info'];

// Severities selected on first load — the two that warrant immediate attention.
// Also the fallback the Top Issues band and table query use when no severity is chosen.
const DEFAULT_SEVERITY: SeverityLevel[] = ['Critical', 'High'];

const CATEGORY_FILTER_OPTIONS = [
  { label: 'Right Sizing', value: 'RightSizing' },
  { label: 'Infra Upgrade', value: 'InfraUpgrade' },
  { label: 'Config', value: 'Configuration' },
  { label: 'Spot Instance', value: 'K8sSpotRecommendation' },
];

// Leading-dot colour per category, so the category filter chips read as a
// categorical set alongside the severity chips (which carry their own dots).
const CATEGORY_DOT_COLOR: Record<string, string> = {
  RightSizing: 'var(--ds-blue-500)',
  InfraUpgrade: 'var(--ds-purple-500)',
  Configuration: 'var(--ds-amber-500)',
  K8sSpotRecommendation: 'var(--ds-green-500)',
};

// Track segmentation for the Cost / Reliability control.
type OptimizeTrack = 'cost' | 'reliability' | 'all';

// Which categories belong to each track. Mirrors the codebase's own cost-vs-
// reliability notion (see interpretation/buildInterpretation.ts: a recommendation
// counts as "cost" when it carries dollar savings): the spend-optimization families
// go under Cost, the policy/best-practice hygiene family under Reliability.
// Swap this mapping if product wants a different split — it drives the table
// filter, the category chips, and the toggle counts from one place.
const TRACK_CATEGORIES: Record<OptimizeTrack, string[]> = {
  cost: ['RightSizing', 'K8sSpotRecommendation', 'InfraUpgrade'],
  reliability: ['Configuration'],
  all: ['RightSizing', 'K8sSpotRecommendation', 'InfraUpgrade', 'Configuration'],
};
// Sort presets for the "Sort by" control. Each maps to a real backend sort
// column so the dropdown and the column-header sort share one source of truth
// (sortField + sortDirection). Options with no backend column (e.g. a pure
// "safest first" — there is no numeric safety rank, safety_band is a text band)
// are intentionally omitted rather than shipped non-functional.
type OptimizeSortValue = 'severe' | 'savings' | 'recent';
const SORT_PRESETS: Record<OptimizeSortValue, { field: SortField; direction: SortDirection; label: string }> = {
  severe: { field: 'severity', direction: 'asc', label: 'Most severe' },
  savings: { field: 'estimated_savings', direction: 'desc', label: 'Highest savings' },
  recent: { field: 'updated_at', direction: 'desc', label: 'Last seen' },
};
const SORT_ORDER: OptimizeSortValue[] = ['severe', 'savings', 'recent'];
// Friendly label for the dropdown trigger when the active sort came from a column
// header that isn't one of the presets above (Category).
const SORT_FIELD_LABEL: Partial<Record<SortField, string>> = {
  estimated_savings: 'Highest savings',
  severity: 'Most severe',
  updated_at: 'Last seen',
  category: 'Category',
};

// Pins the leading-dot to a categorical hue per severity / category row — the DS
// dot otherwise follows `tone`, and these chips need a specific shade per row.
// (Font, idle-label colour and selected-border now live in the Chip primitive.)
const dotSx = (color: string) => ({ '& [data-dot]': { backgroundColor: color, borderColor: color } });

// Shimmer placeholders for a chip filter group (severity / category / top issues)
// while its counts are loading.
const chipSkeletons = (count: number, width: number) =>
  Array.from({ length: count }, (_, i) => <Skeleton key={i} shape='rect' width={width} height={20} />);

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const WIDGET_CATEGORIES = ['RightSizing', 'InfraUpgrade', 'Configuration', 'K8sSpotRecommendation'] as const;

function sumCategoryRows(rows: any[]): { count: number; savings: number } {
  let count = 0;
  let savings = 0;
  for (const r of rows) {
    count += r.count || 0;
    savings += r.sum_estimated_savings || 0;
  }
  return { count, savings };
}
const WIDGET_CATEGORY_LABELS: Record<string, string> = {
  RightSizing: 'Right Sizing',
  InfraUpgrade: 'Infra Upgrade',
  Configuration: 'Config',
  K8sSpotRecommendation: 'Spot Instance',
};

const WIDGET_CATEGORY_TOOLTIPS: Record<string, string> = {
  RightSizing: 'CPU and memory right-sizing recommendations for workloads based on actual usage patterns',
  InfraUpgrade: 'Infrastructure upgrade recommendations including node groups, instance types, and cluster versions',
  Configuration: 'Configuration best practices and policy compliance recommendations',
  K8sSpotRecommendation: 'Workloads eligible for Spot/preemptible instances to reduce compute costs',
};

/** Parse a URL query param that may be a string or string[] into a string[] */
const parseQueryArray = (param: string | string[] | undefined): string[] => {
  if (!param) {
    return [];
  }
  return Array.isArray(param) ? param : [param];
};

// Map between CustomTable2 header labels and backend sort fields.
const HEADER_TO_SORT_FIELD: Record<string, SortField> = {
  Severity: 'severity',
  Category: 'category',
  Savings: 'estimated_savings',
  'Last Seen': 'updated_at',
};
// Reverse map, used to highlight the actively-sorted column. Partial because the
// SortField type allows fields with no matching column; those leave no column
// highlighted, while the sortable columns still show their idle icon.
const SORT_FIELD_TO_HEADER: Partial<Record<SortField, string>> = {
  severity: 'Severity',
  category: 'Category',
  estimated_savings: 'Savings',
  updated_at: 'Last Seen',
};

// The exact Safety chip used in the table cell, reused inside the header tooltip
// so the legend reads with the same visual vocabulary as the rows.
const safetyChip = (band: string) => (
  <Chip variant='status' size='2xs' tone={safetyBandTone(band)} dot>
    {safetyBandLabel(band)}
  </Chip>
);

// Header tooltip for the Safety column — a lead sentence plus a chip legend for each
// blast-radius band (computed by the knowledge-graph impact pipeline; see safetyBand.ts).
const SAFETY_HEADER_TOOLTIP = (
  <TooltipBody
    lead='Blast radius from the dependency graph — how many resources depend on this one.'
    rows={[
      { term: safetyChip('safe'), description: 'Low blast radius — safe to act now.' },
      { term: safetyChip('review'), description: 'Check dependents before acting.' },
      { term: safetyChip('risky'), description: 'High blast radius — proceed with caution.' },
    ]}
  />
);

// A savings value rendered exactly like the table cell — reused as the example in
// the header tooltip so each scenario reads with the same green/red visual.
const savingsExample = (value: number) => (
  <Currency
    value={Math.abs(value)}
    precison={0}
    withTooltip={false}
    prefix={value < 0 ? '-$' : '$'}
    suffix='/mo'
    sx={{ fontSize: ds.text.body, fontWeight: ds.weight.medium, color: value > 0 ? ds.green[600] : ds.red[600] }}
    sxSuffix={{ fontSize: ds.text.caption, fontWeight: ds.weight.regular, color: ds.gray[500] }}
  />
);

// Header tooltip for the Savings column — a lead sentence plus a worked example of
// each scenario (money saved vs. a red −$ cost increase).
const SAVINGS_HEADER_TOOLTIP = (
  <TooltipBody
    lead='Estimated monthly cost change if you apply this recommendation.'
    rows={[
      { term: savingsExample(120), description: 'Money saved — the recommendation lowers spend.' },
      { term: savingsExample(-40), description: 'A red −$ value means it would increase spend (e.g. right-sizing up to meet demand).' },
    ]}
  />
);

// Column headers for the recommendations table. Sortable columns carry
// `sortEnabled` so CustomTable2 renders the sort affordance. Safety sits 5th
// (after Category) so severity → resource → recommendation read first. Safety is
// not sortable — safety_band is a text band, so an alphabetical sort isn't a
// meaningful "safest first" ordering.
const TABLE_HEADERS = [
  { name: 'Severity', width: '6%', sortEnabled: true },
  { name: 'Resource', width: '24%' },
  { name: 'Recommendation', width: '24%' },
  { name: 'Category', width: '9%', sortEnabled: true },
  { name: 'Safety', width: '9%', info: SAFETY_HEADER_TOOLTIP, infoPlacement: 'top' as const },
  { name: 'Savings', width: '10%', sortEnabled: true, align: 'left' as const, info: SAVINGS_HEADER_TOOLTIP, infoPlacement: 'top' as const },
  { name: 'Last Seen', width: '12%', sortEnabled: true, align: 'left' as const },
  { name: '', width: '12%', align: 'right' as const },
];

// Categorical hue per recommendation category — maps to DS Chip `hue` values.
const CATEGORY_HUE: Record<string, 'blue' | 'violet' | 'amber' | 'green' | 'pink' | 'teal' | 'slate'> = {
  RightSizing: 'blue',
  InfraUpgrade: 'violet',
  Configuration: 'amber',
  K8sSpotRecommendation: 'green',
  Cost: 'green',
  K8sVersionUpgrade: 'pink',
};

// Severity → DS Chip tone for the Severity filter row. These are `filter` chips
// (they carry `pressed`), so the tone only drives the leading dot — the chip's
// bg/text come from the filter-selection palette. We pick the nearest semantic
// tone here and pin the exact dot shade below so the colour is unambiguously
// correct regardless of which tone we land on.
const SEVERITY_TONE: Record<SeverityLevel, 'critical' | 'warning' | 'info' | 'neutral'> = {
  Critical: 'critical',
  High: 'critical',
  Medium: 'warning',
  Low: 'info',
  Info: 'neutral',
};

const SEVERITY_DOT_COLOR: Record<SeverityLevel, string> = {
  Critical: 'var(--ds-red-700)',
  High: 'var(--ds-red-500)',
  Medium: 'var(--ds-amber-500)',
  Low: 'var(--ds-blue-500)',
  Info: 'var(--ds-gray-400)',
};

const getTicketSourceFromCloudProvider = (cloudProvider: string | undefined): string => {
  switch ((cloudProvider || '').toLowerCase()) {
    case 'aws':
      return 'aws';
    case 'gcp':
      return 'gcp';
    case 'azure':
      return 'azure';
    default:
      return 'kubernetes';
  }
};

interface RowActionsProps {
  rowId: string;
  rec: any;
  category: string;
  ticketId: string;
  assistantName: string | undefined;
  onAskNubi: (rec: any) => void;
  onResolve: (rec: any) => void;
  onCreateTicket: (rec: any) => void;
  onCopyCli: (rec: any) => void;
}

const RowActions = memo(({ rowId, rec, category, ticketId, assistantName, onAskNubi, onResolve, onCreateTicket, onCopyCli }: RowActionsProps) => {
  const showResolve = category === 'RightSizing' && rec.rule_name === 'pod_right_sizing' && hasWriteAccess(rec.account_id);
  const showCopyCli = category === 'RightSizing' && rec.rule_name === 'pod_right_sizing';

  const menuItems: Array<{ label: string; icon: React.ReactNode; onSelect: () => void; disabled?: boolean; id?: string }> = [
    {
      id: `action-ticket-${rowId}`,
      label: ticketId ? `Ticket: ${ticketId}` : 'Create ticket',
      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
      onSelect: () => onCreateTicket(rec),
      disabled: !!ticketId,
    },
    ...(showCopyCli
      ? [
          {
            id: `action-copy-cli-${rowId}`,
            label: 'Copy CLI command',
            icon: <ContentCopyOutlinedIcon sx={{ fontSize: 16 }} />,
            onSelect: () => onCopyCli(rec),
          },
        ]
      : []),
  ];

  return (
    <Box onClick={(e) => e.stopPropagation()} sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], justifyContent: 'flex-end' }}>
      {showResolve && (
        <Tooltip title='Optimize' placement='top'>
          <span>
            <Button
              tone='ghost'
              size='xs'
              composition='icon-only'
              icon={<SafeIcon src={OptimizeIcon} alt='' width={16} height={16} />}
              aria-label='Optimize'
              id={`action-resolve-${rowId}`}
              onClick={() => onResolve(rec)}
            />
          </span>
        </Tooltip>
      )}
      <Tooltip title={`Ask ${assistantName || 'Nubi'}`} placement='top'>
        <span>
          <Button
            tone='ghost'
            size='xs'
            composition='icon-only'
            icon={<SafeIcon src={getNubiIconUrl()} alt='' width={16} height={16} />}
            aria-label={`Ask ${assistantName || 'Nubi'}`}
            id={`action-ask-nubi-${rowId}`}
            onClick={() => onAskNubi(rec)}
          />
        </span>
      </Tooltip>
      <DropdownMenu
        align='end'
        size='sm'
        items={menuItems}
        trigger={
          <Button tone='ghost' size='xs' composition='icon-only' icon={<MoreVertIcon />} aria-label='More actions' id={`action-menu-${rowId}`} />
        }
      />
    </Box>
  );
});
RowActions.displayName = 'RowActions';

const OptimizeNewPage = () => {
  const router = useRouter();
  const routerRef = useRef(router);
  routerRef.current = router;

  const { setAllCluster } = useData();

  // Accounts state: id → { name, cloud_provider }
  const [accounts, setAccounts] = useState<Record<string, { name: string; cloud_provider: string }>>({});

  // Summary state — split into two independent fetches so the summary cards
  // (Total Recommendations, per-category counts) stay a static overview,
  // unaffected by the category/search filters applied to the table below.
  // Cards: all categories, no search — scoped only by account.
  const [cardRows, setCardRows] = useState<any[]>([]);
  const [cardsLoading, setCardsLoading] = useState(true);
  // Severity chips + Top Issues: scoped by account, category and search —
  // matches the table below exactly.
  const [summaryData, setSummaryData] = useState<SeveritySummaryData[]>([]);
  const [perSeverityRules, setPerSeverityRules] = useState<Record<string, { rule_name: string; count: number }[]>>({});
  const [severityLoading, setSeverityLoading] = useState(true);

  // Table state
  const [recommendations, setRecommendations] = useState<any[]>([]);
  const [tableTotal, setTableTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(10);
  const [tableLoading, setTableLoading] = useState(true);

  // Sort state — single source of truth shared by the "Sort by" dropdown and the
  // column-header sort. Defaults to "Most severe" (severity asc), matching the
  // default dropdown selection.
  const [sortField, setSortField] = useState<SortField>('severity');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');

  // Cost / Reliability track toggle. Switching the track scopes the table,
  // category chips and summary counts to that track's categories.
  const [track, setTrack] = useState<OptimizeTrack>('all');

  // Filters state — initialised from URL query params
  const [filters, setFilters] = useState<FilterState>(() => {
    // Default to Critical + High on first load; an explicit ?severity= in the URL wins.
    const severityFromUrl = parseQueryArray(router.query.severity) as SeverityLevel[];
    return {
      severity: severityFromUrl.length > 0 ? severityFromUrl : DEFAULT_SEVERITY,
      account: parseQueryArray(router.query.account),
      category: parseQueryArray(router.query.category),
      search: (router.query.search as string) || '',
    };
  });

  // Local search input state — typed value, not yet applied. Mirrors ManualInvestigated pattern.
  const [searchInput, setSearchInput] = useState((router.query.search as string) || '');

  // Top issues bar state
  const [topIssuesActive, setTopIssuesActive] = useState(false);
  const [activeRuleName, setActiveRuleName] = useState<string | null>(null);

  // Detail panel state
  const [selectedRec, setSelectedRec] = useState<any>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailInitialTab, setDetailInitialTab] = useState(0);

  // Close the detail panel when the user navigates away (e.g. via the left nav).
  // The drawer is non-modal with no backdrop, so nothing else dismisses it on a
  // route change and it would otherwise linger over the next page.
  useEffect(() => {
    const closeDetail = () => setDetailOpen(false);
    router.events.on('routeChangeStart', closeDetail);
    return () => router.events.off('routeChangeStart', closeDetail);
  }, [router.events]);

  // Direct action modal state
  const [ticketModalRec, setTicketModalRec] = useState<any>(null);
  const [resolveModalRec, setResolveModalRec] = useState<any>(null);
  const [cliModalRec, setCliModalRec] = useState<any>(null);

  // NuBi sidebar state
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  // Sync filters to URL
  const updateUrl = useCallback((newFilters: FilterState) => {
    const r = routerRef.current;
    const query: Record<string, string | string[]> = {};
    if (r.query.accountId) {
      query.accountId = r.query.accountId as string;
    }
    if (newFilters.severity.length > 0) {
      query.severity = newFilters.severity;
    }
    if (newFilters.account.length > 0) {
      query.account = newFilters.account;
    }
    if (newFilters.category.length > 0) {
      query.category = newFilters.category;
    }

    if (newFilters.search) {
      query.search = newFilters.search;
    }

    const currentHash = r.asPath.split('#')[1];
    r.replace({ pathname: r.pathname, query, ...(currentHash ? { hash: `#${currentHash}` } : {}) }, undefined, { shallow: true });
  }, []);

  const handleFiltersChange = useCallback(
    (newFilters: FilterState) => {
      setFilters(newFilters);
      setPage(0);
      setTopIssuesActive(false);
      setActiveRuleName(null);
      updateUrl(newFilters);
    },
    [updateUrl]
  );

  // Reset every filter (severity, category, account, search) and the top-issues
  // drill-down back to empty. handleFiltersChange already clears top issues + page.
  const handleClearAll = useCallback(() => {
    setSearchInput('');
    handleFiltersChange({ severity: [], account: [], category: [], search: '' });
  }, [handleFiltersChange]);

  // Fetch accounts
  useEffect(() => {
    apiHome
      .getCloudAccounts()
      .then((res: any) => {
        setAccounts(Object.fromEntries(res.map((v: any) => [v.id, { name: v.account_name, cloud_provider: v.cloud_provider || '' }])));
        const clusters = transformClusters(res);
        setAllCluster(clusters);
      })
      .catch(() => {
        /* accounts are non-blocking; widgets degrade gracefully */
      });
  }, []);

  // Build filter query for the API
  const buildFilterQuery = useCallback(
    (extraFilters?: Partial<FilterState>) => {
      const merged = { ...filters, ...extraFilters };
      const query: any = {};

      // No explicit category chips → scope to the active track's categories.
      query.category = merged.category.length > 0 ? merged.category : TRACK_CATEGORIES[track];
      query.status = DEFAULT_STATUS;
      query.excludeRuleName = UPGRADE_PLANNER_RULES;

      if (merged.account.length > 0) {
        query.accountId = merged.account;
      }

      if (merged.severity.length > 0) {
        query.severity = merged.severity;
      }

      if (merged.search) {
        query.accountObjectId = merged.search;
      }

      return query;
    },
    [filters, track]
  );

  // Process raw severity results into summary and per-severity rule data
  const processSummaryResults = useCallback((rows: any[]) => {
    // Group rows by severity
    const rowsBySeverity: Record<string, any[]> = {};
    for (const sev of SEVERITY_ORDER) {
      rowsBySeverity[sev] = [];
    }
    for (const r of rows) {
      if (r.severity && rowsBySeverity[r.severity]) {
        rowsBySeverity[r.severity].push(r);
      }
    }

    const summaryItems: SeveritySummaryData[] = SEVERITY_ORDER.map((sev) => {
      const sevRows = rowsBySeverity[sev];
      const count = sevRows.reduce((sum: number, r: any) => sum + (r.count || 0), 0);
      const savings = sevRows.reduce((sum: number, r: any) => sum + (r.sum_estimated_savings || 0), 0);
      return { severity: sev, count, savings };
    });

    // Build per-severity rule data for top issues
    const sevRules: Record<string, { rule_name: string; count: number }[]> = {};
    for (const sev of SEVERITY_ORDER) {
      const ruleCountMap: Record<string, number> = {};
      for (const r of rowsBySeverity[sev]) {
        if (r.rule_name) {
          ruleCountMap[r.rule_name] = (ruleCountMap[r.rule_name] || 0) + (r.count || 0);
        }
      }
      sevRules[sev] = Object.entries(ruleCountMap)
        .map(([rule_name, count]) => ({ rule_name, count }))
        .sort((a, b) => b.count - a.count);
    }

    return { summaryItems, sevRules };
  }, []);

  // Fetch summary cards — re-fetches only when the account filter changes.
  // Always requests ALL categories and no search term: the summary cards
  // (Total Recommendations, per-category counts) must stay a static
  // overview, unaffected by the category/search filters applied below.
  useEffect(() => {
    let cancelled = false;
    setCardsLoading(true);

    const accountId = filters.account.length > 0 ? filters.account : '';

    const fetchCardRows = async () => {
      try {
        const allRows = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          accountId,
          category: NON_SECURITY_CATEGORIES as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          status: DEFAULT_STATUS,
          severity: [...SEVERITY_ORDER],
        });
        if (cancelled) {
          return;
        }
        setCardRows(Array.isArray(allRows) ? allRows : []);
      } catch {
        if (!cancelled) {
          snackbar.error('Failed to load recommendation summary. Try refreshing.');
        }
      } finally {
        if (!cancelled) {
          setCardsLoading(false);
        }
      }
    };

    fetchCardRows();
    return () => {
      cancelled = true;
    };
  }, [filters.account]);

  // Per-category card counts: always derived from the full (unfiltered) rows above.
  const categoryCounts = useMemo(() => {
    const catData: Record<string, { count: number; savings: number }> = {};
    for (const cat of WIDGET_CATEGORIES) {
      const catRows = cardRows.filter((r: any) => r.category === cat);
      catData[cat] = sumCategoryRows(catRows);
    }
    return catData;
  }, [cardRows]);

  // Fetch severity chips + Top Issues — scoped by account, category and
  // search, matching the table below exactly. This is a separate fetch (not
  // a client-side slice of cardRows) because the search term is applied
  // server-side and cardRows above never carries it.
  useEffect(() => {
    let cancelled = false;
    setSeverityLoading(true);

    const accountId = filters.account.length > 0 ? filters.account : '';
    const activeCategories = filters.category.length > 0 ? filters.category : TRACK_CATEGORIES[track];

    const fetchSeverityRows = async () => {
      try {
        const rows = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          accountId,
          category: activeCategories as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          accountObjectId: filters.search || undefined,
          status: DEFAULT_STATUS,
          severity: [...SEVERITY_ORDER],
        });
        if (cancelled) {
          return;
        }
        const { summaryItems, sevRules } = processSummaryResults(Array.isArray(rows) ? rows : []);
        setSummaryData(summaryItems);
        setPerSeverityRules(sevRules);
      } catch {
        if (!cancelled) {
          snackbar.error('Failed to load recommendation summary. Try refreshing.');
        }
      } finally {
        if (!cancelled) {
          setSeverityLoading(false);
        }
      }
    };

    fetchSeverityRows();
    return () => {
      cancelled = true;
    };
  }, [filters.account, filters.category, filters.search, track, processSummaryResults]);

  // Fetch table data — used both by useEffect (with cancellation) and manual refresh calls
  const buildTableQuery = useCallback(() => {
    const apiQuery = buildFilterQuery();
    // When top issues filter is active and no explicit severity selected, default to Critical+High
    if (topIssuesActive && !apiQuery.severity) {
      apiQuery.severity = DEFAULT_SEVERITY;
    }
    return {
      ...apiQuery,
      ...(topIssuesActive && activeRuleName ? { ruleName: activeRuleName } : {}),
      orderBy: sortField,
      orderAsc: sortDirection === 'asc',
      limit: rowsPerPage,
      offset: page * rowsPerPage,
      fetchTicket: true,
    };
  }, [buildFilterQuery, topIssuesActive, activeRuleName, sortField, sortDirection, rowsPerPage, page]);

  const applyTableResult = useCallback((result: any) => {
    const recs = result?.data?.recommendation || [];
    const count = result?.data?.recommendation_aggregate?.aggregate?.count || 0;
    setRecommendations(recs);
    setTableTotal(count);
  }, []);

  // Auto-fetch with cancellation guard on dependency change
  useEffect(() => {
    let cancelled = false;
    setTableLoading(true);

    const fetchRecs = async () => {
      try {
        const result: any = await recommendationApi.getK8sRecommendation(buildTableQuery());
        if (cancelled) return;
        applyTableResult(result);
      } catch {
        if (!cancelled) snackbar.error('Failed to load recommendations. Try refreshing.');
      } finally {
        if (!cancelled) setTableLoading(false);
      }
    };

    fetchRecs();
    return () => {
      cancelled = true;
    };
  }, [buildTableQuery, applyTableResult]);

  // Manual re-fetch (e.g. after ticket creation) — no cancellation needed since it's user-initiated
  const fetchTableData = useCallback(async () => {
    setTableLoading(true);
    try {
      const result: any = await recommendationApi.getK8sRecommendation(buildTableQuery());
      applyTableResult(result);
    } catch {
      snackbar.error('Failed to load recommendations. Try refreshing.');
    } finally {
      setTableLoading(false);
    }
  }, [buildTableQuery, applyTableResult]);

  // O(1) lookup for keeping the detail panel in sync after table refreshes.
  const recById = useMemo(() => new Map(recommendations.map((r: any) => [r.id, r])), [recommendations]);

  // Keep the detail drawer in sync when table data refreshes
  useEffect(() => {
    setSelectedRec((prev: any) => {
      if (!prev || !detailOpen) return prev;
      return recById.get(prev.id) ?? prev;
    });
  }, [recById, detailOpen]);

  // CSV export — replaces the legacy DownloadButton DOM-scraping path.
  // Built directly from the in-memory recommendation rows so it stays decoupled
  // from the table's render markup.
  const handleDownloadCsv = useCallback(() => {
    const escape = (v: unknown) => {
      const str = v == null ? '' : String(v);
      return `"${str.replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    };
    const headers = ['Severity', 'Resource', 'Recommendation', 'Category', 'Safety', 'Environment', 'Savings ($/mo)', 'Last Seen'];
    const rows = recommendations.map((rec: any) => {
      const accountInfo = accounts[rec.account_id];
      return [
        rec.severity || '',
        getResourceDisplayName(rec, ''),
        formatRuleName(rec.rule_name || ''),
        rec.category || '',
        safetyBandLabel(rec.safety_band),
        accountInfo?.name || '',
        rec.estimated_savings || 0,
        rec.updated_at || rec.created_at || '',
      ];
    });
    const csv = [headers, ...rows].map((row) => row.map(escape).join(',')).join('\r\n');
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'recommendations.csv';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  }, [recommendations, accounts]);

  // ─── Computed: Top issues based on severity selection ───

  const topIssueData = useMemo(() => {
    const targetSeverities: string[] = filters.severity.length > 0 ? filters.severity : DEFAULT_SEVERITY;

    const merged: Record<string, number> = {};
    let total = 0;
    for (const sev of targetSeverities) {
      for (const rule of perSeverityRules[sev] || []) {
        merged[rule.rule_name] = (merged[rule.rule_name] || 0) + rule.count;
        total += rule.count;
      }
    }

    const items = Object.entries(merged)
      .map(([rule_name, count]) => ({ rule_name, count }))
      .sort((a, b) => b.count - a.count)
      .slice(0, 5);

    const severityLabel = (filters.severity.length > 0 ? filters.severity : DEFAULT_SEVERITY).join(' + ');

    return { items, total, severityLabel };
  }, [perSeverityRules, filters.severity]);

  // ─── Computed: Summary widget totals ───

  // Computed: Account filter options from loaded accounts.
  // Grouped by cloud provider (AWS / K8S / AZURE / GCP …) so the dropdown
  // surfaces a collapsible header per provider with the account name underneath.
  const accountFilterOptions = useMemo(
    () =>
      Object.entries(accounts).map(([id, info]) => ({
        label: info.name || id,
        value: id,
        group: (info.cloud_provider || '').toUpperCase() || 'Other',
      })),
    [accounts]
  );

  // Category chips shown under the toolbar are scoped to the active track's
  // categories, so the chips never disagree with the Cost/Reliability toggle.
  const trackCategoryOptions = useMemo(() => CATEGORY_FILTER_OPTIONS.filter((opt) => TRACK_CATEGORIES[track].includes(opt.value)), [track]);

  // Card totals: derived from cardRows (unfiltered by category/search) so the
  // Total Recommendations card stays a static overview, same as the per-category cards above.
  const cardSummaryData = useMemo(() => processSummaryResults(cardRows).summaryItems, [cardRows, processSummaryResults]);
  const totalCount = useMemo(() => cardSummaryData.reduce((sum, s) => sum + s.count, 0), [cardSummaryData]);
  const totalSavings = useMemo(() => cardSummaryData.reduce((sum, s) => sum + s.savings, 0), [cardSummaryData]);
  const criticalCount = useMemo(() => cardSummaryData.find((s) => s.severity === 'Critical')?.count || 0, [cardSummaryData]);
  const highCount = useMemo(() => cardSummaryData.find((s) => s.severity === 'High')?.count || 0, [cardSummaryData]);

  // Per-track totals for the toggle + outcome widgets — summed from the same
  // unfiltered category counts as the summary cards, so they stay a stable
  // overview independent of the severity/search filters below.
  const trackCounts = useMemo(() => {
    const sumTrack = (t: OptimizeTrack) => TRACK_CATEGORIES[t].reduce((sum, cat) => sum + (categoryCounts?.[cat]?.count || 0), 0);
    return { cost: sumTrack('cost'), reliability: sumTrack('reliability') };
  }, [categoryCounts]);

  const hasActiveFilter = useMemo(
    () => filters.severity.length > 0 || filters.account.length > 0 || filters.category.length > 0 || filters.search.length > 0 || topIssuesActive,
    [filters, topIssuesActive]
  );

  // ─── Computed: Table rows for DS Table ───

  const tableRows = useMemo(
    () =>
      recommendations.map((rec: any) => {
        const accountInfo = accounts[rec.account_id];
        return {
          id: rec.id,
          rec,
          severity: (rec.severity || 'Info') as SeverityLevel,
          resourceName: getResourceDisplayName(rec),
          resourceType: rec.cloud_resourse?.type || '',
          cloudService: rec.resource_cloud_service || '',
          ruleName: formatRuleName(rec.rule_name || ''),
          brief: getRecommendationBrief(rec) || '',
          category: rec.category || '',
          accountName: accountInfo?.name || '',
          accountCloudProvider: accountInfo?.cloud_provider || '',
          savings: rec.estimated_savings || 0,
          safetyBand: rec.safety_band || '',
          updatedAt: rec.updated_at || rec.created_at || '',
          ticketId: rec.ticket?.ticket_id || '',
          ticketUrl: rec.ticket?.url || '',
        };
      }),
    [recommendations, accounts]
  );

  // ─── Handlers ───

  // Track toggle — switching tracks clears any category-chip narrowing so the
  // new track shows its full category set rather than carrying a stale category.
  const handleTrackChange = useCallback(
    (next: OptimizeTrack) => {
      if (next === track) {
        return;
      }
      setTrack(next);
      handleFiltersChange({ ...filters, category: [] });
    },
    [track, filters, handleFiltersChange]
  );

  // Multi-select: each click toggles that severity in/out of the filter set,
  // mirroring the category chips. (Multi-select is group state — the Chip itself
  // just reflects `pressed`, so nothing is needed on the Chip primitive.)
  const handleSeverityClick = useCallback(
    (severity: SeverityLevel) => {
      const next = filters.severity.includes(severity) ? filters.severity.filter((s) => s !== severity) : [...filters.severity, severity];
      handleFiltersChange({ ...filters, severity: next });
    },
    [filters, handleFiltersChange]
  );

  const handleRuleClick = useCallback((ruleName: string | null) => {
    // Clicking a specific rule activates the top issues filter and sets that rule
    setTopIssuesActive(true);
    setActiveRuleName((prev) => (prev === ruleName ? null : ruleName));
    setPage(0);
  }, []);

  const handleToggleTopIssues = useCallback(() => {
    if (topIssuesActive && !activeRuleName) {
      // "All" is selected → deselect top issues filter (show all severities)
      setTopIssuesActive(false);
      setActiveRuleName(null);
    } else {
      // Either inactive or a specific rule is selected → go back to "All" top issues
      setTopIssuesActive(true);
      setActiveRuleName(null);
    }
    setPage(0);
  }, [topIssuesActive, activeRuleName]);

  const handleRowClick = (rec: any, tab = 0) => {
    setSelectedRec(rec);
    setDetailInitialTab(tab);
    setDetailOpen(true);
  };

  // Notification deep link: /optimise?id=<recommendation_id>#recommendations opens
  // that recommendation's detail panel. Fetched by id, independent of the table's
  // filters and default status, so closed or filtered-out items still open. Tracks
  // the last handled id so a different deep link arriving without a remount still
  // opens, while filter changes stripping the param don't re-trigger.
  const lastHandledDeepLinkId = useRef('');
  useEffect(() => {
    if (!router.isReady) return;
    const deepLinkId = typeof router.query.id === 'string' ? router.query.id : '';
    if (!deepLinkId || deepLinkId === lastHandledDeepLinkId.current) return;
    lastHandledDeepLinkId.current = deepLinkId;
    (async () => {
      try {
        const result: any = await recommendationApi.getK8sRecommendation({
          recommendationId: deepLinkId,
          status: [],
          limit: 1,
        });
        const rec = result?.data?.recommendation?.[0];
        if (rec) {
          setSelectedRec(rec);
          setDetailInitialTab(0);
          setDetailOpen(true);
        } else {
          snackbar.error('The recommendation from your notification is no longer available.');
        }
      } catch {
        snackbar.error('Failed to open the recommendation from your notification.');
      }
    })();
  }, [router.isReady, router.query.id]);

  const buildTicketDescription = (rec: any): string => {
    const resourceName = rec.resource_name || rec.cloud_resourse?.name || '';
    const namespace = rec.resource_k8s_namespace || rec.cloud_resourse?.meta?.namespace || '';
    const details = recommendationApi.getRecommendationDetails(rec.category, rec.rule_name);
    let description = `**Recommendation**: ${details?.title || rec.rule_name}\n`;
    description += `**Category**: ${rec.category}\n`;
    description += `**Resource**: ${resourceName}\n`;
    if (namespace) description += `**Namespace**: ${namespace}\n`;
    description += `**Severity**: ${rec.severity || 'N/A'}\n`;
    if (rec.estimated_savings) {
      description += `**Estimated Savings**: $${rec.estimated_savings.toFixed(2)}/mo\n`;
    }
    if (rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing' && rec.recommendation) {
      const parsedRecData = safeParseJSON(rec.recommendation);
      for (const [containerName, entries] of Object.entries(parsedRecData)) {
        if (!Array.isArray(entries)) continue;
        description += `\n**Container**: ${containerName}\n`;
        const cpu = entries.find((e: any) => e.resource === 'cpu');
        const mem = entries.find((e: any) => e.resource === 'memory');
        if (cpu) {
          description += `  CPU Request: ${cpu.allocated?.request || 'N/A'} → ${cpu.recommended?.request || 'N/A'}\n`;
          description += `  CPU Limit: ${cpu.allocated?.limit || 'N/A'} → ${cpu.recommended?.limit || 'N/A'}\n`;
        }
        if (mem) {
          description += `  Memory Request: ${formatMemory(mem.allocated?.request, 'bytes', 'mb', false) || 'N/A'} → ${
            formatMemory(mem.recommended?.request, 'bytes', 'mb', false) || 'N/A'
          } MB\n`;
          description += `  Memory Limit: ${formatMemory(mem.allocated?.limit, 'bytes', 'mb', false) || 'N/A'} → ${
            formatMemory(mem.recommended?.limit, 'bytes', 'mb', false) || 'N/A'
          } MB\n`;
        }
      }
    }
    return description;
  };

  // CustomTable2 sort: map header label → backend sort field.
  const handleTableSort = useCallback((nextSort: { name: string; order: string }) => {
    const field = HEADER_TO_SORT_FIELD[nextSort.name];
    if (!field) return;
    setSortField(field);
    setSortDirection(nextSort.order as SortDirection);
    setPage(0);
  }, []);

  // CustomTable2 pagination: 1-based page; same callback handles page + pageSize.
  const handlePaginationChange = useCallback(
    (nextPage: number, pageSize: number) => {
      if (pageSize !== rowsPerPage) {
        setRowsPerPage(pageSize);
        setPage(0);
      } else {
        setPage(nextPage - 1);
      }
    },
    [rowsPerPage]
  );

  // Current sort in CustomTable2 shape.
  const sortBy = useMemo(() => ({ name: SORT_FIELD_TO_HEADER[sortField], order: sortDirection }), [sortField, sortDirection]);

  // The "Sort by" dropdown reflects the shared sort state: highlight the preset
  // matching the active (field, direction), else fall back to a friendly label.
  const activeSortValue = useMemo(
    () => SORT_ORDER.find((v) => SORT_PRESETS[v].field === sortField && SORT_PRESETS[v].direction === sortDirection),
    [sortField, sortDirection]
  );
  const sortTriggerLabel = activeSortValue ? SORT_PRESETS[activeSortValue].label : SORT_FIELD_LABEL[sortField] ?? 'Sort';
  const handleSortOptionSelect = useCallback((value: OptimizeSortValue) => {
    const preset = SORT_PRESETS[value];
    setSortField(preset.field);
    setSortDirection(preset.direction);
    setPage(0);
  }, []);

  // Stable ref so askNubiAboutRec has no dep on `accounts` and doesn't invalidate tableData.
  const accountsRef = useRef(accounts);
  useEffect(() => {
    accountsRef.current = accounts;
  }, [accounts]);

  // Reused by both the row action menu and the detail panel.
  const askNubiAboutRec = useCallback(
    (rec: any) => {
      const accountInfo = accountsRef.current[rec.account_id];
      const prompt = buildNubiOptimizePrompt({
        ruleName: formatRuleName(rec.rule_name || ''),
        category: rec.category || '',
        severity: rec.severity || 'Info',
        resourceName: getResourceDisplayName(rec, ''),
        resourceType: rec.resource_type || rec.cloud_resourse?.type || '',
        namespace: rec.resource_k8s_namespace || rec.cloud_resourse?.meta?.namespace || '',
        accountName: accountInfo?.name || '',
        estimatedSavings: rec.estimated_savings || undefined,
        brief: getRecommendationBrief(rec) || undefined,
      });
      setNubiQuery(prompt);
      setNubiAccountId(rec.account_id || '');
      setNubiConversationId(`recom_${rec.id}`);
      setNubiSidebarVisible(true);
    },
    [] // reads accounts via accountsRef — stable across account reloads
  );

  const { assistantName } = useTenantBranding();

  // CustomTable2 row data. Each row is an array of `{ component }` cell objects,
  // one per TABLE_HEADERS column, holding the same content the DS Table columns
  // rendered. The first cell carries `drilldownQuery` so `onRowClick` receives
  // the recommendation. Closes over handlers + branding.
  const tableData = useMemo(
    () =>
      tableRows.map((row) => {
        const providerLabel = row.cloudService === 'kubernetes' ? 'K8s' : row.cloudService ? row.cloudService.toUpperCase() : '';
        const providerSlug = row.accountCloudProvider || (row.cloudService === 'kubernetes' ? 'K8S' : row.cloudService.toUpperCase());

        return [
          // Severity
          {
            drilldownQuery: { rec: row.rec },
            component: <SeverityIcon level={row.severity.toLowerCase() as DsSeverityLevel} size={12} aria-label={row.severity} />,
          },
          // Resource
          {
            component: (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                  <Box component='span' sx={{ color: ds.gray[700] }}>
                    {row.resourceName}
                  </Box>
                  <CopyButton text={row.resourceName} size='xs' tone='ghost' />
                </Box>
                {(providerLabel || row.accountName || row.resourceType) && (
                  <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], color: ds.gray[500] }}>
                    {providerLabel && <CloudProviderIcon cloud_provider={providerSlug} height='14px' width='14px' />}
                    {row.accountName && <Box component='span'>{row.accountName}</Box>}
                    {row.accountName && row.resourceType && (
                      <Box component='span' sx={{ color: ds.gray[400] }}>
                        |
                      </Box>
                    )}
                    {row.resourceType && <Box component='span'>{row.resourceType}</Box>}
                  </Box>
                )}
                {row.ticketId && (
                  <Box>
                    <CustomTicketLink ticketURL={row.ticketUrl} ticketID={row.ticketId} />
                  </Box>
                )}
              </Box>
            ),
          },
          // Recommendation
          {
            component: (
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)', maxWidth: ds.space.mul(0, 130) }}>
                {row.brief && <OverflowTooltip text={row.brief} sx={{ color: ds.gray[700] }} placement='top' enterDelay={400} />}
                <Box
                  component='span'
                  sx={{
                    display: 'inline-block',
                    maxWidth: '100%',
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    color: ds.gray[500],
                  }}
                >
                  {row.ruleName}
                </Box>
              </Box>
            ),
          },
          // Category
          {
            component: row.category ? (
              <Chip variant='tag' size='xs' hue={CATEGORY_HUE[row.category] || 'slate'}>
                {CATEGORY_LABELS[row.category] || row.category}
              </Chip>
            ) : null,
          },
          // Safety band (knowledge-graph blast radius)
          {
            component: row.safetyBand ? (
              <Chip variant='status' size='2xs' tone={safetyBandTone(row.safetyBand)} dot>
                {safetyBandLabel(row.safetyBand)}
              </Chip>
            ) : (
              <Tooltip title='Blast radius not assessed for this recommendation' placement='top'>
                <Box component='span' sx={{ color: ds.gray[400], cursor: 'default' }}>
                  —
                </Box>
              </Tooltip>
            ),
          },
          // Savings
          {
            component:
              row.savings !== 0 ? (
                <Currency
                  value={Math.abs(row.savings)}
                  precison={0}
                  withTooltip={false}
                  prefix={row.savings < 0 ? '-$' : '$'}
                  suffix='/mo'
                  sx={{
                    fontSize: ds.text.body,
                    fontWeight: ds.weight.medium,
                    color: row.savings > 0 ? ds.green[600] : ds.red[600],
                  }}
                  sxSuffix={{
                    fontSize: ds.text.caption,
                    fontWeight: ds.weight.regular,
                    color: ds.gray[500],
                  }}
                />
              ) : (
                <Box component='span' sx={{ color: ds.gray[400] }}>
                  —
                </Box>
              ),
          },
          // Last Seen
          {
            component: <Datetime value={row.updatedAt} />,
          },
          // Actions
          {
            component: (
              <RowActions
                rowId={row.id}
                rec={row.rec}
                category={row.category}
                ticketId={row.ticketId}
                assistantName={assistantName}
                onAskNubi={askNubiAboutRec}
                onResolve={setResolveModalRec}
                onCreateTicket={setTicketModalRec}
                onCopyCli={setCliModalRec}
              />
            ),
          },
        ];
      }),
    [tableRows, assistantName, askNubiAboutRec, setResolveModalRec, setTicketModalRec, setCliModalRec]
  );

  return (
    <Box sx={{ p: '0px' }} data-testid='optimize-new-page'>
      {/* Summary widgets */}
      <Box sx={{ display: 'flex', gap: ds.space[3], mt: ds.space[4] }}>
        <WidgetCard
          id='optimize-card-savings'
          sx={{
            flex: 1,
            minWidth: 0,
            mt: 0,
            padding: `${ds.space[3]} ${ds.space[4]}`,
          }}
        >
          <Stat
            size='md'
            label='Total Savings'
            info={{ tooltip: 'Total estimated monthly savings if all recommendations are applied' }}
            value={cardsLoading ? '…' : <CostCallout size='lg' tone='high-savings' value={totalSavings} period='/ mo' />}
          />
        </WidgetCard>
        <WidgetCard
          sx={{
            flex: 1,
            minWidth: 0,
            mt: 0,
            padding: `${ds.space[3]} ${ds.space[4]}`,
          }}
        >
          <Stat
            size='md'
            label='Total Recommendations'
            info={{ tooltip: 'Total number of active optimization recommendations across all categories' }}
            value={
              cardsLoading ? (
                '…'
              ) : (
                <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
                  <Box component='span'>{totalCount.toLocaleString()}</Box>
                  {(criticalCount > 0 || highCount > 0) && (
                    <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                      {criticalCount > 0 && (
                        <Chip size='2xs' tone='critical' dot aria-label={`${criticalCount} critical`}>
                          {criticalCount.toLocaleString()}
                        </Chip>
                      )}
                      {highCount > 0 && (
                        <Chip size='2xs' tone='warning' dot aria-label={`${highCount} high`}>
                          {highCount.toLocaleString()}
                        </Chip>
                      )}
                    </Box>
                  )}
                </Box>
              )
            }
          />
        </WidgetCard>

        {WIDGET_CATEGORIES.map((cat) => {
          const catCount = categoryCounts[cat]?.count || 0;
          const catSavings = categoryCounts[cat]?.savings || 0;
          return (
            <WidgetCard
              key={cat}
              sx={{
                flex: 1,
                minWidth: 0,
                mt: 0,
                padding: `${ds.space[3]} ${ds.space[4]}`,
              }}
            >
              <Stat
                size='md'
                label={WIDGET_CATEGORY_LABELS[cat]}
                info={{ tooltip: WIDGET_CATEGORY_TOOLTIPS[cat] }}
                value={
                  cardsLoading ? (
                    '…'
                  ) : (
                    <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[2] }}>
                      <Box component='span'>{catCount.toLocaleString()}</Box>
                      {catSavings > 0 && <CostCallout size='sm' tone='low-savings' value={catSavings} period='/ mo' />}
                    </Box>
                  )
                }
              />
            </WidgetCard>
          );
        })}
      </Box>

      <ListingLayout id='optimize-recommendations' sx={{ mt: ds.space[4] }}>
        <ListingLayout.Toolbar
          sx={{ padding: `${ds.space[3]} ${ds.space[4]}` }}
          actions={
            <>
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[2] }}>
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600], fontWeight: ds.weight.medium }}>Sort by</Typography>
                <DropdownMenu
                  align='end'
                  size='sm'
                  items={SORT_ORDER.map((value) => ({
                    id: `optimize-sort-${value}`,
                    label: SORT_PRESETS[value].label,
                    active: activeSortValue === value,
                    onSelect: () => handleSortOptionSelect(value),
                  }))}
                  trigger={
                    <Button
                      id='optimize-sort-trigger'
                      tone='secondary'
                      size='sm'
                      icon={<KeyboardArrowDownIcon />}
                      iconPlacement='end'
                      aria-label='Sort recommendations'
                    >
                      {sortTriggerLabel}
                    </Button>
                  }
                />
              </Box>
              <Button
                id='optimize-download'
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<FileDownloadOutlinedIcon />}
                aria-label='Download recommendations as CSV'
                onClick={handleDownloadCsv}
              />
            </>
          }
        >
          <NewToggleButtons
            size='xs'
            options={[
              {
                value: 'all',
                label: (
                  <>
                    All{' '}
                    <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], fontWeight: ds.weight.regular }}>
                      ({(trackCounts.cost + trackCounts.reliability).toLocaleString()})
                    </Box>
                  </>
                ),
              },
              {
                value: 'cost',
                label: (
                  <>
                    Cost{' '}
                    <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], fontWeight: ds.weight.regular }}>
                      ({trackCounts.cost.toLocaleString()})
                    </Box>
                  </>
                ),
              },
              {
                value: 'reliability',
                label: (
                  <>
                    Reliability{' '}
                    <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[400], fontWeight: ds.weight.regular }}>
                      ({trackCounts.reliability.toLocaleString()})
                    </Box>
                  </>
                ),
              },
            ]}
            activeValue={track}
            onChange={(value) => handleTrackChange(value as OptimizeTrack)}
            width='300px'
            noShadow
          />
          <CustomSearch
            id='optimize-search'
            value={searchInput}
            onChange={(next: string) => {
              setSearchInput((prev: string) => {
                if (prev.trim() !== '' && next.trim() === '') {
                  handleFiltersChange({ ...filters, search: '' });
                }
                return next;
              });
            }}
            onEnterPress={() => handleFiltersChange({ ...filters, search: searchInput })}
            onClear={() => {
              setSearchInput('');
              handleFiltersChange({ ...filters, search: '' });
            }}
            label='Search resource…'
          />
          <FilterDropdown
            id='optimize-account-filter'
            label='Account'
            multiple
            grouped
            groupIcon={renderAccountGroupIcon}
            options={accountFilterOptions}
            value={accountFilterOptions.filter((o) => filters.account.includes(o.value))}
            onSelect={(_e: any, items: any) => {
              const next = (Array.isArray(items) ? items : []).map((it: any) => it.value);
              handleFiltersChange({ ...filters, account: next });
            }}
          />
        </ListingLayout.Toolbar>

        {/* Severity row */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[2],
            padding: `${ds.space[3]} ${ds.space[4]} ${ds.space[2]} ${ds.space[4]}`,
            flexWrap: 'wrap',
          }}
          data-testid='severity-summary-bar'
        >
          {/* Category chips — only when the active track has more than one
              category to narrow between (the reliability track is a single one). */}
          {trackCategoryOptions.length > 1 && (
            <>
              <Typography
                sx={{
                  fontSize: ds.text.caption,
                  color: ds.gray[500],
                  fontWeight: ds.weight.semibold,
                  letterSpacing: '0.5px',
                  textTransform: 'uppercase',
                  mr: ds.space[1],
                }}
              >
                Category
              </Typography>
              {severityLoading
                ? chipSkeletons(trackCategoryOptions.length, 120)
                : trackCategoryOptions.map((opt) => {
                    const isActive = filters.category.includes(opt.value);
                    const count = categoryCounts[opt.value]?.count || 0;
                    return (
                      <Chip
                        key={opt.value}
                        size='sm'
                        pressed={isActive}
                        dot
                        onClick={() => {
                          const next = isActive ? filters.category.filter((c) => c !== opt.value) : [...filters.category, opt.value];
                          handleFiltersChange({ ...filters, category: next });
                        }}
                        count={count}
                        highlightCount
                        data-testid={`category-chip-${opt.value.toLowerCase()}`}
                        sx={dotSx(CATEGORY_DOT_COLOR[opt.value])}
                      >
                        {opt.label}
                      </Chip>
                    );
                  })}

              {/* Divider between the category and severity chip groups */}
              <Box
                aria-hidden='true'
                sx={{ width: '1px', alignSelf: 'stretch', backgroundColor: ds.gray[200], mx: ds.space[1], minHeight: ds.space[4] }}
              />
            </>
          )}

          <Typography
            sx={{
              fontSize: ds.text.caption,
              color: ds.gray[500],
              fontWeight: ds.weight.semibold,
              letterSpacing: '0.5px',
              textTransform: 'uppercase',
              mr: ds.space[1],
            }}
          >
            Severity
          </Typography>
          {severityLoading
            ? chipSkeletons(5, 88)
            : summaryData.map((item) => {
                const isActive = filters.severity.includes(item.severity);
                return (
                  <Chip
                    key={item.severity}
                    size='sm'
                    pressed={isActive}
                    onClick={() => handleSeverityClick(item.severity)}
                    dot
                    tone={SEVERITY_TONE[item.severity]}
                    count={item.count}
                    highlightCount
                    data-testid={`severity-chip-${item.severity.toLowerCase()}`}
                    sx={dotSx(SEVERITY_DOT_COLOR[item.severity])}
                  >
                    {item.severity}
                  </Chip>
                );
              })}
        </Box>

        {/* Top issues row — separate band so the eye reads severity first,
            then "of those, here are the top rule names". */}
        {(severityLoading || topIssueData.items.length > 0 || hasActiveFilter) && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[2],
              padding: `${ds.space[1]} ${ds.space[4]}`,
              flexWrap: 'wrap',
            }}
            data-testid='top-issues-bar'
          >
            {(severityLoading || topIssueData.items.length > 0) && (
              <>
                <Box sx={{ display: 'inline-flex', alignItems: 'baseline', gap: ds.space[1], mr: ds.space[1] }}>
                  <Typography
                    sx={{
                      fontSize: ds.text.caption,
                      color: ds.gray[500],
                      fontWeight: ds.weight.semibold,
                      letterSpacing: '0.5px',
                      textTransform: 'uppercase',
                    }}
                  >
                    Top Issues
                  </Typography>
                  <Typography sx={{ fontSize: ds.text.caption, color: ds.gray[500] }}>({topIssueData.severityLabel})</Typography>
                </Box>
                {severityLoading ? (
                  chipSkeletons(5, 150)
                ) : (
                  <>
                    <Chip
                      size='sm'
                      pressed={topIssuesActive && !activeRuleName}
                      onClick={handleToggleTopIssues}
                      count={topIssueData.total}
                      highlightCount
                    >
                      All
                    </Chip>
                    {topIssueData.items.map((item) => {
                      const isActive = topIssuesActive && activeRuleName === item.rule_name;
                      return (
                        <Chip
                          key={item.rule_name}
                          size='sm'
                          pressed={isActive}
                          onClick={() => handleRuleClick(item.rule_name)}
                          count={item.count}
                          highlightCount
                        >
                          {formatRuleName(item.rule_name)}
                        </Chip>
                      );
                    })}
                  </>
                )}
              </>
            )}
            {hasActiveFilter && (
              <Box sx={{ ml: 'auto' }}>
                <Button id='optimize-clear-filters' tone='link' size='xs' icon={<CloseIcon sx={{ fontSize: 12 }} />} onClick={handleClearAll}>
                  Clear all
                </Button>
              </Box>
            )}
          </Box>
        )}

        <ListingLayout.Body>
          <CustomTable2
            id='optimize-recommendations-table'
            headers={TABLE_HEADERS}
            tableData={tableData}
            loading={tableLoading}
            rowsPerPage={rowsPerPage}
            totalRows={tableTotal}
            pageNumber={page + 1}
            onPageChange={handlePaginationChange}
            sort={sortBy}
            onSortChange={handleTableSort}
            onRowClick={(query: any) => query?.rec && handleRowClick(query.rec)}
            showEmptyStateText
            emptyStateText={
              hasActiveFilter
                ? 'No recommendations match these filters. Try clearing one of the filters to see more results.'
                : 'No active recommendations. Your infrastructure looks well-optimised — check back after the next scan.'
            }
          />
        </ListingLayout.Body>
      </ListingLayout>

      {/* Detail panel */}
      <RecommendationDetailPanel
        open={detailOpen}
        onClose={() => setDetailOpen(false)}
        recommendation={selectedRec}
        accounts={accounts}
        initialTab={detailInitialTab}
        onCreateTicket={(rec) => setTicketModalRec(rec)}
        onResolve={(rec) => setResolveModalRec(rec)}
        onCopyCli={(rec) => setCliModalRec(rec)}
        onAskNubi={(rec) => {
          const accountInfo = accounts[rec.account_id];
          const prompt = buildNubiOptimizePrompt({
            ruleName: formatRuleName(rec.rule_name || ''),
            category: rec.category || '',
            severity: rec.severity || 'Info',
            resourceName: rec.resource_name || rec.cloud_resourse?.name || rec.account_object_id || '',
            resourceType: rec.resource_type || rec.cloud_resourse?.type || '',
            namespace: rec.resource_k8s_namespace || rec.cloud_resourse?.meta?.namespace || '',
            accountName: accountInfo?.name || '',
            estimatedSavings: rec.estimated_savings || undefined,
            brief: getRecommendationBrief(rec) || undefined,
          });
          setNubiQuery(prompt);
          setNubiAccountId(rec.account_id || '');
          setNubiConversationId(`recom_${rec.id}`);
          setDetailOpen(false);
          setNubiSidebarVisible(true);
        }}
      />

      {/* Direct Create Ticket modal */}
      {ticketModalRec && (
        <TicketCreatePopupForm
          open={!!ticketModalRec}
          handleClose={() => setTicketModalRec(null)}
          onClose={() => setTicketModalRec(null)}
          onSuccess={({ ticketId, url }: { ticketId?: string; url?: string } = {}) => {
            const recId = ticketModalRec?.id;
            setTicketModalRec(null);
            if (recId) {
              setRecommendations((prev) => prev.map((rec: any) => (rec.id === recId ? { ...rec, ticket: { ticket_id: ticketId, url } } : rec)));
            }
          }}
          onFailure={(error: string) => {
            snackbar.error(error || 'Failed to create ticket');
          }}
          ticketData={{
            subject: `${ticketModalRec.category} - ${
              recommendationApi.getRecommendationDetails(ticketModalRec.category, ticketModalRec.rule_name)?.title || ticketModalRec.rule_name
            }: ${getResourceDisplayName(ticketModalRec, '')}`,
            description: buildTicketDescription(ticketModalRec),
            accountId: ticketModalRec.account_id || '',
          }}
          ticketUrl={{}}
          reference={{
            id: ticketModalRec.id,
            type: getTicketSourceFromCloudProvider(accounts[ticketModalRec.account_id]?.cloud_provider),
          }}
        />
      )}

      {/* Direct Resolve modal */}
      {resolveModalRec && (
        <ResolveModal
          open={!!resolveModalRec}
          onClose={() => setResolveModalRec(null)}
          recommendation={resolveModalRec}
          clusterName={accounts[resolveModalRec.account_id]?.name}
          // ResolveModal fires its own action-specific toast (deploy fix / auto-optimize rule); close and refresh the list to reflect the new status.
          onSuccess={() => {
            setResolveModalRec(null);
            fetchTableData();
          }}
        />
      )}

      {/* CLI Command modal */}
      {cliModalRec && <CliCommandModal rec={cliModalRec} onClose={() => setCliModalRec(null)} />}

      {/* NuBi AI sidebar */}
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
    </Box>
  );
};

export default OptimizeNewPage;
