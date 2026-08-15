/**
 * Regression coverage for the Events "Search by message" filter.
 *
 * Locks in that the applied search reaches every query the card fires (list,
 * count and trend chart), survives a reload via the URL, and clears with a
 * single navigation.
 */
import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';

let routerQuery = {};
let rerender = null;
const routerPush = jest.fn((target) => {
  routerQuery = { ...(target?.query || {}) };
  if (rerender) act(() => rerender());
  return Promise.resolve(true);
});

jest.mock('next/router', () => ({
  useRouter: () => ({
    push: routerPush,
    replace: jest.fn(),
    query: routerQuery,
    pathname: '/troubleshoot',
    asPath: '/troubleshoot#all-events',
    route: '/troubleshoot',
    prefetch: jest.fn().mockResolvedValue(null),
  }),
}));

jest.mock('@lib/auth', () => ({
  hasWriteAccess: () => true,
  hasReadAccess: () => true,
  getCurrentTenant: () => ({ id: 'tenant-1' }),
}));

jest.mock('next/dynamic', () => ({
  __esModule: true,
  default: () => {
    const Table = () => <div data-testid='events-table' />;
    return Table;
  },
}));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sEvents: jest.fn(),
    getK8sEventsCount: jest.fn(),
    getK8sEventGroupings: jest.fn(),
  },
}));
jest.mock('@api1/tickets', () => ({ __esModule: true, default: { listTicketsSummary: jest.fn() } }));
jest.mock('@api1/user', () => ({ __esModule: true, default: { getUserPreferencesTablePageSize: () => 10 } }));
jest.mock('@api1/triage', () => ({ getTriageStatusTooltip: () => 'tooltip' }));

jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: () => ({
    accounts: [{ id: 'acc-1', label: 'Cluster 1', cloud_provider: 'K8s' }],
    accountType: 'K8s',
    namespaceFilter: [],
    workloadFilter: [],
    subjectTypeFilter: [],
    aggregationKeyFilter: [],
    sourceFilter: [],
    nbStatusFilter: [],
    isOptionsLoading: {},
  }),
}));
jest.mock('@hooks/useCloudFilters', () => ({
  useEventCloudFilter: () => ({ serviceNamesFilter: [], eventNamesFilter: [], isOptionsLoading: {} }),
}));
jest.mock('@hooks/usePersistedFilters', () => ({
  readPersistedFilters: () => null,
  writePersistedFilters: jest.fn(),
}));

jest.mock('@components/tickets/TicketCreatePopupForm', () => ({ __esModule: true, default: () => null }));
jest.mock('@components/events/EventClassifyModal', () => ({ __esModule: true, default: () => null }));
jest.mock('@ui/Chart', () => ({
  __esModule: true,
  default: { Line: () => <div data-testid='trend-chart' /> },
}));
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => <div data-testid='date-range-picker' />,
}));

import KubernetesEventsTable from '@components/events/KubernetesEvents';

const k8sApi = require('@api1/kubernetes').default;
const ticketsApi = require('@api1/tickets').default;

const sampleEvents = [{ id: 'evt-1', fingerprint: 'fp-1', title: 'Pod OOM killed', account_id: 'acc-1', priority: 'HIGH', status: 'FIRING' }];

beforeEach(() => {
  jest.clearAllMocks();
  routerQuery = {};
  rerender = null;
  k8sApi.getK8sEvents.mockResolvedValue({ data: { events: sampleEvents } });
  k8sApi.getK8sEventsCount.mockResolvedValue({ count: 1 });
  k8sApi.getK8sEventGroupings.mockResolvedValue({ data: { event_groupings: [] } });
  ticketsApi.listTicketsSummary.mockResolvedValue({ data: { tickets: [] } });
});

const searchInput = () => document.getElementById('filter-search-message');
const lastListQuery = () => k8sApi.getK8sEvents.mock.calls.at(-1)[2];
const lastCountQuery = () => k8sApi.getK8sEventsCount.mock.calls.at(-1)[0];
const lastTrendQuery = () => k8sApi.getK8sEventGroupings.mock.calls.at(-1)[2];

const renderTable = (props = {}) => {
  const view = render(<KubernetesEventsTable accountId='acc-1' {...props} />);
  rerender = () => view.rerender(<KubernetesEventsTable accountId='acc-1' {...props} />);
  return view;
};

const search = (text) => {
  fireEvent.change(searchInput(), { target: { value: text } });
  fireEvent.keyDown(searchInput(), { key: 'Enter' });
};

describe('KubernetesEvents — search by message', () => {
  it('sends the search to both the list and the count query, and to the URL', async () => {
    renderTable();
    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());
    k8sApi.getK8sEvents.mockClear();
    k8sApi.getK8sEventsCount.mockClear();

    search('oom');

    await waitFor(() => expect(lastListQuery().messageSearch).toBe('oom'));
    expect(lastCountQuery().messageSearch).toBe('oom');
    expect(routerQuery.messageSearch).toBe('oom');
  });

  it('seeds the applied filter from the URL so a reload keeps filtering', async () => {
    routerQuery = { messageSearch: 'disk pressure' };
    renderTable();
    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());

    expect(k8sApi.getK8sEvents.mock.calls[0][2].messageSearch).toBe('disk pressure');
    expect(searchInput().value).toBe('disk pressure');
  });

  it('normalizes a duplicated query param to the first value', async () => {
    routerQuery = { messageSearch: ['oom', 'disk'] };
    renderTable();
    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());

    expect(k8sApi.getK8sEvents.mock.calls[0][2].messageSearch).toBe('oom');
  });

  it('clears with the X in a single navigation', async () => {
    renderTable();
    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());
    search('oom');
    await waitFor(() => expect(lastListQuery().messageSearch).toBe('oom'));

    routerPush.mockClear();
    fireEvent.click(screen.getByLabelText('clear search'));

    await waitFor(() => expect(lastListQuery().messageSearch).toBeUndefined());
    // One click => one router.push. Two pushes in the same tick cancel each
    // other's navigation in the pages router.
    expect(routerPush).toHaveBeenCalledTimes(1);
    expect(routerQuery.messageSearch).toBeUndefined();
    expect(searchInput().value).toBe('');
  });

  it('drops the filter when the field is emptied by hand', async () => {
    renderTable();
    await waitFor(() => expect(k8sApi.getK8sEvents).toHaveBeenCalled());
    search('oom');
    await waitFor(() => expect(lastListQuery().messageSearch).toBe('oom'));

    fireEvent.change(searchInput(), { target: { value: '' } });

    await waitFor(() => expect(lastListQuery().messageSearch).toBeUndefined());
    expect(routerQuery.messageSearch).toBeUndefined();
  });

  it('applies the search to the trend chart above the table', async () => {
    renderTable({ enableTrendChart: true });
    await waitFor(() => expect(k8sApi.getK8sEventGroupings).toHaveBeenCalled());
    k8sApi.getK8sEventGroupings.mockClear();

    search('oom');

    await waitFor(() => expect(k8sApi.getK8sEventGroupings).toHaveBeenCalled());
    expect(lastTrendQuery().messageSearch).toBe('oom');
  });
});
