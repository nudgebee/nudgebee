import { renderHook, waitFor } from '@testing-library/react';
import { useClusterInsights } from '@hooks/useClusterInsights';

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getInsights: jest.fn(),
  },
}));

import homeApi from '@api1/home';
const mockGetInsights = homeApi.getInsights;

const mockInsights = [
  { id: '1', type: 'Troubleshooting', title: 'High CPU' },
  { id: '2', type: 'Optimization', title: 'Reduce replicas' },
  { id: '3', type: 'Troubleshooting', title: 'OOMKilled' },
];

describe('useClusterInsights', () => {
  beforeEach(() => jest.clearAllMocks());

  it('returns empty arrays when accountId is falsy', () => {
    const { result } = renderHook(() => useClusterInsights(''));
    expect(result.current.troubleShootData).toEqual([]);
    expect(result.current.optimizationData).toEqual([]);
    expect(mockGetInsights).not.toHaveBeenCalled();
  });

  it('fetches insights on mount when accountId is provided', async () => {
    mockGetInsights.mockResolvedValue({
      data: { data: { insight_v2: { rows: mockInsights } } },
    });
    const { result } = renderHook(() => useClusterInsights('acc-1'));
    await waitFor(() => {
      expect(result.current.troubleShootData).toHaveLength(2);
    });
    expect(mockGetInsights).toHaveBeenCalledWith('acc-1');
  });

  it('separates troubleshooting and optimization insights', async () => {
    mockGetInsights.mockResolvedValue({
      data: { data: { insight_v2: { rows: mockInsights } } },
    });
    const { result } = renderHook(() => useClusterInsights('acc-1'));
    await waitFor(() => expect(result.current.troubleShootData).toHaveLength(2));
    expect(result.current.optimizationData).toHaveLength(1);
    expect(result.current.optimizationData[0].title).toBe('Reduce replicas');
  });

  it('returns empty arrays when API returns no rows', async () => {
    mockGetInsights.mockResolvedValue({
      data: { data: { insight_v2: { rows: [] } } },
    });
    const { result } = renderHook(() => useClusterInsights('acc-1'));
    await waitFor(() => expect(mockGetInsights).toHaveBeenCalled());
    expect(result.current.troubleShootData).toEqual([]);
    expect(result.current.optimizationData).toEqual([]);
  });

  it('re-fetches when accountId changes', async () => {
    mockGetInsights.mockResolvedValue({
      data: { data: { insight_v2: { rows: mockInsights } } },
    });
    const { rerender } = renderHook(({ id }) => useClusterInsights(id), {
      initialProps: { id: 'acc-1' },
    });
    await waitFor(() => expect(mockGetInsights).toHaveBeenCalledTimes(1));
    rerender({ id: 'acc-2' });
    await waitFor(() => expect(mockGetInsights).toHaveBeenCalledTimes(2));
    expect(mockGetInsights).toHaveBeenLastCalledWith('acc-2');
  });
});
