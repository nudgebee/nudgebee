import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ZenDutyAccountModal from '@components/integrations/modal/ZenDutyAccountModal';

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
  Input: ({ id, label, value, onChange, error, required, disabled }) => (
    <div>
      {label && (
        <label htmlFor={id}>
          {label}
          {required && '*'}
        </label>
      )}
      <input id={id} data-testid={id} value={value || ''} disabled={disabled} onChange={(e) => onChange?.(e.target.value)} />
      {error && <span>{error}</span>}
    </div>
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ id, checked, onChange, label }) => (
    <label>
      <input id={id} data-testid={id} type='checkbox' checked={!!checked} onChange={(e) => onChange?.(e.target.checked)} />
      {label}
    </label>
  ),
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listTicketConfigurationsByTool: jest.fn().mockResolvedValue({ data: [] }),
    createTicketIntegration: jest.fn().mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    }),
    addIntegrations: jest.fn().mockResolvedValue({
      data: { data: { integrations_create_config: { configs: [] } } },
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
  parseHttpResponseBodyMessage: jest.fn().mockReturnValue('Error occurred'),
}));

describe('ZenDutyAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    const apiTicketIntegrations = require('@api1/tickets').default;
    apiTicketIntegrations.listTicketConfigurations.mockResolvedValue({ data: [] });
  });

  it('renders modal when open is true', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Add ZenDuty Account')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<ZenDutyAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders all form fields', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Account URL/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Email/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^API Token/)).toBeInTheDocument();
  });

  it('shows read-only ZenDuty URL field', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    const urlField = screen.getByLabelText(/^Account URL/);
    expect(urlField).toHaveValue('www.zenduty.com');
    expect(urlField).toBeDisabled();
  });

  it('renders Cancel and Save buttons', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    expect(screen.getByTestId('cancel-btn')).toBeInTheDocument();
    expect(screen.getByTestId('create-zenduty-acc')).toBeInTheDocument();
  });

  it('shows validation errors on submit with empty form', async () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('create-zenduty-acc'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('shows email validation error for invalid email', async () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestZD' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'not-an-email' } });
    fireEvent.change(screen.getByLabelText(/^API Token/), { target: { value: 'mytoken123' } });
    fireEvent.click(screen.getByTestId('create-zenduty-acc'));

    await waitFor(() => {
      expect(screen.getByText('Please enter a valid email address')).toBeInTheDocument();
    });
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('cancel-btn'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on valid form submission', async () => {
    const { toast } = require('@ui/Toast');
    const apiIntegrations = require('@api1/integrations').default;
    apiIntegrations.createTicketIntegration.mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    });

    render(<ZenDutyAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestZenDuty' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'user@test.com' } });
    fireEvent.change(screen.getByLabelText(/^API Token/), { target: { value: 'mytoken123' } });

    fireEvent.click(screen.getByTestId('create-zenduty-acc'));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalled();
    });
  });
});
