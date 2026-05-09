import React from 'react';
import { renderHook, act } from '@testing-library/react';
import { transformClusters, useUpdateAllClusterOption } from '@components1/common/UpdateDataContext';

const mockSetAllCluster = jest.fn();

jest.mock('@context/DataContext', () => ({
  useData: () => ({
    setAllCluster: mockSetAllCluster,
    allCluster: [],
  }),
}));

const mockGetCloudAccounts = jest.fn();
jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: (...args) => mockGetCloudAccounts(...args),
  },
}));

const sampleResponse = [
  {
    id: 'cluster-1',
    account_name: 'My Cluster',
    status: 'active',
    cloud_provider: 'aws',
    cloud_account_attrs: { region: 'us-east-1' },
    account_access: 'full',
    created_at: '2024-01-01',
    agents: [
      {
        last_connected_at: '2024-03-01',
        k8s_provider: 'EKS',
        k8s_version: '1.27',
      },
    ],
  },
  {
    id: 'cluster-2',
    account_name: 'Second Cluster',
    status: 'inactive',
    cloud_provider: 'gcp',
    cloud_account_attrs: {},
    account_access: '',
    created_at: '2024-02-01',
    agents: [],
  },
];

describe('UpdateDataContext', () => {
  beforeEach(() => {
    jest.clearAllMocks();
  });

  describe('transformClusters', () => {
    test('transforms response array to cluster objects', () => {
      const result = transformClusters(sampleResponse);
      expect(result).toHaveLength(2);
      expect(result[0].label).toBe('My Cluster');
      expect(result[0].value).toBe('cluster-1');
      expect(result[0].cloud_provider).toBe('aws');
      expect(result[0].k8s_provider).toBe('EKS');
      expect(result[0].k8s_version).toBe('1.27');
      expect(result[0].last_connected_at).toBe('2024-03-01');
    });

    test('handles missing agents gracefully', () => {
      const result = transformClusters(sampleResponse);
      expect(result[1].last_connected_at).toBe('');
      expect(result[1].k8s_provider).toBe('');
      expect(result[1].k8s_version).toBe('');
      expect(result[1].agent).toEqual({});
    });

    test('uses empty string for missing status', () => {
      const input = [{ id: 'x', account_name: 'X', agents: [] }];
      const result = transformClusters(input);
      expect(result[0].status).toBe('');
    });

    test('returns empty array when input is empty', () => {
      expect(transformClusters([])).toEqual([]);
    });
  });

  describe('useUpdateAllClusterOption', () => {
    test('renders without crashing', () => {
      const { result } = renderHook(() => useUpdateAllClusterOption());
      expect(result.current).toBeInstanceOf(Function);
    });

    test('calls getCloudAccounts with empty string and refresh=false by default', async () => {
      mockGetCloudAccounts.mockResolvedValue(sampleResponse);
      const { result } = renderHook(() => useUpdateAllClusterOption());
      await act(async () => {
        result.current();
      });
      expect(mockGetCloudAccounts).toHaveBeenCalledWith('', false);
    });

    test('calls getCloudAccounts with refresh=true when passed', async () => {
      mockGetCloudAccounts.mockResolvedValue(sampleResponse);
      const { result } = renderHook(() => useUpdateAllClusterOption());
      await act(async () => {
        result.current(true);
      });
      expect(mockGetCloudAccounts).toHaveBeenCalledWith('', true);
    });

    test('calls setAllCluster with transformed cluster data', async () => {
      mockGetCloudAccounts.mockResolvedValue(sampleResponse);
      const { result } = renderHook(() => useUpdateAllClusterOption());
      await act(async () => {
        result.current();
      });
      expect(mockSetAllCluster).toHaveBeenCalledWith(
        expect.arrayContaining([
          expect.objectContaining({ label: 'My Cluster', value: 'cluster-1' }),
          expect.objectContaining({ label: 'Second Cluster', value: 'cluster-2' }),
        ])
      );
    });
  });
});
