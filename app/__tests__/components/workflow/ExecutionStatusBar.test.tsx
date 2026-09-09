import React from 'react';
import { render, screen } from '@testing-library/react';
import ExecutionStatusBar from '@components/workflow/components/ExecutionStatusBar';

const pendingApprovals = [{ taskId: 'deployment-approval-gate', options: ['approve', 'reject'] }];

describe('ExecutionStatusBar', () => {
  it('renders the approval form while the run is live', () => {
    render(<ExecutionStatusBar visible pendingApprovals={pendingApprovals} onApprove={jest.fn()} completedTasks={0} totalTasks={1} />);
    expect(screen.getByText('Waiting for approval')).toBeInTheDocument();
    expect(screen.getByText('deployment-approval-gate: respond below or via Slack/MS Teams')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'approve' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'reject' })).toBeInTheDocument();
  });

  // A cancelled run leaves its task at SCHEDULED until the next fetch, and the buttons
  // would only 409 against a closed Temporal workflow (#36358).
  it('renders nothing once the run is no longer live, even with a task still pending', () => {
    const { container } = render(
      <ExecutionStatusBar visible={false} pendingApprovals={pendingApprovals} onApprove={jest.fn()} completedTasks={0} totalTasks={1} />
    );
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByText('Waiting for approval')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'approve' })).not.toBeInTheDocument();
  });

  it('renders the progress pill without an approval section when nothing is pending', () => {
    render(<ExecutionStatusBar visible completedTasks={2} totalTasks={5} />);
    expect(screen.getByText('Manual run in progress...')).toBeInTheDocument();
    expect(screen.getByText('2/5 tasks')).toBeInTheDocument();
    expect(screen.queryByText('Waiting for approval')).not.toBeInTheDocument();
  });
});
