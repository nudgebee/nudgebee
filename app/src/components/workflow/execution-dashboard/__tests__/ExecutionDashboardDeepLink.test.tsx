import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import apiWorkflow from '@api1/workflow';
import ExecutionDashboard from '../ExecutionDashboard';
import { clearSavedExecutionFilters } from '../useExecutionDashboard';

// The Home "Triggered (24h)" stat links here with the exact window it counted.
const FROM = '2026-10-11T09:00:00.000Z';
const TO = '2026-10-12T09:00:00.000Z';

jest.mock('next/router', () => ({
  useRouter: () => ({
    isReady: true,
    query: { accountId: 'acc-1', from: '2026-10-11T09:00:00.000Z', to: '2026-10-12T09:00:00.000Z' },
    pathname: '/automation',
    asPath: '/automation#executions',
    replace: jest.fn(),
  }),
}));
jest.mock('@lib/auth', () => ({ getUserSession: () => ({ roles: [] }), getCurrentTenant: () => ({ id: 'tenant-1' }) }));
jest.mock('@api1/home', () => ({ __esModule: true, default: { getCloudAccounts: jest.fn(() => Promise.resolve([])) } }));
jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { getUserPreferencesTablePageSize: () => 20, listUsers: jest.fn(() => Promise.resolve({ data: [] })) },
}));
jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    listAccountExecutions: jest.fn(() =>
      Promise.resolve({
        data: {
          executions_list: {
            total_count: 37,
            total_is_approximate: true,
            executions: Array.from({ length: 20 }, (_, i) => ({
              id: `run-${i}`,
              account_id: 'acc-1',
              workflow_id: 'wf-1',
              workflow_name: 'Notify on-call',
              status: 'COMPLETED',
              start_time: '2026-10-12T08:00:00Z',
            })),
          },
        },
      })
    ),
    aggregateExecutions: jest.fn(() => Promise.resolve({ data: { executions_aggregate: null } })),
    listWorkflows: jest.fn(() => Promise.resolve({ data: { workflow_list: { workflows: [] } } })),
  },
}));

const SAVED_FILTERS_KEY = 'nudgebee:automation:executions:filters:v1:tenant-1';
const listExecutions = apiWorkflow.listAccountExecutions as jest.Mock;
const firstCall = () => listExecutions.mock.calls[0];

describe('ExecutionDashboard deep link from the Home Triggered (24h) stat (#40934)', () => {
  beforeEach(() => {
    listExecutions.mockClear();
    window.localStorage.setItem(SAVED_FILTERS_KEY, JSON.stringify({ statuses: ['FAILED'], workflowIds: ['wf-old'] }));
  });
  afterEach(() => window.localStorage.clear());

  it('lists runs in exactly the linked window, with no saved filter narrowing it', async () => {
    clearSavedExecutionFilters();
    render(<ExecutionDashboard />);

    await waitFor(() => expect(listExecutions).toHaveBeenCalled());
    const [accountIds, request] = firstCall();
    expect(accountIds).toEqual(['acc-1']);
    expect(request.start_date).toBe(FROM);
    expect(request.end_date).toBe(TO);
    expect(request.statuses).toBeUndefined();
    expect(request.workflow_ids).toBeUndefined();
    await waitFor(() => expect(document.body.textContent).toMatch(/Showing\s*1-20\s*of\s*37\s*results/));
  });

  it('shows the linked window on the date picker, not the 10-day default', async () => {
    clearSavedExecutionFilters();
    render(<ExecutionDashboard />);

    expect(await screen.findByText('Oct 11 - Oct 12')).toBeInTheDocument();
  });

  it('would narrow the list with a saved filter if it were not cleared first', async () => {
    render(<ExecutionDashboard />);

    await waitFor(() => expect(listExecutions).toHaveBeenCalled());
    expect(firstCall()[1].statuses).toEqual(['FAILED']);
    expect(firstCall()[1].workflow_ids).toEqual(['wf-old']);
  });
});
