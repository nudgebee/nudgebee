import { useEffect, useMemo, useState } from 'react';
import k8sApi from '@api1/kubernetes';
import { safeParseJSON } from '@components/optimise-new/utils';
import { extractContainerData, replicaWindowOf } from '@components/optimise-new/rightSizingData';
import { toPerPodTrend, type PerPodTrend } from '@components/optimise-new/evidence/perPodTrend';
import { trendRangeFor, useTrendRange, type TrendRangeDays } from '@components/optimise-new/evidence/TrendRange';

/**
 * The usage trend behind a pod right-sizing recommendation: the workload's CPU
 * and memory, per pod, from the utilisation API, over a range that starts at
 * the window the recommendation was computed over.
 *
 * Owned by the detail drawer rather than by either tab, because the Details tab
 * draws a compact version of the same series the Evidence tab draws in full,
 * and both tabs stay mounted while the drawer is open. One fetch feeds both.
 * Recommendations that are not pod right-sizing never fetch: `hasData` stays
 * false and the chart surfaces render nothing.
 */
export interface RightSizingTrend {
  /** Rows divided to per-pod values, or left as totals when nothing could divide them. */
  perPod: PerPodTrend;
  loading: boolean;
  /** True once the fetch settled with at least one row for this recommendation. */
  hasData: boolean;
  /** The executed metric queries, keyed by metric; only the Prometheus datasource returns them. */
  cpuQueries: Record<string, string>;
  memoryQueries: Record<string, string>;
  /** Days the trend covers; both tabs label their charts with it. */
  rangeDays: TrendRangeDays;
  setRangeDays: (days: TrendRangeDays) => void;
}

const NO_ROWS: PerPodTrend = { rows: [], source: null, replicas: null, hasBusiestPod: false };

const TREND_METRICS = [
  'cpu_usage',
  'memory_usage',
  'cpu_limit',
  'cpu_request',
  'memory_limit',
  'memory_request',
  'pod_count',
  'cpu_usage_max_pod',
  'memory_usage_max_pod',
];

// Split the executed queries (keyed by metric, e.g. cpu_usage / memory_limit)
// into the CPU and memory groups each chart's query popover shows.
function splitQueries(res: any): { cpu: Record<string, string>; memory: Record<string, string> } {
  const promQueries: Record<string, string> = res?.data?.promQueries || {};
  const cpu: Record<string, string> = {};
  const memory: Record<string, string> = {};
  Object.entries(promQueries).forEach(([key, q]) => {
    if (key.startsWith('cpu_')) cpu[key] = q;
    else if (key.startsWith('memory_')) memory[key] = q;
    else if (key === 'pod_count') {
      // The divisor behind both charts.
      cpu[key] = q;
      memory[key] = q;
    }
  });
  return { cpu, memory };
}

export function useRightSizingTrend(fullRecommendation: any): RightSizingTrend {
  const recData = useMemo(() => safeParseJSON(fullRecommendation?.recommendation), [fullRecommendation?.recommendation]);
  const containers = useMemo(() => extractContainerData(recData), [recData]);
  const containerName = containers[0]?.containerName;
  const primary = containers[0];
  const windowHours =
    primary?.cpu?.strategy?.settings?.history_duration ??
    primary?.memory?.strategy?.settings?.history_duration ??
    replicaWindowOf(primary ? [primary.cpu, primary.memory] : [])?.windowHours;
  const [rangeDays, setRangeDays] = useTrendRange(fullRecommendation?.id, trendRangeFor(windowHours));

  const [rows, setRows] = useState<any[]>([]);
  const [loading, setLoading] = useState(false);
  const [cpuQueries, setCpuQueries] = useState<Record<string, string>>({});
  const [memoryQueries, setMemoryQueries] = useState<Record<string, string>>({});

  // The query is keyed on the fields it is built from, not on the
  // recommendation object: the drawer re-renders freely and a parent handing
  // down a fresh object each time must not refetch, or reset, on every render.
  const accountId = fullRecommendation?.account_id;
  const meta = fullRecommendation?.cloud_resourse?.meta || {};
  const resourceType = fullRecommendation?.cloud_resourse?.type || fullRecommendation?.resource_type || '';
  const namespaceName = fullRecommendation?.resource_k8s_namespace || meta?.config?.namespace || meta?.namespace || meta?.namespaceName || '';
  const isPod = resourceType === 'Pod';
  const workloadName = isPod
    ? meta?.controller || meta?.config?.controller || fullRecommendation?.resource_name
    : fullRecommendation?.cloud_resourse?.name || fullRecommendation?.resource_name;
  const workloadType = isPod ? meta?.controllerKind || meta?.config?.controllerKind || 'Deployment' : resourceType || 'Deployment';
  // For Pod-level: only pass podName if different from workloadName (matches existing page behavior)
  const podName = isPod ? fullRecommendation?.cloud_resourse?.name || fullRecommendation?.resource_name : undefined;
  const effectivePodName = podName && podName !== workloadName ? podName : undefined;
  const hasContainers = containers.length > 0;

  useEffect(() => {
    // Rows never outlive the recommendation, or the range, they were fetched for: the drawer
    // is reused across recommendations, and a stale trend under a new verdict
    // would be read as that verdict's evidence. The resets are no-ops when
    // there is nothing to clear, so a re-run never causes a render by itself.
    setRows((r) => (r.length ? [] : r));
    setCpuQueries((q) => (Object.keys(q).length ? {} : q));
    setMemoryQueries((q) => (Object.keys(q).length ? {} : q));
    if (!hasContainers || !accountId || !namespaceName || !workloadName) return undefined;

    let cancelled = false;
    setLoading(true);
    const startDate = new Date();
    startDate.setDate(startDate.getDate() - rangeDays);

    // Match the KubernetesUtilization page: the 'prometheus' datasource with an
    // explicit metrics list. The recommendation line is the first container's
    // target, so usage is scoped to that container and asked for per pod: the
    // running pod count and the busiest pod ride along with the workload totals.
    const query: any = {
      accountId,
      namespaceName,
      workloadName,
      workloadType: workloadType?.toLowerCase(),
      startDate,
      endDate: new Date(),
      metrics: TREND_METRICS,
    };
    if (effectivePodName) query.podName = effectivePodName;
    // The legacy {notifications: [...]} shape names no container; its 'default'
    // placeholder must not become a PromQL filter that matches nothing.
    if (containerName && containerName !== 'default') query.containerName = containerName;

    const groupBy = ['tenant_id', 'account_id', 'timestamp', 'namespace_name', 'workload_name'];
    if (effectivePodName) groupBy.push('pod_name');

    k8sApi
      .getK8sPodGroupings2(500, query, groupBy, 'prometheus')
      .then((res: any) => {
        if (cancelled) return undefined;
        const found = res?.data?.k8s_pod_groupings || [];
        if (found.length > 0) {
          setRows(found);
          const q = splitQueries(res);
          setCpuQueries(q.cpu);
          setMemoryQueries(q.memory);
          return undefined;
        }
        // Fallback: the 'nb' (RPC) datasource for historical data.
        return k8sApi.getK8sPodGroupings2(500, query, groupBy, 'nb').then((res2: any) => {
          if (cancelled) return;
          setRows(res2?.data?.k8s_pod_groupings || []);
        });
      })
      .catch((err: any) => {
        if (cancelled) return;
        console.error('[useRightSizingTrend] Failed to fetch pod trend data:', err);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [fullRecommendation?.id, hasContainers, accountId, namespaceName, workloadName, workloadType, effectivePodName, containerName, rangeDays]);

  // Per pod, like the recommendation line: totals are divided by the running
  // pod count, or by the replica average the producers stamped on the row.
  const livePods = fullRecommendation?.cloud_resourse?.meta?.total_pods;
  const perPod = useMemo(() => {
    if (rows.length === 0) return NO_ROWS;
    const window = replicaWindowOf(containers[0] ? [containers[0].cpu, containers[0].memory] : [])?.avg;
    return toPerPodTrend(rows, window, livePods);
  }, [rows, containers, livePods]);

  return { perPod, loading, hasData: rows.length > 0, cpuQueries, memoryQueries, rangeDays, setRangeDays };
}

export default useRightSizingTrend;
