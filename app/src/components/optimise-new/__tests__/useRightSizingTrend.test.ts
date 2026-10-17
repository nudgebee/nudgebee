import { act, renderHook, waitFor } from '@testing-library/react';
import k8sApi from '@api1/kubernetes';
import { useRightSizingTrend } from '@hooks/useRightSizingTrend';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: { getK8sPodGroupings2: jest.fn() },
}));

const fetchTrend = k8sApi.getK8sPodGroupings2 as jest.Mock;

const MB = 1024 * 1024;

const entry = (resource: string, allocated: number, recommended: number, historyHours?: number) => ({
  resource,
  allocated: { request: allocated, limit: allocated },
  recommended: { request: recommended, limit: recommended },
  ...(historyHours ? { strategy: { settings: { history_duration: historyHours } } } : {}),
});

const DAY_MS = 24 * 60 * 60 * 1000;
// Days between the start and end the n-th fetch asked for.
const fetchedDays = (call = 0) => {
  const [, query] = fetchTrend.mock.calls[call];
  return Math.round((query.endDate.getTime() - query.startDate.getTime()) / DAY_MS);
};

const rightSizingRec = (id = 'rec-1', historyHours?: number) => ({
  id,
  account_id: 'acct-1',
  rule_name: 'pod_right_sizing',
  resource_type: 'Deployment',
  resource_name: 'api',
  resource_k8s_namespace: 'payments',
  cloud_resourse: { type: 'Deployment', name: 'api', meta: { total_pods: 2 } },
  recommendation: { api: [entry('cpu', 1, 0.5, historyHours), entry('memory', 2048 * MB, 512 * MB, historyHours)] },
});

const rows = (n: number) =>
  Array.from({ length: n }, (_, i) => ({
    timestamp: `2026-09-18T${String(i).padStart(2, '0')}:00:00Z`,
    avg_cpu_used: 0.8,
    avg_memory_used: 600 * MB,
    pod_count: 2,
  }));

beforeEach(() => {
  fetchTrend.mockReset();
});

describe('useRightSizingTrend', () => {
  it('tolerates the drawer rendering with no recommendation at all', () => {
    const { result, rerender } = renderHook(({ rec }) => useRightSizingTrend(rec), { initialProps: { rec: null as any } });
    expect(fetchTrend).not.toHaveBeenCalled();
    expect(result.current.hasData).toBe(false);
    expect(result.current.loading).toBe(false);
    rerender({ rec: undefined as any });
    expect(fetchTrend).not.toHaveBeenCalled();
  });

  it('never fetches for a recommendation with no container data', () => {
    const { result } = renderHook(() => useRightSizingTrend({ id: 'r', account_id: 'a', recommendation: { reason: 'unused' } }));
    expect(fetchTrend).not.toHaveBeenCalled();
    expect(result.current.hasData).toBe(false);
    expect(result.current.loading).toBe(false);
  });

  it('fetches once per recommendation, scoped to the first container, and divides to per pod', async () => {
    fetchTrend.mockResolvedValueOnce({ data: { k8s_pod_groupings: rows(3), promQueries: { cpu_usage: 'q1', memory_usage: 'q2', pod_count: 'q3' } } });
    const { result } = renderHook(() => useRightSizingTrend(rightSizingRec()));

    await waitFor(() => expect(result.current.hasData).toBe(true));
    expect(fetchTrend).toHaveBeenCalledTimes(1);
    const [, query, groupBy, datasource] = fetchTrend.mock.calls[0];
    expect(datasource).toBe('prometheus');
    expect(query.containerName).toBe('api');
    expect(query.workloadName).toBe('api');
    expect(groupBy).toEqual(expect.arrayContaining(['namespace_name', 'workload_name']));
    // Per pod: the 0.8 total is divided by the measured count of 2.
    expect(result.current.perPod.source).toBe('measured');
    expect(result.current.perPod.rows[0].avg_cpu_used).toBeCloseTo(0.4);
    expect(result.current.cpuQueries).toEqual({ cpu_usage: 'q1', pod_count: 'q3' });
    expect(result.current.memoryQueries).toEqual({ memory_usage: 'q2', pod_count: 'q3' });
    expect(result.current.loading).toBe(false);
  });

  it('falls back to the nb datasource when prometheus returns nothing', async () => {
    fetchTrend.mockResolvedValueOnce({ data: { k8s_pod_groupings: [] } }).mockResolvedValueOnce({ data: { k8s_pod_groupings: rows(2) } });
    const { result } = renderHook(() => useRightSizingTrend(rightSizingRec()));

    await waitFor(() => expect(result.current.hasData).toBe(true));
    expect(fetchTrend).toHaveBeenCalledTimes(2);
    expect(fetchTrend.mock.calls[1][3]).toBe('nb');
  });

  it('drops the previous rows the moment the drawer swaps to another recommendation', async () => {
    fetchTrend.mockResolvedValueOnce({ data: { k8s_pod_groupings: rows(2) } });
    let pending: (v: any) => void = () => {};
    fetchTrend.mockImplementationOnce(() => new Promise((resolve) => (pending = resolve)));

    const { result, rerender } = renderHook(({ rec }) => useRightSizingTrend(rec), { initialProps: { rec: rightSizingRec('rec-1') } });
    await waitFor(() => expect(result.current.hasData).toBe(true));

    rerender({ rec: rightSizingRec('rec-2') });
    // The old trend is gone before the new one arrives — a stale chart under a
    // new verdict would read as that verdict's evidence.
    expect(result.current.hasData).toBe(false);
    expect(result.current.loading).toBe(true);

    pending({ data: { k8s_pod_groupings: rows(1) } });
    await waitFor(() => expect(result.current.hasData).toBe(true));
    expect(result.current.perPod.rows).toHaveLength(1);
  });

  it('does not refetch, or reset, when a parent hands down a fresh but equal object each render', async () => {
    fetchTrend.mockResolvedValueOnce({ data: { k8s_pod_groupings: rows(2) } });
    const { result, rerender } = renderHook(({ rec }) => useRightSizingTrend(rec), { initialProps: { rec: rightSizingRec() } });
    await waitFor(() => expect(result.current.hasData).toBe(true));

    rerender({ rec: rightSizingRec() });
    rerender({ rec: rightSizingRec() });
    expect(fetchTrend).toHaveBeenCalledTimes(1);
    expect(result.current.hasData).toBe(true);
  });

  it('does not fetch without the account, namespace and workload the query needs', () => {
    const rec = { ...rightSizingRec(), resource_k8s_namespace: '', cloud_resourse: { type: 'Deployment', name: 'api', meta: {} } };
    const { result } = renderHook(() => useRightSizingTrend(rec));
    expect(fetchTrend).not.toHaveBeenCalled();
    expect(result.current.loading).toBe(false);
  });

  it('starts the range at the window the recommendation was computed over', async () => {
    fetchTrend.mockResolvedValue({ data: { k8s_pod_groupings: rows(1) } });
    const { result } = renderHook(() => useRightSizingTrend(rightSizingRec('rec-1', 336)));
    await waitFor(() => expect(result.current.hasData).toBe(true));
    expect(result.current.rangeDays).toBe(14);
    expect(fetchedDays()).toBe(14);
  });

  it('reads the window off the replica summary when the strategy settings are missing', () => {
    fetchTrend.mockResolvedValue({ data: { k8s_pod_groupings: [] } });
    const rec = rightSizingRec();
    rec.recommendation.api[0] = { ...rec.recommendation.api[0], add_info: { replicas: { avg: 2, min: 2, max: 2, window_hours: 720 } } } as any;
    const { result } = renderHook(() => useRightSizingTrend(rec));
    expect(result.current.rangeDays).toBe(30);
  });

  it('keeps the week for a recommendation that stamps no window', () => {
    fetchTrend.mockResolvedValue({ data: { k8s_pod_groupings: [] } });
    const { result } = renderHook(() => useRightSizingTrend(rightSizingRec()));
    expect(result.current.rangeDays).toBe(7);
    expect(fetchedDays()).toBe(7);
  });

  it('refetches over a picked range, and the next recommendation starts from its own window', async () => {
    fetchTrend.mockResolvedValue({ data: { k8s_pod_groupings: rows(1) } });
    const { result, rerender } = renderHook(({ rec }) => useRightSizingTrend(rec), { initialProps: { rec: rightSizingRec('rec-1', 336) } });
    await waitFor(() => expect(result.current.hasData).toBe(true));

    act(() => result.current.setRangeDays(30));
    expect(result.current.rangeDays).toBe(30);
    await waitFor(() => expect(fetchTrend).toHaveBeenCalledTimes(2));
    expect(fetchedDays(1)).toBe(30);

    rerender({ rec: rightSizingRec('rec-2', 168) });
    expect(result.current.rangeDays).toBe(7);
    await waitFor(() => expect(fetchTrend).toHaveBeenCalledTimes(3));
    expect(fetchedDays(2)).toBe(7);
  });
});
