import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { useRouter } from 'next/router';
import { formatDateForTrace } from 'src/utils/common';
import apiTrace from '@api1/kubernetes/trace';
import { Text } from '@components1/common';
import apiUser from '@api1/user';
import { useData } from '@context/DataContext';
import KubernetesTracesListing from './KubernetesTracesListing';
import { Box } from '@mui/material';
import EmptyData from '@components1/common/EmptyData';
import noDataImg from '@assets/Icon-no-data-available.svg';
import { colors } from 'src/utils/colors';

interface PassedTimestamp {
  startTimestamp: number;
  endTimestamp: number;
}

interface KubernetesTracesCrossZoneListingProps {
  namespace: string;
  workloadName: string;
  destinationWorkload: string;
  destinationNamespace: string;
  destinationName: string;
  showNamespaceFilter: boolean;
  showWorkloadFilter: boolean;
  showTimeFilter: boolean;
  passedSelectedTimestamp: PassedTimestamp;
}

const KubernetesTracesCrossZoneListing: React.FC<KubernetesTracesCrossZoneListingProps> = ({
  namespace = '',
  workloadName = '',
  destinationNamespace = '',
  destinationWorkload = '',
  destinationName = '',
  showNamespaceFilter = true,
  showWorkloadFilter = true,
  showTimeFilter = true,
  passedSelectedTimestamp = {
    startTimestamp: new Date().getTime() - 24 * 3600 * 1000,
    endTimestamp: new Date().getTime(),
  },
}) => {
  const router = useRouter();
  const [data, setData] = useState([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [allWorkloads, _setAllWorkloads] = useState<string[]>([]);
  const [workloads, setWorkloads] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<string>(namespace);
  const [selectedWorkload, setSelectedWorkload] = useState<string>(workloadName);
  const [errorMsg, setErrorMsg] = useState('');
  const [time, setTime] = useState({
    startTime: passedSelectedTimestamp.startTimestamp,
    endTime: passedSelectedTimestamp.endTimestamp,
    shortcutClickTime: 0,
  });

  const selectedK8sAccount = router.query?.KubernetesDetails;
  const { providerCapabilities } = useData();
  const tracesProvider = providerCapabilities.find((e: any) => e.provider_type === 'traces');
  const tracesCaps = tracesProvider?.capabilities;
  const tracesProviderName = tracesProvider?.provider;
  const supportsFeature = tracesCaps?.supports_cross_zone_communication ?? null;
  const LISTING_HEADER = [
    { name: 'Source', width: '30%' },
    { name: 'Source Zone', width: '10%' },
    { name: 'Target', width: '30%' },
    { name: 'Target Zone', width: '15%' },
    'Count',
  ];
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());

  const listTraces = () => {
    setLoading(true);
    setErrorMsg('');
    apiTrace
      .traceGroupZones({
        accountId: selectedK8sAccount as string,
        namespace: selectedNamespace,
        workload: selectedWorkload,
        destinationNamespace,
        destinationWorkload,
        destinationName,
        limit: 10,
        offset: currentPage * recordsPerPage,
        startDate: formatDateForTrace(time.startTime),
        endDate: formatDateForTrace(time.endTime),
      })
      .then((res: any) => {
        setLoading(false);
        if (res) {
          const showData = res?.traces_groupings?.rows?.map((item: any) => {
            return [
              {
                component: <Text showAutoEllipsis value={(item?.workload_namespace || '-') + '/' + item.workload_name} />,
                drilldownQuery: {
                  workloadName: item.workload_name,
                  workloadNamespace: item.workload_namespace,
                  destinationWorkload: item.destination_workload_name,
                  destinationNamespace: item.destination_workload_namespace,
                  sourceZone: item.workload_zone,
                  targetZone: item.destination_workload_zone,
                },
              },
              {
                component: <Text value={item.workload_zone || '-'} />,
              },
              {
                component: <Text showAutoEllipsis value={(item?.destination_workload_namespace || '-') + '/' + item.destination_workload_name} />,
              },
              {
                component: <Text value={item.destination_workload_zone} />,
              },
              {
                component: <Text value={item.count} />,
              },
            ];
          });
          setData(showData);
          setTotalCount(res?.traces_groupings_v2_aggregate.rows[0].count);
        } else {
          setData([]);
          setTotalCount(0);
        }
      })
      .catch(() => {
        setLoading(false);
        setData([]);
        setTotalCount(0);
        setErrorMsg('Failed to traces');
      });
  };

  useEffect(() => {
    if (showNamespaceFilter && showWorkloadFilter) {
      apiTrace
        .traceDistinctWorloadAndNamespace(selectedK8sAccount as string, {
          startDate: formatDateForTrace(time.startTime),
          endDate: formatDateForTrace(time.endTime),
          destinationNamespace,
          destinationWorkload,
          showNamespaceFilter,
          showWorkloadFilter,
        })
        .then((res) => {
          if (res && Object.keys(res).length > 0) {
            const workload_name = res?.workload_name?.values || [];
            const workload_namespace = res?.workload_namespace?.values || [];
            setWorkloads(workload_name.sort((a: string, b: string) => a.localeCompare(b)));
            setNamespaces(workload_namespace.sort((a: string, b: string) => a.localeCompare(b)));
          }
        });
    }
  }, [time, router.query?.KubernetesDetails]);

  useEffect(() => {
    listTraces();
  }, [currentPage, recordsPerPage, selectedNamespace, selectedWorkload, time, router.query?.KubernetesDetails]);

  const filterWorkloadOnSelectedNamespace = (value: string) => {
    if (value) {
      const filteredWorkloads = allWorkloads
        .filter((m) => m.split('|')[1] == value)
        .map((g) => g.split('|')[0])
        .sort((a, b) => a.localeCompare(b));

      setWorkloads(filteredWorkloads);
    } else {
      setWorkloads(allWorkloads.map((g) => g.split('|')[0]).sort((a, b) => a.localeCompare(b)));
    }
  };

  const onDateTimeRangeChange = (selectedDateTime: any) => {
    setTime(selectedDateTime);
    setData([]);
    setTotalCount(0);
  };

  const renderKubernetesTracesListing = (opt: any, drilldownQuery: any, _row: any) => (
    <KubernetesTracesListing
      namespace={drilldownQuery.workloadNamespace}
      workloadName={drilldownQuery.workloadName}
      destinationNamespace={drilldownQuery.destinationNamespace}
      destinationWorkload={drilldownQuery.destinationWorkload}
      destinationName={''}
      showNamespaceFilter={false}
      showWorkloadFilter={false}
      showStatusFilter={true}
      showTimeFilter={false}
      passedSelectedTimestamp={{
        startTimestamp: time.startTime,
        endTimestamp: time.endTime,
      }}
      fixedTrace={false}
      httpStatus={''}
      accountId={router.query?.KubernetesDetails as string}
      duration={null}
      apiOrQuery={''}
    />
  );

  if (supportsFeature === false) {
    return (
      <Box sx={{ border: '1px solid', borderColor: 'divider', borderRadius: '8px', bgcolor: colors.background.white }}>
        <EmptyData
          id='cross-zone-unsupported'
          img={noDataImg}
          heading='Cross-Zone Communication not supported'
          subHeading={`Your current trace provider ${
            tracesProviderName ? `(${tracesProviderName}) ` : ''
          }does not support cross-zone communication metrics.`}
          height='400px'
          sx={{ flexDirection: 'column', gap: '16px', textAlign: 'center' }}
        />
      </Box>
    );
  }

  return (
    <BoxLayout2
      id='k8s-traces-group-box'
      dateTimeRange={{
        enabled: showTimeFilter,
        onChange: onDateTimeRangeChange,
        passedSelectedDateTime: time,
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
              tableId: 'k8s-trace-group-listing',
            };
          },
        },
      }}
      filterOptions={[
        {
          type: 'dropdown',
          enabled: showNamespaceFilter,
          options: namespaces,
          onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
            setSelectedNamespace(e?.target?.value);
            filterWorkloadOnSelectedNamespace(e?.target?.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Namespace',
          value: selectedNamespace,
        },
        {
          type: 'dropdown',
          enabled: showWorkloadFilter,
          options: workloads,
          onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
            setSelectedWorkload(e?.target?.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Workload',
          value: selectedWorkload,
        },
      ]}
    >
      <KubernetesTable2
        id='k8s-trace-group-listing'
        headers={LISTING_HEADER}
        rowsPerPage={recordsPerPage}
        data={data}
        onPageChange={(page: number, limit: number) => {
          setCurrentPage(page - 1);
          setRecordsPerPage(limit);
        }}
        totalRows={totalCount}
        loading={loading}
        onSortChange={{}}
        pageNumber={currentPage + 1}
        errorMessage={errorMsg}
        showExpandable={true}
        expandable={{
          tabs: [
            {
              componentFn: renderKubernetesTracesListing,
              text: 'Traces',
              value: 0,
              key: 'traces',
            },
          ],
        }}
      />
    </BoxLayout2>
  );
};

export default KubernetesTracesCrossZoneListing;
