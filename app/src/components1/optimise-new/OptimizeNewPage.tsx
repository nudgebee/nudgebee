import { Box, Typography } from '@mui/material';
import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { useRouter } from 'next/router';
import { ds } from 'src/utils/colors';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import { transformClusters } from '@components1/common/UpdateDataContext';
import recommendationApi from '@api1/recommendation';
import Loader from '@components1/common/Loader';
import { toast as snackbar } from '@components1/ds/Toast';
import { SeverityIcon, type SeverityLevel as DsSeverityLevel } from '@components1/ds/SeverityIcon';
import { Skeleton } from '@components1/ds/Skeleton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import ConfirmationNumberOutlinedIcon from '@mui/icons-material/ConfirmationNumberOutlined';
import ContentCopyOutlinedIcon from '@mui/icons-material/ContentCopyOutlined';
import OptimizeIcon from 'src/assets/images/home/optimize-icon-button.svg';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import SafeIcon from '@components1/common/SafeIcon';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import Currency from '@common-new/format/Currency';
import Datetime from '@common-new/format/Datetime';
import CloudProviderIcon from '@components1/common/CloudIcon';
import Tooltip from '@components1/ds/Tooltip';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import CustomTicketLink from '@common-new/CustomTicketLink';
import { hasWriteAccess } from '@lib/auth';
import { formatMemory } from '@lib/formatter';
import ResolveModal from './ResolveModal';
import CliCommandModal from './CliCommandModal';
import WidgetCard from '@components1/ds/WidgetCard';
import { ListingLayout } from '@components1/ds/ListingLayout';
import { Stat } from '@components1/ds/Stat';
import { CostCallout } from '@components1/ds/CostCallout';
import { Chip } from '@components1/ds/Chip';
import CustomSearch from '@common-new/CustomSearch';
import FilterDropdown from '@components1/ds/FilterDropdown';
import { Button } from '@components1/ds/Button';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import RecommendationDetailPanel from './RecommendationDetailPanel';
import { type SeverityLevel } from './SeverityBadge';
import {
  NON_SECURITY_CATEGORIES,
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

const CATEGORY_FILTER_OPTIONS = [
  { label: 'Right Sizing', value: 'RightSizing' },
  { label: 'Infra Upgrade', value: 'InfraUpgrade' },
  { label: 'Config', value: 'Configuration' },
  { label: 'Spot Instance', value: 'K8sSpotRecommendation' },
];

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
  Savings: 'estimated_savings',
  'Last Seen': 'updated_at',
};
const SORT_FIELD_TO_HEADER: Record<SortField, string> = {
  severity: 'Severity',
  estimated_savings: 'Savings',
  updated_at: 'Last Seen',
};

// Column headers for the recommendations table. Sortable columns carry
// `sortEnabled` so CustomTable2 renders the sort affordance.
const TABLE_HEADERS = [
  { name: 'Severity', width: '6%', sortEnabled: true },
  { name: 'Resource', width: '20%' },
  { name: 'Recommendation', width: '22%' },
  { name: 'Category', width: '9%' },
  { name: 'Environment', width: '10%' },
  { name: 'Savings', width: '8%', sortEnabled: true, align: 'left' as const },
  { name: 'Last Seen', width: '14%', sortEnabled: true, align: 'left' as const },
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

// Severity → DS Chip tone for the Severity filter row. We reuse the Chip's
// built-in dot+tone composition (small coloured dot + neutral gray label)
// instead of a SeverityIcon badge, so the chip text reads quietly. Medium
// borrows the `agent` tone purely for its purple dot — the alternative
// (warning) would collide with High.
const SEVERITY_TONE: Record<SeverityLevel, 'critical' | 'warning' | 'agent' | 'info' | 'neutral'> = {
  Critical: 'critical',
  High: 'warning',
  Medium: 'agent',
  Low: 'info',
  Info: 'neutral',
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

const OptimizeNewPage = () => {
  const router = useRouter();
  const routerRef = useRef(router);
  routerRef.current = router;

  const { setAllCluster } = useData();

  // Accounts state: id → { name, cloud_provider }
  const [accounts, setAccounts] = useState<Record<string, { name: string; cloud_provider: string }>>({});

  // Summary state
  const [summaryData, setSummaryData] = useState<SeveritySummaryData[]>([]);
  const [perSeverityRules, setPerSeverityRules] = useState<Record<string, { rule_name: string; count: number }[]>>({});
  const [categoryCounts, setCategoryCounts] = useState<Record<string, { count: number; savings: number }>>({});
  const [summaryLoading, setSummaryLoading] = useState(true);

  // Table state
  const [recommendations, setRecommendations] = useState<any[]>([]);
  const [tableTotal, setTableTotal] = useState(0);
  const [page, setPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(20);
  const [tableLoading, setTableLoading] = useState(true);

  // Sort state
  const [sortField, setSortField] = useState<SortField>('severity');
  const [sortDirection, setSortDirection] = useState<SortDirection>('asc');

  // Filters state — initialised from URL query params
  const [filters, setFilters] = useState<FilterState>(() => ({
    severity: parseQueryArray(router.query.severity) as SeverityLevel[],
    account: parseQueryArray(router.query.account),
    category: parseQueryArray(router.query.category),
    search: (router.query.search as string) || '',
  }));

  // Local search input state — typed value, not yet applied. Mirrors ManualInvestigated pattern.
  const [searchInput, setSearchInput] = useState((router.query.search as string) || '');

  // Top issues bar state
  const [topIssuesActive, setTopIssuesActive] = useState(false);
  const [activeRuleName, setActiveRuleName] = useState<string | null>(null);

  // Detail panel state
  const [selectedRec, setSelectedRec] = useState<any>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailInitialTab, setDetailInitialTab] = useState(0);

  // Direct action modal state
  const [ticketModalRec, setTicketModalRec] = useState<any>(null);
  const [resolveModalRec, setResolveModalRec] = useState<any>(null);
  const [cliModalRec, setCliModalRec] = useState<any>(null);

  // NuBi sidebar state
  const [nubiSidebarVisible, setNubiSidebarVisible] = useState(false);
  const [nubiQuery, setNubiQuery] = useState('');
  const [nubiAccountId, setNubiAccountId] = useState('');
  const [nubiConversationId, setNubiConversationId] = useState('');

  // Page-level loading
  const [pageLoading, setPageLoading] = useState(true);

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
      setActiveRuleName(null);
      updateUrl(newFilters);
    },
    [updateUrl]
  );

  // Fetch accounts
  useEffect(() => {
    apiHome
      .getCloudAccounts()
      .then((res: any) => {
        setAccounts(Object.fromEntries(res.map((v: any) => [v.id, { name: v.account_name, cloud_provider: v.cloud_provider || '' }])));
        const clusters = transformClusters(res);
        setAllCluster(clusters);
      })
      .finally(() => setPageLoading(false));
  }, []);

  // Build filter query for the API
  const buildFilterQuery = useCallback(
    (extraFilters?: Partial<FilterState>) => {
      const merged = { ...filters, ...extraFilters };
      const query: any = {};

      query.category = merged.category.length > 0 ? merged.category : NON_SECURITY_CATEGORIES;
      query.status = DEFAULT_STATUS;

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
    [filters]
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

  // Fetch severity summary — re-fetches when account or category filters change
  useEffect(() => {
    let cancelled = false;
    setSummaryLoading(true);

    const accountId = filters.account.length > 0 ? filters.account : '';
    const activeCategories = filters.category.length > 0 ? filters.category : NON_SECURITY_CATEGORIES;

    const fetchSummary = async () => {
      try {
        const allRows = await recommendationApi.getK8sRecommendationSummaryByRuleName({
          accountId,
          category: activeCategories as any,
          status: DEFAULT_STATUS,
          severity: [...SEVERITY_ORDER],
        });
        if (cancelled) {
          return;
        }

        const rows = Array.isArray(allRows) ? allRows : [];
        const { summaryItems, sevRules } = processSummaryResults(rows);
        setSummaryData(summaryItems);
        setPerSeverityRules(sevRules);

        // Derive category counts from the same response
        const catData: Record<string, { count: number; savings: number }> = {};
        for (const cat of WIDGET_CATEGORIES) {
          const catRows = rows.filter((r: any) => r.category === cat);
          catData[cat] = sumCategoryRows(catRows);
        }
        setCategoryCounts(catData);
      } catch {
        if (!cancelled) {
          snackbar.error('Failed to load recommendation summary. Try refreshing.');
        }
      } finally {
        if (!cancelled) {
          setSummaryLoading(false);
        }
      }
    };

    fetchSummary();
    return () => {
      cancelled = true;
    };
  }, [processSummaryResults, filters.account, filters.category]);

  // Fetch table data — used both by useEffect (with cancellation) and manual refresh calls
  const buildTableQuery = useCallback(() => {
    const apiQuery = buildFilterQuery();
    // When top issues filter is active and no explicit severity selected, default to Critical+High
    if (topIssuesActive && !apiQuery.severity) {
      apiQuery.severity = ['Critical', 'High'];
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

  // Keep the detail drawer in sync when table data refreshes
  useEffect(() => {
    setSelectedRec((prev: any) => {
      if (!prev || !detailOpen) return prev;
      return recommendations.find((r: any) => r.id === prev.id) ?? prev;
    });
  }, [recommendations, detailOpen]);

  // CSV export — replaces the legacy DownloadButton DOM-scraping path.
  // Built directly from the in-memory recommendation rows so it stays decoupled
  // from the table's render markup.
  const handleDownloadCsv = useCallback(() => {
    const escape = (v: unknown) => {
      const str = v == null ? '' : String(v);
      return `"${str.replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    };
    const headers = ['Severity', 'Resource', 'Recommendation', 'Category', 'Environment', 'Savings ($/mo)', 'Last Seen'];
    const rows = recommendations.map((rec: any) => {
      const accountInfo = accounts[rec.account_id];
      return [
        rec.severity || '',
        getResourceDisplayName(rec, ''),
        formatRuleName(rec.rule_name || ''),
        rec.category || '',
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
    const targetSeverities: string[] = filters.severity.length > 0 ? filters.severity : ['Critical', 'High'];

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

    const severityLabel = filters.severity.length > 0 ? filters.severity.join(' + ') : 'Critical + High';

    return { items, total, severityLabel };
  }, [perSeverityRules, filters.severity]);

  // ─── Computed: Summary widget totals ───

  // Computed: Account filter options from loaded accounts.
  // FilterDropdown supports grouped options, but we surface the cloud
  // provider as a label prefix so the trigger summary stays one-line.
  const accountFilterOptions = useMemo(
    () =>
      Object.entries(accounts).map(([id, info]) => {
        const provider = (info.cloud_provider || '').toUpperCase();
        const name = info.name || id;
        return { label: provider ? `${provider} · ${name}` : name, value: id };
      }),
    [accounts]
  );

  const totalCount = useMemo(() => summaryData.reduce((sum, s) => sum + s.count, 0), [summaryData]);
  const totalSavings = useMemo(() => summaryData.reduce((sum, s) => sum + s.savings, 0), [summaryData]);
  const criticalCount = useMemo(() => summaryData.find((s) => s.severity === 'Critical')?.count || 0, [summaryData]);
  const highCount = useMemo(() => summaryData.find((s) => s.severity === 'High')?.count || 0, [summaryData]);

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
          updatedAt: rec.updated_at || rec.created_at || '',
          ticketId: rec.ticket?.ticket_id || '',
          ticketUrl: rec.ticket?.url || '',
        };
      }),
    [recommendations, accounts]
  );

  // ─── Handlers ───

  const handleWidgetCategoryClick = useCallback(
    (category: string | null) => {
      const newFilters = {
        ...filters,
        category: category ? [category] : [],
      };
      handleFiltersChange(newFilters);
    },
    [filters, handleFiltersChange]
  );

  const handleSeverityClick = useCallback(
    (severity: SeverityLevel | null) => {
      const newFilters = {
        ...filters,
        severity: severity ? [severity] : [],
      };
      handleFiltersChange(newFilters);
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

  // Reused by both the row action menu and the detail panel.
  const askNubiAboutRec = useCallback(
    (rec: any) => {
      const accountInfo = accounts[rec.account_id];
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
    [accounts]
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

        const showResolve = row.category === 'RightSizing' && row.rec.rule_name === 'pod_right_sizing' && hasWriteAccess(row.rec.account_id);
        const showCopyCli = row.category === 'RightSizing' && row.rec.rule_name === 'pod_right_sizing';

        // Items behind the kebab — everything that isn't surfaced inline.
        const menuItems: Array<{ label: string; icon: React.ReactNode; onSelect: () => void; disabled?: boolean; id?: string }> = [];
        menuItems.push({
          id: `action-ticket-${row.id}`,
          label: row.ticketId ? `Ticket: ${row.ticketId}` : 'Create ticket',
          icon: <ConfirmationNumberOutlinedIcon sx={{ fontSize: 16 }} />,
          onSelect: () => setTicketModalRec(row.rec),
          disabled: !!row.ticketId,
        });
        if (showCopyCli) {
          menuItems.push({
            id: `action-copy-cli-${row.id}`,
            label: 'Copy CLI command',
            icon: <ContentCopyOutlinedIcon sx={{ fontSize: 16 }} />,
            onSelect: () => setCliModalRec(row.rec),
          });
        }

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
                <Box component='span' sx={{ color: ds.gray[700], fontWeight: ds.weight.medium }}>
                  {row.resourceName}
                </Box>
                {(providerLabel || row.resourceType) && (
                  <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], color: ds.gray[500] }}>
                    {providerLabel && (
                      <>
                        <CloudProviderIcon cloud_provider={providerSlug} height='14px' width='14px' />
                        <Box component='span'>{providerLabel}</Box>
                      </>
                    )}
                    {providerLabel && row.resourceType && (
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
              <Box sx={{ display: 'flex', flexDirection: 'column', gap: 'var(--ds-space-1)' }}>
                <Box component='span' sx={{ color: ds.gray[700], fontWeight: ds.weight.medium }}>
                  {row.ruleName}
                </Box>
                {row.brief && (
                  <Tooltip title={row.brief} placement='top' enterDelay={400}>
                    <Box
                      component='span'
                      sx={{
                        display: 'inline-block',
                        maxWidth: ds.space.mul(0, 130),
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        color: ds.gray[500],
                      }}
                    >
                      {row.brief}
                    </Box>
                  </Tooltip>
                )}
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
          // Environment
          {
            component: (
              <Box sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
                {row.accountCloudProvider && <CloudProviderIcon cloud_provider={row.accountCloudProvider} height='16px' width='16px' />}
                <Box component='span' sx={{ fontSize: ds.text.caption, color: ds.gray[700] }}>
                  {row.accountName || '—'}
                </Box>
              </Box>
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
                    fontSize: ds.text.small,
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
              <Box
                onClick={(e) => e.stopPropagation()}
                sx={{ display: 'inline-flex', alignItems: 'center', gap: ds.space[1], justifyContent: 'flex-end' }}
              >
                {showResolve && (
                  <Tooltip title='Optimize' placement='top'>
                    <span>
                      <Button
                        tone='ghost'
                        size='xs'
                        composition='icon-only'
                        icon={<SafeIcon src={OptimizeIcon} alt='' width={16} height={16} />}
                        aria-label='Optimize'
                        id={`action-resolve-${row.id}`}
                        onClick={() => setResolveModalRec(row.rec)}
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
                      id={`action-ask-nubi-${row.id}`}
                      onClick={() => askNubiAboutRec(row.rec)}
                    />
                  </span>
                </Tooltip>
                <DropdownMenu
                  align='end'
                  size='sm'
                  items={menuItems}
                  trigger={
                    <Button
                      tone='ghost'
                      size='xs'
                      composition='icon-only'
                      icon={<MoreVertIcon />}
                      aria-label='More actions'
                      id={`action-menu-${row.id}`}
                    />
                  }
                />
              </Box>
            ),
          },
        ];
      }),
    [tableRows, assistantName, askNubiAboutRec]
  );

  // Show page-level loader on initial load
  if (pageLoading && summaryLoading) {
    return (
      <Box sx={{ display: 'flex', justifyContent: 'center', alignItems: 'center', minHeight: '60vh' }}>
        <Loader style={{ height: '400px', width: 'auto' }} />
      </Box>
    );
  }

  return (
    <Box sx={{ p: '0px' }} data-testid='optimize-new-page'>
      {/* Summary widgets */}
      <Box sx={{ display: 'flex', gap: ds.space[3], mt: ds.space[4] }}>
        <WidgetCard
          sx={{
            flex: 1,
            minWidth: 0,
            mt: 0,
            padding: `${ds.space[3]} ${ds.space[4]}`,
            cursor: 'pointer',
          }}
          onClick={() => handleWidgetCategoryClick(null)}
        >
          <Stat
            size='md'
            label='Total Recommendations'
            info={{ tooltip: 'Total number of active optimization recommendations across all categories' }}
            value={
              summaryLoading ? (
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
          const isActive = filters.category.length === 1 && filters.category[0] === cat;
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
                cursor: 'pointer',
                borderColor: isActive ? ds.blue[600] : undefined,
                transition: `border-color ${ds.motion.micro} ${ds.motion.ease}`,
              }}
              onClick={() => handleWidgetCategoryClick(isActive ? null : cat)}
            >
              <Stat
                size='md'
                label={WIDGET_CATEGORY_LABELS[cat]}
                info={{ tooltip: WIDGET_CATEGORY_TOOLTIPS[cat] }}
                value={
                  summaryLoading ? (
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
            label='Total Savings'
            info={{ tooltip: 'Total estimated monthly savings if all recommendations are applied' }}
            value={summaryLoading ? '…' : <CostCallout size='md' tone='high-savings' value={totalSavings} period='/ mo' />}
          />
        </WidgetCard>
      </Box>

      <ListingLayout id='optimize-recommendations' sx={{ mt: ds.space[4] }}>
        <ListingLayout.Toolbar
          sx={{ borderBottom: `1px solid ${ds.gray[200]}`, padding: `${ds.space[3]} ${ds.space[4]}` }}
          actions={
            <>
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
            options={accountFilterOptions}
            value={accountFilterOptions.filter((o) => filters.account.includes(o.value))}
            onSelect={(_e: any, items: any) => {
              const next = (Array.isArray(items) ? items : []).map((it: any) => it.value);
              handleFiltersChange({ ...filters, account: next });
            }}
          />
          <FilterDropdown
            id='optimize-category-filter'
            label='Category'
            multiple
            options={CATEGORY_FILTER_OPTIONS}
            value={CATEGORY_FILTER_OPTIONS.filter((o) => filters.category.includes(o.value))}
            onSelect={(_e: any, items: any) => {
              const next = (Array.isArray(items) ? items : []).map((it: any) => it.value);
              handleFiltersChange({ ...filters, category: next });
            }}
          />
        </ListingLayout.Toolbar>

        {/* Severity row */}
        <Box
          sx={{
            display: 'flex',
            alignItems: 'center',
            gap: ds.space[2],
            padding: `${ds.space[3]} ${ds.space[4]}`,
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
          {summaryLoading
            ? [1, 2, 3, 4, 5].map((i) => <Skeleton key={i} shape='rect' width={88} height={20} />)
            : summaryData.map((item) => {
                const isActive = (filters.severity.length === 1 ? filters.severity[0] : null) === item.severity;
                return (
                  <Chip
                    key={item.severity}
                    size='sm'
                    pressed={isActive}
                    onClick={() => handleSeverityClick(isActive ? null : item.severity)}
                    dot
                    tone={SEVERITY_TONE[item.severity]}
                    count={item.count}
                    data-testid={`severity-chip-${item.severity.toLowerCase()}`}
                  >
                    {item.severity}
                  </Chip>
                );
              })}
        </Box>

        {/* Top issues row — separate band so the eye reads severity first,
            then "of those, here are the top rule names". */}
        {!summaryLoading && topIssueData.items.length > 0 && (
          <Box
            sx={{
              display: 'flex',
              alignItems: 'center',
              gap: ds.space[2],
              padding: `${ds.space[3]} ${ds.space[4]}`,
              borderBottom: `1px solid ${ds.gray[200]}`,
              flexWrap: 'wrap',
            }}
            data-testid='top-issues-bar'
          >
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
            <Chip size='sm' pressed={topIssuesActive && !activeRuleName} onClick={handleToggleTopIssues} count={topIssueData.total}>
              All
            </Chip>
            {topIssueData.items.map((item) => {
              const isActive = topIssuesActive && activeRuleName === item.rule_name;
              return (
                <Chip key={item.rule_name} size='sm' pressed={isActive} onClick={() => handleRuleClick(item.rule_name)} count={item.count}>
                  {formatRuleName(item.rule_name)}
                </Chip>
              );
            })}
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
          onSuccess={() => {
            setTicketModalRec(null);
            snackbar.success('Ticket created successfully');
            fetchTableData();
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
          onSuccess={() => {
            setResolveModalRec(null);
            snackbar.success('Recommendation applied successfully');
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
