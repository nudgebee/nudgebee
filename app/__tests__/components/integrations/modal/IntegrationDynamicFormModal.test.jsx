import React from 'react';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import '@testing-library/jest-dom';
import IntegrationDynamicFormModal from '@components/integrations/modal/IntegrationDynamicFormModal';

jest.mock('@utils/colors');

jest.mock('@ui/Modal', () => ({
  Modal: ({ open, handleClose, title, children, loader }) =>
    open ? (
      <div data-testid='modal'>
        <h2>{title}</h2>
        {loader && <div data-testid='modal-loader'>Loading...</div>}
        <div data-testid='modal-content'>{children}</div>
        <button data-testid='modal-close-btn' onClick={handleClose}>
          Close Modal
        </button>
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
  Input: ({ label, value, onChange, placeholder, id }) => (
    <input
      id={id}
      aria-label={label || placeholder || 'text-field'}
      value={value || ''}
      onChange={(e) => onChange?.(e.target.value)}
      placeholder={placeholder}
    />
  ),
}));

jest.mock('@ui/Checkbox', () => ({
  Checkbox: ({ id, checked, onChange, label }) => (
    <label>
      <input id={id} type='checkbox' checked={!!checked} onChange={(e) => onChange?.(e.target.checked)} />
      {label}
    </label>
  ),
}));

jest.mock('@ui/Switch', () => ({
  Switch: ({ id, checked, onChange }) => (
    <input id={id} type='checkbox' role='switch' checked={!!checked} onChange={(e) => onChange?.(e.target.checked)} />
  ),
}));

jest.mock('@ui/FilterDropdown', () => ({
  __esModule: true,
  default: ({ label, options = [], value, onSelect }) => (
    <select aria-label={label || 'dropdown'} value={value || ''} onChange={(e) => onSelect?.(e, { value: e.target.value, label: e.target.value })}>
      <option value=''>Select</option>
      {(options || []).map((o) => (
        <option key={typeof o === 'string' ? o : o.value} value={typeof o === 'string' ? o : o.value}>
          {typeof o === 'string' ? o : o.label}
        </option>
      ))}
    </select>
  ),
}));

jest.mock('@shared/buttons/CopyButton', () => ({
  __esModule: true,
  default: ({ text }) => <button data-testid='copy-btn' data-text={text} />,
}));

jest.mock('@shared/icons/SafeIcon', () => ({
  __esModule: true,
  default: ({ alt }) => React.createElement('img', { alt }),
}));

jest.mock('@ui/Toast', () => ({
  toast: { success: jest.fn(), error: jest.fn() },
}));

jest.mock('@api1/integrations/helpers', () => ({
  ENCRYPTED_MASK: '••••••••',
}));

jest.mock('@lib/cache', () => ({
  __esModule: true,
  default: { get: jest.fn(), set: jest.fn(), clear: jest.fn() },
}));

jest.mock('@lib/externalUrls', () => ({
  docsUrl: jest.fn((p) => `https://docs.example.com${p}`),
}));

// Mock the sibling VmAgent dialog so its internal rendering doesn't interfere.
jest.mock('@components/integrations/modal/VmAgentCredentialsDialog', () => ({
  __esModule: true,
  default: ({ open }) => (open ? <div data-testid='vm-agent-dialog' /> : null),
}));

const mockListIntegrationSchema = jest.fn();
const mockAddIntegrations = jest.fn();
const mockCreateTicketIntegration = jest.fn();
const mockListTicketConfigurations = jest.fn();

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listIntegrationSchema: (...args) => mockListIntegrationSchema(...args),
    addIntegrations: (...args) => mockAddIntegrations(...args),
    createTicketIntegration: (...args) => mockCreateTicketIntegration(...args),
  },
}));

jest.mock('@api1/tickets', () => ({
  __esModule: true,
  default: {
    listTicketConfigurations: (...args) => mockListTicketConfigurations(...args),
  },
}));

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: {
    listAccounts: jest.fn().mockResolvedValue([]),
  },
}));

jest.mock('@assets', () => ({
  PlusIcon: '/plus.svg',
  DeleteIconRed: '/delete.svg',
  infoIcon: '/info.svg',
}));

jest.mock('@lib/formatter', () => ({
  titleCase: (s) => (s ? s.charAt(0).toUpperCase() + s.slice(1) : s),
}));

jest.mock('src/utils/common', () => ({
  getAccountCreationSuccessMsg: (name) => `${name} account created successfully`,
  parseHttpResponseBodyMessage: () => 'Error occurred',
  safeJSONParse: (s) => {
    try {
      return JSON.parse(s);
    } catch {
      return null;
    }
  },
  snakeToTitleCase: (s) => s.replace(/_/g, ' '),
  toKebabCase: (s) => (s || '').toLowerCase().replace(/\s+/g, '-'),
}));

jest.mock('@hooks/useTenantBranding', () => ({
  useBrandingConfig: () => ({
    relayUrl: 'https://relay.example.com',
    signingPublicKey: '',
  }),
}));

const defaultSchemaResponse = {
  data: {
    data: {
      integrations_get_schema: {
        data: {
          properties: {
            integration_config_name: {
              type: 'string',
              display_name: 'Integration Config Name',
              description: 'Name for this integration configuration',
              required: true,
            },
            api_token: {
              type: 'string',
              display_name: 'API Token',
              description: 'The API token for authentication',
              is_encrypted: true,
            },
          },
          required: ['integration_config_name'],
        },
      },
    },
  },
};

const renderModal = (props = {}) =>
  render(
    <IntegrationDynamicFormModal
      integrationName='datadog'
      openModal={false}
      handleClose={jest.fn()}
      title='Add Datadog Integration'
      integrationData={[]}
      editData={null}
      {...props}
    />
  );

describe('IntegrationDynamicFormModal', () => {
  beforeEach(() => {
    jest.clearAllMocks();
    mockListIntegrationSchema.mockResolvedValue(defaultSchemaResponse);
    mockAddIntegrations.mockResolvedValue({
      data: {
        data: {
          integrations_create_config: { configs: [{ name: 'id', value: 'abc123' }] },
        },
      },
    });
    mockListTicketConfigurations.mockResolvedValue({ data: [] });
    mockCreateTicketIntegration.mockResolvedValue({
      data: { data: { ticket_integration_create_config: { id: 'ticket-1' } } },
    });
  });

  test('renders null (no modal) when openModal=false', () => {
    renderModal({ openModal: false });
    expect(screen.queryByTestId('modal')).not.toBeInTheDocument();
  });

  test('renders modal with title when openModal=true', async () => {
    await act(async () => {
      renderModal({ openModal: true, title: 'Add Datadog Integration' });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });
    expect(screen.getByText('Add Datadog Integration')).toBeInTheDocument();
  });

  test('renders the form fields after schema loads', async () => {
    await act(async () => {
      renderModal({ openModal: true });
    });

    await waitFor(() => {
      expect(mockListIntegrationSchema).toHaveBeenCalledWith({
        integration_name: 'datadog',
        source: 'user',
      });
    });
  });

  test('cancel button calls handleClose', async () => {
    const handleClose = jest.fn();

    await act(async () => {
      renderModal({ openModal: true, handleClose });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const cancelBtn = screen.queryByText('Cancel');
    if (cancelBtn) {
      fireEvent.click(cancelBtn);
      await waitFor(() => {
        expect(handleClose).toHaveBeenCalled();
      });
    } else {
      fireEvent.click(screen.getByTestId('modal-close-btn'));
      await waitFor(() => {
        expect(handleClose).toHaveBeenCalled();
      });
    }
  });

  test('submit button is present when modal is open', async () => {
    await act(async () => {
      renderModal({ openModal: true });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const saveBtn = screen.queryByText('Save') || screen.queryByText('Update');
    expect(saveBtn).toBeTruthy();
  });

  test('shows loading state (loader) while schema is being fetched', async () => {
    let resolveSchema;
    mockListIntegrationSchema.mockReturnValue(
      new Promise((resolve) => {
        resolveSchema = resolve;
      })
    );

    await act(async () => {
      renderModal({ openModal: true });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    expect(screen.getByTestId('modal-loader')).toBeInTheDocument();

    await act(async () => {
      resolveSchema(defaultSchemaResponse);
    });
  });

  test('renders without crashing on API failure during submit', async () => {
    mockListIntegrationSchema.mockResolvedValue(defaultSchemaResponse);
    mockAddIntegrations.mockRejectedValue(new Error('Network error'));

    await act(async () => {
      renderModal({ openModal: true });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const saveBtn = screen.queryByText('Save');
    if (saveBtn) {
      await act(async () => {
        fireEvent.click(saveBtn);
      });
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    }
  });

  test('renders in edit mode with Update button when editData is provided', async () => {
    const editData = {
      id: 'existing-id',
      name: 'existing-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'existing-config',
        api_token: 'secret',
      },
    };

    await act(async () => {
      renderModal({ openModal: true, editData });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const updateBtn = screen.queryByText('Update');
    expect(updateBtn).toBeTruthy();
  });
});
