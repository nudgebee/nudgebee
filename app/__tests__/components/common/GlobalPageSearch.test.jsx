import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import GlobalPageSearch from '@shared/navigation/GlobalPageSearch';

const mockPush = jest.fn();
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: mockPush,
    replace: jest.fn(),
    pathname: '/',
    query: {},
    asPath: '/',
    route: '/',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('next-auth/react', () => ({
  useSession: () => ({ data: { tenant: { id: 'tenant-1' } }, status: 'authenticated' }),
}));

const mockSetSelectedCluster = jest.fn();
// Mutated per-test before render — GlobalPageSearch reads selectedCluster/allCluster
// once per render via useData(), so tests that need a different fixture set this
// before rendering rather than trying to update it after the fact.
let mockDataContextValue = { selectedCluster: null, allCluster: [], setSelectedCluster: mockSetSelectedCluster };
jest.mock('@context/DataContext', () => ({
  useData: () => mockDataContextValue,
}));

const mockGetLastAccountIdForProvider = jest.fn().mockReturnValue(null);
const mockSetLastAccountIdForProvider = jest.fn();
const mockGetRecentPageSearches = jest.fn().mockReturnValue([]);
const mockAddRecentPageSearch = jest.fn();
const mockStoreUserPreferences = jest.fn();
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    getLastAccountIdForProvider: (...args) => mockGetLastAccountIdForProvider(...args),
    setLastAccountIdForProvider: (...args) => mockSetLastAccountIdForProvider(...args),
    getRecentPageSearches: (...args) => mockGetRecentPageSearches(...args),
    addRecentPageSearch: (...args) => mockAddRecentPageSearch(...args),
    storeUserPreferences: (...args) => mockStoreUserPreferences(...args),
  },
  PREFERENCE_LAST_ACCOUNT_ID: 'last_account',
}));

const openSearch = () => fireEvent.click(screen.getByText('Search pages…'));
const getSearchInput = () => screen.getByPlaceholderText(/search pages/i);

describe('GlobalPageSearch', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockGetLastAccountIdForProvider.mockReturnValue(null);
    mockGetRecentPageSearches.mockReturnValue([]);
    mockDataContextValue = { selectedCluster: null, allCluster: [], setSelectedCluster: mockSetSelectedCluster };
  });

  it('renders the trigger with the Ctrl/Cmd+K hint', () => {
    render(<GlobalPageSearch />);
    expect(screen.getByText('Search pages…')).toBeInTheDocument();
    expect(screen.getByText('K')).toBeInTheDocument();
  });

  it('opens the results popover on trigger click', () => {
    render(<GlobalPageSearch />);
    openSearch();
    expect(screen.getByText('All Events')).toBeInTheDocument();
    expect(screen.getByText('Triage Inbox')).toBeInTheDocument();
  });

  it('filters results by the path-segment acronym', () => {
    render(<GlobalPageSearch />);
    openSearch();
    fireEvent.change(getSearchInput(), { target: { value: 'umu' } });
    expect(screen.getByText('Users')).toBeInTheDocument();
    expect(screen.queryByText('All Events')).not.toBeInTheDocument();
  });

  it('navigates and records a recent search when a result is clicked', async () => {
    render(<GlobalPageSearch />);
    openSearch();
    fireEvent.click(screen.getByText('All Events'));
    expect(mockPush).toHaveBeenCalledWith('/troubleshoot#all-events/all');
    expect(mockAddRecentPageSearch).toHaveBeenCalledWith('/troubleshoot#all-events/all', 'tenant-1');
    // Picking a result closes the popover (MUI's exit transition unmounts
    // the content asynchronously, hence the wait).
    await waitFor(() => expect(screen.queryByText('Triage Inbox')).not.toBeInTheDocument());
  });

  it('selects the keyboard-highlighted row on Enter', () => {
    render(<GlobalPageSearch />);
    openSearch();
    const input = getSearchInput();
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'Enter' });
    expect(mockPush).toHaveBeenCalledWith('/troubleshoot#all-events/all');
  });

  it('closes the popover on Escape', async () => {
    render(<GlobalPageSearch />);
    openSearch();
    fireEvent.keyDown(getSearchInput(), { key: 'Escape' });
    await waitFor(() => expect(screen.queryByText('All Events')).not.toBeInTheDocument());
  });

  it('toggles the popover with Ctrl+K', async () => {
    render(<GlobalPageSearch />);
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    expect(screen.getByText('All Events')).toBeInTheDocument();
    fireEvent.keyDown(window, { key: 'k', ctrlKey: true });
    await waitFor(() => expect(screen.queryByText('All Events')).not.toBeInTheDocument());
  });

  it('shows the All Pages caption and its provider-account legend even with no recents yet', () => {
    mockDataContextValue = {
      selectedCluster: null,
      allCluster: [{ value: 'aws-1', label: 'AWS Prod', cloud_provider: 'AWS' }],
      setSelectedCluster: mockSetSelectedCluster,
    };
    render(<GlobalPageSearch />);
    openSearch();
    expect(screen.queryByText('Recents')).not.toBeInTheDocument();
    expect(screen.getByText('All Pages')).toBeInTheDocument();
    expect(screen.getByText('AWS Prod')).toBeInTheDocument();
  });

  it('shows a Recents section for previously-picked pages, alongside the full list', () => {
    mockGetRecentPageSearches.mockReturnValue(['/troubleshoot#all-events/all']);
    render(<GlobalPageSearch />);
    openSearch();
    expect(screen.getByText('Recents')).toBeInTheDocument();
    expect(screen.getByText('All Pages')).toBeInTheDocument();
    // One copy in "Recents", one in "All Pages".
    expect(screen.getAllByText('All Events')).toHaveLength(2);
  });

  it('shows an account-name chip on a recent search that carries an accountId, and a provider legend under All Pages', () => {
    mockGetRecentPageSearches.mockReturnValue(['/cloud-account/details/aws-1#summary']);
    mockDataContextValue = {
      selectedCluster: null,
      allCluster: [{ value: 'aws-1', label: 'AWS Prod', cloud_provider: 'AWS' }],
      setSelectedCluster: mockSetSelectedCluster,
    };
    render(<GlobalPageSearch />);
    openSearch();
    expect(screen.getByText('Recents')).toBeInTheDocument();
    expect(screen.getByText('All Pages')).toBeInTheDocument();
    // One "AWS Prod" chip on the Recents row (its resolved account), one more
    // in the provider legend under "All Pages" (AWS is the only provider with
    // a resolved account in this fixture).
    expect(screen.getAllByText('AWS Prod')).toHaveLength(2);
  });

  it('scopes results to a mentioned account and navigates into its detail page', () => {
    mockDataContextValue = {
      selectedCluster: null,
      allCluster: [{ value: 'aws-1', label: 'AWS Prod', cloud_provider: 'AWS' }],
      setSelectedCluster: mockSetSelectedCluster,
    };
    render(<GlobalPageSearch />);
    openSearch();
    fireEvent.change(getSearchInput(), { target: { value: '@' } });
    expect(screen.getByText('AWS Prod')).toBeInTheDocument();
    expect(screen.queryByText('All Events')).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('AWS Prod'));
    // The popover stays open, re-scoped to that account's pages. Several rows
    // share the "Summary" label (aws/summary, aws/ec2/summary, …) — the type
    // chip's full path is what's unique, so anchor on that.
    expect(screen.getByPlaceholderText(/search for aws prod/i)).toBeInTheDocument();
    const summaryPathChip = screen.getByText('/aws/summary');
    fireEvent.click(summaryPathChip.closest('[role="option"]'));
    expect(mockPush).toHaveBeenCalledWith('/cloud-account/details/aws-1#summary');
    expect(mockSetSelectedCluster).toHaveBeenCalledWith({ value: 'aws-1', label: 'AWS Prod', cloud_provider: 'AWS' });
    expect(mockSetLastAccountIdForProvider).toHaveBeenCalledWith('AWS', 'aws-1', 'tenant-1');
  });
});
