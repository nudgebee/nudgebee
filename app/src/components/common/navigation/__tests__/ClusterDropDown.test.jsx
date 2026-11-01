import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import cache from '@lib/cache';
import { queryGraphQL } from '@lib/HttpService';
import homeApi from '@api1/home';
import { DataProvider } from '@context/DataContext';
import { useUpdateAllClusterOption } from '@shared/layout/UpdateDataContext';
import ClusterDropdown from '../ClusterDropDown';

// The real account-list API runs here, on top of a stubbed network, so each test counts the
// GetCloudAccounts requests the dropdown really causes (a cache hit is not a request).
jest.mock('@lib/HttpService', () => ({ queryGraphQL: jest.fn(), gqlStringify: () => '{}' }));
jest.mock('@api1/mock', () => ({
  __esModule: true,
  default: () => Promise.resolve({ GetCloudAccount: { id: 'demo', account_name: 'Demo', status: 'active', cloud_provider: 'K8s' } }),
}));
jest.mock('@api1/user', () => ({
  __esModule: true,
  PREFERENCE_LAST_ACCOUNT_ID: 'last_account',
  default: { getUserPreferences: () => ({}), storeUserPreferences: jest.fn(), setLastAccountIdForProvider: jest.fn() },
}));
jest.mock('@api1/account', () => ({ __esModule: true, default: { listProviderCapabilities: () => Promise.resolve({}) } }));
// EE build, where the dropdown leaves a URL account it cannot find for the account guard to judge.
jest.mock('@lib/accountGuard', () => ({ isAccountGuardActive: () => true }));
const mockRouter = { pathname: '/home', query: {}, push: jest.fn() };
jest.mock('next/router', () => ({ useRouter: () => mockRouter }));
const mockSession = { data: { tenant: { id: 'tenant-1' } } };
jest.mock('next-auth/react', () => ({ useSession: () => mockSession }));
jest.mock('@shared/CustomDropdown', () => ({
  __esModule: true,
  default: ({ options, value }) => (
    <ul data-testid='picker' data-selected={value}>
      {options.map((option) => (
        <li key={option.value}>{option.label}</li>
      ))}
    </ul>
  ),
}));

const LIST_FAILED = { data: { errors: [{ message: 'upstream unavailable' }] } };
const listOf = (rows) => ({ data: { data: { cloud_accounts: { rows } } } });
const K8S_DEV = { id: 'acc-1', account_name: 'k8s-dev', cloud_provider: 'K8s', status: 'active', agents: [] };

const pickerOptions = () => screen.getAllByRole('listitem').map((item) => item.textContent);
// Lets pending requests settle, then moves the clock forward a second at a time: React only
// re-renders (and arms the next timer) between steps, so one big jump would hide every retry.
const wait = async (ms = 0) => {
  await act(() => jest.advanceTimersByTimeAsync(0));
  for (let elapsed = 0; elapsed < ms; elapsed += 1000) {
    await act(() => jest.advanceTimersByTimeAsync(1000));
  }
};
const TWO_MINUTES = 2 * 60 * 1000;

// Stands in for the screens that re-read the list after an account is added or removed.
const RefreshList = () => {
  const refresh = useUpdateAllClusterOption();
  return <button onClick={() => refresh(true)}>refresh list</button>;
};
const refreshList = async () => {
  fireEvent.click(screen.getByText('refresh list'));
  await wait();
};

const renderDropdown = () =>
  render(
    <DataProvider>
      <ClusterDropdown />
      <RefreshList />
    </DataProvider>
  );

describe('ClusterDropdown when the account list has no real account', () => {
  beforeEach(() => {
    jest.useFakeTimers();
    cache.clear();
    queryGraphQL.mockReset();
    mockRouter.query = {};
  });
  afterEach(() => {
    jest.restoreAllMocks();
    jest.useRealTimers();
  });

  it('lists the real accounts again, without a page reload, once the list request works', async () => {
    queryGraphQL.mockResolvedValueOnce(LIST_FAILED).mockResolvedValue(listOf([K8S_DEV]));
    mockRouter.query = { accountId: 'acc-1' }; // as in /home?accountId=acc-1
    renderDropdown();
    await wait();
    expect(pickerOptions()).toEqual(['Demo']);

    await wait(2000);

    expect(pickerOptions()).toEqual(['k8s-dev', 'Demo']);
    expect(screen.getByTestId('picker')).toHaveAttribute('data-selected', 'acc-1');
    expect(queryGraphQL).toHaveBeenCalledTimes(2);
  });

  it('asks once, and never again, for a tenant that has no accounts', async () => {
    queryGraphQL.mockResolvedValue(listOf([]));
    const listLookups = jest.spyOn(homeApi, 'getCloudAccounts');
    renderDropdown();

    await wait(TWO_MINUTES);

    expect(pickerOptions()).toEqual(['Demo']);
    expect(queryGraphQL).toHaveBeenCalledTimes(1);
    // The first load, then one read of the cached list that confirms the tenant has no accounts.
    expect(listLookups).toHaveBeenCalledTimes(2);
  });

  it('stops asking after four more tries when the list keeps failing', async () => {
    queryGraphQL.mockResolvedValue(LIST_FAILED);
    renderDropdown();

    await wait(TWO_MINUTES);

    expect(pickerOptions()).toEqual(['Demo']);
    expect(queryGraphQL).toHaveBeenCalledTimes(5);
  });

  it('recovers from a later failed refresh, after it had stopped asking for a tenant with no accounts', async () => {
    queryGraphQL
      .mockResolvedValueOnce(listOf([]))
      .mockResolvedValueOnce(listOf([K8S_DEV]))
      .mockResolvedValueOnce(LIST_FAILED);
    renderDropdown();
    await wait(TWO_MINUTES);
    expect(pickerOptions()).toEqual(['Demo']);

    await refreshList(); // the tenant connected its first cluster
    expect(pickerOptions()).toEqual(['k8s-dev', 'Demo']);

    await refreshList(); // this refresh fails and leaves the Demo-only stand-in
    expect(pickerOptions()).toEqual(['Demo']);
    await wait(2000);

    expect(pickerOptions()).toEqual(['k8s-dev', 'Demo']);
  });
});
