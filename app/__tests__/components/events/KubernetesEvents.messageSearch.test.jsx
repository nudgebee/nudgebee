import React from 'react';
import { render, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockRouterPush = jest.fn().mockResolvedValue(true);
let mockRouterQuery = {};
jest.mock('next/router', () => ({
  useRouter: () => ({
    push: mockRouterPush,
    replace: jest.fn(),
    query: mockRouterQuery,
    pathname: '/troubleshoot',
    asPath: '/troubleshoot#all-events/all',
    route: '/troubleshoot',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

const mockGetK8sEvents = jest.fn();
const mockGetK8sEventsCount = jest.fn();
jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sEvents: (...a) => mockGetK8sEvents(...a),
    getK8sEventsCount: (...a) => mockGetK8sEventsCount(...a),
    getK8sEventGroupings: jest.fn().mockResolvedValue({ data: { event_groupings: [] } }),
  },
}));

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: { listTicketsSummary: jest.fn().mockResolvedValue({ data: { tickets: [] } }) },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: () => 10 },
}));

jest.mock('@api1/triage', () => ({ getTriageStatusTooltip: () => 'tip' }));

jest.mock('@lib/auth', () => ({
  hasWriteAccess: () => true,
  getCurrentTenant: () => ({ id: 'tenant-1' }),
}));

jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: () => ({
    accounts: [{ id: 'acc-1', label: 'Acc 1', cloud_provider: 'K8s' }],
    accountType: 'K8s',
    namespaceFilter: [],
    workloadFilter: [],
    subjectTypeFilter: [],
    aggregationKeyFilter: [],
    sourceFilter: [],
    isOptionsLoading: {},
  }),
}));

jest.mock('@hooks/useCloudFilters', () => ({
  useEventCloudFilter: () => ({ serviceNamesFilter: [], eventNamesFilter: [], isOptionsLoading: {} }),
}));

jest.mock('@components/k8s/common/KubernetesTable', () => ({
  __esModule: true,
  default: () => <div data-testid='k8s-table' />,
}));

jest.mock('@components/tickets/TicketCreatePopupForm', () => ({
  __esModule: true,
  default: () => null,
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => <div data-testid='date-range-picker' />,
}));

import KubernetesEventsTable from '@components/events/KubernetesEvents';

const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};

describe('KubernetesEvents message search', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockRouterQuery = {};
    window.localStorage.clear();
    mockGetK8sEvents.mockResolvedValue({ data: { events: [] } });
    mockGetK8sEventsCount.mockResolvedValue({ count: 0 });
  });

  const lastCountQuery = () => mockGetK8sEventsCount.mock.calls[mockGetK8sEventsCount.mock.calls.length - 1][0];

  it('applies the typed message to the fetch on Enter', async () => {
    render(<KubernetesEventsTable accountId='acc-1' />);
    await flush();

    const input = document.getElementById('filter-search-message');
    expect(input).toBeTruthy();

    fireEvent.change(input, { target: { value: 'OOMKilled' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await flush();

    await waitFor(() => {
      expect(lastCountQuery().messageSearch).toBe('OOMKilled');
    });
  });

  it('seeds the input and the fetch from ?messageSearch on mount', async () => {
    mockRouterQuery = { messageSearch: 'CrashLoop' };
    render(<KubernetesEventsTable accountId='acc-1' />);
    await flush();

    expect(document.getElementById('filter-search-message').value).toBe('CrashLoop');
    await waitFor(() => {
      expect(lastCountQuery().messageSearch).toBe('CrashLoop');
    });
  });

  it('clears the filter from the X, navigating exactly once', async () => {
    render(<KubernetesEventsTable accountId='acc-1' />);
    await flush();
    const input = document.getElementById('filter-search-message');

    fireEvent.change(input, { target: { value: 'OOM' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await flush();
    expect(lastCountQuery().messageSearch).toBe('OOM');

    mockRouterPush.mockClear();
    fireEvent.click(document.querySelector('[aria-label="clear search"]'));
    await flush();

    expect(input.value).toBe('');
    await waitFor(() => {
      expect(lastCountQuery().messageSearch).toBeUndefined();
    });
    // Regression: SearchInput used to fire onChange('') *and* onClear(), so this
    // filter ran its clear twice and pushed the same route twice in one tick —
    // the second push cancels the first ("Cancel rendering route") and the URL
    // could be left still carrying ?messageSearch.
    expect(mockRouterPush).toHaveBeenCalledTimes(1);
  });

  it('does not navigate when an unsubmitted draft is emptied', async () => {
    render(<KubernetesEventsTable accountId='acc-1' />);
    await flush();
    const input = document.getElementById('filter-search-message');

    mockRouterPush.mockClear();
    fireEvent.change(input, { target: { value: 'OOM' } });
    fireEvent.change(input, { target: { value: '' } });
    await flush();

    expect(mockRouterPush).not.toHaveBeenCalled();
  });

  it('drops the filter when the field is emptied by hand', async () => {
    render(<KubernetesEventsTable accountId='acc-1' />);
    await flush();
    const input = document.getElementById('filter-search-message');

    fireEvent.change(input, { target: { value: 'OOM' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await flush();
    expect(lastCountQuery().messageSearch).toBe('OOM');

    fireEvent.change(input, { target: { value: '' } });
    await flush();
    await waitFor(() => {
      expect(lastCountQuery().messageSearch).toBeUndefined();
    });
  });
});
