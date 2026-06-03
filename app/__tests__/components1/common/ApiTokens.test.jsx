import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import ApiTokens from '@components1/common/ApiTokens';

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
  Modal: ({ open, onClose: _onClose, title, children, footerContent }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
        {footerContent}
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

jest.mock('@components1/ds/Input', () => ({
  __esModule: true,
  Input: ({ label, value, onChange, placeholder }) => (
    <input
      aria-label={label}
      value={value || ''}
      placeholder={placeholder}
      onChange={(e) => onChange(e.target.value)}
      data-testid={`field-${label}`}
    />
  ),
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('next-auth/react', () => ({
  useSession: () => ({ data: { user: { email: 'test@test.com' } } }),
}));

jest.mock('@api1/user/', () => ({
  __esModule: true,
  default: {
    listUserTokens: jest.fn().mockResolvedValue({ data: [] }),
    createUserToken: jest.fn().mockResolvedValue({ data: { token: 'new-token-value-xyz' } }),
    deleteUserToken: jest.fn().mockResolvedValue({ data: {} }),
  },
}));

jest.mock('src/utils/common', () => ({
  parseHttpResponseBodyMessage: jest.fn().mockReturnValue('Error occurred'),
}));

describe('ApiTokens', () => {
  const defaultProps = {
    open: true,
    title: 'API Tokens',
    onClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders modal when open is true', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });
    expect(screen.getByRole('heading', { name: 'API Tokens' })).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<ApiTokens {...defaultProps} open={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('shows empty state message when no tokens', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByText('No API tokens found. Create your first token to get started.')).toBeInTheDocument();
    });
  });

  it('shows Create New Token button initially', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-Create New Token')).toBeInTheDocument();
    });
  });

  it('shows create token form when Create New Token is clicked', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-Create New Token')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-Create New Token'));
    expect(screen.getByTestId('field-Token Name')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Create Token')).toBeInTheDocument();
  });

  it('calls createUserToken and shows success on token creation', async () => {
    const { snackbar } = require('@components1/common/snackbarService');
    const apiUser = require('@api1/user/').default;
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-Create New Token')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-Create New Token'));
    fireEvent.change(screen.getByTestId('field-Token Name'), { target: { value: 'My Token' } });
    fireEvent.click(screen.getByTestId('btn-Create Token'));
    await waitFor(() => {
      expect(apiUser.createUserToken).toHaveBeenCalledWith('My Token');
      expect(snackbar.success).toHaveBeenCalledWith('API Token created successfully');
    });
  });

  it('displays tokens in a table when tokens exist', async () => {
    const apiUser = require('@api1/user/').default;
    apiUser.listUserTokens.mockResolvedValue({
      data: [{ id: 'tok-1', name: 'My Token', created_at: '2024-01-01T00:00:00Z', accessed_at: null }],
    });
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByText('My Token')).toBeInTheDocument();
    });
  });

  it('calls onClose when Close button is clicked', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-Close')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-Close'));
    expect(defaultProps.onClose).toHaveBeenCalled();
  });

  it('shows How to use instructions modal when button clicked', async () => {
    render(<ApiTokens {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('btn-How to use')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-How to use'));
    await waitFor(() => {
      expect(screen.getByText('How to use API Tokens')).toBeInTheDocument();
    });
  });
});
