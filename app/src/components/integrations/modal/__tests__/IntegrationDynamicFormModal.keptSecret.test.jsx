import React from 'react';
import { render, screen, waitFor, fireEvent } from '@testing-library/react';
import IntegrationDynamicFormModal from '../IntegrationDynamicFormModal';
import { savedConfigFlags } from '@components/integrations/form/schemaForm';
import { ENCRYPTED_MASK } from '@api1/integrations/helpers';
import apiIntegrations from '@api1/integrations';

// Edit an integration and save without touching its secret. integrations_list
// blanks secret values and sends has_value, so the form has to mask the field
// from the flag and send is_encrypted=true with no value, which the server
// resolves to the stored secret by field name.

jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    listIntegrationSchema: jest.fn(),
    addIntegrations: jest.fn(),
  },
}));
jest.mock('@api1/user', () => ({ __esModule: true, default: {} }));
jest.mock('@api1/tickets', () => ({ __esModule: true, default: {} }));
jest.mock('@api1/kubernetes', () => ({ __esModule: true, default: {} }));
jest.mock('@api1/observability', () => ({ __esModule: true, default: {} }));
jest.mock('@hooks/useTenantBranding', () => ({ getBrandTitle: () => 'Brand', useBrandingConfig: () => ({}) }));
jest.mock('../VmAgentCredentialsDialog', () => () => null);
jest.mock('@ui/Toast', () => ({ toast: { success: jest.fn(), error: jest.fn(), info: jest.fn(), warning: jest.fn() } }));

const schema = {
  type: 'object',
  properties: {
    url: { type: 'string', display_name: 'URL', description: 'Endpoint', priority: 2 },
    password: { type: 'string', display_name: 'Password', description: 'Password', is_encrypted: true, priority: 1 },
  },
};

// A list row as integrations_list returns it after redaction, flattened the way
// ListIntegrations hands it to the form.
const listRow = {
  id: 'int-1',
  name: 'my-integration',
  source: 'user',
  integrations_cloud_accounts: [],
  integration_config_values: [
    { name: 'url', value: 'https://example.test', has_value: true },
    { name: 'password', value: '', has_value: true },
  ],
};
const editData = {
  ...listRow,
  integration_config_values: { url: 'https://example.test', password: '', integration_config_name: 'my-integration' },
  integration_config_has_value: savedConfigFlags(listRow.integration_config_values),
};

beforeEach(() => {
  jest.clearAllMocks();
  apiIntegrations.listIntegrationSchema.mockResolvedValue({ data: { data: { integrations_get_schema: { data: schema } } } });
  apiIntegrations.addIntegrations.mockResolvedValue({ data: { data: { integrations_create_config: { configs: [{ name: 'url', value: 'x' }] } } } });
});

const renderModal = (data) =>
  render(<IntegrationDynamicFormModal integrationName='newrelic' openModal handleClose={jest.fn()} title='Edit' editData={data} />);

it('masks a saved secret whose value the list blanked, and keeps it on save', async () => {
  renderModal(editData);

  await waitFor(() => expect(screen.getByDisplayValue(ENCRYPTED_MASK)).toBeInTheDocument());
  fireEvent.click(screen.getByRole('button', { name: 'Save Webhook' }));

  await waitFor(() => expect(apiIntegrations.addIntegrations).toHaveBeenCalledTimes(1));
  const payload = apiIntegrations.addIntegrations.mock.calls[0][0];
  expect(payload.integration_id).toBe('int-1');
  expect(payload.integration_config_values).toEqual(
    expect.arrayContaining([
      { name: 'password', value: '', is_encrypted: true },
      { name: 'url', value: 'https://example.test', is_encrypted: false },
    ])
  );
});

it('does not mask a secret that has no saved value', async () => {
  renderModal({ ...editData, integration_config_has_value: { url: true, password: false } });

  await waitFor(() => expect(screen.getByDisplayValue('https://example.test')).toBeInTheDocument());
  expect(screen.queryByDisplayValue(ENCRYPTED_MASK)).not.toBeInTheDocument();
});
