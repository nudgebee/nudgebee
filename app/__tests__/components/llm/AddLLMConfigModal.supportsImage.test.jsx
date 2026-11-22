import React from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import AddLLMConfigModal from '@components/llm/AddLLMConfigModal';

const mockAddIntegrations = jest.fn();
const mockListModelPricing = jest.fn();
const mockUpsertModelPricing = jest.fn();

jest.mock('@api1/user', () => ({
  __esModule: true,
  default: { listAccounts: jest.fn().mockResolvedValue([{ id: 'acct-1', account_name: 'Acct', cloud_provider: 'aws' }]) },
}));
jest.mock('@api1/integrations', () => ({
  __esModule: true,
  default: {
    addIntegrations: (...a) => mockAddIntegrations(...a),
    listIntegrations: jest.fn(),
    // A tenant default exists and no account changes, so Save needs no popup.
    checkLLMDefaults: jest.fn().mockResolvedValue({
      env_configured: true,
      tenant_default: { id: 'cfg-1', name: 'cfg' },
      affected: [],
      replaced_account_defaults: [],
      blocked: [],
    }),
  },
}));
jest.mock('@api1/ask-nudgebee', () => ({
  __esModule: true,
  default: {
    listAgents: jest.fn().mockResolvedValue([]),
    listModelPricing: (...a) => mockListModelPricing(...a),
    upsertModelPricing: (...a) => mockUpsertModelPricing(...a),
  },
}));

// The modal is heavy to render; under a parallel run the first test can pass jest's 5s default.
jest.setTimeout(20000);

const MODEL = 'my-vision-model';
const NAME = /supports image input/i;

// CONNECTED skips the Test Connection gate; the defaults check above reports
// nothing to confirm, so Save goes straight to performSave.
const editData = {
  id: 'cfg-1',
  name: 'test-config',
  healthStatus: 'CONNECTED',
  integrations_cloud_accounts: [{ cloud_account_id: 'acct-1' }],
  integration_config_values: [
    { name: 'llm_provider', value: 'custom' },
    { name: 'llm_model_name', value: MODEL },
    { name: 'default_llm_provider', value: 'true' },
    { name: 'llm_provider_api_key', value: '', has_value: true },
  ],
};

const priceRow = (extra = {}) => ({
  provider_name: 'custom',
  model_name: MODEL,
  cost_per_million_input_tokens: 2.5,
  cost_per_million_output_tokens: 10,
  is_built_in: true,
  ...extra,
});

const setup = async (prices = []) => {
  mockListModelPricing.mockResolvedValue({ data: { prices }, errors: [] });
  mockUpsertModelPricing.mockResolvedValue({ data: {}, errors: [] });
  mockAddIntegrations.mockResolvedValue({ data: { data: { integrations_create_config: { configs: [{ id: 'c' }] } } } });
  render(<AddLLMConfigModal open onClose={() => {}} editData={editData} accountId='acct-1' />);
  await userEvent.click(await screen.findByRole('button', { name: /advanced options/i }));
  // The recorded-verdict lookup reads the pricing catalog, which loads async.
  await waitFor(() => expect(mockListModelPricing).toHaveBeenCalled());
  return screen.findByRole('checkbox', { name: NAME });
};

const save = async () => {
  await userEvent.click(screen.getByRole('button', { name: /^save$/i }));
  await waitFor(() => expect(mockAddIntegrations).toHaveBeenCalled());
};

describe('AddLLMConfigModal — supports image input', () => {
  beforeEach(() => jest.clearAllMocks());

  // A custom endpoint has no rate to carry, so the row is written unpriced —
  // which is exactly how it already reports today.
  it('records the verdict for a model with no existing price', async () => {
    const box = await setup([]);
    await userEvent.click(box);
    await save();
    await waitFor(() => expect(mockUpsertModelPricing).toHaveBeenCalledTimes(1));
    expect(mockUpsertModelPricing.mock.calls[0][0][0]).toMatchObject({
      model_name: MODEL,
      provider_name: 'custom',
      supports_image_input: true,
      cost_per_million_input_tokens: 0,
      cost_per_million_output_tokens: 0,
    });
  });

  // The upsert replaces the whole row: sending the 0/0 placeholder would wipe a
  // model's built-in price.
  it('reuses the existing rates instead of zeroing them', async () => {
    const box = await setup([priceRow({ supports_image_input: null })]);
    await userEvent.click(box);
    await save();
    await waitFor(() => expect(mockUpsertModelPricing).toHaveBeenCalledTimes(1));
    expect(mockUpsertModelPricing.mock.calls[0][0][0]).toMatchObject({
      supports_image_input: true,
      cost_per_million_input_tokens: 2.5,
      cost_per_million_output_tokens: 10,
    });
  });

  // The modal never sends token ceilings, and the upsert replaces the row, so
  // ceilings set from Model Pricing would be wiped by ticking the box.
  it("keeps the account's own token ceilings when the row is rewritten", async () => {
    const box = await setup([priceRow({ is_built_in: false, supports_image_input: null, max_output_tokens: 16384, max_context_tokens: 200000 })]);
    await userEvent.click(box);
    await save();
    await waitFor(() => expect(mockUpsertModelPricing).toHaveBeenCalledTimes(1));
    expect(mockUpsertModelPricing.mock.calls[0][0][0]).toMatchObject({ max_output_tokens: 16384, max_context_tokens: 200000 });
  });

  it('does not pin built-in token ceilings into the account row', async () => {
    const box = await setup([priceRow({ supports_image_input: null, max_output_tokens: 16384, max_context_tokens: 200000 })]);
    await userEvent.click(box);
    await save();
    await waitFor(() => expect(mockUpsertModelPricing).toHaveBeenCalledTimes(1));
    const sent = mockUpsertModelPricing.mock.calls[0][0][0];
    expect(sent).not.toHaveProperty('max_output_tokens');
    expect(sent).not.toHaveProperty('max_context_tokens');
  });

  it('writes nothing when the box is left unticked', async () => {
    await setup([priceRow({ supports_image_input: null })]);
    await save();
    expect(mockUpsertModelPricing).not.toHaveBeenCalled();
  });

  it('shows a verdict already on file and does not rewrite it', async () => {
    const box = await setup([priceRow({ supports_image_input: true })]);
    await waitFor(() => expect(box).toBeChecked());
    expect(box).toBeDisabled();
    await save();
    expect(mockUpsertModelPricing).not.toHaveBeenCalled();
  });
});
