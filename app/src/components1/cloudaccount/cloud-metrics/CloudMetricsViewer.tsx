import React, { useState, useCallback, useRef, useEffect } from 'react';
import { Box } from '@mui/material';
import { ListingLayout } from '@components1/ds/ListingLayout';
import WidgetCard from '@components1/ds/WidgetCard';
import { Banner } from '@components1/ds/Banner';
import { EmptyState } from '@components1/ds/EmptyState';
import { Button as DsButton } from '@components1/ds/Button';
import { Skeleton as DsSkeleton } from '@components1/ds/Skeleton';
import Charts from '@components1/common/charts/LineCharts';
// ds/DateRangePicker has known bugs (see CostChart.tsx). Same shape, swap when stable.
import CustomDateTimeRangePicker from '@components1/common/widgets/CustomDateTimeRangePicker';
import observability from '@api1/observability';
import { ds } from '@utils/colors';
import { useCloudMetricsQueryPanel, type CloudMetricsQueryParams } from './CloudMetricsQueryPanel';

interface CloudMetricsViewerProps {
  accountId: string;
  provider: 'AWS' | 'Azure' | 'GCP';
}

interface MetricChartData {
  metricName: string;
  labels: string[];
  dataset: { label: string; data: number[] }[];
  unit: string;
}

const METRIC_UNITS: Record<string, string> = {
  // Percent
  CPUUtilization: 'Percent',
  EBSIOBalance: 'Percent',
  EBSByteBalance: 'Percent',
  BurstBalance: 'Percent',
  // Bytes
  DiskReadBytes: 'Bytes',
  DiskWriteBytes: 'Bytes',
  NetworkIn: 'Bytes',
  NetworkOut: 'Bytes',
  EBSReadBytes: 'Bytes',
  EBSWriteBytes: 'Bytes',
  FreeableMemory: 'Bytes',
  FreeStorageSpace: 'Bytes',
  SwapUsage: 'Bytes',
  BinLogDiskUsage: 'Bytes',
  BucketSizeBytes: 'Bytes',
  VolumeThroughputPercentage: 'Percent',
  VolumeReadBytes: 'Bytes',
  VolumeWriteBytes: 'Bytes',
  // Count
  CPUCreditBalance: 'Count',
  CPUCreditUsage: 'Count',
  CPUSurplusCreditBalance: 'Count',
  CPUSurplusCreditsCharged: 'Count',
  DiskReadOps: 'Count',
  DiskWriteOps: 'Count',
  NetworkPacketsIn: 'Count',
  NetworkPacketsOut: 'Count',
  StatusCheckFailed: 'Count',
  StatusCheckFailed_Instance: 'Count',
  StatusCheckFailed_System: 'Count',
  EBSReadOps: 'Count',
  EBSWriteOps: 'Count',
  DatabaseConnections: 'Count',
  RequestCount: 'Count',
  HealthyHostCount: 'Count',
  UnHealthyHostCount: 'Count',
  NumberOfObjects: 'Count',
  VolumeReadOps: 'Count',
  VolumeWriteOps: 'Count',
  VolumeQueueLength: 'Count',
  // Bytes/Second
  ReadThroughput: 'Bytes/Second',
  WriteThroughput: 'Bytes/Second',
  // Count/Second
  ReadIOPS: 'Count/Second',
  WriteIOPS: 'Count/Second',
  VolumeIdleTime: 'Seconds',
  VolumeTotalReadTime: 'Seconds',
  VolumeTotalWriteTime: 'Seconds',
  // Seconds
  ReadLatency: 'Seconds',
  WriteLatency: 'Seconds',
  TargetResponseTime: 'Seconds',
  ReplicaLag: 'Seconds',
};

function inferMetricUnit(metricName: string): string {
  if (METRIC_UNITS[metricName]) return METRIC_UNITS[metricName];
  const name = metricName.toLowerCase();
  if (name.includes('utilization') || name.includes('percent')) return 'Percent';
  if (name.includes('throughput') || (name.includes('bytes') && name.includes('second'))) return 'Bytes/Second';
  if (name.endsWith('bytes')) return 'Bytes';
  if (name.includes('latency') || name.includes('duration')) return 'Seconds';
  if (name.endsWith('count') || name.endsWith('ops') || name.includes('iops')) return 'Count';
  return '';
}

function formatYAxisValue(value: number, unit: string): string {
  switch (unit) {
    case 'Percent':
      return Number.isInteger(value) ? `${value}%` : `${value.toFixed(1)}%`;
    case 'Bytes':
      if (Math.abs(value) >= 1e9) return `${(value / 1e9).toFixed(1)} GB`;
      if (Math.abs(value) >= 1e6) return `${(value / 1e6).toFixed(1)} MB`;
      if (Math.abs(value) >= 1e3) return `${(value / 1e3).toFixed(1)} KB`;
      return `${Math.round(value)} B`;
    case 'Bytes/Second':
      if (Math.abs(value) >= 1e9) return `${(value / 1e9).toFixed(1)} GB/s`;
      if (Math.abs(value) >= 1e6) return `${(value / 1e6).toFixed(1)} MB/s`;
      if (Math.abs(value) >= 1e3) return `${(value / 1e3).toFixed(1)} KB/s`;
      return `${Math.round(value)} B/s`;
    case 'Count':
      return Number.isInteger(value) ? String(value) : '';
    case 'Count/Second':
      return Number.isInteger(value) ? `${value}/s` : `${value.toFixed(1)}/s`;
    case 'Seconds':
      return `${Number.isInteger(value) ? value : value.toFixed(2)}s`;
    case 'Milliseconds':
      return `${Number.isInteger(value) ? value : value.toFixed(1)}ms`;
    default:
      return Number.isInteger(value) ? String(value) : value.toFixed(1);
  }
}

function getUnitLabel(unit: string): string {
  switch (unit) {
    case 'Percent':
      return '%';
    case 'Bytes':
      return 'Bytes';
    case 'Bytes/Second':
      return 'Bytes/s';
    case 'Count':
      return 'Count';
    case 'Count/Second':
      return 'Count/s';
    case 'Seconds':
      return 'Seconds';
    case 'Milliseconds':
      return 'ms';
    default:
      return unit;
  }
}

function buildScaleOptions(unit: string) {
  return {
    x: {
      type: 'category' as const,
      grid: { display: false, color: 'rgba(0,0,0,0.1)', drawBorder: false, lineWidth: 0.2 },
      ticks: { autoSkip: true, maxTicksLimit: 4 },
    },
    y: {
      grid: { display: true, color: 'rgba(0,0,0,0.1)', drawBorder: false, lineWidth: 0.2 },
      ticks: {
        callback: function (value: number) {
          return unit ? formatYAxisValue(value, unit) : Number.isInteger(value) ? String(value) : value.toFixed(1);
        },
        ...(unit === 'Count' ? { precision: 0 } : {}),
      },
    },
  };
}

function parseMetricResults(
  results: any[],
  setError: (msg: string | null) => void
): Record<string, { timestamps: Map<number, number>; datasets: Record<string, Map<number, number>> }> {
  const chartMap: Record<string, { timestamps: Map<number, number>; datasets: Record<string, Map<number, number>> }> = {};

  for (const result of results) {
    if (result.error) {
      setError(result.error);
      continue;
    }
    const payload = result.payload || [];
    for (const item of payload) {
      const metricName = item.metric?.name || 'unknown';
      const resourceId = item.metric?.resource_id || 'unknown';

      if (!chartMap[metricName]) {
        chartMap[metricName] = { timestamps: new Map(), datasets: {} };
      }

      if (!chartMap[metricName].datasets[resourceId]) {
        chartMap[metricName].datasets[resourceId] = new Map();
      }

      const timestamps = item.timestamps || [];
      const values = item.values || [];

      for (let i = 0; i < timestamps.length; i++) {
        chartMap[metricName].timestamps.set(timestamps[i], timestamps[i]);
        chartMap[metricName].datasets[resourceId].set(timestamps[i], values[i]);
      }
    }
  }

  return chartMap;
}

function buildChartData(
  chartMap: Record<string, { timestamps: Map<number, number>; datasets: Record<string, Map<number, number>> }>
): MetricChartData[] {
  return Object.entries(chartMap).map(([metricName, data]) => {
    const sortedTimestamps = [...data.timestamps.keys()].sort((a, b) => a - b);
    const labels = sortedTimestamps.map((ts) => new Date(ts).toLocaleString());

    const dataset = Object.entries(data.datasets).map(([resourceId, valueMap]) => ({
      label: resourceId,
      data: sortedTimestamps.map((ts) => valueMap.get(ts) ?? 0),
    }));

    const unit = inferMetricUnit(metricName);
    return { metricName, labels, dataset, unit };
  });
}

function renderCharts(charts: MetricChartData[]): React.ReactNode {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {charts.map((chart) => {
        const showUnitInTitle = chart.unit === 'Percent' || chart.unit === 'Count';
        const chartTitle = showUnitInTitle ? `${chart.metricName} (${getUnitLabel(chart.unit)})` : chart.metricName;
        const scaleOptions = buildScaleOptions(chart.unit);
        return (
          <WidgetCard key={chart.metricName}>
            <Charts chartTitle={chartTitle} dataset={chart.dataset} labels={chart.labels} data={[]} loading={false} scaleOptions={scaleOptions} />
          </WidgetCard>
        );
      })}
    </Box>
  );
}

const CloudMetricsViewer: React.FC<CloudMetricsViewerProps> = ({ accountId, provider }) => {
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [charts, setCharts] = useState<MetricChartData[]>([]);
  const [dateRange, setDateRange] = useState({
    startTime: Date.now() - 7 * 24 * 3600000,
    endTime: Date.now(),
    shortcutClickTime: 0,
  });

  const queryParamsRef = useRef<CloudMetricsQueryParams | null>(null);

  const handleQueryParamsChange = useCallback((params: CloudMetricsQueryParams) => {
    queryParamsRef.current = params;
  }, []);

  const { primaryFilters, secondaryFilters } = useCloudMetricsQueryPanel({
    provider,
    accountId,
    onChange: handleQueryParamsChange,
  });

  const fetchData = useCallback(async () => {
    const params = queryParamsRef.current;
    if (!params) {
      return;
    }

    if (!params.region) {
      setError('Please select a region');
      setCharts([]);
      return;
    }

    if (params.resourceIds.length === 0) {
      setError('Please select at least one resource');
      setCharts([]);
      return;
    }

    if (params.metricNames.length === 0) {
      setError('Please select at least one metric');
      setCharts([]);
      return;
    }

    setLoading(true);
    setError(null);

    try {
      const requestPayload = {
        account_id: accountId,
        metric_provider: 'aws_cloudwatch',
        metric_provider_source: 'user',
        queries: { A: '' },
        start_time: dateRange.startTime,
        end_time: dateRange.endTime,
        instant: false,
        request: {
          service_name: params.serviceName,
          region: params.region,
          resource_ids: params.resourceIds,
          resource_type: params.resourceType,
          metric_names: params.metricNames,
          statistics: params.statistics,
        },
      };

      const response = await observability.metricsQuery(requestPayload);
      const results = response?.data?.data?.metrics_query?.results || [];
      const chartMap = parseMetricResults(results, setError);
      const chartData = buildChartData(chartMap);

      setCharts(chartData);

      if (chartData.length === 0 && !error) {
        setError(null);
      }
    } catch (err: unknown) {
      const errorObj = err as { response?: { data?: { errors?: { message?: string }[] } }; message?: string };
      const msg = errorObj?.response?.data?.errors?.[0]?.message || errorObj?.message || 'Failed to fetch metrics';
      setError(msg);
      setCharts([]);
    } finally {
      setLoading(false);
    }
  }, [accountId, dateRange, error]);

  const handleDateRangeChange = (passedSelectedDateTime: { shortcutClickTime: number; startTime: number; endTime: number }): void => {
    if (passedSelectedDateTime.shortcutClickTime > 0) {
      setDateRange({
        startTime: Date.now() - passedSelectedDateTime.shortcutClickTime,
        endTime: Date.now(),
        shortcutClickTime: passedSelectedDateTime.shortcutClickTime,
      });
    } else {
      setDateRange({
        startTime: passedSelectedDateTime.startTime,
        endTime: passedSelectedDateTime.endTime,
        shortcutClickTime: 0,
      });
    }
  };

  useEffect(() => {
    const params = queryParamsRef.current;
    if (params?.region && params.resourceIds.length > 0 && params.metricNames.length > 0) {
      fetchData();
    }
  }, [dateRange, fetchData]);

  // No prior data: fall back to selected-metrics count, capped at 6 (spec cap).
  const skeletonCount = charts.length > 0 ? charts.length : Math.min(queryParamsRef.current?.metricNames.length ?? 3, 6);

  const renderContent = (): React.ReactNode => {
    if (loading) {
      return (
        <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
          {Array.from({ length: skeletonCount }).map((_, idx) => (
            <DsSkeleton key={idx} shape='rect' height={260} ariaLabel={`Loading metric chart ${idx + 1}`} />
          ))}
        </Box>
      );
    }

    if (charts.length > 0) {
      return renderCharts(charts);
    }

    if (!error) {
      return (
        <EmptyState
          size='inline'
          illustration='no-results'
          title='No metrics yet'
          description='Select a service, region, and resource, then click "Run Query" to fetch metrics.'
        />
      );
    }

    return null;
  };

  return (
    <ListingLayout id='cloud-metrics-viewer'>
      <ListingLayout.Toolbar
        actions={
          <>
            <DsButton id='cloud-metrics-run' tone='primary' size='md' onClick={fetchData} loading={loading} disabled={loading}>
              Run Query
            </DsButton>
            <CustomDateTimeRangePicker
              passedSelectedDateTime={dateRange}
              onChange={(result: any) => {
                const val = result?.selection ?? result;
                if (val) handleDateRangeChange(val);
              }}
            />
          </>
        }
      >
        {primaryFilters}
        {secondaryFilters}
      </ListingLayout.Toolbar>

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        {error && (
          <Box sx={{ mb: ds.space[3] }}>
            <Banner tone='critical' surface='section' message={error} />
          </Box>
        )}

        {renderContent()}
      </ListingLayout.Body>
    </ListingLayout>
  );
};

export default CloudMetricsViewer;
