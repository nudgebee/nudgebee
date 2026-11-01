/**
 * The unit a PromQL query's values are in, read off Prometheus metric-naming
 * conventions (base units in the name: `_bytes`, `_seconds`, `_ratio`, and
 * `_total` for counters), and the formatting that goes with it.
 *
 * Deliberately conservative: a wrong unit on a chart is worse than none, so
 * anything that could change what the number means — `*`, `/`, `%`, `^`,
 * `bool`, a literal added to a series, a function not listed here, mixed units —
 * answers `null`, and the chart shows plain numbers.
 */

export type MetricUnit = 'bytes' | 'bytes/s' | 'cores' | 'seconds' | 'ratio' | 'percent' | 'per_second' | 'count';

/** Functions whose result is in the unit of their argument. */
const UNIT_PRESERVING = new Set([
  'sum',
  'avg',
  'min',
  'max',
  'topk',
  'bottomk',
  'quantile',
  'stddev',
  'sort',
  'sort_desc',
  'abs',
  'ceil',
  'floor',
  'round',
  'clamp',
  'clamp_min',
  'clamp_max',
  'avg_over_time',
  'min_over_time',
  'max_over_time',
  'quantile_over_time',
  'stddev_over_time',
  'last_over_time',
  'label_replace',
  'label_join',
  'predict_linear',
  'holt_winters',
  'double_exponential_smoothing',
  'scalar',
  'vector',
]);
const PER_SECOND = new Set(['rate', 'irate']);
const KNOWN_FUNCTIONS = new Set([...UNIT_PRESERVING, ...PER_SECOND, 'increase', 'histogram_quantile']);
/** Identifiers that are not metric names. */
const KEYWORDS = new Set(['and', 'or', 'unless', 'by', 'without', 'on', 'ignoring', 'group_left', 'group_right', 'offset', 'inf', 'nan']);

// Epoch timestamps are named `_seconds` too (process_start_time_seconds,
// node_boot_time_seconds) but are not durations.
const isTimestamp = (name: string) => /timestamp|_time_seconds$/.test(name);

/** A gauge's unit from its name. */
function gaugeUnit(name: string): MetricUnit | null {
  if (name.endsWith('_bytes')) return 'bytes';
  if (name.endsWith('_seconds') && !isTimestamp(name)) return 'seconds';
  if (name.endsWith('_ratio')) return 'ratio';
  if (/_(percent|percentage)$/.test(name)) return 'percent';
  return null;
}

/** A counter's unit once `rate`/`irate` makes it per second. */
function rateUnit(name: string): MetricUnit | null {
  if (name.endsWith('_bytes_total')) return 'bytes/s';
  // CPU time used per second is cores. Other seconds-per-second counters (CPU
  // throttling, disk busy time, GC time) mean something different per metric.
  if (/cpu_(usage_|user_|system_)?seconds_total$/.test(name)) return 'cores';
  if (name.endsWith('_seconds_total')) return null;
  if (/_(total|count)$/.test(name)) return 'per_second';
  return null;
}

/** A counter's unit under `increase`. */
function increaseUnit(name: string): MetricUnit | null {
  if (name.endsWith('_bytes_total')) return 'bytes';
  if (name.endsWith('_seconds_total')) return 'seconds';
  if (/_(total|count)$/.test(name)) return 'count';
  return null;
}

/** The unit `query`'s values are in, or null when it cannot be read with confidence. */
export function inferPromqlUnit(query: unknown): MetricUnit | null {
  if (typeof query !== 'string' || !query.trim()) return null;
  const expr = query
    .replace(/#.*$/gm, '')
    .replace(/"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'|`[^`]*`/g, '""')
    .replace(/\{[^}]*\}/g, '')
    .replace(/\[[^\]]*\]/g, '')
    .replace(/\b(?:by|without|on|ignoring|group_left|group_right)\s*\([^)]*\)/gi, '')
    .replace(/\boffset\s+-?[\w.]+/gi, '')
    .replace(/@\s*(?:start\(\s*\)|end\(\s*\)|[\d.]+)/g, '');

  if (/[*/%^]|\bbool\b|\batan2\b/i.test(expr)) return null;
  // `1 - x` or `x + 5`: a literal combined with a series usually means a different quantity.
  if (/(^|[^\w:.])\d[\d.]*(?:e[+-]?\d+)?\s*[-+]|[-+]\s*\.?\d/i.test(expr)) return null;

  const metrics: string[] = [];
  const functions: string[] = [];
  for (const match of expr.matchAll(/(\d[\d.]*(?:e[+-]?\d+)?[a-z]*)|([a-z_:][\w:]*)(\s*\()?/gi)) {
    const name = match[2];
    if (!name) continue;
    if (match[3]) functions.push(name.toLowerCase());
    else if (!KEYWORDS.has(name.toLowerCase())) metrics.push(name);
  }
  if (metrics.length === 0 || functions.some((f) => !KNOWN_FUNCTIONS.has(f))) return null;

  let units: (MetricUnit | null)[];
  if (functions.includes('histogram_quantile')) {
    units = metrics.map((name) => (name.endsWith('_bucket') ? gaugeUnit(name.slice(0, -'_bucket'.length)) : null));
  } else {
    const perSecond = functions.filter((f) => PER_SECOND.has(f)).length;
    const increases = functions.filter((f) => f === 'increase').length;
    if (perSecond && increases) return null;
    if (perSecond || increases) {
      // Every series must sit under one: `rate(a_total[5m]) + b_total` mixes units.
      if (perSecond + increases !== metrics.length) return null;
      units = metrics.map(perSecond ? rateUnit : increaseUnit);
    } else {
      units = metrics.map(gaugeUnit);
    }
  }
  const [first] = units;
  return first && units.every((unit) => unit === first) ? first : null;
}

interface UnitScale {
  /** Multiplies a raw value into the display unit. */
  factor: number;
  suffix: string;
  /** Abbreviate large numbers (1.2k); off where the unit already scales. */
  compact: boolean;
}

const IEC: [number, string][] = [
  [1024 ** 4, 'TiB'],
  [1024 ** 3, 'GiB'],
  [1024 ** 2, 'MiB'],
  [1024, 'KiB'],
];

function bytesScale(maxAbs: number, per: string): UnitScale {
  const [size, name] = IEC.find(([s]) => maxAbs >= s) ?? [1, 'B'];
  return { factor: 1 / size, suffix: ` ${name}${per}`, compact: false };
}

/** The display unit for values up to `maxAbs`. */
function unitScale(unit: MetricUnit | null, maxAbs: number): UnitScale {
  switch (unit) {
    case 'bytes':
      return bytesScale(maxAbs, '');
    case 'bytes/s':
      return bytesScale(maxAbs, '/s');
    case 'cores':
      // Same split as formatCores: millicores below one core.
      return maxAbs < 1 ? { factor: 1000, suffix: ' m', compact: false } : { factor: 1, suffix: ' cores', compact: false };
    case 'seconds':
      return maxAbs < 1 ? { factor: 1000, suffix: ' ms', compact: false } : { factor: 1, suffix: ' s', compact: false };
    case 'ratio':
      return { factor: 100, suffix: '%', compact: false };
    case 'percent':
      return { factor: 1, suffix: '%', compact: false };
    case 'per_second':
      return { factor: 1, suffix: '/s', compact: true };
    default:
      return { factor: 1, suffix: '', compact: true };
  }
}

const SI: [number, string][] = [
  [1e12, 'T'],
  [1e9, 'G'],
  [1e6, 'M'],
  [1e3, 'k'],
];

/** Three significant figures, so a small non-zero value never reads as 0.00. */
function formatNumber(value: number, compact: boolean): string {
  if (!Number.isFinite(value)) return '-';
  const rounded = Number(value.toPrecision(3));
  if (compact) {
    const si = SI.find(([size]) => Math.abs(rounded) >= size);
    if (si) return `${Number((rounded / si[0]).toPrecision(3))}${si[1]}`;
  }
  return Math.abs(value) >= 100 ? Math.round(value).toLocaleString('en-US') : String(rounded);
}

const format = (value: number, scale: UnitScale) => (Number.isFinite(value) ? `${formatNumber(value, scale.compact)}${scale.suffix}` : '-');

/** One raw value in `unit`, in the display unit that suits it (2147483648 bytes → "2 GiB"). */
export function formatMetricValue(raw: number, unit: MetricUnit | null): string {
  const scale = unitScale(unit, Math.abs(raw));
  return format(raw * scale.factor, scale);
}

/**
 * Converts a chart's series into one display unit chosen from their largest
 * value, so the axis ticks land on round numbers of that unit. Returns the
 * converted series and the formatter for its ticks, tooltip and legend.
 */
export function scaleSeriesToUnit<T extends { data?: unknown[] }>(
  datasets: T[],
  unit: MetricUnit | null
): { datasets: T[]; formatValue: (value: number) => string } {
  let maxAbs = 0;
  for (const dataset of datasets) {
    for (const value of dataset.data ?? []) {
      const n = Number(value);
      if (value !== '' && value !== null && Number.isFinite(n)) maxAbs = Math.max(maxAbs, Math.abs(n));
    }
  }
  const scale = unitScale(unit, maxAbs);
  const scaled =
    scale.factor === 1
      ? datasets
      : datasets.map((dataset) => ({
          ...dataset,
          data: dataset.data?.map((value) =>
            value !== '' && value !== null && Number.isFinite(Number(value)) ? Number(value) * scale.factor : value
          ),
        }));
  return { datasets: scaled, formatValue: (value: number) => format(value, scale) };
}
