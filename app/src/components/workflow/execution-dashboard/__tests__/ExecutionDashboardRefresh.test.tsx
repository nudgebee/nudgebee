import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import apiWorkflow from '@api1/workflow';
import ExecutionDashboard from '../ExecutionDashboard';

jest.mock('next/router', () => ({
  useRouter: () => ({ isReady: true, query: {}, pathname: '/automation', asPath: '/automation', replace: jest.fn() }),
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
    listAccountExecutions: jest.fn(() => Promise.resolve({ data: { executions_list: { executions: [], total_count: 0 } } })),
    aggregateExecutions: jest.fn(() => Promise.resolve({ data: { executions_aggregate: null } })),
    listWorkflows: jest.fn(() => Promise.resolve({ data: { workflow_list: { workflows: [] } } })),
  },
}));
// Stub the picker so a test can choose an absolute range or a "Last N" shortcut.
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({
  __esModule: true,
  default: ({ onChange }: { onChange: (value: unknown) => void }) => (
    <>
      <button onClick={() => onChange({ selection: { startTime: ABSOLUTE_START, endTime: ABSOLUTE_END, shortcutClickTime: 0 } })}>
        pick-absolute
      </button>
      <button onClick={() => onChange({ selection: { startTime: Date.now() - HOUR_MS, endTime: Date.now(), shortcutClickTime: HOUR_MS } })}>
        pick-last-hour
      </button>
    </>
  ),
}));

const HOUR_MS = 60 * 60 * 1000;
const ABSOLUTE_START = Date.parse('2026-09-01T00:00:00Z');
const ABSOLUTE_END = Date.parse('2026-09-02T00:00:00Z');
const PAGE_LOAD = Date.parse('2026-09-14T12:06:04Z');

const listExecutions = apiWorkflow.listAccountExecutions as jest.Mock;
const lastRequest = () => listExecutions.mock.calls[listExecutions.mock.calls.length - 1][1];

const clickRefreshLater = async () => {
  const callsBefore = listExecutions.mock.calls.length;
  jest.setSystemTime(PAGE_LOAD + 5 * 60 * 1000);
  fireEvent.click(screen.getByTestId('execution-dashboard-refresh-btn'));
  await waitFor(() => expect(listExecutions.mock.calls.length).toBeGreaterThan(callsBefore));
};

describe('ExecutionDashboard Refresh', () => {
  beforeEach(() => {
    jest.useFakeTimers({ advanceTimers: true });
    jest.setSystemTime(PAGE_LOAD);
    listExecutions.mockClear();
  });
  afterEach(() => jest.useRealTimers());

  it('refetches the table up to now on the default range, so a run started after page load is included', async () => {
    render(<ExecutionDashboard />);
    await waitFor(() => expect(listExecutions).toHaveBeenCalled());
    expect(lastRequest().end_date).toBe(new Date(PAGE_LOAD).toISOString());

    await clickRefreshLater();

    expect(lastRequest().end_date).toBe(new Date(PAGE_LOAD + 5 * 60 * 1000).toISOString());
  });

  it('slides a "Last N" shortcut window forward on Refresh', async () => {
    render(<ExecutionDashboard />);
    await waitFor(() => expect(listExecutions).toHaveBeenCalled());
    fireEvent.click(screen.getByText('pick-last-hour'));
    await waitFor(() => expect(lastRequest().start_date).toBe(new Date(PAGE_LOAD - HOUR_MS).toISOString()));

    await clickRefreshLater();

    expect(lastRequest().start_date).toBe(new Date(PAGE_LOAD + 5 * 60 * 1000 - HOUR_MS).toISOString());
    expect(lastRequest().end_date).toBe(new Date(PAGE_LOAD + 5 * 60 * 1000).toISOString());
  });

  it('keeps a user-chosen absolute range on Refresh', async () => {
    render(<ExecutionDashboard />);
    await waitFor(() => expect(listExecutions).toHaveBeenCalled());
    fireEvent.click(screen.getByText('pick-absolute'));
    await waitFor(() => expect(lastRequest().end_date).toBe(new Date(ABSOLUTE_END).toISOString()));

    await clickRefreshLater();

    expect(lastRequest().start_date).toBe(new Date(ABSOLUTE_START).toISOString());
    expect(lastRequest().end_date).toBe(new Date(ABSOLUTE_END).toISOString());
  });
});
