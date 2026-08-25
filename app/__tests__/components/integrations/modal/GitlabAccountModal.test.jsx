import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import GitlabAccountModal from '@components/integrations/modal/GitlabAccountModal';

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
  getAccountCreationSuccessMsg: jest.fn(() => 'GitLab account added successfully.'),
}));

describe('GitlabAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders modal when open is true', () => {
    render(<GitlabAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
    expect(screen.getByText('Add GitLab Account')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<GitlabAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders all form fields with default GitLab URL', () => {
    render(<GitlabAccountModal {...defaultProps} />);
    expect(screen.getByLabelText(/^Name/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^GitLab URL/)).toHaveValue('https://gitlab.com');
    expect(screen.getByLabelText(/^Username/)).toBeInTheDocument();
    expect(screen.getByLabelText(/^Personal Access Token/)).toBeInTheDocument();
  });

  it('renders Cancel and Save buttons', () => {
    render(<GitlabAccountModal {...defaultProps} />);
    expect(screen.getByTestId('cancel-btn')).toBeInTheDocument();
    expect(screen.getByTestId('create-gitlab-acc')).toBeInTheDocument();
  });

  it('shows validation errors when saving empty form', async () => {
    render(<GitlabAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('create-gitlab-acc'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('shows Edit GitLab Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My GitLab', url: 'https://gitlab.com', username: 'testuser' };
    render(<GitlabAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit GitLab Account')).toBeInTheDocument();
    expect(screen.getByTestId('update-gitlab-acc')).toBeInTheDocument();
  });

  it('prefills fields in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My GitLab', url: 'https://mygitlab.example.com', username: 'testuser' };
    render(<GitlabAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByLabelText(/^Name/)).toHaveValue('My GitLab');
    expect(screen.getByLabelText(/^Username/)).toHaveValue('testuser');
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<GitlabAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('cancel-btn'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on valid save', async () => {
    const { toast } = require('@ui/Toast');
    render(<GitlabAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestGitlab' } });
    fireEvent.change(screen.getByLabelText(/^Username/), { target: { value: 'myuser' } });
    fireEvent.change(screen.getByLabelText(/^Personal Access Token/), { target: { value: 'mytoken123' } });

    fireEvent.click(screen.getByTestId('create-gitlab-acc'));

    await waitFor(() => {
      expect(toast.success).toHaveBeenCalledWith('GitLab account added successfully.');
    });
  });
});
