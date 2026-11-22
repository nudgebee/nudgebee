import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
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
jest.mock('@shared/widgets/CustomDateTimeRangePicker', () => ({ __esModule: true, default: () => null }));
jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: {
    listAccountExecutions: jest.fn(() =>
      Promise.resolve({
        data: {
          executions_list: {
            total_count: 2,
            executions: [
              {
                id: 'run-failed',
                account_id: 'acc-1',
                workflow_id: 'wf-1',
                workflow_name: 'Notify on-call',
                status: 'COMPLETED',
                start_time: '2026-10-07T09:34:41Z',
                close_time: '2026-10-07T09:34:43Z',
                failed_step_count: 1,
                first_failed_step: 'send-slack-dm',
                first_failed_step_error: 'User not found',
              },
              {
                id: 'run-clean',
                account_id: 'acc-1',
                workflow_id: 'wf-1',
                workflow_name: 'Notify on-call',
                status: 'COMPLETED',
                start_time: '2026-10-07T09:30:00Z',
                close_time: '2026-10-07T09:30:02Z',
              },
            ],
          },
        },
      })
    ),
    aggregateExecutions: jest.fn(() => Promise.resolve({ data: { executions_aggregate: null } })),
    listWorkflows: jest.fn(() => Promise.resolve({ data: { workflow_list: { workflows: [] } } })),
  },
}));

describe('ExecutionDashboard failed-steps mark (#40461)', () => {
  it('marks a completed run with a failed step and shows that step error in the Error column', async () => {
    render(<ExecutionDashboard />);

    await waitFor(() => expect(screen.getByTestId('execution-dashboard-failed-steps-mark-run-failed')).toHaveTextContent('1 step failed'));
    expect(screen.getByText('send-slack-dm: User not found')).toBeInTheDocument();
    expect(screen.queryByTestId('execution-dashboard-failed-steps-mark-run-clean')).toBeNull();
  });
});
