import { renderHook, waitFor } from '@testing-library/react';
import useKubernetesEventFilters from '@hooks/useKubernetesEventFilters';

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getAllK8sWorkload: jest.fn(),
    getEventFilterValues: jest.fn(),
  },
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: jest.fn(),
  },
}));

jest.mock('@context/DataContext', () => ({
  useData: jest.fn(() => ({ allCluster: [] })),
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
  titleCaseForAggregationKey: jest.fn((s) => s),
}));

import k8sApi from '@api1/kubernetes';
import { useData as _useData } from '@context/DataContext';

const mockGetEventFilters = k8sApi.getEventFilterValues;
const mockGetWorkload = k8sApi.getAllK8sWorkload;

const defaultParams = {
  selectedAccountId: 'acc-1',
  enableFilters: true,
  disabledFilters: [],
  resource_ids: [],
};

// Waits until all loading flags settle to false, ensuring every .finally() callback
// has run inside act() and React's state update warnings are suppressed.
const waitForIdle = (result) =>
  waitFor(() => {
    const l = result.current.isOptionsLoading;
    expect(l.namespace).toBe(false);
    expect(l.workload).toBe(false);
    expect(l.subjectType).toBe(false);
    expect(l.aggregationKey).toBe(false);
    expect(l.source).toBe(false);
    expect(l.nbStatus).toBe(false);
  });

describe('useKubernetesEventFilters', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    mockGetWorkload.mockResolvedValue({ data: [] });
  });

  it('returns initial empty filter arrays', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    expect(result.current.namespaceFilter).toEqual([]);
    expect(result.current.workloadFilter).toEqual([]);
    expect(result.current.aggregationKeyFilter).toEqual([]);
    expect(result.current.sourceFilter).toEqual([]);
    await waitForIdle(result);
  });

  it('initialises isOptionsLoading all to false', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => {
      expect(result.current.isOptionsLoading.namespace).toBe(false);
      expect(result.current.isOptionsLoading.workload).toBe(false);
    });
  });

  it('does not fetch filters when enableFilters is false', () => {
    renderHook(() => useKubernetesEventFilters({ ...defaultParams, enableFilters: false }));
    expect(mockGetEventFilters).not.toHaveBeenCalled();
  });

  it('does not fetch filters when selectedAccountId is falsy', () => {
    renderHook(() => useKubernetesEventFilters({ ...defaultParams, selectedAccountId: '' }));
    expect(mockGetEventFilters).not.toHaveBeenCalled();
  });

  it('fetches filter values when enableFilters=true and accountId provided', async () => {
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-1' })));
    await waitForIdle(result);
  });

  it('populates namespaceFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'namespace', values: [{ value: 'default' }, { value: 'kube-system' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.namespaceFilter).toEqual(['default', 'kube-system']));
    await waitForIdle(result);
  });

  it('populates aggregationKeyFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'aggregation_key', values: [{ value: 'pod_oom_killed' }, { value: 'cpu_throttling' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.aggregationKeyFilter).toHaveLength(2));
    await waitForIdle(result);
  });

  it('populates sourceFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'source', values: [{ value: 'k8s_event' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.sourceFilter).toHaveLength(1));
    await waitForIdle(result);
  });

  it('skips disabled filters from the API request', async () => {
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, disabledFilters: ['namespace', 'workload'] }));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalled());
    const callArg = mockGetEventFilters.mock.calls[0][0];
    expect(callArg.filterTypes).not.toContain('namespace');
    expect(callArg.filterTypes).not.toContain('workload');
    await waitForIdle(result);
  });

  it('fetches workload data when resource_ids are provided', async () => {
    mockGetWorkload.mockResolvedValue({
      data: [{ name: 'my-deploy', namespace: 'default', kind: 'Deployment' }],
    });
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, resource_ids: ['res-1'] }));
    await waitFor(() => expect(mockGetWorkload).toHaveBeenCalledWith(expect.objectContaining({ resource_ids: ['res-1'] })));
    await waitForIdle(result);
  });

  it('sets workload and namespace filters from workload API response', async () => {
    mockGetWorkload.mockResolvedValue({
      data: [
        { name: 'deploy-a', namespace: 'ns-a', kind: 'Deployment' },
        { name: 'deploy-b', namespace: 'ns-b', kind: 'StatefulSet' },
      ],
    });
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, resource_ids: ['res-1'] }));
    await waitFor(() => expect(result.current.workloadFilter).toHaveLength(2));
    expect(result.current.namespaceFilter).toContain('ns-a');
    expect(result.current.namespaceFilter).toContain('ns-b');
    await waitForIdle(result);
  });

  it('returns accountType as "K8s" by default', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    expect(result.current.accountType).toBe('K8s');
    await waitForIdle(result);
  });

  // --- Cross-filter tests ---

  it('passes subjectName to namespace fetch when selectedWorkload is provided', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, selectedWorkload: 'hive-server' }));
    await waitFor(() =>
      expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['namespace'], subjectName: 'hive-server' }))
    );
    await waitForIdle(result);
  });

  it('passes no subjectName to namespace fetch when selectedWorkload is empty', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, selectedWorkload: '' }));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['namespace'] })));
    const namespaceCalls = mockGetEventFilters.mock.calls.filter((c) => c[0].filterTypes?.includes('namespace'));
    expect(namespaceCalls[0][0].subjectName).toBeFalsy();
    await waitForIdle(result);
  });

  it('passes subjectNamespace to workload fetch when selectedNamespace is provided', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, selectedNamespace: 'hive' }));
    await waitFor(() =>
      expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['workload'], subjectNamespace: 'hive' }))
    );
    await waitForIdle(result);
  });

  it('passes no subjectNamespace to workload fetch when selectedNamespace is empty', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, selectedNamespace: '' }));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['workload'] })));
    const workloadCalls = mockGetEventFilters.mock.calls.filter((c) => c[0].filterTypes?.includes('workload'));
    expect(workloadCalls[0][0].subjectNamespace).toBeFalsy();
    await waitForIdle(result);
  });

  it('re-fetches namespace filter with new subjectName when selectedWorkload changes', async () => {
    const { result, rerender } = renderHook((props) => useKubernetesEventFilters(props), {
      initialProps: { ...defaultParams, selectedWorkload: '' },
    });
    await waitForIdle(result);
    mockGetEventFilters.mockClear();

    rerender({ ...defaultParams, selectedWorkload: 'hive-server' });
    await waitFor(() =>
      expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['namespace'], subjectName: 'hive-server' }))
    );
    await waitForIdle(result);
  });

  it('re-fetches workload filter with new subjectNamespace when selectedNamespace changes', async () => {
    const { result, rerender } = renderHook((props) => useKubernetesEventFilters(props), {
      initialProps: { ...defaultParams, selectedNamespace: '' },
    });
    await waitForIdle(result);
    mockGetEventFilters.mockClear();

    rerender({ ...defaultParams, selectedNamespace: 'hive' });
    await waitFor(() =>
      expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ filterTypes: ['workload'], subjectNamespace: 'hive' }))
    );
    await waitForIdle(result);
  });

  it('does not fetch namespace or workload filters when resource_ids are provided', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, resource_ids: ['res-1'] }));
    await waitFor(() => expect(mockGetWorkload).toHaveBeenCalled());
    await waitForIdle(result);
    const calls = mockGetEventFilters.mock.calls;
    expect(calls.some((c) => c[0].filterTypes?.includes('namespace'))).toBe(false);
    expect(calls.some((c) => c[0].filterTypes?.includes('workload'))).toBe(false);
  });

  it('does not fetch subject_type in effect 4c when resource_ids are provided', async () => {
    const { result } = renderHook(() => useKubernetesEventFilters({ ...defaultParams, resource_ids: ['res-1'] }));
    await waitFor(() => expect(mockGetWorkload).toHaveBeenCalled());
    await waitForIdle(result);
    const otherCalls = mockGetEventFilters.mock.calls.filter(
      (c) => !c[0].filterTypes?.includes('namespace') && !c[0].filterTypes?.includes('workload')
    );
    otherCalls.forEach((call) => {
      expect(call[0].filterTypes).not.toContain('subject_type');
    });
  });

  it('ignores stale namespace response when selectedWorkload changes rapidly', async () => {
    let resolveFirst;
    let resolveSecond;
    mockGetEventFilters
      .mockImplementationOnce(
        () =>
          new Promise((res) => {
            resolveFirst = () => res({ data: { filters: [{ filter_type: 'namespace', values: [{ value: 'stale-ns' }] }] } });
          })
      )
      .mockImplementationOnce(
        () =>
          new Promise((res) => {
            resolveSecond = () => res({ data: { filters: [{ filter_type: 'namespace', values: [{ value: 'fresh-ns' }] }] } });
          })
      )
      .mockResolvedValue({ data: { filters: [] } });

    const { result, rerender } = renderHook((props) => useKubernetesEventFilters(props), {
      initialProps: { ...defaultParams, selectedWorkload: 'workload-a' },
    });

    rerender({ ...defaultParams, selectedWorkload: 'workload-b' });

    // Resolve fresh (second) first, then stale (first)
    resolveSecond();
    resolveFirst();

    await waitFor(() => expect(result.current.namespaceFilter).toEqual(['fresh-ns']));
    expect(result.current.namespaceFilter).not.toContain('stale-ns');
  });
});
