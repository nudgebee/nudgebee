import React, { useEffect, useRef, useState } from 'react';
import KubernetesTable2 from '@components/k8s/common/KubernetesTable2';
import { Box, Typography } from '@mui/material';
import { ToggleGroup } from '@ui/ToggleGroup';
import { Divider } from '@ui/Divider';
import { ListingLayout } from '@ui/ListingLayout';
import Tooltip from '@ui/Tooltip';
import { Toggle } from '@ui/Toggle';
import { Checkbox } from '@ui/Checkbox';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import DownloadButton from '@shared/buttons/DownloadButton';
import {
  convertNumberToTimestamp,
  isAtMost70PercentDifferent,
  parseHttpResponseBodyMessage,
  safeJSONParse,
  snakeToTitleCase,
} from 'src/utils/common';
import { useRouter } from 'next/router';
import LineChart from '@shared/charts/LineCharts';
import Text from '@shared/format/Text';
import UserHistoryButton from '@shared/widgets/UserHistory';
import apiAskNudgebee from '@api1/ask-nudgebee';
import { v4 as uuidv4 } from 'uuid';
import { Skeleton } from '@ui/Skeleton';
import { snackbar } from '@ui/Toast';
import QueryModeSwitcher from '@components/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components/k8s/common/operatorCatalog';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import { Button } from '@ui/Button';
import observability from '@api1/observability';
import cache from '@lib/cache';
import apiAccount from '@api1/account';
import { useData } from '@context/DataContext';
import { Info as InfoIcon, KeyboardArrowDown as KeyboardArrowDownIcon } from '@mui/icons-material';
import { DropdownMenu } from '@ui/DropdownMenu';
import { Label } from '@ui/Label';
import { buildSeriesChart, decimateData, MAX_CHART_DATASETS, MAX_CHART_DATA_POINTS } from '@utils/metricSeriesChart';

// One entry of observability_get_default_provider.available_providers: a provider the
// account can query for metrics, with the operator catalog it supports.
interface AvailableMetricsProvider {
  provider: string;
  supported_operator_descriptors?: OperatorDescriptor[];
}

// snakeToTitleCase would render the hyphenated provider name verbatim, so spell the
// one provider whose id is not snake_case out by hand.
const metricsProviderLabel = (provider: string) => (provider === 'victoria-metrics' ? 'Victoria Metrics' : snakeToTitleCase(provider));

// Data limiting constants to prevent memory issues with large datasets
const MAX_TABLE_ROWS = 100;

// A metric series can hold values JSON cannot carry as numbers: +Inf and -Inf from a
// ratio query whose denominator hit zero, NaN from 0/0, or something the provider sent
// that was not a number at all. The backend sends each of those points as null and
// reports what it was per series in `non_finite`, so the chart gap can be explained
// instead of leaving the reader to guess whether data is missing or broken.
const NON_FINITE_LABELS: Record<string, string> = {
  nan: 'undefined (NaN)',
  '+inf': 'infinite (+Inf)',
  '-inf': 'negative infinite (-Inf)',
  unparseable: 'not numeric',
};

const summariseNonFinite = (results: any[]): Record<string, number> => {
  const totals: Record<string, number> = {};
  results?.forEach((result: any) => {
    result?.payload?.forEach((series: any) => {
      Object.entries(series?.non_finite || {}).forEach(([kind, count]) => {
        if (typeof count === 'number' && count > 0) {
          totals[kind] = (totals[kind] || 0) + count;
        }
      });
    });
  });
  return totals;
};

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
  /** Formats the numbers the charts print; the chart default (`toFixed(2)`) when omitted. */
  formatValue?: (value: number) => string;
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
  formatValue,
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
  // ES Code-tab query language ('dsl' | 'kql'), mirrored from QueryModeSwitcher.
  // Builder mode ignores it — that path sends the cross-provider builder shape.
  const [esQueryType, setEsQueryType] = useState<string>('dsl');
  const [llmQueryResponse, setLlmQueryResponse] = useState('');
  const [instant, setInstant] = useState(false);
  const [promqlItems, setPromqlItems] = useState<Array<{ key: string; query: string; title?: string }>>([]);
  const [generateQuestionText, _setGenerateQuestionText] = useState('');
  const [qLEditor, setQLEditor] = useState('code');
  const [metricsProvider, setMetricsProvider] = useState('prometheus');
  // The account's configured default. Kept apart from metricsProvider so the switcher
  // can tell "switched back to default" (restore the resolved index) from an override.
  const [defaultProvider, setDefaultProvider] = useState('');
  const [defaultIndex, setDefaultIndex] = useState('');
  const [availableProviders, setAvailableProviders] = useState<AvailableMetricsProvider[]>([]);
  // Guards the async ES-index resolve below against a stale response landing after
  // the user has switched providers again.
  const selectedProviderRef = useRef('');
  // Bumped on every provider switch and used as QueryModeSwitcher's key, so a switch
  // remounts it. That component keeps the editor text, the builder blocks and the ES
  // query language in its own state, which a reset here does not reach: without the
  // remount the old query stays on screen while `query` is empty, and Submit sends
  // nothing. The key is not metricsProvider because that also changes when the account
  // default resolves on load; a remount then would clear a query that arrived in the
  // link from `query` while the editor still showed it.
  const [providerSwitchCount, setProviderSwitchCount] = useState(0);
  const [operatorDescriptors, setOperatorDescriptors] = useState<OperatorDescriptor[] | undefined>(undefined);
  const [isAiLoading, setIsAiLoading] = useState(false);
  const [truncationWarnings, setTruncationWarnings] = useState<{
    tableRows?: { total: number; shown: number };
    chartDatasets?: { total: number; shown: number };
    chartDataPoints?: { total: number; shown: number };
  }>({});
  // Counts of samples the provider returned that cannot be plotted, keyed by what
  // they were ("nan", "+inf", "-inf", "unparseable"). The backend sends those points
  // as null so the gap lands at the right time; without this the chart would show an
  // unexplained hole and the reader could not tell a divide-by-zero from bad data.
  const [nonFiniteCounts, setNonFiniteCounts] = useState<Record<string, number>>({});

  const deleteDataOnQueryBlockDeletion = (query_key: string) => {
    setData((prevData) => prevData.filter((item) => item.query_key !== query_key));
  };

  const fetchDefaultProvider = async () => {
    // v4: cache payload widened to carry available_providers + default_index so the
    // switcher can render from cache without a refetch.
    const cached = cache.get(`${accountId}-metrics-v4`);
    if (cached && typeof cached === 'object' && cached.provider) {
      setOperatorDescriptors(cached.operator_descriptors);
      setAvailableProviders(cached.available_providers || []);
      setDefaultIndex(cached.default_index || '');
      setEsIndex(cached.default_index || '');
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

      const obs = res?.data?.data?.observability_get_default_provider;
      const resolvedDefaultIndex = obs?.default_index || '';
      const descriptors = obs?.capabilities?.supported_operator_descriptors;
      const provider = obs?.provider || '';
      // Drop any malformed entry without a provider name so the dropdown map never
      // hits snakeToTitleCase(undefined) or builds an invalid option id.
      const providers: AvailableMetricsProvider[] = Array.isArray(obs?.available_providers)
        ? obs.available_providers.filter((p: AvailableMetricsProvider) => p?.provider)
        : [];
      // Guarantee the active default is always one of the selectable options (it can
      // be missing when `provider` came from the cluster fallback), so it shows a
      // check-mark and the user can switch back to it.
      if (provider && !providers.some((p) => p?.provider === provider)) {
        providers.unshift({ provider, supported_operator_descriptors: descriptors });
      }
      setOperatorDescriptors(descriptors);
      setAvailableProviders(providers);
      setDefaultIndex(resolvedDefaultIndex);
      setEsIndex(resolvedDefaultIndex);
      cache.set(
        `${accountId}-metrics-v4`,
        { provider, operator_descriptors: descriptors, available_providers: providers, default_index: resolvedDefaultIndex },
        5 * 60
      );
      return provider;
    } catch (error: any) {
      snackbar.error(error.message || 'Failed to fetch default provider');
      return '';
    }
  };

  // Operator catalog for a given provider. Each available_providers entry carries its
  // own supported_operator_descriptors; fall back to the default provider's entry,
  // then to whatever catalog is currently loaded.
  const getDescriptorsForProvider = (providerName: string): OperatorDescriptor[] | undefined => {
    const entry = availableProviders?.find((p) => p?.provider === providerName);
    if (entry?.supported_operator_descriptors) {
      return entry.supported_operator_descriptors;
    }
    const defaultEntry = availableProviders?.find((p) => p?.provider === defaultProvider);
    return defaultEntry?.supported_operator_descriptors ?? operatorDescriptors;
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
    setNonFiniteCounts({});
  };

  // Switch the active metrics provider. Treated like a fresh provider: clears the
  // current query/results, swaps in the new provider's operator catalog, and resets
  // the ES index (restoring the configured default only when switching back to the
  // account default). metricsProvider is already sent as `metric_provider` on every
  // request, so the fetch follows the selection with no extra plumbing.
  const handleProviderChange = (newProvider: string) => {
    if (!newProvider || newProvider === metricsProvider) {
      return;
    }
    const descriptors = getDescriptorsForProvider(newProvider);
    resetAllStates();
    setSolarwindsRequest(null);
    setEsQueryType('dsl');
    setOperatorDescriptors(descriptors);
    selectedProviderRef.current = newProvider;
    setMetricsProvider(newProvider);
    setProviderSwitchCount((count) => count + 1);

    if (newProvider === defaultProvider) {
      // Switching back to the account default — its per-account/top-level index was
      // already resolved on load.
      setEsIndex(defaultIndex);
      return;
    }

    // Overriding to a non-default provider. The index picker only renders for ES, so
    // only ES needs a resolved default; ask the backend for ES's per-account
    // (Advanced Settings mapping) / top-level index instead of leaving it blank.
    setEsIndex('');
    if (newProvider === 'ES' && accountId && accountId !== 'demo') {
      apiAccount
        .getDefaultProvider({ account_id: accountId, provider_type: 'metrics', provider: 'ES' })
        .then((res: any) => {
          const resolved = res?.data?.data?.observability_get_default_provider?.default_index || '';
          // Ignore a stale response if the user has since switched providers.
          if (resolved && selectedProviderRef.current === 'ES') {
            setEsIndex(resolved);
          }
        })
        .catch(() => {
          // Non-fatal: the index picker just starts empty and the user picks one.
        });
    }
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
        setDefaultProvider('prometheus');
        selectedProviderRef.current = 'prometheus';
      } else {
        const resolvedDefault = await fetchDefaultProvider();
        let metricsProvider = '';
        if (resolvedDefault) {
          metricsProvider = resolvedDefault;
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
        setDefaultProvider(metricsProvider);
        selectedProviderRef.current = metricsProvider;
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
      setNonFiniteCounts(summariseNonFinite(preparedEvidences));
      setLoading(false);
    }
  }, [preparedEvidences]);

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

        // Series alignment + downsampling is shared with the cloud account's
        // Metrics tab; only the table half below is specific to this screen.
        const chart = buildSeriesChart(g.payload);
        const decimatedLabels = chart.labels;
        const chartDataDataset = chart.dataset;

        maxTotalDatasets = Math.max(maxTotalDatasets, chart.totalDatasets);
        maxTotalDataPoints = Math.max(maxTotalDataPoints, chart.totalDataPoints);
        datasetsWereTruncated = datasetsWereTruncated || chart.datasetsWereTruncated;
        pointsWereDecimated = pointsWereDecimated || chart.pointsWereDecimated;

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
                  // Result.timestamps is already Unix epoch MILLISECONDS (see the
                  // Result struct in api-server observability/entity.go); every
                  // provider converts to ms before marshalling. Scaling by 1000
                  // again pushed dates ~56,000 years into the future.
                  text: new Date(item).toString(),
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
            // Already milliseconds — see the note on the table branch above.
            labels: decimatedLabels.map((e: number) => convertNumberToTimestamp(e)),
            data: fromMetric
              ? chartDataDataset
              : // A series with no labels of its own is named after the metric it charts.
                [
                  {
                    label: g.query_key || 'Value',
                    data: decimateData(g.payload[0]?.values?.map((e: string) => parseFloat(e)) || [], MAX_CHART_DATA_POINTS),
                  },
                ],
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
        // In Builder mode there is no query box to type into — the query is empty
        // because no label filter was added. Match the logs builder's wording and
        // severity (KubernetesLogs) rather than telling the user to type a query.
        if (qLEditor === 'build') {
          snackbar.warning('Please select at least one label filter');
        } else {
          snackbar.error('Please enter a query before submitting');
        }
      }
      return;
    }
    setLoading(true);
    setData([]);
    setChartData([]);
    setTruncationWarnings({});
    setNonFiniteCounts({});

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

    // The provider-native query the backend actually executed, keyed like `promqls`.
    // ES sends the cross-provider builder shape and the backend renders it into an
    // _search body, so echoing what we sent showed a query that never ran.
    const executedQueries: Record<string, string> = {};

    // KQL is translated to DSL server-side, so the executed query is unreadable
    // next to what was typed. Surface the source too — but only for KQL: in
    // Builder mode the sent string is the internal `_binary` shape, which is
    // exactly what this chip stopped showing.
    const showSourceQuery = metricsProvider === 'ES' && qLEditor === 'code' && esQueryType === 'kql';

    const getQueryByKey = (key: string) => {
      // `||`, not `??`: Datadog/Splunk-O11y/Chronosphere/CloudWatch never set Query,
      // so the value arrives as '' and must still fall back to the string we sent.
      const entry: any = executedQueries[key] || promqls[key];
      const sourceQuery = showSourceQuery && promqls[key] !== entry ? promqls[key] : '';
      return entry
        ? {
            query: entry,
            sourceQuery,
            title: '',
            query_key: key,
          }
        : {
            query: '',
            sourceQuery: '',
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
      // query_type tells the backend the query is raw ES text rather than the
      // cross-provider builder shape. Gating it on the Code tab alone meant a query
      // written by the AI tab arrived unlabelled, so the backend parsed it as a
      // builder where-clause list and rejected it outright. Builder mode is the only
      // mode that must omit it.
      ...(metricsProvider === 'ES' && esIndex
        ? { request: { metric_name: esIndex, ...(qLEditor !== 'build' ? { query_type: esQueryType || 'dsl' } : {}) } }
        : {}),
      // The backend records query history; this flag distinguishes a real submit
      // from the date-range/instant-toggle re-runs that reuse this same path.
      // See FetchMetricsRequest.RecordHistory.
      record_history: fromOnSubmit,
    };

    observability
      .metricsQuery(requestBody)
      .then((res) => {
        const results = res?.data?.data?.metrics_list?.results || [];

        results.forEach((result: any) => {
          if (result?.query_key && result?.query) {
            executedQueries[result.query_key] = result.query;
          }
        });

        const resultsWithErrors = results.filter((result: any) => result.error);

        if (resultsWithErrors.length > 0) {
          setData([]);
          setChartData([]);

          const errorMessages = resultsWithErrors.map((result: any) => result.error).filter(Boolean);
          const displayMessage =
            errorMessages.length > 0 ? errorMessages.join('\n') : 'Invalid query: No data returned. Please check your query syntax.';
          snackbar.error(displayMessage);
        } else if (results?.length) {
          const { tableData, graphData, truncationInfo } = processEvidenceDataKeys(results, getQueryByKey);
          setData(tableData);
          setChartData(graphData);
          setTruncationWarnings(truncationInfo);
          setNonFiniteCounts(summariseNonFinite(results));
        } else if (res?.data?.errors?.length) {
          setData([]);
          setChartData([]);
          snackbar.error(`failed to query metrics ${parseHttpResponseBodyMessage(res?.data)}`);
        } else {
          setData([]);
          setChartData([]);
        }
        if (type == 'ai') {
          aiCreateFeedback(true, query, llmQueryResponse);
        }
      })
      .catch(() => {
        snackbar.error('Failed to fetch the Data');
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
        setNonFiniteCounts(summariseNonFinite([{ payload: metricsData }]));
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
  const NonFiniteNotice = () => {
    const kinds = Object.entries(nonFiniteCounts).filter(([, count]) => count > 0);

    if (kinds.length === 0) {
      return null;
    }

    const total = kinds.reduce((sum, [, count]) => sum + count, 0);
    const described = kinds.map(([kind, count]) => `${count} ${NON_FINITE_LABELS[kind] || kind}`).join(', ');

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
        data-testid='metrics-non-finite-notice'
      >
        <InfoIcon sx={{ color: 'var(--ds-amber-700)', fontSize: 'var(--ds-text-title)' }} />
        <Typography sx={{ fontSize: 'var(--ds-text-body)', color: 'var(--ds-amber-700)' }}>
          <strong>
            {total} data {total === 1 ? 'point' : 'points'} could not be plotted:
          </strong>{' '}
          {described}. They appear as gaps at the time they occurred.
        </Typography>
      </Box>
    );
  };

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
                    metricsProvider === 'cubeapm' ||
                    metricsProvider === 'signoz' ||
                    metricsProvider === 'openobserve' ||
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
                  <UserHistoryButton accountId={accountId} module={`metrics_query_${metricsProvider?.toLowerCase()}`} />
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
              {!!metricsProvider &&
                (() => {
                  const badgeSx = {
                    display: 'flex',
                    alignItems: 'center',
                    gap: 'var(--ds-space-2)',
                    padding: 'var(--ds-space-1) var(--ds-space-3)',
                    backgroundColor: 'var(--ds-gray-alpha-100)',
                    borderRadius: 'var(--ds-radius-md)',
                    border: '1px solid var(--ds-gray-alpha-200)',
                    minWidth: 'fit-content',
                  } as const;
                  const badgeInner = (
                    <>
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
                        value={metricsProviderLabel(metricsProvider)}
                        sx={{
                          fontSize: 'var(--ds-text-body-lg)',
                          fontWeight: 'var(--ds-font-weight-semibold)',
                          color: 'var(--ds-gray-700)',
                          whiteSpace: 'nowrap',
                        }}
                      />
                    </>
                  );

                  // Only offer a switcher when the account has more than one
                  // configured metrics provider; otherwise keep the static badge.
                  if (availableProviders.length <= 1) {
                    return <Box sx={badgeSx}>{badgeInner}</Box>;
                  }

                  return (
                    <DropdownMenu
                      align='start'
                      minWidth={220}
                      trigger={
                        <Box
                          component='button'
                          type='button'
                          id='metrics-provider-switcher'
                          sx={{
                            ...badgeSx,
                            font: 'inherit',
                            cursor: 'pointer',
                            transition: 'border-color 120ms ease, background-color 120ms ease',
                            '&:hover': { borderColor: 'var(--ds-gray-300)', backgroundColor: 'var(--ds-gray-alpha-200)' },
                          }}
                        >
                          {badgeInner}
                          <KeyboardArrowDownIcon sx={{ fontSize: 'var(--ds-text-heading)', color: 'var(--ds-gray-500)' }} />
                        </Box>
                      }
                      items={availableProviders.map((p) => ({
                        id: `metrics-provider-option-${p.provider}`,
                        label: metricsProviderLabel(p.provider),
                        icon: <CloudProviderIcon cloud_provider={p.provider} width='16px' height='16px' />,
                        kbd: p.provider === metricsProvider ? '\u2713' : undefined,
                        searchText: p.provider,
                        onSelect: () => handleProviderChange(p.provider),
                      }))}
                    />
                  );
                })()}
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
              key={providerSwitchCount}
              accountId={accountId}
              params={{ ...selectedDateRange }}
              logProvider={metricsProvider}
              // The ES index picker lists targets through the logs action (there is no
              // metrics equivalent), which resolves the account's DEFAULT LOG provider
              // when no provider is pinned. On an account whose logs default is Loki,
              // picking ES here asked Loki for indexes and 400'd. Pin the selected
              // metrics provider so the listing resolves ES. Safe for the metrics
              // formatted-query preview, which branches to its own handler.
              providerOverride={metricsProvider}
              operatorDescriptors={operatorDescriptors}
              onQueryChange={(e: any) => {
                setQuery(e.query);
                setQueryKeys(e.queryKeys);
                setSolarwindsRequest(e.solarwindsRequest || null);
                if (e.index !== undefined) setEsIndex(e.index);
                if (e.queryType !== undefined) setEsQueryType(e.queryType);
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
            <NonFiniteNotice />
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
                        {cd.sourceQuery && (
                          <>
                            KQL: {cd.sourceQuery}
                            <br />
                          </>
                        )}
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
                        formatValue={formatValue}
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
                        {cd.sourceQuery && (
                          <>
                            KQL: {cd.sourceQuery}
                            <br />
                          </>
                        )}
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
                                              : query?.timestamps?.map((e: number) => convertNumberToTimestamp(e)) || []
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
