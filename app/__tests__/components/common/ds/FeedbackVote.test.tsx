jest.mock('@ui/Toast', () => ({
  __esModule: true,
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@ui/Modal', () => ({
  __esModule: true,
  Modal: ({ open, children, title, actionButtons }: { open: boolean; children?: React.ReactNode; title?: string; actionButtons?: React.ReactNode }) =>
    open ? (
      <div role='dialog'>
        {title && <div>{title}</div>}
        {children}
        {actionButtons}
      </div>
    ) : null,
  default: ({
    open,
    children,
    title,
    actionButtons,
  }: {
    open: boolean;
    children?: React.ReactNode;
    title?: string;
    actionButtons?: React.ReactNode;
  }) =>
    open ? (
      <div role='dialog'>
        {title && <div>{title}</div>}
        {children}
        {actionButtons}
      </div>
    ) : null,
}));

import React from 'react';
import { render, screen, fireEvent, act } from '@testing-library/react';
import FeedbackVote from '@ui/FeedbackVote';
import { toast } from '@ui/Toast';

describe('FeedbackVote', () => {
  it('renders Yes and No buttons', () => {
    render(<FeedbackVote onFeedbackSubmit={jest.fn()} />);
    expect(screen.getByText('Yes')).toBeInTheDocument();
    expect(screen.getByText('No')).toBeInTheDocument();
  });

  it('calls onFeedbackSubmit with thumbs_up when Yes is clicked', async () => {
    const onFeedbackSubmit = jest.fn().mockResolvedValue(undefined);
    render(<FeedbackVote onFeedbackSubmit={onFeedbackSubmit} />);
    await act(async () => {
      fireEvent.click(screen.getByText('Yes'));
    });
    expect(onFeedbackSubmit).toHaveBeenCalledWith({ type: 'thumbs_up', message: '' });
  });

  it('shows success toast after thumbs up', async () => {
    const onFeedbackSubmit = jest.fn().mockResolvedValue(undefined);
    render(<FeedbackVote onFeedbackSubmit={onFeedbackSubmit} />);
    await act(async () => {
      fireEvent.click(screen.getByText('Yes'));
    });
    expect(toast.success).toHaveBeenCalledWith('Feedback submitted successfully!');
  });

  it('opens dialog when No is clicked', () => {
    render(<FeedbackVote onFeedbackSubmit={jest.fn()} />);
    fireEvent.click(screen.getByText('No'));
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(screen.getByText('What went wrong?')).toBeInTheDocument();
  });

  it('closes dialog when Cancel is clicked', () => {
    render(<FeedbackVote onFeedbackSubmit={jest.fn()} />);
    fireEvent.click(screen.getByText('No'));
    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renders iconOnly mode', () => {
    render(<FeedbackVote onFeedbackSubmit={jest.fn()} iconOnly />);
    expect(screen.getByRole('button', { name: 'Yes' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'No' })).toBeInTheDocument();
  });

  it('reflects submitted=true positive state from sentFeedback', () => {
    render(<FeedbackVote onFeedbackSubmit={jest.fn()} sentFeedback={{ submitted: true, isPositive: true }} />);
    expect(screen.getByText('Yes')).toBeInTheDocument();
  });

  it('shows error toast when submission fails', async () => {
    const onFeedbackSubmit = jest.fn().mockRejectedValue(new Error('Network error'));
    render(<FeedbackVote onFeedbackSubmit={onFeedbackSubmit} />);
    await act(async () => {
      fireEvent.click(screen.getByText('Yes'));
    });
    expect(toast.error).toHaveBeenCalledWith('Error submitting feedback. Please try again.');
  });
});
