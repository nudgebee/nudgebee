import React, { useState, useEffect } from 'react';
import k8sApi from '@api1/kubernetes';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import Datetime from '@components1/common/format/Datetime';
import SeverityIcon from '@components1/common/widgets/SeverityIcon';
import CustomLabels from '@components1/common/widgets/CustomLabels';
import apiUser from '@api1/user';
import { Text } from '@components1/common';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import { applyFiltersOnRouter } from '@lib/router';
import { useRouter } from 'next/router';
import { titleCaseForAggregationKey } from 'src/utils/common';

interface KubernetesGroupedApplicationsProps {
  accountId: string;
}

const KubernetesGroupedApplications: React.FC<KubernetesGroupedApplicationsProps> = ({ accountId }) => {
  const componentId = 'Grouped Events';
  const router = useRouter();
  const headers = [
    { name: 'Application', width: '30%' },
    { name: 'Event Type', width: '30%' },
    { name: 'Last Occurred', width: '10%' },
    { name: 'Event Count', width: '10%' },
    { name: 'Severity', width: '10%' },
    { name: 'Alert Status', width: '10%' },
    '',
  ];

  const [currentPage, setCurrentPage] = useState<number>(1);
  const [totalRows, setTotalRows] = useState<number>(0);
  const [tableData, setTableData] = useState<any>({});
  const [loading, setLoading] = useState<boolean>(false);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: new Date().getTime() - 60 * 60 * 24 * 1000,
    endDate: new Date().getTime(),
    key: 'selection',
  });
  const [perPage, setPerPage] = useState(apiUser.getUserPreferencesTablePageSize() ?? 10);
  const [namespaceFilter, setNamespaceFilter] = useState<any[]>([]);
  const [workloadFilter, setWorkloadFilter] = useState<any[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState(router?.query?.namespace ?? router?.query?.eventNamespace ?? null);
  const [allWorkload, setAllWorkload] = useState([]);
  const [selectedWorkload, setSelectedWorkload] = useState(router?.query?.eventSubjectName ?? '');
  const [aggregationKeyFilter, setAggregationKeyFilter] = useState([]);
  const [selectedAggregationKey, setSelectedAggregationKey] = useState<any[]>([]);

  const [isAggregationKeyReady, setIsAggregationKeyReady] = useState(false);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      key: 'selection',
    });
    setCurrentPage(1);
  };
  const onNamespaceFilterChange = (e: any, _p: any) => {
    setSelectedWorkload('');
    if (e?.target?.value) {
      setSelectedNamespace(e?.target?.value);
      const filterWorkloads = allWorkload?.filter((f: any) => f.namespace == e.target.value).map((d: any) => d.name);
      setWorkloadFilter(filterWorkloads);
    } else {
      setWorkloadFilter([...new Set(allWorkload?.map((e: any) => e.name))]);
      setSelectedNamespace('');
    }
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventNamespace: e?.target?.value, eventSubjectName: '' });
  };

  const onWorkloadFilterChange = (e: any) => {
    setSelectedWorkload(e?.target.value);
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventSubjectName: e?.target?.value });
  };

  const onAggregationKeyFilterChange = (e: any, p: any) => {
    if (p && p.length > 0) {
      setSelectedAggregationKey(p);
    } else {
      setSelectedAggregationKey([]);
    }
    setCurrentPage(1);
    applyFiltersOnRouter(router, { eventAggregationKey: p?.map((v: any) => v.value) });
  };

  useEffect(() => {
    if (!accountId) {
      return;
    }

    k8sApi.getK8sNamespaceNames(accountId).then((res) => {
      const namespaces = res.data.namespaces;
      setNamespaceFilter(namespaces);
    });

    k8sApi
      .getAllK8sWorkload({
        accountId: accountId,
      })
      .then((res) => {
        setWorkloadFilter([...new Set(res?.data.map((e: any) => e.name))]);
        setAllWorkload(res?.data);
      })
      .catch((error) => {
        return error;
      });
  }, [accountId]);

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setIsAggregationKeyReady(false);

    k8sApi
      .getEventFilterValues({
        accountId,
        filterTypes: ['aggregation_key'],
      })
      .then((res: any) => {
        let selectedKeys: any[] = [];
        const selectedValues: any[] = [];
        if (router.query.eventAggregationKey) {
          if (Array.isArray(router.query.eventAggregationKey)) {
            selectedKeys = router.query.eventAggregationKey;
          } else if (typeof router.query.eventAggregationKey === 'string') {
            selectedKeys = router.query.eventAggregationKey.split(',');
          }
        }

        const aggregationFilter = res?.data?.filters?.find((f: any) => f.filter_type === 'aggregation_key');
        setAggregationKeyFilter(
          (aggregationFilter?.values || []).map((d: any) => {
            const data = {
              label: titleCaseForAggregationKey(d.value),
              value: d.value,
            };
            if (selectedKeys.includes(d.value)) {
              selectedValues.push(data);
            }
            return data;
          })
        );
        setSelectedAggregationKey(selectedValues);
        setIsAggregationKeyReady(true);
      })
      .catch((error) => {
        console.error(error);
      });
  }, [accountId]);

  useEffect(() => {
    const query: any = {};
    if (!accountId || !isAggregationKeyReady) {
      return;
    }
    query.account_id = accountId;

    query.start_date = new Date(selectedDateRange.startDate);
    query.end_date = new Date(selectedDateRange.endDate);
    query.subject_name = selectedWorkload;
    query.subject_namespace = selectedNamespace;
    query.aggregation_key = selectedAggregationKey?.map((e: any) => e.value);

    const cols: Array<string> = [
      'max_created_at',
      'event_count',
      'subject_name',
      'subject_namespace',
      'aggregation_key',
      'distinct_priority',
      'distinct_status',
    ];
    //not showing account specific data as of now
    setLoading(true);
    k8sApi
      .getK8sEventGroupings(
        perPage,
        (currentPage - 1) * perPage,
        query,
        ['tenant_id', 'account_id', 'subject_name', 'subject_namespace', 'aggregation_key'],
        cols,
        {
          name: 'event_count',
          order: 'desc',
        }
      )
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
                component: (
                  <ClusterNameWithRegion
                    name={item.subject_name}
                    maxWidth='60'
                    hideIcon={true}
                    namespace={item.subject_namespace ? 'Namespace: ' + item.subject_namespace : ''}
                    namespaceFont='12px'
                  />
                ),
                drilldownQuery: {
                  subject_name: [item.subject_name],
                  subject_namespace: item.subject_namespace,
                  finding_type: '',
                  startTime: selectedDateRange.startDate,
                  endTime: selectedDateRange.endDate,
                  aggregation_key: item.aggregation_key,
                },
              },
              {
                component: <Text showAutoEllipsis value={titleCaseForAggregationKey(item.aggregation_key)} />,
              },
              {
                component: <Datetime baseDate={new Date()} value={item.max_created_at} />,
              },
              {
                component: <Text showAutoEllipsis value={item.event_count} />,
              },
              {
                component: <SeverityIcon severityType={severity} />,
              },
              {
                component: <CustomLabels margin='auto' text={status} />,
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
    perPage,
    selectedDateRange.startDate,
    selectedDateRange.endDate,
    accountId,
    selectedNamespace,
    selectedWorkload,
    selectedAggregationKey,
    isAggregationKeyReady,
  ]);

  return (
    <BoxLayout2
      id={'Grouped Applications'}
      heading={''}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: componentId,
            };
          },
        },
        sharing: {
          enabled: false,
          onClick: null,
        },
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
        {
          type: 'multi-dropdown',
          options: aggregationKeyFilter,
          onSelect: onAggregationKeyFilterChange,
          label: 'Event Type',
          value: selectedAggregationKey,
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
        onSortChange={{}}
        showExpandable
        expandable={{
          tabs: [{ text: 'Events', key: 'events-drilldown-applications' }],
        }}
        tableHeadingCenter={['Severity', 'Alert Status']}
      />
    </BoxLayout2>
  );
};

export default KubernetesGroupedApplications;
