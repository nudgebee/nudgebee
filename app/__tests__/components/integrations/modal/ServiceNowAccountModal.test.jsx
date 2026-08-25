import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ServiceNowAccountModal from '@components/integrations/modal/ServiceNowAccountModal';

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

describe('ServiceNowAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders modal when open is true', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Add ServiceNow Account')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<ServiceNowAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders all form fields', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Instance URL/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Password/)).toBeInTheDocument();
  });

  it('renders Sync Knowledge Base checkbox', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    expect(screen.getByText('Sync Knowledge Base')).toBeInTheDocument();
  });

  it('renders Cancel and Save buttons', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    expect(screen.getByTestId('cancel-btn')).toBeInTheDocument();
    expect(screen.getByTestId('create-servicenow-acc')).toBeInTheDocument();
  });

  it('shows validation errors when saving empty form', async () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('create-servicenow-acc'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('cancel-btn'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('toggles sync knowledge base checkbox', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    const checkbox = screen.getByRole('checkbox');
    expect(checkbox).not.toBeChecked();
    fireEvent.click(checkbox);
    expect(checkbox).toBeChecked();
  });

  it('shows duplicate name error when name already exists', async () => {
    const apiIntegrations = require('@api1/integrations').default;
    apiIntegrations.listTicketConfigurationsByTool.mockResolvedValue({
      data: [{ name: 'ExistingAccount' }],
    });
    render(<ServiceNowAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'ExistingAccount' } });
    fireEvent.change(screen.getByLabelText(/^Instance URL/), { target: { value: 'https://test.service-now.com' } });
    fireEvent.change(screen.getByLabelText(/^Username/), { target: { value: 'testuser' } });
    fireEvent.change(screen.getByLabelText(/^Password/), { target: { value: 'testpassword' } });
    fireEvent.click(screen.getByTestId('create-servicenow-acc'));

    await waitFor(() => {
      expect(screen.getByText('ExistingAccount already exists. Please choose a different name.')).toBeInTheDocument();
    });
  });
});
