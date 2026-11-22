import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import ExecutionsView from '../ExecutionsView';
import { LastExecutionCell } from '../WorkflowListing';

jest.mock('next/router', () => ({
  useRouter: () => ({ isReady: true, query: {}, pathname: '/automation/wf-1', asPath: '/automation/wf-1', replace: jest.fn(), push: jest.fn() }),
}));
jest.mock('reactflow', () => ({
  __esModule: true,
  ReactFlow: () => null,
  Background: () => null,
  Controls: () => null,
  MiniMap: () => null,
  PanOnScrollMode: { Free: 'free' },
  MarkerType: { ArrowClosed: 'arrowclosed' },
  Position: { Top: 'top', Bottom: 'bottom', Left: 'left', Right: 'right' },
  Handle: () => null,
  useReactFlow: () => ({}),
}));
jest.mock('reactflow/dist/style.css', () => ({}));
jest.mock('@lib/auth', () => ({
  hasWriteAccess: () => false,
  getUserSession: () => ({ roles: [] }),
  getCurrentTenant: () => ({ id: 'tenant-1' }),
}));
jest.mock('@hooks/useTenantBranding', () => ({ useTenantBranding: () => ({}) }));
jest.mock('@api1/workflow', () => ({
  __esModule: true,
  default: { getWorkflowExecution: jest.fn(() => Promise.resolve({ data: { workflow_get_execution: null } })) },
}));

const baseExecution = {
  status: 'COMPLETED' as const,
  start_time: '2026-10-07T09:34:41Z',
  close_time: '2026-10-07T09:34:43Z',
  trigger_type: 'event',
  workflow_id: 'wf-1',
};

describe('failed-steps mark (#40461)', () => {
  it('marks a completed run with a failed step in the automation Executions tab, and leaves a clean run alone', async () => {
    render(
      <ExecutionsView
        workflowId='wf-1'
        accountId='acc-1'
        loading={false}
        onRefresh={jest.fn()}
        taskDefinitions={[]}
        editorNodes={[]}
        editorEdges={[]}
        executions={[
          { ...baseExecution, id: 'run-failed', failed_step_count: 1, first_failed_step: 'send-slack-dm', first_failed_step_error: 'User not found' },
          { ...baseExecution, id: 'run-clean' },
        ]}
      />
    );

    await waitFor(() => expect(screen.getByTestId('execution-failed-steps-mark-run-failed')).toHaveTextContent('1 step failed'));
    expect(screen.queryByTestId('execution-failed-steps-mark-run-clean')).toBeNull();
  });

  it('marks the Automations list "last run" when it completed with failed steps', () => {
    const workflow = {
      id: 'wf-1',
      last_execution_status: 'COMPLETED',
      last_execution_time: '2026-10-07T09:34:43Z',
      last_execution_failed_step_count: 2,
    };
    const { rerender } = render(<LastExecutionCell workflow={workflow} />);
    expect(screen.getByText('completed')).toBeInTheDocument();
    expect(screen.getByTestId('workflow-last-run-failed-steps-wf-1')).toHaveTextContent('2 steps failed');

    rerender(<LastExecutionCell workflow={{ ...workflow, last_execution_failed_step_count: undefined }} />);
    expect(screen.queryByTestId('workflow-last-run-failed-steps-wf-1')).toBeNull();
  });
});
