import { timeSeriesToChart } from '@components/llm/cost-analyser/adapt';
import { bucketKey } from '@components/llm/cost-analyser/format';

const row = (bucket: string, cost_usd: number) => ({ bucket, key: 'gpt', cost_usd, requests: 1 });
const series = (rows: ReturnType<typeof row>[]) => ({ granularity: 'week', by_dimension: { model: rows, source: [], agent: [] } } as any);

describe('week bucketing matches the backend (Postgres date_trunc week, Monday-first)', () => {
  it('labels Sunday with the previous ISO week and Monday with the next', () => {
    expect(bucketKey('2026-10-18T00:00:00Z', 'week')).toBe('W42');
    expect(bucketKey('2026-10-19T00:00:00Z', 'week')).toBe('W43');
    expect(bucketKey('2026-10-25T00:00:00Z', 'week')).toBe('W43');
  });

  it('follows ISO year rollover', () => {
    expect(bucketKey('2026-12-31T00:00:00Z', 'week')).toBe('W53');
    expect(bucketKey('2027-01-01T00:00:00Z', 'week')).toBe('W53');
    expect(bucketKey('2027-01-04T00:00:00Z', 'week')).toBe('W1');
  });

  it('keeps Sunday spend when a short range straddles Sunday → Monday', () => {
    // Backend buckets Sun 18-Oct under Mon 12-Oct and Mon 19-Oct under itself.
    const ts = series([row('2026-10-12T00:00:00Z', 6), row('2026-10-19T00:00:00Z', 0.7)]);
    const { buckets } = timeSeriesToChart(ts, 'model', 'cost', '2026-10-18', '2026-10-19', 'week');
    expect(buckets.map((b) => b.label)).toEqual(['W42', 'W43']);
    expect(buckets.reduce((sum, b) => sum + b.total, 0)).toBeCloseTo(6.7);
  });
});
