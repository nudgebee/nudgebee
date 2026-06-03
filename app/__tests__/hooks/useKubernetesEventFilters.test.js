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

describe('useKubernetesEventFilters', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    mockGetWorkload.mockResolvedValue({ data: [] });
  });

  it('returns initial empty filter arrays', () => {
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    expect(result.current.namespaceFilter).toEqual([]);
    expect(result.current.workloadFilter).toEqual([]);
    expect(result.current.aggregationKeyFilter).toEqual([]);
    expect(result.current.sourceFilter).toEqual([]);
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
    renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-1' })));
  });

  it('populates namespaceFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'namespace', values: [{ value: 'default' }, { value: 'kube-system' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.namespaceFilter).toEqual(['default', 'kube-system']));
  });

  it('populates aggregationKeyFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'aggregation_key', values: [{ value: 'pod_oom_killed' }, { value: 'cpu_throttling' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.aggregationKeyFilter).toHaveLength(2));
  });

  it('populates sourceFilter from API response', async () => {
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [{ filter_type: 'source', values: [{ value: 'k8s_event' }] }],
      },
    });
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    await waitFor(() => expect(result.current.sourceFilter).toHaveLength(1));
  });

  it('skips disabled filters from the API request', async () => {
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    renderHook(() => useKubernetesEventFilters({ ...defaultParams, disabledFilters: ['namespace', 'workload'] }));
    await waitFor(() => expect(mockGetEventFilters).toHaveBeenCalled());
    const callArg = mockGetEventFilters.mock.calls[0][0];
    expect(callArg.filterTypes).not.toContain('namespace');
    expect(callArg.filterTypes).not.toContain('workload');
  });

  it('fetches workload data when resource_ids are provided', async () => {
    mockGetWorkload.mockResolvedValue({
      data: [{ name: 'my-deploy', namespace: 'default', kind: 'Deployment' }],
    });
    renderHook(() => useKubernetesEventFilters({ ...defaultParams, resource_ids: ['res-1'] }));
    await waitFor(() => expect(mockGetWorkload).toHaveBeenCalledWith(expect.objectContaining({ resource_ids: ['res-1'] })));
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
  });

  it('returns accountType as "K8s" by default', () => {
    const { result } = renderHook(() => useKubernetesEventFilters(defaultParams));
    expect(result.current.accountType).toBe('K8s');
  });
});
