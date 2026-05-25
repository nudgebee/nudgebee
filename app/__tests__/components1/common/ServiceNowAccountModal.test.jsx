import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ServiceNowAccountModal from '@components1/common/ServiceNowAccountModal';

jest.mock('src/utils/colors', () => ({
  colors: {
    primary: '#3B82F6',
    text: {
      primary: '#3B82F6',
      secondary: '#374151',
      white: '#fff',
      black: '#000',
      tertiary: '#6B7280',
      secondaryDark: '#374151',
    },
    background: { primaryLightest: '#EFF6FF', white: '#fff' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
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

jest.mock('@components1/common/modal', () => {
  const PropTypes = require('prop-types');
  const Modal = function Modal({ open, title, children }) {
    return open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null;
  };
  Modal.propTypes = { open: PropTypes.bool, title: PropTypes.node, children: PropTypes.node };
  return { Modal };
});

jest.mock('@components1/common/NewCustomButton', () => {
  const PropTypes = require('prop-types');
  const NewCustomButton = ({ text, onClick, disabled, loading }) => (
    <button onClick={onClick} disabled={disabled || loading} data-testid={`btn-${text}`}>
      {text}
    </button>
  );
  NewCustomButton.propTypes = { text: PropTypes.string, onClick: PropTypes.func, disabled: PropTypes.bool, loading: PropTypes.bool };
  return { __esModule: true, default: NewCustomButton };
});

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@components1/common/CustomTooltip', () => ({
  __esModule: true,
  default: ({ children }) => <div>{children}</div>,
}));

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listTicketConfigurationsByTool: jest.fn().mockResolvedValue({ data: [] }),
    addIntegrations: jest.fn().mockResolvedValue({
      data: { data: { integrations_create_config: { configs: [] } } },
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
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
  });

  it('shows validation errors when saving empty form', async () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Save'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<ServiceNowAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
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
    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(screen.getByText('ExistingAccount already exists. Please choose a different name.')).toBeInTheDocument();
    });
  });
});
