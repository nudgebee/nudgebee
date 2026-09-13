import { accountCappedWarning, capSeries, cappedSeriesWarning, capSeriesByAccount, MAX_CHART_SERIES, panelStep } from '../panelBounds';
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

describe('capSeriesByAccount', () => {
  /** `n` series for one account, ascending peaks so the busiest are the last. */
  const account = (name: string, n: number): RawSeries[] =>
    Array.from({ length: n }, (_, i) => ({ label: `${name}-${i}`, accountLabel: name, timestamps: [1], values: [i + 1] }));

  it('gives every account an equal share instead of ranking them against each other', () => {
    // 177 / 200 / 350 series across three accounts. Ranked in one pool, the
    // account with the largest numbers takes every slot and the other two vanish
    // from the chart with nothing saying they were queried.
    const raw = [...account('A', 177), ...account('B', 200), ...account('C', 350)];
    const { kept, perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);

    const share = Math.floor(MAX_CHART_SERIES / 3);
    expect(perAccount).toEqual([
      { account: 'A', kept: share, total: 177 },
      { account: 'B', kept: share, total: 200 },
      { account: 'C', kept: share, total: 350 },
    ]);
    expect(kept).toHaveLength(share * 3);
    // Every account is represented — the whole point of splitting the budget.
    expect(new Set(kept.map((s) => s.accountLabel))).toEqual(new Set(['A', 'B', 'C']));
  });

  it('keeps the BUSIEST of each account, not the first it was handed', () => {
    const { kept } = capSeriesByAccount([...account('A', 5), ...account('B', 5)], 4);
    // Peaks ascend with the index, so the busiest two of five are -3 and -4.
    expect(kept.map((s) => s.label)).toEqual(['A-3', 'A-4', 'B-3', 'B-4']);
  });

  it('never starves an account, even when there are more accounts than budget', () => {
    // 25 accounts against a budget of 20 would floor to zero series each — a
    // chart of nothing. One each is the floor.
    const raw = Array.from({ length: 25 }, (_, i) => account(`acct-${i}`, 3)).flat();
    const { kept, perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);
    expect(kept).toHaveLength(25);
    expect(perAccount.every((a) => a.kept === 1)).toBe(true);
  });

  it('keeps the ordinary single-account message rather than "each of 1 accounts"', () => {
    const raw = Array.from({ length: 30 }, (_, i) => ({ label: `s-${i}`, timestamps: [1], values: [i] }));
    const { perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);
    expect(accountCappedWarning(perAccount)).toBe(cappedSeriesWarning(MAX_CHART_SERIES, 30));
  });

  it('degenerates to a plain cap when the panel queried one account', () => {
    const raw = Array.from({ length: 30 }, (_, i) => ({ label: `s-${i}`, timestamps: [1], values: [i] }));
    const { kept, perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);
    expect(kept).toHaveLength(MAX_CHART_SERIES);
    expect(perAccount).toEqual([{ account: '', kept: MAX_CHART_SERIES, total: 30 }]);
  });

  it('says nothing when nothing was trimmed', () => {
    const { perAccount } = capSeriesByAccount([...account('A', 2), ...account('B', 2)], MAX_CHART_SERIES);
    expect(accountCappedWarning(perAccount)).toBe('');
  });

  it('names each trimmed account and its real count', () => {
    const raw = [...account('A', 177), ...account('B', 200), ...account('C', 350)];
    const { perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);
    expect(accountCappedWarning(perAccount)).toBe(
      'Showing 6 of 177 from A, 6 of 200 from B and 6 of 350 from C. Add an aggregation such as sum by (…), or filter to one account, to see the rest.'
    );
  });

  it('does not claim an account was trimmed when it showed everything it had', () => {
    // 177 and 5 against a share of 10 draws 10 and 5. "10 busiest from each"
    // is false for the second account and overstates what was dropped.
    const { kept, perAccount } = capSeriesByAccount([...account('A', 177), ...account('B', 5)], MAX_CHART_SERIES);
    expect(kept).toHaveLength(15);
    expect(accountCappedWarning(perAccount)).toBe(
      'Showing 10 of 177 from A. Add an aggregation such as sum by (…), or filter to one account, to see the rest.'
    );
  });

  it('summarises rather than listing once more than three accounts are trimmed', () => {
    const raw = Array.from({ length: 4 }, (_, i) => account(`acct-${i}`, 100)).flat();
    const { perAccount } = capSeriesByAccount(raw, MAX_CHART_SERIES);
    expect(accountCappedWarning(perAccount)).toBe(
      'Showing the 5 busiest series from each of 4 accounts — 400 matched. Add an aggregation such as sum by (…), or filter to one account, to see the rest.'
    );
  });
});
