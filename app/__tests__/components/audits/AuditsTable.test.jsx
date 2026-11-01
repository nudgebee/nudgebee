import React from 'react';
import { render, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';

const mockListAudits = jest.fn();
jest.mock('@api1/audits', () => ({
  __esModule: true,
  default: {
    listAudits: (...args) => mockListAudits(...args),
    lisUsersAndAccounts: jest.fn().mockResolvedValue({
      data: {
        cloud_accounts: { rows: [{ id: 'account-1', account_name: 'cluster-one', cloud_provider: 'K8S' }] },
        users: { rows: [] },
      },
    }),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: () => 10 },
}));

// The picker pulls in image assets jest cannot resolve; the range it is given is
// asserted through the query instead.
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: () => null,
}));

import { AuditsTable } from '@components/audits';
import { buildAuditLogUrl } from '@components/audits/common';

const DAY_MS = 24 * 60 * 60 * 1000;

// FilterDropdown renders its trigger as `auto-complete-${id}`.
const filterTrigger = (id) => document.getElementById(`auto-complete-${id}`);

const renderAt = async (url) => {
  window.history.pushState({}, '', url);
  const view = render(<AuditsTable />);
  await waitFor(() => expect(mockListAudits).toHaveBeenCalled());
  return view;
};

describe('AuditsTable deep link', () => {
  beforeEach(() => {
    mockListAudits.mockReset();
    mockListAudits.mockResolvedValue({ data: { audits: [], count: 0 } });
  });

  it('opens unfiltered on the last 24 hours when the URL carries no audit params', async () => {
    await renderAt('/user-management#access-users/audit-log');

    const [, offset, query] = mockListAudits.mock.calls[0];
    expect(offset).toBe(0);
    expect(query).toMatchObject({ accountId: '', category: '', target: '', status: '', action: '', username: '' });
    expect(query.eventEnd.getTime() - query.eventStart.getTime()).toBeCloseTo(DAY_MS, -3);
    expect(filterTrigger('audit-filter-category')).toBeInTheDocument();
    expect(filterTrigger('audit-filter-target')).toBeNull();
  });

  it('seeds the filters from a deep link and queries only the matching entry', async () => {
    const at = Date.parse('2026-06-25T11:41:04.098Z');
    await renderAt(buildAuditLogUrl({ accountId: 'account-1', category: 'ALERT_MANAGER', target: 'HighErrorRate', at }));

    const [, , query] = mockListAudits.mock.calls[0];
    expect(query).toMatchObject({ accountId: 'account-1', category: 'ALERT_MANAGER', target: 'HighErrorRate' });
    expect(query.eventStart.getTime()).toBe(at - 5 * 60 * 1000);
    expect(query.eventEnd.getTime()).toBe(at + 5 * 60 * 1000);
    // The target filter is on the bar, so it can be seen and cleared like the others.
    expect(filterTrigger('audit-filter-target')).toHaveTextContent('HighErrorRate');
    expect(filterTrigger('audit-filter-category')).toHaveTextContent('Alert manager');
    await waitFor(() => expect(filterTrigger('audit-filter-cluster')).toHaveTextContent('cluster-one'));
  });
});
