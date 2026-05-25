import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import GithubAccountModal from '@components1/common/GithubAccountModal';

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

jest.mock('next/image', () => {
  const PropTypes = require('prop-types');
  const NextImage = ({ alt }) => <img alt={alt} />;
  NextImage.propTypes = { alt: PropTypes.string };
  return { __esModule: true, default: NextImage };
});

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

// Mock fetch for github app name config
global.fetch = jest.fn().mockResolvedValue({
  json: jest.fn().mockResolvedValue({ githubAppName: 'nudgebee' }),
});

describe('GithubAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    global.fetch = jest.fn().mockResolvedValue({
      json: jest.fn().mockResolvedValue({ githubAppName: 'nudgebee' }),
    });
  });

  it('renders modal when open is true', () => {
    render(<GithubAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Add Github Account')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<GithubAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders auth type selection in add mode', () => {
    render(<GithubAccountModal {...defaultProps} />);
    expect(screen.getByText('Application')).toBeInTheDocument();
    expect(screen.getByText('User Token')).toBeInTheDocument();
  });

  it('shows github-app auth content by default', () => {
    render(<GithubAccountModal {...defaultProps} />);
    expect(screen.getByTestId('btn-Authenticate with Github App')).toBeInTheDocument();
  });

  it('shows user token fields when User Token radio is selected', () => {
    render(<GithubAccountModal {...defaultProps} />);
    // Click the User Token radio button to trigger RadioGroup onChange
    const userTokenLabel = screen.getByText('User Token');
    fireEvent.click(userTokenLabel);
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Token/)).toBeInTheDocument();
  });

  it('shows Edit Github Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Github', url: 'api.github.com', username: 'testuser' };
    render(<GithubAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit Github Account')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Update')).toBeInTheDocument();
  });

  it('prefills fields in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Github', url: 'api.github.com', username: 'testuser' };
    render(<GithubAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByLabelText(/^Name/)).toHaveValue('My Github');
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<GithubAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('shows validation errors when saving empty user-token form', async () => {
    render(<GithubAccountModal {...defaultProps} />);
    // Click User Token label to switch auth type
    fireEvent.click(screen.getByText('User Token'));
    fireEvent.click(screen.getByTestId('btn-Save'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });
});
