import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import JiraAccountModal from '@components1/common/JiraAccountModal';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    nudgebeeMain: '#3B82F6',
    yellow: '#F59E0B',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      disabled: '#9CA3AF',
      secondaryDark: '#374151',
    },
    background: {
      primaryLightest: '#EFF6FF',
      white: '#fff',
      transparent: 'transparent',
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
      tertiaryBorder: '#BFDBFE',
      secondary: '#fff',
      secondaryBorder: '#D1D5DB',
      secondaryText: '#374151',
    },
  },
}));

jest.mock('@components1/common/modal', () => ({
  Modal: ({ open, handleClose: _handleClose, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled, loading }) => (
    <button onClick={onClick} disabled={disabled || loading} data-testid={`btn-${text}`}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children }) => <div>{children}</div>,
}));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ alt }) => <img alt={alt} />,
}));

jest.mock('@assets', () => ({ infoIcon: 'info-icon.svg' }));

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listTicketConfigurationsByTool: jest.fn().mockResolvedValue({ data: [] }),
    createTicketIntegration: jest.fn().mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    }),
  },
}));

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: {
    listTicketConfigurations: jest.fn().mockResolvedValue({ data: [] }),
  },
}));

jest.mock('src/utils/common', () => ({
  getAccountCreationSuccessMsg: jest.fn().mockReturnValue('Account created successfully!'),
}));

describe('JiraAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders modal when open is true', () => {
    render(<JiraAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Add Jira Account')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<JiraAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders all form fields', () => {
    render(<JiraAccountModal {...defaultProps} />);
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Account URL/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^User Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Token/)).toBeInTheDocument();
  });

  it('renders Cancel and Save buttons', () => {
    render(<JiraAccountModal {...defaultProps} />);
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
  });

  it('shows Edit Jira Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Jira', url: 'https://test.atlassian.net', username: 'user@test.com' };
    render(<JiraAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit Jira Account')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Update')).toBeInTheDocument();
  });

  it('prefills fields when editConfig is provided', () => {
    const editConfig = { id: 'edit-123', name: 'My Jira', url: 'https://test.atlassian.net', username: 'user@test.com' };
    render(<JiraAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByLabelText(/^Name/)).toHaveValue('My Jira');
    expect(screen.getByLabelText(/^Account URL/)).toHaveValue('https://test.atlassian.net');
    expect(screen.getByLabelText(/^User Name/)).toHaveValue('user@test.com');
  });

  it('updates name field on input change', () => {
    render(<JiraAccountModal {...defaultProps} />);
    const nameField = screen.getByLabelText(/^Name/);
    fireEvent.change(nameField, { target: { value: 'Test Jira Account' } });
    expect(nameField).toHaveValue('Test Jira Account');
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<JiraAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on save', async () => {
    const { snackbar } = require('@components1/common/snackbarService');
    render(<JiraAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestJira' } });
    fireEvent.change(screen.getByLabelText(/^Account URL/), { target: { value: 'https://test.atlassian.net' } });
    fireEvent.change(screen.getByLabelText(/^User Name/), { target: { value: 'user@test.com' } });
    fireEvent.change(screen.getByLabelText(/^Token/), { target: { value: 'token123' } });

    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(snackbar.success).toHaveBeenCalled();
    });
  });
});
