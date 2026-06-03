import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/router';
import PropTypes from 'prop-types';
import RefreshIcon from '@mui/icons-material/Refresh';
import { Box } from '@mui/material';
import { FiArrowRight } from 'react-icons/fi';

// DS V2 primitives
import ListingLayout from '@components1/ds/ListingLayout';
import { Button as DsButton } from '@components1/ds/Button';
import { Switch as DsSwitch } from '@components1/ds/Switch';
import SeverityIcon from '@components1/ds/SeverityIcon';
import NBStatusBadge from '@common-new/widgets/NBStatusBadge';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';

// DS V2 compositions / format / widgets
import Datetime from '@common-new/format/Datetime';
import Text from '@common-new/format/Text';
import { Label } from '@components1/ds/Label';
import CustomTicketLink from '@common-new/CustomTicketLink';
import ThreeDotsMenu from '@common-new/ThreeDotsMenu';
import { toast as snackbar } from '@components1/ds/Toast';
import ScoreDisplay from '@common-new/widgets/ScoreDisplay';
import NewIssueChip from '@common-new/widgets/NewIssueChip';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';

// MUI icons used by the toolbar action cluster
import FileDownloadOutlinedIcon from '@mui/icons-material/FileDownloadOutlined';
import IosShareOutlinedIcon from '@mui/icons-material/IosShareOutlined';

// TODO(ds-migration): no v2 yet — track in V2_GAPS follow-up
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import LineChart from '@components1/common/charts/LineCharts';
import CloudProviderIcon from '@components1/common/CloudIcon';
import CustomPRLink from '@components1/common/CustomPRLink';
import { Link } from '@components1/ds/Link';
import SafeIcon from '@components1/common/SafeIcon';
import Tooltip from '@components1/ds/Tooltip';
import EventClassifyModal from './EventClassifyModal';

// API & Utils
import k8sApi from '@api1/kubernetes';
import ticketsApi from '@api1/tickets';
import apiUser from '@api1/user';
import { getDateString, getLast24Hrs } from '@lib/datetime';
import { hasWriteAccess } from '@lib/auth';
import { safeJSONParse, titleCaseForAggregationKey, syncFilterFromQuery, toSeverityLevel } from 'src/utils/common';
import { applyFiltersOnRouter } from '@lib/router';
import { action } from 'src/utils/actionStyles';
import { ds } from 'src/utils/colors';

import { useEventCloudFilter } from '@hooks/useCloudFilters';

// Assets
import TicketsIcon from '@assets/sidebar-icon/tickets-icon.svg';
import { dashboardIcon1 as ClassifyIcon, infoIcon } from '@assets';
import { getTriageStatusTooltip } from '@api1/triage';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';
import { readPersistedFilters, writePersistedFilters } from '@hooks/usePersistedFilters';
import WorkflowIcon from '@assets/WorkflowIcon';

// localStorage key for the Troubleshoot Events tab. Bump the suffix when the
// shape of persisted filter values changes so old entries are ignored.
const TROUBLESHOOT_EVENTS_FILTER_STORAGE_KEY = 'troubleshoot:events:filters:v1';

// Known shortcut durations from CustomDateTimeRangePicker, kept in sync so we can
// rehydrate a relative time selection ("Current Week", "Last 24 Hours", ...) by
// recomputing from `now` instead of restoring stale absolute timestamps.
const KNOWN_SHORTCUT_DURATIONS_MS = new Set([
  5 * 60 * 1000,
  10 * 60 * 1000,
  15 * 60 * 1000,
  30 * 60 * 1000,
  60 * 60 * 1000,
  3 * 60 * 60 * 1000,
  6 * 60 * 60 * 1000,
  12 * 60 * 60 * 1000,
  24 * 60 * 60 * 1000,
  7 * 24 * 60 * 60 * 1000,
]);
const CURRENT_WEEK_MS = 7 * 24 * 60 * 60 * 1000;

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
      <Tooltip
        variant='interactive'
        title='Triage Status'
        desc="Your team's response to this issue. Update it as you investigate, escalate, or resolve. To handle matching issues automatically, go to"
        linkText='Triage Rules →'
        linkUrl='/troubleshoot#triage-rules'
        placement='top'
      >
        <Box component='span' sx={{ cursor: 'default', display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
          Triage Status
          <Box component='span' sx={{ position: 'relative', top: ds.space[0], opacity: '50%' }}>
            <SafeIcon src={infoIcon} alt='info' width={12} height={14} />
          </Box>
        </Box>
      </Tooltip>
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

  // Persist filter selections only on the Troubleshoot Events tab. Sidebar nav
  // strips query params (layout/index.jsx forces /troubleshoot#all-events), so
  // localStorage is what survives a "leave + come back" round-trip. All other
  // call sites (PodsDetails, KubernetesTable2 expand, SLO configs, threshold
  // evidence) keep the legacy URL-only behavior.
  const persistKey = isTroubleshootPage ? TROUBLESHOOT_EVENTS_FILTER_STORAGE_KEY : null;
  // Read once on mount; precedence is URL query > localStorage > component default.
  const persisted = useMemo(() => readPersistedFilters(persistKey), [persistKey]);

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
    // Persisted relative shortcut → recompute from now to avoid serving stale
    // absolute timestamps. Custom (non-shortcut) absolute ranges are only
    // restored if the saved end date is still in the past 30 days; older
    // saved ranges fall through to the per-page default.
    const persistedShortcut = persisted?.shortcutClickTime;
    if (typeof persistedShortcut === 'number' && KNOWN_SHORTCUT_DURATIONS_MS.has(persistedShortcut)) {
      const now = Date.now();
      return { startDate: now - persistedShortcut, endDate: now, shortcutClickTime: persistedShortcut };
    }
    if (
      typeof persisted?.startDate === 'number' &&
      typeof persisted?.endDate === 'number' &&
      Date.now() - persisted.endDate < 30 * 24 * 60 * 60 * 1000
    ) {
      return { startDate: persisted.startDate, endDate: persisted.endDate };
    }
    if (isTroubleshootPage) {
      const now = Date.now();
      return { startDate: now - CURRENT_WEEK_MS, endDate: now, shortcutClickTime: CURRENT_WEEK_MS };
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
    if (selectedKeys.length === 0 && Array.isArray(persisted?.aggregationKey)) {
      selectedKeys = persisted.aggregationKey.filter((v) => v != null && v !== '').map((v) => ({ value: v }));
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
          <Tooltip
            variant='interactive'
            title='Triage Status'
            desc="Your team's response to this issue. Update it as you investigate, escalate, or resolve. To handle matching issues automatically, go to"
            linkText='Triage Rules →'
            linkUrl='/troubleshoot#triage-rules'
            placement='top'
          >
            <Box component='span' sx={{ cursor: 'default', display: 'inline-flex', alignItems: 'center', gap: 'var(--ds-space-1)' }}>
              Triage Status
              <Box component='span' sx={{ position: 'relative', top: ds.space[0], opacity: '50%' }}>
                <SafeIcon src={infoIcon} alt='info' width={12} height={14} />
              </Box>
            </Box>
          </Tooltip>
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

  // Selections — precedence: explicit prop / defaultQuery > URL query > persisted > built-in default
  const [selectedAccountId, setSelectedAccountId] = useState(() => {
    const raw = accountId || router.query.accountId;
    if (raw) return String(raw).split(',').filter(Boolean);
    if (Array.isArray(persisted?.accountId) && persisted.accountId.length > 0) return persisted.accountId;
    return [];
  });
  const [selectedNamespace, setSelectedNamespace] = useState(
    () => defaultQuery?.namespace ?? getValidParam(router?.query?.namespace) ?? getValidParam(router?.query?.eventNamespace) ?? persisted?.namespace
  );
  const [selectedWorkload, setSelectedWorkload] = useState(
    () =>
      defaultQuery?.workloadName ??
      defaultQuery?.subjectName ??
      getValidParam(router?.query?.eventSubjectName || router?.query?.subject_name, '') ??
      persisted?.workload ??
      ''
  );
  const [selectedSubjectType, setSelectedSubjectType] = useState(() => getValidParam(router.query.eventSubjectType) ?? persisted?.subjectType);
  const [selectedAggregationKey, setSelectedAggregationKey] = useState(() => getInitialAggregationKey());
  const [selectedPriority, setSelectedPriority] = useState(
    () => defaultQuery?.eventPriority ?? getValidParam(router.query.eventPriority) ?? persisted?.priority
  );
  const [selectedDateRange, setSelectedDateRange] = useState(() => getInitialTime());
  const [selectedStatus, setSelectedStatus] = useState(
    () =>
      defaultQuery?.eventStatus ??
      getValidParam(router.query.eventStatus || router.query.status) ??
      persisted?.status ??
      (isTroubleshootPage ? 'FIRING' : undefined)
  );
  const [selectedSource, setSelectedSource] = useState([]);
  const [selectedServiceName, setSelectedServiceName] = useState(() => persisted?.serviceName ?? '');
  const [selectedEventName, setSelectedEventName] = useState(() => persisted?.eventName ?? '');
  const [searchByLabel, setSearchByLabel] = useState('');
  const [appliedSearchByLabel, setAppliedSearchByLabel] = useState('');
  const [selectedNbStatus, setSelectedNbStatus] = useState([]);
  const [selectedSortBy, setSelectedSortBy] = useState(() => getValidParam(router.query.sortBy) || persisted?.sortBy || 'created_at');
  const [selectedIssueType, setSelectedIssueType] = useState(() => getValidParam(router.query.issueType) || persisted?.issueType || 'all');

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
  const {
    serviceNamesFilter,
    eventNamesFilter,
    isOptionsLoading: cloudOptionsLoading,
  } = useEventCloudFilter(selectedAccountId[0] ?? '', {
    subjectNamespace: selectedServiceName,
  });

  const areFiltersDisabled = isTroubleshootPage && !selectedAccountId.length;

  // --- Effects ---

  // Intentionally depends only on sourceFilter (not router?.query?.source).
  // Purpose: initialize selectedSource once from the URL query when filter options first load.
  // Omitting router?.query?.source from deps prevents a re-run loop:
  // onSourceFilterChange → applyFiltersOnRouter updates the query → useEffect would fire again
  // even though state is already correct. After initialization, the handler owns the state.
  useEffect(() => {
    const fromQuery = syncFilterFromQuery(sourceFilter, router?.query?.source, (f) => f.value);
    if (fromQuery.length > 0) {
      setSelectedSource(fromQuery);
      return;
    }
    if (Array.isArray(persisted?.source) && persisted.source.length > 0) {
      setSelectedSource((prev) => {
        const next = syncFilterFromQuery(sourceFilter, persisted.source.join(','), (f) => f.value);
        if (prev.length === 0 && next.length === 0) return prev;
        return next;
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceFilter]);

  // NB_STATUS_FILTER is a stable module-level constant so this runs once on mount,
  // reading the router query at that point to initialize. Same pattern as selectedSource.
  useEffect(() => {
    const fromQuery = syncFilterFromQuery(NB_STATUS_FILTER, router?.query?.nbStatus, (f) => f.value);
    if (fromQuery.length > 0) {
      setSelectedNbStatus(fromQuery);
      return;
    }
    if (Array.isArray(persisted?.nbStatus) && persisted.nbStatus.length > 0) {
      setSelectedNbStatus((prev) => {
        const next = syncFilterFromQuery(NB_STATUS_FILTER, persisted.nbStatus.join(','), (f) => f.value);
        if (prev.length === 0 && next.length === 0) return prev;
        return next;
      });
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    const raw = accountId || router.query.accountId;
    const next = raw ? String(raw).split(',').filter(Boolean) : [];
    setSelectedAccountId((prev) => {
      if (prev.length === next.length && prev.every((id, i) => id === next[i])) return prev;
      return next;
    });
  }, [accountId, router.query.accountId]);

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

  const onAccountFilterChange = (_e, value) => {
    const ids = (value || []).map((v) => v.value);
    setSelectedAccountId(ids);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { accountId: ids.join(',') });
    writePersistedFilters(persistKey, { accountId: ids });
  };

  const onNamespaceFilterChange = (e, _p) => {
    setSelectedWorkload('');
    setSelectedNamespace(e?.target?.value);
    // Note: Workload filtering is handled automatically by the hook based on selectedNamespace
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventNamespace: e?.target?.value, eventSubjectName: '' });
    writePersistedFilters(persistKey, { namespace: e?.target?.value, workload: '' });
  };

  const onWorkloadFilterChange = (e) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
    writePersistedFilters(persistKey, { workload: e?.target?.value });
  };

  const onTypeFilterChange = (e, _p) => {
    setSelectedSubjectType(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectType: e?.target?.value });
    writePersistedFilters(persistKey, { subjectType: e?.target?.value });
  };

  const onAggregationKeyFilterChange = (_e, _p) => {
    if (_p && _p.length > 0) {
      setSelectedAggregationKey(_p);
    } else {
      setSelectedAggregationKey([]);
    }
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventAggregationKey: _p?.map((v) => v.value) });
    writePersistedFilters(persistKey, { aggregationKey: _p?.map((v) => v.value) ?? [] });
  };

  const onPriorityFilterChange = (e, _p) => {
    setSelectedPriority(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventPriority: e?.target?.value });
    writePersistedFilters(persistKey, { priority: e?.target?.value });
  };

  const onStatusFilterChange = (e, _p) => {
    setSelectedStatus(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventStatus: e?.target?.value });
    writePersistedFilters(persistKey, { status: e?.target?.value });
  };

  const onSourceFilterChange = (e, _p) => {
    const value = e?.target?.value ?? [];
    setSelectedSource(value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { source: value.map((s) => s.value).join(',') });
    writePersistedFilters(persistKey, { source: value.map((s) => s.value) });
  };

  const onNbStatusFilterChange = (e, _p) => {
    const value = e?.target?.value ?? [];
    setSelectedNbStatus(value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { nbStatus: value.map((v) => v.value).join(',') });
    writePersistedFilters(persistKey, { nbStatus: value.map((v) => v.value) });
  };

  const onSortByChange = (e, _p) => {
    setSelectedSortBy(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { sortBy: e?.target?.value });
    writePersistedFilters(persistKey, { sortBy: e?.target?.value });
  };

  const onServiceNamesFilterChange = (e) => {
    setSelectedServiceName(e?.target?.value);
    setCurrentPage(0);
    writePersistedFilters(persistKey, { serviceName: e?.target?.value });
  };

  const onEventNamesFilterChange = (e) => {
    setSelectedEventName(e?.target?.value || '');
    setCurrentPage(0);
    writePersistedFilters(persistKey, { eventName: e?.target?.value || '' });
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
          iconBlack: true,
        },
        {
          icon: ClassifyIcon,
          label: 'Classify',
          id: 4,
          iconBlack: true,
        },
        {
          icon: WorkflowIcon,
          label: 'Create Automation',
          id: 5,
          iconBlack: true,
        },
      ];
    } else {
      MENU_ITEMS = [
        {
          icon: TicketsIcon,
          label: 'Create Ticket',
          id: 0,
          disabled: disableTicket,
          iconBlack: true,
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
      const params = new URLSearchParams({ accountId, returnUrl: router.asPath });
      if (data.aggregation_key) params.set('eventType', data.aggregation_key);
      if (data.priority) params.set('eventPriority', data.priority);
      if (data.source) params.set('eventSource', data.source);
      if (accountId) params.set('eventCluster', accountId);
      if (data.subject_namespace) params.set('eventNamespace', data.subject_namespace);
      router.push(`/workflow/new?${params.toString()}`);
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
    const next = {
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      shortcutClickTime: passedSelectedDateTime.shortcutClickTime ?? 0,
    };
    setSelectedDateRange(next);
    applyFiltersOnRouter(router, { start_time: passedSelectedDateTime.startTime, end_time: passedSelectedDateTime.endTime });
    // Persist the shortcut duration when known so we can rehydrate as a relative
    // range. For a custom range (shortcutClickTime = 0) persist the absolute
    // bounds; getInitialTime() ignores them once they're > 30 days old.
    writePersistedFilters(persistKey, {
      shortcutClickTime: next.shortcutClickTime,
      startDate: next.startDate,
      endDate: next.endDate,
    });
  };

  const handleClearFilters = () => {
    setSearchByLabel('');
    setAppliedSearchByLabel('');
    setCurrentPage(0);
  };

  // CSV export — replaces the v1 DownloadButton DOM scrape. Mirrors the same
  // contract (only cells with data-export-enabled="true" are emitted; cells
  // with data-export-data="..." override innerText) so the table component
  // doesn't need to change. Triggered by the ds/Button below.
  const handleDownloadCsv = () => {
    const oTable = document.getElementById(kubernetesEventsTable);
    if (!oTable) {
      snackbar.error('Nothing to export — table not ready.');
      return;
    }
    const escape = (s) => `"${(s == null ? '' : String(s)).replace(/"/g, '""').replace(/[\r\n]+/g, ' ')}"`;
    let csv = '';
    const headerRows = oTable.querySelectorAll('thead tr');
    const headerRow = headerRows?.[headerRows.length - 1];
    if (headerRow) {
      csv +=
        [...headerRow.children]
          .filter((th) => th.getAttribute('data-export-enabled') !== 'false')
          .map((th) => escape(th.innerText))
          .join(',') + '\r\n';
    }
    const bodyRows = oTable.querySelectorAll('tbody tr') || [];
    for (const tr of bodyRows) {
      const cells = [...tr.children].filter((td) => td.getAttribute('data-export-enabled') === 'true');
      if (cells.length === 0) continue;
      csv += cells.map((td) => escape(td.getAttribute('data-export-data') ?? td.innerText)).join(',') + '\r\n';
    }
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'event.csv';
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
  };

  // --- Data Fetching ---

  const listEvents = () => {
    if (!selectedAccountId.length && !isTroubleshootPage) {
      return;
    }
    setData([]);
    setTotalCount([]);
    let query = {
      exact_subject_name_search: getValidParam(router.query?.exact) === 'true',
    };

    if (selectedAccountId.length) {
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
    if (appliedSearchByLabel) {
      query.searchByLabel = parseKeyValueStringToJSON(appliedSearchByLabel);
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
                  gap: 0,
                  '@media(max-width: 1100px)': {
                    '& p': {
                      fontSize: ds.text.bodyLg,
                    },
                  },
                }}
              >
                <SeverityIcon level={toSeverityLevel(item.priority)} aria-label={`${item.priority || 'unknown'}`} />
                <Datetime value={item.created_at || item.starts_at} sx={{ fontSize: ds.text.caption }} />
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
                      fontSize: ds.text.bodyLg,
                    },
                  },
                }}
              >
                <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
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
              smallScreenWidth: ds.space.mul(0, 60),
              maxWidth: '100%',
              showAutoEllipsis: true,
              lineClamp: 3,
              showTooltip: false,
              cursorPointer: false,
              wordBreak: true,
              font: ds.text.small,
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
                <Label
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
                    minWidth: showEllipsis && ds.space.mul(0, 75),
                    '@media(max-width: 1100px)': {
                      '& p': {
                        fontSize: ds.text.bodyLg,
                      },
                    },
                  }}
                >
                  <Link style={{ textDecoration: 'none', display: 'inline-flex' }} target={'_blank'} href={navigateUrl} openInNew={true}>
                    <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />
                  </Link>
                </Box>
              ),
            });
          } else {
            row.push({
              text: (
                <Box
                  sx={{
                    minWidth: showEllipsis && ds.space.mul(0, 75),
                    '@media(max-width: 1100px)': {
                      '& p': {
                        fontSize: ds.text.bodyLg,
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
              <Tooltip variant='default' title={getTriageStatusTooltip(item.nb_status || 'OPEN', item.snoozed_until)} placement='top'>
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
              </Tooltip>
            ),
            data: item.nb_status || 'OPEN',
          });
        }
        row.push({
          component: item.aggregation_key && (
            <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space[2]} justifyContent={'center'}>
              <DsButton
                tone='secondary'
                size='xs'
                trailingAccent={<FiArrowRight />}
                href={`/investigate?id=${item.id}&accountId=${item.account_id}`}
                data-testid='investigate-btn'
              >
                Investigate
              </DsButton>
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
    appliedSearchByLabel,
  ]);

  useEffect(() => {
    if (!selectedAccountId.length && !isTroubleshootPage) {
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

    if (selectedAccountId.length) {
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
            accountId: selectedEvent?.account_id || selectedAccountId[0],
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
      <ListingLayout id='all-events'>
        <ListingLayout.Toolbar
          actions={
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2] }}>
              {showTimeFilter && (
                <CustomDateTimeRangePicker
                  passedSelectedDateTime={{
                    startTime: selectedDateRange.startDate,
                    endTime: selectedDateRange.endDate,
                    shortcutClickTime: selectedDateRange.shortcutClickTime || 0,
                  }}
                  minDate={new Date(new Date().getFullYear(), new Date().getMonth() - 6, 1)}
                  onChange={({ selection }) => handleDateRangeChange(selection)}
                />
              )}
              <DsSwitch id='showTrend' label='Show Trend' size='sm' checked={showTrendChart} onChange={(_e, checked) => setShowTrendChart(checked)} />
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<IosShareOutlinedIcon />}
                aria-label='Share'
                tooltip='Share (coming soon)'
                id='all-events-share'
                disabled
              />
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<FileDownloadOutlinedIcon />}
                aria-label='Download events as CSV'
                tooltip='Download as CSV'
                id='all-events-download'
                onClick={handleDownloadCsv}
              />
              <DsButton
                tone='secondary'
                size='sm'
                composition='icon-only'
                icon={<RefreshIcon />}
                aria-label='Refresh'
                tooltip='Refresh'
                id='all-events-refresh'
                onClick={() => listEvents()}
                loading={loading}
              />
            </Box>
          }
        >
          {enableFilters && (
            <>
              {isTroubleshootPage && (
                <FilterDropdown
                  id='filter-account'
                  label='Account'
                  multiple
                  grouped
                  groupIcon={renderAccountGroupIcon}
                  selectionWithinGroup
                  options={accounts.map((acc) => ({
                    label: acc.label || acc.account_name,
                    value: acc.id || acc.value,
                    group: acc.cloud_provider || 'Other',
                  }))}
                  value={accounts
                    .filter((acc) => selectedAccountId.includes(acc.id || acc.value))
                    .map((acc) => ({
                      label: acc.label || acc.account_name,
                      value: acc.id || acc.value,
                      group: acc.cloud_provider || 'Other',
                    }))}
                  onSelect={onAccountFilterChange}
                />
              )}

              {accountType === 'K8s' && (!isTroubleshootPage || selectedAccountId.length) ? (
                <>
                  {!isTroubleshootPage && !disabledFilters.includes('search_labels') && (
                    <CustomSearch
                      id='filter-search-labels'
                      label='Search by alert labels'
                      value={searchByLabel}
                      onChange={(next) => {
                        setSearchByLabel((prev) => {
                          if (prev.trim() !== '' && next.trim() === '') {
                            setAppliedSearchByLabel('');
                            setCurrentPage(0);
                          }
                          return next;
                        });
                      }}
                      onEnterPress={() => {
                        setAppliedSearchByLabel(searchByLabel);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        handleClearFilters();
                      }}
                    />
                  )}
                  {!disabledFilters.includes('namespace') && (
                    <FilterDropdown
                      id='filter-namespace'
                      label='Namespace'
                      options={ensureSelectedInOptions(namespaceFilter, selectedNamespace)}
                      value={selectedNamespace}
                      onSelect={onNamespaceFilterChange}
                      disabled={areFiltersDisabled}
                      isOptionsLoading={isOptionsLoading.namespace}
                    />
                  )}
                  {!disabledFilters.includes('workload') && (
                    <FilterDropdown
                      id='filter-workload'
                      label='Workload'
                      options={ensureSelectedInOptions(workloadFilter, selectedWorkload)}
                      value={selectedWorkload}
                      onSelect={onWorkloadFilterChange}
                      disabled={areFiltersDisabled}
                      isOptionsLoading={isOptionsLoading.workload}
                    />
                  )}
                  {!disabledFilters.includes('subjectType') && (
                    <FilterDropdown
                      id='filter-subject-type'
                      label='Subject Type'
                      options={ensureSelectedInOptions(subjectTypeFilter, selectedSubjectType)}
                      value={selectedSubjectType}
                      onSelect={onTypeFilterChange}
                      disabled={areFiltersDisabled}
                      isOptionsLoading={isOptionsLoading.subjectType}
                    />
                  )}
                  {!disabledFilters.includes('aggregationKey') && (
                    <FilterDropdown
                      id='filter-event-type'
                      label='Event Type'
                      multiple
                      options={ensureSelectedInOptions(aggregationKeyFilter, selectedAggregationKey)}
                      value={selectedAggregationKey}
                      onSelect={onAggregationKeyFilterChange}
                      isOptionsLoading={isOptionsLoading.aggregationKey}
                    />
                  )}
                </>
              ) : null}

              {(accountType === 'AWS' || accountType === 'GCP' || accountType === 'Azure') && (!isTroubleshootPage || selectedAccountId.length) ? (
                <>
                  <FilterDropdown
                    id='filter-event-name'
                    label='Event Name'
                    options={ensureSelectedInOptions(eventNamesFilter, selectedEventName)}
                    value={selectedEventName}
                    onSelect={onEventNamesFilterChange}
                    isOptionsLoading={cloudOptionsLoading.aggregationKey}
                  />
                  <FilterDropdown
                    id='filter-service-name'
                    label='Service Name'
                    options={ensureSelectedInOptions(serviceNamesFilter, selectedServiceName)}
                    value={selectedServiceName}
                    onSelect={onServiceNamesFilterChange}
                    isOptionsLoading={cloudOptionsLoading.namespace}
                  />
                </>
              ) : null}

              {!disabledFilters.includes('priority') && (
                <FilterDropdown
                  id='filter-priority'
                  label='Severity'
                  options={priorityFilter}
                  value={selectedPriority}
                  onSelect={onPriorityFilterChange}
                />
              )}
              {!disabledFilters.includes('status') && (
                <FilterDropdown id='filter-status' label='Status' options={statusFilter} value={selectedStatus} onSelect={onStatusFilterChange} />
              )}
              {!disabledFilters.includes('source') && (
                <FilterDropdown
                  id='filter-source'
                  label='Source'
                  multiple
                  options={sourceFilter}
                  value={selectedSource}
                  onSelect={onSourceFilterChange}
                  isOptionsLoading={isOptionsLoading.source}
                />
              )}
              {!disabledFilters.includes('nbStatus') && (
                <FilterDropdown
                  id='filter-nb-status'
                  label='Triage Status'
                  multiple
                  options={NB_STATUS_FILTER}
                  value={selectedNbStatus}
                  onSelect={onNbStatusFilterChange}
                />
              )}
              {!disabledFilters.includes('sortBy') && (
                <FilterDropdown id='filter-sort-by' label='Sort By' options={sortByOptions} value={selectedSortBy} onSelect={onSortByChange} />
              )}
              <FilterDropdown
                id='filter-issue-type'
                label='Issue Type'
                options={[
                  { value: 'all', label: 'All Issues' },
                  { value: 'new', label: 'New Issues' },
                  { value: 'recurring', label: 'Recurring Issues' },
                ]}
                value={selectedIssueType}
                onSelect={(e) => {
                  setSelectedIssueType(e.target.value);
                  setCurrentPage(0);
                  applyFiltersOnRouter(router, { issueType: e.target.value === 'all' ? '' : e.target.value });
                }}
              />
            </>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
            rounded={ds.radius.md}
            pageNumber={currentPage + 1}
            tableHeadingCenter={['Severity', 'NB Priority', 'Triage Score', 'Alert Status', 'Triage Status', 'Action']}
            stickyColumnIndex={stickyColumnIndex}
            resizableColumns
          />
        </ListingLayout.Body>
      </ListingLayout>
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
