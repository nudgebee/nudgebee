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
    // Read during useState initialisation by CustomTable, which the Features tab renders.
    getUserPreferencesTablePageSize: jest.fn(() => 10),
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

// Props-driven stub, keyed on idPrefix: the modal renders this component twice now
// (logs and traces), so the previous fixed data-testid would be ambiguous. It renders
// one input per supplied field — flat, no disclosure, which is the real component's own
// test's job — so the hydrate and save-payload tests can drive the trace mapper without
// mounting the real grid. @shared/settings/labelMapperFields is deliberately NOT mocked,
// so the payload assertions run the real serialiser.
jest.mock('@shared/settings/TenantAccountCommonSettings', () => ({
  __esModule: true,
  default: ({ idPrefix = 'log-label', title, fields = [], advancedFields = [], settings = {}, setSettings, disabled }) => (
    <div data-testid={`common-settings-${idPrefix}`}>
      <span>{title}</span>
      {[...fields, ...advancedFields].map(({ field, label }) => (
        <input
          key={field}
          aria-label={label}
          data-testid={`${idPrefix}-${field}`}
          value={settings[field] || ''}
          disabled={disabled}
          onChange={(e) => {
            const next = e.target.value;
            setSettings((prev) => ({ ...prev, [field]: next }));
          }}
        />
      ))}
    </div>
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

  // The modal is tabbed: General / Label Mapping / Features, with Logs / Traces /
  // Webhook alerts nested under Label Mapping. Anything below the General tab has to be
  // navigated to before it exists in the DOM.
  const openTab = async (...tabNames) => {
    await act(async () => {
      render(<TenantSettings {...defaultProps} />);
    });
    for (const name of tabNames) {
      fireEvent.click(screen.getByRole('tab', { name }));
    }
  };

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

  it('renders the log label mapper on the Label Mapping tab', async () => {
    await openTab('Label Mapping');
    expect(screen.getByTestId('common-settings-log-label')).toBeInTheDocument();
  });

  it('renders Webhook Label Mapping section', async () => {
    await openTab('Label Mapping', 'Webhook alerts');
    expect(screen.getByText(/Map alert label keys to event fields/)).toBeInTheDocument();
  });

  it('renders Feature Flag section', async () => {
    await openTab('Features');
    expect(screen.getByText('Feature Flags')).toBeInTheDocument();
  });

  it('renders webhook autocomplete fields', async () => {
    await openTab('Label Mapping', 'Webhook alerts');
    expect(screen.getByTestId('autocomplete-Subject Name Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Namespace Labels')).toBeInTheDocument();
    expect(screen.getByTestId('autocomplete-Severity Labels')).toBeInTheDocument();
  });

  describe('trace label mapping', () => {
    const { getTenantAttributes, upsertTenantAttributes } = require('@lib/UserService');

    const openTraceTab = () => openTab('Label Mapping', 'Traces');

    const savedAttr = (name) => {
      const call = upsertTenantAttributes.mock.calls[0];
      expect(call).toBeDefined();
      return call[0].find((attr) => attr.name === name);
    };

    const saveAndParseTraceLabels = async () => {
      fireEvent.click(screen.getByTestId('btn-Save'));
      await waitFor(() => expect(upsertTenantAttributes).toHaveBeenCalled());
      const attr = savedAttr('trace_labels');
      expect(attr).toBeDefined();
      return JSON.parse(attr.value);
    };

    it('renders the trace mapper on its own sub-tab', async () => {
      await openTraceTab();
      expect(screen.getByTestId('common-settings-trace-label')).toBeInTheDocument();
      expect(screen.getByText('Trace Label Mapper')).toBeInTheDocument();
    });

    it('hydrates the inputs from a stored trace_labels blob', async () => {
      getTenantAttributes.mockResolvedValueOnce([{ name: 'trace_labels', value: '{"service_name":"k8s.service","trace_id":"traceID"}' }]);
      await openTraceTab();
      expect(screen.getByTestId('trace-label-service_name')).toHaveValue('k8s.service');
      expect(screen.getByTestId('trace-label-trace_id')).toHaveValue('traceID');
    });

    it('saves the edited mapping under the trace_labels attribute', async () => {
      await openTraceTab();
      fireEvent.change(screen.getByTestId('trace-label-service_name'), { target: { value: 'k8s.service' } });
      const saved = await saveAndParseTraceLabels();
      expect(saved.service_name).toBe('k8s.service');
      expect(Object.keys(saved)).toHaveLength(10);
    });

    // Pins the unconditional write. Gating it on "has the operator typed anything" would
    // make clearing the last override a silent no-op.
    it('writes an all-empty mapping from an untouched form', async () => {
      await openTraceTab();
      const saved = await saveAndParseTraceLabels();
      expect(Object.values(saved).every((v) => v === '')).toBe(true);
    });

    it('writes an empty string when a configured field is cleared', async () => {
      getTenantAttributes.mockResolvedValueOnce([{ name: 'trace_labels', value: '{"service_name":"k8s.service"}' }]);
      await openTraceTab();
      fireEvent.change(screen.getByTestId('trace-label-service_name'), { target: { value: '' } });
      expect(await saveAndParseTraceLabels()).toHaveProperty('service_name', '');
    });

    // The SQL-era overrides this screen replaces may carry keys the form does not render.
    // They are working mapping entries, so a save must not silently delete them.
    it('preserves a non-canonical key the form never renders', async () => {
      getTenantAttributes.mockResolvedValueOnce([{ name: 'trace_labels', value: '{"service_name":"k8s.service","my_custom_field":"x"}' }]);
      await openTraceTab();
      const saved = await saveAndParseTraceLabels();
      expect(saved.my_custom_field).toBe('x');
      expect(saved.service_name).toBe('k8s.service');
    });

    it('never writes the dead defaultQuery key', async () => {
      await openTraceTab();
      expect(await saveAndParseTraceLabels()).not.toHaveProperty('defaultQuery');
    });
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

    it('renders the trace label inputs read-only', async () => {
      await act(async () => {
        render(<TenantSettings {...defaultProps} />);
      });
      fireEvent.click(screen.getByRole('tab', { name: 'Label Mapping' }));
      fireEvent.click(screen.getByRole('tab', { name: 'Traces' }));
      expect(screen.getByTestId('trace-label-service_name')).toBeDisabled();
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
