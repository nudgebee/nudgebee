import { Box, Typography } from '@mui/material';
import { useEffect, useState, useCallback, useRef, useMemo } from 'react';
import NubiChatSidebar from '@components1/common/NubiChatSidebar';
import { buildNubiOptimizePrompt } from 'src/utils/nubiPromptBuilder';
import { useRouter } from 'next/router';
import { colors } from 'src/utils/colors';
import { useData } from '@context/DataContext';
import apiHome from '@api1/home';
import { transformClusters } from '@components1/common/UpdateDataContext';
import recommendationApi from '@api1/recommendation';
import Loader from '@components1/common/Loader';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { snackbar } from '@components1/common/snackbarService';
import CustomTable2 from '@components1/common/tables/CustomTable2';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Currency from '@components1/common/format/Currency';
import Datetime from '@components1/common/format/Datetime';
import CloudProviderIcon from '@components1/common/CloudIcon';
import Text from '@components1/common/format/Text';
import CustomTooltip from '@components1/common/CustomTooltip';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { hasWriteAccess } from '@lib/auth';
import { formatMemory } from '@lib/formatter';
import ResolveModal from './ResolveModal';
import CliCommandModal from './CliCommandModal';
import SummaryWidget from '@components1/optimise/SummaryWidget';
import SeveritySummaryBar, { type SeveritySummaryData } from './SeveritySummaryBar';
import TopIssuesBar from './TopIssuesBar';
import RecommendationDetailPanel from './RecommendationDetailPanel';
import ActionButtons from './ActionButtons';
import CategoryChip from './CategoryChip';
import { type SeverityLevel } from './SeverityBadge';
import {
  NON_SECURITY_CATEGORIES,
  DEFAULT_STATUS,
  formatRuleName,
  getRecommendationBrief,
  getResourceDisplayName,
  safeParseJSON,
  type SortField,
  type SortDirection,
} from './utils';

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

// Sort field ↔ header name mapping
const SORT_FIELD_TO_HEADER: Record<SortField, string> = {
  severity: 'Severity',
  estimated_savings: 'Savings',
  updated_at: 'Last Seen',
};
const HEADER_TO_SORT_FIELD: Record<string, SortField> = {
  Severity: 'severity',
  Savings: 'estimated_savings',
  'Last Seen': 'updated_at',
};

// Table headers matching the screenshot columns
const TABLE_HEADERS = [
  { name: 'Severity', width: '6%', sortEnabled: true },
  { name: 'Resource', width: '20%' },
  { name: 'Recommendation', width: '22%' },
  { name: 'Category', width: '9%' },
  { name: 'Environment', width: '10%' },
  { name: 'Savings', width: '8%', sortEnabled: true },
  { name: 'Last Seen', width: '8%', sortEnabled: true },
  { name: 'Actions', width: '10%' },
];

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

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

  const tableId = 'optimize-recommendations-table';

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

    r.replace({ pathname: r.pathname, query }, undefined, { shallow: true });
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
      } catch (err) {
        console.error('Failed to fetch severity summary:', err);
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
      } catch (err) {
        console.error('Failed to fetch recommendations:', err);
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
    } catch (err) {
      console.error('Failed to fetch recommendations:', err);
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

  // Computed: Account filter options from loaded accounts
  const accountFilterOptions = useMemo(
    () => Object.entries(accounts).map(([id, info]) => ({ label: info.name || id, value: id, group: info.cloud_provider || 'Other' })),
    [accounts]
  );

  const totalCount = useMemo(() => summaryData.reduce((sum, s) => sum + s.count, 0), [summaryData]);
  const totalSavings = useMemo(() => summaryData.reduce((sum, s) => sum + s.savings, 0), [summaryData]);
  const criticalCount = useMemo(() => summaryData.find((s) => s.severity === 'Critical')?.count || 0, [summaryData]);
  const highCount = useMemo(() => summaryData.find((s) => s.severity === 'High')?.count || 0, [summaryData]);

  // ─── Computed: Table data for CustomTable2 ───

  const tableData = useMemo(
    () =>
      recommendations.map((rec: any) => {
        const resourceName = getResourceDisplayName(rec);
        const resourceType = rec.cloud_resourse?.type || '';
        const cloudService = rec.resource_cloud_service || '';
        const severity = rec.severity || 'Info';
        const category = rec.category || '';
        const savings = rec.estimated_savings || 0;
        const ruleName = formatRuleName(rec.rule_name || '');
        const brief = getRecommendationBrief(rec);
        const accountInfo = accounts[rec.account_id];
        const accountName = accountInfo?.name || '';
        const accountCloudProvider = accountInfo?.cloud_provider || '';
        return [
          // Severity
          {
            component: <SeverityIcon severityType={severity} size={38} />,
            drilldownQuery: { rec },
            data: severity,
          },
          // Resource
          {
            component: (
              <Box>
                <Text showAutoEllipsis value={resourceName} />
                {(cloudService || resourceType) && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px', mt: '2px' }}>
                    {cloudService && (
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '3px' }}>
                        <CloudProviderIcon
                          cloud_provider={accountCloudProvider || (cloudService === 'kubernetes' ? 'K8S' : cloudService.toUpperCase())}
                          height='14px'
                          width='14px'
                        />
                        <Typography sx={{ fontSize: '11px', color: colors.text.quaternary }}>
                          {cloudService === 'kubernetes' ? 'K8s' : cloudService.toUpperCase()}
                        </Typography>
                      </Box>
                    )}
                    {cloudService && resourceType && (
                      <Box component='span' sx={{ mx: '2px', color: colors.text.disabled, fontSize: '11px' }}>
                        |
                      </Box>
                    )}
                    {resourceType && (
                      <Typography component='span' sx={{ fontSize: '11px', color: colors.text.quaternary }}>
                        {resourceType}
                      </Typography>
                    )}
                  </Box>
                )}
                {rec.ticket?.ticket_id && (
                  <Box sx={{ mt: '2px' }}>
                    <CustomTicketLink ticketURL={rec.ticket?.url} ticketID={rec.ticket?.ticket_id} />
                  </Box>
                )}
              </Box>
            ),
          },
          // Recommendation
          {
            component: (
              <Box>
                <Typography
                  sx={{
                    fontSize: '13px',
                    color: colors.text.secondary,
                    overflow: 'hidden',
                    textOverflow: 'ellipsis',
                    whiteSpace: 'nowrap',
                    maxWidth: '260px',
                  }}
                >
                  {ruleName}
                </Typography>
                {brief && (
                  <CustomTooltip title={brief} placement='top' enterDelay={400}>
                    <Typography
                      sx={{
                        fontSize: '11px',
                        color: colors.text.quaternary,
                        mt: '1px',
                        overflow: 'hidden',
                        textOverflow: 'ellipsis',
                        whiteSpace: 'nowrap',
                        maxWidth: '260px',
                      }}
                    >
                      {brief}
                    </Typography>
                  </CustomTooltip>
                )}
              </Box>
            ),
          },
          // Category
          {
            component: <CategoryChip category={category} />,
          },
          // Environment
          {
            component: (
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                {accountCloudProvider && <CloudProviderIcon cloud_provider={accountCloudProvider} height='16px' width='16px' />}
                <Typography sx={{ fontSize: '12px', color: colors.text.secondary }}>{accountName || '—'}</Typography>
              </Box>
            ),
          },
          // Savings
          {
            component:
              savings !== 0 ? (
                <Currency
                  value={Math.abs(savings)}
                  precison={0}
                  withTooltip={false}
                  prefix={savings < 0 ? '-$' : '$'}
                  suffix='/mo'
                  sx={{
                    fontSize: '13px',
                    color: savings > 0 ? colors.text.currency : colors.error,
                  }}
                  sxSuffix={{
                    fontSize: '11px',
                    fontWeight: 400,
                    color: colors.text.quaternary,
                  }}
                />
              ) : (
                <Typography sx={{ fontSize: '12px', color: colors.text.disabled }}>-</Typography>
              ),
          },
          // Last Seen
          {
            component: <Datetime value={rec.updated_at || rec.created_at} />,
          },
          // Actions
          {
            component: (
              <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
                <ActionButtons
                  recommendationId={rec.id}
                  existingTicketId={rec.ticket?.ticket_id}
                  onDismiss={() => snackbar.info('Dismiss is not yet implemented')}
                  onCreateTicket={() => setTicketModalRec(rec)}
                  showResolve={rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing' && hasWriteAccess(rec.account_id)}
                  onResolve={() => setResolveModalRec(rec)}
                  showCopyCli={rec.category === 'RightSizing' && rec.rule_name === 'pod_right_sizing'}
                  onCopyCli={() => setCliModalRec(rec)}
                  onAskNubi={() => {
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
                  }}
                />
              </Box>
            ),
          },
        ];
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

  // CustomTable2 sort handler
  const handleCustomTableSort = useCallback((sort: { name: string; order: string }) => {
    const field = HEADER_TO_SORT_FIELD[sort.name];
    if (field) {
      setSortField(field);
      setSortDirection(sort.order as SortDirection);
      setPage(0);
    }
  }, []);

  // CustomTable2 pagination handler (1-based pages)
  const handleTablePageChange = useCallback(
    (newPage: number, newLimit: number) => {
      if (newLimit !== rowsPerPage) {
        setRowsPerPage(newLimit);
        setPage(0);
      } else {
        setPage(newPage - 1);
      }
    },
    [rowsPerPage]
  );

  // CustomTable2 row click handler
  const handleTableRowClick = useCallback((query: any) => {
    if (query?.rec) {
      handleRowClick(query.rec);
    }
  }, []);

  // Current sort as CustomTable2 format
  const sortObj = useMemo(() => ({ name: SORT_FIELD_TO_HEADER[sortField], order: sortDirection }), [sortField, sortDirection]);

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
      <Box sx={{ display: 'flex', gap: '12px', mt: '16px' }}>
        <SummaryWidget
          title='Total Recommendations'
          size='small'
          sx={{ flex: 1, minWidth: 0 }}
          showInfoIcon
          tooltipContent='Total number of active optimization recommendations across all categories'
          onClick={() => handleWidgetCategoryClick(null)}
          value={
            summaryLoading ? (
              '...'
            ) : (
              <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '6px' }}>
                <Typography sx={{ fontSize: '20px', fontWeight: 600, lineHeight: '22px', color: colors.text.secondary }}>
                  {totalCount.toLocaleString()}
                </Typography>
                {(criticalCount > 0 || highCount > 0) && (
                  <Box sx={{ display: 'flex', alignItems: 'center', gap: '4px' }}>
                    {criticalCount > 0 && (
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '2px' }}>
                        <Box sx={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: colors.error }} />
                        <Typography sx={{ fontSize: '11px', color: colors.error, fontWeight: 500 }}>{criticalCount}</Typography>
                      </Box>
                    )}
                    {highCount > 0 && (
                      <Box sx={{ display: 'flex', alignItems: 'center', gap: '2px' }}>
                        <Box sx={{ width: 6, height: 6, borderRadius: '50%', backgroundColor: '#F97316' }} />
                        <Typography sx={{ fontSize: '11px', color: '#F97316', fontWeight: 500 }}>{highCount}</Typography>
                      </Box>
                    )}
                  </Box>
                )}
              </Box>
            )
          }
        />
        {WIDGET_CATEGORIES.map((cat) => {
          const isActive = filters.category.length === 1 && filters.category[0] === cat;
          return (
            <SummaryWidget
              key={cat}
              title={WIDGET_CATEGORY_LABELS[cat]}
              size='small'
              sx={{
                flex: 1,
                minWidth: 0,
                ...(isActive && { borderColor: colors.primary, boxShadow: `0px 2px 10px 0px ${colors.primary}33` }),
              }}
              showInfoIcon
              tooltipContent={WIDGET_CATEGORY_TOOLTIPS[cat]}
              onClick={() => handleWidgetCategoryClick(isActive ? null : cat)}
              value={
                summaryLoading ? (
                  '...'
                ) : (
                  <Box sx={{ display: 'flex', alignItems: 'baseline', gap: '6px' }}>
                    <Typography sx={{ fontSize: '20px', fontWeight: 600, lineHeight: '22px', color: colors.text.secondary }}>
                      {(categoryCounts[cat]?.count || 0).toLocaleString()}
                    </Typography>
                    {(categoryCounts[cat]?.savings || 0) > 0 && (
                      <Currency
                        value={categoryCounts[cat].savings}
                        suffix='/mo'
                        precison={0}
                        withTooltip={false}
                        sx={{ fontSize: '12px', color: colors.text.quaternary }}
                      />
                    )}
                  </Box>
                )
              }
            />
          );
        })}
        <SummaryWidget
          title='Total Savings'
          variant='savings'
          size='small'
          sx={{ flex: 1, minWidth: 0 }}
          showInfoIcon
          tooltipContent='Total estimated monthly savings if all recommendations are applied'
          value={
            summaryLoading ? (
              '...'
            ) : (
              <Currency
                value={totalSavings}
                suffix='/mo'
                precison={0}
                withTooltip={false}
                sx={{ fontSize: '20px', lineHeight: '22px', fontWeight: 600 }}
              />
            )
          }
        />
      </Box>

      <BoxLayout2
        id='optimize-recommendations'
        showBorder={true}
        marginTop='16px'
        marginBottom='0px'
        filterOptions={[
          {
            type: 'search',
            label: 'Search resource...',
            value: filters.search,
            onSelect: (e: any) => handleFiltersChange({ ...filters, search: e.target.value }),
            onClear: () => handleFiltersChange({ ...filters, search: '' }),
            minWidth: '260px',
            maxWidth: '260px',
          },
          {
            type: 'multi-dropdown',
            label: 'Account',
            options: accountFilterOptions,
            value: accountFilterOptions.filter((o: any) => filters.account.includes(o.value)),
            grouped: true,
            groupIcon: renderAccountGroupIcon,
            onSelect: (_e: any, selected: any) => {
              const values = (selected || []).map((s: any) => s.value);
              handleFiltersChange({ ...filters, account: values });
            },
          },
          {
            type: 'multi-dropdown',
            label: 'Category',
            options: CATEGORY_FILTER_OPTIONS,
            value: CATEGORY_FILTER_OPTIONS.filter((o) => filters.category.includes(o.value)),
            onSelect: (_e: any, selected: any) => {
              const values = (selected || []).map((s: any) => s.value);
              handleFiltersChange({ ...filters, category: values });
            },
          },
        ]}
        sharingOptions={{
          sharing: { enabled: true, onClick: null },
          download: {
            enabled: true,
            onClick: () => ({ tableId: tableId }),
          },
        }}
      >
        {/* Severity chips with counts */}
        <SeveritySummaryBar
          data={summaryData}
          loading={summaryLoading}
          onSeverityClick={handleSeverityClick}
          activeSeverity={filters.severity.length === 1 ? filters.severity[0] : null}
        />

        {/* Top 5 issues bar */}
        <TopIssuesBar
          items={topIssueData.items}
          totalCount={topIssueData.total}
          severityLabel={topIssueData.severityLabel}
          activeRuleName={activeRuleName}
          topIssuesActive={topIssuesActive}
          onRuleClick={handleRuleClick}
          onToggleTopIssues={handleToggleTopIssues}
          loading={summaryLoading}
        />

        {/* Recommendations table */}
        <CustomTable2
          id={tableId}
          tableData={tableData}
          headers={TABLE_HEADERS}
          loading={tableLoading}
          rowsPerPage={rowsPerPage}
          totalRows={tableTotal}
          pageNumber={page + 1}
          onPageChange={handleTablePageChange}
          sort={sortObj}
          onSortChange={handleCustomTableSort}
          onRowClick={handleTableRowClick}
          showUpdatedTable
          showUpdatedEmptyData
        />
      </BoxLayout2>

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
        onDismiss={() => snackbar.info('Dismiss is not yet implemented')}
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
