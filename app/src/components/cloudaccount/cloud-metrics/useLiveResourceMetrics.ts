import { useEffect, useState } from 'react';
import observability from '@api1/observability';
import { inferMetricUnit } from './metricUnits';

// Chart-ready shape consumed by Chart.Line on the cloud-account Summary /
// Instances drilldown metrics panels. `unit` drives the Y-axis formatter
// (buildScaleOptions) — values stay raw so bytes/count aren't crushed.
export interface LiveMetricChart {
  labels: string[];
  datasets: { label: string; data: (number | null)[] }[];
  unit: string;
}

export interface MetricResourceRef {
  resourse_id: string;
  region?: string;
  type?: string;
  name?: string;
}

interface UseLiveResourceMetricsArgs {
  accountId: string;
  serviceName: string;
  resources: MetricResourceRef[];
  startDate: number; // epoch ms
  endDate: number; // epoch ms
}

interface UseLiveResourceMetricsResult {
  dataByMetric: Record<string, LiveMetricChart>;
  loading: boolean;
  error: string | null;
}

// The provider aligns the series into fixed buckets and returns one point per bucket.
// Left unbounded (the backend default is 60s) a 7-day range yields ~10k points per
// series — heavy to transfer + render and prone to the collector's 30s timeout (the
// stored ETL uses a 1-hour bucket for exactly this reason). Bound the bucket to the
// range so the point count stays ~constant regardless of span: fine detail on short
// ranges, coarser averages on long ones. Returned as the Go time.Duration
// (nanoseconds) that the backend's `step` field expects.
const TARGET_POINTS = 400;
const MIN_STEP_SECONDS = 60; // GCP's native resolution for utilization; finer gains nothing
function stepNanosForRange(startMs: number, endMs: number): number {
  const rangeSeconds = Math.max(1, Math.floor((endMs - startMs) / 1000));
  const stepSeconds = Math.max(MIN_STEP_SECONDS, Math.floor(rangeSeconds / TARGET_POINTS));
  return stepSeconds * 1_000_000_000;
}

// Turns the metrics_list response into per-metric, per-resource chart series. Values
// are kept RAW (the axis formatter handles B/KB/MB/GB, Count, %) — never divided into
// GB in the data, which collapses small byte series to ~0. The one transform: GCP
// reports utilization as a 0–1 fraction (AWS/Azure already report 0–100 percent), so
// GCP Percent metrics are scaled ×100. Keyed off the provider — NOT value magnitude:
// an idle AWS instance whose percent samples all sit below 1 must not become ×100.
function buildChartsFromResults(results: any[], idToName: Record<string, string>, isGcp: boolean): Record<string, LiveMetricChart> {
  const acc: Record<string, { timestamps: Set<number>; byResource: Record<string, Map<number, number>> }> = {};

  for (const result of results) {
    if (result?.error) continue;
    for (const item of result?.payload || []) {
      const metricName = item.metric?.name || 'unknown';
      const resourceId = item.metric?.resource_id || 'unknown';
      if (!acc[metricName]) acc[metricName] = { timestamps: new Set(), byResource: {} };
      if (!acc[metricName].byResource[resourceId]) acc[metricName].byResource[resourceId] = new Map();

      const timestamps: number[] = item.timestamps || [];
      const values: number[] = item.values || [];
      for (let i = 0; i < timestamps.length; i++) {
        acc[metricName].timestamps.add(timestamps[i]);
        acc[metricName].byResource[resourceId].set(timestamps[i], values[i]);
      }
    }
  }

  const out: Record<string, LiveMetricChart> = {};
  for (const [metricName, m] of Object.entries(acc)) {
    // Skip metrics with no data points at all — the provider auto-detects a curated
    // set per service, but some don't apply to every resource (e.g. EC2 instance-store
    // DiskReadOps is empty for EBS-only instances). Drop those empty cards.
    if (m.timestamps.size === 0) continue;
    const unit = inferMetricUnit(metricName);
    const isFraction = isGcp && unit === 'Percent';
    const sortedTs = [...m.timestamps].sort((a, b) => a - b);
    const resourceKeys = Object.keys(m.byResource);
    out[metricName] = {
      unit,
      labels: sortedTs.map((ts) => new Date(ts).toLocaleString()),
      datasets: resourceKeys.map((resourceId) => ({
        // The provider labels compute series by numeric instance_id; map back to the
        // human resource name when we have it (fall back to the id otherwise).
        label: idToName[resourceId] || resourceId,
        data: sortedTs.map((ts) => {
          const v = m.byResource[resourceId].get(ts);
          if (v === undefined) return null;
          return isFraction ? Math.round(v * 10000) / 100 : v;
        }),
      })),
    };
  }
  return out;
}

/**
 * Fetches resource metrics live from the provider (CloudWatch / GCP Monitoring /
 * Azure Monitor) via the metrics_list action — the same path the Cloud Metrics tab
 * uses. Empty `metric_names`/`statistics` let the backend auto-detect each service's
 * curated metric set with its default statistic, i.e. the same set the stored path
 * showed, but always fresh for the selected range (no collector lag / stale window).
 */
export function useLiveResourceMetrics({
  accountId,
  serviceName,
  resources,
  startDate,
  endDate,
}: UseLiveResourceMetricsArgs): UseLiveResourceMetricsResult {
  const [dataByMetric, setDataByMetric] = useState<Record<string, LiveMetricChart>>({});
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  // Stable dep: identity of the resource set (id + region + type).
  const resourcesKey = resources.map((r) => `${r.resourse_id}|${r.region || ''}|${r.type || ''}`).join(',');

  useEffect(() => {
    if (!accountId || !serviceName || resources.length === 0) {
      setDataByMetric({});
      return;
    }
    let cancelled = false;
    const run = async () => {
      setLoading(true);
      setError(null);
      try {
        // The provider query takes a single region + resource_type + a list of ids, so
        // fan out one call per region+type group and merge — a service can span regions,
        // and a region can hold >1 resource type, so grouping by both keeps each call
        // homogeneous (correct resource_type for every id). GCP ignores region when ids
        // are given; AWS/Azure need it.
        const byRegionType = new Map<string, MetricResourceRef[]>();
        for (const r of resources) {
          const key = `${r.region || ''}|${r.type || ''}`;
          if (!byRegionType.has(key)) byRegionType.set(key, []);
          byRegionType.get(key)!.push(r);
        }
        const step = stepNanosForRange(startDate, endDate);
        // allSettled, not all: one group failing (throttle / perm) must not blank the rest.
        const settled = await Promise.allSettled(
          [...byRegionType.entries()].map(([key, group]) => {
            const [region, type] = key.split('|');
            return observability.metricsQuery({
              account_id: accountId,
              metric_provider: 'aws_cloudwatch',
              metric_provider_source: 'user',
              queries: { A: '' },
              start_time: startDate,
              end_time: endDate,
              instant: false,
              request: {
                service_name: serviceName,
                region,
                resource_ids: group.map((r) => r.resourse_id),
                resource_type: type,
                metric_names: [],
                statistics: [],
                step,
              },
            });
          })
        );
        if (cancelled) return;
        const idToName: Record<string, string> = {};
        for (const r of resources) if (r.name) idToName[r.resourse_id] = r.name;
        // GCP resource types carry the API host (e.g. "compute.googleapis.com/Instance");
        // AWS/Azure do not. Used to decide utilization fraction→percent scaling.
        const isGcp = resources.some((r) => /(?:^|\.)googleapis\.com\//.test(r.type || ''));
        const results = settled.flatMap((s) => (s.status === 'fulfilled' ? s.value?.data?.data?.metrics_list?.results || [] : []));
        setDataByMetric(buildChartsFromResults(results, idToName, isGcp));
        if (results.length === 0 && settled.every((s) => s.status === 'rejected')) {
          setError('Failed to fetch metrics');
        }
      } catch (err: unknown) {
        if (cancelled) return;
        const e = err as { response?: { data?: { errors?: { message?: string }[] } }; message?: string };
        setError(e?.response?.data?.errors?.[0]?.message || e?.message || 'Failed to fetch metrics');
        setDataByMetric({});
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    run();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId, serviceName, resourcesKey, startDate, endDate]);

  return { dataByMetric, loading, error };
}
