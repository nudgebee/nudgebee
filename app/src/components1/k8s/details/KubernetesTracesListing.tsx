import React, { useEffect, useState } from 'react';
import BoxLayout2 from '@components1/common/BoxLayout2';
import { KubernetesUtilizationCharts } from '@components1/k8s/common/KubernetesTable2';
import { useRouter } from 'next/router';
import {
  type SortOrderObject,
  extractIp,
  formatDateForPlusMinusDuration,
  formatDateForTrace,
  formatDurationInTrace,
  snakeToTitleCase,
} from 'src/utils/common';
import DateTime from '@components1/common/format/Datetime';
import { Box, Typography } from '@mui/material';
import apiTrace from '@api1/kubernetes/trace';
import k8sApi from '@api1/kubernetes';
import SafeIcon from '@components1/common/SafeIcon';
import { RightArrowIcon } from '@assets';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import Text from '@components1/common/format/Text';
import KubernetesLogs from './KubernetesLogs';
import apiUser from '@api1/user';
import { colors } from 'src/utils/colors';
import { applyFiltersOnRouter } from '@lib/router';
import CustomIconButton from '@components1/CustomIconButton';
import ConversationPopup from '@components1/llm/ConversationPopup';
import CustomTable from '@components1/common/tables/CustomTable2';
import { useData } from '@context/DataContext';
import { KubernetesTraceServiceOperation } from '@components1/k8s/common/KubernetesTraceServiceOperation';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import apiAccount from '@api1/account';
import CloudProviderIcon from '@components1/common/CloudIcon';

interface PassedTimestamp {
  startTimestamp: number;
  endTimestamp: number;
}

interface KubernetesTracesListingProps {
  namespace: string;
  workloadName: string;
  destinationWorkload: string;
  destinationNamespace: string;
  destinationName?: string;
  showNamespaceFilter: boolean;
  showWorkloadFilter: boolean;
  showTimeFilter: boolean;
  passedSelectedTimestamp: PassedTimestamp;
  fixedTrace?: boolean;
  httpStatus?: string | string[];
  accountId: string;
  duration?: number | null;
  showStatusFilter?: boolean;
  apiOrQuery?: string;
  statusCode?: string;
  fromWorkload?: boolean;
  traceData?: any[];
  displaySideFilters?: boolean;
  traceIds?: string[];
}

interface SourceDestinationViewProps {
  namespace: string;
  workloadName: string;
  destinationWorkload: string;
  destinationNamespace: string;
}

const SourceDestinationView: React.FC<SourceDestinationViewProps> = ({ namespace, workloadName, destinationNamespace, destinationWorkload }) => {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', m: '24px 0px 16px' }}>
      <Box sx={{ border: `1px solid ${colors.border.secondary}`, borderRadius: '4px', p: '12px 16px', mr: '10px', flex: 1 }}>
        <Typography sx={{ fontSize: '12px', fontWeight: 500, fontColor: colors.text.secondaryDark }}>Source:</Typography>
        <Typography sx={{ fontSize: '18px', fontWeight: 500, fontColor: colors.text.secondary }}>{workloadName}</Typography>
        <Typography sx={{ fontSize: '12px', fontWeight: 400, fontColor: colors.text.secondaryDark }}>ns: {namespace}</Typography>
      </Box>
      <Box sx={{}}>
        <SafeIcon src={RightArrowIcon} width={24} alt={'Right Arrow Icon'} />
      </Box>
      <Box sx={{ border: `1px solid ${colors.border.secondary}`, borderRadius: '4px', p: '12px 16px', ml: '10px', flex: 1 }}>
        <Typography sx={{ fontSize: '12px', fontWeight: 500, fontColor: colors.text.secondaryDark }}>Destination:</Typography>
        <Typography sx={{ fontSize: '18px', fontWeight: 500, fontColor: colors.text.secondary }}>{destinationWorkload}</Typography>
        <Typography sx={{ fontSize: '12px', fontWeight: 400, fontColor: colors.text.secondaryDark }}>ns: {destinationNamespace}</Typography>
      </Box>
    </Box>
  );
};

const KubernetesTracesListing: React.FC<KubernetesTracesListingProps> = ({
  namespace = '',
  workloadName = '',
  destinationNamespace = '',
  destinationWorkload = '',
  destinationName = '',
  showNamespaceFilter = true,
  showWorkloadFilter = true,
  showStatusFilter = true,
  showTimeFilter = true,
  passedSelectedTimestamp = {
    startTimestamp: new Date().getTime() - 15 * 60 * 1000,
    endTimestamp: new Date().getTime(),
  },
  fixedTrace = false,
  httpStatus = '',
  accountId = '',
  duration = null,
  apiOrQuery = '',
  statusCode = '',
  fromWorkload = false,
  traceData = [],
  displaySideFilters = true,
  traceIds = [],
}) => {
  const router = useRouter();
  const { assistantName } = useTenantBranding();
  const LISTING_HEADER = [
    { name: 'Timestamp', width: '5%' },
    !fixedTrace ? { name: 'Source', width: '25%' } : '',
    { name: 'Status Code', width: '5%' },
    { name: 'Span', width: '5%' },
    { name: 'Duration', sortEnabled: true, width: '5%' },
    { name: 'Resource', width: '15%' },
    !fixedTrace ? { name: 'Destination', width: '35%' } : '',
    { name: '', width: '5%' },
  ];
  const tracesSource = ['ebpf', 'otel'];
  const selectedK8sAccount = (router.query?.KubernetesDetails as string) || (router.query?.accountId as string) || accountId;
  const { selectedCluster } = useData();

  const getInitialWorkload = () => {
    if (workloadName) {
      return [workloadName];
    }
    if (router?.query?.workloadName) {
      return [router.query.workloadName];
    }
    return [];
  };

  const getInitialDestinationWorkload = () => {
    if (destinationWorkload) {
      return [destinationWorkload];
    }
    if (router?.query?.destinationWorkload) {
      return [router.query.destinationWorkload];
    }
    return [];
  };

  const getInitialNamespace = () => {
    if (namespace) {
      return [namespace];
    }
    if (router?.query?.namespaceName) {
      return [router.query.namespaceName];
    }
    return [];
  };

  const getInitialDestinationNamespace = () => {
    if (destinationNamespace) {
      return [destinationNamespace];
    }
    if (router?.query?.destinationNamespace) {
      return [router.query.destinationNamespace];
    }
    return [];
  };

  const getInitialDuration = () => {
    if (duration) {
      return duration;
    }
    if (router?.query?.duration) {
      return parseInt(router.query.duration as string);
    }
    return null;
  };

  const getInitialTime = () => {
    if (router.query.start_time && router.query.end_time) {
      return {
        startTime: parseInt(router.query.start_time as string),
        endTime: parseInt(router.query.end_time as string),
        shortcutClickTime: 0,
      };
    }
    return { startTime: passedSelectedTimestamp.startTimestamp, endTime: passedSelectedTimestamp.endTimestamp, shortcutClickTime: 0 };
  };

  const getInitialResource = () => {
    if (apiOrQuery) {
      return apiOrQuery;
    }
    if (router?.query?.resource) {
      return router.query.resource as string;
    }
    return '';
  };

  const normalizeStatusCodeLabel = (v: string): string => {
    const upper = v.toUpperCase();
    if (upper === 'OK' || upper === 'STATUS_CODE_OK') return 'Ok';
    if (upper === 'ERROR' || upper === 'STATUS_CODE_ERROR') return 'Error';
    if (upper === 'UNSET' || upper === 'STATUS_CODE_UNSET') return 'Unset';
    return v;
  };

  const [data, setData] = useState<any[]>([]);
  const [loading, setLoading] = useState<boolean>(false);
  const [totalCount, setTotalCount] = useState<number>(0);
  const [currentPage, setCurrentPage] = useState<number>(0);
  const [recordsPerPage, setRecordsPerPage] = useState<number>(apiUser.getUserPreferencesTablePageSize());
  const [namespaces, setNamespaces] = useState<string[]>([]);
  const [allWorkloads, _setAllWorkloads] = useState<string[]>([]);
  const [workloads, setWorkloads] = useState<string[]>([]);
  const [selectedNamespace, setSelectedNamespace] = useState<any>(getInitialNamespace());
  const [selectedWorkload, setSelectedWorkload] = useState<any>(getInitialWorkload());
  const [destinationNamespaces, setDestinationNamespaces] = useState<string[]>([]);
  const [destinationAllWorkloads, _setDestinationAllWorkloads] = useState<string[]>([]);
  const [destinationWorkloads, setDestinationWorkloads] = useState<string[]>([]);
  const [destinationSelectedNamespace, setDestinationSelectedNamespace] = useState<any>(getInitialDestinationNamespace());
  const [destinationSelectedWorkload, setDestinationSelectedWorkload] = useState<any>(getInitialDestinationWorkload());
  const [httpStatusCodes, setHttpStatusCodes] = useState<string[]>([]);
  const [selectedHttpStatus, setSelectedHttpStatus] = useState<string | string[]>(httpStatus);
  const [httpSpans, setHttpSpans] = useState<string[]>([]);
  const [statusCodes, setStatusCodes] = useState<string[]>([]);
  const [selectedHttpSpan, setSelectedHttpSpan] = useState<string>('');
  const [errorMsg, setErrorMsg] = useState('');
  const [time, setTime] = useState(getInitialTime());
  const [resource, setResource] = useState(getInitialResource());
  const [sortObject, setSortObject] = useState<SortOrderObject>({
    name: 'Timestamp',
    order: 'desc',
  });
  const [header, setHeader] = useState('');
  const [selectedStatusCode, setSelectedStatusCode] = useState<string>((router?.query?.statusCode as string) || statusCode || '');
  const [resetDateTime, setResetDateTime] = useState(0);
  const [expandedAccordions, setExpandedAccordions] = useState({});
  const [selectedTracesSource, setSelectedTracesSource] = useState<string>('');
  const [traceId, setTraceId] = useState<string>('');
  const [analysisQuery, setAnalysisQuery] = useState<string>('');
  const [isConversationPopupOpen, setIsConversationPopupOpen] = useState(false);
  const [sessionId, setSessionId] = useState<string>('');
  const [traceProvider, setTraceProvider] = useState('');
  const [services, setServices] = useState<string[]>([]);

  useEffect(() => {
    if (traceData.length > 0) {
      return;
    }
    setTraceProvider('');
    setLoading(true);
    handleClearAll();
    const init = async () => {
      if (selectedK8sAccount === 'demo') {
        setTraceProvider('otel_clickhouse');
      } else {
        const defaultProvider = await fetchDefaultProvider();
        setTraceProvider(defaultProvider);
      }
    };

    init();
  }, [router.query?.KubernetesDetails, accountId, selectedCluster]);

  const fetchDefaultProvider = async () => {
    try {
      const res = await apiAccount.getDefaultProvider({
        account_id: selectedK8sAccount,
        provider_type: 'traces',
      });
      if (res?.data?.errors) {
        return '';
      }
      return res?.data?.data?.get_default_provider?.provider || '';
    } catch (error: any) {
      console.error(error);
      return '';
    }
  };

  const getHttpStatusText = (item: any) => {
    if (item.span_name !== 'query') {
      return item.http_status_code || '-';
    }
    if (item.status_code === 'STATUS_CODE_UNSET') {
      return 'OK';
    }
    if (item.status_code === 'STATUS_CODE_ERROR') {
      return 'ERROR';
    }
    return '-';
  };

  const getTraceTableData = (traceData: any[]) => {
    const showData =
      traceData?.map((item: any) => {
        return [
          {
            component: <DateTime value={item?.timestamp} baseDate={new Date()} />,
            drilldownQuery: item,
          },
          !fixedTrace && {
            component: (
              <Box>
                <Text
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: '12px',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: '10px',
                    },
                  }}
                  showAutoEllipsis
                  value={`name: ${item.workload_name}`}
                />
                <Text
                  secondaryText
                  showAutoEllipsis
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: '12px',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: '10px',
                    },
                  }}
                  value={`ns: ${item.workload_namespace}`}
                />
              </Box>
            ),
          },
          {
            component: (
              <Text
                sx={{
                  '@media(max-width: 1425px)': {
                    fontSize: '12px',
                  },
                }}
                value={getHttpStatusText(item)}
              />
            ),
          },
          {
            component: (
              <Text
                sx={{
                  '@media(max-width: 1425px)': {
                    fontSize: '12px',
                  },
                }}
                value={item.span_name}
              />
            ),
          },
          {
            component: (
              <Text
                sx={{
                  '@media(max-width: 1425px)': {
                    fontSize: '12px',
                  },
                }}
                value={formatDurationInTrace(item.duration_ns)}
              />
            ),
          },
          {
            component: (
              <Text
                value={item.resource}
                showAutoEllipsis
                sx={{
                  '@media(max-width: 1425px)': {
                    fontSize: '12px',
                  },
                  '@media(max-width: 1100px)': {
                    fontSize: '10px',
                  },
                }}
              />
            ),
          },
          !fixedTrace && {
            component: (
              <Box>
                <Text
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: '12px',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: '10px',
                    },
                  }}
                  showAutoEllipsis
                  value={`name: ${item.destination_name}`}
                />
                <Text
                  secondaryText
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: '12px',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: '10px',
                    },
                  }}
                  value={`ns: ${item.destination_workload_namespace}`}
                />
                <Text
                  secondaryText
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: '12px',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: '10px',
                    },
                  }}
                  value={`wl: ${item.destination_workload_name}`}
                />
              </Box>
            ),
          },
          {
            component:
              item?.span_name === 'query' && item.resource ? (
                <CustomIconButton
                  onClick={(e) => {
                    e.stopPropagation();
                    handleGenerateQueryAnalysis(item);
                  }}
                  variant={'secondary'}
                  size={'xsmall'}
                  sx={{ height: '28px', mr: '4px', width: '28px' }}
                >
                  <SafeIcon src={getNubiIconUrl()} width={24} height={24} alt={`Ask ${assistantName}`} />{' '}
                </CustomIconButton>
              ) : (
                <></>
              ),
          },
        ];
      }) || [];
    setData(showData);
  };

  const listTraces = () => {
    setLoading(true);
    setErrorMsg('');
    setData([]);
    setTotalCount(0);
    let sortCol = 'timestamp';
    if (sortObject.name == 'Duration') {
      sortCol = 'duration_ns';
    }
    apiTrace
      .traceV2({
        accountId: selectedK8sAccount,
        namespace: selectedNamespace,
        workload: selectedWorkload,
        destinationNamespace: destinationSelectedNamespace,
        destinationWorkload: destinationSelectedWorkload,
        destinationName: destinationName,
        limit: recordsPerPage,
        offset: currentPage * recordsPerPage,
        startDate: formatDateForTrace(time.startTime),
        endDate: formatDateForTrace(time.endTime),
        selectedHttpStatus: selectedHttpStatus,
        selectedHttpSpan: selectedHttpSpan,
        resource: resource.replaceAll(/'/g, "\\'"),
        duration: getInitialDuration(),
        sortCol: sortCol,
        sortOrder: sortObject.order,
        header: header.replaceAll(/'/g, "\\'"),
        selectedStatusCode: selectedStatusCode,
        traceSource: selectedTracesSource,
        traceId: traceId || traceIds,
        fromWorkload: fromWorkload,
        cols: [
          'trace_id',
          'span_id',
          'parent_span_id',
          'workload_namespace',
          'workload_name',
          'timestamp',
          'status_code',
          'span_name',
          'resource',
          'duration_ns',
          'destination_workload_name',
          'destination_workload_namespace',
          'destination_name',
          'headers',
          'http_status_code',
          'request_payload',
          'http_response',
          'trace_source',
          'span_attributes',
        ],
      })
      .then((res: any) => {
        setLoading(false);
        if (res) {
          const traces = res?.traces_query || [];
          getTraceTableData(traces);
          const serverCount = res?.traces_counts?.count;
          if (serverCount === -1) {
            // Jaeger doesn't support counting — estimate total for pagination
            if (traces.length >= recordsPerPage) {
              setTotalCount((currentPage + 2) * recordsPerPage);
            } else {
              setTotalCount(currentPage * recordsPerPage + traces.length);
            }
          } else {
            setTotalCount(serverCount || 0);
          }
        }
      })
      .catch(() => {
        setLoading(false);
        setErrorMsg('Failed to traces');
      });
  };

  const sortEventChange = (e: any) => {
    setSortObject(e);
  };

  useEffect(() => {
    if (traceData.length > 0 || !traceProvider) {
      return;
    }
    if (showNamespaceFilter && showWorkloadFilter && traceProvider === 'otel_clickhouse') {
      apiTrace
        .traceDistinctWorloadAndNamespace(selectedK8sAccount, {
          startDate: formatDateForTrace(time.startTime),
          endDate: formatDateForTrace(time.endTime),
          destinationNamespace,
          destinationWorkload,
          showNamespaceFilter,
          showWorkloadFilter,
        })
        .then((res) => {
          if (res && Object.keys(res).length > 0) {
            const destination_workload_name = res?.destination_workload_name?.values || [];
            const destination_workload_namespace = res?.destination_workload_namespace?.values || [];
            const workload_name = res?.workload_name?.values || [];
            const workload_namespace = res?.workload_namespace?.values || [];
            setDestinationWorkloads(destination_workload_name.filter((v: string) => v?.trim()).sort((a: string, b: string) => a.localeCompare(b)));

            setDestinationNamespaces(
              destination_workload_namespace.filter((v: string) => v?.trim()).sort((a: string, b: string) => a.localeCompare(b))
            );

            setWorkloads(workload_name.filter((v: string) => v?.trim()).sort((a: string, b: string) => a.localeCompare(b)));

            setNamespaces(workload_namespace.filter((v: string) => v?.trim()).sort((a: string, b: string) => a.localeCompare(b)));
          }
        });
    }
    apiTrace
      .traceDistinctFilters(selectedK8sAccount, {
        startDate: formatDateForTrace(time.startTime),
        endDate: formatDateForTrace(time.endTime),
        destinationNamespace,
        destinationWorkload,
        showNamespaceFilter,
        showWorkloadFilter,
      })
      .then((res) => {
        if (res && Object.keys(res).length > 0) {
          const span_name = res?.span_name?.values || [];
          const http_status_code = res?.http_status_code?.values || [];
          const status_code = res?.status_code?.values || [];
          setHttpStatusCodes(http_status_code.filter((v: string) => v?.trim()));
          setHttpSpans(span_name.filter((v: string) => v?.trim()));
          setStatusCodes(status_code.filter((v: string) => v?.trim()));
        }
      });
  }, [traceProvider, time, router.query?.KubernetesDetails]);

  // Fetch services for Jaeger provider
  useEffect(() => {
    if (traceData.length > 0 || !traceProvider || traceProvider === 'otel_clickhouse') {
      return;
    }
    apiTrace
      .traceDistinctWorloadAndNamespace(selectedK8sAccount, {
        startDate: formatDateForTrace(time.startTime),
        endDate: formatDateForTrace(time.endTime),
      })
      .then((res) => {
        if (res && Object.keys(res).length > 0) {
          const serviceNames = res?.workload_name?.values || [];
          const sortedServices = serviceNames.filter((v: string) => v?.trim()).sort((a: string, b: string) => a.localeCompare(b));
          setServices(sortedServices);
        }
      })
      .catch((err) => {
        console.error('Failed to fetch services for Jaeger:', err);
      });
  }, [traceProvider, time, selectedK8sAccount]);

  useEffect(() => {
    if (traceData.length > 0) {
      getTraceTableData(traceData);
    }
  }, [traceData]);

  const traceDeps =
    traceProvider != 'otel_clickhouse'
      ? [
          currentPage,
          time,
          selectedK8sAccount,
          recordsPerPage,
          selectedWorkload,
          selectedStatusCode,
          selectedHttpStatus,
          selectedHttpSpan,
          destinationSelectedNamespace,
          destinationSelectedWorkload,
          resource,
          sortObject,
        ]
      : [
          currentPage,
          recordsPerPage,
          selectedNamespace,
          selectedWorkload,
          time,
          selectedHttpStatus,
          selectedHttpSpan,
          selectedK8sAccount,
          sortObject,
          destinationSelectedNamespace,
          destinationSelectedWorkload,
          selectedStatusCode,
          selectedTracesSource,
        ];

  useEffect(() => {
    if (traceData.length > 0 || !traceProvider) {
      return;
    }
    listTraces();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [traceProvider, ...traceDeps]);

  const filterWorkloadOnSelectedNamespace = (value: string | string[]) => {
    let filteredWorkloads;
    if (Array.isArray(value)) {
      filteredWorkloads = [...new Set<string>(allWorkloads.filter((m) => value.includes(m.split('|')[1])).map((g) => g.split('|')[0]))];
    } else if (value) {
      filteredWorkloads = [...new Set<string>(allWorkloads.filter((m) => m.split('|')[1] === value).map((g) => g.split('|')[0]))];
    } else {
      filteredWorkloads = [...new Set<string>(allWorkloads.map((g) => g.split('|')[0]))];
    }

    setWorkloads(filteredWorkloads.sort((a, b) => a.localeCompare(b)));
  };

  const filterDestinationWorkloadOnSelectedDestinationNamespace = (value: string | string[]) => {
    let filteredWorkloads;
    if (Array.isArray(value)) {
      filteredWorkloads = [...new Set<string>(destinationAllWorkloads.filter((m) => value.includes(m.split('|')[1])).map((g) => g.split('|')[0]))];
    } else if (value) {
      filteredWorkloads = [...new Set<string>(destinationAllWorkloads.filter((m) => m.split('|')[1] === value).map((g) => g.split('|')[0]))];
    } else {
      filteredWorkloads = [...new Set<string>(destinationAllWorkloads.map((g) => g.split('|')[0]))];
    }

    setDestinationWorkloads(filteredWorkloads.sort((a, b) => a.localeCompare(b)));
  };

  const onDateTimeRangeChange = (selectedDateTime: any) => {
    if (selectedDateTime?.shortcutClickTime > 0) {
      setTime({
        ...selectedDateTime,
        startTime: new Date().getTime() - selectedDateTime.shortcutClickTime,
        endTime: new Date().getTime(),
      });
      applyFiltersOnRouter(router, { start_time: new Date().getTime() - selectedDateTime.shortcutClickTime, end_time: new Date().getTime() });
    } else {
      setTime(selectedDateTime);
      applyFiltersOnRouter(router, { start_time: selectedDateTime.startTime, end_time: selectedDateTime.endTime });
    }
  };

  const getHeaderObject = (headerInput: string, isMaterialized = false) => {
    let parsedHeader: Record<string, any> = {};

    try {
      const rawString = isMaterialized ? headerInput : atob(headerInput);
      parsedHeader = JSON.parse(rawString);
    } catch {
      return null;
    }

    if (parsedHeader && typeof parsedHeader === 'object' && Object.keys(parsedHeader).length > 0) {
      return Object.entries(parsedHeader).map(([key, value]) => <Typography key={key}>{`${key}: ${value}`}</Typography>);
    }

    return null;
  };

  const onResourceFilterChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    setResource(event.target.value);
  };

  const onHeaderFilterChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    setHeader(event.target.value);
  };

  const onTraceFilterChange = (event: React.ChangeEvent<HTMLInputElement>) => {
    setTraceId(event.target.value);
  };

  const onEnterPress = () => {
    setCurrentPage(0);
    if (currentPage == 0) {
      listTraces();
    }
  };

  const handleClearAll = () => {
    setSelectedNamespace(getInitialNamespace());
    setSelectedWorkload(getInitialWorkload());
    setDestinationSelectedNamespace(getInitialDestinationNamespace());
    setDestinationSelectedWorkload(getInitialDestinationWorkload());
    setSelectedHttpStatus('');
    setSelectedHttpSpan('');
    setStatusCodes([]);
    setResource('');
    setHeader('');
    setSelectedStatusCode('');
    setCurrentPage(0);
    setTime({ startTime: passedSelectedTimestamp.startTimestamp, endTime: passedSelectedTimestamp.endTimestamp, shortcutClickTime: 0 });
    setSortObject({
      name: 'Timestamp',
      order: 'desc',
    });
    setResetDateTime((prev) => prev + 1);
    filterWorkloadOnSelectedNamespace([]);
    filterDestinationWorkloadOnSelectedDestinationNamespace('');
    setExpandedAccordions({});
  };

  const handleGenerateQueryAnalysis = async (item: any) => {
    try {
      const res = await k8sApi.listFrameworkResources(accountId, ['postgres', 'mysql', 'clickhouse', 'redis', 'mongodb'], '');

      if (Array.isArray(res)) {
        const data = res.map((item: any) => ({
          type: item?.value,
          name: item?.cloud_resourse?.name,
          namespace: item?.cloud_resourse?.namespace,
        }));
        const agent = determineTypeOfAgent(item, data);
        setAnalysisQuery(`Optimize the following ${agent} query: \n\n` + item.resource);
        setIsConversationPopupOpen(true);
        setSessionId(item.trace_id);
      }
    } catch (error) {
      console.error('Error fetching framework resources:', error);
    }
  };

  const determineTypeOfAgent = (item: any, dbmsData: any) => {
    let agent = '';
    //split is added to handle host:port format - Only host is extracted and checked for similarity
    const dbms = dbmsData.find(
      (dbms: any) =>
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

  return (
    <>
      <ConversationPopup
        open={isConversationPopupOpen}
        handleClose={() => handleCloseConversationPopup()}
        query={analysisQuery}
        sessionId={sessionId}
        accountId={accountId}
        title='Query Optimization'
      />
      {fixedTrace && (
        <SourceDestinationView
          namespace={namespace}
          workloadName={workloadName}
          destinationNamespace={destinationNamespace}
          destinationWorkload={destinationWorkload}
        />
      )}
      <BoxLayout2
        id='k8s-traces-box'
        marginBottom='0px'
        setExpandedAccordions={setExpandedAccordions}
        expandedAccordions={expandedAccordions}
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
        resetDateTime={resetDateTime}
        sharingOptions={{
          sharing: {
            enabled: traceData.length > 0 ? false : true,
            onClick: null,
          },
          download: {
            enabled: traceData.length > 0 ? false : true,
            onClick: () => {
              return {
                tableId: 'k8s-trace-listing',
              };
            },
          },
        }}
        filterOptions={
          traceData.length > 0
            ? []
            : traceProvider != 'otel_clickhouse'
            ? [
                {
                  type: 'single-select',
                  enabled: true,
                  options: services,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedWorkload(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Service',
                  value: selectedWorkload,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options:
                    statusCodes.length > 0
                      ? statusCodes.map((v: string) => ({ label: normalizeStatusCodeLabel(v), value: v }))
                      : [
                          { label: 'Ok', value: 'STATUS_CODE_UNSET' },
                          { label: 'Error', value: 'STATUS_CODE_ERROR' },
                        ],
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedStatusCode(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '120px',
                  label: 'Ok/Error',
                  value: selectedStatusCode,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: ['200', '201', '204', '301', '302', '400', '401', '403', '404', '500', '502', '503', '504'],
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedHttpStatus(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'HTTP Status',
                  value: selectedHttpStatus,
                },
                {
                  type: 'search',
                  enabled: true,
                  onSelect: onResourceFilterChange,
                  minWidth: '150px',
                  label: 'Search By Resource',
                  onEnter: onEnterPress,
                  value: resource,
                },
                {
                  type: 'search',
                  enabled: true,
                  onSelect: onTraceFilterChange,
                  minWidth: '150px',
                  label: 'Search By Trace Id',
                  onEnter: onEnterPress,
                },
                ...(traceProvider === 'newrelic' && httpSpans.length > 0
                  ? [
                      {
                        type: 'dropdown',
                        enabled: true,
                        options: httpSpans,
                        onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                          setSelectedHttpSpan(e?.target?.value);
                          setCurrentPage(0);
                        },
                        minWidth: '150px',
                        label: 'Span Name',
                        value: selectedHttpSpan,
                      },
                    ]
                  : [
                      {
                        type: 'search',
                        enabled: true,
                        onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                          setSelectedHttpSpan(e?.target?.value);
                          setCurrentPage(0);
                        },
                        minWidth: '150px',
                        label: 'Search By Span Name',
                        onEnter: onEnterPress,
                      },
                    ]),
              ]
            : [
                {
                  type: 'multi-select',
                  enabled: showNamespaceFilter,
                  options: namespaces,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    const newValue = e?.target?.value;
                    setSelectedNamespace(newValue);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Src. Namespace',
                  value: selectedNamespace,
                },
                {
                  type: 'multi-select',
                  enabled: showWorkloadFilter,
                  options: workloads,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedWorkload(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Src. Workload',
                  value: selectedWorkload,
                  limitTags: 1,
                },
                {
                  type: 'multi-select',
                  enabled: showNamespaceFilter,
                  options: destinationNamespaces,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setDestinationSelectedNamespace(e?.target?.value);
                    setCurrentPage(0);
                  },
                  limitTags: 1,
                  minWidth: '150px',
                  label: 'Dest. Namespace',
                  value: destinationSelectedNamespace,
                },
                {
                  type: 'multi-select',
                  enabled: showWorkloadFilter,
                  options: destinationWorkloads,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setDestinationSelectedWorkload(e?.target?.value);
                    setCurrentPage(0);
                  },
                  limitTags: 1,
                  minWidth: '150px',
                  label: 'Dest. Workload',
                  value: destinationSelectedWorkload,
                },
                {
                  type: 'multi-select',
                  enabled: showStatusFilter,
                  options: httpStatusCodes,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedHttpStatus(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Status Code',
                  value: selectedHttpStatus,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: httpSpans,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedHttpSpan(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Span',
                  value: selectedHttpSpan,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options:
                    statusCodes.length > 0
                      ? statusCodes.map((v: string) => ({ label: normalizeStatusCodeLabel(v), value: v }))
                      : [
                          { label: 'Ok', value: 'STATUS_CODE_UNSET' },
                          { label: 'Error', value: 'STATUS_CODE_ERROR' },
                        ],
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedStatusCode(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Ok/Error',
                  value: selectedStatusCode,
                },
                {
                  type: 'search',
                  enabled: true,
                  onSelect: onResourceFilterChange,
                  minWidth: '180px',
                  label: 'Search By Resource',
                  onEnter: onEnterPress,
                  value: resource,
                },
                {
                  type: 'search',
                  enabled: true,
                  onSelect: onHeaderFilterChange,
                  minWidth: '180px',
                  label: 'Search By Headers',
                  onEnter: onEnterPress,
                },
                {
                  type: 'search',
                  enabled: true,
                  onSelect: onTraceFilterChange,
                  minWidth: '180px',
                  label: 'Search By Trace Id',
                  onEnter: onEnterPress,
                },
                {
                  type: 'dropdown',
                  enabled: true,
                  options: tracesSource,
                  onSelect: (e: React.ChangeEvent<HTMLInputElement>) => {
                    setSelectedTracesSource(e?.target?.value);
                    setCurrentPage(0);
                  },
                  minWidth: '150px',
                  label: 'Trace Source',
                  value: selectedTracesSource,
                },
              ]
        }
        leftExtraOptions={
          traceProvider
            ? [
                <Box
                  key={'trace-provider-info'}
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    padding: '6px 12px',
                    backgroundColor: 'rgba(0, 0, 0, 0.02)',
                    borderRadius: '6px',
                    border: '1px solid rgba(0, 0, 0, 0.08)',
                    minWidth: 'fit-content',
                  }}
                >
                  <Text
                    value='Trace Provider:'
                    sx={{
                      fontSize: '14px',
                      fontWeight: 500,
                      color: colors.text.greyDark,
                      whiteSpace: 'nowrap',
                    }}
                  />
                  <CloudProviderIcon cloud_provider={traceProvider} width='20px' height='20px' />
                  <Text
                    value={snakeToTitleCase(traceProvider)}
                    sx={{
                      fontSize: '14px',
                      fontWeight: 600,
                      color: colors.text.secondary,
                      whiteSpace: 'nowrap',
                    }}
                  />
                </Box>,
              ]
            : []
        }
        onRefresh={{
          enabled: traceData.length > 0 ? false : true,
          loading: loading,
          text: '',
          onClick: () => {
            listTraces();
          },
        }}
        onClearAll={handleClearAll}
        displaySideFilters={displaySideFilters}
        minDate={new Date(new Date().getFullYear(), new Date().getMonth(), new Date().getDate() - 14)}
      >
        <CustomTable
          id='k8s-trace-listing'
          headers={LISTING_HEADER}
          rowsPerPage={traceData.length > 0 ? traceData.length : recordsPerPage}
          tableData={data}
          onPageChange={(page: number, limit: number) => {
            setCurrentPage(page - 1);
            setRecordsPerPage(limit);
          }}
          totalRows={traceData.length > 0 ? traceData.length : totalCount}
          loading={loading}
          onSortChange={(e: any) => {
            sortEventChange(e);
          }}
          sort={traceData.length > 0 ? {} : sortObject}
          pageNumber={currentPage + 1}
          errorMessage={errorMsg}
          showExpandable={true}
          timeStampMinWidth={true}
          expandable={{
            tabs: [
              {
                key: 'trace-heatmap',
                value: 0,
                text: 'Service & Operation',
                componentFn: function (_opt: any, drilldownQuery: any) {
                  return <KubernetesTraceServiceOperation accountId={selectedK8sAccount} query={drilldownQuery} />;
                },
              },
              {
                componentFn: function (_opt: any, drilldownQuery: any) {
                  if (drilldownQuery?.span_attributes) {
                    const { headers, convertedJson2 } = getTableData4([drilldownQuery?.span_attributes || {}]);
                    return (
                      <BoxLayout2
                        sharingOptions={{
                          download: {
                            enabled: false,
                            onClick: () => {
                              return {
                                tableId: '',
                              };
                            },
                          },
                          sharing: {
                            enabled: false,
                            onClick: () => {
                              return '';
                            },
                          },
                        }}
                      >
                        <CustomTable
                          headers={headers}
                          tableData={convertedJson2}
                          rowsPerPage={convertedJson2.length}
                          totalRows={convertedJson2.length}
                        />
                      </BoxLayout2>
                    );
                  }
                  return <Typography sx={{ padding: '10px' }}>No Span Attributes Available</Typography>;
                },
                text: 'Span Attributes',
                value: 1,
                key: 'span_attributes',
              },

              {
                text: 'Logs / Query',
                value: 2,
                key: 'trace-logs',
                componentFn: function (_opt: any, drilldownQuery: any) {
                  if (drilldownQuery.span_name == 'query') {
                    return <Typography>{drilldownQuery.resource}</Typography>;
                  }
                  const query = `{"namespaceName":"${drilldownQuery.workload_namespace}","workloadName":"${drilldownQuery.workload_name}", "traceId": "${drilldownQuery.trace_id}"}`;
                  const plusMinus5Minutes = formatDateForPlusMinusDuration(new Date(drilldownQuery?.timestamp).getTime(), 5);
                  return (
                    <KubernetesLogs
                      accountId={selectedK8sAccount}
                      showTrend={false}
                      showQueryTextBox={false}
                      dateTime={{
                        startTime: plusMinus5Minutes.dateMinusMinutes,
                        endTime: plusMinus5Minutes.datePlusMinutes,
                      }}
                      queryFromProps={query}
                      showPolling={false}
                    />
                  );
                },
              },
              {
                text: 'CPU & Memory',
                value: 3,
                key: 'trace-cpu-memory',
                componentFn: function (_opt: any, drilldownQuery: any, _row: any) {
                  let src_query: any = {
                    workloadName: drilldownQuery.workload_name,
                    namespaceName: drilldownQuery.workload_namespace,
                  };
                  let dest_query: any = {
                    workloadName: drilldownQuery.destination_workload_name,
                    namespaceName: drilldownQuery.destination_workload_namespace,
                  };
                  if (drilldownQuery.workload_namespace == 'node' || drilldownQuery.workload_namespace == 'external') {
                    src_query = {
                      internalIp: extractIp(drilldownQuery.workload_name),
                    };
                  }
                  if (drilldownQuery.destination_workload_namespace == 'node' || drilldownQuery.destination_workload_namespace == 'external') {
                    dest_query = {
                      internalIp: extractIp(drilldownQuery.destination_workload_name),
                    };
                  }
                  const plusMinus10Minutes = formatDateForPlusMinusDuration(new Date(drilldownQuery?.timestamp).getTime(), 10);
                  return (
                    <>
                      <Typography>
                        Source: {drilldownQuery.workload_name} | {drilldownQuery.workload_namespace}
                      </Typography>
                      <KubernetesUtilizationCharts
                        accountId={selectedK8sAccount}
                        query={src_query}
                        selectedDateRange={{
                          startDate: plusMinus10Minutes.dateMinusMinutes,
                          endDate: plusMinus10Minutes.datePlusMinutes,
                        }}
                        memLimit={undefined}
                        cpuLimit={undefined}
                      />
                      {drilldownQuery.destination_workload_namespace != 'external' && (
                        <>
                          <Typography>
                            Destination: {drilldownQuery.destination_workload_name} | {drilldownQuery.destination_workload_namespace}
                          </Typography>
                          <KubernetesUtilizationCharts
                            accountId={selectedK8sAccount}
                            query={dest_query}
                            selectedDateRange={{
                              startDate: plusMinus10Minutes.dateMinusMinutes,
                              endDate: plusMinus10Minutes.datePlusMinutes,
                            }}
                            memLimit={undefined}
                            cpuLimit={undefined}
                          />
                        </>
                      )}
                    </>
                  );
                },
              },
              {
                componentFn: function (_opt: any, drilldownQuery: any) {
                  if (drilldownQuery?.span_name == 'query') {
                    return <>{drilldownQuery.resource || 'No Data Available'}</>;
                  }
                  const parsedHeader = drilldownQuery.headers
                    ? getHeaderObject(
                        drilldownQuery.headers,
                        selectedCluster?.agent?.connection_status?.traceProviderConfig?.hasMaterializedColumn || false
                      )
                    : '';
                  return (
                    <div>
                      {parsedHeader && (
                        <>
                          <Typography
                            sx={{
                              fontWeight: 700,
                            }}
                          >
                            Headers:
                          </Typography>
                          <Typography sx={{ maxWidth: '1300px', wordBreak: 'break-all' }}>{parsedHeader}</Typography>
                        </>
                      )}
                      {drilldownQuery?.headers && drilldownQuery?.request_payload && <div style={{ marginBottom: '10px' }} />}
                      {drilldownQuery?.request_payload && (
                        <>
                          <Typography
                            sx={{
                              fontWeight: 700,
                            }}
                          >
                            Request Payload:
                          </Typography>
                          <Typography
                            sx={{
                              display: 'flex',
                            }}
                          >
                            {atob(drilldownQuery.request_payload)}
                          </Typography>
                        </>
                      )}
                      {!drilldownQuery?.headers && !drilldownQuery?.request_payload && <Typography>No Data Available</Typography>}
                    </div>
                  );
                },
                text: 'Request',
                value: 4,
                key: 'request',
                alphaIcon: true,
              },
              {
                componentFn: function (_opt: any, drilldownQuery: any) {
                  return (
                    <div>
                      {drilldownQuery?.http_response ? (
                        <>
                          <Typography
                            sx={{
                              fontWeight: 700,
                            }}
                          >
                            Response:
                          </Typography>
                          <Typography sx={{ whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>{atob(drilldownQuery.http_response)}</Typography>
                        </>
                      ) : (
                        <Typography>No Data Available</Typography>
                      )}
                    </div>
                  );
                },
                text: 'Response',
                value: 5,
                key: 'response',
                alphaIcon: true,
              },
            ],
          }}
        />
      </BoxLayout2>
    </>
  );
};

export default KubernetesTracesListing;
