import { formatClockCompact } from '@shared/format/Datetime';
import {
  mentionsContainer,
  summarizeAlertLabels,
  summarizeAllocation,
  summarizeCloudWatchAlarm,
  summarizeCpu,
  summarizeDependencies,
  summarizeDescription,
  summarizeMetricGroups,
  summarizeNoisyNeighbours,
  summarizeQueryMetrics,
  summarizeResources,
  summarizeSeries,
  summarizeTimeline,
} from '../cardSummaries';

const messages = (lines) => lines.map((l) => l.message);

describe('summarizeTimeline', () => {
  const first = { timestamp: '2026-09-30T07:42:00Z', ref_type: 'event', action: 'first_occurrence' };
  const change = { timestamp: '2026-09-30T09:12:00Z', ref_type: 'config_change', action: 'configuration_changed' };
  const fired = { timestamp: '2026-10-02T19:07:00Z', ref_type: 'event', action: 'fired' };

  it('says when it was first seen, when the workload last changed and when it fired', () => {
    expect(
      messages(summarizeTimeline([{ timestamp: '2026-09-30T07:41:00Z', ref_type: 'workload', action: 'created' }, first, change, fired]))
    ).toEqual([
      `First seen ${formatClockCompact(first.timestamp)} · config changed ${formatClockCompact(change.timestamp)} · fired ${formatClockCompact(
        fired.timestamp
      )}`,
    ]);
  });

  it('counts several config changes and names the last', () => {
    const later = { ...change, timestamp: '2026-10-01T10:00:00Z' };
    expect(messages(summarizeTimeline([change, later, fired]))).toEqual([
      `Last of 2 config changes ${formatClockCompact(later.timestamp)} · fired ${formatClockCompact(fired.timestamp)}`,
    ]);
  });

  it('does not repeat the firing as a first sighting, and says nothing with nothing to say', () => {
    expect(messages(summarizeTimeline([{ ...first, timestamp: fired.timestamp }, fired]))).toEqual([`Fired ${formatClockCompact(fired.timestamp)}`]);
    expect(summarizeTimeline([{ timestamp: '2026-09-30T07:41:00Z', ref_type: 'workload', action: 'created' }])).toEqual([]);
    expect(summarizeTimeline(undefined)).toEqual([]);
    // A change whose time cannot be read is not counted or shown.
    expect(messages(summarizeTimeline([change, { ...change, timestamp: 'not a time' }, fired]))).toEqual([
      `Config changed ${formatClockCompact(change.timestamp)} · fired ${formatClockCompact(fired.timestamp)}`,
    ]);
  });
});

describe('summarizeDependencies', () => {
  const node = (id, name) => ({ id, node_type: 'Workload', properties: { name } });
  const calls = (source, dest) => ({ source_node_id: source, dest_node_id: dest, relationship_type: 'CALLS' });

  it('says so when nothing calls the service and it calls nothing', () => {
    expect(messages(summarizeDependencies([node('a', 'checkout')], [], 'checkout'))).toEqual([
      'Nothing in the service map calls checkout, and it calls nothing',
    ]);
  });

  it('names callers and callees from CALLS edges only', () => {
    const nodes = [node('a', 'checkout'), node('b', 'frontend'), node('c', 'payments'), node('d', 'cart'), node('n', 'node-1')];
    const edges = [calls('b', 'a'), calls('a', 'c'), calls('a', 'd'), { source_node_id: 'a', dest_node_id: 'n', relationship_type: 'RUNS_ON' }];
    expect(messages(summarizeDependencies(nodes, edges, 'checkout'))).toEqual([
      '1 service calls checkout: frontend',
      'checkout calls 2 services: payments, cart',
    ]);
  });

  it('says nothing without a graph or a target, or when the target is not in the graph by name', () => {
    expect(summarizeDependencies([], [], 'checkout')).toEqual([]);
    expect(summarizeDependencies([node('a', 'checkout')], [], '')).toEqual([]);
    expect(summarizeDependencies([node('b', 'frontend'), node('c', 'api')], [calls('b', 'c')], 'checkout')).toEqual([]);
  });
});

describe('summarizeNoisyNeighbours', () => {
  const GIB = 1024 ** 3;
  it('states how committed the node was and this pod’s share', () => {
    const parsed = {
      memory: { allocatable: 14.45 * GIB, requested: 11.02 * GIB },
      neighbours: [
        { is_subject: true, memory_used: 7.52 * GIB },
        { is_subject: false, memory_used: 0.98 * GIB },
      ],
    };
    expect(messages(summarizeNoisyNeighbours(parsed))).toEqual(['Node memory 76% committed (11.02 GiB of 14.45 GiB); this pod uses 52% of the node']);
  });

  it('finds the alerting pod by name on blocks that do not flag it', () => {
    const parsed = {
      memory: { allocatable: 10 * GIB },
      neighbours: [
        { pod_name: 'db-0', memory_used: 5 * GIB },
        { pod_name: 'other', memory_used: 1 * GIB },
      ],
    };
    expect(messages(summarizeNoisyNeighbours(parsed, 'db-0'))).toEqual(['this pod uses 50% of the node']);
    expect(summarizeNoisyNeighbours(parsed)).toEqual([]);
  });

  it('says nothing when the node’s size is unknown', () => {
    expect(summarizeNoisyNeighbours(null)).toEqual([]);
    expect(summarizeNoisyNeighbours({ memory: {}, neighbours: [] })).toEqual([]);
  });
});

describe('summarizeAllocation', () => {
  const MIB = 1024 * 1024;
  it('states the peak against the limit, for the container the alert is about', () => {
    const items = [
      { container: 'sidecar', resource_type: 'memory', peakUsage: 19 * MIB, limits: 0, request: 0 },
      { container: 'app', resource_type: 'memory', peakUsage: 412 * MIB, limits: 512 * MIB, request: 256 * MIB },
      { container: 'app', resource_type: 'cpu', peakUsage: 0.4, limits: 512 * MIB },
    ];
    expect(messages(summarizeAllocation(items, 'app'))).toEqual(['Container app peaked at 412 MiB, 80% of its 512 MiB limit']);
    expect(messages(summarizeAllocation(items))).toEqual(['Container sidecar peaked at 19 MiB and has no memory limit']);
  });

  it('says when there were no samples', () => {
    expect(
      messages(summarizeAllocation([{ container: 'app', resource_type: 'memory', peakUsage: 0, limits: 256 * MIB, request: 192 * MIB }]))
    ).toEqual(['Container app: no memory samples around the alert']);
    expect(summarizeAllocation([])).toEqual([]);
  });
});

describe('summarizeCpu', () => {
  it('states the peak against the limit, or the request when there is no limit', () => {
    expect(messages(summarizeCpu({ usage: [0.1, 0.42, 0.3], request: [0.1], limit: [0.5] }))).toEqual([
      'CPU peaked at 0.42 cores, 84% of its 0.5-core limit',
    ]);
    expect(messages(summarizeCpu({ usage: [0.02], request: [0.01], limit: [] }))).toEqual([
      'CPU peaked at 0.02 cores, 200% of its 0.01-core request; no CPU limit',
    ]);
    expect(messages(summarizeCpu({ usage: [0.02] }))).toEqual(['CPU peaked at 0.02 cores; no request or limit set']);
  });

  it('says there were no samples instead of leaving an empty chart unexplained', () => {
    expect(messages(summarizeCpu({ usage: [], request: [0.1], limit: [0.5] }))).toEqual([
      'No CPU samples for this pod in the 10 minutes around the alert',
    ]);
    expect(summarizeCpu(null)).toEqual([]);
    // The usage query was not answered at all: that is not "no samples".
    expect(summarizeCpu({ usage: undefined, request: [0.1], limit: [0.5] })).toEqual([]);
  });
});

describe('summarizeQueryMetrics', () => {
  it('states each metric’s peak and average, without a unit', () => {
    const results = [
      { query_key: 'cpu_usage', payload: [{ metric: {}, values: ['0.422', '1.87', '0.635'] }] },
      { query_key: 'empty', payload: [{ metric: {}, values: [] }] },
    ];
    expect(messages(summarizeQueryMetrics(results))).toEqual(['cpu_usage: peak 1.87, average 0.98']);
    expect(summarizeQueryMetrics(undefined)).toEqual([]);
  });
});

describe('summarizeCloudWatchAlarm', () => {
  it('uses the alarm’s own reason', () => {
    expect(
      messages(summarizeCloudWatchAlarm({ stateReason: 'Threshold Crossed: 1 datapoint [5.99] was greater than the threshold (5.0).' }))
    ).toEqual(['Threshold Crossed: 1 datapoint [5.99] was greater than the threshold (5.0).']);
    expect(messages(summarizeCloudWatchAlarm({ state: { reason: 'Threshold Crossed' } }))).toEqual(['Threshold Crossed']);
  });

  it('falls back to the alarm’s condition, and says nothing when it has neither', () => {
    expect(messages(summarizeCloudWatchAlarm({ metricName: 'CPUUtilization', comparisonOperator: 'GreaterThanThreshold', threshold: 80 }))).toEqual([
      'CPUUtilization GreaterThanThreshold 80',
    ]);
    expect(summarizeCloudWatchAlarm({})).toEqual([]);
    expect(summarizeCloudWatchAlarm(null)).toEqual([]);
  });
});

describe('summarizeMetricGroups', () => {
  const groups = {
    'ReadIOPS (Average)': { series_list_result: [{ metric: { name: 'ReadIOPS' }, values: [0.05, 0, 0.03] }] },
    'ServerlessDatabaseCapacity (Average)': { series_list_result: [{ metric: { name: 'ServerlessDatabaseCapacity' }, values: [4, 5.99, 4.5] }] },
  };

  it('leads with the metric the alarm watches', () => {
    expect(messages(summarizeMetricGroups(groups, 'ServerlessDatabaseCapacity'))).toEqual([
      'ServerlessDatabaseCapacity (Average) peaked at 5.99; 2 metrics charted',
    ]);
  });

  it('lists the metrics when the alarm’s metric is not among them', () => {
    expect(messages(summarizeMetricGroups(groups))).toEqual(['2 metrics charted: ReadIOPS (Average), ServerlessDatabaseCapacity (Average)']);
    expect(messages(summarizeMetricGroups(groups, 'FreeStorageSpace'))).toEqual([
      "2 metrics charted; the alarm's own metric, FreeStorageSpace, is not one of them",
    ]);
    expect(summarizeMetricGroups({})).toEqual([]);
    // A group named for the alarm's metric but holding no series is not a peak.
    expect(messages(summarizeMetricGroups({ 'CPUUtilization (Average)': null, 'ReadIOPS (Average)': {} }, 'CPUUtilization'))).toEqual([
      "2 metrics charted; the alarm's own metric, CPUUtilization, is not one of them",
    ]);
    // The group key alone identifies the alarm's metric when its series carry no name.
    expect(
      messages(summarizeMetricGroups({ 'CPUUtilization (Average)': { series_list_result: [{ metric: {}, values: [10, 93] }] } }, 'CPUUtilization'))
    ).toEqual(['CPUUtilization (Average) peaked at 93; 1 metric charted']);
  });
});

describe('summarizeSeries', () => {
  it('states how many series and the range they cover', () => {
    expect(messages(summarizeSeries([{ metric: { datname: 'app' }, values: ['0.97', '0.54', '0.95'] }]))).toEqual([
      '1 series, between 0.54 and 0.97',
    ]);
    expect(messages(summarizeSeries([{ values: [2] }, { values: [2] }], (v) => `${v} cores`))).toEqual(['2 series, constant at 2 cores']);
    expect(summarizeSeries([{ values: [] }])).toEqual([]);
    // More values than a function call can take as arguments.
    const many = Array.from({ length: 200000 }, (_, i) => i % 1000);
    expect(messages(summarizeSeries([{ values: many }]))).toEqual(['1 series, between 0 and 999']);
    expect(messages(summarizeQueryMetrics([{ query_key: 'big', payload: [{ values: many }] }]))).toEqual(['big: peak 999, average 499.5']);
  });
});

describe('summarizeResources', () => {
  it('states the resource with its status', () => {
    expect(messages(summarizeResources([{ name: 'games-db', type: 'cluster', status: 'Deleted', region: 'us-east-1' }]))).toEqual([
      'games-db · cluster · Deleted · us-east-1',
    ]);
    expect(messages(summarizeResources([{ name: 'a', type: 'instance' }, { name: 'b' }]))).toEqual(['a · instance and 1 more']);
    expect(summarizeResources([])).toEqual([]);
  });
});

describe('summarizeAlertLabels', () => {
  it('names the alert, its severity and what it is about', () => {
    const rows = [
      { label: 'alertname', value: 'PostgreSQLCacheHitRatio' },
      { label: 'severity', value: 'warning' },
      { label: 'namespace', value: 'prod' },
      { label: 'pod', value: 'db-0' },
    ];
    expect(messages(summarizeAlertLabels(rows))).toEqual(['PostgreSQLCacheHitRatio · severity warning · prod/db-0']);
    expect(summarizeAlertLabels([{ label: 'severity', value: 'warning' }])).toEqual([]);
  });
});

describe('summarizeDescription', () => {
  it('takes the first line that says something, without markdown marks', () => {
    expect(messages(summarizeDescription('%%%\n\n# grafana Alert - Incident #252107\n\n## Incident Details'))).toEqual([
      'grafana Alert - Incident #252107',
    ]);
    expect(messages(summarizeDescription('**Alert Name:** PostgreSQLCacheHitRatio\n**Alert Source:** grafana'))).toEqual([
      'Alert Name: PostgreSQLCacheHitRatio',
    ]);
    expect(messages(summarizeDescription('x'.repeat(200), 10))).toEqual([`${'x'.repeat(10)}…`]);
    expect(summarizeDescription('')).toEqual([]);
    expect(summarizeDescription(undefined)).toEqual([]);
  });
});

describe('mentionsContainer', () => {
  it('matches the whole container name', () => {
    expect(mentionsContainer({ message: 'Container app peaked at 412 MiB' }, 'app')).toBe(true);
    expect(mentionsContainer('Container app: working set went 17MiB → 18MiB', 'app')).toBe(true);
    expect(mentionsContainer({ message: 'Container app-sidecar peaked at 19 MiB' }, 'app')).toBe(false);
    expect(mentionsContainer({ message: 'Container config-reloader: working set …' }, 'prometheus')).toBe(false);
    expect(mentionsContainer({ message: 'Container a.b peaked' }, 'a.b')).toBe(true);
    expect(mentionsContainer({ message: 'Container app peaked' }, '')).toBe(false);
  });
});
