import React, { useEffect, useState } from 'react';
import KubernetesTable2 from '@components1/k8s/common/KubernetesTable2';
import { Box, Typography } from '@mui/material';
import { ToggleGroup } from '@components1/ds/ToggleGroup';
import { Divider } from '@components1/ds/Divider';
import { ListingLayout } from '@components1/ds/ListingLayout';
import Tooltip from '@components1/ds/Tooltip';
import { Toggle } from '@components1/ds/Toggle';
import { Checkbox } from '@components1/ds/Checkbox';
import CustomDateTimeRangePicker from '@common-new/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@common-new/DownloadButton';
import {
  convertNumberToTimestamp,
  isAtMost70PercentDifferent,
  parseHttpResponseBodyMessage,
  safeJSONParse,
  snakeToTitleCase,
} from 'src/utils/common';
import { useRouter } from 'next/router';
import LineChart from '@common/charts/LineCharts';
import Text from '@common-new/format/Text';
import UserHistoryButton from '@components1/common/UserHistory';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { v4 as uuidv4 } from 'uuid';
import { Skeleton } from '@components1/ds/Skeleton';
import { snackbar } from '@components1/ds/Toast';
import QueryModeSwitcher from '@components1/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components1/k8s/common/operatorCatalog';
import CloudProviderIcon from '@components1/common/CloudIcon';
import { Button } from '@components1/ds/Button';
import observability from '@api1/observability';
import cache from '@lib/cache';
import apiAccount from '@api1/account';
import { useData } from '@context/DataContext';
import { Info as InfoIcon } from '@mui/icons-material';
import { Label } from '@components1/ds/Label';

// Data limiting constants to prevent memory issues with large datasets
const MAX_TABLE_ROWS = 100;
const MAX_CHART_DATASETS = 20;
const MAX_CHART_DATA_POINTS = 500;

interface QueryMetricsProps {
  accountId: string;
  showDrilldown: boolean;
  chartView: boolean;
  showExtraOptions: boolean;
  showQueryBox: boolean;
  preparedEvidences?: any[];
  showDateTime?: boolean;
  queriesToExecute?: Array<{ key: string; query: string; title?: string }>;
  dateTime?: {
    startTime: number;
    endTime: number;
  };
}

interface Header {
  name: string;
  width: string;
  component?: any;
}

const QueryMetrics: React.FC<QueryMetricsProps> = ({
  accountId,
  showDrilldown = true,
  chartView = true,
  showExtraOptions = true,
  showQueryBox = true,
  showDateTime = true,
  queriesToExecute = [],
  dateTime = {
    startTime: 0,
    endTime: 0,
  },
  preparedEvidences = [],
}) => {
  const router = useRouter();
  const k8sProm = 'k8sProm';
  const startDate = new Date(new Date().getTime() - 60 * 60 * 1000);
  const { selectedCluster } = useData();

  const [data, setData] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [chartData, setChartData] = useState<any>([]);
  const [selectedDateRange, setSelectedDateRange] = useState<any>({
    startDate: dateTime.startTime > 0 ? dateTime.startTime : startDate.getTime(),
    endDate: dateTime.endTime > 0 ? dateTime.endTime : new Date().getTime(),
  });
  const [showChartView, setShowChartView] = useState<boolean>(chartView);
  const [conversationId, setConversationId] = useState('');
  const [query, setQuery] = useState('');
  const [queryKeys, setQueryKeys] = useState(['']);
  const [solarwindsRequest, setSolarwindsRequest] = useState<any>(null);
  const [esIndex, setEsIndex] = useState<string>('');
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const [instant, setInstant] = useState(false);
  const [promqlItems, setPromqlItems] = useState<Array<{ key: string; query: string; title?: string }>>([]);
  const [generateQuestionText, _setGenerateQuestionText] = useState('');
  const [qLEditor, setQLEditor] = useState('code');
  const [metricsProvider, setMetricsProvider] = useState('prometheus');
  const [operatorDescriptors, setOperatorDescriptors] = useState<OperatorDescriptor[] | undefined>(undefined);
  const [isAiLoading, setIsAiLoading] = useState(false);
  const [truncationWarnings, setTruncationWarnings] = useState<{
    tableRows?: { total: number; shown: number };
    chartDatasets?: { total: number; shown: number };
    chartDataPoints?: { total: number; shown: number };
  }>({});

  const deleteDataOnQueryBlockDeletion = (query_key: string) => {
    setData((prevData) => prevData.filter((item) => item.query_key !== query_key));
  };

  const fetchDefaultProvider = async () => {
    const cached = cache.get(`${accountId}-metrics-v3`);
    if (cached && typeof cached === 'object' && cached.provider) {
      setOperatorDescriptors(cached.operator_descriptors);
      setEsIndex(cache.get(`${accountId}-metrics-index`) || '');
      return cached.provider;
    }
    try {
      const res = await apiAccount.getDefaultProvider({
        account_id: accountId,
        provider_type: 'metrics',
      });

      if (res?.data?.errors) {
        snackbar.error(parseHttpResponseBodyMessage(res?.data));
        return '';
      }

      const defaultIndex = res?.data?.data?.observability_get_default_provider?.default_index || '';
      const descriptors = res?.data?.data?.observability_get_default_provider?.capabilities?.supported_operator_descriptors;
      setOperatorDescriptors(descriptors);
      setEsIndex(defaultIndex);
      if (defaultIndex) {
        cache.set(`${accountId}-metrics-index`, defaultIndex, 5 * 60);
      }
      const provider = res?.data?.data?.observability_get_default_provider?.provider || '';
      cache.set(`${accountId}-metrics-v3`, { provider, operator_descriptors: descriptors }, 5 * 60);
      return provider;
    } catch (error: any) {
      snackbar.error(error.message || 'Failed to fetch default provider');
      return '';
    }
  };

  const resetAllStates = () => {
    setData([]);
    setChartData([]);
    setQuery('');
    setQueryKeys(['']);
    setLlmQueryResponse('');
    setShowChartView(false);
    setPromqlItems([]);
    setIsAiLoading(false);
    setLoading(false);
    setTruncationWarnings({});
  };

  // Execute queries from URL parameters when router is ready and metrics provider is prometheus
  // Handles both single and multiple queries (separated by semicolons)
  useEffect(() => {
    if (!router.isReady) {
      return;
    }

    if (metricsProvider === 'prometheus') {
      const queryFromUrl = router.query.query as string;
      if (queryFromUrl) {
        // Split multiple queries by semicolon and filter out empty strings
        const queryBlocks = queryFromUrl.split(';').filter((q) => q.trim());
        // Generate unique keys for each query block to avoid state conflicts
        const newQueryKeys = queryBlocks.map(() => uuidv4());
        setQuery(queryFromUrl);
        setQueryKeys(newQueryKeys);
        // Execute the queries with the newly generated keys
        handleSubmit(queryFromUrl, newQueryKeys, true);
      }
    }
  }, [router.isReady, router.query?.query, metricsProvider]);

  useEffect(() => {
    const init = async () => {
      if (accountId === 'demo') {
        setMetricsProvider('prometheus');
      } else {
        const defaultProvider = await fetchDefaultProvider();
        let metricsProvider = '';
        if (defaultProvider) {
          metricsProvider = defaultProvider;
        } else {
          metricsProvider = selectedCluster?.agent?.connection_status?.prometheusUrl?.includes('chronosphere')
            ? 'chronosphere'
            : selectedCluster?.agent?.connection_status?.prometheusUrl?.includes('prometheus')
            ? 'prometheus'
            : selectedCluster?.agent?.connection_status?.prometheusUrl?.includes('victoria-metrics')
            ? 'victoria-metrics'
            : '';
        }
        setMetricsProvider(metricsProvider);
      }
      const queryFromUrl = router.query.query as string;
      if ((!preparedEvidences || preparedEvidences.length === 0) && !queryFromUrl) {
        resetAllStates();
      }
    };

    init();
  }, [accountId]);

  useEffect(() => {
    if (['dynatrace', 'solarwinds'].includes(metricsProvider) && qLEditor === 'ai') {
      setQLEditor('code');
    }
  }, [metricsProvider]);

  useEffect(() => {
    if (router?.query?.startDate && router?.query?.endDate) {
      setSelectedDateRange({
        startDate: router?.query?.startDate,
        endDate: router?.query?.endDate,
      });
    }
  }, [router.query.startDate, router.query.endDate]);

  useEffect(() => {
    if (selectedDateRange.startDate && selectedDateRange.endDate) {
      handleSubmit(query, queryKeys, false, llmQueryResponse, '', queriesToExecute);
    }
  }, [selectedDateRange.startDate, selectedDateRange.endDate, instant]);

  const getObjectWithMaxKeys = (data: any) => {
    const metricsObjects = data?.filter((obj: any) => 'metric' in obj).map((j: any) => j.metric);
    const objectWithMaxKeys = metricsObjects.reduce((maxObj: any, currentObj: any) => {
      const maxObjKeys = Object.keys(maxObj).length;
      const currentObjKeys = Object.keys(currentObj).length;

      if (currentObjKeys > maxObjKeys) {
        return currentObj;
      }
      return maxObj;
    }, {});
    return objectWithMaxKeys;
  };

  const aiCreateFeedback = async (createFeedback: boolean, promqlQuery: string, llmQueryResponse: string) => {
    if ((llmQueryResponse != promqlQuery && isAtMost70PercentDifferent(llmQueryResponse, promqlQuery)) || createFeedback) {
      await apiAskNudgebee.createAiFeedback({
        session_id: uuidv4(),
        module: 'prometheus',
        question: generateQuestionText,
        llm_response: llmQueryResponse,
        user_corrected_response: promqlQuery,
        additional_notes: 'User did correction to the response',
        conversation_id: conversationId,
        cloud_account_id: accountId,
        useful: true,
      });
    }
  };

  useEffect(() => {
    if (preparedEvidences && preparedEvidences.length) {
      setLoading(true);
      setData([]);
      setChartData([]);
      const getQueryByKey = (key: string) => {
        const promqls: any = {};
        const entry: any = promqls[key];
        return entry
          ? {
              query: entry,
              title: '',
              query_key: key,
            }
          : {
              query: '',
              title: '',
              query_key: key,
            };
      };
      const { tableData, graphData, truncationInfo } = processEvidenceDataKeys(preparedEvidences, getQueryByKey);
      setData(tableData);
      setChartData(graphData);
      setTruncationWarnings(truncationInfo);
      setLoading(false);
    }
  }, [preparedEvidences]);

  /**
   * Decimates an array to a maximum number of points, preserving peaks
   * Uses a bucket-based approach to maintain visual accuracy
   */
  const decimateData = (data: number[], maxPoints: number): number[] => {
    if (data.length <= maxPoints) {
      return data;
    }

    const step = data.length / maxPoints;
    const decimated: number[] = [];

    for (let i = 0; i < maxPoints; i++) {
      const startIdx = Math.floor(i * step);
      const endIdx = Math.floor((i + 1) * step);

      // Take the max value in each bucket to preserve peaks
      let maxVal = data[startIdx];
      for (let j = startIdx; j < endIdx && j < data.length; j++) {
        if (data[j] > maxVal) {
          maxVal = data[j];
        }
      }
      decimated.push(maxVal);
    }

    return decimated;
  };

  /**
   * Decimates labels to match decimated data
   */
  const decimateLabels = <T,>(labels: T[], maxPoints: number): T[] => {
    if (labels.length <= maxPoints) {
      return labels;
    }
    const step = labels.length / maxPoints;
    return Array.from({ length: maxPoints }, (_, i) => labels[Math.floor(i * step)]);
  };

  const processEvidenceDataKeys = (
    evidenceData: any,
    getQueryByKey: (key: string) => {
      query: string;
      title: string;
      query_key: string;
    } | null
  ): {
    tableData: any[];
    graphData: any[];
    truncationInfo: {
      tableRows?: { total: number; shown: number };
      chartDatasets?: { total: number; shown: number };
      chartDataPoints?: { total: number; shown: number };
    };
  } => {
    const tableData: any = [];
    const graphData: any = [];

    // Track truncation info
    let maxTotalRows = 0;
    let maxTotalDatasets = 0;
    let maxTotalDataPoints = 0;
    let tableWasTruncated = false;
    let datasetsWereTruncated = false;
    let pointsWereDecimated = false;

    evidenceData.forEach((g: any) => {
      if (g?.payload?.length > 0) {
        const maxKeysObject = getObjectWithMaxKeys(g.payload);
        const metricKeys: string[] = Object.keys(maxKeysObject);
        let fromMetric = true;
        let headers: any[] = [];

        if (metricKeys && metricKeys.length > 0) {
          const headersWithWidth: Header[] = metricKeys.map((key) => {
            let width = '';
            if (key === 'Count') {
              width = '10%';
            }
            return { name: key, width: width, component: <Text value={key} /> };
          });
          headers = [...headersWithWidth, { name: '', width: '' }];
          metricKeys.push('Count');
        } else if (g.payload[0].timestamps.length > 0) {
          fromMetric = false;
          headers = [
            { name: 'timestamps', width: '', component: <Text value='timestamp' /> },
            { name: 'values', width: '', component: <Text value='values' /> },
          ];
        }

        const labels = [...new Set(g.payload?.flatMap((e: any) => e.timestamps) ?? [])];
        labels.sort();

        // Track original counts for truncation warning
        maxTotalDatasets = Math.max(maxTotalDatasets, g.payload.length);
        maxTotalDataPoints = Math.max(maxTotalDataPoints, labels.length);

        // Limit number of datasets for charts
        const limitedPayload = g.payload.slice(0, MAX_CHART_DATASETS);
        datasetsWereTruncated = datasetsWereTruncated || g.payload.length > MAX_CHART_DATASETS;

        // Check if decimation is needed
        const needsDecimation = labels.length > MAX_CHART_DATA_POINTS;
        pointsWereDecimated = pointsWereDecimated || needsDecimation;
        const decimatedLabels = needsDecimation ? decimateLabels(labels, MAX_CHART_DATA_POINTS) : labels;

        const chartDataDataset: any[] = [];

        // Process limited payload for charts
        limitedPayload?.forEach((item: any) => {
          // Create a Map for O(1) lookups instead of O(N) indexOf calls
          const timestampToValue = new Map<any, number>();
          item.timestamps?.forEach((ts: any, idx: number) => {
            timestampToValue.set(ts, parseFloat(item.values[idx]));
          });

          const values: any[] = [];
          labels.forEach((label) => {
            const value = timestampToValue.get(label);
            values.push(value !== undefined ? value : 0);
          });

          // Decimate values for chart
          const chartValues = needsDecimation ? decimateData(values, MAX_CHART_DATA_POINTS) : values;

          chartDataDataset.push({
            label: Object.entries(item.metric)
              .map(([key, value]) => `${key}=${value}`)
              .join('\n'),
            data: chartValues,
          });
        });

        // Process all payload for table (will be limited later)
        const groupData = fromMetric
          ? g.payload?.map((item: any) => {
              const metricData = metricKeys.map((h, i) => {
                if (item.metric[h]) {
                  if (i == 0) {
                    return {
                      text: <Text value={item.metric[h]} showAutoEllipsis />,
                      drilldownQuery: item,
                    };
                  }
                  return {
                    text: <Text value={item.metric[h]} showAutoEllipsis />,
                  };
                } else if (h == 'Count') {
                  return {
                    text: item?.values ? (
                      <Text value={item?.values?.reduce((accumulator: number, currentValue: string) => accumulator + parseFloat(currentValue), 0)} />
                    ) : (
                      '-'
                    ),
                  };
                }
                return {
                  text: '-',
                };
              });
              return metricData;
            })
          : g.payload[0]?.timestamps?.map((item: any, indx: any) => {
              return [
                {
                  text: new Date(item * 1000).toString(),
                },
                {
                  text: g.payload[0]?.values[indx] || '-',
                },
              ];
            });

        // Track and limit table rows
        const originalRowCount = groupData?.length || 0;
        maxTotalRows = Math.max(maxTotalRows, originalRowCount);
        const limitedGroupData = groupData?.slice(0, MAX_TABLE_ROWS);
        tableWasTruncated = tableWasTruncated || originalRowCount > MAX_TABLE_ROWS;

        tableData.push({
          ...getQueryByKey(g.query_key),
          data: limitedGroupData,
          headers: headers,
          totalRows: originalRowCount,
        });

        graphData.push({
          ...getQueryByKey(g.query_key),
          data: {
            labels: decimatedLabels.map((e: any) => convertNumberToTimestamp(e * 1000)),
            data: fromMetric
              ? chartDataDataset
              : [{ label: 'Value', data: decimateData(g.payload[0]?.values?.map((e: string) => parseFloat(e)) || [], MAX_CHART_DATA_POINTS) }],
          },
        });
      } else if (evidenceData[g]) {
        const dataItems = evidenceData[g];

        if (Array.isArray(dataItems) && dataItems[0]?.value && Array.isArray(dataItems[0].value)) {
          // Collect all unique metric keys from the array
          const metricKeys = Array.from(new Set(dataItems.flatMap((item) => (item.metric ? Object.keys(item.metric) : []))));

          // Define dynamic headers
          const headers = [
            { name: 'Timestamp', width: '', component: <Text value='Timestamp' /> },
            ...metricKeys.map((key) => ({
              name: key,
              width: '',
              component: <Text value={key} />,
            })),
            { name: 'Value', width: '', component: <Text value='Value' /> },
          ];

          // Build table data
          const data = dataItems.map((item) => {
            const timestamp = new Date(item.value[0] * 1000).toLocaleString();
            const value = item.value[1];

            const row = [
              { text: timestamp, drilldownQuery: item },
              ...metricKeys.map((key) => ({
                text: item.metric?.[key] ?? '-',
              })),
              { text: value },
            ];

            return row;
          });

          tableData.push({
            ...getQueryByKey(g.query_key),
            data,
            headers,
          });

          // For graph: plot total value over time
          graphData.push({
            ...getQueryByKey(g.query_key),
            data: {
              labels: dataItems.map((item) => convertNumberToTimestamp(item.value[0] * 1000)),
              data: [
                {
                  label: 'Total',
                  data: dataItems.map((item) => parseFloat(item.value[1])),
                },
              ],
            },
          });
        } else {
          // fallback for string_result or unknown structure
          tableData.push({
            ...getQueryByKey(g.query_key),
            data: [],
            headers: [],
          });

          graphData.push({
            ...getQueryByKey(g.query_key),
            data: {
              labels: [],
              data: [],
            },
          });
        }
      } else {
        tableData.push({
          ...getQueryByKey(g.query_key),
          data: [],
          headers: [],
        });
        graphData.push({
          ...getQueryByKey(g.query_key),
          data: {
            labels: [],
            data: [],
          },
        });
      }
    });

    return {
      tableData,
      graphData,
      truncationInfo: {
        tableRows: tableWasTruncated ? { total: maxTotalRows, shown: MAX_TABLE_ROWS } : undefined,
        chartDatasets: datasetsWereTruncated ? { total: maxTotalDatasets, shown: MAX_CHART_DATASETS } : undefined,
        chartDataPoints: pointsWereDecimated ? { total: maxTotalDataPoints, shown: MAX_CHART_DATA_POINTS } : undefined,
      },
    };
  };

  const createUserHistory = async (query: string, status: string, duration: number) => {
    await observability.createUserHistory({
      account_id: accountId,
      data: query,
      duration: duration,
      module: `metrics_query_${metricsProvider}`,
      status: status,
    });
  };

  const handleSubmit = (
    query = '',
    queryKeys = [''],
    fromOnSubmit = false,
    llmQueryResponse = '',
    type = '',
    queriesToExecute: Array<{ key: string; query: string; title?: string }> = []
  ) => {
    if (!query && (!queriesToExecute || queriesToExecute.length === 0)) {
      if (fromOnSubmit) {
        snackbar.error('Please enter a query before submitting');
      }
      return;
    }
    setLoading(true);
    setData([]);
    setChartData([]);
    setTruncationWarnings({});

    const now = new Date().getTime();
    const queryBlocks = query
      .replace(/^;+|;+$/g, '')
      .split(';')
      .map((q) => q.trim());

    const newQueryKeys = queryBlocks.map((_, index) => queryKeys[index] || uuidv4());

    let promqls = queryBlocks.reduce<Record<string, string>>((acc, g, index) => {
      acc[newQueryKeys[index]] = g;
      return acc;
    }, {});

    if (queriesToExecute.length) {
      promqls = queriesToExecute.reduce<Record<string, string>>((acc, item) => {
        acc[item.key || uuidv4()] = item.query.trim();
        return acc;
      }, {});
    }

    setQuery(query);
    setQueryKeys(newQueryKeys);
    setLlmQueryResponse(llmQueryResponse);

    const getQueryByKey = (key: string) => {
      const entry: any = promqls[key];
      return entry
        ? {
            query: entry,
            title: '',
            query_key: key,
          }
        : {
            query: '',
            title: '',
            query_key: key,
          };
    };

    const requestBody = {
      account_id: accountId,
      queries: promqls,
      start_time: selectedDateRange.startDate,
      end_time: selectedDateRange.endDate,
      instant: instant,
      ...(metricsProvider !== null && metricsProvider !== undefined ? { metric_provider: metricsProvider } : {}),
      ...(metricsProvider === 'solarwinds' && solarwindsRequest ? { request: solarwindsRequest } : {}),
      ...(metricsProvider === 'ES' && esIndex ? { request: { metric_name: esIndex, ...(qLEditor === 'code' ? { query_type: 'dsl' } : {}) } } : {}),
    };

    observability
      .metricsQuery(requestBody)
      .then((res) => {
        const results = res?.data?.data?.metrics_list?.results || [];

        const resultsWithErrors = results.filter((result: any) => result.error);

        if (resultsWithErrors.length > 0) {
          setData([]);
          setChartData([]);

          const errorMessages = resultsWithErrors.map((result: any) => result.error).filter(Boolean);
          const displayMessage =
            errorMessages.length > 0 ? errorMessages.join('\n') : 'Invalid query: No data returned. Please check your query syntax.';
          snackbar.error(displayMessage);
          fromOnSubmit && createUserHistory(query, 'FAILED', new Date().getTime() - now);
        } else if (results?.length) {
          const { tableData, graphData, truncationInfo } = processEvidenceDataKeys(results, getQueryByKey);
          setData(tableData);
          setChartData(graphData);
          setTruncationWarnings(truncationInfo);
          fromOnSubmit && createUserHistory(query, 'SUCCESS', new Date().getTime() - now);
        } else if (res?.data?.errors?.length) {
          setData([]);
          setChartData([]);
          snackbar.error(`failed to query metrics ${parseHttpResponseBodyMessage(res?.data)}`);
          fromOnSubmit && createUserHistory(query, 'FAILED', new Date().getTime() - now);
        } else {
          setData([]);
          setChartData([]);
          fromOnSubmit && createUserHistory(query, 'SUCCESS', new Date().getTime() - now);
        }
        if (type == 'ai') {
          aiCreateFeedback(true, query, llmQueryResponse);
        }
      })
      .catch(() => {
        snackbar.error('Failed to fetch the Data');
        fromOnSubmit && createUserHistory(query, 'FAILED', new Date().getTime() - now);
      })
      .finally(() => {
        setLoading(false);
      });
  };

  useEffect(() => {
    if (llmQueryResponse) {
      const metricsData = safeJSONParse(llmQueryResponse);
      if (metricsData?.length > 0) {
        const promqls: any = {};
        const getQueryByKey = (key: string) => {
          const entry: any = promqls[key];
          return entry
            ? {
                query: entry,
                title: '',
                query_key: key,
              }
            : {
                query: '',
                title: '',
                query_key: key,
              };
        };
        const { tableData, graphData, truncationInfo } = processEvidenceDataKeys([{ payload: metricsData }], getQueryByKey);
        setData(tableData);
        setChartData(graphData);
        setTruncationWarnings(truncationInfo);
      }
    }
  }, [llmQueryResponse]);

  const handleDateRangeChange = (passedSelectedDateTime: any) => {
    setSelectedDateRange({
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
    });
  };

  // Truncation warning component for large datasets
  const TruncationWarning = () => {
    const warnings: string[] = [];

    if (truncationWarnings.tableRows) {
      warnings.push(`Showing ${truncationWarnings.tableRows.shown} of ${truncationWarnings.tableRows.total} rows`);
    }
    if (truncationWarnings.chartDatasets) {
      warnings.push(`Showing ${truncationWarnings.chartDatasets.shown} of ${truncationWarnings.chartDatasets.total} time series`);
    }
    if (truncationWarnings.chartDataPoints) {
      warnings.push(`Decimated from ${truncationWarnings.chartDataPoints.total} to ${truncationWarnings.chartDataPoints.shown} data points`);
    }

    if (warnings.length === 0) {
      return null;
    }

    return (
      <Box
        sx={{
          padding: 'var(--ds-space-2) var(--ds-space-4)',
          backgroundColor: 'var(--ds-amber-100)',
          border: '1px solid var(--ds-amber-200)',
          borderRadius: 'var(--ds-radius-sm)',
          marginTop: 'var(--ds-space-4)',
          marginBottom: 'var(--ds-space-2)',
          display: 'flex',
          alignItems: 'center',
          gap: 'var(--ds-space-2)',
        }}
      >
        <InfoIcon sx={{ color: 'var(--ds-amber-700)', fontSize: 'var(--ds-text-title)' }} />
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-amber-700)' }}>
          <strong>Large dataset:</strong> {warnings.join('. ')}. Use more specific queries for complete data.
        </Typography>
      </Box>
    );
  };

  return (
    <div>
      <ListingLayout id='query-logs' sx={{ mb: 'var(--ds-space-2)' }}>
        <ListingLayout.Toolbar
          actions={
            <>
              {showExtraOptions && (
                <>
                  {(metricsProvider === 'prometheus' ||
                    metricsProvider === 'chronosphere' ||
                    metricsProvider === 'datadog' ||
                    metricsProvider === 'newrelic' ||
                    metricsProvider === 'dynatrace' ||
                    metricsProvider === 'solarwinds' ||
                    metricsProvider === 'victoria-metrics' ||
                    metricsProvider === 'splunk_observability_platform' ||
                    metricsProvider === 'ES') && (
                    <>
                      <Button
                        loading={loading}
                        size='sm'
                        onClick={() => handleSubmit(query, queryKeys, true)}
                        disabled={loading || isAiLoading || (qLEditor === 'ai' && !query)}
                      >
                        Submit
                      </Button>
                      {(metricsProvider === 'prometheus' || metricsProvider === 'chronosphere' || metricsProvider === 'victoria-metrics') && (
                        <Tooltip
                          variant='explainer'
                          placement='top'
                          title='Instant query'
                          desc={
                            <>
                              When enabled, runs as a Prometheus <strong>instant query</strong> (<code>/api/v1/query</code>) — evaluates the
                              expression at a single point in time instead of a range. Returns one sample per series at the end of the selected
                              window.
                            </>
                          }
                          arrow
                          tooltipStyle={{ maxWidth: '380px', maxHeight: 'unset', overflowY: 'visible' }}
                        >
                          <Box
                            component='span'
                            sx={{
                              display: 'inline-flex',
                              alignItems: 'center',
                              px: 'var(--ds-space-3)',
                              height: '30px',
                              border: '1px solid var(--ds-gray-300)',
                              borderRadius: 'var(--ds-radius-sm)',
                              backgroundColor: instant ? 'var(--ds-blue-50)' : 'transparent',
                              borderColor: instant ? 'var(--ds-blue-400)' : 'var(--ds-gray-300)',
                              cursor: 'pointer',
                              transition:
                                'border-color var(--ds-motion-micro) var(--ds-motion-ease), background-color var(--ds-motion-micro) var(--ds-motion-ease)',
                              '&:hover': {
                                borderColor: 'var(--ds-blue-400)',
                              },
                            }}
                          >
                            <Checkbox checked={instant} onChange={(checked) => setInstant(checked)} label='Instant' />
                          </Box>
                        </Tooltip>
                      )}
                    </>
                  )}
                  <UserHistoryButton accountId={accountId} module={`metrics_query_${metricsProvider}`} />
                </>
              )}
              {showDateTime && (
                <CustomDateTimeRangePicker
                  passedSelectedDateTime={{
                    startTime: selectedDateRange.startDate,
                    endTime: selectedDateRange.endDate,
                    shortcutClickTime: 0,
                  }}
                  onChange={(result: any) => {
                    const val = result?.selection ?? result;
                    if (val) handleDateRangeChange(val);
                  }}
                />
              )}
              <DownloadButton onClick={() => (showChartView ? { canvasId: 'k8sPromChart-0', tableId: '' } : { tableId: k8sProm })} />
            </>
          }
        >
          {showQueryBox && (
            <>
              {!!metricsProvider && (
                <Box
                  sx={{
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--ds-space-2)',
                    padding: 'var(--ds-space-1) var(--ds-space-3)',
                    backgroundColor: 'var(--ds-gray-alpha-100)',
                    borderRadius: 'var(--ds-radius-md)',
                    border: '1px solid var(--ds-gray-alpha-200)',
                    minWidth: 'fit-content',
                  }}
                >
                  <Text
                    value='Metrics Provider:'
                    sx={{
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-medium)',
                      color: 'var(--ds-gray-600)',
                      whiteSpace: 'nowrap',
                    }}
                  />
                  <CloudProviderIcon cloud_provider={metricsProvider} width='20px' height='20px' />
                  <Text
                    value={metricsProvider === 'victoria-metrics' ? 'Victoria Metrics' : snakeToTitleCase(metricsProvider)}
                    sx={{
                      fontSize: 'var(--ds-text-body-lg)',
                      fontWeight: 'var(--ds-font-weight-semibold)',
                      color: 'var(--ds-gray-700)',
                      whiteSpace: 'nowrap',
                    }}
                  />
                </Box>
              )}
              <ToggleGroup
                selection='single'
                size='md'
                value={qLEditor}
                onChange={(next) => setQLEditor(next)}
                options={[
                  { value: 'build', label: 'Builder' },
                  { value: 'code', label: 'Code' },
                  ...(!['dynatrace', 'solarwinds'].includes(metricsProvider) ? [{ value: 'ai', label: 'AI' }] : []),
                ]}
              />
              {qLEditor === 'code' && (
                <Tooltip
                  variant='explainer'
                  title='How to use'
                  desc={
                    <Box component='ul' sx={{ m: 0, pl: 'var(--ds-space-4)', '& li': { fontSize: 'var(--ds-text-small)', mb: 'var(--ds-space-1)' } }}>
                      <li>Type freely the metrics query; or use suggestions step by step</li>
                      <li>Use &quot;;&quot; to execute multiple queries</li>
                    </Box>
                  }
                  placement='top'
                  tooltipStyle={{ maxWidth: '420px' }}
                >
                  <InfoIcon sx={{ color: 'var(--ds-gray-500)', opacity: 0.4, fontSize: 'var(--ds-text-title)', cursor: 'default' }} />
                </Tooltip>
              )}
            </>
          )}
        </ListingLayout.Toolbar>
        <ListingLayout.Body sx={{ pb: 'var(--ds-space-4)' }}>
          {showQueryBox && (
            <QueryModeSwitcher
              accountId={accountId}
              params={{ ...selectedDateRange }}
              logProvider={metricsProvider}
              operatorDescriptors={operatorDescriptors}
              onQueryChange={(e: any) => {
                setQuery(e.query);
                setQueryKeys(e.queryKeys);
                setSolarwindsRequest(e.solarwindsRequest || null);
                if (e.index !== undefined) setEsIndex(e.index);
              }}
              queryItems={promqlItems as any}
              setQueryItems={setPromqlItems}
              setLlmQueryResponse={setLlmQueryResponse}
              setConversationId={setConversationId}
              qLEditor={qLEditor}
              setQLEditor={setQLEditor}
              onAiLoadingChange={(loading: boolean) => {
                setIsAiLoading(loading);
              }}
              deleteDataOnQueryBlockDeletion={deleteDataOnQueryBlockDeletion}
              providerType={'metrics'}
              initialQuery={query}
              initialEsIndex={esIndex}
            />
          )}
          {data.length > 0 && (
            <Box sx={{ display: 'flex', justifyContent: 'flex-end', mt: 'var(--ds-space-2)', mb: 'var(--ds-space-2)' }}>
              <Box
                sx={{
                  display: 'inline-flex',
                  alignItems: 'center',
                  gap: 'var(--ds-space-2)',
                  padding: 'var(--ds-space-1) var(--ds-space-2)',
                  backgroundColor: 'var(--ds-gray-alpha-100)',
                  borderRadius: 'var(--ds-radius-md)',
                  border: '1px solid var(--ds-gray-alpha-200)',
                }}
              >
                <Text
                  value='Display View'
                  sx={{
                    fontSize: 'var(--ds-text-small)',
                    fontWeight: 'var(--ds-font-weight-medium)',
                    color: 'var(--ds-gray-600)',
                    whiteSpace: 'nowrap',
                  }}
                />
                <Toggle
                  size='sm'
                  options={[
                    { value: 'chart', label: 'Chart' },
                    { value: 'table', label: 'Table' },
                  ]}
                  activeValue={showChartView ? 'chart' : 'table'}
                  onChange={(next) => setShowChartView(next === 'chart')}
                />
              </Box>
            </Box>
          )}
          <Box sx={{ width: '100%', maxWidth: '100%', marginTop: 'var(--ds-space-4)' }}>
            <TruncationWarning />
            {loading ? (
              <Skeleton shape='rect' height='400px' width='98%' />
            ) : showChartView ? (
              chartData.map((cd: any, index: number) => (
                <Box sx={{ mb: 'var(--ds-space-2)', width: '100%', maxWidth: '100%' }} key={cd.key || index}>
                  {cd.title && (
                    <Typography variant='body1' sx={{ mb: 'var(--ds-space-4)', fontWeight: 'var(--ds-font-weight-semibold)' }}>
                      {cd.title}
                    </Typography>
                  )}
                  {cd.query && (
                    <Box
                      sx={{
                        padding: 'var(--ds-space-1) var(--ds-space-3)',
                        border: '1px solid var(--ds-red-400)',
                        backgroundColor: 'var(--ds-red-100)',
                        borderRadius: 'var(--ds-radius-md)',
                        maxWidth: 'fit-content',
                        mb: 'var(--ds-space-4)',
                        mt: 'var(--ds-space-4)',
                        overflowWrap: 'break-word',
                        wordBreak: 'break-word',
                      }}
                    >
                      <Typography
                        variant='body1'
                        sx={{
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-gray-700)',
                          overflowWrap: 'break-word',
                          wordBreak: 'break-word',
                        }}
                      >
                        Query: {cd.query}
                      </Typography>
                    </Box>
                  )}
                  <Box
                    sx={{
                      padding: 'var(--ds-space-6) var(--ds-space-3)',
                      border: '1px solid var(--ds-gray-300)',
                      backgroundColor: 'var(--ds-background-300)',
                      borderRadius: 'var(--ds-radius-md)',
                      width: '98%',
                      maxWidth: '100%',
                      mb: 'var(--ds-space-6)',
                      mt: 'var(--ds-space-4)',
                      overflow: 'hidden',
                      boxSizing: 'border-box',
                    }}
                  >
                    <Box sx={{ width: '100%', maxWidth: '100%', overflow: 'hidden' }}>
                      <LineChart
                        id={`k8sPromChart-${index}`}
                        dataset={cd.data.data}
                        labels={cd.data.labels}
                        legendOptions={{
                          renderer: 'html',
                        }}
                        interactionOptions={{
                          enabled: false,
                        }}
                      />
                    </Box>
                  </Box>
                </Box>
              ))
            ) : (
              data.map((cd: any, index: number) => (
                <Box sx={{ mb: 'var(--ds-space-6)' }} key={`${cd.key || index}`}>
                  {cd.title && (
                    <Typography
                      variant='body1'
                      sx={{ mb: 'var(--ds-space-4)', mt: 'var(--ds-space-4)', fontWeight: 'var(--ds-font-weight-semibold)' }}
                    >
                      {cd.title}
                    </Typography>
                  )}
                  {cd.query && (
                    <Box
                      sx={{
                        padding: 'var(--ds-space-1) var(--ds-space-3)',
                        border: '1px solid var(--ds-red-400)',
                        backgroundColor: 'var(--ds-red-100)',
                        borderRadius: 'var(--ds-radius-md)',
                        maxWidth: 'fit-content',
                        mb: 'var(--ds-space-4)',
                        mt: 'var(--ds-space-4)',
                        overflowWrap: 'break-word',
                        wordBreak: 'break-word',
                      }}
                    >
                      <Typography
                        variant='body1'
                        sx={{
                          fontSize: 'var(--ds-text-body)',
                          fontWeight: 'var(--ds-font-weight-medium)',
                          color: 'var(--ds-gray-700)',
                          overflowWrap: 'break-word',
                          wordBreak: 'break-word',
                        }}
                      >
                        Query: {cd.query}
                      </Typography>
                    </Box>
                  )}
                  <KubernetesTable2
                    id={k8sProm}
                    totalRows={cd.totalRows || cd.data.length}
                    data={cd.data}
                    rounded={'0px'}
                    headers={cd.headers}
                    rowsPerPage={Math.min(cd.data.length, MAX_TABLE_ROWS)}
                    showExpandable={showDrilldown}
                    expandable={{
                      tabs: [
                        {
                          text: 'Row Details',
                          value: 0,
                          key: 'prometheus-query-log',
                          componentFn: (_option: any, query: any, _row: any) => {
                            return (
                              <ListingLayout id='query-metrics-row-details'>
                                <ListingLayout.Body>
                                  {Object.keys(query).length > 0 ? (
                                    <Box sx={{ padding: 'var(--ds-space-4)' }}>
                                      <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body-lg)' }}>
                                        Labels
                                      </Typography>
                                      <Divider />
                                      <Box
                                        sx={{
                                          display: 'flex',
                                          flexWrap: 'wrap',
                                          gap: 'var(--ds-space-2)',
                                          mt: 'var(--ds-space-2)',
                                          maxWidth: '80vw',
                                        }}
                                      >
                                        {Object.keys(query?.metric || {}).map((key) => (
                                          <Label key={key} text={`${key}=${query.metric[key]}`} tooltipCharLimit={40} displayTooltip />
                                        ))}
                                      </Box>
                                      <Divider />
                                      <Box sx={{ mb: 'var(--ds-space-4)' }}>
                                        <Typography sx={{ fontWeight: 'var(--ds-font-weight-semibold)', fontSize: 'var(--ds-text-body-lg)' }}>
                                          Trend
                                        </Typography>
                                        <Divider />
                                        <LineChart
                                          data={
                                            instant
                                              ? query?.value?.[1] != null
                                                ? [query.value[1]]
                                                : []
                                              : query?.values?.map((e: string) => parseFloat(e)) || []
                                          }
                                          labels={
                                            instant
                                              ? query?.value?.[0] != null
                                                ? [convertNumberToTimestamp(query.value[0] * 1000)]
                                                : []
                                              : query?.timestamps?.map((e: number) => convertNumberToTimestamp(e * 1000)) || []
                                          }
                                          chartLabel={'Count'}
                                        />
                                      </Box>
                                    </Box>
                                  ) : (
                                    <Box sx={{ padding: 'var(--ds-space-4)' }}>
                                      <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-gray-500)' }}>No Data</Typography>
                                    </Box>
                                  )}
                                </ListingLayout.Body>
                              </ListingLayout>
                            );
                          },
                        },
                      ],
                    }}
                    onPageChange={undefined}
                    onSortChange={undefined}
                  />
                  {index < data.length - 1 && <Divider sx={{ mt: 'var(--ds-space-6)' }} />}
                </Box>
              ))
            )}
          </Box>
        </ListingLayout.Body>
      </ListingLayout>
    </div>
  );
};

export default QueryMetrics;
