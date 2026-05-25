import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ZenDutyAccountModal from '@components1/common/ZenDutyAccountModal';

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

jest.mock('@components1/common/modal/NDialog', () => {
  const PropTypes = require('prop-types');
  const NDialog = ({ open, dialogTitle, dialogContent }) =>
    open ? (
      <div data-testid='ndialog'>
        <div>{dialogTitle}</div>
        <div>{dialogContent}</div>
      </div>
    ) : null;
  NDialog.propTypes = { open: PropTypes.bool, dialogTitle: PropTypes.node, dialogContent: PropTypes.node };
  return { __esModule: true, default: NDialog };
});

jest.mock('@components1/common/CopyableText', () => {
  const PropTypes = require('prop-types');
  const CopyableText = ({ copyableText }) => <span data-testid='copyable-text'>{copyableText}</span>;
  CopyableText.propTypes = { copyableText: PropTypes.string };
  return { __esModule: true, default: CopyableText };
});

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listTicketConfigurationsByTool: jest.fn(),
    createTicketIntegration: jest.fn().mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    }),
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
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
  });

  it('shows validation errors on submit with empty form', async () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Save'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('shows email validation error for invalid email', async () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestZD' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'not-an-email' } });
    fireEvent.change(screen.getByLabelText(/^API Token/), { target: { value: 'mytoken123' } });
    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(screen.getByText('Please enter a valid email address')).toBeInTheDocument();
    });
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<ZenDutyAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on valid form submission', async () => {
    const { snackbar } = require('@components1/common/snackbarService');
    const apiIntegrations = require('@api1/integrations').default;
    apiIntegrations.createTicketIntegration.mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'new-id-123' } } },
    });

    render(<ZenDutyAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestZenDuty' } });
    fireEvent.change(screen.getByLabelText(/^Email/), { target: { value: 'user@test.com' } });
    fireEvent.change(screen.getByLabelText(/^API Token/), { target: { value: 'mytoken123' } });

    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(snackbar.success).toHaveBeenCalled();
    });
  });
});
