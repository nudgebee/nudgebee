import { cleanup, renderHook, waitFor } from '@testing-library/react';
import { usePanelData } from '../usePanelData';
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

let scoped: AccountOption[] = [];
jest.mock('../panelAccounts', () => ({
  resolvePanelAccounts: () => scoped,
  panelQueryAccounts: () => ({ accounts: scoped }),
}));

const ONE = [{ value: 'acc-1', label: 'Account 1' }] as AccountOption[];
const TWO = [
  { value: 'acc-1', label: 'Account 1' },
  { value: 'acc-2', label: 'Account 2' },
] as AccountOption[];

const SYNTAX = 'prometheus: /api/v1/query_range returned HTTP 422: singleExpr: unexpected token';
const REFUSED = 'dial tcp 10.0.0.1:8429: connect: connection refused';

function panel(type: Panel['type'], exprs = ['sum(rate(']): Panel {
  return {
    id: 1,
    title: 'cpu',
    type,
    datasource: 'metrics',
    account_ids: scoped.map((a) => a.value),
    grid_pos: { x: 0, y: 0, w: 6, h: 8 },
    targets: exprs.map((expr, i) => ({ ref_id: 'AB'[i], expr })),
  } as Panel;
}

/** A key that answered with one series. */
const series = (key: string) => ({ query_key: key, payload: [{ metric: { pod: 'p' }, timestamps: [1_700_000_000], values: [1] }] });
/** A key whose query failed: empty, with the cause — the server's shape since noteFailedKeys. */
const failedKey = (key: string, error: string) => ({
  query_key: key,
  payload: [],
  error,
  note: `Query failed, so this key returned no data: ${error}`,
});
const answer = (...results: unknown[]) => ({ data: { data: { metrics_list: { results } } } });

const render = (p: Panel) =>
  renderHook(() => usePanelData({ panel: p, accounts: scoped, variables: {}, startTime: 0, endTime: 3_600_000, immediate: true }));

describe('usePanelData reads a failed query as a failure, not as no data', () => {
  beforeEach(() => metricsQuery.mockReset());
  afterEach(cleanup);

  it('fails the panel with the provider’s reason when its only query failed', async () => {
    scoped = ONE;
    metricsQuery.mockResolvedValue(answer(failedKey('A', SYNTAX)));
    const { result } = render(panel('timeseries'));
    await waitFor(() => expect(result.current.error).not.toBeNull());

    expect(result.current.error).toEqual({ kind: 'failed', message: 'Could not load data for Account 1.', detail: SYNTAX });
    expect(result.current.data).toBeNull();
  });

  it('names an account whose queries all failed as not answering, and draws the rest', async () => {
    scoped = TWO;
    metricsQuery.mockImplementation(({ account_id }: { account_id: string }) =>
      Promise.resolve(account_id === 'acc-1' ? answer(series('A')) : answer(failedKey('A', REFUSED)))
    );
    const { result } = render(panel('timeseries', ['count(up)']));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.error).toBeNull();
    expect(result.current.data?.failedAccounts).toEqual(['Account 2']);
    expect(result.current.data?.emptyAccounts).toEqual([]);
    expect(result.current.warning).toBe('No answer from Account 2 — showing the rest.');
  });

  it('says which query failed when the panel’s other query answered', async () => {
    scoped = ONE;
    metricsQuery.mockResolvedValue(answer(series('A'), failedKey('B', SYNTAX)));
    const { result } = render(panel('stat', ['count(up)', 'sum(rate(']));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.error).toBeNull();
    expect(result.current.data?.series).toHaveLength(1);
    expect(result.current.warning).toBe('Query B failed — showing the rest.');
  });

  it('names the account a query failed on when several were asked', async () => {
    scoped = TWO;
    metricsQuery.mockImplementation(({ account_id }: { account_id: string }) =>
      Promise.resolve(account_id === 'acc-1' ? answer(series('A'), series('B')) : answer(series('A'), failedKey('B', REFUSED)))
    );
    const { result } = render(panel('timeseries', ['count(up)', 'sum(up)']));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.warning).toBe('Query B failed on Account 2 — showing the rest.');
  });

  it('still reads a query that ran and matched nothing as no data', async () => {
    scoped = ONE;
    metricsQuery.mockResolvedValue(answer({ query_key: 'A', payload: [] }));
    const { result } = render(panel('timeseries', ['up{job="none"}']));
    await waitFor(() => expect(result.current.data).not.toBeNull());

    expect(result.current.error).toBeNull();
    expect(result.current.data?.emptyAccounts).toEqual(['Account 1']);
    expect(result.current.warning).toBeNull();
  });

  it('carries the gateway’s message when the whole request failed', async () => {
    scoped = ONE;
    metricsQuery.mockResolvedValue({ data: { errors: [{ message: 'agent not connected' }], data: null } });
    const { result } = render(panel('timeseries', ['count(up)']));
    await waitFor(() => expect(result.current.error).not.toBeNull());

    expect(result.current.error).toEqual({ kind: 'failed', message: 'Could not load data for Account 1.', detail: 'agent not connected' });
  });

  it('carries the request’s own error when it never got an answer', async () => {
    scoped = ONE;
    metricsQuery.mockRejectedValue(new Error('Network Error'));
    const { result } = render(panel('timeseries', ['count(up)']));
    await waitFor(() => expect(result.current.error).not.toBeNull());

    expect(result.current.error).toEqual({ kind: 'failed', message: 'Could not load data for Account 1.', detail: 'Network Error' });
  });
});
