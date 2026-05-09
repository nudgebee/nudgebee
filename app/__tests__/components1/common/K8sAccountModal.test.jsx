import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import K8sAccountModal from '@components1/common/K8sAccountModal';

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
    secondary: { dark: '#374151' },
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

jest.mock('@components1/common/modal', () => ({
  Modal: ({ open, handleClose: _handleClose, title, children }) =>
    open ? (
      <div data-testid='modal'>
        <div>{title}</div>
        {children}
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

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@components1/common/UpdateDataContext', () => ({
  useUpdateAllClusterOption: () => jest.fn(),
}));

jest.mock('@components1/common/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({
  CopyIconBlue: 'copy-icon.svg',
  PlayCircleIcon: 'play-circle.svg',
}));

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    createAccount: jest.fn().mockResolvedValue({
      data: {
        status: 'SUCCESS',
        data: {
          cloud_accounts_insert_one: {
            access_key: 'test-key',
            access_secret: 'test-secret',
          },
        },
      },
    }),
  },
}));

jest.mock('src/utils/common', () => ({
  isK8sAccountNameValid: jest.fn((value) => value.length >= 4 && value.length <= 50),
}));

describe('K8sAccountModal', () => {
  const defaultProps = {
    openModal: true,
    handleClose: jest.fn(),
    handleOnAccountCreate: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
    // Mock clipboard
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: jest.fn().mockResolvedValue(undefined) },
      writable: true,
    });
  });

  it('renders modal when open is true', () => {
    render(<K8sAccountModal {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('does not render when open is false', () => {
    render(<K8sAccountModal {...defaultProps} openModal={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders step 1 with account name field', () => {
    render(<K8sAccountModal {...defaultProps} />);
    expect(screen.getByLabelText(/^Account Name/)).toBeInTheDocument();
  });

  it('renders environment type radio buttons', () => {
    render(<K8sAccountModal {...defaultProps} />);
    expect(screen.getByText('Production')).toBeInTheDocument();
    expect(screen.getByText('Non-production')).toBeInTheDocument();
  });

  it('shows validation error for short account name', () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(false);
    render(<K8sAccountModal {...defaultProps} />);
    const nameInput = screen.getByLabelText(/^Account Name/);
    fireEvent.change(nameInput, { target: { value: 'ab' } });
    expect(
      screen.getByText(
        'Minimum 4 and Maximum 50 Characters. Name accepts alphanumeric, space, hyphen and underscore. Name should not start or end with space, hyphen or underscore'
      )
    ).toBeInTheDocument();
  });

  it('Next button is disabled when account name is empty', () => {
    render(<K8sAccountModal {...defaultProps} />);
    expect(screen.getByTestId('btn-Next')).toBeDisabled();
  });

  it('Next button is enabled after valid account name entry', () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    expect(screen.getByTestId('btn-Next')).not.toBeDisabled();
  });

  it('calls createAccount API and advances to step 2 on Next click', async () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    fireEvent.click(screen.getByTestId('btn-Next'));
    await waitFor(() => {
      expect(screen.getByTestId('btn-Finish')).toBeInTheDocument();
    });
  });

  it('calls handleClose on Finish button click', async () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    fireEvent.click(screen.getByTestId('btn-Next'));
    await waitFor(() => {
      expect(screen.getByTestId('btn-Finish')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('btn-Finish'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });
});
