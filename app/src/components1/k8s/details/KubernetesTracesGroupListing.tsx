import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { useRouter } from 'next/router';
import { formatDateForTrace, formatDurationInTrace } from 'src/utils/common';
import apiTrace from '@api1/kubernetes/trace';
import { useData } from '@context/DataContext';
import KubernetesTracesListing from './KubernetesTracesListing';
import Text from '@components1/common/format/Text';
import { Box } from '@mui/material';
import EmptyData from '@components1/common/EmptyData';
import noDataImg from '@assets/Icon-no-data-available.svg';
import { colors } from 'src/utils/colors';
import apiUser from '@api1/user';
import CustomIconButton from '@components1/CustomIconButton';
import ConversationPopup from '@components1/llm/ConversationPopup';
import { DEFAULT_TITLE, getNubiIconUrl } from '@hooks/useTenantBranding';
import SafeIcon from '@components1/common/SafeIcon';
import { md5 } from '@lib/encode';
import k8sApi from '@api1/kubernetes';
import CustomTable from '@components1/common/tables/CustomTable2';

interface PassedTimestamp {
  startTimestamp: number;
  endTimestamp: number;
}

interface KubernetesTracesGroupListingProps {
  namespace: string | string[];
  workloadName: string | string[];
  destinationWorkload: string | string[];
  destinationNamespace: string | string[];
  destinationName: string;
  showNamespaceFilter: boolean;
  showWorkloadFilter: boolean;
  showTimeFilter: boolean;
  passedSelectedTimestamp: PassedTimestamp;
}

const KubernetesTracesGroupListing: React.FC<KubernetesTracesGroupListingProps> = ({
  namespace = '',
  workloadName = '',
  destinationNamespace = '',
  destinationWorkload = '',
  destinationName = '',
  showNamespaceFilter = true,
  showWorkloadFilter = true,
  showTimeFilter = true,
  passedSelectedTimestamp = {
    startTimestamp: new Date().getTime() - 3600 * 1000,
    endTimestamp: new Date().getTime(),
  },
}) => {
  const router = useRouter();

  const [data, setData] = useState([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [workloads, setWorkloads] = useState<string[]>([]);
  const [selectedDestNamespace, setSelectedDestNamespace] = useState<string | string[]>(destinationNamespace);
  const [selectedDestWorkload, setSelectedDestWorkload] = useState<string | string[]>(destinationWorkload);
  const [time, setTime] = useState({
    startTime: passedSelectedTimestamp.startTimestamp,
    endTime: passedSelectedTimestamp.endTimestamp,
    shortcutClickTime: 0,
  });
  const [resource, setResource] = useState('');
  const [spanType, setSpanType] = useState('http');
  const selectedK8sAccount = router.query?.KubernetesDetails;
  const [recordsPerPage, setRecordsPerPage] = useState(apiUser.getUserPreferencesTablePageSize());
  const { providerCapabilities } = useData();
  const tracesProvider = providerCapabilities.find((e: any) => e.provider_type === 'traces');
  const tracesCaps = tracesProvider?.capabilities;
  const tracesProviderName = tracesProvider?.provider;
  const supportsFeature = tracesCaps?.supports_trace_grouping ?? null;
  const [analysisQuery, setAnalysisQuery] = useState<string>('');
  const [isConversationPopupOpen, setIsConversationPopupOpen] = useState(false);
  const [sessionId, setSessionId] = useState<string>('');
  const [dbmsData, setDbmsData] = useState<any[]>([]);
  const [sortObject, setSortObject] = useState({
    name: 'Error Count',
    order: 'desc',
  });

  useEffect(() => {
    k8sApi.listFrameworkResources(selectedK8sAccount as string, ['postgres', 'mysql', 'clickhouse', 'redis', 'mongodb'], '').then((res) => {
      const data = res.map((item: any) => {
        return {
          type: item.value,
          name: item.cloud_resourse.name,
          namespace: item.cloud_resourse.namespace,
        };
      });
      setDbmsData(data);
    });
  }, []);

  const LISTING_HEADER = [
    { name: 'Total Request', width: '10%' },
    { name: 'Error Count', sortEnabled: true, width: '10%' },
    { name: 'Source', width: '20%' },
    { name: 'Span', width: '10%' },
    { name: 'Status Code', width: '10%' },
    { name: 'Target' },
    { name: 'Resource' },
    { name: 'Duration', sortEnabled: true },
    { name: 'P99', sortEnabled: true },
    { name: 'P95', sortEnabled: true },
    { name: 'Max', sortEnabled: true },
    { name: '', width: '5%' },
  ];

  const listTraces = () => {
    setLoading(true);
    let sortCol = 'error_count';
    if (sortObject.name == 'Duration') {
      sortCol = 'duration_ns';
    } else if (sortObject.name == 'P99') {
      sortCol = 'p99_latency';
    } else if (sortObject.name == 'P95') {
      sortCol = 'p95_latency';
    } else if (sortObject.name == 'Max') {
      sortCol = 'max_latency';
    }
    apiTrace
      .traceGroupV2(
        selectedK8sAccount as string,
        namespace,
        workloadName,
        selectedDestNamespace,
        selectedDestWorkload,
        destinationName,
        recordsPerPage,
        currentPage * recordsPerPage,
        formatDateForTrace(time.startTime),
        formatDateForTrace(time.endTime),
        '',
        resource.replaceAll(/'/g, "\\'"),
        spanType,
        sortCol,
        sortObject.order
      )
      .then((res: any) => {
        setLoading(false);
        const groupListing = res?.traces_grouping_v3 || [];
        if (groupListing) {
          const showData = groupListing?.map((item: any) => {
            return [
              {
                component: (
                  <Text
                    value={item.count || '-'}
                    minWidth={'30px'}
                    sx={{
                      '@media(max-width: 1100px)': {
                        minWidth: '90px',
                        fontSize: '10px',
                      },
                    }}
                  />
                ),
                drilldownQuery: {
                  workloadName: item.workload_name,
                  workloadNamespace: item.workload_namespace,
                  destinationWorkload: item.destination_workload_name,
                  destinationNamespace: item.destination_workload_namespace,
                  httpStatusCode: item.http_status_code,
                  resource: item.resource,
                },
              },
              {
                component: (
                  <Text
                    value={item.error_count || '-'}
                    minWidth={'30px'}
                    sx={{
                      '@media(max-width: 1100px)': {
                        minWidth: '90px',
                        fontSize: '10px',
                      },
                    }}
                  />
                ),
              },
              {
                component: <Text sx={{ minWidth: '120px' }} value={(item?.workload_namespace || '-') + '/' + item.workload_name} showAutoEllipsis />,
              },
              {
                component: <Text value={item.span_name} />,
              },
              {
                component: <Text value={item.http_status_code} />,
              },
              {
                component: <Text value={(item?.destination_workload_namespace || '-') + '/' + item.destination_workload_name} />,
              },
              {
                component: (
                  <Text
                    value={item.resource || '-'}
                    sx={{
                      '@media(max-width: 1100px)': {
                        minWidth: '90px',
                        fontSize: '10px',
                      },
                    }}
                    showAutoEllipsis
                  />
                ),
              },
              {
                component: <Text value={formatDurationInTrace(item.duration_ns)} />,
              },
              {
                component: <Text value={formatDurationInTrace(item.p99_latency)} />,
              },
              {
                component: <Text value={formatDurationInTrace(item.p95_latency)} />,
              },
              {
                component: <Text value={formatDurationInTrace(item.max_latency)} />,
              },
              {
                component:
                  item.span_name == 'query' ? (
                    <CustomIconButton
                      onClick={(e) => {
                        e.stopPropagation();
                        handleGenerateQueryAnalysis(item);
                      }}
                      variant={'secondary'}
                      size={'xsmall'}
                      sx={{ height: '28px', mr: '4px', border: '0px', width: '28px' }}
                    >
                      <SafeIcon src={getNubiIconUrl()} width={24} height={24} alt={`Ask ${DEFAULT_TITLE}`} />{' '}
                    </CustomIconButton>
                  ) : (
                    <></>
                  ),
              },
            ];
          });
          setData(showData);
          setTotalCount(res?.traces_grouping_count_v3?.count || 0);
        } else {
          setData([]);
          setTotalCount(0);
        }
      })
      .catch(() => {
        setLoading(false);
        setData([]);
        setTotalCount(0);
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
  }, [currentPage, recordsPerPage, spanType, selectedDestNamespace, selectedDestWorkload, time, router.query?.KubernetesDetails, sortObject]);

  const onDateTimeRangeChange = (selectedDateTime: any) => {
    setTime(selectedDateTime);
    setData([]);
    setTotalCount(0);
    setCurrentPage(0);
  };

  const onResourceFilterChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    setResource(event.target.value);
  };

  const onEnterPress = () => {
    if (currentPage === 0) {
      listTraces();
    } else {
      setCurrentPage(0);
    }
  };

  const clearResourceFilter = () => {
    setResource('');
  };

  const handleGenerateQueryAnalysis = (item: any) => {
    const agent = determineTypeOfAgent(item);
    setAnalysisQuery(`Optimize the following ${agent} query: \n\n` + item.resource);
    setIsConversationPopupOpen(true);
    const session = md5([
      item.workload_name +
        item.workload_namespace +
        item.destination_workload_name +
        item.destination_workload_namespace +
        item.span_name +
        item.resource +
        item.trace_id,
    ]);
    setSessionId(session);
  };

  const determineTypeOfAgent = (item: any) => {
    let agent = '';
    //split is added to handle host:port format - Only host is extracted and checked for similarity
    const dbms = dbmsData.find(
      (dbms) =>
        dbms.name == item.destination_name ||
        dbms.name == item.destination_workload_name ||
        item.destination_workload_name == dbms.name.split(':')[0] ||
        item.destination_name == dbms.name.split(':')[0]
    );
    if (dbms) {
      agent = dbms.type;
    }
    return agent;
  };

  const handleCloseConversationPopup = () => {
    setIsConversationPopupOpen(false);
    setSessionId('');
    setAnalysisQuery('');
  };

  if (supportsFeature === false) {
    return (
      <Box sx={{ border: '1px solid', borderColor: 'divider', borderRadius: '8px', bgcolor: colors.background.white }}>
        <EmptyData
          id='trace-grouping-unsupported'
          img={noDataImg}
          heading='Trace Grouping not supported'
          subHeading={`Your current trace provider ${tracesProviderName ? `(${tracesProviderName}) ` : ''}does not support trace grouping.`}
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
        shortCuts: [
          'Last 5 Minutes',
          'Last 10 Minutes',
          'Last 15 Minutes',
          'Last 30 Minutes',
          'Last 1 Hour',
          'Last 3 Hours',
          'Last 6 Hours',
          'Last 12 Hours',
          'Last 24 Hours',
          'Current Week',
        ],
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
            const val = e.target.value;
            setSelectedDestNamespace(val);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Destination Namespace',
          value: selectedDestNamespace as string,
        },
        {
          type: 'dropdown',
          enabled: showWorkloadFilter,
          options: workloads,
          onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
            setSelectedDestWorkload(e.target.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Destination Workload',
          value: selectedDestWorkload as string,
        },
        {
          type: 'dropdown',
          enabled: true,
          options: ['http', 'query'],
          onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
            setSpanType(e?.target?.value);
            setCurrentPage(0);
          },
          minWidth: '150px',
          label: 'Span Type',
          value: spanType,
        },
        {
          type: 'search',
          enabled: true,
          onSelect: onResourceFilterChange,
          minWidth: '150px',
          label: 'Search By Resource',
          onEnter: onEnterPress,
          onClear: clearResourceFilter,
        },
      ]}
    >
      <ConversationPopup
        open={isConversationPopupOpen}
        handleClose={() => handleCloseConversationPopup()}
        query={analysisQuery}
        sessionId={sessionId}
        accountId={router.query?.KubernetesDetails as string}
        title='Query Optimization'
      />
      <CustomTable
        id='k8s-trace-group-listing'
        // @ts-ignore: allow flexible header types
        headers={LISTING_HEADER}
        rowsPerPage={recordsPerPage}
        tableData={data}
        onPageChange={(page: number, limit: number) => {
          setCurrentPage(page - 1);
          setRecordsPerPage(limit);
        }}
        totalRows={totalCount}
        loading={loading}
        pageNumber={currentPage + 1}
        showExpandable={true}
        sort={sortObject}
        expandable={{
          tabs: [
            {
              componentFn: function (_opt: any, drilldownQuery: any) {
                return (
                  <KubernetesTracesListing
                    showNamespaceFilter={false}
                    showWorkloadFilter={false}
                    destinationNamespace={drilldownQuery.destinationNamespace}
                    destinationWorkload={drilldownQuery.destinationWorkload}
                    namespace={drilldownQuery.workloadNamespace}
                    workloadName={drilldownQuery.workloadName}
                    accountId={router.query?.KubernetesDetails as string}
                    passedSelectedTimestamp={{
                      startTimestamp: time.startTime,
                      endTimestamp: time.endTime,
                    }}
                    showTimeFilter={false}
                    apiOrQuery={drilldownQuery.resource}
                    httpStatus={drilldownQuery.httpStatusCode}
                  />
                );
              },
              text: 'Traces',
              value: 0,
              key: 'traces',
            },
          ],
        }}
        onSortChange={(e) => {
          setSortObject(e);
        }}
      />
    </BoxLayout2>
  );
};

export default KubernetesTracesGroupListing;
