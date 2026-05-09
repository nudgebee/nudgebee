import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import FeedbackComponent from '@components1/common/ThumpsUpAndDown';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      title: '#111827',
      primaryLight: '#60A5FA',
      success: '#16a34a',
      disabledInput: '#9CA3AF',
      secondaryDark: '#1F2937',
      yellowLabel: '#F59E0B',
      tertiarymedium: '#6B7280',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
      switchTrackDark: '#3B82F6',
      tertiaryLightest: '#F0F9FF',
      input: '#F9FAFB',
    },
    border: {
      secondary: '#D1D5DB',
      primary: '#3B82F6',
      success: '#22C55E',
      primaryLight: '#60A5FA',
      secondaryLight: '#E5E7EB',
      white: '#fff',
      vertical: '#E5E7EB',
    },
    button: {
      primary: '#3B82F6',
      primaryText: '#fff',
      primaryHover: '#2563EB',
      primaryDisabled: '#93C5FD',
      primaryDisabledText: '#fff',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
      secondaryHover: '#F9FAFB',
      secondaryHoverBorder: '#9CA3AF',
      secondaryDisabled: '#F3F4F6',
      secondaryDisabledText: '#9CA3AF',
      secondaryDisabledBorder: '#E5E7EB',
      tertiary: '#EFF6FF',
      tertiaryBorder: '#BFDBFE',
      tertiaryText: '#3B82F6',
      tertiaryHover: '#DBEAFE',
      tertiaryDisabled: '#F9FAFB',
      tertiaryDisabledText: '#93C5FD',
      tertiaryDisabledBorder: '#DBEAFE',
    },
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@components1/common/modal', () => ({
  Modal: ({ open, children, actionButtons, title }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
        {actionButtons}
      </div>
    ) : null,
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled }) => (
    <button onClick={onClick} disabled={disabled}>
      {text}
    </button>
  ),
}));

jest.mock('react-icons/bi', () => ({
  BiLike: () => <svg data-testid='bi-like' />,
  BiDislike: () => <svg data-testid='bi-dislike' />,
}));

const defaultSentFeedback = { submitted: false, isPositive: null, message: '' };

describe('FeedbackComponent (ThumpsUpAndDown)', () => {
  const mockOnFeedbackSubmit = jest.fn();

  beforeEach(() => {
    mockOnFeedbackSubmit.mockClear();
    const { snackbar } = require('@components1/common/snackbarService');
    snackbar.success.mockClear();
    snackbar.error.mockClear();
  });

  it('renders Yes and No buttons', () => {
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    expect(screen.getByText('Yes')).toBeInTheDocument();
    expect(screen.getByText('No')).toBeInTheDocument();
  });

  it('calls onFeedbackSubmit with thumbs_up when Yes clicked', async () => {
    mockOnFeedbackSubmit.mockResolvedValue(undefined);
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('Yes'));
    await waitFor(() => {
      expect(mockOnFeedbackSubmit).toHaveBeenCalledWith({ type: 'thumbs_up', message: '' });
    });
  });

  it('shows success snackbar after thumbs up', async () => {
    mockOnFeedbackSubmit.mockResolvedValue(undefined);
    const { snackbar } = require('@components1/common/snackbarService');
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('Yes'));
    await waitFor(() => {
      expect(snackbar.success).toHaveBeenCalledWith('Feedback submitted successfully!');
    });
  });

  it('opens dialog when No clicked', () => {
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('No'));
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('What went wrong?')).toBeInTheDocument();
  });

  it('closes dialog when Cancel clicked', () => {
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('No'));
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Cancel'));
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('Submit button is disabled when no option selected and feedback is empty', () => {
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('No'));
    const submitButton = screen.getByText('Submit').closest('button');
    expect(submitButton).toBeDisabled();
  });

  it('Submit button enabled when an option is selected', () => {
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('No'));
    // Click one of the checkbox options
    const checkbox = screen.getByLabelText('Agent/Plan Incorrect');
    fireEvent.click(checkbox);
    const submitButton = screen.getByText('Submit').closest('button');
    expect(submitButton).not.toBeDisabled();
  });

  it('calls onFeedbackSubmit with thumbs_down on submit', async () => {
    mockOnFeedbackSubmit.mockResolvedValue(undefined);
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('No'));
    const checkbox = screen.getByLabelText('Agent/Plan Incorrect');
    fireEvent.click(checkbox);
    fireEvent.click(screen.getByText('Submit'));
    await waitFor(() => {
      expect(mockOnFeedbackSubmit).toHaveBeenCalledWith(expect.objectContaining({ type: 'thumbs_down' }));
    });
  });

  it('shows error snackbar when onFeedbackSubmit throws', async () => {
    mockOnFeedbackSubmit.mockRejectedValue(new Error('Network error'));
    const { snackbar } = require('@components1/common/snackbarService');
    render(<FeedbackComponent onFeedbackSubmit={mockOnFeedbackSubmit} sentFeedback={defaultSentFeedback} />);
    fireEvent.click(screen.getByText('Yes'));
    await waitFor(() => {
      expect(snackbar.error).toHaveBeenCalledWith('Error submitting feedback. Please try again.');
    });
  });
});
