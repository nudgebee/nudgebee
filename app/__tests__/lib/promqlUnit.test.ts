import { formatMetricValue, inferPromqlUnit, scaleSeriesToUnit } from '@lib/promqlUnit';

describe('inferPromqlUnit', () => {
  it.each([
    // Gauges named in their base unit.
    ['container_memory_working_set_bytes{namespace="shop", pod="cart-7d9f"}', 'bytes'],
    ['sum(container_memory_working_set_bytes{namespace="shop", container!=""}) by (pod)', 'bytes'],
    ['sum by (pod) (container_memory_working_set_bytes{container=~"app_seconds.*"})', 'bytes'],
    ['max_over_time(container_memory_working_set_bytes{pod="cart-7d9f"}[1h])', 'bytes'],
    ['node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes', 'bytes'],
    ['container_memory_working_set_bytes > 1e9', 'bytes'],
    ['go_gc_duration_seconds{quantile="0.99"}', 'seconds'],
    ['node_filesystem_files_free_ratio', 'ratio'],
    ['disk_used_percent{host="db-1"}', 'percent'],
    ['avg(cpu_usage_percentage)', 'percent'],
    // Counters under rate / irate.
    ['rate(container_cpu_usage_seconds_total{pod="cart-7d9f", container!=""}[5m])', 'cores'],
    ['sum(rate(container_cpu_usage_seconds_total{namespace="shop"}[5m])) by (pod)', 'cores'],
    ['sum by (mode) (irate(node_cpu_seconds_total{mode!="idle"}[2m]))', 'cores'],
    ['rate(process_cpu_seconds_total[5m])', 'cores'],
    ['avg_over_time(rate(container_cpu_usage_seconds_total[5m])[30m:1m])', 'cores'],
    ['rate(container_network_receive_bytes_total{pod="cart-7d9f"}[5m])', 'bytes/s'],
    ['sum(rate(http_requests_total{status=~"5.."}[5m]))', 'per_second'],
    ['sum(rate(http_requests_total[5m])) + sum(rate(grpc_requests_total[5m]))', 'per_second'],
    ['rate(http_request_duration_seconds_count[5m])', 'per_second'],
    // Counters under increase.
    ['increase(kube_pod_container_status_restarts_total{pod="cart-7d9f"}[1h])', 'count'],
    ['increase(container_network_transmit_bytes_total[1h])', 'bytes'],
    // Histogram quantiles take the bucket's base unit.
    ['histogram_quantile(0.99, sum(rate(http_server_requests_seconds_bucket{service="cart"}[5m])) by (le))', 'seconds'],
    ['histogram_quantile(0.5, sum by (le) (rate(response_size_bytes_bucket[5m])))', 'bytes'],
  ])('%s → %s', (query, unit) => {
    expect(inferPromqlUnit(query)).toBe(unit);
  });

  it.each([
    // Division and multiplication change the quantity — no guessing.
    'container_memory_working_set_bytes / 1024 / 1024',
    'sum(container_memory_working_set_bytes) / sum(kube_pod_container_resource_limits{resource="memory"})',
    'node_filesystem_avail_bytes / node_filesystem_size_bytes',
    '100 * (1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m])))',
    'rate(http_requests_total[5m]) * 60',
    'rate(http_request_duration_seconds_sum[5m]) / rate(http_request_duration_seconds_count[5m])',
    // A literal combined with a series.
    '1 - avg(rate(node_cpu_seconds_total{mode="idle"}[5m]))',
    'container_memory_working_set_bytes + 100',
    // No unit in the name.
    'kube_deployment_status_replicas_available{deployment="cart"}',
    'kube_pod_container_resource_limits{resource="memory"}',
    'up',
    // Epoch timestamps, not durations.
    'process_start_time_seconds',
    'node_boot_time_seconds{instance="10.0.0.1:9100"}',
    // Bare counters, and counters under the wrong function.
    'container_network_receive_bytes_total',
    'rate(container_memory_working_set_bytes[5m])',
    'rate(node_disk_io_time_seconds_total[5m])',
    'sum by (pod) (rate(container_cpu_cfs_throttled_seconds_total[5m]))',
    'rate(a_total[5m]) + b_total',
    'rate(a_total[5m]) + increase(b_total[5m])',
    // Functions whose unit is not their argument's.
    'count(kube_pod_info)',
    'time() - process_start_time_seconds',
    'sum_over_time(container_memory_working_set_bytes[1h])',
    'container_memory_working_set_bytes > bool 1e9',
    // Mixed units.
    'container_memory_working_set_bytes or go_gc_duration_seconds',
    // A bucket outside histogram_quantile.
    'rate(http_server_requests_seconds_bucket[5m])',
    // Recording rules and group names are not read.
    'node_namespace_pod_container:container_cpu_usage_seconds_total:sum_irate',
    'memory_usage',
    '',
    undefined,
    42,
  ])('%s → null', (query) => {
    expect(inferPromqlUnit(query)).toBeNull();
  });
});

describe('formatMetricValue', () => {
  it.each([
    [2147483648, 'bytes', '2 GiB'],
    [536870912, 'bytes', '512 MiB'],
    [20262912, 'bytes', '19.3 MiB'],
    [1536, 'bytes', '1.5 KiB'],
    [512, 'bytes', '512 B'],
    [0, 'bytes', '0 B'],
    [1048576, 'bytes/s', '1 MiB/s'],
    [0.25, 'cores', '250 m'],
    [0.0004, 'cores', '0.4 m'],
    [2.5, 'cores', '2.5 cores'],
    [0.0123, 'seconds', '12.3 ms'],
    [3.2, 'seconds', '3.2 s'],
    [0.873, 'ratio', '87.3%'],
    [45.678, 'percent', '45.7%'],
    [1234.5, 'per_second', '1.23k/s'],
    [0.002, 'per_second', '0.002/s'],
    [3, 'count', '3'],
  ] as const)('%s %s → %s', (raw, unit, text) => {
    expect(formatMetricValue(raw, unit)).toBe(text);
  });

  it('keeps small non-zero values readable when the unit is unknown', () => {
    expect(formatMetricValue(0.000123, null)).toBe('0.000123');
    expect(formatMetricValue(0.3000000004, null)).toBe('0.3');
    expect(formatMetricValue(2147483648, null)).toBe('2.15G');
    expect(formatMetricValue(999999, null)).toBe('1M');
    expect(formatMetricValue(-42.5, null)).toBe('-42.5');
    expect(formatMetricValue(NaN, null)).toBe('-');
  });
});

describe('scaleSeriesToUnit', () => {
  it('converts every series into the unit of the largest value', () => {
    const { datasets, formatValue } = scaleSeriesToUnit(
      [
        { label: 'pod=a', data: [1073741824, 2147483648] },
        { label: 'pod=b', data: [5242880, NaN] },
      ],
      'bytes'
    );
    expect(datasets[0].data).toEqual([1, 2]);
    expect(datasets[1].data?.[0]).toBeCloseTo(0.00488, 5);
    expect(datasets[1].data?.[1]).toBeNaN();
    expect(datasets[0].label).toBe('pod=a');
    expect(formatValue(1.5)).toBe('1.5 GiB');
    expect(formatValue(datasets[1].data?.[0] as number)).toBe('0.00488 GiB');
  });

  it('shows sub-core CPU in millicores', () => {
    const { datasets, formatValue } = scaleSeriesToUnit([{ data: [0.012, 0.25] }], 'cores');
    expect(datasets[0].data).toEqual([12, 250]);
    expect(formatValue(250)).toBe('250 m');
  });

  it('renders a 0-1 ratio as a percentage', () => {
    const { datasets, formatValue } = scaleSeriesToUnit([{ data: [0.5, 0.95] }], 'ratio');
    expect(datasets[0].data).toEqual([50, 95]);
    expect(formatValue(95)).toBe('95%');
  });

  it('leaves unknown-unit series as they are and only formats them', () => {
    const input = [{ data: [0.0001, 12500] }];
    const { datasets, formatValue } = scaleSeriesToUnit(input, null);
    expect(datasets).toBe(input);
    expect(formatValue(0.0001)).toBe('0.0001');
    expect(formatValue(12500)).toBe('12.5k');
  });
});
