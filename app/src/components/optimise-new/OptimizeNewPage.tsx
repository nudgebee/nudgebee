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
import CustomTable from '@shared/tables/CustomTable';
import { DropdownMenu } from '@ui/DropdownMenu';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import DoNotDisturbOnOutlinedIcon from '@mui/icons-material/DoNotDisturbOnOutlined';
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
import DismissModal from './DismissModal';
import TicketLink from '@shared/links/TicketLink';
import { hasWriteAccess } from '@lib/auth';
import { formatMemory } from '@lib/formatter';
import ResolveModal from './ResolveModal';
import CliCommandModal from './CliCommandModal';
import WidgetCard from '@ui/WidgetCard';
import { ListingLayout } from '@ui/ListingLayout';
import { Stat } from '@ui/Stat';
import { CostCallout } from '@ui/CostCallout';
import { Chip } from '@ui/Chip';
import { safetyBandTone, safetyBandLabel } from './safetyBand';
import SearchInput from '@ui/SearchInput';
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
  SAVINGS_BUCKETS,
  LAST_SEEN_BUCKETS,
  savingsBucketToParams,
  lastSeenBucketToParams,
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
  // Single-select in the UI (the category stat-cards act as tabs), but kept as
  // an array so existing ?category= URLs with multiple values keep working.
  category: string[];
  search: string;
  safety: string[];
  rules: string[];
  savings: string;
  lastSeen: string;
}

const SEVERITY_ORDER: SeverityLevel[] = ['Critical', 'High', 'Medium', 'Low', 'Info'];

// Severities selected on first load — the two that warrant immediate attention.
// Also the fallback the Top Issues band and table query use when no severity is chosen.
const DEFAULT_SEVERITY: SeverityLevel[] = ['Critical', 'High'];

// Leading-dot colour per category — carried on the category stat-card tabs and
// the group headers inside the Rules filter, so both read as one categorical set.
const CATEGORY_DOT_COLOR: Record<string, string> = {
  RightSizing: 'var(--ds-blue-500)',
  InfraUpgrade: 'var(--ds-purple-500)',
  Configuration: 'var(--ds-amber-500)',
  K8sSpotRecommendation: 'var(--ds-green-500)',
};

// Display order + dot colours for the Safety filter chips. `unknown` collects
// rows whose blast radius was never assessed (NULL safety_band included — see
// applyFacetFilters in @api1/recommendation).
const SAFETY_ORDER = ['safe', 'review', 'risky', 'unknown'] as const;
const SAFETY_DOT_COLOR: Record<string, string> = {
  safe: 'var(--ds-green-500)',
  review: 'var(--ds-amber-500)',
  risky: 'var(--ds-red-500)',
  unknown: 'var(--ds-gray-400)',
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

// Shared chrome for the clickable stat-card tabs: the active card carries the
// same blue border + tint the Troubleshoot summary widgets use for their active
// drill-down; zero-count cards render muted and inert.
const cardTabSx = (pressed: boolean, muted: boolean) => ({
  flex: 1,
  minWidth: 0,
  mt: 0,
  padding: `${ds.space[3]} ${ds.space[4]}`,
  ...(muted
    ? { opacity: 0.5 }
    : {
        cursor: 'pointer',
        // Stat and Chip pin their own `cursor: default`, which would otherwise leave
        // the hand pointer showing only on the card's bare padding. `&&` outranks them.
        '&& *': { cursor: 'pointer' },
        transition: `border-color ${ds.motion.micro} ${ds.motion.ease}, background-color ${ds.motion.micro} ${ds.motion.ease}`,
        // Re-assert the blue border on hover for the active card — the gray hover
        // border would otherwise mask its highlight while hovering.
        '&:hover': { borderColor: pressed ? ds.blue[400] : ds.gray[400] },
      }),
  ...(pressed ? { borderColor: ds.blue[400], backgroundColor: ds.blue[100] } : {}),
});

// Enter/Space activation so the card tabs work as buttons for keyboard users.
const cardKeyDown = (activate: () => void) => (e: React.KeyboardEvent) => {
  if (e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    activate();
  }
};

/** Parse a URL query param that may be a string or string[] into a string[] */
const parseQueryArray = (param: string | string[] | undefined): string[] => {
  if (!param) {
    return [];
  }
  return Array.isArray(param) ? param : [param];
};

// Map between CustomTable header labels and backend sort fields.
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
// `sortEnabled` so CustomTable renders the sort affordance. Safety sits 5th
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
  ticketId: string;
  assistantName: string | undefined;
  onAskNubi: (rec: any) => void;
  onResolve: (rec: any) => void;
  onCreateTicket: (rec: any) => void;
  onCopyCli: (rec: any) => void;
  onDismiss: (rec: any) => void;
}

const RowActions = memo(({ rowId, rec, ticketId, assistantName, onAskNubi, onResolve, onCreateTicket, onCopyCli, onDismiss }: RowActionsProps) => {
  const showResolve = rec.rule_name === 'pod_right_sizing' && hasWriteAccess(rec.account_id);
  const showCopyCli = rec.rule_name === 'pod_right_sizing';
  const canDismiss = hasWriteAccess(rec.account_id);

  const menuItems: Array<{ label: string; icon: React.ReactNode; onSelect: () => void; disabled?: boolean; id?: string }> = [
    {
      id: `action-ticket-${rowId}`,
      label: ticketId ? `Ticket: ${ticketId}` : 'Create ticket',
      icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
      onSelect: () => onCreateTicket(rec),
      disabled: !!ticketId,
    },
    ...(canDismiss
      ? [
          {
            id: `action-dismiss-${rowId}`,
            label: rec.status === 'Dismissed' ? 'Reactivate' : 'Dismiss / snooze',
            icon: <DoNotDisturbOnOutlinedIcon sx={{ fontSize: 16 }} />,
            onSelect: () => onDismiss(rec),
          },
        ]
      : []),
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
  // Severity chips + Rules filter options: raw per-(rule, severity) aggregate
  // rows, scoped by account, category, search and the safety/savings/last-seen
  // facets — matching the table below. Kept raw so the severity chip counts can
  // be re-derived client-side when the Rules selection changes (no refetch).
  const [summaryRows, setSummaryRows] = useState<any[]>([]);
  const [severityLoading, setSeverityLoading] = useState(true);
  // Safety chip counts — separate aggregate because a facet's own counts must
  // not shrink when that facet is selected.
  const [safetyRows, setSafetyRows] = useState<{ count: number; safety_band: string | null }[]>([]);
  const [safetyLoading, setSafetyLoading] = useState(true);
  // Distinguishes "the aggregate failed" from "every band is empty" — without it
  // a failed fetch mutes all four chips as if there were nothing to filter.
  const [safetyError, setSafetyError] = useState(false);

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

  // Filters state — initialised from URL query params
  const [filters, setFilters] = useState<FilterState>(() => {
    // Default to Critical + High on first load; an explicit ?severity= in the URL wins.
    const severityFromUrl = parseQueryArray(router.query.severity) as SeverityLevel[];
    return {
      severity: severityFromUrl.length > 0 ? severityFromUrl : DEFAULT_SEVERITY,
      account: parseQueryArray(router.query.account),
      category: parseQueryArray(router.query.category),
      search: (router.query.search as string) || '',
      safety: parseQueryArray(router.query.safety),
      rules: parseQueryArray(router.query.rules),
      savings: (router.query.savings as string) || '',
      lastSeen: (router.query.seen as string) || '',
    };
  });

  // Local search input state — typed value, not yet applied. Mirrors ManualInvestigated pattern.
  const [searchInput, setSearchInput] = useState((router.query.search as string) || '');

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
  const [dismissModalRec, setDismissModalRec] = useState<any>(null);

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
    if (newFilters.safety.length > 0) {
      query.safety = newFilters.safety;
    }
    if (newFilters.rules.length > 0) {
      query.rules = newFilters.rules;
    }
    if (newFilters.savings) {
      query.savings = newFilters.savings;
    }
    if (newFilters.lastSeen) {
      query.seen = newFilters.lastSeen;
    }

    const currentHash = r.asPath.split('#')[1];
    r.replace({ pathname: r.pathname, query, ...(currentHash ? { hash: `#${currentHash}` } : {}) }, undefined, { shallow: true });
  }, []);

  const handleFiltersChange = useCallback(
    (newFilters: FilterState) => {
      setFilters(newFilters);
      setPage(0);
      updateUrl(newFilters);
    },
    [updateUrl]
  );

  // Reset every filter back to empty (the category card tabs return to All).
  const handleClearAll = useCallback(() => {
    setSearchInput('');
    handleFiltersChange({ severity: [], account: [], category: [], search: '', safety: [], rules: [], savings: '', lastSeen: '' });
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

      // No category card selected → all optimize categories.
      query.category = merged.category.length > 0 ? merged.category : NON_SECURITY_CATEGORIES;
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

      if (merged.safety.length > 0) {
        query.safetyBand = merged.safety;
      }
      Object.assign(query, savingsBucketToParams(merged.savings), lastSeenBucketToParams(merged.lastSeen));

      return query;
    },
    [filters]
  );

  // Bucket raw per-(rule, severity) aggregate rows into per-severity totals.
  const processSummaryResults = useCallback((rows: any[]): SeveritySummaryData[] => {
    return SEVERITY_ORDER.map((sev) => {
      const sevRows = rows.filter((r: any) => r.severity === sev);
      const count = sevRows.reduce((sum: number, r: any) => sum + (r.count || 0), 0);
      const savings = sevRows.reduce((sum: number, r: any) => sum + (r.sum_estimated_savings || 0), 0);
      return { severity: sev, count, savings };
    });
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

  // Fetch severity chips + Rules filter options — scoped by account, category,
  // search and the safety/savings/last-seen facets, matching the table below.
  // The Rules selection is deliberately NOT applied server-side here: these rows
  // also feed the Rules dropdown, and a facet's own options must not vanish when
  // selected. Severity chips get the rules narrowing client-side (see summaryData).
  useEffect(() => {
    let cancelled = false;
    setSeverityLoading(true);

    const accountId = filters.account.length > 0 ? filters.account : '';
    const activeCategories = filters.category.length > 0 ? filters.category : NON_SECURITY_CATEGORIES;

    const fetchSeverityRows = async () => {
      try {
        const rows = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          accountId,
          category: activeCategories as any,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          accountObjectId: filters.search || undefined,
          status: DEFAULT_STATUS,
          severity: [...SEVERITY_ORDER],
          safetyBand: filters.safety.length > 0 ? filters.safety : undefined,
          ...savingsBucketToParams(filters.savings),
          ...lastSeenBucketToParams(filters.lastSeen),
        });
        if (cancelled) {
          return;
        }
        setSummaryRows(Array.isArray(rows) ? rows : []);
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
  }, [filters.account, filters.category, filters.search, filters.safety, filters.savings, filters.lastSeen]);

  // Severity chip counts — the raw aggregate narrowed client-side by the Rules
  // selection, so the chips always predict what clicking them will show.
  const summaryData = useMemo(() => {
    const rows = filters.rules.length > 0 ? summaryRows.filter((r: any) => filters.rules.includes(r.rule_name)) : summaryRows;
    return processSummaryResults(rows);
  }, [summaryRows, filters.rules, processSummaryResults]);

  // Fetch safety chip counts — same scope as the summary above PLUS the rules
  // selection, minus the safety facet itself.
  useEffect(() => {
    let cancelled = false;
    setSafetyLoading(true);

    const accountId = filters.account.length > 0 ? filters.account : '';
    const activeCategories = filters.category.length > 0 ? filters.category : NON_SECURITY_CATEGORIES;

    const fetchSafetyRows = async () => {
      try {
        const rows = await recommendationApi.getK8sRecommendationSafetyGroups({
          accountId,
          category: activeCategories,
          ruleName: filters.rules.length > 0 ? filters.rules : undefined,
          excludeRuleName: UPGRADE_PLANNER_RULES,
          accountObjectId: filters.search || undefined,
          status: DEFAULT_STATUS,
          severity: filters.severity.length > 0 ? [...filters.severity] : [...SEVERITY_ORDER],
          ...savingsBucketToParams(filters.savings),
          ...lastSeenBucketToParams(filters.lastSeen),
        });
        if (cancelled) {
          return;
        }
        setSafetyRows(Array.isArray(rows) ? rows : []);
        setSafetyError(false);
      } catch {
        // Non-blocking: the safety chips fall back to countless rendering, so
        // the bands stay clickable instead of looking like empty results.
        if (!cancelled) {
          setSafetyRows([]);
          setSafetyError(true);
        }
      } finally {
        if (!cancelled) {
          setSafetyLoading(false);
        }
      }
    };

    fetchSafetyRows();
    return () => {
      cancelled = true;
    };
  }, [filters.account, filters.category, filters.search, filters.severity, filters.rules, filters.savings, filters.lastSeen]);

  // Safety band → count. NULL / unrecognised bands roll into `unknown`, matching
  // how the table renders unassessed rows and how the unknown filter queries.
  const safetyCounts = useMemo(() => {
    const counts: Record<string, number> = { safe: 0, review: 0, risky: 0, unknown: 0 };
    for (const row of safetyRows) {
      const band = row.safety_band && counts[row.safety_band] !== undefined ? row.safety_band : 'unknown';
      counts[band] += row.count || 0;
    }
    return counts;
  }, [safetyRows]);

  // Fetch table data — used both by useEffect (with cancellation) and manual refresh calls
  const buildTableQuery = useCallback(() => {
    return {
      ...buildFilterQuery(),
      // An explicit rule selection replaces the UPGRADE_PLANNER_RULES exclusion
      // inside the API (`_in` and `_not_in` cannot coexist) — safe, because the
      // Rules options are sourced from an aggregate that already excludes them.
      ...(filters.rules.length > 0 ? { ruleName: filters.rules } : {}),
      orderBy: sortField,
      orderAsc: sortDirection === 'asc',
      limit: rowsPerPage,
      offset: page * rowsPerPage,
      fetchTicket: true,
    };
  }, [buildFilterQuery, filters.rules, sortField, sortDirection, rowsPerPage, page]);

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

  // ─── Computed: Rules filter (grouped by category, sorted by open count) ───

  // Options come from the same aggregate as the severity chips, merged across
  // the selected severities (all severities when none are selected), grouped
  // under their category — the Account-dropdown treatment. The open count rides
  // in the label; `searchText` keeps the built-in search matching on the name.
  const rulesFilterOptions = useMemo(() => {
    const targetSeverities: string[] = filters.severity.length > 0 ? filters.severity : [...SEVERITY_ORDER];
    const agg: Record<string, { count: number; category: string }> = {};
    for (const r of summaryRows) {
      if (!r.rule_name || !targetSeverities.includes(r.severity)) {
        continue;
      }
      const entry = agg[r.rule_name] || { count: 0, category: r.category || '' };
      entry.count += r.count || 0;
      agg[r.rule_name] = entry;
    }
    return Object.entries(agg)
      .sort((a, b) => b[1].count - a[1].count)
      .map(([ruleName, entry]) => ({
        label: `${formatRuleName(ruleName)} (${entry.count})`,
        value: ruleName,
        group: CATEGORY_LABELS[entry.category] || entry.category || 'Other',
        searchText: formatRuleName(ruleName),
      }));
  }, [summaryRows, filters.severity]);

  // Selected rules render from filter state, never by filtering the options —
  // a selected rule stays visible in the trigger even when the shrinking
  // aggregate drops it from the options list (it would otherwise silently
  // filter the table with an idle-looking control).
  const rulesFilterValue = useMemo(
    () =>
      filters.rules.map(
        (ruleName) =>
          rulesFilterOptions.find((o) => o.value === ruleName) || {
            label: formatRuleName(ruleName),
            value: ruleName,
            group: 'Selected',
            searchText: formatRuleName(ruleName),
          }
      ),
    [filters.rules, rulesFilterOptions]
  );

  // Preset options for the single-select Savings / Last-seen filters. The ''
  // ("Any" / "Any time") entry reads as no-selection in FilterDropdown, so
  // picking it clears the filter.
  const savingsFilterOptions = useMemo(() => SAVINGS_BUCKETS.map((b) => ({ label: b.label, value: b.key })), []);
  const lastSeenFilterOptions = useMemo(() => LAST_SEEN_BUCKETS.map((b) => ({ label: b.label, value: b.key })), []);

  // Category label → dot colour for the Rules dropdown group headers.
  const rulesGroupIcon = useCallback((groupLabel: string) => {
    const categoryKey = Object.keys(CATEGORY_LABELS).find((k) => CATEGORY_LABELS[k] === groupLabel);
    return (
      <Box
        component='span'
        sx={{
          width: 8,
          height: 8,
          borderRadius: '50%',
          display: 'inline-block',
          backgroundColor: categoryKey ? CATEGORY_DOT_COLOR[categoryKey] : ds.gray[400],
        }}
      />
    );
  }, []);

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

  // Card totals: derived from cardRows (unfiltered by category/search) so the
  // All Recommendations card stays a static overview, same as the per-category cards.
  const cardSummaryData = useMemo(() => processSummaryResults(cardRows), [cardRows, processSummaryResults]);
  const totalCount = useMemo(() => cardSummaryData.reduce((sum, s) => sum + s.count, 0), [cardSummaryData]);
  const totalSavings = useMemo(() => cardSummaryData.reduce((sum, s) => sum + s.savings, 0), [cardSummaryData]);
  const criticalCount = useMemo(() => cardSummaryData.find((s) => s.severity === 'Critical')?.count || 0, [cardSummaryData]);
  const highCount = useMemo(() => cardSummaryData.find((s) => s.severity === 'High')?.count || 0, [cardSummaryData]);

  const hasActiveFilter = useMemo(
    () =>
      filters.severity.length > 0 ||
      filters.account.length > 0 ||
      filters.category.length > 0 ||
      filters.search.length > 0 ||
      filters.safety.length > 0 ||
      filters.rules.length > 0 ||
      filters.savings.length > 0 ||
      filters.lastSeen.length > 0,
    [filters]
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

  // Rule → category, from the unfiltered card aggregate, used to keep the Rules
  // selection consistent with the category card tabs in both directions.
  const ruleCategoryMap = useMemo(() => {
    const map: Record<string, string> = {};
    for (const r of cardRows) {
      if (r.rule_name && r.category && !map[r.rule_name]) {
        map[r.rule_name] = r.category;
      }
    }
    return map;
  }, [cardRows]);

  // Category card tabs: single-select; clicking the active card again unselects
  // back to All. Narrowing prunes selected rules from other categories (rules
  // with no known category are kept — better to over-show than silently drop).
  const handleCategoryCardClick = useCallback(
    (category: string) => {
      const isActive = filters.category.length === 1 && filters.category[0] === category;
      const nextCategory = isActive ? [] : [category];
      const nextRules =
        nextCategory.length > 0 ? filters.rules.filter((rule) => !ruleCategoryMap[rule] || ruleCategoryMap[rule] === category) : filters.rules;
      handleFiltersChange({ ...filters, category: nextCategory, rules: nextRules });
    },
    [filters, handleFiltersChange, ruleCategoryMap]
  );

  // The All card always selects the full category set (no toggle — there is
  // nothing narrower to fall back to).
  const handleAllCardClick = useCallback(() => {
    if (filters.category.length > 0) {
      handleFiltersChange({ ...filters, category: [] });
    }
  }, [filters, handleFiltersChange]);

  // Multi-select: each click toggles that severity in/out of the filter set.
  // (Multi-select is group state — the Chip itself just reflects `pressed`.)
  const handleSeverityClick = useCallback(
    (severity: SeverityLevel) => {
      const next = filters.severity.includes(severity) ? filters.severity.filter((s) => s !== severity) : [...filters.severity, severity];
      handleFiltersChange({ ...filters, severity: next });
    },
    [filters, handleFiltersChange]
  );

  const handleSafetyClick = useCallback(
    (band: string) => {
      const next = filters.safety.includes(band) ? filters.safety.filter((b) => b !== band) : [...filters.safety, band];
      handleFiltersChange({ ...filters, safety: next });
    },
    [filters, handleFiltersChange]
  );

  // Rules selection — picking a rule outside the active category card widens the
  // category back to All so the table never silently hides the picked rule.
  const handleRulesChange = useCallback(
    (nextRules: string[]) => {
      const activeCategory = filters.category.length === 1 ? filters.category[0] : '';
      const crossesCategory = activeCategory && nextRules.some((rule) => ruleCategoryMap[rule] && ruleCategoryMap[rule] !== activeCategory);
      handleFiltersChange({ ...filters, rules: nextRules, ...(crossesCategory ? { category: [] } : {}) });
    },
    [filters, handleFiltersChange, ruleCategoryMap]
  );

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
    if (rec.rule_name === 'pod_right_sizing' && rec.recommendation) {
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

  // CustomTable sort: map header label → backend sort field.
  const handleTableSort = useCallback((nextSort: { name: string; order: string }) => {
    const field = HEADER_TO_SORT_FIELD[nextSort.name];
    if (!field) return;
    setSortField(field);
    setSortDirection(nextSort.order as SortDirection);
    setPage(0);
  }, []);

  // CustomTable pagination: 1-based page; same callback handles page + pageSize.
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

  // Current sort in CustomTable shape.
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
  // Dismiss/snooze opens the modal; a Dismissed rec reactivates directly.
  const handleDismissAction = useCallback((rec: any) => {
    if (rec.status !== 'Dismissed') {
      setDismissModalRec(rec);
      return;
    }
    recommendationApi
      .updateRecommendationDismissal(rec.account_id, rec.id, { dismissed: false })
      .then((res: any) => {
        if (res?.errors?.length) {
          snackbar.error(res.errors[0]?.message || 'Failed to reactivate recommendation');
          return;
        }
        // Fail closed: an empty response means the change cannot be confirmed.
        if (!res?.data || res.data.applied === false) {
          snackbar.error(res?.data?.message || 'Failed to reactivate recommendation');
          return;
        }
        snackbar.success('Recommendation reactivated');
        setRecommendations((prev) => prev.map((r: any) => (r.id === rec.id ? { ...r, status: 'Open', snoozed_until: null } : r)));
        // A Dismissed rec is absent from the table (default status filter), so the
        // list update above can't reach the open drawer — update it directly.
        setSelectedRec((prev: any) => (prev?.id === rec.id ? { ...prev, status: 'Open', snoozed_until: null } : prev));
      })
      .catch(() => {
        snackbar.error('Failed to reactivate recommendation');
      });
  }, []);

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
        alarmConfig: safeParseJSON(rec.recommendation)?.alarm_config || undefined,
      });
      setNubiQuery(prompt);
      setNubiAccountId(rec.account_id || '');
      setNubiConversationId(`recom_${rec.id}`);
      setNubiSidebarVisible(true);
    },
    [] // reads accounts via accountsRef — stable across account reloads
  );

  const { assistantName } = useTenantBranding();

  // CustomTable row data. Each row is an array of `{ component }` cell objects,
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
                    <TicketLink ticketURL={row.ticketUrl} ticketID={row.ticketId} />
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
                ticketId={row.ticketId}
                assistantName={assistantName}
                onAskNubi={askNubiAboutRec}
                onResolve={setResolveModalRec}
                onCreateTicket={setTicketModalRec}
                onCopyCli={setCliModalRec}
                onDismiss={handleDismissAction}
              />
            ),
          },
        ];
      }),
    [tableRows, assistantName, askNubiAboutRec, setResolveModalRec, setTicketModalRec, setCliModalRec, handleDismissAction]
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
          id='optimize-card-all'
          role='button'
          tabIndex={0}
          aria-pressed={filters.category.length === 0}
          data-testid='optimize-card-all'
          onClick={handleAllCardClick}
          onKeyDown={cardKeyDown(handleAllCardClick)}
          sx={cardTabSx(filters.category.length === 0, false)}
        >
          <Stat
            size='md'
            label='All Recommendations'
            info={{ tooltip: 'Total number of active optimization recommendations across all categories. Click to show every category.' }}
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
          const pressed = filters.category.length === 1 && filters.category[0] === cat;
          const muted = catCount === 0 && !pressed && !cardsLoading;
          const card = (
            <WidgetCard
              key={cat}
              role='button'
              tabIndex={muted ? -1 : 0}
              aria-pressed={pressed}
              aria-disabled={muted || undefined}
              data-testid={`optimize-card-${cat.toLowerCase()}`}
              onClick={muted ? undefined : () => handleCategoryCardClick(cat)}
              onKeyDown={muted ? undefined : cardKeyDown(() => handleCategoryCardClick(cat))}
              sx={cardTabSx(pressed, muted)}
            >
              <Stat
                size='md'
                label={WIDGET_CATEGORY_LABELS[cat]}
                info={{ tooltip: `${WIDGET_CATEGORY_TOOLTIPS[cat]} Click to filter the list; click again to unselect.` }}
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
          return muted ? (
            <Tooltip key={cat} title='No open recommendations' placement='top'>
              {card}
            </Tooltip>
          ) : (
            card
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
          <SearchInput
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
          <FilterDropdown
            id='optimize-rules-filter'
            label='Rules'
            searchPlaceholder='Search rules…'
            multiple
            grouped
            groupIcon={rulesGroupIcon}
            isOptionsLoading={severityLoading}
            options={rulesFilterOptions}
            value={rulesFilterValue}
            onSelect={(_e: any, items: any) => {
              handleRulesChange((Array.isArray(items) ? items : []).map((it: any) => it.value));
            }}
          />
          <FilterDropdown
            id='optimize-savings-filter'
            label='Savings'
            options={savingsFilterOptions}
            value={savingsFilterOptions.find((o) => o.value === filters.savings && o.value !== '') || null}
            onSelect={(_e: any, item: any) => {
              const next = (item && typeof item === 'object' ? item.value : item) || '';
              handleFiltersChange({ ...filters, savings: next });
            }}
          />
          <FilterDropdown
            id='optimize-last-seen-filter'
            label='Last seen'
            options={lastSeenFilterOptions}
            value={lastSeenFilterOptions.find((o) => o.value === filters.lastSeen && o.value !== '') || null}
            onSelect={(_e: any, item: any) => {
              const next = (item && typeof item === 'object' ? item.value : item) || '';
              handleFiltersChange({ ...filters, lastSeen: next });
            }}
          />
          {hasActiveFilter && (
            <Button id='optimize-clear-filters' tone='link' size='xs' icon={<CloseIcon sx={{ fontSize: 12 }} />} onClick={handleClearAll}>
              Clear all
            </Button>
          )}
        </ListingLayout.Toolbar>

        {/* Severity + Safety chip row */}
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
                const muted = item.count === 0 && !isActive;
                const chip = (
                  <Chip
                    key={item.severity}
                    size='sm'
                    pressed={isActive}
                    disabled={muted}
                    onClick={muted ? undefined : () => handleSeverityClick(item.severity)}
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
                return muted ? (
                  <Tooltip key={item.severity} title='No open recommendations' placement='top'>
                    <Box component='span' sx={{ display: 'inline-flex' }}>
                      {chip}
                    </Box>
                  </Tooltip>
                ) : (
                  chip
                );
              })}

          {/* Divider between the severity and safety chip groups */}
          <Box
            aria-hidden='true'
            sx={{ width: '1px', alignSelf: 'stretch', backgroundColor: ds.gray[200], mx: ds.space[1], minHeight: ds.space[4] }}
          />

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
            Safety
          </Typography>
          {safetyLoading
            ? chipSkeletons(4, 88)
            : SAFETY_ORDER.map((band) => {
                const isActive = filters.safety.includes(band);
                // Counts are unknown when the aggregate failed — render the band
                // countless and clickable rather than muting it as if it were empty.
                const count = safetyError ? undefined : safetyCounts[band] || 0;
                const muted = count === 0 && !isActive;
                const chip = (
                  <Chip
                    key={band}
                    size='sm'
                    pressed={isActive}
                    disabled={muted}
                    onClick={muted ? undefined : () => handleSafetyClick(band)}
                    dot
                    tone={safetyBandTone(band)}
                    count={count}
                    highlightCount={count !== undefined}
                    data-testid={`safety-chip-${band}`}
                    sx={dotSx(SAFETY_DOT_COLOR[band])}
                  >
                    {safetyBandLabel(band)}
                  </Chip>
                );
                return muted ? (
                  <Tooltip key={band} title='No open recommendations' placement='top'>
                    <Box component='span' sx={{ display: 'inline-flex' }}>
                      {chip}
                    </Box>
                  </Tooltip>
                ) : (
                  chip
                );
              })}
        </Box>

        <ListingLayout.Body>
          <CustomTable
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
        onDismiss={handleDismissAction}
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
            alarmConfig: safeParseJSON(rec.recommendation)?.alarm_config || undefined,
          });
          setNubiQuery(prompt);
          setNubiAccountId(rec.account_id || '');
          setNubiConversationId(`recom_${rec.id}`);
          setDetailOpen(false);
          setNubiSidebarVisible(true);
        }}
      />

      {/* Dismiss / snooze modal */}
      {dismissModalRec && (
        <DismissModal
          rec={dismissModalRec}
          onClose={() => setDismissModalRec(null)}
          onSuccess={(recId: string) => {
            setDismissModalRec(null);
            setDetailOpen(false);
            setRecommendations((prev) => prev.filter((r: any) => r.id !== recId));
          }}
        />
      )}

      {/* Direct Create Ticket modal */}
      {ticketModalRec && (
        <TicketCreatePopupForm
          open={!!ticketModalRec}
          handleClose={() => setTicketModalRec(null)}
          onClose={() => setTicketModalRec(null)}
          onSuccess={({ ticketId, url }: { ticketId?: string; url?: string } = {}) => {
            const recId = ticketModalRec?.id;
            const recAccountId = ticketModalRec?.account_id;
            setTicketModalRec(null);
            if (recId) {
              setRecommendations((prev) => prev.map((rec: any) => (rec.id === recId ? { ...rec, ticket: { ticket_id: ticketId, url } } : rec)));
            }
            // Record the ticket as a resolution attempt so the recommendation is
            // claimed and the Resolutions tab shows the delegation. Best-effort:
            // the ticket exists either way.
            if (recId && recAccountId && ticketId) {
              recommendationApi.createTicketResolution(recAccountId, recId, String(ticketId)).catch((e: unknown) => {
                console.error('failed to record ticket resolution', e);
              });
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
