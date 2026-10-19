import { parseVmMetricResults, VmMetricResult } from '@api1/vm';
import { cpuChart, diskIoChart, emptyReasons, formatBytes, hasAnyData, joinFleetMetrics, latest, serviceName, serviceRows } from '../vmMetricsData';

const GIB = 1024 ** 3;

const result = (key: string, series: Array<{ metric?: Record<string, string>; t: number[]; v: number[] }>, reason?: string): VmMetricResult => ({
  key,
  reason,
  series: series.map((s) => ({ metric: s.metric || {}, timestamps: s.t, values: s.v })),
});

describe('parseVmMetricResults', () => {
  it('converts string values to numbers and drops non-finite points', () => {
    const [r] = parseVmMetricResults([{ query_key: 'cpu_usage_line', payload: [{ timestamps: [1, 2, 3], values: ['0.5', 'NaN', '1.5'] }] }]);
    expect(r.series[0].timestamps).toEqual([1, 3]);
    expect(r.series[0].values).toEqual([0.5, 1.5]);
  });

  it('drops points with a non-numeric timestamp too', () => {
    const [r] = parseVmMetricResults([{ query_key: 'k', payload: [{ timestamps: [1, 'x', null, 4], values: ['1', '2', '3', '4'] }] }]);
    expect(r.series[0].timestamps).toEqual([1, 4]);
    expect(r.series[0].values).toEqual([1, 4]);
  });

  it('keeps the reason a key has no data', () => {
    const [a, b] = parseVmMetricResults([
      { query_key: 'x', payload: [], note: 'no metrics provider resolved for this account' },
      { query_key: 'y', payload: [], error: 'boom' },
    ]);
    expect(a.reason).toBe('no metrics provider resolved for this account');
    expect(b.reason).toBe('boom');
  });

  it('tolerates a non-array response', () => {
    expect(parseVmMetricResults(undefined as any)).toEqual([]);
  });
});

describe('chart helpers', () => {
  it('builds a cpu chart from the line key', () => {
    const chart = cpuChart([result('cpu_usage_line', [{ t: [60, 120], v: [0.25, 0.5] }])]);
    expect(chart.series).toEqual([{ key: 'CPU used (cores)', data: [0.25, 0.5] }]);
    expect(chart.labels).toHaveLength(2);
  });

  it('shares one time axis and carries a series forward over a gap', () => {
    const chart = diskIoChart([
      result('disk_read_bytes', [{ t: [60, 120], v: [1024 ** 2, 2 * 1024 ** 2] }]),
      result('disk_write_bytes', [{ t: [120], v: [3 * 1024 ** 2] }]),
    ]);
    expect(chart.labels).toHaveLength(2);
    expect(chart.series.find((s) => s.key === 'Read (MiB/s)')?.data).toEqual([1, 2]);
    expect(chart.series.find((s) => s.key === 'Write (MiB/s)')?.data).toEqual([0, 3]);
  });

  it('returns an empty chart when the key has no points', () => {
    expect(cpuChart([result('cpu_usage_line', [])])).toEqual({ labels: [], series: [] });
    expect(cpuChart([])).toEqual({ labels: [], series: [] });
  });
});

describe('serviceName', () => {
  it('shortens systemd units and leaves everything else alone', () => {
    expect(serviceName('/system.slice/ssh.service')).toBe('ssh');
    expect(serviceName('/system.slice/snap.amazon-ssm-agent.amazon-ssm-agent.service')).toBe('snap.amazon-ssm-agent.amazon-ssm-agent');
    expect(serviceName('/docker/3f2a9c1d7e5b')).toBe('/docker/3f2a9c1d7e5b');
  });
});

describe('summary helpers', () => {
  it('latest returns the last point', () => {
    expect(latest([result('cpu_total', [{ t: [1, 2], v: [4, 8] }])], 'cpu_total')).toBe(8);
    expect(latest([], 'cpu_total')).toBeUndefined();
  });

  it('serviceRows joins the per-service keys on container_id, busiest CPU first', () => {
    const rows = serviceRows([
      result('vm_service_cpu_usage', [
        { metric: { container_id: '/system.slice/nginx.service' }, t: [1], v: [0.1] },
        { metric: { container_id: '/system.slice/db.service' }, t: [1], v: [0.9] },
      ]),
      result('vm_service_memory_usage', [{ metric: { container_id: '/system.slice/nginx.service' }, t: [1], v: [2 * GIB] }]),
    ]);
    expect(rows.map((r) => r.service)).toEqual(['db', 'nginx']);
    expect(rows.map((r) => r.containerId)).toEqual(['/system.slice/db.service', '/system.slice/nginx.service']);
    expect(rows[1].memoryBytes).toBe(2 * GIB);
    expect(rows[0].memoryBytes).toBeUndefined();
  });

  it('serviceRows carries the repository a service declares itself, only for services that have one', () => {
    const rows = serviceRows([
      result('vm_service_cpu_usage', [
        { metric: { container_id: '/docker/api' }, t: [1], v: [0.9] },
        { metric: { container_id: '/docker/web' }, t: [1], v: [0.1] },
      ]),
      result('vm_service_git', [
        { metric: { container_id: '/docker/api', git_repo: 'https://github.com/org/api', git_branch: 'main' }, t: [1], v: [1] },
      ]),
    ]);
    expect(rows[0]).toMatchObject({ containerId: '/docker/api', agentRepo: 'https://github.com/org/api', agentBranch: 'main' });
    expect(rows[1].agentRepo).toBeUndefined();
  });

  it('ignores series with no container_id, including ones that only carry the empty app_id', () => {
    expect(serviceRows([result('vm_service_cpu_usage', [{ t: [1], v: [1] }])])).toEqual([]);
    expect(serviceRows([result('vm_service_cpu_usage', [{ metric: { app_id: '' }, t: [1], v: [1] }])])).toEqual([]);
  });

  it('reports why keys are empty, once', () => {
    const results = [result('a', [], 'no provider'), result('b', [], 'no provider'), result('c', [{ t: [1], v: [1] }], 'ignored')];
    expect(emptyReasons(results)).toEqual(['no provider']);
    expect(hasAnyData(results)).toBe(true);
    expect(hasAnyData([result('a', [])])).toBe(false);
  });

  it('formats bytes', () => {
    expect(formatBytes(undefined)).toBe('—');
    expect(formatBytes(1.5 * GIB)).toBe('1.5 GiB');
    expect(formatBytes(512 * 1024 ** 2)).toBe('512 MiB');
  });
});

describe('joinFleetMetrics', () => {
  const GIB_ = 1024 ** 3;
  const labelled = (key: string, rows: Array<Record<string, string>>, value = 1): VmMetricResult => ({
    key,
    series: rows.map((metric) => ({ metric, timestamps: [1], values: [value] })),
  });
  const perInstance = (key: string, values: Record<string, number>): VmMetricResult => ({
    key,
    series: Object.entries(values).map(([instance, v]) => ({ metric: { instance }, timestamps: [1], values: [v] })),
  });

  const fleet = (identity: Array<Record<string, string>>, hostnames: Array<Record<string, string>> = []): VmMetricResult[] => [
    labelled('vm_fleet_identity', identity),
    labelled('vm_fleet_hostname', hostnames),
    perInstance('vm_fleet_cpu_used', { A: 0.5, B: 3 }),
    perInstance('vm_fleet_cpu_cores', { A: 2, B: 2 }),
    perInstance('vm_fleet_mem_used', { A: 1 * GIB_, B: 2 * GIB_ }),
    perInstance('vm_fleet_mem_total', { A: 4 * GIB_, B: 4 * GIB_ }),
  ];
  const vm = (id: string, over: Partial<{ resourse_id: string; name: string; meta: any }> = {}) => ({
    id,
    name: 'web',
    resourse_id: 'i-none',
    meta: {},
    ...over,
  });

  it('turns an instance into CPU and memory percentages', () => {
    const out = joinFleetMetrics(fleet([{ instance: 'A', instance_id: 'i-1' }]), [vm('v1', { resourse_id: 'i-1' })]);
    expect(out.v1).toEqual({ cpuPct: 25, memPct: 25 });
  });

  it('matches by cloud instance id first, then private IP, then hostname', () => {
    const results = fleet(
      [
        { instance: 'A', instance_id: 'i-1', local_ipv4: '10.0.0.1' },
        { instance: 'B', instance_id: 'i-2', local_ipv4: '10.0.0.2' },
      ],
      [{ instance: 'B', hostname: 'db' }]
    );
    const out = joinFleetMetrics(results, [
      vm('byId', { resourse_id: 'i-1', meta: { private_ip: '10.0.0.2' } }), // id wins over a conflicting IP
      vm('byIp', { meta: { private_ip: '10.0.0.2' } }),
      vm('byHost', { name: 'db' }),
    ]);
    expect(out.byId?.cpuPct).toBe(25); // instance A
    expect(out.byIp?.cpuPct).toBe(100); // instance B: 3 cores used of 2, clamped
    expect(out.byHost?.cpuPct).toBe(100);
  });

  it('clamps a busy VM to 100%', () => {
    const out = joinFleetMetrics(fleet([{ instance: 'B', instance_id: 'i-2' }]), [vm('v', { resourse_id: 'i-2' })]);
    expect(out.v.cpuPct).toBe(100);
  });

  it('omits a VM nothing reports for', () => {
    expect(joinFleetMetrics(fleet([{ instance: 'A', instance_id: 'i-1' }]), [vm('ghost', { resourse_id: 'i-404', name: 'nope' })])).toEqual({});
  });

  it('does not guess when an identifier names more than one machine', () => {
    // two clones that kept the image hostname, and an IP both claim
    const results = fleet(
      [
        { instance: 'A', local_ipv4: '10.0.0.9' },
        { instance: 'B', local_ipv4: '10.0.0.9' },
      ],
      [
        { instance: 'A', hostname: 'web' },
        { instance: 'B', hostname: 'web' },
      ]
    );
    expect(joinFleetMetrics(results, [vm('v', { name: 'web', meta: { private_ip: '10.0.0.9' } })])).toEqual({});
  });

  it('leaves out a figure that was not reported instead of showing 0', () => {
    const results: VmMetricResult[] = [
      labelled('vm_fleet_identity', [{ instance: 'A', instance_id: 'i-1' }]),
      perInstance('vm_fleet_cpu_used', { A: 1 }),
      perInstance('vm_fleet_cpu_cores', { A: 4 }),
    ];
    expect(joinFleetMetrics(results, [vm('v', { resourse_id: 'i-1' })]).v).toEqual({ cpuPct: 25, memPct: undefined });
  });

  it('tolerates empty results', () => {
    expect(joinFleetMetrics([], [vm('v')])).toEqual({});
    expect(joinFleetMetrics(fleet([]), [])).toEqual({});
  });
});
