import React, { useEffect, useState } from 'react';
import ListingLayout from '@components1/ds/ListingLayout';
import FilterDropdown from '@components1/ds/FilterDropdown';
import CustomSearch from '@common-new/CustomSearch';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import { Label } from '@components1/ds/Label';
import { Button as DsButton } from '@components1/ds/Button';
import RefreshIcon from '@mui/icons-material/Refresh';
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
import DateTime from '@common-new/format/Datetime';
import { Box, Typography } from '@mui/material';
import apiTrace from '@api1/kubernetes/trace';
import k8sApi from '@api1/kubernetes';
import SafeIcon from '@components1/common/SafeIcon';
import { RightArrowIcon } from '@assets';
import { getNubiIconUrl, useTenantBranding } from '@hooks/useTenantBranding';
import Text from '@common-new/format/Text';
import KubernetesLogs from './KubernetesLogs';
import apiUser from '@api1/user';
import { applyFiltersOnRouter } from '@lib/router';
import ConversationPopup from '@components1/llm/ConversationPopup';
import CustomTable from '@common-new/tables/CustomTable2';
import WidgetCard from '@components1/ds/WidgetCard';
import { useData } from '@context/DataContext';
import { KubernetesTraceServiceOperation } from '@components1/k8s/common/KubernetesTraceServiceOperation';
import { getTableData4 } from '@components1/k8s/investigate/cards/util';
import apiAccount from '@api1/account';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { ds } from '@utils/colors';

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

const safeAtob = (input: string): string => {
  try {
    return atob(input);
  } catch {
    return input;
  }
};

const SourceDestinationView: React.FC<SourceDestinationViewProps> = ({ namespace, workloadName, destinationNamespace, destinationWorkload }) => {
  return (
    <Box sx={{ display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)', m: 'var(--ds-space-5) 0 var(--ds-space-4)' }}>
      <Box sx={{ border: '1px solid var(--ds-gray-300)', borderRadius: 'var(--ds-radius-sm)', p: 'var(--ds-space-3) var(--ds-space-4)', flex: 1 }}>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-500)' }}>
          Source:
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          {workloadName}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-500)' }}>
          ns: {namespace}
        </Typography>
      </Box>
      <Box>
        <SafeIcon src={RightArrowIcon} width={24} alt={'Right Arrow Icon'} />
      </Box>
      <Box sx={{ border: '1px solid var(--ds-gray-300)', borderRadius: 'var(--ds-radius-sm)', p: 'var(--ds-space-3) var(--ds-space-4)', flex: 1 }}>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-500)' }}>
          Destination:
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-title)', fontWeight: 'var(--ds-font-weight-medium)', color: 'var(--ds-gray-700)' }}>
          {destinationWorkload}
        </Typography>
        <Typography sx={{ fontSize: 'var(--ds-text-small)', fontWeight: 'var(--ds-font-weight-regular)', color: 'var(--ds-gray-500)' }}>
          ns: {destinationNamespace}
        </Typography>
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
  const [inputSelectedHttpSpan, setInputSelectedHttpSpan] = useState<string>('');
  const [errorMsg, setErrorMsg] = useState('');
  const [time, setTime] = useState(getInitialTime());
  const [resource, setResource] = useState(getInitialResource());
  const [inputResource, setInputResource] = useState(getInitialResource());
  const [sortObject, setSortObject] = useState<SortOrderObject>({
    name: 'Timestamp',
    order: 'desc',
  });
  const [header, setHeader] = useState('');
  const [inputHeader, setInputHeader] = useState('');
  const [selectedStatusCode, setSelectedStatusCode] = useState<string>((router?.query?.statusCode as string) || statusCode || '');
  const [resetDateTime, setResetDateTime] = useState(0);
  const [_expandedAccordions, setExpandedAccordions] = useState({});
  const [selectedTracesSource, setSelectedTracesSource] = useState<string>('');
  const [traceId, setTraceId] = useState<string>('');
  const [inputTraceId, setInputTraceId] = useState<string>('');
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
      return res?.data?.data?.observability_get_default_provider?.provider || '';
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
                      fontSize: 'var(--ds-text-small)',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: 'var(--ds-text-caption)',
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
                      fontSize: 'var(--ds-text-small)',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: 'var(--ds-text-caption)',
                    },
                  }}
                  value={`ns: ${item.workload_namespace}`}
                />
              </Box>
            ),
          },
          {
            component: <Label text={getHttpStatusText(item)} />,
          },
          {
            component: (
              <Text
                sx={{
                  '@media(max-width: 1425px)': {
                    fontSize: 'var(--ds-text-small)',
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
                    fontSize: 'var(--ds-text-small)',
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
                    fontSize: 'var(--ds-text-small)',
                  },
                  '@media(max-width: 1100px)': {
                    fontSize: 'var(--ds-text-caption)',
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
                      fontSize: 'var(--ds-text-small)',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: 'var(--ds-text-caption)',
                    },
                  }}
                  showAutoEllipsis
                  value={`name: ${item.destination_name}`}
                />
                <Text
                  secondaryText
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: 'var(--ds-text-small)',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: 'var(--ds-text-caption)',
                    },
                  }}
                  value={`ns: ${item.destination_workload_namespace}`}
                />
                <Text
                  secondaryText
                  sx={{
                    '@media(max-width: 1425px)': {
                      fontSize: 'var(--ds-text-small)',
                    },
                    '@media(max-width: 1100px)': {
                      fontSize: 'var(--ds-text-caption)',
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
                <Box sx={{ mr: 'var(--ds-space-1)' }}>
                  <DsButton
                    tone='secondary'
                    size='sm'
                    composition='icon-only'
                    aria-label={`Ask ${assistantName}`}
                    tooltip={`Ask ${assistantName}`}
                    icon={<SafeIcon src={getNubiIconUrl()} width={18} height={18} alt={`Ask ${assistantName}`} />}
                    onClick={(e) => {
                      e.stopPropagation();
                      handleGenerateQueryAnalysis(item);
                    }}
                  />
                </Box>
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
          const traces = res?.traces_list || [];
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
          traceId,
          selectedHttpSpan,
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
          resource,
          traceId,
          header,
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

  const handleClearAll = () => {
    setSelectedNamespace(getInitialNamespace());
    setSelectedWorkload(getInitialWorkload());
    setDestinationSelectedNamespace(getInitialDestinationNamespace());
    setDestinationSelectedWorkload(getInitialDestinationWorkload());
    setSelectedHttpStatus('');
    setSelectedHttpSpan('');
    setInputSelectedHttpSpan('');
    setResource('');
    setInputResource('');
    setHeader('');
    setInputHeader('');
    setTraceId('');
    setInputTraceId('');
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

  const okErrorOptions =
    statusCodes.length > 0
      ? statusCodes.map((v: string) => ({ label: normalizeStatusCodeLabel(v), value: v }))
      : [
          { label: 'Ok', value: 'STATUS_CODE_UNSET' },
          { label: 'Error', value: 'STATUS_CODE_ERROR' },
        ];

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
      <ListingLayout id='k8s-traces-box'>
        <ListingLayout.Toolbar>
          <Box sx={{ flexBasis: '100%', display: 'flex', alignItems: 'center' }}>
            {traceProvider && (
              <Box
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-2)',
                  padding: 'var(--ds-space-2) var(--ds-space-3)',
                  backgroundColor: 'var(--ds-gray-alpha-100)',
                  borderRadius: 'var(--ds-radius-md)',
                  border: '1px solid var(--ds-gray-alpha-200)',
                }}
              >
                <Text
                  value='Trace Provider:'
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-600)',
                    whiteSpace: 'nowrap',
                  }}
                />
                <CloudProviderIcon cloud_provider={traceProvider} width='20px' height='20px' />
                <Text
                  value={snakeToTitleCase(traceProvider)}
                  sx={{
                    fontSize: 'var(--ds-text-body-lg)',
                    fontWeight: 'var(--ds-font-weight-semibold)',
                    color: 'var(--ds-gray-700)',
                    whiteSpace: 'nowrap',
                  }}
                />
              </Box>
            )}
            <Box sx={{ marginLeft: 'auto', display: 'flex', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
              {showTimeFilter && (
                <CustomDateTimeRangePicker
                  key={resetDateTime}
                  passedSelectedDateTime={time}
                  onChange={(result: any) => {
                    const val = result?.selection ?? result;
                    if (val) onDateTimeRangeChange(val);
                  }}
                  minDate={new Date(new Date().getFullYear(), new Date().getMonth(), new Date().getDate() - 14) as any}
                  shortCuts={[
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
                  ]}
                />
              )}
              {traceData.length === 0 && (
                <DsButton
                  tone='secondary'
                  size='sm'
                  composition='icon-only'
                  icon={<RefreshIcon />}
                  aria-label='Refresh'
                  tooltip='Refresh'
                  onClick={listTraces}
                  loading={loading}
                />
              )}
            </Box>
          </Box>
          <Box sx={{ flexBasis: '100%', display: 'flex', flexWrap: 'wrap', alignItems: 'center', gap: 'var(--ds-space-2)' }}>
            {displaySideFilters && traceData.length === 0 && (
              <>
                {traceProvider !== 'otel_clickhouse' ? (
                  <>
                    <FilterDropdown
                      label='Service'
                      options={services}
                      value={selectedWorkload}
                      onSelect={(e: any) => {
                        setSelectedWorkload(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <FilterDropdown
                      label='Ok/Error'
                      options={okErrorOptions}
                      value={selectedStatusCode}
                      onSelect={(e: any) => {
                        setSelectedStatusCode(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <FilterDropdown
                      label='HTTP Status'
                      options={['200', '201', '204', '301', '302', '400', '401', '403', '404', '500', '502', '503', '504']}
                      value={selectedHttpStatus}
                      onSelect={(e: any) => {
                        setSelectedHttpStatus(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <CustomSearch
                      id='k8s-traces-search-resource'
                      label='Search By Resource'
                      value={inputResource}
                      onChange={(v: string) => {
                        if (resource && v.trim() === '') {
                          setResource('');
                          setCurrentPage(0);
                        }
                        setInputResource(v);
                      }}
                      onEnterPress={() => {
                        setResource(inputResource);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        setResource('');
                        setInputResource('');
                        setCurrentPage(0);
                      }}
                      minWidth={ds.space.mul(0, 75)}
                    />
                    <CustomSearch
                      id='k8s-traces-search-trace-id'
                      label='Search By Trace Id'
                      value={inputTraceId}
                      onChange={(v: string) => {
                        if (traceId && v.trim() === '') {
                          setTraceId('');
                          setCurrentPage(0);
                        }
                        setInputTraceId(v);
                      }}
                      onEnterPress={() => {
                        setTraceId(inputTraceId);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        setTraceId('');
                        setInputTraceId('');
                        setCurrentPage(0);
                      }}
                      minWidth={ds.space.mul(0, 75)}
                    />
                    {traceProvider === 'newrelic' && httpSpans.length > 0 ? (
                      <FilterDropdown
                        label='Span Name'
                        options={httpSpans}
                        value={selectedHttpSpan}
                        onSelect={(e: any) => {
                          setSelectedHttpSpan(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        size='sm'
                      />
                    ) : (
                      <CustomSearch
                        id='k8s-traces-search-span-name'
                        label='Search By Span Name'
                        value={inputSelectedHttpSpan}
                        onChange={(v: string) => {
                          if (selectedHttpSpan && v.trim() === '') {
                            setSelectedHttpSpan('');
                            setCurrentPage(0);
                          }
                          setInputSelectedHttpSpan(v);
                        }}
                        onEnterPress={() => {
                          setSelectedHttpSpan(inputSelectedHttpSpan);
                          setCurrentPage(0);
                        }}
                        onClear={() => {
                          setInputSelectedHttpSpan('');
                          setSelectedHttpSpan('');
                          setCurrentPage(0);
                        }}
                        minWidth={ds.space.mul(0, 75)}
                      />
                    )}
                  </>
                ) : (
                  <>
                    {showNamespaceFilter && (
                      <FilterDropdown
                        label='Src. Namespace'
                        multiple
                        options={namespaces}
                        value={selectedNamespace}
                        onSelect={(e: any) => {
                          setSelectedNamespace(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        size='sm'
                      />
                    )}
                    {showWorkloadFilter && (
                      <FilterDropdown
                        label='Src. Workload'
                        multiple
                        options={workloads}
                        value={selectedWorkload}
                        onSelect={(e: any) => {
                          setSelectedWorkload(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        limitTag={1}
                        size='sm'
                      />
                    )}
                    {showNamespaceFilter && (
                      <FilterDropdown
                        label='Dest. Namespace'
                        multiple
                        options={destinationNamespaces}
                        value={destinationSelectedNamespace}
                        onSelect={(e: any) => {
                          setDestinationSelectedNamespace(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        limitTag={1}
                        size='sm'
                      />
                    )}
                    {showWorkloadFilter && (
                      <FilterDropdown
                        label='Dest. Workload'
                        multiple
                        options={destinationWorkloads}
                        value={destinationSelectedWorkload}
                        onSelect={(e: any) => {
                          setDestinationSelectedWorkload(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        limitTag={1}
                        size='sm'
                      />
                    )}
                    {showStatusFilter && (
                      <FilterDropdown
                        label='Status Code'
                        multiple
                        options={httpStatusCodes}
                        value={selectedHttpStatus}
                        onSelect={(e: any) => {
                          setSelectedHttpStatus(e?.target?.value);
                          setCurrentPage(0);
                        }}
                        size='sm'
                      />
                    )}
                    <FilterDropdown
                      label='Span'
                      options={httpSpans}
                      value={selectedHttpSpan}
                      onSelect={(e: any) => {
                        setSelectedHttpSpan(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <FilterDropdown
                      label='Ok/Error'
                      options={okErrorOptions}
                      value={selectedStatusCode}
                      onSelect={(e: any) => {
                        setSelectedStatusCode(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <FilterDropdown
                      label='Trace Source'
                      options={tracesSource}
                      value={selectedTracesSource}
                      onSelect={(e: any) => {
                        setSelectedTracesSource(e?.target?.value);
                        setCurrentPage(0);
                      }}
                      size='sm'
                    />
                    <CustomSearch
                      id='k8s-traces-search-resource'
                      label='Search By Resource'
                      value={inputResource}
                      onChange={(v: string) => {
                        if (resource && v.trim() === '') {
                          setResource('');
                          setCurrentPage(0);
                        }
                        setInputResource(v);
                      }}
                      onEnterPress={() => {
                        setResource(inputResource);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        setResource('');
                        setInputResource('');
                        setCurrentPage(0);
                      }}
                      minWidth={ds.space.mul(0, 90)}
                    />
                    <CustomSearch
                      id='k8s-traces-search-headers'
                      label='Search By Headers'
                      value={inputHeader}
                      onChange={(v: string) => {
                        if (header && v.trim() === '') {
                          setHeader('');
                          setCurrentPage(0);
                        }
                        setInputHeader(v);
                      }}
                      onEnterPress={() => {
                        setHeader(inputHeader);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        setInputHeader('');
                        setHeader('');
                        setCurrentPage(0);
                      }}
                      minWidth={ds.space.mul(0, 90)}
                    />
                    <CustomSearch
                      id='k8s-traces-search-trace-id'
                      label='Search By Trace Id'
                      value={inputTraceId}
                      onChange={(v: string) => {
                        if (traceId && v.trim() === '') {
                          setTraceId('');
                          setCurrentPage(0);
                        }
                        setInputTraceId(v);
                      }}
                      onEnterPress={() => {
                        setTraceId(inputTraceId);
                        setCurrentPage(0);
                      }}
                      onClear={() => {
                        setInputTraceId('');
                        setTraceId('');
                        setCurrentPage(0);
                      }}
                      minWidth={ds.space.mul(0, 90)}
                    />
                  </>
                )}
                <DsButton tone='secondary' size='sm' onClick={handleClearAll}>
                  Clear All
                </DsButton>
              </>
            )}
          </Box>
        </ListingLayout.Toolbar>
        <ListingLayout.Body>
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
                        <WidgetCard>
                          <CustomTable
                            headers={headers}
                            tableData={convertedJson2}
                            rowsPerPage={convertedJson2.length}
                            totalRows={convertedJson2.length}
                          />
                        </WidgetCard>
                      );
                    }
                    return (
                      <WidgetCard>
                        <Typography>No Span Attributes Available</Typography>
                      </WidgetCard>
                    );
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
                      return (
                        <WidgetCard>
                          <Typography
                            sx={{
                              fontFamily: 'monospace',
                              fontSize: 'var(--ds-text-body)',
                              lineHeight: '1.6',
                              wordBreak: 'break-word',
                              overflowWrap: 'anywhere',
                            }}
                          >
                            {drilldownQuery.resource}
                          </Typography>
                        </WidgetCard>
                      );
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
                      <WidgetCard>
                        <Typography sx={{ fontWeight: 'bold', mb: 'var(--ds-space-2)' }}>
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
                            <Typography sx={{ fontWeight: 'bold', mt: 'var(--ds-space-4)', mb: 'var(--ds-space-2)' }}>
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
                      </WidgetCard>
                    );
                  },
                },
                {
                  componentFn: function (_opt: any, drilldownQuery: any) {
                    if (drilldownQuery?.span_name == 'query') {
                      return (
                        <WidgetCard>
                          <Typography
                            sx={{
                              fontFamily: 'monospace',
                              fontSize: 'var(--ds-text-body)',
                              lineHeight: '1.6',
                              wordBreak: 'break-word',
                              overflowWrap: 'anywhere',
                            }}
                          >
                            {drilldownQuery.resource || 'No Data Available'}
                          </Typography>
                        </WidgetCard>
                      );
                    }
                    const parsedHeader = drilldownQuery.headers
                      ? getHeaderObject(
                          drilldownQuery.headers,
                          selectedCluster?.agent?.connection_status?.traceProviderConfig?.hasMaterializedColumn || false
                        )
                      : '';
                    return (
                      <WidgetCard>
                        {parsedHeader && (
                          <>
                            <Typography sx={{ fontWeight: 'bold', mb: 'var(--ds-space-1)' }}>Headers:</Typography>
                            <Typography
                              sx={{
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-body)',
                                lineHeight: '1.6',
                                wordBreak: 'break-word',
                                overflowWrap: 'anywhere',
                              }}
                            >
                              {parsedHeader}
                            </Typography>
                          </>
                        )}
                        {drilldownQuery?.request_payload && (
                          <>
                            <Typography
                              sx={{
                                fontWeight: 'bold',
                                mt: parsedHeader ? 'var(--ds-space-3)' : 0,
                                mb: 'var(--ds-space-1)',
                              }}
                            >
                              Request Payload:
                            </Typography>
                            <Typography
                              sx={{
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-body)',
                                lineHeight: '1.6',
                                wordBreak: 'break-word',
                                overflowWrap: 'anywhere',
                              }}
                            >
                              {safeAtob(drilldownQuery.request_payload)}
                            </Typography>
                          </>
                        )}
                        {!drilldownQuery?.headers && !drilldownQuery?.request_payload && <Typography>No Data Available</Typography>}
                      </WidgetCard>
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
                      <WidgetCard>
                        {drilldownQuery?.http_response ? (
                          <>
                            <Typography sx={{ fontWeight: 'bold', mb: 'var(--ds-space-1)' }}>Response:</Typography>
                            <Typography
                              sx={{
                                fontFamily: 'monospace',
                                fontSize: 'var(--ds-text-body)',
                                lineHeight: '1.6',
                                whiteSpace: 'pre-wrap',
                                wordBreak: 'break-word',
                                overflowWrap: 'anywhere',
                              }}
                            >
                              {safeAtob(drilldownQuery.http_response)}
                            </Typography>
                          </>
                        ) : (
                          <Typography>No Data Available</Typography>
                        )}
                      </WidgetCard>
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
        </ListingLayout.Body>
      </ListingLayout>
    </>
  );
};

export default KubernetesTracesListing;
