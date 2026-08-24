import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import '@testing-library/jest-dom';
import K8sAccountModal from '@components/integrations/modal/K8sAccountModal';

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

jest.mock('@ui/ToggleGroup', () => ({
  ToggleGroup: ({ options = [], onChange, value: activeValue }) => (
    <div data-testid='toggle-group'>
      {options.map((opt) => (
        <button key={opt.value} data-active={activeValue === opt.value} disabled={opt.disabled} onClick={() => onChange?.(opt.value)}>
          {opt.label}
        </button>
      ))}
    </div>
  ),
}));

jest.mock('@shared/navigation/Tabs', () => ({
  __esModule: true,
  default: ({ tabs = [] }) => (
    <div data-testid='tabs'>
      {tabs.map((t, i) => (
        <div key={i}>{t.label || t.text}</div>
      ))}
    </div>
  ),
}));

jest.mock('@ui/Banner', () => ({
  Banner: ({ message }) => <div data-testid='banner'>{message}</div>,
}));

jest.mock('@ui/Tooltip', () => ({ __esModule: true, default: ({ children }) => <div>{children}</div> }));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@shared/layout/UpdateDataContext', () => ({
  useUpdateAllClusterOption: () => jest.fn(),
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => <span>{alt}</span>,
}));

jest.mock('@assets', () => ({
  CopyIconBlue: 'copy-icon.svg',
  PlayCircleIcon: 'play-circle.svg',
  infoIcon: 'info.svg',
}));

jest.mock('@api1/account', () => ({
  __esModule: true,
  default: {
    createAccount: jest.fn().mockResolvedValue({
      data: {
        status: 'SUCCESS',
        data: {
          accounts_create: {
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

  it('renders environment type toggle (Production / Non-production)', () => {
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
    expect(screen.getByTestId('create-k8s-acc')).toBeDisabled();
  });

  it('Next button is enabled after valid account name entry', () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    expect(screen.getByTestId('create-k8s-acc')).not.toBeDisabled();
  });

  it('calls createAccount API and advances to step 2 on Next click', async () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    fireEvent.click(screen.getByTestId('create-k8s-acc'));
    await waitFor(() => {
      expect(screen.getByTestId('finish-btn')).toBeInTheDocument();
    });
  });

  it('calls handleClose on Finish button click', async () => {
    const { isK8sAccountNameValid } = require('src/utils/common');
    isK8sAccountNameValid.mockReturnValue(true);
    render(<K8sAccountModal {...defaultProps} />);
    fireEvent.change(screen.getByLabelText(/^Account Name/), { target: { value: 'MyCluster' } });
    fireEvent.click(screen.getByTestId('create-k8s-acc'));
    await waitFor(() => {
      expect(screen.getByTestId('finish-btn')).toBeInTheDocument();
    });
    fireEvent.click(screen.getByTestId('finish-btn'));
    expect(defaultProps.handleClose).toHaveBeenCalled();
  });
});
