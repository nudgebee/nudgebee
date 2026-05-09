import React, { useState, useEffect } from 'react';
import k8sApi from '@api1/kubernetes';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Datetime from '@components1/common/format/Datetime';
import { getSpecificTime } from '@lib/datetime';
import { titleCaseForAggregationKey } from 'src/utils/common';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import { applyFiltersOnRouter } from '@lib/router';
import { useRouter } from 'next/router';
interface KubernetesGroupedEventTypeTable {
  accountId: string; //this prop is added to eliminate error, more props will be added here
}

const KubernetesGroupedEventTypeTable: React.FC<KubernetesGroupedEventTypeTable> = ({ accountId }) => {
  const [currentPage, setCurrentPage] = useState<number>(1);
  const [totalRows, setTotalRows] = useState<number>(0);
  const [tableData, setTableData] = useState<any>({});
  const [loading, setLoading] = useState<boolean>(false);
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startDate: getSpecificTime(1440),
    endDate: new Date().getTime(),
  });

  const router = useRouter();
  const [selectedStatus, setSelectedStatus] = useState<string>('');

  const [selectedPriority, setSelectedPriority] = useState(router.query.eventPriority ?? null);

  const [selectedAggregationKey, setSelectedAggregationKey] = useState(router.query.aggregation_key ?? null);

  const [aggregationKeyFilter, setAggregationKeyFilter] = useState<Array<{ label: string; value: string }>>([]);

  const statusFilter = [
    { value: 'FIRING', label: 'Firing' },
    { value: 'CLOSED', label: 'Closed' },
  ];
  const priorityFilter = [
    { value: 'HIGH', label: 'High' },
    { value: 'MEDIUM', label: 'Medium' },
    { value: 'DEBUG', label: 'Debug' },
    { value: 'LOW', label: 'Low' },
    { value: 'INFO', label: 'Info' },
  ];
  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const headers = [
    'Event Type',
    'Last Occurred',
    'Event Count',
    { name: 'Severity', width: '10%' },
    { name: 'Alert Status', width: '10%' },
    'Subjects',
    '',
  ];

  const onStatusFilterChange = (e: React.ChangeEvent<HTMLInputElement>, _p: any) => {
    setSelectedStatus(e?.target?.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventStatus: e?.target?.value });
  };

  const onPriorityFilterChange = (e: React.ChangeEvent<HTMLInputElement>, _p: any) => {
    setSelectedPriority(e?.target?.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventPriority: e?.target?.value });
  };

  useEffect(() => {
    const query: any = {};
    if (!accountId) {
      return;
    }
    query.account_id = accountId;
    query.start_date = new Date(selectedDateRange.startDate);
    query.end_date = new Date(selectedDateRange.endDate);
    if (selectedPriority) {
      query.priority = selectedPriority;
    }
    if (selectedStatus) {
      query.status = selectedStatus;
    }

    if (selectedAggregationKey) {
      query.aggregation_key = selectedAggregationKey;
    }
    const cols: Array<string> = ['max_created_at', 'event_count', 'aggregation_key', 'count_subject_name', 'distinct_priority', 'distinct_status'];
    //not showing account specific data as of now
    setLoading(true);
    k8sApi
      .getK8sEventGroupings(perPage, (currentPage - 1) * perPage, query, ['tenant_id', 'account_id', 'aggregation_key'], cols, {
        name: 'event_count',
        order: 'desc',
      })
      .then((res: any) => {
        const data: Array<any> = [];
        try {
          res.data.event_groupings.forEach((item: any) => {
            let severity = 'INFO';
            if (item.distinct_priority?.indexOf('HIGH') >= 0) {
              severity = 'HIGH';
            } else if (item.distinct_priority?.indexOf('MEDIUM') >= 0) {
              severity = 'MEDIUM';
            } else if (item.distinct_priority?.indexOf('LOW') >= 0) {
              severity = 'LOW';
            } else if (item.distinct_priority?.indexOf('DEBUG') >= 0) {
              severity = 'DEBUG';
            }

            const status = item.distinct_status?.indexOf('FIRING') > 0 ? 'FIRING' : 'CLOSED';

            data.push([
              {
                component: <Text value={titleCaseForAggregationKey(item.aggregation_key)} showAutoEllipsis sx={{ minWidth: '300px' }} />,
                drilldownQuery: {
                  aggregation_key: item.aggregation_key,
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  namespace: '',
                },
              },
              {
                component: <Datetime baseDate={new Date()} value={item.max_created_at} />,
              },
              {
                component: <Text value={item.event_count} />,
              },
              {
                component: <SeverityIcon severityType={severity} />,
              },
              {
                component: <CustomLabels margin='auto' text={status} />,
              },
              {
                component: <Text sx={{ textAlign: 'center' }} value={item.count_subject_name ?? '-'} />,
              },
              {},
            ]);
          });
          setTableData(data);
          setTotalRows(res.data.event_groupings_aggregate.aggregate.count);
          setLoading(false);
        } catch {
          setLoading(false);
        }
      });
  }, [
    currentPage,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    accountId,
    perPage,
    selectedPriority,
    selectedStatus,
    selectedAggregationKey,
  ]);

  const onAggregationKeyFilterChange = (e: React.ChangeEvent<HTMLInputElement>, _p: any) => {
    setSelectedAggregationKey(e?.target?.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { aggregation_key: e?.target?.value });
  };

  const getAggregationKeyFilterOptions = () => {
    if (!accountId) {
      return;
    }

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes: ['aggregation_key'],
      })
      .then((res: any) => {
        const aggregationFilter = res?.data?.filters?.find((f: any) => f.filter_type === 'aggregation_key');
        setAggregationKeyFilter(
          (aggregationFilter?.values || [])?.map((d: any) => {
            const data = {
              label: titleCaseForAggregationKey(d.value),
              value: d.value,
            };
            return data;
          })
        );
      });
  };

  useEffect(() => {
    getAggregationKeyFilterOptions();
  }, [accountId]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(1);
  };
  return (
    <BoxLayout2
      id={'grouped-events'}
      heading={''}
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
        {
          type: 'dropdown',
          enabled: true,
          options: priorityFilter,
          onSelect: onPriorityFilterChange,
          label: 'Severity',
          value: selectedPriority,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: statusFilter,
          onSelect: onStatusFilterChange,
          label: 'Status',
          value: selectedStatus,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: aggregationKeyFilter,
          onSelect: onAggregationKeyFilterChange,
          label: 'Event Type',
          value: router.query.aggregation_key ?? null,
        },
      ]}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: 'kubernetesEventType',
            };
          },
        },
        sharing: {
          enabled: false,
          onClick: null,
        },
      }}
    >
      <KubernetesTable2
        id={'kubernetesEventType'}
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
        onSortChange={{}}
        showExpandable
        expandable={{
          tabs: [{ text: 'Events', key: 'events-drilldown-events' }],
        }}
        tableHeadingCenter={['Severity', 'Alert Status', 'Subjects']}
      />
    </BoxLayout2>
  );
};

export default KubernetesGroupedEventTypeTable;
