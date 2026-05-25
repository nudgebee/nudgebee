import { renderHook, waitFor } from '@testing-library/react';
import { useCloudFilter, useRecommendationCloudFilter, useEventCloudFilter } from '@hooks/useCloudFilters';

jest.mock('@api1/recommendation', () => ({
  __esModule: true,
  default: {
    listRecommendationNamesapces: jest.fn(),
    listRecommendationFilter: jest.fn(),
  },
  RECOMMENDATION_SERVERITY: ['low', 'medium', 'high'],
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getEventFilterValues: jest.fn(),
  },
}));

jest.mock('@api1/resources', () => ({
  __esModule: true,
  default: {
    getResourceServices: jest.fn(),
  },
}));

jest.mock('src/utils/common', () => ({
  snakeToTitleCase: jest.fn((s) => s.replace(/_/g, ' ')),
}));

import apiRecommendations from '@api1/recommendation';
import k8sApi from '@api1/kubernetes';
import apiResources from '@api1/resources';

const mockListNamespaces = apiRecommendations.listRecommendationNamesapces as jest.Mock;
const mockListFilter = apiRecommendations.listRecommendationFilter as jest.Mock;
const mockGetEventFilters = k8sApi.getEventFilterValues as jest.Mock;
const mockGetResourceServices = apiResources.getResourceServices as jest.Mock;

describe('useCloudFilter', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetResourceServices.mockResolvedValue({ data: [] });
  });

  it('returns empty serviceNamesFilter when accountId is falsy', () => {
    const { result } = renderHook(() => useCloudFilter(''));
    expect(result.current.serviceNamesFilter).toEqual([]);
  });

  it('returns severityFilterType from RECOMMENDATION_SERVERITY constant', () => {
    const { result } = renderHook(() => useCloudFilter('acc-1'));
    expect(result.current.severityFilterType).toEqual(['low', 'medium', 'high']);
  });

  it('fetches service names when accountId is provided', async () => {
    mockGetResourceServices.mockResolvedValue({ data: ['svc-1', 'svc-2'] });
    const { result } = renderHook(() => useCloudFilter('acc-1'));
    await waitFor(() => expect(result.current.serviceNamesFilter).toEqual(['svc-1', 'svc-2']));
    expect(mockGetResourceServices).toHaveBeenCalledWith('acc-1');
  });
});

describe('useRecommendationCloudFilter', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns empty filters when accountId is falsy', () => {
    const { result } = renderHook(() => useRecommendationCloudFilter(''));
    expect(result.current.ruleNamesFilter).toEqual([]);
    expect(result.current.serviceNamesFilter).toEqual([]);
  });

  it('fetches rule names filter on mount', async () => {
    mockListFilter.mockResolvedValue({
      data: {
        data: {
          recommendation: [{ rule_name: 'pod_right_sizing' }, { rule_name: 'replica_right_sizing' }],
        },
      },
    });
    const { result } = renderHook(() => useRecommendationCloudFilter('acc-1'));
    await waitFor(() => expect(result.current.ruleNamesFilter).toHaveLength(2));
    expect((result.current.ruleNamesFilter[0] as { label: string; value: string }).value).toBe('pod_right_sizing');
  });

  it('returns severityFilter', () => {
    const { result } = renderHook(() => useRecommendationCloudFilter('acc-1'));
    expect(result.current.severityFilter).toEqual(['low', 'medium', 'high']);
  });

  it('does not fetch serviceNames when data.serviceName is provided', async () => {
    mockListFilter.mockResolvedValue({ data: { data: { recommendation: [] } } });
    renderHook(() => useRecommendationCloudFilter('acc-1', { serviceName: 'existing-service' }));
    await waitFor(() => expect(mockListFilter).toHaveBeenCalled());
    // Should only call once for rule_name, not for resource_cloud_service
    expect(mockListFilter).toHaveBeenCalledTimes(1);
  });
});

describe('useEventCloudFilter', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns static filters (statusFilter, nbStatusFilter) and empty sourceFilter when accountId is falsy', () => {
    const { result } = renderHook(() => useEventCloudFilter(''));
    expect(result.current.sourceFilter).toHaveLength(0);
    expect(result.current.statusFilter).toHaveLength(2);
    expect(result.current.nbStatusFilter).toHaveLength(7);
  });

  it('does not fetch when accountId is falsy', () => {
    renderHook(() => useEventCloudFilter(''));
    expect(mockListNamespaces).not.toHaveBeenCalled();
    expect(mockGetEventFilters).not.toHaveBeenCalled();
  });

  it('fetches namespace and aggregation_key filters when accountId is provided', async () => {
    mockListNamespaces.mockResolvedValue({ data: { namespaces: ['ns-1', 'ns-2'] } });
    mockGetEventFilters.mockResolvedValue({ data: { filters: [] } });
    renderHook(() => useEventCloudFilter('acc-1'));
    await waitFor(() => expect(mockListNamespaces).toHaveBeenCalledWith(expect.objectContaining({ accountId: 'acc-1' })));
  });

  it('sets aggregation_key filter values from event filter API response', async () => {
    mockListNamespaces.mockResolvedValue({ data: { namespaces: [] } });
    mockGetEventFilters.mockResolvedValue({
      data: {
        filters: [
          {
            filter_type: 'aggregation_key',
            values: [{ value: 'pod_oom_killed' }, { value: 'cpu_throttling' }],
          },
        ],
      },
    });
    const { result } = renderHook(() => useEventCloudFilter('acc-1'));
    await waitFor(() => expect(result.current.eventNamesFilter).toHaveLength(2));
  });
});
