import React, { useState, useEffect } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import { getLast30Days } from '@lib/datetime';
import ClusterNameWithRegion from '@components1/k8s/common/ClusterNameWithRegion';
import Datetime from '@components1/common/format/Datetime';
import { formatNumber } from '@lib/formatter';
import CopyableText from '@components1/common/CopyableText';
import Memory from '@components1/common/format/Memory';
import Currency from '@components1/common/format/Currency';
import NumberComponent from '@components1/common/format/Number';
import apiUser from '@api1/user';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import { useRouter } from 'next/router';
import { applyFiltersOnRouter } from '@lib/router';

const NAMESPACE_HEADERS = [
  'Namespace',
  'Workloads/Pods',
  { name: 'CPU', width: '15%' },
  { name: 'Memory', width: '15%' },
  { name: 'CPU', width: '15%' },
  { name: 'Memory', width: '15%' },
  'Cost',
];
const NAMESPACE_UPPER_HEADERS = [
  { text: '' },
  { text: '' },
  { text: 'Avg requested per resource', colSpan: 2, backgroundColor: '#F5F8FF' },
  { text: 'Avg used per resource', colSpan: 2, backgroundColor: '#FFF9F9' },
  { text: '' },
];

const KubernetesNamespaceTable = ({ accountId }) => {
  const router = useRouter();
  const kubernetesNamespaceTable = 'kubernetesNamespaceTable';

  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [currentPage, setCurrentPage] = useState(0);
  const [selectedDateRange, setSelectedDateRange] = useState({
    startDate: getLast30Days().getTime() + 60 * 1000,
    endDate: new Date().getTime(),
  });
  const [loading, setLoading] = useState(false);
  const [namespaces, setNamespaces] = useState([]);
  const [selectedName, setSelectedName] = useState(router.query.namespace || undefined);
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  useEffect(() => {
    listNamespaces();
  }, [accountId, currentPage, recordsPerPage]);

  useEffect(() => {
    if (!accountId || namespaces == undefined || namespaces.length == 0) {
      return;
    }

    k8sApi
      .getK8sMetrices({
        accountId: accountId,
        namespaceName: namespaces,
        startDate: new Date(selectedDateRange.startDate),
        endDate: new Date(selectedDateRange.endDate),
      })
      .then((res) => {
        for (let i = 0; i < data.length; i++) {
          let item = res.data?.k8s_pod_groupings?.find((item) => item.namespace_name === data[i][0].drilldownQuery.namespaceName);
          if (item) {
            data[i][2] = {
              component: (
                <NumberComponent
                  value={item.avg_cpu_request}
                  sx={{ fontSize: '14px', color: '#374151', fontWeight: '500' }}
                  suffixSx={{ fontSize: '12px', color: '#9F9F9F', pl: '2px' }}
                />
              ),
            };
            data[i][3] = {
              component: (
                <Memory
                  value={item.avg_memory_request || null}
                  sx={{ fontSize: '14px', color: '#374151', fontWeight: '500' }}
                  suffixSx={{ fontSize: '12px', color: '#9F9F9F', pl: '2px' }}
                />
              ),
            };
            data[i][4] = {
              component: (
                <NumberComponent
                  value={item.avg_cpu_used}
                  sx={{ fontSize: '14px', color: '#374151', fontWeight: '500' }}
                  suffixSx={{ fontSize: '12px', color: '#9F9F9F', pl: '2px' }}
                />
              ),
            };
            data[i][5] = {
              component: (
                <Memory
                  value={item.avg_memory_used || null}
                  sx={{ fontSize: '14px', color: '#374151', fontWeight: '500' }}
                  suffixSx={{ fontSize: '12px', color: '#9F9F9F', pl: '2px' }}
                />
              ),
            };
            data[i][6] = { component: <Currency value={item.cost} /> };
          }
        }
        setData([...data]);
      });
  }, [accountId, namespaces, selectedDateRange.startDate, selectedDateRange.endDate]);

  const listNamespaces = () => {
    if (!accountId) {
      return;
    }
    setLoading(true);
    setData([]);
    setTotalCount(0);
    let query = {
      accountId: accountId,
      name: selectedName,
    };
    k8sApi
      .getK8sNamespaces(recordsPerPage, currentPage * recordsPerPage, query)
      .then((res) => {
        setLoading(false);
        let namespaces = [];
        let data = res.data?.k8s_namespaces?.map((item) => {
          namespaces.push(item.name);
          return [
            {
              component: (
                <CopyableText copyableText={item.name}>
                  <ClusterNameWithRegion name={item.name} namespace={<Datetime value={item.creation_time} />} hideIcon={true} />
                </CopyableText>
              ),
              drilldownQuery: { accountId: accountId, namespaceName: item.name, subject_namespace: item.name, type: 'namespace' },
            },
            {
              component: <Text value={formatNumber(item.workload_count || 0) + '/' + formatNumber(item.pod_count || 0)} />,
            },
            { text: '-' },
            { text: '-' },
            { text: '-' },
            { text: '-' },
            { text: '-' },
          ];
        });
        let totalCount = res.data?.k8s_namespaces_aggregate?.aggregate?.count;
        setNamespaces(namespaces);
        setData(data);
        setTotalCount(totalCount);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  const onPageChange = (page, limit) => {
    setCurrentPage(page - 1);
    setRecordsPerPage(limit);
  };

  const onNameFilterChange = (e, _p) => {
    setSelectedName(e?.target?.value);
    applyFiltersOnRouter(router, { namespace: e?.target?.value });
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
    setCurrentPage(0);
  };

  const onEnterPress = () => {
    if (currentPage === 0) {
      listNamespaces();
    } else {
      setCurrentPage(0);
    }
  };

  useEffect(() => {
    if (selectedName === '') {
      listNamespaces();
    }
  }, [selectedName]);

  const handleClearFilters = () => {
    setSelectedName('');
    applyFiltersOnRouter(router, { namespace: '' });
  };

  return (
    <BoxLayout2
      id='all-namespaces'
      heading=''
      filterOptions={[
        {
          type: 'search',
          enabled: true,
          onSelect: onNameFilterChange,
          minWidth: '150px',
          label: 'Namespace Name',
          value: selectedName,
          onEnter: onEnterPress,
          onClear: handleClearFilters,
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
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: kubernetesNamespaceTable,
            };
          },
        },
        sharing: { enabled: true },
      }}
    >
      <KubernetesTable2
        id={kubernetesNamespaceTable}
        headers={NAMESPACE_HEADERS}
        data={data}
        expandable={{
          tabs: [
            { text: 'Pods', value: 0, key: 'pods' },
            { text: 'Utilization Trends', value: 1, key: 'utilization3' },
            { text: 'Cost Trends', value: 2, key: 'cost' },
            { text: 'Recent Events', value: 3, key: 'events' },
            { text: 'Network', value: 4, key: 'network' },
            { text: 'Service Map', value: 5, key: 'serviceMap' },
          ],
        }}
        rowsPerPage={recordsPerPage}
        onPageChange={onPageChange}
        upperHeaders={NAMESPACE_UPPER_HEADERS}
        totalRows={totalCount}
        showExpandable
        loading={loading}
        selectedDateRange={selectedDateRange}
        pageNumber={currentPage + 1}
      />
    </BoxLayout2>
  );
};

KubernetesNamespaceTable.propTypes = {
  accountId: PropTypes.string,
};

export default KubernetesNamespaceTable;
