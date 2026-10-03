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
  default: ({ label, options = [], value, onSelect }) => {
    const opts = (Array.isArray(options) ? options : []).map((o) => (typeof o === 'string' ? { value: o, label: o } : o));
    // freeSolo: the real control displays a value that is not in the option list —
    // a saved filter column the backend probe did not return, which is exactly the
    // state an edit form opens in. A <select> can only show what it has an option
    // for, so give it one. Primitives only: this same component is used multi-select
    // with an array value, and unshifting that would render an array as an <option>.
    const isPrimitive = typeof value === 'string' || typeof value === 'number';
    if (isPrimitive && value !== '' && !opts.some((o) => o.value === value)) {
      opts.unshift({ value, label: value });
    }
    // Multi-select with saved values the option list doesn't carry yet (e.g.
    // page IDs before the backend picker answers) — same reason as above.
    const isMulti = Array.isArray(value);
    if (isMulti) {
      value.filter((v) => !opts.some((o) => o.value === v)).forEach((v) => opts.unshift({ value: v, label: v }));
    }
    return (
      <select
        aria-label={label || 'dropdown'}
        multiple={isMulti}
        value={isMulti ? value : value || ''}
        onChange={(e) =>
          isMulti
            ? onSelect?.(
                e,
                Array.from(e.target.selectedOptions).map((o) => o.value)
              )
            : onSelect?.(e, { value: e.target.value, label: e.target.value })
        }
      >
        <option value=''>Select</option>
        {opts.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    );
  },
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
const mockGetAutogenOptions = jest.fn();

const mockListIntegrations = jest.fn();
jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listIntegrations: (...args) => mockListIntegrations(...args),
    listIntegrationSchema: (...args) => mockListIntegrationSchema(...args),
    addIntegrations: (...args) => mockAddIntegrations(...args),
    createTicketIntegration: (...args) => mockCreateTicketIntegration(...args),
    listESIndexes: jest.fn().mockResolvedValue({ indexes: [] }),
    getAutogenOptions: (...args) => mockGetAutogenOptions(...args),
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
  // The modal white-labels its help text through this. The mock predates the export,
  // and only started biting once a section the suite renders began calling it.
  getBrandTitle: () => 'Nudgebee',
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

  // ----- alert delivery for a directly connected Prometheus -------------------

  const webhookRows = (rows) => ({ data: { data: { integrations_list: { rows } } } });
  const directPrometheusEdit = {
    id: 'prom-1',
    name: 'grafana-cloud',
    source: 'user',
    integrations_cloud_accounts: [{ cloud_account_id: 'acc-1', cloud_account_name: 'acme-prod' }],
    integration_config_values: { integration_config_name: 'grafana-cloud' },
  };

  // Without an agent in the cluster, alerts reach Nudgebee only through the public
  // Alertmanager webhook. The receiver URL is the one thing the operator cannot
  // work out from this form, so the edit view shows it — with the account name
  // pinned as the cluster, the value an agent-delivered alert would carry.
  test('shows the Alertmanager receiver URL for a directly connected Prometheus', async () => {
    mockListIntegrations.mockResolvedValue(
      webhookRows([
        {
          id: 'wh-1',
          name: 'prod-alertmanager',
          type: 'prometheus_alertmanager_webhook',
          integrations_cloud_accounts: JSON.stringify([{ cloud_account_id: 'acc-1', cloud_account_name: 'acme-prod' }]),
          integration_config_values: JSON.stringify([{ name: 'token', value: 'tok-123' }]),
        },
        {
          id: 'wh-2',
          name: 'other-account-webhook',
          type: 'prometheus_alertmanager_webhook',
          integrations_cloud_accounts: JSON.stringify([{ cloud_account_id: 'acc-9', cloud_account_name: 'other' }]),
          integration_config_values: JSON.stringify([{ name: 'token', value: 'tok-999' }]),
        },
      ])
    );

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'prometheus', title: 'Edit Prometheus', editData: directPrometheusEdit });
    });

    await waitFor(() => {
      expect(screen.getByTestId('prometheus-alert-delivery-url')).toBeInTheDocument();
    });
    expect(mockListIntegrations).toHaveBeenCalledWith(expect.objectContaining({ type: 'prometheus_alertmanager_webhook' }));
    expect(screen.getAllByTestId('prometheus-alert-delivery-url')).toHaveLength(1);
    expect(screen.getByTestId('prometheus-alert-delivery-url')).toHaveTextContent(
      '/api/webhooks/prometheus-alertmanager?token=tok-123&cluster=acme-prod'
    );
  });

  test('points at creating the webhook integration when none is linked to the account', async () => {
    mockListIntegrations.mockResolvedValue(webhookRows([]));

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'prometheus', title: 'Edit Prometheus', editData: directPrometheusEdit });
    });

    await waitFor(() => {
      expect(screen.getByTestId('prometheus-alert-delivery-missing')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('prometheus-alert-delivery-url')).not.toBeInTheDocument();
  });

  // The agent-created prometheus row has a runner receiving Alertmanager in-cluster;
  // the webhook URL would be wrong advice there.
  test('does not show alert delivery for the agent-created Prometheus row', async () => {
    await act(async () => {
      renderModal({
        openModal: true,
        integrationName: 'prometheus',
        title: 'Edit Prometheus',
        editData: { ...directPrometheusEdit, source: 'agent' },
      });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });
    expect(screen.queryByTestId('prometheus-alert-delivery')).not.toBeInTheDocument();
    expect(mockListIntegrations).not.toHaveBeenCalled();
  });

  // Advanced Settings used to show Per-Account Index but NOT Default Log Filters for
  // Elasticsearch, while every other log integration showed the filter editor. The
  // backend applies a saved default_filters value provider-agnostically in FetchLogs,
  // so an ES filter was enforced but uneditable. Both names the modal treats as the
  // ES log integration are covered.
  test.each(['ES', 'elasticsearch'])('shows Default Log Filters alongside Per-Account Index for %s', async (integrationName) => {
    await act(async () => {
      renderModal({ openModal: true, integrationName, title: 'Add ES Integration' });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByText('Default Log Filters (Optional)')).toBeInTheDocument();
    expect(screen.getByText('Per-Account Index (Optional)')).toBeInTheDocument();
  });

  test('hydrates a saved ES default_filters config into the filter editor', async () => {
    const editData = {
      id: 'es-1',
      name: 'es-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'es-config',
        default_filters: JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'cluster_id', op: '_eq', value: 'nudgebee' }] }]),
      },
    };

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'ES', editData });
    });

    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByDisplayValue('cluster_id')).toBeInTheDocument();
    expect(screen.getByDisplayValue('nudgebee')).toBeInTheDocument();
  });

  // --- Default Trace Filters (#37403) ---
  // The trace card is a SEPARATE config value from the log one on purpose: datadog,
  // dynatrace, chronosphere and ES are each one integration record serving both logs
  // and traces, so a shared list would apply log filters to trace queries.
  test('shows Default Trace Filters for a trace integration, alongside the log card', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', title: 'Add Datadog Integration' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByText('Default Log Filters (Optional)')).toBeInTheDocument();
    expect(screen.getByText('Default Trace Filters (Optional)')).toBeInTheDocument();
  });

  // signoz has no trace source in getTraceSource, so offering a trace filter there
  // would save a config nothing ever reads.
  test('hides Default Trace Filters for a log-only integration (signoz)', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'signoz', title: 'Add Signoz Integration' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByText('Default Log Filters (Optional)')).toBeInTheDocument();
    expect(screen.queryByText('Default Trace Filters (Optional)')).not.toBeInTheDocument();
  });

  // otel_clickhouse is trace-only — and is the agent trace provider, NOT the
  // unrelated 'clickhouse' database integration.
  test('shows only Default Trace Filters for otel_clickhouse', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'otel_clickhouse', title: 'Add ClickHouse Traces' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByText('Default Trace Filters (Optional)')).toBeInTheDocument();
    expect(screen.queryByText('Default Log Filters (Optional)')).not.toBeInTheDocument();
  });

  test('hydrates a saved default_trace_filters config into the trace editor', async () => {
    const editData = {
      id: 'dd-1',
      name: 'dd-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'dd-config',
        default_trace_filters: JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'workload_namespace', op: '_eq', value: 'production' }] }]),
      },
    };

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', editData });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    expect(screen.getByDisplayValue('workload_namespace')).toBeInTheDocument();
    expect(screen.getByDisplayValue('production')).toBeInTheDocument();
  });

  // The two configs are independent: a record carrying only log filters must not
  // spill them into the trace card, which is what a shared config name would do.
  test('a saved default_filters value does not populate the trace card', async () => {
    const editData = {
      id: 'dd-2',
      name: 'dd-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'dd-config',
        default_filters: JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'cluster_id', op: '_eq', value: 'nudgebee' }] }]),
      },
    };

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', editData });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      fireEvent.click(screen.getByTestId('advanced-settings-toggle'));
    });

    // The log card holds it...
    expect(screen.getByDisplayValue('cluster_id')).toBeInTheDocument();
    // ...and the trace card is untouched: exactly one blank card, no rows carried over.
    expect(screen.getByTestId('default-trace-filter-card-0')).toBeInTheDocument();
    expect(screen.queryByTestId('default-trace-filter-card-1')).not.toBeInTheDocument();
    expect(screen.queryAllByDisplayValue('cluster_id')).toHaveLength(1);
  });
});

// A schema property flagged `advanced` is rendered by the same picker branch as
// any other autogen field, but inside the collapsed Advanced Settings section;
// an array-typed one collects chips and round-trips as a comma-joined string.
describe('schema-driven advanced fields', () => {
  const confluenceSchema = {
    data: {
      data: {
        integrations_get_schema: {
          data: {
            properties: {
              integration_config_name: { type: 'string', display_name: 'Integration Config Name', description: 'Name', required: true },
              host: { type: 'string', description: 'Confluence base URL' },
              namespace: { type: 'string', description: 'Confluence namespace' },
              page_trees: {
                type: 'array',
                description: 'Index only these pages and everything beneath them.',
                auto_generate_func: 'listConfluencePages',
                depends_on: ['host', 'namespace', 'page_trees'],
                advanced: true,
              },
            },
            required: ['integration_config_name'],
          },
        },
      },
    },
  };

  beforeEach(() => {
    jest.clearAllMocks();
    mockListIntegrationSchema.mockResolvedValue(confluenceSchema);
    mockGetAutogenOptions.mockResolvedValue({ options: [], message: '' });
    mockAddIntegrations.mockResolvedValue({
      data: { data: { integrations_create_config: { configs: [{ name: 'id', value: 'conf-1' }] } } },
    });
  });

  test('renders an advanced array field under the Advanced Settings toggle, with its description kept for the tooltip', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'confluence', title: 'Add Confluence Integration' });
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    expect(screen.getByLabelText('host')).toBeInTheDocument();
    expect(screen.getByTestId('advanced-settings-toggle')).toBeInTheDocument();
    expect(screen.getByLabelText('page trees')).toBeInTheDocument();
    expect(screen.queryByText('Index only these pages and everything beneath them.')).not.toBeInTheDocument();
  });

  test('hydrates a saved comma-joined value into chips and submits it re-joined', async () => {
    const editData = {
      id: 'conf-1',
      name: 'conf',
      source: 'user',
      integration_config_values: { integration_config_name: 'conf', host: 'https://wiki.example.com', page_trees: '100,200' },
    };
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'confluence', editData });
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const picker = screen.getByLabelText('page trees');
    expect(
      Array.from(picker.selectedOptions)
        .map((o) => o.value)
        .sort()
    ).toEqual(['100', '200']);

    await act(async () => {
      fireEvent.click(screen.getByText('Update'));
    });

    await waitFor(() => {
      expect(mockAddIntegrations).toHaveBeenCalled();
    });
    const payload = mockAddIntegrations.mock.calls[0][0];
    expect(payload.integration_id).toBe('conf-1');
    expect(payload.integration_config_values).toContainEqual({ name: 'page_trees', value: '100,200', is_encrypted: false });
  });

  // The picker returns no suggestions here, which is what an edit form does
  // when its secret was not re-typed. Removal must not depend on the dropdown
  // being able to list the entry, or the only way out is clearing them all.
  test('removes one saved entry without dropping the rest, even with no suggestions loaded', async () => {
    const editData = {
      id: 'conf-1',
      name: 'conf',
      source: 'user',
      integration_config_values: { integration_config_name: 'conf', host: 'https://wiki.example.com', page_trees: '100,200' },
    };
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'confluence', editData });
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    expect(screen.getByTestId('page_trees-remove-100')).toBeInTheDocument();
    await act(async () => {
      fireEvent.click(screen.getByTestId('page_trees-remove-100'));
    });

    expect(screen.queryByTestId('page_trees-remove-100')).not.toBeInTheDocument();
    expect(screen.getByTestId('page_trees-remove-200')).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByText('Update'));
    });
    await waitFor(() => {
      expect(mockAddIntegrations).toHaveBeenCalled();
    });
    const payload = mockAddIntegrations.mock.calls[0][0];
    expect(payload.integration_config_values).toContainEqual({ name: 'page_trees', value: '200', is_encrypted: false });
  });
});

// A schema property flagged `advanced` is rendered by the same picker branch as
// any other autogen field, but inside the collapsed Advanced Settings section;
// an array-typed one collects chips and round-trips as a comma-joined string.
describe('schema-driven advanced fields', () => {
  const confluenceSchema = {
    data: {
      data: {
        integrations_get_schema: {
          data: {
            properties: {
              integration_config_name: { type: 'string', display_name: 'Integration Config Name', description: 'Name', required: true },
              host: { type: 'string', description: 'Confluence base URL' },
              namespace: { type: 'string', description: 'Confluence namespace' },
              page_trees: {
                type: 'array',
                description: 'Index only these pages and everything beneath them.',
                auto_generate_func: 'listConfluencePages',
                depends_on: ['host', 'namespace', 'page_trees'],
                advanced: true,
              },
            },
            required: ['integration_config_name'],
          },
        },
      },
    },
  };

  beforeEach(() => {
    jest.clearAllMocks();
    mockListIntegrationSchema.mockResolvedValue(confluenceSchema);
    mockGetAutogenOptions.mockResolvedValue({ options: [], message: '' });
    mockAddIntegrations.mockResolvedValue({
      data: { data: { integrations_create_config: { configs: [{ name: 'id', value: 'conf-1' }] } } },
    });
  });

  test('renders an advanced array field under the Advanced Settings toggle, with its description kept for the tooltip', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'confluence', title: 'Add Confluence Integration' });
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    expect(screen.getByLabelText('host')).toBeInTheDocument();
    expect(screen.getByTestId('advanced-settings-toggle')).toBeInTheDocument();
    expect(screen.getByLabelText('page trees')).toBeInTheDocument();
    expect(screen.queryByText('Index only these pages and everything beneath them.')).not.toBeInTheDocument();
  });

  test('hydrates a saved comma-joined value into chips and submits it re-joined', async () => {
    const editData = {
      id: 'conf-1',
      name: 'conf',
      source: 'user',
      integration_config_values: { integration_config_name: 'conf', host: 'https://wiki.example.com', page_trees: '100,200' },
    };
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'confluence', editData });
    });
    await waitFor(() => {
      expect(screen.getByTestId('modal')).toBeInTheDocument();
    });

    const picker = screen.getByLabelText('page trees');
    expect(
      Array.from(picker.selectedOptions)
        .map((o) => o.value)
        .sort()
    ).toEqual(['100', '200']);

    await act(async () => {
      fireEvent.click(screen.getByText('Update'));
    });

    await waitFor(() => {
      expect(mockAddIntegrations).toHaveBeenCalled();
    });
    const payload = mockAddIntegrations.mock.calls[0][0];
    expect(payload.integration_id).toBe('conf-1');
    expect(payload.integration_config_values).toContainEqual({ name: 'page_trees', value: '100,200', is_encrypted: false });
  });

  // --- Default Trace Filters (#37403) ---
  // The trace card is a SEPARATE config value from the log one on purpose: datadog,
  // dynatrace, chronosphere and ES are each one integration record serving both logs
  // and traces, so a shared list would apply log filters to trace queries.
  test('shows Default Trace Filters for a trace integration, alongside the log card', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', title: 'Add Datadog Integration' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      // OSS renders the shared Advanced Settings toggle in more than one section;
      // all of them drive the same collapse.
      fireEvent.click(screen.getAllByTestId('advanced-settings-toggle')[0]);
    });

    expect(screen.getByText('Default Log Filters (Optional)')).toBeInTheDocument();
    expect(screen.getByText('Default Trace Filters (Optional)')).toBeInTheDocument();
  });

  // signoz has no trace source in getTraceSource, so offering a trace filter there
  // would save a config nothing ever reads.
  test('hides Default Trace Filters for a log-only integration (signoz)', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'signoz', title: 'Add Signoz Integration' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      // OSS renders the shared Advanced Settings toggle in more than one section;
      // all of them drive the same collapse.
      fireEvent.click(screen.getAllByTestId('advanced-settings-toggle')[0]);
    });

    expect(screen.getByText('Default Log Filters (Optional)')).toBeInTheDocument();
    expect(screen.queryByText('Default Trace Filters (Optional)')).not.toBeInTheDocument();
  });

  // otel_clickhouse is trace-only — and is the agent trace provider, NOT the
  // unrelated 'clickhouse' database integration.
  test('shows only Default Trace Filters for otel_clickhouse', async () => {
    await act(async () => {
      renderModal({ openModal: true, integrationName: 'otel_clickhouse', title: 'Add ClickHouse Traces' });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      // OSS renders the shared Advanced Settings toggle in more than one section;
      // all of them drive the same collapse.
      fireEvent.click(screen.getAllByTestId('advanced-settings-toggle')[0]);
    });

    expect(screen.getByText('Default Trace Filters (Optional)')).toBeInTheDocument();
    expect(screen.queryByText('Default Log Filters (Optional)')).not.toBeInTheDocument();
  });

  test('hydrates a saved default_trace_filters config into the trace editor', async () => {
    const editData = {
      id: 'dd-1',
      name: 'dd-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'dd-config',
        default_trace_filters: JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'workload_namespace', op: '_eq', value: 'production' }] }]),
      },
    };

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', editData });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      // OSS renders the shared Advanced Settings toggle in more than one section;
      // all of them drive the same collapse.
      fireEvent.click(screen.getAllByTestId('advanced-settings-toggle')[0]);
    });

    expect(screen.getByDisplayValue('workload_namespace')).toBeInTheDocument();
    expect(screen.getByDisplayValue('production')).toBeInTheDocument();
  });

  // The two configs are independent: a record carrying only log filters must not
  // spill them into the trace card, which is what a shared config name would do.
  test('a saved default_filters value does not populate the trace card', async () => {
    const editData = {
      id: 'dd-2',
      name: 'dd-config',
      source: 'user',
      integration_config_values: {
        integration_config_name: 'dd-config',
        default_filters: JSON.stringify([{ accountId: 'acc-1', filters: [{ key: 'cluster_id', op: '_eq', value: 'nudgebee' }] }]),
      },
    };

    await act(async () => {
      renderModal({ openModal: true, integrationName: 'datadog', editData });
    });
    await waitFor(() => expect(screen.getByTestId('modal')).toBeInTheDocument());

    await act(async () => {
      // OSS renders the shared Advanced Settings toggle in more than one section;
      // all of them drive the same collapse.
      fireEvent.click(screen.getAllByTestId('advanced-settings-toggle')[0]);
    });

    // The log card holds it...
    expect(screen.getByDisplayValue('cluster_id')).toBeInTheDocument();
    // ...and the trace card is untouched: exactly one blank card, no rows carried over.
    expect(screen.getByTestId('default-trace-filter-card-0')).toBeInTheDocument();
    expect(screen.queryByTestId('default-trace-filter-card-1')).not.toBeInTheDocument();
    expect(screen.queryAllByDisplayValue('cluster_id')).toHaveLength(1);
  });
});
