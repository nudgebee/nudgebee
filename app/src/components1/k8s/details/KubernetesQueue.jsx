import React, { useState, useEffect } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import KubernetesTracesListing from './KubernetesTracesListing';
import KubernetesServiceMap from '@components1/k8s/details/KubernetesServiceMap';
import AppDashboard from '@components1/dashboards/AppDashboard';
import Datetime from '@components1/common/format/Datetime';

const QUEUE_HEADERS = ['Type', 'Name', 'Namespace', 'Status', 'Created At', 'Updated At'];

const KubernetesQueueTable = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedStatus, setSelectedStatus] = useState('Active');

  const kubernetesQueueTable = 'kubernetesQueueTable';

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);

    k8sApi
      .listFrameworkResources(accountId, ['rabbitmq', 'kafka', 'nats'], selectedStatus)
      .then((res) => {
        let data = res ?? [];
        let tableData = data?.map((item) => {
          return [
            {
              component: <Text value={item.value} />,
            },
            {
              component: <Text value={item.cloud_resourse.name} />,
              drilldownQuery: {
                data: item,
              },
            },
            {
              component: <Text value={item.cloud_resourse.namespace ?? 'External'} />,
            },
            {
              component: <Text value={item.cloud_resourse.status} />,
            },
            {
              component: <Datetime value={item.cloud_resourse.created_at} />,
            },
            {
              component: <Datetime value={item.cloud_resourse.updated_at} />,
            },
          ];
        });
        setData(tableData ?? []);
        setTotalCount(tableData?.length);
        setLoading(false);
      })
      .catch(() => {
        setLoading(false);
      });
  }, [accountId, selectedStatus]);

  const onStatusFilterChange = (selectedStatus) => {
    setSelectedStatus(selectedStatus?.target?.value);
  };

  return (
    <BoxLayout2
      id='all-namespaces'
      heading=''
      filterOptions={[
        {
          type: 'dropdown',
          enabled: true,
          options: ['Active', 'Inactive'],
          onSelect: onStatusFilterChange,
          minWidth: '150px',
          label: 'Status',
          value: selectedStatus,
        },
      ]}
      dateTimeRange={{
        enabled: false,
      }}
      sharingOptions={{
        download: {
          enabled: true,
          onClick: () => {
            return {
              tableId: kubernetesQueueTable,
            };
          },
        },
        sharing: { enabled: true },
      }}
    >
      <KubernetesTable2
        id={kubernetesQueueTable}
        headers={QUEUE_HEADERS}
        data={data}
        expandable={{
          tabs: [
            {
              text: 'Details',
              value: 0,
              key: 'WorkloadDetails',
              componentFn: function (opt, drilldownQuery, _row) {
                let name = drilldownQuery?.data?.cloud_resourse?.name;
                if (name) {
                  name = name.split(':')[0];
                }
                // sometimes we get kind as Pod
                if (drilldownQuery?.data?.cloud_resourse?.type == 'Pod') {
                  let nameSplits = name.split('-');
                  name = nameSplits.slice(0, nameSplits.length - 2).join('-');
                }
                return (
                  <Box>
                    <AppDashboard
                      accountId={accountId}
                      workloadName={name}
                      namespaceName={drilldownQuery?.data?.cloud_resourse?.namespace ?? 'external'}
                      appType={drilldownQuery?.data?.value}
                    />
                  </Box>
                );
              },
            },
            {
              text: 'Service Map',
              value: 1,
              key: 'serviceMap',
              componentFn: function (opt, drilldownQuery, _row) {
                let name = drilldownQuery?.data?.cloud_resourse?.name;
                if (name) {
                  name = name.split(':')[0];
                }
                let endTime = new Date();
                let endDate = drilldownQuery?.data?.cloud_resourse?.updated_at;
                if (endDate) {
                  endDate = new Date(endDate);
                  // for tings less than 24hrs.. keep 24hrs as limit
                  if (new Date().getTime() - endDate.getTime() < 24 * 3600 * 1000) {
                    endDate = undefined;
                  }
                }
                if (endDate) {
                  endTime = endDate;
                }

                let startTime = new Date();
                startTime = new Date(endTime.getTime() - 1 * 3600 * 1000);
                return (
                  <KubernetesServiceMap
                    accountId={accountId}
                    appName={name}
                    namespaceName={drilldownQuery?.data?.cloud_resourse?.namespace ?? 'external'}
                    disableNamespaceFilter={true}
                    dateRange={{
                      startDateInMilli: startTime.getTime(),
                      endDateInMilli: endTime.getTime(),
                    }}
                    showSourceType={true}
                  />
                );
              },
            },
            {
              text: 'Traces',
              value: 12,
              key: 'workload-traces',
              componentFn: function (opt, drilldownQuery, _row) {
                let endTime = new Date();
                let name = drilldownQuery?.data?.cloud_resourse?.name;
                if (name) {
                  name = name.split(':')[0];
                }
                let endDate = drilldownQuery?.data?.cloud_resourse?.updated_at;
                endDate = new Date(endDate);
                if (isNaN(endDate.getTime())) {
                  endDate = new Date();
                }
                const now = new Date();
                if (now.getTime() - endDate.getTime() < 24 * 3600 * 1000) {
                  endDate = now;
                }
                endTime = endDate;
                let startTime = new Date(endTime.getTime() - 1 * 3600 * 1000);
                return (
                  <KubernetesTracesListing
                    showNamespaceFilter={false}
                    showWorkloadFilter={false}
                    destinationNamespace={drilldownQuery?.data?.cloud_resourse?.namespace ?? 'external'}
                    destinationWorkload={name}
                    namespace={drilldownQuery.data?.namespaceName}
                    workloadName={drilldownQuery.data?.workloadName}
                    accountId={accountId}
                    passedSelectedTimestamp={{
                      startTimestamp: startTime.getTime(),
                      endTimestamp: endTime.getTime(),
                    }}
                    destinationName={''}
                    showTimeFilter={true}
                    httpStatus={''}
                    duration={''}
                    showStatusFilter={false}
                  />
                );
              },
            },
          ],
        }}
        rowsPerPage={totalCount}
        totalRows={totalCount}
        showExpandable
        loading={loading}
      />
    </BoxLayout2>
  );
};

KubernetesQueueTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesQueueTable;
