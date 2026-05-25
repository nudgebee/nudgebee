import React, { useState, useEffect, useMemo, useCallback, useRef } from 'react';
import { useRouter } from 'next/router';
import SafeIcon from '@components1/common/SafeIcon';
import CloudProviderIcon from '@components1/common/CloudIcon';

// Components
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Datetime from '@common-new/format/Datetime';
import SeverityIcon from '@components1/ds/SeverityIcon';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import { ds } from 'src/utils/colors';
import Text from '@common-new/format/Text';
import CustomTooltip from '@components1/common/CustomTooltip';

// DS v2 / component-new
import NBStatusBadge from '@common-new/widgets/NBStatusBadge';
import NewIssueChip from '@common-new/widgets/NewIssueChip';
import ScoreDisplay from '@common-new/widgets/ScoreDisplay';
import CustomTicketLink from '@common-new/CustomTicketLink';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { snackbar } from '@common-new/snackbarService';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu } from '@components1/ds/DropdownMenu';
import CustomLabels from '@common-new/widgets/CustomLabels';
import { FiArrowRight } from 'react-icons/fi';

// APIs & Utils
import k8sApi from '@api1/kubernetes';
import apiUser from '@api1/user';
import ticketsApi from '@api1/tickets';
import { applyFiltersOnRouter } from '@lib/router';
import { convertToReadableFormat, titleCaseForAggregationKey, syncFilterFromQuery, toSeverityLevel } from 'src/utils/common';
import { Box, Typography } from '@mui/material';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';
import { useEventCloudFilter } from '@hooks/useCloudFilters';
import EventClassifyModal from '@components1/events/EventClassifyModal';
import { CLASSIFICATION_OPTIONS, getTriageStatusTooltip } from '@api1/triage';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { action } from 'src/utils/actionStyles';
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
  onClassify?: (item: any, classificationType: string) => void,
  ticketMap?: Map<string, any>,
  nbStatus?: string[]
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
      ...(nbStatus && nbStatus.length > 0 ? { nb_status: nbStatus } : {}),
    };

    const existingTicket = ticketMap?.get(item.fingerprint);
    const hasExistingTicket = Boolean(existingTicket);

    // Columns based on type
    if (groupEventType === 'fingerprint') {
      return [
        {
          component: (
            <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0px' }}>
              <SeverityIcon level={toSeverityLevel(severity)} aria-label={`${severity || 'unknown'}`} />
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
              <DsButton
                tone='secondary'
                size='xs'
                trailingAccent={<FiArrowRight />}
                href={`/investigate?id=${item.latest_event_id}&accountId=${item.account_id}`}
                data-testid='investigate-btn'
              >
                Investigate
              </DsButton>
              <DropdownMenu
                align='end'
                minWidth={300}
                trigger={
                  <DsButton
                    tone='secondary'
                    size='xs'
                    composition='icon-only'
                    icon={<CategoryOutlinedIcon />}
                    aria-label='Classify'
                    tooltip='Classify'
                    onClick={(e) => e.stopPropagation()}
                  />
                }
                items={CLASSIFICATION_OPTIONS.map((option) => ({
                  id: option.value,
                  label: (
                    <Box sx={{ display: 'flex', flexDirection: 'column', gap: '2px' }}>
                      <Typography sx={{ fontWeight: ds.weight.medium, fontSize: ds.text.small, lineHeight: '16px', color: ds.gray[700] }}>
                        {option.label}
                      </Typography>
                      <Typography sx={{ fontSize: ds.text.caption, lineHeight: '14px', color: ds.gray[500] }}>{option.description}</Typography>
                    </Box>
                  ),
                  onSelect: () => onClassify?.(item, option.value),
                }))}
              />
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
      ...(nbStatus && nbStatus.length > 0 ? { nb_status: nbStatus } : {}),
    };
    return [
      {
        component: <SeverityIcon level={toSeverityLevel(severity)} aria-label={`${severity || 'unknown'}`} />,
        data: severity,
      },
      {
        component: (
          <Box>
            <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} style={{ fontWeight: 400 }} />
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
    ];
  });
};

const renderAccountGroupIcon = (provider: string) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;

const normalizeOptionValue = (entry: any) => {
  if (entry == null) return null;
  if (typeof entry === 'string' || typeof entry === 'number' || typeof entry === 'boolean') return entry;
  return entry.value ?? entry.label ?? null;
};

const ensureSelectedInOptions = (options: any[] = [], selectedValue: any) => {
  if (selectedValue == null || selectedValue === '') return options;
  const selectedValues = (Array.isArray(selectedValue) ? selectedValue : [selectedValue])
    .map(normalizeOptionValue)
    .filter((v: any) => v != null && v !== '');
  if (selectedValues.length === 0) return options;

  const existingValues = new Set(options.map(normalizeOptionValue).filter((v: any) => v != null && v !== ''));
  const missing = selectedValues.filter((v: any) => !existingValues.has(v));
  if (missing.length === 0) return options;

  const isStringArray = options.length === 0 || options.every((o) => typeof o === 'string');
  return [...options, ...missing.map((v: any) => (isStringArray ? v : { label: String(v), value: v }))];
};

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
  const [selectedDateRange, setSelectedDateRange] = useState<DateRange>(() => {
    const startTime = router?.query?.start_time;
    const endTime = router?.query?.end_time;
    if (startTime && endTime) {
      return { startDate: parseInt(startTime as string), endDate: parseInt(endTime as string), key: 'selection' };
    }
    return { startDate: new Date().getTime() - 60 * 60 * 24 * 1000, endDate: new Date().getTime(), key: 'selection' };
  });

  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace ?? router?.query?.eventNamespace ?? null);
  const [selectedWorkload, setSelectedWorkload] = useState(router?.query?.eventSubjectName ?? '');
  const [selectedAggregationKey, setSelectedAggregationKey] = useState<any[]>([]);
  const [selectedAccountId, setSelectedAccountId] = useState<string[]>(() => {
    const raw = accountId || (router.query.accountId as string);
    return raw ? raw.split(',').filter(Boolean) : [];
  });
  const [selectedStatus, setSelectedStatus] = useState<string>('');
  const [selectedPriority, setSelectedPriority] = useState('');

  const [selectedSource, setSelectedSource] = useState<any[]>([]);
  const [selectedNBStatus, setSelectedNBStatus] = useState<Array<{ label: string; value: string }>>([{ value: 'OPEN', label: 'Open' }]);
  const [selectedServiceName, setSelectedServiceName] = useState('');
  const [selectedIssueType, setSelectedIssueType] = useState<string>((router?.query?.issueType as string) || 'all');

  const sortByOptions = useMemo(() => {
    if (groupEventType === 'fingerprint') {
      return [
        { value: 'Priority', label: 'Triage Score' },
        { value: 'Last Occurred', label: 'Last Occurred' },
      ];
    }
    return [
      { value: 'Priority', label: 'Triage Score' },
      { value: 'Last Occurred', label: 'Last Occurred' },
      { value: 'Event Count', label: 'Event Count' },
    ];
  }, [groupEventType]);

  const { accounts, accountType, workloadFilter, namespaceFilter, aggregationKeyFilter, sourceFilter, isOptionsLoading } = useKubernetesEventFilters({
    selectedAccountId,
    isTroubleshootPage,
    enableFilters: true,
    disabledFilters: ['subjectType', ...(isTroubleshootPage && !selectedAccountId.length ? ['workload', 'namespace'] : [])],
    resource_ids: [],
    selectedNamespace,
  });

  const { serviceNamesFilter, isOptionsLoading: cloudOptionsLoading } = useEventCloudFilter(selectedAccountId, {
    subjectNamespace: selectedServiceName,
  });

  useEffect(() => {
    const raw = accountId || (router.query.accountId as string);
    const next = raw ? raw.split(',').filter(Boolean) : [];
    setSelectedAccountId((prev) => {
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
  }, [accountId, router.query.accountId]);

  useEffect(() => {
    setSelectedAggregationKey((prev) => {
      const next = syncFilterFromQuery(aggregationKeyFilter, router?.query?.eventAggregationKey, (f: any) => f.value);
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [aggregationKeyFilter]);

  useEffect(() => {
    setSelectedSource((prev) => {
      const next = syncFilterFromQuery(sourceFilter, router?.query?.source, (f: any) => f.value);
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceFilter]);

  // Sort state
  const [sortConfig, setSortConfig] = useState<{ name: string; order: 'asc' | 'desc' }>({
    name: 'Priority',
    order: 'desc',
  });

  // Classify Modal State
  const [classifyModalOpen, setClassifyModalOpen] = useState(false);
  const [selectedEventForClassify, setSelectedEventForClassify] = useState<any>(null);
  const [defaultClassification, setDefaultClassification] = useState<string>('');

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

  const handleClassifySelect = useCallback((item: any, classificationType: string) => {
    setSelectedEventForClassify({
      id: item.latest_event_id,
      title: item.latest_title || item.aggregation_key,
      fingerprint: item.fingerprint,
      accountId: item.account_id,
    });
    setDefaultClassification(classificationType);
    setClassifyModalOpen(true);
  }, []);

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
      width: '14%',
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
          width: '30%',
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
          width: '16%',
          info: 'Current alert state from your source system (e.g. Prometheus, Datadog). Reflects whether the alert is still firing.',
        },
        { name: 'Action', width: '12%', size: 'sm', align: 'right' as const, exportEnabled: false },
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
          width: '30%',
          info: 'The type of alert or event reported by the source system.',
        },
        {
          name: 'Last Occurred',
          width: '14%',
          info: 'The most recent time this event type was reported.',
          sortEnabled: true,
        },
        {
          name: 'Event Count',
          width: '12%',
          info: 'Number of times this event has fired in the selected time range.',
        },
        {
          name: 'Subject',
          width: '10%',
          info: 'Number of distinct workloads or resources affected by this event type.',
        },
        triageStatusHeader,
      ];
    }
    return [];
  }, [groupEventType, isTroubleshootPage]);

  // 3. Main Data Fetching
  const fetchTableDataRef = useRef<() => Promise<void>>();

  const fetchTableData = useCallback(async () => {
    if (!selectedAccountId.length && !isTroubleshootPage) {
      return;
    }

    setLoading(true);

    const isCloudAccount = accountType === 'AWS' || accountType === 'GCP' || accountType === 'Azure';
    const query: any = {
      account_id: selectedAccountId.length ? selectedAccountId : undefined,
      start_date: new Date(selectedDateRange.startDate),
      end_date: new Date(selectedDateRange.endDate),
      subject_name: selectedWorkload,
      subject_namespace: isCloudAccount && selectedServiceName ? selectedServiceName : selectedNamespace,
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
      'Event Count': 'event_count',
      Priority: 'latest_computed_score',
      'Last Occurred': 'max_created_at',
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
    selectedServiceName,
    accountType,
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
        handleClassifySelect,
        ticketReferenceMap,
        selectedNBStatus.length > 0 ? selectedNBStatus.map((s) => s.value) : undefined
      ),
    [
      rawEventGroupings,
      groupEventType,
      isTroubleshootPage,
      accounts,
      accountType,
      selectedDateRange,
      onMenuClick,
      handleClassifySelect,
      ticketReferenceMap,
      selectedNBStatus,
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
    applyFiltersOnRouter(router, { start_time: passedSelectedDateTime.startTime, end_time: passedSelectedDateTime.endTime });
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

  const onAccountFilterChange = (_e: any, value: any[]) => {
    const ids = (value || []).map((v: any) => v.value);
    setSelectedAccountId(ids);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { accountId: ids.join(',') });
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

  const onSortByChange = (e: any) => {
    setSortConfig({ name: e.target.value, order: 'desc' });
    setCurrentPage(1);
  };

  const onServiceNamesFilterChange = (e: any) => {
    setSelectedServiceName(e?.target?.value || '');
    setCurrentPage(1);
  };

  // CSV export — mirrors the v1 DownloadButton DOM-scrape contract
  // (data-export-enabled, data-export-data) used by KubernetesTable2 cells.
  const handleDownloadCsv = () => {
    const oTable = document.getElementById(componentId);
    if (!oTable) {
      snackbar.error('Nothing to export — table not ready.');
      return;
    }
    const escape = (s: any) => `"${(s == null ? '' : String(s)).replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    let csv = '';
    const headerRows = oTable.querySelectorAll('thead tr');
    const headerRow = headerRows?.[headerRows.length - 1];
    if (headerRow) {
      csv +=
        [...headerRow.children]
          .filter((th) => th.getAttribute('data-export-enabled') !== 'false')
          .map((th) => escape((th as HTMLElement).innerText))
          .join(',') + '\r\n';
    }
    const bodyRows = oTable.querySelectorAll('tbody tr') || [];
    for (const tr of Array.from(bodyRows)) {
      const cells = [...tr.children].filter((td) => td.getAttribute('data-export-enabled') === 'true');
      if (cells.length === 0) continue;
      csv += cells.map((td) => escape(td.getAttribute('data-export-data') ?? (td as HTMLElement).innerText)).join(',') + '\r\n';
    }
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = `${componentId}.csv`;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  /**
   * Whole-row option-to-JSX recipe. Each FilterDropdown is rendered in the
   * Toolbar's children slot; conditionals mirror the original filterOptions[]
   * array shape so the visible set is identical to the BoxLayout2 path.
   */
  const filterDropdownsLegacyShape = [
    ...(isTroubleshootPage
      ? [
          {
            type: 'multi-dropdown',
            enabled: true,
            grouped: true,
            groupIcon: renderAccountGroupIcon,
            selectionWithinGroup: true,
            options: accounts.map((acc: any) => ({
              label: acc.label || acc.account_name,
              value: acc.id || acc.value,
              group: acc.cloud_provider || 'Other',
            })),
            onSelect: onAccountFilterChange,
            label: 'Account',
            value: accounts
              .filter((acc: any) => selectedAccountId.includes(acc.id || acc.value))
              .map((acc: any) => ({
                label: acc.label || acc.account_name,
                value: acc.id || acc.value,
                group: acc.cloud_provider || 'Other',
              })),
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
      : accountType === 'K8s' && selectedAccountId.length
      ? [
          {
            type: 'dropdown',
            options: namespaceFilter,
            onSelect: onNamespaceFilterChange,
            label: 'Namespace',
            value: selectedNamespace,
            isOptionsLoading: isOptionsLoading.namespace,
          },
        ]
      : (accountType === 'AWS' || accountType === 'GCP' || accountType === 'Azure') && selectedAccountId.length
      ? [
          {
            type: 'dropdown',
            enabled: true,
            options: ensureSelectedInOptions(serviceNamesFilter, selectedServiceName),
            onSelect: onServiceNamesFilterChange,
            label: 'Service Name',
            value: selectedServiceName,
            isOptionsLoading: cloudOptionsLoading.namespace,
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
      type: 'dropdown',
      enabled: true,
      options: sortByOptions,
      onSelect: onSortByChange,
      label: 'Sort By',
      value: sortConfig.name,
    },
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
  ];

  return (
    <>
      <ListingLayout id='Grouped Applications'>
        <ListingLayout.Toolbar
          actions={
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  shortcutClickTime: 0,
                }}
                onChange={({ selection }: { selection: { startTime: number; endTime: number; shortcutClickTime: number } }) =>
                  handleDateRangeChange(selection)
                }
              />
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<FileDownloadOutlinedIcon />}
                aria-label='Download events as CSV'
                tooltip='Download as CSV'
                id='triage-inbox-download'
                onClick={handleDownloadCsv}
              />
            </Box>
          }
        >
          {filterDropdownsLegacyShape.map((opt: any, idx: number) => (
            <FilterDropdown
              key={`${opt.label || 'filter'}-${idx}`}
              id={`filter-${(opt.label || `filter-${idx}`).toString().replace(/\s+/g, '-').toLowerCase()}`}
              label={opt.label}
              multiple={opt.type === 'multi-dropdown'}
              grouped={!!opt.grouped}
              groupIcon={opt.groupIcon}
              selectionWithinGroup={!!opt.selectionWithinGroup}
              options={opt.options || []}
              value={opt.value}
              onSelect={opt.onSelect}
              isOptionsLoading={opt.isOptionsLoading}
            />
          ))}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
            sort={sortConfig}
            onSortChange={handleSortChange}
            tableHeadingCenter={['Severity']}
            showExpandable
            expandable={{
              tabs: [{ text: 'Events', key: 'events' }],
            }}
          />
        </ListingLayout.Body>
      </ListingLayout>

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
    </>
  );
};

export default KubernetesGroupedEventsTable;
