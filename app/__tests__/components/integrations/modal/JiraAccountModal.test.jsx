import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import JiraAccountModal from '@components/integrations/modal/JiraAccountModal';

jest.mock('@utils/colors');

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));

jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Input', () => ({
  Input: ({ id, label, value, onChange, error, required }) => (
    <div>
      {label && (
        <label htmlFor={id}>
          {label}
          {required && '*'}
        </label>
      )}
      <input id={id} data-testid={id} value={value || ''} onChange={(e) => onChange?.(e.target.value)} />
      {error && <span>{error}</span>}
    </div>
  ),
}));

jest.mock('@ui/Tooltip', () => ({ __esModule: true, default: ({ children }) => <div>{children}</div> }));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('next/image', () => ({ __esModule: true, default: ({ alt }) => React.createElement('img', { alt }) }));

jest.mock('@assets', () => ({ infoIcon: 'info-icon.svg' }));

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listTicketConfigurationsByTool: jest.fn().mockResolvedValue({ data: [] }),
    createTicketIntegration: jest.fn().mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    }),
    testTicketConnectionByConfig: jest.fn().mockResolvedValue({ success: true }),
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
    expect(screen.getByTestId('cancel-create-account')).toBeInTheDocument();
    expect(screen.getByTestId('create-jira-acc')).toBeInTheDocument();
  });

  it('shows Edit Jira Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Jira', url: 'https://test.atlassian.net', username: 'user@test.com' };
    render(<JiraAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit Jira Account')).toBeInTheDocument();
    expect(screen.getByTestId('update-jira-acc')).toBeInTheDocument();
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
    fireEvent.click(screen.getByTestId('cancel-create-account'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on save', async () => {
    const { toast } = require('@ui/Toast');
    render(<JiraAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestJira' } });
    fireEvent.change(screen.getByLabelText(/^Account URL/), { target: { value: 'https://test.atlassian.net' } });
    fireEvent.change(screen.getByLabelText(/^User Name/), { target: { value: 'user@test.com' } });
    fireEvent.change(screen.getByLabelText(/^Token/), { target: { value: 'token123' } });

    fireEvent.click(screen.getByTestId('create-jira-acc'));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
  });
});
