import { Box } from '@mui/material';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiCloudAccount from '@api1/cloud-account';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import CustomTable2 from '@common-new/tables/CustomTable2';
import { Button as DsButton } from '@components1/ds/Button';
import { DropdownMenu as DsDropdownMenu } from '@components1/ds/DropdownMenu';
import { SeverityIcon as DsSeverityIcon } from '@components1/ds/SeverityIcon';
import { Label } from '@components1/ds/Label';
import { ds } from 'src/utils/colors';
import MoreVertIcon from '@mui/icons-material/MoreVert';
import HelpBeeModal from '@components1/helpbee';
import { getLast7Days } from '@lib/datetime';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Text from '@common-new/format/Text';
import Datetime from '@common-new/format/Datetime';
import { useEventCloudFilter } from '@hooks/useCloudFilters';
import { syncFilterFromQuery, toSeverityLevel } from '@utils/common';
import { FiArrowRight } from 'react-icons/fi';
import NBStatusBadge from '@common-new/widgets/NBStatusBadge';
import { usePagination } from '@hooks/usePagination';
import { hasWriteAccess } from '@lib/auth';
import { TicketsIcon, dashboardIcon1 as ClassifyIcon, infoIcon } from '@assets';
import ticketsApi from '@api1/tickets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import EventClassifyModal from '@components1/events/EventClassifyModal';
import { snackbar } from '@components1/common/snackbarService';
import ScoreDisplay from '@components1/common/widgets/ScoreDisplay';
import WorkflowIcon from '@assets/WorkflowIcon';
import Tooltip from '@components1/ds/Tooltip';
import CustomTicketLink from '@common-new/CustomTicketLink';
import { getTriageStatusTooltip } from '@api1/triage';
import SafeIcon from '@components1/common/SafeIcon';

const TABLE_COLUMNS = [
  {
    name: 'Severity',
    width: '9%',
    info: "Severity is the original urgency level assigned by the source monitoring/alerting system, based on that tool's built-in rules or your configured thresholds",
  },
  {
    name: 'Application',
    width: '14%',
    info: 'The resource or workload this event belongs to.',
  },
  {
    name: 'Event',
    width: '28%',
    info: 'The type of alert or event reported by the source system.',
  },
  {
    name: 'Triage Score',
    width: '9%',
    info: "Triage Score is NudgeBee's context-aware triage score/level, computed using multiple signals beyond raw thresholds such as service criticality, customer/user impact, recurrence frequency, dependency (upstream/downstream) blast radius, and the nature of the service/workload.",
  },
  {
    name: 'Alert Status',
    width: '10%',
    info: 'Current alert state from your source system. Reflects whether the alert is still firing.',
  },
  {
    name: 'Triage Status',
    width: '9%',
    component: (
      <Tooltip
        variant='interactive'
        title='Triage Status'
        desc="Your team's response to this issue. Update it as you investigate, escalate, or resolve. To handle matching issues automatically, go to"
        linkText='Triage Rules →'
        linkUrl='/troubleshoot#triage-rules'
        placement='top'
      >
        <Box component='span' sx={{ cursor: 'default', display: 'inline-flex', alignItems: 'center', gap: ds.space[1] }}>
          Triage Status
          <Box component='span' sx={{ position: 'relative', top: ds.space[0], opacity: '50%' }}>
            <SafeIcon src={infoIcon} alt='info' width={12} height={14} />
          </Box>
        </Box>
      </Tooltip>
    ),
  },
  { name: '', width: '11%' },
];

const getValidParam = (param: any, defaultValue = ''): string => {
  if (!param || param === 'undefined' || param === 'null' || param === '') {
    return defaultValue;
  }
  return String(param);
};

const getStatusText = (status: string | undefined): string => {
  if (status === 'FIRING') return 'Open';
  if (status === 'CLOSED') return 'Closed';
  if (status === 'RESOLVED') return 'Resolved';
  return status || '-';
};

const getStatusTone = (status: string | undefined): 'critical' | 'success' | 'neutral' => {
  if (status === 'FIRING') return 'critical';
  if (status === 'RESOLVED') return 'success';
  return 'neutral';
};

const parseSubjectName = (item: any): string | undefined => {
  if (item.subject_name) return item.subject_name;

  let evidence = item.evidences;
  if (typeof evidence !== 'string') return undefined;

  try {
    evidence = JSON.parse(evidence);
    if (evidence?.length > 0 && evidence[0].type === 'json') {
      const evidenceData = JSON.parse(evidence[0].data);
      let name = evidenceData?.resources?.[0]?.['ARN'];
      if (name?.startsWith('arn:aws')) {
        const parts = name.split(':');
        name = parts[parts.length - 1];
      }
      return name;
    }
  } catch (error) {
    console.error(error);
  }

  return undefined;
};

const isCrashMessage = (msg: string): boolean => msg.includes('CRASHED') || msg.includes('crash') || msg.includes('DOWN');

const extractCFCrashFromEvidences = (evidences: any[]): string => {
  // Check process_stats evidence for crash insights (e.g. "WARNING: 2 instances in CRASHED state")
  const processStats = evidences.find((e: any) => e?.additional_info?.action_type === 'process_stats');
  const insights = Array.isArray(processStats?.insight) ? processStats.insight : [];
  for (const ins of insights) {
    const msg = typeof ins === 'string' ? ins : ins?.message;
    if (msg && isCrashMessage(msg)) return msg;
  }

  // Fall back to raw event for exit_description/reason
  if (evidences[0]?.type === 'json') {
    const rawData = typeof evidences[0].data === 'string' ? JSON.parse(evidences[0].data) : evidences[0].data;
    return rawData?.exit_description || rawData?.reason || '';
  }
  return '';
};

const CloudAccountEvents = (props: {
  accountId: string | undefined;
  serviceName: string | undefined;
  subjectName: string | undefined;
  subjectType?: string | string[];
  stickyColumnIndex?: any;
  tableHeadingCenter?: any;
  heading?: string;
}) => {
  const router = useRouter();
  const [events, setEvents] = useState([]);
  const [eventsCount, setEventsCount] = useState(0);
  const [selectedSeverity, setSelectedSeverity] = useState(() => getValidParam(router?.query?.eventPriority));
  const [selectedEventName, setSelectedEventName] = useState(() => getValidParam(router?.query?.eventAggregationKey));
  const [selectedSource, setSelectedSource] = useState<{ label: string; value: string }[]>([]);
  const [selectedStatus, setSelectedStatus] = useState(() => getValidParam(router?.query?.eventStatus));
  const [selectedDateRange, setSelectedDateRange] = useState<any>(() => {
    const startParam = Number(router?.query?.start_time);
    const endParam = Number(router?.query?.end_time);
    return {
      startDate: startParam || getLast7Days().getTime(),
      endDate: endParam || new Date().getTime(),
    };
  });

  const [isHelpBeeOpen, setIsHelpBeeOpen] = useState(false);
  const [loading, setLoading] = useState(false);
  const { page, rowsPerPage, changePage, setPage } = usePagination(10);
  const [_isSnackBarOpen, _setIsSnackBarOpen] = useState(false);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState<any>({});
  const [isClassifyModalOpen, setIsClassifyModalOpen] = useState(false);
  const [selectedEvent, setSelectedEvent] = useState<any>(null);

  const cloudAccountEventsTable = 'cloudaccount-events';
  const _showEllipsis = true;

  const { severityFilterType, eventNamesFilter, sourceFilter, statusFilter, nbStatusFilter } = useEventCloudFilter(
    props.accountId as string,
    { subjectNamespace: props?.serviceName },
    { startTime: new Date(selectedDateRange.startDate).toISOString(), endTime: new Date(selectedDateRange.endDate).toISOString() }
  );

  const [selectedNbStatus, setSelectedNbStatus] = useState<Array<{ value: string }>>([]);

  // Intentionally depends only on sourceFilter (not router?.query?.source).
  // Purpose: initialize selectedSource once from the URL query when filter options first load.
  // Omitting router?.query?.source from deps prevents a re-run loop:
  // onSourceFilterChange → applyFiltersOnRouter updates the query → useEffect would fire again
  // even though state is already correct. After initialization, the handler owns the state.
  useEffect(() => {
    setSelectedSource((prev) => {
      const next = syncFilterFromQuery(sourceFilter, router?.query?.source, (f) => f.value);
      if (prev.length === 0 && next.length === 0) return prev;
      return next;
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sourceFilter]);

  const onNbStatusFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const value = e?.target?.value as unknown as Array<{ value: string }> | null;
    setSelectedNbStatus(value || []);
    setPage(0);
  };

  const onMenuClick = (menuItem: { id: number }, data: any) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    } else if (menuItem.id === 1) {
      setSelectedEvent({
        id: data.id,
        title: data.title,
        fingerprint: data.fingerprint,
        accountId: data.account_id || props.accountId,
      });
      setIsClassifyModalOpen(true);
    } else if (menuItem.id === 2) {
      const accountId = data.account_id || props.accountId;
      const params = new URLSearchParams({ accountId, returnUrl: router.asPath });
      if (data.aggregation_key) params.set('eventType', data.aggregation_key);
      if (data.priority) params.set('eventPriority', data.priority);
      if (data.source) params.set('eventSource', data.source);
      if (accountId) params.set('eventCluster', accountId);
      if (data.subject_namespace) params.set('eventNamespace', data.subject_namespace);
      router.push(`/workflow/new?${params.toString()}`);
    }
  };

  const onSeverityFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedSeverity(e?.target?.value || '');
    applyFiltersOnRouter(router, { eventPriority: e?.target?.value });
    setPage(0);
  };

  const onSourceFilterChange = (e: any) => {
    const value: { label: string; value: string }[] = e?.target?.value ?? [];
    setSelectedSource(value);
    applyFiltersOnRouter(router, { source: value.map((s) => s.value).join(',') });
    setPage(0);
  };

  const onEventNamesFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedEventName(e?.target?.value || '');
    applyFiltersOnRouter(router, { eventAggregationKey: e?.target?.value });
    setPage(0);
  };

  const onStatusFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedStatus(e?.target?.value || '');
    applyFiltersOnRouter(router, { eventStatus: e?.target?.value });
    setPage(0);
  };

  const getMenuItems = (item: any, disableTicket: boolean) => {
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
          id: 1,
        },
        {
          icon: WorkflowIcon,
          label: 'Create Automation',
          id: 2,
        },
      ];
    }
    return MENU_ITEMS;
  };

  const extractCrashDetail = (item: any): string => {
    if (item.source !== 'cloudfoundry' || !item.evidences) return '';
    try {
      const evidences = typeof item.evidences === 'string' ? JSON.parse(item.evidences) : item.evidences;
      if (!Array.isArray(evidences) || !evidences.length) return '';
      return extractCFCrashFromEvidences(evidences);
    } catch (e) {
      console.error('Error parsing evidences: ', e);
      return '';
    }
  };

  const mapEventToRow = (item: any, ticketReferenceMap: Map<string, any>): ICustomTable2Row[] => {
    const subjectName = parseSubjectName(item);
    const rowData: ICustomTable2Row[] = [];

    // Severity + Occurrence time merged
    rowData.push({
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 0 }}>
          <DsSeverityIcon level={toSeverityLevel(item.priority)} aria-label={`Severity: ${item.priority || 'unknown'}`} />
          <Datetime value={item.starts_at} sx={{ fontSize: ds.text.caption }} />
        </Box>
      ),
      data: item.priority,
    });

    rowData.push({
      component: (
        <Box sx={{ minWidth: ds.space.mul(0, 60) }}>
          <Text showAutoEllipsis value={subjectName} />
          {item.subject_namespace && <Text value={`service: ${item.subject_namespace}`} secondaryText />}
        </Box>
      ),
    });

    const crashDetail = extractCrashDetail(item);

    // Event + Message merged (Event as primary, Message as sub-text)
    rowData.push({
      component: (
        <Box sx={{ minWidth: ds.space.mul(0, 60) }}>
          <Text showAutoEllipsis value={item.aggregation_key} />
          {ClusterNameWithRegion({
            name: item.title,
            showAutoEllipsis: true,
            maxWidth: '100%',
            hideIcon: true,
            font: ds.text.caption,
            sx: { fontStyle: 'italic', color: ds.gray[600] },
          })}
          {crashDetail && <Text value={crashDetail} secondaryText sx={{ fontSize: ds.text.caption, color: ds.red[600], mt: 'var(--ds-space-1)' }} />}
          {ticketReferenceMap.has(item.fingerprint) && (
            <CustomTicketLink
              ticketURL={ticketReferenceMap.get(item.fingerprint)?.url || ''}
              ticketID={ticketReferenceMap.get(item.fingerprint)?.ticket_id || ''}
            />
          )}
        </Box>
      ),
      drilldownQuery: { event: item },
      data: item.aggregation_key,
    });

    rowData.push({
      component: (
        <ScoreDisplay
          score={item.computed_score}
          priority={item.computed_priority}
          scoreFactors={item.score_factors}
          confidence={item.score_confidence}
        />
      ),
      data: item.computed_priority,
    });

    // Alert Status + Source merged (Status as primary, Source as secondary)
    rowData.push({
      component: (
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
          <Label tone={getStatusTone(item?.status)} size='sm'>
            {getStatusText(item?.status)}
          </Label>
          <Text value={item.source?.replace('AWS_', '')} secondaryText sx={{ fontSize: ds.text.caption, mt: 'var(--ds-space-1)' }} />
        </Box>
      ),
    });

    rowData.push({
      component: (
        <Tooltip variant='default' title={getTriageStatusTooltip(item?.nb_status || 'OPEN', item?.snoozed_until)} placement='top'>
          <Box>
            <NBStatusBadge
              eventId={item.id}
              currentStatus={item?.nb_status || 'OPEN'}
              snoozedUntil={item?.snoozed_until}
              onStatusChange={() => listCloudAccountEvents()}
              onCreateTicket={() => {
                setTicketData(item);
                setIsTicketCreateFormOpen(true);
              }}
              disableTooltip
            />
          </Box>
        </Tooltip>
      ),
      data: item?.nb_status,
    });

    const menuItemsConfig = getMenuItems(item, ticketReferenceMap.has(item.fingerprint));
    rowData.push({
      component: (
        <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={ds.space[1]} justifyContent={'flex-start'}>
          <DsButton
            tone='secondary'
            size='xs'
            trailingAccent={<FiArrowRight />}
            href={`/investigate?id=${item.id}&accountId=${props?.accountId}`}
            data-testid='investigate-btn'
          >
            Investigate
          </DsButton>
          {menuItemsConfig && menuItemsConfig.length > 0 && (
            <DsDropdownMenu
              align='end'
              size='sm'
              items={menuItemsConfig.map((m) => ({
                id: `events-action-${item.id}-${m.id}`,
                label: m.label,
                icon: m.icon ? <SafeIcon src={m.icon} alt='' width={14} height={14} /> : undefined,
                disabled: m.disabled,
                onSelect: () => onMenuClick({ id: m.id }, item),
              }))}
              trigger={<DsButton tone='ghost' size='xs' composition='icon-only' aria-label='More actions' icon={<MoreVertIcon />} />}
            />
          )}
        </Box>
      ),
    });

    return rowData;
  };

  const listCloudAccountEvents = () => {
    setLoading(true);

    apiCloudAccount
      .listEvents(
        {
          accountId: props?.accountId as string,
          subjectNamespace: props?.serviceName,
          startDate: new Date(selectedDateRange.startDate),
          endDate: new Date(selectedDateRange.endDate),
          aggregationKey: selectedEventName,
          subjectName: props?.subjectName,
          subjectType: props?.subjectType,
          priority: selectedSeverity,
          source: selectedSource.map((s) => s.value),
          status: selectedStatus,
          nbStatus: selectedNbStatus.length > 0 ? selectedNbStatus.map((s) => s?.value) : undefined,
        },
        rowsPerPage,
        page * rowsPerPage
      )
      .then(async (res: any) => {
        const events = res.data?.events || [];
        const totalCount = res.data?.events_aggregate?.aggregate?.count ?? 0;

        if (events.length === 0) {
          setEvents([]);
          setEventsCount(0);
          setLoading(false);
          return;
        }

        // 1. Extract all unique fingerprints (Reference IDs)
        const uniqueReferenceIds = new Set();
        events.forEach((item: any) => {
          if (item.fingerprint) {
            uniqueReferenceIds.add(item.fingerprint);
          }
        });
        const references: any = Array.from(uniqueReferenceIds);

        try {
          // 2. Fetch Tickets for all events in one go
          const ticketRes: any = await ticketsApi.listTicketsSummary({ reference_id: references });

          // 3. Create a Map for quick lookup
          const ticketReferenceMap = new Map();
          ticketRes?.data?.tickets?.forEach((element: any) => {
            ticketReferenceMap.set(element.reference_id, element);
          });

          // 4. Map events to table rows
          const ec2ResourceData = events.map((item: any) => mapEventToRow(item, ticketReferenceMap));

          // 5. Update State
          setEvents(ec2ResourceData);
          setEventsCount(totalCount);
        } catch (err) {
          console.error('Error fetching ticket summaries', err);
          // Optional: handle partial failure (show events without tickets)
        } finally {
          setLoading(false);
        }
      })
      .catch(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    if (!props?.accountId) {
      return;
    }
    listCloudAccountEvents();
  }, [
    props?.accountId,
    page,
    rowsPerPage,
    selectedSeverity,
    selectedDateRange,
    selectedEventName,
    selectedSource,
    selectedStatus,
    selectedNbStatus,
    props?.subjectName,
    props.subjectType,
    props?.serviceName,
  ]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setPage(0);
    applyFiltersOnRouter(router, {
      start_time: passedSelectedDateTime.startTime,
      end_time: passedSelectedDateTime.endTime,
    });
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data: any) => {
    return `**Title**: ${data?.title ?? 'N/A'}
      **Priority**: ${data?.priority ?? 'N/A'}
      **Aggregation Key**: ${data?.aggregation_key ?? 'N/A'}
      **Subject Type**: ${data?.subject_type ?? 'N/A'}
      **Subject Name**: ${data?.subject_name ?? 'N/A'}
      **Subject Namespace**: ${data?.subject_namespace ?? 'N/A'}
      `;
  };

  const handleTicketSuccess = () => {
    listCloudAccountEvents();
  };

  const handleTicketFailure = (res: any) => {
    snackbar.error(`Failed! ${res}`);
  };

  return (
    <>
      <HelpBeeModal isModalVisible={isHelpBeeOpen} onClose={() => setIsHelpBeeOpen(false)} />
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
          type: 'event',
        }}
      />
      {selectedEvent && (
        <EventClassifyModal
          open={isClassifyModalOpen}
          handleClose={() => {
            setIsClassifyModalOpen(false);
            setSelectedEvent(null);
          }}
          event={selectedEvent}
          onSuccess={() => {
            setIsClassifyModalOpen(false);
            setSelectedEvent(null);
            listCloudAccountEvents();
          }}
        />
      )}
      <ListingLayout id='cloudaccount-events'>
        <ListingLayout.Toolbar
          data-testid={`${cloudAccountEventsTable}-filter-toolbar`}
          actions={
            <>
              <CustomDateTimeRangePicker
                passedSelectedDateTime={{
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  shortcutClickTime: 0,
                }}
                onChange={(result: any) => {
                  const val = result?.selection ?? result;
                  if (val) handleDateRangeChange(val);
                }}
              />
              <DownloadButton id={`${cloudAccountEventsTable}-download`} onClick={() => ({ tableId: cloudAccountEventsTable })} />
            </>
          }
        >
          <FilterDropdown
            id={`${cloudAccountEventsTable}-filter-event-name`}
            label='Event Name'
            options={eventNamesFilter}
            value={(eventNamesFilter || []).find((o: any) => o.value === selectedEventName) ?? null}
            onSelect={(_e: any, item: any) => onEventNamesFilterChange({ target: { value: item?.value || '' } } as any)}
          />
          <FilterDropdown
            id={`${cloudAccountEventsTable}-filter-severity`}
            label='Severity'
            options={(severityFilterType || []).map((s: string) => ({ label: s, value: s }))}
            value={selectedSeverity ? { label: selectedSeverity, value: selectedSeverity } : null}
            onSelect={(_e: any, item: any) => onSeverityFilterChange({ target: { value: item?.value || '' } } as any)}
          />
          <FilterDropdown
            id={`${cloudAccountEventsTable}-filter-source`}
            label='Source'
            multiple
            options={sourceFilter}
            value={selectedSource}
            onSelect={(_e: any, items: any) => onSourceFilterChange({ target: { value: Array.isArray(items) ? items : [] } })}
          />
          <FilterDropdown
            id={`${cloudAccountEventsTable}-filter-status`}
            label='Status'
            options={statusFilter}
            value={(statusFilter || []).find((o: any) => o.value === selectedStatus) ?? null}
            onSelect={(_e: any, item: any) => onStatusFilterChange({ target: { value: item?.value || '' } } as any)}
          />
          <FilterDropdown
            id={`${cloudAccountEventsTable}-filter-triage-status`}
            label='Triage Status'
            multiple
            options={nbStatusFilter}
            value={selectedNbStatus}
            onSelect={(_e: any, items: any) => onNbStatusFilterChange({ target: { value: Array.isArray(items) ? items : [] } } as any)}
          />
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
          <CustomTable2
            id={cloudAccountEventsTable}
            headers={TABLE_COLUMNS}
            tableData={events}
            rowsPerPage={rowsPerPage}
            onPageChange={changePage}
            totalRows={eventsCount}
            loading={loading}
            showExpandable={false}
            pageNumber={page + 1}
            tableHeadingCenter={props.tableHeadingCenter || ['Severity', 'Alert Status']}
            stickyColumnIndex={props.stickyColumnIndex}
          />
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};
export default CloudAccountEvents;
