import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import ExecutionStatusBar from '../ExecutionStatusBar';

describe('ExecutionStatusBar', () => {
  it('shows the dry-run label and approval buttons that submit the decision', () => {
    const onApprove = jest.fn();
    render(
      <ExecutionStatusBar
        visible
        label='Dry run in progress...'
        pendingApprovals={[{ taskId: 'gate', options: ['approve', 'reject'] }]}
        onApprove={onApprove}
      />
    );

    expect(screen.getByText('Dry run in progress...')).toBeInTheDocument();
    expect(screen.queryByText('Manual run in progress...')).not.toBeInTheDocument();
    fireEvent.click(document.getElementById('workflow-approval-reject-btn') as HTMLElement);
    expect(onApprove).toHaveBeenCalledWith('gate', 'reject', undefined);
  });

  it('defaults to the manual-run label', () => {
    render(<ExecutionStatusBar visible />);
    expect(screen.getByText('Manual run in progress...')).toBeInTheDocument();
  });
});
