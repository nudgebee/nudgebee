import { Box } from '@mui/material';
import { useEffect, useState } from 'react';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';
import apiCloudAccount from '@api1/cloud-account';
import BoxLayout2 from '@common/BoxLayout2';
import CloudAccountTable from './CloudAccountTable';
import HelpBeeModal from '@components1/helpbee';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { action } from 'src/utils/actionStyles';
import { getLast7Days } from '@lib/datetime';
import type { ICustomTable2Row } from './ec2/Instances';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Text from '@components1/common/format/Text';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import Datetime from '@components1/common/format/Datetime';
import { useEventCloudFilter } from '@hooks/useCloudFilters';
import { syncFilterFromQuery } from '@utils/common';
import InvestigateButton from '@components1/common/InvestigateButton';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import NBStatusBadge from '@components1/common/widgets/NBStatusBadge';
import { usePagination } from '@hooks/usePagination';
import { hasWriteAccess } from '@lib/auth';
import { TicketsIcon, dashboardIcon1 as ClassifyIcon, infoIcon } from '@assets';
import ticketsApi from '@api1/tickets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import EventClassifyModal from '@components1/events/EventClassifyModal';
import { snackbar } from '@components1/common/snackbarService';
import ScoreDisplay from '@components1/common/widgets/ScoreDisplay';
import WorkflowIcon from '@assets/WorkflowIcon';
import CustomTooltip from '@components1/common/CustomTooltip';
import CustomTicketLink from '@components1/common/CustomTicketLink';
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
  return status || '-';
};

const getStatusVariant = (status: string | undefined): string => {
  if (status === 'FIRING') return 'red';
  if (status === 'CLOSED') return 'grey';
  return '';
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

  const { severityFilterType, eventNamesFilter, sourceFilter, statusFilter, nbStatusFilter } = useEventCloudFilter(props.accountId as string, {
    subjectNamespace: props?.serviceName,
  });

  const [selectedNbStatus, setSelectedNbStatus] = useState<Array<{ value: string }>>([]);

  // Intentionally depends only on sourceFilter (not router?.query?.source).
  // Purpose: initialize selectedSource once from the URL query when filter options first load.
  // Omitting router?.query?.source from deps prevents a re-run loop:
  // onSourceFilterChange → applyFiltersOnRouter updates the query → useEffect would fire again
  // even though state is already correct. After initialization, the handler owns the state.
  useEffect(() => {
    setSelectedSource(syncFilterFromQuery(sourceFilter, router?.query?.source, (f) => f.value));
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
      router.push(`/workflow/new?accountId=${accountId}&returnUrl=${encodeURIComponent(router.asPath)}`);
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
        <Box sx={{ display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '0px' }}>
          <SeverityIcon severityType={item.priority} />
          <Datetime value={item.starts_at} sx={{ fontSize: '11px' }} />
        </Box>
      ),
      data: item.priority,
    });

    rowData.push({
      component: (
        <Box sx={{ minWidth: '120px' }}>
          <Text showAutoEllipsis value={subjectName} />
          {item.subject_namespace && <Text value={`service: ${item.subject_namespace}`} secondaryText />}
        </Box>
      ),
    });

    const crashDetail = extractCrashDetail(item);

    // Event + Message merged (Event as primary, Message as sub-text)
    rowData.push({
      component: (
        <Box sx={{ minWidth: '120px' }}>
          <Text showAutoEllipsis value={item.aggregation_key} />
          {ClusterNameWithRegion({
            name: item.title,
            showAutoEllipsis: true,
            maxWidth: '100%',
            hideIcon: true,
            font: '11px',
            sx: { fontStyle: 'italic', color: 'text.secondary' },
          })}
          {crashDetail && <Text value={crashDetail} secondaryText sx={{ fontSize: '10px', color: '#DC2626', mt: '2px' }} />}
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
          <CustomLabels margin='0' text={getStatusText(item?.status)} variant={getStatusVariant(item?.status)} />
          <Text value={item.source?.replace('AWS_', '')} secondaryText sx={{ fontSize: '11px', mt: '2px' }} />
        </Box>
      ),
    });

    rowData.push({
      component: (
        <CustomTooltip variant='default' title={getTriageStatusTooltip(item?.nb_status || 'OPEN', item?.snoozed_until)} placement='top'>
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
        </CustomTooltip>
      ),
      data: item?.nb_status,
    });

    rowData.push({
      component: (
        <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'2px'} justifyContent={'flex-start'}>
          <InvestigateButton displayText url={`/investigate?id=${item.id}&accountId=${props?.accountId}`} />
          <ThreeDotsMenu
            sx={{ ...action.primary }}
            menuItems={getMenuItems(item, ticketReferenceMap.has(item.fingerprint))}
            data={item}
            onMenuClick={onMenuClick}
          />
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
      <BoxLayout2
        heading={props.heading ?? 'Events'}
        id='cloudaccount-events'
        filterOptions={[
          {
            type: 'dropdown',
            enabled: true,
            options: eventNamesFilter,
            onSelect: onEventNamesFilterChange,
            minWidth: '150px',
            label: 'Event Name',
            value: selectedEventName,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: severityFilterType,
            onSelect: onSeverityFilterChange,
            minWidth: '150px',
            label: 'Severity',
            value: selectedSeverity,
          },
          {
            type: 'multi-dropdown',
            enabled: true,
            options: sourceFilter,
            onSelect: onSourceFilterChange,
            minWidth: '150px',
            label: 'Source',
            value: selectedSource,
          },
          {
            type: 'dropdown',
            enabled: true,
            options: statusFilter,
            onSelect: onStatusFilterChange,
            minWidth: '150px',
            label: 'Status',
            value: selectedStatus,
          },
          {
            type: 'multi-dropdown',
            enabled: true,
            options: nbStatusFilter,
            onSelect: onNbStatusFilterChange,
            minWidth: '150px',
            label: 'Triage Status',
            value: selectedNbStatus,
          },
        ]}
        sharingOptions={{
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: cloudAccountEventsTable,
              };
            },
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
      >
        <CloudAccountTable
          id={cloudAccountEventsTable}
          headers={TABLE_COLUMNS}
          data={events}
          rowsPerPage={rowsPerPage}
          onPageChange={changePage}
          totalRows={eventsCount}
          loading={loading}
          showExpandable={false}
          pageNumber={page + 1}
          tableHeadingCenter={props.tableHeadingCenter || ['Severity', 'Alert Status']}
          stickyColumnIndex={props.stickyColumnIndex}
        />
      </BoxLayout2>
    </>
  );
};
export default CloudAccountEvents;
