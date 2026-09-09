import { capSeries, cappedSeriesWarning, MAX_CHART_SERIES, panelStep } from '../panelBounds';
import type { RawSeries } from '../panelSeries';

const HOUR = 60 * 60 * 1000;

describe('panelStep', () => {
  it('sizes the step to about 200 points, snapped up to a clean interval', () => {
    // 1h / 200 = 18s → 30s (120 points). The agent's own default is 60s.
    expect(panelStep(0, HOUR)).toBe(30);
    // 24h / 200 = 432s → 10m (144 points).
    expect(panelStep(0, 24 * HOUR)).toBe(600);
    // 7d / 200 = 3024s → 1h (168 points). Before: 60s, 10,080 points.
    expect(panelStep(0, 7 * 24 * HOUR)).toBe(3600);
  });

  it('never goes below the smallest rung or above the largest', () => {
    expect(panelStep(0, 0)).toBe(15);
    expect(panelStep(5, 0)).toBe(15);
    expect(panelStep(0, 400 * 24 * HOUR)).toBe(86400);
  });

  it('honours a different point budget', () => {
    expect(panelStep(0, HOUR, 60)).toBe(60);
    expect(panelStep(0, HOUR, 1000)).toBe(15);
  });
});

function series(label: string, values: (number | null)[]): RawSeries {
  return { label, timestamps: values.map((_v, i) => i), values };
}

describe('capSeries', () => {
  it('leaves a list under the limit alone, same array', () => {
    const raw = [series('a', [1]), series('b', [2])];
    const { kept, dropped } = capSeries(raw, 2);
    expect(kept).toBe(raw);
    expect(dropped).toBe(0);
  });

  it('keeps the busiest series and reports the rest', () => {
    const raw = [series('quiet', [0.1, 0.2]), series('loud', [5, 90]), series('mid', [4, 4]), series('negative', [-100, 0])];
    const { kept, dropped } = capSeries(raw, 2);
    // Magnitude, not sign: a large negative swing is as much a line worth
    // seeing as a large positive one.
    expect(kept.map((s) => s.label)).toEqual(['loud', 'negative']);
    expect(dropped).toBe(2);
  });

  it('keeps the provider order among the survivors, and is stable on ties', () => {
    const raw = [series('c', [3]), series('a', [3]), series('b', [3]), series('d', [1])];
    const { kept } = capSeries(raw, 3);
    expect(kept.map((s) => s.label)).toEqual(['c', 'a', 'b']);
  });

  it('ranks a series with no numbers last', () => {
    const raw = [series('gaps', [null, null]), series('one', [1])];
    const { kept } = capSeries(raw, 1);
    expect(kept.map((s) => s.label)).toEqual(['one']);
  });

  it('orders two all-null series by provider order, not by NaN', () => {
    // Both peak at -Infinity. Subtracting them gives NaN, which a comparator is
    // not defined for — so the tie has to be decided by index explicitly.
    const raw = [series('gaps-a', [null, null]), series('gaps-b', [null, null]), series('one', [1])];
    const { kept, dropped } = capSeries(raw, 2);
    expect(kept.map((s) => s.label)).toEqual(['gaps-a', 'one']);
    expect(dropped).toBe(1);
  });

  it('default chart cap is small enough to draw and read', () => {
    expect(MAX_CHART_SERIES).toBeLessThanOrEqual(50);
  });
});

describe('cappedSeriesWarning', () => {
  it('names both numbers and says how to see the rest', () => {
    const text = cappedSeriesWarning(20, 4798);
    expect(text).toContain('20');
    expect(text).toContain('4798');
    expect(text).toMatch(/aggregation/);
  });
});
