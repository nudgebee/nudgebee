// Forecast SLOs (cpu_throttling, cpu_saturation, memory_saturation) are threshold SLOs
// the backend evaluates with a forecast instead of a good/bad ratio. This module holds
// what the SLO dialog and the SLO list both need to describe one: the types and the
// wording of a forecast result (the forecast_generate response and
// slo_config.last_forecast share one shape).

export interface CpuSloType {
  name: 'cpu_throttling' | 'cpu_saturation' | 'memory_saturation';
  title: string;
  description: string;
  unit: string;
  /** Measures one container, which the SLO must name. */
  containerScoped?: boolean;
}

export const CPU_SLO_TYPES: CpuSloType[] = [
  {
    name: 'cpu_throttling',
    title: 'Throttling',
    description: 'Alert before the share of throttled CPU periods reaches',
    unit: 'of CPU periods throttled',
  },
  {
    name: 'cpu_saturation',
    title: 'Saturation',
    description: 'Alert before CPU usage reaches',
    unit: 'of the CPU limit',
  },
];

/** The memory forecast SLO: one container's RSS as a share of its own memory limit. */
export const MEMORY_SLO_TYPES: CpuSloType[] = [
  {
    name: 'memory_saturation',
    title: 'Saturation',
    description: "Alert before the container's memory (RSS) reaches",
    unit: 'of its memory limit',
    containerScoped: true,
  },
];

export const FORECAST_SLO_TYPES: CpuSloType[] = [...CPU_SLO_TYPES, ...MEMORY_SLO_TYPES];

/** Every forecast alert key (events.aggregation_key), CPU and memory. */
export const FORECAST_EVENT_KEYS = [
  'CPUThrottlingPredicted',
  'CPUSaturationPredicted',
  'CPUThrottlingSLOBreached',
  'CPUSaturationSLOBreached',
  'MemorySaturationPredicted',
  'MemorySaturationSLOBreached',
];

/** What the memory SLO's help text says it cannot see. */
export const MEMORY_SLO_CAVEATS =
  'Measures each hour\'s peak RSS, since a container is OOM-killed at its peak, not its average. Page cache that the kernel can reclaim is not counted. It cannot see shared memory or tmpfs (emptyDir medium Memory, /dev/shm) or kernel memory, so such a container can be OOM-killed while this reads low; the OOMKilled alert covers that. A container whose limit VPA keeps changing may show "Not enough data".';

export const isForecastSloName = (name?: string) => FORECAST_SLO_TYPES.some((t) => t.name === String(name ?? '').toLowerCase());

export const isContainerScopedSloName = (name?: string) => MEMORY_SLO_TYPES.some((t) => t.name === String(name ?? '').toLowerCase());

/**
 * A forecast SLO whose latest forecast says it is breached. Forecast SLOs write no
 * slo_report rows, so SLO counts built from report observations must also check this,
 * or a workload whose only SLO is a breached forecast one counts as configured but
 * never firing.
 */
export const isBreachedForecastSlo = (config: { name?: string; last_forecast?: unknown }, now = Date.now()) => {
  const f = parseForecast(config?.last_forecast);
  return isForecastSloName(config?.name) && f?.status === 'breached' && !isStaleForecast(f, now);
};

const KNOWN_SIDECARS = [
  'istio-proxy',
  'linkerd-proxy',
  'envoy',
  'fluent-bit',
  'fluentd',
  'vector',
  'otel-collector',
  'oauth2-proxy',
  'cloud-sql-proxy',
];

/** The container a new memory SLO starts on: the only one, or the first that is not a known sidecar. */
export const defaultMemoryContainer = (containers: string[]): string => {
  if (containers.length <= 1) return containers[0] ?? '';
  return containers.find((c) => !KNOWN_SIDECARS.includes(c)) ?? containers[0];
};

/** The containers of a workload's spec that set a memory limit (k8s_workloads meta). */
export const memoryLimitedContainers = (meta: unknown): string[] => {
  let m: any = meta;
  if (typeof m === 'string') {
    try {
      m = JSON.parse(m);
    } catch {
      return [];
    }
  }
  const containers = Array.isArray(m?.config?.containers) ? m.config.containers : [];
  return containers.filter((c: any) => c?.name && c?.resources?.limits?.memory).map((c: any) => String(c.name));
};

export interface ForecastDailyRow {
  date: string;
  value_pct: number;
  per_pod_usage?: number;
  per_pod_limit?: number;
  projected?: boolean;
}

export interface ForecastResult {
  status: 'projected' | 'not_projected' | 'breached' | 'insufficient_history' | 'no_limit' | 'provider_unsupported' | 'error';
  reason?: string;
  condition?: string;
  threshold_pct?: number;
  current_pct?: number;
  slope_pct_per_day?: number;
  eta_hours?: number;
  level?: 'HIGH' | 'MEDIUM' | 'BREACHED';
  per_pod_limit?: number;
  suggested_limit?: number;
  /** Unit of every limit and usage figure, e.g. "cores" or "MiB". */
  limit_unit?: string;
  /** The container a memory forecast measured. */
  container?: string;
  /** Restarts in the last 6h whose last exit was OOMKilled (memory only; evidence). */
  oom_restarts_6h?: number;
  daily?: ForecastDailyRow[];
  evaluated_at?: string;
}

/** last_forecast arrives as a JSON string from slo_config_v2 and as an object from the RPC. */
export const parseForecast = (raw: unknown): ForecastResult | null => {
  if (!raw) return null;
  if (typeof raw === 'object') return raw as ForecastResult;
  try {
    const parsed = JSON.parse(String(raw));
    return parsed && typeof parsed === 'object' ? (parsed as ForecastResult) : null;
  } catch {
    return null;
  }
};

// The hourly SLO run rewrites last_forecast every time. A stored result older than one
// missed run (a deploy killed the run, the run is failing, the SLO is disabled or the
// cluster is disconnected) no longer describes the workload, so it is shown as out of
// date instead of as a live status, and never counts as a breach. Two hours, not just
// over one: a run evaluates every account in turn, so one that is merely slow must not
// hide a current breach.
const STALE_AFTER_MS = 2 * 60 * 60 * 1000;

/** True when a stored forecast is older than a missed hourly run. */
export const isStaleForecast = (f: ForecastResult | null, now = Date.now()) => {
  const at = Date.parse(f?.evaluated_at ?? '');
  return Number.isFinite(at) && now - at > STALE_AFTER_MS;
};

const ageHours = (f: ForecastResult, now: number) => (now - Date.parse(f.evaluated_at ?? '')) / 3_600_000;

/** A limit or usage figure: cores as millicores ("350m"), the way Kubernetes writes them; any other unit as "<value> <unit>". */
export const quantity = (v: number, unit?: string) => (unit && unit !== 'cores' ? `${Math.round(v * 10) / 10} ${unit}` : `${Math.round(v * 1000)}m`);

const num = (v?: number) => `${Math.round((v ?? 0) * 10) / 10}`;
const pct = (v?: number) => `${num(v)}%`;

export const humanHours = (h: number) => (h < 48 ? `${Math.max(Math.round(h), 1)}h` : `${Math.round((h / 24) * 10) / 10} days`);

export type ForecastTone = 'neutral' | 'info' | 'success' | 'warning' | 'critical';

/** A short status for a Label, plus its tone. */
export const forecastStatus = (f: ForecastResult | null, now = Date.now()): { text: string; tone: ForecastTone } => {
  if (isStaleForecast(f, now)) return { text: 'Out of date', tone: 'neutral' };
  switch (f?.status) {
    case 'projected':
      return { text: 'Breach projected', tone: f.level === 'HIGH' ? 'critical' : 'warning' };
    case 'breached':
      return { text: 'Breached', tone: 'critical' };
    case 'not_projected':
      return { text: 'OK', tone: 'success' };
    case 'insufficient_history':
      return { text: 'Not enough data', tone: 'neutral' };
    case 'no_limit':
      return { text: 'No limit', tone: 'neutral' };
    case 'provider_unsupported':
      return { text: 'Unsupported', tone: 'neutral' };
    case 'error':
      return { text: 'Error', tone: 'critical' };
    default:
      return { text: 'Not evaluated yet', tone: 'neutral' };
  }
};

const trend = (slope?: number) => {
  if (!slope) return 'flat';
  return slope > 0 ? `rising ${num(slope)} pp/day` : `falling ${num(-slope)} pp/day`;
};

/** One sentence describing the result, e.g. "Reaches 75% in ~35h (now 37.3%, rising 25.6 pp/day)". */
export const forecastSummary = (f: ForecastResult | null, now = Date.now()): string => {
  if (!f) return 'Evaluated hourly once saved';
  if (isStaleForecast(f, now)) return `Last evaluated ${humanHours(ageHours(f, now))} ago`;
  switch (f.status) {
    case 'projected':
      return `Reaches ${pct(f.threshold_pct)} in ~${humanHours(f.eta_hours ?? 0)} (now ${pct(f.current_pct)}, ${trend(f.slope_pct_per_day)})`;
    case 'breached':
      return `Above ${pct(f.threshold_pct)}: now ${pct(f.current_pct)}, ${trend(f.slope_pct_per_day)}`;
    case 'not_projected':
      return `No breach projected (now ${pct(f.current_pct)}, ${trend(f.slope_pct_per_day)})`;
    default:
      return f.reason || forecastStatus(f).text;
  }
};

/** The day-by-day climb in one line, projected days marked. */
export const forecastDailyLine = (f: ForecastResult | null): string =>
  (f?.daily ?? [])
    .map((d) => {
      const usage = d.per_pod_usage ? `, ${quantity(d.per_pod_usage, f?.limit_unit)}` : '';
      return `${String(d.date ?? '').slice(5)}: ${pct(d.value_pct)}${usage}${d.projected ? ' (projected)' : ''}`;
    })
    .join(' · ');
