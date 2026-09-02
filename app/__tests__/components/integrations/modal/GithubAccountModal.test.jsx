import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import GithubAccountModal from '@components/integrations/modal/GithubAccountModal';

jest.mock('@utils/colors');

// Modal → @ui/Modal (named export)
jest.mock('@ui/Modal', () => ({
  Modal: ({ open, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));

// Button → @ui/Button (named, children → testid `btn-${children}`)
jest.mock('@ui/Button', () => ({
  Button: ({ children, onClick, id, disabled, loading }) => (
    <button data-testid={id || `btn-${children}`} onClick={onClick} disabled={disabled || loading}>
      {children}
    </button>
  ),
}));

// Input → @ui/Input (named, label + onChange(value))
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

// ToggleGroup → @ui/ToggleGroup (named, options=[{value,label}], onChange(value))
jest.mock('@ui/ToggleGroup', () => ({
  ToggleGroup: ({ options = [], value: activeValue, onChange }) => (
    <div data-testid='toggle-group'>
      {options.map((opt) => (
        <button key={opt.value} data-active={activeValue === opt.value} disabled={opt.disabled} onClick={() => onChange?.(opt.value)}>
          {opt.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@ui/Tooltip', () => ({
  __esModule: true,
  default: ({ children }) => <div>{children}</div>,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span data-testid={`icon-${alt}`}>icon</span>,
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('next/image', () => ({
  __esModule: true,
  default: ({ alt }) => React.createElement('img', { alt }),
}));

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
    expect(screen.getByTestId('authenticate-btn')).toBeInTheDocument();
  });

  it('shows user token fields when User Token toggle is selected', () => {
    render(<GithubAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByText('User Token'));
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Token/)).toBeInTheDocument();
  });

  it('shows Edit Github Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Github', url: 'api.github.com', username: 'testuser' };
    render(<GithubAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit Github Account')).toBeInTheDocument();
    expect(screen.getByTestId('update-github-acc')).toBeInTheDocument();
  });

  it('prefills fields in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My Github', url: 'api.github.com', username: 'testuser' };
    render(<GithubAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByLabelText(/^Name/)).toHaveValue('My Github');
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<GithubAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('cancel-btn'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('shows validation errors when saving empty user-token form', async () => {
    render(<GithubAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByText('User Token'));
    fireEvent.click(screen.getByTestId('create-github-acc'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });
});
