import type { VmFleetMetric, VmMetricResult, VmMetricSeries, VmResource } from '@api1/vm';

const GIB = 1024 ** 3;
const MIB = 1024 ** 2;

export interface ChartData {
  labels: string[];
  series: Array<{ key: string; data: number[] }>;
}

const find = (results: VmMetricResult[], key: string) => results.find((r) => r.key === key);

const clock = (epochSeconds: number) => {
  const d = new Date(epochSeconds * 1000);
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
};

/**
 * Merge several keyed single-line results onto one shared time axis. Series from
 * different queries land on slightly different timestamps, so the axis is the
 * union and a series has `NaN`-free gaps filled with its previous value (or 0 before
 * its first point) rather than being dropped.
 */
export function toChart(results: VmMetricResult[], lines: Array<{ key: string; label: string; scale?: number }>): ChartData {
  const present = lines
    .map((l) => ({ ...l, s: find(results, l.key)?.series?.[0] }))
    .filter((l): l is typeof l & { s: VmMetricSeries } => Boolean(l.s && l.s.timestamps.length));
  if (!present.length) return { labels: [], series: [] };

  const axis = Array.from(new Set(present.flatMap((l) => l.s.timestamps))).sort((a, b) => a - b);
  return {
    labels: axis.map(clock),
    series: present.map((l) => {
      const byTs = new Map(l.s.timestamps.map((t, i) => [t, l.s.values[i]]));
      let last = 0;
      return {
        key: l.label,
        data: axis.map((t) => {
          if (byTs.has(t)) last = (byTs.get(t) as number) / (l.scale ?? 1);
          return last;
        }),
      };
    }),
  };
}

export const cpuChart = (r: VmMetricResult[]) => toChart(r, [{ key: 'cpu_usage_line', label: 'CPU used (cores)' }]);
export const memoryChart = (r: VmMetricResult[]) => toChart(r, [{ key: 'memory_usage_line', label: 'Memory used (GiB)', scale: GIB }]);
export const diskIoChart = (r: VmMetricResult[]) =>
  toChart(r, [
    { key: 'disk_read_bytes', label: 'Read (MiB/s)', scale: MIB },
    { key: 'disk_write_bytes', label: 'Write (MiB/s)', scale: MIB },
  ]);

/** Last value of the first series of a key, or undefined when it has no data. */
export function latest(results: VmMetricResult[], key: string): number | undefined {
  const s = find(results, key)?.series?.[0];
  return s && s.values.length ? s.values[s.values.length - 1] : undefined;
}

export interface ServiceRow {
  /** Readable name: the systemd unit without its path and `.service`, or the raw id for anything else. */
  service: string;
  /** node-agent's container_id, the series' real identity (e.g. `/system.slice/ssh.service`). */
  containerId: string;
  cpuCores?: number;
  memoryBytes?: number;
  diskBytes?: number;
  /** Repository and branch the service declares itself (container label or systemd Environment=). */
  agentRepo?: string;
  agentBranch?: string;
}

/**
 * `/system.slice/ssh.service` -> `ssh`. Containers (`/docker/<id>`) and anything else keep
 * their id, since there is nothing shorter that is still unambiguous.
 */
export const serviceName = (containerId: string): string => containerId.replace(/^\/system\.slice\//, '').replace(/\.service$/, '');

/**
 * One row per service, the latest value of each per-service key, busiest CPU first.
 * A service is node-agent's `container_id`; its `app_id` is the Kubernetes application id
 * and is empty on a VM.
 */
export function serviceRows(results: VmMetricResult[]): ServiceRow[] {
  const rows = new Map<string, ServiceRow>();
  const fill = (key: string, field: 'cpuCores' | 'memoryBytes' | 'diskBytes') => {
    for (const s of find(results, key)?.series || []) {
      const id = s.metric?.container_id;
      if (!id || !s.values.length) continue;
      const row = rows.get(id) || { service: serviceName(id), containerId: id };
      row[field] = s.values[s.values.length - 1];
      rows.set(id, row);
    }
  };
  fill('vm_service_cpu_usage', 'cpuCores');
  fill('vm_service_memory_usage', 'memoryBytes');
  fill('vm_service_disk_used', 'diskBytes');
  for (const s of find(results, 'vm_service_git')?.series || []) {
    const row = rows.get(s.metric?.container_id);
    if (row && s.metric?.git_repo) {
      row.agentRepo = s.metric.git_repo;
      row.agentBranch = s.metric.git_branch || '';
    }
  }
  return [...rows.values()].sort((a, b) => (b.cpuCores ?? 0) - (a.cpuCores ?? 0) || a.containerId.localeCompare(b.containerId));
}

/** Distinct reasons keys came back empty, so the panel can say why instead of showing blank charts. */
export function emptyReasons(results: VmMetricResult[]): string[] {
  return Array.from(new Set(results.filter((r) => !r.series.some((s) => s.values.length) && r.reason).map((r) => r.reason as string)));
}

export const hasAnyData = (results: VmMetricResult[]) => results.some((r) => r.series.some((s) => s.values.length));

export const formatBytes = (bytes?: number) => {
  if (bytes === undefined) return '—';
  if (bytes >= GIB) return `${(bytes / GIB).toFixed(1)} GiB`;
  return `${(bytes / MIB).toFixed(0)} MiB`;
};

export const formatCores = (cores?: number) => (cores === undefined ? '—' : cores.toFixed(2));

// ----- fleet gauges ---------------------------------------------------------

const lastValue = (s?: VmMetricSeries): number | undefined => (s && s.values.length ? s.values[s.values.length - 1] : undefined);

/** `instance` -> latest value, for a per-instance result. */
const byInstance = (results: VmMetricResult[], key: string): Map<string, number> => {
  const out = new Map<string, number>();
  for (const s of find(results, key)?.series || []) {
    const instance = s.metric?.instance;
    const v = lastValue(s);
    if (instance && v !== undefined) out.set(instance, v);
  }
  return out;
};

/** label value -> the instances carrying it, from an identity result such as node_cloud_info. */
const instancesByLabel = (results: VmMetricResult[], key: string, label: string): Map<string, Set<string>> => {
  const out = new Map<string, Set<string>>();
  for (const s of find(results, key)?.series || []) {
    const value = s.metric?.[label];
    const instance = s.metric?.instance;
    if (!value || !instance) continue;
    if (!out.has(value)) out.set(value, new Set());
    (out.get(value) as Set<string>).add(instance);
  }
  return out;
};

const pct = (used?: number, total?: number): number | undefined =>
  used === undefined || !total || total <= 0 ? undefined : Math.min(100, Math.max(0, (used / total) * 100));

/**
 * Match each VM to the node-agent `instance` that reports for it and turn that instance's
 * CPU and memory into percentages. The match is the strongest identifier first: the cloud
 * instance id, then the private IP, then the hostname. An identifier that names more than one
 * instance (a reused IP, hostnames cloned from one image) matches none: showing another
 * machine's load would be worse than showing nothing.
 */
export function joinFleetMetrics(
  results: VmMetricResult[],
  vms: Array<Pick<VmResource, 'id' | 'name' | 'resourse_id' | 'meta'>>
): Record<string, VmFleetMetric> {
  const byInstanceId = instancesByLabel(results, 'vm_fleet_identity', 'instance_id');
  const byIp = instancesByLabel(results, 'vm_fleet_identity', 'local_ipv4');
  const byHostname = instancesByLabel(results, 'vm_fleet_hostname', 'hostname');
  const cpuUsed = byInstance(results, 'vm_fleet_cpu_used');
  const cpuCores = byInstance(results, 'vm_fleet_cpu_cores');
  const memUsed = byInstance(results, 'vm_fleet_mem_used');
  const memTotal = byInstance(results, 'vm_fleet_mem_total');

  const only = (m: Map<string, Set<string>>, value?: string): string | undefined => {
    const set = value ? m.get(value) : undefined;
    return set && set.size === 1 ? [...set][0] : undefined;
  };

  const out: Record<string, VmFleetMetric> = {};
  for (const vm of vms) {
    const instance = only(byInstanceId, vm.resourse_id) || only(byIp, vm.meta?.private_ip) || only(byHostname, vm.name);
    if (!instance) continue;
    const metric: VmFleetMetric = {
      cpuPct: pct(cpuUsed.get(instance), cpuCores.get(instance)),
      memPct: pct(memUsed.get(instance), memTotal.get(instance)),
    };
    if (metric.cpuPct !== undefined || metric.memPct !== undefined) out[vm.id] = metric;
  }
  return out;
}
