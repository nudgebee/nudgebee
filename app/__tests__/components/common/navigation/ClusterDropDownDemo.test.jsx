import React from 'react';
import { render, waitFor, act } from '@testing-library/react';
import ClusterDropdown from '@shared/navigation/ClusterDropDown';
import userApi from '@api1/user';

// Which account the dropdown opens on when the list holds the Demo stand-in: Demo must not win
// over a real account just because it was saved, or written into ?accountId=, while it was the
// only account — the state every fresh install's first login is in.

const mockRouter = { pathname: '/home', query: {}, push: jest.fn(), replace: jest.fn() };
jest.mock('next/router', () => ({ useRouter: () => mockRouter }));

jest.mock('next-auth/react', () => ({
  useSession: () => ({ data: { tenant: { id: 'tenant-1' } }, status: 'authenticated' }),
}));

// DataContext backed by real state, so the dropdown's writes are visible to its own effects.
const mockDataContext = React.createContext(null);
jest.mock('@context/DataContext', () => ({
  useData: () => require('react').useContext(mockDataContext),
}));

jest.mock('@shared/CustomDropdown', () => ({
  __esModule: true,
  default: ({ value, onChange, options }) => (
    <select data-testid='cluster-dropdown' value={value} onChange={onChange}>
      <option value=''>Select</option>
      {options.map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@shared/layout/UpdateDataContext', () => ({ transformClusters: jest.fn((data) => data) }));
jest.mock('@api1/home', () => ({
  __esModule: true,
  default: { getCloudAccounts: jest.fn().mockResolvedValue([]) },
  isCloudAccountsLoadFailure: jest.fn().mockReturnValue(false),
}));
jest.mock('@api1/account', () => ({
  __esModule: true,
  default: { listProviderCapabilities: jest.fn().mockResolvedValue({}) },
}));
jest.mock('@lib/accountGuard', () => ({ isAccountGuardActive: () => false }));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferences: jest.fn(),
    storeUserPreferences: jest.fn(),
    setLastAccountIdForProvider: jest.fn(),
  },
  PREFERENCE_LAST_ACCOUNT_ID: 'last_account',
}));

const DEMO = { value: 'demo', label: 'Demo', cloud_provider: 'K8s' };
const REAL = { value: 'acc-1', label: 'nb-control-plane-k8s', cloud_provider: 'K8s' };

let setClustersFromOutside;
let latestSelected;
function Harness({ initialClusters }) {
  const [allCluster, setAllCluster] = React.useState(initialClusters);
  const [selectedCluster, setSelectedCluster] = React.useState(null);
  setClustersFromOutside = setAllCluster;
  latestSelected = selectedCluster;
  const value = { allCluster, setAllCluster, selectedCluster, setSelectedCluster, setProviderCapabilities: jest.fn() };
  return (
    <mockDataContext.Provider value={value}>
      <ClusterDropdown />
    </mockDataContext.Provider>
  );
}

beforeEach(() => {
  jest.clearAllMocks();
  mockRouter.pathname = '/home';
  mockRouter.query = {};
  userApi.getUserPreferences.mockReturnValue({});
});

describe('ClusterDropdown and the Demo account', () => {
  it('opens on the real account when Demo was saved as the last account', async () => {
    userApi.getUserPreferences.mockReturnValue({ last_account: 'demo' });
    render(<Harness initialClusters={[REAL, DEMO]} />);

    await waitFor(() => expect(latestSelected?.value).toBe('acc-1'));
    expect(userApi.storeUserPreferences).toHaveBeenCalledWith('last_account', 'acc-1');
  });

  it('opens on the real account when ?accountId=demo was left in the URL', async () => {
    mockRouter.query = { accountId: 'demo' };
    render(<Harness initialClusters={[REAL, DEMO]} />);

    await waitFor(() => expect(latestSelected?.value).toBe('acc-1'));
  });

  it('still opens Demo on its own details page', async () => {
    mockRouter.pathname = '/kubernetes/details/[KubernetesDetails]';
    mockRouter.query = { KubernetesDetails: 'demo' };
    render(<Harness initialClusters={[REAL, DEMO]} />);

    await waitFor(() => expect(latestSelected?.value).toBe('demo'));
  });

  it('keeps a saved real account', async () => {
    const other = { value: 'acc-2', label: 'other', cloud_provider: 'K8s' };
    userApi.getUserPreferences.mockReturnValue({ last_account: 'acc-2' });
    render(<Harness initialClusters={[REAL, other, DEMO]} />);

    await waitFor(() => expect(latestSelected?.value).toBe('acc-2'));
  });

  it('does not save Demo when it was picked only because no real account existed', async () => {
    render(<Harness initialClusters={[DEMO]} />);

    await waitFor(() => expect(latestSelected?.value).toBe('demo'));
    expect(userApi.storeUserPreferences).not.toHaveBeenCalled();
  });

  it('moves to the real account when it loads after Demo was picked as the fallback', async () => {
    render(<Harness initialClusters={[DEMO]} />);
    await waitFor(() => expect(latestSelected?.value).toBe('demo'));
    mockRouter.query = { accountId: 'demo' }; // what the dropdown wrote for the fallback

    act(() => setClustersFromOutside([REAL, DEMO]));

    await waitFor(() => expect(latestSelected?.value).toBe('acc-1'));
    expect(userApi.storeUserPreferences).toHaveBeenCalledWith('last_account', 'acc-1');
  });
});
