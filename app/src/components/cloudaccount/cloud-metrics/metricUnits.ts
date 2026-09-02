import { ds, resolveColor } from '@utils/colors';

// Unit inference + axis formatting for cloud-resource metric charts. Values are kept
// RAW in the dataset; only the Y-axis label is unit-formatted (so a 1.5 MB byte value
// stays 1_500_000 in the data and renders as "1.5 MB" — never divided into GB in the
// data, which would collapse small byte series to ~0). Shared by the Cloud Metrics tab
// and the Summary / Instances drilldown live-metrics path so both render identically.

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

export function inferMetricUnit(metricName: string): string {
  if (METRIC_UNITS[metricName]) return METRIC_UNITS[metricName];
  const name = metricName.toLowerCase();
  if (name.includes('utilization') || name.includes('percent')) return 'Percent';
  if (name.includes('throughput') || (name.includes('bytes') && name.includes('second'))) return 'Bytes/Second';
  if (name.endsWith('bytes')) return 'Bytes';
  if (name.includes('latency') || name.includes('duration')) return 'Seconds';
  if (name.endsWith('count') || name.endsWith('ops') || name.includes('iops')) return 'Count';
  return '';
}

export function formatYAxisValue(value: number, unit: string): string {
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

export function getUnitLabel(unit: string): string {
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

export function buildScaleOptions(unit: string) {
  return {
    x: {
      type: 'category' as const,
      grid: { display: false, color: resolveColor(ds.gray.alpha[300]), drawBorder: false, lineWidth: 0.2 },
      ticks: { autoSkip: true, maxTicksLimit: 4 },
    },
    y: {
      grid: { display: true, color: resolveColor(ds.gray.alpha[300]), drawBorder: false, lineWidth: 0.2 },
      ticks: {
        callback: function (value: number) {
          return unit ? formatYAxisValue(value, unit) : Number.isInteger(value) ? String(value) : value.toFixed(1);
        },
        ...(unit === 'Count' ? { precision: 0 } : {}),
      },
    },
  };
}
