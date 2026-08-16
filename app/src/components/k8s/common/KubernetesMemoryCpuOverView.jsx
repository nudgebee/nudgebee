import { useState, useEffect, useCallback } from 'react';
import { Grid, Box } from '@mui/material';
import K8sMemoryCpuIndicator from './K8sMemoryCpuIndicator';
import PropTypes from 'prop-types';
import apiKubernetes1 from '@api1/kubernetes1';
import { buildPromQueries } from '@shared/MetricQueryInfo';
import { getSpecificTime } from '@lib/datetime';
import { Skeleton } from '@ui/Skeleton';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import { ds } from '@utils/colors';
import { Modal } from '@ui/Modal';
import Chart from '@ui/Chart';
import dayjs from 'dayjs';

export const CpuMemorySkeleton = ({ sx = {} }) => (
  <Box sx={{ display: 'flex', gap: 'var(--ds-space-4)', ...sx }}>
    <Box sx={{ flex: 1 }}>
      <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 65)} />
    </Box>
    <Box sx={{ flex: 1 }}>
      <Skeleton shape='rect' width='100%' height={ds.space.mul(0, 65)} />
    </Box>
  </Box>
);

// Per-metric config for the Trend popup — which range-query keys build each line.
const TREND_CONFIG = {
  cpu: {
    title: 'CPU Utilization Trend',
    unit: 'Cores',
    divisor: 1,
    keys: { usage: 'cpu_usage_trend', total: 'cpu_total', request: 'cpu_request', limit: 'cpu_limit' },
  },
  memory: {
    title: 'Memory Utilization Trend',
    unit: 'GiB',
    divisor: 1024 ** 3,
    keys: { usage: 'mem_usage_trend', total: 'mem_total', request: 'memory_request', limit: 'memory_limit' },
  },
};

// One line per label — matches the per-workload utilisation chart (Usage amber,
// Request blue, Limit red), with Total in grey as the capacity ceiling.
const TREND_SERIES = [
  { field: 'usage', label: 'Usage', color: ds.amber[500] },
  { field: 'total', label: 'Total', color: ds.gray[400] },
  { field: 'request', label: 'Request', color: ds.blue[400] },
  { field: 'limit', label: 'Limit', color: ds.red[500] },
];

// Shared by both card pickers and the Trend popup's own time filter.
const RANGE_SHORTCUTS = [
  'Last 5 Minutes',
  'Last 10 Minutes',
  'Last 15 Minutes',
  'Last 30 Minutes',
  'Last 1 Hour',
  'Last 3 Hours',
  'Last 6 Hours',
  'Last 12 Hours',
  'Last 24 Hours',
];

/** Default window for the utilisation cards: the last hour. */
export const createUtilizationRange = () => ({
  startDate: getSpecificTime(60),
  endDate: new Date().getTime(),
  shortcutClickTime: 1 * 60 * 60 * 1000,
});

/**
 * The range picker on its own, so a parent card can place it in its heading row
 * beside the title rather than letting this component drop it above the gauges.
 * Pair it with the `dateRange` / `onDateRangeChange` props below — supplying
 * those makes the range controlled and suppresses the internal picker.
 */
export const UtilizationRangePicker = ({ value, onChange, sx = {} }) => (
  <CustomDateTimeRangePicker
    // Size to the label instead of the picker's fixed 180px default: this sits
    // inline next to a card title, and the spare width is what pushed it onto
    // its own line on narrower cards.
    width='auto'
    showAbsoluteRange={false}
    showOnlyCalenderIcon={false}
    passedSelectedDateTime={{
      startTime: value?.startDate,
      endTime: value?.endDate,
      shortcutClickTime: value?.shortcutClickTime || 0,
    }}
    shortCuts={RANGE_SHORTCUTS}
    onChange={(dr) =>
      onChange?.({
        startDate: dr.selection.startTime,
        endDate: dr.selection.endTime,
        shortcutClickTime: dr.selection.shortcutClickTime || 0,
      })
    }
    sx={{
      border: '1px solid var(--ds-brand-200) !important',
      borderRadius: 'var(--ds-radius-sm) !important',
      ...sx,
    }}
  />
);

UtilizationRangePicker.propTypes = {
  value: PropTypes.object,
  onChange: PropTypes.func,
  sx: PropTypes.object,
};

const formatTrendTs = (ts) => dayjs(ts > 1e12 ? ts : ts * 1000).format('HH:mm');

// Build Chart.Series { labels, dataset } from a metric group's range-query response.
const buildTrendChart = (results, cfg) => {
  const byKey = {};
  results.forEach((item) => {
    const first = (item.payload || [])[0];
    byKey[item.query_key] = { values: (first?.values || []).map((v) => parseFloat(v)), timestamps: first?.timestamps || [] };
  });
  // Longest series drives the x-axis labels (a query_range batch shares one time grid).
  const labelSeries = Object.values(byKey).reduce((a, b) => (b.timestamps.length > a.timestamps.length ? b : a), { timestamps: [] });
  const labels = labelSeries.timestamps.map(formatTrendTs);
  const dataset = TREND_SERIES.map((s) => {
    const series = byKey[cfg.keys[s.field]] || { values: [] };
    return {
      label: s.label,
      data: series.values.map((v) => (Number.isFinite(v) ? v / cfg.divisor : null)),
      borderColor: s.color,
      backgroundColor: s.color,
      borderWidth: 1.5,
      pointRadius: 0,
      tension: 0.3,
      fill: false,
    };
  });
  return { labels, dataset };
};

const KubernetesMemoryCpuOverView = ({
  showUpdatedUi = false,
  requiredTooltip = false,
  clusterSummary,
  updatedOverview = true,
  showUsage = false,
  hideLabels = false,
  sx = {},
  accountId,
  // Controlled range. When `dateRange` is supplied the parent owns the window and
  // renders its own <UtilizationRangePicker> (so it can sit beside the card
  // title); this component then skips its internal picker row entirely.
  dateRange,
  onDateRangeChange,
}) => {
  const [memoryCpuData, setMemoryCpuData] = useState({
    memory: [
      { name: 'Total', total: 0 },
      { name: 'Usage', usage: 0 },
      { name: 'Limit', limit: 0 },
      { name: 'Request', request: 0 },
    ],
    cpu: [
      { name: 'Total', total: 0 },
      { name: 'Usage', usage: 0 },
      { name: 'Limit', limit: 0 },
      { name: 'Request', request: 0 },
    ],
  });
  const [loading, setLoading] = useState(!!accountId);
  const [loadingStates, setLoadingStates] = useState({
    cpu: !!accountId,
    memory: !!accountId,
    percentile: !!accountId,
  });
  // Track which data sections have been loaded
  const [dataLoaded, setDataLoaded] = useState({
    cpu: false,
    memory: false,
    percentile: false,
  });
  // Store individual data sections as they become available
  const [individualData, setIndividualData] = useState({
    cpu: {},
    memory: {},
    percentile: {},
  });
  // Executed metric queries (keyed by metric, e.g. cpu_real / mem_total) accumulated across the API calls.
  const [promQueries, setPromQueries] = useState({});
  const mergePromQueries = (response) => setPromQueries((prev) => ({ ...prev, ...buildPromQueries(response) }));

  // Full utilisation timeseries shown in the per-metric Trend popup (fetched on demand).
  const [trendMetric, setTrendMetric] = useState(null); // 'cpu' | 'memory' | null
  const [trendChart, setTrendChart] = useState({ labels: [], dataset: [] });
  const [trendLoading, setTrendLoading] = useState(false);
  // The popup's own time filter — seeded from the card's selection on open, then
  // independently adjustable inside the dialog.
  const [trendRange, setTrendRange] = useState(null);

  const [internalDateRange, setInternalDateRange] = useState(createUtilizationRange);
  const isRangeControlled = !!dateRange;
  const selectedDateRange = isRangeControlled ? dateRange : internalDateRange;

  const formattedDateRange = {
    startTime: selectedDateRange.startDate,
    endTime: selectedDateRange.endDate,
    shortcutClickTime: selectedDateRange.shortcutClickTime || 0,
  };

  useEffect(() => {
    if (accountId) {
      getClusterDataFromPromethethus(accountId);
    }
  }, [accountId, selectedDateRange]);

  // Effect to handle loading state synchronization - set loading false as soon as any data is available
  useEffect(() => {
    const anyDataLoaded = Object.values(dataLoaded).some((loaded) => loaded);
    if (anyDataLoaded && loading) {
      // Set loading to false as soon as any data section is loaded
      setLoading(false);
    }
  }, [dataLoaded, loading]);

  // Update display data whenever individual data sections change
  useEffect(() => {
    updateDisplayData();
  }, [individualData, dataLoaded]);

  const createMetricsParser = (combinedData) => {
    const getSeriesValue = (series) => {
      const value = series?.[0]?.value?.at?.(-1);
      return value ? parseFloat(value) : 0;
    };

    const getFormattedValue = (series) => {
      const value = getSeriesValue(series);
      return value;
    };

    const metrics = {
      memory: {
        total: () => getFormattedValue(combinedData.mem_total),
        usage: () => getFormattedValue(combinedData.mem_real),
        limit: () => getFormattedValue(combinedData.memory_limit),
        request: () => getFormattedValue(combinedData.memory_request),
        p50usage: () => getFormattedValue(combinedData.p50_mem),
        p90usage: () => getFormattedValue(combinedData.p90_mem),
        maxusage: () => getFormattedValue(combinedData.max_usage_mem),
      },
      cpu: {
        total: () => getFormattedValue(combinedData.cpu_total),
        usage: () => getFormattedValue(combinedData.cpu_real),
        limit: () => getFormattedValue(combinedData.cpu_limit),
        request: () => getFormattedValue(combinedData.cpu_request),
        p50usage: () => getFormattedValue(combinedData.p50_cpu),
        p90usage: () => getFormattedValue(combinedData.p90_cpu),
        maxusage: () => getFormattedValue(combinedData.max_usage_cpu),
      },
    };

    const generateMetricsData = (type, includeExtended = false) => {
      const baseMetrics = ['total', 'usage', 'limit', 'request'];
      const extendedMetrics = [...baseMetrics, 'p50usage', 'p90usage', 'maxusage'];
      const metricsToUse = includeExtended ? extendedMetrics : baseMetrics;

      return metricsToUse.map((metric) => {
        const value = metrics[type][metric]();
        return {
          name: metric.charAt(0).toUpperCase() + metric.slice(1),
          [metric]: value,
        };
      });
    };

    return {
      getMetricsData: (updatedOverview = false) => ({
        memory: generateMetricsData('memory', updatedOverview),
        cpu: generateMetricsData('cpu', updatedOverview),
      }),
    };
  };

  // Update display data based on available individual data sections
  // Use useCallback to prevent unnecessary re-renders and ensure stable reference
  const updateDisplayData = useCallback(() => {
    const { cpu, memory, percentile } = individualData;

    // Combine available data
    const combinedData = {
      ...cpu,
      ...memory,
      ...percentile,
    };

    if (Object.keys(combinedData).length > 0) {
      const metricsParser = createMetricsParser(combinedData);
      const newData = metricsParser.getMetricsData(updatedOverview);

      // Use functional update to prevent race conditions
      setMemoryCpuData((prevData) => {
        const updatedData = { ...prevData };

        // Only update sections that have new data available
        if (dataLoaded.cpu && newData.cpu) {
          updatedData.cpu = newData.cpu;
        }
        if (dataLoaded.memory && newData.memory) {
          updatedData.memory = newData.memory;
        }

        return updatedData;
      });
    }
  }, [individualData, dataLoaded, updatedOverview]);

  // Helper function to extract data from API response
  const extractDataFromResponse = (results) => {
    const formattedData = {};
    results?.forEach((item) => {
      const key = item.query_key;
      const payload = item.payload || [];

      // Map each series in the payload to the target format
      formattedData[key] = payload.map((series) => {
        // We grab the LAST available data point to match the "Instant Vector" format
        // (value: [timestamp, "value"])
        const lastIndex = series.timestamps?.length - 1;
        const timestamp = series.timestamps?.[lastIndex];
        const value = series.values?.[lastIndex];

        return {
          metric: series.metric || {},
          value: [timestamp, String(value)],
        };
      });
    });

    return formattedData;
  };

  // Fetch one metric group's full timeseries (usage/total/request/limit) as a range
  // query for the given window.
  const fetchTrendSeries = async (metric, range, isCancelled = () => false) => {
    const cfg = TREND_CONFIG[metric];
    if (!cfg || !accountId || !range) {
      return;
    }
    setTrendLoading(true);
    try {
      const response = await apiKubernetes1.utilisationApi({
        accountId,
        metrics: Object.values(cfg.keys),
        startDate: range.startDate,
        endDate: range.endDate,
        instant: false,
      });
      if (isCancelled()) {
        return;
      }
      if (Array.isArray(response)) {
        mergePromQueries(response);
        setTrendChart(buildTrendChart(response, cfg));
      }
    } catch (error) {
      if (!isCancelled()) {
        console.error('Error fetching utilisation trend:', error);
      }
    } finally {
      if (!isCancelled()) {
        setTrendLoading(false);
      }
    }
  };

  // Open the Trend popup for a metric, seeding its time filter from the card's current
  // selection; the fetch runs from the effect below (also on in-popup filter changes).
  const openTrend = (metric) => {
    if (!TREND_CONFIG[metric] || !accountId) {
      return;
    }
    setTrendChart({ labels: [], dataset: [] });
    setTrendRange({ ...selectedDateRange });
    setTrendMetric(metric);
  };

  useEffect(() => {
    if (!trendMetric || !trendRange) {
      return;
    }
    let cancelled = false;
    fetchTrendSeries(trendMetric, trendRange, () => cancelled);
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [trendMetric, trendRange]);

  // Make API call for a specific query group with individual response handling
  const makeApiCall = async (accountId, metrics, apiType) => {
    try {
      setLoadingStates((prev) => ({ ...prev, [apiType]: true }));

      const response = await apiKubernetes1.utilisationApi({
        accountId: accountId,
        metrics: metrics,
        startDate: selectedDateRange.startDate,
        endDate: selectedDateRange.endDate,
        instant: true,
      });

      mergePromQueries(response);
      const data = extractDataFromResponse(response);

      // Update individual data section immediately when available
      if (Object.keys(data).length > 0) {
        // Use functional updates to prevent race conditions
        setIndividualData((prev) => ({
          ...prev,
          [apiType]: data,
        }));

        // Set data loaded flag using functional update
        setDataLoaded((prev) => ({
          ...prev,
          [apiType]: true,
        }));
      }

      return data;
    } catch (error) {
      console.error(`Error fetching ${apiType} data:`, error);
      return {};
    } finally {
      setLoadingStates((prev) => ({ ...prev, [apiType]: false }));
    }
  };

  const getClusterDataFromPromethethus = async (accountId) => {
    // Reset data loaded state when starting new fetch
    setDataLoaded({
      cpu: false,
      memory: false,
      percentile: false,
    });
    setIndividualData({
      cpu: {},
      memory: {},
      percentile: {},
    });
    setPromQueries({});
    // Reset memoryCpuData to initial state to prevent stale data
    setMemoryCpuData({
      memory: [
        { name: 'Total', total: 0 },
        { name: 'Usage', usage: 0 },
        { name: 'Limit', limit: 0 },
        { name: 'Request', request: 0 },
      ],
      cpu: [
        { name: 'Total', total: 0 },
        { name: 'Usage', usage: 0 },
        { name: 'Limit', limit: 0 },
        { name: 'Request', request: 0 },
      ],
    });

    if (!updatedOverview) {
      // Use the old single API call for backward compatibility
      try {
        setLoading(true);
        const response = await apiKubernetes1.utilisationApi({
          accountId: accountId,
          metrics: ['cpu_real', 'cpu_request', 'cpu_limit', 'cpu_total', 'mem_real', 'mem_total', 'memory_limit', 'memory_request'],
          startDate: selectedDateRange.startDate,
          endDate: selectedDateRange.endDate,
          instant: true,
        });
        mergePromQueries(response);
        const data = extractDataFromResponse(response);
        if (Object.keys(data).length > 0) {
          const metricsParser = createMetricsParser(data);
          setMemoryCpuData(metricsParser.getMetricsData(updatedOverview));
        }
      } finally {
        setLoading(false);
      }
      return;
    }

    // New implementation with 3 separate API calls that update UI independently
    try {
      setLoading(true);

      // Execute all 3 API calls in parallel, but each updates UI as soon as it completes
      // Don't await here - let each call complete independently
      makeApiCall(accountId, ['cpu_real', 'cpu_total', 'cpu_request', 'cpu_limit'], 'cpu');
      makeApiCall(accountId, ['mem_real', 'mem_total', 'memory_limit', 'memory_request'], 'memory');
      makeApiCall(accountId, ['p90_mem', 'p90_cpu', 'p50_mem', 'p50_cpu', 'max_usage_mem', 'max_usage_cpu'], 'percentile');
    } catch (error) {
      console.error('Error fetching cluster data:', error);
    }
    // Note: loading state is managed by the useEffect that watches dataLoaded
  };

  const handleDateRangeChange = (passedSelectedDateTime) => {
    const next = {
      startDate: passedSelectedDateTime.startTime,
      endDate: passedSelectedDateTime.endTime,
      shortcutClickTime: passedSelectedDateTime.shortcutClickTime || 0,
    };
    if (isRangeControlled) {
      onDateRangeChange?.(next);
    } else {
      setInternalDateRange(next);
    }
  };

  // Split the executed queries per gauge (keys like cpu_real / mem_total / memory_limit).
  const cpuQueries = {};
  const memoryQueries = {};
  Object.entries(promQueries).forEach(([key, q]) => {
    if (key.startsWith('cpu')) {
      cpuQueries[key] = q;
    } else if (key.startsWith('mem')) {
      memoryQueries[key] = q;
    }
  });

  return (
    <>
      {updatedOverview ? (
        <Grid container mt={ds.space.mul(0, 5)} spacing={ds.space.mul(0, 10)} sx={{ position: 'relative' }}>
          {/* Fallback picker row for callers that don't own the range themselves.
              It used to be `position: absolute; top: -20px; right: 0`, which
              reserved no layout space and painted over the card heading. Parents
              that want the picker beside their title pass `dateRange` /
              `onDateRangeChange` and render <UtilizationRangePicker> instead. */}
          {!isRangeControlled && (
            <Grid item xs={12} sx={{ display: 'flex', justifyContent: 'flex-end' }}>
              <CustomDateTimeRangePicker
                showAbsoluteRange={false}
                showOnlyCalenderIcon={false}
                passedSelectedDateTime={formattedDateRange}
                shortCuts={RANGE_SHORTCUTS}
                onChange={(dr) => handleDateRangeChange(dr.selection)}
                sx={{
                  border: '1px solid var(--ds-brand-200) !important',
                  borderRadius: 'var(--ds-radius-sm) !important',
                }}
              />
            </Grid>
          )}
          <Grid item xs={12} sm={6}>
            {!dataLoaded.cpu && loadingStates.cpu ? (
              <Skeleton width={'100%'} height={ds.space.mul(0, 100)} />
            ) : (
              <>
                <K8sMemoryCpuIndicator
                  showUpdatedUi={showUpdatedUi}
                  requiredTooltip={requiredTooltip}
                  clusterSummary={clusterSummary}
                  key='CPU'
                  unit=''
                  title='CPU'
                  queries={cpuQueries}
                  data={memoryCpuData?.cpu ?? []}
                  updatedOverview={updatedOverview}
                  showUsage={showUsage}
                  hideLabels={hideLabels}
                  onOpenTrend={() => openTrend('cpu')}
                />
                {/* Loading indicator for CPU data */}
                {loadingStates.cpu && !dataLoaded.cpu && (
                  <div
                    style={{
                      position: 'absolute',
                      top: ds.space.mul(0, 5),
                      left: ds.space.mul(0, 5),
                      backgroundColor: 'var(--ds-blue-600)',
                      color: ds.background[100],
                      padding: 'var(--ds-space-1) var(--ds-space-2)',
                      borderRadius: 'var(--ds-radius-sm)',
                      fontSize: 'var(--ds-text-small)',
                      zIndex: 1000,
                    }}
                  >
                    Loading CPU...
                  </div>
                )}
              </>
            )}
          </Grid>
          <Grid item xs={12} sm={6}>
            {!dataLoaded.memory && loadingStates.memory ? (
              <Skeleton width={'100%'} height={ds.space.mul(0, 100)} />
            ) : (
              <>
                <K8sMemoryCpuIndicator
                  showUpdatedUi={showUpdatedUi}
                  requiredTooltip={requiredTooltip}
                  clusterSummary={clusterSummary}
                  key='Memory'
                  unit=''
                  title='Memory'
                  queries={memoryQueries}
                  data={memoryCpuData?.memory ?? []}
                  updatedOverview={updatedOverview}
                  showUsage={showUsage}
                  hideLabels={hideLabels}
                  onOpenTrend={() => openTrend('memory')}
                />
                {/* Loading indicator for Memory data */}
                {loadingStates.memory && !dataLoaded.memory && (
                  <div
                    style={{
                      position: 'absolute',
                      top: ds.space.mul(0, 5),
                      left: ds.space.mul(0, 5),
                      backgroundColor: 'var(--ds-blue-600)',
                      color: ds.background[100],
                      padding: 'var(--ds-space-1) var(--ds-space-2)',
                      borderRadius: 'var(--ds-radius-sm)',
                      fontSize: 'var(--ds-text-small)',
                      zIndex: 1000,
                    }}
                  >
                    Loading Memory...
                  </div>
                )}
              </>
            )}
          </Grid>
        </Grid>
      ) : loading ? (
        <CpuMemorySkeleton sx={{ mt: ds.space.mul(0, 5) }} />
      ) : (
        <>
          <Grid
            sx={{
              ...sx,
              minHeight: clusterSummary ? ds.space.mul(0, 50) : ds.space.mul(0, 62),
              height: '100%',
              boxSizing: 'border-box',
              flexShrink: 0,
              position: 'relative',
              display: 'flex',
              flexDirection: 'column',
              alignItems: 'stretch',
              justifyContent: 'center',
              gap: 'var(--ds-space-2)',
              p: clusterSummary
                ? `${ds.space.mul(0, 7)} ${ds.space[5]} ${ds.space[2]} ${ds.space[5]}`
                : `${ds.space.mul(0, 5)} ${ds.space[3]} ${ds.space.mul(0, 3)} ${ds.space[3]}`,
              borderRadius: 'var(--ds-radius-lg)',
              border: clusterSummary ? 'none' : `1px solid ${ds.brand[100]}`,
              backgroundColor: 'var(--ds-background-100)',
              boxShadow: 'none',
              overflowWrap: 'break-word',
            }}
          >
            {/* The card is a column: a right-aligned row for the range picker, then
                the two gauges side by side. The picker used to be
                `position: absolute; top: 5px; right: 5px`, which reserved no layout
                space and so painted over the right-hand gauge's "Memory" label. */}
            <Box sx={{ display: 'flex', justifyContent: 'flex-end' }}>
              <CustomDateTimeRangePicker
                showAbsoluteRange={false}
                showOnlyCalenderIcon={false}
                passedSelectedDateTime={formattedDateRange}
                shortCuts={RANGE_SHORTCUTS}
                onChange={(dr) => handleDateRangeChange(dr.selection)}
                sx={{
                  border: '1px solid var(--ds-gray-200) !important',
                  borderRadius: 'var(--ds-radius-md) !important',
                }}
              />
            </Box>
            <Box sx={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 'var(--ds-space-5)', minWidth: 0 }}>
              <Grid item sm={6} sx={{ minWidth: 0 }}>
                <K8sMemoryCpuIndicator
                  showUpdatedUi={showUpdatedUi}
                  requiredTooltip={requiredTooltip}
                  clusterSummary={clusterSummary}
                  key='CPU'
                  unit=''
                  title='CPU'
                  queries={cpuQueries}
                  data={memoryCpuData?.cpu ?? []}
                  updatedOverview={updatedOverview}
                  hideLabels={hideLabels}
                />
              </Grid>
              <Grid item sm={6} sx={{ padding: '0px', minWidth: 0 }}>
                <K8sMemoryCpuIndicator
                  showUpdatedUi={showUpdatedUi}
                  requiredTooltip={requiredTooltip}
                  clusterSummary={clusterSummary}
                  key='Memory'
                  unit=''
                  title='Memory'
                  queries={memoryQueries}
                  data={memoryCpuData?.memory ?? []}
                  updatedOverview={updatedOverview}
                  hideLabels={hideLabels}
                />
              </Grid>
            </Box>
          </Grid>
        </>
      )}
      {trendMetric && (
        <Modal
          open
          handleClose={() => setTrendMetric(null)}
          title={TREND_CONFIG[trendMetric].title}
          width='lg'
          rightComponentOnTitle={
            <Box sx={{ mr: ds.space[4] }}>
              <CustomDateTimeRangePicker
                showAbsoluteRange={false}
                showOnlyCalenderIcon={false}
                passedSelectedDateTime={{
                  startTime: trendRange?.startDate,
                  endTime: trendRange?.endDate,
                  shortcutClickTime: trendRange?.shortcutClickTime || 0,
                }}
                shortCuts={RANGE_SHORTCUTS}
                onChange={(dr) =>
                  setTrendRange({
                    startDate: dr.selection.startTime,
                    endDate: dr.selection.endTime,
                    shortcutClickTime: dr.selection.shortcutClickTime || 0,
                  })
                }
                sx={{
                  border: '1px solid var(--ds-gray-200) !important',
                  borderRadius: 'var(--ds-radius-md) !important',
                }}
              />
            </Box>
          }
        >
          <Box sx={{ height: 360 }}>
            <Chart.Series
              id={`${trendMetric}-utilization-trend-chart`}
              labels={trendChart.labels}
              dataset={trendChart.dataset}
              loading={trendLoading}
              maxHeight={360}
              options={{
                responsive: true,
                maintainAspectRatio: false,
                interaction: { mode: 'index', intersect: false },
                plugins: { legend: { position: 'bottom' } },
                scales: {
                  x: { ticks: { maxTicksLimit: 8, autoSkip: true } },
                  y: { beginAtZero: true, title: { display: true, text: TREND_CONFIG[trendMetric].unit } },
                },
              }}
            />
          </Box>
        </Modal>
      )}
    </>
  );
};

export default KubernetesMemoryCpuOverView;

KubernetesMemoryCpuOverView.propTypes = {
  showUpdatedUi: PropTypes.bool,
  requiredTooltip: PropTypes.bool,
  clusterSummary: PropTypes.any,
  sx: PropTypes.object,
  updatedOverview: PropTypes.bool,
  showUsage: PropTypes.bool,
  hideLabels: PropTypes.bool,
  accountId: PropTypes.string,
  dateRange: PropTypes.object,
  onDateRangeChange: PropTypes.func,
};
