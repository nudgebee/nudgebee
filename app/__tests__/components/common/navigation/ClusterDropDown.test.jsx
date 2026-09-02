import React from 'react';
import { render, screen } from '@testing-library/react';
import ClusterDropdown from '@shared/navigation/ClusterDropDown';

jest.mock('next-auth/react', () => ({
  useSession: () => ({ data: { tenant: { id: 'tenant-1' } }, status: 'authenticated' }),
}));

jest.mock('@context/DataContext', () => ({
  useData: () => ({
    selectedCluster: { value: 'cluster-1', label: 'Cluster 1' },
    setSelectedCluster: jest.fn(),
    allCluster: [
      { value: 'cluster-1', label: 'Cluster 1' },
      { value: 'cluster-2', label: 'Cluster 2' },
    ],
    setAllCluster: jest.fn(),
  }),
}));

jest.mock('@shared/CustomDropdown', () => ({
  __esModule: true,
  default: ({ value, onChange, label, options, isLoading }) => (
    <div>
      {label && <label>{label}</label>}
      <select data-testid='cluster-dropdown' value={value} onChange={onChange} disabled={isLoading}>
        <option value=''>Select</option>
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </div>
  ),
}));

jest.mock('@shared/layout/UpdateDataContext', () => ({
  transformClusters: jest.fn((data) => data),
}));

jest.mock('@api1/home', () => ({
  __esModule: true,
  default: {
    getCloudAccounts: jest.fn().mockResolvedValue([]),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getUserPreferences: jest.fn().mockReturnValue({}),
    storeUserPreferences: jest.fn(),
    getUserPreferencesTablePageSize: jest.fn().mockReturnValue(10),
    getLastAccountIdForProvider: jest.fn(),
    setLastAccountIdForProvider: jest.fn(),
  },
  PREFERENCE_LAST_ACCOUNT_ID: 'lastAccountId',
}));

describe('ClusterDropdown', () => {
  it('renders without crashing', () => {
    const { container } = render(<ClusterDropdown />);
    expect(container.firstChild).toBeInTheDocument();
  });

  it('renders the cluster dropdown element', () => {
    render(<ClusterDropdown />);
    expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
  });

  it('renders "Change Cluster" label by default', () => {
    render(<ClusterDropdown />);
    expect(screen.getByText('Change Cluster')).toBeInTheDocument();
  });

  it('renders no label when noLabel is true', () => {
    render(<ClusterDropdown noLabel={true} />);
    expect(screen.queryByText('Change Cluster')).not.toBeInTheDocument();
  });

  it('renders cluster options from context', () => {
    render(<ClusterDropdown />);
    expect(screen.getByRole('option', { name: 'Cluster 1' })).toBeInTheDocument();
    expect(screen.getByRole('option', { name: 'Cluster 2' })).toBeInTheDocument();
  });

  it('renders without onChange callback without crashing', () => {
    render(<ClusterDropdown />);
    expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
  });

  it('renders with disableRouteChanges prop without crashing', () => {
    render(<ClusterDropdown disableRouteChanges={true} />);
    expect(screen.getByTestId('cluster-dropdown')).toBeInTheDocument();
  });
});
