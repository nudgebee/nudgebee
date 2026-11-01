import React from 'react';
import { render, waitFor } from '@testing-library/react';
import Kubernetes from '@pages/kubernetes';
import homeApi from '@api1/home';
import userApi from '@api1/user';

const mockRouter = { push: jest.fn(), replace: jest.fn() };
jest.mock('next/router', () => ({ useRouter: () => mockRouter }));
const mockData = { selectedCluster: {}, setSelectedCluster: jest.fn() };
jest.mock('@context/DataContext', () => ({ useData: () => mockData }));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn() } }));
jest.mock('@lib/auth', () => ({ getCurrentTenant: () => ({ id: 'tenant-1' }) }));
// The real preference store (localStorage) runs; only its network imports are stubbed.
jest.mock('@lib/HttpService', () => ({}));
jest.mock('@lib/UserService', () => ({}));

const account = (id, cloud_provider) => ({ id, account_name: id, cloud_provider, status: 'active', agents: [] });
// API order: the cluster the user works in is not the first K8s row.
const ACCOUNTS = [account('aws-prod', 'AWS'), account('k8s-other', 'K8s'), account('k8s-dev', 'K8s')];
// What the header picker saves when the user picks a cluster.
const saveLastCluster = (id) => userApi.setLastAccountIdForProvider('K8s', id, 'tenant-1');

const openedPath = async () => {
  await waitFor(() => expect(mockRouter.push).toHaveBeenCalled());
  return mockRouter.push.mock.calls[0][0];
};

describe('/kubernetes with no K8s cluster selected yet', () => {
  beforeEach(() => {
    localStorage.clear();
    jest.clearAllMocks();
    mockData.selectedCluster = {};
    homeApi.getCloudAccounts.mockResolvedValue(ACCOUNTS);
  });

  it('opens the cluster the user last picked, not the first one in the list', async () => {
    saveLastCluster('k8s-dev');
    render(<Kubernetes />);

    expect(await openedPath()).toBe('/kubernetes/details/k8s-dev#summary');
    expect(mockData.setSelectedCluster).toHaveBeenCalledWith(expect.objectContaining({ value: 'k8s-dev' }));
  });

  it('opens the first cluster when the saved one is no longer connected', async () => {
    saveLastCluster('k8s-removed');
    render(<Kubernetes />);

    expect(await openedPath()).toBe('/kubernetes/details/k8s-other#summary');
  });

  it('opens the first cluster for a user with no saved pick', async () => {
    render(<Kubernetes />);

    expect(await openedPath()).toBe('/kubernetes/details/k8s-other#summary');
  });

  it('opens the cluster selected in the header over the saved one', async () => {
    saveLastCluster('k8s-dev');
    mockData.selectedCluster = { value: 'k8s-other', cloud_provider: 'K8s' };
    render(<Kubernetes />);

    expect(await openedPath()).toBe('/kubernetes/details/k8s-other#summary');
    expect(homeApi.getCloudAccounts).not.toHaveBeenCalled();
  });
});
