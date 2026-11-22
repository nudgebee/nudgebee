import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import { usePanelData } from '../usePanelData';
import { MAX_CHART_SERIES, PANEL_TIMEOUT_MS } from '../panelBounds';
import type { AccountOption, Panel } from '@api1/dashboards';

const metricsQuery = jest.fn();

jest.mock('@api1/observability', () => ({
  __esModule: true,
  default: { metricsQuery: (...args: unknown[]) => metricsQuery(...args), fetchLogs: jest.fn() },
}));

jest.mock('@api1/dashboards', () => ({
  __esModule: true,
  default: {},
  isCommandDatasource: () => false,
}));

// One resolvable account, so the fetch effect gets past its scope guard.
jest.mock('../panelAccounts', () => ({
  resolvePanelAccounts: () => [{ value: 'acc-1', label: 'Account 1' }],
  panelQueryAccounts: () => ({ accounts: [{ value: 'acc-1', label: 'Account 1' }] }),
}));

const accounts = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];
const HOUR = 60 * 60 * 1000;

function metricsPanel(type: Panel['type']): Panel {
  return {
    id: 1,
    title: 'cpu',
    type,
    datasource: 'metrics',
    account_ids: ['acc-1'],
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: [{ ref_id: 'A', expr: 'sum by (pod)(rate(container_cpu_usage_seconds_total[5m]))' }],
  } as Panel;
}

/** A provider answer with `count` series of one point each. */
function answerWith(count: number) {
  const payload = Array.from({ length: count }, (_v, i) => ({
    metric: { pod: `pod-${i}` },
    timestamps: [1_700_000_000],
    values: [i],
  }));
  return { data: { data: { metrics_list: { results: [{ query_key: 'A', payload }] } } } };
}

// `immediate` skips the admission queue: these tests are about the request and
// its answer, not about when the panel is allowed to send it.
const render = (panel: Panel) => renderHook(() => usePanelData({ panel, accounts, variables: {}, startTime: 0, endTime: HOUR, immediate: true }));

describe('usePanelData bounds the request', () => {
  beforeEach(() => metricsQuery.mockReset());
  afterEach(cleanup);

  it('asks a chart for a range sized to the panel, with a step', async () => {
    metricsQuery.mockResolvedValue(answerWith(1));
    const { result } = render(metricsPanel('timeseries'));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    const [request, signal] = metricsQuery.mock.calls[0];
    expect(request.instant).toBe(false);
    // 1h at ~200 points → 30s.
    expect(request.step_interval).toBe(30);
    expect(signal).toBeInstanceOf(AbortSignal);
  });

  it('asks a table for one instant sample rather than a whole range', async () => {
    metricsQuery.mockResolvedValue(answerWith(1));
    const { result } = render(metricsPanel('table'));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    const [request] = metricsQuery.mock.calls[0];
    // Before: a range query whose every point but the last was thrown away —
    // a 24h increase recomputed at every step of the range.
    expect(request.instant).toBe(true);
    expect(request.step_interval).toBeUndefined();
  });
});

describe('usePanelData bounds the answer', () => {
  beforeEach(() => metricsQuery.mockReset());
  afterEach(cleanup);

  it('draws the busiest series and says how many it left out', async () => {
    metricsQuery.mockResolvedValue(answerWith(4798));
    const { result } = render(metricsPanel('timeseries'));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.data?.series).toHaveLength(MAX_CHART_SERIES);
    // The busiest survive: values are 0..4797, so the top of the range.
    // One label collapses to its value — `pod-4778`, not `{pod="pod-4778"}`.
    expect(result.current.data?.series[0].label).toBe(`pod-${4798 - MAX_CHART_SERIES}`);
    expect(result.current.warning).toContain(`${MAX_CHART_SERIES} busiest of 4798`);
    expect(result.current.error).toBeNull();
  });

  it('bins every series for a histogram rather than capping to the busiest, with no warning', async () => {
    metricsQuery.mockResolvedValue(answerWith(4798));
    const { result } = render(metricsPanel('histogram'));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    // A histogram pools values into buckets and draws no per-series mark, so the
    // chart cap does not apply — it reads all the series, and nothing is "left out".
    expect(result.current.data?.series).toHaveLength(4798);
    expect(result.current.warning).toBeNull();
    expect(result.current.error).toBeNull();
  });

  it('draws everything when the answer is small, with no warning', async () => {
    metricsQuery.mockResolvedValue(answerWith(3));
    const { result } = render(metricsPanel('timeseries'));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.data?.series).toHaveLength(3);
    expect(result.current.warning).toBeNull();
  });
});

describe('usePanelData gives up on a request that never answers', () => {
  beforeEach(() => {
    metricsQuery.mockReset();
    jest.useFakeTimers();
  });
  afterEach(() => {
    cleanup();
    jest.useRealTimers();
  });

  it('does not raise the deadline against a panel that has already answered', async () => {
    metricsQuery.mockResolvedValue(answerWith(3));
    const { result } = render(metricsPanel('timeseries'));
    // Let the resolved promise chain run under fake timers.
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });
    expect(result.current.data?.series).toHaveLength(3);

    await act(async () => {
      jest.advanceTimersByTime(PANEL_TIMEOUT_MS + 1);
    });

    // The clock was not cleared on settle at first, so every panel showed
    // "No answer after 30s" half a minute after it had drawn.
    expect(result.current.error).toBeNull();
    expect(result.current.data?.series).toHaveLength(3);
    const [, signal] = metricsQuery.mock.calls[0];
    expect((signal as AbortSignal).aborted).toBe(false);
  });

  it('does not raise the deadline against a panel that never sent a request', async () => {
    // Every target renders to an empty expression: the effect settles without
    // a request. The clock was armed before that check, so it used to replace
    // the empty answer with "No answer after 30s".
    const panel = { ...metricsPanel('timeseries'), targets: [{ ref_id: 'A', expr: '   ' }] } as Panel;
    const { result } = render(panel);
    expect(result.current.data?.series).toEqual([]);
    expect(metricsQuery).not.toHaveBeenCalled();

    await act(async () => {
      jest.advanceTimersByTime(PANEL_TIMEOUT_MS + 1);
    });

    expect(result.current.error).toBeNull();
    expect(result.current.data?.series).toEqual([]);
  });

  it('aborts at the deadline and leaves a retryable error, not a skeleton', async () => {
    metricsQuery.mockImplementation(() => new Promise(() => {}));
    const { result } = render(metricsPanel('timeseries'));
    expect(result.current.loading).toBe(true);

    await act(async () => {
      jest.advanceTimersByTime(PANEL_TIMEOUT_MS + 1);
    });

    const [, signal] = metricsQuery.mock.calls[0];
    expect((signal as AbortSignal).aborted).toBe(true);
    expect(result.current.loading).toBe(false);
    expect(result.current.error?.kind).toBe('failed');
    expect(result.current.error?.message).toMatch(/30s/);
    expect(result.current.data).toBeNull();
  });
});

/*
 * On a capped chart the thresholds decide which lines are kept. Editing a step
 * must re-rank the lines already fetched — without asking the provider again.
 */
describe('usePanelData keeps the lines a chart’s thresholds need', () => {
  beforeEach(() => metricsQuery.mockReset());
  afterEach(cleanup);

  it('re-ranks the fetched lines when a step changes, without refetching', async () => {
    const count = MAX_CHART_SERIES + 5;
    // pod-0 is the quietest line, so a peak ranking drops it first.
    const QUIET = 'pod-0';
    metricsQuery.mockResolvedValue(answerWith(count));
    const plain = metricsPanel('timeseries');
    const { result, rerender } = renderHook(
      ({ panel }: { panel: Panel }) => usePanelData({ panel, accounts, variables: {}, startTime: 0, endTime: HOUR, immediate: true }),
      { initialProps: { panel: plain } }
    );
    await waitFor(() => expect(result.current.data?.series.length).toBe(MAX_CHART_SERIES));
    expect(result.current.data?.series.map((s) => s.label)).not.toContain(QUIET);

    const withStep = { ...plain, options: { thresholds: [{ value: 1, color: 'red', op: 'lt' }] } } as Panel;
    await act(async () => rerender({ panel: withStep }));
    await waitFor(() => expect(result.current.data?.series.map((s) => s.label)).toContain(QUIET));
    expect(metricsQuery).toHaveBeenCalledTimes(1);
    expect(result.current.data?.capped).toEqual({ shown: MAX_CHART_SERIES, total: count });
  });
});
