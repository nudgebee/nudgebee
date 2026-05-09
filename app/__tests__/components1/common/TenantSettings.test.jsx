import React from 'react';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import TenantSettings from '@components1/common/TenantSettings';

jest.mock('src/utils/colors', () => ({
  colors: {
    text: { primary: '#111827', secondary: '#374151', tertiary: '#6B7280' },
    border: { secondary: '#D1D5DB', primary: '#3B82F6' },
    background: { white: '#fff' },
  },
}));

jest.mock('next-auth/react', () => ({
  useSession: () => ({
    data: {
      user: { email: 'admin@example.com' },
      tenant: { name: 'TestTenant' },
    },
  }),
}));

jest.mock('@lib/UserService', () => ({
  getTenantAttributes: jest.fn().mockResolvedValue([]),
  getFeatures: jest.fn().mockResolvedValue([]),
  upsertTenantAttributes: jest.fn().mockResolvedValue({ data: {} }),
  deleteTenantAttributes: jest.fn().mockResolvedValue({ data: {} }),
  updateTenantFeatureFlag: jest.fn().mockResolvedValue({ data: {} }),
}));

jest.mock('@lib/auth', () => ({
  fetchFeatureFlagsForTenant: jest.fn().mockResolvedValue([]),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listUserTenants: jest.fn().mockResolvedValue({ data: [{ name: 'TestTenant' }] }),
  },
}));

jest.mock('@components1/common/snackbarService', () => ({
  snackbar: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('src/utils/common', () => ({
  parseHttpResponseBodyMessage: jest.fn((e) => String(e)),
  safeJSONParse: jest.fn((val) => {
    try {
      return JSON.parse(val);
    } catch {
      return null;
    }
  }),
}));

jest.mock('@components1/common/modal', () => ({
  Modal: ({ open, children, title }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));

jest.mock('@components1/common/TenantAccountCommonSettings', () => ({
  __esModule: true,
  default: ({ logSettings: _logSettings, setLogSettings: _setLogSettings }) => (
    <div data-testid='tenant-account-common-settings'>Log Label Mapper</div>
  ),
}));

jest.mock('@components1/common/CustomTextField', () => ({
  __esModule: true,
  default: ({ label, value, onChange, disabled, placeholder }) => (
    <div>
      <label htmlFor={`field-${label}`}>{label}</label>
      <input
        id={`field-${label}`}
        data-testid={`field-${label}`}
        value={value || ''}
        onChange={onChange}
        disabled={disabled}
        placeholder={placeholder}
      />
    </div>
  ),
}));

jest.mock('@components1/common/CustomCheckbox', () => ({
  __esModule: true,
  default: ({ text, checked, onChange }) => (
    <label>
      <input type='checkbox' checked={checked} onChange={onChange} />
      {text}
    </label>
  ),
}));

jest.mock('@components1/common/NewCustomButton', () => ({
  __esModule: true,
  default: ({ text, onClick, disabled }) => (
    <button onClick={onClick} disabled={disabled} data-testid={`btn-${text}`}>
      {text}
    </button>
  ),
}));

jest.mock('@components1/common/TextWithBorder', () => ({
  __esModule: true,
  default: ({ value }) => <div data-testid='text-with-border'>{value}</div>,
}));

jest.mock('@components1/common/CustomAutocomplete', () => ({
  __esModule: true,
  default: ({ label }) => <div data-testid={`autocomplete-${label}`}>{label}</div>,
}));

global.fetch = jest.fn().mockResolvedValue({
  ok: true,
  json: jest.fn().mockResolvedValue({}),
});

describe('TenantSettings', () => {
  const defaultProps = {
    open: true,
    title: 'Tenant Settings',
    onClose: jest.fn(),
  };

  beforeEach(() => {
    jest.clearAllMocks();
  });

  it('renders modal when open is true', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<TenantSettings {...defaultProps} open={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders the modal title', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByText('Tenant Settings')).toBeInTheDocument();
  });

  it('renders Tenant Name field', async () => {
    render(<TenantSettings {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('field-Tenant Name')).toBeInTheDocument();
    });
  });

  it('renders Save and Cancel buttons', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
  });

  it('calls onClose when Cancel button is clicked', () => {
    render(<TenantSettings {...defaultProps} />);
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.onClose).toHaveBeenCalledWith(null, 'hide');
  });

  it('renders domain login checkbox', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByText('For Self-Onboarding Enable specific domain login')).toBeInTheDocument();
  });

  it('renders Allowed Domains field (disabled by default)', () => {
    render(<TenantSettings {...defaultProps} />);
    const allowedDomainsField = screen.getByTestId('field-Allowed Domains');
    expect(allowedDomainsField).toBeDisabled();
  });

  it('renders Default Auth Role field (disabled by default)', () => {
    render(<TenantSettings {...defaultProps} />);
    const authRoleField = screen.getByTestId('field-Default Auth Role');
    expect(authRoleField).toBeDisabled();
  });

  it('enables Allowed Domains field after checking the checkbox', () => {
    render(<TenantSettings {...defaultProps} />);
    const checkbox = screen.getByRole('checkbox');
    fireEvent.click(checkbox);
    expect(screen.getByTestId('field-Allowed Domains')).not.toBeDisabled();
  });

  it('renders TenantAccountCommonSettings component', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByTestId('tenant-account-common-settings')).toBeInTheDocument();
  });

  it('renders Webhook Label Mapping section', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByText('Webhook Label Mapping')).toBeInTheDocument();
  });

  it('renders Feature Flag section', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByText('Feature Flag')).toBeInTheDocument();
  });

  it('renders webhook autocomplete fields', () => {
    render(<TenantSettings {...defaultProps} />);
    expect(screen.getByTestId('autocomplete-Subject Name Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Namespace Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Severity Labels')).toBeInTheDocument();
  });

  it('shows error snackbar when empty allowed domains with checkbox enabled', async () => {
    const { snackbar } = require('@components1/common/snackbarService');
    render(<TenantSettings {...defaultProps} />);

    // Wait for the initial loading effect to complete (Save button becomes enabled)
    await waitFor(() => {
      expect(screen.getByTestId('btn-Save')).not.toBeDisabled();
    });

    // Enable checkbox
    fireEvent.click(screen.getByRole('checkbox'));

    // Click save without filling in allowed domains
    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(snackbar.error).toHaveBeenCalledWith('Allowed Domains field cannot be empty when domain login is enabled.');
    });
  });
});
