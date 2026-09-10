/**
 * Turns the workload-total trend rows the utilisation API returns into per-pod
 * rows, so usage, request and limit share an axis with the per-pod
 * recommendation line instead of a fleet total sitting against one replica.
 *
 * The divisor is the measured running-pod count at each point when the
 * provider returned one (Prometheus), otherwise the replica average the
 * producers stamped on the recommendation, otherwise the pod count the
 * workload reports right now. With none, the rows stay totals and `source`
 * says so, so the chart never claims "per pod" it cannot back.
 */

export type PerPodSource = 'measured' | 'window' | 'live' | null;

export interface PerPodTrend {
  rows: any[];
  source: PerPodSource;
  /** Replicas the division used: the mean measured count, or the window average. */
  replicas: number | null;
  /** True when the busiest-pod series came back for at least one point. */
  hasBusiestPod: boolean;
}

const DIVIDED = ['avg_cpu_used', 'avg_cpu_request', 'avg_cpu_limit', 'avg_memory_used', 'avg_memory_request', 'avg_memory_limit'];

const positive = (v: any): number | null => (typeof v === 'number' && Number.isFinite(v) && v > 0 ? v : null);
const isFiniteNumber = (v: any): boolean => typeof v === 'number' && Number.isFinite(v);

export function toPerPodTrend(rows: any[], windowReplicas: number | null | undefined, liveReplicas?: number | null): PerPodTrend {
  rows = rows || [];
  const window = positive(windowReplicas);
  const live = positive(liveReplicas);
  const fallback = window ?? live;
  const measured = rows.map((r) => positive(r?.pod_count));
  // An idle busiest pod is still a data point, so zero counts here.
  const hasBusiestPod = rows.some((r) => isFiniteNumber(r?.max_pod_cpu_used));
  const anyMeasured = measured.some((m) => m != null);
  const source: PerPodSource = anyMeasured ? 'measured' : window ? 'window' : live ? 'live' : null;
  if (!source) {
    return { rows, source, replicas: null, hasBusiestPod };
  }

  const out = rows.map((r, i) => {
    const divisor = measured[i] ?? fallback;
    const next = { ...r };
    for (const key of DIVIDED) {
      if (typeof r[key] !== 'number') continue;
      // A point with no count and no fallback is a gap, never a raw total
      // drawn inside a per-pod series.
      next[key] = divisor ? r[key] / divisor : null;
    }
    return next;
  });

  let replicas: number | null = fallback;
  if (anyMeasured) {
    const counts = measured.filter((m): m is number => m != null);
    replicas = Math.round((counts.reduce((a, b) => a + b, 0) / counts.length) * 10) / 10;
  }
  return { rows: out, source, replicas, hasBusiestPod };
}

/** The one-line caption under a per-pod chart title. */
export function perPodCaption(trend: PerPodTrend): string | null {
  if (!trend.source || trend.replicas == null) return null;
  const n = Number.isInteger(trend.replicas) ? String(trend.replicas) : trend.replicas.toFixed(1);
  const pods = trend.replicas === 1 ? 'pod' : 'pods';
  if (trend.source === 'measured') return `Average per pod across ${n} running ${pods}; the busiest pod is drawn separately.`;
  if (trend.source === 'window') return `Workload total divided by the ${n}-pod average the recommendation was priced at.`;
  return `Workload total divided by the ${n} ${pods} running now.`;
}
