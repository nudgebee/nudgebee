import React, { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { useRouter } from 'next/router';
import SafeIcon from '@components1/common/SafeIcon';
import CloudProviderIcon from '@components1/common/CloudIcon';

// Components
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Datetime from '@components1/common/format/Datetime';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import NBStatusBadge from '@components1/common/widgets/NBStatusBadge';
import { Text } from '@components1/common';
import CustomTooltip from '@components1/common/CustomTooltip';

// APIs & Utils
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import ticketsApi from '@api1/tickets';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import { applyFiltersOnRouter } from '@lib/router';
import { convertToReadableFormat, titleCaseForAggregationKey } from 'src/utils/common';
import { Box, IconButton, Tooltip, Menu, MenuItem, ListItemText, Typography } from '@mui/material';
import InvestigateButton from '@components1/common/InvestigateButton';
import ScoreDisplay from '@components1/common/widgets/ScoreDisplay';
import NewIssueChip from '@components1/common/widgets/NewIssueChip';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';
import EventClassifyModal from '@components1/events/EventClassifyModal';
import { CLASSIFICATION_OPTIONS, getTriageStatusTooltip } from '@api1/triage';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { action } from 'src/utils/actionStyles';
import { snackbar } from '@components1/common/snackbarService';
import CategoryOutlinedIcon from '@mui/icons-material/CategoryOutlined';
import { infoIcon, TicketsIcon } from '@assets';
import WorkflowIcon from '@assets/WorkflowIcon';

// --- Types & Interfaces ---
interface KubernetesGroupedEventsTableProps {
  accountId?: string;
  groupEventType: string;
  isTroubleshootPage: boolean;
}

interface DateRange {
  startDate: number;
  endDate: number;
  key: string;
}

// --- Helper Functions (Pure Logic) ---

// NB Status display helper
const getNBStatusDisplay = (nbStatus: string) => {
  const statusMap: Record<string, { label: string; variant: string }> = {
    OPEN: { label: 'Open', variant: 'blue' },
    ACTION_REQUIRED: { label: 'Action Required', variant: 'red' },
    ACKNOWLEDGED: { label: 'Acknowledged', variant: 'purple' }, // Deprecated, kept for backwards compatibility
    INVESTIGATING: { label: 'Investigating', variant: 'yellow' }, // Deprecated, kept for backwards compatibility
    SNOOZED: { label: 'Snoozed', variant: 'grey' },
    SUPPRESSED: { label: 'Suppressed', variant: 'grey' },
    DROPPED: { label: 'Dropped', variant: 'grey' },
    DUPLICATE: { label: 'Duplicate', variant: 'grey' },
    RESOLVED: { label: 'Resolved', variant: 'green' },
  };
  return statusMap[nbStatus] || { label: nbStatus || '-', variant: '' };
};

// Menu items for ThreeDotsMenu — disableTicket flips when a ticket already exists for this fingerprint
const getMenuItems = (disableTicket: boolean) => [
  {
    icon: TicketsIcon,
    label: 'Create Ticket',
    id: 0,
    disabled: disableTicket,
    iconBlack: true,
  },
  {
    icon: WorkflowIcon,
    label: 'Create Automation',
    id: 1,
    iconBlack: true,
  },
];

/**
 * Transforms raw API data into Table Row format.
 * Extracted to prevent recreation on every render.
 */
const transformTableData = (
  eventGroupings: any[],
  groupEventType: string,
  isTroubleshootPage: boolean,
  accounts: any[],
  accountType: string,
  dateRange: DateRange,
  onMenuClick: (menuItem: any, data: any) => void,
  onStatusChange?: () => void,
  onCreateTicket?: (item: any) => void,
  onClassify?: (event: React.MouseEvent<HTMLElement>, item: any) => void,
  ticketMap?: Map<string, any>
) => {
  if (!eventGroupings || !Array.isArray(eventGroupings)) {
    return [];
  }

  return eventGroupings.map((item) => {
    // Determine Severity
    let severity = 'INFO';
    if (item.distinct_priority?.includes('HIGH')) {
      severity = 'HIGH';
    } else if (item.distinct_priority?.includes('MEDIUM')) {
      severity = 'MEDIUM';
    } else if (item.distinct_priority?.includes('LOW')) {
      severity = 'LOW';
    } else if (item.distinct_priority?.includes('DEBUG')) {
      severity = 'DEBUG';
    }

    // Determine Status
    let status = 'OPEN';
    if (item.distinct_status?.includes('FIRING')) {
      status = 'FIRING';
    } else if (item.distinct_status?.includes('CLOSED')) {
      status = 'CLOSED';
    }

    // Resolve account name for the Alert cell
    const account = isTroubleshootPage ? accounts.find((acc: any) => (acc.id || acc.value) === item.account_id) : null;
    const accountName = account?.label || account?.account_name || item.account_id;
    const cloudProvider = account?.cloud_provider || accountType;
    const namespaceLabel = cloudProvider && cloudProvider !== 'K8s' ? 'service' : 'ns';

    // Common Drilldown Props
    const commonDrilldown = {
      fingerprint: [item.fingerprint],
      finding_type: '',
      startTime: dateRange.startDate,
      endTime: dateRange.endDate,
      accountId: item.account_id,
    };

    const existingTicket = ticketMap?.get(item.fingerprint);
    const hasExistingTicket = Boolean(existingTicket);

    // Columns based on type
    if (groupEventType === 'fingerprint') {
      return [
        {
          component: (
            <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0px' }}>
              <SeverityIcon severityType={severity} />
              <Datetime value={item.max_created_at} sx={{ fontSize: '11px' }} />
            </Box>
          ),
          data: severity,
        },
        {
          component: (
            <Box>
              <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                <Text showAutoEllipsis value={item.subject_name} style={{ fontWeight: 500 }} />
                {item.is_new_issue && <NewIssueChip firstSeenAt={item.fingerprint_first_seen_at} />}
              </Box>
              {isTroubleshootPage && <Text value={`acc: ${accountName}`} secondaryText showAutoEllipsis />}
              {item.subject_namespace && <Text value={`${namespaceLabel}: ${item.subject_namespace}`} secondaryText showAutoEllipsis />}
              {hasExistingTicket && <CustomTicketLink ticketURL={existingTicket?.url} ticketID={existingTicket?.ticket_id} />}
            </Box>
          ),
          drilldownQuery: commonDrilldown,
        },
        { component: <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} /> },
        { component: <Text showAutoEllipsis value={item.fingerprint_event_count ?? item.event_count} /> },
        {
          component: (
            <ScoreDisplay
              score={item.latest_computed_score}
              priority={item.latest_computed_priority}
              scoreFactors={item.latest_score_factors}
              confidence={item.latest_score_confidence}
            />
          ),
        },
        {
          component: (
            <CustomTooltip variant='default' title={getTriageStatusTooltip(item.latest_nb_status)} placement='top'>
              <Box>
                <NBStatusBadge
                  eventId={item.latest_event_id}
                  currentStatus={item.latest_nb_status}
                  onStatusChange={onStatusChange}
                  onCreateTicket={() => onCreateTicket?.(item)}
                  disableTooltip
                />
              </Box>
            </CustomTooltip>
          ),
        },
        {
          component: (
            <CustomLabels
              margin='auto'
              text={convertToReadableFormat(status)}
              variant={status === 'FIRING' ? 'red' : status === 'CLOSED' ? 'grey' : ''}
            />
          ),
        },
        {
          component: (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'4px'} justifyContent={'flex-end'}>
              <InvestigateButton displayText url={`/investigate?id=${item.latest_event_id}&accountId=${item.account_id}`} />
              <Tooltip title='Classify'>
                <IconButton
                  onClick={(e) => {
                    e.stopPropagation();
                    onClassify?.(e, item);
                  }}
                  size='small'
                  sx={{ ...action.primary, padding: '4px' }}
                >
                  <CategoryOutlinedIcon sx={{ fontSize: 16 }} />
                </IconButton>
              </Tooltip>
              <ThreeDotsMenu sx={{ ...action.primary }} menuItems={getMenuItems(hasExistingTicket)} data={item} onMenuClick={onMenuClick} />
            </Box>
          ),
        },
      ];
    }

    // Event Type Columns
    const eventTypeNbStatus = getNBStatusDisplay(item.latest_nb_status);
    const eventTypeDrilldown = {
      aggregation_key: item.aggregation_key,
      finding_type: '',
      startTime: dateRange.startDate,
      endTime: dateRange.endDate,
      accountId: item.account_id,
    };
    return [
      {
        component: <SeverityIcon severityType={severity} />,
        data: severity,
      },
      {
        component: (
          <Box>
            <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} style={{ fontWeight: 500 }} />
            {isTroubleshootPage && <Text value={`acc: ${accountName}`} secondaryText showAutoEllipsis />}
          </Box>
        ),
        drilldownQuery: eventTypeDrilldown,
      },
      { component: <Datetime baseDate={new Date()} value={item.max_created_at} /> },
      { component: <Text showAutoEllipsis value={item.event_count} /> },
      { component: <Text value={item.count_subject_name ?? '-'} /> },
      {
        component: (
          <CustomTooltip variant='default' title={getTriageStatusTooltip(item.latest_nb_status)} placement='top'>
            <Box>
              <CustomLabels margin='auto' text={eventTypeNbStatus.label} variant={eventTypeNbStatus.variant} />
            </Box>
          </CustomTooltip>
        ),
      },
      {},
    ];
  });
};

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const KubernetesGroupedEventsTable: React.FC<KubernetesGroupedEventsTableProps> = ({
  accountId = '',
  groupEventType = 'fingerprint',
  isTroubleshootPage = false,
}) => {
  const componentId = 'Grouped Events';
  const router = useRouter();

  // --- State Management ---
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [totalRows, setTotalRows] = useState<number>(0);
  const [rawEventGroupings, setRawEventGroupings] = useState<any[]>([]);
  const [ticketReferenceMap, setTicketReferenceMap] = useState<Map<string, any>>(new Map());
  const [loading, setLoading] = useState<boolean>(false);
  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);

  // Filters
  const [selectedDateRange, setSelectedDateRange] = useState<DateRange>({
    startDate: new Date().getTime() - 60 * 60 * 24 * 1000,
    endDate: new Date().getTime(),
    key: 'selection',
  });

  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace ?? router?.query?.eventNamespace ?? null);
  const [selectedWorkload, setSelectedWorkload] = useState(router?.query?.eventSubjectName ?? '');
  const [selectedAggregationKey, setSelectedAggregationKey] = useState<any[]>([]);
  const [selectedAccountId, setSelectedAccountId] = useState(accountId || router.query.accountId);
  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [selectedPriority, setSelectedPriority] = useState('');

  const [selectedSource, setSelectedSource] = useState<any[]>([]);
  const [selectedNBStatus, setSelectedNBStatus] = useState<Array<{ label: string; value: string }>>([{ value: 'OPEN', label: 'Open' }]);
  const [selectedIssueType, setSelectedIssueType] = useState<string>((router?.query?.issueType as string) || 'all');

  const { accounts, accountType, workloadFilter, namespaceFilter, aggregationKeyFilter, sourceFilter, isOptionsLoading } = useKubernetesEventFilters({
    selectedAccountId,
    isTroubleshootPage,
    enableFilters: true,
    disabledFilters: ['subjectType', ...(isTroubleshootPage ? ['workload', 'namespace'] : [])],
    resource_ids: [],
    selectedNamespace,
  });

  useEffect(() => {
    setSelectedAccountId(accountId || router.query.accountId);
  }, [accountId, router.query.accountId]);

  // Sort state
  const [sortConfig, setSortConfig] = useState<{ name: string; order: 'asc' | 'desc' }>({
    name: 'Priority',
    order: 'desc',
  });

  // Classify Modal State
  const [classifyModalOpen, setClassifyModalOpen] = useState(false);
  const [selectedEventForClassify, setSelectedEventForClassify] = useState<any>(null);
  const [defaultClassification, setDefaultClassification] = useState<string>('');

  // Classify Menu State
  const [classifyMenuAnchor, setClassifyMenuAnchor] = useState<null | HTMLElement>(null);
  const [classifyMenuData, setClassifyMenuData] = useState<any>(null);

  // Ticket Modal State
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState<any>(null);

  // Menu Click Handler - handles Create Ticket, Classify, and Create Workflow
  const onMenuClick = useCallback(
    (menuItem: { id: number }, data: any) => {
      if (menuItem.id === 0) {
        // Create Ticket
        setTicketData(data);
        setIsTicketCreateFormOpen(true);
      } else if (menuItem.id === 1) {
        // Create Workflow — prefill Event Trigger with row's identifying fields.
        // distinct_priority is an array on grouped rows, so skip eventPriority here
        // to keep the trigger broad (workflow fires for all severities of this event).
        const accountId = data.account_id || router.query.accountId;
        const params = new URLSearchParams({ accountId, returnUrl: router.asPath });
        if (data.aggregation_key) params.set('eventType', data.aggregation_key);
        if (accountId) params.set('eventCluster', accountId);
        if (data.subject_namespace) params.set('eventNamespace', data.subject_namespace);
        router.push(`/workflow/new?${params.toString()}`);
      }
    },
    [router]
  );

  const handleClassifyMenuOpen = useCallback((event: React.MouseEvent<HTMLElement>, item: any) => {
    setClassifyMenuAnchor(event.currentTarget);
    setClassifyMenuData(item);
  }, []);

  const handleClassifyMenuClose = useCallback(() => {
    setClassifyMenuAnchor(null);
    setClassifyMenuData(null);
  }, []);

  const handleClassifyMenuSelect = useCallback(
    (classificationType: string) => {
      if (classifyMenuData) {
        setSelectedEventForClassify({
          id: classifyMenuData.latest_event_id,
          title: classifyMenuData.latest_title || classifyMenuData.aggregation_key,
          fingerprint: classifyMenuData.fingerprint,
          accountId: classifyMenuData.account_id,
        });
        setDefaultClassification(classificationType);
        setClassifyModalOpen(true);
      }
      handleClassifyMenuClose();
    },
    [classifyMenuData, handleClassifyMenuClose]
  );

  const handleClassifyClose = useCallback(() => {
    setClassifyModalOpen(false);
    setSelectedEventForClassify(null);
    setDefaultClassification('');
  }, []);

  const handleClassifySuccess = useCallback(() => {
    handleClassifyClose();
    // Trigger refetch by resetting page
    setCurrentPage(1);
  }, [handleClassifyClose]);

  const closeTicketCreateForm = useCallback(() => {
    setIsTicketCreateFormOpen(false);
    setTicketData(null);
  }, []);

  const handleTicketSuccess = useCallback(() => {
    closeTicketCreateForm();
    fetchTableDataRef.current?.();
  }, [closeTicketCreateForm]);

  const handleTicketFailure = useCallback((res: string) => {
    snackbar.error(`Failed! ${res}`);
  }, []);

  const getTicketDescription = (data: any) => {
    const investigateUrl = `${window.location.origin}/investigate?id=${data?.latest_event_id}&accountId=${data?.account_id}`;
    const firstOccurred = data?.min_created_at ? new Date(data.min_created_at).toLocaleString() : '';
    const lastOccurred = data?.max_created_at ? new Date(data.max_created_at).toLocaleString() : '';

    return [
      `Event: ${data?.aggregation_key || ''}`,
      `Subject: ${data?.subject_name || ''}`,
      `Namespace: ${data?.subject_namespace || ''}`,
      `Occurrences: ${data?.fingerprint_event_count ?? data?.event_count ?? ''}`,
      firstOccurred ? `Happening Since: ${firstOccurred}` : '',
      lastOccurred ? `Last Occurred: ${lastOccurred}` : '',
      `Fingerprint: ${data?.fingerprint || ''}`,
      '',
      `Investigation Link: ${investigateUrl}`,
    ]
      .filter(Boolean)
      .join('\n');
  };

  // --- Configuration ---

  const headers = useMemo(() => {
    const triageStatusHeader = {
      name: 'Triage Status',
      width: '12%',
      component: (
        <CustomTooltip
          variant='interactive'
          title='Triage Status'
          desc="Your team's response to this issue. Update it as you investigate, escalate, or resolve. To handle matching issues automatically, go to"
          linkText='Triage Rules →'
          linkUrl='/troubleshoot#triage-rules'
          placement='top'
        >
          <Box component='span' sx={{ cursor: 'default', display: 'inline-flex', alignItems: 'center', gap: '4px' }}>
            Triage Status
            <Box component='span' sx={{ position: 'relative', top: '3px', opacity: '50%' }}>
              <SafeIcon src={infoIcon} alt='info' width={12} height={14} />
            </Box>
          </Box>
        </CustomTooltip>
      ),
    };

    if (groupEventType === 'fingerprint') {
      return [
        {
          name: 'Severity',
          width: '10%',
          info: "Severity is the original urgency level assigned by the source monitoring/alerting system, based on that tool's built-in rules or your configured thresholds",
          infoPlacement: 'top-start',
        },
        {
          name: 'Application',
          width: '32%',
          info: 'The workload or service where this issue was detected.',
        },
        {
          name: 'Event Type',
          width: '16%',
          info: 'The type of alert or event reported by the source system.',
        },
        {
          name: 'Count',
          width: '8%',
          sortable: true,
          info: 'Number of times this event has fired in the selected time range.',
        },
        {
          name: 'Triage Score',
          width: '10%',
          sortable: true,
          info: "Triage Score is NudgeBee's context-aware triage score/level, computed using multiple signals beyond raw thresholds such as service criticality, customer/user impact, recurrence frequency, dependency (upstream/downstream) blast radius, and the nature of the service/workload.",
        },
        triageStatusHeader,
        {
          name: 'Alert Status',
          width: '12%',
          info: 'Current alert state from your source system (e.g. Prometheus, Datadog). Reflects whether the alert is still firing.',
        },
        '',
      ];
    }

    if (groupEventType === 'event_type') {
      return [
        {
          name: 'Severity',
          width: '12%',
          info: "Severity is the original urgency level assigned by the source monitoring/alerting system, based on that tool's built-in rules or your configured thresholds",
          infoPlacement: 'top-start',
        },
        {
          name: 'Event Type',
          width: '42%',
          info: 'The type of alert or event reported by the source system.',
        },
        {
          name: 'Last Occurred',
          width: '12%',
          info: 'The most recent time this event type was reported.',
        },
        {
          name: 'Event Count',
          width: '10%',
          info: 'Number of times this event has fired in the selected time range.',
        },
        {
          name: 'Subject',
          width: '10%',
          info: 'Number of distinct workloads or resources affected by this event type.',
        },
        triageStatusHeader,
        '',
      ];
    }
    return [];
  }, [groupEventType, isTroubleshootPage]);

  // 3. Main Data Fetching
  const fetchTableDataRef = useRef<() => Promise<void>>();

  const fetchTableData = useCallback(async () => {
    if (!selectedAccountId && !isTroubleshootPage) {
      return;
    }

    setLoading(true);

    const query: any = {
      account_id: selectedAccountId,
      start_date: new Date(selectedDateRange.startDate),
      end_date: new Date(selectedDateRange.endDate),
      subject_name: selectedWorkload,
      subject_namespace: selectedNamespace,
      aggregation_key: selectedAggregationKey?.map((e: any) => e.value) || [],
      status: selectedStatus,
      priority: selectedPriority,
      priority_nin: !selectedPriority ? ['DEBUG', 'INFO'] : undefined,
      source: selectedSource?.map((f: any) => f.value) || [],
      nb_priority: '',
      nb_status: selectedNBStatus.length > 0 ? selectedNBStatus.map((s) => s?.value || s) : undefined,
      is_new_issue: selectedIssueType === 'new' ? true : selectedIssueType === 'recurring' ? false : undefined,
    };

    let cols: string[] = [];
    let groupCols: string[] = [];

    if (groupEventType === 'fingerprint') {
      cols = [
        'max_created_at',
        'event_count',
        'subject_name',
        'subject_namespace',
        'aggregation_key',
        'distinct_priority',
        'distinct_status',
        'fingerprint',
        'min_created_at',
        'account_id',
        'latest_event_id',
        'latest_computed_priority',
        'latest_score_confidence',
        'latest_score_factors',
        'latest_computed_score',
        'latest_nb_status',
        'latest_title',
        'is_new_issue',
        'fingerprint_first_seen_at',
        'fingerprint_event_count',
      ];
      groupCols = ['tenant_id', 'account_id', 'subject_name', 'subject_namespace', 'aggregation_key', 'fingerprint'];
    } else {
      cols = [
        'max_created_at',
        'event_count',
        'aggregation_key',
        'distinct_priority',
        'distinct_status',
        'count_subject_name',
        'account_id',
        'latest_nb_status',
        'latest_computed_score',
      ];
      groupCols = ['tenant_id', 'account_id', 'aggregation_key'];
    }

    const SORT_COLUMN_MAP: Record<string, string> = {
      Count: 'fingerprint_event_count',
      Priority: 'latest_computed_score',
    };
    const apiSortConfig = {
      name: SORT_COLUMN_MAP[sortConfig.name] || sortConfig.name,
      order: sortConfig.order,
    };

    try {
      const res: any = await k8sApi.getK8sEventGroupings(perPage, (currentPage - 1) * perPage, query, groupCols, cols, apiSortConfig);
      const groupings = res?.data?.event_groupings ?? [];
      setRawEventGroupings(groupings);
      setTotalRows(res.data.event_groupings_aggregate.aggregate.count);

      // Only the fingerprint variant has a Create Ticket action keyed off fingerprint
      if (groupEventType === 'fingerprint') {
        const fingerprints = Array.from(new Set(groupings.map((g: any) => g.fingerprint).filter(Boolean))) as string[];
        if (fingerprints.length > 0) {
          try {
            const ticketRes: any = await ticketsApi.listTicketsSummary({ reference_id: fingerprints });
            const map = new Map<string, any>();
            ticketRes?.data?.tickets?.forEach((t: any) => {
              map.set(t.reference_id, t);
            });
            setTicketReferenceMap(map);
          } catch (err) {
            console.error('Failed to fetch ticket summaries', err);
            setTicketReferenceMap(new Map());
          }
        } else {
          setTicketReferenceMap(new Map());
        }
      } else {
        setTicketReferenceMap(new Map());
      }
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  }, [
    selectedAccountId,
    currentPage,
    perPage,
    groupEventType,
    isTroubleshootPage,
    selectedDateRange,
    selectedNamespace,
    selectedWorkload,
    selectedAggregationKey,
    selectedStatus,
    selectedPriority,

    selectedSource,
    selectedNBStatus,
    selectedIssueType,
    sortConfig,
  ]);

  useEffect(() => {
    fetchTableData();
  }, [fetchTableData]);

  // Keep ref updated for use in table row callbacks
  fetchTableDataRef.current = fetchTableData;

  // Derive table data from raw API response + display dependencies (no API call)
  const tableData = useMemo(
    () =>
      transformTableData(
        rawEventGroupings,
        groupEventType,
        isTroubleshootPage,
        accounts,
        accountType,
        selectedDateRange,
        onMenuClick,
        () => fetchTableDataRef.current?.(),
        (item: any) => {
          setTicketData(item);
          setIsTicketCreateFormOpen(true);
        },
        handleClassifyMenuOpen,
        ticketReferenceMap
      ),
    [
      rawEventGroupings,
      groupEventType,
      isTroubleshootPage,
      accounts,
      accountType,
      selectedDateRange,
      onMenuClick,
      handleClassifyMenuOpen,
      ticketReferenceMap,
    ]
  );

  // --- Event Handlers ---

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      key: 'selection',
    });
    setCurrentPage(1);
  };

  const onNamespaceFilterChange = (e: any) => {
    const val = e?.target?.value;
    setSelectedNamespace(val);
    setSelectedWorkload('');
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventNamespace: val, eventSubjectName: '' });
  };

  const onWorkloadFilterChange = (e: any) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
  };

  const onAggregationKeyFilterChange = (_e: any, p: any) => {
    const newVal = p && p.length > 0 ? p : [];
    setSelectedAggregationKey(newVal);
    setCurrentPage(1); // Fixed: Reset to 1, not 0
    applyFiltersOnRouter(router, { eventAggregationKey: newVal?.map((v: any) => v.value) });
  };

  const onAccountFilterChange = (e: any) => {
    setSelectedAccountId(e.target.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const onSourceFilterChange = (_e: any, value: any[]) => {
    const newValues = value && value.length > 0 ? value : [];
    setSelectedSource(newValues);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { source: newValues.map((v) => v.value) });
  };

  const handleSortChange = (sort: { name: string; order: string }) => {
    setSortConfig({
      name: sort.name,
      order: sort.order as 'asc' | 'desc',
    });
    setCurrentPage(1);
  };

  return (
    <BoxLayout2
      id={'Grouped Applications'}
      heading={''}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => ({ tableId: componentId }),
        },
        sharing: { enabled: false, onClick: null },
      }}
      dateTimeRange={{
        enabled: true,
        onChange: handleDateRangeChange,
        passedSelectedDateTime: {
          startTime: selectedDateRange.startDate,
          endTime: selectedDateRange.endDate,
          shortcutClickTime: 0,
        },
      }}
      filterOptions={[
        ...(isTroubleshootPage
          ? [
              {
                type: 'dropdown',
                enabled: true,
                grouped: true,
                groupIcon: renderAccountGroupIcon,
                options: accounts.map((acc: any) => ({
                  label: acc.label || acc.account_name,
                  value: acc.id || acc.value,
                  group: acc.cloud_provider || 'Other',
                })),
                onSelect: onAccountFilterChange,
                label: 'Account',
                value: selectedAccountId,
              },
            ]
          : []),

        ...(!isTroubleshootPage
          ? [
              {
                type: 'dropdown',
                options: namespaceFilter,
                onSelect: onNamespaceFilterChange,
                label: 'Namespace',
                value: selectedNamespace,
              },
              {
                type: 'dropdown',
                options: workloadFilter,
                onSelect: onWorkloadFilterChange,
                label: 'Workload',
                value: selectedWorkload,
              },
            ]
          : []),
        {
          type: 'multi-dropdown',
          options: aggregationKeyFilter,
          onSelect: onAggregationKeyFilterChange,
          label: 'Event Type',
          value: selectedAggregationKey,
          isOptionsLoading: isOptionsLoading.aggregationKey,
        },
        {
          type: 'multi-dropdown',
          enabled: true,
          options: sourceFilter,
          onSelect: onSourceFilterChange,
          label: 'Source',
          value: selectedSource,
          isOptionsLoading: isOptionsLoading.source,
        },
        ...(groupEventType === 'event_type' || groupEventType === 'fingerprint'
          ? [
              {
                type: 'dropdown',
                enabled: true,
                options: [
                  { value: 'HIGH', label: 'High' },
                  { value: 'MEDIUM', label: 'Medium' },
                  { value: 'DEBUG', label: 'Debug' },
                  { value: 'LOW', label: 'Low' },
                  { value: 'INFO', label: 'Info' },
                ],
                onSelect: (e: any) => {
                  setSelectedPriority(e.target.value);
                  setCurrentPage(1);
                },
                label: 'Severity',
                value: selectedPriority,
              },
              {
                type: 'dropdown',
                enabled: true,
                options: [
                  { value: 'FIRING', label: 'Firing' },
                  { value: 'CLOSED', label: 'Closed' },
                ],
                onSelect: (e: any) => {
                  setSelectedStatus(e.target.value);
                  setCurrentPage(1);
                },
                label: 'Status',
                value: selectedStatus,
              },
            ]
          : []),
        ...(groupEventType === 'fingerprint'
          ? [
              {
                type: 'dropdown',
                enabled: true,
                options: [
                  { value: 'all', label: 'All Issues' },
                  { value: 'new', label: 'New Issues' },
                  { value: 'recurring', label: 'Recurring Issues' },
                ],
                onSelect: (e: any) => {
                  setSelectedIssueType(e.target.value);
                  setCurrentPage(1);
                  applyFiltersOnRouter(router, { issueType: e.target.value === 'all' ? '' : e.target.value });
                },
                label: 'Issue Type',
                value: selectedIssueType,
              },
            ]
          : []),
        {
          type: 'multi-dropdown',
          enabled: true,
          options: [
            { value: 'OPEN', label: 'Open' },
            { value: 'ACTION_REQUIRED', label: 'Action Required' },
            { value: 'SNOOZED', label: 'Snoozed' },
            { value: 'SUPPRESSED', label: 'Suppressed' },
            { value: 'DROPPED', label: 'Dropped' },
            { value: 'DUPLICATE', label: 'Duplicate' },
            { value: 'RESOLVED', label: 'Resolved' },
          ],
          onSelect: (e: any) => {
            setSelectedNBStatus(e.target.value);
            setCurrentPage(1);
          },
          label: 'Triage Status',
          value: selectedNBStatus,
        },
      ]}
    >
      <KubernetesTable2
        id={componentId}
        headers={headers}
        loading={loading}
        data={tableData}
        rowsPerPage={perPage}
        totalRows={totalRows}
        onPageChange={(e: number, limit: number) => {
          setCurrentPage(e);
          setPerPage(limit);
        }}
        pageNumber={currentPage}
        onSortChange={handleSortChange}
        tableHeadingCenter={['Severity']}
        showExpandable
        expandable={{
          tabs: [{ text: 'Events', key: 'events' }],
        }}
      />

      {/* Classification Type Menu */}
      <Menu
        anchorEl={classifyMenuAnchor}
        open={Boolean(classifyMenuAnchor)}
        onClose={handleClassifyMenuClose}
        anchorOrigin={{ vertical: 'bottom', horizontal: 'right' }}
        transformOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        {CLASSIFICATION_OPTIONS.map((option) => (
          <MenuItem key={option.value} onClick={() => handleClassifyMenuSelect(option.value)} sx={{ py: '12px' }}>
            <ListItemText
              primary={
                <Typography variant='body2' sx={{ fontWeight: 500, fontSize: '13px', lineHeight: '13px' }}>
                  {option.label}
                </Typography>
              }
              secondary={
                <Typography variant='caption' sx={{ color: 'text.secondary', lineHeight: '12px' }}>
                  {option.description}
                </Typography>
              }
            />
          </MenuItem>
        ))}
      </Menu>

      {/* Event Classify Modal */}
      {selectedEventForClassify && (
        <EventClassifyModal
          open={classifyModalOpen}
          handleClose={handleClassifyClose}
          event={selectedEventForClassify}
          onSuccess={handleClassifySuccess}
          defaultClassification={defaultClassification}
        />
      )}

      {/* Ticket Create Modal */}
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + (ticketData?.latest_title || ticketData?.aggregation_key || ''),
          description: getTicketDescription(ticketData),
          accountId: ticketData?.account_id,
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.latest_event_id}&accountId=${ticketData?.account_id}` }}
        reference={{
          id: ticketData?.fingerprint,
          type: 'kubernetes',
        }}
      />
    </BoxLayout2>
  );
};

export default KubernetesGroupedEventsTable;
