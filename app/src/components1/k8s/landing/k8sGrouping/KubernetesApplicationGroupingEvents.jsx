import CustomTable from '@components1/common/tables/CustomTable2';
import { useState, useEffect } from 'react';
import apiAppGrouping from '@api1/application-groupings';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import Datetime from '@components1/common/format/Datetime';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import InvestigateButton from '@components1/common/InvestigateButton';
import { action } from 'src/utils/actionStyles';
import { Box } from '@mui/material';
import ticketsApi from '@api1/tickets';
import { TicketsIcon } from '@assets';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { hasWriteAccess } from '@lib/auth';
import { applyFiltersOnRouter } from '@lib/router';
import { useRouter } from 'next/router';
import apiUser from '@api1/user';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import { getLast7Days } from '@lib/datetime';
import WorkflowIcon from '@assets/WorkflowIcon';

const KubernetesApplicationGroupingEvents = ({ groupId }) => {
  const [applications, setApplications] = useState([]);
  const [tableData, setTableData] = useState([]);
  const router = useRouter();
  const [loading, setLoading] = useState(false);

  const [rowsPerPage, setRowsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const [currentPage, setCurrentPage] = useState(0);

  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState({});

  const [_namespaceFilter, _setNamespaceFilter] = useState([]);
  // const [workloadFilter, setWorkloadFilter] = useState([]);
  const [selectedNamespace, _setSelectedNamespace] = useState(router?.query?.namespace ?? router?.query?.eventNamespace ?? null);
  //const [allWorkload, setAllWorkload] = useState([]);
  const [selectedWorkload, setSelectedWorkload] = useState(router?.query?.eventSubjectName ?? '');
  const findingTypeFilter = ['issue', 'configuration_change'];
  const [selectedFindingType, setSelectedFindingType] = useState(router?.query?.eventFindingType ?? 'issue');
  // const [subjectTypeFilter, setSubjectTypeFilter] = useState([]);
  const [selectedSubjectType, setSelectedSubjectType] = useState(router.query.eventSubjectType ?? null);
  // const [aggregationKeyFilter, setAggregationKeyFilter] = useState([]);
  // eventAggregationKey, managed by data pull, because setting default value here is complicated
  const [selectedAggregationKey, setSelectedAggregationKey] = useState([]);
  const [eventsCount, setEventsCount] = useState(0);

  const priorityFilter = [
    { value: 'HIGH', label: 'High' },
    { value: 'MEDIUM', label: 'Medium' },
    { value: 'DEBUG', label: 'Debug' },
    { value: 'LOW', label: 'Low' },
    { value: 'Info', label: 'Info' },
  ];
  const [selectedPriority, setSelectedPriority] = useState(router.query.eventPriority ?? null);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const statusFilter = [
    { value: 'FIRING', label: 'Firing' },
    { value: 'CLOSED', label: 'Closed' },
  ];

  const _showEllipsis = true;
  const [selectedStatus, setSelectedStatus] = useState(router.query.eventStatus ?? null);

  const [_snackMessage, setSnackMessage] = useState('');
  const [_severity, setSeverity] = useState('');
  const [_snackbarOpen, setSnackbarOpen] = useState(false);

  // const onNamespaceFilterChange = (e, _p) => {
  //   setSelectedWorkload('');
  //   if (e) {
  //     setSelectedNamespace(e?.target?.value);
  //     const filterWorkloads = allWorkload.filter((f) => f.namespace == e.target.value).map((d) => d.name);
  //     setWorkloadFilter(filterWorkloads);
  //   } else {
  //     setWorkloadFilter([...new Set(allWorkload.map((e) => e.name))]);
  //     setSelectedNamespace('');
  //   }
  //   setCurrentPage(0);
  //   applyFiltersOnRouter(router, { eventNamespace: e?.target?.value, eventSubjectName: '' });
  // };

  const _onWorkloadFilterChange = (e) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
  };
  const onFindingTypeFilterChange = (e, _p) => {
    setSelectedFindingType(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventFindingType: e?.target?.value });
  };

  const _onTypeFilterChange = (e, _p) => {
    setSelectedSubjectType(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectType: e?.target?.value });
  };

  const _onAggregationKeyFilterChange = (_e, p) => {
    if (p && p.length > 0) {
      setSelectedAggregationKey(p);
    } else {
      setSelectedAggregationKey([]);
    }
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventAggregationKey: p?.map((v) => v.value) });
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

  const onMenuClick = (menuItem, data) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    } else if (menuItem.id === 1) {
      const accountId = data.account_id || router.query.accountId;
      router.push(`/workflow/new?accountId=${accountId}&returnUrl=${encodeURIComponent(router.asPath)}`);
    }
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };
  const handleTicketSuccess = () => {};

  const handleTicketFailure = (res) => {
    setSeverity('error');
    setSnackMessage(`Failed! ${res}.`);
    setSnackbarOpen(true);
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
          icon: WorkflowIcon,
          label: 'Create Automation',
          id: 1,
        },
      ];
    }
    return MENU_ITEMS;
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  useEffect(() => {
    setLoading(true);
    apiAppGrouping.getApplicationsByGroup(groupId).then((res) => {
      const appsMapping = res?.data?.application_group_mapping.filter((item) => ({
        workload_name: item.workload_name,
        namespace_name: item.namespace_name,
        workload_kind: item.workload_kind,
        account_id: item.account_id,
      }));
      setApplications(appsMapping);
      setLoading(false);
    });
  }, [groupId]);

  useEffect(() => {
    if (applications) {
      setLoading(true);
      setEventsCount(0);
      setTableData([]);
      let query = {};
      if (selectedNamespace) {
        query.subject_namespace = selectedNamespace;
      }
      if (selectedFindingType) {
        query.finding_type = selectedFindingType;
      }
      if (selectedSubjectType) {
        query.subject_type = selectedSubjectType;
      }
      if (selectedAggregationKey?.length > 0) {
        query.aggregation_key = selectedAggregationKey.map((f) => f.value);
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
      query.startDate = new Date(selectedDateRange.startDate);
      query.endDate = new Date(selectedDateRange.endDate);

      apiAppGrouping.getApplicationsEvents(query, applications, rowsPerPage, currentPage).then((res) => {
        const uniqueReferenceIds = new Set();
        res?.data?.events_v2?.rows?.forEach((item) => {
          uniqueReferenceIds.add(item.fingerprint);
        });
        setEventsCount(res?.data?.events_aggregate?.rows[0]?.count);
        const references = Array.from(uniqueReferenceIds);
        ticketsApi.listTicketsSummary({ reference_id: references }).then((ticketRes) => {
          const ticketReferenceMap = new Map();
          ticketRes?.data?.tickets.forEach((element) => {
            ticketReferenceMap.set(element.reference_id, element);
          });

          const rows = res?.data?.events_v2?.rows.map((item) => [
            {
              text: item.title,
            },
            {
              text: item.subject_name,
            },
            {
              text: item.aggregation_key,
            },
            {
              component: <CustomLabels text={item.priority} />,
            },
            {
              component: <Datetime value={item.starts_at} />,
              data: item.starts_at,
            },
            {
              component: <CustomLabels text={item.status} />,
            },
            {
              component: item.aggregation_key && (
                <Box display={'flex'} flexDirection={'row'} alignItems={'center'} gap={'6px'} justifyContent={'flex-end'}>
                  <InvestigateButton url={`/investigate?id=${item.id}&accountId=${item.account_id}`} />
                  <ThreeDotsMenu
                    sx={{ ...action.primary }}
                    menuItems={getMenuItems(item, ticketReferenceMap.has(item.fingerprint))}
                    data={item}
                    onMenuClick={onMenuClick}
                  />
                </Box>
              ),
            },
          ]);
          setTableData(rows);
          setLoading(false);
        });
      });
    }
  }, [
    applications,
    selectedStatus,
    selectedPriority,
    selectedWorkload,
    selectedAggregationKey,
    selectedNamespace,
    selectedSubjectType,
    selectedDateRange,
    selectedFindingType,
    currentPage,
    rowsPerPage,
  ]);

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRowsPerPage(limit);
  };

  return (
    <>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + ticketData?.title,
          description: getTicketDescription(ticketData),
          accountId: '',
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.id}&accountId=${''}` }}
        reference={{
          id: ticketData?.fingerprint,
          type: 'kubernetes',
        }}
      />
      <BoxLayout2
        filterOptions={[
          {
            type: 'dropdown',
            options: findingTypeFilter,
            onSelect: onFindingTypeFilterChange,
            label: 'Finding Type',
            value: selectedFindingType,
          },

          {
            type: 'dropdown',
            options: priorityFilter,
            onSelect: onPriorityFilterChange,
            label: 'Severity',
            value: selectedPriority,
          },
          {
            type: 'dropdown',
            options: statusFilter,
            onSelect: onStatusFilterChange,
            label: 'Status',
            value: selectedStatus,
          },
        ]}
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
          },
        }}
      >
        <CustomTable
          loading={loading}
          tableData={tableData}
          headers={['Message', 'Name', 'Type', 'Severity', 'Occured time', 'Status', '']}
          rowsPerPage={rowsPerPage}
          totalRows={eventsCount}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
        />
      </BoxLayout2>
    </>
  );
};

export default KubernetesApplicationGroupingEvents;
