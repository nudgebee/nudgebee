import { perPodCaption, toPerPodTrend } from '@components/optimise-new/evidence/perPodTrend';

const row = (overrides: Record<string, any> = {}) => ({
  timestamp: '2026-09-09T10:00:00Z',
  avg_cpu_used: 3,
  avg_cpu_request: 1.5,
  avg_cpu_limit: 6,
  avg_memory_used: 6000,
  avg_memory_request: 3000,
  avg_memory_limit: 6000,
  ...overrides,
});

describe('toPerPodTrend', () => {
  it('divides every total by the measured pod count at that point', () => {
    const trend = toPerPodTrend([row({ pod_count: 6, max_pod_cpu_used: 0.9 }), row({ pod_count: 3, avg_cpu_used: 1.5 })], 4);
    expect(trend.source).toBe('measured');
    expect(trend.rows[0]).toMatchObject({ avg_cpu_used: 0.5, avg_cpu_request: 0.25, avg_cpu_limit: 1, avg_memory_used: 1000 });
    expect(trend.rows[1]).toMatchObject({ avg_cpu_used: 0.5, avg_cpu_limit: 2 });
    expect(trend.replicas).toBe(4.5);
    expect(trend.hasBusiestPod).toBe(true);
    expect(trend.rows[0].max_pod_cpu_used).toBe(0.9);
  });

  it('falls back to the replica window when the provider gave no count', () => {
    const trend = toPerPodTrend([row(), row()], 3);
    expect(trend.source).toBe('window');
    expect(trend.rows[0].avg_cpu_used).toBe(1);
    expect(trend.rows[0].avg_memory_limit).toBe(2000);
    expect(trend.replicas).toBe(3);
    expect(trend.hasBusiestPod).toBe(false);
  });

  it('leaves totals alone and says so when there is nothing to divide by', () => {
    const rows = [row()];
    const trend = toPerPodTrend(rows, null);
    expect(trend.source).toBeNull();
    expect(trend.rows).toBe(rows);
    expect(perPodCaption(trend)).toBeNull();
  });

  it('uses the window for points that lack a count when others have one', () => {
    const trend = toPerPodTrend([row({ pod_count: 2 }), row({ pod_count: null })], 4);
    expect(trend.rows[0].avg_cpu_used).toBe(1.5);
    expect(trend.rows[1].avg_cpu_used).toBe(0.75);
  });

  it('captions the two sources differently', () => {
    expect(perPodCaption(toPerPodTrend([row({ pod_count: 6 })], null))).toBe(
      'Average per pod across 6 running pods; the busiest pod is drawn separately.'
    );
    expect(perPodCaption(toPerPodTrend([row()], 2.5))).toBe('Workload total divided by the 2.5-pod average the recommendation was priced at.');
  });

  it('leaves a gap, not a raw total, at points with no count and no fallback', () => {
    const trend = toPerPodTrend([row({ pod_count: 6 }), row({ pod_count: null })], null);
    expect(trend.source).toBe('measured');
    expect(trend.rows[0].avg_cpu_used).toBe(0.5);
    expect(trend.rows[1]).toMatchObject({ avg_cpu_used: null, avg_cpu_request: null, avg_memory_limit: null });
    expect(trend.rows[1].timestamp).toBe(trend.rows[0].timestamp);
  });

  it('divides by the pods running now when the row has no replica window', () => {
    const trend = toPerPodTrend([row()], null, 3);
    expect(trend.source).toBe('live');
    expect(trend.rows[0].avg_cpu_used).toBe(1);
    expect(perPodCaption(trend)).toBe('Workload total divided by the 3 pods running now.');
    expect(perPodCaption(toPerPodTrend([row()], null, 1))).toBe('Workload total divided by the 1 pod running now.');
  });

  it('prefers the replica window over the live count', () => {
    expect(toPerPodTrend([row()], 2, 3).source).toBe('window');
  });

  it('counts an idle busiest pod as a data point', () => {
    expect(toPerPodTrend([row({ pod_count: 2, max_pod_cpu_used: 0 })], null).hasBusiestPod).toBe(true);
    expect(toPerPodTrend(null as any, 2).rows).toEqual([]);
  });
});
