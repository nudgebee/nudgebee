import React, { useEffect, useState } from 'react';
import k8sApi, { priorityFilter, statusFilter } from '@api1/kubernetes';
import { Box } from '@mui/material';
import BoxLayout2 from '@components1/common/BoxLayout2';
import Text from '@components1/common/format/Text';
import InvestigateButton from '@components1/common/InvestigateButton';
import { useRouter } from 'next/router';
import ThreeDotsMenu from '@components1/common/ThreeDotsMenu';
import { TicketsIcon } from '@assets';
import TicketCreatePopupForm from '@components1/tickets/TicketCreatePopupForm';
import Datetime from '@components1/common/format/Datetime';
import { getLast7Days } from '@lib/datetime';
import { action } from 'src/utils/actionStyles';
import apiUser from '@api1/user';
import { applyFiltersOnRouter } from '@lib/router';
import { snackbar } from '@components1/common/snackbarService';
import { safeJSONParse } from 'src/utils/common';
import CustomTable from '@components1/common/tables/CustomTable2';

interface DefaulQueryProps {
  aggregation_key: string[];
  namespace: string;
  workloadName: string;
  eventPriority: string;
  eventStatus: string;
}

interface KubernetesApplicationLogFailureProps {
  accountId: string;
  defaultQuery: DefaulQueryProps;
  stickyColumnIndex: string;
}

const KubernetesApplicationLogFailure: React.FC<KubernetesApplicationLogFailureProps> = ({ accountId, defaultQuery, stickyColumnIndex = '' }) => {
  const router = useRouter();

  const [data, setData] = useState([]);
  const [loading, setLoading] = useState(false);
  const [currentPage, setCurrentPage] = useState(0);
  const [isTicketCreateFormOpen, setIsTicketCreateFormOpen] = useState(false);
  const [ticketData, setTicketData] = useState<any>({});
  const [totalCount, setTotalCount] = useState(0);
  const [namespaceFilter, setNamespaceFilter] = useState([]);
  const [workloadFilter, setWorkloadFilter] = useState<any[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string | null | undefined>(
    defaultQuery?.namespace ?? router?.query?.namespace ?? router?.query?.eventNamespace ?? null
  );
  const [allWorkload, setAllWorkload] = useState([]);
  const [selectedStatus, setSelectedStatus] = useState<string>(defaultQuery?.eventStatus ?? router.query.eventStatus ?? null);
  const [selectedPriority, setSelectedPriority] = useState<string>(defaultQuery?.eventPriority ?? router.query.eventPriority ?? null);
  const [selectedWorkload, setSelectedWorkload] = useState<string>(defaultQuery?.workloadName ?? router?.query?.eventSubjectName ?? '');
  const [selectedDateRange, _setSelectedDateRange] = useState<any>({
    startDate: getLast7Days().getTime(),
    endDate: new Date().getTime(),
  });
  const k8sProm = 'k8sAppLogFailure';
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    if (!accountId) {
      return;
    }

    k8sApi.getK8sNamespaceNames(accountId).then((res: any) => {
      const namespaces = res.data.namespaces;
      setNamespaceFilter(namespaces);
    });
  }, [accountId]);

  useEffect(() => {
    if (!accountId) {
      return;
    }

    const query = {
      accountId: accountId,
    };
    k8sApi
      .getAllK8sWorkload(query)
      .then((res) => {
        setWorkloadFilter([...new Set(res?.data.map((e: any) => e.name))]);
        setAllWorkload(res?.data);
      })
      .catch((error) => {
        return error;
      });
  }, [accountId]);

  useEffect(() => {
    handleSubmit();
  }, [
    currentPage,
    recordsPerPage,
    selectedNamespace,
    selectedWorkload,
    selectedPriority,
    selectedStatus,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    accountId,
  ]);

  const onMenuClick = (menuItem: any, data: any) => {
    if (menuItem.id === 0) {
      setTicketData(data);
      setIsTicketCreateFormOpen(true);
    }
  };

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    _setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  const extractNamespaceAndApplication = (value: string, type: string) => {
    if (!value) {
      return value;
    }
    const valueArray = value.split('/').filter((e) => e != '');
    if (valueArray.length === 4) {
      if (type === 'namespace') {
        return valueArray[1];
      } else if (type === 'application') {
        const secondLastHyphenIndex = valueArray[2].lastIndexOf('-', valueArray[2].lastIndexOf('-') - 1);
        const _result = secondLastHyphenIndex !== -1 ? valueArray[2].substring(0, secondLastHyphenIndex) : valueArray[2];
        return _result;
      }
    } else {
      return value;
    }
  };

  const handleSubmit = () => {
    setLoading(true);
    const query: any = {};
    if (selectedNamespace) {
      query.subject_namespace = selectedNamespace;
    }
    if (accountId) {
      query.account_id = accountId;
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
    if (selectedDateRange?.startDate) {
      query.startDate = new Date(selectedDateRange.startDate);
    }
    if (selectedDateRange?.endDate) {
      query.endDate = new Date(selectedDateRange.endDate);
    }
    k8sApi
      .getK8sEvents(recordsPerPage, currentPage * recordsPerPage, { ...query, ...defaultQuery })
      .then((res: any) => {
        const data = res.data?.events?.map((item: any) => {
          let dataObject: any = {};
          if (item?.labels) {
            let labelData = item.labels;
            if (typeof labelData === 'string') {
              labelData = safeJSONParse(labelData);
            }
            if (labelData && typeof labelData === 'object' && Object.keys(labelData).length > 0) {
              dataObject = labelData;
            }
          }
          const MENU_ITEMS: any[] = [
            {
              icon: TicketsIcon,
              label: 'Create Ticket',
              id: 0,
            },
          ];

          return [
            {
              component: <Text value={dataObject.sample || '-'} copyableTooltip={true} showAutoEllipsis />,
              drilldownQuery: item,
            },
            {
              component: <Text showAutoEllipsis value={extractNamespaceAndApplication(dataObject.container_id, 'namespace')} />,
            },
            {
              component: <Text showAutoEllipsis value={extractNamespaceAndApplication(dataObject.container_id, 'application')} />,
            },
            { component: <Datetime value={item.starts_at} baseDate={new Date()} />, data: item.starts_at },
            {
              component: (
                <Box display={'flex'} flexDirection={'row'} alignItems={'space-between'} gap={'6px'} justifyContent={'flex-end'}>
                  <InvestigateButton displayText url={`/investigate?id=${item.id}&accountId=${accountId}`} />
                  <ThreeDotsMenu sx={{ ...action.primary }} menuItems={MENU_ITEMS} data={item} onMenuClick={onMenuClick} />
                </Box>
              ),
            },
          ];
        });
        setLoading(false);
        setData(data);
        setTotalCount(res.data.events_aggregate?.aggregate?.count);
      })
      .catch((error) => {
        console.error(error);
      });
  };

  const closeTicketCreateForm = () => {
    setIsTicketCreateFormOpen(false);
  };

  const getTicketDescription = (data: any) => {
    let description = '';
    description += '**Title**: ' + data.title + '\n';
    description += '**Priority**: ' + data.priority + '\n';
    description += '**Aggregation Key**: ' + data.aggregation_key + '\n';
    description += '**Subject Type**: ' + data.subject_type + '\n';
    description += '**Subject Name**: ' + data.subject_name + '\n';
    description += '**Subject Namespace**: ' + data.subject_namespace + '\n';
    return description;
  };

  const onPageChange = (page: number, limit: number) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNamespaceFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    if (e?.target?.value) {
      setSelectedNamespace(e?.target?.value);
      const filterWorkloads = allWorkload.filter((f: any) => f.namespace == e.target.value).map((d: any) => d.name);
      setWorkloadFilter(filterWorkloads);
    } else {
      setWorkloadFilter(allWorkload.map((e: any) => e.name));
      setSelectedNamespace('');
    }
    setSelectedWorkload('');
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventNamespace: e?.target?.value, eventSubjectName: '' });
  };

  const onWorkloadFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
  };

  const onPriorityFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedPriority(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventPriority: e?.target?.value });
  };

  const onStatusFilterChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    setSelectedStatus(e?.target?.value);
    setCurrentPage(0);
    applyFiltersOnRouter(router, { eventStatus: e?.target?.value });
  };

  const handleTicketSuccess = () => {
    handleSubmit();
  };

  const handleTicketFailure = (res: string) => {
    snackbar.error(`Failed! ${res}.`);
  };

  return (
    <div>
      <TicketCreatePopupForm
        open={isTicketCreateFormOpen}
        handleClose={closeTicketCreateForm}
        onClose={closeTicketCreateForm}
        onSuccess={handleTicketSuccess}
        onFailure={handleTicketFailure}
        ticketData={{
          subject: 'Investigate Event - ' + ticketData?.title,
          description: getTicketDescription(ticketData),
          accountId: accountId,
        }}
        ticketUrl={{ url: `/investigate?id=${ticketData?.id}` }}
        reference={{
          id: ticketData?.id,
          type: 'kubernetes',
        }}
      />
      <BoxLayout2
        id='query-logs'
        heading=''
        dateTimeRange={{
          enabled: true,
          onChange: handleDateRangeChange,
          passedSelectedDateTime: {
            startTime: selectedDateRange.startDate,
            endTime: selectedDateRange.endDate,
            shortcutClickTime: 0,
          },
        }}
        sharingOptions={{
          sharing: {
            enabled: false,
            onClick: null,
          },
          download: {
            enabled: true,
            onClick: () => {
              return {
                tableId: k8sProm,
              };
            },
          },
        }}
        filterOptions={[
          {
            type: 'dropdown',
            options: namespaceFilter,
            onSelect: onNamespaceFilterChange,
            minWidth: '120px',
            label: 'Namespace',
            value: selectedNamespace,
          },
          {
            type: 'dropdown',
            value: selectedWorkload,
            options: workloadFilter,
            onSelect: onWorkloadFilterChange,
            minWidth: '90px',
            label: 'Workload',
          },
          {
            type: 'dropdown',
            options: priorityFilter,
            onSelect: onPriorityFilterChange,
            minWidth: '90px',
            label: 'Severity',
            value: selectedPriority,
          },
          {
            type: 'dropdown',
            options: statusFilter,
            onSelect: onStatusFilterChange,
            minWidth: '90px',
            label: 'Status',
            value: selectedStatus,
          },
        ]}
      >
        <CustomTable
          id={k8sProm}
          totalRows={totalCount}
          tableData={data}
          headers={[
            { name: 'Sample', width: '50%' },
            { name: 'Namespace', width: '15%' },
            { name: 'Application', width: '15%' },
            { name: 'Created At', width: '10%' },
            '',
          ]}
          rowsPerPage={recordsPerPage}
          showExpandable={false}
          loading={loading}
          onSortChange={undefined}
          onPageChange={onPageChange}
          pageNumber={currentPage + 1}
          stickyColumnIndex={stickyColumnIndex}
          onRowClick={(row: any) => {
            router.push(`/investigate?id=${row.id}&accountId=${accountId}`);
          }}
        />
      </BoxLayout2>
    </div>
  );
};

export default KubernetesApplicationLogFailure;
