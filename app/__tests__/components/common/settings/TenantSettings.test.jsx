import React from 'react';
import { act, render, screen, fireEvent, waitFor } from '@testing-library/react';
import TenantSettings from '@shared/settings/TenantSettings';

jest.mock('@utils/colors');

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
  updateTenantName: jest.fn().mockResolvedValue({ data: {} }),
}));

jest.mock('@lib/auth', () => ({
  fetchFeatureFlagsForTenant: jest.fn().mockResolvedValue([]),
  // Default to the editable persona so the existing assertions (Save button,
  // enabled fields) keep describing a tenant admin. The read-only persona is
  // covered by its own describe block below.
  canEditTenantSettings: jest.fn(() => true),
  missingPermissionMessage: jest.fn((perm) => `You need the "${perm}" permission. Ask an admin to grant it.`),
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listUserTenants: jest.fn().mockResolvedValue({ data: [{ name: 'TestTenant' }] }),
  },
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
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

jest.mock('@ui/Modal', () => ({
  __esModule: true,
  Modal: ({ open, children, title }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {children}
      </div>
    ) : null,
}));

jest.mock('@shared/settings/TenantAccountCommonSettings', () => ({
  __esModule: true,
  default: ({ logSettings: _logSettings, setLogSettings: _setLogSettings }) => (
    <div data-testid='tenant-account-common-settings'>Log Label Mapper</div>
  ),
}));

jest.mock('@ui/Input', () => ({
  __esModule: true,
  Input: ({ label, value, onChange, disabled, placeholder }) => (
    <div>
      <label htmlFor={`field-${label}`}>{label}</label>
      <input
        id={`field-${label}`}
        data-testid={`field-${label}`}
        value={value || ''}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        placeholder={placeholder}
      />
    </div>
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  __esModule: true,
  Checkbox: ({ label, checked, onChange }) => (
    <label>
      <input type='checkbox' checked={checked} onChange={onChange} />
      {label}
    </label>
  ),
}));

jest.mock('@ui/Button', () => ({
  __esModule: true,
  Button: ({ children, onClick, disabled }) => (
    <button onClick={onClick} disabled={disabled} data-testid={`btn-${children}`}>
      {children}
    </button>
  ),
}));

jest.mock('@ui/Card', () => ({
  __esModule: true,
  Card: ({ children, header }) => (
    <div>
      {header}
      {children}
    </div>
  ),
}));

jest.mock('@ui/FilterDropdown', () => ({
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

  it('renders modal when open is true', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('modal')).toBeInTheDocument();
  });

  it('does not render modal when open is false', () => {
    render(<TenantSettings {...defaultProps} open={false} />);
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  it('renders the modal title', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByText('Tenant Settings')).toBeInTheDocument();
  });

  it('renders Tenant Name field', async () => {
    render(<TenantSettings {...defaultProps} />);
    await waitFor(() => {
      expect(screen.getByTestId('field-Tenant Name')).toBeInTheDocument();
    });
  });

  it('renders Save and Cancel buttons', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('btn-Save')).toBeInTheDocument();
    expect(screen.getByTestId('btn-Cancel')).toBeInTheDocument();
  });

  it('calls onClose when Cancel button is clicked', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    fireEvent.click(screen.getByTestId('btn-Cancel'));
    expect(defaultProps.onClose).toHaveBeenCalledWith(null, 'hide');
  });

  it('renders domain login checkbox', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByText('Allow self-onboarding via domain login')).toBeInTheDocument();
  });

  it('renders Allowed Domains field (disabled by default)', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('field-Allowed Domains')).toBeDisabled();
  });

  it('renders Default Auth Role field (disabled by default)', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('field-Default Auth Role')).toBeDisabled();
  });

  it('enables Allowed Domains field after checking the checkbox', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    fireEvent.click(screen.getByRole('checkbox'));
    expect(screen.getByTestId('field-Allowed Domains')).not.toBeDisabled();
  });

  it('renders TenantAccountCommonSettings component', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('tenant-account-common-settings')).toBeInTheDocument();
  });

  it('renders Webhook Label Mapping section', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByText('Webhook Label Mapping')).toBeInTheDocument();
  });

  it('renders Feature Flag section', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByText('Feature Flags')).toBeInTheDocument();
  });

  it('renders webhook autocomplete fields', async () => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    expect(screen.getByTestId('autocomplete-Subject Name Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Namespace Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Severity Labels')).toBeInTheDocument();
  });

  it('shows error snackbar when empty allowed domains with checkbox enabled', async () => {
    const { toast } = require('@ui/Toast');
    render(<TenantSettings {...defaultProps} />);

    await waitFor(() => {
      expect(screen.getByTestId('btn-Save')).not.toBeDisabled();
    });

    fireEvent.click(screen.getByRole('checkbox'));
    fireEvent.click(screen.getByTestId('btn-Save'));

    await waitFor(() => {
      expect(toast.error).toHaveBeenCalledWith('Allowed Domains field cannot be empty when domain login is enabled.');
    });
  });

  describe('read-only viewer (tenants:Read, no write grant)', () => {
    const { canEditTenantSettings } = require('@lib/auth');

    beforeEach(() => canEditTenantSettings.mockReturnValue(false));
    afterEach(() => canEditTenantSettings.mockReturnValue(true));

    it('drops Save and offers Close instead of Cancel', async () => {
      await act(async () => {
        render(<TenantSettings {...defaultProps} />);
      });
      expect(screen.queryByTestId('btn-Save')).not.toBeInTheDocument();
      expect(screen.getByTestId('btn-Close')).toBeInTheDocument();
    });

    it('explains why, naming the grant to ask for', async () => {
      await act(async () => {
        render(<TenantSettings {...defaultProps} />);
      });
      expect(screen.getByText(/You need the "tenants:Write" permission/)).toBeInTheDocument();
    });

    it('renders the tenant name field read-only', async () => {
      await act(async () => {
        render(<TenantSettings {...defaultProps} />);
      });
      // The whole form is inert for a viewer, so the backend write gate is never
      // reached from the UI — Tenant Name stands in for every field here.
      const input = screen.getByLabelText(/Tenant Name/i);
      expect(input).toBeDisabled();
    });
  });
});
