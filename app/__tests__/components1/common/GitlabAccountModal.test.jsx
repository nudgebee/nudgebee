import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import GitlabAccountModal from '@components1/common/GitlabAccountModal';

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
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
  });

  it('shows validation errors when saving empty form', async () => {
    render(<GitlabAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Save'));
    await waitFor(() => {
      expect(screen.getByText('Name is required')).toBeInTheDocument();
    });
  });

  it('shows Edit GitLab Account title and Update button in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My GitLab', url: 'https://gitlab.com', username: 'testuser' };
    render(<GitlabAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByText('Edit GitLab Account')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Update')).toBeInTheDocument();
  });

  it('prefills fields in edit mode', () => {
    const editConfig = { id: 'edit-123', name: 'My GitLab', url: 'https://mygitlab.example.com', username: 'testuser' };
    render(<GitlabAccountModal {...defaultProps} editConfig={editConfig} />);
    expect(screen.getByLabelText(/^Name/)).toHaveValue('My GitLab');
    expect(screen.getByLabelText(/^Username/)).toHaveValue('testuser');
  });

  it('calls handleClose when Cancel button is clicked', () => {
    render(<GitlabAccountModal {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });

  it('calls API and shows success on valid save', async () => {
    const { snackbar } = require('@components1/common/snackbarService');
    render(<GitlabAccountModal {...defaultProps} />);

    fireEvent.change(screen.getByLabelText(/^Name/), { target: { value: 'TestGitlab' } });
    fireEvent.change(screen.getByLabelText(/^Username/), { target: { value: 'myuser' } });
    fireEvent.change(screen.getByLabelText(/^Personal Access Token/), { target: { value: 'mytoken123' } });

    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(snackbar.success).toHaveBeenCalledWith('GitLab account added successfully.');
    });
  });
});
