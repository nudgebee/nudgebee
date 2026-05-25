import { useState, useEffect } from 'react';
import { ListingLayout } from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import DownloadButton from '@common-new/DownloadButton';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import k8sApi from '@api1/kubernetes';
import { Box } from '@mui/material';
import PropTypes from 'prop-types';
import { Text } from '@components1/common';
import KubernetesTracesListing from './KubernetesTracesListing';
import KubernetesServiceMap from '@components1/k8s/details/KubernetesServiceMap';
import AppDashboard from '@components1/dashboards/AppDashboard';
import Datetime from '@components1/common/format/Datetime';

const DBMS_HEADERS = ['Type', 'Name', 'Namespace', 'Status', 'Created At', 'Updated At'];

const KubernetesDbmsTable = ({ accountId }) => {
  const [data, setData] = useState([]);
  const [totalCount, setTotalCount] = useState(0);
  const [loading, setLoading] = useState(false);
  const [selectedStatus, setSelectedStatus] = useState('Active');
  const kubernetesDbmsTable = 'kubernetesDbmsTable';

  useEffect(() => {
    if (!accountId) {
      return;
    }
    setLoading(true);

    k8sApi
      .listFrameworkResources(accountId, ['postgres', 'mysql', 'clickhouse', 'redis', 'mongodb'], selectedStatus)
      .then((res) => {
        let data = res ?? [];
        let tableData = data?.map((item) => {
          return [
            {
              component: <Text showAutoEllipsis value={item.value} />,
            },
            {
              component: <Text showAutoEllipsis value={item.cloud_resourse.name} />,
              drilldownQuery: {
                data: item,
              },
            },
            {
              component: <Text showAutoEllipsis value={item.cloud_resourse.namespace ?? 'External'} />,
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

  const onStatusFilterChange = (e) => {
    setSelectedStatus(e.target?.value || '');
  };

  return (
    <ListingLayout id='all-status'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DownloadButton onClick={() => ({ tableId: kubernetesDbmsTable })} />
          </>
        }
      >
        <FilterDropdown
          label='Status'
          options={[
            { label: 'Active', value: 'Active' },
            { label: 'In Active', value: 'Inactive' },
          ]}
          value={selectedStatus}
          onSelect={onStatusFilterChange}
        />
      </ListingLayout.Toolbar>
      <ListingLayout.Body>
        <KubernetesTable2
          id={kubernetesDbmsTable}
          headers={DBMS_HEADERS}
          data={data}
          expandable={{
            tabs: [
              {
                text: 'Details',
                value: 0,
                key: 'WorkloadDetails',
                componentFn: function (opt, drilldownQuery) {
                  let name = drilldownQuery?.data?.cloud_resourse?.name;
                  if (name) {
                    name = name.split(':')[0];
                  }
                  let endDate = drilldownQuery?.data?.cloud_resourse?.updated_at;
                  if (endDate) {
                    endDate = new Date(endDate);
                    // for tings less than 24hrs.. keep 24hrs as limit
                    if (new Date().getTime() - endDate.getTime() < 24 * 3600 * 1000) {
                      endDate = undefined;
                    }
                  }
                  let startDate = undefined;
                  if (endDate) {
                    startDate = new Date(endDate.getTime() - 24 * 3600 * 1000);
                  }

                  return (
                    <Box>
                      <AppDashboard
                        accountId={accountId}
                        workloadName={name}
                        namespaceName={drilldownQuery?.data?.cloud_resourse?.namespace || 'external'}
                        appType={drilldownQuery?.data?.value}
                        endDate={endDate}
                        startDate={startDate}
                      />
                    </Box>
                  );
                },
              },
              {
                text: 'Service Map',
                value: 1,
                key: 'serviceMap',
                componentFn: function (opt, drilldownQuery) {
                  let name = drilldownQuery?.data?.cloud_resourse?.name;
                  if (name) {
                    name = name.split(':')[0];
                  }
                  let _endTime = new Date();
                  let endDate = drilldownQuery?.data?.cloud_resourse?.updated_at;
                  if (endDate) {
                    endDate = new Date(endDate);
                    // for tings less than 24hrs.. keep 24hrs as limit
                    if (new Date().getTime() - endDate.getTime() < 24 * 3600 * 1000) {
                      endDate = undefined;
                    }
                  }
                  if (endDate) {
                    _endTime = endDate;
                  }

                  let _startTime = new Date();
                  _startTime = new Date(_endTime.getTime() - 1 * 3600 * 1000);
                  return (
                    <KubernetesServiceMap
                      accountId={accountId}
                      appName={name}
                      namespaceName={drilldownQuery?.data?.cloud_resourse?.namespace ?? 'external'}
                      disableNamespaceFilter={true}
                      dateRange={{
                        startDateInMilli: _startTime.getTime(),
                        endDateInMilli: _endTime.getTime(),
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
                componentFn: function (opt, drilldownQuery) {
                  let name = drilldownQuery?.data?.cloud_resourse?.name;
                  if (name) {
                    name = name.split(':')[0];
                  }
                  let _endTime = new Date();
                  let endDate = drilldownQuery?.data?.cloud_resourse?.updated_at;
                  if (endDate) {
                    endDate = new Date(endDate);
                    // for tings less than 24hrs.. keep 24hrs as limit
                    if (new Date().getTime() - endDate.getTime() < 24 * 3600 * 1000) {
                      endDate = undefined;
                    }
                  }
                  if (endDate) {
                    _endTime = endDate;
                  }

                  let _startTime = new Date();
                  _startTime = new Date(_endTime.getTime() - 1 * 3600 * 1000);

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
                        startTimestamp: _startTime.getTime(),
                        endTimestamp: _endTime.getTime(),
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
      </ListingLayout.Body>
    </ListingLayout>
  );
};

KubernetesDbmsTable.propTypes = {
  accountId: PropTypes.string.isRequired,
};

export default KubernetesDbmsTable;
