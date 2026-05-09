import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import { Box, FormControlLabel } from '@mui/material';
import CustomSwitch from '@common/CustomSwitch';

// Components
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Datetime from '@components1/common/format/Datetime';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import InvestigateButton from '@components1/common/InvestigateButton';
import LineChart from '@components1/common/charts/LineCharts';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import NBStatusBadge from '@components1/common/widgets/NBStatusBadge';
import ScoreDisplay from '@components1/common/widgets/ScoreDisplay';
import NewIssueChip from '@components1/common/widgets/NewIssueChip';
import CloudProviderIcon from '@components1/common/CloudIcon';
import Text from '@components1/common/format/Text';
import CustomTicketLink from '@components1/common/CustomTicketLink';
import CustomPRLink from '@components1/common/CustomPRLink';
import CustomLink from '@components1/common/CustomLink';
import EventClassifyModal from './EventClassifyModal';

// API & Utils
import k8sApi from '@api1/kubernetes';
import ticketsApi from '@api1/tickets';
import apiUser from '@api1/user';
import { getDateString, getLast24Hrs } from '@lib/datetime';
import { hasWriteAccess } from '@lib/auth';
import { safeJSONParse, titleCaseForAggregationKey, syncFilterFromQuery } from 'src/utils/common';
import { applyFiltersOnRouter } from '@lib/router';
import { snackbar } from '@components1/common/snackbarService';
import { action } from 'src/utils/actionStyles';

import { useEventCloudFilter } from '@hooks/useCloudFilters';

// Assets
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import { dashboardIcon1 as ClassifyIcon, infoIcon } from '@assets';
import SafeIcon from '@components1/common/SafeIcon';
import CustomTooltip from '@components1/common/CustomTooltip';
import { getTriageStatusTooltip } from '@api1/triage';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';
import WorkflowIcon from '@assets/WorkflowIcon';

const DEFAULT_TABLE_COLUMNS = [
  {
    name: 'Severity',
    width: '9%',
    align: 'center',
    defaultVisible: true,
    info: "Severity is the original urgency level assigned by the source monitoring/alerting system, based on that tool's built-in rules or your configured thresholds",
    infoPlacement: 'top-start',
  },
  {
    name: 'Application',
    width: '22%',
    align: 'left',
    truncate: 'clamp-2',
    defaultVisible: true,
    info: 'The resource or workload this event belongs to.',
  },
  {
    name: 'Message',
    width: '25%',
    align: 'left',
    truncate: 'clamp-2',
    defaultVisible: true,
    info: 'The alert message as received from the source system.',
  },
  { name: 'Event Type', width: '12%', align: 'left', defaultVisible: false },
  {
    name: 'Triage Score',
    width: '10%',
    align: 'left',
    defaultVisible: true,
    info: "Triage Score is NudgeBee's context-aware triage score/level, computed using multiple signals beyond raw thresholds such as service criticality, customer/user impact, recurrence frequency, dependency (upstream/downstream) blast radius, and the nature of the service/workload.",
  },
  {
    name: 'Alert Status',
    width: '12%',
    align: 'center',
    defaultVisible: true,
    info: 'Current alert state from your source system. Reflects whether the alert is still firing.',
  },
  {
    name: 'Triage Status',
    width: '10%',
    align: 'center',
    defaultVisible: false,
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
  },
  { name: 'Action', width: '12%', size: 'sm', align: 'right', exportEnabled: false, defaultVisible: true },
];

const getValidParam = (param, defaultValue = null) => {
  if (!param || param === 'undefined' || param === 'null' || param === '' || param === undefined || param === null) {
    return defaultValue;
  }
  return param;
};

function parseKeyValueStringToJSON(str) {
  const obj = {};
  if (!str || typeof str !== 'string') {
    return JSON.stringify(obj);
  }

  const pairs = str
    .split(',')
    .map((pair) => pair.trim())
    .filter((pair) => pair.length > 0);

  for (const pair of pairs) {
    const parts = pair.split(':');

    if (parts.length !== 2 || !parts[0].trim() || !parts[1].trim()) {
      snackbar.error('Expected format at labels search is key:value. Examples of valid input: status:401, method:POST');
      continue;
    }
    const key = parts[0].trim();
    const rawValue = parts[1].trim();
    const value = isNaN(rawValue) ? rawValue : Number(rawValue);
    obj[key] = value;
  }
  return JSON.stringify(obj);
}

// Ensures the selected value from URL is included in dropdown options even if not returned by API
const normalizeOptionValue = (entry) => {
  if (entry == null) return null;
  if (typeof entry === 'string' || typeof entry === 'number' || typeof entry === 'boolean') return entry;
  return entry.value ?? entry.label ?? null;
};

const ensureSelectedInOptions = (options = [], selectedValue) => {
  if (selectedValue == null || selectedValue === '') return options;
  const selectedValues = (Array.isArray(selectedValue) ? selectedValue : [selectedValue])
    .map(normalizeOptionValue)
    .filter((v) => v != null && v !== '');
  if (selectedValues.length === 0) return options;

  const existingValues = new Set(options.map(normalizeOptionValue).filter((v) => v != null && v !== ''));
  const missing = selectedValues.filter((v) => !existingValues.has(v));
  if (missing.length === 0) return options;

  const isStringArray = options.length === 0 || options.every((o) => typeof o === 'string');
  return [...options, ...missing.map((v) => (isStringArray ? v : { label: String(v), value: v }))];
};

const renderAccountGroupIcon = (provider) => <CloudProviderIcon cloud_provider={provider} width='14px' height='14px' />;
const NB_STATUS_FILTER = [
  { value: 'OPEN', label: 'Open' },
  { value: 'ACTION_REQUIRED', label: 'Action Required' },
  { value: 'SNOOZED', label: 'Snoozed' },
  { value: 'SUPPRESSED', label: 'Suppressed' },
  { value: 'DROPPED', label: 'Dropped' },
  { value: 'DUPLICATE', label: 'Duplicate' },
  { value: 'RESOLVED', label: 'Resolved' },
];

const KubernetesEventsTable = ({
  accountId,
  recordsPerPage,
  defaultQuery = {},
  enableFilters = true,
  disabledFilters = [],
  enableTrendChart = false,
  heading: _heading = 'All Events',
  tableColumns: initialTableColumns = DEFAULT_TABLE_COLUMNS,
  stickyColumnIndex = '',
  resource_ids = [],
  showTimeFilter = true,
  isTroubleshootPage = false,
}) => {
  const router = useRouter();

  const showEllipsis = true;
  const statusFilter = [
    { value: 'FIRING', label: 'Open' },
    { value: 'CLOSED', label: 'Closed' },
  ];
  const priorityFilter = [
    { value: 'HIGH', label: 'High' },
    { value: 'MEDIUM', label: 'Medium' },
    { value: 'DEBUG', label: 'Debug' },
    { value: 'LOW', label: 'Low' },
    { value: 'INFO', label: 'Info' },
  ];
  const sortByOptions = [
    { value: 'created_at', label: 'Time' },
    { value: 'computed_score', label: 'Triage Score' },
  ];
  const kubernetesEventsTable = 'kubernetesEventsTable';

  const getInitialTime = () => {
    const startTime = getValidParam(router.query.start_time);
    const endTime = getValidParam(router.query.end_time);

    if (startTime && endTime) {
      return {
        startDate: parseInt(startTime),
        endDate: parseInt(endTime),
      };
    } else if (defaultQuery?.startTime && defaultQuery?.endTime) {
      return { startDate: defaultQuery.startTime, endDate: defaultQuery.endTime };
    }
    return { startDate: getLast24Hrs().getTime(), endDate: new Date().getTime() };
  };

  const getInitialAggregationKey = () => {
    let selectedKeys = [];
    const aggregationKey = getValidParam(router.query.eventAggregationKey || router.query.aggregation_key || defaultQuery?.aggregation_key);

    if (aggregationKey) {
      if (Array.isArray(aggregationKey)) {
        selectedKeys = aggregationKey.filter((e) => getValidParam(e)).map((e) => ({ value: e }));
      } else if (typeof aggregationKey === 'string') {
        selectedKeys = aggregationKey
          .split(',')
          .filter((e) => getValidParam(e))
          .map((e) => ({ value: e }));
      }
    }
    return selectedKeys;
  };

  // --- Component State ---
  const troubleshootColumns = useMemo(
    () => [
      {
        name: 'Severity',
        width: '9%',
        align: 'center',
        info: "Severity is the original urgency level assigned by the source monitoring/alerting system, based on that tool's built-in rules or your configured thresholds",
        infoPlacement: 'top-start',
      },
      {
        name: 'Application',
        width: '18%',
        align: 'left',
        truncate: 'clamp-2',
        info: 'The resource or workload this event belongs to.',
      },
      {
        name: 'Message',
        width: '28%',
        align: 'left',
        truncate: 'clamp-2',
        info: 'The alert message as received from the source system.',
      },
      {
        name: 'Triage Score',
        width: '10%',
        align: 'left',
        info: "Triage Score is NudgeBee's context-aware triage score/level, computed using multiple signals beyond raw thresholds such as service criticality, customer/user impact, recurrence frequency, dependency (upstream/downstream) blast radius, and the nature of the service/workload.",
      },
      {
        name: 'Alert Status',
        sortEnabled: true,
        width: '12%',
        align: 'center',
        info: 'Current alert state from your source system. Reflects whether the alert is still firing.',
      },
      {
        name: 'Triage Status',
        width: '11%',
        align: 'center',
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
      },
      { name: 'Action', width: '12%', size: 'sm', align: 'right', exportEnabled: false },
    ],
    []
  );
  const [tableColumns, setTableColumns] = useState(() => (isTroubleshootPage ? troubleshootColumns : initialTableColumns));
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [currentPage, setCurrentPage] = useState(0);
  const [rowsPerPage, setRowsPerPage] = useState(() => recordsPerPage ?? apiUser.getUserPreferencesTablePageSize());

  // Selections
  const [selectedAccountId, setSelectedAccountId] = useState(() => accountId || router.query.accountId);
  const [selectedNamespace, setSelectedNamespace] = useState(
    () => defaultQuery?.namespace ?? getValidParam(router?.query?.namespace) ?? getValidParam(router?.query?.eventNamespace)
  );
  const [selectedWorkload, setSelectedWorkload] = useState(
    () => defaultQuery?.workloadName ?? defaultQuery?.subjectName ?? getValidParam(router?.query?.eventSubjectName || router?.query?.subject_name, '')
  );
  const [selectedSubjectType, setSelectedSubjectType] = useState(() => getValidParam(router.query.eventSubjectType));
  const [selectedAggregationKey, setSelectedAggregationKey] = useState(() => getInitialAggregationKey());
  const [selectedPriority, setSelectedPriority] = useState(() => defaultQuery?.eventPriority ?? getValidParam(router.query.eventPriority));
  const [selectedDateRange, setSelectedDateRange] = useState(() => getInitialTime());
  const [selectedStatus, setSelectedStatus] = useState(
    () => defaultQuery?.eventStatus ?? getValidParam(router.query.eventStatus || router.query.status)
  );
  const [selectedSource, setSelectedSource] = useState([]);
  const [selectedServiceName, setSelectedServiceName] = useState('');
  const [selectedEventName, setSelectedEventName] = useState('');
  const [searchByLabel, setSearchByLabel] = useState('');
  const [selectedNbStatus, setSelectedNbStatus] = useState([]);
  const [selectedSortBy, setSelectedSortBy] = useState(() => getValidParam(router.query.sortBy) || 'created_at');
  const [selectedIssueType, setSelectedIssueType] = useState(() => getValidParam(router.query.issueType) || 'all');

  // UI Toggles & Popups
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});
  const [showTrendChart, setShowTrendChart] = useState(enableTrendChart);
  const [trendChartData, setTrendChartData] = useState({ data: [], labels: [] });
  const [isTrendChartLoading, setIsTrendChartLoading] = useState(false);
  const [selectedEvent, setSelectedEvent] = useState({});
  const [isClassifyModalOpen, setIsClassifyModalOpen] = useState(false);

  // --- Hooks ---

  // Custom Hook for Filters
  const { accounts, accountType, namespaceFilter, workloadFilter, subjectTypeFilter, aggregationKeyFilter, sourceFilter, isOptionsLoading } =
    useKubernetesEventFilters({
      selectedAccountId,
      isTroubleshootPage,
      enableFilters,
      disabledFilters,
      resource_ids,
      selectedNamespace,
    });

  // Cloud Filters Hook
  const { serviceNamesFilter, eventNamesFilter } = useEventCloudFilter(selectedAccountId, {
    subjectNamespace: selectedServiceName,
  });

  const areFiltersDisabled = isTroubleshootPage && !selectedAccountId;

  // --- Effects ---

  // Intentionally depends only on sourceFilter (not router?.query?.source).
  // Purpose: initialize selectedSource once from the URL query when filter options first load.
  // Omitting router?.query?.source from deps prevents a re-run loop:
  // onSourceFilterChange → applyFiltersOnRouter updates the query → useEffect would fire again
  // even though state is already correct. After initialization, the handler owns the state.
  useEffect(() => {
    setSelectedSource(syncFilterFromQuery(sourceFilter, router?.query?.source, (f) => f.value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceFilter]);

  // NB_STATUS_FILTER is a stable module-level constant so this runs once on mount,
  // reading the router query at that point to initialize. Same pattern as selectedSource.
  useEffect(() => {
    setSelectedNbStatus(syncFilterFromQuery(NB_STATUS_FILTER, router?.query?.nbStatus, (f) => f.value));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (accountId) {
      setSelectedAccountId(accountId);
    }
  }, [accountId]);

  useEffect(() => {
    if (isTroubleshootPage) {
      setTableColumns(troubleshootColumns);
    } else {
      setTableColumns(initialTableColumns);
    }
  }, [isTroubleshootPage, initialTableColumns, troubleshootColumns]);

  const currentHeader = useMemo(() => {
    return tableColumns.map((item) => {
      if (item?.name) {
        return {
          name: item.name,
          sortEnabled: item?.sortEnabled,
          width: item?.width,
          exportEnabled: item?.exportEnabled ?? true,
          info: item?.info,
          infoPlacement: item?.infoPlacement,
          component: item?.component,
          ...(item?.defaultVisible !== undefined && { defaultVisible: item.defaultVisible }),
        };
      }
      return { name: item, sortEnabled: false, width: '10%' };
    });
  }, [tableColumns]);

  useEffect(() => {
    if (defaultQuery?.aggregation_key) {
      setSelectedAggregationKey((prev) => {
        const newValue = getInitialAggregationKey();
        return JSON.stringify(prev) === JSON.stringify(newValue) ? prev : newValue;
      });
    }
  }, [JSON.stringify(defaultQuery?.aggregation_key)]);

  // --- Filter Handlers ---

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  const onAccountFilterChange = (e) => {
    setSelectedAccountId(e.target.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { accountId: e.target.value });
  };

  const onNamespaceFilterChange = (e, _p) => {
    setSelectedWorkload('');
    setSelectedNamespace(e?.target?.value);
    // Note: Workload filtering is handled automatically by the hook based on selectedNamespace
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventNamespace: e?.target?.value, eventSubjectName: '' });
  };

  const onWorkloadFilterChange = (e) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
  };

  const onTypeFilterChange = (e, _p) => {
    setSelectedSubjectType(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectType: e?.target?.value });
  };

  const onAggregationKeyFilterChange = (_e, _p) => {
    if (_p && _p.length > 0) {
      setSelectedAggregationKey(_p);
    } else {
      setSelectedAggregationKey([]);
    }
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventAggregationKey: _p?.map((v) => v.value) });
  };

  const onPriorityFilterChange = (e, _p) => {
    setSelectedPriority(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventPriority: e?.target?.value });
  };

  const onStatusFilterChange = (e, _p) => {
    setSelectedStatus(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventStatus: e?.target?.value });
  };

  const onSourceFilterChange = (e, _p) => {
    const value = e?.target?.value ?? [];
    setSelectedSource(value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { source: value.map((s) => s.value).join(',') });
  };

  const onNbStatusFilterChange = (e, _p) => {
    const value = e?.target?.value ?? [];
    setSelectedNbStatus(value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { nbStatus: value.map((v) => v.value).join(',') });
  };

  const onSortByChange = (e, _p) => {
    setSelectedSortBy(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { sortBy: e?.target?.value });
  };

  const onServiceNamesFilterChange = (e) => {
    setSelectedServiceName(e?.target?.value);
    setCurrentPage(0);
  };

  const onEventNamesFilterChange = (e) => {
    setSelectedEventName(e?.target?.value || '');
    setCurrentPage(0);
  };

  // --- Ticket & Menu Handlers ---

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data) => {
    let description = '';
    description += '**Title**: ' + data.title + '\n';
    description += '**Priority**: ' + data.priority + '\n';
    description += '**Aggregation Key**: ' + data.aggregation_key + '\n';
    description += '**Subject Type**: ' + data.subject_type + '\n';
    description += '**Subject Name**: ' + data.subject_name + '\n';
    description += '**Subject Namespace**: ' + data.subject_namespace + '\n';
    return description;
  };

  const getMenuItems = (item, disableTicket) => {
    let MENU_ITEMS;
    if (hasWriteAccess(item.account_id)) {
      MENU_ITEMS = [
        {
          icon: TicketsIcon,
          label: 'Create Ticket',
          id: 0,
          disabled: disableTicket,
        },
        {
          icon: ClassifyIcon,
          label: 'Classify',
          id: 4,
        },
        {
          icon: WorkflowIcon,
          label: 'Create Automation',
          id: 5,
        },
      ];
    } else {
      MENU_ITEMS = [
        {
          icon: TicketsIcon,
          label: 'Create Ticket',
          id: 0,
          disabled: disableTicket,
        },
      ];
    }
    return MENU_ITEMS;
  };

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
    if (menuItem.id == 4) {
      setSelectedEvent(data);
      setIsClassifyModalOpen(true);
    }
    if (menuItem.id === 5) {
      const accountId = data.account_id || router.query.accountId;
      router.push(`/workflow/new?accountId=${accountId}&returnUrl=${encodeURIComponent(router.asPath)}`);
    }
  };

  const handleTicketSuccess = () => {
    listEvents();
  };

  const handleTicketFailure = (res) => {
    snackbar.error(`Failed! ${res}`);
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setCurrentPage(0);
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    applyFiltersOnRouter(router, { start_time: passedSelectedDateTime.startTime, end_time: passedSelectedDateTime.endTime });
  };

  const onSearchLabelFilter = (e) => {
    setCurrentPage(0);
    setSearchByLabel(e.target.value);
  };

  const onEnterPress = () => {
    listEvents();
  };

  const handleClearFilters = () => {
    setSearchByLabel('');
    setCurrentPage(0);
  };

  // --- Data Fetching ---

  const listEvents = () => {
    if (!selectedAccountId && !isTroubleshootPage) {
      return;
    }
    setData([]);
    setTotalCount([]);
    let query = {
      exact_subject_name_search: getValidParam(router.query?.exact) === 'true',
    };

    if (selectedAccountId) {
      query.account_id = selectedAccountId;
    }

    if (defaultQuery) {
      query = { ...query, ...defaultQuery };
    }
    if (selectedNamespace) {
      query.subject_namespace = selectedNamespace;
    }
    if (selectedSubjectType) {
      query.subject_type = selectedSubjectType;
    }
    if (selectedAggregationKey?.length > 0) {
      query.aggregation_key = selectedAggregationKey.map((f) => f.value || f);
    }
    if (selectedPriority) {
      query.priority = selectedPriority;
    }
    if (selectedStatus) {
      query.status = selectedStatus;
    }
    if (selectedWorkload) {
      query.subject_name = selectedWorkload;
    }
    if (selectedSource && selectedSource.length > 0) {
      query.source = selectedSource?.map((e) => e.value);
    }
    if (defaultQuery.startDate) {
      query.startDate = new Date(defaultQuery.startDate);
    } else if (selectedDateRange?.startDate) {
      query.startDate = new Date(selectedDateRange.startDate);
    }
    if (defaultQuery.endDate) {
      query.endDate = new Date(defaultQuery.endDate);
    } else if (selectedDateRange?.endDate) {
      query.endDate = new Date(selectedDateRange.endDate);
    }
    if (resource_ids.length) {
      query.resource_ids = resource_ids;
    }
    if (searchByLabel) {
      query.searchByLabel = parseKeyValueStringToJSON(searchByLabel);
    } else if (defaultQuery?.searchByLabel) {
      // Support searchByLabel from defaultQuery (e.g., from drilldown queries)
      query.searchByLabel = defaultQuery.searchByLabel;
    }
    if (accountType === 'AWS' || accountType === 'GCP' || accountType === 'Azure') {
      if (selectedServiceName) {
        query.subject_namespace = selectedServiceName;
      }
      if (selectedEventName) {
        query.aggregation_key = selectedEventName;
      }
    }
    if (selectedNbStatus && selectedNbStatus.length > 0) {
      query.nb_status = selectedNbStatus.map((s) => s?.value || s);
    }
    if (selectedSortBy) {
      query.sort_by = selectedSortBy;
      query.sort_order = 'desc';
    }
    if (selectedIssueType === 'new') {
      query.is_new_issue = true;
    } else if (selectedIssueType === 'recurring') {
      query.is_new_issue = false;
    }
    setLoading(true);

    // Build row data from events + ticket map
    const buildRowData = (events, ticketReferenceMap) => {
      return events?.map((item) => {
        const row = [];
        const headersArray = currentHeader.map((item) => item.name);

        if (headersArray.includes('Severity')) {
          row.push({
            component: (
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                  gap: '0px',
                  '@media(max-width: 1100px)': {
                    '& p': {
                      fontSize: '14px',
                    },
                  },
                }}
              >
                <SeverityIcon severityType={item.priority} />
                <Datetime value={item.created_at || item.starts_at} sx={{ fontSize: '11px' }} />
              </Box>
            ),
            data: item.priority,
          });
        }

        if (headersArray.includes('Application')) {
          const account = isTroubleshootPage ? accounts.find((acc) => (acc.id || acc.value) === item.account_id) : null;
          const cloudProvider = account?.cloud_provider || accountType;
          const namespaceLabel = cloudProvider && cloudProvider !== 'K8s' ? 'svc' : 'ns';
          row.push({
            component: (
              <Box
                sx={{
                  '@media(max-width: 1100px)': {
                    '& p': {
                      fontSize: '14px',
                    },
                  },
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: '6px' }}>
                  <Text showAutoEllipsis value={item.subject_name} />
                  {item.is_new_issue && <NewIssueChip firstSeenAt={item.fingerprint_first_seen_at} />}
                </Box>
                {isTroubleshootPage && account && (
                  <Text value={`acc: ${account?.label || account?.account_name || item.account_id}`} secondaryText showAutoEllipsis />
                )}
                {item.subject_namespace && <Text value={`${namespaceLabel}: ${item.subject_namespace}`} secondaryText showAutoEllipsis />}
              </Box>
            ),
          });
        }
        if (headersArray.includes('Message') || headersArray.includes('Title')) {
          row.push({
            component: ClusterNameWithRegion({
              name: item.title,
              hideIcon: true,
              smallScreenWidth: '120px',
              maxWidth: '100%',
              showAutoEllipsis: true,
              lineClamp: 3,
              showTooltip: false,
              cursorPointer: false,
              wordBreak: true,
              font: '12px',
              sx: {
                fontStyle: 'italic',
              },
              region: (
                <>
                  {ticketReferenceMap.has(item.fingerprint) && (
                    <CustomTicketLink
                      ticketURL={ticketReferenceMap.get(item.fingerprint)?.url}
                      ticketID={ticketReferenceMap.get(item.fingerprint)?.ticket_id}
                    />
                  )}
                  {item.pr_url && <CustomPRLink prURL={item.pr_url} />}
                </>
              ),
            }),
            drilldownQuery: { workloadName: item.workload_name, namespaceName: item.namespace_name, id: item.id },
            data: item.title,
          });
        }
        if (headersArray.includes('Event Type')) {
          row.push({
            component: <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />,
            data: item.aggregation_key,
          });
        }
        if (headersArray.includes('Triage Score')) {
          row.push({
            component: (
              <Box sx={{ justifySelf: 'center' }}>
                <ScoreDisplay
                  score={item.computed_score}
                  priority={item.computed_priority}
                  scoreFactors={item.score_factors}
                  confidence={item.score_confidence}
                />
              </Box>
            ),
          });
        }
        if (headersArray.includes('Alert Status')) {
          row.push({
            component: (
              <Box
                sx={{
                  display: 'flex',
                  flexDirection: 'column',
                  alignItems: 'center',
                }}
              >
                <CustomLabels
                  margin='0'
                  text={item.status === 'FIRING' ? 'Open' : item.status === 'CLOSED' ? 'Closed' : item.status}
                  variant={item.status === 'FIRING' ? 'red' : item.status === 'CLOSED' ? 'grey' : ''}
                />
              </Box>
            ),
          });
        }
        if (headersArray.includes('Error Type')) {
          const alertData = safeJSONParse(item.labels) || '{}';
          if (alertData && Object.keys(alertData).length > 0) {
            const navigateUrl = !router.pathname.includes('/kubernetes/details')
              ? `kubernetes/details/${item.account_id}?name=${item.aggregation_key}#monitoring/alert-manager`
              : `${item.account_id}?name=${item.aggregation_key}#monitoring/alert-manager`;
            row.push({
              text: (
                <Box
                  sx={{
                    minWidth: showEllipsis && '150px',
                    '@media(max-width: 1100px)': {
                      '& p': {
                        fontSize: '14px',
                      },
                    },
                  }}
                >
                  <CustomLink style={{ textDecoration: 'none', display: 'inline-flex' }} target={'_blank'} href={navigateUrl} openInNew={true}>
                    <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />
                  </CustomLink>
                </Box>
              ),
            });
          } else {
            row.push({
              text: (
                <Box
                  sx={{
                    minWidth: showEllipsis && '150px',
                    '@media(max-width: 1100px)': {
                      '& p': {
                        fontSize: '14px',
                      },
                    },
                  }}
                >
                  <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />
                </Box>
              ),
            });
          }
        }
        if (headersArray.includes('Triage Status')) {
          row.push({
            component: (
              <CustomTooltip variant='default' title={getTriageStatusTooltip(item.nb_status || 'OPEN', item.snoozed_until)} placement='top'>
                <Box>
                  <NBStatusBadge
                    eventId={item.id}
                    currentStatus={item.nb_status || 'OPEN'}
                    snoozedUntil={item.snoozed_until}
                    onStatusChange={() => listEvents()}
                    onCreateTicket={() => {
                      setTicketData(item);
                      setIsTicketCreateFormOpen(true);
                    }}
                    disableTooltip
                  />
                </Box>
              </CustomTooltip>
            ),
            data: item.nb_status || 'OPEN',
          });
        }
        row.push({
          component: item.aggregation_key && (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'center'}>
              <InvestigateButton displayText url={`/investigate?id=${item.id}&accountId=${item.account_id}`} />
              <ThreeDotsMenu
                sx={{ ...action.primary }}
                menuItems={getMenuItems(item, ticketReferenceMap.has(item.fingerprint))}
                data={item}
                onMenuClick={onMenuClick}
              />
            </Box>
          ),
          exportEnabled: false,
        });
        return row;
      });
    };

    // Fire data query (onlyData skips count) and count query in parallel
    const dataQuery = { ...query, onlyData: true };
    const dataPromise = k8sApi.getK8sEvents(rowsPerPage, currentPage * rowsPerPage, dataQuery);
    const countPromise = k8sApi.getK8sEventsCount(query);

    // Data + tickets chain: once data arrives, fetch ticket summaries, then render
    const dataAndTicketsPromise = dataPromise.then((res) => {
      const events = res.data?.events || [];
      const uniqueReferenceIds = new Set();
      events.forEach((item) => {
        uniqueReferenceIds.add(item.fingerprint);
      });
      const references = Array.from(uniqueReferenceIds);

      return ticketsApi.listTicketsSummary({ reference_id: references }).then((ticketRes) => {
        const ticketReferenceMap = new Map();
        ticketRes?.data?.tickets?.forEach((element) => {
          ticketReferenceMap.set(element.reference_id, element);
        });
        const data = buildRowData(events, ticketReferenceMap);
        setData(data);
        setLoading(false);
      });
    });

    // Count updates independently (doesn't block table rendering)
    countPromise.then((countRes) => {
      setTotalCount(countRes.count);
    });

    // Handle errors from the data chain
    dataAndTicketsPromise.catch(() => {
      setLoading(false);
    });
  };

  useEffect(() => {
    if (isTroubleshootPage) {
      if (accounts.length > 0) {
        listEvents();
      }
    } else {
      listEvents();
    }
  }, [
    selectedAccountId,
    currentPage,
    rowsPerPage,
    selectedNamespace,
    selectedWorkload,
    selectedSubjectType,
    selectedAggregationKey,
    selectedPriority,
    selectedDateRange,
    selectedStatus,
    JSON.stringify(defaultQuery),
    JSON.stringify(resource_ids),
    selectedSource,
    isTroubleshootPage,
    accounts.length,
    selectedServiceName,
    selectedEventName,
    selectedNbStatus,
    selectedSortBy,
    selectedIssueType,
  ]);

  useEffect(() => {
    if (!selectedAccountId && !isTroubleshootPage) {
      return;
    }
    if (!showTrendChart) {
      return;
    }
    let query = {
      subject_namespace: selectedNamespace,
      subject_type: selectedSubjectType,
      aggregation_key: selectedAggregationKey,
      priority: selectedPriority,
      start_date: selectedDateRange.startDate,
      end_date: selectedDateRange.endDate,
      status: selectedStatus,
    };

    if (selectedAccountId) {
      query.account_id = selectedAccountId;
    }

    if (selectedDateRange?.startDate) {
      query.start_date = new Date(selectedDateRange.startDate);
    }
    if (selectedDateRange?.endDate) {
      query.end_date = new Date(selectedDateRange.endDate);
    }
    if (selectedWorkload) {
      query.subject_name = selectedWorkload;
    }
    if (resource_ids.length) {
      query.resource_ids = resource_ids;
    }
    if (defaultQuery) {
      query = { ...query, ...defaultQuery };
    }
    setIsTrendChartLoading(true);
    k8sApi
      .getK8sEventGroupings(1000, 0, query)
      .then((res) => {
        let data = [];
        let labels = [];

        res.data.event_groupings.forEach((item) => {
          data.push(item.event_count);
          labels.push(getDateString(item.created_at));
        });
        setTrendChartData({
          data: data,
          labels: labels,
        });
      })
      .finally(() => {
        setIsTrendChartLoading(false);
      });
  }, [
    selectedAccountId,
    selectedNamespace,
    selectedWorkload,
    selectedSubjectType,
    selectedAggregationKey,
    selectedPriority,
    selectedStatus,
    selectedDateRange,
    showTrendChart,
    isTroubleshootPage,
    selectedAccountId,
  ]);

  return (
    <>
      {isClassifyModalOpen && (
        <EventClassifyModal
          open={isClassifyModalOpen}
          handleClose={() => {
            setIsClassifyModalOpen(false);
            setSelectedEvent({});
          }}
          event={{
            id: selectedEvent?.id,
            title: selectedEvent?.title,
            fingerprint: selectedEvent?.fingerprint,
            accountId: selectedEvent?.account_id || selectedAccountId,
          }}
          onSuccess={() => {
            setIsClassifyModalOpen(false);
            setSelectedEvent({});
            listEvents();
          }}
        />
      )}
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + ticketData?.title,
          description: getTicketDescription(ticketData),
          accountId: ticketData?.account_id,
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.id}&accountId=${ticketData?.account_id}` }}
        reference={{
          id: ticketData?.fingerprint,
          type: 'kubernetes',
        }}
      />
      <BoxLayout2
        id='all-events'
        filterOptions={
          enableFilters
            ? [
                ...(isTroubleshootPage
                  ? [
                      {
                        type: 'dropdown',
                        enabled: true,
                        grouped: true,
                        groupIcon: renderAccountGroupIcon,
                        options: accounts.map((acc) => ({
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
                ...(accountType === 'K8s'
                  ? [
                      ...(!isTroubleshootPage
                        ? [
                            {
                              type: 'search',
                              enabled: !disabledFilters.includes('search_labels'),
                              onSelect: onSearchLabelFilter,
                              label: 'Search By Alert Labels',
                              onEnter: onEnterPress,
                              minWidth: '220px',
                              maxWidth: '220px',
                              value: searchByLabel,
                              onClear: handleClearFilters,
                            },
                          ]
                        : []),
                      {
                        type: 'dropdown',
                        enabled: !disabledFilters.includes('namespace') && !areFiltersDisabled,
                        options: ensureSelectedInOptions(namespaceFilter, selectedNamespace),
                        onSelect: onNamespaceFilterChange,
                        label: 'Namespace',
                        value: selectedNamespace,
                        isOptionsLoading: isOptionsLoading.namespace,
                      },
                      {
                        type: 'dropdown',
                        enabled: !disabledFilters.includes('workload') && !areFiltersDisabled,
                        options: ensureSelectedInOptions(workloadFilter, selectedWorkload),
                        onSelect: onWorkloadFilterChange,
                        label: 'Workload',
                        value: selectedWorkload,
                        isOptionsLoading: isOptionsLoading.workload,
                      },
                      {
                        type: 'dropdown',
                        enabled: !disabledFilters.includes('subjectType') && !areFiltersDisabled,
                        options: ensureSelectedInOptions(subjectTypeFilter, selectedSubjectType),
                        onSelect: onTypeFilterChange,
                        label: 'Subject Type',
                        value: selectedSubjectType,
                        isOptionsLoading: isOptionsLoading.subjectType,
                      },
                      {
                        type: 'multi-dropdown',
                        enabled: !disabledFilters.includes('aggregationKey'),
                        options: ensureSelectedInOptions(aggregationKeyFilter, selectedAggregationKey),
                        onSelect: onAggregationKeyFilterChange,
                        label: 'Event Type',
                        value: selectedAggregationKey,
                        isOptionsLoading: isOptionsLoading.aggregationKey,
                      },
                    ]
                  : []),
                ...(accountType === 'AWS' || accountType === 'GCP' || accountType === 'Azure'
                  ? [
                      {
                        type: 'dropdown',
                        enabled: true,
                        options: ensureSelectedInOptions(eventNamesFilter, selectedEventName),
                        onSelect: onEventNamesFilterChange,
                        label: 'Event Name',
                        value: selectedEventName,
                      },
                      {
                        type: 'dropdown',
                        enabled: true,
                        options: ensureSelectedInOptions(serviceNamesFilter, selectedServiceName),
                        onSelect: onServiceNamesFilterChange,
                        label: 'Service Name',
                        value: selectedServiceName,
                      },
                    ]
                  : []),
                {
                  type: 'dropdown',
                  enabled: !disabledFilters.includes('priority'),
                  options: priorityFilter,
                  onSelect: onPriorityFilterChange,
                  label: 'Severity',
                  value: selectedPriority,
                },
                {
                  type: 'dropdown',
                  enabled: !disabledFilters.includes('status'),
                  options: statusFilter,
                  onSelect: onStatusFilterChange,
                  label: 'Status',
                  value: selectedStatus,
                },
                {
                  type: 'multi-dropdown',
                  enabled: !disabledFilters.includes('source'),
                  options: sourceFilter,
                  onSelect: onSourceFilterChange,
                  label: 'Source',
                  value: selectedSource,
                  isOptionsLoading: isOptionsLoading.source,
                },
                {
                  type: 'multi-dropdown',
                  enabled: !disabledFilters.includes('nbStatus'),
                  options: NB_STATUS_FILTER,
                  onSelect: onNbStatusFilterChange,
                  label: 'Triage Status',
                  value: selectedNbStatus,
                },
                {
                  type: 'dropdown',
                  enabled: !disabledFilters.includes('sortBy'),
                  options: sortByOptions,
                  onSelect: onSortByChange,
                  label: 'Sort By',
                  value: selectedSortBy,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: [
                    { value: 'all', label: 'All Issues' },
                    { value: 'new', label: 'New Issues' },
                    { value: 'recurring', label: 'Recurring Issues' },
                  ],
                  onSelect: (e) => {
                    setSelectedIssueType(e.target.value);
                    setCurrentPage(0);
                    applyFiltersOnRouter(router, { issueType: e.target.value === 'all' ? '' : e.target.value });
                  },
                  label: 'Issue Type',
                  value: selectedIssueType,
                },
              ]
            : []
        }
        dateTimeRange={{
          enabled: showTimeFilter,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: selectedDateRange.shortcutClickTime || 0,
          },
        }}
        minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: kubernetesEventsTable,
                fileName: 'event.csv',
              };
            },
          },
          sharing: { enabled: true },
        }}
        extraOptions={[
          <FormControlLabel
            sx={{ gap: 1, marginRight: '4px', '& .MuiFormControlLabel-label': { fontSize: '13px', width: 'max-content' } }}
            control={<CustomSwitch id='showTrend' checked={showTrendChart} onChange={(e) => setShowTrendChart(e.target.checked)} />}
            label='Show Trend'
            key='showTrend'
          />,
        ]}
        onRefresh={{
          enabled: true,
          loading: loading,
          text: '',
          onClick: () => {
            {
              listEvents();
            }
          },
        }}
      >
        {showTrendChart && <LineChart data={trendChartData.data} labels={trendChartData.labels} loading={isTrendChartLoading} />}
        <KubernetesTable2
          id={kubernetesEventsTable}
          headers={currentHeader}
          data={data}
          sort={{
            name: 'Alert Status',
            order: 'desc',
          }}
          onSortChange={undefined}
          showExpandable={false}
          rowsPerPage={rowsPerPage}
          onPageChange={onPageChange}
          totalRows={totalCount}
          loading={loading}
          rounded={'10px'}
          pageNumber={currentPage + 1}
          tableHeadingCenter={['Severity', 'NB Priority', 'Triage Score', 'Alert Status', 'Triage Status', 'Action']}
          stickyColumnIndex={stickyColumnIndex}
          resizableColumns
        />
      </BoxLayout2>
    </>
  );
};

KubernetesEventsTable.propTypes = {
  accountId: PropTypes.string,
  recordsPerPage: PropTypes.number,
  defaultQuery: PropTypes.object,
  enableFilters: PropTypes.bool,
  disabledFilters: PropTypes.arrayOf(PropTypes.string),
  enableTrendChart: PropTypes.bool,
  heading: PropTypes.string,
  podAllTabRadio: PropTypes.node,
  tableColumns: PropTypes.arrayOf(
    PropTypes.oneOfType([
      PropTypes.string,
      PropTypes.shape({
        name: PropTypes.string.isRequired,
        width: PropTypes.string,
        sortEnabled: PropTypes.bool,
      }),
    ])
  ),
  stickyColumnIndex: PropTypes.oneOfType([PropTypes.string, PropTypes.number]),
  resource_ids: PropTypes.arrayOf(PropTypes.string),
  showTimeFilter: PropTypes.bool,
  isTroubleshootPage: PropTypes.bool,
};

export default KubernetesEventsTable;
