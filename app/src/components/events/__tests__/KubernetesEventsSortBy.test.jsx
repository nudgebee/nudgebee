import React from 'react';
import { act, cleanup, render, waitFor } from '@testing-library/react';
import KubernetesEvents from '../KubernetesEvents';

/*
 * `sortBy` in the Troubleshoot URL is shared with the Triage Inbox, whose sort
 * values ('Count', 'Priority', 'Last Occurred') are not event columns. Sorting
 * events by one fails the query, so the Inbox row's Events drill-down and the
 * Events tab rendered empty. Only this table's own sort options may be read.
 */

const applyFiltersOnRouter = jest.fn();
const getK8sEvents = jest.fn();
let mockQuery = {};

jest.mock('next/router', () => ({
  useRouter: () => ({
    events: { on: jest.fn(), off: jest.fn() },
    push: jest.fn(),
    replace: jest.fn(),
    asPath: '/troubleshoot',
    pathname: '/troubleshoot',
    query: mockQuery,
  }),
}));

jest.mock('next/dynamic', () => ({ __esModule: true, default: () => () => null }));

jest.mock('@lib/router', () => ({ applyFiltersOnRouter: (...args) => applyFiltersOnRouter(...args) }));

jest.mock('@api1/kubernetes', () => ({
  __esModule: true,
  default: {
    getK8sEvents: (...args) => getK8sEvents(...args),
    getK8sEventsCount: jest.fn(async () => ({ data: { data: { count: 0 } } })),
    getK8sEventGroupings: jest.fn(async () => ({ data: { data: [] } })),
  },
}));
jest.mock('@api1/tickets', () => ({ __esModule: true, default: { listTicketsSummary: jest.fn(async () => ({ data: { data: [] } })) } }));
jest.mock('@api1/user', () => ({ __esModule: true, default: { getUserPreferencesTablePageSize: () => 10 } }));

jest.mock('@lib/auth', () => ({
  hasWriteAccess: () => true,
  hasPermission: () => true,
  getCurrentTenant: () => ({ id: 'tenant-1' }),
  getUserSession: () => ({}),
}));

jest.mock('@hooks/useKubernetesEventFilters', () => ({
  __esModule: true,
  default: () => ({
    accounts: [],
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
  useEventCloudFilter: () => ({ serviceNamesFilter: [], eventNamesFilter: [], isOptionsLoading: false }),
}));

jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));

afterEach(() => {
  cleanup();
  jest.clearAllMocks();
  mockQuery = {};
});

const sortByOfLastEventsQuery = () => getK8sEvents.mock.calls.at(-1)[2].sort_by;

const renderDrilldown = async () => {
  await act(async () => {
    render(<KubernetesEvents accountId='acc-1' enableFilters={false} defaultQuery={{ fingerprint: ['fp-1'] }} />);
  });
  await waitFor(() => expect(getK8sEvents).toHaveBeenCalled());
};

describe('KubernetesEvents sort from the URL', () => {
  beforeEach(() => {
    getK8sEvents.mockResolvedValue({ data: { data: [] } });
  });

  it.each(['Count', 'Priority', 'Last Occurred'])("ignores the Triage Inbox's sortBy=%s and sorts by time", async (inboxSort) => {
    mockQuery = { sortBy: inboxSort };
    await renderDrilldown();
    expect(sortByOfLastEventsQuery()).toBe('created_at');
  });

  it("keeps a sortBy that is one of this table's own options", async () => {
    mockQuery = { sortBy: 'computed_score' };
    await renderDrilldown();
    expect(sortByOfLastEventsQuery()).toBe('computed_score');
  });
});
