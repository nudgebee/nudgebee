import React, { useState, useCallback, useRef, useEffect } from 'react';
import { Box, Typography } from '@mui/material';
import { ListingLayout } from '@ui/ListingLayout';
import MetricQueryInfo from '@shared/MetricQueryInfo';
import { Card } from '@ui/Card';
import { Banner } from '@ui/Banner';
import { EmptyState } from '@ui/EmptyState';
import { Button as DsButton } from '@ui/Button';
import { Skeleton as DsSkeleton } from '@ui/Skeleton';
import Chart from '@ui/Chart';
import CustomDateTimeRangePicker from '@shared/widgets/CustomDateTimeRangePicker';
import observability from '@api1/observability';
import { ds } from '@utils/colors';
import { formatMetricName } from '@utils/common';
import { useCloudMetricsQueryPanel, type CloudMetricsQueryParams } from './CloudMetricsQueryPanel';
import { inferMetricUnit, getUnitLabel, buildScaleOptions } from './metricUnits';
import { DropdownMenu } from '@ui/DropdownMenu';
import { KeyboardArrowDown as KeyboardArrowDownIcon } from '@mui/icons-material';
import CloudProviderIcon from '@shared/icons/CloudIcon';
import QueryModeSwitcher from '@components/k8s/common/QueryModeSwitcher';
import { OperatorDescriptor } from '@components/k8s/common/operatorCatalog';
import apiAccount from '@api1/account';
import { snakeToTitleCase } from 'src/utils/common';
import { buildSeriesChart } from '@utils/metricSeriesChart';

// The cloud-native metrics path is a synthetic provider (`aws_cloudwatch`) the
// backend dispatches by cloud account type. It is filtered out of
// available_providers below and re-added by hand so it is always the default.
const NATIVE_PROVIDER = 'aws_cloudwatch';

const NATIVE_LABEL: Record<string, string> = {
  AWS: 'CloudWatch',
  Azure: 'Azure Monitor',
  GCP: 'Cloud Monitoring',
};

interface MetricProviderOption {
  provider: string;
  label: string;
  iconKey: string;
  operatorDescriptors?: OperatorDescriptor[];
}

interface CloudMetricsViewerProps {
  accountId: string;
  provider: 'AWS' | 'Azure' | 'GCP';
}

interface MetricChartData {
  metricName: string;
  labels: string[];
  dataset: { label: string; data: number[] }[];
  unit: string;
  /** The executed provider query that produced this chart, when the backend returns one. */
  query?: string;
}

interface ChartMapEntry {
  timestamps: Map<number, number>;
  datasets: Record<string, Map<number, number>>;
  query?: string;
}

// The GraphQL gateway answers HTTP 200 with a top-level `errors` array for most
// backend failures (a cloud-collector timeout included), so a caller that reads
// `data.data...` straight through sees empty results and no error — which is why
// a timed-out query rendered "No metrics yet" with no hint. Pull that top-level
// message out so it can be surfaced.
function topLevelErrorMessage(response: any): string | null {
  return response?.data?.errors?.[0]?.message || null;
}

// The backend surfaces a request timeout as a raw Go string
// (`... context deadline exceeded`). Turn it into a hint the user can act on.
function friendlyMetricsError(msg: string): string {
  if (/context deadline exceeded|deadline exceeded|timeout/i.test(msg)) {
    return 'The metrics query timed out. Try selecting fewer metrics or a shorter time range.';
  }
  return msg;
}

function parseMetricResults(results: any[], setError: (msg: string | null) => void): Record<string, ChartMapEntry> {
  const chartMap: Record<string, ChartMapEntry> = {};

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
      if (result.query) {
        chartMap[metricName].query = result.query;
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

/**
 * Charts for a SaaS provider result. One chart per query key, series labelled
 * from the metric's label map.
 *
 * Unit inference is deliberately skipped: `inferMetricUnit` reads a CloudWatch
 * metric NAME (`CPUUtilization`, `BytesDownloaded`), and a free-form query has
 * none — feeding it a PromQL expression would guess a unit off an arbitrary
 * substring and mis-scale the axis. An empty unit renders a plain numeric axis,
 * which is the honest default.
 */
function buildSaasCharts(results: any[]): MetricChartData[] {
  const charts: MetricChartData[] = [];
  results.forEach((result: any, idx: number) => {
    const chart = buildSeriesChart(result?.payload);
    if (chart.dataset.length === 0) {
      return;
    }
    charts.push({
      // The query is the only name a free-form result has.
      metricName: result?.query || result?.query_key || `Query ${idx + 1}`,
      labels: chart.labels.map((ts) => new Date(Number(ts) * 1000).toLocaleString()),
      dataset: chart.dataset,
      unit: '',
      query: result?.query,
    });
  });
  return charts;
}

function buildChartData(chartMap: Record<string, ChartMapEntry>, isGcp: boolean): MetricChartData[] {
  return Object.entries(chartMap).map(([metricName, data]) => {
    const sortedTimestamps = [...data.timestamps.keys()].sort((a, b) => a - b);
    const labels = sortedTimestamps.map((ts) => new Date(ts).toLocaleString());

    // GCP reports utilization as a 0–1 fraction while AWS/Azure already report 0–100
    // percent, so GCP Percent metrics are scaled ×100. Keyed off the provider, not the
    // value magnitude. Mirrors useLiveResourceMetrics.ts.
    const unit = inferMetricUnit(metricName);
    const isFraction = isGcp && unit === 'Percent';

    const dataset = Object.entries(data.datasets).map(([resourceId, valueMap]) => ({
      label: resourceId,
      data: sortedTimestamps.map((ts) => {
        const v = valueMap.get(ts) ?? 0;
        return isFraction ? Math.round(v * 10000) / 100 : v;
      }),
    }));

    return { metricName, labels, dataset, unit, query: data.query };
  });
}

function renderCharts(charts: MetricChartData[]): React.ReactNode {
  return (
    <Box sx={{ display: 'flex', flexDirection: 'column', gap: ds.space[3] }}>
      {charts.map((chart) => {
        const showUnitInTitle = chart.unit === 'Percent' || chart.unit === 'Count';
        // Raw `chart.metricName` comes straight from the cloud provider's API in
        // its native casing — `BytesDownloaded`, `4xxErrorRate`, `stored_bytes`,
        // etc. `formatMetricName` normalises both camelCase and snake_case into
        // a human-readable title, and special-cases known compound words and
        // acronyms (CPU, IOPS) so they don't get over-split.
        // A free-form query is already human-written; formatMetricName would
        // split it on camelCase/underscores and mangle it.
        const displayName = chart.unit === '' && /[\s{}()\[\]:"]/.test(chart.metricName) ? chart.metricName : formatMetricName(chart.metricName);
        const chartTitle = showUnitInTitle ? `${displayName} (${getUnitLabel(chart.unit)})` : displayName;
        const scaleOptions = buildScaleOptions(chart.unit);
        return (
          <Card key={chart.metricName} sx={{ mt: 'var(--ds-space-5)', padding: 'var(--ds-space-4) var(--ds-space-5)' }}>
            <Box sx={{ display: 'flex', alignItems: 'center', gap: ds.space[2], mb: ds.space[2] }}>
              <Typography
                sx={{
                  fontSize: ds.text.small,
                  fontWeight: ds.weight.medium,
                  color: ds.brand[500],
                  wordBreak: 'break-all',
                  lineHeight: '1.3',
                }}
              >
                {chartTitle}
              </Typography>
              <MetricQueryInfo queries={chart.query ? { Query: chart.query } : null} />
            </Box>
            <Chart.Line dataset={chart.dataset} labels={chart.labels} data={[]} loading={false} scaleOptions={scaleOptions} />
          </Card>
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

  // Provider switcher: the native cloud-metrics option plus any SaaS metrics
  // providers attached to this cloud account. The backend already drops
  // providers that cannot serve metrics, so no filtering is needed here.
  const [providers, setProviders] = useState<MetricProviderOption[]>([]);
  const [selectedProvider, setSelectedProvider] = useState<string>(NATIVE_PROVIDER);
  // SaaS query state — only used when a non-native provider is selected.
  const [saasQuery, setSaasQuery] = useState('');
  const [saasQLEditor, setSaasQLEditor] = useState('code');
  const [saasQueryItems, setSaasQueryItems] = useState<any[]>([]);
  const [saasEsIndex, setSaasEsIndex] = useState('');
  const [saasEsQueryType, setSaasEsQueryType] = useState('dsl');

  const isNative = selectedProvider === NATIVE_PROVIDER;
  const selectedOption = providers.find((p) => p.provider === selectedProvider);

  const queryParamsRef = useRef<CloudMetricsQueryParams | null>(null);

  const handleQueryParamsChange = useCallback((params: CloudMetricsQueryParams) => {
    queryParamsRef.current = params;
  }, []);

  const { primaryFilters, secondaryFilters } = useCloudMetricsQueryPanel({
    provider,
    accountId,
    onChange: handleQueryParamsChange,
  });

  const resetSaasQueryState = useCallback(() => {
    setSaasQuery('');
    setSaasQueryItems([]);
    setSaasEsIndex('');
    setSaasEsQueryType('dsl');
  }, []);

  // The native option is synthesised: aws_cloudwatch is dispatched by cloud
  // account type rather than configured as an integration, so it is not in
  // available_providers. A getDefaultProvider failure falls back to native-only
  // — the switcher is auxiliary and must not break the existing screen.
  useEffect(() => {
    const nativeOption: MetricProviderOption = { provider: NATIVE_PROVIDER, label: NATIVE_LABEL[provider] || 'Cloud Metrics', iconKey: provider };
    // Always land on the native provider for a new account: the one selected for
    // the previous account may not be attached to this one, which would leave the
    // SaaS editor open against a provider absent from the menu.
    setSelectedProvider(NATIVE_PROVIDER);
    resetSaasQueryState();
    setCharts([]);
    setError(null);
    if (!accountId || accountId === 'demo') {
      setProviders([nativeOption]);
      return;
    }

    let cancelled = false;
    const loadProviders = async () => {
      try {
        const res = await apiAccount.getDefaultProvider({ account_id: accountId, provider_type: 'metrics' });
        const obs = res?.data?.data?.observability_get_default_provider;
        const available = Array.isArray(obs?.available_providers) ? obs.available_providers : [];
        const saasOptions: MetricProviderOption[] = available
          .filter((p: any) => p?.provider && p.provider !== NATIVE_PROVIDER)
          .map((p: any) => ({
            provider: p.provider,
            label: snakeToTitleCase(p.provider),
            iconKey: p.provider,
            operatorDescriptors: p.supported_operator_descriptors,
          }));
        if (!cancelled) {
          setProviders([nativeOption, ...saasOptions]);
        }
      } catch {
        if (!cancelled) {
          setProviders([nativeOption]);
        }
      }
    };
    loadProviders();
    return () => {
      cancelled = true;
    };
  }, [accountId, provider, resetSaasQueryState]);

  // Switching provider is a fresh start: the native guided selection and a SaaS
  // query describe different things, so neither survives the switch.
  const handleProviderChange = (next: string) => {
    if (!next || next === selectedProvider) {
      return;
    }
    setSelectedProvider(next);
    setCharts([]);
    setError(null);
    resetSaasQueryState();
  };

  const fetchData = useCallback(async () => {
    // SaaS provider: a free-form query against the attached provider. The guided
    // service/region/resource selection is CloudWatch-shaped and means nothing
    // here, so none of it is sent and none of it is validated.
    if (!isNative) {
      if (!saasQuery.trim()) {
        setError('Enter a query to run.');
        setCharts([]);
        return;
      }
      setLoading(true);
      setError(null);
      try {
        const response = await observability.metricsQuery({
          account_id: accountId,
          metric_provider: selectedProvider,
          metric_provider_source: 'user',
          queries: { A: saasQuery },
          start_time: dateRange.startTime,
          end_time: dateRange.endTime,
          instant: false,
          ...(selectedProvider === 'ES' && saasEsIndex ? { request: { index: saasEsIndex, query_type: saasEsQueryType } } : {}),
        });
        const topLevelError = topLevelErrorMessage(response);
        if (topLevelError) {
          setError(friendlyMetricsError(topLevelError));
          setCharts([]);
          return;
        }
        const results = response?.data?.data?.metrics_list?.results || [];
        const failed = results.find((r: any) => r?.error);
        if (failed) {
          setError(failed.error);
          setCharts([]);
          return;
        }
        // Every non-CloudWatch provider returns label-map series rather than the
        // name/resource_id shape parseMetricResults expects, so the shared
        // series builder renders them instead.
        setCharts(buildSaasCharts(results));
      } catch (err: unknown) {
        const errorObj = err as { response?: { data?: { errors?: { message?: string }[] } }; message?: string };
        setError(errorObj?.response?.data?.errors?.[0]?.message || errorObj?.message || 'Failed to fetch metrics');
        setCharts([]);
      } finally {
        setLoading(false);
      }
      return;
    }

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
      const topLevelError = topLevelErrorMessage(response);
      if (topLevelError) {
        setError(friendlyMetricsError(topLevelError));
        setCharts([]);
        return;
      }
      const results = response?.data?.data?.metrics_list?.results || [];
      const chartMap = parseMetricResults(results, setError);
      const chartData = buildChartData(chartMap, provider === 'GCP');

      setCharts(chartData);
    } catch (err: unknown) {
      const errorObj = err as { response?: { data?: { errors?: { message?: string }[] } }; message?: string };
      const msg = errorObj?.response?.data?.errors?.[0]?.message || errorObj?.message || 'Failed to fetch metrics';
      setError(msg);
      setCharts([]);
    } finally {
      setLoading(false);
    }
    // `error` is deliberately NOT a dependency. Every run sets it to null and a
    // failing one sets it back to a message — two real state changes, so listing
    // it here churns this callback's identity, which re-fires the effect below
    // that calls it, which fails again: an endless retry on any query error.
  }, [accountId, dateRange, isNative, provider, selectedProvider, saasQuery, saasEsIndex, saasEsQueryType]);

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
          description={
            isNative
              ? 'Select a service, region, and resource, then click "Run Query" to fetch metrics.'
              : 'Build or write a query, then click "Run Query" to fetch metrics.'
          }
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
        {providers.length > 1 && (
          <DropdownMenu
            align='start'
            minWidth={200}
            trigger={
              <Box
                component='button'
                type='button'
                id='cloud-metrics-provider-switcher'
                sx={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: ds.space[2],
                  padding: `${ds.space[1]} ${ds.space[3]}`,
                  backgroundColor: 'var(--ds-gray-alpha-100)',
                  borderRadius: 'var(--ds-radius-md)',
                  border: '1px solid var(--ds-gray-alpha-200)',
                  cursor: 'pointer',
                  font: 'inherit',
                  whiteSpace: 'nowrap',
                  '&:hover': { borderColor: 'var(--ds-gray-300)', backgroundColor: 'var(--ds-gray-alpha-200)' },
                }}
              >
                <Typography sx={{ fontSize: ds.text.small, color: ds.gray[600] }}>Metrics Provider:</Typography>
                <CloudProviderIcon cloud_provider={selectedOption?.iconKey || selectedProvider} width='18px' height='18px' />
                <Typography sx={{ fontSize: ds.text.small, fontWeight: ds.weight.semibold, color: ds.gray[700] }}>
                  {selectedOption?.label || snakeToTitleCase(selectedProvider)}
                </Typography>
                <KeyboardArrowDownIcon sx={{ fontSize: 18, color: ds.gray[500] }} />
              </Box>
            }
            items={providers.map((p) => ({
              id: `cloud-metric-provider-${p.provider}`,
              label: p.label,
              icon: <CloudProviderIcon cloud_provider={p.iconKey} width='16px' height='16px' />,
              kbd: p.provider === selectedProvider ? '\u2713' : undefined,
              onSelect: () => handleProviderChange(p.provider),
            }))}
          />
        )}
        {isNative && primaryFilters}
        {isNative && secondaryFilters}
      </ListingLayout.Toolbar>

      <ListingLayout.Body padding={`${ds.space[3]} ${ds.space[5]}`}>
        {!isNative && (
          <Box sx={{ mb: ds.space[3] }}>
            <QueryModeSwitcher
              accountId={accountId}
              logProvider={selectedProvider}
              providerOverride={selectedProvider}
              operatorDescriptors={selectedOption?.operatorDescriptors}
              params={{ startTime: dateRange.startTime, endTime: dateRange.endTime }}
              queryItems={saasQueryItems}
              setQueryItems={setSaasQueryItems}
              onQueryChange={(e: any) => {
                setSaasQuery(e.query);
                if (e.index !== undefined) setSaasEsIndex(e.index);
                if (e.queryType !== undefined) setSaasEsQueryType(e.queryType);
              }}
              qLEditor={saasQLEditor}
              setQLEditor={setSaasQLEditor}
              allowMultipleQueries={false}
              providerType='metrics'
              initialEsIndex={saasEsIndex}
            />
          </Box>
        )}

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
